package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type ConfigHandler struct{ service *services.ConfigService }
func NewConfigHandler(s *services.ConfigService) *ConfigHandler { return &ConfigHandler{service: s} }

func (h *ConfigHandler) Get(c *fiber.Ctx) error {
	cfg, err := h.service.GetAll(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, cfg)
}
func (h *ConfigHandler) UpdateNginx(c *fiber.Ctx) error {
	var req models.NginxConfig; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateNginx(c.UserContext(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Nginx config updated", nil)
}
func (h *ConfigHandler) UpdatePHP(c *fiber.Ctx) error {
	var req models.PHPConfig
	if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdatePHP(c.UserContext(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "PHP config updated", nil)
}
func (h *ConfigHandler) UpdateMongoDB(c *fiber.Ctx) error {
	var req models.MongoDBConfig; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateMongoDB(c.UserContext(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "MongoDB config updated", nil)
}
func (h *ConfigHandler) UpdateHostname(c *fiber.Ctx) error {
	var body struct{ Hostname string `json:"hostname"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateHostname(c.UserContext(), body.Hostname); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Hostname updated", nil)
}
func (h *ConfigHandler) UpdateTimezone(c *fiber.Ctx) error {
	var body struct{ Timezone string `json:"timezone"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateTimezone(c.UserContext(), body.Timezone); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Timezone updated", nil)
}
func (h *ConfigHandler) UpdateContactEmail(c *fiber.Ctx) error {
	var body struct{ Email string `json:"email"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateContactEmail(c.UserContext(), body.Email); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Contact email updated", nil)
}
func (h *ConfigHandler) TestNginx(c *fiber.Ctx) error {
	result, err := h.service.TestNginx(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, result)
}
func (h *ConfigHandler) RestartService(c *fiber.Ctx) error {
	service := c.Params("service")
	if err := h.service.RestartService(c.UserContext(), service); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, service+" restarted", nil)
}

// GetUISettings returns the admin-facing copy of the UI feature flags
// (demo-credentials toggles). Public-facing reads use a separate endpoint.
func (h *ConfigHandler) GetUISettings(c *fiber.Ctx) error {
	return response.Success(c, h.service.GetUISettings(c.UserContext()))
}

// UpdateUISettings persists the demo-credentials toggles. Platform owner
// only — gated at the route layer via server.manage.
func (h *ConfigHandler) UpdateUISettings(c *fiber.Ctx) error {
	var body services.UISettings
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.SetUISettings(c.UserContext(), body); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "UI settings updated", body)
}

// PublicSettings is the unauthenticated read of the same flags. The
// login pages can't carry a JWT yet, so they need a public surface; only
// non-sensitive UI hints land here.
func (h *ConfigHandler) PublicSettings(c *fiber.Ctx) error {
	return response.Success(c, h.service.GetUISettings(c.UserContext()))
}

// GetPanelDomain returns the current panel access domain, its SSL status
// and the server's public IP so the UI can render DNS instructions.
func (h *ConfigHandler) GetPanelDomain(c *fiber.Ctx) error {
	data, err := h.service.GetPanelDomain(c.UserContext())
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}

// UpdatePanelDomain connects a new custom domain to the panel UI and
// optionally issues a Let's Encrypt certificate for it.
func (h *ConfigHandler) UpdatePanelDomain(c *fiber.Ctx) error {
	var body struct {
		Domain   string `json:"domain"`
		IssueSSL bool   `json:"issue_ssl"`
		Email    string `json:"email"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	result, err := h.service.UpdatePanelDomain(c.UserContext(), body.Domain, body.IssueSSL, body.Email)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, result)
}

// GetPanelSSL returns the current TLS state for the panel vhost.
func (h *ConfigHandler) GetPanelSSL(c *fiber.Ctx) error {
	state, err := h.service.GetPanelSSL(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, state)
}

// InstallPanelSSL (re)issues a Let's Encrypt cert for the panel's
// current domain. Accepts { email?, force_renew? }.
func (h *ConfigHandler) InstallPanelSSL(c *fiber.Ctx) error {
	var body struct {
		Email      string `json:"email"`
		ForceRenew bool   `json:"force_renew"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	state, err := h.service.InstallPanelSSL(c.UserContext(), body.Email, body.ForceRenew)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, state)
}

// ReassignIP rewrites the server's public IP across DNS, SPF TXTs,
// domain records, panel .env, and the panel nginx vhost. Used when
// migrating the whole server to a new public address.
func (h *ConfigHandler) ReassignIP(c *fiber.Ctx) error {
	var body struct {
		OldIP string `json:"old_ip"`
		NewIP string `json:"new_ip"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	result, err := h.service.ReassignServerIP(c.UserContext(), body.OldIP, body.NewIP)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, result)
}

// -----------------------------------------------------------
// WHM Edit Database Configuration
// -----------------------------------------------------------
func (h *ConfigHandler) GetMySQLConfig(c *fiber.Ctx) error {
	cfg, err := h.service.GetMySQLConfig(c.UserContext())
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, cfg)
}
func (h *ConfigHandler) UpdateMySQLConfig(c *fiber.Ctx) error {
	var req services.MySQLConfig
	if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateMySQLConfig(c.UserContext(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "MySQL/MariaDB config updated", nil)
}

// WHM Repair Databases
func (h *ConfigHandler) ListMySQLDatabases(c *fiber.Ctx) error {
	dbs, err := h.service.ListMySQLDatabases(c.UserContext())
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, dbs)
}
func (h *ConfigHandler) RepairDatabase(c *fiber.Ctx) error {
	var body struct{ Database string `json:"database"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	out, err := h.service.RepairDatabase(c.UserContext(), body.Database)
	if err != nil { return response.BadRequest(c, err.Error(), fiber.Map{"output": out}) }
	return response.Success(c, fiber.Map{"output": out})
}

// WHM MultiPHP INI Editor
func (h *ConfigHandler) ListPHPVersions(c *fiber.Ctx) error {
	versions, err := h.service.ListPHPVersions(c.UserContext())
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, versions)
}
func (h *ConfigHandler) GetPHPIni(c *fiber.Ctx) error {
	version := c.Params("version")
	dirs, err := h.service.GetPHPIniDirectives(c.UserContext(), version)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, dirs)
}
func (h *ConfigHandler) UpdatePHPIni(c *fiber.Ctx) error {
	version := c.Params("version")
	var body struct {
		Directives []services.PHPIniDirective `json:"directives"`
	}
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdatePHPIniDirectives(c.UserContext(), version, body.Directives); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "php.ini updated", nil)
}

// WHM Reboot (graceful + forceful)
func (h *ConfigHandler) GracefulReboot(c *fiber.Ctx) error {
	if err := h.service.GracefulReboot(c.UserContext()); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Graceful reboot scheduled in 1 minute", nil)
}
func (h *ConfigHandler) ForcefulReboot(c *fiber.Ctx) error {
	if err := h.service.ForcefulReboot(c.UserContext()); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Forceful reboot issued", nil)
}

// -----------------------------------------------------------
// WHM MongoDB admin surface (v3.1.109)
// -----------------------------------------------------------
func (h *ConfigHandler) GetMongoDBConfig(c *fiber.Ctx) error {
	cfg, err := h.service.GetMongoDBConfig(c.UserContext())
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, cfg)
}
func (h *ConfigHandler) GetMongoRawConf(c *fiber.Ctx) error {
	raw, err := h.service.GetRawMongodConf(c.UserContext())
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, fiber.Map{"content": raw})
}
func (h *ConfigHandler) UpdateMongoRawConf(c *fiber.Ctx) error {
	var body struct{ Content string `json:"content"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateRawMongodConf(c.UserContext(), body.Content); err != nil { return response.BadRequest(c, err.Error(), nil) }
	return response.SuccessMessage(c, "mongod.conf updated — mongod restarted", nil)
}
func (h *ConfigHandler) GetMongoStatus(c *fiber.Ctx) error {
	st, err := h.service.GetMongoStatus(c.UserContext())
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, st)
}
func (h *ConfigHandler) ListMongoDatabases(c *fiber.Ctx) error {
	dbs, err := h.service.ListMongoDatabases(c.UserContext())
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, dbs)
}
func (h *ConfigHandler) ListMongoAdminUsers(c *fiber.Ctx) error {
	users, err := h.service.ListMongoAdminUsers(c.UserContext())
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, users)
}
func (h *ConfigHandler) CreateMongoAdminUser(c *fiber.Ctx) error {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.CreateMongoAdminUser(c.UserContext(), body.Username, body.Password, body.Role); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "MongoDB admin user created", nil)
}
func (h *ConfigHandler) DeleteMongoAdminUser(c *fiber.Ctx) error {
	if err := h.service.DeleteMongoAdminUser(c.UserContext(), c.Params("username")); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "MongoDB admin user deleted", nil)
}
