package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

// ProjectHandler exposes the Deploy Software feature over HTTP. Routes are
// registered in whm_routes.go under /api/v1/whm/projects/*, plus the public
// webhook at /api/v1/deploy/webhooks/project/:project_id.
type ProjectHandler struct {
	service *services.ProjectService
}

func NewProjectHandler(s *services.ProjectService) *ProjectHandler {
	return &ProjectHandler{service: s}
}

func (h *ProjectHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	list, total, err := h.service.List(c.UserContext(), page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, list, page, limit, total)
}

func (h *ProjectHandler) Get(c *fiber.Ctx) error {
	p, err := h.service.Get(c.UserContext(), c.Params("id"))
	if err != nil {
		return response.NotFound(c, "Project not found")
	}
	return response.Success(c, p)
}

func (h *ProjectHandler) Create(c *fiber.Ctx) error {
	var req models.CreateProjectRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	p, err := h.service.Create(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, p)
}

// Provision is the atomic create-project-with-services endpoint. Preferred
// over the split Create + AddService flow because it rolls back a
// half-created project on failure instead of leaving a stranded row that
// future retries collide with.
//
// A failed install/build step (a problem in the operator's own code, e.g.
// a missing import that breaks tsc) returns 422 Unprocessable Entity with
// the full ANSI-stripped build output in the error details. Everything
// else (DB failure, disk full, clone auth, ...) stays 500.
func (h *ProjectHandler) Provision(c *fiber.Ctx) error {
	var req models.ProvisionProjectRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	res, err := h.service.Provision(c.UserContext(), &req)
	if err != nil {
		if pe, ok := err.(*services.ProvisionError); ok {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
				"success": false,
				"error": fiber.Map{
					"code":    "BUILD_FAILED",
					"message": fmt.Sprintf("Service %q: %s failed — %s", pe.ServiceName, pe.Build.Stage, pe.Build.Summary),
					"details": fiber.Map{
						"service": pe.ServiceName,
						"stage":   pe.Build.Stage,
						"summary": pe.Build.Summary,
						"output":  pe.Build.Details,
					},
				},
			})
		}
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, res)
}

func (h *ProjectHandler) Update(c *fiber.Ctx) error {
	var req models.UpdateProjectRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	p, err := h.service.Update(c.UserContext(), c.Params("id"), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, p)
}

func (h *ProjectHandler) Delete(c *fiber.Ctx) error {
	if err := h.service.Delete(c.UserContext(), c.Params("id")); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Project deleted", nil)
}

// Export returns the project's portable deploy manifest as a downloadable
// JSON file. Content-Disposition is set so the browser saves directly to
// disk instead of pretty-printing in a new tab — operators routinely
// re-import without ever opening the file, and a download-by-default
// keeps the secrets-bearing payload (env vars) off the screen.
//
// Auth: gated by the projects-group `deploy.manage` permission so the
// payload can't be exfiltrated by a read-only role.
func (h *ProjectHandler) Export(c *fiber.Ctx) error {
	manifest, err := h.service.Export(c.UserContext(), c.Params("id"))
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	// File-safe slug: the project's Name might contain spaces or
	// punctuation, so strip back to alnum + dash for the download
	// name. The actual project slug isn't always safe either (e.g.
	// "waapi-dev-3-0") but it's the closest stable identifier.
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return '-'
		}
	}, manifest.Project.Name)
	if slug == "" {
		slug = "project"
	}
	c.Set("Content-Type", "application/json; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.deploy.json"`, slug))
	return c.JSON(manifest)
}

// Import provisions a new project from a previously-exported manifest.
// Delegates to the service-level Provision pipeline (via ProjectService.Import),
// so atomic rollback, slug allocation, and webhook secret generation behave
// identically to a manual New Project wizard run.
//
// A failed install/build step in any of the manifest's services bubbles up
// here as a *ProvisionError — we map it to 422 Unprocessable Entity with
// the ANSI-stripped build output in the details field, same shape as
// Provision so the WHM import modal can render the build log without
// special-casing the error path.
func (h *ProjectHandler) Import(c *fiber.Ctx) error {
	var req models.ImportProjectRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid JSON body", nil)
	}
	res, err := h.service.Import(c.UserContext(), &req)
	if err != nil {
		if pe, ok := err.(*services.ProvisionError); ok {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
				"success": false,
				"error": fiber.Map{
					"code":    "BUILD_FAILED",
					"message": fmt.Sprintf("Service %q: %s failed — %s", pe.ServiceName, pe.Build.Stage, pe.Build.Summary),
					"details": fiber.Map{
						"service": pe.ServiceName,
						"stage":   pe.Build.Stage,
						"summary": pe.Build.Summary,
						"output":  pe.Build.Details,
					},
				},
			})
		}
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, res)
}

func (h *ProjectHandler) RotatePAT(c *fiber.Ctx) error {
	var req models.RotatePATRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	if err := h.service.RotatePAT(c.UserContext(), c.Params("id"), req.GitHubPAT); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "PAT rotated", nil)
}

// RegenerateWebhookSecret mints a fresh HMAC secret for this project's
// auto-deploy webhook and returns the new plaintext value. Surfaces the
// secret in the response body so the UI can render a copy button with
// the warning "Old secret is gone — paste this new one into GitHub".
//
// Same auth posture as WebhookInfo / RotatePAT: any caller who already
// has write access to the project (vendor_owner or the project's
// owning tenant) can rotate it.
func (h *ProjectHandler) RegenerateWebhookSecret(c *fiber.Ctx) error {
	newSecret, err := h.service.RegenerateWebhookSecret(c.UserContext(), c.Params("id"))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, fiber.Map{
		"secret": newSecret,
		// url stays stable; surfacing it alongside the new secret means
		// the UI can render one "Copy URL + Secret pair" hint with both
		// values without a second round-trip.
		"url": h.service.GetWebhookURL(c.Params("id")),
	})
}

// WebhookInfo returns the copy-paste URL + raw secret so the UI can render
// both. The secret is only readable by admins who can already see everything
// else about this project; withholding it would block the whole auto-deploy
// setup flow.
func (h *ProjectHandler) WebhookInfo(c *fiber.Ctx) error {
	id := c.Params("id")
	secret, err := h.service.GetWebhookSecret(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "Project not found")
	}
	return response.Success(c, fiber.Map{
		"url":    h.service.GetWebhookURL(id),
		"secret": secret,
	})
}

func (h *ProjectHandler) ListServices(c *fiber.Ctx) error {
	list, err := h.service.ListServices(c.UserContext(), c.Params("id"))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, list)
}

// ListAllServices returns every Deploy Software service the caller can
// see, flat across all projects. Designed for "give me one paginated
// inventory of every service I run" workflows — dashboards, infra
// audits, the External API. Tenant scoping mirrors the standard
// Projects list, so a vendor can never see another tenant's services.
//
// Query: page, limit, search (matches service name OR primary_domain).
func (h *ProjectHandler) ListAllServices(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 50)
	search := c.Query("search")
	list, total, err := h.service.ListAllServices(c.UserContext(), page, limit, search)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, list, page, limit, total)
}

// Activity returns the aggregate activity payload for the project's
// "Activity" card in the WHM detail drawer.
//
// Query: ?limit=N — caps the `recent` slice. Default 10, max 500.
// The lifetime total/successful/failed counters are exact regardless
// of limit (server-side CountDocuments), so the UI can render
// "47 of 50" headers reliably even when only 10 rows ship.
func (h *ProjectHandler) Activity(c *fiber.Ctx) error {
	a, err := h.service.Activity(c.UserContext(), c.Params("id"), c.QueryInt("limit", 10))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, a)
}

// LatestDeployment returns the most recent ProjectDeployment record for a
// given service — including the per-step timeline + progress. Polled by the
// "Deploy in progress" detail drawer in the WHM UI.
func (h *ProjectHandler) LatestDeployment(c *fiber.Ctx) error {
	dep, err := h.service.LatestDeployment(c.UserContext(), c.Params("svc"))
	if err != nil {
		return response.NotFound(c, "no deployment found")
	}
	return response.Success(c, dep)
}

func (h *ProjectHandler) AddService(c *fiber.Ctx) error {
	var req models.AddServiceRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	svc, err := h.service.AddService(c.UserContext(), c.Params("id"), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, svc)
}

// BulkAddServices (POST /whm/projects/:id/services/bulk) accepts a
// CSV / XLSX upload and dispatches each row through the same
// AddService pipeline the single-create form uses — clone, framework
// preset apply, install / build, port allocation, systemd unit,
// nginx vhost, Let's Encrypt expansion. Per-row failures don't abort
// the batch; the response carries a result table the WHM UI renders
// so the operator sees exactly which rows need fixing.
//
// Multipart form fields:
//
//	file — required; .csv or .xlsx; max 10 MB (matches the domain
//	       bulk-upload contract, generous for a 1000-row sheet)
//
// On success returns 200 with BulkServicesUploadResponse (partial
// success is normal — a 200 may include rows with success=false).
// Returns 400 only on parser failures (missing required columns,
// malformed file) — the per-row outcomes belong inside the 200.
func (h *ProjectHandler) BulkAddServices(c *fiber.Ctx) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return response.BadRequest(c, "file is required (multipart field 'file')", nil)
	}
	if fh.Size > 10*1024*1024 {
		return response.BadRequest(c, "file too large (max 10 MB)", nil)
	}
	f, err := fh.Open()
	if err != nil {
		return response.BadRequest(c, "could not open uploaded file: "+err.Error(), nil)
	}
	defer f.Close()

	resp, err := h.service.BulkAddServicesFromContentType(
		c.UserContext(), c.Params("id"), f, fh.Header.Get("Content-Type"), fh.Filename,
	)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, resp)
}

// BulkAddServicesTemplate (GET /whm/projects/:id/services/bulk/template)
// returns a CSV / XLSX with the canonical column headers + three
// example rows showing the common layouts (backend with explicit port,
// static frontend on apex, minimal Next.js row with everything else
// derived from the preset). Generated from code so the column set
// stays in lock-step with AddServiceRequest.
func (h *ProjectHandler) BulkAddServicesTemplate(c *fiber.Ctx) error {
	format := strings.ToLower(strings.TrimSpace(c.Query("format", "csv")))
	if format == "xlsx" || format == "excel" {
		buf, err := services.BulkAddServicesXLSXTemplate()
		if err != nil {
			return response.InternalError(c, "build xlsx template: "+err.Error())
		}
		c.Set("Content-Type", services.MimeForFormat(services.BulkUploadFormatXLSX))
		c.Set("Content-Disposition", `attachment; filename="`+services.BulkAddServicesXLSXTemplateName()+`"`)
		return c.Send(buf)
	}
	c.Set("Content-Type", services.MimeForFormat(services.BulkUploadFormatCSV))
	c.Set("Content-Disposition", `attachment; filename="`+services.BulkAddServicesCSVTemplateName()+`"`)
	return c.Send(services.BulkAddServicesCSVTemplate())
}

// ExportServices (GET /whm/projects/:id/services/export) downloads a
// portable JSON snapshot of the project's services. Strips host paths,
// systemd unit names, and per-instance runtime state so the manifest is
// safe to re-import on the same or a different panel. Sets a
// Content-Disposition attachment so the browser saves the file directly
// instead of rendering env-vars on screen.
func (h *ProjectHandler) ExportServices(c *fiber.Ctx) error {
	out, err := h.service.ExportServices(c.UserContext(), c.Params("id"))
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return '-'
		}
	}, out.ProjectSlug)
	if slug == "" {
		slug = "project"
	}
	c.Set("Content-Type", "application/json; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.services.json"`, slug))
	return c.JSON(out)
}

// servicesJSONBody is the wire shape the import/edit endpoints accept.
// Accepts either `{services: [...]}` (the full export envelope) OR a
// bare top-level array — the latter is what an operator who copies the
// `services` field out of an existing manifest is going to send, and
// rejecting it for not having the envelope is rude UX. The Import /
// Edit handlers unwrap both shapes transparently.
type servicesJSONBody struct {
	SchemaVersion int                             `json:"schema_version"`
	Services      []services.ProjectServiceExport `json:"services"`
}

// parseServicesJSONBody reads the request body as either the wrapped
// envelope shape OR a bare array. Returns the services slice on
// success, or a clear "what was expected" error message.
func parseServicesJSONBody(c *fiber.Ctx) ([]services.ProjectServiceExport, error) {
	raw := c.Body()
	if len(raw) == 0 {
		return nil, errors.New("request body is empty — expected a JSON object with a 'services' array or a bare array")
	}
	// Try the envelope shape first (the export's own emit format).
	var env servicesJSONBody
	if err := json.Unmarshal(raw, &env); err == nil && len(env.Services) > 0 {
		return env.Services, nil
	}
	// Fallback: bare array.
	var arr []services.ProjectServiceExport
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		return arr, nil
	}
	return nil, errors.New("could not read services from body — expected `{\"services\": [...]}` or a bare array")
}

// BulkAddServicesJSON (POST /whm/projects/:id/services/import-json)
// runs every entry in a JSON manifest through AddService. Same per-row
// outcome shape as the CSV/XLSX upload so the WHM UI's result-table
// renderer doesn't need to special-case the format. Incoming `id`
// fields are ignored — fresh services are minted for every row.
func (h *ProjectHandler) BulkAddServicesJSON(c *fiber.Ctx) error {
	svcs, err := parseServicesJSONBody(c)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	resp, err := h.service.BulkAddServicesFromJSON(c.UserContext(), c.Params("id"), svcs)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, resp)
}

// BulkEditServicesJSON (PUT /whm/projects/:id/services/bulk-edit)
// runs every entry in a JSON manifest through UpdateService. Each
// entry MUST carry an `id` field whose service belongs to :id;
// cross-project IDs are rejected per-row without leaking which
// project they came from. Missing/optional fields leave the existing
// service value untouched (UpdateServiceRequest uses pointer fields
// to distinguish "omitted" from "cleared").
func (h *ProjectHandler) BulkEditServicesJSON(c *fiber.Ctx) error {
	svcs, err := parseServicesJSONBody(c)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	resp, err := h.service.BulkUpdateServicesFromJSON(c.UserContext(), c.Params("id"), svcs)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, resp)
}

func (h *ProjectHandler) UpdateService(c *fiber.Ctx) error {
	var req models.UpdateServiceRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	svc, err := h.service.UpdateService(c.UserContext(), c.Params("svc"), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, svc)
}

func (h *ProjectHandler) RemoveService(c *fiber.Ctx) error {
	if err := h.service.RemoveService(c.UserContext(), c.Params("svc")); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Service removed", nil)
}

func (h *ProjectHandler) DeployService(c *fiber.Ctx) error {
	// Per-service Redeploy skips git pull by default — git pull is
	// project-level only (one shared clone). Caller can opt back in by
	// passing ?pull=1 (used by webhook auto-deploy + project-level
	// Deploy all to refresh source first).
	skipPull := c.Query("pull") != "1"
	if err := h.service.DeployService(c.Params("svc"), "manual", skipPull); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Deploy queued", nil)
}

// ServiceAction handles start/stop/restart for a single service via
// POST /projects/:id/services/:svc/action/:action. Kept separate from
// DeployService (above) because those actions don't rebuild — they only
// toggle the systemd unit's run state.
func (h *ProjectHandler) ServiceAction(c *fiber.Ctx) error {
	action := c.Params("action")
	if err := h.service.ServiceAction(c.UserContext(), c.Params("svc"), action); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "action "+action+" completed", nil)
}

// ProjectAction fans out start/stop/restart across every backend service in
// the project. Useful for "put the whole project to sleep" or "restart after
// a config change" without having to click through each service.
func (h *ProjectHandler) ProjectAction(c *fiber.Ctx) error {
	action := c.Params("action")
	if err := h.service.ProjectAction(c.UserContext(), c.Params("id"), action); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "project "+action+" completed", nil)
}

// Pause / Resume are explicit for clarity in the UI, even though both could
// be folded into the generic Update endpoint. Separate endpoints also give
// us a natural audit-log action name.
func (h *ProjectHandler) Pause(c *fiber.Ctx) error {
	if err := h.service.SetPaused(c.UserContext(), c.Params("id"), true); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "auto-deploy paused", nil)
}

func (h *ProjectHandler) Resume(c *fiber.Ctx) error {
	if err := h.service.SetPaused(c.UserContext(), c.Params("id"), false); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "auto-deploy resumed", nil)
}

func (h *ProjectHandler) DeployAll(c *fiber.Ctx) error {
	if err := h.service.DeployAll(c.UserContext(), c.Params("id")); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Deploy all queued", nil)
}

func (h *ProjectHandler) AddAlias(c *fiber.Ctx) error {
	var req models.AddAliasRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	svc, err := h.service.AddAliasWithProject(c.UserContext(), c.Params("id"), c.Params("svc"), req.Domain)
	if err != nil {
		return mapAliasErr(c, err)
	}
	return response.Success(c, svc)
}

func (h *ProjectHandler) RemoveAlias(c *fiber.Ctx) error {
	svc, err := h.service.RemoveAliasWithProject(c.UserContext(), c.Params("id"), c.Params("svc"), c.Params("domain"))
	if err != nil {
		return mapAliasErr(c, err)
	}
	return response.Success(c, svc)
}

// mapAliasErr translates the alias-link sentinel errors raised by
// ProjectService into HTTP status codes so the panel UI's toast reads
// "Forbidden" / "Not found" instead of an opaque 500.
func mapAliasErr(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, services.ErrServiceNotFound),
		errors.Is(err, services.ErrProjectNotFound):
		return response.NotFound(c, err.Error())
	case errors.Is(err, services.ErrServiceProjectMismatch),
		errors.Is(err, services.ErrCrossTenantProject),
		errors.Is(err, services.ErrLinkedDomainNotOwned):
		return response.Forbidden(c, err.Error())
	}
	return response.InternalError(c, err.Error())
}

func (h *ProjectHandler) Logs(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 5)
	logs, err := h.service.GetDeploymentLogs(c.UserContext(), c.Params("svc"), limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, logs)
}

// Webhook is the public, signature-verified endpoint GitHub posts to. Signature
// verification happens inside the service layer so handler stays tiny. Always
// returns 200 on accepted/no-op to prevent GitHub from retrying; returns 400
// only on truly malformed requests (missing project_id, bad JSON framing).
func (h *ProjectHandler) Webhook(c *fiber.Ctx) error {
	sig := c.Get("X-Hub-Signature-256")
	eventType := c.Get("X-GitHub-Event")
	projectID := c.Params("project_id")
	body := c.Body()
	if projectID == "" {
		// Only truly malformed requests return 4xx — GitHub retries 4xx
		// deliveries aggressively, and a bad URL is the sort of thing an
		// operator wants to see via the delivery-failed badge in GitHub's
		// webhook UI.
		return response.BadRequest(c, "project_id is required", nil)
	}
	if err := h.service.HandleWebhook(c.UserContext(), projectID, sig, eventType, body); err != nil {
		// Signature mismatches, missing projects, and malformed payloads
		// all return 200 with a body describing the reason. Rationale:
		// GitHub treats any non-2xx as a failed delivery and retries on
		// a backoff curve — a project with a rotated secret would flood
		// delivery history with retries forever. We want the operator to
		// see "Last delivery: failed" in our UI (via the LastWebhookAt
		// staying empty) rather than in GitHub's.
		return c.JSON(fiber.Map{"success": false, "ignored": err.Error()})
	}
	return response.SuccessMessage(c, "ok", nil)
}
