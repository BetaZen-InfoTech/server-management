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
	dbs, total, err := h.service.List(c.Context(), page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, dbs, page, limit, total)
}

func (h *DatabaseHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	db, err := h.service.GetByID(c.Context(), id)
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
	db, err := h.service.Create(c.Context(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, db)
}

func (h *DatabaseHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Delete(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Database deleted", nil)
}

func (h *DatabaseHandler) ListUsers(c *fiber.Ctx) error {
	id := c.Params("id")
	users, err := h.service.ListUsers(c.Context(), id)
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
	user, err := h.service.CreateUser(c.Context(), id, &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, user)
}

func (h *DatabaseHandler) DeleteUser(c *fiber.Ctx) error {
	id := c.Params("id")
	userID := c.Params("userId")
	if err := h.service.DeleteUser(c.Context(), id, userID); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Database user deleted", nil)
}

func (h *DatabaseHandler) EnableRemoteAccess(c *fiber.Ctx) error {
	id := c.Params("id")
	var req models.RemoteAccessRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.EnableRemoteAccess(c.Context(), id, &req); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Remote access enabled", nil)
}
