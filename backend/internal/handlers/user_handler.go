package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

type UserHandler struct {
	service *services.UserService
}

func NewUserHandler(s *services.UserService) *UserHandler {
	return &UserHandler{service: s}
}

// vendorResponse is the row shape returned by the WHM Vendors page. It
// summarises a tenant root account (vendor_admin) plus a couple of useful
// rollup counts so the frontend table can render without N round-trips.
type vendorResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	Status      string `json:"status"`
	TeamCount   int64  `json:"team_count"`
	DomainCount int64  `json:"domain_count"`
	CreatedAt   string `json:"createdAt"`
	LastLogin   string `json:"lastLogin"`
}

// userResponse maps backend User model to frontend-expected format
type userResponse struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
	LastLogin string `json:"lastLogin"`
}

func mapRoleToFrontend(role string) string {
	switch role {
	case "vendor_owner":
		return "admin"
	case "vendor_admin":
		return "vendor"
	case "vendor_staff":
		return "staff"
	case "developer":
		return "operator"
	case "support":
		return "operator"
	case "customer":
		return "viewer"
	default:
		return role
	}
}

// callerCtx pulls the requester's role + tenant + user ID from fiber locals
// (set by middleware.Auth) so the handler can pass them to UserService methods
// for tenant scoping. Returns empty strings for any missing claim.
func callerCtx(c *fiber.Ctx) (role, tenantHex, userHex string) {
	if v, ok := c.Locals("role").(string); ok {
		role = v
	}
	if v, ok := c.Locals("tenant_id").(string); ok {
		tenantHex = v
	}
	if v, ok := c.Locals("user_id").(string); ok {
		userHex = v
	}
	return
}

func (h *UserHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 50)
	search := c.Query("search")

	role, tenantHex, _ := callerCtx(c)
	users, total, err := h.service.List(c.UserContext(), page, limit, search, role, tenantHex)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	// Map to frontend format
	result := make([]userResponse, len(users))
	for i, u := range users {
		status := "active"
		if !u.IsActive {
			status = "suspended"
		}
		lastLogin := ""
		if u.LastLogin != nil {
			lastLogin = u.LastLogin.Format("2006-01-02 15:04")
		}
		result[i] = userResponse{
			ID:        u.ID.Hex(),
			Username:  u.Username,
			Name:      u.Name,
			Email:     u.Email,
			Role:      mapRoleToFrontend(u.Role),
			Status:    status,
			CreatedAt: u.CreatedAt.Format("2006-01-02 15:04"),
			LastLogin: lastLogin,
		}
	}

	return response.Paginated(c, result, page, limit, total)
}

func (h *UserHandler) Create(c *fiber.Ctx) error {
	var body struct {
		Username      string `json:"username"`
		Name          string `json:"name"`
		Email         string `json:"email"`
		Password      string `json:"password"`
		Role          string `json:"role"`
		PackageID     string `json:"package_id"`
		PrimaryDomain string `json:"primary_domain"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if body.Username == "" || body.Name == "" || body.Email == "" || body.Password == "" {
		return response.BadRequest(c, "Username, name, email, and password are required", nil)
	}
	// Package is required for customer/vendor roles
	if body.PackageID == "" && (body.Role == "vendor" || body.Role == "customer") {
		return response.BadRequest(c, "Package is required for customer/vendor accounts", nil)
	}

	role, _, userHex := callerCtx(c)
	user, err := h.service.Create(c.UserContext(), body.Username, body.Name, body.Email, body.Password, body.Role, body.PackageID, body.PrimaryDomain, role, userHex)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Created(c, userResponse{
		ID:        user.ID.Hex(),
		Username:  user.Username,
		Name:      user.Name,
		Email:     user.Email,
		Role:      mapRoleToFrontend(user.Role),
		Status:    "active",
		CreatedAt: user.CreatedAt.Format("2006-01-02 15:04"),
	})
}

func (h *UserHandler) Suspend(c *fiber.Ctx) error {
	id := c.Params("id")
	role, tenantHex, _ := callerCtx(c)
	if err := h.service.Suspend(c.UserContext(), id, role, tenantHex); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "User suspended", nil)
}

func (h *UserHandler) Activate(c *fiber.Ctx) error {
	id := c.Params("id")
	role, tenantHex, _ := callerCtx(c)
	if err := h.service.Activate(c.UserContext(), id, role, tenantHex); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "User activated", nil)
}

func (h *UserHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	role, tenantHex, _ := callerCtx(c)
	if err := h.service.Delete(c.UserContext(), id, role, tenantHex); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "User deleted", nil)
}

// Get returns a single user by ID — used by the WHM Edit modal so it can
// pre-fill the form with the current values.
func (h *UserHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	role, tenantHex, _ := callerCtx(c)
	u, err := h.service.GetByID(c.UserContext(), id, role, tenantHex)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	pkgID := ""
	if u.PackageID != nil {
		pkgID = u.PackageID.Hex()
	}
	status := "active"
	if !u.IsActive {
		status = "suspended"
	}
	return response.Success(c, fiber.Map{
		"id":           u.ID.Hex(),
		"username":     u.Username,
		"name":         u.Name,
		"email":        u.Email,
		"role":         mapRoleToFrontend(u.Role),
		"package_id":   pkgID,
		"package_name": u.PackageName,
		"status":       status,
	})
}

// Update applies a partial update from the WHM Edit modal.
func (h *UserHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")
	var in services.UpdateInput
	if err := c.BodyParser(&in); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	role, tenantHex, _ := callerCtx(c)
	u, err := h.service.Update(c.UserContext(), id, &in, role, tenantHex)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, fiber.Map{
		"id":    u.ID.Hex(),
		"name":  u.Name,
		"email": u.Email,
		"role":  mapRoleToFrontend(u.Role),
	})
}

// ResetPassword sets a fresh password for a user (admin action). The
// matching Linux account is updated too so SSH/FTP keep working.
func (h *UserHandler) ResetPassword(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		Password string `json:"password"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	role, tenantHex, _ := callerCtx(c)
	if err := h.service.ResetPassword(c.UserContext(), id, body.Password, role, tenantHex); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Password reset", nil)
}

// AdminListVendors returns a paginated list of tenant root accounts
// (vendor_admin role) for the WHM Vendors page. Platform-owner only —
// gated at the route layer via server.manage. For each row we also fetch
// a team count (other users sharing that vendor's tenant_id) so the table
// can show "X team members" without an extra request per vendor.
func (h *UserHandler) AdminListVendors(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 50)
	ctx := c.UserContext()

	users, total, err := h.service.ListByRole(ctx, "vendor_admin", page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	result := make([]vendorResponse, len(users))
	for i, v := range users {
		status := "active"
		if !v.IsActive {
			status = "suspended"
		}
		lastLogin := ""
		if v.LastLogin != nil {
			lastLogin = v.LastLogin.Format("2006-01-02 15:04")
		}
		teamCount, _ := h.service.CountByRole(ctx, bson.M{
			"tenant_id": v.ID,
			"_id":       bson.M{"$ne": v.ID},
		})
		result[i] = vendorResponse{
			ID:          v.ID.Hex(),
			Username:    v.Username,
			Name:        v.Name,
			Email:       v.Email,
			Status:      status,
			TeamCount:   teamCount,
			DomainCount: 0,
			CreatedAt:   v.CreatedAt.Format("2006-01-02 15:04"),
			LastLogin:   lastLogin,
		}
	}

	return response.Paginated(c, result, page, limit, total)
}

// AdminVendorStats returns aggregate counts for the Vendors page header
// card: total / active vendor accounts plus total team members and total
// managed users across the platform. Platform-owner only.
func (h *UserHandler) AdminVendorStats(c *fiber.Ctx) error {
	ctx := c.UserContext()

	totalVendors, err := h.service.CountByRole(ctx, bson.M{"role": "vendor_admin"})
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	activeVendors, err := h.service.CountByRole(ctx, bson.M{
		"role":      "vendor_admin",
		"is_active": true,
	})
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	totalTeamMembers, err := h.service.CountByRole(ctx, bson.M{"role": "vendor_staff"})
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	totalManagedUsers, err := h.service.CountByRole(ctx, bson.M{
		"role": bson.M{"$in": bson.A{"vendor_staff", "customer", "developer", "support"}},
	})
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, fiber.Map{
		"total_vendors":       totalVendors,
		"active_vendors":      activeVendors,
		"total_team_members":  totalTeamMembers,
		"total_managed_users": totalManagedUsers,
	})
}
