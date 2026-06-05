package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/config"
)

type DNSRecordStatus struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Found    string `json:"found,omitempty"`
	OK       bool   `json:"ok"`
}

type DNSStatus struct {
	Domain  string            `json:"domain"`
	Records []DNSRecordStatus `json:"records"`
	AllOK   bool              `json:"all_ok"`
}

type DNSService struct {
	cfg   *config.Config
	panel *BetazenPanelClient
}

func NewDNSService(cfg *config.Config, panel *BetazenPanelClient) *DNSService {
	return &DNSService{cfg: cfg, panel: panel}
}

// EnableMail upserts the canonical mail records for a domain via the Betazen panel.
// Records: MX, A (mail.<domain>), TXT SPF, TXT DKIM, TXT DMARC.
func (s *DNSService) EnableMail(ctx context.Context, domain string) (*DNSStatus, error) {
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "" {
		return nil, errors.New("domain required")
	}
	if !s.panel.Configured() {
		return nil, ErrPanelNotConfigured
	}

	// 1. Ensure DKIM key exists; the panel returns 200 if already set up.
	_ = s.panel.SetupDKIM(ctx, domain)

	mailHost := "mail." + domain
	records := []addRecordReq{
		{Name: "@", Type: "MX", TTL: "300", Value: "10 " + mailHost + "."},
		{Name: "mail", Type: "A", TTL: "300", Value: ""}, // panel resolves server IP itself
		{Name: "@", Type: "TXT", TTL: "300", Value: "v=spf1 mx ~all"},
		{Name: "_dmarc", Type: "TXT", TTL: "300", Value: "v=DMARC1; p=quarantine; rua=mailto:dmarc@" + domain + "; adkim=s; aspf=s"},
		// DKIM TXT is created/managed by SetupDKIM above.
	}
	for _, r := range records {
		if r.Type == "A" && r.Value == "" {
			continue // panel populates server IP when value is empty
		}
		if err := s.panel.AddDNSRecord(ctx, domain, r.Name, r.Type, r.TTL, r.Value); err != nil {
			return nil, fmt.Errorf("add %s record: %w", r.Type, err)
		}
	}
	return s.Verify(ctx, domain)
}

func (s *DNSService) Verify(ctx context.Context, domain string) (*DNSStatus, error) {
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "" {
		return nil, errors.New("domain required")
	}
	timeout, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	resolver := &net.Resolver{PreferGo: true}

	out := &DNSStatus{Domain: domain}
	mailHost := "mail." + domain

	// MX
	mx, _ := resolver.LookupMX(timeout, domain)
	mxFound := ""
	for _, m := range mx {
		mxFound = m.Host
		break
	}
	out.Records = append(out.Records, DNSRecordStatus{
		Type: "MX", Name: "@", Expected: mailHost + ".", Found: mxFound,
		OK: strings.EqualFold(mxFound, mailHost+"."),
	})
	// A mail.<domain>
	ips, _ := resolver.LookupIP(timeout, "ip4", mailHost)
	aFound := ""
	if len(ips) > 0 {
		aFound = ips[0].String()
	}
	out.Records = append(out.Records, DNSRecordStatus{
		Type: "A", Name: "mail", Expected: "(server IP)", Found: aFound, OK: aFound != "",
	})
	// SPF
	txts, _ := resolver.LookupTXT(timeout, domain)
	spfFound := ""
	for _, t := range txts {
		if strings.HasPrefix(t, "v=spf1") {
			spfFound = t
			break
		}
	}
	out.Records = append(out.Records, DNSRecordStatus{
		Type: "TXT", Name: "@ (SPF)", Expected: "v=spf1 mx ~all", Found: spfFound, OK: spfFound != "",
	})
	// DMARC
	dtxts, _ := resolver.LookupTXT(timeout, "_dmarc."+domain)
	dmarcFound := ""
	for _, t := range dtxts {
		if strings.HasPrefix(t, "v=DMARC1") {
			dmarcFound = t
			break
		}
	}
	out.Records = append(out.Records, DNSRecordStatus{
		Type: "TXT", Name: "_dmarc", Expected: "v=DMARC1; p=quarantine; ...", Found: dmarcFound, OK: dmarcFound != "",
	})
	// DKIM
	ktxts, _ := resolver.LookupTXT(timeout, "default._domainkey."+domain)
	dkimFound := ""
	for _, t := range ktxts {
		if strings.Contains(t, "v=DKIM1") {
			dkimFound = t
			break
		}
	}
	out.Records = append(out.Records, DNSRecordStatus{
		Type: "TXT", Name: "default._domainkey", Expected: "v=DKIM1; ...", Found: dkimFound, OK: dkimFound != "",
	})

	out.AllOK = true
	for _, r := range out.Records {
		if !r.OK {
			out.AllOK = false
			break
		}
	}
	return out, nil
}
