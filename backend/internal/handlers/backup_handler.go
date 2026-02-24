package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type BackupHandler struct {
	service *services.BackupService
}

func NewBackupHandler(s *services.BackupService) *BackupHandler {
	return &BackupHandler{service: s}
}

func (h *BackupHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	backups, total, err := h.service.List(c.Context(), page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, backups, page, limit, total)
}

func (h *BackupHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	b, err := h.service.GetByID(c.Context(), id)
	if err != nil {
		return response.NotFound(c, "Backup not found")
	}
	return response.Success(c, b)
}

func (h *BackupHandler) Create(c *fiber.Ctx) error {
	var req models.CreateBackupRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	b, err := h.service.Create(c.Context(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, b)
}

func (h *BackupHandler) Restore(c *fiber.Ctx) error {
	var req models.RestoreRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	if err := h.service.Restore(c.Context(), &req); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Restore completed", nil)
}

func (h *BackupHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Delete(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Backup deleted", nil)
}

func (h *BackupHandler) Download(c *fiber.Ctx) error {
	id := c.Params("id")
	path, err := h.service.GetDownloadPath(c.Context(), id)
	if err != nil {
		return response.NotFound(c, "Backup not found")
	}
	return c.Download(path)
}

func (h *BackupHandler) ListSchedules(c *fiber.Ctx) error {
	schedules, err := h.service.ListSchedules(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, schedules)
}

func (h *BackupHandler) CreateSchedule(c *fiber.Ctx) error {
	var req models.BackupSchedule
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	s, err := h.service.CreateSchedule(c.Context(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, s)
}

func (h *BackupHandler) DeleteSchedule(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.DeleteSchedule(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Schedule deleted", nil)
}
