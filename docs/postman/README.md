# Betazen Server Panel — Postman Collection

> **See also:** the rendered HTML reference at [`docs/api/index.html`](../api/index.html) and the
> webhook signature recipes at [`docs/api/webhooks.html`](../api/webhooks.html). Both are served
> live from the panel itself at **`/docs/api/`** and **`/docs/api/webhooks.html`** when the
> server is running. The OpenAPI 3.1 spec at [`docs/api/openapi.yaml`](../api/openapi.yaml)
> is the machine-readable companion.

A complete Postman v2.1 collection covering every API surface in the Betazen Server Panel:

- **Authentication** — JWT login + refresh for both the Platform Owner and Vendor surfaces.
- **Developer · WHM** — manage API tokens and outbound webhooks at the super-admin scope. Tokens may be **pinned to a vendor** so the bearer string is immutably scoped to that tenant.
- **Developer · Vendor** — same surface, scoped to the calling vendor's tenant.
- **Programmatic API** (`/api/v1/external/*`) — token-authenticated endpoints for domain provisioning, SSL, email and Deploy Software linking.

Files in this folder:

- [`Betazen-Server-Panel.postman_collection.json`](./Betazen-Server-Panel.postman_collection.json) — runnable Postman collection (36 requests, 25 variables).
- [`API-Reference.md`](./API-Reference.md) — full input / output variable reference. Every endpoint, every field, every type, every constraint, every error code. Pair this with the Postman collection.

---

## Quick start

### 1. Import

In Postman: **File → Import → Upload Files** → select the JSON file. The collection appears in the left sidebar with all folders pre-populated.

### 2. Set your environment variables

Right-click the collection → **Edit → Variables** tab. Update at minimum:

| Variable | What to put | Example |
|---|---|---|
| `base_url` | Panel URL, no trailing slash | `https://panel.acme.com` |
| `owner_email` | Platform owner login | `owner@acme.com` |
| `owner_password` | Platform owner password | *(your real password)* |
| `vendor_email` | A vendor login (optional, for vendor folder) | `acme-customer@example.com` |
| `vendor_password` | Vendor password | *(real password)* |
| `vendor_user_id` | ObjectID of a vendor — used as `pinned_vendor_id` | `65f0a1b2c3d4e5f601020304` |

You can find `vendor_user_id` in WHM → Vendors (the URL on each row), or via `GET /api/v1/whm/admin/vendors`.

### 3. Run the chain

The test scripts auto-capture every secret + ID into collection variables, so you can run requests in order without manual paste:

```
Authentication ▸ Login (Platform Owner)
   → captures access_token, refresh_token, owner_user_id

Developer · WHM ▸ Create API Token (pinned to vendor)
   → captures plaintext_token (into api_token), token_id

Programmatic API · Domains ▸ Create Domain
   → captures domain_id, tenant_domain

Programmatic API · SSL ▸ Issue Let's Encrypt Certificate

Programmatic API · Email ▸ Create Mailbox
   → captures mailbox_address, mailbox_local_part, mailbox_id

Programmatic API · Email ▸ Generate Webmail Auto-login Link
   → opens Roundcube with no re-login required

Programmatic API · Deploy Software ▸ Link Domain to Service
```

The **Programmatic API** folder uses `Authorization: Bearer {{api_token}}` collection-level — Postman fills it from the captured plaintext token.

---

## What's where

### Authentication (3 requests)

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/v1/auth/login` | Owner login (captures `access_token`) |
| `POST` | `/api/v1/auth/login` | Vendor login (captures `vendor_access_token`) |
| `POST` | `/api/v1/auth/refresh` | Rotate the refresh token |

### Developer · WHM (13 requests)

API tokens:

| Method | Path |
|---|---|
| `GET` | `/api/v1/whm/developer/tokens/scopes` |
| `GET` | `/api/v1/whm/developer/tokens` |
| `POST` | `/api/v1/whm/developer/tokens` |
| `POST` | `/api/v1/whm/developer/tokens/{id}/rotate` |
| `DELETE` | `/api/v1/whm/developer/tokens/{id}` |

Webhooks:

| Method | Path |
|---|---|
| `GET` | `/api/v1/whm/developer/webhooks/events` |
| `GET` | `/api/v1/whm/developer/webhooks` |
| `POST` | `/api/v1/whm/developer/webhooks` |
| `PATCH` | `/api/v1/whm/developer/webhooks/{id}` |
| `POST` | `/api/v1/whm/developer/webhooks/{id}/rotate` |
| `POST` | `/api/v1/whm/developer/webhooks/{id}/test` |
| `DELETE` | `/api/v1/whm/developer/webhooks/{id}` |
| `GET` | `/api/v1/whm/developer/webhooks/deliveries?limit=200` |

### Developer · Vendor (5 requests)

Same shape, `/api/v1/cpanel/developer/*` prefix. Vendors only see their own tenant's tokens / webhooks.

### Programmatic API (15 requests)

All routes under `/api/v1/external/*` authenticate with `Bearer {{api_token}}` (the plaintext from token creation).

| Folder | Method | Path |
|---|---|---|
| Domains | `GET` | `/api/v1/external/domains` |
| Domains | `POST` | `/api/v1/external/domains` |
| SSL | `POST` | `/api/v1/external/ssl/{domain}/issue` |
| SSL | `POST` | `/api/v1/external/ssl/{domain}/force` (on/off) |
| Email | `GET` | `/api/v1/external/email/{domain}/mailboxes` |
| Email | `POST` | `/api/v1/external/email/{domain}/mailboxes` |
| Email | `GET` | `/api/v1/external/email/{domain}/mailboxes/{addr}/stats` |
| Email | `POST` | `/api/v1/external/email/{domain}/mailboxes/{addr}/webmail-link` |
| Email | `DELETE` | `/api/v1/external/email/{domain}/mailboxes/{addr}` |
| Email | `GET` | `/api/v1/external/email/{domain}/forwarders` |
| Email | `POST` | `/api/v1/external/email/{domain}/forwarders` |
| Email | `DELETE` | `/api/v1/external/email/{domain}/forwarders/{id}` |
| Deploy | `POST` | `/api/v1/external/deploy/projects/{id}/services/{svc}/link-domain` |
| Deploy | `DELETE` | `/api/v1/external/deploy/projects/{id}/services/{svc}/link-domain/{domain}` |

---

## Token format reference

Tokens look like:

```
btz_prod_a1b2c3d4e5f6_<32-byte hex secret>
└─┬─┘ └─┬─┘ └─────┬────────┘ └────────┬─────────┘
 prefix env  public token id        secret half (bcrypt-hashed server-side)
```

- **Public ID** is stored visible — used to look up the row.
- **Secret half** is bcrypt-hashed; only shown plaintext on creation / rotation.
- **`pinned_vendor_id`** (owner-issued only) immutably scopes every call to that vendor's tenant. Vendors cannot set this field.
- **Scopes** are checked per-route. Token scopes can never exceed the creator's RBAC permissions.

## Webhook signature reference

Every outbound delivery includes:

```
X-Betazen-Event:     domain.created
X-Betazen-Delivery:  01HXX...
X-Betazen-Timestamp: 1735000000
X-Betazen-Signature: sha256=<hex>
```

Compute `HMAC-SHA256(<signing_secret>, <timestamp>.<raw_body>)` and constant-time compare against the signature header. Reject deliveries older than 5 minutes (replay protection).

## Available scopes

| Scope | Purpose |
|---|---|
| `domain:read` | List + inspect domains |
| `domain:write` | Create / update / delete domains |
| `email:read` | List mailboxes, forwarders, mailbox stats |
| `email:write` | Create / delete mailboxes + forwarders |
| `email:webmail` | Generate Roundcube SSO links |
| `ssl:read` | List installed certificates |
| `ssl:write` | Issue Let's Encrypt + toggle Force HTTPS |
| `deploy:read` | List Deploy Software projects + services |
| `deploy:link` | Link / unlink domains to project services |
| `webhook:manage` | Create / rotate / delete outbound webhooks |

## Available webhook events

| Event | When it fires |
|---|---|
| `domain.created` | After a new domain is added |
| `domain.deleted` | After a domain is removed |
| `ssl.issued` | After Let's Encrypt issues a cert |
| `ssl.renewed` | On automatic or manual renewal |
| `ssl.forced` | When Force-SSL is toggled |
| `email.mailbox.created` | After a mailbox is provisioned |
| `email.mailbox.deleted` | After a mailbox is deleted |
| `email.forwarder.created` | After a forwarder is added |
| `deploy.linked` | When a domain is attached to a service |
| `deploy.unlinked` | When a domain is detached from a service |
| `deploy.completed` | When a Deploy Software service finishes deploying |
| `deploy.failed` | When a Deploy Software service fails to deploy |

## Rate limiting

Default per-token: **600 reads/min, 60 writes/min**. Returns `429` with `Retry-After` header. Configurable per token via the IP allowlist + scope picker.
