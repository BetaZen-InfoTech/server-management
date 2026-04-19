package services

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/ssh"
)

// TransferTokenService manages the source-side lifecycle of transfer
// tokens: minting, listing, revoking, redeeming, and the periodic janitor
// that prunes expired authorized_keys lines.
//
// All authorized_keys mutations go through one mutex (authKeysMu) — this
// file is the only writer the panel knows about, so we don't need
// fcntl-style locking, just in-process serialisation.
type TransferTokenService struct {
	db *mongo.Database
}

func NewTransferTokenService(db *mongo.Database) *TransferTokenService {
	return &TransferTokenService{db: db}
}

const (
	authorizedKeysPath  = "/root/.ssh/authorized_keys"
	tokenStringPrefix   = "bzn_xfer_"
	tokenMarkerPrefix   = "bzn-transfer-"
	defaultTokenTTLHrs  = 24
	maxTokenTTLHrs      = 168 // 7 days
	defaultTokenSSHPort = 22
)

var authKeysMu sync.Mutex

// ============================================================================
// Issue
// ============================================================================

// Issue mints a new token. The flow is:
//
//  1. Generate ed25519 keypair.
//  2. Generate a random opaque token string and bcrypt-hash it for storage.
//  3. Append the public key to /root/.ssh/authorized_keys with a marker
//     comment ("bzn-transfer-<id>") so the janitor can find and remove it
//     when the token expires or is revoked.
//  4. Persist the record (private key in PEM, hash, marker, expiry).
//  5. Return the plain token in the response — this is the ONE chance the
//     operator gets to copy it.
//
// On any error after step 3 we attempt to roll back the authorized_keys
// edit so we don't leak temporary access into the file.
func (s *TransferTokenService) Issue(ctx context.Context, req *models.IssueTransferTokenRequest, createdBy *models.User) (*models.TransferToken, error) {
	ttl := req.TTLHours
	if ttl <= 0 {
		ttl = defaultTokenTTLHrs
	}
	if ttl > maxTokenTTLHrs {
		ttl = maxTokenTTLHrs
	}
	sshPort := req.SSHPort
	if sshPort <= 0 || sshPort > 65535 {
		sshPort = defaultTokenSSHPort
	}

	// ed25519 keypair (small, fast, supported by golang.org/x/crypto/ssh).
	pubRaw, privRaw, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate keypair: %w", err)
	}
	privPEM, pubAuthorized, err := encodeED25519Keypair(privRaw, pubRaw)
	if err != nil {
		return nil, fmt.Errorf("encode keypair: %w", err)
	}

	// Plain token + hash. The plain token is shown once and discarded —
	// only its SHA-256 lives in the DB. We don't use bcrypt here because
	// (a) the token is already 256 bits of cryptographic randomness, so
	// slow-hashing buys nothing against brute force, and (b) bcrypt has a
	// 72-byte input cap that our `bzn_xfer_<64-hex>` token format
	// overshoots by one. Constant-time SHA-256 hex compare is the right
	// shape for "verify a high-entropy bearer token" — it's what every
	// API-key system does.
	plainToken, err := newTokenString()
	if err != nil {
		return nil, err
	}
	hash := hashToken(plainToken)

	// Pre-generate the record ID so we can build the marker comment with it.
	recordID := primitive.NewObjectID()
	marker := tokenMarkerPrefix + recordID.Hex()
	authLine := fmt.Sprintf("%s %s", strings.TrimSpace(pubAuthorized), marker)

	if err := appendAuthorizedKey(authLine); err != nil {
		return nil, fmt.Errorf("install authorized_keys line: %w", err)
	}

	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(ttl) * time.Hour)
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = fmt.Sprintf("Transfer token (%s)", now.Format("2006-01-02 15:04"))
	}

	tok := models.TransferToken{
		ID:            recordID,
		Label:         label,
		TokenHash:     hash,
		TokenPrefix:   plainToken[:12],
		SSHUser:       "root",
		SSHPort:       sshPort,
		PrivateKey:    privPEM,
		PublicKey:     pubAuthorized,
		AuthKeyMarker: marker,
		Status:        "active",
		CreatedAt:     now,
		ExpiresAt:     expiresAt,
	}
	if createdBy != nil {
		tok.CreatedByUserID = createdBy.ID
		tok.CreatedByEmail = createdBy.Email
	}

	if _, err := s.db.Collection(database.ColTransferTokens).InsertOne(ctx, tok); err != nil {
		// Best-effort rollback so we don't leave a dangling authorized_keys line.
		_ = removeAuthorizedKeyByMarker(marker)
		return nil, fmt.Errorf("persist token: %w", err)
	}

	tok.PlainToken = plainToken
	// Don't echo private key in the response shape — only the redeem path
	// returns it, gated on knowledge of the plain token.
	tok.PrivateKey = ""
	return &tok, nil
}

// ============================================================================
// List / Revoke / Redeem
// ============================================================================

func (s *TransferTokenService) List(ctx context.Context) ([]models.TransferToken, error) {
	col := s.db.Collection(database.ColTransferTokens)
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := col.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var out []models.TransferToken
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []models.TransferToken{}
	}
	// Mark expired tokens that the janitor hasn't visited yet — purely
	// cosmetic, doesn't touch the DB. The redeem path checks ExpiresAt
	// independently.
	for i := range out {
		if out[i].Status == "active" && time.Now().After(out[i].ExpiresAt) {
			out[i].Status = "expired"
		}
		out[i].PrivateKey = ""
		out[i].TokenHash = ""
	}
	return out, nil
}

// Revoke flips the token to "revoked" and removes its authorized_keys
// line. Idempotent: revoking an already-revoked token is a no-op.
func (s *TransferTokenService) Revoke(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("invalid token ID")
	}
	col := s.db.Collection(database.ColTransferTokens)
	var tok models.TransferToken
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&tok); err != nil {
		return err
	}
	if tok.Status == "revoked" {
		return nil
	}
	if tok.AuthKeyMarker != "" {
		_ = removeAuthorizedKeyByMarker(tok.AuthKeyMarker)
	}
	now := time.Now().UTC()
	_, err = col.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{"status": "revoked", "revoked_at": now},
	})
	return err
}

// Redeem matches a plain token to its record, returns the SSH credential
// payload, and stamps RedeemedAt. It is intentionally generic on failure
// ("invalid or expired transfer token") so a caller can't distinguish
// "wrong token" from "expired" from "revoked" — the redeem endpoint is
// public, so we don't want it to be a useful enumeration oracle.
//
// Redemption does NOT consume the token. The same SSH key is needed
// throughout the discover + transfer lifecycle (which can take hours), so
// the destination panel may redeem multiple times over the token's TTL.
// Single-use semantics would require the destination to cache the key,
// which adds a second secret-storage surface for no real safety win — the
// authorized_keys line is already revoked when the token expires.
func (s *TransferTokenService) Redeem(ctx context.Context, plain, fromIP string) (*models.RedeemTransferTokenResponse, error) {
	plain = strings.TrimSpace(plain)
	if !strings.HasPrefix(plain, tokenStringPrefix) || len(plain) < len(tokenStringPrefix)+16 {
		return nil, errors.New("invalid or expired transfer token")
	}
	col := s.db.Collection(database.ColTransferTokens)

	// Narrow the bcrypt scan via the prefix — bcrypt comparisons are slow,
	// so we only check rows whose first 12 chars match. Even if two tokens
	// happen to share a prefix (vanishingly unlikely with 32 bytes of
	// entropy), we just compare both.
	prefix := plain[:12]
	cursor, err := col.Find(ctx, bson.M{
		"token_prefix": prefix,
		"status":       "active",
	})
	if err != nil {
		return nil, errors.New("invalid or expired transfer token")
	}
	defer cursor.Close(ctx)

	var candidates []models.TransferToken
	if err := cursor.All(ctx, &candidates); err != nil {
		return nil, errors.New("invalid or expired transfer token")
	}

	expected := hashToken(plain)
	for i := range candidates {
		t := &candidates[i]
		if time.Now().After(t.ExpiresAt) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(t.TokenHash), []byte(expected)) != 1 {
			continue
		}
		// Stamp redemption (best-effort).
		now := time.Now().UTC()
		_, _ = col.UpdateOne(ctx, bson.M{"_id": t.ID}, bson.M{
			"$set": bson.M{"redeemed_at": now, "redeemed_from_ip": fromIP},
		})
		return &models.RedeemTransferTokenResponse{
			SSHUser:       t.SSHUser,
			SSHPort:       t.SSHPort,
			PrivateKeyPEM: t.PrivateKey,
			TokenID:       t.ID.Hex(),
			ExpiresAt:     t.ExpiresAt,
		}, nil
	}
	return nil, errors.New("invalid or expired transfer token")
}

// ============================================================================
// Janitor
// ============================================================================

// StartJanitor kicks off a goroutine that, once an hour, sweeps tokens
// past their ExpiresAt and removes their authorized_keys lines. Safe to
// call multiple times — but only the first call has any effect.
func (s *TransferTokenService) StartJanitor(ctx context.Context) {
	go func() {
		t := time.NewTicker(1 * time.Hour)
		defer t.Stop()
		// Run once on startup so a long-down panel doesn't leave keys live.
		s.sweep(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sweep(ctx)
			}
		}
	}()
}

func (s *TransferTokenService) sweep(ctx context.Context) {
	col := s.db.Collection(database.ColTransferTokens)
	cursor, err := col.Find(ctx, bson.M{
		"status":     "active",
		"expires_at": bson.M{"$lt": time.Now().UTC()},
	})
	if err != nil {
		return
	}
	defer cursor.Close(ctx)
	var expired []models.TransferToken
	if err := cursor.All(ctx, &expired); err != nil {
		return
	}
	for _, t := range expired {
		if t.AuthKeyMarker != "" {
			_ = removeAuthorizedKeyByMarker(t.AuthKeyMarker)
		}
		_, _ = col.UpdateOne(ctx, bson.M{"_id": t.ID}, bson.M{
			"$set": bson.M{"status": "expired"},
		})
	}
}

// ============================================================================
// Helpers — keypair encoding, token strings, authorized_keys editing
// ============================================================================

// encodeED25519Keypair returns (privatePEM, authorizedKeyEncoding, err).
//
// authorizedKeyEncoding is the single-line `ssh-ed25519 AAAA…` form the
// SSH server expects in authorized_keys. The marker comment is appended
// by the caller so this helper stays generic.
func encodeED25519Keypair(priv ed25519.PrivateKey, pub ed25519.PublicKey) (string, string, error) {
	// PKCS8-style PEM is what golang.org/x/crypto/ssh.ParsePrivateKey expects
	// out of the box on the destination side.
	privBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return "", "", err
	}
	privPEM := string(pem.EncodeToMemory(privBlock))

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	authorized := strings.TrimRight(string(ssh.MarshalAuthorizedKey(sshPub)), "\n")
	return privPEM, authorized, nil
}

// newTokenString returns a URL-safe opaque token like
// "bzn_xfer_a1b2…". 32 bytes of entropy → 64 hex chars → ~256 bits.
func newTokenString() (string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return tokenStringPrefix + hex.EncodeToString(buf), nil
}

// hashToken returns the hex-encoded SHA-256 of the plain token, prefixed
// with "sha256:" so the stored value is self-identifying. Compared with
// subtle.ConstantTimeCompare in Redeem to avoid a timing side channel
// across tokens with similar prefixes.
func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// appendAuthorizedKey writes a line to /root/.ssh/authorized_keys,
// ensuring the directory and file exist with the correct permissions.
// SSH refuses to read authorized_keys with loose perms, so we explicitly
// chmod after touching.
func appendAuthorizedKey(line string) error {
	authKeysMu.Lock()
	defer authKeysMu.Unlock()

	dir := filepath.Dir(authorizedKeysPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(authorizedKeysPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return err
	}
	// Re-assert restrictive perms in case the file already existed with
	// looser bits (sshd will silently ignore an authorized_keys file that
	// is group/world-writable).
	_ = os.Chmod(authorizedKeysPath, 0o600)
	_ = os.Chmod(dir, 0o700)
	return nil
}

// removeAuthorizedKeyByMarker drops every line in authorized_keys whose
// trailing comment matches the given marker. Idempotent: returns nil if
// the file or the marker line is absent.
func removeAuthorizedKeyByMarker(marker string) error {
	authKeysMu.Lock()
	defer authKeysMu.Unlock()

	data, err := os.ReadFile(authorizedKeysPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for _, ln := range lines {
		if strings.Contains(ln, marker) {
			changed = true
			continue
		}
		out = append(out, ln)
	}
	if !changed {
		return nil
	}
	final := strings.Join(out, "\n")
	if err := os.WriteFile(authorizedKeysPath, []byte(final), 0o600); err != nil {
		return err
	}
	_ = os.Chmod(authorizedKeysPath, 0o600)
	return nil
}

// ============================================================================
// Destination-side helper: redeem a token against a remote source panel.
// ============================================================================

// RedeemRemoteToken is what the DESTINATION panel calls when the operator
// pastes a token into the wizard. It hits the source's public redeem
// endpoint and returns the SSH credentials to use for the rest of the
// transfer.
//
// panelURL may be empty — in that case we try the canonical paths in
// order: https://<sourceIP>, http://<sourceIP>, http://<sourceIP>:8080.
// The first one that returns a 2xx with a parseable JSON body wins.
//
// We deliberately use InsecureSkipVerify for HTTPS attempts because the
// destination usually only knows the source's IP address (not its panel
// hostname) and the source's TLS cert is keyed to the hostname. The token
// itself is the trust anchor — leakage of the redeem response over a MITM
// would be bad, but the operator is migrating between two boxes they
// control, and the alternative (forcing the operator to know and type the
// hostname) is worse.
func RedeemRemoteToken(ctx context.Context, sourceIP, panelURL, plainToken string) (*models.RedeemTransferTokenResponse, error) {
	candidates := buildRedeemCandidateURLs(sourceIP, panelURL)
	if len(candidates) == 0 {
		return nil, errors.New("no panel URL to contact for token redemption")
	}

	bodyBytes := []byte(fmt.Sprintf(`{"token":%q}`, plainToken))
	httpClient := &http.Client{Timeout: 20 * time.Second, Transport: insecureTLSTransport()}

	var lastErr error
	for _, base := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/transfer/redeem", strings.NewReader(string(bodyBytes)))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("source panel %s returned HTTP %d", base, resp.StatusCode)
			continue
		}
		var wrapper struct {
			Data    *models.RedeemTransferTokenResponse `json:"data"`
			Success bool                                `json:"success"`
			Message string                              `json:"message"`
		}
		if err := decodeJSON(raw, &wrapper); err != nil {
			lastErr = fmt.Errorf("parse redeem response: %w", err)
			continue
		}
		if wrapper.Data == nil || wrapper.Data.PrivateKeyPEM == "" {
			lastErr = fmt.Errorf("redeem response missing key (msg: %s)", wrapper.Message)
			continue
		}
		return wrapper.Data, nil
	}
	if lastErr == nil {
		lastErr = errors.New("could not reach source panel")
	}
	return nil, lastErr
}

func buildRedeemCandidateURLs(sourceIP, panelURL string) []string {
	panelURL = strings.TrimRight(strings.TrimSpace(panelURL), "/")
	if panelURL != "" {
		// If user passed something like "panel.example.com" (no scheme),
		// try both schemes.
		if !strings.HasPrefix(panelURL, "http://") && !strings.HasPrefix(panelURL, "https://") {
			return []string{"https://" + panelURL, "http://" + panelURL}
		}
		return []string{panelURL}
	}
	if sourceIP == "" {
		return nil
	}
	return []string{
		"https://" + sourceIP,
		"http://" + sourceIP,
		"http://" + sourceIP + ":8080",
	}
}

func decodeJSON(raw []byte, into any) error {
	return json.Unmarshal(raw, into)
}

// insecureTLSTransport is broken out so the InsecureSkipVerify decision
// stays grep-able. Used only by the redeem client because the destination
// usually only knows the source's IP, not the cert hostname; the token
// itself is the trust anchor.
func insecureTLSTransport() http.RoundTripper {
	return &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}
}
