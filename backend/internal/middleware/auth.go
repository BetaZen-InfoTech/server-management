package middleware

import (
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/jwt"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

func Auth(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return response.Unauthorized(c, "Missing authorization header")
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return response.Unauthorized(c, "Invalid authorization format")
		}

		claims, err := jwt.ValidateToken(cfg.JWTSecret, parts[1])
		if err != nil {
			return response.Unauthorized(c, "Invalid or expired token")
		}

		c.Locals("user_id", claims.UserID)
		c.Locals("email", claims.Email)
		c.Locals("role", claims.Role)
		c.Locals("permissions", claims.Permissions)
		c.Locals("is_super_admin", claims.IsSuperAdmin)
		c.Locals("impersonated_by", claims.ImpersonatedBy)
		// tenant_id may be empty for tokens issued before multi-tenancy.
		// Tenant-scoped handlers fall back to user_id in that case.
		tenantID := claims.TenantID
		if tenantID == "" {
			tenantID = claims.UserID
		}
		c.Locals("tenant_id", tenantID)

		return c.Next()
	}
}

func OptionalAuth(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Next()
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && parts[0] == "Bearer" {
			claims, err := jwt.ValidateToken(cfg.JWTSecret, parts[1])
			if err == nil {
				c.Locals("user_id", claims.UserID)
				c.Locals("email", claims.Email)
				c.Locals("role", claims.Role)
				c.Locals("permissions", claims.Permissions)
				c.Locals("is_super_admin", claims.IsSuperAdmin)
				c.Locals("impersonated_by", claims.ImpersonatedBy)
				tenantID := claims.TenantID
				if tenantID == "" {
					tenantID = claims.UserID
				}
				c.Locals("tenant_id", tenantID)
			}
		}

		return c.Next()
	}
}
