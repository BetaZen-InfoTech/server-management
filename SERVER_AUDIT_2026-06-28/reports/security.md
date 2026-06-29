# Agent 10 — Security Audit

**Date:** 2026-06-28
**Scope:** BetaZen Server Panel demo environment — two Ubuntu 24.04 clones + deployed code (local repo @ v3.1.109, rev 466b52e)
**Servers:** Server 1 = `89.116.34.207` (migration SOURCE) · Server 2 = `195.35.7.64` (migration DEST)
**Mode:** READ-ONLY. No service was restarted, no config edited, no data created/deleted. The only "writes" attempted were login POSTs to verify whether the documented default admin password is still accepted; these do not mutate state.

---

## Executive summary

Both servers are configured **identically** (no security drift detected between Server 1 and Server 2). The host posture has solid foundations — UFW is active with a default-deny inbound policy, the panel + agent + database ports are correctly kept off the external allow-list, the `.env` is `600 root:root`, JWT/encryption/agent secrets are real 32–64 char random values (not the dev defaults) **and are unique per server** (so a migrated JWT will not validate on the destination), and the Go code uses `html/template` (XSS-safe), parameterised-by-type Mongo queries (no NoSQL operator injection on the paths reviewed), an SSRF-guarded outbound HTTP client for webhooks/notifications, path-traversal + zip-slip defenses in the File Manager, account-lockout on login, and a properly trusted-proxy rate limiter.

However, there are **two confirmed critical issues**: (1) the publicly-documented default admin credentials `admin@betazeninfotech.com` / `admin123` are **still accepted (HTTP 200)** on both `APP_ENV=production` panels, and (2) multiple authenticated handlers shell out via `bash -c fmt.Sprintf(...)` with **user-controlled domain / hostname / email values that have no character validation and are not shell-escaped**, yielding authenticated command injection as **root** (the panel runs as root). Default creds + command injection chained together = unauthenticated-to-root for anyone who knows the well-known password. Secondary issues: SSH allows `PermitRootLogin yes` + `PasswordAuthentication yes` on both boxes, no HTTP security headers anywhere (no CSP / X-Frame-Options / X-Content-Type-Options / Referrer-Policy / HSTS), CORS is `AllowOrigins: *`, fail2ban only jails sshd (SMTP/IMAP/FTP/panel-login unprotected), the JWT access TTL is a long 4h, the terminal WebSocket trusts a token in the query string and skips the suspended-user check, and plaintext FTP (21) is internet-exposed.

**Overall health: critical.**

---

## 1. SSH configuration — BOTH servers (identical)

```
$ sshd -T | grep -Ei 'permitrootlogin|passwordauthentication|pubkeyauthentication|permitemptypasswords'
usepam yes
permitrootlogin yes
pubkeyauthentication yes
passwordauthentication yes
permitemptypasswords no

$ grep -Ei 'PermitRootLogin|PasswordAuthentication' /etc/ssh/sshd_config /etc/ssh/sshd_config.d/*.conf
/etc/ssh/sshd_config:PermitRootLogin yes
/etc/ssh/sshd_config.d/50-cloud-init.conf:PasswordAuthentication yes
/etc/ssh/sshd_config.d/60-cloudimg-settings.conf:PasswordAuthentication no
```

- **`PermitRootLogin yes`** — direct root login over SSH is permitted.
- **Effective `passwordauthentication yes`** — password auth is on. Note the conflicting drop-ins: `50-cloud-init.conf` (lower number = higher precedence, first match wins) sets `yes`, overriding `60-cloudimg-settings.conf`'s `no`. The effective `sshd -T` confirms `yes`.
- Combined: **root password login over the internet is allowed**, and SSH (22) is open to Anywhere in UFW. fail2ban's sshd jail is the only thing throttling brute force (0 bans currently).
- `permitemptypasswords no` and `kbdinteractive no` are fine.

Identical on both servers.

## 2. Host firewall (UFW / iptables / nftables) — BOTH servers (identical)

```
$ ufw status verbose
Status: active
Default: deny (incoming), allow (outgoing), disabled (routed)
22,80,443,53(t/u),25,465,587,143,993,110,995,21,30000:30009  ALLOW IN  Anywhere (+v6)

$ iptables -S | head
-P INPUT DROP
-P FORWARD DROP
-P OUTPUT ACCEPT
... (ufw-* chains; nft ruleset = 415 lines)
```

- **Firewall IS actively filtering** — UFW active, default-deny inbound, iptables policy `INPUT DROP`, nftables `ip filter` + `ip6 filter` tables populated (415-line ruleset).
- **Panel :8080 and agent :8443 are NOT in the allow-list** → filtered from external reach (verified: `ufw status | grep 8080|8443` returns nothing). Good — the panel is only reachable via nginx :80.
- Exposed to Anywhere: 22 (SSH), 80 (HTTP), 443 (no listener), 53 (DNS), 25/465/587 (SMTP), 143/993/110/995 (IMAP/POP), **21 (FTP, plaintext)**, 30000-30009 (FTP passive).
- `443/tcp ALLOW` exists in UFW but nothing listens on 443 (no TLS) — harmless but misleading.

## 3. Open ports — 0.0.0.0 vs localhost — BOTH servers (identical)

```
$ ss -tlnp
127.0.0.1:27017  mongod        <- localhost only (good)
127.0.0.1:3306   mariadbd      <- localhost only (good)
127.0.0.1:783    spamd         <- localhost only (good)
127.0.0.1:8081   pdns_server   <- PowerDNS API, localhost only (good)
0.0.0.0:8080     server        <- PANEL BOUND TO ALL INTERFACES
0.0.0.0:25/465/587/143/993/110/995/21/53/80   mail+dns+ftp+web (intended)
0.0.0.0:22       sshd
```

- **Finding:** the panel Go binary (`/opt/serverpanel/bin/server`) **binds `0.0.0.0:8080`, not `127.0.0.1:8080`.** It is currently saved only by UFW dropping 8080. Defense-in-depth says bind it to loopback (nginx proxies from 127.0.0.1 anyway) so a future firewall flush / misconfig doesn't instantly expose the admin API. MongoDB / MariaDB / spamd / PowerDNS-API are all correctly loopback-bound.
- **8443 (agent mTLS) is NOT listening** on either box — the agent runs in-process; the mTLS port is not exposed. Good.

## 4. Authentication / default credentials — CRITICAL — BOTH servers

```
$ curl -s -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:8080/api/v1/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"email":"admin@betazeninfotech.com","password":"admin123"}'
Server 1: 200
Server 2: 200
```

- The **documented default admin password `admin123` is still valid** on both production panels. The panel is internet-reachable (nginx :80, bare IP). This is a publicly known default (it is printed in this very runbook and in the repo). Anyone can log in as the platform owner.
- `APP_ENV=production` on both — so this is not a dev box.
- Login does have an IP rate-limiter (10 / 15 min, see §10) and per-account lockout (`failed_logins` / `locked_until` in `auth_service.go:146-149`), but a *correct* default password defeats both.

## 5. JWT — secret strength, expiry, algorithm

- **Algorithm:** `HS256` (`pkg/jwt/jwt.go:59`), and `ValidateToken` correctly rejects non-HMAC methods (`jwt.go:73`) → **not vulnerable to the `alg:none` / RS→HS confusion attack.**
- **Secret strength (no values printed):**
  ```
  $ # name + length + emptiness only
  JWT_SECRET          len=64   set      (is_jwt_dev_default = no)
  APP_ENCRYPTION_KEY  len=64   set
  AGENT_API_KEY       len=32   set      (is_agentkey_dev_default = no)
  BACKUP_ENCRYPTION_KEY len=32 set
  MONGO_PASS          len=32   set
  TLS_CERT            len=0    EMPTY
  TLS_KEY             len=0    EMPTY
  ```
  JWT_SECRET is a 64-char value, not the code default `dev-secret-change-in-production` (`config.go:122`).
- **Per-server uniqueness (sha256 prefix of secret, not the secret):** Server 1 JWT_SECRET `f5abcf14…` vs Server 2 `5ed0343f…`; AGENT_API_KEY `09dd09f1…` vs `1674f116…`; APP_ENCRYPTION_KEY `a34109f8…` vs `e1a3f281…`. **All differ** → a JWT minted on the SOURCE will not validate on the DEST (correct migration hygiene).
- **Expiry:** `JWT_ACCESS_EXPIRY=4h`, `JWT_REFRESH_EXPIRY=720h` (30d). The 4h access TTL is **long** for a root-equivalent admin panel — a stolen/leaked access token is usable for up to 4 hours (the code comment at `config.go:123-128` justifies this for UX). There is **no server-side access-token revocation list** — logout/suspend relies on the 15s `authcache` `is_active` check (`middleware/auth.go:40-65`), which does kick suspended users within ~15s for the JWT path (but see §11 for the terminal WS gap).
- **TLS_CERT / TLS_KEY are intentionally empty** (panel is plain HTTP behind nginx; bare-IP, no domain → no Let's Encrypt). This is by design for this demo, but means **no TLS** (see §7).

## 6. Secrets handling & file permissions

```
$ stat -c '%a %U:%G %n' /opt/serverpanel/.env
600 root:root /opt/serverpanel/.env
$ ls -la /opt/serverpanel/   # .env -rw------- ; .env.example/.dev/.prod/.local are -rw-r--r--
```
- `.env` is `600 root:root` — **correct.** No weak/empty *required* secrets (TLS_CERT/KEY empty is expected). No dev-default secrets in use.
- The full **git repo (incl. `.git/`) is deployed under `/opt/serverpanel/`** and the panel runs from there. `.env` is gitignored and the committed `.env.*` files carry only placeholders, so no secret is in git. nginx does not serve the filesystem (only proxies :8080 + serves /webmail + phpMyAdmin snippet), so the on-disk `.git` is low risk — but it is unusual to ship the VCS dir to prod.
- **Panel process runs as `root`** (`systemctl cat serverpanel` → `User=root`, `ExecStart=/opt/serverpanel/bin/server`). This is the reason the command-injection findings in §8 are *root* RCE.
- Password hashing: **bcrypt at `DefaultCost` (10)** (`pkg/password/password.go:6`) — acceptable.

## 7. TLS / SSL & security headers

- **No TLS** anywhere: nothing listens on 443, `TLS_CERT`/`TLS_KEY` empty, panel is HTTP on a bare IP. All panel traffic (incl. the admin login password and the JWT) crosses the network **in cleartext**. Expected for a bare-IP demo, but must be called out.
- **No HTTP security headers** — confirmed by curl against both nginx :80 and the panel :8080, and by code review:
  ```
  $ curl -I http://127.0.0.1:8080/whm/
  HTTP/1.1 200 OK
  Content-Type: text/html; charset=utf-8
  Cache-Control: no-store, no-cache, must-revalidate
  Pragma: no-cache
  Expires: 0
  (no X-Frame-Options, no X-Content-Type-Options, no Content-Security-Policy,
   no Referrer-Policy, no Strict-Transport-Security)
  ```
  Code: `grep` for `X-Frame-Options|X-Content-Type|Content-Security-Policy|Referrer-Policy|Strict-Transport` across `backend/` returns **zero** header-setting sites (only `Cache-Control`, `Content-Disposition`, `Retry-After`, rate-limit headers exist). nginx `sites-enabled/serverpanel` sets **no `add_header`** directives either.
  - Missing **X-Frame-Options / frame-ancestors CSP** → the panel is **clickjackable**.
  - Missing **X-Content-Type-Options: nosniff** and **CSP** → weaker XSS containment (the SPA is mitigated by `html/template` auto-escaping, but headers are defense-in-depth).
- **CORS is wide open:** `middleware/cors.go:9` → `AllowOrigins: "*"` with `AllowMethods` incl. POST/PUT/DELETE. Because auth is via `Authorization: Bearer` (not cookies) and Fiber will not reflect credentials with `*`, this is not a same-site CSRF hole, but `*` is needlessly permissive for an admin panel and should be tightened to the panel origin.

## 8. Command injection — CRITICAL — code review

The panel shells out heavily (265 `exec.Command`/`bash -c` occurrences across 39 service files). The safe pattern is `agent.RunCommand(ctx, "binary", arg1, arg2…)` which uses `exec.CommandContext(name, args...)` (`internal/agent/executor.go:18-37`) — **no shell**, so each arg is a single argv element and is injection-safe. The dangerous pattern is `agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("… %s …", userValue))`. Several of these interpolate **user-controlled values that have no character validation and are not shell-escaped**:

**8a. Mailbox create — `internal/services/email_service.go:493-531`** (most user-facing)
```go
escEmail := strings.ReplaceAll(req.Email, ".", "\\.")
agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
    "sed -i '/^%s:/d' /etc/dovecot/users 2>/dev/null; echo '%s' >> /etc/dovecot/users",
    escEmail, userLine))          // userLine embeds raw req.Email + maildir
... echo '%s' >> /etc/postfix/virtual_mailbox_maps  // mapping embeds raw req.Email
... echo '%s OK' >> /etc/postfix/virtual_mailbox_domains  // raw domain
```
`req.Email` is validated only with `validate:"required,email"` (`models/email.go:22`) and lowercased/trimmed. go-playground/validator's `email` rule accepts a **single quote** in the local part (RFC-legal, e.g. `o'brien@x.com`), which **breaks out of the `echo '…'` single-quote context**. The `\\.`-escaping only neutralises dots for the `sed` regex; it does nothing for `'`, `;`, `$()`. Reachable by `vendor_admin`/`vendor_staff` (cPanel email) and the WHM email handler.

**8b. DNS zone / mail setup — `internal/services/dns_service.go:1355,1358,1361,1373` and `setupMailServer` / `EnsureDKIMForDomain` (`email_service.go:1583,1586,1589`)**
```go
agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
    "... || echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table", escDom, domain, domain))
... echo '%s OK' >> /etc/postfix/virtual_mailbox_domains   // raw domain
... echo '%s' >> /etc/opendkim/trusted.hosts               // raw domain
```
`regexp.QuoteMeta` is applied only to the *grep pattern* (`escDom`); the `echo '…%s…'` parts use the **raw `domain`/`fqdn`**. `CreateZoneRequest.Domain` is `validate:"required"` only — **no FQDN/format constraint** (`models/dns.go:39`) — so a domain containing `'` injects. `CreateZone` is WHM-side, gated on `dns.manage`.

**8c. Domain create — `internal/services/domain_service.go:312`**
```go
agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("rm -f /run/php/*-fpm-%s.sock", req.Domain))
```
`CreateDomainRequest.Domain` is `validate:"required"` only (`models/domain.go:121`); `sanitizeDomain()` (`app_helpers.go:90`) only lowercases/trims and isn't even called here. A domain like `x.sock; <cmd>; #` injects into a `rm -f` running as root. Reachable by vendors via `POST /api/v1/cpanel/domains` (`CPanelCreate`).

**8d. Hostname update — `internal/services/config_service.go:387`**
```go
agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
    "sed -i 's/127.0.1.1.*/127.0.1.1\\t%s/' /etc/hosts", hostname))
```
`ConfigHandler.UpdateHostname` (`config_handler.go:33`) `BodyParser`s `hostname` with **no validation at all** and passes it straight in. Owner / `server.manage` gated, but still root command injection via the `sed` replacement.

**Impact:** authenticated → arbitrary command execution as **root** (panel runs as root). The blast radius depends on which role can reach each entry point (vendor for 8a/8c, owner/staff-with-perm for 8b/8d), but all of them are post-auth-only. Chained with the default `admin123` password (§4), this is effectively remote root for anyone who knows the default creds.

**Note — helpers exist but are inconsistently used:** `shellQuote` (`config_service.go:887`), `shellQuoteLocal` (`email_service.go:1901`), and `shellQuote` (`mail_suite_install.go:461`) correctly POSIX-single-quote values. The send-test path (`email_service.go:1859-1884`) uses `shellQuoteLocal` properly. The fix is to (a) add strict FQDN/email/hostname validation at the model layer and (b) route every remaining `echo '…%s…'`/`sed 's/…%s…/'` through a shell-quoting helper or, better, replace with argv-form `tee`/`crudini`/file writes.

## 9. Other injection classes (reviewed, mostly OK)

- **NoSQL injection:** login uses `bson.M{"email": loginEmail}` with a typed lowercased string (`auth_service.go:121-125`); lookups use parsed `primitive.ObjectIDFromHex` for `_id`. `UpdateMailbox` builds `$set` from an **allowlist** of fields (`email_service.go`), not the raw body map, so attacker-supplied `$`-operators can't reach Mongo. **No NoSQL operator-injection found on the reviewed paths.**
- **SQL injection (MariaDB / Roundcube):** the panel shells `mysql`/`gunzip | mysql` with `dbName` etc. via `bash -c` in the agent/transfer code; DB names come from the panel's own create flow. Not a classic SQLi (no concatenated SQL strings in Go), but the same `bash -c` interpolation caveat as §8 applies if a DB name were attacker-controlled — worth confirming DB-name validation in a follow-up.
- **SSRF:** outbound **webhook + notification** delivery correctly uses `newGuardedHTTPClient` (`ssrf_guard.go`) which resolves the host, **rejects any private/loopback/link-local/metadata IP**, and dials the validated IP to defeat DNS rebinding (`webhook_service.go:62`, `notification_service.go:162`). **RDAP/whois lookups** (`domain_service.go:1604-1625`) use a *plain* `http.Client`, but the target host comes from the IANA RDAP bootstrap (trusted), not user input — low risk. `whois` is invoked as argv (`RunCommand(ctx,"whois",domain)`), safe from shell injection (minor: a domain starting with `-` could be read as a whois flag).
- **Path traversal:** File Manager `validatePath` (`file_service.go:106-131`) enforces the resolved path stays within `/home/<user>/` for non-root callers, and the archive-extract path has explicit **zip-slip** protection (`file_service.go:920-936`). Solid.
- **XSS:** the `GET /` home page uses **`html/template`** with auto-escaping (`home_page_render.go:6,14`); `template.URL` is used only for operator-controlled branding data-URLs. The SPAs are static React bundles. No reflected-XSS sink found.
- **CSRF:** auth is `Authorization: Bearer` header (not ambient cookies) for the API, so cross-site form posts can't carry the token. The guest magic-link surface uses a separate cookie + `GuestAuth`; review of CSRF on that surface is out of this agent's depth but flagged.

## 10. AuthN/AuthZ model (RBAC) — reviewed, sound

- `middleware.Auth` validates the JWT, **explicitly rejects `role=="guest"` tokens** on normal routes (`auth.go:98`), and re-checks `is_active` against Mongo every 15s via `authcache` so suspended/deleted users are kicked (`auth.go:105`).
- `RequirePermission` bypasses for `vendor_owner` and `is_super_admin` (by design), otherwise requires every named permission (`rbac.go:25-58`).
- Tenant isolation: `InjectScope` (`tenant_scope.go`) + service-layer `scope.AssertOwns(...)` (e.g. `domain_service.go:302-305`) prevent a non-owner from acting on another tenant's resources even via crafted `req.user`.
- API tokens (`btz_…`) go through a separate `APITokenAuth` + per-route `RequireTokenScope` + per-token rate limit (`api_token.go`). Tokens never fall back to JWT.
- Rate limiting keys on `c.IP()`, and Fiber is correctly configured with `EnableTrustedProxyCheck:true`, `TrustedProxies:[127.0.0.1,::1]`, `ProxyHeader:X-Forwarded-For` (`cmd/server/main.go:387-389`) → `c.IP()` resolves the **real client IP** from nginx, so the login limiter (10/15min) is genuinely per-client, not per-proxy. Good.

## 11. Terminal WebSocket — token-in-URL + missing suspend check (high)

`NewTerminalWSHandler` (`terminal_handler.go:60-160`):
- Authenticates via **`token` query parameter** (`c.Query("token")`). Tokens in URLs leak into nginx access logs, `Referer` headers, and browser history.
- It calls `jwt.ValidateToken` directly and **does not run the `isUserAllowed` suspended/deleted check** that `middleware.Auth` enforces → a user suspended within the access-token's lifetime (≤4h) could still open a shell.
- Privilege separation is otherwise **correct**: `vendor_owner` → root (or su to a validated existing user), `vendor_admin` → su to own/tenant user (membership checked via `AssertUsernameInTenant`), `vendor_staff`/`developer`/`support` → **never root**, sandboxed rcfile. So this is not a priv-esc, but the suspend-bypass + token-in-URL are real weaknesses.

## 12. fail2ban — only sshd jailed (medium) — BOTH servers

```
$ fail2ban-client status
Jail list: sshd          (the only jail)
$ cat /etc/fail2ban/jail.d/serverpanel.conf  -> [sshd] enabled=true port=ssh
```
- Only **sshd** is jailed. Despite Postfix (25/465/587), Dovecot (110/143/993/995), and pure-ftpd (21) being internet-exposed, there are **no jails** for postfix-sasl, dovecot, pure-ftpd, or the panel login. Credential brute-force against mail/FTP/the panel is throttled only by app-level limits (panel: 10/15min IP).

## 13. Plaintext FTP exposed (medium) — BOTH servers

- pure-ftpd listens on `0.0.0.0:21` + passive `30000:30009`, both ALLOWed in UFW. `/etc/pure-ftpd/conf/TLS = 1` → TLS is *offered* but pure-ftpd mode `1` still **permits cleartext** logins (mode `2` would force TLS). FTP credentials can cross the wire in plaintext.

---

## Drift between Server 1 and Server 2

**None found.** SSH config, UFW rules, listening sockets, fail2ban jails, `.env` permissions, secret *lengths*, `APP_ENV`, JWT TTLs, default-creds acceptance, missing security headers, and FTP/TLS config are identical. The only intentional difference is that the **secret values differ per server** (verified via sha256 fingerprints), which is the correct, expected state — a migrated token from S1 will not authenticate on S2.

---

## Prioritised findings & remediation

| # | Severity | Finding | Safe auto-fix? |
|---|----------|---------|----------------|
| 1 | critical | Default admin creds `admin123` still accepted on both prod panels | **No** (must rotate password — operational change; coordinate) |
| 2 | critical | Command injection (root) via unsanitised domain/email/hostname into `bash -c` (§8a-d) | **No** (code change + redeploy; needs careful validation + tests) |
| 3 | high | SSH `PermitRootLogin yes` + `PasswordAuthentication yes` (both boxes) | **Yes** (sshd hardening) — but ensure key-based access exists first |
| 4 | high | No HTTP security headers (CSP/X-Frame-Options/X-Content-Type-Options/Referrer-Policy) | **Yes** (add nginx `add_header` or Go middleware — non-breaking) |
| 5 | high | Terminal WS: token-in-URL + skips suspended-user check | No (code change) |
| 6 | medium | Panel binds `0.0.0.0:8080` instead of `127.0.0.1` | Partial (config/flag change; verify nginx still reaches it) |
| 7 | medium | CORS `AllowOrigins:"*"` on admin API | **Yes** (restrict to panel origin) |
| 8 | medium | fail2ban only jails sshd (no mail/ftp/panel jails) | **Yes** (add jails — additive, non-breaking) |
| 9 | medium | Plaintext FTP (21) internet-exposed; TLS not forced | Partial (set pure-ftpd TLS=2 — may break cleartext clients) |
| 10 | low | JWT access TTL 4h (long for root-equiv admin) + no revocation list | No (policy/code decision) |
| 11 | low | Full `.git/` deployed to `/opt/serverpanel` | **Yes** (remove from deploy artifact) |
| 12 | info | bcrypt DefaultCost(10), html/template XSS-safe, SSRF guard, path-traversal/zip-slip defenses, tenant AssertOwns, trusted-proxy rate limiter, per-server unique secrets — all GOOD |  — |

**Safe to auto-fix now (no risk to working features):** #4 security headers (additive), #7 CORS tightening to the panel origin, #8 additional fail2ban jails (additive), #11 stop shipping `.git`. #3 (sshd) is a safe hardening *only after* confirming an SSH key path exists for the operator — otherwise it can lock everyone out, so treat as "safe config, but verify key access first."

**Must NOT be auto-applied:** #1 (rotate creds — operational), #2 (injection fix — needs validation + tests + redeploy), #5/#6/#9/#10 (behavioural changes that can break clients/sessions).
