# 18 — Production Readiness Checklist

**Date:** 2026-06-29  Scope: the bPanel demo pair S1 `89.116.34.207` / S2 `195.35.7.64` (v3.1.112).
Legend: ✅ ready · ⚠️ needs attention before production · ⛔ blocker for real data.

## Platform & runtime
- ✅ Ubuntu 24.04.4 LTS, kernel 6.8, 8 vCPU / 31 GiB / 387 GB — heavily over-provisioned (~1–2% utilization)
- ✅ Panel v3.1.112 running, 0 failed systemd units, all services active+enabled
- ✅ MongoDB 8.0.26 (FCV 8.0), MariaDB 10.11 — both loopback-bound, ~100% cache hit, 0 slow queries
- ✅ Swap configured (4 GiB) — *added this run*
- ⚠️ 40 pending OS updates (25 security) on S1 — patch in a maintenance window

## Data & databases
- ✅ Mongo `serverpanel`: 29 collections, all `indexes.go` indexes present, **0 orphans**, integrity clean
- ✅ `metrics` bounded by a 7-day app retention sweep (+ 90-day TTL index added this run)
- ✅ MariaDB least-privilege (each app user scoped to its DB; root via unix socket)
- ⚠️ `audit_logs` has no TTL (grows slowly); email unique index is case-sensitive (service layer compensates)

## Mail
- ✅ Postfix/Dovecot/OpenDKIM/Roundcube healthy; mail queue empty; maps consistent (42/42/36)
- ✅ **Source-agnostic mail logging works** (webmail/smtp-client/api-local/inbound-smtp) — verified live on both boxes
- ⚠️ SpamAssassin not wired into Postfix; snakeoil TLS cert; plaintext IMAP/POP3 auth allowed; no quotas
- ⚠️ (S1) DKIM published≠signing, duplicate SPF/DMARC — cosmetic on `.local`, fix before public domains

## DNS
- ✅ PowerDNS authoritative (gsqlite3), 6 zones, full record sets; S2 serves its own IP correctly
- ⚠️ (S1 only) stale served IP + duplicate SPF/DMARC (B2/B3) — remediation in report 17

## Security
- ✅ UFW default-deny with tight allow-list; nothing unintentionally internet-exposed; datastores loopback-bound
- ✅ Strong app layer: JWT alg-pinning, guest-token rejection, argv-array exec (no shell injection), parameterized Mongo queries, rate-limited login/OTP, bcrypt, audit logging
- ✅ fail2ban on 4 jails (sshd, postfix-sasl, dovecot, pure-ftpd); secrets `600 root` at rest
- ⛔ **No HTTPS** on the panel (plaintext logins/JWTs) — must fix before production
- ⚠️ SSH root + password auth internet-exposed; legacy TLS 1.0/1.1 on mail/nginx; no HSTS/CSP

## API & UI
- ✅ 60/63 API checks pass, 0 functional breakage; auth/RBAC/error-envelope all correct
- ✅ SPAs serve (`/whm`, `/user-panel`), `/cpanel→/user-panel` 301 works, Roundcube login healthy
- ✅ `test-connection` 500→400 fixed in code (v3.1.114) — *deploy to activate*

## Backup & DR
- ⛔ **No backups configured** on either box — highest operational gap (report 17 R4)
- ✅ A full pre-migration backup of S2 exists at `/var/backups/premigration-20260629-145010/`

## Migration
- ✅ Native transfer S1→S2 re-validated this run (14/14 steps, 0 errors, parity + functional verified)

## Monitoring
- ⚠️ Metrics collected but **no alerting** and no external watchdog (report 17 R12)

---

### Verdict
**Excellent as a demo/staging environment** — stable, fast, functionally complete, mail logging
working, migration proven. **Before production with real data**, clear the ⛔ blockers (HTTPS,
backups) and the P1/P2 items in report 17 (SSH hardening, credential rotation, DNS/DKIM cleanup,
TLS floor, alerting).
