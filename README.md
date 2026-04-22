<div align="center">

# Betazen Server Panel

**A modern, self-hosted WHM / cPanel-style server-management platform by [BetaZen InfoTech](https://betazeninfotech.com).**

[![Version](https://img.shields.io/badge/version-1.0.1-blue)](./backend/pkg/version/version.go)
[![License](https://img.shields.io/badge/license-BetaZen%20Source--Available%20v1.0-orange)](./LICENSE)
[![Platform](https://img.shields.io/badge/platform-Ubuntu%2022.04%20%2F%2024.04-E95420)](#2-system-requirements)
[![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8)](https://go.dev)
[![Node](https://img.shields.io/badge/Node-18%20%7C%2020%20%7C%2022-339933)](https://nodejs.org)
[![MongoDB](https://img.shields.io/badge/MongoDB-7.0%2B-47A248)](https://www.mongodb.com)
[![Security](https://img.shields.io/badge/security-coordinated%20disclosure-critical)](./SECURITY.md)

[Features](#3-what-you-get) · [Install](#5-installation) · [Upgrade](#8-upgrading) · [API](#11-api-reference) · [Contributing](./CONTRIBUTING.md) · [Security](./SECURITY.md) · [License](./LICENSE)

</div>

---

> **Copyright (c) 2024-2026 BetaZen InfoTech. All rights reserved.**
> Distributed under the **[BetaZen InfoTech Source-Available License v1.0](./LICENSE)** — source is open for audit, self-hosting, and contribution, but commercial redistribution and competing hosted services require a separate written agreement. See Section 15 below for the full terms summary.

---

## Table of contents

1. [Overview](#1-overview)
2. [System requirements](#2-system-requirements)
3. [What you get](#3-what-you-get)
4. [Architecture](#4-architecture)
5. [Installation](#5-installation)
    - 5.1 [One-line install (recommended)](#51-one-line-install-recommended)
    - 5.2 [Manual install from a clone](#52-manual-install-from-a-clone)
    - 5.3 [What the installer actually does](#53-what-the-installer-actually-does)
    - 5.4 [Post-install verification](#54-post-install-verification)
    - 5.5 [Hardening checklist (do this before going live)](#55-hardening-checklist-do-this-before-going-live)
6. [First login](#6-first-login)
7. [Development setup](#7-development-setup)
8. [Upgrading](#8-upgrading)
9. [Migrating an existing VPS into Betazen Server Panel](#9-migrating-an-existing-vps-into-betazen-server-panel)
10. [Common commands](#10-common-commands)
11. [API reference](#11-api-reference)
12. [Environment variables](#12-environment-variables)
13. [Docker](#13-docker)
14. [Troubleshooting](#14-troubleshooting)
15. [License, copyright & trademarks (MUST READ)](#15-license-copyright--trademarks-must-read)
16. [Contributing](#16-contributing)
17. [Security disclosure](#17-security-disclosure)
18. [Project governance & support](#18-project-governance--support)
19. [Trademarks & legal disclaimers](#19-trademarks--legal-disclaimers)

---

## 1. Overview

Betazen Server Panel ships **two Single-Page Applications** (SPAs) — a WHM panel for platform owners and a User Panel for vendors / staff / customers — served from a single Go backend, plus a lightweight **agent daemon** that can manage remote VPS instances over mTLS. It replaces the WHM/cPanel stack with a modern, auditable, lightweight alternative that hosting providers and in-house teams can self-host on their own infrastructure.

**Who it is for:**

- Hosting resellers who want to run their own control panel instead of paying cPanel per-account licensing.
- In-house IT teams that manage a fleet of Linux VPSes and want role-based delegation.
- Agencies hosting their clients' sites who want a white-labelled self-service portal.
- Developers who want a self-hosted "deploy a GitHub repo, auto-issue SSL, auto-configure nginx" workflow.

**Who it is NOT for:**

- Someone who wants a shrink-wrapped shared-hosting business without reading any documentation.
- Someone who wants to re-skin it and resell it as their own SaaS control panel — that is explicitly forbidden by the [LICENSE](./LICENSE) without a separate commercial agreement.

---

## 2. System requirements

### 2.1 Supported operating systems

| OS | Version | Support |
|---|---|---|
| Ubuntu Server | 22.04 LTS | ✅ Primary (all installer paths tested on each release) |
| Ubuntu Server | 24.04 LTS | ✅ Primary |
| Debian | 12 (bookworm) | ⚠️ Best-effort; may require minor install-script tweaks |
| RHEL / Rocky / AlmaLinux | 9.x | ❌ Not supported (install.sh is apt-based) |
| macOS / Windows | any | 🧪 Development only (via `make dev`); never for production |

### 2.2 Hardware minimums

| Profile | CPU | RAM | Disk | Use-case |
|---|---|---|---|---|
| Evaluation | 1 vCPU | 2 GB | 20 GB SSD | Kick the tyres, no real tenants. |
| Small production | 2 vCPU | 4 GB | 40 GB SSD | Up to ~20 hosted sites, light mail traffic. |
| Standard production | 4 vCPU | 8 GB | 80 GB SSD | Up to ~100 hosted sites, full mail stack, DNS master. |
| Heavy | 8+ vCPU | 16+ GB | 160+ GB NVMe | Larger fleets; put MongoDB on its own host, Postfix queues on their own volume. |

### 2.3 Network prerequisites

- **A public IPv4** with a working reverse-DNS / PTR record (Postfix will reject outbound mail otherwise).
- **Ports to open inbound:** `80/tcp`, `443/tcp`, `22/tcp` (or your SSH port), plus `25/465/587/993/995/tcp` if you use the mail stack, `53/tcp+udp` if you use PowerDNS, and `20/21/tcp + 40000-50000/tcp` (passive FTP) if you use pure-ftpd.
- **Port 8443/tcp** is the agent mTLS channel — open it only between the panel server and the remote agents, **never** to the public internet.
- **A DNS A record** pointing at the server for the panel itself (e.g. `panel.example.com`). You can install before DNS is set up and add it later.

### 2.4 Software prerequisites (development only)

Only relevant if you want to run `make dev` locally — the VPS installer pulls everything it needs itself.

| Tool | Version |
|---|---|
| Go | 1.22 or newer |
| Node.js | 18 LTS, 20 LTS, or 22 LTS |
| npm | 10 or newer |
| MongoDB | 7.0 or newer (local or Atlas connection string) |
| GNU Make | 4 or newer |
| Docker + Docker Compose | optional, for the Docker dev path |

---

## 3. What you get

- **WHM panel** at `/whm/*` — platform owner & vendor/staff administration.
- **User Panel** at `/user-panel/*` — vendors, their team, and customers.
- **Server transfer wizard** — one-click migration between two Betazen Server Panel installs (domains, files, DNS, SSL, email, FTP, databases, Node apps, Deploy-Software projects, firewall rules, SSH keys, maintenance state).
- **Deploy Software** — GitHub-integrated project runner with framework presets (Next.js, Nuxt, static, Node API, Python, Go), per-service systemd units, and auto-reconciling nginx vhosts.
- **Apps** — PM2-managed Node app runtime, static-site hosting, reverse-proxy vhosts, automatic SSL.
- **Mail stack** — per-domain DKIM / SPF / DMARC, Roundcube webmail with SSO, mailbox quotas, virtual forwarders, SpamAssassin filtering.
- **DNS** — PowerDNS with a UI-driven zone/record editor, full CRUD for A / AAAA / MX / TXT / SRV / CNAME / CAA.
- **Backups** — scheduled, encrypted (AES-GCM), per-tenant, restore-preview before rolling forward.
- **Maintenance mode** — per-domain or server-wide, and state is preserved on transfer.
- **RBAC** — five roles (`vendor_owner`, `vendor_admin`, `vendor_staff`, `developer`, `support`, `customer`) with fine-grained permissions at the route level.
- **Self-service profile** — every user can manage their own name, email, password from the User Panel without bugging an admin.
- **Audit trail** — all privileged API actions are logged with actor, target, tenant, before/after state.

See [`FEATURES_VENDOR_WHM.md`](./FEATURES_VENDOR_WHM.md) for the full feature catalogue — every module, its API surface, and its UI screens.

---

## 4. Architecture

```
                    Single domain (panel.example.com)
        +------------------------------------------------------+
        |  /whm/*          - Owner panel (React SPA)           |
        |  /user-panel/*   - Vendor/tenant panel (React SPA)   |
        |  /api/v1/*       - Fiber v2 HTTP API                 |
        |  /webmail/*      - Roundcube (optional)              |
        +-----------------------+------------------------------+
                                |
                     nginx reverse proxy (systemd: nginx)
                                |
                     Go binary: /opt/serverpanel/bin/server
                     (systemd:  serverpanel.service)
                                |
                  +-------------+-------------+
                  |                           |
             MongoDB 7.0+                PM2 + systemd units
             (panel state)               (tenant apps)
                  |
                  v mTLS, port 8443, allow-listed to agent IPs
                  +--------------+
                  | Agent daemon |   runs on each managed VPS,
                  | (per VPS)    |   exposes a narrow RPC to the
                  +--------------+   panel for fs / systemd / nginx ops
```

Both SPAs are served by the same Go binary — there is no separate Node web server in production. The agent is an **optional** component; the all-in-one `install.sh` runs the panel, MongoDB, mail, DNS, and hosted tenant sites on one box (the typical small-provider setup).

| Tier | Technology |
|---|---|
| Backend | Go 1.22, Fiber v2, go-playground/validator, robfig/cron v3, Zerolog, bcrypt, golang-jwt/jwt v5 |
| Frontend | React 18, TypeScript 5, Vite 5, Tailwind CSS 3 (dark), Zustand, React Router v6, Recharts, Lucide, React Hot Toast |
| Monorepo | Turborepo 2.8.10 with npm workspaces |
| Database | MongoDB 7.0+ with `authSource=admin` |
| Agent comm | mTLS (client-cert pinned), port 8443 |
| Web server | nginx (reverse proxy + per-domain vhosts) |
| TLS | Let's Encrypt via certbot, webroot + auto-renew |
| Mail | Postfix + Dovecot + OpenDKIM + SpamAssassin |
| DNS | PowerDNS Authoritative with MySQL backend |
| FTP | pure-ftpd (virtual users) |
| Process supervision | systemd (panel + per-tenant-app units) + PM2 (Node apps) |
| CI/CD | GitHub Actions → VPS deploy |

---

## 5. Installation

Install on a **fresh Ubuntu 22.04 or 24.04 VPS**. The installer assumes it owns the box — it will install and reconfigure MongoDB, MariaDB, Postfix, Dovecot, nginx, PowerDNS, and ufw. Do not run it on a box that already has those services in production use.

### 5.1 One-line install (recommended)

```bash
curl -sSL https://raw.githubusercontent.com/BetaZen-InfoTech/server-management/main/install.sh | bash
```

Takes **5–8 minutes** on a 2 vCPU / 4 GB droplet with a good network. The installer is **idempotent** — re-running it pulls the latest `main`, rebuilds the binaries and SPAs, and restarts the service without touching MongoDB data or tenant state.

For fully reproducible installs (air-gapped, paranoid, or audit-first environments) use the two-step variant:

```bash
# 1. Download first, read, then run.
curl -sSL https://raw.githubusercontent.com/BetaZen-InfoTech/server-management/main/install.sh -o install.sh
less install.sh          # read the whole thing
sha256sum install.sh     # compare to the hash published on the release page
bash install.sh
```

### 5.2 Manual install from a clone

```bash
git clone https://github.com/BetaZen-InfoTech/server-management.git /opt/serverpanel
cd /opt/serverpanel
bash install.sh
```

This is the preferred path if you want to pin to a specific tag:

```bash
git clone https://github.com/BetaZen-InfoTech/server-management.git /opt/serverpanel
cd /opt/serverpanel
git checkout v1.0.1
bash install.sh
```

For a **fully manual**, hand-turned deployment (no install.sh at all — useful if you need to split MongoDB onto its own host, or if your environment forbids running an install script) follow [`DEPLOYMENT.md`](./DEPLOYMENT.md). It walks through every step the installer takes, in order, with the exact commands.

### 5.3 What the installer actually does

In order:

1. **Root / sudo check** — refuses to run as a non-root user.
2. **OS check** — refuses to run on non-Ubuntu / non-Debian or unsupported versions.
3. **System packages** — `apt-get install` for curl, git, build tools, certbot, nginx, Postfix, Dovecot, OpenDKIM, SpamAssassin, pure-ftpd, PowerDNS + PowerDNS-backend-MySQL, MariaDB, jailkit, ufw.
4. **MongoDB 7.0** — adds the MongoDB APT repo, installs, enables `mongod`, creates a local `admin` user, sets `authSource=admin`.
5. **PHP 8.2 + 8.4** — FPM pools, per-domain sockets under `/run/php/`.
6. **Node 18 / 20 / 22** — installed side-by-side via [`n`](https://github.com/tj/n); PM2 installed globally on the default version.
7. **Go 1.23** — installed to `/opt/go/1.23` (independent of any distro `golang-go` package).
8. **Panel binaries** — clones this repo to `/opt/serverpanel` (if not already there), runs `go build` against `cmd/server`, `cmd/agent`, `cmd/seed` into `/opt/serverpanel/bin/`.
9. **Panel frontends** — `npm install --legacy-peer-deps` + `npx turbo build` under `frontend/`; builds both the WHM and User Panel SPAs.
10. **systemd unit** — writes `/etc/systemd/system/serverpanel.service`, enables it, starts it.
11. **nginx panel vhost** — serves `/whm/*`, `/user-panel/*`, `/cpanel/*` (301 → user-panel), `/api/v1/*` on port 80 as the `default_server`.
12. **Roundcube webmail** at `/webmail/` with SSO from the panel (can be skipped if you don't need mail).
13. **Firewall** — ufw rules for HTTP(S), SSH, FTP data ports, mail ports (25/465/587/993/995).
14. **Seed admin** — creates `admin@betazeninfotech.com / admin123` if no owner exists yet.

The installer **does not**:

- touch an existing MongoDB if it detects one, unless you pass `--reinstall-mongo`;
- overwrite an existing `/opt/serverpanel/.env` — it will merge missing keys only;
- issue a Let's Encrypt certificate for the panel (do that yourself from the UI once DNS is pointed at the server — the installer doesn't know your domain at install time);
- uninstall or downgrade any system package.

### 5.4 Post-install verification

After the installer finishes, verify each layer in sequence:

```bash
# 1) Panel process is running
systemctl status serverpanel --no-pager | head -15

# 2) Panel is listening
ss -ltnp | grep -E ':80|:8080|:8443'

# 3) API responds
curl -s http://localhost:8080/api/v1/version | jq
# -> { "name": "Betazen Server Panel", "version": "1.0.1", ... }

# 4) Public HTTP answers
curl -sI http://<your-ip>/ | head -5
# -> HTTP/1.1 200 OK, Server: nginx, ...

# 5) Panel DB reachable
systemctl status mongod --no-pager | head -10
mongosh --quiet --eval 'db.adminCommand({ ping: 1 })' mongodb://127.0.0.1/?authSource=admin -u <admin> -p <pass>
```

If any of the five fail, check `journalctl -u serverpanel -n 80 --no-pager` first — it's the single best signal.

### 5.5 Hardening checklist (do this before going live)

- [ ] **Change the seeded admin password.** Log in, go to **Users → Admins**, rotate immediately.
- [ ] **Confirm `JWT_SECRET` and `APP_ENCRYPTION_KEY` are randomized** in `/opt/serverpanel/.env` (the installer randomizes these; `grep -E 'JWT_SECRET|APP_ENCRYPTION_KEY' /opt/serverpanel/.env` should show 64+ random characters, not a placeholder).
- [ ] **Lock SSH** — key-based auth only (`PasswordAuthentication no` in `/etc/ssh/sshd_config`), and ideally on a non-default port behind ufw allowlists.
- [ ] **Lock agent port 8443** to panel-server IP only (`ufw allow from <panel-ip> to any port 8443`).
- [ ] **Point DNS** at the server, set **Server Settings → Panel Domain** in the UI, then issue SSL from the **SSL** page.
- [ ] **Enable automated backups** under **Backups → Schedules**.
- [ ] **Subscribe to security releases** — Watch the GitHub repo (Releases only, at minimum). See [SECURITY.md](./SECURITY.md).

---

## 6. First login

1. Navigate to `http://<server-ip>/whm/login` (or `https://<your-domain>/whm/login` once SSL is issued).
2. Log in with the seeded credentials:
   - **Email:** `admin@betazeninfotech.com`
   - **Password:** `admin123`
3. **Change the password immediately** from **Users → Admins** (the password strength meter enforces ≥ 12 chars; use a password manager).
4. (Optional) Point a real domain at the server, set **Server Settings → Panel Domain**, then issue SSL from the **SSL** page.
5. Create your first vendor from **Vendors → New Vendor**. That vendor logs in at `/user-panel/login`, not `/whm/login` — the login surfaces are strictly split.

---

## 7. Development setup

```bash
# Prereqs: Go 1.22+, Node 18/20/22 LTS, MongoDB 7+, npm 10+, make 4+

git clone https://github.com/BetaZen-InfoTech/server-management.git
cd server-management

cp .env.example .env            # fill in MONGO_URI + JWT_SECRET at minimum
make setup                      # go mod download + npm install across workspaces
make dev                        # backend (Air hot-reload) + both SPAs (Vite)
```

Dev endpoints:

| Service | URL | Notes |
|---|---|---|
| Backend API | `http://localhost:8080` | Air auto-rebuilds on `backend/**` changes. |
| WHM SPA | `http://localhost:5173` | Proxies `/api/v1/*` → `:8080`. |
| User Panel SPA | `http://localhost:5174` | Proxies `/api/v1/*` → `:8080`. |

Tip: `make dev-backend` / `make dev-frontend` run them separately if you want cleaner logs.

---

## 8. Upgrading

### 8.1 Fast path (production)

```bash
cd /opt/serverpanel
sudo git fetch --quiet origin main
sudo git reset --hard origin/main
sudo /opt/go/1.23/bin/go -C backend build -o /opt/serverpanel/bin/server ./cmd/server
( cd /opt/serverpanel/frontend && sudo npx turbo build )
sudo systemctl restart serverpanel
```

Or simply re-run the installer — it is **idempotent** and performs the same pull-and-rebuild sequence with all dependency-version checks intact:

```bash
curl -sSL https://raw.githubusercontent.com/BetaZen-InfoTech/server-management/main/install.sh | bash
```

### 8.2 Safer staged upgrade (production, zero-downtime)

For a build-to-side, smoke-test, atomic-symlink-swap, rollback-ready upgrade, follow [`docs/server-panel-upgrade.md`](./docs/server-panel-upgrade.md). Use this for any upgrade that crosses a minor version or touches MongoDB indexes.

### 8.3 Downgrading / rollback

The panel data lives in MongoDB; binaries live on disk. To roll back to a previous release:

```bash
cd /opt/serverpanel
sudo git fetch --tags
sudo git checkout v<previous-tag>
sudo /opt/go/1.23/bin/go -C backend build -o /opt/serverpanel/bin/server ./cmd/server
( cd /opt/serverpanel/frontend && sudo npx turbo build )
sudo systemctl restart serverpanel
```

MongoDB schema changes are **forward-compatible** within a minor version; they are **not guaranteed** to be backward-compatible across minor versions. If you must downgrade across a minor version, take a `mongodump` first and restore it on the older panel.

---

## 9. Migrating an existing VPS into Betazen Server Panel

The built-in **server transfer wizard** moves an entire VPS worth of tenants (domains, files, DNS, SSL, email, FTP, databases, Node apps, Deploy-Software projects, firewall rules, SSH keys, maintenance state; and the source's DNS records get re-pointed at the destination IP) between two Betazen Server Panel installs in one click.

```
   +--- Source ---+                 +--- Destination --+
   |  .old-ip     |  -- SSH mTLS -> |  .new-ip         |
   |  old panel   |     tokenized   |  new panel       |
   +--------------+                 +------------------+
```

Step-by-step operator guide: [`docs/server-transfer.md`](./docs/server-transfer.md).

There is **no** general-purpose "import from cPanel / CyberPanel / Plesk" wizard at this time. If you are migrating off a non-Betazen panel, plan for per-service migration (mail with `imapsync`, DNS via AXFR, sites via rsync).

---

## 10. Common commands

```bash
# Development
make dev                 # backend (Air) + both SPAs (Vite)
make dev-backend         # backend only
make dev-frontend        # SPAs only

# Build
make build               # everything for production
make build-backend       # Go binaries: server + agent + seed
make build-frontend      # both SPAs via Turbo

# Docker (dev only — not a production deployment path)
make docker-up
make docker-down
make docker-build

# Quality
make lint                # golangci-lint + turbo lint
make test                # go test ./... + turbo test

# Setup & cleanup
make setup               # go mod download + npm install
make clean               # remove build artefacts
```

---

## 11. API reference

All runtime API routes live under `/api/v1/`. Authentication is `Authorization: Bearer <access_token>`, where the token is returned by `POST /api/v1/auth/login` with a JSON body `{ "email": "...", "password": "..." }`.

### 11.1 Route prefixes & audiences

| Prefix | Audience | Notes |
|---|---|---|
| `/api/v1/auth/*` | public | `login`, `refresh`, password-reset request/confirm. |
| `/api/v1/whm/*` | `vendor_owner` + staff with explicit perms | Server-level ops (software, maintenance, resources summary) stay behind the `server.manage` perm. |
| `/api/v1/cpanel/*` | `vendor_admin`, `vendor_staff`, `developer`, `support`, `customer` | Tenant-scoped; tenancy enforced in the service layer via `callerCtx(role, tenantID)`. |
| `/api/v1/agent/*` | agent-to-panel channel | **mTLS** with client-cert pinning, **not JWT**. Port 8443. |
| `/api/v1/version` | public | Product name + version triple. Safe to hit from uptime monitors. |

### 11.2 Response envelope

Every endpoint returns the same JSON envelope:

```json
{
  "success": true,
  "data":    { /* payload */ },
  "error":   null,
  "pagination": { "page": 1, "limit": 20, "total": 42, "total_pages": 3 }
}
```

On error:

```json
{
  "success": false,
  "data":    null,
  "error":   { "code": "VALIDATION_FAILED", "message": "...", "fields": {...} }
}
```

### 11.3 Pagination

List endpoints accept `?page=N&limit=M` (default `page=1`, `limit=20`, `limit` capped at 100). The response's `pagination` block is populated on list endpoints and omitted on single-resource responses.

### 11.4 JSON field naming

**Token fields use `snake_case`** (`access_token`, `refresh_token`) — this is intentional and matches the [RFC 6749](https://datatracker.ietf.org/doc/html/rfc6749) OAuth 2.0 convention. Other domain fields generally use `camelCase`. Don't mix conventions inside a single payload.

---

## 12. Environment variables

Panel config lives in `/opt/serverpanel/.env` on the server, generated by the installer. The reference template is [`.env.example`](./.env.example).

| Variable | Purpose |
|---|---|
| `SERVER_IP` | Public IPv4 used for DNS A records, SPF, and the panel's own reverse-proxy source IP. |
| `DOMAIN` | Hostname where the panel is served (used for SSO, cookies, cert SAN). |
| `MONGO_URI` | MongoDB connection string; **must** include `authSource=admin`. |
| `JWT_SECRET` | Signing secret for access + refresh tokens. Rotating invalidates all existing sessions. |
| `APP_ENCRYPTION_KEY` | AES-GCM key for Deploy Software PATs and mailbox SSO tokens. **Never lose this** — encrypted data becomes unrecoverable. |
| `SSL_EMAIL` | Let's Encrypt ACME account email. |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASS` | Outbound mail relay for panel-originated mail (password reset, notifications). Separate from tenant mail. |
| `LOG_LEVEL` | `debug` / `info` / `warn` / `error`. Default `info`. |
| `PORT` | Backend HTTP port. Default `8080`. Don't change unless you also update nginx. |

The `.env` file should be readable **only** by `root` and the `serverpanel` system user. The installer `chmod 600` it.

---

## 13. Docker

A `docker-compose.yml` is included for **local development and testing only**. It is **not recommended for production** — the panel's install integrates deeply with systemd, nginx, Postfix, Dovecot, PowerDNS, and ufw, and those don't run meaningfully inside a container. Use `install.sh` on a real VPS for production.

```bash
make docker-up      # builds + starts containers
make docker-down    # stops + removes containers
make docker-build   # rebuilds images without starting
```

---

## 14. Troubleshooting

| Symptom | First thing to check |
|---|---|
| Panel returns **502 Bad Gateway** | `systemctl status serverpanel` + `journalctl -u serverpanel -n 80 --no-pager` |
| Login returns **401** with known-good credentials | `JWT_SECRET` changed since the browser last logged in. Hard-refresh or log in again. |
| Domain serves **"Welcome to your new website!"** over HTTPS | SSL cert exists but the vhost was written without `listen 443 ssl`. Re-run SSL from the panel's **SSL** page. |
| Mail bounces with **SPF fail** | Public DNS hasn't propagated. `dig @8.8.8.8 <domain> TXT` and compare to `pdnsutil list-zone <domain>`. |
| After a transfer, a project's domain doesn't serve | `ls /etc/nginx/sites-enabled/<domain>` (symlink must exist) + `systemctl status sp-proj-<slug>-<svc>` must be active. |
| MongoDB connection refused at startup | `MONGO_URI` missing `authSource=admin`, or the `mongod` service is down. `systemctl status mongod`. |
| Agent doesn't register | Port 8443 firewall rule + mTLS cert mismatch. Re-issue agent creds from **Servers → Agents → Regenerate**. |
| Frontend loads but all API calls **401** | Token expired and refresh-token logic tripped. Log out, log back in. If it recurs, check server time (`timedatectl`). |

More scenarios in [`docs/server-transfer.md`](./docs/server-transfer.md) under **Troubleshooting**, and in [`DEPLOYMENT.md`](./DEPLOYMENT.md) § 16.

---

## 15. License, copyright & trademarks (MUST READ)

> **Copyright © 2024-2026 BetaZen InfoTech. All rights reserved.**

Betazen Server Panel is published as **source-available** software under the **[BetaZen InfoTech Source-Available License v1.0](./LICENSE)** (the "License"). This is not an OSI-approved open-source license — it intentionally imposes restrictions on commercial redistribution and competing hosted services. Read the full text in [`LICENSE`](./LICENSE) before using the software in any commercial context.

### TL;DR — what you CAN do

- ✅ **Read, audit, and study** the source code.
- ✅ **Self-host** the panel, for free, to run **your own** hosting business, agency, or in-house infra — on as many servers as you like.
- ✅ **Modify** the code for your own internal use.
- ✅ **Contribute patches** back under the Contributor License Agreement described in [CONTRIBUTING.md § 3](./CONTRIBUTING.md#3-contributor-license-agreement-cla).
- ✅ **Redistribute unmodified copies** that include this LICENSE and NOTICE file intact, provided you are not operating a *Competing Service*.
- ✅ **Use the older releases under AGPL-3.0** once a given release hits its Change Date (four years after its public release, per [LICENSE § 4](./LICENSE)).

### TL;DR — what you CANNOT do (without a signed commercial license)

- ❌ **Offer Betazen Server Panel (or a derivative of it) to third parties as a paid, metered, or subscription-gated control-panel / VPS-management SaaS.** This is a *Competing Service* under [LICENSE § 1.6](./LICENSE) and is prohibited by [§ 3.1](./LICENSE).
- ❌ **Strip, rename, or replace the BetaZen / Betazen Server Panel branding** and then redistribute or offer that rebranded build to third parties ([§ 3.2](./LICENSE)).
- ❌ **Remove, disable, or circumvent** any license-enforcement, feature-gating, or tamper-detection mechanism ([§ 3.3](./LICENSE)).
- ❌ **Use the source code to train a generative-AI model** offered to third parties ([§ 3.4](./LICENSE)).
- ❌ **Use the BetaZen trademarks** — the name, wordmark, or logo — in the branding of a fork, SaaS, or company, outside the narrow nominative-use carve-out in [LICENSE § 7](./LICENSE).
- ❌ **Use the software for illegal content, spam-at-scale, CSAM, terrorism-facilitation,** or any use violating applicable export-control law ([§ 3.5, § 3.6](./LICENSE)).
- ❌ **Assert a patent** against BetaZen InfoTech or any contributor alleging the software infringes — doing so immediately terminates your rights ([§ 3.7](./LICENSE)).

### Commercial / OEM / resale licensing

If any of the "cannot" items apply to your intended use, we're happy to talk:

- 📧 **licensing@betazeninfotech.com**
- We offer OEM, white-label, and SaaS-operator licenses on commercially reasonable terms. The existence of the LICENSE above is there precisely to fund the work — not to prevent it.

### Governing law & jurisdiction

The License is governed by the laws of the **Republic of India**, with exclusive jurisdiction in the competent courts at **Kolkata, West Bengal, India** ([LICENSE § 9](./LICENSE)).

### Copyright notice in derived files

If you add a significant new file to the repository, please prepend the standard copyright header:

```
/*
 * Copyright (c) 2024-2026 BetaZen InfoTech and the Betazen Server Panel
 * contributors. Licensed under the BetaZen InfoTech Source-Available
 * License v1.0. See the LICENSE file at the root of this repository for
 * the full terms, or visit https://github.com/BetaZen-InfoTech/server-management.
 */
```

You retain copyright in your own contributions; the License and the CLA together grant BetaZen InfoTech the rights it needs to distribute the product as a coherent whole.

### Full notice of third-party components

See [`NOTICE`](./NOTICE) for the list of third-party components bundled with or orchestrated by Betazen Server Panel, along with their respective copyrights and licenses.

---

## 16. Contributing

Contributions are welcome — bugfixes, features, docs, translations. Before opening a pull request, read [CONTRIBUTING.md](./CONTRIBUTING.md) in full. In particular:

1. **The CLA** — by opening a PR you grant BetaZen InfoTech a broad license to your contribution ([CONTRIBUTING § 3](./CONTRIBUTING.md#3-contributor-license-agreement-cla)). You retain copyright; this is a *license to*, not an *assignment of*, your copyright.
2. **Scope** — this is a hosting control panel. Off-scope features will be politely declined.
3. **Quality gates** — `make lint && make test` must be green. PRs that touch auth, billing, or the mTLS channel require two maintainer approvals.
4. **Code of Conduct** — participants agree to uphold the [Contributor Covenant](./CODE_OF_CONDUCT.md). Enforcement: **conduct@betazeninfotech.com**.

---

## 17. Security disclosure

**Do not open public GitHub issues for security vulnerabilities.** Report them privately per [SECURITY.md](./SECURITY.md):

- 📧 **security@betazeninfotech.com** (preferred; acknowledgement within 72 hours)
- Or via GitHub private advisory at [/security/advisories/new](https://github.com/BetaZen-InfoTech/server-management/security/advisories/new)

We follow a **90-day coordinated-disclosure window** and publish an advisory within 14 days of the patched release. See [SECURITY.md § 5](./SECURITY.md) for our safe-harbour guarantee for good-faith research.

---

## 18. Project governance & support

| Need | Where to go |
|---|---|
| Bug report | [GitHub Issues](https://github.com/BetaZen-InfoTech/server-management/issues) with the **Bug report** template |
| Feature request | [GitHub Issues](https://github.com/BetaZen-InfoTech/server-management/issues) with the **Feature request** template |
| Security report | `security@betazeninfotech.com` — [SECURITY.md](./SECURITY.md) |
| Commercial / OEM license | `licensing@betazeninfotech.com` — [LICENSE § 11](./LICENSE) |
| Code of Conduct enforcement | `conduct@betazeninfotech.com` — [CODE_OF_CONDUCT.md](./CODE_OF_CONDUCT.md) |
| General support (paying customers) | `support@betazeninfotech.com` |
| Legal / trademark / DMCA | `legal@betazeninfotech.com` |

Community support is best-effort via GitHub Discussions and Issues. Paid support, SLA-backed support, and priority patches are available — contact `licensing@` or `support@`.

---

## 19. Trademarks & legal disclaimers

- **"BetaZen"**, **"BetaZen InfoTech"**, **"Betazen Server Panel"**, the BetaZen wordmark, and the BetaZen logo are **trademarks of BetaZen InfoTech**. No license to those marks is granted by the LICENSE — see [LICENSE § 7](./LICENSE).
- **"cPanel"**, **"WHM"**, and **"WebHost Manager"** are registered trademarks of **cPanel, L.L.C.** Betazen Server Panel is an **independent product** and is **not affiliated with, sponsored by, or endorsed by cPanel, L.L.C.** References to "WHM / cPanel-style" in this project's documentation are nominative descriptions of the product category, not a licensing or commercial relationship.
- All other trademarks, logos, and brand names in this repository are the property of their respective owners and are used for identification only.
- The software is provided **"AS IS"**, without warranty of any kind. See [LICENSE § 6](./LICENSE) for the full disclaimer.

---

<div align="center">

**Betazen Server Panel** · Built with ❤️ by [BetaZen InfoTech](https://betazeninfotech.com) in Kolkata, India.

Copyright © 2024-2026 BetaZen InfoTech. All rights reserved. Distributed under the [BetaZen InfoTech Source-Available License v1.0](./LICENSE).

</div>
