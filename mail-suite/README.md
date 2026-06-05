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
