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

// IssueLetsEncryptBulkResponse is the SHAPE of the eventual outcome
// table for a bulk SSL run. Pre-3.1.60 the bulk handler returned this
// synchronously from the POST. From 3.1.60 onward the POST returns a
// job_id immediately and the frontend polls /ssl/bulk-jobs/:id —
// SSLBulkJob below carries this struct's counts + Items live and gets
// updated after every certbot call so the operator sees per-domain
// progress instead of a 10-minute spinner.
//
// The shape is preserved so external scripts that poll the final
// result still see the same JSON; the only delta is the wrapper
// (job_id + status fields on SSLBulkJob).
type IssueLetsEncryptBulkResponse struct {
	Total    int                        `json:"total"`
	Success  int                        `json:"success"`
	Failed   int                        `json:"failed"`
	Issued   int                        `json:"issued"`   // # successes that were fresh issuances
	Reissued int                        `json:"reissued"` // # successes that force-replaced an existing cert
	Items    []IssueLetsEncryptBulkItem `json:"items"`
}

// SSLBulkJobStatus is the lifecycle of a bulk SSL run as the operator
// sees it. The values are deliberately user-facing strings so the
// frontend can render them directly without a lookup map.
const (
	SSLBulkJobStatusQueued    = "queued"     // job row created, goroutine not yet scheduled
	SSLBulkJobStatusRunning   = "running"    // goroutine is iterating domains
	SSLBulkJobStatusCompleted = "completed"  // every domain processed (mix of success + fail is normal)
	SSLBulkJobStatusFailed    = "failed"     // catastrophic abort — server restart, panic, etc.
	SSLBulkJobStatusCancelled = "cancelled"  // operator hit Cancel mid-run
)

// SSLBulkJob is the durable progress record the frontend polls for
// live updates during the "Issue / Reissue N Certificates" flow.
//
// One doc per operator click. Lifecycle:
//
//   1. Handler creates the doc with Status=queued, Items pre-populated
//      with the de-duped domain list (each marked Pending), Total set,
//      and returns the job ID.
//   2. Background goroutine flips to Status=running, walks Items in
//      order. Before each domain it stamps CurrentDomain so the UI
//      can render "Currently issuing: <domain>"; after each domain
//      it updates that row in-place with Success / Error / Action +
//      bumps Progress.
//   3. When the last row finishes, FinishedAt + Status=completed are
//      stamped.
//
// Status=failed is reserved for the rare case where the goroutine
// itself can't continue (DB write loop fails, panic recovered) —
// per-domain certbot failures land as Items[i].Success=false +
// Items[i].Error and don't change the job's Status.
//
// OwnerUserID + TenantID enforce tenant-scoped poll access: a vendor
// can only see their own jobs. The platform owner can see all.
type SSLBulkJob struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	OwnerUserID   primitive.ObjectID `bson:"owner_user_id,omitempty" json:"owner_user_id"`
	TenantID      primitive.ObjectID `bson:"tenant_id,omitempty" json:"tenant_id,omitempty"`
	Status        string             `bson:"status" json:"status"` // SSLBulkJobStatus*
	// Total is the de-duped domain count — what the operator's
	// "Reissuing 27..." button counted down to. Set once at create
	// time and never modified.
	Total int `bson:"total" json:"total"`
	// Success / Failed / Issued / Reissued are live counters bumped
	// after each Item completes. Derived data: equivalent to scanning
	// Items, but pre-computed so the UI can render the summary line
	// in O(1).
	Success  int `bson:"success" json:"success"`
	Failed   int `bson:"failed" json:"failed"`
	Issued   int `bson:"issued" json:"issued"`
	Reissued int `bson:"reissued" json:"reissued"`
	// Progress is 0..100, derived as ((Success+Failed)/Total)*100,
	// rounded down. Updated alongside the counters so the UI doesn't
	// have to math it.
	Progress int `bson:"progress" json:"progress"`
	// CurrentDomain is the domain whose certbot run is in flight RIGHT
	// NOW (between "we picked it up" and "we wrote the row outcome").
	// Empty when the goroutine isn't actively certbot-ing anything —
	// e.g. between two domains, or once the job is complete.
	CurrentDomain string                       `bson:"current_domain,omitempty" json:"current_domain,omitempty"`
	Items         []IssueLetsEncryptBulkItem   `bson:"items" json:"items"`
	// Wildcard / Reissue mirror the request flags so a polling client
	// that lost the original modal state can still tell what was
	// asked for.
	Wildcard bool `bson:"wildcard" json:"wildcard"`
	Reissue  bool `bson:"reissue" json:"reissue"`
	// CancelRequested is set by POST /ssl/bulk-jobs/:id/cancel. The
	// loop checks it between domains and aborts cleanly, flipping
	// Status to cancelled. In-flight certbot calls are NOT killed —
	// certbot's --non-interactive mode runs to completion in seconds-
	// to-minutes and forcing it could leave half-written cert files.
	CancelRequested bool       `bson:"cancel_requested" json:"cancel_requested"`
	StartedAt       time.Time  `bson:"started_at" json:"started_at"`
	FinishedAt      *time.Time `bson:"finished_at,omitempty" json:"finished_at,omitempty"`
	CreatedAt       time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time  `bson:"updated_at" json:"updated_at"`
}

// IssueLetsEncryptBulkStartResponse is the body of the POST that
// kicks off a bulk run. job_id is the only field a client needs to
// start polling; total + status are echoed back so a curl-using
// integrator sees the right shape without a second round-trip.
type IssueLetsEncryptBulkStartResponse struct {
	JobID  string `json:"job_id"`
	Total  int    `json:"total"`
	Status string `json:"status"`
}
