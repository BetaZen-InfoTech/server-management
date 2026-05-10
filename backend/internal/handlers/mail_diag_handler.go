// Package handlers — mail_diag_handler.go.
//
// Two endpoints behind the WHM "Mail Issues & Resolution" page:
//
//   GET  /api/v1/whm/diagnostics/mail-stack          → DiagnosticReport
//   POST /api/v1/whm/diagnostics/mail-stack/fix      → []FixResult
//
// Both gated on `server.manage` at the route layer. Diagnose is read-
// only + safe to call on every render; AutoFix executes the per-check
// FixCommand the service marked AutoFixable=true (apt install,
// systemctl start, postmap — never `rm -rf`).
package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type MailDiagHandler struct {
	service *services.MailDiagnosticService
}

func NewMailDiagHandler(s *services.MailDiagnosticService) *MailDiagHandler {
	return &MailDiagHandler{service: s}
}

// Diagnose returns the full mail-stack report.
func (h *MailDiagHandler) Diagnose(c *fiber.Ctx) error {
	rep, err := h.service.Diagnose(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, rep)
}

// AutoFix runs the per-check FixCommand for each id in the body.
// Body: `{ "ids": ["svc.dovecot", "postfix.maps", ...] }`.
// Returns one FixResult per id with success + output so the UI can
// show a green check / red cross + the journal output inline.
func (h *MailDiagHandler) AutoFix(c *fiber.Ctx) error {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "invalid request body", nil)
	}
	if len(body.IDs) == 0 {
		return response.BadRequest(c, "ids is required (non-empty array of check ids to auto-fix)", nil)
	}
	if len(body.IDs) > 50 {
		return response.BadRequest(c, "too many ids (cap 50 per call)", nil)
	}
	results, err := h.service.AutoFix(c.UserContext(), body.IDs)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, fiber.Map{"results": results})
}
