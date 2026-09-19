package services

import (
	"reflect"
	"testing"
)

func TestNormalizeNameservers(t *testing.T) {
	// Cleans case/whitespace/trailing-dot + de-dupes, keeping order.
	got, err := normalizeNameservers([]string{" DNS1.Example.com. ", "dns2.example.com", "dns1.example.com"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if want := []string{"dns1.example.com", "dns2.example.com"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// Fewer than 2 → error.
	if _, err := normalizeNameservers([]string{"dns1.example.com"}); err == nil {
		t.Fatalf("expected error for a single nameserver")
	}
	// After de-dupe fewer than 2 → error.
	if _, err := normalizeNameservers([]string{"ns.x.com", "ns.x.com."}); err == nil {
		t.Fatalf("expected error when dedupe drops below 2")
	}
	// More than 8 → error.
	many := []string{"a.x.com", "b.x.com", "c.x.com", "d.x.com", "e.x.com", "f.x.com", "g.x.com", "h.x.com", "i.x.com"}
	if _, err := normalizeNameservers(many); err == nil {
		t.Fatalf("expected error for 9 nameservers (max 8)")
	}
	// Exactly 8 is allowed.
	if _, err := normalizeNameservers(many[:8]); err != nil {
		t.Fatalf("8 nameservers must be allowed, got %v", err)
	}
	// Not an FQDN → error.
	if _, err := normalizeNameservers([]string{"dns1", "dns2.example.com"}); err == nil {
		t.Fatalf("expected error for a non-FQDN nameserver")
	}
	// Injection-ish characters rejected by IsSafeDNSName.
	if _, err := normalizeNameservers([]string{"dns1.example.com;rm -rf", "dns2.example.com"}); err == nil {
		t.Fatalf("expected error for unsafe characters")
	}
}

func TestToCleanNameservers(t *testing.T) {
	// Simulates the bson.A shape Mongo returns for a stored array.
	in := []interface{}{"DNS1.Example.com.", " dns2.example.com ", "", 42}
	got := toCleanNameservers(in)
	if want := []string{"dns1.example.com", "dns2.example.com"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if toCleanNameservers(nil) != nil {
		t.Fatalf("nil value must yield nil")
	}
}
