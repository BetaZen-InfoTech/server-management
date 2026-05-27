package mailer

import (
	"strings"
	"testing"
)

// TestBuildDomainExpiry_IncludesEveryDetail asserts the v3.1.59
// requirement that the expiry-warning email surfaces every detail
// the vendor needs to act — Registrar, Purchased on (RegisteredOn),
// Expires on, Days left, Auto-renew state, AND every Nameserver.
// Pre-3.1.59 the template only showed Registrar + Expires + Days
// left; a vendor diagnosing "I renewed but the panel still warns"
// couldn't see the nameservers in-email and had to log in to check.
func TestBuildDomainExpiry_IncludesEveryDetail(t *testing.T) {
	subject, text, htmlBody, err := BuildDomainExpiry(DomainExpiryData{
		Name:         "Acme Holdings",
		Email:        "ops@acme.example",
		Domain:       "acme.example",
		Registrar:    "GoDaddy LLC",
		RegisteredOn: "15 Jan 2024",
		ExpiresOn:    "15 Jan 2026",
		DaysLeft:     30,
		AutoRenew:    true,
		Nameservers:  []string{"ns1.cloudflare.com", "ns2.cloudflare.com"},
		PanelName:    "Betazen Server Panel",
		PanelURL:     "https://panel.example/user-panel",
	})
	if err != nil {
		t.Fatalf("BuildDomainExpiry returned err: %v", err)
	}
	if !strings.Contains(subject, "acme.example") {
		t.Errorf("subject missing domain: %q", subject)
	}

	// Plain-text body — every detail field must appear.
	for _, want := range []string{
		"acme.example",         // domain
		"GoDaddy LLC",          // registrar
		"15 Jan 2024",          // purchased on
		"15 Jan 2026",          // expires on
		"30 days",              // days left (pluralised)
		"ns1.cloudflare.com",   // first nameserver
		"ns2.cloudflare.com",   // second nameserver
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plain-text body missing %q. Full body:\n%s", want, text)
		}
	}

	// HTML body — same detail set, also escaped for the < > " &
	// characters that a hostile registrar string could contain.
	for _, want := range []string{
		"acme.example",
		"GoDaddy LLC",
		"15 Jan 2024",
		"15 Jan 2026",
		"ns1.cloudflare.com",
		"ns2.cloudflare.com",
		"Purchased on",
		"Nameservers",
		"Auto-renew",
	} {
		if !strings.Contains(htmlBody, want) {
			t.Errorf("html body missing %q", want)
		}
	}
}

// TestBuildDomainExpiry_HidesOptionalFieldsWhenBlank asserts that a
// minimal-data email (operator never WHOIS-refreshed; only ExpiresOn
// set) doesn't render an empty "Registrar:" or "Purchased on:" row.
// We'd rather omit a row than render a stub that says "Registrar:".
func TestBuildDomainExpiry_HidesOptionalFieldsWhenBlank(t *testing.T) {
	_, text, htmlBody, err := BuildDomainExpiry(DomainExpiryData{
		Name:      "vendor",
		Email:     "v@example",
		Domain:    "minimal.example",
		ExpiresOn: "01 Jan 2026",
		DaysLeft:  7,
		PanelName: "Betazen Server Panel",
		PanelURL:  "https://panel.example/user-panel",
		// Registrar, RegisteredOn, Nameservers all blank
	})
	if err != nil {
		t.Fatalf("BuildDomainExpiry returned err: %v", err)
	}
	for _, badPhrase := range []string{
		"Registrar    : \n",  // stub label with no value
		"Purchased on : \n",
		"Nameservers  : \n",
	} {
		if strings.Contains(text, badPhrase) {
			t.Errorf("plain-text body has empty-value stub %q", badPhrase)
		}
	}
	// HTML side — the {{if .Registrar}}…{{end}} guards mean those
	// rows shouldn't render at all when blank.
	if strings.Contains(htmlBody, ">Registrar<") {
		t.Error("html body shouldn't include the Registrar row when registrar is blank")
	}
	if strings.Contains(htmlBody, ">Purchased on<") {
		t.Error("html body shouldn't include the Purchased on row when registered_on is blank")
	}
	if strings.Contains(htmlBody, ">Nameservers<") {
		t.Error("html body shouldn't include the Nameservers row when nameservers is empty")
	}
}

// TestBuildDomainExpiry_EscapesHostileRegistrarString asserts the
// HTML body escapes <, >, " and & characters in registrar /
// nameserver fields. A hostile RDAP response (or a typo-fix
// migration that splats raw HTML into a registrar string) must
// NOT smuggle markup into the email.
func TestBuildDomainExpiry_EscapesHostileRegistrarString(t *testing.T) {
	_, _, htmlBody, err := BuildDomainExpiry(DomainExpiryData{
		Name:        "vendor",
		Email:       "v@example",
		Domain:      "ok.example",
		Registrar:   "<script>alert(1)</script>",
		ExpiresOn:   "01 Jan 2026",
		DaysLeft:    7,
		Nameservers: []string{`ns<inject>.example`},
		PanelName:   "Betazen Server Panel",
		PanelURL:    "https://panel.example/user-panel",
	})
	if err != nil {
		t.Fatalf("BuildDomainExpiry returned err: %v", err)
	}
	if strings.Contains(htmlBody, "<script>") {
		t.Error("html body contains unescaped <script> from registrar — XSS escape regression")
	}
	if strings.Contains(htmlBody, "ns<inject>.example") {
		t.Error("html body contains unescaped <inject> from nameserver — XSS escape regression on the Nameservers row")
	}
}

// TestBuildDomainExpiry_UrgentSubjectAtBoundary asserts the "URGENT"
// prefix fires at the user-visible boundary (≤ 3 days). A 4-day-
// out warning is bad-but-not-urgent; a 3-day or sooner one is the
// last chance to act and the inbox needs to scream.
func TestBuildDomainExpiry_UrgentSubjectAtBoundary(t *testing.T) {
	cases := []struct {
		daysLeft  int
		wantUrgent bool
	}{
		{30, false},
		{4, false},
		{3, true},
		{1, true},
	}
	for _, c := range cases {
		subject, _, _, err := BuildDomainExpiry(DomainExpiryData{
			Name:      "v",
			Email:     "v@example",
			Domain:    "x.example",
			ExpiresOn: "01 Jan 2026",
			DaysLeft:  c.daysLeft,
			PanelName: "P",
			PanelURL:  "https://p",
		})
		if err != nil {
			t.Fatalf("d=%d: %v", c.daysLeft, err)
		}
		isUrgent := strings.HasPrefix(subject, "URGENT")
		if isUrgent != c.wantUrgent {
			t.Errorf("d=%d: urgent=%v, want %v (subject: %q)", c.daysLeft, isUrgent, c.wantUrgent, subject)
		}
	}
}
