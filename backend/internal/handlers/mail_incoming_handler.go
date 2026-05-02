// Package handlers — MailIncomingHandler is the internal-only callback the
// VPS Sieve helper (/usr/local/lib/dovecot/sieve-pipe/betazen-mail-hook)
// hits once per delivered message. It is NOT mounted under the JWT-protected
// /api/v1/whm or /api/v1/cpanel groups; the helper authenticates with a
// shared bearer token loaded from /etc/serverpanel/mail-hook.token (root-
// readable file written by the agent at email-stack install time).
//
// The handler runs four steps:
//
//  1. Verify the bearer matches the file (constant-time compare).
//  2. Resolve the recipient to a mailbox row (rejects if unknown — we don't
//     fan out webhooks for addresses we don't manage).
//  3. Look up the tenant via the mailbox's domain so the dispatcher only
//     reaches endpoints owned by the right vendor.
//  4. Emit `email.message.received` with metadata only — Message-ID, From,
//     Subject, Date, Size. The receiver is expected to fetch the body via
//     IMAP/POP3 with the mailbox's own credentials. We deliberately do
//     NOT include the body or attachments here: keeps the payload tiny,
//     keeps secrets (mail content) inside the mail server, and keeps the
//     signing surface small.
package handlers

import (
	"crypto/subtle"
	"os"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
)

// MailHookTokenPath is where the agent writes the shared bearer at
// install time. Root-readable (mode 0600) so the bearer never leaks to
// unprivileged users on the box.
const MailHookTokenPath = "/etc/serverpanel/mail-hook.token"

type MailIncomingHandler struct {
	emailSvc *services.EmailService
	db       *mongo.Database
}

func NewMailIncomingHandler(emailSvc *services.EmailService, db *mongo.Database) *MailIncomingHandler {
	return &MailIncomingHandler{emailSvc: emailSvc, db: db}
}

// IncomingMailRequest is the body shape POSTed by betazen-mail-hook. All
// fields are optional except recipient — the helper extracts what it can
// from the message headers and forwards it. Missing fields land as empty
// strings in the webhook payload.
type IncomingMailRequest struct {
	Recipient string `json:"recipient"`
	Sender    string `json:"sender"`
	Subject   string `json:"subject"`
	MessageID string `json:"message_id"`
	Date      string `json:"date"`
	Size      int    `json:"size"`
	Folder    string `json:"folder"`
}

// Receive is mounted at POST /api/v1/agent/mail/incoming. Bearer-auth via
// X-Mail-Hook-Token (avoids confusion with the JWT Bearer in Authorization).
func (h *MailIncomingHandler) Receive(c *fiber.Ctx) error {
	if !h.verifyHookToken(c) {
		return response.Unauthorized(c, "invalid mail hook token")
	}
	var req IncomingMailRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	recipient := strings.ToLower(strings.TrimSpace(req.Recipient))
	if recipient == "" || !strings.Contains(recipient, "@") {
		return response.BadRequest(c, "recipient is required", nil)
	}

	// Look up the mailbox so the webhook payload carries its id (caller
	// can correlate against panel state) and so we can resolve tenant
	// for dispatch scoping. An unknown recipient = drop (we don't fire
	// webhooks for addresses we don't manage; the operator's receiver
	// would have no way to act on them anyway).
	mailbox, err := h.emailSvc.GetMailboxByAddress(c.UserContext(), recipient)
	if err != nil || mailbox == nil {
		return response.SuccessMessage(c, "ignored: recipient not on this panel", nil)
	}

	tenantID := services.LookupTenantIDForDomain(c.UserContext(), h.db, mailbox.Domain)

	payload := map[string]any{
		"id":             mailbox.ID.Hex(),
		"mailbox_id":     mailbox.ID.Hex(),
		"mailbox_email":  mailbox.Email,
		"email":          mailbox.Email,
		"recipient":      recipient,
		"domain":         mailbox.Domain,
		"sender":         strings.TrimSpace(req.Sender),
		"from":           strings.TrimSpace(req.Sender),
		"subject":        strings.TrimSpace(req.Subject),
		"message_id":     strings.TrimSpace(req.MessageID),
		"date":           strings.TrimSpace(req.Date),
		"size":           req.Size,
		"folder":         strings.TrimSpace(req.Folder),
		"received_at":    time.Now().UTC().Format(time.RFC3339),
		"fetch_protocol": "imap",
		"fetch_hint":     "Use IMAP/POP3 against this server with the mailbox's own credentials to fetch the message body. The webhook payload intentionally carries metadata only.",
	}
	services.EmitEvent(c.UserContext(), "email.message.received", tenantID, payload)
	return response.SuccessMessage(c, "ok", nil)
}

// verifyHookToken reads the X-Mail-Hook-Token header and constant-time
// compares it against the disk token. The handler is mounted on a public
// route (no JWT middleware) so this check is the SOLE defence — be
// careful refactoring it.
func (h *MailIncomingHandler) verifyHookToken(c *fiber.Ctx) bool {
	provided := strings.TrimSpace(c.Get("X-Mail-Hook-Token"))
	if provided == "" {
		// Accept Authorization: Bearer <token> as a secondary form so a
		// curl with just -H "Authorization: ..." also works for local
		// debugging. The Sieve helper uses the X-Mail-Hook-Token header
		// — that's the documented path.
		auth := strings.TrimSpace(c.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			provided = strings.TrimSpace(auth[7:])
		}
	}
	if provided == "" {
		return false
	}
	expected, err := os.ReadFile(MailHookTokenPath)
	if err != nil {
		return false
	}
	exp := strings.TrimSpace(string(expected))
	if exp == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(exp)) == 1
}
