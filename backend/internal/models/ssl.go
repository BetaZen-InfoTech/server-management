package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type SSLCertificate struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Domain         string             `bson:"domain" json:"domain"`
	Issuer         string             `bson:"issuer" json:"issuer"`
	Type           string             `bson:"type" json:"type"`
	Domains        []string           `bson:"domains" json:"domains"`
	IssuedAt       *time.Time         `bson:"issued_at" json:"issued_at"`
	ExpiresAt      *time.Time         `bson:"expires_at" json:"expires_at"`
	DaysRemaining  int                `bson:"days_remaining" json:"days_remaining"`
	AutoRenew      bool               `bson:"auto_renew" json:"auto_renew"`
	ForceSSL       bool               `bson:"force_ssl" json:"force_ssl"`
	Wildcard       bool               `bson:"wildcard" json:"wildcard"`
	KeyType        string             `bson:"key_type" json:"key_type"`
	SerialNumber   string             `bson:"serial_number" json:"serial_number"`
	FingerprintSHA256 string          `bson:"fingerprint_sha256" json:"fingerprint_sha256"`
	CertPath       string             `bson:"cert_path" json:"-"`
	KeyPath        string             `bson:"key_path" json:"-"`
	CABundlePath   string             `bson:"ca_bundle_path" json:"-"`
	CreatedAt      time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt      time.Time          `bson:"updated_at" json:"updated_at"`
}

type IssueLetsEncryptRequest struct {
	Domain string `json:"domain" validate:"required"`
	// Email is now an OPTIONAL ACME registration override. When empty
	// the service auto-derives admin@<owning-vendor> via the same
	// LookupVendorEmailForUsername walk the notifier uses, with a
	// last-resort fallback to admin@<domain>. Kept on the wire so old
	// API consumers that still pass it keep working.
	Email             string   `json:"email" validate:"omitempty,email"`
	Wildcard          bool     `json:"wildcard"`
	AdditionalDomains []string `json:"additional_domains"`
	// Reissue forces a fresh certbot run even when a valid certificate
	// already exists on disk. Default false preserves the historical
	// "skip if cert is already present" short-circuit so legacy API
	// callers don't suddenly burn Let's Encrypt rate-limit slots.
	// Set true to mint a brand-new cert (e.g. after a key compromise
	// or to repair a corrupt live lineage) — paired with the
	// /ssl/:domain/reissue endpoint and the WHM/User Panel "Issue /
	// Reissue Certificate" UI affordance.
	Reissue bool `json:"reissue"`
}

type UploadCustomCertRequest struct {
	Domain      string `json:"domain" validate:"required"`
	Certificate string `json:"certificate" validate:"required"`
	PrivateKey  string `json:"private_key" validate:"required"`
	CABundle    string `json:"ca_bundle"`
}

// IssueLetsEncryptBulkRequest is the payload for issuing SSL across N
// domains in one operator click. The backend processes the list
// serially (one certbot run at a time) so Let's Encrypt rate limits
// stay predictable and a partial failure doesn't abort the whole
// batch — every item produces an entry in the response, success or
// failure.
//
// Email is OPTIONAL and effectively unused by the modern UI — the
// service auto-resolves the ACME email per-domain from each domain's
// owning vendor (so a multi-vendor batch gets each cert registered
// under its own vendor's address). Kept on the wire so legacy API
// consumers can still pass an explicit override; left blank by the
// frontend.
//
// Wildcard is a single flag rather than per-domain because in
// practice operators want either "all wildcard" or "all SAN" in one
// pass — mixed batches are rare enough to warrant a second submit.
type IssueLetsEncryptBulkRequest struct {
	Domains  []string `json:"domains" validate:"required,min=1,dive,required"`
	Email    string   `json:"email" validate:"omitempty,email"`
	Wildcard bool     `json:"wildcard"`
	// Reissue forces a fresh cert even when one is already on disk for
	// the domain. Used by the "Issue / Reissue Certificate" modal when
	// the operator picks domains from the Active SSL tab — the intent
	// is to mint a new cert, not skip with "already present".
	Reissue bool `json:"reissue"`
}

// IssueLetsEncryptBulkItem is one entry in the bulk-issue response.
// Success carries the issued cert's id + expiry; failure carries the
// already-friendlied certbot error so the UI can surface it inline
// next to the offending domain. Domain is always populated so the
// frontend can pair items even when the order it sent diverges.
type IssueLetsEncryptBulkItem struct {
	Domain    string     `json:"domain"`
	Success   bool       `json:"success"`
	Error     string     `json:"error,omitempty"`
	CertID    string     `json:"cert_id,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// Action records which path produced this item: "issued" for a
	// fresh issuance (no cert was on disk before) or "reissued" when
	// the existing cert was force-replaced. Lets the UI render
	// "Issued 4, reissued 32" instead of a single ambiguous count.
	Action string `json:"action,omitempty"`
}

// IssueLetsEncryptBulkResponse is what the bulk handler returns.
// Counts are pre-computed server-side so the UI doesn't have to walk
// Items twice (once for the toast summary, once for the per-row
// rendering).
type IssueLetsEncryptBulkResponse struct {
	Total    int                        `json:"total"`
	Success  int                        `json:"success"`
	Failed   int                        `json:"failed"`
	Issued   int                        `json:"issued"`   // # successes that were fresh issuances
	Reissued int                        `json:"reissued"` // # successes that force-replaced an existing cert
	Items    []IssueLetsEncryptBulkItem `json:"items"`
}
