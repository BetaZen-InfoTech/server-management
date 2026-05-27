package services

import (
	"testing"
)

// TestIsApexDomain_NoShorterFormInPanel locks the simplest happy
// path — a plain "example.com" with no shorter form registered is
// the apex. Regression guard for "the cron stopped refreshing my
// only domain because the apex check went funny".
func TestIsApexDomain_NoShorterFormInPanel(t *testing.T) {
	known := map[string]bool{"example.com": true}
	if !IsApexDomain("example.com", known) {
		t.Error("example.com should be apex when no shorter form is in the panel")
	}
}

// TestIsApexDomain_SubdomainOfPanelManagedApex asserts that
// app.example.com is recognised as a SUBDOMAIN when the panel also
// holds example.com. This is the wasted-RDAP-quota case the cron
// is designed to skip.
func TestIsApexDomain_SubdomainOfPanelManagedApex(t *testing.T) {
	known := map[string]bool{
		"example.com":     true,
		"app.example.com": true,
	}
	if IsApexDomain("app.example.com", known) {
		t.Error("app.example.com should be flagged as subdomain when example.com is in the panel")
	}
}

// TestIsApexDomain_DeepSubdomain asserts the multi-label
// detection — api.app.example.com should still find example.com as
// its panel-registered ancestor (the walk checks EVERY proper
// suffix, not just len-2).
func TestIsApexDomain_DeepSubdomain(t *testing.T) {
	known := map[string]bool{
		"example.com":         true,
		"api.app.example.com": true,
	}
	if IsApexDomain("api.app.example.com", known) {
		t.Error("api.app.example.com should be a subdomain (example.com is registered)")
	}
}

// TestIsApexDomain_CCTldWithoutSplitApex asserts the panel's PSL-
// free design — a domain like "acme.co.uk" where the panel only
// holds acme.co.uk (not co.uk, which would be nonsensical) is
// treated as apex. The RDAP / whois fallback handles the actual
// ccTLD lookup; the panel doesn't need PSL data to do this right.
func TestIsApexDomain_CCTldWithoutSplitApex(t *testing.T) {
	known := map[string]bool{"acme.co.uk": true}
	if !IsApexDomain("acme.co.uk", known) {
		t.Error("acme.co.uk should be apex when no shorter form is in the panel")
	}
}

// TestIsApexDomain_CCTldSubdomain asserts subdomain detection
// works on ccTLD-nested names too — billing.acme.co.uk is a
// subdomain of acme.co.uk just like billing.acme.com is of
// acme.com.
func TestIsApexDomain_CCTldSubdomain(t *testing.T) {
	known := map[string]bool{
		"acme.co.uk":         true,
		"billing.acme.co.uk": true,
	}
	if IsApexDomain("billing.acme.co.uk", known) {
		t.Error("billing.acme.co.uk should be a subdomain (acme.co.uk is registered)")
	}
}

// TestIsApexDomain_SubdomainWhenApexMissing asserts the niche case
// where the operator manages ONLY a subdomain (e.g. they added
// app.example.com directly without ever adding example.com). The
// cron treats it as apex and runs WHOIS on it — the RDAP / whois
// lookup at the protocol layer correctly resolves to the parent
// registration, so this is harmless.
func TestIsApexDomain_SubdomainWhenApexMissing(t *testing.T) {
	known := map[string]bool{"app.example.com": true} // no example.com row
	if !IsApexDomain("app.example.com", known) {
		t.Error("app.example.com should be apex when no shorter form is in the panel (operator manages only the subdomain)")
	}
}

// TestIsApexDomain_CaseInsensitive asserts the panel's case-
// insensitive contract — RDAP returns lower-case names but
// operators sometimes type "Example.COM" in the Add Domain form.
// The cron lower-cases both sides before the set lookup so a
// mixed-case row still gets matched against its lower-case
// ancestor.
func TestIsApexDomain_CaseInsensitive(t *testing.T) {
	known := map[string]bool{"example.com": true}
	if IsApexDomain("App.Example.COM", known) {
		t.Error("case-insensitive lookup should find example.com as the parent of App.Example.COM")
	}
	if !IsApexDomain("EXAMPLE.com", known) {
		t.Error("EXAMPLE.com should be apex (case-insensitive match against itself doesn't count)")
	}
}

// TestIsApexDomain_TrailingDot asserts FQDN-with-trailing-dot
// inputs are normalised. RDAP responses sometimes include the
// root dot; we strip it before the suffix walk so "example.com."
// matches "example.com" in the known set.
func TestIsApexDomain_TrailingDot(t *testing.T) {
	known := map[string]bool{"example.com": true}
	if IsApexDomain("app.example.com.", known) {
		t.Error("trailing-dot input should resolve to subdomain (example.com is parent)")
	}
}

// TestIsApexDomain_RejectsInvalidNames asserts defence-in-depth
// — single-label (no dot) or empty inputs are returned as non-
// apex so the cron skips them gracefully. The Create validator
// already rejects these at the request layer; this is the safety
// net for direct cron callers (e.g. a future test seeding bad
// data).
func TestIsApexDomain_RejectsInvalidNames(t *testing.T) {
	known := map[string]bool{}
	for _, bad := range []string{"", "   ", "localhost", "."} {
		if IsApexDomain(bad, known) {
			t.Errorf("IsApexDomain(%q) should reject invalid name, returned true", bad)
		}
	}
}

// TestExpiryBuckets_LadderShape locks the user-facing bucket set
// — operators on the dashboard's filter pills row see exactly
// these numbers, and a future edit that adds / removes one will
// drift the pill row out of sync with the cron's notification
// cadence. The test guarantees both stay in lockstep.
//
// Descending order is required for the smallest-bucket-wins
// match logic in RunDomainExpirySweep.
func TestExpiryBuckets_LadderShape(t *testing.T) {
	want := []int{60, 45, 30, 15, 7, 5, 4, 3, 2, 1}
	got := ExpiryBuckets()
	if len(got) != len(want) {
		t.Fatalf("bucket count = %d, want %d (set: %v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("bucket[%d] = %d, want %d (full set: %v)", i, got[i], w, got)
		}
	}
	// Defensive: assert strictly descending order so the smallest-
	// matching-bucket loop in RunDomainExpirySweep keeps producing
	// the expected "smallest match wins" behaviour.
	for i := 1; i < len(got); i++ {
		if got[i] >= got[i-1] {
			t.Errorf("buckets must be strictly descending: got[%d]=%d not < got[%d]=%d", i, got[i], i-1, got[i-1])
		}
	}
}

// TestExpiryBuckets_ReturnedSliceIsCopy asserts ExpiryBuckets
// returns a defensive copy — a caller that mutates the result
// (e.g. sorts ascending for a UI render) must NOT corrupt the
// package-private cron state. Without this guarantee a frontend
// handler could accidentally break the next sweep tick.
func TestExpiryBuckets_ReturnedSliceIsCopy(t *testing.T) {
	first := ExpiryBuckets()
	first[0] = 999 // mutate
	second := ExpiryBuckets()
	if second[0] != 60 {
		t.Errorf("ExpiryBuckets() must return a copy; caller mutation leaked through. Got %v", second)
	}
}
