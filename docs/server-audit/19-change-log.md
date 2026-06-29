# 19 — Complete Change Log

**Run date:** 2026-06-29  **Engineer:** Autonomous multi-agent program (Claude Opus 4.8)
**Servers:** S1 `89.116.34.207` (source) · S2 `195.35.7.64` (destination)
**Panel:** deployed v3.1.112 on both; repo bumped v3.1.113 → **v3.1.114**.

Every change below is reversible. Nothing was deleted; no working module was rewritten.

## Server changes — Server 1 (89.116.34.207)
| When | Change | Reversal |
|------|--------|----------|
| 14:3x | Added 4 GiB `/swapfile` + `/etc/fstab` entry | `swapoff /swapfile && rm /swapfile` + remove fstab line |
| 14:3x | Added `/etc/logrotate.d/mongodb` (mongod.log rotation) | `rm /etc/logrotate.d/mongodb` |
| 14:4x | Added Mongo TTL index `metrics.timestamp_ttl` (90 d, defence-in-depth) | `db.metrics.dropIndex("timestamp_ttl")` |
| 14:4x | Generated multi-source demo mail traffic (9 messages; `mail_logs` → 16) | demo data; no reversal needed |
| 14:xx | Live third-party-SMTP logging verification (1 test message) | demo data |

## Server changes — Server 2 (195.35.7.64)
| When | Change | Reversal |
|------|--------|----------|
| 14:50 | Pre-migration backup → `/var/backups/premigration-20260629-145010/` (mongodump + mysqldump + configs) | n/a (additive) |
| 14:54 | **Native transfer from S1** (`6a4287222d1e5bec41138630`, 14/14 steps, 0 errors) | restore from the pre-migration backup above |
| 15:0x | `rehydrate-all` (mailboxes 42/42, forwarders 36/36, dns 6 zones/72 rrsets, mysql 3/3; 0 failed) | restore from backup |
| 15:1x | Added 4 GiB swap, `mongod` logrotate, `metrics` TTL index (same as S1) | same as S1 reversals |
| 15:1x | Live post-migration functional verification (1 test message) | demo data |

## Code changes (repo — committed, not yet deployed)
| File | Change |
|------|--------|
| `backend/internal/handlers/transfer_handler.go` | `TestConnection` returns **400** (was 500) on a failed connectivity probe — matches `backups/test-connection`; verified via `GOOS=linux GOARCH=amd64 go build ./...` |
| `backend/pkg/version/version.go` | `Patch 113 → 114` + changelog comment |
| `docs/server-audit/*.md` | This 16-report deliverable set (new) |

## Deliverables produced (docs/server-audit/)
`00-executive-summary` · `01-infrastructure` · `02-mail-system` · `03-mail-suite` ·
`04-dns` · `05-mongodb` · `06-sql` · `07-demo-data` · `08-security` · `09-performance` ·
`10-logging-monitoring` · `11-api-testing` · `12-ui-webmail` · `13-migration` ·
`14-migration-verification` · `15-bug-report` · `16-fixed-issues` · `17-recommendations` ·
`18-production-readiness` · `19-change-log`

## Explicitly NOT changed (and why)
- **Panel binary not redeployed** — v3.1.112 is healthy; the IMPORTANT RULE forbids unnecessary upgrades. v3.1.114 ships on the next normal deploy.
- **DNS bulk rewrite on S1 (B2/B3)** — harness-gated for human sign-off + cosmetic on `.local`; documented in report 17 (R5/R6).
- **DKIM republish, SSH/TLS/HTTPS hardening, SpamAssassin wiring** — touch access paths / mail flow on a working demo; left as prioritized recommendations.
- **`indexes.go` metrics TTL** — left unchanged; a 7-day app-level retention sweep already exists (the "unbounded" finding was a false alarm).
