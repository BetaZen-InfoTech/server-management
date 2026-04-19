package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

type UserHandler struct {
	service *services.UserService
	// auth is optional — wired by SetAuthService so the WHM Vendors page
	// can mint short-lived impersonation tokens. Left nil on older wiring
	// paths and the Impersonate handler returns a friendly error.
	auth *services.AuthService
}

func NewUserHandler(s *services.UserService) *UserHandler {
	return &UserHandler{service: s}
}

// SetAuthService wires the auth service after construction so the
// Impersonate handler can call AuthService.Impersonate. Called from
// main.go right after NewUserHandler.
func (h *UserHandler) SetAuthService(a *services.AuthService) {
	h.auth = a
}

// vendorResponse is the row shape returned by the WHM Vendors page. It
// summarises a tenant root account (vendor_admin) plus a couple of useful
// rollup counts so the frontend table can render without N round-trips.
type vendorResponse struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Status       string `json:"status"`
	PackageID    string `json:"package_id"`
	PackageName  string `json:"package_name"`
	TeamCount    int64  `json:"team_count"`
	DomainCount  int64  `json:"domain_count"`
	StorageBytes int64  `json:"storage_bytes"`
	CreatedAt    string `json:"createdAt"`
	LastLogin    string `json:"lastLogin"`
	// Trash fields — present only when DeletedAt is set; the frontend
	// uses them to render a countdown in the Trash tab.
	DeletedAt      string `json:"deleted_at,omitempty"`
	TrashExpiresAt string `json:"trash_expires_at,omitempty"`
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
	// Always strict: the Users & RBAC page on WHM is for platform-level
	// accounts (owner + the support/developer staff they created). Vendor
	// accounts live under /admin/vendors and their tenant team members
	// live under the cPanel users page, so we never want them bleeding
	// into this list — even for vendor_owner, who bypasses other filters.
	users, total, err := h.service.List(c.UserContext(), page, limit, search, role, tenantHex, true)
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
	role, tenantHex, userHex := callerCtx(c)
	isSuper, _ := c.Locals("is_super_admin").(bool)
	if err := h.service.Delete(c.UserContext(), id, role, tenantHex, userHex, isSuper); err != nil {
		return response.BadRequest(c, err.Error(), nil)
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
		// Team = every non-vendor user whose tenant_id points at this
		// vendor. Rolls up staff + customers + developers + support into
		// a single "managed users under this vendor" count so it matches
		// the "Managed Users" header card when summed across rows.
		teamCount, _ := h.service.CountByRole(ctx, bson.M{
			"tenant_id": v.ID,
			"_id":       bson.M{"$ne": v.ID},
		})
		// Domain count was hardcoded to 0 — now actually queries the
		// domains collection for rows whose `user` matches any username
		// under this vendor's tenant. Failures fall through as 0 rather
		// than failing the whole list render; the badge stays neutral.
		domainCount, _ := h.service.CountDomainsForTenant(ctx, v.ID)
		// Storage is read from the 5-min cache — the first request after
		// a restart pays one `du -sb` round-trip per vendor, subsequent
		// loads return the cached bytes instantly.
		storage := h.service.VendorStorageBytes(ctx, v.Username, false)
		packageID := ""
		if v.PackageID != nil {
			packageID = v.PackageID.Hex()
		}
		result[i] = vendorResponse{
			ID:           v.ID.Hex(),
			Username:     v.Username,
			Name:         v.Name,
			Email:        v.Email,
			Status:       status,
			PackageID:    packageID,
			PackageName:  v.PackageName,
			TeamCount:    teamCount,
			DomainCount:  domainCount,
			StorageBytes: storage,
			CreatedAt:    v.CreatedAt.Format("2006-01-02 15:04"),
			LastLogin:    lastLogin,
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

	// Scope team + managed counts to users whose tenant_id points at
	// an ACTIVE vendor. Without this filter, orphan rows (users whose
	// parent vendor was deleted or suspended) inflated the platform
	// totals, producing "Managed Users: 1" in the header even when
	// every vendor's row showed "0 team" — the bug the Vendors page
	// screenshot flagged.
	activeVendorIDs, err := h.service.ActiveVendorIDs(ctx)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	tenantFilter := bson.M{"tenant_id": bson.M{"$in": activeVendorIDs}}

	totalTeamMembers, err := h.service.CountByRole(ctx, bson.M{
		"role":      "vendor_staff",
		"tenant_id": tenantFilter["tenant_id"],
	})
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	totalManagedUsers, err := h.service.CountByRole(ctx, bson.M{
		"role":      bson.M{"$in": bson.A{"vendor_staff", "customer", "developer", "support"}},
		"tenant_id": tenantFilter["tenant_id"],
	})
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	totalDomains, err := h.service.CountTotalDomains(ctx)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, fiber.Map{
		"total_vendors":       totalVendors,
		"active_vendors":      activeVendors,
		"total_team_members":  totalTeamMembers,
		"total_managed_users": totalManagedUsers,
		"total_domains":       totalDomains,
	})
}

// AdminTrashVendor soft-deletes a vendor: locks their linux account,
// stops their systemd apps + cron, and flips deleted_at on. The record
// survives in trash for 15 days (see services.trashRetention) before
// the background purger fires.
func (h *UserHandler) AdminTrashVendor(c *fiber.Ctx) error {
	id := c.Params("id")
	_, _, userHex := callerCtx(c)
	isSuper, _ := c.Locals("is_super_admin").(bool)
	if err := h.service.Trash(c.UserContext(), id, userHex, isSuper); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Vendor moved to trash (15-day recovery window)", nil)
}

// AdminRestoreVendor pulls a trashed vendor back, unlocks their linux
// account, and re-enables the record. Services aren't auto-started —
// the admin can start them individually from the Apps page once the
// vendor is back.
func (h *UserHandler) AdminRestoreVendor(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Restore(c.UserContext(), id); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Vendor restored", nil)
}

// AdminListTrashedVendors returns the paginated list of soft-deleted
// vendors for the Trash tab. Each row carries deleted_at and
// trash_expires_at so the UI can render a countdown.
func (h *UserHandler) AdminListTrashedVendors(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 50)
	ctx := c.UserContext()

	users, total, err := h.service.ListTrashed(ctx, page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	// Only show tenant roots (vendors) here; staff/customers in a
	// deleted tenant get their own trash-listing UI later if needed.
	result := make([]vendorResponse, 0, len(users))
	for _, v := range users {
		if v.Role != "vendor_admin" {
			continue
		}
		deleted := ""
		expires := ""
		if v.DeletedAt != nil {
			deleted = v.DeletedAt.Format("2006-01-02 15:04")
		}
		if v.TrashExpiresAt != nil {
			expires = v.TrashExpiresAt.Format("2006-01-02 15:04")
		}
		packageID := ""
		if v.PackageID != nil {
			packageID = v.PackageID.Hex()
		}
		result = append(result, vendorResponse{
			ID:             v.ID.Hex(),
			Username:       v.Username,
			Name:           v.Name,
			Email:          v.Email,
			Status:         "trashed",
			PackageID:      packageID,
			PackageName:    v.PackageName,
			CreatedAt:      v.CreatedAt.Format("2006-01-02 15:04"),
			DeletedAt:      deleted,
			TrashExpiresAt: expires,
		})
	}
	return response.Paginated(c, result, page, limit, total)
}

// AdminUpdateVendorPackage swaps a vendor's hosting package. Body:
//
//	{ "package_id": "<hex>" }
//
// The service validates the package exists and caches its name on the
// user row so subsequent list renders don't re-join.
func (h *UserHandler) AdminUpdateVendorPackage(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		PackageID string `json:"package_id"`
	}
	if err := c.BodyParser(&body); err != nil || body.PackageID == "" {
		return response.BadRequest(c, "package_id is required", nil)
	}
	if err := h.service.UpdatePackage(c.UserContext(), id, body.PackageID); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Package updated", nil)
}

// AdminVendorStorage returns bytes used by the vendor's home dir. Used
// to refresh the Storage column in the Vendors table without doing a
// full list reload. Query ?force=1 bypasses the 5-minute cache so the
// admin can get fresh bytes after a big change.
func (h *UserHandler) AdminVendorStorage(c *fiber.Ctx) error {
	id := c.Params("id")
	force := c.QueryBool("force", false)
	role, tenantHex, _ := callerCtx(c)
	u, err := h.service.GetByID(c.UserContext(), id, role, tenantHex)
	if err != nil {
		return response.NotFound(c, "vendor not found")
	}
	bytes := h.service.VendorStorageBytes(c.UserContext(), u.Username, force)
	return response.Success(c, fiber.Map{
		"username":      u.Username,
		"storage_bytes": bytes,
	})
}

// AdminImpersonateVendor mints a short-lived access token for the target
// vendor and returns it so the frontend can drop it into localStorage and
// navigate into the vendor's panel. Gated on server.manage — only the
// platform owner can cross tenant boundaries this way.
//
// The returned JWT carries `impersonated_by = <admin user id>` so the
// frontend can show an "Impersonating as X" banner and every audit log
// entry made while the token is active identifies the real actor.
func (h *UserHandler) AdminImpersonateVendor(c *fiber.Ctx) error {
	if h.auth == nil {
		return response.InternalError(c, "impersonation not wired — SetAuthService was not called")
	}
	targetID := c.Params("id")
	_, _, adminUserHex := callerCtx(c)
	if adminUserHex == "" {
		return response.Unauthorized(c, "admin identity missing from token")
	}
	// Extra guard: the caller must be the platform owner. The route-level
	// RequirePermission("server.manage") already blocks everyone else but
	// we belt-and-brace here since impersonation crosses tenant bounds.
	if role, _ := c.Locals("role").(string); role != "vendor_owner" {
		return response.Forbidden(c, "only the platform owner can impersonate vendors")
	}
	resp, err := h.auth.Impersonate(c.UserContext(), adminUserHex, targetID)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, resp)
}
