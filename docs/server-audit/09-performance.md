# 09 — Performance Audit (Server 1 / 89.116.34.207)

**Box:** Ubuntu 24.04 (kernel 6.8.0-124), KVM guest, AMD EPYC 9354P — 8 vCPU / 31 GiB RAM / 387 GB disk
**Panel:** v3.1.112 (`server` Go binary :8080) · MongoDB 8.0.26 · MariaDB 10.11.14 · Postfix/Dovecot · PowerDNS · nginx
**Audit type:** READ-ONLY measurement on a near-idle DEMO box. No changes applied. All numbers captured 2026-06-29 ~14:37–14:41 UTC, system uptime 19.5 h.

---

## Summary

This box is **massively over-provisioned for its current workload and effectively idle.** Across CPU, memory, disk I/O, database, and mail subsystems there is **no resource contention of any kind**: load average 0.04/0.11/0.11 on 8 cores, 29 GiB RAM available, disk at 3% with sub-millisecond awaits, Mongo and MariaDB datasets that fit entirely in RAM with ~100% cache hit rates, and an empty mail queue. The panel binary and all background workers are quiescent.

The only items worth attention are **forward-looking hygiene**, not present-day problems: (1) the `metrics` collection grows unbounded (~4,300 docs/day, no TTL); (2) MariaDB and nginx still run stock Ubuntu defaults that would matter only at far higher load; (3) zero swap is configured (acceptable here given the huge headroom, but a single guardrail is missing). Nothing is currently resource-constrained.

**Overall verdict: Healthy / heavily under-utilized. Capacity headroom is very large.**

---

## Metrics

| Subsystem | Metric | Value | Assessment |
|---|---|---|---|
| **CPU** | Model / topology | AMD EPYC 9354P, 8 vCPU (KVM, 1 thread/core, 2.0 GHz) | Modern, ample |
| CPU | Load avg 1/5/15 | 0.04 / 0.11 / 0.11 | Idle (~1.4% of 8-core capacity) |
| CPU | %idle (mpstat avg) | 99.38% | Idle |
| CPU | %steal (mpstat / iostat) | 0.42% avg (peaks 0.75%) | Negligible noisy-neighbor; OK |
| CPU | Context switches (vmstat) | ~470/s | Trivial |
| CPU | Top consumer | mongod (1.3% accumulated; 3% during a write tick) | Periodic metrics writes |
| **Memory** | Total / used / avail | 31 GiB / 1.5 GiB used / **29 GiB available** | <5% used |
| Memory | buff/cache | 7.0 GiB | Healthy page cache |
| Memory | Swap | **0 B configured** (swappiness 60) | See risks |
| Memory | Committed_AS vs limit | 3.2 GiB / 15.7 GiB | No memory pressure |
| Memory | Top RSS | mongod 216 MB · spamd 144+133+133 MB · mariadbd 122 MB · node demo apps ~50–69 MB ea · `server` panel **32 MB** | All small |
| **Disk** | Filesystem / usage | ext4 on /dev/sda1, **8 GB / 387 GB = 3%** | Vast free space |
| Disk | Inodes | 280 k / 52 M = 1% | Fine |
| Disk | iostat %util / await | ~0.17% util, r_await 0.37 ms, w_await 0.71 ms | No I/O contention |
| Disk | %iowait | 0.00–0.12% | None |
| Disk | mount opts | `discard,commit=30` (TRIM enabled) | Good for SSD |
| **MongoDB** | Version / uptime | 8.0.26 / 19.5 h | Current |
| MongoDB | Connections | 11 current / 25,589 available (10,175 created) | Far below limit |
| MongoDB | opcounters | 4,128 ins · 2,100 qry · 816 upd · 1,308 del · 37,445 cmd | Light |
| MongoDB | WT cache configured | **15.17 GB** (default ~50% RAM) | Oversized vs data |
| MongoDB | WT cache used | **2.9 MB (0.0% of cfg)** | Whole DB in cache |
| MongoDB | Page faults / dirty | **0** / 0 MB | Zero disk pressure |
| MongoDB | Cache hit | 283,570 pages requested vs **30 read from disk** (~100%) | Effectively all-RAM |
| MongoDB | globalLock queue | 0 readers / 0 writers | No lock contention |
| MongoDB | Profiler | **OFF** (level 0, slowms 100) | Default |
| MongoDB | currentOp long ops | 0 (the 1 reported was the probe itself) | None |
| MongoDB | DB size | 4,009 objs / 29 colls · **data 0.47 MB · index 1.80 MB · total 2.62 MB** | Index > data (over-indexed) |
| MongoDB | `metrics` collection | **3,510 docs, 317 KB, NO TTL**, ~20 s write cadence (3 docs/min ≈ 4,326/day) | Unbounded growth (see risks) |
| MongoDB | explain audit_logs | IXSCAN→LIMIT, 50 keys/50 docs examined, **0 ms** | Index-backed, optimal |
| MongoDB | explain mail_logs | FETCH, 8/8 examined, 4 ms (only 8 docs) | Fine; 8 indexes for 8 docs |
| **MariaDB** | Version / uptime | 10.11.14 / 19.6 h | Current |
| MariaDB | Buffer pool size | 128 MB (8,112 pages, 7,638 free) | Stock default; >30× the data |
| MariaDB | Data size (all DBs) | mysql 3.4 MB · roundcube 0.5 MB · demotwo_appdb 0.19 MB | Tiny |
| MariaDB | BP hit ratio | **98.43%** (10,817 req / 170 reads) | ~100% effective (170 = cold reads) |
| MariaDB | Slow_queries | **0** (slow log OFF, long_query_time 10 s) | None measured |
| MariaDB | Threads_connected / Max_used | 1 / **1** | Idle |
| MariaDB | Created_tmp_disk_tables | 390 of 2,152 tmp (18% on-disk) | Minor; roundcube/info_schema |
| MariaDB | Aborted_connects | 2 | Negligible |
| **Mail** | postqueue depth | **Empty** | No backlog |
| Mail | qmgr limits | default_process_limit 100 · dest concurrency 20 · active_limit 20,000 | Healthy defaults |
| **Workers** | PM2 processes | only `pm2-logrotate` (4 restarts, 0.4% cpu, 67 MB) | Demo apps are systemd, not PM2 |
| Workers | sp-app-* / betazen-* units | 11 units, MemoryCurrent 2.8–46 MB each, all `online` | Light; no crash-looping |
| Workers | Panel `server` | 16 threads, **0.00% CPU over 2 s**, 0.365% avg since boot | No tight loop |
| Workers | mongod avg CPU since boot | 1.345% (top consumer) | Driven by 20 s metrics writes |
| **nginx** | worker_processes | `auto` (≈8, matches cores) | Correct |
| nginx | worker_connections | **768** (Ubuntu default) | Low for scale (see recs) |
| nginx | gzip | `on` but **no `gzip_types`** set | Only text/html compresses |
| nginx | sendfile / tcp_nopush | on / on | Good |
| nginx | http/2 module | compiled in | OK |
| **Startup** | Total boot | 5.35 s kernel + 24.95 s userspace = **30.30 s** | Cloud-image overhead |
| Startup | graphical.target | 12.08 s | — |
| Startup | Blame top | cloud-final 12.9 s · cloud-init 4.7 s · cloud-init-local 1.9 s · man-db 1.7 s | All OS, not panel |
| Startup | Service start times | mariadb 531 ms · postfix 700 ms · (serverpanel not in top 15) | Fast |

---

## Bottlenecks / Risks (prioritized)

1. **`metrics` collection grows unbounded — no TTL index (LOW now, MEDIUM long-term).**
   3,510 docs and counting, written every ~20 s (≈4,326 docs/day ≈ 1.6 M/year). It is indexed on `timestamp` so reads stay cheap, and each doc is tiny (~90 B). But with no expiry it will accumulate forever, slowly inflating storage and the periodic write working set. This is the single clearest "will degrade eventually" item. **Impact today: none. Impact at 1–2 years: modest storage + write churn.**

2. **Zero swap configured (LOW).**
   With 29 GiB available this is not a present risk, but there is no safety valve: an unexpected memory spike (e.g. a runaway demo app or a large Mongo aggregation) would trigger the OOM killer rather than degrade gracefully. Given 31 GiB RAM, even a small (2–4 GiB) swap file would add a cheap guardrail.

3. **MariaDB & nginx on stock Ubuntu defaults (LOW — only matters under future load).**
   - `innodb_buffer_pool_size` 128 MB and nginx `worker_connections` 768 are fine for the current footprint but are the first tuning gaps you'd hit if this box ever served real production traffic.
   - nginx `gzip on` without `gzip_types` means JSON/JS/CSS responses are sent uncompressed — wasted bandwidth on the SPA bundles and API responses.
   - MariaDB slow query log is OFF, so future slow queries would go unnoticed.

4. **Over-indexing of tiny collections (INFORMATIONAL).**
   Total index size (1.80 MB) exceeds data size (0.47 MB); `mail_logs` carries 8 indexes for 8 documents, `metrics`/`audit_logs` etc. similarly. Harmless at this scale (write amplification is negligible), but worth noting the schema was designed for volume that doesn't exist yet — which is correct for a panel meant to scale.

5. **Minor: 18% of tmp tables spill to disk (INFORMATIONAL).**
   390 of 2,152 (mostly roundcube / information_schema queries). `tmp_table_size`/`max_heap_table_size` are 16 MB. Immaterial at this query volume.

---

## Optimization Recommendations (with expected impact)

| # | Recommendation | Expected impact |
|---|---|---|
| 1 | **Add a TTL index on `metrics.timestamp`** (e.g. `expireAfterSeconds` = 30–90 days), or implement a rollup/retention job in the metrics collector. | Caps unbounded growth; keeps the hot write set small indefinitely. High value for near-zero cost. The audit_logs/mail_logs collections should be reviewed for the same retention question. |
| 2 | **Add a small swap file (2–4 GiB)** and lower `vm.swappiness` to ~10. | Converts a potential hard OOM-kill into graceful degradation. Cheap insurance; no downside on a box with 387 GB free. |
| 3 | **Set nginx `gzip_types`** (application/json, application/javascript, text/css, text/xml, image/svg+xml) and consider `gzip_comp_level 5`. | Smaller SPA bundle + API payloads → faster first paint and lower egress. Visible on real client traffic; nil cost. |
| 4 | **When real load arrives:** raise `innodb_buffer_pool_size` to ~50–60% of RAM, bump nginx `worker_connections` to 4096+ and `worker_rlimit_nofile`, and add a `keepalive` upstream block to the panel proxy. | Removes the first scale ceilings. No benefit at current load — defer until traffic justifies it. |
| 5 | **Turn on MariaDB slow query log** with `long_query_time = 1` (and optionally `log_queries_not_using_indexes`). | Observability only; surfaces slow queries before they bite. Negligible overhead at this volume. |
| 6 | **Optional:** prune indexes that aren't query-backed on the highest-write collections (`metrics`, `mail_logs`) once access patterns are confirmed. | Slightly less write amplification + storage. Minor; verify with usage data first. |

*All recommendations are advisory — no changes were applied (DEMO box, read-only mandate).*

---

## Capacity Verdict

**This box is running at roughly 1–2% of its capacity and can absorb a very large amount of additional load before any tuning is required.**

- **CPU:** 8 EPYC cores at 99.4% idle. The current workload (panel + Mongo + MariaDB + mail + ~17 demo processes) consumes well under one core's worth of time. Headroom: effectively the entire 8 cores.
- **Memory:** 1.5 GiB used of 31 GiB; 29 GiB available. Mongo's WT cache alone is configured for 15 GiB and uses 3 MB. You could run 10–20× the current tenant/app count before memory becomes interesting.
- **Disk:** 8 GB of 387 GB used, sub-ms latency, ~0% utilization. Years of runway on space and orders of magnitude on IOPS.
- **Databases:** Both datasets fit entirely in RAM with ~100% cache hit rates and zero page faults; this scales comfortably into the multi-GB range before disk reads matter.

**First bottleneck under growth — in order:**
1. **`metrics` collection storage / write churn** (the only component on an unbounded growth curve) — mitigated entirely by recommendation #1.
2. **MariaDB `innodb_buffer_pool_size` (128 MB)** — the first hard tuning ceiling once SQL-backed app data exceeds ~100 MB working set.
3. **nginx `worker_connections` (768)** — the first concurrency ceiling under real HTTP traffic.

None of these is active today. The practical limit is operational (retention hygiene), not hardware.
