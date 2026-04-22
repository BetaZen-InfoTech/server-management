package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/crypto"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/mailer"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// PanelMailService owns the panel's own outgoing-mail configuration
// (SMTP relay used for password resets, notifications, domain-expiry
// warnings). Distinct from EmailService which manages tenant Postfix
// mailboxes.
//
// The SMTP password is AES-GCM encrypted at rest using the same
// APP_ENCRYPTION_KEY that guards Deploy Software PATs. Admin UI
// responses show a masked preview ("smtp-****") so a stolen JWT
// can't be used to exfiltrate the relay credentials.
type PanelMailService struct {
	db       *mongo.Database
	encKey   []byte
	m        *mailer.Mailer // shared, hot-reloaded on Save
	notifier *NotifierService
}

// panelMailConfigDoc is the Mongo shape. Singleton row keyed on
// `_id: "panel_mail"` so Get/Save are trivial upserts.
type panelMailConfigDoc struct {
	ID             string    `bson:"_id"`
	Host           string    `bson:"host"`
	Port           int       `bson:"port"`
	Username       string    `bson:"username"`
	PasswordCipher []byte    `bson:"password_cipher,omitempty"`
	TLSMode        string    `bson:"tls_mode"`
	FromAddr       string    `bson:"from_addr"`
	FromName       string    `bson:"from_name"`
	ReplyTo        string    `bson:"reply_to"`
	Configured     bool      `bson:"configured"` // true once an admin has saved valid settings
	UpdatedAt      time.Time `bson:"updated_at"`
}

const panelMailConfigID = "panel_mail"

// PanelMailConfigView is what the UI reads. Password is intentionally
// replaced with "****" when set so the form can show "SMTP credentials
// are configured" without ever echoing the real password back.
type PanelMailConfigView struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	TLSMode     string `json:"tls_mode"`
	FromAddr    string `json:"from_addr"`
	FromName    string `json:"from_name"`
	ReplyTo     string `json:"reply_to"`
	Configured  bool   `json:"configured"`
}

// SavePanelMailRequest is the write payload from the Server Settings UI.
// Password is optional — the empty string means "keep the existing
// cipher text". That lets an admin edit Host/Port without having to
// re-type the SMTP password every time.
type SavePanelMailRequest struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	TLSMode  string `json:"tls_mode" validate:"omitempty,oneof=none starttls tls"`
	FromAddr string `json:"from_addr"`
	FromName string `json:"from_name"`
	ReplyTo  string `json:"reply_to"`
}

func NewPanelMailService(db *mongo.Database, encKey []byte) *PanelMailService {
	s := &PanelMailService{
		db:     db,
		encKey: encKey,
		m:      mailer.New(mailer.Config{}),
	}
	// Best-effort warm-start — if the config exists in Mongo, hand it
	// to the mailer so password-reset emails start working immediately
	// after a process restart (no waiting for the admin to resave).
	if cfg, err := s.loadPlaintext(context.Background()); err == nil {
		s.m.Reload(cfg)
	}
	return s
}

// Mailer returns the shared mailer handle — passed to AuthService /
// NotificationService so they can Send without re-reading config.
func (s *PanelMailService) Mailer() *mailer.Mailer {
	return s.m
}

// SetNotifier wires the shared NotifierService so Save can fire a
// "SMTP is live" confirmation email to the contact address once the
// relay flips into the configured state. Called once from main.go
// after NotifierService is constructed.
func (s *PanelMailService) SetNotifier(n *NotifierService) { s.notifier = n }

// loadPlaintext reads the config from Mongo and decrypts the password.
// Returns a zero-value Config (which Mailer.Valid reports false on) when
// no config has ever been saved — that's the fresh-install case.
func (s *PanelMailService) loadPlaintext(ctx context.Context) (mailer.Config, error) {
	var doc panelMailConfigDoc
	err := s.db.Collection(database.ColServerConfig).FindOne(ctx, bson.M{"_id": panelMailConfigID}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return mailer.Config{}, nil
	}
	if err != nil {
		return mailer.Config{}, err
	}
	cfg := mailer.Config{
		Host:     doc.Host,
		Port:     doc.Port,
		Username: doc.Username,
		TLSMode:  doc.TLSMode,
		FromAddr: doc.FromAddr,
		FromName: doc.FromName,
		ReplyTo:  doc.ReplyTo,
	}
	if len(doc.PasswordCipher) > 0 && len(s.encKey) == 32 {
		if plain, err := crypto.DecryptGCM(doc.PasswordCipher, s.encKey); err == nil {
			cfg.Password = string(plain)
		}
	}
	return cfg, nil
}

// Get returns the UI-safe view of the current config.
func (s *PanelMailService) Get(ctx context.Context) (*PanelMailConfigView, error) {
	var doc panelMailConfigDoc
	err := s.db.Collection(database.ColServerConfig).FindOne(ctx, bson.M{"_id": panelMailConfigID}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return &PanelMailConfigView{TLSMode: "starttls", Port: 587}, nil
	}
	if err != nil {
		return nil, err
	}
	return &PanelMailConfigView{
		Host:        doc.Host,
		Port:        doc.Port,
		Username:    doc.Username,
		HasPassword: len(doc.PasswordCipher) > 0,
		TLSMode:     doc.TLSMode,
		FromAddr:    doc.FromAddr,
		FromName:    doc.FromName,
		ReplyTo:     doc.ReplyTo,
		Configured:  doc.Configured,
	}, nil
}

// Save upserts the config. Empty Password means "keep the existing
// cipher text" so an admin editing Host/Port doesn't have to retype
// the password. Also reloads the shared Mailer so subsequent sends
// use the new settings without a process restart.
func (s *PanelMailService) Save(ctx context.Context, req *SavePanelMailRequest) (*PanelMailConfigView, error) {
	req.Host = strings.TrimSpace(req.Host)
	req.Username = strings.TrimSpace(req.Username)
	req.FromAddr = strings.TrimSpace(req.FromAddr)
	req.FromName = strings.TrimSpace(req.FromName)
	req.ReplyTo = strings.TrimSpace(req.ReplyTo)
	if req.TLSMode == "" {
		req.TLSMode = "starttls"
	}
	if req.Port == 0 {
		req.Port = 587
	}

	set := bson.M{
		"host":       req.Host,
		"port":       req.Port,
		"username":   req.Username,
		"tls_mode":   req.TLSMode,
		"from_addr":  req.FromAddr,
		"from_name":  req.FromName,
		"reply_to":   req.ReplyTo,
		"updated_at": time.Now(),
		"configured": req.Host != "" && req.Port > 0 && req.FromAddr != "",
	}
	if strings.TrimSpace(req.Password) != "" {
		if len(s.encKey) != 32 {
			return nil, fmt.Errorf("encryption key unavailable; cannot store SMTP password")
		}
		cipher, err := crypto.EncryptGCM([]byte(req.Password), s.encKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt SMTP password: %w", err)
		}
		set["password_cipher"] = cipher
	}

	_, err := s.db.Collection(database.ColServerConfig).UpdateOne(ctx,
		bson.M{"_id": panelMailConfigID},
		bson.M{"$set": set},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return nil, err
	}

	// Reload the shared mailer so the next password-reset email uses
	// the new config. If the operator didn't supply a password this
	// time, we pull the existing cipher back out before reloading.
	if cfg, err := s.loadPlaintext(ctx); err == nil {
		s.m.Reload(cfg)
	}

	// "SMTP is live" confirmation — self-test that the relay can send
	// before a vendor triggers the first password reset. Fire-and-
	// forget; log-only on failure. Uses a background context so it
	// survives even if the HTTP request ends before the send completes.
	if s.notifier != nil && s.m.Enabled() {
		go s.notifier.NotifySMTPConfigured(context.Background())
	}

	return s.Get(ctx)
}

// TestSend fires a one-off "test email" to the given address using the
// currently-saved config. Runs even when the config's `configured`
// flag is false as long as Host+Port+FromAddr are populated — admins
// testing before flipping the switch need to be able to try.
func (s *PanelMailService) TestSend(ctx context.Context, to string) error {
	to = strings.TrimSpace(to)
	if to == "" {
		return fmt.Errorf("recipient address is required")
	}
	if !s.m.Enabled() {
		// Try a one-shot reload from DB in case the in-memory copy is
		// stale (e.g. the service was restarted mid-save).
		if cfg, err := s.loadPlaintext(ctx); err == nil {
			s.m.Reload(cfg)
		}
	}
	if !s.m.Enabled() {
		return fmt.Errorf("SMTP is not configured — set Host, Port, and From address first")
	}
	return s.m.Send(ctx, mailer.Message{
		To:      to,
		Subject: "Betazen Server Panel — SMTP test",
		Text:    "This is a test message from Betazen Server Panel. If you received it, your outgoing mail is configured correctly.\n",
		HTML:    `<p>This is a test message from <b>Betazen Server Panel</b>. If you received it, your outgoing mail is configured correctly.</p>`,
	})
}
