// Package version is the single source of truth for the product name and
// release number. The frontend fetches these over /api/v1/version and
// renders them in the WHM top bar so every surface — logs, health checks,
// UI, API responses — reads from the same constants.
//
// Bumping a release:
//   - patch fix        → bump Patch
//   - new feature      → bump Minor, reset Patch
//   - breaking change  → bump Major, reset Minor + Patch
//
// Anything more sophisticated (build SHA, channel) can be set via ldflags
// later without touching call sites.
package version

import "fmt"

const (
	// Name is the product name shown next to the version in the UI.
	Name = "Betazen Server Panel"

	// Major, Minor, Patch make up the semantic version. Update here; the
	// API response and frontend header pick it up automatically.
	//
	// 3.0.17 (2026-04-28) — Database transfer follow-up: read panel
	// records from SOURCE (not destination) since panel-records sync
	// runs AFTER Transfer Databases; modernize mongorestore flags.
	//
	// E2E test on v3.0.16 caught two remaining defects:
	//
	//   1. resolvePanelDB (and recreateAccessHostGrants) read from the
	//      destination's mongo `databases` / `db_access_hosts` to find
	//      operator-set credentials — but the panel-records sync that
	//      populates those collections runs AFTER Transfer Databases
	//      in the orchestration. Net effect: the destination collection
	//      was empty when looked up, panelPass came back "", and the
	//      MySQL/MongoDB users got created with random passwords (the
	//      autologin failure mode 3.0.16 was meant to fix). Now we
	//      RemoteMongoExport from the SOURCE directly, same trick the
	//      panel-records sync uses, so we have credentials available
	//      the moment we need them.
	//
	//   2. agent.RestoreMongoDB used --db / --collection which mongo
	//      tools 100.7+ deprecate and exit non-zero on. Switched to
	//      --nsInclude=*.* with --nsFrom/--nsTo for explicit
	//      database mapping. mongorestore now actually applies the
	//      dump instead of bailing on the deprecation warning.
	//
	// 3.0.16 (2026-04-28) — Database transfer: MongoDB now actually
	// arrives, MySQL autologin works post-transfer, and remote-IP
	// allowlists carry over verbatim.
	//
	// Five interlocking bugs:
	//
	//   1. expandLinuxUserSelection had a MySQL prefix-match block but
	//      no MongoDB equivalent — when the operator picked a Linux user
	//      and didn't manually whitelist mongo DBs, the transfer-databases
	//      step iterated `discovered.Databases` directly. On a stale-cache
	//      run where `discovered` was nil, MongoDB was silently skipped.
	//      MongoDB DBs are now auto-populated by the same `<user>_<…>`
	//      prefix match the MySQL block uses.
	//
	//   2. The MySQL transfer block created destination users with
	//      `generateRandomPassword(16)` instead of the password stored
	//      on the panel's databases row — phpMyAdmin auto-login then
	//      tried the panel's password against MySQL's actual auth
	//      string, which was different, and silently failed. Now we
	//      look up the destination's panel `databases` row (populated
	//      by the panel-records sync that runs FIRST) and pass that
	//      password into agent.CreateMySQLUser so the panel's record
	//      and MySQL's reality stay in lock-step.
	//
	//   3. The MongoDB transfer block did mongorestore but never
	//      created an actual MongoDB user on the destination, so the
	//      panel showed a row but every connect attempt got
	//      AuthenticationFailed. Now we agent.CreateMongoUser using
	//      the panel-stored password right after RestoreMongoDB.
	//
	//   4. db_access_hosts (per-database remote-IP allowlist) was not
	//      synced at all — the destination's Database page showed zero
	//      allowed hosts even when the source had several. New
	//      syncDBAccessHosts walks the destination's databases, joins
	//      to the source by (db_name, type) to recover the source's
	//      database_id, then re-inserts every db_access_hosts row with
	//      the destination's database_id.
	//
	//   5. The MySQL GRANT rows that an AddAccessHost issues live in
	//      mysql.user / mysql.db — they don't transfer with mongorestore
	//      or with the panel-records sync. New recreateAccessHostGrants
	//      reads the destination's just-synced db_access_hosts and
	//      re-issues each GRANT via agent.CreateMySQLUserWithRole, so
	//      external apps connecting from a previously-allowed IP
	//      reach the destination's mysqld instead of getting "ERROR
	//      1130 (HY000): Host is not allowed to connect".
	//
	// 3.0.15 (2026-04-28) — Transfer DNS import preserves third-party
	// records. Two bugs the migration pipeline hid:
	//
	//   1. The import loop's `if recType == "SOA" || recType == "NS"
	//      { continue }` skipped EVERY NS record, including subdomain
	//      delegations (e.g. `app NS ns1.thirdparty.com`) that an
	//      operator had deliberately configured on the source. After
	//      transfer the destination zone served strictly less DNS
	//      data than the source — the user noticed when their
	//      `app NS …` row didn't appear post-migration. Now we skip
	//      SOA always (one per zone) and skip ONLY the apex NS set
	//      (`@ NS dns1…`) — those belong to the destination panel.
	//      Subdomain NS rows transfer verbatim.
	//
	//   2. When SOA-based source-IP detection failed (oldIP empty),
	//      the A-record rewrite branch wiped EVERY A value to destIP
	//      — which silently destroyed third-party A values that were
	//      never the source IP to begin with. Now we only rewrite
	//      when oldIP is known AND the value exactly matches. A
	//      third-party value (8.8.8.8, an external API host, …)
	//      lands on the destination unchanged.
	//
	// 3.0.14 (2026-04-28) — DNS Zone Records page type-chip filter
	// hardened. User reported clicking the NS chip but seeing TXT/MX
	// rows in the listing. The filter compared `r.type` to the
	// upper-cased chip label by raw string equality, so any record
	// that landed in Mongo with a lower-case / whitespace-padded type
	// (transferred zone, hand-patched migration row) silently failed
	// the match and rendered through. filteredRecords + counts both
	// now trim+upper-case both sides so `r.type === "ns"` increments
	// the NS chip count AND survives the NS filter. Same UX cPanel
	// exposes for record-type chips.
	//
	// 3.0.13 (2026-04-28) — DNS rrset TTL unified across siblings.
	// DNS protocol (RFC 2181 §5.2) stores TTL once per rrset, so two
	// A values at the same name share one TTL whether the panel
	// records them that way or not. The previous reconcile picked the
	// min TTL and wrote that to pdns but left each Mongo row at its
	// original TTL, so the WHM listing would show `60s` and `3600s`
	// rows for the same rrset while pdns served everything at 60s —
	// what the operator saw didn't match what resolvers got.
	//
	// reconcileRRSet now picks the most-recently-updated sibling's
	// TTL (last-write-wins, matching operator intent — when you edit
	// one row's TTL you mean the whole rrset), propagates that TTL
	// across every sibling Mongo row, and writes the unified value to
	// pdns. Falls back to min, then to the type default, when no
	// sibling has a useful UpdatedAt to disambiguate.
	//
	// 3.0.12 (2026-04-27) — DNS delete now reaches every name-shape
	// row for the same logical record, fixing the "delete then it
	// reappears" loop on zones whose Mongo state had legacy non-
	// canonical name rows from pre-3.0.11 code paths.
	//
	// Two changes pushing the canonicalization deeper:
	//   * reconcileRRSet normalizes the lookup name and pulls siblings
	//     by all three shapes via $in, so pdnsutil always gets the
	//     relative form (no more `ns1.zone.com.zone.com.` double-
	//     suffix on rrset writes).
	//   * DeleteRecord wipes every Mongo row whose name canonicalizes
	//     to the same relative label AND has the same type+value, not
	//     just the targeted ObjectID. Without this, a delete on the
	//     visible row left a legacy `ns1.zone.com` row behind; the
	//     next ListRecords call's heal-on-read pass saw pdns still
	//     serving and re-inserted a fresh row.
	//
	// 3.0.11 (2026-04-27) — DNS record names canonicalized to zone-
	// relative form on every Add/Update/Delete entry point, fixing the
	// "already exists" toast when an operator types ns1.zone.com. (or
	// ns1.zone.com) in the WHM form. Three failure modes the previous
	// build allowed:
	//   1. Three Mongo rows (`ns1`, `ns1.zone.com`, `ns1.zone.com.`)
	//      for the same logical record — dup check used a raw string
	//      match, so each shape passed.
	//   2. PowerDNS double-suffix corruption: pdnsutil add-record
	//      treats NAME as relative-to-zone, so passing the FQDN
	//      shape produced `ns1.zone.com.zone.com`.
	//   3. Edit by FQDN landing a parallel row instead of editing the
	//      existing relative-named row.
	// New normalizeRecordName helper strips trailing dots, collapses
	// the apex (zone-name and `@` both → `@`), and trims the zone
	// suffix to the relative label. Called at the head of AddRecord,
	// UpdateRecord, BulkAddRecords (via AddRecord), and the
	// UpdateRecordByNameType / DeleteRecordByNameType fallbacks.
	//
	// ReconcileZone now also rewrites pre-existing Mongo rows to the
	// canonical name BEFORE dedup, and walks pdns to drop any
	// `.zone.zone` doubled-suffix rrsets — single click heals every
	// shape inconsistency a 3.0.10-and-older zone may have collected.
	//
	// 3.0.10 (2026-04-27) — Database Connection modal advertises the
	// externally-reachable host instead of "localhost". Each db row is
	// still stored with Host="localhost" because that's what the panel
	// itself connects through; only the GetConnectionInfo response —
	// the one rendered in the modal and copied into mongosh / Compass
	// / mysql CLI — is rewritten. Resolution order per type:
	// MONGO_PUBLIC_HOST / MYSQL_PUBLIC_HOST env override (set this to a
	// friendly DNS name like mongo.example.com when one exists) →
	// SERVER_IP (auto-detected) → stored Host (legacy fallback). The
	// connection string + CLI command are rebuilt against the resolved
	// host so the three displayed fields stay consistent.
	//
	// 3.0.9 (2026-04-27) — Transfer IP repoint preserves third-party
	// values. The repointSourceDNSToDestination shell script used
	// `pdnsutil replace-rrset` with a single dst-IP value to swap A
	// records still pointing at the source IP, but replace-rrset
	// rewrites the WHOLE rrset, so any third-party A values sharing
	// the same name (multi-value rrset for redundancy/failover) were
	// silently wiped along with the source IP. Same blast radius for
	// SPF TXT — the rewrite clobbered any non-SPF TXT records at the
	// apex (Google verification, ownership tokens, etc.). The script
	// now reads the existing rrset values, edits only the source-IP
	// matches in-place (and only the v=spf1 token in the SPF line),
	// then calls replace-rrset with the FULL post-edit list. Third-
	// party values land on the destination unchanged.
	//
	// 3.0.8 (2026-04-27) — DNS list/edit/delete: heal records that
	// existed only in PowerDNS without a Mongo backing. The 3.0.7 fix
	// closed the multi-value-rrset delete bug; 3.0.8 closes the
	// "edit/delete returns 'record not found'" follow-up where the
	// frontend got `record.id = "000000000000000000000000"` (zero
	// ObjectID) and sent it back to the backend, which couldn't decode
	// it. Three changes:
	//
	//   * ListRecords now matches PowerDNS↔Mongo via a normalized key
	//     (TXT with-or-without surrounding quotes, MX with-or-without
	//     priority prefix, etc.) so type-quirky records get their
	//     real Mongo IDs in the response.
	//   * Records that genuinely have no Mongo backing get one inserted
	//     during the list pass (heal-on-read). Subsequent edits/
	//     deletes work via real IDs.
	//   * Backend Update/Delete handlers fall back to a name+type+
	//     value lookup when the URL :id can't be decoded — so a stale
	//     browser tab that loaded the zone BEFORE the heal still works.
	//   * Frontend treats the all-zeros ObjectID as a sentinel and
	//     routes through the fallback path, carrying `existing_value`
	//     so the backend disambiguates inside multi-value rrsets.
	//
	// 3.0.7 (2026-04-24) — DNS records: stop dropping multi-value
	// rrsets on delete + block exact duplicates on add. The old code
	// path called `pdnsutil delete-rrset` for every single-row delete,
	// which wipes the ENTIRE rrset (every value sharing that name+type)
	// at once — so deleting one of two `ns1 A` rows orphaned the
	// surviving sibling in PowerDNS, and the next delete failed with
	// "record not found" because pdns no longer had the rrset. Add
	// allowed exact duplicates (same name+type+value) into Mongo even
	// though pdns can't represent them, so the panel showed a fiction.
	//
	// Now Mongo is the source of truth; PowerDNS is the projection. New
	// reconcileRRSet helper rewrites a single rrset via
	// `pdnsutil replace-rrset` from whatever rows survive in Mongo,
	// using the min TTL across siblings (DNS protocol stores TTL per
	// rrset, not per value). Add/Update/Delete each call it after their
	// Mongo write. Add rejects same name+type+value duplicates up-front
	// with a clear error. Update catches "edit collapses to an existing
	// duplicate" before writing. Delete is now idempotent on already-
	// gone rrsets.
	//
	// New POST /api/v1/whm/dns/zones/:domain/reconcile heals existing
	// drift in one click — collapses duplicate Mongo rows and replays
	// every rrset, returning a count report. Use it on any zone the
	// 3.0.6-and-older code corrupted.
	//
	// 3.0.6 (2026-04-24) — WHM Create Database button no longer
	// requires the optional Domain field. The submit gate was
	// `creating || !form.domain` even though Domain is labelled
	// "(optional)" and only tags the db for the dashboard's per-
	// domain grouping (no impact on prefix or usage). Operators who
	// picked a vendor + filled name/user/password were stuck staring
	// at a dimmed button. Gate now matches cpanel: `disabled={creating}`,
	// and handleCreate already toasts on missing required fields
	// (db_name, username, password, vendor-or-domain).
	//
	// 3.0.5 (2026-04-24) — Edit Service modal Domains UI parity with
	// Add Service. The 3.0.4 first cut shipped a plain text input for
	// primary and a vertical row list for aliases — visually different
	// from the Add modal's PrimaryDomainSelect dropdown + chip-style
	// alias picker. Both modals now use the same components so the
	// operator sees a registered-domain dropdown (+ DnsHint) on edit.
	// Behaviour unchanged: the PUT still carries primary_domain +
	// alias_domains and the backend reconciles vhost + cert in one
	// round trip.
	//
	// 3.0.4 (2026-04-24) — Edit Service modal gains a Domains section
	// (primary domain + alias add/remove/edit). Backend: UpdateService
	// now accepts primary_domain and alias_domains; primary rename
	// unlinks the old vhost file and runs reconcile on the new primary
	// (SAN cert reissued via --expand under the new --cert-name);
	// alias_domains replaces the entire list in one shot. Aliases that
	// collide with the new primary are dropped silently. The dedicated
	// AddAlias / RemoveAlias endpoints stay for incremental tweaks
	// from elsewhere.
	//
	// 3.0.3 (2026-04-24) — RemoveAlias vhost bug fix. Dropping an alias
	// from a project service left the removed domain in server_name
	// because reconcileVhostFor unioned aliases from the DB's sibling
	// rows *before* the caller's own row was updated — the removed
	// alias quietly came back through the sibling walk. Alias list is
	// now persisted BEFORE reconcile (same ordering RemoveService
	// already used). AddAlias gets the same ordering for symmetry.
	// Stale "skipServiceID" docstring removed.
	//
	// 3.0.2 (2026-04-24) — Deploy Software multi-domain for every role
	// + transfer recovery preserves aliases. The "Alias domains" input
	// now renders for backend/fullstack/worker services (previously
	// gated to frontend/static) — one service, one port, any number
	// of domains on a shared nginx vhost + SAN cert. The server-to-
	// server transfer pipeline's recoverProjectService / heal paths
	// now use CreateProjectVhost + IssueLetsEncryptMulti instead of
	// the single-domain CreateReverseProxy, closing a silent-drop bug
	// where migrated services with aliases landed as server_name
	// primary-only on the destination (aliases survived in Mongo but
	// were missing from nginx and the Let's Encrypt cert).
	//
	// 3.0.1 (2026-04-24) — OTP magic-link cross-browser handoff: the
	// originating browser polls for approval and auto-completes when
	// the emailed URL is clicked from another browser (e.g. your
	// mailbox open in Browser B while you're signing in from
	// Browser A). The clicking browser never gets a session; the
	// binding-cookie gate still blocks forwarded-link takeovers.
	// Also removes the legacy binding_hash="" carveout now that any
	// in-flight OTPs from 3.0.0 have expired.
	Major = 3
	Minor = 0
	Patch = 17
)

// Number returns the semantic version as "MAJOR.MINOR.PATCH". The
// Patch component auto-increments via .github/workflows/bump-version.yml
// on every code-touching push to main.
func Number() string {
	return fmt.Sprintf("%d.%d.%d", Major, Minor, Patch)
}

// Info is the JSON shape returned by GET /api/v1/version.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Major   int    `json:"major"`
	Minor   int    `json:"minor"`
	Patch   int    `json:"patch"`
}

// Get returns the current version info. Cheap — no allocation-heavy work.
func Get() Info {
	return Info{
		Name:    Name,
		Version: Number(),
		Major:   Major,
		Minor:   Minor,
		Patch:   Patch,
	}
}
