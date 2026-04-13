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

// Presets returns the framework preset catalogue so the deploy modal can
// render labels, default ports, and build/start commands that exactly match
// what the server will run. Keeping the frontend in sync used to be manual.
func (h *AppHandler) Presets(c *fiber.Ctx) error {
	return response.Success(c, services.GetPresets())
}

func (h *AppHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	apps, total, err := h.service.List(c.UserContext(), page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, apps, page, limit, total)
}

func (h *AppHandler) Get(c *fiber.Ctx) error {
	name := c.Params("name")
	app, err := h.service.GetByName(c.UserContext(), name)
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
	app, err := h.service.Deploy(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, app)
}

func (h *AppHandler) Redeploy(c *fiber.Ctx) error {
	name := c.Params("name")
	app, err := h.service.Redeploy(c.UserContext(), name)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, app)
}

func (h *AppHandler) Action(c *fiber.Ctx) error {
	name := c.Params("name")
	action := c.Params("action")
	if err := h.service.Action(c.UserContext(), name, action); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Action "+action+" completed", nil)
}

func (h *AppHandler) Delete(c *fiber.Ctx) error {
	name := c.Params("name")
	if err := h.service.Delete(c.UserContext(), name); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "App deleted", nil)
}

func (h *AppHandler) Logs(c *fiber.Ctx) error {
	name := c.Params("name")
	lines := c.QueryInt("lines", 100)
	logs, err := h.service.GetLogs(c.UserContext(), name, lines)
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
	if err := h.service.UpdateEnv(c.UserContext(), name, body.EnvVars, body.Restart); err != nil {
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
	if err := h.service.Rollback(c.UserContext(), name, body.DeploymentID); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Rollback completed", nil)
}

// Backup creates a tar.gz snapshot of the app's code + systemd unit.
func (h *AppHandler) Backup(c *fiber.Ctx) error {
	name := c.Params("name")
	bk, err := h.service.Backup(c.UserContext(), name)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, bk)
}

// ListBackups returns all backup archives for an app.
func (h *AppHandler) ListBackups(c *fiber.Ctx) error {
	name := c.Params("name")
	list, err := h.service.ListBackups(c.UserContext(), name)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, list)
}

// Restore reverts an app to a previous backup archive.
func (h *AppHandler) Restore(c *fiber.Ctx) error {
	name := c.Params("name")
	var body struct {
		File string `json:"file"`
	}
	if err := c.BodyParser(&body); err != nil || body.File == "" {
		return response.BadRequest(c, "file is required", nil)
	}
	if err := h.service.Restore(c.UserContext(), name, body.File); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Restore completed", nil)
}

// Transfer moves an app to a different system user or exports it to a remote host.
func (h *AppHandler) Transfer(c *fiber.Ctx) error {
	name := c.Params("name")
	var req services.TransferRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	app, err := h.service.Transfer(c.UserContext(), name, &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, app)
}

func (h *AppHandler) ListOwn(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	apps, total, err := h.service.ListByUser(c.UserContext(), userID, c.QueryInt("page", 1), c.QueryInt("limit", 20))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, apps, c.QueryInt("page", 1), c.QueryInt("limit", 20), total)
}
