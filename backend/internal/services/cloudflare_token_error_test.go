package services

import (
	"errors"
	"strings"
	"testing"
)

// TestAugmentTokenError proves the terse Cloudflare verify failures get
// actionable guidance appended, without ever dropping the original message.
func TestAugmentTokenError(t *testing.T) {
	cases := []struct {
		name    string
		token   string
		err     error
		wantSub string
	}{
		{
			name:    "cfat prefix R2 token",
			token:   "cfat_9VrZkdQJAASIiIYRexample",
			err:     errors.New("cloudflare: Invalid API Token (code 1000)"),
			wantSub: "profile/api-tokens",
		},
		{
			name:    "code 1000 without prefix",
			token:   "someopaqueusertoken",
			err:     errors.New("cloudflare: Invalid API Token (code 1000)"),
			wantSub: "Edit zone",
		},
		{
			name:    "code 6003 malformed",
			token:   "bad token",
			err:     errors.New("cloudflare: Invalid request headers (code 6003)"),
			wantSub: "malformed",
		},
		{
			name:    "unrelated error passes through",
			token:   "sometoken",
			err:     errors.New("dial tcp: i/o timeout"),
			wantSub: "dial tcp: i/o timeout",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := augmentTokenError(tc.token, tc.err)
			if !strings.Contains(got, tc.wantSub) {
				t.Fatalf("augmentTokenError(%q, %v) = %q; want substring %q",
					tc.token, tc.err, got, tc.wantSub)
			}
			if !strings.Contains(got, tc.err.Error()) {
				t.Fatalf("augmented message dropped the original error: %q", got)
			}
		})
	}

	if got := augmentTokenError("cfat_x", nil); got != "" {
		t.Fatalf("augmentTokenError(_, nil) = %q; want empty string", got)
	}
}
