package services

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Async bulk-domain-upload job. Mirrors the SSL bulk-job / Cloudflare sync-job
// pattern: a durable Mongo doc whose per-row items + counters + progress are
// updated IN PLACE by a detached worker goroutine, so the Domains modal can poll
// GET .../bulk-upload/jobs/{id} and render LIVE per-domain progress, and the job
// survives a backend restart (boot recovery marks abandoned runs failed).
//
// Why a job (not the old synchronous request): provisioning N domains (zone +
// vhost + mail per row) takes minutes; a synchronous POST would either time out
// the client or leave it staring at a spinner with no feedback. SSL is deferred
// to the background exactly as the synchronous path does.

const bulkDomainJobMaxDuration = 60 * time.Minute // hard cap on the detached goroutine

// bulkCell reads a cell by canonical column key, trimmed, "" when absent.
func bulkCell(row []string, headerIdx map[string]int, key string) string {
	idx, ok := headerIdx[key]
	if !ok || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

// StartBulkUploadJob validates the parsed rows, creates a queued job doc with
// one pre-seeded item per data row, launches the background worker, and returns
// the job so the handler can hand back {job_id, total} for polling.
func (s *DomainService) StartBulkUploadJob(ctx context.Context, rows [][]string, format BulkUploadFormat, opts BulkUploadOptions, owner, tenant primitive.ObjectID) (*models.DomainBulkJob, error) {
	headerIdx, err := bulkHeaderIndex(rows)
	if err != nil {
		return nil, err
	}
	// Pre-seed one item per NON-BLANK data row, in order, so the UI renders the
	// full domain list immediately and the worker iterates the same rows.
	items := make([]models.DomainBulkJobItem, 0, len(rows))
	for i := 1; i < len(rows); i++ {
		if rowAllBlank(rows[i]) {
			continue
		}
		user := bulkCell(rows[i], headerIdx, "user")
		if opts.CallerUsername != "" {
			user = opts.CallerUsername
		}
		items = append(items, models.DomainBulkJobItem{
			RowNumber: i + 1,
			Domain:    strings.ToLower(bulkCell(rows[i], headerIdx, "domain")),
			User:      user,
			Status:    models.DomainBulkItemPending,
		})
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no domain rows found in the file")
	}

	now := time.Now()
	job := models.DomainBulkJob{
		OwnerUserID: owner,
		TenantID:    tenant,
		Status:      models.DomainBulkJobQueued,
		Format:      string(format),
		Total:       len(items),
		Items:       items,
		IssueSSL:    opts.IssueSSL,
		ForceSSL:    opts.ForceSSL,
		StartedAt:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	res, err := s.db.Collection(database.ColDomainBulkJobs).InsertOne(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("create domain bulk job: %w", err)
	}
	job.ID = res.InsertedID.(primitive.ObjectID)

	// Snapshot the caller's tenant scope BEFORE returning — the request ctx dies
	// when the handler returns, so the detached worker must carry the scope
	// itself or Create's per-row AssertOwns tenant guard would be silently
	// skipped (a cross-tenant escalation). Mirrors the SSL bulk-job worker.
	scope := GetCallerScope(ctx)
	go s.runBulkUploadJob(job.ID, scope, rows, opts)
	return &job, nil
}

// StartBulkUploadJobFromContentType parses an uploaded CSV/XLSX synchronously
// (in the request — the file is ≤10 MB) then starts the async job. The
// background worker only ever sees the already-parsed rows, so the multipart
// file handle can be released as soon as this returns.
func (s *DomainService) StartBulkUploadJobFromContentType(ctx context.Context, body io.Reader, contentType, filename string, opts BulkUploadOptions, owner, tenant primitive.ObjectID) (*models.DomainBulkJob, error) {
	rows, format, err := parseBulkFile(body, contentType, filename)
	if err != nil {
		return nil, err
	}
	return s.StartBulkUploadJob(ctx, rows, format, opts, owner, tenant)
}

// GetBulkUploadJob returns a job by hex id (for polling). Tenant scoping is the
// caller/handler's responsibility.
func (s *DomainService) GetBulkUploadJob(ctx context.Context, id string) (*models.DomainBulkJob, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid job id")
	}
	var job models.DomainBulkJob
	if err := s.db.Collection(database.ColDomainBulkJobs).FindOne(ctx, bson.M{"_id": oid}).Decode(&job); err != nil {
		return nil, fmt.Errorf("job not found")
	}
	return &job, nil
}

// CancelBulkUploadJob sets the cancel flag; the worker checks it between rows.
func (s *DomainService) CancelBulkUploadJob(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid job id")
	}
	_, err = s.db.Collection(database.ColDomainBulkJobs).UpdateOne(ctx,
		bson.M{"_id": oid}, bson.M{"$set": bson.M{"cancel_requested": true, "updated_at": time.Now()}})
	return err
}

// runBulkUploadJob is the detached worker: it processes each data row, updating
// the job doc IN PLACE (per-item + counters + progress) so pollers see live
// progress. Uses $set of run-owned fields ONLY (never a whole-doc replace) so a
// concurrently-set cancel_requested is never clobbered. Deferred SSL is issued
// in one background pass after the create loop, exactly like the sync path.
func (s *DomainService) runBulkUploadJob(jobID primitive.ObjectID, scope *CallerScope, rows [][]string, opts BulkUploadOptions) {
	ctx, cancel := context.WithTimeout(context.Background(), bulkDomainJobMaxDuration)
	defer cancel()
	// Re-inject the caller's tenant scope so Create's per-row ownership guard
	// (AssertOwns on req.User) still runs in the background — WITHOUT this a
	// non-owner WHM operator could bulk-create domains under another tenant's user.
	ctx = WithCallerScope(ctx, scope)

	col := s.db.Collection(database.ColDomainBulkJobs)
	setJob := func(fields bson.M) {
		fields["updated_at"] = time.Now()
		// Independent short-lived context — NOT the 60-minute worker ctx. A run
		// that exceeds bulkDomainJobMaxDuration cancels that ctx, and if the
		// terminal completed/failed/cancelled write rode on it too, the write
		// would be a no-op and the job would be stuck "running" forever (until
		// boot recovery). The per-row WORK still respects the worker ctx below;
		// only the status persistence is decoupled so it always lands.
		wctx, wcancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer wcancel()
		_, _ = col.UpdateOne(wctx, bson.M{"_id": jobID}, bson.M{"$set": fields})
	}
	setJob(bson.M{"status": models.DomainBulkJobRunning})

	headerIdx, err := bulkHeaderIndex(rows)
	if err != nil {
		fin := time.Now()
		setJob(bson.M{"status": models.DomainBulkJobFailed, "error": err.Error(), "finished_at": fin})
		return
	}

	// True denominator = non-blank data rows (matches the pre-seeded item count),
	// so progress isn't understated when the sheet has trailing blank rows.
	total := 0
	for i := 1; i < len(rows); i++ {
		if !rowAllBlank(rows[i]) {
			total++
		}
	}
	if total == 0 {
		total = 1
	}

	var (
		processed, successes, failures, sslIssued, sslForced int
		sslQueue                                             []string
		itemIdx                                              int
	)
	for i := 1; i < len(rows); i++ {
		if rowAllBlank(rows[i]) {
			continue
		}
		// Cancel check between rows — cheap findOne of just the flag.
		var flag struct {
			CancelRequested bool `bson:"cancel_requested"`
		}
		if err := col.FindOne(ctx, bson.M{"_id": jobID},
			options.FindOne().SetProjection(bson.M{"cancel_requested": 1})).Decode(&flag); err == nil && flag.CancelRequested {
			fin := time.Now()
			setJob(bson.M{"status": models.DomainBulkJobCancelled, "current_domain": "", "finished_at": fin})
			return
		}

		domain := strings.ToLower(bulkCell(rows[i], headerIdx, "domain"))
		// Mark this row in-flight so the UI shows which domain is being created.
		setJob(bson.M{
			fmt.Sprintf("items.%d.status", itemIdx): models.DomainBulkItemCreating,
			"current_domain":                        domain,
		})

		result, queueSSL := s.processBulkRow(ctx, rows[i], i+1, headerIdx, opts)
		if queueSSL {
			sslQueue = append(sslQueue, result.Domain)
		}
		item := models.DomainBulkJobItem{
			RowNumber:            result.RowNumber,
			Domain:               result.Domain,
			User:                 result.User,
			Success:              result.Success,
			Error:                result.Error,
			SSLIssued:            result.SSLIssued,
			SSLForced:            result.SSLForced,
			SSLMessage:           result.SSLMessage,
			SetupWarnings:        result.SetupWarnings,
			AdminMailbox:         result.AdminMailbox,
			AdminMailboxPassword: result.AdminMailboxPassword,
			Status:               models.DomainBulkItemDone,
		}
		if !result.Success {
			item.Status = models.DomainBulkItemFailed
			failures++
		} else {
			successes++
		}
		if result.SSLIssued {
			sslIssued++
		}
		if result.SSLForced {
			sslForced++
		}
		processed++
		progress := int(float64(processed) / float64(total) * 100)
		if progress > 100 {
			progress = 100
		}
		setJob(bson.M{
			fmt.Sprintf("items.%d", itemIdx): item,
			"processed":                      processed,
			"successes":                      successes,
			"failures":                       failures,
			"ssl_issued":                     sslIssued,
			"ssl_forced":                     sslForced,
			"progress":                       progress,
		})
		itemIdx++
	}

	// Deferred SSL — one background pass, serial, off this job's critical path.
	if opts.IssueSSL && s.ssl != nil && len(sslQueue) > 0 {
		go s.issueBulkSSLBackground(append([]string(nil), sslQueue...), opts.ForceSSL)
	}

	fin := time.Now()
	setJob(bson.M{
		"status":         models.DomainBulkJobCompleted,
		"progress":       100,
		"current_domain": "",
		"finished_at":    fin,
	})
}

// RecoverStaleBulkUploadJobsOnBoot marks any job left "queued"/"running" (its
// worker goroutine died when the process last stopped) as failed, so the UI
// renders a terminal state instead of a forever-spinning bar. Called once at
// boot, mirroring the SSL/Cloudflare bulk-job recovery.
func (s *DomainService) RecoverStaleBulkUploadJobsOnBoot(ctx context.Context) {
	_, _ = s.db.Collection(database.ColDomainBulkJobs).UpdateMany(ctx,
		bson.M{"status": bson.M{"$in": []string{models.DomainBulkJobQueued, models.DomainBulkJobRunning}}},
		bson.M{"$set": bson.M{
			"status":      models.DomainBulkJobFailed,
			"error":       "interrupted by a backend restart — re-upload to continue",
			"finished_at": time.Now(),
			"updated_at":  time.Now(),
		}})
}
