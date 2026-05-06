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
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type ProgrammaticHandler struct {
	domains  *services.DomainService
	emails   *services.EmailService
	ssl      *services.SSLService
	projects *services.ProjectService
}

func NewProgrammaticHandler(d *services.DomainService, e *services.EmailService, s *services.SSLService, p *services.ProjectService) *ProgrammaticHandler {
	return &ProgrammaticHandler{domains: d, emails: e, ssl: s, projects: p}
}

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
	dom, err := h.domains.Create(c.UserContext(), &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, dom)
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

func (h *ProgrammaticHandler) WebmailLink(c *fiber.Ctx) error {
	addr := c.Params("addr")
	domain := c.Params("domain")
	email := addr
	if !strings.Contains(email, "@") {
		email = email + "@" + domain
	}
	tok, err := h.emails.GenerateWebmailToken(c.UserContext(), email)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, fiber.Map{
		"token": tok,
		"url":   "/webmail/sso.php?token=" + tok,
	})
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
	if err := h.emails.DeleteForwarder(c.UserContext(), c.Params("id")); err != nil {
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

func (h *ProgrammaticHandler) LinkDomain(c *fiber.Ctx) error {
	svcID := c.Params("svc")
	var body struct {
		Domain string `json:"domain"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if body.Domain == "" {
		return response.BadRequest(c, "domain is required", nil)
	}
	res, err := h.projects.AddAlias(c.UserContext(), svcID, body.Domain)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, res)
}

func (h *ProgrammaticHandler) UnlinkDomain(c *fiber.Ctx) error {
	svcID := c.Params("svc")
	domain := c.Params("domain")
	res, err := h.projects.RemoveAlias(c.UserContext(), svcID, domain)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, res)
}

// resolveMailboxID accepts either an ObjectID or an email address and
// returns the mailbox's ObjectID hex. Address form lets API consumers pass
// "alice@example.com" without first calling List to discover the id.
func (h *ProgrammaticHandler) resolveMailboxID(c *fiber.Ctx) (string, error) {
	addr := c.Params("addr")
	domain := c.Params("domain")
	if !strings.Contains(addr, "@") {
		addr = addr + "@" + domain
	}
	mb, err := h.emails.GetMailboxByAddress(c.UserContext(), addr)
	if err != nil || mb == nil {
		return "", err
	}
	return mb.ID.Hex(), nil
}
