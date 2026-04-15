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

log()  { echo -e "${GREEN}[+]${NC} $1"; echo "[$(date)] $1" >> "$LOG_FILE"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; echo "[$(date)] WARN: $1" >> "$LOG_FILE"; }
err()  { echo -e "${RED}[x]${NC} $1"; echo "[$(date)] ERROR: $1" >> "$LOG_FILE"; }
step() { echo -e "\n${CYAN}==>${NC} ${BLUE}$1${NC}"; echo "[$(date)] STEP: $1" >> "$LOG_FILE"; }

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
if [ -z "$MONGO_PASS" ]; then
    MONGO_PASS=$(openssl rand -hex 16)
fi
echo ""

JWT_SECRET=$(openssl rand -hex 32)
AGENT_KEY=$(openssl rand -hex 16)
BACKUP_KEY=$(openssl rand -hex 16)

echo ""
log "Installation starting... (log: $LOG_FILE)"
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

# Create MongoDB admin user and app user
log "Setting up MongoDB users..."
mongosh --quiet --eval "
  use admin;
  try {
    db.createUser({ user: 'admin', pwd: '${MONGO_PASS}', roles: ['root'] });
  } catch(e) {}
  try {
    db.createUser({ user: 'serverpanel', pwd: '${MONGO_PASS}', roles: [
      { role: 'readWrite', db: 'serverpanel' },
      { role: 'dbAdmin', db: 'serverpanel' }
    ]});
  } catch(e) {}
" >> "$LOG_FILE" 2>&1 || true

# Enable auth in mongod.conf if not already
if ! grep -q "authorization: enabled" /etc/mongod.conf 2>/dev/null; then
    if grep -q "^#security:" /etc/mongod.conf 2>/dev/null; then
        sed -i 's/^#security:/security:\n  authorization: enabled/' /etc/mongod.conf
    elif ! grep -q "^security:" /etc/mongod.conf 2>/dev/null; then
        echo -e "\nsecurity:\n  authorization: enabled" >> /etc/mongod.conf
    fi
    systemctl restart mongod >> "$LOG_FILE" 2>&1
fi
log "MongoDB configured with authentication"

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
if ! command -v pdns_server &>/dev/null; then
    # Stop systemd-resolved to free port 53
    systemctl stop systemd-resolved >> "$LOG_FILE" 2>&1 || true
    systemctl disable systemd-resolved >> "$LOG_FILE" 2>&1 || true
    echo "nameserver 8.8.8.8" > /etc/resolv.conf
    echo "nameserver 8.8.4.4" >> /etc/resolv.conf

    apt-get install -y pdns-server pdns-backend-gsqlite3 sqlite3 >> "$LOG_FILE" 2>&1

    # Initialize SQLite backend
    PDNS_DB="/var/lib/powerdns/pdns.sqlite3"
    mkdir -p /var/lib/powerdns
    if [ ! -f "$PDNS_DB" ]; then
        sqlite3 "$PDNS_DB" < /usr/share/doc/pdns-backend-gsqlite3/schema.sqlite3.sql 2>> "$LOG_FILE" || true
    fi
    chown -R pdns:pdns /var/lib/powerdns

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
    systemctl restart pdns >> "$LOG_FILE" 2>&1
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
    wget -q "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -O /tmp/go.tar.gz
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
    curl -fsSL https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar -o /usr/local/bin/wp
    chmod +x /usr/local/bin/wp
    log "WP-CLI $(wp --version) installed"
else
    log "WP-CLI $(wp --version) already installed"
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

# Seed database with admin user
log "Seeding admin user..."
cd "${INSTALL_DIR}"
"${INSTALL_DIR}/bin/seed" >> "$LOG_FILE" 2>&1 || true

# Enable and start services
systemctl daemon-reload
systemctl enable serverpanel >> "$LOG_FILE" 2>&1
systemctl start serverpanel >> "$LOG_FILE" 2>&1
nginx -t >> "$LOG_FILE" 2>&1 && systemctl reload nginx

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
echo ""
echo -e "${GREEN}============================================${NC}"
echo -e "${GREEN}   ServerPanel installed successfully!${NC}"
echo -e "${GREEN}============================================${NC}"
echo ""
echo -e "  Panel URL:    ${CYAN}http://${PANEL_DOMAIN}:8080${NC}"
if [ -f "/etc/letsencrypt/live/${PANEL_DOMAIN}/fullchain.pem" ]; then
echo -e "  Panel URL:    ${CYAN}https://${PANEL_DOMAIN}${NC}"
fi
echo -e "  WHM:          ${CYAN}http://${PANEL_DOMAIN}:8080/whm${NC}"
echo -e "  cPanel:       ${CYAN}http://${PANEL_DOMAIN}:8080/cpanel${NC}"
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
