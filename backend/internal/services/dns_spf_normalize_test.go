package services

import "testing"

// TestNormalizeBootstrapSPF verifies the migration-safety contract of the SPF
// reconcile: it rewrites a panel-bootstrap SPF that hard-codes one of THIS
// panel's own historical IPs into the IP-change-proof `a:<mailhost>` form, and
// it NEVER touches a custom SPF or one authorising a genuine third party.
func TestNormalizeBootstrapSPF(t *testing.T) {
	const mailHost = "mailmx.betazeninfotech.com"
	const canonical = "v=spf1 a:mailmx.betazeninfotech.com ~all"
	// The panel's own historical IPs (current + previous + ip_history).
	panelIPs := map[string]bool{
		"187.127.167.190": true, // current
		"187.127.150.159": true, // previous
		"187.127.172.193": true, // original
	}

	cases := []struct {
		name    string
		in      string
		wantOut string
		wantOK  bool
	}{
		{"current panel ip", "v=spf1 ip4:187.127.167.190 ~all", canonical, true},
		{"previous panel ip", "v=spf1 ip4:187.127.150.159 ~all", canonical, true},
		{"ancient panel ip (the sayantankar.com case)", "v=spf1 ip4:187.127.172.193 ~all", canonical, true},
		{"quoted value", "\"v=spf1 ip4:187.127.172.193 ~all\"", canonical, true},
		{"cidr suffix", "v=spf1 ip4:187.127.172.193/32 ~all", canonical, true},
		{"hard fail qualifier", "v=spf1 ip4:187.127.150.159 -all", canonical, true},
		{"neutral qualifier", "v=spf1 ip4:187.127.167.190 ?all", canonical, true},

		// MUST be left alone:
		{"already canonical", canonical, "", false},
		{"third-party ip (NOT ours) — never touch", "v=spf1 ip4:187.127.181.182 ~all", "", false},
		{"google include — custom, never touch", "v=spf1 include:_spf.google.com ~all", "", false},
		{"multi-mechanism — never touch", "v=spf1 ip4:187.127.172.193 include:sendgrid.net ~all", "", false},
		{"a-mechanism already — never touch", "v=spf1 a mx ~all", "", false},
		{"ip6 — out of scope, never touch", "v=spf1 ip6:2001:db8::1 ~all", "", false},
		{"not spf", "v=DKIM1; k=rsa; p=abc", "", false},
		{"empty", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizeBootstrapSPF(tc.in, mailHost, panelIPs)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (in=%q)", ok, tc.wantOK, tc.in)
			}
			if got != tc.wantOut {
				t.Fatalf("out = %q, want %q (in=%q)", got, tc.wantOut, tc.in)
			}
		})
	}
}

// TestNormalizeBootstrapSPF_EmptyPanelIPs — with no known panel IPs the reconcile
// must rewrite NOTHING (it can't prove any ip4 is its own), even a bootstrap-shaped
// record, so an unseeded server-identity row can never cause a wrong rewrite.
func TestNormalizeBootstrapSPF_EmptyPanelIPs(t *testing.T) {
	if _, ok := normalizeBootstrapSPF("v=spf1 ip4:187.127.172.193 ~all", "mailmx.betazeninfotech.com", map[string]bool{}); ok {
		t.Fatal("rewrote with no known panel IPs — must be a no-op")
	}
}
