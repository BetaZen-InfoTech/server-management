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
	Patch = 10
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
