package services

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ssrf_guard.go hardens the panel's OUTBOUND HTTP clients (outbound webhook
// delivery + notification fan-out) against Server-Side Request Forgery.
//
// Before v3.1.108 these clients POSTed to any operator/developer-supplied URL
// with only an http(s):// prefix check — no guard against the host resolving
// to a private/loopback/link-local address. Because the panel runs on the
// same box as the PowerDNS HTTP API (127.0.0.1:8081), MongoDB (27017),
// MariaDB (3306) and cloud metadata (169.254.169.254), a webhook/notification
// URL pointed at those internal services would have them reached with the
// panel's local privileges.
//
// newGuardedHTTPClient returns an http.Client whose DialContext resolves the
// target host and refuses to connect if ANY resolved IP is private/loopback/
// link-local/unspecified, then dials the validated IP explicitly so a second
// resolution can't swap in a blocked address (DNS-rebinding defense).
func newGuardedHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				// Reject outright if ANY resolved address is blocked — a host
				// that resolves to a mix of public + private is suspicious and
				// not worth racing.
				for _, ip := range ips {
					if isBlockedIP(ip.IP) {
						return nil, fmt.Errorf("ssrf guard: refusing to connect to non-public address %s (host %q)", ip.IP, host)
					}
				}
				if len(ips) == 0 {
					return nil, fmt.Errorf("ssrf guard: no address for host %q", host)
				}
				// Dial the validated IP directly (not the hostname) so the
				// connection can't be rebound to a different IP between our
				// check and the dial.
				return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
			},
		},
	}
}

// isBlockedIP reports whether an IP is one the panel must never be coerced
// into contacting via a user-supplied URL: loopback, RFC1918 / ULA private,
// link-local (incl. the 169.254.169.254 cloud-metadata endpoint), and the
// unspecified address. Handles IPv4-mapped IPv6 via the stdlib helpers.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsUnspecified()
}
