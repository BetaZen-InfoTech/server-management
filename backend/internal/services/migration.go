package services

import (
	"context"
	"fmt"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// migration.go holds one-shot startup migrations. Functions here are called
// from cmd/server/main.go right after database.Connect and must be idempotent
// — they no-op on every boot after the first.

// BackfillTenantIDs is the multi-tenancy migration. Existing user records
// predate the tenant_id field; this routine sets it for every user that's
// missing one. The rule:
//
//   - vendor_owner / vendor_admin become their own tenant root: tenant_id = self._id
//   - everyone else (developer, support, customer, vendor_staff) gets the
//     first vendor_owner's _id, so the platform owner keeps full visibility
//     and existing customer accounts stay where they were
//
// Safe to run repeatedly. Each call only touches users that don't already
// have tenant_id set, so the second invocation is a no-op.
func BackfillTenantIDs(ctx context.Context, db *mongo.Database) error {
	col := db.Collection(database.ColUsers)

	// Find an anchor vendor_owner. Without one, there's nothing to anchor
	// child users to — that's a fresh install, nothing to backfill.
	var owner models.User
	err := col.FindOne(ctx, bson.M{"role": constants.RoleVendorOwner}).Decode(&owner)
	if err != nil {
		// No owner found — fresh DB, nothing to do.
		return nil
	}

	// 1. Tenant-root accounts (vendor_owner / vendor_admin): tenant_id = self
	//    Update one-by-one because each row needs its own _id as the value.
	cursor, err := col.Find(ctx, bson.M{
		"role":      bson.M{"$in": bson.A{constants.RoleVendorOwner, constants.RoleVendorAdmin}},
		"tenant_id": bson.M{"$exists": false},
	})
	if err != nil {
		return fmt.Errorf("backfill: find tenant roots: %w", err)
	}
	var roots []models.User
	if err := cursor.All(ctx, &roots); err != nil {
		cursor.Close(ctx)
		return fmt.Errorf("backfill: decode tenant roots: %w", err)
	}
	cursor.Close(ctx)
	for _, u := range roots {
		_, _ = col.UpdateByID(ctx, u.ID, bson.M{"$set": bson.M{"tenant_id": u.ID}})
	}

	// 2. Everyone else gets attached to the platform owner. Bulk update —
	//    cheap because the missing-tenant filter is selective on the second
	//    boot.
	_, err = col.UpdateMany(ctx,
		bson.M{
			"role":      bson.M{"$nin": bson.A{constants.RoleVendorOwner, constants.RoleVendorAdmin}},
			"tenant_id": bson.M{"$exists": false},
		},
		bson.M{"$set": bson.M{"tenant_id": owner.ID}},
	)
	if err != nil {
		return fmt.Errorf("backfill: attach legacy users: %w", err)
	}

	return nil
}

// BackfillProjectOwnership re-routes Project.tenant_id and
// Project.owner_user_id to match the project's `user` (the linux
// account it was provisioned under). Fixes the long-standing
// "WHM admin provisioned a project for a vendor — vendor can't see
// it in the User Panel" bug: Create() was using the CALLER's scope
// to stamp tenant_id, so admin-provisioned projects always landed
// under the admin's tenant regardless of which vendor they were
// for. The cpanel project list filters strictly on tenant_id so
// the project stayed invisible.
//
// Idempotent: only updates rows where tenant_id drifts from the
// owning user's actual tenant. The new Provision path
// (assignProjectOwnership) prevents NEW projects from drifting,
// but existing rows on already-deployed installs need this
// one-shot sweep on first boot after the fix lands.
//
// Safe to run on every boot — second pass finds nothing to change.
func BackfillProjectOwnership(ctx context.Context, db *mongo.Database) error {
	projects := db.Collection(database.ColProjects)
	users := db.Collection(database.ColUsers)

	// Pull every project with a usable `user` field. The match has
	// to happen client-side because we need to look each one up by
	// username.
	cursor, err := projects.Find(ctx, bson.M{"user": bson.M{"$exists": true, "$ne": ""}})
	if err != nil {
		return fmt.Errorf("backfill projects: find: %w", err)
	}
	var rows []models.Project
	if err := cursor.All(ctx, &rows); err != nil {
		cursor.Close(ctx)
		return fmt.Errorf("backfill projects: decode: %w", err)
	}
	cursor.Close(ctx)

	for _, p := range rows {
		var u models.User
		if err := users.FindOne(ctx, bson.M{"username": p.User}).Decode(&u); err != nil {
			continue // unknown user (synthetic sp-* fallback or stale row) — skip
		}
		// Resolve correct tenant_id for this user. Same rule as the
		// rest of the panel: tenant root = self, others = parent.
		correctTenant := u.ID
		if !u.TenantID.IsZero() {
			correctTenant = u.TenantID
		}
		if p.TenantID == correctTenant && p.OwnerUserID == u.ID {
			continue // already pointing at the right place
		}
		_, _ = projects.UpdateByID(ctx, p.ID, bson.M{
			"$set": bson.M{
				"tenant_id":     correctTenant,
				"owner_user_id": u.ID,
			},
		})
	}

	// Second pass — heal projects the first pass could NOT (empty/synthetic
	// `user`, so the username lookup missed) but which still carry an
	// owner_user_id. These are the rows that leave tenant_id == zero, and a
	// zero tenant_id makes the project un-linkable by any tenant-scoped
	// caller (External API deploy:link → "project belongs to a different
	// tenant"). Resolve the tenant from the owner_user_id instead so the
	// stamp is correct at boot without waiting for a link-time self-heal.
	zeroCursor, err := projects.Find(ctx, bson.M{
		"tenant_id":     bson.M{"$in": bson.A{nil, primitive.NilObjectID}},
		"owner_user_id": bson.M{"$exists": true, "$ne": primitive.NilObjectID},
	})
	if err != nil {
		return fmt.Errorf("backfill projects: find untenanted: %w", err)
	}
	var zeroRows []models.Project
	if err := zeroCursor.All(ctx, &zeroRows); err != nil {
		zeroCursor.Close(ctx)
		return fmt.Errorf("backfill projects: decode untenanted: %w", err)
	}
	zeroCursor.Close(ctx)
	for _, p := range zeroRows {
		var u models.User
		if err := users.FindOne(ctx, bson.M{"_id": p.OwnerUserID}).Decode(&u); err != nil {
			continue // owner no longer exists — leave for manual reconciliation
		}
		correctTenant := u.ID
		if !u.TenantID.IsZero() {
			correctTenant = u.TenantID
		}
		if correctTenant.IsZero() {
			continue
		}
		_, _ = projects.UpdateByID(ctx, p.ID, bson.M{"$set": bson.M{"tenant_id": correctTenant}})
	}
	return nil
}
