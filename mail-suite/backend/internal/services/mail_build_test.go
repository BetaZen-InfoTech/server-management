package services

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestRewriteLinksRoundTrip(t *testing.T) {
	in := `<p>Hi <a href="https://example.com/path?x=1&y=2">click</a> and <a href='https://foo.bar/z'>two</a></p>`
	out := rewriteLinks(in, "https://m.co/", "abc123")

	// Both links must be routed through the click beacon.
	if strings.Count(out, "https://m.co/t/click/abc123?u=") != 2 {
		t.Fatalf("expected 2 rewritten links, got: %s", out)
	}
	// The encoded target must decode back to the original URL (matches the
	// handler's base64.RawURLEncoding decode).
	enc := base64.RawURLEncoding.EncodeToString([]byte("https://example.com/path?x=1&y=2"))
	if !strings.Contains(out, "u="+enc) {
		t.Fatalf("first link not encoded as expected; want u=%s in %s", enc, out)
	}
	dec, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || string(dec) != "https://example.com/path?x=1&y=2" {
		t.Fatalf("round-trip decode failed: %v %q", err, string(dec))
	}
}

func TestRewriteLinksLeavesNonHTTP(t *testing.T) {
	in := `<a href="mailto:x@y.com">mail</a> <a href="#anchor">a</a>`
	out := rewriteLinks(in, "https://m.co", "t")
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
