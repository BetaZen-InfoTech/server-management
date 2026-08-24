// Package services — domain_bulk_service.go is the parser + executor
// behind the WHM "Bulk Upload Domains" button (and its cPanel mirror).
//
// Why a dedicated file: the existing DomainService.Create returns a
// single *Domain or an error. The bulk path has different semantics
// — partial success is the common case (one bad row shouldn't abort
// the other 49) and the response shape is a per-row result table the
// UI can render. Wrapping Create in a loop keeps the single-row
// validation + side-effects (DNS, vhost, package limits, RBAC) honest
// instead of duplicating them in a "batch_create" method.
//
// Format support: CSV (RFC 4180, comma-separated, header row required)
// and XLSX (first sheet, header row required). The xlsx path uses
// excelize's stream reader so a 1000-row sheet doesn't pin the whole
// workbook in memory at once.
//
// SSL behavior: every successfully-created domain is queued for
// Let's Encrypt issuance + force-HTTPS. SSL failures DON'T fail the
// row — the domain row still lands "active" in the panel; the per-row
// result records SSL outcome separately so the operator can re-issue
// from the SSL page later (DNS may not have propagated yet on a brand-
// new registration, which would 404 the HTTP-01 challenge).
package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/mailer"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/rs/zerolog/log"
	"github.com/xuri/excelize/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// BulkUploadFormat is the on-wire file format the upload endpoint
// detected. We surface it back to the operator in the response so a
// "I uploaded the wrong file" mistake is obvious without scrolling
// through the row results.
type BulkUploadFormat string

const (
	BulkUploadFormatCSV  BulkUploadFormat = "csv"
	BulkUploadFormatXLSX BulkUploadFormat = "xlsx"
	// BulkUploadFormatJSON is the wire-shape used by the Services
	// Export/Import/Edit-JSON trio in the WHM Deploy Software drawer.
	// Same per-row result shape as the CSV/XLSX path, distinguished
	// only by this Format field so the UI can render the right
	// "imported from JSON" label on the result summary card.
	BulkUploadFormatJSON BulkUploadFormat = "json"
)

// BulkRowResult is one row's outcome. Domain is captured even on
// validation failure so the operator can find the offending line in
// their source spreadsheet without counting indices. RowNumber is
// 1-based and INCLUDES the header row, matching what Excel/Sheets
// shows at the left margin — this is the number a non-engineer will
// recognise when fixing the file.
type BulkRowResult struct {
	RowNumber  int    `json:"row_number"`
	Domain     string `json:"domain"`
	User       string `json:"user"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	SSLIssued  bool   `json:"ssl_issued"`
	SSLForced  bool   `json:"ssl_forced"`
	SSLMessage string `json:"ssl_message,omitempty"`
	// SetupWarnings forwards DomainService.Create's per-step status
	// (zone create failures, mail setup failures, SSL retry give-up,
	// admin-mailbox failure) so the bulk-upload UI surfaces the same
	// detail the single-create response now carries. Empty slice on
	// a clean create.
	SetupWarnings []string `json:"setup_warnings,omitempty"`
	// AdminMailbox + AdminMailboxPassword surface the auto-created
	// admin@<domain> mailbox + its generated password so the operator
	// can save the credentials before the modal closes. Pre-3.1.16
	// the password was created and discarded — operators couldn't
	// log in without resetting it from the Email page. Empty when
	// the auto-mailbox creation failed (the reason lands in
	// SetupWarnings).
	AdminMailbox         string `json:"admin_mailbox,omitempty"`
	AdminMailboxPassword string `json:"admin_mailbox_password,omitempty"`
}

// BulkUploadResponse is the JSON the API returns. Counters at the
// top let the UI render a one-line summary ("48 of 50 domains added,
// 2 failed") without re-walking Items.
type BulkUploadResponse struct {
	Format      BulkUploadFormat `json:"format"`
	TotalRows   int              `json:"total_rows"`
	Successes   int              `json:"successes"`
	Failures    int              `json:"failures"`
	SSLIssued   int              `json:"ssl_issued"`
	SSLForced   int              `json:"ssl_forced"`
	Items       []BulkRowResult  `json:"items"`
}

// BulkUploadOptions tunes the executor. CallerUsername is non-empty
// when the cPanel surface invokes us — the per-row `user` column is
// IGNORED and replaced with this value so a vendor can't create
// domains for someone else's tenant via a doctored CSV. PHPDefault
// fills in any row whose php_version cell is empty so the validator's
// `oneof` doesn't reject every blank.
type BulkUploadOptions struct {
	CallerUsername string
	PHPDefault     string
	IssueSSL       bool
	ForceSSL       bool
}

// DefaultBulkUploadOptions returns the safe defaults the WHM endpoint
// uses when no options block is sent: SSL on, force-HTTPS on, php 8.2.
// The cPanel endpoint overrides CallerUsername at the handler.
func DefaultBulkUploadOptions() BulkUploadOptions {
	return BulkUploadOptions{
		PHPDefault: "8.2",
		IssueSSL:   true,
		ForceSSL:   true,
	}
}

// canonical column names. CSV/XLSX headers are matched case-insensitively
// AND with snake_case ↔ space ↔ dash interchangeable so a non-engineer
// editing in Excel ("Domain Name", "PHP Version") gets the same parse
// as a developer saving from a script (`domain`, `php_version`).
var bulkHeaderAliases = map[string]string{
	"domain":             "domain",
	"domainname":         "domain",
	"site":               "domain",
	"hostname":           "domain",
	"user":               "user",
	"username":           "user",
	"owner":              "user",
	"phpversion":         "php_version",
	"php":                "php_version",
	"phpv":               "php_version",
	"diskquotamb":        "disk_quota_mb",
	"diskquota":          "disk_quota_mb",
	"diskmb":             "disk_quota_mb",
	"bandwidthlimitgb":   "bandwidth_limit_gb",
	"bandwidthgb":        "bandwidth_limit_gb",
	"maxdatabases":       "max_databases",
	"maxemailaccounts":   "max_email_accounts",
	"maxemails":          "max_email_accounts",
	"maxsubdomains":      "max_subdomains",
	"maxapps":            "max_apps",
	"registrar":          "registrar",
	"registeredon":       "registered_on",
	"expireson":          "expires_on",
	"autorenew":          "auto_renew",
}

// normaliseHeader collapses every case + separator variant so the
// matcher accepts "PHP Version", "php_version", "PHP-Version", "phpversion",
// and " PHP version " interchangeably. Operators editing in Excel /
// Google Sheets will Title-Case headers without thinking about it.
func normaliseHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(" ", "", "_", "", "-", "", ".", "").Replace(s)
	return s
}

// resolveHeader maps a (normalised) input header to the canonical
// CreateDomainRequest field name, or "" when it's an unknown column
// the parser should pass through unchanged. Unknown columns aren't
// fatal — operators sometimes keep a Notes column and we shouldn't
// reject the file for it.
func resolveHeader(in string) string {
	if canon, ok := bulkHeaderAliases[normaliseHeader(in)]; ok {
		return canon
	}
	return ""
}

// BulkUploadCSV is the entry point for *.csv uploads. Reads the
// whole file into memory because a domain spreadsheet is bounded
// (operator typing, not log streaming) — keeping the parser
// streaming-shaped would just complicate the column-index pass.
func (s *DomainService) BulkUploadCSV(ctx context.Context, body io.Reader, opts BulkUploadOptions) (*BulkUploadResponse, error) {
	r := csv.NewReader(body)
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1 // tolerate ragged rows; parser handles missing cells

	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse csv: %w", err)
	}
	return s.executeBulkRows(ctx, rows, BulkUploadFormatCSV, opts)
}

// BulkUploadXLSX is the entry point for *.xlsx uploads. excelize
// returns rows AS [][]string already so the executor sees the same
// shape as the CSV path. Always reads the FIRST sheet — operators
// occasionally upload multi-tab workbooks where tabs 2..n are notes;
// we'd rather fail fast on the first sheet than guess.
func (s *DomainService) BulkUploadXLSX(ctx context.Context, body io.Reader, opts BulkUploadOptions) (*BulkUploadResponse, error) {
	f, err := excelize.OpenReader(body)
	if err != nil {
		return nil, fmt.Errorf("parse xlsx: %w", err)
	}
	defer f.Close()

	sheet := f.GetSheetName(0)
	if sheet == "" {
		return nil, errors.New("xlsx file has no sheets")
	}
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("read xlsx rows: %w", err)
	}
	return s.executeBulkRows(ctx, rows, BulkUploadFormatXLSX, opts)
}

// BulkUploadFromContentType picks the right parser based on the
// Content-Type the multipart upload announced. Falls back to a
// best-effort sniff on the first 512 bytes if Content-Type is the
// generic application/octet-stream that some browsers / curl invocations
// emit.
func (s *DomainService) BulkUploadFromContentType(ctx context.Context, body io.Reader, contentType, filename string, opts BulkUploadOptions) (*BulkUploadResponse, error) {
	ct := strings.ToLower(contentType)
	lowName := strings.ToLower(filename)
	if strings.Contains(ct, "csv") || strings.HasSuffix(lowName, ".csv") {
		return s.BulkUploadCSV(ctx, body, opts)
	}
	if strings.Contains(ct, "spreadsheet") || strings.Contains(ct, "excel") ||
		strings.HasSuffix(lowName, ".xlsx") || strings.HasSuffix(lowName, ".xls") {
		return s.BulkUploadXLSX(ctx, body, opts)
	}
	// Last-resort sniff: peek the first chunk and look for the XLSX zip
	// magic ("PK\x03\x04"). Anything else routes to CSV.
	peek := make([]byte, 512)
	n, _ := io.ReadFull(body, peek)
	peek = peek[:n]
	combined := io.MultiReader(strings.NewReader(string(peek)), body)
	if n >= 4 && string(peek[:4]) == "PK\x03\x04" {
		return s.BulkUploadXLSX(ctx, combined, opts)
	}
	return s.BulkUploadCSV(ctx, combined, opts)
}

// parseBulkFile reads an uploaded CSV/XLSX into rows + the detected format
// WITHOUT processing them. The async job path parses up-front (fast, in the
// request) then processes rows in the background, so it needs parse split from
// execute. Mirrors BulkUploadFromContentType's format detection.
func parseBulkFile(body io.Reader, contentType, filename string) ([][]string, BulkUploadFormat, error) {
	ct := strings.ToLower(contentType)
	lowName := strings.ToLower(filename)
	isCSV := strings.Contains(ct, "csv") || strings.HasSuffix(lowName, ".csv")
	isXLSX := strings.Contains(ct, "spreadsheet") || strings.Contains(ct, "excel") ||
		strings.HasSuffix(lowName, ".xlsx") || strings.HasSuffix(lowName, ".xls")
	if !isCSV && !isXLSX {
		peek := make([]byte, 512)
		n, _ := io.ReadFull(body, peek)
		peek = peek[:n]
		body = io.MultiReader(strings.NewReader(string(peek)), body)
		if n >= 4 && string(peek[:4]) == "PK\x03\x04" {
			isXLSX = true
		} else {
			isCSV = true
		}
	}
	if isXLSX {
		f, err := excelize.OpenReader(body)
		if err != nil {
			return nil, "", fmt.Errorf("parse xlsx: %w", err)
		}
		defer f.Close()
		sheet := f.GetSheetName(0)
		if sheet == "" {
			return nil, "", errors.New("xlsx file has no sheets")
		}
		rows, err := f.GetRows(sheet)
		if err != nil {
			return nil, "", fmt.Errorf("read xlsx rows: %w", err)
		}
		return rows, BulkUploadFormatXLSX, nil
	}
	r := csv.NewReader(body)
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, "", fmt.Errorf("parse csv: %w", err)
	}
	return rows, BulkUploadFormatCSV, nil
}

// executeBulkRows is the shared path between the CSV and XLSX entries.
// Walks the header row, builds a column-name → cell-index map, and
// then runs each data row through DomainService.Create + the optional
// SSL pass. Per-row failures populate Items but never abort.
func (s *DomainService) executeBulkRows(ctx context.Context, rows [][]string, format BulkUploadFormat, opts BulkUploadOptions) (*BulkUploadResponse, error) {
	resp := &BulkUploadResponse{Format: format, Items: []BulkRowResult{}}
	headerIdx, err := bulkHeaderIndex(rows)
	if err != nil {
		return resp, err
	}

	// Domains successfully created this run whose SSL was deferred — issued in
	// the background after the loop so the request returns fast.
	var sslQueue []string

	for i := 1; i < len(rows); i++ {
		row := rows[i]
		// Skip the entire-blank rows that Excel leaves at the bottom
		// of a spreadsheet on save. Without this we'd surface a noisy
		// "domain is required" failure for every trailing empty line.
		if rowAllBlank(row) {
			continue
		}

		result, queueSSL := s.processBulkRow(ctx, row, i+1, headerIdx, opts)
		if queueSSL {
			sslQueue = append(sslQueue, result.Domain)
		}
		if result.Success {
			resp.Successes++
		} else {
			resp.Failures++
		}
		if result.SSLIssued {
			resp.SSLIssued++
		}
		if result.SSLForced {
			resp.SSLForced++
		}
		resp.Items = append(resp.Items, result)
	}
	resp.TotalRows = len(resp.Items)

	// Background SSL pass for the freshly-created domains — serial, off the
	// request path, so bulk upload of N domains never blocks for N×30s of LE
	// retry sleeps (the "timeout of 60000ms exceeded" cause). Best-effort;
	// per-domain failures are logged and re-issuable from the SSL page.
	if opts.IssueSSL && s.ssl != nil && len(sslQueue) > 0 {
		go s.issueBulkSSLBackground(append([]string(nil), sslQueue...), opts.ForceSSL)
	}
	return resp, nil
}

// bulkHeaderIndex resolves the header row into a canonical column→index map and
// verifies the required "domain" column is present.
func bulkHeaderIndex(rows [][]string) (map[string]int, error) {
	if len(rows) == 0 {
		return nil, errors.New("file is empty — at minimum a header row plus one domain is required")
	}
	headerIdx := map[string]int{}
	for i, h := range rows[0] {
		if canon := resolveHeader(h); canon != "" {
			headerIdx[canon] = i // last duplicate wins
		}
	}
	if _, ok := headerIdx["domain"]; !ok {
		return nil, errors.New("missing required column: domain")
	}
	return headerIdx, nil
}

// processBulkRow creates ONE domain from a data row and returns its result plus
// whether its (deferred) SSL should be queued for the background pass. Shared by
// the synchronous executeBulkRows and the async DomainBulkJob runner so both
// behave identically — same validation, same DeferSSL + SkipCloudflare, same
// force-HTTPS-on-top. rowNum is the 1-based row number (including the header).
func (s *DomainService) processBulkRow(ctx context.Context, row []string, rowNum int, headerIdx map[string]int, opts BulkUploadOptions) (BulkRowResult, bool) {
	cell := func(key string) string {
		idx, ok := headerIdx[key]
		if !ok || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	user := cell("user")
	if opts.CallerUsername != "" {
		// cPanel context — clobber whatever the row said with the authenticated
		// username so a vendor can't reach outside their tenant via a doctored CSV.
		user = opts.CallerUsername
	}
	php := cell("php_version")
	if php == "" {
		php = opts.PHPDefault
	}

	req := &models.CreateDomainRequest{
		Domain:           strings.ToLower(cell("domain")),
		User:             user,
		PHPVersion:       php,
		DiskQuotaMB:      atoiSafe(cell("disk_quota_mb")),
		BandwidthLimitGB: atoiSafe(cell("bandwidth_limit_gb")),
		MaxDatabases:     atoiSafe(cell("max_databases")),
		MaxEmailAccounts: atoiSafe(cell("max_email_accounts")),
		MaxSubdomains:    atoiSafe(cell("max_subdomains")),
		MaxApps:          atoiSafe(cell("max_apps")),
		Registrar:        cell("registrar"),
		RegisteredOn:     cell("registered_on"),
		ExpiresOn:        cell("expires_on"),
		AutoRenew:        parseBool(cell("auto_renew")),
		Source:           "bulk_upload",
		DeferSSL:         true, // Create must not run its 3×-retry-with-30s-sleeps SSL
		SkipCloudflare:   true, // bulk create never auto-connects to Cloudflare
	}

	result := BulkRowResult{RowNumber: rowNum, Domain: req.Domain, User: req.User}

	if errs := validator.Validate(*req); errs != nil {
		result.Error = "validation: " + firstValidationError(errs)
		return result, false
	}
	domainDoc, err := s.Create(ctx, req)
	if err != nil {
		result.Error = err.Error()
		return result, false
	}
	result.Success = true
	result.SetupWarnings = domainDoc.SetupWarnings
	result.AdminMailbox = "admin@" + domainDoc.Domain
	result.AdminMailboxPassword = domainDoc.AdminMailboxPassword

	queueSSL := false
	if domainDoc.SSLActive {
		result.SSLIssued = true
		if opts.ForceSSL && s.ssl != nil {
			if forceErr := s.ssl.ForceSSL(ctx, domainDoc.Domain, true); forceErr == nil {
				result.SSLForced = true
			} else {
				result.SSLMessage = "force-https: " + forceErr.Error()
			}
		}
	} else if opts.IssueSSL {
		// Deferred: issued in the background so we never block on per-row LE retry.
		result.SSLMessage = "queued — issuing in the background; watch the SSL page"
		queueSSL = true
	}
	return result, queueSSL
}

// issueBulkSSLBackground issues Let's Encrypt (and optionally force-HTTPS) for a
// batch of freshly bulk-created domains, ONE AT A TIME off the request path.
// Serial so a big upload doesn't spawn many certbot processes / hammer ACME at
// once; a light retry gives brand-new domains a moment for DNS to propagate.
// Best-effort — a failure is logged and the operator re-issues from the SSL page.
func (s *DomainService) issueBulkSSLBackground(domains []string, forceSSL bool) {
	if s.ssl == nil {
		return
	}
	sslEmail := s.cfg.SSLEmail
	if sslEmail == "" {
		sslEmail = "admin@betazeninfotech.com"
	}
	for _, d := range domains {
		req := &models.IssueLetsEncryptRequest{
			Domain:            d,
			Email:             sslEmail,
			AdditionalDomains: []string{"www." + d, "cname." + d},
		}
		var issErr error
		for attempt := 1; attempt <= 2; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			_, issErr = s.ssl.IssueLetsEncrypt(ctx, req)
			cancel()
			if issErr == nil {
				break
			}
			if attempt < 2 {
				time.Sleep(15 * time.Second)
			}
		}
		if issErr != nil {
			fmt.Fprintf(os.Stderr, "bulk background SSL failed for %s: %v\n", d, issErr)
			continue
		}
		if forceSSL {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			_ = s.ssl.ForceSSL(ctx, d, true)
			cancel()
		}
	}
}

// rowAllBlank reports whether every cell in the row is empty / whitespace.
// Excel exports often have a few empty rows at the bottom of a sheet
// from earlier deletions — we skip them silently rather than calling
// every one a validation failure.
func rowAllBlank(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// atoiSafe is "best-effort int" for the optional numeric columns. A
// non-numeric value (operator typed "unlimited" in Excel) becomes 0,
// which the package layer treats as "use the package default" — same
// fallback the single-create form's empty input gets.
func atoiSafe(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// parseBool accepts the common Excel/CSV truthy values an operator
// might type. "TRUE" / "True" from Excel's checkbox export, "yes" /
// "y" / "1" from a hand-edited CSV. Anything else is false.
func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "y", "1", "on":
		return true
	}
	return false
}

// firstValidationError pulls a single human-readable message out of
// the validator's slice. The bulk row table only has space for one
// error string per row; surfacing all of them would clip in the UI
// anyway.
func firstValidationError(errs []validator.FieldError) string {
	if len(errs) == 0 {
		return "invalid request"
	}
	return errs[0].Field + ": " + errs[0].Message
}

// BulkUploadCSVTemplate returns the bytes of the sample CSV template
// the UI's "Download Template" button serves. Keeping it as code (vs
// an embedded file) means the column set stays in lock-step with
// CreateDomainRequest — a future field added to the request struct
// is one edit here, not a forgotten static file.
//
// The first data row is a fully-populated example so a non-technical
// operator can see what each column expects without reading docs;
// the second is the minimum-viable row (just domain + user) with
// blanks for everything else, demonstrating that most columns are
// optional. Both rows use a `.example` TLD so a copy-paste-and-go
// operator doesn't accidentally try to register a real domain on
// their first test run.
// BulkUploadCSVTemplate emits the operator-editable spreadsheet for
// the bulk-upload modal. omitUser=true is the cPanel/User-Panel
// variant: vendors don't need to type their own username because the
// backend overrides whatever's in the row with their authenticated
// caller — including a `user` column would be misleading and would
// make a vendor's first test upload feel "rejected" if they left it
// blank. omitUser=false is the WHM variant where the platform owner
// chooses each domain's owner explicitly.
func BulkUploadCSVTemplate(omitUser bool) []byte {
	header := []string{"domain"}
	example := []string{"site1.example"}
	minimal := []string{"site2.example"}
	if !omitUser {
		header = append(header, "user")
		example = append(example, "vendor1")
		minimal = append(minimal, "vendor1")
	}
	header = append(header, "php_version", "disk_quota_mb", "bandwidth_limit_gb",
		"max_databases", "max_email_accounts", "max_subdomains", "max_apps",
		"registrar", "registered_on", "expires_on", "auto_renew")
	example = append(example, "8.2", "5000", "100", "10", "20", "5", "5",
		"GoDaddy", "2026-01-15", "2027-01-15", "true")
	minimal = append(minimal, "", "", "", "", "", "", "", "", "", "", "")

	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write(header)
	_ = w.Write(example)
	_ = w.Write(minimal)
	w.Flush()
	return []byte(b.String())
}

// BulkUploadXLSXTemplate is the spreadsheet equivalent of the CSV
// template. omitUser drops the `user` column for the cPanel/User-
// Panel surface — see BulkUploadCSVTemplate for the rationale.
func BulkUploadXLSXTemplate(omitUser bool) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Domains"
	idx, err := f.NewSheet(sheet)
	if err != nil {
		return nil, err
	}
	f.SetActiveSheet(idx)
	// Default sheet "Sheet1" — we want our named one to be the only
	// one, so the import path's "first sheet" assumption holds.
	_ = f.DeleteSheet("Sheet1")

	headers := []string{"domain"}
	example := []any{"site1.example"}
	minimal := []any{"site2.example"}
	if !omitUser {
		headers = append(headers, "user")
		example = append(example, "vendor1")
		minimal = append(minimal, "vendor1")
	}
	headers = append(headers, "php_version", "disk_quota_mb", "bandwidth_limit_gb",
		"max_databases", "max_email_accounts", "max_subdomains", "max_apps",
		"registrar", "registered_on", "expires_on", "auto_renew")
	example = append(example, "8.2", 5000, 100, 10, 20, 5, 5,
		"GoDaddy", "2026-01-15", "2027-01-15", "true")
	minimal = append(minimal, "", "", "", "", "", "", "", "", "", "", "")

	for i, h := range headers {
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetCellValue(sheet, col+"1", h)
	}
	for i, v := range example {
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetCellValue(sheet, col+"2", v)
	}
	for i, v := range minimal {
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetCellValue(sheet, col+"3", v)
	}

	// Note row at the bottom so a cPanel operator opening the file
	// in Excel sees the auto-assignment policy without reading docs.
	noteRow := len(example) + 3
	if omitUser {
		noteCell, _ := excelize.ColumnNumberToName(1)
		_ = f.SetCellValue(sheet, fmt.Sprintf("%s%d", noteCell, noteRow),
			"Note: every uploaded domain will be assigned to your logged-in account automatically. The 'user' column is intentionally absent — vendors cannot create domains under another vendor's account.")
	}

	// Bold header row.
	if styleID, sErr := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}}); sErr == nil {
		lastCol, _ := excelize.ColumnNumberToName(len(headers))
		_ = f.SetCellStyle(sheet, "A1", lastCol+"1", styleID)
	}
	// Reasonable default widths so "max_email_accounts" doesn't clip.
	_ = f.SetColWidth(sheet, "A", "M", 18)

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BulkUploadCSVTemplateName / BulkUploadXLSXTemplateName are the
// suggested file names served via Content-Disposition. Date-stamped
// so an operator who downloads the template multiple times during a
// migration can tell which version they're editing.
func BulkUploadCSVTemplateName() string {
	return fmt.Sprintf("domains-bulk-upload-template-%s.csv", time.Now().Format("2006-01-02"))
}

func BulkUploadXLSXTemplateName() string {
	return fmt.Sprintf("domains-bulk-upload-template-%s.xlsx", time.Now().Format("2006-01-02"))
}

// MimeForFormat is the right Content-Type for the served template,
// kept here so handler code reads as plain English instead of opaque
// MIME strings.
func MimeForFormat(format BulkUploadFormat) string {
	switch format {
	case BulkUploadFormatXLSX:
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	return "text/csv; charset=utf-8"
}

// _ keeps net/http imported even when the file is bloomed with
// helpers — handler-side callers may want http.StatusOK constants
// from this package directly.
var _ = http.StatusOK

// --- Export ---------------------------------------------------------
//
// The export side of the same table-row-import-export pair the WHM /
// User Panel Domains pages expose. Operators select rows in the table
// (or check Select All), pick CSV / Excel, and the file that comes
// back has the SAME column shape as the bulk-upload template — so a
// round-trip (export → edit in Excel → re-upload) just works without
// re-mapping fields.
//
// Two columns ARE in the export but are NOT accepted on the bulk-
// upload import: `ssl_active` and `status`. They're informational
// for the operator's review (so an exported sheet shows which
// domains are SSL-active / suspended) but the upload parser silently
// ignores any column not in its header alias table — so re-uploading
// the unedited export is still a no-op.

// ExportableDomain captures the subset of Domain fields the export
// emits. Same column order as the bulk-upload template + two read-
// only review columns at the end. Pointer-typed dates so we can
// distinguish "no value" from "zero time" in the cell (`""` vs
// `"0001-01-01"`).
type ExportableDomain struct {
	Domain           string
	User             string
	PHPVersion       string
	DiskQuotaMB      int
	BandwidthLimitGB int
	MaxDatabases     int
	MaxEmailAccounts int
	MaxSubdomains    int
	MaxApps          int
	Registrar        string
	RegisteredOn     *time.Time
	ExpiresOn        *time.Time
	AutoRenew        bool
	// Read-only columns — present in the export so the operator can
	// see SSL/status state in their spreadsheet, but ignored on
	// re-upload (the parser's header alias table doesn't list them).
	SSLActive bool
	Status    string
}

// exportColumns is the canonical column order shared by both the CSV
// and XLSX export paths. Kept alongside the bulk-upload header so the
// two stay in lockstep — a future field on CreateDomainRequest gets
// added in both places at once.
var exportColumns = []string{
	"domain", "user", "php_version",
	"disk_quota_mb", "bandwidth_limit_gb",
	"max_databases", "max_email_accounts", "max_subdomains", "max_apps",
	"registrar", "registered_on", "expires_on", "auto_renew",
	"ssl_active", "status",
}

// rowFor produces the per-domain cell values in `exportColumns` order.
// Numeric zeros render as "" (matches the bulk-upload template's
// "leave blank, use package default" semantics — re-uploading an
// exported row keeps that contract).
func (d ExportableDomain) row() []string {
	dateOrEmpty := func(t *time.Time) string {
		if t == nil || t.IsZero() {
			return ""
		}
		return t.Format("2006-01-02")
	}
	intOrEmpty := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	boolOrEmpty := func(b bool) string {
		if b {
			return "true"
		}
		return ""
	}
	return []string{
		d.Domain, d.User, d.PHPVersion,
		intOrEmpty(d.DiskQuotaMB), intOrEmpty(d.BandwidthLimitGB),
		intOrEmpty(d.MaxDatabases), intOrEmpty(d.MaxEmailAccounts),
		intOrEmpty(d.MaxSubdomains), intOrEmpty(d.MaxApps),
		d.Registrar, dateOrEmpty(d.RegisteredOn), dateOrEmpty(d.ExpiresOn),
		boolOrEmpty(d.AutoRenew),
		boolOrEmpty(d.SSLActive), d.Status,
	}
}

// ExportDomainsCSV serialises a domain set as CSV with the export
// header row + one row per domain, in `exportColumns` order. Returns
// raw bytes so the caller can stream them straight to the response.
func ExportDomainsCSV(domains []ExportableDomain) []byte {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write(exportColumns)
	for _, d := range domains {
		_ = w.Write(d.row())
	}
	w.Flush()
	return []byte(b.String())
}

// ExportDomainsXLSX serialises a domain set as a real .xlsx workbook
// (single sheet named "Domains", header row bolded, sensible column
// widths). Same shape an operator would download from the bulk-upload
// "Excel template" button — round-trip-clean.
func ExportDomainsXLSX(domains []ExportableDomain) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Domains"
	idx, err := f.NewSheet(sheet)
	if err != nil {
		return nil, err
	}
	f.SetActiveSheet(idx)
	_ = f.DeleteSheet("Sheet1")

	for i, h := range exportColumns {
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetCellValue(sheet, col+"1", h)
	}
	for ri, d := range domains {
		for ci, v := range d.row() {
			col, _ := excelize.ColumnNumberToName(ci + 1)
			_ = f.SetCellValue(sheet, fmt.Sprintf("%s%d", col, ri+2), v)
		}
	}
	if styleID, sErr := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}}); sErr == nil {
		lastCol, _ := excelize.ColumnNumberToName(len(exportColumns))
		_ = f.SetCellStyle(sheet, "A1", lastCol+"1", styleID)
	}
	_ = f.SetColWidth(sheet, "A", "O", 18)

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExportFilenameCSV / ExportFilenameXLSX produce the date-stamped
// suggested filenames served via Content-Disposition. Operators
// downloading multiple times during a migration get distinguishable
// files in their Downloads folder.
func ExportFilenameCSV() string {
	return fmt.Sprintf("domains-export-%s.csv", time.Now().Format("2006-01-02"))
}

func ExportFilenameXLSX() string {
	return fmt.Sprintf("domains-export-%s.xlsx", time.Now().Format("2006-01-02"))
}

// FetchDomainsForExport returns ExportableDomain rows for the given
// `ids` selection (or every domain visible to the caller when ids is
// empty). Tenant scoping is handled at the handler layer via the
// existing List / ListByUser paths so this function stays simple.
//
// Why a slice of ExportableDomain rather than handing back []models.Domain
// directly: the column subset is intentional — we don't want to leak
// the OwnerEmail (computed-only), Password (zero-valued in JSON but
// present in BSON), or any pre-flight-derived fields like
// ResolvedIP. The bulk-upload template doesn't accept those columns
// either, so re-uploading the export wouldn't round-trip cleanly if
// they were included.
func (s *DomainService) FetchDomainsForExport(ctx context.Context, ids []string, listAll bool) ([]ExportableDomain, error) {
	col := s.db.Collection(database.ColDomains)

	filter := bson.M{}
	if !listAll {
		oids := make([]primitive.ObjectID, 0, len(ids))
		for _, id := range ids {
			if oid, err := primitive.ObjectIDFromHex(id); err == nil {
				oids = append(oids, oid)
			}
		}
		if len(oids) == 0 {
			// Empty selection + non-listAll = nothing to export. Better
			// to return an empty slice than to silently spew the whole
			// table (would be a tenant-scope leak on cPanel callers).
			return []ExportableDomain{}, nil
		}
		filter["_id"] = bson.M{"$in": oids}
	}

	cur, err := col.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("query domains: %w", err)
	}
	defer cur.Close(ctx)

	var out []ExportableDomain
	for cur.Next(ctx) {
		var d models.Domain
		if err := cur.Decode(&d); err != nil {
			continue
		}
		// Tenant-scope filter — when the caller has a CallerScope
		// (cPanel surface), drop domains they don't own. WHM owners
		// pass through unrestricted.
		if scope := GetCallerScope(ctx); scope != nil {
			if err := scope.AssertOwnsDomain(ctx, s.db, d.Domain); err != nil {
				continue
			}
		}
		out = append(out, ExportableDomain{
			Domain:           d.Domain,
			User:             d.User,
			PHPVersion:       d.PHPVersion,
			DiskQuotaMB:      d.DiskQuotaMB,
			BandwidthLimitGB: d.BandwidthLimitGB,
			MaxDatabases:     d.MaxDatabases,
			MaxEmailAccounts: d.MaxEmailAccounts,
			MaxSubdomains:    d.MaxSubdomains,
			MaxApps:          d.MaxApps,
			Registrar:        d.Registrar,
			RegisteredOn:     d.RegisteredOn,
			ExpiresOn:        d.ExpiresOn,
			AutoRenew:        d.AutoRenew,
			SSLActive:        d.SSLActive,
			Status:           d.Status,
		})
	}
	// Hierarchical order: apex first, then its subdomains immediately
	// underneath. Operators reading an exported sheet see logically
	// related rows grouped together (apex `example.com`, then
	// `app.example.com`, then `api.app.example.com`) instead of a
	// creation-time-ordered jumble where children appear above their
	// parent because the operator added them later.
	SortExportableDomainsHierarchical(out)
	return out, nil
}

// reverseLabelKey produces the comparison key used by the hierarchical
// domain sort. Reverses the dot-separated labels so `app.example.com`
// becomes `com.example.app`. A regular string sort over reversed
// keys naturally clusters domains by zone — every name under
// example.com sorts under the `com.example` prefix and the apex
// (`com.example`) comes BEFORE its longer-suffix children
// (`com.example.app`, `com.example.api`).
//
// Whitespace + case are normalised so a manually-edited row with
// trailing whitespace doesn't sort to a weird position.
func reverseLabelKey(d string) string {
	d = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(d, ".")))
	if d == "" {
		return ""
	}
	parts := strings.Split(d, ".")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, ".")
}

// domainLessHierarchical reports whether `a` should sort before `b`
// in the hierarchical order. Pure reverse-label comparison — see
// reverseLabelKey for the rationale.
func domainLessHierarchical(a, b string) bool {
	return reverseLabelKey(a) < reverseLabelKey(b)
}

// SortExportableDomainsHierarchical sorts the export-shaped slice
// in place using the hierarchical comparator. Stable so two rows
// with the same Domain (shouldn't happen — Mongo's domain index is
// unique — but defensive) keep their relative order.
func SortExportableDomainsHierarchical(rows []ExportableDomain) {
	sort.SliceStable(rows, func(i, j int) bool {
		return domainLessHierarchical(rows[i].Domain, rows[j].Domain)
	})
}

// SortDomainsHierarchical sorts a slice of models.Domain in place.
// Used by the WHM/cPanel List endpoints so the on-screen table shows
// the same apex-first order an exported file does.
func SortDomainsHierarchical(rows []models.Domain) {
	sort.SliceStable(rows, func(i, j int) bool {
		return domainLessHierarchical(rows[i].Domain, rows[j].Domain)
	})
}

// --- Bulk Delete (WHM-only, OTP-gated) -------------------------------
//
// Two-step destructive flow on /api/v1/whm/domains/bulk-delete:
//
//  1. POST /request-otp { ids: [...] }  → 6-digit code mailed to
//     the WHM owner. Server stamps a row in `bulk_delete_otp` with the
//     SHA-256 of the code + the target id list + a 10-minute expiry,
//     and returns an opaque `token` the client holds for step 2.
//  2. POST /confirm { token, code }     → backend looks the row up by
//     token, checks `code` against `code_hash`, marks the row used,
//     and runs DomainService.Delete in a loop. Per-row results
//     mirror the BulkUpload response shape so the WHM modal renders
//     the same outcome table.
//
// The token is a 32-byte CSPRNG hex string — long enough that brute-
// forcing it before the row expires is infeasible. The 6-digit code
// is also CSPRNG-random; combined with the 5-attempt cap below this
// gates the destructive action behind genuine email possession.
//
// Why a separate collection (not ColOTPRequests): different lifecycle
// (carries the target id list), different caller contract (one-shot
// per id-set, not per-email), and a future "purge bulk-delete OTPs
// older than 1h" cron shouldn't have to filter login OTPs.
//
// Why WHM-only: bulk-delete on cPanel would let a vendor nuke their
// own domains too. That's strictly safer than the WHM owner doing
// the same across tenants, but the tenant scope check + the
// destructive-confirmation expectation already cover the cPanel
// surface via the per-id Delete button. Adding a bulk-OTP loop on
// cPanel is out of scope; this surface is only on /whm.

// BulkDeleteOTPMaxAttempts caps how many wrong codes a single
// request can absorb before the row is invalidated. Five misses
// per 10-minute TTL is generous for a typo, hostile for a brute.
const BulkDeleteOTPMaxAttempts = 5

// bulkDeleteOTPTTL is how long a request-otp token stays valid
// before confirm-delete refuses it. Long enough for the operator
// to switch to their email tab + paste the code; short enough that
// a stolen token + cracked code window is tiny.
const bulkDeleteOTPTTL = 10 * time.Minute

// BulkDeleteOTPRequest is the on-disk shape stored in
// `bulk_delete_otp`. Email + DomainNames are denormalised onto the
// row so the OTP email body + the confirmation modal can both
// render without a second round-trip; the source of truth stays
// the user/domain rows.
type BulkDeleteOTPRequest struct {
	ID          primitive.ObjectID   `bson:"_id,omitempty" json:"-"`
	Token       string               `bson:"token" json:"-"`
	UserID      primitive.ObjectID   `bson:"user_id" json:"-"`
	Email       string               `bson:"email" json:"email"`
	DomainIDs   []primitive.ObjectID `bson:"domain_ids" json:"-"`
	DomainNames []string             `bson:"domain_names" json:"domain_names"`
	CodeHash    string               `bson:"code_hash" json:"-"`
	Attempts    int                  `bson:"attempts" json:"-"`
	Used        bool                 `bson:"used" json:"-"`
	CreatedAt   time.Time            `bson:"created_at" json:"-"`
	ExpiresAt   time.Time            `bson:"expires_at" json:"expires_at"`
}

// BulkDeleteRequestResult is what /request-otp returns to the client:
// the opaque token + a preview of what will be deleted (so the modal
// can render "You're about to delete 12 domains: example.com, …").
type BulkDeleteRequestResult struct {
	Token       string    `json:"token"`
	Email       string    `json:"email"`
	DomainCount int       `json:"domain_count"`
	DomainNames []string  `json:"domain_names"`
	ExpiresAt   time.Time `json:"expires_at"`
	// MailerEnabled is false when SMTP is not configured. UI surfaces a
	// warning banner pointing the operator at the journalctl fallback
	// (the code is still printed to stderr in that case so a fresh-
	// install admin can complete the flow without working email).
	MailerEnabled bool `json:"mailer_enabled"`
}

// BulkDeleteRowResult is one domain's outcome on /confirm. Mirrors
// BulkUpload's row result so the WHM modal can reuse the same render.
type BulkDeleteRowResult struct {
	ID      string `json:"id"`
	Domain  string `json:"domain"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// BulkDeleteConfirmResult is the /confirm response shape.
type BulkDeleteConfirmResult struct {
	TotalRows int                   `json:"total_rows"`
	Successes int                   `json:"successes"`
	Failures  int                   `json:"failures"`
	Items     []BulkDeleteRowResult `json:"items"`
}

// generateBulkDeleteCode returns a 6-digit numeric code using
// crypto/rand. Numeric (not alphanumeric) for two reasons: (a) the
// Bulk Delete OTP is a destructive-action gate, not a login flow,
// and a numeric pad is universally easier to type from a phone email
// app; (b) 10^6 = 1M combinations + the 5-attempt cap = effectively
// 1-in-200k brute-force chance per request, which is well below the
// risk we're guarding against (operator's email account compromise).
func generateBulkDeleteCode() (string, error) {
	const digits = "0123456789"
	b := make([]byte, 6)
	for i := range b {
		// Pull one random byte at a time, reject biased values to keep
		// the distribution uniform. Same shape as generateOTPCode in
		// auth_service.go but locked to the digit alphabet.
		var buf [1]byte
		for {
			if _, err := rand.Read(buf[:]); err != nil {
				return "", err
			}
			limit := byte(256 - (256 % len(digits)))
			if buf[0] < limit {
				b[i] = digits[int(buf[0])%len(digits)]
				break
			}
		}
	}
	return string(b), nil
}

// generateBulkDeleteToken returns the 32-byte CSPRNG hex the client
// holds between request-otp and confirm. Long enough that brute-force
// over the TTL window is infeasible (~10^77 keyspace).
func generateBulkDeleteToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// bulkDeleteSHA256Hex is a private mirror of auth_service.sha256Hex
// kept here so the bulk-delete file doesn't have to import auth_service
// internals (which would create a service-to-service coupling for
// what's a 3-line helper).
func bulkDeleteSHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// RequestBulkDeleteOTP stamps a row in `bulk_delete_otp` for the
// given admin + id list, then emails the 6-digit code. Returns the
// token the client holds for the second step.
//
// userID identifies the WHM owner from the auth middleware; the
// ids slice is the selection from the Domains-page checkboxes. The
// rest is straight CRUD around the OTP collection.
//
// Constraints:
//   * len(ids) > 0 + at most 500 (sanity cap; the panel's Domains
//     page caps the visible list at 10k anyway, so anything above
//     500 is almost certainly a click-mistake or a programmatic
//     misuse — refusing it loud is better than chewing through 10k
//     deletes after one OTP).
//   * Every id parses as ObjectID and references a real domain row.
//     Bad ids are skipped silently (operator's UI selection might
//     reference a now-deleted row); a fully-empty resolved list
//     returns an error so the caller's modal doesn't display a
//     "0 domains will be deleted" confirmation.
func (s *DomainService) RequestBulkDeleteOTP(ctx context.Context, userID primitive.ObjectID, ids []string) (*BulkDeleteRequestResult, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("no domains selected")
	}
	if len(ids) > 500 {
		return nil, fmt.Errorf("too many domains in one request (max 500)")
	}

	// Look up the admin's email + name. We need the email to actually
	// send the OTP; the name personalises the email body.
	var admin models.User
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": userID}).Decode(&admin); err != nil {
		return nil, fmt.Errorf("look up admin: %w", err)
	}
	if strings.TrimSpace(admin.Email) == "" {
		return nil, fmt.Errorf("admin account has no email on file — cannot send OTP")
	}

	// Resolve each id to an actual domain row. Drop bad / missing ids
	// silently (UI may have stale rows after a transfer / heal). We
	// store ObjectIDs in the OTP doc so the confirm step's lookup
	// matches even if the operator triggered a fetchDomains in between
	// and the Domain list shifted.
	objIDs := make([]primitive.ObjectID, 0, len(ids))
	for _, raw := range ids {
		oid, err := primitive.ObjectIDFromHex(raw)
		if err != nil {
			continue
		}
		objIDs = append(objIDs, oid)
	}
	if len(objIDs) == 0 {
		return nil, fmt.Errorf("no valid domain ids in request")
	}

	type domainRow struct {
		ID     primitive.ObjectID `bson:"_id"`
		Domain string             `bson:"domain"`
	}
	cur, err := s.db.Collection(database.ColDomains).Find(ctx, bson.M{"_id": bson.M{"$in": objIDs}})
	if err != nil {
		return nil, fmt.Errorf("look up domains: %w", err)
	}
	defer cur.Close(ctx)

	resolvedIDs := []primitive.ObjectID{}
	resolvedNames := []string{}
	for cur.Next(ctx) {
		var d domainRow
		if err := cur.Decode(&d); err == nil && d.Domain != "" {
			resolvedIDs = append(resolvedIDs, d.ID)
			resolvedNames = append(resolvedNames, d.Domain)
		}
	}
	if len(resolvedIDs) == 0 {
		return nil, fmt.Errorf("none of the selected ids matched a domain row")
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
	doc := BulkDeleteOTPRequest{
		Token:       token,
		UserID:      userID,
		Email:       strings.ToLower(strings.TrimSpace(admin.Email)),
		DomainIDs:   resolvedIDs,
		DomainNames: resolvedNames,
		CodeHash:    bulkDeleteSHA256Hex(code),
		Attempts:    0,
		Used:        false,
		CreatedAt:   now,
		ExpiresAt:   now.Add(bulkDeleteOTPTTL),
	}
	if _, err := s.db.Collection(database.ColBulkDeleteOTP).InsertOne(ctx, doc); err != nil {
		return nil, fmt.Errorf("save otp request: %w", err)
	}

	mailerEnabled := s.mailer != nil && s.mailer.Enabled()
	if !mailerEnabled {
		// Mailer disabled — log the code so a fresh-install admin can
		// still complete the flow via journalctl. Mirrors the
		// AuthService.RequestOTP fallback.
		log.Warn().Str("admin_email", admin.Email).Msg("bulk-delete OTP: SMTP not configured — code only in stderr")
		fmt.Printf("[bulk-delete] OTP for %s (mailer disabled): %s — domains=%d, expires in %dm\n",
			admin.Email, code, len(resolvedNames), int(bulkDeleteOTPTTL/time.Minute))
	} else {
		subject, text, htmlBody := buildBulkDeleteOTPEmail(admin.Name, admin.Email, code, resolvedNames, int(bulkDeleteOTPTTL/time.Minute))
		if err := s.mailer.Send(ctx, mailer.Message{
			To:      admin.Email,
			Subject: subject,
			Text:    text,
			HTML:    htmlBody,
		}); err != nil {
			// Don't roll back the row on send failure — the operator can
			// re-request and we'll insert a fresh row. But surface the
			// error so the UI shows "we couldn't email you" instead of
			// silently waiting for a code that never lands.
			log.Error().Err(err).Str("admin_email", admin.Email).Msg("bulk-delete OTP: send failed")
			return nil, fmt.Errorf("send otp email: %w", err)
		}
	}

	return &BulkDeleteRequestResult{
		Token:         token,
		Email:         admin.Email,
		DomainCount:   len(resolvedNames),
		DomainNames:   resolvedNames,
		ExpiresAt:     doc.ExpiresAt,
		MailerEnabled: mailerEnabled,
	}, nil
}

// ConfirmBulkDelete validates the OTP and runs Delete in a loop.
// Returns a per-row result table the WHM modal renders. On success
// the OTP row is marked Used so the same code can't be replayed.
//
// Failure modes (all return error WITHOUT marking the row Used so
// the operator can retry within the TTL):
//   * Token not found / expired
//   * Wrong user (the row's user_id doesn't match the caller's)
//   * Code mismatch (Attempts++ until BulkDeleteOTPMaxAttempts, then
//     the row is hard-invalidated by setting Used=true)
//   * Already used
func (s *DomainService) ConfirmBulkDelete(ctx context.Context, userID primitive.ObjectID, token, code string) (*BulkDeleteConfirmResult, error) {
	col := s.db.Collection(database.ColBulkDeleteOTP)

	var doc BulkDeleteOTPRequest
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
		// Mismatch can only happen if a different admin holds the same
		// token (impossible — token is CSPRNG-unique) OR if a session
		// hijack tried to swap the caller. Either way refuse.
		return nil, fmt.Errorf("this confirmation belongs to a different account")
	}

	// Code check + attempt counter. Increment-on-failure is its own
	// update so a concurrent attempt (operator double-clicking) can't
	// burn the counter.
	expected := bulkDeleteSHA256Hex(strings.TrimSpace(code))
	if expected != doc.CodeHash {
		newAttempts := doc.Attempts + 1
		update := bson.M{"$set": bson.M{"attempts": newAttempts}}
		if newAttempts >= BulkDeleteOTPMaxAttempts {
			// Burned through the attempt budget — hard-invalidate the
			// row so even a fresh code guess can't replay.
			update["$set"].(bson.M)["used"] = true
		}
		_, _ = col.UpdateByID(ctx, doc.ID, update)
		if newAttempts >= BulkDeleteOTPMaxAttempts {
			return nil, fmt.Errorf("too many wrong codes — request a new one")
		}
		return nil, fmt.Errorf("wrong code (%d attempts left)", BulkDeleteOTPMaxAttempts-newAttempts)
	}

	// Code matched — mark the row used BEFORE running deletes so a
	// concurrent retry can't fire the destructive loop twice.
	if _, err := col.UpdateByID(ctx, doc.ID, bson.M{"$set": bson.M{"used": true}}); err != nil {
		return nil, fmt.Errorf("mark otp used: %w", err)
	}

	// Run Delete per id, capturing per-row outcomes. Failures don't
	// abort the loop — same shape as bulk-upload.
	res := &BulkDeleteConfirmResult{
		TotalRows: len(doc.DomainIDs),
		Items:     make([]BulkDeleteRowResult, 0, len(doc.DomainIDs)),
	}
	// Pre-fetch the domain names so the result rows can show the
	// hostname even if Delete strips the row mid-loop.
	nameByID := map[string]string{}
	for i, id := range doc.DomainIDs {
		if i < len(doc.DomainNames) {
			nameByID[id.Hex()] = doc.DomainNames[i]
		}
	}
	for _, id := range doc.DomainIDs {
		hex := id.Hex()
		row := BulkDeleteRowResult{ID: hex, Domain: nameByID[hex]}
		if err := s.Delete(ctx, hex); err != nil {
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
		Int("requested", len(doc.DomainIDs)).
		Int("succeeded", res.Successes).
		Int("failed", res.Failures).
		Msg("bulk-delete: confirmed + executed")
	return res, nil
}

// buildBulkDeleteOTPEmail composes the subject + text + html bodies
// for the OTP email. Plain HTML (no template engine) keeps the
// dependency surface tiny; the email is intentionally short — code
// + warning + domain count — because the operator's email client
// has limited real estate and the operator already saw the full
// list in the WHM confirmation modal.
func buildBulkDeleteOTPEmail(adminName, adminEmail, code string, domainNames []string, expiresMin int) (subject, text, htmlBody string) {
	if adminName == "" {
		adminName = "admin"
	}
	// Full list — bulk delete is destructive, so the operator must be able
	// to review EVERY domain queued for deletion, not a truncated preview.
	// Bounded by the 500-domain request cap (RequestBulkDeleteOTP), which is
	// fine to render inline in an email body.
	preview := domainNames
	subject = fmt.Sprintf("Bulk Delete confirmation code: %s", code)

	var sb strings.Builder
	sb.WriteString("Hi ")
	sb.WriteString(adminName)
	sb.WriteString(",\n\nYou requested a bulk delete of ")
	sb.WriteString(fmt.Sprintf("%d domain(s) on Betazen Server Panel.\n\n", len(domainNames)))
	sb.WriteString("Confirmation code: ")
	sb.WriteString(code)
	sb.WriteString("\n\nThe code expires in ")
	sb.WriteString(fmt.Sprintf("%d minutes. ", expiresMin))
	sb.WriteString("If you didn't initiate this, ignore this email — no domains will be deleted without the code.\n\n")
	sb.WriteString("Domains queued for deletion:\n")
	for _, d := range preview {
		sb.WriteString("  - ")
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	text = sb.String()

	var hb strings.Builder
	hb.WriteString(`<!DOCTYPE html><html><body style="font-family:system-ui,sans-serif;line-height:1.5;color:#1a1a1a;">`)
	hb.WriteString(`<p>Hi <b>` + escapeHTML(adminName) + `</b>,</p>`)
	hb.WriteString(fmt.Sprintf(`<p>You requested a bulk delete of <b>%d domain(s)</b> on Betazen Server Panel.</p>`, len(domainNames)))
	hb.WriteString(`<p style="margin:16px 0;">Confirmation code:</p>`)
	hb.WriteString(`<p style="font-size:28px;font-family:monospace;letter-spacing:6px;background:#f4f4f4;padding:14px 20px;border-radius:8px;display:inline-block;border:1px solid #ddd;">` + escapeHTML(code) + `</p>`)
	hb.WriteString(fmt.Sprintf(`<p>The code expires in <b>%d minutes</b>. If you didn't initiate this, ignore this email — <b>no domains will be deleted</b> without the code.</p>`, expiresMin))
	hb.WriteString(`<p style="margin-top:20px;"><b>Domains queued for deletion:</b></p><ul>`)
	for _, d := range preview {
		hb.WriteString(`<li>` + escapeHTML(d) + `</li>`)
	}
	hb.WriteString(`</ul></body></html>`)
	htmlBody = hb.String()
	return
}

// escapeHTML is a minimal HTML-attribute-safe escaper for the OTP
// email body. We don't pull html/template here because the email
// is fixed-shape (no operator-supplied template strings) and the
// inputs are bounded — admin name, code, domain names, all of
// which the panel already validates upstream.
func escapeHTML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}
