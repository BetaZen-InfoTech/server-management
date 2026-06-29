# 15 — Production Readiness Checklist

Legend: ✅ done · ⚠️ partial / demo-acceptable · ❌ required before production

## Platform / infrastructure
- ✅ OS current (Ubuntu 24.04.4, kernel 6.8) on both nodes
- ✅ All systemd services healthy; no failed units; panel auto-restarts
- ✅ Firewall active (UFW default-deny; panel :8080 & agent :8443 not externally exposed)
- ⚠️ Swap: 0 GB on 31 GB RAM — add swap and/or cap Mongo cache (`14` #17)
- ❌ Automated backups (Mongo/MariaDB/maildir) — none configured (`14` #16)

## Security
- ✅ Default admin password rotated (no longer `admin123`)
- ✅ Command-injection sinks gated by shell-safe input validation
- ✅ Secrets unique per server, `.env` 600, JWT HS256 alg-confusion-safe, bcrypt, SSRF guard, RBAC
- ✅ HTTP security headers (X-Frame-Options/nosniff/Referrer-Policy) added
- ✅ fail2ban jails for ssh + postfix-sasl + dovecot + pure-ftpd
- ❌ TLS/HTTPS for panel + mail (bare IP, no cert) (`14` #2)
- ❌ SSH: disable root password login after key setup (`14` #1)
- ⚠️ Panel binds 0.0.0.0:8080 (UFW-protected) — bind loopback (`14` #3)
- ⚠️ CORS `*`, 4 h JWT TTL, terminal-WS token-in-URL, cleartext FTP (`14` #4–7)

## Mail
- ✅ Postfix/Dovecot/OpenDKIM running; SMTP/IMAP/POP3/submission all functional
- ✅ DKIM/SPF/DMARC records present for all domains; DKIM signing wired
- ✅ Postfix chroot resolver fixed on both (outbound MX resolves)
- ✅ Source-agnostic mail logging captures webmail + SMTP-client + API + local (all sources)
- ⚠️ SpamAssassin not wired into Postfix; no ClamAV (`14` #19)
- ❌ Real domain + PTR for external deliverability (bare IP is spam-foldered) (`14` #20)

## Data / databases
- ✅ MongoDB 8.0.26 auth-enabled, loopback-bound, unique email index
- ✅ MariaDB 10.11 loopback-bound, socket-auth root, scoped app users
- ⚠️ Mongo case-insensitive email relies on app layer; profiler off; tune at scale (`14` #18)

## Application / migration
- ✅ 6 demo apps deployed (Go/Node/Python/static) with reverse proxy + health
- ✅ 6 demo systemd services (startup/restart/logging/health verified)
- ✅ Native server transfer works end-to-end (13/13 steps, 0 errors) + rehydrate
- ✅ Migration data + functional parity verified (S2 == S1)
- ⚠️ Transfer code follow-ups: dup-username, unscoped SSO `$unset`, Mongo-DB discovery (`14` #9–13)

## Logging / monitoring
- ✅ journald (persistent) + rsyslog + logrotate; panel zerolog JSON; mail/auth/nginx/mongo logs
- ✅ Panel mail_logs (90-day TTL) + audit_logs + metrics collector (60s)
- ⚠️ No proactive metric-threshold alerting loop; audit_logs no TTL (`reports/logging.md`)

## Verdict
**Demo environment: fully functional, audited, populated, migrated, and validated.**
**Production: address the ❌ items (TLS, backups, SSH key-auth) and the migration code
follow-ups before going live.**
