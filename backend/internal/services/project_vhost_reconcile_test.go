package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// TestSelectStaticFrontendDomains verifies the selection that drives
// ReconcileAllProjectVhosts: only static-frontend primary domains are returned
// (the class a server transfer clobbers into a generic public_html vhost),
// deduped by domain, and backends / empty domains are excluded.
func TestSelectStaticFrontendDomains(t *testing.T) {
	oid := primitive.NewObjectID()
	svcs := []models.ProjectService{
		{ProjectID: oid, Role: "frontend", PrimaryDomain: "menu.example.com", BuildDir: "/home/u/proj/dist"},
		{ProjectID: oid, Role: "backend", PrimaryDomain: "api.example.com", Port: 3000},
		{ProjectID: oid, Role: "static", PrimaryDomain: "docs.example.com", BuildDir: "/home/u/docs/dist"},
		{ProjectID: oid, Role: "frontend", PrimaryDomain: "menu.example.com"}, // duplicate domain → collapsed
		{ProjectID: oid, Role: "frontend", PrimaryDomain: ""},                 // no domain → skipped
	}

	got := selectStaticFrontendDomains(svcs)

	if len(got) != 2 {
		t.Fatalf("want 2 static-frontend domains, got %d: %v", len(got), got)
	}
	if _, ok := got["menu.example.com"]; !ok {
		t.Error("menu.example.com (frontend) should be selected")
	}
	if _, ok := got["docs.example.com"]; !ok {
		t.Error("docs.example.com (static) should be selected")
	}
	if _, ok := got["api.example.com"]; ok {
		t.Error("api.example.com (backend) must NOT be selected — backends keep their proxy vhost")
	}
	// The representative service for the menu domain must carry its build_dir so
	// the rebuilt vhost serves the SPA (not the first, build-dir-less duplicate).
	if bd := got["menu.example.com"].BuildDir; bd != "/home/u/proj/dist" {
		t.Errorf("menu representative build_dir = %q, want the first (built) entry's dir", bd)
	}
}

// TestSelectStaticFrontendDomains_Empty confirms a project with only backends
// (or nothing) yields no rebuild targets — so a reconcile on such a box is a
// no-op rather than touching healthy reverse-proxy vhosts.
func TestSelectStaticFrontendDomains_Empty(t *testing.T) {
	oid := primitive.NewObjectID()
	svcs := []models.ProjectService{
		{ProjectID: oid, Role: "backend", PrimaryDomain: "a.example.com", Port: 8080},
		{ProjectID: oid, Role: "backend", PrimaryDomain: "b.example.com", Port: 8081},
	}
	if got := selectStaticFrontendDomains(svcs); len(got) != 0 {
		t.Fatalf("want 0 targets for a backend-only project, got %d: %v", len(got), got)
	}
	if got := selectStaticFrontendDomains(nil); len(got) != 0 {
		t.Fatalf("want 0 targets for nil services, got %d", len(got))
	}
}

// TestStaticServeMissingIndex locks the v3.1.284 guard: a pure static serve
// (root set, nothing proxied) whose build_dir has no index.html must be flagged
// as missing-build (so the reconcile skips it instead of building a 500-ing
// vhost), while a present index.html or any proxy clears the flag.
func TestStaticServeMissingIndex(t *testing.T) {
	// Case 1: build_dir exists but has no index.html → needs build.
	empty := t.TempDir()
	if !staticServeMissingIndex(empty, false) {
		t.Errorf("empty build_dir (no index.html) should be reported as missing-build")
	}

	// Case 2: build_dir with an index.html → ready to serve.
	built := t.TempDir()
	if err := os.WriteFile(filepath.Join(built, "index.html"), []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if staticServeMissingIndex(built, false) {
		t.Errorf("build_dir WITH index.html must not be flagged as missing-build")
	}

	// Case 3: no root at all → not a static serve, not flagged.
	if staticServeMissingIndex("", false) {
		t.Errorf("empty root must not be flagged (nothing to static-serve)")
	}

	// Case 4: anything proxied → the site works via proxy, so even a missing
	// index must not be flagged (don't block a mixed static+proxy vhost).
	if staticServeMissingIndex(empty, true) {
		t.Errorf("a proxied spec must not be flagged as missing static build")
	}
}
