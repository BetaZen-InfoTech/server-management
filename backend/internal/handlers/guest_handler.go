// Package handlers — GuestHandler powers the no-login, single-domain guest
// surface (/api/v1/guest/*). It reuses EmailService and DNSService exactly as
// the programmatic handler does, but with three hard rules that make the
// session safe:
//
//  1. The domain is ALWAYS taken from the guest session (c.Locals
//     "guest_domain"), never from the URL or body. There is no :domain param.
//  2. Because several EmailService lookups resolve by id/address WITHOUT a
//     tenant check, every handler re-verifies the resolved row's Domain ==
//     session domain before mutating, and forces "@<domain>" on create.
//  3. DNS is only reachable for "email_dns" (main-domain) links, and apex "@"
//     A/AAAA records + zone create/delete are blocked.
package handlers

import (
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type GuestHandler struct {
	guest *services.GuestLinkService
	email *services.EmailService
	dns   *services.DNSService
}

func NewGuestHandler(guest *services.GuestLinkService, email *services.EmailService, dns *services.DNSService) *GuestHandler {
	return &GuestHandler{guest: guest, email: email, dns: dns}
}

// ---- session lifecycle ---------------------------------------------------

// Redeem (public, POST /api/v1/guest/redeem) is the first call the magic-link
// SPA makes. It verifies the token, performs the atomic first-open browser
// binding (or re-issues for the same browser within the window), sets the two
// HttpOnly cookies, and returns the session shape so the SPA can render.
func (h *GuestHandler) Redeem(c *fiber.Ctx) error {
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		var body struct {
			Token string `json:"token"`
		}
		_ = c.BodyParser(&body)
		token = strings.TrimSpace(body.Token)
	}
	if token == "" {
		return response.BadRequest(c, "token is required", nil)
	}

	link, err := h.guest.Verify(c.UserContext(), token)
	if err != nil {
		return response.Unauthorized(c, "This guest link is invalid or has expired.")
	}
	redeemed, bind, err := h.guest.Redeem(c.UserContext(), link, c.Cookies(services.GuestBindCookieName), c.Get("User-Agent"), c.IP())
	if err != nil {
		return response.Unauthorized(c, "This guest link has expired or was opened in another browser.")
	}
	sess, err := h.guest.IssueSessionToken(redeemed)
	if err != nil {
		return response.Unauthorized(c, "This guest link has expired.")
	}

	maxAge := 1
	if redeemed.WindowExpiresAt != nil {
		if secs := int(time.Until(*redeemed.WindowExpiresAt).Seconds()); secs > maxAge {
			maxAge = secs
		}
	}
	// Bind cookie: Path "/" + SameSite=Lax so it survives the top-level
	// navigation onto the magic URL and rides same-site guest XHRs.
	c.Cookie(&fiber.Cookie{Name: services.GuestBindCookieName, Value: bind, Path: "/", MaxAge: maxAge, HTTPOnly: true, Secure: c.Secure(), SameSite: "Lax"})
	// Session cookie: scoped to the guest API path, SameSite=Strict so it
	// never leaves on a cross-site request.
	c.Cookie(&fiber.Cookie{Name: services.GuestSessionCookieName, Value: sess, Path: "/api/v1/guest", MaxAge: maxAge, HTTPOnly: true, Secure: c.Secure(), SameSite: "Strict"})

	return response.Success(c, fiber.Map{
		"domain":            redeemed.Domain,
		"link_type":         redeemed.LinkType,
		"window_expires_at": redeemed.WindowExpiresAt,
	})
}

// Logout clears the guest cookies. The link stays bound to its first browser;
// the window keeps ticking but this browser can no longer act.
func (h *GuestHandler) Logout(c *fiber.Ctx) error {
	c.Cookie(&fiber.Cookie{Name: services.GuestSessionCookieName, Value: "", Path: "/api/v1/guest", MaxAge: -1, HTTPOnly: true, Secure: c.Secure(), SameSite: "Strict"})
	c.Cookie(&fiber.Cookie{Name: services.GuestBindCookieName, Value: "", Path: "/", MaxAge: -1, HTTPOnly: true, Secure: c.Secure(), SameSite: "Lax"})
	return response.SuccessMessage(c, "Signed out", nil)
}

// Session returns what the restricted UI needs to render: the domain, link
// type, the email limits + current usage, and the countdown deadline.
func (h *GuestHandler) Session(c *fiber.Ctx) error {
	link := h.link(c)
	if link == nil {
		return response.Unauthorized(c, "no guest session")
	}
	_, total, _ := h.email.ListMailboxes(c.UserContext(), link.Domain, 1, 1)
	return response.Success(c, fiber.Map{
		"domain":                link.Domain,
		"link_type":             link.LinkType,
		"max_mailboxes":         link.MaxMailboxes,
		"default_quota_mb":      link.DefaultQuotaMB,
		"default_send_per_hour": link.DefaultSendPerHour,
		"mailbox_count":         total,
		"window_expires_at":     link.WindowExpiresAt,
	})
}

// ---- email ---------------------------------------------------------------

func (h *GuestHandler) ListMailboxes(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 100)
	rows, total, err := h.email.ListMailboxes(c.UserContext(), h.domain(c), page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, rows, page, limit, total)
}

func (h *GuestHandler) CreateMailbox(c *fiber.Ctx) error {
	link := h.link(c)
	domain := link.Domain
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	addr, err := guestScopedAddress(body.Email, domain)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	// Enforce the per-link mailbox cap server-side.
	_, total, _ := h.email.ListMailboxes(c.UserContext(), domain, 1, 1)
	if total >= int64(link.MaxMailboxes) {
		return response.Forbidden(c, fmt.Sprintf("Mailbox limit reached for this domain (%d).", link.MaxMailboxes))
	}
	req := &models.CreateMailboxRequest{
		Email:            addr,
		Password:         body.Password,
		Domain:           domain,
		QuotaMB:          link.DefaultQuotaMB,
		SendLimitPerHour: link.DefaultSendPerHour,
	}
	if errs := validator.Validate(*req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	mb, err := h.email.CreateMailbox(c.UserContext(), req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, mb)
}

func (h *GuestHandler) UpdateMailbox(c *fiber.Ctx) error {
	mb, err := h.resolveMailbox(c)
	if err != nil {
		return err
	}
	var body struct {
		QuotaMB          *int   `json:"quota_mb"`
		SendLimitPerHour *int   `json:"send_limit_per_hour"`
		Password         string `json:"password"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	updates := map[string]interface{}{}
	if body.QuotaMB != nil {
		updates["quota_mb"] = *body.QuotaMB
	}
	if body.SendLimitPerHour != nil {
		updates["send_limit_per_hour"] = *body.SendLimitPerHour
	}
	if strings.TrimSpace(body.Password) != "" {
		if len(body.Password) < 8 {
			return response.BadRequest(c, "password must be at least 8 characters", nil)
		}
		updates["password"] = body.Password
	}
	if len(updates) == 0 {
		return response.BadRequest(c, "nothing to update", nil)
	}
	res, err := h.email.UpdateMailbox(c.UserContext(), mb.ID.Hex(), updates)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, res)
}

func (h *GuestHandler) ResetMailboxPassword(c *fiber.Ctx) error {
	mb, err := h.resolveMailbox(c)
	if err != nil {
		return err
	}
	var body models.ResetMailboxPasswordRequest
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(body); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	res, err := h.email.UpdateMailbox(c.UserContext(), mb.ID.Hex(), map[string]interface{}{"password": body.Password})
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, fiber.Map{"email": res.Email, "updated_at": res.UpdatedAt})
}

func (h *GuestHandler) DeleteMailbox(c *fiber.Ctx) error {
	mb, err := h.resolveMailbox(c)
	if err != nil {
		return err
	}
	if derr := h.email.DeleteMailbox(c.UserContext(), mb.ID.Hex()); derr != nil {
		return response.BadRequest(c, derr.Error(), nil)
	}
	return response.SuccessMessage(c, "Mailbox deleted", nil)
}

func (h *GuestHandler) WebmailLink(c *fiber.Ctx) error {
	mb, err := h.resolveMailbox(c)
	if err != nil {
		return err
	}
	tok, gerr := h.email.GenerateWebmailToken(c.UserContext(), mb.Email)
	if gerr != nil {
		return response.BadRequest(c, gerr.Error(), nil)
	}
	absoluteURL := strings.TrimRight(c.BaseURL(), "/") + "/webmail/sso.php?token=" + tok
	return response.Success(c, fiber.Map{"url": absoluteURL, "token": tok, "expires_in": 300})
}

func (h *GuestHandler) ListForwarders(c *fiber.Ctx) error {
	rows, err := h.email.ListForwarders(c.UserContext(), h.domain(c))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, rows)
}

func (h *GuestHandler) CreateForwarder(c *fiber.Ctx) error {
	domain := h.domain(c)
	var body struct {
		Source       string   `json:"source"`
		Destinations []string `json:"destinations"`
		KeepCopy     bool     `json:"keep_copy"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	src, err := guestScopedAddress(body.Source, domain)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	dests := make([]string, 0, len(body.Destinations))
	for _, d := range body.Destinations {
		if t := strings.TrimSpace(d); t != "" {
			dests = append(dests, t)
		}
	}
	if len(dests) == 0 {
		return response.BadRequest(c, "at least one destination is required", nil)
	}
	fwd := &models.EmailForwarder{Source: src, Destinations: dests, KeepCopy: body.KeepCopy, Domain: domain}
	res, err := h.email.CreateForwarder(c.UserContext(), fwd)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, res)
}

func (h *GuestHandler) DeleteForwarder(c *fiber.Ctx) error {
	domain := h.domain(c)
	id := strings.TrimSpace(c.Params("id"))
	// Ownership: the forwarder must belong to this domain. There's no
	// scope-checked GetForwarderByID, so confirm membership via the
	// domain-scoped list before deleting.
	list, err := h.email.ListForwarders(c.UserContext(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	found := false
	for _, f := range list {
		if f.ID.Hex() == id {
			found = true
			break
		}
	}
	if !found {
		return response.NotFound(c, "forwarder not found")
	}
	if derr := h.email.DeleteForwarder(c.UserContext(), id); derr != nil {
		return response.BadRequest(c, derr.Error(), nil)
	}
	return response.SuccessMessage(c, "Forwarder deleted", nil)
}

// ---- DNS (email_dns links only) ------------------------------------------

func (h *GuestHandler) ListDNSRecords(c *fiber.Ctx) error {
	if !h.dnsAllowed(c) {
		return response.Forbidden(c, "DNS management is not available for this link.")
	}
	rows, err := h.dns.ListRecords(c.UserContext(), h.domain(c))
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, rows)
}

func (h *GuestHandler) CreateDNSRecord(c *fiber.Ctx) error {
	if !h.dnsAllowed(c) {
		return response.Forbidden(c, "DNS management is not available for this link.")
	}
	domain := h.domain(c)
	var req models.CreateRecordRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	if guestApexBlocked(req.Name, req.Type, domain) {
		return response.Forbidden(c, "A/AAAA records for the domain root (@) cannot be managed from a guest link.")
	}
	rec, err := h.dns.AddRecord(c.UserContext(), domain, &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, rec)
}

func (h *GuestHandler) UpdateDNSRecord(c *fiber.Ctx) error {
	if !h.dnsAllowed(c) {
		return response.Forbidden(c, "DNS management is not available for this link.")
	}
	domain := h.domain(c)
	id := strings.TrimSpace(c.Params("id"))
	existing, err := h.findRecord(c, domain, id)
	if err != nil {
		return response.NotFound(c, "record not found")
	}
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	// Block both editing an existing apex A/AAAA and turning a record INTO one.
	name := existing.Name
	if v, ok := body["name"].(string); ok && strings.TrimSpace(v) != "" {
		name = v
	}
	rtype := existing.Type
	if v, ok := body["type"].(string); ok && strings.TrimSpace(v) != "" {
		rtype = v
	}
	if guestApexBlocked(existing.Name, existing.Type, domain) || guestApexBlocked(name, rtype, domain) {
		return response.Forbidden(c, "A/AAAA records for the domain root (@) cannot be managed from a guest link.")
	}
	rec, err := h.dns.UpdateRecord(c.UserContext(), domain, id, body)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, rec)
}

func (h *GuestHandler) DeleteDNSRecord(c *fiber.Ctx) error {
	if !h.dnsAllowed(c) {
		return response.Forbidden(c, "DNS management is not available for this link.")
	}
	domain := h.domain(c)
	id := strings.TrimSpace(c.Params("id"))
	existing, err := h.findRecord(c, domain, id)
	if err != nil {
		return response.NotFound(c, "record not found")
	}
	if guestApexBlocked(existing.Name, existing.Type, domain) {
		return response.Forbidden(c, "A/AAAA records for the domain root (@) cannot be managed from a guest link.")
	}
	if derr := h.dns.DeleteRecord(c.UserContext(), domain, id); derr != nil {
		return response.BadRequest(c, derr.Error(), nil)
	}
	return response.SuccessMessage(c, "Record deleted", nil)
}

// ---- helpers -------------------------------------------------------------

func (h *GuestHandler) domain(c *fiber.Ctx) string {
	v, _ := c.Locals("guest_domain").(string)
	return v
}

func (h *GuestHandler) link(c *fiber.Ctx) *models.GuestLink {
	v, _ := c.Locals("guest_link").(*models.GuestLink)
	return v
}

func (h *GuestHandler) dnsAllowed(c *fiber.Ctx) bool {
	t, _ := c.Locals("guest_link_type").(string)
	return t == models.GuestLinkTypeEmailDNS
}

// resolveMailbox turns the :addr param into a mailbox, forcing the session
// domain and re-verifying the resolved row's Domain (EmailService lookups are
// not tenant-checked). Returns a *response error ready to return on failure.
func (h *GuestHandler) resolveMailbox(c *fiber.Ctx) (*models.Mailbox, error) {
	domain := h.domain(c)
	addr, aerr := guestScopedAddress(c.Params("addr"), domain)
	if aerr != nil {
		return nil, response.BadRequest(c, aerr.Error(), nil)
	}
	mb, err := h.email.GetMailboxByAddress(c.UserContext(), addr)
	if err != nil || mb == nil {
		return nil, response.NotFound(c, "mailbox not found")
	}
	if !strings.EqualFold(mb.Domain, domain) {
		return nil, response.Forbidden(c, "not allowed")
	}
	return mb, nil
}

func (h *GuestHandler) findRecord(c *fiber.Ctx, domain, id string) (*models.DNSRecord, error) {
	rows, err := h.dns.ListRecords(c.UserContext(), domain)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].ID.Hex() == id {
			return &rows[i], nil
		}
	}
	return nil, fmt.Errorf("record not found")
}

// guestScopedAddress forces an email/source address into the session domain.
// A bare local part ("info") becomes "info@<domain>"; a full address must
// already be in <domain> or it's rejected.
func guestScopedAddress(raw, domain string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", fmt.Errorf("address is required")
	}
	if strings.Contains(v, "@") {
		parts := strings.SplitN(v, "@", 2)
		if parts[0] == "" {
			return "", fmt.Errorf("invalid address")
		}
		if parts[1] != strings.ToLower(domain) {
			return "", fmt.Errorf("address must belong to %s", domain)
		}
		return v, nil
	}
	return v + "@" + strings.ToLower(domain), nil
}

// guestApexBlocked reports whether (name, type) is an A/AAAA record at the zone
// apex ("@"), which a guest link may not manage. Uses the same normalization
// AddRecord uses so "@", the FQDN, and trailing-dot forms all collapse first.
func guestApexBlocked(name, rtype, domain string) bool {
	n := services.NormalizeRecordName(name, domain)
	t := strings.ToUpper(strings.TrimSpace(rtype))
	return n == "@" && (t == "A" || t == "AAAA")
}
