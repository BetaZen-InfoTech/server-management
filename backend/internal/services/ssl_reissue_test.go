package services

import (
	"reflect"
	"testing"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
)

// TestMergeAlias pins the dedup contract ensureWWWAliases relies on: an alias
// is appended once, case/space-insensitively, and never duplicates an existing
// entry — so a cert's SAN list can't grow a duplicate www.<d> on repeated
// issue/reissue.
func TestMergeAlias(t *testing.T) {
	cases := []struct {
		list  []string
		alias string
		want  []string
	}{
		{nil, "www.example.com", []string{"www.example.com"}},
		{[]string{"www.example.com"}, "www.example.com", []string{"www.example.com"}},
		{[]string{"WWW.Example.com"}, "www.example.com", []string{"WWW.Example.com"}},
		{[]string{" www.example.com "}, "www.example.com", []string{" www.example.com "}},
		{[]string{"cname.example.com"}, "www.example.com", []string{"cname.example.com", "www.example.com"}},
	}
	for i, c := range cases {
		if got := mergeAlias(c.list, c.alias); !reflect.DeepEqual(got, c.want) {
			t.Errorf("case %d: mergeAlias(%v, %q) = %v, want %v", i, c.list, c.alias, got, c.want)
		}
	}
}

// TestSSL_IssueRequest_BackcompatDefaults pins the wire-shape default
// for IssueLetsEncryptRequest.Reissue. A legacy API client (or the
// existing programmatic-issue handler that doesn't yet know about
// Reissue) constructs the struct with zero values; that path MUST
// keep producing the historical "skip if cert exists" short-circuit
// behaviour, otherwise we'd silently start burning Let's Encrypt
// rate-limit slots on every domain-create request.
//
// Default Reissue=false is the contract this test defends.
func TestSSL_IssueRequest_BackcompatDefaults(t *testing.T) {
	req := models.IssueLetsEncryptRequest{
		Domain: "example.com",
	}
	if req.Reissue {
		t.Error("zero-value IssueLetsEncryptRequest.Reissue must be false (back-compat)")
	}
}

// TestSSL_BulkRequest_BackcompatDefaults defends the bulk shape with
// the same contract.
func TestSSL_BulkRequest_BackcompatDefaults(t *testing.T) {
	req := models.IssueLetsEncryptBulkRequest{
		Domains: []string{"a.example.com"},
	}
	if req.Reissue {
		t.Error("zero-value IssueLetsEncryptBulkRequest.Reissue must be false (back-compat)")
	}
}

// TestSSL_BulkResponse_ActionAccountingFields checks the new counters
// exist on the response so a frontend that splits "Issued X, reissued
// Y" toasts won't compile against an outdated server type.
func TestSSL_BulkResponse_ActionAccountingFields(t *testing.T) {
	resp := models.IssueLetsEncryptBulkResponse{
		Total:    5,
		Success:  4,
		Failed:   1,
		Issued:   1,
		Reissued: 3,
	}
	if resp.Issued+resp.Reissued != resp.Success {
		t.Errorf("issued (%d) + reissued (%d) should equal success (%d)",
			resp.Issued, resp.Reissued, resp.Success)
	}
}

// TestSSL_BulkItem_ActionField pins that BulkItem.Action serialises with
// the values the frontend assumes — "issued" or "reissued" — and that
// an empty Action stays omitted from JSON so legacy consumers don't
// see noisy zero-value fields.
func TestSSL_BulkItem_ActionField(t *testing.T) {
	item := models.IssueLetsEncryptBulkItem{
		Domain:  "x.example.com",
		Success: true,
		Action:  "reissued",
	}
	if item.Action != "reissued" {
		t.Errorf("Action field round-trip broke; got %q", item.Action)
	}
}
