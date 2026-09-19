package services

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// BIMIService resolves and serves the SENDER's BIMI logo for incoming mail. It
// does a cached DNS lookup of default._bimi.<domain>, and (lazily, when the
// browser requests the image) fetches the referenced SVG over HTTPS with strict
// SSRF protection + sanitization, then caches it.
//
// SECURITY:
//   - A logo is only ever ASSOCIATED with a message that passed DMARC (the gate
//     lives in the caller: SenderLogoURL is only stamped onto a message when
//     dmarcPassed() is true). This stops a spoofed sender from borrowing a brand's
//     logo — the core BIMI anti-phishing property.
//   - The fetch blocks private/loopback/link-local targets (SSRF), requires
//     https, caps size (32 KB), requires an SVG content-type, refuses redirects,
//     and sanitizes the SVG before it is ever served from our origin.
type BIMIService struct {
	client *http.Client
	mu     sync.RWMutex
	cache  map[string]*bimiEntry
}

type bimiEntry struct {
	hasRecord bool      // default._bimi TXT with an l= URL exists
	url       string    // the l= URL
	svg       []byte    // fetched + sanitized SVG (nil until first image request)
	fetchedAt time.Time // when the DNS lookup was done
	svgAt     time.Time // when the SVG was fetched
	svgOK     bool
}

const (
	bimiPosTTL  = 6 * time.Hour       // re-check a domain that HAS a record every 6h
	bimiNegTTL  = 1 * time.Hour       // re-check a domain with NO record every 1h
	bimiSVGTTL  = 6 * time.Hour       // re-fetch the SVG every 6h
	bimiMaxSVG  = 32 * 1024           // BIMI SVG size ceiling
	bimiTimeout = 6 * time.Second     // per DNS/HTTP operation
)

// NewBIMIService builds the service with an SSRF-guarded HTTP client.
func NewBIMIService() *BIMIService {
	// The custom DialContext re-resolves the target and refuses any non-public
	// IP, so a BIMI l= pointing at an internal address can't be reached even via
	// DNS trickery. Redirects are refused separately (CheckRedirect).
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if !isPublicIP(ip) {
					return nil, fmt.Errorf("refusing to connect to non-public address %s", ip)
				}
			}
			// Dial the first public IP explicitly so we connect to a vetted target.
			d := &net.Dialer{Timeout: bimiTimeout}
			return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
		TLSHandshakeTimeout:   bimiTimeout,
		ResponseHeaderTimeout: bimiTimeout,
		DisableKeepAlives:     true,
	}
	return &BIMIService{
		client: &http.Client{
			Timeout:   bimiTimeout,
			Transport: transport,
			// Refuse redirects — a BIMI l= should be a direct https SVG, and a
			// redirect is a classic SSRF-guard bypass.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return fmt.Errorf("redirects not allowed")
			},
		},
		cache: map[string]*bimiEntry{},
	}
}

// isPublicIP rejects loopback / private / link-local / unspecified / ULA targets.
func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	return true
}

var bimiLValue = regexp.MustCompile(`(?i)\bl\s*=\s*([^;\s]+)`)

// lookupRecord does the cached DNS lookup for default._bimi.<domain> and returns
// the l= URL. Positive + negative results are cached with different TTLs.
func (s *BIMIService) lookupRecord(ctx context.Context, domain string) (string, bool) {
	domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(domain, ".")))
	if domain == "" {
		return "", false
	}
	now := time.Now()
	s.mu.RLock()
	if e, ok := s.cache[domain]; ok {
		ttl := bimiNegTTL
		if e.hasRecord {
			ttl = bimiPosTTL
		}
		if now.Sub(e.fetchedAt) < ttl {
			url, has := e.url, e.hasRecord
			s.mu.RUnlock()
			return url, has
		}
	}
	s.mu.RUnlock()

	lc, cancel := context.WithTimeout(ctx, bimiTimeout)
	defer cancel()
	txts, _ := net.DefaultResolver.LookupTXT(lc, "default._bimi."+domain)
	url := ""
	for _, t := range txts {
		if !strings.Contains(strings.ToLower(t), "v=bimi1") {
			continue
		}
		if m := bimiLValue.FindStringSubmatch(t); len(m) == 2 {
			candidate := strings.Trim(m[1], `"' `)
			if strings.HasPrefix(strings.ToLower(candidate), "https://") {
				url = candidate
			}
			break
		}
	}
	s.mu.Lock()
	e := s.cache[domain]
	if e == nil {
		e = &bimiEntry{}
		s.cache[domain] = e
	}
	e.hasRecord = url != ""
	e.url = url
	e.fetchedAt = now
	s.mu.Unlock()
	return url, url != ""
}

// SenderLogoURL returns the same-origin proxy path the frontend should use for a
// sender domain's logo, or "" when the domain publishes no usable BIMI record.
// Callers MUST only invoke this for messages that passed DMARC.
func (s *BIMIService) SenderLogoURL(ctx context.Context, domain string) string {
	if _, ok := s.lookupRecord(ctx, domain); !ok {
		return ""
	}
	return "/bimi/logo/" + strings.ToLower(strings.TrimSpace(domain)) + ".svg"
}

// LogoBytes returns the sanitized SVG for a domain, fetching + caching on demand.
// Backs the GET /bimi/logo/:domain proxy endpoint.
func (s *BIMIService) LogoBytes(ctx context.Context, domain string) ([]byte, bool) {
	domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(domain, ".")))
	url, ok := s.lookupRecord(ctx, domain)
	if !ok {
		return nil, false
	}
	now := time.Now()
	s.mu.RLock()
	if e, ok := s.cache[domain]; ok && e.svg != nil && now.Sub(e.svgAt) < bimiSVGTTL {
		svg := e.svg
		s.mu.RUnlock()
		return svg, e.svgOK
	}
	s.mu.RUnlock()

	svg, err := s.fetchSVG(ctx, url)
	s.mu.Lock()
	e := s.cache[domain]
	if e == nil {
		e = &bimiEntry{hasRecord: true, url: url, fetchedAt: now}
		s.cache[domain] = e
	}
	e.svgAt = now
	if err != nil || svg == nil {
		e.svg = []byte{} // cache the failure so we don't refetch every image load
		e.svgOK = false
		s.mu.Unlock()
		return nil, false
	}
	e.svg = svg
	e.svgOK = true
	s.mu.Unlock()
	return svg, true
}

// fetchSVG performs the SSRF-guarded HTTPS GET + validation + sanitization.
func (s *BIMIService) fetchSVG(ctx context.Context, rawURL string) ([]byte, error) {
	if !strings.HasPrefix(strings.ToLower(rawURL), "https://") {
		return nil, fmt.Errorf("BIMI logo URL must be https")
	}
	rc, cancel := context.WithTimeout(ctx, bimiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rc, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Betazen-MailSuite-BIMI/1.0")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("BIMI logo fetch: HTTP %d", resp.StatusCode)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(ct, "svg") {
		return nil, fmt.Errorf("BIMI logo is not an SVG (content-type %q)", ct)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, bimiMaxSVG+1))
	if err != nil {
		return nil, err
	}
	if len(body) > bimiMaxSVG {
		return nil, fmt.Errorf("BIMI logo exceeds %d bytes", bimiMaxSVG)
	}
	if err := bimiSVGSafe(body); err != nil {
		return nil, err
	}
	return body, nil
}

var (
	reBSVGRoot   = regexp.MustCompile(`(?i)<\s*svg`)
	reBSVGScript = regexp.MustCompile(`(?i)<\s*script\b|<\s*foreignobject\b|<\s*a\b|<\s*image\b|<\s*(?:animate|set)\b`)
	reBSVGEvent  = regexp.MustCompile(`(?i)\son[a-z]+\s*=|javascript\s*:`)
	reBSVGExtRef = regexp.MustCompile(`(?i)(?:xlink:)?href\s*=\s*["']?\s*(?:https?:)?//`)
	reBSVGDoctp  = regexp.MustCompile(`(?i)<!doctype|<!entity`)
)

// bimiSVGSafe rejects active/unsafe content in a fetched sender SVG before we
// serve it from our own origin. Mirrors the panel-side sanitizer.
func bimiSVGSafe(raw []byte) error {
	low := strings.ToLower(string(raw))
	switch {
	case !reBSVGRoot.MatchString(low):
		return fmt.Errorf("not an SVG")
	case reBSVGDoctp.MatchString(low):
		return fmt.Errorf("SVG contains a DOCTYPE/entity")
	case reBSVGScript.MatchString(low):
		return fmt.Errorf("SVG contains active/disallowed elements")
	case reBSVGEvent.MatchString(low):
		return fmt.Errorf("SVG contains event handlers or javascript:")
	case reBSVGExtRef.MatchString(low):
		return fmt.Errorf("SVG references external resources")
	}
	return nil
}

var reDMARCPass = regexp.MustCompile(`(?i)\bdmarc\s*=\s*pass\b`)

// dmarcPassed reports whether an Authentication-Results header value indicates a
// DMARC pass. This is the anti-phishing gate for showing a sender's logo.
func dmarcPassed(authResults string) bool {
	return authResults != "" && reDMARCPass.MatchString(authResults)
}

// senderDomain extracts the lowercase domain from an email address. Robust to a
// full "Name <addr>" string sneaking in (defensive — From[0].Address is normally
// a bare addr) by stripping the angle-bracketed part.
func senderDomain(address string) string {
	address = strings.TrimSpace(strings.ToLower(address))
	if i := strings.LastIndex(address, "<"); i >= 0 {
		address = address[i+1:]
	}
	address = strings.TrimSuffix(strings.TrimSpace(address), ">")
	at := strings.LastIndex(address, "@")
	if at < 0 || at == len(address)-1 {
		return ""
	}
	dom := strings.TrimRight(address[at+1:], "> \t")
	return dom
}
