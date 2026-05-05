package services

import (
	"testing"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
)

// TestReverseLabelKey locks in the comparison-key shape the
// hierarchical sort relies on. Apex domains reverse into their
// suffix-prefixed form (`com.example`); subdomains add labels
// after the apex's reversed key, which is why a regular string
// sort over reverse-keys naturally clusters by zone with apex first.
func TestReverseLabelKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"example.com", "com.example"},
		{"www.example.com", "com.example.www"},
		{"app.example.com", "com.example.app"},
		{"api.abc.users.example.com", "com.example.users.abc.api"},
		// trailing dot / case / whitespace tolerance
		{"Example.COM.", "com.example"},
		{"  example.com  ", "com.example"},
		// empty input → empty key
		{"", ""},
		{"   ", ""},
		// single label (degenerate — not a real domain, but parser shouldn't blow up)
		{"localhost", "localhost"},
	}
	for _, c := range cases {
		if got := reverseLabelKey(c.in); got != c.want {
			t.Errorf("reverseLabelKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSortDomainsHierarchical_ApexBeforeChildren is the headline
// regression guard for the user-reported behaviour: "at first, main
// domain then sub-domain". Mixed apex+subdomain input must come out
// with each apex preceding its own subdomains, and the multi-level
// `api.abc.users.example.com` clustering under `users.example.com`.
func TestSortDomainsHierarchical_ApexBeforeChildren(t *testing.T) {
	in := []models.Domain{
		{Domain: "shop.another.com"},
		{Domain: "api.example.com"},
		{Domain: "another.com"},
		{Domain: "example.com"},
		{Domain: "app.example.com"},
		{Domain: "api.abc.users.example.com"},
		{Domain: "users.example.com"},
	}
	want := []string{
		"another.com",
		"shop.another.com",
		"example.com",
		"api.example.com",
		"app.example.com",
		"users.example.com",
		"api.abc.users.example.com",
	}
	SortDomainsHierarchical(in)
	for i, d := range in {
		if d.Domain != want[i] {
			t.Errorf("position %d: got %q, want %q", i, d.Domain, want[i])
		}
	}
}

// TestSortDomainsHierarchical_StableForDuplicates asserts the sort
// is stable when two rows hash to the same reverse-label key.
// shouldn't happen in production (domain is uniquely indexed) but
// we want the API contract to be deterministic regardless.
func TestSortDomainsHierarchical_StableForDuplicates(t *testing.T) {
	in := []models.Domain{
		{Domain: "example.com", User: "a"},
		{Domain: "example.com", User: "b"},
		{Domain: "example.com", User: "c"},
	}
	SortDomainsHierarchical(in)
	for i, want := range []string{"a", "b", "c"} {
		if in[i].User != want {
			t.Errorf("stability broken at %d: got %q, want %q", i, in[i].User, want)
		}
	}
}

// TestSortExportableDomainsHierarchical mirrors the Domain test on
// the export-shaped slice so the export endpoint doesn't drift away
// from the list endpoint.
func TestSortExportableDomainsHierarchical(t *testing.T) {
	in := []ExportableDomain{
		{Domain: "z.example.com"},
		{Domain: "example.com"},
		{Domain: "a.example.com"},
		{Domain: "another.com"},
	}
	want := []string{"another.com", "example.com", "a.example.com", "z.example.com"}
	SortExportableDomainsHierarchical(in)
	for i, d := range in {
		if d.Domain != want[i] {
			t.Errorf("position %d: got %q, want %q", i, d.Domain, want[i])
		}
	}
}

// TestDomainLessHierarchical_TLDClustering asserts that domains
// across different TLDs cluster correctly — `.com` rows together,
// `.in` rows together — instead of interleaved by leading label.
// This is what "apex first" buys at the multi-vendor scale: a vendor
// with example.com and example.in sees both apexes near each other
// in their list, rather than scattered across the alphabet.
func TestDomainLessHierarchical_TLDClustering(t *testing.T) {
	in := []models.Domain{
		{Domain: "alpha.in"},
		{Domain: "beta.com"},
		{Domain: "alpha.com"},
		{Domain: "beta.in"},
	}
	want := []string{"alpha.com", "beta.com", "alpha.in", "beta.in"}
	SortDomainsHierarchical(in)
	for i, d := range in {
		if d.Domain != want[i] {
			t.Errorf("position %d: got %q, want %q", i, d.Domain, want[i])
		}
	}
}
