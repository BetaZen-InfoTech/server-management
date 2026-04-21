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
	"go.mongodb.org/mongo-driver/bson"
)

// PreflightCheck is one row of the per-probe verdict list returned to
// the UI. The Add Domain modal renders these as a vertical list of
// pass/fail rows so the operator can see at a glance which DNS / WHOIS
// / firewall step is the blocker.
type PreflightCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// PreflightResult is the full payload the preflight endpoint returns.
// All fields are JSON-shaped for direct passthrough to the frontend.
type PreflightResult struct {
	Domain            string           `json:"domain"`
	Registrar         string           `json:"registrar"`
	RegisteredOn      string           `json:"registered_on"`
	ExpiresOn         string           `json:"expires_on"`
	Nameservers       []string         `json:"nameservers"`
	ResolvedIPs       []string         `json:"resolved_ips"`
	MXRecords         []string         `json:"mx_records"`
	DomainType        string           `json:"domain_type"`
	ParentDomain      string           `json:"parent_domain,omitempty"`
	ServerIP          string           `json:"server_ip"`
	IPMatchesServer   bool             `json:"ip_matches_server"`
	FirewallOpenPorts []int            `json:"firewall_open_ports"`
	FirewallOK        bool             `json:"firewall_ok"`
	Checks            []PreflightCheck `json:"checks"`
	CheckedAt         time.Time        `json:"checked_at"`
}

// ufwPortRe matches a UFW status line for a specific tcp port that's
// allowed inbound. Sample lines from `ufw status verbose`:
//
//	80/tcp                     ALLOW IN    Anywhere
//	443/tcp (v6)               ALLOW IN    Anywhere (v6)
var ufwPortRe = regexp.MustCompile(`(?m)^\s*(\d+)/tcp(?:\s*\([^)]+\))?\s+ALLOW`)

// ufwInactiveRe matches `Status: inactive` so we can distinguish "no
// firewall enforced" from "firewall up and blocking 80/443" — the
// former is treated as OK (nothing to block traffic) while the latter
// is a real preflight failure.
var ufwInactiveRe = regexp.MustCompile(`(?mi)^\s*Status:\s+inactive`)

// RunPreflight runs every domain probe in parallel and returns a fully
// populated PreflightResult. Per-probe failures degrade individual
// checks but never error the whole call — the operator should always
// get back a complete checklist so they know what to fix.
//
// Total wall time is bounded by the slowest probe (~5s WHOIS); DNS
// probes are 3s each and run concurrently.
func (s *DomainService) RunPreflight(ctx context.Context, domain string) *PreflightResult {
	domain = strings.ToLower(strings.TrimSpace(domain))
	res := &PreflightResult{
		Domain:    domain,
		ServerIP:  s.cfg.ServerIP,
		CheckedAt: time.Now().UTC(),
		Checks:    []PreflightCheck{},
	}
	if domain == "" {
		res.Checks = append(res.Checks, PreflightCheck{Name: "domain", OK: false, Detail: "domain is required"})
		return res
	}

	var (
		wg sync.WaitGroup
		mu sync.Mutex

		whoisOK     bool
		whoisDetail string

		aIPs     []string
		aErr     error
		nsHosts  []string
		nsSource string // "self" or "apex:<name>"
		nsErr    error
		mxHosts  []string
		mxErr    error

		ufwRaw string
		ufwErr error
	)

	// WHOIS probe — 5s budget. Reuses the existing WhoisLookup helper
	// so any TLD-specific parsing fixes there flow through automatically.
	wg.Add(1)
	go func() {
		defer wg.Done()
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		w, err := s.WhoisLookup(wctx, domain)
		mu.Lock()
		defer mu.Unlock()
		if err != nil || w == nil {
			whoisOK = false
			if err != nil {
				whoisDetail = err.Error()
			} else {
				whoisDetail = "whois lookup returned no data"
			}
			return
		}
		res.Registrar = w.Registrar
		// WHOIS dates come back in whatever shape the registry emits.
		// Normalise to YYYY-MM-DD for the response so the frontend
		// doesn't need its own parser.
		if t := parseFlexibleDate(w.RegisteredOn); t != nil {
			res.RegisteredOn = t.Format("2006-01-02")
		}
		if t := parseFlexibleDate(w.ExpiresOn); t != nil {
			res.ExpiresOn = t.Format("2006-01-02")
		}
		whoisOK = w.Registrar != "" || res.RegisteredOn != "" || res.ExpiresOn != ""
		if whoisOK {
			whoisDetail = w.Registrar
			if whoisDetail == "" {
				whoisDetail = "whois data retrieved"
			}
		} else {
			whoisDetail = "no registrar / dates returned (unusual TLD or whois server down)"
		}
	}()

	// DNS A probe — 3s budget.
	wg.Add(1)
	go func() {
		defer wg.Done()
		dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var r net.Resolver
		ips, err := r.LookupHost(dctx, domain)
		mu.Lock()
		defer mu.Unlock()
		aIPs = ips
		aErr = err
	}()

	// DNS NS probe — 3s. NS records live at the zone apex; for a
	// subdomain like app.example.com the lookup typically returns
	// nothing, so we fall back to the apex (last 2 labels) so the UI
	// shows a real nameserver list.
	wg.Add(1)
	go func() {
		defer wg.Done()
		dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var r net.Resolver
		nss, err := r.LookupNS(dctx, domain)
		hosts := nsHostsToStrings(nss)
		source := "self"
		if len(hosts) == 0 {
			parts := strings.Split(domain, ".")
			if len(parts) > 2 {
				apex := strings.Join(parts[len(parts)-2:], ".")
				if nss2, err2 := r.LookupNS(dctx, apex); err2 == nil {
					hosts = nsHostsToStrings(nss2)
					if len(hosts) > 0 {
						source = "apex:" + apex
						err = nil
					}
				}
			}
		}
		mu.Lock()
		defer mu.Unlock()
		nsHosts = hosts
		nsSource = source
		nsErr = err
	}()

	// DNS MX probe — 3s.
	wg.Add(1)
	go func() {
		defer wg.Done()
		dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var r net.Resolver
		mxs, err := r.LookupMX(dctx, domain)
		hosts := make([]string, 0, len(mxs))
		for _, m := range mxs {
			if m == nil {
				continue
			}
			hosts = append(hosts, strings.TrimSuffix(m.Host, "."))
		}
		mu.Lock()
		defer mu.Unlock()
		mxHosts = hosts
		mxErr = err
	}()

	// Firewall probe — 3s. Reads UFW status and parses out the
	// ALLOW lines for tcp ports.
	wg.Add(1)
	go func() {
		defer wg.Done()
		fctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		raw, err := agent.GetUFWStatus(fctx)
		mu.Lock()
		defer mu.Unlock()
		ufwRaw = raw
		ufwErr = err
	}()

	wg.Wait()

	// Stamp results onto the response and build the per-check rows.
	res.Nameservers = nsHosts
	res.ResolvedIPs = aIPs
	res.MXRecords = mxHosts

	// WHOIS row.
	res.Checks = append(res.Checks, PreflightCheck{Name: "whois", OK: whoisOK, Detail: whoisDetail})

	// DNS A row.
	if aErr != nil || len(aIPs) == 0 {
		detail := "no A record found"
		if aErr != nil {
			detail = aErr.Error()
		}
		res.Checks = append(res.Checks, PreflightCheck{Name: "dns_a", OK: false, Detail: detail})
	} else {
		res.Checks = append(res.Checks, PreflightCheck{Name: "dns_a", OK: true, Detail: strings.Join(aIPs, ", ")})
	}

	// DNS NS row.
	if (nsErr != nil && len(nsHosts) == 0) || len(nsHosts) == 0 {
		detail := "no NS records found"
		if nsErr != nil {
			detail = nsErr.Error()
		}
		res.Checks = append(res.Checks, PreflightCheck{Name: "dns_ns", OK: false, Detail: detail})
	} else {
		detail := strings.Join(nsHosts, ", ")
		if strings.HasPrefix(nsSource, "apex:") {
			detail += " (from " + strings.TrimPrefix(nsSource, "apex:") + ")"
		}
		res.Checks = append(res.Checks, PreflightCheck{Name: "dns_ns", OK: true, Detail: detail})
	}

	// DNS MX row. Empty MX is valid (domain may not be running mail) so
	// we mark it OK with an explanatory detail rather than failing.
	if mxErr != nil && len(mxHosts) == 0 {
		res.Checks = append(res.Checks, PreflightCheck{Name: "dns_mx", OK: false, Detail: mxErr.Error()})
	} else if len(mxHosts) == 0 {
		res.Checks = append(res.Checks, PreflightCheck{Name: "dns_mx", OK: true, Detail: "no MX records (mail not configured)"})
	} else {
		res.Checks = append(res.Checks, PreflightCheck{Name: "dns_mx", OK: true, Detail: strings.Join(mxHosts, ", ")})
	}

	// Domain-type classification — DB-driven so we honour what the
	// operator has already set up. "parked" is intentionally not
	// detected: distinguishing parked from primary cheaply requires
	// HTTP probes we don't want to do during a sync preflight.
	dtype, parent := s.classifyDomainType(ctx, domain)
	res.DomainType = dtype
	res.ParentDomain = parent
	dtDetail := dtype
	if dtype == "subdomain" && parent != "" {
		dtDetail = "subdomain of " + parent
	}
	res.Checks = append(res.Checks, PreflightCheck{Name: "domain_type", OK: true, Detail: dtDetail})

	// IP-match row — at least one resolved IP must equal the server IP.
	if res.ServerIP == "" {
		res.Checks = append(res.Checks, PreflightCheck{Name: "ip_match", OK: false, Detail: "server IP not configured on the panel"})
	} else if len(aIPs) == 0 {
		res.Checks = append(res.Checks, PreflightCheck{Name: "ip_match", OK: false, Detail: "no A record to compare against server IP " + res.ServerIP})
	} else {
		matched := false
		for _, ip := range aIPs {
			if ip == res.ServerIP {
				matched = true
				break
			}
		}
		res.IPMatchesServer = matched
		if matched {
			res.Checks = append(res.Checks, PreflightCheck{Name: "ip_match", OK: true, Detail: "matches server IP " + res.ServerIP})
		} else {
			res.Checks = append(res.Checks, PreflightCheck{Name: "ip_match", OK: false, Detail: fmt.Sprintf("A record %s does not match server IP %s", strings.Join(aIPs, ", "), res.ServerIP)})
		}
	}

	// Firewall row.
	if ufwErr != nil {
		res.FirewallOK = true
		res.FirewallOpenPorts = []int{}
		res.Checks = append(res.Checks, PreflightCheck{Name: "firewall", OK: true, Detail: "ufw status unavailable: " + ufwErr.Error()})
	} else if ufwInactiveRe.MatchString(ufwRaw) {
		res.FirewallOK = true
		res.FirewallOpenPorts = []int{}
		res.Checks = append(res.Checks, PreflightCheck{Name: "firewall", OK: true, Detail: "ufw inactive (no firewall enforced)"})
	} else {
		ports := parseUFWAllowedPorts(ufwRaw)
		res.FirewallOpenPorts = ports
		has80, has443 := false, false
		for _, p := range ports {
			if p == 80 {
				has80 = true
			}
			if p == 443 {
				has443 = true
			}
		}
		res.FirewallOK = has80 && has443
		var open []string
		if has80 {
			open = append(open, "80")
		}
		if has443 {
			open = append(open, "443")
		}
		var missing []string
		if !has80 {
			missing = append(missing, "80")
		}
		if !has443 {
			missing = append(missing, "443")
		}
		var detail string
		if res.FirewallOK {
			detail = "ports " + strings.Join(open, ", ") + " open"
		} else {
			detail = "missing required ports: " + strings.Join(missing, ", ")
		}
		res.Checks = append(res.Checks, PreflightCheck{Name: "firewall", OK: res.FirewallOK, Detail: detail})
	}

	if res.Nameservers == nil {
		res.Nameservers = []string{}
	}
	if res.ResolvedIPs == nil {
		res.ResolvedIPs = []string{}
	}
	if res.MXRecords == nil {
		res.MXRecords = []string{}
	}
	if res.FirewallOpenPorts == nil {
		res.FirewallOpenPorts = []int{}
	}
	return res
}

// nsHostsToStrings flattens net.NS pointers into a clean lower-case
// host list with the trailing dot stripped (so the UI doesn't show
// "ns1.cloudflare.com." with a stray dot).
func nsHostsToStrings(nss []*net.NS) []string {
	out := make([]string, 0, len(nss))
	for _, n := range nss {
		if n == nil {
			continue
		}
		h := strings.ToLower(strings.TrimSuffix(n.Host, "."))
		if h != "" {
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

// parseUFWAllowedPorts pulls the integer port list out of `ufw status
// verbose`. Duplicates (v4 + v6 lines for the same port) are merged.
func parseUFWAllowedPorts(raw string) []int {
	matches := ufwPortRe.FindAllStringSubmatch(raw, -1)
	seen := map[int]bool{}
	var ports []int
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		var p int
		if _, err := fmt.Sscanf(m[1], "%d", &p); err != nil {
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// classifyDomainType returns ("subdomain", parent), ("addon", "") or
// ("primary", ""). Parked detection is deliberately skipped.
//
// - subdomain: another row in domains owns a parent zone of this name.
// - addon: this is a 2-label apex AND the same Linux user already owns
//   at least one *other* 2-label apex.
// - primary: anything else.
func (s *DomainService) classifyDomainType(ctx context.Context, domain string) (string, string) {
	if parent := findParentDomain(ctx, s.db, domain); parent != "" {
		return "subdomain", parent
	}
	parts := strings.Split(domain, ".")
	if len(parts) == 2 {
		col := s.db.Collection(database.ColDomains)
		// Look up the existing row (if any) so we can find its owner.
		var existing struct {
			User string `bson:"user"`
		}
		_ = col.FindOne(ctx, bson.M{"domain": domain}).Decode(&existing)
		if existing.User != "" {
			// Count other 2-label domains under the same user.
			cur, err := col.Find(ctx, bson.M{
				"user":   existing.User,
				"domain": bson.M{"$ne": domain},
			})
			if err == nil {
				defer cur.Close(ctx)
				for cur.Next(ctx) {
					var d struct {
						Domain string `bson:"domain"`
					}
					if err := cur.Decode(&d); err != nil {
						continue
					}
					if strings.Count(d.Domain, ".") == 1 {
						return "addon", ""
					}
				}
			}
		}
	}
	return "primary", ""
}
