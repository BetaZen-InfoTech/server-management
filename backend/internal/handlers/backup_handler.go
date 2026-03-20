package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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

// RestoreUpload handles restoring from an uploaded backup file.
func (h *BackupHandler) RestoreUpload(c *fiber.Ctx) error {
	file, err := c.FormFile("file")
	if err != nil {
		return response.BadRequest(c, "Backup file is required", nil)
	}

	restoreType := c.FormValue("restore_type", "files")
	user := c.FormValue("user")
	domain := c.FormValue("domain")

	if user == "" || domain == "" {
		return response.BadRequest(c, "User and domain are required", nil)
	}

	// Save uploaded file to temp directory
	tmpDir := fmt.Sprintf("/tmp/serverpanel-restore-%d", time.Now().UnixNano())
	if err := os.MkdirAll(tmpDir, 0750); err != nil {
		return response.InternalError(c, "Failed to create temp directory")
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, file.Filename)
	if err := c.SaveFile(file, tmpPath); err != nil {
		return response.InternalError(c, "Failed to save uploaded file")
	}

	req := &models.RestoreRequest{
		BackupID:    tmpPath,
		Source:      "upload",
		RestoreType: restoreType,
		User:        user,
		Domain:      domain,
	}

	if err := h.service.Restore(c.Context(), req); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Restore from upload completed", nil)
}

// TestConnection tests connectivity to a remote FTP/SFTP/SCP server.
func (h *BackupHandler) TestConnection(c *fiber.Ctx) error {
	var req models.TestConnectionRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	if err := h.service.TestConnection(c.Context(), &req); err != nil {
		return response.InternalError(c, fmt.Sprintf("Connection failed: %s", err.Error()))
	}
	return response.SuccessMessage(c, "Connection successful", nil)
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
