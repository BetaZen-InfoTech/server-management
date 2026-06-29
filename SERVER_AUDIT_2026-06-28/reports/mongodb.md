# Agent 5 — MongoDB Audit (read-only)

**Date:** 2026-06-28
**Scope:** MongoDB 8.0 on both demo VPS clones.
**Hosts:** S1 = `89.116.34.207` (`srv1785162`, migration SOURCE) · S2 = `195.35.7.64` (`srv1789639`, migration DEST)
**Deployed code:** local git repo @ `c:/Users/Administrator/Downloads/Project/server-management` (v3.1.109, rev 466b52e)
**Method:** SSH via `bz.py` helper (root). All commands below are exactly what was run. **No mutating commands were issued** — read-only inspection only.

---

## 0. Executive summary

- **Both servers are MongoDB `8.0.26`, FCV `8.0`, standalone, WiredTiger, auth ENABLED, bound to `127.0.0.1` only, no TLS.** The 7.0→8.0 upgrade from the runbook is **complete** on both (binary at 8.0.26 *and* FCV raised to 8.0 — the point-of-no-cheap-return has been crossed; rollback is now dump-restore-only / Path B).
- **Config is byte-identical** between S1 and S2 (`/etc/mongod.conf` diff-clean). **Topology, users, roles, and the full index set (27 collections, 93 indexes) are identical.** Only transient runtime data drifts (metrics sample counts, uptime).
- **Auth is genuinely enforced:** an unauthenticated `listDatabases` is rejected on both (`requires authentication`), and the live instance's `startupWarnings` no longer contains the "Access control is not enabled" line.
- **The globally-unique email index exists** (`users.email_1 UNIQUE`) on both — but it is **case-sensitive** (no collation). Case-insensitivity is enforced only in the Go service layer (`strings.ToLower`). Defense-in-depth gap, not a live integrity bug.
- **No backup automation exists** — no `mongodump` cron/timer, no `/var/backups/mongo-upgrade`, no baseline files. `mongodump`/`mongorestore` (tools 100.17.0) are installed and usable; backup is purely manual / runbook-driven today.
- **Security posture is good for a single-box localhost deployment** (auth on, localhost-only bind). The only real exposure to flag is the **shared password for `admin` (root) and `serverpanel`** (per runbook, both read the same `MONGO_PASS`).

**Health: warnings** (operational gaps: no automated backups, case-sensitive unique email index, shared admin/app password, no TLS, no profiler) — no critical defects.

---

## 1. Versions, packages, tooling

```bash
python bz.py 1 'mongod --version | head -1; dpkg -l | grep -iE "mongo"; apt-mark showhold | grep -i mongo'
python bz.py 2 '... same ...'
```

| Item | S1 | S2 |
|---|---|---|
| `mongod --version` | `db version v8.0.26` | `db version v8.0.26` |
| `db.version()` / `serverStatus().version` | `8.0.26` | `8.0.26` |
| `mongosh` | `2.9.0` | `2.9.0` |
| `mongodb-org` (apt) | `8.0.26` | `8.0.26` |
| `mongodb-org-server / -database / -mongos / -tools / -shell` | `8.0.26` | `8.0.26` |
| `mongodb-database-tools` (`mongodump`/`mongorestore`/`mongoexport`) | `100.17.0` | `100.17.0` |
| `php8.2-mongodb` | `2.1.4` | `2.1.4` |
| apt holds on mongo pkgs | **none** | **none** |

**Drift:** none. Package set is identical down to patch level.
**Note:** runbook §4.6 recommends `apt-mark hold` on the mongo packages post-upgrade; **neither host has holds applied**, so an unattended `apt upgrade` could pull a future minor unexpectedly.

---

## 2. mongod.conf, network, service, process

```bash
python bz.py 1 'cat /etc/mongod.conf; systemctl is-active mongod; systemctl is-enabled mongod; ss -ltnp | grep 27017; ls -ld /var/lib/mongodb; du -sh /var/lib/mongodb; pgrep -a mongod; pgrep -a mongos'
python bz.py 2 '... same ...'
```

`/etc/mongod.conf` is **identical on both hosts**:

```yaml
storage:
  dbPath: /var/lib/mongodb
systemLog:
  destination: file
  logAppend: true
  path: /var/log/mongodb/mongod.log
net:
  port: 27017
  bindIp: 127.0.0.1
processManagement:
  timeZoneInfo: /usr/share/zoneinfo
security:
  authorization: enabled
```

Observations:
- **`bindIp: 127.0.0.1`** — localhost only. Confirmed by `ss`: `LISTEN 127.0.0.1:27017` (S1 pid 23854, S2 pid 23985). **Not publicly exposed.**
- **`security.authorization: enabled`** — present at the bottom of the file. (The file also has a `#security:` comment placeholder near the top; YAML uses the real keyed block, so this is cosmetic, not a duplicate-key problem.)
- **No `storage.wiredTiger.cacheSizeGB`** override → default cache (see §4).
- **No `operationProfiling`** block → profiler off by default.
- **No `replication` / `sharding`** blocks → standalone.
- **No `net.tls` / `net.ssl`** → TLS not configured (consistent with the "No TLS/443" environment note; acceptable because the only client is the local panel over loopback).
- Service: `active` + `enabled` on both. Single `mongod --config /etc/mongod.conf` process; **no `mongos`** (`pgrep -a mongos` → "no mongos (good)").
- Data dir `/var/lib/mongodb` owned `mongodb:mongodb`, **203M** on both.
- `serverpanel` systemd unit has `Requires=mongod.service` / `After=...mongod.service` (matches runbook §0 coupling: **mongod downtime == panel downtime**).

---

## 3. Authentication & users (security posture)

### Auth is enforced (unauthenticated access fails)

```bash
python bz.py 1 'mongosh --quiet --port 27017 --eval "db.adminCommand({listDatabases:1})"'
# -> MongoServerError: Command listDatabases requires authentication   (identical on S2)
```

Both hosts reject unauthenticated `listDatabases` and `getDBNames()`.

### Connection string (from `/opt/serverpanel/.env`, password redacted)

```
MONGO_URI=mongodb://serverpanel:<REDACTED>@127.0.0.1:27017/serverpanel?authSource=admin
MONGO_DB_NAME=serverpanel
MONGO_PASS length: 32   # = openssl rand -hex 16, per install.sh / runbook
```

`authSource=admin` is present (matches the CLAUDE.md requirement). The panel connects as the **`serverpanel`** user, not root.

### Users in `admin` (`db.getSiblingDB('admin').system.users`)

Identical on both hosts:

| user | db | roles |
|---|---|---|
| `admin` | `admin` | `root@admin` |
| `serverpanel` | `admin` | `readWrite@serverpanel`, `dbAdmin@serverpanel` |

- **Least-privilege is respected for the app user** — `serverpanel` is scoped to the `serverpanel` DB (readWrite + dbAdmin), not root. Good.
- **`admin` = `root`** (full cluster admin) — expected for the break-glass account.
- **Security note (from runbook §0, not directly readable here):** both Mongo users share the **same** `MONGO_PASS`. A leak of the app password is therefore also a leak of the root password. This is a deployment design weakness, not a per-host drift.
- `connectionStatus.authInfo` for `admin` reports `root@admin` (verified via auth ping `ok:1`).

### Live startup warnings (auth state proof)

```bash
mongosh -u admin ... --eval 'db.adminCommand({getLog:"startupWarnings"})'
```

The **current** instance's `startupWarnings` (count=4) contains only XFS + THP tuning notes — **NOT** "Access control is not enabled". The "Access control is not enabled" lines that appear earlier in `mongod.log` are from the install-time bootstrap restarts (S1: 19:06:38 / 19:06:41) when auth was briefly off to seed users; the final running mongod has auth ON. Consistent with §3's unauthenticated-access rejection.

---

## 4. Topology, storage, FCV, cache

```bash
mongosh -u admin ... --eval 'serverStatus / getParameter FCV / serverCmdLineOpts / rs.status'
```

| Item | S1 | S2 |
|---|---|---|
| Storage engine | `wiredTiger` | `wiredTiger` |
| FCV (`featureCompatibilityVersion`) | **`8.0`** | **`8.0`** |
| WiredTiger cache (max bytes configured) | `16289628160` (~15.17 GiB) | `16289628160` (~15.17 GiB) |
| Host RAM (`MemTotal`) | `32865096 kB` (~31 GiB) | `32865092 kB` (~31 GiB) |
| `replication` (parsed cmdline) | `null` | `null` |
| `sharding` (parsed cmdline) | `null` | `null` |
| `rs.status()` | error: `not running with --replSet` | error: `not running with --replSet` |
| TLS | `null` (off) | `null` (off) |
| Connections current / available | 11 / 25589 | 11 / 25589 |

- **Standalone confirmed** three ways: `replication=null`, `rs.status()` errors with "not running with --replSet", no `mongos`. Matches the runbook's standalone-only assumption.
- **WiredTiger cache = default** (no config override): 16,289,628,160 B ≈ 15.17 GiB = `50% × (32 GiB − 1 GiB)`, the MongoDB default formula. Sane for a 32 GB box; the working set here is tiny (12 KB data), so cache is vastly oversized for current data — fine, it's just a ceiling.
- **No replica set = no built-in HA and no oplog/PITR.** For a migration this means a `mongodump` (logical) snapshot is the consistency mechanism; there is no point-in-time recovery.

---

## 5. `serverpanel` DB — collections, counts, storage

```bash
mongosh -u serverpanel ... serverpanel --eval 'db.stats(); db.getCollectionNames().forEach(c=>countDocuments)'
```

### dbStats

| metric | S1 | S2 |
|---|---|---|
| collections | 27 | 27 |
| objects (docs) | **94** | **73** |
| dataSize | 12287 B | 10341 B |
| storageSize | 258048 B | 258048 B |
| indexes | **93** | **93** |
| indexSize | 708608 B | 708608 B |
| totalSize | 966656 B | 966656 B |

### Per-collection document counts (only non-zero shown; all others = 0)

| collection | S1 | S2 |
|---|---|---|
| `users` | 2 | 2 |
| `hosting_packages` | 1 | 1 |
| `audit_logs` | 2 | 2 |
| `login_sessions` | 2 | 2 |
| `metrics` | **87** | **66** |
| **all others** (apps, backups, cron_jobs, databases, db_access_hosts, dns_records, dns_zones, domains, email_forwarders, email_installations, email_server_configs, github_deploys, guest_links, mail_logs, mailboxes, project_deployments, project_services, projects, ssl_certificates, api_tokens, webhook_deliveries, webhook_endpoints) | 0 | 0 |

- `users` = 2 (`admin@betazeninfotech.com` = `vendor_owner`, `demo@betazeninfotech.com` = `customer`) on both — matches the stated data state.
- **Drift (benign):** `metrics` 87 (S1) vs 66 (S2) and total docs 94 vs 73. This is the MonitoringService accumulating samples on a `timestamp` field at slightly different uptimes (S1 up 2202 s, S2 up 1657 s at capture). Not a data-integrity concern.
- Real data collections (domains/mailboxes/dns/etc.) are empty on both — consistent with the demo "everything else = 0" state. The PROD inventory targets in the runbook (domains≈192, mailboxes≈32, users≈33) are NOT present here (these are staging clones, as expected).

---

## 6. Indexes (verifying the globally-unique email index + full coverage)

```bash
mongosh -u serverpanel ... serverpanel --eval 'getCollectionNames().forEach(c => getCollection(c).getIndexes())'
```

**Index topology is identical on S1 and S2** (27 collections, 93 indexes). The deployed set exactly matches `backend/internal/database/indexes.go` (`EnsureIndexes`), confirming the Go boot-time index creation ran on both. Highlights:

### Globally-unique email index — PRESENT

```
users:
  _id_       {"_id":1}
  email_1    {"email":1} UNIQUE     <-- the globally-unique email index
  role_1     {"role":1}
```

- The unique email index **exists on both hosts.** ✅
- **However**, the full index spec is `{ v:2, key:{email:1}, name:'email_1', unique:true }` — **no `collation`**. The index is byte-exact / **case-sensitive**: `Admin@x.com` and `admin@x.com` would be two distinct keys at the DB layer.
- The CLAUDE.md guarantee ("email globally unique ... enforced **case-insensitively** plus a unique MongoDB index") is satisfied **only because the Go service layer lowercases first** (`auth_service.go:121` `loginEmail := strings.ToLower(strings.TrimSpace(req.Email))`, plus `user_service.go:62/271`). The DB index alone would NOT block a mixed-case duplicate if any future code path inserted without normalizing. Defense-in-depth gap (low severity given current code always normalizes). `mailboxes.email_1` (UNIQUE) has the same case-sensitive shape.

### Unique constraints present (all confirmed on both)

`users.email` · `domains.domain` · `apps.name` · `databases.db_name` · `mailboxes.email` · `email_forwarders.{source,domain}` · `dns_zones.domain` · `ssl_certificates.domain` · `db_access_hosts.{database_id,host}` · `project_services.{project_id,name}` · `projects.slug` · `projects.{tenant_id,name}` (partial, `tenant_id $exists`) · `mail_logs.log_key` · `api_tokens.token_id` · `guest_links.link_id`.

### TTL indexes (auto-expiry) present on both

- `mail_logs.created_at` → `expireAfterSeconds = 7776000` (90 days). Matches `indexes.go` (`90*24*60*60`).
- `webhook_deliveries.created_at` → `expireAfterSeconds = 2592000` (30 days).

### Partial indexes present on both

- `project_services.primary_domain` PARTIAL `{primary_domain:{$gt:""}}`
- `projects.{tenant_id,name}` UNIQUE PARTIAL `{tenant_id:{$exists:true}}`

### Missing-index observations

- `hosting_packages` and `login_sessions` have **only the default `_id_` index.** `EnsureIndexes` defines no secondary indexes for them, so this is by-design, not drift. `login_sessions` is queried by session/user; if that collection grows it would COLLSCAN — worth a future index, but volume is 2 rows today (no impact).
- No index gaps vs the source code — deployed indexes are a 1:1 match with `indexes.go`. The historical "metrics indexed on the wrong field" bug noted in `indexes.go` is already fixed: live index is `metrics.timestamp_-1` (the real field MonitoringService writes/queries), not the old `collected_at`.

---

## 7. Profiler / slow-query logging

```bash
mongosh ... serverpanel --eval 'db.getProfilingStatus()'
grep -c 'Slow query' /var/log/mongodb/mongod.log
```

| Item | S1 | S2 |
|---|---|---|
| `getProfilingStatus()` | `{ was:0, slowms:100, sampleRate:1 }` | same |
| "Slow query" lines in `mongod.log` | 0 | 0 |

- **Profiler is OFF** (`was:0`) on both — the default. Slow ops above `slowms:100` would still be written to `mongod.log` as `Slow query` entries, but there are **zero** such lines (tiny dataset, no slow ops). No `system.profile` collection is being populated. For a demo this is fine; for PROD, enabling profiling level 1 (or lowering `slowms`) during/after migration would help catch regressions — currently there is no slow-query observability beyond the log.

## 8. Startup warnings, THP, ulimits (benign tuning)

```bash
mongosh -u admin ... --eval 'db.adminCommand({getLog:"startupWarnings"})'
cat /sys/kernel/mm/transparent_hugepage/enabled; cat /proc/<mongod-pid>/limits
```

Current `startupWarnings` (count=4, identical on both):
1. **XFS recommended with WiredTiger** (`id:22297`) — filesystem is ext4, not XFS. MongoDB performance advisory only; harmless at this scale.
2/3. **THP `enabled`/`defrag`** tuning suggestions (`id:9068900`) — under MongoDB 8.0's `tcmalloc-google` allocator the *suggested* value is `enabled=always` / `defrag=defer+madvise`; current kernel is `[madvise]` for both. Suggestion, not error.
4. **`khugepaged/max_ptes_none` suggested 0** (`id:8640302`), current 511. Suggestion only.

- **No `Access control is not enabled`** warning in the live instance (auth is on — see §3).
- **ulimits healthy:** mongod process `Max open files = 64000`, `Max processes = 64000` (well above MongoDB's recommended 64000 nofile). No rlimit warning.
- All four warnings are benign and **identical** across S1/S2.

---

## 9. Backup approach / migration readiness

```bash
ls -la /var/backups/mongo-upgrade            # -> no such dir (both hosts)
ls /root/*baseline* /root/*dbstats*          # -> none
crontab -l; ls /etc/cron.d /etc/cron.daily | grep -i mongo   # -> no mongo cron
systemctl list-timers | grep -i mongo        # -> no mongo systemd timers
which mongodump mongorestore mongoexport      # -> /usr/bin/* present
```

- **No automated backups of any kind.** No `mongodump` cron, no systemd timer, no `/var/backups/mongo-upgrade`, no leftover runbook baseline files (`/root/mongo-preupgrade-baseline.json`, etc.). The 7.0→8.0 upgrade artifacts (archive/tar/sha256) are **not retained on disk** on either host — either cleaned up post-sign-off or the upgrade was performed without persisting them here.
- **Tooling is ready:** `mongodump` / `mongorestore` / `mongoexport` are installed (`mongodb-database-tools 100.17.0`) and on `$PATH` at `/usr/bin/`. A migration dump/restore is fully supported.
- **Migration-relevant facts (dump/restore plan):**
  - Both standalone + auth on → use `mongodump -u admin -p <MONGO_PASS> --authenticationDatabase admin --host 127.0.0.1 --port 27017 --archive=… --gzip` (whole instance, captures `admin` users + `serverpanel` data), then `mongorestore --gzip --archive=… --drop`. This is exactly the runbook §2/§6 approach.
  - **Quiesce the panel first** (`systemctl stop serverpanel`) because there's no replica-set oplog for a consistent online dump; standalone dumps are point-in-time only when writes are stopped.
  - Dataset is trivially small (203M data dir, 12 KB logical, ≤94 docs) → dump/restore is seconds-fast. Wall-clock is dominated by the deliberate gates in the runbook, not data volume.
  - **FCV is already 8.0 on both** → for these staging boxes, binary-only rollback is no longer possible (Path B / dump-restore only). For the actual S1→S2 *data* migration (source→dest), a logical `mongodump`/`mongorestore` between two same-version (8.0.26) instances is straightforward and the unique indexes (esp. `users.email`) will be recreated identically by `EnsureIndexes` on panel boot even if the restore is data-only.

---

## 10. Drift summary: S1 vs S2

| Dimension | Drift? | Detail |
|---|---|---|
| MongoDB version / FCV | No | both 8.0.26 / FCV 8.0 |
| Package set & versions | No | identical, incl. tools 100.17.0 |
| `/etc/mongod.conf` | No | byte-identical |
| Users / roles | No | `admin`=root, `serverpanel`=readWrite+dbAdmin |
| Auth enforcement | No | both reject unauth `listDatabases` |
| Collections (27) & indexes (93) | No | identical topology incl. unique email index, TTLs, partials |
| WT cache / RAM / ulimits | No | 15.17 GiB cache, ~31 GiB RAM, 64000 nofile |
| Profiler / TLS / replication | No | off / off / standalone on both |
| **Document counts** | **Yes (benign)** | `metrics` 87 (S1) vs 66 (S2); total 94 vs 73 — monitoring samples accumulating at different uptimes |
| apt holds | No (both absent) | neither host has mongo pkgs held |
| Backup automation | No (both absent) | no cron/timer/backups on either |

**Net: no meaningful drift.** The only difference is the live `metrics` sample count, which is expected and self-correcting.

---

## 11. Findings & recommendations (read-only — nothing was changed)

1. **No automated MongoDB backups (medium).** Neither host has a `mongodump` cron/timer or any retained dump. A disk failure today loses all panel data. *Recommend:* a scheduled `mongodump --gzip --archive` with off-box copy + a TTL/rotation, even for staging; mandatory before PROD migration. (Tools already installed.)
2. **`users.email` / `mailboxes.email` unique index is case-sensitive (low).** Uniqueness only holds because the Go layer lowercases (`auth_service.go:121`, `user_service.go:62/271`). *Recommend:* add a case-insensitive **collation** (`{locale:'en', strength:2}`) to the unique email indexes as defense-in-depth so a future un-normalized insert path can't create `Admin@x` + `admin@x`. (Index rebuild — do under maintenance, not in this audit.)
3. **Shared password for `admin` (root) and `serverpanel` (medium).** Both read the same `MONGO_PASS`. *Recommend:* give the app user a distinct credential so an app-side leak doesn't hand over cluster root. (Deployment/install.sh change.)
4. **apt holds not applied (low).** Runbook §4.6 recommends `apt-mark hold` on the mongo packages post-upgrade; neither host has them. *Recommend:* apply holds so an unattended upgrade can't pull a surprise minor and trigger a coupled panel restart.
5. **No query profiling / slow-query visibility (low/info).** Profiler off, zero `Slow query` log lines. Fine for the demo; *recommend* enabling profiling level 1 (or `slowms` tuning) around the PROD migration to catch regressions.
6. **No TLS on 27017 (info — accepted).** Acceptable here: localhost-only bind, single-box, no remote DB clients. Documented as a known constraint, not a defect.

**No critical issues.** Auth is enforced, the DB is localhost-bound, the globally-unique email index exists, indexes match the source code on both hosts, and the two clones are in lockstep apart from transient metrics samples.
