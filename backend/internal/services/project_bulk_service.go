// Package services — project_bulk_service.go is the parser + executor
// behind the WHM "Bulk Upload Services" button on the Deploy Software
// project detail drawer.
//
// Why a dedicated file: the existing ProjectService.AddService runs a
// 500-line pipeline — clone, preset apply, package.json detect, install
// + build, port allocation, systemd unit, nginx vhost, Let's Encrypt
// expansion, env-var .env file, soft-delete on failure. Replicating any
// of that in a "batch add" code path would be a maintenance landmine.
// So this file does the parser + per-row dispatch ONLY: every row is
// converted into the same *models.AddServiceRequest the single-create
// form posts and then handed to AddService unchanged. That keeps the
// invariants (port conflicts, subpath uniqueness, framework presets,
// vhost reconciliation, SSL issuance) honest across both code paths.
//
// Partial success is normal: one row failing (bad domain, port clash,
// build failure) does NOT abort the others. Each row gets its own
// outcome line in the response so the operator can fix-and-re-upload
// only the failing rows.
//
// File format: CSV (RFC 4180) or XLSX (excelize). Shared parser shape
// with domain_bulk_service.go — we reuse normaliseHeader / rowAllBlank
// / atoiSafe / parseBool / firstValidationError from that file because
// they're in the same package.
//
// Column mapping (canonical names → operator-friendly aliases):
//
//	name           — required; unique within the project
//	role           — backend | frontend | static; default "backend" when
//	                 framework is unknown / blank, "frontend" when the
//	                 framework's preset is IsStatic (react-vite, vue-vite)
//	framework      — preset key (e.g. "nextjs", "node-express"); fills in
//	                 install/build/start cmds + default port at AddService
//	subpath        — monorepo subdir relative to the project's clone
//	path_prefix    — nginx location prefix (e.g. "/api" for a backend
//	                 sharing a domain with a frontend)
//	primary_domain — required; must already exist in the panel
//	port           — backend only; 0 / blank = auto-allocate
//	alias_domains  — semicolon-separated list (CSV's comma collides
//	                 with the field separator)
//
// Optional advanced columns (operators using the export-edit-reimport
// flow may want them; the parser tolerates either presence or absence):
//
//	install_cmd / build_cmd / start_cmd — override the framework preset
//	runtime_version — node/python/ruby/go version pin
//	git_branch      — only honored for legacy per-service-branch projects;
//	                  modern projects share the project-level branch
//	env_vars        — semicolon-separated KEY=VALUE pairs
//	user            — system user; default = primary_domain's owner
//
// Roles default policy: if the row leaves Role blank, we derive it
// from the framework's preset (IsStatic → "frontend"; AppType=node/go/
// python/ruby → "backend"). When the framework is unknown OR blank,
// we fall back to "backend" — the most common case, and one the
// validator's `oneof` accepts.
package services

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/xuri/excelize/v2"
)

// BulkServiceRowResult is one row's outcome. RowNumber is 1-based and
// includes the header row — the same number Excel/Sheets show at the
// left margin, so a non-engineer can find the offending line without
// counting indices.
//
// FinalPort surfaces the port AddService actually allocated for a
// backend service — operators uploading 10 backends with no port
// column need to know what they got so they can update DNS / firewall
// rules without re-querying the API row-by-row.
type BulkServiceRowResult struct {
	RowNumber     int    `json:"row_number"`
	Name          string `json:"name"`
	Role          string `json:"role"`
	Framework     string `json:"framework"`
	PrimaryDomain string `json:"primary_domain"`
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
	// ServiceID is the just-created service's hex id, populated on
	// success so the UI can deep-link straight from the result row
	// to the per-service detail drawer.
	ServiceID string `json:"service_id,omitempty"`
	// FinalPort is the port AddService picked (either the operator's
	// requested value or the auto-allocated one for backend services).
	// Zero for frontend/static rows that have no listening port.
	FinalPort int `json:"final_port,omitempty"`
	// MissingEnvKeys mirrors models.ProjectService.MissingEnvKeys —
	// non-empty when the cloned repo declared .env.example keys that
	// the row didn't supply. The service still lands in the DB +
	// nginx + systemd; the WHM banner asks the operator to fill the
	// keys and start the service. Surfacing it in the bulk-upload
	// row so the operator sees "needs env vars" before the modal
	// closes instead of refreshing the page to find a yellow banner.
	MissingEnvKeys []string `json:"missing_env_keys,omitempty"`
}

// BulkServicesUploadResponse is the JSON the API returns. Counters
// at the top let the UI render a one-line summary ("8 of 10 services
// added, 2 failed") without re-walking Items.
type BulkServicesUploadResponse struct {
	Format    BulkUploadFormat        `json:"format"`
	TotalRows int                     `json:"total_rows"`
	Successes int                     `json:"successes"`
	Failures  int                     `json:"failures"`
	Items     []BulkServiceRowResult  `json:"items"`
}

// serviceHeaderAliases collapses common header variants (Title-Case from
// Excel, snake_case from a hand-edited CSV, the abbreviated forms an
// operator might prefer) onto the canonical column names the executor
// reads. Matched after normaliseHeader strips space/underscore/dash/dot
// and lowercases.
var serviceHeaderAliases = map[string]string{
	"name":           "name",
	"servicename":    "name",
	"service":        "name",
	"role":           "role",
	"servicerole":    "role",
	"type":           "role",
	"framework":      "framework",
	"preset":         "framework",
	"app":            "framework",
	"subpath":        "subpath",
	"gitsubpath":     "subpath",
	"path":           "subpath",
	"pathprefix":     "path_prefix",
	"nginxprefix":    "path_prefix",
	"locationprefix": "path_prefix",
	"primarydomain":  "primary_domain",
	"domain":         "primary_domain",
	"host":           "primary_domain",
	"hostname":       "primary_domain",
	"port":           "port",
	"aliasdomains":   "alias_domains",
	"aliases":        "alias_domains",
	"alias":          "alias_domains",
	"altdomains":     "alias_domains",
	"installcmd":     "install_cmd",
	"install":        "install_cmd",
	"buildcmd":       "build_cmd",
	"build":          "build_cmd",
	"startcmd":       "start_cmd",
	"start":          "start_cmd",
	"runtimeversion": "runtime_version",
	"runtime":        "runtime_version",
	"nodeversion":    "runtime_version",
	"gitbranch":      "git_branch",
	"branch":         "git_branch",
	"envvars":        "env_vars",
	"env":            "env_vars",
	"environment":    "env_vars",
	"user":           "user",
	"username":       "user",
	"owner":          "user",
}

// resolveServiceHeader maps a (normalised) input header to the canonical
// AddServiceRequest field name, or "" when it's an unknown column.
// Unknown columns are not fatal — operators sometimes keep a Notes column
// and we shouldn't reject the file for it.
func resolveServiceHeader(in string) string {
	if canon, ok := serviceHeaderAliases[normaliseHeader(in)]; ok {
		return canon
	}
	return ""
}

// BulkAddServicesFromContentType picks the right parser based on the
// uploaded file's content-type / filename, then runs the parsed rows
// through AddService one at a time. Per-row failures populate Items
// but never abort the batch.
//
// The 10 MB cap mirrors domain_bulk_service.go's contract — even a
// 1000-service spreadsheet stays well under that limit, and a multi-
// GB upload would OOM the panel.
func (s *ProjectService) BulkAddServicesFromContentType(
	ctx context.Context,
	projectID string,
	body io.Reader,
	contentType string,
	filename string,
) (*BulkServicesUploadResponse, error) {
	ct := strings.ToLower(contentType)
	lowName := strings.ToLower(filename)
	if strings.Contains(ct, "csv") || strings.HasSuffix(lowName, ".csv") {
		return s.bulkAddServicesCSV(ctx, projectID, body)
	}
	if strings.Contains(ct, "spreadsheet") || strings.Contains(ct, "excel") ||
		strings.HasSuffix(lowName, ".xlsx") || strings.HasSuffix(lowName, ".xls") {
		return s.bulkAddServicesXLSX(ctx, projectID, body)
	}
	// Last-resort sniff: peek the first chunk for the XLSX zip magic.
	// Anything else routes to CSV.
	peek := make([]byte, 512)
	n, _ := io.ReadFull(body, peek)
	peek = peek[:n]
	combined := io.MultiReader(strings.NewReader(string(peek)), body)
	if n >= 4 && string(peek[:4]) == "PK\x03\x04" {
		return s.bulkAddServicesXLSX(ctx, projectID, combined)
	}
	return s.bulkAddServicesCSV(ctx, projectID, combined)
}

func (s *ProjectService) bulkAddServicesCSV(ctx context.Context, projectID string, body io.Reader) (*BulkServicesUploadResponse, error) {
	r := csv.NewReader(body)
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse csv: %w", err)
	}
	return s.executeBulkServiceRows(ctx, projectID, rows, BulkUploadFormatCSV)
}

func (s *ProjectService) bulkAddServicesXLSX(ctx context.Context, projectID string, body io.Reader) (*BulkServicesUploadResponse, error) {
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
	return s.executeBulkServiceRows(ctx, projectID, rows, BulkUploadFormatXLSX)
}

// executeBulkServiceRows is the shared body of the CSV + XLSX entries.
// Walks the header row, builds a canonical-name → cell-index map, and
// then runs each data row through AddService. Per-row failures populate
// Items but never abort.
func (s *ProjectService) executeBulkServiceRows(ctx context.Context, projectID string, rows [][]string, format BulkUploadFormat) (*BulkServicesUploadResponse, error) {
	resp := &BulkServicesUploadResponse{Format: format, Items: []BulkServiceRowResult{}}
	if len(rows) == 0 {
		return resp, errors.New("file is empty — at minimum a header row plus one service is required")
	}

	headerIdx := map[string]int{}
	for i, h := range rows[0] {
		canon := resolveServiceHeader(h)
		if canon == "" {
			continue
		}
		headerIdx[canon] = i
	}
	if _, ok := headerIdx["name"]; !ok {
		return resp, errors.New("missing required column: name")
	}
	if _, ok := headerIdx["primary_domain"]; !ok {
		return resp, errors.New("missing required column: primary_domain")
	}

	for i := 1; i < len(rows); i++ {
		row := rows[i]
		// Skip the entire-blank rows that Excel leaves at the bottom of
		// a sheet on save — without this we'd surface noisy "name is
		// required" failures for every trailing empty line.
		if rowAllBlank(row) {
			continue
		}

		req := buildBulkServiceRequest(row, headerIdx)

		result := BulkServiceRowResult{
			RowNumber:     i + 1, // 1-based + header row
			Name:          req.Name,
			Role:          req.Role,
			Framework:     req.Framework,
			PrimaryDomain: req.PrimaryDomain,
		}

		// Validate up front — same validator the single-create
		// endpoint runs, so error wording is identical across both
		// paths. Skip the row on a validation failure (cheap; no
		// side effects yet).
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
		// Refresh the surface fields from the persisted service —
		// AddService may have re-applied framework presets (auto-
		// detected from package.json) or remapped a backend to a
		// static role, and the result table should reflect that.
		result.Role = svc.Role
		result.Framework = svc.Framework
		resp.Items = append(resp.Items, result)
		resp.Successes++
	}

	resp.TotalRows = len(resp.Items)
	return resp, nil
}

// buildBulkServiceRequest converts a single CSV/XLSX row + the
// header-name → column-index map into the AddServiceRequest the
// single-create form posts. Extracted so a parser test can exercise
// the row → request mapping (including role defaulting + alias /
// env-var encoding) without needing a real *ProjectService bound to
// a mongo handle.
//
// Role defaulting policy: if the row leaves role blank, we derive
// it from the framework's preset (IsStatic → "frontend"; anything
// else → "backend"). Unknown / blank framework also falls back to
// "backend" — the most common case and a value the validator's
// `oneof=backend frontend static` accepts.
func buildBulkServiceRequest(row []string, headerIdx map[string]int) *models.AddServiceRequest {
	cell := func(key string) string {
		idx, ok := headerIdx[key]
		if !ok || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	framework := strings.ToLower(cell("framework"))
	role := strings.ToLower(cell("role"))
	if role == "" {
		if framework != "" {
			if p, ok := lookupPreset(framework); ok {
				if p.IsStatic {
					role = "frontend"
				} else {
					role = "backend"
				}
			} else {
				role = "backend"
			}
		} else {
			role = "backend"
		}
	}

	return &models.AddServiceRequest{
		Name:           strings.ToLower(cell("name")),
		Role:           role,
		Framework:      framework,
		GitSubpath:     cell("subpath"),
		GitBranch:      cell("git_branch"),
		PathPrefix:     cell("path_prefix"),
		PrimaryDomain:  strings.ToLower(cell("primary_domain")),
		AliasDomains:   parseSemicolonList(cell("alias_domains")),
		InstallCmd:     cell("install_cmd"),
		BuildCmd:       cell("build_cmd"),
		StartCmd:       cell("start_cmd"),
		RuntimeVersion: cell("runtime_version"),
		Port:           atoiSafe(cell("port")),
		EnvVars:        parseEnvVarPairs(cell("env_vars")),
		User:           cell("user"),
	}
}

// bulkServiceErrorMessage flattens an AddService error into a single
// line suited to the result-table column. Build errors carry a long
// multi-paragraph Details field (the stage's stderr); the row table
// only has space for a summary, so we surface Stage + Summary and let
// the operator drill in via the live deploy logs if they need the
// full output. Everything else is passed through unchanged.
func bulkServiceErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if be, ok := err.(*BuildError); ok {
		return fmt.Sprintf("%s failed: %s", be.Stage, be.Summary)
	}
	return err.Error()
}

// parseSemicolonList splits "a.example;b.example; c.example" into a
// trimmed, blank-free slice. Semicolon because CSV's comma collides
// with the field separator and operators editing in Excel would have
// to quote every cell containing aliases.
func parseSemicolonList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseEnvVarPairs splits "API_KEY=abc;FLAG=on" into a map.
// Unterminated lines (a single token with no =) are dropped silently
// so a typo doesn't fail an otherwise-good row; the AddService env-var
// validator will still catch shell-injection-risk keys.
func parseEnvVarPairs(s string) map[string]string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := map[string]string{}
	for _, p := range strings.Split(s, ";") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(p[:eq])
		v := strings.TrimSpace(p[eq+1:])
		if k == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// --- Templates -------------------------------------------------------
//
// Both templates are generated from code (not embedded files) so the
// column set stays in lock-step with AddServiceRequest — a future field
// added to the struct is one edit here, not a forgotten static asset.
//
// The example rows demonstrate two layouts:
//
//  1. A Node.js backend on an apex domain, framework=node-express, with
//     a build/install command from the preset.
//  2. A Vite static frontend on a subdomain, framework=react-vite,
//     role auto-derived (left blank — the preset is IsStatic).
//  3. A minimum-viable row: just name + primary_domain + framework. The
//     parser fills role from the preset; AddService fills install/build/
//     start cmds + port from the preset. Demonstrates how spare a row
//     can be when the framework preset carries the defaults.

// BulkAddServicesCSVTemplate returns the CSV template bytes.
func BulkAddServicesCSVTemplate() []byte {
	header := []string{
		"name", "role", "framework", "subpath", "path_prefix",
		"primary_domain", "port", "alias_domains",
		"install_cmd", "build_cmd", "start_cmd",
		"runtime_version", "git_branch", "env_vars", "user",
	}
	example1 := []string{
		"api", "backend", "node-express", "apps/api", "/api",
		"api.example.com", "3000", "www.api.example.com",
		"", "", "", "20", "", "NODE_ENV=production;LOG_LEVEL=info", "",
	}
	example2 := []string{
		"web", "", "react-vite", "apps/web", "",
		"www.example.com", "", "example.com",
		"", "", "", "20", "", "", "",
	}
	example3 := []string{
		"worker", "", "nextjs", "", "",
		"app.example.com", "", "",
		"", "", "", "", "", "", "",
	}

	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write(header)
	_ = w.Write(example1)
	_ = w.Write(example2)
	_ = w.Write(example3)
	w.Flush()
	return []byte(b.String())
}

// BulkAddServicesXLSXTemplate emits the .xlsx equivalent — same column
// shape + example rows, plus a bold header row and reasonable column
// widths so "primary_domain" / "alias_domains" don't clip in Excel.
func BulkAddServicesXLSXTemplate() ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Services"
	idx, err := f.NewSheet(sheet)
	if err != nil {
		return nil, err
	}
	f.SetActiveSheet(idx)
	_ = f.DeleteSheet("Sheet1")

	headers := []string{
		"name", "role", "framework", "subpath", "path_prefix",
		"primary_domain", "port", "alias_domains",
		"install_cmd", "build_cmd", "start_cmd",
		"runtime_version", "git_branch", "env_vars", "user",
	}
	rows := [][]any{
		{"api", "backend", "node-express", "apps/api", "/api",
			"api.example.com", 3000, "www.api.example.com",
			"", "", "", "20", "", "NODE_ENV=production;LOG_LEVEL=info", ""},
		{"web", "", "react-vite", "apps/web", "",
			"www.example.com", "", "example.com",
			"", "", "", "20", "", "", ""},
		{"worker", "", "nextjs", "", "",
			"app.example.com", "", "",
			"", "", "", "", "", "", ""},
	}

	for i, h := range headers {
		col, _ := excelize.ColumnNumberToName(i + 1)
		_ = f.SetCellValue(sheet, col+"1", h)
	}
	for ri, r := range rows {
		for ci, v := range r {
			col, _ := excelize.ColumnNumberToName(ci + 1)
			_ = f.SetCellValue(sheet, fmt.Sprintf("%s%d", col, ri+2), v)
		}
	}
	// Note row at the bottom so an operator opening the file in Excel
	// sees the alias / env-var encoding without reading docs.
	noteCol, _ := excelize.ColumnNumberToName(1)
	_ = f.SetCellValue(sheet, fmt.Sprintf("%s%d", noteCol, len(rows)+3),
		"Note: alias_domains and env_vars use SEMICOLON as separator (CSV's comma collides with field boundaries). Example env_vars: KEY1=value1;KEY2=value2. Blank role + known framework → role derived from preset (static frameworks like react-vite/vue-vite become frontend; everything else becomes backend). Blank install/build/start cmds + a framework preset → AddService fills them from the preset automatically.")

	// Bold header row.
	if styleID, sErr := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}}); sErr == nil {
		lastCol, _ := excelize.ColumnNumberToName(len(headers))
		_ = f.SetCellStyle(sheet, "A1", lastCol+"1", styleID)
	}
	_ = f.SetColWidth(sheet, "A", "O", 20)

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BulkAddServicesCSVTemplateName / BulkAddServicesXLSXTemplateName are
// the suggested filenames served via Content-Disposition.
func BulkAddServicesCSVTemplateName() string {
	return fmt.Sprintf("services-bulk-upload-template-%s.csv", time.Now().Format("2006-01-02"))
}

func BulkAddServicesXLSXTemplateName() string {
	return fmt.Sprintf("services-bulk-upload-template-%s.xlsx", time.Now().Format("2006-01-02"))
}

// _ keeps strconv imported even when nobody outside the package
// references it directly — the row-level Atoi is in atoiSafe (from
// domain_bulk_service.go) but a future field added here may need it
// inline.
var _ = strconv.Atoi
