package services

import (
	"context"
	"fmt"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"go.mongodb.org/mongo-driver/bson"
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
