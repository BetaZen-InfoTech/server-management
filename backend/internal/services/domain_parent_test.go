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

// TestParentZoneOf_BugDivergence is the side-by-side proof that the
// 3.0.24 fix actually changes the outcome on the user's exact input.
// We feed the SAME input through TWO predicates: one that queries
// `domains` (the buggy collection) and one that queries `dns_zones`
// (the fix). The two diverge — which is the regression evidence the
// reproducer needed.
//
// Concretely:
//
//	domains   = {qwe.com, abc.xyz.qwe.com}   ← subdomain row exists here
//	dns_zones = {qwe.com}                    ← only the apex has authority
//	input     = abc.abc.xyz.qwe.com
//
//	OLD logic (looked up in `domains`):
//	    parent = abc.xyz.qwe.com   ← WRONG (greedy match on subdomain row)
//	    subPart = "abc"            ← user's reported symptom
//
//	NEW logic (looks up in `dns_zones`):
//	    parent = qwe.com           ← CORRECT
//	    subPart = "abc.abc.xyz"    ← what the operator expected
func TestParentZoneOf_BugDivergence(t *testing.T) {
	const input = "abc.abc.xyz.qwe.com"

	// What the OLD code "saw" — both rows in `domains`.
	domainsCollection := map[string]bool{
		"qwe.com":         true,
		"abc.xyz.qwe.com": true, // panel-subdomain row, no real DNS zone
	}
	// What the NEW code sees — only the apex is in `dns_zones`.
	dnsZonesCollection := map[string]bool{
		"qwe.com": true,
	}

	oldParent := parentZoneOf(input, func(c string) bool { return domainsCollection[c] })
	newParent := parentZoneOf(input, func(c string) bool { return dnsZonesCollection[c] })

	// Compute the subPart each branch would feed into AddRecord.
	oldSub := trimRight(input, "."+oldParent)
	newSub := trimRight(input, "."+newParent)

	t.Logf("OLD code (queried `domains`):   parent=%q  → A record name=%q in zone %q",
		oldParent, oldSub, oldParent)
	t.Logf("NEW code (queries `dns_zones`): parent=%q  → A record name=%q in zone %q",
		newParent, newSub, newParent)

	// 1. The OLD path produces the buggy outcome the user reported.
	if oldParent != "abc.xyz.qwe.com" || oldSub != "abc" {
		t.Fatalf("expected OLD path to reproduce the user's bug "+
			"(parent=abc.xyz.qwe.com, sub=abc); got parent=%q sub=%q",
			oldParent, oldSub)
	}
	// 2. The NEW path produces the correct outcome.
	if newParent != "qwe.com" || newSub != "abc.abc.xyz" {
		t.Fatalf("expected NEW path to fix the bug "+
			"(parent=qwe.com, sub=abc.abc.xyz); got parent=%q sub=%q",
			newParent, newSub)
	}
	// 3. Sanity: the two paths actually diverge — the test isn't
	//    accidentally trivial.
	if oldParent == newParent {
		t.Fatalf("OLD and NEW paths should diverge on this input — fix wouldn't be doing anything")
	}
}

// TestParentZoneOf_DelegatedSubdomainZoneWins protects the legitimate
// case the fix MUST keep working: an operator who explicitly
// delegates corp.example.com (its own SOA + NS in dns_zones) creates
// app.corp.example.com — that record MUST land in corp.example.com,
// not in example.com, otherwise the delegation breaks.
func TestParentZoneOf_DelegatedSubdomainZoneWins(t *testing.T) {
	zones := map[string]bool{
		"example.com":      true,
		"corp.example.com": true, // explicit delegation
	}
	got := parentZoneOf("app.corp.example.com", func(c string) bool { return zones[c] })
	if got != "corp.example.com" {
		t.Fatalf("parent = %q, want corp.example.com (most-specific delegated zone)", got)
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

// TestParentZoneOf_LookupOrder confirms most-specific candidate is
// queried first. This matters for performance (cheap mongo round-trips
// when the apex is the only registered zone) and correctness (delegated
// subdomain zones must beat the apex).
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
	want := []string{"abc.xyz.qwe.com", "xyz.qwe.com", "qwe.com"}
	if len(queried) != len(want) {
		t.Fatalf("queried %d candidates, want %d: %v", len(queried), len(want), queried)
	}
	for i := range want {
		if queried[i] != want[i] {
			t.Fatalf("query[%d] = %q, want %q (most-specific first)", i, queried[i], want[i])
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
