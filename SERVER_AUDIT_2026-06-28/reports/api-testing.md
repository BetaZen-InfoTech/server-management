# AGENT 13 — API Testing Baseline

**Audit date:** 2026-06-28
**Scope:** REST API read-endpoint baseline on BOTH VPS clones, pre-demo-data / pre-migration.
**Servers:** Server 1 = `89.116.34.207` (migration SOURCE) · Server 2 = `195.35.7.64` (migration DEST)
**Deployed code:** local git repo `c:/Users/Administrator/Downloads/Project/server-management` @ v3.1.109 (rev 466b52e)
**Method:** READ-ONLY. Logged in with the admin (vendor_owner) and demo (customer) accounts, exercised GET endpoints, recorded HTTP status + response shape, and checked auth/RBAC enforcement. **No POST/PUT/PATCH/DELETE/mutating call was made.**

---

## 1. How endpoints were enumerated

Routes were read directly from the deployed source (faster + authoritative):

- `backend/internal/routes/whm_routes.go` — `/api/v1/whm/*` (group gate: `Auth` → `InjectScope` → `RequireRole(vendor_owner, vendor_admin, vendor_staff, developer, support)` → `RateLimiter` → `AuditLogger`).
- `backend/internal/routes/cpanel_routes.go` — `/api/v1/cpanel/*` (group gate: `Auth` → `InjectScope` → `RequireRole(vendor_admin, vendor_staff, developer, support, customer)` → `RateLimiter`). **Note: no `AuditLogger` on the cpanel group.**
- `backend/internal/routes/api_routes.go` — `RegisterDeveloperRoutes` (`/developer/*` under both groups) + `/api/v1/external/*` (API-token surface; not JWT, not exercised here).
- `backend/internal/routes/auth_routes.go` — `/api/v1/auth/*` (login/refresh/2FA/OTP/me).
- `backend/cmd/server/main.go` (lines 453-482) — the public, unauth root endpoints: `/api/v1/version`, `/api/v1/public-settings`, `/api/v1/branding`, `/api/v1/home-page`.

Mapping note for the assignment's named endpoints:
- **"ssl-certificates"** → `GET /api/v1/whm/ssl/` (and `/cpanel/ssl`). There is no `/ssl-certificates` path.
- **"hosting-packages"** → `GET /api/v1/whm/packages/` (and `/cpanel/packages/`).
- **"settings/branding/public-settings"** → public `GET /api/v1/branding`, `GET /api/v1/public-settings`; authenticated mirrors at `/api/v1/whm/config/branding`, `/config/ui-settings`.
- **"users/accounts"** → `GET /api/v1/whm/users/` (platform staff) and `GET /api/v1/whm/admin/vendors/` (tenant roots).
- There is **no `/health`** route — `GET /health` returns 404 (the health probe lives elsewhere; the panel exposes `/api/v1/version` for liveness).

---

## 2. Commands run

**Tokens (both servers):**
```bash
ADMIN_TOK=$(curl -s -X POST http://127.0.0.1:8080/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"admin@betazeninfotech.com","password":"admin123"}' | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
DEMO_TOK=$(curl -s -X POST http://127.0.0.1:8080/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"demo@betazeninfotech.com","password":"demo123"}'  | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
```
- `admin@betazeninfotech.com / admin123` → role `vendor_owner` (token len 1395). Login OK on both.
- `demo@betazeninfotech.com  / demo123`  → role `customer`     (token len 687).  Login OK on both.

Each endpoint was hit with:
```bash
curl -s -w "\n%{http_code}" -H "Authorization: Bearer $TOK" "http://127.0.0.1:8080<path>"
```
Full driver script: `scratchpad/apitest.sh`, run via `bz.py 1 --file ...` and `bz.py 2 --file ...`. ~120 distinct calls per server.

---

## 3. Results — both servers IDENTICAL unless noted

### 3.1 Public / unauthenticated (no token) — all 200 except /health
| Endpoint | Status | Shape (evidence) |
|---|---|---|
| `GET /api/v1/version` | **200** | `{"data":{"name":"Betazen Server Panel","version":"3.1.109","major":3,"minor":1,"patch":109},"success":true}` |
| `GET /api/v1/public-settings` | **200** | `{"success":true,"data":{"show_demo_login_credentials":true,"show_demo_transfer_settings":true}}` |
| `GET /api/v1/branding` | **200** | `{"success":true,"data":{"panel_name":"Betazen Server Panel"}}` |
| `GET /api/v1/home-page` | **200** | `{"success":true,"data":{"enabled":false,"hero_title":"Welcome",...}}` |
| `GET /health` | **404** | `{"error":{"code":"NOT_FOUND","message":"Cannot GET /health"},"success":false}` — route does not exist (expected) |

### 3.2 Auth enforcement — no token → 401 (correct)
All four sampled protected endpoints returned **401** with `{"code":"UNAUTHORIZED","message":"Missing authorization header"}`:
`/api/v1/whm/dashboard/stats`, `/api/v1/whm/domains/`, `/api/v1/cpanel/dashboard/stats`, `/api/v1/whm/resources/summary`.
Garbage bearer token → **401** `{"code":"UNAUTHORIZED","message":"Invalid or expired token"}`.

### 3.3 WHM read endpoints (vendor_owner token) — 70/70 returned 200
All returned `200` with `{"success":true,...}`. Representative evidence:

| Endpoint | Status | Shape (key fields) |
|---|---|---|
| `whm/resources/summary` | 200 | `data:[{path:"/",used:8,total:387,percent:2},{path:"/var/www"...},{path:"/var/lib/mongodb"...},{path:"/var/mail"...}]` |
| `whm/dashboard/stats` | 200 | `{totalDomains:0,activeApps:0,databases:0,sslCertificates:0}` |
| `whm/dashboard/server-status` | 200 | `{cpuPercent,memoryPercent,diskPercent,uptimeString}` |
| `whm/domains/` | 200 | `data:[]` + `pagination:{page:1,limit:20,total:0}` |
| `whm/domains/expiring/buckets` | 200 | `{buckets:[60,45,30,15,7,5,4,3,2,1],counts:{...all 0}}` |
| `whm/email/` | 200 | `data:[]` paginated |
| `whm/email/forwarders` | 200 | `data:[]` |
| `whm/email/logs` / `logs/stats` | 200 | `data:[]` / `{total:0,by_status:{},by_direction:{},by_source:{},window_days:7}` |
| `whm/dns/zones` | 200 | `data:[]` |
| `whm/apps/` / `apps/presets` | 200 | `[]` / full framework preset map (go-chi, go-echo, ...) |
| `whm/databases/` | 200 | `data:[]` paginated |
| `whm/packages/` | 200 | `data:[{name:"Default",disk_quota_mb:5120,bandwidth_mb:102400,...}]` (the 1 seeded package) |
| `whm/ssl/` | 200 | `data:[]` |
| `whm/audit/` | 200 | `data:[{action:"login.success",resource_type:"auth",user:{email:"demo@...",role:"customer"}}]` |
| `whm/users/` | 200 | `data:[{name:"Demo User",email:"demo@...",role:"viewer"},{...admin...}]` (see Finding A) |
| `whm/admin/vendors/` / `vendors/stats` | 200 | `[]` / all-zero stats |
| `whm/software/installed` | 200 | `[{id:"php8.2",version:"8.2.31"},{id:"mongodb",latestVersion:"8.0.26"},...]` |
| `whm/software/runtimes` | 200 | go/node/python/ruby version matrices (go 1.23 installed) |
| `whm/monitor/system` | 200 | `{cpu_count:8,cpu_model:"AMD EPYC 9354P...",hostname:"srv1785162"(S1)/"srv1789639"(S2),ip:...}` |
| `whm/monitor/services` | 200 | nginx/mongod/postfix/dovecot... all `active:true` |
| `whm/monitor/metrics` / `server-info` / `service-status` | 200 | live CPU/mem/load; processor list; per-service version+status |
| `whm/firewall/status` / `rules` / `fail2ban/status` / `blocked-ips` | 200 | ufw enabled, default deny-in; `[]`; sshd jail 0 bans; `[]` |
| `whm/config/` | 200 | `{hostname:"srv1785162.hstgr.cloud"(S1)/"srv1789639.hstgr.cloud"(S2),timezone:"Etc/UTC"}` |
| `whm/config/mysql/databases` | 200 | `["roundcube"]` |
| `whm/config/mongodb/status` | 200 | `{version:"8.0.26",uptime,connections_current:11,...}` |
| `whm/config/mongodb/databases` | 200 | `[admin,config,local,serverpanel]` |
| `whm/config/mongodb/users` | 200 | `[{user:"admin",roles:["root@admin"]},{user:"serverpanel",roles:["readWrite@serverpanel","dbAdmin@serverpanel"]}]` |
| `whm/config/panel-domain` | 200 | `{domain:"89.116.34.207"(S1)/"195.35.7.64"(S2),ssl_active:false}` |
| `whm/diagnostics/mail-stack` | 200 | structured `checks[]` (dovecot-imapd pass, etc.) |
| `whm/maintenance/` | 200 | `{domains:null,server:{enabled:false}}` |
| `whm/transfers/` / `transfers/tokens` | 200 | `[]` / `[]` |
| `whm/projects/` / `projects/services` | 200 | `[]` paginated |
| `whm/developer/tokens/` `/scopes` `/webhooks/` `/events` `/deliveries` | 200 | `[]` / scope catalog / `[]` / event catalog / `null` |
| `whm/mail-suite/deployments` | 200 | `[]` |

Full per-call output captured in the run logs of `scratchpad/apitest.sh`.

### 3.4 cPanel read endpoints (customer/demo token) — 28/31 returned 200, 1×404, 2×403 (all expected)
| Endpoint | Status | Note |
|---|---|---|
| `cpanel/dashboard/stats` | 200 | `{domains:0,apps:0,databases:0,storageUsed:"0 GB",storageTotal:"50 GB",emailAccounts:0,sslCerts:0}` |
| `cpanel/dashboard/activity` | 200 | own login event |
| `cpanel/domains` | 200 | `[]` paginated |
| `cpanel/apps` / `apps/presets` | 200 | `[]` / preset map |
| `cpanel/databases` | 200 | `[]` paginated |
| `cpanel/email` / `forwarders` / `logs` / `logs/stats` | 200 | all empty/zeroed |
| `cpanel/ssl` | 200 | `[]` |
| `cpanel/backups` | 200 | `[]` paginated |
| **`cpanel/backups/schedules`** | **404** | `{"code":"NOT_FOUND","message":"Backup not found"}` — see Finding B (route-ordering) |
| `cpanel/wordpress` | 200 | `[]` |
| `cpanel/dns/zones` | 200 | `[]` |
| `cpanel/deploy` / `projects/` / `projects/services` | 200 | empty |
| `cpanel/ssh-keys/` | 200 | `[]` |
| `cpanel/audit` | 200 | own actions only |
| `cpanel/packages/` / `my-package` / `my-request` | 200 | catalog / Default pkg / `null` |
| `cpanel/notifications/settings` / `history` | 200 | defaults |
| `cpanel/cron` | 200 | `[]` |
| **`cpanel/logs/files`** | **403** | `{"code":"FORBIDDEN","message":"Missing permission: log.view"}` — customer lacks `log.view` (correct) |
| **`cpanel/users/`** | **403** | `{"code":"FORBIDDEN","message":"Missing permission: user.create"}` — customer can't manage a team (correct) |
| `cpanel/developer/tokens/` `/scopes` `/webhooks/` | 200 | `[]` / scope catalog / `[]` |

### 3.5 RBAC enforcement (cross-surface) — all correct
| Scenario | Sample paths | Status | Body |
|---|---|---|---|
| **cpanel(customer) token → WHM** | `/whm/dashboard/stats`, `/whm/domains/`, `/whm/resources/summary`, `/whm/config/` | **403** | `{"code":"FORBIDDEN","message":"Insufficient role for this action"}` |
| **admin(owner) token → cPanel** | `/cpanel/dashboard/stats`, `/cpanel/domains` | **403** | `{"code":"FORBIDDEN","message":"Insufficient role for this action"}` |

Login is correctly split per CLAUDE.md: vendor_owner is rejected from the cPanel surface and the customer is rejected from WHM, enforced at the `RequireRole` group middleware (role 403) before any handler runs.

### 3.6 Errors / panics
- **No 5xx on any endpoint** across either server.
- `journalctl -u serverpanel --since "5 min ago" | grep -iE "panic|error|fatal|level=error"` returned **nothing** on both servers during the test window — no handler panics, no logged errors.

---

## 4. Findings

### Finding A — `users/` list reports the customer role as `viewer` (INFO, by design)
`GET /api/v1/whm/users/` shows `demo@betazeninfotech.com` with `role:"viewer"`, while its JWT and the audit log show `role:"customer"`. This is **not a data bug** — it is an intentional display mapping in `backend/internal/handlers/user_handler.go:63 mapRoleToFrontend()`:
`vendor_owner→admin, vendor_admin→vendor, vendor_staff→staff, developer/support→operator, customer→viewer`. The underlying DB role is unchanged (Mongo + JWT both say `customer`). Worth knowing so this isn't mistaken for role drift after the migration. Identical on both servers.

### Finding B — `GET /cpanel/backups/schedules` 404s due to route ordering (LOW, repo bug, present on both)
On cPanel, `GET /backups/:id` is registered (cpanel_routes.go:181) **before** `GET /backups/schedules` (cpanel_routes.go:186), so Fiber's literal-vs-param matcher treats `schedules` as a backup `:id` and the handler returns `404 "Backup not found"`. The WHM group registers `/backups/schedules` (whm_routes.go:336) **before** `/backups/:id` (whm_routes.go:339), which is why the WHM equivalent returns 200. Same defect on both servers (same code). This is a pre-existing source ordering issue, not runtime drift. Low impact (backup schedules are not a migration-critical read), but the cPanel "Backup Schedules" view cannot load via this path. Fix = move the two `/backups/schedules` registrations above `/backups/:id` in `cpanel_routes.go`, mirroring the WHM ordering. NOT auto-fixed (read-only audit; requires a code change + redeploy).

### Finding C — Source/Dest API surface is in lockstep (INFO)
Every endpoint returned the same status and same shape on both servers; the only differences are expected per-host values (hostname `srv1785162` vs `srv1789639`, IP `89.116.34.207` vs `195.35.7.64`, live CPU/uptime numbers, and per-run audit/object IDs). Both run v3.1.109, MongoDB 8.0.26, PHP 8.2.31, nginx 1.24.0, go 1.23. No API-level drift. This is a clean baseline to compare against after demo-data load + migration.

### Finding D — Data state matches the stated baseline (INFO)
Every list endpoint (domains, mailboxes, forwarders, dns zones, apps, databases, ssl, backups, projects, transfers, mail-logs, webhooks, api-tokens, vendors) returned empty on both servers; `packages/` returned the single seeded "Default" package; `users/` returned the 2 seeded accounts (admin + demo). This corroborates the documented current data state and confirms the read endpoints correctly report "empty" rather than erroring on empty collections.

---

## 5. Conclusion

The API read surface is **healthy and identical on both VPS clones**. Auth (401 on no/invalid token), RBAC role-split (403 cross-surface), and per-permission gating (403 on `log.view`/`user.create` for a customer) all enforce correctly. No 5xx, no panics, no logged errors. One pre-existing low-severity route-ordering bug (`/cpanel/backups/schedules` → 404, Finding B) exists in the deployed code on both servers; everything else returns 200 with the expected shape. This establishes a solid API baseline before demo-data injection and the source→dest migration.
