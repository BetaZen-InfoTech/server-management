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
	"go.mongodb.org/mongo-driver/mongo/options"
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

	// --- Pass 0: mirror the panel's own user roster from source.
	//
	// A transfer isn't just the hosting workload — the operator expects
	// the destination's /whm/users page to look like the source's when
	// the dust settles. Previously we only synced the linux users whose
	// /home/ tree we copied, which meant:
	//
	//   - the destination's platform-owner credential still used the
	//     install.sh default (admin@betazeninfotech.com / admin123);
	//   - every team member / customer whose site wasn't in the transfer
	//     set was invisible post-migration; and
	//   - stale accounts left over from the destination's previous life
	//     stayed logged-in-able even though the hosting workload was
	//     gone.
	//
	// mirrorPanelUsers fixes all three: the super-admin's email/name/
	// password/permissions are upgraded in-place to match source, every
	// non-super-admin destination row is removed, then source's entire
	// non-super-admin user roster is re-seeded. The returned idMap covers
	// every migrated user so downstream passes can remap user_id refs.
	srcDB := "serverpanel"
	mirrorIDMap, mirroredEmails := s.mirrorPanelUsers(ctx, jobID, host, port, sshUser, sshPass, srcDB)

	// --- Pass 1: users / vendors. Builds the userID translation map.
	idMap, vendorEmails, ownedDomains := s.syncUsersForTransfer(ctx, jobID, host, port, sshUser, sshPass, srcDB, picked)
	// Merge the full-roster mapping on top so collections that reference
	// users we didn't pick (e.g. a team member with no linux /home tree)
	// still land on the right account.
	for src, dst := range mirrorIDMap {
		if _, ok := idMap[src]; !ok {
			idMap[src] = dst
		}
	}
	if len(mirroredEmails) > 0 {
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("Panel roster mirrored from source: %d account(s) upgraded/re-seeded.", len(mirroredEmails)),
			"panel-records")
	}
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

	// db_access_hosts — per-database remote-IP allowlist. Without this
	// sync the destination's Database page shows zero allowed hosts even
	// though the source had several configured, and any external app
	// connecting from an allowlisted IP gets MySQL's "Host not allowed
	// to connect" on the destination. The transfer-databases step's
	// recreateAccessHostGrants reads these rows post-restore and
	// re-issues each MySQL GRANT.
	//
	// Strategy: walk the destination's just-synced `databases` rows,
	// look up the source's matching row by db_name to find the SOURCE
	// database_id, fetch the source's db_access_hosts filtered on that
	// source ObjectID, and re-insert with the destination's _id.
	stats["db_access_hosts"] = s.syncDBAccessHosts(ctx, jobID, host, port, sshUser, sshPass, srcDB)

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

	// Postfix-side rehydrate. The Mongo sync above carries the
	// forwarder ROWS but does NOT touch /etc/postfix/virtual_alias_maps,
	// which is the file Postfix actually reads when routing inbound
	// mail. Pre-3.1.37 a server transfer that imported 50 forwarders
	// silently left mail to every one of them dead-lettering, because
	// destination Postfix had no map entry for any of them. The
	// operator only noticed when a customer reported "I'm not getting
	// forwarded mail anymore" days later.
	//
	// RebuildVirtualAliasMaps re-emits the file from scratch using
	// every forwarder row currently in the destination's Mongo, runs
	// postmap, and reloads Postfix once. Idempotent — running it
	// twice produces the same file. Logs the count to the transfer
	// job so the operator sees "rebuilt N forwarder map entries" in
	// the recovery summary alongside vhosts_healed and apps_restarted.
	if s.emailSvc == nil {
		// Optional dep — main.go wires it but a slim test harness or a
		// future split build might not. Soft-warn so the operator
		// knows to run the heal manually.
		s.addLog(ctx, jobID, "warn",
			"forwarder Postfix rehydrate skipped — EmailService not wired into TransferService; run `bzpanel heal-forwarders` (or POST /api/v1/whm/email/forwarders/rehydrate) on the destination to wire Postfix.",
			"panel-records")
	} else if rebuilt, err := s.emailSvc.RebuildVirtualAliasMaps(ctx); err != nil {
		s.addLog(ctx, jobID, "warn",
			"forwarder Postfix rehydrate failed — Mongo rows landed but mail won't route until you run `bzpanel heal-forwarders` or POST /api/v1/whm/email/forwarders/rehydrate. Reason: "+err.Error(),
			"panel-records")
	} else {
		stats["forwarder_postfix_rebuilt"] = rebuilt
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("forwarder Postfix rehydrate ok — %d forwarder map entries written to /etc/postfix/virtual_alias_maps", rebuilt),
			"panel-records")
	}

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

	// Upgrade HTTP-only vhosts to SSL whenever a Let's Encrypt cert is
	// already on disk for the domain. Catches the race where a
	// recovery path (recoverApp / recoverProjectService) wrote an
	// HTTP-only vhost AFTER the Transfer SSL step had produced the
	// SSL vhost — the HTTP-only version wins, the :443 block is lost,
	// and HTTPS traffic for the domain lands on whichever other vhost
	// holds `listen 443 ssl`. Defensive and idempotent: skip if the
	// vhost already has a listen-443 line or no cert is present.
	stats["vhosts_ssl_upgraded"] = s.healMissingSSLBlocks(ctx, jobID)

	// Maintenance state — preserve source's server-wide maintenance flag
	// on the destination. The expectation here mirrors the rest of the
	// transfer: if the operator put the source into maintenance, the
	// destination must come up the same way so DNS cutover doesn't
	// surface the new server in a broken state. Idempotent: writes the
	// destination's server_config doc and, if enabled, calls the local
	// MaintenanceService.EnableServer to apply the nginx changes.
	stats["maintenance_state"] = s.syncMaintenanceState(ctx, jobID, host, port, sshUser, sshPass, srcDB)

	// Server settings — timezone, contact email, demo-credential toggles,
	// branding (panel name + logo + favicon), home page, panel mail
	// SMTP relay. Previously the "Transfer Server Config" step was a
	// no-op — operators had to re-set every server-level option by hand
	// after a transfer. This sync mirrors the operator-meaningful subset
	// of server_config from source to destination, idempotently. Excludes
	// the destination's local-machine state (panel_domain / server_ip /
	// nginx / php / mongodb / mysql) since those describe the box the
	// panel is RUNNING on, not the operator's product preferences.
	stats["server_settings"] = s.syncServerSettings(ctx, jobID, host, port, sshUser, sshPass, srcDB)

	// Developer-surface assets — API tokens and outbound webhook
	// endpoints. Tenant-keyed (tenant_id is the user_id of the tenant
	// root), translated through idMap by normaliseDoc. Webhook
	// signing secrets are AES-GCM encrypted under the SOURCE's
	// APP_ENCRYPTION_KEY which doesn't exist on the destination, so the
	// dest panel marks each migrated webhook inactive and surfaces a
	// "rotate to activate" CTA — the operator can rotate to mint a
	// fresh secret without losing the URL / event subscriptions /
	// description. Delivery logs are intentionally skipped: they're
	// short-lived attempt records, not config worth migrating.
	stats["api_tokens"] = s.syncAPITokens(ctx, jobID, host, port, sshUser, sshPass, srcDB, idMap)
	stats["webhook_endpoints"] = s.syncWebhookEndpoints(ctx, jobID, host, port, sshUser, sshPass, srcDB, idMap)
	// Legacy platform-notification webhooks (admin/webhooks routes,
	// pre-dating the per-tenant webhook_endpoints surface). Plaintext
	// HMAC secrets so they survive an APP_ENCRYPTION_KEY change without
	// re-encryption — just need to be carried across so the operator's
	// Slack / on-call URL keeps receiving alerts post-cutover.
	stats["webhooks_legacy"] = s.syncLegacyNotificationWebhooks(ctx, jobID, host, port, sshUser, sshPass, srcDB)
	stats["notification_settings"] = s.syncNotificationSettings(ctx, jobID, host, port, sshUser, sshPass, srcDB)

	// Hot-reload the destination's in-memory mailer so password resets
	// and notifications use the freshly-mirrored SMTP config without
	// requiring a panel restart. No-op when PanelMailService isn't
	// wired (older boot order) or no panel_mail doc exists.
	if s.panelMailSvc != nil {
		s.panelMailSvc.ReloadFromDB(ctx)
	}

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
		// Strip session / reset / lockout state so a migrated user can't
		// resume an already-active session on the destination using a
		// refresh token that was minted with the SOURCE's JWT_SECRET.
		// The bcrypt password hash carries over (it's keyless), so the
		// operator can log in normally — they just can't ride a stale
		// refresh token from the source. Mirrors the wipe the owner row
		// gets in mirrorPanelUsers.
		for _, k := range []string{
			"refresh_token", "refresh_expires_at",
			"failed_logins", "locked_until",
			"reset_token_hash", "reset_expires_at", "reset_requested_at",
		} {
			delete(insert, k)
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

// mirrorPanelUsers takes over the destination's user roster so it matches
// the source panel's.  Three effects, in order:
//
//  1. Read source's users collection in full (no username filter —
//     panel-team members without a linux home tree must come over too).
//  2. Find the source's super-admin (role=vendor_owner). If it exists,
//     UPDATE the destination's super-admin in place with source's
//     email / name / password_hash / permissions / is_super_admin bit.
//     The destination's _id doesn't change — that preserves any
//     downstream refs the destination's own non-mirrored rows might
//     still hold. Login with source-side credentials starts working
//     immediately.
//  3. DELETE every non-super-admin row on destination (vendor_admin,
//     vendor_staff, developer, support, customer). Then INSERT every
//     non-super-admin source user as-is (keeping source's password
//     hashes — operators keep their existing passwords post-transfer).
//
// Returns:
//   - idMap: source ObjectID hex → destination ObjectID. Needed by
//     downstream remaps (apps, domains, etc.) so they point at the
//     new destination user rows. The super-admin is in here too: its
//     source _id maps to the destination super-admin's (pre-existing)
//     _id so rows that referenced the source owner end up owned by the
//     destination owner.
//   - emails: the list of accounts mirrored, for the operator log.
//
// Safety: if the source panel can't be read (network blip, missing
// mongosh, etc.), this pass bails with a warn log and a nil map. The
// destination's roster is NOT touched in that case — better to land in
// a degraded state with stale users than to wipe the destination's
// auth table on a transient failure.
func (s *TransferService) mirrorPanelUsers(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string) (map[string]primitive.ObjectID, []string) {
	idMap := map[string]primitive.ObjectID{}
	emails := []string{}

	// Read every user row on source, no filter — we want the whole roster.
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColUsers, "{}")
	if err != nil {
		s.addLog(ctx, jobID, "warn",
			fmt.Sprintf("Could not read source users for roster mirror: %s — destination users left as-is.", err),
			"panel-records")
		return idMap, emails
	}
	if len(docs) == 0 {
		s.addLog(ctx, jobID, "warn",
			"Source users collection is empty — destination users left as-is.",
			"panel-records")
		return idMap, emails
	}

	col := s.db.Collection(database.ColUsers)

	// Step 1 — locate source super-admin + upgrade destination super-admin.
	var srcOwner map[string]any
	for _, d := range docs {
		role, _ := d["role"].(string)
		if role == "vendor_owner" {
			srcOwner = d
			break
		}
	}
	if srcOwner != nil {
		srcOwnerOID := extractOID(srcOwner["_id"])
		var dstOwner bson.M
		if err := col.FindOne(ctx, bson.M{"role": "vendor_owner"}).Decode(&dstOwner); err == nil {
			dstOID, _ := dstOwner["_id"].(primitive.ObjectID)
			set := bson.M{"updated_at": time.Now()}
			for _, k := range []string{"name", "email", "username", "password", "permissions", "is_super_admin", "two_factor_enabled", "two_factor_secret", "recovery_codes", "recovery_email"} {
				if v, ok := srcOwner[k]; ok && v != nil {
					set[k] = v
				}
			}
			// Clear any stale session tokens so the browser is forced through
			// a fresh login against the new credentials.
			if _, err := col.UpdateByID(ctx, dstOID, bson.M{
				"$set": set,
				"$unset": bson.M{
					"refresh_token":      "",
					"refresh_expires_at": "",
					"failed_logins":      "",
					"locked_until":       "",
					"reset_token_hash":   "",
					"reset_expires_at":   "",
					"reset_requested_at": "",
				},
			}); err != nil {
				s.addLog(ctx, jobID, "warn",
					fmt.Sprintf("Could not upgrade destination super-admin: %s", err),
					"panel-records")
			} else {
				if srcOwnerOID != "" {
					idMap[srcOwnerOID] = dstOID
				}
				email, _ := srcOwner["email"].(string)
				if email != "" {
					emails = append(emails, email+" (owner-upgraded)")
				}
			}
		} else if err == mongo.ErrNoDocuments {
			s.addLog(ctx, jobID, "warn",
				"Destination has no vendor_owner — skipping owner upgrade.",
				"panel-records")
		}
	}

	// Step 2 — wipe non-super-admin destination users. The super-admin row
	// was just upgraded in place above, so it's preserved regardless.
	if res, err := col.DeleteMany(ctx, bson.M{"role": bson.M{"$ne": "vendor_owner"}}); err != nil {
		s.addLog(ctx, jobID, "warn",
			fmt.Sprintf("Could not clear destination non-owner users: %s", err),
			"panel-records")
	} else if res.DeletedCount > 0 {
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("Cleared %d non-owner user(s) on destination to mirror source roster.", res.DeletedCount),
			"panel-records")
	}

	// Step 3 — insert every non-owner source user.
	for _, d := range docs {
		role, _ := d["role"].(string)
		if role == "vendor_owner" {
			continue // handled above
		}
		email, _ := d["email"].(string)
		if email == "" {
			continue
		}
		oldID := extractOID(d["_id"])
		newOID := primitive.NewObjectID()
		insert := s.normaliseDoc(d, idMap)
		insert["_id"] = newOID
		// tenant_id may point at the source super-admin (a vendor_admin
		// under an owner). Remap through idMap when we can — if the ref
		// isn't in the map yet (tenant-root rows insert themselves before
		// their team members do), fall back to the new _id for self-refs.
		if tid, ok := insert["tenant_id"]; ok {
			if oid, ok := tid.(primitive.ObjectID); ok {
				if mapped, ok := idMap[oid.Hex()]; ok {
					insert["tenant_id"] = mapped
				}
			}
		}
		if _, hasT := insert["tenant_id"]; !hasT {
			insert["tenant_id"] = newOID
		}
		if _, err := col.InsertOne(ctx, insert); err != nil {
			s.addLog(ctx, jobID, "warn",
				fmt.Sprintf("Could not insert mirrored user %s: %s", email, err),
				"panel-records")
			continue
		}
		if oldID != "" {
			idMap[oldID] = newOID
		}
		emails = append(emails, email)
	}
	return idMap, emails
}

// syncDBAccessHosts copies per-database remote-IP allowlist rows
// (db_access_hosts) from the source to the destination. The source's
// rows reference databases via the SOURCE's ObjectIDs; the destination
// has its own ObjectIDs after the databases sync. Strategy:
//
//  1. Pull the SOURCE databases collection so we can map (db_name, type)
//     → source ObjectID.
//  2. For each destination database row, look up the matching source
//     row by (db_name, type) and remember its source ObjectID.
//  3. For every source ObjectID we collected, RemoteMongoExport the
//     source's db_access_hosts filtered on database_id = that source
//     ObjectID, then re-insert each row into the destination with the
//     destination's database_id and a fresh _id.
//
// Dedup is by (database_id, host) so a re-run doesn't pile duplicates.
// Returns the count of newly-inserted rows.
func (s *TransferService) syncDBAccessHosts(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string) int {
	// 1. Source databases (need every row that belongs to the picked
	// users — but the panel-records sync already filtered upstream
	// when copying `databases` to the destination, so we can short-
	// circuit by reading the destination instead and joining via
	// db_name + type.)
	dstDBs := []models.Database{}
	cur, err := s.db.Collection(database.ColDatabases).Find(ctx, bson.M{})
	if err != nil {
		return 0
	}
	if err := cur.All(ctx, &dstDBs); err != nil {
		cur.Close(ctx)
		return 0
	}
	cur.Close(ctx)
	if len(dstDBs) == 0 {
		return 0
	}

	// 2. Pull source's databases — same filter contract as
	// syncSimpleByUser but we don't have `picked` here, so widen to
	// every row whose db_name matches one we just inserted on the
	// destination.
	dbNameSet := map[string]models.Database{}
	for _, d := range dstDBs {
		dbNameSet[d.DBName+"|"+d.Type] = d
	}
	quoted := make([]string, 0, len(dstDBs))
	for _, d := range dstDBs {
		quoted = append(quoted, fmt.Sprintf("%q", d.DBName))
	}
	filter := fmt.Sprintf(`{"db_name":{"$in":[%s]}}`, strings.Join(quoted, ","))
	srcRows, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColDatabases, filter)
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source databases for db_access_hosts: %s", err), "panel-records")
		return 0
	}
	// Map: source ObjectID hex → destination database row
	srcIDToDst := map[string]models.Database{}
	for _, raw := range srcRows {
		dbName, _ := raw["db_name"].(string)
		dbType, _ := raw["type"].(string)
		if dst, ok := dbNameSet[dbName+"|"+dbType]; ok {
			oid := extractOID(raw["_id"])
			if oid != "" {
				srcIDToDst[oid] = dst
			}
		}
	}
	if len(srcIDToDst) == 0 {
		return 0
	}

	// 3. Pull source's db_access_hosts for every source-DB ObjectID we
	// found, then re-insert with the destination's database_id.
	srcIDList := make([]string, 0, len(srcIDToDst))
	for sid := range srcIDToDst {
		srcIDList = append(srcIDList, fmt.Sprintf(`{"$oid":%q}`, sid))
	}
	hostFilter := fmt.Sprintf(`{"database_id":{"$in":[%s]}}`, strings.Join(srcIDList, ","))
	hostDocs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColDBAccessHosts, hostFilter)
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source db_access_hosts: %s", err), "panel-records")
		return 0
	}

	col := s.db.Collection(database.ColDBAccessHosts)
	inserted := 0
	for _, raw := range hostDocs {
		srcDBOID := extractOID(raw["database_id"])
		dst, ok := srcIDToDst[srcDBOID]
		if !ok {
			continue
		}
		hostVal, _ := raw["host"].(string)
		if strings.TrimSpace(hostVal) == "" {
			continue
		}

		// Dedup on the destination side by (database_id, host).
		var existing bson.M
		err := col.FindOne(ctx, bson.M{"database_id": dst.ID, "host": hostVal}).Decode(&existing)
		if err == nil {
			continue
		}

		// Re-insert with the destination's database_id + a fresh _id.
		comment, _ := raw["comment"].(string)
		now := time.Now()
		if _, err := col.InsertOne(ctx, models.DBAccessHost{
			ID:         primitive.NewObjectID(),
			DatabaseID: dst.ID,
			Host:       hostVal,
			Comment:    comment,
			CreatedAt:  now,
		}); err != nil {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("insert db_access_host (%s, %s) failed: %s", dst.DBName, hostVal, err), "panel-records")
			continue
		}
		inserted++
	}
	return inserted
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

	// Lazy-install the runtime this app needs if the Transfer Software
	// step missed it (selection didn't include `software`, or the source
	// version wasn't under `/usr/local/n/versions/node/` / `/etc/php/`).
	// Install is idempotent: InstallNodeJS / InstallPHP no-op when the
	// version is already present.
	ensureRuntimeForApp(ctx, app.AppType, app.RuntimeVersion)

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
	//
	// If a Let's Encrypt cert is already on disk for this domain (either
	// brought across by the Transfer SSL step or issued earlier), use
	// the SSL template so the :443 block is preserved. Plain
	// CreateReverseProxy writes HTTP-only, which CLOBBERS the SSL block
	// the earlier SSL step created — the destination then has a :443
	// cert on disk but no vhost claims the listener, so HTTPS requests
	// fall through to whichever other vhost happens to hold `listen 443`
	// and visitors see a stranger's placeholder ("Welcome to your new
	// website!"). Exactly the "domain not linked to Deploy Software"
	// shape of the bug reports.
	if app.Domain != "" && app.Port > 0 {
		if agent.LetsEncryptCertExists(app.Domain) {
			if err := agent.CreateReverseProxyWithSSL(ctx, &agent.VhostConfig{Domain: app.Domain, Port: app.Port}); err != nil {
				return fmt.Errorf("reverse proxy (SSL): %w", err)
			}
		} else {
			if err := agent.CreateReverseProxy(ctx, &agent.VhostConfig{Domain: app.Domain, Port: app.Port}); err != nil {
				return fmt.Errorf("reverse proxy: %w", err)
			}
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
	// Lazy-install the runtime version if Transfer Software missed it.
	// Idempotent — no-ops when the version is already present.
	ensureRuntimeForApp(ctx, runtimeKey, svc.RuntimeVersion)
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
	// Rebuild the nginx vhost using the multi-domain spec builder so
	// alias_domains (copied by syncProjectServices but previously
	// ignored here) make it into the server_name list AND the SAN
	// cert. Without this, a service transferred with
	//   primary=a.com, aliases=[b.com, c.com]
	// would survive in Mongo but land as single-domain nginx, leaving
	// b.com/c.com 404 or routed to a stranger's vhost — the same
	// silent-drop bug a cPanel transfer would have shown when importing
	// an Addon Domain.
	//
	// The two-phase write (HTTP vhost → certbot → SSL vhost) mirrors
	// project_helpers.go:reconcileVhostFor exactly: the first write
	// serves /.well-known/acme-challenge/ over :80 so certbot's webroot
	// probe succeeds, then the cert lets us re-emit with listen 443 ssl.
	if svc.PrimaryDomain != "" && svc.Port > 0 {
		spec := buildRecoveryVhostSpec(svc)
		hadCert := agent.LetsEncryptCertExists(svc.PrimaryDomain)
		spec.UseSSL = hadCert
		if err := agent.CreateProjectVhost(ctx, spec); err != nil {
			return fmt.Errorf("reverse proxy: %w", err)
		}
		// Request (or --expand) a SAN cert covering primary + every
		// alias. Idempotent on the already-covered case. When certbot
		// fails (DNS not yet propagated, rate-limited, etc.) we leave
		// the HTTP vhost in place — better than rolling back to the
		// old single-domain config and dropping aliases entirely.
		email := "admin@" + svc.PrimaryDomain
		if err := agent.IssueLetsEncryptMulti(ctx, svc.PrimaryDomain, svc.AliasDomains, email); err == nil {
			spec.UseSSL = true
			if err := agent.CreateProjectVhost(ctx, spec); err != nil {
				return fmt.Errorf("reverse proxy (SSL): %w", err)
			}
		} else {
			s.addLog(ctx, jobID, "warn",
				fmt.Sprintf("certbot failed for %s (+%d aliases): %v — vhost left on HTTP-only",
					svc.PrimaryDomain, len(svc.AliasDomains), err),
				"panel-records")
		}
	}
	return nil
}

// buildRecoveryVhostSpec assembles a ProjectVhostSpec matching the
// service's runtime shape so recovery and heal paths rebuild nginx
// the way the original Deploy Software create would have. Trimmed
// down from buildMergedVhostSpec (project_helpers.go) — recovery runs
// AFTER the DB copy so any siblings sharing the same primary domain
// are already in the destination's project_services collection and
// the next sibling's own recovery pass will union its locations in.
// No sibling merge here would risk dropping an existing sibling's
// location block; instead we rely on the per-service reconcile that
// every service's recovery step triggers.
func buildRecoveryVhostSpec(svc *models.ProjectService) *agent.ProjectVhostSpec {
	spec := &agent.ProjectVhostSpec{
		PrimaryDomain: svc.PrimaryDomain,
		Aliases:       append([]string(nil), svc.AliasDomains...), // copy so caller can't mutate
	}
	switch svc.Role {
	case "frontend", "static":
		if svc.BuildDir != "" {
			spec.Root = svc.BuildDir
		} else {
			spec.Root = svc.InstallDir
		}
	case "backend":
		prefix := svc.PathPrefix
		if prefix == "" {
			prefix = "/"
		}
		spec.Proxies = append(spec.Proxies, agent.ProjectProxyLoc{Prefix: prefix, Port: svc.Port})
	case "fullstack":
		// Fullstack needs BOTH a static root AND an API proxy. Follow the
		// same defaulting buildMergedVhostSpec uses: path_prefix defaults
		// to /api when the operator didn't pick one.
		if svc.BuildDir != "" {
			spec.Root = svc.BuildDir
		} else {
			spec.Root = svc.InstallDir
		}
		prefix := svc.PathPrefix
		if prefix == "" {
			prefix = "/api"
		}
		spec.Proxies = append(spec.Proxies, agent.ProjectProxyLoc{Prefix: prefix, Port: svc.Port})
	default:
		// worker / unknown roles — no HTTP surface. Give the spec a
		// default "/" proxy to the service's port so CreateProjectVhost
		// still has something to emit (the validator requires root or
		// at least one proxy).
		spec.Proxies = append(spec.Proxies, agent.ProjectProxyLoc{Prefix: "/", Port: svc.Port})
	}
	return spec
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

// healMissingSSLBlocks walks every domain that has a Let's Encrypt cert
// on disk and checks its nginx vhost file. If the vhost is HTTP-only
// (no listen-443 line), rewrite it with the SSL template so HTTPS
// requests for this domain land on the right upstream instead of
// whichever other vhost happens to hold `listen 443 ssl` on the box.
//
// The shape of the bug this defends against: a transfer's SSL step
// upgrades the vhost to SSL, then a later Sync Panel Records step
// (recoverApp / recoverProjectService / healMissingVhosts before the
// ssl-aware fix) rewrites the vhost as HTTP-only, wiping the :443
// block. Destination ends up with a cert on disk, no :443 server_name
// match for the domain, and visitors hitting HTTPS see a stranger's
// content ("Welcome to your new website!" from whichever fallback
// vhost nginx picked). Idempotent: skip if the file already has
// listen-443, or there's no cert, or no vhost file.
func (s *TransferService) healMissingSSLBlocks(ctx context.Context, jobID string) int {
	cur, err := s.db.Collection(database.ColDomains).Find(ctx, bson.M{})
	if err != nil {
		return 0
	}
	defer cur.Close(ctx)
	var domains []models.Domain
	if err := cur.All(ctx, &domains); err != nil {
		return 0
	}
	upgraded := 0
	for _, d := range domains {
		dom := strings.TrimSpace(d.Domain)
		if dom == "" || !agent.LetsEncryptCertExists(dom) {
			continue
		}
		availPath := fmt.Sprintf("/etc/nginx/sites-available/%s", dom)
		body, err := os.ReadFile(availPath)
		if err != nil {
			continue
		}
		if strings.Contains(string(body), "listen 443") || strings.Contains(string(body), "listen [::]:443") {
			continue
		}
		// Figure out upstream port: app row → project_service row → skip.
		var app models.App
		appErr := s.db.Collection(database.ColApps).FindOne(ctx, bson.M{"domain": dom}).Decode(&app)
		if appErr == nil && app.Port > 0 {
			if e := agent.CreateReverseProxyWithSSL(ctx, &agent.VhostConfig{Domain: dom, Port: app.Port}); e == nil {
				upgraded++
				s.addLog(ctx, jobID, "info",
					fmt.Sprintf("Upgraded app vhost %s to SSL (cert on disk but :443 block was missing)", dom), "vhost-heal")
			}
			continue
		}
		var svc models.ProjectService
		svcErr := s.db.Collection(database.ColProjectServices).FindOne(ctx, bson.M{"primary_domain": dom}).Decode(&svc)
		if svcErr == nil && svc.Port > 0 {
			// Re-emit with the multi-domain spec so server_name lists
			// primary + every alias. Falling back to single-domain here
			// would silently drop aliases on the upgrade-to-SSL path.
			spec := buildRecoveryVhostSpec(&svc)
			spec.UseSSL = true
			if e := agent.CreateProjectVhost(ctx, spec); e == nil {
				upgraded++
				s.addLog(ctx, jobID, "info",
					fmt.Sprintf("Upgraded project vhost %s (+%d aliases) to SSL (cert on disk but :443 block was missing)", dom, len(svc.AliasDomains)), "vhost-heal")
			}
			continue
		}
		// Plain PHP-FPM domain with cert — write the PHP-FPM SSL vhost.
		if e := agent.CreateVhostWithSSL(ctx, &agent.VhostConfig{
			Domain:     dom,
			User:       d.User,
			PHPVersion: d.PHPVersion,
		}); e == nil {
			upgraded++
			s.addLog(ctx, jobID, "info",
				fmt.Sprintf("Upgraded PHP vhost %s to SSL (cert on disk but :443 block was missing)", dom), "vhost-heal")
		}
	}
	return upgraded
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
  case "$fqdn" in *."$zone") echo "${fqdn%%.$zone}";; *) echo "$fqdn";; esac
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
  # Rewrite A records still pointing at source IP. Per-VALUE swap so
  # third-party A values that share the same name (multi-value rrset:
  # ns A SRC_IP plus ns A some.other.ip for redundancy) are PRESERVED.
  # We only flip the value that matches SRC_IP, not the whole rrset.
  # Earlier code blindly called replace-rrset with one value, which
  # wiped third-party siblings.
  for fqdn in $(pdnsutil list-zone "$ZONE" 2>/dev/null | awk -v ip="$SRC_IP" '$4=="A" && $5==ip {print $1}' | sort -u); do
    rel=$(to_relative "$fqdn" "$ZONE")
    # Build the new value list: every existing A value, with SRC_IP
    # rewritten to DST_IP (preserves order and any other values).
    new_values=$(pdnsutil list-zone "$ZONE" 2>/dev/null \
      | awk -v fqdn="$fqdn" -v src="$SRC_IP" -v dst="$DST_IP" \
        '$1==fqdn && $4=="A" { v=$5; if (v==src) v=dst; print v }' \
      | sort -u | xargs)
    if [ -n "$new_values" ]; then
      ttl=$(pdnsutil list-zone "$ZONE" 2>/dev/null \
        | awk -v fqdn="$fqdn" '$1==fqdn && $4=="A" {print $2; exit}')
      [ -z "$ttl" ] && ttl=3600
      pdnsutil replace-rrset "$ZONE" "$rel" A "$ttl" $new_values >/dev/null 2>&1 && changed=1
    fi
  done
  # Rewrite SPF TXT lines to authorize destination. Per-VALUE swap so
  # any non-SPF TXT records sharing the same name (e.g. Google site
  # verification at the apex, Atlassian, Facebook ownership tokens —
  # all of these legitimately co-exist with SPF on @) are PRESERVED.
  # Only the v=spf1 entries' ip4 are rewritten; everything else is
  # passed through verbatim.
  for fqdn in $(pdnsutil list-zone "$ZONE" 2>/dev/null | awk '$4=="TXT" && /spf1/ {print $1}' | sort -u); do
    rel=$(to_relative "$fqdn" "$ZONE")
    # Collect every TXT value at this name, rewriting only SPF lines.
    # Need to re-quote each value for replace-rrset since list-zone's
    # output already has surrounding quotes from pdnsutil.
    new_txt=$(pdnsutil list-zone "$ZONE" 2>/dev/null \
      | awk -v fqdn="$fqdn" -v dst="$DST_IP" '
          $1==fqdn && $4=="TXT" {
            # Reassemble the original TXT value (everything from $5 on).
            v=""
            for (i=5; i<=NF; i++) v = v (i>5 ? OFS : "") $i
            if (v ~ /v=spf1/) {
              # Replace any existing ip4: token with ip4:<dst>.
              gsub(/ip4:[^ "]+/, "ip4:" dst, v)
            }
            print v
          }')
    if [ -n "$new_txt" ]; then
      ttl=$(pdnsutil list-zone "$ZONE" 2>/dev/null \
        | awk -v fqdn="$fqdn" '$1==fqdn && $4=="TXT" {print $2; exit}')
      [ -z "$ttl" ] && ttl=3600
      # Pass each value as its own argument so replace-rrset accepts
      # them as a multi-value rrset. xargs -d preserves the embedded
      # quotes pdns emitted.
      printf '%%s\n' "$new_txt" \
        | xargs -d '\n' -r pdnsutil replace-rrset "$ZONE" "$rel" TXT "$ttl" >/dev/null 2>&1 && changed=1
    fi
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

	// Idempotency: if the destination already has a server_config doc for
	// maintenance, the operator has already touched it (either initial
	// mirror succeeded last transfer, or they explicitly toggled). Leave
	// local state alone — stomping on the operator's "disabled" flips
	// every transferred domain back into the 503 catch-all, which is
	// exactly the "after I set normal, all domains show maintenance" bug
	// (re-transfer silently re-enabled maintenance on the destination
	// even though the operator had already cleared it). Only mirror on
	// the very first transfer, when no local doc exists yet.
	col := s.db.Collection(database.ColServerConfig)
	if err := col.FindOne(ctx, bson.M{"key": "maintenance"}).Err(); err == nil {
		s.addLog(ctx, jobID, "info",
			"Source is in maintenance but destination already has a maintenance record — honouring operator's local state", "panel-records")
		return 0
	}

	if err := s.maintSvc.EnableServer(ctx, cfg); err != nil {
		s.addLog(ctx, jobID, "warn",
			fmt.Sprintf("Mirror source maintenance state failed: %s", err.Error()), "panel-records")
		return 0
	}
	s.addLog(ctx, jobID, "info",
		"Source was in maintenance — destination put into maintenance to match (first-time mirror)", "panel-records")
	return 1
}

// syncServerSettings mirrors the operator-meaningful subset of the
// source's server_config collection onto the destination. This is the
// missing piece that left every post-transfer panel with default
// timezone / empty contact email / no branding / no SMTP / Welcome
// home page — the file-transfer steps brought hosting workloads
// across, but the platform owner's product settings stayed at install
// defaults until they re-typed each one.
//
// Copies (idempotent upserts on the natural key):
//
//   - server_config{key:"timezone"}      — system timezone
//   - server_config{key:"contact_email"} — admin alerts / Let's Encrypt fallback
//   - server_config{key:"ui_settings"}   — Demo & Example Hints toggles
//   - server_config{_id:"branding"}      — panel name, logo, favicon
//   - server_config{_id:"home_page"}     — public landing-page draft + content
//   - server_config{_id:"panel_mail"}    — outgoing SMTP relay (host/port/from)
//
// Deliberately EXCLUDES local-machine state that describes the box
// the panel is currently running on, not the operator's preferences:
//
//   - server_config{key:"hostname"}      — handled by Transfer Hostname earlier
//   - server_config{key:"server_ip"}     — destination has its own IP
//   - server_config{key:"panel_domain"}  — destination connects its own domain
//   - server_config{key:"nginx"}         — bumped per-host by self-heal
//   - server_config{key:"php"|"mysql"|"mongodb"} — runtime knobs, dest sets its own
//
// Panel-mail caveat: the SMTP password lives encrypted under the
// source's APP_ENCRYPTION_KEY. If the destination's key matches (rare
// across distinct installs), decryption keeps working. If it doesn't,
// the cipher decodes to garbage and SMTP auth fails — which the
// operator sees on the next Save thanks to the synchronous test-send
// from notifier_service. We log a warning so the operator knows to
// re-enter the SMTP password if mail starts failing on the new box.
//
// Returns the count of distinct settings docs successfully copied.
func (s *TransferService) syncServerSettings(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string) int {
	type entry struct {
		filter   string // mongo filter for RemoteMongoExport
		descKey  string // human-readable label for logs
		match    bson.M // local upsert filter
	}
	entries := []entry{
		{filter: `{"key":"timezone"}`, descKey: "timezone", match: bson.M{"key": "timezone"}},
		{filter: `{"key":"contact_email"}`, descKey: "contact email", match: bson.M{"key": "contact_email"}},
		{filter: `{"key":"ui_settings"}`, descKey: "demo-hint toggles", match: bson.M{"key": "ui_settings"}},
		{filter: `{"_id":"branding"}`, descKey: "branding (name/logo/favicon)", match: bson.M{"_id": "branding"}},
		{filter: `{"_id":"home_page"}`, descKey: "home page", match: bson.M{"_id": "home_page"}},
		{filter: `{"_id":"panel_mail"}`, descKey: "outgoing SMTP", match: bson.M{"_id": "panel_mail"}},
	}

	col := s.db.Collection(database.ColServerConfig)
	copied := 0

	for _, e := range entries {
		docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB,
			database.ColServerConfig, e.filter)
		if err != nil {
			s.addLog(ctx, jobID, "warn",
				fmt.Sprintf("server settings: %s — source export failed: %s", e.descKey, err.Error()),
				"panel-records")
			continue
		}
		if len(docs) == 0 {
			// Source never had this setting configured — skip silently.
			// Destination keeps whatever it had, which on a fresh install
			// is the install-time default.
			continue
		}

		// Unwrap mongoexport's extended-JSON wrappers so binary fields
		// (panel_mail.password_cipher) and dates (updated_at) round-
		// trip as their proper Go types. Without this pass, the
		// destination would store the literal `{"$binary":{"base64":
		// "..."}}` map for password_cipher and the panel-mail service
		// would fail to decode it as []byte at read time — which is
		// exactly why the destination's Outgoing Mail card kept
		// rendering "Not configured" even after a transfer claimed
		// to mirror the SMTP doc. unwrapEJSON returns a bson.M
		// already; assert and fall back defensively.
		raw, _ := unwrapEJSON(docs[0]).(bson.M)
		if raw == nil {
			s.addLog(ctx, jobID, "warn",
				fmt.Sprintf("server settings: %s — unexpected source doc shape", e.descKey),
				"panel-records")
			continue
		}

		// Mongo refuses any $set that touches _id, even when the
		// value matches the filter's _id. Strip it before the
		// upsert; the filter already pins identity for both the
		// match path and the insert path.
		delete(raw, "_id")

		// Special-case panel_mail: the password_cipher field was
		// AES-GCM encrypted under the SOURCE server's
		// APP_ENCRYPTION_KEY. Each install picks a different key
		// at first boot, so the destination can't decrypt it as-is
		// — DecryptGCM returns garbage bytes that the panel then
		// sends to Gmail as the password, which Gmail rejects with
		// "535 5.7.8 BadCredentials". Re-encrypt the password
		// under THIS server's key so the cipher round-trips into a
		// usable plaintext on next read.
		//
		// Two cases for the cipher field shape:
		//   - the normal mongoexport path emits {"$binary":{"base64":...}}
		//     and unwrapEJSON converts it to []byte
		//   - some mongoexport builds emit plain base64 strings without
		//     the wrapper, in which case unwrapEJSON returns string —
		//     we decode it manually as a fallback so the re-encryption
		//     path still works.
		//
		// Falls back to UNSETTING password_cipher entirely when the
		// source key isn't readable or decryption fails; the upsert
		// then carries an explicit $unset alongside $set so any
		// stale garbage from a previous (failed) transfer attempt
		// gets wiped instead of silently surviving on the
		// destination. Operator re-types the SMTP password once and
		// it works.
		var unsetCipher bool
		if e.descKey == "outgoing SMTP" {
			cipher := extractCipherBytes(raw["password_cipher"])
			s.addLog(ctx, jobID, "info",
				fmt.Sprintf("server settings: SMTP — source cipher present=%t, length=%d", len(cipher) > 0, len(cipher)),
				"panel-records")
			if len(cipher) > 0 && s.panelMailSvc != nil {
				srcEncKey := ""
				if r, sErr := agent.SSHCommand(ctx, host, port, sshUser, sshPass,
					`grep -E '^APP_ENCRYPTION_KEY=' /opt/serverpanel/.env 2>/dev/null | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'" | tr -d ' ' | tr -d '\r'`); sErr == nil && r != nil {
					srcEncKey = strings.TrimSpace(r.Output)
				}
				switch {
				case srcEncKey == "":
					s.addLog(ctx, jobID, "warn",
						"server settings: SMTP — source APP_ENCRYPTION_KEY not readable from /opt/serverpanel/.env; dropping password_cipher and re-type the SMTP password on Server Settings",
						"panel-records")
					unsetCipher = true
					delete(raw, "password_cipher")
				default:
					newCipher, rErr := s.panelMailSvc.ReencryptForTransfer(cipher, srcEncKey)
					if rErr != nil {
						s.addLog(ctx, jobID, "warn",
							fmt.Sprintf("server settings: SMTP — re-encryption failed (%s); dropping cipher; re-type the SMTP password on Server Settings",
								rErr.Error()),
							"panel-records")
						unsetCipher = true
						delete(raw, "password_cipher")
					} else if len(newCipher) == 0 {
						s.addLog(ctx, jobID, "warn",
							"server settings: SMTP — re-encryption produced empty cipher; dropping",
							"panel-records")
						unsetCipher = true
						delete(raw, "password_cipher")
					} else {
						raw["password_cipher"] = newCipher
						s.addLog(ctx, jobID, "info",
							fmt.Sprintf("server settings: SMTP password re-encrypted under destination key (cipher length: %d)", len(newCipher)),
							"panel-records")
					}
				}
			} else if s.panelMailSvc == nil {
				s.addLog(ctx, jobID, "warn",
					"server settings: SMTP — PanelMailService not wired; password_cipher copied verbatim and will not decrypt on the destination",
					"panel-records")
			}
		}

		// updated_at gets refreshed locally so an audit trail says
		// "this row last changed when the transfer ran", not "when
		// the operator saved it on source three months ago".
		raw["updated_at"] = time.Now()

		// Build the upsert payload. When the SMTP cipher couldn't be
		// re-encrypted, $unset the destination's existing
		// password_cipher so any stale garbage from a previous
		// (failed) transfer doesn't survive — operator's next manual
		// Save with a real password starts from a clean slate.
		update := bson.M{"$set": raw}
		if unsetCipher {
			update["$unset"] = bson.M{"password_cipher": ""}
		}

		if _, upErr := col.UpdateOne(ctx,
			e.match,
			update,
			options.Update().SetUpsert(true),
		); upErr != nil {
			s.addLog(ctx, jobID, "warn",
				fmt.Sprintf("server settings: %s — destination upsert failed: %s", e.descKey, upErr.Error()),
				"panel-records")
			continue
		}
		copied++
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("server settings: %s mirrored from source", e.descKey),
			"panel-records")
	}

	return copied
}

// extractCipherBytes pulls the password_cipher payload out of a
// mongoexport-decoded source doc into a clean []byte regardless of how
// the wrapper landed.
//
// The expected shape after unwrapEJSON is []byte (the $binary case in
// unwrapEJSON decodes base64 → bytes), but we've also seen mongoexport
// builds emit a plain base64 string when the BSON binary subtype is
// non-standard, AND the bson driver itself sometimes hands back
// primitive.Binary directly when the same code path runs on a local
// query. Cover all three so the SMTP re-encryption never silently
// skips because of an unexpected wrapper shape.
func extractCipherBytes(v any) []byte {
	switch x := v.(type) {
	case []byte:
		return x
	case primitive.Binary:
		return x.Data
	case string:
		// Some mongoexport paths emit the raw base64 string when the
		// wrapper is stripped. Decode-or-return-empty so we don't
		// stamp ASCII text onto destination as if it were ciphertext.
		if x == "" {
			return nil
		}
		if data, err := base64.StdEncoding.DecodeString(x); err == nil {
			return data
		}
		return nil
	case map[string]any:
		// Wrapper survived unwrapEJSON (theoretically can't happen,
		// but be defensive). Try the EJSON v2 layout first, then v1.
		if inner, ok := x["$binary"]; ok {
			if im, ok := inner.(map[string]any); ok {
				if b64, ok := im["base64"].(string); ok {
					if data, err := base64.StdEncoding.DecodeString(b64); err == nil {
						return data
					}
				}
			}
			if s, ok := inner.(string); ok {
				if data, err := base64.StdEncoding.DecodeString(s); err == nil {
					return data
				}
			}
		}
	}
	return nil
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

		// Pick SSL or HTTP-only template based on whether a cert is on
		// disk — otherwise the healed vhost is HTTP-only and the :443
		// block goes missing, letting unrelated SSL vhosts capture
		// HTTPS traffic for this domain.
		useSSL := agent.LetsEncryptCertExists(d.Domain)
		if appErr == nil && app.Port > 0 {
			cfg := &agent.VhostConfig{Domain: d.Domain, Port: app.Port}
			var e error
			if useSSL {
				e = agent.CreateReverseProxyWithSSL(ctx, cfg)
			} else {
				e = agent.CreateReverseProxy(ctx, cfg)
			}
			if e == nil {
				healed++
				s.addLog(ctx, jobID, "info",
					fmt.Sprintf("Healed missing vhost for app domain %s → :%d (ssl=%t)", d.Domain, app.Port, useSSL), "vhost-heal")
			}
			continue
		}
		if svcErr == nil && svc.Port > 0 {
			// Heal missing vhost WITH aliases so re-running the transfer
			// pipeline on a partially-provisioned destination restores
			// the full server_name list instead of collapsing to primary.
			spec := buildRecoveryVhostSpec(&svc)
			spec.UseSSL = useSSL
			if e := agent.CreateProjectVhost(ctx, spec); e == nil {
				healed++
				s.addLog(ctx, jobID, "info",
					fmt.Sprintf("Healed missing vhost for project domain %s (+%d aliases) → :%d (ssl=%t)", d.Domain, len(svc.AliasDomains), svc.Port, useSSL), "vhost-heal")
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

// ensureRuntimeForApp lazily installs the runtime version an app or
// project_service needs, if it isn't already on the destination. Called
// from recoverApp / recoverProjectService so apps land on the SAME
// runtime version they had on source even when the operator skipped the
// Transfer Software component OR when the source's `/usr/local/n` was
// unreadable during detection. Idempotent: InstallNodeJS / InstallPHP
// return quickly when the requested version is already present.
//
// Empty appType or version → no-op (defaults to whatever the panel
// already has installed; resolveRuntimeBinDir falls back to "node" /
// system php).
func ensureRuntimeForApp(ctx context.Context, appType, version string) {
	appType = strings.ToLower(strings.TrimSpace(appType))
	version = strings.TrimSpace(version)
	if version == "" {
		return
	}
	switch appType {
	case "node", "nodejs":
		// The n version manager is major-keyed: "20", "22". If the
		// caller stored a full semver ("20.10.1"), collapse to the
		// major so we match the install convention.
		maj := version
		if i := strings.IndexByte(maj, '.'); i > 0 {
			maj = maj[:i]
		}
		if _, err := agent.RunCommand(ctx, "bash", "-c",
			fmt.Sprintf(`ls -d /usr/local/n/versions/node/%s.* 2>/dev/null | head -1`, maj)); err == nil {
			// check passed, still verify output non-empty
		}
		// Always call InstallNodeJS; it short-circuits when the version is already present.
		_ = agent.InstallNodeJS(ctx, maj)
	case "php":
		if _, err := agent.RunCommand(ctx, "php"+version, "-v"); err != nil {
			_ = agent.InstallPHP(ctx, version)
		}
	// ruby / python / go: no lazy installer yet; fall through.
	}
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
	// Source key for AES-GCM re-encryption of the GitHub PAT. fetchSourceEncKey
	// memoises the SSH probe so this is a no-op if the SMTP / webhook paths
	// already loaded it earlier in the run.
	srcEncKey := s.fetchSourceEncKey(ctx, host, port, sshUser, sshPass)
	if srcEncKey == "" {
		s.addLog(ctx, jobID, "warn",
			fmt.Sprintf("projects: source APP_ENCRYPTION_KEY unreadable (%s) — Deploy Software PATs will land empty; re-enter each in Project Settings",
				s.srcEncKeySource),
			"panel-records")
	}

	col := s.db.Collection(database.ColProjects)
	inserted := 0
	patReencrypted := 0
	patHealed := 0
	patDropped := 0
	for _, raw := range docs {
		oldID := extractOID(raw["_id"])
		// Pull the cipher BEFORE normaliseDoc — normaliseDoc only translates
		// id refs and doesn't touch binary fields, but reading from `raw`
		// keeps the path explicit and doesn't depend on that contract
		// staying true if normaliseDoc is ever extended.
		patCipher := extractCipherBytes(raw["github_pat_encrypted"])
		patMasked, _ := raw["github_pat_masked"].(string)

		doc := s.normaliseDoc(raw, idMap)
		slug, _ := doc["slug"].(string)
		user, _ := doc["user"].(string)
		if slug == "" || user == "" {
			continue
		}

		// Re-encrypt the PAT under the destination key when possible. If
		// the source key is unreadable or decryption fails (key rotated
		// since the cipher was written), drop the field entirely so the
		// project comes up with a clear "PAT not configured" state
		// instead of a blob that decrypt errors at every git pull.
		var rencryptedCipher []byte
		if len(patCipher) > 0 {
			if s.projectSvc != nil && srcEncKey != "" {
				newCipher, rErr := s.projectSvc.ReencryptPATForTransfer(patCipher, srcEncKey)
				if rErr == nil && len(newCipher) > 0 {
					doc["github_pat_encrypted"] = newCipher
					rencryptedCipher = newCipher
					patReencrypted++
				} else {
					delete(doc, "github_pat_encrypted")
					delete(doc, "github_pat_masked")
					patDropped++
					s.addLog(ctx, jobID, "warn",
						fmt.Sprintf("project user=%q slug=%q: PAT re-encryption failed (%v); re-enter PAT in Project Settings", user, slug, rErr),
						"panel-records")
				}
			} else {
				delete(doc, "github_pat_encrypted")
				delete(doc, "github_pat_masked")
				patDropped++
			}
		}

		var existing bson.M
		err := col.FindOne(ctx, bson.M{"slug": slug, "user": user}).Decode(&existing)
		if err == nil {
			if oid, ok := existing["_id"].(primitive.ObjectID); ok && oldID != "" {
				projIDMap[oldID] = oid
			}
			// Heal an existing destination project that has no PAT but the
			// source DOES — common on transfer re-runs where the first
			// attempt landed PAT-less (source key unreadable at the time)
			// and the operator has since fixed source-side perms or
			// switched to a root SSH user. Without this update the operator
			// would have to delete + re-create the project on destination
			// to recover its PAT, even though the cipher is now valid.
			if len(rencryptedCipher) > 0 {
				existingCipher := extractCipherBytes(existing["github_pat_encrypted"])
				if len(existingCipher) == 0 {
					update := bson.M{"github_pat_encrypted": rencryptedCipher}
					if patMasked != "" {
						update["github_pat_masked"] = patMasked
					}
					if oid, ok := existing["_id"].(primitive.ObjectID); ok {
						if _, uErr := col.UpdateOne(ctx, bson.M{"_id": oid},
							bson.M{"$set": update}); uErr == nil {
							patHealed++
						} else {
							s.addLog(ctx, jobID, "warn",
								fmt.Sprintf("project user=%q slug=%q: heal PAT failed: %s", user, slug, uErr),
								"panel-records")
						}
					}
				}
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
	if patReencrypted > 0 {
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("Migrated %d project GitHub PAT(s) — re-encrypted under destination key, auto-deploy keeps working.", patReencrypted),
			"panel-records")
	}
	if patHealed > 0 {
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("Healed %d existing project(s) on destination by backfilling their missing GitHub PAT — auto-deploy now armed.", patHealed),
			"panel-records")
	}
	if patDropped > 0 {
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("%d project(s) had unreadable PAT — re-enter the GitHub PAT in Project Settings to restore auto-deploy.", patDropped),
			"panel-records")
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
