# Agent 6 — SQL / MariaDB Audit

**Date:** 2026-06-28
**Auditor:** Agent 6 (read-only)
**Scope:** MariaDB on both demo VPS clones
- **Server 1 (S1)** = `89.116.34.207` (migration SOURCE), hostname `srv1785162`
- **Server 2 (S2)** = `195.35.7.64` (migration DEST), hostname `srv1789639`
**Deployed code:** local git repo `v3.1.109` rev `466b52e`

---

## Executive Summary

MariaDB is healthy and effectively identical on both servers. Engine is **MariaDB 10.11.14** (Ubuntu 24.04 package `10.11.14-0ubuntu0.24.04.1`). The only non-system database is **`roundcube`** (webmail), with all 17 application tables empty (0 rows) except the `system` schema-version marker — webmail has never been used. There are **no panel-created tenant/application databases**, confirming the panel provisions MySQL DBs lazily (on demand) rather than seeding any. `3306` is bound to `127.0.0.1` only (verified via running server var, config file, and `ss`), so it is not network-exposed. Root/admin accounts use **unix_socket** auth (password disabled); only the `roundcube` account uses a real password.

**Crucial confirmation for the migration / "Demo SQL Database" generation:** no application or mail data lives in MariaDB. Mail accounts are stored in Dovecot's `passwd-file` backend (`/etc/dovecot/users`, currently empty) and panel entities live in MongoDB. The MariaDB layer carries only the (empty) Roundcube webmail schema.

**Two real gaps:** (1) **no MariaDB backups of any kind** exist on either box (no `mysqldump`/`mariadb-backup` files, no backup tooling, no backup cron), and (2) the `roundcube` DB-user password **differs between S1 and S2** (expected per-install, but a migration that copies `/etc/roundcube/debian-db.php` from S1 to S2 without re-aligning the grant, or vice-versa, would break webmail DB login).

**Health:** good (with the two gaps noted above as findings).

---

## 1. Engine Version & Databases

### Commands
```bash
# S1 and S2
mariadb --version
mysql -N -e "SELECT @@version, @@version_comment, @@hostname; SHOW DATABASES;"
```

### Output (identical engine on both)
```
mariadb  Ver 15.1 Distrib 10.11.14-MariaDB, for debian-linux-gnu (x86_64)
```

| | S1 (`89.116.34.207`) | S2 (`195.35.7.64`) |
|---|---|---|
| `@@version` | `10.11.14-MariaDB-0ubuntu0.24.04.1` | `10.11.14-MariaDB-0ubuntu0.24.04.1` |
| `@@version_comment` | `Ubuntu 24.04` | `Ubuntu 24.04` |
| `@@hostname` | `srv1785162` | `srv1789639` |
| Databases | information_schema, mysql, performance_schema, **roundcube**, sys | information_schema, mysql, performance_schema, **roundcube**, sys |

**Only `roundcube` + system DBs present on both — exactly as expected. No drift.** No tenant/customer databases exist (consistent with the documented data state: `databases:0` in Mongo).

Datadir size: `du -sh /var/lib/mysql` = **127M** on both.

---

## 2. Roundcube Schema (tables, row counts, key tables)

### Command (per server)
```bash
mysql -N -e "SELECT table_name, engine, table_rows, ROUND((data_length+index_length)/1024,1) AS kb
             FROM information_schema.tables WHERE table_schema='roundcube' ORDER BY table_name;"
# plus exact COUNT(*) per table, and the schema version row
```

### Tables (17), all InnoDB, identical on S1 and S2

```
cache, cache_index, cache_messages, cache_shared, cache_thread,
collected_addresses, contactgroupmembers, contactgroups, contacts,
dictionary, filestore, identities, responses, searches, session,
system, users
```

### Exact row counts (S1 == S2)

| Table | Rows |
|---|---|
| users | 0 |
| identities | 0 |
| contacts | 0 |
| contactgroups | 0 |
| session | 0 |
| cache / cache_shared / cache_index / cache_thread / cache_messages | 0 |
| dictionary | 0 |
| searches | 0 |
| filestore | 0 |
| responses | 0 |
| collected_addresses | 0 |
| **system** | **1** |

`SELECT user_id, username, mail_host, created, last_login FROM roundcube.users;` returned **no rows** on both servers — no webmail account has ever logged in (matches mailboxes:0 / `/etc/dovecot/users` being empty).

### Schema version
```bash
mysql -N -e "SELECT name, value FROM roundcube.system WHERE name='roundcube-version';"
```
Both servers: `roundcube-version = 2022081200` (the single row in the `system` table). **No drift.**

The `users` / `identities` / `contacts` tables are the data-bearing ones the prompt called out; all three are empty on both servers. If a "Demo SQL Database" is generated, this is the schema it would be writing into — but note Roundcube auto-creates these rows on first webmail login, so seeding them directly is not required for webmail to function.

---

## 3. Accounts, Grants & Authentication Method

### Command
```bash
mysql -N -e "SELECT User, Host, plugin, IF(authentication_string='','(empty)','(set)') AS pw
             FROM mysql.user ORDER BY User, Host;"
# + SHOW GRANTS FOR each account
```

### Accounts (identical roster on both — all `@localhost`)

| User | Host | plugin | password |
|---|---|---|---|
| `mariadb.sys` | localhost | mysql_native_password | (empty) |
| `mysql` | localhost | mysql_native_password | (set) |
| `root` | localhost | mysql_native_password | (set) |
| `roundcube` | localhost | mysql_native_password | (set) |

- **No `%` / wildcard-host accounts.** No remote-capable users. No `debian-sys-maint` (does not exist — `SHOW GRANTS` errored 1141). No SQL roles (`is_role='Y'` returned nothing).
- **No panel-created tenant DB users** — consistent with `databases:0`.

### Authentication method (the important nuance)

`SHOW GRANTS FOR 'root'@'localhost'` (S1 and S2 identical):
```
GRANT ALL PRIVILEGES ON *.* TO `root`@`localhost`
  IDENTIFIED VIA mysql_native_password USING 'invalid' OR unix_socket WITH GRANT OPTION
GRANT PROXY ON ''@'%' TO 'root'@'localhost' WITH GRANT OPTION
```
- `root` and `mysql` both authenticate via **`unix_socket`** (the `mysql_native_password` part is literally `USING 'invalid'` — i.e. password login is impossible; only the local socket as OS-root works). This is why the panel's agent runs `mysql -e "..."` with no credentials: it relies on root socket auth as the panel's OS user. Confirmed in code: `backend/internal/agent/mysql.go` shells `mysql -e` / `mysql -N -e` with no `-u`/`-p`.
- `mariadb.sys`@localhost — internal system account, USAGE only (+ SELECT/DELETE on `mysql.global_priv`), empty password (locked by design).
- **`roundcube`@localhost** — the only real password account:
  ```
  GRANT USAGE ON *.* TO `roundcube`@`localhost` IDENTIFIED BY PASSWORD '<hash>'
  GRANT ALL PRIVILEGES ON `roundcube`.* TO `roundcube`@`localhost`
  ```
  Scoped to the `roundcube` database only — correct least-privilege for webmail.

### DRIFT — roundcube password hash differs between servers
```
S1: IDENTIFIED BY PASSWORD '*47899C9C1854B8EB0951888C9185A69AB5B4E3AE'
S2: IDENTIFIED BY PASSWORD '*B09DD2D2080DAEC4411417D296F63462FA897F82'
```
Each install generated its own `ROUNDCUBE_DB_PASS` (see `install.sh:1268,1279`), so the grant hash and the matching `/etc/roundcube/debian-db.php` differ per box. This is normal for independent installs but is a **migration hazard**: if a transfer copies the Roundcube config (`/etc/roundcube/debian-db.php`, password `$dbpass`) from one server to the other without also re-running the `CREATE USER ... IDENTIFIED BY` grant to match, webmail will fail to connect to its DB. With both `roundcube.users` tables empty, the safest migration path is to leave each server's roundcube credentials self-consistent and not cross-copy.

---

## 4. Network Exposure & Security (3306 localhost-only? YES)

### Commands
```bash
mysql -N -e "SELECT @@bind_address, @@port, @@socket, @@skip_networking;"
ss -ltnp | grep ':3306'
grep -rEn 'bind-address' /etc/mysql/
ufw status
```

### Output (S1 == S2)
```
bind_address=127.0.0.1   port=3306   socket=/run/mysqld/mysqld.sock   skip_networking=0
ss: LISTEN 127.0.0.1:3306  users:(("mariadbd",pid=...,fd=23))
/etc/mysql/mariadb.conf.d/50-server.cnf:27: bind-address = 127.0.0.1
ufw: Status: active   (no 3306 rule present)
```

- **3306 is localhost-only — confirmed three ways** (running `@@bind_address`, the `50-server.cnf` config line, and the `ss` listener showing `127.0.0.1:3306`, never `0.0.0.0`).
- UFW is **active** with no 3306 allow rule — correct; since MariaDB never listens on a public interface, no firewall rule is needed (defence in depth: even if bind-address were changed, UFW would block it).
- `60-galera.cnf` has a commented `#bind-address = 0.0.0.0` (stock Debian default, inert).
- The panel's own remote-access feature (`database_service.go::AddAccessHost` / `agent.AllowPort(... "3306" ...)`) would open UFW + create a host-scoped MySQL user — but **no such access-host has been added** (no remote/wildcard users exist), so 3306 remains fully closed.

Service state: `systemctl is-active mariadb` = **active**, `is-enabled` = **enabled** on both.

---

## 5. Configuration (my.cnf)

`/etc/mysql/` layout is the stock Debian/MariaDB structure, **identical on both servers**:
```
my.cnf -> /etc/alternatives/my.cnf
mariadb.cnf
mariadb.conf.d/{50-client,50-mysql-clients,50-mysqld_safe,50-server,60-galera, provider_*}.cnf
```
- No custom/hand-edited tuning files. `max_connections`, `innodb_buffer_pool_size`, `datadir` are all left at the commented defaults in `50-server.cnf` (defaults: datadir `/var/lib/mysql`, buffer pool MariaDB default ~128M).
- `debian.cnf` exists (mode 600) but the `debian-sys-maint` account it references does not exist in `mysql.user` — harmless, the file is unused.

**No configuration drift between S1 and S2.**

---

## 6. Where does app / mail data actually live? (MariaDB vs Mongo vs files)

This was the key question. **Confirmed: NO application or mail data is in MariaDB.**

**Mail accounts — Dovecot uses passwd-file, not SQL.** Effective config via `doveconf -n` (S1, S2 identical):
```
passdb { driver = passwd-file
         args = scheme=SHA512-CRYPT username_format=%u /etc/dovecot/users }
userdb { driver = passwd-file
         args = username_format=%u /etc/dovecot/users
         default_fields = uid=5000 gid=5000 home=/var/mail/vhosts/%d/%n }
```
- `/etc/dovecot/users` is **0 bytes (empty)** on both — matches `mailboxes:0`.
- `/etc/dovecot/dovecot-sql.conf.ext` exists but is a **stock, inert** package file: `driver` and `connect` are commented out (`grep` for active `driver=`/`connect=` returned nothing). The `auth-sql.conf.ext` is present in `conf.d` but is **not** the active passdb/userdb (`doveconf -n` shows only passwd-file). So Dovecot does NOT touch MariaDB.

**Postfix routing — not SQL-backed.** `grep 'mysql:' /etc/postfix/main.cf` returned nothing — no `mysql:` lookup tables; virtual maps are file/hash based.

**Panel entities — MongoDB, not MariaDB.** Panel `.env` has no MySQL URI or credentials (`grep -iE 'mysql|mariadb|3306' /opt/serverpanel/.env` → none). The panel only reaches MariaDB on demand, by shelling `mysql -e` as socket-root through the agent (`agent/mysql.go`); it has no persistent MySQL connection. Confirmed at runtime: `ss -tnp | grep :3306` showed **no established connections** to MariaDB on either server.

**Net:** MariaDB on these boxes serves exactly one purpose — backing the (empty) Roundcube webmail schema. Domains, mailboxes, DNS, forwarders, apps, and panel-managed "databases" all live in MongoDB (or, for mail accounts, flat passwd-files). This means a "Demo SQL Database" feature would write a brand-new, panel-provisioned MySQL DB (via `agent.CreateMySQLDatabase` → `CREATE DATABASE` + `CreateMySQLUserWithRole`), independent of Roundcube and independent of Mongo's `databases` collection metadata.

### How the panel manages user MySQL databases (code review)

- `backend/internal/handlers/database_handler.go` — thin Fiber handlers; create/list/delete DBs, manage DB users, password/role updates, connection-info, phpMyAdmin auto-login, and remote-access hosts.
- `backend/internal/services/database_service.go`:
  - `Create()` resolves a vendor/tenant **prefix** (`<user>_<dbname>`, classic cPanel style) and, for `type:"mysql"`, calls `agent.CreateMySQLDatabase` then `agent.CreateMySQLUserWithRole(...,"dbOwner")`. Metadata (incl. plaintext password + connection string) is stored in Mongo `databases` + `database_users` collections — **not** in MariaDB.
  - `GetConnectionInfo()` rewrites the advertised host to the public IP (via `SetPublicHosts`) while internal ops keep using `localhost`/127.0.0.1.
  - `AddAccessHost()` is the remote-access path that would create a `user@<host>` grant and open UFW 3306 — currently unused (no such users exist).
- `backend/internal/agent/mysql.go` — the actual SQL executor. **Security-relevant:** post-3.1.108 hardening validates identifiers against `^[A-Za-z0-9_]{1,64}$`, whitelists hosts, and backslash-escapes password literals before `fmt.Sprintf` into `mysql -e "..."` (fixes a prior SQL-injection-as-root via crafted DB/user/password). Roles map to privilege sets (`read`, `readWrite`, `dbAdmin`, `userAdmin`/`dbOwner`=ALL PRIVILEGES). All statements run as socket-root.

---

## 7. Backups

### Command
```bash
ls -la /var/backups/
find /var/backups /opt /root /backup -type f \( -name '*.sql' -o -name '*.sql.gz' ... \)
{ crontab -l; cat /etc/cron.d/*; } | grep -iE 'mysql|mariadb|dump|backup'
which automysqlbackup mariadb-backup mariabackup xtrabackup
```

### Findings (S1 == S2)
- **No SQL dumps anywhere** — `find` for `*.sql` / `*.sql.gz` / `*.xb*` under common backup dirs returned **nothing**.
- **No backup tooling installed** — `automysqlbackup`, `mariadb-backup`, `mariabackup`, `xtrabackup` all absent.
- The only DB-touching cron is Roundcube's own housekeeping (not a backup):
  ```
  0 5 * * *  www-data ... /usr/share/roundcube/bin/cleandb.sh
  5,35 * * * * www-data ... /usr/share/roundcube/bin/gc.sh
  ```
- `/var/backups/` contains only dpkg/alternatives system files (and a `serverpanel/` dir) — no MariaDB content.

**Conclusion: there is no MariaDB backup strategy on either server.** Low data-loss impact today (the only SQL data is an empty Roundcube schema that the package recreates), but this is a real gap once webmail accumulates contacts/identities/preferences.

---

## 8. Drift Summary (S1 vs S2)

| Aspect | Drift? | Detail |
|---|---|---|
| Engine version | No | 10.11.14 both |
| Database list | No | roundcube + system DBs both |
| Roundcube schema / version | No | 17 tables, all empty, version 2022081200 both |
| Account roster | No | root, mysql, mariadb.sys, roundcube (@localhost) both |
| Auth method | No | socket-auth root; password only for roundcube |
| `roundcube` password hash | **YES** | S1 `*47899C...` vs S2 `*B09DD2...` (per-install secret; migration hazard if config is cross-copied) |
| bind-address / port / 3306 exposure | No | 127.0.0.1:3306 only, both |
| my.cnf / config files | No | stock Debian layout, no custom tuning, both |
| Backups | No (both absent) | neither server has any MariaDB backups |
| Mail backend | No | Dovecot passwd-file (empty), not SQL, both |

Aside from the per-install Roundcube DB password (expected, but flagged for migration), S1 and S2 are byte-for-byte equivalent at the MariaDB layer.

---

## Appendix — Evidence index (exact commands)
All commands run read-only via the SSH helper `bz.py` (`1` = S1, `2` = S2). Multi-line scripts staged in scratchpad and run with `--file`:
- `rc_schema.sh` — roundcube tables/rows/version
- `users_grants.sh` — mysql.user, SHOW GRANTS, bind vars
- `config_net.sh` — config files, bind-address, ss, ufw, datadir size
- `backups_mail.sh` — backup search, cron, Dovecot/Postfix backends
- `dovecot_active.sh` + `doveconf -n` — effective Dovecot passdb/userdb, live 3306 connections, panel .env mysql check
