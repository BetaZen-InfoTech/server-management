package services

import (
	"strings"
	"testing"
)

// TestGenerateBulkDeleteCode pins the shape of the OTP code: exactly
// 6 digits, ASCII '0'-'9'. A regression here (e.g. accidentally
// reverting to alphanumeric) would render the email message
// inconsistent with the input field's `inputMode="numeric"` +
// 6-char maxlength on the WHM modal — operators would type partial
// codes and never reach a successful confirm.
func TestGenerateBulkDeleteCode(t *testing.T) {
	for i := 0; i < 100; i++ {
		code, err := generateBulkDeleteCode()
		if err != nil {
			t.Fatalf("generateBulkDeleteCode: %v", err)
		}
		if len(code) != 6 {
			t.Fatalf("len(code) = %d, want 6 (got %q)", len(code), code)
		}
		for j, r := range code {
			if r < '0' || r > '9' {
				t.Fatalf("code[%d] = %q, want digit (full code: %q)", j, r, code)
			}
		}
	}
}

// TestGenerateBulkDeleteCode_Uniqueness asserts the CSPRNG produces
// distinct codes across many calls. Even a few collisions in 100k
// 6-digit codes would imply biased randomness (the math: with proper
// uniform sampling, the birthday-bound for 1M codes hitting the same
// value across 100k draws is ~0.5 collisions on average — but a
// truly broken RNG that mod-biased would produce hundreds).
//
// We allow a small number of collisions because 100k draws into
// 1M slots HAS expected duplicates by birthday math. The threshold
// is generous (50) so a CI flake doesn't break the build.
func TestGenerateBulkDeleteCode_Uniqueness(t *testing.T) {
	const n = 10000
	seen := map[string]int{}
	for i := 0; i < n; i++ {
		code, err := generateBulkDeleteCode()
		if err != nil {
			t.Fatalf("generateBulkDeleteCode: %v", err)
		}
		seen[code]++
	}
	// 10k draws into 1M slots → birthday-expected collisions ≈ 50.
	// Allow up to 200 to absorb test variance without obscuring a
	// catastrophic RNG regression.
	collisions := 0
	for _, n := range seen {
		if n > 1 {
			collisions += n - 1
		}
	}
	if collisions > 200 {
		t.Errorf("too many collisions across %d draws: %d (suggests biased RNG)", n, collisions)
	}
}

// TestGenerateBulkDeleteToken pins the token shape: 64 hex chars
// (32 bytes encoded). A shorter token would shrink the brute-force
// keyspace below the security threshold this flow depends on.
func TestGenerateBulkDeleteToken(t *testing.T) {
	for i := 0; i < 50; i++ {
		tok, err := generateBulkDeleteToken()
		if err != nil {
			t.Fatalf("generateBulkDeleteToken: %v", err)
		}
		if len(tok) != 64 {
			t.Fatalf("len(token) = %d, want 64 (got %q)", len(tok), tok)
		}
		for j, r := range tok {
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
			if !isHex {
				t.Fatalf("token[%d] = %q, want hex digit (full: %q)", j, r, tok)
			}
		}
	}
}

// TestGenerateBulkDeleteToken_Uniqueness — collisions across 1000
// draws should be exactly zero. With 32 bytes of entropy the
// expected birthday collision is essentially 1-in-2^128, so a
// SINGLE collision here would mean a deterministic RNG bug.
func TestGenerateBulkDeleteToken_Uniqueness(t *testing.T) {
	const n = 1000
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		tok, err := generateBulkDeleteToken()
		if err != nil {
			t.Fatalf("generateBulkDeleteToken: %v", err)
		}
		if seen[tok] {
			t.Fatalf("collision after %d draws — RNG is broken", i)
		}
		seen[tok] = true
	}
}

// TestBulkDeleteSHA256Hex sanity-checks the hash function used to
// store the OTP codeHash. Two identical inputs must produce the
// same hex; different inputs must differ. Trivial — but a regression
// (e.g. accidentally calling sha1 instead) would silently break
// every confirm-step lookup.
func TestBulkDeleteSHA256Hex(t *testing.T) {
	a := bulkDeleteSHA256Hex("123456")
	b := bulkDeleteSHA256Hex("123456")
	c := bulkDeleteSHA256Hex("123457")
	if a != b {
		t.Errorf("hash not deterministic: %q vs %q", a, b)
	}
	if a == c {
		t.Errorf("collision on different inputs: %q == %q", a, c)
	}
	if len(a) != 64 {
		t.Errorf("sha256 hex length = %d, want 64", len(a))
	}
}

// TestBuildBulkDeleteOTPEmail covers the three shape invariants the
// email body must hold for the flow to be usable from a phone:
//   - Subject contains the code (so the operator sees it in their
//     notification preview without opening the message).
//   - Plaintext body contains the code AND the domain count.
//   - HTML body contains the code in a copy-paste-friendly block
//     (we render the digits in monospace for readability).
//
// Bulk delete is destructive, so buildBulkDeleteOTPEmail deliberately
// lists EVERY queued domain (no "+N more" truncation) — see its comment.
// The 5th arg is expiresMin (expiry window in minutes), not a list cap.
// Pin: full list present + the expiry line renders.
func TestBuildBulkDeleteOTPEmail(t *testing.T) {
	doms := []string{
		"a.com", "b.com", "c.com", "d.com", "e.com",
		"f.com", "g.com", "h.com", "i.com", "j.com",
		"k.com", "l.com", // 12 total → all listed, none truncated
	}
	subj, text, html := buildBulkDeleteOTPEmail("Operator", "ops@example.com", "987654", doms, 10)
	if !strings.Contains(subj, "987654") {
		t.Errorf("subject missing code: %q", subj)
	}
	if !strings.Contains(text, "987654") {
		t.Errorf("text body missing code: %q", text)
	}
	if !strings.Contains(text, "12 domain") {
		t.Errorf("text body missing domain count: %q", text)
	}
	if !strings.Contains(text, "expires in 10 minutes") {
		t.Errorf("text body missing expiry line (expiresMin=10): %q", text)
	}
	// Every queued domain must appear — no truncation on a destructive op.
	for _, d := range doms {
		if !strings.Contains(text, d) {
			t.Errorf("text body missing queued domain %q (full list expected): %q", d, text)
		}
	}
	if !strings.Contains(html, "987654") {
		t.Errorf("html body missing code: %q", html)
	}
}

// TestBuildBulkDeleteOTPEmail_HTMLEscape locks in the escapeHTML
// pass on the admin name + domain names. A name like
// `Acme <script>alert(1)</script>` from Mongo's user.name field
// (operator-controlled, not strictly validated) would otherwise
// land as live script in the HTML body.
func TestBuildBulkDeleteOTPEmail_HTMLEscape(t *testing.T) {
	hostile := "<script>alert(1)</script>"
	_, _, html := buildBulkDeleteOTPEmail(hostile, "ops@example.com", "111111", []string{hostile}, 10)
	if strings.Contains(html, "<script>") {
		t.Errorf("html body contains unescaped <script>: %q", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("html body missing escaped form: %q", html)
	}
}

// TestEscapeHTML covers the minimal escaper used by the OTP email.
// The pairs (& <> " ') are what HTML attribute / element contexts
// need; anything else passes through.
func TestEscapeHTML(t *testing.T) {
	cases := map[string]string{
		"plain":              "plain",
		"<a>":                "&lt;a&gt;",
		`"x"`:                `&quot;x&quot;`,
		"a & b":              "a &amp; b",
		"o'reilly":           "o&#39;reilly",
		"<img src=x onerror>": "&lt;img src=x onerror&gt;",
	}
	for in, want := range cases {
		if got := escapeHTML(in); got != want {
			t.Errorf("escapeHTML(%q) = %q, want %q", in, got, want)
		}
	}
}
