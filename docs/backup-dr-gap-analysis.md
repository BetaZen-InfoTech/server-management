# Backup & Disaster-Recovery Gap Analysis — Betazen Server Panel

**Date:** 2026-06-28
**Scope:** Full read-only audit of the backup, restore, and IP/DNS-rewrite code paths.
**Goal being assessed:** Can this panel take an automatic daily backup such that, if the VPS dies,
we can install bPanel fresh on a **new server** and restore everything (data, mail, DNS, sites) —
with all old-server-IP references rewritten to the new server IP?

> **Method:** Three independent read-only passes over the codebase (DNS/IP-rewrite,
> backup/restore storage matrix, full DR state inventory). No code or server state was modified.

---

## 1. Executive summary

The panel has a **per-domain** backup feature (one website at a time) and a **purpose-built
IP-reassignment** routine — both partially useful. But there is **no whole-server / whole-panel
disaster-recovery path today.** The panel's own brain (the `serverpanel` MongoDB database), its
secrets (`.env`), DKIM keys, Dovecot users, MariaDB data, and the PowerDNS zone store are **not
captured by any backup code**. Several advertised destinations (S3, encryption, scheduling,
retention) exist only as **model/UI fields with no backend implementation.**

### Verdict on the 5 requested capabilities

| # | Capability                                    | Status                     | One-line reason                                                                           |
| - | --------------------------------------------- | -------------------------- | ----------------------------------------------------------------------------------------- |
| 1 | Restore-time DNS IP rewrite (A, AAAA, mail)   | 🟡**Partial**        | A + mail A + SPF`ip4:` work; **AAAA (IPv6) and FTP passive IP are not rewritten** |
| 2 | Backup → Google Drive, restore → new bPanel | 🔴**Absent**         | No Drive/rclone code at all; whole-server restore does not exist                          |
| 3 | Backup → FTP, restore ← FTP                 | 🟡**Works, limited** | End-to-end works, but per-domain and a "full" backup ships only the files archive         |
| 4 | Backup → S3, restore ← S3                   | 🔴**Fake**           | S3 fields exist in model + UI; backend never reads them                                   |
| 5 | Local backup + local restore                  | 🟢**Works**          | Per-domain round-trip works; a few silent bugs (see §6)                                  |

**Bottom line:** items (3) and (5) work for a single site manually. Item (1) exists but has real
gaps. Items (2) and (4) do not exist. And independent of all of that, **"reinstall bPanel and
restore everything to a new box" is not currently possible** because the panel never backs up its
own state — see §7.

---

## 2. Architecture context (what must survive a disaster)

`install.sh` rebuilds the **software** on a fresh box (nginx, MongoDB, MariaDB, PHP, Postfix,
Dovecot, OpenDKIM, PowerDNS, phpMyAdmin, Roundcube). What it **cannot** rebuild is the
**persistent state**:

- The `serverpanel` MongoDB database — every user, vendor, domain, DNS zone/record, API token,
  app, package, SSL record, FTP account, etc. (`MONGO_DB_NAME=serverpanel`).
- The secrets in `/opt/serverpanel/.env` — most critically `APP_ENCRYPTION_KEY`.
- Hosted site files (`/home/*`), MySQL databases, mailboxes, DKIM keys, SSL certs, PowerDNS zones.

A real DR backup is the union of all of that. The current backup code captures only a thin,
per-domain slice of it.

---

## 3. DNS / IP rewrite on restore (Capability 1)

A dedicated feature already exists: **`ConfigService.ReassignServerIP(oldIP, newIP)`**
([`config_service.go:1007`](../backend/internal/services/config_service.go#L1007)), reachable via:

- HTTP `POST /api/v1/whm/config/reassign-ip` (`config_handler.go:142`, route `whm_routes.go:569`)
- CLI `bzpanel reassign-ip [<old-ip>] <new-ip>` (`cmd/bzpanel/main.go:137,2525`)
- Auto-runs at the end of both transfer paths (`transfer_service.go:3494`, `transfer_panel_records.go:496`)

### DNS storage model (important for restore)

DNS is stored in **two tiers**:

1. **PowerDNS = authoritative**, backed by **SQLite** at `/var/lib/powerdns/pdns.sqlite3`
   (`launch=gsqlite3`, `install.sh:1078,1120`). *Not* BIND files, *not* MySQL, *not* Mongo.
2. **MongoDB `dns_zones` + `dns_records`** = a UI/metadata mirror (`collections.go:16-17`).

Models: `DNSZone`, `DNSRecord` in `models/dns.go:8-36` (`DNSZone.ServerIP` mirrors the IP per zone).

### What `ReassignServerIP` already covers

| Concern                                                      | Status                | Evidence                                                                                                           |
| ------------------------------------------------------------ | --------------------- | ------------------------------------------------------------------------------------------------------------------ |
| A records (apex + any name)                                  | ✅ EXISTS             | PowerDNS`pdnsutil replace-rrset` + Mongo update — `config_service.go:1101-1126`                               |
| `mail.<domain>` A record                                   | ✅ EXISTS (implicit)  | Matched by the generic A-record sweep                                                                              |
| MX                                                           | ✅ no rewrite needed  | Value is a hostname, not an IP                                                                                     |
| SPF (`v=spf1 ... ip4:<oldIP>`)                             | ✅ EXISTS             | `config_service.go:1106-1141`                                                                                    |
| DKIM / DMARC                                                 | ✅ no rewrite needed  | Public key / email address, no IP                                                                                  |
| NS / SOA                                                     | ✅ EXISTS (restamped) | `config_service.go:1048-1079,1114`                                                                               |
| `domains.server_ip` / `dns_zones.server_ip` (DB mirrors) | ✅ EXISTS             | `config_service.go:1145-1154`                                                                                    |
| Panel self-IP discovery                                      | ✅ EXISTS             | `SERVER_IP` env → fallback `hostname -I` (`config.go:189-202`); `.env` patched `config_service.go:1159` |
| Panel nginx vhost (`server_name` IP)                       | ✅ EXISTS             | sed +`nginx -t` + reload (`config_service.go:1169-1173`)                                                       |
| Vendor/tenant vhosts                                         | ✅ no rewrite needed  | Proxy to`127.0.0.1`, `server_name <domain>`                                                                    |
| Postfix myhostname / mailname                                | ✅ no rewrite needed  | Hostnames, not IPs                                                                                                 |
| SSL / Let's Encrypt                                          | ✅ no rewrite needed  | Domain-based, no IP inside                                                                                         |

### Gaps in IP rewrite

| Gap                                                  | Severity | Detail                                                                                                                                                                                                                                                                                                        |
| ---------------------------------------------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **AAAA (IPv6) not rewritten**                  | 🔴 High  | Sweep`switch recType` handles only `"A"` and `"TXT"` (`config_service.go:1100-1112`); no `AAAA` case in `ReassignServerIP` or `repointSourceDNSToDestination` (`transfer_panel_records.go:2056` filters `$4=="A"`). If the new box has a new IPv6, every AAAA (apex, `mail`) stays stale. |
| **pure-ftpd `ForcePassiveIP` not rewritten** | 🔴 High  | `install.sh:1163` writes the old IP to `/etc/pure-ftpd/conf/ForcePassiveIP`; never rewritten. Passive FTP hands clients the OLD IP after restore → data connections fail. Needs rewrite + `systemctl restart pure-ftpd`.                                                                               |
| **SPF only matches bare `ip4:<oldIP>`**      | 🟡 Med   | A CIDR (`ip4:<oldIP>/NN`) or `a:`/`mx:` mechanism is not matched (`config_service.go:1107-1108`).                                                                                                                                                                                                     |
| **No backend restart after `.env` patch**    | 🟡 Med   | In-process`cfg.ServerIP` stays stale until restart (`config_service.go:1156-1163`). On a fresh box `hostname -I` already returns the new IP so usually moot.                                                                                                                                            |
| **NS restamp is destructive**                  | 🟡 Med   | Forces NS to`dns1-4.betazeninfotech.com.` on every zone — overwrites any custom delegation.                                                                                                                                                                                                                |
| **PTR / registrar glue**                       | ⚪ Info  | Not managed (lives at provider/registrar). Document as a manual DR step.                                                                                                                                                                                                                                      |
| **`/etc/hosts` public-IP entries**           | ⚪ Info  | Only the loopback`127.0.1.1` line is managed (`config_service.go:243`).                                                                                                                                                                                                                                   |

**Practical path for a new IPv4 box:** restore PowerDNS SQLite + Mongo + files, then run
`bzpanel reassign-ip <old-ipv4> <new-ipv4>`. That covers A (incl. `mail`), SPF `ip4:`, both DB
mirrors, panel `.env` + nginx, and NS/SOA. Patch manually (or add code): **AAAA, ForcePassiveIP,
backend restart.**

---

## 4. Backup / restore storage matrix (Capabilities 2–5)

There are **two separate backup subsystems** — do not conflate:

- **Domain backups** — `BackupService` / `models.Backup`, the WHM + cPanel `/backups` feature
  (this is what the questions are about).
- **App backups** — `AppService.Backup/Restore` (`app_backup.go`), per-deployed-app code
  snapshots, local-disk only, different routes (`/apps/:name/backup`).

### Domain-backup destination matrix

| Destination                     | Backup  | Restore | Evidence                                                                                                                                                                                                                                                                        | Gap                                                |
| ------------------------------- | ------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------- |
| **Local (on VPS)**        | ✅ full | ✅ full | `backup_service.go:71-134`, `211-267`; `agent/backup.go:13-124`; routes `whm_routes.go:320-323`                                                                                                                                                                         | Works end-to-end (caveats §6)                     |
| **FTP**                   | ✅ full | ✅ full | `transferToRemote`→`TransferViaFTP` (curl `-T`); `restoreFromRemote`→`DownloadViaFTP` (curl `-o`) — `backup_service.go:173,187`; `agent/backup.go:140-170`                                                                                                   | Single-file only; "full" ships only files archive  |
| **SFTP / SCP**            | ✅ full | ✅ full | Native Go SSH`SCPUpload`/`SCPDownload` — `agent/backup.go:129-175`                                                                                                                                                                                                       | Same single-file limitation                        |
| **S3**                    | 🔴 none | 🔴 none | Fields`models/backup.go:33-34,47-51,75-78`; `storage=s3` validated `backup.go:43`; UI label `whm/BackupsPage.tsx:44`. **But** `transferToRemote`/`downloadFromRemote` switch only on `sftp/ftp/scp` and `Create()` transfers only when `storage=="remote" |                                                    |
| **Google Drive / rclone** | 🔴 none | 🔴 none | No`drive/rclone/gdrive` references in any backup code.                                                                                                                                                                                                                        | **Absent entirely** — no model field, no UI |

**Net: only Local + FTP + SFTP + SCP are real. S3 is a stub; Google Drive/rclone does not exist.**

### Per-domain vs whole-server

**Per-domain / per-user** — every path is scoped to one `req.Domain` + `req.User`
(`backup_service.go:71-121`). A `type:"full"` backup writes **four sibling archives** to
`/home/<user>/backups/`:

- Files → `tar -czf -C /home <user>` (`agent/backup.go:13`)
- Database → `mongodump --db <domain>` (`agent/backup.go:18`) — **note: dumps a DB named after the
  domain, not `serverpanel`**
- Email → `tar` from `/var/mail/vhosts/<domain>` (`agent/backup.go:23`)
- Config → DNS zone text, SSL `live/`, nginx vhost, PHP-FPM pool, user crontab, Mongo-record JSON
  (`backup_service.go:357-399`)

### Scheduling, encryption, retention — all unimplemented

| Feature                          | Status    | Evidence                                                                                                                                                                                                                                                                                   |
| -------------------------------- | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Scheduled/daily backup** | 🔴 Broken | `CreateSchedule` writes a cron calling `/opt/serverpanel/backend/scripts/backup.sh` (`backup_service.go:337`) — **that script does not exist anywhere in the repo.** Every scheduled backup is a no-op. `DeleteSchedule` also never removes the cron line it created.       |
| **Encryption**             | 🔴 Unused | `BACKUP_ENCRYPTION_KEY` loaded into config (`config.go:84,162`) and referenced nowhere else. `EncryptionPassword` (`models/backup.go:45,59`) never read. No AES/CBC/GCM in any backup path. `FEATURES_VENDOR_WHM.md:1210` claims "AES-256-CBC" — **false documentation.** |
| **Retention**              | 🔴 Unused | `BackupSchedule.RetentionCount` (`models/backup.go:74`) never read. Backups accumulate on disk indefinitely.                                                                                                                                                                           |

---

## 5. Full DR state inventory (what a real backup MUST capture)

| #  | Item                                                                                                                                                           | Path                                                            | Needed for clean restore?                   | Captured today?     | Notes                                                                                                                                                                                               |
| -- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- | ------------------------------------------- | ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1  | **`serverpanel` MongoDB DB** (all collections)                                                                                                         | DB`serverpanel` (`install.sh:1546`; `collections.go`)     | **YES — the panel's entire brain**   | 🔴**NO**      | `BackupMongoDB` passes the **domain** as DB name → `mongodump --db <domain>` (`agent/backup.go:18`, called `backup_service.go:106,114`), never `serverpanel`                       |
| 2  | **Panel secrets `.env`** (`APP_ENCRYPTION_KEY`, `JWT_SECRET`, `MONGO_PASS`, `AGENT_API_KEY`, `BACKUP_ENCRYPTION_KEY`, `PMA_SIGNON_SECRET`) | `/opt/serverpanel/.env`                                       | **YES — critical**                   | 🔴**NO**      | `APP_ENCRYPTION_KEY` decrypts PATs/SMTP pass/webhook secrets/SSO tokens at rest in Mongo (`main.go:120-127`). Lose it → restored Mongo secrets are **permanently undecryptable**         |
| 3  | **Hosted site files**                                                                                                                                    | `/home/<user>/`                                               | YES                                         | 🟡 PARTIAL          | Only the single user of an explicit per-domain backup; no`/home` sweep (`agent/backup.go:13`)                                                                                                   |
| 4  | **MariaDB / MySQL DBs**                                                                                                                                  | `/var/lib/mysql`                                              | **YES** (MySQL sites + `roundcube`) | 🔴**NO**      | No`mysqldump` in any backup path (`mysqldump` exists only in the live transfer flow, `agent/transfer.go:615`)                                                                                 |
| 5  | **Mailboxes + Dovecot users**                                                                                                                            | `/var/mail/vhosts/<domain>/`; `/etc/dovecot/users`          | YES                                         | 🟡 PARTIAL + BROKEN | Backup tars`/var/mail/vhosts` but restore extracts to `/var/vmail` (`agent/backup.go:24` vs `:102`). **`/etc/dovecot/users` (SHA512 hashes) not captured** → no mailbox can log in |
| 6  | **nginx vhosts**                                                                                                                                         | `/etc/nginx/sites-available/<domain>`                         | YES (per-domain regenerable)                | 🟡 PARTIAL          | Per-domain only (`backup_service.go:381`)                                                                                                                                                         |
| 7  | **SSL certs**                                                                                                                                            | `/etc/letsencrypt/`                                           | YES                                         | 🟡 PARTIAL          | Only per-domain`live/` via `cp -rL` (`backup_service.go:377`); skips `archive/`, `renewal/`, ACME account keys                                                                            |
| 8  | **PowerDNS zones**                                                                                                                                       | `/var/lib/powerdns/pdns.sqlite3`; `/etc/powerdns/pdns.conf` | YES                                         | 🟡 PARTIAL          | Per-domain`pdnsutil list-zone` text export only (`backup_service.go:372`); the **SQLite DB + config (holds API key) never captured**                                                      |
| 9  | **PHP-FPM pools**                                                                                                                                        | `/etc/php/*/fpm/pool.d/<domain>.conf`                         | YES (regenerable)                           | 🟡 PARTIAL          | Per-domain only (`backup_service.go:385`)                                                                                                                                                         |
| 10 | **OpenDKIM keys**                                                                                                                                        | `/etc/opendkim/keys/`, `signing.table`, `key.table`       | **YES — critical**                   | 🔴**NO**      | Lose these →**DKIM breaks for every domain** (published DNS TXT no longer matches)                                                                                                           |
| 11 | **phpMyAdmin secrets**                                                                                                                                   | `/etc/phpmyadmin/signon-secret`, `config.inc.php`           | YES                                         | 🔴**NO**      | `signon-secret` must equal `PMA_SIGNON_SECRET` in `.env`                                                                                                                                      |
| 12 | **PowerDNS config + API key**                                                                                                                            | `/etc/powerdns/pdns.conf`                                     | YES                                         | 🔴**NO**      | Embeds`AGENT_KEY`                                                                                                                                                                                 |

### Additional uncaptured state (found while reading `install.sh`)

- **Roundcube** DB + `/etc/roundcube/*` (`des_key`, db password, `sso_hmac_secret`) — webmail prefs/contacts/identities lost.
- **Postfix** `/etc/postfix/main.cf` + `virtual_*` hash maps.
- **Pure-FTPd** TLS cert `/etc/ssl/private/pure-ftpd.pem`.
- **PM2** dump `/root/.pm2/dump.pm2` and app systemd units (`/etc/systemd/system/sp-app-*.service` — only captured per-app by `app_backup.go:84`).

---

## 6. Confirmed bugs (independent of DR scope)

1. **`BackupMongoDB` dumps the wrong database** — `--db <domain>` instead of `serverpanel`
   (`agent/backup.go:18`).
2. **Email backup/restore directory mismatch** — backup from `/var/mail/vhosts`, restore to
   `/var/vmail` (`agent/backup.go:24` vs `:102`).
3. **Scheduled backups call a non-existent script** — `/opt/serverpanel/backend/scripts/backup.sh`
   (`backup_service.go:337`).
4. **"full" backup ignores side-archive errors** — only `BackupFiles` sets `backupErr`; DB/email/
   config failures are discarded yet the backup is marked `completed` (`backup_service.go:106-110`).
5. **Remote "full" backup ships only the files archive** — DB/email/config side-archives stay
   local (`backup_service.go:147`).
6. **cPanel "full" restore restores files only** — silently skips DB/email/config
   (`backup_handler.go:151`; `restoreFromFile` `backup_service.go:244-248`).
7. **False encryption claim** in `FEATURES_VENDOR_WHM.md:1210` (no AES code exists).

---

## 7. What a real daily DR backup must do

A server-level job (independent of the broken in-panel scheduler) should, each day, bundle and ship
off-site:

| Component          | Action                                                                                                                                                  |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Panel brain        | `mongodump --db serverpanel` (the **whole** DB)                                                                                                 |
| Secrets (critical) | `/opt/serverpanel/.env` (esp. `APP_ENCRYPTION_KEY`)                                                                                                 |
| Hosted sites       | `tar /home`                                                                                                                                           |
| MySQL              | `mysqldump --all-databases` (incl. `roundcube`)                                                                                                     |
| Email              | `tar /var/mail/vhosts` (or `/var/vmail` — **fix the mismatch first**) + `/etc/dovecot/users`                                               |
| DKIM               | `/etc/opendkim/keys` + `signing.table` + `key.table`                                                                                              |
| DNS                | `/var/lib/powerdns/pdns.sqlite3` + `/etc/powerdns/pdns.conf`                                                                                        |
| SSL                | whole`/etc/letsencrypt` (incl. `archive/`, `renewal/`, account keys)                                                                              |
| Web/mail config    | `/etc/nginx/sites-available`, `/etc/php/*/fpm/pool.d`, `/etc/postfix`, `/etc/roundcube`, `/etc/phpmyadmin/signon-secret` + `config.inc.php` |
| Encryption         | encrypt the bundle before upload (rclone crypt or GPG)                                                                                                  |
| Off-site           | ship to destination + rotate (retention by count/age)                                                                                                   |
| Destinations       | **Google Drive (rclone)**, **S3/B2**, **FTP/SFTP**, **local**                                                                   |

**Restore flow:** fresh `install.sh` on the new box → restore `serverpanel` Mongo + `.env` + `/home`

+ MySQL + mail + DKIM + DNS + configs → run `bzpanel reassign-ip <old-ip> <new-ip>` → manually
  patch **AAAA records** + **pure-ftpd ForcePassiveIP** → restart backend, nginx, pure-ftpd, pdns.

---

## 8. Proposed remediation (priority order)

1. **Whole-server DR backup + restore scripts** (`bzpanel-backup.sh` / `bzpanel-restore.sh`)
   covering §7. Highest impact — unblocks "reinstall on a new box."
2. **Wire real destinations:** rclone (Google Drive / S3 / B2) + fix the dead S3 path, or route
   everything through rclone for one consistent backend.
3. **Fix the in-panel scheduler** — ship the missing `backup.sh`, enforce `RetentionCount`, and
   remove the cron line on `DeleteSchedule`.
4. **Implement encryption** (use `BACKUP_ENCRYPTION_KEY` / `EncryptionPassword`) or remove the
   false claim from docs.
5. **Fix confirmed bugs §6** — `serverpanel` dump target, email dir mismatch, side-archive error
   propagation, "full" restore scope.
6. **Extend `ReassignServerIP`** — add AAAA rewrite, pure-ftpd `ForcePassiveIP`, optional backend
   restart, and broaden SPF matching.

---

## Appendix — key files

| Area              | File                                                                          |
| ----------------- | ----------------------------------------------------------------------------- |
| Backup service    | `backend/internal/services/backup_service.go`                               |
| Backup agent ops  | `backend/internal/agent/backup.go`                                          |
| Backup model      | `backend/internal/models/backup.go`                                         |
| Backup handler    | `backend/internal/handlers/backup_handler.go`                               |
| App backups       | `backend/internal/services/app_backup.go`                                   |
| IP reassignment   | `backend/internal/services/config_service.go` (`ReassignServerIP` ~L1007) |
| DNS model         | `backend/internal/models/dns.go`                                            |
| DNS service       | `backend/internal/services/dns_service.go`                                  |
| Mongo collections | `backend/internal/database/collections.go`                                  |
| Provisioning      | `install.sh`                                                                |
| Config / secrets  | `backend/internal/config/config.go`                                         |

---

*Generated from a read-only audit on 2026-06-28. No code or server state was modified.*
