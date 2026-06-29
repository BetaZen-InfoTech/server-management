# 03 — Mail Suite Audit (Server 1, 89.116.34.207)

**Audited:** 2026-06-29 · **Mode:** read-only. No changes applied.

## Summary

The **Betazen Mail Suite is a separate product**, not the panel's built-in mail module. It is a three-part app (Go Fiber backend + React/Vite webmail SPA + Flutter mobile) that talks to the *same* local Postfix/Dovecot over IMAP 143 / SMTP 587, storing only metadata in its own `mail_suite` MongoDB database, and delegates DNS to the panel's PowerDNS pipeline via `BETAZEN_PANEL_URL`/`BETAZEN_PANEL_TOKEN`.

**On Server 1 it is staged but NOT deployed.** The source tree exists at `/opt/serverpanel/mail-suite` (backend, webmail, mobile), but there is **no `mail-suite.service`**, **no `mail_suite` Mongo database**, and **no `mail_suite_deployments` collection** registered in the panel. The panel ships a one-click installer (`backend/internal/agent/mail_suite_install.go`) that *would* build the binary, write `/opt/mail-suite/.env`, install a systemd unit + nginx reverse-proxy vhost on :9090, and obtain a certbot cert — but it has not been run here.

The **active webmail on this box is Roundcube 1.6.6** (served at `/webmail/`), which is a distinct, working mail client — see report 02. The Mail Suite product and Roundcube are not the same thing.

## What it is (from repo source)

- **Repo location (dev):** `mail-suite/` — `backend/` (Go Fiber + MongoDB), `webmail/` (React + Vite SPA), `mobile/` (Flutter).
- **Design (README):** "A Gmail/Zoho-style mail platform — separate product from the Betazen panel, but provisioned and DNS-controlled by it." It does *not* read Maildirs; Dovecot stays canonical. Backend stores accounts, devices, signatures, passkeys, OAuth clients, message metadata in Mongo `mail_suite`.
- **Phase status (README):** Phase 1 (foundation) done; Phase 2 (mobile + push) scaffold done with FCM dispatch as a stub; Phase 3 (Passkey/WebAuthn + OAuth2/OIDC provider) pending.

## Findings

| Area | Status | Detail |
|------|--------|--------|
| Nature | INFO | Separate product (own go.mod `github.com/betazeninfotech/mail-suite`, own React SPA, own Flutter app). NOT the panel mail module. |
| Source on server | OK | `/opt/serverpanel/mail-suite` present (owner root, created Jun 28 19:11) — backend + webmail + mobile |
| Deployment state | **NOT DEPLOYED** | No `mail-suite.service` unit (absent/inactive); not in `systemctl list-unit-files` |
| Runtime | NOT RUNNING | Nothing listening on :9090 from a mail-suite binary; service inactive |
| Mongo DB | ABSENT | No `mail_suite` database exists on the Mongo instance |
| Panel registration | ABSENT | Panel has no `mail_suite_deployments` collection / 0 registered deployments |
| Installer | OK (code) | `backend/internal/agent/mail_suite_install.go` — idempotent: builds Go binary (bootstraps Go 1.22.5 if needed), builds webmail (bootstraps Node 20), writes `/opt/mail-suite/.env`, systemd unit `mail-suite.service`, nginx vhost `/etc/nginx/sites-available/mail-suite.conf` reverse-proxying `127.0.0.1:9090`, self-signed bootstrap cert + certbot `--nginx`. Default `InstallDir=/opt/mail-suite`. |
| Source search path | OK | Installer's `findMailSuiteSources()` looks in `/opt/serverpanel/mail-suite` (matches actual layout), `/opt/server-panel`, then `$PWD` |
| API endpoints | OK (code) | `backend/internal/routes/api_routes.go`: `/api/v1` — public `auth/{register,login,refresh,logout}`; JWT-protected `auth/me`, `accounts*`, `mail/:account_id/{folders,threads,messages/:uid,send}`, `signatures*`, `forwarders*`, `devices*`, `dns/:domain/{enable-mail,status}` |
| Auth / JWT | OK (code) | JWT access (15m) + refresh (168h/7d); `JWT_SECRET` env (auto-generated 32-byte hex by installer); middleware `internal/middleware/auth.go` |
| Service token | OK (code) | Installer auto-generates `PANEL_SERVICE_TOKEN` (32-byte hex) for panel→mail-suite admin calls; panel stores it in `MailSuiteDeployment.service_token` (json `-`, not serialized out) |
| DB | OK (code) | MongoDB, db `mail_suite`; collections via `internal/database/collections.go`+`indexes.go`; metadata only (Dovecot canonical) |
| Background workers | INFO (code) | IMAP IDLE worker for push planned; README notes live FCM dispatch is a stub (full impl pending in `idle_worker.go`) |
| Logging | OK (code) | `LOG_LEVEL` env; structured logging in backend |
| Reverse proxy / TLS | OK (code) | Designed to run behind nginx at `https://<domain>/` (webmail at `/mail/`, API same-origin `/api/v1`), TLS via certbot — **not provisioned on this box** |
| Env vars (names only) | INFO | `APP_ENV, LOG_LEVEL, MONGO_URI, MONGO_DB, JWT_SECRET, JWT_ACCESS_EXPIRY, JWT_REFRESH_EXPIRY, SERVER_PORT, PUBLIC_URL, IMAP_HOST/PORT, SMTP_HOST/PORT, BETAZEN_PANEL_URL, BETAZEN_PANEL_TOKEN, WEBAUTHN_RPID/RP_NAME/ORIGINS, OAUTH_ISSUER, FCM_CREDENTIALS_FILE/PROJECT_ID, WEBMAIL_DIR, PANEL_SERVICE_TOKEN` (no values inspected) |
| Panel-side integration | OK (code) | `backend/internal/{handlers/mailsuite_handler.go, services/mailsuite_service.go, models/mail_suite_deployment.go}` — WHM → Developer → Mail Suite page registers a deployment (label/url/service_token/webmail_url) and triggers per-domain Enable-Mail |

## Issues (by severity)

| # | Severity | Issue | Detail |
|---|----------|-------|--------|
| 1 | Info / Expected | Mail Suite not deployed | Source staged at `/opt/serverpanel/mail-suite` but no systemd unit, no `mail_suite` DB, no panel registration. On a demo box whose webmail is Roundcube, this is expected — but worth recording that the Mail Suite product is dormant. |
| 2 | Low (latent) | Installer would default to a self-signed cert if certbot fails | `mail_suite_install.go` mints a throwaway self-signed cert as nginx bootstrap so `nginx -t` never breaks globally; certbot `--register-unsafely-without-email` runs best-effort. For `.local` demo domains certbot can't succeed → it would stay self-signed. (Not active here.) |

## Fixes applied

**None.** Mail Suite is not running; nothing to safely change. Read-only audit.

## Recommendations

1. **Decide deployment intent**: if Mail Suite should be live on Server 1, run the panel's one-click installer (WHM → Developer → Mail Suite, or the agent `InstallMailSuite`) with a routable domain so certbot can issue a real cert. Otherwise leave it dormant and rely on Roundcube — document that choice.
2. If deployed for the `.local` demo, expect the nginx vhost to serve the **self-signed bootstrap cert** (certbot can't validate `.local`); accept the browser warning or supply a cert manually.
3. **Phase 3 gap**: WebAuthn/passkey + OAuth2/OIDC are pending and live FCM push is a stub — don't advertise mobile push / passkey login as production-ready until `idle_worker.go` and the identity layer land.
4. Once deployed, **issue a dedicated `PANEL_SERVICE_TOKEN`** rather than reusing `BETAZEN_PANEL_TOKEN` in both directions (README flags this as a Phase-3 follow-up).
