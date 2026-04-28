package services

import "testing"

// TestParentZoneOf_UserReportedBug locks in the fix for the bug a user
// hit creating abc.abc.xyz.qwe.com when an earlier-created subdomain
// abc.xyz.qwe.com lived in `domains` (resource-counter row) but had
// no entry in `dns_zones` (it was a subdomain of qwe.com, no separate
// authority). The pre-fix code queried `domains` and greedy-matched
// abc.xyz.qwe.com as the parent, sliced the label down to "abc", and
// landed an A record in a zone PowerDNS didn't even own.
//
// parentZoneOf is the pure helper now; findParentDomain wires it to
// dns_zones. Test exercises the helper directly with an in-memory
// "zones" set so it runs without Mongo.
func TestParentZoneOf_UserReportedBug(t *testing.T) {
	// Only qwe.com is a real DNS zone. abc.xyz.qwe.com exists in the
	// `domains` collection (panel-tracked subdomain row) but NOT in
	// dns_zones — that's the discriminator that pre-fix logic ignored.
	zones := map[string]bool{
		"qwe.com": true,
	}
	got := parentZoneOf("abc.abc.xyz.qwe.com", func(c string) bool { return zones[c] })
	if got != "qwe.com" {
		t.Fatalf("parent = %q, want qwe.com (the apex). subPart would slice to %q instead of %q",
			got, trimRight("abc.abc.xyz.qwe.com", "."+got), "abc.abc.xyz")
	}
}

// TestParentZoneOf_RobustToStaleSubdomainZones is the post-3.0.31
// guard. Pre-3.0.31 the bug was triggered when dns_zones held a
// stale row for an INTERMEDIATE label (a pre-3.0.24
// GetOrCreateZone leftover) — most-specific-wins routed child
// creates through the orphan and either lost the A record or
// landed it at the wrong relative name. Apex-wins (3.0.31) makes
// the stale row a no-op for the lookup: we step over it and reach
// the real apex.
//
// User's exact reproduction:
//
//	dns_zones = { konsultkaro.com, api.users.konsultkaro.com }
//	            └── apex      └── stale orphan from pre-3.0.24
//	input = dev.api.users.konsultkaro.com
//
// Pre-3.0.31 (most-specific-wins):
//
//	parent = api.users.konsultkaro.com   ← orphan
//	subPart = "dev"                      ← user's reported wrong label
//
// 3.0.31 (apex-wins):
//
//	parent = konsultkaro.com
//	subPart = "dev.api.users"            ← the right relative name
//
// We assert the 3.0.31 behaviour on the user's exact label shape
// to keep this regression locked in.
func TestParentZoneOf_RobustToStaleSubdomainZones(t *testing.T) {
	zones := map[string]bool{
		"konsultkaro.com":              true, // real apex
		"api.users.konsultkaro.com":    true, // stale (no pdns SOA in real life)
	}
	input := "dev.api.users.konsultkaro.com"
	parent := parentZoneOf(input, func(c string) bool { return zones[c] })
	if parent != "konsultkaro.com" {
		t.Fatalf("parent = %q, want konsultkaro.com (apex-wins steps over the stale subdomain row)", parent)
	}
	sub := trimRight(input, "."+parent)
	if sub != "dev.api.users" {
		t.Fatalf("subPart = %q, want dev.api.users (the user's expected name for the A record)", sub)
	}
}

// TestParentZoneOf_ApexWinsOverSubdomainZone codifies the 3.0.31 rule
// flip from most-specific-wins (3.0.24) to apex-wins. Reason for the
// flip: pre-3.0.24 buggy GetOrCreateZone calls left orphan dns_zones
// rows for subdomain "zones" PowerDNS never had — with most-specific-
// wins, those orphans hijacked child creates and routed the A record
// into a non-existent zone (the user's konsultkaro.com bug). Apex-
// wins is robust to that stale state without making the operator
// clean up first; legitimate subdomain delegations are an out-of-
// scope niche the panel UI doesn't drive.
//
// Pre-3.0.31 expectation: parent = corp.example.com (most-specific).
// Post-3.0.31 expectation: parent = example.com (apex).
func TestParentZoneOf_ApexWinsOverSubdomainZone(t *testing.T) {
	zones := map[string]bool{
		"example.com":      true,
		"corp.example.com": true, // could be a delegation OR a stale orphan
	}
	got := parentZoneOf("app.corp.example.com", func(c string) bool { return zones[c] })
	if got != "example.com" {
		t.Fatalf("parent = %q, want example.com (apex-wins, even when corp.example.com is also a zone)", got)
	}
}

// TestParentZoneOf_NoParent — primary apex has no parent zone.
func TestParentZoneOf_NoParent(t *testing.T) {
	zones := map[string]bool{} // empty
	if got := parentZoneOf("example.com", func(c string) bool { return zones[c] }); got != "" {
		t.Fatalf("apex with no zones should return \"\", got %q", got)
	}
	// Even with the apex registered, asking about the apex itself returns "".
	zones["example.com"] = true
	if got := parentZoneOf("example.com", func(c string) bool { return zones[c] }); got != "" {
		t.Fatalf("apex domain should not be its own parent, got %q", got)
	}
}

// TestParentZoneOf_TwoLabelTooShort — a two-label name has no possible
// parent in this scheme (we'd need at least app.example.com).
func TestParentZoneOf_TwoLabelTooShort(t *testing.T) {
	zones := map[string]bool{"com": true}
	if got := parentZoneOf("example.com", func(c string) bool { return zones[c] }); got != "" {
		t.Fatalf("two-label domain should never resolve a parent (would land in TLD), got %q", got)
	}
}

// TestParentZoneOf_TrailingDot — operators occasionally paste FQDN
// strings with the canonical trailing dot. parentZoneOf should
// normalise rather than failing.
func TestParentZoneOf_TrailingDot(t *testing.T) {
	zones := map[string]bool{"qwe.com": true}
	got := parentZoneOf("abc.abc.xyz.qwe.com.", func(c string) bool { return zones[c] })
	if got != "qwe.com" {
		t.Fatalf("trailing-dot FQDN should resolve same parent as bare, got %q", got)
	}
}

// TestParentZoneOf_LookupOrder confirms apex (shortest suffix) is
// queried FIRST in 3.0.31+. Pre-3.0.31 used most-specific-first;
// the flip is the core of the 3.0.31 fix.
func TestParentZoneOf_LookupOrder(t *testing.T) {
	queried := make([]string, 0, 4)
	predicate := func(c string) bool {
		queried = append(queried, c)
		return c == "qwe.com" // only the apex is a zone
	}
	got := parentZoneOf("abc.abc.xyz.qwe.com", predicate)
	if got != "qwe.com" {
		t.Fatalf("got %q, want qwe.com", got)
	}
	// Apex first: qwe.com is the FIRST candidate queried; the
	// predicate returns true on it so iteration stops immediately.
	want := []string{"qwe.com"}
	if len(queried) != len(want) {
		t.Fatalf("queried %d candidates, want %d: %v", len(queried), len(want), queried)
	}
	for i := range want {
		if queried[i] != want[i] {
			t.Fatalf("query[%d] = %q, want %q (shortest-suffix first)", i, queried[i], want[i])
		}
	}
}

// TestParentZoneOf_LookupOrder_FullWalk verifies the iteration when
// none of the early candidates match — we should walk all the way
// from the apex out to the most-specific possible parent.
func TestParentZoneOf_LookupOrder_FullWalk(t *testing.T) {
	queried := make([]string, 0, 4)
	predicate := func(c string) bool {
		queried = append(queried, c)
		return false // no match — exercise the full walk
	}
	parentZoneOf("abc.abc.xyz.qwe.com", predicate)
	want := []string{"qwe.com", "xyz.qwe.com", "abc.xyz.qwe.com"}
	if len(queried) != len(want) {
		t.Fatalf("queried %d candidates, want %d: %v", len(queried), len(want), queried)
	}
	for i := range want {
		if queried[i] != want[i] {
			t.Fatalf("query[%d] = %q, want %q (apex first, then more-specific)", i, queried[i], want[i])
		}
	}
}

// TestParentZoneOf_EmptyAndWhitespace — defensive trims so callers
// don't have to.
func TestParentZoneOf_EmptyAndWhitespace(t *testing.T) {
	zones := map[string]bool{"qwe.com": true}
	for _, in := range []string{"", "   ", ".", "  .  "} {
		if got := parentZoneOf(in, func(c string) bool { return zones[c] }); got != "" {
			t.Fatalf("input %q should return \"\", got %q", in, got)
		}
	}
}

// trimRight is a tiny test-only helper for the fail-message in the
// user-reported-bug test — keeps the assertion message readable
// without dragging strings.TrimSuffix into the test body just for
// formatting.
func trimRight(s, suffix string) string {
	if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)]
	}
	return s
}
