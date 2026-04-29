package services

import (
	"context"
	"strings"
	"testing"
)

// These tests cover the HomePageService.Save validation path that runs
// BEFORE any Mongo I/O — so we can run them without a live database.
// The DB-touching path is exercised at integration time on the deploy
// VPS; here we lock in the input rejection rules so a regression in
// the validation block can't ship.
//
// All cases pass a nil mongo.Database. Save() only dereferences s.db
// AFTER validation, so the rejection paths return their typed error
// before the nil-deref would happen. The success path WOULD nil-deref,
// so we don't have a "happy path" case here — the round-trip flow is
// verified against the real branding singleton in production.

func TestHomePageService_Save_RejectsEmptyHeroWhenEnabled(t *testing.T) {
	s := NewHomePageService(nil)
	_, err := s.Save(context.Background(), &SaveHomePageRequest{
		Enabled:   true,
		HeroTitle: "   ", // whitespace-only must NOT count
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "hero_title") {
		t.Errorf("error should mention hero_title; got %q", err.Error())
	}
}

func TestHomePageService_Save_AllowsEmptyHeroWhenDisabled(t *testing.T) {
	// When Enabled=false, we should pass validation (the page is a
	// draft; the operator can iterate on hero text without saving
	// being blocked). Save still nil-derefs at the DB write — assert
	// that we got PAST validation by recovering from the panic. Ugly,
	// but the alternative is mocking the entire mongo driver.
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic at DB write (proves validation passed)")
		}
	}()
	s := NewHomePageService(nil)
	_, _ = s.Save(context.Background(), &SaveHomePageRequest{
		Enabled:   false,
		HeroTitle: "",
	})
}

func TestHomePageService_Save_LengthCaps(t *testing.T) {
	s := NewHomePageService(nil)
	cases := []struct {
		name string
		req  *SaveHomePageRequest
		want string // substring expected in the error
	}{
		{
			name: "hero title too long",
			req:  &SaveHomePageRequest{Enabled: true, HeroTitle: strings.Repeat("a", 201)},
			want: "hero_title",
		},
		{
			name: "hero subtitle too long",
			req: &SaveHomePageRequest{
				Enabled: true, HeroTitle: "ok",
				HeroSubtitle: strings.Repeat("b", 401),
			},
			want: "hero_subtitle",
		},
		{
			name: "body too long",
			req: &SaveHomePageRequest{
				Enabled: true, HeroTitle: "ok",
				BodyText: strings.Repeat("c", 8001),
			},
			want: "body_text",
		},
		{
			name: "vendor login label too long",
			req: &SaveHomePageRequest{
				Enabled: true, HeroTitle: "ok",
				VendorLoginLabel: strings.Repeat("d", 61),
			},
			want: "vendor_login_label",
		},
		{
			name: "whm login label too long",
			req: &SaveHomePageRequest{
				Enabled: true, HeroTitle: "ok",
				WHMLoginLabel: strings.Repeat("e", 61),
			},
			want: "whm_login_label",
		},
		{
			name: "footer too long",
			req: &SaveHomePageRequest{
				Enabled: true, HeroTitle: "ok",
				FooterText: strings.Repeat("f", 401),
			},
			want: "footer_text",
		},
		{
			name: "support email too long",
			req: &SaveHomePageRequest{
				Enabled: true, HeroTitle: "ok",
				SupportEmail: strings.Repeat("g", 201),
			},
			want: "support_email",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Save(context.Background(), tc.req)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should mention %q; got %q", tc.want, err.Error())
			}
		})
	}
}

// TestDefaultHomePage locks in the seed payload a fresh install renders
// when the singleton doc doesn't exist yet. Two important invariants:
//   - Enabled is false (we keep the historical / → /whm/login behaviour
//     until the operator explicitly opts in).
//   - The login labels are non-empty so the buttons always have text,
//     even on a never-saved system.
func TestDefaultHomePage(t *testing.T) {
	d := defaultHomePage()
	if d.Enabled {
		t.Error("default home page should be DISABLED so fresh installs don't auto-publish a stub page")
	}
	if d.VendorLoginLabel == "" {
		t.Error("default vendor_login_label must be non-empty")
	}
	if d.WHMLoginLabel == "" {
		t.Error("default whm_login_label must be non-empty")
	}
	if !d.ShowWHMLogin {
		t.Error("default show_whm_login should be true so admins can find their login from /")
	}
}
