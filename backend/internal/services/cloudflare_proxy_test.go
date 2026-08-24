package services

import (
	"testing"

	"github.com/betazeninfotech/whm-cpanel-management/pkg/cloudflare"
)

func TestProxyableType(t *testing.T) {
	for _, typ := range []string{"A", "AAAA", "CNAME", "a", "cname"} {
		if !proxyableType(typ) {
			t.Errorf("proxyableType(%q) = false; want true", typ)
		}
	}
	for _, typ := range []string{"MX", "TXT", "NS", "SRV", "CAA", "SOA"} {
		if proxyableType(typ) {
			t.Errorf("proxyableType(%q) = true; want false", typ)
		}
	}
}

// TestHostLabelsBelowZone pins the depth arithmetic the whole multi-level guard
// relies on: apex = 0, one label = 1, two labels = 2, and a name outside the
// zone = -1.
func TestHostLabelsBelowZone(t *testing.T) {
	cases := []struct {
		name, zone string
		want       int
	}{
		{"example.com", "example.com", 0},
		{"@", "example.com", 0},
		{"", "example.com", 0},
		{"api.example.com", "example.com", 1},
		{"www.example.com", "example.com", 1},
		{"api.saas.example.com", "example.com", 2},
		{"betazen.w.example.com", "example.com", 2},
		{"a.b.c.example.com", "example.com", 3},
		{"example.com.", "example.com", 0},
		{"API.SAAS.EXAMPLE.COM", "example.com", 2},
		{"other.org", "example.com", -1},
	}
	for _, tc := range cases {
		if got := hostLabelsBelowZone(tc.name, tc.zone); got != tc.want {
			t.Errorf("hostLabelsBelowZone(%q,%q) = %d; want %d", tc.name, tc.zone, got, tc.want)
		}
	}
}

// TestUniversalSSLCovers proves the free-plan certificate boundary: apex and a
// single label are covered by `zone` + `*.zone`; anything deeper is not.
func TestUniversalSSLCovers(t *testing.T) {
	covered := []string{"example.com", "@", "api.example.com", "www.example.com", "mail.example.com"}
	for _, h := range covered {
		if !universalSSLCovers(h, "example.com") {
			t.Errorf("universalSSLCovers(%q) = false; want true", h)
		}
	}
	notCovered := []string{"api.saas.example.com", "betazen.w.example.com", "a.b.c.example.com", "www.api.example.com"}
	for _, h := range notCovered {
		if universalSSLCovers(h, "example.com") {
			t.Errorf("universalSSLCovers(%q) = true; want false", h)
		}
	}
}

// TestApplyProxyPolicy proves the create-path proxy policy: eligible web records
// get orange-clouded when on, mail + non-proxyable stay DNS-only, multi-level
// subdomains are forced DNS-only (Universal SSL can't cover them) even when off,
// and Advanced Certificate Manager lifts that restriction.
func TestApplyProxyPolicy(t *testing.T) {
	show := func(b *bool) string {
		if b == nil {
			return "nil"
		}
		if *b {
			return "true"
		}
		return "false"
	}
	cases := []struct {
		name     string
		p        cloudflare.RecordParams
		proxyWeb bool
		adv      bool
		want     string // "true" | "false" | "nil"
	}{
		{"apex A proxied when on", cloudflare.RecordParams{Type: "A", Name: "example.com", Content: "1.2.3.4"}, true, false, "true"},
		{"www CNAME proxied when on", cloudflare.RecordParams{Type: "CNAME", Name: "www.example.com", Content: "example.com"}, true, false, "true"},
		{"subdomain AAAA proxied when on", cloudflare.RecordParams{Type: "AAAA", Name: "app.example.com", Content: "2606::1"}, true, false, "true"},
		{"mail A stays DNS-only", cloudflare.RecordParams{Type: "A", Name: "mail.example.com", Content: "1.2.3.4"}, true, false, "false"},
		{"MX untouched (non-proxyable)", cloudflare.RecordParams{Type: "MX", Name: "example.com", Content: "mail.example.com"}, true, false, "nil"},
		{"SPF TXT untouched (non-proxyable)", cloudflare.RecordParams{Type: "TXT", Name: "example.com", Content: "v=spf1 ~all"}, true, false, "nil"},
		{"grey-cloud when proxyWeb off + coverable", cloudflare.RecordParams{Type: "A", Name: "example.com", Content: "1.2.3.4"}, false, false, "false"},
		// The bug this fix targets: multi-level subdomains must never be proxied
		// on a free plan (no cert), regardless of the proxyWeb toggle.
		{"deep subdomain forced DNS-only when on", cloudflare.RecordParams{Type: "A", Name: "api.saas.example.com", Content: "1.2.3.4"}, true, false, "false"},
		{"deep subdomain forced DNS-only when off", cloudflare.RecordParams{Type: "A", Name: "api.saas.example.com", Content: "1.2.3.4"}, false, false, "false"},
		{"very deep subdomain forced DNS-only", cloudflare.RecordParams{Type: "A", Name: "gg.gg.gtg.tg.example.com", Content: "1.2.3.4"}, true, false, "false"},
		// Advanced Certificate Manager / Total TLS lifts the restriction.
		{"deep subdomain proxied with advanced certs", cloudflare.RecordParams{Type: "A", Name: "api.saas.example.com", Content: "1.2.3.4"}, true, true, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			applyProxyPolicy(&p, "", "", "example.com", tc.proxyWeb, tc.adv)
			if got := show(p.Proxied); got != tc.want {
				t.Fatalf("applyProxyPolicy(%s %s, proxyWeb=%v, adv=%v) proxied = %s; want %s",
					tc.p.Type, tc.p.Name, tc.proxyWeb, tc.adv, got, tc.want)
			}
		})
	}
}

// TestProxiedDecision proves the reconcile logic the sync uses on records that
// already exist in Cloudflare — including the repair that un-proxies a
// multi-level subdomain that was wrongly orange-clouded.
func TestProxiedDecision(t *testing.T) {
	cases := []struct {
		name                          string
		typ, host, content            string
		proxyWeb, adv, current        bool
		want                          bool
	}{
		{"repair broken deep subdomain", "A", "api.saas.example.com", "1.2.3.4", false, false, true, false},
		{"repair deep subdomain even with proxyWeb on", "A", "betazen.w.example.com", "1.2.3.4", true, false, true, false},
		{"preserve proxied 1-level when off", "A", "api.example.com", "1.2.3.4", false, false, true, true},
		{"apply toggle to grey 1-level", "A", "api.example.com", "1.2.3.4", true, false, false, true},
		{"leave grey 1-level grey when off", "A", "api.example.com", "1.2.3.4", false, false, false, false},
		{"mail never proxied", "A", "mail.example.com", "1.2.3.4", true, false, false, false},
		{"MX never proxied", "MX", "example.com", "mail.example.com", true, false, true, false},
		{"deep subdomain proxied with advanced certs", "A", "api.saas.example.com", "1.2.3.4", true, true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := proxiedDecision(tc.typ, tc.host, tc.content, "example.com", tc.proxyWeb, tc.adv, tc.current)
			if got != tc.want {
				t.Fatalf("proxiedDecision(%s %s, proxyWeb=%v, adv=%v, current=%v) = %v; want %v",
					tc.typ, tc.host, tc.proxyWeb, tc.adv, tc.current, got, tc.want)
			}
		})
	}
}

// TestResolveProxied proves the 3-level precedence (record → domain → system)
// and that the two hard-safety gates (mail, multi-level coverage) override every
// tier.
func TestResolveProxied(t *testing.T) {
	cases := []struct {
		name                     string
		host                     string
		recordMode, zoneMode     string
		proxyWeb, adv, current   bool
		want                     bool
	}{
		// Precedence: most specific wins.
		{"record on beats zone off", "api.example.com", "on", "off", false, false, false, true},
		{"record off beats zone on", "api.example.com", "off", "on", true, false, true, false},
		{"zone on when record default", "api.example.com", "", "on", false, false, false, true},
		{"zone off when record default", "api.example.com", "", "off", true, false, true, false},
		{"both default → system on", "api.example.com", "", "", true, false, false, true},
		{"both default → system off preserves current(true)", "api.example.com", "", "", false, false, true, true},
		{"both default → system off preserves current(false)", "api.example.com", "", "", false, false, false, false},
		// Hard safety overrides every explicit mode.
		{"mail forced off even record on", "mail.example.com", "on", "on", true, false, true, false},
		{"deep subdomain record on but no ACM → off", "api.saas.example.com", "on", "on", true, false, true, false},
		{"deep subdomain zone on but no ACM → off", "api.saas.example.com", "", "on", true, false, true, false},
		// Advanced certs lift the multi-level restriction, honouring the mode.
		{"deep subdomain record on with ACM → on", "api.saas.example.com", "on", "", false, true, false, true},
		{"deep subdomain record off with ACM → off", "api.saas.example.com", "off", "on", true, true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveProxied("A", tc.host, "1.2.3.4", "example.com", tc.recordMode, tc.zoneMode, tc.proxyWeb, tc.adv, tc.current)
			if got != tc.want {
				t.Fatalf("resolveProxied(%s rec=%q zone=%q web=%v adv=%v cur=%v) = %v; want %v",
					tc.host, tc.recordMode, tc.zoneMode, tc.proxyWeb, tc.adv, tc.current, got, tc.want)
			}
		})
	}
}
