package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type FirewallHandler struct{ service *services.FirewallService }
func NewFirewallHandler(s *services.FirewallService) *FirewallHandler { return &FirewallHandler{service: s} }

func (h *FirewallHandler) Status(c *fiber.Ctx) error {
	status, err := h.service.GetStatus(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, status)
}
func (h *FirewallHandler) ListRules(c *fiber.Ctx) error {
	rules, err := h.service.ListRules(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, rules)
}
func (h *FirewallHandler) AllowPort(c *fiber.Ctx) error {
	var req models.AllowPortRequest; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.AllowPort(c.UserContext(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Port allowed", nil)
}
func (h *FirewallHandler) DenyPort(c *fiber.Ctx) error {
	var req models.AllowPortRequest; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.DenyPort(c.UserContext(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Port denied", nil)
}
func (h *FirewallHandler) DeleteRule(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.DeleteRule(c.UserContext(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Rule deleted", nil)
}
func (h *FirewallHandler) BlockIP(c *fiber.Ctx) error {
	var req models.BlockIPRequest; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.BlockIP(c.UserContext(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "IP blocked", nil)
}
func (h *FirewallHandler) UnblockIP(c *fiber.Ctx) error {
	var body struct{ IP string `json:"ip"` }; if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UnblockIP(c.UserContext(), body.IP); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "IP unblocked", nil)
}
func (h *FirewallHandler) ListBlockedIPs(c *fiber.Ctx) error {
	ips, err := h.service.ListBlockedIPs(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, ips)
}
func (h *FirewallHandler) Fail2BanStatus(c *fiber.Ctx) error {
	status, err := h.service.Fail2BanStatus(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, status)
}
func (h *FirewallHandler) UpdateFail2Ban(c *fiber.Ctx) error {
	var req models.Fail2BanConfig; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateFail2Ban(c.UserContext(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "fail2ban updated", nil)
}
