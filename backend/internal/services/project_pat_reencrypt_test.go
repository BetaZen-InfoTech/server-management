package services

import (
	"encoding/hex"
	"testing"

	"github.com/betazeninfotech/whm-cpanel-management/pkg/crypto"
)

// TestReencryptPATForTransfer is the regression guard for the
// "Deploy Software GitHub PAT goes missing on server transfer" bug.
//
// The contract: a cipher sealed under SOURCE's APP_ENCRYPTION_KEY must
// round-trip through ReencryptPATForTransfer and come out as a fresh
// cipher that DESTINATION's key can decrypt back to the same plaintext.
// If this ever stops working, every migrated project's PAT becomes
// unreadable garbage and `git pull` / auto-deploy break for the operator.
func TestReencryptPATForTransfer(t *testing.T) {
	srcKey := mustGenKey(t)
	dstKey := mustGenKey(t)

	plain := []byte("ghp_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789")
	srcCipher, err := crypto.EncryptGCM(plain, mustLoadKey(t, srcKey))
	if err != nil {
		t.Fatalf("seal under source key: %v", err)
	}

	dst := &ProjectService{encKey: mustLoadKey(t, dstKey)}

	got, err := dst.ReencryptPATForTransfer(srcCipher, srcKey)
	if err != nil {
		t.Fatalf("ReencryptPATForTransfer: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("re-encrypted cipher is empty — destination would land PAT-less")
	}

	// The whole point: the destination key must be able to read the new cipher.
	roundTrip, err := crypto.DecryptGCM(got, mustLoadKey(t, dstKey))
	if err != nil {
		t.Fatalf("destination key cannot decrypt re-encrypted cipher: %v", err)
	}
	if string(roundTrip) != string(plain) {
		t.Fatalf("plaintext changed: got %q want %q", roundTrip, plain)
	}

	// And the new cipher must be DIFFERENT from the input — otherwise we
	// haven't actually rotated the seal envelope. AES-GCM uses a fresh
	// nonce on every Seal so even encrypting the same plaintext twice
	// under the same key produces different ciphertext.
	if string(got) == string(srcCipher) {
		t.Fatal("re-encrypted cipher equals source cipher — re-encryption is a no-op")
	}
}

// TestReencryptPATForTransfer_EmptyInputs locks in the "no PAT to migrate"
// fast-path: empty cipher OR empty key returns (nil, nil) so the caller
// can skip the whole branch without surfacing a spurious "decrypt failed"
// warning to the operator.
func TestReencryptPATForTransfer_EmptyInputs(t *testing.T) {
	dstKey := mustGenKey(t)
	dst := &ProjectService{encKey: mustLoadKey(t, dstKey)}

	got, err := dst.ReencryptPATForTransfer(nil, dstKey)
	if err != nil || got != nil {
		t.Fatalf("empty cipher: got=%v err=%v, want (nil,nil)", got, err)
	}
	got, err = dst.ReencryptPATForTransfer([]byte("anything"), "")
	if err != nil || got != nil {
		t.Fatalf("empty key: got=%v err=%v, want (nil,nil)", got, err)
	}
}

// TestReencryptPATForTransfer_WrongSourceKey ensures we surface a clear
// error (not silent garbage) when the source key the destination grepped
// off /opt/serverpanel/.env doesn't match the key that actually sealed
// the cipher (key rotated mid-life, .env regenerated, etc.). Caller
// turns this into a "re-enter PAT" warning + drops the field, which
// is the right UX — better than persisting an undecryptable blob.
func TestReencryptPATForTransfer_WrongSourceKey(t *testing.T) {
	realSrcKey := mustGenKey(t)
	wrongSrcKey := mustGenKey(t)
	dstKey := mustGenKey(t)

	srcCipher, err := crypto.EncryptGCM([]byte("ghp_real_pat"), mustLoadKey(t, realSrcKey))
	if err != nil {
		t.Fatal(err)
	}
	dst := &ProjectService{encKey: mustLoadKey(t, dstKey)}

	got, err := dst.ReencryptPATForTransfer(srcCipher, wrongSrcKey)
	if err == nil {
		t.Fatal("expected error when source key doesn't match cipher, got nil")
	}
	if got != nil {
		t.Fatalf("got non-nil cipher %x on failure — caller would persist garbage", got)
	}
}

// TestReencryptPATForTransfer_DestKeyMissing covers the boot-order bug
// where SetProjectService landed before APP_ENCRYPTION_KEY was loaded.
// We must refuse to encrypt rather than panic or stamp a 0-byte key.
func TestReencryptPATForTransfer_DestKeyMissing(t *testing.T) {
	srcKey := mustGenKey(t)
	srcCipher, err := crypto.EncryptGCM([]byte("ghp_x"), mustLoadKey(t, srcKey))
	if err != nil {
		t.Fatal(err)
	}
	dst := &ProjectService{encKey: nil}

	if _, err := dst.ReencryptPATForTransfer(srcCipher, srcKey); err == nil {
		t.Fatal("expected error when destination key is unset, got nil")
	}
}

func mustGenKey(t *testing.T) string {
	t.Helper()
	k, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return k
}

func mustLoadKey(t *testing.T, raw string) []byte {
	t.Helper()
	b, err := crypto.LoadKey(raw)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if _, err := hex.DecodeString(raw); err != nil {
		t.Fatalf("test key not hex: %v", err)
	}
	return b
}
