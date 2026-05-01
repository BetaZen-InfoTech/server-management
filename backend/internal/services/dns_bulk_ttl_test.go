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
