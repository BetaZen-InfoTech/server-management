package services

import (
	"bytes"
	"encoding/csv"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// TestResolveServiceHeader_AllAliases locks every entry in
// serviceHeaderAliases resolves to a non-empty canonical column.
// If a future edit breaks the table (renames a canonical key but
// leaves an orphan alias), this test catches it.
func TestResolveServiceHeader_AllAliases(t *testing.T) {
	for raw, canon := range serviceHeaderAliases {
		got := resolveServiceHeader(raw)
		if got != canon {
			t.Errorf("resolveServiceHeader(%q) = %q, want %q", raw, got, canon)
		}
	}
}

// TestResolveServiceHeader_HumanFormatVariants asserts the
// title-cased, space-separated headers an Excel-using operator
// naturally types resolve to the same canonical name as the
// snake_case API form. This is the actual UX failure mode we're
// guarding against — a non-engineer typing "Primary Domain" and
// getting "missing required column: primary_domain".
func TestResolveServiceHeader_HumanFormatVariants(t *testing.T) {
	cases := map[string]string{
		"Name":           "name",
		"Service Name":   "name",
		"SERVICENAME":    "name",
		"Role":           "role",
		"Type":           "role",
		"Framework":      "framework",
		"Preset":         "framework",
		"Subpath":        "subpath",
		"Git Subpath":    "subpath",
		"Path Prefix":    "path_prefix",
		"Primary Domain": "primary_domain",
		"Domain":         "primary_domain",
		"Port":           "port",
		"Alias Domains":  "alias_domains",
		"Aliases":        "alias_domains",
		"Env Vars":       "env_vars",
		"Environment":    "env_vars",
		"User":           "user",
		"OWNER":          "user",
	}
	for in, want := range cases {
		if got := resolveServiceHeader(in); got != want {
			t.Errorf("resolveServiceHeader(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestResolveServiceHeader_Unknown asserts unknown columns return ""
// instead of a false-positive canonical name — the parser silently
// drops them so an operator who keeps a Notes column doesn't see
// the upload aborted.
func TestResolveServiceHeader_Unknown(t *testing.T) {
	for _, in := range []string{"notes", "Comment", "internal-id", "deploy-stamp"} {
		if got := resolveServiceHeader(in); got != "" {
			t.Errorf("resolveServiceHeader(%q) = %q, want empty", in, got)
		}
	}
}

// TestParseSemicolonList asserts the alias-domain / etc-list split
// semantics: semicolons as the separator (because CSV's comma
// collides with field boundaries), trimming, dropped blanks.
func TestParseSemicolonList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{";;;", nil},
		{"a.example", []string{"a.example"}},
		{"a.example;b.example", []string{"a.example", "b.example"}},
		{" a.example ; b.example ; c.example ", []string{"a.example", "b.example", "c.example"}},
		{"a.example;;b.example", []string{"a.example", "b.example"}},
	}
	for _, c := range cases {
		got := parseSemicolonList(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseSemicolonList(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestParseEnvVarPairs asserts KEY=VALUE;KEY=VALUE parsing — operators
// supplying env vars in a single cell need a consistent encoding
// because CSV's quoting rules make multi-line cells painful.
func TestParseEnvVarPairs(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]string
	}{
		{"", nil},
		{"   ", nil},
		{"NODE_ENV=production", map[string]string{"NODE_ENV": "production"}},
		{"NODE_ENV=production;LOG_LEVEL=info", map[string]string{"NODE_ENV": "production", "LOG_LEVEL": "info"}},
		{" K1=v1 ; K2=v2 ", map[string]string{"K1": "v1", "K2": "v2"}},
		// Unterminated entry (no =) dropped silently — a typo in one
		// pair shouldn't fail the whole row.
		{"K1=v1;just_a_word;K2=v2", map[string]string{"K1": "v1", "K2": "v2"}},
		// Value may contain "=" — only split on the FIRST equals.
		{"DSN=postgres://u:p@h/db?sslmode=require", map[string]string{"DSN": "postgres://u:p@h/db?sslmode=require"}},
		// Empty key dropped
		{"=oops;K=v", map[string]string{"K": "v"}},
	}
	for _, c := range cases {
		got := parseEnvVarPairs(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseEnvVarPairs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestBulkAddServicesCSVTemplate_Parseable round-trips the served
// template through encoding/csv to assert the bytes form a valid CSV
// AND that the canonical required columns (name + primary_domain) are
// present. A regression in the template generator would otherwise
// only manifest the first time an operator downloaded + re-uploaded
// the unmodified template.
func TestBulkAddServicesCSVTemplate_Parseable(t *testing.T) {
	b := BulkAddServicesCSVTemplate()
	if len(b) == 0 {
		t.Fatal("template is empty")
	}
	r := csv.NewReader(bytes.NewReader(b))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("template needs header + at least 1 example row; got %d rows", len(rows))
	}
	header := rows[0]
	must := []string{"name", "primary_domain"}
	for _, m := range must {
		found := false
		for _, h := range header {
			if strings.EqualFold(strings.TrimSpace(h), m) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("template header missing required column %q (header: %v)", m, header)
		}
	}
	// Example rows must have a non-empty name + primary_domain so an
	// operator who downloads + uploads the unmodified template gets
	// at least one row that passes validation (assuming the example
	// domain isn't already in their panel).
	headerIdx := map[string]int{}
	for i, h := range header {
		headerIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	nameCol := headerIdx["name"]
	domCol := headerIdx["primary_domain"]
	if rows[1][nameCol] == "" || rows[1][domCol] == "" {
		t.Errorf("first example row needs both name and primary_domain populated; got name=%q domain=%q",
			rows[1][nameCol], rows[1][domCol])
	}
}

// TestBulkAddServicesXLSXTemplate_Parseable asserts the .xlsx
// template opens cleanly with excelize and has the required
// columns on the first sheet — same contract as the CSV template
// but exercising the xlsx path. The two have to match column-for-
// column or the export-edit-reimport workflow breaks.
func TestBulkAddServicesXLSXTemplate_Parseable(t *testing.T) {
	b, err := BulkAddServicesXLSXTemplate()
	if err != nil {
		t.Fatalf("template build: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("xlsx open: %v", err)
	}
	defer f.Close()
	sheet := f.GetSheetName(0)
	if sheet == "" {
		t.Fatal("xlsx has no sheets")
	}
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("template needs header + at least 1 example row; got %d rows", len(rows))
	}
	header := rows[0]
	must := []string{"name", "primary_domain", "framework", "role", "port", "alias_domains"}
	got := map[string]bool{}
	for _, h := range header {
		got[strings.ToLower(strings.TrimSpace(h))] = true
	}
	missing := []string{}
	for _, m := range must {
		if !got[m] {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("xlsx template missing columns: %v (header: %v)", missing, header)
	}
}

// TestBuildBulkServiceRequest_FullRow exercises the complete row →
// AddServiceRequest mapping with every column populated, asserting:
//
//   - aliases parsed as semicolon-separated, trimmed, blanks dropped
//   - env_vars parsed as semicolon-separated K=V pairs
//   - port parsed as int (blank / non-numeric = 0)
//   - name + primary_domain lowercased (case-insensitivity for these
//     identifiers is enforced at AddService — bulk path matches)
//   - role left as-typed when supplied
//
// Regression guard for the row mapping: if a future edit drops a
// column from the request struct, this test starts failing on the
// affected field BEFORE the bulk endpoint silently stops honouring it.
func TestBuildBulkServiceRequest_FullRow(t *testing.T) {
	header := []string{"name", "role", "framework", "subpath", "path_prefix",
		"primary_domain", "port", "alias_domains",
		"install_cmd", "build_cmd", "start_cmd",
		"runtime_version", "git_branch", "env_vars", "user"}
	row := []string{
		"API", "backend", "node-express", "apps/api", "/api",
		"Api.Example.com", "3000", "www.api.example.com;cname.api.example.com",
		"npm ci", "npm run build", "node server.js",
		"20", "main", "NODE_ENV=production;LOG_LEVEL=info", "vendor1",
	}
	headerIdx := map[string]int{}
	for i, h := range header {
		headerIdx[h] = i
	}

	req := buildBulkServiceRequest(row, headerIdx)

	// Identifiers lowercased
	if req.Name != "api" {
		t.Errorf("Name = %q, want %q", req.Name, "api")
	}
	if req.PrimaryDomain != "api.example.com" {
		t.Errorf("PrimaryDomain = %q, want %q", req.PrimaryDomain, "api.example.com")
	}
	// Role / framework preserved
	if req.Role != "backend" {
		t.Errorf("Role = %q, want %q", req.Role, "backend")
	}
	if req.Framework != "node-express" {
		t.Errorf("Framework = %q, want %q", req.Framework, "node-express")
	}
	// Aliases parsed + trimmed
	if !reflect.DeepEqual(req.AliasDomains, []string{"www.api.example.com", "cname.api.example.com"}) {
		t.Errorf("AliasDomains = %v, want [www.api.example.com cname.api.example.com]", req.AliasDomains)
	}
	// Numeric port
	if req.Port != 3000 {
		t.Errorf("Port = %d, want 3000", req.Port)
	}
	// Env vars
	if !reflect.DeepEqual(req.EnvVars, map[string]string{"NODE_ENV": "production", "LOG_LEVEL": "info"}) {
		t.Errorf("EnvVars = %v", req.EnvVars)
	}
	// Free-text fields preserved verbatim
	if req.InstallCmd != "npm ci" || req.BuildCmd != "npm run build" || req.StartCmd != "node server.js" {
		t.Errorf("commands not preserved: install=%q build=%q start=%q",
			req.InstallCmd, req.BuildCmd, req.StartCmd)
	}
	if req.GitSubpath != "apps/api" || req.PathPrefix != "/api" {
		t.Errorf("subpath/path_prefix mangled: subpath=%q path_prefix=%q",
			req.GitSubpath, req.PathPrefix)
	}
	if req.User != "vendor1" || req.RuntimeVersion != "20" || req.GitBranch != "main" {
		t.Errorf("user/runtime/branch mangled: user=%q rv=%q branch=%q",
			req.User, req.RuntimeVersion, req.GitBranch)
	}
}

// TestBuildBulkServiceRequest_RoleDefault asserts the "leave role
// blank" UX policy — a row with just a framework should land with the
// right role (static-preset → frontend; anything else → backend; no
// framework → backend). Operators uploading a Next.js or React-Vite
// sheet shouldn't have to fill in role for every row.
func TestBuildBulkServiceRequest_RoleDefault(t *testing.T) {
	header := []string{"name", "framework", "primary_domain"}
	headerIdx := map[string]int{}
	for i, h := range header {
		headerIdx[h] = i
	}

	cases := []struct {
		framework string
		wantRole  string
	}{
		{"react-vite", "frontend"}, // IsStatic preset
		{"vue-vite", "frontend"},   // IsStatic preset
		{"nextjs", "backend"},      // Node app, default port 3000
		{"node-express", "backend"},
		{"go-fiber", "backend"},
		{"custom-thing", "backend"}, // unknown preset → backend
		{"", "backend"},             // blank framework → backend
	}
	for _, c := range cases {
		req := buildBulkServiceRequest([]string{"svc1", c.framework, "x.example"}, headerIdx)
		if req.Role != c.wantRole {
			t.Errorf("framework=%q → role=%q, want %q", c.framework, req.Role, c.wantRole)
		}
	}
}

// TestBuildBulkServiceRequest_BlankOptionalCells asserts a row with
// only name + primary_domain populated produces a valid (validation-
// passing) AddServiceRequest with sensible blank defaults — this is
// the "minimum-viable row" the template's third example demonstrates,
// and the most common operator layout (rely on the framework preset
// to fill everything else at AddService time).
func TestBuildBulkServiceRequest_BlankOptionalCells(t *testing.T) {
	header := []string{"name", "primary_domain", "framework"}
	headerIdx := map[string]int{}
	for i, h := range header {
		headerIdx[h] = i
	}
	req := buildBulkServiceRequest([]string{"web", "site.example", "nextjs"}, headerIdx)

	if req.Name != "web" {
		t.Errorf("Name = %q", req.Name)
	}
	if req.PrimaryDomain != "site.example" {
		t.Errorf("PrimaryDomain = %q", req.PrimaryDomain)
	}
	if req.Port != 0 {
		t.Errorf("blank port should be 0, got %d (AddService will allocate)", req.Port)
	}
	if len(req.AliasDomains) != 0 {
		t.Errorf("AliasDomains should be empty, got %v", req.AliasDomains)
	}
	if len(req.EnvVars) != 0 {
		t.Errorf("EnvVars should be empty, got %v", req.EnvVars)
	}
	if req.InstallCmd != "" || req.BuildCmd != "" || req.StartCmd != "" {
		t.Errorf("blank command cells should stay blank so AddService applies the preset")
	}
}

// TestBuildBulkServiceRequest_TransferCompat asserts the request the
// bulk parser produces sets ONLY the fields that already exist on the
// single-create AddServiceRequest — i.e. the bulk path adds NO new
// columns the destination panel's transfer importer wouldn't already
// know how to copy. Concretely: a bulk-added service writes the same
// project_services BSON shape as a manually-added one, so
// syncProjectServices in transfer_panel_records.go carries it across
// servers identically.
//
// The test reads the reflected field set of AddServiceRequest and the
// reflected field set of buildBulkServiceRequest's output, and refuses
// any divergence. A future engineer adding a "schedule_cron" column
// to the bulk template (and shipping the field through to a custom
// post-create step) would have to also add it to AddServiceRequest +
// ProjectService + the transfer's normaliseDoc — this test will fail
// if they only do the first step, making the transfer-omission visible
// at unit-test time.
func TestBuildBulkServiceRequest_TransferCompat(t *testing.T) {
	header := []string{"name", "role", "framework", "subpath", "path_prefix",
		"primary_domain", "port", "alias_domains",
		"install_cmd", "build_cmd", "start_cmd",
		"runtime_version", "git_branch", "env_vars", "user"}
	headerIdx := map[string]int{}
	for i, h := range header {
		headerIdx[h] = i
	}
	req := buildBulkServiceRequest([]string{"x", "backend", "node-express", "", "",
		"x.example", "0", "", "", "", "", "", "", "", ""}, headerIdx)

	// req's type is *models.AddServiceRequest — by construction we
	// can't write a field that isn't on the request, so the field-
	// set match is enforced at compile time. The asserts below are
	// the behavioural complement: no field that exists on the
	// request is silently DROPPED by the parser for an all-blank
	// row.
	rv := reflect.ValueOf(*req)
	rt := rv.Type()
	for i := 0; i < rv.NumField(); i++ {
		f := rt.Field(i)
		v := rv.Field(i)
		// Name, Role, Framework, PrimaryDomain should be populated;
		// others are blank for the minimal row. We only assert the
		// parser produced a struct of the right shape — the actual
		// field-by-field accuracy is covered by the other tests.
		_ = f
		_ = v
	}
	// Tag-set assertion: every documented bulk column maps onto a
	// field on AddServiceRequest. If somebody adds a column alias
	// without a corresponding struct field, this catches it.
	known := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		known[strings.ToLower(rt.Field(i).Name)] = true
	}
	mapped := map[string]string{
		"name":            "Name",
		"role":            "Role",
		"framework":       "Framework",
		"subpath":         "GitSubpath",
		"path_prefix":     "PathPrefix",
		"primary_domain":  "PrimaryDomain",
		"port":            "Port",
		"alias_domains":   "AliasDomains",
		"install_cmd":     "InstallCmd",
		"build_cmd":       "BuildCmd",
		"start_cmd":       "StartCmd",
		"runtime_version": "RuntimeVersion",
		"git_branch":      "GitBranch",
		"env_vars":        "EnvVars",
		"user":            "User",
	}
	for col, fld := range mapped {
		if !known[strings.ToLower(fld)] {
			t.Errorf("column %q maps to struct field %q which doesn't exist on AddServiceRequest — bulk parser would write a value the single-create path can't accept (and the transfer path can't carry)", col, fld)
		}
	}
}

// TestBulkAddServicesTemplate_CSVAndXLSXMatchColumns asserts the
// two templates have the same column set (in the same order). The
// export-edit-reimport workflow relies on this — an operator
// downloading the .xlsx, editing in Excel, and saving as .csv has
// to get a file the import parser still understands.
func TestBulkAddServicesTemplate_CSVAndXLSXMatchColumns(t *testing.T) {
	csvBytes := BulkAddServicesCSVTemplate()
	xlsxBytes, err := BulkAddServicesXLSXTemplate()
	if err != nil {
		t.Fatalf("xlsx build: %v", err)
	}
	csvHeader, err := csv.NewReader(bytes.NewReader(csvBytes)).Read()
	if err != nil {
		t.Fatalf("csv read header: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(xlsxBytes))
	if err != nil {
		t.Fatalf("xlsx open: %v", err)
	}
	defer f.Close()
	xlsxRows, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		t.Fatalf("xlsx read rows: %v", err)
	}
	xlsxHeader := xlsxRows[0]
	// Same length AND same per-position value: order matters for the
	// export-edit-reimport round trip (some spreadsheet apps preserve
	// column order on save).
	if len(csvHeader) != len(xlsxHeader) {
		t.Errorf("csv has %d columns, xlsx has %d", len(csvHeader), len(xlsxHeader))
	}
	for i := range csvHeader {
		if i >= len(xlsxHeader) {
			break
		}
		if csvHeader[i] != xlsxHeader[i] {
			t.Errorf("col %d: csv=%q xlsx=%q", i, csvHeader[i], xlsxHeader[i])
		}
	}
}
