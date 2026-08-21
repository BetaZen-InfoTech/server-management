// Package handlers — ProgrammaticHandler exposes the customer-facing
// /api/v1/external/* surface used by API-token bearers. The handler is a
// thin wrapper around the existing services so behaviour, validation, and
// tenant scoping stay identical to the JWT-authenticated panel surface.
//
// All routes go through middleware.APITokenAuth which sets a CallerScope on
// the request context; downstream services apply that scope automatically.
// Per-route scope checks are wired via middleware.RequireTokenScope.
package handlers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type ProgrammaticHandler struct {
	domains    *services.DomainService
	emails     *services.EmailService
	ssl        *services.SSLService
	projects   *services.ProjectService
	guest      *services.GuestLinkService
	cloudflare *services.CloudflareService
}

func NewProgrammaticHandler(d *services.DomainService, e *services.EmailService, s *services.SSLService, p *services.ProjectService, g *services.GuestLinkService) *ProgrammaticHandler {
	return &ProgrammaticHandler{domains: d, emails: e, ssl: s, projects: p, guest: g}
}

// SetCloudflareService wires the Cloudflare service so the external API can
// expose connect + nameserver retrieval. Kept a setter so the constructor
// signature stays stable.
func (h *ProgrammaticHandler) SetCloudflareService(cf *services.CloudflareService) { h.cloudflare = cf }

// Domains -----------------------------------------------------------------

func (h *ProgrammaticHandler) ListDomains(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 50)
	domains, total, err := h.domains.List(c.UserContext(), page, limit, c.Query("q"))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, domains, page, limit, total)
}

func (h *ProgrammaticHandler) CreateDomain(c *fiber.Ctx) error {
	var req models.CreateDomainRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if req.PHPVersion == "" {
		req.PHPVersion = "8.1"
	}
	// Validate the request like the WHM handler + the other programmatic
	// endpoints do — otherwise a missing `user`/`php_version` falls through to a
	// confusing downstream error ("user account '' not found") instead of a clean
	// 400 telling the integrator exactly which field is missing.
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	// 3.1.88 — stamp creation source as "api" so the Domains list shows
	// the amber chip + filter on it. Always overrides whatever the
	// caller sent in the body (an external integrator can't pretend a
	// row was created manually from the panel).
	req.Source = "api"
	dom, err := h.domains.Create(c.UserContext(), &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, dom)
}

// Cloudflare --------------------------------------------------------------
//
// These let a reseller integration, after adding a domain, connect it to the
// panel's Cloudflare account and fetch the Cloudflare-assigned nameservers to
// hand back to the end customer for their registrar. All are per-domain scoped
// (RequireDomainOwnership) so a token only touches its own domains, and they
// use the panel owner's centralized Cloudflare account token.

func cfProgErr(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, services.ErrCloudflareDisabled):
		return response.BadRequest(c, "Cloudflare is not enabled on this panel", nil)
	case errors.Is(err, services.ErrCloudflareNoToken):
		return response.BadRequest(c, "Cloudflare API token is not configured on this panel", nil)
	default:
		return response.BadRequest(c, err.Error(), nil)
	}
}

// CloudflareStatus (GET /api/v1/external/cloudflare/:domain, scope cloudflare:read)
// returns whether the domain is connected to Cloudflare plus its zone status and
// assigned nameservers.
func (h *ProgrammaticHandler) CloudflareStatus(c *fiber.Ctx) error {
	if h.cloudflare == nil {
		return response.InternalError(c, "cloudflare service unavailable")
	}
	zone, err := h.cloudflare.FindZone(c.UserContext(), c.Params("domain"))
	if err != nil {
		return cfProgErr(c, err)
	}
	if zone == nil {
		return response.Success(c, fiber.Map{"connected": false})
	}
	return response.Success(c, fiber.Map{
		"connected":   true,
		"zone_id":     zone.ID,
		"status":      zone.Status,
		"nameservers": zone.NameServers,
	})
}

// CloudflareNameservers (GET /api/v1/external/cloudflare/:domain/nameservers,
// scope cloudflare:read) returns just the Cloudflare nameservers for a domain —
// the value an integrator needs to give the customer for their registrar. 404
// when the domain isn't connected to Cloudflare yet.
func (h *ProgrammaticHandler) CloudflareNameservers(c *fiber.Ctx) error {
	if h.cloudflare == nil {
		return response.InternalError(c, "cloudflare service unavailable")
	}
	zone, err := h.cloudflare.FindZone(c.UserContext(), c.Params("domain"))
	if err != nil {
		return cfProgErr(c, err)
	}
	if zone == nil {
		return response.NotFound(c, "domain is not connected to Cloudflare — POST .../connect first")
	}
	return response.Success(c, fiber.Map{
		"domain":      zone.Name,
		"zone_id":     zone.ID,
		"status":      zone.Status,
		"nameservers": zone.NameServers,
	})
}

// CloudflareConnect (POST /api/v1/external/cloudflare/:domain/connect, scope
// cloudflare:write) finds-or-creates the domain's Cloudflare zone (never
// duplicates) and returns the assigned nameservers so the customer can point
// their registrar at Cloudflare.
func (h *ProgrammaticHandler) CloudflareConnect(c *fiber.Ctx) error {
	if h.cloudflare == nil {
		return response.InternalError(c, "cloudflare service unavailable")
	}
	res, err := h.cloudflare.ConnectDomain(c.UserContext(), c.Params("domain"))
	if err != nil {
		return cfProgErr(c, err)
	}
	return response.Success(c, res)
}

// Guest links -------------------------------------------------------------

// MintGuestLink (POST /api/v1/external/guest-links, scope guest:create) mints
// a one-time, 30-minute, browser-locked login URL for a single domain. The
// link type (email vs email+DNS) is auto-derived from whether the domain is a
// subdomain of a panel-managed zone. The integrator redirects the end-user to
// the returned `url`.
//
// Body: { domain, max_mailboxes?, default_quota_mb?, default_send_per_hour? }
func (h *ProgrammaticHandler) MintGuestLink(c *fiber.Ctx) error {
	var req models.MintGuestLinkRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	scope := services.GetCallerScope(c.UserContext())
	issued, err := h.guest.Mint(c.UserContext(), scope, req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	// Build the public magic URL from the request's own host so it works
	// whatever hostname the panel is reached on.
	issued.URL = strings.TrimRight(c.BaseURL(), "/") + "/user-panel/m/" + issued.Token
	return response.Created(c, issued)
}

// SSL ---------------------------------------------------------------------

func (h *ProgrammaticHandler) IssueSSL(c *fiber.Ctx) error {
	var req models.IssueLetsEncryptRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if req.Domain == "" {
		req.Domain = c.Params("domain")
	}
	cert, err := h.ssl.IssueLetsEncrypt(c.UserContext(), &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, cert)
}

func (h *ProgrammaticHandler) ForceSSL(c *fiber.Ctx) error {
	domain := c.Params("domain")
	var body struct {
		Enable *bool `json:"enable"`
	}
	_ = c.BodyParser(&body)
	enable := true
	if body.Enable != nil {
		enable = *body.Enable
	}
	if err := h.ssl.ForceSSL(c.UserContext(), domain, enable); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Force-SSL toggled", fiber.Map{"domain": domain, "enabled": enable})
}

// Email -------------------------------------------------------------------

func (h *ProgrammaticHandler) ListMailboxes(c *fiber.Ctx) error {
	domain := c.Params("domain")
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 100)
	rows, total, err := h.emails.ListMailboxes(c.UserContext(), domain, page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, rows, page, limit, total)
}

func (h *ProgrammaticHandler) CreateMailbox(c *fiber.Ctx) error {
	domain := c.Params("domain")
	var req models.CreateMailboxRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if !strings.Contains(req.Email, "@") {
		req.Email = req.Email + "@" + domain
	}
	if req.Domain == "" {
		req.Domain = domain
	}
	mailbox, err := h.emails.CreateMailbox(c.UserContext(), &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, mailbox)
}

func (h *ProgrammaticHandler) GetMailboxStats(c *fiber.Ctx) error {
	id, err := h.resolveMailboxID(c)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	mb, err := h.emails.GetMailbox(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "mailbox not found")
	}
	return response.Success(c, fiber.Map{
		"email":               mb.Email,
		"quota_mb":            mb.QuotaMB,
		"used_mb":             mb.UsedMB,
		"send_limit_per_hour": mb.SendLimitPerHour,
		"created_at":          mb.CreatedAt,
		"updated_at":          mb.UpdatedAt,
	})
}

func (h *ProgrammaticHandler) DeleteMailbox(c *fiber.Ctx) error {
	id, err := h.resolveMailboxID(c)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	if err := h.emails.DeleteMailbox(c.UserContext(), id); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Mailbox deleted", nil)
}

// WebmailLink mints a one-shot SSO URL into Roundcube for the given
// mailbox. Address may be supplied as a bare local-part (combined with
// the URL :domain) OR as a full email — in either case the lookup is
// case-folded so callers don't have to know the exact case the row was
// stored under.
//
// Response shape matches the OpenAPI 3.1 spec at
// docs/api/openapi.yaml — { url, token, expires_in }. The URL is
// ABSOLUTE (built from the request's own scheme + host via
// c.BaseURL()), so an external integrator can hand it straight to a
// browser without having to know the panel's hostname; pre-3.1.29 the
// URL was a bare path and required the caller to glue the host on
// themselves. `expires_in` is the live TTL of the SSO token in seconds
// so the consumer knows when to re-mint.
//
// The 5-minute TTL itself is enforced by /webmail/sso.php (which
// rejects tokens older than 300 s based on the embedded `ts`).
const webmailSSOTTLSeconds = 300

func (h *ProgrammaticHandler) WebmailLink(c *fiber.Ctx) error {
	addr := strings.TrimSpace(c.Params("addr"))
	domain := strings.TrimSpace(c.Params("domain"))
	email := strings.ToLower(addr)
	if !strings.Contains(email, "@") {
		email = email + "@" + strings.ToLower(domain)
	}
	tok, err := h.emails.GenerateWebmailToken(c.UserContext(), email)
	if err != nil {
		// "mailbox not found" surfaced by the service layer is a 404
		// from the consumer's perspective — they asked for a resource
		// by name and the panel doesn't have it. Returning 400 made it
		// indistinguishable from a malformed request body.
		msg := err.Error()
		if strings.HasPrefix(msg, "mailbox not found") {
			return response.NotFound(c, msg)
		}
		return response.BadRequest(c, msg, nil)
	}
	relativeURL := "/webmail/sso.php?token=" + tok
	absoluteURL := strings.TrimRight(c.BaseURL(), "/") + relativeURL
	return response.Success(c, fiber.Map{
		"url":        absoluteURL,
		"token":      tok,
		"expires_in": webmailSSOTTLSeconds,
	})
}

// ResetMailboxPassword rotates the password on an existing mailbox. Gated
// behind a dedicated email:password scope so an integrator can grant
// "provision new mailboxes" (email:write) without also granting "lock
// existing users out by changing their passwords" (this scope).
//
// Address parameter accepts either a bare local-part (combined with the URL
// :domain) OR a full email address — matches the resolveMailboxID contract
// used by Delete and Stats so callers don't need to know which form the
// mailbox row stored.
//
// The internal flow is exactly the same path the WHM UI runs through
// EmailHandler.UpdateMailbox:
//   - hash the plaintext via `doveadm pw -s SHA512-CRYPT`
//   - rewrite the matching row in /etc/dovecot/users in place (awk, FS=":")
//     preserving the maildir path + uid/gid + extra fields
//   - re-encrypt the plaintext under jwtSecret for the webmail SSO path
//   - persist the new hash and encrypted plaintext to mongo
//
// The plaintext is NEVER echoed back in the response — the caller already
// knows it. Response shape mirrors GetMailboxStats so consumers can confirm
// the rotation landed on the row they expected.
func (h *ProgrammaticHandler) ResetMailboxPassword(c *fiber.Ctx) error {
	var req models.ResetMailboxPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	id, err := h.resolveMailboxID(c)
	if err != nil || id == "" {
		return response.NotFound(c, "mailbox not found")
	}
	// Blank password → generate a strong one and echo it back (v3.1.180).
	// This is the webmail-SSO recovery flow: a migration that couldn't re-key
	// encrypted_pass leaves a mailbox with no SSO credential, and the only way
	// to re-seed it is to set a fresh password. Letting the caller omit the
	// password (and returning the generated value) means an integrator can
	// recover SSO for a mailbox in a single call without inventing one. When
	// the caller DID supply a password we honour the existing contract and
	// never echo it — they already know it.
	generated := false
	pass := strings.TrimSpace(req.Password)
	if pass == "" {
		pass = services.GeneratedMailboxPassword()
		generated = true
	}
	// Record WHO/HOW on the mailbox row so every reset done through the public
	// API is an auditable record (filter password_set_via:"api"). Derived from
	// the authenticated token, never from the request body.
	by := "api"
	if tok, ok := c.Locals("api_token").(*models.APIToken); ok && tok != nil && strings.TrimSpace(tok.Name) != "" {
		by = "api:" + tok.Name
	}
	mb, err := h.emails.UpdateMailbox(c.UserContext(), id, map[string]interface{}{
		"password":         pass,
		"password_set_via": "api",
		"password_set_by":  by,
	})
	if err != nil {
		// "mailbox not found" surfaced by the service layer maps to 404;
		// everything else (doveadm failure, awk failure, mongo failure)
		// is 500 territory.
		msg := err.Error()
		if strings.HasPrefix(msg, "mailbox not found") {
			return response.NotFound(c, msg)
		}
		return response.InternalError(c, msg)
	}
	out := fiber.Map{
		"email":      mb.Email,
		"domain":     mb.Domain,
		"sso_ready":  mb.SSOReady,
		"updated_at": mb.UpdatedAt,
	}
	if generated {
		// Only present when the SERVER generated the password — the caller
		// needs it once to hand to the mailbox owner.
		out["generated_password"] = pass
	}
	return response.Success(c, out)
}

func (h *ProgrammaticHandler) ListForwarders(c *fiber.Ctx) error {
	rows, err := h.emails.ListForwarders(c.UserContext(), c.Params("domain"))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, rows)
}

func (h *ProgrammaticHandler) CreateForwarder(c *fiber.Ctx) error {
	domain := c.Params("domain")
	var req models.EmailForwarder
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	req.Domain = domain
	if !strings.Contains(req.Source, "@") {
		req.Source = req.Source + "@" + domain
	}
	fwd, err := h.emails.CreateForwarder(c.UserContext(), &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, fwd)
}

func (h *ProgrammaticHandler) DeleteForwarder(c *fiber.Ctx) error {
	// Scope the delete to :domain (already ownership-verified by the route
	// group) so the global forwarder :id can't be used to remove another
	// tenant's forwarder.
	if err := h.emails.DeleteForwarderInDomain(c.UserContext(), c.Params("id"), c.Params("domain")); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Forwarder deleted", nil)
}

// Deploy Software linking -------------------------------------------------

// ListServices returns every Deploy Software service the calling
// token can see, flat across all projects with project name/slug
// stamped on each row. Required scope: deploy:read. Tenant scoping
// flows through the standard CallerScope set by APITokenAuth.
//
// Pagination + search are passed through to ProjectService.ListAllServices
// so behaviour matches the JWT-driven /whm/projects/services route.
func (h *ProgrammaticHandler) ListServices(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 50)
	search := c.Query("search")
	list, total, err := h.projects.ListAllServices(c.UserContext(), page, limit, search)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, list, page, limit, total)
}

// LinkDomain attaches a domain alias to a Deploy Software service.
//
// Path: POST /api/v1/external/deploy/projects/:id/services/:svc/link-domain
// Body: { "domain": "alias.example.com" }
//
// Tenant + project-id checks now flow through
// ProjectService.AddAliasWithProject — see assertCanLinkAliasOnService
// for the full guard set. Pre-3.1.30 the handler ignored the :id path
// param entirely and the service layer didn't apply CallerScope, so a
// vendor with a deploy:link token could mutate any service across
// tenants just by guessing the service ObjectID. That's now blocked
// at the service layer; this handler additionally maps the new
// sentinel errors to 404 / 403 so an integrator can tell "wrong ID"
// (404) from "wrong tenant" (403) from "malformed body" (400).
func (h *ProgrammaticHandler) LinkDomain(c *fiber.Ctx) error {
	projectID := c.Params("id")
	svcID := c.Params("svc")
	var body struct {
		Domain string `json:"domain"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if strings.TrimSpace(body.Domain) == "" {
		return response.BadRequest(c, "domain is required", nil)
	}
	res, err := h.projects.AttachDomain(c.UserContext(), projectID, svcID, body.Domain)
	if err != nil {
		return mapProjectAliasError(c, err)
	}
	return response.Success(c, res)
}

func (h *ProgrammaticHandler) UnlinkDomain(c *fiber.Ctx) error {
	projectID := c.Params("id")
	svcID := c.Params("svc")
	domain := c.Params("domain")
	res, err := h.projects.DetachDomain(c.UserContext(), projectID, svcID, domain)
	if err != nil {
		return mapProjectAliasError(c, err)
	}
	return response.Success(c, res)
}

// mapProjectAliasError translates the sentinel errors raised by
// assertCanLinkAliasOnService to HTTP status codes. Any unmatched error
// falls through to 400 Bad Request so service-level validation messages
// (duplicate alias, "is already the primary domain", etc.) keep the
// status code their UI is already accustomed to.
func mapProjectAliasError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, services.ErrServiceNotFound),
		errors.Is(err, services.ErrProjectNotFound):
		return response.NotFound(c, err.Error())
	case errors.Is(err, services.ErrServiceProjectMismatch),
		errors.Is(err, services.ErrCrossTenantProject),
		errors.Is(err, services.ErrLinkedDomainNotOwned):
		return response.Forbidden(c, err.Error())
	}
	return response.BadRequest(c, err.Error(), nil)
}

// resolveMailboxID accepts either an ObjectID or an email address and
// returns the mailbox's ObjectID hex. Address form lets API consumers pass
// "alice@example.com" without first calling List to discover the id.
func (h *ProgrammaticHandler) resolveMailboxID(c *fiber.Ctx) (string, error) {
	addr := strings.ToLower(strings.TrimSpace(c.Params("addr")))
	domain := strings.ToLower(strings.TrimSpace(c.Params("domain")))
	if strings.Contains(addr, "@") {
		// A full address MUST be under the :domain the route group already
		// verified the caller owns (RequireDomainOwnership) — reject a
		// cross-domain address so :addr can't be used to reach another tenant's
		// mailbox (e.g. /email/mine.com/mailboxes/ceo@victim.com/...).
		if !strings.HasSuffix(addr, "@"+domain) {
			return "", fmt.Errorf("address must be under domain %s", domain)
		}
	} else {
		addr = addr + "@" + domain
	}
	mb, err := h.emails.GetMailboxByAddress(c.UserContext(), addr)
	if err != nil || mb == nil {
		return "", err
	}
	return mb.ID.Hex(), nil
}
