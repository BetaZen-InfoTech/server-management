#!/usr/bin/env bash
#
# bzpanel-restore.sh — restore a whole-server Betazen DR bundle onto a FRESH
# box and re-point every old-server-IP reference at the new IP.
#
# Prerequisite: run install.sh first so the software stack (nginx, MongoDB,
# MariaDB, PowerDNS, Postfix/Dovecot, OpenDKIM, PHP, pure-ftpd) already exists.
# This script restores the *state* on top of that fresh install.
#
# Usage:
#   bzpanel-restore.sh --bundle <file.tar.gz[.enc]> [--new-ip <ip>] [--yes]
#   bzpanel-restore.sh --rclone gdrive:bzpanel [--latest] [--new-ip <ip>] [--yes]
#
# Flags:
#   --bundle <path>   local bundle to restore
#   --rclone <remote> pull the newest bzpanel-dr-* bundle from an rclone remote
#   --new-ip <ip>     target IP (default: auto-detected primary IP of this box)
#   --no-reassign     restore data but skip the IP rewrite
#   --yes             don't prompt for confirmation (for automation)
#
# The .env merge is deliberate: the panel DATABASE is restored using THIS
# (new) box's working Mongo credentials, then the OLD .env is restored for its
# secrets (APP_ENCRYPTION_KEY etc. — without which restored Mongo secrets are
# undecryptable), but the new box's MONGO_* connection settings are preserved
# so the panel can still reach the freshly-installed MongoDB.
#
set -euo pipefail

ENV_FILE="${BZ_ENV_FILE:-/opt/serverpanel/.env}"
LOG_TAG="bzpanel-restore"
WORK="${BZ_RESTORE_WORK_DIR:-/var/backups/bzpanel/restore}"

BUNDLE=""
RCLONE_REMOTE=""
NEW_IP=""
ASSUME_YES=0
DO_REASSIGN=1

log()  { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*"; logger -t "$LOG_TAG" -- "$*" 2>/dev/null || true; }
warn() { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] WARN: $*" >&2; logger -t "$LOG_TAG" -p user.warning -- "$*" 2>/dev/null || true; }
die()  { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] FATAL: $*" >&2; logger -t "$LOG_TAG" -p user.err -- "$*" 2>/dev/null || true; exit 1; }

getenvval() {
  local key="$1" file="$2" line
  [ -f "$file" ] || return 0
  line="$(grep -E "^[[:space:]]*${key}=" "$file" 2>/dev/null | tail -n1 || true)"
  [ -n "$line" ] || return 0
  line="${line#*=}"; line="${line%$'\r'}"
  line="${line#\"}"; line="${line%\"}"; line="${line#\'}"; line="${line%\'}"
  printf '%s' "$line"
}

# setenvval KEY VALUE FILE — idempotently set KEY=VALUE in a dotenv file.
setenvval() {
  local key="$1" val="$2" file="$3"
  if grep -qE "^[[:space:]]*${key}=" "$file" 2>/dev/null; then
    # use a temp file + sed via a safe pattern (val may contain slashes)
    local esc; esc="$(printf '%s' "$val" | sed -e 's/[\/&]/\\&/g')"
    sed -i "s|^[[:space:]]*${key}=.*|${key}=${esc}|" "$file"
  else
    printf '%s=%s\n' "$key" "$val" >> "$file"
  fi
}

while [ $# -gt 0 ]; do
  case "$1" in
    --bundle)      BUNDLE="${2:-}"; shift ;;
    --bundle=*)    BUNDLE="${1#*=}" ;;
    --rclone)      RCLONE_REMOTE="${2:-}"; shift ;;
    --rclone=*)    RCLONE_REMOTE="${1#*=}" ;;
    --latest)      : ;;  # default behaviour for --rclone
    --new-ip)      NEW_IP="${2:-}"; shift ;;
    --new-ip=*)    NEW_IP="${1#*=}" ;;
    --no-reassign) DO_REASSIGN=0 ;;
    --yes|-y)      ASSUME_YES=1 ;;
    -h|--help)     sed -n '2,32p' "$0"; exit 0 ;;
    *)             die "unknown argument: $1" ;;
  esac
  shift
done

[ "$(id -u)" -eq 0 ] || die "must run as root"
[ -d /opt/serverpanel ] || die "/opt/serverpanel not found — run install.sh first"
mkdir -p "$WORK"

# ----------------------------------------------------------------------------
# locate the bundle (pull newest from rclone if requested)
# ----------------------------------------------------------------------------
if [ -n "$RCLONE_REMOTE" ]; then
  command -v rclone >/dev/null 2>&1 || die "rclone not installed"
  newest="$(rclone lsf "$RCLONE_REMOTE/" 2>/dev/null | grep -E '^bzpanel-dr-.*\.tar\.gz(\.enc)?$' | sort -r | head -n1 || true)"
  [ -n "$newest" ] || die "no bzpanel-dr-* bundle found on $RCLONE_REMOTE"
  log "pulling $newest from $RCLONE_REMOTE"
  rclone copy "$RCLONE_REMOTE/$newest" "$WORK/" || die "rclone download failed"
  rclone copy "$RCLONE_REMOTE/${newest}.manifest.json" "$WORK/" >/dev/null 2>&1 || true
  BUNDLE="$WORK/$newest"
fi

[ -n "$BUNDLE" ] || die "no bundle specified (use --bundle or --rclone)"
[ -f "$BUNDLE" ] || die "bundle not found: $BUNDLE"

# ----------------------------------------------------------------------------
# decrypt + extract
# ----------------------------------------------------------------------------
STAGE="$WORK/extract-$$"
mkdir -p "$STAGE"
cleanup() { rm -rf "$STAGE" 2>/dev/null || true; }
trap cleanup EXIT

ENC_KEY="$(getenvval BACKUP_ENCRYPTION_KEY "$ENV_FILE")"
case "$BUNDLE" in
  *.enc)
    [ -n "$ENC_KEY" ] || die "bundle is encrypted but BACKUP_ENCRYPTION_KEY is unset in $ENV_FILE (restore it from your old .env first)"
    log "decrypting bundle…"
    openssl enc -d -aes-256-cbc -pbkdf2 -salt -in "$BUNDLE" -pass pass:"$ENC_KEY" \
      | tar -xzf - -C "$STAGE" || die "decrypt/extract failed (wrong BACKUP_ENCRYPTION_KEY?)"
    ;;
  *.tar.gz)
    tar -xzf "$BUNDLE" -C "$STAGE" || die "extract failed"
    ;;
  *) die "unrecognised bundle extension: $BUNDLE" ;;
esac

ROOT="$(find "$STAGE" -maxdepth 1 -type d -name 'bzpanel-dr-*' | head -n1)"
[ -n "$ROOT" ] || die "bundle does not contain a bzpanel-dr-* root"
[ -f "$ROOT/manifest.json" ] || warn "manifest.json missing — IP reassignment may be skipped"

OLD_IP="$(getenvval server_ip "$ROOT/manifest.json" 2>/dev/null || true)"
# manifest is JSON, not dotenv; pull server_ip with a tolerant grep
[ -n "$OLD_IP" ] || OLD_IP="$(grep -oE '"server_ip"[[:space:]]*:[[:space:]]*"[^"]*"' "$ROOT/manifest.json" 2>/dev/null | sed -E 's/.*"([^"]*)"$/\1/' || true)"
[ -n "$NEW_IP" ] || NEW_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"

log "bundle root: $(basename "$ROOT")"
log "old IP (from manifest): ${OLD_IP:-unknown}"
log "new IP (this box):       ${NEW_IP:-unknown}"

if [ "$ASSUME_YES" != "1" ]; then
  echo
  echo "This will OVERWRITE the panel database, /home, MySQL, mail, DNS, TLS and"
  echo "configuration on THIS server with the contents of the bundle, then rewrite"
  echo "${OLD_IP:-<old>} → ${NEW_IP:-<new>}. This is destructive and irreversible."
  printf "Type 'restore' to proceed: "
  read -r confirm
  [ "$confirm" = "restore" ] || die "aborted by operator"
fi

restore_dir() {  # restore_dir <stage-rel-path> <dest>
  local rel="$1" dest="$2"
  [ -e "$ROOT/$rel" ] || { warn "skip (not in bundle): $rel"; return 0; }
  mkdir -p "$(dirname "$dest")"
  cp -a "$ROOT/$rel" "$dest" && log "restored → $dest" || warn "failed to restore $rel"
}

# ----------------------------------------------------------------------------
# 1. panel DB — restore using THIS box's working Mongo credentials
# ----------------------------------------------------------------------------
NEW_MONGO_URI="$(getenvval MONGO_URI "$ENV_FILE")"
NEW_MONGO_DB="$(getenvval MONGO_DB_NAME "$ENV_FILE")"; NEW_MONGO_DB="${NEW_MONGO_DB:-serverpanel}"
NEW_MONGO_PUBLIC="$(getenvval MONGO_PUBLIC_HOST "$ENV_FILE")"
# Pick the panel-brain archive specifically — app DBs are app__*.archive.gz and
# are restored separately below, so they must NOT be selected here. (v3.1.120)
ARCHIVE="$ROOT/mongo/${NEW_MONGO_DB}.archive.gz"
[ -f "$ARCHIVE" ] || ARCHIVE="$(find "$ROOT/mongo" -name '*.archive.gz' ! -name 'app__*' 2>/dev/null | head -n1 || true)"
if [ -n "$ARCHIVE" ] && [ -f "$ARCHIVE" ] && command -v mongorestore >/dev/null 2>&1; then
  log "restoring MongoDB ($NEW_MONGO_DB)…"
  if [ -n "$NEW_MONGO_URI" ]; then
    mongorestore --uri="$NEW_MONGO_URI" --gzip --drop --archive="$ARCHIVE" >/dev/null 2>&1 \
      || mongorestore --gzip --drop --archive="$ARCHIVE" >/dev/null 2>&1 \
      || warn "mongorestore failed"
  else
    mongorestore --gzip --drop --archive="$ARCHIVE" >/dev/null 2>&1 || warn "mongorestore failed"
  fi
  log "MongoDB restore complete"
else
  warn "no mongo archive in bundle (or mongorestore missing) — panel DB NOT restored"
fi

# ----------------------------------------------------------------------------
# 1b. application Mongo databases — restore every app__<db>.archive.gz captured
#     by the backup, as the root 'admin' user on THIS box. (v3.1.120)
# ----------------------------------------------------------------------------
if command -v mongorestore >/dev/null 2>&1 && ls "$ROOT"/mongo/app__*.archive.gz >/dev/null 2>&1; then
  _newadmin=""
  if [ -n "$NEW_MONGO_URI" ]; then
    _np=$(printf '%s' "$NEW_MONGO_URI" | sed -E 's#^mongodb(\+srv)?://[^:]+:([^@]+)@.*#\2#')
    _nh=$(printf '%s' "$NEW_MONGO_URI" | sed -E 's#^mongodb(\+srv)?://[^@]+@([^/?]+).*#\2#')
    _newadmin="mongodb://admin:${_np}@${_nh}/?authSource=admin"
  fi
  for _af in "$ROOT"/mongo/app__*.archive.gz; do
    [ -e "$_af" ] || continue
    _adb=$(basename "$_af" .archive.gz); _adb=${_adb#app__}
    if [ -n "$_newadmin" ]; then
      mongorestore --uri="$_newadmin" --gzip --drop --archive="$_af" >/dev/null 2>&1 \
        || mongorestore --gzip --drop --archive="$_af" >/dev/null 2>&1 \
        || { warn "app DB $_adb restore failed"; continue; }
    else
      mongorestore --gzip --drop --archive="$_af" >/dev/null 2>&1 || { warn "app DB $_adb restore failed"; continue; }
    fi
    log "restored app MongoDB: $_adb"
  done
fi

# ----------------------------------------------------------------------------
# 2. MySQL / MariaDB — restore all databases (incl. user grants + roundcube)
# ----------------------------------------------------------------------------
SQL="$ROOT/mysql/all-databases.sql.gz"
if [ -f "$SQL" ] && command -v mysql >/dev/null 2>&1; then
  log "restoring MySQL (all databases)…"
  if gunzip -c "$SQL" | mysql 2>/dev/null; then
    mysql -e "FLUSH PRIVILEGES;" 2>/dev/null || true
    log "MySQL restore complete"
  else
    warn "MySQL restore reported errors"
  fi
else
  warn "no MySQL dump in bundle (or mysql client missing) — skipping"
fi

# ----------------------------------------------------------------------------
# 3. hosted sites + owned-domain mail
# ----------------------------------------------------------------------------
if [ -f "$ROOT/home/home.tar.gz" ]; then
  log "restoring /home…"
  tar -xzf "$ROOT/home/home.tar.gz" -C / && log "/home restored" || warn "/home restore reported errors"
fi

# ----------------------------------------------------------------------------
# 4. mail fallback dirs + Dovecot auth
# ----------------------------------------------------------------------------
[ -d "$ROOT/mail/vhosts" ] && restore_dir "mail/vhosts" /var/mail/vhosts
[ -d "$ROOT/mail/vmail" ]  && restore_dir "mail/vmail"  /var/vmail
[ -f "$ROOT/mail/dovecot-users" ] && restore_dir "mail/dovecot-users" /etc/dovecot/users
[ -f "$ROOT/mail/99-panel.conf" ] && restore_dir "mail/99-panel.conf" /etc/dovecot/conf.d/99-panel.conf
id vmail >/dev/null 2>&1 && { chown -R vmail:vmail /var/mail/vhosts /var/vmail 2>/dev/null || true; }

# ----------------------------------------------------------------------------
# 5. DKIM, DNS, TLS
# ----------------------------------------------------------------------------
restore_dir "dkim/opendkim" /etc/opendkim
chown -R opendkim:opendkim /etc/opendkim 2>/dev/null || true

if [ -f "$ROOT/dns/pdns.sqlite3" ]; then
  systemctl stop pdns 2>/dev/null || true
  restore_dir "dns/pdns.sqlite3" /var/lib/powerdns/pdns.sqlite3
  chown pdns:pdns /var/lib/powerdns/pdns.sqlite3 2>/dev/null || true
  systemctl start pdns 2>/dev/null || true
fi
restore_dir "dns/pdns.conf" /etc/powerdns/pdns.conf
[ -d "$ROOT/dns/pdns.d" ] && restore_dir "dns/pdns.d" /etc/powerdns/pdns.d

restore_dir "ssl/letsencrypt" /etc/letsencrypt
[ -f "$ROOT/ssl/pure-ftpd.pem" ] && restore_dir "ssl/pure-ftpd.pem" /etc/ssl/private/pure-ftpd.pem

# ----------------------------------------------------------------------------
# 6. web / mail / ftp configuration
# ----------------------------------------------------------------------------
restore_dir "config/nginx/sites-available" /etc/nginx/sites-available
restore_dir "config/nginx/snippets"        /etc/nginx/snippets
restore_dir "config/postfix"               /etc/postfix
restore_dir "config/roundcube"             /etc/roundcube
restore_dir "config/phpmyadmin"            /etc/phpmyadmin
restore_dir "config/pure-ftpd"             /etc/pure-ftpd
# PHP pools: copy only pool.d files so we don't clobber the new box's PHP build.
if [ -d "$ROOT/config/php" ]; then
  find "$ROOT/config/php" -path '*/fpm/pool.d/*.conf' 2>/dev/null | while read -r p; do
    rel="${p#"$ROOT"/config/php/}"; mkdir -p "/etc/php/$(dirname "$rel")"; cp -a "$p" "/etc/php/$rel" 2>/dev/null || true
  done
  log "restored PHP-FPM pools"
fi
# Re-enable nginx vhosts (sites-enabled symlinks aren't in the archive).
for f in /etc/nginx/sites-available/*; do
  [ -e "$f" ] || continue
  ln -sf "$f" "/etc/nginx/sites-enabled/$(basename "$f")" 2>/dev/null || true
done

# ----------------------------------------------------------------------------
# 7. app runtime
# ----------------------------------------------------------------------------
[ -f "$ROOT/apps/pm2-dump.pm2" ] && { mkdir -p /root/.pm2; cp -a "$ROOT/apps/pm2-dump.pm2" /root/.pm2/dump.pm2 2>/dev/null || true; }
if [ -d "$ROOT/apps/systemd" ]; then
  cp -a "$ROOT"/apps/systemd/sp-app-*.service /etc/systemd/system/ 2>/dev/null || true
  systemctl daemon-reload 2>/dev/null || true
fi

# ----------------------------------------------------------------------------
# 7b. mail-suite — restore the standalone mail product's install dir (.env +
#     built binary + webmail dist) and its systemd unit. Its Mongo DB came back
#     via the app-DB restore (1b), its nginx vhost via section 6 (and was
#     re-symlinked into sites-enabled above), and its deployment registration
#     via the panel DB (1). Only present in bundles from boxes that actually had
#     mail-suite installed — a no-op otherwise. The .env keeps the SAME public
#     domain (mail-suite addresses by domain, not IP, so the IP reassignment in
#     section 10 doesn't touch it); a move to a NEW domain is handled by the
#     server-transfer path, not DR restore.
# ----------------------------------------------------------------------------
if [ -d "$ROOT/mailsuite/opt-mail-suite" ]; then
  restore_dir "mailsuite/opt-mail-suite" /opt/mail-suite
  [ -f "$ROOT/mailsuite/mail-suite.service" ] && restore_dir "mailsuite/mail-suite.service" /etc/systemd/system/mail-suite.service
  [ -d "$ROOT/mailsuite/ssl-mail-suite" ]     && restore_dir "mailsuite/ssl-mail-suite"     /etc/ssl/mail-suite
  systemctl daemon-reload 2>/dev/null || true
  systemctl enable mail-suite 2>/dev/null || true
  log "restored mail-suite install + unit (started in the service-restart step below)"
fi

# ----------------------------------------------------------------------------
# 8. .env merge — restore OLD secrets, KEEP new Mongo connection
# ----------------------------------------------------------------------------
if [ -f "$ROOT/secrets/.env" ]; then
  log "merging restored secrets into $ENV_FILE (keeping this box's Mongo credentials)…"
  cp -a "$ENV_FILE" "${ENV_FILE}.newbox.bak" 2>/dev/null || true
  cp -a "$ROOT/secrets/.env" "$ENV_FILE"
  # re-stamp the new box's Mongo connection so the panel can still reach Mongo
  [ -n "$NEW_MONGO_URI" ]    && setenvval MONGO_URI        "$NEW_MONGO_URI"    "$ENV_FILE"
  [ -n "$NEW_MONGO_DB" ]     && setenvval MONGO_DB_NAME    "$NEW_MONGO_DB"     "$ENV_FILE"
  [ -n "$NEW_MONGO_PUBLIC" ] && setenvval MONGO_PUBLIC_HOST "$NEW_MONGO_PUBLIC" "$ENV_FILE"
  chmod 600 "$ENV_FILE"
fi

# ----------------------------------------------------------------------------
# 9. restart services + reload everything
# ----------------------------------------------------------------------------
log "reloading services…"
systemctl daemon-reload 2>/dev/null || true
nginx -t 2>/dev/null && systemctl reload nginx 2>/dev/null || warn "nginx config test failed — review before serving"
for svc in opendkim postfix dovecot pdns mariadb mysql pure-ftpd mail-suite serverpanel; do
  systemctl restart "$svc" 2>/dev/null || true
done
command -v pm2 >/dev/null 2>&1 && { pm2 resurrect 2>/dev/null || true; }

# ----------------------------------------------------------------------------
# 10. IP reassignment — rewrites DNS (A/AAAA), SPF, DB mirrors, .env, nginx,
#     and pure-ftpd ForcePassiveIP via the panel's own routine.
# ----------------------------------------------------------------------------
if [ "$DO_REASSIGN" = "1" ]; then
  if [ -n "$OLD_IP" ] && [ -n "$NEW_IP" ] && [ "$OLD_IP" != "$NEW_IP" ]; then
    BZ=""
    for cand in /opt/serverpanel/bin/bzpanel /usr/local/bin/bzpanel /opt/serverpanel/bzpanel "$(command -v bzpanel 2>/dev/null || true)"; do
      [ -x "$cand" ] && { BZ="$cand"; break; }
    done
    if [ -n "$BZ" ]; then
      log "reassigning IP $OLD_IP → $NEW_IP via $BZ"
      "$BZ" reassign-ip "$OLD_IP" "$NEW_IP" || warn "reassign-ip reported errors — review DNS/ftp manually"
    else
      warn "bzpanel CLI not found — run manually: bzpanel reassign-ip $OLD_IP $NEW_IP"
    fi
  else
    warn "skipping reassign-ip (old=$OLD_IP new=$NEW_IP)"
  fi
fi

log "=== Betazen DR restore complete ==="
log "Post-restore manual checks: PTR/rDNS + registrar glue at your provider; verify mailbox login + DKIM TXT."
exit 0
