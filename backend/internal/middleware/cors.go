package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

// CORS builds the cross-origin policy. allowOrigins is a comma-separated
// allowlist (config.CORSAllowOrigins); empty keeps the permissive "*" default.
//
// A wildcard is safe for THIS API: every authenticated route carries a Bearer
// token in the Authorization header, never an ambient cookie, and we never set
// Access-Control-Allow-Credentials — so a malicious origin cannot ride a
// logged-in user's session (the browser won't attach credentials, and the
// attacker has no token). The one cookie-backed surface (guest links) runs on
// its own SameSite=Strict cookie + middleware. Operators who still want to pin
// the panel to its own origin set CORS_ALLOW_ORIGINS.
func CORS(allowOrigins string) fiber.Handler {
	origins := strings.TrimSpace(allowOrigins)
	if origins == "" {
		origins = "*"
	}
	return cors.New(cors.Config{
		AllowOrigins:  origins,
		AllowMethods:  "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders:  "Origin,Content-Type,Accept,Authorization,X-Requested-With",
		ExposeHeaders: "X-RateLimit-Limit,X-RateLimit-Remaining,X-RateLimit-Reset",
		MaxAge:        86400,
	})
}
