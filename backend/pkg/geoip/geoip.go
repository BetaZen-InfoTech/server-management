// Package geoip resolves IPv4/IPv6 addresses to a rough location using
// the free ip-api.com JSON endpoint. Best-effort — callers must treat
// every field as optional. Private / loopback / link-local IPs skip
// the lookup entirely.
//
// We use the free tier's 45-req/min limit which is generous for login
// traffic. Results are cached in-memory for 24h per IP so repeated
// logins from the same device don't burn the quota.
package geoip

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Result is the subset of ip-api.com's response we persist. Every
// field may be empty — the caller should render a placeholder when
// none of them resolved.
type Result struct {
	Country string `json:"country"`
	Region  string `json:"regionName"`
	City    string `json:"city"`
}

type cacheEntry struct {
	res       Result
	expiresAt time.Time
}

var (
	cacheMu sync.RWMutex
	cache   = make(map[string]cacheEntry, 256)
)

// cacheTTL is how long we trust a successful lookup. 24h is a good
// balance — a traveller's IP rarely changes within a day, and if it
// does, the next login rotates the cache.
const cacheTTL = 24 * time.Hour

// Skip returns true for addresses we should never query — private,
// loopback, link-local, multicast, or empty. These would only waste
// the ip-api quota.
func Skip(ip string) bool {
	if ip == "" {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return true
	}
	return parsed.IsLoopback() || parsed.IsPrivate() ||
		parsed.IsLinkLocalUnicast() || parsed.IsMulticast() ||
		parsed.IsUnspecified()
}

// Lookup resolves ip via ip-api.com. Returns a zero-value Result and
// nil error when the IP is private / invalid (to keep callers from
// having to special-case). A non-nil error means the HTTP request
// itself failed; the caller should still proceed with a zero Result.
//
// ctx is respected — pass a deadline so slow ip-api responses don't
// stall login. The caller typically runs Lookup in a goroutine after
// the response has already been sent.
func Lookup(ctx context.Context, ip string) (Result, error) {
	if Skip(ip) {
		return Result{}, nil
	}

	cacheMu.RLock()
	if e, ok := cache[ip]; ok && e.expiresAt.After(time.Now()) {
		cacheMu.RUnlock()
		return e.res, nil
	}
	cacheMu.RUnlock()

	// Only fetch the fields we store so the response stays tiny.
	endpoint := "http://ip-api.com/json/" + url.PathEscape(ip) + "?fields=status,message,country,regionName,city"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, err
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, errors.New("ip-api: non-200 response")
	}

	var body struct {
		Status     string `json:"status"`
		Message    string `json:"message"`
		Country    string `json:"country"`
		RegionName string `json:"regionName"`
		City       string `json:"city"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Result{}, err
	}
	if body.Status != "success" {
		// `fail` with a reason like "private range" or "reserved range"
		// — cache a blank result so we don't retry the same bad IP.
		cacheMu.Lock()
		cache[ip] = cacheEntry{res: Result{}, expiresAt: time.Now().Add(cacheTTL)}
		cacheMu.Unlock()
		return Result{}, nil
	}

	res := Result{Country: body.Country, Region: body.RegionName, City: body.City}
	cacheMu.Lock()
	cache[ip] = cacheEntry{res: res, expiresAt: time.Now().Add(cacheTTL)}
	cacheMu.Unlock()
	return res, nil
}
