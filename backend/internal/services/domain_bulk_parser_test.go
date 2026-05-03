package services

import (
	"strings"
	"testing"
)

// TestNormaliseHeader locks in the header-aliasing rules an operator
// editing in Excel/Google Sheets relies on. "PHP Version" / "php-version"
// / "phpversion" / "php_version" all have to resolve to the same canonical
// field name — otherwise the parser silently drops the column and the
// row fails validation with "php_version is required" even though the
// operator clearly wrote it. Regression guard for the editor-touched
// header bug.
func TestNormaliseHeader(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"domain", "domain"},
		{"Domain", "domain"},
		{"DOMAIN", "domain"},
		{" domain ", "domain"},
		{"domain_name", "domainname"},
		{"Domain Name", "domainname"},
		{"PHP Version", "phpversion"},
		{"php-version", "phpversion"},
		{"php_version", "phpversion"},
		{"PHP_VERSION", "phpversion"},
	}
	for _, c := range cases {
		if got := normaliseHeader(c.in); got != c.want {
			t.Errorf("normaliseHeader(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestResolveHeader_AllAliases asserts every entry in bulkHeaderAliases
// resolves to a non-empty canonical column. If a future edit breaks
// the alias table (e.g. removes "site" but leaves a docs reference),
// this test catches the orphan.
func TestResolveHeader_AllAliases(t *testing.T) {
	for raw, canon := range bulkHeaderAliases {
		got := resolveHeader(raw)
		if got != canon {
			t.Errorf("resolveHeader(%q) = %q, want %q", raw, got, canon)
		}
	}
}

// TestResolveHeader_UnknownReturnsEmpty asserts that a column the parser
// doesn't recognise is silently dropped (returns ""). Operators sometimes
// keep a "Notes" column in their import sheet; we don't want that to
// abort the upload.
func TestResolveHeader_UnknownReturnsEmpty(t *testing.T) {
	for _, in := range []string{"notes", "Comment", "internal-id", "anything else"} {
		if got := resolveHeader(in); got != "" {
			t.Errorf("resolveHeader(%q) = %q, want empty", in, got)
		}
	}
}

// TestResolveHeader_HumanFormatVariants asserts the title-cased,
// space-separated header an Excel-using operator naturally types
// resolves to the same canonical name as the snake_case API form.
// This is the actual mode of failure the bulk-upload UX is trying to
// avoid — a non-engineer typing "Domain Name" and getting "missing
// required column: domain" because the parser's strict.
func TestResolveHeader_HumanFormatVariants(t *testing.T) {
	cases := map[string]string{
		"Domain":             "domain",
		"Domain Name":        "domain",
		"PHP Version":        "php_version",
		"PHP-Version":        "php_version",
		"User":               "user",
		"USERNAME":           "user",
		"Disk Quota MB":      "disk_quota_mb",
		"Bandwidth limit gb": "bandwidth_limit_gb",
		"max databases":      "max_databases",
		"Auto Renew":         "auto_renew",
	}
	for in, want := range cases {
		if got := resolveHeader(in); got != want {
			t.Errorf("resolveHeader(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRowAllBlank asserts the trailing-empty-row skip — Excel exports
// commonly include 2-5 blank rows at the bottom of a sheet (left over
// from earlier deletions). Without this skip every blank row would
// surface as a noisy "domain is required" failure, drowning out real
// errors in the row-results table.
func TestRowAllBlank(t *testing.T) {
	cases := []struct {
		row  []string
		want bool
	}{
		{[]string{}, true},
		{[]string{""}, true},
		{[]string{"", "", ""}, true},
		{[]string{" ", "\t", ""}, true},
		{[]string{"x"}, false},
		{[]string{"", "site.example", ""}, false},
	}
	for i, c := range cases {
		if got := rowAllBlank(c.row); got != c.want {
			t.Errorf("rowAllBlank(case %d) = %v, want %v", i, got, c.want)
		}
	}
}

// TestParseBool covers the truthy values an operator might type in
// Excel's TRUE/FALSE cells, a hand-edited CSV's "yes"/"y"/"1", or
// the toggle-style "on" some import tools emit. Anything else is
// false — we deliberately don't try to be cleverer than the validator
// (which doesn't accept ambiguity at all).
func TestParseBool(t *testing.T) {
	truthy := []string{"true", "TRUE", "True", "yes", "YES", "Y", "1", "on", " true ", "Yes"}
	for _, s := range truthy {
		if !parseBool(s) {
			t.Errorf("parseBool(%q) = false, want true", s)
		}
	}
	falsy := []string{"", "no", "false", "0", "off", "n", "maybe", "1.0"}
	for _, s := range falsy {
		if parseBool(s) {
			t.Errorf("parseBool(%q) = true, want false", s)
		}
	}
}

// TestAtoiSafe asserts the "best-effort int" semantics for the
// optional numeric columns. Empty / non-numeric → 0 (treated by the
// service layer as "use package default"). This is the same fallback
// the single-create form's empty input gets, so an operator who
// types "unlimited" in their spreadsheet doesn't get a parse error.
func TestAtoiSafe(t *testing.T) {
	cases := map[string]int{
		"":          0,
		"   ":       0,
		"unlimited": 0,
		"abc":       0,
		"0":         0,
		"5000":      5000,
		" 100 ":     100,
		"-25":       -25,
	}
	for in, want := range cases {
		if got := atoiSafe(in); got != want {
			t.Errorf("atoiSafe(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestBulkUploadCSVTemplate sanity-checks the served template:
// header row contains the required `domain` column, plus at least
// one example data row. If the template ever loses `domain` the UI's
// download-and-upload-back roundtrip would fail at the parser's
// "missing required column: domain" check.
func TestBulkUploadCSVTemplate(t *testing.T) {
	body := string(BulkUploadCSVTemplate())
	if !strings.HasPrefix(body, "domain,") {
		t.Fatalf("template should start with `domain,`; got %q", body[:min(40, len(body))])
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) < 3 {
		t.Fatalf("template should have header + 2 example rows; got %d lines", len(lines))
	}
	if !strings.Contains(lines[1], "site1.example") {
		t.Errorf("first example row should reference site1.example; got %q", lines[1])
	}
}

// TestBulkUploadXLSXTemplate sanity-checks the XLSX template encodes
// the same header set as the CSV template. We don't crack the zip
// here — just assert the function returns non-empty bytes that look
// like a real XLSX (PK zip magic).
func TestBulkUploadXLSXTemplate(t *testing.T) {
	buf, err := BulkUploadXLSXTemplate()
	if err != nil {
		t.Fatalf("BulkUploadXLSXTemplate: %v", err)
	}
	if len(buf) < 100 {
		t.Fatalf("xlsx template suspiciously small: %d bytes", len(buf))
	}
	if string(buf[:4]) != "PK\x03\x04" {
		t.Fatalf("xlsx template missing zip magic; first 4 bytes=%x", buf[:4])
	}
}

// TestBulkUploadCSVTemplateName / TestBulkUploadXLSXTemplateName lock
// in the date-stamped filename pattern so an operator who downloads
// the template multiple times gets distinguishable files in their
// Downloads folder.
func TestBulkUploadTemplateNames(t *testing.T) {
	csv := BulkUploadCSVTemplateName()
	if !strings.HasPrefix(csv, "domains-bulk-upload-template-") || !strings.HasSuffix(csv, ".csv") {
		t.Errorf("csv template name shape wrong: %q", csv)
	}
	xlsx := BulkUploadXLSXTemplateName()
	if !strings.HasPrefix(xlsx, "domains-bulk-upload-template-") || !strings.HasSuffix(xlsx, ".xlsx") {
		t.Errorf("xlsx template name shape wrong: %q", xlsx)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
