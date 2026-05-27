package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type SSLHandler struct {
	service *services.SSLService
	// domains is wired post-construction via SetDomainService so the
	// existing NewSSLHandler call sites in main.go don't need to know
	// about the dep. Used only by BulkForceSSL — every other handler
	// here works on cert rows directly.
	domains *services.DomainService
}

func NewSSLHandler(s *services.SSLService) *SSLHandler {
	return &SSLHandler{service: s}
}

// SetDomainService wires the DomainService dep used by BulkForceSSL —
// see the handler comment below for why we need both services here.
// Optional; the bulk endpoint refuses with a 500 if it isn't wired
// (so an under-construction main.go can't accidentally ship a
// half-wired bulk action).
func (h *SSLHandler) SetDomainService(d *services.DomainService) {
	h.domains = d
}

func (h *SSLHandler) List(c *fiber.Ctx) error {
	certs, err := h.service.List(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, certs)
}

func (h *SSLHandler) Get(c *fiber.Ctx) error {
	domain := c.Params("domain")
	cert, err := h.service.GetByDomain(c.UserContext(), domain)
	if err != nil {
		return response.NotFound(c, "Certificate not found")
	}
	return response.Success(c, cert)
}

func (h *SSLHandler) IssueLetsEncrypt(c *fiber.Ctx) error {
	var req models.IssueLetsEncryptRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	cert, err := h.service.IssueLetsEncrypt(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, cert)
}

// IssueLetsEncryptBulk handles the multi-domain SSL request fired by
// the SSL page's "Issue X Certificates" button.
//
// v3.1.60 changed this from a synchronous "wait for the whole batch"
// POST to a job-id + poll model — pre-3.1.60 the operator stared at a
// "Reissuing 27..." spinner for 10 minutes with no visibility into
// which domain was running, which succeeded, or which failed. Now:
//
//   1. This handler validates the request, calls StartBulkIssueJob,
//      and returns 202 Accepted with { job_id, total, status: queued }.
//   2. A detached goroutine inside StartBulkIssueJob walks the domain
//      list and writes per-row outcomes to Mongo as each certbot run
//      completes.
//   3. The frontend polls GET /ssl/bulk-jobs/:id every ~1.5s and
//      renders the live results table + progress bar.
//
// Returns 202 (Accepted) instead of 200 (OK) because the work isn't
// finished by the time we respond — RFC 7231 §6.3.3 explicitly fits.
// Older API consumers expecting 200 still get a 2xx, just a 202.
func (h *SSLHandler) IssueLetsEncryptBulk(c *fiber.Ctx) error {
	var req models.IssueLetsEncryptBulkRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	// Resolve the caller's user_id + tenant_id from the auth
	// middleware-stamped locals so the goroutine's tenant scope works
	// even after the HTTP request ends.
	var ownerOID, tenantOID primitive.ObjectID
	if uidStr, ok := c.Locals("user_id").(string); ok {
		if oid, err := primitive.ObjectIDFromHex(uidStr); err == nil {
			ownerOID = oid
		}
	}
	if tidStr, ok := c.Locals("tenant_id").(string); ok {
		if oid, err := primitive.ObjectIDFromHex(tidStr); err == nil {
			tenantOID = oid
		}
	}
	job, err := h.service.StartBulkIssueJob(c.UserContext(), ownerOID, tenantOID, &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"success": true,
		"data": models.IssueLetsEncryptBulkStartResponse{
			JobID:  job.ID.Hex(),
			Total:  job.Total,
			Status: job.Status,
		},
	})
}

// BulkIssueJobStatus is the poll endpoint the frontend hits every
// ~1.5s while a bulk issuance is in flight. Returns the full
// SSLBulkJob doc — status, total, success/failed counts, current
// domain, progress %, and per-row items.
//
// Tenant scope is enforced inside the service layer: a vendor can
// only see their own jobs. "Not found" and "not authorised" fold
// into a single 404 so a vendor can't probe other tenants' job IDs.
func (h *SSLHandler) BulkIssueJobStatus(c *fiber.Ctx) error {
	jobID := c.Params("id")
	var callerOID primitive.ObjectID
	if uidStr, ok := c.Locals("user_id").(string); ok {
		if oid, err := primitive.ObjectIDFromHex(uidStr); err == nil {
			callerOID = oid
		}
	}
	job, err := h.service.GetBulkIssueJob(c.UserContext(), jobID, callerOID)
	if err != nil {
		return response.NotFound(c, "ssl bulk job not found")
	}
	return response.Success(c, job)
}

// BulkIssueJobCancel sets the cancel flag on an in-flight job. The
// detached goroutine sees the flag between rows and exits cleanly.
// Already-issued rows are NOT rolled back — certbot writes are
// idempotent and the operator's intent on Cancel is "stop spending
// rate-limit slots on remaining domains", not "undo what already
// landed".
//
// Idempotent: cancelling a completed/cancelled/failed job is a
// no-op + success, so a double-click can't error.
func (h *SSLHandler) BulkIssueJobCancel(c *fiber.Ctx) error {
	jobID := c.Params("id")
	var callerOID primitive.ObjectID
	if uidStr, ok := c.Locals("user_id").(string); ok {
		if oid, err := primitive.ObjectIDFromHex(uidStr); err == nil {
			callerOID = oid
		}
	}
	if err := h.service.CancelBulkIssueJob(c.UserContext(), jobID, callerOID); err != nil {
		return response.NotFound(c, "ssl bulk job not found")
	}
	return response.SuccessMessage(c, "cancellation requested", nil)
}

func (h *SSLHandler) UploadCustom(c *fiber.Ctx) error {
	var req models.UploadCustomCertRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	cert, err := h.service.UploadCustom(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, cert)
}

func (h *SSLHandler) Renew(c *fiber.Ctx) error {
	domain := c.Params("domain")
	cert, err := h.service.Renew(c.UserContext(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, cert)
}

// Reissue forces a fresh Let's Encrypt certificate for an existing
// domain — operator-initiated when they need a new cert NOW, not
// when the renewal cron next fires. Same response shape as Renew so
// the UI can pipe both buttons through the same toast logic.
//
// Errors land as 400 (not 500) when they're certbot-friendly messages
// — DNS-not-pointing-here, rate-limit-exceeded, etc. — so the
// frontend's tryExtractBuildError-style banner can show actionable
// text instead of a generic "Internal server error".
func (h *SSLHandler) Reissue(c *fiber.Ctx) error {
	domain := c.Params("domain")
	cert, err := h.service.Reissue(c.UserContext(), domain)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, cert)
}

func (h *SSLHandler) Revoke(c *fiber.Ctx) error {
	domain := c.Params("domain")
	if err := h.service.Revoke(c.UserContext(), domain); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Certificate revoked", nil)
}

func (h *SSLHandler) ForceSSL(c *fiber.Ctx) error {
	domain := c.Params("domain")
	var req struct {
		Enable bool `json:"enable"`
	}
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.ForceSSL(c.UserContext(), domain, req.Enable); err != nil {
		return response.InternalError(c, err.Error())
	}
	msg := "Force SSL enabled"
	if !req.Enable {
		msg = "Force SSL disabled"
	}
	return response.SuccessMessage(c, msg, nil)
}

// BulkForceSSL (POST /ssl/force-ssl-bulk) flips the force_ssl flag
// (HTTPS-only redirect) on every selected domain (or every visible
// domain when `all=true`). Domains without a live cert are SKIPPED
// — turning on Force HTTPS for an HTTP-only domain would 502 it.
//
// Body: { ids?: string[], all?: bool, enable: bool }
//   - ids:     selected row ids (uses cert.domain to resolve to a
//              domain row). Ignored when all=true.
//   - all:     hit every domain the caller can see.
//   - enable:  true to turn ON Force HTTPS, false to revert.
//
// The bulk service lives in DomainService (it owns the tenant-scoped
// domain list); this handler just hands the request off to it with
// our SSLService passed in for the per-row ForceSSL call.
func (h *SSLHandler) BulkForceSSL(c *fiber.Ctx) error {
	if h.domains == nil {
		return response.InternalError(c, "bulk force-ssl is not wired (DomainService dep missing)")
	}
	var body struct {
		IDs    []string `json:"ids"`
		All    bool     `json:"all"`
		Enable bool     `json:"enable"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "invalid request body", nil)
	}
	res, err := h.domains.BulkForceSSL(c.UserContext(), body.IDs, body.All, body.Enable, h.service)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, res)
}

func (h *SSLHandler) Delete(c *fiber.Ctx) error {
	domain := c.Params("domain")
	if err := h.service.Delete(c.UserContext(), domain); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Certificate deleted", nil)
}
