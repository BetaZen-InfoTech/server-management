# 12 — Bug Report

Confirmed bugs found across the audit, demo-generation, and migration phases. Status: **FIXED**
(applied + redeployed this run) or **OPEN** (documented with a recommended fix in `14`).
Full code-level analysis of the mail-logging/transfer bugs is in `reports/bug-detection.md`.

| ID | Sev | Area | Summary | Status |
|----|-----|------|---------|--------|
| B-01 | critical | Mail (S2) | Postfix chroot `resolv.conf` was the dead systemd stub `127.0.0.53` → outbound MX lookups failed → external mail would defer/bounce | **FIXED** |
| B-02 | critical | Security | Default owner password `admin123` accepted on both internet-reachable production panels | **FIXED** (rotated) |
| B-03 | critical | Security (code) | Root command injection via unsanitised domain/email/hostname interpolated into `bash -c` (mailbox/domain/DNS/hostname create) | **FIXED** (validation) |
| B-04 | high | Mail logging | `mail_log_service` idle-flusher dropped a still-queued entry from memory; a deferred message retried after 3 min got a NEW first_seen → **duplicate row + orphaned phantom "stuck" row** | **FIXED** |
| B-05 | high | Migration | Post-transfer app recovery rebuilt the app systemd unit **without `Environment=PORT`** → migrated Go/Python apps fell back to `:8080` and crash-looped (hit live: `demo-erp`) | **FIXED** |
| B-06 | medium | Migration | Destination created a synthetic `<user>@localhost` *customer* per Linux user (in "Transfer Domains & Files") **before** "Sync Panel Records" added the real `vendor_admin`, leaving two panel rows sharing one username | **OPEN** (data cleaned; fix in `14`) |
| B-07 | medium | Migration (code) | `reencryptSyncedMailboxes` `$unset`s `encrypted_pass` across **all** destination mailboxes (not scoped to the migrated tenant) when the source `JWT_SECRET` is unreadable → degrades webmail SSO for unrelated tenants | **OPEN** (didn't trigger here — source key readable; fix in `14`) |
| B-08 | low | API routing | `GET /cpanel/backups/schedules` 404'd because `/backups/:id` was registered before `/backups/schedules` | **FIXED** |
| B-09 | high | Mail Suite | One-click installer searched `/opt/server-panel` but the real path is `/opt/serverpanel` → install always failed pre-cwd-fallback | **FIXED** |
| B-10 | high | Mail Suite | Panel sends a Bearer **service token** that mail-suite never validates (no `PANEL_SERVICE_TOKEN` handling) → Enable-Mail/DNS proxy would 401 | **OPEN** (mail-suite undeployed; fix in `14`) |
| B-11 | low | Mail logging | Ingestor `tail -n 0 -F` starts at EOF → mail logged during panel downtime is never ingested | **OPEN** (fix in `14`) |
| B-12 | low | Migration (code) | `detectSourceIP` returned the first `A` in the zone (could be a subdomain), risking a skipped apex A-rewrite on cutover | **FIXED** (apex-preferred) |
| B-13 | low | Mail logging | `MailLogService.List/Stats` swallowed the `TenantDomains` error → a transient DB fault looked like "you have no mail" | **FIXED** |
| B-14 | low | Demo/forwarders | `keep_copy=true` on a forwarder whose source is an *alias* (not a mailbox) bounced the kept-copy leg (target still delivered) | **FIXED** (demo data; product note in `14`) |
| B-15 | info | Config | Go installed at `/opt/go` but not on the deploy build PATH → panel Go app builds failed `go: command not found` | **FIXED** (symlink on both) |

## Notes

- **B-04 is the core of the originally-reported "third-party SMTP not logged" issue.** The v3.1.108
  source-agnostic ingestor (a `tail -F /var/log/mail.log` parser) genuinely captures every source;
  the residual defect was duplicate/phantom rows for *deferred* mail, now fixed. End-to-end capture
  of a remote third-party client was verified live (see `11` / `reports/bug-detection.md`).
- Items verified **correct / not regressed**: v3.1.107 wrong-cert & server_name guards, certbot
  lineage pinning, `mail.*` recursion guard, v3.1.50 mailbox dedup, cPanel mail-log tenant scoping,
  SSRF guard, File Manager path-traversal/zip-slip defenses, RBAC (`AssertOwns`), HS256
  alg-confusion protection.
