package services

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// TestExtractOID covers all three shapes mongoexport / mongosh / the Go
// driver may surface for an ObjectID column. A regression here would
// silently break the user_id remap on every doc.
func TestExtractOID(t *testing.T) {
	hex := "65ab7d8e9fa12b34c5d6e7f8"
	oid, _ := primitive.ObjectIDFromHex(hex)

	cases := []struct {
		name string
		in   any
		want string
	}{
		{"native ObjectID", oid, hex},
		{"hex string", hex, hex},
		{"ejson wrapper", map[string]any{"$oid": hex}, hex},
		{"nil", nil, ""},
		{"random string", "not-an-oid", ""},
		{"int", 42, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractOID(c.in); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestUnwrapEJSON_ScalarShapes pins the round-trip for the three
// extended-JSON wrappers we hit in real exports: $oid, $date (string),
// $date with $numberLong, and bare $numberLong. If any of these stay as
// a map after unwrap, the mongo driver stores them as embedded objects
// and the destination's typed reads (App, Project, ...) start failing
// in subtle ways.
func TestUnwrapEJSON_ScalarShapes(t *testing.T) {
	hex := "65ab7d8e9fa12b34c5d6e7f8"
	oid, _ := primitive.ObjectIDFromHex(hex)

	t.Run("$oid → ObjectID", func(t *testing.T) {
		got := unwrapEJSON(map[string]any{"$oid": hex})
		if got != oid {
			t.Fatalf("got %#v want %#v", got, oid)
		}
	})

	t.Run("$date string → time.Time", func(t *testing.T) {
		got := unwrapEJSON(map[string]any{"$date": "2026-01-02T03:04:05Z"})
		want, _ := time.Parse(time.RFC3339, "2026-01-02T03:04:05Z")
		if !got.(time.Time).Equal(want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("$date $numberLong → time.Time", func(t *testing.T) {
		// 1 729 425 845 000 ms = 2024-10-20T11:24:05Z
		got := unwrapEJSON(map[string]any{
			"$date": map[string]any{"$numberLong": "1729425845000"},
		})
		if _, ok := got.(time.Time); !ok {
			t.Fatalf("got %T (%v), want time.Time", got, got)
		}
	})

	t.Run("$numberLong → int64", func(t *testing.T) {
		got := unwrapEJSON(map[string]any{"$numberLong": "9223372036854775000"})
		if got.(int64) != int64(9223372036854775000) {
			t.Fatalf("got %v", got)
		}
	})
}

// TestUnwrapEJSON_NestedRecursion is the realistic case: a Project doc
// with embedded Service array, each Service holding its own ObjectID
// and timestamps. Earlier prototypes only walked the top level and
// left nested ObjectIDs as map[$oid] — the destination then refused to
// type-decode the doc back into the Project struct.
func TestUnwrapEJSON_NestedRecursion(t *testing.T) {
	hex := "65ab7d8e9fa12b34c5d6e7f8"
	in := map[string]any{
		"name": "mongo",
		"services": []any{
			map[string]any{
				"id":         map[string]any{"$oid": hex},
				"created_at": map[string]any{"$date": "2026-01-02T03:04:05Z"},
				"name":       "frontend",
			},
		},
	}
	out := unwrapEJSON(in).(bson.M)
	if out["name"].(string) != "mongo" {
		t.Fatalf("name lost")
	}
	svcs := out["services"].([]any)
	if len(svcs) != 1 {
		t.Fatalf("services len %d", len(svcs))
	}
	svc := svcs[0].(bson.M)
	oid, _ := primitive.ObjectIDFromHex(hex)
	if svc["id"] != oid {
		t.Fatalf("nested $oid not unwrapped: %#v", svc["id"])
	}
	if _, ok := svc["created_at"].(time.Time); !ok {
		t.Fatalf("nested $date not unwrapped: %T", svc["created_at"])
	}
}

// TestNormaliseDoc_TranslatesUserAndTenantIDs ensures the per-collection
// inserts point at the destination's vendor row, not the source's. If
// idMap doesn't fire for either field, downstream reads (e.g. the WHM
// /apps endpoint that joins on user_id) come back empty even though the
// rows are physically present.
func TestNormaliseDoc_TranslatesUserAndTenantIDs(t *testing.T) {
	srcUser := "65000000000000000000aaaa"
	srcTen := "65000000000000000000bbbb"
	dstUser := primitive.NewObjectID()
	dstTen := primitive.NewObjectID()
	idMap := map[string]primitive.ObjectID{
		srcUser: dstUser,
		srcTen:  dstTen,
	}
	s := &TransferService{} // none of the fields used in this path

	in := map[string]any{
		"_id":       map[string]any{"$oid": "650000000000000000009999"},
		"name":      "myapp",
		"user":      "vendor1",
		"user_id":   map[string]any{"$oid": srcUser},
		"tenant_id": map[string]any{"$oid": srcTen},
	}
	out := s.normaliseDoc(in, idMap)

	if _, hasID := out["_id"]; hasID {
		t.Fatal("_id should be stripped (will be re-stamped at insert)")
	}
	if out["user_id"] != dstUser {
		t.Fatalf("user_id not translated: %#v", out["user_id"])
	}
	if out["tenant_id"] != dstTen {
		t.Fatalf("tenant_id not translated: %#v", out["tenant_id"])
	}
	if out["name"].(string) != "myapp" || out["user"].(string) != "vendor1" {
		t.Fatal("scalar fields lost")
	}
}

// TestNormaliseDoc_KeepsUntranslatedRefAsObjectID covers the fallback
// case: source has a user_id we don't know about (vendor wasn't selected
// for transfer, or admin row that doesn't exist on dest). Don't blank
// the field — keep the original ObjectID so the destination admin can
// see it for triage.
func TestNormaliseDoc_KeepsUntranslatedRefAsObjectID(t *testing.T) {
	s := &TransferService{}
	hex := "650000000000000000007777"
	in := map[string]any{
		"name":    "orphan",
		"user_id": map[string]any{"$oid": hex},
	}
	out := s.normaliseDoc(in, map[string]primitive.ObjectID{})
	oid, _ := primitive.ObjectIDFromHex(hex)
	if out["user_id"] != oid {
		t.Fatalf("untranslated ref should fall back to original ObjectID, got %#v", out["user_id"])
	}
}
