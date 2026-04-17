package githubsig

import "testing"

func TestGoodSignaturePasses(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "super-secret"
	sig := Sign(body, secret)
	if !VerifySignature(body, sig, secret) {
		t.Fatal("good signature rejected")
	}
}

func TestTamperedBodyFails(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "super-secret"
	sig := Sign(body, secret)
	if VerifySignature([]byte(`{"ref":"refs/heads/evil"}`), sig, secret) {
		t.Fatal("tampered body accepted")
	}
}

func TestWrongSecretFails(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	sig := Sign(body, "correct")
	if VerifySignature(body, sig, "wrong") {
		t.Fatal("wrong secret accepted")
	}
}

func TestEmptyInputsFail(t *testing.T) {
	body := []byte(`{}`)
	if VerifySignature(body, "", "secret") {
		t.Fatal("empty header accepted")
	}
	if VerifySignature(body, "sha256=abc", "") {
		t.Fatal("empty secret accepted")
	}
	if VerifySignature(body, "sha1=deadbeef", "secret") {
		t.Fatal("wrong prefix accepted")
	}
	if VerifySignature(body, "sha256=notahex", "secret") {
		t.Fatal("non-hex signature accepted")
	}
}
