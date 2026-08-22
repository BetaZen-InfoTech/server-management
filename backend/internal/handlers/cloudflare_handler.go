package handlers

import (
	"errors"

	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/cloudflare"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// cfRecordBody is the JSON payload for create/update record. Proxied + Priority
// are pointers so "unset" is distinguishable (the service forces mail records
// DNS-only regardless).
type cfRecordBody struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  *bool  `json:"proxied"`
	Priority *int   `json:"priority"`
}

func (b cfRecordBody) toParams() cloudflare.RecordParams {
	return cloudflare.RecordParams{
		Type:     b.Type,
		Name:     b.Name,
		Content:  b.Content,
		TTL:      b.TTL,
		Proxied:  b.Proxied,
		Priority: b.Priority,
	}
}

// CloudflareHandler exposes the centralized Cloudflare account configuration.
// Routes live under /api/v1/whm/config/cloudflare and are gated on
// server.manage — the same bar as the SMTP secret, since the API token is a
// platform-level credential. The token is AES-GCM encrypted at rest and never
// echoed back: GET returns has_token + a masked preview only.
type CloudflareHandler struct {
	service *services.CloudflareService
	sync    *services.CloudflareSyncJobService
}

func NewCloudflareHandler(s *services.CloudflareService) *CloudflareHandler {
	return &CloudflareHandler{service: s}
}

// SetSyncService wires the sync-job service (constructed after this handler in
// main.go). Kept a setter so NewCloudflareHandler's signature stays one-arg.
func (h *CloudflareHandler) SetSyncService(s *services.CloudflareSyncJobService) { h.sync = s }

// callerIDs pulls the owner + tenant ObjectIDs off the request locals (set by
// the auth middleware as hex strings). Zero values on parse failure — the job
// still runs; these are metadata/scoping only.
func callerIDs(c *fiber.Ctx) (owner, tenant primitive.ObjectID) {
	if s, ok := c.Locals("user_id").(string); ok {
		if id, err := primitive.ObjectIDFromHex(s); err == nil {
			owner = id
		}
	}
	if s, ok := c.Locals("tenant_id").(string); ok {
		if id, err := primitive.ObjectIDFromHex(s); err == nil {
			tenant = id
		}
	}
	return owner, tenant
}

// Get returns the UI-safe view (no token — has_token + masked preview only).
func (h *CloudflareHandler) Get(c *fiber.Ctx) error {
	view, err := h.service.Get(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, view)
}

// Save upserts the config. Empty api_token keeps the existing encrypted token.
func (h *CloudflareHandler) Save(c *fiber.Ctx) error {
	var req services.SaveCloudflareRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	view, err := h.service.Save(c.UserContext(), &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, view)
}

// Test verifies the stored token against Cloudflare's read-only token-verify
// endpoint and records the verdict so the UI can show connection status.
func (h *CloudflareHandler) Test(c *fiber.Ctx) error {
	view, err := h.service.TestConnection(c.UserContext())
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, view)
}

// cfErr maps service errors to a clear 400 (disabled / no-token / Cloudflare
// API error). All of these are operator-actionable, not server faults.
func cfErr(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, services.ErrCloudflareDisabled):
		return response.BadRequest(c, "Cloudflare integration is disabled — enable it in Settings → Cloudflare", nil)
	case errors.Is(err, services.ErrCloudflareNoToken):
		return response.BadRequest(c, "Cloudflare API token is not configured", nil)
	default:
		return response.BadRequest(c, err.Error(), nil)
	}
}

// ListZones returns every zone the account token can see (read-only).
func (h *CloudflareHandler) ListZones(c *fiber.Ctx) error {
	zones, err := h.service.ListZones(c.UserContext())
	if err != nil {
		return cfErr(c, err)
	}
	return response.Success(c, fiber.Map{"zones": zones})
}

// GetZone returns the Cloudflare zone (id + status + nameservers) for a domain,
// or {found:false} when the account has no zone for it (read-only).
func (h *CloudflareHandler) GetZone(c *fiber.Ctx) error {
	zone, err := h.service.FindZone(c.UserContext(), c.Params("domain"))
	if err != nil {
		return cfErr(c, err)
	}
	if zone == nil {
		return response.Success(c, fiber.Map{"found": false})
	}
	return response.Success(c, fiber.Map{"found": true, "zone": zone})
}

// ListRecords returns a domain's Cloudflare zone + DNS records (read-only).
func (h *CloudflareHandler) ListRecords(c *fiber.Ctx) error {
	zone, recs, err := h.service.ListRecords(c.UserContext(), c.Params("domain"))
	if err != nil {
		return cfErr(c, err)
	}
	if zone == nil {
		return response.Success(c, fiber.Map{"found": false, "records": []any{}})
	}
	return response.Success(c, fiber.Map{"found": true, "zone": zone, "records": recs})
}

// Compare returns the read-only local↔Cloudflare reconciliation for a domain.
func (h *CloudflareHandler) Compare(c *fiber.Ctx) error {
	res, err := h.service.Compare(c.UserContext(), c.Params("domain"))
	if err != nil {
		return cfErr(c, err)
	}
	return response.Success(c, res)
}

// Connect finds-or-creates the Cloudflare zone for a domain (never duplicates)
// and stamps the mapping onto the local zone row. WRITE.
func (h *CloudflareHandler) Connect(c *fiber.Ctx) error {
	res, err := h.service.ConnectDomain(c.UserContext(), c.Params("domain"))
	if err != nil {
		return cfErr(c, err)
	}
	return response.Success(c, res)
}

// CreateRecord adds a DNS record to a domain's Cloudflare zone. WRITE.
func (h *CloudflareHandler) CreateRecord(c *fiber.Ctx) error {
	var b cfRecordBody
	if err := c.BodyParser(&b); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	rec, err := h.service.CreateRecord(c.UserContext(), c.Params("domain"), b.toParams())
	if err != nil {
		return cfErr(c, err)
	}
	return response.Success(c, rec)
}

// UpdateRecord replaces a record in a domain's Cloudflare zone. WRITE.
func (h *CloudflareHandler) UpdateRecord(c *fiber.Ctx) error {
	var b cfRecordBody
	if err := c.BodyParser(&b); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	rec, err := h.service.UpdateRecord(c.UserContext(), c.Params("domain"), c.Params("id"), b.toParams())
	if err != nil {
		return cfErr(c, err)
	}
	return response.Success(c, rec)
}

// DeleteRecord removes a record from a domain's Cloudflare zone. Destructive —
// requires ?confirm=true (the human-approval gate). WRITE.
func (h *CloudflareHandler) DeleteRecord(c *fiber.Ctx) error {
	confirm := c.Query("confirm") == "true"
	if err := h.service.DeleteRecord(c.UserContext(), c.Params("domain"), c.Params("id"), confirm); err != nil {
		return cfErr(c, err)
	}
	return response.SuccessMessage(c, "Record deleted", nil)
}

// EnableDomain turns Cloudflare ON for a single domain (WHM). WRITE.
func (h *CloudflareHandler) EnableDomain(c *fiber.Ctx) error {
	if err := h.service.SetDomainCloudflareEnabled(c.UserContext(), c.Params("domain"), true); err != nil {
		return cfErr(c, err)
	}
	return response.SuccessMessage(c, "Cloudflare enabled for domain", nil)
}

// DisableDomain turns Cloudflare OFF for a single domain (WHM). Does NOT delete
// the Cloudflare zone and does NOT affect any other domain. WRITE.
func (h *CloudflareHandler) DisableDomain(c *fiber.Ctx) error {
	if err := h.service.SetDomainCloudflareEnabled(c.UserContext(), c.Params("domain"), false); err != nil {
		return cfErr(c, err)
	}
	return response.SuccessMessage(c, "Cloudflare disabled for domain (zone not deleted)", nil)
}

// CheckNameservers does a live delegation check (DNS NS lookup vs Cloudflare's
// assigned nameservers + zone status) and returns a state.
func (h *CloudflareHandler) CheckNameservers(c *fiber.Ctx) error {
	res, err := h.service.CheckNameservers(c.UserContext(), c.Params("domain"))
	if err != nil {
		return cfErr(c, err)
	}
	return response.Success(c, res)
}

// VerifyDomain triggers Cloudflare's activation check for the domain's zone
// (the "Verify domain" button) and returns the refreshed delegation status.
func (h *CloudflareHandler) VerifyDomain(c *fiber.Ctx) error {
	res, err := h.service.VerifyDomain(c.UserContext(), c.Params("domain"))
	if err != nil {
		return cfErr(c, err)
	}
	return response.Success(c, res)
}

// ReconcileAll connects (find/create zone) + syncs every eligible primary
// domain — the existing-domain backfill. Background job; poll like a sync job.
func (h *CloudflareHandler) ReconcileAll(c *fiber.Ctx) error {
	if h.sync == nil {
		return response.InternalError(c, "sync service unavailable")
	}
	var b syncBody
	_ = c.BodyParser(&b)
	owner, tenant := callerIDs(c)
	job, err := h.sync.StartReconcileAll(c.UserContext(), b.ApplyDeletes, owner, tenant)
	if err != nil {
		return cfErr(c, err)
	}
	return response.Success(c, job)
}

// syncBody carries the one destructive knob: apply_deletes removes Cloudflare
// records absent locally (mail records excluded). Default false = create/update
// only.
type syncBody struct {
	ApplyDeletes bool `json:"apply_deletes"`
}

// SyncDomain starts a background local→Cloudflare sync for one domain. Returns
// the job immediately; the UI polls GET .../sync/jobs/:id for live progress.
func (h *CloudflareHandler) SyncDomain(c *fiber.Ctx) error {
	if h.sync == nil {
		return response.InternalError(c, "sync service unavailable")
	}
	var b syncBody
	_ = c.BodyParser(&b)
	owner, tenant := callerIDs(c)
	job, err := h.sync.StartSyncDomain(c.UserContext(), c.Params("domain"), b.ApplyDeletes, owner, tenant)
	if err != nil {
		return cfErr(c, err)
	}
	return response.Success(c, job)
}

// SyncAll starts a background sync over every Cloudflare-connected domain.
func (h *CloudflareHandler) SyncAll(c *fiber.Ctx) error {
	if h.sync == nil {
		return response.InternalError(c, "sync service unavailable")
	}
	var b syncBody
	_ = c.BodyParser(&b)
	owner, tenant := callerIDs(c)
	job, err := h.sync.StartSyncAll(c.UserContext(), b.ApplyDeletes, owner, tenant)
	if err != nil {
		return cfErr(c, err)
	}
	return response.Success(c, job)
}

// GetSyncJob returns a job for polling.
func (h *CloudflareHandler) GetSyncJob(c *fiber.Ctx) error {
	if h.sync == nil {
		return response.InternalError(c, "sync service unavailable")
	}
	id, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "Invalid job id", nil)
	}
	job, err := h.sync.GetSyncJob(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "Job not found")
	}
	return response.Success(c, job)
}

// ListSyncJobs returns recent jobs for the history list.
func (h *CloudflareHandler) ListSyncJobs(c *fiber.Ctx) error {
	if h.sync == nil {
		return response.InternalError(c, "sync service unavailable")
	}
	jobs, err := h.sync.ListSyncJobs(c.UserContext(), 20)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, fiber.Map{"jobs": jobs})
}

// CancelSyncJob requests cooperative cancellation of a running job.
func (h *CloudflareHandler) CancelSyncJob(c *fiber.Ctx) error {
	if h.sync == nil {
		return response.InternalError(c, "sync service unavailable")
	}
	id, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "Invalid job id", nil)
	}
	if err := h.sync.CancelSyncJob(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Cancellation requested", nil)
}
