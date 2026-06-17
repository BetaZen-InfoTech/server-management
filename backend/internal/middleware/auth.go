package middleware

import (
	"context"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/authcache"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/jwt"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// activeUserCacheTTL is how long a positive/negative allow decision is
// cached before re-checking the DB. Short on purpose — suspending or
// deleting a vendor kicks them out within ~15s of the admin click
// without the cache becoming useless.
//
// The cache itself lives in pkg/authcache so that both this package
// (reader + writer) and internal/services (cache invalidator on
// suspend / delete / activate) can access it without creating a
// middleware ↔ services import cycle.
const activeUserCacheTTL = 15 * time.Second

// isUserAllowed answers "should this user_id be allowed to make
// authenticated requests right now?" Two reasons it can return false:
//   - User document was deleted (vendor cascade-delete); we can't
//     recover anything about them, so deny.
//   - User.IsActive == false (suspended); the panel + system cron + linux
//     account are all locked, so block API calls too.
//
// Cached for activeUserCacheTTL to keep the hot path cheap. A miss
// always queries Mongo, but a hit during an active SPA poll session is
// answered without any DB call.
func isUserAllowed(ctx context.Context, db *mongo.Database, userID string) bool {
	if userID == "" {
		return false
	}
	now := time.Now()
	if entry, ok := authcache.Get(userID); ok && entry.ExpiresAt.After(now) {
		return entry.Allowed
	}

	allowed := false
	if oid, err := primitive.ObjectIDFromHex(userID); err == nil {
		var u struct {
			IsActive bool `bson:"is_active"`
		}
		// FindOne with a 2s timeout so a slow Mongo round-trip can't
		// stall request handling indefinitely.
		ctxQ, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := db.Collection(database.ColUsers).FindOne(ctxQ, bson.M{"_id": oid}).Decode(&u)
		cancel()
		if err == nil {
			allowed = u.IsActive
		}
	}
	authcache.Put(userID, authcache.Status{Allowed: allowed, ExpiresAt: now.Add(activeUserCacheTTL)})
	return allowed
}

// InvalidateUserCache is kept as a thin shim so existing callers still
// resolve; new code should prefer authcache.Invalidate directly.
func InvalidateUserCache(userID string) { authcache.Invalidate(userID) }

// InvalidateAllUserCache nukes the entire cache — used after bulk
// operations like a tenant cascade delete where individual invalidation
// would be wasteful.
func InvalidateAllUserCache() { authcache.InvalidateAll() }

func Auth(cfg *config.Config, db *mongo.Database) fiber.Handler {
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

		// Guest-session JWTs (role="guest") are minted for the no-login
		// magic-link surface and must NEVER authenticate a normal panel
		// route, even if someone lifts the cookie value into an
		// Authorization header. The guest surface uses its own GuestAuth
		// middleware + a cookie, not this path.
		if claims.Role == "guest" {
			return response.Unauthorized(c, "Invalid or expired token")
		}

		// Account status check — a suspended or deleted user must NOT be
		// able to keep using their session via an existing valid JWT.
		// Cached for 15s so the SPA's chatty polling doesn't hammer Mongo.
		if !isUserAllowed(c.UserContext(), db, claims.UserID) {
			return response.Unauthorized(c, "Account suspended or deleted")
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

func OptionalAuth(cfg *config.Config, db *mongo.Database) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Next()
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && parts[0] == "Bearer" {
			claims, err := jwt.ValidateToken(cfg.JWTSecret, parts[1])
			if err == nil && isUserAllowed(c.UserContext(), db, claims.UserID) {
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
