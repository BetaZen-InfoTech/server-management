package handlers

import (
	"fmt"

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
func (h *ProjectHandler) Activity(c *fiber.Ctx) error {
	a, err := h.service.Activity(c.UserContext(), c.Params("id"))
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
	svc, err := h.service.AddAlias(c.UserContext(), c.Params("svc"), req.Domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, svc)
}

func (h *ProjectHandler) RemoveAlias(c *fiber.Ctx) error {
	svc, err := h.service.RemoveAlias(c.UserContext(), c.Params("svc"), c.Params("domain"))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, svc)
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
