package services

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
)

// TestExtractOID covers all three shapes mongoexport / mongosh / the Go
// driver may surface for an ObjectID column. A regression here would
// silently break the user_id remap on every doc.
func TestExtractOID(t *testing.T) {
	hex := "65ab7d8e9fa12b34c5d6e7f8"
	oid, _ := primitive.ObjectIDFromHex(hex)

	cases := []struct {
		name string
		in   any
		want string
	}{
		{"native ObjectID", oid, hex},
		{"hex string", hex, hex},
		{"ejson wrapper", map[string]any{"$oid": hex}, hex},
		{"nil", nil, ""},
		{"random string", "not-an-oid", ""},
		{"int", 42, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractOID(c.in); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestUnwrapEJSON_ScalarShapes pins the round-trip for the three
// extended-JSON wrappers we hit in real exports: $oid, $date (string),
// $date with $numberLong, and bare $numberLong. If any of these stay as
// a map after unwrap, the mongo driver stores them as embedded objects
// and the destination's typed reads (App, Project, ...) start failing
// in subtle ways.
func TestUnwrapEJSON_ScalarShapes(t *testing.T) {
	hex := "65ab7d8e9fa12b34c5d6e7f8"
	oid, _ := primitive.ObjectIDFromHex(hex)

	t.Run("$oid → ObjectID", func(t *testing.T) {
		got := unwrapEJSON(map[string]any{"$oid": hex})
		if got != oid {
			t.Fatalf("got %#v want %#v", got, oid)
		}
	})

	t.Run("$date string → time.Time", func(t *testing.T) {
		got := unwrapEJSON(map[string]any{"$date": "2026-01-02T03:04:05Z"})
		want, _ := time.Parse(time.RFC3339, "2026-01-02T03:04:05Z")
		if !got.(time.Time).Equal(want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("$date $numberLong → time.Time", func(t *testing.T) {
		// 1 729 425 845 000 ms = 2024-10-20T11:24:05Z
		got := unwrapEJSON(map[string]any{
			"$date": map[string]any{"$numberLong": "1729425845000"},
		})
		if _, ok := got.(time.Time); !ok {
			t.Fatalf("got %T (%v), want time.Time", got, got)
		}
	})

	t.Run("$numberLong → int64", func(t *testing.T) {
		got := unwrapEJSON(map[string]any{"$numberLong": "9223372036854775000"})
		if got.(int64) != int64(9223372036854775000) {
			t.Fatalf("got %v", got)
		}
	})

	// $binary unwrap is the regression guard for the bug that hid every
	// synced Project: mongoexport emits []byte fields (github_pat_encrypted,
	// User.totp_secret) as $binary wrappers; if we leave them as embedded
	// documents the bson driver fails decode at API read time with
	// "cannot decode embedded document into a []byte".
	t.Run("$binary v1 → []byte", func(t *testing.T) {
		got := unwrapEJSON(map[string]any{"$binary": "aGVsbG8=", "$type": "00"})
		b, ok := got.([]byte)
		if !ok {
			t.Fatalf("got %T, want []byte", got)
		}
		if string(b) != "hello" {
			t.Fatalf("got %q want %q", b, "hello")
		}
	})
	t.Run("$binary v2 → []byte", func(t *testing.T) {
		got := unwrapEJSON(map[string]any{"$binary": map[string]any{"base64": "aGVsbG8=", "subType": "00"}})
		b, ok := got.([]byte)
		if !ok {
			t.Fatalf("got %T, want []byte", got)
		}
		if string(b) != "hello" {
			t.Fatalf("got %q want %q", b, "hello")
		}
	})

	t.Run("$numberDouble → float64", func(t *testing.T) {
		got := unwrapEJSON(map[string]any{"$numberDouble": "3.14"})
		if got.(float64) != 3.14 {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("$numberInt → int32", func(t *testing.T) {
		got := unwrapEJSON(map[string]any{"$numberInt": "42"})
		if got.(int32) != int32(42) {
			t.Fatalf("got %v", got)
		}
	})
}

// TestUnwrapEJSON_NestedRecursion is the realistic case: a Project doc
// with embedded Service array, each Service holding its own ObjectID
// and timestamps. Earlier prototypes only walked the top level and
// left nested ObjectIDs as map[$oid] — the destination then refused to
// type-decode the doc back into the Project struct.
func TestUnwrapEJSON_NestedRecursion(t *testing.T) {
	hex := "65ab7d8e9fa12b34c5d6e7f8"
	in := map[string]any{
		"name": "mongo",
		"services": []any{
			map[string]any{
				"id":         map[string]any{"$oid": hex},
				"created_at": map[string]any{"$date": "2026-01-02T03:04:05Z"},
				"name":       "frontend",
			},
		},
	}
	out := unwrapEJSON(in).(bson.M)
	if out["name"].(string) != "mongo" {
		t.Fatalf("name lost")
	}
	svcs := out["services"].([]any)
	if len(svcs) != 1 {
		t.Fatalf("services len %d", len(svcs))
	}
	svc := svcs[0].(bson.M)
	oid, _ := primitive.ObjectIDFromHex(hex)
	if svc["id"] != oid {
		t.Fatalf("nested $oid not unwrapped: %#v", svc["id"])
	}
	if _, ok := svc["created_at"].(time.Time); !ok {
		t.Fatalf("nested $date not unwrapped: %T", svc["created_at"])
	}
}

// TestNormaliseDoc_TranslatesUserAndTenantIDs ensures the per-collection
// inserts point at the destination's vendor row, not the source's. If
// idMap doesn't fire for either field, downstream reads (e.g. the WHM
// /apps endpoint that joins on user_id) come back empty even though the
// rows are physically present.
func TestNormaliseDoc_TranslatesUserAndTenantIDs(t *testing.T) {
	srcUser := "65000000000000000000aaaa"
	srcTen := "65000000000000000000bbbb"
	dstUser := primitive.NewObjectID()
	dstTen := primitive.NewObjectID()
	idMap := map[string]primitive.ObjectID{
		srcUser: dstUser,
		srcTen:  dstTen,
	}
	s := &TransferService{} // none of the fields used in this path

	in := map[string]any{
		"_id":       map[string]any{"$oid": "650000000000000000009999"},
		"name":      "myapp",
		"user":      "vendor1",
		"user_id":   map[string]any{"$oid": srcUser},
		"tenant_id": map[string]any{"$oid": srcTen},
	}
	out := s.normaliseDoc(in, idMap)

	if _, hasID := out["_id"]; hasID {
		t.Fatal("_id should be stripped (will be re-stamped at insert)")
	}
	if out["user_id"] != dstUser {
		t.Fatalf("user_id not translated: %#v", out["user_id"])
	}
	if out["tenant_id"] != dstTen {
		t.Fatalf("tenant_id not translated: %#v", out["tenant_id"])
	}
	if out["name"].(string) != "myapp" || out["user"].(string) != "vendor1" {
		t.Fatal("scalar fields lost")
	}
}

// TestNormaliseDoc_KeepsUntranslatedRefAsObjectID covers the fallback
// case: source has a user_id we don't know about (vendor wasn't selected
// for transfer, or admin row that doesn't exist on dest). Don't blank
// the field — keep the original ObjectID so the destination admin can
// see it for triage.
func TestNormaliseDoc_KeepsUntranslatedRefAsObjectID(t *testing.T) {
	s := &TransferService{}
	hex := "650000000000000000007777"
	in := map[string]any{
		"name":    "orphan",
		"user_id": map[string]any{"$oid": hex},
	}
	out := s.normaliseDoc(in, map[string]primitive.ObjectID{})
	oid, _ := primitive.ObjectIDFromHex(hex)
	if out["user_id"] != oid {
		t.Fatalf("untranslated ref should fall back to original ObjectID, got %#v", out["user_id"])
	}
}

// TestBuildRecoveryVhostSpec_PreservesAliases pins the shape of the
// vhost spec produced by the transfer recovery / heal paths for every
// service role. Before the 3.0.2 fix, recovery built a single-domain
// VhostConfig that silently dropped svc.AliasDomains — services with
// primary=a.com, aliases=[b.com,c.com] landed as server_name a.com;
// only, leaving b.com/c.com routed to whatever default vhost nginx
// picked. These cases guard against a regression.
func TestBuildRecoveryVhostSpec_PreservesAliases(t *testing.T) {
	t.Run("backend role carries primary + aliases and proxies PathPrefix→Port", func(t *testing.T) {
		svc := &models.ProjectService{
			Role:          "backend",
			PrimaryDomain: "a.com",
			AliasDomains:  []string{"b.com", "c.com"},
			Port:          4001,
			PathPrefix:    "/api",
		}
		spec := buildRecoveryVhostSpec(svc)
		if spec.PrimaryDomain != "a.com" {
			t.Fatalf("primary: got %q want a.com", spec.PrimaryDomain)
		}
		if len(spec.Aliases) != 2 || spec.Aliases[0] != "b.com" || spec.Aliases[1] != "c.com" {
			t.Fatalf("aliases: got %#v want [b.com c.com]", spec.Aliases)
		}
		if spec.Root != "" {
			t.Fatalf("backend spec must not set Root, got %q", spec.Root)
		}
		if len(spec.Proxies) != 1 || spec.Proxies[0].Prefix != "/api" || spec.Proxies[0].Port != 4001 {
			t.Fatalf("backend proxy: got %#v want [{/api 4001}]", spec.Proxies)
		}
	})

	t.Run("backend defaults PathPrefix empty → /", func(t *testing.T) {
		svc := &models.ProjectService{
			Role: "backend", PrimaryDomain: "a.com", Port: 3000,
		}
		spec := buildRecoveryVhostSpec(svc)
		if spec.Proxies[0].Prefix != "/" {
			t.Fatalf("default prefix: got %q want /", spec.Proxies[0].Prefix)
		}
	})

	t.Run("frontend role uses BuildDir as Root, no proxy", func(t *testing.T) {
		svc := &models.ProjectService{
			Role:          "frontend",
			PrimaryDomain: "site.com",
			AliasDomains:  []string{"www.site.com"},
			BuildDir:      "/opt/sp/projects/site/dist",
			InstallDir:    "/opt/sp/projects/site",
		}
		spec := buildRecoveryVhostSpec(svc)
		if spec.Root != "/opt/sp/projects/site/dist" {
			t.Fatalf("root: got %q want BuildDir", spec.Root)
		}
		if len(spec.Proxies) != 0 {
			t.Fatalf("frontend must not add proxies, got %#v", spec.Proxies)
		}
		if len(spec.Aliases) != 1 || spec.Aliases[0] != "www.site.com" {
			t.Fatalf("aliases: got %#v", spec.Aliases)
		}
	})

	t.Run("frontend falls back to InstallDir when BuildDir empty", func(t *testing.T) {
		svc := &models.ProjectService{
			Role: "frontend", PrimaryDomain: "site.com",
			InstallDir: "/opt/sp/projects/site",
		}
		spec := buildRecoveryVhostSpec(svc)
		if spec.Root != "/opt/sp/projects/site" {
			t.Fatalf("root fallback: got %q want InstallDir", spec.Root)
		}
	})

	t.Run("fullstack role gets BOTH Root and a /api proxy by default", func(t *testing.T) {
		svc := &models.ProjectService{
			Role:          "fullstack",
			PrimaryDomain: "app.com",
			AliasDomains:  []string{"app.co"},
			BuildDir:      "/opt/sp/projects/app/out",
			Port:          5000,
		}
		spec := buildRecoveryVhostSpec(svc)
		if spec.Root != "/opt/sp/projects/app/out" {
			t.Fatalf("fullstack root: got %q", spec.Root)
		}
		if len(spec.Proxies) != 1 || spec.Proxies[0].Prefix != "/api" || spec.Proxies[0].Port != 5000 {
			t.Fatalf("fullstack default proxy: got %#v", spec.Proxies)
		}
	})

	t.Run("unknown role falls back to a / proxy so CreateProjectVhost's validator is satisfied", func(t *testing.T) {
		svc := &models.ProjectService{
			Role: "worker", PrimaryDomain: "wk.com", Port: 9000,
		}
		spec := buildRecoveryVhostSpec(svc)
		if len(spec.Proxies) != 1 || spec.Proxies[0].Prefix != "/" || spec.Proxies[0].Port != 9000 {
			t.Fatalf("worker fallback: got %#v", spec.Proxies)
		}
	})

	t.Run("nil alias slice stays nil (back-compat: single-domain services)", func(t *testing.T) {
		svc := &models.ProjectService{
			Role: "backend", PrimaryDomain: "a.com", Port: 3000,
		}
		spec := buildRecoveryVhostSpec(svc)
		if len(spec.Aliases) != 0 {
			t.Fatalf("empty aliases: got %#v", spec.Aliases)
		}
	})

	t.Run("caller mutation of returned spec.Aliases does not leak back into the input slice", func(t *testing.T) {
		in := []string{"b.com"}
		svc := &models.ProjectService{
			Role: "backend", PrimaryDomain: "a.com", Port: 3000, AliasDomains: in,
		}
		spec := buildRecoveryVhostSpec(svc)
		spec.Aliases = append(spec.Aliases, "d.com")
		if len(in) != 1 || in[0] != "b.com" {
			t.Fatalf("input slice mutated by caller: got %#v", in)
		}
	})
}
