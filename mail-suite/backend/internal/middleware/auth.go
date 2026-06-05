package middleware

import (
	"strings"

	"github.com/betazeninfotech/mail-suite/pkg/jwt"
	"github.com/betazeninfotech/mail-suite/pkg/response"
	"github.com/gofiber/fiber/v2"
)

const CtxUserID = "user_id"
const CtxUserEmail = "user_email"

func Auth(jm *jwt.Manager) fiber.Handler {
	return func(c *fiber.Ctx) error {
		h := c.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			return response.Unauthorized(c, "missing bearer token")
		}
		claims, err := jm.Validate(strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			return response.Unauthorized(c, "invalid token")
		}
		c.Locals(CtxUserID, claims.UserID)
		c.Locals(CtxUserEmail, claims.Email)
		return c.Next()
	}
}
