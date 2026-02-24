package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type FileHandler struct{ service *services.FileService }
func NewFileHandler(s *services.FileService) *FileHandler { return &FileHandler{service: s} }

func (h *FileHandler) ListDir(c *fiber.Ctx) error {
	user := c.Query("user"); path := c.Query("path", "/")
	data, err := h.service.ListDirectory(c.Context(), user, path)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}
func (h *FileHandler) ReadFile(c *fiber.Ctx) error {
	user := c.Query("user"); path := c.Query("path")
	data, err := h.service.ReadFile(c.Context(), user, path)
	if err != nil { return response.NotFound(c, "File not found") }
	return response.Success(c, data)
}
func (h *FileHandler) CreateFile(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Path string `json:"path"`; Content string `json:"content"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.CreateFile(c.Context(), body.User, body.Path, body.Content); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "File created", nil)
}
func (h *FileHandler) EditFile(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Path string `json:"path"`; Content string `json:"content"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.EditFile(c.Context(), body.User, body.Path, body.Content); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "File updated", nil)
}
func (h *FileHandler) DeleteFile(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Path string `json:"path"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.DeleteFile(c.Context(), body.User, body.Path); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Deleted", nil)
}
func (h *FileHandler) Upload(c *fiber.Ctx) error {
	user := c.FormValue("user"); path := c.FormValue("path")
	file, err := c.FormFile("file"); if err != nil { return response.BadRequest(c, "File is required", nil) }
	if err := h.service.Upload(c.Context(), user, path, file); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "File uploaded", nil)
}
func (h *FileHandler) Rename(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Source string `json:"source"`; Destination string `json:"destination"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Rename(c.Context(), body.User, body.Source, body.Destination); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Renamed", nil)
}
func (h *FileHandler) Chmod(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Path string `json:"path"`; Permissions string `json:"permissions"`; Recursive bool `json:"recursive"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Chmod(c.Context(), body.User, body.Path, body.Permissions, body.Recursive); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Permissions updated", nil)
}
func (h *FileHandler) Compress(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Paths []string `json:"paths"`; Output string `json:"output"`; Format string `json:"format"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Compress(c.Context(), body.User, body.Paths, body.Output, body.Format); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Compressed", nil)
}
func (h *FileHandler) Extract(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Archive string `json:"archive"`; Destination string `json:"destination"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Extract(c.Context(), body.User, body.Archive, body.Destination); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Extracted", nil)
}
