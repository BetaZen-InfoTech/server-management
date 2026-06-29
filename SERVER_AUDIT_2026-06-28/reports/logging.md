# Agent 15 — Logging & Monitoring Audit

**Date:** 2026-06-28
**Scope:** Both VPS clones — Server 1 = `89.116.34.207` (migration SOURCE, host `srv1785162`), Server 2 = `195.35.7.64` (migration DEST, host `srv1789639`).
**Deployed code:** local git repo `c:/Users/Administrator/Downloads/Project/server-management` (v3.1.109, rev 466b52e).
**Mode:** READ-ONLY. No service was restarted/reloaded, no config edited, no data created/deleted.

---

## 0. Executive summary

Logging is broadly healthy and the two servers are near-identical. Core OS logging (journald persistent, rsyslog active), mail logging (postfix/dovecot/opendkim/spamd all writing to `/var/log/mail.log`), the panel application log (zerolog JSON via journald), log rotation (logrotate.timer active, all log classes covered), and the in-app `metrics` collector (60s cadence) are all working on both hosts.

The single most important finding is architectural, not a runtime fault: **the panel's `mail_logs` ingestion pipeline is a `tail -F /var/log/mail.log` log follower** (a syslog-file tail, not a milter / rsyslog pipe / Postfix logfile API). It is running correctly on both servers (`tail -n 0 -F` child process present, header_checks installed), but the collection is empty on both (`mail_logs: 0`) because no real mail with a Postfix queue-id has flowed yet — only localhost connect/disconnect probe noise. Because the tail-based design parses *Postfix's own syslog stream*, it now **does** capture third-party SMTP, webmail, local sendmail, and inbound — i.e. the historical "third-party SMTP not logged" issue is fixed in this code version *provided Postfix logs to syslog*, which it does here (`maillog_file` empty). Caveats below.

Secondary findings: `audit_logs` captures only authentication events plus a narrow allow-list of mutating routes (most reads and many actions are NOT audited); `audit_logs` and `metrics` have **no TTL index** (audit grows unbounded; metrics relies on an app-side 7-day `DeleteMany`); fail2ban runs **only the `sshd` jail** — there are no postfix/dovecot/sasl jails despite the mail stack being exposed; and `/var/log/mail.err` / `mail.warn` do not exist yet (expected — `notempty`/no entries).

Health: **warnings** (no critical/broken logging; several gaps and one design caveat worth recording).

---

## 1. System logs (journald / rsyslog / syslog)

### journald — persistent, bounded, identical on both
`/etc/systemd/journald.conf` (both servers):
```
[Journal]
SystemMaxUse=1G
SystemMaxFileSize=100M
MaxRetentionSec=7day
MaxFileSec=1day
Compress=yes
```
Evidence (both):
```
$ journalctl --disk-usage           -> Archived and active journals take up 8.0M in the file system.
$ ls -ld /var/log/journal           -> drwxr-sr-x+ ... /var/log/journal   (PERSISTENT journal dir exists)
  File path: /var/log/journal/<machine-id>/system.journal
```
Persistent storage is enabled (journal dir on disk, not volatile-only), capped at 1G / 7 days. Good.

### rsyslog — active & enabled, classic routing
```
$ systemctl is-active rsyslog   -> active        (both)
$ systemctl is-enabled rsyslog  -> enabled       (both)
rsyslogd 8.2312.0
```
`/etc/rsyslog.d/` (both): `20-ufw.conf`, `21-cloudinit.conf`, `50-default.conf`, `postfix.conf`.

`50-default.conf` routing (both identical):
```
auth,authpriv.*          /var/log/auth.log
*.*;auth,authpriv.none   -/var/log/syslog
kern.*                   -/var/log/kern.log
mail.*                   -/var/log/mail.log
mail.err                 /var/log/mail.err
*.emerg                  :omusrmsg:*
```
`/etc/rsyslog.d/postfix.conf` (both): adds the chroot socket `$AddUnixListenSocket /var/spool/postfix/dev/log` so Postfix (which runs chrooted) keeps logging across an rsyslog restart. This is the standard Debian/Ubuntu file and is the reason mail logging is reliable.

### Core log files present & live (both servers)
| File | S1 size | S2 size | Notes |
|---|---|---|---|
| /var/log/syslog | 146289 | 144398 | live (mtime 19:47/19:48) |
| /var/log/auth.log | 57557 | 53690 | live |
| /var/log/mail.log | 4670 | 4817 | live |
| /var/log/mail.err | **MISSING** | **MISSING** | expected — no mail.err-level events yet |
| /var/log/mail.warn | **MISSING** | **MISSING** | no rsyslog rule routes mail.warn anyway |
| /var/log/kern.log | 154617 | 154581 | live |
| /var/log/nginx/access.log | 204 | 200 | live |
| /var/log/nginx/error.log | 76 | 76 | live |
| /var/log/mongodb/mongod.log | 1000462 | 828182 | live (no E/F entries — see §7) |
| /var/log/fail2ban.log | 6537 | 6537 | live |

`/var/log/mail.err` is absent on both. This is **expected/correct**: rsyslog only routes `mail.err` there (no `mail.warn` target exists), the file is created lazily on first error, and there have been no error-priority mail events. The panel's log viewer does not reference `mail.err`/`mail.warn` (see `log_service.go` `logSources`), so its absence does not break the panel.

---

## 2. Mail logs (postfix / dovecot / opendkim / spamassassin)

`/var/log/mail.log` is the single shared mail log; all mail components write to it via the syslog `mail` facility. Program tags observed in the current file (S1 / S2):
```
postfix/smtpd   13 / 13
spamd           11 / 11
dovecot          7 /  7
postfix/postfix  4 /  5
postfix/master   4 /  4
opendkim         3 /  3
```
So **Postfix, Dovecot, OpenDKIM, and SpamAssassin (spamd) are all confirmed logging**. The current content is connection-level noise (localhost probes — `connect`/`disconnect`/`STARTTLS`/`imap-login` aborts), almost certainly the audit-probe traffic itself (`EHLO audit.local`). No message ever entered the Postfix queue (no queue-id lines), which is why the panel `mail_logs` collection is empty (see §3).

Postfix logging destination (S1): `postconf -h maillog_file` -> **empty** = classic syslog routing (Postfix does NOT write its own file; it goes through rsyslog to `/var/log/mail.log`). This matters for §3 — the ingestor depends on it.

---

## 3. CRITICAL — complete `mail_logs` ingestion pipeline

### Mechanism (from code): syslog **file tail**, not a milter / rsyslog pipe / Postfix API
Source: `backend/internal/services/mail_log_service.go`.

The capture mechanism is **`tail -n 0 -F /var/log/mail.log`** spawned as a child of the panel process, parsed line-by-line in Go, correlated per Postfix queue-id in memory, and upserted to Mongo `mail_logs`. It is NOT a Postfix milter, NOT an rsyslog `omprog`/pipe, NOT a libmilter hook, NOT webmail-only. It is a **passive follower of Postfix's own syslog output**, which the file's own header documents as the fix for the pre-v3.1.108 "only Dovecot Sieve webhook → outbound/3rd-party SMTP never logged" problem.

Precise end-to-end flow (verified live):

```
Postfix (smtpd/cleanup/qmgr/smtp/lmtp/local/pickup/...) logs via syslog "mail" facility
   │  (maillog_file empty -> classic syslog, confirmed S1)
   ▼
rsyslog  (mail.* -> -/var/log/mail.log ; 50-default.conf)
   │     chroot socket kept alive by rsyslog.d/postfix.conf
   ▼
/var/log/mail.log
   │
   ▼
serverpanel:  tail -n 0 -F /var/log/mail.log     <-- child PID 51274, parent 51248 (the panel), runs as root
   │   StartIngestor() -> tailLoop() : exec.CommandContext("tail","-n","0","-F", "/var/log/mail.log")
   │   restarts tail on exit (rotation-safe); starts at EOF on panel restart
   ▼
parseLine()  (mail_log_service.go:188)
   - mlLineRe splits "<ts> <host> <prog>[pid]: <msg>"; SKIPS any non-"postfix" program
     (Dovecot/opendkim/spamd lines are intentionally ignored here — message-centric)
   - mlQidRe pulls the Postfix queue-id (6+ alnum) or NOQUEUE
   - per-subprogram extraction: smtpd(client/sasl) | pickup | cleanup(Subject/Content-Type/message-id)
     | qmgr(from/size/nrcpt, "removed") | smtp/lmtp/local/virtual/pipe/error(per-recipient status)
   ▼
in-memory map[queue_id]*partialEntry   (mu-guarded; cap mailLogMaxPartial=8000 w/ oldest-evict)
   ▼
finalize on qmgr "removed"  OR  flusher() every 30s for items idle > 3min (deferred/queued)
   ▼
upsert -> Mongo serverpanel.mail_logs  keyed on log_key="<queue_id>:<first_seen_unix>"  (idempotent)
```

Subject + Content-Type enrichment: `EnsureHeaderChecks()` installs `/etc/postfix/header_checks_betazen` (a **regexp:** map, deliberately NOT pcre, with a `postmap -q` pre-flight + rollback so it can never temp-fail mail) containing `/^Subject:/ WARN` and `/^Content-Type:/ WARN`. Postfix's `cleanup` then logs the Subject/Content-Type as a `warning: header ...` line that `parseLine` scrapes (`mlHeaderWarnRe`).

### Runtime verification (both servers)
serverpanel boot log (S1; S2 identical 8 min later):
```
{"level":"info","path":"/var/log/mail.log","message":"mail-log: ingestor started (capturing ALL mail, every source)"}
{"level":"info","message":"mail-log: Postfix header_checks installed (Subject + Content-Type logging)"}
```
Tail child process is alive (S1):
```
$ ps -ef | grep "tail .*-F.*mail.log"
root  51274  51248  ... tail -n 0 -F /var/log/mail.log     # 51248 = /opt/serverpanel/bin/server (root)
```
header_checks wired (both):
```
$ postconf -h header_checks          -> regexp:/etc/postfix/header_checks_betazen
$ cat /etc/postfix/header_checks_betazen
  /^Subject:/ WARN
  /^Content-Type:/ WARN
```
`mail_logs` indexes (both): unique `log_key`, plus `first_seen/-1`, `status`, `direction`, `source`, `domains`, and a **TTL index `created_at` expireAfterSeconds=7776000 (90 days)**.

### Current state of the collection
```
mail_logs: 0   (both servers)
```
**This is consistent, not a bug.** The mail in `/var/log/mail.log` is all connection-level noise with no Postfix queue-id (probes that `EHLO`+`QUIT` or abort STARTTLS). `parseLine` returns early for any line without a queue-id, so there is nothing to record. The ingestor will populate `mail_logs` the moment a real message gets a queue-id (queued, sent, deferred, bounced, or a NOQUEUE pre-queue reject). Demo data state confirms `domains/mailboxes = 0`, so no mailbox exists to send/receive yet.

### Caveats / risks of the tail-based design (record for completeness)
1. **Hard dependency on syslog file routing.** The ingestor parses `/var/log/mail.log`. If Postfix were ever reconfigured to log to its own file (`maillog_file = /var/...`) — which modern Postfix supports and some hardening guides recommend — mail.log would go silent and the panel would capture nothing, with no error surfaced. Currently `maillog_file` is empty, so this is fine, but it is an undocumented coupling.
2. **Start-at-EOF gap.** On panel restart the tail starts with `-n 0` (end of file). Any messages logged during the restart window are never captured (the code comments accept this as "negligible"). There is no inode/offset checkpoint and no backfill.
3. **Non-Postfix components are deliberately dropped.** Dovecot logins, OpenDKIM signing/verify results, and SpamAssassin scores are present in `/var/log/mail.log` but `parseLine` skips every non-`postfix` program. So the `mail_logs` collection will never contain Dovecot auth failures, DKIM pass/fail, or spam scores — those remain visible only in the raw mail.log (which the panel's generic Log Viewer can show, but they are not structured).
4. **Timestamp trust.** `parseMailTS` parses the rsyslog RFC3339 stamp; entries here use high-precision ISO (`2026-06-28T19:44:32.558732+00:00`), which it handles. Good.

---

## 4. Panel application logs (serverpanel)

- Unit: `serverpanel.service`, `Type=simple`, `Restart=always`, **active & enabled** on both. ExecStart `/opt/serverpanel/bin/server`, runs as **root**.
- Logging: zerolog → stdout → captured by systemd journald (`journalctl -u serverpanel`). In production (`APP_ENV=production`) the human `ConsoleWriter` is disabled, so output is **structured JSON** (confirmed). Env on both: `APP_ENV=production`, `LOG_LEVEL=info`, `SERVER_IP` set per host, `MAIL_HOSTNAME=mail.<IP>`.
- Code: `pkg/logger/logger.go` parses LOG_LEVEL (note: config default is `debug` if unset — but `.env` explicitly sets `info`, so effective level is **info**). `middleware/logger.go` logs every request as `{"message":"request", method, path, status, latency, ip}`.
- Boot sequence captured cleanly (S1), including: Mongo connect, "project deploy pool started" (workers:4, queue_buffer:256 — see §6), HTTP server start, whois daily refresh, nginx self-heal OK, mail-log ingestor start, header_checks install.
- **Zero warn/error/fatal entries** in the serverpanel journal on either host:
  ```
  $ journalctl -u serverpanel | grep '"level":"(warn|error|fatal)"'  -> (no matches, exit 1)
  ```
- **Gap:** journald is the *only* sink for app logs — there is no `/var/log/serverpanel*.log` for the running service (only an install-time `/var/log/serverpanel-install.log`, see §8) and no Mongo app-log collection (the only `*log*` collections are `mail_logs`, `audit_logs`, plus `login_sessions`). App-log retention is therefore bounded by journald's 7-day / 1G cap; there is no long-term application-error archive.
- **Observation:** request-log `ip` is empty for proxied requests (`"ip":""`) and only populated when the panel is hit on its bare IP directly. nginx is the trusted proxy and the panel trusts `X-Forwarded-For` from loopback, but the local probes show empty IP — low-impact, but request audit/correlation by client IP is unreliable for proxied traffic.

---

## 5. audit_logs collection + AuditLogger middleware

### Two write paths
1. **`middleware/audit.go` `AuditLogger`** (wired on WHM + cPanel route groups, e.g. `whm_routes.go:61`). It audits **only mutating methods** (POST/PUT/PATCH/DELETE; GET/HEAD/OPTIONS skipped), **only when a user is authenticated**, and **only when `parseRoute` recognizes the route** (`action==""` → skipped). `parseRoute` maps method+path to `verb.resource` and special action suffixes (suspend, restart, deploy, install, renew, ...). Anything it does not map is silently not audited.
2. **`handlers/auth_handler.go`** explicit `LogAction` calls for auth events: `login.success`, `login.failed`, `otp.request`, `otp.verify.success/failed`, `otp.handoff.approved`, `otp.complete.success/failed`, `otp.cancel`.

`AuditService.LogAction` inserts into `audit_logs` (fire-and-forget — error ignored: `_, _ = ... InsertOne`).

### Current state (both servers)
```
audit_logs: 3
```
All three are `login.success` for `admin@betazeninfotech.com` (the audit probe logins):
```
2026-06-28T19:44:13Z  login.success  admin@betazeninfotech.com  success  "User logged in"
2026-06-28T19:39:37Z  login.success  admin@betazeninfotech.com  success  "User logged in"
2026-06-28T19:32:41Z  login.success  admin@betazeninfotech.com  success  "User logged in"
```
Indexes (both): `timestamp/-1`, `user.id`, `action`, `resource_type`. **No TTL** — `audit_logs` grows unbounded (acceptable for an audit trail, but worth a documented retention/archival policy since there is no rotation).

### Gaps
- **Reads are never audited** (by design — GET skipped). Sensitive read operations (viewing secrets, downloading exports, listing tokens) leave no audit trail.
- **Coverage depends on `parseRoute`'s allow-list.** Any mutating route whose first path segment / action suffix isn't recognized produces `action==""` and is dropped with no record and no warning. This is a maintenance hazard: new endpoints are silently un-audited until added to `parseRoute`.
- The `login.failed` path exists in code but the demo only logged successes (no failed logins occurred).

---

## 6. Worker logs

- The panel's only background worker pool is the **project deploy pool** (`{"workers":4,"queue_buffer":256,"message":"project deploy pool started"}`). Its logs go to the same zerolog/journald sink as the rest of the app — no separate worker log file or collection.
- Other periodic goroutines (all journald-logged): metrics collector (60s, §7), mail-log ingestor + 30s flusher (§3), daily whois refresh (logged "no apex domains to refresh"), and a 30-min sweep for expired API tokens / guest links (`main.go:346`).
- No dedicated worker/queue log; if the deploy pool ever fails a job, it surfaces only as journald entries within the 7-day window.

---

## 7. Error logs

- **serverpanel:** no warn/error/fatal entries on either host (§4).
- **MongoDB** (`/var/log/mongodb/mongod.log`): S2 2479 lines, **0 entries at severity E (error) or F (fatal)** (`grep -c '"s":"E"|"s":"F"' -> 0`). Healthy.
- **nginx error.log:** present and tiny (76 bytes both) — essentially no errors.
- **mail.err:** absent (no error-priority mail events — §1).
- The panel's generic Log Viewer (`log_service.go`) exposes `nginx-error`, `system`, `auth`, `mail`, `mongodb`, and `app` (journalctl -u serverpanel) to the platform owner; tenant-scoped users only get their own per-domain nginx logs. It is read-only `tail`/`journalctl` with a journal fallback when a file is missing/empty. No error-log gaps for the operator.

---

## 8. Log rotation (logrotate)

- `logrotate.timer` **active**; next run `Mon 2026-06-29 00:00:00 UTC` on both. Also a `/etc/cron.daily/logrotate` fallback present.
- `/etc/logrotate.d/` is fully populated on both (identical set): `rsyslog`, `nginx`, `mariadb`, `php8.2-fpm`, `fail2ban`, `roundcube-core`, `pure-ftpd-common`, `serverpanel`, plus OS defaults.
- **rsyslog** rotation (covers syslog, **mail.log**, kern.log, auth.log, user.log, cron.log): weekly, rotate 4, compress, delaycompress, `postrotate /usr/lib/rsyslog/rsyslog-rotate`. So mail.log/syslog/auth.log are retained ~4 weeks.
- **nginx**: daily, rotate 14, compress, postrotate `invoke-rc.d nginx rotate`.
- **serverpanel** logrotate (both) rotates **only the install log**, not the running service (the service logs to journald, so nothing on disk to rotate):
  ```
  /var/log/serverpanel-install.log { weekly; rotate 4; missingok; notifempty; compress; delaycompress; create 0644 root root }
  ```
- logrotate state confirms recent rotations ran today: `/var/log/syslog`, `/var/log/auth.log`, `/var/log/mail.log` all stamped `2026-6-28-19:0:0`.

Rotation is healthy and covers every on-disk log class. No unrotated/growing-forever on-disk log was found (the only unbounded growth is in Mongo `audit_logs` — §5).

---

## 9. Authentication logs & fail2ban

- `/var/log/auth.log` active (sshd/sudo/session). Routed by `auth,authpriv.* -> /var/log/auth.log`.
- **fail2ban active & enabled** on both. `fail2ban-client status` shows **`Number of jail: 1` → only `sshd`**, using `backend = systemd` (reads sshd journal; `_SYSTEMD_UNIT=sshd.service`). 0 currently/total banned.
- Jail config: `/etc/fail2ban/jail.d/defaults-debian.conf` (sshd enabled, `banaction = nftables`) + `/etc/fail2ban/jail.d/serverpanel.conf` (the panel-managed file) which **only enables `[sshd]`** (bantime 1h, findtime 10m, maxretry 5). There is **no `jail.local`**.
- **Finding — no mail/auth jails despite an exposed mail stack.** `/var/log/fail2ban.log` shows the audit/diagnostic probe asking fail2ban for postfix / postfix-sasl / dovecot / sasl jail status and getting `UnknownJailException` for all four on both servers:
  ```
  ERROR Command ['status','postfix']      ... UnknownJailException('postfix')
  ERROR Command ['status','postfix-sasl'] ... UnknownJailException('postfix-sasl')
  ERROR Command ['status','dovecot']      ... UnknownJailException('dovecot')
  ERROR Command ['status','sasl']         ... UnknownJailException('sasl')
  ```
  Postfix submission (587/465) and Dovecot IMAP/POP auth are reachable but **not protected by fail2ban** — SMTP-AUTH / IMAP brute-force is unthrottled. (SSH brute-force IS protected.) This is consistent across both servers (no drift).

---

## 10. Monitoring — the `metrics` collection

- Writer: `MonitoringService.StartMetricsCollector(ctx, 60*time.Second)` (`main.go:359`). A goroutine collects immediately on start, then every **60s** writes three docs per tick — `cpu` (top), `memory` (free -b → percent + used/total), `disk` (df -B1 / → percent + used/total) — each `{metric, value, timestamp, ...}` into `metrics`.
- Cadence verified live (S2 newest cpu samples, exactly 60s apart):
  ```
  19:48:43 ... 19:47:43 ... 19:46:43 ... 19:45:43 ...  (Δ = 60s)
  ```
- Counts grew during the audit (started at 54 per the brief): **S1 = 108, S2 = 87** (3 docs/min). Distinct metrics each = `cpu`, `memory`, `disk` with equal counts (S1 36 each over 19:12→19:47; S2 29 each over 19:20→19:48).
- Retention: **app-side** — every tick runs `DeleteMany({timestamp < now-7d})`. There is **no TTL index** on `metrics`; the only index is `timestamp/-1` (matches both the historical read query and the retention delete; an older bogus `collected_at` index was fixed per the code comment). If the panel is stopped, the 7-day cleanup also stops (no DB-side TTL safety net) — minor.
- Read API: `HistoricalMetrics` (1h/6h/24h/7d/30d windows, limit 500), `LiveMetrics`, `ServiceStatus`/`ServiceStatusSummary` (live `systemctl is-active`/version probes — not persisted). Alert thresholds live in `server_config` (`GetAlertsConfig` default cpu/mem 90, disk 85, email/slack disabled) but **there is no alerting loop** that reads metrics and fires on threshold — thresholds are stored config only, no evaluator was found. So "monitoring" = historical charting + on-demand status; **no proactive alerting is active**.

---

## 11. Drift between Server 1 and Server 2

Essentially **none**. Both run Ubuntu 24.04.4, identical journald/rsyslog/logrotate/fail2ban configs, identical serverpanel env (LOG_LEVEL=info, APP_ENV=production), identical header_checks, identical index sets, identical empty `mail_logs` and 3-row `audit_logs`. Differences are only the expected per-host values (hostname, IP, `MAIL_HOSTNAME`, uptime-driven metrics counts: S1 108 vs S2 87 because S1 booted ~8 min earlier; mongod.log size differs trivially). No logging/monitoring configuration drift was detected.

---

## 12. Missing / weak logging — consolidated

| # | Item | Severity | Server |
|---|---|---|---|
| 1 | fail2ban runs only `sshd`; no postfix/postfix-sasl/dovecot/sasl jails → SMTP/IMAP auth brute-force unthrottled (confirmed by UnknownJailException in fail2ban.log) | high | both |
| 2 | `audit_logs` has no TTL/rotation (unbounded growth) and audits only mutating, route-recognized actions + auth events — reads and unmapped mutations are never recorded | medium | both |
| 3 | mail-log ingestion is a `tail -F /var/log/mail.log` follower → hard, undocumented dependency on Postfix syslog routing (`maillog_file` empty); start-at-EOF restart gap; no inode checkpoint/backfill | medium | both |
| 4 | Dovecot auth, OpenDKIM, SpamAssassin lines are intentionally dropped by the ingestor → never structured into `mail_logs` (only raw in mail.log) | low | both |
| 5 | `metrics` retention is app-side only (no DB TTL); cleanup stops if panel stops | low | both |
| 6 | Panel app logs live only in journald (7-day/1G cap); no on-disk app log, no app-log Mongo collection, no long-term error archive | low | both |
| 7 | Request-log/audit client IP empty for proxied traffic (`"ip":""`) | low | both |
| 8 | No proactive alerting loop — alert thresholds stored in `server_config` but nothing evaluates metrics against them | low | both |

`/var/log/mail.err` absence and `mail_logs: 0` are **NOT** defects — both are the correct state given no error-priority mail events and no queued messages yet.

---

## 13. Commands run (evidence index)

All via `python bz.py <1|2> ...` (read-only). Key commands:
- `cat /etc/systemd/journald.conf`; `journalctl --disk-usage`; `journalctl --header`; `ls -ld /var/log/journal`
- `systemctl is-active/is-enabled rsyslog`; `cat /etc/rsyslog.d/50-default.conf /etc/rsyslog.d/postfix.conf`; `grep mail /etc/rsyslog.conf`
- `stat` on all core log files; `tail` syslog/mail.log; `grep -oE '(postfix/..|dovecot|opendkim|spamd)' /var/log/mail.log | sort | uniq -c`
- `postconf -h header_checks`; `postconf -h maillog_file`; `cat /etc/postfix/header_checks_betazen`
- `systemctl is-active/is-enabled/show serverpanel`; `grep '^(LOG_LEVEL|APP_ENV|...)' /opt/serverpanel/.env`; `journalctl -u serverpanel -o short-iso`; `journalctl -u serverpanel | grep '"level":"(warn|error|fatal)"'`
- `ps -ef | grep 'tail .*-F.*mail.log'`; `ps -o user,pid,cmd -p <MainPID>`
- `ls /etc/logrotate.d/`; `cat /etc/logrotate.d/{rsyslog,nginx,serverpanel}`; `systemctl list-timers logrotate.timer`; `grep ... /var/lib/logrotate/status`
- `systemctl is-active/is-enabled fail2ban`; `fail2ban-client status [sshd]`; `cat /etc/fail2ban/jail.d/*.conf`; `tail /var/log/fail2ban.log`
- `mongosh "$MONGO_URI"`: countDocuments on mail_logs/audit_logs/metrics/users/domains/mailboxes; metrics `$group` by metric (min/max ts) + 60s cadence sample; mail_logs/audit_logs/metrics `getIndexes()`; newest audit_logs; `getCollectionNames().filter(/log/i)`
- `wc -l /var/log/mongodb/mongod.log`; `grep -c '"s":"E"|"s":"F"' /var/log/mongodb/mongod.log`

Code read locally (deployed v3.1.109): `backend/internal/services/{mail_log_service.go,log_service.go,audit_service.go,monitoring_service.go}`, `backend/internal/middleware/{audit.go,logger.go}`, `backend/pkg/logger/logger.go`, `backend/cmd/server/main.go`, `backend/internal/database/indexes.go`.
