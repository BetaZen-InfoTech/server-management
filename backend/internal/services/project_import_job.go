package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// StartImportJob records an import job, kicks the actual Provision pipeline off
// on a DETACHED context, and returns the job immediately for the UI to poll.
//
// Why detached: Provision is atomic — if its context is cancelled mid-way it
// rolls back and DELETES the half-built project. A large import runs for
// minutes-to-an-hour, far longer than the HTTP request / nginx survive, so the
// old synchronous handler 504'd, the request context cancelled, and the import
// self-deleted ("project rolled back"). Running it on a background context with
// the caller scope copied over lets it finish regardless of the client.
func (s *ProjectService) StartImportJob(ctx context.Context, req *models.ImportProjectRequest) (*models.ProjectImportJob, error) {
	if req == nil {
		return nil, fmt.Errorf("import request is required")
	}
	name := strings.TrimSpace(req.OverrideName)
	if name == "" {
		name = strings.TrimSpace(req.Manifest.Project.Name)
	}

	// Validate the manifest SYNCHRONOUSLY so an obviously-bad submit is rejected
	// with a 400 instead of creating a job row that instantly flips to failed.
	// Mirrors the checks at the top of Import (the authoritative ones still run
	// there in the background).
	m := req.Manifest
	if m.SchemaVersion == 0 {
		return nil, fmt.Errorf("manifest missing schema_version — not a valid project export")
	}
	if strings.TrimSpace(m.Project.GitRepoURL) == "" {
		return nil, fmt.Errorf("manifest is missing project.git_repo_url")
	}
	if len(m.Services) == 0 {
		return nil, fmt.Errorf("manifest has zero services — at least one service is required")
	}
	if name == "" {
		return nil, fmt.Errorf("manifest is missing project.name")
	}

	// Resolve the caller's tenant for both the dedup check and the job row.
	var tenantID primitive.ObjectID
	if scope := GetCallerScope(ctx); scope != nil && scope.TenantHex != "" {
		if tid, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			tenantID = tid
		}
	}

	// Idempotency guard: because Import now returns instantly (202), a double
	// click or client retry would otherwise start a SECOND import of the same
	// project — two Provisions racing on slug/domain uniqueness → a duplicate
	// "name-2" project or a half-provisioned collision. Reject if one with the
	// same name (+ tenant) is already running.
	dupFilter := bson.M{"status": "running", "project_name": name}
	if tenantID != primitive.NilObjectID {
		dupFilter["tenant_id"] = tenantID
	}
	if n, _ := s.db.Collection(database.ColProjectImportJobs).CountDocuments(ctx, dupFilter); n > 0 {
		return nil, fmt.Errorf("an import of %q is already running — wait for it to finish before importing again", name)
	}

	now := time.Now()
	job := &models.ProjectImportJob{
		Status:        "running",
		ProjectName:   name,
		TotalServices: len(m.Services),
		User:          strings.TrimSpace(req.User),
		TenantID:      tenantID,
		StartedAt:     now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	res, err := s.db.Collection(database.ColProjectImportJobs).InsertOne(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("create import job: %w", err)
	}
	job.ID = res.InsertedID.(primitive.ObjectID)

	// Detach from the request: copy the caller scope onto a background context
	// so the import survives the client/nginx disconnect. Deliberately NO
	// per-import deadline — a timeout here would cancel a legitimately-long
	// import mid-run, which trips Provision's atomic rollback and DELETES the
	// near-complete project, i.e. the exact 504→rollback-delete bug this async
	// path removes, just moved to the deadline boundary. Each Provision step
	// already has its own command timeout so the goroutine still terminates; a
	// truly-stuck "running" job is cleared by RecoverStaleImportJobsOnBoot.
	bgCtx := WithCallerScope(context.Background(), GetCallerScope(ctx))
	jobID := job.ID
	go func() {
		result, impErr := s.Import(bgCtx, req)
		fin := time.Now()
		set := bson.M{"finished_at": fin, "updated_at": fin}
		if impErr != nil {
			set["status"] = "failed"
			set["error"] = friendlyImportError(impErr)
			// Preserve the full build log for a service build failure so the
			// modal can still show it (the old sync 422 carried it in details).
			if pe, ok := impErr.(*ProvisionError); ok && strings.TrimSpace(pe.Build.Details) != "" {
				set["error_details"] = pe.Build.Details
			}
			log.Warn().Err(impErr).Str("job_id", jobID.Hex()).Str("name", name).Msg("async project import failed")
		} else {
			set["status"] = "completed"
			if result != nil && result.Project.ID != primitive.NilObjectID {
				set["project_id"] = result.Project.ID
			}
			log.Info().Str("job_id", jobID.Hex()).Str("name", name).Msg("async project import completed")
		}
		// The request context is long gone — update on a fresh background ctx.
		uctx, ucancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ucancel()
		if _, uerr := s.db.Collection(database.ColProjectImportJobs).UpdateOne(uctx, bson.M{"_id": jobID}, bson.M{"$set": set}); uerr != nil {
			log.Error().Err(uerr).Str("job_id", jobID.Hex()).Msg("failed to persist terminal import-job status — boot-recovery will reconcile")
		}
	}()

	return job, nil
}

// friendlyImportError shortens a ProvisionError into a one-line reason for the
// job row; otherwise truncates the raw error.
func friendlyImportError(err error) string {
	if err == nil {
		return ""
	}
	if pe, ok := err.(*ProvisionError); ok {
		return fmt.Sprintf("service %q: %s failed — %s", pe.ServiceName, pe.Build.Stage, pe.Build.Summary)
	}
	msg := err.Error()
	if len(msg) > 600 {
		msg = msg[:600] + "…"
	}
	return msg
}

// GetImportJob returns one import job, tenant-scoped (owner sees all).
func (s *ProjectService) GetImportJob(ctx context.Context, jobHex string) (*models.ProjectImportJob, error) {
	oid, err := primitive.ObjectIDFromHex(jobHex)
	if err != nil {
		return nil, fmt.Errorf("invalid job id")
	}
	filter := bson.M{"_id": oid}
	if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) && scope.TenantHex != "" {
		if tid, terr := primitive.ObjectIDFromHex(scope.TenantHex); terr == nil {
			filter["tenant_id"] = tid
		}
	}
	var job models.ProjectImportJob
	if err := s.db.Collection(database.ColProjectImportJobs).FindOne(ctx, filter).Decode(&job); err != nil {
		return nil, err
	}
	return &job, nil
}

// RecoverStaleImportJobsOnBoot marks any import job still "running" when the
// process died as failed, so the UI shows a terminal state instead of a
// forever-spinning bar. Mirrors the SSL / domain bulk-job boot recovery.
func (s *ProjectService) RecoverStaleImportJobsOnBoot(ctx context.Context) {
	now := time.Now()
	s.db.Collection(database.ColProjectImportJobs).UpdateMany(ctx,
		bson.M{"status": "running"},
		bson.M{"$set": bson.M{
			"status":      "failed",
			"error":       "import interrupted — the panel restarted while it was running. A partially-created project may exist; delete it before re-running the import.",
			"finished_at": now,
			"updated_at":  now,
		}})
}
