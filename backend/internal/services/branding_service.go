package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// BrandingService owns the panel's whitelabel surface — the logo on the
// sidebar, the favicon in the browser tab, and the panel display name
// on the login page / top bar / outgoing emails. Singleton in mongo
// `server_config` keyed on `_id: "branding"`, parallel to PanelMailService.
//
// Why a singleton: branding is per-install, not per-tenant. A tenant
// can't override the panel chrome on their own — only the platform owner
// can. The service is read on every page load via the public
// `/api/v1/branding` endpoint (no auth — needed by index.html before
// any token exists), so reads must stay cheap and writes must stay
// rare.
//
// Image storage strategy: data: URLs (base64-inlined) stored in mongo,
// not on the filesystem. Tradeoff:
//
//	+ atomic — a single mongo doc is the whole branding config; no
//	  file/DB drift, no orphan files when the operator re-uploads
//	+ no nginx static-asset routing required (the panel's nginx
//	  template stays minimal)
//	+ trivial backup story (mongodump captures branding too)
//	- bigger payload over the wire than a regular img URL — capped
//	  at 256 KB per asset (logo OR favicon), which is generous for
//	  PNGs and well under mongo's 16 MB doc limit. A real CDN
//	  setup is a v3.1 upgrade if any operator hits the cap.
type BrandingService struct {
	db *mongo.Database
}

// brandingDoc is the mongo shape. Singleton row at `_id: "branding"`.
type brandingDoc struct {
	ID            string    `bson:"_id"`
	PanelName     string    `bson:"panel_name"`
	LogoDataURL   string    `bson:"logo_data_url"`
	FaviconDataURL string   `bson:"favicon_data_url"`
	UpdatedAt     time.Time `bson:"updated_at"`
}

const brandingConfigID = "branding"

// MaxBrandingAssetBytes is the per-image upload limit. 256 KB covers
// 512×512 PNGs comfortably; favicons are tiny so they never bind. The
// frontend rejects oversize uploads before POST so this is the
// belt-and-braces backstop.
const MaxBrandingAssetBytes = 256 * 1024

// BrandingView is what callers (panel UI + public index.html) read.
// Same shape as the mongo doc — there's nothing sensitive to mask.
// Defaults are baked in so a fresh install (no doc yet) returns a
// usable response without callers needing branching logic.
type BrandingView struct {
	PanelName      string `json:"panel_name"`
	LogoDataURL    string `json:"logo_data_url,omitempty"`
	FaviconDataURL string `json:"favicon_data_url,omitempty"`
}

// SaveBrandingRequest is the write payload. Empty string for either
// data URL means "clear this asset" — the operator removed their
// custom logo and wants the bundled default back. The frontend sends
// data: URLs ("data:image/png;base64,..."); we validate the prefix
// and length before persisting.
type SaveBrandingRequest struct {
	PanelName      string `json:"panel_name"`
	LogoDataURL    string `json:"logo_data_url"`
	FaviconDataURL string `json:"favicon_data_url"`
}

func NewBrandingService(db *mongo.Database) *BrandingService {
	return &BrandingService{db: db}
}

// Get returns the current branding config, falling back to defaults
// when no document exists yet (fresh install).
func (s *BrandingService) Get(ctx context.Context) (*BrandingView, error) {
	var doc brandingDoc
	err := s.db.Collection(database.ColServerConfig).
		FindOne(ctx, bson.M{"_id": brandingConfigID}).
		Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return &BrandingView{PanelName: "Betazen Server Panel"}, nil
	}
	if err != nil {
		return nil, err
	}
	view := &BrandingView{
		PanelName:      doc.PanelName,
		LogoDataURL:    doc.LogoDataURL,
		FaviconDataURL: doc.FaviconDataURL,
	}
	if strings.TrimSpace(view.PanelName) == "" {
		view.PanelName = "Betazen Server Panel"
	}
	return view, nil
}

// Save upserts the branding config. Validates panel name + data URL
// shape and size; returns a typed error for the handler to map to a
// 400 response.
func (s *BrandingService) Save(ctx context.Context, req *SaveBrandingRequest) (*BrandingView, error) {
	name := strings.TrimSpace(req.PanelName)
	if name == "" {
		return nil, errors.New("panel_name is required")
	}
	if len(name) > 80 {
		return nil, errors.New("panel_name is too long (max 80 chars)")
	}
	if err := validateDataURL(req.LogoDataURL, "logo_data_url"); err != nil {
		return nil, err
	}
	if err := validateDataURL(req.FaviconDataURL, "favicon_data_url"); err != nil {
		return nil, err
	}

	doc := brandingDoc{
		ID:             brandingConfigID,
		PanelName:      name,
		LogoDataURL:    req.LogoDataURL,
		FaviconDataURL: req.FaviconDataURL,
		UpdatedAt:      time.Now(),
	}
	if _, err := s.db.Collection(database.ColServerConfig).UpdateOne(
		ctx,
		bson.M{"_id": brandingConfigID},
		bson.M{"$set": doc},
		options.Update().SetUpsert(true),
	); err != nil {
		return nil, err
	}
	return s.Get(ctx)
}

// validateDataURL accepts the empty string (clear asset), or a
// `data:image/...;base64,...` URL whose total length fits under
// MaxBrandingAssetBytes. Other shapes (http URLs, raw base64, plain
// text) are rejected — the frontend always sends data URLs and we
// don't want to expand the trust surface to remote fetches.
func validateDataURL(s string, field string) error {
	if s == "" {
		return nil
	}
	if !strings.HasPrefix(s, "data:image/") {
		return errors.New(field + " must be a data: URL with an image MIME type")
	}
	if !strings.Contains(s, ";base64,") {
		return errors.New(field + " must be base64-encoded")
	}
	if len(s) > MaxBrandingAssetBytes {
		return errors.New(field + " is too large — keep images under 256 KB")
	}
	return nil
}
