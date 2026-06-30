// Package services — project_services_json.go is the JSON-flavoured
// sibling of project_bulk_service.go's CSV/XLSX surface. It powers the
// "Export JSON / Import JSON / Edit JSON" trio on the WHM Deploy
// Software project drawer's Services toolbar.
//
// Why a separate file rather than another branch inside
// BulkAddServicesFromContentType: the JSON variant is *not* a
// spreadsheet-shaped row stream. The export carries the project's id
// alongside each service so the Edit-JSON flow can match incoming
// entries to existing rows by service _id (the cell-position-based
// dispatch the CSV path uses doesn't work for "update in place"). And
// the edit endpoint differs by HTTP verb (PUT, not POST) and by per-row
// semantics (UpdateService, not AddService). Forcing it into the
// existing executor would muddy three distinct flows; a dedicated file
// keeps each one obvious.
//
// Three operations:
//
//  1. ExportServices — read-only snapshot of the project's services
//     as a portable JSON manifest. Strips host-specific fields
//     (install_dir, build_dir, systemd_unit) and per-instance runtime
//     state (status, last_commit_sha, last_deployed_at,
//     missing_env_keys) so the result can be re-imported on the same
//     or a different panel without dragging stale state along.
//     Keeps service _id so an operator who downloads, hand-edits, and
//     re-uploads via Edit JSON gets in-place updates instead of
//     duplicates. The export is also useful as a backup snapshot
//     before a risky bulk edit.
//
//  2. BulkAddServicesFromJSON — additive import. Walks the manifest's
//     services array, calls AddService per row, surfaces partial-
//     success via the same BulkServicesUploadResponse shape the
//     CSV/XLSX path returns. Existing services on the project are
//     left untouched; rows with the same Name as an existing service
//     fail with a clear "name already used" error rather than silently
//     overwriting (matches the single-create form's behaviour).
//
//  3. BulkUpdateServicesFromJSON — in-place edit. Walks the manifest's
//     services array, requires `id` on every row, looks the row up by
//     hex ObjectID, and calls UpdateService with the patchable subset
//     (framework, git_branch, git_subpath, path_prefix, install_cmd,
//     build_cmd, start_cmd, runtime_version, port, env_vars,
//     primary_domain, alias_domains). Rows targeting a different
//     project (id-typo, paste-from-another-project) are rejected
//     so a careless edit can't reach across project boundaries.
//
// Partial success is normal for both write paths — one bad row never
// aborts the others. Each row gets its own outcome line so the
// operator can fix and retry the failing rows only.

package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/version"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ProjectServicesExport is the JSON shape returned by ExportServices.
// Wraps the per-service entries in an object with metadata so the
// import side can validate the shape up-front instead of guessing.
type ProjectServicesExport struct {
	SchemaVersion int                    `json:"schema_version"`
	ProjectID     string                 `json:"project_id"`
	ProjectSlug   string                 `json:"project_slug"`
	ExportedAt    string                 `json:"exported_at"`
	PanelVersion  string                 `json:"panel_version"`
	Services      []ProjectServiceExport `json:"services"`
}

// ProjectServiceExport is a single portable service entry. ID is
// preserved so re-uploads via Edit JSON match in place; on Import JSON
// (the additive endpoint) the ID is *ignored* and a fresh row is created.
type ProjectServiceExport struct {
	ID             string            `json:"id,omitempty"`
	Name           string            `json:"name"`
	Role           string            `json:"role"`
	Framework      string            `json:"framework,omitempty"`
	GitSubpath     string            `json:"git_subpath,omitempty"`
	GitBranch      string            `json:"git_branch,omitempty"`
	PathPrefix     string            `json:"path_prefix,omitempty"`
	PrimaryDomain  string            `json:"primary_domain"`
	AliasDomains   []string          `json:"alias_domains,omitempty"`
	InstallCmd     string            `json:"install_cmd,omitempty"`
	BuildCmd       string            `json:"build_cmd,omitempty"`
	StartCmd       string            `json:"start_cmd,omitempty"`
	RuntimeVersion string            `json:"runtime_version,omitempty"`
	Port           int               `json:"port,omitempty"`
	EnvVars        map[string]string `json:"env_vars,omitempty"`
	User           string            `json:"user,omitempty"`
}

// CurrentProjectServicesExportSchemaVersion is the version stamped into
// every freshly-generated services-export. Bump on field renames or
// type changes; leave alone for additive optional fields older panels
// can ignore.
const CurrentProjectServicesExportSchemaVersion = 1

// ExportServices builds a portable JSON snapshot of every service on
// the given project. Host-specific paths (install_dir, build_dir,
// systemd_unit) and per-instance runtime state (status,
// last_commit_sha, last_deployed_at, missing_env_keys) are stripped.
// Service _id is preserved so Edit JSON can match in-place; Import
// JSON (the additive endpoint) treats incoming IDs as advisory.
func (s *ProjectService) ExportServices(ctx context.Context, projectID string) (*ProjectServicesExport, error) {
	oid, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		return nil, fmt.Errorf("invalid project id")
	}
	proj, err := s.loadProject(ctx, oid)
	if err != nil {
		return nil, fmt.Errorf("project not found")
	}
	svcs, err := s.listServicesForProject(ctx, oid)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	out := &ProjectServicesExport{
		SchemaVersion: CurrentProjectServicesExportSchemaVersion,
		ProjectID:     proj.ID.Hex(),
		ProjectSlug:   proj.Slug,
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		PanelVersion:  version.Number(),
		Services:      make([]ProjectServiceExport, 0, len(svcs)),
	}
	for _, svc := range svcs {
		out.Services = append(out.Services, ProjectServiceExport{
			ID:             svc.ID.Hex(),
			Name:           svc.Name,
			Role:           svc.Role,
			Framework:      svc.Framework,
			GitSubpath:     svc.GitSubpath,
			GitBranch:      svc.GitBranch,
			PathPrefix:     svc.PathPrefix,
			PrimaryDomain:  svc.PrimaryDomain,
			// Union of legacy alias_domains + durably-attached domains, so the
			// Import-JSON / Edit-JSON round trip restores every additional
			// domain (BulkAdd re-attaches via AddService; BulkUpdate re-syncs).
			AliasDomains:   dedupeDomains(svc.AliasDomains, attachedDomainNames(ctx, s.db, svc.ID)),
			InstallCmd:     svc.InstallCmd,
			BuildCmd:       svc.BuildCmd,
			StartCmd:       svc.StartCmd,
			RuntimeVersion: svc.RuntimeVersion,
			Port:           svc.Port,
			EnvVars:        copyEnvVars(svc.EnvVars),
			User:           svc.User,
		})
	}
	return out, nil
}

// BulkAddServicesFromJSON walks the JSON manifest and runs every
// service entry through AddService. Mirrors the CSV/XLSX flow: per-row
// validation, per-row failure isolation, identical response shape so
// the WHM UI can render the result table without special-casing the
// format. Incoming IDs are ignored — this is the additive endpoint.
//
// Common operator workflow: snapshot a project's services on a staging
// panel via Export JSON, sanity-check the manifest in an editor, then
// Import JSON on production. The defaults (GitRepoURL / GitBranch
// inherited from the destination project) keep the manifest portable
// without manual rewrites.
func (s *ProjectService) BulkAddServicesFromJSON(ctx context.Context, projectID string, services []ProjectServiceExport) (*BulkServicesUploadResponse, error) {
	resp := &BulkServicesUploadResponse{Format: BulkUploadFormatJSON, Items: []BulkServiceRowResult{}}
	if len(services) == 0 {
		return resp, fmt.Errorf("services array is empty — at least one service is required")
	}
	for i, entry := range services {
		req := &models.AddServiceRequest{
			Name:           strings.ToLower(strings.TrimSpace(entry.Name)),
			Role:           strings.ToLower(strings.TrimSpace(entry.Role)),
			Framework:      strings.ToLower(strings.TrimSpace(entry.Framework)),
			GitSubpath:     strings.TrimSpace(entry.GitSubpath),
			GitBranch:      strings.TrimSpace(entry.GitBranch),
			PathPrefix:     strings.TrimSpace(entry.PathPrefix),
			PrimaryDomain:  strings.ToLower(strings.TrimSpace(entry.PrimaryDomain)),
			AliasDomains:   append([]string(nil), entry.AliasDomains...),
			InstallCmd:     entry.InstallCmd,
			BuildCmd:       entry.BuildCmd,
			StartCmd:       entry.StartCmd,
			RuntimeVersion: entry.RuntimeVersion,
			Port:           entry.Port,
			EnvVars:        copyEnvVars(entry.EnvVars),
			User:           strings.TrimSpace(entry.User),
		}
		// Default role to "backend" if blank — matches CSV path's
		// fallback policy. Frontend / static rows MUST set role
		// explicitly in JSON; the manifest has space for the field
		// and silent defaulting would surprise the operator.
		if req.Role == "" {
			req.Role = "backend"
		}
		result := BulkServiceRowResult{
			RowNumber:     i + 1,
			Name:          req.Name,
			Role:          req.Role,
			Framework:     req.Framework,
			PrimaryDomain: req.PrimaryDomain,
		}
		if errs := validator.Validate(*req); errs != nil {
			result.Error = "validation: " + firstValidationError(errs)
			resp.Items = append(resp.Items, result)
			resp.Failures++
			continue
		}
		svc, err := s.AddService(ctx, projectID, req)
		if err != nil {
			result.Error = bulkServiceErrorMessage(err)
			resp.Items = append(resp.Items, result)
			resp.Failures++
			continue
		}
		result.Success = true
		result.ServiceID = svc.ID.Hex()
		result.FinalPort = svc.Port
		result.MissingEnvKeys = svc.MissingEnvKeys
		result.Role = svc.Role
		result.Framework = svc.Framework
		resp.Items = append(resp.Items, result)
		resp.Successes++
	}
	resp.TotalRows = len(resp.Items)
	return resp, nil
}

// BulkServicesEditResult is one row's outcome on the Edit JSON path.
// Distinct from BulkServiceRowResult only because the "row" here is
// keyed by ServiceID (which is REQUIRED on edit; optional on add) and
// includes the post-update field snapshot so the UI can diff what
// changed without a separate round-trip.
type BulkServicesEditResult struct {
	ServiceID     string `json:"service_id"`
	Name          string `json:"name"`
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
	PrimaryDomain string `json:"primary_domain,omitempty"`
	Port          int    `json:"port,omitempty"`
}

// BulkServicesEditResponse mirrors BulkServicesUploadResponse for the
// edit endpoint. Same Successes / Failures counters so the UI's
// summary card renders identically across the import + edit flows.
type BulkServicesEditResponse struct {
	Format    BulkUploadFormat         `json:"format"`
	TotalRows int                      `json:"total_rows"`
	Successes int                      `json:"successes"`
	Failures  int                      `json:"failures"`
	Items     []BulkServicesEditResult `json:"items"`
}

// BulkUpdateServicesFromJSON walks the manifest and runs every entry
// through UpdateService. Rows MUST carry a hex ServiceID that belongs
// to `projectID` — a foreign or invalid ID fails the row but never
// reaches across project boundaries. Patchable subset matches
// UpdateServiceRequest exactly; fields the manifest omits leave the
// service unchanged.
//
// Common operator workflow: download via Export JSON, tweak env_vars
// across half a dozen services in an editor (much faster than the
// per-service Edit modal), upload via Edit JSON to apply the changes
// in one batch. The endpoint is idempotent per row — re-uploading the
// same manifest is a no-op except for the updated_at timestamp.
func (s *ProjectService) BulkUpdateServicesFromJSON(ctx context.Context, projectID string, services []ProjectServiceExport) (*BulkServicesEditResponse, error) {
	resp := &BulkServicesEditResponse{Format: BulkUploadFormatJSON, Items: []BulkServicesEditResult{}}
	if len(services) == 0 {
		return resp, fmt.Errorf("services array is empty — at least one service is required")
	}
	projOID, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		return resp, fmt.Errorf("invalid project id")
	}
	for _, entry := range services {
		row := BulkServicesEditResult{
			ServiceID: entry.ID,
			Name:      entry.Name,
		}
		if strings.TrimSpace(entry.ID) == "" {
			row.Error = "missing id — every entry must carry the service's id"
			resp.Items = append(resp.Items, row)
			resp.Failures++
			continue
		}
		svcOID, err := primitive.ObjectIDFromHex(entry.ID)
		if err != nil {
			row.Error = "invalid id format"
			resp.Items = append(resp.Items, row)
			resp.Failures++
			continue
		}
		// Cross-project guard: reject IDs that point at a different
		// project so a careless paste can't reach across boundaries.
		// One mongo round-trip per row is fine — Edit JSON is for
		// dozens not thousands of services.
		var found models.ProjectService
		if err := s.db.Collection(database.ColProjectServices).FindOne(ctx, bson.M{
			"_id":        svcOID,
			"project_id": projOID,
		}).Decode(&found); err != nil {
			row.Error = "service not found in this project"
			resp.Items = append(resp.Items, row)
			resp.Failures++
			continue
		}

		// Build a sparse UpdateServiceRequest — only fields actually
		// present in the manifest entry get a non-nil pointer, which
		// is the way UpdateService distinguishes "omit" from "clear".
		// Trimmed copies match the CSV path's normalisation policy.
		req := &models.UpdateServiceRequest{}
		if v := strings.ToLower(strings.TrimSpace(entry.Framework)); v != "" {
			req.Framework = &v
		}
		if v := strings.TrimSpace(entry.GitSubpath); v != "" {
			req.GitSubpath = &v
		}
		if v := strings.TrimSpace(entry.GitBranch); v != "" {
			req.GitBranch = &v
		}
		if v := entry.PathPrefix; v != "" {
			req.PathPrefix = &v
		}
		if v := entry.InstallCmd; v != "" {
			req.InstallCmd = &v
		}
		if v := entry.BuildCmd; v != "" {
			req.BuildCmd = &v
		}
		if v := entry.StartCmd; v != "" {
			req.StartCmd = &v
		}
		if v := entry.RuntimeVersion; v != "" {
			req.RuntimeVersion = &v
		}
		if entry.Port > 0 {
			p := entry.Port
			req.Port = &p
		}
		if entry.EnvVars != nil {
			ev := copyEnvVars(entry.EnvVars)
			req.EnvVars = &ev
		}
		if v := strings.ToLower(strings.TrimSpace(entry.PrimaryDomain)); v != "" {
			req.PrimaryDomain = &v
		}
		// Additional domains are deliberately NOT pushed through
		// UpdateService's alias_domains — that would re-merge them into the
		// primary's server_name and collide with their own attached vhosts.
		// They're re-synced durably below via attach/detach.

		svc, err := s.UpdateService(ctx, entry.ID, req)
		if err != nil {
			row.Error = err.Error()
			resp.Items = append(resp.Items, row)
			resp.Failures++
			continue
		}
		// Re-sync attached domains to the manifest list (Edit JSON treats the
		// uploaded manifest as the desired state). Detach what's no longer
		// listed, attach what's new — each via the durable AttachDomain /
		// DetachDomain so it never re-merges into the primary vhost.
		if entry.AliasDomains != nil {
			desired := map[string]bool{}
			for _, d := range entry.AliasDomains {
				if dd := sanitizeDomain(d); dd != "" && dd != svc.PrimaryDomain {
					desired[dd] = true
				}
			}
			curSet := map[string]bool{}
			for _, d := range attachedDomainNames(ctx, s.db, svcOID) {
				curSet[d] = true
				if !desired[d] {
					_, _ = s.DetachDomain(ctx, projectID, entry.ID, d)
				}
			}
			for d := range desired {
				if !curSet[d] {
					_, _ = s.AttachDomain(ctx, projectID, entry.ID, d)
				}
			}
		}
		row.Success = true
		row.PrimaryDomain = svc.PrimaryDomain
		row.Port = svc.Port
		resp.Items = append(resp.Items, row)
		resp.Successes++
	}
	resp.TotalRows = len(resp.Items)
	return resp, nil
}
