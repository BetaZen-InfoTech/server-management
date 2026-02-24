package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type AppHandler struct {
	service *services.AppService
}

func NewAppHandler(s *services.AppService) *AppHandler {
	return &AppHandler{service: s}
}

func (h *AppHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	apps, total, err := h.service.List(c.Context(), page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, apps, page, limit, total)
}

func (h *AppHandler) Get(c *fiber.Ctx) error {
	name := c.Params("name")
	app, err := h.service.GetByName(c.Context(), name)
	if err != nil {
		return response.NotFound(c, "App not found")
	}
	return response.Success(c, app)
}

func (h *AppHandler) Deploy(c *fiber.Ctx) error {
	var req models.DeployAppRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	app, err := h.service.Deploy(c.Context(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, app)
}

func (h *AppHandler) Redeploy(c *fiber.Ctx) error {
	name := c.Params("name")
	app, err := h.service.Redeploy(c.Context(), name)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, app)
}

func (h *AppHandler) Action(c *fiber.Ctx) error {
	name := c.Params("name")
	action := c.Params("action")
	if err := h.service.Action(c.Context(), name, action); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Action "+action+" completed", nil)
}

func (h *AppHandler) Delete(c *fiber.Ctx) error {
	name := c.Params("name")
	if err := h.service.Delete(c.Context(), name); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "App deleted", nil)
}

func (h *AppHandler) Logs(c *fiber.Ctx) error {
	name := c.Params("name")
	lines := c.QueryInt("lines", 100)
	logs, err := h.service.GetLogs(c.Context(), name, lines)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, logs)
}

func (h *AppHandler) UpdateEnv(c *fiber.Ctx) error {
	name := c.Params("name")
	var body struct {
		EnvVars map[string]string `json:"env_vars"`
		Restart bool              `json:"restart"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.UpdateEnv(c.Context(), name, body.EnvVars, body.Restart); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Environment variables updated", nil)
}

func (h *AppHandler) Rollback(c *fiber.Ctx) error {
	name := c.Params("name")
	var body struct {
		DeploymentID string `json:"deployment_id"`
	}
	_ = c.BodyParser(&body)
	if err := h.service.Rollback(c.Context(), name, body.DeploymentID); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Rollback completed", nil)
}

func (h *AppHandler) ListOwn(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	apps, total, err := h.service.ListByUser(c.Context(), userID, c.QueryInt("page", 1), c.QueryInt("limit", 20))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, apps, c.QueryInt("page", 1), c.QueryInt("limit", 20), total)
}
