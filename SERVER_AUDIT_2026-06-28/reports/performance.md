# AGENT 11 — Performance Audit

**Date:** 2026-06-28
**Scope:** Both VPS clones — Server 1 `89.116.34.207` (migration SOURCE), Server 2 `195.35.7.64` (migration DEST)
**Deployed code:** local git repo `c:/Users/Administrator/Downloads/Project/server-management` (v3.1.109, rev 466b52e)
**Method:** READ-ONLY. Runtime state via SSH helper `bz.py`; query/worker patterns read from local Go source. Servers are idle — focus is config-level analysis + load projections, with concrete code/runtime evidence.

---

## 0. Executive summary

Both servers are **byte-for-byte identical** in service config and run an 8-vCPU AMD EPYC 9354P / 32 GB / 387 GB SSD (virtio, non-rotating) VPS. At idle they are healthy: load < 0.2, 0 % iowait, ~1.2 GB RAM used, panel API health responds in **0.5–4 ms**. There are **no current bottlenecks** because the data set is empty.

The risk is entirely **config-default + at-scale**:

1. **MariaDB InnoDB buffer pool = 128 MB (stock default)** on a 32 GB box — will thrash badly once webmail (Roundcube) has real users.
2. **MongoDB WiredTiger cache is unbounded (no `cacheSizeGB`)** → defaults to ~15.5 GB reserved, on a box that also runs MariaDB + SpamAssassin + the panel, **with 0 swap** — a memory-pressure / OOM-kill hazard at scale.
3. **`mail_logs` / `audit_logs` / `domains` list queries have no compound (filter+sort) index**, use **unanchored case-insensitive `$regex` search (guaranteed COLLSCAN)**, and **skip-based deep pagination** — these are the queries that *will* hurt as `mail_logs` grows on a busy mail server.
4. **nginx `gzip on` but no `gzip_types`** → JSON API + JS/CSS bundles are served **uncompressed** (only `text/html` is gzipped by default). Cheap, safe, high-value fix.
5. **0 swap on 31 GB usable** — opinion: add a small (2–4 GB) emergency swap as a safety valve given the unbounded Mongo cache.

Drift between Server 1 and Server 2: **none of substance**. Only differences are per-host (hostname in slow-log filename, IP in nginx `server_name`) and transient noise — Server 2 showed ~0.5 % CPU **steal** vs Server 1's ~0 %, suggesting a slightly busier hypervisor neighbor on the destination, but it is negligible at idle.

---

## 1. OS baseline (load / idle)

Command (run on both via `baseline.sh`):
```bash
uptime; nproc; free -m; swapon --show; cat /proc/sys/vm/swappiness; vmstat 1 3; top -bn1 | head -25
```

| Metric | Server 1 (SOURCE) | Server 2 (DEST) |
|---|---|---|
| CPU | AMD EPYC 9354P, **8 vCPU** | identical |
| RAM | 32094 MB total, **1205 used**, 6317 buff/cache, 30889 avail | 32094 MB, **1208 used**, 30886 avail |
| **Swap** | **0 / 0 / 0** | **0 / 0 / 0** |
| swappiness | 60 | 60 |
| loadavg (1/5/15) | 0.12 / 0.16 / 0.20 | 0.15 / 0.09 / 0.15 |
| `vmstat` cpu (us/sy/id/wa/st) | 0/0/99/0/0 | 0/0/99/0/**1** |
| `top` %Cpu steal | 0.0 | **2.3** (transient) / `mpstat` avg 0.50 |
| uptime | 42 min | 38 min |

`mpstat 1 2` averages: S1 `%steal 0.25 / %idle 99.50`; S2 `%steal 0.50 / %idle 99.19`. Load normalized: **0.016 / core (S1), 0.006 / core (S2)** — effectively idle.

**Findings**
- **No swap on either host** (`SwapTotal: 0 kB`). With a 32 GB box that *also* lets MongoDB reserve ~15.5 GB of WiredTiger cache by default, the only relief valve under memory pressure is the OOM killer. **Opinion:** provision a small (2–4 GB) swapfile and keep `vm.swappiness=10` so it's an emergency cushion, not a hot-path. This is a deliberate, conservative safety net, not a substitute for sizing the DB caches (items below).
- `swappiness=60` is the desktop default; with a server workload + a swapfile, 10 is more appropriate.
- Server 2 carries slightly more hypervisor **steal** than Server 1. Worth re-checking under real load on the destination after migration cutover; not actionable now.

---

## 2. Per-process CPU / memory

Command: `ps -eo pid,user,%cpu,%mem,rss,comm --sort=-rss | head -21` and an RSS-by-binary rollup.

Top resident consumers (RSS, MB) — **identical on both servers**:

| Service | RSS (MB) | Note |
|---|---|---|
| spamd (SpamAssassin, 3 procs) | **~401** | largest consumer; `--max-children 5` |
| mongod | ~204 (cold) | will grow toward WiredTiger cache ceiling under load |
| php-fpm8.2 (3 procs) | ~120 | Roundcube webmail FastCGI |
| mariadbd | ~115 | buffer pool only 128 MB |
| pdns_server | ~63 | |
| node + PM2 | ~116 | PM2 god + 1 managed node proc under `/root/.pm2` |
| nginx (master + 8 workers) | ~51 | |
| fail2ban-server | ~34 | 1 jail (sshd) |
| **server (the Go panel)** | **~30** | very lean; `MemoryCurrent` via systemd = 18 MB |

**Findings**
- The Go panel is extremely lean (RSS ~30 MB, systemd `MemoryCurrent=18 MB`, `TasksCurrent=14–16`). No memory concern there.
- SpamAssassin's `spamd` is the heaviest single component (~400 MB) and is the main steady-CPU contributor when mail flows (each scan forks a child, `--max-children 5`).
- `top -bn1` CPU columns are dominated by the SSH helper itself (transient) — there is no runaway process on either host.

Evidence (panel CPU consumed since start, `systemctl show serverpanel -p CPUUsageNSec`): **S1 14.7 s** over ~38 min, **S2 10.2 s**. That non-zero idle drift is the panel's own per-minute metrics collector (see §6).

---

## 3. MongoDB performance

`mongod 8.0.26`, WiredTiger, bound `127.0.0.1:27017`, auth enabled, config `/etc/mongod.conf`. The app user `serverpanel` lacks `clusterMonitor`, so `serverStatus`/`currentOp` returned `not authorized` (read-only auditor, did not escalate). Cache/connection live counters could not be pulled, but config + index posture is fully assessable.

### 3a. Cache sizing (HIGH at scale)

`/etc/mongod.conf` has the `wiredTiger` block **commented out** — no `engineConfig.cacheSizeGB`:
```
storage:
  dbPath: /var/lib/mongodb
#  engine:
#  wiredTiger:
```
With no override, WiredTiger uses `max(50% of (RAM − 1 GB), 256 MB)` = **~15.5 GB reserved** on a 32 GB host. mongod is only ~200 MB RSS now because the DB is tiny (`db.stats()`: dataSize **14.8 KB** S1 / **12.6 KB** S2, storageSize 258 KB, indexSize **708 KB**), but the cache grows lazily toward that ceiling under load.

**Finding:** on a box co-hosting MariaDB, SpamAssassin (~400 MB), php-fpm, PowerDNS, nginx and the panel — **with 0 swap** — letting Mongo claim half of RAM is a real OOM hazard at scale. **Recommendation:** pin `storage.wiredTiger.engineConfig.cacheSizeGB` explicitly (e.g. 4–8 GB for this workload — the panel's working set is small: counts, lists, audit/mail logs) so total committed memory across all services stays well under 32 GB. (Config change — operator action, not auto-applied.)

### 3b. Profiling / slow ops

`db.getProfilingStatus()` → `{"was":0,"slowms":100,"sampleRate":1}` on both. Profiling **off** (level 0). Slow-query log threshold is the default 100 ms; `mongod.log` shows only normal startup lines (WiredTiger opened in 158–666 ms across restarts), **no COLLSCAN or slow-query entries** — expected, since the DB is empty and idle.

### 3c. Indexes (mostly good; specific compound-index gaps)

`db.getCollectionNames()` + `getIndexes()` confirmed **93 indexes across 27 collections** (matches `internal/database/indexes.go`). The base posture is solid: every high-cardinality lookup field is indexed, `mail_logs` has a **90-day TTL index** on `created_at` (`SetExpireAfterSeconds(90*24*3600)`) so the busiest collection can't grow unbounded, and there's a unique `log_key` for idempotent upserts.

**Gaps that will hurt at scale** (the exact collections the assignment flags — `mail_logs`/`audit_logs`/`domains`/`mailboxes`):

- **`mail_logs` — no compound (filter + sort) index.** The list query (`mail_log_service.go:664`) *always* sorts `first_seen: -1` and layers filters `direction`/`status`/`source`/`domains`. Each facet has its own single-field index, but Mongo can use only one per query — so it either filters via (say) `domains_1` then does an **in-memory blocking sort** on `first_seen` (32 MB sort cap → risk of *"Sort exceeded memory limit"* on a busy tenant), or walks `first_seen_-1` filtering as it scans. **Add** `{domains:1, first_seen:-1}` (tenant list) and `{status:1, first_seen:-1}` / `{direction:1, first_seen:-1}` for the common facet+sort paths.
- **`audit_logs` — no `{user.id:1, timestamp:-1}` compound.** `GetWHMActivity`/`GetCPanelActivity` (`dashboard_service.go:263-314`) filter `user.id` and sort `timestamp:-1 limit 10`. The owner path (no filter) is covered by `timestamp_-1`; the per-user path is not — it'll filter via `user.id_1` then in-memory sort.
- **`domains` — sort field `created_at` is not indexed.** `DomainService.List` (`domain_service.go:179`) sorts `created_at:-1` but the only domain indexes are `domain`, `user`, `status`. Every domain-list page does an in-memory sort.
- **`metrics` — query filters `{timestamp:$gte, metric:X}` but only `timestamp_-1` exists.** Minor (capped at ~30 k docs by 7-day retention), but `{metric:1, timestamp:1}` would make the chart query covered.

---

## 4. MariaDB performance

`10.11.14-MariaDB`, root socket auth, bound `127.0.0.1:3306`, sole real consumer = `roundcube` DB (currently empty). All config is **stock Debian default** on both servers.

Command: `mysql -N -e "SHOW VARIABLES/STATUS LIKE ..."`

| Variable | Value (both servers) | Assessment |
|---|---|---|
| **`innodb_buffer_pool_size`** | **134217728 (128 MB)** | **Far too small** for a 32 GB host |
| `innodb_buffer_pool_instances` | (default) | fine for 128 MB |
| `innodb_log_file_size` | 100663296 (96 MB) | ok |
| `innodb_flush_method` | `O_DIRECT` | good (avoids double-buffering) |
| `innodb_flush_log_at_trx_commit` | 1 (full ACID) | safe default; fine for webmail |
| `innodb_io_capacity` | 200 | low for SSD; 1000–2000 appropriate for NVMe/virtio-SSD |
| `max_connections` | 151 | adequate (php-fpm max_children=5 → tiny demand) |
| **`slow_query_log`** | **OFF** | no slow-query visibility |
| `long_query_time` | 10 s | too coarse even if enabled |
| `log_queries_not_using_indexes` | OFF | |
| `query_cache_type` | OFF | correct (deprecated/removed) |
| `thread_cache_size` | 151 | fine |
| `table_open_cache` | 2000 | fine |
| `Innodb_buffer_pool_pages_free` | 7695 / 8112 total | 95 % free — empty DB, nothing cached yet |
| `Threads_connected` / `Max_used_connections` | 1 / 1 | idle |
| `Slow_queries` | 0 | idle |

**Findings**
- **128 MB buffer pool** is the headline. Roundcube's working set (sessions, `contacts`, `cache_messages`, `cache_index`, `cache_thread`) lives in InnoDB; once webmail has active users the 128 MB pool will evict constantly and every miss becomes a disk read. On a box with 31 GB free RAM and a tiny number of other MariaDB consumers, **bumping to ~1–2 GB** is safe and high-value. (Pair with the Mongo cache cap in §3a so the two DB caches + SpamAssassin + panel all fit under 32 GB.)
- **Slow query log OFF + `long_query_time=10`** means there's zero visibility into Roundcube query regressions. Enable slow log with `long_query_time=1` (and optionally `log_queries_not_using_indexes`) to a file — read-only diagnostic value.
- `innodb_io_capacity=200` is a spinning-disk-era default; the disk is non-rotating (`ROTA=0`). Raising to ~1000 lets InnoDB flush more aggressively, matching the SSD.

---

## 5. Mail queue throughput config

`postqueue -p` → **"Mail queue is empty"** on both; `find /var/spool/postfix/{maildrop,incoming,active,deferred}` → 0 files. Nothing queued, nothing deferred.

Throughput-relevant `postconf` values (identical both servers):

| Param | Value | Note |
|---|---|---|
| `default_process_limit` | 100 | global per-service smtpd/smtp cap |
| `smtpd_client_connection_count_limit` | 50 | |
| `default_destination_concurrency_limit` | 20 | parallel deliveries per destination |
| `qmgr_message_active_limit` | 20000 | active queue size |
| `queue_run_delay` / `minimal_backoff_time` | 300 s / 300 s | |
| `maximal_backoff_time` | 4000 s | |
| `maximal_queue_lifetime` | 5 d | |
| `in_flow_delay` | 1 s | |
| `content_filter` | **(empty)** | no amavis/after-queue filter → SpamAssassin runs via milter/`spamc`, not a queue choke point |

`master.cf`: `qmgr` and `pickup` run at the standard single-process service limits; `smtp`/`smtpd`/`relay` use the global `default_process_limit=100`. `smtps`(465)/`submission`(587) require SASL + TLS.

**Dovecot:** `default_process_limit=100`, `default_client_limit=1000` (defaults).
**SpamAssassin:** `spamd --max-children 5`.

**Findings**
- For a small-to-mid mailflow these defaults are fine. The realistic throughput ceiling is **SpamAssassin's 5 children**: under an inbound burst, smtpd will accept faster than 5 concurrent SA scans can clear, so scan latency (not Postfix) becomes the queue-fill driver. If this becomes a managed-hosting mail server, raise `--max-children` (each child ~130 MB RSS — budget against RAM) **after** the DB caches are sized.
- `content_filter` empty is good for throughput (no after-queue reinjection). No tuning needed for the demo's load.
- No mail-queue bottleneck exists or is projected at demo scale.

---

## 6. Background workers / cron frequency

### Panel-internal goroutines (`backend/cmd/server/main.go`, confirmed in `journalctl -u serverpanel`)
| Worker | Interval | Source | Note |
|---|---|---|---|
| API-token + guest-link expiry sweep | **30 min** | main.go:347 | two `UpdateMany`-style sweeps; cheap |
| **Metrics collector** | **60 s** | main.go:359, `monitoring_service.go:212` | see below |
| Mail-log ingestor (tails `/var/log/mail.log`) | continuous | main.go:364 | upsert-per-message |
| project-deploy pool | n/a | log: `workers:4, queue_buffer:256` | idle |
| (other) 6 h, 24 h tickers | 6 h / 24 h | main.go:785/866 | infrequent maintenance |
| transfer-token sweep | 1 h | transfer_token_service.go:289 | |
| WS ping | 30 s/conn | ws_handler.go:24 | per live socket |

**Metrics collector cost (`monitoring_service.go:231-281`)** — runs every 60 s and, each tick, **shells out 3 subprocesses**:
- `bash -c "top -bn1 | grep 'Cpu(s)' | awk '{print $2}'"` — `top -bn1` self-samples ~1.5 s of CPU,
- `free -b`,
- `df -B1 /`,
then 1–3 `InsertOne` + 1 `DeleteMany` (7-day retention, line 280).

That's **~4,320 process spawns/day** purely for metrics and is the source of the panel's non-zero idle CPU (S1 14.7 s / S2 10.2 s consumed in ~38 min). Negligible on an 8-core idle box, but it's avoidable churn — CPU% can be read by diffing `/proc/stat` twice instead of forking `top`. (`dashboard_service.go:getCPUPercent` already reads `/proc/stat` directly — but only **once**, so it reports cumulative-since-boot %, ~0, not live CPU; a correctness nit, not a perf issue.)

### System cron / systemd timers (`/etc/cron.d`, `systemctl list-timers`) — identical both servers
- `serverpanel-mail-ssl-sweep` → `bzpanel mail-ssl-sweep` **hourly** (`:17`).
- Roundcube `gc.sh` every 30 min, `cleandb.sh` daily 05:00.
- certbot renew twice daily; sysstat collect every 10 min; php sessionclean twice hourly; standard Ubuntu dailies.
- **No root crontab.** 19 systemd timers, all standard/low-frequency.

**Finding:** worker cadence is reasonable. The only avoidable cost is the per-minute `top -bn1` fork in the metrics collector. No worker is a bottleneck at any realistic scale.

---

## 7. nginx config (workers / gzip / keepalive / buffering)

`nginx/1.24.0`. `nginx.conf` + `sites-enabled/serverpanel` (identical except the IP in `server_name`).

```
worker_processes auto;          # 8 worker procs confirmed via ps
events { worker_connections 768; }   # Debian default
gzip on;                        # NO gzip_types / gzip_min_length / gzip_comp_level / gzip_vary
sendfile on; tcp_nopush on;     # tcp_nodelay NOT set
# no keepalive_timeout / keepalive_requests in http{}
# no upstream{} block → no keepalive to the :8080 panel (new TCP conn per proxied request)
```

vhost `/` (panel) and `/ws/` proxy to `http://127.0.0.1:8080` with:
`proxy_request_buffering off`, `proxy_read_timeout 86400s` (`/`) / `3600s` (`/ws/`), `client_max_body_size 10G`, `client_body_timeout 3600s`, `send_timeout 3600s`. `/webmail/` served via php-fpm FastCGI; phpMyAdmin via snippet. **No `listen 443` / HTTP/2** (by design — bare-IP, no TLS).

**Findings (ranked)**
1. **`gzip on` with no `gzip_types` (MEDIUM, safe fix).** nginx's default `gzip_types` is `text/html` only — so **every JSON API response and every JS/CSS SPA bundle is sent uncompressed**. For an SPA-heavy panel this is the single highest-value, lowest-risk perf win. Add `gzip_types application/json application/javascript text/css text/xml application/xml image/svg+xml; gzip_min_length 1024; gzip_comp_level 5; gzip_vary on;`.
2. **`worker_connections 768` (LOW–MEDIUM).** 8 workers × 768 = 6,144 max connections, and each proxied request consumes *two* (client + upstream). For a panel + webmail + long-lived WebSockets this ceiling is low. Raise to 4096–8192 and set `worker_rlimit_nofile` accordingly.
3. **No upstream keepalive to :8080 (LOW).** Every proxied request opens a fresh TCP connection to the Go server. An `upstream panel { server 127.0.0.1:8080; keepalive 32; }` with `proxy_http_version 1.1; proxy_set_header Connection "";` on `location /` removes per-request connection setup. (Note: the current single `proxy_set_header Connection "upgrade"` on `/` is there for WebSocket upgrade but is applied to *all* `/` traffic — a dedicated upstream + a separate WS location would be cleaner and faster.)
4. **`proxy_read_timeout 86400s` on `/` (LOW).** 24 h read timeout on the whole API surface means a stuck upstream request ties up a worker connection for a day. Long timeout belongs only on streaming/WS endpoints; the general API can be 60–300 s.
5. `tcp_nodelay` not set (default off in this config) — enabling it slightly improves small-response latency for the proxy. Minor.

---

## 8. Panel binary startup time + disk I/O

### Startup (`journalctl -u serverpanel`)
Both servers start cleanly in **well under one second** — every log line (Starting → Connected to MongoDB → deploy pool → mail-log ingestor → HTTP server listening → Fiber banner) shares the same one-second timestamp:
```
19:12:52 Started serverpanel.service
19:12:52 Starting Betazen Server Panel
19:12:52 Connected to MongoDB
19:12:52 project deploy pool started  workers:4 queue_buffer:256
19:12:52 mail-log: ingestor started
19:12:52 Starting HTTP server  addr=":8080"
```
`systemctl show serverpanel`: **`NRestarts=0`**, `ActiveState=active`, `MemoryCurrent` 18 MB. Service unit: `Type=simple`, `Restart=always`, `RestartSec=5`, `Requires=mongod.service`, runs as **root** in `/opt/serverpanel`. **No `GOMAXPROCS`/`GOGC`/`GOMEMLIMIT`** env set — Go 1.22 auto-uses all 8 cores (correct on a non-cgroup-capped VPS); default GC is fine for an 18 MB heap. Live health latency: **0.5–4 ms** across samples on both.

`systemd-analyze blame` (service contributions to boot): postfix ~0.7 s, mariadb 0.25–0.53 s, pdns ~0.1 s, nginx/dovecot ~20 ms each. None is a concern.

### Disk / I/O
- **Device:** single `sda` 400 GB, **`ROTA=0` (SSD-backed virtio)**, I/O scheduler **`[none]`** — correct for virtio/NVMe (avoids redundant reordering). `df -h /` → 387 G, **2 % used** (7.3 G).
- `iostat -dx 1 2`: first sample shows lifetime-since-boot averages (high `f_await` 36–44 ms is cumulative fsync incl. boot writes); the **second 1-second sample is all zeros on both hosts** → disk is genuinely idle, no live I/O pressure.
- `/var/lib/mongodb` only 0.2 G, `/var/mail` 0 G (panel WHM `resources/summary` endpoint confirmed).
- Tunables: `vm.dirty_ratio=20`, `dirty_background_ratio=10` (defaults, fine for SSD); `transparent_hugepage = madvise` (acceptable for MongoDB — `madvise`/`never` both fine, only `always` is discouraged); `vm.overcommit_memory=0` (heuristic — combined with 0 swap + unbounded Mongo cache, see §1/§3a). `fstrim.timer` active (good for SSD longevity).

**Findings:** startup, scheduler, and idle I/O are all healthy and identical across both servers. The only disk-adjacent risk is indirect: `df`/`du` shell-outs in hot paths (§9) and metrics fork churn (§6) generate steady small I/O; and `dashboard_service.GetCPanelStats` runs **`du -sb /home/<user>` on every dashboard load** (5 s timeout) — a synchronous disk-walk that scales with home-dir size and competes with mailbox I/O.

---

## 9. Backend hot-path query patterns (read from local source, v3.1.109)

Inspected `internal/services/{dashboard,domain,mail_log,monitoring,resource}_service.go` and `internal/database/indexes.go`.

**Good news (no N+1 found in the obvious places):**
- `DomainService.EnrichOwnerEmails` (`domain_service.go:215`) caches vendor-email lookups per username — a vendor with 50 domains costs **1** lookup, not 50. Comment is accurate.
- Dashboard counts use `CountDocuments` per collection (4–6 cheap indexed counts), not row fetches.
- mail-log ingestor upserts by unique-indexed `log_key` — idempotent and index-backed.

**Projected bottlenecks (data-volume dependent — current counts are 0):**

1. **Unanchored case-insensitive `$regex` search = guaranteed COLLSCAN.**
   - `mail_log_service.go:629-637`: search builds `$or` of `{sender|recipients.address|subject|message_id: /QuoteMeta(q)/i}` — none text-indexed, `i`-option regex can't use a btree → full scan per search on a collection that grows fastest of all.
   - `domain_service.go:160-165`: search `$or` of `{domain|user: /q/i}` — same, scans domains.
   - **Fix:** a MongoDB `text` index (or anchored `^prefix` regex for prefix search), and/or restrict free-text search to indexed prefixes.

2. **Sort field without matching index → in-memory blocking sort (32 MB cap).**
   - `mail_log_service.go:664-667`: filter facets + `SetSort(first_seen:-1)` with no compound index (§3c). Highest-volume path; risk of *"Sort exceeded memory limit"* for a busy tenant.
   - `domain_service.go:179`: `SetSort(created_at:-1)` with no `created_at` index on domains.
   - `dashboard_service.go:278`: audit activity `sort timestamp:-1 limit 10` filtered by `user.id` with no compound (§3c).

3. **Skip-based deep pagination — O(N) skip.**
   - `mail_log_service.go:666` `SetSkip((page-1)*limit)` and `domain_service.go:178` skip — page *P* scans `(P-1)*limit` docs just to discard them. Combined with #2, deep pages on a large `mail_logs` are doubly expensive. Prefer range/cursor pagination (`first_seen < lastSeenOfPrevPage`).

4. **Two full passes per list call.** Each `List` runs `CountDocuments(filter)` **and** `Find(filter)` — the count alone scans when the filter isn't fully indexed. For large collections, consider an estimated count or caching totals.

5. **Per-request shell-outs on hot read paths.**
   - `dashboard_service.GetCPanelStats` → `du -sb /home/<user>` (line 160) **every** dashboard render — synchronous disk walk, 5 s timeout, scales with home size. Should be cached / sampled out-of-band (e.g. by the existing metrics collector) not computed inline.
   - `getDiskPercent` → `df`, `ResourceService.Summary` → `df -BG`, metrics collector → `top/free/df` per minute (§6).

6. **Correctness nit (not perf):** `dashboard_service.getCPUPercent` (line 326) reads `/proc/stat` **once** and divides cumulative jiffies → returns near-constant ~0 % rather than live CPU. Needs a two-sample delta. Flagging because the dashboard "CPU" tile is effectively meaningless, not because it's slow.

---

## 10. Drift between Server 1 and Server 2

| Area | Server 1 (SOURCE) | Server 2 (DEST) | Drift? |
|---|---|---|---|
| CPU / RAM / disk / scheduler | EPYC 9354P 8c / 32 GB / sda SSD `[none]` | identical | No |
| Swap | 0 | 0 | No |
| MongoDB 8.0.26 cfg + 93 indexes | as above | identical | No |
| MariaDB 10.11.14 defaults (128 MB pool, slow-log off) | as above | identical | No |
| Postfix/Dovecot/SA limits | as above | identical | No |
| nginx (768 conns, gzip, no gzip_types, no 443) | as above | identical (IP differs in `server_name`) | Cosmetic only |
| Panel startup / `NRestarts=0` / health latency | clean | clean | No |
| CPU steal (idle) | ~0 % | ~0.5 % avg (2.3 % spot) | Minor (hypervisor neighbor) |
| slow_query_log_file name | `srv1785162-slow.log` | `srv1789639-slow.log` | Per-host (hostname) |

**Conclusion:** the two hosts are configuration twins. The only meaningful runtime difference is slightly higher hypervisor steal on the destination (Server 2) — worth watching post-cutover but immaterial at idle.

---

## 11. Prioritized recommendations

| # | Recommendation | Severity | Server | Safe auto-fix? |
|---|---|---|---|---|
| 1 | Cap MongoDB WiredTiger cache (`storage.wiredTiger.engineConfig.cacheSizeGB`, e.g. 4–8 GB) — default ~15.5 GB on 0-swap box is OOM risk at scale | High | both | No (config + restart) |
| 2 | Raise MariaDB `innodb_buffer_pool_size` 128 MB → ~1–2 GB (size jointly with #1) | High | both | No (config + restart) |
| 3 | Add `gzip_types` (JSON/JS/CSS/SVG) + `gzip_min_length`/`gzip_vary` to nginx | Medium | both | Yes (reload) — but report-only here (read-only audit) |
| 4 | Add compound indexes: `mail_logs {domains:1,first_seen:-1}` (+`{status:1,first_seen:-1}`/`{direction:1,first_seen:-1}`), `audit_logs {user.id:1,timestamp:-1}`, `domains {created_at:-1}` | Medium | both (code/data) | No (schema/code) |
| 5 | Replace unanchored `/q/i` regex search with text index / anchored prefix in `mail_logs` & `domains` list | Medium | repo | No (code) |
| 6 | Move skip-pagination → range/cursor pagination on `mail_logs`/`domains` | Medium | repo | No (code) |
| 7 | Provision 2–4 GB swap + lower `vm.swappiness` to 10 (safety valve) | Medium | both | No |
| 8 | Cache `du -sb /home/<user>` out-of-band instead of per-dashboard-render | Medium | repo | No (code) |
| 9 | nginx: raise `worker_connections` to 4096+, add upstream keepalive to :8080, trim `proxy_read_timeout` on `/` | Low–Med | both | Partial (reload) |
| 10 | Enable MariaDB slow query log (`long_query_time=1`); raise `innodb_io_capacity` 200→1000 for SSD | Low | both | No (config) |
| 11 | Metrics collector: read `/proc/stat` delta instead of forking `top -bn1` every 60 s; fix `getCPUPercent` two-sample bug | Low | repo | No (code) |

> All recommendations are advisory. This was a **read-only** audit — no service was restarted/reloaded, no config or data changed, no mutating command run.

---

## Appendix — commands run (representative)

```bash
# §1 baseline
uptime; nproc; free -m; swapon --show; cat /proc/sys/vm/swappiness; vmstat 1 3; top -bn1 | head -25
# §2/§8 process + IO
ps -eo pid,user,%cpu,%mem,rss,comm --sort=-rss | head -21
iostat -dx 1 2; mpstat 1 2; lsblk -o NAME,SIZE,TYPE,MOUNTPOINT,ROTA,SCHED
cat /sys/block/sda/queue/scheduler; df -h /
# §3 mongo (app user = serverpanel; serverStatus/currentOp = not authorized)
source /opt/serverpanel/.env
mongosh "$MONGO_URI" --quiet --eval 'db.version()'
mongosh "$MONGO_URI" --quiet --eval 'JSON.stringify(db.getProfilingStatus())'
mongosh "$MONGO_URI" --quiet --eval 'db.getCollectionNames()...getIndexes()...'   # 93 idx / 27 col
mongosh "$MONGO_URI" --quiet --eval 'var d=db.stats(); ...'
cat /etc/mongod.conf
# §4 mariadb (root socket auth)
mysql -N -e "SHOW VARIABLES LIKE 'innodb_buffer_pool_size'"   # 134217728
mysql -N -e "SHOW VARIABLES LIKE 'slow_query_log'"            # OFF
# §5 mail
postqueue -p
postconf -h default_process_limit smtpd_client_connection_count_limit default_destination_concurrency_limit ...
doveconf -h default_process_limit default_client_limit; ps -o args= -C spamd
# §6 cron/workers
crontab -l; cat /etc/cron.d/*; systemctl list-timers --all
# §7 nginx
grep -vE '^\s*#|^\s*$' /etc/nginx/nginx.conf; cat /etc/nginx/sites-enabled/serverpanel
grep -r 'gzip_types' /etc/nginx/   # none
# §8 panel
systemctl cat serverpanel; journalctl -u serverpanel -o short-iso
systemctl show serverpanel -p NRestarts -p MemoryCurrent -p CPUUsageNSec
curl -s -o /dev/null -w '%{http_code} %{time_total}s' http://127.0.0.1:8080/api/v1/health
# memory rollup + projections
ps -eo rss,comm | awk 'NR>1{a[$2]+=$1} END{for(c in a) if(a[c]>5000) printf "%-20s %8.1f MB\n",c,a[c]/1024}'
cat /sys/kernel/mm/transparent_hugepage/enabled   # always [madvise] never
```
