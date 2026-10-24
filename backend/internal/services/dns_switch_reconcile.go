package services

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
)

// bootstrapSPFRe matches ONLY the panel-bootstrap SPF shape that setupMailServer
// historically emitted: `v=spf1 ip4:<A.B.C.D>[/NN] <qualifier>all`. A custom SPF
// (includes, multiple mechanisms, a:/mx:, a third-party ip4, extra tokens) never
// matches, so normalizeBootstrapSPF leaves it completely alone.
var bootstrapSPFRe = regexp.MustCompile(`^v=spf1\s+ip4:(\d{1,3}(?:\.\d{1,3}){3})(?:/\d{1,3})?\s+[-~?+]?all$`)

// normalizeBootstrapSPF rewrites a panel-bootstrap SPF record that hard-codes one
// of the panel's OWN historical server IPs into the IP-change-proof canonical
// form `v=spf1 a:<mailHost> ~all`. It is deliberately conservative: it rewrites
// ONLY a record of the exact bootstrap shape whose single ip4 literal is a KNOWN
// panel IP (panelIPs) — so a custom SPF, or one authorizing a genuine third-party
// sender, is never touched. Returns (newValue, true) only when a rewrite applies.
func normalizeBootstrapSPF(value, mailHost string, panelIPs map[string]bool) (string, bool) {
	v := strings.TrimSpace(strings.Trim(strings.TrimSpace(value), "\""))
	m := bootstrapSPFRe.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	if !panelIPs[m[1]] {
		return "", false
	}
	canonical := fmt.Sprintf("v=spf1 a:%s ~all", strings.TrimSuffix(mailHost, "."))
	if v == canonical {
		return "", false
	}
	return canonical, true
}

// panelHistoricalIPs returns the set of IPv4 addresses this panel has ever served
// from — current + previous + every ip_history entry on the stable server-identity
// row — so a reconcile can safely rewrite ONLY the panel's own stale IPs out of
// bootstrap SPF records and never touch a third party's ip4 literal.
func (s *DNSService) panelHistoricalIPs(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	var srv models.Server
	if err := s.db.Collection(database.ColServers).FindOne(ctx, bson.M{}).Decode(&srv); err != nil {
		return out
	}
	for _, ip := range append([]string{srv.CurrentIP, srv.PreviousIP}, ipHistoryIPs(srv.IPHistory)...) {
		if ip = strings.TrimSpace(ip); ip != "" {
			out[ip] = true
		}
	}
	return out
}

func ipHistoryIPs(h []models.IPHistoryEntry) []string {
	out := make([]string, 0, len(h))
	for _, e := range h {
		out = append(out, e.IP)
	}
	return out
}

// SwitchDomainToPowerDNS performs the real Cloudflare→PowerDNS cutover for one
// domain that the old enable/disable toggle only half-did: it makes the panel's
// own PowerDNS the authoritative provider in BOTH the stored state and the
// effective routing, ensures the PowerDNS zone actually exists (rebuilding it
// from Mongo if a migration left it behind), and returns the live nameserver
// delegation status so the UI can tell the operator exactly which nameservers to
// set at the registrar and whether that cutover has landed yet.
//
// The Cloudflare zone (cf_zone_id) is intentionally preserved so the operator
// can switch back without re-connecting. Records are untouched — they already
// live in Mongo + PowerDNS; only the provider flags move.
func (s *DNSService) SwitchDomainToPowerDNS(ctx context.Context, domain string) (*PowerDNSNameserverStatus, error) {
	domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(domain, ".")))
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}

	// Make sure PowerDNS is actually serving this zone before we point the
	// provider flags at it — otherwise the switch would leave the domain with no
	// authoritative server once the registrar nameservers are moved.
	s.ensurePowerDNSZone(ctx, domain)

	// Flip the stored provider to agree with the effective one so every reader
	// (nameserver audit, subdomain inheritance, CF sync gate, UI toggle) treats
	// the domain as PowerDNS from here on.
	_, err := s.db.Collection(database.ColDNSZones).UpdateOne(ctx,
		bson.M{"domain": domain},
		bson.M{"$set": bson.M{
			"provider":           "powerdns",
			"cloudflare_enabled": false,
			"sync_state":         "",
			"updated_at":         time.Now(),
		}},
	)
	if err != nil {
		return nil, err
	}

	// Live delegation status (panel nameservers vs what the registrar points at
	// now). Drives the "point your registrar here / ✓ verified" UI.
	return s.CheckPowerDNSNameservers(ctx, domain)
}

// ensurePowerDNSZone guarantees a PowerDNS zone exists for domain. For the
// common case (migrated CF domains, existing PowerDNS domains) the zone is
// already present and this is a cheap no-op. Only when a migration carried the
// Mongo rows but not the PowerDNS zone does it recreate the zone and replay the
// stored records, so a Cloudflare→PowerDNS switch never cuts over to an empty
// authoritative server.
func (s *DNSService) ensurePowerDNSZone(ctx context.Context, domain string) {
	existing, err := agent.ListAllZones(ctx)
	if err == nil {
		for _, z := range existing {
			if strings.EqualFold(strings.TrimSuffix(z, "."), domain) {
				return // already served
			}
		}
	}

	var zone models.DNSZone
	if err := s.db.Collection(database.ColDNSZones).FindOne(ctx, bson.M{"domain": domain}).Decode(&zone); err != nil {
		return // no zone doc — nothing to rebuild from
	}
	serverIP := strings.TrimSpace(zone.ServerIP)
	if serverIP == "" {
		serverIP = s.panelServerIP()
	}
	ns := zone.Nameservers
	if len(ns) == 0 {
		ns = s.expectedNameserversClean()
	}
	if err := agent.CreateDNSZone(ctx, domain, serverIP, zone.AdminEmail, ns); err != nil {
		return
	}

	// Replay the stored records on top of the fresh zone so it matches Mongo.
	// reconcileRRSet owns all the content formatting (MX priority, trailing
	// dots, sibling collapse) and is idempotent, so call it once per distinct
	// (name, type) group. SOA is panel-owned (set by CreateDNSZone).
	col := s.db.Collection(database.ColDNSRecords)
	cur, err := col.Find(ctx, bson.M{"zone_id": zone.ID})
	if err != nil {
		return
	}
	defer cur.Close(ctx)
	var rows []models.DNSRecord
	if err := cur.All(ctx, &rows); err != nil {
		return
	}
	type key struct{ name, rtype string }
	seen := map[key]bool{}
	for _, r := range rows {
		if strings.EqualFold(r.Type, "SOA") {
			continue
		}
		k := key{normalizeRecordName(r.Name, domain), strings.ToUpper(r.Type)}
		if seen[k] {
			continue
		}
		seen[k] = true
		_ = s.reconcileRRSet(ctx, zone.ID, domain, k.name, k.rtype)
	}
}

// ProviderReconcileChange records one zone whose stored provider was corrected
// to match the live nameserver delegation reality.
type ProviderReconcileChange struct {
	Domain string   `json:"domain"`
	From   string   `json:"from"`
	To     string   `json:"to"`
	LiveNS []string `json:"live_ns,omitempty"`
}

// ProviderReconcileReport summarises a ReconcileProvidersFromReality run.
type ProviderReconcileReport struct {
	Checked    int                       `json:"checked"`
	Changed    int                       `json:"changed"`
	ToPowerDNS int                       `json:"to_powerdns"`
	ToCF       int                       `json:"to_cloudflare"`
	Skipped    int                       `json:"skipped"` // ambiguous / unresolved NS — left untouched
	Changes    []ProviderReconcileChange `json:"changes"`
}

// ReconcileProvidersFromReality corrects dns_zones.provider so it matches where
// each domain's registrar nameservers ACTUALLY point right now. It exists
// because the stored provider is otherwise only ever a copy of source-Mongo
// state (never derived from on-server reality), so a migration — or an operator
// who repointed nameservers at the registrar directly — leaves the panel
// showing the wrong provider ("Cloudflare→PowerDNS does nothing"). It is the
// migration-safe reconciler: run after a transfer, from `bzpanel reconcile`, or
// via the owner endpoint.
//
// Classification is deliberately conservative: a domain is flipped to PowerDNS
// only when at least one of the panel's own nameservers is live in its NS set
// AND none of Cloudflare's are; to Cloudflare only when a Cloudflare nameserver
// is live. A failed lookup or an unrecognised third-party nameserver is SKIPPED,
// never guessed — so it will not steal a domain the operator keeps elsewhere.
func (s *DNSService) ReconcileProvidersFromReality(ctx context.Context) (*ProviderReconcileReport, error) {
	cur, err := s.db.Collection(database.ColDNSZones).Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	var zones []models.DNSZone
	if err := cur.All(ctx, &zones); err != nil {
		return nil, err
	}

	expected := map[string]bool{}
	for _, ns := range s.expectedNameserversClean() {
		expected[ns] = true
	}

	report := &ProviderReconcileReport{}
	var mu sync.Mutex

	jobs := make(chan models.DNSZone)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for z := range jobs {
				change, kind := s.reconcileOneProvider(ctx, z, expected)
				mu.Lock()
				report.Checked++
				switch kind {
				case "powerdns":
					report.Changed++
					report.ToPowerDNS++
					report.Changes = append(report.Changes, *change)
				case "cloudflare":
					report.Changed++
					report.ToCF++
					report.Changes = append(report.Changes, *change)
				case "skip":
					report.Skipped++
				}
				mu.Unlock()
			}
		}()
	}
	for _, z := range zones {
		jobs <- z
	}
	close(jobs)
	wg.Wait()

	sort.Slice(report.Changes, func(i, k int) bool { return report.Changes[i].Domain < report.Changes[k].Domain })
	return report, nil
}

// reconcileOneProvider resolves one zone's live NS and returns the applied
// change (if any). kind ∈ "powerdns" | "cloudflare" | "noop" | "skip".
func (s *DNSService) reconcileOneProvider(ctx context.Context, z models.DNSZone, expected map[string]bool) (*ProviderReconcileChange, string) {
	domain := strings.ToLower(strings.TrimSpace(z.Domain))
	if domain == "" {
		return nil, "skip"
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var resolver net.Resolver
	nss, lookErr := resolver.LookupNS(lookupCtx, domain)
	current := make([]string, 0, len(nss))
	panelHit, cfHit := false, false
	for _, ns := range nss {
		h := strings.ToLower(strings.TrimSuffix(ns.Host, "."))
		current = append(current, h)
		if expected[h] {
			panelHit = true
		}
		if strings.HasSuffix(h, "ns.cloudflare.com") {
			cfHit = true
		}
	}
	sort.Strings(current)

	switch {
	case lookErr == nil && panelHit && !cfHit:
		// Really served by the panel's PowerDNS.
		curPDNS := strings.EqualFold(z.Provider, "powerdns")
		curDisabled := z.CloudflareEnabled != nil && !*z.CloudflareEnabled
		if curPDNS && curDisabled {
			return nil, "noop"
		}
		from := providerLabel(z)
		_, err := s.db.Collection(database.ColDNSZones).UpdateOne(ctx,
			bson.M{"domain": domain},
			bson.M{"$set": bson.M{"provider": "powerdns", "cloudflare_enabled": false, "sync_state": "", "updated_at": time.Now()}},
		)
		if err != nil {
			return nil, "skip"
		}
		return &ProviderReconcileChange{Domain: domain, From: from, To: "powerdns", LiveNS: current}, "powerdns"

	case cfHit:
		// Really served by Cloudflare — only correct a wrong stored provider; do
		// not force-enable the per-domain toggle (the operator may have it off).
		if strings.EqualFold(z.Provider, "cloudflare") {
			return nil, "noop"
		}
		from := providerLabel(z)
		_, err := s.db.Collection(database.ColDNSZones).UpdateOne(ctx,
			bson.M{"domain": domain},
			bson.M{"$set": bson.M{"provider": "cloudflare", "updated_at": time.Now()}},
		)
		if err != nil {
			return nil, "skip"
		}
		return &ProviderReconcileChange{Domain: domain, From: from, To: "cloudflare", LiveNS: current}, "cloudflare"

	default:
		// Lookup failed or NS point to an unrecognised third party — don't guess.
		return nil, "skip"
	}
}

// MailMXChange records one MX rrset repointed from a legacy per-domain mail
// host to the shared Betazen mail host.
type MailMXChange struct {
	Domain string `json:"domain"`
	Name   string `json:"name"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// MailMXReconcileReport summarises a ReconcileMailMX run.
type MailMXReconcileReport struct {
	MXChecked    int            `json:"mx_checked"`
	MXRepointed  int            `json:"mx_repointed"`
	MailARemoved int            `json:"mail_a_removed"`
	SPFChecked   int            `json:"spf_checked"`
	SPFRewritten int            `json:"spf_rewritten"`
	Changes      []MailMXChange `json:"changes"`
}

// ReconcileMailMX repoints every MX record that still uses the legacy
// per-domain mail host (`mail.<fqdn>`) to the single shared Betazen mail host
// (mailHostFQDN), and removes the now-dead `mail.<fqdn>` A record. It is
// CONSERVATIVE: it only ever touches an MX whose value is exactly the panel's
// own legacy `mail.<fqdn>` — a custom external MX (Google Workspace, a third-
// party relay, or an already-shared MX) is left completely alone, so it can
// never break a domain that deliberately points its mail elsewhere.
//
// Used by `bzpanel reconcile` and the post-transfer rehydrate so a migrated
// box's pre-shared-MX domains self-heal onto the shared host.
func (s *DNSService) ReconcileMailMX(ctx context.Context) (*MailMXReconcileReport, error) {
	sharedDotted := s.mailHostFQDN()                     // mailmx.betazeninfotech.com.
	sharedClean := strings.TrimSuffix(sharedDotted, ".") // mailmx.betazeninfotech.com
	rep := &MailMXReconcileReport{}

	// zone_id -> domain
	zcur, err := s.db.Collection(database.ColDNSZones).Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	var zones []models.DNSZone
	if err := zcur.All(ctx, &zones); err != nil {
		return nil, err
	}
	zoneDomain := map[string]string{}
	managed := map[string]bool{}
	for _, z := range zones {
		d := strings.ToLower(strings.TrimSpace(z.Domain))
		zoneDomain[z.ID.Hex()] = d
		if d != "" {
			managed[d] = true
		}
	}
	// hostInManaged reports whether a mail-host FQDN lives inside one of the
	// panel's own managed zones. Used to tell the panel's legacy per-domain mail
	// host (mail.<our-domain>) apart from a custom EXTERNAL MX (Google Workspace,
	// Outlook, a third-party relay), which is never within our zones and is left
	// completely untouched.
	hostInManaged := func(h string) bool {
		for zd := range managed {
			if h == zd || strings.HasSuffix(h, "."+zd) {
				return true
			}
		}
		return false
	}

	cur, err := s.db.Collection(database.ColDNSRecords).Find(ctx, bson.M{"type": "MX"})
	if err != nil {
		return nil, err
	}
	var mxRecs []models.DNSRecord
	if err := cur.All(ctx, &mxRecs); err != nil {
		return nil, err
	}
	rep.MXChecked = len(mxRecs)

	for _, r := range mxRecs {
		dom := zoneDomain[r.ZoneID.Hex()]
		if dom == "" {
			continue
		}
		// Resolve the record's relative name (zone-relative, "@" for apex).
		relName := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(r.Name)), ".")
		if relName == "@" || relName == "" || relName == dom {
			relName = "@"
		} else {
			relName = strings.TrimSuffix(relName, "."+dom)
		}
		// Current MX target, stripped of any "NN " priority prefix + trailing dot.
		val := strings.TrimSuffix(strings.TrimSpace(r.Value), ".")
		if parts := strings.Fields(val); len(parts) == 2 {
			val = strings.TrimSuffix(parts[1], ".")
		}
		lval := strings.ToLower(val)
		// Repoint ONLY the panel's own legacy per-domain mail host: a `mail.<fqdn>`
		// value whose host sits inside a managed zone (apex `mail.<domain>` OR a
		// subdomain pointing at a parent's `mail.<domain>`). Anything already on the
		// shared host, or any custom external MX (never `mail.<our-zone>`), is skipped.
		if lval == "" || lval == sharedClean || !strings.HasPrefix(lval, "mail.") || !hostInManaged(lval) {
			continue
		}

		// Repoint PowerDNS rrset to the shared host.
		if err := agent.ReplaceDNSRecordSet(ctx, dom, relName, "MX", fmt.Sprint(bootstrapTTLFor("MX")), []string{"10 " + sharedDotted}); err != nil {
			continue
		}
		// Collapse the Mongo MX rrset to a single shared record.
		pri := 10
		s.db.Collection(database.ColDNSRecords).DeleteMany(ctx, bson.M{"zone_id": r.ZoneID, "type": "MX", "name": r.Name})
		s.db.Collection(database.ColDNSRecords).InsertOne(ctx, models.DNSRecord{
			ZoneID: r.ZoneID, Type: "MX", Name: relName, Value: sharedDotted, TTL: bootstrapTTLFor("MX"), Priority: &pri, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
		rep.MXRepointed++
		rep.Changes = append(rep.Changes, MailMXChange{Domain: dom, Name: relName, From: val, To: sharedClean})

		// Drop the now-dead per-domain mail host A record. The dead host IS the old
		// MX value (e.g. mail.bizenly.com); its A lives at that host's name inside
		// this zone (mail.bizenly.com -> name "mail" in zone bizenly.com). Derive it
		// from the value, not the MX record's own name, so a subdomain MX that
		// pointed at the PARENT's mail host cleans up the right record. Idempotent
		// (many subdomains share one mail.<parent> A — repeated deletes are no-ops).
		if mailAName := strings.TrimSuffix(lval, "."+dom); mailAName != "" && mailAName != lval {
			_ = agent.DeleteDNSRecord(ctx, dom, mailAName, "A")
			if res, derr := s.db.Collection(database.ColDNSRecords).DeleteMany(ctx, bson.M{"zone_id": r.ZoneID, "type": "A", "name": mailAName}); derr == nil && res != nil && res.DeletedCount > 0 {
				rep.MailARemoved += int(res.DeletedCount)
			}
		}

		// Propagate the MX repoint (+ mail-A removal) to Cloudflare for CF-managed
		// zones. Fire-and-forget; no-op when Cloudflare is off, the domain is
		// disabled/disconnected, or the hook isn't wired.
		if s.cloudflareSync != nil {
			go s.cloudflareSync(dom)
		}
	}

	// SPF normalization — rewrite every panel-bootstrap SPF that still hard-codes
	// one of THIS panel's own historical IPs (ip4:<old-box-IP>) into the
	// IP-change-proof canonical form `v=spf1 a:<mailhost> ~all`. This is the exact
	// migration failure mode the operator hit: after the box moved IP, every
	// domain's SPF kept authorising the dead IP, so outbound mail started failing
	// SPF at the receiver — and on Cloudflare zones the IP sweep deliberately
	// skips mail records, so nothing ever healed it. Gated by the panel's own
	// ip_history: a custom SPF, or one authorising a genuine third-party sender,
	// never matches and is left untouched.
	panelIPs := s.panelHistoricalIPs(ctx)
	if len(panelIPs) > 0 {
		if txtCur, terr := s.db.Collection(database.ColDNSRecords).Find(ctx, bson.M{
			"type":  "TXT",
			"value": bson.M{"$regex": "v=spf1"},
		}); terr == nil {
			var txts []models.DNSRecord
			if err := txtCur.All(ctx, &txts); err == nil {
				for i := range txts {
					r := &txts[i]
					dom := zoneDomain[r.ZoneID.Hex()]
					if dom == "" {
						continue
					}
					rep.SPFChecked++
					newVal, ok := normalizeBootstrapSPF(r.Value, sharedClean, panelIPs)
					if !ok {
						continue
					}
					relName := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(r.Name)), ".")
					if relName == "@" || relName == "" || relName == dom {
						relName = "@"
					} else {
						relName = strings.TrimSuffix(relName, "."+dom)
					}
					ttl := r.TTL
					if ttl <= 0 {
						ttl = bootstrapTTLFor("TXT")
					}
					// PowerDNS (authoritative for powerdns zones; a harmless write for
					// CF zones, which propagate through the sync hook below).
					pdnsVal := formatRecordValueForPDNS(&models.DNSRecord{Type: "TXT", Value: newVal})
					_ = agent.ReplaceDNSRecordSet(ctx, dom, relName, "TXT", fmt.Sprint(ttl), []string{pdnsVal})
					// Mongo — source of truth for the UI AND what the Cloudflare sync reads.
					s.db.Collection(database.ColDNSRecords).UpdateOne(ctx,
						bson.M{"_id": r.ID},
						bson.M{"$set": bson.M{"value": newVal, "updated_at": time.Now()}})
					// Push to Cloudflare for CF-managed zones (fire-and-forget; no-op when
					// Cloudflare is off, the domain is disabled/disconnected, or the hook
					// isn't wired).
					if s.cloudflareSync != nil {
						go s.cloudflareSync(dom)
					}
					rep.SPFRewritten++
					rep.Changes = append(rep.Changes, MailMXChange{Domain: dom, Name: relName, From: strings.Trim(strings.TrimSpace(r.Value), "\""), To: newVal})
				}
			}
		}
	}

	sort.Slice(rep.Changes, func(i, k int) bool { return rep.Changes[i].Domain < rep.Changes[k].Domain })
	return rep, nil
}

func providerLabel(z models.DNSZone) string {
	p := strings.ToLower(strings.TrimSpace(z.Provider))
	if p == "" {
		p = "powerdns"
	}
	if z.CloudflareEnabled != nil && !*z.CloudflareEnabled {
		return p + " (cf-disabled)"
	}
	return p
}
