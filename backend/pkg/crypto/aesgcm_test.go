package crypto

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	key, _ := LoadKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	plain := []byte("ghp_abc123DEF456ghi789JKL")

	ct, err := EncryptGCM(plain, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := DecryptGCM(ct, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("got %q want %q", got, plain)
	}
}

func TestWrongKeyFails(t *testing.T) {
	k1, _ := LoadKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	k2, _ := LoadKey("fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210")

	ct, err := EncryptGCM([]byte("secret"), k1)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := DecryptGCM(ct, k2); err == nil {
		t.Fatal("decrypt with wrong key should fail")
	}
}

func TestNonDeterministic(t *testing.T) {
	key, _ := LoadKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	a, _ := EncryptGCM([]byte("same input"), key)
	b, _ := EncryptGCM([]byte("same input"), key)
	if bytes.Equal(a, b) {
		t.Fatal("encryption should be non-deterministic due to random nonce")
	}
}

func TestTamperedCiphertextRejected(t *testing.T) {
	key, _ := LoadKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	ct, _ := EncryptGCM([]byte("secret"), key)
	ct[len(ct)-1] ^= 0x01
	if _, err := DecryptGCM(ct, key); err == nil {
		t.Fatal("tampered ciphertext should fail auth")
	}
}

func TestShortCiphertext(t *testing.T) {
	key, _ := LoadKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if _, err := DecryptGCM([]byte{1, 2, 3}, key); err != ErrShortCiphertext {
		t.Fatalf("expected ErrShortCiphertext, got %v", err)
	}
}

func TestMaskToken(t *testing.T) {
	cases := map[string]string{
		"":                                "",
		"short":                           "****",
		"ghp_abcdefghijklmnop1234":        "ghp_****1234",
		"github_pat_ABC123DEFxyz0000abcd": "github_****abcd",
		"noprefixbutlongstring":           "nopr_****ring",
	}
	for in, want := range cases {
		if got := MaskToken(in); got != want {
			t.Errorf("MaskToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadKey(t *testing.T) {
	// hex
	if _, err := LoadKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
		t.Errorf("hex key should load: %v", err)
	}
	// wrong length
	if _, err := LoadKey("0123456789abcdef"); err == nil {
		t.Error("short key should fail")
	}
	// empty
	if _, err := LoadKey(""); err == nil {
		t.Error("empty key should fail")
	}
}
