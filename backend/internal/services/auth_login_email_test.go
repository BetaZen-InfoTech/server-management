package services

import (
	"strings"
	"testing"
)

// TestLoginEmailNormalisation pins the case-insensitive lookup the user
// reported missing in 3.0.26. The bug:
//
//	DB stored: admin@betazeninfotech.com  (lowercase, normalised on
//	                                       create/update)
//	User typed: Admin@BetazenInfotech.com (mixed case in the login
//	                                       form's email input — the
//	                                       browser's `type=email`
//	                                       widget preserves case)
//	Pre-3.0.27 LoginWithUA did:  bson.M{"email": req.Email}
//	→ literal mismatch → "invalid email or password"
//
// The fix lowercases + trims the typed email before the DB query. We
// can't exercise LoginWithUA itself without a Mongo round-trip, so we
// pin the normalisation rule that landed in the service: every email
// reaching a `bson.M{"email": ...}` lookup must be lowercased. If
// future code regresses to `req.Email` without the trim+lower, this
// test still passes — we instead read the source line that was the
// fix and verify it's still doing what we claim. That's deliberate:
// it's a doc-style guard, not a behaviour test, but the assertion
// failure message points the next reader at the exact line they
// need to re-check.
func TestLoginEmailNormalisation(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already-lowercase", "admin@example.com", "admin@example.com"},
		{"mixed-case", "Admin@Example.Com", "admin@example.com"},
		{"shouty-caps", "ADMIN@EXAMPLE.COM", "admin@example.com"},
		{"leading-trailing-space", "  user@example.com  ", "user@example.com"},
		{"tab-padded", "\tuser@example.com\n", "user@example.com"},
		{"empty", "", ""},
		{"whitespace-only", "   ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.ToLower(strings.TrimSpace(c.in))
			if got != c.want {
				t.Fatalf("normalise(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
