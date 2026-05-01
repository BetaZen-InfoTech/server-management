package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type DNSZone struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Domain        string             `bson:"domain" json:"domain"`
	ServerIP      string             `bson:"server_ip" json:"server_ip"`
	AdminEmail    string             `bson:"admin_email" json:"admin_email"`
	Nameservers   []string           `bson:"nameservers" json:"nameservers"`
	DNSSECEnabled bool               `bson:"dnssec_enabled" json:"dnssec_enabled"`
	Status        string             `bson:"status,omitempty" json:"status"`
	Serial        int                `bson:"serial" json:"serial"`
	RecordsCount  int                `bson:"-" json:"records_count"`
	CreatedAt     time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt     time.Time          `bson:"updated_at" json:"updated_at"`
}

type DNSRecord struct {
	ID       primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	ZoneID   primitive.ObjectID `bson:"zone_id" json:"zone_id"`
	Type     string             `bson:"type" json:"type"`
	Name     string             `bson:"name" json:"name"`
	Value    string             `bson:"value" json:"value"`
	TTL      int                `bson:"ttl" json:"ttl"`
	Priority *int               `bson:"priority,omitempty" json:"priority,omitempty"`
	Weight   *int               `bson:"weight,omitempty" json:"weight,omitempty"`
	Port     *int               `bson:"port,omitempty" json:"port,omitempty"`
	CAAFlag  *int               `bson:"caa_flag,omitempty" json:"caa_flag,omitempty"`
	CAATag   string             `bson:"caa_tag,omitempty" json:"caa_tag,omitempty"`
	CreatedAt time.Time         `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time         `bson:"updated_at" json:"updated_at"`
}

type CreateZoneRequest struct {
	Domain      string      `json:"domain" validate:"required"`
	ServerIP    string      `json:"server_ip" validate:"required"`
	AdminEmail  string      `json:"admin_email"`
	Nameservers []string    `json:"nameservers"`
	Template    string      `json:"template" validate:"omitempty,oneof=standard email wordpress blank"`
	Records     []DNSRecord `json:"records"`
}

type CreateRecordRequest struct {
	// Record type. The set below is the superset cPanel exposes in its
	// "Add Record" dropdown (A, AAAA, AFSDB, CAA, CNAME, DNAME, DS, HINFO,
	// HTTPS, LOC, MX, NS, NAPTR, PTR, RP, SRV, TXT) plus panel-specific
	// aliases ("ALIAS", "SPF", "DMARC" — the latter two are actually TXT
	// in the wire format but we accept the aliases for authoring
	// convenience). Validation stays enum-strict so an unknown type
	// surfaces as a 400 instead of silently corrupting the zone.
	Type     string `json:"type" validate:"required,oneof=A AAAA AFSDB ALIAS CAA CNAME DMARC DNAME DS HINFO HTTPS LOC MX NAPTR NS PTR RP SOA SPF SRV TXT"`
	Name     string `json:"name" validate:"required"`
	Value    string `json:"value" validate:"required"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority"`
	Weight   *int   `json:"weight"`
	Port     *int   `json:"port"`
	CAAFlag  *int   `json:"caa_flag"`
	CAATag   string `json:"caa_tag"`
}

// BulkRecordsRequest is the payload for POST /dns/zones/:domain/records/bulk.
// The backend processes entries serially, one PowerDNS call per record,
// so a partial failure (e.g. record #3 has an invalid value) leaves the
// already-committed earlier records in place and returns a per-item
// result the UI can surface inline. Mirrors the SSL bulk-issue shape.
type BulkRecordsRequest struct {
	Records []CreateRecordRequest `json:"records" validate:"required,min=1,dive"`
}

// BulkRecordResultItem is one row in the bulk response. Index matches
// the position in the input array so the UI can pair results with
// pending rows even when order changes client-side.
type BulkRecordResultItem struct {
	Index   int        `json:"index"`
	Success bool       `json:"success"`
	Error   string     `json:"error,omitempty"`
	Record  *DNSRecord `json:"record,omitempty"`
}

type BulkRecordsResponse struct {
	Total   int                    `json:"total"`
	Success int                    `json:"success"`
	Failed  int                    `json:"failed"`
	Items   []BulkRecordResultItem `json:"items"`
}

// BulkTTLRequest is the payload for POST /dns/bulk-ttl. The same shape
// is accepted on the WHM and cPanel surfaces; the only difference is the
// caller's CallerScope, which decides which zones are reachable
// (vendor_owner sees every zone on the box; tenant-scoped callers see
// only their own domains). Types is the multi-select list from the UI
// modal — one or more of A / AAAA / CNAME / MX / TXT / NS / etc. SOA is
// rejected at the validation layer because its TTL governs negative-cache
// behaviour and shouldn't be swept along with operator content.
type BulkTTLRequest struct {
	Types []string `json:"types" validate:"required,min=1,max=32,dive,min=1,max=10"`
	TTL   int      `json:"ttl" validate:"required,min=30,max=604800"`
}

// BulkTTLZoneResult is one row in the bulk-TTL response. Reported per
// zone so the UI can show which domains were touched (and which had
// nothing matching the picked types). Errors are scoped to a single
// zone — one bad zone doesn't fail the whole sweep.
type BulkTTLZoneResult struct {
	Domain         string `json:"domain"`
	UpdatedCount   int    `json:"updated_count"`   // # of Mongo records flipped
	RRSetsAffected int    `json:"rrsets_affected"` // # of distinct (name, type) pairs reconciled to PowerDNS
	Error          string `json:"error,omitempty"`
}

// BulkTTLResponse is the rolled-up result of a sweep. TotalRecordsUpdated
// is the sum of UpdatedCount across every zone; DomainsAffected counts
// only zones that had ≥1 matching record. DomainsConsidered is the size
// of the caller's reachable-zone list — useful when the sweep matched
// nothing so the operator can tell "your filter was empty" from "you
// have no zones".
type BulkTTLResponse struct {
	TotalRecordsUpdated int                 `json:"total_records_updated"`
	DomainsAffected     int                 `json:"domains_affected"`
	DomainsConsidered   int                 `json:"domains_considered"`
	Items               []BulkTTLZoneResult `json:"items"`
}
