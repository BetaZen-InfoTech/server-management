package middleware

import (
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/gofiber/fiber/v2"
)

// AuditLogger returns middleware that logs mutating API requests to the audit log.
func AuditLogger(auditService *services.AuditService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Only audit mutating requests
		method := c.Method()
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			return c.Next()
		}

		// Execute the handler first
		err := c.Next()

		// Extract user info from locals (set by Auth middleware)
		userID, _ := c.Locals("user_id").(string)
		email, _ := c.Locals("email").(string)
		role, _ := c.Locals("role").(string)

		// Skip if no authenticated user
		if userID == "" {
			return err
		}

		// Determine action and resource from the route
		path := c.Path()
		action, resourceType, resourceID := parseRoute(method, path, c)

		// Skip unrecognized routes
		if action == "" {
			return err
		}

		// Determine status
		status := "success"
		statusCode := c.Response().StatusCode()
		if statusCode >= 400 {
			status = "failure"
		}

		description := method + " " + path
		ip := c.IP()
		userAgent := c.Get("User-Agent")

		auditService.LogAction(
			c.Context(),
			userID, email, role,
			action, resourceType, resourceID,
			description, ip, userAgent, status,
			nil,
		)

		return err
	}
}

// parseRoute extracts action, resource type, and resource ID from the request.
func parseRoute(method, path string, c *fiber.Ctx) (action, resourceType, resourceID string) {
	// Remove API prefix
	path = strings.TrimPrefix(path, "/api/v1/whm/")
	path = strings.TrimPrefix(path, "/api/v1/cpanel/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 {
		return "", "", ""
	}

	// Map HTTP method to action verb
	var verb string
	switch method {
	case "POST":
		verb = "create"
	case "PUT", "PATCH":
		verb = "update"
	case "DELETE":
		verb = "delete"
	default:
		return "", "", ""
	}

	resourceType = parts[0]

	// Handle special action paths (e.g., POST /domains/:id/suspend)
	if len(parts) >= 3 {
		resourceID = parts[1]
		actionSuffix := parts[len(parts)-1]
		// Check for action verbs in the path
		switch actionSuffix {
		case "suspend", "unsuspend", "activate":
			verb = actionSuffix
		case "kill":
			verb = "kill"
		case "toggle":
			verb = "update"
		case "restart", "start", "stop":
			verb = actionSuffix
		case "deploy", "redeploy", "rollback":
			verb = actionSuffix
		case "install", "uninstall":
			verb = actionSuffix
		case "enable", "disable":
			verb = actionSuffix
		case "renew":
			verb = "renew"
		case "force-ssl":
			verb = "update"
			resourceType = "ssl"
		}
	} else if len(parts) >= 2 {
		resourceID = parts[1]
	}

	action = verb + "." + resourceType
	return action, resourceType, resourceID
}
