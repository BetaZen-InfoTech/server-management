package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/mailer"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// mailboxResetTokenTTL is how long a webmail reset link stays valid.
const mailboxResetTokenTTL = 30 * time.Minute

// verifyMailboxPassword checks a mailbox password against Dovecot using
// `doveadm auth test`, which exits non-zero on a wrong password. argv (not a
// shell string) carries the secret so a crafted password can't inject.
func (s *EmailService) verifyMailboxPassword(ctx context.Context, email, password string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return fmt.Errorf("invalid credentials")
	}
	if _, err := agent.RunCommand(ctx, "doveadm", "auth", "test", email, password); err != nil {
		return fmt.Errorf("invalid credentials")
	}
	return nil
}

// SetMailboxRecoveryEmail sets (or clears, when recoveryEmail is blank) the
// recovery address on a mailbox after re-authenticating with the mailbox
// password. Stored on the mailbox doc so it transfers with the mailbox.
func (s *EmailService) SetMailboxRecoveryEmail(ctx context.Context, email, password, recoveryEmail string) error {
	if err := s.verifyMailboxPassword(ctx, email, password); err != nil {
		return err
	}
	mb, err := s.findMailboxByEmail(ctx, email)
	if err != nil {
		return err
	}
	recoveryEmail = strings.ToLower(strings.TrimSpace(recoveryEmail))
	if recoveryEmail != "" {
		if !looksLikeEmail(recoveryEmail) {
			return fmt.Errorf("recovery email is not a valid address")
		}
		if recoveryEmail == strings.ToLower(mb.Email) {
			return fmt.Errorf("recovery email must be different from the mailbox itself")
		}
	}
	_, err = s.db.Collection(database.ColMailboxes).UpdateByID(ctx, mb.ID, bson.M{
		"$set": bson.M{"recovery_email": recoveryEmail, "updated_at": time.Now()},
	})
	return err
}

// GetMailboxRecoveryEmailMasked returns the masked recovery email after
// re-authenticating with the mailbox password. Masking avoids leaking the full
// backup address to anyone who only knows the mailbox password over the wire.
func (s *EmailService) GetMailboxRecoveryEmailMasked(ctx context.Context, email, password string) (string, error) {
	if err := s.verifyMailboxPassword(ctx, email, password); err != nil {
		return "", err
	}
	mb, err := s.findMailboxByEmail(ctx, email)
	if err != nil {
		return "", err
	}
	return maskEmail(mb.RecoveryEmail), nil
}

// StartMailboxPasswordReset begins the forgot-password flow. It ALWAYS returns
// nil for the "can't send" cases (mailbox missing, no recovery email, mailer
// disabled) so the HTTP handler can answer 200 uniformly and never reveal
// whether an address exists or has recovery configured. A link is emailed only
// when a mailbox with a recovery address exists. resetBaseURL is the webmail
// reset page (the raw token is appended as ?token=).
func (s *EmailService) StartMailboxPasswordReset(ctx context.Context, email, resetBaseURL string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	mb, err := s.findMailboxByEmail(ctx, email)
	if err != nil || strings.TrimSpace(mb.RecoveryEmail) == "" {
		return nil // silent: no enumeration
	}
	if s.mailer == nil || !s.mailer.Enabled() {
		log.Warn().Str("mailbox", email).Msg("mailbox reset requested but mailer is not configured")
		return nil
	}

	raw, err := randomToken(32)
	if err != nil {
		return err
	}
	hash := sha256Hex(raw)
	now := time.Now()
	_, err = s.db.Collection(database.ColMailboxResetTokens).InsertOne(ctx, models.MailboxResetToken{
		Email:     strings.ToLower(mb.Email),
		TokenHash: hash,
		ExpiresAt: now.Add(mailboxResetTokenTTL),
		Used:      false,
		CreatedAt: now,
	})
	if err != nil {
		return err
	}

	link := resetBaseURL
	if strings.Contains(link, "?") {
		link += "&token=" + raw
	} else {
		link += "?token=" + raw
	}
	subject := "Reset your Betazen Mail password"
	text := fmt.Sprintf(
		"A password reset was requested for your mailbox %s.\n\nOpen this link to set a new password (valid for %d minutes):\n%s\n\nIf you didn't request this, you can ignore this email — your password stays unchanged.",
		mb.Email, int(mailboxResetTokenTTL.Minutes()), link)
	html := fmt.Sprintf(
		`<div style="font-family:system-ui,Arial,sans-serif;font-size:14px;color:#1f2937;line-height:1.6">`+
			`<p>A password reset was requested for your mailbox <b>%s</b>.</p>`+
			`<p><a href="%s" style="display:inline-block;background:#2563eb;color:#fff;text-decoration:none;padding:10px 18px;border-radius:8px">Set a new password</a></p>`+
			`<p style="color:#6b7280">This link is valid for %d minutes. If you didn't request this, ignore this email — your password stays unchanged.</p>`+
			`<p style="color:#9ca3af;font-size:12px">%s</p></div>`,
		mb.Email, link, int(mailboxResetTokenTTL.Minutes()), link)

	if err := s.mailer.Send(ctx, mailer.Message{To: mb.RecoveryEmail, Subject: subject, Text: text, HTML: html}); err != nil {
		// Log but still return nil — we don't leak send failures to the caller.
		log.Error().Err(err).Str("mailbox", mb.Email).Msg("failed to send mailbox reset email")
	}
	return nil
}

// CompleteMailboxPasswordReset consumes a reset token and sets the new mailbox
// password (Dovecot hash + webmail-SSO re-seed via UpdateMailbox). The token is
// single-use and must be unexpired.
func (s *EmailService) CompleteMailboxPasswordReset(ctx context.Context, token, newPassword string) error {
	token = strings.TrimSpace(token)
	if len(newPassword) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	hash := sha256Hex(token)
	col := s.db.Collection(database.ColMailboxResetTokens)
	var rec models.MailboxResetToken
	// Atomically claim the token (used=false -> true) so a double-submit or a
	// race can't apply the same token twice.
	err := col.FindOneAndUpdate(ctx,
		bson.M{"token_hash": hash, "used": false, "expires_at": bson.M{"$gt": time.Now()}},
		bson.M{"$set": bson.M{"used": true}},
		options.FindOneAndUpdate().SetReturnDocument(options.Before),
	).Decode(&rec)
	if err == mongo.ErrNoDocuments {
		return fmt.Errorf("this reset link is invalid or has expired — request a new one")
	}
	if err != nil {
		return err
	}
	mb, err := s.findMailboxByEmail(ctx, rec.Email)
	if err != nil {
		return fmt.Errorf("mailbox no longer exists")
	}
	if _, err := s.UpdateMailbox(ctx, mb.ID.Hex(), map[string]interface{}{
		"password":         newPassword,
		"password_set_via": "webmail-reset",
		"password_set_by":  mb.Email,
	}); err != nil {
		// Un-claim the token so the user can retry after a transient failure.
		col.UpdateByID(ctx, rec.ID, bson.M{"$set": bson.M{"used": false}})
		return fmt.Errorf("could not set the new password: %w", err)
	}
	return nil
}

// ---- small local helpers ----

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	return strings.IndexByte(s[at+1:], '.') >= 0 && !strings.ContainsAny(s, " \t\r\n")
}

// maskEmail turns "john.doe@gmail.com" into "jo••••@gmail.com". Blank stays "".
func maskEmail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 {
		return "•••"
	}
	local, domain := s[:at], s[at:]
	keep := 2
	if len(local) < keep {
		keep = len(local)
	}
	return local[:keep] + strings.Repeat("•", 4) + domain
}
