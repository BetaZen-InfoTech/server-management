// Package cloudflare is a small, dependency-free client for the Cloudflare
// v4 REST API, scoped to the operations the panel needs: token verification,
// zone lookup/listing, nameserver retrieval, and DNS record listing. Write
// operations (create/update/delete zone + records) are added in a later
// increment; this file is deliberately read-only so it can ship behind the
// read-only reconciliation view without any risk of mutating a live zone.
//
// Every request carries: a caller-supplied context (timeout + cancellation),
// bounded retries with exponential backoff that honours Cloudflare's
// Retry-After header on 429 / 5xx, and structured error decoding of the
// standard Cloudflare envelope {success, errors[], result, result_info}.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DefaultBaseURL is the Cloudflare v4 API root.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

// APIError is one entry from Cloudflare's `errors` array. It implements error
// so callers can surface the exact message ("Invalid API Token", etc.).
type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e APIError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("cloudflare: %s (code %d)", e.Message, e.Code)
	}
	return "cloudflare: " + e.Message
}

// Zone is the subset of a Cloudflare zone object we consume.
type Zone struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	NameServers []string `json:"name_servers"`
	Paused      bool     `json:"paused"`
	Type        string   `json:"type"`
	Account     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"account"`
}

// DNSRecord is the subset of a Cloudflare DNS record we consume. Priority is a
// pointer so a missing value (non-MX/SRV records) is distinguishable from 0.
type DNSRecord struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  bool   `json:"proxied"`
	Priority *int   `json:"priority,omitempty"`
}

// ResultInfo is Cloudflare's pagination block.
type ResultInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Count      int `json:"count"`
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
}

type apiEnvelope struct {
	Success    bool            `json:"success"`
	Errors     []APIError      `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo *ResultInfo     `json:"result_info,omitempty"`
}

// Client is a Cloudflare API client bound to a single account-level token.
type Client struct {
	token      string
	base       string
	http       *http.Client
	maxRetries int
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient injects a shared *http.Client (connection reuse + a sane
// per-request timeout). If nil, a default 20s client is used.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// WithBaseURL overrides the API root (used by tests / self-hosted proxies).
func WithBaseURL(u string) Option {
	return func(c *Client) {
		if u != "" {
			c.base = u
		}
	}
}

// WithMaxRetries sets how many times a 429 / 5xx / transient network error is
// retried before giving up. Default 3.
func WithMaxRetries(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.maxRetries = n
		}
	}
}

// New builds a client for the given token.
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:      token,
		base:       DefaultBaseURL,
		http:       &http.Client{Timeout: 20 * time.Second},
		maxRetries: 3,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// do issues a request with retries and decodes the Cloudflare envelope into
// out (which may be nil). body, when non-nil, is JSON-encoded and re-sent on
// each retry attempt. It returns the pagination block when present.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out interface{}) (*ResultInfo, error) {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var encoded []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode cloudflare request body: %w", err)
		}
		encoded = b
	}

	backoff := 500 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		var rdr io.Reader
		if encoded != nil {
			rdr = bytes.NewReader(encoded)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rdr)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json")
		if encoded != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			// Transient network error — retry unless the context is done or
			// we're out of attempts.
			lastErr = fmt.Errorf("cloudflare request failed: %w", err)
			if attempt < c.maxRetries && ctx.Err() == nil {
				sleepCtx(ctx, backoff)
				backoff *= 2
				continue
			}
			return nil, lastErr
		}

		// Rate limit / server error — respect Retry-After, then retry.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			wait := parseRetryAfter(resp.Header.Get("Retry-After"), backoff)
			resp.Body.Close()
			lastErr = fmt.Errorf("cloudflare returned HTTP %d", resp.StatusCode)
			if attempt < c.maxRetries && ctx.Err() == nil {
				sleepCtx(ctx, wait)
				backoff *= 2
				continue
			}
			return nil, lastErr
		}

		var env apiEnvelope
		decErr := json.NewDecoder(resp.Body).Decode(&env)
		resp.Body.Close()
		if decErr != nil {
			return nil, fmt.Errorf("decode cloudflare response (HTTP %d): %w", resp.StatusCode, decErr)
		}
		if !env.Success {
			if len(env.Errors) > 0 {
				return env.ResultInfo, env.Errors[0]
			}
			return env.ResultInfo, fmt.Errorf("cloudflare returned success=false (HTTP %d)", resp.StatusCode)
		}
		if out != nil && len(env.Result) > 0 {
			if err := json.Unmarshal(env.Result, out); err != nil {
				return env.ResultInfo, fmt.Errorf("unmarshal cloudflare result: %w", err)
			}
		}
		return env.ResultInfo, nil
	}
	return nil, lastErr
}

// VerifyToken calls /user/tokens/verify and returns the token status
// ("active" on success).
func (c *Client) VerifyToken(ctx context.Context) (string, error) {
	var res struct {
		Status string `json:"status"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, nil, &res); err != nil {
		return "", err
	}
	return res.Status, nil
}

// ListZones returns every zone the token can see, following pagination.
func (c *Client) ListZones(ctx context.Context) ([]Zone, error) {
	var all []Zone
	page := 1
	for {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", "50")
		var batch []Zone
		info, err := c.do(ctx, http.MethodGet, "/zones", q, nil, &batch)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if info == nil || len(batch) == 0 || info.Page >= info.TotalPages {
			break
		}
		page++
	}
	return all, nil
}

// FindZoneByName returns the zone whose name exactly matches, or nil if none.
func (c *Client) FindZoneByName(ctx context.Context, name string) (*Zone, error) {
	q := url.Values{}
	q.Set("name", name)
	var zones []Zone
	if _, err := c.do(ctx, http.MethodGet, "/zones", q, nil, &zones); err != nil {
		return nil, err
	}
	if len(zones) == 0 {
		return nil, nil
	}
	return &zones[0], nil
}

// GetZone fetches a single zone by id.
func (c *Client) GetZone(ctx context.Context, zoneID string) (*Zone, error) {
	var z Zone
	if _, err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID), nil, nil, &z); err != nil {
		return nil, err
	}
	return &z, nil
}

// ListDNSRecords returns every DNS record in a zone, following pagination.
func (c *Client) ListDNSRecords(ctx context.Context, zoneID string) ([]DNSRecord, error) {
	var all []DNSRecord
	page := 1
	for {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", "100")
		var batch []DNSRecord
		info, err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID)+"/dns_records", q, nil, &batch)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if info == nil || len(batch) == 0 || info.Page >= info.TotalPages {
			break
		}
		page++
	}
	return all, nil
}

// RecordParams is the payload for creating / updating a DNS record. Proxied is
// a pointer so "leave unset" (DNS-only default) is distinguishable from an
// explicit false. Priority applies to MX / SRV.
type RecordParams struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl,omitempty"`
	Proxied  *bool  `json:"proxied,omitempty"`
	Priority *int   `json:"priority,omitempty"`
}

// CreateZone creates (or, via the caller's find-first flow, is only reached
// when absent) a zone for name under the given account. type=full — the
// standard "Cloudflare is authoritative" setup.
func (c *Client) CreateZone(ctx context.Context, name, accountID string) (*Zone, error) {
	body := map[string]interface{}{
		"name": name,
		"type": "full",
	}
	if accountID != "" {
		body["account"] = map[string]string{"id": accountID}
	}
	var z Zone
	if _, err := c.do(ctx, http.MethodPost, "/zones", nil, body, &z); err != nil {
		return nil, err
	}
	return &z, nil
}

// CreateDNSRecord adds a record to a zone.
func (c *Client) CreateDNSRecord(ctx context.Context, zoneID string, p RecordParams) (*DNSRecord, error) {
	var r DNSRecord
	if _, err := c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(zoneID)+"/dns_records", nil, p, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateDNSRecord replaces a record's fields by id (PUT is a full replace in
// the Cloudflare API).
func (c *Client) UpdateDNSRecord(ctx context.Context, zoneID, recordID string, p RecordParams) (*DNSRecord, error) {
	var r DNSRecord
	path := "/zones/" + url.PathEscape(zoneID) + "/dns_records/" + url.PathEscape(recordID)
	if _, err := c.do(ctx, http.MethodPut, path, nil, p, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// DeleteDNSRecord removes a record by id.
func (c *Client) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	path := "/zones/" + url.PathEscape(zoneID) + "/dns_records/" + url.PathEscape(recordID)
	_, err := c.do(ctx, http.MethodDelete, path, nil, nil, nil)
	return err
}

// sleepCtx sleeps for d, or returns early if the context is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// parseRetryAfter reads an integer-seconds Retry-After header, capping it so a
// hostile / buggy value can't stall a request for minutes. Falls back to the
// caller's current backoff when the header is absent or unparseable.
func parseRetryAfter(header string, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	secs, err := strconv.Atoi(header)
	if err != nil || secs < 0 {
		return fallback
	}
	d := time.Duration(secs) * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}
