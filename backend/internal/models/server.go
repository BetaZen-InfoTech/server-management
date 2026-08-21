package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Server is the panel's stable identity for the machine it manages.
//
// Before this model existed, a "server" was only ever identified by its
// current primary IPv4 (re-derived live from `hostname -I`). That made the IP
// a de-facto identity, which breaks the moment a VPS is migrated or its address
// changes. Server decouples identity (the immutable ServerID UUID) from the
// mutable network attribute (CurrentIP), and keeps an append-only IPHistory so
// an operator can audit every address the box has ever had.
//
// This is ADDITIVE. Nothing else in the panel is required to reference ServerID
// yet. Every existing IP-bearing field — dns_zones.server_ip, domains.server_ip,
// the server_config{key:"server_ip"} mirror, and .env SERVER_IP — keeps its
// exact current meaning ("the current IPv4") and is untouched, because the
// ReassignServerIP sweep and the DNS-value-rewrite guard both depend on the
// literal current IP still living in those fields. This row is seeded once at
// boot (services.SeedServerIdentity) and its IP is only ever changed at the
// single IP-change chokepoint (ConfigService.ReassignServerIP), where a new
// IPHistory entry is appended.
type Server struct {
	ID primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	// ServerID is the stable, IP-independent identity (UUIDv4). Never changes
	// once seeded — this is what future rows/migrations reference instead of IP.
	ServerID   string           `bson:"server_id" json:"server_id"`
	Hostname   string           `bson:"hostname" json:"hostname"`
	CurrentIP  string           `bson:"current_ip" json:"current_ip"`
	PreviousIP string           `bson:"previous_ip,omitempty" json:"previous_ip,omitempty"`
	// IPHistory is append-only; index 0 is the earliest recorded address.
	IPHistory []IPHistoryEntry `bson:"ip_history,omitempty" json:"ip_history,omitempty"`
	CreatedAt time.Time        `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time        `bson:"updated_at" json:"updated_at"`
}

// IPHistoryEntry records one address the server held, and why it changed.
type IPHistoryEntry struct {
	IP    string    `bson:"ip" json:"ip"`
	SetAt time.Time `bson:"set_at" json:"set_at"`
	// Reason is a short free-form note: "seed", "reassign-ip", "transfer:<jobID>".
	Reason string `bson:"reason,omitempty" json:"reason,omitempty"`
}
