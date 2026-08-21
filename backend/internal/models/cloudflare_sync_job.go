package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// CloudflareSyncJob is the durable record of a Cloudflare DNS sync run, in the
// same shape as SSLBulkJob: one Mongo doc is the single source of truth for
// status + progress + per-domain items + a bounded live event stream. The WHM
// UI polls this doc (~1.5s) so progress survives a page refresh AND a backend
// restart (boot recovery marks abandoned "running" rows failed). The persisted
// Events slice is what powers the live "watch it work" panel — it is durable,
// not in-memory, so a refresh replays it.
type CloudflareSyncJob struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	OwnerUserID primitive.ObjectID `bson:"owner_user_id,omitempty" json:"owner_user_id,omitempty"`
	TenantID    primitive.ObjectID `bson:"tenant_id,omitempty" json:"tenant_id,omitempty"`

	Kind      string `bson:"kind" json:"kind"`           // "sync_domain" | "sync_all"
	Direction string `bson:"direction" json:"direction"` // "local_to_cf" (push local → Cloudflare)
	Status    string `bson:"status" json:"status"`       // queued|running|completed|failed|cancelled
	// ApplyDeletes gates the ONE destructive action a sync can take: removing
	// records that exist in Cloudflare but not locally (cf_only). Default false
	// → sync only creates/updates, never deletes. Mail records are never
	// deleted regardless.
	ApplyDeletes bool `bson:"apply_deletes" json:"apply_deletes"`

	Total         int    `bson:"total" json:"total"`
	Created       int    `bson:"created" json:"created"`
	Updated       int    `bson:"updated" json:"updated"`
	Deleted       int    `bson:"deleted" json:"deleted"`
	Skipped       int    `bson:"skipped" json:"skipped"`
	Failed        int    `bson:"failed" json:"failed"`
	Progress      int    `bson:"progress" json:"progress"` // 0-100, server-computed
	CurrentDomain string `bson:"current_domain" json:"current_domain"`

	CancelRequested bool `bson:"cancel_requested" json:"cancel_requested"`

	Items  []CloudflareSyncItem  `bson:"items" json:"items"`
	Events []CloudflareSyncEvent `bson:"events" json:"events"`

	Error       string     `bson:"error,omitempty" json:"error,omitempty"`
	StartedAt   time.Time  `bson:"started_at" json:"started_at"`
	UpdatedAt   time.Time  `bson:"updated_at" json:"updated_at"`
	CompletedAt *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
}

// CloudflareSyncItem is one domain's row in the job.
type CloudflareSyncItem struct {
	Domain  string `bson:"domain" json:"domain"`
	Status  string `bson:"status" json:"status"` // pending|running|done|failed|skipped
	Created int    `bson:"created" json:"created"`
	Updated int    `bson:"updated" json:"updated"`
	Deleted int    `bson:"deleted" json:"deleted"`
	Skipped int    `bson:"skipped" json:"skipped"`
	Error   string `bson:"error,omitempty" json:"error,omitempty"`
}

// CloudflareSyncEvent is one line in the durable live event stream.
type CloudflareSyncEvent struct {
	Time    time.Time `bson:"time" json:"time"`
	Level   string    `bson:"level" json:"level"` // info|warn|error
	Domain  string    `bson:"domain,omitempty" json:"domain,omitempty"`
	Message string    `bson:"message" json:"message"`
}
