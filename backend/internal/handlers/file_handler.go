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
	var body struct {
		User      string `json:"user"`
		Path      string `json:"path"`
		Permanent bool   `json:"permanent"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.DeleteFile(c.UserContext(), body.User, body.Path, body.Permanent); err != nil {
		return response.InternalError(c, err.Error())
	}
	msg := "Moved to Trash"
	if body.Permanent {
		msg = "Deleted"
	}
	return response.SuccessMessage(c, msg, nil)
}

// ── Trash ──────────────────────────────────────────────
// Three thin handlers wrapping the service. User is passed as a query
// param on GETs and in the body on mutations, mirroring the rest of
// this file.

func (h *FileHandler) ListTrash(c *fiber.Ctx) error {
	user := c.Query("user")
	data, err := h.service.ListTrash(c.UserContext(), user)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, data)
}

func (h *FileHandler) RestoreTrash(c *fiber.Ctx) error {
	var body struct {
		User      string `json:"user"`
		ID        string `json:"id"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.RestoreFromTrash(c.UserContext(), body.User, body.ID, body.Overwrite); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Restored", nil)
}

// DeleteTrash handles both "delete one trashed item" and "empty the
// whole trash". The client sends id="" (or omits it) to mean "empty
// everything"; the service translates that to the "*" sentinel.
func (h *FileHandler) DeleteTrash(c *fiber.Ctx) error {
	var body struct {
		User string `json:"user"`
		ID   string `json:"id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	id := body.ID
	if id == "" {
		id = "*"
	}
	if err := h.service.DeleteFromTrash(c.UserContext(), body.User, id); err != nil {
		return response.InternalError(c, err.Error())
	}
	msg := "Trash emptied"
	if body.ID != "" {
		msg = "Deleted permanently"
	}
	return response.SuccessMessage(c, msg, nil)
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

func (h *FileHandler) PasswordProtect(c *fiber.Ctx) error {
	var body struct{ User, Path, Username, Password, Label string }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.PasswordProtect(c.UserContext(), body.User, body.Path, body.Username, body.Password, body.Label); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Directory protected", nil)
}

func (h *FileHandler) Unprotect(c *fiber.Ctx) error {
	var body struct{ User, Path string }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Unprotect(c.UserContext(), body.User, body.Path); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Protection removed", nil)
}

func (h *FileHandler) Info(c *fiber.Ctx) error {
	user := c.Query("user"); path := c.Query("path")
	data, err := h.service.GetInfo(c.UserContext(), user, path)
	if err != nil { return response.NotFound(c, err.Error()) }
	return response.Success(c, data)
}
