# 10 — Logging & Monitoring Audit (Server 1)

- **Host:** `89.116.34.207` — Ubuntu 24.04.4 LTS, kernel 6.8.0-124-generic
- **Panel:** BetaZen Server Panel (serverpanel), `APP_ENV=production`, `LOG_LEVEL=info`
- **Mongo DB:** `serverpanel`
- **Auditor:** Agent 15 (Logging & Monitoring)
- **Date:** 2026-06-29
- **Mode:** READ-ONLY assessment. No changes applied.

---

## Summary

Logging on Server 1 is **broadly healthy**. Every major log domain exists and is
populated, structured panel logging (zerolog JSON) is working, the mail-log
ingestion pipeline is alive and capturing all sources, and log rotation is
configured for essentially the entire stack via `logrotate` (system, mail, auth,
nginx, php-fpm, mariadb, fail2ban, serverpanel-install) **plus** `pm2-logrotate`
for worker apps.

Two real gaps stand out:

1. **`mongod.log` is not rotated by anything** — no `logrotate.d` entry and no
   `logRotate` directive in `mongod.conf`. With `logAppend: true` it grows
   unbounded. Low urgency here (~24 MB/day, 379 GB free) but a genuine coverage
   hole and the only meaningful disk-fill risk.
2. **There is effectively no proactive monitoring or alerting.** The panel
   samples CPU/mem/disk into Mongo every 60s for charts, but **nothing evaluates
   thresholds and nothing fires alerts** (email/slack disabled and, more
   importantly, no code path evaluates the thresholds at all). No external
   monitoring agent (node_exporter / netdata / prometheus) is installed.

A smaller gap: **password-change is not recorded in `audit_logs`**, and
`audit_logs` has **no retention/TTL** (currently fine at 239 docs).

---

## Coverage matrix

| Log domain | Present? | Populated? | Rotated? | Retention | Notes |
|---|---|---|---|---|---|
| **journald (system)** | Yes | Yes | Yes (built-in) | `SystemMaxUse=1G`, `MaxRetentionSec=7day` | **Persistent** — `/var/log/journal` exists (created Jun 28). Using 48 MB. |
| **rsyslog → /var/log/syslog** | Yes | Yes (fresh, ~3.8 MB, mtime now) | Yes | `weekly`, rotate 4, compress | rsyslog active; rules in `50-default.conf`, `20-ufw.conf`, `21-cloudinit.conf`. |
| **/var/log/auth.log (SSH auth)** | Yes | Yes (fresh, ~400 KB) | Yes | `weekly`, rotate 4, compress | Covered by `logrotate.d/rsyslog`. |
| **/var/log/mail.log** | Yes | Yes (fresh, last line 14:25) | Yes | `weekly`, rotate 4, compress | Postfix logging via `rsyslog.d/postfix.conf` (chroot socket). |
| **/var/log/mail.err** | No (absent) | n/a | n/a | n/a | Not created — no mail errors logged separately. Errors still land in mail.log/journald. Not a defect. |
| **Panel API (serverpanel)** | Yes | Yes (zerolog **JSON**, per-request) | Yes (journald) | 7 day / 1 GB | `level=info`, request logging on (`method/path/status/latency`). **0 panic/fatal/error** in last 2000 lines. Ingestor `tail -F` child (PID 90897) alive under serverpanel (PID 90874). |
| **serverpanel-install.log** | Yes | Yes | Yes | `weekly`, rotate 4, compress | `logrotate.d/serverpanel`. |
| **mongod.log** | Yes (~19.8 MB) | Yes | **NO** | none — unbounded | **GAP.** No logrotate entry, no `logRotate` in mongod.conf, `logAppend: true`. 0 W/E/F in last 2000 lines (only startup deprecation warnings). |
| **MariaDB error log** | Yes (journald) | Yes | Yes (journald) | 7 day | `log_error` empty → goes to journald. `logrotate.d/mariadb` exists for file-based logs (monthly, rotate 6, maxsize 500M) but no file logs are active. |
| **MariaDB slow query log** | Disabled | n/a | (mariadb policy) | — | `slow_query_log=OFF`, `long_query_time=10`. Acceptable default. |
| **audit_logs (Mongo)** | Yes (239) | Yes | n/a (DB) | **No TTL** | login.success/failed, create/delete email+dns+domains+apps+users+db+projects, transfers, backups. **No password-change action.** |
| **login_sessions (Mongo)** | Yes (32) | Yes | n/a | none | Written on successful password login (`auth_service.RecordLoginSession`, async geoip/UA). |
| **fail2ban.log** | Yes | Yes (~16 KB) | Yes | (logrotate.d/fail2ban) | 4 jails active: `sshd`, `postfix-sasl`, `dovecot`, `pure-ftpd`. |
| **dovecot / postfix auth** | Yes | Yes | Yes (mail.log/journald) | weekly/7day | Default auth logging; fail2ban consumes it. |
| **nginx access/error** | Yes | Yes | Yes | `daily`, rotate 14, compress | Per-vhost access+error logs all tracked in logrotate status. |
| **php8.2-fpm.log** | Yes | Yes | Yes | `weekly`, rotate 12, compress | `logrotate.d/php8.2-fpm`. |
| **pure-ftpd** | Yes (active) | Yes (journald/syslog) | Yes | weekly/7day | `pure-ftpd-common` logrotate present. |
| **Worker units (systemd)** | Yes | Yes | Yes (journald) | 7 day / 1 GB | `betazen-demo-{api,monitor,queue,scheduler,web,worker}` + `sp-app-{demo-cms,demo-crm,demo-erp,flask-sample,node-sample}` all **active/running**, none failed. |
| **PM2 worker logs** | Yes | Yes | Yes (**pm2-logrotate**) | retain 10, daily, compress | `pm2-logrotate` module active: `max_size 50M`, `retain 10`, `compress true`, daily rotate. |
| **metrics (Mongo)** | Yes (3507) | Yes (cpu/mem/disk, 1169 each, newest 14:39) | n/a | 7 day (DeleteMany sweep) | Sampled every 60s by `StartMetricsCollector`. Charts only. |
| **Alerting / threshold eval** | **NO** | — | — | — | **GAP.** No firing path (see below). |
| **External monitoring agent** | **NO** | — | — | — | No node_exporter/netdata/prometheus; no :9100/:19999/:9090 listeners. |

---

## Gaps (by severity)

### MEDIUM — No monitoring/alerting (only passive charts)
- The panel collects CPU/mem/disk every 60s into `metrics` (7-day retention) purely
  for historical charts (`monitoring_service.go: collectAndStoreMetrics`).
- Alert thresholds exist as a stored config (`GetAlertsConfig`/`UpdateAlertsConfig`,
  defaults cpu 90 / mem 90 / disk 85, email+slack disabled) but **no code evaluates
  samples against thresholds and no notification is ever sent** — confirmed by code
  search (`StartMetricsCollector` only samples+stores; there is no threshold-eval
  goroutine). On the box the alerts config doc does not even exist yet.
- No external agent (node_exporter/netdata) and no healthcheck/uptime probe.
- **Impact:** an operator only learns about high CPU/full disk/down service if they
  open the dashboard. No paging, no email, no Slack.

### LOW–MEDIUM — `mongod.log` never rotates
- No `logrotate.d` file references mongod; `mongod.conf` has no `logRotate`
  directive; `logAppend: true`. The only rotation mechanism (SIGUSR1 / `logRotate`
  admin command) is never triggered.
- Growth ~24 MB/day (19.8 MB in ~19.5 h uptime). Disk is 379 GB free (3% used), so
  the immediate risk is low, but the file will grow without bound across the box's
  lifetime — the **one genuine unrotated-log disk-fill exposure.**

### LOW — `audit_logs` has no retention (TTL) and omits password-change
- `audit_logs` has no TTL index — grows forever. Fine now (239 docs) but unbounded.
  (For contrast, `mail_logs` has a 90-day TTL on `created_at` and `metrics` is swept
  to 7 days; audit is the outlier.)
- Audit capture is route-driven (`middleware/audit.go` on mutating requests) and
  records login success/fail, creates, deletes, transfers — but **no
  password-change action** appears (0 matches). Self-service password changes are
  not auditable. Login is well covered (44 login-related entries).

---

## Fixes applied

**None.** Per the read-only demo-box rule, no changes were made. The only
candidate (a `logrotate.d/mongodb` file) touches system config and is not urgent
given 379 GB free, so it is left as a recommendation rather than applied to keep
the box pristine.

---

## Recommendations

**1. Rotate `mongod.log` (closes the only disk-fill gap).** Add
`/etc/logrotate.d/mongodb` and trigger Mongo's reopen on rotate:

```
/var/log/mongodb/mongod.log {
    daily
    rotate 14
    compress
    delaycompress
    missingok
    notifempty
    create 0600 mongodb mongodb
    sharedscripts
    postrotate
        /bin/kill -SIGUSR1 $(cat /var/run/mongodb/mongod.pid 2>/dev/null) 2>/dev/null || true
    endscript
}
```
(Reversible: delete the file. SIGUSR1 makes mongod reopen its log file cleanly.)

**2. Implement real alerting** — the highest-value gap. The data (`metrics`) and
the thresholds (`GetAlertsConfig`) already exist; what is missing is the evaluator.
Add a goroutine alongside `StartMetricsCollector` that, each tick, compares the
latest sample to the configured thresholds and, when exceeded, sends a
notification via the existing `notification_service` / `notifier_service` /
webhook plumbing (email + Slack toggles already in the config schema). Also wire
service-down detection (worker units / mongod / mariadb) into the same path.

**3. Add a lightweight external/independent watchdog** for the case where the
panel itself is down (it cannot alert on its own death). Minimum viable: an
external uptime/healthcheck ping against the panel HTTP endpoint. Optionally
`node_exporter` if a Prometheus/Grafana stack is desired later.

**4. Add a TTL to `audit_logs`** (e.g. a 1-year `expireAfterSeconds` on
`timestamp`, or longer for compliance) so it cannot grow unbounded, mirroring the
existing TTL pattern on `mail_logs`. Keep it long — audit trails are
security-relevant.

**5. Record password-change in the audit trail.** Add an explicit
`auditService.LogAction(... action="update.password" ...)` in the
change-password service path so credential changes are auditable (the route-based
middleware does not capture it distinctly today).

**6. (Optional) Enable MariaDB slow query log** if DB performance becomes a
concern (`slow_query_log=ON`); `logrotate.d/mariadb` already exists to rotate it.
