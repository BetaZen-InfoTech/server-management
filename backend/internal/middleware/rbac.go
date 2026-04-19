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
		// vendor_owner always bypasses — they're the platform owner.
		if role, _ := c.Locals("role").(string); role == "vendor_owner" {
			return c.Next()
		}
		// Super admins (within their panel scope) also bypass. A
		// vendor_admin with is_super_admin=true has full control over
		// their tenant without needing every permission individually
		// listed on their user record. Tenant scoping still applies via
		// tenant_scope middleware and per-handler AssertOwns checks, so
		// this only elevates *permissions*, not *visibility*.
		if isSuper, _ := c.Locals("is_super_admin").(bool); isSuper {
			return c.Next()
		}

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
