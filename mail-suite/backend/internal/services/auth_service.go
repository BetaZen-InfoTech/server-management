package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/betazeninfotech/mail-suite/pkg/jwt"
	"github.com/betazeninfotech/mail-suite/pkg/password"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

var (
	ErrUserExists      = errors.New("user already exists")
	ErrInvalidLogin    = errors.New("invalid email or password")
	ErrInvalidRefresh  = errors.New("invalid or expired refresh token")
)

type AuthService struct {
	db         *database.DB
	jwt        *jwt.Manager
	refreshTTL time.Duration
}

func NewAuthService(db *database.DB, jm *jwt.Manager, refreshTTL time.Duration) *AuthService {
	return &AuthService{db: db, jwt: jm, refreshTTL: refreshTTL}
}

func (s *AuthService) Register(ctx context.Context, req models.RegisterRequest, ua, ip string) (*models.TokenPair, error) {
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if cnt, _ := s.db.Col(database.ColUsers).CountDocuments(ctx, bson.M{"email": email}); cnt > 0 {
		return nil, ErrUserExists
	}
	hash, err := password.Hash(req.Password)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	u := models.User{
		Email: email, PasswordHash: hash, Name: req.Name,
		CreatedAt: now, UpdatedAt: now,
	}
	res, err := s.db.Col(database.ColUsers).InsertOne(ctx, u)
	if err != nil {
		return nil, err
	}
	u.ID = res.InsertedID.(primitive.ObjectID)
	return s.issueTokens(ctx, &u, ua, ip)
}

func (s *AuthService) Login(ctx context.Context, req models.LoginRequest, ua, ip string) (*models.TokenPair, error) {
	email := strings.ToLower(strings.TrimSpace(req.Email))
	var u models.User
	err := s.db.Col(database.ColUsers).FindOne(ctx, bson.M{"email": email}).Decode(&u)
	if err == mongo.ErrNoDocuments {
		return nil, ErrInvalidLogin
	}
	if err != nil {
		return nil, err
	}
	if !password.Verify(u.PasswordHash, req.Password) {
		return nil, ErrInvalidLogin
	}
	return s.issueTokens(ctx, &u, ua, ip)
}

func (s *AuthService) Refresh(ctx context.Context, refreshTok, ua, ip string) (*models.TokenPair, error) {
	var rt models.RefreshToken
	err := s.db.Col(database.ColRefreshToks).FindOne(ctx, bson.M{"token": refreshTok}).Decode(&rt)
	if err == mongo.ErrNoDocuments || (err == nil && rt.ExpiresAt.Before(time.Now())) {
		return nil, ErrInvalidRefresh
	}
	if err != nil {
		return nil, err
	}
	// Rotate
	_, _ = s.db.Col(database.ColRefreshToks).DeleteOne(ctx, bson.M{"_id": rt.ID})

	var u models.User
	if err := s.db.Col(database.ColUsers).FindOne(ctx, bson.M{"_id": rt.UserID}).Decode(&u); err != nil {
		return nil, ErrInvalidRefresh
	}
	return s.issueTokens(ctx, &u, ua, ip)
}

func (s *AuthService) Logout(ctx context.Context, refreshTok string) error {
	_, err := s.db.Col(database.ColRefreshToks).DeleteOne(ctx, bson.M{"token": refreshTok})
	return err
}

func (s *AuthService) GetUser(ctx context.Context, id primitive.ObjectID) (*models.User, error) {
	var u models.User
	err := s.db.Col(database.ColUsers).FindOne(ctx, bson.M{"_id": id}).Decode(&u)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *AuthService) issueTokens(ctx context.Context, u *models.User, ua, ip string) (*models.TokenPair, error) {
	access, exp, err := s.jwt.AccessToken(u.ID.Hex(), u.Email)
	if err != nil {
		return nil, err
	}
	refresh, err := jwt.RandomRefreshToken()
	if err != nil {
		return nil, err
	}
	rt := models.RefreshToken{
		UserID: u.ID, Token: refresh, UserAgent: ua, IP: ip,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(s.refreshTTL),
	}
	if _, err := s.db.Col(database.ColRefreshToks).InsertOne(ctx, rt); err != nil {
		return nil, err
	}
	return &models.TokenPair{AccessToken: access, RefreshToken: refresh, ExpiresAt: exp, User: u}, nil
}
