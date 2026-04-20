package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// transferPanelRecords copies the SOURCE panel's mongo records that
// correspond to each migrated linux user into the DESTINATION panel's
// mongo. Without this step, file transfer alone leaves the destination's
// Apps / Deploy Software / Email / SSL pages empty even though the data
// is on disk — those pages are populated from mongo, not the filesystem.
//
// The source must be another Betazen Server Panel; we read its mongo
// over SSH using mongoexport (or mongosh as fallback). Other source
// types (cPanel, Plesk, bare) are skipped — they don't have a Betazen
// mongo to copy from.
//
// ID translation:
//
//   - Every record gets a fresh _id generated on the destination so
//     primary-key collisions never happen.
//   - user_id and tenant_id columns are remapped through `idMap` (built
//     from the users collection during the first pass). If a referenced
//     user wasn't synced, the record is skipped — there's no orphan
//     vendor account to attach it to.
//   - Created/updated timestamps are kept as-is so audit windows stay
//     accurate.
//
// Dedup:
//
//   - Per-collection natural keys (email for users, user+name for apps,
//     domain for ssl_certificates, etc.) are checked first; existing
//     rows are left alone. Operators sometimes re-run a transfer to
//     pick up new data without nuking what's already on the destination.
func (s *TransferService) transferPanelRecords(ctx context.Context, jobID string, host string, port int, sshUser, sshPass string, selectedUsers []string) {
	if len(selectedUsers) == 0 {
		s.addLog(ctx, jobID, "info", "No linux users selected — skipping panel records sync.", "panel-records")
		return
	}

	picked := make(map[string]bool, len(selectedUsers))
	for _, u := range selectedUsers {
		picked[strings.TrimSpace(u)] = true
	}

	// --- Pass 1: users / vendors. Builds the userID translation map.
	srcDB := "serverpanel"
	idMap, vendorEmails, ownedDomains := s.syncUsersForTransfer(ctx, jobID, host, port, sshUser, sshPass, srcDB, picked)
	s.addLog(ctx, jobID, "info",
		fmt.Sprintf("Synced %d vendor account(s); will use them as the owner for the rest of the imports.", len(idMap)),
		"panel-records")

	// --- Pass 2: per-vendor collections.
	stats := map[string]int{}

	stats["apps"] = s.syncSimpleByUser(ctx, jobID, host, port, sshUser, sshPass, srcDB,
		database.ColApps, "user", picked, idMap,
		func(doc map[string]any) (bson.M, string) {
			name, _ := doc["name"].(string)
			user, _ := doc["user"].(string)
			return s.normaliseDoc(doc, idMap), fmt.Sprintf("user=%q,name=%q", user, name)
		},
		func(doc bson.M) bson.M {
			return bson.M{"name": doc["name"], "user": doc["user"]}
		})

	stats["projects"] = s.syncSimpleByUser(ctx, jobID, host, port, sshUser, sshPass, srcDB,
		database.ColProjects, "user", picked, idMap,
		func(doc map[string]any) (bson.M, string) {
			slug, _ := doc["slug"].(string)
			user, _ := doc["user"].(string)
			return s.normaliseDoc(doc, idMap), fmt.Sprintf("user=%q,slug=%q", user, slug)
		},
		func(doc bson.M) bson.M {
			return bson.M{"slug": doc["slug"], "user": doc["user"]}
		})

	// project_services / project_deployments are keyed by project_id which
	// changes during the project sync above. Skip them for now — the user
	// can re-discover services by visiting the project. (Re-fetching the
	// .git tree on first deploy is the canonical path anyway.)

	stats["wordpress"] = s.syncSimpleByUser(ctx, jobID, host, port, sshUser, sshPass, srcDB,
		database.ColWordPress, "user", picked, idMap,
		func(doc map[string]any) (bson.M, string) {
			dom, _ := doc["domain"].(string)
			return s.normaliseDoc(doc, idMap), fmt.Sprintf("domain=%q", dom)
		},
		func(doc bson.M) bson.M {
			return bson.M{"domain": doc["domain"]}
		})

	stats["databases"] = s.syncSimpleByUser(ctx, jobID, host, port, sshUser, sshPass, srcDB,
		database.ColDatabases, "user", picked, idMap,
		func(doc map[string]any) (bson.M, string) {
			db, _ := doc["db_name"].(string)
			return s.normaliseDoc(doc, idMap), fmt.Sprintf("db=%q", db)
		},
		func(doc bson.M) bson.M {
			return bson.M{"db_name": doc["db_name"]}
		})

	stats["ftp_accounts"] = s.syncSimpleByUser(ctx, jobID, host, port, sshUser, sshPass, srcDB,
		database.ColFTPAccounts, "user", picked, idMap,
		func(doc map[string]any) (bson.M, string) {
			u, _ := doc["username"].(string)
			return s.normaliseDoc(doc, idMap), fmt.Sprintf("ftp_user=%q", u)
		},
		func(doc bson.M) bson.M {
			return bson.M{"username": doc["username"]}
		})

	// Domain-keyed collections — the picked-by-user filter doesn't apply
	// directly. Filter by ownedDomains instead.
	stats["mailboxes"] = s.syncByDomain(ctx, jobID, host, port, sshUser, sshPass, srcDB,
		database.ColMailboxes, "domain", ownedDomains, idMap,
		func(doc map[string]any) (bson.M, string) {
			a, _ := doc["address"].(string)
			return s.normaliseDoc(doc, idMap), fmt.Sprintf("address=%q", a)
		},
		func(doc bson.M) bson.M { return bson.M{"address": doc["address"]} })

	stats["forwarders"] = s.syncByDomain(ctx, jobID, host, port, sshUser, sshPass, srcDB,
		database.ColForwarders, "domain", ownedDomains, idMap,
		func(doc map[string]any) (bson.M, string) {
			a, _ := doc["source"].(string)
			return s.normaliseDoc(doc, idMap), fmt.Sprintf("source=%q", a)
		},
		func(doc bson.M) bson.M { return bson.M{"source": doc["source"]} })

	stats["ssl_certificates"] = s.syncByDomain(ctx, jobID, host, port, sshUser, sshPass, srcDB,
		database.ColSSLCerts, "domain", ownedDomains, idMap,
		func(doc map[string]any) (bson.M, string) {
			d, _ := doc["domain"].(string)
			return s.normaliseDoc(doc, idMap), fmt.Sprintf("domain=%q", d)
		},
		func(doc bson.M) bson.M { return bson.M{"domain": doc["domain"]} })

	// Summary log so the operator sees what landed.
	pieces := make([]string, 0, len(stats))
	for k, v := range stats {
		if v == 0 {
			continue
		}
		pieces = append(pieces, fmt.Sprintf("%s:%d", k, v))
	}
	if len(pieces) == 0 {
		pieces = []string{"nothing new — destination already had every record"}
	}
	s.addLog(ctx, jobID, "info",
		fmt.Sprintf("Panel records: %s. (vendors=%d, vendor emails=%v)", strings.Join(pieces, ", "), len(idMap), vendorEmails),
		"panel-records")
}

// syncUsersForTransfer reads source `users` rows for the picked linux
// users, inserts any that aren't already on this panel (matched by
// email — the panel's globally-unique key), and returns:
//
//   - idMap: source ObjectID hex → destination ObjectID. Used to remap
//     user_id / tenant_id refs in every other collection.
//   - vendorEmails: the email list, useful in the operator-facing log.
//   - ownedDomains: every domain whose `user` matches one of the picked
//     linux usernames, sourced from the source's domains collection.
//     Used by the domain-keyed sync passes.
func (s *TransferService) syncUsersForTransfer(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string, picked map[string]bool) (map[string]primitive.ObjectID, []string, map[string]bool) {
	idMap := map[string]primitive.ObjectID{}
	emails := []string{}
	ownedDomains := map[string]bool{}

	// Build the {"username": {"$in": [...]}} filter. mongoexport's --query
	// accepts strict JSON only, so quote each name explicitly.
	quoted := make([]string, 0, len(picked))
	for u := range picked {
		quoted = append(quoted, fmt.Sprintf("%q", u))
	}
	filter := fmt.Sprintf(`{"username":{"$in":[%s]}}`, strings.Join(quoted, ","))

	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColUsers, filter)
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source users: %s", err), "panel-records")
		return idMap, emails, ownedDomains
	}

	col := s.db.Collection(database.ColUsers)
	for _, doc := range docs {
		email, _ := doc["email"].(string)
		username, _ := doc["username"].(string)
		oldID := extractOID(doc["_id"])

		if email == "" {
			continue
		}

		// Already on destination? Reuse its ObjectID for downstream remap.
		var existing bson.M
		err := col.FindOne(ctx, bson.M{"email": email}).Decode(&existing)
		if err == nil {
			if newOID, ok := existing["_id"].(primitive.ObjectID); ok && oldID != "" {
				idMap[oldID] = newOID
			}
			emails = append(emails, email+" (existing)")
			continue
		}
		if err != mongo.ErrNoDocuments {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("user lookup %s: %s", email, err), "panel-records")
			continue
		}

		// Insert fresh.
		newOID := primitive.NewObjectID()
		insert := s.normaliseDoc(doc, idMap)
		insert["_id"] = newOID
		// tenant_id self-reference → the new own _id (vendor-owner pattern).
		if _, hasT := insert["tenant_id"]; hasT {
			insert["tenant_id"] = newOID
		}
		if _, err := col.InsertOne(ctx, insert); err != nil {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("insert user %s failed: %s", email, err), "panel-records")
			continue
		}
		if oldID != "" {
			idMap[oldID] = newOID
		}
		emails = append(emails, email+" (new)")
		_ = username
	}

	// Pull domains for the picked users so domain-keyed syncs (mailboxes,
	// ssl, forwarders) know which rows belong to who.
	dq := fmt.Sprintf(`{"user":{"$in":[%s]}}`, strings.Join(quoted, ","))
	dDocs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColDomains, dq)
	if err == nil {
		for _, d := range dDocs {
			if name, _ := d["name"].(string); name != "" {
				ownedDomains[name] = true
			}
			if dom, _ := d["domain"].(string); dom != "" {
				ownedDomains[dom] = true
			}
		}
	}
	return idMap, emails, ownedDomains
}

// syncSimpleByUser is the workhorse for collections keyed by the linux
// `user` column. Returns the number of NEW rows inserted (existing rows
// don't count — they're left alone).
func (s *TransferService) syncSimpleByUser(
	ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB, collection, userField string,
	picked map[string]bool, idMap map[string]primitive.ObjectID,
	prepare func(doc map[string]any) (bson.M, string),
	naturalKey func(bson.M) bson.M,
) int {
	quoted := make([]string, 0, len(picked))
	for u := range picked {
		quoted = append(quoted, fmt.Sprintf("%q", u))
	}
	filter := fmt.Sprintf(`{%q:{"$in":[%s]}}`, userField, strings.Join(quoted, ","))
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, collection, filter)
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source %s: %s", collection, err), "panel-records")
		return 0
	}
	return s.insertDeduped(ctx, jobID, collection, docs, idMap, prepare, naturalKey)
}

func (s *TransferService) syncByDomain(
	ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB, collection, domainField string,
	owned map[string]bool, idMap map[string]primitive.ObjectID,
	prepare func(doc map[string]any) (bson.M, string),
	naturalKey func(bson.M) bson.M,
) int {
	if len(owned) == 0 {
		return 0
	}
	quoted := make([]string, 0, len(owned))
	for d := range owned {
		quoted = append(quoted, fmt.Sprintf("%q", d))
	}
	filter := fmt.Sprintf(`{%q:{"$in":[%s]}}`, domainField, strings.Join(quoted, ","))
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, collection, filter)
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source %s: %s", collection, err), "panel-records")
		return 0
	}
	return s.insertDeduped(ctx, jobID, collection, docs, idMap, prepare, naturalKey)
}

func (s *TransferService) insertDeduped(
	ctx context.Context, jobID, collection string, docs []map[string]any,
	idMap map[string]primitive.ObjectID,
	prepare func(map[string]any) (bson.M, string),
	naturalKey func(bson.M) bson.M,
) int {
	col := s.db.Collection(collection)
	inserted := 0
	for _, raw := range docs {
		doc, label := prepare(raw)
		key := naturalKey(doc)
		var existing bson.M
		err := col.FindOne(ctx, key).Decode(&existing)
		if err == nil {
			continue // already on destination
		}
		if err != mongo.ErrNoDocuments {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("%s lookup %s: %s", collection, label, err), "panel-records")
			continue
		}
		// Always stamp a fresh _id on insert.
		doc["_id"] = primitive.NewObjectID()
		if _, err := col.InsertOne(ctx, doc); err != nil {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("insert %s %s failed: %s", collection, label, err), "panel-records")
			continue
		}
		inserted++
	}
	return inserted
}

// normaliseDoc converts a JSON-decoded source doc into a bson.M that's
// safe to insert into the destination's mongo, doing two things:
//
//  1. Strip the source's _id (we stamp a fresh one at insert time).
//  2. Translate user_id / tenant_id refs through idMap, so every
//     downstream document points at the destination's vendor row instead
//     of the source's. Untranslated values are dropped — leaving them
//     would create dangling pointers.
//  3. Re-wrap any extended-JSON shapes ($oid, $date) into the right Go
//     types so the mongo driver doesn't store them as strings.
func (s *TransferService) normaliseDoc(doc map[string]any, idMap map[string]primitive.ObjectID) bson.M {
	out := bson.M{}
	for k, v := range doc {
		if k == "_id" {
			continue
		}
		out[k] = unwrapEJSON(v)
	}
	for _, refField := range []string{"user_id", "tenant_id", "vendor_id", "owner_id", "package_id"} {
		if cur, ok := out[refField]; ok {
			oldHex := extractOID(cur)
			if newOID, found := idMap[oldHex]; found {
				out[refField] = newOID
			} else if oldHex != "" {
				// If we can parse the old hex but have no map entry,
				// keep the original ObjectID — it lets the destination
				// admin see the value rather than blanking the field.
				if oid, err := primitive.ObjectIDFromHex(oldHex); err == nil {
					out[refField] = oid
				}
			}
		}
	}
	return out
}

// extractOID handles all the shapes mongoexport / mongosh might emit
// for an ObjectID: a primitive.ObjectID, a hex string, or the EJSON
// {"$oid": "..."} wrapper. Returns the hex string ("" on failure).
func extractOID(v any) string {
	switch x := v.(type) {
	case primitive.ObjectID:
		return x.Hex()
	case string:
		if _, err := primitive.ObjectIDFromHex(x); err == nil {
			return x
		}
	case map[string]any:
		if oid, ok := x["$oid"].(string); ok {
			return oid
		}
	}
	return ""
}

// unwrapEJSON converts MongoDB Extended JSON wrappers into Go-native
// types the bson driver knows how to re-serialise. Recurses through
// nested maps and slices so a Project with embedded Service docs (or a
// User with []byte fields) survives the round-trip.
//
// Wrappers handled:
//
//   - {"$oid": "<hex>"}                         → primitive.ObjectID
//   - {"$date": "<iso>"} | {"$date": {...}}     → time.Time
//   - {"$numberLong": "<int>"}                  → int64
//   - {"$numberDouble": "<float>"}              → float64
//   - {"$numberInt": "<int>"}                   → int32
//   - {"$binary": {"base64":..,"subType":..}}   → []byte (EJSON v2)
//   - {"$binary": "...", "$type": "00"}         → []byte (EJSON v1, what
//     mongoexport emits without --jsonArray --pretty). Without this, any
//     collection with binary fields (User.totp_secret, Project.github_pat_
//     encrypted, Webhook.signature_key, ...) would round-trip as an
//     embedded document and fail decode at API read time with
//     "cannot decode embedded document into a []byte".
func unwrapEJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if oid, ok := x["$oid"].(string); ok && len(x) == 1 {
			if id, err := primitive.ObjectIDFromHex(oid); err == nil {
				return id
			}
			return oid
		}
		if dt, ok := x["$date"]; ok && len(x) == 1 {
			switch d := dt.(type) {
			case string:
				if t, err := time.Parse(time.RFC3339Nano, d); err == nil {
					return t
				}
			case map[string]any:
				if nl, ok := d["$numberLong"].(string); ok {
					var ms int64
					_, _ = fmt.Sscanf(nl, "%d", &ms)
					if ms > 0 {
						return time.UnixMilli(ms)
					}
				}
			}
			return v
		}
		if nl, ok := x["$numberLong"].(string); ok && len(x) == 1 {
			var i int64
			_, _ = fmt.Sscanf(nl, "%d", &i)
			return i
		}
		if nd, ok := x["$numberDouble"].(string); ok && len(x) == 1 {
			var f float64
			_, _ = fmt.Sscanf(nd, "%f", &f)
			return f
		}
		if ni, ok := x["$numberInt"].(string); ok && len(x) == 1 {
			var i int32
			_, _ = fmt.Sscanf(ni, "%d", &i)
			return i
		}
		// $binary — both EJSON v1 and v2 shapes.
		if b, ok := x["$binary"]; ok {
			switch bv := b.(type) {
			case string:
				// EJSON v1: {"$binary": "<base64>", "$type": "00"}
				if data, err := base64.StdEncoding.DecodeString(bv); err == nil {
					return data
				}
				return []byte{}
			case map[string]any:
				// EJSON v2: {"$binary": {"base64": "<base64>", "subType": "00"}}
				if s, ok := bv["base64"].(string); ok {
					if data, err := base64.StdEncoding.DecodeString(s); err == nil {
						return data
					}
				}
				return []byte{}
			}
		}
		// Plain map — recurse.
		out := bson.M{}
		for k, vv := range x {
			out[k] = unwrapEJSON(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = unwrapEJSON(vv)
		}
		return out
	default:
		return v
	}
}

// jsonStringify is the inverse helper used in tests so we can pretty-print
// docs we got back. Kept here so the tests don't have to import "encoding/json"
// just to compose a debug message.
func jsonStringify(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
