package services

import (
	"context"
	"strings"
	"testing"
)

// TestBulkUpdateTTL_Validation covers the rejection paths in
// DNSService.BulkUpdateTTL that fire BEFORE any Mongo / PowerDNS I/O,
// so we can exercise them without spinning up a database. The success
// path is verified at integration time on the deploy VPS — here we
// lock in the input rules so a regression in the validation block
// can't ship a footgun (e.g. "TTL=0 wipes all caches").

func TestBulkUpdateTTL_RejectsEmptyTypes(t *testing.T) {
	s := NewDNSService(nil)
	_, err := s.BulkUpdateTTL(context.Background(), []string{}, 3600)
	if err == nil {
		t.Fatal("expected validation error for empty types, got nil")
	}
	if !strings.Contains(err.Error(), "type") {
		t.Errorf("error should mention record types; got %q", err.Error())
	}
}

func TestBulkUpdateTTL_RejectsAllWhitespaceTypes(t *testing.T) {
	s := NewDNSService(nil)
	_, err := s.BulkUpdateTTL(context.Background(), []string{"  ", ""}, 3600)
	if err == nil {
		t.Fatal("expected validation error for whitespace-only types, got nil")
	}
}

func TestBulkUpdateTTL_RejectsSOA(t *testing.T) {
	s := NewDNSService(nil)
	_, err := s.BulkUpdateTTL(context.Background(), []string{"A", "SOA"}, 3600)
	if err == nil {
		t.Fatal("expected SOA to be rejected (zone-managed)")
	}
	if !strings.Contains(err.Error(), "SOA") {
		t.Errorf("error should explicitly mention SOA; got %q", err.Error())
	}
}

func TestBulkUpdateTTL_RejectsUnknownType(t *testing.T) {
	s := NewDNSService(nil)
	_, err := s.BulkUpdateTTL(context.Background(), []string{"FAKETYPE"}, 3600)
	if err == nil {
		t.Fatal("expected unknown type to be rejected")
	}
	if !strings.Contains(err.Error(), "FAKETYPE") {
		t.Errorf("error should echo the offending type; got %q", err.Error())
	}
}

func TestBulkUpdateTTL_TTLBounds(t *testing.T) {
	s := NewDNSService(nil)
	cases := []struct {
		name string
		ttl  int
	}{
		{"too low", 29},
		{"zero", 0},
		{"negative", -1},
		{"too high", 604801},
		{"way too high", 31536000}, // 1 year — RFC-allowed but a footgun for hosting
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.BulkUpdateTTL(context.Background(), []string{"A"}, tc.ttl)
			if err == nil {
				t.Fatalf("ttl=%d should have been rejected", tc.ttl)
			}
			if !strings.Contains(err.Error(), "ttl") && !strings.Contains(err.Error(), "TTL") {
				t.Errorf("error should mention TTL; got %q", err.Error())
			}
		})
	}
}

func TestBulkUpdateTTL_TypesAreCaseInsensitive(t *testing.T) {
	// Validation should normalise to upper-case so a UI that sends
	// lowercase types (or mixed) doesn't silently match nothing on
	// the server. We can't run the full sweep here (no Mongo), but we
	// CAN check the validation gate doesn't reject lowercase by
	// matching error-message content: a "type not supported" error
	// would echo the offending type back in the message; a downstream
	// ListZones / agent failure would surface a different message
	// shape. Either is acceptable evidence that validation passed.
	s := NewDNSService(nil)
	_, err := s.BulkUpdateTTL(context.Background(), []string{"a", "Mx"}, 3600)
	if err == nil {
		// We expected SOMETHING to fail downstream (no Mongo); but
		// a clean nil result also implies validation didn't reject.
		return
	}
	if strings.Contains(err.Error(), "not supported") {
		t.Errorf("lowercase types should be normalised to upper-case, but got rejection: %v", err)
	}
}

// TestBulkTTLAllowedTypes_NoSOA pins the safety invariant: SOA must NOT
// be in the whitelist. If a future commit accidentally adds it, this
// test fails immediately rather than letting a bulk sweep clobber the
// negative-cache TTL across an entire fleet of domains.
func TestBulkTTLAllowedTypes_NoSOA(t *testing.T) {
	if bulkTTLAllowedTypes["SOA"] {
		t.Error("SOA must NEVER be in bulkTTLAllowedTypes — its TTL is the negative-cache duration (RFC 2308 §5)")
	}
	// Also confirm the common types ARE present so a refactor doesn't
	// silently shrink the surface.
	for _, must := range []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS"} {
		if !bulkTTLAllowedTypes[must] {
			t.Errorf("expected %q in bulkTTLAllowedTypes, got missing", must)
		}
	}
}

// TestBootstrapTTLFor_Policy locks in the fresh-domain TTL policy so a
// future change to defaults can't silently regress the operator
// experience. Spec: when a brand-new domain enters the system (via
// "Add Domain", /api/v1/whm/domains POST, or vendor signup), the
// records the panel auto-creates use a deliberately short TTL so the
// operator can re-point things in the first hours without being
// trapped by resolver caches.
//
// The agent layer's CreateDNSZone hardcodes the same numeric values
// ("30" / "60") since it can't import the services package — this
// test is the cross-link that catches drift.
func TestBootstrapTTLFor_Policy(t *testing.T) {
	cases := []struct {
		rtype string
		want  int
	}{
		{"A", 30},
		{"AAAA", 30},
		{"CNAME", 60},
		{"NS", 60},
		{"MX", 60},
		{"TXT", 60},
		{"SRV", 60},
		{"CAA", 60},
		// Even truly unknown types fall through to 60 — caller is
		// trusted to pass a real DNS type, but a typo can't make us
		// emit TTL=0 or some other nonsense.
		{"WHATEVER", 60},
		{"", 60},
	}
	for _, tc := range cases {
		t.Run(tc.rtype, func(t *testing.T) {
			got := bootstrapTTLFor(tc.rtype)
			if got != tc.want {
				t.Errorf("bootstrapTTLFor(%q) = %d, want %d", tc.rtype, got, tc.want)
			}
		})
	}

	// Also confirm bootstrap is STRICTLY shorter than defaultTTLFor
	// for the form-default case. If someone ever flips the policy so
	// bootstrap >= default, the "low TTL on bootstrap, lift via
	// Bulk TTL update once settled" workflow falls apart silently.
	if bootstrapTTLFor("A") >= defaultTTLFor("A") {
		t.Errorf("bootstrap A TTL (%d) must be lower than default A TTL (%d)",
			bootstrapTTLFor("A"), defaultTTLFor("A"))
	}
	if bootstrapTTLFor("MX") >= defaultTTLFor("MX") {
		t.Errorf("bootstrap MX TTL (%d) must be lower than default MX TTL (%d)",
			bootstrapTTLFor("MX"), defaultTTLFor("MX"))
	}
}
