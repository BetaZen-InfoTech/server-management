# BetaZen Server Panel — Audit · Demo Data · Migration · Validation (2026-06-29)

**Engineer:** Autonomous multi-agent program (Claude Opus 4.8)
**Servers:** S1 `89.116.34.207` (SOURCE) · S2 `195.35.7.64` (DESTINATION) — both Ubuntu 24.04.4,
8 vCPU / 31 GiB / 387 GB, Hostinger VPS, no Docker.
**Panel:** Betazen Server Panel **v3.1.112** deployed on both; repo advanced to **v3.1.114**.

> This was the **second** autonomous pass on this demo pair (the first ran 2026-06-28 and is
> archived under `SERVER_AUDIT_2026-06-28/`). This pass re-audited everything fresh, verified the
> headline mail-logging concern, enriched the demo data, **re-ran the full S1→S2 migration with
> verified parity**, applied safe hardening, fixed one confirmed code bug, and produced this
> 16-report deliverable set.

---

## Headline outcomes

1. **The reported defect — "third-party SMTP emails don't appear in application logs" — is RESOLVED
   and was proven so end-to-end on BOTH servers.** A fresh authenticated submission from a
   non-loopback client is captured in the panel `mail_logs` within ~3 s as `source=smtp-client`
   with the real client IP, auth method, Message-ID, SMTP response, queue status, delivery result
   and attachment flag. The deployed v3.1.108+ ingestor tails Postfix's authoritative log, so it
   captures **every message from every source** (webmail, Thunderbird/Outlook/mobile, API/sendmail,
   inbound). No code change was needed — it already works. *(reports 02, 15)*

2. **Migration S1 → S2 succeeded with verified parity and no data loss.** After backing up S2, the
   panel's native transfer completed **14/14 steps, 0 errors**; `rehydrate-all` healed 42/42
   mailboxes, 36/36 forwarders, 6 DNS zones, 3/3 MySQL DBs with **0 failures**. S2 then
   independently sent + logged mail, resolved DNS to **its own IP**, answered the API, and served
   all 5 demo apps (HTTP 200) with **0 failed units**. *(reports 13, 14)*

3. **The environment is healthy and complete.** 9 parallel audit agents found the box stable and
   ~1–2% utilized, with a complete demo dataset (6 domains, 42 mailboxes, 36 forwarders incl. 6
   chained, 6 apps, 6 services, 2 MySQL DBs + panel Mongo + a demo Mongo DB, 119 DNS records).
   API 60/63 pass, RBAC + error-envelope correct, SPAs + webmail serve correctly.

4. **One confirmed code bug fixed:** `transfers/test-connection` returned **500** for ordinary
   connection failures → now **400** (v3.1.114, builds clean for linux/amd64). *(reports 15, 16)*

5. **Safe hardening applied to both boxes:** 4 GiB swap, `mongod.log` rotation, `metrics` TTL index.

---

## Safe fixes applied (both servers unless noted)
4 GiB swap · `mongod` logrotate · `metrics` TTL index (defence-in-depth) · `test-connection`
500→400 in code (v3.1.114, ships next deploy) · multi-source demo mail traffic generated (S1).
Full list + reversals in **16-fixed-issues.md** and **19-change-log.md**.

## Most important open items (full list in 17 & 18)
- ⛔ **No HTTPS** on the panel (plaintext logins/JWTs) and ⛔ **no backups** configured — clear both before real data.
- ⚠️ SSH root+password exposure; legacy TLS 1.0/1.1; rotate demo credentials; (S1) DNS stale-IP + duplicate SPF/DMARC + DKIM key mismatch (cosmetic on `.local`, S2 already clean post-migration); no alerting.

## Two cross-agent claims adversarially re-checked
- "`metrics` grows unbounded" → **false**: a 7-day app retention sweep already exists; `indexes.go` left unchanged.
- DKIM "match vs mismatch" disagreement between two agents → resolved with the correct `mail` selector: **mismatch confirmed** on S1 (low impact, `.local`).

---

## Deliverable index (`docs/server-audit/`)

| # | Report | File |
|---|--------|------|
| 0 | Executive Summary | `00-executive-summary.md` |
| 1 | Infrastructure Audit | `01-infrastructure.md` |
| 2 | Mail System Audit | `02-mail-system.md` |
| 3 | Mail Suite Audit | `03-mail-suite.md` |
| 4 | Domain & DNS | `04-dns.md` |
| 5 | MongoDB | `05-mongodb.md` |
| 6 | SQL / MariaDB | `06-sql.md` |
| 7 | Demo Data Generation | `07-demo-data.md` |
| 8 | Security | `08-security.md` |
| 9 | Performance | `09-performance.md` |
| 10 | Logging & Monitoring | `10-logging-monitoring.md` |
| 11 | API Testing | `11-api-testing.md` |
| 12 | UI / Webmail | `12-ui-webmail.md` |
| 13 | Migration | `13-migration.md` |
| 14 | Migration Verification | `14-migration-verification.md` |
| 15 | Bug Report | `15-bug-report.md` |
| 16 | Fixed Issues | `16-fixed-issues.md` |
| 17 | Remaining Recommendations | `17-recommendations.md` |
| 18 | Production Readiness Checklist | `18-production-readiness.md` |
| 19 | Complete Change Log | `19-change-log.md` |

## ⚠️ Credentials referenced in these reports (demo only — rotate before real use)
WHM owner `admin@betazeninfotech.com` / `Betazen!Demo-2026#Kx9pQ2` · demo mailboxes (×42) `M@ilbox2026!` ·
demo vendors `owner@demo-one|demo-two|internaldemo.local` / `Demo!Pass2026`.
