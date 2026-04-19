package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type DatabaseHandler struct {
	service *services.DatabaseService
}

func NewDatabaseHandler(s *services.DatabaseService) *DatabaseHandler {
	return &DatabaseHandler{service: s}
}

func (h *DatabaseHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	dbs, total, err := h.service.List(c.UserContext(), page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, dbs, page, limit, total)
}

func (h *DatabaseHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	db, err := h.service.GetByID(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "Database not found")
	}
	return response.Success(c, db)
}

func (h *DatabaseHandler) Create(c *fiber.Ctx) error {
	var req models.CreateDatabaseRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	db, err := h.service.Create(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, db)
}

func (h *DatabaseHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Delete(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Database deleted", nil)
}

func (h *DatabaseHandler) ListUsers(c *fiber.Ctx) error {
	id := c.Params("id")
	users, err := h.service.ListUsers(c.UserContext(), id)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, users)
}

func (h *DatabaseHandler) CreateUser(c *fiber.Ctx) error {
	id := c.Params("id")
	var req models.CreateDBUserRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	user, err := h.service.CreateUser(c.UserContext(), id, &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, user)
}

func (h *DatabaseHandler) DeleteUser(c *fiber.Ctx) error {
	id := c.Params("id")
	userID := c.Params("userId")
	if err := h.service.DeleteUser(c.UserContext(), id, userID); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Database user deleted", nil)
}

func (h *DatabaseHandler) UpdateOwnerPassword(c *fiber.Ctx) error {
	id := c.Params("id")
	var req models.UpdatePasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	if err := h.service.UpdateOwnerPassword(c.UserContext(), id, req.Password); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Password updated", nil)
}

func (h *DatabaseHandler) UpdateUserPassword(c *fiber.Ctx) error {
	id := c.Params("id")
	userID := c.Params("userId")
	var req models.UpdatePasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	if err := h.service.UpdateUserPassword(c.UserContext(), id, userID, req.Password); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "User password updated", nil)
}

func (h *DatabaseHandler) UpdateUserRole(c *fiber.Ctx) error {
	id := c.Params("id")
	userID := c.Params("userId")
	var req models.UpdateUserRoleRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	if err := h.service.UpdateUserRole(c.UserContext(), id, userID, req.Role); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "User role updated", nil)
}

func (h *DatabaseHandler) GetConnection(c *fiber.Ctx) error {
	id := c.Params("id")
	info, err := h.service.GetConnectionInfo(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.Success(c, info)
}

func (h *DatabaseHandler) GetPhpMyAdmin(c *fiber.Ctx) error {
	id := c.Params("id")
	info, err := h.service.GetPhpMyAdminInfo(c.UserContext(), id, "/phpmyadmin/")
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, info)
}

func (h *DatabaseHandler) EnableRemoteAccess(c *fiber.Ctx) error {
	id := c.Params("id")
	var req models.RemoteAccessRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.EnableRemoteAccess(c.UserContext(), id, &req); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Remote access enabled", nil)
}

// ListAccessHosts — GET /databases/:id/access-hosts — returns every remote
// host the operator has authorised for this database. Used by the WHM
// "Remote Database Access" modal.
func (h *DatabaseHandler) ListAccessHosts(c *fiber.Ctx) error {
	hosts, err := h.service.ListAccessHosts(c.UserContext(), c.Params("id"))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, hosts)
}

// AddAccessHost — POST /databases/:id/access-hosts — authorises a new host
// (IP / CIDR / hostname / %) for remote connections. Creates the matching
// MySQL host-scoped user and opens the firewall where applicable.
func (h *DatabaseHandler) AddAccessHost(c *fiber.Ctx) error {
	var req models.AddAccessHostRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	h2, err := h.service.AddAccessHost(c.UserContext(), c.Params("id"), req.Host, req.Comment)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, h2)
}

// RemoveAccessHost — DELETE /databases/:id/access-hosts/:hostId — drops the
// host-scoped MySQL user and deletes the DB record. Firewall rules are
// intentionally left alone (see service-layer comment for why).
func (h *DatabaseHandler) RemoveAccessHost(c *fiber.Ctx) error {
	if err := h.service.RemoveAccessHost(c.UserContext(), c.Params("id"), c.Params("hostId")); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Access host removed", nil)
}
