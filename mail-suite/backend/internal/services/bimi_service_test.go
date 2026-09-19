package services

import (
	"net"
	"testing"
)

func TestDMARCPassed(t *testing.T) {
	pass := []string{
		"mx.example.com; dmarc=pass (p=quarantine sp=none dis=none) header.from=example.com",
		"DMARC=Pass header.from=x.com",
		"spf=pass smtp.mailfrom=a.com; dkim=pass; dmarc=pass header.from=a.com",
	}
	fail := []string{
		"",
		"mx.example.com; dmarc=fail header.from=example.com",
		"spf=pass; dkim=pass",                 // no dmarc verdict
		"dmarc=none header.from=example.com",  // none is NOT pass
		"x-dmarc=passable",                    // must be the dmarc= token
	}
	for _, s := range pass {
		if !dmarcPassed(s) {
			t.Errorf("expected pass for %q", s)
		}
	}
	for _, s := range fail {
		if dmarcPassed(s) {
			t.Errorf("expected NON-pass for %q", s)
		}
	}
}

func TestSenderDomain(t *testing.T) {
	cases := map[string]string{
		"Alice <alice@Example.COM>": "example.com", // hardened: strips display + angle brackets
		"alice@example.com":         "example.com",
		"a.b@sub.example.co.uk":     "sub.example.co.uk",
		"nope":                      "",
		"trailing@":                 "",
	}
	for in, want := range cases {
		if got := senderDomain(in); got != want {
			t.Errorf("senderDomain(%q)=%q want %q", in, got, want)
		}
	}
}

func TestIsPublicIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.5", "192.168.1.1", "172.16.0.1", "169.254.0.1", "::1", "fc00::1", "0.0.0.0", "224.0.0.1"}
	ok := []string{"1.1.1.1", "8.8.8.8", "187.127.150.159", "2606:4700:4700::1111"}
	for _, s := range blocked {
		if isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked (non-public)", s)
		}
	}
	for _, s := range ok {
		if !isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed (public)", s)
		}
	}
	if isPublicIP(nil) {
		t.Error("nil IP must be blocked")
	}
}

func TestBimiSVGSafe(t *testing.T) {
	bad := []string{
		"<html></html>",
		`<svg><script>x</script></svg>`,
		`<svg onload="x()"></svg>`,
		`<svg><image href="data:image/png;base64,AA"/></svg>`,
		`<svg><a href="x"/></svg>`,
		`<svg><use xlink:href="https://evil/x#a"/></svg>`,
		`<!DOCTYPE svg><svg></svg>`,
	}
	for _, s := range bad {
		if err := bimiSVGSafe([]byte(s)); err == nil {
			t.Errorf("expected rejection for %q", s)
		}
	}
	good := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><title>ok</title><rect width="512" height="512"/></svg>`
	if err := bimiSVGSafe([]byte(good)); err != nil {
		t.Errorf("valid svg rejected: %v", err)
	}
}

func TestBimiLValueParse(t *testing.T) {
	cases := map[string]string{
		"v=BIMI1; l=https://example.com/logo.svg;":            "https://example.com/logo.svg",
		"v=BIMI1;l=https://x.com/a.svg":                       "https://x.com/a.svg",
		"v=BIMI1; l=https://x.com/a.svg; a=https://x.com/vmc": "https://x.com/a.svg",
	}
	for in, want := range cases {
		m := bimiLValue.FindStringSubmatch(in)
		if len(m) != 2 || m[1] != want {
			t.Errorf("parse %q: got %v want %q", in, m, want)
		}
	}
}
