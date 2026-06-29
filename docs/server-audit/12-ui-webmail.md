# Agent 14 — UI / Webmail Audit

**Date:** 2026-06-29
**Agent:** 14 — UI / Webmail Tester
**Box:** Server 1 (demo / prod-clone) = `89.116.34.207`
**Method:** Read-only HTTP verification over SSH (`curl` against `:8080` Go panel direct, `:80` nginx) + static-asset probes + Dovecot user lookup + a single Roundcube login POST (wrong-password observation only). No mail sent, no state mutated.
**Constraint:** No interactive browser; demo domains are `.local` (not publicly resolvable). Full click-through UX of the SPAs and Roundcube is NOT possible in this environment — see *Env Limitations*.

---

## Summary

Everything in scope serves correctly over HTTP.

- **Panel SPAs (WHM + User Panel)** return `200` with a valid SPA shell (`<div id="root">`, branded `<title>`, hashed JS/CSS bundle) on both the Go panel (`:8080`) and through nginx (`:80`). Every referenced hashed asset resolves `200` with the correct content-type.
- **`/cpanel` → `/user-panel` 301 redirect works** as specified, including deep paths and on both `:8080` and `:80`.
- **Root `GET /` (unauthenticated)** issues `302 → /whm/` (Home Page disabled; documented fallback).
- **Roundcube 1.6.6 webmail is healthy** at `/webmail/`: branded login page renders (`200`), CSRF `_token` present, session cookie issued, IMAP/SMTP backends (Dovecot 143/993, Postfix submission 587) all reachable. The full auth pipeline was confirmed end-to-end via a login POST — a wrong password correctly returns `401 Login failed`, proving Roundcube reaches and queries the IMAP backend.
- **Security headers**: nginx adds `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy` on panel + webmail surfaces. No CSP anywhere; HSTS absent (the box is HTTP-only, so HSTS is moot here). The Go panel on `:8080` emits **no** security headers itself — protection relies entirely on the nginx layer.

No regressions vs the prior audit (`2c3357e:.../webmail-ui.md`). One minor housekeeping item: a stale WHM JS bundle remains on disk after a rebuild.

---

## What Was Verifiable Over HTTP

| Surface | Check | Result |
|---|---|---|
| Panel `/` (`:8080`) | unauth root redirect | `302 → /whm/` |
| Panel `/whm/` (`:8080`) | SPA shell + 200 | `200` text/html, `Cache-Control: no-store`, `<div id="root">`, `<title>Betazen Server Panel - WHM</title>` |
| Panel `/user-panel/` (`:8080`) | SPA shell + 200 | `200` text/html, `no-store`, `<div id="root">`, `<title>Betazen Server Panel - User Panel</title>` |
| `/whm/assets/index-Dq0pbI4j.js` | hashed JS asset | `200 text/javascript` |
| `/whm/assets/index-1oFrfvF1.css` | hashed CSS asset | `200 text/css` |
| `/user-panel/assets/index-CB5yrilK.js` | hashed JS asset | `200 text/javascript` |
| `/user-panel/assets/index-ueHYG4J7.css` | hashed CSS asset | `200 text/css` |
| `/cpanel` (`:8080`) | legacy redirect | `301 → /user-panel/` |
| `/cpanel/` (`:8080`) | legacy redirect | `301 → /user-panel/` |
| `/cpanel/dashboard` (`:8080`) | deep-path redirect | `301 → /user-panel/dashboard` (path preserved) |
| `/` `/whm/` `/user-panel/` `/cpanel` (`:80` nginx, IP Host) | nginx routing parity | `302 / 200 / 200 / 301` — matches `:8080` |
| `/webmail/` (`:80`, IP Host) | Roundcube reachable | `200 text/html` |
| `/roundcube/` (`:80`, IP Host) | alt path | `404` (path is `/webmail/`, not `/roundcube/`) |
| `/webmail/` login page | CSRF + form | `<title>...Betazen Server Panel Webmail</title>`, `_token` (32 chars), `action="/webmail/?_task=login"`, `rcmloginuser`/`rcmloginpwd` fields, `roundcube_sessid` HttpOnly cookie |
| IMAP `127.0.0.1:143` | TCP + banner | OPEN — `* OK [CAPABILITY ... STARTTLS AUTH=PLAIN AUTH=LOGIN] Dovecot` |
| IMAPS `127.0.0.1:993` | TCP | OPEN (Dovecot) |
| Submission `127.0.0.1:587` | TCP | OPEN (Postfix master) |
| Roundcube login POST (wrong pass) | auth pipeline | `401 Unauthorized` + body "Login failed" — backend queried, credential rejected |
| `/webmail/installer/` | hardening | `404` (installer blocked) |
| `/webmail/config/config.inc.php` | hardening | `404` (config not web-readable) |
| `/webmail/.htaccess` | hardening | `403` (dotfiles denied) |

### Security headers observed

| Surface | X-Frame-Options | X-Content-Type-Options | Referrer-Policy | CSP | HSTS |
|---|---|---|---|---|---|
| Panel `/whm/` via `:8080` (Go direct) | — | — | — | — | — |
| Panel `/whm/`, `/` via `:80` (nginx) | `SAMEORIGIN` | `nosniff` | `strict-origin-when-cross-origin` | absent | absent (HTTP box) |
| Webmail `/webmail/` via `:80` | `SAMEORIGIN` | `nosniff` | `strict-origin-when-cross-origin` | absent | absent |

> Note: nginx is the sole source of the security headers. The Go panel served raw on `:8080` returns none; if anything were ever proxied around nginx (or `:8080` exposed), the headers would be lost.

---

## Roundcube Webmail State

- **Version:** Roundcube **1.6.6** (`roundcube-core 1.6.6+dfsg-2ubuntu0.1`).
- **Served at:** nginx `location ^~ /webmail/` → `alias /var/lib/roundcube/public_html/`; `.php` to php8.2-fpm. (There is no `/roundcube/` path and no dedicated `mail.*` webmail vhost — the `mail.demo-one.local` server block proxies to `:8091`, which is the panel's per-domain `app` returning JSON, NOT Roundcube.)
- **Login page:** renders `200`, branded title "Betazen Server Panel Webmail", CSRF `_token` present (32 chars), HttpOnly `roundcube_sessid` cookie, form posts to `/webmail/?_task=login`.
- **Backend config:** `imap_host = ["localhost:143"]`, `smtp_host = "tls://localhost:587"`. Both reachable; Dovecot advertises `STARTTLS AUTH=PLAIN AUTH=LOGIN`.
- **End-to-end auth confirmed:** mailbox `admin@mail.demo-one.local` exists in Dovecot (uid 5000, `maildir:/home/demoone/mail/mail.demo-one.local/admin`). A login POST with the correct CSRF token + session cookie and a deliberately wrong password returned `401 Login failed` — i.e., Roundcube successfully forwarded the credential to the IMAP backend and the backend rejected it. The webmail → IMAP path is functional; a correct password would establish a session.
- **Real-credential login not exercised:** the mailbox password is stored as an AES-GCM `encrypted_pass` per mailbox in MongoDB (keyed by `SHA256(JWT_SECRET)`); `mongosh` here required auth and was not used to decrypt it. Per scope rules I did **not** brute force. The form-load + backend-reachability + wrong-password-rejection together already prove the auth chain works; only the final "valid session with real password" step is unverified (needs the decrypted mailbox password or the panel's SSO bridge).
- **Hardening (good):** installer blocked (`404`), config not web-readable (`404`), dotfiles denied (`403`).

---

## Env Limitations (what needs a real browser)

1. **SPA runtime rendering** — HTTP confirms the shell + bundle download, but not that React mounts, routes (`/whm/login`, `/user-panel/login`), or renders without JS console errors. Needs a browser/headless Chromium.
2. **Login UX & token flow** — actual JWT login, role-split (WHM rejects non-owners; User Panel rejects `vendor_owner`), and post-login dashboards cannot be clicked through here.
3. **Roundcube authenticated session** — inbox, compose, send, folder ops are not testable without (a) a real mailbox password or (b) the panel SSO "open webmail" bridge, plus a browser to drive the JS-heavy Elastic skin.
4. **`.local` domains not resolvable** — the per-domain mail vhosts (`mail.demo-one.local` → `:8091`) can only be reached by forging the `Host` header; no DNS/TLS browser path exists in this env.
5. **PWA / service worker** — `manifest.webmanifest` and `registerSW.js` download, but install/offline behavior needs a browser.

---

## Issues

| # | Severity | Issue |
|---|---|---|
| 1 | Low | **No Content-Security-Policy** on any served surface (panel or webmail). A CSP would harden the SPAs and Roundcube against XSS/clickjacking beyond the existing `X-Frame-Options`. |
| 2 | Low | **Go panel (`:8080`) emits no security headers** of its own — `X-Frame-Options`/`nosniff`/`Referrer-Policy` exist only because nginx adds them. Single point of failure if `:8080` is ever reached directly or proxied around nginx. |
| 3 | Low | **Stale WHM asset on disk:** `/opt/serverpanel/frontend/apps/whm/dist/assets/` contains both the live `index-Dq0pbI4j.js` and an orphaned `index-KXqLuqsI.js` (the bundle referenced by the prior 2026-06-28 audit). The served `index.html` points only at the current hash, so it is harmless, but old bundles should be pruned on rebuild to avoid disk creep and confusion. |
| 4 | Info | **HTTP-only box (no TLS/443).** HSTS absent and the webmail SSO bridge would pass credentials over plaintext. Expected for a demo box; flagged so it is not carried into a public deployment. (See prior audit for SSO-token cleartext-password detail.) |
| 5 | Info | **`manifest.webmanifest` served as `text/plain`** rather than `application/manifest+json` — cosmetic, browsers still parse it via `<link rel="manifest">`; strict PWA validators flag it. |

---

## Recommendations

1. **Add a Content-Security-Policy** to the nginx panel + webmail blocks (start in report-only mode). Allow the Google Fonts origins the SPAs reference (`fonts.googleapis.com`, `fonts.gstatic.com`).
2. **Set the baseline security headers in the Go server too** (not only nginx), so the panel is safe regardless of how it is fronted. Keep nginx as defense-in-depth.
3. **Prune stale frontend bundles on deploy** — have the build/deploy step clear `dist/assets/` before copying the new build, or run a cleanup pass, so only the live hashes remain.
4. **For full UI/webmail sign-off**, run a headless-browser pass (Playwright/Puppeteer) against a TLS-enabled staging host with resolvable domains: SPA mount + route + login, role-split enforcement, and an authenticated Roundcube session (compose/draft, no send).
5. **On any public/TLS deployment**, enable HSTS and re-verify the webmail SSO bridge no longer exposes the mailbox password over plaintext.
