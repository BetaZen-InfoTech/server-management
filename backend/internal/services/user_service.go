package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

var usernameRegex = regexp.MustCompile(`^[a-z][a-z0-9]{2,15}$`)

type UserService struct {
	db     *mongo.Database
	domain *DomainService
}

func NewUserService(db *mongo.Database) *UserService {
	return &UserService{db: db}
}

// SetDomainService injects the DomainService so fresh-install provisioning
// can spin up a full domain stack (vhost, PHP pool, DNS zone, SSL, mail, FTP)
// on user creation. Optional — when nil, user create stays minimal.
func (s *UserService) SetDomainService(d *DomainService) {
	s.domain = d
}

// List returns users visible to the caller. vendor_owner sees every user;
// every other role only sees users belonging to their own tenant. The role
// and tenant come from the JWT claims via fiber locals.
func (s *UserService) List(ctx context.Context, page, limit int, search, callerRole, callerTenantHex string) ([]models.User, int64, error) {
	col := s.db.Collection(database.ColUsers)

	filter := bson.M{}
	if search != "" {
		filter["$or"] = bson.A{
			bson.M{"name": bson.M{"$regex": search, "$options": "i"}},
			bson.M{"email": bson.M{"$regex": search, "$options": "i"}},
			bson.M{"username": bson.M{"$regex": search, "$options": "i"}},
		}
	}
	if err := s.applyTenantFilter(filter, callerRole, callerTenantHex); err != nil {
		return nil, 0, err
	}

	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().
		SetSkip(skip).
		SetLimit(int64(limit)).
		SetSort(bson.M{"created_at": -1})

	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var users []models.User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

// applyTenantFilter mutates the filter so it only matches users belonging
// to the caller's tenant. vendor_owner is unrestricted. Tenant-scoped callers
// match tenant_id == their tenant OR _id == their tenant (so the tenant root
// itself shows up even if its tenant_id is unset due to legacy data).
func (s *UserService) applyTenantFilter(filter bson.M, callerRole, callerTenantHex string) error {
	if !constants.IsTenantScoped(callerRole) {
		return nil
	}
	if callerTenantHex == "" {
		// No tenant id on the JWT — show nothing rather than everything.
		filter["_id"] = bson.M{"$in": bson.A{}}
		return nil
	}
	tid, err := primitive.ObjectIDFromHex(callerTenantHex)
	if err != nil {
		return errors.New("invalid tenant id")
	}
	filter["$or"] = bson.A{
		bson.M{"tenant_id": tid},
		bson.M{"_id": tid},
	}
	return nil
}

// assertSameTenant returns an error if the target user is not visible to the
// caller. Used by every mutating method (Get/Update/Delete/Suspend/...) so a
// vendor cannot poke at users in another tenant by guessing IDs.
func (s *UserService) assertSameTenant(ctx context.Context, targetID primitive.ObjectID, callerRole, callerTenantHex string) error {
	if !constants.IsTenantScoped(callerRole) {
		return nil
	}
	var target models.User
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": targetID}).Decode(&target); err != nil {
		return errors.New("user not found")
	}
	tid, err := primitive.ObjectIDFromHex(callerTenantHex)
	if err != nil {
		return errors.New("invalid tenant id")
	}
	// Caller's own tenant root account always passes.
	if target.ID == tid {
		return nil
	}
	if !target.TenantID.IsZero() && target.TenantID == tid {
		return nil
	}
	return errors.New("user not found")
}

func (s *UserService) GetByUsername(ctx context.Context, username string) (*models.User, error) {
	col := s.db.Collection(database.ColUsers)
	var user models.User
	if err := col.FindOne(ctx, bson.M{"username": username}).Decode(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

// Create provisions a new user record + linux account. callerRole / callerUserHex
// describe the user issuing the request and are used to:
//   - reject vendors trying to create vendor_owner / vendor_admin accounts
//   - stamp tenant_id and parent_user_id so multi-tenant scoping works
//   - jail the linux account when it's a child of a vendor tenant
func (s *UserService) Create(ctx context.Context, username, name, email, password, role, packageID, primaryDomain, callerRole, callerUserHex string) (*models.User, error) {
	col := s.db.Collection(database.ColUsers)

	// Map frontend role label early so the role checks below operate on the
	// canonical backend identifier.
	backendRole := mapFrontendRole(role)

	// Privilege checks: only vendor_owner can create owners or vendor admins.
	// vendor_admin can create vendor_staff / customer / developer / support
	// inside their own tenant. Anyone else creating users is rejected up at
	// the route layer, but we belt-and-brace here too.
	if backendRole == constants.RoleVendorOwner && callerRole != constants.RoleVendorOwner {
		return nil, errors.New("only the platform owner can create admin accounts")
	}
	if backendRole == constants.RoleVendorAdmin && callerRole != constants.RoleVendorOwner {
		return nil, errors.New("only the platform owner can create vendor accounts")
	}

	// Validate username format
	if !usernameRegex.MatchString(username) {
		return nil, errors.New("username must be 3-16 lowercase alphanumeric characters, starting with a letter")
	}

	// Check if username already exists
	count, _ := col.CountDocuments(ctx, bson.M{"username": username})
	if count > 0 {
		return nil, errors.New("username already taken")
	}

	// Check if email already exists
	count, _ = col.CountDocuments(ctx, bson.M{"email": email})
	if count > 0 {
		return nil, errors.New("user with this email already exists")
	}

	// Validate and resolve package
	var pkgOID *primitive.ObjectID
	var pkgName string
	if packageID != "" {
		oid, err := primitive.ObjectIDFromHex(packageID)
		if err != nil {
			return nil, errors.New("invalid package ID")
		}
		var pkg models.HostingPackage
		if err := s.db.Collection(database.ColPackages).FindOne(ctx, bson.M{"_id": oid}).Decode(&pkg); err != nil {
			return nil, errors.New("package not found")
		}
		pkgOID = &oid
		pkgName = pkg.Name
	}

	// Create Linux user on the server
	if err := agent.CreateLinuxUser(ctx, username, password); err != nil {
		return nil, fmt.Errorf("failed to create system user: %w", err)
	}

	// Create home directory structure
	if err := agent.CreateUserDirectories(ctx, username); err != nil {
		return nil, fmt.Errorf("failed to create user directories: %w", err)
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, errors.New("failed to hash password")
	}

	// Assign default permissions for the role
	perms := constants.DefaultPermissions[backendRole]

	// Resolve tenant + parent. Tenant roots (vendor_owner, vendor_admin) own
	// themselves; everyone else inherits from the caller.
	newID := primitive.NewObjectID()
	var (
		tenantID     primitive.ObjectID
		parentUserID *primitive.ObjectID
	)
	if constants.IsTenantRoot(backendRole) {
		tenantID = newID
	} else {
		// Default: caller's tenant. Falls back to the new user being its own
		// tenant only when the caller is missing (e.g. seeded fixtures).
		if callerUserHex != "" {
			callerOID, perr := primitive.ObjectIDFromHex(callerUserHex)
			if perr == nil {
				var caller models.User
				if err := col.FindOne(ctx, bson.M{"_id": callerOID}).Decode(&caller); err == nil {
					if !caller.TenantID.IsZero() {
						tenantID = caller.TenantID
					} else {
						tenantID = caller.ID
					}
					pid := caller.ID
					parentUserID = &pid
				}
			}
		}
		if tenantID.IsZero() {
			tenantID = newID
		}
	}

	now := time.Now()
	user := models.User{
		ID:           newID,
		Username:     username,
		Email:        email,
		Password:     string(hashedPassword),
		Name:         name,
		Role:         backendRole,
		TenantID:     tenantID,
		ParentUserID: parentUserID,
		PackageID:    pkgOID,
		PackageName:  pkgName,
		Permissions:  perms,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	_, err = col.InsertOne(ctx, user)
	if err != nil {
		return nil, err
	}

	// Tenant child users get jailed so SSH lands in /home/<u> with no path
	// out. vendor_owner and vendor_admin keep a normal /bin/bash shell so
	// they can do ops over real SSH.
	if !constants.IsTenantRoot(backendRole) {
		if err := agent.JailUser(ctx, username); err != nil {
			// Don't unwind the user — they're still usable via the panel.
			fmt.Fprintf(os.Stderr, "warning: jailkit failed for %s: %v\n", username, err)
		}
	}

	// Increment package account count
	if pkgOID != nil {
		s.db.Collection(database.ColPackages).UpdateOne(ctx, bson.M{"_id": *pkgOID},
			bson.M{"$inc": bson.M{"account_count": 1}})
	}

	// Fresh-install provisioning: if a primary domain was supplied and the
	// DomainService is wired up, spin up the full stack (dir, PHP-FPM pool,
	// nginx vhost, DNS zone, Let's Encrypt SSL, admin mailbox, root FTP).
	// Errors here are logged but do not fail user creation — the account is
	// already usable and the admin can retry domain creation from the UI.
	if primaryDomain != "" && s.domain != nil {
		domReq := &models.CreateDomainRequest{
			Domain:     primaryDomain,
			User:       username,
			PHPVersion: "8.2",
		}
		if _, domErr := s.domain.Create(ctx, domReq); domErr != nil {
			fmt.Fprintf(os.Stderr, "warning: fresh-install domain provision failed for %s: %v\n", primaryDomain, domErr)
		}
	}

	return &user, nil
}

func (s *UserService) Suspend(ctx context.Context, id, callerRole, callerTenantHex string) error {
	col := s.db.Collection(database.ColUsers)

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("invalid user ID")
	}
	if err := s.assertSameTenant(ctx, objID, callerRole, callerTenantHex); err != nil {
		return err
	}
	// Load the user once so we can drive the cascade (linux lock +
	// stop owned systemd units + clear cron) before flipping the flag.
	// If any of those fail we still persist is_active=false so the flag
	// and the DB agree — failing steps log to stderr.
	var user models.User
	if err := col.FindOne(ctx, bson.M{"_id": objID}).Decode(&user); err != nil {
		return errors.New("user not found")
	}
	if user.Username != "" {
		_, _ = agent.RunCommand(ctx, "usermod", "-L", user.Username)
	}
	suspendOwnedServices(ctx, user.Username)

	result, err := col.UpdateByID(ctx, objID, bson.M{
		"$set": bson.M{
			"is_active":  false,
			"updated_at": time.Now(),
		},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return errors.New("user not found")
	}
	return nil
}

func (s *UserService) Activate(ctx context.Context, id, callerRole, callerTenantHex string) error {
	col := s.db.Collection(database.ColUsers)

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("invalid user ID")
	}
	if err := s.assertSameTenant(ctx, objID, callerRole, callerTenantHex); err != nil {
		return err
	}
	// Unlock the linux account. Services aren't auto-started — ops may
	// want to inspect before resuming traffic — but the cron backup
	// taken at suspend time can be restored manually from
	// /var/spool/cron/crontabs.bak_<user>.
	var user models.User
	if err := col.FindOne(ctx, bson.M{"_id": objID}).Decode(&user); err == nil && user.Username != "" {
		_, _ = agent.RunCommand(ctx, "usermod", "-U", user.Username)
	}

	result, err := col.UpdateByID(ctx, objID, bson.M{
		"$set": bson.M{
			"is_active":  true,
			"updated_at": time.Now(),
		},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return errors.New("user not found")
	}
	return nil
}

func (s *UserService) Delete(ctx context.Context, id, callerRole, callerTenantHex string) error {
	col := s.db.Collection(database.ColUsers)

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("invalid user ID")
	}
	if err := s.assertSameTenant(ctx, objID, callerRole, callerTenantHex); err != nil {
		return err
	}

	// Get user to find username for system cleanup
	var user models.User
	if err := col.FindOne(ctx, bson.M{"_id": objID}).Decode(&user); err != nil {
		return errors.New("user not found")
	}

	// Delete Linux user and home directory
	if user.Username != "" {
		agent.DeleteLinuxUser(ctx, user.Username)
	}

	result, err := col.DeleteOne(ctx, bson.M{"_id": objID})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return errors.New("user not found")
	}

	// Decrement package account count
	if user.PackageID != nil {
		s.db.Collection(database.ColPackages).UpdateOne(ctx, bson.M{"_id": *user.PackageID},
			bson.M{"$inc": bson.M{"account_count": -1}})
	}
	return nil
}

// GetByID looks up a single user by hex ObjectID. Used by the WHM Edit
// modal to populate the form with the current values. Vendors can only
// load users from their own tenant.
func (s *UserService) GetByID(ctx context.Context, id, callerRole, callerTenantHex string) (*models.User, error) {
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, errors.New("invalid user ID")
	}
	if err := s.assertSameTenant(ctx, objID, callerRole, callerTenantHex); err != nil {
		return nil, err
	}
	var u models.User
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": objID}).Decode(&u); err != nil {
		return nil, errors.New("user not found")
	}
	return &u, nil
}

// UpdateInput is the set of fields the WHM Edit modal can change. Pointer
// fields mean "leave as-is when nil"; an empty string is treated as "no
// change" so an accidentally-blank input doesn't wipe a value.
type UpdateInput struct {
	Name      *string `json:"name"`
	Email     *string `json:"email"`
	Role      *string `json:"role"`
	PackageID *string `json:"package_id"`
	IsActive  *bool   `json:"is_active"`
}

// Update applies a partial update to a user. Role values come from the
// frontend in their UI form ("admin", "vendor", "operator", "viewer") and
// are translated back to the backend's internal role identifiers. Vendors
// can only edit users in their own tenant and cannot promote anyone to a
// vendor_owner / vendor_admin role.
func (s *UserService) Update(ctx context.Context, id string, in *UpdateInput, callerRole, callerTenantHex string) (*models.User, error) {
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, errors.New("invalid user ID")
	}
	if err := s.assertSameTenant(ctx, objID, callerRole, callerTenantHex); err != nil {
		return nil, err
	}

	// Vendors cannot use Update to escalate a user to a tenant-root role.
	if in.Role != nil && *in.Role != "" && constants.IsTenantScoped(callerRole) {
		newBackendRole := mapFrontendRole(*in.Role)
		if constants.IsTenantRoot(newBackendRole) {
			return nil, errors.New("you cannot promote a user to admin or vendor")
		}
	}

	set := bson.M{"updated_at": time.Now()}
	if in.Name != nil && *in.Name != "" {
		set["name"] = *in.Name
	}
	if in.Email != nil && *in.Email != "" {
		set["email"] = *in.Email
	}
	if in.Role != nil && *in.Role != "" {
		backendRole := mapFrontendRole(*in.Role)
		set["role"] = backendRole
		// Refresh permissions when role changes so the user immediately gets
		// the right access on their next request.
		set["permissions"] = constants.DefaultPermissions[backendRole]
	}
	if in.PackageID != nil && *in.PackageID != "" {
		pkgID, perr := primitive.ObjectIDFromHex(*in.PackageID)
		if perr != nil {
			return nil, errors.New("invalid package_id")
		}
		set["package_id"] = pkgID
		// Best-effort: also store the package name for display.
		var pkg struct {
			Name string `bson:"name"`
		}
		if err := s.db.Collection(database.ColPackages).FindOne(ctx, bson.M{"_id": pkgID}).Decode(&pkg); err == nil {
			set["package_name"] = pkg.Name
		}
	}
	if in.IsActive != nil {
		set["is_active"] = *in.IsActive
	}

	col := s.db.Collection(database.ColUsers)
	res, err := col.UpdateByID(ctx, objID, bson.M{"$set": set})
	if err != nil {
		return nil, err
	}
	if res.MatchedCount == 0 {
		return nil, errors.New("user not found")
	}

	var updated models.User
	if err := col.FindOne(ctx, bson.M{"_id": objID}).Decode(&updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

// ResetPassword sets a new bcrypt-hashed password for the user. Used by
// the WHM "Reset password" action so an admin can hand a fresh credential
// to a customer who lost theirs. The Linux account password (if any) is
// updated to match so SSH/FTP keep working.
func (s *UserService) ResetPassword(ctx context.Context, id, newPassword, callerRole, callerTenantHex string) error {
	if len(newPassword) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("invalid user ID")
	}
	if err := s.assertSameTenant(ctx, objID, callerRole, callerTenantHex); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	col := s.db.Collection(database.ColUsers)
	var u models.User
	if err := col.FindOne(ctx, bson.M{"_id": objID}).Decode(&u); err != nil {
		return errors.New("user not found")
	}
	if _, err := col.UpdateByID(ctx, objID, bson.M{"$set": bson.M{
		"password":   string(hash),
		"updated_at": time.Now(),
	}}); err != nil {
		return err
	}

	// Best effort: sync the matching Linux account password so the user can
	// keep using SSH / FTP without a separate reset. Ignore errors — not
	// every user has a Linux account.
	if u.Username != "" {
		agent.SetLinuxUserPassword(ctx, u.Username, newPassword)
	}
	return nil
}

// ListByRole returns users with the given backend role. Caller must be
// vendor_owner — enforced at the route layer via server.manage permission.
// Used by the WHM Vendors page.
func (s *UserService) ListByRole(ctx context.Context, role string, page, limit int) ([]models.User, int64, error) {
	col := s.db.Collection(database.ColUsers)
	// Exclude trashed rows — active listings never show them. The
	// trash view uses ListTrashed instead.
	filter := bson.M{"role": role, "deleted_at": bson.M{"$in": bson.A{nil, primitive.Null{}}}}
	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.M{"created_at": -1})
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	var users []models.User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// ListTrashed returns every user currently in the trash (deleted_at
// set). Sorted newest-first so admins see their most recent soft-
// delete at the top. Used by the Vendors → Trash tab.
func (s *UserService) ListTrashed(ctx context.Context, page, limit int) ([]models.User, int64, error) {
	col := s.db.Collection(database.ColUsers)
	filter := bson.M{"deleted_at": bson.M{"$exists": true, "$ne": nil}}
	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.M{"deleted_at": -1})
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	var users []models.User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// CountByRole counts users matching a role expression. Adds the
// deleted_at:nil gate automatically so stats don't include trashed
// rows. Callers that genuinely want trashed users counted must query
// the collection directly.
func (s *UserService) CountByRole(ctx context.Context, filter bson.M) (int64, error) {
	if _, ok := filter["deleted_at"]; !ok {
		filter["deleted_at"] = bson.M{"$in": bson.A{nil, primitive.Null{}}}
	}
	return s.db.Collection(database.ColUsers).CountDocuments(ctx, filter)
}

// ActiveVendorIDs returns the _id of every active vendor_admin on the
// platform. Used by the Vendors stats endpoint to scope "managed users"
// counts to users actually attached to a live vendor — without this
// filter an orphaned user (leftover from a deleted vendor) inflates the
// platform total and makes the header card disagree with the per-vendor
// "Users" column.
func (s *UserService) ActiveVendorIDs(ctx context.Context) ([]primitive.ObjectID, error) {
	cur, err := s.db.Collection(database.ColUsers).Find(ctx,
		bson.M{"role": constants.RoleVendorAdmin, "is_active": true},
		options.Find().SetProjection(bson.M{"_id": 1}),
	)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var rows []struct {
		ID primitive.ObjectID `bson:"_id"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	ids := make([]primitive.ObjectID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// CountDomainsForTenant returns the number of domains owned by any
// user in the given tenant. Looks up usernames under that tenant first
// (domains reference users by `user` string, not by tenant_id), then
// counts matching rows in the domains collection. Returns 0 when the
// tenant has no users, not an error.
func (s *UserService) CountDomainsForTenant(ctx context.Context, tenantID primitive.ObjectID) (int64, error) {
	userCur, err := s.db.Collection(database.ColUsers).Find(ctx,
		bson.M{"tenant_id": tenantID},
		options.Find().SetProjection(bson.M{"username": 1}),
	)
	if err != nil {
		return 0, err
	}
	defer userCur.Close(ctx)
	var rows []struct {
		Username string `bson:"username"`
	}
	if err := userCur.All(ctx, &rows); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	usernames := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Username != "" {
			usernames = append(usernames, r.Username)
		}
	}
	if len(usernames) == 0 {
		return 0, nil
	}
	return s.db.Collection(database.ColDomains).CountDocuments(ctx,
		bson.M{"user": bson.M{"$in": usernames}},
	)
}

// CountTotalDomains returns the platform-wide domain count for the
// Vendors stats header.
func (s *UserService) CountTotalDomains(ctx context.Context) (int64, error) {
	return s.db.Collection(database.ColDomains).CountDocuments(ctx, bson.M{})
}

func mapFrontendRole(role string) string {
	switch role {
	case "admin":
		return constants.RoleVendorOwner
	case "vendor":
		return constants.RoleVendorAdmin
	case "staff":
		return constants.RoleVendorStaff
	case "operator":
		return constants.RoleDeveloper
	case "viewer":
		return constants.RoleCustomer
	default:
		return role
	}
}

// ----------------------------------------------------------------------
// Soft-delete (trash) + admin vendor management
// ----------------------------------------------------------------------

// trashRetention is how long a soft-deleted user stays in the trash
// before the background purger wipes them permanently. 15 days matches
// what the product page promises — "for 15 days you can recover".
const trashRetention = 15 * 24 * time.Hour

// Trash soft-deletes a user. The linux account is LOCKED (usermod -L)
// and services owned by the user are stopped, but the home directory
// and DB record are preserved until trashRetention elapses. A caller
// with Restore rights can flip the flag back before then.
func (s *UserService) Trash(ctx context.Context, id string) error {
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("invalid user ID")
	}
	col := s.db.Collection(database.ColUsers)
	var user models.User
	if err := col.FindOne(ctx, bson.M{"_id": objID}).Decode(&user); err != nil {
		return errors.New("user not found")
	}
	if user.Role == constants.RoleVendorOwner {
		return errors.New("cannot trash the platform owner")
	}
	now := time.Now()
	expires := now.Add(trashRetention)
	// Lock the linux account + stop services so the vendor can't log in
	// or serve traffic while they sit in trash. DeleteLinuxUser would be
	// irreversible; usermod -L is a reversible lock that Restore can
	// undo via usermod -U.
	if user.Username != "" {
		_, _ = agent.RunCommand(ctx, "usermod", "-L", user.Username)
	}
	suspendOwnedServices(ctx, user.Username)
	if _, err := col.UpdateByID(ctx, objID, bson.M{
		"$set": bson.M{
			"deleted_at":       now,
			"trash_expires_at": expires,
			"is_active":        false,
			"updated_at":       now,
		},
	}); err != nil {
		return err
	}
	return nil
}

// Restore pulls a user back out of the trash, unlocks the linux
// account, and flips is_active back on. Services remain whatever state
// they were in — the admin can start them individually from the Apps
// page once the owner is back.
func (s *UserService) Restore(ctx context.Context, id string) error {
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("invalid user ID")
	}
	col := s.db.Collection(database.ColUsers)
	var user models.User
	if err := col.FindOne(ctx, bson.M{"_id": objID}).Decode(&user); err != nil {
		return errors.New("user not found")
	}
	if user.DeletedAt == nil {
		return errors.New("user is not in trash")
	}
	if user.Username != "" {
		_, _ = agent.RunCommand(ctx, "usermod", "-U", user.Username)
	}
	if _, err := col.UpdateByID(ctx, objID, bson.M{
		"$set": bson.M{
			"is_active":  true,
			"updated_at": time.Now(),
		},
		"$unset": bson.M{
			"deleted_at":       "",
			"trash_expires_at": "",
		},
	}); err != nil {
		return err
	}
	return nil
}

// PurgeExpiredTrash runs the permanent-deletion pass. Called every
// few hours from a background goroutine in main.go. For each user
// whose trash_expires_at is past, the linux account + home dir are
// wiped and the DB row is removed. Idempotent — zero rows means no-op.
func (s *UserService) PurgeExpiredTrash(ctx context.Context) (int, error) {
	col := s.db.Collection(database.ColUsers)
	cur, err := col.Find(ctx, bson.M{
		"deleted_at":       bson.M{"$exists": true, "$ne": nil},
		"trash_expires_at": bson.M{"$lte": time.Now()},
	})
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)
	var expired []models.User
	if err := cur.All(ctx, &expired); err != nil {
		return 0, err
	}
	purged := 0
	for _, u := range expired {
		if u.Username != "" {
			_ = agent.DeleteLinuxUser(ctx, u.Username)
		}
		if _, err := col.DeleteOne(ctx, bson.M{"_id": u.ID}); err == nil {
			purged++
		}
	}
	return purged, nil
}

// suspendOwnedServices stops / disables things a suspended user was
// running: systemd apps named sp-app-<anything> owned by them, their
// cron jobs, their FTP/SSH logins (already blocked by usermod -L).
// Best-effort; every step logs its own errors and the call returns
// without short-circuiting so one failing service doesn't leave the
// others running.
func suspendOwnedServices(ctx context.Context, username string) {
	if username == "" {
		return
	}
	// Stop every app systemd unit whose User= equals the target. The
	// `grep + awk` pipeline keeps the logic in a single RunCommand so
	// we don't need to round-trip the list of unit names through Go.
	script := fmt.Sprintf(
		"for unit in $(systemctl list-units --all 'sp-app-*' --no-legend 2>/dev/null | awk '{print $1}'); do "+
			"owner=$(systemctl show -p User --value $unit 2>/dev/null); "+
			"if [ \"$owner\" = %q ]; then systemctl stop $unit 2>/dev/null; fi; "+
			"done",
		username,
	)
	_, _ = agent.RunCommand(ctx, "bash", "-c", script)
	// Clear active cron jobs; they're preserved as a backup at
	// /var/spool/cron/crontabs.bak_<user> so restore can revive them.
	_, _ = agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"crontab -u %q -l > /var/spool/cron/crontabs.bak_%s 2>/dev/null; crontab -u %q -r 2>/dev/null",
		username, username, username,
	))
}

// UpdatePackage swaps a user's PackageID + cached PackageName. Used by
// the Vendors → Update Package action. Doesn't retroactively resize
// existing domains — package quota changes take effect on the next
// domain create / resize.
func (s *UserService) UpdatePackage(ctx context.Context, userID, packageID string) error {
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return errors.New("invalid user ID")
	}
	pID, err := primitive.ObjectIDFromHex(packageID)
	if err != nil {
		return errors.New("invalid package ID")
	}
	// Load the package so we can cache its name on the user record.
	// Avoids an extra join every time we render a vendor row.
	var pkg struct {
		Name string `bson:"name"`
	}
	if err := s.db.Collection(database.ColPackages).FindOne(ctx, bson.M{"_id": pID}).Decode(&pkg); err != nil {
		return errors.New("package not found")
	}
	res, err := s.db.Collection(database.ColUsers).UpdateByID(ctx, uID, bson.M{
		"$set": bson.M{
			"package_id":   pID,
			"package_name": pkg.Name,
			"updated_at":   time.Now(),
		},
	})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errors.New("user not found")
	}
	return nil
}

// vendorStorageCache memoises `du -sb /home/<user>` so the Vendors
// page doesn't fan-out N disk walks on every render. Entries are
// considered stale after vendorStorageCacheTTL, after which the next
// request re-runs du and replaces the entry.
type vendorStorageEntry struct {
	bytes   int64
	fetched time.Time
}

var (
	vendorStorageCache    = map[string]vendorStorageEntry{}
	vendorStorageCacheMu  = &sync.Mutex{}
	vendorStorageCacheTTL = 5 * time.Minute
)

// VendorStorageBytes returns the disk bytes used by the user's home
// directory. Cached for 5 minutes per username. Returns 0 when the
// user has no provisioned home or du fails.
func (s *UserService) VendorStorageBytes(ctx context.Context, username string, force bool) int64 {
	if username == "" {
		return 0
	}
	vendorStorageCacheMu.Lock()
	if !force {
		if e, ok := vendorStorageCache[username]; ok && time.Since(e.fetched) < vendorStorageCacheTTL {
			vendorStorageCacheMu.Unlock()
			return e.bytes
		}
	}
	vendorStorageCacheMu.Unlock()

	home := fmt.Sprintf("/home/%s", username)
	res, err := agent.RunCommand(ctx, "du", "-sb", home)
	if err != nil || res == nil {
		return 0
	}
	// Output: "<bytes>\t<path>\n"
	var bytes int64
	_, _ = fmt.Sscanf(res.Output, "%d", &bytes)

	vendorStorageCacheMu.Lock()
	vendorStorageCache[username] = vendorStorageEntry{bytes: bytes, fetched: time.Now()}
	vendorStorageCacheMu.Unlock()
	return bytes
}
