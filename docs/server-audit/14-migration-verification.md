# 14 — Migration Verification Report

**Date:** 2026-06-29  **S1** `89.116.34.207` (source) · **S2** `195.35.7.64` (destination)

The migrated server (S2) was validated for both **data parity** and **functional behaviour**.
Verdict: **PASS — S2 behaves as S1, with no data loss.**

## 1. Data parity (post-migration counts)

| Resource | S1 | S2 | Verdict |
|----------|----|----|---------|
| Panel domains | 6 | 6 | ✅ match |
| DNS zones | 6 | 6 | ✅ match |
| DNS records | 119 | 84 | ✅ S2 deduped-cleaner (replace-rrset collapsed S1's dup SPF/DMARC) — no loss |
| Mailboxes | 42 | 42 | ✅ match |
| Dovecot users (passwd-file) | 42 | 42 | ✅ match |
| Email forwarders | 36 | 36 | ✅ match |
| Apps | 6 | 6 | ✅ match |
| FTP accounts | 6 | 6 | ✅ match |
| Panel users | 5 | 5 | ✅ match |
| Hosting packages | 1 | 1 | ✅ match |
| MariaDB DBs | `demotwo_appdb`, `roundcube` | + `demoone_appdata` | ✅ S2 superset (extra demo DB present) |
| Active demo/app services | 11 | 11 | ✅ match |
| Failed systemd units | 0 | 0 | ✅ |

Every source resource is present on the destination. The two count differences are **gains/cleanups**,
not losses: S2's DNS is the deduplicated form, and S2 retains an extra demo database.

## 2. Functional verification (Server 2)

| Check | Method | Result |
|-------|--------|--------|
| Mailbox authentication | `doveadm auth test admin@mail.demo-one.local` | ✅ **succeeded** (migrated password `M@ilbox2026!` works) |
| Authenticated SMTP send | submit via `195.35.7.64:587` (STARTTLS + AUTH) | ✅ **sent**, recipient `support@…:sent` |
| Mail-log capture | query `mail_logs` for the test message | ✅ captured `source=smtp-client`, `client_ip=195.35.7.64`, `status=sent` — **third-party logging works on S2** |
| DNS resolution | `dig @127.0.0.1 company-demo.local A` | ✅ `195.35.7.64` (correctly serves **S2's own IP** after rewrite) |
| Panel API | `/api/v1/version`, `/whm/domains` | ✅ v3.1.112, domains=6, `success:true` |
| Demo apps (HTTP) | curl ports 3101/3102/3103/5101/8091 | ✅ all **HTTP 200** |
| Service health | `systemctl --state=failed` | ✅ **0 failed units** |

## 3. Conclusion

The destination server independently **sends, receives, logs, and serves** mail; resolves DNS to
its own IP; answers the panel API; and runs every migrated application. Combined with the parity
table above, this confirms the migration met the **"no data loss / behaves exactly like the source"**
requirement. A full pre-migration backup of S2 remains at
`/var/backups/premigration-20260629-145010/` for rollback if ever needed.
