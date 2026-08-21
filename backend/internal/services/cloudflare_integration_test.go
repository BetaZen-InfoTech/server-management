package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// -----------------------------------------------------------------------------
// Mock Cloudflare v4 API — an in-memory zone + records store that speaks the
// same envelope shape as the real API, so the WHOLE integration (connect →
// nameservers → sync → migration) can be driven end-to-end without a real
// account/token. Records are stored exactly as the client sends them, so this
// also proves the client's request encoding round-trips.
// -----------------------------------------------------------------------------

type mockCFRecord struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  bool   `json:"proxied"`
	Priority *int   `json:"priority,omitempty"`
}

type mockCFZone struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	NameServers []string `json:"name_servers"`
}

type mockCF struct {
	mu      sync.Mutex
	zones   map[string]*mockCFZone   // id -> zone
	records map[string][]mockCFRecord // zoneID -> records
	nextID  int
	server  *httptest.Server
}

func newMockCF() *mockCF {
	m := &mockCF{zones: map[string]*mockCFZone{}, records: map[string][]mockCFRecord{}}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	return m
}

func (m *mockCF) id(prefix string) string {
	m.nextID++
	return prefix + "-" + strings.Repeat("0", 1) + itoa(m.nextID)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func ok(w http.ResponseWriter, result interface{}) {
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"errors":      []interface{}{},
		"result":      result,
		"result_info": map[string]int{"page": 1, "per_page": 100, "total_pages": 1},
	})
}

func (m *mockCF) handle(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	p := strings.Trim(r.URL.Path, "/")
	seg := strings.Split(p, "/")

	// /user/tokens/verify
	if r.URL.Path == "/user/tokens/verify" {
		ok(w, map[string]string{"status": "active"})
		return
	}

	// /zones ...
	if len(seg) >= 1 && seg[0] == "zones" {
		switch {
		case len(seg) == 1 && r.Method == http.MethodGet:
			// list / find-by-name
			name := r.URL.Query().Get("name")
			arr := []mockCFZone{}
			for _, z := range m.zones {
				if name == "" || z.Name == name {
					arr = append(arr, *z)
				}
			}
			ok(w, arr)
			return
		case len(seg) == 1 && r.Method == http.MethodPost:
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			z := &mockCFZone{ID: m.id("zone"), Name: body.Name, Status: "pending",
				NameServers: []string{"ns1.mock-cf.test", "ns2.mock-cf.test"}}
			m.zones[z.ID] = z
			m.records[z.ID] = []mockCFRecord{}
			ok(w, *z)
			return
		case len(seg) == 2 && r.Method == http.MethodGet:
			// GET /zones/:id
			if z, exists := m.zones[seg[1]]; exists {
				ok(w, *z)
			} else {
				http.Error(w, "not found", 404)
			}
			return
		case len(seg) == 3 && seg[2] == "dns_records" && r.Method == http.MethodGet:
			ok(w, m.records[seg[1]])
			return
		case len(seg) == 3 && seg[2] == "dns_records" && r.Method == http.MethodPost:
			var rec mockCFRecord
			_ = json.NewDecoder(r.Body).Decode(&rec)
			rec.ID = m.id("rec")
			m.records[seg[1]] = append(m.records[seg[1]], rec)
			ok(w, rec)
			return
		case len(seg) == 4 && seg[2] == "dns_records" && r.Method == http.MethodPut:
			var rec mockCFRecord
			_ = json.NewDecoder(r.Body).Decode(&rec)
			rec.ID = seg[3]
			list := m.records[seg[1]]
			for i := range list {
				if list[i].ID == seg[3] {
					list[i] = rec
				}
			}
			m.records[seg[1]] = list
			ok(w, rec)
			return
		case len(seg) == 4 && seg[2] == "dns_records" && r.Method == http.MethodDelete:
			list := m.records[seg[1]]
			out := list[:0]
			for _, x := range list {
				if x.ID != seg[3] {
					out = append(out, x)
				}
			}
			m.records[seg[1]] = out
			ok(w, map[string]string{"id": seg[3]})
			return
		}
	}
	http.Error(w, "unhandled: "+r.Method+" "+r.URL.Path, 400)
}

// recsFor returns the records of the (single) zone whose name matches.
func (m *mockCF) recsFor(zoneName string) []mockCFRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, z := range m.zones {
		if z.Name == zoneName {
			cp := make([]mockCFRecord, len(m.records[id]))
			copy(cp, m.records[id])
			return cp
		}
	}
	return nil
}

func (m *mockCF) findRec(zoneName, typ, name string) *mockCFRecord {
	for _, r := range m.recsFor(zoneName) {
		if strings.EqualFold(r.Type, typ) && r.Name == name {
			rc := r
			return &rc
		}
	}
	return nil
}

// setProxied flips a record's proxied flag directly in the store (simulates an
// operator having orange-clouded a record in the Cloudflare dashboard).
func (m *mockCF) setProxied(zoneName, typ, name string, proxied bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, z := range m.zones {
		if z.Name == zoneName {
			for i := range m.records[id] {
				if strings.EqualFold(m.records[id][i].Type, typ) && m.records[id][i].Name == name {
					m.records[id][i].Proxied = proxied
				}
			}
		}
	}
}

// -----------------------------------------------------------------------------
// The integration test.
// -----------------------------------------------------------------------------

func mongoTestDB(t *testing.T) (*mongo.Database, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://127.0.0.1:27017"))
	if err != nil {
		t.Skipf("mongo not reachable, skipping integration test: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		t.Skipf("mongo ping failed, skipping integration test: %v", err)
	}
	db := client.Database("serverpanel_cf_integration_test")
	_ = db.Drop(context.Background())
	return db, func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	}
}

func pollJob(t *testing.T, s *CloudflareSyncJobService, id primitive.ObjectID) {
	t.Helper()
	for i := 0; i < 100; i++ { // up to ~10s
		job, err := s.GetSyncJob(context.Background(), id)
		if err == nil && (job.Status == "completed" || job.Status == "failed" || job.Status == "cancelled") {
			if job.Status != "completed" {
				t.Fatalf("sync job ended %s: %s / items=%+v", job.Status, job.Error, job.Items)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("sync job did not complete in time")
}

func TestCloudflareIntegration_ConnectSyncMigrate(t *testing.T) {
	db, cleanup := mongoTestDB(t)
	defer cleanup()
	ctx := context.Background()

	mock := newMockCF()
	defer mock.server.Close()

	// 32-byte key so token encryption works.
	encKey := []byte("0123456789abcdef0123456789abcdef")

	cf := NewCloudflareService(db, encKey)
	cf.SetAPIBase(mock.server.URL)

	// Configure + enable Cloudflare (token stored encrypted).
	if _, err := cf.Save(ctx, &SaveCloudflareRequest{
		AccountID: "acct-int", APIToken: "int-test-token", Enabled: true, DefaultProvider: "cloudflare",
	}); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	// Seed a local zone (example.com) + its records, INCLUDING subdomain records
	// (which the panel stores inside the parent zone).
	zoneID := primitive.NewObjectID()
	if _, err := db.Collection(database.ColDNSZones).InsertOne(ctx, bson.M{
		"_id": zoneID, "domain": "example.com", "server_ip": "1.2.3.4",
	}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	pri10 := 10
	seed := []bson.M{
		{"zone_id": zoneID, "type": "A", "name": "@", "value": "1.2.3.4", "ttl": 30},          // web apex
		{"zone_id": zoneID, "type": "CNAME", "name": "www", "value": "example.com.", "ttl": 60}, // web
		{"zone_id": zoneID, "type": "A", "name": "mail", "value": "1.2.3.4", "ttl": 30},        // MAIL
		{"zone_id": zoneID, "type": "MX", "name": "@", "value": "mail.example.com.", "ttl": 60, "priority": pri10}, // MAIL
		{"zone_id": zoneID, "type": "TXT", "name": "@", "value": "v=spf1 ip4:1.2.3.4 ~all", "ttl": 60},             // MAIL (SPF)
		{"zone_id": zoneID, "type": "TXT", "name": "mail._domainkey", "value": "v=DKIM1; k=rsa; p=ABCDEF", "ttl": 60}, // MAIL (DKIM)
		{"zone_id": zoneID, "type": "TXT", "name": "_dmarc", "value": "v=DMARC1; p=none", "ttl": 60},                 // MAIL (DMARC)
		{"zone_id": zoneID, "type": "A", "name": "shop", "value": "1.2.3.4", "ttl": 30},                             // SUBDOMAIN (web)
		{"zone_id": zoneID, "type": "CNAME", "name": "www.shop", "value": "shop.example.com.", "ttl": 60},           // SUBDOMAIN (web)
	}
	for _, r := range seed {
		if _, err := db.Collection(database.ColDNSRecords).InsertOne(ctx, r); err != nil {
			t.Fatalf("seed record: %v", err)
		}
	}

	// --- (1) CONNECT: create/reuse zone + get nameservers -------------------
	res, err := cf.ConnectDomain(ctx, "example.com")
	if err != nil {
		t.Fatalf("ConnectDomain: %v", err)
	}
	if !res.Created {
		t.Errorf("expected a new zone to be created")
	}
	if len(res.Nameservers) != 2 || res.Nameservers[0] != "ns1.mock-cf.test" {
		t.Fatalf("nameservers: got %#v want [ns1.mock-cf.test ns2.mock-cf.test]", res.Nameservers)
	}
	// dns_zones must now be stamped with the provider + zone id + nameservers.
	var zdoc struct {
		Provider    string   `bson:"provider"`
		CFZoneID    string   `bson:"cf_zone_id"`
		CFNS        []string `bson:"cf_nameservers"`
	}
	if err := db.Collection(database.ColDNSZones).FindOne(ctx, bson.M{"domain": "example.com"}).Decode(&zdoc); err != nil {
		t.Fatalf("read stamped zone: %v", err)
	}
	if zdoc.Provider != "cloudflare" || zdoc.CFZoneID == "" || len(zdoc.CFNS) != 2 {
		t.Fatalf("zone not stamped: %+v", zdoc)
	}

	// --- (2) SYNC: push local (apex + subdomain) records into Cloudflare -----
	sync := NewCloudflareSyncJobService(db, cf)
	job, err := sync.StartSyncDomain(ctx, "example.com", false, primitive.NilObjectID, primitive.NilObjectID)
	if err != nil {
		t.Fatalf("StartSyncDomain: %v", err)
	}
	pollJob(t, sync, job.ID)

	recs := mock.recsFor("example.com")
	// All 9 records must be present (SOA/NS are Cloudflare-owned; we push A/CNAME/MX/TXT).
	want := map[string]bool{
		"A @": false, "CNAME www.example.com": false, "A mail.example.com": false,
		"MX @": false, "TXT @": false, "TXT mail._domainkey.example.com": false,
		"TXT _dmarc.example.com": false, "A shop.example.com": false, "CNAME www.shop.example.com": false,
	}
	byKey := map[string]mockCFRecord{}
	for _, r := range recs {
		byKey[r.Type+" "+r.Name] = r
	}
	// Cloudflare stores names as FQDNs; the apex is "example.com", MX name is the apex.
	assertPresent := func(typ, fqdn string) mockCFRecord {
		for _, r := range recs {
			if strings.EqualFold(r.Type, typ) && r.Name == fqdn {
				return r
			}
		}
		t.Fatalf("expected record %s %s in Cloudflare; got %+v", typ, fqdn, recs)
		return mockCFRecord{}
	}
	_ = want
	apex := assertPresent("A", "example.com")
	assertPresent("CNAME", "www.example.com")
	mailA := assertPresent("A", "mail.example.com")
	mx := assertPresent("MX", "example.com")
	spf := assertPresent("TXT", "example.com")
	assertPresent("TXT", "mail._domainkey.example.com")
	assertPresent("TXT", "_dmarc.example.com")
	// SUBDOMAIN records must have synced too (proves subdomain-with-parent).
	assertPresent("A", "shop.example.com")
	assertPresent("CNAME", "www.shop.example.com")

	// Mail records must be DNS-only (never proxied).
	if mailA.Proxied {
		t.Error("mail A must not be proxied")
	}
	if mx.Proxied || spf.Proxied {
		t.Error("MX/SPF must not be proxied")
	}
	// MX priority must round-trip (split out of the value).
	if mx.Priority == nil || *mx.Priority != 10 {
		t.Errorf("MX priority = %v, want 10", mx.Priority)
	}
	// SPF TXT must be stored WITHOUT surrounding quotes (bare string).
	if strings.HasPrefix(spf.Content, `"`) {
		t.Errorf("SPF content should be unquoted, got %q", spf.Content)
	}
	_ = apex

	// --- (3) IDEMPOTENCY: a second sync creates nothing, duplicates nothing --
	before := len(mock.recsFor("example.com"))
	job2, _ := sync.StartSyncDomain(ctx, "example.com", false, primitive.NilObjectID, primitive.NilObjectID)
	pollJob(t, sync, job2.ID)
	if after := len(mock.recsFor("example.com")); after != before {
		t.Fatalf("idempotency broken: record count %d -> %d (duplicates created)", before, after)
	}
	j2, _ := sync.GetSyncJob(ctx, job2.ID)
	if j2.Created != 0 {
		t.Errorf("second sync created %d records, want 0 (all should match)", j2.Created)
	}

	// --- (4) PROXIED PRESERVATION: operator orange-clouds apex; local IP change
	// must UPDATE the record but KEEP proxied=true.
	mock.setProxied("example.com", "A", "example.com", true)
	_, _ = db.Collection(database.ColDNSRecords).UpdateOne(ctx,
		bson.M{"zone_id": zoneID, "type": "A", "name": "@"},
		bson.M{"$set": bson.M{"value": "5.6.7.8"}})
	job3, _ := sync.StartSyncDomain(ctx, "example.com", false, primitive.NilObjectID, primitive.NilObjectID)
	pollJob(t, sync, job3.ID)
	if a := mock.findRec("example.com", "A", "example.com"); a == nil || a.Content != "5.6.7.8" {
		t.Fatalf("apex A not updated: %+v", a)
	} else if !a.Proxied {
		t.Error("apex A proxied flag was lost on sync (should be preserved)")
	}
	// Reset apex back to the shared IP for the migration test below.
	_, _ = db.Collection(database.ColDNSRecords).UpdateOne(ctx,
		bson.M{"zone_id": zoneID, "type": "A", "name": "@"},
		bson.M{"$set": bson.M{"value": "1.2.3.4"}})
	job4, _ := sync.StartSyncDomain(ctx, "example.com", false, primitive.NilObjectID, primitive.NilObjectID)
	pollJob(t, sync, job4.ID)

	// --- (5) SERVER MIGRATION: IP 1.2.3.4 -> 9.9.9.9. WEB A records repoint;
	// mail A (mail.example.com) stays put. ------------------------------------
	n, err := cf.UpdateWebRecordsForServerIPChange(ctx, "1.2.3.4", "9.9.9.9")
	if err != nil {
		t.Fatalf("UpdateWebRecordsForServerIPChange: %v", err)
	}
	if apexA := mock.findRec("example.com", "A", "example.com"); apexA == nil || apexA.Content != "9.9.9.9" {
		t.Fatalf("apex A not repointed on migration: %+v", apexA)
	} else if !apexA.Proxied {
		t.Error("apex A proxied flag lost during migration repoint")
	}
	if shopA := mock.findRec("example.com", "A", "shop.example.com"); shopA == nil || shopA.Content != "9.9.9.9" {
		t.Fatalf("subdomain A not repointed on migration: %+v", shopA)
	}
	// MAIL A must NOT have moved.
	if mA := mock.findRec("example.com", "A", "mail.example.com"); mA == nil || mA.Content != "1.2.3.4" {
		t.Fatalf("mail A was moved by a web-IP migration (must be protected): %+v", mA)
	}
	// Exactly the two web A records (apex + shop) should have been updated.
	if n != 2 {
		t.Errorf("migration updated %d records, want 2 (apex + shop web A; mail protected)", n)
	}
}
