package agent

import (
	"context"
	"embed"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Branded default pages (welcome + full 4xx/5xx error set) are EMBEDDED in
// the binary via go:embed. That is the migration/transfer-safety guarantee:
// the pages travel with the code, so a server move / rebuild / fresh install
// always has them — there is no external asset directory to forget to copy.
// On each boot (and whenever branding changes) EnsureDefaultWebAssets writes
// them to disk + installs the nginx snippets, self-healing if anything on the
// box was wiped.

//go:embed defaultpages/welcome.html
var welcomeHTMLRaw string

//go:embed defaultpages/errors/*.html
var errorPagesFS embed.FS

const (
	// DefaultWebRoot holds the panel's shared, non-tenant web assets (the
	// welcome logo the agent reads). Agent-only — never served by nginx.
	DefaultWebRoot = "/opt/serverpanel/webroot"
	// ErrorPagesDir is the on-disk home of the branded error pages, served to
	// every vhost through the nginx snippets via an internal location. Kept
	// under /var/www (like the existing sp-placeholder / sp-suspended roots),
	// NOT under /opt/serverpanel, so error-page serving never depends on the
	// install dir staying world-traversable — an operator can chmod 0750
	// /opt/serverpanel to protect .env without 403-ing every error page.
	ErrorPagesDir = "/var/www/sp-errors"
	// brandingDir + welcomeLogoFile persist the CMS logo (a data: URL) so the
	// agent's per-domain welcome-page writer stays Mongo-free (and therefore
	// usable from the remote agent daemon too).
	brandingDir     = DefaultWebRoot + "/branding"
	welcomeLogoFile = brandingDir + "/welcome-logo.txt"

	nginxSnippetsDir = "/etc/nginx/snippets"
	// ErrSnippetStatic is included by file/PHP/static vhosts (full error set).
	ErrSnippetStatic = nginxSnippetsDir + "/betazen-errors.conf"
	// ErrSnippetProxy is included by reverse-proxy (app/API) vhosts — gateway
	// errors ONLY (502/503/504), so an app's own JSON 4xx/5xx is never
	// replaced by an HTML page.
	ErrSnippetProxy = nginxSnippetsDir + "/betazen-errors-proxy.conf"

	// errInternalLoc is the internal nginx path the error pages are served
	// from. Deliberately obscure so it can't collide with a real site route.
	errInternalLoc = "/__bzerr/"

	// IncludeErrorsStatic / IncludeErrorsProxy are the one-line include
	// directives the vhost templates embed. Kept as constants so the
	// retrofit sweep can detect an already-wired vhost by substring.
	IncludeErrorsStatic = "    include " + ErrSnippetStatic + ";\n"
	IncludeErrorsProxy  = "    include " + ErrSnippetProxy + ";\n"
)

// logoMarker is the exact span every bundled page uses for the brand mark.
// Injecting the CMS logo is a single replace of this token.
const logoMarker = `<span class="logo">β</span>`

// welcomeSentinel is an exact token embedded in the bundled welcome page (an
// HTML comment). The welcome-page retrofit re-stamps ONLY pages that still
// carry this sentinel — so a customer who edited their welcome page in place
// (dropping the comment, or any real site) is never overwritten.
const welcomeSentinel = "bzpanel-welcome:v1"

// errorCodesFull is every status code we ship a dedicated page for. Used for
// the static/PHP snippet. Codes without a page fall through to nginx's own
// default, which is fine.
var errorCodesFull = []int{
	400, 401, 403, 404, 405, 408, 409, 410, 413, 414, 415, 416,
	421, 422, 423, 425, 426, 428, 429, 431, 451,
	500, 501, 502, 503, 504, 505, 506, 507, 508, 510, 511,
}

// proxyErrorCodes are the gateway errors nginx itself emits when an upstream
// is down/slow — safe to brand on reverse-proxy vhosts without intercepting
// the app's own responses.
var proxyErrorCodes = []int{502, 503, 504}

// injectLogo swaps the β glyph marker for the configured logo image. Empty
// logo keeps the glyph (the bundled default).
func injectLogo(pageHTML, logoDataURL string) string {
	logoDataURL = strings.TrimSpace(logoDataURL)
	if logoDataURL == "" {
		return pageHTML
	}
	// HTML-escape the URL before placing it in the src attribute. Defence in
	// depth: branding_service also restricts the value to a base64 data: URL,
	// but we never want a stray quote to break out of the attribute and inject
	// markup onto every tenant's welcome + error pages.
	img := fmt.Sprintf(`<img class="logo" src="%s" alt="logo" style="object-fit:contain;background:transparent">`, html.EscapeString(logoDataURL))
	return strings.ReplaceAll(pageHTML, logoMarker, img)
}

// WelcomeHTML returns the branded welcome page with the configured logo
// injected. The logo is read from the ensured on-disk file so this works
// from the agent without a Mongo connection.
func WelcomeHTML() string {
	return injectLogo(welcomeHTMLRaw, readWelcomeLogo())
}

func readWelcomeLogo() string {
	b, err := os.ReadFile(welcomeLogoFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// EnsureDefaultWebAssets writes the branded error pages (logo injected) to
// ErrorPagesDir, persists the logo for the welcome-page writer, and installs
// the two nginx snippets. Idempotent + self-healing: safe to call on every
// boot and whenever branding changes. logoDataURL may be empty (β default).
func EnsureDefaultWebAssets(ctx context.Context, logoDataURL string) error {
	for _, d := range []string{DefaultWebRoot, ErrorPagesDir, brandingDir, nginxSnippetsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
		_ = os.Chmod(d, 0755) // www-data must traverse to read error pages
	}

	logoDataURL = strings.TrimSpace(logoDataURL)
	if err := os.WriteFile(welcomeLogoFile, []byte(logoDataURL), 0644); err != nil {
		return fmt.Errorf("write welcome logo: %w", err)
	}

	entries, err := errorPagesFS.ReadDir("defaultpages/errors")
	if err != nil {
		return fmt.Errorf("read embedded error pages: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := errorPagesFS.ReadFile("defaultpages/errors/" + e.Name())
		if err != nil {
			continue
		}
		html := injectLogo(string(raw), logoDataURL)
		if err := os.WriteFile(filepath.Join(ErrorPagesDir, e.Name()), []byte(html), 0644); err != nil {
			return fmt.Errorf("write %s: %w", e.Name(), err)
		}
	}

	if err := os.WriteFile(ErrSnippetStatic, []byte(buildErrorSnippet(errorCodesFull)), 0644); err != nil {
		return fmt.Errorf("write static snippet: %w", err)
	}
	if err := os.WriteFile(ErrSnippetProxy, []byte(buildErrorSnippet(proxyErrorCodes)), 0644); err != nil {
		return fmt.Errorf("write proxy snippet: %w", err)
	}
	return nil
}

// ApplyErrorPagesToExistingVhosts retrofits every EXISTING site vhost with the
// branded error-page include, so already-created websites/apps/software get
// them too — not just newly created ones. Idempotent. Reverse-proxy vhosts get
// the gateway-only snippet (so an app's own JSON errors pass through); file/
// static/PHP vhosts get the full set. Suspended/placeholder vhosts (which run
// their own error_page 503/410 handling) and the nginx default are left alone.
//
// Safety: edits are staged in memory, a single `nginx -t` gates the whole
// batch, and ANY failure rolls back every edited file before returning — a bad
// rewrite can never leave nginx unable to reload. Only reloads on success.
func ApplyErrorPagesToExistingVhosts(ctx context.Context, logoDataURL string) (updated, skipped int, err error) {
	if err = EnsureDefaultWebAssets(ctx, logoDataURL); err != nil {
		return 0, 0, err
	}
	const dir = "/etc/nginx/sites-available"
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		return 0, 0, rerr
	}

	type backup struct{ path, orig string }
	var backups []backup
	rollback := func() {
		for _, b := range backups {
			_ = os.WriteFile(b.path, []byte(b.orig), 0644)
		}
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "default" {
			skipped++
			continue
		}
		path := filepath.Join(dir, name)
		raw, re := os.ReadFile(path)
		if re != nil {
			skipped++
			continue
		}
		content := string(raw)
		// Already wired, or a suspended/placeholder vhost with its own special
		// handling — leave it. Suspended uses `error_page 503`; the deleted-
		// domain placeholder uses `try_files … =410` + the sp-placeholder root
		// (so match the root, since `=410` isn't the string "error_page 410").
		if strings.Contains(content, "betazen-errors") ||
			strings.Contains(content, "error_page 503") ||
			strings.Contains(content, "error_page 410") ||
			strings.Contains(content, "sp-placeholder") ||
			strings.Contains(content, "sp-suspended") {
			skipped++
			continue
		}
		include := IncludeErrorsStatic
		if strings.Contains(content, "proxy_pass") {
			include = IncludeErrorsProxy
		}
		newContent := insertIncludeAfterServerName(content, include)
		if newContent == content {
			skipped++
			continue
		}
		backups = append(backups, backup{path, content})
		if werr := os.WriteFile(path, []byte(newContent), 0644); werr != nil {
			rollback()
			return 0, 0, werr
		}
		updated++
	}

	if _, terr := RunCommand(ctx, "nginx", "-t"); terr != nil {
		rollback()
		return 0, 0, fmt.Errorf("nginx -t failed after error-page retrofit, rolled back %d file(s): %w", len(backups), terr)
	}
	_ = ReloadNginx(ctx)
	return updated, skipped, nil
}

// legacyWelcomePlaceholder is the EXACT trivial page older builds dropped into
// a new domain's docroot. The welcome-page retrofit replaces only files whose
// content matches this byte-for-byte, so a customer's real site is never
// touched.
const legacyWelcomePlaceholder = `<!DOCTYPE html><html><head><title>Welcome</title></head><body><h1>Welcome to your new website!</h1></body></html>`

// UpgradeDefaultWelcomePages rewrites the OLD placeholder index.html of
// already-created domains to the new branded welcome page. It is deliberately
// conservative: a docroot is upgraded ONLY when its index.html is exactly the
// legacy placeholder (or the previous branded welcome, so a logo change
// re-applies). Any real customer content — a different index.html, an
// index.php, an uploaded site — is left alone. Ownership is preserved by
// chowning the rewritten file back to the domain's /home/<user>.
func UpgradeDefaultWelcomePages(ctx context.Context) (upgraded, scanned int, err error) {
	matches, gerr := filepath.Glob("/home/*/domains/*/public_html/index.html")
	if gerr != nil {
		return 0, 0, gerr
	}
	newHTML := WelcomeHTML()
	newTrim := strings.TrimSpace(newHTML)
	for _, p := range matches {
		scanned++
		// Never follow a symlink in a user-controlled public_html while running
		// as root — a customer could point index.html at a file outside their
		// tree. Skip anything that isn't a regular file.
		if fi, lerr := os.Lstat(p); lerr != nil || !fi.Mode().IsRegular() {
			continue
		}
		raw, re := os.ReadFile(p)
		if re != nil {
			continue
		}
		cur := strings.TrimSpace(string(raw))
		// Skip real content. Replace only the legacy placeholder, or a prior
		// branded welcome whose logo/markup has since changed.
		if cur != legacyWelcomePlaceholder && !(isBrandedWelcome(cur) && cur != newTrim) {
			continue
		}
		if werr := os.WriteFile(p, []byte(newHTML), 0644); werr != nil {
			continue
		}
		// /home/<user>/domains/<domain>/public_html/index.html → <user>
		if parts := strings.Split(p, "/"); len(parts) > 2 && parts[1] == "home" && parts[2] != "" {
			RunCommand(ctx, "chown", parts[2]+":"+parts[2], p)
		}
		upgraded++
	}
	return upgraded, scanned, nil
}

// isBrandedWelcome reports whether an index.html is one of OUR *unmodified*
// branded welcome pages — detected by the exact embedded sentinel, not a fuzzy
// content match. A customer who edited the page in place (and dropped the
// comment) no longer matches, so their work is never discarded on a re-stamp.
func isBrandedWelcome(page string) bool {
	return strings.Contains(page, welcomeSentinel)
}

// insertIncludeAfterServerName inserts the include directive ONCE per server{}
// block — after that block's FIRST server_name line. Inserting after every
// server_name would duplicate the snippet's `location ^~ /__bzerr/` inside a
// block that legally uses multiple server_name directives, which nginx rejects
// as a duplicate location and would abort the whole fleet retrofit.
func insertIncludeAfterServerName(content, include string) string {
	line := strings.TrimRight(include, "\n")
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines)+4)
	needInclude := false
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "server {") || trimmed == "server{" {
			needInclude = true // a new block opened; arm one include for it
		}
		out = append(out, ln)
		if needInclude && strings.HasPrefix(trimmed, "server_name ") {
			out = append(out, line)
			needInclude = false
		}
	}
	return strings.Join(out, "\n")
}

// buildErrorSnippet renders the nginx error_page directives for the given
// codes plus the shared internal location that serves the pages.
func buildErrorSnippet(codes []int) string {
	sorted := append([]int(nil), codes...)
	sort.Ints(sorted)
	var b strings.Builder
	b.WriteString("# Managed by Betazen Server Panel — branded default error pages.\n")
	b.WriteString("# Do not edit by hand; regenerated on boot / branding change.\n")
	for _, c := range sorted {
		fmt.Fprintf(&b, "error_page %d %s%d.html;\n", c, errInternalLoc, c)
	}
	fmt.Fprintf(&b, "location ^~ %s {\n    internal;\n    alias %s/;\n}\n", errInternalLoc, ErrorPagesDir)
	return b.String()
}
