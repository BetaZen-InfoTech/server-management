package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type WordPressHandler struct{ service *services.WordPressService }
func NewWordPressHandler(s *services.WordPressService) *WordPressHandler { return &WordPressHandler{service: s} }

func (h *WordPressHandler) List(c *fiber.Ctx) error {
	installs, err := h.service.List(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, installs)
}
func (h *WordPressHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id"); wp, err := h.service.GetByID(c.UserContext(), id)
	if err != nil { return response.NotFound(c, "WordPress install not found") }
	return response.Success(c, wp)
}
func (h *WordPressHandler) CheckConflict(c *fiber.Ctx) error {
	domain := c.Query("domain"); path := c.Query("path")
	if domain == "" { return response.BadRequest(c, "domain is required", nil) }
	conflict, msg := h.service.CheckConflict(c.UserContext(), domain, path)
	return response.Success(c, fiber.Map{"conflict": conflict, "message": msg})
}
func (h *WordPressHandler) Install(c *fiber.Ctx) error {
	var req models.InstallWordPressRequest
	if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if errs := validator.Validate(req); errs != nil { return response.BadRequest(c, "Validation failed", errs) }
	wp, err := h.service.Install(c.UserContext(), &req); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Created(c, wp)
}
func (h *WordPressHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.Delete(c.UserContext(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "WordPress deleted", nil)
}
func (h *WordPressHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.Update(c.UserContext(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "WordPress updated", nil)
}
func (h *WordPressHandler) SecurityScan(c *fiber.Ctx) error {
	id := c.Params("id"); result, err := h.service.SecurityScan(c.UserContext(), id)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, result)
}
func (h *WordPressHandler) ListPlugins(c *fiber.Ctx) error {
	id := c.Params("id"); plugins, err := h.service.ListPlugins(c.UserContext(), id)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, plugins)
}
func (h *WordPressHandler) InstallPlugin(c *fiber.Ctx) error {
	id := c.Params("id"); var body struct{ Slug string `json:"slug"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.InstallPlugin(c.UserContext(), id, body.Slug); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Plugin installed", nil)
}
func (h *WordPressHandler) Rescan(c *fiber.Ctx) error {
	user := c.Query("user")
	count, err := h.service.RescanUser(c.UserContext(), user)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, fiber.Map{"synced": count})
}
func (h *WordPressHandler) ToggleAutoUpdate(c *fiber.Ctx) error {
	id := c.Params("id"); var body struct{ Enabled bool `json:"enabled"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.ToggleAutoUpdate(c.UserContext(), id, body.Enabled); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Auto-update setting updated", nil)
}
func (h *WordPressHandler) ToggleMaintenance(c *fiber.Ctx) error {
	id := c.Params("id"); var body struct{ Enabled bool `json:"enabled"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.ToggleMaintenance(c.UserContext(), id, body.Enabled); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Maintenance mode updated", nil)
}
func (h *WordPressHandler) AutoLogin(c *fiber.Ctx) error {
	id := c.Params("id"); url, err := h.service.AutoLogin(c.UserContext(), id)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, fiber.Map{"login_url": url})
}
func (h *WordPressHandler) ListUsers(c *fiber.Ctx) error {
	id := c.Params("id"); users, err := h.service.ListUsers(c.UserContext(), id)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, users)
}
func (h *WordPressHandler) CreateUser(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if body.Username == "" || body.Email == "" || body.Password == "" {
		return response.BadRequest(c, "username, email, and password are required", nil)
	}
	if body.Role == "" { body.Role = "subscriber" }
	if err := h.service.CreateUser(c.UserContext(), id, body.Username, body.Email, body.Password, body.Role); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "User created", nil)
}
func (h *WordPressHandler) DeleteUser(c *fiber.Ctx) error {
	id := c.Params("id"); userID := c.Params("uid")
	if err := h.service.DeleteUser(c.UserContext(), id, userID); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "User deleted", nil)
}
func (h *WordPressHandler) UpdateUserRole(c *fiber.Ctx) error {
	id := c.Params("id"); userID := c.Params("uid")
	var body struct { Role string `json:"role"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if body.Role == "" { return response.BadRequest(c, "role is required", nil) }
	if err := h.service.UpdateUserRole(c.UserContext(), id, userID, body.Role); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "User role updated", nil)
}

// ---------------------------------------------------------------------
// Plugin lifecycle — activate / deactivate / update / delete. List +
// Install already existed; these round out the per-plugin actions so
// the UI matches WP Toolkit's Plugins tab.
//
// Route convention: slug travels as `:slug` in the path when the
// target is a specific plugin. Activate/Deactivate/Delete require it;
// Update takes it optionally (empty slug = update all).
// ---------------------------------------------------------------------

func (h *WordPressHandler) ActivatePlugin(c *fiber.Ctx) error {
	id := c.Params("id"); slug := c.Params("slug")
	if err := h.service.ActivatePlugin(c.UserContext(), id, slug); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Plugin activated", nil)
}
func (h *WordPressHandler) DeactivatePlugin(c *fiber.Ctx) error {
	id := c.Params("id"); slug := c.Params("slug")
	if err := h.service.DeactivatePlugin(c.UserContext(), id, slug); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Plugin deactivated", nil)
}
func (h *WordPressHandler) UpdatePlugin(c *fiber.Ctx) error {
	id := c.Params("id"); slug := c.Params("slug") // optional — "" means update all
	if err := h.service.UpdatePlugin(c.UserContext(), id, slug); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Plugin update(s) applied", nil)
}
func (h *WordPressHandler) DeletePlugin(c *fiber.Ctx) error {
	id := c.Params("id"); slug := c.Params("slug")
	if err := h.service.DeletePlugin(c.UserContext(), id, slug); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Plugin deleted", nil)
}

// ---------------------------------------------------------------------
// Theme lifecycle — full parity with plugins (list / install / activate
// / update / delete). Matches the Themes tab in WP Toolkit.
// ---------------------------------------------------------------------

func (h *WordPressHandler) ListThemes(c *fiber.Ctx) error {
	id := c.Params("id")
	themes, err := h.service.ListThemes(c.UserContext(), id)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, themes)
}
func (h *WordPressHandler) InstallTheme(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct{ Slug string `json:"slug"` }
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if body.Slug == "" {
		return response.BadRequest(c, "slug is required", nil)
	}
	if err := h.service.InstallTheme(c.UserContext(), id, body.Slug); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Theme installed", nil)
}
func (h *WordPressHandler) ActivateTheme(c *fiber.Ctx) error {
	id := c.Params("id"); slug := c.Params("slug")
	if err := h.service.ActivateTheme(c.UserContext(), id, slug); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Theme activated", nil)
}
func (h *WordPressHandler) UpdateTheme(c *fiber.Ctx) error {
	id := c.Params("id"); slug := c.Params("slug") // optional — "" means update all
	if err := h.service.UpdateTheme(c.UserContext(), id, slug); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Theme update(s) applied", nil)
}
func (h *WordPressHandler) DeleteTheme(c *fiber.Ctx) error {
	id := c.Params("id"); slug := c.Params("slug")
	if err := h.service.DeleteTheme(c.UserContext(), id, slug); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Theme deleted", nil)
}

// Detach removes the site from our tracking DB without touching the
// files or database on disk. Matches WP Toolkit's "Detach" action.
// A `.wp-toolkit-ignore` marker is dropped at the install root so a
// subsequent Rescan doesn't re-attach the same site.
func (h *WordPressHandler) Detach(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Detach(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Site detached (files preserved)", nil)
}
