package services

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// BIMIService manages the panel's mail logo (BIMI) — the brand image that
// supporting mail clients (Apple Mail, Fastmail, and Gmail with a VMC) render
// next to a domain's mail. It stores ONE shared SVG (server_config singleton,
// migrated like branding) served over HTTPS from the panel, and publishes the
// per-domain `default._bimi.<domain>` TXT record that points at it.
//
// Security note: the SVG is served from the panel's own origin, so an uploaded
// SVG carrying script would be an XSS vector. sanitizeBIMISVG rejects any active
// content (script / event handlers / external refs / raster / links) on upload,
// and the serving handler additionally sends a locked-down CSP + nosniff.
type BIMIService struct {
	db        *mongo.Database
	dns       *DNSService
	panelHost string // the panel's own domain, e.g. panel.betazeninfotech.com (cfg.Domain)
}

// NewBIMIService builds the service. dns is used to publish/unpublish the BIMI
// TXT record (provider-aware — works for PowerDNS and Cloudflare domains).
func NewBIMIService(db *mongo.Database, dns *DNSService, panelHost string) *BIMIService {
	return &BIMIService{db: db, dns: dns, panelHost: strings.TrimSpace(panelHost)}
}

const mailLogoConfigID = "mail_logo"

// MaxBIMISVGBytes is the hard cap. The BIMI spec recommends the SVG stay well
// under 32 KB; receivers reject larger. We match that ceiling.
const MaxBIMISVGBytes = 32 * 1024

type mailLogoDoc struct {
	ID        string    `bson:"_id"`
	SVG       string    `bson:"svg"`
	UpdatedAt time.Time `bson:"updated_at"`
}

// BIMIStatus is the per-domain publication state the UI + API read.
type BIMIStatus struct {
	Domain    string   `json:"domain"`
	LogoSet   bool     `json:"logo_set"`   // an SVG has been uploaded
	Published bool     `json:"published"`  // default._bimi TXT exists for this domain
	LogoURL   string   `json:"logo_url"`   // where the SVG is served
	Record    string   `json:"record,omitempty"` // the published BIMI TXT value
	Warnings  []string `json:"warnings,omitempty"`
}

var (
	// Active-content / disallowed constructs. BIMI SVG Tiny 1.2 PS forbids ALL
	// of these; they are also the XSS surface when the SVG is served from our
	// origin, so we reject rather than strip (clearer for the operator).
	reBIMIScript      = regexp.MustCompile(`(?i)<\s*script\b`)
	reBIMIForeign     = regexp.MustCompile(`(?i)<\s*foreignobject\b`)
	reBIMIImage       = regexp.MustCompile(`(?i)<\s*image\b`)      // raster embed — forbidden by BIMI
	reBIMIAnchor      = regexp.MustCompile(`(?i)<\s*a\b`)          // hyperlinks not allowed
	reBIMIEventAttr   = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)   // onload=, onclick=, ...
	reBIMIJSURI       = regexp.MustCompile(`(?i)javascript\s*:`)
	reBIMIExternalRef = regexp.MustCompile(`(?i)(?:xlink:)?href\s*=\s*["']?\s*(?:https?:)?//`)
	reBIMIExtEntity   = regexp.MustCompile(`(?i)<!doctype|<!entity`)
	reBIMIAnimate     = regexp.MustCompile(`(?i)<\s*(?:animate|animatetransform|animatemotion|set)\b`)
)

// sanitizeBIMISVG validates an uploaded SVG for both SAFETY (hard errors — never
// serve active content from our origin) and BIMI PS-PROFILE conformance (soft
// warnings — a logo that fails these still uploads but may not render at strict
// receivers). It returns the trimmed SVG text, a list of profile warnings, and a
// hard error when the input is unsafe/unusable.
func sanitizeBIMISVG(raw []byte) (string, []string, error) {
	if len(raw) == 0 {
		return "", nil, fmt.Errorf("empty file")
	}
	if len(raw) > MaxBIMISVGBytes {
		return "", nil, fmt.Errorf("SVG is %d bytes — BIMI requires under %d KB", len(raw), MaxBIMISVGBytes/1024)
	}
	svg := strings.TrimSpace(string(raw))
	low := strings.ToLower(svg)

	// Must actually be an SVG document.
	if !strings.Contains(low, "<svg") {
		return "", nil, fmt.Errorf("not an SVG file (no <svg> root element)")
	}
	// Reject anything before the root that isn't an XML declaration/comment —
	// stops HTML smuggling ahead of the <svg>.
	if i := strings.Index(low, "<svg"); i > 0 {
		head := strings.TrimSpace(low[:i])
		head = regexp.MustCompile(`(?s)<\?xml.*?\?>`).ReplaceAllString(head, "")
		head = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(head, "")
		if strings.TrimSpace(head) != "" {
			return "", nil, fmt.Errorf("unexpected content before the <svg> element")
		}
	}

	// --- HARD safety rejections ---
	switch {
	case reBIMIExtEntity.MatchString(low):
		return "", nil, fmt.Errorf("SVG must not contain a DOCTYPE or entity declaration")
	case reBIMIScript.MatchString(low):
		return "", nil, fmt.Errorf("SVG must not contain <script>")
	case reBIMIForeign.MatchString(low):
		return "", nil, fmt.Errorf("SVG must not contain <foreignObject>")
	case reBIMIImage.MatchString(low):
		return "", nil, fmt.Errorf("SVG must not embed raster images (<image>) — BIMI requires pure vector")
	case reBIMIAnchor.MatchString(low):
		return "", nil, fmt.Errorf("SVG must not contain hyperlinks (<a>)")
	case reBIMIEventAttr.MatchString(low):
		return "", nil, fmt.Errorf("SVG must not contain event-handler attributes (on*=)")
	case reBIMIJSURI.MatchString(low):
		return "", nil, fmt.Errorf("SVG must not contain javascript: URIs")
	case reBIMIExternalRef.MatchString(low):
		return "", nil, fmt.Errorf("SVG must not reference external resources (http(s):// hrefs)")
	case reBIMIAnimate.MatchString(low):
		return "", nil, fmt.Errorf("SVG must not contain animation elements")
	}

	// --- SOFT BIMI PS-profile conformance (warn, don't block) ---
	var warn []string
	if !strings.Contains(low, `baseprofile="tiny-ps"`) && !strings.Contains(low, `baseprofile='tiny-ps'`) {
		warn = append(warn, `missing baseProfile="tiny-ps" — required by the BIMI SVG Tiny 1.2 PS profile`)
	}
	if !regexp.MustCompile(`(?i)version\s*=\s*["']1\.2["']`).MatchString(low) {
		warn = append(warn, `missing version="1.2"`)
	}
	if !strings.Contains(low, "viewbox") {
		warn = append(warn, "missing a viewBox attribute (BIMI logos must be square, e.g. viewBox=\"0 0 512 512\")")
	}
	if !strings.Contains(low, "<title") {
		warn = append(warn, "missing a <title> element (BIMI recommends one for accessibility)")
	}
	if strings.Contains(low, "x=") || strings.Contains(low, "y=") {
		// Not authoritative (x/y may appear on inner elements) — informational only.
	}
	return svg, warn, nil
}

// GetLogoSVG returns the stored mail-logo SVG and whether one is set.
func (s *BIMIService) GetLogoSVG(ctx context.Context) (string, bool) {
	var doc mailLogoDoc
	if err := s.db.Collection(database.ColServerConfig).
		FindOne(ctx, bson.M{"_id": mailLogoConfigID}).Decode(&doc); err != nil {
		return "", false
	}
	if strings.TrimSpace(doc.SVG) == "" {
		return "", false
	}
	return doc.SVG, true
}

// SetLogo validates + stores the mail-logo SVG. Returns any PS-profile warnings.
func (s *BIMIService) SetLogo(ctx context.Context, raw []byte) ([]string, error) {
	svg, warn, err := sanitizeBIMISVG(raw)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Collection(database.ColServerConfig).UpdateOne(ctx,
		bson.M{"_id": mailLogoConfigID},
		bson.M{"$set": bson.M{"svg": svg, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return nil, err
	}
	return warn, nil
}

// DeleteLogo clears the stored mail logo. Published BIMI records still resolve
// to a now-empty logo URL; the operator should unpublish per domain too.
func (s *BIMIService) DeleteLogo(ctx context.Context) error {
	_, err := s.db.Collection(database.ColServerConfig).DeleteOne(ctx, bson.M{"_id": mailLogoConfigID})
	return err
}

// LogoURL is the public HTTPS URL the BIMI record points at. Served by the panel
// from the stored SVG. Falls back to a plain host string if panelHost is unset.
func (s *BIMIService) LogoURL() string {
	host := s.panelHost
	if host == "" {
		host = "localhost"
	}
	return "https://" + host + "/bimi/logo.svg"
}

// bimiRecordValue is the TXT value published at default._bimi.<domain>.
func (s *BIMIService) bimiRecordValue() string {
	return fmt.Sprintf("v=BIMI1; l=%s;", s.LogoURL())
}

// PublishBIMI publishes the default._bimi.<domain> TXT record pointing at the
// panel-served logo. Requires a logo to be set. Provider-aware via DNSService.
// AddRecord (PowerDNS or Cloudflare). Idempotent-ish: a duplicate value is a
// no-op-with-error from AddRecord, which we treat as already-published.
func (s *BIMIService) PublishBIMI(ctx context.Context, domain string) (*BIMIStatus, error) {
	domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(domain, ".")))
	if _, ok := s.GetLogoSVG(ctx); !ok {
		return nil, fmt.Errorf("no mail logo uploaded yet — upload an SVG first")
	}
	if s.dns == nil {
		return nil, fmt.Errorf("DNS service is not available")
	}
	val := s.bimiRecordValue()
	// Replace any existing record first so a changed logo URL / re-publish is clean.
	_ = s.dns.DeleteRecordByNameType(ctx, domain, "default._bimi", "TXT", "")
	_, err := s.dns.AddRecord(ctx, domain, &models.CreateRecordRequest{
		Type:  "TXT",
		Name:  "default._bimi",
		Value: val,
		TTL:   3600,
	})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return nil, fmt.Errorf("publish BIMI record: %w", err)
	}
	return s.Status(ctx, domain)
}

// UnpublishBIMI removes the default._bimi.<domain> TXT record.
func (s *BIMIService) UnpublishBIMI(ctx context.Context, domain string) (*BIMIStatus, error) {
	domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(domain, ".")))
	if s.dns == nil {
		return nil, fmt.Errorf("DNS service is not available")
	}
	if err := s.dns.DeleteRecordByNameType(ctx, domain, "default._bimi", "TXT", ""); err != nil {
		return nil, fmt.Errorf("unpublish BIMI record: %w", err)
	}
	return s.Status(ctx, domain)
}

// Status reports whether a logo is set and whether the domain has a published
// BIMI record (read from the mirrored dns_records so it's provider-agnostic).
func (s *BIMIService) Status(ctx context.Context, domain string) (*BIMIStatus, error) {
	domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(domain, ".")))
	_, logoSet := s.GetLogoSVG(ctx)
	st := &BIMIStatus{Domain: domain, LogoSet: logoSet, LogoURL: s.LogoURL()}

	var zone models.DNSZone
	if err := s.db.Collection(database.ColDNSZones).FindOne(ctx, bson.M{"domain": domain}).Decode(&zone); err == nil {
		var rec models.DNSRecord
		if err := s.db.Collection(database.ColDNSRecords).FindOne(ctx, bson.M{
			"zone_id": zone.ID, "type": "TXT", "name": "default._bimi",
		}).Decode(&rec); err == nil {
			st.Published = true
			st.Record = rec.Value
		}
	}
	if !logoSet {
		st.Warnings = append(st.Warnings, "no mail logo uploaded — upload an SVG on Server Settings first")
	}
	// BIMI needs an enforced DMARC policy (p=quarantine or p=reject). The panel's
	// default DMARC is p=none, which is NOT sufficient for receivers to show the
	// logo. Surface it as guidance.
	st.Warnings = append(st.Warnings, "BIMI shows the logo only when this domain's DMARC is p=quarantine or p=reject (the panel default is p=none), and Gmail additionally requires a Verified Mark Certificate (VMC).")
	return st, nil
}
