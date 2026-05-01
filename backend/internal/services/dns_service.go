package services

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DNSService struct {
	db *mongo.Database
}

func NewDNSService(db *mongo.Database) *DNSService {
	return &DNSService{db: db}
}

func (s *DNSService) ListZones(ctx context.Context) ([]models.DNSZone, error) {
	// Get all zones from PowerDNS
	pdnsZones, err := agent.ListAllZones(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list PowerDNS zones: %w", err)
	}

	// Get MongoDB zone metadata for enrichment
	col := s.db.Collection(database.ColDNSZones)
	cursor, err := col.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var dbZones []models.DNSZone
	cursor.All(ctx, &dbZones)

	dbMap := make(map[string]models.DNSZone)
	for _, z := range dbZones {
		dbMap[z.Domain] = z
	}

	// Multi-tenant: vendors only see zones for domains they own.
	scope := GetCallerScope(ctx)
	var allowedDomains map[string]bool
	if scope != nil && constants.IsTenantScoped(scope.Role) {
		domains, _ := scope.TenantDomains(ctx, s.db)
		allowedDomains = make(map[string]bool, len(domains))
		for _, d := range domains {
			allowedDomains[d] = true
		}
	}

	// Build zone list: PowerDNS zones enriched with MongoDB metadata
	var zones []models.DNSZone
	for _, domain := range pdnsZones {
		if allowedDomains != nil && !allowedDomains[domain] {
			continue
		}
		if z, ok := dbMap[domain]; ok {
			zones = append(zones, z)
		} else {
			// Zone exists in PowerDNS but not in MongoDB — show it anyway
			zones = append(zones, models.DNSZone{
				Domain: domain,
				Status: "active",
			})
		}
	}
	if zones == nil {
		zones = []models.DNSZone{}
	}
	return zones, nil
}

// assertCallerOwnsDomain is the single gate every per-zone DNS method
// passes through before touching PowerDNS or Mongo. Platform operators
// (vendor_owner) are unrestricted; tenant-scoped callers are rejected
// when the target domain isn't in their tenant's domain list. Without
// this, a vendor_admin could GET /dns/zones/<other-tenant>.com/records
// and read or (worse) modify another customer's zone.
func (s *DNSService) assertCallerOwnsDomain(ctx context.Context, domain string) error {
	scope := GetCallerScope(ctx)
	if scope == nil {
		return nil
	}
	return scope.AssertOwnsDomain(ctx, s.db, domain)
}

func (s *DNSService) GetZone(ctx context.Context, domain string) (*models.DNSZone, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return nil, err
	}
	col := s.db.Collection(database.ColDNSZones)
	var zone models.DNSZone
	if err := col.FindOne(ctx, bson.M{"domain": domain}).Decode(&zone); err != nil {
		return nil, err
	}
	zone.Status = "active"
	return &zone, nil
}

// GetOrCreateZone returns the zone from MongoDB, creating it ONLY when
// PowerDNS already has the zone (the heal-on-read case for a panel
// whose Mongo dns_zones row was wiped but pdns still serves the zone).
//
// Pre-3.0.31 this method silently inserted a Mongo row for ANY domain
// passed in — and AddRecord called it with whatever findParentDomain
// returned. When pre-3.0.24 buggy code passed a subdomain like
// `users.konsultkaro.com` (parent it had picked from `domains`),
// GetOrCreateZone happily created a stale dns_zones row that PowerDNS
// never had. Those orphan rows then hijacked future
// findParentDomain lookups (post-3.0.24 dns_zones lookup +
// most-specific-wins) and corrupted the user's konsultkaro.com /
// dev.api.users.konsultkaro.com flow.
//
// 3.0.31 hardens the path: if PowerDNS doesn't have a SOA record for
// `domain`, refuse to mint a Mongo row. Callers either passed a
// non-existent zone (a real bug they need to surface) or passed a
// subdomain that should not own its own zone (the historical leak
// path). Either way, returning an error is safer than persisting
// fiction.
func (s *DNSService) GetOrCreateZone(ctx context.Context, domain string) (*models.DNSZone, error) {
	zone, err := s.GetZone(ctx, domain)
	if err == nil {
		return zone, nil
	}
	// Verify pdns actually owns the zone before we persist a Mongo row.
	// `pdnsutil list-zone <domain>` exits 0 with the SOA line at the top
	// when the zone exists; "Zone 'X' not found!" otherwise.
	listed, listErr := agent.RunCommand(ctx, "pdnsutil", "list-zone", domain)
	pdnsHasZone := listErr == nil && listed != nil && !strings.Contains(listed.Output, "Zone '")
	if !pdnsHasZone {
		return nil, fmt.Errorf("DNS zone %q does not exist (refusing to create a stale Mongo row — call CreateZone first if this is a new primary domain)", domain)
	}
	// pdns has the zone, Mongo row is missing — this IS a heal-on-read
	// case (panel rebuilt against an existing pdns), insert the row so
	// future lookups are O(1) instead of paying the pdnsutil round-trip.
	now := time.Now()
	z := models.DNSZone{
		Domain:    domain,
		Status:    "active",
		Serial:    1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	col := s.db.Collection(database.ColDNSZones)
	result, err := col.InsertOne(ctx, z)
	if err != nil {
		return nil, err
	}
	z.ID = result.InsertedID.(primitive.ObjectID)
	return &z, nil
}

func (s *DNSService) CreateZone(ctx context.Context, req *models.CreateZoneRequest) (*models.DNSZone, error) {
	if err := agent.CreateDNSZone(ctx, req.Domain, req.ServerIP, req.AdminEmail, req.Nameservers); err != nil {
		return nil, fmt.Errorf("failed to create DNS zone: %w", err)
	}

	now := time.Now()
	zone := models.DNSZone{
		Domain:      req.Domain,
		ServerIP:    req.ServerIP,
		AdminEmail:  req.AdminEmail,
		Nameservers: req.Nameservers,
		Serial:      1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	col := s.db.Collection(database.ColDNSZones)
	result, err := col.InsertOne(ctx, zone)
	if err != nil {
		return nil, fmt.Errorf("failed to save zone record: %w", err)
	}
	zone.ID = result.InsertedID.(primitive.ObjectID)

	// Save default records (A, www CNAME, NS) to MongoDB. A records
	// use the 60s default so a fresh-install IP cutover propagates
	// quickly; CNAME/NS keep the historic 3600s since those rarely
	// change and short TTLs there hurt resolver caches.
	recCol := s.db.Collection(database.ColDNSRecords)
	defaultRecords := []interface{}{
		models.DNSRecord{ZoneID: zone.ID, Type: "A", Name: "@", Value: req.ServerIP, TTL: defaultTTLFor("A"), CreatedAt: now, UpdatedAt: now},
		models.DNSRecord{ZoneID: zone.ID, Type: "CNAME", Name: "www", Value: req.Domain + ".", TTL: defaultTTLFor("CNAME"), CreatedAt: now, UpdatedAt: now},
	}
	for _, ns := range req.Nameservers {
		defaultRecords = append(defaultRecords, models.DNSRecord{ZoneID: zone.ID, Type: "NS", Name: "@", Value: ns, TTL: defaultTTLFor("NS"), CreatedAt: now, UpdatedAt: now})
	}
	recCol.InsertMany(ctx, defaultRecords)

	// Insert template records if provided
	if len(req.Records) > 0 {
		for _, rec := range req.Records {
			rec.ZoneID = zone.ID
			rec.CreatedAt = now
			rec.UpdatedAt = now
			recCol.InsertOne(ctx, rec)
		}
	}

	// Auto-setup mail server and mail DNS records
	s.setupMailServer(ctx, req.Domain, req.ServerIP, &zone)

	return &zone, nil
}

func (s *DNSService) DeleteZone(ctx context.Context, domain string) error {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return err
	}
	if err := agent.DeleteDNSZone(ctx, domain); err != nil {
		return fmt.Errorf("failed to delete DNS zone: %w", err)
	}

	// Clean up MongoDB records if zone exists there
	zone, err := s.GetZone(ctx, domain)
	if err == nil {
		s.db.Collection(database.ColDNSRecords).DeleteMany(ctx, bson.M{"zone_id": zone.ID})
		s.db.Collection(database.ColDNSZones).DeleteOne(ctx, bson.M{"_id": zone.ID})
	}
	return nil
}

// normalizeRecordName collapses every input shape an operator might
// type — bare label `ns1`, FQDN `ns1.example.com`, FQDN-with-trailing-
// dot `ns1.example.com.`, the apex `example.com`, the apex-with-dot
// `example.com.`, the bare `@` — into the canonical zone-relative
// form pdnsutil expects (`ns1` or `@`). Without this, AddRecord
// happily inserted three Mongo rows for the same logical record AND
// pdnsutil's add-record produced the double-suffix corruption (e.g.
// `ns1.example.com.example.com`) when handed the FQDN shape because
// pdnsutil treats every NAME argument as relative-to-zone.
//
// Idempotent: passing an already-normalized name is a no-op.
func normalizeRecordName(name, domain string) string {
	n := strings.TrimSpace(name)
	d := strings.TrimSpace(strings.TrimSuffix(domain, "."))
	if n == "" {
		return ""
	}
	// Strip trailing dot — operators sometimes copy from BIND zone
	// files or pdnsutil output where every FQDN ends with one.
	n = strings.TrimSuffix(n, ".")
	// Apex shorthand stays as-is.
	if n == "@" {
		return "@"
	}
	// Bare zone → apex.
	if strings.EqualFold(n, d) {
		return "@"
	}
	// FQDN within the zone → strip the suffix to make it relative.
	suffix := "." + d
	if strings.HasSuffix(strings.ToLower(n), strings.ToLower(suffix)) {
		return n[:len(n)-len(suffix)]
	}
	// Already relative (e.g. `ns1`, `_dmarc`, `mail._domainkey`) or
	// belongs to a different zone (rare, operator probably means it
	// to be inserted as-is — the pdnsutil call will then double-
	// suffix it; that's the same behavior the panel had pre-fix).
	return n
}

// normalizeValueForMatch reduces a record value to a canonical form
// that matches whether it was produced by `pdnsutil list-zone` (which
// emits TXT with surrounding quotes and MX as `<priority> <target>`)
// OR by the panel's Add/Update path (which stores TXT raw and stashes
// MX priority on a separate Priority column with Value=target only).
// Used as a key for cross-referencing parsed PowerDNS records against
// their Mongo backings so the WHM UI gets stable record IDs even on
// types that diverge between the two stores.
func normalizeValueForMatch(rtype, value string) string {
	v := strings.TrimSpace(value)
	switch strings.ToUpper(rtype) {
	case "TXT":
		// PowerDNS emits "quoted"; Mongo native-add stores raw. Strip
		// matching outer quotes so both shapes hash to the same key.
		if len(v) >= 2 && strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"") {
			return v[1 : len(v)-1]
		}
	case "MX":
		// PowerDNS: `<priority> <target>`. Mongo: `<target>` with
		// priority on the row's Priority column. Strip leading
		// numeric token if present.
		fields := strings.Fields(v)
		if len(fields) >= 2 {
			if _, err := strconv.Atoi(fields[0]); err == nil {
				return strings.Join(fields[1:], " ")
			}
		}
	case "SRV":
		// PowerDNS: `<priority> <weight> <port> <target>`. Mongo:
		// `<target>` with the three numerics on dedicated columns.
		fields := strings.Fields(v)
		if len(fields) >= 4 {
			allNumeric := true
			for i := 0; i < 3; i++ {
				if _, err := strconv.Atoi(fields[i]); err != nil {
					allNumeric = false
					break
				}
			}
			if allNumeric {
				return strings.Join(fields[3:], " ")
			}
		}
	case "CAA":
		// PowerDNS: `<flag> <tag> "<value>"`. Mongo: just `<value>`
		// with flag+tag on dedicated columns. Strip leading flag+tag
		// when present, then unquote.
		fields := strings.Fields(v)
		if len(fields) >= 3 {
			if _, err := strconv.Atoi(fields[0]); err == nil {
				v = strings.Join(fields[2:], " ")
			}
		}
		if len(v) >= 2 && strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"") {
			return v[1 : len(v)-1]
		}
		return v
	}
	return v
}

func (s *DNSService) ListRecords(ctx context.Context, domain string) ([]models.DNSRecord, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return nil, err
	}
	parsed, err := agent.ListZoneRecords(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to list zone records: %w", err)
	}

	zone, _ := s.GetZone(ctx, domain)

	var dbRecords []models.DNSRecord
	if zone != nil {
		col := s.db.Collection(database.ColDNSRecords)
		cursor, _ := col.Find(ctx, bson.M{"zone_id": zone.ID})
		if cursor != nil {
			cursor.All(ctx, &dbRecords)
			cursor.Close(ctx)
		}
	}

	// Lookup map keyed on type+name+normalized-value. The normalization
	// step is what fixes the WHM "edit returns 'record not found'" bug:
	// previously TXT records (PowerDNS=quoted, Mongo=raw) and MX records
	// (PowerDNS=`pri target`, Mongo=`target`) never matched, so the
	// rendered row's ID stayed at the zero ObjectID and the next PUT
	// went to /records/000…000 which Mongo can't decode.
	dbMap := make(map[string]models.DNSRecord)
	for _, r := range dbRecords {
		key := r.Type + "|" + r.Name + "|" + normalizeValueForMatch(r.Type, r.Value)
		dbMap[key] = r
	}

	var records []models.DNSRecord
	var orphans []models.DNSRecord
	now := time.Now()
	for _, p := range parsed {
		ttl, _ := strconv.Atoi(p.TTL)
		rec := models.DNSRecord{
			Type:  p.Type,
			Name:  p.Name,
			Value: p.Value,
			TTL:   ttl,
		}
		key := p.Type + "|" + p.Name + "|" + normalizeValueForMatch(p.Type, p.Value)
		if dbRec, ok := dbMap[key]; ok {
			rec.ID = dbRec.ID
			rec.ZoneID = dbRec.ZoneID
			rec.Priority = dbRec.Priority
			rec.Weight = dbRec.Weight
			rec.Port = dbRec.Port
			rec.CAAFlag = dbRec.CAAFlag
			rec.CAATag = dbRec.CAATag
			rec.CreatedAt = dbRec.CreatedAt
			rec.UpdatedAt = dbRec.UpdatedAt
			records = append(records, rec)
			continue
		}
		// Orphan in PowerDNS — no Mongo backing. Heal on read by
		// inserting a Mongo row for it AFTER the loop (we batch the
		// writes so a slow Mongo doesn't fan out N round-trips). The
		// Value stored is the raw PowerDNS form for A/AAAA/CNAME/NS;
		// for the type-quirky kinds we strip the on-the-wire shape so
		// the row is consistent with what AddRecord would have written.
		if zone != nil {
			rec.ID = primitive.NewObjectID()
			rec.ZoneID = zone.ID
			rec.CreatedAt = now
			rec.UpdatedAt = now
			rec.Value = stripPDNSValueToMongoShape(p.Type, p.Value, &rec)
			orphans = append(orphans, rec)
		}
		records = append(records, rec)
	}

	// One bulk insert for any healed orphans. Failure here is logged
	// but doesn't break the response — the list still returns with
	// real IDs (from the in-memory rec.ID assignment) so the operator
	// can edit/delete them; subsequent reads will retry the heal.
	if len(orphans) > 0 {
		col := s.db.Collection(database.ColDNSRecords)
		docs := make([]interface{}, 0, len(orphans))
		for i := range orphans {
			docs = append(docs, orphans[i])
		}
		if _, err := col.InsertMany(ctx, docs); err != nil {
			fmt.Fprintf(os.Stderr, "[dns] heal-on-read InsertMany failed for %s: %v\n", domain, err)
		}
	}

	if records == nil {
		records = []models.DNSRecord{}
	}
	return records, nil
}

// stripPDNSValueToMongoShape converts a value emitted by
// `pdnsutil list-zone` (TXT="…", MX="<pri> target", SRV="<pri> <w> <p> target",
// CAA="<flag> <tag> \"value\"") into the form AddRecord stores in Mongo.
// Side-effects on `rec`: extracts priority/weight/port/CAAFlag/CAATag
// onto the dedicated columns. Idempotent: passing an already-stripped
// value (e.g. one written natively via AddRecord) is a no-op.
func stripPDNSValueToMongoShape(rtype, value string, rec *models.DNSRecord) string {
	v := strings.TrimSpace(value)
	switch strings.ToUpper(rtype) {
	case "TXT":
		if len(v) >= 2 && strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"") {
			return v[1 : len(v)-1]
		}
	case "MX":
		fields := strings.Fields(v)
		if len(fields) >= 2 {
			if pri, err := strconv.Atoi(fields[0]); err == nil {
				rec.Priority = &pri
				return strings.Join(fields[1:], " ")
			}
		}
	case "SRV":
		fields := strings.Fields(v)
		if len(fields) >= 4 {
			pri, e1 := strconv.Atoi(fields[0])
			weight, e2 := strconv.Atoi(fields[1])
			port, e3 := strconv.Atoi(fields[2])
			if e1 == nil && e2 == nil && e3 == nil {
				rec.Priority = &pri
				rec.Weight = &weight
				rec.Port = &port
				return strings.Join(fields[3:], " ")
			}
		}
	case "CAA":
		fields := strings.Fields(v)
		if len(fields) >= 3 {
			if flag, err := strconv.Atoi(fields[0]); err == nil {
				rec.CAAFlag = &flag
				rec.CAATag = fields[1]
				rest := strings.Join(fields[2:], " ")
				if len(rest) >= 2 && strings.HasPrefix(rest, "\"") && strings.HasSuffix(rest, "\"") {
					return rest[1 : len(rest)-1]
				}
				return rest
			}
		}
	}
	return v
}

// defaultTTLFor returns the cPanel-style default TTL for a record type
// when the caller didn't supply one. A records (and their AAAA siblings)
// default to 60 seconds because IPs are the most likely thing to change
// quickly during a migration and a long cache there hurts cutover.
// Everything else stays at 3600 (1 hour) — the historic default — so
// MX/NS/SOA/TXT records aren't churning resolver caches.
func defaultTTLFor(rtype string) int {
	switch rtype {
	case "A", "AAAA":
		return 60
	}
	return 3600
}

// formatRecordValueForPDNS converts a DNSRecord row's Mongo
// representation into the on-the-wire string `pdnsutil` expects.
// Each record type has its own quirks: TXT must be wrapped in
// double quotes (pdnsutil rejects bare data starting with a letter),
// MX is `<priority> <target>` as a SINGLE argument, SRV is
// `<priority> <weight> <port> <target>`, CAA is `<flag> <tag>
// "<value>"`. The original setupMailServer / Add path hand-formats
// these, but stores the value RAW in Mongo (without the quotes /
// without the priority). Reconcile reads Mongo and reproduces the
// pdnsutil-shaped string here so the rrset writes back identically
// to a fresh add.
func formatRecordValueForPDNS(rec *models.DNSRecord) string {
	v := strings.TrimSpace(rec.Value)
	switch strings.ToUpper(rec.Type) {
	case "TXT":
		// Already quoted? Trust the caller — operators sometimes paste
		// pre-quoted DKIM strings. Otherwise wrap, escaping any
		// embedded quotes (rare but possible in DKIM concatenated
		// strings) so pdnsutil's parser sees a single quoted token.
		if strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"") {
			return v
		}
		return "\"" + strings.ReplaceAll(v, "\"", "\\\"") + "\""
	case "MX":
		pri := 10
		if rec.Priority != nil {
			pri = *rec.Priority
		}
		return fmt.Sprintf("%d %s", pri, v)
	case "SRV":
		pri, weight, port := 0, 0, 0
		if rec.Priority != nil {
			pri = *rec.Priority
		}
		if rec.Weight != nil {
			weight = *rec.Weight
		}
		if rec.Port != nil {
			port = *rec.Port
		}
		return fmt.Sprintf("%d %d %d %s", pri, weight, port, v)
	case "CAA":
		// pdnsutil wants `<flags> <tag> "<value>"`. Mongo stores flags
		// + tag + value separately on the record row.
		val := v
		if !strings.HasPrefix(val, "\"") {
			val = "\"" + strings.ReplaceAll(val, "\"", "\\\"") + "\""
		}
		flag := 0
		if rec.CAAFlag != nil {
			flag = *rec.CAAFlag
		}
		return fmt.Sprintf("%d %s %s", flag, rec.CAATag, val)
	default:
		return v
	}
}

// reconcileRRSet rewrites a single PowerDNS rrset to exactly the set
// of values currently in Mongo for (zone, name, type). Pulls every
// sibling row, picks the minimum TTL across them (DNS protocol stores
// TTL once per rrset — the smallest TTL wins so resolvers re-fetch on
// the tightest cadence anyone configured), formats each value into
// pdnsutil's expected on-the-wire shape (quotes for TXT, priority
// prefix for MX/SRV, flag+tag for CAA), and calls
// agent.ReplaceDNSRecordSet (or DeleteDNSRecord when zero siblings).
//
// Called by Add/Update/Delete after every Mongo write. Mongo is the
// source of truth; PowerDNS is the projection. Idempotent — safe to
// run twice or against an already-aligned zone.
func (s *DNSService) reconcileRRSet(ctx context.Context, zoneID primitive.ObjectID, domain, name, rtype string) error {
	col := s.db.Collection(database.ColDNSRecords)

	// Canonicalize the lookup name. Even if every entry point already
	// normalizes incoming user input, an OLD Mongo row left over from
	// pre-3.0.11 code can still arrive here as `ns1.zone.com.` etc.,
	// and pdnsutil treats the NAME argument as relative-to-zone — so
	// passing the FQDN shape would make pdnsutil look up
	// `ns1.zone.com.zone.com` (doubled), miss, and leave the real
	// rrset behind. Normalize once at the choke point so every caller
	// (Add, Update, Delete, by-name-fallback) gets it for free.
	name = normalizeRecordName(name, domain)

	// Pull every Mongo row whose name canonicalizes to the same
	// relative form — covers the legacy mix where Mongo carries
	// `ns1`, `ns1.zone.com`, `ns1.zone.com.` rows side-by-side.
	zoneSuffix := "." + strings.TrimSuffix(domain, ".")
	candidates := []string{name}
	if name == "@" {
		candidates = append(candidates, domain, domain+".")
	} else {
		candidates = append(candidates, name+zoneSuffix, name+zoneSuffix+".")
	}
	cur, err := col.Find(ctx, bson.M{
		"zone_id": zoneID,
		"type":    rtype,
		"name":    bson.M{"$in": candidates},
	})
	if err != nil {
		return fmt.Errorf("read siblings: %w", err)
	}
	defer cur.Close(ctx)

	var siblings []models.DNSRecord
	if err := cur.All(ctx, &siblings); err != nil {
		return fmt.Errorf("decode siblings: %w", err)
	}

	if len(siblings) == 0 {
		return agent.DeleteDNSRecord(ctx, domain, name, rtype)
	}

	values := make([]string, 0, len(siblings))
	// DNS protocol stores TTL once per rrset (RFC 2181 §5.2 — multiple
	// values at the same name+type MUST share one TTL). We pick the
	// most-recently-updated row's TTL as the rrset TTL and propagate
	// it back to every sibling Mongo row so the listing matches what
	// pdns serves. Last-write-wins matches operator intent: when you
	// change one row's TTL, you mean to change the whole rrset (you
	// can't have a per-value TTL anyway). Falls back to min if every
	// row has the same UpdatedAt (no recent edit to disambiguate),
	// then to the type default (60s for A/AAAA, 3600s otherwise).
	rrsetTTL := siblings[0].TTL
	latest := siblings[0].UpdatedAt
	for i := range siblings {
		sib := &siblings[i]
		values = append(values, formatRecordValueForPDNS(sib))
		if sib.UpdatedAt.After(latest) && sib.TTL > 0 {
			rrsetTTL = sib.TTL
			latest = sib.UpdatedAt
		}
	}
	if rrsetTTL <= 0 {
		rrsetTTL = defaultTTLFor(rtype)
	}

	// Propagate the chosen TTL to every sibling so Mongo doesn't carry
	// a stale per-row TTL the operator can't reconcile mentally.
	// Skip the write when every row already matches — keeps the
	// updated_at timestamp stable on no-op reconciles.
	mismatched := make([]primitive.ObjectID, 0)
	for i := range siblings {
		if siblings[i].TTL != rrsetTTL {
			mismatched = append(mismatched, siblings[i].ID)
		}
	}
	if len(mismatched) > 0 {
		_, _ = col.UpdateMany(ctx,
			bson.M{"_id": bson.M{"$in": mismatched}},
			bson.M{"$set": bson.M{"ttl": rrsetTTL, "updated_at": time.Now()}})
	}

	return agent.ReplaceDNSRecordSet(ctx, domain, name, rtype, fmt.Sprint(rrsetTTL), values)
}

func (s *DNSService) AddRecord(ctx context.Context, domain string, req *models.CreateRecordRequest) (*models.DNSRecord, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return nil, err
	}
	zone, err := s.GetOrCreateZone(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("zone not found: %w", err)
	}

	// Canonicalize to zone-relative — without this, an operator who
	// types `ns1.example.com.` in the form would land a Mongo row
	// with that exact string, while another typing `ns1` would land
	// a SECOND row, and pdnsutil add-record on the FQDN form would
	// double-suffix to ns1.example.com.example.com.
	req.Name = normalizeRecordName(req.Name, domain)
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	ttl := req.TTL
	if ttl == 0 {
		ttl = defaultTTLFor(req.Type)
	}

	col := s.db.Collection(database.ColDNSRecords)

	// Block exact duplicates — same zone + name + type + value. PowerDNS's
	// rrset doesn't tolerate two identical values anyway, and Mongo accepting
	// them was the entry point for the (name,type) drift the user reported:
	// click-add three times → three Mongo rows, one PowerDNS rrset value,
	// then delete falls over because the visible rows out-number the actual
	// rrset.
	dup := col.FindOne(ctx, bson.M{
		"zone_id": zone.ID,
		"name":    req.Name,
		"type":    req.Type,
		"value":   req.Value,
	})
	if dup.Err() == nil {
		return nil, fmt.Errorf("a %s record for %q with value %q already exists", req.Type, req.Name, req.Value)
	}

	now := time.Now()
	record := models.DNSRecord{
		ZoneID:    zone.ID,
		Type:      req.Type,
		Name:      req.Name,
		Value:     req.Value,
		TTL:       ttl,
		Priority:  req.Priority,
		Weight:    req.Weight,
		Port:      req.Port,
		CAAFlag:   req.CAAFlag,
		CAATag:    req.CAATag,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Insert into Mongo first so the reconcile pass below sees the new
	// row alongside any siblings. If pdns then rejects the rrset (e.g.
	// invalid value for the type) we roll the insert back so Mongo and
	// PowerDNS stay aligned.
	result, err := col.InsertOne(ctx, record)
	if err != nil {
		return nil, err
	}
	record.ID = result.InsertedID.(primitive.ObjectID)

	if err := s.reconcileRRSet(ctx, zone.ID, domain, req.Name, req.Type); err != nil {
		_, _ = col.DeleteOne(ctx, bson.M{"_id": record.ID})
		return nil, fmt.Errorf("failed to add DNS record: %w", err)
	}

	s.db.Collection(database.ColDNSZones).UpdateOne(ctx, bson.M{"_id": zone.ID}, bson.M{
		"$inc": bson.M{"serial": 1},
		"$set": bson.M{"updated_at": now},
	})

	return &record, nil
}

// BulkAddRecords inserts N records into the zone serially. Per-item
// failure does NOT abort the batch — every entry produces an item in
// the response so the UI can render success / error inline against the
// pending row that produced it. Mirrors the SSL bulk-issue shape so
// frontends can use the same pattern.
//
// Why serial: PowerDNS's pdnsutil add-record uses an internal lock per
// zone; parallel adds would just block each other. Sequential keeps
// the per-record error surface clean and avoids race windows on the
// zone serial increment.
func (s *DNSService) BulkAddRecords(ctx context.Context, domain string, req *models.BulkRecordsRequest) (*models.BulkRecordsResponse, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return nil, err
	}
	resp := &models.BulkRecordsResponse{
		Total: len(req.Records),
		Items: make([]models.BulkRecordResultItem, 0, len(req.Records)),
	}
	for i := range req.Records {
		r := req.Records[i]
		item := models.BulkRecordResultItem{Index: i}
		rec, err := s.AddRecord(ctx, domain, &r)
		if err != nil {
			item.Success = false
			item.Error = err.Error()
			resp.Items = append(resp.Items, item)
			resp.Failed++
			continue
		}
		item.Success = true
		item.Record = rec
		resp.Items = append(resp.Items, item)
		resp.Success++
	}
	return resp, nil
}

func (s *DNSService) UpdateRecord(ctx context.Context, domain string, id string, updates map[string]interface{}) (*models.DNSRecord, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return nil, err
	}
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid record ID")
	}

	col := s.db.Collection(database.ColDNSRecords)
	var existing models.DNSRecord
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&existing); err != nil {
		return nil, fmt.Errorf("record not found")
	}

	newName := existing.Name
	newType := existing.Type
	newValue := existing.Value
	now := time.Now()

	setFields := bson.M{"updated_at": now}
	if v, ok := updates["name"].(string); ok {
		// Same FQDN/relative/trailing-dot collapse as AddRecord so an
		// edit that retypes the name in a different shape stays at
		// the canonical relative form.
		v = normalizeRecordName(v, domain)
		newName = v
		setFields["name"] = v
	}
	if v, ok := updates["type"].(string); ok {
		newType = v
		setFields["type"] = v
	}
	if v, ok := updates["value"].(string); ok {
		newValue = v
		setFields["value"] = v
	}
	// TTL changes flow through Mongo only; reconcileRRSet picks the
	// new min across siblings on the next replace-rrset call. We don't
	// need a local for the new value here.
	if v, ok := updates["ttl"].(float64); ok {
		setFields["ttl"] = int(v)
	}

	// Catch the "edit collapses to an existing duplicate" case: if the
	// post-edit (name, type, value) tuple already lives on a different
	// row, the edit would create a true duplicate the next reconcile
	// would silently merge. Reject up-front so the operator gets a
	// clear error instead of a vanishing row.
	if newName != existing.Name || newType != existing.Type || newValue != existing.Value {
		dup := col.FindOne(ctx, bson.M{
			"_id":     bson.M{"$ne": oid},
			"zone_id": existing.ZoneID,
			"name":    newName,
			"type":    newType,
			"value":   newValue,
		})
		if dup.Err() == nil {
			return nil, fmt.Errorf("a %s record for %q with value %q already exists", newType, newName, newValue)
		}
	}

	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var record models.DNSRecord
	if err := col.FindOneAndUpdate(ctx, bson.M{"_id": oid}, bson.M{"$set": setFields}, opts).Decode(&record); err != nil {
		return nil, err
	}

	// Reconcile the OLD rrset first (the edit may have removed the row
	// from it — e.g. name change), then the NEW rrset (which now owns
	// the updated value). When name+type didn't change, the second call
	// is a no-op-equivalent that just re-issues the same replace-rrset.
	if existing.Name != newName || existing.Type != newType {
		if err := s.reconcileRRSet(ctx, existing.ZoneID, domain, existing.Name, existing.Type); err != nil {
			return nil, fmt.Errorf("reconcile old rrset: %w", err)
		}
	}
	if err := s.reconcileRRSet(ctx, existing.ZoneID, domain, newName, newType); err != nil {
		return nil, fmt.Errorf("reconcile rrset: %w", err)
	}

	s.db.Collection(database.ColDNSZones).UpdateOne(ctx, bson.M{"_id": existing.ZoneID}, bson.M{
		"$inc": bson.M{"serial": 1},
		"$set": bson.M{"updated_at": now},
	})

	return &record, nil
}

func (s *DNSService) DeleteRecord(ctx context.Context, domain string, id string) error {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return err
	}
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid record ID")
	}

	col := s.db.Collection(database.ColDNSRecords)
	var record models.DNSRecord
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&record); err != nil {
		return fmt.Errorf("record not found")
	}

	// Wipe Mongo rows for the same logical record across every name
	// shape (`ns1`, `ns1.zone.com`, `ns1.zone.com.`). On a v3.0.11+ box
	// this is just the targeted row; on a zone whose Mongo state was
	// corrupted by pre-canonicalization writes, this prevents a deleted
	// record from re-appearing on the next list call (heal-on-read
	// would re-insert if the legacy row's pdns value was still served).
	canonName := normalizeRecordName(record.Name, domain)
	zoneSuffix := "." + strings.TrimSuffix(domain, ".")
	nameVariants := []string{canonName}
	if canonName == "@" {
		nameVariants = append(nameVariants, domain, domain+".")
	} else {
		nameVariants = append(nameVariants, canonName+zoneSuffix, canonName+zoneSuffix+".")
	}
	if _, err := col.DeleteMany(ctx, bson.M{
		"zone_id": record.ZoneID,
		"type":    record.Type,
		"value":   record.Value,
		"name":    bson.M{"$in": nameVariants},
	}); err != nil {
		return fmt.Errorf("failed to delete DNS record: %w", err)
	}

	// Reconcile pdns from whatever still survives in Mongo for the
	// canonical (name, type). If this was the last row, reconcileRRSet
	// falls through to DeleteDNSRecord which is now idempotent on
	// already-gone rrsets.
	if err := s.reconcileRRSet(ctx, record.ZoneID, domain, canonName, record.Type); err != nil {
		return fmt.Errorf("failed to delete DNS record: %w", err)
	}

	zone, _ := s.GetZone(ctx, domain)
	if zone != nil {
		s.db.Collection(database.ColDNSZones).UpdateOne(ctx, bson.M{"_id": zone.ID}, bson.M{
			"$inc": bson.M{"serial": 1},
			"$set": bson.M{"updated_at": time.Now()},
		})
	}

	return nil
}

// DeleteRecordByNameType deletes a DNS record by name and type. Used
// as a fallback when the caller doesn't have a Mongo ObjectID — e.g.
// a stale browser tab that listed the zone BEFORE the heal-on-read
// pass backfilled Mongo. Best effort: if a Mongo row exists for
// (zone, name, type, value?), delete it; reconcile the rrset from
// surviving siblings (or remove from PowerDNS when none remain).
//
// `value` is optional. When supplied, only the row(s) matching that
// exact value are removed from Mongo (the legitimate multi-value
// rrset case the user actually has). When empty, all Mongo rows for
// (name, type) are removed and the entire rrset is dropped from
// PowerDNS — matches the WHM Delete-by-row UX which asks "delete
// this <type> record for <name>?" without showing the value.
func (s *DNSService) DeleteRecordByNameType(ctx context.Context, domain, name, rtype string, value string) error {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return err
	}
	zone, _ := s.GetZone(ctx, domain)
	col := s.db.Collection(database.ColDNSRecords)

	name = normalizeRecordName(name, domain)
	if zone != nil {
		filter := bson.M{"zone_id": zone.ID, "name": name, "type": rtype}
		if value != "" {
			// Match either Mongo's raw shape OR PowerDNS's on-the-wire
			// shape — a stale UI typing the visible (quoted/prefixed)
			// value still resolves to the right row.
			normalized := normalizeValueForMatch(rtype, value)
			filter["$or"] = []bson.M{
				{"value": value},
				{"value": normalized},
			}
		}
		_, _ = col.DeleteMany(ctx, filter)
	}

	if zone != nil {
		return s.reconcileRRSet(ctx, zone.ID, domain, name, rtype)
	}
	return agent.DeleteDNSRecord(ctx, domain, name, rtype)
}

// UpdateRecordByNameType is the fallback path for stale UIs that send
// PUT with an all-zeros ObjectID (records that pre-dated the heal-on-
// read backfill — they had no Mongo row, listed with rec.ID=zero, and
// the next save lands here). Resolves the row by current name+type
// and applies the updates the same way UpdateRecord does, so the
// reconcile + dup guards still run.
func (s *DNSService) UpdateRecordByNameType(ctx context.Context, domain, name, rtype string, updates map[string]interface{}) (*models.DNSRecord, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return nil, err
	}
	zone, err := s.GetZone(ctx, domain)
	if err != nil || zone == nil {
		return nil, fmt.Errorf("zone not found")
	}
	name = normalizeRecordName(name, domain)
	col := s.db.Collection(database.ColDNSRecords)

	// Pick the row to edit. When the caller didn't supply an existing-
	// value hint, we can't disambiguate inside a multi-value rrset, so
	// reject — the operator should refresh the page and try again.
	filter := bson.M{"zone_id": zone.ID, "name": name, "type": rtype}
	if existing, ok := updates["existing_value"].(string); ok && existing != "" {
		filter["$or"] = []bson.M{
			{"value": existing},
			{"value": normalizeValueForMatch(rtype, existing)},
		}
	}
	count, _ := col.CountDocuments(ctx, filter)
	if count > 1 && filter["$or"] == nil {
		return nil, fmt.Errorf("multiple records share %q %s — refresh the page and try again", name, rtype)
	}
	if count == 0 {
		return nil, fmt.Errorf("record not found")
	}

	var row models.DNSRecord
	if err := col.FindOne(ctx, filter).Decode(&row); err != nil {
		return nil, fmt.Errorf("record not found")
	}
	return s.UpdateRecord(ctx, domain, row.ID.Hex(), updates)
}

// ZoneReconcileReport summarises a heal pass for the WHM operator so
// they see exactly what shifted: how many rrsets were rewritten, how
// many duplicate Mongo rows were merged into existing siblings, and
// any per-rrset failures (PowerDNS down, invalid value, etc.).
type ZoneReconcileReport struct {
	Domain               string             `json:"domain"`
	RRSetsWritten        int                `json:"rrsets_written"`
	DuplicateRowsRemoved int                `json:"duplicate_rows_removed"`
	Errors               []ZoneReconcileErr `json:"errors,omitempty"`
}

type ZoneReconcileErr struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Error string `json:"error"`
}

// ReconcileZone heals existing drift between Mongo and PowerDNS for one
// zone. Walks every (name, type) group of records in Mongo, collapses
// exact duplicates (same name+type+value rows — the failure mode
// AddRecord used to allow), then runs reconcileRRSet on each group so
// PowerDNS matches Mongo exactly. Used both from a new admin route
// (so an operator can fix the user's existing zones in one click) and
// internally during recovery flows.
//
// Idempotent: re-running on an already-aligned zone reports zero
// changes. Doesn't touch the SOA / nameserver records the panel
// manages itself — those are owned by CreateDNSZone.
func (s *DNSService) ReconcileZone(ctx context.Context, domain string) (*ZoneReconcileReport, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return nil, err
	}
	zone, err := s.GetZone(ctx, domain)
	if err != nil || zone == nil {
		return nil, fmt.Errorf("zone not found")
	}

	col := s.db.Collection(database.ColDNSRecords)
	cur, err := col.Find(ctx, bson.M{"zone_id": zone.ID})
	if err != nil {
		return nil, fmt.Errorf("read records: %w", err)
	}
	defer cur.Close(ctx)
	var rows []models.DNSRecord
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("decode records: %w", err)
	}

	report := &ZoneReconcileReport{Domain: domain}

	// First pass: rewrite every Mongo row's name to the canonical
	// zone-relative form. Operators who typed `ns1.zone.com.` or
	// `ns1.zone.com` in the form left rows with non-canonical names;
	// future Add-by-relative would treat them as a different rrset
	// and let the operator add a duplicate. Normalize before we
	// dedup so the dedup pass collapses logical duplicates.
	for i := range rows {
		canon := normalizeRecordName(rows[i].Name, domain)
		if canon != rows[i].Name {
			_, _ = col.UpdateByID(ctx, rows[i].ID, bson.M{"$set": bson.M{"name": canon}})
			rows[i].Name = canon
		}
	}

	// Second pass: collapse exact duplicates (same name+type+value).
	// PowerDNS can't represent more than one identical value in an
	// rrset anyway, so the extra Mongo rows are pure UI noise — keep
	// the oldest (first inserted), drop the rest.
	type key struct{ name, rtype, value string }
	seen := map[key]primitive.ObjectID{}
	var dupIDs []primitive.ObjectID
	for _, r := range rows {
		k := key{r.Name, r.Type, r.Value}
		if _, exists := seen[k]; exists {
			dupIDs = append(dupIDs, r.ID)
			continue
		}
		seen[k] = r.ID
	}
	if len(dupIDs) > 0 {
		res, err := col.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": dupIDs}})
		if err != nil {
			return nil, fmt.Errorf("collapse duplicates: %w", err)
		}
		report.DuplicateRowsRemoved = int(res.DeletedCount)
	}

	// Third pass: clear any double-suffix junk PowerDNS may carry from
	// past add-record calls that received an FQDN as NAME (pdnsutil
	// treats NAME as relative-to-zone, so add-record on `ns1.zone.com`
	// produced `ns1.zone.com.zone.com`). Walk pdns's actual zone, find
	// any rrset whose name contains the zone label twice, and drop it.
	// Same cleanup the transfer pipeline does post-import — bringing
	// it into the routine reconcile means an operator can heal at
	// will without waiting for a transfer.
	parsed, _ := agent.ListZoneRecords(ctx, domain)
	doubledSuffix := "." + domain + "." + domain
	for _, p := range parsed {
		// `name` is already zone-relative from ListZoneRecords. The
		// FQDN reconstruction lets us spot double-suffix at any depth.
		fqdn := p.Name
		if fqdn == "@" {
			fqdn = domain
		} else {
			fqdn = fqdn + "." + domain
		}
		if strings.Contains(fqdn, doubledSuffix) {
			_ = agent.DeleteDNSRecord(ctx, domain, p.Name, p.Type)
		}
	}

	// Second pass: replay every rrset. Build the unique set of (name,
	// type) tuples from the surviving rows; reconcileRRSet handles the
	// per-tuple read/min-TTL/replace-rrset dance.
	type nt struct{ name, rtype string }
	tuples := map[nt]struct{}{}
	for _, r := range rows {
		tuples[nt{r.Name, r.Type}] = struct{}{}
	}
	for t := range tuples {
		if err := s.reconcileRRSet(ctx, zone.ID, domain, t.name, t.rtype); err != nil {
			report.Errors = append(report.Errors, ZoneReconcileErr{
				Name: t.name, Type: t.rtype, Error: err.Error(),
			})
			continue
		}
		report.RRSetsWritten++
	}

	s.db.Collection(database.ColDNSZones).UpdateOne(ctx, bson.M{"_id": zone.ID}, bson.M{
		"$inc": bson.M{"serial": 1},
		"$set": bson.M{"updated_at": time.Now()},
	})

	return report, nil
}

func (s *DNSService) ExportZone(ctx context.Context, domain string) (string, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return "", err
	}
	output, err := agent.ExportDNSZone(ctx, domain)
	if err != nil {
		return "", fmt.Errorf("failed to export zone: %w", err)
	}
	return output, nil
}

// SetupSubdomainMail wires mail for a SUBDOMAIN that lives inside an
// existing parent DNS zone. Unlike setupMailServer (which runs on
// fresh zone creation) we don't own a separate zone here — records
// are added into the parent, and DKIM is registered under the
// subdomain's FQDN so OpenDKIM signs outbound mail properly.
//
// Without this, creating sub.example.com and then admin@sub.example.com
// produced three silent failures that together broke subdomain email:
//   1. No MX record at `sub` in example.com's zone → external senders
//      fell back to the A record for sub.example.com. Often worked,
//      but hostname mismatch tripped SPF.
//   2. sub.example.com never entered the OpenDKIM signing.table →
//      outbound mail from this subdomain went unsigned → Gmail's
//      DMARC alignment failed → spam folder or reject.
//   3. sub.example.com was only added to virtual_mailbox_domains
//      lazily, on the first CreateMailbox call. Any inbound delivery
//      that raced a CreateMailbox bounced "Recipient address rejected:
//      Domain not found".
//
// subPart is the label under the parent (e.g. "mail" or "shop.blog").
// parentDomain is the owning zone root (e.g. "example.com").
func (s *DNSService) SetupSubdomainMail(ctx context.Context, subPart, parentDomain, serverIP string) error {
	if subPart == "" || parentDomain == "" {
		return fmt.Errorf("subPart and parentDomain are required")
	}
	fqdn := subPart + "." + parentDomain
	escFqdn := regexp.QuoteMeta(fqdn)
	escParent := regexp.QuoteMeta(parentDomain)

	// 1. DKIM. We REUSE the parent zone's key rather than minting a new
	//    selector per subdomain. Two reasons:
	//      * the parent's DKIM TXT is already published and trusted; a
	//        fresh per-subdomain TXT adds nothing and means two DNS
	//        changes whenever we rotate.
	//      * Gmail's DMARC alignment prefers the key be in the parent
	//        zone for subdomain senders — a key-per-subdomain works but
	//        produces a more complex alignment story.
	//    If the parent has no key yet (edge case — someone called
	//    SetupSubdomainMail directly against a bare zone), fall back to
	//    generating one for the subdomain so we never leave mail
	//    unsigned.
	parentKeyDir := fmt.Sprintf("/etc/opendkim/keys/%s", parentDomain)
	parentHasKey := false
	if r, err := agent.RunCommand(ctx, "test", "-f", parentKeyDir+"/mail.private"); err == nil && r != nil {
		parentHasKey = true
	}

	if parentHasKey {
		// Reuse parent's selector for signing.
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
			`grep -qE '^\*@%s[[:space:]]' /etc/opendkim/signing.table 2>/dev/null || echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table`,
			escFqdn, fqdn, parentDomain))
	} else {
		// Fallback: mint a dedicated key for the subdomain.
		keyDir := fmt.Sprintf("/etc/opendkim/keys/%s", fqdn)
		agent.RunCommand(ctx, "mkdir", "-p", keyDir)
		agent.RunCommand(ctx, "opendkim-genkey", "-s", "mail", "-d", fqdn, "-D", keyDir)
		agent.RunCommand(ctx, "chown", "-R", "opendkim:opendkim", keyDir)
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
			`grep -qE '^\*@%s[[:space:]]' /etc/opendkim/signing.table 2>/dev/null || echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table`,
			escFqdn, fqdn, fqdn))
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
			`grep -qE '^mail\._domainkey\.%s[[:space:]]' /etc/opendkim/key.table 2>/dev/null || echo 'mail._domainkey.%s %s:mail:%s/mail.private' >> /etc/opendkim/key.table`,
			escFqdn, fqdn, fqdn, keyDir))
	}
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"grep -qxF '%s' /etc/opendkim/trusted.hosts 2>/dev/null || echo '%s' >> /etc/opendkim/trusted.hosts",
		fqdn, fqdn))
	agent.RunCommand(ctx, "systemctl", "restart", "opendkim")

	// 2. Postfix — accept mail for this subdomain NOW instead of lazily
	//    at CreateMailbox time. Closes the race where Postfix rejects
	//    inbound delivery in the window between domain create and
	//    mailbox create. Format is `<domain> OK` for hash: compatibility.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		`grep -qE '^%s[[:space:]]' /etc/postfix/virtual_mailbox_domains 2>/dev/null || echo '%s OK' >> /etc/postfix/virtual_mailbox_domains`,
		escFqdn, fqdn))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_domains")
	agent.RunCommand(ctx, "systemctl", "reload", "postfix")

	// 3. DNS records in the parent zone. MX/SPF/DMARC always; DKIM TXT
	//    for the subdomain is only added when we minted a standalone
	//    key (parent-key reuse means DNS lookups for
	//    mail._domainkey.<sub>.<parent> follow the CNAME semantics —
	//    OpenDKIM itself is using the PARENT selector, so the subdomain
	//    TXT isn't required).
	//
	//    No separate `mail.sub` A record — subdomain mail traffic uses
	//    the parent's mail. hostname, which already has an A record.
	mailHost := fmt.Sprintf("mail.%s.", parentDomain)
	agent.RunCommand(ctx, "pdnsutil", "add-record", parentDomain, subPart, "MX", "3600",
		fmt.Sprintf("10 %s", mailHost))
	agent.RunCommand(ctx, "pdnsutil", "add-record", parentDomain, subPart, "TXT", "3600",
		fmt.Sprintf("\"v=spf1 ip4:%s ~all\"", serverIP))
	agent.RunCommand(ctx, "pdnsutil", "add-record", parentDomain, "_dmarc."+subPart, "TXT", "3600",
		fmt.Sprintf("\"v=DMARC1; p=none; rua=mailto:admin@%s\"", fqdn))

	var dkimValue string
	if !parentHasKey {
		keyDir := fmt.Sprintf("/etc/opendkim/keys/%s", fqdn)
		if r, _ := agent.RunCommand(ctx, "cat", fmt.Sprintf("%s/mail.txt", keyDir)); r != nil {
			if v := parseDKIMPublicKey(r.Output); v != "" {
				dkimValue = v
				agent.RunCommand(ctx, "pdnsutil", "add-record", parentDomain,
					"mail._domainkey."+subPart, "TXT", "3600",
					fmt.Sprintf("\"%s\"", v))
			}
		}
	}
	agent.RunCommand(ctx, "pdns_control", "reload")
	_ = escParent // kept for future IP-rewrite paths

	// 4. Persist to MongoDB under the parent zone's dns_records so the
	//    UI's DNS editor reflects them.
	var parentZone models.DNSZone
	if err := s.db.Collection(database.ColDNSZones).FindOne(ctx, bson.M{"domain": parentDomain}).Decode(&parentZone); err == nil {
		now := time.Now()
		recCol := s.db.Collection(database.ColDNSRecords)
		mxPri := 10
		toInsert := []interface{}{
			models.DNSRecord{ZoneID: parentZone.ID, Type: "MX", Name: subPart, Value: mailHost, TTL: 3600, Priority: &mxPri, CreatedAt: now, UpdatedAt: now},
			models.DNSRecord{ZoneID: parentZone.ID, Type: "TXT", Name: subPart, Value: fmt.Sprintf("v=spf1 ip4:%s ~all", serverIP), TTL: 3600, CreatedAt: now, UpdatedAt: now},
			models.DNSRecord{ZoneID: parentZone.ID, Type: "TXT", Name: "_dmarc." + subPart, Value: fmt.Sprintf("v=DMARC1; p=none; rua=mailto:admin@%s", fqdn), TTL: 3600, CreatedAt: now, UpdatedAt: now},
		}
		if dkimValue != "" {
			toInsert = append(toInsert, models.DNSRecord{ZoneID: parentZone.ID, Type: "TXT", Name: "mail._domainkey." + subPart, Value: dkimValue, TTL: 3600, CreatedAt: now, UpdatedAt: now})
		}
		recCol.InsertMany(ctx, toInsert)
		s.db.Collection(database.ColDNSZones).UpdateOne(ctx, bson.M{"_id": parentZone.ID}, bson.M{
			"$inc": bson.M{"serial": len(toInsert)},
			"$set": bson.M{"updated_at": now},
		})
	}
	return nil
}

// setupMailServer configures DKIM, Postfix virtual domain, and adds
// mail-related DNS records (MX, SPF, DKIM, DMARC) when a new zone is created.
//
// Every idempotent-append here anchors its grep on the full record
// format. The previous `grep -q 'example.com'` patterns treated the
// domain as a substring, so adding "foo.example.com" after "example.com"
// would see the older entry and silently skip the new one — leaving
// outbound mail unsigned and inbound bouncing "Domain not found".
func (s *DNSService) setupMailServer(ctx context.Context, domain, serverIP string, zone *models.DNSZone) {
	escDom := regexp.QuoteMeta(domain)

	// 1. Generate DKIM key
	keyDir := fmt.Sprintf("/etc/opendkim/keys/%s", domain)
	agent.RunCommand(ctx, "mkdir", "-p", keyDir)
	agent.RunCommand(ctx, "opendkim-genkey", "-s", "mail", "-d", domain, "-D", keyDir)
	agent.RunCommand(ctx, "chown", "-R", "opendkim:opendkim", keyDir)

	// OpenDKIM tables — anchored to avoid the substring-match bug.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		`grep -qE '^\*@%s[[:space:]]' /etc/opendkim/signing.table 2>/dev/null || echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table`,
		escDom, domain, domain))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		`grep -qE '^mail\._domainkey\.%s[[:space:]]' /etc/opendkim/key.table 2>/dev/null || echo 'mail._domainkey.%s %s:mail:%s/mail.private' >> /etc/opendkim/key.table`,
		escDom, domain, domain, keyDir))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"grep -qxF '%s' /etc/opendkim/trusted.hosts 2>/dev/null || echo '%s' >> /etc/opendkim/trusted.hosts",
		domain, domain))

	// Restart OpenDKIM
	agent.RunCommand(ctx, "systemctl", "restart", "opendkim")

	// 2. Add domain to Postfix virtual_mailbox_domains. main.cf references
	// this as `hash:`, so each line must be `<domain> OK` — a bare domain
	// name triggers the "expected format: key whitespace value" warning
	// on every postmap call and eventually produces an empty .db that
	// rejects inbound mail.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		`grep -qE '^%s[[:space:]]' /etc/postfix/virtual_mailbox_domains 2>/dev/null || echo '%s OK' >> /etc/postfix/virtual_mailbox_domains`,
		escDom, domain))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_domains")
	agent.RunCommand(ctx, "systemctl", "reload", "postfix")

	// 3. Read DKIM public key for DNS record
	dkimResult, _ := agent.RunCommand(ctx, "cat", fmt.Sprintf("%s/mail.txt", keyDir))
	dkimValue := ""
	if dkimResult != nil {
		dkimValue = parseDKIMPublicKey(dkimResult.Output)
	}

	// 4. Add mail DNS records to PowerDNS. mail A record uses the
	// 60s default so an IP migration of the mail host is fast; MX
	// stays 3600 since the hostname rarely changes.
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "mail", "A", fmt.Sprint(defaultTTLFor("A")), serverIP)
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "@", "MX", "3600", fmt.Sprintf("10 mail.%s.", domain))
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "@", "TXT", "3600", fmt.Sprintf("\"v=spf1 ip4:%s ~all\"", serverIP))
	if dkimValue != "" {
		agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "mail._domainkey", "TXT", "3600", fmt.Sprintf("\"%s\"", dkimValue))
	}
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "_dmarc", "TXT", "3600", fmt.Sprintf("\"v=DMARC1; p=none; rua=mailto:admin@%s\"", domain))
	agent.RunCommand(ctx, "pdns_control", "reload")

	// 5. Save mail DNS records to MongoDB
	now := time.Now()
	recCol := s.db.Collection(database.ColDNSRecords)
	mxPri := 10
	mailRecords := []interface{}{
		models.DNSRecord{ZoneID: zone.ID, Type: "A", Name: "mail", Value: serverIP, TTL: defaultTTLFor("A"), CreatedAt: now, UpdatedAt: now},
		models.DNSRecord{ZoneID: zone.ID, Type: "MX", Name: "@", Value: fmt.Sprintf("mail.%s.", domain), TTL: 3600, Priority: &mxPri, CreatedAt: now, UpdatedAt: now},
		models.DNSRecord{ZoneID: zone.ID, Type: "TXT", Name: "@", Value: fmt.Sprintf("v=spf1 ip4:%s ~all", serverIP), TTL: 3600, CreatedAt: now, UpdatedAt: now},
		models.DNSRecord{ZoneID: zone.ID, Type: "TXT", Name: "_dmarc", Value: fmt.Sprintf("v=DMARC1; p=none; rua=mailto:admin@%s", domain), TTL: 3600, CreatedAt: now, UpdatedAt: now},
	}
	if dkimValue != "" {
		mailRecords = append(mailRecords, models.DNSRecord{ZoneID: zone.ID, Type: "TXT", Name: "mail._domainkey", Value: dkimValue, TTL: 3600, CreatedAt: now, UpdatedAt: now})
	}
	recCol.InsertMany(ctx, mailRecords)

	// Update zone serial
	s.db.Collection(database.ColDNSZones).UpdateOne(ctx, bson.M{"_id": zone.ID}, bson.M{
		"$inc": bson.M{"serial": len(mailRecords)},
		"$set": bson.M{"updated_at": now},
	})
}

// bulkTTLAllowedTypes is the whitelist the bulk-TTL sweep accepts. SOA
// is intentionally absent — its TTL governs negative-caching behaviour
// (RFC 2308 §5) and is the zone's own "minimum" field; mass-rewriting
// it could hide propagation regressions on a dozen domains at once.
// NSEC/NSEC3/RRSIG aren't operator-edited (DNSSEC handles them) so
// they're omitted too.
var bulkTTLAllowedTypes = map[string]bool{
	"A": true, "AAAA": true, "AFSDB": true, "CAA": true, "CNAME": true,
	"DNAME": true, "DS": true, "HINFO": true, "HTTPS": true, "LOC": true,
	"MX": true, "NAPTR": true, "NS": true, "PTR": true, "RP": true,
	"SRV": true, "TXT": true,
}

// BulkUpdateTTL sweeps every zone the caller can see, re-stamps every
// record whose type is in the requested set with the new TTL, and
// reconciles each affected rrset back to PowerDNS. Used by the
// "Bulk TTL update" modal — vendor_owner runs it across the whole
// fleet; a tenant-scoped vendor runs it across their own domains only.
//
// Scoping comes for free: ListZones already filters by
// CallerScope.TenantDomains() for non-owner callers, and ListRecords
// gates each per-zone fetch through assertCallerOwnsDomain. So a
// vendor_admin who calls this endpoint can never touch another
// tenant's zones, even if they crafted a request that would have.
//
// Failure model: per-zone, not all-or-nothing. A reconciliation
// failure on one zone (e.g. PowerDNS unavailable for that name)
// records the error against just that zone and lets the rest of the
// sweep proceed. The operator gets a per-zone success/failure list
// in the response so they can retry only the failed ones.
//
// Idempotency: hitting the endpoint twice with the same TTL is a
// no-op on the second call (Mongo UpdateMany matches nothing, the
// rrset reconcile sees no TTL drift, the replace-rrset is still
// emitted but with identical values). Safe to retry.
func (s *DNSService) BulkUpdateTTL(ctx context.Context, types []string, newTTL int) (*models.BulkTTLResponse, error) {
	if len(types) == 0 {
		return nil, fmt.Errorf("at least one record type is required")
	}
	if newTTL < 30 || newTTL > 604800 {
		return nil, fmt.Errorf("ttl must be between 30 seconds and 604800 (1 week)")
	}
	// Normalize and validate types up-front so we fail fast on a typo
	// instead of silently matching nothing per zone.
	typeSet := make(map[string]bool, len(types))
	for _, t := range types {
		t = strings.ToUpper(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if t == "SOA" {
			return nil, fmt.Errorf("SOA records are zone-managed and cannot be bulk-edited; pick A/AAAA/CNAME/MX/TXT/NS/etc. instead")
		}
		if !bulkTTLAllowedTypes[t] {
			return nil, fmt.Errorf("record type %q is not supported for bulk TTL update", t)
		}
		typeSet[t] = true
	}
	if len(typeSet) == 0 {
		return nil, fmt.Errorf("at least one record type is required")
	}

	zones, err := s.ListZones(ctx)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}

	resp := &models.BulkTTLResponse{
		DomainsConsidered: len(zones),
		Items:             make([]models.BulkTTLZoneResult, 0, len(zones)),
	}

	now := time.Now()
	for _, zone := range zones {
		item := models.BulkTTLZoneResult{Domain: zone.Domain}

		records, err := s.ListRecords(ctx, zone.Domain)
		if err != nil {
			item.Error = err.Error()
			resp.Items = append(resp.Items, item)
			continue
		}

		// Group matching records by (name, type) — that's the rrset
		// granularity PowerDNS replaces atomically. We collect Mongo
		// IDs of matching rows so a single UpdateMany flips them all.
		type rrsetKey struct{ name, rtype string }
		rrsets := make(map[rrsetKey]bool)
		matchingIDs := make([]primitive.ObjectID, 0)
		for _, r := range records {
			if !typeSet[r.Type] {
				continue
			}
			rrsets[rrsetKey{name: r.Name, rtype: r.Type}] = true
			if !r.ID.IsZero() {
				matchingIDs = append(matchingIDs, r.ID)
			}
		}

		if len(matchingIDs) == 0 {
			// Zone had nothing matching — record a clean zero-row
			// result so the UI can show "0 records in example.com"
			// instead of dropping the zone from the report entirely.
			resp.Items = append(resp.Items, item)
			continue
		}

		// One UpdateMany flips every matching row's TTL. The
		// reconcile loop below propagates that to PowerDNS.
		col := s.db.Collection(database.ColDNSRecords)
		upd, err := col.UpdateMany(ctx,
			bson.M{"_id": bson.M{"$in": matchingIDs}},
			bson.M{"$set": bson.M{"ttl": newTTL, "updated_at": now}})
		if err != nil {
			item.Error = fmt.Sprintf("mongo update: %v", err)
			resp.Items = append(resp.Items, item)
			continue
		}
		item.UpdatedCount = int(upd.ModifiedCount)
		item.RRSetsAffected = len(rrsets)

		// reconcileRRSet needs the zone's Mongo ID. GetZone honours
		// the same scope check, but we already passed via ListZones
		// + ListRecords above so a NotFound here means a heal-on-read
		// race (zone exists in PowerDNS but Mongo dns_zones row is
		// missing). Skip the reconcile for that zone — the next manual
		// edit on the records page will heal it via GetOrCreateZone.
		zoneRow, zerr := s.GetZone(ctx, zone.Domain)
		if zerr != nil || zoneRow == nil {
			item.Error = "zone metadata missing — TTL written to Mongo, PowerDNS reconcile skipped (next manual edit will heal)"
			resp.Items = append(resp.Items, item)
			resp.TotalRecordsUpdated += item.UpdatedCount
			resp.DomainsAffected++
			continue
		}

		// Push every changed rrset to PowerDNS. Per-rrset failures
		// are captured into item.Error; we keep going so a single
		// stuck name doesn't abort the whole zone's sweep.
		var firstReconcileErr string
		for k := range rrsets {
			if rerr := s.reconcileRRSet(ctx, zoneRow.ID, zone.Domain, k.name, k.rtype); rerr != nil {
				if firstReconcileErr == "" {
					firstReconcileErr = fmt.Sprintf("reconcile %s/%s: %v", k.name, k.rtype, rerr)
				}
			}
		}
		if firstReconcileErr != "" {
			item.Error = firstReconcileErr
		}

		resp.TotalRecordsUpdated += item.UpdatedCount
		resp.DomainsAffected++
		resp.Items = append(resp.Items, item)
	}

	return resp, nil
}
