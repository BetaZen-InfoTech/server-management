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

// HomePageService owns the public landing page that unauthenticated
// visitors see at `GET /`. Singleton in mongo `server_config` keyed on
// `_id: "home_page"`, parallel to BrandingService.
//
// Why a singleton: the home page is per-install, not per-tenant. The
// WHM owner edits one document; every visitor at `/` reads the same
// page. Disabled-by-default so a fresh install keeps the historical
// behaviour (root → /whm/login redirect) until the operator turns it
// on from WHM → Server Settings → Home Page.
//
// Branding (logo + favicon + panel name) is intentionally NOT
// duplicated here — the home page renderer pulls those from
// BrandingService so the home page, login pages, and topbar all stay
// in sync from one source of truth.
type HomePageService struct {
	db *mongo.Database
}

// homePageDoc is the mongo shape. Singleton row at `_id: "home_page"`.
type homePageDoc struct {
	ID                 string    `bson:"_id"`
	Enabled            bool      `bson:"enabled"`
	HeroTitle          string    `bson:"hero_title"`
	HeroSubtitle       string    `bson:"hero_subtitle"`
	BodyText           string    `bson:"body_text"`
	VendorLoginLabel   string    `bson:"vendor_login_label"`
	WHMLoginLabel      string    `bson:"whm_login_label"`
	ShowWHMLogin       bool      `bson:"show_whm_login"`
	FooterText         string    `bson:"footer_text"`
	SupportEmail       string    `bson:"support_email"`
	UpdatedAt          time.Time `bson:"updated_at"`
}

const homePageConfigID = "home_page"

// HomePageView is the read-side payload for both the public renderer
// and the WHM admin form. Defaults are baked in so a fresh install
// (no doc yet) returns a usable response without callers needing
// branching logic.
type HomePageView struct {
	Enabled          bool   `json:"enabled"`
	HeroTitle        string `json:"hero_title"`
	HeroSubtitle     string `json:"hero_subtitle"`
	BodyText         string `json:"body_text"`
	VendorLoginLabel string `json:"vendor_login_label"`
	WHMLoginLabel    string `json:"whm_login_label"`
	ShowWHMLogin     bool   `json:"show_whm_login"`
	FooterText       string `json:"footer_text"`
	SupportEmail     string `json:"support_email"`
}

// SaveHomePageRequest is the write payload. All fields are accepted
// verbatim and trimmed of surrounding whitespace; HTML is NOT
// sanitised here — the renderer html-escapes all of it before writing
// to the response, so an operator typing `<script>` lands as literal
// text on the page, not an exec.
type SaveHomePageRequest struct {
	Enabled          bool   `json:"enabled"`
	HeroTitle        string `json:"hero_title"`
	HeroSubtitle     string `json:"hero_subtitle"`
	BodyText         string `json:"body_text"`
	VendorLoginLabel string `json:"vendor_login_label"`
	WHMLoginLabel    string `json:"whm_login_label"`
	ShowWHMLogin     bool   `json:"show_whm_login"`
	FooterText       string `json:"footer_text"`
	SupportEmail     string `json:"support_email"`
}

// Defaults return the seed payload a fresh install renders before the
// operator has saved anything. Kept as a function (not a `var`) so the
// strings can include the year / panel name when we extend it later.
func defaultHomePage() *HomePageView {
	return &HomePageView{
		Enabled:          false,
		HeroTitle:        "Welcome",
		HeroSubtitle:     "Manage your hosting, domains, and apps from one panel.",
		BodyText:         "Sign in to your vendor account to manage domains, mailboxes, databases, and deployed applications.",
		VendorLoginLabel: "Vendor Login",
		WHMLoginLabel:    "Admin Login",
		ShowWHMLogin:     true,
		FooterText:       "",
		SupportEmail:     "",
	}
}

func NewHomePageService(db *mongo.Database) *HomePageService {
	return &HomePageService{db: db}
}

// Get returns the current home-page config, falling back to defaults
// when no document exists yet (fresh install). Defaults have
// Enabled=false so the historical root-redirect behaviour stays in
// place until the operator opts in.
func (s *HomePageService) Get(ctx context.Context) (*HomePageView, error) {
	var doc homePageDoc
	err := s.db.Collection(database.ColServerConfig).
		FindOne(ctx, bson.M{"_id": homePageConfigID}).
		Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return defaultHomePage(), nil
	}
	if err != nil {
		return nil, err
	}
	view := &HomePageView{
		Enabled:          doc.Enabled,
		HeroTitle:        doc.HeroTitle,
		HeroSubtitle:     doc.HeroSubtitle,
		BodyText:         doc.BodyText,
		VendorLoginLabel: doc.VendorLoginLabel,
		WHMLoginLabel:    doc.WHMLoginLabel,
		ShowWHMLogin:     doc.ShowWHMLogin,
		FooterText:       doc.FooterText,
		SupportEmail:     doc.SupportEmail,
	}
	// Fill in label defaults if the operator left them blank — empty
	// labels would render an unclickable button.
	if strings.TrimSpace(view.VendorLoginLabel) == "" {
		view.VendorLoginLabel = "Vendor Login"
	}
	if strings.TrimSpace(view.WHMLoginLabel) == "" {
		view.WHMLoginLabel = "Admin Login"
	}
	return view, nil
}

// Save upserts the home-page config. Validates length caps so a stray
// paste of a multi-MB blob can't bloat the singleton; everything else
// (including the HTML safety story) is the renderer's responsibility.
func (s *HomePageService) Save(ctx context.Context, req *SaveHomePageRequest) (*HomePageView, error) {
	hero := strings.TrimSpace(req.HeroTitle)
	if req.Enabled && hero == "" {
		return nil, errors.New("hero_title is required when the home page is enabled")
	}
	if len(hero) > 200 {
		return nil, errors.New("hero_title is too long (max 200 chars)")
	}
	if len(req.HeroSubtitle) > 400 {
		return nil, errors.New("hero_subtitle is too long (max 400 chars)")
	}
	// Body cap matches a typical landing-page blurb. Higher than the
	// branding doc cap (256 KB on raw images) but well under mongo's
	// 16 MB doc limit. If an operator legitimately hits this they
	// should be hosting the marketing site as a separate Deploy
	// Software project, not stuffing a novel into server_config.
	if len(req.BodyText) > 8000 {
		return nil, errors.New("body_text is too long (max 8000 chars)")
	}
	if len(req.VendorLoginLabel) > 60 {
		return nil, errors.New("vendor_login_label is too long (max 60 chars)")
	}
	if len(req.WHMLoginLabel) > 60 {
		return nil, errors.New("whm_login_label is too long (max 60 chars)")
	}
	if len(req.FooterText) > 400 {
		return nil, errors.New("footer_text is too long (max 400 chars)")
	}
	if len(req.SupportEmail) > 200 {
		return nil, errors.New("support_email is too long (max 200 chars)")
	}

	doc := homePageDoc{
		ID:               homePageConfigID,
		Enabled:          req.Enabled,
		HeroTitle:        hero,
		HeroSubtitle:     strings.TrimSpace(req.HeroSubtitle),
		BodyText:         req.BodyText,
		VendorLoginLabel: strings.TrimSpace(req.VendorLoginLabel),
		WHMLoginLabel:    strings.TrimSpace(req.WHMLoginLabel),
		ShowWHMLogin:     req.ShowWHMLogin,
		FooterText:       strings.TrimSpace(req.FooterText),
		SupportEmail:     strings.TrimSpace(req.SupportEmail),
		UpdatedAt:        time.Now(),
	}
	if _, err := s.db.Collection(database.ColServerConfig).UpdateOne(
		ctx,
		bson.M{"_id": homePageConfigID},
		bson.M{"$set": doc},
		options.Update().SetUpsert(true),
	); err != nil {
		return nil, err
	}
	return s.Get(ctx)
}
