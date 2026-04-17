// Package crypto provides small, carefully-scoped helpers for symmetric
// encryption of secrets (Personal Access Tokens, webhook secrets) stored in
// MongoDB. AES-GCM is chosen because it authenticates the ciphertext, and the
// 96-bit random nonce is prepended to the output so decryption needs nothing
// but the key.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// keyLen is the required AES-256 key length.
const keyLen = 32

// nonceLen is the required GCM nonce length.
const nonceLen = 12

// ErrShortCiphertext is returned when DecryptGCM is given input shorter than
// the minimum nonce + tag envelope.
var ErrShortCiphertext = errors.New("crypto: ciphertext too short")

// EncryptGCM seals plaintext with AES-256-GCM using a fresh random nonce.
// Output layout: nonce (12 bytes) || ciphertext || tag (16 bytes).
func EncryptGCM(plaintext, key []byte) ([]byte, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("crypto: key must be %d bytes, got %d", keyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	// Seal appends ciphertext to nonce so the result is self-contained.
	return aead.Seal(nonce, nonce, plaintext, nil), nil
}

// DecryptGCM reverses EncryptGCM. Returns ErrShortCiphertext for obviously
// malformed input; any other failure (wrong key, tampered ciphertext) bubbles
// up from the AEAD Open call.
func DecryptGCM(ciphertext, key []byte) ([]byte, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("crypto: key must be %d bytes, got %d", keyLen, len(key))
	}
	if len(ciphertext) < nonceLen+16 {
		return nil, ErrShortCiphertext
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, body := ciphertext[:nonceLen], ciphertext[nonceLen:]
	return aead.Open(nil, nonce, body, nil)
}

// MaskToken returns a safe preview of a Personal Access Token like
// "ghp_****abcd". Empty and very-short tokens collapse to fixed placeholders
// so the UI never renders a bare raw secret by accident.
func MaskToken(tok string) string {
	if tok == "" {
		return ""
	}
	if len(tok) <= 8 {
		return "****"
	}
	// Keep the provider prefix (ghp_, github_pat_, gho_, etc.) visible up to
	// the first underscore for human recognition, plus the final 4 characters.
	prefix := ""
	for i := 0; i < len(tok) && i < 16; i++ {
		if tok[i] == '_' {
			prefix = tok[:i+1]
			break
		}
	}
	if prefix == "" {
		prefix = tok[:4] + "_"
	}
	return prefix + "****" + tok[len(tok)-4:]
}

// LoadKey decodes a 32-byte AES key from a string. Accepts either base64
// (with or without padding) or lowercase hex. Returns an error if the decoded
// length is not exactly 32 bytes.
func LoadKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, errors.New("crypto: key is empty")
	}
	// Try hex first (most explicit), then base64.
	if b, err := hex.DecodeString(raw); err == nil && len(b) == keyLen {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == keyLen {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(b) == keyLen {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(raw); err == nil && len(b) == keyLen {
		return b, nil
	}
	return nil, fmt.Errorf("crypto: key must decode to %d bytes (got hex/base64)", keyLen)
}

// GenerateKey returns a fresh random 32-byte key, hex-encoded. Used on boot
// in non-production environments when APP_ENCRYPTION_KEY isn't set, so dev
// instances still function without operator setup.
func GenerateKey() (string, error) {
	b := make([]byte, keyLen)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
