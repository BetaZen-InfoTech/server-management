# Agent 5 — MongoDB Audit (Server 1, 89.116.34.207)

**Scope:** Read-only audit of the `serverpanel` MongoDB instance on the bPanel demo box "Server 1".
**Date:** 2026-06-29 · **Mode:** READ-ONLY (no destructive changes)

---

## Summary

MongoDB on Server 1 is **healthy and correctly configured**. Version **8.0.26**, FCV **8.0**,
WiredTiger, **bound to 127.0.0.1 only**, authorization **enabled**. The panel application user is
properly scoped to the `serverpanel` DB (no admin privileges — verified by an
`not authorized on admin` error when running `serverStatus` through the app URI).

Every index declared in `backend/internal/database/indexes.go` is **present** on the box —
including the unique email indexes, the `mail_logs.log_key` unique index, the `mail_logs` 90-day TTL,
and all partial/compound indexes. **Zero missing indexes vs. code.** Data integrity is clean:
**no orphaned references** anywhere, and `validate()` reports all sampled collections valid.

Two non-blocking issues stand out: (1) the `metrics` collection has **no TTL / capped strategy** and is
growing unbounded (~3,470 docs in ~19h of uptime); (2) the unique email indexes on `users` and
`mailboxes` are **case-sensitive at the DB layer** (no collation) — case-insensitivity is enforced only
in the service layer, so the DB index is not a hard backstop.

---

## Server Facts

| Property            | Value |
|---------------------|-------|
| Version             | 8.0.26 |
| featureCompatibilityVersion | 8.0 |
| Storage engine      | wiredTiger |
| bindIp              | 127.0.0.1 (port 27017) — **not public** ✓ |
| Listening socket    | `127.0.0.1:27017` only (mongod pid 23854) ✓ |
| authorization       | enabled ✓ |
| Connections         | 11 current / 25,589 available |
| Uptime at audit     | ~19.3 h |

### `serverpanel` dbStats

| Metric | Value |
|--------|-------|
| Collections | 29 |
| Objects | 3,962 |
| Data size | 0.465 MB |
| Storage size | 0.813 MB |
| Indexes | 95 |
| Index size | 1.805 MB |

---

## Findings — Collections & Counts

| Collection | Count | Expected (spec) | Note |
|-----------|------:|-----------------|------|
| apps | 6 | 6 | ✓ |
| audit_logs | 232 | ~232 | ✓ |
| dns_records | 114 | 114 | ✓ |
| dns_zones | 6 | 6 | ✓ |
| domains | 6 | 6 | ✓ |
| email_forwarders | 36 | 36 | ✓ |
| ftp_accounts | 6 | 6 | ✓ |
| mailboxes | 42 | 42 | ✓ |
| metrics | 3,468–3,471 | 3,432 | growing live (≈+1/15s) |
| mail_logs | 8 | 7 | ✓ (+1 since spec) |
| users | 5 | 5 | ✓ |
| databases | 2 | 2 | ✓ |
| database_users | 2 | — | ✓ |
| hosting_packages | 1 | — | ✓ |
| projects | 1 | — | ✓ |
| login_sessions | 30 | — | active sessions |
| api_tokens, backups, cron_jobs, db_access_hosts, email_installations, email_server_configs, github_deploys, guest_links, project_deployments, project_services, ssl_certificates, webhook_deliveries, webhook_endpoints | 0 | — | empty (expected on demo) |

All "known counts" from the brief match within live-drift tolerance.

---

## Findings — Indexes (vs. `indexes.go`)

**All declared indexes are present.** Spot-checks of the critical ones:

| Collection | Critical index | Present? | Detail |
|-----------|----------------|:--------:|--------|
| users | `email_1` UNIQUE | ✓ | **case-sensitive (no collation)** — see Issue M2 |
| users | `role_1` | ✓ | |
| mailboxes | `email_1` UNIQUE | ✓ | **case-sensitive (no collation)** — see Issue M2 |
| email_forwarders | `source_1_domain_1` UNIQUE | ✓ | matches dedup design |
| mail_logs | `log_key_1` UNIQUE | ✓ | |
| mail_logs | `created_at_1` TTL=7776000 (90d) | ✓ | retention working |
| metrics | `timestamp_-1` | ✓ | **no TTL** — see Issue M1 |
| projects | `slug_1` UNIQUE + `tenant_id_1_name_1` UNIQUE PARTIAL | ✓ | partial filter present |
| project_services | `primary_domain_1` PARTIAL (`$gt:""`) | ✓ | |
| webhook_deliveries | `created_at_1` TTL=2592000 (30d) | ✓ | |
| db_access_hosts | `database_id_1_host_1` UNIQUE | ✓ | |
| ssl_certificates / dns_zones / domains / apps / databases | `domain`/`name`/`db_name` UNIQUE | ✓ | all present |

Collections **not declared** in `indexes.go` that carry only the default `_id_` index
(expected — these aren't indexed by the panel today): `ftp_accounts`, `database_users`,
`login_sessions`, `hosting_packages`. These do small/full scans, but their cardinality is tiny
(≤30 docs), so this is informational only, **not** a flagged gap.

> Note: `ftp_accounts` is **not** declared in `indexes.go` (the brief's mention of an ftp index
> expectation does not appear in the code), so its single `_id_` index is **not** a code deviation.

---

## Findings — Integrity

| Check | Result |
|-------|--------|
| mailboxes with `domain` not in `domains` | **0 orphans** ✓ |
| email_forwarders with `domain` not in `domains` | **0 orphans** ✓ |
| ftp_accounts with `domain` not in `domains` | **0 orphans** ✓ |
| dns_records with `zone_id` not in `dns_zones` | **0 orphans** (114/114 resolve) ✓ |
| domains ↔ dns_zones | 1:1 — same 6 domains on both sides ✓ |

Domains in the dataset (6): `mail.demo-one.local`, `company-demo.local`, `mail.demo-two.local`,
`examplemail.local`, `testing-domain.local`, `internal.demo.local`.

### `validate()` (lightweight)

| Collection | valid | invalid docs | nrecords |
|-----------|:-----:|:-----------:|---------:|
| metrics | true | 0 | 3,471 |
| mailboxes | true | 0 | 42 |
| dns_records | true | 0 | 114 |

---

## Issues (by severity)

### M1 — `metrics` collection grows unbounded (MEDIUM)
`metrics` has a `timestamp_-1` index but **no TTL and no capped-collection strategy**. The
MonitoringService writes a sample roughly every 15s: the collection went from the spec's 3,432 to
**3,471 docs during the ~19h window of this audit** (≈4,300 docs/day). On a long-lived box this
grows without bound. Critically, `indexes.go` declares only `{timestamp:-1}` for `ColMetrics` —
there is **no retention sweep in code** for this collection (unlike `mail_logs` and
`webhook_deliveries`, which both carry TTL indexes). This is a **code-level gap**, not just a box-level one.

### M2 — Unique email indexes are case-sensitive at the DB layer (LOW–MEDIUM)
`users.email_1` and `mailboxes.email_1` are unique but have **no collation** (case-sensitive).
`CLAUDE.md` states email is "enforced case-insensitively at the service layer **plus a unique MongoDB
index**." The DB index does **not** enforce case-insensitivity — `Foo@x.com` and `foo@x.com` could
both insert if the service-layer guard were ever bypassed (e.g. a direct write or a code path that
forgets to lowercase). Defense-in-depth is weaker than the doc implies.

### M3 — mongod binds correctly here (NO ISSUE — noted for contrast)
Unlike the production box noted in memory (which binds `0.0.0.0`), Server 1's mongod binds
`127.0.0.1` only. No action.

---

## Fixes Applied

**None.** Every index `indexes.go` declares is already present, so there was no "clearly-missing
index the code expects" to safely add. The two real issues (M1 metrics TTL, M2 email collation) are
**not** in-spec missing indexes — adding them would deviate from what the code currently declares, so
they are left as recommendations for the code/demo-data phase rather than applied blind on a demo box.

---

## Recommendations

1. **(M1) Add metrics retention.** Either add a TTL index in `indexes.go` for `ColMetrics`
   (e.g. `{timestamp:1}` with `SetExpireAfterSeconds(7*24*3600)`), or convert `metrics` to a capped
   collection / add a periodic `DeleteMany({timestamp:{$lt:cutoff}})` sweep. Without this the
   collection grows ~4.3k docs/day forever.
2. **(M2) Make email uniqueness case-insensitive at the DB.** Recreate `users.email_1` and
   `mailboxes.email_1` with `collation: { locale: "en", strength: 2 }` so the unique index itself is a
   true backstop. (Requires a rebuild + a data-side lowercasing pass; do in the schema phase, not on
   the demo box.)
3. Leave the empty/zero-count collections as-is — expected for a fresh demo.
