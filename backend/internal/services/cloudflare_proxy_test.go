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

// TestApplyProxyPolicy proves the "proxy web records" policy: eligible web
// records get orange-clouded when on, mail + non-proxyable stay DNS-only, and
// it's a no-op when off.
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
		want     string // "true" | "false" | "nil"
	}{
		{"apex A proxied when on", cloudflare.RecordParams{Type: "A", Name: "example.com", Content: "1.2.3.4"}, true, "true"},
		{"www CNAME proxied when on", cloudflare.RecordParams{Type: "CNAME", Name: "www.example.com", Content: "example.com"}, true, "true"},
		{"subdomain AAAA proxied when on", cloudflare.RecordParams{Type: "AAAA", Name: "app.example.com", Content: "2606::1"}, true, "true"},
		{"mail A stays DNS-only", cloudflare.RecordParams{Type: "A", Name: "mail.example.com", Content: "1.2.3.4"}, true, "false"},
		{"MX never proxied", cloudflare.RecordParams{Type: "MX", Name: "example.com", Content: "mail.example.com"}, true, "false"},
		{"SPF TXT never proxied", cloudflare.RecordParams{Type: "TXT", Name: "example.com", Content: "v=spf1 ~all"}, true, "false"},
		{"no-op when proxyWeb off", cloudflare.RecordParams{Type: "A", Name: "example.com", Content: "1.2.3.4"}, false, "nil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			applyProxyPolicy(&p, tc.proxyWeb)
			if got := show(p.Proxied); got != tc.want {
				t.Fatalf("applyProxyPolicy(%s %s, proxyWeb=%v) proxied = %s; want %s",
					tc.p.Type, tc.p.Name, tc.proxyWeb, got, tc.want)
			}
		})
	}
}
