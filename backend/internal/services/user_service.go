package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
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

func (s *UserService) List(ctx context.Context, page, limit int, search string) ([]models.User, int64, error) {
	col := s.db.Collection(database.ColUsers)

	filter := bson.M{}
	if search != "" {
		filter["$or"] = bson.A{
			bson.M{"name": bson.M{"$regex": search, "$options": "i"}},
			bson.M{"email": bson.M{"$regex": search, "$options": "i"}},
			bson.M{"username": bson.M{"$regex": search, "$options": "i"}},
		}
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

func (s *UserService) GetByUsername(ctx context.Context, username string) (*models.User, error) {
	col := s.db.Collection(database.ColUsers)
	var user models.User
	if err := col.FindOne(ctx, bson.M{"username": username}).Decode(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *UserService) Create(ctx context.Context, username, name, email, password, role, packageID, primaryDomain string) (*models.User, error) {
	col := s.db.Collection(database.ColUsers)

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

	// Map frontend roles to backend roles
	backendRole := mapFrontendRole(role)

	// Assign default permissions for the role
	perms := constants.DefaultPermissions[backendRole]

	now := time.Now()
	user := models.User{
		ID:          primitive.NewObjectID(),
		Username:    username,
		Email:       email,
		Password:    string(hashedPassword),
		Name:        name,
		Role:        backendRole,
		PackageID:   pkgOID,
		PackageName: pkgName,
		Permissions: perms,
		IsActive:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err = col.InsertOne(ctx, user)
	if err != nil {
		return nil, err
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

func (s *UserService) Suspend(ctx context.Context, id string) error {
	col := s.db.Collection(database.ColUsers)

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("invalid user ID")
	}

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

func (s *UserService) Activate(ctx context.Context, id string) error {
	col := s.db.Collection(database.ColUsers)

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("invalid user ID")
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

func (s *UserService) Delete(ctx context.Context, id string) error {
	col := s.db.Collection(database.ColUsers)

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("invalid user ID")
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
// modal to populate the form with the current values.
func (s *UserService) GetByID(ctx context.Context, id string) (*models.User, error) {
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, errors.New("invalid user ID")
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
// are translated back to the backend's internal role identifiers.
func (s *UserService) Update(ctx context.Context, id string, in *UpdateInput) (*models.User, error) {
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, errors.New("invalid user ID")
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
func (s *UserService) ResetPassword(ctx context.Context, id, newPassword string) error {
	if len(newPassword) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("invalid user ID")
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

func mapFrontendRole(role string) string {
	switch role {
	case "admin":
		return "vendor_owner"
	case "vendor":
		return "vendor_admin"
	case "operator":
		return "developer"
	case "viewer":
		return "customer"
	default:
		return role
	}
}
