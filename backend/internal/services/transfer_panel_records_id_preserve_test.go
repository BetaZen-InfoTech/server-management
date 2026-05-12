package services

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Tests for the v3.1.52 fix that preserves source _id during transfer
// for operator-facing collections (projects, project_services).
// Pre-3.1.52 every transfer regenerated _id, breaking baked GitHub
// webhook URLs (which embed the project ObjectID), the documented
// External API at /api/v1/external/deploy/projects/<id>/services/
// <svc-id>/*, and the per-service "id: 6a02f3..." badges shown in
// the WHM Deploy Software UI.

func TestChooseDestinationID_PreservesValidHexWhenFree(t *testing.T) {
	srcHex := "69f9e50d82545a13b595e29f"
	noCollision := func(primitive.ObjectID) bool { return false }

	got, collided := chooseDestinationID(srcHex, noCollision)
	if collided {
		t.Fatalf("collided=true for free ID — should be false")
	}
	if got.Hex() != srcHex {
		t.Fatalf("got.Hex()=%q want %q (regression: source _id NOT preserved — operator integrations would break post-transfer)",
			got.Hex(), srcHex)
	}
}

func TestChooseDestinationID_FreshOnCollision(t *testing.T) {
	srcHex := "69f9e50d82545a13b595e29f"
	srcOID, _ := primitive.ObjectIDFromHex(srcHex)
	collide := func(oid primitive.ObjectID) bool { return oid == srcOID }

	got, collided := chooseDestinationID(srcHex, collide)
	if !collided {
		t.Fatalf("collided=false but the source ID is taken on dest — should report collision")
	}
	if got == srcOID {
		t.Fatalf("returned the colliding ID — must mint a fresh one")
	}
	if got.IsZero() {
		t.Fatalf("returned zero ObjectID — invalid")
	}
}

func TestChooseDestinationID_FreshOnEmpty(t *testing.T) {
	noCollision := func(primitive.ObjectID) bool { return false }
	got, collided := chooseDestinationID("", noCollision)
	if collided {
		t.Fatalf("empty source ID should not be reported as collision")
	}
	if got.IsZero() {
		t.Fatalf("empty source ID should produce a fresh non-zero ObjectID")
	}
}

func TestChooseDestinationID_FreshOnInvalidHex(t *testing.T) {
	noCollision := func(primitive.ObjectID) bool { return false }
	for _, badHex := range []string{
		"not-a-hex",
		"69f9e50d82545a13b595e29",  // 23 chars
		"69f9e50d82545a13b595e29ff", // 25 chars
		"zzzzzzzzzzzzzzzzzzzzzzzz",  // 24 chars but non-hex
	} {
		got, collided := chooseDestinationID(badHex, noCollision)
		if collided {
			t.Fatalf("invalid hex %q should not report collision", badHex)
		}
		if got.IsZero() {
			t.Fatalf("invalid hex %q should produce a fresh non-zero ObjectID", badHex)
		}
	}
}

// TestChooseDestinationID_RegressionPreservesScreenshotIDs simulates
// the exact case from the user's screenshots — pre-3.1.52 the project
// "Restaurent-Dev-Mode" had _id 69f9e50d82545a13b595e29f on the source
// box but post-transfer ended up with 6a02f34d837b3fdb1765e17f on the
// destination, breaking baked GitHub webhooks. This test pins that
// post-fix the source _id is preserved when no collision exists.
func TestChooseDestinationID_RegressionPreservesScreenshotIDs(t *testing.T) {
	cases := []struct {
		label string
		hex   string
	}{
		{"project Restaurent-Dev-Mode", "69f9e50d82545a13b595e29f"},
		{"service vendor-admin-backend", "69f9e50d82545a13b595e29a"},
		{"service company-admin-backend", "69fab40000000000000000aa"},
		{"service super-admin-backend", "69fab50000000000000000bb"},
	}
	noCollision := func(primitive.ObjectID) bool { return false }
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			got, _ := chooseDestinationID(c.hex, noCollision)
			if got.Hex() != c.hex {
				t.Fatalf("regression — source _id %s NOT preserved across transfer (got %s); operator integrations referencing %s would 404",
					c.hex, got.Hex(), c.hex)
			}
		})
	}
}
