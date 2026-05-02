package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// WebhookEndpoint is a caller-owned URL the panel POSTs to whenever an event
// the endpoint subscribed to fires. Each endpoint has its own HMAC-SHA256
// signing secret so a leak of one webhook's secret does not let an attacker
// forge deliveries to a different endpoint.
type WebhookEndpoint struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	URL         string             `bson:"url" json:"url"`
	Description string             `bson:"description,omitempty" json:"description,omitempty"`

	OwnerKind   string             `bson:"owner_kind" json:"owner_kind"`
	OwnerUserID primitive.ObjectID `bson:"owner_user_id" json:"owner_user_id"`
	TenantID    primitive.ObjectID `bson:"tenant_id" json:"tenant_id"`

	// MailboxEmail, when non-empty, scopes this endpoint to a single
	// mailbox: only events whose payload concerns that exact address fire
	// to this endpoint. Empty means tenant-wide. Email (not ObjectID) is
	// the scope key so server transfers carry the binding intact — emails
	// are stable across servers, mailbox _ids are minted fresh on import.
	MailboxEmail string `bson:"mailbox_email,omitempty" json:"mailbox_email,omitempty"`

	Events []string `bson:"events" json:"events"`

	SecretEnc []byte `bson:"secret_enc" json:"-"`
	Prefix    string `bson:"prefix" json:"prefix"`

	Active bool `bson:"active" json:"active"`

	LastSuccessAt *time.Time `bson:"last_success_at,omitempty" json:"last_success_at,omitempty"`
	LastFailureAt *time.Time `bson:"last_failure_at,omitempty" json:"last_failure_at,omitempty"`
	LastError     string     `bson:"last_error,omitempty" json:"last_error,omitempty"`

	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// WebhookDelivery is one attempt to POST an event to an endpoint. We keep the
// log short (default 30 days) so vendors can debug their integration without
// the collection growing unboundedly.
type WebhookDelivery struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	EndpointID primitive.ObjectID `bson:"endpoint_id" json:"endpoint_id"`
	Event      string             `bson:"event" json:"event"`
	Payload    string             `bson:"payload" json:"payload"`
	Signature  string             `bson:"signature" json:"signature"`
	Timestamp  int64              `bson:"timestamp" json:"timestamp"`

	StatusCode int    `bson:"status_code,omitempty" json:"status_code,omitempty"`
	Attempts   int    `bson:"attempts" json:"attempts"`
	Success    bool   `bson:"success" json:"success"`
	Error      string `bson:"error,omitempty" json:"error,omitempty"`

	NextRetryAt *time.Time `bson:"next_retry_at,omitempty" json:"next_retry_at,omitempty"`
	DeliveredAt *time.Time `bson:"delivered_at,omitempty" json:"delivered_at,omitempty"`
	CreatedAt   time.Time  `bson:"created_at" json:"created_at"`
}

// CreateWebhookEndpointRequest is the body shape posted to POST /webhooks.
// Events must be a non-empty subset of AllWebhookEvents; the service rejects
// unknown event keys at validation time.
type CreateWebhookEndpointRequest struct {
	URL         string   `json:"url" validate:"required,url"`
	Description string   `json:"description"`
	Events      []string `json:"events" validate:"required,min=1"`
	Active      *bool    `json:"active"`
	// MailboxEmail (optional) scopes this endpoint to a single mailbox.
	// When set, only events whose payload concerns that address fire here.
	// Empty/omitted = tenant-wide.
	MailboxEmail string `json:"mailbox_email"`
}

// IssuedWebhook returns the freshly-created endpoint plus its signing secret in
// plaintext exactly once. Same model as APIToken: the operator must copy the
// secret out of this response or rotate it later.
type IssuedWebhook struct {
	Webhook         *WebhookEndpoint `json:"webhook"`
	PlaintextSecret string           `json:"plaintext_secret"`
}

// WebhookEvent describes one event key the panel can fire. The Developer page
// renders this list as a multi-select on the Create Webhook form.
type WebhookEvent struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Group       string `json:"group"`
}

// AllWebhookEvents is the canonical list. New events appended here become
// available in the UI on the next build with no schema change required.
var AllWebhookEvents = []WebhookEvent{
	{Key: "domain.created", Label: "Domain created", Description: "Fired after a new domain is added", Group: "Domain"},
	{Key: "domain.deleted", Label: "Domain deleted", Description: "Fired after a domain is removed", Group: "Domain"},
	{Key: "ssl.issued", Label: "SSL certificate issued", Description: "Fired after Let's Encrypt issues a certificate", Group: "SSL"},
	{Key: "ssl.renewed", Label: "SSL certificate renewed", Description: "Fired on automatic or manual renewal", Group: "SSL"},
	{Key: "ssl.forced", Label: "Force HTTPS toggled", Description: "Fired when Force-SSL is enabled or disabled", Group: "SSL"},
	{Key: "email.mailbox.created", Label: "Mailbox created", Description: "Fired after a mailbox is provisioned", Group: "Email"},
	{Key: "email.mailbox.deleted", Label: "Mailbox deleted", Description: "Fired after a mailbox is deleted", Group: "Email"},
	{Key: "email.forwarder.created", Label: "Forwarder created", Description: "Fired after a forwarder is added", Group: "Email"},
	// Inbound delivery — fires once per message after Dovecot's LMTP/LDA
	// hands it to the user's maildir. Payload carries metadata only
	// (recipient, sender, subject, message-id, date, size); the receiver
	// is expected to fetch the full body via IMAP/POP3 with the
	// mailbox's own credentials. Pair with mailbox_email scoping on the
	// endpoint to receive notifications for one mailbox at a time.
	{Key: "email.message.received", Label: "Message received", Description: "Fired after a message is delivered to a panel-managed mailbox (metadata only)", Group: "Email"},
	{Key: "deploy.linked", Label: "Domain linked to service", Description: "Fired when a domain is attached to a Deploy Software service", Group: "Deploy"},
	{Key: "deploy.unlinked", Label: "Domain unlinked", Description: "Fired when a domain is detached from a service", Group: "Deploy"},
	{Key: "deploy.completed", Label: "Deployment completed", Description: "Fired when a Deploy Software service finishes deploying", Group: "Deploy"},
	{Key: "deploy.failed", Label: "Deployment failed", Description: "Fired when a Deploy Software service fails to deploy", Group: "Deploy"},
}

// IsKnownWebhookEvent returns true if the key matches an entry in AllWebhookEvents.
func IsKnownWebhookEvent(key string) bool {
	for _, e := range AllWebhookEvents {
		if e.Key == key {
			return true
		}
	}
	return false
}
