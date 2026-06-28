# Server Transfer Process

Complete guide for migrating a server (or specific domains) into Betazen Server Panel using the built-in transfer wizard.

---

## Quick Start — Fresh Server Setup + Migration

### 1. Install Betazen Server Panel on the new server (Ubuntu 22.04/24.04)

```bash
curl -sSL https://raw.githubusercontent.com/BetaZen-InfoTech/server-management/main/install.sh | bash
```

The installer will prompt for:
- **Panel domain** (e.g., `panel.example.com`) — or uses server IP
- **Admin email** and **password**
- **MongoDB password** (auto-generated if left blank)

It automatically installs and configures all 12 components:

| # | Component | Purpose |
|---|-----------|---------|
| 1 | Base packages | curl, git, build-essential, ufw, fail2ban, sshpass |
| 2 | Nginx | Web server + reverse proxy for panel |
| 3 | PHP 8.2 | PHP-FPM with mysql, xml, mbstring, curl, gd, etc. |
| 4 | MongoDB 8.0 | Database with auth user creation |
| 5 | MariaDB | MySQL database for site databases |
| 6 | Postfix + Dovecot + OpenDKIM | Full email stack |
| 7 | PowerDNS | DNS server with SQLite backend |
| 8 | Certbot + Pure-FTPd | SSL certificates + FTP server |
| 9 | Go 1.23 | Compiles the backend binary |
| 10 | Node.js 20 | Builds the frontend (Vite + Turbo) |
| 11 | Betazen Server Panel | Clones repo, builds backend + frontend, creates .env |
| 12 | Systemd + Nginx | Service file, reverse proxy, firewall, auto-SSL |

After install (~10-15 min), you get:
- **WHM**: `https://your-domain/whm` (nginx reverse-proxy handles 80→443 and forwards to the backend on :8080 internally — no port in the URL)
- **cPanel**: `https://your-domain/cpanel`

> Before DNS is pointed at the new server, you can hit the panel directly via the server IP: `http://<server-ip>/whm`. The install.sh auto-issues a Let's Encrypt cert as soon as the panel domain resolves here.

### 2. Transfer from old server

1. Open WHM panel → **Transfer** page
2. Enter old server **IP**, **SSH port** (22), **root** user, **password**
3. Click **Connect & Discover** — auto-detects server type (cPanel, Plesk, DirectAdmin, bare)
4. Review discovered resources (domains, databases, email, DNS, SSL, etc.)
5. Select components to transfer (or leave all checked for full migration)
6. Click **Start Transfer** — runs in background with live progress

### 3. Update DNS

Point your domain nameservers / A records to the new server IP. Done!

```
Old Server (187.127.132.4)  ──SSH──►  New Server (187.127.129.188)
   domains, files, DNS,                  Betazen Server Panel auto-creates
   databases, email, SSL,                nginx, PHP-FPM, DNS zones,
   cron, FTP, firewall                   SSL certs, email accounts
```

---

## Supported Source Server Types

The transfer wizard auto-detects the source server type and discovers resources from the correct paths:

| Server Type | Detection Method | Domain Discovery |
|-------------|-----------------|------------------|
| **cPanel/WHM** | `/usr/local/cpanel/cpanel` | `/etc/trueuserdomains`, `/etc/userdatadomains` |
| **Plesk** | `/usr/local/psa/version` | `/var/www/vhosts/`, Plesk MySQL DB |
| **DirectAdmin** | `/usr/local/directadmin/directadmin` | `/etc/virtual/domainowners` |
| **CyberPanel** | `/etc/cyberpanel/machineIP` | CyberPanel CLI |
| **Betazen Server Panel** | `/opt/serverpanel` | `/home/*/domains/` |
| **Bare server** | (fallback) | nginx/apache configs, `/home/*/public_html/` |

---

## Prerequisites

- **Source server**: Root SSH access (password authentication)
- **Destination server**: Betazen Server Panel installed and running (use `install.sh` for fresh setup)
- **DNS**: Nameservers should point to `dns1–dns4.betazeninfotech.com` (or update after transfer)
- **Ports**: SSH (22) open between source and destination

---

## How It Works

The transfer wizard connects to the source server via SSH, discovers all resources, and migrates them step-by-step to the destination server. All DNS A records, mail records, and SPF records are automatically updated to the new server's IP.

```
┌─────────────────────┐         SSH          ┌─────────────────────┐
│    Source Server     │ ──────────────────►  │  Destination Server │
│  (old VPS)          │   files, DNS, DB,    │  (Betazen Server Panel)      │
│                     │   email, SSL, cron   │                     │
└─────────────────────┘                      └─────────────────────┘
```

---

## Transfer Steps

### Step 1 — Validate Connection

Establishes SSH connection to the source server and verifies credentials.

### Step 2 — Discover Resources

Scans the source server for all transferable resources:

| Resource | Discovery Methods (checked in order) |
|---|---|
| Server Type | cPanel, Plesk, DirectAdmin, CyberPanel, Betazen Server Panel, bare |
| Domains | `/home/*/domains/`, cPanel `/etc/trueuserdomains`, Plesk `/var/www/vhosts/`, DirectAdmin `/etc/virtual/domainowners`, nginx/apache configs, `/home/*/public_html/` |
| MongoDB Databases | `mongosh --eval listDatabases` (excludes admin, local, config) |
| MySQL Databases | `mysql -N -e "SHOW DATABASES"` (excludes system DBs) |
| Email Domains | `/var/mail/vhosts/`, `/etc/postfix/virtual_domains`, `/etc/dovecot/users`, `/home/*/mail/` |
| Email Forwarders | `grep '@domain' /etc/postfix/virtual_alias_maps` |
| DNS Zones | `pdnsutil list-all-zones`, BIND `/etc/bind/zones/`, `named.conf.local` |
| SSL Certificates | `/etc/letsencrypt/live/`, `/etc/ssl/custom/` |
| Cron Jobs | `/var/spool/cron/crontabs/`, `/var/spool/cron/` |
| FTP Users | `pure-pw list` |
| PHP Versions | `ls /etc/php/` |

### Step 3 — Transfer Hostname (optional)

When the `hostname` component is enabled, the destination's system hostname is set to match the source so that mail HELO / SMTP banners stay consistent for transferred mailboxes.

1. Read source hostname via `hostnamectl --static`
2. `hostnamectl set-hostname <source-hostname>` on destination
3. Update `/etc/hosts` so `127.0.1.1` resolves to the new name
4. Reload Postfix so its `myhostname` setting picks up the new value

> Leave this off if your new server already has a meaningful hostname (e.g. `web1.betazeninfotech.com`) — renaming it can break monitoring and SSH `known_hosts`.

### Step 4 — Transfer Software (PHP Versions)

- Detects all PHP versions installed on source (`/etc/php/`)
- Checks which are already installed on destination
- Installs missing PHP versions automatically

### Step 5 — Transfer Domains & Files

For each domain:

1. **Detect system user** from source (`stat -c '%U'`)
2. **Detect PHP version** from source Nginx config
3. **Create system user** on destination (`useradd`)
4. **Create directory structure** (`/home/{user}/domains/{domain}/public_html`)
5. **Download & restore files** via `tar` + `scp`
6. **Create PHP-FPM pool** for the domain
7. **Create Nginx vhost** (HTTP)
8. **Save domain record** to MongoDB (visible in WHM panel)

### Step 6 — Transfer DNS Zones

For each DNS zone:

1. **Export zone** from source via `pdnsutil list-zone`
2. **Detect old server IP** from zone's A records
3. **Create zone** on destination PowerDNS
4. **Import all records** with automatic IP replacement:
   - **A records**: Old IP → New server IP (all: root, mail, subdomains)
   - **SPF records**: `ip4:oldIP` → `ip4:newIP`
   - **MX, CNAME, TXT, NS**: Imported as-is
   - **SOA**: Skipped (auto-created by pdnsutil)
5. **Save zone + records** to MongoDB (visible in DNS panel)
6. **Reload PowerDNS**

### Step 7 — Transfer SSL Certificates

For each domain:

1. **Download certs** from source (`/etc/letsencrypt/live/{domain}/`)
2. If download fails → **Issue new Let's Encrypt cert** via Certbot
3. **Copy certs** to destination cert directory
4. **Upgrade Nginx vhost** to HTTPS (443 + redirect)
5. **Reload Nginx**

### Step 8 — Transfer Databases

#### MongoDB Databases

For each MongoDB database:

1. **Run `mongodump`** on source (compressed)
2. **Download dump** via SCP
3. **Run `mongorestore`** on destination
4. **Save database record** to MongoDB panel
5. **Cleanup** temp files

#### MySQL/MariaDB Databases (phpMyAdmin)

For each MySQL database:

1. **Run `mysqldump`** on source with `--single-transaction --routines --triggers --events`
2. **Download compressed dump** via SCP
3. **Create database** on destination (`CREATE DATABASE IF NOT EXISTS`)
4. **Restore dump** via `gunzip | mysql`
5. **Discover MySQL users** with grants for the database from source
6. **Recreate users** on destination with new passwords and full grants
7. **Save database record + user records** to MongoDB panel
8. **Cleanup** temp files

> **Note**: MySQL user passwords are reset during transfer. Update application configs (e.g., `wp-config.php`) with new credentials.

### Step 9 — Transfer Email

For each email domain:

1. **Download mail data** from source (`/var/mail/vhosts/{domain}/`)
2. **Restore mail directories** on destination
3. **Create maildir structure** (`cur/`, `new/`, `tmp/`) with `vmail:vmail` ownership
4. **Setup Postfix virtual domain** and virtual mailbox mappings
5. **Generate Dovecot password hash** (SHA512-CRYPT) for each mailbox
6. **Add Dovecot user entries** to `/etc/dovecot/users`
7. **Generate DKIM keys** (OpenDKIM)
8. **Configure signing table, key table, trusted hosts**
9. **Transfer email forwarders** from `/etc/postfix/virtual_alias_maps`
10. **Save mailbox + forwarder records** to MongoDB
11. **Restart OpenDKIM and reload Postfix**

### Step 10 — Transfer Cron Jobs

For each user with crontabs:

1. **Export crontab** from source (`crontab -u {user} -l`)
2. **Parse schedule + command** (skip comments)
3. **Write crontab** on destination
4. **Save cron job** to MongoDB (visible in Cron panel)

### Step 11 — Transfer FTP Accounts

For each FTP user:

1. **Match FTP username** to a domain (by username pattern, e.g. `user_domain_com`)
2. **Look up domain record** in MongoDB for correct system user and home directory
3. **Generate new password** (old passwords cannot be recovered from Pure-FTPd)
4. **Create Pure-FTPd virtual user** chrooted to the domain's `public_html`
5. **Save FTP account** to MongoDB (marked as `IsRoot: true` — non-deletable)

> **Note**: FTP passwords are reset during transfer. Users must set new passwords.

### Step 12 — Transfer Firewall Rules

1. **Export UFW rules** from source (`ufw status`)
2. **Enable UFW** on destination
3. **Import rules** (ALLOW/DENY/LIMIT for each port)
4. Falls back to `iptables-save` export if UFW is not available

### Step 13 — Transfer Server Config

- Captures source Nginx configs for reference
- Logs for manual review

### Step 14 — Verify Transfer

Post-transfer health checks:

| Check | Action on Failure |
|---|---|
| `nginx -t` | Log warning for manual fix |
| PHP-FPM running per domain | Auto-start service |
| DNS resolution per domain | Log mismatch warning |
| Postfix running | Auto-start service |
| Dovecot running | Auto-start service |

---

## Transfer Components

All components can be individually toggled in the wizard (Step 3):

| Component | Key | What It Does |
|---|---|---|
| Hostname | `hostname` | Sets destination hostname to match source |
| Software | `software` | Installs matching PHP versions |
| Domains | `domains` | Creates domain records, Nginx, PHP-FPM |
| Website Files | `files` | Transfers `/home/{user}/` directories |
| DNS Zones & Records | `dns` | Full zone transfer with IP auto-update |
| SSL Certificates | `ssl` | Transfers certs or issues new ones |
| Databases | `databases` | MongoDB dump & restore |
| Email Accounts & Data | `email_data` | Mail data + Postfix/DKIM setup |
| FTP Accounts | `ftp_accounts` | Recreates FTP users (new passwords) |
| Cron Jobs | `cron_jobs` | Transfers crontabs |
| Firewall Rules | `firewall` | Imports UFW/iptables rules |
| Server Configuration | `server_config` | Captures configs for review |

---

## IP Auto-Update

When transferring DNS zones, the system automatically replaces the old server's IP with the new one:

```
Source Server: 187.127.132.4     →     Destination: 187.127.129.188

A     @           187.127.132.4  →     187.127.129.188
A     mail        187.127.132.4  →     187.127.129.188
A     subdomain   187.127.132.4  →     187.127.129.188
TXT   @   "v=spf1 ip4:187.127.132.4 ~all"  →  "v=spf1 ip4:187.127.129.188 ~all"
MX    @   10 mail.example.com.   →     (unchanged — resolves via updated mail A record)
```

The destination server IP is determined from:
1. `SERVER_IP` environment variable (if set in `.env`)
2. Auto-detected via `hostname -I` (fallback)

---

## Configuration

Add to your `.env` file on the destination server:

```env
# Server IP used for DNS A records, MX, SPF etc.
# Auto-detected from hostname -I if not set.
SERVER_IP=187.127.129.188
```

---

## API Endpoints

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/whm/transfers` | List all transfer jobs |
| `GET` | `/api/v1/whm/transfers/:id` | Get transfer job details |
| `POST` | `/api/v1/whm/transfers` | Start a new transfer |
| `POST` | `/api/v1/whm/transfers/test-connection` | Test SSH connection |
| `POST` | `/api/v1/whm/transfers/discover` | Discover source resources |
| `POST` | `/api/v1/whm/transfers/:id/cancel` | Cancel a running transfer |

### Start Transfer Request

```json
{
  "source_ip": "192.168.1.100",
  "source_port": 22,
  "username": "root",
  "password": "your-ssh-password",
  "protocol": "ssh",
  "components": {
    "hostname": true,
    "software": true,
    "dns": true,
    "ssl": true,
    "domains": true,
    "files": true,
    "databases": true,
    "email_data": true,
    "ftp_accounts": true,
    "cron_jobs": true,
    "firewall": true,
    "server_config": true
  },
  "domains": []
}
```

> Pass specific domains in the `domains` array for partial transfer. Leave empty for full server migration.

---

## Job Status

| Status | Meaning |
|---|---|
| `pending` | Job created, waiting to start |
| `in_progress` | Transfer is running |
| `completed` | All steps finished successfully |
| `partial` | Completed with some failed steps |
| `failed` | Critical failure (e.g., SSH connection failed) |
| `cancelled` | Manually cancelled by user |

---

## Post-Transfer Checklist

After the transfer completes:

- [ ] Verify domains load correctly in browser
- [ ] Check DNS propagation (`dig domain.com @dns1.betazeninfotech.com`)
- [ ] Update domain registrar nameservers if not already pointing to your DNS
- [ ] Test email send/receive for transferred domains
- [ ] Reset email passwords (temporary passwords generated during transfer)
- [ ] Verify SSL certificates are valid (`https://domain.com`)
- [ ] Reset FTP passwords for transferred accounts
- [ ] Update MySQL credentials in application configs (`wp-config.php`, `.env` files)
- [ ] Verify MySQL databases via phpMyAdmin
- [ ] Review firewall rules imported
- [ ] Check cron jobs are running (`crontab -u {user} -l`)
- [ ] Remove source server access credentials from transfer logs

---

## Full Installation Reference (install.sh)

The one-click installer creates a production-ready Betazen Server Panel instance:

### What install.sh does

```
install.sh
├── 1.  apt-get update + base packages (curl, git, ufw, fail2ban, sshpass...)
├── 2.  Nginx (web server)
├── 3.  PHP 8.2 (FPM + 15 extensions)
├── 4.  MongoDB 8.0 (with auth user + admin user)
├── 5.  MariaDB (MySQL-compatible)
├── 6.  Email Stack
│   ├── Postfix (SMTP) — virtual domains, virtual mailboxes, alias maps
│   ├── Dovecot (IMAP/POP3) — user auth from /etc/dovecot/users
│   └── OpenDKIM — signing table, key table, trusted hosts
├── 7.  PowerDNS (DNS server with SQLite backend, API enabled)
├── 8.  Certbot (Let's Encrypt) + Pure-FTPd
├── 9.  Go 1.23 (compiles backend)
├── 10. Node.js 20 + npm (builds frontend)
├── 11. Betazen Server Panel
│   ├── git clone → /opt/serverpanel
│   ├── Generate .env (MongoDB URI, JWT secret, agent key, etc.)
│   ├── go build → /opt/serverpanel/bin/server
│   ├── go build → /opt/serverpanel/bin/seed
│   ├── npm install + turbo build (frontend SPAs)
│   └── Seed admin user to MongoDB
└── 12. System Configuration
    ├── systemd service (serverpanel.service)
    ├── Nginx reverse proxy (port 8080 → panel)
    ├── UFW firewall (22, 80, 443, 53, 25, 587, 993, 995, 21)
    └── Auto-SSL for panel domain (if DNS is ready)
```

### Generated file structure on server

```
/opt/serverpanel/               # Installation directory
├── .env                        # Configuration (auto-generated, chmod 600)
├── bin/
│   ├── server                  # Compiled Go backend
│   └── seed                    # Database seeder
├── backend/                    # Go source code
├── frontend/                   # React frontend (built SPAs)
│   └── apps/
│       ├── whm/dist/           # WHM panel build
│       └── cpanel/dist/        # cPanel build
/etc/systemd/system/
└── serverpanel.service         # Systemd service file
/etc/nginx/sites-available/
└── serverpanel                 # Nginx reverse proxy config
```

### Usage

```bash
# Install on a fresh Ubuntu 22.04/24.04 server:
curl -sSL https://raw.githubusercontent.com/BetaZen-InfoTech/server-management/main/install.sh | bash

# Or download and run manually:
wget https://raw.githubusercontent.com/BetaZen-InfoTech/server-management/main/install.sh
chmod +x install.sh
./install.sh
```

### Managing the service

```bash
systemctl start serverpanel      # Start
systemctl stop serverpanel       # Stop
systemctl restart serverpanel    # Restart
systemctl status serverpanel     # Check status
journalctl -u serverpanel -f     # View live logs
```

### Updating Betazen Server Panel

For minor patches the one-liner below is fine, but it pulls before building — a failed build leaves `/opt/serverpanel` on a half-applied commit. For anything involving migrations, schema changes, new env vars, or service restarts, read [`server-panel-upgrade.md`](./server-panel-upgrade.md) first. It covers pre-upgrade backup, staged build, atomic swap, rollback, and post-upgrade verification.

```bash
# Minor patch only (no schema / env / dependency changes):
cd /opt/serverpanel
git pull
cd backend && /opt/go/1.23/bin/go build -o ../bin/server ./cmd/server
cd ../frontend && npm install --legacy-peer-deps && npx turbo build
systemctl restart serverpanel
```
