package handlers

import (
	"context"
	"html/template"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/gofiber/fiber/v2"
)

// These tests cover the GET / dispatch logic end-to-end without a real
// Mongo: we wire a tiny Fiber app that mirrors cmd/server/main.go's root
// handler, then assert each of the four paths behaves correctly.
//
// The four paths:
//
//   1. preview=1 + page disabled  → render with DRAFT banner
//   2. preview=1 + page enabled   → render WITHOUT DRAFT banner
//   3. no preview + disabled      → 30x redirect to /whm/
//   4. no preview + enabled       → render WITHOUT DRAFT banner
//
// We bypass the OptionalAuth middleware here — the role-redirect path
// is straightforward switch-on-string logic, and adding it would require
// also stubbing the JWT validator. The render-vs-redirect dispatch IS
// the production-shaped logic worth pinning down.

// stubHomeService implements just enough of the HomePageService surface
// for the renderer. We can't use the real service (it needs a Mongo
// db); we can't use a mock library (this repo doesn't pull one in).
// A small concrete stub is the cheapest way to drive the test.
type stubHomeService struct {
	view *services.HomePageView
}

func (s *stubHomeService) Get(ctx context.Context) (*services.HomePageView, error) {
	return s.view, nil
}

type stubBrandingService struct {
	view *services.BrandingView
}

func (s *stubBrandingService) Get(ctx context.Context) (*services.BrandingView, error) {
	return s.view, nil
}

// renderHomePageStub mirrors RenderHomePage but takes our minimal
// interfaces so the test can drive it without a real Mongo. Same
// shape as the real function — we re-derive the data the same way
// and execute the same compiled template.
func renderHomePageStub(c *fiber.Ctx, h *stubHomeService, b *stubBrandingService) error {
	home, _ := h.Get(c.UserContext())
	brand, _ := b.Get(c.UserContext())
	preview := c.Query("preview") == "1"
	data := homePageRenderData{
		PanelName:        brand.PanelName,
		LogoDataURL:      template.URL(brand.LogoDataURL),
		FaviconDataURL:   template.URL(brand.FaviconDataURL),
		HeroTitle:        home.HeroTitle,
		HeroSubtitle:     home.HeroSubtitle,
		BodyParagraphs:   splitParagraphs(home.BodyText),
		VendorLoginLabel: home.VendorLoginLabel,
		WHMLoginLabel:    home.WHMLoginLabel,
		ShowWHMLogin:     home.ShowWHMLogin,
		FooterText:       home.FooterText,
		SupportEmail:     home.SupportEmail,
		DraftBanner:      preview && !home.Enabled,
	}
	c.Set("Content-Type", "text/html; charset=utf-8")
	if preview {
		c.Set("Cache-Control", "no-store, must-revalidate")
	} else {
		c.Set("Cache-Control", "public, max-age=60")
	}
	return parsedHomePageTemplate.Execute(c, data)
}

// buildTestApp wires the same dispatch the real main.go uses, minus
// the OptionalAuth middleware. We're testing the render-vs-redirect
// fork, not the auth fork.
func buildTestApp(home *services.HomePageView, brand *services.BrandingView) *fiber.App {
	app := fiber.New()
	hs := &stubHomeService{view: home}
	bs := &stubBrandingService{view: brand}
	app.Get("/", func(c *fiber.Ctx) error {
		preview := c.Query("preview") == "1"
		// (no role-redirect — this test surface is unauthenticated)
		if home.Enabled || preview {
			if err := renderHomePageStub(c, hs, bs); err == nil {
				return nil
			}
		}
		return c.Redirect("/whm/")
	})
	return app
}

func TestRoot_PreviewDisabled_RendersWithDraftBanner(t *testing.T) {
	app := buildTestApp(
		&services.HomePageView{
			Enabled:          false,
			HeroTitle:        "Welcome",
			VendorLoginLabel: "Vendor Login",
			WHMLoginLabel:    "Admin Login",
			ShowWHMLogin:     true,
		},
		&services.BrandingView{PanelName: "Test Panel"},
	)

	req := httptest.NewRequest("GET", "/?preview=1", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("preview should set no-store, got %q", cc)
	}
	body, _ := io.ReadAll(resp.Body)
	out := string(body)
	if !strings.Contains(out, "DRAFT") {
		t.Errorf("preview of a disabled page should show DRAFT banner; body:\n%s", out)
	}
	if !strings.Contains(out, `href="/user-panel/login"`) {
		t.Errorf("vendor login link missing in draft preview")
	}
}

func TestRoot_PreviewEnabled_RendersWithoutDraftBanner(t *testing.T) {
	app := buildTestApp(
		&services.HomePageView{
			Enabled:          true,
			HeroTitle:        "Welcome",
			VendorLoginLabel: "Vendor Login",
			WHMLoginLabel:    "Admin Login",
			ShowWHMLogin:     true,
		},
		&services.BrandingView{PanelName: "Test Panel"},
	)

	req := httptest.NewRequest("GET", "/?preview=1", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	out := string(body)
	if strings.Contains(out, "DRAFT") {
		t.Errorf("preview of an ENABLED page must NOT show DRAFT banner; body:\n%s", out)
	}
}

func TestRoot_NoPreview_Disabled_RedirectsToWHM(t *testing.T) {
	app := buildTestApp(
		&services.HomePageView{Enabled: false, HeroTitle: "Welcome", VendorLoginLabel: "Login"},
		&services.BrandingView{PanelName: "Test Panel"},
	)

	req := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 302 && resp.StatusCode != 301 {
		t.Errorf("expected 30x redirect, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "/whm/") {
		t.Errorf("expected redirect to /whm/, got Location=%q", loc)
	}
}

func TestRoot_NoPreview_Enabled_RendersHomePage(t *testing.T) {
	app := buildTestApp(
		&services.HomePageView{
			Enabled:          true,
			HeroTitle:        "Welcome to MyHosting",
			HeroSubtitle:     "One panel for everything",
			BodyText:         "First paragraph.\n\nSecond paragraph.",
			VendorLoginLabel: "Vendor Login",
			WHMLoginLabel:    "Admin Login",
			ShowWHMLogin:     true,
			FooterText:       "© 2026 MyHosting",
			SupportEmail:     "support@example.com",
		},
		&services.BrandingView{
			PanelName:      "MyHosting Panel",
			LogoDataURL:    "data:image/png;base64,iVBORw0KGgo=",
			FaviconDataURL: "data:image/png;base64,iVBORw0KGgo=",
		},
	)

	req := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Errorf("public render should set max-age cache, got %q", cc)
	}
	body, _ := io.ReadAll(resp.Body)
	out := string(body)

	checks := []struct {
		name string
		want string
	}{
		{"hero title", "Welcome to MyHosting"},
		{"hero subtitle", "One panel for everything"},
		{"first paragraph", "First paragraph."},
		{"second paragraph", "Second paragraph."},
		{"vendor login link", `href="/user-panel/login"`},
		{"vendor login label", "Vendor Login"},
		{"admin login link", `href="/whm/login"`},
		{"footer text", "© 2026 MyHosting"},
		{"mailto link", `href="mailto:support@example.com"`},
		{"logo img", `src="data:image/png;base64,iVBORw0KGgo="`},
		{"favicon link", `<link rel="icon"`},
		{"no DRAFT banner", "DRAFT"}, // verified inverted below
	}
	for _, c := range checks[:len(checks)-1] {
		if !strings.Contains(out, c.want) {
			t.Errorf("body missing %s (looking for %q):\n%s", c.name, c.want, out[:min(2000, len(out))])
		}
	}
	if strings.Contains(out, "DRAFT") {
		t.Errorf("public render of an enabled page must NOT contain DRAFT banner")
	}
}

// TestRoot_AdminLoginHidden verifies the show_whm_login=false path:
// only the vendor login button should render, no admin login button.
// This is the path operators selling a closed-vendor service take —
// they don't want random visitors finding a /whm/login link.
func TestRoot_AdminLoginHidden(t *testing.T) {
	app := buildTestApp(
		&services.HomePageView{
			Enabled:          true,
			HeroTitle:        "Welcome",
			VendorLoginLabel: "Vendor Login",
			WHMLoginLabel:    "Admin Login",
			ShowWHMLogin:     false,
		},
		&services.BrandingView{PanelName: "Test"},
	)

	req := httptest.NewRequest("GET", "/", nil)
	resp, _ := app.Test(req)
	body, _ := io.ReadAll(resp.Body)
	out := string(body)
	if strings.Contains(out, `href="/whm/login"`) {
		t.Errorf("when show_whm_login=false, /whm/login link must NOT render")
	}
	if !strings.Contains(out, `href="/user-panel/login"`) {
		t.Errorf("vendor login link must always render")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
