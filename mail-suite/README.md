# Betazen Mail Suite

A Gmail/Zoho-style mail platform — separate product from the Betazen panel, but provisioned and DNS-controlled by it.

```
mail-suite/
├── backend/    # Go Fiber + MongoDB. IMAP/SMTP relay, multi-account, DNS, FCM, signatures, forwarders.
├── webmail/    # React + Vite SPA. Compose / inbox / threads / settings.
└── mobile/     # Flutter app. Multi-account, FCM push, biometric unlock.
```

## Quick start (Phase 1)

```bash
# 1. Backend
cd mail-suite/backend
cp .env.example .env
go run ./cmd/server     # listens on :9090

# 2. Webmail (separate terminal)
cd mail-suite/webmail
npm install
npm run dev             # http://localhost:5173/mail

# 3. Mobile (separate terminal)
cd mail-suite/mobile
flutter pub get
flutter run             # set Server URL to http://10.0.2.2:9090 (Android emulator) or your LAN host
```

## Architecture

The mail-suite backend talks to a local **Postfix** (submission on :587) and **Dovecot** (IMAP on :143/:993). It does *not* read Maildirs directly — Dovecot remains the canonical state. The backend stores only metadata, accounts, devices, signatures, passkeys, and OAuth clients in MongoDB (`mail_suite` database).

DNS auto-setup is delegated to the Betazen panel's existing PowerDNS pipeline via `BETAZEN_PANEL_URL` + `BETAZEN_PANEL_TOKEN`. Enabling mail on a domain upserts MX, A (`mail.<domain>`), SPF, DKIM, and DMARC records, then verifies them via DNS lookup.

## Phase status

- **Phase 1** (Foundation) ✅ — backend, webmail, multi-account, DNS, signatures, forwarders.
- **Phase 2** (Mobile + push) ✅ for scaffold — Flutter app + FCM service + biometric gate. (Live FCM dispatch from the IDLE worker is implemented as a stub; full implementation in the backend `idle_worker.go` lands next.)
- **Phase 3** (Identity) ⏳ — Passkey/WebAuthn + OAuth2/OIDC provider — pending.

## Environment

`backend/.env.example` lists every config key. Critical ones for first run:

- `MONGO_URI` — your MongoDB instance.
- `JWT_SECRET` — change before deploying.
- `BETAZEN_PANEL_URL` / `BETAZEN_PANEL_TOKEN` — leave blank to skip the DNS feature.
- `IMAP_HOST` / `SMTP_HOST` — point at your Postfix / Dovecot host.
- `FCM_CREDENTIALS_FILE` / `FCM_PROJECT_ID` — point at your Firebase service-account JSON.

---

## Where to set the URL (per component)

There are **four** URLs you may need to set, depending on which side you're configuring. Each is set in one place only.

### 1. mail-suite backend — its own public URL + the Betazen panel URL

File: `mail-suite/backend/.env` (copy from `.env.example`).

| Variable             | What it is                                                    | Example                                |
|----------------------|---------------------------------------------------------------|----------------------------------------|
| `SERVER_PORT`        | Port the backend listens on                                   | `9090`                                 |
| `PUBLIC_URL`         | The URL the world reaches **this** mail-suite backend at      | `https://mail.example.com`             |
| `BETAZEN_PANEL_URL`  | The URL of the Betazen WHM/cPanel API (for DNS upserts)       | `https://panel.betazeninfotech.com`    |
| `BETAZEN_PANEL_TOKEN`| Bearer token issued by the Betazen panel for service-to-service calls | (an API token from WHM → Developer)    |
| `IMAP_HOST` / `SMTP_HOST` | Postfix / Dovecot host (usually `127.0.0.1`)             | `127.0.0.1`                            |
| `OAUTH_ISSUER`       | Issuer URL advertised in the OpenID config (Phase 3)          | same as `PUBLIC_URL`                   |
| `WEBAUTHN_ORIGINS`   | Comma-separated list of allowed origins for WebAuthn          | `https://mail.example.com,https://app.example.com` |

After editing `.env`, restart the backend (`go run ./cmd/server` or `systemctl restart mail-suite`).

### 2. Webmail SPA — which backend it talks to

In **dev**, the Vite proxy in `mail-suite/webmail/vite.config.ts` already forwards `/api/*` to `http://localhost:9090`. Change the `target:` line there if your backend runs on a different host/port.

In **production**, the webmail is served by the mail-suite backend itself (`https://mail.example.com/mail/`) and uses **same-origin** `/api/v1/...` calls — no URL to set.

If you ever want to point the dev SPA at a **remote** backend, create `mail-suite/webmail/.env.local`:

```
VITE_API_BASE_URL=https://mail.example.com/api/v1
```

### 3. Flutter app — server URL entered at sign-in

The Flutter app **asks for the server URL on the login screen** (the field labelled "Server URL"). It is persisted in `SharedPreferences` and reused on next launch.

| Where you're running         | What to enter                            |
|------------------------------|------------------------------------------|
| Android emulator (host on same machine) | `http://10.0.2.2:9090`         |
| iOS simulator                | `http://localhost:9090`                  |
| Physical phone on your LAN   | `http://192.168.x.x:9090`                |
| Production                   | `https://mail.example.com`               |

To **clear** the stored URL (e.g. switching environments), Settings → Sign out then sign back in with the new URL. The plumbing is in `mail-suite/mobile/lib/services/api_client.dart` (`_kServerUrl`).

### 4. Betazen WHM panel — registering a mail-suite deployment

Once a mail-suite backend is running, the Betazen panel needs to know about it so the **WHM → Developer → Mail Suite** page can offer per-domain "Enable Mail" and open the webmail.

1. Open the Betazen WHM panel.
2. Sidebar → **Developer → Mail Suite**.
3. Fill in:
   - **Label** — friendly name, e.g. `Primary`.
   - **API URL** — same as the mail-suite backend's `PUBLIC_URL`, e.g. `https://mail.example.com`.
   - **Service token** — a token the mail-suite backend will accept on inbound admin calls. (Currently this is the same value as `BETAZEN_PANEL_TOKEN` you set in step 1, used in reverse — the panel sends it back to the mail-suite backend. Replace with a dedicated token once Phase 3 lands.)
   - **Webmail URL** — optional; defaults to `<API URL>/mail/`.
4. Click **Register deployment**. The deployment appears in the list with an **Open webmail** link.

That same page is where you then type a domain (e.g. `example.com`) and click **Enable mail** to upsert MX / SPF / DKIM / DMARC.

### Quick-reference flow

```
┌─────────────────────────────────────────────────────────────────────┐
│  mail-suite/backend/.env                                             │
│    SERVER_PORT=9090                                                  │
│    PUBLIC_URL=https://mail.example.com         ← set here  (1)       │
│    BETAZEN_PANEL_URL=https://panel.example.com ← set here  (1)       │
│    BETAZEN_PANEL_TOKEN=••••                                          │
└──────────────────────────┬──────────────────────────────────────────┘
                           │ same-origin /api/*
                           ▼
┌─────────────────────────────────────────────────────────────────────┐
│  mail-suite/webmail/                                                 │
│    dev:  proxy in vite.config.ts        ← change here  (2, optional) │
│    prod: served by backend; same-origin                              │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│  mail-suite/mobile/  (Flutter app)                                   │
│    typed in the LOGIN SCREEN's "Server URL" field   ← set here (3)  │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│  Betazen WHM panel  →  Developer → Mail Suite                        │
│    "API URL" field on the page                       ← set here (4) │
└─────────────────────────────────────────────────────────────────────┘
```
