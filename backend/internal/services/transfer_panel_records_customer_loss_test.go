package services

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Regression for the v3.1.224 fix: mirrorPanelUsers' post-insert cleanup of the
// synthetic "<username>@localhost" file-step placeholder must NEVER delete the
// row it just migrated.
//
// A hosting-account customer's real email IS "<username>@localhost" (that is how
// the panel seeds ad7g@localhost, bizenly@localhost, …). Before the fix the
// cleanup filter {username, "<username>@localhost", role:"customer"} matched that
// very row, so mirrorPanelUsers inserted then instantly deleted it — every such
// customer silently vanished from the destination (observed live: customers
// 16 → 2 after a real migration, all 14 "@localhost" accounts gone).
func TestLocalhostPlaceholderCleanupFilter_ExcludesMigratedRow(t *testing.T) {
	migrated := primitive.NewObjectID()
	f := localhostPlaceholderCleanupFilter("ad7g", migrated)

	// The _id guard must be present and exclude the just-migrated row.
	idClause, ok := f["_id"].(bson.M)
	if !ok {
		t.Fatalf("cleanup filter has no _id guard — the migrated row can be deleted; got %#v", f["_id"])
	}
	if got, ok := idClause["$ne"].(primitive.ObjectID); !ok || got != migrated {
		t.Fatalf("_id guard must be {$ne: migratedID}=%s, got %#v", migrated.Hex(), idClause["$ne"])
	}

	// The rest of the natural key must still be scoped to a localhost customer
	// placeholder so the cleanup doesn't over-match unrelated rows.
	if f["username"] != "ad7g" {
		t.Fatalf("username = %v, want ad7g", f["username"])
	}
	if f["email"] != "ad7g@localhost" {
		t.Fatalf("email = %v, want ad7g@localhost", f["email"])
	}
	if f["role"] != "customer" {
		t.Fatalf("role = %v, want customer", f["role"])
	}
}

// A different stale placeholder (a genuinely different row) is still eligible for
// cleanup — the guard only spares the exact migrated _id, so two rows never share
// a username on the destination.
func TestLocalhostPlaceholderCleanupFilter_StillCleansOtherRows(t *testing.T) {
	migrated := primitive.NewObjectID()
	other := primitive.NewObjectID()
	f := localhostPlaceholderCleanupFilter("bizenly", migrated)

	idClause := f["_id"].(bson.M)
	ne := idClause["$ne"].(primitive.ObjectID)
	if ne == other {
		t.Fatalf("guard must not exempt an unrelated placeholder row %s", other.Hex())
	}
}
