package services

import (
	"context"
	"errors"
	"fmt"
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
	db *mongo.Database
}

func NewUserService(db *mongo.Database) *UserService {
	return &UserService{db: db}
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

func (s *UserService) Create(ctx context.Context, username, name, email, password, role string) (*models.User, error) {
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
		Permissions: perms,
		IsActive:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err = col.InsertOne(ctx, user)
	if err != nil {
		return nil, err
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
