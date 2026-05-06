package services

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	mathrand "math/rand"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/mailer"
	"github.com/rs/zerolog/log"
	"github.com/xuri/excelize/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// ─────────────────────────────────────────────────────────────────────
// Bulk Email Operations
// ─────────────────────────────────────────────────────────────────────
//
// This file mirrors domain_bulk_service.go but for the mailbox
// surface. Three flows ride here:
//
//   1. Bulk Export — CSV / XLSX dump of selected mailboxes (or all).
//      Sensitive: when includePassword=true, the operator's
//      AES-decrypted plaintext lands in the file. Gated on a fresh
//      OTP confirmation (same shape as Bulk Delete) so a leaked
//      session cookie can't silently exfiltrate every customer
//      password.
//
//   2. Bulk Upload — CSV / XLSX with one row per mailbox. Blank
//      password column = auto-generate (returned in the response so
//      the operator can hand them out). Idempotent on duplicate
//      email addresses (skipped, reported in the per-row result).
//
//   3. Bulk Delete — OTP-gated multi-mailbox removal. Two-step flow
//      (request-otp + confirm) identical to domains' Bulk Delete,
//      reusing the same crypto + hashing helpers.
//
// All three honour the existing tenant scope (vendor_admin can only
// touch their own domains' mailboxes). Vendor scoping is enforced
// inside the service layer via CallerScope, NOT via separate routes.

// ─────────────────────────────────────────────────────────────────────
// Bulk Export
// ─────────────────────────────────────────────────────────────────────

// ExportableMailbox is the shape every export row gets flattened to.
// Password is the plaintext AES-decrypted from EncryptedPass — empty
// when (a) the operator didn't request it, (b) the mailbox row is
// pre-3.x.x without an encrypted_pass blob, or (c) the panel's
// JWT_SECRET differs from the one that encrypted the row (post-
// transfer with no re-encrypt yet).
type ExportableMailbox struct {
	Email            string  `json:"email"`
	Password         string  `json:"password,omitempty"`
	Domain           string  `json:"domain"`
	Username         string  `json:"username"` // local-part of the address
	QuotaMB          int     `json:"quota_mb"`
	UsedMB           float64 `json:"used_mb"`
	SendLimitPerHour int     `json:"send_limit_per_hour"`
	CreatedAt        string  `json:"created_at"`
}

func (e ExportableMailbox) row(includePassword bool) []string {
	row := []string{
		e.Email,
		e.Domain,
		e.Username,
		fmt.Sprintf("%d", e.QuotaMB),
		fmt.Sprintf("%.2f", e.UsedMB),
		fmt.Sprintf("%d", e.SendLimitPerHour),
		e.CreatedAt,
	}
	if includePassword {
		row = append(row, e.Password)
	}
	return row
}

func mailboxExportHeader(includePassword bool) []string {
	h := []string{"email", "domain", "username", "quota_mb", "used_mb", "send_limit_per_hour", "created_at"}
	if includePassword {
		h = append(h, "password")
	}
	return h
}

// FetchMailboxesForExport pulls the requested mailbox rows (or all
// when listAll=true) and decrypts EncryptedPass into Password when
// includePassword=true. Tenant scope is enforced via CallerScope:
// a non-owner caller's resulting set is filtered to mailboxes whose
// domain is in their tenant's domain list.
func (s *EmailService) FetchMailboxesForExport(ctx context.Context, ids []string, listAll bool, includePassword bool) ([]ExportableMailbox, error) {
	col := s.db.Collection(database.ColMailboxes)
	filter := bson.M{}

	if !listAll && len(ids) > 0 {
		objIDs := make([]primitive.ObjectID, 0, len(ids))
		for _, raw := range ids {
			if oid, err := primitive.ObjectIDFromHex(raw); err == nil {
				objIDs = append(objIDs, oid)
			}
		}
		if len(objIDs) == 0 {
			return []ExportableMailbox{}, nil
		}
		filter["_id"] = bson.M{"$in": objIDs}
	}

	// Tenant scope — vendor_admin / vendor_staff see only their own
	// domains' mailboxes. Owner role passes unrestricted.
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyDomainScope(ctx, s.db, "domain", filter)
	}

	cur, err := col.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list mailboxes: %w", err)
	}
	defer cur.Close(ctx)

	var rows []models.Mailbox
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("decode mailboxes: %w", err)
	}

	out := make([]ExportableMailbox, 0, len(rows))
	for _, m := range rows {
		username := m.Email
		if at := strings.IndexByte(m.Email, '@'); at > 0 {
			username = m.Email[:at]
		}
		ex := ExportableMailbox{
			Email:            m.Email,
			Domain:           m.Domain,
			Username:         username,
			QuotaMB:          m.QuotaMB,
			UsedMB:           m.UsedMB,
			SendLimitPerHour: m.SendLimitPerHour,
			CreatedAt:        m.CreatedAt.UTC().Format(time.RFC3339),
		}
		if includePassword && m.EncryptedPass != "" && s.jwtSecret != "" {
			if plain, err := decryptPassword(m.EncryptedPass, s.jwtSecret); err == nil {
				ex.Password = plain
			}
			// Decrypt failure is non-fatal — leaves Password empty so
			// the export still lands. Common cause: post-transfer
			// mailbox not yet re-encrypted under this panel's secret.
		}
		out = append(out, ex)
	}
	return out, nil
}

// ExportMailboxesCSV writes RFC 4180 CSV. Header column set is
// dynamic — `password` only appears when the caller successfully
// passed the OTP gate.
func ExportMailboxesCSV(rows []ExportableMailbox, includePassword bool) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write(mailboxExportHeader(includePassword))
	for _, r := range rows {
		w.Write(r.row(includePassword))
	}
	w.Flush()
	return buf.Bytes()
}

// ExportMailboxesXLSX writes an XLSX with the same columns. Frozen
// header row + autofilter so the operator can sort / filter inside
// Excel without a fight.
func ExportMailboxesXLSX(rows []ExportableMailbox, includePassword bool) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	const sheet = "Mailboxes"
	// Default sheet rename, not delete-and-recreate, so the file's
	// internal sheet index stays clean (Excel complains about the
	// hidden default sheet otherwise).
	f.SetSheetName("Sheet1", sheet)

	headers := mailboxExportHeader(includePassword)
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}
	for r, row := range rows {
		for c, val := range row.row(includePassword) {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
			f.SetCellValue(sheet, cell, val)
		}
	}
	// Header style — bold + light fill, mirrors the domain export.
	style, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#E8EEF7"}},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})
	endCell, _ := excelize.CoordinatesToCellName(len(headers), 1)
	f.SetCellStyle(sheet, "A1", endCell, style)
	f.SetPanes(sheet, &excelize.Panes{Freeze: true, Split: false, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, fmt.Errorf("write xlsx: %w", err)
	}
	return buf.Bytes(), nil
}

func MailboxExportFilenameCSV() string  { return "mailboxes-" + time.Now().UTC().Format("2006-01-02") + ".csv" }
func MailboxExportFilenameXLSX() string { return "mailboxes-" + time.Now().UTC().Format("2006-01-02") + ".xlsx" }

// ─────────────────────────────────────────────────────────────────────
// Bulk Upload — CSV / XLSX
// ─────────────────────────────────────────────────────────────────────

// BulkMailboxRowResult is one row's outcome on upload. Mirrors the
// domain version's BulkRowResult; duplicated rather than reused
// because the column shape and password-generation field are
// mailbox-specific.
type BulkMailboxRowResult struct {
	RowNumber         int    `json:"row_number"`
	Email             string `json:"email"`
	Success           bool   `json:"success"`
	Error             string `json:"error,omitempty"`
	GeneratedPassword string `json:"generated_password,omitempty"` // only when the password column was blank → server-generated
	MailboxID         string `json:"mailbox_id,omitempty"`
	// DomainCreated flips true when the row's domain didn't exist at
	// upload time and the bulk uploader auto-provisioned it (WHM-only
	// path; the row's `user` column names the owner). Lets the WHM UI
	// render "Mailbox + Domain created" badges on these rows so the
	// operator notices the side-effect and can adjust DNS / SSL if
	// the auto-defaults aren't what they wanted.
	DomainCreated bool `json:"domain_created,omitempty"`
}

type BulkMailboxUploadResponse struct {
	Format          BulkUploadFormat       `json:"format"`
	TotalRows       int                    `json:"total_rows"`
	Successes       int                    `json:"successes"`
	Failures        int                    `json:"failures"`
	Generated       int                    `json:"generated"`        // # of rows that had blank password → server auto-set
	DomainsCreated  int                    `json:"domains_created"`  // # of rows that triggered an auto-domain-create on WHM
	Items           []BulkMailboxRowResult `json:"items"`
}

// generatedMailboxPassword returns a 16-char URL-safe random
// password for rows whose `password` column is blank. We keep the
// alphabet ambiguity-friendly (no 0/O/I/l) so the operator can read
// out the printed values to a customer over the phone without a
// game of "is that a one or an L".
func generatedMailboxPassword() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789@#$%"
	rng := mathrand.New(mathrand.NewSource(time.Now().UnixNano() ^ int64(mathrand.Int63())))
	b := make([]byte, 16)
	for i := range b {
		b[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(b)
}

// resolveMailboxHeader maps a free-form column header to its
// canonical key. Reuses the same normaliseHeader helper the domain
// upload uses (lowercased, separators stripped).
func resolveMailboxHeader(in string) string {
	n := normaliseHeader(in)
	switch n {
	case "email", "address", "mailbox":
		return "email"
	case "domain", "host":
		return "domain"
	case "password", "pass", "pwd":
		return "password"
	case "quota", "quotamb", "quotamib", "quotamegabytes", "size":
		return "quota_mb"
	case "sendlimit", "sendlimitperhour", "ratelimit", "hourlysend":
		return "send_limit_per_hour"
	case "user", "owner", "vendor", "username":
		return "user"
	}
	return n
}

// executeBulkMailboxRows is the row-level executor shared by the CSV
// and XLSX entry points. The first row is always treated as a
// header; data starts at row 2 (1-indexed for operator-friendly
// error messages).
func (s *EmailService) executeBulkMailboxRows(ctx context.Context, rows [][]string, format BulkUploadFormat) (*BulkMailboxUploadResponse, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("file is empty")
	}
	header := rows[0]
	colIdx := map[string]int{}
	for i, raw := range header {
		colIdx[resolveMailboxHeader(raw)] = i
	}
	if _, ok := colIdx["email"]; !ok {
		return nil, fmt.Errorf("missing required column: email")
	}

	resp := &BulkMailboxUploadResponse{
		Format:    format,
		Items:     make([]BulkMailboxRowResult, 0, len(rows)-1),
		TotalRows: 0,
	}

	get := func(row []string, key string) string {
		i, ok := colIdx[key]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	for i, row := range rows[1:] {
		rowNum := i + 2 // 1-indexed + header row
		if rowAllBlank(row) {
			continue
		}
		resp.TotalRows++
		item := BulkMailboxRowResult{RowNumber: rowNum}

		email := strings.ToLower(get(row, "email"))
		if email == "" {
			item.Success = false
			item.Error = "email is required"
			resp.Items = append(resp.Items, item)
			resp.Failures++
			continue
		}
		item.Email = email

		// Domain falls back to the email's domain part — operator
		// shouldn't have to repeat info we can derive.
		domain := get(row, "domain")
		if domain == "" {
			if at := strings.IndexByte(email, '@'); at > 0 {
				domain = email[at+1:]
			}
		}
		domain = strings.ToLower(domain)
		if domain == "" {
			item.Success = false
			item.Error = "could not determine domain"
			resp.Items = append(resp.Items, item)
			resp.Failures++
			continue
		}

		// Three-step domain gate:
		//   1. Look the domain up in the panel's `domains` collection.
		//   2. If it exists: enforce tenant ownership (cpanel-side
		//      vendors can only operate on their own domains; WHM
		//      owners pass through).
		//   3. If it doesn't exist:
		//        * Tenant-scoped caller (cpanel) → fail clean. They
		//          can't auto-create domains for themselves through
		//          the bulk-mailbox path; that's a Domains-page op.
		//        * Owner caller (WHM) with a `user` column on the
		//          row → auto-provision the domain via DomainService
		//          .Create with sane defaults (PHP 8.2, default
		//          server IP). Then continue with the mailbox.
		//        * Owner caller without `user` column → fail clean
		//          with a message that points at the workaround.
		domainOwner, domainCreated, dgErr := s.bulkResolveDomain(ctx, domain, get(row, "user"))
		if dgErr != nil {
			item.Success = false
			item.Error = dgErr.Error()
			resp.Items = append(resp.Items, item)
			resp.Failures++
			continue
		}
		_ = domainOwner // reserved for a future "place mailbox under owner's home" check
		if domainCreated {
			item.DomainCreated = true
			resp.DomainsCreated++
		}

		password := get(row, "password")
		if password == "" {
			password = generatedMailboxPassword()
			item.GeneratedPassword = password
			resp.Generated++
		}

		quota := atoiSafe(get(row, "quota_mb"))
		sendLimit := atoiSafe(get(row, "send_limit_per_hour"))

		mailbox, err := s.CreateMailbox(ctx, &models.CreateMailboxRequest{
			Email:            email,
			Password:         password,
			Domain:           domain,
			QuotaMB:          quota,
			SendLimitPerHour: sendLimit,
		})
		if err != nil {
			item.Success = false
			item.Error = err.Error()
			// Don't echo a generated password we never persisted —
			// the operator would chase a credential that doesn't
			// exist on the server.
			item.GeneratedPassword = ""
			if password == "" /* impossible — kept for clarity */ {
				resp.Generated--
			}
			resp.Items = append(resp.Items, item)
			resp.Failures++
			continue
		}

		item.Success = true
		item.MailboxID = mailbox.ID.Hex()
		resp.Items = append(resp.Items, item)
		resp.Successes++
	}

	return resp, nil
}

// BulkUploadMailboxesCSV reads RFC 4180 CSV from body and creates
// one mailbox per data row.
func (s *EmailService) BulkUploadMailboxesCSV(ctx context.Context, body io.Reader) (*BulkMailboxUploadResponse, error) {
	r := csv.NewReader(body)
	r.FieldsPerRecord = -1 // tolerate ragged rows; we read by header index
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse csv: %w", err)
	}
	return s.executeBulkMailboxRows(ctx, rows, BulkUploadFormatCSV)
}

// BulkUploadMailboxesXLSX reads .xlsx from body. Reads the first sheet
// only (the template ships a single "Mailboxes" sheet).
func (s *EmailService) BulkUploadMailboxesXLSX(ctx context.Context, body io.Reader) (*BulkMailboxUploadResponse, error) {
	f, err := excelize.OpenReader(body)
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("xlsx contains no sheets")
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("read xlsx rows: %w", err)
	}
	return s.executeBulkMailboxRows(ctx, rows, BulkUploadFormatXLSX)
}

// BulkUploadMailboxesFromContentType picks the right parser based on
// the multipart form's Content-Type header (preferred) or filename
// extension (fallback for callers that omit Content-Type).
func (s *EmailService) BulkUploadMailboxesFromContentType(ctx context.Context, body io.Reader, contentType, filename string) (*BulkMailboxUploadResponse, error) {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(ct, "spreadsheet") || strings.Contains(ct, "officedocument") || strings.HasSuffix(strings.ToLower(filename), ".xlsx") {
		return s.BulkUploadMailboxesXLSX(ctx, body)
	}
	return s.BulkUploadMailboxesCSV(ctx, body)
}

// ─────────────────────────────────────────────────────────────────────
// Upload templates
// ─────────────────────────────────────────────────────────────────────

const (
	mailboxTemplateHeaderEmail        = "email"
	mailboxTemplateHeaderDomain       = "domain"
	mailboxTemplateHeaderPassword     = "password"
	mailboxTemplateHeaderQuotaMB      = "quota_mb"
	mailboxTemplateHeaderSendLimit    = "send_limit_per_hour"
	// `user` is the WHM-only auto-create-domain hook. When the row's
	// domain doesn't exist yet AND the caller is the platform owner,
	// the bulk uploader provisions a fresh domain under this user and
	// then creates the mailbox on it. Cpanel/vendor uploads ignore
	// this column — vendors can only operate on domains they already
	// own, full stop.
	mailboxTemplateHeaderUser = "user"
)

func mailboxTemplateHeaders() []string {
	return []string{
		mailboxTemplateHeaderEmail,
		mailboxTemplateHeaderDomain,
		mailboxTemplateHeaderPassword,
		mailboxTemplateHeaderQuotaMB,
		mailboxTemplateHeaderSendLimit,
		mailboxTemplateHeaderUser,
	}
}

// mailboxTemplateHeadersForSurface returns the column header list,
// dropping the `user` column on cPanel (vendors don't choose the
// owner — it's force-set to the authenticated caller).
func mailboxTemplateHeadersForSurface(omitUser bool) []string {
	full := mailboxTemplateHeaders()
	if !omitUser {
		return full
	}
	out := make([]string, 0, len(full))
	for _, h := range full {
		if h == mailboxTemplateHeaderUser {
			continue
		}
		out = append(out, h)
	}
	return out
}

// BulkMailboxUploadCSVTemplate returns a 1-row example CSV the
// operator downloads via /email/bulk-upload/template?format=csv,
// edits in Excel / Numbers / a text editor, and re-uploads.
//
// omitUser=true is the cPanel/User-Panel variant — vendors never
// pick a domain owner (the panel force-overrides their `user` field
// to the authenticated caller), so the column is omitted to avoid
// confusion. omitUser=false is the WHM admin variant where the
// column drives the auto-create-missing-domain hook.
func BulkMailboxUploadCSVTemplate(omitUser bool) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write(mailboxTemplateHeadersForSurface(omitUser))
	if omitUser {
		// Cpanel: 2 rows, both on existing-domain. No auto-create
		// path on this surface.
		w.Write([]string{"alice@example.com", "example.com", "", "1024", "100"})
		w.Write([]string{"bob@example.com", "example.com", "MyOwnP@ss123", "2048", "200"})
	} else {
		// WHM: 3 rows. Row 3 demonstrates the user→auto-create-domain
		// hook (newshop.com doesn't exist; gets created under alice
		// before the mailbox lands).
		w.Write([]string{"alice@example.com", "example.com", "", "1024", "100", ""})
		w.Write([]string{"bob@example.com", "example.com", "MyOwnP@ss123", "2048", "200", ""})
		w.Write([]string{"hello@newshop.com", "newshop.com", "", "1024", "100", "alice"})
	}
	w.Flush()
	return buf.Bytes()
}

// BulkMailboxUploadXLSXTemplate returns the same template shape as
// BulkMailboxUploadCSVTemplate, formatted as a single-sheet .xlsx
// with a frozen header row + a small "instructions" sheet so an
// operator opening it in Excel sees what every column means.
func BulkMailboxUploadXLSXTemplate(omitUser bool) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	const sheet = "Mailboxes"
	f.SetSheetName("Sheet1", sheet)

	headers := mailboxTemplateHeadersForSurface(omitUser)
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}
	if omitUser {
		// Cpanel: 2 rows, no `user` column. Owner is auto-set to the
		// authenticated caller server-side.
		f.SetCellValue(sheet, "A2", "alice@example.com")
		f.SetCellValue(sheet, "B2", "example.com")
		f.SetCellValue(sheet, "C2", "")
		f.SetCellValue(sheet, "D2", 1024)
		f.SetCellValue(sheet, "E2", 100)

		f.SetCellValue(sheet, "A3", "bob@example.com")
		f.SetCellValue(sheet, "B3", "example.com")
		f.SetCellValue(sheet, "C3", "MyOwnP@ss123")
		f.SetCellValue(sheet, "D3", 2048)
		f.SetCellValue(sheet, "E3", 200)
	} else {
		// WHM: 3 rows. Row 3 demonstrates the user→auto-create-domain
		// hook.
		f.SetCellValue(sheet, "A2", "alice@example.com")
		f.SetCellValue(sheet, "B2", "example.com")
		f.SetCellValue(sheet, "C2", "")
		f.SetCellValue(sheet, "D2", 1024)
		f.SetCellValue(sheet, "E2", 100)
		f.SetCellValue(sheet, "F2", "")

		f.SetCellValue(sheet, "A3", "bob@example.com")
		f.SetCellValue(sheet, "B3", "example.com")
		f.SetCellValue(sheet, "C3", "MyOwnP@ss123")
		f.SetCellValue(sheet, "D3", 2048)
		f.SetCellValue(sheet, "E3", 200)
		f.SetCellValue(sheet, "F3", "")

		f.SetCellValue(sheet, "A4", "hello@newshop.com")
		f.SetCellValue(sheet, "B4", "newshop.com")
		f.SetCellValue(sheet, "C4", "")
		f.SetCellValue(sheet, "D4", 1024)
		f.SetCellValue(sheet, "E4", 100)
		f.SetCellValue(sheet, "F4", "alice")
	}

	style, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#E8EEF7"}},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})
	endCell, _ := excelize.CoordinatesToCellName(len(headers), 1)
	f.SetCellStyle(sheet, "A1", endCell, style)
	f.SetPanes(sheet, &excelize.Panes{Freeze: true, Split: false, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})

	// Instructions sheet — one place an operator can read the rules
	// without having to remember the API doc.
	instr := "Instructions"
	f.NewSheet(instr)
	f.SetCellValue(instr, "A1", "Bulk Mailbox Upload Template")
	f.SetCellValue(instr, "A2", "")
	f.SetCellValue(instr, "A3", "Required column: email")
	f.SetCellValue(instr, "A4", "Optional columns: domain (auto-derived from email), password (BLANK = auto-generated, returned in the upload response), quota_mb (0 = unlimited), send_limit_per_hour (0 = unlimited)")
	f.SetCellValue(instr, "A5", "")
	f.SetCellValue(instr, "A6", "Tenant scope: a vendor uploading this CSV/XLSX can only create mailboxes on domains they own. Rows for unknown / out-of-tenant domains fail with an actionable error.")
	f.SetCellValue(instr, "A7", "WHM auto-create-domain (owner-only): on the WHM admin upload, when a row's domain doesn't exist yet, you may set the 'user' column to the desired owner — the bulk uploader will provision the domain (PHP 8.2 default) under that user before creating the mailbox. Vendors cannot use this column.")
	f.SetCellValue(instr, "A8", "Server transfer: this file format is identical across panel installs — export from one box, import to another.")

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, fmt.Errorf("write template xlsx: %w", err)
	}
	return buf.Bytes(), nil
}

func BulkMailboxUploadCSVTemplateName() string  { return "mailboxes-template.csv" }
func BulkMailboxUploadXLSXTemplateName() string { return "mailboxes-template.xlsx" }

// ─────────────────────────────────────────────────────────────────────
// Bulk Delete (OTP-gated)
// Bulk Export Password Reveal (OTP-gated, same flow)
// ─────────────────────────────────────────────────────────────────────
//
// We share one OTP collection (ColBulkMailboxOTP) for both flows
// using a `kind` discriminator: "delete" (destructive) and "export"
// (sensitive read of plaintext passwords). The mechanics — token,
// SHA-256-hashed code, 10-minute TTL, 5-attempt cap — are identical
// to the domain bulk-delete flow.

// BulkMailboxOTPMaxAttempts caps wrong-code retries before the row
// is hard-invalidated. Mirrors BulkDeleteOTPMaxAttempts.
const BulkMailboxOTPMaxAttempts = 5

// bulkMailboxOTPTTL is how long a token+code pair stays valid.
const bulkMailboxOTPTTL = 10 * time.Minute

// BulkMailboxOTPKind discriminator values.
const (
	BulkMailboxOTPKindDelete = "delete"
	BulkMailboxOTPKindExport = "export"
)

// BulkMailboxOTPRequest is the on-disk shape stored in
// ColBulkMailboxOTP. Email + Addresses denormalised onto the row so
// the OTP email body + the confirmation modal both render without a
// second round-trip.
type BulkMailboxOTPRequest struct {
	ID         primitive.ObjectID   `bson:"_id,omitempty" json:"-"`
	Token      string               `bson:"token" json:"-"`
	Kind       string               `bson:"kind" json:"kind"` // "delete" or "export"
	UserID     primitive.ObjectID   `bson:"user_id" json:"-"`
	Email      string               `bson:"email" json:"email"`
	MailboxIDs []primitive.ObjectID `bson:"mailbox_ids" json:"-"`
	Addresses  []string             `bson:"addresses" json:"addresses"`
	CodeHash   string               `bson:"code_hash" json:"-"`
	Attempts   int                  `bson:"attempts" json:"-"`
	Used       bool                 `bson:"used" json:"-"`
	CreatedAt  time.Time            `bson:"created_at" json:"-"`
	ExpiresAt  time.Time            `bson:"expires_at" json:"expires_at"`
}

// BulkMailboxOTPRequestResult is the /request-otp response. Same
// shape on both flows; the UI can branch on `kind` to render
// appropriate copy.
type BulkMailboxOTPRequestResult struct {
	Token         string    `json:"token"`
	Kind          string    `json:"kind"`
	Email         string    `json:"email"`
	MailboxCount  int       `json:"mailbox_count"`
	Addresses     []string  `json:"addresses"`
	ExpiresAt     time.Time `json:"expires_at"`
	MailerEnabled bool      `json:"mailer_enabled"`
}

// BulkMailboxDeleteRowResult is one row's outcome on the delete
// confirm. Mirrors BulkDeleteRowResult.
type BulkMailboxDeleteRowResult struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type BulkMailboxDeleteConfirmResult struct {
	TotalRows int                          `json:"total_rows"`
	Successes int                          `json:"successes"`
	Failures  int                          `json:"failures"`
	Items     []BulkMailboxDeleteRowResult `json:"items"`
}

// requestBulkMailboxOTP is the shared request-OTP path for both
// delete + export. Validates the id list, resolves email addresses,
// stamps a row in ColBulkMailboxOTP, and emails the code.
func (s *EmailService) requestBulkMailboxOTP(ctx context.Context, userID primitive.ObjectID, ids []string, kind string) (*BulkMailboxOTPRequestResult, error) {
	if kind != BulkMailboxOTPKindDelete && kind != BulkMailboxOTPKindExport {
		return nil, fmt.Errorf("invalid otp kind %q", kind)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no mailboxes selected")
	}
	if len(ids) > 500 {
		return nil, fmt.Errorf("too many mailboxes in one request (max 500)")
	}

	// Look up admin email for OTP delivery.
	var admin models.User
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": userID}).Decode(&admin); err != nil {
		return nil, fmt.Errorf("look up admin: %w", err)
	}
	if strings.TrimSpace(admin.Email) == "" {
		return nil, fmt.Errorf("admin account has no email on file — cannot send OTP")
	}

	objIDs := make([]primitive.ObjectID, 0, len(ids))
	for _, raw := range ids {
		if oid, err := primitive.ObjectIDFromHex(raw); err == nil {
			objIDs = append(objIDs, oid)
		}
	}
	if len(objIDs) == 0 {
		return nil, fmt.Errorf("no valid mailbox ids in request")
	}

	// Resolve to live mailbox rows so the email body shows real
	// addresses + the confirm step works against ids that are
	// guaranteed to exist (operator's UI may have stale rows).
	type mboxRow struct {
		ID     primitive.ObjectID `bson:"_id"`
		Email  string             `bson:"email"`
		Domain string             `bson:"domain"`
	}
	cur, err := s.db.Collection(database.ColMailboxes).Find(ctx, bson.M{"_id": bson.M{"$in": objIDs}})
	if err != nil {
		return nil, fmt.Errorf("look up mailboxes: %w", err)
	}
	defer cur.Close(ctx)

	resolvedIDs := []primitive.ObjectID{}
	resolvedAddrs := []string{}
	for cur.Next(ctx) {
		var m mboxRow
		if err := cur.Decode(&m); err != nil || m.Email == "" {
			continue
		}
		// Tenant gate: skip mailboxes whose domain isn't in caller
		// scope. Same shape as the upload's per-row check.
		if scope := GetCallerScope(ctx); scope != nil {
			if err := scope.AssertOwnsDomain(ctx, s.db, m.Domain); err != nil {
				continue
			}
		}
		resolvedIDs = append(resolvedIDs, m.ID)
		resolvedAddrs = append(resolvedAddrs, m.Email)
	}
	if len(resolvedIDs) == 0 {
		return nil, fmt.Errorf("none of the selected ids matched a mailbox row")
	}

	code, err := generateBulkDeleteCode()
	if err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}
	token, err := generateBulkDeleteToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	now := time.Now()
	doc := BulkMailboxOTPRequest{
		Token:      token,
		Kind:       kind,
		UserID:     userID,
		Email:      strings.ToLower(strings.TrimSpace(admin.Email)),
		MailboxIDs: resolvedIDs,
		Addresses:  resolvedAddrs,
		CodeHash:   bulkDeleteSHA256Hex(code),
		Attempts:   0,
		Used:       false,
		CreatedAt:  now,
		ExpiresAt:  now.Add(bulkMailboxOTPTTL),
	}
	if _, err := s.db.Collection(database.ColBulkMailboxOTP).InsertOne(ctx, doc); err != nil {
		return nil, fmt.Errorf("save otp request: %w", err)
	}

	mailerEnabled := s.mailer != nil && s.mailer.Enabled()
	if !mailerEnabled {
		log.Warn().Str("admin_email", admin.Email).Str("kind", kind).Msg("bulk-mailbox OTP: SMTP not configured — code only in stderr")
		fmt.Printf("[bulk-mailbox] OTP for %s (mailer disabled, kind=%s): %s — mailboxes=%d, expires in %dm\n",
			admin.Email, kind, code, len(resolvedAddrs), int(bulkMailboxOTPTTL/time.Minute))
	} else {
		subject, text, htmlBody := buildBulkMailboxOTPEmail(admin.Name, code, kind, resolvedAddrs, int(bulkMailboxOTPTTL/time.Minute))
		if err := s.mailer.Send(ctx, mailer.Message{
			To:      admin.Email,
			Subject: subject,
			Text:    text,
			HTML:    htmlBody,
		}); err != nil {
			log.Error().Err(err).Str("admin_email", admin.Email).Str("kind", kind).Msg("bulk-mailbox OTP: send failed")
			return nil, fmt.Errorf("send otp email: %w", err)
		}
	}

	return &BulkMailboxOTPRequestResult{
		Token:         token,
		Kind:          kind,
		Email:         admin.Email,
		MailboxCount:  len(resolvedAddrs),
		Addresses:     resolvedAddrs,
		ExpiresAt:     doc.ExpiresAt,
		MailerEnabled: mailerEnabled,
	}, nil
}

// RequestBulkMailboxDeleteOTP starts the OTP flow for a destructive
// multi-mailbox removal. Identical mechanics to the domain version;
// see requestBulkMailboxOTP.
func (s *EmailService) RequestBulkMailboxDeleteOTP(ctx context.Context, userID primitive.ObjectID, ids []string) (*BulkMailboxOTPRequestResult, error) {
	return s.requestBulkMailboxOTP(ctx, userID, ids, BulkMailboxOTPKindDelete)
}

// RequestBulkMailboxExportOTP starts the OTP flow gating a
// password-revealing CSV/XLSX export. Same shape as the delete
// request, different `kind`. The UI flips its "Confirm" copy based
// on this discriminator.
func (s *EmailService) RequestBulkMailboxExportOTP(ctx context.Context, userID primitive.ObjectID, ids []string) (*BulkMailboxOTPRequestResult, error) {
	return s.requestBulkMailboxOTP(ctx, userID, ids, BulkMailboxOTPKindExport)
}

// validateBulkMailboxOTP loads + validates an OTP doc, increments
// the attempt counter on miss, and returns the doc when the code
// matches. Shared by ConfirmBulkMailboxDelete and the export path.
//
// On success the caller is responsible for marking Used=true (so the
// caller can decide WHEN — before destructive work, or after a
// successful read — without this helper presuming).
func (s *EmailService) validateBulkMailboxOTP(ctx context.Context, userID primitive.ObjectID, token, code, expectedKind string) (*BulkMailboxOTPRequest, error) {
	col := s.db.Collection(database.ColBulkMailboxOTP)

	var doc BulkMailboxOTPRequest
	if err := col.FindOne(ctx, bson.M{"token": token}).Decode(&doc); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("invalid or expired confirmation token")
		}
		return nil, fmt.Errorf("look up otp: %w", err)
	}
	if doc.Used {
		return nil, fmt.Errorf("this confirmation has already been used")
	}
	if time.Now().After(doc.ExpiresAt) {
		return nil, fmt.Errorf("confirmation expired — request a new code")
	}
	if doc.UserID != userID {
		return nil, fmt.Errorf("this confirmation belongs to a different account")
	}
	if expectedKind != "" && doc.Kind != expectedKind {
		return nil, fmt.Errorf("this confirmation is for a different action (expected %s, got %s)", expectedKind, doc.Kind)
	}

	expected := bulkDeleteSHA256Hex(strings.TrimSpace(code))
	if expected != doc.CodeHash {
		newAttempts := doc.Attempts + 1
		update := bson.M{"$set": bson.M{"attempts": newAttempts}}
		if newAttempts >= BulkMailboxOTPMaxAttempts {
			update["$set"].(bson.M)["used"] = true
		}
		_, _ = col.UpdateByID(ctx, doc.ID, update)
		if newAttempts >= BulkMailboxOTPMaxAttempts {
			return nil, fmt.Errorf("too many wrong codes — request a new one")
		}
		return nil, fmt.Errorf("wrong code (%d attempts left)", BulkMailboxOTPMaxAttempts-newAttempts)
	}
	return &doc, nil
}

// ConfirmBulkMailboxDelete validates the OTP and runs DeleteMailbox
// per id. Returns a per-row result table the WHM modal renders.
func (s *EmailService) ConfirmBulkMailboxDelete(ctx context.Context, userID primitive.ObjectID, token, code string) (*BulkMailboxDeleteConfirmResult, error) {
	doc, err := s.validateBulkMailboxOTP(ctx, userID, token, code, BulkMailboxOTPKindDelete)
	if err != nil {
		return nil, err
	}

	// Mark used BEFORE destructive work so a concurrent retry can't
	// fire DeleteMailbox twice.
	col := s.db.Collection(database.ColBulkMailboxOTP)
	if _, err := col.UpdateByID(ctx, doc.ID, bson.M{"$set": bson.M{"used": true}}); err != nil {
		return nil, fmt.Errorf("mark otp used: %w", err)
	}

	res := &BulkMailboxDeleteConfirmResult{
		TotalRows: len(doc.MailboxIDs),
		Items:     make([]BulkMailboxDeleteRowResult, 0, len(doc.MailboxIDs)),
	}
	addrByID := map[string]string{}
	for i, id := range doc.MailboxIDs {
		if i < len(doc.Addresses) {
			addrByID[id.Hex()] = doc.Addresses[i]
		}
	}
	for _, id := range doc.MailboxIDs {
		hex := id.Hex()
		row := BulkMailboxDeleteRowResult{ID: hex, Email: addrByID[hex]}
		if err := s.DeleteMailbox(ctx, hex); err != nil {
			row.Success = false
			row.Error = err.Error()
			res.Failures++
		} else {
			row.Success = true
			res.Successes++
		}
		res.Items = append(res.Items, row)
	}
	log.Info().
		Str("admin_email", doc.Email).
		Int("requested", len(doc.MailboxIDs)).
		Int("succeeded", res.Successes).
		Int("failed", res.Failures).
		Msg("bulk-mailbox-delete: confirmed + executed")
	return res, nil
}

// ConfirmBulkMailboxExport validates the OTP for the password-reveal
// path and returns the resolved mailbox id list. The handler then
// pipes that into FetchMailboxesForExport(includePassword=true).
//
// Returning the id list (not the file bytes) keeps this method
// HTTP-shape-agnostic — the handler picks the format (CSV vs XLSX)
// from the query string.
func (s *EmailService) ConfirmBulkMailboxExport(ctx context.Context, userID primitive.ObjectID, token, code string) ([]string, error) {
	doc, err := s.validateBulkMailboxOTP(ctx, userID, token, code, BulkMailboxOTPKindExport)
	if err != nil {
		return nil, err
	}

	// Mark used so the same OTP can't be replayed for a second
	// download. Operator who needs another copy must re-request.
	col := s.db.Collection(database.ColBulkMailboxOTP)
	if _, err := col.UpdateByID(ctx, doc.ID, bson.M{"$set": bson.M{"used": true}}); err != nil {
		return nil, fmt.Errorf("mark otp used: %w", err)
	}

	out := make([]string, 0, len(doc.MailboxIDs))
	for _, id := range doc.MailboxIDs {
		out = append(out, id.Hex())
	}
	log.Info().
		Str("admin_email", doc.Email).
		Int("mailboxes", len(out)).
		Msg("bulk-mailbox-export: password reveal confirmed")
	return out, nil
}

// buildBulkMailboxOTPEmail composes the OTP email body. Branches on
// `kind` so the operator sees the right copy: "Bulk Delete" when
// they're about to remove mailboxes, "Bulk Export" when the panel
// is about to ship plaintext passwords down a TLS pipe to their
// browser.
func buildBulkMailboxOTPEmail(adminName, code, kind string, addresses []string, expiresMin int) (subject, text, htmlBody string) {
	if adminName == "" {
		adminName = "admin"
	}
	preview := addresses
	if len(preview) > 10 {
		preview = preview[:10]
	}
	action := "delete"
	verb := "delete"
	if kind == BulkMailboxOTPKindExport {
		action = "export with passwords"
		verb = "download credentials for"
	}

	subject = fmt.Sprintf("Mailbox Bulk %s confirmation code: %s", strings.Title(action), code)

	var sb strings.Builder
	sb.WriteString("Hi ")
	sb.WriteString(adminName)
	sb.WriteString(",\n\nYou requested to ")
	sb.WriteString(verb)
	sb.WriteString(fmt.Sprintf(" %d mailbox(es) on Betazen Server Panel.\n\n", len(addresses)))
	sb.WriteString("Confirmation code: ")
	sb.WriteString(code)
	sb.WriteString("\n\nThe code expires in ")
	sb.WriteString(fmt.Sprintf("%d minutes. ", expiresMin))
	if kind == BulkMailboxOTPKindExport {
		sb.WriteString("If you didn't initiate this, ignore this email — no passwords will be revealed without the code.\n\n")
	} else {
		sb.WriteString("If you didn't initiate this, ignore this email — no mailboxes will be deleted without the code.\n\n")
	}
	sb.WriteString("Mailboxes affected:\n")
	for _, a := range preview {
		sb.WriteString("  - ")
		sb.WriteString(a)
		sb.WriteString("\n")
	}
	if len(addresses) > len(preview) {
		sb.WriteString(fmt.Sprintf("  … and %d more\n", len(addresses)-len(preview)))
	}
	text = sb.String()

	var hb strings.Builder
	hb.WriteString(`<!DOCTYPE html><html><body style="font-family:system-ui,sans-serif;line-height:1.5;color:#1a1a1a;">`)
	hb.WriteString(`<p>Hi <b>` + escapeHTML(adminName) + `</b>,</p>`)
	hb.WriteString(fmt.Sprintf(`<p>You requested to <b>%s %d mailbox(es)</b> on Betazen Server Panel.</p>`, escapeHTML(verb), len(addresses)))
	hb.WriteString(`<p style="margin:16px 0;">Confirmation code:</p>`)
	hb.WriteString(`<p style="font-size:28px;font-family:monospace;letter-spacing:6px;background:#f4f4f4;padding:14px 20px;border-radius:8px;display:inline-block;border:1px solid #ddd;">` + escapeHTML(code) + `</p>`)
	if kind == BulkMailboxOTPKindExport {
		hb.WriteString(fmt.Sprintf(`<p>The code expires in <b>%d minutes</b>. If you didn't initiate this, ignore this email — <b>no passwords will be revealed</b> without the code.</p>`, expiresMin))
	} else {
		hb.WriteString(fmt.Sprintf(`<p>The code expires in <b>%d minutes</b>. If you didn't initiate this, ignore this email — <b>no mailboxes will be deleted</b> without the code.</p>`, expiresMin))
	}
	hb.WriteString(`<p style="margin-top:20px;"><b>Mailboxes affected:</b></p><ul>`)
	for _, a := range preview {
		hb.WriteString(`<li>` + escapeHTML(a) + `</li>`)
	}
	if len(addresses) > len(preview) {
		hb.WriteString(fmt.Sprintf(`<li>… and %d more</li>`, len(addresses)-len(preview)))
	}
	hb.WriteString(`</ul></body></html>`)
	htmlBody = hb.String()
	return
}

// bulkResolveDomain is the per-row domain gate the bulk uploader runs
// before it attempts CreateMailbox. Returns:
//
//	owner    — the username that owns the (existing or just-created)
//	           domain. Used by future placement logic; currently
//	           informational.
//	created  — true when the domain didn't exist and we auto-created
//	           it on this call. Tracked in the row result so the
//	           operator can distinguish "mailbox added to existing
//	           domain" from "mailbox + domain created".
//	err      — non-nil error if the row should be rejected. Carries
//	           an actionable message for the operator (e.g. "domain
//	           is not in your tenant", "domain X doesn't exist —
//	           include a 'user' column to auto-create on WHM").
//
// Decision matrix (callerKind = vendor_owner / non-owner-tenant):
//
//	     | domain exists  | domain missing
//	-----+----------------+-------------------------
//	 WHM | check ok       | auto-create when row.user
//	cpanel | AssertOwns   | reject (out of tenant)
//
// Auto-create defaults: PHP 8.2 (panel default), no SSL/registrar
// fields populated; the operator can fill those in later via the
// Domains page. Server IP is left empty so DomainService.Create's
// own resolveServerIP fallback fires.
func (s *EmailService) bulkResolveDomain(ctx context.Context, domain, rowUser string) (owner string, created bool, err error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return "", false, fmt.Errorf("domain is required")
	}

	// Lookup. The Domains collection's primary index is on `domain`,
	// so this is a single B-tree probe.
	var d struct {
		Domain string `bson:"domain"`
		User   string `bson:"user"`
	}
	findErr := s.db.Collection(database.ColDomains).
		FindOne(ctx, bson.M{"domain": domain}).
		Decode(&d)

	scope := GetCallerScope(ctx)
	tenantScoped := scope != nil && scope.Role != "" && scope.Role != "vendor_owner"

	if findErr == nil {
		// Domain exists. Tenant gate for non-owner callers.
		if tenantScoped {
			if aerr := scope.AssertOwnsDomain(ctx, s.db, domain); aerr != nil {
				return "", false, fmt.Errorf("domain %q is not in your tenant — bulk upload can only create mailboxes on domains you own", domain)
			}
		}
		return d.User, false, nil
	}
	if findErr != mongo.ErrNoDocuments {
		return "", false, fmt.Errorf("domain lookup failed: %w", findErr)
	}

	// Domain doesn't exist.
	if tenantScoped {
		return "", false, fmt.Errorf("domain %q is not in your tenant — add it on the Domains page first, then re-upload", domain)
	}

	// WHM/owner caller. Auto-create requires:
	//   - DomainCreator wired (main.go after both services exist)
	//   - row's `user` column populated (no safe default for owner)
	if s.domainCreator == nil {
		return "", false, fmt.Errorf("domain %q does not exist; create it on the Domains page first (auto-create not configured)", domain)
	}
	owner = strings.TrimSpace(rowUser)
	if owner == "" {
		return "", false, fmt.Errorf("domain %q does not exist — add a 'user' column to the CSV/XLSX naming the owner so the bulk uploader can auto-create it, or create it on the Domains page first", domain)
	}

	// Attempt the auto-create. Defaults: PHP 8.2 (current default
	// across newly-created domains), all other fields blank so
	// DomainService.Create's resolveServerIP / nameservers fallback
	// kicks in. Failure surfaces verbatim — the row's error is the
	// best diagnostic we can offer.
	if _, cerr := s.domainCreator.Create(ctx, &models.CreateDomainRequest{
		Domain:     domain,
		User:       owner,
		PHPVersion: "8.2",
	}); cerr != nil {
		return "", false, fmt.Errorf("auto-create domain %q (user=%s) failed: %v", domain, owner, cerr)
	}
	return owner, true, nil
}

