// Package services — ssl_bulk_job_service.go is the live-progress
// variant of the bulk SSL issuance flow. Replaces the synchronous
// IssueLetsEncryptBulk's "POST and wait 10 minutes" UX with a
// job-id + poll model:
//
//   1. StartBulkIssueJob writes an SSLBulkJob row to Mongo (Status =
//      queued, Items pre-populated with the de-duped domain list)
//      and returns the job ID immediately.
//   2. A detached goroutine flips Status → running and walks the
//      Items list. For each domain it stamps CurrentDomain, runs
//      the existing per-domain IssueLetsEncrypt pipeline, and
//      writes the row outcome back to the Items array in Mongo. The
//      operator's frontend polls GetBulkIssueJob every ~1.5s and
//      sees the table fill in live.
//   3. When the loop finishes (or the operator hits Cancel and
//      CancelRequested flips) the goroutine stamps FinishedAt +
//      Status = completed / cancelled.
//
// The job DOC is the source of truth — the goroutine doesn't hold
// the result in memory beyond the next-row scope. A server restart
// mid-job is recoverable (see RecoverStaleRunningJobs in the same
// file) and a client refresh just re-polls the same job ID.
//
// Why a detached goroutine instead of the request's context:
//
//   The HTTP request's ctx dies when the client disconnects (closes
//   the modal, hits Cancel, or the browser hits its idle timeout).
//   For a 27-domain run that can be 5-15 minutes, ANY of those
//   would kill an in-flight certbot — leaving a half-written cert
//   directory and a job with no clear outcome. The detached
//   context.Background() with a hard 45-minute cap keeps the loop
//   honest even when the operator closes the laptop.
package services

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// bulkSSLJobMaxDuration caps how long the detached goroutine will
// run before cancelling itself. 45 minutes accommodates the worst
// realistic case (60 wildcard certs on a slow DNS provider's
// propagation) without leaving a forever-stuck "running" row if
// something pathological happens.
const bulkSSLJobMaxDuration = 45 * time.Minute

// staleRunningCutoff is how long a "running" row must have gone
// without a progress write before RecoverStaleRunningJobs marks it
// failed at server boot. The loop writes after every domain (one
// every few seconds at most), so 10 minutes of silence means the
// goroutine is dead.
const staleRunningCutoff = 10 * time.Minute

// StartBulkIssueJob creates the job doc + starts the background
// loop, returning the job ID for the caller's poll URL. Validation
// (request shape, min 1 domain) is the handler's responsibility;
// this layer trusts req and treats per-domain failures as ROW
// outcomes, not job aborts.
//
// ownerUserID + tenantID are stamped onto the doc so poll requests
// can enforce tenant scope without re-deriving the caller's scope
// on every fetch.
func (s *SSLService) StartBulkIssueJob(ctx context.Context, ownerUserID, tenantID primitive.ObjectID, req *models.IssueLetsEncryptBulkRequest) (*models.SSLBulkJob, error) {
	// De-duplicate while preserving order — operators occasionally
	// double-tick the same domain and we'd otherwise burn a Let's
	// Encrypt rate-limit slot twice.
	seen := make(map[string]struct{}, len(req.Domains))
	domains := make([]string, 0, len(req.Domains))
	for _, raw := range req.Domains {
		d := strings.TrimSpace(raw)
		if d == "" {
			continue
		}
		if _, dup := seen[d]; dup {
			continue
		}
		seen[d] = struct{}{}
		domains = append(domains, d)
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("no domains supplied")
	}

	// Pre-populate the Items array with one Pending row per domain.
	// The UI renders this list immediately so the operator sees the
	// full table before the first certbot call even starts.
	items := make([]models.IssueLetsEncryptBulkItem, len(domains))
	for i, d := range domains {
		items[i] = models.IssueLetsEncryptBulkItem{Domain: d}
	}

	now := time.Now()
	job := models.SSLBulkJob{
		OwnerUserID:     ownerUserID,
		TenantID:        tenantID,
		Status:          models.SSLBulkJobStatusQueued,
		Total:           len(domains),
		Items:           items,
		Wildcard:        req.Wildcard,
		Reissue:         req.Reissue,
		CancelRequested: false,
		StartedAt:       now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	res, err := s.db.Collection(database.ColSSLBulkJobs).InsertOne(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("create ssl bulk job: %w", err)
	}
	job.ID = res.InsertedID.(primitive.ObjectID)

	// Capture the caller's tenant scope so the per-domain
	// AssertOwnsDomain check still works inside the detached
	// goroutine (the original request context is gone the moment
	// the handler returns). A nil scope (vendor_owner) carries
	// through as nil and the bulk loop skips the check, matching
	// the synchronous path.
	scope := GetCallerScope(ctx)

	// Detached goroutine — context.Background() so the loop survives
	// the HTTP request ending. Hard 45-minute cap so a stuck certbot
	// can't pin a "running" row forever.
	go s.runBulkIssueJob(job.ID, scope, req.Email, req.Wildcard, req.Reissue, domains)

	return &job, nil
}

// runBulkIssueJob is the detached worker that walks the domain list,
// stamps progress to Mongo after each row, and finalises the job
// status. Panics are recovered so a single goroutine crash can't
// take down the rest of the panel.
func (s *SSLService) runBulkIssueJob(jobID primitive.ObjectID, scope *CallerScope, email string, wildcard, reissue bool, domains []string) {
	ctx, cancel := context.WithTimeout(context.Background(), bulkSSLJobMaxDuration)
	defer cancel()
	// Re-inject the caller's scope so per-domain AssertOwnsDomain
	// resolves against the right tenant inside this fresh context.
	if scope != nil {
		ctx = WithCallerScope(ctx, scope)
	}

	col := s.db.Collection(database.ColSSLBulkJobs)
	defer func() {
		if r := recover(); r != nil {
			log.Error().
				Interface("panic", r).
				Str("job_id", jobID.Hex()).
				Msg("ssl bulk job: goroutine panicked — marking job failed")
			s.markBulkJobFailed(jobID, fmt.Sprintf("internal panic: %v", r))
		}
	}()

	// Transition queued → running so the UI knows the loop has
	// actually picked up the job (vs. queued = "we wrote the row,
	// goroutine hasn't started").
	if _, err := col.UpdateByID(ctx, jobID, bson.M{
		"$set": bson.M{
			"status":     models.SSLBulkJobStatusRunning,
			"started_at": time.Now(),
			"updated_at": time.Now(),
		},
	}); err != nil {
		log.Error().Err(err).Str("job_id", jobID.Hex()).Msg("ssl bulk job: failed to mark running")
		return
	}

	var success, failed, issued, reissued int
	for i, domain := range domains {
		// Cancellation check — if the operator hit Cancel between
		// rows, finalise the job and bail. We do NOT kill an
		// in-flight certbot run; certbot's --non-interactive mode
		// runs to completion in seconds-to-minutes and forcing it
		// could leave half-written cert files.
		if s.isBulkJobCancelled(ctx, jobID) {
			s.finaliseBulkJob(jobID, models.SSLBulkJobStatusCancelled, success, failed, issued, reissued, len(domains))
			return
		}

		// Stamp current_domain so the UI can render "Currently
		// issuing: <domain>" while certbot is in flight.
		_, _ = col.UpdateByID(ctx, jobID, bson.M{
			"$set": bson.M{
				"current_domain": domain,
				"updated_at":     time.Now(),
			},
		})

		item := models.IssueLetsEncryptBulkItem{Domain: domain}

		// Tenant gate — mirrors the synchronous bulk's check at
		// ssl_service.go:483-491. A vendor calling from the User
		// Panel can't issue SSL on a domain outside their tenant.
		if scope != nil {
			if err := scope.AssertOwnsDomain(ctx, s.db, domain); err != nil {
				item.Success = false
				item.Error = "domain is not in your tenant"
				failed++
				s.writeBulkRowOutcome(ctx, jobID, i, item, success, failed, issued, reissued, len(domains))
				continue
			}
		}

		// Snapshot the on-disk cert state BEFORE the call so we can
		// classify the result as "issued" (no prior cert) vs
		// "reissued" (replaced an existing one).
		preExisted := agent.LetsEncryptCertExists(domain)

		single := &models.IssueLetsEncryptRequest{
			Domain:   domain,
			Email:    email,
			Wildcard: wildcard,
			Reissue:  reissue,
		}
		cert, err := s.IssueLetsEncrypt(ctx, single)
		if err != nil {
			item.Success = false
			item.Error = err.Error()
			failed++
		} else {
			item.Success = true
			item.CertID = cert.ID.Hex()
			item.ExpiresAt = cert.ExpiresAt
			if preExisted {
				item.Action = "reissued"
				reissued++
			} else {
				item.Action = "issued"
				issued++
			}
			success++
		}
		s.writeBulkRowOutcome(ctx, jobID, i, item, success, failed, issued, reissued, len(domains))
	}

	s.finaliseBulkJob(jobID, models.SSLBulkJobStatusCompleted, success, failed, issued, reissued, len(domains))
}

// writeBulkRowOutcome stamps a single domain's result back onto the
// job doc + bumps the live counters. Failures here are logged but
// not fatal — at worst the operator sees a slightly stale poll
// response and a fresh one rights it.
func (s *SSLService) writeBulkRowOutcome(ctx context.Context, jobID primitive.ObjectID, idx int, item models.IssueLetsEncryptBulkItem, success, failed, issued, reissued, total int) {
	progress := 0
	if total > 0 {
		progress = ((success + failed) * 100) / total
	}
	update := bson.M{
		"$set": bson.M{
			fmt.Sprintf("items.%d", idx): item,
			"success":                    success,
			"failed":                     failed,
			"issued":                     issued,
			"reissued":                   reissued,
			"progress":                   progress,
			"updated_at":                 time.Now(),
		},
	}
	// Clear current_domain once the row has landed — between two
	// rows the UI shouldn't keep pointing at the just-completed one.
	update["$unset"] = bson.M{"current_domain": ""}
	if _, err := s.db.Collection(database.ColSSLBulkJobs).UpdateByID(ctx, jobID, update); err != nil {
		log.Warn().Err(err).Str("job_id", jobID.Hex()).Int("idx", idx).Msg("ssl bulk job: row outcome write failed")
	}
}

// isBulkJobCancelled is a single-field read against the job doc to
// check whether the operator hit Cancel since the last row. Bounded
// 2-second context so a transient Mongo hiccup doesn't stall the
// whole loop.
func (s *SSLService) isBulkJobCancelled(parentCtx context.Context, jobID primitive.ObjectID) bool {
	ctx, cancel := context.WithTimeout(parentCtx, 2*time.Second)
	defer cancel()
	var doc struct {
		CancelRequested bool `bson:"cancel_requested"`
	}
	err := s.db.Collection(database.ColSSLBulkJobs).
		FindOne(ctx, bson.M{"_id": jobID}).
		Decode(&doc)
	if err != nil {
		// Mongo unavailable → assume not cancelled; if the row was
		// deleted out from under us, the next progress write will
		// log the error and we'll exit naturally.
		return false
	}
	return doc.CancelRequested
}

// finaliseBulkJob stamps the terminal Status + counts + FinishedAt.
// Called once per job at the end of the goroutine (or on
// cancellation / panic).
func (s *SSLService) finaliseBulkJob(jobID primitive.ObjectID, status string, success, failed, issued, reissued, total int) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	progress := 0
	if total > 0 {
		progress = ((success + failed) * 100) / total
		if progress > 100 {
			progress = 100
		}
	}
	now := time.Now()
	_, err := s.db.Collection(database.ColSSLBulkJobs).UpdateByID(ctx, jobID, bson.M{
		"$set": bson.M{
			"status":      status,
			"success":     success,
			"failed":      failed,
			"issued":      issued,
			"reissued":    reissued,
			"progress":    progress,
			"finished_at": now,
			"updated_at":  now,
		},
		"$unset": bson.M{"current_domain": ""},
	})
	if err != nil {
		log.Warn().Err(err).Str("job_id", jobID.Hex()).Msg("ssl bulk job: finalise write failed")
		return
	}
	log.Info().
		Str("job_id", jobID.Hex()).
		Str("status", status).
		Int("total", total).
		Int("success", success).
		Int("failed", failed).
		Msg("ssl bulk job finalised")
}

// markBulkJobFailed flips Status → failed with a one-line error
// message. Reserved for catastrophic aborts (goroutine panic, etc.)
// — per-domain certbot failures don't reach this path.
func (s *SSLService) markBulkJobFailed(jobID primitive.ObjectID, msg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now()
	_, _ = s.db.Collection(database.ColSSLBulkJobs).UpdateByID(ctx, jobID, bson.M{
		"$set": bson.M{
			"status":      models.SSLBulkJobStatusFailed,
			"finished_at": now,
			"updated_at":  now,
		},
		"$unset": bson.M{"current_domain": ""},
	})
	_ = msg // surfaced via logger above the call site; not stored on the doc to keep the row shape stable
}

// GetBulkIssueJob is the poll endpoint — returns the current state of
// a job for a caller authorised to see it. Tenant scope: the caller
// must either be the OwnerUserID, share the TenantID, OR be the
// platform owner (vendor_owner — represented by a nil CallerScope
// because the WHM middleware doesn't apply tenant scope to it).
//
// Returns nil + mongo.ErrNoDocuments if the job doesn't exist OR if
// the caller isn't allowed to see it — we intentionally fold "not
// found" and "not authorised" into the same response so a vendor
// can't probe other tenants' job IDs.
func (s *SSLService) GetBulkIssueJob(ctx context.Context, jobIDHex string, callerUserID primitive.ObjectID) (*models.SSLBulkJob, error) {
	oid, err := primitive.ObjectIDFromHex(jobIDHex)
	if err != nil {
		return nil, mongo.ErrNoDocuments
	}
	var job models.SSLBulkJob
	if err := s.db.Collection(database.ColSSLBulkJobs).FindOne(ctx, bson.M{"_id": oid}).Decode(&job); err != nil {
		return nil, err
	}
	scope := GetCallerScope(ctx)
	// WHM owner (nil scope) sees every job. Tenant-scoped callers
	// must be the row's owner or share its tenant.
	if scope != nil {
		if job.OwnerUserID != callerUserID && (job.TenantID.IsZero() || scope.TenantHex != job.TenantID.Hex()) {
			return nil, mongo.ErrNoDocuments
		}
	}
	return &job, nil
}

// CancelBulkIssueJob sets the CancelRequested flag on a job. The
// detached goroutine sees it between rows and aborts cleanly. Same
// scope rules as GetBulkIssueJob — and same "fold not-found into
// not-authorised" response.
//
// Note: cancellation does NOT roll back already-issued certs. The
// row outcomes that have already landed stay; the loop just stops
// processing the remaining domains.
func (s *SSLService) CancelBulkIssueJob(ctx context.Context, jobIDHex string, callerUserID primitive.ObjectID) error {
	job, err := s.GetBulkIssueJob(ctx, jobIDHex, callerUserID)
	if err != nil {
		return err
	}
	// Idempotent: cancelling a job that's already cancelled /
	// completed / failed is a no-op + success. The loop has either
	// already exited or will see the flag and bail at the next
	// between-row check.
	if job.Status != models.SSLBulkJobStatusRunning && job.Status != models.SSLBulkJobStatusQueued {
		return nil
	}
	_, err = s.db.Collection(database.ColSSLBulkJobs).UpdateByID(ctx, job.ID, bson.M{
		"$set": bson.M{
			"cancel_requested": true,
			"updated_at":       time.Now(),
		},
	})
	return err
}

// RecoverStaleRunningJobs is called once at server boot to clean up
// "running" rows whose goroutine died (server restart, crash). A
// healthy run writes to the doc after every domain — minutes apart
// at most — so any row that's been "running" with no update_at
// progress for staleRunningCutoff is dead and should be marked
// failed.
//
// Idempotent: re-running on a clean panel does nothing. Safe to
// call from every boot.
func (s *SSLService) RecoverStaleRunningJobs(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-staleRunningCutoff)
	res, err := s.db.Collection(database.ColSSLBulkJobs).UpdateMany(ctx,
		bson.M{
			"status":     bson.M{"$in": []string{models.SSLBulkJobStatusQueued, models.SSLBulkJobStatusRunning}},
			"updated_at": bson.M{"$lt": cutoff},
		},
		bson.M{
			"$set": bson.M{
				"status":      models.SSLBulkJobStatusFailed,
				"finished_at": time.Now(),
				"updated_at":  time.Now(),
			},
			"$unset": bson.M{"current_domain": ""},
		},
	)
	if err != nil {
		return 0, err
	}
	return int(res.ModifiedCount), nil
}

// ComputeBulkJobProgress is a pure helper exposing the progress
// formula so unit tests can lock the rounding behaviour without
// reaching into the package internals. progress = ⌊(done/total)*100⌋,
// clamped to [0, 100]. Total of 0 returns 0 (no work, no progress).
func ComputeBulkJobProgress(success, failed, total int) int {
	if total <= 0 {
		return 0
	}
	done := success + failed
	if done <= 0 {
		return 0
	}
	p := (done * 100) / total
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// ─────────────────────────────────────────────────────────────────────
// Boot-time sweep — runs once from main.go on startup.
// ─────────────────────────────────────────────────────────────────────

// recoverSSLJobsOnce coordinates a single boot-time RecoverStaleRunningJobs
// call so the operator's main.go init code doesn't have to thread the
// service down to a separate helper. sync.Once so a future double-init
// (e.g. integration tests booting the server twice in-process) doesn't
// re-mark already-cleaned rows.
var recoverSSLJobsOnce sync.Once

// SSLBulkRecoverOnBoot is the public entry main.go calls. Logs the
// number of recovered rows so the operator sees the heartbeat.
func (s *SSLService) SSLBulkRecoverOnBoot(ctx context.Context) {
	recoverSSLJobsOnce.Do(func() {
		n, err := s.RecoverStaleRunningJobs(ctx)
		if err != nil {
			log.Warn().Err(err).Msg("ssl bulk job: boot-time recovery failed")
			return
		}
		if n > 0 {
			log.Info().Int("recovered", n).Msg("ssl bulk job: marked stale running rows as failed")
		}
	})
}
