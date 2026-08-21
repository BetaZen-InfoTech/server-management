package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/cloudflare"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/crypto"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// CloudflareService owns the panel's centralized, account-level Cloudflare
// credentials + connection state, and (Increment 1) the read-only zone/record
// operations built on the pkg/cloudflare client. There is exactly ONE token
// for the whole panel (the design mandates a centralized credential with
// per-zone mapping), stored encrypted at rest exactly the way PanelMailService
// stores the SMTP password: AES-GCM under APP_ENCRYPTION_KEY, never echoed back
// to the browser.
//
// Everything here is READ-ONLY with respect to both Cloudflare and local DNS —
// listing zones/records and comparing local vs Cloudflare state. Write
// operations (create/update/delete) land in a later, separately-gated
// increment.
type CloudflareService struct {
	db     *mongo.Database
	encKey []byte
	http   *http.Client
	// apiBase overrides the Cloudflare API root (empty = real api.cloudflare.com).
	// Set from CLOUDFLARE_API_BASE for a CF-compatible endpoint or test mock.
	apiBase string
}

// SetAPIBase overrides the Cloudflare API root for every client this service
// builds. Empty keeps the real api.cloudflare.com. Called once in main.go.
func (s *CloudflareService) SetAPIBase(base string) { s.apiBase = base }

// newClient builds a Cloudflare client for a token, applying the shared HTTP
// client and the optional base-URL override.
func (s *CloudflareService) newClient(token string) *cloudflare.Client {
	opts := []cloudflare.Option{cloudflare.WithHTTPClient(s.http)}
	if strings.TrimSpace(s.apiBase) != "" {
		opts = append(opts, cloudflare.WithBaseURL(s.apiBase))
	}
	return cloudflare.New(token, opts...)
}

// Errors callers can branch on when Cloudflare isn't ready.
var (
	ErrCloudflareDisabled = errors.New("cloudflare integration is disabled")
	ErrCloudflareNoToken  = errors.New("cloudflare api token is not configured")
)

// cloudflareConfigID is the singleton _id in the shared server_config
// collection, mirroring panel_mail's "panel_mail" key.
const cloudflareConfigID = "cloudflare"

// cloudflareConfigDoc is the Mongo shape. Singleton keyed on _id:"cloudflare".
type cloudflareConfigDoc struct {
	ID               string     `bson:"_id"`
	AccountID        string     `bson:"account_id"`
	APITokenCipher   []byte     `bson:"api_token_cipher,omitempty"`
	TokenPreview     string     `bson:"token_preview,omitempty"` // already masked — safe to store/return
	Enabled          bool       `bson:"enabled"`
	AutoEnable       bool       `bson:"auto_enable"` // auto-connect+sync every NEW domain to Cloudflare
	DefaultProvider  string     `bson:"default_provider"` // "powerdns" (default) | "cloudflare"
	ConnectionStatus string     `bson:"connection_status,omitempty"` // "" | "ok" | "failed"
	LastTestAt       *time.Time `bson:"last_test_at,omitempty"`
	LastSyncAt       *time.Time `bson:"last_sync_at,omitempty"`
	LastError        string     `bson:"last_error,omitempty"`
	UpdatedAt        time.Time  `bson:"updated_at"`
}

// CloudflareConfigView is the UI-safe projection. The token is NEVER returned;
// the form shows has_token + a masked preview so a stolen JWT can't exfiltrate
// the credential.
type CloudflareConfigView struct {
	AccountID        string     `json:"account_id"`
	HasToken         bool       `json:"has_token"`
	TokenPreview     string     `json:"token_preview,omitempty"`
	Enabled          bool       `json:"enabled"`
	AutoEnable       bool       `json:"auto_enable"`
	DefaultProvider  string     `json:"default_provider"`
	ConnectionStatus string     `json:"connection_status,omitempty"`
	LastTestAt       *time.Time `json:"last_test_at,omitempty"`
	LastSyncAt       *time.Time `json:"last_sync_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	Configured       bool       `json:"configured"`
	// TestStatus / TestError are populated only on Test responses so the UI can
	// surface the real Cloudflare error ("Invalid API Token", etc.).
	TestStatus string `json:"test_status,omitempty"`
	TestError  string `json:"test_error,omitempty"`
}

// SaveCloudflareRequest is the write payload from the WHM settings form.
// APIToken is optional — empty means "keep the existing encrypted token" so an
// operator can toggle Enabled or edit AccountID without re-pasting the token.
type SaveCloudflareRequest struct {
	AccountID       string `json:"account_id"`
	APIToken        string `json:"api_token"`
	Enabled         bool   `json:"enabled"`
	AutoEnable      bool   `json:"auto_enable"` // auto-connect+sync every new domain
	DefaultProvider string `json:"default_provider" validate:"omitempty,oneof=powerdns cloudflare"`
}

func NewCloudflareService(db *mongo.Database, encKey []byte) *CloudflareService {
	return &CloudflareService{
		db:     db,
		encKey: encKey,
		http:   &http.Client{Timeout: 20 * time.Second},
	}
}

func (s *CloudflareService) col() *mongo.Collection {
	return s.db.Collection(database.ColServerConfig)
}

func (s *CloudflareService) load(ctx context.Context) (*cloudflareConfigDoc, error) {
	var doc cloudflareConfigDoc
	err := s.col().FindOne(ctx, bson.M{"_id": cloudflareConfigID}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// Token returns the decrypted Cloudflare API token for server-side API calls.
// Returns "" when unset or when the encryption key is unavailable. Consumed by
// the zone/record client and by TestConnection — NEVER exposed over HTTP.
func (s *CloudflareService) Token(ctx context.Context) (string, error) {
	doc, err := s.load(ctx)
	if err != nil || doc == nil {
		return "", err
	}
	if len(doc.APITokenCipher) == 0 || len(s.encKey) != 32 {
		return "", nil
	}
	plain, err := crypto.DecryptGCM(doc.APITokenCipher, s.encKey)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func viewFromDoc(doc *cloudflareConfigDoc) *CloudflareConfigView {
	if doc == nil {
		return &CloudflareConfigView{DefaultProvider: "powerdns"}
	}
	provider := doc.DefaultProvider
	if provider == "" {
		provider = "powerdns"
	}
	return &CloudflareConfigView{
		AccountID:        doc.AccountID,
		HasToken:         len(doc.APITokenCipher) > 0,
		TokenPreview:     doc.TokenPreview,
		Enabled:          doc.Enabled,
		AutoEnable:       doc.AutoEnable,
		DefaultProvider:  provider,
		ConnectionStatus: doc.ConnectionStatus,
		LastTestAt:       doc.LastTestAt,
		LastSyncAt:       doc.LastSyncAt,
		LastError:        doc.LastError,
		Configured:       len(doc.APITokenCipher) > 0 && strings.TrimSpace(doc.AccountID) != "",
	}
}

// IsEnabled reports whether the global Cloudflare integration is enabled.
func (s *CloudflareService) IsEnabled(ctx context.Context) bool {
	doc, err := s.load(ctx)
	if err != nil || doc == nil {
		return false
	}
	return doc.Enabled
}

// AutoEnableOn reports whether NEW domains should be auto-connected + synced to
// Cloudflare (global integration enabled AND the auto-enable toggle on).
func (s *CloudflareService) AutoEnableOn(ctx context.Context) bool {
	doc, err := s.load(ctx)
	if err != nil || doc == nil {
		return false
	}
	return doc.Enabled && doc.AutoEnable
}

// Get returns the UI-safe view of the current config.
func (s *CloudflareService) Get(ctx context.Context) (*CloudflareConfigView, error) {
	doc, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	return viewFromDoc(doc), nil
}

// Save upserts the config. Empty APIToken keeps the existing cipher text so an
// operator editing AccountID / Enabled doesn't have to re-paste the token.
func (s *CloudflareService) Save(ctx context.Context, req *SaveCloudflareRequest) (*CloudflareConfigView, error) {
	req.AccountID = strings.TrimSpace(req.AccountID)
	req.APIToken = strings.TrimSpace(req.APIToken)
	if req.DefaultProvider == "" {
		req.DefaultProvider = "powerdns"
	}

	set := bson.M{
		"account_id":       req.AccountID,
		"enabled":          req.Enabled,
		"auto_enable":      req.AutoEnable,
		"default_provider": req.DefaultProvider,
		"updated_at":       time.Now(),
	}
	if req.APIToken != "" {
		if len(s.encKey) != 32 {
			return nil, fmt.Errorf("encryption key unavailable; cannot store Cloudflare API token")
		}
		cipher, err := crypto.EncryptGCM([]byte(req.APIToken), s.encKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt Cloudflare API token: %w", err)
		}
		set["api_token_cipher"] = cipher
		set["token_preview"] = crypto.MaskToken(req.APIToken)
		// A freshly-set token invalidates any prior connection verdict.
		set["connection_status"] = ""
		set["last_error"] = ""
	}

	_, err := s.col().UpdateOne(ctx,
		bson.M{"_id": cloudflareConfigID},
		bson.M{"$set": set},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx)
}

// TestConnection verifies the stored token against Cloudflare's READ-ONLY
// token-verify endpoint and records the verdict on the config doc. It builds
// the client directly from the token (NOT via clientFor) so an operator can
// verify a token BEFORE flipping Enabled on. It changes nothing in Cloudflare.
func (s *CloudflareService) TestConnection(ctx context.Context) (*CloudflareConfigView, error) {
	token, err := s.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("read stored token: %w", err)
	}
	view, _ := s.Get(ctx)
	if view == nil {
		view = &CloudflareConfigView{DefaultProvider: "powerdns"}
	}
	if strings.TrimSpace(token) == "" {
		view.TestStatus = "failed"
		view.TestError = "no API token configured"
		return view, nil
	}

	client := s.newClient(token)
	status := "ok"
	testErr := ""
	if st, verr := client.VerifyToken(ctx); verr != nil {
		status = "failed"
		testErr = augmentTokenError(token, verr)
	} else if st != "active" {
		status = "failed"
		testErr = "token status: " + st
	}

	now := time.Now()
	_, _ = s.col().UpdateOne(ctx,
		bson.M{"_id": cloudflareConfigID},
		bson.M{"$set": bson.M{
			"connection_status": status,
			"last_test_at":      now,
			"last_error":        testErr,
		}},
		options.Update().SetUpsert(true),
	)

	view.ConnectionStatus = status
	view.LastTestAt = &now
	view.LastError = testErr
	view.TestStatus = status
	view.TestError = testErr
	return view, nil
}

// augmentTokenError turns Cloudflare's terse token-verify failures into
// actionable guidance. The most common operator mistake is pasting an R2
// storage token (S3 credentials, prefixed "cfat_") or an account-scoped token
// instead of a *user* API token that carries Zone permissions — Cloudflare
// rejects those at /user/tokens/verify with code 1000 ("Invalid API Token").
// code 6003 ("Invalid request headers") instead means the token/header is
// malformed, usually stray whitespace introduced on paste.
func augmentTokenError(token string, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.HasPrefix(strings.TrimSpace(token), "cfat_"),
		strings.Contains(lower, "code 1000"),
		strings.Contains(lower, "invalid api token"):
		return msg + " — this looks like an R2/storage or account-scoped token, " +
			"not a DNS API token. Create one at dash.cloudflare.com/profile/api-tokens " +
			"with permissions Zone · DNS · Edit and Zone · Zone · Read (the \"Edit zone " +
			"DNS\" template), then paste that token."
	case strings.Contains(lower, "code 6003"),
		strings.Contains(lower, "invalid request headers"):
		return msg + " — the token looks malformed; re-copy it and check for stray " +
			"spaces or line breaks."
	default:
		return msg
	}
}

// ReencryptForTransfer translates the Cloudflare api_token_cipher from the
// SOURCE server's APP_ENCRYPTION_KEY into THIS (destination) server's, so a
// server-to-server migration can carry the token in a form the destination can
// actually decrypt. Without it, the source cipher lands verbatim and DecryptGCM
// on the destination returns garbage — the panel would have Cloudflare
// "enabled" but every API call would fail auth, so a post-migration IP change
// would silently not update Cloudflare. Mirrors PanelMailService.ReencryptForTransfer.
//
// Returns (nil, nil) when re-encryption isn't possible (empty cipher, empty
// source key, or decryption with the source key fails) so the caller drops the
// cipher and prompts the operator to re-enter the token rather than stamping
// garbage.
func (s *CloudflareService) ReencryptForTransfer(srcCipher []byte, srcEncKeyRaw string) ([]byte, error) {
	if len(srcCipher) == 0 || strings.TrimSpace(srcEncKeyRaw) == "" {
		return nil, nil
	}
	if len(s.encKey) != 32 {
		return nil, fmt.Errorf("destination encryption key unavailable")
	}
	srcKey, err := crypto.LoadKey(srcEncKeyRaw)
	if err != nil {
		return nil, fmt.Errorf("load source key: %w", err)
	}
	plain, err := crypto.DecryptGCM(srcCipher, srcKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt with source key: %w", err)
	}
	return crypto.EncryptGCM(plain, s.encKey)
}

// clientFor builds an API client from the stored token, but only when the
// integration is Enabled and a token exists. Read/sync operations go through
// this so a disabled integration makes zero Cloudflare calls.
func (s *CloudflareService) clientFor(ctx context.Context) (*cloudflare.Client, error) {
	doc, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if doc == nil || !doc.Enabled {
		return nil, ErrCloudflareDisabled
	}
	token, err := s.Token(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, ErrCloudflareNoToken
	}
	return s.newClient(token), nil
}

// ListZones returns every zone visible to the account token (read-only).
func (s *CloudflareService) ListZones(ctx context.Context) ([]cloudflare.Zone, error) {
	client, err := s.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	return client.ListZones(ctx)
}

// FindZone returns the Cloudflare zone for a domain, or nil if the account has
// no such zone (read-only).
func (s *CloudflareService) FindZone(ctx context.Context, domain string) (*cloudflare.Zone, error) {
	client, err := s.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	return client.FindZoneByName(ctx, normalizeDomain(domain))
}

// ListRecords returns a domain's Cloudflare zone + its DNS records (read-only).
// The zone is nil (and records empty) when the account has no zone for it.
func (s *CloudflareService) ListRecords(ctx context.Context, domain string) (*cloudflare.Zone, []cloudflare.DNSRecord, error) {
	client, err := s.clientFor(ctx)
	if err != nil {
		return nil, nil, err
	}
	zone, err := client.FindZoneByName(ctx, normalizeDomain(domain))
	if err != nil {
		return nil, nil, err
	}
	if zone == nil {
		return nil, nil, nil
	}
	recs, err := client.ListDNSRecords(ctx, zone.ID)
	if err != nil {
		return zone, nil, err
	}
	return zone, recs, nil
}

// --- Reconciliation compare (read-only) -------------------------------------

// CompareCounts is the roll-up of a compare.
type CompareCounts struct {
	Matched   int `json:"matched"`
	LocalOnly int `json:"local_only"`
	CFOnly    int `json:"cf_only"`
	Conflict  int `json:"conflict"`
}

// CompareRow is one record-level comparison. Status ∈
// matched | local_only | cf_only | conflict.
type CompareRow struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	LocalValue string `json:"local_value,omitempty"`
	CFValue    string `json:"cf_value,omitempty"`
	LocalTTL   int    `json:"local_ttl,omitempty"`
	CFTTL      int    `json:"cf_ttl,omitempty"`
	Proxied    bool   `json:"proxied,omitempty"`
	CFRecordID string `json:"cf_record_id,omitempty"`
}

// CompareResult is the full read-only reconciliation for one domain.
type CompareResult struct {
	Domain        string        `json:"domain"`
	ZoneFound     bool          `json:"zone_found"`
	CFZoneID      string        `json:"cf_zone_id,omitempty"`
	CFStatus      string        `json:"cf_status,omitempty"`
	CFNameservers []string      `json:"cf_nameservers,omitempty"`
	Counts        CompareCounts `json:"counts"`
	Rows          []CompareRow  `json:"rows"`
	Note          string        `json:"note,omitempty"`
}

// Compare classifies each DNS record as matched / local_only / cf_only /
// conflict between the panel's local zone (Mongo) and Cloudflare. It is purely
// informational — nothing is written to either side. Value comparison is
// best-effort (case-insensitive, trailing-dot-insensitive, priority-aware for
// MX/SRV); a formatting-only difference may occasionally surface as a conflict.
func (s *CloudflareService) Compare(ctx context.Context, domain string) (*CompareResult, error) {
	domain = normalizeDomain(domain)
	client, err := s.clientFor(ctx)
	if err != nil {
		return nil, err
	}

	localRecs, err := s.localRecords(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("load local records: %w", err)
	}

	zone, err := client.FindZoneByName(ctx, domain)
	if err != nil {
		return nil, err
	}

	res := &CompareResult{Domain: domain}
	if zone == nil {
		res.ZoneFound = false
		res.Note = "domain has no zone in Cloudflare; showing local records only"
		for _, r := range localRecs {
			res.Rows = append(res.Rows, CompareRow{
				Type:       strings.ToUpper(r.Type),
				Name:       normalizeName(r.Name, domain),
				Status:     "local_only",
				LocalValue: normalizeValue(r.Type, r.Value, r.Priority),
				LocalTTL:   r.TTL,
			})
			res.Counts.LocalOnly++
		}
		sortRows(res.Rows)
		return res, nil
	}

	res.ZoneFound = true
	res.CFZoneID = zone.ID
	res.CFStatus = zone.Status
	res.CFNameservers = zone.NameServers

	cfRecs, err := client.ListDNSRecords(ctx, zone.ID)
	if err != nil {
		return nil, err
	}

	type recKey struct{ typ, name string }
	type localVal struct{ ttl int }
	type cfVal struct {
		id      string
		ttl     int
		proxied bool
	}

	localMap := map[recKey]map[string]localVal{}
	for _, r := range localRecs {
		k := recKey{strings.ToUpper(r.Type), normalizeName(r.Name, domain)}
		v := normalizeValue(r.Type, r.Value, r.Priority)
		if localMap[k] == nil {
			localMap[k] = map[string]localVal{}
		}
		localMap[k][v] = localVal{ttl: r.TTL}
	}
	cfMap := map[recKey]map[string]cfVal{}
	for _, r := range cfRecs {
		k := recKey{strings.ToUpper(r.Type), normalizeName(r.Name, domain)}
		v := normalizeValue(r.Type, r.Content, r.Priority)
		if cfMap[k] == nil {
			cfMap[k] = map[string]cfVal{}
		}
		cfMap[k][v] = cfVal{id: r.ID, ttl: r.TTL, proxied: r.Proxied}
	}

	// Union of keys, deterministic order.
	keySet := map[recKey]struct{}{}
	for k := range localMap {
		keySet[k] = struct{}{}
	}
	for k := range cfMap {
		keySet[k] = struct{}{}
	}
	keys := make([]recKey, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].typ != keys[j].typ {
			return keys[i].typ < keys[j].typ
		}
		return keys[i].name < keys[j].name
	})

	for _, k := range keys {
		lv := localMap[k]
		cv := cfMap[k]

		// Matched values (present, identical, on both sides).
		matched := make([]string, 0)
		for val := range lv {
			if _, ok := cv[val]; ok {
				matched = append(matched, val)
			}
		}
		sort.Strings(matched)
		for _, val := range matched {
			res.Rows = append(res.Rows, CompareRow{
				Type: k.typ, Name: k.name, Status: "matched",
				LocalValue: val, CFValue: val,
				LocalTTL: lv[val].ttl, CFTTL: cv[val].ttl,
				Proxied: cv[val].proxied, CFRecordID: cv[val].id,
			})
			res.Counts.Matched++
		}

		localExtra := sortedExtras(lv, cv)
		cfExtra := sortedExtras(cv, lv)

		// When a (type,name) rrset has leftovers on BOTH sides, treat the
		// pairs as changed records (conflict) rather than unrelated
		// local_only + cf_only. Leftovers beyond the pairing become
		// one-sided rows.
		paired := 0
		if len(localExtra) > 0 && len(cfExtra) > 0 {
			paired = len(localExtra)
			if len(cfExtra) < paired {
				paired = len(cfExtra)
			}
			for i := 0; i < paired; i++ {
				lval := localExtra[i]
				cval := cfExtra[i]
				res.Rows = append(res.Rows, CompareRow{
					Type: k.typ, Name: k.name, Status: "conflict",
					LocalValue: lval, CFValue: cval,
					LocalTTL: lv[lval].ttl, CFTTL: cv[cval].ttl,
					Proxied: cv[cval].proxied, CFRecordID: cv[cval].id,
				})
				res.Counts.Conflict++
			}
		}
		for i := paired; i < len(localExtra); i++ {
			lval := localExtra[i]
			res.Rows = append(res.Rows, CompareRow{
				Type: k.typ, Name: k.name, Status: "local_only",
				LocalValue: lval, LocalTTL: lv[lval].ttl,
			})
			res.Counts.LocalOnly++
		}
		for i := paired; i < len(cfExtra); i++ {
			cval := cfExtra[i]
			res.Rows = append(res.Rows, CompareRow{
				Type: k.typ, Name: k.name, Status: "cf_only",
				CFValue: cval, CFTTL: cv[cval].ttl,
				Proxied: cv[cval].proxied, CFRecordID: cv[cval].id,
			})
			res.Counts.CFOnly++
		}
	}

	sortRows(res.Rows)
	return res, nil
}

// localRecords loads the panel's local DNS records for a domain straight from
// Mongo (dns_zones → dns_records), avoiding any pdnsutil shell-out so the
// compare is a pure read.
func (s *CloudflareService) localRecords(ctx context.Context, domain string) ([]models.DNSRecord, error) {
	var zone models.DNSZone
	err := s.db.Collection(database.ColDNSZones).FindOne(ctx, bson.M{"domain": domain}).Decode(&zone)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cur, err := s.db.Collection(database.ColDNSRecords).Find(ctx, bson.M{"zone_id": zone.ID})
	if err != nil {
		return nil, err
	}
	var recs []models.DNSRecord
	if err := cur.All(ctx, &recs); err != nil {
		return nil, err
	}
	return recs, nil
}

// --- normalization helpers --------------------------------------------------

func normalizeDomain(d string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), "."))
}

// normalizeName returns a comparable FQDN for a record name. Local names are
// relative ("@", "www", "_dmarc"); Cloudflare names are already FQDNs. Both
// collapse to a lower-case, trailing-dot-free FQDN.
func normalizeName(name, domain string) string {
	n := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if n == "" || n == "@" {
		return domain
	}
	if n == domain || strings.HasSuffix(n, "."+domain) {
		return n
	}
	return n + "." + domain
}

// normalizeValue returns a comparable record value: lower-case, trailing-dot
// free, and (for MX/SRV) prefixed with the priority so a value carried in a
// separate Priority field matches one embedded in the string.
func normalizeValue(typ, value string, priority *int) string {
	v := strings.TrimSuffix(strings.TrimSpace(strings.ToLower(value)), ".")
	t := strings.ToUpper(typ)
	if t == "TXT" {
		// PowerDNS and Cloudflare quote TXT differently (surrounding double
		// quotes; long values split into 255-char chunks joined by `" "`).
		// Normalize both to the bare string so an existing SPF/DKIM/DMARC
		// record MATCHES instead of being seen as new (which would make the
		// sync create a duplicate). Only affects matching, not stored content.
		return stripTXTQuoting(v)
	}
	if t == "MX" || t == "SRV" {
		fields := strings.Fields(v)
		pri := ""
		if priority != nil {
			pri = strconv.Itoa(*priority)
		}
		if len(fields) > 1 {
			if _, err := strconv.Atoi(fields[0]); err == nil {
				if pri == "" {
					pri = fields[0]
				}
				v = strings.Join(fields[1:], " ")
			}
		}
		if pri != "" {
			return pri + " " + v
		}
	}
	return v
}

// stripTXTQuoting reduces a TXT value to its bare string: it removes the
// `" "` joins PowerDNS/Cloudflare insert between 255-char chunks and any
// surrounding double quotes. Used both for match-key normalization and for
// building the content sent to Cloudflare (which quotes TXT itself), so a
// record that already exists is recognized instead of duplicated.
func stripTXTQuoting(s string) string {
	s = strings.ReplaceAll(s, `" "`, "")
	s = strings.TrimPrefix(s, `"`)
	s = strings.TrimSuffix(s, `"`)
	return s
}

// sortedExtras returns the keys of `a` not present in `b`, sorted.
func sortedExtras[T any, U any](a map[string]T, b map[string]U) []string {
	out := make([]string, 0)
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func sortRows(rows []CompareRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Type != rows[j].Type {
			return rows[i].Type < rows[j].Type
		}
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Status < rows[j].Status
	})
}
