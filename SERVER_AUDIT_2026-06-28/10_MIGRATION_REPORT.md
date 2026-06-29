# 10 — Migration Report (Server 1 → Server 2)

**Source:** `89.116.34.207` · **Destination:** `195.35.7.64`
**Method:** the panel's **native server transfer** (run on the destination, SSH-pull from source),
driven over the encrypted SSH channel (destination `localhost:8080`) so the root password never
crossed the internet in cleartext. Supplemented by two manual steps the native transfer doesn't
auto-cover.

## 1. Pre-flight

- `POST /whm/transfers/test-connection` (ssh, root) → **Connection successful**.
- `POST /whm/transfers/discover` → source detected as `serverpanel`; found 6 domains, 6 DNS zones,
  6 email domains, 2 MySQL DBs (`demotwo_appdb`, `roundcube`), 6 FTP users, 3 Linux users, 3 node apps.
- Destination baseline confirmed empty (2 users, 0 domains/mailboxes/etc.).
- Pre-staged Go/Node/Python on the destination (Go PATH symlink) so migrated apps rebuild.

## 2. Native transfer

`POST /whm/transfers` with components: domains, files, DNS, SSL, databases, email_data,
ftp_accounts, node_apps, ssh_keys, packages (selection = all). `hostname`, `firewall` and
`server_config` were intentionally **disabled** so the destination keeps its own panel identity
(its own IP/panel domain/secrets) — the per-zone A-record rewrite still repoints migrated DNS to
the destination IP.

**Result: status=completed, progress=100, 13/13 steps completed, ZERO warnings/errors:**

```
✅ Validate Connection      ✅ Transfer SSL Certificates   ✅ Transfer SSH Keys
✅ Discover Resources       ✅ Transfer Databases          ✅ Sync Panel Records
✅ Transfer Packages        ✅ Transfer Email              ✅ Verify Transfer
✅ Transfer Domains & Files ✅ Transfer FTP Accounts
✅ Transfer DNS Zones       ✅ Transfer Node.js Apps
```

## 3. Post-transfer rehydrate (`POST /whm/transfers/rehydrate-all`)

Rebuilt destination filesystem/DB/DNS state from synced Mongo, all healed, 0 failed:
`mailboxes 42/42 · forwarders 36/36 · dns 6 zones + 72 rrsets · mysql 2/2`.

## 4. Manual migration (items outside the native transfer's scope)

1. **Demo MongoDB `demoone_appdata`** — the transfer's DB discovery only enumerates MySQL, so this
   panel-managed Mongo DB was migrated manually: `mongodump` (S1) → re-provision the DB+user via the
   panel API on S2 → `mongorestore` (**109 documents restored, 0 failed**).
2. **Standalone systemd demo services** (`betazen-demo-*`, not panel-managed) — re-created on S2 via
   the same idempotent installer; all 6 active+enabled, health + restart verified.

## 5. In-flight bug found & fixed during migration

The Go app `demo-erp` crash-looped on S2: the post-transfer app recovery rebuilt its systemd unit
**without** `Environment=PORT=8091`, so the Go binary fell back to `:8080` and collided with the
panel. Root-caused to `recoverApp` (transfer_panel_records.go) omitting the PORT env that the
original deploy stamps. **Fixed in code (v3.1.111, redeployed to both servers)** and the running
unit repaired; demo-erp now serves on :8091 via its domain. (See `12`/`13`.)

## 6. What migrated

Applications (6), Mail server (Postfix/Dovecot config + mailboxes + **maildir message data** +
forwarders + DKIM keys), MongoDB (panel `serverpanel` DB via record-sync + demo `demoone_appdata`),
SQL (`demotwo_appdb` + roundcube), Domains (6), DNS (114 records), SSL config, Nginx vhosts,
systemd app+demo services, FTP accounts, /home uploads & website files, SSH keys, hosting packages,
and AES-secret re-encryption under the destination key. **No data loss.**

Mail Suite: not deployed on either server (source-only repo subproject) — nothing to migrate;
parity preserved (see `reports/mail-suite.md`).
