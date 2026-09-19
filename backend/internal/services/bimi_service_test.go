package services

import "testing"

func TestSanitizeBIMISVG_RejectsUnsafe(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"not-svg", "<html><body>hi</body></html>"},
		{"script", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`},
		{"event-attr", `<svg xmlns="http://www.w3.org/2000/svg" onload="x()"><rect/></svg>`},
		{"raster-image", `<svg xmlns="http://www.w3.org/2000/svg"><image href="data:image/png;base64,AAAA"/></svg>`},
		{"anchor", `<svg xmlns="http://www.w3.org/2000/svg"><a href="x"><rect/></a></svg>`},
		{"external-href", `<svg xmlns="http://www.w3.org/2000/svg"><use xlink:href="https://evil.example/x.svg#a"/></svg>`},
		{"js-uri", `<svg xmlns="http://www.w3.org/2000/svg"><rect fill="url(javascript:alert(1))"/></svg>`},
		{"doctype", `<!DOCTYPE svg><svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`},
		{"animate", `<svg xmlns="http://www.w3.org/2000/svg"><rect><animate attributeName="x"/></rect></svg>`},
		{"foreignobject", `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><body/></foreignObject></svg>`},
		{"html-before-svg", `<b>hi</b><svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := sanitizeBIMISVG([]byte(c.in)); err == nil {
				t.Fatalf("expected rejection for %q, got nil error", c.name)
			}
		})
	}
}

func TestSanitizeBIMISVG_AcceptsValidPS(t *testing.T) {
	// A minimal BIMI SVG Tiny 1.2 PS logo: square viewBox, baseProfile, title.
	svg := `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" version="1.2" baseProfile="tiny-ps" viewBox="0 0 512 512">
<title>Betazen</title><rect width="512" height="512" fill="#0af"/></svg>`
	out, warn, err := sanitizeBIMISVG([]byte(svg))
	if err != nil {
		t.Fatalf("valid PS SVG rejected: %v", err)
	}
	if out == "" {
		t.Fatal("expected sanitized SVG back")
	}
	if len(warn) != 0 {
		t.Fatalf("expected no profile warnings, got: %v", warn)
	}
}

func TestSanitizeBIMISVG_WarnsMissingProfile(t *testing.T) {
	// Safe SVG but missing the BIMI PS markers → should upload with warnings.
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`
	_, warn, err := sanitizeBIMISVG([]byte(svg))
	if err != nil {
		t.Fatalf("safe SVG should not hard-error: %v", err)
	}
	if len(warn) == 0 {
		t.Fatal("expected PS-profile warnings for an SVG missing baseProfile/version/viewBox/title")
	}
}

func TestSanitizeBIMISVG_RejectsOversize(t *testing.T) {
	big := make([]byte, MaxBIMISVGBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	// Make it look like an SVG so size is the failing check, not the shape.
	copy(big, []byte("<svg "))
	if _, _, err := sanitizeBIMISVG(big); err == nil {
		t.Fatal("expected oversize rejection")
	}
}
