# Server Audit 08 — Security Assessment

**Target:** Server 1 — `89.116.34.207` (Ubuntu 24.04), BetaZen Server Panel (bPanel) **v3.1.109** on `:8080`
**Auditor:** Agent 10 (Security Auditor)
**Date:** 2026-06-29
**Method:** Live host inspection over SSH (read-only / perms-only) + source review of the panel repo. No exploitation, no service disruption.

---

## Summary

The box is in reasonably good shape for a demo: UFW default-deny incoming, fail2ban running on 4 jails, secrets at rest correctly locked down (`.env` 600 root, OpenDKIM keys 600, no world-readable private keys, no world-writable files, no anomalous SUID), and the application layer shows real defensive engineering (parameterized Mongo queries with `regexp.QuoteMeta`, `exec.Command` with argv arrays rather than shell strings, login rate-limiting + bcrypt + audit logging, JWT signing-method pinning, guest-token rejection on the privileged auth path, and a short-TTL active-user revocation cache).

The dominant weakness is **transport security**: there is **no HTTPS configured anywhere in nginx** — zero `listen 443`, no certificate, no Let's Encrypt issuance. Every panel surface (WHM/User-Panel logins, JWT bearer tokens, File Manager, phpMyAdmin, Roundcube webmail) is served over **plaintext HTTP**, so credentials and session tokens travel in the clear. Secondary issues: SSH permits **root login with password authentication** exposed to the internet, and the mail stack permits legacy TLSv1.0/1.1.

Nothing internet-exposed was found that the firewall *intends* to block but doesn't — the demo apps and the panel on `0.0.0.0` (8080/8091/3101–3103/5101) are all absent from the UFW allow-list and therefore dropped by the default-deny incoming policy. They are reachable only from the host itself.

No remediation was applied: the one candidate for a safe perms fix (`/opt/serverpanel/.env`) is already `600 root:root`. All TLS/SSH/firewall findings are report-only per the engagement rules.

---

## Risk-ranked findings

| # | Finding | Severity | Evidence | Recommendation |
|---|---------|----------|----------|----------------|
| 1 | **Panel served over plaintext HTTP — no TLS at all.** No HTTPS vhost exists; credentials + JWT bearer tokens + phpMyAdmin/webmail traffic are unencrypted on the wire. | **Critical** | `nginx -T \| grep -c 'listen 443'` → `0`; no `ssl_certificate` directive anywhere; `/etc/letsencrypt/live/` empty (no certs); panel vhost `sites-available/serverpanel` only has `listen 80 default_server`. Panel returns `302 → /whm/` and serves the SPA over `:80`. | Issue a Let's Encrypt cert (certbot webroot challenge path is already wired in the vhost) and add a `listen 443 ssl` server block + `return 301 https://` on `:80`. Then add HSTS (see #5). |
| 2 | **SSH allows root login with password authentication, internet-exposed.** Brute-forceable root over port 22 (open to Anywhere in UFW). | **High** | `sshd -T`: `permitrootlogin yes`, `passwordauthentication yes`, `maxauthtries 6`; UFW allows `22/tcp Anywhere`; `/var/log/auth.log` shows 418 historical `Accepted password` logins. fail2ban `sshd` jail active (6 total bans) mitigates but does not eliminate. | Move to key-only auth: `PermitRootLogin prohibit-password` + `PasswordAuthentication no`. **Not changed per engagement rules (never touch sshd).** Reported only. |
| 3 | **Mail stack permits legacy TLS 1.0/1.1.** Postfix advertises `>=TLSv1` on internet-facing SMTP submission ports. | **Medium** | `postconf`: `smtpd_tls_protocols = >=TLSv1`, `smtpd_tls_mandatory_protocols = >=TLSv1`; ports 25/465/587 are `0.0.0.0` + UFW-allowed. (Dovecot is fine: `ssl_min_protocol = TLSv1.2`.) | Set `smtpd_tls_protocols = >=TLSv1.2` and `smtpd_tls_mandatory_protocols = >=TLSv1.2` (and the `smtp_` client equivalents). Validate mail flow after. |
| 4 | **nginx global `ssl_protocols` includes TLSv1 + TLSv1.1.** Latent misconfig — currently inert because no 443 listener exists, but will apply the moment TLS is enabled (i.e. while fixing #1). | **Medium** | `/etc/nginx/nginx.conf:33`: `ssl_protocols TLSv1 TLSv1.1 TLSv1.2 TLSv1.3;` | Change to `ssl_protocols TLSv1.2 TLSv1.3;` before/while standing up HTTPS so the new cert isn't served over weak protocols. |
| 5 | **No HSTS header.** Even once HTTPS is added, browsers won't be pinned to it. | **Medium** | `/etc/nginx/snippets/security-headers.conf` sets X-Frame-Options, X-Content-Type-Options, Referrer-Policy, X-XSS-Protection — but **no `Strict-Transport-Security`** (`grep -rl 'Strict-Transport' /etc/nginx` → none). Confirmed served: `curl -I http://127.0.0.1/whm/` returns the 4 headers, no HSTS. | Add `add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;` to the snippet — **only after** #1 so you don't HSTS-pin a host that can't serve HTTPS. |
| 6 | **No Content-Security-Policy header.** XSS blast-radius is larger than necessary for a panel that renders user-supplied domain/email/file data. | **Low** | `security-headers.conf` has no `Content-Security-Policy`. | Add a CSP (start in report-only mode given inline scripts/styles common in SPAs + phpMyAdmin/Roundcube). |
| 7 | **JWT access token TTL is long (4h); refresh 30 days.** A leaked access token (more likely given #1's plaintext transport) is replayable for up to 4 hours. | **Low** | `.env.prod`: `JWT_ACCESS_EXPIRY=4h`, `JWT_REFRESH_EXPIRY=720h`. Partially mitigated by the 15s active-user revocation cache in `internal/middleware/auth.go` (suspended/deleted users are kicked within ~15s regardless of token validity). | Consider shortening access TTL to 15–60m. The revocation cache already covers suspend/delete, so the main residual risk is a *stolen valid token for an active user* — shorter TTL limits that window. |
| 8 | **Demo apps & panel bind `0.0.0.0` (defense-in-depth, not currently exploitable).** Panel `:8080`, app `:8091`, node demos `:3101/3102/3103`, gunicorn `:5101` all listen on all interfaces. They are NOT firewall-exposed (saved by UFW default-deny), so a single UFW rule mistake would expose them. | **Low** | `ss -tlnp`: `0.0.0.0:8080 server`, `0.0.0.0:8091 app`, `0.0.0.0:3101/3102/3103 node`, `0.0.0.0:5101 gunicorn`. None appear in `ufw status` allow-list; `DEFAULT_INPUT_POLICY="DROP"`. | Where feasible, bind these to `127.0.0.1` and front via nginx, so firewall is not the *only* thing standing between the internet and these services. |
| 9 | **Host regex in panel vhost uses unescaped dots / duplicated token.** Minor: `if ($host !~* ^(89.116.34.207|89.116.34.207)$)` — dots are regex-any, and the IP is listed twice. Cosmetic for an IP, but the pattern would over-match if a hostname were added later. | **Low** | `sites-available/serverpanel`. | Escape dots (`89\.116\.34\.207`) and dedupe when this becomes a real hostname. |

---

## Fixes applied

**None.** No safe, unambiguous, access-preserving fix was available:

- The one perms-tightening candidate, `/opt/serverpanel/.env`, is **already `600 root:root`** (verified via `stat`). Nothing to tighten.
- OpenDKIM private keys are already `600 opendkim:opendkim`; `/etc/dovecot/users` is `640 root:dovecot`; no world-readable `*.key`/`*.pem` under `/etc/ssl/private` or `/etc/letsencrypt`; no world-writable files under `/opt/serverpanel`; SUID set is the stock Ubuntu list only.
- All other findings (TLS, SSH, UFW, mail protocols) are explicitly out-of-bounds for modification per the engagement rules (no sshd, no ufw, no service restarts, no secret rotation). They are reported for the operator to action.

---

## Hardening recommendations (prioritized)

1. **Stand up HTTPS (fixes #1, enables #5).** Run certbot against the panel hostname (the `/.well-known/acme-challenge/` webroot location already exists in the vhost), add a `listen 443 ssl` block, redirect `:80 → :443`. This is the single highest-impact change.
2. **While enabling TLS, fix nginx `ssl_protocols` to `TLSv1.2 TLSv1.3` (#4)** so the new cert is never offered over weak protocols, then **add HSTS (#5)**.
3. **SSH: go key-only.** `PermitRootLogin prohibit-password` + `PasswordAuthentication no` after confirming a working key is installed. Removes the brute-force surface that fail2ban only partially blunts (#2).
4. **Raise mail TLS floor to 1.2 (#3).**
5. **Bind internal/demo services to localhost (#8)** so UFW is defense-in-depth, not the sole control.
6. **Shorten JWT access TTL (#7)** and add a CSP (#6).

---

## Positive controls observed

- **Firewall:** UFW active, `DEFAULT_INPUT_POLICY="DROP"` (default-deny incoming) + `DEFAULT_FORWARD_POLICY="DROP"`. Allow-list is tight and intentional (22/80/443/53/mail/21/passive-FTP 30000-30009). Everything else on `0.0.0.0` is dropped.
- **fail2ban:** 4 jails active — `sshd` (6 bans to date), `dovecot`, `postfix-sasl`, `pure-ftpd` — `bantime 1h`, `findtime 10m`, `maxretry 5`.
- **Secrets at rest:** `/opt/serverpanel/.env` `600 root:root`; OpenDKIM private keys `600 opendkim:opendkim`; `/etc/dovecot/users` `640 root:dovecot`; no world-readable private keys; no world-writable files under `/opt/serverpanel`; only stock SUID binaries.
- **Network exposure of datastores:** `mongod` (`127.0.0.1:27017`), `mariadb` (`127.0.0.1:3306`), `pdns-api` (`127.0.0.1:8081`), `spamd` (`127.0.0.1:783`) all bound to loopback. (Note: on Server 1, mongod is loopback-only — unlike the PROD box noted in memory which binds 0.0.0.0.)
- **Security headers present** on the panel (X-Frame-Options SAMEORIGIN, X-Content-Type-Options nosniff, Referrer-Policy strict-origin-when-cross-origin, X-XSS-Protection) — confirmed actually served, not just configured.
- **Dovecot TLS** correctly floored at `ssl_min_protocol = TLSv1.2` with a hardened cipher list (`!kRSA !aNULL !EXPORT !DES !3DES !MD5 !RC4 !LOW`).

### Application layer (source review — design notes, not pentest)

- **JWT:** `pkg/jwt/jwt.go` pins the signing method (`*SigningMethodHMAC`) and rejects anything else, blocking the classic `alg:none` / RS↔HS confusion attack. Refresh tokens are 32 bytes of `crypto/rand`.
- **Auth middleware** (`internal/middleware/auth.go`): explicitly rejects `role == "guest"` tokens on the privileged path (prevents guest magic-link cookies being lifted into a Bearer header); per-request active-user check (suspended/deleted users blocked within ~15s via a TTL cache); transient-DB-error path denies the request without poisoning the cache with a sticky negative.
- **RBAC** (`internal/middleware/rbac.go`): `vendor_owner` and `is_super_admin` bypass *permissions* only; tenant scoping/visibility is enforced separately (`tenant_scope` middleware + per-handler `AssertOwns`), so the bypass doesn't cross tenant boundaries.
- **Injection defenses:**
  - *NoSQL/Mongo:* lookups use parameterized `bson.M` filters; the user-email regex query escapes input — `internal/services/user_service.go:316`: `"$regex": "^" + regexp.QuoteMeta(email) + "$"`. No `$where`/JS-eval query construction from user input observed.
  - *Command injection:* `exec.Command` / `exec.CommandContext` are called with **argv arrays**, not shell strings (`internal/agent/executor.go`, `internal/handlers/terminal_handler.go`). Where a shell is needed, the structure is `su - <user> -c <script>` / `sudo -H -u <user> bash -c <command>` with the user passed as a separate argv element, not interpolated into a shell word.
- **Brute-force / auth abuse:** `LoginRateLimiter` (10 attempts / 15 min per IP) guards `/login` **and** every OTP endpoint (`internal/routes/auth_routes.go`); failed and successful logins are audit-logged with IP + user-agent (`internal/handlers/auth_handler.go`).
- **Dependencies:** Go toolchain `go1.23.0` (module declares `go 1.24.0`); Fiber `v2.52.5`, `golang-jwt/jwt/v5 v5.2.1`, `mongo-driver v1.17.1`, `golang.org/x/crypto v0.48.0` — all reasonably current, no obviously abandoned/CVE-flagged pins. Node `v20.20.2` (LTS). Lockfiles not stale at a glance. (Full `npm audit` / `govulncheck` left to a dedicated dependency scan — out of scope to run here.)
