# Change Log — Betazen Server Panel v3.1.107 → v3.1.108 (2026-06-28)

Every modification made during the audit/upgrade/migration engagement. Code changes are in
this local repo and **deployed + verified on S1 (89.116.34.207)**. Production (195.35.7.161)
and S2 (195.35.7.64) deploy pending (see PRODUCTION_DEPLOY_RUNBOOK.md).

## A. NEW FEATURE — Source-agnostic Mail Log (the reported "3rd-party client mail not logged" bug)
Root cause: the panel only observed mail via the Dovecot Sieve **delivery** hook, which fires
only for messages landing in a local maildir — structurally blind to outbound mail, authenticated
submission from 3rd-party clients (Thunderbird/Outlook/mobile), webmail, and API/sendmail mail.
No structured mail-log collection existed.

Fix — new ingestor that tails Postfix's own log (the one authoritative record of EVERY message):
- `backend/internal/models/mail_log.go` (NEW) — `MailLogEntry`, `MailLogRecipient`, `MailLogStats`.
- `backend/internal/services/mail_log_service.go` (NEW) — `tail -F /var/log/mail.log` ingestor,
  per-queue-id correlation (smtpd→cleanup→qmgr→smtp/lmtp/local/virtual→removed), source
  classification (webmail / smtp-client / api-local / inbound-smtp), tenant-scoped `List`/`Stats`
  read API, and `EnsureHeaderChecks` (installs a **regexp** Subject/Content-Type WARN map).
- `backend/internal/handlers/mail_log_handler.go` (NEW) — `GET /email/logs`, `GET /email/logs/stats`.
- `backend/internal/database/collections.go` — added `ColMailLogs = "mail_logs"`.
- `backend/internal/database/indexes.go` — `mail_logs` indexes (unique `log_key`, `first_seen`,
  `status`/`direction`/`source`/`domains` facets, **90-day TTL** on `created_at`).
- `backend/internal/routes/whm_routes.go` + `cpanel_routes.go` — registered the two routes
  (static, before `/:id`; owner sees all, tenant scoped to its domains).
- `backend/cmd/server/main.go` — construct `MailLogService`, `StartIngestor(metricsCtx)`,
  construct handler, add `MailLog` to `WHMHandlers`.
- Captured fields: timestamp, source, client, IP, auth user/method, sender, recipients[]
  (address/status/dsn/relay/response/delay/delivered_at), subject, message-id, content-type,
  has_attachments, size, nrcpt, status (sent/deferred/bounced/rejected/received/queued),
  smtp_response, queued, domains, server_ip, hostname.
- **Tested on S1**: api-local (sendmail), webmail (loopback auth), inbound-smtp (external :25),
  smtp-client (external :587 auth — Thunderbird-like), outbound→external (bounced+SMTP response),
  multipart attachment detection, message-id — ALL captured with correct classification.

### Incident + hardening during testing
- First attempt wired `header_checks = pcre:` — **stock Postfix has no pcre map support**
  (`unsupported dictionary type: pcre`) and it temp-failed ALL mail (451). Hotfixed the live
  server to `regexp:` (built-in), then hardened `EnsureHeaderChecks` to use `regexp:`, validate
  the map with `postmap -q` BEFORE wiring it, strip any stale entry, and roll back on reload
  failure — so it can never break mail flow. Re-verified mail delivery after.

## B. USER-REPORTED — MongoDB database creation re-enabled ("I am not able mongo")
Root cause: `database_service.go` explicitly disabled MongoDB creation since v3.0.19 because the
panel's `serverpanel` Mongo user is DB-scoped (`readWrite`+`dbAdmin` on `serverpanel` only) →
"not authorized" creating tenant DBs. The frontend dropdown only offered MySQL as a result.
But install.sh ALSO creates an `admin` user with the `root` role using the same `MONGO_PASS`.

Fix:
- `backend/internal/agent/mongodb.go` — `mongoEval` now derives an **admin** URI (swap username
  to `admin`, target `/admin?authSource=admin`, same password) so provisioning has the needed
  privileges. `CreateMongoDatabase` now creates the owning user as **dbOwner** AND an initial
  `data` collection so the DB materializes/lists immediately.
- `backend/internal/services/database_service.go` — removed the "temporarily disabled" stub;
  re-enabled the `mongodb` case (create + connection string `mongodb://…?authSource=<db>`); added
  `isSafeDBIdent` whitelist (`^[A-Za-z0-9_]{1,64}$`) to prevent JS injection via db/user name.
- `frontend/apps/whm/src/pages/DatabasesPage.tsx` + `apps/cpanel/src/pages/DatabasesPage.tsx` —
  added `<option value="mongodb">MongoDB</option>` and contextual help text.
- **Tested on S1**: create (db + dbOwner user + initial collection + connection string), tenant
  connects & writes, lists in API, delete drops the db — full lifecycle works.

## C. SECURITY / BUG FIXES (from the 12-area audit)
- **CRITICAL — SQL injection** `backend/internal/agent/mysql.go`: db/user/host were interpolated
  unescaped into `mysql -e` running as MariaDB root; the user-chosen PASSWORD was an injection
  sink. Added identifier whitelisting (`^[A-Za-z0-9_]{1,64}$`), host validation, and
  `mysqlQuoteLiteral` escaping for password/value literals across all functions.
- **HIGH — RefreshToken bypass** `backend/internal/services/auth_service.go`: `RefreshToken` now
  rejects soft-deleted (`DeletedAt`) and brute-force-locked (`LockedUntil`) accounts, matching
  `Login`/`Me` (previously a locked/trashed account could keep minting access tokens for ~30d).
- **HIGH — global rate-limit bucket** `backend/cmd/server/main.go`: Fiber now
  `EnableTrustedProxyCheck` + `TrustedProxies=[127.0.0.1,::1]` + `ProxyHeader=X-Forwarded-For`,
  so `c.IP()` resolves the real client behind nginx and the WHM/cPanel/login limiters work
  per-IP instead of collapsing every request into one `127.0.0.1` bucket.
- **HIGH — SSRF** new `backend/internal/services/ssrf_guard.go`: outbound webhook + notification
  HTTP clients now use a DNS-rebinding-safe dialer that refuses loopback/RFC1918/link-local/
  metadata targets; wired into `webhook_service.go` + `notification_service.go`.
- **MEDIUM — wrong systemd unit** `email_service.go` + `agent/email_install.go`: `spamassassin`
  → `spamd` (the real unit on Ubuntu 24.04; spam-settings reload + install enable were no-ops).
- **MEDIUM — wrong metrics index** `database/indexes.go`: `ColMetrics` indexed `collected_at`
  but docs store `timestamp` → every metrics read + the retention DeleteMany was a COLLSCAN.
  Fixed to `timestamp`.
- **LOW — error-code mislabel** `main.go` `customErrorHandler`: derive `error.code` from the
  HTTP status (NOT_FOUND/UNAUTHORIZED/FORBIDDEN/…) instead of always `INTERNAL_ERROR`.
- **BUG — forced logout / wrong login redirect** `frontend/packages/api-client/src/client.ts`:
  added single-flight refresh (coalesces concurrent 401s so the rotating refresh token doesn't
  self-invalidate — the "logged out after a while" symptom) and made the post-refresh-failure
  redirect surface-aware (`/user-panel/login` vs hardcoded `/whm/login`, which stranded non-owner
  roles using the shared client on Dashboard/Profile/Sessions).
- `backend/pkg/version/version.go` — bumped to 3.1.108 with a full changelog entry.

## D. S1 SYSTEM / CONFIG HARDENING (applied live on 89.116.34.207)
- Added **8 GiB swap** + `vm.swappiness=10` + `vm.vfs_cache_pressure=50` (was: no swap).
- **THP=never** via a `disable-thp.service` ordered before mongod (MongoDB recommendation).
- Applied **21 pending security updates** (→ 0); tuned unattended-upgrades
  (`Download-Upgradeable-Packages=1`, `AutocleanInterval=7`); `apt-get clean` (reclaimed ~553M).
- Removed/curtailed dead services: purged `snapd` (0 snaps) + `telnet`/`inetutils-telnet`;
  masked `ModemManager` + `multipathd` (single-disk VPS). (`openbsd-inetd` had to stay — it's a
  hard dependency of `pure-ftpd`; the audit's "remove inetd" advice does not apply here.)
- **pure-ftpd TLS required** (`/etc/pure-ftpd/conf/TLS=2`) + ensured a TLS cert exists.
- **nginx security headers** (X-Frame-Options, X-Content-Type-Options, Referrer-Policy) +
  `server_tokens off` via `/etc/nginx/conf.d/zz-betazen-security.conf` (verified externally).
- **PowerDNS** `default-soa-edit=INCEPTION-INCREMENT` so zone serials auto-advance.
- Pre-compiled the Sieve `.svbin` (dovecot `ProtectSystem=full` can't write `/etc` at delivery
  time → transient "read-only" warning on first-mailbox-create; non-fatal, delivery succeeds).

## E. Operational artifacts created
- `scripts/` helpers (session-local) for SSH/SFTP; production backups under
  `/opt/serverpanel/_pre108_backup/` on 195.35.7.161 (mongodump, mysqldump, binary, .env, configs).
- Rollback: each deploy keeps `bin/server.bak.<ts>`; source revertible via git + the backups.
