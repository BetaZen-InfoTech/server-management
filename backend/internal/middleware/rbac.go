package middleware

import (
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

// serverManagePerm gates host-wide, server-ROOT operations (reboot, Mongo/MySQL
// admin, raw config, IP reassign, cross-tenant vendor management, account
// transfers that hand out root's SSH key). It is deliberately NOT bypassed by
// is_super_admin — see RequirePermission. Only the platform owner, or someone
// the owner grants it EXPLICITLY, may pass it.
const serverManagePerm = "server.manage"

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
		// Super admins (within their panel scope) also bypass — EXCEPT for
		// server-root permissions. A vendor_admin with is_super_admin=true has
		// full control over their own TENANT without needing every permission
		// listed on their user record. But server.manage gates host-wide ops
		// that ESCAPE the tenant boundary (reboot, Mongo/MySQL admin, IP
		// reassign, cross-tenant vendor management, account transfers that issue
		// root's SSH key) — exactly the cross-tenant "visibility"/scope a
		// super-admin is documented NOT to get. Security (audit §12): the old
		// unconditional bypass let an owner who promoted a vendor_admin to
		// super-admin (believing it was tenant-bounded) unintentionally hand
		// them server root. So a super-admin must still hold server.manage
		// explicitly; only the platform owner (handled above) has it by default.
		if isSuper, _ := c.Locals("is_super_admin").(bool); isSuper {
			requiresServerManage := false
			for _, p := range permissions {
				if p == serverManagePerm {
					requiresServerManage = true
					break
				}
			}
			if !requiresServerManage {
				return c.Next()
			}
			// Fall through to the explicit-permission check below, so a
			// super-admin who was ALSO granted server.manage outright still
			// passes, but one who wasn't is blocked from server-root ops.
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
