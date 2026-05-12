package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type TransferHandler struct {
	service *services.TransferService
}

func NewTransferHandler(s *services.TransferService) *TransferHandler {
	return &TransferHandler{service: s}
}

func (h *TransferHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	jobs, total, err := h.service.List(c.UserContext(), page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, jobs, page, limit, total)
}

func (h *TransferHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	job, err := h.service.GetByID(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "Transfer job not found")
	}
	return response.Success(c, job)
}

func (h *TransferHandler) Create(c *fiber.Ctx) error {
	var req models.CreateTransferRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	job, err := h.service.Create(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, job)
}

func (h *TransferHandler) Cancel(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Cancel(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Transfer cancelled", nil)
}

func (h *TransferHandler) TestConnection(c *fiber.Ctx) error {
	var req models.TestConnectionRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	if err := h.service.TestConnection(c.UserContext(), &req); err != nil {
		return response.InternalError(c, "Connection failed: "+err.Error())
	}
	return response.SuccessMessage(c, "Connection successful", nil)
}

func (h *TransferHandler) Discover(c *fiber.Ctx) error {
	var req models.DiscoverRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	data, err := h.service.Discover(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, data)
}

// RehydrateAll runs every Rebuild*FromMongo method (mailboxes,
// forwarders, ssh_keys, dns, mysql access, ftp, wordpress) in
// sequence. Idempotent — safe to call after every transfer or
// whenever the operator notices "rows in panel UI but feature
// dead". Returns per-area counts.
//
// Same code path the post-3.1.48 transfer panel-records sync calls
// at its end + the bzpanel heal-after-transfer CLI subcommand.
// Used as a recovery surface when:
//   * a transfer's panel-records-only re-run skipped filesystem
//     rehydrate
//   * the operator restored Mongo from backup but not /etc/dovecot,
//     /etc/postfix, /var/spool/cron, etc.
//   * a manual edit of /etc/dovecot/users left it stale relative
//     to the panel
func (h *TransferHandler) RehydrateAll(c *fiber.Ctx) error {
	res := h.service.RunAllRehydrates(c.UserContext())
	return response.Success(c, res)
}
