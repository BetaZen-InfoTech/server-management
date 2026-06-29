# 17 — Deep Bug-Hunt Report (round 2) — v3.1.112

**Date:** 2026-06-28 · **Trigger:** "deeply check the whole project + previous docs, find & fix all bugs."
**Method:** a multi-agent hunt (10 finders over the whole backend + the previous audit docs) with
**per-candidate adversarial verification** (each candidate handed to an independent skeptic told to
refute it; default-refute on doubt). 32 candidates → **29 confirmed real** (3 refuted, 0 uncertain).
Then a 14-agent file-partitioned fix pass applied every confirmed fix; central `go build ./...` and
`go vet` are clean. Shipped as **v3.1.112** to both servers; verified, no regressions, parity intact.

Per-bug evidence + exact patches: `reports/deep-bughunt-fixes-detail.md`.

## Confirmed bugs (all FIXED unless noted)

| # | Sev | Area | Bug | Status |
|---|-----|------|-----|--------|
| 1 | crit | mail | Root cmd-injection in CreateForwarder/applyForwarderToPostfix (source+dests into `echo '…'`) | FIXED (IsSafeEmail at sink + entry) |
| 2 | crit | mail | Root cmd-injection in UpdateSpamSettings (:domain path + whitelist/blacklist) | FIXED (IsSafeDNSName + glob-safe) |
| 3 | crit | auth | Empty `refresh_token` minted a session for an arbitrary active user | FIXED (reject empty in RefreshToken/Logout) |
| 4 | crit | api | Shell-injection + cross-tenant via unvalidated `:domain` in resources/bandwidth | FIXED (shellQuote + AssertOwnsDomain) |
| 5 | high | migrate | B-06 duplicate `<user>@localhost` customer collides with migrated vendor | FIXED (placeholder cleanup in mirrorPanelUsers) |
| 6 | high | migrate | B-07 unscoped `$unset encrypted_pass` across ALL mailboxes (failure path) | FIXED (scoped to transfer domains) |
| 7 | high | migrate | B-07b same on the success path (destination-only mailboxes wiped) | FIXED (scoped to transfer domains) |
| 8 | high | mail | B-11 ingestor `tail -n 0` starts at EOF → downtime mail never logged | FIXED (`-n 5000` backfill) |
| 9 | high | auth | Interactive terminal WS never re-checks suspended/deleted account | FIXED (status re-check after JWT) |
| 10 | high | auth | Install-terminal WS completely unauthenticated (install-output disclosure) | FIXED (owner/admin JWT + FE sends token) |
| 11 | high | dns | UpdateRecord silently drops priority/weight/port/CAA edits (MX priority no-op) | FIXED (map aux fields) |
| 12 | high | db | DatabaseService.Create never asserts domain/vendor ownership (cross-tenant) | FIXED (AssertOwnsDomain/AssertOwns) |
| 13 | high | db | Domain-less DBs invisible/unmanageable to their tenant | FIXED (new `owner` field + owner-OR-domain scope + backfill) |
| 14 | high | deploy | UpdateService env edits never reach the running backend | FIXED (.env rewrite + unit regen) |
| 15 | high | migrate | Static app vhost not rebuilt on migration (misrouted to PHP) | FIXED (rebuild static vhost in recoverApp) |
| 16 | high | ssl | Single Force-SSL has no cert guard → breaks site (301 to no-TLS) | FIXED (refuse enable without active cert) |
| 17 | high | db | ListUsers/ListAccessHosts skip the ownership scope check | FIXED (route via GetByID) |
| 18 | med | migrate | mirrorPanelUsers self-referencing tenant_id for out-of-order team members | FIXED (second-pass tenant_id fixup) |
| 19 | med | mail | 1-hour hard-age cap reintroduced duplicate/phantom rows (regression of the v3.1.111 BUG-1 fix) | FIXED (recoverFirstSeen keeps log_key stable) |
| 20 | med | dns | formatRecordValueForPDNS emits malformed CAA when caa_tag empty | FIXED (pass full value through) |
| 21 | med | db | Password/role rotation not propagated to remote-access MySQL grants | FIXED (re-apply per access-host) |
| 22 | med | deploy | Legacy GitHub-Deploy double-prefixes the systemd unit (Redeploy/Logs hit a ghost unit) | FIXED (CreateSystemdUnit, no double prefix) |
| 23 | med | deploy | runDeploy deletes the monorepo root package.json/lockfiles | FIXED (exclude proj.ProjectDir from rm sweep) |
| 24 | med | ssl | IssueLetsEncrypt reuse short-circuit ignores SAN coverage (SAN mismatch) | FIXED (certCoversAll gate) |
| 25 | med | api | Cross-tenant ResourceService.DomainUsage (reads any domain) | FIXED (AssertOwnsDomain) |
| 26 | low | auth | Brute-force lockout bypassable via OTP login | FIXED (LockedUntil guard in VerifyOTP/CompleteOTP) |
| 27 | low | auth | isUserAllowed caches a negative on transient Mongo errors (15s lockout of legit users) | FIXED (don't cache transient errors) |
| 28 | low | mail | Ingestor holds the parse mutex across the synchronous Mongo upsert | FIXED (build under lock, upsert after unlock) |
| 29 | low | validator | IsSafeEmail rejected dotless/underscore mail domains accepted elsewhere | FIXED (widened domain class, still shell-safe) |

Refuted by the adversarial pass (not bugs): 3 candidates (kept out of the fix set).

## Verification (both Server 1 and Server 2, post-deploy)

- `go build ./...` + `go vet ./...` clean; binary rebuilt and hot-swapped; panel **v3.1.112** active, 0 failed units.
- **#1 forwarder injection** `a'$(id)'@…` → **rejected** (no file created, no Mongo row); **#2 spam-settings**
  metachar domain → no file; **#4 bandwidth** `$(touch …)` → no file; **#3 empty refresh_token** → rejected.
- **No regressions:** 6 domains / 42 mailboxes / 36 forwarders / 6 apps (5 running) / 6 demo services intact on
  both; authenticated SMTP send → delivered (`250 Saved`) → captured in `mail_logs` (mail-log refactor #8/#19/#28
  did not break ingestion; backfill ingestor running); WHM + user-panel + webmail all HTTP 200.
- **#13 backfill:** demo DBs stamped with `owner` (demoone_appdata→demoone, demotwo_appdb→demotwo) so they are
  visible to their tenants again.
- **#10 frontend** rebuilt (`index-Dq0pbI4j.js`) and deployed to both; `/whm/` + asset return 200.
- **Parity** preserved: both servers identical except the previously-documented benign cosmetic diffs
  (Mongo dns_records 114 vs 84 — PowerDNS serves 114 = 114 on both; databases 2 vs 3 — S2 also tracks roundcube).

## Notes
- Two of the 29 were **regressions from this engagement's own earlier fixes** (#19 the 1h-cap dup from the
  v3.1.111 BUG-1 fix; #29 the over-strict IsSafeEmail from v3.1.110) — both now corrected.
- The command-injection class is now closed across all confirmed sinks: mailbox, domain, DNS zone, hostname
  (v3.1.110) **plus** forwarder, spam-settings, and resources/bandwidth (v3.1.112).
