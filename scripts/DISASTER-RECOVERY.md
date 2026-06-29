# Disaster Recovery — Betazen Server Panel

Two backup systems exist; don't confuse them:

| | What it captures | Use |
|---|---|---|
| **In-panel `/backups`** | one website (files / DB / email / config) | per-site rollback |
| **Whole-server DR** (this doc) | the **entire box** incl. the panel's own brain | rebuild on a new server |

`install.sh` can reinstall the *software* on a fresh box, but it cannot
recreate the *state* — the `serverpanel` MongoDB DB, `/opt/serverpanel/.env`
secrets (`APP_ENCRYPTION_KEY`!), hosted sites, MySQL, mailboxes, DKIM keys,
PowerDNS zones and TLS certs. The DR scripts do.

## What a DR bundle contains

`mongodump serverpanel` · `/opt/serverpanel/.env` · `/home` · `mysqldump
--all-databases` · `/var/mail/vhosts` + `/var/vmail` + `/etc/dovecot/users` ·
`/etc/opendkim` · `/var/lib/powerdns/pdns.sqlite3` + `pdns.conf` ·
`/etc/letsencrypt` (whole tree) · nginx / php-fpm / postfix / roundcube /
phpmyadmin / pure-ftpd configs · PM2 dump + `sp-app-*.service` units.
A `manifest.json` records the **old server IP** for the restore-time rewrite.

The bundle is AES-256 encrypted with `BACKUP_ENCRYPTION_KEY` from
`/opt/serverpanel/.env` — **back that key up separately**, or an encrypted
bundle (and the secrets inside the restored Mongo) is unrecoverable.

## Setup (already wired by install.sh)

A daily `bzpanel-backup.timer` runs `bzpanel-backup` at ~03:17. Configure
destinations + retention in **`/etc/bzpanel/backup.conf`**:

```sh
BZ_BACKUP_RCLONE_REMOTE=gdrive:bzpanel-backups   # rclone config first: `rclone config`
BZ_BACKUP_RETENTION_COUNT=7
```

`rclone` covers Google Drive, S3, Backblaze B2, FTP and SFTP from one config.
Test a run now: `bzpanel-backup --dry-run`.

## Restore onto a NEW server

```sh
# 1. fresh install (rebuilds the software stack)
bash install.sh

# 2. restore state + rewrite old IP → this box's IP
bzpanel-restore --rclone gdrive:bzpanel-backups          # pulls the newest bundle
#   or: bzpanel-restore --bundle /path/bzpanel-dr-*.tar.gz.enc
```

The restore: loads the panel DB with **this** box's Mongo credentials, restores
the OLD `.env` secrets (keeping the new Mongo connection), restores MySQL / mail
/ DKIM / DNS / TLS / configs, then runs `bzpanel reassign-ip <old> <new>` which
rewrites A + **AAAA** records, SPF `ip4:`, the DB mirrors, `.env`, the panel
nginx vhost and **pure-ftpd ForcePassiveIP**.

## Manual steps the panel can't do

- **PTR / rDNS** and **registrar glue** live at your provider — update them there.
- Verify mailbox login (Dovecot) and that published **DKIM** TXT still matches
  `/etc/opendkim/keys` after restore.
