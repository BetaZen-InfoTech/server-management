package handlers

import (
	"bytes"
	"context"
	"html/template"
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/gofiber/fiber/v2"
)

// homePageHTMLTemplate renders the public landing page server-side at
// GET /. We use html/template (not text/template) so every interpolated
// field is auto-escaped — operator-supplied HeroTitle / BodyText / etc.
// can never become an XSS vector even if someone pastes <script> tags.
//
// Body paragraphs: BodyText is split on blank lines client-side (in the
// renderer) and each paragraph is rendered as its own <p>. Single \n
// inside a paragraph becomes <br>. This gives operators a Markdown-lite
// authoring experience without us shipping a Markdown parser.
//
// Visual style: no Tailwind, no React, no fonts to fetch. The page is
// a single self-contained HTML doc with inlined CSS so it loads fast
// and works on slow connections / behind strict CSPs. Layout is
// responsive without media queries — flex with wrap and percentage
// widths.
const homePageHTMLTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.PanelName}}</title>
{{if .FaviconDataURL}}<link rel="icon" href="{{.FaviconDataURL}}">{{end}}
<style>
  *{box-sizing:border-box}
  html,body{margin:0;padding:0;background:#0b1020;color:#e6e8ee;font-family:system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;line-height:1.55}
  a{color:#60a5fa;text-decoration:none}
  a:hover{text-decoration:underline}
  .wrap{min-height:100vh;display:flex;flex-direction:column}
  .topbar{display:flex;align-items:center;justify-content:space-between;padding:18px 28px;border-bottom:1px solid #1f2a44}
  .topbar .brand{display:flex;align-items:center;gap:12px;font-weight:600;color:#fff}
  .topbar .brand img{height:32px;width:auto;border-radius:6px}
  .topbar .nav{display:flex;align-items:center;gap:14px;font-size:14px}
  .topbar .nav a.btn{padding:8px 16px;border-radius:8px;background:#2563eb;color:#fff;font-weight:500}
  .topbar .nav a.btn:hover{background:#1d4ed8;text-decoration:none}
  .topbar .nav a.btn.ghost{background:transparent;color:#cbd5e1;border:1px solid #334155}
  .topbar .nav a.btn.ghost:hover{border-color:#60a5fa;color:#fff}
  .hero{flex:1;display:flex;align-items:center;justify-content:center;padding:60px 28px;text-align:center}
  .hero .inner{max-width:760px}
  .hero h1{font-size:44px;margin:0 0 16px 0;line-height:1.15;color:#fff;letter-spacing:-0.02em}
  .hero .subtitle{font-size:19px;color:#9aa6c2;margin:0 0 28px 0}
  .hero .body{font-size:16px;color:#cbd5e1;text-align:left;margin:28px auto 0 auto;max-width:640px}
  .hero .body p{margin:0 0 14px 0}
  .hero .cta{margin-top:36px;display:flex;gap:12px;flex-wrap:wrap;justify-content:center}
  .hero .cta a{padding:14px 28px;border-radius:10px;font-weight:600;font-size:15px}
  .hero .cta a.primary{background:#2563eb;color:#fff}
  .hero .cta a.primary:hover{background:#1d4ed8;text-decoration:none}
  .hero .cta a.secondary{background:transparent;color:#cbd5e1;border:1px solid #334155}
  .hero .cta a.secondary:hover{border-color:#60a5fa;color:#fff;text-decoration:none}
  .footer{border-top:1px solid #1f2a44;padding:18px 28px;font-size:13px;color:#64748b;display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:8px}
  @media(max-width:600px){
    .hero h1{font-size:32px}
    .hero .subtitle{font-size:16px}
    .topbar{padding:14px 18px}
    .hero{padding:40px 20px}
  }
</style>
</head>
<body>
<div class="wrap">
  <header class="topbar">
    <div class="brand">
      {{if .LogoDataURL}}<img src="{{.LogoDataURL}}" alt="{{.PanelName}}">{{end}}
      <span>{{.PanelName}}</span>
    </div>
    <nav class="nav">
      <a class="btn" href="/user-panel/login">{{.VendorLoginLabel}}</a>
      {{if .ShowWHMLogin}}<a class="btn ghost" href="/whm/login">{{.WHMLoginLabel}}</a>{{end}}
    </nav>
  </header>

  <main class="hero">
    <div class="inner">
      <h1>{{.HeroTitle}}</h1>
      {{if .HeroSubtitle}}<p class="subtitle">{{.HeroSubtitle}}</p>{{end}}
      {{if .BodyParagraphs}}
      <div class="body">
        {{range .BodyParagraphs}}<p>{{.}}</p>{{end}}
      </div>
      {{end}}
      <div class="cta">
        <a class="primary" href="/user-panel/login">{{.VendorLoginLabel}}</a>
        {{if .ShowWHMLogin}}<a class="secondary" href="/whm/login">{{.WHMLoginLabel}}</a>{{end}}
      </div>
    </div>
  </main>

  <footer class="footer">
    <div>{{if .FooterText}}{{.FooterText}}{{else}}&copy; {{.PanelName}}{{end}}</div>
    {{if .SupportEmail}}<div>Support: <a href="mailto:{{.SupportEmail}}">{{.SupportEmail}}</a></div>{{end}}
  </footer>
</div>
</body>
</html>`

// parsedHomePageTemplate is parsed once at startup so every request
// reuses the compiled tree (template parsing isn't free at request
// scope — it's all reflection).
var parsedHomePageTemplate = template.Must(template.New("home").Parse(homePageHTMLTemplate))

// homePageRenderData is the merged view that the template walks. We
// pre-split BodyText into paragraphs so the template can `range` over
// them; doing the split in the template would require a custom
// template func and a tighter reading.
type homePageRenderData struct {
	PanelName        string
	LogoDataURL      string
	FaviconDataURL   string
	HeroTitle        string
	HeroSubtitle     string
	BodyParagraphs   []string
	VendorLoginLabel string
	WHMLoginLabel    string
	ShowWHMLogin     bool
	FooterText       string
	SupportEmail     string
}

// splitParagraphs turns a multi-line BodyText into <p>-friendly chunks.
// Blank-line-separated blocks become separate paragraphs; single \n
// inside a block is preserved by html/template's auto-escape (newlines
// render as whitespace in HTML). Empty input yields a nil slice so the
// template's `if .BodyParagraphs` block is skipped entirely.
func splitParagraphs(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	chunks := strings.Split(s, "\n\n")
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		c = strings.TrimSpace(c)
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

// RenderHomePage is the GET / handler used when a visitor is
// unauthenticated AND HomePageService.Enabled == true. It composes
// branding + home-page settings into the inline template, sets a
// short cache header (so a fresh deploy's edits show up within a
// minute even on aggressive CDNs), and writes the HTML directly.
//
// Failure mode: any internal error falls through to the caller, which
// is expected to redirect to /whm/ (the pre-feature default). We
// never want a misconfigured home page to brick the panel's login.
func RenderHomePage(
	ctx context.Context,
	c *fiber.Ctx,
	homeService *services.HomePageService,
	brandingService *services.BrandingService,
) error {
	home, err := homeService.Get(ctx)
	if err != nil {
		return err
	}
	brand, err := brandingService.Get(ctx)
	if err != nil {
		return err
	}
	data := homePageRenderData{
		PanelName:        brand.PanelName,
		LogoDataURL:      brand.LogoDataURL,
		FaviconDataURL:   brand.FaviconDataURL,
		HeroTitle:        home.HeroTitle,
		HeroSubtitle:     home.HeroSubtitle,
		BodyParagraphs:   splitParagraphs(home.BodyText),
		VendorLoginLabel: home.VendorLoginLabel,
		WHMLoginLabel:    home.WHMLoginLabel,
		ShowWHMLogin:     home.ShowWHMLogin,
		FooterText:       home.FooterText,
		SupportEmail:     home.SupportEmail,
	}
	var buf bytes.Buffer
	if err := parsedHomePageTemplate.Execute(&buf, data); err != nil {
		return err
	}
	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Cache-Control", "public, max-age=60")
	return c.Send(buf.Bytes())
}
