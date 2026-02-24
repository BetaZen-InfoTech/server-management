# ServerPanel — Vendor (WHM) Feature Guide

> Complete server management features available from the **Vendor Panel** (WHM-style interface).
> Both the **WHM (Vendor)** and **cPanel (Client)** panels are served from a **single domain** on one Go binary — path-based routing separates them (`/whm/*` and `/cpanel/*`).
> This panel gives hosting providers full control over their VPS — domains, apps, databases, email, DNS, SSL, backups, firewall, monitoring, and more.

---

## Table of Contents

1. [Authentication & User Management](#1-authentication--user-management)
2. [Domain Management](#2-domain-management)
3. [Application Deployment](#3-application-deployment)
4. [Database Management](#4-database-management)
5. [Email Server Management](#5-email-server-management)
6. [DNS Management](#6-dns-management)
7. [SSL / TLS Certificate Management](#7-ssl--tls-certificate-management)
8. [Backup & Restore](#8-backup--restore)
9. [WordPress Management](#9-wordpress-management)
10. [Firewall & Security](#10-firewall--security)
11. [Software Installation](#11-software-installation)
12. [System Monitoring](#12-system-monitoring)
13. [Log Viewer](#13-log-viewer)
14. [Cron Job Management](#14-cron-job-management)
15. [File Manager](#15-file-manager)
16. [SSH Key Management](#16-ssh-key-management)
17. [Process Manager](#17-process-manager)
18. [Resource Usage & Bandwidth](#18-resource-usage--bandwidth)
19. [Notification & Webhooks](#19-notification--webhooks)
20. [Activity / Audit Log](#20-activity--audit-log)
21. [Server Configuration](#21-server-configuration)
22. [Maintenance Mode](#22-maintenance-mode)
23. [GitHub Deployment (CI/CD)](#23-github-deployment-cicd)
24. [Role-Based Access Control (RBAC)](#24-role-based-access-control-rbac)
25. [Client (cPanel) Features](#25-client-cpanel-features)
26. [Project Structure & Tech Stack](#26-project-structure--tech-stack)

---

## API Conventions

All endpoints follow these conventions unless stated otherwise.

### Base URL

```
https://panel.betazeninfotech.com/api/v1/whm/     # WHM (Vendor) endpoints
https://panel.betazeninfotech.com/api/v1/cpanel/   # cPanel (Client) endpoints
https://your-vps:8443/api/v1/                 # Agent (internal, on VPS)
```

### Authentication

All API requests (except `POST /auth/login`) require a JWT Bearer token:

```
Authorization: Bearer <access_token>
```

### Standard Success Response

```json
{
  "success": true,
  "message": "Operation completed successfully",
  "data": { ... }
}
```

### Standard Error Response

```json
{
  "success": false,
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Domain name is required",
    "details": [
      { "field": "domain", "message": "This field is required" }
    ]
  }
}
```

### Common Error Codes

| HTTP Status | Error Code | Description |
|-------------|-----------|-------------|
| 400 | `VALIDATION_ERROR` | Invalid or missing request fields |
| 401 | `UNAUTHORIZED` | Missing or expired JWT token |
| 403 | `FORBIDDEN` | Insufficient permissions for this action |
| 404 | `NOT_FOUND` | Resource does not exist |
| 409 | `CONFLICT` | Resource already exists (duplicate domain, user, etc.) |
| 422 | `AGENT_ERROR` | Agent failed to execute the server operation |
| 429 | `RATE_LIMITED` | Too many requests — retry after `Retry-After` header |
| 500 | `INTERNAL_ERROR` | Unexpected server error |

### Pagination

All list endpoints support pagination via query parameters:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `page` | `1` | Page number (1-indexed) |
| `limit` | `20` | Items per page (max 100) |
| `sort` | varies | Sort field (e.g., `created_at`, `name`) |
| `order` | `desc` | Sort direction: `asc` or `desc` |
| `search` | — | Full-text search across relevant fields |

**Paginated response envelope:**

```json
{
  "success": true,
  "data": [ ... ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 87,
    "total_pages": 5
  }
}
```

### Rate Limiting

| Panel | Path Prefix | Limit | Window |
|-------|-------------|-------|--------|
| Vendor WHM | `/api/v1/whm/*` | 200 requests | Per minute |
| Client cPanel | `/api/v1/cpanel/*` | 100 requests | Per minute |

Rate limit headers are included in every response:

```
X-RateLimit-Limit: 200
X-RateLimit-Remaining: 194
X-RateLimit-Reset: 1703001260
```

---

## 1. Authentication & User Management

### Login

| Field | Details |
|-------|---------|
| Endpoint | `POST /api/v1/auth/login` |
| Rate Limit | 10 attempts per 15 minutes per IP |
| Payload | `{ "email": "...", "password": "...", "totp_code": "..." }` |

**Response:**

```json
{
  "success": true,
  "data": {
    "access_token": "eyJhbGciOiJIUzI1NiIs...",
    "refresh_token": "dGhpcyBpcyBhIHJlZnJl...",
    "expires_in": 900,
    "token_type": "Bearer",
    "user": {
      "id": "65a1b2c3d4e5f6a7b8c9d0e1",
      "email": "admin@example.com",
      "name": "Admin User",
      "role": "vendor_owner",
      "permissions": ["server.manage", "domain.create", "..."],
      "two_factor_enabled": true,
      "last_login": "2025-12-15T10:30:00Z"
    }
  }
}
```

- Only active users (`is_active: true`) can log in.
- JWT embeds user ID, email, role, and full permission list.
- `totp_code` is required only when 2FA is enabled for the user.
- After 5 failed login attempts, the account is locked for 15 minutes.

### Token Refresh

| Field | Details |
|-------|---------|
| Endpoint | `POST /api/v1/auth/refresh` |
| Payload | `{ "refresh_token": "..." }` |
| Response | New access token (15 min) + new refresh token (7 days) |

- Refresh tokens are single-use — each refresh issues a new pair.
- Old refresh tokens are invalidated immediately (rotation).

### Logout

| Field | Details |
|-------|---------|
| Endpoint | `POST /api/v1/auth/logout` |
| Payload | `{ "refresh_token": "..." }` |
| Effect | Invalidates the refresh token; access token expires naturally |

### Password Reset

| Action | Endpoint | Details |
|--------|----------|---------|
| Request reset | `POST /api/v1/auth/forgot-password` | Sends a reset link to the user's email |
| Confirm reset | `POST /api/v1/auth/reset-password` | Validates token and sets new password |

**Request reset payload:**

```json
{
  "email": "user@example.com"
}
```

**Confirm reset payload:**

```json
{
  "token": "reset-token-from-email",
  "new_password": "newSecurePass456"
}
```

- Reset tokens expire after 1 hour.
- Password requirements: minimum 8 characters, at least 1 uppercase, 1 lowercase, 1 digit.

### Two-Factor Authentication (2FA)

| Action | Endpoint | Permission |
|--------|----------|------------|
| Enable 2FA | `POST /api/v1/auth/2fa/enable` | Self (authenticated) |
| Verify & activate | `POST /api/v1/auth/2fa/verify` | Self (authenticated) |
| Disable 2FA | `POST /api/v1/auth/2fa/disable` | Self (authenticated) |
| Generate recovery codes | `POST /api/v1/auth/2fa/recovery-codes` | Self (authenticated) |

**Enable 2FA response:**

```json
{
  "success": true,
  "data": {
    "secret": "JBSWY3DPEHPK3PXP",
    "qr_code_url": "otpauth://totp/ServerPanel:admin@example.com?secret=JBSWY3DPEHPK3PXP&issuer=ServerPanel",
    "qr_code_base64": "data:image/png;base64,..."
  }
}
```

- Uses TOTP (Time-based One-Time Password) compatible with Google Authenticator, Authy, etc.
- 8 recovery codes are generated — each can be used once as a backup for login.
- Vendor owners can enforce 2FA for all users via server configuration.

### User Management

| Action | Endpoint | Permission |
|--------|----------|------------|
| List users | `GET /api/v1/users/` | `user.view` |
| Get user | `GET /api/v1/users/:id` | `user.view` |
| Create user | `POST /api/v1/users/` | `user.create` |
| Update user | `PUT /api/v1/users/:id` | `user.manage` |
| Delete user | `DELETE /api/v1/users/:id` | `user.manage` |
| Suspend user | `PATCH /api/v1/users/:id/suspend` | `user.manage` |
| Unsuspend user | `PATCH /api/v1/users/:id/unsuspend` | `user.manage` |

**Create user payload:**

```json
{
  "email": "dev@example.com",
  "password": "securePass123",
  "name": "John Developer",
  "role": "developer",
  "permissions": [],
  "domains": ["example.com"],
  "notify": true
}
```

**Update user payload (partial updates allowed):**

```json
{
  "name": "John Senior Developer",
  "role": "vendor_admin",
  "permissions": ["domain.view", "app.deploy", "app.manage"],
  "domains": ["example.com", "another.com"],
  "is_active": true
}
```

- If `permissions` is empty on create, the system auto-assigns default permissions based on the selected role.
- Available roles: `vendor_owner`, `vendor_admin`, `developer`, `support`, `customer`.
- The `domains` array restricts which domains a user (especially customers) can access.
- `notify: true` sends a welcome email with login credentials.
- Suspended users cannot log in but their data is preserved.
- Deleting a user does **not** delete their associated domains or data.

**Query parameters for list:**

| Parameter | Example | Description |
|-----------|---------|-------------|
| `role` | `?role=customer` | Filter by role |
| `is_active` | `?is_active=true` | Filter by active status |
| `search` | `?search=john` | Search name or email |

---

## 2. Domain Management

Provision and manage website domains with full Linux user isolation.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List domains | `GET /api/v1/domains/` | `domain.view` |
| Get domain | `GET /api/v1/domains/:id` | `domain.view` |
| Create domain | `POST /api/v1/domains/` | `domain.create` |
| Update domain | `PUT /api/v1/domains/:id` | `domain.manage` |
| Delete domain | `DELETE /api/v1/domains/:id` | `domain.delete` |
| Suspend domain | `PATCH /api/v1/domains/:id/suspend` | `domain.manage` |
| Unsuspend domain | `PATCH /api/v1/domains/:id/unsuspend` | `domain.manage` |
| Switch PHP version | `PATCH /api/v1/domains/:id/php` | `domain.manage` |
| Get domain stats | `GET /api/v1/domains/:id/stats` | `domain.view` |

### Create Domain

```json
{
  "domain": "example.com",
  "user": "exampleuser",
  "password": "userPass123",
  "php_version": "8.2",
  "disk_quota_mb": 5120,
  "bandwidth_limit_gb": 100,
  "max_databases": 10,
  "max_email_accounts": 25,
  "max_subdomains": 20,
  "max_apps": 5
}
```

**What happens on the server when a domain is created:**

1. A dedicated **Linux system user** is created (`useradd -m -s /bin/bash`).
2. User password is set via `chpasswd`.
3. Directory structure is created:
   ```
   /home/exampleuser/
   ├── public_html/     <- document root (default index.html placed here)
   ├── logs/
   ├── tmp/
   ├── apps/
   ├── backups/
   └── ssl/
   ```
4. A **per-user PHP-FPM pool** is configured at `/etc/php/8.2/fpm/pool.d/exampleuser.conf`:
   - `open_basedir` restricts access to `/home/exampleuser:/tmp:/usr/share/php`
   - Dangerous functions disabled: `exec`, `passthru`, `shell_exec`, `system`, `proc_open`, `popen`
   - Upload limits: 100 MB file / 100 MB POST / 256 MB memory / 300s max execution
   - Dynamic process management (min 1, max 3 spare servers, 500 max requests)
5. An **Nginx virtual host** is created with PHP-FPM or reverse proxy support.
6. Nginx is reloaded.
7. **Disk quota** is applied via `setquota` if `disk_quota_mb > 0`.
8. Resource limits are stored in the database for enforcement.

### Switch PHP Version

```json
{
  "php_version": "8.3"
}
```

- Removes the old PHP-FPM pool config and creates a new one for the target version.
- Supported versions: `7.4`, `8.0`, `8.1`, `8.2`, `8.3`.
- Nginx vhost is updated to point to the new PHP-FPM socket.
- Both old and new PHP-FPM services are reloaded.

### Subdomain Management

| Action | Endpoint | Permission |
|--------|----------|------------|
| List subdomains | `GET /api/v1/domains/:id/subdomains` | `domain.view` |
| Create subdomain | `POST /api/v1/domains/:id/subdomains` | `domain.manage` |
| Delete subdomain | `DELETE /api/v1/domains/:id/subdomains/:subId` | `domain.manage` |

**Create subdomain payload:**

```json
{
  "subdomain": "blog",
  "document_root": "/home/exampleuser/public_html/blog",
  "php_version": "8.2"
}
```

- Creates `blog.example.com` as a separate Nginx server block.
- Can point to a custom document root or a reverse proxy.
- Each subdomain gets its own access/error log files.

### Domain Aliases & Redirects

| Action | Endpoint | Permission |
|--------|----------|------------|
| List aliases | `GET /api/v1/domains/:id/aliases` | `domain.view` |
| Create alias | `POST /api/v1/domains/:id/aliases` | `domain.manage` |
| Delete alias | `DELETE /api/v1/domains/:id/aliases/:aliasId` | `domain.manage` |
| List redirects | `GET /api/v1/domains/:id/redirects` | `domain.view` |
| Create redirect | `POST /api/v1/domains/:id/redirects` | `domain.manage` |
| Delete redirect | `DELETE /api/v1/domains/:id/redirects/:redirectId` | `domain.manage` |

**Create alias payload:**

```json
{
  "alias_domain": "example.org"
}
```

- Adds `example.org` as a `server_name` alias in the Nginx vhost — serves the same content as the primary domain.
- DNS for the alias domain must be configured separately.

**Create redirect payload:**

```json
{
  "source_path": "/old-page",
  "target_url": "https://example.com/new-page",
  "type": "301",
  "match_type": "exact"
}
```

| Redirect Type | Description |
|--------------|-------------|
| `301` | Permanent redirect (SEO-friendly) |
| `302` | Temporary redirect |

| Match Type | Description |
|-----------|-------------|
| `exact` | Matches the exact path only |
| `prefix` | Matches the path and all sub-paths |
| `regex` | Regular expression match |

### Suspend / Unsuspend Domain

- **Suspend** replaces the Nginx vhost with a "suspended" page and stops the PHP-FPM pool.
- **Unsuspend** restores the original vhost and restarts PHP-FPM.
- All apps under the domain are also stopped/started accordingly.
- Databases and email remain intact but inaccessible while suspended.

### Domain Stats Response

```json
{
  "success": true,
  "data": {
    "domain": "example.com",
    "disk_used_mb": 1250,
    "disk_quota_mb": 5120,
    "bandwidth_used_gb": 45.2,
    "bandwidth_limit_gb": 100,
    "email_accounts": 8,
    "max_email_accounts": 25,
    "databases": 3,
    "max_databases": 10,
    "subdomains": 4,
    "max_subdomains": 20,
    "apps": 2,
    "max_apps": 5,
    "php_version": "8.2",
    "ssl_active": true,
    "ssl_expires": "2025-03-15T00:00:00Z",
    "status": "active",
    "created_at": "2024-06-01T12:00:00Z"
  }
}
```

### Delete Domain

Removes everything:
- Nginx vhost (from `sites-enabled/` and `sites-available/`)
- All subdomain vhosts
- PHP-FPM pool configs (for all PHP versions: 7.4, 8.0, 8.1, 8.2, 8.3)
- Linux user and home directory (`userdel -r`)
- Associated cron jobs
- Associated SSL certificates

**Warning:** This operation is irreversible. A confirmation flag `"confirm": true` is required in the request body.

---

## 3. Application Deployment

Deploy and manage web applications with multiple language and deployment options.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List apps | `GET /api/v1/apps/` | `app.view` |
| Get app | `GET /api/v1/apps/:name` | `app.view` |
| Deploy app | `POST /api/v1/apps/deploy` | `app.deploy` |
| Redeploy app | `POST /api/v1/apps/:name/redeploy` | `app.deploy` |
| Start / Stop / Restart | `POST /api/v1/apps/:name/:action` | `app.manage` |
| Delete app | `DELETE /api/v1/apps/:name` | `app.manage` |
| Get app logs | `GET /api/v1/apps/:name/logs` | `app.view` |
| Update env vars | `PUT /api/v1/apps/:name/env` | `app.manage` |
| Rollback | `POST /api/v1/apps/:name/rollback` | `app.deploy` |

### Supported Application Types

| Type | Runtime | Process Manager |
|------|---------|-----------------|
| **Go** | Compiled binary | systemd service |
| **Node.js** | `npm start` or `node index.js` | systemd service |
| **Python** | Virtual env + pip / poetry | systemd service |
| **Ruby** | Bundler + Puma/Unicorn | systemd service |
| **Rust** | Compiled binary | systemd service |
| **Java** | JRE + JAR/WAR | systemd service |
| **Static** | HTML/CSS/JS | Nginx (direct serve) |
| **Docker** | Docker container | `docker run --restart unless-stopped` |

### Supported Deploy Methods

| Method | Description |
|--------|-------------|
| `git` | Clone from Git repository (supports SSH keys and tokens) |
| `zip` | Upload zip archive |
| `binary` | Pre-built binary |
| `docker` | Pull Docker image (supports private registries) |

### Deploy Payload Example (Node.js via Git)

```json
{
  "name": "my-node-app",
  "domain": "example.com",
  "app_type": "node",
  "deploy_method": "git",
  "user": "exampleuser",
  "port": 3000,
  "git_url": "https://github.com/user/repo.git",
  "git_branch": "main",
  "git_token": "",
  "build_cmd": "npm run build",
  "start_cmd": "npm start",
  "health_check_path": "/health",
  "min_instances": 1,
  "max_instances": 1,
  "env_vars": {
    "NODE_ENV": "production",
    "DB_URL": "mongodb://localhost:27017/mydb"
  }
}
```

### Deploy Payload Example (Docker)

```json
{
  "name": "my-docker-app",
  "domain": "example.com",
  "app_type": "docker",
  "deploy_method": "docker",
  "user": "exampleuser",
  "port": 8080,
  "docker_image": "registry.example.com/myapp:latest",
  "docker_registry_user": "",
  "docker_registry_pass": "",
  "docker_volumes": [
    "/home/exampleuser/data:/app/data"
  ],
  "docker_network": "bridge",
  "env_vars": {
    "APP_ENV": "production"
  }
}
```

### What Happens on Deploy

1. Source code is fetched (git clone / docker pull / extract zip).
2. Dependencies are installed (`npm install`, `pip install -r requirements.txt`, `go build`, `bundle install`, `cargo build --release`, etc.).
3. Build command is executed if specified.
4. A **systemd service** is created as `sp-app-{name}` with:
   - `Restart=always`, `RestartSec=5`
   - Environment variables loaded from `.env` file
   - Logs directed to journald
   - `WorkingDirectory` set to the app directory
   - `User` and `Group` set to the domain's Linux user
5. An **Nginx reverse proxy** is configured:
   - WebSocket upgrade support
   - `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Real-IP` headers
   - 86400s read timeout
   - Optional custom Nginx directives
6. **Health check** is performed if `health_check_path` is set — waits up to 30 seconds for a 200 response.
7. The service is started and enabled on boot.
8. A deployment record is saved (for rollback support).

### App Actions

| Action | What It Does |
|--------|--------------|
| `start` | `systemctl start sp-app-{name}` |
| `stop` | `systemctl stop sp-app-{name}` |
| `restart` | `systemctl restart sp-app-{name}` |

### Redeploy

Re-fetches the latest code from the same source and repeats the build/deploy process. For git deploys, runs `git pull` instead of a fresh clone.

### Rollback

```json
{
  "deployment_id": "65a1b2c3d4e5f6a7b8c9d0e1"
}
```

- Each deploy creates a snapshot (previous release is kept).
- Rollback restores the previous deployment's code and restarts the service.
- Up to 5 previous deployments are retained per app.

### Update Environment Variables

```json
{
  "env_vars": {
    "NODE_ENV": "production",
    "DB_URL": "mongodb://localhost:27017/mydb",
    "NEW_VAR": "new_value"
  },
  "restart": true
}
```

- Completely replaces the `.env` file with the provided variables.
- If `restart: true`, the app is automatically restarted to pick up changes.

### Get App Response

```json
{
  "success": true,
  "data": {
    "name": "my-node-app",
    "domain": "example.com",
    "app_type": "node",
    "deploy_method": "git",
    "user": "exampleuser",
    "port": 3000,
    "status": "running",
    "pid": 12345,
    "memory_mb": 128,
    "cpu_percent": 2.5,
    "uptime": "3d 14h 22m",
    "git_url": "https://github.com/user/repo.git",
    "git_branch": "main",
    "last_deployed": "2025-01-10T14:30:00Z",
    "deployments_count": 7,
    "created_at": "2024-12-01T10:00:00Z"
  }
}
```

---

## 4. Database Management

Create and manage databases across three engines.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List databases | `GET /api/v1/databases/` | `database.view` |
| Get database | `GET /api/v1/databases/:id` | `database.view` |
| Create database | `POST /api/v1/databases/` | `database.create` |
| Delete database | `DELETE /api/v1/databases/:id` | `database.manage` |
| List database users | `GET /api/v1/databases/:id/users` | `database.view` |
| Create database user | `POST /api/v1/databases/:id/users` | `database.manage` |
| Delete database user | `DELETE /api/v1/databases/:id/users/:userId` | `database.manage` |
| Update user privileges | `PUT /api/v1/databases/:id/users/:userId/privileges` | `database.manage` |
| Enable remote access | `POST /api/v1/databases/:id/remote-access` | `database.manage` |

### Create Sequence

When a database is created, the agent executes:

```
use {db_name}
db.createUser({
  user: "{username}",
  pwd: "{password}",
  roles: [{ role: "readWrite", db: "{db_name}" }]
})
```

### Create Database Payload

```json
{
  "db_name": "myapp_db",
  "username": "myapp_user",
  "password": "dbPass123",
  "domain": "example.com"
}
```

**Response:**

```json
{
  "success": true,
  "data": {
    "id": "65a1b2c3d4e5f6a7b8c9d0e1",
    "db_name": "myapp_db",
    "username": "myapp_user",
    "host": "localhost",
    "port": 27017,
    "connection_string": "mongodb://myapp_user:***@localhost:27017/myapp_db",
    "domain": "example.com",
    "size_mb": 0,
    "created_at": "2025-01-10T14:30:00Z"
  }
}
```

### Create Additional Database User

```json
{
  "username": "readonly_user",
  "password": "readonlyPass123",
  "role": "read"
}
```

**Available MongoDB roles:**

| Role | Description |
|------|-------------|
| `readWrite` | Read and write to the database (default) |
| `read` | Read-only access |
| `dbAdmin` | Schema management, indexing, statistics |
| `dbOwner` | Full control — combines `readWrite`, `dbAdmin`, `userAdmin` |
| `userAdmin` | Manage users and roles for the database |

### Enable Remote Access

```json
{
  "username": "myapp_user",
  "allowed_ip": "203.0.113.50"
}
```

- Updates `bindIp` in `/etc/mongod.conf` to include the specified IP.
- Automatically opens port 27017 in UFW for the specified IP only.
- Restarts `mongod` service to apply the binding changes.

### Delete Database

Removes the database, all associated users, and revokes remote access rules.

**Warning:** This operation is irreversible. A confirmation flag `"confirm": true` is required.

---

## 5. Email Server Management

Full-featured email hosting with SMTP, IMAP, POP3, spam filtering, and webmail.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List mailboxes | `GET /api/v1/email/` | `email.view` |
| Get mailbox | `GET /api/v1/email/:id` | `email.view` |
| Create mailbox | `POST /api/v1/email/` | `email.create` |
| Update mailbox | `PUT /api/v1/email/:id` | `email.manage` |
| Delete mailbox | `DELETE /api/v1/email/:id` | `email.manage` |
| List forwarders | `GET /api/v1/email/forwarders` | `email.view` |
| Create forwarder | `POST /api/v1/email/forwarders` | `email.manage` |
| Delete forwarder | `DELETE /api/v1/email/forwarders/:id` | `email.manage` |
| List autoresponders | `GET /api/v1/email/autoresponders` | `email.view` |
| Create autoresponder | `POST /api/v1/email/autoresponders` | `email.manage` |
| Update autoresponder | `PUT /api/v1/email/autoresponders/:id` | `email.manage` |
| Delete autoresponder | `DELETE /api/v1/email/autoresponders/:id` | `email.manage` |
| Set catch-all | `POST /api/v1/email/catch-all` | `email.manage` |
| Get spam settings | `GET /api/v1/email/spam-settings/:domain` | `email.view` |
| Update spam settings | `PUT /api/v1/email/spam-settings/:domain` | `email.manage` |
| Setup DKIM | `POST /api/v1/email/dkim/:domain` | `email.manage` |
| Get DKIM status | `GET /api/v1/email/dkim/:domain` | `email.view` |

### Mail Stack (installed via Agent)

| Component | Purpose |
|-----------|---------|
| **Postfix** | SMTP server — handles sending/receiving mail |
| **Dovecot** | IMAP/POP3 server — mailbox access for email clients |
| **SpamAssassin** | Spam filtering with Bayesian learning |
| **OpenDKIM** | DKIM email signing for deliverability |
| **OpenDMARC** | DMARC policy enforcement |
| **Roundcube** | Web-based email client (webmail) |
| **ClamAV** | Email virus/malware scanning |

### Mail Server Configuration

- **TLS encryption** on SMTP (STARTTLS on 587, implicit TLS on 465)
- **SASL authentication** via Dovecot
- **Virtual domain support** — host multiple domains on one server
- **Maildir format** — stored at `/var/mail/vhosts/{domain}/{user}/`
- **Password hashing** — SHA512-CRYPT via `doveadm`
- **Message size limit** — 50 MB
- **Concurrent connections** — max 20 per IP
- **Greylisting** — optional, delays first-time senders by 5 minutes

### Create Mailbox Payload

```json
{
  "email": "user@example.com",
  "password": "mailPass123",
  "domain": "example.com",
  "quota_mb": 1024,
  "send_limit_per_hour": 100
}
```

**What happens on the server:**

1. Maildir created at `/var/mail/vhosts/example.com/user/`
2. Password hashed with `doveadm pw -s SHA512-CRYPT`
3. Entry added to `/etc/dovecot/users`
4. Entry added to `/etc/postfix/virtual_mailbox`
5. Domain registered in `/etc/postfix/virtual_domains`
6. `postmap` run, Postfix + Dovecot reloaded
7. Quota applied via Dovecot quota plugin

### Update Mailbox

```json
{
  "password": "newMailPass456",
  "quota_mb": 2048,
  "send_limit_per_hour": 200
}
```

### Email Forwarders

```json
{
  "source": "info@example.com",
  "destinations": [
    "admin@example.com",
    "external@gmail.com"
  ],
  "keep_copy": true
}
```

- `keep_copy: true` — delivers to the original mailbox AND forwards.
- `keep_copy: false` — forwards only, no local delivery.
- Multiple destinations supported per forwarder.

### Autoresponders

```json
{
  "email": "support@example.com",
  "subject": "We received your message",
  "body": "Thank you for contacting us. We'll respond within 24 hours.",
  "start_date": "2025-12-20T00:00:00Z",
  "end_date": "2025-12-31T23:59:59Z",
  "interval_hours": 24
}
```

- `interval_hours` — minimum time between auto-replies to the same sender (prevents loops).
- Supports date ranges for vacation-style responses.
- Implemented via Sieve scripts in Dovecot.

### Catch-All

```json
{
  "domain": "example.com",
  "action": "forward",
  "forward_to": "admin@example.com"
}
```

| Action | Description |
|--------|-------------|
| `forward` | Forward unmatched emails to a specific mailbox |
| `reject` | Bounce unmatched emails with a 550 error |
| `discard` | Silently discard unmatched emails |

### Spam Settings

```json
{
  "domain": "example.com",
  "spam_threshold": 5.0,
  "spam_action": "move_to_spam",
  "whitelist": ["trusted-sender@example.org"],
  "blacklist": ["spammer@evil.com"],
  "clamav_enabled": true
}
```

| Spam Action | Description |
|------------|-------------|
| `move_to_spam` | Deliver to the user's Junk/Spam folder |
| `tag_subject` | Prepend `[SPAM]` to the subject line |
| `reject` | Reject the email at SMTP level |
| `discard` | Silently drop the email |

### DKIM Setup

Vendor can set up DKIM signing per domain:
- Generates 2048-bit RSA keys with `opendkim-genkey`
- Configures `/etc/opendkim/KeyTable` and `/etc/opendkim/SigningTable`
- Outputs the DNS TXT record to add for DKIM verification

**DKIM status response:**

```json
{
  "success": true,
  "data": {
    "domain": "example.com",
    "enabled": true,
    "selector": "default",
    "dns_record": "default._domainkey.example.com",
    "dns_value": "v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAA...",
    "verified": true,
    "dmarc_record": "v=DMARC1; p=quarantine; rua=mailto:admin@example.com"
  }
}
```

---

## 6. DNS Management

Create and manage DNS zones powered by **PowerDNS**.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List zones | `GET /api/v1/dns/zones` | `dns.view` |
| Get zone | `GET /api/v1/dns/zones/:domain` | `dns.view` |
| Create zone | `POST /api/v1/dns/zones` | `dns.manage` |
| Delete zone | `DELETE /api/v1/dns/zones/:domain` | `dns.manage` |
| List records | `GET /api/v1/dns/zones/:domain/records` | `dns.view` |
| Add record | `POST /api/v1/dns/zones/:domain/records` | `dns.manage` |
| Update record | `PUT /api/v1/dns/zones/:domain/records/:id` | `dns.manage` |
| Delete record | `DELETE /api/v1/dns/zones/:domain/records/:id` | `dns.manage` |
| Import zone | `POST /api/v1/dns/zones/import` | `dns.manage` |
| Export zone | `GET /api/v1/dns/zones/:domain/export` | `dns.view` |
| Apply template | `POST /api/v1/dns/zones/:domain/template` | `dns.manage` |

### Supported Record Types

`A`, `AAAA`, `CNAME`, `MX`, `TXT`, `NS`, `SRV`, `CAA`, `SOA`, `PTR`, `ALIAS`, `DNAME`

### Create Zone Payload

```json
{
  "domain": "example.com",
  "server_ip": "203.0.113.10",
  "admin_email": "admin@example.com",
  "nameservers": ["ns1.example.com", "ns2.example.com"],
  "template": "standard",
  "records": [
    { "type": "A", "name": "@", "value": "203.0.113.10", "ttl": 3600 },
    { "type": "MX", "name": "@", "value": "mail.example.com", "ttl": 3600, "priority": 10 },
    { "type": "TXT", "name": "@", "value": "v=spf1 ip4:203.0.113.10 -all", "ttl": 3600 }
  ]
}
```

**Zone features:**
- Auto-generates SOA and NS records
- Auto-creates `ns1.{domain}` and `ns2.{domain}` if nameservers not specified
- Serial numbers auto-managed (format: `YYYYMMDD01`)
- Zone changes trigger `pdns_control reload` automatically
- DNSSEC can be enabled per zone

### DNS Templates

| Template | Records Created |
|----------|----------------|
| `standard` | A, AAAA (if IPv6), www CNAME, MX, SPF TXT |
| `email` | All of `standard` + DKIM TXT, DMARC TXT, autodiscover CNAME |
| `wordpress` | All of `standard` + www A record (instead of CNAME for CDN compat) |
| `blank` | SOA and NS only |

**Apply template payload:**

```json
{
  "template": "email",
  "server_ip": "203.0.113.10",
  "overwrite_existing": false
}
```

### Add Record Payload

```json
{
  "type": "A",
  "name": "www",
  "value": "203.0.113.10",
  "ttl": 3600
}
```

**SRV record example:**

```json
{
  "type": "SRV",
  "name": "_sip._tcp",
  "value": "sip.example.com",
  "ttl": 3600,
  "priority": 10,
  "weight": 60,
  "port": 5060
}
```

**CAA record example:**

```json
{
  "type": "CAA",
  "name": "@",
  "value": "letsencrypt.org",
  "ttl": 3600,
  "caa_flag": 0,
  "caa_tag": "issue"
}
```

### Import Zone (BIND format)

```json
{
  "domain": "example.com",
  "zone_content": "$ORIGIN example.com.\n$TTL 3600\n@ IN SOA ns1.example.com. admin.example.com. ...",
  "overwrite": false
}
```

### Export Zone

`GET /api/v1/dns/zones/example.com/export?format=bind`

Returns the zone in standard BIND format for migration to other DNS providers.

### DNSSEC

| Action | Endpoint | Permission |
|--------|----------|------------|
| Enable DNSSEC | `POST /api/v1/dns/zones/:domain/dnssec/enable` | `dns.manage` |
| Disable DNSSEC | `POST /api/v1/dns/zones/:domain/dnssec/disable` | `dns.manage` |
| Get DS records | `GET /api/v1/dns/zones/:domain/dnssec` | `dns.view` |

- Enables DNSSEC signing via PowerDNS's built-in signing.
- Returns DS records that need to be configured at the domain registrar.
- Key rollover is handled automatically.

---

## 7. SSL / TLS Certificate Management

Issue free Let's Encrypt certificates, install custom ones, or manage certificate lifecycle.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List certificates | `GET /api/v1/ssl/` | `ssl.manage` |
| Get certificate info | `GET /api/v1/ssl/:domain` | `ssl.manage` |
| Issue Let's Encrypt | `POST /api/v1/ssl/letsencrypt` | `ssl.manage` |
| Upload custom cert | `POST /api/v1/ssl/custom` | `ssl.manage` |
| Force renew | `POST /api/v1/ssl/:domain/renew` | `ssl.manage` |
| Revoke certificate | `POST /api/v1/ssl/:domain/revoke` | `ssl.manage` |
| Delete certificate | `DELETE /api/v1/ssl/:domain` | `ssl.manage` |

### Let's Encrypt (Free SSL)

```json
{
  "domain": "example.com",
  "email": "admin@example.com",
  "wildcard": false,
  "additional_domains": ["www.example.com", "blog.example.com"]
}
```

- **Standard domains:** Uses `certbot --nginx` mode (automatic HTTP validation)
- **Wildcard certificates:** Uses `--manual --preferred-challenges dns` with PowerDNS API integration for automatic DNS validation
- **Multi-domain (SAN):** Supports multiple domains in a single certificate via `-d` flags
- **Auto-renewal:** Cron job at 3 AM daily: `certbot renew --quiet --post-hook "systemctl reload nginx"`

### Custom Certificate Upload

```json
{
  "domain": "example.com",
  "certificate": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----",
  "private_key": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----",
  "ca_bundle": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----"
}
```

- Certificate + private key + optional chain stored at `/etc/serverpanel/ssl/{domain}/`
- Validates that the certificate matches the private key before saving.
- Validates that the certificate is not expired.
- Nginx vhost updated to SSL mode automatically.

### Certificate Info Response

```json
{
  "success": true,
  "data": {
    "domain": "example.com",
    "issuer": "Let's Encrypt Authority X3",
    "type": "letsencrypt",
    "domains": ["example.com", "www.example.com"],
    "issued_at": "2025-01-01T00:00:00Z",
    "expires_at": "2025-03-31T00:00:00Z",
    "days_remaining": 65,
    "auto_renew": true,
    "wildcard": false,
    "key_type": "RSA 2048",
    "serial_number": "03:A1:B2:C3:D4:E5:F6",
    "fingerprint_sha256": "AB:CD:EF:..."
  }
}
```

### SSL Vhost Configuration

Once SSL is active, Nginx is configured with:
- TLSv1.2 and TLSv1.3 only
- Modern cipher suite (ECDHE+AESGCM:ECDHE+CHACHA20)
- Automatic HTTP -> HTTPS redirect (301)
- HSTS header (`max-age=31536000; includeSubDomains`)
- OCSP stapling enabled
- SSL session cache (10 MB, 10 min timeout)

---

## 8. Backup & Restore

Comprehensive backup system with local and S3 storage options, plus scheduled backups.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List backups | `GET /api/v1/backups/` | `backup.view` |
| Get backup info | `GET /api/v1/backups/:id` | `backup.view` |
| Create backup | `POST /api/v1/backups/` | `backup.create` |
| Restore backup | `POST /api/v1/backups/restore` | `backup.restore` |
| Delete backup | `DELETE /api/v1/backups/:id` | `backup.create` |
| Download backup | `GET /api/v1/backups/:id/download` | `backup.view` |
| List schedules | `GET /api/v1/backups/schedules` | `backup.view` |
| Create schedule | `POST /api/v1/backups/schedules` | `backup.create` |
| Update schedule | `PUT /api/v1/backups/schedules/:id` | `backup.create` |
| Delete schedule | `DELETE /api/v1/backups/schedules/:id` | `backup.create` |

### Backup Types

| Type | What's Backed Up | Method |
|------|-----------------|--------|
| `files` | `/home/{user}/` | `tar -czf` |
| `database` | Single MongoDB database | `mongodump` |
| `email` | `/var/mail/vhosts/{domain}/` | `tar -czf` |
| `full` | Files + email + databases combined | Combined tar archive |
| `config` | Nginx, PHP-FPM, DNS configs for the domain | `tar -czf` |

### Storage Options

| Storage | Description |
|---------|-------------|
| `local` | Stored at `/var/backups/serverpanel/{user}/` |
| `s3` | Uploaded to AWS S3 or S3-compatible storage (DigitalOcean Spaces, MinIO, Backblaze B2, etc.) |

### Create Backup Payload

```json
{
  "type": "full",
  "user": "exampleuser",
  "domain": "example.com",
  "storage": "s3",
  "s3_bucket": "my-backups",
  "s3_region": "us-east-1",
  "s3_access_key": "...",
  "s3_secret_key": "...",
  "s3_endpoint": "",
  "compression": "gzip",
  "encryption_password": ""
}
```

- `compression`: `gzip` (default) or `zstd` (faster, better compression).
- `encryption_password`: if set, the backup is encrypted with AES-256-CBC before storage.

### Backup Response

```json
{
  "success": true,
  "data": {
    "id": "65a1b2c3d4e5f6a7b8c9d0e1",
    "type": "full",
    "domain": "example.com",
    "user": "exampleuser",
    "storage": "s3",
    "status": "completed",
    "size_mb": 256,
    "file_count": 1247,
    "databases_included": ["myapp_db", "wp_example"],
    "path": "s3://my-backups/exampleuser/full_2025-01-10_143000.tar.gz",
    "encrypted": false,
    "created_at": "2025-01-10T14:30:00Z",
    "completed_at": "2025-01-10T14:32:15Z"
  }
}
```

### Scheduled Backups

```json
{
  "domain": "example.com",
  "user": "exampleuser",
  "type": "full",
  "storage": "s3",
  "schedule": "daily",
  "time": "03:00",
  "timezone": "UTC",
  "retention_count": 7,
  "s3_bucket": "my-backups",
  "s3_region": "us-east-1",
  "s3_access_key": "...",
  "s3_secret_key": "...",
  "notify_email": "admin@example.com"
}
```

| Schedule | Description |
|----------|-------------|
| `hourly` | Every hour |
| `daily` | Once per day at the specified time |
| `weekly` | Once per week (on Sunday at the specified time) |
| `monthly` | First day of each month |

- `retention_count` — number of backups to keep. Older backups are automatically deleted.
- `notify_email` — receives success/failure notifications after each scheduled backup.
- Implemented via system cron jobs managed by the agent.

### Database Backup Details

| Tool | Output Format |
|------|---------------|
| `mongodump --archive --gzip --db {db_name}` | `.archive.gz` file |

- Uses `mongodump` with `--archive` for single-file output and `--gzip` for compression.
- Supports authentication via `--authenticationDatabase admin`.

### Restore

```json
{
  "backup_id": "65a1b2c3d4e5f6a7b8c9d0e1",
  "restore_type": "full",
  "overwrite": true,
  "encryption_password": ""
}
```

Restores are type-aware:
- **Files:** `tar -xzf` + `chown -R` to restore ownership
- **Database:** `mongorestore --archive --gzip --drop --db {db_name}`
- **Config:** Restores Nginx/PHP-FPM configs and reloads services

---

## 9. WordPress Management

Full WordPress lifecycle management via **WP-CLI**.

> **Note:** WordPress requires a MySQL-compatible database. When WordPress is installed, the agent automatically installs **MariaDB** (MySQL-compatible) if not already present. MariaDB is used exclusively for WordPress and is not exposed as a general-purpose database — MongoDB remains the primary database engine.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List installs | `GET /api/v1/wordpress/` | `wordpress.manage` |
| Get install info | `GET /api/v1/wordpress/:id` | `wordpress.manage` |
| Install WordPress | `POST /api/v1/wordpress/install` | `wordpress.install` |
| Delete WordPress | `DELETE /api/v1/wordpress/:id` | `wordpress.manage` |
| Update WordPress | `POST /api/v1/wordpress/:id/update` | `wordpress.manage` |
| Clone WordPress | `POST /api/v1/wordpress/:id/clone` | `wordpress.install` |
| Backup WordPress | `POST /api/v1/wordpress/:id/backup` | `wordpress.manage` |
| Restore WordPress | `POST /api/v1/wordpress/:id/restore` | `wordpress.manage` |
| List plugins | `GET /api/v1/wordpress/:id/plugins` | `wordpress.manage` |
| Install plugin | `POST /api/v1/wordpress/:id/plugins` | `wordpress.manage` |
| Remove plugin | `DELETE /api/v1/wordpress/:id/plugins/:slug` | `wordpress.manage` |
| List themes | `GET /api/v1/wordpress/:id/themes` | `wordpress.manage` |
| Install theme | `POST /api/v1/wordpress/:id/themes` | `wordpress.manage` |
| Activate theme | `PATCH /api/v1/wordpress/:id/themes/:slug/activate` | `wordpress.manage` |
| Security scan | `POST /api/v1/wordpress/:id/security-scan` | `wordpress.manage` |
| Toggle maintenance | `PATCH /api/v1/wordpress/:id/maintenance` | `wordpress.manage` |
| Search & replace | `POST /api/v1/wordpress/:id/search-replace` | `wordpress.manage` |
| Enable debug mode | `PATCH /api/v1/wordpress/:id/debug` | `wordpress.manage` |

### Install WordPress Payload

```json
{
  "domain": "example.com",
  "user": "exampleuser",
  "path": "/",
  "db_name": "wp_example",
  "db_user": "wp_user",
  "db_pass": "wpDbPass123",
  "db_host": "localhost",
  "site_title": "My Blog",
  "admin_user": "wpadmin",
  "admin_pass": "wpAdminPass123",
  "admin_email": "admin@example.com",
  "locale": "en_US",
  "multisite": false,
  "auto_update": true
}
```

- `auto_update: true` — enables WordPress auto-updates for minor releases.
- `multisite: true` — installs WordPress Multisite (subdirectory mode).
- `locale` — sets the default language for WordPress.

### WordPress Operations (via Agent)

| Operation | Description |
|-----------|-------------|
| **Install** | Downloads WP core, creates `wp-config.php`, runs `wp core install` |
| **Clone** | Copies files + database, runs `wp search-replace` for URL migration |
| **Backup** | Archives WP files + exports database |
| **Restore** | Extracts archive, imports database |
| **Update** | Updates WP core, all plugins, all themes, and database |
| **Plugin Install** | `wp plugin install {name} --activate` |
| **Plugin Remove** | `wp plugin deactivate` + `wp plugin delete` |
| **Theme Install** | `wp theme install {name}` |
| **Theme Activate** | `wp theme activate {name}` |
| **Get Info** | WP version, site URL, list of all plugins with status/version |
| **Security Scan** | Core checksum verification, outdated plugin detection, recently modified PHP files |
| **Search & Replace** | `wp search-replace` — useful for domain migration |
| **Debug Mode** | Toggles `WP_DEBUG`, `WP_DEBUG_LOG`, `WP_DEBUG_DISPLAY` in `wp-config.php` |
| **Maintenance Mode** | Drops a `.maintenance` file to enable maintenance mode |

### Clone WordPress (Staging)

```json
{
  "target_domain": "staging.example.com",
  "target_user": "exampleuser",
  "target_path": "/staging",
  "target_db_name": "wp_staging",
  "target_db_user": "wp_staging_user",
  "target_db_pass": "stagingPass123"
}
```

- Copies all WordPress files to the target location.
- Exports and imports the database.
- Runs `wp search-replace` to update URLs from source to target.
- Configures a separate Nginx vhost for the staging site.
- Adds `noindex` meta to prevent search engine indexing of staging.

### WordPress Info Response

```json
{
  "success": true,
  "data": {
    "id": "65a1b2c3d4e5f6a7b8c9d0e1",
    "domain": "example.com",
    "path": "/",
    "version": "6.7.1",
    "db_name": "wp_example",
    "site_url": "https://example.com",
    "admin_url": "https://example.com/wp-admin/",
    "multisite": false,
    "auto_update": true,
    "debug_mode": false,
    "maintenance_mode": false,
    "plugins": [
      { "name": "akismet", "status": "active", "version": "5.3", "update_available": false },
      { "name": "woocommerce", "status": "active", "version": "8.5.1", "update_available": true }
    ],
    "theme": {
      "name": "twentytwentyfour",
      "version": "1.2",
      "update_available": false
    },
    "disk_usage_mb": 450,
    "created_at": "2024-06-01T12:00:00Z"
  }
}
```

### WordPress Security Scan

The security scan checks:
1. **Core integrity** — `wp core verify-checksums` to detect modified core files
2. **Outdated plugins** — `wp plugin list --update=available` to find unpatched plugins
3. **Outdated themes** — `wp theme list --update=available`
4. **Suspicious files** — Searches for PHP files modified in the last 7 days that are newer than `wp-config.php`
5. **File permissions** — Checks that `wp-config.php` is not world-readable (should be 640 or 600)
6. **Database prefix** — Warns if using default `wp_` prefix
7. **Admin username** — Warns if admin username is `admin`
8. **Debug mode** — Warns if `WP_DEBUG` is enabled on a production site

**Security scan response:**

```json
{
  "success": true,
  "data": {
    "overall_status": "warning",
    "checks": [
      { "name": "core_integrity", "status": "pass", "message": "WordPress core files verified" },
      { "name": "outdated_plugins", "status": "warning", "message": "2 plugins have updates available", "details": ["woocommerce", "yoast-seo"] },
      { "name": "suspicious_files", "status": "pass", "message": "No suspicious PHP files found" },
      { "name": "file_permissions", "status": "pass", "message": "wp-config.php permissions are correct (640)" },
      { "name": "db_prefix", "status": "warning", "message": "Using default wp_ prefix" },
      { "name": "admin_username", "status": "pass", "message": "Admin username is not 'admin'" },
      { "name": "debug_mode", "status": "pass", "message": "Debug mode is disabled" }
    ],
    "scanned_at": "2025-01-10T14:30:00Z"
  }
}
```

---

## 10. Firewall & Security

Manage **UFW** (Uncomplicated Firewall) and **fail2ban** intrusion prevention.

| Action | Endpoint | Permission |
|--------|----------|------------|
| Get firewall status | `GET /api/v1/firewall/status` | `firewall.manage` |
| List rules | `GET /api/v1/firewall/rules` | `firewall.manage` |
| Allow port | `POST /api/v1/firewall/allow` | `firewall.manage` |
| Deny port | `POST /api/v1/firewall/deny` | `firewall.manage` |
| Delete rule | `DELETE /api/v1/firewall/rules/:id` | `firewall.manage` |
| Block IP | `POST /api/v1/firewall/block-ip` | `firewall.manage` |
| Unblock IP | `POST /api/v1/firewall/unblock-ip` | `firewall.manage` |
| List blocked IPs | `GET /api/v1/firewall/blocked-ips` | `firewall.manage` |
| Get fail2ban status | `GET /api/v1/firewall/fail2ban/status` | `firewall.manage` |
| List fail2ban bans | `GET /api/v1/firewall/fail2ban/bans` | `firewall.manage` |
| Unban IP | `POST /api/v1/firewall/fail2ban/unban` | `firewall.manage` |
| Update fail2ban config | `PUT /api/v1/firewall/fail2ban/config` | `firewall.manage` |

### Firewall Rule Payload

```json
{
  "port": "27017",
  "protocol": "tcp",
  "source": "10.0.0.0/8",
  "comment": "MongoDB from private network"
}
```

### Block IP Payload

```json
{
  "ip": "203.0.113.99",
  "reason": "Brute force attack",
  "duration": "permanent"
}
```

| Duration | Description |
|----------|-------------|
| `permanent` | Blocked until manually unblocked |
| `1h`, `6h`, `24h`, `7d`, `30d` | Temporary block — auto-removed after duration |

### Default Firewall Setup

When the firewall is first set up, these ports are opened by default:

| Port | Protocol | Service |
|------|----------|---------|
| 22 | TCP | SSH |
| 80 | TCP | HTTP (redirects to 443) |
| 443 | TCP | HTTPS — Panel (WHM + cPanel on single domain) |
| 8443 | TCP | Agent API (internal) |
| 25 | TCP | SMTP |
| 587 | TCP | SMTP (submission) |
| 465 | TCP | SMTPS |
| 993 | TCP | IMAPS |
| 995 | TCP | POP3S |
| 53 | TCP + UDP | DNS |

**Default policy:** Deny all incoming, allow all outgoing.

### Firewall Status Response

```json
{
  "success": true,
  "data": {
    "enabled": true,
    "default_incoming": "deny",
    "default_outgoing": "allow",
    "rules_count": 15,
    "blocked_ips_count": 3,
    "fail2ban_active": true,
    "fail2ban_total_bans": 47
  }
}
```

### fail2ban Jails

| Jail | Ban Time | Max Retries | Find Time |
|------|----------|-------------|-----------|
| `sshd` | 3600s | 5 | 600s |
| `postfix` | 3600s | 5 | 600s |
| `dovecot` | 3600s | 5 | 600s |
| `nginx-http-auth` | 3600s | 5 | 600s |
| `nginx-botsearch` | 86400s | 3 | 600s |
| `nginx-req-limit` | 600s | 10 | 600s |
| `serverpanel-auth` | 1800s | 5 | 600s |

### Update fail2ban Config

```json
{
  "jail": "sshd",
  "ban_time": 7200,
  "max_retries": 3,
  "find_time": 300
}
```

---

## 11. Software Installation

Install, detect, and manage server software directly from the panel.

| Action | Endpoint | Role Required |
|--------|----------|---------------|
| List installed | `GET /api/v1/software/installed` | `vendor_owner` or `vendor_admin` |
| Install software | `POST /api/v1/software/install` | `vendor_owner` or `vendor_admin` |
| Uninstall software | `POST /api/v1/software/uninstall` | `vendor_owner` |
| Check for updates | `GET /api/v1/software/updates` | `vendor_owner` or `vendor_admin` |
| Update software | `POST /api/v1/software/update` | `vendor_owner` or `vendor_admin` |
| List PHP extensions | `GET /api/v1/software/php/:version/extensions` | `vendor_owner` or `vendor_admin` |
| Install PHP extension | `POST /api/v1/software/php/:version/extensions` | `vendor_owner` or `vendor_admin` |

### Installable Software

| Software | What Gets Installed |
|----------|-------------------|
| **PHP** | `php{ver}-fpm` + 17 extension packages (curl, gd, mbstring, xml, zip, intl, bcmath, soap, imagick, etc.) via ondrej/php PPA |
| **Node.js** | Node.js from NodeSource + **pm2** + **yarn** globally |
| **Python** | `python{ver}` + `venv` + `dev` + pip via deadsnakes PPA |
| **Go** | Official tarball from go.dev extracted to `/usr/local`, PATH configured |
| **Rust** | Installed via rustup, available system-wide |
| **Ruby** | Installed via rbenv, Bundler included |
| **Java** | OpenJDK (`openjdk-{ver}-jdk`) from official repos |
| **MongoDB** | Official apt repo, `mongodb-org`, mongod enabled and started, authentication configured |
| **Memcached** | `memcached`, enabled and started |
| **Nginx** | `nginx` + `sites-available/` and `sites-enabled/` directories |
| **Apache** | `apache2` + modules: rewrite, ssl, proxy, proxy_http, proxy_fcgi, headers |
| **Docker** | Docker Engine + Docker Compose from official repo |
| **Email Server** | Full mail stack: Postfix, Dovecot, SpamAssassin, OpenDKIM, OpenDMARC, ClamAV, Roundcube (see Section 5 for management APIs) |
| **Certbot** | Let's Encrypt client with Nginx plugin |
| **WP-CLI** | WordPress command-line interface |
| **Composer** | PHP dependency manager (installed globally) |

### Supported Versions

| Software | Available Versions |
|----------|-------------------|
| PHP | 7.4, 8.0, 8.1, 8.2, 8.3, 8.4 |
| Node.js | 18 (LTS), 20 (LTS), 22 (Current) |
| Python | 3.9, 3.10, 3.11, 3.12, 3.13 |
| Go | 1.21, 1.22, 1.23 |
| Java | 11, 17 (LTS), 21 (LTS) |
| MongoDB | 7.0, 8.0 |

### Install Payload

```json
{
  "software": "php",
  "version": "8.2"
}
```

### PHP Extension Management

```json
{
  "extension": "imagick"
}
```

**Default PHP extensions installed:**

`bcmath`, `cli`, `common`, `curl`, `fpm`, `gd`, `igbinary`, `imagick`, `intl`, `mbstring`, `mongodb`, `opcache`, `readline`, `soap`, `xml`, `zip`

**Additional extensions available:**

`memcached`, `sqlite3`, `ldap`, `imap`, `gmp`, `bz2`, `xdebug`

### Detection Response

```json
{
  "success": true,
  "data": [
    { "name": "nginx", "version": "1.24.0", "status": "running", "installed": true },
    { "name": "php", "version": "8.2.15", "status": "running", "installed": true },
    { "name": "mongodb", "version": "7.0.14", "status": "running", "installed": true },
    { "name": "node", "version": "20.11.0", "status": "installed", "installed": true },
    { "name": "docker", "version": null, "status": null, "installed": false }
  ]
}
```

The "List Installed" endpoint runs version commands (`nginx -v`, `node --version`, `go version`, etc.) and checks `systemctl` status for services.

### Email Server Installation

Install the complete email server stack in one operation. This sets up all components needed for full email hosting.

| Action | Endpoint | Permission |
|--------|----------|------------|
| Install email server | `POST /api/v1/software/install-email` | `server.manage` |
| Get email server status | `GET /api/v1/software/email-status` | `server.view` |
| Uninstall email server | `POST /api/v1/software/uninstall-email` | `server.manage` |
| Reconfigure email server | `POST /api/v1/software/reconfigure-email` | `server.manage` |

#### Install Email Server

```json
POST /api/v1/software/install-email
{
  "hostname": "mail.example.com",
  "ssl": "letsencrypt",
  "webmail": true,
  "antivirus": true,
  "spam_filter": true,
  "dkim": true,
  "dmarc": true,
  "greylisting": false,
  "max_message_size_mb": 50,
  "max_connections_per_ip": 20
}
```

**What gets installed and configured:**

| Step | Component | Action |
|------|-----------|--------|
| 1 | **Postfix** | Install, configure as virtual mailbox MTA, enable STARTTLS (587) + implicit TLS (465), SASL auth via Dovecot |
| 2 | **Dovecot** | Install, configure IMAP (993) + POP3 (995), Maildir format at `/var/mail/vhosts/`, quota plugin, Sieve filtering |
| 3 | **SpamAssassin** | Install, enable Bayesian learning, integrate with Postfix via milter, auto-update rules via cron |
| 4 | **OpenDKIM** | Install, generate signing keys, integrate with Postfix milter, auto-generate DNS TXT records |
| 5 | **OpenDMARC** | Install, configure policy checking, integrate with Postfix milter |
| 6 | **ClamAV** | Install, enable clamd + freshclam, integrate with Postfix via milter, auto-update virus definitions |
| 7 | **Roundcube** | Install webmail at `/webmail` path, configure Nginx vhost, connect to Dovecot IMAP, custom branding support |
| 8 | **SSL/TLS** | Obtain Let's Encrypt certificate for `mail.example.com`, configure in Postfix + Dovecot + Nginx |
| 9 | **DNS records** | Auto-generate required DNS records (MX, SPF, DKIM, DMARC) — returned in response for user to add |
| 10 | **Firewall** | Open ports: 25, 465, 587 (SMTP), 993 (IMAPS), 995 (POP3S), 80/443 (webmail) |

**Response:**

```json
{
  "success": true,
  "data": {
    "status": "installed",
    "hostname": "mail.example.com",
    "installed_at": "2025-01-15T10:05:00Z",
    "components": {
      "postfix": { "version": "3.6.4", "status": "running" },
      "dovecot": { "version": "2.3.19", "status": "running" },
      "spamassassin": { "version": "4.0.0", "status": "running" },
      "opendkim": { "version": "2.11.0", "status": "running" },
      "opendmarc": { "version": "1.4.2", "status": "running" },
      "clamav": { "version": "1.0.3", "status": "running" },
      "roundcube": { "version": "1.6.5", "status": "running" }
    },
    "ports_opened": [25, 465, 587, 993, 995],
    "webmail_url": "https://mail.example.com/webmail",
    "dns_records_required": [
      { "type": "MX", "name": "example.com", "value": "mail.example.com", "priority": 10 },
      { "type": "A", "name": "mail.example.com", "value": "203.0.113.50" },
      { "type": "TXT", "name": "example.com", "value": "v=spf1 mx a ip4:203.0.113.50 ~all" },
      { "type": "TXT", "name": "default._domainkey.example.com", "value": "v=DKIM1; k=rsa; p=MIGfMA0G..." },
      { "type": "TXT", "name": "_dmarc.example.com", "value": "v=DMARC1; p=quarantine; rua=mailto:dmarc@example.com" }
    ]
  }
}
```

#### Email Server Status

```json
GET /api/v1/software/email-status
```

```json
{
  "success": true,
  "data": {
    "installed": true,
    "hostname": "mail.example.com",
    "components": {
      "postfix": { "version": "3.6.4", "status": "running", "uptime": "15d 3h" },
      "dovecot": { "version": "2.3.19", "status": "running", "uptime": "15d 3h" },
      "spamassassin": { "version": "4.0.0", "status": "running", "uptime": "15d 3h" },
      "opendkim": { "version": "2.11.0", "status": "running", "uptime": "15d 3h" },
      "opendmarc": { "version": "1.4.2", "status": "running", "uptime": "15d 3h" },
      "clamav": { "version": "1.0.3", "status": "running", "uptime": "15d 3h" },
      "roundcube": { "version": "1.6.5", "status": "running", "uptime": "15d 3h" }
    },
    "ssl_expiry": "2025-04-15T00:00:00Z",
    "total_mailboxes": 42,
    "total_domains": 8,
    "queue_size": 3,
    "disk_usage_mb": 2450
  }
}
```

#### Reconfigure Email Server

Update email server settings without reinstalling:

```json
POST /api/v1/software/reconfigure-email
{
  "hostname": "mail.example.com",
  "max_message_size_mb": 100,
  "max_connections_per_ip": 30,
  "greylisting": true,
  "spam_threshold": 5.0,
  "virus_scanning": true,
  "webmail": true
}
```

- Reconfigure updates Postfix/Dovecot config files and reloads services.
- No data loss — existing mailboxes and messages are preserved.
- Changes take effect within 30 seconds (service reload, not restart).

---

## 12. System Monitoring

Real-time server health monitoring with alerting.

| Action | Endpoint | Permission |
|--------|----------|------------|
| System info | `GET /api/v1/monitor/system` | `monitor.view` |
| Live metrics | `GET /api/v1/monitor/metrics` | `monitor.view` |
| Service status | `GET /api/v1/monitor/services` | `monitor.view` |
| Historical metrics | `GET /api/v1/monitor/history` | `monitor.view` |
| Get alerts config | `GET /api/v1/monitor/alerts` | `monitor.view` |
| Update alerts config | `PUT /api/v1/monitor/alerts` | `server.manage` |

### System Info Response

| Field | Description |
|-------|-------------|
| hostname | Server hostname |
| os | Operating system name |
| platform | Distribution (e.g., ubuntu) |
| platform_version | OS version (e.g., 22.04) |
| kernel | Kernel version |
| arch | Architecture (amd64, arm64) |
| cpu_model | CPU model name |
| cpu_cores | Number of CPU cores |
| ram_total_mb | Total RAM in MB |
| swap_total_mb | Total swap in MB |
| disk_total_gb | Total disk in GB |
| public_ip | Public IP address |
| private_ip | Private/LAN IP address |
| ipv6 | IPv6 address (if available) |
| uptime | Server uptime |
| timezone | Server timezone |

### Live Metrics Response

```json
{
  "success": true,
  "data": {
    "cpu_percent": 23.5,
    "cpu_per_core": [15.2, 31.8, 22.1, 25.0],
    "ram_used_mb": 3840,
    "ram_total_mb": 8192,
    "ram_percent": 46.9,
    "swap_used_mb": 128,
    "swap_total_mb": 2048,
    "disk_used_gb": 45.2,
    "disk_total_gb": 100,
    "disk_percent": 45.2,
    "disk_io_read_mb": 12.5,
    "disk_io_write_mb": 8.3,
    "network_rx_bytes": 1048576,
    "network_tx_bytes": 524288,
    "network_rx_rate_mbps": 15.2,
    "network_tx_rate_mbps": 8.7,
    "load_avg_1m": 0.75,
    "load_avg_5m": 0.65,
    "load_avg_15m": 0.55,
    "process_count": 142,
    "tcp_connections": 87,
    "top_processes": [
      { "pid": 1234, "name": "mongod", "cpu_percent": 8.2, "memory_mb": 512 },
      { "pid": 5678, "name": "nginx", "cpu_percent": 3.1, "memory_mb": 128 }
    ],
    "collected_at": "2025-01-10T14:30:00Z"
  }
}
```

### Service Status Response

```json
{
  "success": true,
  "data": [
    { "name": "nginx", "status": "running", "uptime": "15d 3h 22m", "pid": 1234, "memory_mb": 128 },
    { "name": "php8.2-fpm", "status": "running", "uptime": "15d 3h 22m", "pid": 2345, "memory_mb": 256 },
    { "name": "mongod", "status": "running", "uptime": "15d 3h 22m", "pid": 3456, "memory_mb": 512 },
    { "name": "postfix", "status": "running", "uptime": "15d 3h 22m", "pid": 4567, "memory_mb": 64 },
    { "name": "dovecot", "status": "running", "uptime": "15d 3h 22m", "pid": 5678, "memory_mb": 48 },
    { "name": "memcached", "status": "stopped", "uptime": null, "pid": null, "memory_mb": null },
    { "name": "pdns", "status": "running", "uptime": "15d 3h 22m", "pid": 7890, "memory_mb": 32 },
    { "name": "fail2ban", "status": "running", "uptime": "15d 3h 22m", "pid": 8901, "memory_mb": 24 }
  ]
}
```

### Historical Metrics

`GET /api/v1/monitor/history?metric=cpu&period=24h&interval=5m`

| Parameter | Options | Description |
|-----------|---------|-------------|
| `metric` | `cpu`, `ram`, `disk`, `network`, `load` | Metric type |
| `period` | `1h`, `6h`, `24h`, `7d`, `30d` | Time window |
| `interval` | `1m`, `5m`, `15m`, `1h`, `1d` | Data point interval |

- Metrics are collected every minute by a background agent process.
- Data retained for 30 days, then aggregated to hourly averages for 1 year.

### Alerts Configuration

```json
{
  "enabled": true,
  "notify_email": "admin@example.com",
  "notify_webhook": "https://hooks.slack.com/services/...",
  "thresholds": {
    "cpu_percent": { "warning": 80, "critical": 95 },
    "ram_percent": { "warning": 85, "critical": 95 },
    "disk_percent": { "warning": 80, "critical": 90 },
    "load_avg_1m_per_core": { "warning": 2.0, "critical": 5.0 }
  },
  "service_monitoring": {
    "enabled": true,
    "services": ["nginx", "mongod", "php8.2-fpm", "postfix", "dovecot"],
    "auto_restart": true,
    "notify_on_restart": true
  }
}
```

- Alert states: `ok`, `warning`, `critical`.
- `auto_restart` — automatically restarts crashed services and sends a notification.
- Notifications sent via email and/or webhook (Slack, Discord, custom HTTP endpoint).

---

## 13. Log Viewer

View, search, and download server and application logs.

| Action | Endpoint | Permission |
|--------|----------|------------|
| View logs | `GET /api/v1/logs/:type` | `log.view` |
| Search logs | `GET /api/v1/logs/:type/search` | `log.view` |
| Download log | `GET /api/v1/logs/:type/download` | `log.view` |
| List log files | `GET /api/v1/logs/files` | `log.view` |

### Available Log Types

| Type Parameter | Source | Example |
|---------------|--------|---------|
| `system` | `journalctl` | System-wide logs |
| `app/{name}` | `journalctl -u sp-app-{name}` | Application logs |
| `access/{domain}` | `/var/log/nginx/{domain}-access.log` | Nginx access log |
| `error/{domain}` | `/var/log/nginx/{domain}-error.log` | Nginx error log |
| `php/{domain}` | `/home/{user}/logs/php-error.log` | PHP-FPM error log |
| `mail` | `/var/log/mail.log` | Mail server log |
| `mongodb` | `/var/log/mongodb/mongod.log` | MongoDB log |
| `auth` | `/var/log/auth.log` | Authentication/SSH log |
| `cron` | `journalctl -u cron` | Cron execution log |
| `service/{name}` | `journalctl -u {name}` | Any systemd service |
| `firewall` | `journalctl -k \| grep UFW` | UFW firewall log |

### Query Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `lines` | `100` | Number of lines to return (max 5000) |
| `since` | — | Start time (ISO 8601): `?since=2025-01-10T00:00:00Z` |
| `until` | — | End time (ISO 8601) |
| `priority` | — | Syslog priority filter: `emerg`, `alert`, `crit`, `err`, `warning`, `notice`, `info`, `debug` |

### Search Logs

`GET /api/v1/logs/access/example.com/search?query=404&lines=200`

| Parameter | Description |
|-----------|-------------|
| `query` | Search string (supports regex) |
| `lines` | Max results to return |
| `case_sensitive` | `true` or `false` (default: false) |

### Download Log

`GET /api/v1/logs/access/example.com/download?format=raw`

| Format | Description |
|--------|-------------|
| `raw` | Plain text log file |
| `gz` | Gzip-compressed log file |

### Log Response

```json
{
  "success": true,
  "data": {
    "type": "access/example.com",
    "source": "/var/log/nginx/example.com-access.log",
    "lines_returned": 100,
    "lines": [
      "203.0.113.50 - - [10/Jan/2025:14:30:00 +0000] \"GET / HTTP/2.0\" 200 1234 \"-\" \"Mozilla/5.0...\"",
      "..."
    ]
  }
}
```

### Examples

```
GET /api/v1/logs/system?lines=200
GET /api/v1/logs/app/my-node-app?lines=50
GET /api/v1/logs/access/example.com?since=2025-01-10T00:00:00Z
GET /api/v1/logs/error/example.com?priority=err
GET /api/v1/logs/mail?lines=500
GET /api/v1/logs/service/nginx
GET /api/v1/logs/auth?lines=100
GET /api/v1/logs/access/example.com/search?query=POST.*login&lines=200
```

---

## 14. Cron Job Management

Create and manage scheduled tasks (cron jobs) for any domain.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List cron jobs | `GET /api/v1/cron/` | `cron.manage` |
| Get cron job | `GET /api/v1/cron/:id` | `cron.manage` |
| Create cron job | `POST /api/v1/cron/` | `cron.manage` |
| Update cron job | `PUT /api/v1/cron/:id` | `cron.manage` |
| Delete cron job | `DELETE /api/v1/cron/:id` | `cron.manage` |
| Toggle cron job | `PATCH /api/v1/cron/:id/toggle` | `cron.manage` |
| Run cron job now | `POST /api/v1/cron/:id/run` | `cron.manage` |
| View execution history | `GET /api/v1/cron/:id/history` | `cron.manage` |

### Create Cron Job Payload

```json
{
  "domain": "example.com",
  "user": "exampleuser",
  "command": "php /home/exampleuser/public_html/artisan schedule:run",
  "schedule": "*/5 * * * *",
  "description": "Laravel task scheduler",
  "notify_email": "admin@example.com",
  "notify_on": "failure",
  "enabled": true
}
```

### Schedule Presets

| Preset | Cron Expression | Description |
|--------|----------------|-------------|
| `every_minute` | `* * * * *` | Every minute |
| `every_5_minutes` | `*/5 * * * *` | Every 5 minutes |
| `every_15_minutes` | `*/15 * * * *` | Every 15 minutes |
| `every_30_minutes` | `*/30 * * * *` | Every 30 minutes |
| `hourly` | `0 * * * *` | Every hour at :00 |
| `daily` | `0 0 * * *` | Midnight daily |
| `weekly` | `0 0 * * 0` | Midnight Sunday |
| `monthly` | `0 0 1 * *` | Midnight on the 1st |

Users can either use a preset name or provide a custom cron expression.

### Notification Options

| Option | Description |
|--------|-------------|
| `never` | No notifications |
| `always` | Notify on every execution |
| `failure` | Notify only on non-zero exit code |
| `success` | Notify only on successful execution |

### Execution History Response

```json
{
  "success": true,
  "data": [
    {
      "id": "65a1b2c3d4e5f6a7b8c9d0e1",
      "executed_at": "2025-01-10T14:30:00Z",
      "exit_code": 0,
      "duration_ms": 1250,
      "output": "Scheduled tasks executed successfully.",
      "status": "success"
    },
    {
      "id": "65a1b2c3d4e5f6a7b8c9d0e2",
      "executed_at": "2025-01-10T14:25:00Z",
      "exit_code": 1,
      "duration_ms": 850,
      "output": "Error: Database connection refused",
      "status": "failure"
    }
  ]
}
```

### Implementation Details

- Cron jobs are written to the Linux user's crontab (`crontab -u {user}`).
- Output is captured to a log file at `/home/{user}/logs/cron-{id}.log`.
- Each execution record is stored in MongoDB for history (retained for 30 days).
- Commands run as the domain's Linux user — not root.

---

## 15. File Manager

Browse, upload, download, edit, and manage files within a domain's home directory.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List directory | `GET /api/v1/files/list` | `file.manage` |
| Get file content | `GET /api/v1/files/read` | `file.manage` |
| Create file | `POST /api/v1/files/create` | `file.manage` |
| Upload file | `POST /api/v1/files/upload` | `file.manage` |
| Edit file | `PUT /api/v1/files/edit` | `file.manage` |
| Delete file/folder | `DELETE /api/v1/files/delete` | `file.manage` |
| Rename / move | `POST /api/v1/files/rename` | `file.manage` |
| Copy | `POST /api/v1/files/copy` | `file.manage` |
| Create directory | `POST /api/v1/files/mkdir` | `file.manage` |
| Set permissions | `POST /api/v1/files/chmod` | `file.manage` |
| Compress | `POST /api/v1/files/compress` | `file.manage` |
| Extract | `POST /api/v1/files/extract` | `file.manage` |
| Download | `GET /api/v1/files/download` | `file.manage` |
| Get disk usage | `GET /api/v1/files/disk-usage` | `file.manage` |

### List Directory

`GET /api/v1/files/list?user=exampleuser&path=/public_html`

**Response:**

```json
{
  "success": true,
  "data": {
    "path": "/home/exampleuser/public_html",
    "items": [
      {
        "name": "index.html",
        "type": "file",
        "size": 1024,
        "permissions": "644",
        "owner": "exampleuser",
        "group": "exampleuser",
        "modified_at": "2025-01-10T14:30:00Z"
      },
      {
        "name": "images",
        "type": "directory",
        "size": 4096,
        "permissions": "755",
        "owner": "exampleuser",
        "group": "exampleuser",
        "modified_at": "2025-01-08T10:00:00Z"
      }
    ],
    "total_items": 15,
    "current_disk_usage_mb": 1250,
    "disk_quota_mb": 5120
  }
}
```

### Create / Edit File

```json
{
  "user": "exampleuser",
  "path": "/public_html/config.php",
  "content": "<?php\nreturn ['debug' => false];\n",
  "encoding": "utf-8"
}
```

### Upload File

`POST /api/v1/files/upload` (multipart/form-data)

| Field | Description |
|-------|-------------|
| `user` | Linux username |
| `path` | Target directory path |
| `file` | File binary (max 500 MB) |
| `overwrite` | `true` to overwrite existing file |

### Rename / Move

```json
{
  "user": "exampleuser",
  "source": "/public_html/old-name.html",
  "destination": "/public_html/new-name.html"
}
```

### Set Permissions

```json
{
  "user": "exampleuser",
  "path": "/public_html/uploads",
  "permissions": "755",
  "recursive": true
}
```

### Compress

```json
{
  "user": "exampleuser",
  "paths": ["/public_html/images", "/public_html/uploads"],
  "output": "/public_html/archive.zip",
  "format": "zip"
}
```

| Format | Description |
|--------|-------------|
| `zip` | Standard ZIP archive |
| `tar.gz` | Gzip-compressed tar archive |

### Extract

```json
{
  "user": "exampleuser",
  "archive": "/public_html/archive.zip",
  "destination": "/public_html/extracted/"
}
```

Supports: `.zip`, `.tar.gz`, `.tar.bz2`, `.tar`, `.rar`, `.7z`

### Security Constraints

- All file operations are sandboxed to `/home/{user}/` — no path traversal allowed.
- Symlinks outside the home directory are blocked.
- Executable uploads (`.sh`, `.bin`, etc.) are restricted to the `apps/` directory.
- File operations run as the domain's Linux user — not root.

---

## 16. SSH Key Management

Manage SSH keys for domain users, enabling passwordless authentication and Git deploy keys.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List SSH keys | `GET /api/v1/ssh-keys/:user` | `server.manage` |
| Add SSH key | `POST /api/v1/ssh-keys/:user` | `server.manage` |
| Delete SSH key | `DELETE /api/v1/ssh-keys/:user/:id` | `server.manage` |
| Generate key pair | `POST /api/v1/ssh-keys/:user/generate` | `server.manage` |

### Add SSH Key Payload

```json
{
  "name": "John's Laptop",
  "public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5... john@laptop",
  "key_type": "login"
}
```

| Key Type | Description |
|----------|-------------|
| `login` | Added to `~/.ssh/authorized_keys` — allows SSH login |
| `deploy` | Added to `~/.ssh/` as an identity — used for Git clone/pull |

### Generate Key Pair Response

```json
{
  "success": true,
  "data": {
    "name": "deploy-key-example",
    "public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5... deploy@serverpanel",
    "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNza...\n-----END OPENSSH PRIVATE KEY-----",
    "fingerprint": "SHA256:AbCdEf..."
  }
}
```

**Important:** The private key is returned ONLY during generation. It is not stored on the server. The user must save it immediately.

### Implementation

- Keys are written to `/home/{user}/.ssh/authorized_keys` (for login keys).
- Deploy keys are stored at `/home/{user}/.ssh/deploy_{name}` with a corresponding SSH config entry.
- `chmod 700 ~/.ssh` and `chmod 600 ~/.ssh/authorized_keys` enforced.
- Supported key types: `ssh-rsa`, `ssh-ed25519`, `ecdsa-sha2-nistp256`.

---

## 17. Process Manager

View and manage running processes on the server.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List processes | `GET /api/v1/processes/` | `server.view` |
| Get process detail | `GET /api/v1/processes/:pid` | `server.view` |
| Kill process | `POST /api/v1/processes/:pid/kill` | `server.manage` |
| List services | `GET /api/v1/processes/services` | `server.view` |
| Control service | `POST /api/v1/processes/services/:name/:action` | `server.manage` |

### List Processes

`GET /api/v1/processes/?sort=cpu&limit=50`

**Response:**

```json
{
  "success": true,
  "data": {
    "total_count": 142,
    "processes": [
      {
        "pid": 1234,
        "ppid": 1,
        "user": "mongodb",
        "name": "mongod",
        "command": "/usr/bin/mongod --config /etc/mongod.conf",
        "cpu_percent": 8.2,
        "memory_mb": 512,
        "memory_percent": 6.25,
        "status": "running",
        "started_at": "2025-01-01T00:00:00Z",
        "threads": 24
      }
    ]
  }
}
```

### Kill Process

```json
{
  "signal": "SIGTERM"
}
```

| Signal | Description |
|--------|-------------|
| `SIGTERM` | Graceful termination (default) |
| `SIGKILL` | Force kill |
| `SIGHUP` | Reload configuration |

**Safety:** Killing system-critical processes (PID 1, init, agent, sshd) is blocked.

### Service Control

`POST /api/v1/processes/services/nginx/restart`

| Action | Description |
|--------|-------------|
| `start` | `systemctl start {name}` |
| `stop` | `systemctl stop {name}` |
| `restart` | `systemctl restart {name}` |
| `reload` | `systemctl reload {name}` |
| `enable` | `systemctl enable {name}` (start on boot) |
| `disable` | `systemctl disable {name}` (don't start on boot) |

---

## 18. Resource Usage & Bandwidth

Track per-domain resource consumption and enforce limits.

| Action | Endpoint | Permission |
|--------|----------|------------|
| Get usage summary | `GET /api/v1/resources/summary` | `server.view` |
| Get domain usage | `GET /api/v1/resources/domains/:domain` | `domain.view` |
| Get bandwidth stats | `GET /api/v1/resources/bandwidth` | `server.view` |
| Get bandwidth by domain | `GET /api/v1/resources/bandwidth/:domain` | `domain.view` |
| Update domain limits | `PUT /api/v1/resources/domains/:domain/limits` | `domain.manage` |

### Server Usage Summary

```json
{
  "success": true,
  "data": {
    "total_domains": 12,
    "total_disk_used_gb": 45.2,
    "total_disk_gb": 100,
    "total_bandwidth_used_gb": 320.5,
    "period": "2025-01",
    "top_domains_by_disk": [
      { "domain": "example.com", "disk_used_mb": 8500 },
      { "domain": "shop.example.com", "disk_used_mb": 6200 }
    ],
    "top_domains_by_bandwidth": [
      { "domain": "shop.example.com", "bandwidth_gb": 120.5 },
      { "domain": "example.com", "bandwidth_gb": 85.2 }
    ]
  }
}
```

### Domain Usage

```json
{
  "success": true,
  "data": {
    "domain": "example.com",
    "disk": {
      "used_mb": 1250,
      "quota_mb": 5120,
      "percent": 24.4,
      "breakdown": {
        "public_html_mb": 800,
        "apps_mb": 250,
        "email_mb": 150,
        "logs_mb": 30,
        "backups_mb": 20
      }
    },
    "bandwidth": {
      "used_gb": 85.2,
      "limit_gb": 100,
      "percent": 85.2,
      "daily_average_gb": 2.75
    },
    "email_accounts": { "used": 8, "limit": 25 },
    "databases": { "used": 3, "limit": 10 },
    "subdomains": { "used": 4, "limit": 20 },
    "apps": { "used": 2, "limit": 5 }
  }
}
```

### Bandwidth Stats

`GET /api/v1/resources/bandwidth?period=2025-01&interval=daily`

```json
{
  "success": true,
  "data": {
    "period": "2025-01",
    "total_gb": 320.5,
    "daily": [
      { "date": "2025-01-01", "rx_gb": 5.2, "tx_gb": 8.1 },
      { "date": "2025-01-02", "rx_gb": 4.8, "tx_gb": 7.5 }
    ]
  }
}
```

- Bandwidth is calculated by parsing Nginx access logs (request/response sizes).
- Data is aggregated daily and stored in MongoDB.
- When a domain reaches 90% of bandwidth limit, a warning notification is sent.
- At 100%, access is blocked (shows a "bandwidth exceeded" page) unless `bandwidth_overage: allow` is configured.

### Update Domain Limits

```json
{
  "disk_quota_mb": 10240,
  "bandwidth_limit_gb": 200,
  "max_databases": 20,
  "max_email_accounts": 50,
  "max_subdomains": 50,
  "max_apps": 10
}
```

---

## 19. Notification & Webhooks

Configure how the system sends alerts and event notifications.

| Action | Endpoint | Permission |
|--------|----------|------------|
| Get notification settings | `GET /api/v1/notifications/settings` | `server.manage` |
| Update notification settings | `PUT /api/v1/notifications/settings` | `server.manage` |
| List notification history | `GET /api/v1/notifications/history` | `server.view` |
| List webhooks | `GET /api/v1/webhooks/` | `server.manage` |
| Create webhook | `POST /api/v1/webhooks/` | `server.manage` |
| Update webhook | `PUT /api/v1/webhooks/:id` | `server.manage` |
| Delete webhook | `DELETE /api/v1/webhooks/:id` | `server.manage` |
| Test webhook | `POST /api/v1/webhooks/:id/test` | `server.manage` |

### Notification Settings

```json
{
  "email": {
    "enabled": true,
    "recipients": ["admin@example.com", "ops@example.com"],
    "events": ["service_down", "backup_failed", "ssl_expiring", "disk_critical", "security_alert"]
  },
  "slack": {
    "enabled": true,
    "webhook_url": "https://hooks.slack.com/services/T.../B.../xxx",
    "channel": "#server-alerts",
    "events": ["service_down", "backup_failed", "security_alert"]
  }
}
```

### Webhook Events

| Event | Trigger |
|-------|---------|
| `domain.created` | New domain provisioned |
| `domain.deleted` | Domain removed |
| `domain.suspended` | Domain suspended |
| `app.deployed` | Application deployed or redeployed |
| `app.crashed` | Application service exited unexpectedly |
| `backup.completed` | Backup finished successfully |
| `backup.failed` | Backup failed |
| `ssl.issued` | SSL certificate issued or renewed |
| `ssl.expiring` | SSL certificate expires within 14 days |
| `service.down` | Monitored service is unreachable |
| `service.recovered` | Previously down service is back |
| `disk.warning` | Disk usage exceeded warning threshold |
| `disk.critical` | Disk usage exceeded critical threshold |
| `cpu.critical` | CPU usage exceeded critical threshold |
| `security.login_failed` | Multiple failed login attempts detected |
| `security.ip_blocked` | IP address blocked by fail2ban |
| `user.created` | New user account created |
| `bandwidth.warning` | Domain approaching bandwidth limit |

### Create Webhook Payload

```json
{
  "url": "https://api.example.com/webhooks/serverpanel",
  "secret": "webhook-signing-secret",
  "events": ["app.deployed", "app.crashed", "backup.completed"],
  "active": true
}
```

### Webhook Delivery

Each webhook delivery includes:

```
POST https://your-endpoint.com/webhook
Content-Type: application/json
X-ServerPanel-Event: app.deployed
X-ServerPanel-Signature: sha256=abc123...
X-ServerPanel-Delivery: 65a1b2c3d4e5f6a7b8c9d0e1

{
  "event": "app.deployed",
  "timestamp": "2025-01-10T14:30:00Z",
  "data": {
    "app_name": "my-node-app",
    "domain": "example.com",
    "deployment_id": "65a1b2c3d4e5f6a7b8c9d0e2",
    "status": "success"
  }
}
```

- Signature is HMAC-SHA256 of the body using the webhook secret.
- Failed deliveries are retried 3 times with exponential backoff (1s, 30s, 300s).
- Delivery history retained for 7 days.

---

## 20. Activity / Audit Log

Track all actions performed through the panel for security auditing and troubleshooting.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List activity logs | `GET /api/v1/audit/` | `server.view` |
| Get log entry | `GET /api/v1/audit/:id` | `server.view` |
| Export audit log | `GET /api/v1/audit/export` | `server.manage` |

### Query Parameters

| Parameter | Example | Description |
|-----------|---------|-------------|
| `user_id` | `?user_id=65a...` | Filter by user |
| `action` | `?action=domain.create` | Filter by action type |
| `resource` | `?resource=domain` | Filter by resource type |
| `ip` | `?ip=203.0.113.50` | Filter by source IP |
| `since` | `?since=2025-01-01T00:00:00Z` | Start date |
| `until` | `?until=2025-01-10T23:59:59Z` | End date |
| `status` | `?status=success` | Filter: `success` or `failure` |

### Audit Log Entry

```json
{
  "id": "65a1b2c3d4e5f6a7b8c9d0e1",
  "timestamp": "2025-01-10T14:30:00Z",
  "user": {
    "id": "65a1b2c3d4e5f6a7b8c9d0e2",
    "email": "admin@example.com",
    "role": "vendor_owner"
  },
  "action": "domain.create",
  "resource_type": "domain",
  "resource_id": "65a1b2c3d4e5f6a7b8c9d0e3",
  "description": "Created domain example.com",
  "ip_address": "203.0.113.50",
  "user_agent": "Mozilla/5.0 ...",
  "status": "success",
  "metadata": {
    "domain": "example.com",
    "user": "exampleuser",
    "php_version": "8.2"
  }
}
```

### Logged Actions

All state-changing operations are logged:
- Authentication (login, logout, failed attempts, 2FA events)
- User management (create, update, delete, suspend)
- Domain operations (create, delete, suspend, PHP switch)
- App deployment (deploy, redeploy, rollback, start, stop)
- Database operations (create, delete, remote access changes)
- Email management (create, delete mailboxes, forwarding changes)
- DNS changes (zone creation, record changes)
- SSL operations (issue, renew, revoke)
- Backup operations (create, restore, delete)
- Firewall changes (rule modifications, IP blocks)
- Software installation/removal
- Server configuration changes
- Cron job modifications
- File operations (upload, delete, permission changes)

### Export

`GET /api/v1/audit/export?since=2025-01-01T00:00:00Z&format=csv`

| Format | Description |
|--------|-------------|
| `csv` | CSV file download |
| `json` | JSON array download |

- Audit logs are retained for 1 year.
- Logs are immutable — they cannot be edited or deleted through the API.

---

## 21. Server Configuration

Tune server-wide settings for Nginx, PHP, MongoDB, and other services.

| Action | Endpoint | Permission |
|--------|----------|------------|
| Get server config | `GET /api/v1/config/` | `server.manage` |
| Update Nginx config | `PUT /api/v1/config/nginx` | `server.manage` |
| Update PHP config | `PUT /api/v1/config/php/:version` | `server.manage` |
| Update MongoDB config | `PUT /api/v1/config/mongodb` | `server.manage` |
| Update hostname | `PUT /api/v1/config/hostname` | `server.manage` |
| Update timezone | `PUT /api/v1/config/timezone` | `server.manage` |
| Update nameservers | `PUT /api/v1/config/nameservers` | `server.manage` |
| Test Nginx config | `POST /api/v1/config/nginx/test` | `server.manage` |
| Restart service | `POST /api/v1/config/:service/restart` | `server.manage` |

### Nginx Configuration

```json
{
  "worker_processes": "auto",
  "worker_connections": 1024,
  "keepalive_timeout": 65,
  "client_max_body_size": "100m",
  "gzip": true,
  "gzip_types": ["text/plain", "text/css", "application/json", "application/javascript"],
  "server_tokens": false,
  "rate_limit_enabled": true,
  "rate_limit_requests": 50,
  "rate_limit_burst": 100
}
```

- Changes are written to `/etc/nginx/nginx.conf` (global) or `/etc/nginx/conf.d/serverpanel.conf`.
- `nginx -t` is always run before applying changes — if the test fails, changes are rolled back.

### PHP Configuration (per version)

```json
{
  "memory_limit": "256M",
  "max_execution_time": 300,
  "max_input_time": 300,
  "post_max_size": "100M",
  "upload_max_filesize": "100M",
  "max_file_uploads": 20,
  "display_errors": false,
  "error_reporting": "E_ALL & ~E_DEPRECATED & ~E_STRICT",
  "date_timezone": "UTC",
  "opcache_enabled": true,
  "opcache_memory": 128
}
```

- Applied to `/etc/php/{version}/fpm/conf.d/99-serverpanel.ini`.
- PHP-FPM is reloaded after changes.

### MongoDB Configuration

```json
{
  "storage_engine": "wiredTiger",
  "cache_size_gb": 1,
  "max_connections": 500,
  "journal_enabled": true,
  "slow_query_threshold_ms": 100,
  "profiling_level": 0,
  "bind_ip": "127.0.0.1",
  "auth_enabled": true
}
```

- Applied to `/etc/mongod.conf`.
- `mongod` is restarted after changes (warning shown to user).
- `profiling_level`: `0` = off, `1` = slow queries only, `2` = all queries.

### Server Hostname

```json
{
  "hostname": "server1.hostingprovider.com"
}
```

### Timezone

```json
{
  "timezone": "America/New_York"
}
```

---

## 22. Maintenance Mode

Enable server-wide or per-domain maintenance mode to show a "service unavailable" page during updates.

| Action | Endpoint | Permission |
|--------|----------|------------|
| Get maintenance status | `GET /api/v1/maintenance/` | `server.view` |
| Enable server-wide | `POST /api/v1/maintenance/enable` | `server.manage` |
| Disable server-wide | `POST /api/v1/maintenance/disable` | `server.manage` |
| Enable per-domain | `POST /api/v1/maintenance/domains/:domain/enable` | `domain.manage` |
| Disable per-domain | `POST /api/v1/maintenance/domains/:domain/disable` | `domain.manage` |

### Enable Maintenance Mode

```json
{
  "message": "We are currently performing scheduled maintenance. We'll be back shortly.",
  "allowed_ips": ["203.0.113.50", "10.0.0.0/8"],
  "estimated_end": "2025-01-10T16:00:00Z",
  "custom_page_html": "",
  "retry_after": 3600
}
```

- `allowed_ips` — these IPs bypass maintenance mode and see the live site (for testing).
- `retry_after` — sets the HTTP `Retry-After` header value (in seconds).
- `custom_page_html` — optional custom HTML page; if empty, a default ServerPanel maintenance page is used.
- Returns HTTP 503 to all other visitors.
- Nginx is configured to serve the maintenance page directly (no PHP/app processing).

---

## 23. GitHub Deployment (CI/CD)

Deploy applications and the ServerPanel itself directly from GitHub repositories. Supports automated deployments via GitHub webhooks, manual deploys from any branch/tag, and GitHub Actions integration.

### 23.1 Connect GitHub Account

| Action | Endpoint | Permission |
|--------|----------|------------|
| Connect GitHub (OAuth) | `POST /api/v1/github/connect` | `deploy.manage` |
| Disconnect GitHub | `DELETE /api/v1/github/connect` | `deploy.manage` |
| Get connection status | `GET /api/v1/github/status` | `deploy.view` |
| List repositories | `GET /api/v1/github/repos` | `deploy.view` |
| List branches | `GET /api/v1/github/repos/:owner/:repo/branches` | `deploy.view` |

#### Connect GitHub (OAuth Flow)

```
1. Frontend redirects to: GET /api/v1/github/authorize
2. User authorizes on GitHub
3. GitHub redirects back with code
4. Backend exchanges code for access token
5. Token stored encrypted in MongoDB
```

**Request — Connect with personal access token (alternative):**

```json
POST /api/v1/github/connect
{
  "method": "pat",
  "token": "ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
}
```

**Response:**

```json
{
  "success": true,
  "data": {
    "github_username": "betazeninfotech",
    "connected_at": "2025-01-15T10:00:00Z",
    "method": "pat",
    "scopes": ["repo", "read:org", "admin:repo_hook"]
  }
}
```

### 23.2 Deploy from GitHub Repository

| Action | Endpoint | Permission |
|--------|----------|------------|
| Create deployment | `POST /api/v1/deploy/` | `deploy.manage` |
| List deployments | `GET /api/v1/deploy/` | `deploy.view` |
| Get deployment | `GET /api/v1/deploy/:id` | `deploy.view` |
| Redeploy | `POST /api/v1/deploy/:id/redeploy` | `deploy.manage` |
| Rollback | `POST /api/v1/deploy/:id/rollback` | `deploy.manage` |
| Cancel deployment | `POST /api/v1/deploy/:id/cancel` | `deploy.manage` |
| Delete deployment config | `DELETE /api/v1/deploy/:id` | `deploy.manage` |
| Get deploy logs | `GET /api/v1/deploy/:id/logs` | `deploy.view` |

#### Create Deployment

```json
POST /api/v1/deploy/
{
  "domain": "example.com",
  "repo": "betazeninfotech/my-app",
  "branch": "main",
  "app_type": "nodejs",
  "auto_deploy": true,
  "build_command": "npm ci && npm run build",
  "start_command": "npm start",
  "env_vars": {
    "NODE_ENV": "production",
    "PORT": "3000"
  },
  "node_version": "20",
  "root_dir": "/",
  "pre_deploy_script": "",
  "post_deploy_script": "npx prisma migrate deploy"
}
```

**Supported `app_type` values:**

| App Type | Build & Runtime |
|----------|----------------|
| `nodejs` | Node.js (npm/yarn/pnpm) |
| `static` | Static HTML/CSS/JS (Vite, CRA, Hugo, etc.) |
| `php` | PHP (composer install + PHP-FPM) |
| `python` | Python (pip install + gunicorn/uvicorn) |
| `go` | Go (go build + binary execution) |
| `docker` | Dockerfile-based (docker build + docker run) |

**Response:**

```json
{
  "success": true,
  "data": {
    "id": "deploy_abc123",
    "domain": "example.com",
    "repo": "betazeninfotech/my-app",
    "branch": "main",
    "app_type": "nodejs",
    "status": "deploying",
    "auto_deploy": true,
    "current_commit": "a1b2c3d",
    "commit_message": "fix: resolve login bug",
    "commit_author": "betazeninfotech",
    "deploy_url": "http://example.com",
    "created_at": "2025-01-15T12:00:00Z"
  }
}
```

#### Deployment Status Values

| Status | Description |
|--------|-------------|
| `queued` | Deployment is queued, waiting to start |
| `cloning` | Cloning repository from GitHub |
| `building` | Running build command |
| `deploying` | Moving build output and starting app |
| `live` | Deployment successful — app is running |
| `failed` | Deployment failed (check logs) |
| `cancelled` | Deployment was manually cancelled |
| `rolling_back` | Rollback in progress |

#### Get Deploy Logs (streamed)

```
GET /api/v1/deploy/deploy_abc123/logs?stream=true

Accept: text/event-stream
```

```
data: {"time":"12:00:01","line":"Cloning betazeninfotech/my-app@main..."}
data: {"time":"12:00:05","line":"Cloned commit a1b2c3d (fix: resolve login bug)"}
data: {"time":"12:00:06","line":"Running: npm ci"}
data: {"time":"12:00:22","line":"added 412 packages in 16s"}
data: {"time":"12:00:23","line":"Running: npm run build"}
data: {"time":"12:00:38","line":"Build completed successfully"}
data: {"time":"12:00:39","line":"Running post-deploy: npx prisma migrate deploy"}
data: {"time":"12:00:42","line":"1 migration applied"}
data: {"time":"12:00:43","line":"Starting app: npm start"}
data: {"time":"12:00:45","line":"App is live on http://example.com"}
data: {"time":"12:00:45","line":"[DONE] Deployment deploy_abc123 completed"}
```

#### Rollback

```json
POST /api/v1/deploy/deploy_abc123/rollback
{
  "target_commit": "e4f5g6h"
}
```

If `target_commit` is omitted, rolls back to the previous successful deployment.

### 23.3 GitHub Webhooks (Auto-Deploy)

When `auto_deploy` is enabled, ServerPanel automatically registers a GitHub webhook on the repository. On every push to the configured branch, a new deployment is triggered.

| Action | Endpoint | Permission |
|--------|----------|------------|
| List webhooks | `GET /api/v1/deploy/webhooks/` | `deploy.view` |
| GitHub webhook receiver | `POST /api/v1/deploy/webhooks/github` | _(public, verified by signature)_ |
| Pause auto-deploy | `POST /api/v1/deploy/:id/pause` | `deploy.manage` |
| Resume auto-deploy | `POST /api/v1/deploy/:id/resume` | `deploy.manage` |

**Webhook flow:**

```
GitHub push event
  → POST /api/v1/deploy/webhooks/github
  → Verify X-Hub-Signature-256 (HMAC-SHA256)
  → Match repo + branch to deployment config
  → Trigger new deployment
  → Send notification (email/webhook) with result
```

**Webhook payload validation:**

- All incoming webhooks are verified using the `X-Hub-Signature-256` header with the webhook secret.
- Only `push` events on the configured branch trigger deployments.
- Other events (`pull_request`, `release`, etc.) are logged but ignored.

### 23.4 GitHub Actions Integration

For advanced CI/CD pipelines, ServerPanel provides a deploy API that can be called from GitHub Actions.

**Deploy API key:**

```json
POST /api/v1/deploy/api-keys
{
  "name": "github-actions-prod",
  "deployment_id": "deploy_abc123"
}
```

**Response:**

```json
{
  "success": true,
  "data": {
    "key": "spd_xxxxxxxxxxxxxxxxxxxxxxxxxxxx",
    "name": "github-actions-prod",
    "created_at": "2025-01-15T10:00:00Z"
  }
}
```

**Example GitHub Actions workflow (`.github/workflows/deploy.yml`):**

```yaml
name: Deploy to ServerPanel

on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Setup Node.js
        uses: actions/setup-node@v4
        with:
          node-version: 20

      - name: Install & Build
        run: |
          npm ci
          npm run build
          npm test

      - name: Deploy to ServerPanel
        run: |
          curl -X POST "https://panel.betazeninfotech.com/api/v1/whm/deploy/deploy_abc123/trigger" \
            -H "Authorization: Bearer ${{ secrets.SERVERPANEL_DEPLOY_KEY }}" \
            -H "Content-Type: application/json" \
            -d '{"commit": "${{ github.sha }}", "ref": "${{ github.ref }}"}'
```

| Action | Endpoint | Auth |
|--------|----------|------|
| Trigger deploy (API key) | `POST /api/v1/deploy/:id/trigger` | `Bearer spd_xxx` (deploy key) |
| List deploy API keys | `GET /api/v1/deploy/api-keys` | `deploy.manage` |
| Revoke deploy API key | `DELETE /api/v1/deploy/api-keys/:key_id` | `deploy.manage` |

### 23.5 Deployment Pipeline

Each deployment follows this pipeline:

```
1. Clone         → git clone --depth 1 --branch <branch> <repo>
2. Detect        → Identify app_type if auto (package.json, go.mod, etc.)
3. Install       → Install dependencies (npm ci, pip install, composer install, etc.)
4. Build         → Run build_command
5. Pre-deploy    → Run pre_deploy_script (migrations, cache clear, etc.)
6. Switch        → Atomic symlink switch (zero-downtime)
7. Start/Reload  → Start or restart the application process
8. Post-deploy   → Run post_deploy_script
9. Health check  → HTTP GET to app URL, wait for 200 response
10. Notify       → Send deployment status via configured channels
```

**Zero-downtime strategy:**

- Deployments clone into a new release directory: `/var/serverpanel/releases/<deploy_id>/<timestamp>/`
- On success, the `current` symlink is atomically switched to the new release.
- Previous releases are kept (configurable retention, default: 5 releases).
- On failure, the `current` symlink remains on the last working release.

### 23.6 Deploy History

```json
GET /api/v1/deploy/deploy_abc123/history?page=1&limit=10
```

**Response:**

```json
{
  "success": true,
  "data": [
    {
      "id": "release_001",
      "commit": "a1b2c3d",
      "commit_message": "fix: resolve login bug",
      "author": "betazeninfotech",
      "branch": "main",
      "status": "live",
      "trigger": "webhook",
      "duration_seconds": 45,
      "deployed_at": "2025-01-15T12:00:45Z"
    },
    {
      "id": "release_000",
      "commit": "x9y8z7w",
      "commit_message": "feat: add user dashboard",
      "author": "betazeninfotech",
      "branch": "main",
      "status": "superseded",
      "trigger": "manual",
      "duration_seconds": 52,
      "deployed_at": "2025-01-14T09:30:00Z"
    }
  ],
  "pagination": { "page": 1, "limit": 10, "total": 2, "total_pages": 1 }
}
```

### 23.7 Environment Variables per Deployment

| Action | Endpoint | Permission |
|--------|----------|------------|
| List env vars | `GET /api/v1/deploy/:id/env` | `deploy.manage` |
| Set env vars | `PUT /api/v1/deploy/:id/env` | `deploy.manage` |
| Delete env var | `DELETE /api/v1/deploy/:id/env/:key` | `deploy.manage` |

```json
PUT /api/v1/deploy/deploy_abc123/env
{
  "env_vars": {
    "DATABASE_URL": "mongodb+srv://...",
    "API_SECRET": "s3cret",
    "NODE_ENV": "production"
  }
}
```

- Env vars are stored encrypted in MongoDB.
- Changing env vars triggers an automatic app restart (unless `restart: false` is passed).
- Sensitive values are masked in the UI and API responses (`API_SECRET: "s3c***"`).

---

## 24. Role-Based Access Control (RBAC)

### Roles

| Role | Description | Use Case |
|------|-------------|----------|
| `vendor_owner` | Full WHM access — all permissions | Hosting company owner |
| `vendor_admin` | WHM access minus destructive & config ops | Staff administrator |
| `developer` | App deployment + files + logs + monitoring | Developer with server access |
| `support` | Read-only access across all resources | Support team member |
| `customer` | Self-service cPanel (`/cpanel/*` routes only) | End customer / website owner |

### Permission Matrix

| Permission | vendor_owner | vendor_admin | developer | support | customer |
|------------|:---:|:---:|:---:|:---:|:---:|
| server.manage | Y | - | - | - | - |
| server.view | Y | Y | - | Y | - |
| domain.create | Y | Y | - | - | - |
| domain.view | Y | Y | Y | Y | Y |
| domain.delete | Y | - | - | - | - |
| domain.manage | Y | Y | - | - | - |
| email.create | Y | Y | - | - | Y |
| email.view | Y | Y | - | Y | Y |
| email.manage | Y | Y | - | - | Y |
| database.create | Y | Y | - | - | Y |
| database.view | Y | Y | Y | Y | Y |
| database.manage | Y | Y | - | - | Y |
| app.deploy | Y | Y | Y | - | Y |
| app.manage | Y | Y | Y | - | - |
| app.view | Y | Y | Y | Y | Y |
| wordpress.install | Y | Y | - | - | Y |
| wordpress.manage | Y | Y | - | - | Y |
| backup.create | Y | Y | - | - | Y |
| backup.restore | Y | Y | - | - | - |
| backup.view | Y | Y | - | Y | Y |
| ssl.manage | Y | Y | - | - | Y |
| dns.manage | Y | Y | - | - | - |
| dns.view | Y | Y | - | Y | Y |
| cron.manage | Y | Y | - | - | Y |
| firewall.manage | Y | - | - | - | - |
| user.create | Y | Y | - | - | - |
| user.view | Y | Y | - | Y | - |
| user.manage | Y | - | - | - | - |
| monitor.view | Y | Y | Y | Y | - |
| log.view | Y | Y | Y | Y | - |
| file.manage | Y | Y | Y | - | Y |
| ssh.manage | Y | Y | - | - | - |
| process.view | Y | Y | Y | Y | - |
| process.manage | Y | - | - | - | - |
| notification.manage | Y | Y | - | - | - |
| audit.view | Y | Y | - | Y | - |
| config.manage | Y | - | - | - | - |
| maintenance.manage | Y | Y | - | - | - |
| deploy.manage | Y | Y | Y | - | - |
| deploy.view | Y | Y | Y | Y | - |

### Custom Permissions

When creating a user, you can override the default role permissions by specifying a custom `permissions` array. This allows fine-grained access control:

```json
{
  "email": "custom@example.com",
  "role": "developer",
  "permissions": ["domain.view", "app.deploy", "app.manage", "app.view", "log.view", "backup.create"]
}
```

---

## 25. Client (cPanel) Features

Customers access the **Client Panel** at `/cpanel/*`. It provides a scoped, self-service subset of vendor features.

> Only users with role `customer` can access the cPanel routes. Other roles are redirected to `/whm/`. Both panels are served from the same domain.

### Feature Comparison: WHM vs cPanel

| Feature | WHM (Vendor) | cPanel (Client) |
|---------|:---:|:---:|
| Create domains | Y | - |
| Delete domains | Y | - |
| View own domains | Y | Y |
| Domain stats | Y | Y (own domains) |
| Manage subdomains | Y | Y (own domains) |
| Manage aliases | Y | Y (own domains) |
| Manage redirects | Y | Y (own domains) |
| Deploy apps | Y | Y (own domains) |
| Start / Stop / Restart apps | Y | - |
| View app logs | Y | Y (own apps) |
| Create databases | Y | Y (own domains) |
| Delete databases | Y | Y (own databases) |
| Create mailboxes | Y | Y (own domains) |
| Manage forwarders | Y | Y (own domains) |
| Manage autoresponders | Y | Y (own domains) |
| Issue SSL | Y | Y (own domains) |
| View SSL info | Y | Y |
| Create DNS zones | Y | - |
| View DNS zones | Y | Y (own domains) |
| Create backups | Y | Y (own domains) |
| Schedule backups | Y | Y (own domains) |
| Restore backups | Y | - |
| Install WordPress | Y | Y (own domains) |
| WordPress security scan | Y | - |
| WordPress plugins/themes | Y | Y (own installs) |
| File manager | Y | Y (own files) |
| Cron jobs | Y | Y (own domain) |
| SSH keys | Y | - |
| System monitoring | Y | - |
| View logs | Y | - |
| Manage firewall | Y | - |
| Install software | Y | - |
| Manage users | Y | - |
| Process manager | Y | - |
| Resource usage | Y | Y (own domains) |
| Notification settings | Y | - |
| Activity log | Y | Y (own actions) |
| Server configuration | Y | - |
| Maintenance mode | Y | - |
| GitHub deploy | Y | Y (own domains) |
| Deploy logs | Y | Y (own deploys) |
| Rollback deploy | Y | - |

### Key Client Panel Restrictions

- **Domain ownership enforcement** — Every operation checks that the authenticated customer owns the target domain.
- **No destructive operations** — Customers cannot delete domains, stop apps, or restore backups.
- **Read-only DNS** — Customers can view their DNS zones but cannot modify records.
- **No server access** — Customers cannot view system monitoring, logs, processes, or firewall.
- **No software management** — Customers cannot install, uninstall, or configure server software.
- **Rate limit** — 100 requests/minute (vs 200/min for WHM).
- **File size limit** — 500 MB per upload (same as WHM).
- **Scoped audit log** — Customers can only see their own actions.

---

## 26. Project Structure & Tech Stack

### Tech Stack

| Layer | Technology | Details |
|-------|-----------|---------|
| **Backend** | Go 1.22+ | Primary language for all services |
| **Web Framework** | Fiber v2 | Fast HTTP framework built on fasthttp |
| **Database** | MongoDB 7.0+ | Primary data store (document-based) |
| **MongoDB URI** | `mongodb+srv://betazeninfotech:BetaZen2023@cluster0.odayp11.mongodb.net/` | Atlas cluster connection |
| **ODM** | go.mongodb.org/mongo-driver | Official MongoDB Go driver |
| **Auth** | JWT (golang-jwt/jwt/v5) | Access + refresh token pair |
| **Frontend** | React 18 (Vite SPA) | Single-page application per panel |
| **Styling** | Tailwind CSS 3 | Utility-first CSS framework |
| **Build Tool** | Vite 5 | Frontend dev server & bundler |
| **Monorepo** | Turborepo | Shared packages across frontend apps |
| **Container** | Docker + docker-compose | Local dev & production deployment |
| **Hot Reload** | Air (backend), Vite HMR (frontend) | Development experience |

### Monorepo Root

```
whm-cPanel-management/
├── backend/                   # Go backend (all 3 services)
├── frontend/                  # React frontend (monorepo with Turborepo)
├── docker-compose.yml         # Dev & production compose file
├── Makefile                   # Common commands (build, dev, deploy)
├── .env.example               # Environment variable template
├── .gitignore                 # Git ignore rules
└── FEATURES_VENDOR_WHM.md     # This document
```

### Backend Structure (Go + Fiber)

```
backend/
├── cmd/
│   ├── server/                # Single panel server (WHM + cPanel on :443)
│   │   └── main.go
│   └── agent/                 # Agent service entry point (:8443, runs on VPS)
│       └── main.go
├── internal/
│   ├── config/                # App configuration (env loading)
│   │   └── config.go
│   ├── database/              # MongoDB connection, collections, indexes
│   │   ├── mongo.go           # Connection pool & client setup
│   │   ├── collections.go     # Collection name constants
│   │   └── indexes.go         # Index definitions per collection
│   ├── models/                # MongoDB document structs
│   │   ├── user.go
│   │   ├── domain.go
│   │   ├── app.go
│   │   ├── database.go
│   │   ├── email.go
│   │   ├── dns.go
│   │   ├── ssl.go
│   │   ├── backup.go
│   │   ├── wordpress.go
│   │   ├── firewall.go
│   │   ├── cron.go
│   │   ├── ssh_key.go
│   │   ├── notification.go
│   │   ├── audit_log.go
│   │   ├── server_config.go
│   │   └── deployment.go
│   ├── handlers/              # HTTP request handlers (Fiber handlers)
│   │   ├── auth_handler.go
│   │   ├── domain_handler.go
│   │   ├── app_handler.go
│   │   ├── database_handler.go
│   │   ├── email_handler.go
│   │   ├── dns_handler.go
│   │   ├── ssl_handler.go
│   │   ├── backup_handler.go
│   │   ├── wordpress_handler.go
│   │   ├── firewall_handler.go
│   │   ├── software_handler.go
│   │   ├── monitoring_handler.go
│   │   ├── log_handler.go
│   │   ├── cron_handler.go
│   │   ├── file_handler.go
│   │   ├── ssh_key_handler.go
│   │   ├── process_handler.go
│   │   ├── resource_handler.go
│   │   ├── notification_handler.go
│   │   ├── audit_handler.go
│   │   ├── config_handler.go
│   │   ├── maintenance_handler.go
│   │   └── deploy_handler.go
│   ├── services/              # Business logic layer
│   │   ├── auth_service.go
│   │   ├── domain_service.go
│   │   ├── app_service.go
│   │   ├── database_service.go
│   │   ├── email_service.go
│   │   ├── dns_service.go
│   │   ├── ssl_service.go
│   │   ├── backup_service.go
│   │   ├── wordpress_service.go
│   │   ├── firewall_service.go
│   │   ├── software_service.go
│   │   ├── monitoring_service.go
│   │   ├── log_service.go
│   │   ├── cron_service.go
│   │   ├── file_service.go
│   │   ├── ssh_key_service.go
│   │   ├── process_service.go
│   │   ├── resource_service.go
│   │   ├── notification_service.go
│   │   ├── audit_service.go
│   │   ├── config_service.go
│   │   ├── maintenance_service.go
│   │   └── deploy_service.go
│   ├── middleware/             # Fiber middleware
│   │   ├── auth.go            # JWT verification & extraction
│   │   ├── rbac.go            # Role & permission checks
│   │   ├── rate_limiter.go    # Per-panel rate limiting
│   │   ├── logger.go          # Request/response logging
│   │   └── cors.go            # CORS configuration
│   ├── routes/                # Route registration
│   │   ├── auth_routes.go     # Shared auth routes (/api/v1/auth/*)
│   │   ├── whm_routes.go      # WHM API routes (/api/v1/whm/*)
│   │   ├── cpanel_routes.go   # cPanel API routes (/api/v1/cpanel/*)
│   │   └── agent_routes.go    # Agent routes (:8443)
│   └── agent/                 # Agent — Linux command executors
│       ├── executor.go        # Safe command runner (os/exec wrapper)
│       ├── nginx.go           # Nginx config generation & reload
│       ├── php.go             # PHP-FPM pool management
│       ├── mongodb.go         # MongoDB user/db provisioning
│       ├── postfix.go         # Postfix/Dovecot email management
│       ├── dns.go             # PowerDNS zone management
│       ├── certbot.go         # Let's Encrypt SSL operations
│       ├── firewall.go        # UFW / fail2ban management
│       ├── backup.go          # mongodump/tar/gzip operations
│       ├── wordpress.go       # WP-CLI operations
│       ├── deploy.go          # Git clone, build, symlink, process management
│       └── system.go          # systemctl, apt-get, cron, etc.
├── pkg/                       # Shared (importable) packages
│   ├── logger/                # Structured logger (zerolog)
│   │   └── logger.go
│   ├── jwt/                   # JWT creation & validation helpers
│   │   └── jwt.go
│   ├── password/              # Bcrypt hash & verify
│   │   └── password.go
│   ├── validator/             # Request validation helpers
│   │   └── validator.go
│   ├── response/              # Standard API response builders
│   │   └── response.go
│   └── constants/             # App-wide constants & enums
│       └── constants.go
├── go.mod
├── go.sum
└── .air.toml                  # Air hot-reload config for development
```

### Frontend Structure (React + Vite + Tailwind)

```
frontend/
├── turbo.json                 # Turborepo pipeline config
├── package.json               # Root workspace package.json
├── apps/
│   ├── whm/                   # WHM Panel SPA (served at /whm/*)
│   │   ├── index.html
│   │   ├── package.json
│   │   ├── vite.config.ts
│   │   ├── tailwind.config.ts
│   │   ├── postcss.config.js
│   │   ├── tsconfig.json
│   │   └── src/
│   │       ├── main.tsx           # React entry point
│   │       ├── App.tsx            # Root component + router
│   │       ├── index.css          # Tailwind imports
│   │       ├── routes/            # Page-level route components
│   │       │   ├── Dashboard.tsx
│   │       │   ├── Domains.tsx
│   │       │   ├── Apps.tsx
│   │       │   ├── Databases.tsx
│   │       │   ├── Email.tsx
│   │       │   ├── DNS.tsx
│   │       │   ├── SSL.tsx
│   │       │   ├── Backups.tsx
│   │       │   ├── WordPress.tsx
│   │       │   ├── Firewall.tsx
│   │       │   ├── Software.tsx
│   │       │   ├── Monitoring.tsx
│   │       │   ├── Logs.tsx
│   │       │   ├── CronJobs.tsx
│   │       │   ├── FileManager.tsx
│   │       │   ├── SSHKeys.tsx
│   │       │   ├── Processes.tsx
│   │       │   ├── Resources.tsx
│   │       │   ├── Users.tsx
│   │       │   ├── Deployments.tsx
│   │       │   ├── Settings.tsx
│   │       │   └── Login.tsx
│   │       ├── components/        # Reusable UI components
│   │       ├── hooks/             # Custom React hooks
│   │       ├── store/             # State management (Zustand)
│   │       └── lib/               # Utility functions
│   └── cpanel/                # cPanel Panel SPA (served at /cpanel/*)
│       ├── index.html
│       ├── package.json
│       ├── vite.config.ts
│       ├── tailwind.config.ts
│       ├── postcss.config.js
│       ├── tsconfig.json
│       └── src/
│           ├── main.tsx
│           ├── App.tsx
│           ├── index.css
│           ├── routes/            # Scoped customer pages
│           │   ├── Dashboard.tsx
│           │   ├── Domains.tsx
│           │   ├── Apps.tsx
│           │   ├── Databases.tsx
│           │   ├── Email.tsx
│           │   ├── SSL.tsx
│           │   ├── Backups.tsx
│           │   ├── WordPress.tsx
│           │   ├── FileManager.tsx
│           │   ├── CronJobs.tsx
│           │   ├── Deployments.tsx
│           │   ├── Resources.tsx
│           │   └── Login.tsx
│           ├── components/
│           ├── hooks/
│           ├── store/
│           └── lib/
└── packages/                  # Shared packages across both apps
    ├── ui/                    # Shared UI component library
    │   ├── package.json
    │   └── src/
    │       ├── Button.tsx
    │       ├── Modal.tsx
    │       ├── Table.tsx
    │       ├── Card.tsx
    │       ├── Sidebar.tsx
    │       ├── TopBar.tsx
    │       ├── StatusBadge.tsx
    │       ├── CodeBlock.tsx
    │       └── index.ts       # Barrel export
    ├── api-client/            # Typed API client (Axios + types)
    │   ├── package.json
    │   └── src/
    │       ├── client.ts      # Axios instance with interceptors
    │       ├── auth.ts        # Auth API methods
    │       ├── domains.ts     # Domain API methods
    │       ├── apps.ts
    │       ├── databases.ts
    │       ├── email.ts
    │       ├── dns.ts
    │       ├── ssl.ts
    │       ├── backups.ts
    │       ├── wordpress.ts
    │       ├── firewall.ts
    │       ├── monitoring.ts
    │       ├── deploy.ts
    │       └── index.ts
    ├── types/                 # Shared TypeScript type definitions
    │   ├── package.json
    │   └── src/
    │       ├── user.ts
    │       ├── domain.ts
    │       ├── app.ts
    │       ├── database.ts
    │       ├── email.ts
    │       ├── dns.ts
    │       ├── ssl.ts
    │       ├── backup.ts
    │       ├── wordpress.ts
    │       ├── deployment.ts
    │       ├── api.ts         # Standard response/error types
    │       └── index.ts
    └── tailwind-config/       # Shared Tailwind preset
        ├── package.json
        └── tailwind.preset.ts # Shared colors, fonts, spacing
```

### Frontend Serving — Single Domain

Both frontend apps are built as static files and served by a **single Go binary** on **one domain** using path-based routing:

```
# Build step (during deployment)
cd frontend && npx turbo run build

# Output directories (both served by the same Go process)
frontend/apps/whm/dist/        →  Served at /whm/*
frontend/apps/cpanel/dist/     →  Served at /cpanel/*
```

**How it works:**

1. `vite build` compiles each React app into static HTML/JS/CSS in the `dist/` folder.
2. The single Go Fiber server mounts both SPAs under different path prefixes.
3. API routes are grouped: `/api/v1/whm/*` for vendor, `/api/v1/cpanel/*` for customer, `/api/v1/auth/*` shared.
4. SPA fallback routes return the correct `index.html` for each panel.
5. Hitting the root `/` checks the JWT cookie/header and redirects to `/whm/` or `/cpanel/` based on role.
6. Access the panel at `https://panel.betazeninfotech.com` — one domain, one SSL cert, one server.

```go
// Single Go binary serves both panels + all APIs
app := fiber.New()

// --- Shared auth routes ---
auth := app.Group("/api/v1/auth")
auth.Post("/login", authHandler.Login)
auth.Post("/register", authHandler.Register)
auth.Post("/refresh", authHandler.Refresh)
auth.Post("/2fa/verify", authHandler.Verify2FA)

// --- WHM API routes (vendor) ---
whm := app.Group("/api/v1/whm", middleware.Auth(), middleware.RequireRole("vendor_owner", "vendor_admin", "developer", "support"))
whm.Get("/domains", domainHandler.List)
whm.Post("/domains", domainHandler.Create)
whm.Get("/monitor/metrics", monitorHandler.Metrics)
// ... all other WHM API routes

// --- cPanel API routes (customer) ---
cpanel := app.Group("/api/v1/cpanel", middleware.Auth(), middleware.RequireRole("customer"))
cpanel.Get("/domains", domainHandler.ListOwn)
cpanel.Get("/apps", appHandler.ListOwn)
// ... all other cPanel API routes

// --- Serve WHM React SPA ---
app.Static("/whm", "./frontend/apps/whm/dist")
app.Get("/whm/*", func(c *fiber.Ctx) error {
    return c.SendFile("./frontend/apps/whm/dist/index.html")
})

// --- Serve cPanel React SPA ---
app.Static("/cpanel", "./frontend/apps/cpanel/dist")
app.Get("/cpanel/*", func(c *fiber.Ctx) error {
    return c.SendFile("./frontend/apps/cpanel/dist/index.html")
})

// --- Root redirect based on role ---
app.Get("/", middleware.OptionalAuth(), func(c *fiber.Ctx) error {
    if role := c.Locals("role"); role == "customer" {
        return c.Redirect("/cpanel/")
    }
    return c.Redirect("/whm/")
})

app.Listen(":443")
```

### Vite Base Path Configuration

Each frontend app must set its `base` path in `vite.config.ts` so assets load correctly:

```ts
// frontend/apps/whm/vite.config.ts
export default defineConfig({
  base: '/whm/',
  // ...
})

// frontend/apps/cpanel/vite.config.ts
export default defineConfig({
  base: '/cpanel/',
  // ...
})
```

React Router also uses the same base path:

```tsx
// WHM app
<BrowserRouter basename="/whm">

// cPanel app
<BrowserRouter basename="/cpanel">
```

### Key Backend Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/gofiber/fiber/v2` | Web framework |
| `go.mongodb.org/mongo-driver` | MongoDB driver |
| `github.com/golang-jwt/jwt/v5` | JWT token handling |
| `golang.org/x/crypto/bcrypt` | Password hashing |
| `github.com/rs/zerolog` | Structured logging |
| `github.com/go-playground/validator/v10` | Struct validation |
| `github.com/joho/godotenv` | .env file loading |
| `github.com/pquerna/otp` | TOTP 2FA support |
| `github.com/robfig/cron/v3` | Cron scheduling |

### Key Frontend Dependencies

| Package | Purpose |
|---------|---------|
| `react` + `react-dom` | UI library |
| `react-router-dom` | Client-side routing |
| `axios` | HTTP client for API calls |
| `zustand` | Lightweight state management |
| `tailwindcss` | Utility-first CSS |
| `@tanstack/react-query` | Server state & caching |
| `react-hook-form` | Form handling |
| `lucide-react` | Icon library |
| `recharts` | Charts for monitoring dashboards |
| `@xterm/xterm` | Terminal emulator (SSH/console) |
| `monaco-editor` | Code editor (file manager) |

### Environment Variables

```env
# MongoDB
MONGO_URI=mongodb+srv://betazeninfotech:BetaZen2023@cluster0.odayp11.mongodb.net/
MONGO_DB_NAME=serverpanel

# JWT
JWT_SECRET=your-secret-key
JWT_ACCESS_EXPIRY=15m
JWT_REFRESH_EXPIRY=7d

# Server (single domain)
DOMAIN=panel.betazeninfotech.com
SERVER_PORT=443
TLS_CERT=/etc/letsencrypt/live/panel.betazeninfotech.com/fullchain.pem
TLS_KEY=/etc/letsencrypt/live/panel.betazeninfotech.com/privkey.pem

# Agent (runs on each managed VPS)
AGENT_PORT=8443
AGENT_API_KEY=your-agent-api-key
AGENT_TLS_CERT=/etc/serverpanel/agent.crt
AGENT_TLS_KEY=/etc/serverpanel/agent.key

# GitHub Deployment
GITHUB_CLIENT_ID=your-github-oauth-app-id
GITHUB_CLIENT_SECRET=your-github-oauth-app-secret
GITHUB_WEBHOOK_SECRET=your-webhook-secret

# Email (Postfix)
MAIL_HOSTNAME=mail.example.com

# Backup
BACKUP_DIR=/var/backups/serverpanel
BACKUP_ENCRYPTION_KEY=your-encryption-key

# App
APP_ENV=production
LOG_LEVEL=info
```

---

## Architecture Overview

### Single-Domain Deployment

Both the WHM (vendor) and cPanel (client) panels are served from a **single Go binary** on a **single domain**. Path-based routing separates the two panels. The Agent runs separately on each managed VPS.

```
                    +------------------------------------------+
                    |            SaaS API (:8080)               |
                    |    Multi-tenant vendor/license mgmt       |
                    +-------------------+----------------------+
                                        | Manages
                                        v
+-------------------------------------------------------------------+
|              panel.betazeninfotech.com (:443)                           |
|              Single Go binary — one domain, one server             |
|                                                                    |
|   /whm/*          → WHM React SPA (vendor admin)                  |
|   /cpanel/*        → cPanel React SPA (customer self-serve)       |
|   /api/v1/whm/*    → WHM API endpoints (full server admin)        |
|   /api/v1/cpanel/* → cPanel API endpoints (scoped customer API)   |
|   /api/v1/auth/*   → Shared auth endpoints (login, refresh, 2FA)  |
+-------------------------------------------------------------------+
                                        |
                                        | API calls (HTTPS)
                                        v
                          +---------------------+
                          |   Agent (:8443)     |
                          | Runs on each VPS    |
                          | Executes all real   |
                          | system operations   |
                          +----------+----------+
                                     |
                          +----------+----------+
                          |     Linux Server    |
                          |  nginx, php-fpm,    |
                          |  mongodb, postfix,  |
                          |  dovecot, ufw,      |
                          |  pdns, fail2ban,    |
                          |  certbot, etc.      |
                          +---------------------+
```

**How it works:**
1. A **single Go Fiber server** runs on port 443 (HTTPS) behind your domain, serving both panels.
2. **Path-based routing** separates WHM (`/whm/*`, `/api/v1/whm/*`) and cPanel (`/cpanel/*`, `/api/v1/cpanel/*`).
3. **JWT authentication** determines the user's role — vendors see WHM routes, customers see cPanel routes.
4. Both panels delegate all actual server operations to the **Agent** via authenticated HTTPS calls.
5. The **Agent** runs on each managed VPS and executes real Linux commands (systemctl, apt-get, useradd, nginx, certbot, etc.).
6. The **SaaS API** sits above everything, managing vendors, licenses, servers, and billing plans.

### URL Routing Map

| URL Pattern | What It Serves |
|-------------|----------------|
| `https://panel.betazeninfotech.com/` | Redirect to `/whm/` or `/cpanel/` based on role |
| `https://panel.betazeninfotech.com/whm/` | WHM React SPA (vendor admin dashboard) |
| `https://panel.betazeninfotech.com/whm/*` | WHM SPA client-side routes |
| `https://panel.betazeninfotech.com/cpanel/` | cPanel React SPA (customer dashboard) |
| `https://panel.betazeninfotech.com/cpanel/*` | cPanel SPA client-side routes |
| `https://panel.betazeninfotech.com/api/v1/auth/*` | Shared auth API (login, register, 2FA, refresh) |
| `https://panel.betazeninfotech.com/api/v1/whm/*` | WHM API (all vendor endpoints) |
| `https://panel.betazeninfotech.com/api/v1/cpanel/*` | cPanel API (scoped customer endpoints) |

### Communication Flow

```
Browser → panel.betazeninfotech.com (:443) → Agent (:8443) → Linux Server
           (single Go binary)           (API key auth)    (system commands)
           (JWT auth + path routing)
```

- **Single domain:** One DNS record, one SSL certificate, one Go process.
- **Panel <-> Agent:** All communication uses HTTPS with mutual TLS or API key authentication.
- **Agent security:** The agent only accepts connections from its registered panel IP.
- **Command execution:** The agent uses Go's `os/exec` to run system commands — no shell injection possible.
- **Role-based redirect:** Hitting `/` checks JWT role and redirects — `vendor_*` roles go to `/whm/`, `customer` role goes to `/cpanel/`.

---

## API Base URLs

| Service | Default URL |
|---------|-------------|
| SaaS API | `https://your-cloud-server:8080/api/v1/` |
| Shared Auth | `https://panel.betazeninfotech.com/api/v1/auth/` |
| Vendor WHM API | `https://panel.betazeninfotech.com/api/v1/whm/` |
| Client cPanel API | `https://panel.betazeninfotech.com/api/v1/cpanel/` |
| WHM Frontend | `https://panel.betazeninfotech.com/whm/` |
| cPanel Frontend | `https://panel.betazeninfotech.com/cpanel/` |
| Agent (internal, per VPS) | `https://your-vps:8443/api/v1/` |

---

*ServerPanel — A complete hosting control panel built with Go, Fiber, MongoDB, React, and Tailwind CSS.*
