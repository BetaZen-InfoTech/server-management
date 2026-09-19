package services

import "testing"

func TestNormalizeDNSProvider(t *testing.T) {
	cases := map[string]string{
		"cloudflare":   DNSProviderCloudflare,
		"Cloudflare":   DNSProviderCloudflare,
		" CF ":         DNSProviderCloudflare,
		"powerdns":     DNSProviderPowerDNS,
		"PowerDNS":     DNSProviderPowerDNS,
		"betazen":      DNSProviderPowerDNS,
		"betazen-dns":  DNSProviderPowerDNS,
		"pdns":         DNSProviderPowerDNS,
		"bind":         DNSProviderPowerDNS,
		"":             "",
		"nonsense":     "",
	}
	for in, want := range cases {
		if got := NormalizeDNSProvider(in); got != want {
			t.Errorf("NormalizeDNSProvider(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveDNSProvider(t *testing.T) {
	// Explicit request value always wins, whatever the global default.
	s := &DomainService{defaultDNSProvider: func() string { return "powerdns" }}
	if got := s.resolveDNSProvider("cloudflare"); got != DNSProviderCloudflare {
		t.Fatalf("explicit cloudflare must win over powerdns default, got %q", got)
	}
	if got := s.resolveDNSProvider("powerdns"); got != DNSProviderPowerDNS {
		t.Fatalf("explicit powerdns must be honored, got %q", got)
	}
	// Empty request follows the global default — both directions.
	if got := s.resolveDNSProvider(""); got != DNSProviderPowerDNS {
		t.Fatalf("empty must follow the powerdns global default, got %q", got)
	}
	sc := &DomainService{defaultDNSProvider: func() string { return "cloudflare" }}
	if got := sc.resolveDNSProvider(""); got != DNSProviderCloudflare {
		t.Fatalf("empty must follow the cloudflare global default, got %q", got)
	}
	// …and PowerDNS is the product default when neither request nor global set.
	s2 := &DomainService{}
	if got := s2.resolveDNSProvider(""); got != DNSProviderPowerDNS {
		t.Fatalf("empty with no global default must be powerdns, got %q", got)
	}
	s3 := &DomainService{defaultDNSProvider: func() string { return "" }}
	if got := s3.resolveDNSProvider(""); got != DNSProviderPowerDNS {
		t.Fatalf("empty global default must fall back to powerdns, got %q", got)
	}
}
