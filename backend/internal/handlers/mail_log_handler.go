// Package handlers — MailLogHandler exposes the structured, source-agnostic
// mail log built by services.MailLogService. Mounted under both the WHM
// (platform-owner) and cPanel (tenant-scoped) surfaces; the service applies
// tenant scoping from the CallerScope on the context, so the same handler
// serves both safely.
package handlers

import (
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type MailLogHandler struct {
	service *services.MailLogService
}

func NewMailLogHandler(s *services.MailLogService) *MailLogHandler {
	return &MailLogHandler{service: s}
}

// List handles GET /mail/logs?page=&limit=&direction=&status=&source=&q=&since=&until=
// since/until accept either RFC3339 or a plain YYYY-MM-DD date.
func (h *MailLogHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 50)
	f := services.MailLogFilter{
		Direction: strings.TrimSpace(c.Query("direction")),
		Status:    strings.TrimSpace(c.Query("status")),
		Source:    strings.TrimSpace(c.Query("source")),
		Query:     strings.TrimSpace(c.Query("q")),
		Since:     parseFlexDate(c.Query("since")),
		Until:     parseFlexDate(c.Query("until")),
	}
	rows, total, err := h.service.List(c.UserContext(), f, page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, rows, page, limit, total)
}

// Stats handles GET /mail/logs/stats?days=7
func (h *MailLogHandler) Stats(c *fiber.Ctx) error {
	days := c.QueryInt("days", 7)
	stats, err := h.service.Stats(c.UserContext(), days)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, stats)
}

// parseFlexDate accepts RFC3339, "YYYY-MM-DDTHH:MM", or "YYYY-MM-DD".
// Returns the zero time when empty/unparseable so the service treats it as
// "no bound".
func parseFlexDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
