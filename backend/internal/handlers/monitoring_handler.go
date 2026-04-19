package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type MonitoringHandler struct{ service *services.MonitoringService }
func NewMonitoringHandler(s *services.MonitoringService) *MonitoringHandler { return &MonitoringHandler{service: s} }

func (h *MonitoringHandler) SystemInfo(c *fiber.Ctx) error {
	info, err := h.service.SystemInfo(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, info)
}
func (h *MonitoringHandler) Metrics(c *fiber.Ctx) error {
	metrics, err := h.service.LiveMetrics(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, metrics)
}
func (h *MonitoringHandler) Services(c *fiber.Ctx) error {
	svcs, err := h.service.ServiceStatus(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, svcs)
}
func (h *MonitoringHandler) History(c *fiber.Ctx) error {
	metric := c.Query("metric", "cpu"); period := c.Query("period", "24h"); interval := c.Query("interval", "5m")
	data, err := h.service.HistoricalMetrics(c.UserContext(), metric, period, interval)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}
func (h *MonitoringHandler) GetAlerts(c *fiber.Ctx) error {
	config, err := h.service.GetAlertsConfig(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, config)
}
func (h *MonitoringHandler) UpdateAlerts(c *fiber.Ctx) error {
	var body map[string]interface{}; if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateAlertsConfig(c.UserContext(), body); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Alert configuration updated", nil)
}

// ServerInformation backs the WHM Server Information page — per-CPU
// details, the boot-time memory line, uname, physical disks, raw `free`
// output and a per-mount disk table.
func (h *MonitoringHandler) ServerInformation(c *fiber.Ctx) error {
	info, err := h.service.ServerInformation(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, info)
}

// ServiceStatusSummary backs the WHM Service Status page — running
// services with versions plus the load/memory/swap/disk rollup.
func (h *MonitoringHandler) ServiceStatusSummary(c *fiber.Ctx) error {
	sum, err := h.service.ServiceStatusSummary(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, sum)
}
