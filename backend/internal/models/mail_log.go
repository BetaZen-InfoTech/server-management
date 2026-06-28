package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// MailLogEntry is one correlated mail event — a single Postfix queue item —
// captured by the MailLogService ingestor from /var/log/mail.log.
//
// Why this exists: the pre-3.1.108 panel only ever "saw" mail through the
// Dovecot Sieve webhook hook, which fires once per message DELIVERED to a
// local maildir. That hook is structurally blind to everything else —
// outbound mail, mail submitted by third-party SMTP clients (Thunderbird,
// Outlook desktop, mobile apps), API/sendmail-injected mail, and mail that
// never reaches a local mailbox. As a result, "emails sent from third-party
// clients are not appearing in the application's mail logs."
//
// The ingestor closes that gap by reading the authoritative system record —
// Postfix's own log — which records EVERY message regardless of source, and
// correlating the per-queue-id lines (smtpd → cleanup → qmgr → delivery)
// into one structured row carrying the fields operators need: timestamp,
// source, client, IP, sender, recipient(s), subject, status, delivery
// result, SMTP response, message-id, queue status, attachments, and auth
// method.
type MailLogEntry struct {
	ID primitive.ObjectID `bson:"_id,omitempty" json:"id"`

	// LogKey is the idempotent-upsert natural key: "<queue_id>:<first_seen_unix>".
	// Postfix recycles queue IDs over time, so pairing the id with the
	// first-seen second prevents two unrelated messages from being merged.
	LogKey  string `bson:"log_key" json:"log_key"`
	QueueID string `bson:"queue_id" json:"queue_id"`

	// Direction is a best-effort classification derived from how the
	// message entered and where it was delivered:
	//   inbound  — arrived via unauthenticated smtpd (port 25) for a local box
	//   outbound — authenticated submission / local injection relayed offsite
	//   internal — local sender to local recipient (delivered via Dovecot)
	//   unknown  — not enough lines correlated to decide
	Direction string `bson:"direction" json:"direction"`

	// Source is where the message originated, the field that makes
	// third-party-client mail finally visible:
	//   webmail        — authenticated submission from 127.0.0.1 (Roundcube)
	//   smtp-client    — authenticated submission from a remote IP
	//                    (Thunderbird / Outlook / mobile / external app)
	//   api-local      — injected via local sendmail/pickup (panel, PHP
	//                    mail(), cron, Laravel/Node/Python/Go/WordPress)
	//   inbound-smtp   — unauthenticated delivery from the internet
	//   unknown        — could not classify
	Source     string `bson:"source" json:"source"`
	Client     string `bson:"client,omitempty" json:"client,omitempty"`
	ClientIP   string `bson:"client_ip,omitempty" json:"client_ip,omitempty"`
	AuthUser   string `bson:"auth_user,omitempty" json:"auth_user,omitempty"`
	AuthMethod string `bson:"auth_method,omitempty" json:"auth_method,omitempty"`

	Sender         string             `bson:"sender" json:"sender"`
	Recipients     []MailLogRecipient `bson:"recipients" json:"recipients"`
	Subject        string             `bson:"subject,omitempty" json:"subject,omitempty"`
	MessageID      string             `bson:"message_id,omitempty" json:"message_id,omitempty"`
	ContentType    string             `bson:"content_type,omitempty" json:"content_type,omitempty"`
	HasAttachments bool               `bson:"has_attachments" json:"has_attachments"`

	SizeBytes int64 `bson:"size_bytes" json:"size_bytes"`
	NRcpt     int   `bson:"nrcpt" json:"nrcpt"`

	// Status is the overall message outcome, rolled up across recipients:
	//   sent | deferred | bounced | rejected | received | queued
	Status       string `bson:"status" json:"status"`
	SMTPResponse string `bson:"smtp_response,omitempty" json:"smtp_response,omitempty"`

	// Domains lists every LOCAL (panel-managed-or-not) domain that appears
	// in the sender or recipient addresses. Tenant-scoped callers only see
	// rows whose Domains intersect their tenant's domains; the platform
	// owner sees everything. Storing the raw domains here lets the read
	// path scope without a per-message ownership lookup at ingest time.
	Domains []string `bson:"domains,omitempty" json:"domains,omitempty"`

	ServerIP string `bson:"server_ip,omitempty" json:"server_ip,omitempty"`
	Hostname string `bson:"hostname,omitempty" json:"hostname,omitempty"`

	// Queued is true until Postfix logs "<qid>: removed" (message left the
	// queue). A row stuck with queued=true + status=deferred is mail still
	// retrying in the active/deferred queue.
	Queued    bool      `bson:"queued" json:"queued"`
	FirstSeen time.Time `bson:"first_seen" json:"first_seen"`
	LastEvent time.Time `bson:"last_event" json:"last_event"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// MailLogRecipient is one delivery attempt within a message. Postfix logs
// one delivery line per recipient, so a message to three addresses carries
// three entries here, each with its own status / DSN / SMTP response.
type MailLogRecipient struct {
	Address     string    `bson:"address" json:"address"`
	Status      string    `bson:"status" json:"status"` // sent|deferred|bounced|expired|rejected
	DSN         string    `bson:"dsn,omitempty" json:"dsn,omitempty"`
	Relay       string    `bson:"relay,omitempty" json:"relay,omitempty"`
	Response    string    `bson:"response,omitempty" json:"response,omitempty"`
	Delay       string    `bson:"delay,omitempty" json:"delay,omitempty"`
	DeliveredAt time.Time `bson:"delivered_at,omitempty" json:"delivered_at,omitempty"`
}

// MailLogStats is the summary surfaced above the mail-log table.
type MailLogStats struct {
	Total      int64            `json:"total"`
	ByStatus   map[string]int64 `json:"by_status"`
	ByDirection map[string]int64 `json:"by_direction"`
	BySource   map[string]int64 `json:"by_source"`
	WindowDays int              `json:"window_days"`
}
