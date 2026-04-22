package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
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

	// Domains collection sync — pulls every source `domains` row owned by
	// a picked linux user. Without this, the destination's Domains page
	// only contains rows that the file-transfer step created (rows whose
	// /home/<owner>/domains/<dom>/ directory exists on disk). Any domain
	// the panel knows about but doesn't have a directory for — and any
	// domain referenced only by an app or project_service — would never
	// reach the Domains page.
	stats["domains"] = s.syncSimpleByUser(ctx, jobID, host, port, sshUser, sshPass, srcDB,
		database.ColDomains, "user", picked, idMap,
		func(doc map[string]any) (bson.M, string) {
			d, _ := doc["domain"].(string)
			return s.normaliseDoc(doc, idMap), fmt.Sprintf("domain=%q", d)
		},
		func(doc bson.M) bson.M {
			return bson.M{"domain": doc["domain"]}
		})

	// Enrich existing domain rows with registration metadata from source.
	// File transfer's per-domain wiring step creates a bare row (only
	// domain/user/php_version/status/created_at) BEFORE this sync runs;
	// insertDeduped above skips on FindOne hit, so the source's
	// registrar / registered_on / expires_on / auto_renew / nameservers /
	// whois_synced_at never make it across. Without this $set pass the
	// destination's Domains page shows expiry "—" + empty registrar for
	// every domain that had real WHOIS data on source. Idempotent.
	stats["domains_enriched"] = s.enrichDomainRegistration(ctx, jobID, host, port, sshUser, sshPass, srcDB, picked)

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

	// Materialize any app or project_service domains that didn't make it
	// into the domains collection through the source-side sync above.
	// Belt-and-braces — covers the case where a source app / service
	// references a domain that was never registered in the source's own
	// `domains` collection (rare, but possible if the app was deployed
	// before the domain row was created, or via a vendor scope that
	// hides it from the cross-tenant query).
	stats["domains_materialized"] = s.materializeReferencedDomains(ctx, jobID, picked)

	// Apps recovery — for every app row that just landed on the destination,
	// try to start its systemd unit. If the unit doesn't exist (because
	// the source's /etc/systemd/system/sp-app-<name>.service wasn't part
	// of the file transfer) the start fails and we mark the app status as
	// "needs_deploy" so the operator sees it in the WHM Apps page and can
	// click Deploy. Without this step every freshly-synced app sat at
	// status="stopped" with no hint of what to do next.
	stats["apps_restarted"] = s.tryStartSyncedApps(ctx, jobID, picked)

	// Project services recovery — same gap as apps for sp-proj-* units.
	// Without this every Deploy Software project landed in mongo but its
	// systemd unit didn't exist on the destination, leaving the project's
	// primary domain serving 404 from the panel default vhost.
	stats["projects_restarted"] = s.tryStartSyncedProjects(ctx, jobID, picked)

	// Vhost healer — final safety net. Walks every imported domain and
	// guarantees an nginx vhost exists for it. Catches the case where
	// the per-domain wiring step in Transfer Domains & Files silently
	// failed to write a vhost (cleanup race, nginx -t blip on a sibling
	// vhost, etc) and the operator would otherwise see a 404 from the
	// catch-all panel default vhost on freshly migrated domains.
	stats["vhosts_healed"] = s.healMissingVhosts(ctx, jobID, picked)

	// Enable any vhost files whose sites-enabled symlink is missing even
	// though the domain is active. Defensive — catches states where an
	// earlier flow wrote the vhost file but dropped the symlink
	// (DomainService.Suspend + never-unsuspend, certbot/ssl-upgrade race,
	// half-finished redeploy). Without this the project/app looks linked
	// to the domain in the UI but nginx returns 404 for every request,
	// matching the "Deploy Software and service not to link with domain"
	// shape of the bug report.
	stats["vhosts_enabled"] = s.healDisabledVhostSymlinks(ctx, jobID)

	// Maintenance state — preserve source's server-wide maintenance flag
	// on the destination. The expectation here mirrors the rest of the
	// transfer: if the operator put the source into maintenance, the
	// destination must come up the same way so DNS cutover doesn't
	// surface the new server in a broken state. Idempotent: writes the
	// destination's server_config doc and, if enabled, calls the local
	// MaintenanceService.EnableServer to apply the nginx changes.
	stats["maintenance_state"] = s.syncMaintenanceState(ctx, jobID, host, port, sshUser, sshPass, srcDB)

	// Repoint source's pdns records to the destination IP. Critical when
	// the transferred zones are publicly delegated to BOTH the source's
	// nameservers AND the destination's (e.g. dns1/dns2 → source,
	// dns3/dns4 → destination). External resolvers (incl. Gmail's SPF
	// check) round-robin across NSs; if half still serve the source IP
	// in A/SPF records, mail from the new server fails SPF
	// authentication 50% of the time. Updates A records that match the
	// source IP and rewrites every SPF TXT line to ip4:<dest IP>, then
	// bumps SOA serial + restarts pdns so secondaries notice. Idempotent.
	stats["source_dns_repointed"] = s.repointSourceDNSToDestination(ctx, jobID, host, port, sshUser, sshPass)

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

// materializeReferencedDomains scans the destination's apps + project_services
// rows for the picked linux users, collects every domain string they
// reference (apps.domain, services.primary_domain, services.alias_domains),
// and upserts a row into the domains collection for any that aren't
// already there. Default php_version=8.2 and status="active" — same shape
// the file-transfer step would have stamped if the directory existed.
//
// Without this, an app/service references a domain that has no domains-
// collection row, the WHM Domains page silently omits it, and the operator
// can't toggle SSL / change PHP version on it without using the API
// directly. Returns the number of newly-inserted domain rows.
func (s *TransferService) materializeReferencedDomains(ctx context.Context, jobID string, picked map[string]bool) int {
	if len(picked) == 0 {
		return 0
	}
	users := make([]string, 0, len(picked))
	for u := range picked {
		users = append(users, u)
	}

	// Existing domains, lower-cased so the membership check matches the
	// case-insensitive way nginx and the panel store domain names.
	existing := map[string]bool{}
	cur, err := s.db.Collection(database.ColDomains).Find(ctx, bson.M{}, nil)
	if err == nil {
		var all []bson.M
		if err := cur.All(ctx, &all); err == nil {
			for _, d := range all {
				if dn, _ := d["domain"].(string); dn != "" {
					existing[strings.ToLower(dn)] = true
				}
			}
		}
		cur.Close(ctx)
	}

	// Walk apps + project_services to collect (domain → owner) refs.
	type ref struct{ domain, user string }
	var refs []ref

	if appCur, err := s.db.Collection(database.ColApps).Find(ctx, bson.M{"user": bson.M{"$in": users}}); err == nil {
		var apps []bson.M
		if err := appCur.All(ctx, &apps); err == nil {
			for _, a := range apps {
				d, _ := a["domain"].(string)
				u, _ := a["user"].(string)
				if d != "" && u != "" && !s.isPanelDomain(d) {
					refs = append(refs, ref{d, u})
				}
			}
		}
		appCur.Close(ctx)
	}

	if svcCur, err := s.db.Collection(database.ColProjectServices).Find(ctx, bson.M{"user": bson.M{"$in": users}}); err == nil {
		var svcs []bson.M
		if err := svcCur.All(ctx, &svcs); err == nil {
			for _, sv := range svcs {
				owner, _ := sv["user"].(string)
				if owner == "" {
					continue
				}
				if pd, _ := sv["primary_domain"].(string); pd != "" && !s.isPanelDomain(pd) {
					refs = append(refs, ref{pd, owner})
				}
				if aliases, ok := sv["alias_domains"].(bson.A); ok {
					for _, a := range aliases {
						if as, _ := a.(string); as != "" && !s.isPanelDomain(as) {
							refs = append(refs, ref{as, owner})
						}
					}
				}
			}
		}
		svcCur.Close(ctx)
	}

	if len(refs) == 0 {
		return 0
	}

	// Insert anything not already in the domains collection.
	col := s.db.Collection(database.ColDomains)
	inserted := 0
	now := time.Now()
	for _, r := range refs {
		key := strings.ToLower(r.domain)
		if existing[key] {
			continue
		}
		existing[key] = true // dedupe across multiple refs to the same domain
		_, err := col.InsertOne(ctx, bson.M{
			"domain":      r.domain,
			"user":        r.user,
			"php_version": "8.2",
			"status":      "active",
			"created_at":  now,
			"updated_at":  now,
		})
		if err != nil {
			s.addLog(ctx, jobID, "warn",
				fmt.Sprintf("could not materialize domain %q for %q: %s", r.domain, r.user, err.Error()),
				"panel-records")
			continue
		}
		inserted++
	}
	if inserted > 0 {
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("Materialized %d domain row(s) referenced by apps/services but missing from the domains collection.", inserted),
			"panel-records")
	}
	return inserted
}

// tryStartSyncedApps walks the destination's `apps` collection for every
// app owned by a freshly-transferred linux user and ensures each one
// ends up RUNNING — same state it had on the source.
//
// The /etc/systemd/system/sp-app-<name>.service unit doesn't ride along
// in /home/<user>/, and the runtime caches (node_modules, venv, .gem,
// etc) are deliberately stripped at tar time to keep the wire transfer
// small. So for every imported app we re-run the deploy tail:
//
//  1. Re-install deps via app.InstallCmd as the app user (re-creates
//     node_modules / venv / .gem from package.json / requirements.txt /
//     Gemfile that DID make it across).
//  2. Re-build via app.BuildCmd if present.
//  3. Re-write the systemd unit via agent.CreateSystemdService — it
//     does daemon-reload + enable + restart in one shot.
//
// Per-app outcome stamped back into mongo.status:
//   - "running" — install/build OK, unit up
//   - "failed"  — install/build/start failed (details in transfer log)
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

	var apps []models.App
	if err := cursor.All(ctx, &apps); err != nil {
		return 0
	}

	running := 0
	for i := range apps {
		app := &apps[i]
		if app.Name == "" {
			continue
		}
		if err := s.recoverApp(ctx, jobID, app); err != nil {
			s.addLog(ctx, jobID, "warn",
				fmt.Sprintf("App %q recovery failed: %s", app.Name, err.Error()), "apps")
			col.UpdateOne(ctx, bson.M{"_id": app.ID},
				bson.M{"$set": bson.M{"status": "failed", "updated_at": time.Now()}})
			continue
		}
		col.UpdateOne(ctx, bson.M{"_id": app.ID},
			bson.M{"$set": bson.M{"status": "running", "updated_at": time.Now()}})
		running++
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("App %q recovered and running", app.Name), "apps")
	}
	return running
}

// recoverApp rebuilds an imported app's runtime state on the destination:
// reinstall deps, rebuild, write+start the systemd unit. Mirrors what
// AppService.Deploy does, but trimmed to the post-transfer recovery
// path (no nginx/static/port-wait — those came across as part of
// Transfer Domains & Files).
func (s *TransferService) recoverApp(ctx context.Context, jobID string, app *models.App) error {
	appDir := appInstallDir(app)
	workDir := appWorkDir(app)

	// Make sure the app dir is owned by the app user — file transfer ran
	// as root and may have left files owned by root, which trips
	// `npm install` and friends with EACCES.
	chownRecursive(ctx, appDir, app.User)

	runtimeBinDir := resolveRuntimeBinDir(app.AppType, app.RuntimeVersion)
	runtimeEnv := map[string]string{}
	for k, v := range app.EnvVars {
		runtimeEnv[k] = v
	}
	if runtimeBinDir != "" {
		runtimeEnv["PATH"] = runtimeBinDir + ":/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
	}

	if strings.TrimSpace(app.InstallCmd) != "" {
		if err := runBuildAsUser(ctx, app.User, workDir, app.InstallCmd, runtimeBinDir); err != nil {
			return fmt.Errorf("install: %w", err)
		}
	}
	if strings.TrimSpace(app.BuildCmd) != "" {
		if err := runBuildAsUser(ctx, app.User, workDir, app.BuildCmd, runtimeBinDir); err != nil {
			return fmt.Errorf("build: %w", err)
		}
	}

	startCmd := renderStartCmd(app.StartCmd, app.Port)
	if strings.TrimSpace(startCmd) == "" {
		// Static apps and apps without a start_cmd are served by nginx
		// directly — no systemd unit to write, but we still consider
		// them "recovered" since the file transfer brought everything.
		return nil
	}
	if app.AppType == "node" {
		ecosystem := buildPM2Ecosystem(app.Name, startCmd, workDir, app.Port, app.EnvVars, app.MinInstances, app.MaxInstances)
		if err := writeFileAsUser(ctx, filepath.Join(workDir, "ecosystem.config.js"), ecosystem, app.User, "0644"); err != nil {
			return fmt.Errorf("write ecosystem.config.js: %w", err)
		}
		startCmd = "pm2-runtime start ecosystem.config.js"
		runtimeEnv["PM2_HOME"] = filepath.Join(workDir, ".pm2")
	}
	if err := agent.CreateSystemdService(ctx, app.Name, app.User, workDir, startCmd, runtimeEnv); err != nil {
		return fmt.Errorf("systemd unit: %w", err)
	}
	// Front the running app with an nginx reverse-proxy vhost so HTTP
	// traffic to the app's domain reaches its upstream port. Without
	// this the systemd unit comes up healthy but the world sees nothing
	// because no nginx vhost on the destination matches the domain —
	// the precise "domain not running after migration" symptom.
	if app.Domain != "" && app.Port > 0 {
		if err := agent.CreateReverseProxy(ctx, &agent.VhostConfig{Domain: app.Domain, Port: app.Port}); err != nil {
			return fmt.Errorf("reverse proxy: %w", err)
		}
	}
	return nil
}

// tryStartSyncedProjects mirrors tryStartSyncedApps for project_services
// rows. Without this every Deploy-Software project landed in mongo on
// the destination but the corresponding sp-proj-<slug>-<svc>.service
// systemd unit didn't exist (only the .service files in /etc/systemd/
// don't ride along in /home/<user>/), so the project's primary domain
// served 404 from the panel default vhost.
//
// For each imported project_service: re-install deps, re-build, write
// the systemd unit, write the nginx reverse-proxy vhost, start the
// unit. Skips rows whose status is already "stopped" (operator intent).
func (s *TransferService) tryStartSyncedProjects(ctx context.Context, jobID string, picked map[string]bool) int {
	if len(picked) == 0 {
		return 0
	}
	users := make([]string, 0, len(picked))
	for u := range picked {
		users = append(users, u)
	}
	col := s.db.Collection(database.ColProjectServices)
	cursor, err := col.Find(ctx, bson.M{"user": bson.M{"$in": users}})
	if err != nil {
		return 0
	}
	defer cursor.Close(ctx)

	var svcs []models.ProjectService
	if err := cursor.All(ctx, &svcs); err != nil {
		return 0
	}

	running := 0
	for i := range svcs {
		svc := &svcs[i]
		if svc.Name == "" || svc.SystemdUnit == "" {
			continue
		}
		if svc.Status == "stopped" {
			continue
		}
		if err := s.recoverProjectService(ctx, jobID, svc); err != nil {
			s.addLog(ctx, jobID, "warn",
				fmt.Sprintf("Project service %q recovery failed: %s", svc.Name, err.Error()), "projects")
			col.UpdateOne(ctx, bson.M{"_id": svc.ID},
				bson.M{"$set": bson.M{"status": "failed", "updated_at": time.Now()}})
			continue
		}
		col.UpdateOne(ctx, bson.M{"_id": svc.ID},
			bson.M{"$set": bson.M{"status": "running", "updated_at": time.Now()}})
		running++
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("Project service %q recovered and running", svc.Name), "projects")
	}
	return running
}

// recoverProjectService rebuilds an imported project service's runtime:
// re-install deps, rebuild, write the systemd unit, front it with an
// nginx reverse-proxy vhost on its primary domain. Mirrors what the
// regular Deploy-Software provisioner does, trimmed to the post-transfer
// recovery path. Skips deps/build when the operator has flagged
// MissingEnvKeys (the service won't start without them anyway).
func (s *TransferService) recoverProjectService(ctx context.Context, jobID string, svc *models.ProjectService) error {
	// BuildDir is install_dir + git_subpath (the actual app root, where
	// package.json lives for monorepo projects); fall back to install_dir
	// when no subpath is configured. Without using BuildDir, npm install
	// + npm run build + npm start all run in the parent clone where
	// there's no package.json — install no-ops, build silently fails,
	// start exits, and the systemd unit either never gets written (if an
	// earlier step errors) or starts in the wrong cwd and crashloops.
	workDir := svc.BuildDir
	if workDir == "" {
		workDir = svc.InstallDir
	}
	if workDir == "" {
		return fmt.Errorf("install_dir/build_dir empty — nothing to start")
	}
	chownRecursive(ctx, workDir, svc.User)

	// Map project-service runtime ("node"/"python"/"go"/"ruby") onto the
	// app_type values resolveRuntimeBinDir expects. The framework field
	// alone isn't reliable (operator might pick "nextjs" without
	// runtime_version), so we infer from the framework name.
	runtimeKey := frameworkToRuntimeKey(svc.Framework)
	runtimeBinDir := resolveRuntimeBinDir(runtimeKey, svc.RuntimeVersion)
	runtimeEnv := map[string]string{}
	for k, v := range svc.EnvVars {
		runtimeEnv[k] = v
	}
	if svc.Port > 0 {
		runtimeEnv["PORT"] = fmt.Sprintf("%d", svc.Port)
	}
	if runtimeBinDir != "" {
		runtimeEnv["PATH"] = runtimeBinDir + ":/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
	}

	if strings.TrimSpace(svc.InstallCmd) != "" {
		if err := runBuildAsUser(ctx, svc.User, workDir, svc.InstallCmd, runtimeBinDir); err != nil {
			return fmt.Errorf("install: %w", err)
		}
	}
	if strings.TrimSpace(svc.BuildCmd) != "" {
		if err := runBuildAsUser(ctx, svc.User, workDir, svc.BuildCmd, runtimeBinDir); err != nil {
			return fmt.Errorf("build: %w", err)
		}
	}

	startCmd := svc.StartCmd
	if strings.TrimSpace(startCmd) == "" {
		return nil
	}
	if err := agent.CreateSystemdUnit(ctx, svc.SystemdUnit, svc.User, workDir, startCmd, runtimeEnv); err != nil {
		return fmt.Errorf("systemd unit: %w", err)
	}
	if svc.PrimaryDomain != "" && svc.Port > 0 {
		if err := agent.CreateReverseProxy(ctx, &agent.VhostConfig{Domain: svc.PrimaryDomain, Port: svc.Port}); err != nil {
			return fmt.Errorf("reverse proxy: %w", err)
		}
	}
	return nil
}

// enrichDomainRegistration walks the source's domains collection for
// every picked linux user and merges WHOIS / registration metadata
// onto the matching destination domain rows via $set. Needed because
// the file-transfer step creates a bare destination row first, and
// the panel-records sync uses $setOnInsert which silently no-ops on
// existing rows — so the source's registrar / expires_on / auto_renew
// /nameservers / whois_synced_at never reach the destination without
// this pass.
//
// Only writes fields that have a real value on source (empty string,
// nil date, empty array all skipped) so re-runs over an already-
// enriched destination don't blank out fields the operator may have
// since edited via the WHM UI.
//
// Returns the count of destination domains that received at least
// one field update.
func (s *TransferService) enrichDomainRegistration(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string, picked map[string]bool) int {
	if len(picked) == 0 {
		return 0
	}
	quoted := make([]string, 0, len(picked))
	for u := range picked {
		quoted = append(quoted, fmt.Sprintf("%q", u))
	}
	filter := fmt.Sprintf(`{"user":{"$in":[%s]}}`, strings.Join(quoted, ","))
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColDomains, filter)
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source domains for registration enrich: %s", err), "panel-records")
		return 0
	}

	col := s.db.Collection(database.ColDomains)
	enriched := 0
	for _, raw := range docs {
		domain, _ := raw["domain"].(string)
		if domain == "" || s.isPanelDomain(domain) {
			continue
		}
		set := bson.M{}
		// String fields — skip empties so we don't blank out edits.
		if v, ok := raw["registrar"].(string); ok && v != "" {
			set["registrar"] = v
		}
		if v, ok := raw["whois_raw"].(string); ok && v != "" {
			set["whois_raw"] = v
		}
		// Booleans — always copy (false is a valid intentional value).
		if v, ok := raw["auto_renew"].(bool); ok {
			set["auto_renew"] = v
		}
		// Date fields — mongoexport returns Extended JSON
		// {"$date":"2026-12-10T..."}; setting that literal sub-document
		// stores it as a sub-document, NOT a BSON DateTime, and
		// breaks every subsequent decode of the Domain struct ("error
		// decoding key last_checked_at: cannot decode embedded
		// document into a time.Time"). Run through unwrapEJSON so the
		// driver sees a real time.Time and persists it as BSON Date.
		for _, k := range []string{"registered_on", "expires_on", "whois_synced_at", "last_checked_at"} {
			if v := raw[k]; v != nil {
				if vs, ok := v.(string); ok && vs == "" {
					continue
				}
				unwrapped := unwrapEJSON(v)
				// Skip if unwrapEJSON couldn't convert (e.g. left it
				// as a map). Better to omit than corrupt the row.
				if _, isMap := unwrapped.(map[string]any); isMap {
					continue
				}
				set[k] = unwrapped
			}
		}
		// Nameservers — array of strings; skip empty/nil.
		if v, ok := raw["nameservers"].([]any); ok && len(v) > 0 {
			ns := make([]string, 0, len(v))
			for _, item := range v {
				if str, ok := item.(string); ok && str != "" {
					ns = append(ns, str)
				}
			}
			if len(ns) > 0 {
				set["nameservers"] = ns
			}
		}
		// Preflight result fields the source has populated.
		if v, ok := raw["resolved_ip"].(string); ok && v != "" {
			set["resolved_ip"] = v
		}
		if v, ok := raw["domain_type"].(string); ok && v != "" {
			set["domain_type"] = v
		}
		if v, ok := raw["ip_matches_server"].(bool); ok {
			set["ip_matches_server"] = v
		}

		if len(set) == 0 {
			continue
		}
		set["updated_at"] = time.Now()
		res, uerr := col.UpdateOne(ctx, bson.M{"domain": domain}, bson.M{"$set": set})
		if uerr == nil && res != nil && res.MatchedCount > 0 {
			enriched++
		}
	}
	return enriched
}

// healDisabledVhostSymlinks walks every active domain in mongo and makes
// sure its nginx sites-enabled symlink exists. Triggered when a previous
// flow wrote the vhost file to sites-available but the enable step was
// skipped or rolled back — the domain shows up linked to its project in
// the UI, but nginx returns 404. Rather than hunt every regression path
// that can leave this state (Suspend without Unsuspend, certbot mid-flight
// crash, half-applied SSL upgrade), keep the fix idempotent: for each
// non-suspended domain with an available file and a missing enabled
// symlink, `ln -s` the file in. Validates `nginx -t` once at the end and
// reloads only if config parses. Returns the count of symlinks created.
func (s *TransferService) healDisabledVhostSymlinks(ctx context.Context, jobID string) int {
	col := s.db.Collection(database.ColDomains)
	cursor, err := col.Find(ctx, bson.M{"status": bson.M{"$ne": "suspended"}})
	if err != nil {
		return 0
	}
	defer cursor.Close(ctx)
	var domains []models.Domain
	if err := cursor.All(ctx, &domains); err != nil {
		return 0
	}
	fixed := 0
	for _, d := range domains {
		dom := strings.TrimSpace(d.Domain)
		if dom == "" {
			continue
		}
		availPath := fmt.Sprintf("/etc/nginx/sites-available/%s", dom)
		enabledPath := fmt.Sprintf("/etc/nginx/sites-enabled/%s", dom)
		if _, err := os.Stat(availPath); err != nil {
			continue
		}
		if _, err := os.Lstat(enabledPath); err == nil {
			continue
		}
		if _, err := agent.RunCommand(ctx, "ln", "-sf", availPath, enabledPath); err == nil {
			fixed++
			s.addLog(ctx, jobID, "info",
				fmt.Sprintf("Re-enabled nginx vhost for %s (available existed, symlink missing)", dom),
				"panel-records")
		}
	}
	if fixed > 0 {
		if _, err := agent.RunCommand(ctx, "nginx", "-t"); err != nil {
			s.addLog(ctx, jobID, "warn",
				fmt.Sprintf("nginx -t failed after re-enabling %d vhost(s) — rolling back", fixed),
				"panel-records")
			for _, d := range domains {
				agent.RunCommand(ctx, "rm", "-f", fmt.Sprintf("/etc/nginx/sites-enabled/%s", strings.TrimSpace(d.Domain)))
			}
			return 0
		}
		agent.ReloadNginx(ctx)
	}
	return fixed
}

// repointSourceDNSToDestination rewrites the source pdns's A records
// (any matching the source IP) and SPF TXT records to point at this
// destination's IP. Runs as the final step of the transfer pipeline.
//
// Why this is mandatory for any transfer of a publicly-delegated zone:
// when a zone's NS set spans BOTH the source and destination panels
// (the typical "live cutover" topology — dns1/dns2 on source,
// dns3/dns4 on destination), public resolvers round-robin across the
// four. If source's pdns still answers `cholun.com A 187.127.129.188`
// while destination answers `cholun.com A 187.127.146.169`, every
// other DNS lookup gets the wrong answer and mail/HTTP/SPF flap. The
// SPF case is the loudest — Gmail rejected real mail with
//
//	SPF [admin.cholun.com] with ip: [<new>] = did not pass
//
// because half the SPF lookups returned the source's stale
// `ip4:<old>` record.
//
// Strategy: for each non-panel zone on source, replace every A record
// whose value is the source IP with the destination IP, rewrite every
// SPF TXT to `ip4:<dest> ~all`, bump the SOA serial, and restart
// pdns so any DNS-NOTIFY secondaries pick the new serial up. Also
// deletes any DNS rows that previous shell-level patch attempts may
// have left at doubled-suffix names like `cholun.com.cholun.com`
// (pdnsutil interprets bare `cholun.com` as relative-to-zone and
// double-suffixes; harmless to queries but messy in zone listings).
//
// Idempotent: re-running over an already-repointed source no-ops
// because there are no remaining records matching the source IP.
// Returns the count of zones that received at least one update.
func (s *TransferService) repointSourceDNSToDestination(ctx context.Context, jobID, host string, port int, sshUser, sshPass string) int {
	srcIP := strings.TrimSpace(host)
	dstIP := strings.TrimSpace(s.serverIP)
	if srcIP == "" || dstIP == "" || srcIP == dstIP {
		return 0
	}
	// One shell script does the whole walk on source — fewer SSH round
	// trips and atomic per-zone updates. Skip the panel's own management
	// zone (betazeninfotech.com) so we don't accidentally redirect the
	// panel's own DNS to the destination.
	// pdnsutil quirk: replace-rrset always treats NAME as
	// relative-to-zone, even with a trailing dot. So passing
	// "cholun.com." against zone cholun.com writes a record at
	// "cholun.com.cholun.com." Convert the FQDN to a zone-relative
	// label first (apex → @, sub → leading subdomain). Also delete
	// any stale doubled-suffix names left behind by earlier code
	// versions / ad-hoc patches that didn't know this rule.
	script := fmt.Sprintf(`set -e
SRC_IP=%q
DST_IP=%q
to_relative() {
  # $1=FQDN (with optional trailing dot), $2=zone (no trailing dot).
  local fqdn="${1%%.}"; local zone="$2"
  if [ "$fqdn" = "$zone" ]; then echo "@"; return; fi
  case "$fqdn" in *."$zone") echo "${fqdn%.$zone}";; *) echo "$fqdn";; esac
}
updated=0
for ZONE in $(pdnsutil list-all-zones 2>/dev/null | grep -vE 'betazeninfotech\.com|^$'); do
  changed=0
  # Drop any doubled-suffix junk like cholun.com.cholun.com or
  # admin.cholun.com.cholun.com that earlier broken code paths
  # may have written. Match: zone repeated 2+ times anywhere in name.
  for bad in $(pdnsutil list-zone "$ZONE" 2>/dev/null | awk -v z="$ZONE" '$1 ~ z"\\."z {print $1}' | sort -u); do
    rel=$(to_relative "$bad" "$ZONE")
    pdnsutil delete-rrset "$ZONE" "$rel" A   >/dev/null 2>&1 || true
    pdnsutil delete-rrset "$ZONE" "$rel" TXT >/dev/null 2>&1 || true
    changed=1
  done
  # Rewrite A records still pointing at source IP. Names land in
  # column 1 as FQDN ("admin.cholun.com"); convert to relative
  # before replace-rrset to avoid double-suffix.
  for fqdn in $(pdnsutil list-zone "$ZONE" 2>/dev/null | awk -v ip="$SRC_IP" '$4=="A" && $5==ip {print $1}' | sort -u); do
    rel=$(to_relative "$fqdn" "$ZONE")
    pdnsutil replace-rrset "$ZONE" "$rel" A 3600 "$DST_IP" >/dev/null 2>&1 && changed=1
  done
  # Rewrite SPF TXT lines to authorize destination. Match by v=spf1
  # so DKIM/DMARC TXTs are left alone.
  for fqdn in $(pdnsutil list-zone "$ZONE" 2>/dev/null | awk '$4=="TXT" && /spf1/ {print $1}' | sort -u); do
    rel=$(to_relative "$fqdn" "$ZONE")
    pdnsutil replace-rrset "$ZONE" "$rel" TXT 3600 "\"v=spf1 ip4:$DST_IP ~all\"" >/dev/null 2>&1 && changed=1
  done
  if [ "$changed" = "1" ]; then
    pdnsutil increase-serial "$ZONE" >/dev/null 2>&1 || true
    updated=$((updated+1))
  fi
done
systemctl restart pdns >/dev/null 2>&1 || true
echo "$updated"
`, srcIP, dstIP)
	// SSH defaults to /bin/sh which on Debian/Ubuntu is dash. The script
	// uses bash-specific features (functions, case glob with quoted vars,
	// `local`), so explicitly invoke bash via base64-encoded payload — no
	// quoting issues from embedded $/quotes/newlines surviving the wire.
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	wrapped := fmt.Sprintf("echo %s | base64 -d | bash", encoded)
	r, err := agent.SSHCommand(ctx, host, port, sshUser, sshPass, wrapped)
	if err != nil || r == nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Repoint source DNS failed: %v", err), "panel-records")
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(r.Output), "\n") {
		if n, perr := strconv.Atoi(strings.TrimSpace(line)); perr == nil {
			count = n
		}
	}
	if count > 0 {
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("Source pdns repointed to %s — %d zone(s) updated", dstIP, count), "panel-records")
	}
	return count
}

// syncMaintenanceState mirrors the source server's server-wide
// maintenance flag onto the destination. Reads source mongo's
// server_config/{key:"maintenance"} doc; if value.enabled=true and
// the destination's MaintenanceService is wired, calls EnableServer
// to apply the same nginx changes (move tenant vhosts to .maintenance-
// disabled, drop a maintenance HTML page, reload). If false (or no doc),
// no-ops — the destination stays at its default (not in maintenance).
//
// Idempotent: EnableServer detects an already-enabled state and skips
// the duplicate vhost-move, so re-runs after a partial transfer don't
// double-shuffle anything. Returns 1 if it propagated state (enabled
// applied OR explicit disabled state copied), 0 otherwise.
func (s *TransferService) syncMaintenanceState(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string) int {
	if s.maintSvc == nil {
		return 0
	}
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB,
		database.ColServerConfig, `{"key":"maintenance"}`)
	if err != nil || len(docs) == 0 {
		return 0
	}
	value := docs[0]["value"]
	cfg := &models.MaintenanceConfig{
		Message: "Server is undergoing maintenance. Please try again later.",
	}
	if vm, ok := value.(map[string]any); ok {
		if e, ok := vm["enabled"].(bool); ok {
			cfg.Enabled = e
		}
		if m, ok := vm["message"].(string); ok && m != "" {
			cfg.Message = m
		}
		if h, ok := vm["custom_page_html"].(string); ok {
			cfg.CustomPageHTML = h
		}
		if e, ok := vm["estimated_end"].(string); ok {
			cfg.EstimatedEnd = e
		}
		if ips, ok := vm["allowed_ips"].([]any); ok {
			for _, ip := range ips {
				if s, ok := ip.(string); ok && s != "" {
					cfg.AllowedIPs = append(cfg.AllowedIPs, s)
				}
			}
		}
	}
	if !cfg.Enabled {
		return 0
	}
	if err := s.maintSvc.EnableServer(ctx, cfg); err != nil {
		s.addLog(ctx, jobID, "warn",
			fmt.Sprintf("Mirror source maintenance state failed: %s", err.Error()), "panel-records")
		return 0
	}
	s.addLog(ctx, jobID, "info",
		"Source was in maintenance — destination put into maintenance to match", "panel-records")
	return 1
}

// healMissingVhosts is the post-transfer safety net. For every domain
// owned by a freshly-imported linux user, it checks whether an nginx
// vhost actually exists on disk and creates one if missing. Two paths:
//
//   - Domain backs an app (mongo `apps` row) or project_service
//     (mongo `project_services` row matching primary_domain) — write
//     a reverse-proxy vhost pointing at that upstream port.
//   - Otherwise — write a PHP-FPM vhost rooted at
//     /home/<user>/domains/<d>/public_html (mirrors agent.CreateVhost).
//
// Catches the case where Transfer Domains & Files's per-domain wiring
// silently failed (nginx -t race on a sibling reload, cleanupVhostFiles
// removing more than intended, etc.) and the operator would otherwise
// land on a 404 served by the panel's catch-all default vhost.
//
// Returns the count of vhosts written.
//
// Scans EVERY domain in the destination mongo — not just domains owned
// by the picked linux users. The wizard's linux-user selection only
// gates which source rows get pulled; once a domain row lands on the
// destination (for any reason — a picked user's data, an addon
// ownedDomains cascade from expandLinuxUserSelection, a project_service
// primary_domain, etc.), the nginx side must have a matching vhost.
// Scoping the heal to picked users meant domains owned by OTHER
// vendors (e.g. project_services whose user is "easycrm4u" while the
// wizard picked only "jagoanandadhara") silently went without vhosts
// and timed out from the browser.
func (s *TransferService) healMissingVhosts(ctx context.Context, jobID string, picked map[string]bool) int {
	cur, err := s.db.Collection(database.ColDomains).Find(ctx, bson.M{})
	if err != nil {
		return 0
	}
	defer cur.Close(ctx)
	var domains []models.Domain
	if err := cur.All(ctx, &domains); err != nil {
		return 0
	}

	healed := 0
	for i := range domains {
		d := &domains[i]
		if d.Domain == "" {
			continue
		}
		// Already has a vhost? Skip.
		if _, statErr := os.Stat(filepath.Join("/etc/nginx/sites-enabled", d.Domain)); statErr == nil {
			continue
		}

		// Does an app or project_service back this domain?
		var app models.App
		appErr := s.db.Collection(database.ColApps).FindOne(ctx, bson.M{"domain": d.Domain}).Decode(&app)
		var svc models.ProjectService
		svcErr := s.db.Collection(database.ColProjectServices).FindOne(ctx, bson.M{"primary_domain": d.Domain}).Decode(&svc)

		if appErr == nil && app.Port > 0 {
			if e := agent.CreateReverseProxy(ctx, &agent.VhostConfig{Domain: d.Domain, Port: app.Port}); e == nil {
				healed++
				s.addLog(ctx, jobID, "info",
					fmt.Sprintf("Healed missing vhost for app domain %s → :%d", d.Domain, app.Port), "vhost-heal")
			}
			continue
		}
		if svcErr == nil && svc.Port > 0 {
			if e := agent.CreateReverseProxy(ctx, &agent.VhostConfig{Domain: d.Domain, Port: svc.Port}); e == nil {
				healed++
				s.addLog(ctx, jobID, "info",
					fmt.Sprintf("Healed missing vhost for project domain %s → :%d", d.Domain, svc.Port), "vhost-heal")
			}
			continue
		}

		// Plain PHP/static domain. Mirror agent.CreateVhost.
		php := d.PHPVersion
		if php == "" {
			php = "8.2"
		}
		if e := agent.CreateVhost(ctx, &agent.VhostConfig{
			Domain:     d.Domain,
			User:       d.User,
			PHPVersion: php,
		}); e == nil {
			healed++
			s.addLog(ctx, jobID, "info",
				fmt.Sprintf("Healed missing PHP vhost for %s", d.Domain), "vhost-heal")
		}
	}
	return healed
}

// frameworkToRuntimeKey maps a Deploy-Software framework name onto the
// runtime key resolveRuntimeBinDir expects. Falls back to the framework
// name verbatim so unknown ones still resolve via the default lookup.
func frameworkToRuntimeKey(framework string) string {
	switch strings.ToLower(framework) {
	case "nextjs", "express", "node", "nodejs", "nest", "fastify":
		return "nodejs"
	case "go-vanilla", "go-fiber", "go-gin", "go-chi", "go-echo", "go":
		return "go"
	case "python-flask", "python-django", "python-fastapi", "python":
		return "python"
	case "ruby", "ruby-sinatra", "ruby-rails":
		return "ruby"
	}
	return framework
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
