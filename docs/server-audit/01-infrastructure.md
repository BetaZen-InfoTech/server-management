# Server 1 — Infrastructure Audit

**Host:** `89.116.34.207` (`srv1785162`) — BetaZen Server Panel (bPanel) demo box
**OS:** Ubuntu 24.04.4 LTS · Kernel `6.8.0-124-generic` · x86_64
**Hardware:** AMD EPYC 9354P, 8 vCPU · 31 GiB RAM · 387 GB disk (single `/dev/sda1`)
**Audit date:** 2026-06-29 · **Type:** READ-ONLY infrastructure audit (Agent 1)
**Panel:** serverpanel.service deployed v3.1.109 on :8080 (behind nginx :80)

---

## Summary

Server 1 is in **good overall health** with abundant headroom: load average 0.08 on 8 cores (~99% CPU idle over the last hour per `sar`), 23 GiB RAM free, and only **3% disk** used (8.0 GB of 387 GB) with 1% inode usage. There are **zero failed systemd units**, `systemctl is-system-running` reports `running`, no zombie processes, no broken/half-installed packages, and `nginx -t` passes. The package stack is clean (single web server — nginx; single DB — MariaDB; single MTA — Postfix; no Apache/MySQL/exim duplication). The two material risks are operational, not capacity: **(1) there is no backup configured or running at all** — `/var/backups/serverpanel` is empty, no backup cron/timer exists, and the panel binary exposes no backup/restore command; and **(2) swap is 0 B**, so any memory spike has no cushion before the OOM killer fires. Secondary items: 40 pending apt updates (25 security), a self-duplicated nginx `server_name` that logs a harmless conflict warning, and a handful of cosmetic enabled-but-idle units. No process shows memory growth (mongod 212 MB, mariadbd 117 MB, panel `server` 13 MB — all nominal).

---

## Findings table

| Area | Status | Detail |
| --- | --- | --- |
| OS / kernel | ✅ | Ubuntu 24.04.4 LTS, kernel 6.8.0-124, uptime 19 h, 2 sessions |
| Load / CPU | ✅ | Load 0.08 / 0.05 / 0.01 on 8 vCPU (EPYC 9354P); ~98.9% idle avg (`sar`) |
| RAM | ✅ | 31 GiB total · 1.4 GiB used · 23 GiB free · 7.0 GiB buff/cache · 29 GiB available |
| Swap | ⚠️ | **0 B swap** (`swapon` empty); swappiness 60. No safety cushion on OOM |
| Disk `/` | ✅ | 387 G total, **8.0 G used (3%)**, 379 G free; inodes 1% used |
| Disk `/boot` | ✅ | 117 M / 881 M (15%) — fine |
| Largest consumers | ✅ | `/var/cache` 709 M, `/var/lib` 592 M, `/opt/serverpanel` 414 M, `/opt/go` 269 M |
| systemd state | ✅ | `is-system-running` = **running**; **0 failed units** |
| Enabled-but-dead | ✅ | All inactive enabled units are oneshots / on-demand (snapd, vmtools, unattended-upgrades) — normal |
| Running-not-enabled | ✅ | Only `ssh.service` (disabled) — but `ssh.socket` **is enabled** (socket activation). SSH persists across reboot. Not a bug |
| Timers | ✅ | 18 timers, all healthy (logrotate, certbot, sysstat, fstrim, dpkg-db-backup, roundcube-gc) |
| Cron | ✅ | `/etc/cron.d`: certbot, php, roundcube, sysstat, e2scrub, serverpanel-mail-ssl-sweep (hourly). No root/user crontab. **No backup cron** |
| Processes (CPU) | ✅ | Top non-session proc mongod 1.3%. (The 41.8% sshd was the auditor's own session) |
| Processes (MEM) | ✅ | mongod 212 M, spamd 3×~135 M, mariadbd 117 M, fail2ban 88 M, node demos ~50–67 M each |
| Memory growth | ✅ | No drift: mongod under 264 MB cgroup, panel `server` 13 MB, no leak signature |
| Zombies | ✅ | 0 defunct processes |
| Packages | ✅ | 1048 installed; `apt-get check` clean; `dpkg -C` clean; 1 hold (`cloud-init`, expected) |
| Duplicate stacks | ✅ | No Apache+nginx, no MySQL+MariaDB, no exim+postfix. Single clean stack |
| Security updates | ⚠️ | **40 updates pending, 25 security**; unattended-upgrades enabled but last shutdown-run log is from Jun 8 |
| nginx config | ⚠️ | `nginx -t` OK, but warns: conflicting server_name `89.116.34.207` (listed **twice** in default vhost) |
| nginx vhosts | ✅ | 7 enabled (1 panel default_server + 6 demo `.local` sites); no real server_name collision |
| nginx tuning | ✅ | `worker_processes auto`, `worker_connections 768` (default; fine for this load) |
| **Backups** | ❌ | **No backups exist.** `/var/backups/serverpanel` empty; no backup timer/cron; `bzpanel` has no backup/restore command |
| Boot health | ✅ | Boots in 30.3 s; slowest = cloud-init chain (~20 s, expected on cloud VM) |
| Boot errors | ⚠️ | 18 `-p err` entries this boot — all benign (sshd `kex_exchange_identification` scans, one nginx reload glitch, mariadb binlog warning) |
| fail2ban | ✅ | Active with sshd / postfix-sasl / dovecot jails |

---

## Issues found

### ISSUE-1 — No backup or disaster-recovery configured (Severity: HIGH) ❌
- `BACKUP_DIR=/var/backups/serverpanel` is set in `/opt/serverpanel/.env`, but the directory is **empty** (4.0 KB, just the folder) — no backup has ever been produced on this box.
- There is **no backup systemd timer and no backup cron entry** (only an hourly `mail-ssl-sweep` cron, unrelated to backups).
- The `bzpanel` CLI exposes **no `backup` / `restore` / `snapshot` subcommand** (the "Snapshot" strings in the binary belong to the embedded MongoDB Go driver, not a backup feature).
- This matches the repo's own `docs/backup-dr-gap-analysis.md` conclusion: the panel has only a partial per-domain backup feature and **no whole-server DR path** — the panel's own MongoDB state, `.env` secrets, DKIM keys, Dovecot users, MariaDB data, and PowerDNS zones are not captured by anything.
- **Impact:** total data loss if the disk/VM is lost. On a demo box this is acceptable, but it is the single largest infrastructure gap.

### ISSUE-2 — No swap configured (Severity: MEDIUM) ⚠️
- `swapon --show` returns nothing; `free` shows `Swap: 0B`.
- With 31 GiB RAM and only ~1.4 GiB in use there is plenty of headroom today, but **zero swap means the kernel OOM-killer has no buffer** during a transient spike (e.g. a SpamAssassin/MongoDB burst or a runaway demo app) and will kill a process outright.
- A 2–4 GiB swapfile (or `zram`) would absorb short spikes and let `vm.swappiness` keep cold pages out of RAM.

### ISSUE-3 — 40 pending package updates, 25 security (Severity: MEDIUM) ⚠️
- `apt-check` reports 40 updates immediately applicable, **25 of them standard security updates**.
- `unattended-upgrades` is enabled, but the only run log present (`unattended-upgrades-shutdown.log`) is dated Jun 8 — security patches are accumulating faster than they are being applied.

### ISSUE-4 — nginx self-duplicated server_name warning (Severity: LOW) ⚠️
- `nginx -t` emits: `conflicting server name "89.116.34.207" on 0.0.0.0:80, ignored`.
- Root cause: the panel default vhost declares `server_name 89.116.34.207 89.116.34.207 _;` — the IP is **listed twice in the same directive**. This is harmless (nginx just ignores the dup) but produces a warning on every reload/test. It is NOT a collision between two different vhosts.

### ISSUE-5 — Benign boot/runtime error-log noise (Severity: INFORMATIONAL) ⚠️
- 18 `journalctl -b -p err` lines, all non-impacting: repeated `sshd: kex_exchange_identification: Connection reset by peer` (internet background port-scans hitting :22), one historical `Reload failed for nginx.service` (during a config edit), one `pure-ftpd ... Transport endpoint is not connected`, and a MariaDB `--log-bin` advisory. None recurring or service-affecting.

### Non-issues explicitly cleared
- `ssh.service: disabled` is **correct** — Ubuntu 24.04 uses `ssh.socket` (enabled + active) for socket activation; SSH survives reboot.
- `pm2-root.service: inactive` is benign — the demo apps run as native `sp-app-*` / `betazen-demo-*` systemd units; PM2 only carries an idle `pm2-logrotate` module.
- High sshd CPU in the process snapshot was the auditor's own SSH session, not a server process.

---

## Fixes applied

**None.**

Per the read-only / reversible-only mandate, no changes were made. The one fix that was initially a candidate — `systemctl enable ssh` — was investigated and found **unnecessary**: `ssh.socket` is already enabled, so SSH is already persistent across reboots. Enabling `ssh.service` on top would have been redundant and could double-bind the listener. No other safe-and-reversible fix was identified (backups, swap, package upgrades, and nginx edits all carry more than trivial risk or fall outside "running-but-not-enabled" scope), so all are recorded as recommendations below.

---

## Recommendations (prioritized)

### P1 — Stand up a real backup / DR path (addresses ISSUE-1)
- Implement (or wire up) a scheduled job that captures the **panel's own state**: the `serverpanel` MongoDB database (`mongodump`), `/opt/serverpanel/.env`, OpenDKIM keys, `/etc/dovecot/users`, MariaDB (`mariadb-dump --all-databases`), and the PowerDNS zone store — into `BACKUP_DIR` (`/var/backups/serverpanel`), then ship a copy off-box (the gap-analysis flags Drive/S3 as not-yet-implemented; FTP path partially works).
- Add a systemd timer (e.g. `serverpanel-backup.timer`, daily) plus retention pruning.
- Verify restorability at least once. Track against `docs/backup-dr-gap-analysis.md`.
- *Demo-box caveat:* no production data here, so this is about validating the product's DR story, not protecting live data.

### P2 — Add swap (addresses ISSUE-2)
- Create a 2–4 GiB swapfile: `fallocate -l 4G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile`, then persist in `/etc/fstab`. Optionally lower `vm.swappiness` to 10 so it only engages under pressure. (Reversible, but a state change — left as a recommendation rather than applied.)

### P3 — Apply pending security updates (addresses ISSUE-3)
- Run `apt-get update && apt-get upgrade` (25 security fixes pending) during a maintenance window. Confirm `unattended-upgrades` is actually executing on schedule (`Unattended-Upgrade::Automatic-Reboot` and the timer), not only at shutdown.

### P4 — De-duplicate the nginx server_name (addresses ISSUE-4)
- Edit `/etc/nginx/sites-available/serverpanel`: change `server_name 89.116.34.207 89.116.34.207 _;` to `server_name 89.116.34.207 _;` and `nginx -s reload`. Removes the recurring conflict warning. (Low risk; the panel installer template may regenerate this, so the real fix belongs in the installer.)

### P5 — Quiet boot-error noise (addresses ISSUE-5, optional/cosmetic)
- The sshd scan noise is reduced for free once fail2ban's sshd jail bans repeat offenders (already active). No action strictly required.

---

### Appendix — Raw metrics captured

```
Load avg:        0.08 / 0.05 / 0.01   (8 vCPU)
CPU idle (sar):  98.9% avg over last hour
Mem:             31Gi total | 1.4Gi used | 23Gi free | 7.0Gi buff/cache | 29Gi avail
Swap:            0B
Disk /:          8.0G / 387G (3%) ; inodes 280026 / 52297728 (1%)
Boot time:       5.349s kernel + 24.953s userspace = 30.302s
Failed units:    0
Zombies:         0
Installed pkgs:  1048 (apt-get check OK, dpkg -C OK, 1 hold: cloud-init)
Pending updates: 40 (25 security)
Top RSS:         mongod 212MB, spamd ~135MB x3, mariadbd 117MB, fail2ban 88MB
Backup dir:      /var/backups/serverpanel — EMPTY
```

> Note: `BACKUP_ENCRYPTION_KEY` exists in `/opt/serverpanel/.env`; its value was deliberately **not** recorded in this report.
