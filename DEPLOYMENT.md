# ServerPanel — Full VPS Deployment Guide

> Complete step-by-step guide to deploy ServerPanel on a **blank Ubuntu 22.04/24.04 VPS**.
> After initial setup, every `git push` to `main` **auto-deploys** to the VPS.
>
> **Repository:** `https://github.com/BetaZen-InfoTech/whm-cPanel.git`

---

## Table of Contents

1. [Prerequisites](#1-prerequisites)
2. [Initial VPS Setup](#2-initial-vps-setup)
3. [Install Required Software](#3-install-required-software)
4. [Install MongoDB with Authentication](#4-install-mongodb-with-authentication)
5. [Generate SSH Deploy Key & Add to GitHub](#5-generate-ssh-deploy-key--add-to-github)
6. [Clone the Repository](#6-clone-the-repository)
7. [Configure Environment Variables](#7-configure-environment-variables)
8. [First Build & Deploy](#8-first-build--deploy)
9. [Setup SSL (Auto-Renewal)](#9-setup-ssl-auto-renewal)
10. [Configure Nginx Reverse Proxy](#10-configure-nginx-reverse-proxy)
11. [Create Systemd Services](#11-create-systemd-services)
12. [Configure Firewall](#12-configure-firewall)
13. [Seed Demo Users](#13-seed-demo-users)
14. [Start Everything & Verify](#14-start-everything--verify)
15. [Setup Auto-Deploy (git push → auto deploy)](#15-setup-auto-deploy-git-push--auto-deploy)
16. [Maintenance & Troubleshooting](#16-maintenance--troubleshooting)

---

## 1. Prerequisites

| Requirement | Details |
|-------------|---------|
| **VPS** | Ubuntu 22.04 or 24.04 LTS (minimum 2 CPU, 4 GB RAM, 40 GB disk) |
| **Domain** | `panel.betazeninfotech.com` → DNS A record pointing to VPS IP |
| **Root access** | SSH access to the VPS |
| **GitHub** | Personal Access Token (PAT) with `repo` scope for private repos |

### Create a GitHub Personal Access Token

1. Go to: `https://github.com/settings/tokens?type=beta`
2. Click **"Generate new token"**
3. Name: `VPS Deploy`, Expiration: **No expiration**
4. Repository access: Select `BetaZen-InfoTech/whm-cPanel`
5. Permissions: **Contents → Read-only**
6. Click **"Generate token"** and **copy it** (you'll need it in Step 5)

---

## 2. Initial VPS Setup

SSH into your VPS as root:

```bash
ssh root@YOUR_VPS_IP
```

```bash
# Update system
apt update && apt upgrade -y

# Set timezone
timedatectl set-timezone Asia/Kolkata

# Set hostname
hostnamectl set-hostname panel.betazeninfotech.com
```

---

## 3. Install Required Software

```bash
# Essential tools
apt install -y curl wget git build-essential software-properties-common jq

# Install Go 1.22
wget https://go.dev/dl/go1.22.12.linux-amd64.tar.gz
rm -rf /usr/local/go
tar -C /usr/local -xzf go1.22.12.linux-amd64.tar.gz
rm go1.22.12.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
export PATH=$PATH:/usr/local/go/bin
go version

# Install Node.js 20 LTS
curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
apt install -y nodejs
node -v && npm -v

# Install Nginx
apt install -y nginx
systemctl enable nginx

# Install Certbot
apt install -y certbot python3-certbot-nginx
```

---

## 4. Install MongoDB with Authentication

```bash
# Import MongoDB 7.0 GPG key
curl -fsSL https://www.mongodb.org/static/pgp/server-7.0.asc | \
  gpg -o /usr/share/keyrings/mongodb-server-7.0.gpg --dearmor

# Add repository (Ubuntu 22.04 jammy)
echo "deb [ arch=amd64,arm64 signed-by=/usr/share/keyrings/mongodb-server-7.0.gpg ] https://repo.mongodb.org/apt/ubuntu jammy/mongodb-org/7.0 multiverse" | \
  tee /etc/apt/sources.list.d/mongodb-org-7.0.list

# Install & start
apt update
apt install -y mongodb-org
systemctl start mongod
systemctl enable mongod

# Verify
mongosh --eval "db.runCommand({ ping: 1 })"
```

### Create database user

Replace `YOUR_MONGO_PASSWORD` with a strong password:

```bash
mongosh << 'EOF'
use admin
db.createUser({
  user: "serverpanel",
  pwd: "YOUR_MONGO_PASSWORD",
  roles: [{ role: "readWrite", db: "serverpanel" }]
})
EOF
```

### Enable authentication

```bash
cat > /etc/mongod.conf << 'CONF'
storage:
  dbPath: /var/lib/mongodb
systemLog:
  destination: file
  logAppend: true
  path: /var/log/mongodb/mongod.log
net:
  port: 27017
  bindIp: 127.0.0.1
processManagement:
  timeZoneInfo: /usr/share/zoneinfo
security:
  authorization: enabled
CONF

systemctl restart mongod

# Verify auth works (use your password)
mongosh "mongodb://serverpanel:YOUR_MONGO_PASSWORD@127.0.0.1:27017/serverpanel?authSource=admin" --eval "db.runCommand({ ping: 1 })"
```

---

## 5. Generate SSH Deploy Key & Add to GitHub

This allows your VPS to pull code from the **private** GitHub repository.

### 5.1 — Generate SSH key pair on VPS

```bash
ssh-keygen -t ed25519 -C "deploy@panel.betazeninfotech.com" -f ~/.ssh/github_deploy -N ""
```

### 5.2 — Display the public key

```bash
cat ~/.ssh/github_deploy.pub
```

**Copy the entire output.**

### 5.3 — Add deploy key to GitHub

1. Go to: `https://github.com/BetaZen-InfoTech/whm-cPanel/settings/keys`
2. Click **"Add deploy key"**
3. Fill in:
   - **Title:** `VPS Deploy Key`
   - **Key:** Paste the public key from step 5.2
   - **Allow write access:** Leave **unchecked**
4. Click **"Add key"**

### 5.4 — Configure SSH to use this key

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

### 5.5 — Test the connection

```bash
ssh -T git@github.com
# Expected: Hi BetaZen-InfoTech/whm-cPanel! You've successfully authenticated...
```

---

## 6. Clone the Repository

```bash
mkdir -p /opt/serverpanel
cd /opt/serverpanel
git clone git@github.com:BetaZen-InfoTech/whm-cPanel.git .
git config --global --add safe.directory /opt/serverpanel

# Verify
ls -la
# Should show: backend/  frontend/  .env.example  Makefile  README.md  etc.
```

### Create required directories

```bash
mkdir -p /opt/serverpanel/bin
mkdir -p /opt/serverpanel/tmp
mkdir -p /opt/serverpanel/scripts
mkdir -p /var/backups/serverpanel
```

---

## 7. Configure Environment Variables

This creates `.env` with **auto-generated secrets**:

```bash
cd /opt/serverpanel

cat > /opt/serverpanel/.env << ENVEOF
APP_ENV=production
LOG_LEVEL=info

MONGO_URI=mongodb://serverpanel:YOUR_MONGO_PASSWORD@127.0.0.1:27017/serverpanel?authSource=admin
MONGO_DB_NAME=serverpanel

JWT_SECRET=$(openssl rand -hex 64)
JWT_ACCESS_EXPIRY=15m
JWT_REFRESH_EXPIRY=168h

DOMAIN=panel.betazeninfotech.com
SERVER_PORT=8080
TLS_CERT=
TLS_KEY=

AGENT_PORT=8443
AGENT_API_KEY=$(openssl rand -hex 32)
AGENT_TLS_CERT=
AGENT_TLS_KEY=

GITHUB_CLIENT_ID=
GITHUB_CLIENT_SECRET=
GITHUB_WEBHOOK_SECRET=

MAIL_HOSTNAME=mail.betazeninfotech.com

BACKUP_DIR=/var/backups/serverpanel
BACKUP_ENCRYPTION_KEY=$(openssl rand -hex 32)

RATE_LIMIT_WHM=200
RATE_LIMIT_CPANEL=100
ENVEOF

chmod 600 /opt/serverpanel/.env
```

> **Important:** Replace `YOUR_MONGO_PASSWORD` with the password from Step 4. JWT, Agent, and Backup keys are auto-generated.

---

## 8. First Build & Deploy

### Build backend

```bash
cd /opt/serverpanel/backend
go mod tidy
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/serverpanel/bin/server ./cmd/server
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/serverpanel/bin/agent ./cmd/agent
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/serverpanel/bin/seed ./cmd/seed

ls -lh /opt/serverpanel/bin/
# Should show: server  agent  seed
```

### Build frontend

```bash
cd /opt/serverpanel/frontend
npm install
npx turbo run build

# Verify
ls /opt/serverpanel/frontend/apps/whm/dist/index.html
ls /opt/serverpanel/frontend/apps/cpanel/dist/index.html
```

---

## 9. Setup SSL (Auto-Renewal)

### Get SSL certificate

Make sure the DNS A record `panel.betazeninfotech.com` → `YOUR_VPS_IP` is set first:

```bash
dig +short panel.betazeninfotech.com
# Should return your VPS IP
```

```bash
# Get certificate (uses Nginx plugin since Nginx is already running on port 80)
certbot certonly --nginx -d panel.betazeninfotech.com \
  --non-interactive --agree-tos --email admin@betazeninfotech.com
```

### Enable auto-renewal (runs every 12 hours, renews 30 days before expiry)

```bash
systemctl enable certbot.timer
systemctl start certbot.timer

# Add post-renewal hook to reload Nginx automatically
cat > /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh << 'EOF'
#!/bin/bash
systemctl reload nginx
EOF
chmod +x /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh

# Test renewal
certbot renew --dry-run
```

SSL auto-renewal is now fully automatic. Certbot will:
- Check for renewal every 12 hours via systemd timer
- Renew certificates 30 days before expiry
- Automatically reload Nginx after renewal

---

## 10. Configure Nginx Reverse Proxy

```bash
tee /etc/nginx/sites-available/serverpanel << 'NGINX'
server {
    listen 80;
    listen [::]:80;
    server_name panel.betazeninfotech.com;
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name panel.betazeninfotech.com;

    # SSL
    ssl_certificate     /etc/letsencrypt/live/panel.betazeninfotech.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.betazeninfotech.com/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 1d;
    ssl_session_tickets off;

    # Security headers
    add_header Strict-Transport-Security "max-age=63072000" always;
    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-XSS-Protection "1; mode=block" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;

    # General
    client_max_body_size 500M;
    proxy_read_timeout 600s;
    proxy_send_timeout 600s;

    # API → Go backend (port 8080)
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

    # WebSocket → Go backend (real-time terminal output)
    location /ws/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }

    # WHM Panel
    location /whm/ {
        alias /opt/serverpanel/frontend/apps/whm/dist/;
        try_files $uri $uri/ /whm/index.html;
        location ~* \.(js|css|png|jpg|jpeg|gif|ico|svg|woff|woff2|ttf|eot)$ {
            expires 1y;
            add_header Cache-Control "public, immutable";
        }
    }

    # cPanel
    location /cpanel/ {
        alias /opt/serverpanel/frontend/apps/cpanel/dist/;
        try_files $uri $uri/ /cpanel/index.html;
        location ~* \.(js|css|png|jpg|jpeg|gif|ico|svg|woff|woff2|ttf|eot)$ {
            expires 1y;
            add_header Cache-Control "public, immutable";
        }
    }

    # Root redirect
    location = / {
        return 302 /whm/;
    }
}
NGINX

ln -sf /etc/nginx/sites-available/serverpanel /etc/nginx/sites-enabled/
rm -f /etc/nginx/sites-enabled/default
nginx -t
systemctl reload nginx
```

---

## 11. Create Systemd Services

### ServerPanel Server

```bash
tee /etc/systemd/system/serverpanel.service << 'EOF'
[Unit]
Description=ServerPanel Server
After=network.target mongod.service
Wants=mongod.service

[Service]
Type=simple
User=root
WorkingDirectory=/opt/serverpanel
ExecStart=/opt/serverpanel/bin/server
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
```

### ServerPanel Agent

```bash
tee /etc/systemd/system/serverpanel-agent.service << 'EOF'
[Unit]
Description=ServerPanel Agent
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/serverpanel
ExecStart=/opt/serverpanel/bin/agent
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
```

```bash
systemctl daemon-reload
```

---

## 12. Configure Firewall

```bash
ufw allow OpenSSH
ufw allow 'Nginx Full'
ufw --force enable
ufw status
```

---

## 13. Seed Demo Users

Instead of manually inserting into MongoDB, use the seed command:

```bash
cd /opt/serverpanel
/opt/serverpanel/bin/seed
```

Expected output:
```
[config] Loaded .env from /opt/serverpanel/.env
[created] admin@betazeninfotech.com (vendor_owner) — password: admin123
[created] demo@betazeninfotech.com (customer) — password: demo123
```

| User | Email | Password | Panel |
|------|-------|----------|-------|
| WHM Admin | `admin@betazeninfotech.com` | `admin123` | `/whm/` |
| cPanel Demo | `demo@betazeninfotech.com` | `demo123` | `/cpanel/` |

---

## 14. Start Everything & Verify

```bash
# Start services
systemctl enable serverpanel serverpanel-agent
systemctl start serverpanel serverpanel-agent

# Wait for startup
sleep 3

# Check status
systemctl status serverpanel --no-pager -l
journalctl -u serverpanel --no-pager -n 10

# Health check (internal)
curl -s http://127.0.0.1:8080/api/v1/health
# Expected: {"success":true,"data":{"status":"ok","service":"serverpanel"}}

# Health check (external)
curl -s https://panel.betazeninfotech.com/api/v1/health

# Test login
curl -s -X POST https://panel.betazeninfotech.com/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@betazeninfotech.com","password":"admin123"}'
```

### Open in browser

| URL | Panel |
|-----|-------|
| `https://panel.betazeninfotech.com/` | Redirects to WHM |
| `https://panel.betazeninfotech.com/whm/` | WHM Admin Panel |
| `https://panel.betazeninfotech.com/cpanel/` | cPanel Customer Portal |

---

## 15. Setup Auto-Deploy (git push → auto deploy)

Uses **GitHub Actions** — every `git push` to `main` automatically deploys to VPS. No webhook listener, no extra ports, no manual work.

The workflow file `.github/workflows/deploy.yml` is already in the repo.

### 14.1 — Generate SSH key on VPS for GitHub Actions

```bash
ssh-keygen -t ed25519 -C "github-actions-deploy" -f ~/.ssh/github_actions -N ""
cat ~/.ssh/github_actions.pub >> ~/.ssh/authorized_keys
```

### 14.2 — Copy the private key

```bash
cat ~/.ssh/github_actions
```

Copy the **entire output** (including `-----BEGIN` and `-----END` lines).

### 14.3 — Add GitHub Secrets

1. Go to: `https://github.com/BetaZen-InfoTech/whm-cPanel/settings/secrets/actions`
2. Click **"New repository secret"** and add these two secrets:

| Secret Name | Value |
|-------------|-------|
| `VPS_HOST` | Your VPS IP address (e.g., `103.xxx.xxx.xxx`) |
| `VPS_SSH_KEY` | The entire private key you copied in step 14.2 |

### 14.4 — Create the deploy script on VPS

```bash
tee /opt/serverpanel/scripts/deploy.sh << 'SCRIPT'
#!/bin/bash
set -e

APP_DIR="/opt/serverpanel"
LOG_FILE="/var/log/serverpanel-deploy.log"
export PATH=$PATH:/usr/local/go/bin

echo "" >> "$LOG_FILE"
echo "========================================" >> "$LOG_FILE"
echo "$(date '+%Y-%m-%d %H:%M:%S') — Deploy started" >> "$LOG_FILE"
echo "========================================" >> "$LOG_FILE"

cd "$APP_DIR"

# Pull latest code
echo "[1/5] Pulling latest code..." | tee -a "$LOG_FILE"
git pull origin main >> "$LOG_FILE" 2>&1

# Build backend
echo "[2/5] Building backend..." | tee -a "$LOG_FILE"
cd "$APP_DIR/backend"
go mod tidy >> "$LOG_FILE" 2>&1
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o "$APP_DIR/bin/server" ./cmd/server >> "$LOG_FILE" 2>&1
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o "$APP_DIR/bin/agent" ./cmd/agent >> "$LOG_FILE" 2>&1
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o "$APP_DIR/bin/seed" ./cmd/seed >> "$LOG_FILE" 2>&1

# Build frontend
echo "[3/5] Building frontend..." | tee -a "$LOG_FILE"
cd "$APP_DIR/frontend"
npm install >> "$LOG_FILE" 2>&1
npx turbo run build >> "$LOG_FILE" 2>&1

# Restart services
echo "[4/5] Restarting services..." | tee -a "$LOG_FILE"
systemctl restart serverpanel >> "$LOG_FILE" 2>&1
systemctl restart serverpanel-agent >> "$LOG_FILE" 2>&1

# Health check
echo "[5/5] Health check..." | tee -a "$LOG_FILE"
sleep 3
if curl -sf http://127.0.0.1:8080/api/v1/health > /dev/null 2>&1; then
  echo "$(date '+%Y-%m-%d %H:%M:%S') — Deploy SUCCESS" | tee -a "$LOG_FILE"
else
  echo "$(date '+%Y-%m-%d %H:%M:%S') — Deploy WARNING: health check failed" | tee -a "$LOG_FILE"
fi
SCRIPT

chmod +x /opt/serverpanel/scripts/deploy.sh
```

### 14.5 — Test auto-deploy

Push any change from your local machine:

```bash
git add . && git commit -m "test auto deploy" && git push
```

Then check:
- **GitHub:** Go to `Actions` tab — you should see the deploy workflow running
- **VPS:** `tail -f /var/log/serverpanel-deploy.log` — watch the deploy in real time

### How it works

```
You: git push to main
        │
        ▼
GitHub Actions triggers deploy.yml
        │
        ▼
SSH into VPS as root
        │
        ▼
Runs deploy.sh:
  1. git pull origin main
  2. go build (server + agent + seed)
  3. npm install && turbo build
  4. systemctl restart serverpanel
  5. Health check ✓
        │
        ▼
Live in ~90 seconds
```

---

## 16. Maintenance & Troubleshooting

### Manual deploy

```bash
/opt/serverpanel/scripts/deploy.sh
```

### View logs

```bash
# Server logs
journalctl -u serverpanel -f

# Agent logs
journalctl -u serverpanel-agent -f

# Deploy logs
tail -f /var/log/serverpanel-deploy.log

# Nginx logs
tail -f /var/log/nginx/error.log
```

### Restart services

```bash
systemctl restart serverpanel
systemctl restart serverpanel-agent
systemctl reload nginx
```

### SSL certificate status

```bash
# Check certificate expiry
certbot certificates

# Force renewal (normally automatic)
certbot renew --force-renewal
systemctl reload nginx
```

### MongoDB backup

```bash
mongodump --uri="mongodb://serverpanel:YOUR_MONGO_PASSWORD@127.0.0.1:27017/serverpanel?authSource=admin" \
  --out="/var/backups/serverpanel/mongo-$(date +%Y%m%d)"
```

### 502 Bad Gateway

```bash
# Is backend running?
curl -s http://127.0.0.1:8080/api/v1/health

# If not:
systemctl status serverpanel
journalctl -u serverpanel -n 30 --no-pager

# Common causes:
# - MongoDB not running: systemctl start mongod
# - Wrong MONGO_URI in .env
# - Binary not built: cd /opt/serverpanel/backend && go build ...
```

### Server won't start

```bash
journalctl -u serverpanel -n 50 --no-pager

# Check MongoDB
systemctl status mongod
ss -tlnp | grep 27017

# Check port 8080
ss -tlnp | grep 8080
```

---

## Quick Reference

| Command | Description |
|---------|-------------|
| `/opt/serverpanel/scripts/deploy.sh` | Manual deploy |
| `systemctl status serverpanel` | Check server status |
| `systemctl restart serverpanel` | Restart server |
| `journalctl -u serverpanel -f` | Stream server logs |
| `tail -f /var/log/serverpanel-deploy.log` | Deploy logs |
| `nginx -t && systemctl reload nginx` | Test & reload Nginx |
| `certbot certificates` | Check SSL status |
| `certbot renew` | Renew SSL (normally automatic) |

---

## Security Checklist

- [ ] `.env` file has `chmod 600`
- [ ] MongoDB authentication enabled
- [ ] Strong JWT secret (auto-generated, 128 hex chars)
- [ ] UFW firewall enabled
- [ ] SSL/TLS via Let's Encrypt with auto-renewal
- [ ] HSTS header enabled in Nginx
- [ ] `APP_ENV=production` in `.env`

---

## How Auto-Deploy Works

```
Developer pushes to main
        │
        ▼
GitHub sends webhook POST to VPS:9000
        │
        ▼
webhook-listener.sh triggers deploy.sh
        │
        ▼
deploy.sh runs:
  1. git pull origin main
  2. go build (server + agent + seed)
  3. npm install && turbo build
  4. systemctl restart serverpanel
  5. Health check ✓
        │
        ▼
Live in ~60 seconds, zero downtime
```

---

*Built with ServerPanel by BetaZen InfoTech*
