package handlers

import (
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type SoftwareHandler struct{ service *services.SoftwareService }

func NewSoftwareHandler(s *services.SoftwareService) *SoftwareHandler {
	return &SoftwareHandler{service: s}
}

func (h *SoftwareHandler) ListInstalled(c *fiber.Ctx) error {
	sw, err := h.service.ListInstalled(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, sw)
}

func (h *SoftwareHandler) Install(c *fiber.Ctx) error {
	var body struct {
		Software string `json:"software"`
		Version  string `json:"version"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.Install(c.Context(), body.Software, body.Version); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, body.Software+" installed", nil)
}

func (h *SoftwareHandler) Uninstall(c *fiber.Ctx) error {
	var body struct {
		Software string `json:"software"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.Uninstall(c.Context(), body.Software); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, body.Software+" uninstalled", nil)
}

func (h *SoftwareHandler) CheckUpdates(c *fiber.Ctx) error {
	updates, err := h.service.CheckUpdates(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, updates)
}

func (h *SoftwareHandler) InstallEmail(c *fiber.Ctx) error {
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}

	if body["hostname"] == nil || body["hostname"] == "" {
		return response.BadRequest(c, "hostname is required", nil)
	}
	if body["domain"] == nil || body["domain"] == "" {
		return response.BadRequest(c, "domain is required", nil)
	}

	result, err := h.service.InstallEmailServer(c.Context(), body)
	if err != nil {
		if strings.Contains(err.Error(), "already in progress") {
			return response.Conflict(c, err.Error())
		}
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, result)
}

func (h *SoftwareHandler) EmailStatus(c *fiber.Ctx) error {
	status, err := h.service.EmailServerStatus(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, status)
}

func (h *SoftwareHandler) GetEmailInstallation(c *fiber.Ctx) error {
	id := c.Params("id")
	installation, err := h.service.GetEmailInstallation(c.Context(), id)
	if err != nil {
		return response.NotFound(c, "Installation not found")
	}
	return response.Success(c, installation)
}

func (h *SoftwareHandler) UpdateEmailSettings(c *fiber.Ctx) error {
	var req models.UpdateEmailSettingsRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}

	result, err := h.service.UpdateEmailSettings(c.Context(), req)
	if err != nil {
		if strings.Contains(err.Error(), "not installed") {
			return response.NotFound(c, err.Error())
		}
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, result)
}

// ──────────────────────────────────────────────────────
// Runtime version management
// ──────────────────────────────────────────────────────

func (h *SoftwareHandler) ListAllRuntimes(c *fiber.Ctx) error {
	runtimes, err := h.service.ListAllRuntimes(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, runtimes)
}

func (h *SoftwareHandler) ListRuntimeVersions(c *fiber.Ctx) error {
	runtime := c.Params("runtime")
	versions, err := h.service.ListRuntimeVersions(c.Context(), runtime)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, versions)
}

func (h *SoftwareHandler) InstallRuntime(c *fiber.Ctx) error {
	var body struct {
		Runtime string `json:"runtime"`
		Version string `json:"version"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if body.Runtime == "" || body.Version == "" {
		return response.BadRequest(c, "runtime and version are required", nil)
	}
	if err := h.service.InstallRuntime(c.Context(), body.Runtime, body.Version); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, body.Runtime+" "+body.Version+" installed", nil)
}

func (h *SoftwareHandler) UninstallRuntime(c *fiber.Ctx) error {
	var body struct {
		Runtime string `json:"runtime"`
		Version string `json:"version"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if body.Runtime == "" {
		return response.BadRequest(c, "runtime is required", nil)
	}
	if err := h.service.UninstallRuntime(c.Context(), body.Runtime, body.Version); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, body.Runtime+" uninstalled", nil)
}

// ──────────────────────────────────────────────────────
// PHP Extensions
// ──────────────────────────────────────────────────────

func (h *SoftwareHandler) ListPHPExtensions(c *fiber.Ctx) error {
	phpVersion := c.Params("version")
	extensions, err := h.service.ListPHPExtensions(c.Context(), phpVersion)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, extensions)
}

func (h *SoftwareHandler) InstallPHPExtension(c *fiber.Ctx) error {
	phpVersion := c.Params("version")
	var body struct {
		Extension string `json:"extension"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if body.Extension == "" {
		return response.BadRequest(c, "extension is required", nil)
	}
	if err := h.service.InstallPHPExtension(c.Context(), phpVersion, body.Extension); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, body.Extension+" installed for PHP "+phpVersion, nil)
}

func (h *SoftwareHandler) UninstallPHPExtension(c *fiber.Ctx) error {
	phpVersion := c.Params("version")
	var body struct {
		Extension string `json:"extension"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if body.Extension == "" {
		return response.BadRequest(c, "extension is required", nil)
	}
	if err := h.service.UninstallPHPExtension(c.Context(), phpVersion, body.Extension); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, body.Extension+" removed from PHP "+phpVersion, nil)
}

// ──────────────────────────────────────────────────────
// PHP-FPM Management
// ──────────────────────────────────────────────────────

func (h *SoftwareHandler) ListPHPFPMPools(c *fiber.Ctx) error {
	phpVersion := c.Params("version")
	pools, err := h.service.ListPHPFPMPools(c.Context(), phpVersion)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, pools)
}

func (h *SoftwareHandler) GetPHPFPMStatus(c *fiber.Ctx) error {
	phpVersion := c.Params("version")
	status, err := h.service.GetPHPFPMStatus(c.Context(), phpVersion)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, status)
}

func (h *SoftwareHandler) RestartPHPFPM(c *fiber.Ctx) error {
	phpVersion := c.Params("version")
	if err := h.service.RestartPHPFPM(c.Context(), phpVersion); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "PHP-FPM "+phpVersion+" restarted", nil)
}
