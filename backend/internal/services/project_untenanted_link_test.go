package services

import (
	"context"
	"testing"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// These tests pin the authorization decision for linking a domain to a project
// whose tenant_id is ZERO (un-stamped). The rule: a tenant-scoped caller may
// link + self-heal such a project ONLY if they demonstrably own it. Every path
// here is DB-free by construction (owner match short-circuits before any Mongo
// call; an empty proj.User skips the username lookup) so the ProjectService can
// carry a nil db.

func TestCallerOwnsUntenantedProject_OwnerMatchAllowed(t *testing.T) {
	s := &ProjectService{}
	owner := primitive.NewObjectID()
	proj := &models.Project{OwnerUserID: owner}
	scope := &CallerScope{TenantHex: primitive.NewObjectID().Hex(), UserHex: owner.Hex()}
	if !s.callerOwnsUntenantedProject(context.Background(), proj, scope) {
		t.Fatal("caller who IS the project owner_user_id must be allowed to heal + link")
	}
}

func TestCallerOwnsUntenantedProject_NilScopeDenied(t *testing.T) {
	s := &ProjectService{}
	if s.callerOwnsUntenantedProject(context.Background(), &models.Project{}, nil) {
		t.Fatal("nil scope must fail closed")
	}
}

func TestCallerOwnsUntenantedProject_EmptyTenantDenied(t *testing.T) {
	s := &ProjectService{}
	proj := &models.Project{OwnerUserID: primitive.NewObjectID()}
	if s.callerOwnsUntenantedProject(context.Background(), proj, &CallerScope{TenantHex: ""}) {
		t.Fatal("empty-tenant scope must fail closed")
	}
}

func TestCallerOwnsUntenantedProject_DifferentOwnerNoUserDenied(t *testing.T) {
	s := &ProjectService{}
	// A project owned by someone else, with no linux `user` to resolve — a
	// stranger's/owner-only project. Must stay locked out (no cross-tenant claim).
	proj := &models.Project{OwnerUserID: primitive.NewObjectID()}
	scope := &CallerScope{TenantHex: primitive.NewObjectID().Hex(), UserHex: primitive.NewObjectID().Hex()}
	if s.callerOwnsUntenantedProject(context.Background(), proj, scope) {
		t.Fatal("a different owner with no linux user must fail closed")
	}
}

func TestCallerOwnsUntenantedProject_ZeroOwnerNoUserDenied(t *testing.T) {
	s := &ProjectService{}
	// Neither an owner_user_id nor a user field — nothing proves ownership.
	proj := &models.Project{}
	scope := &CallerScope{TenantHex: primitive.NewObjectID().Hex(), UserHex: primitive.NewObjectID().Hex()}
	if s.callerOwnsUntenantedProject(context.Background(), proj, scope) {
		t.Fatal("an un-owned, user-less project must fail closed")
	}
}
