package services

import (
	"fmt"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

// Tests for the v3.1.50 mailbox-dedup fix. Pre-3.1.50 the inline natural-
// key closure queried field `address` but the Mailbox model uses `email`
// (bson:"email", models/email.go:10). Mongo treats `{address: null}` as
// matching docs missing the field, so the second mailbox per collection
// always matched the first inserted (email-keyed) doc and got dropped.
//
// These tests pin the natural-key shape AND simulate the dedup behavior
// against an in-memory "destination" so a regression to the buggy field
// name reproduces the original symptom (only 1 of N mailboxes lands).

// TestMailboxNaturalKey_UsesEmailField guards against the original
// regression: the dedup query MUST be on field `email`. If anyone
// changes it back to `address` (or any field name not present on the
// Mailbox model), this test fails immediately.
func TestMailboxNaturalKey_UsesEmailField(t *testing.T) {
	doc := bson.M{
		"email":    "alice@example.com",
		"domain":   "example.com",
		"password": "{SHA512-CRYPT}$6$...",
		"quota_mb": 256,
	}
	got := mailboxNaturalKey(doc)

	if _, hasAddress := got["address"]; hasAddress {
		t.Fatalf("naturalKey returned `address` field — that's the v3.1.50 bug; got %v", got)
	}
	want := "alice@example.com"
	if v, ok := got["email"].(string); !ok || v != want {
		t.Fatalf("naturalKey[email] = %v, want %q", got["email"], want)
	}
}

// TestMailboxPrepare_LabelsByEmail pins the human-readable label the
// transfer log uses. A regression here would silently revert the log
// labels to `address=""` while the dedup itself stays correct, which
// would make per-mailbox warnings useless.
func TestMailboxPrepare_LabelsByEmail(t *testing.T) {
	s := &TransferService{}
	src := map[string]any{
		"email":    "bob@example.com",
		"domain":   "example.com",
		"password": "x",
	}
	prep := s.mailboxPrepare(nil)
	out, label := prep(src)

	if v, _ := out["email"].(string); v != "bob@example.com" {
		t.Fatalf("normalised doc lost email field: %v", out)
	}
	want := `email="bob@example.com"`
	if label != want {
		t.Fatalf("label = %q, want %q", label, want)
	}
}

// TestMailboxDedup_AllMailboxesLand simulates the actual transfer dedup
// behaviour against an in-memory "destination" that mirrors Mongo's
// {field: null} semantics (a query for {addr: null} matches docs whose
// `addr` field is null OR missing). Pre-3.1.50 the bug caused only 1
// of N mailboxes to land. This test enforces N of N for the fixed code
// AND demonstrates the bug via a parallel buggy run, so a regression
// reproduces the pathology.
func TestMailboxDedup_AllMailboxesLand(t *testing.T) {
	source := []bson.M{}
	for i := 1; i <= 5; i++ {
		source = append(source, bson.M{
			"email":  fmt.Sprintf("mbx%d@example.com", i),
			"domain": "example.com",
		})
	}

	t.Run("fixed naturalKey: all 5 mailboxes land", func(t *testing.T) {
		dest := []bson.M{}
		for _, src := range source {
			key := mailboxNaturalKey(src)
			if findFirst(dest, key) != -1 {
				continue
			}
			dest = append(dest, copyDoc(src))
		}
		if len(dest) != 5 {
			t.Fatalf("expected 5 mailboxes inserted, got %d (regression — dedup is dropping rows)", len(dest))
		}
		for i, src := range source {
			if dest[i]["email"] != src["email"] {
				t.Fatalf("dest[%d].email = %v, want %v", i, dest[i]["email"], src["email"])
			}
		}
	})

	t.Run("BUG demo: pre-3.1.50 buggy naturalKey drops 4 of 5", func(t *testing.T) {
		// Recreate the exact pre-3.1.50 buggy closure inline so the test
		// pins the symptom of any future regression.
		buggyKey := func(doc bson.M) bson.M {
			return bson.M{"address": doc["address"]}
		}
		dest := []bson.M{}
		for _, src := range source {
			key := buggyKey(src)
			// Mongo {field: null} matches docs where field is null OR
			// missing. Simulate that here.
			if findFirstNullSafe(dest, key) != -1 {
				continue
			}
			dest = append(dest, copyDoc(src))
		}
		if len(dest) != 1 {
			t.Fatalf("buggy naturalKey was expected to drop 4 of 5 mailboxes, got %d inserted — bug shape no longer matches v3.1.50 audit", len(dest))
		}
	})
}

// findFirst returns the index of the first dest doc whose values match
// every key in `query`. Field-equality only — no null-coalescing.
func findFirst(dest []bson.M, query bson.M) int {
	for i, d := range dest {
		match := true
		for k, v := range query {
			if d[k] != v {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// findFirstNullSafe mirrors MongoDB's {field: null} semantics: a value
// of null in the query matches docs where the field is null OR missing.
// This is what makes the buggy `{address: null}` query match every
// existing destination row that uses `email` instead.
func findFirstNullSafe(dest []bson.M, query bson.M) int {
	for i, d := range dest {
		match := true
		for k, v := range query {
			if v == nil {
				if dv, ok := d[k]; ok && dv != nil {
					match = false
					break
				}
			} else if d[k] != v {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func copyDoc(src bson.M) bson.M {
	out := bson.M{}
	for k, v := range src {
		out[k] = v
	}
	return out
}
