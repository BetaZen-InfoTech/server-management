# Betazen Mail — Flutter client

Gmail/Zoho-style mobile client for the `mail-suite` backend.

## Features (Phase 1 + 2)

- Sign-in against the mail-suite backend (`/api/v1/auth/login`).
- Multi-account: each user can attach several mailboxes (Settings → Mail accounts) and switch which one the inbox is reading from.
- Inbox / folders, message detail, compose, send.
- HTML signatures with default-selection (Settings → HTML signatures).
- FCM push notifications when new mail arrives (server-side IDLE worker → FCM → local notification).
- "Auto Passkey Login" — biometric / device-credential gate on app open. (Full WebAuthn ceremony lands in Phase 3.)

## Running

```
flutter pub get
flutter run
```

In the login screen, point **Server URL** at your running `mail-suite/backend` (default `http://localhost:9090`).

## Firebase / FCM setup

Push notifications require Firebase. Drop your project's files in:

- `android/app/google-services.json`
- `ios/Runner/GoogleService-Info.plist`

Without these, `FcmService.init()` logs a warning and continues; the rest of the app still works.

## Phase 3 (Passkey + OAuth)

The settings toggle for "Auto Passkey Login" currently uses `local_auth` (biometric / device-credential gate). When the backend's `/api/v1/passkey/*` endpoints land in Phase 3, this will switch to a real WebAuthn ceremony.
