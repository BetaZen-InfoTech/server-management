package services

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

func TestRewriteLinksRoundTrip(t *testing.T) {
	const secret = "test-secret"
	in := `<p>Hi <a href="https://example.com/path?x=1&y=2">click</a> and <a href='https://foo.bar/z'>two</a></p>`
	out := rewriteLinks(in, "https://m.co/", "abc123", secret)

	// Both links must be routed through the click beacon.
	if strings.Count(out, "https://m.co/t/click/abc123?u=") != 2 {
		t.Fatalf("expected 2 rewritten links, got: %s", out)
	}
	// The u= blob decodes to "<sig>:<url>"; the url round-trips and the sig
	// verifies (and fails under a wrong secret) — this is what closes the open
	// redirect on /t/click.
	m := regexp.MustCompile(`u=([A-Za-z0-9_-]+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no u= param in %s", out)
	}
	raw, err := base64.RawURLEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	payload := string(raw)
	i := strings.IndexByte(payload, ':')
	if i <= 0 {
		t.Fatalf("payload is not <sig>:<url>: %q", payload)
	}
	sig, url := payload[:i], payload[i+1:]
	if url != "https://example.com/path?x=1&y=2" {
		t.Fatalf("url mismatch: %q", url)
	}
	if !verifyClick(secret, "abc123", url, sig) {
		t.Fatalf("signature failed to verify")
	}
	if verifyClick("wrong-secret", "abc123", url, sig) {
		t.Fatalf("signature verified under the wrong secret (open redirect not closed)")
	}
}

// TestRewriteLinksUnescapesEntities guards the fix for multi-parameter tracked
// links: TipTap serializes hrefs with &amp;, so the signed/redirected target
// must be HTML-UNESCAPED — otherwise the destination receives a literal "&amp;"
// and loses every query parameter after the first.
func TestRewriteLinksUnescapesEntities(t *testing.T) {
	const secret = "s"
	in := `<a href="https://shop.example.com/p?id=5&amp;ref=news&amp;u=2">buy</a>`
	out := rewriteLinks(in, "https://m.co", "tid", secret)
	m := regexp.MustCompile(`u=([A-Za-z0-9_-]+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no u= param in %s", out)
	}
	raw, err := base64.RawURLEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	payload := string(raw)
	i := strings.IndexByte(payload, ':')
	if i <= 0 {
		t.Fatalf("payload is not <sig>:<url>: %q", payload)
	}
	sig, url := payload[:i], payload[i+1:]
	const want = "https://shop.example.com/p?id=5&ref=news&u=2"
	if url != want {
		t.Fatalf("href not HTML-unescaped: got %q want %q", url, want)
	}
	if !verifyClick(secret, "tid", url, sig) {
		t.Fatalf("signature must verify against the unescaped url")
	}
}

func TestRewriteLinksLeavesNonHTTP(t *testing.T) {
	in := `<a href="mailto:x@y.com">mail</a> <a href="#anchor">a</a>`
	out := rewriteLinks(in, "https://m.co", "t", "sec")
	if out != in {
		t.Fatalf("mailto/anchor links should be untouched, got: %s", out)
	}
}

func TestInjectOpenPixel(t *testing.T) {
	withBody := injectOpenPixel(`<html><body><p>Hi</p></body></html>`, "https://m.co", "tid")
	if !strings.Contains(withBody, `/t/open/tid.png`) {
		t.Fatalf("pixel URL missing: %s", withBody)
	}
	if strings.Index(withBody, "/t/open/tid.png") > strings.Index(withBody, "</body>") {
		t.Fatalf("pixel should be injected before </body>: %s", withBody)
	}
	noBody := injectOpenPixel(`<p>Hi</p>`, "https://m.co", "tid")
	if !strings.HasPrefix(noBody, "<p>Hi</p>") || !strings.HasSuffix(noBody, `/>`) || !strings.Contains(noBody, "/t/open/tid.png") {
		t.Fatalf("pixel should be appended after content when no body: %s", noBody)
	}
}

func TestHTMLToText(t *testing.T) {
	got := htmlToText(`<style>x{}</style><p>Hello <b>world</b></p><br/>line2`)
	if strings.Contains(got, "<") || strings.Contains(got, "x{}") {
		t.Fatalf("text should have no tags/style: %q", got)
	}
	if !strings.Contains(got, "Hello world") || !strings.Contains(got, "line2") {
		t.Fatalf("text content lost: %q", got)
	}
}

func TestBuildMessageIDDomain(t *testing.T) {
	id := buildMessageID("alice@betazeninfotech.com")
	if !strings.HasSuffix(id, "@betazeninfotech.com>") || !strings.HasPrefix(id, "<") {
		t.Fatalf("unexpected message id: %s", id)
	}
}
