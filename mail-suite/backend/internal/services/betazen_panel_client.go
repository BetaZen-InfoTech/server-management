package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/config"
)

// BetazenPanelClient is a tiny REST client used to delegate DNS upserts,
// forwarder writes, and DKIM lookups to the existing Betazen panel API.
// It is OPTIONAL — if BetazenPanelURL/Token are blank, every call is a no-op
// returning ErrPanelNotConfigured, which the caller treats as best-effort.
type BetazenPanelClient struct {
	base  *url.URL
	token string
	http  *http.Client
}

var ErrPanelNotConfigured = errors.New("betazen panel not configured")

func NewBetazenPanelClient(cfg *config.Config) *BetazenPanelClient {
	c := &BetazenPanelClient{
		http: &http.Client{Timeout: 20 * time.Second},
	}
	if cfg.BetazenPanelURL != "" {
		if u, err := url.Parse(cfg.BetazenPanelURL); err == nil {
			c.base = u
		}
	}
	c.token = cfg.BetazenPanelToken
	return c
}

func (c *BetazenPanelClient) Configured() bool { return c.base != nil && c.token != "" }

func (c *BetazenPanelClient) do(ctx context.Context, method, path string, body, out interface{}) error {
	if !c.Configured() {
		return ErrPanelNotConfigured
	}
	u := *c.base
	u.Path = path
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("panel %s %s: %d: %s", method, path, res.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

type addRecordReq struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	TTL   string `json:"ttl"`
	Value string `json:"value"`
}

func (c *BetazenPanelClient) AddDNSRecord(ctx context.Context, domain, name, recordType, ttl, value string) error {
	return c.do(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/cpanel/dns/%s/records", url.PathEscape(domain)),
		addRecordReq{Name: name, Type: recordType, TTL: ttl, Value: value}, nil)
}

func (c *BetazenPanelClient) SetupDKIM(ctx context.Context, domain string) error {
	return c.do(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/cpanel/email/dkim/%s", url.PathEscape(domain)), nil, nil)
}

type forwarderReq struct {
	Source       string   `json:"source"`
	Destinations []string `json:"destinations"`
	KeepCopy     bool     `json:"keep_copy"`
}

func (c *BetazenPanelClient) CreateForwarder(ctx context.Context, source string, dest []string, keep bool) error {
	return c.do(ctx, http.MethodPost,
		"/api/v1/cpanel/email/forwarders",
		forwarderReq{Source: source, Destinations: dest, KeepCopy: keep}, nil)
}

func (c *BetazenPanelClient) DeleteForwarder(ctx context.Context, source string) error {
	return c.do(ctx, http.MethodDelete,
		fmt.Sprintf("/api/v1/cpanel/email/forwarders/by-source/%s", url.PathEscape(source)), nil, nil)
}
