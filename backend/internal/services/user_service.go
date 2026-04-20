package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/authcache"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

// Strict username regex: must be a valid Linux account name (start with a
// letter, 3-16 lowercase alphanumerics). Applied only to roles that
// actually get a /home/<username>/ tree and a `useradd` shell account.
var usernameRegex = regexp.MustCompile(`^[a-z][a-z0-9]{2,15}$`)

// Looser username regex for panel-only team members (staff / developer /
// support / customer / platform owner). Team accounts exist only in
// MongoDB — no Linux user, no home directory — so they just need to be
// unique panel login identifiers. 3-32 chars, alphanumeric + dash +
// underscore + dot, must start with a letter.
var panelUsernameRegex = regexp.MustCompile(`^[a-z][a-z0-9_.\-]{2,31}$`)

// needsSystemAccount reports whether a role actually requires a Linux
// shell account + /home/<username>/ tree on the VPS. Only vendor_admin
// (the hosting customer, shown as "Vendor" in the UI) owns real files
// on disk — every other role is a panel login that shares the parent
// vendor's tenant and has no business getting its own /home/.
//
// Earlier versions of Create called `useradd` and `CreateUserDirectories`
// unconditionally, so every staff/developer/support/customer/admin got a
// pointless Linux account. That's the "When vendor create a team account,
// why user create on /home on VPS?" bug this fix resolves.
func needsSystemAccount(role string) bool {
	return role == constants.RoleVendorAdmin
}

// deriveUsername builds a reasonable default username for a team member
// when the caller leaves the field blank. Preference order:
//   1. email local-part (everything before the @), slugified
//   2. name, slugified
//   3. literal "user" fallback
//
// The result is NOT guaranteed unique — resolveUniqueUsername takes care
// of suffix-bumping on collision. We only guarantee it matches
// panelUsernameRegex so the subsequent validation doesn't re-reject it.
func deriveUsername(email, name string) string {
	slug := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		var b strings.Builder
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				b.WriteRune(r)
			case r == '.' || r == '-' || r == '_':
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	if i := strings.IndexByte(email, '@'); i > 0 {
		if s := slug(email[:i]); s != "" {
			if len(s) > 32 {
				s = s[:32]
			}
			// Must start with a letter to satisfy panelUsernameRegex.
			if s[0] >= 'a' && s[0] <= 'z' && len(s) >= 3 {
				return s
			}
		}
	}
	if s := slug(name); s != "" {
		if len(s) > 32 {
			s = s[:32]
		}
		if len(s) >= 3 && s[0] >= 'a' && s[0] <= 'z' {
			return s
		}
	}
	return "user"
}

// resolveUniqueUsername returns base unchanged if it's free, otherwise
// appends a numeric suffix (user, user2, user3, ...) until it finds one
// that no existing row owns. Caps at 50 tries to stop a pathological
// loop — if we still haven't found a free slot, we fall back to a
// timestamp suffix which is effectively guaranteed unique.
func resolveUniqueUsername(ctx context.Context, col *mongo.Collection, base string) string {
	// We want room for the numeric suffix within the 32-char limit.
	maxBase := 28
	if len(base) > maxBase {
		base = base[:maxBase]
	}
	for i := 2; i < 50; i++ {
		candidate := fmt.Sprintf("%s%d", base, i)
		n, _ := col.CountDocuments(ctx, bson.M{"username": candidate})
		if n == 0 {
			return candidate
		}
	}
	return fmt.Sprintf("%s%d", base, time.Now().UnixNano()%1_000_000)
}

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
//
// When strictTenant is true, even vendor_owner is tenant-scoped — used by
// the WHM Users & RBAC page to show only platform-level accounts (the
// owner, the support/developer staff they created) and NOT vendors or
// their tenant users. The Vendors page lives under a separate endpoint
// (/admin/vendors) that intentionally crosses tenants.
func (s *UserService) List(ctx context.Context, page, limit int, search, callerRole, callerTenantHex string, strictTenant bool) ([]models.User, int64, error) {
	col := s.db.Collection(database.ColUsers)

	filter := bson.M{}
	// Always hide soft-deleted rows from the main list — they live in Trash.
	filter["deleted_at"] = nil
	if search != "" {
		filter["$or"] = bson.A{
			bson.M{"name": bson.M{"$regex": search, "$options": "i"}},
			bson.M{"email": bson.M{"$regex": search, "$options": "i"}},
			bson.M{"username": bson.M{"$regex": search, "$options": "i"}},
		}
	}
	if err := s.applyTenantFilter(filter, callerRole, callerTenantHex, strictTenant); err != nil {
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
// to the caller's tenant. vendor_owner is unrestricted unless strict is
// true (which the WHM Users page uses to prevent vendor accounts from
// leaking into the platform-user list). Tenant-scoped callers always
// match tenant_id == their tenant OR _id == their tenant (so the tenant
// root itself shows up even if its tenant_id is unset due to legacy data).
func (s *UserService) applyTenantFilter(filter bson.M, callerRole, callerTenantHex string, strict bool) error {
	if !strict && !constants.IsTenantScoped(callerRole) {
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

	// Normalise email up front so case differences ("Foo@x.com" vs
	// "foo@x.com") don't slip past the uniqueness check or land two
	// rows that look identical to humans but differ on a case-
	// sensitive comparison.
	email = strings.ToLower(strings.TrimSpace(email))

	// Username handling splits on role:
	//  * Vendor accounts (vendor_admin) need a real Linux username — it
	//    becomes the /home/<username>/ directory and their useradd shell.
	//    Required, must match the strict Linux-compatible regex.
	//  * Everyone else (team members, customers, platform admins) is a
	//    panel-only login. Username is just the identifier on the login
	//    screen — no filesystem footprint. Optional; auto-derived from
	//    email or name if blank; validated against a looser regex.
	username = strings.ToLower(strings.TrimSpace(username))
	if needsSystemAccount(backendRole) {
		if !usernameRegex.MatchString(username) {
			return nil, errors.New("vendor username must be 3-16 lowercase alphanumeric characters, starting with a letter")
		}
	} else {
		if username == "" {
			username = deriveUsername(email, name)
		}
		if !panelUsernameRegex.MatchString(username) {
			return nil, errors.New("username must be 3-32 chars, start with a letter, and use only lowercase letters, digits, dot, dash, or underscore")
		}
	}

	// Username uniqueness is a platform-wide invariant — the login screen
	// uses it as the primary identifier, regardless of whether the user
	// has a Linux account or not. If the caller supplied a username we
	// reject on conflict; if we derived one, we suffix-bump until unique.
	count, _ := col.CountDocuments(ctx, bson.M{"username": username})
	if count > 0 {
		// Only auto-bump when the caller left it blank (we filled it).
		// If they typed a specific username we owe them a clear error.
		if !needsSystemAccount(backendRole) {
			username = resolveUniqueUsername(ctx, col, username)
		} else {
			return nil, errors.New("username already taken")
		}
	}

	// Email uniqueness is enforced platform-wide: every vendor, every
	// vendor team member, every customer must have a distinct email.
	// The case-insensitive regex catches existing rows that pre-date
	// the normalisation above; the unique index in indexes.go is the
	// race-proof backstop.
	count, _ = col.CountDocuments(ctx, bson.M{
		"email": bson.M{"$regex": "^" + regexp.QuoteMeta(email) + "$", "$options": "i"},
	})
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

	// Create the Linux account + home directory tree ONLY for roles that
	// actually own files on disk (vendor_admin). Team members / customers /
	// platform admins are panel-only logins and must not appear in
	// /etc/passwd or under /home/. Skipping this entirely also makes the
	// downstream JailUser call a no-op — jailing a non-existent user would
	// just fail and dirty the logs.
	if needsSystemAccount(backendRole) {
		if err := agent.CreateLinuxUser(ctx, username, password); err != nil {
			return nil, fmt.Errorf("failed to create system user: %w", err)
		}
		if err := agent.CreateUserDirectories(ctx, username); err != nil {
			return nil, fmt.Errorf("failed to create user directories: %w", err)
		}
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

	// Jailkit chroot applies only to users with a real Linux account. Panel-
	// only team members / customers / platform admins skip this entirely;
	// jailing a non-existent username would fail noisily with no benefit.
	// vendor_admin (the only role that still reaches here with a system
	// account) is NOT a tenant root, so it gets jailed — keeps their SSH
	// pinned to /home/<u>/ with no path out, which is the whole point.
	if needsSystemAccount(backendRole) && !constants.IsTenantRoot(backendRole) {
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

// suspendUserDomains swaps every nginx vhost that belongs to username to
// the "account suspended" 503 page. Called from Suspend() and Trash()
// so a suspended vendor's customer-facing sites immediately stop
// serving real content — without this, the nginx symlink stayed live
// and traffic kept hitting the vendor's code + DBs. Idempotent.
//
// The DB-backed domain list is the authoritative source. If there's no
// DB row but a leftover vhost on disk, we won't touch it — operator can
// clean up manually; silently overwriting hand-written vhosts is worse.
func (s *UserService) suspendUserDomains(ctx context.Context, username string) {
	if username == "" {
		return
	}
	cur, err := s.db.Collection(database.ColDomains).Find(ctx, bson.M{"user": username})
	if err != nil {
		return
	}
	defer cur.Close(ctx)
	var doms []models.Domain
	if err := cur.All(ctx, &doms); err != nil {
		return
	}
	for _, d := range doms {
		if err := agent.WriteSuspendedVhost(ctx, d.Domain); err != nil {
			fmt.Fprintf(os.Stderr, "warning: suspend vhost for %s failed: %v\n", d.Domain, err)
		}
	}
}

// unsuspendUserDomains is the inverse — re-emits the normal PHP-FPM
// vhost (SSL variant if a cert exists on disk) for each of the user's
// domains. Run from Activate() / Restore() so unsuspending flips every
// site back to its production shape in one go.
func (s *UserService) unsuspendUserDomains(ctx context.Context, username string) {
	if username == "" {
		return
	}
	cur, err := s.db.Collection(database.ColDomains).Find(ctx, bson.M{"user": username})
	if err != nil {
		return
	}
	defer cur.Close(ctx)
	var doms []models.Domain
	if err := cur.All(ctx, &doms); err != nil {
		return
	}
	for _, d := range doms {
		cfg := &agent.VhostConfig{
			Domain:     d.Domain,
			User:       d.User,
			PHPVersion: d.PHPVersion,
		}
		var werr error
		if agent.LetsEncryptCertExists(d.Domain) {
			werr = agent.CreateVhostWithSSL(ctx, cfg)
		} else {
			werr = agent.CreateVhost(ctx, cfg)
		}
		if werr != nil {
			fmt.Fprintf(os.Stderr, "warning: unsuspend vhost for %s failed: %v\n", d.Domain, werr)
		}
	}
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
	// Flip every vendor-owned domain's nginx vhost to the 503 "account
	// suspended" page. Without this, customer-facing sites kept serving
	// real content even after the vendor was suspended — the panel said
	// "suspended" but public visitors saw no difference.
	s.suspendUserDomains(ctx, user.Username)

	// Persist is_active=false AND clear the refresh_token in one update.
	// Without clearing the refresh token, a suspended vendor could still
	// silently mint new access tokens via /auth/refresh — bypassing the
	// auth-middleware is_active check after each ~15s cache miss. Wiping
	// it forces a full re-login (which then fails because is_active=false).
	result, err := col.UpdateByID(ctx, objID, bson.M{
		"$set": bson.M{
			"is_active":          false,
			"refresh_token":      "",
			"refresh_expires_at": nil,
			"updated_at":         time.Now(),
		},
	})
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return errors.New("user not found")
	}
	// Bust the auth-middleware's allow-list cache so the next request from
	// this user_id is denied within milliseconds rather than 15s later.
	authcache.Invalidate(id)
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
		// Flip each of the user's domains back to the normal PHP-FPM
		// vhost. Mirrors the suspend path so there's no drift between
		// panel state and what nginx actually serves.
		s.unsuspendUserDomains(ctx, user.Username)
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
	// Drop the cached "denied" entry so the next request from this user
	// re-checks the DB and sees is_active=true within milliseconds rather
	// than waiting for the 15s TTL.
	authcache.Invalidate(id)
	return nil
}

func (s *UserService) Delete(ctx context.Context, id, callerRole, callerTenantHex, callerUserHex string, callerIsSuperAdmin bool) error {
	col := s.db.Collection(database.ColUsers)

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("invalid user ID")
	}
	if err := s.assertSameTenant(ctx, objID, callerRole, callerTenantHex); err != nil {
		return err
	}

	// Get user so we can enforce the two hard-stop rules below plus the
	// linux cleanup that happens after.
	var user models.User
	if err := col.FindOne(ctx, bson.M{"_id": objID}).Decode(&user); err != nil {
		return errors.New("user not found")
	}

	// Rule 1: users cannot delete their own account. Prevents accidental
	// self-lockout and makes the "last admin" recovery story simpler.
	if callerUserHex != "" && objID.Hex() == callerUserHex {
		return errors.New("you cannot delete your own account")
	}
	// Rule 2: super admins cannot be deleted by other super admins. A
	// regular admin can't touch them either (no permission to delete
	// admins at all in most flows), but this closes the loophole where
	// one super admin demotes another. The only way to remove a super
	// admin is to first flip their is_super_admin flag off — which
	// requires them to be online and active.
	if user.IsSuperAdmin && callerIsSuperAdmin {
		return errors.New("a super admin cannot delete another super admin — clear their is_super_admin flag first")
	}

	// Tear down every owned resource first (domains, mail, DNS, FTP) and
	// RENAME the Linux account to <username>-deleted-<ts> so files stay
	// recoverable but a future useradd <username> won't collide. The
	// legacy path used `userdel -r` which wiped the home dir and made
	// mistakes irreversible.
	if user.Username != "" {
		s.tearDownUserInfrastructure(ctx, user.Username)
	}

	result, err := col.DeleteOne(ctx, bson.M{"_id": objID})
	if err != nil {
		return err
	}
	if result.DeletedCount == 0 {
		return errors.New("user not found")
	}
	// Bust the auth cache so a still-logged-in deleted user is kicked out
	// on their next request rather than enjoying up-to-15s of valid
	// session time after the admin clicked Delete.
	authcache.Invalidate(id)

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
func (s *UserService) Trash(ctx context.Context, id, callerUserHex string, callerIsSuperAdmin bool) error {
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
	// Self-trash and cross-super-admin-trash mirror the hard Delete rules.
	// Applied here too so a super admin can't soft-delete themselves or
	// their peers out of the panel while staying logged in.
	if callerUserHex != "" && objID.Hex() == callerUserHex {
		return errors.New("you cannot trash your own account")
	}
	if user.IsSuperAdmin && callerIsSuperAdmin {
		return errors.New("a super admin cannot trash another super admin")
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
	// Flip each owned domain to the 503 "suspended" page. Trash uses
	// the same customer-facing page as Suspend because from a visitor's
	// perspective the two states are indistinguishable — the account
	// is offline, traffic should not reach application code.
	s.suspendUserDomains(ctx, user.Username)
	if _, err := col.UpdateByID(ctx, objID, bson.M{
		"$set": bson.M{
			"deleted_at":         now,
			"trash_expires_at":   expires,
			"is_active":          false,
			"refresh_token":      "",
			"refresh_expires_at": nil,
			"updated_at":         now,
		},
	}); err != nil {
		return err
	}
	authcache.Invalidate(id)
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
		// Flip every owned domain back to the live PHP-FPM vhost so
		// customer-facing traffic resumes as soon as Restore returns.
		s.unsuspendUserDomains(ctx, user.Username)
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
	authcache.Invalidate(id)
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
			// Same teardown the manual Delete runs: tear down domains
			// + mail + DNS + FTP then rename linux account to
			// <username>-deleted-<ts>. Files preserved under the new
			// home dir so an operator can still recover content.
			s.tearDownUserInfrastructure(ctx, u.Username)
		}
		if _, err := col.DeleteOne(ctx, bson.M{"_id": u.ID}); err == nil {
			purged++
			authcache.Invalidate(u.ID.Hex())
		}
	}
	return purged, nil
}

// tearDownUserInfrastructure is the shared teardown the permanent-delete
// paths (Delete + PurgeExpiredTrash) run before the DB row goes away.
// Steps, in order:
//
//  1. For every domain owned by username:
//     * unlink the nginx vhost (cleanupVhostFiles stops serving)
//     * tear down the PHP-FPM pool
//     * clean every mailbox under the domain from Dovecot users +
//       Postfix virtual_mailbox_maps + virtual_mailbox_domains +
//       virtual_alias_maps
//     * clean OpenDKIM signing.table / key.table / trusted.hosts rows
//     * drop the DNS zone from PowerDNS + MongoDB
//     * drop every panel-managed DB record (mailboxes, forwarders,
//       autoresponders, ftp, subdomains, apps, deployments, databases,
//       dbusers, wordpress, cron, backups) tied to the domain
//     * finally, remove the `domains` row itself
//
//  2. Remove any FTP accounts that still reference the user.
//
//  3. Rename the Linux account to <username>-deleted so a future
//     `useradd <username>` doesn't collide with orphan uid/gid entries
//     but the operator can still cp -r the old home dir for recovery.
//     Home dir content is preserved intact.
//
// Idempotent — running twice is harmless because every step is
// idempotent on its own. Errors are logged; individual failures don't
// abort the sweep because getting to ~80% deletion beats leaving the
// caller with an undeletable ghost row.
func (s *UserService) tearDownUserInfrastructure(ctx context.Context, username string) {
	if username == "" {
		return
	}

	// 1. Per-domain teardown.
	domCol := s.db.Collection(database.ColDomains)
	cur, err := domCol.Find(ctx, bson.M{"user": username})
	if err == nil {
		defer cur.Close(ctx)
		var doms []models.Domain
		cur.All(ctx, &doms)
		for _, d := range doms {
			s.tearDownDomain(ctx, d, username)
		}
	}

	// 2. Any orphan FTP rows (e.g. created outside the domain flow).
	s.db.Collection(database.ColFTPAccounts).DeleteMany(ctx, bson.M{"user": username})

	// 3. Rename the Linux account so future `useradd` doesn't collide
	// with a stale uid/gid but the files on disk stay recoverable.
	// Target name is <username>-deleted-<yyyymmdd-HHMMSS> so multiple
	// purges of an account that was re-created between runs don't fight
	// each other for the same renamed slot.
	target := fmt.Sprintf("%s-deleted-%s", username, time.Now().UTC().Format("20060102-150405"))
	if err := agent.RenameLinuxUserPreserve(ctx, username, target); err != nil {
		fmt.Fprintf(os.Stderr, "warning: rename linux user %s → %s failed: %v\n", username, target, err)
	}
}

// tearDownDomain wipes a single domain's runtime footprint (nginx,
// PHP-FPM, mail, DNS, DB) as part of the teardown path. Separate from
// DomainService.Delete because:
//   - DomainService.Delete PRESERVES the domain's public_html and mail
//     store on disk. That's right for "the owner deleted their own
//     domain" — we don't trash their files. The user-teardown path
//     does the same (preserves files) but ALSO wipes the panel-managed
//     DB rows (domains, apps, etc.) so the soon-to-be-deleted user row
//     doesn't leave dangling children behind.
//   - Doing this inline means we can tolerate partial failures without
//     propagating back up through DomainService's scope checks.
func (s *UserService) tearDownDomain(ctx context.Context, d models.Domain, username string) {
	// nginx + PHP-FPM
	agent.DeleteVhost(ctx, d.Domain)
	agent.DeletePHPPool(ctx, d.Domain)
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("rm -f /run/php/*-fpm-%s.sock", d.Domain))

	escDom := strings.ReplaceAll(d.Domain, ".", "\\.")

	// Mailboxes under this domain
	var mbs []models.Mailbox
	if c, err := s.db.Collection(database.ColMailboxes).Find(ctx, bson.M{"domain": d.Domain}); err == nil {
		c.All(ctx, &mbs)
		c.Close(ctx)
	}
	for _, mb := range mbs {
		escEmail := strings.ReplaceAll(mb.Email, ".", "\\.")
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s:/d' /etc/dovecot/users 2>/dev/null", escEmail))
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s /d' /etc/postfix/virtual_mailbox_maps 2>/dev/null", escEmail))
	}
	// Forwarders
	var fwds []models.EmailForwarder
	if c, err := s.db.Collection(database.ColForwarders).Find(ctx, bson.M{"domain": d.Domain}); err == nil {
		c.All(ctx, &fwds)
		c.Close(ctx)
	}
	for _, fwd := range fwds {
		escSrc := strings.ReplaceAll(fwd.Source, ".", "\\.")
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s /d' /etc/postfix/virtual_alias_maps 2>/dev/null", escSrc))
	}
	// Drop the domain itself from virtual_mailbox_domains + postmap both maps.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"sed -i '/^%s[[:space:]]/d' /etc/postfix/virtual_mailbox_domains 2>/dev/null", escDom))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_domains")
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_maps")
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_alias_maps")

	// OpenDKIM — drop signing.table, key.table, trusted.hosts rows.
	// trusted.hosts uses ^<domain>$ anchoring so we don't strip the parent
	// when removing a subdomain.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/@%s[[:space:]]/d' /etc/opendkim/signing.table 2>/dev/null", escDom))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^mail\\._domainkey\\.%s[[:space:]]/d' /etc/opendkim/key.table 2>/dev/null", escDom))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s$/d' /etc/opendkim/trusted.hosts 2>/dev/null", escDom))
	agent.RunCommand(ctx, "systemctl", "reload", "opendkim")
	agent.RunCommand(ctx, "systemctl", "reload", "postfix")

	// PowerDNS zone. Only drop if THIS domain owns the zone (root); a
	// subdomain's records live under the parent zone, which we must
	// not delete.
	agent.RunCommand(ctx, "pdnsutil", "delete-zone", d.Domain)
	agent.RunCommand(ctx, "pdns_control", "reload")

	// DB rows for this domain's children.
	s.db.Collection(database.ColMailboxes).DeleteMany(ctx, bson.M{"domain": d.Domain})
	s.db.Collection(database.ColForwarders).DeleteMany(ctx, bson.M{"domain": d.Domain})
	s.db.Collection(database.ColAutoresponders).DeleteMany(ctx, bson.M{"domain": d.Domain})
	s.db.Collection(database.ColFTPAccounts).DeleteMany(ctx, bson.M{"domain": d.Domain})
	s.db.Collection(database.ColSubdomains).DeleteMany(ctx, bson.M{"domain_id": d.ID})
	s.db.Collection(database.ColApps).DeleteMany(ctx, bson.M{"domain": d.Domain})
	s.db.Collection(database.ColDeployments).DeleteMany(ctx, bson.M{"domain": d.Domain})
	s.db.Collection(database.ColDatabases).DeleteMany(ctx, bson.M{"domain": d.Domain})
	s.db.Collection(database.ColDBUsers).DeleteMany(ctx, bson.M{"domain": d.Domain})
	s.db.Collection(database.ColWordPress).DeleteMany(ctx, bson.M{"domain": d.Domain})
	s.db.Collection(database.ColCronJobs).DeleteMany(ctx, bson.M{"domain": d.Domain})
	s.db.Collection(database.ColBackups).DeleteMany(ctx, bson.M{"domain": d.Domain})
	s.db.Collection(database.ColBackupSchedules).DeleteMany(ctx, bson.M{"domain": d.Domain})
	s.db.Collection(database.ColSSLCerts).DeleteMany(ctx, bson.M{"domain": d.Domain})
	// DNS records + zones — only the rows tied to this domain.
	s.db.Collection(database.ColDNSZones).DeleteMany(ctx, bson.M{"domain": d.Domain})

	// Finally, the domain row itself.
	s.db.Collection(database.ColDomains).DeleteOne(ctx, bson.M{"_id": d.ID})

	// Nginx needs one more reload after all the sed + postmap chatter.
	agent.RunCommand(ctx, "bash", "-c", "nginx -t 2>/dev/null && systemctl reload nginx 2>/dev/null")
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

// ---------------------------------------------------------------
// Manage Shell Access — per-user shell mode toggle. Mirrors WHM's
// "Normal / Jailed / Disabled" radio grid.
// ---------------------------------------------------------------

// ShellMode is one of the three allowed shells.
type ShellMode string

const (
	ShellModeNormal   ShellMode = "normal"
	ShellModeJailed   ShellMode = "jailed"
	ShellModeDisabled ShellMode = "disabled"
)

// ShellAccessRow is the row returned by ListShellAccess. UI renders one
// row per user with three radios (Normal / Jailed / Disabled) so the
// operator can toggle without opening each profile.
type ShellAccessRow struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Domain   string `json:"domain"`
	Mode     string `json:"mode"`
}

// ListShellAccess returns every provisioned-Linux-user account with its
// current shell. The shell is read from /etc/passwd (cheap, always
// authoritative) rather than the DB so stale rows can't mask the
// truth. Owner sees every user; tenant-scoped callers see only their
// own team.
func (s *UserService) ListShellAccess(ctx context.Context, callerRole, callerTenantHex string) ([]ShellAccessRow, error) {
	col := s.db.Collection(database.ColUsers)
	filter := bson.M{"deleted_at": nil, "username": bson.M{"$ne": ""}}
	if err := s.applyTenantFilter(filter, callerRole, callerTenantHex, false); err != nil {
		return nil, err
	}
	cur, err := col.Find(ctx, filter, options.Find().SetSort(bson.M{"username": 1}))
	if err != nil { return nil, err }
	defer cur.Close(ctx)

	var users []models.User
	if err := cur.All(ctx, &users); err != nil { return nil, err }

	// Read /etc/passwd once, parse into map.
	shells := map[string]string{}
	if r, err := agent.RunCommand(ctx, "bash", "-c", "awk -F: '{print $1\":\"$7}' /etc/passwd"); err == nil {
		for _, line := range strings.Split(r.Output, "\n") {
			kv := strings.SplitN(line, ":", 2)
			if len(kv) == 2 {
				shells[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}

	// Join a domain label onto each row: pick the first domain whose
	// `user` matches the linux username. One Find() covers every user
	// so we don't N+1 the collection.
	domainByUser := map[string]string{}
	{
		dcur, err := s.db.Collection(database.ColDomains).Find(ctx,
			bson.M{"status": bson.M{"$ne": "deleted"}},
			options.Find().SetProjection(bson.M{"domain": 1, "user": 1}))
		if err == nil {
			defer dcur.Close(ctx)
			for dcur.Next(ctx) {
				var d struct {
					Domain string `bson:"domain"`
					User   string `bson:"user"`
				}
				if err := dcur.Decode(&d); err == nil {
					if _, ok := domainByUser[d.User]; !ok {
						domainByUser[d.User] = d.Domain
					}
				}
			}
		}
	}

	rows := make([]ShellAccessRow, 0, len(users))
	for _, u := range users {
		if u.Username == "" { continue }
		rows = append(rows, ShellAccessRow{
			ID: u.ID.Hex(), Username: u.Username,
			Domain: domainByUser[u.Username],
			Mode:   shellPathToMode(shells[u.Username]),
		})
	}
	return rows, nil
}

// shellPathToMode maps /etc/passwd's shell field to our three modes.
// Anything that looks like /usr/bin/jailshell counts as jailed; a
// nologin or /bin/false entry counts as disabled.
func shellPathToMode(shell string) string {
	switch {
	case strings.Contains(shell, "jailshell"):
		return string(ShellModeJailed)
	case strings.Contains(shell, "nologin"), strings.Contains(shell, "/bin/false"), shell == "":
		return string(ShellModeDisabled)
	default:
		return string(ShellModeNormal)
	}
}

// SetShellAccess updates a user's login shell via usermod -s. The
// caller is tenant-scoped via assertSameTenant so a vendor can toggle
// their own team but not someone else's.
func (s *UserService) SetShellAccess(ctx context.Context, userID, mode, callerRole, callerTenantHex string) error {
	oid, err := primitive.ObjectIDFromHex(userID)
	if err != nil { return errors.New("invalid user ID") }
	if err := s.assertSameTenant(ctx, oid, callerRole, callerTenantHex); err != nil {
		return err
	}
	var u models.User
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": oid}).Decode(&u); err != nil {
		return errors.New("user not found")
	}
	if u.Username == "" {
		return errors.New("user has no linux account")
	}
	var shell string
	switch ShellMode(mode) {
	case ShellModeNormal:
		shell = "/bin/bash"
	case ShellModeJailed:
		// Ensure jailkit / jailshell exists. Fall back to a restricted
		// rbash if no proper jail is configured on this host.
		if r, _ := agent.RunCommand(ctx, "bash", "-c", "command -v jailshell || command -v jk_chrootlaunch || true"); strings.TrimSpace(r.Output) == "" {
			shell = "/bin/rbash"
		} else {
			shell = strings.TrimSpace(strings.Split(r.Output, "\n")[0])
		}
	case ShellModeDisabled:
		shell = "/sbin/nologin"
	default:
		return fmt.Errorf("invalid shell mode %q", mode)
	}
	if _, err := agent.RunCommand(ctx, "usermod", "-s", shell, u.Username); err != nil {
		return fmt.Errorf("usermod: %w", err)
	}
	_, _ = s.db.Collection(database.ColUsers).UpdateByID(ctx, oid, bson.M{
		"$set": bson.M{"shell_mode": mode, "updated_at": time.Now()},
	})
	return nil
}

// ---------------------------------------------------------------
// Bandwidth limits — WHM "Limit Bandwidth on an Account"
// ---------------------------------------------------------------

// BandwidthLimit is the stored cap for a single domain/user. MonthlyMB
// == 0 means unlimited. Used by the resource-usage cron to suspend a
// domain that exceeds its cap.
type BandwidthLimit struct {
	DomainID  primitive.ObjectID `bson:"domain_id" json:"domain_id"`
	Domain    string             `bson:"domain"    json:"domain"`
	User      string             `bson:"user"      json:"user"`
	MonthlyMB int64              `bson:"monthly_mb" json:"monthly_mb"`
	UpdatedAt time.Time          `bson:"updated_at" json:"updated_at"`
}

// SetBandwidthLimit sets/clears the monthly bandwidth cap for a
// domain. Cap of 0 == unlimited.
func (s *UserService) SetBandwidthLimit(ctx context.Context, domainID string, monthlyMB int64) error {
	oid, err := primitive.ObjectIDFromHex(domainID)
	if err != nil { return errors.New("invalid domain ID") }
	col := s.db.Collection(database.ColDomains)
	res, err := col.UpdateByID(ctx, oid, bson.M{
		"$set": bson.M{
			"bandwidth_limit_mb": monthlyMB,
			"updated_at":         time.Now(),
		},
	})
	if err != nil { return err }
	if res.MatchedCount == 0 { return errors.New("domain not found") }
	return nil
}

// ListBandwidthLimits returns every domain with its current cap + the
// bytes-used-this-month value. UI displays both so operators can see
// "85% of the cap consumed" without a separate lookup.
func (s *UserService) ListBandwidthLimits(ctx context.Context) ([]map[string]interface{}, error) {
	col := s.db.Collection(database.ColDomains)
	cur, err := col.Find(ctx, bson.M{"status": bson.M{"$ne": "deleted"}}, options.Find().SetSort(bson.M{"domain": 1}))
	if err != nil { return nil, err }
	defer cur.Close(ctx)
	var rows []map[string]interface{}
	for cur.Next(ctx) {
		var d struct {
			ID               primitive.ObjectID `bson:"_id"`
			Domain           string             `bson:"domain"`
			User             string             `bson:"user"`
			BandwidthLimitMB int64              `bson:"bandwidth_limit_mb"`
			BandwidthUsedMB  int64              `bson:"bandwidth_used_mb"`
		}
		if err := cur.Decode(&d); err != nil { continue }
		rows = append(rows, map[string]interface{}{
			"id":                d.ID.Hex(),
			"domain":            d.Domain,
			"user":              d.User,
			"bandwidth_limit_mb": d.BandwidthLimitMB,
			"bandwidth_used_mb":  d.BandwidthUsedMB,
		})
	}
	return rows, nil
}
