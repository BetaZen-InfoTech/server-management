# ServerPanel — Full VPS Deployment Guide

> Complete step-by-step guide to deploy ServerPanel on a **blank Ubuntu 22.04/24.04 VPS** from scratch.
>
> **Repository:** `https://github.com/BetaZen-InfoTech/whm-cPanel.git`

---

## Table of Contents

1. [Prerequisites](#1-prerequisites)
2. [Initial VPS Setup](#2-initial-vps-setup)
3. [Generate SSH Deploy Key & Add to GitHub](#3-generate-ssh-deploy-key--add-to-github)
4. [Install Required Software](#4-install-required-software)
5. [Clone the Repository](#5-clone-the-repository)
6. [Install MongoDB](#6-install-mongodb)
7. [Build the Backend](#7-build-the-backend)
8. [Build the Frontend](#8-build-the-frontend)
9. [Configure Environment Variables](#9-configure-environment-variables)
10. [Setup SSL with Let's Encrypt](#10-setup-ssl-with-lets-encrypt)
11. [Create Systemd Services](#11-create-systemd-services)
12. [Configure Nginx Reverse Proxy](#12-configure-nginx-reverse-proxy)
13. [Configure Firewall (UFW)](#13-configure-firewall-ufw)
14. [Start Everything](#14-start-everything)
15. [Create First Admin User](#15-create-first-admin-user)
16. [Verify Deployment](#16-verify-deployment)
17. [Auto-Deploy with GitHub Webhooks](#17-auto-deploy-with-github-webhooks)
18. [Maintenance & Updates](#18-maintenance--updates)
19. [Troubleshooting](#19-troubleshooting)
20. [Alternative: Docker Deployment](#20-alternative-docker-deployment)

---

## 1. Prerequisites

Before starting, ensure you have:

| Requirement | Details |
|-------------|---------|
| **VPS** | Ubuntu 22.04 or 24.04 LTS (minimum 2 CPU, 4 GB RAM, 40 GB disk) |
| **Domain** | A domain pointed to your VPS IP (e.g., `panel.betazeninfotech.com`) |
| **DNS A Record** | `panel.betazeninfotech.com` → `YOUR_VPS_IP` |
| **Root/sudo access** | SSH access to the VPS |
| **GitHub account** | Access to `https://github.com/BetaZen-InfoTech/whm-cPanel.git` |

---

## 2. Initial VPS Setup

### 2.1 — SSH into your VPS

```bash
ssh root@YOUR_VPS_IP
```

### 2.2 — Update the system

```bash
apt update && apt upgrade -y
```

### 2.3 — Set timezone

```bash
timedatectl set-timezone Asia/Kolkata
# Or your preferred timezone. List all: timedatectl list-timezones
```

### 2.4 — Set hostname

```bash
hostnamectl set-hostname panel.betazeninfotech.com
```

### 2.5 — Create a deploy user (recommended — avoid running as root)

```bash
adduser deploy
usermod -aG sudo deploy
```

### 2.6 — Enable SSH for the deploy user

```bash
mkdir -p /home/deploy/.ssh
cp ~/.ssh/authorized_keys /home/deploy/.ssh/
chown -R deploy:deploy /home/deploy/.ssh
chmod 700 /home/deploy/.ssh
chmod 600 /home/deploy/.ssh/authorized_keys
```

### 2.7 — Switch to deploy user

```bash
su - deploy
```

From here on, all commands run as the `deploy` user (use `sudo` when needed).

---

## 3. Generate SSH Deploy Key & Add to GitHub

This allows your VPS to pull code from the private GitHub repository without a password.

### 3.1 — Generate an SSH key pair on the VPS

```bash
ssh-keygen -t ed25519 -C "deploy@panel.betazeninfotech.com" -f ~/.ssh/github_deploy
```

- Press **Enter** when prompted for passphrase (leave empty for automated deploys).

This creates two files:
- **Private key:** `~/.ssh/github_deploy` (stays on VPS, never share)
- **Public key:** `~/.ssh/github_deploy.pub` (add to GitHub)

### 3.2 — Display the public key

```bash
cat ~/.ssh/github_deploy.pub
```

Output looks like:
```
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG... deploy@panel.betazeninfotech.com
```

**Copy the entire output.**

### 3.3 — Add the deploy key to the GitHub repository

1. Open your browser and go to:
   ```
   https://github.com/BetaZen-InfoTech/whm-cPanel/settings/keys
   ```
   (Repository → **Settings** → **Deploy keys**)

2. Click **"Add deploy key"**

3. Fill in:
   - **Title:** `VPS Deploy Key - panel.betazeninfotech.com`
   - **Key:** Paste the public key you copied in step 3.2
   - **Allow write access:** Leave **unchecked** (read-only is sufficient for deployment)

4. Click **"Add key"**

### 3.4 — Configure SSH to use this key for GitHub

```bash
cat >> ~/.ssh/config << 'EOF'
Host github.com
    HostName github.com
    User git
    IdentityFile ~/.ssh/github_deploy
    IdentitiesOnly yes
EOF

chmod 600 ~/.ssh/config
```

### 3.5 — Test the connection

```bash
ssh -T git@github.com
```

Expected output:
```
Hi BetaZen-InfoTech/whm-cPanel! You've successfully authenticated, but GitHub does not provide shell access.
```

If you see this, the deploy key is working.

---

## 4. Install Required Software

### 4.1 — Install essential build tools

```bash
sudo apt install -y curl wget git build-essential software-properties-common
```

### 4.2 — Install Go 1.22+

```bash
GO_VERSION=1.22.5
wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
rm go${GO_VERSION}.linux-amd64.tar.gz
```

Add Go to PATH — append to `~/.bashrc`:

```bash
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc
```

Verify:

```bash
go version
# Output: go version go1.22.5 linux/amd64
```

### 4.3 — Install Node.js 20 LTS

```bash
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt install -y nodejs
```

Verify:

```bash
node -v   # v20.x.x
npm -v    # 10.x.x
```

### 4.4 — Install Nginx

```bash
sudo apt install -y nginx
sudo systemctl enable nginx
```

### 4.5 — Install Certbot (Let's Encrypt)

```bash
sudo apt install -y certbot python3-certbot-nginx
```

---

## 5. Clone the Repository

### 5.1 — Create application directory

```bash
sudo mkdir -p /opt/serverpanel
sudo chown deploy:deploy /opt/serverpanel
```

### 5.2 — Clone using SSH

```bash
cd /opt/serverpanel
git clone git@github.com:BetaZen-InfoTech/whm-cPanel.git .
```

> **Note:** The `.` at the end clones directly into `/opt/serverpanel` without creating a subdirectory.

### 5.3 — Verify the clone

```bash
ls -la
# Should show: backend/  frontend/  .env.example  Makefile  docker-compose.yml  README.md  etc.
```

---

## 6. Install MongoDB

### Option A: MongoDB on the VPS (Self-hosted)

```bash
# Import MongoDB GPG key
curl -fsSL https://www.mongodb.org/static/pgp/server-7.0.asc | \
  sudo gpg -o /usr/share/keyrings/mongodb-server-7.0.gpg --dearmor

# Add repository (Ubuntu 22.04)
echo "deb [ arch=amd64,arm64 signed-by=/usr/share/keyrings/mongodb-server-7.0.gpg ] https://repo.mongodb.org/apt/ubuntu jammy/mongodb-org/7.0 multiverse" | \
  sudo tee /etc/apt/sources.list.d/mongodb-org-7.0.list

# Install
sudo apt update
sudo apt install -y mongodb-org

# Start and enable
sudo systemctl start mongod
sudo systemctl enable mongod
```

Verify:

```bash
mongosh --eval "db.runCommand({ ping: 1 })"
# Output: { ok: 1 }
```

**Secure MongoDB** (create admin user):

```bash
mongosh << 'EOF'
use admin
db.createUser({
  user: "serverpanel",
  pwd: "YOUR_STRONG_MONGO_PASSWORD",
  roles: [{ role: "readWrite", db: "serverpanel" }]
})
EOF
```

Enable authentication — edit `/etc/mongod.conf`:

```bash
sudo nano /etc/mongod.conf
```

Add/modify:

```yaml
security:
  authorization: enabled

net:
  bindIp: 127.0.0.1
  port: 27017
```

Restart MongoDB:

```bash
sudo systemctl restart mongod
```

Your connection URI will be:
```
mongodb://serverpanel:YOUR_STRONG_MONGO_PASSWORD@127.0.0.1:27017/serverpanel?authSource=admin
```

### Option B: MongoDB Atlas (Cloud-hosted)

1. Go to [https://cloud.mongodb.com](https://cloud.mongodb.com)
2. Create a free M0 cluster (or paid tier)
3. Create a database user
4. Whitelist your VPS IP in Network Access
5. Get the connection string from **Connect → Drivers → Go**

Your connection URI will look like:
```
mongodb+srv://username:password@cluster0.xxxxx.mongodb.net/
```

---

## 7. Build the Backend

```bash
cd /opt/serverpanel/backend

# Download Go dependencies
go mod download

# Build server binary
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/serverpanel/bin/server ./cmd/server

# Build agent binary
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/serverpanel/bin/agent ./cmd/agent
```

Verify:

```bash
ls -lh /opt/serverpanel/bin/
# Should show: server  agent
```

---

## 8. Build the Frontend

```bash
cd /opt/serverpanel/frontend

# Install dependencies
npm ci

# Build both WHM and cPanel SPAs
npx turbo run build
```

This creates:
- `/opt/serverpanel/frontend/apps/whm/dist/` — WHM admin panel
- `/opt/serverpanel/frontend/apps/cpanel/dist/` — cPanel customer portal

Verify:

```bash
ls frontend/apps/whm/dist/index.html
ls frontend/apps/cpanel/dist/index.html
```

---

## 9. Configure Environment Variables

### 9.1 — Create the .env file

```bash
cd /opt/serverpanel
cp .env.example .env
nano .env
```

### 9.2 — Fill in production values

```bash
# =============================================================================
# ServerPanel — PRODUCTION Environment
# =============================================================================

# Application
APP_ENV=production
LOG_LEVEL=info

# MongoDB (use the URI from Step 6)
MONGO_URI=mongodb://serverpanel:YOUR_STRONG_MONGO_PASSWORD@127.0.0.1:27017/serverpanel?authSource=admin
MONGO_DB_NAME=serverpanel

# JWT (generate strong secrets!)
JWT_SECRET=GENERATE_WITH_openssl_rand_-hex_64
JWT_ACCESS_EXPIRY=15m
JWT_REFRESH_EXPIRY=168h

# Server
DOMAIN=panel.betazeninfotech.com
SERVER_PORT=8080
TLS_CERT=
TLS_KEY=

# Agent
AGENT_PORT=8443
AGENT_API_KEY=GENERATE_WITH_openssl_rand_-hex_32
AGENT_TLS_CERT=
AGENT_TLS_KEY=

# GitHub Deployment (optional — configure later)
GITHUB_CLIENT_ID=
GITHUB_CLIENT_SECRET=
GITHUB_WEBHOOK_SECRET=

# Email
MAIL_HOSTNAME=mail.betazeninfotech.com

# Backup
BACKUP_DIR=/var/backups/serverpanel
BACKUP_ENCRYPTION_KEY=GENERATE_WITH_openssl_rand_-hex_32

# Rate Limiting
RATE_LIMIT_WHM=200
RATE_LIMIT_CPANEL=100
```

> **Note:** `TLS_CERT` and `TLS_KEY` are left empty because Nginx handles SSL termination (see Step 12).

### 9.3 — Generate strong secrets

Run these commands and paste the output into your `.env`:

```bash
# JWT Secret (64 hex chars)
echo "JWT_SECRET=$(openssl rand -hex 64)"

# Agent API Key (32 hex chars)
echo "AGENT_API_KEY=$(openssl rand -hex 32)"

# Backup Encryption Key (32 hex chars)
echo "BACKUP_ENCRYPTION_KEY=$(openssl rand -hex 32)"
```

### 9.4 — Create backup directory

```bash
sudo mkdir -p /var/backups/serverpanel
sudo chown deploy:deploy /var/backups/serverpanel
```

### 9.5 — Restrict .env permissions

```bash
chmod 600 /opt/serverpanel/.env
```

---

## 10. Setup SSL with Let's Encrypt

### 10.1 — Ensure DNS is pointing to the VPS

```bash
dig +short panel.betazeninfotech.com
# Should return YOUR_VPS_IP
```

### 10.2 — Get SSL certificate

```bash
sudo certbot certonly --nginx -d panel.betazeninfotech.com \
  --non-interactive --agree-tos --email admin@betazeninfotech.com
```

Certificates are stored at:
- **Cert:** `/etc/letsencrypt/live/panel.betazeninfotech.com/fullchain.pem`
- **Key:** `/etc/letsencrypt/live/panel.betazeninfotech.com/privkey.pem`

### 10.3 — Enable auto-renewal

```bash
sudo systemctl enable certbot.timer
sudo systemctl start certbot.timer

# Test renewal
sudo certbot renew --dry-run
```

---

## 11. Create Systemd Services

### 11.1 — ServerPanel Server service

```bash
sudo tee /etc/systemd/system/serverpanel.service << 'EOF'
[Unit]
Description=ServerPanel Server
After=network.target mongod.service
Wants=mongod.service

[Service]
Type=simple
User=deploy
Group=deploy
WorkingDirectory=/opt/serverpanel
ExecStart=/opt/serverpanel/bin/server
EnvironmentFile=/opt/serverpanel/.env
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/var/backups/serverpanel /opt/serverpanel/tmp

[Install]
WantedBy=multi-user.target
EOF
```

### 11.2 — ServerPanel Agent service

```bash
sudo tee /etc/systemd/system/serverpanel-agent.service << 'EOF'
[Unit]
Description=ServerPanel Agent
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/serverpanel
ExecStart=/opt/serverpanel/bin/agent
EnvironmentFile=/opt/serverpanel/.env
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
```

> **Note:** The agent runs as `root` because it needs to execute system commands (nginx, PHP-FPM, users, firewall, etc.).

### 11.3 — Reload systemd

```bash
sudo systemctl daemon-reload
```

---

## 12. Configure Nginx Reverse Proxy

### 12.1 — Create the Nginx config

```bash
sudo tee /etc/nginx/sites-available/serverpanel << 'NGINX'
# =============================================================================
# ServerPanel — Nginx Reverse Proxy
# =============================================================================

# Redirect HTTP → HTTPS
server {
    listen 80;
    listen [::]:80;
    server_name panel.betazeninfotech.com;
    return 301 https://$server_name$request_uri;
}

# Main HTTPS server
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name panel.betazeninfotech.com;

    # -------------------------------------------------------------------------
    # SSL Configuration
    # -------------------------------------------------------------------------
    ssl_certificate     /etc/letsencrypt/live/panel.betazeninfotech.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.betazeninfotech.com/privkey.pem;

    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 1d;
    ssl_session_tickets off;

    # HSTS
    add_header Strict-Transport-Security "max-age=63072000" always;

    # -------------------------------------------------------------------------
    # Security Headers
    # -------------------------------------------------------------------------
    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-XSS-Protection "1; mode=block" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;

    # -------------------------------------------------------------------------
    # General Settings
    # -------------------------------------------------------------------------
    client_max_body_size 500M;
    proxy_read_timeout 600s;
    proxy_send_timeout 600s;

    # -------------------------------------------------------------------------
    # API — Proxy to Go backend (port 8080)
    # -------------------------------------------------------------------------
    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }

    # -------------------------------------------------------------------------
    # WHM Panel — Serve static files + SPA fallback
    # -------------------------------------------------------------------------
    location /whm/ {
        alias /opt/serverpanel/frontend/apps/whm/dist/;
        try_files $uri $uri/ /whm/index.html;

        location ~* \.(js|css|png|jpg|jpeg|gif|ico|svg|woff|woff2|ttf|eot)$ {
            expires 1y;
            add_header Cache-Control "public, immutable";
        }
    }

    # -------------------------------------------------------------------------
    # cPanel — Serve static files + SPA fallback
    # -------------------------------------------------------------------------
    location /cpanel/ {
        alias /opt/serverpanel/frontend/apps/cpanel/dist/;
        try_files $uri $uri/ /cpanel/index.html;

        location ~* \.(js|css|png|jpg|jpeg|gif|ico|svg|woff|woff2|ttf|eot)$ {
            expires 1y;
            add_header Cache-Control "public, immutable";
        }
    }

    # -------------------------------------------------------------------------
    # Root — Redirect to WHM by default
    # -------------------------------------------------------------------------
    location = / {
        return 302 /whm/;
    }

    # -------------------------------------------------------------------------
    # Health check (direct to backend)
    # -------------------------------------------------------------------------
    location = /api/v1/health {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
    }
}
NGINX
```

### 12.2 — Enable the site

```bash
sudo ln -sf /etc/nginx/sites-available/serverpanel /etc/nginx/sites-enabled/
sudo rm -f /etc/nginx/sites-enabled/default
```

### 12.3 — Test and reload Nginx

```bash
sudo nginx -t
# Output: nginx: the configuration file /etc/nginx/nginx.conf syntax is ok

sudo systemctl reload nginx
```

---

## 13. Configure Firewall (UFW)

```bash
# Allow SSH (don't lock yourself out!)
sudo ufw allow OpenSSH

# Allow HTTP and HTTPS
sudo ufw allow 'Nginx Full'

# Allow Agent port (only if external VPS agents connect)
# sudo ufw allow 8443/tcp

# Enable firewall
sudo ufw enable

# Verify
sudo ufw status
```

Expected output:

```
Status: active

To                         Action      From
--                         ------      ----
OpenSSH                    ALLOW       Anywhere
Nginx Full                 ALLOW       Anywhere
```

---

## 14. Start Everything

### 14.1 — Start the services

```bash
sudo systemctl start serverpanel
sudo systemctl start serverpanel-agent
```

### 14.2 — Enable auto-start on boot

```bash
sudo systemctl enable serverpanel
sudo systemctl enable serverpanel-agent
```

### 14.3 — Check status

```bash
sudo systemctl status serverpanel
sudo systemctl status serverpanel-agent
```

Both should show **active (running)**.

### 14.4 — Check logs

```bash
# Server logs
sudo journalctl -u serverpanel -f --no-pager

# Agent logs
sudo journalctl -u serverpanel-agent -f --no-pager
```

---

## 15. Create First Admin User

Use `mongosh` to create the initial vendor_owner account:

```bash
mongosh "mongodb://serverpanel:YOUR_STRONG_MONGO_PASSWORD@127.0.0.1:27017/serverpanel?authSource=admin"
```

Inside the mongo shell:

```javascript
// Generate a bcrypt hash for your password
// You can use: htpasswd -nbBC 10 "" "YourStrongPassword" | tr -d ':\n' | sed 's/$2y/$2a/'
// Or use an online bcrypt generator

db.users.insertOne({
  email: "admin@betazeninfotech.com",
  name: "Admin",
  password_hash: "$2a$10$YOUR_BCRYPT_HASH_HERE",
  role: "vendor_owner",
  permissions: [
    "domain.view", "domain.create", "domain.manage",
    "app.view", "app.deploy", "app.manage",
    "database.view", "database.create", "database.manage",
    "email.view", "email.create", "email.manage",
    "dns.view", "dns.manage",
    "ssl.manage",
    "backup.view", "backup.create", "backup.restore",
    "wordpress.manage",
    "firewall.manage",
    "monitor.view",
    "log.view",
    "cron.manage",
    "file.manage",
    "ssh.manage",
    "process.view", "process.manage",
    "server.view", "server.manage",
    "notification.manage",
    "audit.view",
    "config.manage",
    "deploy.manage",
    "user.view", "user.create", "user.manage"
  ],
  status: "active",
  two_factor_enabled: false,
  created_at: new Date(),
  updated_at: new Date()
})
```

**Alternatively**, generate the bcrypt hash on the VPS:

```bash
# Install htpasswd
sudo apt install -y apache2-utils

# Generate bcrypt hash (replace YourStrongPassword)
htpasswd -nbBC 10 "" "YourStrongPassword" | tr -d ':\n' | sed 's/$2y/$2a/'
```

Copy the hash output (starts with `$2a$10$...`) and use it in the MongoDB insert above.

---

## 16. Verify Deployment

### 16.1 — Check the health endpoint

```bash
curl -s https://panel.betazeninfotech.com/api/v1/health
# Expected: {"status":"ok","service":"serverpanel"}
```

### 16.2 — Open in browser

| URL | Panel |
|-----|-------|
| `https://panel.betazeninfotech.com/whm/` | WHM Admin Panel |
| `https://panel.betazeninfotech.com/cpanel/` | cPanel Customer Portal |
| `https://panel.betazeninfotech.com/` | Redirects to `/whm/` |

### 16.3 — Login

Go to `https://panel.betazeninfotech.com/whm/login` and login with the admin credentials you created in Step 15.

---

## 17. Auto-Deploy with GitHub Webhooks

Automate deployments when you push to `main`.

### 17.1 — Create the deploy script

```bash
tee /opt/serverpanel/scripts/deploy.sh << 'SCRIPT'
#!/bin/bash
set -e

APP_DIR="/opt/serverpanel"
LOG_FILE="/var/log/serverpanel-deploy.log"

echo "$(date) — Starting deployment..." >> "$LOG_FILE"

cd "$APP_DIR"

# Pull latest code
git pull origin main >> "$LOG_FILE" 2>&1

# Rebuild backend
cd backend
go mod download >> "$LOG_FILE" 2>&1
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o "$APP_DIR/bin/server" ./cmd/server >> "$LOG_FILE" 2>&1
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o "$APP_DIR/bin/agent" ./cmd/agent >> "$LOG_FILE" 2>&1

# Rebuild frontend
cd "$APP_DIR/frontend"
npm ci >> "$LOG_FILE" 2>&1
npx turbo run build >> "$LOG_FILE" 2>&1

# Restart services
sudo systemctl restart serverpanel
sudo systemctl restart serverpanel-agent

echo "$(date) — Deployment completed successfully!" >> "$LOG_FILE"
SCRIPT

chmod +x /opt/serverpanel/scripts/deploy.sh
```

### 17.2 — Allow deploy user to restart services without password

```bash
sudo tee /etc/sudoers.d/serverpanel << 'EOF'
deploy ALL=(ALL) NOPASSWD: /bin/systemctl restart serverpanel
deploy ALL=(ALL) NOPASSWD: /bin/systemctl restart serverpanel-agent
deploy ALL=(ALL) NOPASSWD: /bin/systemctl reload nginx
EOF

sudo chmod 440 /etc/sudoers.d/serverpanel
```

### 17.3 — Setup GitHub Webhook (optional)

1. Go to `https://github.com/BetaZen-InfoTech/whm-cPanel/settings/hooks`
2. Click **"Add webhook"**
3. **Payload URL:** `https://panel.betazeninfotech.com/api/v1/deploy/webhooks/github`
4. **Content type:** `application/json`
5. **Secret:** Same as `GITHUB_WEBHOOK_SECRET` in your `.env`
6. **Events:** Select "Just the push event"
7. Click **"Add webhook"**

### 17.4 — Manual deploy

To deploy manually at any time:

```bash
/opt/serverpanel/scripts/deploy.sh
```

---

## 18. Maintenance & Updates

### Pull latest code and redeploy

```bash
cd /opt/serverpanel
/opt/serverpanel/scripts/deploy.sh
```

### View logs

```bash
# Server logs
sudo journalctl -u serverpanel -f

# Agent logs
sudo journalctl -u serverpanel-agent -f

# Nginx access logs
sudo tail -f /var/log/nginx/access.log

# Nginx error logs
sudo tail -f /var/log/nginx/error.log

# Deploy logs
tail -f /var/log/serverpanel-deploy.log
```

### Restart services

```bash
sudo systemctl restart serverpanel
sudo systemctl restart serverpanel-agent
sudo systemctl reload nginx
```

### MongoDB backup

```bash
mongodump --uri="mongodb://serverpanel:PASSWORD@127.0.0.1:27017/serverpanel?authSource=admin" \
  --out="/var/backups/serverpanel/mongo-$(date +%Y%m%d)"
```

### Restore MongoDB backup

```bash
mongorestore --uri="mongodb://serverpanel:PASSWORD@127.0.0.1:27017/serverpanel?authSource=admin" \
  --dir="/var/backups/serverpanel/mongo-YYYYMMDD/serverpanel"
```

### Renew SSL certificate

Certbot auto-renews via timer, but to force:

```bash
sudo certbot renew --force-renewal
sudo systemctl reload nginx
```

### Update system packages

```bash
sudo apt update && sudo apt upgrade -y
```

---

## 19. Troubleshooting

### Server won't start

```bash
# Check logs
sudo journalctl -u serverpanel -n 50 --no-pager

# Common issues:
# - MongoDB not running: sudo systemctl start mongod
# - Wrong MONGO_URI in .env
# - Port 8080 already in use: sudo lsof -i :8080
```

### 502 Bad Gateway from Nginx

```bash
# Check if backend is running
curl http://127.0.0.1:8080/api/v1/health

# If not running:
sudo systemctl start serverpanel
sudo systemctl status serverpanel
```

### SSL certificate issues

```bash
# Check certificate status
sudo certbot certificates

# Force renewal
sudo certbot renew --force-renewal
sudo systemctl reload nginx
```

### Permission denied errors

```bash
# Ensure deploy user owns the app directory
sudo chown -R deploy:deploy /opt/serverpanel

# Ensure .env is readable
chmod 600 /opt/serverpanel/.env
```

### MongoDB connection refused

```bash
# Check if MongoDB is running
sudo systemctl status mongod

# Check if it's listening
sudo ss -tlnp | grep 27017

# Check MongoDB logs
sudo journalctl -u mongod -n 50 --no-pager
```

### Frontend shows blank page

```bash
# Check if dist directories exist
ls -la /opt/serverpanel/frontend/apps/whm/dist/
ls -la /opt/serverpanel/frontend/apps/cpanel/dist/

# If missing, rebuild:
cd /opt/serverpanel/frontend && npm ci && npx turbo run build

# Check Nginx config
sudo nginx -t
```

### Deploy key not working

```bash
# Test SSH connection
ssh -vT git@github.com

# Check key permissions
ls -la ~/.ssh/github_deploy
# Should be: -rw------- (600)

# Check SSH config
cat ~/.ssh/config
```

---

## 20. Alternative: Docker Deployment

If you prefer Docker instead of bare-metal:

### 20.1 — Install Docker

```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker deploy
# Log out and back in for group to take effect
```

### 20.2 — Install Docker Compose

```bash
sudo apt install -y docker-compose-plugin
```

### 20.3 — Configure and start

```bash
cd /opt/serverpanel
cp .env.example .env
nano .env   # Fill in production values (see Step 9)

# Build and start
docker compose up -d --build

# Check status
docker compose ps

# View logs
docker compose logs -f server
```

### 20.4 — Docker with external Nginx + SSL

Use the same Nginx config from Step 12, but proxy to Docker's mapped port:

```nginx
# In the proxy_pass directive, use:
proxy_pass http://127.0.0.1:8080;
```

---

## Quick Reference

| Command | Description |
|---------|-------------|
| `sudo systemctl status serverpanel` | Check server status |
| `sudo systemctl restart serverpanel` | Restart server |
| `sudo journalctl -u serverpanel -f` | Stream server logs |
| `sudo systemctl status serverpanel-agent` | Check agent status |
| `sudo nginx -t && sudo systemctl reload nginx` | Test & reload Nginx |
| `sudo certbot renew` | Renew SSL certificates |
| `/opt/serverpanel/scripts/deploy.sh` | Manual redeploy |
| `mongosh` | Open MongoDB shell |

---

## Security Checklist

- [ ] SSH key authentication enabled (password login disabled)
- [ ] UFW firewall enabled with only necessary ports open
- [ ] `.env` file has `chmod 600` (owner read/write only)
- [ ] MongoDB authentication enabled
- [ ] Strong JWT secret (64+ hex characters)
- [ ] Strong Agent API key (32+ hex characters)
- [ ] SSL/TLS enabled via Let's Encrypt
- [ ] HSTS header enabled in Nginx
- [ ] Auto-renewal enabled for SSL certificates
- [ ] Regular MongoDB backups scheduled
- [ ] Deploy key is read-only on GitHub
- [ ] `APP_ENV=production` in `.env`

---

*Built with ServerPanel by BetaZen InfoTech*
