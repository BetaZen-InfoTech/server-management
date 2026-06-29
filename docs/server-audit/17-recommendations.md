# 17 — Remaining Recommendations

**Date:** 2026-06-29  Prioritized actions not performed this run (gated for human sign-off,
out of safe-auto-fix scope on a working demo, or production-only). Each includes the concrete fix.

## P1 — Do before any real (non-demo) use

| # | Area | Recommendation | How |
|---|------|----------------|-----|
| R1 | **TLS for the panel** | The panel, phpMyAdmin, Roundcube, and File Manager are served over **plaintext HTTP** — logins + JWTs travel in the clear. | Issue a cert for the panel hostname and add a `listen 443 ssl` block (the certbot ACME webroot is already wired). For `.local` demo, a self-signed cert + redirect still removes cleartext. |
| R2 | **SSH exposure** | Root login with password auth is open to the internet (fail2ban only partially mitigates). | Set `PermitRootLogin prohibit-password`, `PasswordAuthentication no`, install an admin key first; or restrict port 22 to known IPs. |
| R3 | **Rotate demo credentials** | Documented demo passwords (owner `Betazen!Demo-2026#Kx9pQ2`, mailboxes `M@ilbox2026!`, vendors `Demo!Pass2026`) are known. | Rotate all before real data lands. |
| R4 | **Whole-server backups** | No backup is configured on either box; `/var/backups/serverpanel` is empty. | Schedule a daily off-site bundle: `mongodump --db serverpanel`, `/opt/serverpanel/.env`, `mysqldump --all-databases`, `/var/mail/vhosts` (or `/home/*/mail`) + `/etc/dovecot/users`, `/etc/opendkim/keys`, PowerDNS sqlite, `/etc/letsencrypt`, nginx/postfix/roundcube/phpmyadmin config. (v3.1.113 overhauled the in-panel backup feature — deploying it gives a supported path; see `docs/backup-dr-gap-analysis.md`.) |

## P2 — Correctness / hardening

| # | Area | Recommendation | How |
|---|------|----------------|-----|
| R5 | **DNS B2 (S1 stale IP)** | PowerDNS on S1 serves S2's IP; SPF embeds it too. | `bzpanel reassign-ip 195.35.7.64 89.116.34.207` **or** `POST /whm/config/reassign-ip {old_ip,new_ip}` (rewrites A + SPF `ip4:` in PowerDNS + Mongo). Harness-gated — run with operator approval. |
| R6 | **DNS B3 (dup SPF/DMARC + DKIM tables)** | Two SPF/DMARC records void both; duplicate KeyTable lines. | Per-zone `POST /whm/dns/zones/:domain/reconcile` (collapses exact dups + replays rrsets), then delete the redundant SPF/DMARC variant so exactly one of each remains; `sort -u` the OpenDKIM `key.table`/`signing.table` + `systemctl reload opendkim`. |
| R7 | **DKIM B4 (key mismatch, S1)** | Published key ≠ signing key. | Re-publish the current `mail.private` public key to `mail._domainkey.<zone>` TXT (panel DNS), or regenerate the keypair via the panel's DKIM action. |
| R8 | **Mail TLS floor** | Postfix `smtpd_tls_protocols >= TLSv1`; nginx global `ssl_protocols` includes TLSv1/1.1. | Set `smtpd_tls_protocols = >=TLSv1.2` and trim nginx `ssl_protocols` to `TLSv1.2 TLSv1.3`. (Dovecot already floors at TLSv1.2.) |
| R9 | **Replace snakeoil mail certs** | Postfix/Dovecot use the self-signed snakeoil cert. | Point `smtpd_tls_cert_file`/Dovecot `ssl_cert` at a real cert for the mail hostname in production. |
| R10 | **SpamAssassin not wired** | `spamd` runs but no milter/content_filter → inbound mail is never scored. | Wire `spamass-milter` (or amavis) into Postfix `smtpd_milters`, or remove `spamd` if scoring isn't wanted. |
| R11 | **Security headers** | No HSTS, no CSP (other 4 headers present). | Add `Strict-Transport-Security` (once HTTPS is on) + a Content-Security-Policy to the panel/webmail vhosts. |

## P3 — Operability

| # | Area | Recommendation |
|---|------|----------------|
| R12 | **No alerting** | `metrics` are collected every 60s but never evaluated. Wire the existing alert-threshold schema to email/Slack on high CPU / low disk / service-down, and add an external watchdog (node_exporter + Prometheus/Alertmanager, or a simple uptime check) so a panel-down event pages someone. |
| R13 | **`audit_logs` retention** | No TTL — grows forever (unlike `mail_logs`/`metrics`). Add a TTL or rollup. Also record self-service password changes (currently unaudited). |
| R14 | **Roundcube attachment ceiling** | PHP `upload_max_filesize=2M` < Postfix `message_size_limit=10M` — webmail blocks 2–10 MB attachments. Raise PHP `upload_max_filesize`/`post_max_size` to ≥ 10M. |
| R15 | **Dovecot quotas** | No quota plugin → mailboxes are unbounded. Enable the quota plugin if per-mailbox limits are desired. |
| R16 | **Pending OS updates** | 40 pending (25 security) on S1. Run `unattended-upgrades` / `apt upgrade` in a maintenance window. |
| R17 | **nginx duplicate `server_name`** | Default vhost lists the IP twice → harmless reload warning. Fix the installer template. |
| R18 | **MariaDB/nginx tuning** | Stock `innodb_buffer_pool_size=128M` and nginx `worker_connections=768` are the first ceilings under real load; raise when traffic grows. Enable MariaDB slow-query log. |

## P4 — Product / code (next deploy)

| # | Recommendation |
|---|----------------|
| R19 | Deploy **v3.1.114** to both boxes to ship the `test-connection` 400 fix (and bring the running git checkout in line with the binary). |
| R20 | Consider deploying **v3.1.113's backup/DR overhaul** so the in-panel backup feature (whole-server bundle, rclone destinations, retention, encryption) is available — closes R4 with a supported path. |
| R21 | Make the unique email index **case-insensitive at the DB layer** (collation) to match the service-layer guarantee and CLAUDE.md (currently service-layer only). |
