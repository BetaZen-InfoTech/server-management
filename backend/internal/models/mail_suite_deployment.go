package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// MailSuiteDeployment records a registered mail-suite backend the WHM
// panel knows about. Storing url+token here lets the WHM admin page
// trigger per-domain "Enable Mail" and open the webmail without
// hardcoding the URL in the panel binary.
//
// Tenant identity (vendor_id / tenant_id) is intentionally optional —
// a single-vendor deployment has one global entry; a multi-tenant
// deployment scopes by vendor_id.
type MailSuiteDeployment struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Label       string             `bson:"label" json:"label"`
	URL         string             `bson:"url" json:"url"`
	ServiceToken string            `bson:"service_token" json:"-"`
	VendorID    primitive.ObjectID `bson:"vendor_id,omitempty" json:"vendor_id,omitempty"`
	WebmailURL  string             `bson:"webmail_url" json:"webmail_url"`
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time          `bson:"updated_at" json:"updated_at"`
}

type RegisterMailSuiteRequest struct {
	Label        string `json:"label" validate:"required"`
	URL          string `json:"url" validate:"required,url"`
	ServiceToken string `json:"service_token" validate:"required"`
	WebmailURL   string `json:"webmail_url"`
}
