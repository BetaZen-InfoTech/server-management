# AGENT 14 — UI / WEBMAIL AUDIT

**Date:** 2026-06-28
**Scope:** Panel SPA (WHM + User Panel) routing/asset serving, Roundcube webmail, phpMyAdmin — best-effort via CLI/curl (no browser).
**Servers:** Server 1 = `89.116.34.207` (migration SOURCE), Server 2 = `195.35.7.64` (migration DEST).
**Deployed code:** local git repo @ v3.1.109 / rev 466b52e.
**Method:** Read-only. Inspected backend SPA-serving logic in the repo, then probed runtime over SSH with `curl` against `:8080` (Go panel direct) and `:80` (nginx). No mutating commands run.

> **Headline:** Everything in scope is healthy and the two servers are byte-for-byte identical (same asset hashes, same package versions, same configs — only the per-host public IP differs). One curl-visible gotcha is documented (nginx 404s any request whose `Host` header is not the public IP — by design, but a trap for naive health checks). Webmail + phpMyAdmin are well-hardened. Two low-severity items: a phpMyAdmin version-disclosure file (`/phpmyadmin/README` → 200) and the SSO bridge passing the mailbox password inside the token.

---

## A. PANEL SPA (WHM + User Panel)

### A.1 Backend routing logic (read from repo)

Source of truth: `c:/Users/Administrator/Downloads/Project/server-management/backend/cmd/server/main.go`.

- **`GET /` role redirect** (lines 734–755): wrapped in `middleware.OptionalAuth`. `vendor_owner` → `302 /whm/`; `vendor_admin|vendor_staff|developer|support|customer` → `302 /user-panel/`. Unauthenticated → renders the public Home Page if `home_page.enabled` (or `?preview=1`), else falls back to `302 /whm/`. Render failures fall through to the `/whm/` redirect, never a 500.
- **WHM SPA** (lines 605–658): hashed assets under `/whm/assets` cached 1y; PWA artefacts (`sw.js`, `registerSW.js`, `manifest.webmanifest`, `pwa-icon.svg`, `workbox-*.js`) served explicitly with correct cache headers BEFORE the catchall; `index.html` served `no-store` for `/whm`, `/whm/`, `/whm/*`.
- **User Panel SPA** (lines 665–700): same pattern, served from `./frontend/apps/cpanel/dist` (legacy dir name), mounted at `/user-panel/*`.
- **`/cpanel` → `/user-panel` 301 alias** (lines 706–718): `cpanelAlias` rewrites the path prefix, preserves the query string, and emits `301 Moved Permanently`. Registered for `/cpanel`, `/cpanel/`, `/cpanel/*`.
- Note in code (lines 577–581): deliberately NO redirect handler for bare `/whm` (Fiber non-strict routing would loop) — bare `/whm` serves index.html directly. Confirmed at runtime.
- Root-dispatch logic is unit-tested: `backend/internal/handlers/home_page_root_test.go` covers the 4 render-vs-redirect paths + the `show_whm_login=false` path.

### A.2 Runtime — direct to Go panel (`:8080`), BOTH servers identical

```
$ curl -sD - http://127.0.0.1:8080/<path>   (Server 1 and Server 2 returned identical results)

GET /                  -> 302 Found,  Location: /whm/
GET /whm/              -> 200 OK,     Content-Type: text/html, Cache-Control: no-store,no-cache,must-revalidate  (size 1580)
GET /user-panel/       -> 200 OK,     Content-Type: text/html, Cache-Control: no-store,no-cache,must-revalidate  (size 1634)
GET /cpanel            -> 301 Moved Permanently, Location: /user-panel/
GET /cpanel/           -> 301 Moved Permanently, Location: /user-panel/
GET /cpanel/dashboard  -> 301 Moved Permanently, Location: /user-panel/dashboard   (deep path + suffix preserved)
```

The `/cpanel → /user-panel` 301 works as specified (including deep paths), and the root `GET /` for an unauthenticated client redirects to `/whm/` (Home Page is disabled — the documented fallback).

### A.3 index.html asset references + asset probe (BOTH servers)

`<title>` (read from deployed dist): WHM = `Betazen Server Panel - WHM`; User Panel = `Betazen Server Panel - User Panel`.

WHM `/whm/` references — every asset probed returns 200 with the correct MIME:

```
/whm/pwa-icon.svg              -> 200 image/svg+xml
/whm/assets/index-KXqLuqsI.js  -> 200 text/javascript
/whm/assets/index-1oFrfvF1.css -> 200 text/css
/whm/manifest.webmanifest      -> 200 text/plain
/whm/registerSW.js             -> 200 text/javascript
```

User Panel `/user-panel/` references — all 200:

```
/user-panel/pwa-icon.svg               -> 200 image/svg+xml
/user-panel/assets/index-CB5yrilK.js   -> 200 text/javascript
/user-panel/assets/index-ueHYG4J7.css  -> 200 text/css
/user-panel/manifest.webmanifest       -> 200 text/plain
/user-panel/registerSW.js              -> 200 text/javascript
```

Cross-check: the served WHM hashes (`index-KXqLuqsI.js`, `index-1oFrfvF1.css`) exactly match the files on disk in `/opt/serverpanel/frontend/apps/whm/dist/assets/`. No dangling/404 asset references on either SPA, either server. External Google Fonts links are present (require outbound internet to render, not a server defect).

**Minor observation (info):** `manifest.webmanifest` is served as `text/plain; charset=utf-8` rather than `application/manifest+json`. Browsers still parse it (it is referenced via `<link rel="manifest">`), so this is cosmetic, but a strict PWA validator would flag it. Identical on both servers.

### A.4 Runtime — via nginx (`:80`)

**Important curl gotcha (documented, not a defect):** the panel vhost is `listen 80 default_server` with `server_name <IP> <IP> _;` and contains:

```
# /etc/nginx/sites-enabled/serverpanel  (Server 1)
if ($host !~* ^(89.116.34.207|89.116.34.207)$) { return 404; }
```

So a request with `Host: 127.0.0.1` (the curl default when hitting `127.0.0.1:80`) returns **404** for every path. This is intentional host-pinning (the panel is addressed only by bare IP), but it will trip any uptime probe / health check that hits `http://127.0.0.1/` without setting the IP `Host` header.

With the correct Host header (or hitting the public IP directly), nginx routes everything correctly — verified on **both** servers:

```
$ curl -H "Host: 89.116.34.207" http://127.0.0.1:80/<path>     (S1; S2 identical with its own IP)
/             -> 302  Location: /whm/
/whm/         -> 200  text/html
/user-panel/  -> 200  text/html
/cpanel       -> 301  Location: /user-panel/
/webmail/     -> 200  text/html; charset=UTF-8
/phpmyadmin/  -> 200  text/html

$ curl http://89.116.34.207/        -> 302 Location: http://89.116.34.207/whm/   (public IP, real-world path: works)
$ curl http://89.116.34.207/whm/    -> 200
```

The `_` catch-all in `server_name` is overridden by the explicit `if ($host !~ ...) return 404`, so the box does NOT serve the panel to arbitrary Host headers (vendor vhosts get their own server blocks; non-matching hosts 404). The duplicated IP in both `server_name <IP> <IP> _` and the `if` regex `^(<IP>|<IP>)$` is cosmetic redundancy (likely a template that concatenates a primary + alias that happen to be equal here) — harmless, same on both servers.

---

## B. ROUNDCUBE WEBMAIL

Served by nginx `location ^~ /webmail/` as an `alias` to `/var/lib/roundcube/public_html/`, with `.php` handed to `php8.2-fpm` over `unix:/var/run/php/php8.2-fpm.sock`. BOTH servers identical.

### B.1 Login page renders (HTTP 200, real Roundcube)

```
$ curl -H "Host: <IP>" http://127.0.0.1:80/webmail/
HTTP/1.1 200 OK
Content-Type: text/html; charset=UTF-8
Set-Cookie: roundcube_sessid=...; path=/; HttpOnly
X-Frame-Options: sameorigin
<title>Betazen Server Panel Webmail :: Welcome to Betazen Server Panel Webmail</title>
form action="/webmail/?_task=login"  fields: _user, _pass, _token (CSRF), DOM ids rcmloginuser / rcmloginpwd
```

- Branded title (`product_name = 'Betazen Server Panel Webmail'`), CSRF `_token` present, `HttpOnly` session cookie, `X-Frame-Options: sameorigin`. This is a healthy, rendered login page.
- All referenced skin/JS assets resolve (relative to `/webmail/`, so the `alias` makes them work):
  `skins/elastic/styles/styles.min.css -> 200 text/css`, `program/js/app.min.js -> 200 application/javascript`.

### B.2 Version + DB + IMAP/SMTP + plugins

- **Version:** Roundcube **1.6.6** (`roundcube-core 1.6.6+dfsg-2ubuntu0.1`, `RCMAIL_VERSION '1.6.6'`). `roundcube-mysql` driver installed.
- **`/var/lib/roundcube/public_html`** exists: `index.php`, `sso.php`, plus symlinks `skins`, `plugins`, `.htaccess`, and a `program/` dir.
- **Config** (`/etc/roundcube/config.inc.php`, mirrored at `/var/lib/roundcube/config/config.inc.php`):
  - `imap_host = ["localhost:143"]`  (matches assignment: IMAP host = localhost)
  - `smtp_host = 'tls://localhost:587'`  (submission with STARTTLS to local Postfix)
  - `plugins = ['archive', 'zipdownload']`
  - `des_key` set, length **24** (correct DES key length — not the shipped default/placeholder).
- **DB connection** (`/etc/roundcube/debian-db.php`): `dbtype=mysql`, `dbserver=localhost:3306`, `dbname=roundcube`, `dbuser=roundcube`, password set (masked). Live check against MariaDB: **17 tables** in `roundcube` (`users`, `session`, `contactgroups`, `cache_messages`, `cache_index`, `searches`, `collected_addresses`, `responses`, ...). DB is connected and schema-loaded.
- **php-fpm:** `php8.2-fpm` active; socket `/var/run/php/php8.2-fpm.sock` present (owner www-data).

### B.3 Webmail hardening (good)

- `enable_installer = false` (in `defaults.inc.php`); installer URL blocked: `/webmail/installer/ -> 404`.
- Config not web-readable: `/webmail/config/config.inc.php -> 404`; dotfiles denied by `location ~ /\. { deny all; }`: `/webmail/.htaccess -> 403`.
- **SSO bridge** `sso.php` (panel "click to open webmail"): decodes a base64url token `{email, ts, sig, pass}`, verifies `HMAC-SHA256(email|ts, /etc/roundcube/sso_hmac_secret)` with `hash_equals` (constant-time), enforces a **±60s replay window**, then calls `$rcmail->login()` and regenerates the session id. `sso_hmac_secret` present (65 bytes). The HMAC + short window + constant-time compare is a sound design.
  - **Caveat (low):** the token carries the cleartext mailbox `pass` (so Roundcube can do a real IMAP login). It travels over plain HTTP (no TLS/443 on this box) in the `?token=` query string and would land in nginx/webserver access logs and browser history. The 60s window limits exposure, but on an HTTPS deployment this is materially safer; on the current HTTP-only demo it is a residual exposure. Same on both servers.
- **`managesieve` plugin is installed on disk** (`/usr/share/roundcube/plugins/managesieve`) but is **NOT enabled** in `config['plugins']`. Functional note: users cannot manage server-side Sieve filters / vacation auto-responders from webmail. If the product intends to expose filters, the plugin needs to be added to the plugins array (out of scope to change here).

---

## C. phpMyAdmin

Served by nginx via `include /etc/nginx/snippets/phpmyadmin.conf` → `location ^~ /phpmyadmin/` alias to `/usr/share/phpmyadmin/`, `.php` to php8.2-fpm. BOTH servers identical.

### C.1 Reachable + renders login

```
$ curl -H "Host: <IP>" http://127.0.0.1:80/phpmyadmin/
HTTP/1.1 200 OK
<title>phpMyAdmin</title>   form fields: pma_username, pma_password, token (CSRF)
Set-Cookie: phpMyAdmin=...; path=/phpmyadmin/; HttpOnly; SameSite=Strict
Set-Cookie: pma_lang=en; ...; HttpOnly; SameSite=Strict
X-Frame-Options: DENY
X-Content-Type-Options: nosniff
X-XSS-Protection: 1; mode=block
X-Content-Security-Policy / X-WebKit-CSP: default-src 'self' ...
```

`GET /phpmyadmin` (no trailing slash) → 404 (the Go panel answers, JSON) — only the trailing-slash form is the nginx-served PMA. Cosmetic; the panel UI links the trailing-slash form.

### C.2 Security posture (well-configured)

From `/etc/phpmyadmin/config.inc.php` (= `/usr/share/phpmyadmin/config.inc.php`):

- **Server 1 (cookie auth):** `auth_type = 'cookie'`, `host = 127.0.0.1`, `AllowNoPassword = false`, `blowfish_secret` set (length **32**, not default/empty — cookie encryption is sound), `hide_db = ^(information_schema|performance_schema|mysql|sys|phpmyadmin)$`.
- A second server block defines `auth_type = 'signon'` with `SignonSession = 'panel_pma_signon'`, `SignonURL = /phpmyadmin/_signon.php`, `LogoutURL = /phpmyadmin/?logout=1` — the panel's "open phpMyAdmin" SSO path (matches `PMASignonSecret` wiring in `main.go` line 70).
- Strong response headers: `X-Frame-Options: DENY`, `nosniff`, CSP, `SameSite=Strict` + `HttpOnly` cookies.
- No `AllowRoot=true` and `AllowNoPassword=false`, so root/passwordless logins are refused at the PMA layer; system DBs are hidden.

### C.3 phpMyAdmin findings

- **(LOW) Version-disclosure file readable:** `/phpmyadmin/README -> 200` (CHANGELOG → 404). The nginx static-file regex in `phpmyadmin.conf` serves files by extension only for image/css/js/etc., but `try_files $uri $uri/ =404` still serves extension-less docs like `README`. README discloses the phpMyAdmin version, aiding targeted exploitation. Both servers.
- **(LOW) No edge auth / IP allowlist:** `/phpmyadmin/` is reachable from the public IP (`curl http://<IP>/phpmyadmin/ -> 200`) with no nginx `auth_basic` or `allow/deny`. Access control rests entirely on MySQL credentials + the PMA cookie/signon layer. For a publicly-addressable DB admin surface, an `allow <admin-ip>; deny all;` or basic-auth gate in front is a defense-in-depth improvement. Same for `/webmail/` (expected — webmail must be public). Both servers.
- The `^~` prefix on `location ^~ /phpmyadmin/` makes it take precedence over the `location /` panel proxy, so PMA is correctly reached instead of being swallowed by the SPA catchall. Good.

---

## D. SERVER-TO-SERVER DRIFT

**None of substance.** Server 1 and Server 2 are clones and remain identical across everything inspected:

| Item | Server 1 (89.116.34.207) | Server 2 (195.35.7.64) |
|---|---|---|
| Panel `/`, `/whm/`, `/user-panel/`, `/cpanel` behavior | 302/200/200/301 | identical |
| WHM asset hashes | index-KXqLuqsI.js / index-1oFrfvF1.css | identical |
| User Panel asset hashes | index-CB5yrilK.js / index-ueHYG4J7.css | identical |
| Roundcube version | 1.6.6 | 1.6.6 |
| Roundcube DB tables | 17 | 17 |
| Roundcube plugins | archive, zipdownload | identical |
| phpMyAdmin auth | cookie + signon, AllowNoPassword=false | identical |
| nginx host-pin `if ($host…) return 404` | present | present |
| README info-leak | 200 | 200 |
| Secrets randomized (blowfish32 / des24 / hmac65) | yes | yes |

Only the literal public IP differs (correctly, per host) in `server_name`, the `if ($host…)` regex, and the public-IP curl tests.

---

## E. REQUIRES A REAL BROWSER — OUT OF SCOPE FOR CLI

The following could NOT be validated by curl and are explicitly marked out of scope:

- Full SPA boot/interaction: does the JS bundle hydrate, does the WHM/User Panel login form actually authenticate and render the dashboard (React runtime, Zustand state, route guards). **requires browser.**
- Webmail inbox actions: composing/sending, folder navigation, IMAP message list rendering, the SSO auto-login landing in `?_task=mail`. **requires browser** (plus a real mailbox — none exist; `mailboxes: 0`).
- phpMyAdmin post-login DB browsing and the panel→PMA signon round-trip (`_signon.php`). **requires browser** + a provisioned DB user (none exist; `databases: 0`).
- PWA install / service-worker registration behavior (`sw.js`, workbox runtime). Files serve correctly; actual SW lifecycle needs a browser.
- Visual/console-error inspection of either SPA (JS console warnings, CSP violations at runtime). **requires browser.**

---

## F. SUMMARY OF FINDINGS

| # | Severity | Server | Finding |
|---|---|---|---|
| 1 | info | both | nginx vhost 404s any request whose `Host` != public IP (`if ($host !~ ^(<IP>|<IP>)$) return 404`). By design, but a trap for `127.0.0.1` health checks. |
| 2 | low | both | phpMyAdmin `/phpmyadmin/README` returns 200 — discloses PMA version. |
| 3 | low | both | phpMyAdmin (and webmail) reachable on the public IP with no edge auth_basic / IP allowlist; relies solely on app-layer creds. Defense-in-depth gap for a DB admin surface. |
| 4 | low | both | Roundcube SSO bridge passes cleartext mailbox password inside `?token=` over plain HTTP (no TLS/443); lands in access logs/history. 60s window + HMAC mitigate; HTTPS would close it. |
| 5 | info | both | `manifest.webmanifest` served as `text/plain` not `application/manifest+json` (cosmetic; browsers still parse). |
| 6 | info | both | `managesieve` plugin installed on disk but NOT enabled in `config['plugins']` — server-side filters/vacation unavailable in webmail. |
| 7 | info | both | Duplicated IP in `server_name <IP> <IP> _` and the `if` regex — cosmetic redundancy. |

**No critical/high issues.** All in-scope surfaces (both SPAs + their assets, the `/cpanel`→`/user-panel` 301, role-based root redirect, Roundcube login render + DB/IMAP/SMTP config, phpMyAdmin login + auth posture) are healthy and consistent across both servers.
