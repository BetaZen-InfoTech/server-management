package handlers

import (
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type PackageHandler struct {
	service *services.PackageService
	// userSvc is wired by SetUserService so ApproveRequest can invoke
	// UpdatePackage without PackageService needing a UserService dep
	// (which would create an import cycle).
	userSvc *services.UserService
}

func NewPackageHandler(s *services.PackageService) *PackageHandler {
	return &PackageHandler{service: s}
}

// SetUserService wires the user service after construction so the
// Approve handler can apply the vendor's package swap. main.go calls
// this right after NewPackageHandler.
func (h *PackageHandler) SetUserService(u *services.UserService) {
	h.userSvc = u
}

func (h *PackageHandler) List(c *fiber.Ctx) error {
	search := c.Query("search")
	packages, err := h.service.List(c.UserContext(), search)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, packages)
}

func (h *PackageHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	pkg, err := h.service.GetByID(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "Package not found")
	}
	return response.Success(c, pkg)
}

func (h *PackageHandler) Create(c *fiber.Ctx) error {
	var req models.CreatePackageRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	createdBy := c.Locals("user_id").(string)
	pkg, err := h.service.Create(c.UserContext(), &req, createdBy)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, pkg)
}

func (h *PackageHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	pkg, err := h.service.Update(c.UserContext(), id, body)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, pkg)
}

func (h *PackageHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Delete(c.UserContext(), id); err != nil {
		if strings.Contains(err.Error(), "active accounts") {
			return response.BadRequest(c, err.Error(), nil)
		}
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Package deleted", nil)
}

// ----------------------------------------------------------------------
// Package change requests — vendor plan-switch flow
// ----------------------------------------------------------------------

// RequestChange (POST /packages/request-change) — vendor submits an
// upgrade/downgrade request that sits pending until the platform owner
// confirms the payment externally and approves it.
func (h *PackageHandler) RequestChange(c *fiber.Ctx) error {
	var body struct {
		TargetPackageID string `json:"target_package_id"`
		Note            string `json:"note"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if body.TargetPackageID == "" {
		return response.BadRequest(c, "target_package_id is required", nil)
	}
	userHex, ok := c.Locals("user_id").(string)
	if !ok || userHex == "" {
		return response.Unauthorized(c, "missing user context")
	}
	vendorID, err := primitive.ObjectIDFromHex(userHex)
	if err != nil {
		return response.BadRequest(c, "invalid user id", nil)
	}
	req, err := h.service.RequestChange(c.UserContext(), vendorID, body.TargetPackageID, body.Note)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, req)
}

// MyRequest (GET /packages/my-request) — the vendor's own latest pending
// request, or null if none. Drives the "waiting on admin review" banner
// on the vendor-side Packages page.
func (h *PackageHandler) MyRequest(c *fiber.Ctx) error {
	userHex, ok := c.Locals("user_id").(string)
	if !ok || userHex == "" {
		return response.Unauthorized(c, "missing user context")
	}
	vendorID, err := primitive.ObjectIDFromHex(userHex)
	if err != nil {
		return response.BadRequest(c, "invalid user id", nil)
	}
	req, err := h.service.MyPendingRequest(c.UserContext(), vendorID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, req)
}

// ListRequests (GET /admin/package-requests?status=pending) — platform
// owner's queue of change requests. Default status=pending so the admin
// lands on what actually needs action; ?status= returns everything.
func (h *PackageHandler) ListRequests(c *fiber.Ctx) error {
	status := c.Query("status", "pending")
	rows, err := h.service.ListRequests(c.UserContext(), status)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, rows)
}

// ApproveRequest (POST /admin/package-requests/:id/approve) — admin
// confirms payment externally, then approves. Applies the package swap
// to the vendor's user record via UserService.UpdatePackage.
func (h *PackageHandler) ApproveRequest(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		PaymentReference string `json:"payment_reference"`
		Note             string `json:"note"`
	}
	_ = c.BodyParser(&body)
	admin, _ := c.Locals("email").(string)
	if h.userSvc == nil {
		return response.InternalError(c, "user service not wired")
	}
	if err := h.service.ApproveRequest(c.UserContext(), id, admin, body.PaymentReference, body.Note, h.userSvc.UpdatePackage); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Request approved, package swapped", nil)
}

// RejectRequest (POST /admin/package-requests/:id/reject) — admin
// declines with a reason. Vendor's package stays where it was; the
// request row sticks around as rejected history.
func (h *PackageHandler) RejectRequest(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.BodyParser(&body)
	admin, _ := c.Locals("email").(string)
	if err := h.service.RejectRequest(c.UserContext(), id, admin, body.Reason); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Request rejected", nil)
}
