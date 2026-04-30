// Package middleware — APITokenAuth accepts a Bearer token whose value starts
// with "btz_" and routes the request through APITokenService.Verify instead of
// the JWT path. Successful verification injects a CallerScope identical to
// what middleware.Auth + InjectScope would produce, so downstream services
// (DomainService, EmailService, ...) need no changes.
//
// Scope check is per-route: handlers that need a specific token scope wrap
// themselves in middleware.RequireTokenScope("domain:write") which inspects
// the *models.APIToken stashed on c.Locals.
package middleware

import (
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

// APITokenAuth gates routes that should accept programmatic tokens. Unlike Auth
// it does NOT fall back to JWT — these routes are exclusively for token bearers.
// If you need a route that accepts EITHER a JWT or a token, register two paths.
func APITokenAuth(tokens *services.APITokenService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		raw := strings.TrimSpace(c.Get("Authorization"))
		if raw == "" {
			return response.Unauthorized(c, "Missing authorization header")
		}
		parts := strings.SplitN(raw, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return response.Unauthorized(c, "Invalid authorization format")
		}
		bearer := parts[1]
		if !strings.HasPrefix(bearer, "btz_") {
			return response.Unauthorized(c, "API token expected")
		}
		tok, scope, err := tokens.Verify(c.UserContext(), bearer, c.IP())
		if err != nil {
			return response.Unauthorized(c, err.Error())
		}
		c.Locals("api_token", tok)
		c.Locals("api_token_id", tok.ID.Hex())
		c.Locals("user_id", scope.UserHex)
		c.Locals("role", scope.Role)
		c.Locals("tenant_id", scope.TenantHex)
		c.Locals("permissions", []string{})
		c.Locals("is_super_admin", false)
		c.SetUserContext(services.WithCallerScope(c.UserContext(), scope))
		return c.Next()
	}
}

// RequireTokenScope refuses the request unless the caller's API token includes
// the named scope. It MUST be registered after APITokenAuth in the route
// chain — without that the c.Locals("api_token") slot is empty.
func RequireTokenScope(scope string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		tok, _ := c.Locals("api_token").(*models.APIToken)
		if tok == nil {
			return response.Unauthorized(c, "API token required")
		}
		if !services.HasScope(tok, scope) {
			return response.Forbidden(c, "Token missing scope: "+scope)
		}
		return c.Next()
	}
}

type tokenBucket struct {
	count int
	reset time.Time
}

var (
	tokenRateMu      sync.Mutex
	tokenRateBuckets = map[string]*tokenBucket{}
)

// APITokenRateLimit caps tokens at maxPerMin requests per rolling minute, keyed
// by token id. Hot path lives in process memory only — fine for single-binary
// deploys; a future scale-out would swap this for Redis without changing the
// caller surface. Must be registered after APITokenAuth.
func APITokenRateLimit(maxPerMin int) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, _ := c.Locals("api_token_id").(string)
		if id == "" {
			return c.Next()
		}
		now := time.Now()
		tokenRateMu.Lock()
		b, ok := tokenRateBuckets[id]
		if !ok || now.After(b.reset) {
			b = &tokenBucket{reset: now.Add(time.Minute)}
			tokenRateBuckets[id] = b
		}
		b.count++
		over := b.count > maxPerMin
		retry := int(time.Until(b.reset).Seconds())
		tokenRateMu.Unlock()
		if over {
			c.Set("Retry-After", asciiInt(retry))
			return response.RateLimited(c)
		}
		return c.Next()
	}
}

func asciiInt(n int) string {
	if n <= 0 {
		return "60"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
