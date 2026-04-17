// Package githubsig verifies the HMAC-SHA256 signatures GitHub sends with
// every webhook delivery. GitHub signs the raw request body with a per-webhook
// shared secret and presents the digest in the "X-Hub-Signature-256" header as
// "sha256=<hex>". We recompute it and do a constant-time compare.
package githubsig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const prefix = "sha256="

// VerifySignature returns true iff `header` is a valid "sha256=<hex>" digest
// of `body` under `secret`. All inputs must be non-empty — empty secret or
// empty signature always returns false. Comparison is timing-safe.
func VerifySignature(body []byte, header, secret string) bool {
	if secret == "" || !strings.HasPrefix(header, prefix) {
		return false
	}
	expectedHex := strings.TrimPrefix(header, prefix)
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), expected)
}

// Sign returns the "sha256=<hex>" header value for the given body and secret.
// Used by tests and by internal tooling that replays webhook payloads.
func Sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return prefix + hex.EncodeToString(mac.Sum(nil))
}
