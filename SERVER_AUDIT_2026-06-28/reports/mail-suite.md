# Agent 3 — Mail Suite Audit

**Date:** 2026-06-28
**Auditor:** Read-only infrastructure audit (Agent 3)
**Scope:** `mail-suite/` subproject (Go backend + React webmail + Flutter mobile + FCM), deployment status on both VPS, and its relationship to the live Roundcube mail flow.
**Servers:** Server 1 = 89.116.34.207 (migration SOURCE, `srv1785162`) · Server 2 = 195.35.7.64 (migration DEST, `srv1789639`)
**Deployed code:** local git repo at `c:/Users/Administrator/Downloads/Project/server-management` (v3.1.109, rev 466b52e).

---

## 1. Executive Conclusion

**Mail Suite is a SEPARATE, UNDEPLOYED product.** It is *not* running on either server, and it is *not* part of the live mail flow. The live webmail experience is **Roundcube** at `/webmail/` (PHP, served by nginx, backed by Postfix/Dovecot/MariaDB `roundcube`). Mail Suite is a distinct Go-Fiber + React + Flutter "Gmail/Zoho-style" product whose source ships inside the panel repo (so the panel's one-click installer can build it on demand) but which has never been built, configured, or started on these boxes.

Both servers are in **perfect parity** on this point: source-only, no runtime artifacts.

It is **NOT deployable as-is via the one-click installer** because of a directory-name mismatch (installer looks under `/opt/server-panel`, source is at `/opt/serverpanel`) and a missing Go/Node toolchain. It *can* be deployed with manual steps (and the installer would also fix it if it ran from the right cwd). There is also a **functional authorization gap**: the panel calls the mail-suite admin endpoints with a service token the mail-suite backend does not understand, so the "Enable Mail" / status proxy would fail with 401 even once deployed.

---

## 2. Deployment status — verified on BOTH servers

### What I checked and the commands

Deployment check (run via `bz.py <N> --file deploy_check.sh`):

```bash
hostname; hostname -I
ls -la /opt/mail-suite
find /opt /srv /root /home /usr/local -maxdepth 4 -iname '*mail-suite*'
systemctl list-units --type=service --all | grep -iE 'mail-suite|mailsuite|...'
systemctl list-unit-files --type=service | grep -iE 'mail|betazen|panel|fcm|push'
ls -la /etc/systemd/system/*.service
ss -tlnp
ps aux | grep -iE 'mail-suite|mailsuite|fcm|mail_suite'
```

Source/artifact check (`source_check.sh`) and path/DB check (`path_db_check.sh` / `srv2_parity.sh`):

```bash
ls -la /opt/serverpanel/mail-suite ; ls -la /opt/serverpanel/mail-suite/backend
find / -type f \( -name 'mail-suite' -o -name 'mailsuite' \) -executable
ls /opt/serverpanel/mail-suite/webmail/dist        # built SPA?
ls /opt/serverpanel/mail-suite/backend/.env        # runtime config?
which go ; ls /usr/local/go/bin/go
ss -tlnp | grep -E ':9090|:5173'
ls /opt/server-panel                               # path the installer searches
source /opt/serverpanel/.env; mongosh "$MONGO_URI" --eval '...'  # DB + deployments
ls /etc/letsencrypt/live ; ls /etc/nginx/sites-enabled
```

### Findings (identical on Server 1 and Server 2)

| Check | Server 1 (89.116.34.207) | Server 2 (195.35.7.64) |
|---|---|---|
| `/opt/mail-suite` (install dir) | **absent** | **absent** |
| `mail-suite` source on disk | `/opt/serverpanel/mail-suite` (README, backend, mobile, webmail) | `/opt/serverpanel/mail-suite` (same) |
| Built backend binary (`mail-suite`) anywhere | **none** (`find ... -executable` empty) | **none** |
| Built webmail `webmail/dist/` | **absent** | **absent** |
| `backend/.env` (runtime config) | **absent** (only `.env.example`) | **absent** |
| Go toolchain | `go: command not found`, no `/usr/local/go/bin/go` | same |
| `mail-suite.service` systemd unit | **absent** | **absent** |
| nginx `mail-suite.conf` vhost | **absent** (only `serverpanel` site enabled) | **absent** |
| `/etc/letsencrypt/live` (TLS) | **absent** (no certbot/LE on box) | n/a (no LE dir) |
| Listening port `:9090` (mail-suite) / `:5173` (vite) | **nothing** | **nothing** |
| Running process | "NO mail-suite processes" | "NO mail-suite processes" |
| `mail_suite` Mongo DB | **does not exist** (only `serverpanel`) | **does not exist** (only `serverpanel`) |
| `mail_users` collection count | 0 | 0 |
| `mailsuite_deployments` (panel registry) | 0 | 0 |

The only systemd service unit on either box is `serverpanel.service`. Listening ports are exactly the documented stack (22, 25/465/587, 53, 80, 110/143/993/995, 8080 serverpanel, 8081 pdns, 27017 mongo, 783 spamd, 3306 mariadb, 21 ftp) — **no extra port for mail-suite**. This rigorously confirms the assignment's premise: no systemd unit, no `/opt/mail-suite`, no extra listening port.

The `serverpanel-mail-diagnose` / `serverpanel-mail-reconcile` symlinks in `/usr/local/bin` belong to the *panel's* Postfix/Dovecot tooling, not mail-suite.

> Note on naming: the `mail*` Mongo collections that *do* exist in `serverpanel` (`mailboxes`, `email_forwarders`, `mail_logs`, `email_server_configs`, `email_installations`) are the **panel's** mail objects, not mail-suite's. Mail Suite uses `mail_`-prefixed collections (`mail_users`, `mail_accounts`, …) which are all at count 0 / non-existent.

---

## 3. Code audit — mail-suite/backend

Layout: Go 1.22, Fiber v2, MongoDB driver, IMAP/SMTP via `emersion/go-*`, bcrypt, JWT v5 (HS256). Clean handler → service → DB separation mirroring the main panel. Source files reviewed: `cmd/server/main.go`, `internal/config/config.go`, all handlers (auth, account, mail, signature, forwarder, dns, push), all services (auth, account, mail, signature, forwarder, dns, device, imap_client, smtp_client, betazen_panel_client), `middleware/auth.go`, `routes/api_routes.go`, `pkg/jwt`, `pkg/password`, `internal/database/{mongo,collections,indexes}.go`, models, `.env.example`, `Dockerfile`, `Makefile`, plus the panel-side companion (`backend/internal/services/mailsuite_service.go`, `handlers/mailsuite_handler.go`, `agent/mail_suite_install.go`, `routes/whm_routes.go`, `frontend/apps/whm/.../MailSuitePage.tsx`).

### 3.1 Database
- Uses its **own** MongoDB DB, default name `mail_suite` (`MONGO_DB`, `config.go:75`). Collections all prefixed `mail_` (`database/collections.go`).
- **However**, the panel's one-click installer deliberately reuses the panel's Mongo URI **and DB name** (`mailsuite_service.go:79-88` passes `MongoDBName: s.cfg.MongoDBName`), so in practice mail-suite's `mail_*` collections land *inside* the `serverpanel` DB (the `mail_` prefix avoids collisions). Comment at `mailsuite_service.go:70-78` explains this is to keep the one-click promise on single-DB Mongo users. So "which DB?" = `mail_suite` by default in standalone, but **`serverpanel`** when installed via the panel.
- `EnsureIndexes` (`indexes.go`) builds a full index set including unique `email`, unique `(user_id,address)`, TTL indexes on OAuth grants/tokens and refresh tokens — well designed. Indexes reference Phase-3 collections (`mail_passkeys`, `mail_oauth_*`) that have no handlers yet (forward scaffolding, harmless).

### 3.2 JWT generation / validation
- `pkg/jwt/jwt.go`: HS256, custom `Claims{uid,email}` + registered claims (`iss=betazen-mail`, `sub`, `iat`, `exp`). `Validate` correctly rejects non-HMAC signing methods (`alg` confusion guard at `jwt.go:48`). Access TTL default 15m, refresh 168h (`config.go:59-66`).
- `middleware/auth.go`: requires `Authorization: Bearer <jwt>`, validates, injects `user_id`/`user_email` into `c.Locals`. Standard and correct.

### 3.3 Service tokens — GAP
- The panel calls mail-suite admin endpoints (`/api/v1/dns/:domain/enable-mail`, `/status`) with `Authorization: Bearer <ServiceToken>` (`mailsuite_service.go:214`). The installer writes a `PANEL_SERVICE_TOKEN` into mail-suite's `.env` (`mail_suite_install.go:189`).
- **BUT the mail-suite backend never reads or validates `PANEL_SERVICE_TOKEN`.** `grep -ri "PANEL_SERVICE_TOKEN|ServiceToken|service_token" mail-suite/` → **no matches.** `config.go` has no such field. The DNS routes sit behind the *user-JWT* `middleware.Auth` (`api_routes.go:32,59-60`). A panel-issued service token is not a valid user JWT, so these proxied calls would return **401 invalid token**. The "Enable Mail" / per-domain status feature is therefore **non-functional end-to-end** even after a successful install.

### 3.4 Session management
- Refresh tokens: random 32-byte hex (`jwt.go:59`), stored in `mail_refresh_tokens` with UA/IP, **rotated on use** (old deleted, new issued — `auth_service.go:72-89`), TTL-indexed for expiry. Good.
- Logout deletes the presented refresh token. No server-side access-token revocation (acceptable for 15m HS256).

### 3.5 AuthN / AuthZ within mail-suite
- All non-auth routes require a valid user JWT. Per-resource ownership is enforced by always scoping Mongo queries with `user_id` (e.g. `account_service.go:43`, `mail_service.go`, `forwarder_service.go:63`, `device_service.go:62`). No cross-user IDOR observed. There is **no role model** — every authenticated user is a flat end-user; the DNS "enable-mail" endpoint is exposed to *any* logged-in user (intended to be admin/service-only, see 3.3), which is itself an authZ design gap.

### 3.6 Credential storage — SECURITY
- `MailAccount.Secret` (the IMAP/SMTP password for each attached mailbox) is stored **in plaintext**: `account_service.go:62` sets `Secret: req.Password` directly; the model comment at `account.go:28-31` openly states "we store the password in plaintext … a future ticket will swap in AES-GCM." It is `json:"-"` so it is never returned to clients, but anyone with DB read access gets every attached mailbox's live credentials (including external Gmail app-passwords). Acceptable risk note exists in code, but it is a real exposure if Mongo is ever readable.
- User account passwords are correctly bcrypt-hashed (`pkg/password`), and `User.PasswordHash` is `json:"-"`. `RefreshToken.Token` is `json:"-"`.

### 3.7 IMAP/SMTP clients
- `imap_client.go` / `smtp_client.go` use `emersion/go-imap` and `go-smtp` correctly: TLS via `DialTLS` when `*SSL`, plain/STARTTLS otherwise, SASL PLAIN auth. For `provider:"betazen"` accounts the host/ports are forced to the local Postfix/Dovecot from config (`account_service.go:70-76`) with `*SSL=false` (plaintext to 127.0.0.1 — fine for loopback). This is exactly the README's "Dovecot remains canonical; backend stores only metadata" model — mail-suite is an IMAP/SMTP *client*, not an MTA.

### 3.8 Background workers / FCM push
- **No background workers run.** `cmd/server/main.go` starts only the Fiber listener; there is no IDLE worker / goroutine loop. Device registration (`device_service.go`) just upserts FCM tokens into `mail_devices`.
- **FCM is a stub.** README confirms: "Live FCM dispatch from the IDLE worker is implemented as a stub; full implementation in `idle_worker.go` lands next." There is **no `idle_worker.go`** in the tree and **no Firebase/FCM SDK** in `go.mod`. `FCM_CREDENTIALS_FILE` / `FCM_PROJECT_ID` are read into config but never used. So push notifications do not work; the mobile app can register a token but will never receive a push.

### 3.9 Logging
- Zerolog (structured), Fiber request `logger.New()` and `recover.New()` middleware. CORS is wide open: `AllowOrigins:"*"` with `AllowCredentials:false` (`main.go:80-86`) — acceptable because auth is Bearer-header (not cookies), but `*` means any origin can drive the API with a stolen token.

### 3.10 Config / env requirements
`.env.example` + `config.go`. Required-ish for a real deploy:
- `MONGO_URI`, `MONGO_DB` (default `mail_suite`)
- `JWT_SECRET` — **defaults to the literal `change-me-mail-suite-secret`** if unset (`config.go:76`, via `requireEnv` which despite its name does NOT fail-closed). Shipping without setting this = forgeable tokens. The installer auto-generates a 32-byte hex secret (`mail_suite_install.go:69-75`), so the one-click path is safe; a hand-run `go run ./cmd/server` is not.
- `SERVER_PORT` (9090), `PUBLIC_URL`, `WEBMAIL_DIR` (serves built SPA at `/mail/`)
- `IMAP_HOST/PORT`, `SMTP_HOST/PORT` (default 127.0.0.1:143 / :587)
- `BETAZEN_PANEL_URL` / `BETAZEN_PANEL_TOKEN` — optional; blank = DNS/forwarder bridge no-ops (`betazen_panel_client.go:42`)
- `FCM_*`, `WEBAUTHN_*`, `OAUTH_ISSUER` — read but unused (Phase 2/3 scaffold)

### 3.11 API surface (`routes/api_routes.go`)
Public: `POST /api/v1/auth/{register,login,refresh,logout}`. Protected (user JWT): `GET /auth/me`; accounts CRUD + set-primary; mail folders/threads/message/flag/send; signatures CRUD; forwarders CRUD; device register/delete; `POST /dns/:domain/enable-mail`, `GET /dns/:domain/status`. Plus `GET /healthz` and the `/mail/*` SPA static mount. Reasonable and complete for Phase 1.

### 3.12 Dockerfile / Makefile
- `Dockerfile`: multi-stage golang:1.22-alpine → alpine:3.20, builds `./cmd/server`, `EXPOSE 9090`. Builds the **API only** — it does NOT build/copy the webmail `dist`, so a container would serve API without the SPA unless `WEBMAIL_DIR` is mounted. No Docker is used on these servers anyway.
- `Makefile`: `dev` (go run), `build` (linux/amd64 → ./bin/mail-suite), `test`, `tidy`, `clean`. No webmail or systemd targets.

---

## 4. systemd / nginx / certbot / TLS expectations (per README + installer)

The intended production deployment is **not** the Dockerfile — it is the panel's agent installer `agent.InstallMailSuite` (`backend/internal/agent/mail_suite_install.go`), driven by **WHM → Developer → Mail Suite → Install** (one domain field). It is designed to:
1. `mkdir /opt/mail-suite`
2. Build the Go binary in place (bootstrapping Go 1.22.5 via download if absent) → `/opt/mail-suite/mail-suite`
3. Build the webmail SPA (bootstrapping Node 20 via NodeSource if absent) → copy `dist` → `/opt/mail-suite/webmail`, set `WEBMAIL_DIR`
4. Write `/opt/mail-suite/.env` with auto-generated `JWT_SECRET` + `PANEL_SERVICE_TOKEN`, `PUBLIC_URL=https://<domain>`
5. Write + `enable --now` `/etc/systemd/system/mail-suite.service`
6. Write nginx vhost `/etc/nginx/sites-available/mail-suite.conf` (80→443 redirect, `proxy_pass http://127.0.0.1:9090`, bootstrap self-signed cert so `nginx -t` never breaks the box), symlink to sites-enabled
7. `certbot --nginx -d <domain>` (best-effort) for real TLS, then `nginx -t` + reload
8. Auto-register the deployment in `mailsuite_deployments`

So the expectation is: dedicated public **mail subdomain**, TLS via Let's Encrypt, reverse-proxied at `/` (webmail at `/mail/`, API same-origin). README §1 and §4 corroborate (`PUBLIC_URL=https://mail.example.com`, "served by the mail-suite backend itself … same-origin /api").

**On these servers none of this exists** (no `/opt/mail-suite`, no unit, no vhost, no `/etc/letsencrypt`). The demo is deliberately TLS-less (panel addressed by bare IP), which is itself incompatible with the mail-suite installer's hard `https://` assumptions.

---

## 5. Deployability verdict

**Not deployable as-is via the documented one-click path on these boxes.** Blockers:

1. **Install-dir path mismatch (would break one-click install).** `findMailSuiteSources()` searches `/opt/server-panel/mail-suite/{backend,webmail}` (with a hyphen) and the process cwd. The source on the live servers is `/opt/serverpanel/mail-suite` (**no hyphen**) — confirmed `ls /opt/server-panel` → "No such file or directory" on both. The serverpanel.service WorkingDirectory would have to be `/opt/serverpanel` for the cwd fallback to hit, but even then the *fallback* uses `<cwd>/mail-suite` which only matches if the service runs from `/opt/serverpanel`. If neither matches, install fails: "mail-suite backend source not found in /opt/server-panel/mail-suite/backend or $PWD/mail-suite/backend." (See finding F1.)
2. **No Go toolchain** (`go: command not found`, no `/usr/local/go`). The installer would attempt to download Go 1.22.5 and Node 20 from the internet at install time — slow, and a network-egress dependency, not a pre-baked image.
3. **No TLS / public mail subdomain.** Installer hardcodes `https://<domain>` and runs certbot; the demo has no `/etc/letsencrypt` and is IP-addressed.
4. **Service-token authZ gap (F2).** Even after a clean install, panel→mail-suite "Enable Mail"/status calls would 401 because mail-suite does not validate `PANEL_SERVICE_TOKEN`.

What a real deployment would require: a public DNS name + open 80/443, Go 1.22 + Node 20 (or pre-built binary + dist), the path fix or running the panel from a cwd whose `mail-suite/` resolves, a strong `JWT_SECRET` (installer handles), and a fix so mail-suite accepts the panel service token. Standalone (manual `go build` + `.env` + reverse proxy) is straightforward; the integrated DNS feature needs the token fix.

---

## 6. Relevance to the live mail flow

**None today.** The live, working webmail is **Roundcube** at `/webmail/` (nginx `location ^~ /webmail/` in `/etc/nginx/sites-available/serverpanel`, PHP-FPM, MariaDB `roundcube` DB), with WHM SSO auto-login. Mail Suite is explicitly described in its own README as "a separate product from the Betazen panel," and the panel's `MailSuiteService` doc-comment states it "does NOT replace the existing /api/v1/cpanel/email/* or Roundcube SSO surfaces." Mail flow (Postfix/Dovecot/OpenDKIM/SpamAssassin) is entirely independent of mail-suite. If/when deployed, mail-suite would be an *additional* webmail+mobile client reading the same Dovecot mailboxes — a parallel product, not a replacement, and not currently in the path of any message.

---

## 7. Drift between Server 1 and Server 2

**No drift.** Every deployment-status and DB check returned identical results on both hosts: source-only at `/opt/serverpanel/mail-suite`, no built artifacts, no `.env`, no Go, no `:9090`, no systemd unit, no nginx vhost, no `mail_suite` DB, `mailsuite_deployments=0`, `mail_users=0`. The clones are consistent with respect to Mail Suite.

---

## 8. Findings summary

- **F1 (high, repo):** One-click installer path mismatch — searches `/opt/server-panel` (hyphen) but deployed source is `/opt/serverpanel` (no hyphen). `agent/mail_suite_install.go:473`. One-click install fails unless cwd resolves `mail-suite/`.
- **F2 (high, repo):** Service-token authZ gap — panel sends `Bearer <ServiceToken>` to mail-suite `/dns/*`, mail-suite has no `PANEL_SERVICE_TOKEN` validation; calls 401. `mailsuite_service.go:214` vs `mail-suite/.../api_routes.go:32` + `config.go`.
- **F3 (medium, repo):** Attached-mailbox passwords stored plaintext in `mail_accounts.secret`. `account_service.go:62`, `account.go:28-31`.
- **F4 (medium, repo):** `JWT_SECRET` falls back to a hardcoded literal instead of failing closed when unset; only the installer rescues this. `config.go:76,105-111`.
- **F5 (medium, repo):** FCM push is a non-functional stub; no IDLE worker, no Firebase SDK in `go.mod`; `FCM_*` config read but unused. README Phase-2 note.
- **F6 (low, repo):** DNS "enable-mail" endpoint protected only by flat user JWT (no role/admin/service gate) — any authenticated user can trigger DNS upserts. `api_routes.go:59`.
- **F7 (low, repo):** Dockerfile builds API only, not the webmail SPA; container serves no `/mail/` UI without an external `WEBMAIL_DIR` mount.
- **F8 (info, both):** Mail Suite is undeployed on both servers (parity confirmed); not in the live Roundcube mail path.

---

## Appendix — key raw evidence

Server 1 listening ports (mail-suite would be `:9090` — absent):
```
:8080 server (serverpanel)  :8081 pdns  :27017 mongod  :3306 mariadbd
:25/:465/:587 postfix  :110/:143/:993/:995 dovecot  :53 pdns  :80 nginx  :783 spamd  :21 pure-ftpd
(no :9090, no :5173)
```

Source-only, no artifacts (both servers):
```
ls /opt/mail-suite                                  -> No such file or directory
ls /opt/serverpanel/mail-suite                      -> README.md backend mobile webmail
find / -name mail-suite -executable                 -> (empty)
ls /opt/serverpanel/mail-suite/webmail/dist         -> No such file or directory
ls /opt/serverpanel/mail-suite/backend/.env         -> No such file or directory
which go ; ls /usr/local/go/bin/go                  -> not found
ls /etc/systemd/system/mail-suite.service           -> No such file or directory
ls /etc/nginx/sites-available/mail-suite.conf       -> No such file or directory
```

Path mismatch + DB state (both servers):
```
ls /opt/server-panel                                -> No such file or directory   (installer search path)
ls -ld /opt/serverpanel                             -> exists                       (actual source root)
mongosh: mailsuite_deployments=0  mail_users=0  mail_accounts=0
listDatabases -> only "serverpanel" (no "mail_suite")
```
