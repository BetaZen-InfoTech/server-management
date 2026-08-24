package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Domain bulk-upload job status values.
const (
	DomainBulkJobQueued    = "queued"    // row created, goroutine not yet scheduled
	DomainBulkJobRunning   = "running"   // goroutine is iterating rows
	DomainBulkJobCompleted = "completed" // every row processed
	DomainBulkJobFailed    = "failed"    // catastrophic abort (restart, panic)
	DomainBulkJobCancelled = "cancelled" // operator cancelled mid-run
)

// Per-row item status — the LIVE state each domain moves through so the UI can
// render a per-domain progress list.
const (
	DomainBulkItemPending  = "pending"  // not started yet
	DomainBulkItemCreating = "creating" // Create in flight
	DomainBulkItemDone     = "done"     // created OK
	DomainBulkItemFailed   = "failed"   // create failed / row invalid
)

// DomainBulkJobItem is one row's live outcome. The JSON tags mirror the
// frontend BulkUploadDomainsRow so the modal renders it directly, plus a Status
// field for the live per-domain progress.
type DomainBulkJobItem struct {
	RowNumber            int      `bson:"row_number" json:"row_number"`
	Domain               string   `bson:"domain" json:"domain"`
	User                 string   `bson:"user" json:"user"`
	Status               string   `bson:"status" json:"status"`
	Success              bool     `bson:"success" json:"success"`
	Error                string   `bson:"error,omitempty" json:"error,omitempty"`
	SSLIssued            bool     `bson:"ssl_issued" json:"ssl_issued"`
	SSLForced            bool     `bson:"ssl_forced" json:"ssl_forced"`
	SSLMessage           string   `bson:"ssl_message,omitempty" json:"ssl_message,omitempty"`
	SetupWarnings        []string `bson:"setup_warnings,omitempty" json:"setup_warnings,omitempty"`
	AdminMailbox         string   `bson:"admin_mailbox,omitempty" json:"admin_mailbox,omitempty"`
	AdminMailboxPassword string   `bson:"admin_mailbox_password,omitempty" json:"admin_mailbox_password,omitempty"`
}

// DomainBulkJob is the durable record of a bulk domain-upload run. Progress /
// items / counters are updated in place as the background worker processes each
// row, so the Domains modal can poll and render live per-domain progress, and
// the job survives a backend restart (boot recovery marks abandoned runs failed).
type DomainBulkJob struct {
	ID              primitive.ObjectID  `bson:"_id,omitempty" json:"id"`
	OwnerUserID     primitive.ObjectID  `bson:"owner_user_id,omitempty" json:"owner_user_id,omitempty"`
	TenantID        primitive.ObjectID  `bson:"tenant_id,omitempty" json:"tenant_id,omitempty"`
	Status          string              `bson:"status" json:"status"`
	Format          string              `bson:"format" json:"format"` // csv | xlsx
	Total           int                 `bson:"total" json:"total"`
	Processed       int                 `bson:"processed" json:"processed"`
	Successes       int                 `bson:"successes" json:"successes"`
	Failures        int                 `bson:"failures" json:"failures"`
	SSLIssued       int                 `bson:"ssl_issued" json:"ssl_issued"`
	SSLForced       int                 `bson:"ssl_forced" json:"ssl_forced"`
	Progress        int                 `bson:"progress" json:"progress"` // 0..100
	CurrentDomain   string              `bson:"current_domain,omitempty" json:"current_domain,omitempty"`
	Items           []DomainBulkJobItem `bson:"items" json:"items"`
	IssueSSL        bool                `bson:"issue_ssl" json:"issue_ssl"`
	ForceSSL        bool                `bson:"force_ssl" json:"force_ssl"`
	Error           string              `bson:"error,omitempty" json:"error,omitempty"`
	CancelRequested bool                `bson:"cancel_requested" json:"cancel_requested"`
	StartedAt       time.Time           `bson:"started_at" json:"started_at"`
	FinishedAt      *time.Time          `bson:"finished_at,omitempty" json:"finished_at,omitempty"`
	CreatedAt       time.Time           `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time           `bson:"updated_at" json:"updated_at"`
}

// DomainBulkJobStartResponse is returned by the POST that kicks off the job so
// the client can immediately start polling GET .../bulk-upload/jobs/{id}.
type DomainBulkJobStartResponse struct {
	JobID  string `json:"job_id"`
	Total  int    `json:"total"`
	Status string `json:"status"`
}
