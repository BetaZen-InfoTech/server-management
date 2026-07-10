// Package handlers — APITokenHandler is the JWT-authenticated surface for
// managing API tokens (issue / list / rotate / revoke / list scopes). It
// powers the Developer → API Tokens page in WHM and the User Panel.
package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type APITokenHandler struct {
	svc *services.APITokenService
}

func NewAPITokenHandler(svc *services.APITokenService) *APITokenHandler {
	return &APITokenHandler{svc: svc}
}

// List returns the caller's tokens, newest first. Vendors only see their own
// tenant; vendor_owner sees every issued token.
func (h *APITokenHandler) List(c *fiber.Ctx) error {
	scope := services.GetCallerScope(c.UserContext())
	tokens, err := h.svc.List(c.UserContext(), scope)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, tokens)
}

// Scopes returns the static scope catalogue. Frontend renders the multi-select
// from this so adding a new scope only requires updating models.AllAPITokenScopes.
func (h *APITokenHandler) Scopes(c *fiber.Ctx) error {
	return response.Success(c, models.AllAPITokenScopes)
}

// Create mints a fresh token. Plaintext is returned ONCE; persist it client-side.
func (h *APITokenHandler) Create(c *fiber.Ctx) error {
	var req models.CreateAPITokenRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	scope := services.GetCallerScope(c.UserContext())
	perms, _ := c.Locals("permissions").([]string)
	isSuper, _ := c.Locals("is_super_admin").(bool)
	issued, err := h.svc.Issue(c.UserContext(), scope, perms, isSuper, req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, issued)
}

// Rotate mints a fresh secret on an existing token id.
func (h *APITokenHandler) Rotate(c *fiber.Ctx) error {
	scope := services.GetCallerScope(c.UserContext())
	issued, err := h.svc.Rotate(c.UserContext(), scope, c.Params("id"))
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, issued)
}

// Revoke flips the token to revoked. The bearer string stops working immediately.
func (h *APITokenHandler) Revoke(c *fiber.Ctx) error {
	scope := services.GetCallerScope(c.UserContext())
	if err := h.svc.Revoke(c.UserContext(), scope, c.Params("id")); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Token revoked", nil)
}

// Delete permanently removes a token row. Unlike Revoke (which keeps the row so
// the audit trail survives), this purges it — used to clear dead / rotated-away
// / orphaned tokens from the Developer list. Tenant scoping is enforced in the
// service layer.
func (h *APITokenHandler) Delete(c *fiber.Ctx) error {
	scope := services.GetCallerScope(c.UserContext())
	if err := h.svc.Delete(c.UserContext(), scope, c.Params("id")); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Token deleted", nil)
}

// UpdateScopes replaces an existing token's scope set in place. The
// bearer string stays valid — only the authorization grant changes.
// Body: { "scopes": ["domain:read", "email:write", ...] }.
//
// Same gate as Create: the operator's own permissions are re-checked
// so the edit can't grant a scope the operator doesn't have. Tenant
// scoping is enforced in the service layer.
func (h *APITokenHandler) UpdateScopes(c *fiber.Ctx) error {
	var body struct {
		Scopes []string `json:"scopes"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	scope := services.GetCallerScope(c.UserContext())
	perms, _ := c.Locals("permissions").([]string)
	isSuper, _ := c.Locals("is_super_admin").(bool)
	tok, err := h.svc.SetScopes(c.UserContext(), scope, perms, isSuper, c.Params("id"), body.Scopes)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, tok)
}
