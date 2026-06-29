# 11 — API Testing

**Agent:** 13 — API Tester
**Target:** BetaZen Server Panel (bPanel) demo box "Server 1" — `89.116.34.207`, API at `http://127.0.0.1:8080`
**Panel version:** 3.1.112 (confirmed live via `GET /api/v1/version`)
**Owner identity:** `admin@betazeninfotech.com` (role `vendor_owner`)
**Date:** 2026-06-29

---

## Summary

- **63 distinct requests** exercised across auth, RBAC, WHM read surface, read-style POSTs, error handling, and timing.
- **WHM read surface: 49/49 endpoints returned HTTP 200 with `success: true`** and a well-formed `data` payload. No 500s on any GET.
- **Auth works end to end:** login (200), wrong password (401), no token (401), bad token (401), refresh-token rotation (200). Token field names are `snake_case` (`access_token`, `refresh_token`, `expires_in`, `token_type`, `user`) as required.
- **RBAC behaves exactly per design:** owner (`vendor_owner`) is correctly **rejected with 403 FORBIDDEN** on every `/api/v1/cpanel/*` endpoint (User Panel is non-owner only), and WHM endpoints reject unauthenticated/garbage tokens with 401.
- **Standardized error envelope** `{"success":false,"error":{"code","message"[,"details"]}}` is consistent across 400/401/403/404/500.
- **One unexpected behavior:** `POST /whm/transfers/test-connection` to an unreachable host returns **HTTP 500 INTERNAL_ERROR** for what is a normal user-input-driven connection failure (should arguably be a 4xx or a 200 with a failure flag). See Issues.
- Response times are excellent (sub-10ms for DB-backed lists; ~285ms worst case for live `monitor/metrics`, which shells out to the box).

**Pass/fail tally:** 60 behaved as expected (pass). 0 broke functionally. 3 are "behaved-but-flagged" (the test-connection 500; the 401-before-404 ordering on guarded groups — by design but worth noting; the public `version` endpoint omits the standard envelope shape — cosmetic).

---

## Auth / RBAC Results

| Check | Request | Expected | Actual | OK? |
|---|---|---|---|---|
| Login success | `POST /api/v1/auth/login` (valid creds) | 200 + tokens | 200, `data{access_token,refresh_token,expires_in,token_type,user}` | ✅ |
| Login wrong password | `POST /api/v1/auth/login` (bad pw) | 401 | 401 `UNAUTHORIZED "invalid email or password"` | ✅ |
| Protected, no token | `GET /api/v1/whm/dashboard/stats` | 401 | 401 `UNAUTHORIZED "Missing authorization header"` | ✅ |
| Protected, bad token | `GET /api/v1/whm/domains/` (garbage Bearer) | 401 | 401 `UNAUTHORIZED "Invalid or expired token"` | ✅ |
| Protected, valid token | `GET /api/v1/auth/me/` | 200 | 200, `data{email,id,name,role,username}` | ✅ |
| Refresh flow | `POST /api/v1/auth/refresh` (refresh_token) | 200 + new pair | 200, full new token set issued | ✅ |
| Sessions list | `GET /api/v1/auth/me/sessions` | 200 | 200, `data=list[31]` login-history rows | ✅ |
| **RBAC: owner on cpanel** | `GET /api/v1/cpanel/dashboard/stats` (+ domains, email, ssl) | 403 (by design) | **403 `FORBIDDEN "Insufficient role for this action"`** on all 4 | ✅ (correct per design) |
| RBAC: WHM unauth | `GET /api/v1/whm/domains/` (no token) | 401 | 401 | ✅ |

**Token note:** the JWT is ~4h-lived; `expires_in` returned in the login body. Refresh returns a brand-new access + refresh pair (rotation), so refresh tokens are single-use-style as expected. Token value never printed.

**RBAC design confirmation:** `/api/v1/cpanel/*` is allowlisted to `vendor_admin, vendor_staff, developer, support, customer` and explicitly excludes `vendor_owner` (per `cpanel_routes.go` `RequireRole`). The 403 the owner receives is the intended split-panel behavior — not a bug. WHM group requires one of the staff/owner roles plus per-route `RequirePermission`.

---

## Endpoint Results Table

All rows below are authenticated as `vendor_owner` unless noted. "OK?" = behaved as expected.

### Auth surface

| Endpoint | Method | Auth | Status | OK? | Notes |
|---|---|---|---|---|---|
| `/api/v1/version` | GET | none | 200 | ✅ | `{name, version:"3.1.112", major, minor, patch}` — public; envelope is `{success,data}` but built ad-hoc (see Issues) |
| `/api/v1/auth/login` | POST | none | 200 | ✅ | tokens, snake_case |
| `/api/v1/auth/refresh` | POST | refresh tok | 200 | ✅ | rotates token pair |
| `/api/v1/auth/me/` | GET | JWT | 200 | ✅ | self profile |
| `/api/v1/auth/me/sessions` | GET | JWT | 200 | ✅ | `list[31]` device/IP trail |

### WHM read endpoints (all 200 / success:true)

| Endpoint | Method | Status | Response shape |
|---|---|---|---|
| `/whm/dashboard/stats` | GET | 200 | `{totalDomains, activeApps, databases, sslCertificates}` |
| `/whm/dashboard/activity` | GET | 200 | `list[10]` |
| `/whm/dashboard/server-status` | GET | 200 | `{cpuPercent, memoryPercent, diskPercent, uptimeString}` |
| `/whm/domains/` | GET | 200 | `list[6]` + pagination |
| `/whm/domains/expiring` | GET | 200 | `list[0]` |
| `/whm/packages/` | GET | 200 | `list[1]` |
| `/whm/packages/:id` | GET | 200 | `{id, name, disk_quota_mb, bandwidth_mb, max_ftp_accounts, …}` |
| `/whm/apps/` | GET | 200 | `list[6]` + pagination |
| `/whm/apps/presets` | GET | 200 | `{go-chi, go-fiber, nextjs, node-express, nuxt, …}` |
| `/whm/databases/` | GET | 200 | `list[2]` + pagination |
| `/whm/email/` (mailboxes) | GET | 200 | `list[20]` + pagination |
| `/whm/email/forwarders` | GET | 200 | `list[36]` |
| `/whm/email/logs` | GET | 200 | `list[8]` + pagination |
| `/whm/email/logs/stats` | GET | 200 | `{total, by_status, by_direction, by_source, window_days}` |
| `/whm/dns/zones` | GET | 200 | `list[6]` |
| `/whm/dns/zones/:domain/records` | GET | 200 | `19` records (mail.demo-one.local) |
| `/whm/dns/zones/:domain/export` | GET | 200 | BIND zonefile text (`$ORIGIN …`) |
| `/whm/ssl/` | GET | 200 | `list[0]` |
| `/whm/backups/` | GET | 200 | `list[0]` + pagination |
| `/whm/backups/schedules` | GET | 200 | `list[0]` |
| `/whm/wordpress/` | GET | 200 | `list[0]` |
| `/whm/firewall/status` | GET | 200 | `{enabled, default_incoming, rules_count, fail2ban_active, …}` |
| `/whm/firewall/rules` | GET | 200 | `list[0]` |
| `/whm/firewall/blocked-ips` | GET | 200 | `list[0]` |
| `/whm/software/installed` | GET | 200 | `list[11]` |
| `/whm/software/runtimes` | GET | 200 | `{defaults, go, nodejs, php, python, ruby}` |
| `/whm/monitor/system` | GET | 200 | `{cpu_count, cpu_model, disk_total, hostname, ip, kernel, …}` |
| `/whm/monitor/metrics` | GET | 200 | `{cpu_percent, disk, load_average, memory, network, swap, timestamp}` |
| `/whm/monitor/services` | GET | 200 | `list[8]` |
| `/whm/monitor/server-info` | GET | 200 | `{processors, memory_boot, system, physical_disks, …}` |
| `/whm/monitor/service-status` | GET | 200 | `{services, load_average, cpu_count, memory_total, disks, …}` |
| `/whm/logs/files` | GET | 200 | `list[9]` |
| `/whm/cron/` | GET | 200 | `list[0]` |
| `/whm/processes/` | GET | 200 | `list[50]` |
| `/whm/processes/services` | GET | 200 | `list[208]` |
| `/whm/resources/summary` | GET | 200 | `list[5]` |
| `/whm/resources/bandwidth` | GET | 200 | `data=null` (no bandwidth accounting data yet) |
| `/whm/resources/traffic-stats` | GET | 200 | `{log_file, total_requests:391, total_bytes, …}` |
| `/whm/resources/vendors` | GET | 200 | `list` of per-vendor rollups |
| `/whm/audit/` | GET | 200 | `list[20]` + pagination |
| `/whm/audit/export` | GET | 200 | JSON array attachment (full audit dump) |
| `/whm/config/` | GET | 200 | `{hostname, timezone}` |
| `/whm/config/panel-domain` | GET | 200 | `{domain:"89.116.34.207", server_ip, ssl_active:false}` |
| `/whm/config/mongodb/status` | GET | 200 | `{version:"8.0.26", uptime, connections_current:11, …}` |
| `/whm/config/mongodb/databases` | GET | 200 | `list` of dbs (admin, config, …) |
| `/whm/notifications/settings` | GET | 200 | `{id, email, slack}` |
| `/whm/webhooks/` | GET | 200 | `list[0]` |
| `/whm/transfers/` | GET | 200 | `list[0]` + pagination |
| `/whm/users/` | GET | 200 | `list[2]` + pagination |
| `/whm/admin/vendors/` | GET | 200 | `list[3]` + pagination |
| `/whm/admin/vendors/stats` | GET | 200 | `{active_vendors, total_domains, total_vendors, …}` |
| `/whm/projects/` | GET | 200 | `list[1]` + pagination |
| `/whm/developer/tokens/` | GET | 200 | `list[0]` |
| `/whm/developer/tokens/scopes` | GET | 200 | `list[12]` scope definitions |
| `/whm/developer/webhooks/` | GET | 200 | `list[0]` |
| `/whm/maintenance/` | GET | 200 | `{domains, server}` |
| `/whm/diagnostics/mail-stack` | GET | 200 | `{generated_at, checks, summary}` |

### Read-style POSTs (safe)

| Endpoint | Method | Status | OK? | Notes |
|---|---|---|---|---|
| `/whm/domains/preflight` | POST | 200 | ✅ | Live WHOIS+DNS read for `example.com`; returned registrar/dates. Non-destructive. |
| `/whm/transfers/test-connection` | POST | **500** | ⚠️ | With valid `protocol:ssh` to an unreachable host → `INTERNAL_ERROR "Connection failed: … i/o timeout"`. Functionally probes correctly but maps an expected connection failure to 500 (see Issues). |
| `/whm/backups/test-connection` | POST | 400 | ✅ | Validation envelope listing required fields (`Protocol`, `Host`). |

---

## Error-handling Results

| Scenario | Request | Status | Body | OK? |
|---|---|---|---|---|
| Bogus path (unguarded root) | `GET /api/v1/totally-bogus-xyz` | 404 | `{"success":false,"error":{"code":"NOT_FOUND","message":"Cannot GET /api/v1/totally-bogus-xyz"}}` | ✅ |
| Bogus path (`/api/nonexistent`) | GET | 404 | `NOT_FOUND` envelope | ✅ |
| Bogus subpath WITH token | `GET /api/v1/whm/this-route-does-not-exist` | 404 | `NOT_FOUND` envelope | ✅ |
| Bogus subpath WITHOUT token | `GET /api/v1/whm/this-does-not-exist-xyz` | **401** | `UNAUTHORIZED` (auth middleware runs before route match) | ✅ by design (see note) |
| Malformed JSON to login | `POST /api/v1/auth/login` body `{bad json` | 400 | `VALIDATION_ERROR "Invalid request body"` | ✅ |
| Missing required fields | `POST /api/v1/whm/domains/` body `{}` | 400 | `VALIDATION_ERROR` with `details[]` (Domain, User required) | ✅ |

**Envelope consistency:** success responses use `{success:true, data, [message], [pagination]}`; errors use `{success:false, error:{code, message, [details]}}`. Error `code` values observed: `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `VALIDATION_ERROR`, `INTERNAL_ERROR`. All consistent with `pkg/response`.

**401-before-404 note:** Any unknown path *under a guarded group* (`/whm/*`, `/cpanel/*`) returns 401 when unauthenticated, because Fiber runs the group's `Auth` middleware before route resolution. With a valid token, the same unknown path correctly returns 404. This is standard middleware ordering and not a defect, but it means an unauthenticated 404-probe of guarded namespaces is indistinguishable from an auth failure (mild info-hiding benefit, not a problem).

---

## Response Times

| Endpoint | time_total |
|---|---|
| `/whm/domains/` | 0.0058 s |
| `/whm/dashboard/stats` | 0.0078 s |
| `/whm/audit/` | 0.0107 s |
| `/whm/processes/services` | 0.0359 s |
| `/whm/monitor/metrics` | 0.2857 s |
| `/api/v1/auth/login` | 0.0886 s |

DB-backed reads are single-digit ms. `monitor/metrics` (~285ms) and `processes/services` are slower because they shell out to the live OS — expected. Login (~89ms) is dominated by bcrypt — appropriate.

---

## Issues Found

### Medium
- **`transfers/test-connection` returns HTTP 500 on a normal connection failure.** A timeout/refused connection to a destination host is an expected, user-input-driven outcome, not a server fault. Returning `500 INTERNAL_ERROR` is misleading: clients/monitoring will treat it as a panel bug, and it muddies real 500 alerting. It should return a 4xx (e.g. 422 `AGENT_ERROR`/`VALIDATION_ERROR`) or a `200` with `{success:true, data:{reachable:false, error:"…"}}` so the wizard can render a friendly "couldn't connect" without an error-level status. Compare with `backups/test-connection`, which fails cleanly with a 400 validation envelope.

### Low
- **Public `/api/v1/version` builds its envelope ad-hoc** (`c.JSON(fiber.Map{"success":true,"data":…})` in `main.go`) instead of going through `pkg/response.Success`. It happens to match the standard shape today, but it's the one success response not using the shared helper, so it can silently drift. Cosmetic / maintainability only.
- **`resources/bandwidth` returns `data:null`** rather than an empty object/array. Harmless on this box (no bandwidth accounting populated yet), but a null payload is easy for a frontend to mishandle versus an empty structure. Worth confirming the UI guards against null.

### Informational
- Unauthenticated requests to unknown paths under `/whm/*` and `/cpanel/*` return 401, not 404 (middleware ordering). Expected; documented above so future testers don't flag it as a "missing 404."

---

## Recommendations

1. **Fix the test-connection 500 (Medium).** Map connection/timeout failures in `Transfer.TestConnection` to a non-5xx status — ideally a `200` with a structured `{reachable:false, error}` body so the transfer wizard can show inline feedback, mirroring how `backups/test-connection` reports problems. Reserve 500 for genuine server faults so 5xx alerting stays meaningful.
2. **Route `/api/v1/version` through `response.Success`** for envelope consistency and to prevent drift.
3. **Return empty containers, not `null`,** from list/summary endpoints like `resources/bandwidth` (`data:[]` or `data:{}`), so the frontend never has to special-case null.
4. **No action needed on auth/RBAC/error-envelope** — all three are correct and consistent. The owner→cpanel 403 split, the 401 on missing/invalid token, the 404 NOT_FOUND envelope, the 400 validation envelope with `details[]`, and refresh-token rotation all behaved as designed.
5. Consider an automated smoke-test harness (the scripts used here can be checked in) that runs this 49-endpoint read sweep + the auth/RBAC matrix on each deploy, since the whole sweep finishes in well under a second of API time.
