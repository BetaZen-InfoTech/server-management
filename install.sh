#!/bin/bash
# =============================================================================
# ServerPanel — One-Click Installer
# BetaZen InfoTech (https://betazeninfotech.com)
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/BetaZen-InfoTech/server-management/main/install.sh | bash
#   OR
#   wget -qO- https://raw.githubusercontent.com/BetaZen-InfoTech/server-management/main/install.sh | bash
#
# Supported: Ubuntu 22.04 / 24.04 (amd64)
# =============================================================================

set -e

# Wrap entire script in a function so bash reads the whole file before
# executing anything. This is the robust `curl | bash` pattern: without it,
# an interactive `read` can consume not-yet-parsed script bytes from the pipe
# and cause bogus syntax errors further down.
_serverpanel_install() {

# --- Colors ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

INSTALL_DIR="/opt/serverpanel"
GO_VERSION="1.23.0"
NODE_MAJOR=20
MONGO_VERSION="7.0"
LOG_FILE="/var/log/serverpanel-install.log"

INSTALL_START_TS=$(date +%s)
STEP_START_TS=$INSTALL_START_TS
CURRENT_STEP=""

_fmt_duration() {
    local s=$1
    if [ "$s" -lt 60 ]; then
        printf "%ds" "$s"
    else
        printf "%dm%02ds" "$((s / 60))" "$((s % 60))"
    fi
}

log()  { echo -e "${GREEN}[+]${NC} $1"; echo "[$(date)] $1" >> "$LOG_FILE"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; echo "[$(date)] WARN: $1" >> "$LOG_FILE"; }
err()  { echo -e "${RED}[x]${NC} $1"; echo "[$(date)] ERROR: $1" >> "$LOG_FILE"; }
step() {
    # Close out the previous step's timer, if any.
    if [ -n "$CURRENT_STEP" ]; then
        local dur=$(( $(date +%s) - STEP_START_TS ))
        echo -e "  ${GREEN}✓${NC} completed in $(_fmt_duration "$dur")"
    fi
    CURRENT_STEP=$1
    STEP_START_TS=$(date +%s)
    local total=$(( STEP_START_TS - INSTALL_START_TS ))
    echo -e "\n${CYAN}==>${NC} ${BLUE}$1${NC} ${YELLOW}[total elapsed: $(_fmt_duration "$total")]${NC}"
    echo "[$(date)] STEP: $1" >> "$LOG_FILE"
}

# Surface silent failures: most commands redirect output to $LOG_FILE, so a
# failed command under `set -e` would otherwise exit with no visible context.
# This trap prints the current step + last 40 lines of the log on any error.
_on_error() {
    local rc=$?
    local line=$1
    echo ""
    echo -e "${RED}============================================${NC}"
    echo -e "${RED}[x] Install aborted (exit $rc at line $line)${NC}"
    echo -e "${RED}============================================${NC}"
    if [ -f "$LOG_FILE" ]; then
        echo -e "${YELLOW}Last 40 lines of $LOG_FILE:${NC}"
        tail -n 40 "$LOG_FILE" || true
    fi
    exit $rc
}
trap '_on_error $LINENO' ERR

# --- Pre-flight checks ---
if [ "$(id -u)" -ne 0 ]; then
    err "This script must be run as root"
    exit 1
fi

if ! grep -qi 'ubuntu' /etc/os-release 2>/dev/null; then
    warn "This installer is designed for Ubuntu. Other distros may work but are untested."
fi

ARCH=$(dpkg --print-architecture 2>/dev/null || echo "amd64")
SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}')

echo -e "${CYAN}"
echo "  ____                           ____                  _ "
echo " / ___|  ___ _ ____   _____ _ __|  _ \ __ _ _ __   ___| |"
echo " \___ \ / _ \ '__\ \ / / _ \ '__| |_) / _\` | '_ \ / _ \ |"
echo "  ___) |  __/ |   \ V /  __/ |  |  __/ (_| | | | |  __/ |"
echo " |____/ \___|_|    \_/ \___|_|  |_|   \__,_|_| |_|\___|_|"
echo -e "${NC}"
echo -e " ${BLUE}BetaZen InfoTech — Server Management Panel${NC}"
echo -e " Server IP: ${GREEN}${SERVER_IP}${NC}"
echo ""

# --- Prompt for configuration ---
# When invoked via `curl ... | bash`, stdin is the piped script itself, so we
# must read interactive prompts from the controlling terminal directly.
if [ -r /dev/tty ]; then
    TTY_IN=/dev/tty
else
    TTY_IN=/dev/stdin
fi

read -p "Enter panel domain (e.g., panel.example.com) [default: $SERVER_IP]: " PANEL_DOMAIN < "$TTY_IN"
PANEL_DOMAIN=${PANEL_DOMAIN:-$SERVER_IP}

read -p "Enter admin email [default: admin@betazeninfotech.com]: " ADMIN_EMAIL < "$TTY_IN"
ADMIN_EMAIL=${ADMIN_EMAIL:-admin@betazeninfotech.com}

read -sp "Enter admin password [default: admin123]: " ADMIN_PASS < "$TTY_IN"
ADMIN_PASS=${ADMIN_PASS:-admin123}
echo ""

read -sp "Set MongoDB password [default: auto-generated]: " MONGO_PASS < "$TTY_IN"
echo ""

# Reuse secrets from a previous install so re-running the installer does
# not regenerate credentials that are already provisioned in live services
# (MongoDB users, JWT sessions, agent API key). Without this, every rerun
# wrote a fresh random MONGO_PASS to .env while the MongoDB user kept its
# old password, and the backend then crashed on boot with an auth error —
# which is exactly what produced the 502 Bad Gateway from nginx.
EXISTING_ENV_FILE="${INSTALL_DIR}/.env"
_ex_env() { grep -E "^$1=" "$EXISTING_ENV_FILE" 2>/dev/null | head -1 | cut -d= -f2-; }
EX_MONGO_PASS=""
EX_JWT_SECRET=""
EX_AGENT_KEY=""
EX_BACKUP_KEY=""
if [ -f "$EXISTING_ENV_FILE" ]; then
    EX_MONGO_PASS=$(_ex_env MONGO_PASS)
    EX_JWT_SECRET=$(_ex_env JWT_SECRET)
    EX_AGENT_KEY=$(_ex_env AGENT_API_KEY)
    EX_BACKUP_KEY=$(_ex_env BACKUP_ENCRYPTION_KEY)
    if [ -n "$EX_MONGO_PASS" ]; then
        warn "Re-using MongoDB password from existing ${EXISTING_ENV_FILE}"
    fi
fi

if [ -z "$MONGO_PASS" ]; then
    MONGO_PASS="${EX_MONGO_PASS:-$(openssl rand -hex 16)}"
fi

JWT_SECRET="${EX_JWT_SECRET:-$(openssl rand -hex 32)}"
AGENT_KEY="${EX_AGENT_KEY:-$(openssl rand -hex 16)}"
BACKUP_KEY="${EX_BACKUP_KEY:-$(openssl rand -hex 16)}"

echo ""
log "Installation starting... (log: $LOG_FILE)"
echo -e "  ${YELLOW}Expected duration: 8–15 minutes${NC} on a clean VPS with a good network."
echo -e "  ${YELLOW}Each step prints its own elapsed time when it finishes.${NC}"
echo -e "  ${YELLOW}Detailed output (including apt download speeds) is in: ${LOG_FILE}${NC}"
echo -e "  ${YELLOW}Tip: in a second terminal, run  'tail -f ${LOG_FILE}'  to watch live.${NC}"
echo ""

# =============================================================================
# Step 1: System Update & Base Packages
# =============================================================================
step "1/12 — Updating system and installing base packages"
export DEBIAN_FRONTEND=noninteractive
dpkg --configure --force-confold -a >> "$LOG_FILE" 2>&1 || true
apt-get update -y >> "$LOG_FILE" 2>&1
apt-get install -y \
    curl wget git build-essential software-properties-common \
    gnupg lsb-release ca-certificates apt-transport-https \
    ufw fail2ban unzip jq dnsutils sshpass \
    >> "$LOG_FILE" 2>&1
log "Base packages installed"

# =============================================================================
# Step 2: Nginx
# =============================================================================
step "2/12 — Installing Nginx"
if ! command -v nginx &>/dev/null; then
    apt-get install -y nginx >> "$LOG_FILE" 2>&1
    systemctl enable nginx >> "$LOG_FILE" 2>&1
    log "Nginx installed"
else
    log "Nginx already installed ($(nginx -v 2>&1))"
fi

# Create sites-available/enabled dirs if missing
mkdir -p /etc/nginx/sites-available /etc/nginx/sites-enabled

# =============================================================================
# Step 3: PHP
# =============================================================================
step "3/12 — Installing PHP 8.2"
if ! command -v php8.2 &>/dev/null; then
    add-apt-repository -y ppa:ondrej/php >> "$LOG_FILE" 2>&1
    apt-get update -y >> "$LOG_FILE" 2>&1
    apt-get install -y \
        php8.2 php8.2-fpm php8.2-cli php8.2-common php8.2-mysql \
        php8.2-xml php8.2-mbstring php8.2-curl php8.2-zip php8.2-gd \
        php8.2-bcmath php8.2-intl php8.2-soap php8.2-readline \
        php8.2-mongodb php8.2-imagick php8.2-redis \
        >> "$LOG_FILE" 2>&1
    systemctl enable php8.2-fpm >> "$LOG_FILE" 2>&1
    systemctl start php8.2-fpm >> "$LOG_FILE" 2>&1
    log "PHP 8.2 installed"
else
    log "PHP 8.2 already installed"
fi

# =============================================================================
# Step 4: MongoDB
# =============================================================================
step "4/12 — Installing MongoDB ${MONGO_VERSION}"
if ! command -v mongosh &>/dev/null; then
    curl -fsSL https://www.mongodb.org/static/pgp/server-${MONGO_VERSION}.asc | gpg --dearmor -o /usr/share/keyrings/mongodb-server-${MONGO_VERSION}.gpg 2>> "$LOG_FILE"
    UBUNTU_CODENAME=$(lsb_release -cs)
    # MongoDB 7.0 supports jammy; for noble use jammy repo
    if [ "$UBUNTU_CODENAME" = "noble" ]; then
        UBUNTU_CODENAME="jammy"
    fi
    echo "deb [arch=amd64,arm64 signed-by=/usr/share/keyrings/mongodb-server-${MONGO_VERSION}.gpg] https://repo.mongodb.org/apt/ubuntu ${UBUNTU_CODENAME}/mongodb-org/${MONGO_VERSION} multiverse" > /etc/apt/sources.list.d/mongodb-org-${MONGO_VERSION}.list
    apt-get update -y >> "$LOG_FILE" 2>&1
    apt-get install -y mongodb-org >> "$LOG_FILE" 2>&1
    systemctl enable mongod >> "$LOG_FILE" 2>&1
    systemctl start mongod >> "$LOG_FILE" 2>&1
    sleep 3
    log "MongoDB installed"
else
    log "MongoDB already installed"
    systemctl start mongod >> "$LOG_FILE" 2>&1 || true
fi

# Ensure MongoDB users exist and match the current MONGO_PASS. A previous
# failed run can leave the `serverpanel` user with a stale password and no
# way for us to authenticate in and fix it, so the only reliably
# idempotent flow is: disable auth -> reset users -> re-enable auth.
# This also heals the exact state that caused the 502 Bad Gateway: .env
# with a freshly rotated password while MongoDB still held the old one.

# Helper: wait until mongod responds to a no-auth ping, or bail after ~30s.
_mongo_wait_ready() {
    local i
    for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
        if mongosh --quiet --eval "db.adminCommand('ping')" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    return 1
}

log "Syncing MongoDB users with current password (disabling auth temporarily)..."

# 1. Turn auth off so we can reset users regardless of what's stored.
#    We rewrite the whole `security:` block so the state is deterministic
#    regardless of what a prior run left behind.
python3 - "$MONGO_PASS" <<'PYEOF' >> "$LOG_FILE" 2>&1 || true
import re, sys
path = "/etc/mongod.conf"
with open(path) as f: text = f.read()
# Strip any existing security/authorization lines.
lines = []
skip = False
for line in text.splitlines():
    if re.match(r'^security:', line):
        skip = True
        continue
    if skip and re.match(r'^\s+', line):
        continue
    skip = False
    lines.append(line)
with open(path, 'w') as f:
    f.write("\n".join(lines).rstrip() + "\n")
PYEOF
systemctl restart mongod >> "$LOG_FILE" 2>&1 || true
if ! _mongo_wait_ready; then
    err "mongod did not become ready after disabling auth"
    systemctl status mongod --no-pager -l | tee -a "$LOG_FILE" || true
    journalctl -u mongod --no-pager -n 50 | tee -a "$LOG_FILE" || true
    exit 1
fi

# 2. Create-or-update both users with the current password.
#    Both users live in the `admin` database (authSource=admin in .env).
MONGO_USER_SCRIPT=$(cat <<JSEOF
use admin;
try {
  db.createUser({ user: 'admin', pwd: '${MONGO_PASS}', roles: ['root'] });
  print('admin: created');
} catch(e) {
  try { db.updateUser('admin', { pwd: '${MONGO_PASS}' }); print('admin: updated'); }
  catch(e2) { print('admin sync FAILED: ' + e2.message); }
}
try {
  db.createUser({ user: 'serverpanel', pwd: '${MONGO_PASS}', roles: [
    { role: 'readWrite', db: 'serverpanel' },
    { role: 'dbAdmin', db: 'serverpanel' }
  ]});
  print('serverpanel: created');
} catch(e) {
  try { db.updateUser('serverpanel', { pwd: '${MONGO_PASS}' }); print('serverpanel: updated'); }
  catch(e2) { print('serverpanel sync FAILED: ' + e2.message); }
}
print('---USERS---');
printjson(db.getUsers());
JSEOF
)
echo "$MONGO_USER_SCRIPT" | mongosh --quiet >> "$LOG_FILE" 2>&1 || warn "mongosh user provisioning returned non-zero"

# 3. Turn auth back on deterministically.
cat >> /etc/mongod.conf << 'MCONF'

security:
  authorization: enabled
MCONF
systemctl restart mongod >> "$LOG_FILE" 2>&1 || true

# 4. Wait for mongod readiness (ping still works even with auth, it just
#    returns unauthenticated before login).
_mongo_wait_ready || true
sleep 2

# 5. Verify the serverpanel user can actually authenticate.
_auth_ok=0
for i in 1 2 3 4 5 6 7 8 9 10; do
    if mongosh --quiet -u serverpanel -p "${MONGO_PASS}" --authenticationDatabase admin \
        --eval "db.adminCommand('ping')" >> "$LOG_FILE" 2>&1; then
        _auth_ok=1
        break
    fi
    sleep 2
done
if [ "$_auth_ok" -ne 1 ]; then
    err "MongoDB authentication check failed for user 'serverpanel' — backend will not start"
    echo ""
    echo -e "${YELLOW}--- mongod.conf (tail) ---${NC}"
    tail -n 20 /etc/mongod.conf | tee -a "$LOG_FILE" || true
    echo ""
    echo -e "${YELLOW}--- systemctl status mongod ---${NC}"
    systemctl status mongod --no-pager -l | tee -a "$LOG_FILE" || true
    echo ""
    echo -e "${YELLOW}--- journalctl -u mongod -n 60 ---${NC}"
    journalctl -u mongod --no-pager -n 60 | tee -a "$LOG_FILE" || true
    echo ""
    echo -e "${YELLOW}--- db.getUsers() via no-auth mongosh ---${NC}"
    mongosh --quiet --eval "use admin; printjson(db.getUsers());" 2>&1 | tee -a "$LOG_FILE" || true
    echo ""
    echo -e "${YELLOW}--- last 60 lines of $LOG_FILE ---${NC}"
    tail -n 60 "$LOG_FILE" || true
    exit 1
fi
log "MongoDB configured with authentication (credentials verified)"

# =============================================================================
# Step 5: MariaDB
# =============================================================================
step "5/12 — Installing MariaDB"
if ! command -v mysql &>/dev/null; then
    apt-get install -y mariadb-server mariadb-client >> "$LOG_FILE" 2>&1
    systemctl enable mariadb >> "$LOG_FILE" 2>&1
    systemctl start mariadb >> "$LOG_FILE" 2>&1
    log "MariaDB installed"
else
    log "MariaDB already installed"
fi

# =============================================================================
# Step 6: Email Stack (Postfix + Dovecot + OpenDKIM)
# =============================================================================
step "6/12 — Installing Email Stack (Postfix, Dovecot, OpenDKIM)"
if ! command -v postfix &>/dev/null; then
    # Pre-seed postfix
    debconf-set-selections <<< "postfix postfix/main_mailer_type select Internet Site"
    debconf-set-selections <<< "postfix postfix/mailname string ${PANEL_DOMAIN}"
    apt-get install -y postfix >> "$LOG_FILE" 2>&1
fi

apt-get install -y \
    dovecot-core dovecot-imapd dovecot-pop3d dovecot-lmtpd \
    opendkim opendkim-tools spamassassin \
    >> "$LOG_FILE" 2>&1

# Create vmail user
groupadd -g 5000 vmail 2>/dev/null || true
useradd -g vmail -u 5000 vmail -d /var/mail/vhosts -s /usr/sbin/nologin 2>/dev/null || true
mkdir -p /var/mail/vhosts
chown -R vmail:vmail /var/mail/vhosts

# Create Postfix config files
touch /etc/postfix/virtual_domains /etc/postfix/virtual_mailboxes /etc/postfix/virtual_alias_maps
postmap /etc/postfix/virtual_domains 2>/dev/null || true
postmap /etc/postfix/virtual_mailboxes 2>/dev/null || true
postmap /etc/postfix/virtual_alias_maps 2>/dev/null || true

# Create Dovecot users file
touch /etc/dovecot/users
chmod 640 /etc/dovecot/users

# Create OpenDKIM directories
mkdir -p /etc/opendkim/keys
touch /etc/opendkim/signing.table /etc/opendkim/key.table /etc/opendkim/trusted.hosts
echo "127.0.0.1" > /etc/opendkim/trusted.hosts
echo "::1" >> /etc/opendkim/trusted.hosts

systemctl enable postfix dovecot opendkim >> "$LOG_FILE" 2>&1
systemctl restart postfix dovecot opendkim >> "$LOG_FILE" 2>&1 || true
log "Email stack installed"

# =============================================================================
# Step 7: DNS (PowerDNS)
# =============================================================================
step "7/12 — Installing PowerDNS"
# Reconfigure unconditionally if pdns isn't running — a prior failed install
# can leave the binary present but the service dead, and the old gate
# (command -v pdns_server) would then skip the whole block and silently
# proceed with broken DNS.
if ! command -v pdns_server &>/dev/null || ! systemctl is-active --quiet pdns; then
    # Free port 53 by stopping systemd-resolved's stub listener.
    systemctl stop systemd-resolved >> "$LOG_FILE" 2>&1 || true
    systemctl disable systemd-resolved >> "$LOG_FILE" 2>&1 || true
    rm -f /etc/resolv.conf
    echo "nameserver 8.8.8.8" > /etc/resolv.conf
    echo "nameserver 8.8.4.4" >> /etc/resolv.conf

    # Prevent pdns-server's post-install hook from auto-starting the service.
    # Without this, dpkg runs `systemctl start pdns` before we've written a
    # config/DB, pdns fails to boot, dpkg exits non-zero, and `set -e` kills
    # the whole installer silently. The policy-rc.d shim makes invoke-rc.d
    # return 101 (action forbidden) so the service is never started by apt.
    cat > /usr/sbin/policy-rc.d << 'POLICYEOF'
#!/bin/sh
exit 101
POLICYEOF
    chmod +x /usr/sbin/policy-rc.d

    # Ensure universe is enabled (pdns-backend-sqlite3 lives there on Ubuntu).
    add-apt-repository -y universe >> "$LOG_FILE" 2>&1 || true
    apt-get update -y >> "$LOG_FILE" 2>&1

    # NB: the Debian/Ubuntu package is `pdns-backend-sqlite3` (no leading "g").
    # The `g` in `gsqlite3` is the pdns launcher name, not part of the package
    # name — using pdns-backend-gsqlite3 yields "Unable to locate package".
    apt-get install -y pdns-server pdns-backend-sqlite3 sqlite3 >> "$LOG_FILE" 2>&1
    rm -f /usr/sbin/policy-rc.d

    # The pdns-server package ships a default /etc/powerdns/pdns.d/bind.conf
    # that sets `launch=bind`, which conflicts with our gsqlite3 launch line
    # and makes pdns refuse to start. Drop all default backend snippets.
    rm -f /etc/powerdns/pdns.d/*.conf

    # Initialize SQLite backend. The schema file location varies across
    # Ubuntu releases (plain .sql vs gzipped .sql.gz, /usr/share/doc vs
    # /usr/share/pdns-backend-gsqlite3), so probe all known paths.
    PDNS_DB="/var/lib/powerdns/pdns.sqlite3"
    mkdir -p /var/lib/powerdns
    if [ ! -f "$PDNS_DB" ]; then
        SCHEMA_SRC=""
        for candidate in \
            /usr/share/pdns-backend-sqlite3/schema/schema.sqlite3.sql \
            /usr/share/doc/pdns-backend-sqlite3/schema.sqlite3.sql \
            /usr/share/doc/pdns-backend-sqlite3/schema.sqlite3.sql.gz \
            /usr/share/pdns-backend-gsqlite3/schema/schema.sqlite3.sql \
            /usr/share/doc/pdns-backend-gsqlite3/schema.sqlite3.sql \
            /usr/share/doc/pdns-backend-gsqlite3/schema.sqlite3.sql.gz; do
            if [ -f "$candidate" ]; then
                SCHEMA_SRC="$candidate"
                break
            fi
        done
        if [ -z "$SCHEMA_SRC" ]; then
            err "PowerDNS schema file not found in any known location"
            exit 1
        fi
        if [[ "$SCHEMA_SRC" == *.gz ]]; then
            gunzip -c "$SCHEMA_SRC" | sqlite3 "$PDNS_DB" 2>> "$LOG_FILE"
        else
            sqlite3 "$PDNS_DB" < "$SCHEMA_SRC" 2>> "$LOG_FILE"
        fi
        log "PowerDNS schema imported from $SCHEMA_SRC"
    fi
    chown -R pdns:pdns /var/lib/powerdns
    chmod 664 "$PDNS_DB"

    # Configure PowerDNS
    cat > /etc/powerdns/pdns.conf << PDNSEOF
setgid=pdns
setuid=pdns
launch=gsqlite3
gsqlite3-database=${PDNS_DB}
local-address=0.0.0.0
local-port=53
api=yes
api-key=${AGENT_KEY}
webserver=yes
webserver-address=127.0.0.1
webserver-port=8081
webserver-allow-from=127.0.0.1
PDNSEOF

    systemctl enable pdns >> "$LOG_FILE" 2>&1
    if ! systemctl restart pdns >> "$LOG_FILE" 2>&1; then
        warn "pdns failed to start — dumping status for diagnosis:"
        systemctl status pdns --no-pager -l >> "$LOG_FILE" 2>&1 || true
        journalctl -u pdns --no-pager -n 50 >> "$LOG_FILE" 2>&1 || true
        err "PowerDNS did not start — see $LOG_FILE"
        exit 1
    fi
    log "PowerDNS installed"
else
    log "PowerDNS already installed"
fi

# =============================================================================
# Step 8: SSL (Certbot) + Pure-FTPd
# =============================================================================
step "8/13 — Installing Certbot & Pure-FTPd"
apt-get install -y certbot python3-certbot-nginx pure-ftpd >> "$LOG_FILE" 2>&1
systemctl enable pure-ftpd >> "$LOG_FILE" 2>&1 || true
log "Certbot & Pure-FTPd installed"

# =============================================================================
# Step 8.5: Roundcube Webmail
# =============================================================================
step "8.5/13 — Installing Roundcube Webmail"
ROUNDCUBE_DB_PASS=$(openssl rand -hex 12)

if ! dpkg -l roundcube 2>/dev/null | grep -q "^ii"; then
    # Pre-seed debconf for non-interactive install
    debconf-set-selections <<< "roundcube-core roundcube/dbconfig-install boolean true"
    debconf-set-selections <<< "roundcube-core roundcube/database-type select mysql"
    debconf-set-selections <<< "roundcube-core roundcube/mysql/admin-pass password "
    debconf-set-selections <<< "roundcube-core roundcube/mysql/app-pass password ${ROUNDCUBE_DB_PASS}"
    debconf-set-selections <<< "roundcube-core roundcube/reconfigure-webserver multiselect none"

    apt-get install -y roundcube roundcube-mysql roundcube-plugins >> "$LOG_FILE" 2>&1
    log "Roundcube packages installed"
else
    log "Roundcube already installed"
fi

# Ensure MySQL user and database exist
mysql -e "CREATE DATABASE IF NOT EXISTS roundcube;" >> "$LOG_FILE" 2>&1
mysql -e "DROP USER IF EXISTS 'roundcube'@'localhost'; CREATE USER 'roundcube'@'localhost' IDENTIFIED BY '${ROUNDCUBE_DB_PASS}'; GRANT ALL PRIVILEGES ON roundcube.* TO 'roundcube'@'localhost'; FLUSH PRIVILEGES;" >> "$LOG_FILE" 2>&1

# Import schema if tables are missing
TABLE_COUNT=$(mysql -N -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='roundcube'" 2>/dev/null || echo "0")
if [ "$TABLE_COUNT" -lt 5 ]; then
    mysql roundcube < /usr/share/roundcube/SQL/mysql.initial.sql >> "$LOG_FILE" 2>&1 || true
    log "Roundcube database schema imported"
fi

# Update debian-db.php with correct password
cat > /etc/roundcube/debian-db.php << RCDBEOF
<?php
\$dbuser='roundcube';
\$dbpass='${ROUNDCUBE_DB_PASS}';
\$basepath='';
\$dbname='roundcube';
\$dbserver='localhost';
\$dbport='3306';
\$dbtype='mysql';
RCDBEOF

# Configure Roundcube
cat > /etc/roundcube/config.inc.php << RCEOF
<?php
\$config = [];
include("/etc/roundcube/debian-db-roundcube.php");
\$config['imap_host'] = ["localhost:143"];
\$config['smtp_host'] = 'localhost:587';
\$config['smtp_user'] = '%u';
\$config['smtp_pass'] = '%p';
\$config['support_url'] = '';
\$config['product_name'] = 'ServerPanel Webmail';
\$config['des_key'] = '$(openssl rand -hex 12)';
\$config['plugins'] = ['archive', 'zipdownload'];
\$config['skin'] = 'elastic';

// Auto-create standard mailbox folders
\$config['create_default_folders'] = true;
\$config['drafts_mbox'] = 'Drafts';
\$config['sent_mbox'] = 'Sent';
\$config['junk_mbox'] = 'Junk';
\$config['trash_mbox'] = 'Trash';
\$config['default_folders'] = ['INBOX', 'Drafts', 'Sent', 'Junk', 'Trash'];
RCEOF

# Create SSO HMAC secret for auto-login from WHM panel
openssl rand -hex 32 > /etc/roundcube/sso_hmac_secret
chmod 644 /etc/roundcube/sso_hmac_secret

# Create SSO auto-login script
cat > /var/lib/roundcube/public_html/sso.php << 'SSOPHP'
<?php
/**
 * ServerPanel Webmail SSO — auto-login via signed token from WHM panel.
 */
$token = $_GET['token'] ?? '';
if (empty($token)) { http_response_code(400); die('Missing token'); }

$tokenData = base64_decode(strtr($token, '-_', '+/'));
if ($tokenData === false) { http_response_code(400); die('Invalid token'); }

$payload = json_decode($tokenData, true);
if (!$payload || !isset($payload['email'], $payload['ts'], $payload['sig'])) {
    http_response_code(400); die('Invalid token data');
}

$hmacSecret = trim(@file_get_contents('/etc/roundcube/sso_hmac_secret'));
if (empty($hmacSecret)) { http_response_code(500); die('SSO not configured'); }

$message = $payload['email'] . '|' . $payload['ts'];
$expectedSig = hash_hmac('sha256', $message, $hmacSecret);
if (!hash_equals($expectedSig, $payload['sig'])) { http_response_code(403); die('Invalid signature'); }

if (abs(time() - intval($payload['ts'])) > 60) { http_response_code(403); die('Token expired'); }

$email = $payload['email'];
$password = $payload['pass'] ?? '';
if (empty($password)) { http_response_code(500); die('Missing credentials'); }

define('RCMAIL_CONFIG_DIR', '/etc/roundcube');
define('INSTALL_PATH', '/usr/share/roundcube/');
require_once INSTALL_PATH . 'program/include/iniset.php';

$rcmail = rcmail::get_instance(0, 'prod');
if ($rcmail->login($email, $password, $rcmail->autoselect_host(), false)) {
    $rcmail->session->regenerate_id(false);
    $rcmail->session->set_auth_cookie();
    header('Location: /webmail/?_task=mail');
    exit;
}
http_response_code(401);
die('Login failed for ' . htmlspecialchars($email));
SSOPHP

log "Roundcube webmail + SSO configured"

# =============================================================================
# Step 9: Go (for backend)
# =============================================================================
step "9/13 — Installing Go ${GO_VERSION}"
GO_DIR="/opt/go/${GO_VERSION%%.*}.${GO_VERSION#*.}"
GO_DIR="/opt/go/1.23"
if [ ! -f "${GO_DIR}/bin/go" ]; then
    mkdir -p /opt/go
    # Show the wget progress bar (size + speed + ETA) so the user can see
    # this ~140 MB download moving instead of staring at a frozen terminal.
    wget --show-progress -q "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -O /tmp/go.tar.gz
    tar -C /opt/go -xzf /tmp/go.tar.gz
    mv /opt/go/go "${GO_DIR}"
    rm -f /tmp/go.tar.gz
    log "Go ${GO_VERSION} installed to ${GO_DIR}"
else
    log "Go already installed at ${GO_DIR}"
fi
export PATH="${GO_DIR}/bin:$PATH"

# =============================================================================
# Step 9b: WP-CLI (for WordPress management)
# =============================================================================
step "9b/13 — Installing WP-CLI"
if ! command -v wp &>/dev/null; then
    # -# shows a visible progress bar for the ~7 MB download.
    curl -fL# https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar -o /usr/local/bin/wp
    chmod +x /usr/local/bin/wp
    log "WP-CLI $(wp --allow-root --version 2>/dev/null || echo installed)"
else
    log "WP-CLI $(wp --allow-root --version 2>/dev/null || echo present) already installed"
fi

# =============================================================================
# Step 10: Node.js (for frontend)
# =============================================================================
step "10/13 — Installing Node.js ${NODE_MAJOR}"
if ! command -v node &>/dev/null || ! command -v npm &>/dev/null; then
    curl -fsSL https://deb.nodesource.com/setup_${NODE_MAJOR}.x | bash - >> "$LOG_FILE" 2>&1
    apt-get install -y nodejs >> "$LOG_FILE" 2>&1
    log "Node.js $(node -v) installed"
else
    # Ensure npm is installed even if node exists
    if ! command -v npm &>/dev/null; then
        apt-get install -y npm >> "$LOG_FILE" 2>&1
    fi
    log "Node.js $(node -v) already installed"
fi

# =============================================================================
# Step 11: Clone & Build ServerPanel
# =============================================================================
step "11/13 — Building ServerPanel"

if [ -d "${INSTALL_DIR}/.git" ]; then
    log "Existing installation found, pulling latest..."
    cd "$INSTALL_DIR"
    git pull >> "$LOG_FILE" 2>&1
else
    log "Cloning repository..."
    git clone https://github.com/BetaZen-InfoTech/server-management.git "$INSTALL_DIR" >> "$LOG_FILE" 2>&1
    cd "$INSTALL_DIR"
fi

# Create .env
log "Generating .env configuration..."
cat > "${INSTALL_DIR}/.env" << ENVEOF
APP_ENV=production
LOG_LEVEL=info
# MONGO_PASS is stored as a standalone line so re-runs of install.sh can
# read it back and keep using the same credentials instead of rotating
# them and breaking the already-provisioned MongoDB user.
MONGO_PASS=${MONGO_PASS}
MONGO_URI=mongodb://serverpanel:${MONGO_PASS}@127.0.0.1:27017/serverpanel?authSource=admin
MONGO_DB_NAME=serverpanel
JWT_SECRET=${JWT_SECRET}
JWT_ACCESS_EXPIRY=15m
JWT_REFRESH_EXPIRY=168h
DOMAIN=${PANEL_DOMAIN}
SERVER_PORT=8080
SERVER_IP=${SERVER_IP}
TLS_CERT=
TLS_KEY=
AGENT_PORT=8443
AGENT_API_KEY=${AGENT_KEY}
MAIL_HOSTNAME=mail.${PANEL_DOMAIN}
BACKUP_DIR=/var/backups/serverpanel
BACKUP_ENCRYPTION_KEY=${BACKUP_KEY}
RATE_LIMIT_WHM=200
RATE_LIMIT_CPANEL=100
ENVEOF
chmod 600 "${INSTALL_DIR}/.env"

# Build backend
log "Building Go backend..."
cd "${INSTALL_DIR}/backend"
"${GO_DIR}/bin/go" build -o "${INSTALL_DIR}/bin/server" ./cmd/server >> "$LOG_FILE" 2>&1
"${GO_DIR}/bin/go" build -o "${INSTALL_DIR}/bin/seed" ./cmd/seed >> "$LOG_FILE" 2>&1
log "Backend built"

# Build frontend
log "Building frontend..."
cd "${INSTALL_DIR}/frontend"
npm install --legacy-peer-deps >> "$LOG_FILE" 2>&1
npx turbo build >> "$LOG_FILE" 2>&1
log "Frontend built"

# Create necessary directories
mkdir -p "${INSTALL_DIR}/bin"
mkdir -p /var/backups/serverpanel
mkdir -p /var/log/nginx

# =============================================================================
# Step 12: Configure Systemd & Nginx Proxy
# =============================================================================
step "12/13 — Configuring systemd service & nginx proxy"

# Create systemd service
cat > /etc/systemd/system/serverpanel.service << SVCEOF
[Unit]
Description=ServerPanel API Server
After=network.target mongod.service
Requires=mongod.service

[Service]
Type=simple
User=root
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/bin/server
Restart=always
RestartSec=5
EnvironmentFile=${INSTALL_DIR}/.env
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
SVCEOF

# Create nginx reverse proxy for the panel (with webmail)
cat > /etc/nginx/sites-available/serverpanel << NGXEOF
server {
    listen 80;
    server_name ${PANEL_DOMAIN};

    # Roundcube Webmail (with SSO auto-login from WHM)
    location ^~ /webmail/ {
        alias /var/lib/roundcube/public_html/;
        index index.php;

        location ~ ^/webmail/(.+\.php)\$ {
            alias /var/lib/roundcube/public_html/\$1;
            include fastcgi_params;
            fastcgi_pass unix:/var/run/php/php8.2-fpm.sock;
            fastcgi_param SCRIPT_FILENAME /var/lib/roundcube/public_html/\$1;
            fastcgi_intercept_errors on;
        }

        location ~ /\\. { deny all; }
    }

    # WebSocket support
    location /ws/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_read_timeout 3600s;
    }

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_read_timeout 86400;
        client_max_body_size 500M;
    }
}
NGXEOF

ln -sf /etc/nginx/sites-available/serverpanel /etc/nginx/sites-enabled/serverpanel

# Disable the default nginx site so it doesn't fight our vhost on port 80.
rm -f /etc/nginx/sites-enabled/default

# Seed database with admin user — surface failures instead of swallowing them.
log "Seeding admin user..."
cd "${INSTALL_DIR}"
if ! "${INSTALL_DIR}/bin/seed" >> "$LOG_FILE" 2>&1; then
    warn "seed binary exited non-zero — continuing, but admin user may be missing"
    tail -n 20 "$LOG_FILE" || true
fi

# Enable, (re)start, and health-check the serverpanel service. A plain
# `systemctl start` returns 0 as soon as the process is forked, so a backend
# that crashes on boot looks like a successful start. We now wait briefly
# and assert the unit is active; if not, we dump status + journal so the
# installer terminates with the real error visible on-screen.
systemctl daemon-reload
systemctl enable serverpanel >> "$LOG_FILE" 2>&1
systemctl restart serverpanel >> "$LOG_FILE" 2>&1 || true

# Wait up to ~10s for the unit to settle.
_sp_up=0
for i in 1 2 3 4 5 6 7 8 9 10; do
    if systemctl is-active --quiet serverpanel; then
        # Also require the listener to actually be bound before declaring victory.
        if ss -tln 2>/dev/null | grep -q ':8080\b'; then
            _sp_up=1
            break
        fi
    fi
    sleep 1
done

if [ "$_sp_up" -ne 1 ]; then
    err "serverpanel failed to start or is not listening on :8080"
    echo ""
    echo -e "${YELLOW}--- systemctl status serverpanel ---${NC}"
    systemctl status serverpanel --no-pager -l | tee -a "$LOG_FILE" || true
    echo ""
    echo -e "${YELLOW}--- journalctl -u serverpanel -n 80 ---${NC}"
    journalctl -u serverpanel --no-pager -n 80 | tee -a "$LOG_FILE" || true
    echo ""
    echo -e "${YELLOW}--- ss -tln (listening sockets) ---${NC}"
    ss -tln | tee -a "$LOG_FILE" || true
    exit 1
fi
log "serverpanel service is active and listening on :8080"

# Validate nginx config before reload — a broken vhost would kill :80 too.
if ! nginx -t >> "$LOG_FILE" 2>&1; then
    err "nginx config test failed"
    nginx -t 2>&1 | tee -a "$LOG_FILE" || true
    exit 1
fi
systemctl reload nginx >> "$LOG_FILE" 2>&1

# =============================================================================
# Firewall Setup
# =============================================================================
log "Configuring firewall..."
ufw allow 22/tcp >> "$LOG_FILE" 2>&1    # SSH
ufw allow 80/tcp >> "$LOG_FILE" 2>&1    # HTTP
ufw allow 443/tcp >> "$LOG_FILE" 2>&1   # HTTPS
ufw allow 53/tcp >> "$LOG_FILE" 2>&1    # DNS
ufw allow 53/udp >> "$LOG_FILE" 2>&1    # DNS
ufw allow 25/tcp >> "$LOG_FILE" 2>&1    # SMTP
ufw allow 587/tcp >> "$LOG_FILE" 2>&1   # Submission
ufw allow 993/tcp >> "$LOG_FILE" 2>&1   # IMAPS
ufw allow 995/tcp >> "$LOG_FILE" 2>&1   # POP3S
ufw allow 21/tcp >> "$LOG_FILE" 2>&1    # FTP
ufw --force enable >> "$LOG_FILE" 2>&1

# =============================================================================
# Try SSL for panel domain (if it has DNS)
# =============================================================================
if [ "$PANEL_DOMAIN" != "$SERVER_IP" ]; then
    log "Attempting SSL certificate for ${PANEL_DOMAIN}..."
    certbot certonly --nginx -d "$PANEL_DOMAIN" --non-interactive --agree-tos -m "$ADMIN_EMAIL" >> "$LOG_FILE" 2>&1 && {
        # Update nginx to use SSL
        cat > /etc/nginx/sites-available/serverpanel << NGXSSLEOF
server {
    listen 80;
    server_name ${PANEL_DOMAIN};
    return 301 https://\$host\$request_uri;
}

server {
    listen 443 ssl;
    server_name ${PANEL_DOMAIN};

    ssl_certificate /etc/letsencrypt/live/${PANEL_DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${PANEL_DOMAIN}/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_read_timeout 86400;
        client_max_body_size 500M;
    }
}
NGXSSLEOF
        nginx -t >> "$LOG_FILE" 2>&1 && systemctl reload nginx
        log "SSL configured for ${PANEL_DOMAIN}"

        # Update .env with TLS paths
        sed -i "s|^SERVER_PORT=.*|SERVER_PORT=443|" "${INSTALL_DIR}/.env"
        sed -i "s|^TLS_CERT=.*|TLS_CERT=/etc/letsencrypt/live/${PANEL_DOMAIN}/fullchain.pem|" "${INSTALL_DIR}/.env"
        sed -i "s|^TLS_KEY=.*|TLS_KEY=/etc/letsencrypt/live/${PANEL_DOMAIN}/privkey.pem|" "${INSTALL_DIR}/.env"
    } || warn "SSL failed (DNS may not point to this server yet)"
fi

# =============================================================================
# Done!
# =============================================================================
# Close out the last step's timer before the summary banner.
if [ -n "$CURRENT_STEP" ]; then
    _last_dur=$(( $(date +%s) - STEP_START_TS ))
    echo -e "  ${GREEN}✓${NC} completed in $(_fmt_duration "$_last_dur")"
    CURRENT_STEP=""
fi
INSTALL_TOTAL=$(( $(date +%s) - INSTALL_START_TS ))

echo ""
echo -e "${GREEN}============================================${NC}"
echo -e "${GREEN}   ServerPanel installed successfully!${NC}"
echo -e "${GREEN}   Total install time: $(_fmt_duration "$INSTALL_TOTAL")${NC}"
echo -e "${GREEN}============================================${NC}"
echo ""
if [ -f "/etc/letsencrypt/live/${PANEL_DOMAIN}/fullchain.pem" ]; then
    PANEL_SCHEME="https://"
    PANEL_BASE="${PANEL_SCHEME}${PANEL_DOMAIN}"
else
    PANEL_SCHEME="http://"
    PANEL_BASE="${PANEL_SCHEME}${PANEL_DOMAIN}"
fi
echo -e "  Panel URL:    ${CYAN}${PANEL_BASE}${NC}"
echo -e "  WHM:          ${CYAN}${PANEL_BASE}/whm${NC}"
echo -e "  cPanel:       ${CYAN}${PANEL_BASE}/cpanel${NC}"
echo ""
echo -e "  Admin Login:  ${YELLOW}${ADMIN_EMAIL}${NC}"
echo -e "  Admin Pass:   ${YELLOW}${ADMIN_PASS}${NC}"
echo ""
echo -e "  Server IP:    ${GREEN}${SERVER_IP}${NC}"
echo -e "  Install Dir:  ${GREEN}${INSTALL_DIR}${NC}"
echo -e "  Log File:     ${GREEN}${LOG_FILE}${NC}"
echo ""
echo -e "  ${BLUE}Manage:${NC}  systemctl {start|stop|restart} serverpanel"
echo -e "  ${BLUE}Logs:${NC}    journalctl -u serverpanel -f"
echo ""

} # end _serverpanel_install

_serverpanel_install "$@"
