# 13 — Fixed Issues Report

Everything changed this run, with exact locations. **No working feature was rewritten** — changes
are limited to confirmed bug/security/config fixes. The panel was rebuilt **v3.1.109 → v3.1.111**
and redeployed to both servers (binary swapped via `mv` + `systemctl restart`, prior binary kept as
`/opt/serverpanel/bin/server.bak-pre-audit`).

## A. Server configuration fixes (no rebuild) — applied to both servers

| Fix | Detail |
|-----|--------|
| **Postfix chroot DNS (S2, critical)** | `/var/spool/postfix/etc/resolv.conf` was the systemd stub `127.0.0.53`; replaced with the host's real `8.8.8.8/8.8.4.4` + `postfix reload`. Verified `dig MX gmail.com` resolves and outbound to Gmail connects. (S1 was already correct.) |
| **nginx security headers (high)** | Added `/etc/nginx/snippets/security-headers.conf` (X-Frame-Options=SAMEORIGIN, X-Content-Type-Options=nosniff, Referrer-Policy, X-XSS-Protection) included in the panel vhost. No CSP/HSTS (would risk the SPA / no TLS). Verified live on `/` and `/whm/`. |
| **fail2ban mail jails (medium)** | Added `jail.d/betazen-mail.conf` (postfix-sasl, dovecot, pure-ftpd). Jails now: sshd, postfix-sasl, dovecot, pure-ftpd. |
| **Go build PATH (config)** | Go was at `/opt/go/1.23` but the panel's app-build shell expects `/usr/local/go/bin`; symlinked `/usr/local/go` + `/usr/local/bin/{go,gofmt}` on both. |
| **Admin password rotation (critical)** | `admin@betazeninfotech.com` rotated from `admin123` → `Betazen!Demo-2026#Kx9pQ2` via `POST /auth/me/password` on both; old password now rejected. |

## B. Code fixes (rebuilt + redeployed)

| File | Change |
|------|--------|
| `pkg/validator/validator.go` | **NEW** `IsSafeDNSName` / `IsSafeEmail` shell-safe validators (allowlist charset; reject quotes/`;`/`$`/backtick/space). |
| `internal/services/email_service.go` | `CreateMailbox`: reject non-shell-safe email/domain before the `sed`/`echo` into dovecot/postfix maps (closes injection §8a). |
| `internal/services/domain_service.go` | `Create`: normalise + reject non-shell-safe domain before `rm -f /run/php/*-fpm-<domain>.sock` etc. (§8c). |
| `internal/services/dns_service.go` | `CreateZone`: reject non-shell-safe zone name before opendkim/postfix/pdnsutil shell-outs (§8b). |
| `internal/services/config_service.go` | `UpdateHostname`: reject non-shell-safe hostname before the `/etc/hosts` `sed` (§8d). |
| `internal/services/mail_log_service.go` | **B-04**: idle-flusher keeps non-`removed` entries in memory (preserves `first_seen`→same `log_key`→one row), `lastFlush` dedup, 1 h hard-age cap. **B-13**: `List`/`Stats` propagate the `TenantDomains` error. |
| `internal/services/transfer_panel_records.go` | **B-05**: `recoverApp` re-stamps `Environment=PORT=<app.port>` into the rebuilt systemd unit (matches original deploy). |
| `internal/services/transfer_service.go` | **B-12**: `detectSourceIP` prefers the apex `A` record (falls back to first-A) so the apex isn't left pointing at the source after cutover. |
| `internal/routes/cpanel_routes.go` | **B-08**: register `/backups/schedules` before `/backups/:id`. |
| `internal/agent/mail_suite_install.go` | **B-09**: `findMailSuiteSources` searches the real `/opt/serverpanel` (+ legacy `/opt/server-panel`). |
| `pkg/version/version.go` | Version → 3.1.110 then 3.1.111 with changelog entries for the above. |

## C. Demo-data hygiene fixes

- Recreated 12 alias-only forwarders with `keep_copy=false` (B-14) to stop the kept-copy bounce.
- Removed a stray `test.local` email domain + DKIM dir left by an injection probe (S1).
- Removed 3 spurious `<user>@localhost` customer rows on S2 (B-06) for exact user parity.

## Verification of fixes

- Injection: `domain="evil;touch /tmp/pwned"` and `email="o'brien@…"` / `x$(id)@…` all **rejected**;
  no command executed; clean demo names still pass.
- Mail logging: remote third-party submission captured as `source=smtp-client` with all fields.
- Migration: demo-erp serves :8091; all apps/services running on S2.
- Security headers + chroot DNS + fail2ban jails + password rotation all verified live on both.
