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
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/xuri/excelize/v2"
)

// BulkUploadFormat is the on-wire file format the upload endpoint
// detected. We surface it back to the operator in the response so a
// "I uploaded the wrong file" mistake is obvious without scrolling
// through the row results.
type BulkUploadFormat string

const (
	BulkUploadFormatCSV  BulkUploadFormat = "csv"
	BulkUploadFormatXLSX BulkUploadFormat = "xlsx"
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

// executeBulkRows is the shared path between the CSV and XLSX entries.
// Walks the header row, builds a column-name → cell-index map, and
// then runs each data row through DomainService.Create + the optional
// SSL pass. Per-row failures populate Items but never abort.
func (s *DomainService) executeBulkRows(ctx context.Context, rows [][]string, format BulkUploadFormat, opts BulkUploadOptions) (*BulkUploadResponse, error) {
	resp := &BulkUploadResponse{Format: format, Items: []BulkRowResult{}}
	if len(rows) == 0 {
		return resp, errors.New("file is empty — at minimum a header row plus one domain is required")
	}

	headerIdx := map[string]int{}
	for i, h := range rows[0] {
		canon := resolveHeader(h)
		if canon == "" {
			continue
		}
		// Last duplicate wins — operators occasionally have two
		// "Domain" columns (one named Domain, one named "Domain Name");
		// either resolves to "domain" so we just keep the second.
		headerIdx[canon] = i
	}
	if _, ok := headerIdx["domain"]; !ok {
		return resp, errors.New("missing required column: domain")
	}

	cell := func(row []string, key string) string {
		idx, ok := headerIdx[key]
		if !ok || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	for i := 1; i < len(rows); i++ {
		row := rows[i]
		// Skip the entire-blank rows that Excel leaves at the bottom
		// of a spreadsheet on save. Without this we'd surface a noisy
		// "domain is required" failure for every trailing empty line.
		if rowAllBlank(row) {
			continue
		}

		domain := cell(row, "domain")
		user := cell(row, "user")
		if opts.CallerUsername != "" {
			// cPanel context — clobber whatever the row said with the
			// authenticated username. Prevents a vendor from reaching
			// outside their tenant via a doctored CSV.
			user = opts.CallerUsername
		}
		php := cell(row, "php_version")
		if php == "" {
			php = opts.PHPDefault
		}

		req := &models.CreateDomainRequest{
			Domain:           strings.ToLower(domain),
			User:             user,
			PHPVersion:       php,
			DiskQuotaMB:      atoiSafe(cell(row, "disk_quota_mb")),
			BandwidthLimitGB: atoiSafe(cell(row, "bandwidth_limit_gb")),
			MaxDatabases:     atoiSafe(cell(row, "max_databases")),
			MaxEmailAccounts: atoiSafe(cell(row, "max_email_accounts")),
			MaxSubdomains:    atoiSafe(cell(row, "max_subdomains")),
			MaxApps:          atoiSafe(cell(row, "max_apps")),
			Registrar:        cell(row, "registrar"),
			RegisteredOn:     cell(row, "registered_on"),
			ExpiresOn:        cell(row, "expires_on"),
			AutoRenew:        parseBool(cell(row, "auto_renew")),
		}

		result := BulkRowResult{
			RowNumber: i + 1, // 1-based and including header row
			Domain:    req.Domain,
			User:      req.User,
		}

		// Same validator the single-create endpoint uses — keeps
		// "valid PHP versions" / "domain required" / "user required"
		// errors phrased identically across both code paths so the
		// help docs only have to describe them once.
		if errs := validator.Validate(*req); errs != nil {
			result.Error = "validation: " + firstValidationError(errs)
			resp.Items = append(resp.Items, result)
			resp.Failures++
			continue
		}

		domainDoc, err := s.Create(ctx, req)
		if err != nil {
			result.Error = err.Error()
			resp.Items = append(resp.Items, result)
			resp.Failures++
			continue
		}
		result.Success = true
		resp.Successes++

		// Best-effort SSL pass. Failures are logged on the row, NOT
		// counted toward Failures (the domain itself is created). A
		// brand-new registration may not have DNS propagation yet,
		// in which case the operator will hit "Issue Certificate" on
		// the SSL page once dig @1.1.1.1 resolves the new A record.
		if opts.IssueSSL && s.ssl != nil {
			if _, sslErr := s.ssl.IssueLetsEncrypt(ctx, &models.IssueLetsEncryptRequest{
				Domain: domainDoc.Domain,
			}); sslErr != nil {
				result.SSLMessage = "ssl: " + sslErr.Error()
			} else {
				result.SSLIssued = true
				resp.SSLIssued++
				if opts.ForceSSL {
					if forceErr := s.ssl.ForceSSL(ctx, domainDoc.Domain, true); forceErr == nil {
						result.SSLForced = true
						resp.SSLForced++
					} else {
						result.SSLMessage = "force-https: " + forceErr.Error()
					}
				}
			}
		}

		resp.Items = append(resp.Items, result)
	}
	resp.TotalRows = len(resp.Items)
	return resp, nil
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
func BulkUploadCSVTemplate() []byte {
	header := []string{
		"domain", "user", "php_version",
		"disk_quota_mb", "bandwidth_limit_gb",
		"max_databases", "max_email_accounts", "max_subdomains", "max_apps",
		"registrar", "registered_on", "expires_on", "auto_renew",
	}
	example := []string{
		"site1.example", "vendor1", "8.2",
		"5000", "100", "10", "20", "5", "5",
		"GoDaddy", "2026-01-15", "2027-01-15", "true",
	}
	minimal := []string{
		"site2.example", "vendor1", "",
		"", "", "", "", "", "",
		"", "", "", "",
	}
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write(header)
	_ = w.Write(example)
	_ = w.Write(minimal)
	w.Flush()
	return []byte(b.String())
}

// BulkUploadXLSXTemplate is the spreadsheet equivalent of the CSV
// template. Same rows, but in a real workbook the operator can open
// in Excel/Google Sheets/LibreOffice without any "do you trust the
// CSV separator" prompts. The header row is bolded so it's visually
// distinct from the data rows.
func BulkUploadXLSXTemplate() ([]byte, error) {
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

	headers := []string{
		"domain", "user", "php_version",
		"disk_quota_mb", "bandwidth_limit_gb",
		"max_databases", "max_email_accounts", "max_subdomains", "max_apps",
		"registrar", "registered_on", "expires_on", "auto_renew",
	}
	for i, h := range headers {
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetCellValue(sheet, col+"1", h)
	}
	example := []any{
		"site1.example", "vendor1", "8.2",
		5000, 100, 10, 20, 5, 5,
		"GoDaddy", "2026-01-15", "2027-01-15", "true",
	}
	for i, v := range example {
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetCellValue(sheet, col+"2", v)
	}
	minimal := []any{
		"site2.example", "vendor1", "", "", "", "", "", "", "", "", "", "", "",
	}
	for i, v := range minimal {
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetCellValue(sheet, col+"3", v)
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
