package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/config"
	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/betazeninfotech/mail-suite/pkg/jwt"
	"github.com/betazeninfotech/mail-suite/pkg/password"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

var (
	ErrUserExists            = errors.New("user already exists")
	ErrInvalidLogin          = errors.New("invalid email or password")
	ErrInvalidRefresh        = errors.New("invalid or expired refresh token")
	ErrMailServerUnreachable = errors.New("mail server unreachable")
)

type AuthService struct {
	db         *database.DB
	jwt        *jwt.Manager
	refreshTTL time.Duration
	cfg        *config.Config
}

func NewAuthService(db *database.DB, jm *jwt.Manager, refreshTTL time.Duration, cfg *config.Config) *AuthService {
	return &AuthService{db: db, jwt: jm, refreshTTL: refreshTTL, cfg: cfg}
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
		// A concurrent register can slip between the CountDocuments check and
		// this insert; the unique index then rejects it. Report that as the
		// clean 409 "user exists" rather than a raw 500.
		if mongo.IsDuplicateKeyError(err) {
			return nil, ErrUserExists
		}
		return nil, err
	}
	u.ID = res.InsertedID.(primitive.ObjectID)
	return s.issueTokens(ctx, &u, ua, ip)
}

// Login is the Gmail-style single-step sign-in. The user supplies their
// mailbox email + password; we verify those directly against the mail server
// (the mailbox is the source of truth), then find-or-create the matching
// Mail Suite user and attach/refresh their local "betazen" mailbox so the
// inbox works immediately — no separate registration or add-account step.
func (s *AuthService) Login(ctx context.Context, req models.LoginRequest, ua, ip string) (*models.TokenPair, error) {
	email := strings.ToLower(strings.TrimSpace(req.Email))

	// 1. Verify the credentials against the mail server. IMAP username is the
	//    full email address, matching how Dovecot authenticates mailboxes.
	if err := VerifyIMAPLogin(s.cfg.IMAPHost, s.cfg.IMAPPort, false, email, req.Password); err != nil {
		return nil, err // ErrInvalidLogin (bad creds) or ErrMailServerUnreachable
	}

	// 2. Find or create the Mail Suite user record for this mailbox.
	u, err := s.findOrCreateUser(ctx, email, req.Password)
	if err != nil {
		return nil, err
	}

	// 3. Ensure a betazen mailbox is attached and its stored secret is current
	//    (so a password changed on the server propagates on next sign-in).
	if err := s.provisionBetazenMailbox(ctx, u, email, req.Password); err != nil {
		return nil, err
	}

	return s.issueTokens(ctx, u, ua, ip)
}

// findOrCreateUser returns the user for email, creating a minimal record on
// first sign-in. The stored password hash is not used for authentication
// anymore (the mail server is authoritative) but is kept so the classic
// register/login path and any password-based tooling still have a value.
func (s *AuthService) findOrCreateUser(ctx context.Context, email, pw string) (*models.User, error) {
	col := s.db.Col(database.ColUsers)
	var u models.User
	err := col.FindOne(ctx, bson.M{"email": email}).Decode(&u)
	if err == nil {
		return &u, nil
	}
	if err != mongo.ErrNoDocuments {
		return nil, err
	}

	hash, err := password.Hash(pw)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	u = models.User{
		Email:        email,
		PasswordHash: hash,
		Name:         nameFromEmail(email),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	res, err := col.InsertOne(ctx, u)
	if err != nil {
		// A concurrent first sign-in may have created the same user (unique
		// email index); fall back to reading it rather than failing.
		if e := col.FindOne(ctx, bson.M{"email": email}).Decode(&u); e == nil {
			return &u, nil
		}
		return nil, err
	}
	u.ID = res.InsertedID.(primitive.ObjectID)
	return &u, nil
}

// provisionBetazenMailbox attaches the user's own mailbox as a "betazen"
// account (or refreshes it if already present). The unique index on
// {user_id, address} guarantees at most one row per mailbox.
func (s *AuthService) provisionBetazenMailbox(ctx context.Context, u *models.User, email, secret string) error {
	col := s.db.Col(database.ColAccounts)
	now := time.Now()

	var acc models.MailAccount
	err := col.FindOne(ctx, bson.M{"user_id": u.ID, "address": email}).Decode(&acc)
	if err == nil {
		// Refresh stored secret + host settings in case the mailbox password
		// or the server config changed since last sign-in.
		_, uerr := col.UpdateOne(ctx, bson.M{"_id": acc.ID}, bson.M{"$set": bson.M{
			"provider":  "betazen",
			"username":  email,
			"secret":    secret,
			"imap_host": s.cfg.IMAPHost,
			"imap_port": s.cfg.IMAPPort,
			"imap_ssl":  false,
			"smtp_host": s.cfg.SMTPHost,
			"smtp_port": s.cfg.SMTPPort,
			"smtp_ssl":  false,
			"updated_at": now,
		}})
		return uerr
	}
	if err != mongo.ErrNoDocuments {
		return err
	}

	cnt, _ := col.CountDocuments(ctx, bson.M{"user_id": u.ID})
	acc = models.MailAccount{
		UserID:      u.ID,
		DisplayName: u.Name,
		Address:     email,
		Provider:    "betazen",
		IMAPHost:    s.cfg.IMAPHost,
		IMAPPort:    s.cfg.IMAPPort,
		IMAPSSL:     false,
		SMTPHost:    s.cfg.SMTPHost,
		SMTPPort:    s.cfg.SMTPPort,
		SMTPSSL:     false,
		Username:    email,
		Secret:      secret,
		IsPrimary:   cnt == 0,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err = col.InsertOne(ctx, acc)
	return err
}

// nameFromEmail derives a friendly display name from the local part of an
// address, e.g. "john.doe@x.com" -> "John Doe".
func nameFromEmail(email string) string {
	local := email
	if i := strings.IndexByte(email, '@'); i > 0 {
		local = email[:i]
	}
	local = strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(local)
	fields := strings.Fields(local)
	for i, f := range fields {
		fields[i] = strings.ToUpper(f[:1]) + f[1:]
	}
	if len(fields) == 0 {
		return email
	}
	return strings.Join(fields, " ")
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
