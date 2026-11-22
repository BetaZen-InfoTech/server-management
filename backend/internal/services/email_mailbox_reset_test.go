package services

import "testing"

func TestMaskEmail(t *testing.T) {
	cases := map[string]string{
		"john.doe@gmail.com": "jo••••@gmail.com",
		"a@b.com":            "a••••@b.com",
		"":                   "",
		"noatsign":           "•••",
	}
	for in, want := range cases {
		if got := maskEmail(in); got != want {
			t.Errorf("maskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLooksLikeEmail(t *testing.T) {
	ok := []string{"a@b.com", "john.doe@gmail.com", "x@y.io"}
	bad := []string{"", "nodomain", "a@b", "@b.com", "a@", "a b@c.com", "a@b c.com"}
	for _, s := range ok {
		if !looksLikeEmail(s) {
			t.Errorf("looksLikeEmail(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if looksLikeEmail(s) {
			t.Errorf("looksLikeEmail(%q) = true, want false", s)
		}
	}
}

func TestRandomTokenUnique(t *testing.T) {
	a, err := randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := randomToken(32)
	if a == b {
		t.Error("randomToken returned identical values")
	}
	if len(a) != 64 { // 32 bytes -> 64 hex chars
		t.Errorf("randomToken length = %d, want 64", len(a))
	}
}
