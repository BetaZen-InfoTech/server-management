package handlers

import (
	"fmt"

	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type FileHandler struct{ service *services.FileService }
func NewFileHandler(s *services.FileService) *FileHandler { return &FileHandler{service: s} }

func (h *FileHandler) ListDir(c *fiber.Ctx) error {
	user := c.Query("user"); path := c.Query("path", "/")
	data, err := h.service.ListDirectory(c.UserContext(), user, path)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}
func (h *FileHandler) ReadFile(c *fiber.Ctx) error {
	user := c.Query("user"); path := c.Query("path")
	data, err := h.service.ReadFile(c.UserContext(), user, path)
	if err != nil { return response.NotFound(c, "File not found") }
	return response.Success(c, data)
}
func (h *FileHandler) CreateFile(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Path string `json:"path"`; Content string `json:"content"`; Type string `json:"type"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if body.Type == "directory" {
		if err := h.service.Mkdir(c.UserContext(), body.User, body.Path); err != nil { return response.InternalError(c, err.Error()) }
		return response.SuccessMessage(c, "Folder created", nil)
	}
	if err := h.service.CreateFile(c.UserContext(), body.User, body.Path, body.Content); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "File created", nil)
}
func (h *FileHandler) EditFile(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Path string `json:"path"`; Content string `json:"content"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.EditFile(c.UserContext(), body.User, body.Path, body.Content); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "File updated", nil)
}
func (h *FileHandler) DeleteFile(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Path string `json:"path"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.DeleteFile(c.UserContext(), body.User, body.Path); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Deleted", nil)
}
func (h *FileHandler) Upload(c *fiber.Ctx) error {
	user := c.FormValue("user"); path := c.FormValue("path")
	form, err := c.MultipartForm()
	if err != nil { return response.BadRequest(c, "Invalid multipart form", nil) }
	// Accept both "files" (multi) and "file" (legacy single) field names.
	files := form.File["files"]
	if len(files) == 0 {
		files = form.File["file"]
	}
	if len(files) == 0 { return response.BadRequest(c, "At least one file is required", nil) }
	if err := h.service.Upload(c.UserContext(), user, path, files); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, fmt.Sprintf("%d file(s) uploaded", len(files)), nil)
}

func (h *FileHandler) Copy(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Sources []string `json:"sources"`; Destination string `json:"destination"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Copy(c.UserContext(), body.User, body.Sources, body.Destination); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Copied", nil)
}

func (h *FileHandler) Move(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Sources []string `json:"sources"`; Destination string `json:"destination"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Move(c.UserContext(), body.User, body.Sources, body.Destination); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Moved", nil)
}

func (h *FileHandler) Search(c *fiber.Ctx) error {
	user := c.Query("user"); path := c.Query("path", "/"); query := c.Query("q")
	data, err := h.service.Search(c.UserContext(), user, path, query)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}

func (h *FileHandler) Download(c *fiber.Ctx) error {
	user := c.Query("user"); path := c.Query("path")
	abs, err := h.service.DownloadPath(c.UserContext(), user, path)
	if err != nil { return response.NotFound(c, err.Error()) }
	return c.Download(abs)
}
func (h *FileHandler) Rename(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Source string `json:"source"`; Destination string `json:"destination"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Rename(c.UserContext(), body.User, body.Source, body.Destination); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Renamed", nil)
}
func (h *FileHandler) Chmod(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Path string `json:"path"`; Permissions string `json:"permissions"`; Recursive bool `json:"recursive"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Chmod(c.UserContext(), body.User, body.Path, body.Permissions, body.Recursive); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Permissions updated", nil)
}
func (h *FileHandler) Compress(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Paths []string `json:"paths"`; Output string `json:"output"`; Format string `json:"format"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Compress(c.UserContext(), body.User, body.Paths, body.Output, body.Format); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Compressed", nil)
}
func (h *FileHandler) Extract(c *fiber.Ctx) error {
	var body struct{ User string `json:"user"`; Archive string `json:"archive"`; Destination string `json:"destination"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Extract(c.UserContext(), body.User, body.Archive, body.Destination); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Extracted", nil)
}
