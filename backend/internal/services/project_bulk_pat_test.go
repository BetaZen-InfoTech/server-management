package services

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestParsePATProjectIDs(t *testing.T) {
	v1 := primitive.NewObjectID().Hex()
	v2 := primitive.NewObjectID().Hex()
	got := parsePATProjectIDs([]string{v1, "  " + v2 + "  ", "not-a-hex", "", "123"})
	if len(got) != 2 {
		t.Fatalf("want 2 valid oids (surrounding whitespace trimmed, junk dropped), got %d", len(got))
	}
	if got[0].Hex() != v1 || got[1].Hex() != v2 {
		t.Errorf("parsed oids out of order/incorrect: %v", got)
	}
}

// TestBulkRotatePAT_Guards covers the two guard branches that return BEFORE any
// encryption or Mongo access, so they run without a live DB.
func TestBulkRotatePAT_Guards(t *testing.T) {
	s := &ProjectService{} // guards return before encKey/db are touched
	ctx := context.Background()
	oid := primitive.NewObjectID().Hex()

	if _, err := s.BulkRotatePAT(ctx, []string{oid}, "   "); err == nil {
		t.Error("an empty/whitespace PAT must be rejected")
	}
	if _, err := s.BulkRotatePAT(ctx, []string{"nope", ""}, "ghp_abc123"); err == nil {
		t.Error("a request with no valid project ids must be rejected")
	}
}
