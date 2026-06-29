# 16 — Complete Change Log

Chronological record of every change made during the 2026-06-28 autonomous run. Source changes
are committed to the working tree (panel `v3.1.109 → v3.1.111`); server changes applied live to
both `89.116.34.207` (S1) and `195.35.7.64` (S2).

## Code (repo working tree + deployed binary)

| Version | File | Change |
|---------|------|--------|
| 3.1.110 | `pkg/validator/validator.go` | + `IsSafeDNSName`, `IsSafeEmail` (shell-safe allowlist validators) |
| 3.1.110 | `internal/services/email_service.go` | `CreateMailbox`: validate email/domain before shell sinks |
| 3.1.110 | `internal/services/domain_service.go` | `Create`: normalise+validate domain before shell sinks |
| 3.1.110 | `internal/services/dns_service.go` | `CreateZone`: validate zone name before shell sinks |
| 3.1.110 | `internal/services/config_service.go` | `UpdateHostname`: validate hostname before `/etc/hosts` sed |
| 3.1.110 | `internal/services/mail_log_service.go` | flusher keep-non-removed + `lastFlush` dedup + 1h cap (B-04); List/Stats error propagation (B-13) |
| 3.1.110 | `internal/services/transfer_service.go` | `detectSourceIP` apex-preferred (B-12) |
| 3.1.110 | `internal/routes/cpanel_routes.go` | `/backups/schedules` before `/backups/:id` (B-08) |
| 3.1.110 | `internal/agent/mail_suite_install.go` | installer path `/opt/serverpanel` (B-09) |
| 3.1.110 | `pkg/version/version.go` | bump + changelog |
| 3.1.111 | `internal/services/transfer_panel_records.go` | `recoverApp` re-stamps `Environment=PORT` (B-05) |
| 3.1.111 | `pkg/version/version.go` | bump + changelog |

Build: `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`; deployed by
SFTP upload + `mv` swap + `systemctl restart serverpanel`; prior binary saved as
`bin/server.bak-pre-audit`. Both servers verified on `3.1.111`.

## Server config (both servers unless noted)

- S2: rewrote `/var/spool/postfix/etc/resolv.conf` (127.0.0.53 → 8.8.8.8/8.8.4.4) + reload (B-01).
- Added `/etc/nginx/snippets/security-headers.conf` + include in panel vhost; `nginx -t` + reload.
- Added `/etc/fail2ban/jail.d/betazen-mail.conf` (postfix-sasl, dovecot, pure-ftpd); reload.
- Symlinked `/usr/local/go` + `/usr/local/bin/{go,gofmt}` → `/opt/go/1.23` (B-15).
- Rotated `admin@betazeninfotech.com` password `admin123` → `Betazen!Demo-2026#Kx9pQ2` (B-02).
- S2: stamped `Environment=PORT=8091` on `sp-app-demo-erp.service` (repair of B-05 instance).
- S1: removed stray `test.local` from postfix maps + opendkim + DKIM dir (injection-probe cleanup).

## Demo data created on S1 (then migrated to S2)

- 3 vendor_admin accounts (+ Linux users/homes): demoone, demotwo, internaldemo.
- 6 domains (vhost+zone+mail), 6 DNS zones / 114 records (A/AAAA/MX/SPF/DKIM/DMARC/TXT/CNAME/CAA/NS/SOA).
- 42 mailboxes (varied quota/limits), 36 forwarders (incl. one-to-many + chained).
- 6 apps (Go/Node×3/Python/static) as systemd services + reverse proxy.
- 6 standalone systemd demo services (worker/queue/scheduler/monitor/api/web) under `/opt/betazen-demo`.
- 1 Deploy-Software project (`betazen-demo-platform`).
- Demo MongoDB `demoone_appdata` (8 collections / 109 docs); Demo MySQL `demotwo_appdb` (8 tables).
- Hygiene: 12 alias forwarders recreated `keep_copy=false` (B-14); 3 spurious `@localhost` customer
  rows removed on S2 (B-06).

## Migration (S1 → S2)

- Native transfer: test-connection → discover → create (all data components) → 13/13 steps → rehydrate-all.
- Manual: `demoone_appdata` mongodump→restore (109 docs); `betazen-demo-*` services recreated.

## Tests executed (non-mutating validation)

- Injection probes (rejected, no exec); internal+remote authenticated SMTP (delivered+logged);
  IMAP/POP3 folder/search/flag/trash/restore/concurrent-login; Roundcube login page;
  outbound-to-Gmail SMTP path; ~120 read-only API calls per server; full S1↔S2 parity diff.

## Round 2 — deep bug-hunt (v3.1.112)

A follow-up request ("deeply check the whole project + previous docs, fix all bugs") drove a second
multi-agent hunt (10 finders + per-candidate adversarial verification) → **29 confirmed bugs fixed**
(4 critical, 13 high, 8 medium, 4 low), shipped as **v3.1.112** to both servers. Full table +
verification in `17_DEEP_BUGHUNT_REPORT.md`; per-bug patches in `reports/deep-bughunt-fixes-detail.md`.

Code touched (24 files, applied via a file-partitioned fix workflow then central `go build`/`go vet`):
email_service.go (forwarder + spam-settings injection guards), auth_service.go (empty-refresh +
OTP-lockout), middleware/auth.go (transient-error caching), resource_service.go + resource_handler.go
(bandwidth shellQuote + DomainUsage scope), transfer_panel_records.go (B-06/B-07/B-07b/#15/#18),
mail_log_service.go (backfill tail + recoverFirstSeen + mutex), terminal_handler.go (suspend recheck),
dns_service.go (UpdateRecord aux fields + CAA), database_service.go + models/database.go (#12/#13/#17/#21),
project_service.go (#14/#23), deploy_service.go (#22), ssl_service.go (#16/#24), ws_handler.go + main.go
(install-WS auth), validator.go (widen IsSafeEmail), version.go.

Server-side this round: DB `owner` backfill on both (demoone_appdata→demoone, demotwo_appdb→demotwo);
WHM frontend rebuilt (`index-Dq0pbI4j.js`) + dist deployed to both (old dist saved as
`dist.bak-pre-v3112`); binary hot-swapped (prior saved as `bin/server.bak-pre-audit`).

## Artifacts

Reports under `SERVER_AUDIT_2026-06-28/` (`00`,`07`,`10`–`16` + `reports/*.md`). Helper scripts and
the built binary are in the session scratchpad (not committed).
