package services

import (
	"strings"
	"testing"
)

// TestExportableMailboxRow pins the column order the export writes —
// any drift here will silently break re-upload via the same template
// (column-by-column round-trip is a soft contract operators rely on).
func TestExportableMailboxRow_NoPassword(t *testing.T) {
	m := ExportableMailbox{
		Email:            "alice@example.com",
		Password:         "should-not-leak",
		Domain:           "example.com",
		Username:         "alice",
		QuotaMB:          1024,
		UsedMB:           42.5,
		SendLimitPerHour: 100,
		CreatedAt:        "2026-05-01T00:00:00Z",
	}
	row := m.row(false)
	if len(row) != 7 {
		t.Fatalf("expected 7 columns without password, got %d: %v", len(row), row)
	}
	for _, cell := range row {
		if cell == "should-not-leak" {
			t.Errorf("password leaked into row when includePassword=false: %v", row)
		}
	}
	if row[0] != "alice@example.com" {
		t.Errorf("column 0 should be email, got %q", row[0])
	}
	if row[2] != "alice" {
		t.Errorf("column 2 should be username, got %q", row[2])
	}
}

func TestExportableMailboxRow_WithPassword(t *testing.T) {
	m := ExportableMailbox{
		Email:    "alice@example.com",
		Password: "P@ssw0rd!",
		Domain:   "example.com",
		Username: "alice",
	}
	row := m.row(true)
	if len(row) != 8 {
		t.Fatalf("expected 8 columns with password, got %d", len(row))
	}
	if row[7] != "P@ssw0rd!" {
		t.Errorf("password should be the last column, got %v", row)
	}
}

// TestMailboxExportHeader pins the header text — operators may grep
// for these strings, and a typo would break the upload-after-export
// round-trip silently.
func TestMailboxExportHeader(t *testing.T) {
	h := mailboxExportHeader(false)
	want := []string{"email", "domain", "username", "quota_mb", "used_mb", "send_limit_per_hour", "created_at"}
	if len(h) != len(want) {
		t.Fatalf("header length: got %d want %d", len(h), len(want))
	}
	for i, w := range want {
		if h[i] != w {
			t.Errorf("header[%d]: got %q want %q", i, h[i], w)
		}
	}

	h2 := mailboxExportHeader(true)
	if h2[len(h2)-1] != "password" {
		t.Errorf("password column should be last when includePassword=true; got %v", h2)
	}
}

// TestResolveMailboxHeader covers the case-insensitive synonym table
// the bulk uploader uses to map operator-supplied column names back
// to canonical keys.
func TestResolveMailboxHeader(t *testing.T) {
	cases := map[string]string{
		"email":               "email",
		"Email":               "email",
		"E-Mail":              "email",
		"address":             "email",
		"Mailbox":             "email",
		"domain":              "domain",
		"Host":                "domain",
		"password":            "password",
		"Pass":                "password",
		"PWD":                 "password",
		"quota":               "quota_mb",
		"Quota_MB":            "quota_mb",
		"Size":                "quota_mb",
		"Send Limit":          "send_limit_per_hour",
		"Send_Limit_Per_Hour": "send_limit_per_hour",
		"hourlysend":          "send_limit_per_hour",
		// Unknown headers pass through normalised so operator can see
		// what the parser saw.
		"madeup column": "madeupcolumn",
	}
	for in, want := range cases {
		if got := resolveMailboxHeader(in); got != want {
			t.Errorf("resolveMailboxHeader(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGeneratedMailboxPassword pins the "ambiguous-character-free"
// alphabet rule so future refactors can't silently break operators
// reading passwords aloud over the phone (the documented use case
// for blank-password rows).
func TestGeneratedMailboxPassword(t *testing.T) {
	p := generatedMailboxPassword()
	if len(p) != 16 {
		t.Errorf("generated password should be 16 chars, got %d (%q)", len(p), p)
	}
	for _, banned := range []rune{'0', 'O', 'I', 'l', '1'} {
		if strings.ContainsRune(p, banned) {
			t.Errorf("generated password contains ambiguous char %q: %q", banned, p)
		}
	}
}

// TestBulkMailboxOTPKindsExclusive locks in the discriminator values
// the OTP collection uses. Adding a third value would require updates
// in 3 places (consts, validateBulkMailboxOTP, the email body branch);
// this test is the cross-link.
func TestBulkMailboxOTPKindsExclusive(t *testing.T) {
	if BulkMailboxOTPKindDelete == BulkMailboxOTPKindExport {
		t.Fatal("delete + export OTP kinds must be distinct")
	}
	if BulkMailboxOTPKindDelete == "" || BulkMailboxOTPKindExport == "" {
		t.Fatal("OTP kind constants must not be empty (validateBulkMailboxOTP would skip the kind check)")
	}
}

// TestBulkMailboxOTPMaxAttempts pins the brute-force budget. Five is
// generous for typos but hostile for a brute (10^6 keyspace ÷ 5 ≈
// 1-in-200k chance per OTP; combined with the 10-minute TTL that's
// solidly below the risk threshold for an admin-email compromise).
func TestBulkMailboxOTPMaxAttempts(t *testing.T) {
	if BulkMailboxOTPMaxAttempts != 5 {
		t.Errorf("BulkMailboxOTPMaxAttempts changed to %d — review brute-force surface", BulkMailboxOTPMaxAttempts)
	}
}
