package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type DeployHandler struct{ service *services.DeployService }
func NewDeployHandler(s *services.DeployService) *DeployHandler { return &DeployHandler{service: s} }

func (h *DeployHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1); limit := c.QueryInt("limit", 20)
	deploys, total, err := h.service.List(c.Context(), page, limit); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Paginated(c, deploys, page, limit, total)
}
func (h *DeployHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id"); deploy, err := h.service.GetByID(c.Context(), id)
	if err != nil { return response.NotFound(c, "Deployment not found") }
	return response.Success(c, deploy)
}
func (h *DeployHandler) Create(c *fiber.Ctx) error {
	var req models.CreateGitHubDeployRequest; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if errs := validator.Validate(req); errs != nil { return response.BadRequest(c, "Validation failed", errs) }
	deploy, err := h.service.Create(c.Context(), &req); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Created(c, deploy)
}
func (h *DeployHandler) Redeploy(c *fiber.Ctx) error {
	id := c.Params("id"); deploy, err := h.service.Redeploy(c.Context(), id)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, deploy)
}
func (h *DeployHandler) Rollback(c *fiber.Ctx) error {
	id := c.Params("id"); var body struct{ TargetCommit string `json:"target_commit"` }
	_ = c.BodyParser(&body)
	_, err := h.service.Rollback(c.Context(), id, body.TargetCommit); if err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Rollback initiated", nil)
}
func (h *DeployHandler) Cancel(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.Cancel(c.Context(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Deployment cancelled", nil)
}
func (h *DeployHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.Delete(c.Context(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Deployment config deleted", nil)
}
func (h *DeployHandler) Logs(c *fiber.Ctx) error {
	id := c.Params("id"); releaseID := c.Query("release_id")
	logs, err := h.service.GetLogs(c.Context(), id, releaseID)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, logs)
}
func (h *DeployHandler) History(c *fiber.Ctx) error {
	id := c.Params("id"); page := c.QueryInt("page", 1); limit := c.QueryInt("limit", 10)
	history, total, err := h.service.History(c.Context(), id, page, limit)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Paginated(c, history, page, limit, total)
}
func (h *DeployHandler) GitHubWebhook(c *fiber.Ctx) error {
	deployID := c.Params("id")
	var payload map[string]interface{}
	if err := c.BodyParser(&payload); err != nil { return response.BadRequest(c, "Invalid webhook payload", nil) }
	if err := h.service.HandleGitHubWebhook(c.Context(), deployID, payload); err != nil { return response.BadRequest(c, err.Error(), nil) }
	return response.SuccessMessage(c, "Webhook processed", nil)
}
func (h *DeployHandler) Pause(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.Pause(c.Context(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Auto-deploy paused", nil)
}
func (h *DeployHandler) Resume(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.Resume(c.Context(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Auto-deploy resumed", nil)
}
