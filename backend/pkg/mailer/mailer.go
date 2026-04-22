// Package mailer sends transactional email from the panel itself — password
// resets, security alerts, domain-expiry warnings. It is deliberately
// separate from internal/services/email_service.go (which manages tenant
// mailboxes on Postfix/Dovecot) because the panel's outbound needs are
// simpler: one configured SMTP relay, a handful of hardcoded templates,
// and no storage concerns.
//
// Config is read from the Panel Mail Config singleton at startup and on
// every update. Callers get a Mailer instance with Reload() to pick up
// changes without a process restart.
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// Config is what the UI's "Outgoing Mail" card collects. Password is
// stored AES-GCM encrypted in Mongo; callers decrypt before passing it
// here. Empty Host means "mailer is disabled" — Send returns a typed
// ErrDisabled so handlers can branch on it (e.g. log the reset token
// to stderr in dev, or show the operator "configure SMTP first").
type Config struct {
	Host       string // e.g. "smtp.gmail.com"
	Port       int    // 465 (SMTPS), 587 (STARTTLS), 25 (plaintext), 2525 (alt)
	Username   string
	Password   string
	TLSMode    string // "none" | "starttls" | "tls" (implicit / SMTPS)
	FromAddr   string // e.g. "noreply@panel.example.com"
	FromName   string // e.g. "Betazen Server Panel"
	ReplyTo    string // optional
	SkipVerify bool   // for self-signed relays in dev; never expose in prod UI
}

// Valid reports whether the config has the minimum fields to send.
// Host+Port+FromAddr is enough for relays that don't require auth
// (many cloud providers expose an unauthenticated relay on the VPC).
func (c Config) Valid() bool {
	return strings.TrimSpace(c.Host) != "" && c.Port > 0 && strings.TrimSpace(c.FromAddr) != ""
}

// ErrDisabled is returned when Send is called on a Mailer with no
// Config. Handlers compare against it with errors.Is.
var ErrDisabled = errors.New("mailer: SMTP is not configured")

// Mailer is a thread-safe SMTP sender. One per process; Reload swaps
// the underlying Config atomically.
type Mailer struct {
	mu   sync.RWMutex
	cfg  Config
}

func New(cfg Config) *Mailer {
	return &Mailer{cfg: cfg}
}

func (m *Mailer) Reload(cfg Config) {
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
}

func (m *Mailer) Config() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// Enabled reports whether the current Config can send. Handlers can
// short-circuit (e.g. skip sending and log the token to the audit log)
// when it's false so a missing SMTP config doesn't break the forgot-
// password flow — the operator still gets the token via the UI.
func (m *Mailer) Enabled() bool {
	return m.Config().Valid()
}

// Message is one outgoing email. HTML is optional; if empty we send
// text/plain only. Subject + From are filled from the Mailer's Config
// when not set explicitly so callers don't repeat them.
type Message struct {
	To       string
	Subject  string
	Text     string
	HTML     string
	ReplyTo  string // overrides Config.ReplyTo when set
}

// Send delivers one message via the configured SMTP relay. Returns
// ErrDisabled when no host is set. The 15-second context timeout is
// per-call — long enough for a TLS handshake + multi-step SMTP, short
// enough that a down relay doesn't hang an HTTP handler.
func (m *Mailer) Send(ctx context.Context, msg Message) error {
	cfg := m.Config()
	if !cfg.Valid() {
		return ErrDisabled
	}
	if strings.TrimSpace(msg.To) == "" {
		return errors.New("mailer: To is required")
	}
	if strings.TrimSpace(msg.Subject) == "" {
		return errors.New("mailer: Subject is required")
	}

	// Build the RFC 5322 message. A minimal multipart/alternative
	// block lets relays (gmail, outlook) render HTML while keeping a
	// plain-text fallback for security-minded clients that strip HTML.
	from := mail.Address{Name: cfg.FromName, Address: cfg.FromAddr}
	to := mail.Address{Address: strings.TrimSpace(msg.To)}
	replyTo := firstNonEmpty(msg.ReplyTo, cfg.ReplyTo)
	body := buildRFC5322(from, to, replyTo, msg)

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	conn, err := dial(dialCtx, cfg, addr)
	if err != nil {
		return fmt.Errorf("mailer: dial %s: %w", addr, err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("mailer: smtp handshake: %w", err)
	}
	defer c.Quit() //nolint:errcheck  — best-effort cleanup

	// STARTTLS upgrade for port 587 / plaintext ports that support it.
	if cfg.TLSMode == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			tlsCfg := &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.SkipVerify} //nolint:gosec
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("mailer: STARTTLS: %w", err)
			}
		}
	}

	// Auth only when the operator supplied credentials. Plain relays
	// (e.g. the local postfix installed by install.sh) don't require auth.
	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("mailer: auth: %w", err)
		}
	}

	if err := c.Mail(cfg.FromAddr); err != nil {
		return fmt.Errorf("mailer: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("mailer: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mailer: DATA: %w", err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		return fmt.Errorf("mailer: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer: close body: %w", err)
	}
	return nil
}

// dial opens the transport connection to the SMTP relay, optionally
// wrapping it in TLS up-front (SMTPS / port 465). STARTTLS is handled
// after the plain SMTP handshake in Send.
func dial(ctx context.Context, cfg Config, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	if cfg.TLSMode == "tls" {
		tlsCfg := &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.SkipVerify} //nolint:gosec
		return tls.DialWithDialer(&d, "tcp", addr, tlsCfg)
	}
	return d.DialContext(ctx, "tcp", addr)
}

// buildRFC5322 assembles a MIME message with a multipart/alternative
// body when HTML is set, or a simple text/plain body otherwise. We
// build this by hand rather than pulling in a third-party library to
// keep the dependency footprint tiny and the output easy to audit.
func buildRFC5322(from, to mail.Address, replyTo string, msg Message) string {
	var sb strings.Builder
	sb.WriteString("From: " + from.String() + "\r\n")
	sb.WriteString("To: " + to.String() + "\r\n")
	if replyTo != "" {
		sb.WriteString("Reply-To: " + replyTo + "\r\n")
	}
	sb.WriteString("Subject: " + encodeHeader(msg.Subject) + "\r\n")
	sb.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")

	if msg.HTML == "" {
		sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		sb.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		sb.WriteString(msg.Text)
		return sb.String()
	}

	boundary := fmt.Sprintf("sp_%d_boundary", time.Now().UnixNano())
	sb.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundary))
	if msg.Text != "" {
		sb.WriteString("--" + boundary + "\r\n")
		sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		sb.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		sb.WriteString(msg.Text + "\r\n")
	}
	sb.WriteString("--" + boundary + "\r\n")
	sb.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	sb.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	sb.WriteString(msg.HTML + "\r\n")
	sb.WriteString("--" + boundary + "--\r\n")
	return sb.String()
}

// encodeHeader RFC 2047–encodes a header value only when it contains
// non-ASCII. Keeps ASCII subjects readable in raw logs while still
// handling unicode ("résumé", emoji, …).
func encodeHeader(s string) string {
	for _, r := range s {
		if r > 0x7e {
			return mime.QEncoding.Encode("UTF-8", s)
		}
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
