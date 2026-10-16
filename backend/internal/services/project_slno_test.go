package services

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
)

// serviceSlNoForImport: a valid manifest serial is kept; a missing/invalid one
// (pre-3.1.268 export) falls back to 1-based array position so order survives.
func TestServiceSlNoForImport(t *testing.T) {
	cases := []struct {
		manifest, idx, want int
	}{
		{0, 0, 1},  // legacy: first service -> 1
		{0, 3, 4},  // legacy: fourth service -> 4
		{-5, 2, 3}, // negative treated as unset
		{7, 0, 7},  // explicit serial wins regardless of position
		{2, 9, 2},  // explicit serial wins over position
	}
	for _, c := range cases {
		if got := serviceSlNoForImport(c.manifest, c.idx); got != c.want {
			t.Errorf("serviceSlNoForImport(%d,%d)=%d want %d", c.manifest, c.idx, got, c.want)
		}
	}
}

// The canonical list ordering MUST be sl_no asc, then created_at asc — the UI,
// export, and "Deploy all" all depend on this exact key order.
func TestServiceOrderSort(t *testing.T) {
	if len(serviceOrderSort) != 2 {
		t.Fatalf("serviceOrderSort should have 2 keys, got %d", len(serviceOrderSort))
	}
	if serviceOrderSort[0].Key != "sl_no" || serviceOrderSort[0].Value != 1 {
		t.Errorf("primary sort key must be sl_no asc, got %v", serviceOrderSort[0])
	}
	if serviceOrderSort[1].Key != "created_at" || serviceOrderSort[1].Value != 1 {
		t.Errorf("tiebreak sort key must be created_at asc, got %v", serviceOrderSort[1])
	}
	_ = bson.D{} // ensure bson import is exercised
}

// Wire contract: sl_no must serialise on the service and export, and parse on
// the add/update requests, under the exact JSON name the frontend uses.
func TestSlNoJSONContract(t *testing.T) {
	// ProjectService marshals sl_no.
	b, _ := json.Marshal(models.ProjectService{Name: "api", SlNo: 7})
	if !strings.Contains(string(b), `"sl_no":7`) {
		t.Errorf("ProjectService JSON must contain sl_no; got %s", b)
	}

	// AddServiceRequest parses sl_no.
	var add models.AddServiceRequest
	if err := json.Unmarshal([]byte(`{"name":"api","role":"backend","sl_no":5}`), &add); err != nil {
		t.Fatal(err)
	}
	if add.SlNo != 5 {
		t.Errorf("AddServiceRequest.SlNo=%d want 5", add.SlNo)
	}

	// UpdateServiceRequest uses a pointer so "omitted" is distinct from 0.
	var updOmitted models.UpdateServiceRequest
	json.Unmarshal([]byte(`{"framework":"nestjs"}`), &updOmitted)
	if updOmitted.SlNo != nil {
		t.Errorf("omitted sl_no should decode to nil pointer, got %v", *updOmitted.SlNo)
	}
	var updSet models.UpdateServiceRequest
	json.Unmarshal([]byte(`{"sl_no":3}`), &updSet)
	if updSet.SlNo == nil || *updSet.SlNo != 3 {
		t.Errorf("sl_no:3 should decode to pointer->3, got %v", updSet.SlNo)
	}

	// ExportServiceInfo omits a zero serial (omitempty) but includes a real one.
	z, _ := json.Marshal(models.ExportServiceInfo{Name: "api", SlNo: 0})
	if strings.Contains(string(z), "sl_no") {
		t.Errorf("zero sl_no should be omitted from export; got %s", z)
	}
	nz, _ := json.Marshal(models.ExportServiceInfo{Name: "api", SlNo: 3})
	if !strings.Contains(string(nz), `"sl_no":3`) {
		t.Errorf("non-zero sl_no must appear in export; got %s", nz)
	}
}
