package middleware

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/gofiber/fiber/v2"
)

// InjectScope attaches a services.CallerScope to the request's UserContext so
// every downstream service can pull it out via services.GetCallerScope. Must
// run AFTER middleware.Auth — it reads role / tenant_id / user_id off
// fiber.Locals which Auth sets from the JWT claims.
//
// Handlers that want their service queries to be tenant-scoped must pass
// c.UserContext() (not c.Context()) into the service call so the wrapped
// context is what the service receives.
func InjectScope() fiber.Handler {
	return func(c *fiber.Ctx) error {
		role, _ := c.Locals("role").(string)
		tenantHex, _ := c.Locals("tenant_id").(string)
		userHex, _ := c.Locals("user_id").(string)
		scope := &services.CallerScope{
			Role:      role,
			TenantHex: tenantHex,
			UserHex:   userHex,
		}
		c.SetUserContext(services.WithCallerScope(c.UserContext(), scope))
		return c.Next()
	}
}
