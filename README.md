<div align="center">

# Betazen Server Panel

**A modern, self-hosted WHM / cPanel-style server-management platform by [BetaZen InfoTech](https://betazeninfotech.com).**

[![Version](https://img.shields.io/badge/version-3.1.128-blue)](./backend/pkg/version/version.go)
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
    - 3.1 [What's new in 3.0.x](#31-whats-new-in-30x)
4. [Architecture](#4-architecture)
5. [Installation](#5-installation)
    - 5.1 [One-line install (recommended)](#51-one-line-install-recommended)
    - 5.2 [Manual install from a clone](#52-manual-install-from-a-clone)
    - 5.3 [What the installer actually does](#53-what-the-installer-actually-does)
    - 5.4 [Post-install verification](#54-post-install-verification)
    - 5.5 [Hardening checklist (do this before going live)](#55-hardening-checklist-do-this-before-going-live)
6. [First login](#6-first-login)
7. [Development setup](#7-development-setup)
    - 7.1 [Helper scripts in `scripts/`](#71-helper-scripts-in-scripts)
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
- **Server transfer wizard** — one-click migration between two Betazen Server Panel installs (domains, files, DNS, SSL, email, FTP, databases, Node apps, Deploy-Software projects, firewall rules, SSH keys, maintenance state, and the standalone Mail Suite install + its domain registration).
- **Deploy Software** — GitHub-integrated project runner with framework presets (Next.js, Nuxt, static, Node API, Python, Go), per-service systemd units, and auto-reconciling nginx vhosts.
- **Apps** — PM2-managed Node app runtime, static-site hosting, reverse-proxy vhosts, automatic SSL.
- **Mail stack** — per-domain DKIM / SPF / DMARC, Roundcube webmail with SSO, mailbox quotas, virtual forwarders, SpamAssassin filtering.
- **Mail Suite** — an optional, standalone Gmail/Zoho-style mail app (separate backend + React webmail + Flutter mobile client, installed per-domain from WHM → Mail Suite). One-step login with the mailbox email + password (verified over IMAP against the same Dovecot the classic mail stack uses), an HTML composer, threads, signatures, forwarders, and push notifications. Reads the exact same mailboxes as Roundcube — it's a modern client over the same server, not a second mail store.
- **DNS** — PowerDNS with a UI-driven zone/record editor, full CRUD for A / AAAA / MX / TXT / SRV / CNAME / CAA.
- **Backups** — scheduled, encrypted (AES-GCM), per-tenant, restore-preview before rolling forward.
- **Maintenance mode** — per-domain or server-wide, and state is preserved on transfer.
- **RBAC** — five roles (`vendor_owner`, `vendor_admin`, `vendor_staff`, `developer`, `support`, `customer`) with fine-grained permissions at the route level.
- **Self-service profile** — every user can manage their own name, email, password from the User Panel without bugging an admin.
- **Audit trail** — all privileged API actions are logged with actor, target, tenant, before/after state.

See [`FEATURES_VENDOR_WHM.md`](./FEATURES_VENDOR_WHM.md) for the full feature catalogue — every module, its API surface, and its UI screens.

### 3.1 What's new in 3.0.x

Active fixes/features since the 3.0.0 line opened. Single-line summary; full release notes live in [`backend/pkg/version/version.go`](./backend/pkg/version/version.go).

- **3.1.128** — Mail Suite composer gains a real HTML formatting toolbar (bold / italic / strike / code, H1-H2, bullet + numbered lists, blockquote, divider, links, image-by-URL, undo/redo) over the existing tiptap editor — no new dependency. The body was always HTML; now there are visible controls for it.
- **3.1.127** — Mail Suite can SEND and READ mail against a loopback Dovecot/Postfix, not just log in. Sending failed with `tls: cannot validate certificate for 127.0.0.1 … no IP SANs` (STARTTLS verified the local mail-host cert against the loopback IP) and reading used a bare-plaintext IMAP dial a secure Dovecot rejects. Both now share one hardened path: SMTP `DialStartTLS`/`DialTLS` + IMAP implicit-TLS(993)/STARTTLS(143)/plaintext, all with a loopback-aware TLS config that skips cert verification only for `127.0.0.1`/`::1`/`localhost`. External hosts still verified normally.
- **3.1.125** — Mail Suite login verification hardened (implicit-TLS/STARTTLS/plaintext fallback so Dovecot's `disable_plaintext_auth` default no longer 401s a correct mailbox password), and DR backup/restore now carry the mail-suite install (`/opt/mail-suite` + systemd unit) so a restore brings mail-suite back with its domain instead of silently dropping it.
- **3.1.124** — Fixed the WHM SPA build failing on every deploy: its main chunk crossed vite-plugin-pwa/Workbox's 2 MiB precache ceiling, so `vite build` exited 1 and `turbo build` errored, silently leaving a stale WHM dist while the backend advanced. `workbox.maximumFileSizeToCacheInBytes` raised to 5 MiB. (cPanel was unaffected — ~1.2 MB.)
- **3.1.123** — Mail Suite gains Gmail/Zoho-style one-step login: sign in with the mailbox email + password, verified directly against the mail server over IMAP (the mailbox is the source of truth). The backend auto-provisions the Mail Suite user + local betazen mailbox so the same messages seen in Roundcube appear immediately — no separate register / add-account step. (Flutter app aligned to the same flow in 3.1.126.)
- **3.1.91** — Panel-wide rolling restart on the Deploy Software page header. New "Restart all (rolling)" button next to Refresh walks every project in creation order and runs a rolling restart across each one — fully sequential at BOTH levels (one service `restarting` panel-wide at any given moment). Same stop-on-first-failure guard from v3.1.90 prevents one broken service from cascading into a panel-wide outage. Designed for "I just upgraded Node, restart every backend across every project safely". `POST /api/v1/whm/projects/restart-rolling-all` gated on `server.manage` so vendors with `deploy.manage` can't trigger a cross-tenant restart. Confirm dialog spells out the project count + that it may take several minutes.
- **3.1.90** — Per-project rolling restart: new "Restart one by one" toolbar button next to the existing "Restart all" in the project drawer. Restarts services one at a time, waits up to 15s for `systemctl is-active` between each, stops on first failure so a broken service doesn't take the rest of the project down. Order is stable (alphabetical by service Name) across reruns so the operator can verify "did service X come up clean" without chasing a moving target. New transient status "restarting" stamped on each service in turn; UI's `transitioning` predicate treats it as in-flight so the per-service row spinner + auto-poll keep ticking. Disabled when `totalBackends < 2` (no benefit over restart-all). Fire-and-forget — the actual rolling work runs in a backend goroutine.
- **3.1.89** — Stop auto-creating `admin@<domain>` mailbox on every domain create. Operator request: many domains the panel creates are for static sites / Deploy Software apps that will never send or receive mail; auto-provisioning a 1024 MB admin@ mailbox + leaking a one-shot password through the create response was wasted disk + a credential the operator had to either save or revoke. **Full mail server is still provisioned** (DKIM keys via `EnsureDKIMForDomain`, Postfix vhost + Dovecot config via `setupMailServer`, MX / SPF / DMARC DNS records) — only the mailbox row itself is removed. Operators add `admin@<domain>` (or any other mailbox) in one click via the Email page or the `email:write` external API endpoint. Applies to all three create paths (Manual, Bulk, API) because they all converge on `DomainService.Create`. Transient `AdminMailboxPassword` stays on the model for backwards compat (always empty now).
- **3.1.88** — Domain creation source: badge column + pill filter on the Domains list. Every row now shows HOW it was created — Manual (blue), Bulk (purple, CSV/XLSX upload), API (amber, external programmatic), Transfer (cyan, restored from server-to-server transfer). Pre-3.1.88 every domain looked identical regardless of origin. Pill-style filter next to the search input shows live counts and re-resets pagination to page 1 on change. Backend: `models.Domain.Source` (`bson:"source,omitempty"`) + `CreateDomainRequest.Source`; `DomainService.Create` defaults blank to "manual" so legacy rows keep working; `domain_bulk_service` stamps "bulk_upload" per row; `ProgrammaticHandler.CreateDomain` forces "api" (overrides what the caller sent so an integrator can't pretend a row was manual). Shared `sourceMeta()` helper keeps the badge column and pill filter visually consistent.
- **3.1.87** — Row-level Deploy button on the Deploy Software project list. Operator can redeploy a project straight from the list without opening the drawer first. Frontend-only — hits the same `POST /projects/:id/deploy` endpoint the drawer's "Deploy all" already used. In-flight state tracked per-project in a `Set<string>` so multiple project Deploy clicks fan out independently; each row's button shows its own spinner. Click is `stopPropagation`'d so it doesn't also fire the row's onOpen.
- **3.1.86** — Parallel deploy workers (env-configurable, default 4 — was hardcoded 2) + service name on every Recent Deployments row + fix counter accuracy bug. (1) `DEPLOY_WORKERS` env var controls how many service deploys run in parallel across the panel; clamped `[1, 32]`. Operators with 7-service projects on bigger boxes were watching queue-full / pending stalls after a webhook fan-out because only 2 workers drained the queue. Queue buffer also bumped 64 → 256 so a 20-service monorepo's full fan-out fits. (2) Each Recent Deployments row now shows which service it belongs to — coloured chip (emerald=backend, sky=frontend, slate=static) between the trigger pill and the commit SHA. Backend enriches `ProjectDeployment` with transient `service_name` + `service_role` fields (`bson:"-"`) via a one-shot service-map lookup; missing service (deleted) leaves the field empty and the chip is omitted. (3) Fixed a long-standing bug where `status="running"` was counted as Successful, inflating the success number whenever a deploy was in flight at query time — now only terminal `status="success"` counts.
- **3.1.85** — Webhook auto-deploy: drop three silent no-op filters that were preventing `git pull + redeploy all` after a verified GitHub push. (1) Strict branch equality skipped services with empty `GitBranch` (legacy rows pre-3.1.27 where the field was per-service then hoisted). (2) Per-service commit dedup (`svc.LastCommitSHA == payload.After`) swallowed legitimate re-pushes of the same SHA (force-push, post-revert redeploy, "Recreate delivery" button in the GitHub webhook UI). (3) Subpath matching gate. Operators editing shared libs / root configs / .env templates expected "I pushed → it deploys" and got "nothing to deploy". New behavior: a verified push to `<branch>` deploys every service whose `GitBranch` is `<branch>` OR empty (= follow project default). One project-level pull runs first via `runProjectPullAndEnqueue → inPlaceSync`; per-service deploys run with `skipPull=true`. Only remaining silent no-op: push to a branch no service tracks (logged with `push_branch` + `project_branch` for `journalctl` debug).
- **3.1.84** — Per-domain Document Root override. New purple folder-tree icon next to Edit registration on every Domains row opens a modal with absolute-path input + three quick-fill presets (Laravel `/public`, `…/public_html/public`, Default). On Save, `PATCH /whm/domains/:id/document-root` rewrites the nginx vhost (HTTP or SSL — whichever is active) with a new `root` directive, reloads nginx, persists `Domain.DocumentRoot` to mongo. Survives server-to-server transfer: `healMissingVhosts` (both PHP-only and cert-already-present paths) forwards `d.DocumentRoot` to `agent.CreateVhost` / `CreateVhostWithSSL`. Also fixed a latent bug: `SwitchPHP` previously rewrote the vhost without forwarding `domain.DocumentRoot`, silently wiping any custom docroot. `agent.VhostConfig` gains `DocumentRoot string` + `defaultDocRoot()` fallback so legacy callers keep producing `/home/<user>/domains/<domain>/public_html`.
- **3.1.82** — Activity card upgrade + smoother drawer refresh. (1) Endpoint takes `?limit=` (default 10, max 500); UI defaults to 10 with a "Show all (N)" toggle that bumps to 500 in one round-trip. (2) Lifetime counters now use server-side `CountDocuments` so they're exact regardless of the recent-list limit. (3) Each row gets a coloured trigger pill (manual / github push / api / custom), absolute timestamp on hover, and an expandable error preview with a one-click Copy button — same `CopyTextButton` wired into the ServiceDetail's inline error banner, the timeline error `<pre>`, and the per-deployment error modal. (4) Drawer refetches the live project on mount + after every mutating action so reopening it after a webhook delivery shows fresh `last_webhook_at` / `paused` / `auto_deploy` values immediately. (5) Burst-polling fires after every action button (Deploy all, Pull, Restart, Stop, Start, Pause, per-service Redeploy): 4 quick refreshes at 400/900/1500/2500 ms cover the gap between the API returning and the backend worker picking up the job.
- **3.1.79** — Search box on the Deploy Software project list. Header-level search filters as the operator types — case-insensitive, multi-term ("backend api" matches rows containing BOTH "backend" AND "api" in any searched field). Fields searched: name, slug, `git_repo_url`, description, user. Pagination operates on the filtered set; entering a query auto-resets to page 1 so a filter that shrinks the result count doesn't leave a stale "page 4 of 1". Hidden on the empty-state path; separate no-match panel keeps the two distinct UI states visually unambiguous.
- **3.1.78** — "Open folder" toolbar button on the project drawer. One-click jump from a Deploy Software project to its on-disk folder in the WHM File Manager — opens in a new tab so the operator doesn't lose their place. Sits between Edit and Export JSON. Links to `/whm/files?path=<project_dir>` using the existing File Manager URL contract (same one DomainsPage's "Open files" link already uses). Falls back to `/home/<project.user>` for legacy projects where `project_dir` is blank.
- **3.1.77** — Find / Find-Replace in the JSON editor for Edit JSON / Import JSON modals. Ctrl+F opens find, Ctrl+H opens find+replace, F3 / Enter = next (Shift = previous), Escape closes. Case-sensitive (Aa) + regex (.*) toggles, match counter `n/total`, Find prev/next, Replace, Replace prev, Replace next, Replace all. Regex backreferences (`$1..$9`, `$&`) in the replacement field when regex mode is on. Self-contained `FindReplaceTextarea` wrapper — no editor library dep, ~250 lines, reusable for any future textarea.
- **3.1.76** — Services bulk download / upload / edit as JSON. Three new operations on the project drawer's Services toolbar: `GET /:id/services/export` downloads `<slug>.services.json` (portable, strips host paths + per-instance runtime state); `POST /:id/services/import-json` walks the manifest and runs each entry through `AddService` (same atomic-rollback pipeline the wizard uses); `PUT /:id/services/bulk-edit` patches existing services in place by ID (cross-project IDs rejected per-row — careless paste from another project's export can't reach across boundaries). Pointer-indirection on UpdateServiceRequest means a manifest carrying only `{id, env_vars}` updates env vars across many services in one click without touching their commands/ports/domains.
- **3.1.75** — Programmatic mailbox password reset endpoint. New `POST /api/v1/external/email/:domain/mailboxes/:addr/password` under a dedicated `email:password` token scope (intentionally disjoint from `email:write` so a service account that provisions new mailboxes can't lock existing users out by rotating their passwords). Delegates to `EmailService.UpdateMailbox` — the same path the WHM Email page already runs for in-panel resets. `doveadm pw -s SHA512-CRYPT` rehashes, awk rewrites the matching line in `/etc/dovecot/users` in place preserving maildir + uid/gid + extra fields, plaintext is re-encrypted under `jwtSecret` for webmail SSO, both fields land in mongo in one `FindOneAndUpdate`.
- **3.1.74** — Per-project JSON export / import. `GET /api/v1/whm/projects/:id/export` downloads a portable manifest (name, repo, branch, services, framework, commands, domains, env_vars). Explicitly excludes encrypted PAT cipher (sealed under THIS panel's `APP_ENCRYPTION_KEY`, undecryptable elsewhere), webhook secret (regenerated on import), mongo `_id`s, host-specific paths, and per-instance runtime state. `POST /api/v1/whm/projects/import` accepts the manifest + optional fresh PAT, `override_name` (slug collisions on re-import), per-service `override_domains` (globally-unique-domain constraint blocks re-importing onto the source panel verbatim), and a destination user. Delegates to `ProjectService.Provision` so atomic rollback / slug allocation / webhook-secret minting behave identically to a manual New Project. Disjoint from the server-to-server transfer pipeline — no shared code, no idMap dependency, no cross-collection refs.
- **3.1.73** — Webhook auto-deploy diagnostics: every silent no-op path now visible. Pre-3.1.73 a verified GitHub delivery with a wrong secret returned 200 OK + ignored, GitHub showed success, but `last_webhook_at` never updated because it was set AFTER signature verification — operators saw "Waiting for first delivery" forever even when GitHub was actively delivering rejected requests. New `LastWebhookError` + `LastWebhookErrorAt` on the Project model, stamped on signature mismatch / missing secret / unknown project; cleared automatically on the next verified delivery. UI badge renders a red "Delivery failed · 2m ago" state with the exact reason inline. `GetWebhookSecret` auto-heals projects with an empty `webhook_secret` (legacy rows / rows that lost the field in transfer) by minting a fresh one in place on read. Structured INFO/WARN logs on every silent no-op path (project-not-found, empty-secret, signature-mismatch, paused, auto_deploy disabled, non-push event, empty ref, nothing-to-deploy, enqueuing-redeploy) so `journalctl -u serverpanel | grep "github webhook"` surfaces exactly why a delivery did or didn't deploy.
- **3.1.72** — Webhook auto-deploy no longer silently no-ops on cross-cutting commits. User report: commit `0dd5442` to BetaZen-InfoTech/waapi-3.1 ("SaaS Open Company Panel ... bump 3.4.23") fired the webhook correctly (last_webhook_at recorded, signature verified) but no redeploy ran — operator had to click Deploy All 5 min later. Root cause: the commit touched only root-level files (`.claude/*`, `*.md` docs, `.env.example`, `Template-*-Folder/*`, `WhatsApp-API-POSTMAN/*`, `*.bat` scripts, README version bump) — **zero files under any service's `git_subpath`** (backend_admin / backend_company / backend_vendor / frontend_admin / frontend_company / frontend_vendor / frontend_vendor_whatsapp_api). `HandleWebhook`'s path-match loop dropped every service from `todo`, hit `if len(todo) == 0 { return nil }`, and the pull+enqueue goroutine never started. Fix: when subpath matching finds zero candidates BUT `payload.Commits` has real file changes, fall back to "all on-branch services". Smart-path optimization preserved when at least ONE subpath matches — pushing `backend_admin/foo.js` still redeploys ONLY backend-admin. Only the "zero matches" case flips from silent no-op to "redeploy every on-branch service". Empty commits[] (force-push without diff, branch creation, tag-only push) still no-ops correctly. After-SHA dedup preserved for both paths.
- **3.1.71** — Step 0 "Pulled at project level — commit X (one git fetch shared across every service in this project)" actually lands on the deployment row. v3.1.69 added the IF branch that wrote the project-level message, but the line ~50 lines below in the same `runDeploy` called `completeStep(0, pullDetails)` UNCONDITIONALLY with the per-service-pull message, immediately overwriting the IF branch's write. Net effect: pre-3.1.71 step 0 always ended up as `Pulled latest from main (SHA)` regardless of whether the pull was project-level or per-service, and the v3.1.69 fix was invisible to the operator. Diagnostic logs at runDeploy entry confirmed `skipPull=true + projectPullCommit=...` arriving correctly from the queue; the IF branch ran; then the unconditional `completeStep` clobbered it. The terminal `completeStep` is now gated on `!job.skipPull` so it only fires for the per-service pull path. The `commit` calculation just above remains unconditional because `finalize()` reads it for the deployment row's `commit_sha` and the service's `last_commit_sha` regardless of which branch produced step 0.
- **3.1.70** — "Webhook fires but services show pending" — the deploy WAS running, the badge was lying. User reported on Waapi Dev 3.0: webhook delivered, git HEAD updated, every service's `last_commit_sha` advanced, npm install + npm run build ran (sudo log confirmed), but the project drawer showed all 7 services as "pending" badges for minutes. **Root cause**: v3.1.66's `finalize` wrote `status="success"` to BOTH the deployment row AND the service row. Deployment `status="success"` was correct; service `status="success"` landed in a column the frontend `StatusBadge` mapping doesn't recognise (only knows `running` / `deploying` / `stopped` / `needs_env_vars`) so every successfully-deployed service rendered as blue "pending". `finalize` now distinguishes the columns by intent — `deployment.status ∈ {running, success, error}`, `service.status ∈ {running, deploying, stopped, error, needs_env_vars}` — and translates `success → running` on the service side. `StatusBadge` also gets the safety belt `(running || success) → active` so stale browser caches and any legacy rows still render right. `bzpanel heal-deployments` extended to bulk-flip live `service.status="success"` rows to `"running"`. Idempotent; no DB migration; no migration regression.
- **3.1.69** — Per-service deploy progress shows the project-level git pull as **completed**, not "skipped". User flagged it on the Waapi Dev 3.0 project drawer: webhook hit at 08:15:34 UTC, all 7 services correctly deployed against the latest commit `ca667923...` (git HEAD on disk matched every service's `last_commit_sha`, `last_webhook_at` recorded, `last_deployed_at` within a few minutes of the webhook), but each per-service progress strip showed `Pull source from Git — skipped`. Looking at 7 "skipped" labels, the operator reasonably concluded "the webhook didn't pull anything" — even though the project-level pull HAD landed the new commit 30 seconds earlier in the `runProjectPullAndEnqueue` goroutine. New `projectPullCommit` field on `deployJob` carries the HEAD sha through to `runDeploy`; when set, Step 0 now renders as `completeStep(0, "Pulled at project level — commit XXXXXXX (one git fetch shared across every service in this project)")` so the operator sees a green checkmark with the actual commit hash. Per-service Redeploy button still uses the original `skipStep()` (no project-level pull there). Also pre-seeds the deployment row's `commit_sha` from the same value as defence-in-depth for a transient `rev-parse` failure post-v3.1.66.
- **3.1.68** — Manual "Deploy all" does ONE git pull instead of N. Pre-3.1.68 `DeployAll` enqueued every service with `skipPull=false`, so a 7-service project paid for 7 identical `git fetch + reset --hard` against the same shared clone — observed on the user's Waapi Dev 3.0 project: pull 1 = 2.9 s, pull 2 = 20.3 s (filesystem-lock contention against service 1's npm install warming the same tree), cumulative ~60 s of redundant work before the first real build started. **Plus a correctness bug**: a `git push` landing between pull 1 and pull N left the N services on DIFFERENT commits — `last_commit_sha` per row would look fine but the deploy was internally inconsistent. The webhook path has done it right since v3.1.63 (`HandleWebhook` → `runProjectPullAndEnqueue`: one pull, `UpdateMany` last_commit_sha across every service, then enqueue with `skipPull=true`). v3.1.68 extracts the helper (added a `trigger` param so DeployAll stamps "manual" and HandleWebhook keeps stamping "webhook") and the manual button now uses it too. Legacy split-clone projects (rare; no `proj.ProjectDir`) fall back to the pre-3.1.68 per-service-pull behaviour so they keep working.
- **3.1.67** — `bzpanel deploy` / `bzpanel rebuild` now refreshes the dovecot sieve hook conf as the last step of every upgrade. The v3.1.62 `sieve_plugins = sieve_extprograms` line (which lets the `vnd.dovecot.pipe` extension actually load so inbound mail compile stops failing with "451 sieve: Failed to compile script") only fired from `CreateMailbox`'s goroutine; v3.1.65 wired it into the transfer rehydrate, but an install upgraded via plain `bzpanel deploy` that never migrates and never creates a fresh mailbox kept its pre-3.1.62 conf forever — operator had to know to run `bzpanel heal-after-transfer` or hand-edit the file. `cmdRebuild` now calls `agent.EnsureMailHookInstalled` after the systemd restart so every operator-issued upgrade rolls out the current template. Idempotent (writeFileSecure atomic-renames), best-effort (failures print `! sieve hook refresh: <err>` but don't fail the rebuild — a webhook-helper hiccup shouldn't block the operator's deploy of the actual app). Adds ~50 ms to a no-op rebuild.
- **3.1.66** — Deploy-Software webhook actually looks like it worked. Two long-standing bugs in `runDeploy` surfaced on the user's Restro Dev project: (1) the happy-path finalize passed `status="running"` instead of `"success"` — 793 finished deploys mislabeled across the install, WHM activity list rendered in-progress spinners on completed work, success badge read 0; the frontend had a fallback (treat `status="running" && finished_at!=nil` as success) but raw-status coloring + counts were still wrong. Now `finalize("success", "", commit)` matches the string the positive-path renderer expects. (2) `git -C gitOpsDir rev-parse HEAD` ran as root against a repo owned by the project's hosting user; git 2.35+ refuses with `fatal: detected dubious ownership` — every deployment row landed with `commit_sha=""` even though the service's `last_commit_sha` got set correctly via `runProjectPullAndEnqueue`'s UpdateMany (which masked the bug). Added `-c safe.directory=<dir>` matching `safeArgs` used by `inPlaceSync` since v3.0.31. New CLI `bzpanel heal-deployments` retro-fixes pre-3.1.66 rows: relabels `status="running" + finished_at + no error_msg → "success"` AND backfills `commit_sha` from the matching service's `last_commit_sha`. Idempotent.
- **3.1.65** — Migration smoke-test follow-ups: TXT records actually land in PowerDNS post-rehydrate, and the v3.1.62 sieve_plugins conf rolls out without needing a fresh mailbox create. (1) `RebuildPowerDNSFromMongo` emitted TXT values verbatim to `pdnsutil replace-rrset` — bare `v=spf1 ...`. `replace-rrset` (unlike `add-record`) is strict and rejects unquoted TXT data with "Data field in DNS should start with quote". Every SPF / DKIM / DMARC TXT row on a transferred zone silently dropped; per-zone warn fired but `res.Failed` was never incremented so the aggregate summary reported `failed:0` (false-clean). Now wrapped in `"..."` (escaping embedded quotes for DKIM's `p="..."` shape) + `res.Failed` increments on every error. (2) `EnsureMailHookInstalled` (writes the v3.1.62 `sieve_plugins = sieve_extprograms` conf) only fired from `CreateMailbox`'s goroutine — installs upgraded to v3.1.62+ that didn't subsequently create a mailbox kept stale conf forever. `RunAllRehydrates` now calls it after the mailbox + forwarder map rebuild so any install that runs `bzpanel heal-after-transfer` or the transfer panel-records sync gets the new conf immediately.
- **3.1.64** — Operator-facing webhook secret rotate + clearer post-action notifications. New `POST /projects/:id/regenerate-webhook-secret` mints a fresh 32-byte hex HMAC secret and returns the plaintext so the WHM + cPanel Deploy-Software drawer can render a one-click Regenerate button. Pre-3.1.64 the only way to rotate a leaked or post-migrated secret was to edit Mongo directly. UI: two-click confirm (button swaps to amber "Confirm" for 5s, second click fires) so a misclick can't kill a working webhook; on success the new secret is auto-copied to clipboard AND surfaced in a 30s persistent toast with the value rendered, a Copy button, an "Open GitHub webhooks" deep-link, and a Dismiss — toast spells out "the old secret is gone, GitHub deliveries fail until you update the webhook's Secret field on GitHub". Tangentially: the one-word "PAT rotated" toast now spells out what changed (clone/pull token) AND what didn't (webhook URL + secret unchanged), plus offers a "Run test deploy" CTA so the operator can verify the new token clones cleanly without leaving the page.
- **3.1.63** — Deploy-Software GitHub webhook survives a server migration: repo-URL fallback when the route's `:project_id` no longer exists, and slow git pull moved off the request path. (1) Source's GitHub webhook URL embedded the SOURCE project's ObjectID; transfer re-mints every project's `_id` via idMap so the destination has a DIFFERENT id for the same `git_repo_url`. Pre-3.1.63 the handler returned 200 + `{"ignored":"project not found","success":false}` so GitHub showed success (HTTP 2xx) AND nothing redeployed AND `last_webhook_at` never advanced — uniquely bad diagnostic UX. New `resolveProjectForWebhook` + `canonicaliseRepoURLs` falls back to matching by `Repository.CloneURL` / `SSHURL` / `HTMLURL` / `FullName` from the GitHub payload (normalised to `host/owner/repo`); refuses to disambiguate when multiple projects share the same repo. Operator's stale GitHub URL now just works after cutover. (2) `HandleWebhook` ran `inPlaceSync` (git pull) synchronously before returning. A 5s pull blew past GitHub's 10s webhook timeout, GitHub closed the TCP connection mid-write (nginx 499), the delivery showed Failed in GitHub UI, AND GitHub retried — queuing duplicate deploys. Pull + enqueue moved into a new `runProjectPullAndEnqueue` goroutine that captures its own context.
- **3.1.62** — Mail receive + SSO survive server migration: Sieve `vnd.dovecot.pipe` extension actually loaded, migrated maildirs chowned to `vmail:vmail`, and one-shot heal for SSO ciphertexts the source's `JWT_SECRET` can't decrypt. (1) `sieve_install.go`'s dovecot conf enabled `+vnd.dovecot.pipe` in `sieve_extensions` but never loaded `sieve_plugins = sieve_extprograms` — `lib90_sieve_extprograms_plugin.so` ships with `dovecot-sieve` on Ubuntu 24.04 but Dovecot only activates it when the plugin is named. Every inbound delivery hit the after.d hook, Pigeonhole compile failed with "unknown Sieve capability vnd.dovecot.pipe", Dovecot LMTP returned 451, Postfix deferred. Mail piled up silently — operator only noticed when a customer asked. (2) `RebuildMailboxMaps` wrote `/etc/dovecot/users` + `virtual_mailbox_maps` correctly post-transfer but never touched maildir filesystem ownership; tarball-extract restored files as the SOURCE's uid:gid so Dovecot LMTP (vmail:vmail) couldn't write to `<maildir>/tmp/` and every inbound delivery failed with "Permission denied". New `repairMaildirOwnership` pass runs at the tail of `RebuildMailboxMaps` — one `chown -R vmail:vmail /home/<u>/mail/` per unique hosting user (deduplicated via a set), plus `chgrp vmail` + `chmod g+x` on the parent home for traversal. Same code path reachable via the existing `heal-mailboxes` and a new `heal-mail-perms` alias. (3) `encrypted_pass` blobs stranded by pre-3.1.61 migrations are sealed under the source's `JWT_SECRET`; panel's "Open in Webmail" decrypts garbage and Roundcube shows "Server Error: Internal error occurred". New `EmailService.HealStaleSSOEncryption` walks every mailbox, attempts decrypt with current secret, `$unset`s the column for any row where decrypt fails — panel UI surfaces a clean "Set password to enable SSO" CTA matching the never-had-SSO case. Refuses to clear when `JWT_SECRET` is empty so a misconfigured destination doesn't nuke the column wholesale. Exposed via `bzpanel heal-mail-sso`. IMAP/SMTP login untouched (portable SHA512-CRYPT hash on the `password` field).
- **3.1.61** — `www.<d>` + `cname.<d>` coverage for App auto-SSL + post-migration project services, plus webmail-SSO re-encryption so mail "Open" keeps working after a server transfer. (1) `ensureSSLForApp` requested a cert covering only `[<d>, www.<d>]` even though every App vhost template lists `server_name <d> www.<d> cname.<d>;` — nginx :443 claimed the `cname.<d>` listener but the cert SAN list didn't cover it; browsers visiting `https://cname.<d>` got name-mismatch handshakes even though the page eventually loaded. Now the cert request + persisted `SSLCertificate.Domains` row both include `cname.<d>`, matching what `DomainService.Create` already does for PHP-FPM domains. (2) `buildRecoveryVhostSpec` (used by transfer recovery → `recoverProjectService`) copied `svc.AliasDomains` verbatim; normal create path's `buildMergedVhostSpec` auto-inflates the alias set with `www.<primary>` + `cname.<primary>` AND `www.<alias>` + `cname.<alias>` for every linked alias (since v3.1.11 + v3.1.31). Without same inflation in recovery, transferred Deploy-Software services landed with nginx `server_name <primary>;` only — `https://www.<primary>` returned 404 from panel catch-all (the exact symptom the user flagged for "server migration time"). Extracted into a single helper `expandImplicitAliases(primary, aliases)` so both call paths produce the same nginx server_name + LE SAN list. (3) Mailbox sync copied `encrypted_pass` verbatim during panel-records migration; sealed under the SOURCE's `JWT_SECRET`. New `reencryptSyncedMailboxes` pass mirrors the `panel_mail.password_cipher` re-encryption already in the same file: greps source's `/opt/serverpanel/.env` for `JWT_SECRET`, calls `EmailService.ReencryptForTransfer` for each mailbox, `$unset`s the field when decrypt fails. IMAP/SMTP login itself was already migration-safe.
- **3.1.36** — `vite.config.ts` in both SPAs loads `vite-plugin-pwa` via dynamic `await import()` with a try/catch fallback. Operator deploys that ran `npx turbo build` against a stale `node_modules` (no `npm install` after a dep bump) had been hard-failing with `ERR_MODULE_NOT_FOUND` since 3.1.34; now the build degrades to a plain SPA (no service worker / install prompt) instead of stopping the deploy, with a console warning pointing at the npm-install fix. Belt-and-braces: new top-level `npm run build:deploy` script in `frontend/package.json` runs `npm install --prefer-offline && turbo run build` in one shot for operators who prefer hand-typed deploy blocks.
- **3.1.35** — `bzpanel rebuild` now installs + builds the frontend before the Go binaries (was Go-only). Pre-3.1.35 a deploy that bumped a frontend dep would build the Go side fine but `npx turbo build` exploded because `node_modules/` still pointed at the previous lockfile's closure. New flow: `npm install --no-audit --no-fund --prefer-offline` (cheap when nothing changed) → `npx turbo build` (turbo cache no-ops on unchanged source) → Go binaries → systemctl restart. Frontend runs first so an npm/build failure aborts BEFORE we overwrite a working `bzpanel` binary. ENOENT on `/frontend` is a soft-skip for slim/Docker installs that ship only compiled binaries.
- **3.1.34** — PWA install + offline guard + online-status indicator + modern web-app meta. Both SPAs ship a Workbox-backed service worker (auto-update on next reload), a per-app `manifest.webmanifest` with a brand-coloured `pwa-icon.svg`, and matching `apple-touch-icon` + `theme-color` + `viewport-fit=cover` meta so iOS Add-to-Home-Screen and Android Install both work. New shared `@serverpanel/ui` primitives — `useOnlineStatus()`, `usePingableServer()`, `useInstallPrompt()`, `OfflineError` class, `<OfflineOverlay />`, `<OnlineStatusBadge />`, `<InstallAppButton />`. The `@serverpanel/api-client` axios request interceptor short-circuits when `navigator.onLine === false`, throwing `err.code === "ERR_OFFLINE"` instead of dragging the UI through a 30-second timeout. `<OfflineOverlay />` paints a full-screen `z-[60]` block on every flow that fires HTTP — `DashboardLayout` (whm + cpanel) AND `LoginPage` (whm + cpanel), so even unauthenticated login is gated. Real bug caught + fixed in `cmd/server/main.go`: the existing `app.Get("/whm/*", sendWHMIndex)` catchall would have served `/whm/sw.js` as `text/html`, blocking every browser from registering the service worker; explicit handlers for `sw.js`, `registerSW.js`, `manifest.webmanifest`, `pwa-icon.svg`, and the hashed `workbox-<hash>.js` (via Fiber's `+` glob) now run BEFORE the catchall on both `/whm/*` and `/user-panel/*` mounts. SW files get `Cache-Control: no-store` + `Service-Worker-Allowed: /` so deploys actually replace the running worker instead of waiting 24h for the HTTP cache to expire.
- **3.1.33** — Mobile drawer hardening: ESC closes the off-canvas Sidebar (mirrors Modal's existing affordance), the drawer auto-closes when the viewport crosses up past `md` (768 px) so the body scroll-lock can't leak across browser-resize, and `aria-hidden` now correctly tracks the live viewport via a new `isMobile` `useState` backed by `matchMedia("(max-width: 767px)")`. Pre-3.1.33 a user who opened the drawer on a phone-width window then resized to desktop got the docked Sidebar visually plus a permanently-locked body scroll until they explicitly closed the drawer (which they couldn't see). Pre-3.1.33 the unconditional `aria-hidden` on the off-screen drawer would also have hidden the docked desktop Sidebar from screen readers (because the host always passes `mobileOpen=false` on desktop) — the new `isMobile && !mobileOpen` gate fixes that too. SSR-safe with Safari < 14 `addListener` fallback.
- **3.1.31** — Deploy Software link-domain API now actually wires SSL for the linked alias. Two coupled defects fixed: (1) `buildMergedVhostSpec` added `www.<primary>` + `cname.<primary>` to the vhost server_name + cert SAN list (the v3.1.11 konsultkaro.com fix) but the loop only ran over the primary — every linked alias landed in nginx server_name as `<alias>` alone with no www / cname variant, so a vhost for primary `myapp.com` linked to `shop.example.com` had server_name `myapp.com www.myapp.com cname.myapp.com shop.example.com` and `https://www.shop.example.com` SNI-routed to the panel default vhost. The cert SAN list missed both names too because `IssueLetsEncryptMulti` reads `spec.Aliases` verbatim. (2) `reconcileVhostFor` caught a non-zero certbot exit, logged to stderr, then returned nil — link-domain API passed nil straight up so integrators whose new alias's DNS hadn't propagated got 200 OK + zero signal that `https://<alias>` would serve the wrong cert. Mirror loop now adds `www.<a>` + `cname.<a>` for every alias (with a `www.www.X` recursion guard for callers that already pass a www-prefixed alias). New transient `bson:"-"` fields on `models.ProjectService`: `SSLWarning` (human-readable explainer) + `SSLCoveredDomains` (parsed SAN list from `/etc/letsencrypt/live/<primary>/fullchain.pem`). Populated by new `agent.LetsEncryptCertSANs` (parses openssl x509 output) + new `reconcileVhostForAliasChange` wrapper that runs the standard reconcile then verifies the live cert covers `targetDomain` via wildcard-aware SAN matching.
- **3.1.30** — Deploy Software link/unlink-domain API: `:id` path param now enforced + tenant guard + 404 / 403 status codes. Three coupled defects fixed: (1) the route is `…/projects/:id/services/:svc/link-domain` but the handler pulled `:svc` and dropped `:id` on the floor — `:id` was effectively documentation. Same defect on the panel-side `AddAlias` / `RemoveAlias` handlers (whm + cpanel routes share the same shape). (2) `ProjectService.GetService` used `bson.M{"_id": oid}` with no `tenant_id` filter, so a vendor token holding `deploy:link` could fetch — and `AddAlias` / `RemoveAlias` then mutate — ANY service across tenants just by guessing the ObjectID. `ListAllServices` already had the tenant_id filter so the list endpoint was safe; only the per-id mutating endpoints leaked. (3) Every failure landed as 400 BadRequest with a flat error string — integrators couldn't tell "wrong project ID" from "malformed body" from "duplicate alias" from "cross-tenant escape" without parsing the message. Fix in three layers (defence in depth): new shared `assertCanLinkAliasOnService` guard + new `AddAliasWithProject` / `RemoveAliasWithProject` methods that take `:id` explicitly; five sentinel errors (`ErrServiceNotFound`, `ErrProjectNotFound`, `ErrServiceProjectMismatch`, `ErrCrossTenantProject`, `ErrLinkedDomainNotOwned`) — `ErrServiceProjectMismatch` is 403 not 404 deliberately so the API doesn't leak that the service exists under a different ID; both programmatic + panel handlers translate sentinels to 404 / 403 via a shared `mapAliasErr` helper. Behaviour change worth flagging: tenant-scoped callers can no longer link a domain that isn't a registered Domain in their tenant. Smoke test [`scripts/_smoke_alias_link.py`](./scripts/_smoke_alias_link.py) (paramiko + admin OTP + minted API token) exercises the happy path plus four guarded failure modes.
- **3.1.29** — Mailbox webmail-link API: case-insensitive lookup + absolute URL + sharper 404 + matching response shape. User report: "mailbox not found" toast on the External webmail-link API even when the mailbox visibly existed. Three stacked bugs: (1) `CreateMailbox` stored `req.Email` verbatim and `GenerateWebmailToken` / `GetMailboxByAddress` queried via `bson.M{"email": addr}` with no `$regex` and no `toLower` — a row keyed `Admin@konsultkaro.com` would 404 every `…/mailboxes/admin@konsultkaro.com/webmail-link` call (and vice versa). Three address-form mailbox APIs (GET stats, DELETE, webmail-link) all funnelled through the same broken lookup. (2) The OpenAPI 3.1 spec advertised `{ url, expires_in }` with `url: format uri` but the handler returned `{ token, url }` where `url` was the bare path `/webmail/sso.php?token=…` — external integrators couldn't hand it to a browser without scraping the panel hostname out of their own request. (3) `expires_in` was a magic 300 s in `sso.php` with no surface in the response. Fix: lowercase + trim email at `CreateMailbox` so every new row is canonical; new shared `findMailboxByEmail` helper tries an exact match first (index-only) and only falls back to a case-insensitive regex on miss — so existing pre-3.1.29 mixed-case rows are still findable without rewriting the collection. URL is now built from `c.BaseURL()` so it carries the request's own scheme + host + port. `expires_in: 300` exposed in the response. "mailbox not found" now returns 404 (was 400 — indistinguishable from a malformed body) and the error string includes the address that was searched.
- **3.1.32** — Mobile-friendly chrome: Sidebar slides in as an off-canvas drawer below `md` (768 px), TopBar gets a hamburger + `flex-wrap`, badges + footer collapse gracefully on phones, pagination buttons hit a 36 × 36 px touch target. Pre-3.1.32 the panel was effectively unusable on a phone — the always-docked 256-px Sidebar crushed the main content area to ~100 px on a 360-px viewport. Fix lives entirely in shared `@serverpanel/ui` chrome (Sidebar, TopBar) plus tiny wires through the host `DashboardLayout` (whm + cpanel) — both SPAs get the new behaviour for free with no per-page changes. Sidebar above `md` is bit-identical to pre-3.1.32 (desktop UX unchanged). Black/60 backdrop locks body scroll while the drawer is open; tapping the backdrop, a nav row, the new mobile-only X button, OR the route changing for any other reason (deep link / back button) closes it. TopBar drops fixed `h-16` for `py-3 + flex-wrap`; server-IP + version badges + user-name label hide below `sm` (640 px) so the title + icons all fit on a 360-px viewport. cpanel DashboardPage stat cards now break to 2 columns at `sm` (640 px) instead of `md` (768 px); Quick Actions stack to 1 column on phones.
- **Docs refresh (post-3.1.28)** — API + Webhook docs (HTML/CSS at [`docs/api/index.html`](./docs/api/index.html) + [`webhooks.html`](./docs/api/webhooks.html), OpenAPI 3.1 at [`docs/api/openapi.yaml`](./docs/api/openapi.yaml), importable Postman collection at [`docs/postman/Betazen-Server-Panel.postman_collection.json`](./docs/postman/Betazen-Server-Panel.postman_collection.json)) bumped to v3.1.28 and now cover the flat `GET /api/v1/external/deploy/services` inventory across every project. The Deploy Software page on both surfaces gained an "API / Developer IDs" disclosure card with one-click copy chips for `project_id`, `project_slug`, `project_user`, the webhook URL, and a pre-substituted curl example, plus a per-service `id: a1b2c3…` chip next to each service name — operators wiring an external token script no longer need to dig through Mongo or the URL bar to find the IDs the API expects. WHM Vendors page gained an Edit-details row action so an admin can fix a vendor's `name` / `email` in-place without dropping into Mongo.
- **3.1.28** — File Manager upload cap raised from 500 MB to 10 GB across all three layers that have to agree (frontend `MAX_UPLOAD_BYTES`, Fiber `BodyLimit`, nginx `client_max_body_size`). `client_body_timeout` + `send_timeout` raised 600 s → 3600 s so a 10 GB upload on a 5 MB/s home line doesn't get cut. Existing installs need a one-liner sed on `/etc/nginx/sites-enabled/serverpanel` — full command in `version.go` so it's findable from the binary too. Future work: chunked / resumable (tus.io) for >10 GB.
- **3.1.27** — Deploy Software branch field hoisted from per-service to project. The shared-clone layout (one `.git` per project, services are subdirs) means services share one working tree and CANNOT legitimately track different branches — collecting the field per service was a footgun. Wizard Basics step gains a Branch input next to Repository URL; per-service / Add Service modals lose the field; Edit Service shows it read-only with a pointer to Edit Project. `loadProject` heals existing projects on first read by copying the first service's branch onto the project doc — operator never runs a migration.
- **3.1.26** — Deploy Software Add Service modal swaps the plain `<select>` "primary domain" picker for the existing `SearchableSelect` from `@serverpanel/ui` (same type-ahead picker used for vendor / mailbox dropdowns). Production VPS lists ran 25+ entries deep with look-alike subdomain trees — typing "wl-vrndor" now narrows the list to two. Stored value not in the live list still renders with a `(not registered)` hint so editing an existing service after the source domain was deleted doesn't silently wipe the field.
- **3.1.25** — User Panel mailbox bulk-upload template drops the redundant `domain` column. Server already derives domain from `email.split("@")[1]` (since 3.1.17), and a typed `domain` cell that disagreed was silently overridden anyway — including the column was just operator-confusing noise. Vendor template is now exactly `[email, password, quota_mb, send_limit_per_hour]`. WHM admin template unchanged. Tenant scoping enforced via `CallerScope.AssertOwnsDomain` on the derived domain. Backward-compat: legacy CSVs that still include `domain` / `user` columns parse cleanly (parser ignores unrecognised cells).
- **3.1.24** — Bulk-upload templates (domains + email) are now surface-aware. WHM variant keeps the `user` column (admin picks owner per row); User Panel variant drops it (server force-overrides to the authenticated caller anyway). XLSX User Panel variant adds an inline note row explaining the auto-assignment policy so Excel-savvy operators see the rationale without reading API docs.
- **3.1.23** — Email bulk operations reach User Panel parity with WHM: per-mailbox export (CSV/XLSX of credentials with OTP-gated password reveal), template download, bulk upload, bulk delete with OTP confirmation. WHM-side the bulk-upload parser gains an auto-create-missing-domain hook — when a row's `user` column points at an existing vendor but the row's domain isn't yet registered, the parser provisions the domain (with full SSL + mail wiring) before creating the mailbox so a new vendor's first import works in one round-trip.
- **3.1.22** — WHM Deploy Software "Primary domain" dropdown now scoped to the project's vendor. Pre-3.1.22 the WHM admin's Add Service / Edit Service modals rendered EVERY domain on the box across every tenant — making cross-tenant mistakes one click away (a project's files live under `/home/<project.user>/projects/<slug>/`, so picking a domain owned by another vendor would either fail SSL issuance or write a vhost pointing at the wrong tenant's home). Now `AddServiceModal` + `EditServiceModal` filter `availableDomains` by `project.user`. Add Service also gains a "no domains available for <vendor>" amber banner when the filtered list is empty so the operator sees WHY + where to add a domain. cPanel side unchanged — `/api/v1/cpanel/domains` is already tenant-scoped at the service layer.
- **3.1.16** — Deep audit of zone / mail / SSL / default-mailbox / WHOIS at every domain create entry point. **6 bugs fixed**: (1) bulk-upload duplicated SSL issuance — Create already runs SSL with retry+SANs; the redundant single-shot SAN-less call was wasted on the happy path and could shrink the cert on a race. Now bulk reads `SSLActive` off Create's returned doc and runs ForceSSL on top. (2) ForceSSL gating in bulk-upload only fired when the redundant SSL succeeded — fixed to key off `SSLActive`. (3) `CreateZone` errors were swallowed — apex `pdnsutil create-zone` failures left the domain row stamped active with NO DNS authority + mail setup never ran. Now surfaced in `setup_warnings` + structured zerolog. (4) `SetupSubdomainMail` / apex mail setup errors were stderr-only — bulk-upload reported "created" even when outbound mail would be unsigned. Now in `setup_warnings` with a "run bzpanel heal-mail" hint. (5) `admin@<domain>` auto-mailbox password was generated, used, then DISCARDED — operator could NEVER log in to the auto-created mailbox. Now stamped on the returned Domain's `admin_mailbox_password` field (bson:"-", JSON only) and rendered as a click-to-copy block in the bulk-upload row. (6) WHOIS data fetched by `RunPreflight` was thrown away — operators who left registrar/dates blank ended up with no expiry tracking. Now whois fills any blank registrar/dates field on every create entry point (single modal, programmatic API, bulk upload — all route through Create); operator-provided values still WIN. New bson:"-" fields on Domain (`SetupWarnings`, `AdminMailboxPassword`) + parallel fields on `BulkRowResult`. Shared `BulkUploadDomainsModal` renders an "Admin Mailbox" column + an amber secondary row beneath any row with non-empty warnings.
- **3.1.15** — Bulk Delete domains (WHM-only) gated by an email-OTP confirmation step. Pairs with the v3.1.13 row-selection feature: select rows on the WHM Domains table → click "Delete N Selected" → server mails a 6-digit code to the admin's address → enter the code → server runs `DomainService.Delete` in a loop and returns a per-row result table (mirrors Bulk Upload's shape). New collection `bulk_delete_otp` (separate from login OTP — different lifecycle, carries the target id list, 10-minute TTL, one-shot per id-set). New endpoints `POST /api/v1/whm/domains/bulk-delete/request-otp` + `/confirm`, gated by `middleware.RequireRole("vendor_owner")` so even staff with `domain.manage` can't trigger destructive bulk operations. Security: 6-digit numeric CSPRNG code (sha256-hashed at rest), 32-byte CSPRNG token bound to (admin, ids), 5-attempt cap, code marked Used BEFORE the destructive loop so concurrent retries can't double-fire, 500-row sanity cap on the request. Mailer fallback: when SMTP is disabled the code is printed to `journalctl -u serverpanel` and the response carries `mailer_enabled: false` so the modal surfaces the journalctl hint. Frontend: new `BulkDeleteDomainsModal` in `apps/whm/src/components` (deliberately NOT in `@serverpanel/ui` — User Panel keeps its per-row trash icon as the only delete path) with a 3-step UX: review → verify → result. New tests cover code/token shape + uniqueness, sha256 hash determinism, OTP email body shape, and HTML-escape on hostile admin names.
- **3.1.14** — Hierarchical domain ordering on every list + export: apex first, then its subdomains grouped immediately underneath. Pre-3.1.14 the Domains table and CSV/XLSX export used mongo's `created_at desc` order, so a child created later appeared ABOVE its parent and an operator scrolling 50+ domains had to mentally re-group. Now domains sort by reverse-label key — `app.example.com` becomes `com.example.app` for comparison, so `example.com` (apex `com.example`) naturally sorts before `app.example.com` (`com.example.app`). Multi-level subdomains slot in under the nearest registered parent in the same pass — no special-casing. Stable sort so two rows hashing to the same key keep their order. Tests: `TestReverseLabelKey` / `TestSortDomainsHierarchical_*` / `TestSortExportableDomainsHierarchical` / `TestDomainLessHierarchical_TLDClustering`.
- **3.1.13** — Domains page row selection + Export to CSV / Excel. Pairs with the v3.1.9 Bulk Upload feature so the Domains page now does both halves of the round-trip — select rows (or check Select All), click Export CSV / Export Excel, edit in your spreadsheet, re-import via Bulk Upload. Column shape matches the bulk-upload template byte-for-byte plus two read-only review columns at the end (`ssl_active`, `status`); the bulk-upload parser ignores unknown columns so the unedited export round-trips as a no-op. New endpoint: `GET /domains/export?format=csv|xlsx&ids=<csv>&all=true` (mirrored on `/whm` and `/cpanel`). `all=true` is a separate flag — empty `ids` + no `all` → empty file (so a JS bug that forgets to send the selection can't accidentally dump every tenant's domains). On cPanel `FetchDomainsForExport` runs every row through `CallerScope.AssertOwnsDomain`, so a vendor can only export their own domains even when `all=true`. Frontend: leading checkbox column on the WHM + User Panel table; tri-state Select All header (checked / indeterminate / unchecked) respects the operator's search state; Export buttons adapt their label to show the count (`Export 12 (CSV)`); selection clears on every fetch so stale ids never reach the backend. `Column<T>.header` in `@serverpanel/ui` widened from `string` to `React.ReactNode` to host the header checkbox — backwards-compatible.
- **3.1.12** — New `bzpanel heal-www` (alias `repair-www`, bsp menu option 13) — one-shot heal for every pre-3.1.11 domain on the box. The v3.1.11 fix only covered NEW deploys; existing installs still had the old vhost files + old certs, and manually fixing each one with certbot + nginx edits doesn't scale. `heal-www` walks every domain row, sed-style adds `www.<d>` and `cname.<d>` to every `server_name` line in `/etc/nginx/sites-available/<d>` (preserving operator-added aliases + indentation), parses each cert's current SAN list via `openssl x509 -text`, and runs `certbot --force-renewal` with the union of (existing SANs ∪ www ∪ cname) when the cert is missing either. Wildcard certs / suspended domains / domains without a vhost file are skipped with reasons surfaced in the per-run summary. Idempotent — a re-run on a healed box reports "nothing to do". One-command upgrade for any pre-3.1.11 install: `bzpanel deploy && bzpanel heal-www`.
- **3.1.11** — `https://www.<domain>` now works on Deploy Software apps (Next.js / Node / static frontends). Live-reproduced on `konsultkaro.com`: apex worked, www returned the panel's catch-all cert + 404. Public probe confirmed DNS was fine, but the apex cert had only `DNS:konsultkaro.com` as SAN and nginx fell through to the default vhost on `SNI=www.<X>`. Root cause: `reverseProxyTemplate` / `reverseProxySSLTemplate` / `CreateStaticVhost` / `CreateStaticVhostWithSSL` all used `server_name {{.Domain}};` — bare apex only, no `www.<d>`, no `cname.<d>` — and Deploy Software's `IssueLetsEncryptMulti` never auto-added www to the SAN list. PHP-FPM templates have always covered www; only the reverse-proxy + static vhosts didn't. All three templates now read `<d> www.<d> cname.<d>` and `buildMergedVhostSpec` injects the same aliases implicitly so the cert's SAN list and the nginx server_name list match by construction. Heal path: `SSLService.Reissue` now ensures `www.<d>` + `cname.<d>` are always added to the additional_domains list (de-duped against operator-added aliases) — one click on the WHM/cPanel SSL Reissue button fixes any pre-3.1.11 domain. **Upgrade for konsultkaro.com**: `bzpanel deploy` → WHM → SSL → Reissue on konsultkaro.com (or Deploy Software → Redeploy the app), then verify SAN list with `openssl s_client -connect www.<d>:443 -servername www.<d> </dev/null | openssl x509 -noout -text | grep -A1 'Subject Alternative Name'`.
- **3.1.10** — `cname.<domain>` flat alias auto-created at every domain create entry point. Apex / subdomain / multi-level subdomain all get a `cname` CNAME pointing back at themselves the moment the domain is created — no manual DNS edit needed for third-party services (Vercel / Netlify / SaaS verifications, vanity URL templates) that ask the operator to "add cname.X pointing to X". Single source of truth: every create path (WHM manual, User Panel manual, programmatic API token, Bulk Upload CSV/XLSX) routes through `DomainService.Create`, so one patch covers all four. Multi-level handled by the existing subPart machinery: `api.abc.users.X` lands `cname.api.abc.users` in apex zone X, pointing at `api.abc.users.X.`. Companion fixes keep the alias actually functional end-to-end: nginx vhost `server_name` covers `cname.<domain>` (HTTP + SSL + suspended templates) so HTTP-01 challenges and browser visits don't hit the catch-all default vhost; Let's Encrypt SAN list now includes `cname.<domain>` so HTTPS returns the right cert (no name mismatch). `Domain.Delete` cleanup extended to sweep the new `cname.<sub>` record alongside the v3.0.41 set. `bzpanel heal-dns` backfills the record on pre-3.1.10 subdomain installs idempotently with a new "cname CNAMEs added" summary line.
- **3.1.9** — Bulk Upload Domains. New "Bulk Upload" button next to "Add Domain" on both WHM and User Panel — accepts CSV or XLSX with one row per domain, runs each through the same `DomainService.Create` + Let's Encrypt + force-HTTPS pipeline the single-domain form uses. Per-row failures don't abort the loop — the response carries a result table the UI renders with row number / domain / owner / success-or-error / SSL outcome (issued / force-https / skipped). Header row matches case-insensitively across snake_case / kebab / "Title Case" / concatenated forms (operators editing in Excel type "Domain Name" / "PHP Version" without thinking and the parser still resolves them). On WHM the row's `user` cell is honoured (platform-owner picks any vendor); on User Panel the cell is ignored and replaced with the authenticated caller's username so a tenant can't reach outside their scope. New endpoints: `GET /domains/bulk-upload/template?format=csv|xlsx` + `POST /domains/bulk-upload` (mirrored on `/whm` and `/cpanel`). 10 MB cap. Templates generated FROM CODE so they stay in lockstep with `CreateDomainRequest`. Shared `BulkUploadDomainsModal` lives in `@serverpanel/ui`. New parser tests pin the header-aliasing + cell-coercion contract (`TestNormaliseHeader` / `TestResolveHeader_*` / `TestRowAllBlank` / `TestParseBool` / `TestAtoiSafe`); CSV+XLSX integration tests assert routing + validator boundaries without needing a mongo.
- **3.1.3** — Deploy Software GitHub PAT survives transfer when source's `APP_ENCRYPTION_KEY` isn't readable by a single grep. Pre-3.1.3 `fetchSourceEncKey` ran one command (`grep '^APP_ENCRYPTION_KEY=' /opt/serverpanel/.env`); `.env` is mode 600 root-owned, so any non-root SSH user (wheel-group account, deploy user) silently came up empty and every project's PAT was dropped on transfer — `git pull` / auto-deploy broke until the operator manually re-pasted each PAT. Now four probes in one round-trip: primary `.env`, legacy split `backend/.env`, `sudo -n cat`, then `/proc/<panel-pid>/environ` (running process holds the key even when `.env` was rotated). Plus dedup-skip in `syncProjectsForTransfer` no longer ignores PAT updates: re-running the wizard after fixing source-side perms now backfills the missing PAT on existing destination rows (counted in `patHealed`). Source-key unreadable warn now fires from both the projects path AND webhooks path with a probe-tag suffix so operators can tell why. New `project_pat_reencrypt_test.go` pins the round-trip contract.
- **3.0.41** — Subdomain delete leaves no orphan records. Pre-3.0.41 `Domain.Delete` only matched A + www CNAME — every MX / SPF TXT / `_dmarc.<sub>` TXT / `mail._domainkey.<sub>` TXT written by `SetupSubdomainMail` survived the delete and accumulated stale rrsets in the apex zone (broken SPF / wrong-IP MX after a server transfer). Live-confirmed on production by creating a 3-level `users / abc.users / api.abc.users.konsultkaro.in` hierarchy: A records were correct at every level (apex-wins parentZoneOf works), but DELETE returned 200 while leaving 9 records orphaned in `dns_records` + 6 in `pdnsutil`. Fix matches by EXACT name across A/CNAME/MX/TXT for `subPart`, `www.subPart`, `_dmarc.subPart`, `mail._domainkey.subPart` — refuses to over-delete deeper subdomain records (`users.example.com` won't nuke `api.users.example.com`).
- **3.0.40** — Mail SSL is now fully automatic. New `bzpanel mail-ssl-sweep` walks every panel-tracked domain and wires mail.<domain> SSL for any whose public DNS resolves to this server. `SSLService.IssueLetsEncrypt` spawns a background goroutine to shell out to `bzpanel mail-ssl <domain>` after every web cert issuance — fresh installs that add a domain via WHM get their mail SSL automatically (within an hour of public DNS propagating, controlled by the SOA TTL). install.sh writes `/etc/cron.d/serverpanel-mail-ssl-sweep` for hourly catch-up. No more per-domain manual `bzpanel mail-ssl` step.
- **3.0.39** — Server transfer now carries mail SSL too. `ExportSSLFromRemote` tars `live/mail.<domain>` + `archive/mail.<domain>` + `renewal/mail.<domain>.conf` alongside the regular cert; the destination-side cp puts them in place and shells out to `bzpanel mail-ssl <domain>` to wire up Postfix SNI / Dovecot SNI / nginx helper vhost / renewal hook. `cmdMailSSL` gains a fast-path early-return when the cert already exists on disk (skips DNS pre-flight + certbot, runs only SNI wire-up) so it's safe to invoke during transfer when public DNS still points at the source.
- **3.0.38** — Postfix SNI value column was `<fullchain>,<privkey>` (cert first); Postfix expects `<privkey>,<fullchain>` (key first) and rejects the wrong order at handshake with "key not first → aborting TLS handshake". Swapped order. Strict mail clients now actually receive the LE cert when connecting with SNI=`mail.<domain>`.
- **3.0.37** — `bzpanel mail-ssl` was calling `postmap` without the `-F` flag, so the SNI-map .db stored literal file paths instead of base64-embedded PEM contents — Postfix smtpd then crashed at TLS handshake with "malformed BASE64 value". Now uses `postmap -F` and drops a certbot deploy hook (`renewal-hooks/deploy/bzpanel-mail-sni.sh`) that re-runs the postmap + reloads postfix/dovecot after every renewal so the SNI cert stays in sync with on-disk PEM.
- **3.0.36** — `bzpanel mail-ssl` writes an nginx helper vhost for `mail.<domain>` on port 80 BEFORE calling certbot. Without it, the HTTP-01 challenge GET hits the panel's catch-all vhost which 404s any unmatched Host header → certbot fails even with correct DNS. The helper vhost stays in place after issuance to handle renewals + 301 HTTP→HTTPS for browser visitors.
- **3.0.35** — Mail Client Setup modal (WHM + cPanel) gains a port/encryption pairing table — Gmail "SSL" = 465 implicit-TLS, Gmail "TLS" = 587 STARTTLS — fixing the "Couldn't connect to server" error that the wrong combination produces. `bzpanel mail-ssl` adds a public DNS pre-flight via `dig @1.1.1.1` so a wrong-IP A record fails fast with a clear message instead of a 30-second certbot timeout.
- **3.0.34** — `bzpanel mail-ssl <domain>` (bsp menu 12). Issues a Let's Encrypt cert for `mail.<domain>` and wires Postfix `tls_server_sni_maps` + Dovecot `local_name` SNI dispatch. Fixes "Authentication error" in Gmail's "Send mail as" wizard caused by the default snake-oil TLS cert — strict clients abort the handshake BEFORE sending AUTH PLAIN, surfacing as a generic auth error. Also adds an amber callout to the Mail Client Setup modal (WHM + cPanel) explaining the two gotchas: (1) username MUST be the FULL email, and (2) strict clients need a real cert covering `mail.<domain>`.
- **3.0.33** — Mailbox auth fix: webmail auto-login worked but Outlook/Thunderbird IMAP+SMTP failed with the same password. Cause: pre-3.0.33 `CreateMailbox` blindly appended to `/etc/dovecot/users` with no dedupe; on re-create after delete, Mongo's unique-email index rolled back but the dovecot users line stayed. Dovecot logged "User <email> exists more than once" and picked the FIRST match (old hash). `CreateMailbox` is now idempotent (sed-removes prior entries before append) for both `/etc/dovecot/users` and `/etc/postfix/virtual_mailbox_maps`. New `bzpanel heal-mail` (alias `repair-mail`) + bsp menu option 11 dedupes existing installs by keeping only the LAST line per mailbox.
- **3.0.32** — Branding + Reports. **Branding**: new Server Settings card uploads panel name / logo / favicon (capped 256 KB; stored as data: URLs in `server_config`); WHM + cPanel + login pages + browser tab all read it from the public `/api/v1/branding` endpoint and swap chrome live. **Reports**: new `/reports` WHM page parses nginx access logs and lists top 50 IPs, top 50 URL paths, and per-domain traffic; backed by `GET /api/v1/whm/resources/traffic-stats?domain=<optional>`.
- **3.0.31** — Subdomain create now apex-wins (shortest-suffix first) so stale `dns_zones` rows from pre-3.0.24 buggy `GetOrCreateZone` no longer hijack the lookup. Live repro: `dns_zones={thewaapi.com, api.usersbug.thewaapi.com}` (second is orphan) used to route `dev.api.usersbug.thewaapi.com` through the orphan and land the A record at the wrong name (or vanish entirely if the orphan had no pdns SOA). Now `parentZoneOf` walks shortest-first and the apex wins. Plus `bzpanel heal-dns` prunes orphan `dns_zones` rows whose domain has no SOA in PowerDNS, and `GetOrCreateZone` refuses to silently mint Mongo rows for non-pdns zones — closing the leak path at the source.
- **3.0.30** — Three coupled fixes diagnosed by live VPS probe: (1) `AutoBootstrap` rejected as bad-syntax when `DOMAIN` was an IP — Postfix returned `501 5.1.7` for every send because `admin@<bare-ip>` isn't valid per RFC 5321. New `isUsableMailDomain` predicate filters IPs / localhost / single-label hosts and falls back to `os.Hostname()`. Stale auto-bootstrap mail configs with bad FromAddr now self-heal on next boot (operator-owned configs untouched). (2) `Domain.Create` swallowed `AddRecord` failures to stderr-only — operators saw "site created" while DNS was never wired. Failures now write structured zerolog entries so `journalctl` surfaces them. (3) New `bzpanel heal-dns` (alias `repair-dns`) + menu option 10 — backfills A + www CNAME for orphan subdomain rows from pre-v3.0.24 installs. Idempotent.
- **3.0.29** — `bzpanel deploy` (alias `update` / `upgrade`) and `bzpanel rebuild` subcommands close the gap that left every previous patch stranded on GitHub. Diagnostic from the user's session: VPS binaries were 4–25 patches behind because the auto-deploy GitHub workflow targets a stale VPS_HOST secret on most installs. `bzpanel deploy` runs `git fetch + reset --hard origin/<branch>` (with auto-stash so hand-edits aren't lost) then chains into `rebuild` (server + agent + bzpanel + seed + systemctl restart). Interactive `bsp` menu gains options 8 (Deploy from GitHub) and 9 (Rebuild from on-disk source). install.sh now also creates `/opt/go/bin/go` as a version-independent symlink so the rebuild flow doesn't need to know `/opt/go/<minor>/bin/go`.
- **3.0.28** — `bsp` admin CLI now lowercases the super admin email before saving (was writing the typed string verbatim, breaking login post-3.0.27 when the typed login email gets lowered for the lookup). `bsp admin-password` and `bsp info` / `bsp` menu now also idempotently heal any mixed-case admin row left over from a pre-3.0.28 install — running any of them on a broken install fixes login on the spot and prints the before/after.
- **3.0.27** — Two related auth bugs: (1) Login was case-sensitive on email — typing `Admin@…` against a stored `admin@…` returned "invalid credentials" because every other auth path lowercased but `LoginWithUA` did not. (2) Mailer auto-bootstrap silently skipped on every fresh install because `DOMAIN` defaults to `localhost`; password-reset / OTP emails dead-lettered into journalctl. AutoBootstrap now falls back to `os.Hostname()` so install.sh-provisioned VPSes wire their local Postfix relay automatically.
- **3.0.26** — User Panel Email page reaches per-row parity with WHM: View Details (quota/limits/SSL+non-SSL connection cheat-sheet/dates), Edit Configuration (quota/send-limit/new password), and Mail Client Setup (Outlook/Thunderbird/Gmail/Apple-Mail how-to). Action row order matches WHM byte-for-byte. Backend endpoints already existed; frontend-only port.
- **3.0.25** — Side-by-side regression test (`TestParentZoneOf_BugDivergence`) runs the user's exact `abc.abc.xyz.qwe.com` input through both predicates: OLD (queries `domains`) reproduces `parent=abc.xyz.qwe.com / name=abc`, NEW (queries `dns_zones`) yields `parent=qwe.com / name=abc.abc.xyz`. Any future re-pointing of `findParentDomain` back at the wrong collection now trips a named failure.
- **3.0.24** — Subdomain create no longer slices a multi-label name down to its leading segment when an intermediate panel subdomain shares the suffix. `findParentDomain` now queries `dns_zones` (the source of truth for "this domain has its own DNS authority") instead of `domains` (which holds both apex and panel-subdomain rows). Creating `abc.abc.xyz.qwe.com` against an apex `qwe.com` now correctly lands the A record as `abc.abc.xyz` in `qwe.com`, even when an earlier `abc.xyz.qwe.com` subdomain row exists. Delegated subdomain zones still win (most-specific-wins iteration preserved). Pure `parentZoneOf` helper extracted with seven Go tests.
- **3.0.23** — Shared `PasswordInput` component lands a Generate (CSPRNG-based, 16 chars) + show/hide toggle on every "set a password" field across both SPAs (mailbox, team member, vendor, WP admin / users, manual-mode DB, DB-owner / DB-user rotate, HTTP Basic Auth). External-credential fields (Git PAT, SMTP relay, SSH/SFTP backup destination, server-migration source) get the same component with the dice button hidden — eye toggle stays for paste verification.
- **3.0.22** — WordPress install pipeline overhaul: placeholder `index.html` removed before `wp core download` (was shadowing `index.php` so installed sites still served the placeholder), `--version`/`--locale` from the wizard now reach wp-cli, every dynamic arg POSIX-quoted (passwords / titles with apostrophes no longer break the install), `--skip-email` so installs don't hang on the local MTA, mkdir/chown errors propagate, sudo `-H` so wp-cli's `~/.wp-cli/cache` writes don't EACCES on hosts whose sudoers keeps HOME.
- **3.0.21** — RDAP-first WHOIS lookup so `.in` (and other modern CC-TLDs whose port-43 service is unreliable) get expiry / registrar / nameservers populated on the WHM Domains page.
- **3.0.20** — Transfer Databases now writes the panel password to the destination's `databases` row, restoring phpMyAdmin auto-login post-migration.
- **3.0.19** — MongoDB database creation temporarily disabled (broken on default installs); phpMyAdmin auto-login self-heals (`signon-secret` + `_signon.php` shim) during the transfer self-heal pass.
- **3.0.16-3.0.18** — Database transfer overhaul: MySQL user passwords preserved end-to-end, IP-allowlist (`db_access_hosts`) carries over, MySQL host-scoped GRANTs reissued, mongorestore moved off deprecated flags, auth-aware mongodump.
- **3.0.15** — Server-to-server DNS transfer preserves third-party records: subdomain NS delegations carry over, third-party A values aren't rewritten when only the source IP should flip, SPF preserves sibling TXTs at the same name.
- **3.0.13-3.0.14** — DNS rrset TTL unified across siblings (RFC 2181 §5.2 compliance); WHM Zone Records type-chip filter hardened against shape drift.
- **3.0.7-3.0.12** — DNS records page rewritten around Mongo-as-source-of-truth: `pdnsutil replace-rrset` reconcile on every write, idempotent delete (no more "record not found" on consecutive deletes of multi-value rrsets), heal-on-read for records that landed in PowerDNS without a Mongo backing, name canonicalization (FQDN / FQDN-with-dot / relative all collapse), `POST /dns/zones/:domain/reconcile` heal endpoint.
- **3.0.2-3.0.6** — Deploy Software multi-domain for every service role + transfer recovery preserves alias domains; Edit-Service modal gains Domains section; Database Create button no longer requires the optional Domain field.
- **3.0.1** — OTP magic-link cross-browser handoff: clicking the emailed link in a different browser approves sign-in in the originating browser instead of failing.

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
             MongoDB 8.0+                PM2 + systemd units
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
| Database | MongoDB 8.0+ with `authSource=admin` |
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
4. **MongoDB 8.0** — adds the MongoDB APT repo, installs, enables `mongod`, creates a local `admin` user, sets `authSource=admin`.
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
# -> { "name": "Betazen Server Panel", "version": "3.0.0", ... }

# 4) Public HTTP answers
curl -sI http://<your-ip>/ | head -5
# -> HTTP/1.1 200 OK, Server: nginx, ...

# 5) Panel DB reachable
systemctl status mongod --no-pager | head -10
mongosh --quiet --eval 'db.adminCommand({ ping: 1 })' mongodb://127.0.0.1/?authSource=admin -u <admin> -p <pass>
```

If any of the five fail, check `journalctl -u serverpanel -n 80 --no-pager` first — it's the single best signal.

Once everything is green, run the admin console for an at-a-glance sanity check of the panel state:

```bash
bsp        # interactive numbered menu: admin email, password, domain, SSL, restart…
```

Full reference (every option, scripted equivalents, files it touches, security model, troubleshooting): [`docs/bsp-admin-console.md`](./docs/bsp-admin-console.md).

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
# Prereqs: Go 1.22+, Node 18/20/22 LTS, MongoDB 8+, npm 10+, make 4+

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

### 7.1 Helper scripts in `scripts/`

The repo ships a small kit of paramiko-driven helpers for VPS deploys, smoke tests, and one-shot diagnostics. Anything starting with `_` is a private/dev helper — committed for reproducibility but not part of the user-facing API surface.

| Script | Purpose |
|---|---|
| `_deploy_and_test.py` | Pull `main` on the VPS, rebuild backend + both SPAs, install the binary at the systemd `ExecStart` path, rsync dists into `WorkingDirectory`, restart `serverpanel`, run smoke tests. |
| `_redeploy_binary.py` | Backend-only fast path (skip frontend rebuild). |
| `_smoke_test.py` | curl-level checks against the deployed OTP / DNS endpoints. |
| `_e2e_two_browser.py` | Full simulated cross-browser OTP handoff: Browser A captures cookie, Browser B verifies without cookie, asserts only Browser A's `/complete` succeeds. |
| `_test_dns_*.py` | DNS rrset reconcile tests: corruption-state delete, dup-fix, multi-value preservation, transfer DNS, TTL unify, user scenario. |
| `_test_db_transfer.py` | End-to-end MySQL DB transfer with autologin + IP-allowlist assertions. |
| `_test_transfer_dns_preserve.py` / `_test_ip_repoint.py` | Transfer-time A-record / SPF / NS preservation across third-party values. |
| `_diag_*.py`, `_probe_*.py` | One-shot probes used during specific bug investigations. |

Credentials come from `BZ_VPS_PASS` env var, falling back to a gitignored `testing-vps-details.md` at the repo root. Never hard-coded. The file format the helpers parse is:

```md
## Old server
- Host: `x.x.x.x`
- Password: `…`

## New server
- Host: `y.y.y.y`
- Password: `…`
```

Set `BZ_VPS_HOST` and `BZ_VPS_SECTION` (e.g. `New`) to target a different box than the default `Old`.

---

## 8. Upgrading

### 8.1 Fast path (production)

Pull, rebuild the panel **and the Mail Suite app**, and restart both — so the
standalone Gmail/Zoho-style mail backend + webmail upgrade in lockstep with the
panel. The Mail Suite block is guarded so it's a no-op on boxes without it.

> **Order matters — especially from the WHM Terminal.** That terminal is served
> by the panel itself, so `systemctl restart serverpanel` **ends your terminal
> session**. This block therefore builds everything first, upgrades **Mail Suite
> before** the panel, and restarts the panel **last** — so even from the WHM
> Terminal, Mail Suite is fully done before the session drops. (Over direct SSH
> nothing drops.)

```bash
cd /opt/serverpanel
sudo git fetch --quiet origin main
sudo git reset --hard origin/main

# 1) Build the panel binary + frontends (nothing restarted yet).
sudo /opt/go/1.23/bin/go -C backend build -o /opt/serverpanel/bin/server ./cmd/server
( cd /opt/serverpanel/frontend && sudo npx turbo build )

# 2) Mail Suite — build + restart it BEFORE the panel restart (below), so a WHM
#    Terminal session survives long enough to finish it. No-op if not installed.
if [ -d /opt/mail-suite ]; then
  sudo /opt/go/1.23/bin/go -C mail-suite/backend build -o /opt/mail-suite/mail-suite ./cmd/server
  ( cd /opt/serverpanel/mail-suite/webmail && sudo npm install --no-audit --no-fund && sudo npm run build && sudo cp -r dist/. /opt/mail-suite/webmail/ )
  sudo systemctl restart mail-suite
  curl -s http://127.0.0.1:9090/healthz; echo   # expect {"ok":true,...}
fi

# 3) Restart the panel LAST. From the WHM Terminal THIS LINE ENDS YOUR SESSION
#    (the panel serves that terminal) — that is expected; steps 1-2 are already
#    done. Over SSH the session stays and you can check the version below.
sudo systemctl restart serverpanel
sleep 2; curl -s http://127.0.0.1:8080/api/v1/version; echo
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

# Roll Mail Suite back to the same checkout too (if installed):
if [ -d /opt/mail-suite ]; then
  sudo /opt/go/1.23/bin/go -C mail-suite/backend build -o /opt/mail-suite/mail-suite ./cmd/server
  ( cd /opt/serverpanel/mail-suite/webmail && sudo npm install --no-audit --no-fund && sudo npm run build && sudo cp -r dist/. /opt/mail-suite/webmail/ )
  sudo systemctl restart mail-suite
fi
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

There is **no** general-purpose "import from cPanel / CyberPanel / Plesk" wizard at this time. If you are migrating off a non-Betazen Server Panel install, plan for per-service migration (mail with `imapsync`, DNS via AXFR, sites via rsync).

### 9.1 Post-migration healing

The wizard's panel-records sync auto-runs the rehydrate orchestrator on completion. If you upgraded to v3.1.66 AFTER a migration (so the destination's files are stale or unconfigured), or you notice a specific symptom listed below, run the matching one-shot heal on the **destination** box. Every command is idempotent — safe to re-run on a healthy install, reports `0 changes`.

| Symptom | Command | What it does |
|---|---|---|
| `https://www.<d>` 404s on a Deploy-Software project | `bzpanel heal-www` | Walks every panel domain, adds `www.<d>` + `cname.<d>` to nginx `server_name` + reissues the Let's Encrypt cert with both as additional SANs. |
| Subdomain `www.<sub>` CNAME missing in PowerDNS | `bzpanel heal-dns` | Backfills A + www CNAME records for any subdomain row that lacks them. |
| Inbound mail bounces with `open(...tmp/...) failed: Permission denied` | `bzpanel heal-mailboxes` (or `heal-mail-perms`) | Rebuilds `/etc/dovecot/users` + `virtual_mailbox_maps` from Mongo AND chowns every migrated `/home/<u>/mail/` tree to `vmail:vmail` (v3.1.62+). |
| `/etc/dovecot/users` has dupes (post a re-create-after-delete) | `bzpanel heal-mail` | Dedupes both `/etc/dovecot/users` + `/etc/postfix/virtual_mailbox_maps`, keeping the last entry per mailbox. |
| "Open in Webmail" arrow returns Roundcube "Internal error" toast | `bzpanel heal-mail-sso` (v3.1.62+) | Clears `encrypted_pass` blobs sealed under the source's `JWT_SECRET`. Panel UI then renders a clean "Set password to enable SSO" CTA; user resets password from panel → SSO re-arms. IMAP/SMTP login untouched. |
| WHM Deploy-Software activity list shows in-progress spinner on finished deploys; success badge reads 0 | `bzpanel heal-deployments` (v3.1.66+) | Relabels `status="running" + finished_at + no error_msg` → `status="success"` AND backfills empty `commit_sha` from the matching service's `last_commit_sha`. |
| `mail.<d>` cert missing → strict SMTP clients reject TLS | `bzpanel mail-ssl-sweep` | Walks every panel-tracked domain and runs `mail-ssl` for each; cron-safe + idempotent. Domains whose public DNS doesn't yet resolve to this box are skipped (picked up on the next sweep). |
| Mongo rows look right but underlying state is dead (DNS NXDOMAIN, MySQL access denied, FTP auth failed, WordPress DB error) | `bzpanel heal-after-transfer` | Runs every `Rebuild*FromMongo` in one pass (mailboxes, forwarders, SSH keys, DNS, MySQL, FTP, WordPress configs) AND refreshes the dovecot sieve conf (v3.1.65+). |

**One-stop recipe** for a freshly-migrated install:

```bash
bzpanel deploy                  # pull + rebuild + restart to the current main
bzpanel heal-after-transfer     # rebuild every Mongo-backed service's on-disk state
bzpanel heal-www                # backfill www / cname on every domain's vhost + cert
bzpanel heal-mail-sso           # clear stale webmail SSO blobs
bzpanel heal-deployments        # fix the pre-3.1.66 stuck-at-running rows
```

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
| `PUBLIC_WEBHOOK_BASE_URL` | Public URL GitHub posts to for Deploy Software auto-deploy. Defaults to `https://<DOMAIN>` when blank. |
| `DEPLOY_WORKERS` | How many Deploy Software service deploys run in parallel across the panel. Default `4`; clamped to `[1, 32]`. Raise on bigger boxes with multi-service projects so a webhook fan-out doesn't queue-stall behind the worker pool. Added in v3.1.86. |
| `RATE_LIMIT_WHM` / `RATE_LIMIT_CPANEL` | Per-IP requests-per-minute on the WHM / cPanel API surfaces. Defaults 200 / 100. |

Edits take effect on `sudo systemctl restart serverpanel` (config is read once at boot, no hot-reload). Confirm a `DEPLOY_WORKERS` change took effect with:

```bash
journalctl -u serverpanel --since "1 min ago" | grep "project deploy pool"
# project deploy pool started workers=8 queue_buffer=256
```

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
