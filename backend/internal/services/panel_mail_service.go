package services

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/crypto"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/mailer"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// isUsableMailDomain reports whether `s` is something we can safely
// use as the right-hand side of an email address. RFC 5321 §4.1.2
// rejects bare IPs (you'd need the literal-IP form `[v6:fe80::1]` or
// `[192.0.2.1]` instead); most receiving MTAs (including Postfix's
// smtpd) reject `admin@192.0.2.1` with "501 5.1.7 Bad sender address
// syntax" before the message even leaves localhost. So we treat any
// IPv4 / IPv6 / "localhost" / empty value as unusable and force the
// caller to fall back to a real hostname.
//
// Pre-3.0.30 AutoBootstrap accepted whatever cfg.Domain held — and on
// IP-only deploys (the typical Hostinger / DO snapshot before the
// operator points a real domain) cfg.Domain is the bare server IP.
// FromAddr came out as `admin@<ip>`, every notification + reset
// email got rejected by the local Postfix, and the panel's auto-SSL
// retry loop spammed the journal with "mailer: MAIL FROM: 501 5.1.7
// Bad sender address syntax" while emails silently never landed.
func isUsableMailDomain(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.EqualFold(s, "localhost") {
		return false
	}
	// Bare IPv4 or IPv6 — net.ParseIP returns non-nil for both.
	if net.ParseIP(s) != nil {
		return false
	}
	// Bracketed literal IP shape (`[1.2.3.4]`, `[v6:fe80::1]`) — also
	// not what we want for a default FromAddr; Postfix accepts the
	// shape but downstream relays reject mail from it.
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return false
	}
	// Must contain at least one dot — otherwise it's a single-label
	// host like `srv1234`, which most MTAs treat as a non-routable
	// internal name. install.sh sets a multi-label hostname via
	// hostnamectl, so this is a defensive guard rather than a
	// realistic miss.
	if !strings.Contains(s, ".") {
		return false
	}
	return true
}

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
	// SendStatus is populated only on Save responses so the UI can
	// show the operator whether the new SMTP settings actually work.
	// Values: "ok" when the confirmation email went through, "failed"
	// when the relay rejected the message, "skipped" when the mailer
	// is still disabled, and "" on ordinary Get() calls. SendError
	// carries the relay's raw error line ("authentication failed",
	// "connection refused", etc.) so Gmail's "App Password required"
	// isn't silently swallowed.
	SendStatus string `json:"send_status,omitempty"`
	SendError  string `json:"send_error,omitempty"`
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

// ReloadFromDB re-reads the panel_mail doc and rehydrates the in-memory
// mailer. Called after a server-to-server transfer mirrors the SMTP
// settings into mongo so the destination's mailer picks up the new
// host/port/from immediately, without requiring a panel restart or an
// operator re-save. Safe to call when no doc exists — loadPlaintext
// returns a zero-value Config and Reload simply marks the mailer
// disabled, which is the correct state for an unconfigured install.
func (s *PanelMailService) ReloadFromDB(ctx context.Context) {
	if s == nil || s.m == nil {
		return
	}
	if cfg, err := s.loadPlaintext(ctx); err == nil {
		s.m.Reload(cfg)
	}
}

// ReencryptForTransfer translates a panel_mail.password_cipher blob
// from the SOURCE server's APP_ENCRYPTION_KEY into THIS server's, so
// the destination panel can actually decrypt and authenticate against
// the SMTP relay after a transfer. Without this, the source's cipher
// bytes land verbatim in the destination's mongo, but DecryptGCM on
// boot or on first send returns garbage (the key doesn't match) — and
// SMTP login fails with "535 BadCredentials" because the panel sent
// random bytes as the password.
//
// Mirrors EmailService.ReencryptForTransfer's shape so the transfer
// pipeline reads naturally. Returns (nil, nil) when re-encryption
// isn't possible (source key empty, source cipher empty, or
// decryption with the source key fails) so the caller can fall back
// to dropping the cipher and letting the operator re-type the
// password rather than stamping garbage into the destination.
func (s *PanelMailService) ReencryptForTransfer(srcCipher []byte, srcEncKeyRaw string) ([]byte, error) {
	if len(srcCipher) == 0 || strings.TrimSpace(srcEncKeyRaw) == "" {
		return nil, nil
	}
	if len(s.encKey) != 32 {
		return nil, fmt.Errorf("destination encryption key unavailable")
	}
	srcKey, err := crypto.LoadKey(srcEncKeyRaw)
	if err != nil {
		return nil, fmt.Errorf("load source key: %w", err)
	}
	plain, err := crypto.DecryptGCM(srcCipher, srcKey)
	if err != nil {
		// Source cipher is genuine bytes but the source key didn't
		// match (key rotated since the cipher was written, or we
		// pulled the wrong env line). Caller should drop the cipher
		// and surface a "re-type the password" hint.
		return nil, fmt.Errorf("decrypt with source key: %w", err)
	}
	return crypto.EncryptGCM(plain, s.encKey)
}

// SetNotifier wires the shared NotifierService so Save can fire a
// "SMTP is live" confirmation email to the contact address once the
// relay flips into the configured state. Called once from main.go
// after NotifierService is constructed.
func (s *PanelMailService) SetNotifier(n *NotifierService) { s.notifier = n }

// AutoBootstrap sets up a usable Panel Mail config on a fresh install
// when no operator has visited the SMTP page yet. Without this, the
// first password-reset attempt right after `bash install.sh` falls
// through the "mailer disabled" branch in AuthService.ForgotPassword
// and the operator only sees the reset link via journalctl — surprising
// for a panel where Postfix was just installed and is sitting idle on
// localhost:25 ready to relay.
//
// Strategy: if no panel-mail document exists in Mongo yet AND we have
// a real panel domain (not the dev "localhost" default), write a
// minimal config that points at the local Postfix relay with
// FromAddr = admin@<panelDomain>. The local Postfix accepts mail
// without auth on 127.0.0.1:25 (install.sh's default main.cf
// allows 127.0.0.1/32 in mynetworks), so reset/notification mail
// starts working immediately.
//
// Idempotent — only writes when the panel-mail doc is missing. The
// operator can override at any time via Server Settings → Outgoing
// Mail; their saved config takes precedence on every boot via the
// existing warm-start in NewPanelMailService.
//
// Skipped silently when:
//   - the doc already exists (operator-owned config wins)
//   - both `panelDomain` and the system hostname resolve to "localhost"
//     / empty — we genuinely cannot construct a FromAddr that an
//     upstream relay will accept
//
// Returns nil on every skip path so a missing/broken local Postfix
// doesn't prevent the panel from booting.
//
// FromAddr resolution chain (most-specific first):
//  1. cfg.Domain when set and not "localhost" — operator's intended
//     panel domain, what the rest of the panel uses for self-naming.
//  2. os.Hostname() when set and not "localhost" — fresh installs
//     where the operator hasn't filled DOMAIN yet still get a real
//     mail-domain (typical post-`hostnamectl set-hostname` state).
//
// Pre-3.0.27 the function bailed when panelDomain was empty/localhost
// — but DOMAIN defaults to "localhost" in config.go, so a fresh
// install would NEVER auto-bootstrap unless the operator explicitly
// exported DOMAIN before first boot. Net effect: every "Forgot
// Password" / OTP email silently dead-lettered into stderr because
// the panel mailer was never wired. The hostname fallback closes
// that gap — `hostnamectl --static` returns a usable FQDN on every
// install.sh-provisioned VPS.
func (s *PanelMailService) AutoBootstrap(ctx context.Context, panelDomain string) error {
	// Resolution chain — most-specific first; isUsableMailDomain
	// guards each step so an IP-shaped DOMAIN (typical Hostinger /
	// DigitalOcean snapshot before the operator points a real
	// domain) doesn't slip through and produce admin@<ip> as
	// FromAddr (Postfix rejects with 501 5.1.7).
	var fromDomain string
	if isUsableMailDomain(panelDomain) {
		fromDomain = strings.TrimSpace(panelDomain)
	}
	if fromDomain == "" {
		if hn, err := os.Hostname(); err == nil && isUsableMailDomain(hn) {
			fromDomain = strings.TrimSpace(hn)
			log.Info().Str("hostname", fromDomain).Msg("panel-mail: using system hostname as FromAddr (DOMAIN unset / localhost / IP)")
		}
	}
	if fromDomain == "" {
		log.Info().Str("DOMAIN", panelDomain).Msg("panel-mail: auto-bootstrap skipped — no usable mail domain (configure Server Settings → Outgoing Mail manually)")
		return nil
	}

	// Doc-exists check. We DO overwrite when the existing config has
	// an unusable FromAddr (typical: `admin@<ip>` written by a
	// pre-3.0.30 AutoBootstrap on an IP-only deploy — Postfix
	// rejects every send with 501 5.1.7). Operator-edited configs
	// always have a real `Username` / `PasswordCipher` set when
	// they wired their own SMTP relay, so we keep those intact.
	col := s.db.Collection(database.ColServerConfig)
	var existing panelMailConfigDoc
	switch err := col.FindOne(ctx, bson.M{"_id": panelMailConfigID}).Decode(&existing); err {
	case nil:
		// Operator-owned config (auth filled or non-localhost host)
		// always wins — never overwrite.
		operatorOwned := strings.TrimSpace(existing.Username) != "" ||
			len(existing.PasswordCipher) > 0 ||
			(existing.Host != "" && existing.Host != "127.0.0.1" && !strings.EqualFold(existing.Host, "localhost"))
		if operatorOwned {
			return nil
		}
		// Auto-bootstrapped config — heal if FromAddr's domain part
		// is now considered unusable (post-3.0.30 stricter check).
		fromAt := strings.LastIndex(existing.FromAddr, "@")
		if fromAt > 0 && isUsableMailDomain(existing.FromAddr[fromAt+1:]) {
			// Already healthy — nothing to do.
			return nil
		}
		log.Warn().
			Str("old_from", existing.FromAddr).
			Str("new_from_domain", fromDomain).
			Msg("panel-mail: healing auto-bootstrap config with bad FromAddr (likely IP-shaped from pre-3.0.30)")
	case mongo.ErrNoDocuments:
		// No config — proceed with first-time bootstrap.
	default:
		log.Warn().Err(err).Msg("panel-mail: auto-bootstrap skipped (config lookup failed)")
		return nil
	}

	now := time.Now()
	doc := panelMailConfigDoc{
		ID:         panelMailConfigID,
		Host:       "127.0.0.1", // explicit IPv4 — avoids the IPv6 first-attempt timeout dance
		Port:       25,
		Username:   "",
		TLSMode:    "none",
		FromAddr:   "admin@" + fromDomain,
		FromName:   "Betazen Server Panel",
		Configured: true,
		UpdatedAt:  now,
	}
	if _, err := col.UpdateOne(ctx,
		bson.M{"_id": panelMailConfigID},
		bson.M{"$set": doc},
		options.Update().SetUpsert(true),
	); err != nil {
		log.Warn().Err(err).Msg("panel-mail: auto-bootstrap upsert failed")
		return err
	}

	// Reload the in-memory mailer so subsequent sends pick up the new
	// config without a process restart. Without this the freshly-
	// inserted doc only takes effect on the next boot.
	s.m.Reload(mailer.Config{
		Host:     doc.Host,
		Port:     doc.Port,
		TLSMode:  doc.TLSMode,
		FromAddr: doc.FromAddr,
		FromName: doc.FromName,
	})
	log.Info().
		Str("from", doc.FromAddr).
		Str("relay", fmt.Sprintf("%s:%d", doc.Host, doc.Port)).
		Msg("panel-mail: auto-configured local Postfix relay (no operator config existed)")
	return nil
}

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

	view, err := s.Get(ctx)
	if err != nil {
		return nil, err
	}

	// "SMTP is live" confirmation — synchronous so we can surface the
	// REAL failure reason (Gmail requires an App Password, port 465
	// blocked, auth rejected, etc.) to the UI instead of letting it
	// rot in the server log. The send is capped at ~30s by the
	// notifier's own context timeout so a hung relay can't stall the
	// Save forever; on success the UI shows "saved + confirmation
	// sent", on failure "saved but confirmation failed: <reason>".
	if s.notifier != nil {
		if !s.m.Enabled() {
			view.SendStatus = "skipped"
		} else if sendErr := s.notifier.NotifySMTPConfigured(ctx); sendErr != nil {
			view.SendStatus = "failed"
			view.SendError = sendErr.Error()
		} else {
			view.SendStatus = "ok"
		}
	}

	return view, nil
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
