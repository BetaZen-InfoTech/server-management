package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type LogHandler struct{ service *services.LogService }
func NewLogHandler(s *services.LogService) *LogHandler { return &LogHandler{service: s} }

func (h *LogHandler) View(c *fiber.Ctx) error {
	logType := c.Params("type"); lines := c.QueryInt("lines", 100); since := c.Query("since"); until := c.Query("until")
	data, err := h.service.ViewLogs(c.Context(), logType, lines, since, until)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}
func (h *LogHandler) Search(c *fiber.Ctx) error {
	logType := c.Params("type"); query := c.Query("query"); lines := c.QueryInt("lines", 200)
	data, err := h.service.SearchLogs(c.Context(), logType, query, lines)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}
func (h *LogHandler) Download(c *fiber.Ctx) error {
	logType := c.Params("type"); format := c.Query("format", "raw")
	path, err := h.service.DownloadLog(c.Context(), logType, format)
	if err != nil { return response.NotFound(c, "Log not found") }
	return c.Download(path)
}
func (h *LogHandler) ListFiles(c *fiber.Ctx) error {
	files, err := h.service.ListLogFiles(c.Context())
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, files)
}
