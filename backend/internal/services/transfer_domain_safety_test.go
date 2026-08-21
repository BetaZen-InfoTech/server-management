package services

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

// TestIsSafeDNSToken guards the sanitiser that decides which domain strings
// are safe to interpolate into the remote pdns-repoint shell script. Anything
// carrying shell metacharacters must be rejected so a hostile/garbled
// `domains.domain` value can never inject into the payload.
func TestIsSafeDNSToken(t *testing.T) {
	safe := []string{
		"example.com",
		"api.example.com",
		"deep.api.example.co.uk",
		"my-site1.example.com",
		"_dmarc.example.com",
		"xn--80ak6aa92e.com", // punycode
	}
	for _, d := range safe {
		if !isSafeDNSToken(d) {
			t.Errorf("isSafeDNSToken(%q) = false, want true", d)
		}
	}

	unsafe := []string{
		"",
		"example.com; rm -rf /",
		"$(reboot)",
		"example.com `id`",
		"a b.com",
		"example.com\nmalicious.com",
		"exa|mple.com",
		"example.com&whoami",
		"quote'inject.com",
	}
	for _, d := range unsafe {
		if isSafeDNSToken(d) {
			t.Errorf("isSafeDNSToken(%q) = true, want false (must be rejected)", d)
		}
	}
}

// TestNaturalKeyUsable locks in the dedup-key guard that prevents the
// v3.1.50 {address:null} bug shape: a natural key with an empty/nil/missing
// component must be treated as UNUSABLE so insertDeduped does not run a
// FindOne that false-matches an unrelated null-keyed row and silently drops
// a real insert.
func TestNaturalKeyUsable(t *testing.T) {
	usable := []bson.M{
		{"domain": "example.com"},
		{"source": "info@example.com"},
		{"username": "acme"},
		{"db_name": "acme_wp"},
	}
	for _, k := range usable {
		if !naturalKeyUsable(k) {
			t.Errorf("naturalKeyUsable(%v) = false, want true", k)
		}
	}

	unusable := []bson.M{
		{},                       // no key at all
		{"domain": ""},           // empty string component
		{"domain": "   "},        // whitespace-only
		{"domain": nil},          // explicit nil
		{"source": interface{}(nil)},
	}
	for _, k := range unusable {
		if naturalKeyUsable(k) {
			t.Errorf("naturalKeyUsable(%v) = true, want false (must skip dedup + insert)", k)
		}
	}
}
