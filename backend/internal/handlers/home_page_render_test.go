package handlers

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// TestSplitParagraphs covers the body-text → paragraph-list split that
// the home-page template walks. Important behaviours:
//   - blank-line-separated blocks become separate <p>s
//   - trailing/leading whitespace is trimmed per block
//   - empty input yields a nil slice so the template can skip the
//     whole .body div
//   - CRLF line endings (operator pasting from Word / a Windows editor)
//     normalise the same as Unix LF
func TestSplitParagraphs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", []string{}},
		{"whitespace only", "   \n\n  \t\n", []string{}},
		{"single line", "Hello world.", []string{"Hello world."}},
		{"two paragraphs", "First.\n\nSecond.", []string{"First.", "Second."}},
		{"trims surrounding whitespace", "  First.  \n\n  Second.  ", []string{"First.", "Second."}},
		{"crlf normalises", "First.\r\n\r\nSecond.", []string{"First.", "Second."}},
		{"multiple blanks collapse", "First.\n\n\n\nSecond.", []string{"First.", "Second."}},
		{"single newline stays in paragraph", "Line one\nLine two.", []string{"Line one\nLine two."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitParagraphs(tc.in)
			// Normalise nil vs empty slice for the test — both render
			// the same in the template (the `if .BodyParagraphs` check
			// treats len==0 as falsy regardless of nil-ness).
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitParagraphs(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

// TestHomePageTemplate_DraftBanner locks in the preview-mode visual: the
// orange "DRAFT" banner appears IFF the render data sets DraftBanner=true,
// and the vendor login button always renders regardless of mode (so an
// admin previewing a draft can verify the CTA without publishing).
func TestHomePageTemplate_DraftBanner(t *testing.T) {
	render := func(draft bool) string {
		var buf bytes.Buffer
		err := parsedHomePageTemplate.Execute(&buf, homePageRenderData{
			PanelName:        "Test Panel",
			HeroTitle:        "Welcome",
			VendorLoginLabel: "Vendor Login",
			ShowWHMLogin:     true,
			WHMLoginLabel:    "Admin Login",
			DraftBanner:      draft,
		})
		if err != nil {
			t.Fatalf("template execute: %v", err)
		}
		return buf.String()
	}

	withDraft := render(true)
	if !strings.Contains(withDraft, "DRAFT") {
		t.Errorf("expected DRAFT banner when DraftBanner=true; got:\n%s", withDraft)
	}
	if !strings.Contains(withDraft, `href="/user-panel/login"`) {
		t.Errorf("vendor login link missing in draft preview")
	}

	withoutDraft := render(false)
	if strings.Contains(withoutDraft, "DRAFT") {
		t.Errorf("did NOT expect DRAFT banner when DraftBanner=false")
	}
	if !strings.Contains(withoutDraft, `href="/user-panel/login"`) {
		t.Errorf("vendor login link missing in published render")
	}
}

// TestHomePageTemplate_HTMLEscaping verifies html/template auto-escapes
// operator-supplied content. An XSS attempt in HeroTitle should land as
// literal text in the response, never as an executable <script> tag.
// This is the security backstop for the "BodyText is plain text only"
// claim in the docstring.
func TestHomePageTemplate_HTMLEscaping(t *testing.T) {
	var buf bytes.Buffer
	err := parsedHomePageTemplate.Execute(&buf, homePageRenderData{
		PanelName:        "Test",
		HeroTitle:        `<script>alert("xss")</script>`,
		BodyParagraphs:   []string{`<img src=x onerror=alert(1)>`},
		VendorLoginLabel: "Login",
	})
	if err != nil {
		t.Fatalf("template execute: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `<script>alert("xss")</script>`) {
		t.Errorf("template did not escape <script> in HeroTitle:\n%s", out)
	}
	if strings.Contains(out, `<img src=x onerror`) {
		t.Errorf("template did not escape <img> in BodyParagraphs:\n%s", out)
	}
	// Auto-escaped form must still appear so the operator sees their
	// (safely rendered) text on the page.
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("expected escaped <script> entity, got:\n%s", out)
	}
}
