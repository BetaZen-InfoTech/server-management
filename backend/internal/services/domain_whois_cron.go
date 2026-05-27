// Package services — domain_whois_cron.go is the daily WHOIS / RDAP
// refresh job that keeps every panel-managed apex domain's registrar /
// purchased-on / expires-on / nameservers up to date without operator
// action.
//
// Why a dedicated cron alongside the existing expiry-notification
// sweep (notifier_service.go RunDomainExpirySweep):
//
//   The notification sweep walks `domains.expires_on` and emails the
//   vendor when the date is N days out. If the operator never re-
//   ran WHOIS, that date is whatever they typed at create-time —
//   often months stale. A domain that renewed at the registrar 30
//   days ago would still trigger a "5 days left" email today, OR
//   silently miss the warning entirely if `expires_on` was never set.
//
//   This cron fixes the data first (re-pulls WHOIS for every apex
//   domain), so the notification sweep that runs immediately after
//   it operates on the canonical registrar values.
//
// Apex-only filter — domains.collection holds both apex rows
// (example.com) and subdomain rows (app.example.com). Running WHOIS
// on every row would:
//
//   * waste RDAP quota — the parent registrar's RDAP server returns
//     the SAME data for example.com and app.example.com because the
//     subdomain isn't a separate registration
//   * trip rate-limits — a 100-row panel with one apex + 99
//     subdomains would fire 100 outbound calls instead of 1
//   * confuse the registrar field — for some TLDs the RDAP server
//     refuses subdomain queries entirely, surfacing as a per-row
//     error in the dashboard
//
// So we walk only the apex rows. "Apex" here = no shorter form of
// the domain exists in the panel's domains collection. This is a
// purely topological check (no Public Suffix List dependency) and
// matches the operator's mental model: if they added example.com
// AND app.example.com, the panel knows app.example.com is "under"
// example.com and only WHOIS-refreshes the apex.
package services

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
)

// DailyWhoisRefreshResult is the per-run outcome the caller (main.go's
// boot-time + 24h ticker) logs. Total / Apex / Refreshed / Failed /
// Skipped fields drive a single info-line summary so the operator can
// scrape journalctl for the daily heartbeat.
type DailyWhoisRefreshResult struct {
	// Total is every domain row scanned (apex + subdomain).
	Total int
	// Apex is the count of rows that passed the apex filter — the
	// upper bound on how many RDAP calls we'd make in a healthy run.
	Apex int
	// Refreshed is rows where WHOIS returned data AND the Mongo
	// write succeeded.
	Refreshed int
	// Failed is rows where WHOIS errored OR Mongo write failed.
	// Surfaces in the next operator-triggered "Refresh all" so a
	// transient RDAP outage doesn't permanently desync the panel.
	Failed int
	// SkippedSubdomain is the count of rows the apex filter dropped
	// — purely a telemetry number, no action needed.
	SkippedSubdomain int
	// Started / Ended drive the wall-clock duration in the log line.
	Started time.Time
	Ended   time.Time
}

// RunDailyWhoisRefresh is the entry point the daily cron in main.go
// calls. Walks every Domain row, filters to apex domains, and runs
// the existing per-domain WHOIS pipeline (runWhoisRefreshOne in
// domain_bulk_refresh.go) on each via a bounded worker pool.
//
// Reuses bulkRefreshWorkers (5) so the parallelism matches the
// operator-triggered "Refresh all" path — same RDAP load profile,
// same per-domain timeout (bulkRefreshPerDomainTTL = 25s).
//
// Returns a result struct even on partial failure; the only error
// path is a fatal mongo cursor failure (the caller treats this as
// "skip this tick, retry tomorrow").
func (s *DomainService) RunDailyWhoisRefresh(ctx context.Context) (*DailyWhoisRefreshResult, error) {
	res := &DailyWhoisRefreshResult{Started: time.Now().UTC()}
	col := s.db.Collection(database.ColDomains)

	// Pull every domain row — we need ALL names in the panel to
	// compute the apex filter (the filter checks whether each row's
	// shorter forms exist in the same collection). A pre-filtered
	// Find that returned only apex rows would have to do the same
	// O(rows²) check inside the query, which Mongo isn't great at;
	// streaming + in-process filtering is simpler and well within
	// memory for the panel's scale (panels typically run < 10k
	// domains; even at 100k the slice of strings is ~5 MB).
	cur, err := col.Find(ctx, bson.M{})
	if err != nil {
		res.Ended = time.Now().UTC()
		return res, err
	}
	defer cur.Close(ctx)

	var all []models.Domain
	if err := cur.All(ctx, &all); err != nil {
		res.Ended = time.Now().UTC()
		return res, err
	}
	res.Total = len(all)

	// Build the apex name set in one pass: every row's domain
	// lowercased + trimmed. The set membership check inside
	// IsApexDomain becomes O(1) instead of an O(n²) re-walk.
	known := make(map[string]bool, len(all))
	for _, d := range all {
		known[strings.ToLower(strings.TrimSpace(d.Domain))] = true
	}

	// Filter to apex domains in a separate pass — we don't want to
	// start WHOIS calls while still iterating the Mongo cursor.
	apex := make([]models.Domain, 0, len(all))
	for _, d := range all {
		if IsApexDomain(d.Domain, known) {
			apex = append(apex, d)
		} else {
			res.SkippedSubdomain++
		}
	}
	res.Apex = len(apex)

	if len(apex) == 0 {
		res.Ended = time.Now().UTC()
		log.Info().
			Int("total", res.Total).
			Int("apex", 0).
			Int("skipped_subdomain", res.SkippedSubdomain).
			Msg("daily whois refresh: no apex domains to refresh")
		return res, nil
	}

	// Bounded worker pool — same shape as BulkRefreshRegistration.
	// We don't reuse that function because the bulk endpoint expects
	// a caller-scoped fetch + returns a UI-shaped row list; the cron
	// is unscoped (sees every tenant's domains) and only needs the
	// count summary.
	sem := make(chan struct{}, bulkRefreshWorkers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, d := range apex {
		d := d
		// Respect parent-context cancel: if the surrounding 10-min
		// timeout fires (configured at the caller in main.go), abort
		// the loop cleanly instead of queueing more goroutines that
		// would immediately exit on their own context check.
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			row := s.runWhoisRefreshOne(ctx, d)
			mu.Lock()
			if row.Success {
				res.Refreshed++
			} else {
				res.Failed++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	res.Ended = time.Now().UTC()
	log.Info().
		Int("total", res.Total).
		Int("apex", res.Apex).
		Int("refreshed", res.Refreshed).
		Int("failed", res.Failed).
		Int("skipped_subdomain", res.SkippedSubdomain).
		Str("dur", res.Ended.Sub(res.Started).Round(time.Millisecond).String()).
		Msg("daily whois refresh completed")
	return res, nil
}

// IsApexDomain reports whether `domain` is an apex (registered) name
// in the context of the panel's domains collection — equivalently,
// whether no SHORTER form of `domain` exists in `known`.
//
// `known` is a lower-cased set of every panel-managed domain name;
// the caller (RunDailyWhoisRefresh) builds it once per sweep so this
// check is O(labels-in-domain) per call instead of an O(N) Mongo
// round-trip.
//
// Examples (known = {example.com, app.example.com, billing.acme.co.uk, acme.co.uk}):
//
//   IsApexDomain("example.com",       known) → true   (no shorter form)
//   IsApexDomain("app.example.com",   known) → false  (example.com is shorter)
//   IsApexDomain("acme.co.uk",        known) → true   (no shorter form)
//   IsApexDomain("billing.acme.co.uk",known) → false  (acme.co.uk is shorter)
//
// Note this is intentionally PSL-free: the panel's apex check is
// purely "does the operator have a parent registered?". A panel that
// only manages billing.acme.co.uk (with no acme.co.uk row) treats
// billing.acme.co.uk AS the apex and runs WHOIS on it directly — the
// RDAP/whois fallback handles ccTLD nesting at the protocol layer,
// so we don't need to second-guess it here.
//
// Pure function so the matrix can be unit-tested without a Mongo
// handle.
func IsApexDomain(domain string, known map[string]bool) bool {
	d := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(domain), ".")))
	if d == "" {
		return false
	}
	parts := strings.Split(d, ".")
	// A single-label string isn't a valid DNS name; treat as non-apex
	// so the cron skips it. Should never occur for a real Domain row
	// (the create validator rejects them) but defensive.
	if len(parts) < 2 {
		return false
	}
	// Walk every PROPER suffix (i = 1..len-1) and check `known`. We
	// skip i = 0 (that's d itself) and any all-empty suffix. The
	// first hit means a shorter ancestor is in the panel; d is a
	// subdomain.
	for i := 1; i < len(parts); i++ {
		suffix := strings.Join(parts[i:], ".")
		if suffix == "" || suffix == d {
			continue
		}
		if known[suffix] {
			return false
		}
	}
	return true
}
