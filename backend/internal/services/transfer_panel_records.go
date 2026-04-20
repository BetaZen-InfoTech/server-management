package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
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

	// Projects need a custom sync because we have to remember the
	// source→destination project _id mapping for the services / deployments
	// sync below. The generic syncSimpleByUser doesn't expose that.
	projInserted, projIDMap := s.syncProjectsForTransfer(ctx, jobID, host, port, sshUser, sshPass, srcDB, picked, idMap)
	stats["projects"] = projInserted
	// Now bring across the dependent rows, with project_id remapped.
	stats["project_services"] = s.syncProjectServices(ctx, jobID, host, port, sshUser, sshPass, srcDB, projIDMap, idMap)
	stats["project_deployments"] = s.syncProjectDeployments(ctx, jobID, host, port, sshUser, sshPass, srcDB, projIDMap)

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

	stats["ssh_keys"] = s.syncSimpleByUser(ctx, jobID, host, port, sshUser, sshPass, srcDB,
		database.ColSSHKeys, "user", picked, idMap,
		func(doc map[string]any) (bson.M, string) {
			u, _ := doc["user"].(string)
			n, _ := doc["name"].(string)
			return s.normaliseDoc(doc, idMap), fmt.Sprintf("user=%q,name=%q", u, n)
		},
		// Dedup by fingerprint when present (the panel computes it on
		// add); fall back to user+name. Matching by raw public_key would
		// be brittle across line endings.
		func(doc bson.M) bson.M {
			if fp, _ := doc["fingerprint"].(string); fp != "" {
				return bson.M{"fingerprint": fp}
			}
			return bson.M{"user": doc["user"], "name": doc["name"]}
		})

	// Hosting packages catalog. NOT keyed by linux user — it's a global
	// per-tenant catalog. We pull every package the source admin owns
	// (created_by = source admin user_id) and copy to dest. The package_id
	// refs on synced User rows then resolve to the right package row,
	// instead of every migrated user pointing at the "Migrated" placeholder.
	stats["packages"] = s.syncPackagesCatalog(ctx, jobID, host, port, sshUser, sshPass, srcDB, idMap)

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

	// Apps recovery — for every app row that just landed on the destination,
	// try to start its systemd unit. If the unit doesn't exist (because
	// the source's /etc/systemd/system/sp-app-<name>.service wasn't part
	// of the file transfer) the start fails and we mark the app status as
	// "needs_deploy" so the operator sees it in the WHM Apps page and can
	// click Deploy. Without this step every freshly-synced app sat at
	// status="stopped" with no hint of what to do next.
	stats["apps_restarted"] = s.tryStartSyncedApps(ctx, jobID, picked)

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
			// Strip the panel's own management domain here. The earlier
			// stripPanelDomain pass on discovered.Domains doesn't reach
			// this code path (we're reading raw mongo on the source, not
			// the discovery output) and a leak here cascades into every
			// domain-keyed sync below — mailboxes/ssl/forwarders for
			// panel.example.com would all land on the destination.
			for _, key := range []string{"name", "domain"} {
				if v, _ := d[key].(string); v != "" && !s.isPanelDomain(v) {
					ownedDomains[v] = true
				}
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
		// Defence in depth: if the doc carries the panel's own management
		// domain in any common field, drop it. ownedDomains was already
		// stripped earlier, but this guard means a future caller can
		// add a new domain-keyed sync without having to remember the
		// strip — unsafe-by-omission is the wrong default.
		if d, _ := doc["domain"].(string); d != "" && s.isPanelDomain(d) {
			continue
		}
		if d, _ := doc["name"].(string); d != "" && strings.Contains(d, ".") && s.isPanelDomain(d) {
			continue
		}

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

// tryStartSyncedApps walks the destination's `apps` collection for every
// app owned by a freshly-transferred linux user, runs `systemctl start
// sp-app-<name>` on the host, and stamps the result back into mongo.
//
// Three outcomes per app:
//   - systemd unit exists + start succeeds → status="running"
//   - systemd unit exists + start fails    → status="failed", error in details
//   - systemd unit missing                 → status="needs_deploy" (the file
//     transfer didn't bring the unit across; operator clicks Deploy in WHM
//     to recreate it from the cloned source). Marked separately from
//     "stopped" so the WHM Apps page can show a clear "click Deploy"
//     prompt instead of leaving the operator wondering why Start does
//     nothing.
//
// Returns the count of apps that landed in status="running".
func (s *TransferService) tryStartSyncedApps(ctx context.Context, jobID string, picked map[string]bool) int {
	if len(picked) == 0 {
		return 0
	}
	users := make([]string, 0, len(picked))
	for u := range picked {
		users = append(users, u)
	}
	col := s.db.Collection(database.ColApps)
	cursor, err := col.Find(ctx, bson.M{"user": bson.M{"$in": users}})
	if err != nil {
		return 0
	}
	defer cursor.Close(ctx)

	type appRow struct {
		ID   primitive.ObjectID `bson:"_id"`
		Name string             `bson:"name"`
	}
	var rows []appRow
	if err := cursor.All(ctx, &rows); err != nil {
		return 0
	}

	running := 0
	for _, r := range rows {
		if r.Name == "" {
			continue
		}
		unit := "sp-app-" + r.Name
		// systemctl returns 5 (FAILED) when the unit file is missing
		// (LoadError=NotFound). We probe with `systemctl list-unit-files`
		// first so we can distinguish "no unit" from "unit failed to
		// start" without parsing systemctl's stderr.
		probe, _ := agent.RunCommand(ctx, "systemctl", "list-unit-files", unit+".service")
		if probe == nil || !strings.Contains(probe.Output, unit+".service") {
			s.addLog(ctx, jobID, "info",
				fmt.Sprintf("App %q has no systemd unit on this host — marking needs_deploy. Click Deploy in WHM to recreate the unit from the migrated /home/<user>/apps/<name>/ tree.", r.Name),
				"apps")
			col.UpdateOne(ctx, bson.M{"_id": r.ID}, bson.M{"$set": bson.M{"status": "needs_deploy", "updated_at": time.Now()}})
			continue
		}
		if err := agent.ServiceAction(ctx, unit, "start"); err != nil {
			s.addLog(ctx, jobID, "warn",
				fmt.Sprintf("App %q failed to start: %s", r.Name, err.Error()),
				"apps")
			col.UpdateOne(ctx, bson.M{"_id": r.ID}, bson.M{"$set": bson.M{"status": "failed", "updated_at": time.Now()}})
			continue
		}
		col.UpdateOne(ctx, bson.M{"_id": r.ID}, bson.M{"$set": bson.M{"status": "running", "updated_at": time.Now()}})
		running++
	}
	return running
}

// syncProjectsForTransfer copies the source's `projects` rows for the
// picked linux users into the destination, and returns a
// `srcProjectID(hex) → dstProjectID(ObjectID)` map so the dependent
// project_services / project_deployments syncs can rewrite their
// project_id refs to point at the freshly-stamped destination rows.
//
// Dedup is by (slug, user) — the panel's natural key — so re-running a
// transfer doesn't double-insert. When an existing destination row is
// matched, its _id is added to the map under the source's hex so the
// dependent syncs still find a target.
func (s *TransferService) syncProjectsForTransfer(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string, picked map[string]bool, idMap map[string]primitive.ObjectID) (int, map[string]primitive.ObjectID) {
	projIDMap := map[string]primitive.ObjectID{}
	if len(picked) == 0 {
		return 0, projIDMap
	}
	quoted := make([]string, 0, len(picked))
	for u := range picked {
		quoted = append(quoted, fmt.Sprintf("%q", u))
	}
	filter := fmt.Sprintf(`{"user":{"$in":[%s]}}`, strings.Join(quoted, ","))
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColProjects, filter)
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source projects: %s", err), "panel-records")
		return 0, projIDMap
	}
	col := s.db.Collection(database.ColProjects)
	inserted := 0
	for _, raw := range docs {
		oldID := extractOID(raw["_id"])
		doc := s.normaliseDoc(raw, idMap)
		slug, _ := doc["slug"].(string)
		user, _ := doc["user"].(string)
		if slug == "" || user == "" {
			continue
		}

		var existing bson.M
		err := col.FindOne(ctx, bson.M{"slug": slug, "user": user}).Decode(&existing)
		if err == nil {
			if oid, ok := existing["_id"].(primitive.ObjectID); ok && oldID != "" {
				projIDMap[oldID] = oid
			}
			continue
		}
		if err != mongo.ErrNoDocuments {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("project lookup user=%q slug=%q: %s", user, slug, err), "panel-records")
			continue
		}
		newOID := primitive.NewObjectID()
		doc["_id"] = newOID
		if _, err := col.InsertOne(ctx, doc); err != nil {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("insert project user=%q slug=%q failed: %s", user, slug, err), "panel-records")
			continue
		}
		if oldID != "" {
			projIDMap[oldID] = newOID
		}
		inserted++
	}
	return inserted, projIDMap
}

// syncProjectServices copies the source's `project_services` rows whose
// project_id is in projIDMap, rewriting project_id to the destination's
// new ObjectID. Without this, the destination's Deploy Software page
// shows project shells with no services under them — the operator
// would have to re-add each service by hand.
//
// Dedup is by (project_id, name) on the destination — that's how the
// service AddService path enforces uniqueness.
func (s *TransferService) syncProjectServices(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string, projIDMap map[string]primitive.ObjectID, idMap map[string]primitive.ObjectID) int {
	if len(projIDMap) == 0 {
		return 0
	}
	srcIDs := make([]string, 0, len(projIDMap))
	for src := range projIDMap {
		srcIDs = append(srcIDs, fmt.Sprintf(`{"$oid":%q}`, src))
	}
	filter := fmt.Sprintf(`{"project_id":{"$in":[%s]}}`, strings.Join(srcIDs, ","))
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColProjectServices, filter)
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source project_services: %s", err), "panel-records")
		return 0
	}
	col := s.db.Collection(database.ColProjectServices)
	inserted := 0
	for _, raw := range docs {
		doc := s.normaliseDoc(raw, idMap)
		// Translate project_id through projIDMap. The normaliseDoc loop
		// above doesn't cover project_id (that field name isn't in its
		// generic ref-translation list — it's project-specific).
		oldProj := extractOID(raw["project_id"])
		newProj, ok := projIDMap[oldProj]
		if !ok {
			continue // project wasn't synced — orphan service, drop
		}
		doc["project_id"] = newProj

		name, _ := doc["name"].(string)
		if name == "" {
			continue
		}
		var existing bson.M
		err := col.FindOne(ctx, bson.M{"project_id": newProj, "name": name}).Decode(&existing)
		if err == nil {
			continue
		}
		if err != mongo.ErrNoDocuments {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("project_service lookup name=%q: %s", name, err), "panel-records")
			continue
		}
		doc["_id"] = primitive.NewObjectID()
		if _, err := col.InsertOne(ctx, doc); err != nil {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("insert project_service name=%q failed: %s", name, err), "panel-records")
			continue
		}
		inserted++
	}
	return inserted
}

// syncProjectDeployments copies historical deploy records so the project
// page's "Recent deployments" panel isn't empty after a migration. Same
// project_id rewrite as syncProjectServices.
func (s *TransferService) syncProjectDeployments(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string, projIDMap map[string]primitive.ObjectID) int {
	if len(projIDMap) == 0 {
		return 0
	}
	srcIDs := make([]string, 0, len(projIDMap))
	for src := range projIDMap {
		srcIDs = append(srcIDs, fmt.Sprintf(`{"$oid":%q}`, src))
	}
	filter := fmt.Sprintf(`{"project_id":{"$in":[%s]}}`, strings.Join(srcIDs, ","))
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColProjectDeployments, filter)
	if err != nil {
		// Best-effort — deploy history isn't critical, don't warn loudly.
		return 0
	}
	col := s.db.Collection(database.ColProjectDeployments)
	inserted := 0
	for _, raw := range docs {
		doc := s.normaliseDoc(raw, nil)
		oldProj := extractOID(raw["project_id"])
		newProj, ok := projIDMap[oldProj]
		if !ok {
			continue
		}
		doc["project_id"] = newProj
		doc["_id"] = primitive.NewObjectID()
		if _, err := col.InsertOne(ctx, doc); err != nil {
			continue
		}
		inserted++
	}
	return inserted
}

// syncPackagesCatalog copies the source's hosting_packages collection to
// the destination. Dedup is by name (the panel's natural key — the
// "Add Package" form refuses duplicates per tenant). Returns the count
// of newly inserted packages.
//
// Why this matters: the file-transfer step's old behaviour squashed every
// migrated linux user into a single "Migrated" placeholder package
// (transfer_service.go's migratedPkgID path). With the real catalog
// synced here, the per-user package_id references that came in via
// the users sync resolve to actual package rows on the destination,
// not to a phantom name.
func (s *TransferService) syncPackagesCatalog(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string, idMap map[string]primitive.ObjectID) int {
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColPackages, "{}")
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source packages: %s", err), "panel-records")
		return 0
	}
	col := s.db.Collection(database.ColPackages)
	inserted := 0
	for _, raw := range docs {
		doc := s.normaliseDoc(raw, idMap)
		name, _ := doc["name"].(string)
		if name == "" {
			continue
		}
		var existing bson.M
		if err := col.FindOne(ctx, bson.M{"name": name}).Decode(&existing); err == nil {
			continue
		} else if err != mongo.ErrNoDocuments {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("packages lookup %q: %s", name, err), "panel-records")
			continue
		}
		doc["_id"] = primitive.NewObjectID()
		// Reset the per-package account counter — it tracked the source's
		// vendor count, not ours; the file-transfer + sync passes will
		// re-increment it as users land.
		doc["account_count"] = 0
		if _, err := col.InsertOne(ctx, doc); err != nil {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("insert package %q failed: %s", name, err), "panel-records")
			continue
		}
		inserted++
	}
	return inserted
}

// mergeAuthorizedKeysForUser appends any of `keys` that aren't already
// present in the destination's /home/<sysUser>/.ssh/authorized_keys
// (or /root/.ssh/authorized_keys for root). Returns the number of new
// lines added.
//
// Dedup is by the key body (the second whitespace-delimited field —
// "<keytype> <base64> [comment]") so two entries for the same key with
// different comment fields are treated as duplicates. This keeps a
// re-run from doubling up the file.
//
// File mode and ownership are restored to what sshd will accept (700
// on .ssh, 600 on authorized_keys, owned by the linux user). Without
// the explicit chmod, sshd silently ignores world/group-writable
// authorized_keys and the new keys do nothing.
func mergeAuthorizedKeysForUser(ctx context.Context, sysUser string, keys []string) (int, error) {
	homeDir := "/home/" + sysUser
	if sysUser == "root" {
		homeDir = "/root"
	}
	sshDir := homeDir + "/.ssh"
	authPath := sshDir + "/authorized_keys"

	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return 0, fmt.Errorf("mkdir %s: %w", sshDir, err)
	}

	existing := map[string]bool{}
	if data, err := os.ReadFile(authPath); err == nil {
		for _, ln := range strings.Split(string(data), "\n") {
			body := keyBody(ln)
			if body != "" {
				existing[body] = true
			}
		}
	}

	added := 0
	var sb strings.Builder
	for _, ln := range keys {
		body := keyBody(ln)
		if body == "" || existing[body] {
			continue
		}
		existing[body] = true
		sb.WriteString(strings.TrimRight(ln, "\n"))
		sb.WriteByte('\n')
		added++
	}
	if added == 0 {
		return 0, nil
	}

	f, err := os.OpenFile(authPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", authPath, err)
	}
	if _, err := f.WriteString(sb.String()); err != nil {
		f.Close()
		return 0, fmt.Errorf("write %s: %w", authPath, err)
	}
	f.Close()

	// Restore perms + ownership (sshd is strict).
	_ = os.Chmod(authPath, 0o600)
	_ = os.Chmod(sshDir, 0o700)
	if sysUser != "root" {
		_, _ = agent.RunCommand(ctx, "chown", "-R", sysUser+":"+sysUser, sshDir)
	}
	return added, nil
}

// keyBody returns the "<keytype> <base64>" portion of an authorized_keys
// line, stripping the trailing comment field. Empty for blank/comment lines.
func keyBody(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + " " + parts[1]
}
