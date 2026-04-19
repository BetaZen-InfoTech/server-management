package services

import (
	"crypto/subtle"
	"strings"
	"testing"
)

// TestNewTokenString_FormatAndEntropy locks in the token shape that the
// frontend, the redeem handler, and the destination's RedeemRemoteToken
// all assume: a "bzn_xfer_" prefix followed by 64 hex chars (32 random
// bytes). 73 chars total — comfortably above the 0/very-short cases the
// destination uses to short-circuit before even querying mongo, and the
// 12-char prefix used as a fast index lookup hint.
func TestNewTokenString_FormatAndEntropy(t *testing.T) {
	seen := make(map[string]bool, 1024)
	for i := 0; i < 1024; i++ {
		tok, err := newTokenString()
		if err != nil {
			t.Fatalf("newTokenString: %v", err)
		}
		if !strings.HasPrefix(tok, "bzn_xfer_") {
			t.Fatalf("token missing bzn_xfer_ prefix: %q", tok)
		}
		if got := len(tok); got != len("bzn_xfer_")+64 {
			t.Fatalf("token wrong length: got %d want %d (token=%q)", got, len("bzn_xfer_")+64, tok)
		}
		if seen[tok] {
			t.Fatalf("collision after %d draws — entropy is broken", i)
		}
		seen[tok] = true
	}
}

// TestHashToken_LengthAndCollision pins the hash format the redeem path
// compares against. SHA-256 in hex is 64 chars; with the "sha256:"
// self-identifying prefix the stored value is 71 chars — and crucially,
// the input length to hashToken is unbounded. This is the regression
// guard for the bcrypt 72-byte overflow that crashed the issue handler
// the first time we shipped this feature.
func TestHashToken_LengthAndCollision(t *testing.T) {
	tok, err := newTokenString()
	if err != nil {
		t.Fatal(err)
	}
	h := hashToken(tok)
	if !strings.HasPrefix(h, "sha256:") {
		t.Fatalf("hash missing sha256: prefix: %q", h)
	}
	if len(h) != len("sha256:")+64 {
		t.Fatalf("hash wrong length: got %d want %d", len(h), len("sha256:")+64)
	}

	// Inputs that would have blown past bcrypt's 72-byte cap must still
	// hash without error here.
	long := strings.Repeat("a", 4096)
	hLong := hashToken(long)
	if len(hLong) != len("sha256:")+64 {
		t.Fatalf("long-input hash wrong length: %d", len(hLong))
	}

	// Determinism: same input → same hash.
	if hashToken(tok) != h {
		t.Fatal("hashToken is non-deterministic")
	}
	// Distinct inputs → distinct hashes (round-trip integrity).
	other, _ := newTokenString()
	if hashToken(other) == h {
		t.Fatal("two random tokens collided — sha256 is broken or rng is")
	}
}

// TestHashToken_ConstantTimeMatch is the exact comparison the Redeem
// handler does. A drift between hashToken's output format and the
// compare path would silently break every redeem, which is the kind of
// bug a unit test should catch before deploy.
func TestHashToken_ConstantTimeMatch(t *testing.T) {
	tok, err := newTokenString()
	if err != nil {
		t.Fatal(err)
	}
	stored := hashToken(tok)
	expected := hashToken(tok)
	if subtle.ConstantTimeCompare([]byte(stored), []byte(expected)) != 1 {
		t.Fatal("hashToken disagrees with itself across calls")
	}
	bad := hashToken(tok + "x")
	if subtle.ConstantTimeCompare([]byte(stored), []byte(bad)) == 1 {
		t.Fatal("constant-time compare matched two different hashes")
	}
}

// TestBuildRedeemCandidateURLs covers the panel-URL fallback chain the
// destination uses when the operator only typed an IP. Order matters:
// HTTPS first (most prod panels are reverse-proxied), then plain HTTP,
// then the panel's direct backend port for diagnostic edge cases.
func TestBuildRedeemCandidateURLs(t *testing.T) {
	cases := []struct {
		name     string
		sourceIP string
		panelURL string
		want     []string
	}{
		{
			name:     "ip only, default fallback",
			sourceIP: "203.0.113.10",
			want:     []string{"https://203.0.113.10", "http://203.0.113.10", "http://203.0.113.10:8080"},
		},
		{
			name:     "explicit https URL takes precedence and is single-shot",
			sourceIP: "203.0.113.10",
			panelURL: "https://panel.old.example",
			want:     []string{"https://panel.old.example"},
		},
		{
			name:     "schemeless panelURL tries both",
			panelURL: "panel.old.example",
			want:     []string{"https://panel.old.example", "http://panel.old.example"},
		},
		{
			name:     "trailing slash trimmed",
			panelURL: "https://panel.old.example/",
			want:     []string{"https://panel.old.example"},
		},
		{
			name: "no inputs returns empty",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildRedeemCandidateURLs(c.sourceIP, c.panelURL)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("urls[%d]: got %q want %q (full got=%v)", i, got[i], c.want[i], got)
				}
			}
		})
	}
}
