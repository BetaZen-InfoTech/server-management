#!/bin/bash
# =============================================================================
# Betazen Server Panel — One-Click Installer
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
echo "  ____       _                            ____                  _ "
echo " | __ )  ___| |_ __ _ _______ _ __       |  _ \ __ _ _ __   ___| |"
echo " |  _ \ / _ \ __/ _\` |_  / _ \ '_ \      | |_) / _\` | '_ \ / _ \ |"
echo " | |_) |  __/ || (_| |/ /  __/ | | |     |  __/ (_| | | | |  __/ |"
echo " |____/ \___|\__\__,_/___\___|_| |_|     |_|   \__,_|_| |_|\___|_|"
echo -e "${NC}"
echo -e " ${BLUE}Betazen Server Panel — by BetaZen InfoTech${NC}"
echo -e " Server IP: ${GREEN}${SERVER_IP}${NC}"
echo ""

# --- Prompt for configuration ---
# When invoked via `curl ... | bash`, stdin is the piped script itself, so we
# must read interactive prompts from the controlling terminal directly.
#
# `[ -r /dev/tty ]` was the test before, but on a non-tty SSH session
# (paramiko exec_command, GitHub Actions runner, automated provisioner)
# /dev/tty exists and the `-r` test passes, but the subsequent
# `< /dev/tty` open fails with "No such device or address" and the
# read aborts the whole installer. Probe by actually opening /dev/tty
# and discarding the fd — only commit to TTY_IN=/dev/tty when the
# probe succeeds. Falls through to /dev/stdin (which gets EOF in
# fully-automated runs, harmless because every prompt has a default).
# MUST use a subshell — when `exec` itself fails on a redirection, bash
# aborts the *current* shell (not just the command), and `2>/dev/null`
# only swallows the error message, not the exit. The subshell isolates
# the abort: it dies, the parent sees a non-zero condition, falls through.
if (exec 0</dev/tty) 2>/dev/null; then
    TTY_IN=/dev/tty
else
    TTY_IN=/dev/stdin
fi

# Each read is wrapped in `|| true` so an unattended install (no tty,
# stdin closed) doesn't abort on the first prompt — the variable stays
# empty and the `:- default` substitution below picks the right value.
read -p "Enter panel domain (e.g., panel.example.com) [default: $SERVER_IP]: " PANEL_DOMAIN < "$TTY_IN" || true
PANEL_DOMAIN=${PANEL_DOMAIN:-$SERVER_IP}

read -p "Enter admin email [default: admin@betazeninfotech.com]: " ADMIN_EMAIL < "$TTY_IN" || true
ADMIN_EMAIL=${ADMIN_EMAIL:-admin@betazeninfotech.com}

read -sp "Enter admin password [default: admin123]: " ADMIN_PASS < "$TTY_IN" || true
ADMIN_PASS=${ADMIN_PASS:-admin123}
echo ""

read -sp "Set MongoDB password [default: auto-generated]: " MONGO_PASS < "$TTY_IN" || true
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
EX_APP_ENC_KEY=""
if [ -f "$EXISTING_ENV_FILE" ]; then
    EX_MONGO_PASS=$(_ex_env MONGO_PASS)
    EX_JWT_SECRET=$(_ex_env JWT_SECRET)
    EX_AGENT_KEY=$(_ex_env AGENT_API_KEY)
    EX_BACKUP_KEY=$(_ex_env BACKUP_ENCRYPTION_KEY)
    EX_APP_ENC_KEY=$(_ex_env APP_ENCRYPTION_KEY)
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
# APP_ENCRYPTION_KEY is the AES-GCM key the backend uses to encrypt
# Deploy Software Personal Access Tokens (and any other secret stored
# in mongo by the panel). It MUST be persistent — if the key changes
# between restarts every saved PAT becomes un-decryptable, every
# subsequent `git pull` fails with "decrypt PAT: cipher: message
# authentication failed", and the operator has to re-paste every PAT.
# Reuse the existing value when re-running install.sh; mint a fresh
# 32-byte key on first install and write it into .env below.
APP_ENC_KEY="${EX_APP_ENC_KEY:-$(openssl rand -hex 32)}"

echo ""
log "Installation starting... (log: $LOG_FILE)"
echo -e "  ${YELLOW}Expected duration: 8–15 minutes${NC} on a clean VPS with a good network."
echo -e "  ${YELLOW}Each step prints its own elapsed time when it finishes.${NC}"
echo -e "  ${YELLOW}Detailed output (including apt download speeds) is in: ${LOG_FILE}${NC}"
echo -e "  ${YELLOW}Tip: in a second terminal, run  'tail -f ${LOG_FILE}'  to watch live.${NC}"
echo ""

# Prevent `unattended-upgrades` from grabbing the dpkg lock mid-install.
# Ubuntu cloud images run apt-daily.timer + unattended-upgrades.timer on
# boot; if either one fires while we're between apt-get calls, the next
# apt-get fails with "Could not get lock /var/lib/dpkg/lock-frontend".
# Stop the units and disable their timers for the duration of this run.
log "Stopping unattended-upgrades to avoid dpkg lock conflicts..."
systemctl stop unattended-upgrades.service apt-daily.service apt-daily-upgrade.service 2>/dev/null || true
systemctl stop apt-daily.timer apt-daily-upgrade.timer 2>/dev/null || true
systemctl disable apt-daily.timer apt-daily-upgrade.timer 2>/dev/null || true

# Helper: wait for any outstanding apt/dpkg lock, in case something else
# (a human running apt, a systemd unit we missed) still holds it. Times
# out after 10 minutes.
_wait_for_apt() {
    local waited=0
    while fuser /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/lib/apt/lists/lock >/dev/null 2>&1; do
        if [ "$waited" -eq 0 ]; then
            warn "Another apt/dpkg process is running — waiting for it to finish..."
        fi
        sleep 5
        waited=$((waited + 5))
        if [ "$waited" -ge 600 ]; then
            err "Timed out after 10 minutes waiting for apt lock"
            fuser -v /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/lib/apt/lists/lock 2>&1 | tee -a "$LOG_FILE" || true
            return 1
        fi
    done
}

# -----------------------------------------------------------------------------
# PM2 setup — installs pm2 globally, enables log rotation, and wires it into
# systemd so any pm2-managed apps come back automatically after a reboot.
#
# Why this is a dedicated function:
#  - `pm2 startup systemd` writes a systemd unit whose exact name depends on
#    $USER / $HOME, so we have to resolve that once and re-use it for enable
#    and status checks.
#  - `pm2-logrotate` defaults are way too permissive (no compression, retains
#    30 uncompressed copies) — without tuning, a chatty Node app can fill the
#    disk in days.
#  - Re-running install.sh must not re-register systemd or corrupt the dump
#    file, so every step is idempotent.
# -----------------------------------------------------------------------------
setup_pm2() {
    if ! command -v pm2 &>/dev/null; then
        log "Installing PM2 (global npm)..."
        npm install -g pm2 >> "$LOG_FILE" 2>&1
    fi
    log "PM2 $(pm2 -v 2>/dev/null || echo '?') present"

    # Install pm2-logrotate so app logs don't grow unbounded.
    if ! pm2 list 2>/dev/null | grep -q pm2-logrotate; then
        pm2 install pm2-logrotate >> "$LOG_FILE" 2>&1 || warn "pm2-logrotate install failed (non-fatal)"
    fi
    pm2 set pm2-logrotate:max_size 50M           >> "$LOG_FILE" 2>&1 || true
    pm2 set pm2-logrotate:retain 10              >> "$LOG_FILE" 2>&1 || true
    pm2 set pm2-logrotate:compress true          >> "$LOG_FILE" 2>&1 || true
    pm2 set pm2-logrotate:rotateInterval '0 0 * * *' >> "$LOG_FILE" 2>&1 || true

    # Register systemd unit so `pm2 resurrect` fires on boot. `pm2 startup`
    # prints a command; running it once is enough. Subsequent reruns are
    # no-ops because the unit already exists.
    if [ ! -f /etc/systemd/system/pm2-root.service ]; then
        log "Registering pm2 systemd unit (pm2-root)..."
        env PATH="$PATH:/usr/bin" pm2 startup systemd -u root --hp /root >> "$LOG_FILE" 2>&1 || \
            warn "pm2 startup registration failed (non-fatal)"
    fi
    systemctl enable pm2-root.service >> "$LOG_FILE" 2>&1 || true

    # Ensure a dump file exists so `pm2 resurrect` on reboot doesn't error
    # out when the process list is empty.
    mkdir -p /root/.pm2
    [ -f /root/.pm2/dump.pm2 ] || pm2 save --force >> "$LOG_FILE" 2>&1 || true

    log "PM2 ready (auto-restart on reboot enabled)"
}

# Shadow `apt-get` with a function that waits for the dpkg lock first.
# Every existing `apt-get ...` call in this script now routes through
# this wrapper automatically. `command apt-get` bypasses the function
# inside the wrapper itself to invoke the real binary.
apt-get() {
    _wait_for_apt || return 1
    command apt-get "$@"
}
echo ""

# =============================================================================
# Step 1: System Update & Base Packages
# =============================================================================
step "1/13 — Updating system and installing base packages"
export DEBIAN_FRONTEND=noninteractive
dpkg --configure --force-confold -a >> "$LOG_FILE" 2>&1 || true
apt-get update -y >> "$LOG_FILE" 2>&1
# Base packages: network tools (curl/wget/dnsutils/net-tools), archive utils
# (unzip/zip/bzip2/xz-utils — File Manager extract/download needs these),
# rsync (used by the transfer flow to move files between servers), acl
# (setfacl — shared www-data/user access on per-domain dirs), cron/logrotate
# (expected by every systemd unit we ship), build toolchain and basic editors.
apt-get install -y \
    curl wget git build-essential software-properties-common \
    gnupg lsb-release ca-certificates apt-transport-https \
    ufw fail2ban unzip zip bzip2 xz-utils jq dnsutils sshpass \
    rsync acl cron logrotate net-tools iputils-ping \
    pigz \
    htop nano vim \
    >> "$LOG_FILE" 2>&1
log "Base packages installed"

# -----------------------------------------------------------------------------
# Application-runtime support packages — these are pulled in here (alongside
# the base layer) so the Deploy Software / Apps wizard's preset install
# commands work on first run. Without them, deploys fail with cryptic
# build errors:
#   * python3-venv   — `python3 -m venv venv` fails with "ensurepip is not
#                       available", killing every flask/django/fastapi deploy
#   * python3-pip    — venv creation needs pip; also lets ad-hoc pip work
#   * python3-dev    — wheels (psycopg2, lxml, etc.) need Python headers
#                       to build C extensions during pip install
#   * python3-full   — pulls in tkinter etc. so all stdlib modules work
#   * libffi-dev / libssl-dev / libxml2-dev / libxslt1-dev — common
#                       transitive build deps for pip-installed C wheels
#   * pkg-config     — needed by many Go modules with cgo dependencies
# Wrapped in `|| true` so a missing package on a slim distro doesn't abort
# the whole installer.
apt-get install -y \
    python3 python3-venv python3-pip python3-dev python3-full \
    libffi-dev libssl-dev libxml2-dev libxslt1-dev pkg-config \
    >> "$LOG_FILE" 2>&1 || true
# Some distros ship a versioned -venv package (python3.12-venv on Ubuntu
# 24.04, python3.10-venv on 22.04) that's not aliased by the unversioned
# name. Detect and install the matching one so `python3 -m venv` works.
PY_VER=$(python3 -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")' 2>/dev/null)
if [ -n "$PY_VER" ]; then
    apt-get install -y "python${PY_VER}-venv" "python${PY_VER}-dev" \
        >> "$LOG_FILE" 2>&1 || true
fi
log "Python runtime + venv installed"

# -----------------------------------------------------------------------------
# Swap file — small VPS images (1–2 GB RAM) OOM during `go build` and
# `turbo build`. Create a 2 GB swapfile if none exists and total memory is
# under 4 GB. Skips silently on systems with swap already configured.
# -----------------------------------------------------------------------------
_total_mem_mb=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
_have_swap=$(swapon --show=NAME --noheadings 2>/dev/null | head -1)
if [ -z "$_have_swap" ] && [ "${_total_mem_mb:-0}" -lt 4096 ]; then
    log "Low memory detected (${_total_mem_mb} MB) and no swap — creating 2G swapfile..."
    if ! fallocate -l 2G /swapfile >> "$LOG_FILE" 2>&1; then
        dd if=/dev/zero of=/swapfile bs=1M count=2048 >> "$LOG_FILE" 2>&1 || true
    fi
    chmod 600 /swapfile
    mkswap /swapfile >> "$LOG_FILE" 2>&1 || true
    swapon /swapfile >> "$LOG_FILE" 2>&1 || true
    grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
    log "Swap enabled: $(swapon --show=NAME,SIZE --noheadings | head -1)"
fi

# =============================================================================
# Step 2: Nginx
# =============================================================================
step "2/13 — Installing Nginx"
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
step "3/13 — Installing PHP 8.2"
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
step "4/13 — Installing MongoDB ${MONGO_VERSION}"
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
step "5/13 — Installing MariaDB"
if ! command -v mysql &>/dev/null; then
    apt-get install -y mariadb-server mariadb-client >> "$LOG_FILE" 2>&1
    systemctl enable mariadb >> "$LOG_FILE" 2>&1
    systemctl start mariadb >> "$LOG_FILE" 2>&1
    log "MariaDB installed"
else
    log "MariaDB already installed"
fi

# phpMyAdmin — fully self-contained, idempotent install. Every block
# below is safe to re-run on an existing deploy (the previous standalone
# scripts/install-phpmyadmin.sh has been merged into install.sh). Uses the
# upstream tarball: apt's phpmyadmin package pulls in apache2 + interactive
# dbconfig prompts that don't fit our nginx-only stack.
install_phpmyadmin() {
    local PMA_VER=5.2.1
    # 1. Tarball (skip if already extracted)
    if [ ! -d /usr/share/phpmyadmin ]; then
        log "Installing phpMyAdmin ${PMA_VER}…"
        ( cd /tmp \
          && wget -q "https://files.phpmyadmin.net/phpMyAdmin/${PMA_VER}/phpMyAdmin-${PMA_VER}-all-languages.tar.gz" 2>>"$LOG_FILE" \
          && tar xzf "phpMyAdmin-${PMA_VER}-all-languages.tar.gz" \
          && mv "phpMyAdmin-${PMA_VER}-all-languages" /usr/share/phpmyadmin \
          && rm -f "phpMyAdmin-${PMA_VER}-all-languages.tar.gz" )
        chown -R www-data:www-data /usr/share/phpmyadmin
        log "phpMyAdmin ${PMA_VER} extracted to /usr/share/phpmyadmin"
    else
        log "phpMyAdmin already present at /usr/share/phpmyadmin"
    fi
    # 2. PHP extensions (always run — apt is a no-op if already installed)
    apt-get install -y php-mbstring php-mysql php-zip php-gd php-json php-xml >> "$LOG_FILE" 2>&1 || true
    # 3. Runtime directories (safe to re-create)
    mkdir -p /var/lib/phpmyadmin/tmp /etc/phpmyadmin
    chown -R www-data:www-data /var/lib/phpmyadmin
    chmod 770 /var/lib/phpmyadmin/tmp
    # 4a. PMA signon secret — HMAC-SHA256 key shared between the panel
    #     binary and /usr/share/phpmyadmin/_signon.php. The panel signs
    #     short-lived auto-login tokens; the shim verifies + sets a PMA
    #     signon session. Generated once, preserved across re-runs.
    if [ ! -f /etc/phpmyadmin/signon-secret ]; then
        openssl rand -hex 32 > /etc/phpmyadmin/signon-secret
        chmod 640 /etc/phpmyadmin/signon-secret
        chown root:www-data /etc/phpmyadmin/signon-secret
        log "Generated /etc/phpmyadmin/signon-secret"
    fi
    # Mirror the secret into the panel's .env so both processes pick it up.
    if [ -f /opt/serverpanel/.env ] && ! grep -q '^PMA_SIGNON_SECRET=' /opt/serverpanel/.env; then
        echo "PMA_SIGNON_SECRET=$(cat /etc/phpmyadmin/signon-secret)" >> /opt/serverpanel/.env
    fi

    # 4b. /etc/phpmyadmin/config.inc.php — only generate on first install
    #    (preserves existing blowfish secret across re-runs so active
    #    sessions don't get invalidated). Two server entries:
    #      $i=1: cookie auth — manual login screen at /phpmyadmin/
    #      $i=2: signon auth — fed by _signon.php after token verification
    if [ ! -f /etc/phpmyadmin/config.inc.php ]; then
        local PMA_SECRET=$(openssl rand -hex 16)
        cat > /etc/phpmyadmin/config.inc.php <<PMACONF
<?php
\$cfg['blowfish_secret'] = '${PMA_SECRET}';
\$cfg['UploadDir'] = '/var/lib/phpmyadmin/tmp';
\$cfg['SaveDir'] = '/var/lib/phpmyadmin/tmp';
\$cfg['TempDir'] = '/var/lib/phpmyadmin/tmp';
\$cfg['ShowPhpInfo'] = false;
\$cfg['ShowServerInfo'] = false;
\$cfg['ShowChgPassword'] = false;

// Server 1 — cookie auth, used for direct manual logins at /phpmyadmin/.
\$i = 1;
\$cfg['Servers'][\$i]['auth_type'] = 'cookie';
\$cfg['Servers'][\$i]['host'] = '127.0.0.1';
\$cfg['Servers'][\$i]['compress'] = false;
\$cfg['Servers'][\$i]['AllowNoPassword'] = false;
\$cfg['Servers'][\$i]['hide_db'] = '^(information_schema|performance_schema|mysql|sys|phpmyadmin)\$';

// Server 2 — signon auth. _signon.php sets PMA_single_signon_user/pass
// in the named PHP session and redirects to /phpmyadmin/?server=2&db=...
\$i = 2;
\$cfg['Servers'][\$i]['auth_type'] = 'signon';
\$cfg['Servers'][\$i]['host'] = '127.0.0.1';
\$cfg['Servers'][\$i]['compress'] = false;
\$cfg['Servers'][\$i]['SignonSession'] = 'panel_pma_signon';
\$cfg['Servers'][\$i]['SignonURL'] = '/phpmyadmin/_signon.php';
\$cfg['Servers'][\$i]['LogoutURL'] = '/phpmyadmin/?logout=1';
\$cfg['Servers'][\$i]['hide_db'] = '^(information_schema|performance_schema|mysql|sys|phpmyadmin)\$';

\$cfg['ServerDefault'] = 1;
PMACONF
        log "Generated /etc/phpmyadmin/config.inc.php with cookie+signon auth"
    fi
    # Always (re)link the config into the install dir — webroot may have
    # been updated/wiped between runs.
    ln -sf /etc/phpmyadmin/config.inc.php /usr/share/phpmyadmin/config.inc.php

    # 4c. /usr/share/phpmyadmin/_signon.php — auto-login shim. Always
    #     overwrite (panel-owned, no user data inside).
    cat > /usr/share/phpmyadmin/_signon.php <<'PHPSIGNON'
<?php
// Auto-login shim for the panel. The panel signs a short-lived token with
// the secret in /etc/phpmyadmin/signon-secret; we verify the HMAC, set the
// PMA signon session, and redirect to the requested database. Token is
// "<base64url(json{user,pass,db,exp})>.<hex hmac-sha256>".

$secretFile = '/etc/phpmyadmin/signon-secret';
if (!is_readable($secretFile)) { http_response_code(500); exit('signon disabled: secret file unreadable'); }
$secret = trim(file_get_contents($secretFile));
if ($secret === '') { http_response_code(500); exit('signon disabled: empty secret'); }

$tok = $_GET['t'] ?? '';
$parts = explode('.', $tok, 2);
if (count($parts) !== 2) { http_response_code(400); exit('bad token shape'); }
list($encPayload, $sig) = $parts;
$expected = hash_hmac('sha256', $encPayload, $secret);
if (!hash_equals($expected, $sig)) { http_response_code(403); exit('signature mismatch'); }
// base64url decode
$json = base64_decode(strtr($encPayload, '-_', '+/') . str_repeat('=', (4 - strlen($encPayload) % 4) % 4));
$data = $json ? json_decode($json, true) : null;
if (!is_array($data)) { http_response_code(400); exit('bad payload'); }
if (!isset($data['user'], $data['pass'], $data['db'], $data['exp'])) { http_response_code(400); exit('missing fields'); }
if ((int)$data['exp'] < time()) { http_response_code(410); exit('token expired'); }

// Set the signon session phpMyAdmin reads when auth_type=signon.
session_name('panel_pma_signon');
session_start();
$_SESSION['PMA_single_signon_user']     = $data['user'];
$_SESSION['PMA_single_signon_password'] = $data['pass'];
$_SESSION['PMA_single_signon_host']     = '127.0.0.1';
$_SESSION['PMA_single_signon_port']     = 3306;
session_write_close();

// Redirect to phpMyAdmin's index, hitting server=2 (signon) and pre-selecting db.
$db = rawurlencode($data['db']);
header('Location: /phpmyadmin/index.php?server=2&db=' . $db . '&route=/database/structure');
exit;
PHPSIGNON
    chown www-data:www-data /usr/share/phpmyadmin/_signon.php
    chmod 644 /usr/share/phpmyadmin/_signon.php
    log "Wrote /usr/share/phpmyadmin/_signon.php (auto-login shim)"
    # 5. Nginx snippet — always rewrite (PHP-FPM socket version may have
    #    changed since previous install; safe because the file is panel-owned).
    local PMA_FPM_SOCK=$(ls /run/php/php8.2-fpm.sock 2>/dev/null || ls /run/php/php8.1-fpm.sock 2>/dev/null || ls /run/php/php-fpm.sock 2>/dev/null | head -1)
    if [ -z "$PMA_FPM_SOCK" ]; then
        log "WARN: no PHP-FPM socket found — phpMyAdmin nginx snippet not written"
        return
    fi
    mkdir -p /etc/nginx/snippets
    cat > /etc/nginx/snippets/phpmyadmin.conf <<NGCONF
# phpMyAdmin location — included from the panel vhost. Cookie auth:
# the operator logs in with the per-DB MySQL user the panel created,
# the panel's UI form-POSTs the credentials so it's "click to open".
location ^~ /phpmyadmin/ {
    alias /usr/share/phpmyadmin/;
    index index.php;
    try_files \$uri \$uri/ =404;
    location ~ ^/phpmyadmin/(.+\.php)\$ {
        alias /usr/share/phpmyadmin/\$1;
        fastcgi_pass unix:${PMA_FPM_SOCK};
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME /usr/share/phpmyadmin/\$1;
        include fastcgi_params;
    }
    location ~* ^/phpmyadmin/(.+\.(jpg|jpeg|gif|css|png|js|ico|html|xml|txt|svg|woff|woff2))\$ {
        alias /usr/share/phpmyadmin/\$1;
    }
}
NGCONF
    log "Wrote /etc/nginx/snippets/phpmyadmin.conf (PHP-FPM: ${PMA_FPM_SOCK})"
    # 6. Inject the include into an existing panel vhost (Step 11 below
    #    regenerates the vhost with the include baked in for fresh installs;
    #    this is the back-port path for re-runs against an older deploy
    #    whose vhost predates the include).
    local PMA_VHOST=/etc/nginx/sites-enabled/serverpanel
    [ -f "$PMA_VHOST" ] || PMA_VHOST=/etc/nginx/sites-available/serverpanel
    if [ -f "$PMA_VHOST" ] && ! grep -q 'snippets/phpmyadmin.conf' "$PMA_VHOST"; then
        log "Patching $PMA_VHOST to include phpmyadmin snippet"
        awk 'BEGIN{added=0} /^[[:space:]]*location[[:space:]]+\/[[:space:]]*\{/ && !added{print "    include snippets/phpmyadmin.conf;"; added=1} {print}' "$PMA_VHOST" > "$PMA_VHOST.new"
        if grep -q 'snippets/phpmyadmin.conf' "$PMA_VHOST.new"; then
            mv "$PMA_VHOST.new" "$PMA_VHOST"
            nginx -t >> "$LOG_FILE" 2>&1 && systemctl reload nginx >> "$LOG_FILE" 2>&1
        else
            rm -f "$PMA_VHOST.new"
            log "WARN: couldn't patch $PMA_VHOST (no 'location /' found)"
        fi
    fi
}
install_phpmyadmin

# =============================================================================
# Step 6: Email Stack (Postfix + Dovecot + OpenDKIM)
# =============================================================================
step "6/13 — Installing Email Stack (Postfix, Dovecot, OpenDKIM)"
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

# Create Postfix config files. CRITICAL: the file names MUST be
# virtual_mailbox_domains and virtual_mailbox_maps (matching the
# Postfix directives we set in main.cf below). The earlier
# "virtual_domains" / "virtual_mailboxes" names looked plausible but
# Postfix never reads them — it reads only what main.cf points at.
touch /etc/postfix/virtual_mailbox_domains \
      /etc/postfix/virtual_mailbox_maps \
      /etc/postfix/virtual_alias_maps
postmap /etc/postfix/virtual_mailbox_domains 2>/dev/null || true
postmap /etc/postfix/virtual_mailbox_maps    2>/dev/null || true
postmap /etc/postfix/virtual_alias_maps      2>/dev/null || true

# Wire Postfix's main.cf to USE the files above + Dovecot's LMTP socket
# for delivery. Without this, a fresh install's main.cf has empty
# virtual_mailbox_maps and Postfix bounces inbound mail with
# "User unknown in virtual mailbox table". Idempotent: postconf -e
# replaces existing values rather than duplicating them.
postconf -e "virtual_mailbox_domains = hash:/etc/postfix/virtual_mailbox_domains"
postconf -e "virtual_mailbox_maps    = hash:/etc/postfix/virtual_mailbox_maps"
postconf -e "virtual_alias_maps      = hash:/etc/postfix/virtual_alias_maps"
postconf -e "virtual_mailbox_base    = /var/mail/vhosts"
postconf -e "virtual_minimum_uid     = 100"
postconf -e "virtual_uid_maps        = static:5000"
postconf -e "virtual_gid_maps        = static:5000"
postconf -e "virtual_transport       = lmtp:unix:private/dovecot-lmtp"

# mydestination must NOT include any virtual hosting domains — those
# go through virtual_mailbox_*. Strip down to just localhost so
# Postfix doesn't try to deliver virtual mail through local(8).
postconf -e "mydestination = localhost.\$mydomain, localhost"

# Listen on every interface so external mail can reach us; the
# debconf-installed default of "loopback-only" silently kills inbound.
postconf -e "inet_interfaces = all"

# Force IPv4 for outbound SMTP. Our hosts generally carry an IPv6
# address without a matching PTR record, and Gmail enforces
# 5.7.25 "no PTR" on IPv6 — half the outbound delivery would bounce
# non-deterministically depending on which family Postfix tried first.
# Pin to ipv4 so DKIM + SPF produce consistent results.
postconf -e "inet_protocols = ipv4"

# Postfix's smtp(8) daemon runs under chroot=y by default
# (/var/spool/postfix), which means it reads /var/spool/postfix/etc/
# NOT the real /etc/. If the chroot's resolv.conf points at a
# systemd-resolved stub socket that isn't reachable from inside the
# chroot, DNS lookups for external hostnames (gmail.com, etc.) fail
# with "Host or domain name not found. Name service error for
# name=gmail.com type=MX: Host not found, try again" and every
# outbound message gets deferred.
#
# Sync the resolver + hosts + nsswitch + services files from the host
# into the chroot with real nameservers so DNS works. Also install a
# systemd path unit so the sync repeats when /etc/resolv.conf changes
# (e.g. after netplan apply or a DHCP lease renewal).
install -d -m 0755 /var/spool/postfix/etc
# Follow symlinks so we copy the RESOLVED file, not a link to
# /run/systemd/resolve/stub-resolv.conf that's invisible from chroot.
cp -fL /etc/resolv.conf    /var/spool/postfix/etc/resolv.conf
cp -fL /etc/hosts          /var/spool/postfix/etc/hosts
cp -fL /etc/nsswitch.conf  /var/spool/postfix/etc/nsswitch.conf
cp -fL /etc/host.conf      /var/spool/postfix/etc/host.conf 2>/dev/null || true
cp -fL /etc/services       /var/spool/postfix/etc/services
chmod 0644 /var/spool/postfix/etc/resolv.conf \
           /var/spool/postfix/etc/hosts \
           /var/spool/postfix/etc/nsswitch.conf \
           /var/spool/postfix/etc/services

# If /etc/resolv.conf is a systemd-resolved stub pointing at 127.0.0.53
# (common on Ubuntu 22/24), that address isn't reachable from the
# chroot — rewrite to real upstream resolvers.
if grep -q '127.0.0.53' /var/spool/postfix/etc/resolv.conf 2>/dev/null; then
    cat > /var/spool/postfix/etc/resolv.conf <<'RESOLVEOF'
nameserver 8.8.8.8
nameserver 1.1.1.1
nameserver 8.8.4.4
options timeout:3 attempts:2
RESOLVEOF
fi

# systemd path unit — re-sync the chroot's resolv.conf whenever the
# host's changes. Prevents a slow-creeping regression where netplan
# rewrites resolv.conf and suddenly outbound mail bounces again.
cat > /etc/systemd/system/postfix-chroot-sync.service <<'SVCEOF'
[Unit]
Description=Sync /etc resolver files into postfix chroot
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/bin/install -d -m 0755 /var/spool/postfix/etc
ExecStart=/bin/cp -fL /etc/resolv.conf /var/spool/postfix/etc/resolv.conf
ExecStart=/bin/cp -fL /etc/hosts /var/spool/postfix/etc/hosts
ExecStart=/bin/cp -fL /etc/nsswitch.conf /var/spool/postfix/etc/nsswitch.conf
SVCEOF
cat > /etc/systemd/system/postfix-chroot-sync.path <<'PATHEOF'
[Unit]
Description=Watch /etc/resolv.conf for changes and resync postfix chroot

[Path]
PathChanged=/etc/resolv.conf
Unit=postfix-chroot-sync.service

[Install]
WantedBy=multi-user.target
PATHEOF
systemctl daemon-reload >> "$LOG_FILE" 2>&1
systemctl enable --now postfix-chroot-sync.path >> "$LOG_FILE" 2>&1 || true

# Create Dovecot users file. CRITICAL: chgrp dovecot + mode 0640 so the
# auth process can read it. Default root:root 0640 = "Permission denied"
# from auth, every delivery bounces with 451 4.3.0 Temporary internal
# error.
touch /etc/dovecot/users
chgrp dovecot /etc/dovecot/users
chmod 0640 /etc/dovecot/users

# Deterministic Dovecot override file.
#
# History: the old setup sed-patched individual stock conf.d/* files
# and ONLY worked when the stock files matched very specific byte
# sequences. Ubuntu 24.04 ships slightly different defaults (comment
# wording, spacing, even whether `!include auth-system.conf.ext` is
# present at all), so on fresh installs the patches quietly no-opped
# and:
#   * IMAP/POP3 tried PAM auth against /etc/passwd → virtual users
#     failed every login.
#   * `/var/spool/postfix/private/auth` was never created → Roundcube
#     got "SMTP Error (): Authentication failed".
#   * LMTP socket was never exposed → every inbound mail deferred.
#
# Dovecot reads conf.d/*.conf in lexical order, so a 99-panel.conf we
# own completely wins. Writing it deterministically — instead of
# trying to sed the stock files — makes the fix survive any upstream
# default change. Safe to re-run: we overwrite the file each install.
cat > /etc/dovecot/conf.d/99-panel.conf << 'DOVE99'
# Managed by ServerPanel — do not hand-edit. Overrides stock defaults
# so Dovecot uses our /etc/dovecot/users passwd-file for all auth,
# exposes its SASL socket to Postfix, and drops mail into the
# per-owner maildir tree the panel writes for each mailbox.

protocols = imap pop3 lmtp

# Virtual users live in /etc/dovecot/users (written by EmailService
# on every CreateMailbox). Disable the system-PAM passdb entirely.
passdb {
  driver = passwd-file
  args = scheme=SHA512-CRYPT username_format=%u /etc/dovecot/users
}
userdb {
  driver = passwd-file
  args = username_format=%u /etc/dovecot/users
  default_fields = uid=5000 gid=5000 home=/var/mail/vhosts/%d/%n
}

# The per-row userdb_mail=maildir:/home/<owner>/mail/... override in
# /etc/dovecot/users wins; this fallback catches rows that pre-date
# the override (e.g. re-imported from a legacy file).
mail_location = maildir:~/Maildir
mail_privileged_group = vmail

# PLAIN/LOGIN over TLS so Roundcube + external clients can AUTH.
# smtpd_tls_auth_only on the Postfix side keeps AUTH off the wire
# until TLS is negotiated, so allowing plaintext here is safe.
disable_plaintext_auth = no
auth_mechanisms = plain login

service auth {
  # Postfix's smtpd reads this socket to authenticate submission/smtps
  # clients. `mode 0660` + `user = postfix` makes it accessible only
  # to Postfix, not to any local user.
  unix_listener /var/spool/postfix/private/auth {
    mode = 0660
    user = postfix
    group = postfix
  }
}

service lmtp {
  # Postfix delivers to virtual mailboxes via LMTP over this socket.
  # Without it, virtual_transport = lmtp:unix:private/dovecot-lmtp
  # in Postfix's main.cf would defer every delivery.
  unix_listener /var/spool/postfix/private/dovecot-lmtp {
    mode = 0600
    user = postfix
    group = postfix
  }
}

postmaster_address = postmaster@${PANEL_DOMAIN}
DOVE99

# Stop the stock auth-system.conf.ext from dragging PAM back in. Some
# Ubuntu builds include it uncommented from 10-auth.conf — we override
# the passdb in 99-panel.conf above, but disabling the PAM include
# removes an extra failure point.
if [ -f /etc/dovecot/conf.d/10-auth.conf ]; then
    sed -i 's|^!include auth-system.conf.ext|#!include auth-system.conf.ext|' \
        /etc/dovecot/conf.d/10-auth.conf || true
fi

# Create OpenDKIM directories
mkdir -p /etc/opendkim/keys
touch /etc/opendkim/signing.table /etc/opendkim/key.table /etc/opendkim/trusted.hosts
echo "127.0.0.1" > /etc/opendkim/trusted.hosts
echo "::1" >> /etc/opendkim/trusted.hosts

# Wire OpenDKIM through a Postfix-readable Unix socket. The Debian
# default ships a TCP listener that Postfix can't easily reach from
# inside chroot — by switching to a unix socket under
# /var/spool/postfix/opendkim/ we get a single, simple path Postfix's
# milter directives can name. Without this whole block, every outbound
# message lands at the recipient with NO DKIM-Signature header.
mkdir -p /var/spool/postfix/opendkim
chown opendkim:postfix /var/spool/postfix/opendkim
chmod 0750 /var/spool/postfix/opendkim
usermod -aG opendkim postfix 2>/dev/null || true

cat > /etc/opendkim.conf << 'OPENDKIM_EOF'
Syslog              yes
SyslogSuccess       yes
LogWhy              yes
UMask               002
Mode                sv
Canonicalization    relaxed/simple
ExternalIgnoreList  refile:/etc/opendkim/trusted.hosts
InternalHosts       refile:/etc/opendkim/trusted.hosts
KeyTable            refile:/etc/opendkim/key.table
SigningTable        refile:/etc/opendkim/signing.table
Socket              local:/var/spool/postfix/opendkim/opendkim.sock
PidFile             /run/opendkim/opendkim.pid
OversignHeaders     From
TrustAnchorFile     /usr/share/dns/root.key
UserID              opendkim:opendkim
OPENDKIM_EOF

# Submission (587) + smtps (465) for SMTP-AUTH outbound. Without these,
# users can't send mail at all — only port 25 is open and that doesn't
# accept AUTH. postconf -M is the modern, idempotent way to declare a
# master.cf service.
postconf -e "smtpd_sasl_type = dovecot"
postconf -e "smtpd_sasl_path = private/auth"
postconf -e "smtpd_sasl_auth_enable = yes"
postconf -e "smtpd_sasl_security_options = noanonymous"
postconf -e "broken_sasl_auth_clients = yes"
postconf -e "smtpd_tls_auth_only = yes"

# Snake-oil TLS cert is fine until the operator points a real domain at
# this box and runs certbot. The submission/smtps services need SOME
# cert to even start.
apt-get install -y ssl-cert >> "$LOG_FILE" 2>&1
[ -f /etc/ssl/certs/ssl-cert-snakeoil.pem ] || make-ssl-cert generate-default-snakeoil --force-overwrite 2>/dev/null || true
postconf -e "smtpd_tls_cert_file = /etc/ssl/certs/ssl-cert-snakeoil.pem"
postconf -e "smtpd_tls_key_file  = /etc/ssl/private/ssl-cert-snakeoil.key"
postconf -e "smtpd_tls_security_level = may"
postconf -e "smtp_tls_security_level  = may"

postconf -M submission/inet="submission inet n - y - - smtpd"
postconf -P "submission/inet/smtpd_tls_security_level=encrypt"
postconf -P "submission/inet/smtpd_sasl_auth_enable=yes"
postconf -P "submission/inet/smtpd_client_restrictions=permit_sasl_authenticated,reject"
postconf -P "submission/inet/smtpd_recipient_restrictions=permit_sasl_authenticated,reject_unauth_destination"

postconf -M smtps/inet="smtps inet n - y - - smtpd"
postconf -P "smtps/inet/smtpd_tls_wrappermode=yes"
postconf -P "smtps/inet/smtpd_sasl_auth_enable=yes"
postconf -P "smtps/inet/smtpd_client_restrictions=permit_sasl_authenticated,reject"
postconf -P "smtps/inet/smtpd_recipient_restrictions=permit_sasl_authenticated,reject_unauth_destination"

# Wire OpenDKIM as a milter so outbound mail is signed
postconf -e "milter_default_action = accept"
postconf -e "milter_protocol = 6"
postconf -e "smtpd_milters = local:/opendkim/opendkim.sock"
postconf -e "non_smtpd_milters = local:/opendkim/opendkim.sock"

# SASL socket + plaintext-auth + auth_mechanisms are handled
# deterministically by /etc/dovecot/conf.d/99-panel.conf above, so the
# previous sed/python patches against stock conf files have been
# removed. Leaving a breadcrumb here so the next operator doesn't try
# to "fix" this by reintroducing them — the override file wins, and
# sed-matching stock defaults was the exact source of the auth bug.

systemctl enable postfix dovecot opendkim >> "$LOG_FILE" 2>&1
systemctl restart opendkim >> "$LOG_FILE" 2>&1 || true
systemctl restart dovecot >> "$LOG_FILE" 2>&1 || true
systemctl restart postfix >> "$LOG_FILE" 2>&1 || true
log "Email stack installed (Postfix + Dovecot/passwd-file + LMTP + submission/smtps + DKIM milter)"

# =============================================================================
# Step 7: DNS (PowerDNS)
# =============================================================================
step "7/13 — Installing PowerDNS"
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

# Configure pure-ftpd passive port range. Without this, FTP clients behind
# NAT fail with "Entering Passive Mode ... 227" hangs because the server
# advertises a random high port that the firewall blocks. Lock it to a
# small range we can open in ufw, and force passive-mode IP to this
# server's public IP so clients don't try to connect to the RFC1918
# address pure-ftpd hands out by default on cloud VPS.
mkdir -p /etc/pure-ftpd/conf
echo "30000 30009" > /etc/pure-ftpd/conf/PassivePortRange
echo "$SERVER_IP" > /etc/pure-ftpd/conf/ForcePassiveIP
# ChrootEveryone keeps a compromised FTP account from wandering into
# another tenant's home dir.
echo "yes" > /etc/pure-ftpd/conf/ChrootEveryone
echo "yes" > /etc/pure-ftpd/conf/NoAnonymous
# TLS enforced when a cert is available; falls back gracefully when not.
# pure-ftpd looks at /etc/ssl/private/pure-ftpd.pem (combined key+cert).
if [ ! -f /etc/ssl/private/pure-ftpd.pem ]; then
    openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
        -keyout /tmp/ftp-key.pem -out /tmp/ftp-cert.pem \
        -subj "/CN=${PANEL_DOMAIN}" >> "$LOG_FILE" 2>&1 || true
    if [ -f /tmp/ftp-key.pem ] && [ -f /tmp/ftp-cert.pem ]; then
        mkdir -p /etc/ssl/private
        cat /tmp/ftp-key.pem /tmp/ftp-cert.pem > /etc/ssl/private/pure-ftpd.pem
        chmod 600 /etc/ssl/private/pure-ftpd.pem
        rm -f /tmp/ftp-key.pem /tmp/ftp-cert.pem
        echo "1" > /etc/pure-ftpd/conf/TLS
    fi
fi
systemctl restart pure-ftpd >> "$LOG_FILE" 2>&1 || true
log "Certbot & Pure-FTPd installed (passive range 30000-30009)"

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
// IMPORTANT: Postfix main.cf sets smtpd_tls_auth_only = yes, which
// refuses AUTH on a plaintext connection. Without the `tls://` prefix
// Roundcube opens a bare TCP socket on port 587 and Postfix rejects
// the AUTH attempt before ever looking at the credentials — which is
// exactly the "SMTP Error (): Authentication failed" toast users saw
// from the Compose view. The `tls://` prefix tells Roundcube to
// negotiate STARTTLS, re-EHLO, then AUTH on the encrypted channel.
\$config['smtp_host'] = 'tls://localhost:587';
\$config['smtp_user'] = '%u';
\$config['smtp_pass'] = '%p';
// Self-signed snake-oil cert on localhost is fine — the connection
// never leaves the box, and Roundcube would otherwise refuse to
// complete the STARTTLS handshake and drop back to plaintext which
// then fails smtpd_tls_auth_only.
\$config['smtp_conn_options'] = [
    'ssl' => [
        'verify_peer'       => false,
        'verify_peer_name'  => false,
        'allow_self_signed' => true,
    ],
];
\$config['support_url'] = '';
\$config['product_name'] = 'Betazen Server Panel Webmail';
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

// Writable temp dir for compose drafts + uploaded attachments. The
// Roundcube default /usr/share/roundcube/temp/ doesn't exist on Debian
// packages — point at /var/lib/roundcube/temp which the Debian package
// owns at www-data:www-data 0750. Without a working temp_dir every
// compose attempt redirects, then the second request (with the new
// _id in the URL) can't find the compose data and shows
// "COMPOSE SESSION ERROR — Requested compose session not found".
\$config['temp_dir'] = '/var/lib/roundcube/temp';

// Session lifetime in MINUTES; default 10 is painfully short.
\$config['session_lifetime'] = 60;

// Pin the storage backend so we pick it intentionally rather than
// letting the Debian default flip between releases.
\$config['session_storage'] = 'db';

// Behind nginx + the panel's SSO flow, the strict session-path /
// referer / ip checks reject perfectly-valid compose requests because
// the URL path comes back through the proxy in a shape Roundcube
// didn't expect. Disabling these does NOT weaken auth — the session
// cookie is still the trust anchor — but it does let the
// compose-redirect-then-load-by-_id flow actually find its session.
\$config['request_check_session_path'] = false;
\$config['referer_check']               = false;
\$config['ip_check']                    = false;

// nginx terminates TLS and forwards plain HTTP to PHP-FPM. Setting
// use_secure_urls=false makes Roundcube emit relative URLs that match
// whatever scheme the browser used (rather than forcing https in
// constructed redirects, which mismatched).
\$config['use_secure_urls'] = false;
\$config['proxy_whitelist'] = ['127.0.0.1'];

// Scope the session cookie to /webmail/ so it doesn't collide with
// the panel SPA's own cookies on /. Without this scoping, Set-Cookie
// from /webmail/ would also be sent on /whm/* requests, and the
// panel's own session handling could overwrite Roundcube's.
\$config['cookie_path'] = '/webmail/';
RCEOF

# Make sure the temp dir actually exists with perms www-data can write.
# The Debian package usually creates it but a partial postinst can leave
# /var/lib/roundcube without /temp underneath, and compose silently
# fails with the error above.
mkdir -p /var/lib/roundcube/temp
chown www-data:www-data /var/lib/roundcube/temp
chmod 750 /var/lib/roundcube/temp

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

# Patch Roundcube's compose.php — its redirect() always uses Location
# header, but compose runs INSIDE the iframe-rendering parent so output
# is already flushed by the time redirect tries to set the header. The
# action_handler loop then re-runs compose up to 5x, and each iteration
# concatenates an error page into the response. End user sees:
# "COMPOSE SESSION ERROR — Requested compose session not found".
#
# Fix: skip the post-create redirect entirely. Stamp the new compose ID
# into both $_SESSION['last_compose_session'] and $_GET['_id'] so the
# rest of run() proceeds as if the URL had carried the _id. The
# request lifecycle then loads the compose form normally without ever
# trying a header redirect.
COMPOSE_PHP=/usr/share/roundcube/program/actions/mail/compose.php
if [ -f "$COMPOSE_PHP" ] && grep -q '$rcmail->output->redirect' "$COMPOSE_PHP"; then
    python3 - <<'COMPOSE_PATCH_PY'
import re
p = "/usr/share/roundcube/program/actions/mail/compose.php"
src = open(p).read()
lines = src.split("\n")

# Find the "$rcmail->output->redirect([" line; capture up to the closing "]);"
ridx = next((i for i, ln in enumerate(lines) if "$rcmail->output->redirect([" in ln), None)
if ridx is None:
    print("compose-patch: redirect call not found, skipping")
else:
    end = next((j for j in range(ridx + 1, ridx + 10) if lines[j].strip() == "]);"), None)
    if end is None:
        print("compose-patch: redirect closing not found, skipping")
    else:
        indent = lines[ridx][: len(lines[ridx]) - len(lines[ridx].lstrip())]
        replacement = [
            f"{indent}// PATCHED — see install.sh: skip the always-Location redirect that",
            f"{indent}// fights with the iframe-rendering parent. Stamp the new compose",
            f"{indent}// ID into the request so the rest of run() loads the compose form",
            f"{indent}// directly instead of needing a redirect-then-load round trip.",
            f"{indent}$_SESSION['last_compose_session'] = self::$COMPOSE['id'];",
            f"{indent}$_GET['_id'] = self::$COMPOSE['id'];",
        ]
        new_lines = lines[:ridx] + replacement + lines[end + 1:]
        # Second patch: force exit after the final $rcmail->output->send('compose')
        # call so action_handler's while-loop can't iterate and concatenate
        # 5× copies of the compose HTML (breaks the recipient-input widget).
        send_needle = "$rcmail->output->send('compose');"
        send_repl   = "$rcmail->output->send('compose');\n        exit; // prevent action_handler loop from iterating"
        txt = "\n".join(new_lines)
        if send_needle in txt and send_repl not in txt:
            txt = txt.replace(send_needle, send_repl, 1)
        open(p, "w").write(txt)
        print("compose-patch: applied (redirect-skip + post-send exit)")
COMPOSE_PATCH_PY
fi

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

setup_pm2


# =============================================================================
# Step 11: Clone & Build Betazen Server Panel
# =============================================================================
step "11/13 — Building Betazen Server Panel"

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
JWT_ACCESS_EXPIRY=4h
JWT_REFRESH_EXPIRY=720h
# AES-GCM key for encrypting Deploy Software PATs + any other panel
# secret stored in mongo. Must be hex (any length, but 32+ bytes
# recommended). Persisted so a panel restart doesn't invalidate
# every saved token.
APP_ENCRYPTION_KEY=${APP_ENC_KEY}
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

# Expose the repo's diagnostic + reconcile scripts as system commands
# so an operator can run them without knowing the full repo path. Both
# scripts are idempotent; both pick up future updates automatically
# because they're symlinks into the git checkout.
#   serverpanel-mail-diagnose [domain]  — read-only audit of the mail
#                                         stack (postfix, dovecot,
#                                         opendkim, DNS, chroot DNS).
#   serverpanel-mail-reconcile          — one-shot heal: rewrites
#                                         99-panel.conf, postfix
#                                         directives, chroot /etc/,
#                                         Roundcube SMTP config,
#                                         restarts services.
if [ -f "${INSTALL_DIR}/scripts/mail-diagnose.sh" ]; then
    chmod 0755 "${INSTALL_DIR}/scripts/mail-diagnose.sh" \
               "${INSTALL_DIR}/scripts/reconcile-email.sh"
    ln -sf "${INSTALL_DIR}/scripts/mail-diagnose.sh"   /usr/local/bin/serverpanel-mail-diagnose
    ln -sf "${INSTALL_DIR}/scripts/reconcile-email.sh" /usr/local/bin/serverpanel-mail-reconcile
    log "Mail helper commands installed: serverpanel-mail-diagnose, serverpanel-mail-reconcile"
fi

# Build backend — matches the GitHub Actions deploy workflow (server +
# agent + seed). The agent binary is used by the deploy/transfer flows
# for remote VPS management, so omitting it leaves features broken.
log "Building Go backend..."
cd "${INSTALL_DIR}/backend"
mkdir -p "${INSTALL_DIR}/bin"
CGO_ENABLED=0 "${GO_DIR}/bin/go" build -ldflags="-s -w" -o "${INSTALL_DIR}/bin/server" ./cmd/server >> "$LOG_FILE" 2>&1
CGO_ENABLED=0 "${GO_DIR}/bin/go" build -ldflags="-s -w" -o "${INSTALL_DIR}/bin/agent"  ./cmd/agent  >> "$LOG_FILE" 2>&1
CGO_ENABLED=0 "${GO_DIR}/bin/go" build -ldflags="-s -w" -o "${INSTALL_DIR}/bin/seed"   ./cmd/seed   >> "$LOG_FILE" 2>&1
log "Backend built (server, agent, seed)"

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
Description=Betazen Server Panel API Server
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

# Create nginx reverse proxy for the panel (with webmail).
# Mark as default_server so requests by IP (or any hostname that isn't
# a vendor domain) land on the panel — otherwise nginx picks the first
# alphabetically-loaded vhost as the implicit default, which once any
# vendor is provisioned is a random vendor's public_html. Symptom: the
# raw IP serves "Welcome to your new website!" and /whm/* returns
# "File not found" because it's hitting the vendor's try_files chain.
# Let's Encrypt HTTP-01 webroot — one directory that every :80 server
# block (panel + vendor + app + placeholder) serves, so certbot --webroot
# works uniformly without touching nginx config at issuance time.
mkdir -p /var/www/certbot/.well-known/acme-challenge
chmod -R 0755 /var/www/certbot
cat > /etc/nginx/sites-available/serverpanel << NGXEOF
server {
    listen 80 default_server;
    # Panel hostname + raw IP + underscore catch-all so any request
    # not matching a vendor server_name lands here.
    server_name ${PANEL_DOMAIN} ${SERVER_IP} _;

    # Let's Encrypt HTTP-01 challenge — certbot --webroot writes files
    # here; LE fetches them before the rest of the panel vhost runs.
    # Also catches vendor-domain challenges during brief windows where
    # the vendor vhost is being rewritten (we're default_server).
    location ^~ /.well-known/acme-challenge/ {
        root /var/www/certbot;
        default_type "text/plain";
        try_files \$uri =404;
    }

    # Reject any host that isn't the panel domain or the server IP.
    # Without this guard, a vendor whose vhost was deleted (DNS A still
    # points here) silently gets the panel login on its own hostname.
    if (\$host !~* ^(${PANEL_DOMAIN}|${SERVER_IP})\$) { return 404; }

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

    # phpMyAdmin (served under the panel domain so the single TLS cert
    # covers both UIs). Uses cookie auth — the panel's Databases page
    # form-POSTs per-db credentials so clicking 'Open in phpMyAdmin' lands
    # the operator straight at the target database already logged in.
    include /etc/nginx/snippets/phpmyadmin.conf;

    # Large uploads — File Manager allows 500 MB per request. Server-wide
    # client_max_body_size + generous timeouts so a 300+ MB zip on a slow
    # home connection doesn't hit the default 60s cutoff mid-transfer.
    client_max_body_size 500M;
    client_body_timeout 600s;
    client_header_timeout 60s;
    send_timeout 600s;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        # Stream the request body to the backend instead of buffering it to
        # disk first — nginx's default buffers 500 MB of multipart data to
        # /var/lib/nginx before forwarding, which needs free disk and also
        # makes progress bars lie (they hit 100% long before the backend
        # sees a byte).
        proxy_request_buffering off;
        proxy_connect_timeout 60s;
        proxy_send_timeout    600s;
        proxy_read_timeout    86400s;
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
ufw allow 22/tcp                 >> "$LOG_FILE" 2>&1    # SSH
ufw allow 80/tcp                 >> "$LOG_FILE" 2>&1    # HTTP
ufw allow 443/tcp                >> "$LOG_FILE" 2>&1    # HTTPS
ufw allow 53/tcp                 >> "$LOG_FILE" 2>&1    # DNS
ufw allow 53/udp                 >> "$LOG_FILE" 2>&1    # DNS
ufw allow 25/tcp                 >> "$LOG_FILE" 2>&1    # SMTP
ufw allow 465/tcp                >> "$LOG_FILE" 2>&1    # SMTPS
ufw allow 587/tcp                >> "$LOG_FILE" 2>&1    # Submission
ufw allow 143/tcp                >> "$LOG_FILE" 2>&1    # IMAP
ufw allow 993/tcp                >> "$LOG_FILE" 2>&1    # IMAPS
ufw allow 110/tcp                >> "$LOG_FILE" 2>&1    # POP3
ufw allow 995/tcp                >> "$LOG_FILE" 2>&1    # POP3S
ufw allow 21/tcp                 >> "$LOG_FILE" 2>&1    # FTP
ufw allow 30000:30009/tcp        >> "$LOG_FILE" 2>&1    # FTP passive range (matches pure-ftpd)
ufw --force enable               >> "$LOG_FILE" 2>&1

# -----------------------------------------------------------------------------
# fail2ban — ships in base packages but without a jail it doesn't actually
# protect anything. Enable the SSH jail so a brute-force attacker gets
# banned after a handful of failed logins. Non-fatal if fail2ban is absent
# or the jail syntax drifts between releases.
# -----------------------------------------------------------------------------
if command -v fail2ban-client &>/dev/null; then
    cat > /etc/fail2ban/jail.d/serverpanel.conf << 'F2BEOF'
[DEFAULT]
bantime  = 1h
findtime = 10m
maxretry = 5

[sshd]
enabled = true
port    = ssh
logpath = %(sshd_log)s
backend = systemd
F2BEOF
    systemctl enable fail2ban  >> "$LOG_FILE" 2>&1 || true
    systemctl restart fail2ban >> "$LOG_FILE" 2>&1 || true
    log "fail2ban SSH jail enabled"
fi

# -----------------------------------------------------------------------------
# logrotate — the backend logs to journald (no rotation needed) but the
# install log itself can grow unboundedly on re-runs. Rotate weekly with 4
# weeks kept and gzip'd.
# -----------------------------------------------------------------------------
cat > /etc/logrotate.d/serverpanel << 'LREOF'
/var/log/serverpanel-install.log {
    weekly
    rotate 4
    missingok
    notifempty
    compress
    delaycompress
    create 0644 root root
}
LREOF

# =============================================================================
# Try SSL for panel domain (if it has DNS)
# =============================================================================
if [ "$PANEL_DOMAIN" != "$SERVER_IP" ]; then
    log "Attempting SSL certificate for ${PANEL_DOMAIN}..."
    certbot certonly --webroot -w /var/www/certbot --cert-name "$PANEL_DOMAIN" -d "$PANEL_DOMAIN" --non-interactive --agree-tos -m "$ADMIN_EMAIL" >> "$LOG_FILE" 2>&1 && {
        # Update nginx to use SSL
        cat > /etc/nginx/sites-available/serverpanel << NGXSSLEOF
server {
    listen 80 default_server;
    server_name ${PANEL_DOMAIN} ${SERVER_IP} _;
    client_max_body_size 500M;
    client_body_timeout 600s;
    client_header_timeout 60s;
    send_timeout 600s;

    # Keep the ACME webroot reachable on :80 even after we flip to SSL
    # — cert renewals still use HTTP-01, and certbot retries fail if
    # /.well-known/acme-challenge/ 301s to https before LE follows.
    location ^~ /.well-known/acme-challenge/ {
        root /var/www/certbot;
        default_type "text/plain";
        try_files \$uri =404;
    }

    # Reject any host that isn't the panel domain or the server IP.
    # Without this, a vendor whose vhost was deleted (DNS A still
    # points here) silently gets the panel UI on its own hostname.
    if (\$host !~* ^(${PANEL_DOMAIN}|${SERVER_IP})\$) { return 404; }

    # Panel hostname always upgrades to HTTPS (cert matches). Raw-IP
    # access stays on HTTP so visitors who type the IP directly get the
    # panel without a cert-name-mismatch warning.
    if (\$host = "${PANEL_DOMAIN}") { return 301 https://${PANEL_DOMAIN}\$request_uri; }

    # Webmail (SSO token from WHM)
    location ^~ /webmail/ {
        alias /var/lib/roundcube/public_html/;
        index index.php;

        location ~ ^/webmail/(.+\\.php)\$ {
            alias /var/lib/roundcube/public_html/\$1;
            include fastcgi_params;
            fastcgi_pass unix:/var/run/php/php8.2-fpm.sock;
            fastcgi_param SCRIPT_FILENAME /var/lib/roundcube/public_html/\$1;
            fastcgi_intercept_errors on;
        }

        location ~ /\\. { deny all; }
    }

    # phpMyAdmin
    include /etc/nginx/snippets/phpmyadmin.conf;

    # WebSocket
    location /ws/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_read_timeout 3600s;
    }

    # Main panel (proxied to Go backend on :8080)
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_request_buffering off;
        proxy_connect_timeout 60s;
        proxy_send_timeout    600s;
        proxy_read_timeout    86400s;
    }
}

server {
    listen 443 ssl default_server;
    server_name ${PANEL_DOMAIN} ${SERVER_IP} _;

    ssl_certificate /etc/letsencrypt/live/${PANEL_DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${PANEL_DOMAIN}/privkey.pem;

    # Large uploads — see HTTP variant above for rationale.
    client_max_body_size 500M;
    client_body_timeout 600s;
    client_header_timeout 60s;
    send_timeout 600s;

    # Same host guard as :80 — purged-vendor hostnames whose DNS still
    # points here get 404 instead of the panel login.
    if (\$host !~* ^(${PANEL_DOMAIN}|${SERVER_IP})\$) { return 404; }

    # Roundcube Webmail (with SSO auto-login from WHM)
    location ^~ /webmail/ {
        alias /var/lib/roundcube/public_html/;
        index index.php;

        location ~ ^/webmail/(.+\\.php)\$ {
            alias /var/lib/roundcube/public_html/\$1;
            include fastcgi_params;
            fastcgi_pass unix:/var/run/php/php8.2-fpm.sock;
            fastcgi_param SCRIPT_FILENAME /var/lib/roundcube/public_html/\$1;
            fastcgi_intercept_errors on;
        }

        location ~ /\\. { deny all; }
    }

    # phpMyAdmin (same snippet as HTTP variant — served under panel TLS)
    include /etc/nginx/snippets/phpmyadmin.conf;

    # WebSocket support
    location /ws/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_read_timeout 3600s;
    }

    # Main panel
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_request_buffering off;
        proxy_connect_timeout 60s;
        proxy_send_timeout    600s;
        proxy_read_timeout    86400s;
    }
}
NGXSSLEOF
        nginx -t >> "$LOG_FILE" 2>&1 && systemctl reload nginx
        log "SSL configured for ${PANEL_DOMAIN}"

        # The Go backend stays on plain HTTP :8080 and nginx terminates
        # TLS — historically we rewrote SERVER_PORT to 443 here, which
        # then collided with nginx binding :443 and produced "address
        # already in use" crash-loops on every restart. Leave the
        # backend port + TLS_CERT/TLS_KEY alone.
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
echo -e "${GREEN}   Betazen Server Panel installed successfully!${NC}"
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
