package services

import (
	"context"
	"errors"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

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

func (s *UserService) Create(ctx context.Context, name, email, password, role string) (*models.User, error) {
	col := s.db.Collection(database.ColUsers)

	// Check if email already exists
	count, _ := col.CountDocuments(ctx, bson.M{"email": email})
	if count > 0 {
		return nil, errors.New("user with this email already exists")
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, errors.New("failed to hash password")
	}

	// Map frontend roles to backend roles
	backendRole := mapFrontendRole(role)

	now := time.Now()
	user := models.User{
		ID:        primitive.NewObjectID(),
		Email:     email,
		Password:  string(hashedPassword),
		Name:      name,
		Role:      backendRole,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
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
