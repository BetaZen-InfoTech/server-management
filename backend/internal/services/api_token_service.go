// Package services — APITokenService is the issuer + verifier for the
// programmatic API tokens that authenticate /api/v1/whm/api/* and
// /api/v1/cpanel/api/* calls.
//
// Security notes (the "secure picks" path the operator chose):
//   - Token format: btz_<env>_<11-char id>_<32-char secret>. Only the id is
//     stored visible; the secret half is bcrypt-hashed before insert.
//   - The plaintext is shown to the operator exactly once at creation; rotate
//     mints a fresh secret without changing the id.
//   - Verify uses constant-time bcrypt compare. Failed-auth lockout kicks in
//     after 10 wrong secrets within ~1 minute (resets on success).
//   - Tokens cannot exceed the creator's permissions: the scope catalogue maps
//     each scope key to the underlying RBAC permission and the issue path
//     drops scopes the creator can't grant.
package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/password"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// tokenPrefix is the visible namespace marker. "btz" = Betazen.
const tokenPrefix = "btz"

// tokenIDBytes / tokenSecretBytes set the post-encoding lengths (each byte
// becomes two hex chars). 6 bytes id → 12 hex; 32 bytes secret → 64 hex.
const (
	tokenIDBytes     = 6
	tokenSecretBytes = 32
)

// APITokenService is the persistence + verification layer for API tokens.
type APITokenService struct {
	db  *mongo.Database
	env string
}

// NewAPITokenService returns a service bound to the given Mongo database.
// env is "prod" or "dev"; it shows up in the token prefix so operators can
// tell at a glance which environment a leaked token belongs to.
func NewAPITokenService(db *mongo.Database, env string) *APITokenService {
	if env == "" {
		env = "prod"
	}
	if !strings.EqualFold(env, "production") && !strings.EqualFold(env, "prod") {
		env = "dev"
	} else {
		env = "prod"
	}
	s := &APITokenService{db: db, env: env}
	s.ensureIndexes(context.Background())
	return s
}

func (s *APITokenService) ensureIndexes(ctx context.Context) {
	col := s.db.Collection(database.ColAPITokens)
	_, _ = col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "token_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "owner_user_id", Value: 1}}},
		{Keys: bson.D{{Key: "tenant_id", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "expires_at", Value: 1}}},
	})
}

// Issue mints a fresh token. The plaintext token (id + secret) is returned in
// IssuedAPIToken.PlaintextToken; only the bcrypt hash of the secret half is
// stored. callerPerms is the JWT permission list of the user creating the
// token — scopes whose underlying permission isn't on this list are rejected
// so a token can't grant powers the operator doesn't have. isSuper short-
// circuits the check for tenant-root super admins (mirrors RequirePermission).
func (s *APITokenService) Issue(ctx context.Context, scope *CallerScope, callerPerms []string, isSuper bool, req models.CreateAPITokenRequest) (*models.IssuedAPIToken, error) {
	if scope == nil {
		return nil, errors.New("api token: caller scope required")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("api token: name is required")
	}
	if len(req.Scopes) == 0 {
		return nil, errors.New("api token: at least one scope is required")
	}

	scopes, err := s.filterAllowedScopes(scope, callerPerms, isSuper, req.Scopes)
	if err != nil {
		return nil, err
	}

	tokenID, err := apiTokenRandomHex(tokenIDBytes)
	if err != nil {
		return nil, fmt.Errorf("api token: id gen: %w", err)
	}
	secret, err := apiTokenRandomHex(tokenSecretBytes)
	if err != nil {
		return nil, fmt.Errorf("api token: secret gen: %w", err)
	}
	plaintext := fmt.Sprintf("%s_%s_%s_%s", tokenPrefix, s.env, tokenID, secret)
	prefix := fmt.Sprintf("%s_%s_%s", tokenPrefix, s.env, tokenID[:4])

	hash, err := password.Hash(secret)
	if err != nil {
		return nil, fmt.Errorf("api token: hash: %w", err)
	}

	now := time.Now()
	tok := &models.APIToken{
		TokenID:    tokenID,
		SecretHash: hash,
		Prefix:     prefix,
		Name:       name,
		Description: strings.TrimSpace(req.Description),
		OwnerKind:  ownerKindFromScope(scope),
		Scopes:     scopes,
		IPAllowlist: cleanIPAllowlist(req.IPAllowlist),
		Status:     models.APITokenStatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if oid, err := primitive.ObjectIDFromHex(scope.UserHex); err == nil {
		tok.OwnerUserID = oid
	}
	if oid, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
		tok.TenantID = oid
	}

	if req.ExpiresInDays > 0 {
		exp := now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		tok.ExpiresAt = &exp
	}

	// Pinned vendor — only honoured for the platform owner. Anyone else
	// asking is silently ignored so the field can't be used to cross
	// tenant boundaries from a vendor's own console.
	if scope.Role == "vendor_owner" && req.PinnedVendorID != "" {
		if oid, err := primitive.ObjectIDFromHex(req.PinnedVendorID); err == nil {
			tok.PinnedVendorID = oid
		}
	}

	res, err := s.db.Collection(database.ColAPITokens).InsertOne(ctx, tok)
	if err != nil {
		return nil, fmt.Errorf("api token: insert: %w", err)
	}
	tok.ID = res.InsertedID.(primitive.ObjectID)
	return &models.IssuedAPIToken{Token: tok, PlaintextToken: plaintext}, nil
}

// List returns the caller's tokens, newest first. Vendors only see their own
// tenant's; the owner sees everything.
//
// 3.1.105 — each row is enriched in-memory with the owner's display
// name + username so the WHM Developer page can render an Owner
// column without a second round-trip per row. Tokens created by the
// platform admin (OwnerKind == "platform") render as
// "Platform admin"; tokens created by a vendor render as that
// vendor's name + username from the users collection. Missing user
// (deleted vendor) leaves both fields empty and the UI shows a "—".
//
// Owner lookup is a single $in CountDocuments over unique
// OwnerUserIDs — one mongo round-trip regardless of how many tokens
// the operator has. No N+1 fan-out.
func (s *APITokenService) List(ctx context.Context, scope *CallerScope) ([]*models.APIToken, error) {
	filter := bson.M{}
	if scope != nil && scope.Role != "vendor_owner" {
		if oid, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			filter["tenant_id"] = oid
		}
	}
	cur, err := s.db.Collection(database.ColAPITokens).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []*models.APIToken{}
	ownerIDs := make(map[primitive.ObjectID]struct{})
	for cur.Next(ctx) {
		var t models.APIToken
		if err := cur.Decode(&t); err == nil {
			out = append(out, &t)
			if !t.OwnerUserID.IsZero() {
				ownerIDs[t.OwnerUserID] = struct{}{}
			}
		}
	}

	// Single round-trip to resolve owner identities for the whole list.
	if len(ownerIDs) > 0 {
		ids := make([]primitive.ObjectID, 0, len(ownerIDs))
		for id := range ownerIDs {
			ids = append(ids, id)
		}
		userCur, uerr := s.db.Collection(database.ColUsers).Find(ctx, bson.M{
			"_id": bson.M{"$in": ids},
		}, options.Find().SetProjection(bson.M{"username": 1, "name": 1, "role": 1}))
		ownerByID := make(map[primitive.ObjectID]struct {
			name, username, role string
		}, len(ownerIDs))
		if uerr == nil {
			for userCur.Next(ctx) {
				var u struct {
					ID       primitive.ObjectID `bson:"_id"`
					Username string             `bson:"username"`
					Name     string             `bson:"name"`
					Role     string             `bson:"role"`
				}
				if err := userCur.Decode(&u); err == nil {
					ownerByID[u.ID] = struct{ name, username, role string }{u.Name, u.Username, u.Role}
				}
			}
			userCur.Close(ctx)
		}

		for _, t := range out {
			// Platform-admin tokens render with a fixed label so vendors
			// looking at their own rows don't see admin's username
			// (privacy + clarity — "Platform admin" is the operator
			// mental model).
			if t.OwnerKind == "platform" {
				t.OwnerName = "Platform admin"
				continue
			}
			u, ok := ownerByID[t.OwnerUserID]
			if !ok {
				continue // user deleted → fields stay empty, UI shows —
			}
			// Cross-check by role too: a token where OwnerKind isn't
			// "platform" but the user is actually vendor_owner gets the
			// same "Platform admin" label rather than leaking the
			// admin's account name.
			if u.role == "vendor_owner" {
				t.OwnerName = "Platform admin"
				continue
			}
			t.OwnerName = u.name
			t.OwnerUsername = u.username
		}
	}
	return out, nil
}

// Revoke marks a token revoked. The bearer string immediately stops working —
// the lookup still finds the row, but Verify rejects it for status != active.
func (s *APITokenService) Revoke(ctx context.Context, scope *CallerScope, idHex string) error {
	oid, err := primitive.ObjectIDFromHex(idHex)
	if err != nil {
		return errors.New("invalid token id")
	}
	filter := bson.M{"_id": oid}
	if scope != nil && scope.Role != "vendor_owner" {
		if t, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			filter["tenant_id"] = t
		}
	}
	now := time.Now()
	res, err := s.db.Collection(database.ColAPITokens).UpdateOne(ctx, filter, bson.M{
		"$set": bson.M{"status": models.APITokenStatusRevoked, "revoked_at": now, "updated_at": now},
	})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errors.New("token not found")
	}
	return nil
}

// Delete permanently removes a token row (hard delete). Unlike Revoke — which
// keeps the row so the audit trail survives and Verify can reject it as
// "revoked" — this purges it entirely, so the Developer list can be cleaned of
// dead / rotated-away / orphaned tokens. Same tenant guard as Revoke: a
// non-owner caller can only delete tokens inside their own tenant; the platform
// owner can delete any. Works on tokens of any status (active or revoked).
func (s *APITokenService) Delete(ctx context.Context, scope *CallerScope, idHex string) error {
	oid, err := primitive.ObjectIDFromHex(idHex)
	if err != nil {
		return errors.New("invalid token id")
	}
	filter := bson.M{"_id": oid}
	if scope != nil && scope.Role != "vendor_owner" {
		if t, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			filter["tenant_id"] = t
		}
	}
	res, err := s.db.Collection(database.ColAPITokens).DeleteOne(ctx, filter)
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return errors.New("token not found")
	}
	return nil
}

// Rotate mints a fresh secret for the same token id. Returns the new
// plaintext exactly once; the old secret stops working immediately.
func (s *APITokenService) Rotate(ctx context.Context, scope *CallerScope, idHex string) (*models.IssuedAPIToken, error) {
	oid, err := primitive.ObjectIDFromHex(idHex)
	if err != nil {
		return nil, errors.New("invalid token id")
	}
	filter := bson.M{"_id": oid}
	if scope != nil && scope.Role != "vendor_owner" {
		if t, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			filter["tenant_id"] = t
		}
	}
	var existing models.APIToken
	if err := s.db.Collection(database.ColAPITokens).FindOne(ctx, filter).Decode(&existing); err != nil {
		return nil, errors.New("token not found")
	}

	secret, err := apiTokenRandomHex(tokenSecretBytes)
	if err != nil {
		return nil, err
	}
	hash, err := password.Hash(secret)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if _, err := s.db.Collection(database.ColAPITokens).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{
			"secret_hash":       hash,
			"updated_at":        now,
			"status":            models.APITokenStatusActive,
			"failed_auth_count": 0,
			"locked_until":      nil,
		},
	}); err != nil {
		return nil, err
	}
	existing.SecretHash = hash
	existing.UpdatedAt = now
	plaintext := fmt.Sprintf("%s_%s_%s_%s", tokenPrefix, s.env, existing.TokenID, secret)
	return &models.IssuedAPIToken{Token: &existing, PlaintextToken: plaintext}, nil
}

// SetScopes replaces the scope set on an existing token in place. The
// token's plaintext stays valid — only its authorization grant
// changes. Same tenant + RBAC guards as Issue and Rotate:
//
//   - Tenant gate: vendors can only edit tokens whose tenant_id
//     matches their own (vendor_owner can edit any token).
//   - Permission gate: the new scopes pass through
//     filterAllowedScopes, so an operator can't grant a scope they
//     don't themselves hold (e.g. a vendor_admin without
//     `deploy.manage` can't add `deploy:link` to a token they can
//     otherwise edit). This is the same rule Issue enforces, so an
//     edit can't elevate beyond what a fresh create could produce.
//
// Returns the updated token row (without the bearer string — bearer
// stays unchanged; only Rotate ever mints a new one).
//
// Survives server-to-server transfer automatically: the scopes field
// is a plain []string on the api_tokens collection and the transfer
// pipeline's syncAPITokens copies the whole doc through normaliseDoc,
// so edited scopes ride along to the destination with no extra work.
func (s *APITokenService) SetScopes(ctx context.Context, scope *CallerScope, callerPerms []string, isSuper bool, idHex string, newScopes []string) (*models.APIToken, error) {
	if scope == nil {
		return nil, errors.New("api token: caller scope required")
	}
	if len(newScopes) == 0 {
		return nil, errors.New("api token: at least one scope is required")
	}
	oid, err := primitive.ObjectIDFromHex(idHex)
	if err != nil {
		return nil, errors.New("invalid token id")
	}

	filter := bson.M{"_id": oid}
	if scope.Role != "vendor_owner" {
		if t, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			filter["tenant_id"] = t
		}
	}
	var existing models.APIToken
	if err := s.db.Collection(database.ColAPITokens).FindOne(ctx, filter).Decode(&existing); err != nil {
		return nil, errors.New("token not found")
	}

	// Re-validate the new scope set against the operator's own
	// permissions — same gate Issue uses. Unknown scope keys are
	// rejected, scopes the operator doesn't have are silently
	// dropped (filterAllowedScopes returns the filtered subset).
	allowed, err := s.filterAllowedScopes(scope, callerPerms, isSuper, newScopes)
	if err != nil {
		return nil, err
	}
	if len(allowed) == 0 {
		return nil, errors.New("api token: none of the requested scopes are grantable by the caller")
	}

	now := time.Now()
	if _, err := s.db.Collection(database.ColAPITokens).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{
			"scopes":     allowed,
			"updated_at": now,
		},
	}); err != nil {
		return nil, err
	}
	existing.Scopes = allowed
	existing.UpdatedAt = now
	existing.SecretHash = "" // never return the hash
	return &existing, nil
}

// Verify is the hot-path bearer lookup used by middleware.APIToken. Returns
// the token row + a freshly-derived CallerScope on a hit. Wrong-secret hits
// bump failed_auth_count; ten failures inside a minute lock the row.
func (s *APITokenService) Verify(ctx context.Context, raw, ip string) (*models.APIToken, *CallerScope, error) {
	id, secret, ok := splitPlaintext(raw)
	if !ok {
		return nil, nil, errors.New("malformed token")
	}
	col := s.db.Collection(database.ColAPITokens)
	var tok models.APIToken
	if err := col.FindOne(ctx, bson.M{"token_id": id}).Decode(&tok); err != nil {
		return nil, nil, errors.New("token not found")
	}
	if tok.Status == models.APITokenStatusRevoked {
		return nil, nil, errors.New("token revoked")
	}
	if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
		_, _ = col.UpdateOne(ctx, bson.M{"_id": tok.ID}, bson.M{"$set": bson.M{"status": models.APITokenStatusExpired}})
		return nil, nil, errors.New("token expired")
	}
	if tok.LockedUntil != nil && tok.LockedUntil.After(time.Now()) {
		return nil, nil, errors.New("token temporarily locked after repeated failed auth")
	}
	if !password.Verify(secret, tok.SecretHash) {
		s.recordFailedAuth(ctx, tok.ID)
		return nil, nil, errors.New("invalid token")
	}
	if len(tok.IPAllowlist) > 0 && !ipAllowed(ip, tok.IPAllowlist) {
		return nil, nil, errors.New("IP not allowed for this token")
	}
	now := time.Now()
	_, _ = col.UpdateOne(ctx, bson.M{"_id": tok.ID}, bson.M{
		"$set": bson.M{"last_used_at": now, "last_used_ip": ip, "failed_auth_count": 0, "locked_until": nil},
		"$inc": bson.M{"usage_count": 1},
	})

	// Build the CallerScope the middleware will inject. If the token has
	// a pinned_vendor_id, swap in that vendor's user_id so every service
	// call routes through the pinned tenant — even though the token was
	// issued by vendor_owner.
	scope := &CallerScope{
		Role:      ownerRoleFromKind(tok.OwnerKind),
		UserHex:   tok.OwnerUserID.Hex(),
		TenantHex: tok.TenantID.Hex(),
	}
	if !tok.PinnedVendorID.IsZero() {
		if vendor, err := s.lookupTenantHexByUserID(ctx, tok.PinnedVendorID); err == nil {
			scope.Role = "vendor_admin"
			scope.UserHex = tok.PinnedVendorID.Hex()
			scope.TenantHex = vendor
		}
	}
	return &tok, scope, nil
}

// HasScope is the per-route gate. The route declares which scope it needs
// (e.g. "domain:write"); this returns true if the token has it OR has the
// "*" wildcard (reserved; not granted via UI today).
func HasScope(tok *models.APIToken, required string) bool {
	if tok == nil {
		return false
	}
	for _, s := range tok.Scopes {
		if s == "*" || s == required {
			return true
		}
	}
	return false
}

func (s *APITokenService) recordFailedAuth(ctx context.Context, id primitive.ObjectID) {
	col := s.db.Collection(database.ColAPITokens)
	now := time.Now()
	update := bson.M{"$inc": bson.M{"failed_auth_count": 1}, "$set": bson.M{"updated_at": now}}
	_, _ = col.UpdateOne(ctx, bson.M{"_id": id}, update)
	var t models.APIToken
	if err := col.FindOne(ctx, bson.M{"_id": id}).Decode(&t); err != nil {
		return
	}
	if t.FailedAuthCount >= 10 {
		until := now.Add(15 * time.Minute)
		_, _ = col.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
			"locked_until": until, "failed_auth_count": 0,
		}})
	}
}

// SweepExpired flips status=expired on rows whose expires_at has passed. Run on
// a 30-minute cron from the panel; cheap to rerun and idempotent.
func (s *APITokenService) SweepExpired(ctx context.Context) int64 {
	res, err := s.db.Collection(database.ColAPITokens).UpdateMany(ctx,
		bson.M{"status": models.APITokenStatusActive, "expires_at": bson.M{"$lt": time.Now()}},
		bson.M{"$set": bson.M{"status": models.APITokenStatusExpired, "updated_at": time.Now()}})
	if err != nil {
		return 0
	}
	return res.ModifiedCount
}

func (s *APITokenService) lookupTenantHexByUserID(ctx context.Context, uid primitive.ObjectID) (string, error) {
	var u struct {
		TenantID primitive.ObjectID `bson:"tenant_id"`
	}
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": uid}).Decode(&u); err != nil {
		return "", err
	}
	if u.TenantID.IsZero() {
		return uid.Hex(), nil
	}
	return u.TenantID.Hex(), nil
}

func (s *APITokenService) filterAllowedScopes(scope *CallerScope, callerPerms []string, isSuper bool, requested []string) ([]string, error) {
	permSet := map[string]bool{}
	for _, p := range callerPerms {
		permSet[p] = true
	}
	allowed := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, raw := range requested {
		key := strings.TrimSpace(raw)
		if key == "" || seen[key] {
			continue
		}
		def, ok := models.FindScope(key)
		if !ok {
			return nil, fmt.Errorf("unknown scope: %s", key)
		}
		// vendor_owner + is_super_admin can grant anything; everyone else
		// can only grant scopes whose underlying RBAC permission they
		// themselves hold. Mirrors RequirePermission's bypass logic so
		// the rule "a token can never exceed its creator" stays consistent.
		if scope.Role != "vendor_owner" && !isSuper && !permSet[def.Permission] {
			return nil, fmt.Errorf("you don't have the permission required to grant scope %q", key)
		}
		allowed = append(allowed, key)
		seen[key] = true
	}
	if len(allowed) == 0 {
		return nil, errors.New("at least one valid scope is required")
	}
	return allowed, nil
}

func ownerKindFromScope(scope *CallerScope) string {
	if scope == nil {
		return models.APITokenOwnerKindVendor
	}
	if scope.Role == "vendor_owner" {
		return models.APITokenOwnerKindOwner
	}
	return models.APITokenOwnerKindVendor
}

func ownerRoleFromKind(kind string) string {
	if kind == models.APITokenOwnerKindOwner {
		return "vendor_owner"
	}
	return "vendor_admin"
}

func splitPlaintext(raw string) (id, secret string, ok bool) {
	parts := strings.Split(strings.TrimSpace(raw), "_")
	// Expect btz_<env>_<id>_<secret>
	if len(parts) != 4 {
		return "", "", false
	}
	if parts[0] != tokenPrefix {
		return "", "", false
	}
	return parts[2], parts[3], true
}

// apiTokenRandomHex returns N random bytes hex-encoded. Local-named to avoid
// colliding with the WordPress service's helper of the same shape.
func apiTokenRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func cleanIPAllowlist(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ipAllowed accepts exact IPv4 / IPv6 strings or simple CIDR-style host parts.
// Full CIDR matching is intentionally minimal here; an operator who needs
// proper subnet math can add it without altering the public surface.
func ipAllowed(ip string, list []string) bool {
	if ip == "" {
		return false
	}
	for _, e := range list {
		if e == ip {
			return true
		}
		if strings.HasSuffix(e, ".0/24") {
			prefix := strings.TrimSuffix(e, ".0/24")
			if strings.HasPrefix(ip, prefix+".") {
				return true
			}
		}
	}
	return false
}
