package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type MonitoringHandler struct{ service *services.MonitoringService }
func NewMonitoringHandler(s *services.MonitoringService) *MonitoringHandler { return &MonitoringHandler{service: s} }

func (h *MonitoringHandler) SystemInfo(c *fiber.Ctx) error {
	info, err := h.service.SystemInfo(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, info)
}
func (h *MonitoringHandler) Metrics(c *fiber.Ctx) error {
	metrics, err := h.service.LiveMetrics(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, metrics)
}
func (h *MonitoringHandler) Services(c *fiber.Ctx) error {
	svcs, err := h.service.ServiceStatus(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, svcs)
}
func (h *MonitoringHandler) History(c *fiber.Ctx) error {
	metric := c.Query("metric", "cpu"); period := c.Query("period", "24h"); interval := c.Query("interval", "5m")
	data, err := h.service.HistoricalMetrics(c.Context(), metric, period, interval)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}
func (h *MonitoringHandler) GetAlerts(c *fiber.Ctx) error {
	config, err := h.service.GetAlertsConfig(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, config)
}
func (h *MonitoringHandler) UpdateAlerts(c *fiber.Ctx) error {
	var body map[string]interface{}; if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateAlertsConfig(c.Context(), body); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Alert configuration updated", nil)
}
