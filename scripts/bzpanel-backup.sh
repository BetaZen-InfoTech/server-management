#!/usr/bin/env bash
#
# bzpanel-backup.sh — whole-server disaster-recovery backup for the
# Betazen Server Panel.
#
# Unlike the in-panel per-domain "Backups" feature (one website at a time),
# this captures EVERYTHING needed to rebuild a dead server on a fresh box:
#
#   * the panel's brain      — the `serverpanel` MongoDB database (whole DB)
#   * the panel's secrets    — /opt/serverpanel/.env (APP_ENCRYPTION_KEY etc.)
#   * hosted sites + mail    — /home (owned-domain maildirs live here too)
#   * MySQL/MariaDB          — every database incl. roundcube
#   * mail fallback + auth   — /var/mail/vhosts, /var/vmail, /etc/dovecot/users
#   * DKIM                   — /etc/opendkim (keys + signing/key tables)
#   * authoritative DNS      — /var/lib/powerdns/pdns.sqlite3 + pdns.conf
#   * TLS                    — the whole /etc/letsencrypt tree
#   * web/mail config        — nginx, php-fpm, postfix, roundcube, phpmyadmin,
#                              pure-ftpd (incl. its TLS cert)
#   * app runtime            — PM2 dump + sp-app-*.service units
#
# The bundle is optionally AES-256 encrypted (BACKUP_ENCRYPTION_KEY), shipped
# off-site (local dir + rclone remote + direct FTP), and pruned by retention.
# A manifest.json records the OLD server IP so the restore side can run
# `bzpanel reassign-ip <old> <new>` automatically.
#
# Usage:
#   bzpanel-backup.sh [--dry-run] [--no-encrypt] [--keep N]
#
# Configuration is read from (later wins):
#   1. /opt/serverpanel/.env         (MONGO_URI, SERVER_IP, BACKUP_ENCRYPTION_KEY)
#   2. /etc/bzpanel/backup.conf       (destinations + retention — see below)
#   3. environment / CLI flags
#
# /etc/bzpanel/backup.conf keys (all optional):
#   BZ_BACKUP_LOCAL_DIR=/var/backups/bzpanel
#   BZ_BACKUP_RCLONE_REMOTE=gdrive:bzpanel        # any rclone remote: drive/s3/b2/ftp/sftp
#   BZ_BACKUP_FTP_URL=ftp://host/path             # direct curl FTP (no rclone)
#   BZ_BACKUP_FTP_USER=...                         #   "
#   BZ_BACKUP_FTP_PASS=...                         #   "
#   BZ_BACKUP_RETENTION_COUNT=7                    # keep newest N (0 = unlimited)
#   BZ_BACKUP_RETENTION_DAYS=0                     # also delete older than N days (0 = off)
#   BZ_BACKUP_ENCRYPT=1                            # 0 to disable even when a key is set
#
set -euo pipefail

# ----------------------------------------------------------------------------
# constants / defaults
# ----------------------------------------------------------------------------
ENV_FILE="${BZ_ENV_FILE:-/opt/serverpanel/.env}"
CONF_FILE="${BZ_BACKUP_CONF:-/etc/bzpanel/backup.conf}"
WORK_ROOT="${BZ_BACKUP_WORK_DIR:-/var/backups/bzpanel/work}"
LOCAL_DIR_DEFAULT="/var/backups/bzpanel"
LOG_TAG="bzpanel-backup"

DRY_RUN=0
ENCRYPT_OVERRIDE=""      # "" = use config, 0 = force off
KEEP_OVERRIDE=""

log()  { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*"; logger -t "$LOG_TAG" -- "$*" 2>/dev/null || true; }
warn() { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] WARN: $*" >&2; logger -t "$LOG_TAG" -p user.warning -- "$*" 2>/dev/null || true; }
die()  { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] FATAL: $*" >&2; logger -t "$LOG_TAG" -p user.err -- "$*" 2>/dev/null || true; exit 1; }

# getenvval KEY FILE — read KEY=VALUE from a dotenv-style file without sourcing
# it (avoids executing arbitrary content). Strips surrounding quotes.
getenvval() {
  local key="$1" file="$2" line
  [ -f "$file" ] || return 0
  line="$(grep -E "^[[:space:]]*${key}=" "$file" 2>/dev/null | tail -n1 || true)"
  [ -n "$line" ] || return 0
  line="${line#*=}"
  line="${line%$'\r'}"                       # strip CR if the file is CRLF
  line="${line#\"}"; line="${line%\"}"
  line="${line#\'}"; line="${line%\'}"
  printf '%s' "$line"
}

# capture SRC DESTSUBDIR [tar|copy] — record a component into the stage. Missing
# sources warn and are skipped (a fresh-ish box may legitimately lack some).
have_any=0
capture_dir() {  # capture_dir <src-dir> <stage-subpath>
  local src="$1" sub="$2"
  if [ ! -e "$src" ]; then warn "skip (absent): $src"; return 0; fi
  mkdir -p "$STAGE/$(dirname "$sub")"
  if cp -a "$src" "$STAGE/$sub" 2>/dev/null; then
    have_any=1; log "captured $src"
  else
    warn "failed to capture $src"
  fi
}

# ----------------------------------------------------------------------------
# args
# ----------------------------------------------------------------------------
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run)    DRY_RUN=1 ;;
    --no-encrypt) ENCRYPT_OVERRIDE=0 ;;
    --keep)       KEEP_OVERRIDE="${2:-}"; shift ;;
    --keep=*)     KEEP_OVERRIDE="${1#*=}" ;;
    -h|--help)    sed -n '2,40p' "$0"; exit 0 ;;
    *)            die "unknown argument: $1" ;;
  esac
  shift
done

[ "$(id -u)" -eq 0 ] || die "must run as root (needs read access to /etc, /home, mysql socket)"

# ----------------------------------------------------------------------------
# load configuration
# ----------------------------------------------------------------------------
# shellcheck disable=SC1090
[ -f "$CONF_FILE" ] && . "$CONF_FILE"

MONGO_URI="$(getenvval MONGO_URI "$ENV_FILE")"
MONGO_DB="$(getenvval MONGO_DB_NAME "$ENV_FILE")"; MONGO_DB="${MONGO_DB:-serverpanel}"
SERVER_IP="$(getenvval SERVER_IP "$ENV_FILE")"
[ -n "$SERVER_IP" ] || SERVER_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
ENC_KEY="$(getenvval BACKUP_ENCRYPTION_KEY "$ENV_FILE")"

LOCAL_DIR="${BZ_BACKUP_LOCAL_DIR:-$LOCAL_DIR_DEFAULT}"
RCLONE_REMOTE="${BZ_BACKUP_RCLONE_REMOTE:-}"
FTP_URL="${BZ_BACKUP_FTP_URL:-}"
FTP_USER="${BZ_BACKUP_FTP_USER:-}"
FTP_PASS="${BZ_BACKUP_FTP_PASS:-}"
RETENTION_COUNT="${BZ_BACKUP_RETENTION_COUNT:-7}"
RETENTION_DAYS="${BZ_BACKUP_RETENTION_DAYS:-0}"
ENCRYPT="${BZ_BACKUP_ENCRYPT:-1}"
[ -n "$KEEP_OVERRIDE" ] && RETENTION_COUNT="$KEEP_OVERRIDE"
[ "$ENCRYPT_OVERRIDE" = "0" ] && ENCRYPT=0

# A blank or placeholder key disables encryption rather than producing a
# bundle "encrypted" with a guessable secret.
case "$ENC_KEY" in
  ""|"change-me-to-a-random-encryption-key") [ "$ENCRYPT" = "1" ] && warn "BACKUP_ENCRYPTION_KEY unset/placeholder — writing UNENCRYPTED bundle"; ENCRYPT=0 ;;
esac
command -v openssl >/dev/null 2>&1 || { [ "$ENCRYPT" = "1" ] && warn "openssl missing — writing UNENCRYPTED bundle"; ENCRYPT=0; }

TS="$(date -u '+%Y%m%d-%H%M%S')"
HOSTNAME_FQDN="$(hostname -f 2>/dev/null || hostname)"
BUNDLE_BASE="bzpanel-dr-${HOSTNAME_FQDN}-${TS}"
STAGE="$WORK_ROOT/$BUNDLE_BASE"

log "=== Betazen DR backup start === host=$HOSTNAME_FQDN ip=${SERVER_IP:-unknown} encrypt=$ENCRYPT dry_run=$DRY_RUN"
mkdir -p "$STAGE" "$LOCAL_DIR"
cleanup() { rm -rf "$STAGE" 2>/dev/null || true; }
trap cleanup EXIT

# ----------------------------------------------------------------------------
# 1. panel brain — the WHOLE serverpanel MongoDB database
# ----------------------------------------------------------------------------
mkdir -p "$STAGE/mongo"
if command -v mongodump >/dev/null 2>&1; then
  if [ -n "$MONGO_URI" ]; then
    if mongodump --uri="$MONGO_URI" --gzip --archive="$STAGE/mongo/${MONGO_DB}.archive.gz" >/dev/null 2>&1; then
      have_any=1; log "captured MongoDB ($MONGO_DB) via URI"
    else
      warn "mongodump via URI failed; retrying localhost"
      mongodump --db "$MONGO_DB" --gzip --archive="$STAGE/mongo/${MONGO_DB}.archive.gz" >/dev/null 2>&1 \
        && { have_any=1; log "captured MongoDB ($MONGO_DB) via localhost"; } \
        || warn "mongodump failed — panel DB NOT captured"
    fi
  else
    mongodump --db "$MONGO_DB" --gzip --archive="$STAGE/mongo/${MONGO_DB}.archive.gz" >/dev/null 2>&1 \
      && { have_any=1; log "captured MongoDB ($MONGO_DB) via localhost"; } \
      || warn "mongodump failed — panel DB NOT captured"
  fi
else
  warn "mongodump not installed — panel DB NOT captured"
fi

# ----------------------------------------------------------------------------
# 2. panel secrets — .env (APP_ENCRYPTION_KEY decrypts Mongo secrets at rest;
#    lose it and the restored DB's PATs/SMTP creds are permanently unreadable)
# ----------------------------------------------------------------------------
capture_dir "$ENV_FILE" "secrets/.env"

# ----------------------------------------------------------------------------
# 3. hosted sites + owned-domain mail (/home/<owner>/mail/... lives here)
# ----------------------------------------------------------------------------
mkdir -p "$STAGE/home"
if tar -czf "$STAGE/home/home.tar.gz" -C / home 2>/dev/null; then
  have_any=1; log "captured /home"
else
  warn "tar /home reported errors (continuing — partial archive kept)"
fi

# ----------------------------------------------------------------------------
# 4. MySQL / MariaDB — every database (sites + roundcube)
# ----------------------------------------------------------------------------
mkdir -p "$STAGE/mysql"
if command -v mysqldump >/dev/null 2>&1; then
  if mysqldump --single-transaction --routines --triggers --events \
        --all-databases 2>/dev/null | gzip > "$STAGE/mysql/all-databases.sql.gz"; then
    if [ -s "$STAGE/mysql/all-databases.sql.gz" ]; then
      have_any=1; log "captured MySQL (all databases)"
    else
      warn "mysqldump produced an empty file — MySQL NOT captured"; rm -f "$STAGE/mysql/all-databases.sql.gz"
    fi
  else
    warn "mysqldump failed — MySQL NOT captured"
  fi
else
  warn "mysqldump not installed — skipping MySQL"
fi

# ----------------------------------------------------------------------------
# 5. mail fallback dirs + Dovecot auth (the SHA512 password file — without it
#    no mailbox can log in even though the maildirs restore fine)
# ----------------------------------------------------------------------------
capture_dir /var/mail/vhosts            "mail/vhosts"
capture_dir /var/vmail                  "mail/vmail"
capture_dir /etc/dovecot/users          "mail/dovecot-users"
capture_dir /etc/dovecot/conf.d/99-panel.conf "mail/99-panel.conf"

# ----------------------------------------------------------------------------
# 6. DKIM keys + tables (lose these → DKIM breaks for every domain)
# ----------------------------------------------------------------------------
capture_dir /etc/opendkim               "dkim/opendkim"

# ----------------------------------------------------------------------------
# 7. PowerDNS — authoritative zone store (SQLite) + config (holds API key)
# ----------------------------------------------------------------------------
mkdir -p "$STAGE/dns"
if [ -f /var/lib/powerdns/pdns.sqlite3 ]; then
  # snapshot the live DB consistently rather than copying a file mid-write
  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 /var/lib/powerdns/pdns.sqlite3 ".backup '$STAGE/dns/pdns.sqlite3'" 2>/dev/null \
      && { have_any=1; log "captured PowerDNS sqlite (consistent snapshot)"; } \
      || capture_dir /var/lib/powerdns/pdns.sqlite3 "dns/pdns.sqlite3"
  else
    capture_dir /var/lib/powerdns/pdns.sqlite3 "dns/pdns.sqlite3"
  fi
fi
capture_dir /etc/powerdns/pdns.conf     "dns/pdns.conf"
capture_dir /etc/powerdns/pdns.d        "dns/pdns.d"

# ----------------------------------------------------------------------------
# 8. TLS — the whole Let's Encrypt tree (live + archive + renewal + account)
# ----------------------------------------------------------------------------
capture_dir /etc/letsencrypt            "ssl/letsencrypt"
capture_dir /etc/ssl/private/pure-ftpd.pem "ssl/pure-ftpd.pem"

# ----------------------------------------------------------------------------
# 9. web / mail / ftp configuration
# ----------------------------------------------------------------------------
capture_dir /etc/nginx/sites-available  "config/nginx/sites-available"
capture_dir /etc/nginx/snippets         "config/nginx/snippets"
capture_dir /etc/php                     "config/php"          # pool.d configs (and any ini overrides)
capture_dir /etc/postfix                "config/postfix"
capture_dir /etc/roundcube              "config/roundcube"
capture_dir /etc/phpmyadmin             "config/phpmyadmin"
capture_dir /etc/pure-ftpd              "config/pure-ftpd"

# ----------------------------------------------------------------------------
# 10. app runtime — PM2 dump + panel-managed app units
# ----------------------------------------------------------------------------
capture_dir /root/.pm2/dump.pm2         "apps/pm2-dump.pm2"
mkdir -p "$STAGE/apps/systemd"
for unit in /etc/systemd/system/sp-app-*.service; do
  [ -e "$unit" ] || continue
  cp -a "$unit" "$STAGE/apps/systemd/" 2>/dev/null && have_any=1
done

[ "$have_any" -eq 1 ] || die "nothing was captured — refusing to write an empty backup"

# ----------------------------------------------------------------------------
# manifest — restore side reads server_ip to drive `bzpanel reassign-ip`
# ----------------------------------------------------------------------------
manifest_components() { ( cd "$STAGE" && find . -maxdepth 2 -mindepth 1 -type d -printf '"%P",' ) | sed 's/,$//'; }
cat > "$STAGE/manifest.json" <<JSON
{
  "schema": 1,
  "product": "Betazen Server Panel",
  "created_at": "$(date -u '+%Y-%m-%dT%H:%M:%SZ')",
  "hostname": "$HOSTNAME_FQDN",
  "server_ip": "${SERVER_IP:-}",
  "mongo_db": "$MONGO_DB",
  "encrypted": $( [ "$ENCRYPT" = "1" ] && echo true || echo false ),
  "components": [ $(manifest_components) ]
}
JSON
log "wrote manifest (server_ip=${SERVER_IP:-unknown})"

# ----------------------------------------------------------------------------
# bundle + optional encryption
# ----------------------------------------------------------------------------
RAW_BUNDLE="$WORK_ROOT/${BUNDLE_BASE}.tar.gz"
log "bundling → $(basename "$RAW_BUNDLE")"
tar -czf "$RAW_BUNDLE" -C "$WORK_ROOT" "$BUNDLE_BASE"

if [ "$ENCRYPT" = "1" ]; then
  FINAL="$LOCAL_DIR/${BUNDLE_BASE}.tar.gz.enc"
  # AES-256-CBC + PBKDF2 salted. Decrypt with the same key:
  #   openssl enc -d -aes-256-cbc -pbkdf2 -salt -in <file> -pass pass:$KEY | tar xz
  openssl enc -aes-256-cbc -pbkdf2 -salt -in "$RAW_BUNDLE" -out "$FINAL" -pass pass:"$ENC_KEY" \
    || die "encryption failed"
  rm -f "$RAW_BUNDLE"
else
  FINAL="$LOCAL_DIR/${BUNDLE_BASE}.tar.gz"
  mv "$RAW_BUNDLE" "$FINAL"
fi
chmod 600 "$FINAL"
SHA="$(sha256sum "$FINAL" | awk '{print $1}')"
SIZE="$(du -h "$FINAL" | awk '{print $1}')"
echo "$SHA  $(basename "$FINAL")" > "${FINAL}.sha256"
# Sidecar manifest alongside the bundle so operators can read server_ip
# without decrypting the whole archive.
cp "$STAGE/manifest.json" "${FINAL}.manifest.json"
log "bundle ready: $(basename "$FINAL") ($SIZE, sha256=${SHA:0:12}…)"

if [ "$DRY_RUN" = "1" ]; then
  log "--dry-run: kept local bundle, skipping off-site upload + retention"
  trap - EXIT; cleanup
  exit 0
fi

# ----------------------------------------------------------------------------
# off-site shipping
# ----------------------------------------------------------------------------
ship_ok=1
if [ -n "$RCLONE_REMOTE" ]; then
  if command -v rclone >/dev/null 2>&1; then
    log "uploading to rclone remote: $RCLONE_REMOTE"
    if rclone copy "$FINAL" "$RCLONE_REMOTE/" --no-traverse 2>&1 | logger -t "$LOG_TAG" 2>/dev/null; then
      rclone copy "${FINAL}.sha256" "$RCLONE_REMOTE/" --no-traverse >/dev/null 2>&1 || true
      rclone copy "${FINAL}.manifest.json" "$RCLONE_REMOTE/" --no-traverse >/dev/null 2>&1 || true
      log "rclone upload complete"
    else
      warn "rclone upload failed"; ship_ok=0
    fi
  else
    warn "BZ_BACKUP_RCLONE_REMOTE set but rclone is not installed (run: curl https://rclone.org/install.sh | bash)"; ship_ok=0
  fi
fi

if [ -n "$FTP_URL" ]; then
  log "uploading via FTP: $FTP_URL"
  if curl -fsS -T "$FINAL" --user "$FTP_USER:$FTP_PASS" --ftp-create-dirs --connect-timeout 30 "${FTP_URL%/}/$(basename "$FINAL")"; then
    log "FTP upload complete"
  else
    warn "FTP upload failed"; ship_ok=0
  fi
fi

# ----------------------------------------------------------------------------
# retention — local dir and rclone remote
# ----------------------------------------------------------------------------
prune_local() {
  [ "$RETENTION_COUNT" -gt 0 ] 2>/dev/null || return 0
  # newest-first; delete everything past N (and their sidecars)
  local n=0 f
  ls -1t "$LOCAL_DIR"/bzpanel-dr-*.tar.gz* 2>/dev/null | grep -vE '\.(sha256|manifest\.json)$' | while read -r f; do
    n=$((n+1))
    if [ "$n" -gt "$RETENTION_COUNT" ]; then
      rm -f "$f" "${f}.sha256" "${f}.manifest.json"
      log "retention: pruned local $(basename "$f")"
    fi
  done
  if [ "${RETENTION_DAYS:-0}" -gt 0 ] 2>/dev/null; then
    find "$LOCAL_DIR" -maxdepth 1 -name 'bzpanel-dr-*' -type f -mtime "+$RETENTION_DAYS" -print -delete \
      | sed 's/^/retention: pruned old /' | while read -r l; do log "$l"; done || true
  fi
}
prune_rclone() {
  [ -n "$RCLONE_REMOTE" ] || return 0
  command -v rclone >/dev/null 2>&1 || return 0
  [ "$RETENTION_COUNT" -gt 0 ] 2>/dev/null || return 0
  # keep newest N *.enc/.tar.gz bundles on the remote
  local keep="$RETENTION_COUNT" n=0 name
  rclone lsf "$RCLONE_REMOTE/" 2>/dev/null | grep -E '^bzpanel-dr-.*\.tar\.gz(\.enc)?$' | sort -r | while read -r name; do
    n=$((n+1))
    if [ "$n" -gt "$keep" ]; then
      rclone delete "$RCLONE_REMOTE/$name" >/dev/null 2>&1 || true
      rclone delete "$RCLONE_REMOTE/${name}.sha256" >/dev/null 2>&1 || true
      rclone delete "$RCLONE_REMOTE/${name}.manifest.json" >/dev/null 2>&1 || true
      log "retention: pruned remote $name"
    fi
  done
}
prune_local
prune_rclone

trap - EXIT; cleanup
if [ "$ship_ok" -eq 1 ]; then
  log "=== Betazen DR backup OK === $(basename "$FINAL")"
  exit 0
else
  die "backup written locally but one or more off-site uploads failed"
fi
