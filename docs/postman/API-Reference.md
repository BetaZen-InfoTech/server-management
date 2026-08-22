# Betazen Server Panel — API Reference

Full input / output variable reference for every API surface exposed by the panel. Pair this with the Postman collection in [`Betazen-Server-Panel.postman_collection.json`](./Betazen-Server-Panel.postman_collection.json) for runnable examples.

**Base URL** — `https://panel.betazeninfotech.com` (replace with your deployment).

---

## Table of contents

1. [Conventions](#1-conventions)
2. [Authentication methods](#2-authentication-methods)
3. [Common types](#3-common-types)
4. [Authentication endpoints](#4-authentication-endpoints)
5. [Developer · API tokens](#5-developer--api-tokens)
6. [Developer · Webhooks](#6-developer--webhooks)
7. [Programmatic API · Domains](#7-programmatic-api--domains)
   - [7a. Programmatic API · Guest links](#7a-programmatic-api--guest-links)
   - [7b. Guest browser session](#7b-guest-browser-session)
8. [Programmatic API · SSL](#8-programmatic-api--ssl)
9. [Programmatic API · Email](#9-programmatic-api--email)
10. [Programmatic API · Deploy Software](#10-programmatic-api--deploy-software)
11. [Webhook payload contract](#11-webhook-payload-contract)
12. [Error reference](#12-error-reference)

---

## 1. Conventions

### Response envelope

Every endpoint returns one of three envelopes:

**Success — single object**
```json
{
  "success": true,
  "data": { /* per-endpoint payload */ }
}
```

**Success — paginated list**
```json
{
  "success": true,
  "data": [ /* array of objects */ ],
  "pagination": {
    "page": 1,
    "limit": 50,
    "total": 217,
    "total_pages": 5
  }
}
```

**Error**
```json
{
  "success": false,
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "human-readable summary",
    "details": null
  }
}
```

### Date / time format

All timestamps are ISO-8601 with timezone (`2026-05-01T10:07:59Z`). The `expires_in_days` input on token creation is an integer count of days from "now" (server time).

### IDs

Resource IDs are MongoDB ObjectIDs serialized as 24-character lowercase hex (`65f0a1b2c3d4e5f601020304`).

### Pagination

List endpoints accept `?page=` (1-based) and `?limit=`. Defaults: `page=1`, `limit=50`. Maximum `limit` is enforced server-side at 500.

---

## 2. Authentication methods

The panel exposes two distinct auth surfaces:

### A. JWT bearer (panel UI surfaces)

Used for `/api/v1/whm/*` (platform owner) and `/api/v1/cpanel/*` (vendor / team). Header:

```
Authorization: Bearer <access_token>
```

`access_token` is obtained from `POST /api/v1/auth/login` and lasts 15 minutes. Use the refresh token to mint a new pair.

### B. API token bearer (programmatic surface)

Used for `/api/v1/external/*`. Header:

```
Authorization: Bearer btz_<env>_<token_id>_<secret>
```

API tokens never expire on a session basis; they expire on the operator-chosen `expires_in_days` boundary, or never (`expires_in_days: 0`). Tokens are checked against an IP allowlist (if any), token-level rate limit, and per-route scope.

### Rate limits

| Surface | Default per minute |
|---|---|
| `/api/v1/whm/*` (JWT) | configurable, 600 default |
| `/api/v1/cpanel/*` (JWT) | configurable, 600 default |
| `/api/v1/external/*` (API token) | 600 reads / 60 writes |

Excess requests return `429` with `Retry-After: <seconds>` header.

---

## 3. Common types

These shapes recur in many endpoints. Field tables list every persisted field; `json:"-"` fields (secret material) never appear in responses.

### `User` (auth response shape)

| Field | Type | Description |
|---|---|---|
| `id` | string (ObjectID) | User's primary key |
| `email` | string | Globally unique across the panel |
| `username` | string | Linux username (for vendors / customers; empty for the owner) |
| `name` | string | Display name |
| `role` | enum | `vendor_owner` · `vendor_admin` · `vendor_staff` · `developer` · `support` · `customer` |
| `permissions` | string[] | Flat list of granted permissions |
| `is_super_admin` | bool | Bypasses permission checks within tenant scope |
| `tenant_id` | string (ObjectID) | Tenant root user_id (own _id for vendor_admin) |
| `two_factor_enabled` | bool | Whether 2FA is active on the account |
| `created_at` | string (ISO-8601) | |

### `ApiToken`

| Field | Type | Description |
|---|---|---|
| `id` | string (ObjectID) | Mongo row id |
| `token_id` | string | Public half of the bearer string (12 hex chars) |
| `prefix` | string | Display preview, e.g. `btz_prod_a1b2` |
| `name` | string | Operator-supplied label |
| `description` | string | Optional |
| `owner_kind` | enum | `owner` (issued by platform owner) or `vendor` |
| `owner_user_id` | string (ObjectID) | Whoever clicked Create |
| `tenant_id` | string (ObjectID) | Tenant the token is scoped to |
| `pinned_vendor_id` | string (ObjectID) | When set, token is immutably scoped to this vendor |
| `scopes` | string[] | See [scope catalogue](#scope-catalogue) |
| `ip_allowlist` | string[] | Exact IPs and simple `/24` CIDRs |
| `expires_at` | string\|null | `null` means never expires |
| `status` | enum | `active` · `expired` · `revoked` |
| `last_used_at` | string\|null | Stamped on every successful auth |
| `last_used_ip` | string | |
| `usage_count` | int | Auth hits since creation (or last rotate) |
| `created_at` / `updated_at` / `revoked_at` | string\|null | |

### `WebhookEndpoint`

| Field | Type | Description |
|---|---|---|
| `id` | string (ObjectID) | |
| `url` | string | Caller-controlled receiver URL (immutable) |
| `description` | string | Optional |
| `owner_kind` | enum | `owner` or `vendor` |
| `owner_user_id` / `tenant_id` | string (ObjectID) | |
| `events` | string[] | Subscribed event keys |
| `prefix` | string | Display preview of signing secret, e.g. `whsec_a1b2c3` |
| `active` | bool | When false, dispatcher skips this endpoint |
| `last_success_at` / `last_failure_at` | string\|null | |
| `last_error` | string | Plain-text last error (HTTP status, network err, …) |
| `created_at` / `updated_at` | string | |

### `WebhookDelivery`

| Field | Type | Description |
|---|---|---|
| `id` | string (ObjectID) | |
| `endpoint_id` | string (ObjectID) | |
| `event` | string | Event key (e.g. `domain.created`) |
| `payload` | string (JSON-serialized) | The exact body sent |
| `signature` | string | `sha256=<hex>` |
| `timestamp` | int (unix seconds) | Used in HMAC seed |
| `status_code` | int | Final HTTP status; 0 if all attempts failed at network layer |
| `attempts` | int | 1–3 |
| `success` | bool | True if any attempt landed 2xx |
| `error` | string | Last error if not successful |
| `next_retry_at` | string\|null | When the next retry is scheduled |
| `delivered_at` | string\|null | Set on success |
| `created_at` | string | |

### `Domain`

| Field | Type | Description |
|---|---|---|
| `id` | string (ObjectID) | |
| `domain` | string | FQDN, e.g. `acme-store.com` |
| `user` | string | Owning linux username |
| `php_version` | enum | `7.4` · `8.0` · `8.1` · `8.2` · `8.3` |
| `disk_quota_mb` | int | 0 = unlimited |
| `bandwidth_limit_gb` | int | 0 = unlimited |
| `max_databases` / `max_email_accounts` / `max_subdomains` / `max_apps` | int | 0 = unlimited |
| `ssl_active` | bool | True when a current cert exists |
| `ssl_expires` | string\|null | Cert expiry |
| `force_ssl` | bool | HTTP→HTTPS redirect on |
| `status` | enum | `active` · `suspended` |
| `environment` | enum | Deployment tier: `prod` (default) · `dev` · `test` · `local`. Empty on legacy rows → treated as `prod` |
| `registrar` / `registered_on` / `expires_on` / `auto_renew` / `nameservers` | mixed | WHOIS metadata |
| `resolved_ip` | string | Last DNS A-record observed |
| `ip_matches_server` | bool | True if `resolved_ip == server_ip` |
| `domain_type` | enum | Structural classification: `primary` · `subdomain` · `addon` (computed by preflight — distinct from `environment`) |
| `last_checked_at` | string | |
| `created_at` / `updated_at` | string | |
| `owner_email` | string (transient) | Vendor email (computed; never persisted) |

### `IssuedGuestLink` (mint response)

| Field | Type | Description |
|---|---|---|
| `url` | string | One-time login URL — redirect the end-user here. Shown only once |
| `link_type` | enum | `email_dns` (main domain → Email + DNS) · `email` (subdomain → Email only) |
| `domain` | string | The single domain the link manages |
| `expires_at` | string | Deadline by which the link must be **first opened** (24h). The access window is 30 min from first open |
| `token` | string | Raw plaintext token embedded in `url` |

### `Mailbox`

| Field | Type | Description |
|---|---|---|
| `id` | string (ObjectID) | |
| `email` | string | Full address `local@domain` |
| `domain` | string | |
| `quota_mb` | int | Hard limit; 0 = unlimited |
| `used_mb` | float | Live `du` reading (refreshed on stats fetch) |
| `send_limit_per_hour` | int | Postfix submission cap |
| `created_at` / `updated_at` | string | |

### `EmailForwarder`

| Field | Type | Description |
|---|---|---|
| `id` | string (ObjectID) | |
| `source` | string | Address forwarded from (`sales@acme.com`) |
| `destinations` | string[] | One or more recipient addresses |
| `keep_copy` | bool | When true and a mailbox exists at `source`, keep a copy |
| `domain` | string | |
| `created_at` | string | |

### `SSLCertificate`

| Field | Type | Description |
|---|---|---|
| `id` | string (ObjectID) | |
| `domain` | string | Common name |
| `domains` | string[] | All SAN entries |
| `issuer` | string | `Let's Encrypt`, custom CA name |
| `type` | enum | `letsencrypt` · `custom` |
| `issued_at` / `expires_at` | string | |
| `days_remaining` | int | Computed at fetch time |
| `auto_renew` | bool | LE certs auto-renewed by certbot cron |
| `force_ssl` | bool | Mirror of `Domain.force_ssl` |
| `wildcard` | bool | True for `*.<domain>` certs |
| `key_type` | string | `RSA` · `ECDSA` |
| `serial_number` / `fingerprint_sha256` | string | |

### `ProjectService`

| Field | Type | Description |
|---|---|---|
| `id` | string (ObjectID) | |
| `project_id` | string (ObjectID) | Parent project |
| `name` / `slug` | string | |
| `role` | enum | `backend` · `frontend` · `static` |
| `primary_domain` | string | Main domain on this service |
| `alias_domains` | string[] | Legacy merged-alias domains (pre-3.1.117). New domains use `attached_domains`. |
| `attached_domains` | string[] | Durably-attached domains — each its OWN reverse-proxy vhost + cert, linked via `proxy_service_id` (survives edits / SSL reissue / migration). |
| `framework` | string | `nextjs` · `go-fiber` · `static` · … |
| `runtime_version` | string | `node@20.10`, `go@1.22`, `python@3.11`, … |
| `port` | int | Backend services only |
| `status` | enum | `active` · `paused` · `needs_env_vars` · `failed` |
| `git_branch` / `path_prefix` / `build_dir` / `start_cmd` / `build_cmd` / `install_cmd` | string | |
| `created_at` / `updated_at` | string | |

---

### Scope catalogue

| Scope key | Allows | Required permission to grant |
|---|---|---|
| `domain:read` | List + inspect domains | `domain.view` |
| `domain:write` | Create / update / delete domains | `domain.create` |
| `email:read` | List mailboxes + forwarders + stats | `email.view` |
| `email:write` | Create / delete mailboxes + forwarders | `email.manage` |
| `email:webmail` | Mint Roundcube SSO links | `email.view` |
| `ssl:read` | List installed certificates | `ssl.manage` |
| `ssl:write` | Issue Let's Encrypt + toggle Force HTTPS | `ssl.manage` |
| `deploy:read` | List Deploy Software projects + services | `deploy.manage` |
| `deploy:link` | Attach / detach domains on services | `deploy.manage` |
| `webhook:manage` | Create / rotate / delete outbound webhooks | `server.manage` |
| `guest:create` | Mint one-time, browser-locked guest links for one domain | `email.manage` |
| `cloudflare:read` | Read a domain's Cloudflare zone status + nameservers | `domain.view` |
| `cloudflare:write` | Connect a domain to Cloudflare (create/reuse zone) | `domain.manage` |

A token's scopes can never exceed the granting user's permissions. `vendor_owner` and `is_super_admin: true` users may grant any scope.

---

## 4. Authentication endpoints

### POST `/api/v1/auth/login`

Authenticates an email/password pair and returns access + refresh tokens.

**Auth** — none (public).

**Request body**

| Field | Type | Required | Description |
|---|---|:---:|---|
| `email` | string | ✓ | Globally unique panel email |
| `password` | string | ✓ | Plain text; hashed with bcrypt server-side |

**Response — 200 OK**

```json
{
  "success": true,
  "data": {
    "access_token": "eyJhbGciOiJIUzI1NiIs...",
    "refresh_token": "8c0a1f2e3b4d...64hex",
    "expires_in": 900,
    "user": { /* User */ }
  }
}
```

| Output field | Type | Description |
|---|---|---|
| `access_token` | string (JWT, HS256) | 15-minute TTL |
| `refresh_token` | string (64-char hex) | 7-day TTL, single-use |
| `expires_in` | int (seconds) | Lifetime of access_token |
| `user` | [User](#user-auth-response-shape) | Full profile |

**Errors** — `401 UNAUTHORIZED` on bad credentials or suspended account.

### POST `/api/v1/auth/refresh`

Rotates the refresh token and issues a fresh access token.

**Auth** — none.

**Request body**

| Field | Type | Required | Description |
|---|---|:---:|---|
| `refresh_token` | string | ✓ | Most recent refresh token from login or a prior refresh |

**Response** — same shape as `/login`. Old refresh token is invalidated.

---

## 5. Developer · API tokens

All routes available under both `/api/v1/whm/developer/tokens/*` (platform owner) and `/api/v1/cpanel/developer/tokens/*` (vendor). Tenant scoping is enforced server-side: vendors only see their own tokens.

**Auth** — JWT bearer.

### GET `/{whm|cpanel}/developer/tokens/scopes`

Returns the static scope catalogue (used to populate the create-token form).

**Output** — array of:

| Field | Type | Description |
|---|---|---|
| `key` | string | Scope key (e.g. `domain:write`) |
| `label` | string | Human-friendly label |
| `description` | string | What the scope allows |
| `group` | string | `Domain` · `Email` · `SSL` · `Deploy` · `Developer` |

### GET `/{whm|cpanel}/developer/tokens`

Lists tokens visible to the caller, newest first.

**Output** — array of [`ApiToken`](#apitoken). `secret_hash` is never included.

### POST `/{whm|cpanel}/developer/tokens`

Mints a new API token.

**Request body**

| Field | Type | Required | Constraint | Description |
|---|---|:---:|---|---|
| `name` | string | ✓ | 1–80 chars | Operator-facing label |
| `description` | string |  | ≤ 256 chars | Optional |
| `scopes` | string[] | ✓ | ≥ 1, all from [catalogue](#scope-catalogue) | Cannot exceed creator's permissions |
| `expires_in_days` | int |  | 0–3650 | `0` = never (default) |
| `ip_allowlist` | string[] |  | exact IP or `/24` CIDR | Empty = no allowlist |
| `pinned_vendor_id` | string (ObjectID) |  | owner-only | Silently ignored when caller is not `vendor_owner` |

**Response — 201 Created**

```json
{
  "success": true,
  "data": {
    "token": { /* ApiToken */ },
    "plaintext_token": "btz_prod_a1b2c3d4e5f6_<32 hex>"
  }
}
```

| Output field | Type | Description |
|---|---|---|
| `token` | [ApiToken](#apitoken) | Persisted row (no secret material) |
| `plaintext_token` | string | **Shown ONCE** — bcrypt-hashed server-side; never recoverable |

**Errors** — `400 VALIDATION_ERROR` on bad scope key, missing `name`, or asking for a scope the caller can't grant.

### POST `/{whm|cpanel}/developer/tokens/{id}/rotate`

Mints a fresh secret for the same `token_id`. Old secret stops working immediately.

**Path params**

| Param | Type | Description |
|---|---|---|
| `id` | string (ObjectID) | Mongo `_id` of the token row |

**Output** — same as Create. New `plaintext_token` shown once.

### DELETE `/{whm|cpanel}/developer/tokens/{id}`

Marks the token revoked. Row is preserved with `status: revoked` for audit; bearer string is rejected on the next call.

**Output** — `200 OK` with `{success: true, message: "Token revoked"}`.

---

## 6. Developer · Webhooks

Same dual-prefix as tokens. **Auth** — JWT bearer.

### GET `/{whm|cpanel}/developer/webhooks/events`

Returns the static event catalogue.

**Output** — array of:

| Field | Type | Description |
|---|---|---|
| `key` | string | Event key (e.g. `domain.created`) |
| `label` | string | Human-friendly label |
| `description` | string | When the event fires |
| `group` | string | `Domain` · `Email` · `SSL` · `Deploy` |

See [§11 webhook payload contract](#11-webhook-payload-contract) for the full event list and payload shape.

### GET `/{whm|cpanel}/developer/webhooks`

Lists subscribed endpoints visible to the caller.

**Output** — array of [`WebhookEndpoint`](#webhookendpoint). `secret_enc` is never returned.

### POST `/{whm|cpanel}/developer/webhooks`

Creates a new outbound subscription.

**Request body**

| Field | Type | Required | Constraint | Description |
|---|---|:---:|---|---|
| `url` | string | ✓ | starts `http://` or `https://` | Receiver URL (immutable) |
| `description` | string |  | ≤ 256 chars | |
| `events` | string[] | ✓ | ≥ 1, all known event keys | |
| `active` | bool |  | default `true` | When false, dispatches skip this endpoint |

**Response — 201 Created**

```json
{
  "success": true,
  "data": {
    "webhook": { /* WebhookEndpoint */ },
    "plaintext_secret": "8a9b... 64-hex chars"
  }
}
```

| Output field | Type | Description |
|---|---|---|
| `webhook` | [WebhookEndpoint](#webhookendpoint) | Persisted row |
| `plaintext_secret` | string | HMAC-SHA256 signing secret, **shown ONCE**. Used by the receiver to verify `X-Betazen-Signature`. |

### PATCH `/{whm|cpanel}/developer/webhooks/{id}`

Updates `active`, `events`, or `description`. URL is immutable.

**Path params** — `id` (ObjectID).

**Request body** — every field optional:

| Field | Type | Description |
|---|---|---|
| `active` | bool | |
| `events` | string[] | Replaces the existing list |
| `description` | string | |

### POST `/{whm|cpanel}/developer/webhooks/{id}/rotate`

Mints a fresh signing secret. **Output** — same as Create. The old secret stops working immediately, so update the receiver before the next event fires.

### POST `/{whm|cpanel}/developer/webhooks/{id}/test`

Dispatches a synthetic `webhook.test` event so you can verify the receiver path. Body shape:

```json
{
  "event": "webhook.test",
  "fired_at": "2026-05-01T10:07:59Z",
  "data": { "ping": true, "fired_at": "2026-05-01T10:07:59Z" }
}
```

**Output** — `200 OK` with `{success: true, message: "Test event dispatched"}`. Look for the delivery row in the **Delivery Log** (next endpoint).

### DELETE `/{whm|cpanel}/developer/webhooks/{id}`

Permanently removes the endpoint. Existing delivery rows survive for 30 days (Mongo TTL); new dispatches stop immediately.

### GET `/{whm|cpanel}/developer/webhooks/deliveries?limit=N`

Recent delivery attempts across every endpoint visible to the caller.

**Query params**

| Param | Type | Default | Max | Description |
|---|---|---|---|---|
| `limit` | int | 100 | 500 | Number of rows returned |

**Output** — array of [`WebhookDelivery`](#webhookdelivery).

---

## 7. Programmatic API · Domains

**Base** — `/api/v1/external/domains`. **Auth** — API token bearer.

### GET `/api/v1/external/domains`

**Required scope** — `domain:read`.

**Query params**

| Param | Type | Default | Description |
|---|---|---|---|
| `page` | int | 1 | 1-based |
| `limit` | int | 50 | Max 500 |
| `q` | string | "" | Substring match against `domain` and `user` |

**Output** — paginated list of [`Domain`](#domain).

### POST `/api/v1/external/domains`

**Required scope** — `domain:write`.

**Request body**

| Field | Type | Required | Constraint | Description |
|---|---|:---:|---|---|
| `domain` | string | ✓ | valid FQDN | Lowercased server-side |
| `user` | string | ✓ | existing linux username in caller's tenant | Owning account |
| `php_version` | string | ✓ | `7.4` · `8.0` · `8.1` · `8.2` · `8.3` | |
| `environment` | string |  | `prod` · `dev` · `test` · `local` | Deployment tier; defaults to `prod`, unrecognised values normalise to `prod` |
| `server_ip` | string |  | IPv4 | Optional override (defaults to panel server IP) |
| `nameservers` | string[] |  | | Optional |
| `disk_quota_mb` | int |  | ≥ 0 | 0 = unlimited |
| `bandwidth_limit_gb` | int |  | ≥ 0 | 0 = unlimited |
| `max_databases` / `max_email_accounts` / `max_subdomains` / `max_apps` | int |  | ≥ 0 | 0 = unlimited |
| `registrar` | string |  | | Free text (e.g. `Namecheap`) |
| `registered_on` | string |  | RFC3339 or `YYYY-MM-DD` | |
| `expires_on` | string |  | RFC3339 or `YYYY-MM-DD` | |
| `auto_renew` | bool |  | default `false` | |

**Response — 201 Created** — single [`Domain`](#domain).

**Side effects** — creates the home directory (`/home/<user>/domains/<domain>/public_html`), PHP-FPM pool, nginx vhost (HTTP-only initially), DNS zone (when delegated), sets disk quota (if provided). Fires `domain.created` webhook event.

**Errors**

| Status | Code | When |
|---|---|---|
| 400 | `VALIDATION_ERROR` | Bad PHP version, missing user, malformed dates |
| 403 | `FORBIDDEN` | User belongs to another tenant |
| 409 | `CONFLICT` | Domain already exists |

---

## 7a. Programmatic API · Guest links

**Base** — `/api/v1/external/guest-links`. **Auth** — API token bearer.

Mint a **one-time, browser-locked** login URL for a single domain and redirect an end-user to it. The session has **no login** and exposes **no other data**: 30 minutes from first open, locked to the first browser that opens the link.

- **Main domain** (no parent zone in the panel, e.g. `a.com`, `y.c.com`) → Manage **Email** + Manage **DNS** (no zone create/delete, no apex `@` A/AAAA records).
- **Subdomain** (parent zone exists, e.g. `x.a.com`) → Manage **Email** only.

The link type is auto-derived from whether the domain is a subdomain of a panel-managed zone.

### POST `/api/v1/external/guest-links`

**Required scope** — `guest:create`.

**Request body**

| Field | Type | Required | Default | Description |
|---|---|:---:|---|---|
| `domain` | string | ✓ | | The single domain the link manages (must be in the token's tenant) |
| `max_mailboxes` | int |  | `5` | Max mailboxes the guest may create on the domain |
| `default_quota_mb` | int |  | `1024` | Storage quota applied to mailboxes the guest creates |
| `default_send_per_hour` | int |  | `200` | Hourly send limit applied to mailboxes the guest creates |

**Response — 201 Created** (the `url` is shown only once)

```json
{
  "url": "https://panel.example.com/user-panel/m/gst_prod_ab12cd34_…",
  "link_type": "email_dns",
  "domain": "acme-store.com",
  "expires_at": "2026-06-18T12:00:00Z"
}
```

`link_type` is `email_dns` (main domain) or `email` (subdomain). `expires_at` is the deadline by which the link must be **first opened** (24h); the **access window** is 30 minutes from first open.

**Errors**

| Status | Code | When |
|---|---|---|
| 400 | `BAD_REQUEST` | Domain not found, or not in the token's tenant |
| 403 | `FORBIDDEN` | Token lacks `guest:create` |

---

## 7b. Guest browser session

**Base** — `/api/v1/guest`. **Auth** — HttpOnly session **cookies** (`bz_guest_sess` + `bz_guest_bind`), set on redeem. No Authorization header.

> These endpoints are driven by the magic-link page in the browser — **integrators do not call them directly**; they only mint the link (§7a) and redirect the user. Listed for completeness and for end-to-end testing (Postman manages the cookies automatically). Every call is hard-scoped to the one domain on the session — there is no `domain` parameter, and no list/other-domain/zone endpoints exist.

### POST `/api/v1/guest/redeem` (public)

Opens the link. Body `{ "token": "gst_…" }`. The **first** successful call binds the link to this browser (sets the cookies) and starts the 30-minute window; a different browser is rejected, and the same browser may re-open within the window. Returns `{ domain, link_type, window_expires_at }`. Rate-limited per IP.

### Endpoints (all require the session cookies)

| Method | Path | Notes |
|---|---|---|
| `GET` | `/api/v1/guest/session` | `{ domain, link_type, max_mailboxes, default_quota_mb, default_send_per_hour, mailbox_count, window_expires_at }` |
| `POST` | `/api/v1/guest/logout` | Clears the session cookies |
| `GET` | `/api/v1/guest/mailboxes` | List mailboxes for the domain |
| `POST` | `/api/v1/guest/mailboxes` | Create — `{ email, password }`. Domain forced; quota + send-limit from the link; 403 once `max_mailboxes` is hit |
| `PATCH` | `/api/v1/guest/mailboxes/{addr}` | Update `quota_mb` / `send_limit_per_hour` / `password` |
| `DELETE` | `/api/v1/guest/mailboxes/{addr}` | Delete a mailbox |
| `POST` | `/api/v1/guest/mailboxes/{addr}/password` | Reset password — `{ password }` |
| `POST` | `/api/v1/guest/mailboxes/{addr}/webmail-link` | Mint a Roundcube SSO URL — `{ url, token, expires_in }` |
| `GET` | `/api/v1/guest/forwarders` | List forwarders |
| `POST` | `/api/v1/guest/forwarders` | Create — `{ source, destinations[], keep_copy }` (source forced into the domain) |
| `DELETE` | `/api/v1/guest/forwarders/{id}` | Delete (must belong to the domain) |
| `GET` | `/api/v1/guest/dns/records` | List records — **email_dns links only** (403 for subdomain links) |
| `POST` | `/api/v1/guest/dns/records` | Add a record — apex `@` A/AAAA blocked (403) |
| `PATCH` | `/api/v1/guest/dns/records/{id}` | Update a record — apex `@` A/AAAA blocked (403) |
| `DELETE` | `/api/v1/guest/dns/records/{id}` | Delete a record — apex `@` A/AAAA blocked (403) |

`{addr}` accepts the local part (`info`) or the full address; either way it's forced into the session domain.

---

## 8. Programmatic API · SSL

**Base** — `/api/v1/external/ssl/{domain}`. **Auth** — API token bearer.

> **Domain ownership (v3.1.163+).** Every `/ssl/{domain}/*` call is gated on the token's tenant owning `{domain}`. If it doesn't, the request returns **404 `NOT_FOUND`** (`domain not found`) — a deliberate 404, not 403, so a token can't probe which domains exist in other tenants. A platform-owner token is unaffected. This only rejects cross-tenant calls that should never have been made; a token acting on its own domains sees no change.

### POST `/api/v1/external/ssl/{domain}/issue`

Requests a Let's Encrypt certificate.

**Required scope** — `ssl:write`.

**Path params**

| Param | Type | Description |
|---|---|---|
| `domain` | string | FQDN registered on the panel |

**Request body**

| Field | Type | Required | Constraint | Description |
|---|---|:---:|---|---|
| `domain` | string |  | matches path param | Optional; path param wins if mismatched |
| `email` | string |  | valid email | ACME contact (defaults to `admin@<owning-vendor>`) |
| `wildcard` | bool |  | default `false` | When true, requests `*.<domain>` (DNS-01) |
| `additional_domains` | string[] |  | | Extra SANs (e.g. `www.<domain>`) |

**Response — 200 OK** — single [`SSLCertificate`](#sslcertificate).

**Side effects** — runs `certbot`, writes the cert to `/etc/letsencrypt/live/<domain>/`, upgrades the nginx vhost to listen on `:443`, sets `Domain.ssl_active=true`, fires `ssl.issued` webhook.

### POST `/api/v1/external/ssl/{domain}/force`

Toggle the HTTP→HTTPS 301 redirect on the nginx vhost.

**Required scope** — `ssl:write`.

**Request body**

| Field | Type | Default | Description |
|---|---|---|---|
| `enable` | bool | `true` | When omitted, defaults to enabling Force HTTPS |

**Response — 200 OK**

```json
{ "success": true, "message": "Force-SSL toggled", "data": { "domain": "...", "enabled": true } }
```

Fires `ssl.forced` webhook event.

---

## 8b. Programmatic API · Cloudflare

**Base** — `/api/v1/external/cloudflare/{domain}`. **Auth** — API token bearer.

The typical reseller flow: create the domain (`POST /external/domains`), connect it to Cloudflare (`POST .../connect`), then read back the nameservers (`GET .../nameservers`) to hand to the customer for their registrar. All three use the **panel owner's centralized Cloudflare account** — the customer never needs their own Cloudflare login.

> **Prerequisite.** The panel owner must have enabled Cloudflare in WHM → Settings → Cloudflare (a valid account-scoped API token, `enabled: true`). If not, these endpoints return **400** with a clear message.

> **Domain ownership.** Every call is gated on the token's tenant owning `{domain}` (same rule as `/ssl/{domain}/*` and `/email/{domain}/*`). A platform-owner token is unaffected.

### GET `/api/v1/external/cloudflare/{domain}`

Cloudflare status for a domain.

**Required scope** — `cloudflare:read`.

**Response — 200 OK**

```json
{ "success": true, "data": {
  "connected": true,
  "zone_id": "023e105f4ecef8...",
  "status": "active",
  "nameservers": ["dana.ns.cloudflare.com", "rob.ns.cloudflare.com"]
} }
```

`connected: false` (with no other fields) when the domain has no Cloudflare zone yet.

### GET `/api/v1/external/cloudflare/{domain}/nameservers`

The value to give the customer for their registrar. **This is the endpoint to call after adding a domain.**

**Required scope** — `cloudflare:read`.

**Response — 200 OK**

```json
{ "success": true, "data": {
  "domain": "example.com",
  "zone_id": "023e105f4ecef8...",
  "status": "pending",
  "nameservers": ["dana.ns.cloudflare.com", "rob.ns.cloudflare.com"]
} }
```

**Errors** — `404 NOT_FOUND` when the domain hasn't been connected yet (call `.../connect` first).

### GET `/api/v1/external/cloudflare/{domain}/nameserver-status`

Live delegation check — a real DNS NS lookup vs Cloudflare's assigned nameservers + zone status. Poll this to know when the customer has finished pointing their registrar at Cloudflare.

**Required scope** — `cloudflare:read`.

**Response — 200 OK**

```json
{ "success": true, "data": {
  "domain": "example.com",
  "connected": true,
  "zone_status": "pending",
  "cf_nameservers": ["dana.ns.cloudflare.com", "rob.ns.cloudflare.com"],
  "current_nameservers": ["ns1.oldhost.com", "ns2.oldhost.com"],
  "delegated": false,
  "state": "nameserver_update_required"
} }
```

`state` ∈ `not_connected` · `nameserver_update_required` · `pending_activation` · `active` · `paused`.

> **Auto-verification (v3.1.195+).** The panel also runs this exact check in the background for every connected zone every ~20 minutes and stores the result (`ns_state`, `ns_checked_at`, refreshed `cf_status`) on the zone — so a domain flips to `active` on its own once the registrar cutover propagates. This endpoint remains an on-demand live check for integrators who want the status *right now* rather than waiting for the next sweep.

### POST `/api/v1/external/cloudflare/{domain}/connect`

Find-or-create the domain's Cloudflare zone (never duplicates) and return the assigned nameservers.

**Required scope** — `cloudflare:write`.

**Response — 200 OK**

```json
{ "success": true, "data": {
  "domain": "example.com",
  "zone_id": "023e105f4ecef8...",
  "status": "pending",
  "nameservers": ["dana.ns.cloudflare.com", "rob.ns.cloudflare.com"],
  "created": true
} }
```

`created: true` when a new zone was made; `false` when an existing zone was reused. **Idempotent** — calling twice never creates a duplicate zone.

**Errors** — `400` when Cloudflare isn't enabled on the panel; `403` when the token lacks `cloudflare:write` or doesn't own the domain.

---

## 9. Programmatic API · Email

**Base** — `/api/v1/external/email/{domain}`. **Auth** — API token bearer.

The `:domain` path scope means every per-domain request is namespaced. Mailbox addresses can be passed as the local part (e.g. `john`) — the panel automatically suffixes `@<domain>` when needed.

> **Domain ownership (v3.1.163+).** Every `/email/{domain}/*` call is gated on the token's tenant owning `{domain}`; a non-owned domain returns **404 `NOT_FOUND`** (`domain not found`). In addition, a mailbox path passed as a *full address* (e.g. `.../mailboxes/ceo@other.com/...`) must be under `{domain}` or it is rejected, and `DELETE /forwarders/{id}` only removes a forwarder whose source is under `{domain}`. These close a cross-tenant access gap where the token merely had to *hold* the scope; they do not affect a token operating on its own domains.

### GET `/api/v1/external/email/{domain}/mailboxes`

**Required scope** — `email:read`.

**Query params** — `page`, `limit` (same defaults as domains).

**Output** — paginated list of [`Mailbox`](#mailbox).

### POST `/api/v1/external/email/{domain}/mailboxes`

**Required scope** — `email:write`.

**Request body**

| Field | Type | Required | Constraint | Description |
|---|---|:---:|---|---|
| `email` | string | ✓ | valid local part or full address | If no `@`, `@<domain>` is appended |
| `password` | string | ✓ | ≥ 8 chars | Hashed to SHA512-CRYPT for Dovecot |
| `domain` | string |  | matches path param | Auto-filled |
| `quota_mb` | int |  | default 1024 | 0 = unlimited |
| `send_limit_per_hour` | int |  | default 0 | Postfix submission cap |

**Response — 201 Created** — single [`Mailbox`](#mailbox).

**Side effects** — provisions maildir at `/home/<user>/mail/<domain>/<local>/`, writes Dovecot user line, registers Postfix virtual-mailbox map, fires `email.mailbox.created`.

### GET `/api/v1/external/email/{domain}/mailboxes/{addr}/stats`

**Required scope** — `email:read`.

**Path params**

| Param | Type | Description |
|---|---|---|
| `domain` | string | |
| `addr` | string | Local part (`john`) or full address |

**Output**

| Field | Type | Description |
|---|---|---|
| `email` | string | Full address |
| `quota_mb` | int | |
| `used_mb` | float | Live `du -sm` reading |
| `send_limit_per_hour` | int | |
| `created_at` / `updated_at` | string | |

**No PII / message content is exposed** — for inbox access use the webmail link below.

### POST `/api/v1/external/email/{domain}/mailboxes/{addr}/password`

**Required scope** — `email:password`.

Reset a mailbox password. Send `{ "password": "..." }` to set a specific one, **or send `{}` (omit `password`)** to have the panel generate a strong password and return it (v3.1.180+) — handy for provisioning flows that never store a plaintext the caller chose.

**Body**

| Field | Type | Description |
|---|---|---|
| `password` | string | Optional. Omit/blank → a strong password is generated and echoed back in the response. |

**Output**

| Field | Type | Description |
|---|---|---|
| `email` | string | Full address |
| `password` | string | Present **only** when the panel generated one (blank input); store it, it is not retrievable later |

### POST `/api/v1/external/email/{domain}/mailboxes/{addr}/webmail-link`

**Required scope** — `email:webmail`.

Mints a short-lived HMAC-signed Roundcube SSO token.

**Output**

| Field | Type | Description |
|---|---|---|
| `token` | string | Embed in URL fragment or pass to `/webmail/sso.php` |
| `url` | string | Ready-to-use SSO URL: `/webmail/sso.php?token=<token>` |

Tokens expire after 60 seconds; redeeming logs the user in once and is single-use.

### DELETE `/api/v1/external/email/{domain}/mailboxes/{addr}`

**Required scope** — `email:write`. Tears down maildir, removes Dovecot user, removes Postfix entry, fires `email.mailbox.deleted`.

### GET `/api/v1/external/email/{domain}/forwarders`

**Required scope** — `email:read`.

**Output** — array of [`EmailForwarder`](#emailforwarder).

### POST `/api/v1/external/email/{domain}/forwarders`

**Required scope** — `email:write`.

**Request body**

| Field | Type | Required | Description |
|---|---|:---:|---|
| `source` | string | ✓ | Local part (`sales`) or full address |
| `destinations` | string[] | ✓ | One or more recipient emails |
| `keep_copy` | bool |  | When true and a mailbox exists at `source`, retain a local copy |

**Response — 201 Created** — single [`EmailForwarder`](#emailforwarder). Fires `email.forwarder.created`.

### DELETE `/api/v1/external/email/{domain}/forwarders/{id}`

**Required scope** — `email:write`. Path param `id` is the forwarder ObjectID returned on Create or List.

---

## 10. Programmatic API · Deploy Software

**Base** — `/api/v1/external/deploy/projects/{project_id}/services/{service_id}`. **Auth** — API token bearer.

### POST `/api/v1/external/deploy/projects/{project_id}/services/{service_id}/link-domain`

**Durably attach** a registered domain to the named service (v3.1.117+). The domain gets its OWN nginx reverse-proxy vhost and its own Let's Encrypt certificate, and a `proxy_service_id` link is stamped on the Domain record — so a later service edit, SSL reissue, or server migration can never drop it. The domain must already be registered under Domains and owned by the caller's tenant. (Before 3.1.117 this merged the domain into the primary's shared SAN cert — that fragile path, which silently dropped domains on edits/migration, is gone.)

> **Fixed in v3.1.163.** A project whose `tenant_id` was never stamped (provisioned before tenant stamping, or via a WHM path that didn't assign one) previously returned **403 `project belongs to a different tenant`** for *every* token-based link — even the rightful owner's — leaving an otherwise-verified vendor subdomain stuck at `deploy: failed`. The link now succeeds and heals the project's tenant stamp **when the caller demonstrably owns the project** (they are its `owner_user_id`, or its linux `user` resolves into the caller's tenant). A project genuinely owned by another tenant, or owner-only, still returns 403. No request change is needed — calls that used to fail now go through.

**Required scope** — `deploy:link`.

**Path params**

| Param | Type | Description |
|---|---|---|
| `project_id` | string (ObjectID) | Project _id |
| `service_id` | string (ObjectID) | ProjectService _id |

**Request body**

| Field | Type | Required | Description |
|---|---|:---:|---|
| `domain` | string | ✓ | A registered domain owned by the caller, to attach |

**Response — 200 OK** — updated [`ProjectService`](#projectservice) (includes `attached_domains`; `ssl_warning` is set if DNS for the domain isn't pointing at this server yet, in which case the domain is attached + routing on HTTP but its cert will issue on a later reissue). Fires `deploy.linked`.

**Errors**

| Status | When |
|---|---|
| 400 | Domain already the primary, already attached, the primary of another service, or not a registered domain |
| 404 | Service or project not found / not owned by caller |

### DELETE `/api/v1/external/deploy/projects/{project_id}/services/{service_id}/link-domain/{domain}`

Detach the domain. Clears its `proxy_service_id` link and restores the domain's own base vhost (PHP-FPM, or a placeholder if it isn't a registered Domain). The cert is **not** revoked — this preserves uptime and avoids burning Let's Encrypt rate-limit slots.

**Required scope** — `deploy:link`. Fires `deploy.unlinked`.

---

## 11. Webhook payload contract

When an event fires, the panel POSTs to every active subscription whose `events` array includes the event key.

### Headers

| Header | Example | Purpose |
|---|---|---|
| `Content-Type` | `application/json` | |
| `User-Agent` | `BetazenWebhook/1.0` | |
| `X-Betazen-Event` | `domain.created` | Event key |
| `X-Betazen-Delivery` | `01HXX...` (ObjectID hex) | Unique per attempt — use for dedup |
| `X-Betazen-Timestamp` | `1735000000` | Unix seconds; reject deliveries older than 5 min |
| `X-Betazen-Signature` | `sha256=<hex>` | HMAC-SHA256 over `<timestamp>.<raw_body>` |

### Body

```json
{
  "event": "domain.created",
  "fired_at": "2026-05-01T10:07:59Z",
  "data": { /* event-specific payload */ }
}
```

### Per-event `data` payloads

| Event | `data` fields |
|---|---|
| `domain.created` | `id`, `domain`, `user`, `php_version` |
| `domain.deleted` | `id`, `domain`, `user` |
| `ssl.issued` | `id`, `domain`, `issuer`, `type`, `expires_at`, `wildcard` |
| `ssl.renewed` | `id`, `domain`, `issuer`, `expires_at` |
| `ssl.forced` | `domain`, `enabled` |
| `email.mailbox.created` | `id`, `email`, `domain`, `quota_mb` |
| `email.mailbox.deleted` | `id`, `email`, `domain` |
| `email.forwarder.created` | `id`, `source`, `destinations`, `domain` |
| `deploy.linked` | `project_id`, `service_id`, `primary_domain`, `linked_domain` |
| `deploy.unlinked` | `project_id`, `service_id`, `primary_domain`, `unlinked_domain` |
| `deploy.completed` | `project_id`, `service_id`, `commit_sha`, `duration_ms` |
| `deploy.failed` | `project_id`, `service_id`, `commit_sha`, `error` |
| `webhook.test` | `ping: true`, `fired_at` |

### Verifying signatures (Node.js example)

```js
const crypto = require("crypto");

function verifyBetazenSignature(req, signingSecret) {
  const ts = req.header("X-Betazen-Timestamp");
  const sig = req.header("X-Betazen-Signature"); // sha256=<hex>
  if (!ts || !sig) return false;

  // Reject old deliveries (replay protection)
  const ageSeconds = Math.floor(Date.now() / 1000) - parseInt(ts, 10);
  if (ageSeconds < 0 || ageSeconds > 300) return false;

  const expected = "sha256=" + crypto
    .createHmac("sha256", signingSecret)
    .update(ts + "." + req.rawBody)
    .digest("hex");

  // Constant-time comparison
  return crypto.timingSafeEqual(Buffer.from(sig), Buffer.from(expected));
}
```

### Delivery + retry behaviour

- Per-attempt timeout: **10 seconds**.
- Success window: HTTP `2xx` response code.
- Retries: up to **3 attempts** total (initial + 2 retries).
- Backoff: `1m` → `5m` → `30m`.
- Each attempt persists a [`WebhookDelivery`](#webhookdelivery) row visible in the Delivery Log for 30 days.

---

## 12. Error reference

Every error response uses the envelope:

```json
{ "success": false, "error": { "code": "...", "message": "...", "details": null } }
```

| HTTP | `error.code` | When |
|---|---|---|
| 400 | `VALIDATION_ERROR` | Bad input — missing required field, malformed value, unknown enum |
| 401 | `UNAUTHORIZED` | Missing / expired / invalid bearer token; account suspended |
| 403 | `FORBIDDEN` | Auth OK but caller lacks the required permission / scope |
| 404 | `NOT_FOUND` | Resource doesn't exist or isn't visible to caller |
| 409 | `CONFLICT` | Duplicate (e.g. domain already exists, mailbox already exists) |
| 422 | `AGENT_ERROR` | Local agent (nginx, certbot, dovecot) returned a non-zero exit |
| 429 | `RATE_LIMITED` | Per-IP or per-token rate limit hit; honour `Retry-After` header |
| 500 | `INTERNAL_ERROR` | Unhandled server error — should be surfaced via logs |

### Authorization Header missing on `/api/v1/external/*`

| Status | Code | Message |
|---|---|---|
| 401 | `UNAUTHORIZED` | `Missing authorization header` |
| 401 | `UNAUTHORIZED` | `Invalid authorization format` |
| 401 | `UNAUTHORIZED` | `API token expected` (when bearer doesn't start with `btz_`) |
| 401 | `UNAUTHORIZED` | `token revoked` / `token expired` / `token temporarily locked after repeated failed auth` |
| 401 | `UNAUTHORIZED` | `IP not allowed for this token` |
| 403 | `FORBIDDEN` | `Token missing scope: <scope:key>` |

The panel locks a token for 15 minutes after **10 wrong-secret attempts within ~1 minute**. The lock auto-clears on first successful auth or after the timer expires.

---

*Generated for Betazen Server Panel v3.1.103. For the canonical model definitions see `backend/internal/models/`. For the canonical scope catalogue see `backend/internal/models/api_token.go` (`AllAPITokenScopes`). For the canonical event catalogue see `backend/internal/models/webhook_endpoint.go` (`AllWebhookEvents`).*
