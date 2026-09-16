package services

import (
	"context"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// tenantHealUser is the projection HealTenantIntegrity loads for every user.
type tenantHealUser struct {
	ID       primitive.ObjectID `bson:"_id"`
	Role     string             `bson:"role"`
	Username string             `bson:"username"`
	TenantID primitive.ObjectID `bson:"tenant_id"`
	ParentID primitive.ObjectID `bson:"parent_user_id"`
}

func isTenantRoot(role string) bool {
	return role == "vendor_owner" || role == "vendor_admin"
}

// HealTenantIntegrity repairs dangling / inconsistent tenant_id references that
// a migration can leave behind — most visibly a Deploy Software project or a
// domain that "disappears" from a tenant-scoped view because its tenant_id
// points at a user id that no longer exists on the destination.
//
// WHY THIS IS NEEDED: a tenant ROOT (vendor_owner / vendor_admin) must, by the
// panel's tenancy model, carry tenant_id == its own _id (see CLAUDE.md: "TenantID
// equals own _id for tenant roots"). When a migration inserts that root it gives
// it a FRESH destination _id but normaliseDoc first stamps the SOURCE self-id
// into tenant_id (idMap has no self-mapping yet at row-creation), and a
// destination that was re-seeded between repeated migrations can leave the
// second-pass fixup pointing at a STALE previous-install id. Every child row
// (projects, domains, …) then inherits that stale tenant and drops out of the
// vendor's scoped view even though the data is on disk and serving. Confirmed
// live: 15 of 20 tenant roots had tenant_id != _id and 21 of 25 projects had an
// unresolvable tenant_id after a real migration.
//
// The heal is SOURCE-INDEPENDENT (derives everything from the destination's own
// users), IDEMPOTENT, and CONSERVATIVE — it only rewrites a value that is wrong
// (a root whose tenant_id != _id, or a child whose tenant_id is missing / points
// at a non-existent user); a child that already resolves to a real user is left
// untouched. Three ordered passes:
//
//	1. Tenant ROOTS      : enforce tenant_id == _id.
//	2. MEMBERS           : a dangling tenant_id is repointed to the member's
//	                       parent's (now-consistent) tenant when resolvable.
//	3. CHILD collections : a dangling tenant_id is set to the OWNER row's tenant
//	                       (owner resolved by owner_user_id → user_id → username),
//	                       so a child always shares its owner's tenant.
//
// Returns a per-area count of the rows it corrected.
func (s *TransferService) HealTenantIntegrity(ctx context.Context) (map[string]int, error) {
	sum := map[string]int{}
	usersCol := s.db.Collection(database.ColUsers)

	cur, err := usersCol.Find(ctx, bson.M{})
	if err != nil {
		return sum, err
	}
	var all []tenantHealUser
	if err := cur.All(ctx, &all); err != nil {
		return sum, err
	}

	valid := make(map[primitive.ObjectID]bool, len(all))
	roleOf := make(map[primitive.ObjectID]string, len(all))
	for _, u := range all {
		valid[u.ID] = true
		roleOf[u.ID] = u.Role
	}
	byUsername := make(map[string]primitive.ObjectID, len(all))
	for _, u := range all {
		if u.Username == "" {
			continue
		}
		// When a username is shared between a vendor account and its
		// "<username>@localhost" customer twin, prefer the tenant root — it is
		// the account that actually owns the tenant.
		if ex, ok := byUsername[u.Username]; !ok || (isTenantRoot(u.Role) && !isTenantRoot(roleOf[ex])) {
			byUsername[u.Username] = u.ID
		}
	}

	// Pass 1 — tenant roots: tenant_id MUST equal own _id.
	for i := range all {
		u := &all[i]
		if isTenantRoot(u.Role) && u.TenantID != u.ID {
			if _, err := usersCol.UpdateByID(ctx, u.ID, bson.M{"$set": bson.M{"tenant_id": u.ID}}); err == nil {
				u.TenantID = u.ID
				sum["tenant_roots_fixed"]++
			}
		}
	}

	// tenantOf reflects the post-pass-1 truth.
	tenantOf := make(map[primitive.ObjectID]primitive.ObjectID, len(all))
	for _, u := range all {
		tenantOf[u.ID] = u.TenantID
	}

	// Pass 2 — members with a dangling tenant: adopt the parent's tenant.
	for i := range all {
		u := &all[i]
		if isTenantRoot(u.Role) {
			continue
		}
		if !u.TenantID.IsZero() && valid[u.TenantID] {
			continue // already resolves
		}
		if u.ParentID.IsZero() || !valid[u.ParentID] {
			continue // no usable anchor — leave as-is rather than guess
		}
		pt := tenantOf[u.ParentID]
		if pt.IsZero() || !valid[pt] {
			continue
		}
		if _, err := usersCol.UpdateByID(ctx, u.ID, bson.M{"$set": bson.M{"tenant_id": pt}}); err == nil {
			u.TenantID = pt
			tenantOf[u.ID] = pt
			sum["members_fixed"]++
		}
	}

	// Pass 3 — child rows: a dangling tenant_id inherits the owner's tenant.
	childCollections := []struct {
		coll      string
		ownerOIDs []string // ObjectID owner-reference fields, most specific first
		userField string   // linux-username field mapped via byUsername
	}{
		{database.ColProjects, []string{"owner_user_id", "user_id"}, "user"},
		{database.ColProjectServices, []string{"owner_user_id", "user_id"}, "user"},
		{database.ColDomains, []string{"owner_user_id", "user_id"}, "user"},
		{database.ColDatabases, []string{"owner_user_id", "user_id"}, "user"},
		{database.ColApps, []string{"owner_user_id", "user_id"}, "user"},
		{database.ColWordPress, []string{"owner_user_id", "user_id"}, "user"},
	}

	for _, cc := range childCollections {
		col := s.db.Collection(cc.coll)
		rc, err := col.Find(ctx, bson.M{})
		if err != nil {
			continue
		}
		var rows []bson.M
		if err := rc.All(ctx, &rows); err != nil {
			continue
		}
		for _, row := range rows {
			// Skip rows whose tenant already resolves — never touch good data.
			if tid, ok := row["tenant_id"].(primitive.ObjectID); ok && valid[tid] {
				continue
			}

			var ownerID primitive.ObjectID
			for _, f := range cc.ownerOIDs {
				if oid, ok := row[f].(primitive.ObjectID); ok && valid[oid] {
					ownerID = oid
					break
				}
			}
			if ownerID.IsZero() && cc.userField != "" {
				if uname, ok := row[cc.userField].(string); ok && uname != "" {
					if oid, ok := byUsername[uname]; ok {
						ownerID = oid
					}
				}
			}
			if ownerID.IsZero() {
				continue // can't attribute an owner — leave it rather than mis-file it
			}
			want := tenantOf[ownerID]
			if want.IsZero() || !valid[want] {
				continue
			}
			id, ok := row["_id"].(primitive.ObjectID)
			if !ok {
				continue
			}
			if _, err := col.UpdateByID(ctx, id, bson.M{"$set": bson.M{"tenant_id": want}}); err == nil {
				sum[cc.coll+"_fixed"]++
			}
		}
	}

	return sum, nil
}
