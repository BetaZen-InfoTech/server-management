package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient wires a Client to an httptest server with retries disabled by
// default (tests that exercise retry opt back in).
func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	c := New("test-token",
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithMaxRetries(0),
	)
	return c, srv
}

// writeEnvelope writes a Cloudflare-shaped success envelope with the given
// result payload.
func writeEnvelope(w http.ResponseWriter, result interface{}, info *ResultInfo) {
	env := map[string]interface{}{"success": true, "errors": []APIError{}, "result": result}
	if info != nil {
		env["result_info"] = info
	}
	_ = json.NewEncoder(w).Encode(env)
}

func TestVerifyToken_Success(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("missing/incorrect auth header: %q", got)
		}
		writeEnvelope(w, map[string]string{"status": "active"}, nil)
	})
	defer srv.Close()

	status, err := c.VerifyToken(context.Background())
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if status != "active" {
		t.Fatalf("status = %q, want active", status)
	}
}

func TestVerifyToken_APIError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Cloudflare returns 200 with success:false + errors for a bad token.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"errors":  []APIError{{Code: 1000, Message: "Invalid API Token"}},
		})
	})
	defer srv.Close()

	_, err := c.VerifyToken(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Invalid API Token") {
		t.Fatalf("error = %v, want it to contain the CF message", err)
	}
}

func TestListZones_Pagination(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			writeEnvelope(w, []Zone{{ID: "z1", Name: "a.com"}}, &ResultInfo{Page: 1, TotalPages: 2})
		case "2":
			writeEnvelope(w, []Zone{{ID: "z2", Name: "b.com"}}, &ResultInfo{Page: 2, TotalPages: 2})
		default:
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
		}
	})
	defer srv.Close()

	zones, err := c.ListZones(context.Background())
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if len(zones) != 2 || zones[0].ID != "z1" || zones[1].ID != "z2" {
		t.Fatalf("zones = %+v, want both pages", zones)
	}
}

func TestFindZoneByName(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "example.com" {
			t.Errorf("name filter = %q", r.URL.Query().Get("name"))
		}
		writeEnvelope(w, []Zone{{ID: "zid", Name: "example.com", Status: "active", NameServers: []string{"ns1", "ns2"}}}, nil)
	})
	defer srv.Close()

	z, err := c.FindZoneByName(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("FindZoneByName: %v", err)
	}
	if z == nil || z.ID != "zid" || len(z.NameServers) != 2 {
		t.Fatalf("zone = %+v", z)
	}
}

func TestFindZoneByName_NotFound(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, []Zone{}, nil)
	})
	defer srv.Close()

	z, err := c.FindZoneByName(context.Background(), "nope.com")
	if err != nil {
		t.Fatalf("FindZoneByName: %v", err)
	}
	if z != nil {
		t.Fatalf("expected nil zone, got %+v", z)
	}
}

func TestCreateZone(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "new.com" {
			t.Errorf("body name = %v", body["name"])
		}
		if body["type"] != "full" {
			t.Errorf("body type = %v, want full", body["type"])
		}
		writeEnvelope(w, Zone{ID: "znew", Name: "new.com", Status: "pending"}, nil)
	})
	defer srv.Close()

	z, err := c.CreateZone(context.Background(), "new.com", "acct123")
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	if z.ID != "znew" || z.Status != "pending" {
		t.Fatalf("zone = %+v", z)
	}
}

func TestCreateAndUpdateAndDeleteRecord(t *testing.T) {
	var lastMethod, lastPath string
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		lastMethod, lastPath = r.Method, r.URL.Path
		switch r.Method {
		case http.MethodPost:
			writeEnvelope(w, DNSRecord{ID: "rec1", Type: "A", Name: "example.com", Content: "1.2.3.4"}, nil)
		case http.MethodPut:
			writeEnvelope(w, DNSRecord{ID: "rec1", Type: "A", Name: "example.com", Content: "5.6.7.8"}, nil)
		case http.MethodDelete:
			writeEnvelope(w, map[string]string{"id": "rec1"}, nil)
		}
	})
	defer srv.Close()

	ctx := context.Background()
	rec, err := c.CreateDNSRecord(ctx, "zid", RecordParams{Type: "A", Name: "example.com", Content: "1.2.3.4"})
	if err != nil || rec.ID != "rec1" {
		t.Fatalf("CreateDNSRecord: rec=%+v err=%v", rec, err)
	}
	upd, err := c.UpdateDNSRecord(ctx, "zid", "rec1", RecordParams{Type: "A", Name: "example.com", Content: "5.6.7.8"})
	if err != nil || upd.Content != "5.6.7.8" {
		t.Fatalf("UpdateDNSRecord: rec=%+v err=%v", upd, err)
	}
	if err := c.DeleteDNSRecord(ctx, "zid", "rec1"); err != nil {
		t.Fatalf("DeleteDNSRecord: %v", err)
	}
	if lastMethod != http.MethodDelete || !strings.HasSuffix(lastPath, "/dns_records/rec1") {
		t.Fatalf("last delete call = %s %s", lastMethod, lastPath)
	}
}

func TestRetryOn429(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0") // retry immediately — keeps the test fast
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false})
			return
		}
		writeEnvelope(w, map[string]string{"status": "active"}, nil)
	}))
	defer srv.Close()

	c := New("t", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithMaxRetries(3))
	status, err := c.VerifyToken(context.Background())
	if err != nil {
		t.Fatalf("VerifyToken after retry: %v", err)
	}
	if status != "active" {
		t.Fatalf("status = %q", status)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (one 429 then success)", attempts)
	}
}

func TestContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		writeEnvelope(w, map[string]string{"status": "active"}, nil)
	}))
	defer srv.Close()

	c := New("t", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()), WithMaxRetries(0))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.VerifyToken(ctx); err == nil {
		t.Fatal("expected context deadline error, got nil")
	}
}
