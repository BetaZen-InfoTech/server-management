// Package services — GuestLinkService mints + verifies the one-time,
// time-limited, browser-locked guest links described in models/guest_link.go.
//
// Security posture mirrors APITokenService + the OTP flow:
//   - Token format: gst_<env>_<id>_<secret>. Only the id is stored visible;
//     the secret half is bcrypt-hashed before insert and verified constant-time.
//   - First open atomically binds the link to one browser (sha256 of an
//     HttpOnly cookie token) and starts a 30-minute window. Any other browser
//     is refused. A second open from the SAME browser within the window is
//     allowed (refresh / multiple actions).
//   - The guest session credential is a short-lived role="guest" JWT in an
//     HttpOnly cookie; authorization data (domain, type, limits, window,
//     binding) is reloaded from this row on every request so revoke is instant
//     and the JWT carries no trusted authz payload.
package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/jwt"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/password"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Cookie names for the guest session. Exported so the handler (set) and the
// middleware (read) agree without re-declaring.
const (
	GuestSessionCookieName = "bz_guest_sess" // the role="guest" JWT
	GuestBindCookieName    = "bz_guest_bind" // the first-open browser binding token
)

const (
	guestTokenPrefix    = "gst"
	guestIDBytes        = 6
	guestSecretBytes    = 32
	guestWindow         = 30 * time.Minute // access window after first open
	guestRedeemDeadline = 24 * time.Hour   // must be first-opened within this
)

// Sentinel errors so the handler can map to friendly messages without leaking
// which condition failed (all surface as a generic "link unavailable").
var (
	ErrGuestLinkInvalid     = errors.New("guest link invalid")
	ErrGuestLinkExpired     = errors.New("guest link expired")
	ErrGuestLinkOtherBrowser = errors.New("guest link already opened in another browser")
)

type GuestLinkService struct {
	db        *mongo.Database
	env       string
	jwtSecret string
}

func NewGuestLinkService(db *mongo.Database, env, jwtSecret string) *GuestLinkService {
	if strings.EqualFold(env, "production") || strings.EqualFold(env, "prod") {
		env = "prod"
	} else {
		env = "dev"
	}
	s := &GuestLinkService{db: db, env: env, jwtSecret: jwtSecret}
	s.ensureIndexes(context.Background())
	return s
}

func (s *GuestLinkService) ensureIndexes(ctx context.Context) {
	col := s.db.Collection(database.ColGuestLinks)
	_, _ = col.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "link_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "expires_at", Value: 1}}},
		{Keys: bson.D{{Key: "window_expires_at", Value: 1}}},
	})
}

// Mint creates a guest link for one domain. The minting token's scope fences
// it to its own tenant; the domain must exist in the panel. Main-vs-subdomain
// is decided here (frozen on the row) so a later zone change can't flip it.
func (s *GuestLinkService) Mint(ctx context.Context, scope *CallerScope, req models.MintGuestLinkRequest) (*models.IssuedGuestLink, error) {
	if scope == nil {
		return nil, errors.New("guest link: caller scope required")
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" {
		return nil, errors.New("guest link: domain is required")
	}

	// The domain must exist and belong to the caller's tenant. AssertOwnsDomain
	// is a no-op for vendor_owner, so also confirm the row exists + grab owner.
	if err := scope.AssertOwnsDomain(ctx, s.db, domain); err != nil {
		return nil, fmt.Errorf("guest link: %w", err)
	}
	var domRow struct {
		User string `bson:"user"`
	}
	if err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domain}).Decode(&domRow); err != nil {
		return nil, fmt.Errorf("guest link: domain %q not found", domain)
	}
	if strings.TrimSpace(domRow.User) == "" {
		return nil, fmt.Errorf("guest link: domain %q has no owner", domain)
	}

	// Resolve the owning user + tenant so the guest session runs tenant-scoped.
	ownerUserHex, ownerTenantHex, err := s.resolveOwnerScope(ctx, domRow.User)
	if err != nil {
		return nil, fmt.Errorf("guest link: resolve owner: %w", err)
	}

	// Classify: a parent zone in the panel ⇒ subdomain ⇒ email-only.
	linkType := models.GuestLinkTypeEmailDNS
	if parent := findParentDomain(ctx, s.db, domain); parent != "" {
		linkType = models.GuestLinkTypeEmail
	}

	// Limit defaults — accept what the integrator sent, fill sane floors.
	maxMb := req.MaxMailboxes
	if maxMb <= 0 {
		maxMb = 5
	}
	quota := req.DefaultQuotaMB
	if quota <= 0 {
		quota = 1024
	}
	sendPerHr := req.DefaultSendPerHour
	if sendPerHr <= 0 {
		sendPerHr = 200
	}

	linkID, err := apiTokenRandomHex(guestIDBytes)
	if err != nil {
		return nil, fmt.Errorf("guest link: id gen: %w", err)
	}
	secret, err := apiTokenRandomHex(guestSecretBytes)
	if err != nil {
		return nil, fmt.Errorf("guest link: secret gen: %w", err)
	}
	hash, err := password.Hash(secret)
	if err != nil {
		return nil, fmt.Errorf("guest link: hash: %w", err)
	}

	now := time.Now()
	link := &models.GuestLink{
		LinkID:             linkID,
		SecretHash:         hash,
		Domain:             domain,
		LinkType:           linkType,
		MaxMailboxes:       maxMb,
		DefaultQuotaMB:     quota,
		DefaultSendPerHour: sendPerHr,
		OwnerUserHex:       ownerUserHex,
		OwnerTenantHex:     ownerTenantHex,
		// Always a tenant-scoped role for the guest so CallerScope actively
		// enforces ownership as a second layer behind handler domain-forcing.
		OwnerRole: "vendor_admin",
		Status:    models.GuestLinkStatusPending,
		CreatedAt: now,
		ExpiresAt: now.Add(guestRedeemDeadline),
	}
	res, err := s.db.Collection(database.ColGuestLinks).InsertOne(ctx, link)
	if err != nil {
		return nil, fmt.Errorf("guest link: insert: %w", err)
	}
	link.ID = res.InsertedID.(primitive.ObjectID)

	plaintext := fmt.Sprintf("%s_%s_%s_%s", guestTokenPrefix, s.env, linkID, secret)
	return &models.IssuedGuestLink{
		LinkType:  linkType,
		Domain:    domain,
		ExpiresAt: link.ExpiresAt,
		Token:     plaintext,
	}, nil
}

// Verify looks up a link by its token and constant-time-checks the secret.
// Returns ErrGuestLinkInvalid / ErrGuestLinkExpired without distinguishing.
func (s *GuestLinkService) Verify(ctx context.Context, raw string) (*models.GuestLink, error) {
	id, secret, ok := splitGuestToken(raw)
	if !ok {
		return nil, ErrGuestLinkInvalid
	}
	var link models.GuestLink
	if err := s.db.Collection(database.ColGuestLinks).FindOne(ctx, bson.M{"link_id": id}).Decode(&link); err != nil {
		return nil, ErrGuestLinkInvalid
	}
	if link.Status == models.GuestLinkStatusRevoked || link.Status == models.GuestLinkStatusUsedExpired {
		return nil, ErrGuestLinkExpired
	}
	if link.ExpiresAt.Before(time.Now()) {
		return nil, ErrGuestLinkExpired
	}
	if !password.Verify(secret, link.SecretHash) {
		return nil, ErrGuestLinkInvalid
	}
	return &link, nil
}

// Redeem performs the first-open binding (atomic) or re-issues for the same
// browser within a live window. Returns the (possibly updated) link plus the
// browser-bind token the handler should set as the cookie (a fresh one on
// first open, the unchanged existing cookie on re-open).
func (s *GuestLinkService) Redeem(ctx context.Context, link *models.GuestLink, existingBind, ua, ip string) (*models.GuestLink, string, error) {
	now := time.Now()
	if link.ExpiresAt.Before(now) {
		return nil, "", ErrGuestLinkExpired
	}
	col := s.db.Collection(database.ColGuestLinks)

	// First-open attempt: only one caller can flip redeemed:false → true.
	newBind, err := apiTokenRandomHex(32)
	if err != nil {
		return nil, "", err
	}
	window := now.Add(guestWindow)
	after := options.After
	res := col.FindOneAndUpdate(ctx,
		bson.M{"_id": link.ID, "redeemed": false, "status": models.GuestLinkStatusPending},
		bson.M{"$set": bson.M{
			"redeemed":          true,
			"redeemed_at":       now,
			"window_expires_at": window,
			"binding_hash":      sha256Hex(newBind),
			"ua_hash":           sha256Hex(ua),
			"redeem_ip":         ip,
			"status":            models.GuestLinkStatusActive,
		}},
		&options.FindOneAndUpdateOptions{ReturnDocument: &after},
	)
	var updated models.GuestLink
	if err := res.Decode(&updated); err == nil {
		return &updated, newBind, nil // first open — bind to this browser
	}

	// Already redeemed — same-browser re-open only.
	var cur models.GuestLink
	if err := col.FindOne(ctx, bson.M{"_id": link.ID}).Decode(&cur); err != nil {
		return nil, "", ErrGuestLinkInvalid
	}
	if cur.Status != models.GuestLinkStatusActive || cur.WindowExpiresAt == nil || cur.WindowExpiresAt.Before(now) {
		return nil, "", ErrGuestLinkExpired
	}
	if existingBind == "" || sha256Hex(existingBind) != cur.BindingHash {
		return nil, "", ErrGuestLinkOtherBrowser
	}
	return &cur, existingBind, nil
}

// IssueSessionToken mints the role="guest" JWT carried in the session cookie.
// Expiry tracks the live window so the cookie dies with the session.
func (s *GuestLinkService) IssueSessionToken(link *models.GuestLink) (string, error) {
	if link.WindowExpiresAt == nil {
		return "", ErrGuestLinkExpired
	}
	ttl := time.Until(*link.WindowExpiresAt)
	if ttl <= 0 {
		return "", ErrGuestLinkExpired
	}
	return jwt.GenerateAccessTokenFull(s.jwtSecret, ttl, link.ID.Hex(), "", "guest", link.OwnerTenantHex, []string{}, false, "")
}

// LoadForSession is the per-request middleware reload: it re-checks status +
// window + browser binding against the live row, so a revoked/expired link or
// a mismatched browser is rejected immediately regardless of JWT expiry.
func (s *GuestLinkService) LoadForSession(ctx context.Context, linkID, bindCookie string) (*models.GuestLink, error) {
	oid, err := primitive.ObjectIDFromHex(linkID)
	if err != nil {
		return nil, ErrGuestLinkInvalid
	}
	var link models.GuestLink
	if err := s.db.Collection(database.ColGuestLinks).FindOne(ctx, bson.M{"_id": oid}).Decode(&link); err != nil {
		return nil, ErrGuestLinkInvalid
	}
	if link.Status != models.GuestLinkStatusActive {
		return nil, ErrGuestLinkExpired
	}
	if link.WindowExpiresAt == nil || link.WindowExpiresAt.Before(time.Now()) {
		return nil, ErrGuestLinkExpired
	}
	if bindCookie == "" || sha256Hex(bindCookie) != link.BindingHash {
		return nil, ErrGuestLinkOtherBrowser
	}
	return &link, nil
}

// Scope rebuilds the tenant-scoped CallerScope for a guest session.
func (s *GuestLinkService) Scope(link *models.GuestLink) *CallerScope {
	return &CallerScope{Role: link.OwnerRole, TenantHex: link.OwnerTenantHex, UserHex: link.OwnerUserHex}
}

// SweepExpired flips pending/active rows past their redeem deadline or window
// to used_expired. Housekeeping only — the JWT/window enforce the real cutoff.
func (s *GuestLinkService) SweepExpired(ctx context.Context) int64 {
	now := time.Now()
	res, err := s.db.Collection(database.ColGuestLinks).UpdateMany(ctx,
		bson.M{
			"status": bson.M{"$in": []string{models.GuestLinkStatusPending, models.GuestLinkStatusActive}},
			"$or": bson.A{
				bson.M{"expires_at": bson.M{"$lt": now}},
				bson.M{"window_expires_at": bson.M{"$lt": now}},
			},
		},
		bson.M{"$set": bson.M{"status": models.GuestLinkStatusUsedExpired}})
	if err != nil {
		return 0
	}
	return res.ModifiedCount
}

// resolveOwnerScope returns the (userHex, tenantHex) for a linux username.
func (s *GuestLinkService) resolveOwnerScope(ctx context.Context, username string) (string, string, error) {
	var u models.User
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"username": username}).Decode(&u); err != nil {
		return "", "", err
	}
	tenant := u.TenantID.Hex()
	if u.TenantID.IsZero() {
		tenant = u.ID.Hex()
	}
	return u.ID.Hex(), tenant, nil
}

func splitGuestToken(raw string) (id, secret string, ok bool) {
	parts := strings.Split(strings.TrimSpace(raw), "_")
	if len(parts) != 4 || parts[0] != guestTokenPrefix {
		return "", "", false
	}
	return parts[2], parts[3], true
}
