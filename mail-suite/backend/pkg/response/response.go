package response

import "github.com/gofiber/fiber/v2"

type Envelope struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Code    string      `json:"code,omitempty"`
}

func OK(c *fiber.Ctx, data interface{}) error {
	return c.JSON(Envelope{Success: true, Data: data})
}

func Created(c *fiber.Ctx, data interface{}) error {
	return c.Status(fiber.StatusCreated).JSON(Envelope{Success: true, Data: data})
}

func NoContent(c *fiber.Ctx) error {
	return c.SendStatus(fiber.StatusNoContent)
}

func Err(c *fiber.Ctx, status int, code, msg string) error {
	return c.Status(status).JSON(Envelope{Success: false, Error: msg, Code: code})
}

func BadRequest(c *fiber.Ctx, msg string) error    { return Err(c, fiber.StatusBadRequest, "bad_request", msg) }
func Unauthorized(c *fiber.Ctx, msg string) error  { return Err(c, fiber.StatusUnauthorized, "unauthorized", msg) }
func Forbidden(c *fiber.Ctx, msg string) error     { return Err(c, fiber.StatusForbidden, "forbidden", msg) }
func NotFound(c *fiber.Ctx, msg string) error      { return Err(c, fiber.StatusNotFound, "not_found", msg) }
func Conflict(c *fiber.Ctx, msg string) error      { return Err(c, fiber.StatusConflict, "conflict", msg) }
func Internal(c *fiber.Ctx, msg string) error      { return Err(c, fiber.StatusInternalServerError, "internal", msg) }
