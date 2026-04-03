# Server Transfer Process

Complete guide for migrating a server (or specific domains) into ServerPanel using the built-in transfer wizard.

---

## Prerequisites

- **Source server**: Root SSH access (password authentication)
- **Destination server**: ServerPanel installed and running
- **DNS**: Nameservers should point to `dns1–dns4.betazeninfotech.com` (or update after transfer)
- **Ports**: SSH (22) open between source and destination

---

## How It Works

The transfer wizard connects to the source server via SSH, discovers all resources, and migrates them step-by-step to the destination server. All DNS A records, mail records, and SPF records are automatically updated to the new server's IP.

```
┌─────────────────────┐         SSH          ┌─────────────────────┐
│    Source Server     │ ──────────────────►  │  Destination Server │
│  (old VPS)          │   files, DNS, DB,    │  (ServerPanel)      │
│                     │   email, SSL, cron   │                     │
└─────────────────────┘                      └─────────────────────┘
```

---

## Transfer Steps

### Step 1 — Validate Connection

Establishes SSH connection to the source server and verifies credentials.

### Step 2 — Discover Resources

Scans the source server for all transferable resources:

| Resource | Discovery Method |
|---|---|
| Domains | `ls /home/*/domains/` |
| MongoDB Databases | `mongosh --eval listDatabases` |
| MySQL Databases | `mysql -N -e "SHOW DATABASES"` |
| Email Domains | `ls /var/mail/vhosts/` |
| Email Forwarders | `grep '@domain' /etc/postfix/virtual_alias_maps` |
| DNS Zones | `pdnsutil list-all-zones` |
| SSL Certificates | `ls /etc/letsencrypt/live/` |
| Cron Jobs | `ls /var/spool/cron/crontabs/` |
| FTP Users | `pure-pw list` |
| PHP Versions | `ls /etc/php/` |

### Step 3 — Transfer Software (PHP Versions)

- Detects all PHP versions installed on source (`/etc/php/`)
- Checks which are already installed on destination
- Installs missing PHP versions automatically

### Step 4 — Transfer Domains & Files

For each domain:

1. **Detect system user** from source (`stat -c '%U'`)
2. **Detect PHP version** from source Nginx config
3. **Create system user** on destination (`useradd`)
4. **Create directory structure** (`/home/{user}/domains/{domain}/public_html`)
5. **Download & restore files** via `tar` + `scp`
6. **Create PHP-FPM pool** for the domain
7. **Create Nginx vhost** (HTTP)
8. **Save domain record** to MongoDB (visible in WHM panel)

### Step 5 — Transfer DNS Zones

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

### Step 6 — Transfer SSL Certificates

For each domain:

1. **Download certs** from source (`/etc/letsencrypt/live/{domain}/`)
2. If download fails → **Issue new Let's Encrypt cert** via Certbot
3. **Copy certs** to destination cert directory
4. **Upgrade Nginx vhost** to HTTPS (443 + redirect)
5. **Reload Nginx**

### Step 7 — Transfer Databases

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

### Step 8 — Transfer Email

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
7. **Restart OpenDKIM and reload Postfix**

### Step 9 — Transfer Cron Jobs

For each user with crontabs:

1. **Export crontab** from source (`crontab -u {user} -l`)
2. **Parse schedule + command** (skip comments)
3. **Write crontab** on destination
4. **Save cron job** to MongoDB (visible in Cron panel)

### Step 10 — Transfer FTP Accounts

For each FTP user:

1. **Match FTP username** to a domain (by username pattern, e.g. `user_domain_com`)
2. **Look up domain record** in MongoDB for correct system user and home directory
3. **Generate new password** (old passwords cannot be recovered from Pure-FTPd)
4. **Create Pure-FTPd virtual user** chrooted to the domain's `public_html`
5. **Save FTP account** to MongoDB (marked as `IsRoot: true` — non-deletable)

> **Note**: FTP passwords are reset during transfer. Users must set new passwords.

### Step 11 — Transfer Firewall Rules

1. **Export UFW rules** from source (`ufw status`)
2. **Enable UFW** on destination
3. **Import rules** (ALLOW/DENY/LIMIT for each port)
4. Falls back to `iptables-save` export if UFW is not available

### Step 12 — Transfer Server Config

- Captures source Nginx configs for reference
- Logs for manual review

### Step 13 — Verify Transfer

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
