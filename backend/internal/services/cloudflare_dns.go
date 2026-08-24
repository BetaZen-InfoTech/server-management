package services

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/cloudflare"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// DNSProvider is the write seam for DNS backends. PowerDNS remains the panel's
// existing concrete path (agent.* + DNSService); Cloudflare is the first
// implementation behind this interface. It is intentionally small — just the
// operations the connect + sync flows need — so introducing it doesn't force a
// rewrite of the working PowerDNS code.
type DNSProvider interface {
	Name() string
	// EnsureZone finds the zone for domain and creates it only if absent
	// (never duplicates). Returns the zone and whether it was newly created.
	EnsureZone(ctx context.Context, domain, accountID string) (zone *cloudflare.Zone, created bool, err error)
	ListRecords(ctx context.Context, zoneID string) ([]cloudflare.DNSRecord, error)
	CreateRecord(ctx context.Context, zoneID string, p cloudflare.RecordParams) (*cloudflare.DNSRecord, error)
	UpdateRecord(ctx context.Context, zoneID, recordID string, p cloudflare.RecordParams) (*cloudflare.DNSRecord, error)
	DeleteRecord(ctx context.Context, zoneID, recordID string) error
}

// cloudflareProvider implements DNSProvider over the pkg/cloudflare client.
type cloudflareProvider struct{ client *cloudflare.Client }

func (p *cloudflareProvider) Name() string { return "cloudflare" }

func (p *cloudflareProvider) EnsureZone(ctx context.Context, domain, accountID string) (*cloudflare.Zone, bool, error) {
	z, err := p.client.FindZoneByName(ctx, domain)
	if err != nil {
		return nil, false, err
	}
	if z != nil {
		return z, false, nil
	}
	z, err = p.client.CreateZone(ctx, domain, accountID)
	if err != nil {
		return nil, false, err
	}
	return z, true, nil
}

func (p *cloudflareProvider) ListRecords(ctx context.Context, zoneID string) ([]cloudflare.DNSRecord, error) {
	return p.client.ListDNSRecords(ctx, zoneID)
}
func (p *cloudflareProvider) CreateRecord(ctx context.Context, zoneID string, rp cloudflare.RecordParams) (*cloudflare.DNSRecord, error) {
	return p.client.CreateDNSRecord(ctx, zoneID, rp)
}
func (p *cloudflareProvider) UpdateRecord(ctx context.Context, zoneID, recordID string, rp cloudflare.RecordParams) (*cloudflare.DNSRecord, error) {
	return p.client.UpdateDNSRecord(ctx, zoneID, recordID, rp)
}
func (p *cloudflareProvider) DeleteRecord(ctx context.Context, zoneID, recordID string) error {
	return p.client.DeleteDNSRecord(ctx, zoneID, recordID)
}

// providerFor returns a Cloudflare DNSProvider + the account id, but only when
// the integration is enabled and a token exists.
func (s *CloudflareService) providerFor(ctx context.Context) (DNSProvider, string, error) {
	doc, err := s.load(ctx)
	if err != nil {
		return nil, "", err
	}
	if doc == nil || !doc.Enabled {
		return nil, "", ErrCloudflareDisabled
	}
	token, err := s.Token(ctx)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(token) == "" {
		return nil, "", ErrCloudflareNoToken
	}
	return &cloudflareProvider{client: s.newClient(token)}, doc.AccountID, nil
}

// classifyRecord decides whether a record is mail / web / user-owned so the
// sync and IP-change flows can PROTECT mail records. It prefers the explicit
// ManagedBy marker (stamped on records the integration creates) and falls back
// to name/type/value heuristics for legacy rows that have no marker.
//
// This is the single source of truth for "is this a mail record?" — the safety
// property the whole design hinges on (a web-IP sync must never rewrite or
// proxy MX / SPF / DKIM / DMARC / mail-A records).
func classifyRecord(typ, name, value, managedBy string) string {
	switch managedBy {
	case "mail", "web", "user":
		return managedBy
	}
	t := strings.ToUpper(strings.TrimSpace(typ))
	n := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	v := strings.ToLower(value)
	switch {
	case t == "MX":
		return "mail"
	case t == "TXT" && (strings.Contains(v, "v=spf1") || strings.Contains(v, "v=dmarc1")):
		return "mail"
	case strings.HasPrefix(n, "_dmarc") || strings.Contains(n, "_domainkey"):
		return "mail"
	case t == "A" && (n == "mail" || strings.HasPrefix(n, "mail.")):
		return "mail"
	default:
		return "web"
	}
}

// isMailRecord is the boolean convenience over classifyRecord.
func isMailRecord(typ, name, value, managedBy string) bool {
	return classifyRecord(typ, name, value, managedBy) == "mail"
}

// ConnectResult is returned by ConnectDomain.
type ConnectResult struct {
	Domain      string   `json:"domain"`
	ZoneID      string   `json:"zone_id"`
	Status      string   `json:"status"`
	Nameservers []string `json:"nameservers"`
	Created     bool     `json:"created"` // true = new zone created, false = existing zone reused
}

// ConnectDomain finds (or creates, if absent) the Cloudflare zone for a domain
// and stamps the zone id + nameservers + status onto the local dns_zones row.
// It NEVER duplicates a zone — an existing zone is reused. This is the write
// entry point for "switch this domain to Cloudflare DNS".
func (s *CloudflareService) ConnectDomain(ctx context.Context, domain string) (*ConnectResult, error) {
	domain = normalizeDomain(domain)
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	provider, accountID, err := s.providerFor(ctx)
	if err != nil {
		return nil, err
	}
	zone, created, err := provider.EnsureZone(ctx, domain, accountID)
	if err != nil {
		return nil, err
	}
	s.stampZoneCloudflare(ctx, domain, zone, accountID)
	return &ConnectResult{
		Domain:      domain,
		ZoneID:      zone.ID,
		Status:      zone.Status,
		Nameservers: zone.NameServers,
		Created:     created,
	}, nil
}

// stampZoneCloudflare writes the Cloudflare mapping onto the local dns_zones
// row (upsert — additive fields only, keyed by domain). It never touches the
// PowerDNS-owned fields (server_ip, serial, existing nameservers).
func (s *CloudflareService) stampZoneCloudflare(ctx context.Context, domain string, zone *cloudflare.Zone, accountID string) {
	_, _ = s.db.Collection(database.ColDNSZones).UpdateOne(ctx,
		bson.M{"domain": domain},
		bson.M{"$set": bson.M{
			"provider":       "cloudflare",
			"cf_zone_id":     zone.ID,
			"cf_account_id":  accountID,
			"cf_status":      zone.Status,
			"cf_nameservers": zone.NameServers,
			"sync_state":     "pending",
			"updated_at":     time.Now(),
		}},
		options.Update().SetUpsert(true),
	)
}

// resolveZoneID returns the Cloudflare zone id for a domain: the stamped
// dns_zones.cf_zone_id when present, else a live FindZoneByName lookup. Returns
// "" (no error) when the account genuinely has no zone for the domain.
func (s *CloudflareService) resolveZoneID(ctx context.Context, domain string) (string, error) {
	var zdoc models.DNSZone
	err := s.db.Collection(database.ColDNSZones).FindOne(ctx, bson.M{"domain": domain}).Decode(&zdoc)
	if err == nil && zdoc.CloudflareZoneID != "" {
		return zdoc.CloudflareZoneID, nil
	}
	if err != nil && err != mongo.ErrNoDocuments {
		return "", err
	}
	zone, err := s.FindZone(ctx, domain)
	if err != nil {
		return "", err
	}
	if zone == nil {
		return "", nil
	}
	return zone.ID, nil
}

// CreateRecord adds a DNS record to a domain's Cloudflare zone. Mail records
// are forced DNS-only (never proxied) regardless of the requested proxied flag.
func (s *CloudflareService) CreateRecord(ctx context.Context, domain string, p cloudflare.RecordParams) (*cloudflare.DNSRecord, error) {
	domain = normalizeDomain(domain)
	provider, _, err := s.providerFor(ctx)
	if err != nil {
		return nil, err
	}
	zoneID, err := s.resolveZoneID(ctx, domain)
	if err != nil {
		return nil, err
	}
	if zoneID == "" {
		return nil, fmt.Errorf("domain %q is not connected to Cloudflare", domain)
	}
	forceMailDNSOnly(&p)
	forceUncoverableDNSOnly(&p, domain, s.AdvancedCertsOn(ctx))
	return provider.CreateRecord(ctx, zoneID, p)
}

// UpdateRecord replaces a record in a domain's Cloudflare zone. Mail records
// are forced DNS-only.
func (s *CloudflareService) UpdateRecord(ctx context.Context, domain, recordID string, p cloudflare.RecordParams) (*cloudflare.DNSRecord, error) {
	domain = normalizeDomain(domain)
	provider, _, err := s.providerFor(ctx)
	if err != nil {
		return nil, err
	}
	zoneID, err := s.resolveZoneID(ctx, domain)
	if err != nil {
		return nil, err
	}
	if zoneID == "" {
		return nil, fmt.Errorf("domain %q is not connected to Cloudflare", domain)
	}
	forceMailDNSOnly(&p)
	forceUncoverableDNSOnly(&p, domain, s.AdvancedCertsOn(ctx))
	return provider.UpdateRecord(ctx, zoneID, recordID, p)
}

// DeleteRecord removes a record from a domain's Cloudflare zone. Deletion is a
// destructive operation and REQUIRES confirm=true (the human-approval gate).
// The record is classified first so the caller gets a clear warning when the
// target is a mail record.
func (s *CloudflareService) DeleteRecord(ctx context.Context, domain, recordID string, confirm bool) error {
	domain = normalizeDomain(domain)
	provider, _, err := s.providerFor(ctx)
	if err != nil {
		return err
	}
	zoneID, err := s.resolveZoneID(ctx, domain)
	if err != nil {
		return err
	}
	if zoneID == "" {
		return fmt.Errorf("domain %q is not connected to Cloudflare", domain)
	}

	// Look the record up so we can classify it for the confirmation message.
	recs, err := provider.ListRecords(ctx, zoneID)
	if err != nil {
		return err
	}
	var target *cloudflare.DNSRecord
	for i := range recs {
		if recs[i].ID == recordID {
			target = &recs[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("record %q not found in zone", recordID)
	}
	mail := isMailRecord(target.Type, target.Name, target.Content, "")
	if !confirm {
		if mail {
			return fmt.Errorf("refusing to delete a MAIL record (%s %s) without confirmation — pass confirm=true to proceed", target.Type, target.Name)
		}
		return fmt.Errorf("deleting a Cloudflare record (%s %s) requires confirmation — pass confirm=true", target.Type, target.Name)
	}
	return provider.DeleteRecord(ctx, zoneID, recordID)
}

// forceMailDNSOnly flips Proxied off for any record classified as mail, so an
// operator (or a future automated flow) can never orange-cloud MX / SPF / DKIM
// / DMARC / mail-A records — proxying them would break mail delivery.
func forceMailDNSOnly(p *cloudflare.RecordParams) {
	if isMailRecord(p.Type, p.Name, p.Content, "") {
		off := false
		p.Proxied = &off
	}
}

// proxyableType reports whether Cloudflare can proxy (orange-cloud) this record
// type. Only A, AAAA and CNAME ride the HTTP/HTTPS reverse proxy; every other
// type (MX, TXT, NS, SRV, CAA, SOA, …) is DNS-only by nature and Cloudflare
// rejects a proxied=true on them.
func proxyableType(typ string) bool {
	switch strings.ToUpper(typ) {
	case "A", "AAAA", "CNAME":
		return true
	default:
		return false
	}
}

// hostLabelsBelowZone returns how many DNS labels a hostname sits below the zone
// apex: the apex itself is 0, a single-level subdomain (api.zone) is 1, a
// two-level subdomain (api.saas.zone) is 2, and so on. It returns -1 when name
// is not inside the zone at all (defensive — callers treat that as not
// coverable). Both arguments may be FQDNs or carry a trailing dot.
func hostLabelsBelowZone(name, zone string) int {
	n := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	z := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone), "."))
	if n == "" || n == "@" || n == z {
		return 0 // apex (or a relative "@"/empty name that resolves to the apex)
	}
	if z == "" {
		return -1
	}
	suffix := "." + z
	if !strings.HasSuffix(n, suffix) {
		return -1
	}
	prefix := strings.TrimSuffix(n, suffix)
	if prefix == "" {
		return 0
	}
	return strings.Count(prefix, ".") + 1
}

// universalSSLCovers reports whether Cloudflare's free-plan Universal SSL
// certificate — which secures exactly `zone` and `*.zone` — can terminate TLS
// for a proxied hostname. The apex (0 labels below) and any single-label
// subdomain (1 label below) are covered; a wildcard matches only ONE label, so
// `a.b.zone` and deeper (2+) are NOT. Orange-clouding a host Universal SSL can't
// cover makes Cloudflare present no certificate → every visitor hits a TLS
// handshake failure. This is the guard the whole "why did my subdomain break"
// fix hinges on.
func universalSSLCovers(name, zone string) bool {
	d := hostLabelsBelowZone(name, zone)
	return d == 0 || d == 1
}

// resolveProxied is the single source of truth for every orange-cloud decision.
// It implements the 3-level proxy policy (system default → per-domain →
// per-record, most specific wins) behind two HARD-SAFETY gates that override
// every tier:
//   - mail / non-proxyable types            → DNS-only (false)
//   - multi-level subdomain without ACM      → DNS-only (false): free-plan
//     Universal SSL can't cover it, so proxying breaks TLS. advancedCerts
//     (Total TLS / ACM) lifts this restriction.
//
// Then the tier chain resolves (recordMode/zoneMode are ProxyMode* values):
//  1. per-record on/off  → decides
//  2. per-domain on/off  → decides
//  3. system: proxyWeb ON → proxied; OFF → preserve the record's current CF
//     state (never silently turn OFF an operator's intentional orange-cloud).
func resolveProxied(typ, name, content, zone, recordMode, zoneMode string, proxyWeb, advancedCerts, current bool) bool {
	if !proxyableType(typ) || isMailRecord(typ, name, content, "") {
		return false
	}
	if !advancedCerts && !universalSSLCovers(name, zone) {
		return false
	}
	switch models.NormalizeProxyMode(recordMode) {
	case models.ProxyModeOn:
		return true
	case models.ProxyModeOff:
		return false
	}
	switch models.NormalizeProxyMode(zoneMode) {
	case models.ProxyModeOn:
		return true
	case models.ProxyModeOff:
		return false
	}
	if proxyWeb {
		return true
	}
	return current
}

// proxiedDecision is the mode-less shorthand (system tier only) kept for the
// paths and tests that don't carry per-record / per-domain overrides.
func proxiedDecision(typ, name, content, zone string, proxyWeb, advancedCerts, current bool) bool {
	return resolveProxied(typ, name, content, zone, "", "", proxyWeb, advancedCerts, current)
}

// applyProxyPolicy sets p.Proxied for a record being CREATED, resolving the full
// 3-level policy (per-record → per-domain → system) with hard safety. Proxyable
// web records get an EXPLICIT true/false — never left nil — because Cloudflare
// defaults new A/AAAA/CNAME records to proxied, which would silently break a
// multi-level subdomain. Non-proxyable types are left untouched (Cloudflare
// rejects a proxied flag on MX/TXT/NS/…).
func applyProxyPolicy(p *cloudflare.RecordParams, recordMode, zoneMode, zone string, proxyWeb, advancedCerts bool) {
	if !proxyableType(p.Type) {
		return
	}
	b := resolveProxied(p.Type, p.Name, p.Content, zone, recordMode, zoneMode, proxyWeb, advancedCerts, false)
	p.Proxied = &b
}

// forceUncoverableDNSOnly flips Proxied off for any proxyable record whose
// hostname Cloudflare's certificate can't cover (a multi-level subdomain when
// Advanced Certificate Manager / Total TLS is not enabled). It mirrors
// forceMailDNSOnly: a last-line guard on every single-record write path so the
// panel can never orange-cloud a host that would then fail its TLS handshake.
func forceUncoverableDNSOnly(p *cloudflare.RecordParams, zone string, advancedCerts bool) {
	if !proxyableType(p.Type) {
		return
	}
	if !advancedCerts && !universalSSLCovers(p.Name, zone) {
		off := false
		p.Proxied = &off
	}
}

// ZoneProxyMode returns the per-domain proxy override (ProxyMode* value) for a
// domain, normalized. Absent / unknown → ProxyModeDefault (inherit system).
func (s *CloudflareService) ZoneProxyMode(ctx context.Context, domain string) string {
	domain = normalizeDomain(domain)
	var z models.DNSZone
	if err := s.db.Collection(database.ColDNSZones).FindOne(ctx, bson.M{"domain": domain}).Decode(&z); err != nil {
		return models.ProxyModeDefault
	}
	return models.NormalizeProxyMode(z.ProxyMode)
}

// SetZoneProxyMode persists the per-domain proxy override on the dns_zones row.
// The value is normalized to a known ProxyMode* so the DB never holds garbage.
// The caller is responsible for triggering a Cloudflare sync afterwards so the
// change is reconciled to the live zone.
func (s *CloudflareService) SetZoneProxyMode(ctx context.Context, domain, mode string) error {
	domain = normalizeDomain(domain)
	if domain == "" {
		return fmt.Errorf("domain is required")
	}
	res, err := s.db.Collection(database.ColDNSZones).UpdateOne(ctx,
		bson.M{"domain": domain},
		bson.M{"$set": bson.M{"proxy_mode": models.NormalizeProxyMode(mode), "updated_at": time.Now()}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("domain %q not found", domain)
	}
	// Best-effort: reconcile the live Cloudflare zone so the new default takes
	// effect immediately (no-op when the domain isn't connected to Cloudflare).
	_, _ = s.reapplyZoneProxy(ctx, domain)
	return nil
}

// SetRecordProxyMode persists the per-record proxy override on a dns_records row
// (identified by its Mongo id, scoped to the domain's zone so a caller can't
// touch another tenant's record). The caller triggers a sync afterwards.
func (s *CloudflareService) SetRecordProxyMode(ctx context.Context, domain, recordID, mode string) error {
	domain = normalizeDomain(domain)
	rid, err := primitive.ObjectIDFromHex(recordID)
	if err != nil {
		return fmt.Errorf("invalid record id")
	}
	var z models.DNSZone
	if err := s.db.Collection(database.ColDNSZones).FindOne(ctx, bson.M{"domain": domain}).Decode(&z); err != nil {
		return fmt.Errorf("domain %q not found", domain)
	}
	res, err := s.db.Collection(database.ColDNSRecords).UpdateOne(ctx,
		bson.M{"_id": rid, "zone_id": z.ID},
		bson.M{"$set": bson.M{"proxy_mode": models.NormalizeProxyMode(mode), "updated_at": time.Now()}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("record not found in domain %q", domain)
	}
	// Best-effort: reconcile the live Cloudflare zone so the override takes effect
	// immediately (no-op when the domain isn't connected to Cloudflare).
	_, _ = s.reapplyZoneProxy(ctx, domain)
	return nil
}

// reapplyZoneProxy reconciles ONLY the proxied flag of a zone's Cloudflare
// records to the current 3-level policy (system → per-domain → per-record),
// leaving content/TTL untouched. It's the immediate-effect path behind the
// proxy-mode setters and never creates or deletes records. No-op (nil) when the
// domain isn't connected to Cloudflare or the integration is off.
func (s *CloudflareService) reapplyZoneProxy(ctx context.Context, domain string) (int, error) {
	domain = normalizeDomain(domain)
	provider, _, err := s.providerFor(ctx)
	if err != nil {
		if err == ErrCloudflareDisabled || err == ErrCloudflareNoToken {
			return 0, nil
		}
		return 0, err
	}
	zoneID, err := s.resolveZoneID(ctx, domain)
	if err != nil || zoneID == "" {
		return 0, err
	}
	proxyWeb := s.ProxyWebRecordsOn(ctx)
	advancedCerts := s.AdvancedCertsOn(ctx)
	zoneMode := s.ZoneProxyMode(ctx, domain)

	locals, err := s.localRecords(ctx, domain)
	if err != nil {
		return 0, err
	}
	modeByKey := map[string]string{}
	for _, r := range locals {
		k := strings.ToUpper(r.Type) + "|" + normalizeName(r.Name, domain) + "|" + normalizeValue(r.Type, r.Value, r.Priority)
		modeByKey[k] = r.ProxyMode
	}

	cfRecs, err := provider.ListRecords(ctx, zoneID)
	if err != nil {
		return 0, err
	}
	updated := 0
	for _, r := range cfRecs {
		if !proxyableType(r.Type) {
			continue
		}
		k := strings.ToUpper(r.Type) + "|" + normalizeName(r.Name, domain) + "|" + normalizeValue(r.Type, r.Content, r.Priority)
		want := resolveProxied(r.Type, r.Name, r.Content, domain, modeByKey[k], zoneMode, proxyWeb, advancedCerts, r.Proxied)
		if want == r.Proxied {
			continue
		}
		p := cloudflare.RecordParams{Type: r.Type, Name: r.Name, Content: r.Content, TTL: r.TTL, Proxied: &want}
		if r.Priority != nil {
			pr := *r.Priority
			p.Priority = &pr
		}
		if _, err := provider.UpdateRecord(ctx, zoneID, r.ID, p); err == nil {
			updated++
		}
	}
	return updated, nil
}

// DomainCloudflareEnabled reports whether Cloudflare is enabled for a single
// domain. Absent (nil) = default-enabled; explicit false = the operator disabled
// Cloudflare for THIS domain only.
func (s *CloudflareService) DomainCloudflareEnabled(ctx context.Context, domain string) bool {
	domain = normalizeDomain(domain)
	var z models.DNSZone
	if err := s.db.Collection(database.ColDNSZones).FindOne(ctx, bson.M{"domain": domain}).Decode(&z); err != nil {
		return true // no zone row yet → default enabled
	}
	if z.CloudflareEnabled == nil {
		return true
	}
	return *z.CloudflareEnabled
}

// DomainConnected reports whether the domain has an actual Cloudflare zone
// (cf_zone_id set). Used to gate the DNS-change auto-sync so a record edit on a
// domain that was never connected to Cloudflare doesn't start a no-op sync job.
func (s *CloudflareService) DomainConnected(ctx context.Context, domain string) bool {
	domain = normalizeDomain(domain)
	var z models.DNSZone
	if err := s.db.Collection(database.ColDNSZones).FindOne(ctx,
		bson.M{"domain": domain, "cf_zone_id": bson.M{"$exists": true, "$ne": ""}}).Decode(&z); err != nil {
		return false
	}
	return z.CloudflareZoneID != ""
}

// SetDomainCloudflareEnabled toggles Cloudflare for a single domain (WHM only).
// Disabling one domain never affects any other domain, and it does NOT delete
// the Cloudflare zone — it only stops the auto/bulk sync from touching it.
func (s *CloudflareService) SetDomainCloudflareEnabled(ctx context.Context, domain string, enabled bool) error {
	domain = normalizeDomain(domain)
	if domain == "" {
		return fmt.Errorf("domain is required")
	}
	_, err := s.db.Collection(database.ColDNSZones).UpdateOne(ctx,
		bson.M{"domain": domain},
		bson.M{"$set": bson.M{"cloudflare_enabled": enabled, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)
	return err
}

// NameserverStatus is the delegation check result for a domain.
type NameserverStatus struct {
	Domain             string   `json:"domain"`
	Connected          bool     `json:"connected"`           // has a Cloudflare zone
	ZoneID             string   `json:"zone_id,omitempty"`
	ZoneStatus         string   `json:"zone_status,omitempty"` // Cloudflare's own zone status (pending/active/…)
	CFNameservers      []string `json:"cf_nameservers,omitempty"`      // what Cloudflare assigned
	CurrentNameservers []string `json:"current_nameservers,omitempty"` // what the domain is ACTUALLY delegated to (live DNS)
	Delegated          bool     `json:"delegated"`           // current NS match Cloudflare's assigned NS
	State              string   `json:"state"`               // not_connected | nameserver_update_required | pending_activation | active | paused | error
	Message            string   `json:"message,omitempty"`
}

// CheckNameservers does a LIVE delegation check: it fetches the domain's
// Cloudflare zone (assigned nameservers + zone status) and performs a real DNS
// NS lookup to see what the domain is actually delegated to, then derives a
// human state. It does not rely only on stored state.
func (s *CloudflareService) CheckNameservers(ctx context.Context, domain string) (*NameserverStatus, error) {
	domain = normalizeDomain(domain)
	res := &NameserverStatus{Domain: domain}

	zone, err := s.FindZone(ctx, domain)
	if err != nil {
		return nil, err
	}
	if zone == nil {
		res.State = "not_connected"
		res.Message = "domain has no Cloudflare zone yet"
		return res, nil
	}
	res.Connected = true
	res.ZoneID = zone.ID
	res.ZoneStatus = zone.Status
	res.CFNameservers = lowerTrimAll(zone.NameServers)

	// Live NS lookup — what the registrar actually delegates to right now.
	lookupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var resolver net.Resolver
	nss, lookErr := resolver.LookupNS(lookupCtx, domain)
	current := make([]string, 0, len(nss))
	for _, ns := range nss {
		current = append(current, strings.ToLower(strings.TrimSuffix(ns.Host, ".")))
	}
	sort.Strings(current)
	res.CurrentNameservers = current

	// Delegation = every Cloudflare-assigned NS is present in the live NS set.
	res.Delegated = len(res.CFNameservers) > 0 && subsetOf(res.CFNameservers, current)

	switch {
	case strings.EqualFold(zone.Status, "paused"):
		res.State = "paused"
		res.Message = "the Cloudflare zone is paused"
	case strings.EqualFold(zone.Status, "active"):
		res.State = "active"
	case res.Delegated:
		res.State = "pending_activation"
		res.Message = "nameservers point to Cloudflare; waiting for Cloudflare to activate the zone"
	case lookErr != nil:
		res.State = "nameserver_update_required"
		res.Message = "could not resolve current nameservers — update the registrar to the Cloudflare nameservers"
	default:
		res.State = "nameserver_update_required"
		res.Message = "the domain is not yet delegated to Cloudflare's nameservers"
	}
	return res, nil
}

// VerifyDomain is the on-demand "Verify domain" action: it asks Cloudflare to
// re-check the zone's nameserver delegation immediately (activation_check) and
// then returns — and persists — the freshly computed status. Use it once the
// registrar has been pointed at the Cloudflare nameservers to trigger
// activation now instead of waiting for Cloudflare's periodic scan or the
// background sweep. The activation_check call is best-effort (Cloudflare
// rate-limits it ~1/5min/zone); a failure there never fails the verify — the
// live status is still recomputed and returned.
func (s *CloudflareService) VerifyDomain(ctx context.Context, domain string) (*NameserverStatus, error) {
	domain = normalizeDomain(domain)
	zoneID, err := s.resolveZoneID(ctx, domain)
	if err != nil {
		return nil, err
	}
	if zoneID == "" {
		return nil, fmt.Errorf("domain %q is not connected to Cloudflare", domain)
	}
	if token, terr := s.Token(ctx); terr == nil && strings.TrimSpace(token) != "" {
		_ = s.newClient(token).ActivationCheck(ctx, zoneID)
	}
	st, err := s.CheckNameservers(ctx, domain)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	set := bson.M{"ns_state": st.State, "ns_checked_at": now}
	if st.ZoneStatus != "" {
		set["cf_status"] = st.ZoneStatus
	}
	if len(st.CFNameservers) > 0 {
		set["cf_nameservers"] = st.CFNameservers
	}
	_, _ = s.db.Collection(database.ColDNSZones).UpdateOne(ctx,
		bson.M{"domain": domain}, bson.M{"$set": set})
	s.syncDomainNameservers(ctx, domain, st.CurrentNameservers)
	return st, nil
}

// syncDomainNameservers keeps the domains.nameservers field (shown on the
// Domains page) in step with what DNS actually delegates to *right now* — the
// same live lookup the Cloudflare section shows as "current". WHOIS/RDAP is
// periodic and lags a registrar nameserver change, which made the Domains
// modal show two different nameserver values (stale WHOIS vs live). Writing the
// live result back keeps a single source of truth. Only writes on a successful
// live lookup (non-empty current set).
func (s *CloudflareService) syncDomainNameservers(ctx context.Context, domain string, current []string) {
	if len(current) == 0 {
		return
	}
	_, _ = s.db.Collection(database.ColDomains).UpdateOne(ctx,
		bson.M{"domain": domain},
		bson.M{"$set": bson.M{"nameservers": current, "last_checked_at": time.Now()}})
}

// SweepNameservers is the background auto-verification pass: for every
// Cloudflare-connected, non-disabled zone it runs the same live check as the
// "Check nameservers" button (CF zone status + a live NS lookup) and persists
// the result (ns_state, ns_checked_at) plus a refreshed cf_status/cf_nameservers
// onto the local zone row. This lets the panel show delegation progress —
// including auto-flipping to "active" once the registrar cutover propagates —
// without the operator ever clicking. Returns how many zones were checked.
// No-op when the integration is disabled. Best-effort per zone: one failure
// never aborts the sweep.
func (s *CloudflareService) SweepNameservers(ctx context.Context) (int, error) {
	if !s.IsEnabled(ctx) {
		return 0, nil
	}
	cur, err := s.db.Collection(database.ColDNSZones).Find(ctx, bson.M{
		"cf_zone_id": bson.M{"$exists": true, "$ne": ""},
	})
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)

	checked := 0
	for cur.Next(ctx) {
		var z models.DNSZone
		if err := cur.Decode(&z); err != nil {
			continue
		}
		// Respect the per-domain disable toggle (explicit false).
		if z.CloudflareEnabled != nil && !*z.CloudflareEnabled {
			continue
		}
		st, err := s.CheckNameservers(ctx, z.Domain)
		if err != nil || st == nil {
			continue
		}
		now := time.Now()
		set := bson.M{"ns_state": st.State, "ns_checked_at": now}
		if st.ZoneStatus != "" {
			set["cf_status"] = st.ZoneStatus
		}
		if len(st.CFNameservers) > 0 {
			set["cf_nameservers"] = st.CFNameservers
		}
		_, _ = s.db.Collection(database.ColDNSZones).UpdateOne(ctx,
			bson.M{"_id": z.ID}, bson.M{"$set": set})
		s.syncDomainNameservers(ctx, z.Domain, st.CurrentNameservers)
		checked++
	}
	return checked, cur.Err()
}

func lowerTrimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), ".")))
	}
	sort.Strings(out)
	return out
}

// subsetOf reports whether every element of a is present in b.
func subsetOf(a, b []string) bool {
	set := map[string]struct{}{}
	for _, x := range b {
		set[x] = struct{}{}
	}
	for _, x := range a {
		if _, ok := set[x]; !ok {
			return false
		}
	}
	return true
}

// UpdateWebRecordsForServerIPChange repoints WEB origin A records from oldIP to
// newIP across every Cloudflare-managed zone. This is the Cloudflare side of an
// IP change / server migration. Mail records (mail-A / SPF / MX / DKIM / DMARC)
// are classified and SKIPPED — a web-IP change must never move mail. The
// record's proxied state is preserved. Returns the number of records updated;
// (0, nil) when Cloudflare is disabled so the caller (ReassignServerIP) is
// unaffected on a non-Cloudflare panel. Best-effort per zone — one zone's list
// failure is skipped rather than aborting the whole sweep.
//
// Rollback: this is symmetric — reverting an IP change is simply calling it
// again with (newIP, oldIP), which repoints the same protected web A records
// back. The existing ReassignServerIP already supports that inverse call.
func (s *CloudflareService) UpdateWebRecordsForServerIPChange(ctx context.Context, oldIP, newIP string) (int, error) {
	return s.updateWebRecordsForType(ctx, "A", oldIP, newIP)
}

// UpdateWebAAAARecordsForServerIPChange is the IPv6 sibling of the A-record
// sweep: it repoints WEB origin AAAA records from oldIP6 to newIP6 across every
// Cloudflare-managed zone, protecting mail records exactly the same way. A
// server migration that carries IPv6 must move the AAAA web origins too, or
// dual-stack clients keep resolving the old box over v6. No-op when either
// address is empty (the panel couldn't infer a v6 pair). Called by
// ReassignServerIP alongside the A sweep.
func (s *CloudflareService) UpdateWebAAAARecordsForServerIPChange(ctx context.Context, oldIP6, newIP6 string) (int, error) {
	return s.updateWebRecordsForType(ctx, "AAAA", oldIP6, newIP6)
}

// updateWebRecordsForType is the shared implementation for the A and AAAA
// web-origin sweeps. recordType is "A" or "AAAA"; only records of that type
// whose content equals oldIP are repointed to newIP. Update-only (never
// deletes), mail-protected, proxy/TTL/priority preserved, and best-effort per
// zone so one zone's list failure doesn't abort the sweep.
func (s *CloudflareService) updateWebRecordsForType(ctx context.Context, recordType, oldIP, newIP string) (int, error) {
	oldIP = strings.TrimSpace(oldIP)
	newIP = strings.TrimSpace(newIP)
	recordType = strings.ToUpper(strings.TrimSpace(recordType))
	if oldIP == "" || newIP == "" || oldIP == newIP {
		return 0, nil
	}
	provider, _, err := s.providerFor(ctx)
	if err != nil {
		if err == ErrCloudflareDisabled || err == ErrCloudflareNoToken {
			return 0, nil // no Cloudflare configured — nothing to do
		}
		return 0, err
	}

	cur, err := s.db.Collection(database.ColDNSZones).Find(ctx, bson.M{
		"provider":           "cloudflare",
		"cf_zone_id":         bson.M{"$nin": bson.A{nil, ""}},
		"cloudflare_enabled": bson.M{"$ne": false}, // skip domains disabled for Cloudflare
	})
	if err != nil {
		return 0, err
	}
	var zones []models.DNSZone
	if err := cur.All(ctx, &zones); err != nil {
		return 0, err
	}
	advancedCerts := s.AdvancedCertsOn(ctx)

	updated := 0
	for _, z := range zones {
		recs, err := provider.ListRecords(ctx, z.CloudflareZoneID)
		if err != nil {
			continue // skip this zone; don't abort the whole sweep
		}
		for _, r := range recs {
			if strings.ToUpper(r.Type) != recordType || strings.TrimSpace(r.Content) != oldIP {
				continue
			}
			if isMailRecord(r.Type, r.Name, r.Content, "") {
				continue // PROTECT mail — never move a mail record on a web-IP change
			}
			prox := r.Proxied
			p := cloudflare.RecordParams{
				Type:    r.Type,
				Name:    r.Name,
				Content: newIP,
				TTL:     r.TTL,
				Proxied: &prox,
			}
			// An IP change must never leave a multi-level subdomain orange-clouded
			// when the certificate can't cover it — that's the TLS-handshake break.
			forceUncoverableDNSOnly(&p, z.Domain, advancedCerts)
			if r.Priority != nil {
				pr := *r.Priority
				p.Priority = &pr
			}
			if _, err := provider.UpdateRecord(ctx, z.CloudflareZoneID, r.ID, p); err == nil {
				updated++
			}
		}
	}
	return updated, nil
}
