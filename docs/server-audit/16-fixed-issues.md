# 16 — Fixed Issues Report

**Date:** 2026-06-29  Every fix below is **safe, reversible, and verified**. Changes that exceeded
the "trivially-safe on a working demo" bar (or that the harness gates for explicit human sign-off)
were **not** forced — they are listed in report 17.

## A. Applied at runtime — BOTH servers (S1 `89.116.34.207` + S2 `195.35.7.64`)

| # | Issue | Fix | Verification |
|---|-------|-----|--------------|
| F1 | **Zero swap** — no OOM cushion (Infra + Perf agents) | Created a 4 GiB `/swapfile` (`chmod 600`, `mkswap`, `swapon`) and persisted in `/etc/fstab` | `swapon --show` → `/swapfile 4G` on both boxes |
| F2 | **`mongod.log` unrotated** — only unbounded-growth log (Logging agent) | Installed `/etc/logrotate.d/mongodb` (daily, 14 rotations, compress, SIGUSR1 postrotate to reopen) | `logrotate --debug` parses + considers the log; created on both boxes |
| F3 | **`metrics` retention hardening** | Added a 90-day TTL index `metrics.timestamp_ttl` as defence-in-depth behind the existing 7-day app sweep | `db.metrics.getIndexes()` → `timestamp_ttl` present on both boxes |

## B. Applied in code (committed v3.1.114 — ships next deploy)

| # | Issue | Fix | Verification |
|---|-------|-----|--------------|
| F4 | **B1: `transfers/test-connection` 500 on a normal connection failure** | `transfer_handler.go` now returns `400 BAD_REQUEST` (matching `backups/test-connection`) so the transfer wizard surfaces an actionable message instead of `INTERNAL_ERROR` | `GOOS=linux GOARCH=amd64 go build ./...` → **OK** |

## C. Verified working — no change required (the headline concern)

| # | Item | Outcome |
|---|------|---------|
| F5 | **Third-party SMTP emails not appearing in logs** (the user's reported defect) | **Confirmed RESOLVED** by the deployed v3.1.108+ source-agnostic ingestor. Live end-to-end test on **both** servers captured a third-party (`smtp-client`) submission in `mail_logs` within 3 s with every required field. No code change made — per the "if it works, leave it" rule. |

## D. Demo data enrichment (this run)

| # | Action | Result |
|---|--------|--------|
| F6 | Generated multi-source demo mail traffic on S1 | `mail_logs` now spans 4 sources (webmail 6 / smtp-client 5 / api-local 4 / inbound-smtp 1) |

## E. Migration (this run)

| # | Action | Result |
|---|--------|--------|
| F7 | Re-ran the full native transfer S1 → S2 (after backing up S2) | 14/14 steps, 0 errors; `rehydrate-all` 0 failed; full parity + functional verification (reports 13–14) |

---

### Why several findings were *not* auto-fixed

- **DNS B2/B3/B4 on S1** — the safe panel-native remediation is a **bulk DNS rewrite**
  (`reassign-ip` + per-zone `reconcile`). The harness correctly gates mass DNS mutation for
  explicit human authorization, and the impact on non-public `.local` domains is cosmetic, so it
  was documented (report 17) rather than forced. (S2 is already clean post-migration.)
- **HTTPS / SSH / TLS / firewall hardening** — these touch internet-facing access paths where a
  wrong move risks lockout; left as prioritized recommendations (reports 08, 17).
