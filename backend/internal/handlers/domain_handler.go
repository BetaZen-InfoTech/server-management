package handlers

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type DomainHandler struct {
	service *services.DomainService
	db      *mongo.Database
}

func NewDomainHandler(s *services.DomainService, db *mongo.Database) *DomainHandler {
	return &DomainHandler{service: s, db: db}
}

func (h *DomainHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	search := c.Query("search")
	domains, total, err := h.service.List(c.UserContext(), page, limit, search)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	// Stamp each row with the owning vendor's reg email so the SSL
	// page's "Issue Certificate" modal can autofill the email field
	// without an extra round-trip per domain. Best-effort; transient.
	h.service.EnrichOwnerEmails(c.UserContext(), domains)
	return response.Paginated(c, domains, page, limit, total)
}

func (h *DomainHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	domain, err := h.service.GetByID(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "Domain not found")
	}
	return response.Success(c, domain)
}

func (h *DomainHandler) Create(c *fiber.Ctx) error {
	var req models.CreateDomainRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	domain, err := h.service.Create(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, domain)
}

func (h *DomainHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	domain, err := h.service.Update(c.UserContext(), id, body)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, domain)
}

func (h *DomainHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if err := c.BodyParser(&body); err != nil || !body.Confirm {
		return response.BadRequest(c, "Confirmation required: set confirm=true", nil)
	}
	if err := h.service.Delete(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Domain deleted", nil)
}

func (h *DomainHandler) Suspend(c *fiber.Ctx) error {
	id := c.Params("id")
	force := c.QueryBool("force", false)
	if err := h.service.Suspend(c.UserContext(), id, force); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Domain suspended", nil)
}

func (h *DomainHandler) Unsuspend(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Unsuspend(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Domain unsuspended", nil)
}

func (h *DomainHandler) SwitchPHP(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		PHPVersion string `json:"php_version" validate:"required"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.SwitchPHP(c.UserContext(), id, body.PHPVersion); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "PHP version switched", nil)
}

// SetDocumentRoot changes the nginx `root` directive for a domain in
// place. PATCH /whm/domains/:id/document-root with body
// `{"document_root": "/absolute/path"}`. An empty document_root resets
// to the cPanel-default /home/<user>/domains/<domain>/public_html.
func (h *DomainHandler) SetDocumentRoot(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		DocumentRoot string `json:"document_root"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.SetDocumentRoot(c.UserContext(), id, body.DocumentRoot); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Document root updated", nil)
}

func (h *DomainHandler) Stats(c *fiber.Ctx) error {
	id := c.Params("id")
	stats, err := h.service.GetStats(c.UserContext(), id)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, stats)
}

func (h *DomainHandler) ListOwn(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	domains, total, err := h.service.ListByUser(c.UserContext(), userID, page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	// Same enrichment as the WHM List — for the User Panel this
	// almost always resolves to the caller's own email (vendor_admin
	// owning their own domains), but staff/customer views still get
	// the vendor reg email so the SSL modal autofills consistently.
	h.service.EnrichOwnerEmails(c.UserContext(), domains)
	return response.Paginated(c, domains, page, limit, total)
}

// UpdateRegistration handles PATCH /domains/:id/registration — patches
// just the registrar / purchase / expiry / nameserver fields without
// touching anything else on the record. Separate modal = separate
// endpoint so accidental saves can't overwrite PHP version or quotas.
func (h *DomainHandler) UpdateRegistration(c *fiber.Ctx) error {
	id := c.Params("id")
	var req models.UpdateRegistrationRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body", nil)
	}
	domain, err := h.service.UpdateRegistration(c.UserContext(), id, &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, domain)
}

// ExpiringSoon (GET /domains/expiring?days=30) feeds the dashboard
// widget. Defaults to 30 days when the query param is absent or zero.
// Returns domains sorted by nearest expiry, capped at 100 rows so a
// dashboard render never pulls the whole inventory.
func (h *DomainHandler) ExpiringSoon(c *fiber.Ctx) error {
	days := c.QueryInt("days", 30)
	domains, err := h.service.ExpiringSoon(c.UserContext(), days)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, domains)
}

// ExpiringBuckets (GET /domains/expiring/buckets) returns the
// canonical bucket ladder PLUS the count of visible domains expiring
// within each one. Drives the filter-pill row on the dashboard's
// "Domains Expiring Soon" widget so the operator sees "60: 12, 30:
// 5, 7: 1" without having to fetch the list ten times. Buckets are
// cumulative (60-day count includes everything in 30/15/7) — same
// semantics as ExpiringSoon's `days` param.
//
// Response shape:
//
//	{
//	  "buckets":  [60, 45, 30, 15, 7, 5, 4, 3, 2, 1],
//	  "counts":   {"60": 12, "45": 9, "30": 5, ...}
//	}
func (h *DomainHandler) ExpiringBuckets(c *fiber.Ctx) error {
	buckets := services.ExpiryBuckets()
	counts, err := h.service.ExpiringBucketCounts(c.UserContext(), buckets)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	// JSON object keys must be strings; convert int → string so the
	// frontend can use Object lookup directly.
	countsStr := make(map[string]int, len(counts))
	for k, v := range counts {
		countsStr[strconv.Itoa(k)] = v
	}
	return response.Success(c, fiber.Map{
		"buckets": buckets,
		"counts":  countsStr,
	})
}

// WhoisLookup (POST /domains/:id/whois-refresh) runs the `whois`
// binary against the stored domain name and returns the parsed
// fields. We return — but don't auto-save — the result so the
// operator can review and confirm before overwriting manually-
// entered purchase / expiry dates.
func (h *DomainHandler) WhoisLookup(c *fiber.Ctx) error {
	id := c.Params("id")
	// Load the domain so the caller doesn't have to pass the name
	// separately (avoids mismatch between URL id and payload domain).
	domain, err := h.service.GetByID(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "domain not found")
	}
	result, err := h.service.WhoisLookup(c.UserContext(), domain.Domain)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, result)
}

// BulkRefreshRegistration (POST /domains/whois-refresh-bulk) re-runs
// WHOIS for every selected domain (or every visible domain when
// `all=true`) and OVERWRITES the panel's stored registrar /
// registered_on / expires_on / nameservers fields. Non-destructive
// (re-running with the same target set is idempotent), no OTP gate
// — same posture as the single-row /whois-refresh endpoint above.
//
// Body: { ids?: string[], all?: bool }
//   - ids:  selected row ids; ignored when all=true
//   - all:  refresh every domain the caller can see
//
// Tenant scope flows through CallerScope inside the service so a
// vendor caller only sees + mutates their own domains.
func (h *DomainHandler) BulkRefreshRegistration(c *fiber.Ctx) error {
	var body struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "invalid request body", nil)
	}
	res, err := h.service.BulkRefreshRegistration(c.UserContext(), body.IDs, body.All)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, res)
}

// WhoisLookupByName (GET /domains/whois?domain=<name>) runs the whois
// lookup on a raw domain name that hasn't been added to the panel yet.
// Used by the Add Domain modal to auto-fill registrar + expiry + purchase
// date the moment the operator finishes typing the name, so they don't
// have to copy+paste from their registrar's dashboard.
//
// No DB read, no tenant scoping — the domain isn't ours yet, and whois
// data is public information anyway. We only gate on the caller being
// an authenticated WHM user (route-level auth), so random internet
// traffic can't burn CPU + whois-server-quota with bulk lookups.
func (h *DomainHandler) WhoisLookupByName(c *fiber.Ctx) error {
	name := strings.ToLower(strings.TrimSpace(c.Query("domain")))
	if name == "" {
		return response.BadRequest(c, "domain query parameter is required", nil)
	}
	// Basic sanity check so a garbage input doesn't invoke `whois` on
	// something weird. Allow standard RFC-1035 + internationalised
	// punycode (xn--) domains up to 253 chars.
	if !whoisDomainRe.MatchString(name) {
		return response.BadRequest(c, "domain doesn't look like a valid name", nil)
	}
	result, err := h.service.WhoisLookup(c.UserContext(), name)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, result)
}

// whoisDomainRe rejects anything that isn't clearly a DNS name.
// Accepts 1+ labels separated by dots; each label is alnum + dashes
// (no leading/trailing dash), up to 63 chars. Whole string capped at
// 253 chars as per DNS spec.
var whoisDomainRe = regexp.MustCompile(`^(?i)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

// cpanelUsername resolves the caller's system username from the
// authenticated user_id stored on the Fiber context. The cPanel UI
// doesn't expose `user` in its Add Domain form — every domain a
// cPanel user creates is implicitly theirs — so we auto-fill it
// server-side. Kept inline here rather than shared because the only
// other consumer (ssh_key_handler) already has its own copy.
func (h *DomainHandler) cpanelUsername(c *fiber.Ctx) (string, error) {
	userID := c.Locals("user_id").(string)
	oid, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return "", err
	}
	var user models.User
	if err := h.db.Collection(database.ColUsers).FindOne(c.UserContext(), bson.M{"_id": oid}).Decode(&user); err != nil {
		return "", err
	}
	return user.Username, nil
}

// Preflight (POST /domains/preflight) runs WHOIS + DNS A / NS / MX +
// IP-match + firewall checks against a domain that hasn't been added
// yet. The Add Domain modal calls this on debounce while the operator
// types so they can see the per-check verdict before committing.
//
// Body: {"domain":"example.com"}. No tenant scoping — none of the
// data we read is panel-private (whois + DNS are public, the firewall
// check is server-wide). Auth is enforced by the route middleware.
func (h *DomainHandler) Preflight(c *fiber.Ctx) error {
	var body struct {
		Domain string `json:"domain"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	name := strings.ToLower(strings.TrimSpace(body.Domain))
	if name == "" {
		return response.BadRequest(c, "domain is required", nil)
	}
	if !whoisDomainRe.MatchString(name) {
		return response.BadRequest(c, "domain doesn't look like a valid name", nil)
	}
	res := h.service.RunPreflight(c.UserContext(), name)
	return response.Success(c, res)
}

// Recheck (POST /domains/:id/recheck) re-runs the same preflight set
// against an existing domain row and persists the resolved DNS / IP
// fields back onto the doc. The domain name is read from the URL
// :id row — the request body is empty.
func (h *DomainHandler) Recheck(c *fiber.Ctx) error {
	id := c.Params("id")
	domain, err := h.service.GetByID(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "Domain not found")
	}
	res := h.service.RunPreflight(c.UserContext(), domain.Domain)

	// Persist the resolved snapshot back onto the doc so the dashboard
	// reflects the latest state without waiting for the next preflight.
	oid, err := primitive.ObjectIDFromHex(id)
	if err == nil {
		now := time.Now()
		set := bson.M{
			"domain_type":       res.DomainType,
			"ip_matches_server": res.IPMatchesServer,
			"last_checked_at":   res.CheckedAt,
			"updated_at":        now,
		}
		if len(res.ResolvedIPs) > 0 {
			set["resolved_ip"] = res.ResolvedIPs[0]
		}
		if len(res.Nameservers) > 0 {
			set["nameservers"] = res.Nameservers
		}
		h.db.Collection(database.ColDomains).UpdateByID(c.UserContext(), oid, bson.M{"$set": set})
	}
	return response.Success(c, res)
}

// CPanelCreate (POST /cpanel/domains) is the cPanel-side add-domain
// entrypoint. The cPanel form only asks for `domain` (and optionally
// `type`); `user` is auto-filled from the authenticated session and
// PHPVersion defaults to 8.2 so the validator's `oneof` constraint
// passes. Everything else (quotas, etc.) is left zero — the service
// layer applies package-based defaults.
func (h *DomainHandler) CPanelCreate(c *fiber.Ctx) error {
	username, err := h.cpanelUsername(c)
	if err != nil {
		return response.InternalError(c, "Failed to resolve user")
	}
	var body struct {
		Domain     string `json:"domain"`
		Type       string `json:"type"`
		PHPVersion string `json:"php_version"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if strings.TrimSpace(body.Domain) == "" {
		return response.BadRequest(c, "domain is required", nil)
	}
	if body.PHPVersion == "" {
		body.PHPVersion = "8.2"
	}
	req := models.CreateDomainRequest{
		Domain:     body.Domain,
		User:       username,
		PHPVersion: body.PHPVersion,
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	domain, err := h.service.Create(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, domain)
}

// BulkUpload (POST /domains/bulk-upload) accepts a CSV or XLSX file
// containing one row per domain and creates each one through the same
// DomainService.Create + SSL pipeline that the single-domain form
// uses. Per-row failures don't abort the loop — the response carries
// a result table the WHM/User Panel UI renders so the operator can
// see exactly which lines need fixing.
//
// Multipart form fields:
//
//	file       — required; .csv or .xlsx
//	issue_ssl  — optional; "true" (default) issues Let's Encrypt
//	             cert per row after create
//	force_ssl  — optional; "true" (default) flips force-HTTPS on for
//	             each successfully-issued domain
//	php_default — optional; default "8.2", used when a row's
//	             php_version cell is blank
//
// On the cPanel surface the row's `user` cell is IGNORED — every
// uploaded row is created under the authenticated caller's username
// so a vendor can't reach outside their tenant via a doctored CSV.
// On WHM the `user` cell is REQUIRED (and must name a valid linux
// user / vendor) — the WHM operator is platform-owner and chooses.
func (h *DomainHandler) BulkUpload(c *fiber.Ctx) error {
	return h.bulkUpload(c, "")
}

// CPanelBulkUpload mirrors BulkUpload on /api/v1/cpanel/domains/bulk-upload.
// The only difference is that the per-row `user` column is overridden
// with the authenticated cPanel caller's username — same tenant
// scoping the single-create CPanelCreate enforces.
func (h *DomainHandler) CPanelBulkUpload(c *fiber.Ctx) error {
	username, err := h.cpanelUsername(c)
	if err != nil {
		return response.InternalError(c, "Failed to resolve user")
	}
	return h.bulkUpload(c, username)
}

// bulkUpload is the shared body of BulkUpload + CPanelBulkUpload.
// callerUsername == "" → WHM (per-row `user` from the file is honored).
// callerUsername != "" → cPanel (clobbers the per-row `user`).
func (h *DomainHandler) bulkUpload(c *fiber.Ctx, callerUsername string) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return response.BadRequest(c, "file is required (multipart field 'file')", nil)
	}
	// Hard cap on body size — a CSV/XLSX of 10MB is comfortably more
	// than any operator's domain catalog. Without this an attacker
	// could OOM the panel by uploading a multi-GB sheet.
	if fh.Size > 10*1024*1024 {
		return response.BadRequest(c, "file too large (max 10 MB)", nil)
	}
	f, err := fh.Open()
	if err != nil {
		return response.BadRequest(c, "could not open uploaded file: "+err.Error(), nil)
	}
	defer f.Close()

	opts := services.DefaultBulkUploadOptions()
	opts.CallerUsername = callerUsername
	if v := strings.TrimSpace(c.FormValue("php_default")); v != "" {
		opts.PHPDefault = v
	}
	if v := strings.TrimSpace(c.FormValue("issue_ssl")); v != "" {
		opts.IssueSSL = strings.EqualFold(v, "true") || v == "1"
	}
	if v := strings.TrimSpace(c.FormValue("force_ssl")); v != "" {
		opts.ForceSSL = strings.EqualFold(v, "true") || v == "1"
	}

	resp, err := h.service.BulkUploadFromContentType(
		c.UserContext(), f, fh.Header.Get("Content-Type"), fh.Filename, opts,
	)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, resp)
}

// BulkDeleteRequestOTP (POST /whm/domains/bulk-delete/request-otp)
// is step 1 of the WHM-only OTP-gated bulk delete flow. The handler
// passes the caller's userID + the selected domain ids to the
// service; the service mails a 6-digit code to the admin's email
// and returns an opaque token the client holds for step 2.
//
// Body: { "ids": ["<hex>", "<hex>", ...] }
//
// The route is gated by middleware.RequireRole("vendor_owner") so
// reaching this handler at all means the caller is the platform
// owner. Tenant scoping isn't relevant — vendor_owner sees and can
// delete every domain on the box.
func (h *DomainHandler) BulkDeleteRequestOTP(c *fiber.Ctx) error {
	uidStr, _ := c.Locals("user_id").(string)
	uid, err := primitive.ObjectIDFromHex(uidStr)
	if err != nil {
		return response.Unauthorized(c, "missing user context")
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "invalid request body", nil)
	}
	res, err := h.service.RequestBulkDeleteOTP(c.UserContext(), uid, body.IDs)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, res)
}

// BulkDeleteConfirm (POST /whm/domains/bulk-delete/confirm) is
// step 2: verify the OTP and run Delete in a loop. Returns a
// per-row result table (mirrors BulkUpload's shape).
//
// Body: { "token": "...", "code": "123456" }
func (h *DomainHandler) BulkDeleteConfirm(c *fiber.Ctx) error {
	uidStr, _ := c.Locals("user_id").(string)
	uid, err := primitive.ObjectIDFromHex(uidStr)
	if err != nil {
		return response.Unauthorized(c, "missing user context")
	}
	var body struct {
		Token string `json:"token"`
		Code  string `json:"code"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "invalid request body", nil)
	}
	if strings.TrimSpace(body.Token) == "" || strings.TrimSpace(body.Code) == "" {
		return response.BadRequest(c, "token and code are required", nil)
	}
	res, err := h.service.ConfirmBulkDelete(c.UserContext(), uid, body.Token, body.Code)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, res)
}

// Export (GET /domains/export?format=csv|xlsx&ids=<csv>&all=true)
// returns the selected domains (or every domain visible to the
// caller when all=true / ids is empty + all=true) as a CSV or XLSX
// download. Same column shape as the bulk-upload template — round-
// trip clean so an operator can export, edit in Excel, and re-upload
// via the Bulk Upload modal without column re-mapping.
//
// Security: GET request, but it's a list of domain config rows the
// caller already has read access to. Tenant scoping for cPanel
// callers is enforced inside FetchDomainsForExport via CallerScope.
//
// `all=true` is a separate flag (not "ids empty means all") so a
// JS bug that forgets to send the selection list can't accidentally
// dump every tenant's domains. Empty ids + all=false → empty file.
func (h *DomainHandler) Export(c *fiber.Ctx) error {
	format := strings.ToLower(strings.TrimSpace(c.Query("format", "csv")))
	all := strings.EqualFold(c.Query("all"), "true") || c.Query("all") == "1"
	idsParam := strings.TrimSpace(c.Query("ids", ""))
	var ids []string
	if idsParam != "" {
		for _, raw := range strings.Split(idsParam, ",") {
			id := strings.TrimSpace(raw)
			if id != "" {
				ids = append(ids, id)
			}
		}
	}
	rows, err := h.service.FetchDomainsForExport(c.UserContext(), ids, all)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	if format == "xlsx" || format == "excel" {
		buf, xerr := services.ExportDomainsXLSX(rows)
		if xerr != nil {
			return response.InternalError(c, "build xlsx: "+xerr.Error())
		}
		c.Set("Content-Type", services.MimeForFormat(services.BulkUploadFormatXLSX))
		c.Set("Content-Disposition", `attachment; filename="`+services.ExportFilenameXLSX()+`"`)
		return c.Send(buf)
	}
	c.Set("Content-Type", services.MimeForFormat(services.BulkUploadFormatCSV))
	c.Set("Content-Disposition", `attachment; filename="`+services.ExportFilenameCSV()+`"`)
	return c.Send(services.ExportDomainsCSV(rows))
}

// BulkUploadTemplate (GET /domains/bulk-upload/template?format=csv|xlsx)
// returns a sample spreadsheet with the right column headers + two
// example rows (one fully populated, one minimal). Shared between
// Surface detection:
//   * /api/v1/whm/...    → operator picks each domain's owner via
//                          the `user` column. Template includes it.
//   * /api/v1/cpanel/... → vendor uploads. Backend force-overrides
//                          `user` to the authenticated caller, so
//                          including the column in the template is
//                          confusing — the cPanel template omits it
//                          and the parser silently tolerates either
//                          shape (column absent OR column present
//                          but contents discarded).
//
// We keep the template generated FROM CODE (not a static file) so a
// future field added to CreateDomainRequest is one edit in
// domain_bulk_service.go — not a forgotten asset on disk.
func (h *DomainHandler) BulkUploadTemplate(c *fiber.Ctx) error {
	format := strings.ToLower(strings.TrimSpace(c.Query("format", "csv")))
	omitUser := strings.HasPrefix(c.Path(), "/api/v1/cpanel/")
	if format == "xlsx" || format == "excel" {
		buf, err := services.BulkUploadXLSXTemplate(omitUser)
		if err != nil {
			return response.InternalError(c, "build xlsx template: "+err.Error())
		}
		c.Set("Content-Type", services.MimeForFormat(services.BulkUploadFormatXLSX))
		c.Set("Content-Disposition", `attachment; filename="`+services.BulkUploadXLSXTemplateName()+`"`)
		return c.Send(buf)
	}
	c.Set("Content-Type", services.MimeForFormat(services.BulkUploadFormatCSV))
	c.Set("Content-Disposition", `attachment; filename="`+services.BulkUploadCSVTemplateName()+`"`)
	return c.Send(services.BulkUploadCSVTemplate(omitUser))
}

// CPanelDelete (DELETE /cpanel/domains/:id) is the body-less delete
// counterpart for the cPanel UI. The WHM-side Delete requires an
// explicit `confirm=true` because an operator can nuke any tenant's
// domain; here the caller can only reach their own rows (ListOwn +
// service-layer AssertOwns), so a URL-based DELETE is enough.
func (h *DomainHandler) CPanelDelete(c *fiber.Ctx) error {
	if err := h.service.Delete(c.UserContext(), c.Params("id")); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Domain deleted", nil)
}
