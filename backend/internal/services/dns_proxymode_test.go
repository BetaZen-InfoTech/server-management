package services

import (
	"testing"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// TestMergeDBRecordFields is the regression guard for the bug where a per-record
// Cloudflare proxy override (proxy_mode) reverted to "Default" on every DNS Zones
// refresh: ListRecords rebuilt each record from the live PowerDNS view but forgot
// to carry the Mongo-only proxy_mode back out. mergeDBRecordFields must copy it
// (and the other stable Mongo-owned fields) from the matched dns_records row.
func TestMergeDBRecordFields(t *testing.T) {
	id := primitive.NewObjectID()
	zid := primitive.NewObjectID()
	created := time.Now().Add(-48 * time.Hour)
	updated := time.Now().Add(-1 * time.Hour)
	pri, weight, port, caaFlag := 10, 5, 443, 1

	// `rec` is what ListRecords builds from PowerDNS: value fields only, no id,
	// no proxy_mode (PowerDNS has no proxy concept).
	rec := models.DNSRecord{Type: "A", Name: "mongo", Value: "187.127.172.193", TTL: 60}
	// `dbRec` is the matched Mongo row carrying the persisted override + ids.
	dbRec := models.DNSRecord{
		ID: id, ZoneID: zid,
		Type: "A", Name: "mongo", Value: "187.127.172.193",
		Priority: &pri, Weight: &weight, Port: &port,
		CAAFlag: &caaFlag, CAATag: "issue",
		ProxyMode: models.ProxyModeOff, // the DNS-only the operator picked
		CreatedAt: created, UpdatedAt: updated,
	}

	mergeDBRecordFields(&rec, dbRec)

	if rec.ProxyMode != models.ProxyModeOff {
		t.Fatalf("ProxyMode = %q; want %q (this is the reverts-to-Default bug)", rec.ProxyMode, models.ProxyModeOff)
	}
	if rec.ID != id || rec.ZoneID != zid {
		t.Errorf("ID/ZoneID not carried over: got %v/%v", rec.ID, rec.ZoneID)
	}
	if rec.Priority == nil || *rec.Priority != pri || rec.Weight == nil || *rec.Weight != weight || rec.Port == nil || *rec.Port != port {
		t.Errorf("priority/weight/port not carried over")
	}
	if rec.CAAFlag == nil || *rec.CAAFlag != 1 || rec.CAATag != "issue" {
		t.Errorf("CAA fields not carried over")
	}
	if !rec.CreatedAt.Equal(created) || !rec.UpdatedAt.Equal(updated) {
		t.Errorf("timestamps not carried over")
	}
	// The PowerDNS-derived value fields must be untouched.
	if rec.Type != "A" || rec.Name != "mongo" || rec.Value != "187.127.172.193" || rec.TTL != 60 {
		t.Errorf("live pdns fields were clobbered: %+v", rec)
	}
}
