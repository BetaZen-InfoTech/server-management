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
	Major = 3
	Minor = 1
	Patch = 33
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
