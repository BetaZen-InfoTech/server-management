// Package services — domain_bulk_refresh.go.
//
// Two operator-facing bulk actions on the WHM/cPanel Domains + SSL
// pages, both shaped as "iterate over every visible row, run the
// per-row action, return a result table":
//
//   1. BulkRefreshRegistration — re-runs WHOIS/RDAP for every domain
//      the caller can see and overwrites the panel's stored
//      registrar / registered_on / expires_on / nameservers fields.
//      Fixes the "I added 50 domains six months ago and the expiry
//      column is out of date" problem in one click.
//
//   2. BulkForceSSL — flips the force_ssl flag (HTTPS-only redirect)
//      on every domain the caller can see. Use when an operator just
//      finished a Let's Encrypt sweep and wants every domain pinned
//      to HTTPS without clicking the per-row toggle 92 times.
//
// Neither is destructive; both are idempotent (re-running with the
// same target set yields the same Mongo state). No OTP gate — the
// matching single-row endpoints already exist without one, and the
// only thing the bulk version changes is the loop.
//
// Concurrency: WHOIS lookups dominate the runtime (1–3 s each over
// the network); we cap parallelism at bulkRefreshWorkers so a
// 100-domain refresh doesn't fan out into 100 simultaneous outbound
// connections that the upstream RDAP server might rate-limit. Force
// SSL touches local nginx config + a postmap — fast — so the same
// worker pool serves both without a separate dial.
package services

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	bulkRefreshWorkers       = 5
	bulkRefreshPerDomainTTL  = 25 * time.Second
	bulkRefreshSafetyDomains = 1000 // refuse > this many in a single call so a typo in `all=true` can't lock the panel for half an hour
)

// ─────────────────────────────────────────────────────────────────────
// Bulk WHOIS refresh
// ─────────────────────────────────────────────────────────────────────

type BulkWhoisRowResult struct {
	ID           string    `json:"id"`
	Domain       string    `json:"domain"`
	Success      bool      `json:"success"`
	Error        string    `json:"error,omitempty"`
	Registrar    string    `json:"registrar,omitempty"`
	RegisteredOn string    `json:"registered_on,omitempty"`
	ExpiresOn    string    `json:"expires_on,omitempty"`
	Nameservers  []string  `json:"nameservers,omitempty"`
	FetchedAt    time.Time `json:"fetched_at,omitempty"`
}

type BulkWhoisResponse struct {
	TotalRows int                  `json:"total_rows"`
	Successes int                  `json:"successes"`
	Failures  int                  `json:"failures"`
	Items     []BulkWhoisRowResult `json:"items"`
	StartedAt time.Time            `json:"started_at"`
	EndedAt   time.Time            `json:"ended_at"`
}

// BulkRefreshRegistration runs WhoisLookup on every selected domain
// (or every visible domain when `all=true`), persists the result,
// and returns a per-row outcome table the UI renders inline.
//
// Tenant scope flows through CallerScope — vendor callers only see
// (and only mutate) their own domains. The handler doesn't need to
// re-check ownership because the same scope is applied to the
// initial domain-fetch query below.
//
// Failures don't abort the loop; one TLD's RDAP server timing out
// shouldn't waste the rest of the run. Each row carries a
// per-domain `error` field so the operator sees which lookups
// missed and can rerun them after fixing whatever TLD-side problem
// caused it.
func (s *DomainService) BulkRefreshRegistration(ctx context.Context, ids []string, all bool) (*BulkWhoisResponse, error) {
	domains, err := s.resolveBulkTargets(ctx, ids, all)
	if err != nil {
		return nil, err
	}
	res := &BulkWhoisResponse{
		Items:     make([]BulkWhoisRowResult, 0, len(domains)),
		StartedAt: time.Now().UTC(),
	}
	if len(domains) == 0 {
		res.EndedAt = time.Now().UTC()
		return res, nil
	}

	// Bounded worker pool. We pre-allocate the result slice so each
	// goroutine writes to its own index without a per-row mutex.
	rows := make([]BulkWhoisRowResult, len(domains))
	sem := make(chan struct{}, bulkRefreshWorkers)
	var wg sync.WaitGroup
	for i, d := range domains {
		i, d := i, d
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			rows[i] = s.runWhoisRefreshOne(ctx, d)
		}()
	}
	wg.Wait()

	res.Items = rows
	for _, r := range rows {
		res.TotalRows++
		if r.Success {
			res.Successes++
		} else {
			res.Failures++
		}
	}
	res.EndedAt = time.Now().UTC()
	log.Info().
		Int("total", res.TotalRows).
		Int("ok", res.Successes).
		Int("failed", res.Failures).
		Str("dur", res.EndedAt.Sub(res.StartedAt).Round(time.Millisecond).String()).
		Msg("bulk-whois-refresh completed")
	return res, nil
}

// runWhoisRefreshOne handles one domain end-to-end: per-domain
// timeout + WhoisLookup + persist. Always returns a row even on
// failure so the caller can render it in the result table.
func (s *DomainService) runWhoisRefreshOne(parentCtx context.Context, d models.Domain) BulkWhoisRowResult {
	row := BulkWhoisRowResult{ID: d.ID.Hex(), Domain: d.Domain}
	ctx, cancel := context.WithTimeout(parentCtx, bulkRefreshPerDomainTTL)
	defer cancel()
	res, err := s.WhoisLookup(ctx, d.Domain)
	if err != nil || res == nil {
		row.Success = false
		if err != nil {
			row.Error = err.Error()
		} else {
			row.Error = "whois returned no data"
		}
		return row
	}
	row.Registrar = res.Registrar
	row.RegisteredOn = res.RegisteredOn
	row.ExpiresOn = res.ExpiresOn
	row.Nameservers = res.Nameservers
	row.FetchedAt = res.FetchedAt
	row.Success = true

	// Persist via UpdateRegistration so future per-row "Edit
	// registration" reads see the canonical values. A "blank"
	// returned field is preserved as blank — we don't second-guess
	// the registry.
	registeredPtr := parseFlexibleDate(res.RegisteredOn)
	expiresPtr := parseFlexibleDate(res.ExpiresOn)
	col := s.db.Collection(database.ColDomains)
	set := bson.M{
		"updated_at":        time.Now(),
		"whois_synced_at":   time.Now(),
	}
	if strings.TrimSpace(res.Registrar) != "" {
		set["registrar"] = strings.TrimSpace(res.Registrar)
	}
	if registeredPtr != nil {
		set["registered_on"] = registeredPtr
	}
	if expiresPtr != nil {
		set["expires_on"] = expiresPtr
	}
	if len(res.Nameservers) > 0 {
		set["nameservers"] = res.Nameservers
	}
	if _, uerr := col.UpdateByID(ctx, d.ID, bson.M{"$set": set}); uerr != nil {
		// WHOIS succeeded but Mongo write failed — surface the
		// partial success so the operator knows the lookup was
		// fine and can investigate the DB write separately.
		row.Success = false
		row.Error = "whois ok but Mongo write failed: " + uerr.Error()
	}
	return row
}

// ─────────────────────────────────────────────────────────────────────
// Bulk Force HTTPS
// ─────────────────────────────────────────────────────────────────────

type BulkForceSSLRowResult struct {
	ID      string `json:"id"`
	Domain  string `json:"domain"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Skipped string `json:"skipped,omitempty"` // populated when domain has no live cert
}

type BulkForceSSLResponse struct {
	TotalRows int                     `json:"total_rows"`
	Successes int                     `json:"successes"`
	Failures  int                     `json:"failures"`
	Skipped   int                     `json:"skipped"`
	Enable    bool                    `json:"enable"`
	Items     []BulkForceSSLRowResult `json:"items"`
	StartedAt time.Time               `json:"started_at"`
	EndedAt   time.Time               `json:"ended_at"`
}

// BulkForceSSL flips the force_ssl flag (nginx HTTPS-only redirect)
// on every selected domain (or every visible domain when `all=true`).
// Domains without a live Let's Encrypt cert are SKIPPED, not failed
// — turning on Force HTTPS for an HTTP-only domain would 502 it.
// The skipped row carries `skipped: "no SSL cert"` so the operator
// can issue + reissue + re-run.
//
// Tenant scope: vendor callers only see + mutate their own domains
// (same CallerScope guard as the WHOIS refresh).
func (s *DomainService) BulkForceSSL(ctx context.Context, ids []string, all bool, enable bool, ssl *SSLService) (*BulkForceSSLResponse, error) {
	if ssl == nil {
		return nil, fmt.Errorf("SSL service not wired")
	}
	domains, err := s.resolveBulkTargets(ctx, ids, all)
	if err != nil {
		return nil, err
	}
	res := &BulkForceSSLResponse{
		Items:     make([]BulkForceSSLRowResult, 0, len(domains)),
		Enable:    enable,
		StartedAt: time.Now().UTC(),
	}
	if len(domains) == 0 {
		res.EndedAt = time.Now().UTC()
		return res, nil
	}

	// Same bounded worker pool as the WHOIS path. nginx -t + reload
	// is cheap; the cap mainly prevents 92 simultaneous reloads
	// from racing each other and one stale config killing the
	// rest.
	rows := make([]BulkForceSSLRowResult, len(domains))
	sem := make(chan struct{}, bulkRefreshWorkers)
	var wg sync.WaitGroup
	for i, d := range domains {
		i, d := i, d
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			rows[i] = s.runForceSSLOne(ctx, d, enable, ssl)
		}()
	}
	wg.Wait()

	res.Items = rows
	for _, r := range rows {
		res.TotalRows++
		switch {
		case r.Skipped != "":
			res.Skipped++
		case r.Success:
			res.Successes++
		default:
			res.Failures++
		}
	}
	res.EndedAt = time.Now().UTC()
	log.Info().
		Bool("enable", enable).
		Int("total", res.TotalRows).
		Int("ok", res.Successes).
		Int("skipped", res.Skipped).
		Int("failed", res.Failures).
		Str("dur", res.EndedAt.Sub(res.StartedAt).Round(time.Millisecond).String()).
		Msg("bulk-force-ssl completed")
	return res, nil
}

func (s *DomainService) runForceSSLOne(ctx context.Context, d models.Domain, enable bool, ssl *SSLService) BulkForceSSLRowResult {
	row := BulkForceSSLRowResult{ID: d.ID.Hex(), Domain: d.Domain}
	// Refuse to enable HTTPS-only redirect on a domain without an
	// SSL cert — would 502 the site. Disabling Force HTTPS is
	// always allowed (revert path). When the column ssl_active
	// isn't true we count it as Skipped so the operator sees the
	// row in the result table without it polluting the failure
	// counter.
	if enable && !d.SSLActive {
		row.Skipped = "no SSL cert — issue / reissue first"
		return row
	}
	if err := ssl.ForceSSL(ctx, d.Domain, enable); err != nil {
		row.Success = false
		row.Error = err.Error()
		return row
	}
	row.Success = true
	return row
}

// ─────────────────────────────────────────────────────────────────────
// Shared target resolver
// ─────────────────────────────────────────────────────────────────────

// resolveBulkTargets centralises the "selected ids" vs "all visible"
// logic so both bulk methods share the same tenant scoping + 1000-row
// safety cap. Returns the resolved Domain rows ready for per-row
// processing.
func (s *DomainService) resolveBulkTargets(ctx context.Context, ids []string, all bool) ([]models.Domain, error) {
	col := s.db.Collection(database.ColDomains)
	filter := bson.M{}
	if !all {
		if len(ids) == 0 {
			return []models.Domain{}, nil
		}
		oids := make([]primitive.ObjectID, 0, len(ids))
		for _, id := range ids {
			oid, err := primitive.ObjectIDFromHex(strings.TrimSpace(id))
			if err == nil {
				oids = append(oids, oid)
			}
		}
		if len(oids) == 0 {
			return []models.Domain{}, nil
		}
		filter["_id"] = bson.M{"$in": oids}
	}
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyTo(ctx, s.db, "user", filter)
	}
	cur, err := col.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []models.Domain
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	if len(out) > bulkRefreshSafetyDomains {
		return nil, fmt.Errorf("too many domains in a single bulk run (got %d, cap %d) — narrow the selection or run in batches", len(out), bulkRefreshSafetyDomains)
	}
	return out, nil
}
