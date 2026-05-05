package handlers

import (
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type EmailHandler struct {
	service *services.EmailService
}

func NewEmailHandler(s *services.EmailService) *EmailHandler {
	return &EmailHandler{service: s}
}

func (h *EmailHandler) ListMailboxes(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	domain := c.Query("domain")
	mailboxes, total, err := h.service.ListMailboxes(c.UserContext(), domain, page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, mailboxes, page, limit, total)
}

func (h *EmailHandler) GetMailbox(c *fiber.Ctx) error {
	id := c.Params("id")
	m, err := h.service.GetMailbox(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "Mailbox not found")
	}
	return response.Success(c, m)
}

func (h *EmailHandler) CreateMailbox(c *fiber.Ctx) error {
	var req models.CreateMailboxRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	m, err := h.service.CreateMailbox(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, m)
}

func (h *EmailHandler) UpdateMailbox(c *fiber.Ctx) error {
	id := c.Params("id")
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	m, err := h.service.UpdateMailbox(c.UserContext(), id, body)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, m)
}

func (h *EmailHandler) DeleteMailbox(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.DeleteMailbox(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Mailbox deleted", nil)
}

func (h *EmailHandler) ListForwarders(c *fiber.Ctx) error {
	domain := c.Query("domain")
	fwds, err := h.service.ListForwarders(c.UserContext(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, fwds)
}

func (h *EmailHandler) CreateForwarder(c *fiber.Ctx) error {
	var req models.EmailForwarder
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	f, err := h.service.CreateForwarder(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, f)
}

func (h *EmailHandler) DeleteForwarder(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.DeleteForwarder(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Forwarder deleted", nil)
}

func (h *EmailHandler) UpdateSpamSettings(c *fiber.Ctx) error {
	domain := c.Params("domain")
	var req models.SpamSettings
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	req.Domain = domain
	if err := h.service.UpdateSpamSettings(c.UserContext(), &req); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Spam settings updated", nil)
}

func (h *EmailHandler) SetupDKIM(c *fiber.Ctx) error {
	domain := c.Params("domain")
	result, err := h.service.SetupDKIM(c.UserContext(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, result)
}

func (h *EmailHandler) WebmailToken(c *fiber.Ctx) error {
	var req struct {
		Email string `json:"email" validate:"required,email"`
	}
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	token, err := h.service.GenerateWebmailToken(c.UserContext(), req.Email)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, map[string]string{
		"token": token,
		"url":   "/webmail/sso.php?token=" + token,
	})
}

// SendTest sends a test message from a panel-managed mailbox through
// local Postfix submission with SMTP-AUTH. Returns 200 with the full
// SMTP exchange trace on success, 422 with the same trace on failure —
// whichever side the break is on (auth, relay, DNS), the trace is in
// the response body so the operator can diagnose without digging
// through /var/log/mail.log.
func (h *EmailHandler) SendTest(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		To string `json:"to" validate:"required,email"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(body); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	trace, err := h.service.SendTest(c.UserContext(), id, body.To)
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"success": false,
			"error": fiber.Map{
				"code":    "SMTP_TEST_FAILED",
				"message": err.Error(),
				"details": fiber.Map{"trace": trace},
			},
		})
	}
	return response.Success(c, fiber.Map{"trace": trace})
}

// ReconcileConfig rewrites the Dovecot + Postfix wiring on the VPS to
// match what EmailService expects. One-shot fix for servers installed
// with an earlier fragile sed-based setup; harmless to run on a
// working server since every step is idempotent. Platform-owner only.
func (h *EmailHandler) ReconcileConfig(c *fiber.Ctx) error {
	log, err := h.service.ReconcileConfig(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, fiber.Map{"log": log})
}

// ─────────────────────────────────────────────────────────────────────
// Bulk operations — Export / Upload / Delete (OTP-gated)
// ─────────────────────────────────────────────────────────────────────

// BulkUploadTemplate returns a sample CSV/XLSX an operator downloads,
// edits in Excel / Numbers, and re-uploads. Generated from code so
// adding a new column is one edit in email_bulk_service.go, not a
// stale asset on disk.
func (h *EmailHandler) BulkUploadTemplate(c *fiber.Ctx) error {
	format := strings.ToLower(strings.TrimSpace(c.Query("format", "csv")))
	if format == "xlsx" || format == "excel" {
		buf, err := services.BulkMailboxUploadXLSXTemplate()
		if err != nil {
			return response.InternalError(c, "build xlsx template: "+err.Error())
		}
		c.Set("Content-Type", services.MimeForFormat(services.BulkUploadFormatXLSX))
		c.Set("Content-Disposition", `attachment; filename="`+services.BulkMailboxUploadXLSXTemplateName()+`"`)
		return c.Send(buf)
	}
	c.Set("Content-Type", services.MimeForFormat(services.BulkUploadFormatCSV))
	c.Set("Content-Disposition", `attachment; filename="`+services.BulkMailboxUploadCSVTemplateName()+`"`)
	return c.Send(services.BulkMailboxUploadCSVTemplate())
}

// BulkUpload (POST /email/bulk-upload) accepts a CSV/XLSX file with
// one row per mailbox. Blank password column → server-generates a
// secure password, returned in the response. Per-row failures don't
// abort the loop; the operator gets a result table.
//
// Multipart form field: file (required, .csv or .xlsx).
func (h *EmailHandler) BulkUpload(c *fiber.Ctx) error {
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
	resp, err := h.service.BulkUploadMailboxesFromContentType(
		c.UserContext(), f, fh.Header.Get("Content-Type"), fh.Filename,
	)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, resp)
}

// Export (GET /email/export?format=csv|xlsx&ids=<csv>&all=true&token=<otp-token>)
// returns the selected mailboxes (or all visible ones when all=true)
// as a CSV / XLSX download.
//
// The `token` query param is the OTP confirmation token from
// /email/bulk-export/confirm. When present + valid, the export
// includes the `password` column with AES-decrypted plaintexts.
// Without the token, the export still works but the password column
// is omitted — operator gets the directory data without the secrets.
func (h *EmailHandler) Export(c *fiber.Ctx) error {
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

	// Password reveal gate — only honored when the OTP query param
	// resolves to a valid, unexpired, kind=export confirmation tied
	// to the calling user. Failure modes (bad token, wrong code
	// already, expired, attempt cap hit) return a 400 with the
	// service-supplied reason so the UI can surface it; the operator
	// never gets a silently-stripped password column without
	// feedback.
	includePassword := false
	if rawTok := strings.TrimSpace(c.Query("token")); rawTok != "" {
		uidStr, _ := c.Locals("user_id").(string)
		uid, err := primitive.ObjectIDFromHex(uidStr)
		if err != nil {
			return response.Unauthorized(c, "missing user context")
		}
		code := strings.TrimSpace(c.Query("code"))
		if code == "" {
			return response.BadRequest(c, "code is required when token is supplied", nil)
		}
		confirmedIDs, cerr := h.service.ConfirmBulkMailboxExport(c.UserContext(), uid, rawTok, code)
		if cerr != nil {
			return response.BadRequest(c, cerr.Error(), nil)
		}
		ids = confirmedIDs
		all = false
		includePassword = true
	}

	rows, err := h.service.FetchMailboxesForExport(c.UserContext(), ids, all, includePassword)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	if format == "xlsx" || format == "excel" {
		buf, xerr := services.ExportMailboxesXLSX(rows, includePassword)
		if xerr != nil {
			return response.InternalError(c, "build xlsx: "+xerr.Error())
		}
		c.Set("Content-Type", services.MimeForFormat(services.BulkUploadFormatXLSX))
		c.Set("Content-Disposition", `attachment; filename="`+services.MailboxExportFilenameXLSX()+`"`)
		return c.Send(buf)
	}
	c.Set("Content-Type", services.MimeForFormat(services.BulkUploadFormatCSV))
	c.Set("Content-Disposition", `attachment; filename="`+services.MailboxExportFilenameCSV()+`"`)
	return c.Send(services.ExportMailboxesCSV(rows, includePassword))
}

// BulkDeleteRequestOTP is step 1 of the OTP-gated bulk-delete flow.
// Body: { "ids": ["<hex>", ...] }
// Returns: token + email + preview list.
func (h *EmailHandler) BulkDeleteRequestOTP(c *fiber.Ctx) error {
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
	res, err := h.service.RequestBulkMailboxDeleteOTP(c.UserContext(), uid, body.IDs)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, res)
}

// BulkDeleteConfirm is step 2: verify code + run DeleteMailbox per
// id. Body: { "token": "...", "code": "123456" }.
func (h *EmailHandler) BulkDeleteConfirm(c *fiber.Ctx) error {
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
	res, err := h.service.ConfirmBulkMailboxDelete(c.UserContext(), uid, body.Token, body.Code)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, res)
}

// BulkExportRequestOTP is step 1 of the password-reveal flow. Body
// shape mirrors BulkDeleteRequestOTP — same selection list, same
// outcome shape. Step 2 is implicit: the Export endpoint accepts
// the resulting token + code as query params.
func (h *EmailHandler) BulkExportRequestOTP(c *fiber.Ctx) error {
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
	res, err := h.service.RequestBulkMailboxExportOTP(c.UserContext(), uid, body.IDs)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, res)
}
