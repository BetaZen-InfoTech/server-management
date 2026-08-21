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

// TestAsInt covers the numeric coercion used to carry proxy_port across a
// migration — must handle relaxed JSON (float64 from mongoexport --jsonArray),
// plain ints, and the canonical Extended-JSON number wrappers (mongosh path).
func TestAsInt(t *testing.T) {
	cases := []struct {
		in   any
		want int
		ok   bool
	}{
		{float64(4343), 4343, true},                       // mongoexport --jsonArray
		{int(4152), 4152, true},                           // native
		{int32(6008), 6008, true},                         // bson int32
		{int64(4761), 4761, true},                         // bson int64
		{map[string]any{"$numberInt": "4433"}, 4433, true},   // canonical EJSON
		{map[string]any{"$numberLong": "4763"}, 4763, true},  // canonical EJSON
		{"not-a-number", 0, false},                        // string junk
		{nil, 0, false},                                   // missing
		{map[string]any{"other": "x"}, 0, false},          // unrelated map
	}
	for _, c := range cases {
		got, ok := asInt(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("asInt(%v) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
