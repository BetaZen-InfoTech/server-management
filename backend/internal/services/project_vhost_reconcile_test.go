package services

import (
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
