package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Contact statuses. Only "subscribed" contacts are mailed by campaigns.
const (
	ContactSubscribed   = "subscribed"
	ContactUnsubscribed = "unsubscribed"
	ContactBounced      = "bounced"
	ContactComplained   = "complained"
)

// Contact is one recipient in a user's address book. Email is globally unique
// per user (case-insensitive). Fields holds arbitrary merge values for
// personalization (e.g. {"first_name":"Amit","city":"Kolkata"}). GroupIDs is
// the membership — a contact can belong to many groups. UnsubToken powers the
// public one-click unsubscribe link embedded in campaign mail.
type Contact struct {
	ID         primitive.ObjectID   `bson:"_id,omitempty" json:"id"`
	UserID     primitive.ObjectID   `bson:"user_id" json:"user_id"`
	Email      string               `bson:"email" json:"email"`
	Name       string               `bson:"name" json:"name"`
	Fields     map[string]string    `bson:"fields,omitempty" json:"fields,omitempty"`
	GroupIDs   []primitive.ObjectID `bson:"group_ids" json:"group_ids"`
	Status     string               `bson:"status" json:"status"`
	Source     string               `bson:"source" json:"source"` // manual | import | api
	UnsubToken string               `bson:"unsub_token" json:"-"`
	UnsubAt    *time.Time           `bson:"unsub_at,omitempty" json:"unsub_at,omitempty"`
	CreatedAt  time.Time            `bson:"created_at" json:"created_at"`
	UpdatedAt  time.Time            `bson:"updated_at" json:"updated_at"`
}

// ContactRequest is the create/update body.
type ContactRequest struct {
	Email    string            `json:"email" validate:"required,email"`
	Name     string            `json:"name"`
	Fields   map[string]string `json:"fields"`
	GroupIDs []string          `json:"group_ids"`
	Status   string            `json:"status"` // optional; defaults to subscribed on create
}

// ContactImportRequest bulk-adds contacts (CSV/paste). Existing emails are
// upserted (name/fields/groups merged), not duplicated.
type ContactImportRequest struct {
	GroupIDs []string `json:"group_ids"`
	// Deliberately NOT `dive`-validated per row: the service tolerantly skips
	// invalid rows and reports them in ContactImportResult, so a single bad line
	// in a large paste must never 400 the whole batch.
	Rows []ContactImportRow `json:"rows" validate:"required,min=1"`
}

type ContactImportRow struct {
	Email  string            `json:"email"`
	Name   string            `json:"name"`
	Fields map[string]string `json:"fields"`
}

// ContactImportResult reports what an import did.
type ContactImportResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors,omitempty"`
}
