package services

import (
	"encoding/hex"
	"testing"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/cloudflare"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/crypto"
)

func intPtr(n int) *int { return &n }

// TestReencryptForTransfer proves the Cloudflare token survives a server
// migration: a cipher sealed under the SOURCE key is re-keyed to the
// DESTINATION key so the destination can decrypt it (and the original source
// cipher does NOT decrypt under the destination key).
func TestReencryptForTransfer(t *testing.T) {
	srcKey := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") // 32 bytes
	dstKey := []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") // 32 bytes
	token := "cf-token-abc123"

	srcCipher, err := crypto.EncryptGCM([]byte(token), srcKey)
	if err != nil {
		t.Fatal(err)
	}
	cf := &CloudflareService{encKey: dstKey}
	newCipher, err := cf.ReencryptForTransfer(srcCipher, hex.EncodeToString(srcKey))
	if err != nil || len(newCipher) == 0 {
		t.Fatalf("reencrypt: cipher len=%d err=%v", len(newCipher), err)
	}
	plain, err := crypto.DecryptGCM(newCipher, dstKey)
	if err != nil {
		t.Fatalf("decrypt with destination key: %v", err)
	}
	if string(plain) != token {
		t.Fatalf("round-trip got %q want %q", plain, token)
	}
	// The original source cipher must NOT decrypt under the destination key
	// (proves re-encryption was actually necessary).
	if _, err := crypto.DecryptGCM(srcCipher, dstKey); err == nil {
		t.Error("source cipher unexpectedly decrypted under destination key")
	}
}

func TestClassifyRecord(t *testing.T) {
	cases := []struct {
		name                string
		typ, rname, val, mb string
		want                string
	}{
		{"mx is mail", "MX", "@", "mail.example.com.", "", "mail"},
		{"spf txt is mail", "TXT", "@", "v=spf1 ip4:1.2.3.4 ~all", "", "mail"},
		{"dmarc txt is mail", "TXT", "_dmarc", "v=DMARC1; p=none", "", "mail"},
		{"dmarc by name", "TXT", "_dmarc.sub", "whatever", "", "mail"},
		{"dkim by name", "TXT", "mail._domainkey", "v=DKIM1; k=rsa", "", "mail"},
		{"mail A is mail", "A", "mail", "1.2.3.4", "", "mail"},
		{"mail.sub A is mail", "A", "mail.shop", "1.2.3.4", "", "mail"},
		{"apex A is web", "A", "@", "1.2.3.4", "", "web"},
		{"www cname is web", "CNAME", "www", "example.com.", "", "web"},
		{"marker overrides heuristic (web)", "MX", "@", "mail.example.com.", "web", "web"},
		{"marker overrides heuristic (mail)", "A", "@", "1.2.3.4", "mail", "mail"},
		{"user marker", "A", "custom", "9.9.9.9", "user", "user"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyRecord(c.typ, c.rname, c.val, c.mb); got != c.want {
				t.Fatalf("classifyRecord(%q,%q,%q,%q) = %q, want %q", c.typ, c.rname, c.val, c.mb, got, c.want)
			}
		})
	}
}

func TestIsMailRecord(t *testing.T) {
	if !isMailRecord("MX", "@", "mail.x.", "") {
		t.Error("MX should be mail")
	}
	if isMailRecord("A", "@", "1.2.3.4", "") {
		t.Error("apex A should not be mail")
	}
}

func TestNormalizeName(t *testing.T) {
	cases := []struct{ in, domain, want string }{
		{"@", "example.com", "example.com"},
		{"", "example.com", "example.com"},
		{"www", "example.com", "www.example.com"},
		{"mail", "example.com", "mail.example.com"},
		{"WWW", "example.com", "www.example.com"},
		{"www.example.com.", "example.com", "www.example.com"},
		{"example.com", "example.com", "example.com"},
	}
	for _, c := range cases {
		if got := normalizeName(c.in, c.domain); got != c.want {
			t.Errorf("normalizeName(%q,%q) = %q, want %q", c.in, c.domain, got, c.want)
		}
	}
}

func TestNormalizeValue(t *testing.T) {
	// trailing dot + case
	if got := normalizeValue("CNAME", "Example.COM.", nil); got != "example.com" {
		t.Errorf("cname normalize = %q", got)
	}
	// MX with separate priority field
	if got := normalizeValue("MX", "mail.example.com.", intPtr(10)); got != "10 mail.example.com" {
		t.Errorf("mx w/ priority field = %q, want '10 mail.example.com'", got)
	}
	// MX with priority embedded in value, no priority field — should match the above
	if got := normalizeValue("MX", "10 mail.example.com.", nil); got != "10 mail.example.com" {
		t.Errorf("mx embedded priority = %q, want '10 mail.example.com'", got)
	}
}

func TestLocalToParams_MX(t *testing.T) {
	r := models.DNSRecord{Type: "mx", Name: "@", Value: "10 mail.example.com.", TTL: 3600}
	p := localToParams(r, "example.com")
	if p.Type != "MX" {
		t.Errorf("type = %q", p.Type)
	}
	if p.Content != "mail.example.com" {
		t.Errorf("content = %q, want mail.example.com (priority split out)", p.Content)
	}
	if p.Priority == nil || *p.Priority != 10 {
		t.Errorf("priority = %v, want 10", p.Priority)
	}
	if p.Name != "example.com" {
		t.Errorf("name = %q, want fqdn", p.Name)
	}
	// MX is a mail record — must be forced DNS-only.
	if p.Proxied == nil || *p.Proxied != false {
		t.Errorf("MX proxied = %v, want explicit false", p.Proxied)
	}
}

func TestLocalToParams_TTLClamp(t *testing.T) {
	// Sub-minimum TTL (bootstrap 30s) must clamp to Cloudflare's 60 floor.
	r := models.DNSRecord{Type: "A", Name: "@", Value: "1.2.3.4", TTL: 30}
	if p := localToParams(r, "example.com"); p.TTL != 60 {
		t.Errorf("TTL = %d, want clamped to 60", p.TTL)
	}
	// TTL 1 (auto) is preserved.
	r2 := models.DNSRecord{Type: "A", Name: "@", Value: "1.2.3.4", TTL: 1}
	if p := localToParams(r2, "example.com"); p.TTL != 1 {
		t.Errorf("auto TTL = %d, want 1", p.TTL)
	}
}

func TestStripTXTQuoting(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"v=spf1 ip4:1.2.3.4 ~all"`, "v=spf1 ip4:1.2.3.4 ~all"},
		{`v=spf1 ip4:1.2.3.4 ~all`, "v=spf1 ip4:1.2.3.4 ~all"},
		{`"v=DKIM1; k=rsa; p=AAAA" "BBBB"`, "v=DKIM1; k=rsa; p=AAAABBBB"},
	}
	for _, c := range cases {
		if got := stripTXTQuoting(c.in); got != c.want {
			t.Errorf("stripTXTQuoting(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A quoted PowerDNS SPF and an unquoted Cloudflare SPF must normalize equal so
// the sync treats them as the SAME record (no duplicate created).
func TestNormalizeValue_TXTQuotingMatches(t *testing.T) {
	local := normalizeValue("TXT", `"v=spf1 ip4:1.2.3.4 ~all"`, nil)
	cf := normalizeValue("TXT", `v=spf1 ip4:1.2.3.4 ~all`, nil)
	if local != cf {
		t.Fatalf("quoted vs unquoted TXT should match: %q != %q", local, cf)
	}
}

func TestLocalToParams_TXTStripsQuotes(t *testing.T) {
	r := models.DNSRecord{Type: "TXT", Name: "@", Value: `"v=spf1 ip4:1.2.3.4 ~all"`, TTL: 3600}
	p := localToParams(r, "example.com")
	if p.Content != "v=spf1 ip4:1.2.3.4 ~all" {
		t.Errorf("TXT content = %q, want unquoted", p.Content)
	}
	// SPF is a mail record → must be forced DNS-only.
	if p.Proxied == nil || *p.Proxied != false {
		t.Errorf("SPF proxied = %v, want false", p.Proxied)
	}
}

func TestForceMailDNSOnly(t *testing.T) {
	// Mail record: proxied forced off even if requested on.
	on := true
	mail := cloudflare.RecordParams{Type: "A", Name: "mail.example.com", Content: "1.2.3.4", Proxied: &on}
	forceMailDNSOnly(&mail)
	if mail.Proxied == nil || *mail.Proxied != false {
		t.Errorf("mail proxied = %v, want false", mail.Proxied)
	}
	// Web record: proxied left as-is.
	web := cloudflare.RecordParams{Type: "A", Name: "example.com", Content: "1.2.3.4", Proxied: &on}
	forceMailDNSOnly(&web)
	if web.Proxied == nil || *web.Proxied != true {
		t.Errorf("web proxied = %v, want left true", web.Proxied)
	}
}
