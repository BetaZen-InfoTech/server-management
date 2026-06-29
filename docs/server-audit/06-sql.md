# Agent 6 — SQL / MariaDB Audit (Server 1, 89.116.34.207)

**Scope:** Read-only audit of the MariaDB instance on the bPanel demo box "Server 1".
**Date:** 2026-06-29 · **Mode:** READ-ONLY (no destructive changes)

---

## Summary

MariaDB on Server 1 is **healthy and securely configured**. Version **10.11.14** (Ubuntu 24.04),
**bound to 127.0.0.1 only**, root uses socket auth (`unix_socket` + invalid password), and each
application user is **scoped to its own database**. InnoDB engine is clean — **no deadlocks**,
**0 aborted clients**, only 2 aborted connects over ~19h uptime, no pending flushes.

The demo app DB **`demotwo_appdb` is well-populated and matches the demo spec**: it contains exactly
the expected tables — `users, companies, mailboxes, domains, settings, permissions, logs, notifications`
— all **InnoDB with primary keys** and `utf8mb4`. So the demo-SQL dataset is **NOT thin** for this DB;
that gap does not apply here. `roundcube` has its full 17-table schema (version 2022081200), all InnoDB
with PKs, but is **completely empty** (0 rows in every user/contact/cache table) — a fresh webmail
install with no logins yet.

The only notable gaps are operational: **slow query log is OFF**, and `demotwo_appdb` is the *only*
demo app DB present (single tenant's worth of demo data; if the demo needs multiple app tenants that's
a content gap for the demo-data phase).

---

## Server Facts

| Property | Value |
|----------|-------|
| Version | 10.11.14-MariaDB-0ubuntu0.24.04.1 |
| OS | debian-linux-gnu (Ubuntu 24.04) |
| bind_address | 127.0.0.1 — **not public** ✓ |
| Listening socket | `127.0.0.1:3306` only (mariadbd pid 24666) ✓ |
| Unix socket | `/run/mysqld/mysqld.sock` |
| max_connections | 151 |
| slow_query_log | **OFF** — see Issue S1 |
| long_query_time | 10.0 s |
| Uptime at audit | 69,741 s (~19.4 h) |

### Databases (`SHOW DATABASES`)
`demotwo_appdb`, `roundcube`, plus system DBs `information_schema`, `mysql`, `performance_schema`, `sys`.

### Users (`mysql.user` — user / host / plugin only)

| User | Host | Auth plugin |
|------|------|-------------|
| root | localhost | mysql_native_password (`USING 'invalid' OR unix_socket`) — socket auth ✓ |
| demotwo_appsql | localhost | mysql_native_password |
| roundcube | localhost | mysql_native_password |
| mariadb.sys | localhost | mysql_native_password (locked system acct) |
| mysql | localhost | mysql_native_password (system) |

**No remote (`%` / non-localhost) accounts** — all users are `@localhost` ✓.

### Grants overview (password hashes intentionally not shown)
- `demotwo_appsql@localhost`: `USAGE ON *.*` + `ALL PRIVILEGES ON demotwo_appdb.*` — **scoped** ✓
- `roundcube@localhost`: `USAGE ON *.*` + `ALL PRIVILEGES ON roundcube.*` — **scoped** ✓
- `root@localhost`: `ALL PRIVILEGES ON *.* ... unix_socket WITH GRANT OPTION` — socket-only superuser ✓

No app user has cross-database or global privileges. Least-privilege is correctly applied.

---

## Findings — `demotwo_appdb` (demo application DB)

DB default charset/collation: **utf8mb4 / utf8mb4_general_ci**.

| Table | Engine | Rows | Collation | Size (KB) | PK? |
|-------|--------|-----:|-----------|----------:|:---:|
| users | InnoDB | 10 | utf8mb4_general_ci | 32.0 | ✓ |
| companies | InnoDB | 6 | utf8mb4_general_ci | 16.0 | ✓ |
| mailboxes | InnoDB | 8 | utf8mb4_general_ci | 32.0 | ✓ |
| domains | InnoDB | 6 | utf8mb4_general_ci | 32.0 | ✓ |
| settings | InnoDB | 8 | utf8mb4_general_ci | 32.0 | ✓ |
| permissions | InnoDB | 10 | utf8mb4_general_ci | 16.0 | ✓ |
| logs | InnoDB | 6 | utf8mb4_general_ci | 16.0 | ✓ |
| notifications | InnoDB | 6 | utf8mb4_general_ci | 16.0 | ✓ |

**All 8 spec-required tables present, all InnoDB, all with primary keys.** No missing PKs, no MyISAM.
Sample schema (`users` table) is sensible and relational:
`id (int, PK), company_id (int, FK→companies), name (varchar), email (varchar), role (varchar),
active (tinyint), created_at (datetime)`.

> Per the demo spec ("users, companies, mailboxes, domains, settings, permissions, logs,
> notifications") — **`demotwo_appdb` fully satisfies the demo-SQL table requirement.** The
> "thin demo dataset" risk does **not** apply to this database.

---

## Findings — `roundcube` (webmail DB)

Roundcube schema version **2022081200** (current stable schema). 17 tables, **all InnoDB, all with
PKs**, charsets `utf8mb4_unicode_ci` (a couple `general_ci`).

| Table | Rows | Health |
|-------|-----:|--------|
| users | 0 | no webmail logins yet |
| contacts / contactgroups / contactgroupmembers / collected_addresses | 0 | empty |
| identities | 0 | empty |
| session | 0 | no active sessions |
| cache / cache_index / cache_messages / cache_shared / cache_thread | 0 | empty (clean) |
| dictionary / filestore / responses / searches | 0 | empty |
| system | 1 | holds `roundcube-version = 2022081200` |

Schema is **healthy and complete** — the core `session`, `users`, `contacts`, and `cache_*` tables all
exist with correct structure. The DB is simply unused so far (fresh install). No bloat in `session` or
`cache_*` (a common roundcube problem) because nothing has run yet.

---

## Findings — Engine / Config Health

| Check | Result |
|-------|--------|
| InnoDB latest detected deadlock | **none** ✓ |
| Rolling-back / stuck transactions | none ✓ |
| Pending flushes (fsync) | 0 ✓ |
| Pending reads / writes (LRU, flush list) | 0 / 0 ✓ |
| Aborted_clients | 0 ✓ |
| Aborted_connects | 2 (negligible over ~19h) |
| Threads_connected | 1 |
| Buffer pool dirty pages | 277 (normal) |

`SHOW ENGINE INNODB STATUS` shows a quiet, healthy engine — no errors, no contention.

---

## Issues (by severity)

### S1 — Slow query log disabled (LOW)
`slow_query_log = OFF` and `long_query_time = 10s`. For a demo box this is fine, but with no slow-query
visibility there's no way to catch a regressing query. Informational.

### S2 — Single demo app DB (LOW — content gap, not a defect)
Only `demotwo_appdb` exists as an application DB. It is complete and spec-compliant on its own, but if
the demo narrative needs **multiple** application tenants/databases, additional demo app DBs would need
to be seeded in the demo-data phase. Flagging for that phase — **not** created here per the read-only rule.

### S3 — `mysql_native_password` on app users (INFORMATIONAL)
App users use `mysql_native_password` (rather than the newer ed25519). Standard for Roundcube/PHP
compatibility and localhost-only; no action recommended for a demo box.

---

## Fixes Applied

**None.** Both databases are healthy, schemas are complete with correct engines and primary keys, and
no clearly-missing-and-safe object was identified. The flagged items (slow log, additional demo DBs) are
configuration/content decisions for later phases, not trivially-safe in-place fixes — and the demo box
is read-only by mandate.

---

## Recommendations

1. **(S2 / demo-data phase)** If the demo requires more than one application tenant, seed additional
   app DBs alongside `demotwo_appdb` (same 8-table shape). `demotwo_appdb` itself is a good template —
   it already has users↔companies relationships and realistic row counts.
2. **(S1, optional)** Enable `slow_query_log` with `long_query_time = 1` if you want query-performance
   visibility during demos. Harmless to leave off otherwise.
3. **Roundcube:** schema is current and clean; once it sees real use, watch `session` and `cache_*`
   table growth (roundcube does not always auto-prune) — but nothing to do now.
4. No security changes needed: bind, user scoping, and root socket-auth are all correct.
