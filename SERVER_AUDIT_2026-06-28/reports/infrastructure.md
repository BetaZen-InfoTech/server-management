# Infrastructure Audit — BetaZen Server Panel (Agent 1)

- **Date:** 2026-06-28
- **Scope:** Read-only infrastructure inspection of both demo VPS clones.
- **Server 1 (S1):** `89.116.34.207` — migration **SOURCE** — hostname `srv1785162`
- **Server 2 (S2):** `195.35.7.64` — migration **DEST** — hostname `srv1789639`
- **Deployed code:** git `466b52e` (v3.1.109) on both — confirmed via `git -C /opt/serverpanel rev-parse HEAD` and `GET /api/v1/version` → `3.1.109`.
- **Method:** SSH via `bz.py` helper, runtime state only. No mutating commands were run.

## Executive Summary

Both servers are healthy clones with **no failed systemd units** (`systemctl --failed` = 0; `systemctl is-system-running` = `running`), the Go panel up and answering `200` on `/api/v1/health` and `/api/v1/version` on both, and all documented mail/DB/DNS services active and enabled. Hardware is identical (8 vCPU AMD EPYC 9354P, ~31 GiB RAM, 400 GB disk at 2% used, kernel 6.8.0-124). Memory and CPU are effectively idle (Committed_AS ~2.4 GiB of 31 GiB, load <0.2, no OOM events). The two boxes are near-perfect twins; the only differences are expected per-host values (IPs, machine-id, `BACKUP_ENCRYPTION_KEY`) plus transient audit-induced noise.

The findings worth acting on are operational hygiene, not outages: (1) an **orphaned PM2 daemon** runs as root on both servers managing zero application processes (the panel runs under systemd, not PM2) — pure leftover from early Node-era setup; (2) **`openbsd-inetd` runs with no `/etc/inetd.conf`** — a do-nothing daemon; (3) the panel's **scheduled-backup feature is broken** — the cron command it writes points to `/opt/serverpanel/backend/scripts/backup.sh`, which does not exist anywhere on either box; (4) **`BACKUP_DIR=/var/backups/serverpanel` exists but is empty and is not actually used** by the on-demand backup path (which writes to `/home/<user>/backups`); (5) `pure-ftpd` (:21 + passive 30000-30009) is publicly exposed via UFW though it is not part of the documented stack; (6) UFW allows `443/tcp` and nginx only listens on IPv4 `:80` while there is no TLS/443 service — minor inconsistencies. No high CPU, no memory anomaly, no startup failure.

---

## 1. OS / Kernel / Virtualization

Command: `hostnamectl`, `cat /etc/os-release`, `uname -a`

| Attribute | S1 | S2 |
|---|---|---|
| OS | Ubuntu 24.04.4 LTS (noble) | Ubuntu 24.04.4 LTS (noble) |
| Kernel | 6.8.0-124-generic | 6.8.0-124-generic |
| Virtualization | KVM (QEMU i440FX) | KVM (QEMU i440FX) |
| Machine ID | `fa9ad2bb…e3a5c3` | `e3b46a91…884e6b` |
| Uptime at audit | up 36 min | up 32 min |

No drift other than machine-id (expected). Both freshly rebooted (~30 min uptime).

## 2. CPU / RAM / Disk / Swap

Command: `lscpu`, `free -h`, `swapon --show`, `df -hT`, `lsblk`, `cat /proc/meminfo`

- **CPU:** 8 vCPU, AMD EPYC 9354P 32-Core, 1 thread/core, KVM full virt — **identical** on both.
- **RAM:** `MemTotal` 32,865,096 kB (~31 GiB). `MemAvailable` ~31.6 GiB. `Committed_AS` ~2.4 GiB. Used ~1.2 GiB. **No memory pressure, no anomaly.**
- **Swap: 0 B on both** (`swapon --show` empty, `/proc/swaps` empty). `vm.swappiness=60`. With 31 GiB free this is not currently a risk, but there is **no swap safety margin** if a runaway process spikes — worth a small swapfile or zram for resilience (informational).
- **Disk:** `/dev/sda1` ext4 387 GB, **2% used** (7.2–7.3 GB). `/boot` 15%, `/boot/efi` 6%. Inodes 1% used. Plenty of headroom on both.

```
S1: /dev/sda1 ext4 387G 7.3G 380G 2% /
S2: /dev/sda1 ext4 387G 7.2G 380G 2% /
Swap: 0B 0B 0B   (both)
```

## 3. systemd Services

Command: `systemctl --failed`, `systemctl is-system-running`, `systemctl list-units --type=service --state=running`, `systemctl list-unit-files --state=enabled`

- **`systemctl --failed` → 0 units** on both. `is-system-running` → `running` on both. No degraded state.
- **Running services match** between S1 and S2 (30 active services each), including the full panel/mail/DB/DNS stack:
  `serverpanel`, `nginx`, `mongod`, `mariadb`, `postfix@-`, `dovecot`, `opendkim`, `spamd`, `pdns`, `php8.2-fpm`, `fail2ban`, plus `pure-ftpd`, `inetd`, `qemu-guest-agent`, `cron`, `ssh`, standard systemd units.

### Key service status (active / enabled)

| Service | active | enabled | Note |
|---|---|---|---|
| serverpanel | active | enabled | Go panel, OK |
| nginx | active | enabled | reverse proxy, OK |
| mongod | active | enabled | panel DB, OK |
| mariadb (mysql alias) | active | enabled | webmail DB, OK |
| postfix / dovecot / opendkim / spamd / pdns | active | enabled | mail+DNS stack OK |
| fail2ban | active | enabled | OK |
| php8.2-fpm | active | enabled | webmail/phpMyAdmin |
| pure-ftpd | active | enabled (sysv) | **not in documented stack** (see §9) |
| inetd | active | enabled | **no config — useless** (see §9) |
| **pm2-root** | **inactive (dead)** | **enabled** | unit dead, but a PM2 daemon is running anyway (see §9) |
| certbot.timer | (timer) | enabled | scheduled though No-TLS env (see §6) |
| ufw | (oneshot) | enabled | firewall active (see §7) |

Findings are **identical on both servers** — no service-enablement drift.

## 4. nginx / Reverse-Proxy Wiring

Command: `nginx -t`, `nginx -v`, dump of `/etc/nginx/sites-enabled/serverpanel`

- `nginx -t` → **syntax OK, test successful** on both. nginx 1.24.0 (Ubuntu).
- One vhost: `sites-enabled/serverpanel` → `sites-available/serverpanel`. `conf.d/` empty. `listen 80 default_server`.
- Reverse proxy correctly wired: `location /` and `location /ws/` → `proxy_pass http://127.0.0.1:8080` (the Go panel). `client_max_body_size 10G`, `proxy_request_buffering off`, long timeouts — matches the documented Fiber BodyLimit intent.
- `/webmail/` served via PHP-FPM (`unix:/var/run/php/php8.2-fpm.sock`); phpMyAdmin via `snippets/phpmyadmin.conf`; ACME webroot at `/var/www/certbot`.
- **Cosmetic redundancy (both):** `server_name` lists the IP twice — `server_name 89.116.34.207 89.116.34.207 _;` (S2: `195.35.7.64 195.35.7.64 _`), and the host-guard `if ($host !~* ^(IP|IP)$)` also duplicates the IP. Functionally harmless (a templating artifact; relates to the v3.1.107 "server_name pollution" fix). Each server has its own correct IP — **no cross-host pollution.**
- **IPv6 gap (both):** nginx listens only on `0.0.0.0:80` (no `[::]:80`). IPv6 clients cannot reach the panel/webmail. IPv4 `:80` returns `200` for `/api/v1/health` and `/api/v1/version` on both. Minor; the panel is addressed by bare IPv4 anyway.

## 5. Cron / systemd Timers

Command: `cat /etc/crontab`, `ls + cat /etc/cron.d/*`, `crontab -l`, `systemctl list-timers --all`

- `/etc/crontab` is stock Ubuntu. **Root crontab is empty** (`crontab -l` → none) on both.
- `/etc/cron.d/` (identical on both): `certbot`, `e2scrub_all`, `php`, `roundcube-core`, `sysstat`, and one panel-managed job:
  - `serverpanel-mail-ssl-sweep` → `17 * * * * root /usr/local/bin/bzpanel mail-ssl-sweep >> /var/log/serverpanel-mail-ssl-sweep.log 2>&1` (hourly; `bzpanel` is a symlink → `/opt/serverpanel/bin/bzpanel`, a valid Go ELF binary). Most other cron.d jobs are guarded by `test -d /run/systemd/system` (no-op under systemd; the work is done by the equivalent timers).
- **Timers (19, identical set):** stock Ubuntu maintenance (`logrotate`, `fstrim`, `sysstat-*`, `man-db`, `phpsessionclean`, `roundcube-gc/cleandb`, `certbot`, etc.). All healthy.
- **No backup cron job / timer exists** on either server (`grep -rIl -i backup /etc/cron* /etc/systemd/system` → only `/etc/cron.daily/dpkg`, unrelated). See §8.

## 6. certbot scheduled despite "No TLS/443"

`certbot.timer` is enabled and `/etc/cron.d/certbot` is present on both, but the environment runs **without TLS/443** (panel addressed by bare IP). With no issued certs this is a harmless no-op (renew finds nothing). Informational only — it is the stock certbot package behavior, not a misconfiguration. UFW also pre-opens `443/tcp` (see §7).

## 7. Network / Firewall / Listening Ports

Command: `ip -br addr`, `ip route`, `ss -tulpn`, `ufw status verbose`, `iptables -S`

- **Interfaces:** single `eth0`. S1 `89.116.34.207/24` + IPv6 `2a02:4780:12:aaa3::1/48`; S2 `195.35.7.64/24` + IPv6 `2a02:4780:12:aa93::1/48`. Default route via `…/.254`. DNS = `8.8.8.8 / 8.8.4.4`. No drift beyond addresses.
- **UFW active**, default deny-incoming / allow-outgoing. `iptables -S` confirms `-P INPUT DROP` with ufw chains. nftables ruleset = 413 lines (ufw-generated). **Identical allow-list on both:**
  `22, 80, 443, 53/tcp, 53/udp, 25, 465, 587, 143, 993, 110, 995, 21, 30000:30009` (v4 + v6).

### Listening sockets (`ss -tulpn`) and owners — same on both

| Port | Proc | Bind | Exposure |
|---|---|---|---|
| 22 | sshd | 0.0.0.0 + [::] | UFW allow — OK |
| 80 | nginx | 0.0.0.0 only | UFW allow — OK (IPv4 only, see §4) |
| 25/465/587 | postfix master | 0.0.0.0 | UFW allow — mail |
| 110/143/993/995 | dovecot | 0.0.0.0 + [::] | UFW allow — mail |
| 53 (tcp+udp) | pdns_server | 0.0.0.0 | UFW allow — DNS |
| 21 | pure-ftpd | 0.0.0.0 + [::] | UFW allow — **FTP exposed** (see §9) |
| 30000-30009 | (pure-ftpd passive) | — | UFW allow — FTP passive |
| **8080** | **server (Go panel)** | **0.0.0.0** | **NOT in UFW allow-list → blocked externally by default-deny.** Verified: TCP to public IP:8080 = closed/filtered. Good, but it binds 0.0.0.0 rather than 127.0.0.1 — it relies solely on UFW for isolation. |
| 8081 | pdns_server (API) | 127.0.0.1 | loopback — OK |
| 3306 | mariadbd | 127.0.0.1 | loopback — OK |
| 27017 | mongod | 127.0.0.1 | loopback — OK |
| 783 | spamd | 127.0.0.1 + [::1] | loopback — OK |

- **443/tcp is allowed in UFW but nothing listens on it** (no TLS service) — harmless pre-open, consistent with the No-TLS note.
- **Panel `:8080` binds `0.0.0.0`** (confirmed in panel startup banner "bound on host 0.0.0.0 and port 8080"). External reachability test from each box to its own public IP:8080 = **closed/filtered** (UFW default-deny is the only thing protecting it). Recommend the panel bind `127.0.0.1:8080` for defense-in-depth, since nginx proxies from loopback anyway. (Code change, not a runtime fix.)

## 8. Backup Configuration — FEATURE BROKEN / DIR UNUSED

Command: `ls -la /var/backups/serverpanel`, `grep -i backup /opt/serverpanel/.env`, mongo collection counts, `find /opt/serverpanel -name backup.sh`, repo read of `backend/internal/services/backup_service.go`

- `BACKUP_DIR=/var/backups/serverpanel` and `BACKUP_ENCRYPTION_KEY=<hex>` are set in `.env` (key differs per host — expected). The directory **exists but is empty** on both: `/var/backups/serverpanel/` contains nothing.
- Mongo: `backups = 0`, `backup_schedules = 0` on both. **No backup has ever run and no schedule is configured** — consistent with a fresh demo.
- **Broken scheduled-backup path:** `backup_service.go:337` builds the cron command as
  `"/opt/serverpanel/backend/scripts/backup.sh %s %s %s"` and registers it via `agent.WriteCrontab`. **That script does not exist** — `/opt/serverpanel/backend/` has no `scripts/` directory at all (only `cmd/ internal/ pkg/ .air.toml Dockerfile go.mod go.sum`), and `find /opt/serverpanel -name backup.sh` returns nothing. **Any backup schedule created via the panel would write a cron job that fails with "No such file or directory."** Latent bug present on both deployed clones; low/medium impact only because no schedule is set yet.
- **BACKUP_DIR is effectively unused by the on-demand path:** `Create()` (`backup_service.go:73`) writes to `/home/<user>/backups`, **not** to `$BACKUP_DIR`. So even successful on-demand backups would never populate `/var/backups/serverpanel`. The configured `BACKUP_DIR` and the actual output path disagree — a config/code mismatch worth flagging.

## 9. Unused / Orphan / Unnecessary Background Processes

Command: `pm2 list`, `cat /root/.pm2/dump.pm2`, `ps -o pid,ppid,lstart,cmd`, `systemctl status inetd pure-ftpd`, `cat /etc/inetd.conf`

1. **PM2 orphan daemon (both).** `pm2-root.service` shows `inactive (dead)`, yet a `PM2 v7.0.1: God Daemon` (root, started at boot ~19:10/19:19) plus a `node /root/.pm2` process (~60 MB RSS) are running. `pm2 list` shows **zero application processes**; `dump.pm2 = []`. The only thing PM2 runs is its own `pm2-logrotate` module (already restarted 4×). The serverpanel API runs under **systemd**, not PM2 — so PM2 is pure leftover from an earlier Node-era install (the unit's PATH even references `/opt/go/1.23/bin`). **Recommend removing PM2** (orphaned cruft, runs as root, no purpose). Same on both — no drift.
2. **`inetd` running with no config (both).** `inetd.service` is `active (running)` but `/etc/inetd.conf` **does not exist**, so it superintends nothing. The `openbsd-inetd` package is installed and pulled this in. **Recommend disable/remove** — a useless root daemon and unnecessary attack surface.
3. **`pure-ftpd` exposed (both).** `pure-ftpd` listens on `:21` (+ passive 30000-30009) on all interfaces and UFW allows it publicly (`-P <IP> -J HIGH -A -O clf:… -p 30000:30009`). FTP is **not part of the documented mail/panel stack** in the project notes. If FTP is intentional for File Manager/operator transfer it is fine; otherwise it is an unnecessary publicly-exposed service. Flagging for confirmation.
4. **`php8.2-redis` extension installed but no redis.** The PHP redis extension and `redis-server` is **not installed/running** (no `:6379`, service `not-found`). Harmless unused extension. Informational.

No duplicate/competing services (e.g., no apache2 vs nginx, no mysql+mongo conflict). `mysql` is just an alias of `mariadb`.

## 10. Top Processes / Resource Hotspots

Command: `ps -eo pid,ppid,user,%cpu,%mem,rss,comm --sort=-%mem` / `--sort=-%cpu`

- **By memory (both):** `mongod` ~200 MB, `spamd` (+2 children) ~140 MB each, `mariadbd` ~110 MB, `pdns_server` ~64 MB, PM2 node ~60 MB, `php-fpm` ~42 MB, `fail2ban` ~34 MB, the Go `server` panel only ~28 MB. Total used ~1.2 GiB of 31 GiB. **No memory anomaly.**
- **By CPU:** the only high-CPU entries were the **auditor's own SSH/`bash`/`mongosh` sessions** (transient, e.g. `sshd 76%`, `mongosh 13%`). All real daemons were ≤1.1% (`mongod` ~1%, `systemd` ~1%). **No persistent high-CPU process.**
- Process count: S1 = 194, S2 = 199 (the delta is the auditor's concurrent shells/mongosh — not a real difference).

## 11. /opt/serverpanel Permissions & Env Files

Command: `ls -la /opt/serverpanel`, `stat -c '%A %U:%G' .env`

- **`.env` = `-rw------- (0600) root:root`** on both — **correct** per project requirement.
- Other env templates (`.env.dev/.example/.local/.prod`) are `0644` (non-secret templates — fine).
- Tree owned by `root:root`. Git checkout present (`rev 466b52e`). Working tree shows the same install-time drift on both: `M frontend/package-lock.json`, `M scripts/mail-diagnose.sh`, `M scripts/reconcile-email.sh`, untracked `bin/` (built binaries). Consistent across servers — install artifacts, not tampering.

## 12. Boot / Startup Logs

Command: `journalctl -b -p err`, `journalctl -u serverpanel.service`

- **serverpanel** started cleanly on both: "Connected to MongoDB", deploy pool started (4 workers), HTTP server `:8080`, "nginx self-heal: config OK", mail-log ingestor started. `panel-mail: auto-bootstrap skipped — no usable mail domain` (expected — no mail domain configured yet). Fiber v2.52.5, 1304 handlers.
- **`journalctl -b -p err`** shows only two benign categories on both:
  - MariaDB postinst: *"You need to use --log-bin to make --expire-logs-days … work"* — informational, binlog not enabled (fine for this role).
  - `sshd: kex_exchange_identification: read: Connection reset by peer` — these are the **`bz.py` TCP preflight probes** (open socket, close it) from this very audit; S2 has more lines only because it received more audit traffic. **Self-inflicted, benign.**
- **No OOM events** (`no OOM events`), no service crash, no panic.

## 13. Drift Between S1 (source) and S2 (dest)

The two servers are functionally identical clones. All differences are expected or audit-induced:

| Item | S1 | S2 | Verdict |
|---|---|---|---|
| Public IP / IPv6 | 89.116.34.207 | 195.35.7.64 | expected |
| machine-id | fa9ad2bb… | e3b46a91… | expected |
| BACKUP_ENCRYPTION_KEY | 305f3503… | 86ee4b3d… | expected (per-host) |
| nginx server_name IP | 89.116.34.207 | 195.35.7.64 | correct per host (no pollution) |
| Process count | 194 | 199 | audit shells only |
| sshd kex errors | 4 | 17 | audit preflight only |
| Package versions | identical | identical | no drift |
| Service set / enablement | identical | identical | no drift |
| Git rev / panel version | 466b52e / 3.1.109 | 466b52e / 3.1.109 | no drift |

**No meaningful infrastructure drift.** Both carry the same latent issues (PM2 orphan, inetd, broken scheduled-backup path, BACKUP_DIR mismatch).

---

## Findings Summary (by severity)

| # | Severity | Server | Finding |
|---|---|---|---|
| 1 | medium | both | Scheduled-backup feature broken: cron points to non-existent `/opt/serverpanel/backend/scripts/backup.sh` |
| 2 | medium | both | Orphaned PM2 God Daemon running as root, managing 0 apps (panel is under systemd) |
| 3 | low | both | `BACKUP_DIR=/var/backups/serverpanel` set but unused; on-demand backups write to `/home/<user>/backups` |
| 4 | low | both | `inetd` running with no `/etc/inetd.conf` — useless root daemon (`openbsd-inetd`) |
| 5 | low | both | `pure-ftpd` (:21 + 30000-30009) publicly exposed via UFW; not in documented stack |
| 6 | low | both | Panel binds `0.0.0.0:8080` (UFW-only isolation); recommend `127.0.0.1` |
| 7 | low | both | 0 swap, no safety margin (currently fine at 1.2/31 GiB used) |
| 8 | info | both | UFW opens 443 + certbot scheduled though No-TLS/443 env; nginx IPv4-only `:80` |

**All recommended changes are operational hygiene; nothing is causing an active outage.** No changes were made — this is a read-only report.
