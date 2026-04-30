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
	Major = 3
	Minor = 0
	Patch = 53
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
