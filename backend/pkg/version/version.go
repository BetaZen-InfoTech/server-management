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
	// 3.1.56 (2026-05-17) — In-panel "How to use" guides.
	//
	// Two new pages, same component shape:
	//   - cpanel /help ("How to use the panel") — vendor-facing.
	//     Walks login → domains → DNS → SSL → email → databases →
	//     Deploy Software → WordPress → File Manager → Terminal →
	//     Backups → Team → Security → FAQ. Each block deep-links to
	//     the matching sidebar surface so help and action live one
	//     click apart. No API calls, no auth-scoped data — purely
	//     static content rendered from a typed sections array.
	//   - whm /help ("Owner Guide") — owner-facing. Same shape with
	//     content tailored to running WHM: vendors, packages, server
	//     settings, software/runtimes, transfer, monitoring, DNS at
	//     scale, mail issues, firewall + shell, maintenance tools.
	//
	// Left-nav + search across all sections, sticky on desktop, wraps
	// horizontally on mobile. Search matches section labels, intros,
	// block titles, and step text — every piece of content is reachable
	// without scrolling through unrelated chapters. Sidebar entries
	// added under Account (cpanel) and Developer (whm) using the
	// existing BookOpen lucide icon already imported next to API Docs.
	//
	// No backend changes — pure frontend feature. Both apps rebuild
	// clean; the dist size grows by ~30 kB gzipped per app.
	//
	// 3.1.60 (2026-05-17) — Live per-domain progress for bulk SSL
	// issuance.
	//
	// Replaces the synchronous "POST and wait 10 minutes" UX of the
	// WHM SSL page's "Issue / Reissue 27 Certificates" flow with a
	// job-id + poll model. Pre-3.1.60 the operator stared at a
	// "Reissuing 27..." spinner for the whole batch — no idea
	// which domain was running, which had succeeded, or whether
	// the server was even still working. Now:
	//
	//   1. POST /ssl/letsencrypt/bulk returns 202 Accepted with
	//      { job_id, total, status: queued } within ~50ms — the
	//      handler spawns a detached goroutine for the actual
	//      certbot loop.
	//   2. The goroutine walks the de-duped domain list, stamping
	//      current_domain before each call and writing the row
	//      outcome (success / failed + action + cert id / error)
	//      to Mongo as each certbot run finishes.
	//   3. Frontend polls GET /ssl/bulk-jobs/:id every 1.5s. The
	//      "Issue / Reissue" modal swaps to a live-progress view:
	//      progress bar, current-domain indicator with spinning
	//      dot, per-row table that fills in real time (pending →
	//      running → done), Cancel button.
	//   4. On terminal status (completed / cancelled / failed),
	//      polling stops; the modal stays open so the operator can
	//      review per-row outcomes before closing.
	//
	// Cancellation: POST /ssl/bulk-jobs/:id/cancel flips a flag the
	// detached goroutine checks between rows. In-flight certbot
	// calls run to completion (forcing them mid-write could leave
	// half-written cert files); already-issued rows are preserved.
	//
	// Boot-time recovery: a "running" row whose updated_at is older
	// than 10 minutes is marked failed at server startup
	// (RecoverStaleRunningJobs), so a server crash mid-job doesn't
	// leave the WHM page showing a forever-spinning progress bar.
	//
	// Detached context: the goroutine uses context.Background()
	// with a hard 45-minute cap, so closing the modal / refreshing
	// the page / losing network doesn't kill an in-flight 27-cert
	// run. The operator can refresh the SSL page and the modal
	// re-attaches to the same job_id via poll.
	//
	// New model: SSLBulkJob (in ColSSLBulkJobs collection) carries
	// status, total, success/failed/issued/reissued counts,
	// progress %, current_domain pointer, full per-row items[],
	// wildcard/reissue flags, cancel_requested flag, started_at /
	// finished_at / updated_at timestamps.
	//
	// VALIDATION
	//
	// 11 new unit tests (ComputeBulkJobProgress matrix) lock the
	// rounding behaviour the frontend's progress bar reads. The
	// goroutine-level integration needs a live Mongo so it's
	// covered by manual smoke after deploy rather than unit
	// tests.
	//
	// FILES TOUCHED
	//   - backend/internal/models/ssl.go (+SSLBulkJob, +SSLBulkJobStatus*, +IssueLetsEncryptBulkStartResponse)
	//   - backend/internal/database/collections.go (+ColSSLBulkJobs)
	//   - backend/internal/services/ssl_bulk_job_service.go (NEW — 360 lines)
	//   - backend/internal/services/ssl_bulk_job_service_test.go (NEW)
	//   - backend/internal/handlers/ssl_handler.go (+poll +cancel handlers, +202 response)
	//   - backend/internal/routes/whm_routes.go (+poll/cancel routes)
	//   - backend/internal/routes/cpanel_routes.go (+poll/cancel routes)
	//   - backend/cmd/server/main.go (+boot-time stale-row recovery)
	//   - frontend/apps/whm/src/pages/SSLPage.tsx (live-progress modal + polling effect + Cancel)
	//
	// 3.1.59 (2026-05-17) — Daily apex-domain WHOIS refresh + richer
	// expiry warnings + dashboard filter pills.
	//
	// Adds a daily-running WHOIS refresh cron that walks every apex
	// domain in the panel, re-pulls registrar / purchased-on /
	// expires-on / nameservers via the existing RDAP-then-whois
	// pipeline, and persists the result. Runs BEFORE the existing
	// expiry-notification sweep so that sweep operates on fresh data
	// — pre-3.1.59 the panel would email "5 days left" on a domain
	// the vendor renewed weeks ago, OR silently miss the warning if
	// expires_on was never set in the first place.
	//
	// Apex-only filter — subdomains share their parent's registration,
	// so RDAP-ing them wastes quota and some TLD RDAP servers refuse
	// subdomain queries outright. IsApexDomain (pure helper in
	// domain_whois_cron.go) uses a PSL-free topological check: a row
	// is apex iff no SHORTER form of its name exists in the panel's
	// domains collection. Works for ccTLD-nested names (acme.co.uk)
	// without any external data source.
	//
	// Bucket ladder expanded from {30, 21, 14, 7, 5, 3, 2, 1} to
	// {60, 45, 30, 15, 7, 5, 4, 3, 2, 1} — operators on registrars
	// that take a month to process bulk renewals need the 60-day
	// heads-up to even queue the renewal in time.
	//
	// Email template now surfaces every registrar detail: Registrar,
	// Purchased on (new), Expires on, Days left, Auto-renew, AND
	// every Nameserver (new). Vendors diagnosing "I renewed but the
	// site still doesn't resolve" can spot a stale-nameservers issue
	// in the warning email itself without logging in.
	//
	// Dashboard "Domains expiring soon" widget gets a filter-pill
	// row: clickable badges for {60, 45, 30, 15, 7, 5, 4, 3, 2, 1}
	// days with cumulative count badges. Per-row details now show
	// Purchased on, Expires on, Nameservers inline (no second
	// fetch).
	//
	// New endpoint: GET /domains/expiring/buckets — returns the
	// canonical bucket ladder + per-bucket count for the caller's
	// tenant scope. Drives the filter-pill row.
	//
	// VALIDATION
	//
	// 15 new unit tests across two packages:
	//   * IsApexDomain rule matrix (9 tests) — apex, subdomain, deep
	//     subdomain, ccTLD apex, ccTLD subdomain, subdomain-when-apex-
	//     missing, case-insensitive, trailing-dot, invalid names
	//   * ExpiryBuckets ladder shape + defensive copy (2 tests)
	//   * BuildDomainExpiry template (4 tests) — every-detail
	//     happy path, blank-fields-hidden, hostile-string escape,
	//     urgent-subject-at-boundary
	//
	// FILES TOUCHED
	//   - backend/internal/services/domain_whois_cron.go (NEW)
	//   - backend/internal/services/domain_whois_cron_test.go (NEW)
	//   - backend/internal/services/notifier_service.go (buckets, NotifyDomainExpiry)
	//   - backend/internal/services/domain_service.go (+ExpiringBucketCounts)
	//   - backend/internal/handlers/domain_handler.go (+ExpiringBuckets)
	//   - backend/internal/routes/whm_routes.go (+route)
	//   - backend/cmd/server/main.go (+WHOIS-refresh pass in sweep loop)
	//   - backend/pkg/mailer/notifications.go (Registered/Nameservers in template)
	//   - backend/pkg/mailer/notifications_test.go (NEW)
	//   - frontend/apps/whm/src/pages/DashboardPage.tsx (filter pills, details)
	//
	// 3.1.58 (2026-05-17) — Tenant-scope guard on AddService.
	//
	// AddService now refuses any request whose primary_domain or any
	// alias_domain isn't a panel-registered domain owned by the
	// project's linked vendor (proj.User). The WHM Add Service modal
	// already enforces this client-side by filtering the dropdown to
	// d.user === project.user — but the v3.1.57 bulk-upload path AND
	// any direct API caller (programmatic /external) bypass that UI
	// filter. Pre-3.1.58 a doctored CSV could:
	//
	//   * attach a service to a hostname not registered in the panel
	//     (install files land under a synthetic sp-<slug> user with
	//     no certbot path, vhost dangles)
	//   * attach a service to a domain owned by a DIFFERENT vendor
	//     (install_dir resolves under /home/<other-vendor>/, breaking
	//     SSL issuance + leaking the vhost across tenant boundaries)
	//
	// The new check fires for every project with proj.User set (i.e.
	// every project provisioned since the 3.1.27 user-pinning hoist).
	// Legacy projects without a linked vendor keep the old fallback
	// so their next AddService doesn't suddenly fail.
	//
	// Rules enforced (assertProjectDomainOwnership in
	// project_service.go):
	//
	//   * primary_domain must be registered in the panel
	//   * primary_domain.user must equal proj.User
	//   * every alias_domain must be registered + owned by proj.User
	//   * if req.User is supplied, it must equal proj.User (a service
	//     can't legitimately span two vendor /home dirs)
	//   * on success, req.User is pinned to proj.User
	//
	// Surfaces in the bulk-upload modal's row table as a per-row error
	// without aborting the batch — the operator can fix the offending
	// rows in their spreadsheet and re-upload only those.
	//
	// VALIDATION
	//
	// 9 unit tests in project_tenant_domain_guard_test.go covering
	// every rule (happy path, primary unregistered, primary cross-
	// tenant, alias unregistered, alias cross-tenant, req.User
	// override refused, legacy escape hatch, case-insensitivity,
	// blank-alias skip). The guard is a pure function with an
	// injected ownerLookup so the tests don't need a Mongo handle.
	//
	// FILES TOUCHED
	//   - backend/internal/services/project_service.go (+guard +helper)
	//   - backend/internal/services/project_tenant_domain_guard_test.go (NEW)
	//
	// 3.1.57 (2026-05-17) — Deploy Software: Bulk Upload Services.
	//
	// Adds a "Bulk upload" button to the Deploy Software project
	// detail drawer that accepts a CSV or .xlsx file and dispatches
	// each row through the EXACT same AddService pipeline the
	// single-row form posts — clone, framework preset apply,
	// install + build, port allocation, systemd unit, nginx vhost,
	// Let's Encrypt SSL. Partial success is normal: one bad row
	// (port clash, build failure, missing primary_domain) does NOT
	// abort the others; the response carries a per-row result
	// table the modal renders so the operator sees exactly which
	// rows need fixing.
	//
	// Why a dedicated parser file: replicating any part of
	// AddService (a 460-line pipeline) in a "batch_create" method
	// would be a maintenance landmine. The bulk path converts each
	// row into the same *models.AddServiceRequest the form posts
	// and hands it to AddService unchanged. Port-conflict, subpath-
	// uniqueness, preset defaulting, vhost reconciliation, SSL
	// issuance — all behave identically across both paths.
	//
	// Columns (canonical / aliases tolerated): name, role,
	// framework, subpath, path_prefix, primary_domain, port,
	// alias_domains, install_cmd, build_cmd, start_cmd,
	// runtime_version, git_branch, env_vars, user. Alias and
	// env-var lists use SEMICOLON as separator (CSV's comma
	// collides with field boundaries). Blank role + known
	// framework → role derived from preset (IsStatic → frontend;
	// else backend). Blank install/build/start cmds + framework
	// preset → AddService fills them in.
	//
	// Transfer compatibility: bulk-added services write to
	// project_services with the same models.ProjectService BSON
	// shape as manually-added ones, so syncProjectServices in
	// transfer_panel_records.go carries them across servers
	// identically — no transfer-side change needed. A regression
	// test (TestBuildBulkServiceRequest_TransferCompat) locks
	// that property so a future engineer can't add a column to
	// the bulk parser without also threading it through the
	// AddServiceRequest + ProjectService schema the transfer
	// importer reads.
	//
	// FILES TOUCHED
	//   - backend/internal/services/project_bulk_service.go    (NEW)
	//   - backend/internal/services/project_bulk_parser_test.go (NEW)
	//   - backend/internal/handlers/project_handler.go          (+BulkAddServices, +BulkAddServicesTemplate)
	//   - backend/internal/routes/whm_routes.go                 (+bulk + template routes)
	//   - frontend/apps/whm/src/components/BulkUploadServicesModal.tsx (NEW)
	//   - frontend/apps/whm/src/pages/DeploySoftwarePage.tsx    (+Bulk upload button + modal wiring)
	//
	// 3.1.55 (2026-05-12) — Vue + Express fullstack preset.
	//
	// Adds a third Vue preset to Deploy Software:
	//
	//   vue-express : Vue 3 (Vite-built static frontend) + Express
	//                 (node backend on $PORT serving /api/*). Pick
	//                 service role=fullstack so the panel wires
	//                 BOTH the static root (dist/) AND the /api
	//                 proxy to the backend port. Mirror of the
	//                 existing fullstack pattern that
	//                 buildRecoveryVhostSpec already supports.
	//
	// Scaffold ships a complete runnable fullstack project:
	//   - package.json with "type": "module" (server.js uses ESM
	//     `import express`)
	//   - vite.config.js with a /api proxy to localhost:3000 so
	//     `npm run dev` can talk to Express locally
	//   - index.html + src/main.js + src/App.vue (SFC that
	//     fetch()es /api/hello on mount and renders the JSON
	//     reply)
	//   - server.js — Express that reads $PORT from env and
	//     exposes /api/hello
	//
	// VALIDATION
	//
	// 1 unit test pins the preset shape + scaffold sanity (package.json
	// is valid JSON, has both `vue` + `express` deps, server.js reads
	// $PORT and exposes /api/*, vite.config.js has the /api dev proxy).
	// 1 smoke-tagged end-to-end test scaffolds the preset, runs
	// `npm install` (99 packages, 19s) + `npm run build` (Vite 5.4.2,
	// 9 modules, 924ms, 54 KB bundle), then ACTUALLY boots
	// `node server.js` on a free port and asserts GET /api/hello
	// returns 200 with the scaffold's marker JSON.
	//
	// Run with:
	//   go test -tags=smoke \
	//     -run TestVueExpressPreset_RealNpmBuildAndBoot \
	//     -v ./internal/services/...
	//
	// FILES TOUCHED
	//   - backend/internal/services/app_presets.go
	//     (+vue-express entry, ~120 lines including scaffold)
	//   - backend/internal/services/app_presets_vue_test.go
	//     (+TestVueExpressPreset_ShapeAndScaffold)
	//   - backend/internal/services/app_presets_vue_smoke_test.go
	//     (+TestVueExpressPreset_RealNpmBuildAndBoot)
	//
	// 3.1.54 (2026-05-12) — Vue support in Deploy Software.
	//
	// Two new framework presets registered in app_presets.go:
	//
	//   * vue-vite : Vue 3 + Vite (static SPA, IsStatic=true,
	//                StaticDir=dist) — mirror of react-vite. Same
	//                npm install / npm run build / nginx-serves-dist/
	//                shape. Scaffold: package.json (vue 3.4 + vite
	//                5.4 + @vitejs/plugin-vue 5.1), vite.config.js
	//                loading the Vue plugin, index.html that loads
	//                src/main.js, src/App.vue as a valid SFC with
	//                <template> + <script setup>.
	//
	//   * nuxt     : Nuxt 3 (Vue SSR, AppType=node, DefaultPort=3000)
	//                — mirror of nextjs. Build runs `npm run build`,
	//                start execs `node .output/server/index.mjs`
	//                (Nuxt 3's self-contained Nitro node server).
	//                Reads $PORT from systemd env. Scaffold:
	//                package.json (nuxt 3.13 + vue 3.4 + vue-router
	//                4.4), nuxt.config.ts pinning the node-server
	//                preset, app.vue root component.
	//
	// Frontend dropdown: no change needed — DeploySoftwarePage.tsx
	// fetches presets dynamically from /api/v1/whm/apps/presets, so
	// both new entries auto-appear in the "Framework preset" select.
	//
	// VALIDATION
	//
	// 3 unit tests pin preset shape + scaffold sanity, 2 sub-cases
	// added to TestResolveServiceAppType for the framework→app_type
	// mapping. One real-build smoke test under the `smoke` build tag
	// actually runs `npm install` + `npm run build` against the
	// scaffold in a temp dir and asserts dist/index.html +
	// dist/assets/*.js are produced. Passed locally with Node 24 +
	// npm 11 (Vite 5.4.2, 9 modules transformed, 810ms build).
	//
	// Run the smoke test with:
	//   go test -tags=smoke -run TestVueVitePreset_RealNpmBuild \
	//     -v ./internal/services/...
	//
	// RUNTIME REQUIREMENTS ON THE VPS
	//
	// Same as the existing React + Vite / Next.js presets — Node.js
	// 18+ (the panel's existing Node runtime via /usr/local/n
	// covers this). No new system packages, no extra apt installs.
	// Vite + Vue + Nuxt all come from npm.
	//
	// FILES TOUCHED
	//   - backend/internal/services/app_presets.go (+ ~120 lines)
	//   - backend/internal/services/project_helpers_test.go (+2 cases)
	//   - backend/internal/services/app_presets_vue_test.go (NEW)
	//   - backend/internal/services/app_presets_vue_smoke_test.go (NEW)
	//
	// 3.1.53 (2026-05-12) — diagnostic CLI for post-transfer mailbox
	// login failures. Operator-facing tool for the
	// "I can't log into email/webmail after the transfer" report.
	//
	// NEW: bzpanel diag-mail-login <email> (aliases: diag-mail,
	// mail-diag). Pure read-only. Walks every link in the IMAP-login
	// chain for a specific mailbox and prints a PASS/FAIL checklist:
	//
	//   1. Mongo: mailbox row exists in `mailboxes` collection
	//   2. Mongo: password hash field non-empty + has {SCHEME} prefix
	//   3. Mongo: domain row exists; reports dom.User
	//   4. Computes expected maildir path the way RebuildMailboxMaps does
	//   5. /etc/dovecot/users: line for the email
	//   6. /etc/postfix/virtual_mailbox_maps: line for the email
	//   7. /etc/postfix/virtual_mailbox_domains: domain declared
	//   8. Filesystem: maildir directory exists, owned uid:gid 5000,
	//      cur/new/tmp subdirs present
	//   9. systemctl: dovecot active
	//  10. systemctl: postfix active
	//
	// First FAIL is almost always the root cause — printed in the
	// order dovecot processes them, so a missing Mongo row shadows
	// downstream checks etc. Each FAIL line includes the exact
	// remediation command.
	//
	// USED FOR
	//
	// Operator pastes:
	//     bzpanel diag-mail-login alice@cholun.com
	// after a transfer when their IMAP/webmail client returns "auth
	// failed" / "connection refused" / "user unknown". Within seconds
	// they see WHERE in the dovecot+postfix+mongo+filesystem chain
	// the breakage is. No more "is it Mongo? is it dovecot? is it
	// postfix? is it the maildir?" guessing.
	//
	// CONTEXT
	//
	// Pre-3.1.50 a transfer of N mailboxes silently dropped N-1 of
	// them (mailbox-dedup bug, fixed in 3.1.50). Pre-3.1.51 the
	// surviving mailbox had no old mail because RemoteBackupEmail
	// missed the panel's primary /var/vmail path (fixed in 3.1.51).
	// Operators with v3.1.47 still in production hit BOTH bugs and
	// see "can't log in to my mailbox" because their specific
	// mailbox is one of the N-1 dropped rows. diag-mail-login makes
	// this self-evident: "[FAIL] mongo: mailbox row exists — no row
	// in mailboxes collection — re-run after deploying v3.1.50+".
	//
	// FILES TOUCHED
	//   - backend/cmd/bzpanel/main.go (+1 dispatch case, +1 usage
	//     block, +cmdDiagMailLogin function)
	//
	// 3.1.52 (2026-05-12) — Deploy Software project + service ObjectIDs
	// no longer regenerate during a server transfer. Pre-3.1.52 every
	// transfer minted fresh _ids for projects + project_services,
	// silently breaking:
	//
	//   * GitHub webhooks — URL embeds the project _id
	//     (/api/v1/deploy/webhooks/project/<id>); after migration the
	//     old hex 404'd and `git push` no longer triggered auto-deploy.
	//   * The documented External API at
	//     /api/v1/external/deploy/projects/<id>/services/<svc>/* —
	//     scripted CI deploys + curl-based domain links broke without
	//     warning.
	//   * Operator dashboards / scripts that copy-pasted the per-project
	//     and per-service "id: 6a02f3..." badges shown in the WHM
	//     Deploy Software UI.
	//
	// FIX
	//
	// New helper preserveSourceOIDOrFresh in transfer_panel_records.go:
	// for projects + project_services, try the source's hex _id first;
	// fall back to a fresh ObjectID only on the (astronomically rare)
	// _id collision against an existing destination row, with a loud
	// warn log so the operator knows what happened.
	//
	// chooseDestinationID is the pure decision function (no Mongo) so
	// the policy is regression-tested:
	//   - empty hex          → fresh
	//   - invalid hex        → fresh
	//   - valid hex, free    → preserved
	//   - valid hex, taken   → fresh + warn
	//
	// SCOPE
	//
	// Fixes the user-reported case (Deploy Software). Three other
	// collections still regenerate _id through the generic
	// insertDeduped path: apps (most operator-facing of the three),
	// project_deployments (history records — less critical), and
	// packages (admin catalog — internal). Apps webhook URL uses a
	// separate webhook_id field so app GitHub webhooks survive
	// today, but the app's _id IS exposed in API responses + the WHM
	// Apps page. Follow-up if operators report similar pain there.
	//
	// FILES TOUCHED
	//   - backend/internal/services/transfer_panel_records.go
	//     (preserveSourceOIDOrFresh + chooseDestinationID helpers,
	//     wired into syncProjectsForTransfer + syncProjectServices)
	//   - backend/internal/services/transfer_panel_records_id_preserve_test.go
	//     (5 tests, including a regression case pinned to the
	//     screenshot's actual _id 69f9e50d82545a13b595e29f)
	//
	// 3.1.51 (2026-05-12) — CRITICAL: "Email Accounts & Data" transfer
	// silently dropped EVERY OLD MESSAGE for every panel-managed
	// domain. Fix shipped end-to-end.
	//
	// THE BUG (two halves)
	//
	// Operator question: "also old mail details will be transfer?"
	// Answer pre-3.1.51: no — even with the wizard checkbox ticked,
	// not a single Maildir message moved across.
	//
	// Half 1 — source-side (agent/transfer.go:RemoteBackupEmail).
	// Searched only /home/<owner>/mail/<domain>/ and the legacy
	// /var/mail/vhosts/<domain>/. The panel actually stores Maildirs
	// at /var/vmail/<domain>/<localpart>/ (see EmailService.
	// getMaildirPath at email_service.go:256). Result: when neither
	// of the two checked paths existed, the script took the
	// "no SRC found → emit empty tarball" branch and shipped a
	// zero-message archive across.
	//
	// Half 2 — destination-side (agent/backup.go:RestoreEmail).
	// Hardcoded `tar -xzf ... -C /var/mail/vhosts`. Even on the rare
	// install where the source archive HAD content, the destination
	// extracted it to /var/mail/vhosts/<domain>/ — Dovecot reads
	// from /var/vmail/<domain>/<user>/ on every Betazen install, so
	// the messages landed somewhere nothing on the destination ever
	// reads. Inboxes appeared empty post-transfer.
	//
	// SHIPS THIS VERSION
	//
	// 1. RemoteBackupEmail — search order is now:
	//      /var/vmail/<domain>/         (panel default, most common)
	//      /home/<owner>/mail/<domain>/ (panel + linux-owner case)
	//      /var/mail/vhosts/<domain>/   (legacy cPanel fallback)
	//
	// 2. RestoreEmail — extracts to /var/vmail/, ensures the dir
	//    exists, chowns vmail:vmail, applies u+rwX,g+rX so dovecot
	//    can read the dropped Maildirs. Empty archives (source had
	//    no mail for that domain) are now treated as success.
	//
	// FILES TOUCHED
	//   - backend/internal/agent/transfer.go (RemoteBackupEmail rewrite)
	//   - backend/internal/agent/backup.go   (RestoreEmail rewrite)
	//
	// COMBINED IMPACT WITH v3.1.50
	//
	// v3.1.50 fixed the mailbox-account dedup (Mongo + dovecot/users +
	// postfix maps); v3.1.51 fixes the actual mail-message tarball.
	// Together, "Email Accounts & Data" finally delivers what the
	// wizard label promises: every mailbox row + every old message +
	// IMAP login that works post-transfer with the original password.
	//
	// 3.1.50 (2026-05-12) — CRITICAL fix: bulk-uploaded mailboxes
	// (and ALL mailboxes with multiple-per-domain) silently dropped
	// during server transfer.
	//
	// THE BUG
	//
	// Operator report: "Bulk upload email work properly but one
	// server change, it will not to create and not work — excel
	// upload server change not work."
	//
	// Translation: bulk Excel/CSV upload writes mailboxes correctly
	// to source's Mongo, but after a server transfer only a fraction
	// of those mailboxes appear on the destination — most are
	// silently lost.
	//
	// ROOT CAUSE
	//
	// transfer_panel_records.go:227-230 had the WRONG field name in
	// the natural-key dedup function for the mailboxes sync. The
	// Mailbox model (models/email.go:10) stores the address in field
	// `email` (bson:"email") — but the dedup code queried field
	// `address`:
	//
	//   prepare:    a, _ := doc["address"].(string)             // always nil
	//   naturalKey: bson.M{"address": doc["address"]}            // always {address: null}
	//
	// MongoDB treats `{address: null}` as matching documents where
	// the field is null OR missing entirely. Every previously-
	// inserted mailbox doc has the field MISSING (it uses `email`,
	// not `address`). So:
	//
	//   1. Destination starts empty → query returns ErrNoDocuments
	//      → first mailbox inserted with field `email` (no address)
	//   2. Second mailbox attempted → query `{address: null}` now
	//      matches the previously-inserted doc (because it has no
	//      `address` field) → FindOne returns success → dedup says
	//      "already exists" → SECOND MAILBOX SKIPPED, no error log
	//   3. Third, fourth, ... → all skipped same way
	//
	// Result: every transfer dropped all-but-one mailbox per
	// destination Mongo. Bulk-uploaded sets (where the operator may
	// have 50+ mailboxes per domain) lost 49 silently. Hand-created
	// sets with multiple mailboxes per server hit the same shape
	// but the operator usually noticed the loss — bulk upload made
	// it most visible because operators trust the count to match.
	//
	// AGGRAVATING SECONDARY BUG
	//
	// resource_service.go:908 had the same field-name mismatch on a
	// different surface — projecting `["address", ...]` for the
	// tenant-resource API. Result: the WHM tenant overview returned
	// mailbox docs without the email field, so the UI showed empty
	// "Address" cells under each tenant. Same pattern at line 910
	// for forwarders projecting `destination` (singular) when the
	// model uses `destinations` (plural).
	//
	// SHIPS THIS VERSION
	//
	// 1. transfer_panel_records.go:224-241 — dedup queries the
	//    correct field `email`. After fix, every mailbox in the
	//    source's Mongo correctly transfers to destination, and
	//    re-running the transfer is now idempotent (the second run
	//    finds the previously-inserted rows by their actual key
	//    and skips them properly instead of skipping everything
	//    after the first).
	//
	// 2. resource_service.go:907-910 — projection includes the
	//    canonical model field names (`email` for mailboxes,
	//    `destinations` + `keep_copy` for forwarders). Tenant
	//    resource API now returns full mailbox + forwarder data.
	//
	// VALIDATION
	//
	// scripts/_smoke_bulk_transfer_local.py — runs ON the
	// destination VPS after a transfer. Snapshots Mongo mailbox
	// count, samples a few addresses from source's bulk upload
	// set, confirms ALL of them landed on destination (not just
	// one). Pre-3.1.50 reports "1 of N landed"; post-3.1.50
	// reports "N of N landed".
	//
	// NO MIGRATION NEEDED — fix activates on next transfer; existing
	// destination Mongo state is untouched. Operators with already-
	// dropped mailboxes can re-run the transfer (now idempotent and
	// correct) to bring the missing ones across.
	//
	// 3.1.49 (2026-05-12) — close the post-transfer IP-rewrite gap.
	//
	// THE BUG
	//
	// "After transfer, DNS and zone IP update on old server where ip1
	// change to ip2" — operator's report. Post-transfer, the
	// destination panel's own A records / SPF tokens / domains.server_ip
	// rows / dns_zones.server_ip rows were sometimes still pointing
	// at the SOURCE IP. External resolvers hit the wrong box for half
	// their queries; mail bounced SPF, HTTP went to the old VPS, and
	// the operator had to manually run "Reassign IP" through the
	// WHM UI to clean up.
	//
	// AUDIT
	//
	// Two transfer paths in the codebase:
	//
	//   1. Full-wizard path (transfer_service.go) — CALLS
	//      ConfigService.ReassignServerIP at line 3489 ✓
	//   2. Panel-records-only path (transfer_panel_records.go) —
	//      DID NOT call ReassignServerIP ✗
	//
	// The panel-records-only path is the one operators use for
	// re-running a transfer after a partial failure or for syncing
	// just the Mongo state without redoing rsync. Without the
	// destination-side IP sweep, every record imported into the
	// destination's pdns from the source's pdns kept the source's
	// A-record values + SPF ip4: tokens.
	//
	// repointSourceDNSToDestination at panel_records.go:416 already
	// rewrites the SOURCE side (source's pdns → destination IP) but
	// nothing rewrote the DESTINATION side (destination's pdns +
	// Mongo + .env + nginx vhost) until the operator manually
	// triggered Reassign IP through the UI.
	//
	// SHIPS THIS VERSION
	//
	// 1. transfer_panel_records.go — calls
	//    s.configSvc.ReassignServerIP(ctx, host, s.serverIP) right
	//    after RunAllRehydrates and right before the summary log.
	//    Mirror of the full-wizard wiring at transfer_service.go:3489.
	//    ConfigService is already wired into TransferService at
	//    main.go:185 (transferService.SetConfigService) so no boot-
	//    order change needed. Per-area counts (a_records, spf, etc.)
	//    surface in the panel-records summary log so the operator
	//    sees what was rewritten without diffing the system after.
	//
	// 2. NEW bzpanel reassign-ip CLI subcommand. Aliases:
	//    ip-reassign, rewrite-ip. Usage:
	//        bzpanel reassign-ip [<old-ip>] <new-ip>
	//    Auto-detects old IP from `hostname -I` if omitted. Same
	//    code path the WHM /api/v1/whm/config/reassign-ip endpoint
	//    + the post-transfer auto-sweep both call. Last-mile fix
	//    for operators who notice stale IP records on the
	//    destination box outside of a transfer (manual zone
	//    imports, third-party DNS dumps, etc.).
	//
	// FILES TOUCHED
	//   - backend/internal/services/transfer_panel_records.go
	//     (+50 lines around line 456: destination-side IP sweep
	//     after RunAllRehydrates)
	//   - backend/cmd/bzpanel/main.go
	//     (+1 dispatch case, +1 usage block, +cmdReassignIP function)
	//   - backend/pkg/version/version.go (this entry)
	//
	// END-TO-END IP REWRITE NOW LOOKS LIKE
	//
	//   panel-records sync ends
	//     ├─ RunAllRehydrates (mailboxes, forwarders, ssh, dns,
	//     │   mysql, ftp, wp) — destination filesystem in sync
	//     ├─ repointSourceDNSToDestination — SOURCE pdns A+SPF
	//     │   rewritten to DESTINATION IP (closes split-brain
	//     │   when both panels' NSs are in the live delegation)
	//     └─ ConfigService.ReassignServerIP — DESTINATION pdns
	//         A+SPF + Mongo domains.server_ip + dns_zones.server_ip
	//         + /opt/serverpanel/.env + nginx vhost rewritten;
	//         NS+SOA re-stamped with canonical dns1-dns4.
	//
	// Both directions of the IP-rewrite gap closed in one place,
	// auto-runs at end of every transfer (no operator action
	// required), idempotent, and exposed as both an HTTP endpoint
	// and a CLI subcommand for ad-hoc use.
	//
	// 3.1.48 (2026-05-10) — five MORE post-transfer rehydrate fixes
	// (SSH keys, DNS, MySQL access, FTP accounts, WordPress
	// wp-config) + a unified RunAllRehydrates orchestrator. Same
	// shape as the v3.1.37 (forwarders) and v3.1.47 (mailboxes)
	// fixes: Mongo rows synced cleanly to the destination on
	// transfer, but the matching destination-side filesystem /
	// service state was NEVER rebuilt — leaving operators with
	// rows visible in the panel UI but the underlying feature
	// completely dead.
	//
	// AUDIT (panel-records-only path of transfer_panel_records.go)
	//
	// Eight collections sync to destination Mongo; only TWO had
	// rehydrate before this version (mailboxes from 3.1.47,
	// forwarders from 3.1.37). The other five were silently broken
	// for every operator who re-ran the transfer in panel-records-
	// only mode (or whose first transfer's full file-rehydrate
	// step partially failed).
	//
	// SHIPS THIS VERSION
	//
	// 1. NEW transfer_rehydrate.go — five Rebuild*FromMongo methods
	//    on the relevant services + a standalone RebuildFTPAccounts
	//    function (FTP rebuild is db-only, no service deps):
	//
	//      * SSHKeyService.RebuildAuthorizedKeysFromMongo
	//        Walks ssh_keys, groups by user, atomically rewrites
	//        /home/<user>/.ssh/authorized_keys (or /root/.ssh/
	//        authorized_keys for root) with the right permissions
	//        + ownership.
	//        Pre-3.1.48 symptom: developers / team SSH "permission
	//        denied (publickey)" post-transfer despite their key
	//        appearing in the panel's Users → SSH Keys page.
	//
	//      * DNSService.RebuildPowerDNSFromMongo
	//        Walks dns_zones + dns_records, calls `pdnsutil
	//        list-zone` to probe for missing zones, `pdnsutil
	//        create-zone` for any missing, then `pdnsutil
	//        replace-rrset` per (zone, name, type) group with TTL
	//        + multi-value. Honours MX priority + SRV
	//        priority/weight/port. Final `rectify-all-zones` +
	//        `systemctl reload pdns`.
	//        Pre-3.1.48 symptom: NXDOMAIN on every transferred
	//        zone — mail SPF auth fails 50% of the time, HTTPS
	//        cert validation breaks, nameserver delegation broken.
	//
	//      * DatabaseService.RebuildMySQLAccessFromMongo
	//        Walks databases + db_users + db_access_hosts. CREATE
	//        DATABASE IF NOT EXISTS + CREATE USER IF NOT EXISTS +
	//        ALTER USER (refresh password) + GRANT ALL per
	//        (user, host) tuple. mysqlEscape() escapes single
	//        quotes + backslashes for safe SQL string literal use.
	//        Final FLUSH PRIVILEGES.
	//        Pre-3.1.48 symptom: app connections fail
	//        "Access denied for user '<name>'@'<host>'" — the
	//        database row + user row exist in panel UI but the
	//        actual MySQL/MariaDB has neither.
	//
	//      * RebuildFTPAccountsFromMongo (standalone — db only)
	//        Walks ftp_accounts, runs `pure-pw userdel` (silently)
	//        then `pure-pw useradd` with stdin-piped password
	//        confirmation (UID/GID 5000, the panel convention).
	//        Final `pure-pw mkdb` to compile the .pdb Pure-FTPd
	//        actually reads.
	//        Pre-3.1.48 symptom: FTP login "authentication
	//        failed" silently for every transferred account.
	//
	//      * WordPressService.RewriteWordPressConfigsFromMongo
	//        Walks wordpress_installs, for each one rewrites the
	//        wp-config.php DB_NAME / DB_USER / DB_PASSWORD /
	//        DB_HOST=localhost via four sed -i invocations
	//        (single OR double-quoted patterns, matches both
	//        php styles). Skips installs whose wp-config.php
	//        doesn't exist on disk (file-transfer step would
	//        have created it).
	//        Pre-3.1.48 symptom: every transferred WordPress site
	//        loaded "Error establishing a database connection"
	//        because wp-config.php still pointed at the source's
	//        DB_HOST (often a different LAN IP).
	//
	// 2. UNIFIED ORCHESTRATOR — TransferService.RunAllRehydrates
	//    calls every Rebuild method on its matching wired service
	//    in sequence, returns AllRehydratesResult with per-area
	//    counts + a Skipped[] list of areas whose service isn't
	//    wired. Each call is best-effort; one failed area logs
	//    its error + continues to the next so the operator gets
	//    the full per-area breakdown in one round-trip.
	//
	// 3. WIRED INTO transfer_panel_records.go — at the end of the
	//    panel-records sync (after every Mongo collection has
	//    landed), calls RunAllRehydrates and adds the per-area
	//    healed counts to the transfer-job stats so the operator
	//    sees `rehy_ssh_keys_healed:12, rehy_dns_zones_healed:8,
	//    rehy_mysql_healed:5, …` alongside vhosts_healed and
	//    apps_restarted in the recovery summary.
	//
	// 4. NEW POST /api/v1/whm/transfer/rehydrate-all (gated on
	//    server.manage). Manual-recovery endpoint for an operator
	//    who notices "feature dead post-transfer" and wants to
	//    re-run the rehydrate without re-running the wizard.
	//    Returns the same AllRehydratesResult shape the transfer
	//    job logs.
	//
	// 5. NEW `bzpanel heal-after-transfer` CLI subcommand
	//    (aliases `rehydrate-after-transfer`,
	//    `post-transfer-heal`). Constructs a TransferService with
	//    the 5 db-only services wired + calls RunAllRehydrates.
	//    Prints the per-area breakdown + a "next steps" hint.
	//    SSH-only recovery surface for when the panel itself
	//    isn't reachable.
	//
	// HOW TO RECOVER 187.127.157.108 (the destination box from
	// the in-flight transfer-test session) without re-running the
	// wizard:
	//
	//   ssh root@187.127.157.108
	//   cd /opt/serverpanel && sudo bzpanel deploy   # pull v3.1.48
	//   sudo bzpanel heal-after-transfer             # ONE shot for ALL areas
	//
	// 3.1.47 (2026-05-10) — fix: server transfer left mailbox
	// Postfix/Dovecot maps EMPTY on the destination (mirror of the
	// v3.1.37 forwarder rehydrate fix, applied to mailboxes).
	//
	// User report: "Bulk upload email work properly but one server
	// change, it will not to create and not work — excel upload
	// server change not work". I.e. mailboxes bulk-uploaded on the
	// SOURCE work fine, but after running the server-transfer
	// wizard to a NEW server (set up here as
	// 187.127.156.87 → 187.127.157.108), the transferred mailboxes
	// appear in the destination panel UI but inbound mail bounces
	// "user unknown in virtual_mailbox_maps" + IMAP login fails for
	// every transferred address.
	//
	// AUDIT
	//
	// transfer_panel_records.go syncs the email_mailboxes Mongo
	// collection from source → destination, but pre-3.1.47 there
	// was NO equivalent of the v3.1.37 forwarder
	// `RebuildVirtualAliasMaps()` for mailboxes. The destination's
	// /etc/dovecot/users + /etc/postfix/virtual_mailbox_maps + …
	// /virtual_mailbox_domains were NEVER rebuilt from the freshly-
	// imported Mongo rows. The wizard's per-domain file-rehydrate
	// step in transfer_service.go DID write those files when the
	// full file-transfer step ran, but the panel-records-only
	// re-run path silently skipped it.
	//
	// FIX (mirror of v3.1.37 forwarder pattern)
	//
	// 1. NEW EmailService.RebuildMailboxMaps(ctx) — walks every
	//    Mongo mailbox row, atomically rewrites:
	//      * /etc/dovecot/users (one line per mailbox; uses the
	//        transferred password hash so IMAP auth works
	//        immediately)
	//      * /etc/postfix/virtual_mailbox_maps (one `email
	//        domain/localpart/` line per mailbox)
	//      * /etc/postfix/virtual_mailbox_domains (one `<domain>
	//        OK` line per unique domain, sorted for diff-friendly
	//        churn-free output)
	//    Then runs `postmap` on both Postfix files + reloads
	//    Postfix + Dovecot ONCE. Atomic file replace via .tmp + mv
	//    so a crash mid-run doesn't leave a half-written file
	//    visible to either daemon. Idempotent — running twice
	//    produces byte-identical output. Returns the mailbox count.
	//
	// 2. WIRED INTO transfer_panel_records.go right after the
	//    email_mailboxes syncByDomain pass — same shape as the
	//    forwarder rehydrate that follows it. Soft-skip + warn
	//    when EmailService isn't wired (slim test harness).
	//    Operator can recover via `bzpanel heal-mailboxes` or POST
	//    /api/v1/whm/email/mailboxes/rehydrate post-hoc.
	//
	// 3. NEW POST /api/v1/whm/email/mailboxes/rehydrate handler +
	//    route (gated on vendor_owner role since it touches
	//    Dovecot + Postfix config). Same code path the transfer
	//    recovery calls; surfaced as a WHM admin endpoint so an
	//    operator who notices "transferred mailboxes don't
	//    deliver" can reconcile in one click without SSH.
	//
	// 4. NEW `bzpanel heal-mailboxes` (alias `repair-mailboxes`,
	//    `rehydrate-mailboxes`) CLI subcommand for SSH-only
	//    recovery when the panel itself isn't reachable. Calls
	//    EmailService.RebuildMailboxMaps directly via
	//    database.Connect — no HTTP round-trip needed. Distinct
	//    from existing `heal-mail` which only DEDUPES the existing
	//    files (doesn't rebuild from Mongo).
	//
	// 5. NEW scripts/_smoke_transfer_mailboxes_local.py — stdlib-
	//    only diagnostic the operator pastes after `ssh root@
	//    <destination>`. Snapshots the Mongo mailbox count, samples
	//    10 addresses, checks BEFORE/AFTER rehydrate hit-rates in
	//    /etc/dovecot/users + virtual_mailbox_maps, calls the
	//    rehydrate endpoint, re-checks. Surfaces the gap explicitly
	//    so the operator sees how many mailboxes were broken
	//    pre-rehydrate.
	//
	// HOW TO RECOVER AN ALREADY-TRANSFERRED BOX (no need to re-run
	// the wizard):
	//
	//   ssh root@187.127.157.108
	//   cd /opt/serverpanel && sudo bzpanel deploy   # pull v3.1.47
	//   sudo bzpanel heal-mailboxes                  # one-shot rebuild
	//   sudo python3 scripts/_smoke_transfer_mailboxes_local.py  # verify
	//
	// 3.1.46 (2026-05-10) — REAL fix for the bulk-upload "file is
	// required" error: strip the axios `Content-Type:
	// application/json` default when the body is FormData.
	//
	// User report (screenshot): tried Bulk Upload Forwarders from
	// the panel UI itself with a real CSV picked + clicked Upload —
	// got the new error toast from v3.1.45 ("file is required —
	// POST as multipart/form-data..."). The toast was correctly
	// pointing at the symptom, but the UI was supposed to be SAFE
	// from that bug after the v3.1.41 fix dropped per-call
	// `headers: { "Content-Type": "multipart/form-data" }`. So the
	// v3.1.41 fix was incomplete.
	//
	// ROOT CAUSE
	//
	// Every axios instance in the panel sets a global default of
	// `Content-Type: application/json`:
	//   * frontend/packages/api-client/src/client.ts (shared)
	//   * frontend/apps/whm/src/lib/api.ts (per-app)
	//   * frontend/apps/cpanel/src/lib/api.ts (per-app)
	//
	// That default is correct for every JSON-bodied call (avoids
	// per-call header boilerplate). BUT it ALSO wins for FormData
	// bodies — and a multipart body with `Content-Type:
	// application/json` makes Fiber's parser refuse the request
	// with "file is required" because there's no boundary marker
	// to find each form field. The v3.1.41 fix dropped the per-
	// call header but the instance default still overrode the
	// browser's auto-set, so the bug stayed for every site.
	//
	// FIX (one-liner per axios instance)
	//
	// Each request interceptor now detects `config.data instanceof
	// FormData` and deletes the Content-Type header from the
	// request config before the request goes out. axios + the
	// browser then auto-set the correct
	// `multipart/form-data; boundary=…` header together with the
	// random boundary marker. JSON-bodied requests are unchanged
	// (FormData check is false; default still applies).
	//
	// Three sites:
	//   1. frontend/packages/api-client/src/client.ts — shared
	//      client used by branding / version / WordPress / etc.
	//   2. frontend/apps/whm/src/lib/api.ts — WHM SPA's own
	//      per-page api wrapper (mailbox bulk, forwarder bulk,
	//      domains bulk, file manager, backups restore upload).
	//   3. frontend/apps/cpanel/src/lib/api.ts — same on cpanel.
	//
	// One interceptor = every FormData upload in the entire panel
	// gets the right Content-Type. No per-page changes needed
	// (every existing `api.post(url, fd)` call just works after
	// the deploy).
	//
	// 3.1.45 (2026-05-10) — better "file is required" error message
	// on the bulk-upload endpoints + new local smoke test for the
	// v3.1.44 keep_copy fix.
	//
	// User report: hit the bulk-forwarder upload endpoint via Postman
	// with a JSON body `{ "file": {} }` and got the existing flat
	// "file is required (multipart field 'file')" message — clear
	// enough to identify the missing field but not what they did
	// wrong. Most operators making this mistake send JSON instead of
	// multipart, OR forget to switch the Postman field type from Text
	// to File, OR set Content-Type: multipart/form-data without the
	// boundary (the same bug the v3.1.41 frontend fix addresses for
	// the panel UI). The new error string names all three failure
	// modes + the curl + Postman + axios fix patterns inline so the
	// next operator sees the answer in the response body.
	//
	// Plus: scripts/_smoke_bulk_forwarders_local.py — stdlib-only
	// reproducer the operator pastes after `ssh root@<vps>`. Builds
	// the EXACT CSV from the user's report (4 rows, all
	// keep_copy=TRUE, multi-destination), POSTs as multipart, then
	// per-row asserts:
	//   * Mongo email_forwarders carries the row with keep_copy:true
	//   * /etc/postfix/virtual_alias_maps line has the SOURCE
	//     appended after the destinations (the v3.1.44 keep_copy
	//     fix — pre-3.1.44 this didn't happen and forwarded mail
	//     vanished from the source mailbox)
	//   * postmap .db is fresh (mtime < 60s)
	// Self-cleans before exit.
	//
	// 3.1.44 (2026-05-10) — fix: forwarder `keep_copy` ("Keep a copy
	// in this account") was a no-op everywhere. The flag was stored
	// in Mongo + shown as a UI checkbox + carried through the bulk
	// CSV/XLSX template + propagated by transfer-recovery — but
	// `applyForwarderToPostfix` and `RebuildVirtualAliasMaps` BOTH
	// ignored it. Every line written to /etc/postfix/virtual_alias_maps
	// was always `source → joined destinations`, so toggling "Keep
	// a copy" in the panel had ZERO effect on actual mail routing
	// and operators who turned it on still saw forwarded mail vanish
	// from the source mailbox.
	//
	// User report: "Add bulk email forward, add keep original, original
	// not to save... find bugs and solve all issue".
	//
	// FIX
	//
	// New shared helper composeForwarderDestinations(source, dests,
	// keepCopy) returns the canonical destination list to write into
	// virtual_alias_maps. When keepCopy=true it appends the source
	// address to the destinations so Postfix delivers a copy back to
	// the source mailbox in addition to forwarding (cPanel "Keep a
	// copy in this account" semantics). Output is lower-cased,
	// trimmed, de-duped — so:
	//   * keepCopy=true with source already in destinations doesn't
	//     produce `bob@x.com, sales@x.com, sales@x.com`
	//   * mixed casing in operator input collapses to a canonical
	//     line (`Sales@…` + `sales@…` → one entry)
	//   * blank entries from a trailing comma in a CSV cell get
	//     dropped silently
	//
	// applyForwarderToPostfix now takes keepCopy bool and uses the
	// helper. Three callers updated to pass it through:
	//   * EmailService.CreateForwarder — passes fwd.KeepCopy from
	//     the request body. Single-row Add Forwarder modal now
	//     actually honours the checkbox.
	//   * Bulk-row executor (createOrUpdateForwarderRow) — passes
	//     `keep` from the parsed CSV column. Bulk uploads with
	//     `keep_copy=true` rows now honour the value per row.
	//   * RebuildVirtualAliasMaps — passes each row's fwd.KeepCopy
	//     when re-emitting the file from Mongo. Used by
	//     transfer-recovery + the WHM "Rehydrate forwarder maps"
	//     button + bzpanel heal-forwarders. Pre-3.1.44 these all
	//     stripped the keep-copy behaviour silently.
	//
	// New unit test file email_forwarder_compose_test.go pins 9
	// cases covering keep on/off, source-already-in-dests dedupe,
	// case + whitespace normalisation, blank-entry drop, and the
	// edge cases (empty source, empty dest list).
	//
	// No frontend / route changes — the field already flows through
	// the request body + form + bulk template; only the Postfix
	// write path was broken.
	//
	// 3.1.43 (2026-05-10) — new WHM "Mail Issues & Resolution" page
	// (in-panel mirror of scripts/_diag_mail_stack.py + 1-click
	// auto-heal for the safe scenarios + step-by-step playbook for
	// the rest).
	//
	// User asked: surface the diag + how-to-resolve table inside the
	// panel so an operator who notices "Roundcube can't reach
	// localhost:143" doesn't have to SSH in. Lands as a self-help
	// page at /whm/mail-issues with three layers:
	//
	//   1. STRUCTURED DIAGNOSTIC (backend service + handler + 2
	//      routes). New MailDiagnosticService.Diagnose returns a
	//      DiagnosticReport with one Check per row. Each Check
	//      carries:
	//        - id (stable, e.g. svc.dovecot, port.143)
	//        - group (Packages / Services / Ports / Configuration
	//          / Tooling)
	//        - status (pass / warn / fail)
	//        - problem_type (service_down, missing_package,
	//          port_not_bound, misconfiguration, data_integrity,
	//          missing_tool — UI-only category metadata)
	//        - symptom (human-readable: "Roundcube webmail shows
	//          'Connection to storage server failed — Could not
	//          connect to localhost:143: Connection refused'.
	//          IMAP/POP3 clients can't log in.")
	//        - detail (raw stderr / journal tail / postconf output)
	//        - resolution (ordered playbook steps, e.g. "Click
	//          Auto-fix to run X. If start fails, journal tail
	//          shows why. Common causes: cert missing → run
	//          bzpanel mail-ssl-sweep; config parse error → check
	//          /etc/dovecot/conf.d/10-ssl.conf; port in use → ss
	//          -ltnp 'sport = :143' shows the offender.")
	//        - fix_command (shown verbatim)
	//        - auto_fixable (when true the UI shows a per-row Fix
	//          button + the row is included in "Auto-fix all safe
	//          issues")
	//      Eleven checks ship: 4 packages, 3 services, 4 ports, 3
	//      configuration / tooling. Each populates symptom +
	//      resolution when it fails so the page reads like a
	//      curated knowledge base, not a dump of error strings.
	//
	//   2. SAFE 1-CLICK HEAL (POST /diagnostics/mail-stack/fix).
	//      Body: { ids: [...] }. Runs the per-check FixCommand for
	//      every id in the body — bounded to apt install,
	//      systemctl start/restart, postmap, postconf -e. Never
	//      `rm -rf`, never sed-on-a-config-file. Returns one
	//      FixResult per id with success bool + output so the UI
	//      shows green/red + the journal output inline. Auto-Fix-
	//      All scoops up every status≠pass + auto_fixable=true row
	//      in one batch.
	//
	//   3. WHM PAGE (apps/whm/src/pages/MailIssuesPage.tsx +
	//      route /mail-issues + sidebar entry under Server).
	//      Layout: header summary tiles (PASS / WARN / FAIL counts),
	//      "Auto-fix N safe issues" button, per-group cards (one
	//      Card per Group string with collapsible per-row details),
	//      and a static "Common Symptoms → Resolution" knowledge-
	//      base section at the bottom that maps support-ticket-
	//      style symptoms ("emails not sending", "outbound mail
	//      lands in spam") to the diagnostic check that catches
	//      them + the manual playbook to follow when 1-click fix
	//      can't reach it.
	//
	// Routes (whm_routes.go, gated on server.manage):
	//   GET  /api/v1/whm/diagnostics/mail-stack
	//   POST /api/v1/whm/diagnostics/mail-stack/fix
	//
	// Distinct from but complementary to scripts/_diag_mail_stack.py
	// (the SSH-pasted variant of the same checks) — the script is
	// the right tool when the panel itself won't start; the in-page
	// version is the right tool when the panel works but the mail
	// stack is misbehaving.
	//
	// 3.1.42 (2026-05-10) — DeleteMailbox hardening + bulk-delete
	// loop batching.
	//
	// User asked whether bulk mailbox delete could be causing or
	// compounding the current problems. Audit found four real bugs
	// in DeleteMailbox that compound during a bulk-delete loop:
	//
	//   1. NO PATH-SAFETY GUARD ON `rm -rf` — a malformed Mongo row
	//      whose `email` field was blank or missing the @-part made
	//      getMaildirPath return paths like `/var/vmail/` (just the
	//      prefix) or `/home/<user>/mail//` and `rm -rf` would
	//      happily nuke the entire tenant mail tree. New
	//      isSafeMaildirPath guard accepts EXACTLY two path shapes
	//      (`/var/vmail/<domain>/<localpart>` 5 components OR
	//      `/home/<user>/mail/<domain>/<localpart>` 6 components),
	//      rejects empty / `..` / blank-segment / trailing-slash /
	//      wrong-prefix / wrong-depth. New 17-case unit test
	//      (TestIsSafeMaildirPath) pins every failure mode so a
	//      future refactor can't silently widen the surface.
	//
	//   2. SED REGEX-META ESCAPE ONLY HANDLED `.` — a mailbox like
	//      `sales+leads@example.com` made sed interpret `+` as a
	//      quantifier and either match nothing (silent leak — entry
	//      stays in /etc/dovecot/users + virtual_mailbox_maps) or
	//      match the wrong line. Now uses postfixSedEscape (already
	//      handles `+ * ? ( ) | [ ] { } ^ $ /`).
	//
	//   3. SWALLOWED RunCommand ERRORS — every sed/postmap/rm/
	//      systemctl call ignored its return. A botched delete left
	//      Mongo clean but file state stale; the operator saw "OK"
	//      in the panel + still-routed mail in production. Now each
	//      step's failure is logged WARN with the email + step + err
	//      so journalctl shows the partial-failure trail. Also: the
	//      Mongo row delete is the LAST step + its error IS returned
	//      (was previously also swallowed via `_, err = …` followed
	//      by webhook fan-out + `return err` — the var assignment
	//      worked but the code path obscured intent).
	//
	//   4. PER-ROW POSTMAP + POSTFIX RELOAD IN A BULK LOOP —
	//      ConfirmBulkMailboxDelete called DeleteMailbox(id) in a
	//      serial loop. Each call ran `postmap` + `systemctl reload
	//      postfix`. A 50-mailbox bulk delete fired 50 reloads
	//      back-to-back; on a busy box Postfix occasionally failed
	//      to reload (kernel rate-limit) and the operator saw
	//      "deleted ok" in the panel + still-routed mail in
	//      production for the half-second window before the next
	//      reload won. Worst case the kernel killed the reload
	//      altogether and Postfix sat with a stale map until
	//      something else triggered a reload (fresh deploy, manual
	//      `systemctl restart`).
	//
	// Fix layout:
	//   * NEW isSafeMaildirPath(path) with TestIsSafeMaildirPath
	//     covering 17 attack/edge cases.
	//   * NEW DeleteMailboxBatched(id, skipReload) — same delete
	//     steps, skips per-row postmap + reload when called from
	//     a bulk loop.
	//   * NEW PostfixApplyMailboxDeleteFlush(ctx) — the one-shot
	//     postmap + reload the bulk loop calls AFTER all rows.
	//   * deleteMailbox shared impl carries every hardening above
	//     (path guard, regex escape, structured per-step logging,
	//     malformed-email refusal).
	//   * ConfirmBulkMailboxDelete now uses DeleteMailboxBatched
	//     inside the loop + flushes once at the end. A 50-mailbox
	//     bulk delete now does ONE reload, not 50.
	//
	// No frontend / route changes — the public DeleteMailbox
	// signature is unchanged so single-row delete handlers keep
	// working without edits.
	//
	// Related to but distinct from the user-reported "Connection to
	// localhost:143 refused" Roundcube error (which is the Dovecot
	// daemon being DOWN — not a delete-loop side-effect, but the
	// hardening here means a future bulk-delete typo can't take
	// the mail tree down WITH it).
	//
	// 3.1.41 (2026-05-10) — fix bulk mailbox / forwarder / domain /
	// file / backup uploads: drop the explicit
	// `Content-Type: multipart/form-data` header (no boundary) that
	// was silently breaking the multipart body on some axios versions.
	// Plus structured per-row logging in the bulk-mailbox service +
	// new diagnostic smoke test.
	//
	// User report (screenshot): "Bulk email create not to work — email
	// not to create". Reproduced via `scripts/_smoke_bulk_mailboxes.py`
	// (paramiko + admin OTP + minted JWT + multipart upload + Mongo /
	// dovecot / postfix verification — see file header for the full
	// coverage matrix).
	//
	// Root cause: every UI site that posted a FormData body via axios
	// passed `headers: { "Content-Type": "multipart/form-data" }` —
	// but a multipart body REQUIRES a `boundary=…` token in the
	// Content-Type header so the server's parser can find each form
	// field's start. axios + the browser auto-set the header WITH the
	// random boundary IFF you DON'T set the header yourself; passing
	// the bare type overrides their auto-set and the request lands
	// with no boundary, the server's `c.FormFile("file")` returns
	// "file is required (multipart field 'file')", and the row count
	// in the response is 0.
	//
	// On Chrome's modern XHR + recent axios this sometimes worked
	// because XHR re-fixes the Content-Type for FormData bodies even
	// when set by the caller, but the behaviour is environment-
	// dependent (older Safari, mobile WebView, the just-bumped
	// axios pulled in by vite-plugin-pwa's deps). Result: works on
	// the dev box, intermittent or zero-success on production.
	//
	// FIX (frontend, 7 sites)
	//   * apps/whm/src/pages/EmailPage.tsx — mailbox bulk upload +
	//     forwarder bulk upload.
	//   * apps/cpanel/src/pages/EmailPage.tsx — same two.
	//   * apps/whm/src/pages/DomainsPage.tsx — bulk domain upload
	//     (drops it on the BulkUploadDomainsModal `submit` callback).
	//   * apps/cpanel/src/pages/DomainsPage.tsx — same.
	//   * apps/whm/src/pages/FilesPage.tsx — file manager upload
	//     (10 GB cap, large multipart body where the boundary
	//     mismatch is the most catastrophic).
	//   * apps/cpanel/src/pages/FilesPage.tsx — same.
	//   * apps/whm/src/pages/BackupsPage.tsx — restore-from-upload.
	//   Same pattern in every place: drop `headers: { "Content-Type":
	//   "multipart/form-data" }` from the axios opts so axios + the
	//   browser auto-set the boundary together.
	//
	// FIX (backend logging)
	//   * email_bulk_service.go executeBulkMailboxRows now logs a
	//     per-row WARN with row + email + domain + err on every
	//     CreateMailbox failure, plus an INFO summary at the end with
	//     total / ok / failed / generated_passwords / domains_created.
	//     Pre-3.1.41 the only signal was the row's `error` string
	//     in the JSON response, which the UI sometimes hid behind a
	//     toast. Now `journalctl -u serverpanel -f` while uploading
	//     surfaces every row that hit doveadm-not-on-path /
	//     /etc/dovecot/users-not-writable / postmap-missing / etc.
	//
	// SMOKE TEST (scripts/_smoke_bulk_mailboxes.py)
	//   Paramiko-based reproducer that runs against a live VPS:
	//     1. Pre-flight: doveadm + postmap on PATH; /etc/dovecot/
	//        users + virtual_mailbox_maps writable; doveadm pw
	//        round-trip works.
	//     2. Seeds a test domain.
	//     3. Mints an admin JWT via OTP.
	//     4. Builds the EXACT CSV the WHM Bulk Upload modal would
	//        post (alice + bob).
	//     5. POSTs via curl `-F` (multipart with boundary, exactly
	//        like the browser's XHR after the v3.1.41 fix).
	//     6. Asserts response.successes == 2 + Mongo rows present
	//        + /etc/dovecot/users carries both lines + postfix
	//        virtual_mailbox_maps carries both + postmap .db is
	//        fresh + `doveadm auth test` succeeds against the
	//        auto-generated password.
	//   When all checks pass, the backend bulk path is healthy and
	//   any remaining UI breakage is the FormData / Content-Type
	//   issue this version's frontend fix addresses.
	//
	// 3.1.40 (2026-05-10) — bulk WHOIS / RDAP refresh on the Domains
	// page + bulk Force-HTTPS on the SSL page.
	//
	// User asked for two operator-facing bulk actions:
	//   1. "Forcefully check + upgrade registration details for all
	//      domains" — one click that re-runs WHOIS / RDAP for every
	//      domain in the panel and overwrites the panel's stored
	//      registrar / registration-date / expiry-date / nameserver
	//      fields with the live registry response. Fixes the "I
	//      added 50 domains six months ago and the expiry column is
	//      out of date" problem in one click instead of clicking the
	//      per-row "Edit registration" 50 times.
	//   2. "Forcefull HTTPS active for all domains" — one click that
	//      flips the force_ssl (HTTPS-only redirect) flag on every
	//      domain that has a live cert. Saves the operator from
	//      clicking the per-row toggle 92 times after a Let's
	//      Encrypt sweep.
	//
	// Both ride one new file (services/domain_bulk_refresh.go) that
	// shares the same target resolver + bounded worker pool because
	// the per-row work is so similar.
	//
	// SHARED MACHINERY (services/domain_bulk_refresh.go)
	//   * resolveBulkTargets(ctx, ids, all) — returns the Domain
	//     rows the caller can see, applying the standard CallerScope
	//     tenant filter when present. Refuses > 1000 rows in a
	//     single call so a typo in `all=true` can't lock the panel
	//     for half an hour.
	//   * Bounded parallelism: 5 workers (semaphore + sync.WaitGroup,
	//     each goroutine writes to its own pre-allocated index — no
	//     per-row mutex). Cap stops a 100-domain refresh from
	//     fanning out 100 simultaneous outbound RDAP connections
	//     and tripping rate limits.
	//   * Per-row 25-second timeout (context.WithTimeout) so one
	//     stuck TLD doesn't hold up the rest.
	//
	// 1. BULK WHOIS / RDAP REFRESH
	//    * Service: DomainService.BulkRefreshRegistration(ctx, ids,
	//      all). Calls existing WhoisLookup per domain, persists
	//      result via Mongo $set on registrar/registered_on/
	//      expires_on/nameservers + a new whois_synced_at timestamp.
	//      Blank fields are PRESERVED — we don't second-guess the
	//      registry by overwriting a present value with an empty.
	//    * Handler: DomainHandler.BulkRefreshRegistration. Body
	//      `{ ids?, all? }`.
	//    * Routes:
	//        - POST /api/v1/whm/domains/whois-refresh-bulk
	//          (gate: domain.manage)
	//        - POST /api/v1/cpanel/domains/whois-refresh-bulk
	//          (vendor scope inside the service)
	//    * UI: new "Recheck WHOIS" button in WHM DomainsPage header
	//      (between "Bulk Upload" and "Add Domain"). Confirms first
	//      because RDAP is slow (1–3 s × N). Result modal renders a
	//      per-row table with new registrar / expiry / NS-count /
	//      status. Domain list re-fetches automatically so the
	//      Expires column reflects the fresh values.
	//
	// 2. BULK FORCE-HTTPS
	//    * Service: DomainService.BulkForceSSL(ctx, ids, all,
	//      enable, ssl). Iterates the same target list, calls
	//      SSLService.ForceSSL per domain. Domains without a live
	//      cert (ssl_active=false) are SKIPPED with reason
	//      "no SSL cert — issue / reissue first" so an operator
	//      doesn't accidentally 502 an HTTP-only site by enabling
	//      HTTPS-only redirect on it.
	//    * Handler: SSLHandler.BulkForceSSL. Body
	//      `{ ids?, all?, enable }`. SSLHandler now carries an
	//      optional DomainService dep wired post-construction in
	//      main.go via SetDomainService — the bulk endpoint refuses
	//      with a 500 if it isn't wired (so an under-construction
	//      main.go can't ship a half-wired bulk action).
	//    * Routes:
	//        - POST /api/v1/whm/ssl/force-ssl-bulk
	//          (gate: ssl.manage via the parent group)
	//        - POST /api/v1/cpanel/ssl/force-ssl-bulk
	//          (vendor scope inside the service)
	//      Both registered BEFORE /:domain so Fiber doesn't parse
	//      "force-ssl-bulk" as a {domain} param.
	//    * UI: new "Force HTTPS for All" button on the WHM SSL page
	//      header (between "Upload Custom" and "Issue / Reissue").
	//      Confirms first; runs against `all=true`. Result modal
	//      renders per-row outcomes and counts skipped vs failed
	//      separately so the operator sees which domains need a cert
	//      issued before the next sweep.
	//
	// Tenant-scope flows through CallerScope on both flows — vendor
	// callers only see (and only mutate) their own domains. No OTP
	// gate on either: the matching single-row endpoints don't have
	// one (it's the same operator clicking 1 row vs 50, just less
	// repetitive), and neither flow is destructive — re-running the
	// same target set twice is idempotent.
	//
	// Pre-existing fix bundled in: WHM `App.tsx` imported
	// `@/pages/SslPage` but the file is `SSLPage.tsx`. Windows is
	// case-insensitive so the dev box never tripped, but `tsc`'s
	// case-sensitive parser caught it the moment we touched
	// SSLPage.tsx — fixed the import to match the on-disk casing
	// before the build.
	//
	// 3.1.39 (2026-05-10) — fix WHM "Login as vendor" impersonation
	// + new "Return to admin" banner in the User Panel.
	//
	// User report (screenshot): clicking the blue arrow icon on the
	// WHM Vendors page (the "Login as vendor" affordance) appeared
	// to do nothing — or worse, dropped the admin into a confusing
	// in-between state where the persisted admin Zustand store still
	// showed them as `vendor_owner` but the API rejected every call
	// because the `access_token` in localStorage now belonged to the
	// vendor. Audit pinned two coupled defects:
	//
	//   1. The handler wrote ONLY `localStorage.access_token` and
	//      hard-navigated to /whm. But each SPA persists its own
	//      auth state via Zustand-persist (WHM under `whm-auth`,
	//      User Panel under `cpanel-auth`) and on hydration calls
	//      `setAuthToken(state.accessToken)` — OVERWRITING whatever
	//      was in localStorage.access_token with the persisted
	//      admin value. The impersonation token died on the next
	//      page load.
	//
	//   2. Even if (1) had worked, the WHM SPA's LoginPage rejects
	//      every non-`vendor_owner` role and bounces to
	//      /user-panel/login — but at that bounce point the User
	//      Panel SPA boots cleanly with a STALE cpanel-auth (or
	//      none), so the impersonation token is silently dropped
	//      and the admin sees the User Panel login screen.
	//
	// Fix (frontend-only):
	//
	//   * `handleImpersonate` in apps/whm/src/pages/VendorsPage.tsx
	//     now writes the User Panel SPA's persisted Zustand store
	//     directly (key `cpanel-auth`, exact persist shape with
	//     state.{user, accessToken, refreshToken, isAuthenticated}
	//     + version: 0) and ALSO writes `localStorage.access_token`
	//     for the api-client. Hard-navs to /user-panel/dashboard.
	//     The cpanel SPA boots cleanly as the vendor — no LoginPage
	//     bounce, no token loss.
	//
	//   * Stash for "Return to admin" expanded: in addition to
	//     `admin_restore_token` + `admin_restore_refresh` (already
	//     there), the handler now stashes the entire WHM persisted
	//     Zustand state under `admin_restore_whm_auth` and the
	//     admin's display name under `admin_restore_name`. Without
	//     the WHM-store stash, restoring would set the access token
	//     but leave the WHM SPA thinking nobody is logged in (its
	//     own store is the source of truth for `user` +
	//     isAuthenticated).
	//
	//   * New `<ImpersonationBanner />` component mounted at the
	//     top of cpanel/DashboardLayout. Reads
	//     `admin_restore_name` from localStorage on mount; renders
	//     an amber strip "Impersonating — every action is
	//     audit-logged as <admin>" with a "Return to admin" button.
	//     Click restores all four stash keys (whm-auth, access_token,
	//     refresh_token, admin_restore_*), clears the cpanel-auth +
	//     admin_restore_* stash, and hard-navs to /whm/vendors so
	//     the admin lands exactly where they pressed the original
	//     "Log in as vendor" button. Banner is hidden when no stash
	//     is present — costs nothing in the normal vendor login flow.
	//
	// Backend untouched. The 15-minute impersonation token + the
	// `impersonated_by` JWT claim + the audit-log integration all
	// work as designed in v3.1.38; the bug was entirely frontend
	// state-handoff between the two SPA bundles.
	//
	// 3.1.38 (2026-05-10) — bulk email forwarder UI lands on both
	// surfaces (Email page → Forwarders tab).
	//
	// v3.1.37 shipped the backend (template + upload + export + OTP
	// delete + Postfix transfer rehydrate). v3.1.38 wires the UI:
	//
	//   * Per-row checkbox column on the Forwarders table (with a
	//     filter-aware "select all visible" header checkbox). Same
	//     selectedForwarderIDs Set survives pagination — operator
	//     can pick row 3 on page 1 + row 7 on page 2 + bulk-delete
	//     both, exact mirror of the mailbox bulk pattern.
	//   * Four new bulk-action buttons in the Forwarders header:
	//       - Bulk Upload   (opens CSV/XLSX file-picker modal +
	//         template download links + per-row result table on
	//         success).
	//       - Export CSV / Export XLSX (JWT-aware blob download via
	//         the same `saveBlob` helper the mailbox export uses;
	//         downloads SELECTED rows when any are checked, else
	//         every visible row).
	//       - Delete N (only renders when ≥1 forwarder is selected)
	//         — opens the OTP-gated bulk-delete modal.
	//   * Bulk delete modal — three-step flow (request → confirm
	//     with 6-digit code → result table) identical in shape to
	//     the mailbox bulk-delete modal so the operator's mental
	//     model carries over. Up to 5 wrong codes per request;
	//     shows the source list under a `<details>` so a careful
	//     operator can sanity-check what's about to be deleted
	//     before confirming.
	//   * Bulk upload modal — file picker + CSV/XLSX template
	//     download + result table that distinguishes "created" vs
	//     "updated" (the upload is idempotent; same source
	//     overwrites destinations, surfaced as `updated` per row).
	//
	// Surface differences (whm vs cpanel):
	//   - Same component shape on both, just different button
	//     variants (the cpanel page already uses `<Button
	//     variant="secondary">` etc.; the WHM page uses raw
	//     className overrides).
	//   - cpanel template download omits the `user` column in the
	//     instructions copy because the backend force-overrides to
	//     the authenticated caller (see v3.1.37 backend logic);
	//     WHM keeps the column docs since admins pick the owner.
	//
	// 3.1.37 (2026-05-07) — bulk email forwarders + Postfix-aware
	// transfer rehydrate.
	//
	// User asked for the bulk forwarder surface (template, upload,
	// export, delete-OTP) AND verification that server-transfer
	// carries forwarders cleanly. Audit found the bulk surface
	// missing entirely and a real transfer gap: forwarder Mongo rows
	// were synced but `/etc/postfix/virtual_alias_maps` (the file
	// Postfix actually reads) wasn't touched, so a destination
	// inherited 50 forwarder rows that silently dead-lettered every
	// inbound mail to them until the operator manually re-typed each
	// one. Customers reported "I'm not getting forwarded mail" days
	// after the cutover.
	//
	// SHIPS THIS VERSION
	//
	// 1. Bulk forwarder surface (matches mailbox bulk shape):
	//    * GET  /email/forwarders/bulk-upload/template?format=csv|xlsx
	//      WHM variant: source, destinations, keep_copy, user
	//      cPanel variant: source, destinations, keep_copy
	//      (the `user` column is dropped because vendor-side rows
	//      are auto-attached to the caller's tenant via CallerScope).
	//    * POST /email/forwarders/bulk-upload — multipart `file`,
	//      returns BulkForwarderUploadResponse with per-row outcome
	//      table. `destinations` accepts comma OR semicolon
	//      separation (Outlook copy-paste compatibility). Idempotent
	//      on duplicate `source` — same source = OVERWRITE the
	//      destinations + keep_copy fields (cPanel "edit" semantics)
	//      so re-uploading the same file is a no-op rather than a
	//      unique-index violation.
	//    * GET  /email/forwarders/export?format=csv|xlsx&ids=&all=
	//      No OTP gate — destinations are visible in the same panel
	//      page already, the bulk download is a convenience not an
	//      escalation surface. The mailbox export gates because it
	//      can include AES-decrypted PASSWORDS; forwarders have no
	//      analogous secret column.
	//    * POST /email/forwarders/bulk-delete/request-otp
	//    * POST /email/forwarders/bulk-delete/confirm
	//      Two-step OTP flow identical to mailbox bulk-delete. Uses
	//      a separate ColBulkForwarderOTP collection so a future
	//      retention sweep can target each surface independently
	//      and so the row shape carries forwarder-specific fields
	//      (forwarder_ids, sources) without overloading the mailbox
	//      row. WHM gates with RequireRole("vendor_owner") for
	//      defence-in-depth; cPanel relies on CallerScope tenant
	//      filtering at the service layer (matches the mailbox
	//      bulk-delete pattern).
	//    * POST /email/forwarders/rehydrate (WHM owner-only) —
	//      one-shot heal: rewrites virtual_alias_maps from every
	//      Mongo forwarder row + postmap + reload. Same code path
	//      transfer recovery calls (see #2).
	//
	// 2. Transfer rehydrate gap fix.
	//    `transfer_panel_records.go` already synced
	//    ColForwarders rows but never rebuilt
	//    `/etc/postfix/virtual_alias_maps` — destination Postfix
	//    inherited zero forwarder routing. Now after the syncByDomain
	//    pass on forwarders, we call `EmailService.
	//    RebuildVirtualAliasMaps(ctx)` which rewrites the file from
	//    scratch using EVERY forwarder row in the destination's
	//    Mongo, runs `postmap`, and reloads Postfix once. Idempotent
	//    — running it twice produces the same file. Logged to the
	//    transfer job alongside vhosts_healed and apps_restarted so
	//    the operator sees "rebuilt N forwarder map entries" in the
	//    recovery summary. Soft-skip + warn when EmailService isn't
	//    wired (slim test harness path); the operator can reconcile
	//    via `POST /api/v1/whm/email/forwarders/rehydrate` post-hoc.
	//
	// 3. CreateForwarder + DeleteForwarder hardening (silent fixes).
	//    * CreateForwarder now lowercases + trims source +
	//      destinations + domain at the create boundary so every
	//      downstream Mongo lookup (the bulk delete OTP resolver,
	//      the export filter, this version's idempotent re-upload
	//      detector) keys off a canonical form. Pre-3.1.37 a typed
	//      `Alice@example.com` source would refuse to match any
	//      lowercase query — same class of bug v3.1.29 fixed for
	//      mailboxes.
	//    * Postfix write is now idempotent: sed-removes any prior
	//      line for the same source key, then appends the fresh
	//      one. Pre-3.1.37 every CreateForwarder call appended
	//      blindly so re-creates accumulated duplicate rows in
	//      virtual_alias_maps; postmap silently picked the LAST
	//      one but the file grew unbounded over time.
	//    * Bulk uploads run postmap + Postfix reload ONCE at the
	//      end, not per-row, via a new
	//      `PostfixApplyForwardersFlush` helper — a 100-forwarder
	//      upload no longer reloads Postfix 100 times.
	//    * `postfixSedEscape` escapes `+` and other regex
	//      metacharacters (not just `.`) so a source like
	//      `sales+leads@example.com` doesn't accidentally match
	//      `sales-leads@example.com` during sed-based cleanup.
	//
	// SMOKE TEST
	// `python scripts/_smoke_bulk_forwarders.py` (paramiko + admin
	// OTP + minted JWT) exercises every flow above end-to-end on
	// the live VPS:
	//   1. Template download (CSV + XLSX) — asserts headers + ZIP
	//      signature.
	//   2. Bulk upload of 2 fresh rows — asserts response counts
	//      AND Mongo state AND virtual_alias_maps lines AND
	//      postmap .db file mtime within 30 s.
	//   3. Re-upload same file — asserts idempotency (2 updates,
	//      no Mongo row count change).
	//   4. CSV export — asserts both forwarders appear.
	//   5. Bulk delete OTP flow — wrong code increments attempts,
	//      right code returns successes=2 + Mongo + Postfix clean.
	//   6. Rehydrate endpoint — wrecks virtual_alias_maps with sed,
	//      hits POST /forwarders/rehydrate, asserts the wrecked
	//      lines are back. Same code path transfer recovery uses.
	//
	// 3.1.36 (2026-05-07) — frontend build degrades gracefully when
	// vite-plugin-pwa is missing from a stale node_modules.
	//
	// User report (third deploy attempt): the v3.1.34 bump that added
	// vite-plugin-pwa kept hard-failing the VPS deploy because the
	// operator's hand-typed deploy block runs `npx turbo build`
	// directly without the `npm install` that v3.1.34's lockfile
	// requires. The v3.1.35 patch made `bzpanel deploy` install +
	// build, but the operator's notes still pasted the same legacy
	// command sequence — so the bzpanel-driven path is healed but
	// the manual path still bombs with ERR_MODULE_NOT_FOUND on
	// vite-plugin-pwa.
	//
	// Defence-in-depth fix: vite.config.ts in both apps now does a
	// dynamic `await import("vite-plugin-pwa")` inside an async
	// defineConfig() and wraps it in try/catch. If the plugin is
	// installed, we get the full PWA flow (manifest + sw.js +
	// workbox + offline runtime caching). If it isn't, we log a
	// console warning pointing at the npm-install fix and build the
	// SPA WITHOUT the plugin — the panel still works, just degraded
	// to a non-PWA web app until the operator reconciles
	// node_modules. No more hard-stop deploys from a stale
	// node_modules; the next `bzpanel deploy` (or any `npm install`)
	// re-enables the PWA features automatically.
	//
	// Belt-and-braces: new top-level `npm run build:deploy` script
	// in frontend/package.json that runs `npm install --no-audit
	// --no-fund --prefer-offline && turbo run build` in one shot,
	// so an operator who prefers a hand-typed deploy block has a
	// stable command to copy without remembering the install step:
	//
	//   cd /opt/serverpanel/frontend && sudo npm run build:deploy
	//
	// Both `bzpanel deploy` (v3.1.35) and `npm run build:deploy`
	// (this version) and the graceful-fallback config (this version)
	// fix the same class of bug from three different angles.
	//
	// 3.1.35 (2026-05-07) — bzpanel rebuild now installs + builds the
	// frontend too (no more "Cannot find package" after dep bumps).
	//
	// User report from the v3.1.34 deploy: `npx turbo build` on the
	// VPS exploded with `Cannot find package 'vite-plugin-pwa'`
	// because node_modules/ on the box still pointed at the previous
	// lockfile's closure. The committed package-lock.json had the new
	// dep but `npm install` was never run. cmdRebuild built the Go
	// binaries fine but skipped the frontend entirely — every Go-only
	// patch had been working, every dep-bump patch silently broke.
	//
	// Fix lands in cmdRebuild (cmd/bzpanel/main.go): a frontend pass
	// that runs BEFORE the Go binaries —
	//   1. `npm install --no-audit --no-fund --prefer-offline` to
	//      reconcile node_modules with the lockfile. Cheap when
	//      nothing changed (npm's content-addressed cache +
	//      --prefer-offline skip the registry round-trip on no-op).
	//   2. `npx turbo build` to emit dist/ for both SPAs. Turbo's
	//      pipeline cache makes this a no-op when neither the source
	//      tree nor node_modules/ changed.
	// We run frontend first so an npm/build failure aborts BEFORE
	// we overwrite a working bzpanel binary. ENOENT on /frontend is
	// a soft-skip (slim/Docker installs that ship only the compiled
	// binaries are still valid). Missing `npm` in PATH is a hard
	// error with a pointer at install.sh / a Node symlink.
	//
	// Operators who have ALREADY hit this on the live box can
	// recover with one paste:
	//
	//   cd /opt/serverpanel/frontend && sudo npm install --no-audit \
	//     --no-fund && sudo npx turbo build && \
	//     sudo systemctl restart serverpanel
	//
	// After this patch ships, `bzpanel deploy` is once again the
	// "ship everything" one-shot it claimed to be in v3.0.29.
	//
	// 3.1.34 (2026-05-07) — PWA install + offline guard + online status.
	//
	// User asked for: install-as-app, online/offline indicator, block
	// login + actions when offline, "all modern features". Lands as a
	// coordinated upgrade across both SPAs (whm + cpanel) and the Go
	// static-server.
	//
	// PWA INSTALL
	//   * vite-plugin-pwa wired into both apps with mode generateSW
	//     (Workbox-backed). manifest.webmanifest + sw.js +
	//     workbox-<hash>.js + registerSW.js are emitted to dist/ on
	//     every build. Names + scopes match the served path:
	//       - WHM:        scope=/whm/        start_url=/whm/
	//       - User Panel: scope=/user-panel/ start_url=/user-panel/
	//     so Chrome / Edge accept them as installable. Dual-app means
	//     an installer gets two distinguishable launcher icons.
	//   * registerType: "autoUpdate" — service worker self-updates
	//     without prompting, so a deploy lands on next reload.
	//   * navigateFallback: index.html for SPA routes; denylist on
	//     /api/, /webmail/, /docs/ so authenticated API calls + the
	//     external Roundcube mount + the static docs tree never get
	//     served from the cached SPA shell.
	//   * runtimeCaching for the public no-auth meta endpoints
	//     (/api/v1/version, /api/v1/branding, /api/v1/public-settings)
	//     via StaleWhileRevalidate so the chrome (panel name + logo +
	//     version badge) renders correctly even while the device is
	//     offline. Authenticated /api/* responses are deliberately
	//     NOT cached — stale dashboard data is worse than a clean
	//     offline state.
	//   * pwa-icon.svg shipped per app (different motif so an
	//     installer can tell whm + user panel apart on their
	//     launcher); used for both manifest icons + apple-touch-icon
	//     so iOS Add-to-Home-Screen also gets a real icon.
	//   * theme-color, color-scheme, viewport-fit=cover,
	//     apple-mobile-web-app-* meta tags added to both index.html.
	//
	// OFFLINE GUARD
	//   * New shared @serverpanel/ui module `onlineStatus.ts` exports
	//     useOnlineStatus() (navigator.onLine + online/offline event
	//     listeners), usePingableServer() (stricter variant — also
	//     polls a public endpoint to catch captive-portal cases),
	//     useInstallPrompt() (captures beforeinstallprompt + exposes
	//     a programmatic prompt trigger), and OfflineError class for
	//     typed error checks at axios catch sites.
	//   * New <OfflineOverlay /> component paints a full-screen
	//     z-[60] modal block whenever navigator.onLine flips false.
	//     Sits above the mobile drawer (z-50) and Modal (z-50) so
	//     nothing in the panel can be clicked while offline. Mounted
	//     at the top of every flow that fires HTTP: DashboardLayout
	//     (whm + cpanel) AND LoginPage (whm + cpanel), so even the
	//     unauthenticated login form is gated.
	//   * <OnlineStatusBadge /> renders a red pulsing "Offline" pill
	//     in the TopBar when offline (and nothing when online by
	//     default — pass alwaysVisible to keep a green "Online"
	//     indicator on screen).
	//   * @serverpanel/api-client now short-circuits axios requests
	//     in the request interceptor when navigator.onLine === false,
	//     throwing an error with code: "ERR_OFFLINE" and isOfflineError:
	//     true. No 30 s timeouts, no spinners forever, no generic
	//     "Network Error" toast that's indistinguishable from a real
	//     5xx — the OfflineOverlay fires immediately and existing
	//     callers' .catch handlers can branch on err.isOfflineError
	//     to render a specific message.
	//
	// MODERN FEATURE GRAB-BAG
	//   * <InstallAppButton /> in the TopBar — uses useInstallPrompt
	//     to show a brand-coloured "Install app" chip ONLY when the
	//     browser fires beforeinstallprompt AND the page isn't
	//     already standalone. Tapping triggers the native install
	//     dialog (Chrome / Edge / Android). iOS users see nothing
	//     here (Safari doesn't fire the event); they install via
	//     Share → Add to Home Screen, supported by the apple-touch-
	//     icon meta tag we added.
	//   * Dark color-scheme meta so the address bar matches the
	//     panel's dark theme even before the SPA's Tailwind kicks in.
	//   * viewport-fit=cover so iOS notch / home-indicator areas
	//     paint with the panel's background instead of leaving a
	//     white safe-area band.
	//
	// GO STATIC-SERVER (cmd/server/main.go)
	//   Real bug uncovered while building: the existing SPA mount
	//   (`app.Get("/whm/*", sendWHMIndex)`) caught EVERYTHING under
	//   /whm/ — including the new /whm/sw.js, /whm/manifest.webmanifest,
	//   and /whm/pwa-icon.svg. Browsers refuse to register a service
	//   worker served as text/html, so the PWA would build cleanly
	//   but never install. Fix: explicit handlers for sw.js,
	//   registerSW.js, manifest.webmanifest, pwa-icon.svg, and the
	//   hashed workbox-<hash>.js (via Fiber's `+` glob) registered
	//   BEFORE the catchall on both /whm/* and /user-panel/* mounts.
	//   sw.js + registerSW.js get Cache-Control: no-store +
	//   Service-Worker-Allowed: / so a deploy actually replaces the
	//   running SW instead of waiting 24 h for the HTTP cache to
	//   expire. workbox-<hash>.js gets a year cache (immutable —
	//   filename changes on every build). Belt-and-braces directory-
	//   traversal guard rejects any "/" inside the matched segment.
	//
	// Out of scope (tracked for follow-up): branding-driven dynamic
	// manifest. Right now the manifest is static-built so the
	// installer icon is the bundled pwa-icon.svg, not an operator-
	// uploaded logo. Hooking the runtime branding endpoint into a
	// dynamically-generated manifest needs a small Go endpoint plus
	// a manifest URL rewrite — deferred so this patch ships clean.
	//
	// 3.1.33 (2026-05-07) — Mobile drawer hardening: ESC closes,
	// viewport-resize closes, aria-hidden tracks viewport correctly.
	//
	// Self-audit of v3.1.32 surfaced three real bugs in the mobile
	// drawer that the smoke test wouldn't catch:
	//
	//   1. ESC didn't close the drawer. Modal in the same package
	//      already closes on Escape, so users who'd discovered the
	//      shortcut elsewhere in the panel hit a dead key here. Now
	//      the Sidebar attaches a keydown listener while mobileOpen
	//      is true, mirroring Modal's pattern.
	//
	//   2. Body scroll lock leaked across viewport resize. Sequence:
	//      open drawer on a phone-width window → resize the browser
	//      to desktop. The Sidebar visually docks (md:translate-x-0
	//      wins) but mobileOpen stayed true in React state, so the
	//      `document.body.style.overflow = "hidden"` cleanup never
	//      ran and the docked desktop layout became un-scrollable
	//      until you explicitly closed the drawer (which you can't
	//      see, because it's docked). Fix: matchMedia listener on
	//      `(min-width: 768px)` calls onMobileClose() the instant
	//      the viewport crosses up past md while the drawer is
	//      logically open. The host's setMobileNavOpen(false) flips
	//      the prop on next render, the body-lock cleanup fires,
	//      scroll restored.
	//
	//   3. aria-hidden was attached unconditionally to the off-screen
	//      drawer, which on first reading would have hidden the
	//      DOCKED desktop sidebar from screen readers too (because
	//      the host always passes mobileOpen=false on desktop —
	//      there's no hamburger to flip it). Fix: new isMobile
	//      useState backed by a `(max-width: 767px)` matchMedia
	//      listener. aria-hidden is now `isMobile && !mobileOpen` —
	//      so AT users get the docked nav above md, and hide it on
	//      mobile only when actually off-screen. Also covers the
	//      Tab-key focus-into-off-screen-drawer trap that audits
	//      flag.
	//
	// SSR-safe: the isMobile useState initialiser checks `typeof
	// window` so server-side rendering returns false and the layout
	// converges on client-mount when matchMedia fires sync(). Both
	// matchMedia useEffects use the addEventListener API with the
	// addListener fallback for Safari < 14 (same shim other parts
	// of the panel use).
	//
	// 3.1.32 (2026-05-07) — Mobile-friendly chrome: Sidebar slides
	// in as an off-canvas drawer below md (768 px), TopBar gets a
	// hamburger + flex-wrap, badges + footer collapse gracefully on
	// phones, pagination buttons hit a 36 px touch target.
	//
	// User report: the panel was effectively unusable on a phone —
	// the 256 px Sidebar was always docked left so on a 360 px viewport
	// the main content area shrank to ~100 px wide. Audit pinned the
	// fixed-width docked Sidebar as the dominant defect (every page
	// inherited the broken layout) plus a constellation of smaller
	// issues that compounded the bad first impression.
	//
	// Fix lands entirely in the shared @serverpanel/ui chrome so both
	// SPAs (whm + cpanel) get it for free with no per-page changes:
	//
	//   * Sidebar — new `mobileOpen` + `onMobileClose` props. Below
	//     md the aside is fixed-positioned and translates off-screen
	//     by default (-translate-x-full); the host layout flips
	//     mobileOpen=true on hamburger tap to slide it back in
	//     (translate-x-0 + 200 ms ease-out). A black/60 backdrop
	//     paints over the underlying page; tapping the backdrop OR
	//     a nav row OR the new X button (visible only on mobile)
	//     closes the drawer. Body scroll is locked while the drawer
	//     is open so the page underneath doesn't scroll behind the
	//     overlay. Above md the Sidebar stays statically docked
	//     (md:static md:translate-x-0) — desktop UX is bit-identical
	//     to pre-3.1.32.
	//
	//   * TopBar — new `onMenuClick` prop renders a hamburger button
	//     to the left of the page title, visible only below md. The
	//     header itself drops the fixed h-16 in favour of py-3 +
	//     flex-wrap so badges (server IP, version) can flow onto a
	//     second line on narrow phones instead of clipping. The
	//     server-IP and version badges hide entirely below sm
	//     (640 px) so the title + user chip both fit on a 360 px
	//     viewport without truncation piling up. The user-name
	//     label hides below sm too (icon-only chip remains).
	//
	//   * DashboardLayout (whm + cpanel) — both layouts now host a
	//     `mobileNavOpen` useState. Auto-closes on every route
	//     change (covers deep-link / back-button cases that
	//     wouldn't go through the Sidebar's row-tap close path).
	//     Main content padding scales p-3 → sm:p-6 so phone
	//     viewports get tighter gutters. min-w-0 on the right-hand
	//     flex column so a long-content child can't push the
	//     hamburger off the screen.
	//
	//   * Table pagination — buttons bumped from `px-2 py-1` to
	//     `min-w-9 min-h-9 px-2.5 py-1.5` (36×36 px hit area, just
	//     under the WCAG 2.5.5 AAA target of 44×44 but above the
	//     practical 28-px floor that mobile users miss-tap on).
	//
	//   * cpanel DashboardPage — stat-card grid changed from
	//     `md:grid-cols-2` (768 px breakpoint) to `sm:grid-cols-2`
	//     (640 px) so tablets break to two columns earlier. Quick
	//     Actions grid now stacks to one column on phones
	//     (`grid-cols-1 sm:grid-cols-2`) instead of forcing two
	//     130-px-wide buttons.
	//
	// Out of scope for 3.1.32 (tracked for follow-up): per-page
	// `grid grid-cols-2/3/5` instances inside form modals (Apps,
	// Databases, Deploy Software wizard) — those touch ~15 files
	// and need a focused page-by-page sweep. The Modal itself is
	// already responsive (w-full + mx-4 + max-w-* cap), so phone
	// users at least see the form; the field columns just don't
	// stack yet.
	//
	// 3.1.31 (2026-05-06) — Deploy Software link-domain API: SSL now
	// actually covers the linked alias domain (www + cname + cert SAN
	// list + structured failure signal in the response).
	//
	// User report: connecting a domain to a Deploy Software service
	// via the API succeeded with 200 OK, but `https://<linked-domain>`
	// served the wrong cert and `https://www.<linked-domain>` fell
	// through to the panel's catch-all 404. Audit pinned two coupled
	// defects:
	//
	//   1. www / cname AUTO-ALIAS APPLIED ONLY TO THE PRIMARY.
	//      `buildMergedVhostSpec` (project_helpers.go) added
	//      `www.<primary>` and `cname.<primary>` to spec.Aliases —
	//      the v3.1.11 fix for konsultkaro.com — but the loop ran
	//      ONLY over the primary domain. Every LINKED alias got
	//      stored in alias_domains + landed in nginx server_name as
	//      `<alias>` ALONE, with no www / cname variant. So linking
	//      `shop.example.com` to a service whose primary was
	//      `myapp.com` produced a vhost with server_name
	//      `myapp.com www.myapp.com cname.myapp.com shop.example.com`
	//      — and `https://www.shop.example.com` SNI-routed to the
	//      panel default vhost, returning the wrong cert. The cert
	//      SAN list missed the same names because IssueLetsEncryptMulti
	//      reads spec.Aliases verbatim. Mirror loop fixed: every
	//      alias now gets www.<a> + cname.<a> implicitly, with a
	//      guard against `www.www.X` recursion when the caller
	//      already passed a www-prefixed alias. Same auto-alias
	//      contract the primary has had since v3.1.11 — just
	//      consistently applied.
	//
	//   2. CERTBOT FAILURE WAS STDERR-ONLY. `reconcileVhostFor`
	//      caught a non-zero certbot exit and logged it to stderr,
	//      then returned nil (success). The link/unlink-domain API
	//      passed that nil straight up to the consumer — 200 OK with
	//      an unmodified service object. An integrator whose new
	//      alias's DNS hadn't yet propagated had ZERO signal that
	//      `https://<alias>` would serve the wrong cert. The vhost
	//      was already in HTTPS-with-stale-cert mode (because hadCert
	//      was true on the existing primary cert), so the failure
	//      mode was silent.
	//
	//      Fix: new transient (`bson:"-"`) fields on
	//      models.ProjectService — `SSLWarning` (human-readable
	//      explainer when the alias isn't covered) and
	//      `SSLCoveredDomains` (parsed SAN list from
	//      /etc/letsencrypt/live/<primary>/fullchain.pem). Populated
	//      by new `agent.LetsEncryptCertSANs` (parses openssl x509
	//      output) + new `reconcileVhostForAliasChange` wrapper that
	//      runs the standard reconcile then verifies the live cert
	//      covers `targetDomain` via wildcard-aware SAN matching.
	//      AddAliasWithProject + RemoveAliasWithProject stamp the
	//      result onto the returned service object — so an integrator
	//      reading the API response sees:
	//
	//        {
	//          "id": "...", "alias_domains": ["shop.example.com"],
	//          "ssl_covered_domains": ["myapp.com", "www.myapp.com",
	//                                  "cname.myapp.com"],
	//          "ssl_warning": "alias shop.example.com linked + nginx
	//                          vhost updated, but the Let's Encrypt
	//                          cert for myapp.com does NOT yet cover
	//                          shop.example.com (last certbot run
	//                          failed). Most common cause: DNS for
	//                          shop.example.com is not yet resolving
	//                          to this server's IP. Once DNS is in
	//                          place, hit Reissue on the SSL page."
	//        }
	//
	//      The alias is still persisted + still in the vhost
	//      server_name (so the HTTP-01 challenge works once DNS
	//      lands) — the warning just tells the integrator to wait
	//      for DNS + reissue, instead of leaving them to wonder why
	//      the browser shows a name-mismatch error.
	//
	// Smoke test (scripts/_smoke_alias_link.py) now also asserts that
	// `www.<alias>` + `cname.<alias>` land in the nginx server_name
	// line and that the API response carries the new ssl_warning /
	// ssl_covered_domains fields.
	//
	// 3.1.30 (2026-05-06) — Deploy Software link/unlink-domain API:
	// :id path param now enforced + tenant guard + 404/403 status codes.
	//
	// User asked for a deep audit + smoke test of the alias linking
	// API. The audit pinned three coupled defects that combined to
	// the same symptom — a permissive surface that returned 200 + the
	// service object even when the caller had no business touching it:
	//
	//   1. PATH PARAM IGNORED. The route is
	//      `POST /api/v1/external/deploy/projects/:id/services/:svc/
	//      link-domain` — but the LinkDomain / UnlinkDomain handlers
	//      pulled `:svc` and dropped `:id` on the floor. So a caller
	//      could POST to ANY project ID + ANY svc ID and the handler
	//      would happily mutate the service as long as :svc resolved.
	//      `:id` was effectively documentation. Same defect on the
	//      panel-side AddAlias / RemoveAlias handlers (they take the
	//      same `:id`/`:svc` route shape from whm/cpanel routes).
	//
	//   2. NO CALLERSCOPE GUARD IN THE SERVICE LAYER.
	//      ProjectService.GetService used `bson.M{"_id": oid}` with no
	//      tenant_id filter — a vendor token holding `deploy:link`
	//      could fetch ANY service across tenants. AddAlias /
	//      RemoveAlias both ran straight off that unscoped GetService,
	//      so the same vendor could MUTATE another tenant's vhost
	//      (link a domain they own onto a competitor's service, or
	//      remove an alias from a competitor's service entirely).
	//      ListAllServices already had the tenant_id filter so the
	//      list endpoint was safe — only the per-id mutating endpoints
	//      were exposed. No corresponding flaw on the panel JWT path
	//      because vendors there can't reach `/whm/projects/...` at
	//      all (RBAC blocks the route), but the cpanel route was
	//      reachable by every tenant-scoped role and was therefore
	//      vulnerable too.
	//
	//   3. WRONG / UNHELPFUL STATUS CODES. Every failure landed as
	//      400 BadRequest with a flat error string. An integrator
	//      pointing at the wrong project ID couldn't tell that from
	//      a malformed JSON body or a duplicate-alias rejection,
	//      since all three returned the same shape. Worse, an
	//      integrator hitting the cross-tenant case got a clean 200
	//      because of (2).
	//
	// Fix lands in three layers, each individually sufficient to
	// close the cross-tenant escape but layered for defence-in-depth:
	//
	//   * SERVICE LAYER. New shared guard
	//     `assertCanLinkAliasOnService(ctx, projectIDHex, svcID,
	//     domain, mustOwnDomain)` runs at the top of every alias
	//     mutation. Resolves the service, verifies it lives under
	//     `:id` (when supplied), loads the project, and — when the
	//     caller is tenant-scoped — asserts (a) the project's
	//     tenant_id matches the caller's tenant and (b) the linked
	//     domain belongs to the caller's tenant via
	//     CallerScope.AssertOwnsDomain. New AddAliasWithProject /
	//     RemoveAliasWithProject methods take `:id` explicitly;
	//     legacy AddAlias / RemoveAlias still exist for in-process
	//     callers (transfer replays) and route through the same
	//     guard with `projectIDHex == ""` (skips only the project-id
	//     consistency check; tenant + domain checks still apply).
	//
	//   * SENTINEL ERRORS. Five named values
	//     (ErrServiceNotFound / ErrProjectNotFound /
	//     ErrServiceProjectMismatch / ErrCrossTenantProject /
	//     ErrLinkedDomainNotOwned) — handlers map them to status
	//     codes via `errors.Is`. ErrServiceProjectMismatch is 403
	//     not 404 deliberately so the API doesn't leak that the
	//     service exists under a different ID — same threat-model
	//     reasoning the GitHub web UI uses for repos behind a 404
	//     vs 403.
	//
	//   * HANDLER LAYER. Both programmatic and panel handlers now
	//     pass `:id` to the *WithProject methods, share a tiny
	//     mapAliasErr helper, and translate sentinels to 404 / 403
	//     so an integrator can finally tell the four failure modes
	//     apart from the response code alone — no parsing the error
	//     string.
	//
	// Behaviour change worth flagging: tenant-scoped callers can no
	// longer link a domain that isn't a registered Domain in the
	// panel. Pre-3.1.30 you could pass ANY string and the handler
	// would write it into alias_domains; nginx happily accepted the
	// vhost. Now the AssertOwnsDomain check requires the domain to
	// be in `cs.TenantDomains()` (i.e. registered to a user in your
	// tenant). Vendor_owner / unscoped internal callers bypass this
	// check, matching pre-existing semantics for every other "owns
	// the domain?" gate in the panel.
	//
	// Smoke test (paramiko-based, run on dev box with VPS creds):
	// `python scripts/_smoke_alias_link.py` — exercises the happy
	// path (link → list → unlink) plus the four guarded failure
	// modes (wrong project id, cross-tenant svc, unknown domain,
	// unowned domain) and asserts the new status codes. See header
	// of the script for required env.
	//
	// 3.1.29 (2026-05-06) — Mailbox webmail-link API: case-insensitive
	// lookup + absolute URL + sharper 404 + matching response shape.
	//
	// User report (screenshot toast): "mailbox not found" on the
	// External webmail-link API even when the mailbox visibly existed
	// in the panel. Live trace pinned three independent bugs that
	// stacked to produce the same symptom and confused the diagnosis:
	//
	//   1. CASE SENSITIVITY. CreateMailbox stored req.Email verbatim
	//      (Mongo `email` field carried whatever case the operator
	//      typed). GenerateWebmailToken + GetMailboxByAddress queried
	//      via `bson.M{"email": addr}` — no $regex, no toLower. So a
	//      mailbox row keyed `Admin@konsultkaro.com` would 404 every
	//      `/mailboxes/admin@konsultkaro.com/webmail-link` call (and
	//      vice versa). Address-form mailbox APIs (GET stats, DELETE,
	//      webmail-link) all funnelled through the same broken
	//      lookup, so a single mismatched-case row knocked out three
	//      endpoints. Email RFC says local-part case is "preserved
	//      but ignored" — every other auth path in the panel already
	//      lowercases (login email, transfer self-heal); this lookup
	//      didn't.
	//
	//      Fix lands at both ends of the pipe:
	//        * CreateMailbox now `req.Email = strings.ToLower(strings
	//          .TrimSpace(req.Email))` so every NEW row is canonical.
	//        * findMailboxByEmail (new shared helper) tries an exact
	//          match first (cheap, hits the unique index) and only
	//          falls back to a case-insensitive regex on miss — so
	//          existing pre-3.1.29 mixed-case rows are still findable
	//          without rewriting the collection.
	//        * The webmail SSO heal path (syncDovecotPasswordLine,
	//          /etc/dovecot/users sed, doveadm auth test) keys off
	//          mailbox.Email from the resolved row instead of the
	//          caller-supplied address — guarantees the hash file uses
	//          the SAME case that Roundcube will pass at IMAP login.
	//        * GenerateWebmailToken's "mailbox not found" error now
	//          includes the address that was searched, so the next
	//          time this surfaces in a toast the operator can spot
	//          the typo / case drift at a glance.
	//
	//   2. RELATIVE URL IN API RESPONSE. The OpenAPI 3.1 spec
	//      (docs/api/openapi.yaml lines 659–684) advertises
	//      `{ url, expires_in }` and a URL with format `uri`. The
	//      handler returned `{ token, url }` where `url` was the bare
	//      path `/webmail/sso.php?token=…`. An external integrator
	//      calling the panel through nginx couldn't hand the URL to
	//      a browser without first scraping the panel hostname out
	//      of their own request — the API was strictly less useful
	//      than `printf` over the token. URL is now built from
	//      c.BaseURL() so it carries the request's own scheme + host
	//      + port (works through any proxy that sets the standard
	//      X-Forwarded-* headers, which Fiber's BaseURL respects).
	//
	//   3. MISSING expires_in. The token's TTL (300 s, enforced by
	//      sso.php's ts check) was a magic number with no surface in
	//      the response. Consumers who cached the URL had no programmatic
	//      way to know when to re-mint — they either guessed and got
	//      sporadic 401s, or re-minted on every click and wasted a
	//      Mongo lookup + dovecot sync per impression. Now exposed as
	//      `expires_in: 300`, matching the spec exactly.
	//
	//   4. WRONG STATUS CODE. "mailbox not found" returned 400
	//      Bad Request, indistinguishable from a malformed body in
	//      consumer logs. Now 404 Not Found, matching the resource-
	//      addressing semantic the rest of the External API uses
	//      (GET /domains/{name} 404s on miss, etc.).
	//
	// JWT-driven panel webmail (POST /api/v1/whm/email/webmail-token)
	// gets the same fixes for free — handler also lowercases the
	// inbound email and translates "mailbox not found" → 404 so the
	// in-panel toast reads correctly instead of "Internal error".
	//
	// 3.1.28 (2026-05-06) — File Manager upload cap raised from
	// 500 MB to 10 GB (per file).
	//
	// User asked for 10 GB so operators can drop in full website
	// tarballs / database dumps / video assets via the File Manager
	// without falling back to scp/sftp. The previous 500 MB cap
	// kicked any real-world hosting workload off the panel.
	//
	// Three layers needed to agree, all bumped:
	//
	//   1. Frontend client guard (FilesPage.tsx, both WHM + cpanel) —
	//      MAX_UPLOAD_BYTES = 10*1024*1024*1024. UI label + reject
	//      toast text updated. Saves the operator a slow upload that
	//      would otherwise end in a 413.
	//
	//   2. Backend Fiber BodyLimit (cmd/server/main.go) — bumped to
	//      10 GB. fasthttp streams large multipart bodies to the OS
	//      temp dir, so this doesn't pin 10 GB of RAM per upload —
	//      just disk space at /tmp.
	//
	//   3. nginx client_max_body_size (install.sh, three vhost
	//      blocks: HTTP fallback, HTTP→HTTPS redirect, the SSL
	//      vhost) — bumped to 10G. client_body_timeout +
	//      send_timeout raised from 600s → 3600s so a 10 GB upload
	//      on a 5 MB/s home connection (~33 minutes) doesn't hit
	//      the cutoff mid-stream.
	//
	// Existing-install caveat: install.sh changes only land on
	// fresh installs. Operators on a live box need to bump
	// `/etc/nginx/sites-enabled/serverpanel` from 500M to 10G and
	// reload nginx — otherwise nginx 413s before the request ever
	// reaches the backend's new BodyLimit. One-liner:
	//
	//   sudo sed -i \
	//     -e 's/client_max_body_size 500M;/client_max_body_size 10G;/' \
	//     -e 's/client_body_timeout 600s;/client_body_timeout 3600s;/' \
	//     -e 's/send_timeout 600s;/send_timeout 3600s;/' \
	//     /etc/nginx/sites-enabled/serverpanel \
	//     && sudo nginx -t && sudo nginx -s reload
	//
	// Future work: chunked / resumable uploads (tus.io) would let
	// >10 GB and unreliable-connection scenarios work without
	// raising the cap further. That's a UI rewrite of the upload
	// component, scoped out of this patch.
	//
	// 3.1.27 (2026-05-06) — Deploy Software: hoist `branch` from
	// per-service to the project level.
	//
	// User report (screenshot): every Add Service modal asked the
	// operator to type a branch, but the new shared-clone layout
	// (one .git per project, every service is a subdir) means
	// services CANNOT legitimately track different branches —
	// they share one working tree. Collecting the field per service
	// was redundant in the best case and an inconsistency footgun
	// in the worst (operator types main on service #1, master on
	// service #2 → silent confusion when Pull only fetches one).
	//
	// Branch is now a project-level field:
	//
	//   * Project model gains GitBranch (bson git_branch).
	//   * ProvisionProjectRequest + CreateProjectRequest +
	//     UpdateProjectRequest carry it.
	//   * Wizard's Basics step has a single "Branch" input next to
	//     Repository URL (default "main").
	//   * Add Service modal — Branch input removed (inherits).
	//   * Edit Service modal — Branch shown read-only with a
	//     pointer to Edit Project for changes.
	//   * Edit Project modal — Branch input added next to
	//     Repository URL.
	//
	// Backend behaviour:
	//   * Provision propagates the project-level branch to every
	//     service row so legacy reads of svc.GitBranch stay in
	//     sync; the per-row field becomes a derived mirror.
	//   * AddService inherits from project when the request omits
	//     git_branch.
	//   * Update on the project mirrors the new branch onto every
	//     service in one UpdateMany; next Pull / runDeploy on the
	//     shared clone checks out origin/<new> via inPlaceSync.
	//   * loadProject heals existing projects on first read: when
	//     Project.GitBranch is empty AND services exist, copies
	//     the FIRST service's branch (sorted by _id, deterministic)
	//     onto the project doc and persists. Operator never has to
	//     run a migration.
	//   * AddServiceRequest.GitBranch is no longer
	//     `validate:"required"` — provisioning sites either pass
	//     the propagated value, or the AddService inherit-fallback
	//     fires.
	//
	// Server transfer: GitBranch is a bson field on the Project
	// doc, which the transfer pipeline already exports + imports
	// via RemoteMongoExport(ColProjects). No transfer-side code
	// change needed; the new field travels automatically.
	//
	// 3.1.26 (2026-05-06) — Deploy Software Primary-domain picker is
	// now a searchable dropdown (both WHM + cPanel surfaces).
	//
	// User report with screenshot: the Add Service modal's "select a
	// domain" plain <select> rendered every registered domain in a
	// long scrollable list. On the production VPS the list ran 25+
	// entries deep including look-alike subdomain trees
	// (api.restro.easycrm4u.com, company.restro.easycrm4u.com, …,
	// wl-vrndor.web.restro.easycrm4u.com). Picking the right one
	// without typo'ing required scrolling + scanning, which scaled
	// badly past ~10 domains and was already painful at 25.
	//
	// Switched both PrimaryDomainSelect components (WHM
	// DeploySoftwarePage + cPanel DeploySoftwarePage) to the existing
	// SearchableSelect from @serverpanel/ui — same type-ahead picker
	// the panel already uses for vendor / mailbox dropdowns. Type
	// "wl-vrndor" → list narrows to two; pick.
	//
	// Behaviour preserved:
	//   * Empty list → same "No domains registered yet" guard.
	//   * Stored value not in the live list → still renders with a
	//     "(not registered)" hint so editing an existing service
	//     after the source domain was deleted doesn't silently
	//     wipe the field.
	//   * onChange semantics unchanged — caller still gets the
	//     domain string.
	//
	// Backend untouched. No new tests needed; the SearchableSelect
	// component itself is already covered by the existing UI
	// package's tests.
	//
	// 3.1.25 (2026-05-06) — cPanel mailbox bulk-upload template
	// shrunk to the 4-column minimum operators actually need.
	//
	// User feedback after 3.1.24: vendors uploading the cPanel email
	// bulk template still saw `email | domain | password | …` and
	// felt obligated to type the domain — even though the server
	// already derives it from the email's @part for vendor uploads
	// (since 3.1.17). The unnecessary column was operator-confusing
	// noise: when the typed `domain` cell didn't match the email's
	// @part, the parser silently preferred the email's @part anyway.
	//
	// 3.1.25's cPanel email bulk template is now exactly:
	//
	//   email | password | quota_mb | send_limit_per_hour
	//
	// Server-side derivations:
	//   * domain    ← email.split("@")[1]   (always; the typed
	//                                        `domain` cell, if
	//                                        present in legacy CSVs,
	//                                        is ignored)
	//   * tenant    ← validated via CallerScope.AssertOwnsDomain on
	//                 the derived domain — rows whose email belongs
	//                 to a domain the vendor doesn't own fail with
	//                 a clear error
	//   * user/owner ← auto-resolved from the matching Domain row's
	//                  `user` field (the vendor who owns that
	//                  domain). The mailbox lands under the right
	//                  /home/<vendor>/mail/<domain>/<localpart>
	//                  tree without the operator picking anything.
	//
	// WHM admin template unchanged — admins still pick owner per row
	// via the `user` column; the auto-create-missing-domain hook
	// (3.1.23) still keys off it.
	//
	// XLSX cPanel variant gains an inline "Domain auto-derived"
	// instruction so an Excel-savvy operator sees the rule without
	// reading API docs. The CSV variant has the same 4-column shape;
	// no inline note (CSV doesn't support row styling).
	//
	// Backward compat: legacy CSVs that still include `domain` and/or
	// `user` columns parse cleanly — the parser ignores unrecognised
	// cells and the cPanel handler force-overrides `user` regardless.
	//
	// New tests: TestMailboxTemplateCpanelShape pins the cPanel
	// variant to exactly [email, password, quota_mb,
	// send_limit_per_hour] and asserts neither 'domain' nor 'user'
	// can leak in. TestMailboxTemplateWHMShape pins the full WHM
	// variant.
	//
	// 3.1.24 (2026-05-06) — Bulk-upload templates are now surface-
	// aware: cPanel/User-Panel downloads omit the `user` column.
	//
	// User feedback after 3.1.23: vendors downloading the bulk-upload
	// XLSX template saw a `user` column and assumed they had to fill
	// it in (some left it blank, some typed their own username).
	// The backend already force-overrides the row's `user` field to
	// the authenticated caller on cPanel uploads — including the
	// column in the served template was just operator-confusing
	// noise, and an empty cell could trip up tooling that validates
	// "required" columns positionally.
	//
	// What changed:
	//   * Domain bulk template — BulkUploadCSVTemplate(omitUser bool)
	//     and BulkUploadXLSXTemplate(omitUser bool). When called from
	//     /api/v1/cpanel/domains/bulk-upload/template, the `user`
	//     column is dropped entirely. When called from
	//     /api/v1/whm/... , the WHM operator-picks-owner shape stays.
	//   * Email bulk template — same surface-aware split. Mailbox
	//     template's WHM variant keeps the `user` column for the
	//     auto-create-missing-domain hook (3.1.23); cPanel variant
	//     drops it.
	//   * Surface detection lives in the handler:
	//       omitUser := strings.HasPrefix(c.Path(), "/api/v1/cpanel/")
	//     One-line check, no frontend change required.
	//   * XLSX cPanel variant adds an inline note row clarifying the
	//     auto-assignment policy ("every uploaded domain will be
	//     assigned to your logged-in account"). Excel-savvy operators
	//     get the rationale without reading docs.
	//
	// Backward compat: the parser was already lenient about an
	// absent `user` column (cell() returns "" when the header isn't
	// in the row, and the cPanel handler clobbers user= regardless),
	// so any existing CSVs/XLSXs from older template downloads keep
	// working. Existing tests updated to pass the new bool; new
	// sub-tests pin the cPanel variant's column shape.
	//
	// 3.1.23 (2026-05-06) — Bulk Email: cpanel UI parity + WHM
	// auto-create-domain on missing-domain rows.
	//
	// Two things this lands:
	//
	//   1. Cpanel/User-Panel parity with the WHM Email page's bulk
	//      operations (backend was already there since 3.1.17;
	//      vendor UI was missing). Per-row + select-all-visible
	//      checkboxes (filter-aware), toolbar with Bulk Upload /
	//      Export / Export-w/-Passwords / Delete-N, three modals
	//      (Upload, Delete OTP, Export-w/-Password OTP). Downloads
	//      route through axios responseType=blob so the JWT
	//      attaches — same fix v3.1.18 applied to WHM.
	//
	//   2. Domain availability gate on bulk upload. New
	//      bulkResolveDomain helper runs per row before CreateMailbox:
	//
	//         caller    | domain exists | domain missing
	//         ----------+---------------+-------------------
	//         WHM owner | check ok      | auto-create when row.user
	//         cpanel    | AssertOwns    | reject (out of tenant)
	//
	//      EmailService gains an EmailDomainCreator interface +
	//      SetDomainCreator wiring (interface to avoid the
	//      DomainService<->EmailService circular import). Auto-
	//      create defaults: PHP 8.2; everything else blank so
	//      DomainService.Create's resolveServerIP / nameservers
	//      fallback fires.
	//
	//      Result shape additions: BulkMailboxRowResult.DomainCreated
	//      bool, BulkMailboxUploadResponse.DomainsCreated counter.
	//      Template gains a `user` column (synonyms: user / owner
	//      / vendor / username). Cpanel uploads ignore the column.
	//
	// Tests: TestResolveMailboxHeader extended to cover the new
	// synonyms; new TestMailboxTemplateIncludesUserColumn pins the
	// column position so future refactors can't silently drop it.
	//
	// 3.1.22 (2026-05-06) — WHM Deploy Software "Primary domain"
	// dropdown now scoped to the project's vendor.
	//
	// User reported: "One add new service → Primary domain shows
	// all domain?? Fix it only show all domain for vendor". The
	// WHM admin's Add Service / Edit Service modals were rendering
	// EVERY domain on the box across every tenant — multiple
	// unrelated vendors' rows in one dropdown — making cross-tenant
	// mistakes one click away. (The create-project wizard already
	// filtered by vendor on step 2; only the inside-project
	// add/edit paths were unfiltered.)
	//
	// Why it matters: a project's files live under
	// /home/<project.user>/projects/<slug>/. Picking a domain owned
	// by some OTHER vendor would either fail to bind (LE issuance
	// can't write to /home/<other>/) or — worse — write a vhost that
	// points at the wrong tenant's home directory. Both modes are
	// avoidable by simply not offering the cross-tenant choice in
	// the dropdown.
	//
	// Fix: AddServiceModal + EditServiceModal in
	// apps/whm/src/pages/DeploySoftwarePage now receive
	// `availableDomains.filter((d) => !project.user || d.user === project.user)`
	// — i.e. only domains owned by the current project's vendor.
	// The `!project.user` short-circuit keeps legacy projects that
	// pre-date the user-stamping refactor working with the
	// unfiltered list (defensive default).
	//
	// AddServiceModal also gained a "no domains available for
	// <vendor>" amber banner when the filtered list is empty, so
	// the operator sees WHY the dropdown is empty + where to add
	// a domain (WHM → Domains → Add Domain) instead of staring at
	// a silently empty select.
	//
	// cPanel side: no change. The /api/v1/cpanel/domains endpoint
	// is already tenant-scoped at the service layer (ListOwn calls
	// CallerScope.AssertOwnsDomain), so a vendor's Deploy Software
	// dropdown has only ever shown their own rows.
	//
	// 3.1.21 (2026-05-06) — Webmail "Login failed for X" RCA + guards.
	//
	// Live diagnostic on the production VPS uncovered the actual
	// root cause of the persistent webmail SSO failure: MariaDB had
	// been manually stopped (`Normal shutdown (initiated by:
	// unknown)` in the journal) and never restarted. Roundcube needs
	// the `roundcube.users` table to insert the post-IMAP session
	// record — when MariaDB is unreachable, login() returns false
	// EVEN THOUGH IMAP auth succeeded, and sso.php's bare
	// "Login failed for <email>" page lights up. The Dovecot heal
	// path shipped in 3.1.19/3.1.20 wasn't wrong, just unrelated.
	//
	// Two structural guards added so this can't blindside the
	// operator next time:
	//
	//   1. GenerateWebmailToken pre-flight: probe the MariaDB socket
	//      (/run/mysqld/mysqld.sock) BEFORE issuing a token destined
	//      for sso.php. When the socket is missing, return an
	//      actionable error: "webmail database (MariaDB) is
	//      unavailable — Roundcube cannot create the user session
	//      even though IMAP auth would succeed. Run `systemctl
	//      start mariadb` on the server, then retry." The operator
	//      sees this in the panel toast immediately on click,
	//      skipping 30 minutes of journalctl/Roundcube-log
	//      spelunking.
	//
	//   2. Extended /api/v1/health: legacy `GET /api/v1/health`
	//      keeps its `{status: "ok"}` shape for existing uptime
	//      probes; `GET /api/v1/health?deps=1` adds a per-dependency
	//      breakdown (mariadb / dovecot / postfix / opendkim /
	//      nginx / pdns). Whole-box status flips to "degraded"
	//      when any dep is down, ready for a WHM-dashboard banner.
	//
	// What was NOT the bug, despite chasing it: the Dovecot users
	// file (line was correct, IMAP auth succeeds in journalctl —
	// `imap-login: Login: user=...` was being logged for every
	// failed sso.php click, immediately followed by a clean
	// "Disconnected: Logged out" once Roundcube's PHP died on the
	// MySQL connect failure). v3.1.19 + 3.1.20 hardened that path
	// regardless — drift-resilient now is still better than the
	// brittle awk rewrite.
	//
	// 3.1.20 (2026-05-06) — Webmail SSO heal: harder rewrite + verify.
	//
	// v3.1.19's auto-heal had a silent-fail mode. The in-place awk
	// rewrite (`$1==E{$2=H}`) only matched lines whose first colon-
	// delimited field exactly equalled the email — so an existing
	// /etc/dovecot/users line with stray whitespace, or a row whose
	// per-user line was never written in the first place (mailbox
	// row imported via transfer with no Dovecot file copied), would
	// pass through awk unchanged. The heal logged success but the
	// underlying drift was untouched, so Roundcube's next IMAP
	// login still hit Dovecot's "User not found" / "Password
	// mismatch" path and sso.php showed the same "Login failed for
	// X" wall.
	//
	// User reported the bug for an auto-created `admin@<subdomain>`
	// mailbox — the auto-create path in domain_service.go runs
	// CreateMailbox which writes a per-user line, but the mailbox
	// in the report was old enough that its line shape may have
	// pre-dated the per-user-write convention.
	//
	// 3.1.20 fixes:
	//
	//   1. syncDovecotPasswordLine switches to UNCONDITIONAL
	//      delete-then-append. We sed-delete any matching `^email:`
	//      line, then echo a fresh known-good line — same shape
	//      CreateMailbox uses. Can't drift, can't silently miss.
	//
	//   2. After the rewrite, run `doveadm auth test <email>
	//      <plaintext>` to VERIFY Dovecot accepts the credentials
	//      against the live passdb config. Same code path
	//      Roundcube's $rcmail->login() will exercise next, so a
	//      successful test virtually guarantees the upcoming SSO
	//      login works.
	//
	//   3. If the verify fails, GenerateWebmailToken returns a
	//      specific actionable error to the panel UI ("could not
	//      establish webmail credentials: ...") INSTEAD of issuing
	//      a doomed token. Operator sees a useful toast in the
	//      panel rather than landing on /webmail/sso.php's bare
	//      "Login failed" page with no clue what to do.
	//
	// The verify step also adds a safety net for the "encrypted_pass
	// decrypts to wrong plaintext" case (JWT_SECRET rotated mid-life
	// of the mailbox). The test fails, the user sees "reset the
	// password from the Edit modal" — actionable.
	//
	// 3.1.19 (2026-05-06) — Webmail SSO "Login failed for X" auto-heal.
	//
	// User reported clicking the "Open Webmail" arrow on a freshly-
	// created mailbox and getting "Login failed for <email>" on
	// /webmail/sso.php. Trace: token decoded fine, HMAC verified,
	// timestamp valid — Roundcube's $rcmail->login() reached
	// Dovecot IMAP and Dovecot rejected the credentials.
	//
	// Root cause is drift between the AES-encrypted plaintext
	// stored in Mongo (encrypted_pass) and the SHA512-CRYPT hash
	// stored in /etc/dovecot/users. Drift sources:
	//   * mailbox row imported via server transfer with a stale
	//     /etc/dovecot/users line on the destination
	//   * partial UpdateMailbox failure that updated Mongo but not
	//     the Dovecot file (or vice versa)
	//   * manual edit of /etc/dovecot/users by an admin debugging
	//     a different issue
	//   * mailbox row whose encrypted_pass post-dates the file's
	//     line (pre-3.0.33 row that never wrote the per-user line)
	//
	// Fix: GenerateWebmailToken now treats Mongo's encrypted_pass
	// as the source of truth and AUTO-HEALS /etc/dovecot/users
	// before issuing the SSO token. New helper
	// syncDovecotPasswordLine:
	//   1. Decrypts encrypted_pass under the panel's JWT_SECRET
	//   2. Hashes the plaintext via doveadm pw -s SHA512-CRYPT
	//   3. Rewrites the matching line via awk (preserves every
	//      field after the password — uid, gid, gecos, home,
	//      shell, userdb_mail) so the maildir path stays correct
	//   4. Falls back to APPENDING a fresh line for mailbox rows
	//      whose /etc/dovecot/users entry doesn't exist yet
	//
	// Heal failure is logged but NOT fatal — token still issues so
	// the click flow is unchanged in the worst case. The cost is
	// one doveadm pw + one bash awk per webmail click; both are
	// well under 50ms on the production VPS.
	//
	// Out of scope: this patch doesn't address scenarios where the
	// Mongo encrypted_pass itself decrypts to the WRONG plaintext
	// (e.g. the JWT_SECRET rotated since the mailbox was created).
	// Those still surface a clear error: "JWT_SECRET may have
	// rotated since this mailbox was created. Re-set the password
	// from the Edit modal."
	//
	// 3.1.18 (2026-05-06) — Email Bulk Upload "Download template" and
	// the plain Export button were 401-ing because the WHM EmailPage
	// used window.open() to fetch them. window.open creates a fresh
	// browser navigation that carries no Authorization header — the
	// JWT lives in localStorage, not a cookie, and only axios's
	// interceptor attaches it. Reproduction was visible: hitting
	// /api/v1/whm/email/bulk-upload/template?format=csv directly in
	// the address bar returned `{"code":"UNAUTHORIZED"}`.
	//
	// Fix: route both downloads through axios with responseType=blob,
	// then materialise the response as an object URL and trigger a
	// synthetic <a download> click. Same pattern the WHM Domains
	// page already uses for its export + template downloads. The
	// shared helper saveBlob handles Content-Disposition filename
	// extraction with a fallback so a future backend that forgets
	// the header still produces a sensible filename.
	//
	// Backend untouched — the 401 was purely a frontend missing-
	// header bug. The blob response also unwraps server-side error
	// JSON bodies (delivered as a Blob containing {"error":{"message":…}})
	// so an OTP miss / expired-token rejection surfaces an
	// actionable toast instead of "Export failed".
	//
	// 3.1.17 (2026-05-05) — Email Bulk Operations: export with
	// OTP-gated password reveal, CSV/XLSX bulk upload with auto-
	// generated passwords, OTP-gated bulk delete.
	//
	// Three new flows on the email surface, each mirroring a pattern
	// already proven on the domain bulk surface:
	//
	//   1. Bulk Export — GET /email/export?format=csv|xlsx&ids=…|all=true
	//      returns a flat dump of email, domain, username, quota_mb,
	//      used_mb, send_limit_per_hour, created_at. When the
	//      operator passes ?token=<otp>&code=<6-digit> obtained
	//      from the new POST /email/bulk-export/request-otp flow,
	//      the export ALSO carries an AES-decrypted `password`
	//      column. Password reveal is OTP-gated because every
	//      mailbox's plaintext lands in the file in one shot — a
	//      stolen session cookie shouldn't be enough to exfiltrate.
	//
	//   2. Bulk Upload — POST /email/bulk-upload accepts CSV / XLSX
	//      with one row per mailbox. Required column: email.
	//      Optional: domain (auto-derived from email), password
	//      (BLANK = server-generated 16-char ambiguity-free random,
	//      returned in the response so the operator can hand it
	//      out), quota_mb, send_limit_per_hour. Per-row failures
	//      don't abort the loop. Tenant scope enforced — a
	//      vendor_admin uploading a CSV with a sibling tenant's
	//      mailbox can't reach across.
	//
	//      A template is downloadable at GET
	//      /email/bulk-upload/template?format=csv|xlsx with two
	//      example rows + an instructions sheet (XLSX only)
	//      explaining the blank-password rule.
	//
	//   3. Bulk Delete — POST /email/bulk-delete/request-otp emails
	//      a 6-digit code to the admin's address; POST
	//      /email/bulk-delete/confirm validates token + code and
	//      runs DeleteMailbox in a loop, returning a per-row result
	//      table. Same OTP shape as the domain bulk-delete (10-min
	//      TTL, 5-attempt cap, hard-invalidate on miss-cap, code
	//      hashed at rest). vendor_owner-gated on WHM; tenant-
	//      scoped on cpanel.
	//
	// Server-transfer compatibility: the upload/export column shape
	// is identical across panel installs, so an operator can export
	// from one box and import to another. The `password` column
	// (when included) is the plaintext — the destination panel
	// re-encrypts under its own JWT_SECRET on first save.
	//
	// New collection: bulk_mailbox_otp (separate from
	// bulk_delete_otp so the schema can carry mailbox-shaped fields
	// + a `kind` discriminator distinguishing delete vs export
	// password-reveal).
	//
	// Tests: 7 cases pin the export column order (no-password and
	// with-password variants), header text contract, header synonym
	// resolution (handles "E-Mail" → email, "PWD" → password,
	// "Send Limit" → send_limit_per_hour), generated-password
	// alphabet invariant (no 0/O/I/l/1), OTP-kind discriminator
	// distinctness, brute-force-budget invariant.
	//
	// 3.1.16 (2026-05-05) — Deep audit of zone / mail / SSL / default-
	// mailbox / WHOIS at every domain create entry point. Six bugs
	// fixed; the response shape now carries the post-create status
	// the operator needs.
	//
	// User asked: "bulk upload and domain create time, deeply check
	// zone, mail setup, default mail create — upgrade. also get and
	// store whois on domain add through api/manual/upload — check,
	// upgrade and fix all bugs."
	//
	// Bugs found and fixed:
	//
	// 1. **Bulk-upload duplicated SSL issuance.** Pre-3.1.16
	//    DomainService.Create ran SSL with retry+SANs (www.<d> +
	//    cname.<d>) AND the bulk-upload row loop then ran
	//    s.ssl.IssueLetsEncrypt a second time, single-shot, with
	//    NO additional SANs. The redundant call was wasted on the
	//    happy path and could shrink the cert's SAN list on the
	//    rare reverse-order race. Now the bulk loop trusts
	//    Create's outcome — reads SSLActive off the returned doc
	//    and runs ForceSSL on top.
	//
	// 2. **ForceSSL gating in bulk-upload only fired when the
	//    redundant SSL succeeded.** If Create's SSL succeeded but
	//    the redundant SSL hit a transient failure, force-https
	//    was skipped — leaving https:// flag-off even though the
	//    cert was on disk. Now ForceSSL keys off SSLActive on the
	//    returned domain doc, not the redundant call's outcome.
	//
	// 3. **CreateZone errors were swallowed.** apex `pdnsutil
	//    create-zone` failures left the domain row stamped active
	//    with NO DNS authority on the box; mail setup (chained
	//    inside CreateZone) never ran; LE later failed HTTP-01
	//    silently. Now the error lands in setup_warnings + a
	//    structured zerolog Error so the operator sees it in
	//    journalctl AND the bulk-upload result row.
	//
	// 4. **Mail setup errors were stderr-only.** SetupSubdomainMail
	//    (subdomain branch) and the implicit setupMailServer call
	//    inside CreateZone (apex branch) printed to stderr and
	//    proceeded. Bulk-upload happily reported "domain created"
	//    even when outbound mail would be unsigned + inbound would
	//    bounce. Now each failure path warns into the per-row
	//    setup_warnings list with a "run bzpanel heal-mail" hint.
	//
	// 5. **admin@<domain> auto-mailbox password was discarded.** The
	//    create flow generated a 16-char password, used it to create
	//    the mailbox, then dropped the variable. The mailbox
	//    existed but the operator could NEVER log in — there was
	//    no path to the password short of running `bzpanel
	//    admin-password` to reset it. Now the password is stamped
	//    on the returned Domain's bson:"-" `admin_mailbox_password`
	//    field and surfaced in the bulk-upload row + the single-
	//    create response. UI shows it as a click-to-copy block
	//    with "save it now — the panel won't show it again".
	//
	// 6. **WHOIS fields gathered by preflight were thrown away.**
	//    RunPreflight already fetched registrar / registered_on /
	//    expires_on for every domain, but Create only stamped the
	//    DNS / IP fields onto the row. Operators who left the
	//    registrar fields blank on the Add Domain form (the common
	//    case) ended up with rows that had no expiry tracking and
	//    never showed in the dashboard's "expiring soon" widget.
	//    Now whois data fills any blank registrar / dates field on
	//    every create entry point — single Add Domain modal, the
	//    programmatic API, AND the Bulk Upload flow — because all
	//    three route through this Create. Operator-provided values
	//    still WIN over whois (the form's registrar/dates aren't
	//    overwritten when populated).
	//
	// Wire: new bson:"-" fields on Domain — SetupWarnings []string
	// + AdminMailboxPassword string — surfaced via JSON. The
	// BulkRowResult struct gains parallel fields: SetupWarnings,
	// AdminMailbox, AdminMailboxPassword. The shared
	// BulkUploadDomainsModal renders a 6th "Admin Mailbox" column
	// + a secondary expandable warnings row beneath any row with
	// non-empty setup_warnings.
	//
	// Backwards-compat: the new fields are bson:"-" + json:omitempty,
	// so existing API consumers see no change unless they read the
	// new keys. The single-create response keeps its Domain shape;
	// no caller needs to update unless they want the new visibility.
	//
	// 3.1.15 (2026-05-05) — Bulk Delete domains, WHM-only and gated
	// by an email-OTP confirmation step.
	//
	// User asked: "bulk delete (only for whm login) — after delete
	// click mail verify by otp, after verify delete". Pairs with the
	// v3.1.13 row-selection + export feature so the WHM admin can
	// now: select N rows → click Delete N Selected → request a
	// 6-digit code mailed to their address → enter the code →
	// server runs Delete in a loop and returns a per-row result table.
	//
	// New collection: `bulk_delete_otp` with the on-disk shape
	// {token, user_id, email, domain_ids, domain_names, code_hash,
	//  attempts, used, created_at, expires_at}. Separate from the
	// login OTP collection because the lifecycle is different
	// (carries the target id list, expires after 10 minutes,
	// one-shot per id-set).
	//
	// New endpoints (WHM only, gated by middleware.RequireRole
	// "vendor_owner" so even staff with `domain.manage` can't
	// trigger destructive bulk operations):
	//   POST /api/v1/whm/domains/bulk-delete/request-otp
	//        body: { ids: ["...","..."] }
	//        → mails 6-digit code, returns { token, email,
	//          domain_count, domain_names, expires_at, mailer_enabled }
	//   POST /api/v1/whm/domains/bulk-delete/confirm
	//        body: { token, code }
	//        → verifies code, runs DomainService.Delete in a loop,
	//          returns { total_rows, successes, failures, items[] }
	//          (mirrors BulkUpload's row-result shape)
	//
	// Security:
	//   * 6-digit numeric code (CSPRNG, rejection-sampled to keep
	//     the digit distribution uniform). Stored as sha256 hex.
	//   * 32-byte CSPRNG hex token bound to the (admin, ids) tuple.
	//   * 5-attempt cap per request; 6th wrong code hard-invalidates
	//     the row by setting Used=true.
	//   * 10-minute TTL on the OTP request.
	//   * Code marked Used BEFORE the destructive loop runs, so a
	//     concurrent retry can't double-fire the deletes.
	//   * vendor_owner role required at the route layer; UserID on
	//     the OTP doc cross-checked at confirm so a session swap
	//     can't reuse another admin's token.
	//   * 500-row sanity cap on the request to prevent a click-mistake
	//     from chewing through 10k deletes.
	//
	// Mailer fallback: when SMTP isn't configured the code is
	// printed to stderr (`journalctl -u serverpanel`) so a fresh-
	// install operator can still complete the flow. The
	// /request-otp response carries `mailer_enabled: false` in
	// that case so the modal surfaces the journalctl hint instead
	// of waiting for an email that never arrives.
	//
	// Frontend: new BulkDeleteDomainsModal in apps/whm/src/components
	// (deliberately NOT in @serverpanel/ui — User Panel doesn't
	// expose this surface; cPanel keeps its per-row trash icon as
	// the only delete path). Three-step UX: review → verify → result.
	// "Delete N Selected" button only shows when ≥1 row is checked,
	// in red so it's visually distinct from Bulk Upload / Add Domain.
	//
	// New tests in domain_bulk_delete_test.go:
	//   * generateBulkDeleteCode shape (6 digits) + uniqueness
	//     (10k draws, ≤200 collisions absorbs CI variance without
	//     hiding a biased RNG)
	//   * generateBulkDeleteToken shape (64 hex) + uniqueness (1000
	//     draws, ZERO collisions allowed — 32-byte entropy)
	//   * sha256 hex hash determinism + collision sanity
	//   * OTP email body shape: subject contains code, plaintext
	//     contains code + domain count + "+N more" suffix at the
	//     boundary, HTML body contains the code in monospace block
	//   * HTML escape pass: hostile names from Mongo's user.name
	//     field render escaped (no live <script> in the body)
	//
	// 3.1.14 (2026-05-05) — Hierarchical domain ordering: main domain
	// first, then its subdomains grouped immediately underneath.
	//
	// User asked: "at first, main domain then sub-domain — deeply
	// check and download". The exported CSV/XLSX (v3.1.13) and the
	// on-screen Domains table previously showed creation-time order
	// (mongo's `created_at desc`), so a child created later appeared
	// ABOVE its parent and a multi-tenant operator scrolling through
	// 50+ domains had to mentally re-group rows by zone.
	//
	// New behaviour: every domain list — WHM Domains table, cPanel
	// My Domains table, and the bulk-export CSV/XLSX — is sorted
	// by REVERSE-LABEL key. `app.example.com` becomes `com.example.app`
	// for comparison; a regular string sort over reversed keys
	// naturally clusters by zone with the apex (`com.example`) sorting
	// BEFORE its longer-suffix children (`com.example.app`,
	// `com.example.api`). Multi-level subdomains slot in under the
	// nearest registered parent in the same pass — no special-casing.
	//
	// Example output for a mixed list:
	//   another.com
	//   shop.another.com
	//   example.com
	//   api.example.com
	//   app.example.com
	//   users.example.com
	//   api.abc.users.example.com
	//
	// Implementation: SortDomainsHierarchical(in []models.Domain) and
	// SortExportableDomainsHierarchical(in []ExportableDomain) both use
	// sort.SliceStable so two rows that hash to the same key
	// (shouldn't happen — Mongo's domain index is unique — but
	// defensive) keep their pre-sort order. Mongo's CountDocuments
	// path is unchanged (the count is order-independent); the in-
	// memory re-sort runs on the result slice only.
	//
	// New tests: TestReverseLabelKey covers the comparison-key shape
	// (apex / subdomain / multi-level / case + whitespace tolerance);
	// TestSortDomainsHierarchical_ApexBeforeChildren is the headline
	// regression guard for the user-reported behaviour;
	// TestSortDomainsHierarchical_StableForDuplicates pins stability;
	// TestSortExportableDomainsHierarchical mirrors the contract on
	// the export shape so the two slices can't drift apart;
	// TestDomainLessHierarchical_TLDClustering covers the cross-TLD
	// case (.com rows cluster together, .in rows together).
	//
	// 3.1.13 (2026-05-05) — Domains page row selection + Export to
	// CSV / Excel.
	//
	// User asked: "select domain and export as excel/csv, add select
	// all". Pairs with the v3.1.9 Bulk Upload feature so the Domains
	// page now does both halves of the round-trip — operators can
	// select rows (or check Select All), click Export CSV / Export
	// Excel, edit in their spreadsheet, and re-import via Bulk Upload.
	//
	// New endpoints (mirrored on /whm and /cpanel):
	//   GET /domains/export?format=csv|xlsx&ids=<csv>&all=true
	//
	// `all=true` is a separate flag (not "ids empty means all") so a
	// JS bug that forgets to send the selection list can't accidentally
	// dump every tenant's domains. Empty ids + all=false → empty file.
	// On cPanel, FetchDomainsForExport applies CallerScope.AssertOwnsDomain
	// to every row even when all=true, so a vendor can only export
	// their own domains.
	//
	// Column shape matches the bulk-upload template byte-for-byte
	// (domain, user, php_version, disk_quota_mb, …, registrar,
	// registered_on, expires_on, auto_renew) plus two read-only review
	// columns at the end (ssl_active, status). The bulk-upload parser
	// silently ignores unknown columns, so re-uploading the unedited
	// export is a no-op — round-trip clean.
	//
	// Frontend: Domains table grows a leading checkbox column on both
	// WHM and User Panel surfaces. The header checkbox is a tri-state
	// "Select All Visible" — checked when every filtered row is
	// selected, indeterminate when only some are, unchecked otherwise.
	// New "Export CSV" / "Export Excel" buttons next to "Bulk Upload"
	// adapt their label to show the selection count
	// ("Export 12 (CSV)") so an operator can't mistake an all-export
	// for a selected-export. Selection clears on every fetchDomains
	// (post-add / post-delete / post-bulk-upload) so a stale id never
	// gets sent to the export endpoint.
	//
	// Shared: Column<T>.header in @serverpanel/ui Table widened from
	// `string` to `React.ReactNode` so the Select All checkbox can
	// render in the column heading. All existing string-header callers
	// stay unchanged.
	//
	// 3.1.12 (2026-05-05) — `bzpanel heal-www` one-shot heal for every
	// pre-3.1.11 domain on the box.
	//
	// User reported: "www not work for all other domain — how to
	// work?" after the v3.1.11 template fix landed but existing
	// installs still had the old vhost files + old certs (the v3.1.11
	// fix only affects NEW deploys). Manually running certbot +
	// editing nginx per domain doesn't scale past the second domain.
	//
	// New `bzpanel heal-www` (alias `repair-www`, bsp menu option 13)
	// walks every domain in the panel and:
	//
	//   1. Reads /etc/nginx/sites-available/<d>, scans every
	//      `server_name <d> ...;` line, sed-style adds `www.<d>` and
	//      `cname.<d>` if missing. Preserves indentation, all other
	//      operator-added aliases, and the trailing semicolon. Writes
	//      back only when something changed.
	//   2. If a Let's Encrypt cert exists for the domain, parses its
	//      current SAN list (`openssl x509 -text`). When `www.<d>` or
	//      `cname.<d>` is missing, runs `certbot certonly
	//      --force-renewal --webroot --cert-name <d> -d <d> -d
	//      <existing SANs...> -d www.<d> -d cname.<d>` so the new
	//      cert covers everything the old one did PLUS the missing
	//      names. Wildcard certs are skipped (their *.X SAN already
	//      covers the names).
	//   3. nginx -t once at the end + systemctl reload nginx if the
	//      test passed. nginx -t failure is reported, not catastrophic
	//      — the new vhost files stay in place, only the live reload
	//      is skipped.
	//
	// Idempotent: a re-run on an already-healed box prints
	// "every domain already covers www + cname — nothing to do".
	//
	// Skipped per-domain reasons surfaced in the summary so the
	// operator knows why a row was passed over: suspended (vhost is
	// the placeholder, separate concern), no-vhost-file (domain
	// row but no nginx config — orphan), wildcard-cert (already
	// covered by *.X). Per-domain failures don't abort the loop.
	//
	// One-command upgrade for an entire box stuck on pre-3.1.11
	// vhosts:
	//
	//   bzpanel deploy && bzpanel heal-www
	//
	// 3.1.11 (2026-05-05) — Deploy Software / reverse-proxy / static
	// vhost templates now cover `www.<domain>` + `cname.<domain>`,
	// fixing live-customer breakage where `https://www.<X>` returned
	// the panel's catch-all cert + 404 even though `https://<X>`
	// worked perfectly.
	//
	// User reported: "https://konsultkaro.com works but
	// https://www.konsultkaro.com doesn't — deeply check, how to
	// upgrade?". Public probe confirmed:
	//   * DNS for www CNAMEs to apex which resolves correctly
	//   * apex cert SAN list contains ONLY DNS:konsultkaro.com
	//     (no www, no cname)
	//   * SNI=www.<X> hands back the panel's own cert
	//     (panel.betazeninfotech.com) — meaning nginx had no
	//     server_name match for the www host and fell through to
	//     the catch-all default vhost
	//
	// The PHP-FPM templates (`vhostTemplate` / `vhostSSLTemplate`)
	// have always covered www. The bug lived in three OTHER vhost
	// builders that DIDN'T:
	//
	//   1. `reverseProxyTemplate` / `reverseProxySSLTemplate` (Deploy
	//      Software for Next.js / Node / Go reverse-proxied services)
	//      had `server_name {{.Domain}};` — bare apex only.
	//   2. `CreateStaticVhost` / `CreateStaticVhostWithSSL` (Deploy
	//      Software for static frontends — React, Vite, plain HTML)
	//      had `server_name %s;` — bare apex only.
	//   3. The Deploy Software SSL issuance via
	//      `IssueLetsEncryptMulti(primary, spec.Aliases, email)`
	//      ran with operator-supplied aliases only. www.<primary>
	//      was never automatically included, so the cert's SAN list
	//      was just the primary even when the operator deployed
	//      multiple domains.
	//
	// All three templates now cover `<d> www.<d> cname.<d>` and
	// `buildMergedVhostSpec` injects www + cname into `spec.Aliases`
	// implicitly — so on every fresh Deploy Software run, the cert
	// gets the right SAN set AND the vhost's server_name lines
	// match. Same shape the PHP-FPM template has always had + the
	// v3.1.10 cname alias bolted on.
	//
	// Heal path for existing domains: `SSLService.Reissue` (v3.1.8)
	// already had a "preserve existing SANs on reissue" guarantee.
	// Now it also ENSURES `www.<d>` + `cname.<d>` are present in
	// the additional_domains list, de-duped against operator-added
	// aliases. So a single click on the WHM/cPanel SSL Reissue
	// button heals any older domain to v3.1.11's SAN baseline
	// without the operator having to remember the alias names.
	// (Skipped for wildcards — their *.X SAN already covers it.)
	//
	// Upgrade instructions for `konsultkaro.com` (and any other
	// pre-3.1.11 Deploy Software domain with the same shape):
	//   1. Deploy this version (`bzpanel deploy`)
	//   2. Either: WHM → SSL → Reissue on the affected domain, OR
	//      Deploy Software → Redeploy on the affected app (which
	//      re-runs CreateProjectVhost + IssueLetsEncryptMulti
	//      against the fixed template + auto-aliases path)
	//   3. Verify with
	//      `openssl s_client -connect www.<d>:443 -servername www.<d> </dev/null | openssl x509 -noout -text | grep -A1 'Subject Alternative Name'`
	//      — the SAN list should now show `DNS:<d>, DNS:www.<d>,
	//      DNS:cname.<d>`.
	//
	// 3.1.10 (2026-05-03) — `cname.<domain>` flat alias auto-created
	// at every domain create entry point (apex, subdomain, multi-level
	// subdomain) and across every surface that creates domains
	// (WHM manual, User Panel manual, programmatic API token, Bulk
	// Upload CSV/XLSX).
	//
	// Why: third-party services (Vercel / Netlify / SaaS verifications,
	// "vanity URL" templates, dozens of others) routinely ask the
	// operator to "add a CNAME record at cname.<yourdomain> pointing
	// to <yourdomain>". Pre-3.1.10 every such request was a manual
	// DNS edit by hand on the WHM Records page — every install support
	// ticket that mentioned third-party CNAME verification was triaged
	// the same way. Now it lands at create time so the alias is ready
	// the moment the apex / subdomain comes up.
	//
	// What's added at create time, on every entry point:
	//   * apex `cname` CNAME → <apex>. (DNSService.CreateZone +
	//     agent.CreateDNSZone keep Mongo + pdnsutil in sync)
	//   * subdomain `cname.<subPart>` CNAME → <full>. — multi-level
	//     handled naturally by the existing subPart machinery: for
	//     `api.abc.users.X` (subPart=`api.abc.users`, parent=X), the
	//     record lands as `cname.api.abc.users` in the apex zone
	//     pointing at `api.abc.users.X.`
	//   * `cname.<domain>` added to the nginx vhost server_name list
	//     (HTTP + SSL templates + suspended placeholder) so HTTP-01
	//     challenges and browser visits don't fall through to the
	//     panel's catch-all default vhost
	//   * `cname.<domain>` added to the Let's Encrypt SAN list so
	//     `https://cname.<domain>` returns the right cert (no name
	//     mismatch). Same retry-with-backoff already covers DNS
	//     propagation for the new SAN.
	//
	// Single source of truth: every create path (manual on either
	// surface, API token, bulk upload) routes through
	// DomainService.Create — one patch covers all four.
	//
	// Domain.Delete extended to sweep the new `cname.<sub>` record
	// alongside the v3.0.41 cleanup set (subPart, www.subPart,
	// _dmarc.subPart, mail._domainkey.subPart) so re-creating a
	// subdomain doesn't see a leftover CNAME in the apex zone.
	//
	// `bzpanel heal-dns` extended to backfill `cname.<sub>` on
	// existing subdomain installs — domains created before 3.1.10
	// won't have the record by default; running `bzpanel heal-dns`
	// adds it idempotently. New `cname CNAMEs added` summary line
	// distinguishes the heal counter from the existing www-CNAME
	// counter.
	//
	// Apex domains created before 3.1.10 keep their original record
	// set; operators can add `cname` CNAME manually via WHM → DNS
	// Records, or wait for a future heal-dns extension to apex
	// zones. Behaviour for newly-created apexes is the desired
	// default.
	//
	// 3.1.9 (2026-05-03) — Bulk Upload Domains. New "Bulk Upload"
	// button next to "Add Domain" on both WHM and User Panel surfaces;
	// accepts CSV or XLSX with one row per domain, runs each through
	// the same DomainService.Create + Let's Encrypt + force-HTTPS
	// pipeline the single-domain form uses. Per-row failures don't
	// abort the loop — the response carries a result table the UI
	// renders with row number / domain / owner / success-or-error /
	// SSL outcome (issued / force-https / skipped) so the operator
	// can fix bad rows in the source spreadsheet without re-uploading
	// the good ones.
	//
	// On WHM the per-row `user` cell IS honored (platform-owner picks
	// any vendor). On the User Panel the cell is IGNORED — every row
	// is created under the authenticated caller's username so a
	// vendor can't reach outside their tenant via a doctored CSV.
	// Same scoping the single-create CPanelCreate already enforces.
	//
	// Header rows match case-insensitively across snake_case,
	// kebab-case, "Title Case", and concatenated forms — operators
	// editing in Excel/Google Sheets type "Domain Name" / "PHP Version"
	// without thinking and the parser still resolves them to the
	// canonical CreateDomainRequest fields. Trailing-blank rows from
	// Excel exports are skipped silently (not surfaced as "domain is
	// required" failures, which would drown out real errors in the
	// row-results table).
	//
	// New endpoints (mirrored on /whm and /cpanel):
	//   GET  /domains/bulk-upload/template?format=csv|xlsx
	//   POST /domains/bulk-upload (multipart: file, issue_ssl,
	//        force_ssl, php_default)
	//
	// File cap: 10 MB. xlsx parsed via xuri/excelize (pure-Go, no CGO).
	// CSV + XLSX templates are GENERATED FROM CODE — kept in lockstep
	// with CreateDomainRequest so a future struct field is one edit
	// in domain_bulk_service.go, not a forgotten static asset.
	//
	// SSL pass: best-effort per row. A row's domain is created even
	// when its Let's Encrypt issuance fails (DNS may not have
	// propagated yet on a brand-new registration, which would 404
	// the HTTP-01 challenge). The row result records that the SSL
	// step was skipped + why so the operator can re-issue from the
	// SSL page once `dig @1.1.1.1` resolves the new A record.
	//
	// Shared `BulkUploadDomainsModal` lives in `@serverpanel/ui` so
	// both apps share the file picker / template-download / result-
	// table UX. Caller passes submit + downloadTemplate callbacks
	// (network calls happen in the page, not the modal) — same
	// callback shape as the existing BulkTTLModal.
	//
	// New tests: TestNormaliseHeader / TestResolveHeader_* /
	// TestRowAllBlank / TestParseBool / TestAtoiSafe lock in the
	// header-aliasing + cell-coercion contract. TestBulkUploadCSV_*
	// and TestBulkUploadXLSX_HeaderParsing assert the full parser
	// path without needing a mongo (header-only files exercise the
	// routing + column-index map, validator failures cover the
	// validator→Create boundary, missing-domain-column surfaces a
	// helpful error not a silent zero-row response).
	//
	// 3.1.8 (2026-05-01) — SSL Reissue: force a fresh Let's Encrypt
	// certificate even when the current one isn't near expiry.
	//
	// Pre-3.1.8 there was no clean way to mint a new cert for a domain
	// the panel already had on disk. The "Issue Certificate" modal,
	// when run on already-SSL'd domains (Active SSL tab), short-
	// circuited inside SSLService.IssueLetsEncrypt — no certbot run,
	// no new cert. Operators who needed a fresh cert NOW (private key
	// exposure, broken SAN expansion, post-transfer cleanup) had to
	// manually delete the cert first then re-issue. The new Reissue
	// path eliminates that detour and adds the missing UI surface.
	//
	// What's new:
	//   * `agent.IssueLetsEncryptForced` — `certbot certonly
	//     --force-renewal --webroot ...` wrapper. Works for both fresh
	//     and existing-cert domains; new function (not a parameter
	//     change) so all existing callers keep their semantics.
	//   * `IssueLetsEncryptRequest.Reissue` and
	//     `IssueLetsEncryptBulkRequest.Reissue` — back-compat default
	//     false. Bulk response gains `issued`/`reissued` counters and
	//     each `items[].action` is "issued" or "reissued" so the UI
	//     can render a clean breakdown.
	//   * `SSLService.Reissue(ctx, domain)` — per-row entry point.
	//     Wraps IssueLetsEncrypt with Reissue=true, preserves the
	//     existing cert's wildcard + SAN list so the new lineage
	//     matches the old surface.
	//   * Mongo write switched from InsertOne to upsert with
	//     $setOnInsert on created_at — required because the
	//     ssl_certificates collection has a unique index on `domain`
	//     and a second InsertOne on reissue would fail with E11000.
	//   * Routes: POST /ssl/:domain/reissue on both WHM and cPanel
	//     (tenant scope enforced via service-layer AssertOwnsDomain).
	//   * Frontend: per-row "Reissue" button (RotateCw icon) on both
	//     SSL pages, plus a "Force reissue" toggle in the bulk modal
	//     that defaults to true. Modal title and primary button now
	//     read "Issue / Reissue Certificate".
	//
	// Side-effects on reissue match a fresh issue: nginx vhost
	// upgraded, mail-SSL retriggered async, ssl.issued webhook fired,
	// vendor notification sent.
	//
	// Tests: 4 contract tests pin the wire-shape default (Reissue
	// defaults to false on legacy clients), the issued+reissued sum
	// equals success, and the per-item Action field round-trips.
	//
	// 3.1.7 (2026-05-01) — Bootstrap TTLs lowered for fresh domains.
	//
	// Policy: every record the panel auto-creates when a brand-new
	// domain enters the system uses a deliberately short TTL so the
	// operator can re-point things in the first hours without being
	// trapped by resolver caches.
	//
	//   A / AAAA  → 30 seconds
	//   everything else (CNAME, NS, MX, TXT, SPF, DKIM, DMARC, …) → 60 seconds
	//
	// Pre-3.1.7 the bootstrap path used the historic 60s for A and
	// 3600s (1 hour) for everything else. That meant a freshly-added
	// domain whose mail server, IP, or relay needed re-pointing in
	// the first hour was stuck waiting out the cache before
	// third-party resolvers picked up the fix. Now the operator
	// edits land in resolver caches within a minute.
	//
	// Once the domain has settled (typically a few hours / a day),
	// the operator runs WHM → DNS → Bulk TTL update to lift TTLs
	// to a longer cache duration for stability.
	//
	// Sites updated:
	//   * services/dns_service.go::CreateZone — primary domain A,
	//     www CNAME, NS records.
	//   * services/dns_service.go::setupMailServer — mail A, MX,
	//     SPF TXT, DMARC TXT, DKIM TXT (both PowerDNS and Mongo
	//     paths).
	//   * services/dns_service.go::SetupSubdomainMail — subdomain
	//     MX, SPF, DMARC, DKIM (both PowerDNS and Mongo paths).
	//   * services/domain_service.go — subdomain Add Domain path:
	//     A + www.<sub> CNAME records on the parent zone.
	//   * agent/dns.go::CreateDNSZone — PowerDNS-side @ A, www
	//     CNAME, NS, SOA at the bootstrap TTL.
	//
	// Out of scope: existing domains keep whatever TTLs they have;
	// only fresh creates get the new policy. Re-IP / migration
	// paths in config_service.go intentionally untouched (they
	// retune existing zones, not bootstrap fresh ones).
	//
	// New helper: services.bootstrapTTLFor(rtype) — distinct from
	// the existing defaultTTLFor(rtype) which still returns 60/3600
	// for the inline-add form picker (operator manually adding a
	// record mid-life of a domain has different cache costs than a
	// fresh-domain bootstrap).
	//
	// Tests: 10-case TestBootstrapTTLFor_Policy locks in the policy
	// and asserts bootstrap TTLs are STRICTLY shorter than
	// defaultTTLFor for the form-picker case so a future flip can't
	// silently break the workflow.
	//
	// 3.1.6 (2026-05-01) — Full API reference doc.
	//
	// Adds docs/postman/API-Reference.md alongside the Postman
	// collection: every endpoint, every input field with type +
	// required + constraint + default, every output field with type +
	// description, every error code with its trigger condition.
	// Webhook payload contract documented per-event with a Node.js
	// HMAC-SHA256 verification example. Generated against the live
	// 3.1.6 model definitions so the doc and code stay in sync — when
	// the AllAPITokenScopes / AllWebhookEvents catalogues change, the
	// reference doc is the single place to update outside Go.
	//
	// 3.1.5 (2026-05-01) — Bulk TTL update. New "Bulk TTL update"
	// modal on both the WHM and User Panel DNS Zones pages: operator
	// picks one or more record types (A, AAAA, CNAME, MX, TXT, NS,
	// SRV, PTR, CAA, …), enters a TTL between 30 sec and 1 week, and
	// the backend retunes every matching record across every zone
	// the caller can see in one round-trip.
	//
	// Vendor scoping comes for free from the existing CallerScope:
	// vendor_owner sweeps every zone on the box; tenant-scoped roles
	// (vendor_admin / vendor_staff) only sweep their own domains.
	// Same handler serves both surfaces — no policy duplication.
	//
	// Implementation:
	//   * DNSService.BulkUpdateTTL iterates ListZones (already scoped),
	//     filters records by type set, runs ONE Mongo UpdateMany per
	//     zone, then calls reconcileRRSet for each affected (name,type)
	//     to push the change to PowerDNS.
	//   * SOA is rejected at the validation gate — its TTL is the
	//     zone's negative-cache duration (RFC 2308 §5) and shouldn't
	//     be mass-edited along with operator content.
	//   * Per-zone failure model: a stuck PowerDNS for one domain
	//     records its error against just that zone and the rest of
	//     the sweep proceeds.
	//   * Idempotent — running the same TTL twice is a no-op on the
	//     second call (Mongo matches nothing, reconcile sees no
	//     drift). Safe to retry.
	//
	// Routes:
	//   POST /api/v1/whm/dns/bulk-ttl    (gated on dns.manage)
	//   POST /api/v1/cpanel/dns/bulk-ttl (tenant-scoped via service)
	//
	// Tests: 11 cases pinning the validation gate (empty types,
	// whitespace types, SOA rejection, unknown-type rejection, TTL
	// bounds at 0 / negative / 30 / 604800 / 1 year, case-insensitive
	// type normalisation, allowlist invariants).
	//
	// Frontend: shared BulkTTLModal component in @serverpanel/ui keeps
	// the picker + TTL form identical on both panels — only the API
	// path and the scope-label string differ. Modal renders a per-zone
	// result table after submit (domain, # records updated, # rrsets
	// reconciled, status) so the operator can see at a glance which
	// zones got which updates.
	//
	// 3.1.4 (2026-05-01) — Postman collection + quick-start docs.
	//
	// Ships docs/postman/Betazen-Server-Panel.postman_collection.json
	// (36 requests across 7 folders, 25 collection variables) and
	// docs/postman/README.md. Test scripts auto-capture access_token /
	// api_token / webhook_signing_secret / mailbox_id across requests
	// so the operator runs the entire flow end-to-end without manual
	// paste — Login → Create Token → Create Domain → Issue SSL →
	// Create Mailbox → Generate Webmail Link → Link to Deploy Service
	// chains via collection variables.
	//
	// 3.1.3 (2026-05-01) — Deploy Software GitHub PAT no longer goes
	// missing on server transfer when the source's APP_ENCRYPTION_KEY
	// can't be read by a single grep.
	//
	// Pre-3.1.3 fetchSourceEncKey ran ONE command:
	//   grep '^APP_ENCRYPTION_KEY=' /opt/serverpanel/.env
	// `/opt/serverpanel/.env` is mode 600, owned by root. If the operator
	// configured the transfer wizard with a non-root SSH user (a wheel-
	// group account, a deploy user, anything sudo'd), the grep silently
	// returned empty — every project's PAT was then dropped on the
	// destination and `git pull` / auto-deploy broke for every Deploy
	// Software service post-cutover. Same shape silently broke webhook
	// signing-secret re-encryption.
	//
	// Now: fetchSourceEncKey probes four sources in one round-trip and
	// surfaces WHICH one succeeded (or all-empty) so operator-facing
	// logs explain the failure mode:
	//   1. /opt/serverpanel/.env (primary)
	//   2. /opt/serverpanel/backend/.env (legacy split layout)
	//   3. sudo -n cat /opt/serverpanel/.env (wheel-group SSH user)
	//   4. /proc/<panel-pid>/environ (running process holds the key
	//      even when .env was rotated post-boot)
	//
	// Plus three companion fixes:
	//
	//   * syncProjectsForTransfer dedup-skip on (slug, user) used to
	//     `continue` without ever touching an existing destination row.
	//     Re-running the transfer wizard after fixing source-side perms
	//     therefore couldn't recover a PAT-less destination project —
	//     operator had to delete + re-create. Now: when the existing
	//     destination row has no PAT and the freshly-grepped source key
	//     successfully re-encrypts the cipher, we backfill the row with
	//     the new cipher + masked preview. Counted in `patHealed`.
	//   * The "source key unreadable" warn log is now emitted from BOTH
	//     the projects path and the webhooks path (previously only the
	//     webhooks path warned). Includes the probe-tag suffix
	//     (`miss`/`miss-primary`/`ssh-error`/...) so the operator can
	//     pick the right fix without spelunking through service logs.
	//   * Pre-existing format-string bug in repointSourceDNSToDestination's
	//     bash heredoc (`%.$zone` was being parsed by fmt as a verb)
	//     fixed to `%%.$zone`. Caught by `go vet` while running the new
	//     PAT round-trip tests; harmless at runtime (fmt emitted
	//     `%!.($)` which bash treated as a non-matching parameter
	//     expansion suffix), but blocked the test suite.
	//
	// New tests in project_pat_reencrypt_test.go pin the contract:
	//   * Round-trip: ENCRYPT under src key → ReencryptPATForTransfer
	//     with src key string → DECRYPT under dst key returns the same
	//     plaintext (asserts both keys actually rotate the seal envelope
	//     and the destination key alone reads the new cipher).
	//   * Wrong source key → returns error, NOT silently-garbage cipher.
	//   * Empty inputs → (nil, nil) fast path so callers can skip without
	//     surfacing a spurious warning.
	//   * Destination key unset → refuses to encrypt (catches the boot-
	//     order regression where SetProjectService landed before
	//     APP_ENCRYPTION_KEY loaded).
	//
	// 3.0.44 (2026-04-29) — Home Page logo + favicon were silently
	// stripped by html/template's URL safety guard.
	//
	// Caught while writing end-to-end render tests for the GET / handler:
	// any data: URL passed to <img src=> or <link href=> was being
	// rewritten by Go's html/template to the placeholder "#ZgotmplZ"
	// (its safety stub for "I refuse to emit this"). BrandingService
	// stores logo + favicon as `data:image/...;base64,...` URLs, so
	// every operator who had uploaded a logo would have seen a broken
	// home page — no logo on the topbar, no favicon in the tab.
	//
	// Fix: wrap the data URLs in template.URL at the boundary so
	// html/template trusts them. The values are still validated upstream
	// (BrandingService.validateDataURL: image/* MIME, base64-encoded,
	// ≤256 KB), so the trust is justified at the upload handler, not
	// at the template.
	//
	// New tests that locked this in:
	//   * Five end-to-end /-handler tests covering preview/no-preview x
	//     enabled/disabled and the show_whm_login=false visibility flag.
	//     The logo+favicon assertion in the "enabled, public visit"
	//     test was what surfaced this bug.
	//   * Service-layer Save validation: empty-hero rejection when
	//     enabled, length-cap rejection for every operator-supplied
	//     field, default-payload invariants (Enabled=false on fresh
	//     install, login labels non-empty).
	//
	// 3.0.43 (2026-04-29) — Home Page preview path + draft banner.
	//
	// User reported "home page auto redirect /whm" after enabling the
	// page in 3.0.42. Two ergonomic gaps caused this:
	//
	//   1. The Preview link opened "/" in a new tab, which inherits
	//      session state. A logged-in vendor_owner navigating to /
	//      always hit the role redirect — they could never see their
	//      own home page without an incognito window.
	//
	//   2. If the operator toggled "Enable" in the form but didn't
	//      hit Save, the page on disk stayed disabled, so / kept
	//      bouncing to /whm/login — and there was no UI signal to
	//      tell them whether the page was actually live.
	//
	// Fixes:
	//
	//   * GET /?preview=1 ALWAYS renders the home page, regardless of
	//     auth role and the enabled flag. Used by the WHM admin's
	//     Preview link so an owner can review draft + published pages
	//     from inside the panel.
	//   * Preview responses set Cache-Control: no-store so an admin
	//     iterating on the form sees their last save immediately.
	//   * The renderer surfaces an orange "DRAFT — not yet published"
	//     banner whenever the page is shown via ?preview=1 with
	//     enabled=false, so the admin can't mistake a preview for the
	//     live state.
	//   * The WHM Home Page card grows a colour-coded "Published" /
	//     "Draft" status pill that explains in one line whether
	//     visitors at / will see the page right now.
	//
	// New tests: HTML-escaping coverage on the template (locks in that
	// operator-supplied HeroTitle / body paragraphs cannot inject
	// <script> tags), plus the draft-banner conditional render.
	//
	// 3.0.42 (2026-04-29) — Public Home Page at GET /, manageable from
	// WHM → Server Settings → Home Page.
	//
	// Pre-3.0.42, GET / was a role-based redirect only: authenticated
	// owners → /whm/, vendors/customers → /user-panel/, anyone else
	// (no JWT cookie / fresh visitor) bounced straight to /whm/login.
	// That last case was a UX cliff — operators selling hosting wanted
	// a brandable landing page where prospective vendors could read
	// "what is this" before being asked for credentials.
	//
	// New behaviour:
	//   * Authenticated visitors keep redirecting (unchanged).
	//   * Unauthenticated visitors get a server-rendered home page
	//     when the operator has enabled it. Disabled by default —
	//     fresh installs keep the old "/ → /whm/login" path until
	//     someone opts in.
	//
	// Render path is server-side (html/template, no SPA bundle) so
	// the page loads under 30ms with one DB read for the home_page
	// singleton + one for branding. Body text is operator-supplied
	// plain text; html/template auto-escapes everything, so a paste
	// of `<script>alert(1)</script>` lands as visible text on the
	// page, not an exec.
	//
	// Editing surface: new "Home Page" card on Server Settings.
	// Fields: enable toggle, hero title + subtitle, body (8 KB cap,
	// blank-line-separated paragraphs), vendor/admin login button
	// labels, show-admin-button toggle, footer text, support email.
	// Logo + favicon are reused from the existing Branding card so
	// the home page stays in lockstep with the rest of the panel.
	//
	// Backend storage: server_config singleton at `_id: "home_page"`,
	// parallel to the existing `_id: "branding"`. Public read at
	// /api/v1/home-page (parity with /api/v1/branding for a future
	// preview tab); admin GET/PUT at /api/v1/whm/config/home-page
	// gated on `server.manage`.
	//
	// 3.0.41 (2026-04-29) — Domain.Delete on a subdomain now cleans
	// up MX, SPF, DMARC, DKIM records too — not just A + www CNAME.
	//
	// Live test surface: created a 3-level subdomain hierarchy
	// (users / abc.users / api.abc.users.konsultkaro.in) on
	// production via the WHM API, confirmed every level wrote the
	// correct A record at the correct multi-label name in the
	// apex zone. Cleanup via DELETE returned 200 but mongo and
	// pdnsutil both still showed 9 leftover records: A and CNAME
	// were removed, but MX, TXT (SPF), TXT _dmarc.<sub>, and the
	// occasional DKIM TXT mail._domainkey.<sub> stayed behind.
	//
	// Cause: the Delete loop in domain_service.go only matched
	// `r.Type == "A" && r.Name == subPart` and the www CNAME.
	// SetupSubdomainMail writes the rest, and Delete didn't know
	// about them — orphans accumulated on every subdomain
	// recreate-then-delete cycle, with stale SPF (and stale
	// MX) on a server that had been transferred to a new IP.
	//
	// Fix: delete every record whose name matches one of
	//   subPart, www.subPart, _dmarc.subPart, mail._domainkey.subPart
	// regardless of type. Deliberately matches by EXACT name —
	// a "starts with subPart" rule would over-delete deeper
	// subdomain records (deleting `users.example.com` shouldn't
	// nuke `api.users.example.com`'s A record).
	//
	// 3.0.40 (2026-04-29) — Mail SSL fully automatic on fresh install
	// + on every "Add Domain". User asked: will it Just Work without
	// the operator running `bzpanel mail-ssl <domain>` per site?
	// Now: yes, eventually (within an hour of public DNS propagating).
	//
	// Three coordinated pieces:
	//
	//   1. New `bzpanel mail-ssl-sweep` command. Walks every panel-
	//      tracked domain; for each, runs cmdMailSSL (idempotent on
	//      already-wired sites via the v3.0.39 fast-path). Newly-
	//      eligible domains get their cert + SNI wiring on this
	//      pass; domains whose public DNS isn't ready yet are
	//      classified as "DNS not ready" and skipped — they'll be
	//      caught on a future sweep. Reports a tally so cron logs
	//      stay readable.
	//
	//   2. SSLService.IssueLetsEncrypt now spawns a background
	//      goroutine that shells out to `bzpanel mail-ssl <domain>`
	//      after the web cert lands. Detached context (2 min
	//      timeout) so the API response that triggered the issue
	//      isn't blocked. On fresh install, the call usually fails
	//      the DNS pre-flight (public DNS hasn't propagated the
	//      newly-added mail.<domain> A record yet) — that's fine,
	//      logged informationally, and the hourly sweep cron
	//      catches up.
	//
	//   3. install.sh writes /etc/cron.d/serverpanel-mail-ssl-sweep
	//      with an hourly run at minute 17. Logs go to
	//      /var/log/serverpanel-mail-ssl-sweep.log. Idempotent
	//      every hour; once a domain's public DNS for
	//      mail.<domain> aligns, the next sweep wires it within
	//      the hour.
	//
	// Operator experience after v3.0.40:
	//   * Fresh install → run install.sh → add domain via WHM →
	//     within ~1 hour, mail clients (Gmail / Outlook 365 /
	//     Thunderbird) connect with the panel-set mailbox password
	//     and authenticate normally with no manual mail-ssl step.
	//   * Existing install → `bzpanel deploy` → cron starts running
	//     hourly → all domains catch up over the next 1-2 hours.
	//   * Bulk import / restore from backup → operator can run
	//     `bzpanel mail-ssl-sweep` once manually for instant
	//     wire-up rather than waiting for the hourly cron.
	//
	// 3.0.39 (2026-04-29) — Server transfer carries mail SSL too.
	//
	// User asked: after server transfer from old → new, will mail TLS
	// keep working? Pre-3.0.39 answer: NO. The transfer pipeline
	// copied /etc/letsencrypt/live/<domain>/ but skipped
	// /etc/letsencrypt/live/mail.<domain>/, and even if it had, the
	// destination's Postfix sni-map / Dovecot local_name / nginx
	// helper vhost / renewal hook didn't transfer either. Operator
	// had to manually re-run `bzpanel mail-ssl <domain>` per
	// mail-bearing site after every migration.
	//
	// Two fixes:
	//
	//   1. agent.ExportSSLFromRemote now tars
	//      live/mail.<domain>/, archive/mail.<domain>/, and
	//      renewal/mail.<domain>.conf alongside the regular cert
	//      payload. `tar --ignore-failed-read` makes a missing
	//      mail.<domain> on the source a clean no-op.
	//
	//   2. transfer_service.go's SSL step copies those mail cert
	//      paths into /etc/letsencrypt/{live,archive,renewal}/ on
	//      the destination, then — if a fullchain.pem landed —
	//      shells out to `bzpanel mail-ssl <domain>` which detects
	//      the existing cert and runs the SNI wire-up only (skips
	//      DNS pre-flight + helper-vhost write + certbot, since
	//      those would all fail during transfer when public DNS
	//      still points at the source).
	//
	//   3. cmdMailSSL gains a fast-path early-return: if
	//      /etc/letsencrypt/live/mail.<domain>/fullchain.pem
	//      already exists with non-zero size, skip everything
	//      except the SNI wire-up (postfix sni-map upsert, dovecot
	//      local_name, postmap -F + reload, deploy hook). Makes
	//      the command idempotent for transfer + post-cert-renewal
	//      scenarios both.
	//
	// Net effect: post-transfer, mail clients (Gmail / Outlook 365 /
	// Thunderbird) connect to the destination's mail.<domain>:465
	// and see the same Let's Encrypt cert that was on the source —
	// no per-domain manual step required from the operator.
	//
	// 3.0.38 (2026-04-29) — Postfix SNI map column order fix.
	// `mail-ssl` was writing values as
	//     "<fullchain.pem>,<privkey.pem>"
	// but Postfix's SNI loader REQUIRES private key first then
	// certificate chain. Wrong order → handshake-time rejection:
	//     warning: error loading chain from SNI data for <host>: key not first
	//     warning: aborting TLS handshake
	// `openssl s_client -servername mail.<domain>` returned empty
	// "no peer certificate available" — which is what the user saw
	// when the v3.0.37 fix didn't quite stick. Swapped to
	//     "<privkey.pem>,<fullchain.pem>"
	// in postfixSNIUpsert and re-running mail-ssl now serves the LE
	// cert correctly via SNI.
	//
	// 3.0.37 (2026-04-29) — `bzpanel mail-ssl` postmap fix + renewal
	// hook. Production deploy of v3.0.36 surfaced two more bugs:
	//
	//   1. postmap was called WITHOUT the -F flag, so the .db
	//      stored the literal "/etc/letsencrypt/.../fullchain.pem"
	//      string as the value. Postfix's tls_server_sni_maps
	//      lookup expects base64-encoded PEM contents, so smtpd
	//      bailed at TLS handshake time with
	//          warning: malformed BASE64 value: /etc/letsencrypt/...
	//          warning: TLS library problem: callback failed
	//      Net effect: connection on 465 with SNI=mail.<domain>
	//      crashed before the cert was returned. Dovecot SNI was
	//      fine — its `local_name { ssl_cert = <... }` reads the
	//      PEM file directly, no base64 dance. Fix: use
	//      `postmap -F hash:/etc/postfix/sni-map` so postmap reads
	//      the file paths in the value column and embeds the PEM
	//      contents base64-encoded, which is what Postfix expects.
	//
	//   2. Renewals would have re-broken the SNI cert because the
	//      base64-embedded PEM in the .db wouldn't match the
	//      newly-issued cert on disk. cmdMailSSL now drops a
	//      certbot deploy hook at
	//      /etc/letsencrypt/renewal-hooks/deploy/bzpanel-mail-sni.sh
	//      that re-runs `postmap -F` + reloads postfix + dovecot
	//      after every successful renewal. Idempotent on
	//      re-issuance.
	//
	// 3.0.36 (2026-04-29) — `bzpanel mail-ssl` now writes an nginx
	// helper vhost for `mail.<domain>` BEFORE calling certbot.
	//
	// Production deploy of v3.0.35 surfaced the next bug: even with
	// public DNS pointing correctly at the panel, the certbot HTTP-01
	// challenge against `http://mail.iaj.cx/.well-known/...` returned
	// 404. Cause: nginx had no `server_name mail.<domain>` block on
	// port 80, so Let's Encrypt's GET fell through to the panel's
	// own vhost which has an explicit
	//     if ($host !~* ^(<panel>|<ip>)$) { return 404; }
	// guard. Customer-domain vhosts only know <domain> +
	// www.<domain>, never `mail.<domain>`.
	//
	// Fix: writeMailHelperVhost lays down
	// `/etc/nginx/sites-available/mail-<host>` (symlinked into
	// sites-enabled) with two responsibilities:
	//
	//   * serve /.well-known/acme-challenge/* from /var/www/certbot
	//     (issuance + every future renewal lands cleanly)
	//   * 301-redirect everything else to https://mail.<domain> so a
	//     human typing the URL in a browser doesn't hit a 404
	//
	// Run order in cmdMailSSL is now: DNS pre-flight → write helper
	// vhost → nginx -t → reload → certbot. The helper file is
	// idempotent — re-running on an already-configured domain
	// rewrites the same content and the symlink is left alone.
	//
	// 3.0.35 (2026-04-29) — Two follow-ups to the v3.0.34 mail-ssl
	// flow after a user hit "Couldn't connect to the server" in
	// Gmail's "Send mail as" wizard.
	//
	//   1. Mail Client Setup modal (WHM + cPanel) gains a
	//      port/encryption pairing table that calls out the
	//      mismatch causing the connect error: Gmail's "SSL"
	//      radio = implicit TLS = port 465; Gmail's "TLS" radio
	//      = STARTTLS = port 587. Picking SSL+587 or TLS+465
	//      makes Gmail try the wrong wire protocol on the wrong
	//      port → connect fails BEFORE auth is sent. Operators
	//      reading the setup modal now see the matching pairs
	//      explicitly.
	//
	//   2. `bzpanel mail-ssl <domain>` runs a public-DNS pre-flight
	//      via `dig +short @1.1.1.1 A mail.<domain>` and refuses
	//      to call certbot when the result doesn't match this
	//      server's public IP. Pre-3.0.35 certbot would still try,
	//      wait 30+ seconds for the HTTP-01 challenge to land
	//      on the wrong host, then produce a generic "unauthorized
	//      404" error — operators didn't see the actual cause
	//      (DNS pointing elsewhere) for one cycle. Now the
	//      command exits with a clearly-actionable message:
	//      "public DNS for mail.<domain> resolves to <X> but this
	//      server's IP is <Y> — update the A record at your DNS
	//      provider, wait for propagation, then re-run".
	//
	// Skipped silently when `dig` is unavailable — certbot still
	// produces its own (less friendly) error if DNS is wrong, so
	// the pre-flight is purely an early-feedback improvement.
	//
	// 3.0.34 (2026-04-29) — Mail TLS SNI: `bzpanel mail-ssl <domain>`
	// issues a Let's Encrypt cert for mail.<domain> and wires Postfix
	// + Dovecot SNI dispatch so strict clients (Gmail "Send mail
	// as", Outlook 365, modern Thunderbird) actually accept the
	// cert.
	//
	// User-reported symptom: Gmail's "Add another email address"
	// SMTP wizard returns "Authentication error. Check your username
	// and password" against mail.<domain>:465 even with the correct
	// password. Live VPS probe (port-465 openssl s_client) showed
	// Postfix returning the Ubuntu snake-oil cert: subject CN =
	// system hostname (srv1615717.hstgr.cloud), self-signed, SAN
	// covers only the bare hostname. Two failure modes for any
	// strict client connecting with SNI=mail.<customer-domain>:
	//
	//   * cert hostname mismatch (Gmail expects mail.iaj.cx, gets
	//     srv1615717.hstgr.cloud)
	//   * untrusted CA chain (self-signed)
	//
	// Gmail aborts the TLS handshake BEFORE issuing AUTH PLAIN, then
	// surfaces it as "Authentication error" — same shape as a real
	// auth failure, which is why the user thought their password
	// was wrong. Roundcube auto-login keeps working because it talks
	// to localhost:143 with TLS verification off.
	//
	// Real test confirmed underlying SASL works: AUTH PLAIN on 465
	// with FULL email + correct password returns "235 2.7.0
	// Authentication successful". AUTH PLAIN with bare local part
	// returns "535 SASL authentication failed sasl_username=admin"
	// — Dovecot's passwd-file is keyed on full email, so the local
	// part isn't a valid username. Most strict mail clients send
	// the full email as username; Gmail's wizard does too when its
	// "Username" field has the full address (the displayed value
	// in the screenshot was the local part because Gmail truncates
	// the field UI, not what's wired through SMTP).
	//
	// New `bzpanel mail-ssl <domain>` (and bsp menu option 12):
	//
	//   1. certbot certonly --webroot -d mail.<domain> using the
	//      panel's existing /var/www/certbot directory. Idempotent
	//      via --cert-name pinning so re-runs renew in place.
	//   2. /etc/postfix/sni-map gains "mail.<domain> <fullchain>,<privkey>"
	//      (replace-not-append on re-issue). postconf sets
	//      tls_server_sni_maps=hash:/etc/postfix/sni-map and clears
	//      smtpd_tls_chain_files so the per-SNI cert wins.
	//   3. /etc/dovecot/conf.d/99-panel-mail-sni.conf gains a
	//      `local_name mail.<domain> { ssl_cert ssl_key }` block
	//      (idempotent — existing block is replaced, not appended).
	//   4. postmap + reload postfix + reload dovecot.
	//
	// Mail Client Setup modal in WHM + cPanel now opens with an
	// amber callout listing the two gotchas that cause "auth fails
	// with right password" reports:
	//   1. Username MUST be the FULL email (not local part)
	//   2. Strict clients reject the snake-oil cert — point at
	//      `bzpanel mail-ssl <domain>`
	//
	// Pre-flight: mail.<domain> A record must point at this server
	// (or certbot HTTP-01 challenge times out). Multi-tenant safe —
	// each domain gets its own SNI entry, no cross-tenant cert
	// leakage.
	//
	// 3.0.33 (2026-04-29) — Mailbox auth fix: idempotent CreateMailbox
	// + new `bzpanel heal-mail` to dedupe /etc/dovecot/users and
	// /etc/postfix/virtual_mailbox_maps.
	//
	// User-reported symptom: webmail auto-login works (proves the
	// panel knows the mailbox password), but Outlook/Thunderbird
	// IMAP login + SMTP submission fail with the same email +
	// password. Live VPS probe found duplicate entries in
	// /etc/dovecot/users — `admin@usersbug.thewaapi.com` had 5
	// rows, each from a previous panel re-create. Dovecot logged
	// "User <email> exists more than once" and picked the FIRST
	// match, which still held the OLD password hash. Roundcube's
	// SSO bypasses passdb (HMAC-signed direct IMAP auth with the
	// current plaintext password), which is why it was the only
	// surface that worked.
	//
	// Why duplicates accumulated: pre-3.0.33 CreateMailbox blindly
	// appended a line via `echo $line >> /etc/dovecot/users` with
	// no dedupe. When a subdomain whose admin@<sub> mailbox already
	// existed got re-created, the second create's mongo INSERT hit
	// the unique-email index and rolled back — but the dovecot
	// users file write had already succeeded, so a duplicate row
	// stayed behind. Five test cycles → five rows.
	//
	// Fixes:
	//   * CreateMailbox now sed-removes any line for this email
	//     BEFORE appending. Same for /etc/postfix/virtual_mailbox_maps.
	//     The append is the ONLY canonical write path so the file
	//     can never accumulate duplicates from this code path again.
	//   * New `bzpanel heal-mail` (alias `repair-mail`) + bsp menu
	//     option 11. Reads /etc/dovecot/users, keeps only the LAST
	//     line per email (most recent password wins), atomic
	//     rename to write back, restores ownership/mode, reloads
	//     dovecot. Same for /etc/postfix/virtual_mailbox_maps with
	//     a postmap pass + postfix reload. Idempotent — already-
	//     clean files are a no-op.
	//
	// Operator action on installs that already accumulated stale
	// rows: `bzpanel deploy && bzpanel heal-mail`. After heal-mail
	// completes, IMAP/SMTP login with the panel-set password
	// authenticates immediately.
	//
	// 3.0.32 (2026-04-29) — Branding + Reports.
	//
	// Branding (panel name + logo + favicon). New singleton in
	// server_config keyed `_id: "branding"`, parallel to
	// PanelMailService. Public GET at /api/v1/branding lets
	// index.html / login pages render the configured chrome BEFORE
	// any auth token exists. Admin write at
	// /api/v1/whm/config/branding (server.manage). Image storage is
	// data: URLs in mongo (capped 256 KB / asset) — atomic, no
	// filesystem orphans, no nginx static-routing changes. Sidebar +
	// TopBar + login pages + browser tab all read the branding
	// config at boot and swap their chrome (with bundled defaults
	// when no config exists yet).
	//
	// Reports (top IPs / URLs / per-domain). New WHM page at
	// /reports backed by ResourceService.TrafficStatsByDomain.
	// Single-pass awk over nginx access logs emits TOTAL + per-IP
	// + per-URL counts; Go side sorts + trims to top 50 each.
	// Server-wide variant (no `?domain=`) also returns a
	// per-domain breakdown so an operator can see which sites
	// generate the most traffic. Per-domain variant scopes the
	// report to /var/log/nginx/<domain>-access.log. Endpoint:
	// GET /api/v1/whm/resources/traffic-stats?domain=<optional>
	// (server.view). Read-only — informational only.
	//
	// 3.0.31 (2026-04-29) — `parentZoneOf` flips from
	// most-specific-wins to APEX-WINS, the heal-dns command now
	// also prunes stale dns_zones, and `GetOrCreateZone` refuses
	// to silently mint Mongo rows for non-pdns zones.
	//
	// Live VPS reproduction (testing VPS 187.127.156.87):
	//
	//   dns_zones = { thewaapi.com,
	//                 api.usersbug.thewaapi.com }   ← orphan, no pdns SOA
	//
	//   POST /domains  domain=dev2.api.usersbug.thewaapi.com
	//   → 201 Created  (panel reports success)
	//   → 0 dns_records inserted (pdnsutil rejects rrset against
	//     a zone PowerDNS doesn't own; AddRecord rolls back)
	//
	// User's report at konsultkaro.com had the same shape,
	// landing the A record at name "dev" because their stale zone
	// existed in BOTH Mongo AND pdns (operator had also created
	// the subdomain as a primary at some point). Either way, the
	// outcome was wrong relative to what the operator typed.
	//
	// 3.0.31 fixes:
	//
	//   1. parentZoneOf walks shortest-suffix-first. The apex
	//      always wins because it's queried first; if it's a
	//      registered dns_zone, longer (and potentially stale)
	//      candidates aren't even examined. Net: stale subdomain
	//      dns_zones rows become a no-op for the lookup —
	//      operators can leave them in place or run heal-dns to
	//      tidy up. TestParentZoneOf_RobustToStaleSubdomainZones
	//      pins the user's exact konsultkaro.com / dev.api.users
	//      shape. Trade-off: legitimate subdomain delegations
	//      (operator manually delegated corp.example.com to its
	//      own SOA) lose the most-specific routing — they're a
	//      niche the panel UI doesn't drive, so the trade favours
	//      the common case.
	//
	//   2. heal-dns gains a stale-zones prune pass. Walks every
	//      dns_zones row, asks pdnsutil list-zone whether
	//      PowerDNS owns it; if not, deletes the orphan plus any
	//      dangling dns_records. Reports `stale dns_zones
	//      pruned: N` so the operator sees the cleanup scope.
	//      Pre-3.0.31 the manual recovery was a Mongo shell —
	//      most operators never ran it.
	//
	//   3. GetOrCreateZone now refuses to mint a Mongo dns_zones
	//      row when pdnsutil reports the zone doesn't exist.
	//      That's the leak path that originally created the
	//      stale rows in pre-3.0.24 days; closing it here means
	//      no future code path can accidentally re-introduce
	//      them. Heal-on-read still works: when pdns has the
	//      zone but Mongo doesn't, the row is created.
	//
	// Live verification: deployed via `bzpanel deploy`, repro'd
	// the bug with an injected stale dns_zone (`api.usersbug
	// .thewaapi.com`), confirmed the new logic routes to the
	// apex (`thewaapi.com`) and lands the A record at
	// "dev2.api.usersbug" — the right relative name.
	//
	// 3.0.30 (2026-04-28) — Three coupled fixes for "subdomain
	// created but A record missing + emails not sending" reports.
	// Diagnosed by SSH probe of the testing VPS:
	//
	//   1. AutoBootstrap accepted IP-shaped DOMAIN. config.go
	//      defaults DOMAIN to "localhost" but on IP-only deploys
	//      operators set it to the bare server IP (e.g.
	//      DOMAIN=187.127.156.87). Pre-3.0.30 AutoBootstrap took
	//      that string and produced FromAddr=admin@187.127.156.87.
	//      Postfix rejected every send with 501 5.1.7 Bad sender
	//      address syntax (RFC 5321 §4.1.2 forbids bare IPs in
	//      the email domain — needs the literal-IP `[1.2.3.4]`
	//      form). Net effect: every reset / OTP / notification
	//      mail dead-lettered and the journal filled with the
	//      same line every retry. Fix: new isUsableMailDomain
	//      gate rejects empty / "localhost" / IPv4 / IPv6 /
	//      bracketed-IP / single-label hosts. Resolution chain
	//      is now cfg.Domain → os.Hostname() → skip, with each
	//      step gated by the predicate.
	//
	//      Also heals stale auto-bootstrap docs in mongo: if an
	//      existing panel_mail config has FromAddr whose right-
	//      hand side fails the new predicate AND no operator-
	//      filled credentials exist (Username / Password /
	//      non-localhost Host), AutoBootstrap rewrites it on the
	//      next boot. Operator-owned configs are never touched.
	//
	//   2. Domain.Create swallowed AddRecord failures to stderr
	//      only. The runtime path that wrote the orphan
	//      subdomain rows (`app.thewaapi.com`, `api.lamdainfotech
	//      .thewaapi.com`) silently logged the AddRecord error
	//      and returned 201 to the caller — operator saw "site
	//      created" while DNS was never wired. AddRecord errors
	//      now also write a structured zerolog Error entry so
	//      `journalctl -u serverpanel` surfaces the cause.
	//      Duplicate-record errors are downgraded to debug
	//      because they're harmless.
	//
	//   3. New `bzpanel heal-dns` (alias `repair-dns`) and menu
	//      option 10. Walks every domain row, computes the
	//      correct parent zone via the same longest-suffix
	//      parentZoneOf-style walk the runtime now uses, checks
	//      whether an A record + www CNAME live at the right
	//      relative name, and inserts them when missing.
	//      Idempotent — already-correct records left alone.
	//      Reports `A added N / www added M / scanned X` so the
	//      operator sees the scope. Uses pdnsutil replace-rrset
	//      directly (matching how reconcileRRSet writes from the
	//      service layer) so PowerDNS picks the records up
	//      without needing a service restart, and reloads pdns
	//      once at the end.
	//
	// Live verification: deployed via `bzpanel deploy`, ran
	// `bzpanel heal-dns` against the testing VPS — orphan rows
	// from before v3.0.24 got their A + www CNAME backfilled
	// in one pass, panel mail config replaced admin@<ip> with
	// admin@<hostname>, Postfix accepted MAIL FROM, and a fresh
	// nested-subdomain create
	// (bzptest2.bzptest1.thewaapi.com) ALSO landed its A record
	// at the correct multi-level name with the new audit
	// logging firing in journalctl as expected.
	//
	// 3.0.29 (2026-04-28) — `bzpanel deploy` + `bzpanel rebuild`
	// subcommands close the gap that made every previous patch
	// invisible to users running the panel on a VPS.
	//
	// Diagnostic from the user's session: they reported "bsp not
	// work + login broken" across THREE consecutive patches. SSH
	// probe revealed the root cause — the bzpanel binary on disk
	// was v3.0.2 and the server binary was v3.0.21. Every git push
	// shipped to GitHub, but the auto-deploy GitHub workflow
	// targets a stale VPS_HOST secret on most installs, so the
	// running binaries never advanced. Operators only had a
	// six-step manual rebuild loop with a hardcoded versioned Go
	// path (/opt/go/<ver>/bin/go) — easy to miss, easy to forget.
	// They saw symptoms of every shipped bug because every shipped
	// fix was sitting unused on GitHub.
	//
	// Fix: collapse the rebuild loop into one bzpanel subcommand.
	//
	//   bzpanel rebuild   — re-build server + agent + bzpanel +
	//                       seed from /opt/serverpanel/backend and
	//                       systemctl restart serverpanel.
	//   bzpanel deploy    — git fetch --all + git reset --hard
	//                       origin/<current-branch> against
	//                       /opt/serverpanel, then chain into
	//                       rebuild. `git stash` first so any
	//                       hand-edits are saved, not lost.
	//                       Aliases: update, upgrade.
	//
	// findGoBin walks the canonical install.sh path
	// (/opt/go/<ver>/bin/go), the new stable symlink (3.0.29
	// install.sh creates /opt/go/bin/go), and PATH — so a fresh
	// install Just Works regardless of which Go version landed.
	//
	// install.sh now creates /opt/go/bin/go and /opt/go/bin/gofmt
	// as version-independent symlinks. Re-created on every
	// install.sh run so a Go upgrade refreshes the target.
	//
	// Interactive bsp menu gains options 8 (Deploy from GitHub) and
	// 9 (Rebuild from on-disk source), so an operator who SSH'd
	// in to chase a bug can ship the latest fix and then verify
	// it from the same menu.
	//
	// Live verification: deployed via `bzpanel deploy` on the
	// testing VPS (187.127.156.87), rotated admin password via
	// `bzpanel admin-password`, and confirmed BOTH lowercase
	// (`betazeninfotech@gmail.com`) and mixed-case
	// (`BetazenInfotech@Gmail.com`) login return HTTP 200 with a
	// valid JWT. The 3.0.28 auto-heal also fired on `bzpanel
	// info` — passive recovery confirmed.
	//
	// 3.0.28 (2026-04-28) — `bsp` admin-email + login regression fix.
	//
	// The 3.0.27 patch lowercased the typed login email before the
	// Mongo lookup so case variations would still match a properly-
	// stored row. That uncovered a long-standing latent bug in the
	// `bsp` / `bzpanel` admin CLI: cmdAdminEmail wrote the operator's
	// typed string to users.email VERBATIM at line 240. Pre-3.0.27
	// the bug was hidden because login also matched verbatim — typing
	// the SAME mixed-case at login worked. After 3.0.27, login lowers
	// typed input, so any mixed-case stored email became permanently
	// unloginable.
	//
	// User reproduction:
	//   1. ssh root@panel.example.com
	//   2. bsp → option 1 → New super admin email: Admin@x.com
	//   3. Mongo row lands {email: "Admin@x.com"}
	//   4. Type Admin@x.com (or admin@x.com) at the WHM login form
	//   5. AuthService.LoginWithUA does
	//        loginEmail = ToLower(req.Email) = "admin@x.com"
	//      then bson.M{"email": "admin@x.com"} doesn't match the
	//      mixed-case stored row → "invalid email or password"
	//
	// Fixes in this patch:
	//   * cmdAdminEmail now lowercases + trims newEmail before
	//     validation AND before write. Matches the global invariant
	//     auth_service.go has enforced for years.
	//   * cmdAdminPassword side-effect-heals the row: when the
	//     operator runs `bsp` option 2 to rotate password (the
	//     natural "I can't log in" recovery action), the email
	//     field is also lowered if it was mixed-case. Single
	//     atomic update.
	//   * cmdInfo + interactiveMenu run an idempotent
	//     healAdminEmailCasing pass on entry, so even a read-only
	//     `bsp info` invocation fixes a broken install on the spot
	//     and reports the change inline so the operator understands
	//     why login was failing and that it's now resolved.
	//   * findSuperAdmin's docstring updated to spell out that
	//     casing on the email field doesn't gate the lookup —
	//     role + is_super_admin are stable.
	//
	// 3.0.27 (2026-04-28) — Two related auth-pipeline bugs the user
	// reported as "WHM admin id/password setup login not work" +
	// "email also not to send":
	//
	//   1. Login was case-sensitive on email. Every CREATE/UPDATE
	//      path in the codebase already lowercases the email
	//      (UpdateMe, ForgotPassword, RequestOTP, Resend, …) but
	//      LoginWithUA queried `bson.M{"email": req.Email}` raw.
	//      An admin whose stored email is admin@betazeninfotech.com
	//      who typed Admin@BetazenInfotech.com on the login form
	//      got "invalid email or password" because the typed
	//      string never lowercased on its way to Mongo. The
	//      browser's `type=email` widget preserves case, so the
	//      safe fix lives at the service boundary. LoginWithUA
	//      now does loginEmail := strings.ToLower(strings.TrimSpace
	//      (req.Email)) before the FindOne. Mirrors the
	//      normalisation rule the rest of auth_service.go has
	//      enforced for years. TestLoginEmailNormalisation pins
	//      the rule with seven cases.
	//
	//   2. Mailer auto-bootstrap silently skipped on every fresh
	//      install. config.go defaults DOMAIN to "localhost", and
	//      AutoBootstrap's pre-3.0.27 first line was
	//          if panelDomain == "" || EqualFold(panelDomain, "localhost") { return nil }
	//      Net effect: panel boots → mailer config doc never
	//      created → AuthService.ForgotPassword / RequestOTP fall
	//      into the "mailer disabled" branch and dead-letter the
	//      reset link / OTP code into journalctl. Operators saw
	//      "I clicked Forgot Password but no email arrived".
	//      AutoBootstrap now resolves FromAddr through a fallback
	//      chain: cfg.Domain → os.Hostname() → skip. install.sh
	//      runs `hostnamectl set-hostname <fqdn>` early, so on
	//      every install.sh-provisioned VPS the hostname is a
	//      usable mail domain even when the operator forgot to
	//      export DOMAIN. The local Postfix relay (127.0.0.1:25)
	//      that install.sh provisions accepts mail with a
	//      hostname-based From, so reset/OTP mail flows
	//      end-to-end the moment the panel boots — no
	//      manual SMTP wiring required.
	//
	// 3.0.26 (2026-04-28) — User Panel Email page reaches per-row
	// parity with the WHM Email page. Three actions ported:
	//
	//   * View Details — modal showing email/domain header, quota
	//     usage with a coloured bar, send-limit-per-hour, the
	//     SSL/TLS IMAP/POP3/SMTP cheat-sheet (with non-SSL
	//     collapsible), and created/updated dates. Two pivot
	//     buttons jump straight to Edit Configuration or Mail
	//     Client Setup so the operator doesn't re-find the row.
	//   * Edit Configuration — quota / send-limit / new password.
	//     Empty password leaves the existing one alone (the
	//     backend's UpdateMailbox treats omitted/empty as a no-op).
	//   * Mail Client Setup — read-only IMAP/SMTP cheat-sheet plus
	//     a short Outlook / Thunderbird / Gmail / Apple Mail
	//     how-to. Hardcoded values derived from the mailbox's
	//     domain — no backend call needed.
	//
	// Open Webmail and Delete already existed in the User Panel
	// page; the row icon order now matches WHM byte-for-byte
	// (Eye → Edit → Settings → Send-test → Webmail → Trash) so
	// muscle memory carries over.
	//
	// Backend already exposed the endpoints at /api/v1/cpanel/email/:id
	// (GET / PUT / DELETE) and /email/webmail-token, so this is
	// frontend-only.
	//
	// 3.0.25 (2026-04-28) — Locks in the 3.0.24 subdomain fix with a
	// side-by-side regression test (TestParentZoneOf_BugDivergence)
	// that runs the user's exact input — abc.abc.xyz.qwe.com — through
	// BOTH predicates the old and new code use:
	//
	//   * OLD predicate (queries `domains`, where panel-tracked
	//     subdomain rows live): parent = abc.xyz.qwe.com → A record
	//     name = "abc". That's the bug the user reported verbatim.
	//   * NEW predicate (queries `dns_zones`, the source of truth for
	//     "this domain has its own DNS authority"): parent = qwe.com
	//     → A record name = "abc.abc.xyz". That's the expected
	//     outcome.
	//
	// The test asserts BOTH branches produce their respective
	// outcomes AND that they diverge — so any future change that
	// silently re-points findParentDomain back at the wrong
	// collection (or that flattens the two collections together)
	// trips a clear, named failure instead of regressing to the
	// "label = abc only" symptom in the field.
	//
	// 3.0.24 (2026-04-28) — Subdomain create no longer slices the
	// label down to the first segment when an intermediate panel
	// subdomain happens to share the suffix.
	//
	// User reproduction: panel has qwe.com as a primary domain. An
	// earlier op already created abc.xyz.qwe.com as a subdomain (so a
	// row landed in `domains` for resource counting; no separate
	// dns_zones entry — the A record lives inside qwe.com). User then
	// creates abc.abc.xyz.qwe.com. The pre-3.0.24 findParentDomain
	// queried `domains`, found abc.xyz.qwe.com first (most-specific
	// match wins), computed
	//   subPart = TrimSuffix("abc.abc.xyz.qwe.com", ".abc.xyz.qwe.com")
	//           = "abc"
	// and called dns.AddRecord(zone="abc.xyz.qwe.com", name="abc"),
	// which auto-created an orphan dns_zones row in Mongo and then
	// asked pdnsutil replace-rrset against a zone PowerDNS doesn't
	// own. Net effect: the operator saw "label = abc only" / a
	// failure to actually serve abc.abc.xyz.qwe.com.
	//
	// Fix: findParentDomain now queries dns_zones instead of domains.
	// dns_zones is the source of truth for "this domain has its own
	// DNS authority". An operator-set subdomain that lives only as a
	// record under its parent's zone correctly does NOT count as a
	// parent for any deeper FQDN. A delegated subdomain zone
	// (operator explicitly CreateZone'd it with its own SOA + NS)
	// keeps winning, because most-specific wins is preserved — and
	// landing app.corp.example.com inside corp.example.com is what
	// the delegation requires.
	//
	// Same broken matcher was used by DomainService.Delete and
	// classifyDomainType (preflight); both inherit the fix
	// automatically.
	//
	// Pure parentZoneOf helper extracted from findParentDomain so the
	// most-specific-first iteration is unit-testable without a Mongo
	// round-trip. Seven test cases lock in: user-reported scenario,
	// delegated-subdomain-wins, no-parent, two-label-too-short,
	// trailing-dot-normalisation, lookup-order, empty/whitespace.
	//
	// Server transfer pipeline checked too — it doesn't use
	// findParentDomain (records walk source-server zones via pdnsutil
	// and stay in their declared zone, domain rows are upserted
	// directly into ColDomains), so transfer is unaffected. As a
	// side-benefit, the fix means POST-transfer state where a
	// transferred subdomain row would have re-triggered the bug on
	// the destination is also safe now.
	//
	// 3.0.23 (2026-04-28) — Password generator + show/hide toggle now
	// available on every "set a password" field across both SPAs.
	//
	// Pre-3.0.23 only 3 of 25 password inputs had a Generate button:
	// the WHM and User Panel "Create Database User" forms, plus the
	// User Panel WordPress admin password during install. Operators
	// creating team members, mailboxes, vendors, WP users, HTTP Basic
	// Auth credentials, manual-mode WP DBs, and DB-owner password
	// rotations were typing weak passwords by hand because there was
	// no in-place way to mint a strong one. Worse, the show/hide eye
	// toggle was inconsistent — two pages had it, the other 23 didn't.
	//
	// New shared component @serverpanel/ui ➜ PasswordInput bundles:
	//   * the input (type swaps password ↔ text on toggle)
	//   * an eye / eye-off button to reveal what was typed
	//   * a key-round button that fills the field with a 16-char
	//     cryptographically random password (crypto.getRandomValues
	//     over a 70-char alphabet, no shell-quoting-prone chars) and
	//     auto-reveals so the operator can copy it before submitting
	//
	// generatePassword() is also exported as a plain function for
	// non-component sites that want the same generator without the
	// UI shell.
	//
	// 14 fields gained the generator (every place we MINT a credential
	// — mailbox, team member, vendor, vendor reset, WP admin user,
	// WP user, manual DB password, DB owner rotate, DB-user add,
	// HTTP Basic Auth across both surfaces).
	//
	// 8 external-credential fields (Git PAT in apps/deploy software,
	// SSH/SFTP backup destination, SMTP relay, server-migration
	// source) get the same component with hideGenerator=true: the
	// dice button stays off because generating a random string here
	// would produce one the upstream service rejects, but the show/
	// hide eye toggle still helps when operators paste a long token.
	//
	// Login forms intentionally untouched: no generator on a login
	// password field.
	//
	// 3.0.22 (2026-04-28) — WordPress install pipeline upgrade. Five
	// interlocking fixes for a flow that operators reported as
	// "WordPress not installing properly":
	//
	//   1. Placeholder index.html shadowed index.php after install.
	//      Domain creation drops a default index.html at
	//      /home/<u>/domains/<d>/public_html. nginx + Apache both rank
	//      index.html above index.php, so even a fully successful wp
	//      core install kept serving the placeholder — operators saw
	//      "I clicked Install, the URL still shows the placeholder, so
	//      the install must have failed." InstallWordPress now removes
	//      index.html / index.htm / default.html from the install root
	//      before wp core download runs.
	//
	//   2. wp core download silently ignored --version and --locale.
	//      The install wizard sent both fields (version dropdown,
	//      locale dropdown), but the backend's InstallWordPressRequest
	//      didn't have the JSON tags to receive them. Operators
	//      picking WP 6.4 / hi_IN got the latest English build instead.
	//      Fields are wired through agent.InstallWordPressOptions to
	//      wp-cli's --version and --locale flags.
	//
	//   3. Shell quoting was per-arg ad-hoc — only --dbpass got
	//      single-quoted in the wp config create call, leaving
	//      --dbname / --dbuser / --dbhost open to break when manual
	//      mode supplied a value with shell-interpreted characters.
	//      Same story for wp core install: --title='%s' style
	//      single-quoting broke when the operator's site title or
	//      admin password contained an apostrophe (O'Brien). Every
	//      dynamic value is now POSIX-quoted via shellSingleQuote.
	//
	//   4. wp core install hung up to two minutes waiting on the local
	//      MTA to deliver the "Welcome to WordPress" mail on boxes
	//      without postfix tuning. Added --skip-email; the admin email
	//      still lands in WP options, no welcome mail goes out.
	//
	//   5. mkdir / chown errors were swallowed — a failed mkdir
	//      (parent dir doesn't exist on the unusual /blog subpath
	//      install) made wp core download fail with the generic
	//      "command failed: exit status 1" message instead of a
	//      pointed "create install dir /home/.../public_html/blog
	//      failed: …". Both now propagate.
	//
	//   * Belt-and-braces: RunCommandAsUser now passes -H to sudo so
	//     HOME is reset to the target user's home. wp-cli's
	//     ~/.wp-cli/cache writes need this; on hosts whose sudoers
	//     keeps HOME, wp-cli got EACCES on /root/.wp-cli/cache and
	//     wp core download exited 1 with no obvious cause. -H makes
	//     the behaviour deterministic across every install image.
	//
	//   * Bonus: wp core download gets --force so a re-run after a
	//     half-completed install (no wp-config yet, but partial
	//     wp-includes from the previous attempt) succeeds instead of
	//     bailing with "WordPress files seem to already be present".
	//
	// 3.0.21 (2026-04-28) — RDAP-first WHOIS lookup, fixes missing
	// expiration dates for `.in` (and other modern CC-TLDs whose
	// port-43 whois service is unreliable from this network).
	//
	// Symptom: `.in` rows on the WHM Domains page rendered an empty
	// EXPIRES column even when the domain was perfectly valid.
	// `whois iafoundation.in` from the box returned an empty body —
	// the system whois package's TLD server map either doesn't have
	// a working entry or the registry's port-43 server was rate-
	// limiting / firewalled.
	//
	// Fix: WhoisLookup now tries RDAP (Registration Data Access
	// Protocol, RFC 9224) first. Reads IANA's bootstrap registry
	// at https://data.iana.org/rdap/dns.json (cached in-process for
	// 24 h), routes the domain's TLD to the authoritative RDAP
	// server, GETs <server>/domain/<name>, and parses the JSON for
	// events[?eventAction==expiration|registration].eventDate, the
	// registrar entity's vCard FN, and the nameservers list. Falls
	// back to system whois only when RDAP errors or the TLD isn't
	// on RDAP. RDAP rides 443 (every panel install already has
	// outbound HTTPS), so it sidesteps port-43 firewalls and the
	// ICANN-mandated registry switch to RDAP guarantees coverage
	// for every gTLD plus most modern CC-TLDs including .in / .io /
	// .uk. WhoisResult shape unchanged so the UI / domain-expiry
	// sweep / dashboard read it the same way.
	//
	// 3.0.20 (2026-04-28) — Transfer Databases now writes the panel
	// password into the destination's `databases` row.
	//
	// User reported: post-transfer the WHM Database Connection modal
	// showed Username populated but PASSWORD empty. The
	// Transfer-Databases MySQL upsert ran BEFORE the panel-records
	// sync and put `username` in $setOnInsert WITHOUT `password`,
	// then the panel-records sync's insertDeduped saw the row
	// already existed and skipped it — so the password never
	// landed. The "Open in phpMyAdmin (auto-login)" button then
	// signed a URL with an empty password, which MySQL rejected.
	//
	// Now Transfer-Databases $set the panel-resolved username AND
	// password (the same credentials we just used to issue
	// CreateMySQLUser, sourced from the SOURCE's panel `databases`
	// row via resolvePanelDB). The connection_string is rebuilt
	// with the password embedded so the connection-info modal's
	// CLI command works without copy-pasting from the password
	// field. The destination's row reflects MySQL's actual auth
	// state from the moment Transfer-Databases finishes.
	//
	// 3.0.19 (2026-04-28) — MongoDB creation temporarily disabled +
	// phpMyAdmin auto-login self-heals on transfer.
	//
	// MongoDB removal: install.sh provisions a mongo `serverpanel`
	// user scoped only to its own DB, so createUser on
	// `<vendor>_<name>` fails with "not authorized" and there's no
	// in-panel recovery flow yet. Until the install grows a proper
	// admin-scoped mongo user, the WHM Create Database UI hides
	// MongoDB from the Type dropdown, the backend rejects
	// type=mongodb with a clear "temporarily disabled" message, and
	// the transfer pipeline skips MongoDB DBs (logs that it's
	// disabled, MySQL still transfers normally). Existing MongoDB
	// rows continue to render in the listing for reference. The
	// `bzpanel mongo-bootstrap` CLI escape hatch ships dormant for
	// operators who need it. (b)
	//
	// phpMyAdmin auto-login post-transfer: the self-heal helper
	// `ensurePhpMyAdminInstalled` (called by the transfer pipeline)
	// previously installed phpMyAdmin + nginx but didn't write
	// /etc/phpmyadmin/signon-secret, the dual-server config.inc.php,
	// or /usr/share/phpmyadmin/_signon.php — all three are required
	// for the panel's "Open in phpMyAdmin (auto-login)" button to
	// work. Net effect: clicking the button on a transferred
	// database fell back to /phpmyadmin/'s manual login screen
	// instead of the database structure page. Now the helper writes
	// every piece install.sh would have written, including the
	// HMAC-verifying _signon.php shim. GetPhpMyAdminInfo also
	// re-reads /etc/phpmyadmin/signon-secret on each call, so a
	// panel that started before the secret existed (typical post-
	// transfer state) issues correctly-signed URLs without needing a
	// restart.
	//
	// 3.0.18 (2026-04-28) — Auth-aware mongodump + mongosh `use X`
	// quirk fix.
	//
	//   1. `mongosh --eval 'use X; db.createUser(...)'` does NOT
	//      switch context for the createUser — a long-standing
	//      mongosh quirk that silently sends the op to `test`. Every
	//      mongo agent helper (CreateMongoDatabase / CreateMongoUser
	//      / DeleteMongoUser / UpdateMongoUser*) was using that
	//      pattern, so user accounts the panel claimed it created in
	//      the user's DB actually landed in `test` and connecting
	//      with the printed credentials failed authenticationagainst
	//      the right authSource. Switched every helper to
	//      `db.getSiblingDB(<X>).<op>(...)` so mongosh executes the
	//      operation against the intended DB.
	//
	//   2. agent.RemoteMongoDump used to issue a plain `mongodump
	//      --db <X>` over SSH — but a source mongo with
	//      `authorization: enabled` (the typical production stance)
	//      rejects that with "Command listCollections requires
	//      authentication", producing a 23-byte empty archive that
	//      then fails restore on the destination with "EOF reading
	//      beginning of archive". The new wrapper tries unauthed
	//      first (still fast on dev boxes) then falls back to the
	//      panel's MONGO_URI admin credentials from
	//      /opt/serverpanel/.env, then to caller-supplied per-DB
	//      creds, verifying the archive is non-empty before
	//      declaring success. The Transfer Databases step resolves
	//      the panel's stored mongo user/password by-name from the
	//      source's `databases` collection up-front and threads it
	//      through, so a source whose admin URI has DB-scoped (not
	//      global) listDatabases still gives us a working dump.
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
	// 3.1.2 (2026-05-01) — Final server-transfer secrets sweep.
	//
	// Two gaps left after 3.1.1's re-encryption pass:
	//
	//   - Legacy platform notification webhooks (the admin/webhooks
	//     surface that pre-dated the per-tenant webhook_endpoints
	//     collection) had plaintext HMAC secrets that the migration
	//     never carried across — Slack / on-call alerts went silent
	//     post-cutover until the operator manually recreated each.
	//     Now synced 1:1 (plaintext survives any APP_ENCRYPTION_KEY
	//     change so no re-encryption needed) plus a pass for the
	//     notification_settings singleton.
	//   - Migrated tenant users (vendors + their team) carried
	//     refresh_token / failed_logins / locked_until / reset_token_*
	//     state from source. Refresh tokens were minted under the
	//     SOURCE's JWT_SECRET so they wouldn't validate anyway, but
	//     copying them across leaks "session presence" and allowed a
	//     stale lockout counter to lock a freshly-migrated user out
	//     of the destination. These fields are now stripped at insert
	//     time, mirroring the wipe the owner row already received.
	//
	// 3.1.1 (2026-05-01) — Server transfer now re-encrypts every AES-GCM
	// secret under the destination's APP_ENCRYPTION_KEY instead of
	// dropping it for the operator to rotate by hand.
	//
	// Pre-3.1.1 the SMTP relay password was the only secret carried
	// across; webhook signing secrets and Deploy Software GitHub PATs
	// landed as ciphertext the local key couldn't decrypt, silently
	// breaking every outbound webhook + auto-deploy after cutover.
	//
	// Now: a single SSH grep against /opt/serverpanel/.env on source
	// fetches APP_ENCRYPTION_KEY (cached for the run), and three sync
	// paths (panel_mail / webhook_endpoints / projects) decrypt under
	// the source key and re-encrypt under the destination's. On
	// success webhooks land active and projects keep their PATs;
	// on decrypt failure the cipher is dropped (not silently
	// preserved as garbage) and the operator gets a clear "rotate /
	// re-enter" hint in the transfer log.
	//
	// 3.1.0 (2026-04-30) — Developer surface: API tokens + outbound webhooks.
	//
	// New /api/v1/external/* programmatic API authenticates with btz_*
	// tokens; vendors and the platform owner can mint scoped tokens
	// (domain / email / ssl / deploy:link / webhook:manage) and subscribe
	// outbound webhooks for events like domain.created / ssl.issued /
	// deploy.linked. WHM-issued tokens may be pinned to a vendor at
	// creation so a leaked token can't escape that tenant.
	//
	// Server-transfer pipeline carries api_tokens 1:1 (bcrypt'd secrets
	// keep working) and migrates webhook_endpoints inactive — the new
	// box can't decrypt the source's signing secret blobs, so the
	// operator clicks Rotate to mint fresh ones under the destination's
	// APP_ENCRYPTION_KEY without losing the URL / event subscriptions.
	// 3.1.61 (2026-05-30) — `www.<d>` + `cname.<d>` coverage for App
	// auto-SSL + post-migration project services, plus webmail-SSO
	// re-encryption so mail "Open" keeps working after a server
	// transfer.
	//
	// Three gaps closed:
	//
	//   1. ensureSSLForApp requested a cert covering only
	//      [<d>, www.<d>] even though every App vhost template
	//      (reverseProxyTemplate / reverseProxySSLTemplate /
	//      CreateStaticVhost / CreateStaticVhostWithSSL) lists
	//      `server_name <d> www.<d> cname.<d>;` — so the nginx
	//      :443 block claimed the `cname.<d>` listener but the
	//      cert SAN list didn't cover it, and browsers visiting
	//      `https://cname.<d>` got a name-mismatch handshake even
	//      though the page eventually loaded. Now the cert
	//      request + the persisted SSLCertificate.Domains row
	//      both include `cname.<d>`, matching what
	//      DomainService.Create already does for PHP-FPM domains.
	//
	//   2. buildRecoveryVhostSpec (used by the migration recovery
	//      path tryStartSyncedProjects → recoverProjectService)
	//      copied svc.AliasDomains verbatim. The normal create
	//      path's buildMergedVhostSpec auto-inflates the alias
	//      set with `www.<primary>` + `cname.<primary>` AND
	//      `www.<alias>` + `cname.<alias>` for every linked
	//      alias (project_helpers.go, since 3.1.11 + 3.1.31).
	//      Without the same inflation in recovery, transferred
	//      Deploy-Software services landed on the destination
	//      with nginx `server_name <primary>;` only — `https://
	//      www.<primary>` returned 404 from the panel catch-all
	//      (the exact symptom the user flagged for "server
	//      migration time"). Extracted the rule into a single
	//      package-private helper `expandImplicitAliases(primary,
	//      aliases)` so both call paths produce the same nginx
	//      server_name + LE SAN list. The certbot call in
	//      recoverProjectService also switched from
	//      svc.AliasDomains to spec.Aliases so the SSL coverage
	//      matches what nginx serves.
	//
	//   3. Mailbox sync copied `encrypted_pass` verbatim during
	//      panel-records migration. That blob is AES-GCM-sealed
	//      under the SOURCE's JWT_SECRET — destination's
	//      GenerateWebmailToken can't decrypt it, so the
	//      Email-page "Open in Webmail" arrow handed Roundcube
	//      garbage and produced the generic "Server Error:
	//      Internal error occurred" toast. New
	//      reencryptSyncedMailboxes pass mirrors the
	//      panel_mail.password_cipher re-encryption already in
	//      the same file (~line 2249): grep source's
	//      /opt/serverpanel/.env for JWT_SECRET, call
	//      EmailService.ReencryptForTransfer (which already
	//      existed but was never invoked) for each mailbox, and
	//      `$unset` the field when decrypt fails so the panel
	//      doesn't keep handing garbage to webmail. IMAP / SMTP
	//      login itself was already migration-safe because
	//      /etc/dovecot/users keys off the portable
	//      SHA512-CRYPT `password` hash, not encrypted_pass.
	//
	// No DB migration needed; existing data is healed by running
	// `bzpanel heal-www` (regenerates vhosts + reissues certs with
	// the inflated SAN list) and `bzpanel heal-dns` (backfills
	// any subdomain rows whose www CNAME never landed in pdns).
	// 3.1.62 (2026-05-31) — Mail receive + SSO survive a server
	// migration: Sieve `vnd.dovecot.pipe` extension actually
	// loaded, migrated maildirs chowned to vmail:vmail, and a
	// one-shot heal for SSO ciphertexts the source's JWT_SECRET
	// can't decrypt.
	//
	// Three independent gaps, all dropped on the floor by the
	// transfer pipeline up to 3.1.61, all surfaced together
	// post-cutover as "email not receiving" + "webmail auto-login
	// not working" (the bipvt.in / domain@betazeninfotech.com
	// flow flagged by a live install):
	//
	//   1. sieve_install.go's dovecot conf snippet enabled
	//      `+vnd.dovecot.pipe` in sieve_extensions but never
	//      loaded the plugin that provides it. The plugin .so
	//      ships with `dovecot-sieve` on Ubuntu 24.04
	//      (lib90_sieve_extprograms_plugin.so) but Dovecot only
	//      activates it when `sieve_plugins = sieve_extprograms`
	//      is present in the same plugin {} block. Without that
	//      line, every inbound delivery hit the after.d hook,
	//      Pigeonhole compile failed with "unknown Sieve
	//      capability vnd.dovecot.pipe", Dovecot LMTP returned
	//      451 4.2.0, and Postfix deferred. Mail piled up
	//      silently — operator only noticed when a customer
	//      mentioned "I'm not getting your replies." Added the
	//      one missing line; sievec now compiles the after.d
	//      script cleanly and webhook firing works as designed.
	//
	//   2. RebuildMailboxMaps wrote /etc/dovecot/users +
	//      virtual_mailbox_maps correctly post-transfer but
	//      never touched the maildir tree's filesystem
	//      ownership. The destination's tarball-extract step
	//      restored files as the SOURCE's uid:gid; on a server
	//      where the destination's hosting user got a different
	//      uid (the normal case — fresh installs allocate uids
	//      in order), the entire maildir ended up owned by the
	//      destination's <user>:<user> (matching uid happenstance)
	//      and Dovecot LMTP (which runs as vmail:vmail) couldn't
	//      write to <maildir>/tmp/. Every inbound delivery
	//      failed with "open(...tmp/...) failed: Permission
	//      denied". Now a `repairMaildirOwnership` pass runs at
	//      the tail of RebuildMailboxMaps: one
	//      `chown -R vmail:vmail /home/<u>/mail/` per unique
	//      hosting user (deduplicated via a set so a 12-domain
	//      vendor runs 1 chown not 12), plus chgrp vmail +
	//      chmod g+x on the parent /home/<u>/ for the traversal
	//      bit Dovecot needs to descend into the tree.
	//      Idempotent — repeated runs after the first are
	//      metadata-only walks. Same code path is reachable via
	//      `bzpanel heal-mailboxes` (existing) and a new
	//      `bzpanel heal-mail-perms` alias for operator
	//      discoverability.
	//
	//   3. encrypted_pass blobs that the v3.1.61
	//      reencryptSyncedMailboxes pass would have re-keyed
	//      under the destination's JWT_SECRET are stranded on
	//      every install that migrated BEFORE 3.1.61 deployed.
	//      The panel's "Open in Webmail" handler decrypts the
	//      stale blob with the destination's secret, gets
	//      garbage bytes back, hands them to Roundcube as the
	//      mailbox password, and the user sees "Server Error:
	//      Internal error occurred" with a re-rendered login
	//      form. New EmailService.HealStaleSSOEncryption walks
	//      every mailbox, attempts a decrypt with the current
	//      JWT_SECRET, and $unsets the column for any row where
	//      decrypt fails (so the panel UI surfaces a clean "Set
	//      password to enable SSO" CTA matching the never-had-
	//      SSO case). Refuses to clear when JWT_SECRET is empty
	//      so a misconfigured destination doesn't silently nuke
	//      the column for the entire install. Exposed via
	//      `bzpanel heal-mail-sso`; IMAP/SMTP login is untouched
	//      throughout (the portable SHA512-CRYPT hash on the
	//      password field is what those use).
	//
	// No DB migration needed. Operator post-3.1.62 deploy:
	//   bzpanel heal-mailboxes   # picks up the chown fix
	//   bzpanel heal-mail-sso    # one-shot, idempotent
	//
	// Future migrations: the sieve fix is in the rendered conf
	// every fresh install will write; the chown is part of every
	// RebuildMailboxMaps call which the transfer pipeline already
	// invokes post-sync; and the SSO re-encryption pass already
	// ships in 3.1.61.
	// 3.1.63 (2026-05-31) — Deploy-Software GitHub webhook
	// survives a server migration: repo-URL fallback when the
	// route's :project_id no longer exists, and the slow git
	// pull moved off the request path so GitHub doesn't time out.
	//
	// Two bugs the user surfaced on the MongoDB-Panel project
	// post-migration, in the same delivery sequence:
	//
	//   1. Source's webhook URL embedded the SOURCE project's
	//      ObjectID. The transfer pipeline re-mints every
	//      project's _id during sync (idMap in
	//      transfer_panel_records.go) so the destination has a
	//      DIFFERENT id for the same project + same git_repo_url.
	//      GitHub's webhook URL — once configured on the repo —
	//      never changes, so post-cutover every delivery hit a
	//      "project not found" route and the redeploy never ran.
	//      Pre-3.1.63 the handler returned 200 with
	//      {"ignored":"project not found","success":false} so
	//      GitHub showed the delivery as succeeded (HTTP 2xx)
	//      AND nothing redeployed AND the operator's
	//      last_webhook_at never advanced — the diagnostic
	//      experience was uniquely bad.
	//
	//      New resolveProjectForWebhook falls back to a repo-URL
	//      match when the id lookup misses: extracts
	//      Repository.CloneURL / SSHURL / HTMLURL / FullName
	//      from the GitHub payload, canonicalises every shape
	//      (https/.git/credentials/SSH) to a single
	//      "host/owner/repo" form via canonicaliseRepoURLs, and
	//      matches against every Project.GitRepoURL on the box.
	//      Returns the matching project; refuses to pick when
	//      multiple projects share the same repo (rare — two
	//      deploy targets, same source — but possible). Same
	//      callable surface for the App webhook path is a
	//      future-3.1.64 follow-up; for now App webhooks use
	//      the per-app WebhookID hex which doesn't change
	//      across migrations (it's already a stable token,
	//      not the ObjectID).
	//
	//   2. HandleWebhook ran inPlaceSync (project-level
	//      git pull) synchronously before returning to GitHub.
	//      A 5-second pull on a slow upstream blew past
	//      GitHub's 10-second webhook timeout, GitHub closed
	//      the TCP connection mid-write (nginx logs status
	//      499, body 0), the delivery showed as "Failed" in
	//      the GitHub UI, AND GitHub retried on a backoff
	//      curve — which queued duplicate deploys when the
	//      retry succeeded. The App webhook handler
	//      (handlers/webhook_handler.go) already fire-and-
	//      forgets the redeploy for the same reason; this
	//      brings the project path into line via a new
	//      runProjectPullAndEnqueue goroutine that captures
	//      its own background context (so request cancellation
	//      on the inbound webhook doesn't kill the pull
	//      halfway). Per-row state — last_commit_sha,
	//      last_webhook_at — stamps on the same Mongo cadence
	//      it did before so the WHM "Deploy Software" page's
	//      live progress UI isn't affected.
	//
	// No DB migration needed; the new fallback is purely
	// destination-side decode logic + a Find on Projects. The
	// operator's stale GitHub webhook URL "just works" after
	// the v3.1.63 deploy lands — no need to log into GitHub
	// and rewrite Payload URL on every migrated repo. Updating
	// to the new (destination's) URL is still encouraged for
	// hygiene; both URLs route to the same project after the
	// fix.
	// 3.1.64 (2026-05-31) — Operator-facing rotate controls for
	// the Deploy-Software project webhook + clearer post-action
	// notifications.
	//
	// New: POST /projects/:id/regenerate-webhook-secret mints a
	// fresh 32-byte hex HMAC secret, persists it, and returns the
	// plaintext value in the response body so the WHM + cPanel
	// Deploy-Software project drawer can render a one-click
	// rotate. Pre-3.1.64 the only way to rotate a leaked or
	// migrated secret was to edit Mongo directly — which left
	// the webhook URL valid, the panel-side verification valid,
	// and GitHub's signature header still computed with the OLD
	// secret (no operator-discoverable surface for "your secret
	// just leaked, rotate it").
	//
	// UI:
	//   • Two-click confirm so a single misclick can't kill a
	//     working webhook — first click swaps the button into a
	//     5-second amber "Confirm" affordance, second click fires.
	//     Outside the 5s window state resets and the next click
	//     starts fresh.
	//   • On success, the new secret is auto-copied to clipboard
	//     AND surfaced in a 30-second persistent toast with the
	//     value rendered, a Copy button, an Open-GitHub-webhooks
	//     deep-link (for git_repo_url that starts with
	//     https://github.com/), and a Dismiss button. The toast
	//     spells out "old secret is gone — GitHub deliveries
	//     will fail signature verification until you update the
	//     webhook's Secret field" so the operator knows the
	//     follow-up step isn't optional.
	//
	// Tangential: the existing PAT-rotate toast was a one-word
	// "PAT rotated" that left operators wondering whether
	// webhook deliveries still worked, whether they had to
	// re-test the next deploy, etc. Now it spells out exactly
	// what changed (clone/pull token) AND what didn't (webhook
	// URL + secret unchanged) AND offers a one-click "Run test
	// deploy" CTA so the operator can verify the new token
	// clones cleanly without leaving the page. Mirrored to the
	// cPanel surface so vendors get the same UX.
	//
	// No DB migration; webhook_secret has always been a plain
	// string field on the project document — RegenerateWebhook
	// Secret is just a fresh value written into the same column.
	// 3.1.65 (2026-05-31) — Two migration smoke-test follow-ups:
	// TXT records actually land in PowerDNS post-rehydrate, and
	// the v3.1.62 sieve_plugins conf rolls out without needing
	// a fresh mailbox create.
	//
	// Both surfaced when smoke-testing the migration pipeline
	// against the user's live install:
	//
	//   1. RebuildPowerDNSFromMongo emitted TXT values verbatim
	//      to `pdnsutil replace-rrset` — bare
	//      `v=spf1 ip4:1.2.3.4 ~all`. pdnsutil's replace-rrset
	//      (unlike add-record) is strict: it rejects unquoted
	//      TXT data with "Data field in DNS should start with
	//      quote". Every SPF / DKIM / DMARC TXT row on a
	//      transferred zone landed in Mongo correctly but
	//      silently dropped at the pdnsutil step — the
	//      per-zone warn log fired ("replace-rrset failed") but
	//      the aggregate summary still reported `failed:0`
	//      because the counter was never incremented, giving
	//      the operator a false "all clean" signal. Now TXT
	//      values are wrapped in `"..."` (escaping any embedded
	//      quotes for DKIM's `p="..."` shape) AND res.Failed
	//      increments on every replace-rrset error so the
	//      summary's `failed` column matches the per-zone
	//      logs.
	//
	//   2. EnsureMailHookInstalled (which writes
	//      /etc/dovecot/conf.d/95-betazen-sieve.conf with the
	//      v3.1.62 `sieve_plugins = sieve_extprograms` line)
	//      only fired from CreateMailbox's post-success
	//      goroutine. An install upgraded to v3.1.62+ that
	//      didn't subsequently create a fresh mailbox kept its
	//      pre-3.1.62 conf forever — every inbound delivery
	//      hit the after.d hook, Sieve compile failed
	//      ("unknown Sieve capability vnd.dovecot.pipe"),
	//      Dovecot LMTP returned 451, Postfix deferred.
	//      RunAllRehydrates now calls EnsureMailHookInstalled
	//      after the mailbox + forwarder map rebuild, so any
	//      install that ever runs `bzpanel heal-after-transfer`
	//      OR the transfer panel-records sync gets the new
	//      conf immediately. Errors are swallowed so a
	//      webhook-helper failure doesn't abort the rest of
	//      the rehydrate (DNS, MySQL, FTP, WordPress).
	//
	// No DB migration; both fixes are pure code paths run by
	// the existing rehydrate orchestrator.
	// 3.1.66 (2026-05-31) — Deploy-Software webhook actually
	// looks like it worked: deployments finalize as "success"
	// instead of "running", and the per-deployment commit_sha
	// stops landing empty.
	//
	// Two long-standing bugs the user surfaced when staring at
	// the Restro Dev project drawer post-push, both in
	// project_service.go's runDeploy:
	//
	//   1. The happy-path finalize at end of runDeploy passed
	//      status="running" instead of "success". Every deploy
	//      that completed cleanly stayed at status="running"
	//      forever in project_deployments + on the project_
	//      services row — 796 rows on the user's install. The
	//      WHM Deploy-Software activity list rendered finished
	//      deploys with the in-progress spinner indefinitely
	//      (the frontend has a fallback at line 2978 of
	//      DeploySoftwarePage.tsx that treats
	//      `status==="running" && finished_at!=nil` as success,
	//      but the success-count badge and per-row color all
	//      keyed off raw status and showed wrong). Now
	//      finalize("success", "", commit) — matching the
	//      string the frontend's positive-path renderer
	//      expects at DeploySoftwarePage.tsx:2770.
	//
	//   2. `git -C gitOpsDir rev-parse HEAD` (used to capture
	//      the commit_sha on the deployment row) ran as root
	//      against a repo owned by the project's hosting
	//      linux user. git 2.35+ refuses with "fatal: detected
	//      dubious ownership in repository" when uid doesn't
	//      match — confirmed `exit 128` on the user's box.
	//      Every deploy left commit_sha="" on the
	//      project_deployments row, even though the SERVICE's
	//      last_commit_sha got set correctly via
	//      runProjectPullAndEnqueue's UpdateMany (which masked
	//      the bug — services looked right, only the per-
	//      deploy record was missing). Added the same
	//      `-c safe.directory=<dir>` prefix the inPlaceSync
	//      path's safeArgs helper has used since v3.0.31.
	//
	// New CLI: `bzpanel heal-deployments` (alias
	// repair-deployments) retro-fixes pre-3.1.66 rows on
	// upgrade — relabels every status="running" row that has
	// finished_at set + no error_msg to status="success", AND
	// backfills commit_sha from the matching service's
	// last_commit_sha when the deployment row's own commit_sha
	// is empty. Idempotent.
	//
	// No DB migration — the schema didn't change; only the
	// values being written.
	// 3.1.67 (2026-06-01) — `bzpanel deploy` / `bzpanel rebuild`
	// now refreshes the dovecot sieve hook conf as the last step
	// of every upgrade.
	//
	// The v3.1.62 fix that added `sieve_plugins = sieve_extprograms`
	// to /etc/dovecot/conf.d/95-betazen-sieve.conf (so the
	// vnd.dovecot.pipe extension actually loads and inbound mail
	// compile stops failing) only fired from CreateMailbox's
	// post-success goroutine. The v3.1.65 fix wired it into the
	// transfer rehydrate path (RunAllRehydrates) but left a gap:
	// an install upgraded via plain `bzpanel deploy` that never
	// migrates and never creates a fresh mailbox would keep its
	// pre-3.1.62 conf forever, and inbound delivery would silently
	// defer with "451 sieve: Failed to compile script" until the
	// operator either created a mailbox OR ran
	// `bzpanel heal-after-transfer` — both undiscoverable.
	//
	// cmdRebuild now calls agent.EnsureMailHookInstalled after the
	// systemd restart so every operator-issued upgrade rolls out the
	// current sieve template. Idempotent (writeFileSecure atomic-
	// renames). Best-effort — a failure prints a `! sieve hook
	// refresh: <err>` warning but doesn't fail the rebuild, because
	// a webhook-helper hiccup shouldn't block the operator's deploy
	// of the actual app. Adds ~50 ms to a no-op rebuild (one apt
	// idempotent check + one writeFileSecure + one sievec + one
	// dovecot reload).
	// 3.1.68 (2026-06-01) — Manual "Deploy all" does ONE git
	// pull instead of N, matching the webhook flow's shape.
	//
	// User flagged it on the Waapi Dev 3.0 project drawer: 7
	// services on one shared clone, "Deploy all" button kicked
	// off seven `git fetch + reset --hard` against the same
	// directory back-to-back — pull 1 took 2.9 s, pull 2 took
	// 20.3 s (filesystem-lock contention against service 1's
	// npm install warming the same tree), pulls 3–7 cumulative
	// ~60 s of redundant network + disk work BEFORE the first
	// real build started. Worst case: a `git push` landing
	// between pull 1 and pull 7 left the 7 services on
	// DIFFERENT commits, which is a real correctness bug —
	// `last_commit_sha` per row would look fine but the deploy
	// is internally inconsistent.
	//
	// Pre-3.1.68 DeployAll iterated services and enqueued each
	// with `skipPull=false`, so every service's runDeploy did
	// its own inPlaceSync. The webhook path
	// (HandleWebhook → runProjectPullAndEnqueue) has done the
	// right thing since v3.1.63: pull once at proj.ProjectDir,
	// UpdateMany last_commit_sha on every service row, then
	// enqueue each service with `skipPull=true`. v3.1.68
	// extracts that flow as a shared helper (added a `trigger`
	// param so DeployAll stamps "manual" and HandleWebhook
	// keeps stamping "webhook") and the manual button now uses
	// it too.
	//
	// Legacy split-clone projects (no proj.ProjectDir / no
	// proj.GitRepoURL — rare; pre-shared-clone-refactor
	// installs that haven't been re-provisioned) fall back to
	// the pre-3.1.68 per-service-pull behaviour so they keep
	// working. No DB migration; pure code-path refactor.
	//
	// Side benefits beyond raw wall-clock:
	//   • Per-deployment row's commit_sha is now identical
	//     across every service in one "Deploy all" batch
	//     (post-v3.1.66 safe.directory fix lets runDeploy's
	//     `git rev-parse HEAD` succeed against the shared
	//     clone, and the project-level pull just settled the
	//     HEAD, so every concurrent rev-parse reads the same
	//     value).
	//   • Worker queue back-pressure shows up sooner — N - 1
	//     redundant pulls no longer hide a real stuck npm
	//     install behind them.
	// 3.1.69 (2026-06-01) — Per-service deploy progress shows
	// the project-level git pull as COMPLETED, not "skipped".
	//
	// User flagged it on the Waapi Dev 3.0 project drawer:
	// webhook hit at 08:15:34 UTC, all 7 services correctly
	// deployed against the latest commit (ca667923... — git
	// HEAD on disk matches every service's last_commit_sha,
	// last_webhook_at recorded, last_deployed_at within a few
	// minutes of the webhook), but each per-service progress
	// strip showed "Pull source from Git — skipped". Looking
	// at 7 'skipped' labels, the operator reasonably concluded
	// "the webhook didn't pull anything" — even though the
	// project-level pull HAD landed the new commit 30 seconds
	// earlier in the runProjectPullAndEnqueue goroutine.
	//
	// The "skipped" message was wrong for THIS case. It made
	// sense for the per-service Redeploy button (operator
	// explicitly NOT fetching new commits, just rebuilding on
	// existing on-disk source), but for webhook fan-out and
	// manual "Deploy all" — where a project-level pull DID
	// just run — it left the operator unable to tell which
	// case they were looking at.
	//
	// New `projectPullCommit` field on deployJob carries the
	// HEAD sha through from runProjectPullAndEnqueue to
	// runDeploy. When set, runDeploy now calls
	// completeStep(0, "Pulled at project level — commit XXX
	// (one git fetch shared across every service in this
	// project)") so the operator sees a green checkmark with
	// the actual commit hash, NOT an ambiguous "skipped". The
	// per-service Redeploy button still goes through the
	// original skipStep() path because no project-level pull
	// runs there. Also pre-seeds the deployment row's
	// commit_sha from the same value, so a transient
	// rev-parse failure post-v3.1.66 no longer leaves
	// commit_sha empty when we already know the right value.
	//
	// New `enqueueWithCommit` helper threads the field
	// through alongside the existing `enqueue`. No other
	// callers needed updating.
	Major = 3
	Minor = 1
	Patch = 69
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
