package response

import (
	"github.com/gofiber/fiber/v2"
)

type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

type ErrorResponse struct {
	Success bool       `json:"success"`
	Error   ErrorBody  `json:"error"`
}

type ErrorBody struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

type PaginatedResponse struct {
	Success    bool        `json:"success"`
	Data       interface{} `json:"data"`
	Pagination Pagination  `json:"pagination"`
}

type Pagination struct {
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

func Success(c *fiber.Ctx, data interface{}) error {
	return c.JSON(APIResponse{
		Success: true,
		Data:    data,
	})
}

func SuccessMessage(c *fiber.Ctx, message string, data interface{}) error {
	return c.JSON(APIResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

func Created(c *fiber.Ctx, data interface{}) error {
	return c.Status(fiber.StatusCreated).JSON(APIResponse{
		Success: true,
		Data:    data,
	})
}

func Paginated(c *fiber.Ctx, data interface{}, page, limit, total int) error {
	totalPages := total / limit
	if total%limit != 0 {
		totalPages++
	}
	return c.JSON(PaginatedResponse{
		Success: true,
		Data:    data,
		Pagination: Pagination{
			Page:       page,
			Limit:      limit,
			Total:      total,
			TotalPages: totalPages,
		},
	})
}

func BadRequest(c *fiber.Ctx, message string, details interface{}) error {
	return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
		Error: ErrorBody{Code: "VALIDATION_ERROR", Message: message, Details: details},
	})
}

func Unauthorized(c *fiber.Ctx, message string) error {
	return c.Status(fiber.StatusUnauthorized).JSON(ErrorResponse{
		Error: ErrorBody{Code: "UNAUTHORIZED", Message: message},
	})
}

func Forbidden(c *fiber.Ctx, message string) error {
	return c.Status(fiber.StatusForbidden).JSON(ErrorResponse{
		Error: ErrorBody{Code: "FORBIDDEN", Message: message},
	})
}

func NotFound(c *fiber.Ctx, message string) error {
	return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{
		Error: ErrorBody{Code: "NOT_FOUND", Message: message},
	})
}

func Conflict(c *fiber.Ctx, message string) error {
	return c.Status(fiber.StatusConflict).JSON(ErrorResponse{
		Error: ErrorBody{Code: "CONFLICT", Message: message},
	})
}

func AgentError(c *fiber.Ctx, message string) error {
	return c.Status(fiber.StatusUnprocessableEntity).JSON(ErrorResponse{
		Error: ErrorBody{Code: "AGENT_ERROR", Message: message},
	})
}

func RateLimited(c *fiber.Ctx) error {
	return c.Status(fiber.StatusTooManyRequests).JSON(ErrorResponse{
		Error: ErrorBody{Code: "RATE_LIMITED", Message: "Too many requests"},
	})
}

func InternalError(c *fiber.Ctx, message string) error {
	return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
		Error: ErrorBody{Code: "INTERNAL_ERROR", Message: message},
	})
}
