package services

import (
	"reflect"
	"testing"
)

// TestComposeForwarderDestinations pins the exact line shape that
// gets written to /etc/postfix/virtual_alias_maps for a single
// forwarder. Pre-3.1.44 the bug: applyForwarderToPostfix ignored
// keep_copy entirely — the UI toggle + the Mongo column + the
// transfer-recovery hydration all carried the flag but the
// virtual_alias_maps line was always `source → joined dests`.
//
// Operators who toggled "Keep a copy" still saw forwarded mail vanish
// from the source mailbox.
func TestComposeForwarderDestinations(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		dests    []string
		keep     bool
		want     []string
	}{
		// Happy paths — keep off should NEVER add the source.
		{
			name:   "keep=false, single dest",
			source: "sales@example.com",
			dests:  []string{"bob@other.com"},
			keep:   false,
			want:   []string{"bob@other.com"},
		},
		{
			name:   "keep=false, multi dest",
			source: "sales@example.com",
			dests:  []string{"alice@other.com", "bob@other.com"},
			keep:   false,
			want:   []string{"alice@other.com", "bob@other.com"},
		},

		// Happy paths — keep ON appends source so Postfix delivers
		// a copy back to the source mailbox.
		{
			name:   "keep=true, single dest, source not already there",
			source: "sales@example.com",
			dests:  []string{"bob@other.com"},
			keep:   true,
			want:   []string{"bob@other.com", "sales@example.com"},
		},
		{
			name:   "keep=true, source already in dests — must NOT duplicate",
			source: "sales@example.com",
			dests:  []string{"sales@example.com", "bob@other.com"},
			keep:   true,
			want:   []string{"sales@example.com", "bob@other.com"},
		},

		// Normalisation — every entry lowered + trimmed; the dedupe
		// runs on the canonical form so `Sales@…` + `sales@…` collapse.
		{
			name:   "case + whitespace normalisation",
			source: "Sales@Example.com",
			dests:  []string{"  Bob@Other.com ", "ALICE@OTHER.COM", "bob@other.com"},
			keep:   true,
			want:   []string{"bob@other.com", "alice@other.com", "sales@example.com"},
		},

		// Defensive — blank entries from a trailing comma in the
		// CSV cell get dropped silently.
		{
			name:   "blank entries dropped",
			source: "sales@example.com",
			dests:  []string{"bob@other.com", "", "  ", "alice@other.com"},
			keep:   false,
			want:   []string{"bob@other.com", "alice@other.com"},
		},

		// Edge: keep=true with empty source — don't append empty
		// (would write a malformed Postfix line).
		{
			name:   "keep=true with empty source — source not appended",
			source: "",
			dests:  []string{"bob@other.com"},
			keep:   true,
			want:   []string{"bob@other.com"},
		},

		// Edge: empty dest list with keep=false → empty result. Caller
		// (applyForwarderToPostfix) errors out on empty before writing.
		{
			name:   "no dests, keep=false → empty",
			source: "sales@example.com",
			dests:  []string{},
			keep:   false,
			want:   []string{},
		},

		// Edge: empty dest list with keep=true → just the source
		// (operator who only wants a self-loop for some reason; weird
		// but valid Postfix).
		{
			name:   "no dests, keep=true → source only",
			source: "sales@example.com",
			dests:  []string{},
			keep:   true,
			want:   []string{"sales@example.com"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := composeForwarderDestinations(tc.source, tc.dests, tc.keep)
			// reflect.DeepEqual treats `nil` and `[]string{}` as
			// different — coerce both to empty when zero-length so
			// the comparison checks contents, not allocation pattern.
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("composeForwarderDestinations(%q, %v, keep=%v):\n  got  %v\n  want %v",
					tc.source, tc.dests, tc.keep, got, tc.want)
			}
		})
	}
}
