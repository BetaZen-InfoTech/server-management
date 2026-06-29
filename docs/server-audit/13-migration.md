# 13 — Migration Report (Server 1 → Server 2)

**Date:** 2026-06-29
**Source:** `89.116.34.207` (S1) · **Destination:** `195.35.7.64` (S2)
**Method:** the panel's **native server transfer**, driven from the **destination** (S2 SSH-pulls
from S1 over an encrypted channel). Panel build on both boxes: **v3.1.112**.

## 1. Pre-flight (safety first)

Before any change, Server 2 was fully backed up:

```
/var/backups/premigration-20260629-145010/
  ├── serverpanel.archive   (mongodump --gzip of the panel DB)
  ├── mysql-all.sql.gz       (mysqldump --all-databases, 516K)
  └── configs.tgz            (/etc/postfix, /etc/dovecot/users, /etc/opendkim,
                              powerdns sqlite, nginx sites, .env — 60K)
```

S2 baseline recorded: 6 domains, 84 dns_records, 42 mailboxes, 36 forwarders, 3 MySQL DBs,
serving its own IP `195.35.7.64`.

## 2. Connectivity + discovery (read-only)

- `POST /whm/transfers/discover` (token-auth, SSH to S1) → **succeeded**, inventorying the source:
  6 domains, 6 DNS zones, 6 email domains, MySQL `demotwo_appdb` + `roundcube`, 6 FTP users,
  node apps `demo-crm`/`demo-erp`/`demo-cms`/`node-sample`/`flask-sample`. This alone proves the
  transfer path is fully operational over SSH.

## 3. Native transfer

`POST /whm/transfers` with components enabled: **domains, files, dns, ssl, databases,
email_data, ftp_accounts, cron_jobs, node_apps, ssh_keys, packages**. `hostname`, `firewall`,
`server_config`, `software` were **disabled** so S2 keeps its own panel identity (its own IP,
panel domain, and secrets); the per-zone A-record rewrite still repoints migrated DNS to S2's IP.

**Result — transfer `6a4287222d1e5bec41138630`: `status=completed`, `progress=100`,
14/14 steps completed, ZERO failures.**

```
✅ Validate Connection      ✅ Transfer SSL Certificates   ✅ Transfer Node.js Apps
✅ Discover Resources       ✅ Transfer Databases          ✅ Transfer SSH Keys
✅ Transfer Packages        ✅ Transfer Email              ✅ Transfer Cron Jobs
✅ Transfer Domains & Files ✅ Transfer FTP Accounts       ✅ Sync Panel Records
✅ Transfer DNS Zones       ✅ Verify Transfer
```

(One more step than the 2026-06-28 run's 13/13 — `Transfer Cron Jobs` was additionally enabled this pass.)

> Operational note: the transfer runs **asynchronously** inside the panel. The first poll client
> hit a 10-minute wall-clock cap and was terminated, but the transfer itself completed server-side;
> status was confirmed via `GET /whm/transfers`.

## 4. Post-transfer rehydrate

`POST /whm/transfers/rehydrate-all` rebuilt all filesystem/DB/DNS/mail state from the synced
Mongo records — **0 failed**:

```
mailboxes  42/42 healed      forwarders 36/36 healed
dns        6 zones, 72 rrsets re-emitted via pdnsutil (deduplicated)
mysql      3/3 healed         ssh_keys 0     ftp 6 (already present)
```

## 5. What migrated

Applications (6 + their systemd units), Mail server (Postfix/Dovecot config + 42 mailboxes +
**maildir message data** + 36 forwarders + DKIM keys), MongoDB panel records (domains, DNS,
mailboxes, forwarders, FTP, packages…), SQL (`demotwo_appdb` + `roundcube` + `demoone_appdata`),
Domains (6), DNS (6 zones), Nginx vhosts, FTP accounts, `/home` uploads & website files, SSH
keys, hosting packages, cron jobs, and AES-secret re-encryption under the destination's key.
**No data loss.**

## 6. Known characteristics (not failures)

- **S2's DNS ends cleaner than S1's** (84 vs 119 records): `rehydrate-all`'s `replace-rrset`
  collapses the duplicate SPF/DMARC rows that S1 still carries. S2 also correctly serves **its own
  IP** (`195.35.7.64`) post-rewrite, whereas S1 has a pre-existing stale-IP quirk (see report 15).
- **Mail Suite** (`/opt/serverpanel/mail-suite`) is a source-only subproject, not deployed on
  either box — nothing to migrate; parity preserved.

See report **14** for full post-migration verification.
