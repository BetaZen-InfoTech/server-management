package services

import (
	"strings"
	"testing"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
)

// TestAssertProjectDomainOwnership_HappyPath asserts that a request
// whose primary + every alias belong to the project's linked vendor
// passes the guard. Also verifies req.User gets pinned to proj.User
// even when the caller left it blank — downstream install_dir /
// systemd unit / .env paths all root at /home/<proj.User>/, so the
// pin keeps that invariant honest.
func TestAssertProjectDomainOwnership_HappyPath(t *testing.T) {
	proj := &models.Project{User: "vendor1"}
	req := &models.AddServiceRequest{
		PrimaryDomain: "site.example",
		AliasDomains:  []string{"www.site.example", "cname.site.example"},
		// caller left User blank — guard should pin it to proj.User
	}
	owners := map[string]string{
		"site.example":       "vendor1",
		"www.site.example":   "vendor1",
		"cname.site.example": "vendor1",
	}
	err := assertProjectDomainOwnership(proj, req, func(d string) string { return owners[d] })
	if err != nil {
		t.Fatalf("expected nil error on happy path, got %v", err)
	}
	if req.User != "vendor1" {
		t.Errorf("req.User = %q, want %q (guard must pin to proj.User on success)", req.User, "vendor1")
	}
}

// TestAssertProjectDomainOwnership_PrimaryUnregistered asserts the
// "domain not in the panel" failure mode. Pre-3.1.58 a doctored CSV
// could attach a service to a hostname that doesn't exist as a
// Domain row — the install would silently create files under a
// synthetic sp-<slug> user with no SSL path and no DNS record.
// Now we refuse loudly with a message pointing the operator at the
// Domains page.
func TestAssertProjectDomainOwnership_PrimaryUnregistered(t *testing.T) {
	proj := &models.Project{User: "vendor1"}
	req := &models.AddServiceRequest{PrimaryDomain: "stranger.example"}
	err := assertProjectDomainOwnership(proj, req, func(string) string { return "" })
	if err == nil {
		t.Fatal("expected error for unregistered primary_domain")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("error should mention 'not registered'; got: %v", err)
	}
}

// TestAssertProjectDomainOwnership_PrimaryCrossTenant asserts the
// critical bug we're fixing — primary_domain belongs to a different
// vendor than the project. This is the doctored-CSV attack vector:
// vendor A's WHM admin uploads a CSV pointing services at vendor B's
// domains. Pre-3.1.58 the panel accepted the upload, created files
// under /home/<vendor-b>/, broke SSL on vendor B's certbot account
// rebuilt the nginx vhost under the wrong tenant. Now we refuse with
// a clear message naming both vendors so the operator can move the
// domain or pick a different one.
func TestAssertProjectDomainOwnership_PrimaryCrossTenant(t *testing.T) {
	proj := &models.Project{User: "vendor-a"}
	req := &models.AddServiceRequest{PrimaryDomain: "site.example"}
	err := assertProjectDomainOwnership(proj, req, func(d string) string {
		if d == "site.example" {
			return "vendor-b"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected error when primary_domain belongs to a different vendor")
	}
	if !strings.Contains(err.Error(), "vendor-a") || !strings.Contains(err.Error(), "vendor-b") {
		t.Errorf("error should name BOTH vendors (project's + actual owner) so the operator knows what to fix; got: %v", err)
	}
}

// TestAssertProjectDomainOwnership_AliasCrossTenant asserts that the
// alias_domains list is checked just as strictly as primary_domain.
// An attacker who knew about the primary check could otherwise smuggle
// a cross-tenant link in via an alias (the alias domain gets added to
// the nginx vhost's server_name and the Let's Encrypt SAN list — same
// blast radius as the primary).
func TestAssertProjectDomainOwnership_AliasCrossTenant(t *testing.T) {
	proj := &models.Project{User: "vendor-a"}
	req := &models.AddServiceRequest{
		PrimaryDomain: "ok.example",
		AliasDomains:  []string{"www.ok.example", "smuggled.example"},
	}
	owners := map[string]string{
		"ok.example":       "vendor-a",
		"www.ok.example":   "vendor-a",
		"smuggled.example": "vendor-b",
	}
	err := assertProjectDomainOwnership(proj, req, func(d string) string { return owners[d] })
	if err == nil {
		t.Fatal("expected error when an alias belongs to a different vendor")
	}
	if !strings.Contains(err.Error(), "smuggled.example") {
		t.Errorf("error should name the offending alias; got: %v", err)
	}
}

// TestAssertProjectDomainOwnership_AliasUnregistered asserts the
// alias-not-in-the-panel failure mode. We want the same loud refusal
// as the primary case — otherwise the operator's bulk row silently
// drops the alias on the floor at vhost-build time and they end up
// with a service whose stated alias never serves traffic.
func TestAssertProjectDomainOwnership_AliasUnregistered(t *testing.T) {
	proj := &models.Project{User: "vendor1"}
	req := &models.AddServiceRequest{
		PrimaryDomain: "site.example",
		AliasDomains:  []string{"never-added.example"},
	}
	err := assertProjectDomainOwnership(proj, req, func(d string) string {
		if d == "site.example" {
			return "vendor1"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected error when an alias isn't registered in the panel")
	}
	if !strings.Contains(err.Error(), "never-added.example") || !strings.Contains(err.Error(), "not registered") {
		t.Errorf("error should name the alias and 'not registered'; got: %v", err)
	}
}

// TestAssertProjectDomainOwnership_UserOverrideRefused asserts the
// caller can't supply a req.User that disagrees with proj.User. A
// service split between /home/<vendor-a>/ (proj rooted there) and
// /home/<vendor-b>/ (req.User overriding) would put the install_dir
// under one and the systemd ExecStart user under the other — neither
// can read the other's files, so the build dies silently. Better to
// refuse up front than to ship a broken service.
func TestAssertProjectDomainOwnership_UserOverrideRefused(t *testing.T) {
	proj := &models.Project{User: "vendor-a"}
	req := &models.AddServiceRequest{
		PrimaryDomain: "site.example",
		User:          "vendor-b", // wrong on purpose
	}
	err := assertProjectDomainOwnership(proj, req, func(d string) string {
		if d == "site.example" {
			return "vendor-a"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected error when req.User != proj.User")
	}
	if !strings.Contains(err.Error(), "vendor-a") || !strings.Contains(err.Error(), "vendor-b") {
		t.Errorf("error should name both the supplied and project vendor; got: %v", err)
	}
}

// TestAssertProjectDomainOwnership_LegacyProjectSkips asserts the
// legacy-project escape hatch — when proj.User is empty (pre-3.1.27
// hoist projects that never got a linked vendor) the guard is a
// no-op. The AddService caller then falls back to its existing
// lookup-or-synth-user flow. Without this exit a legacy project's
// next AddService would fail with "primary_domain is required"
// even though the request was perfectly well-formed.
func TestAssertProjectDomainOwnership_LegacyProjectSkips(t *testing.T) {
	proj := &models.Project{User: ""}
	req := &models.AddServiceRequest{
		PrimaryDomain: "anything.example",
		User:          "any-old-user",
	}
	called := false
	err := assertProjectDomainOwnership(proj, req, func(string) string {
		called = true
		return ""
	})
	if err != nil {
		t.Fatalf("legacy project (proj.User empty) should skip the guard; got error %v", err)
	}
	if called {
		t.Error("legacy escape hatch should not even call the owner lookup")
	}
	// User unchanged — the legacy fallback inside AddService is
	// responsible for setting it.
	if req.User != "any-old-user" {
		t.Errorf("req.User mangled: %q", req.User)
	}
}

// TestAssertProjectDomainOwnership_AliasCaseInsensitive asserts that
// a mixed-case alias in the request (operator typed "WWW.Site.example"
// in Excel — title-case auto-correct does this) is lowercased before
// the ownership lookup. Domain rows are stored lowercased, so a raw
// "WWW.Site.example" lookup would miss and surface a false "alias not
// registered" error.
func TestAssertProjectDomainOwnership_AliasCaseInsensitive(t *testing.T) {
	proj := &models.Project{User: "vendor1"}
	req := &models.AddServiceRequest{
		PrimaryDomain: "Site.Example",
		AliasDomains:  []string{"WWW.Site.Example"},
	}
	owners := map[string]string{
		"site.example":     "vendor1",
		"www.site.example": "vendor1",
	}
	err := assertProjectDomainOwnership(proj, req, func(d string) string { return owners[d] })
	if err != nil {
		t.Fatalf("mixed-case domains should be lowercased before lookup; got %v", err)
	}
}

// TestAssertProjectDomainOwnership_BlankAliasSkipped asserts that a
// trailing semicolon in the bulk-upload alias_domains cell (an
// operator typing "a.example;b.example;" with a stray trailing
// separator) doesn't surface as "alias \"\" is not registered" —
// blank entries should be silently skipped, matching the
// parseSemicolonList contract.
func TestAssertProjectDomainOwnership_BlankAliasSkipped(t *testing.T) {
	proj := &models.Project{User: "vendor1"}
	req := &models.AddServiceRequest{
		PrimaryDomain: "site.example",
		AliasDomains:  []string{"www.site.example", "", "   "},
	}
	owners := map[string]string{
		"site.example":     "vendor1",
		"www.site.example": "vendor1",
	}
	err := assertProjectDomainOwnership(proj, req, func(d string) string { return owners[d] })
	if err != nil {
		t.Fatalf("blank alias entries should be skipped; got %v", err)
	}
}
