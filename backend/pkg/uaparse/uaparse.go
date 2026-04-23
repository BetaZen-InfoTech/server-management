// Package uaparse is a dependency-free user-agent parser tuned for the
// login-session tracking in this repo. It's not a general-purpose UA
// library — it covers the common browsers, OSes, and device shapes we
// show on the Sessions page and falls back to "unknown" gracefully.
//
// We deliberately avoid pulling a bigger dep (mssola/user_agent,
// ua-parser-go) because this is only used to humanise the audit row,
// not to gate behaviour.
package uaparse

import "strings"

// Parsed is the flat result of Parse(). Zero values ("") mean we
// couldn't tell from the string — the caller should display the raw
// UA instead.
type Parsed struct {
	Browser string // "Chrome", "Firefox", "Safari", "Edge", "Opera", etc.
	OS      string // "Windows 10/11", "macOS", "Linux", "iOS", "Android"
	Device  string // "desktop" | "mobile" | "tablet" | "bot"
}

// Parse extracts browser, OS, and device kind from a User-Agent header.
// It's case-insensitive on the markers it looks for (users sometimes
// run through proxies that rewrite the UA in unexpected casings).
func Parse(ua string) Parsed {
	p := Parsed{Device: "desktop"}
	if strings.TrimSpace(ua) == "" {
		return Parsed{Device: "unknown"}
	}
	u := strings.ToLower(ua)

	// Device — check bots first because they often carry a mobile
	// marker too (e.g. Googlebot-Mobile).
	switch {
	case strings.Contains(u, "bot") || strings.Contains(u, "crawler") ||
		strings.Contains(u, "spider") || strings.Contains(u, "scraper") ||
		strings.Contains(u, "curl/") || strings.Contains(u, "wget/") ||
		strings.Contains(u, "python-requests") || strings.Contains(u, "go-http-client"):
		p.Device = "bot"
	case strings.Contains(u, "ipad") || strings.Contains(u, "tablet"):
		p.Device = "tablet"
	case strings.Contains(u, "mobile") || strings.Contains(u, "iphone") || strings.Contains(u, "android"):
		// Order matters: "android" alone is often a tablet. If we see
		// "mobile" AND "android" it's a phone; "android" without
		// "mobile" is usually a tablet.
		if strings.Contains(u, "mobile") {
			p.Device = "mobile"
		} else if strings.Contains(u, "android") {
			p.Device = "tablet"
		} else {
			p.Device = "mobile"
		}
	}

	// OS — most specific wins (Windows 11 → Windows before Linux etc).
	switch {
	case strings.Contains(u, "windows nt 10"):
		p.OS = "Windows 10/11"
	case strings.Contains(u, "windows nt 6.3"):
		p.OS = "Windows 8.1"
	case strings.Contains(u, "windows nt 6.2"):
		p.OS = "Windows 8"
	case strings.Contains(u, "windows nt 6.1"):
		p.OS = "Windows 7"
	case strings.Contains(u, "windows"):
		p.OS = "Windows"
	case strings.Contains(u, "iphone") || strings.Contains(u, "ipad") || strings.Contains(u, "ios"):
		p.OS = "iOS"
	case strings.Contains(u, "mac os x") || strings.Contains(u, "macintosh"):
		p.OS = "macOS"
	case strings.Contains(u, "android"):
		p.OS = "Android"
	case strings.Contains(u, "cros"):
		p.OS = "Chrome OS"
	case strings.Contains(u, "ubuntu"):
		p.OS = "Ubuntu"
	case strings.Contains(u, "fedora"):
		p.OS = "Fedora"
	case strings.Contains(u, "linux"):
		p.OS = "Linux"
	case strings.Contains(u, "freebsd"):
		p.OS = "FreeBSD"
	}

	// Browser — order matters. Edge and Opera both impersonate Chrome,
	// Chrome impersonates Safari, so we must detect the rarer ones
	// first. The suffix on tokens like "edg/" (Edge) and "opr/" (Opera)
	// is the stable modern form; older "Edge/" is covered too.
	switch {
	case strings.Contains(u, "edg/") || strings.Contains(u, " edge/"):
		p.Browser = "Edge"
	case strings.Contains(u, "opr/") || strings.Contains(u, "opera/"):
		p.Browser = "Opera"
	case strings.Contains(u, "brave/"):
		p.Browser = "Brave"
	case strings.Contains(u, "vivaldi/"):
		p.Browser = "Vivaldi"
	case strings.Contains(u, "chrome/"):
		p.Browser = "Chrome"
	case strings.Contains(u, "crios/"):
		p.Browser = "Chrome iOS"
	case strings.Contains(u, "firefox/") || strings.Contains(u, "fxios/"):
		p.Browser = "Firefox"
	case strings.Contains(u, "safari/") && !strings.Contains(u, "chrome/"):
		p.Browser = "Safari"
	case strings.Contains(u, "msie ") || strings.Contains(u, "trident/"):
		p.Browser = "IE"
	case strings.Contains(u, "curl/"):
		p.Browser = "curl"
	case strings.Contains(u, "wget/"):
		p.Browser = "wget"
	}

	return p
}
