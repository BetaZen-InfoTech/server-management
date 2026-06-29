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
	service   *services.BackupService
	wpService *services.WordPressService
}

func NewBackupHandler(s *services.BackupService, wp *services.WordPressService) *BackupHandler {
	return &BackupHandler{service: s, wpService: wp}
}

func (h *BackupHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	backups, total, err := h.service.List(c.UserContext(), page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, backups, page, limit, total)
}

func (h *BackupHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	b, err := h.service.GetByID(c.UserContext(), id)
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
	b, err := h.service.Create(c.UserContext(), &req)
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
	if err := h.service.Restore(c.UserContext(), &req); err != nil {
		return response.InternalError(c, err.Error())
	}
	// Re-sync WordPress records from the restored filesystem so any
	// auto_update / version / db fields reflect the restored wp-config.php.
	if h.wpService != nil && req.User != "" {
		_, _ = h.wpService.RescanUser(c.UserContext(), req.User)
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

	if err := h.service.Restore(c.UserContext(), req); err != nil {
		return response.InternalError(c, err.Error())
	}
	if h.wpService != nil && req.User != "" {
		_, _ = h.wpService.RescanUser(c.UserContext(), req.User)
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
	if err := h.service.TestConnection(c.UserContext(), &req); err != nil {
		// A failed connectivity probe is an expected outcome of caller-supplied
		// parameters (unreachable host / closed port / bad credentials), not a
		// server fault — return 400, mirroring transfers/test-connection, so the
		// UI shows an actionable message instead of INTERNAL_ERROR. (verify pass 2026-06-29)
		return response.BadRequest(c, fmt.Sprintf("Connection failed: %s", err.Error()), nil)
	}
	return response.SuccessMessage(c, "Connection successful", nil)
}

func (h *BackupHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Delete(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Backup deleted", nil)
}

// CPanelRestore is the body-less restore entrypoint used by the cPanel
// UI. The frontend only has the backup id — user/domain aren't exposed
// in the cPanel backup list — so we resolve them server-side from the
// stored backup record and always restore a full snapshot from the
// server-local source. WHM retains the explicit Restore handler for
// cross-source / partial restores.
func (h *BackupHandler) CPanelRestore(c *fiber.Ctx) error {
	id := c.Params("id")
	backup, err := h.service.GetByID(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "Backup not found")
	}
	req := &models.RestoreRequest{
		BackupID:    id,
		Source:      "server",
		RestoreType: "full",
		User:        backup.User,
		Domain:      backup.Domain,
	}
	if err := h.service.Restore(c.UserContext(), req); err != nil {
		return response.InternalError(c, err.Error())
	}
	if h.wpService != nil && req.User != "" {
		_, _ = h.wpService.RescanUser(c.UserContext(), req.User)
	}
	return response.SuccessMessage(c, "Restore completed", nil)
}

func (h *BackupHandler) Download(c *fiber.Ctx) error {
	id := c.Params("id")
	path, err := h.service.GetDownloadPath(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "Backup not found")
	}
	return c.Download(path)
}

func (h *BackupHandler) ListSchedules(c *fiber.Ctx) error {
	schedules, err := h.service.ListSchedules(c.UserContext())
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
	s, err := h.service.CreateSchedule(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, s)
}

func (h *BackupHandler) DeleteSchedule(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.DeleteSchedule(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Schedule deleted", nil)
}
