package middleware

import (
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

func RequireRole(roles ...string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userRole, ok := c.Locals("role").(string)
		if !ok || userRole == "" {
			return response.Unauthorized(c, "Authentication required")
		}

		for _, role := range roles {
			if userRole == role {
				return c.Next()
			}
		}

		return response.Forbidden(c, "Insufficient role for this action")
	}
}

func RequirePermission(permissions ...string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userPerms, ok := c.Locals("permissions").([]string)
		if !ok {
			return response.Forbidden(c, "No permissions assigned")
		}

		permMap := make(map[string]bool, len(userPerms))
		for _, p := range userPerms {
			permMap[p] = true
		}

		for _, required := range permissions {
			if !permMap[required] {
				return response.Forbidden(c, "Missing permission: "+required)
			}
		}

		return c.Next()
	}
}
