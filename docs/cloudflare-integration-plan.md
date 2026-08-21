# Cloudflare Integration — Phase 1 Audit & Master Execution Plan

**Status:** ALL INCREMENTS (0–5) COMPLETE & VERIFIED. Backend builds linux/amd64 + `go vet` clean; `go test ./pkg/cloudflare/` and the new services tests pass; WHM full production build (`tsc && vite build`) is clean. Delivered: encrypted centralized config + settings UI (0); read-only REST client + compare view (1); write layer — provider interface, connect zone, guarded record CRUD (2); background sync-job engine + durable live-progress/event-stream panel + boot recovery (3); `ip_history` at the `ReassignServerIP` chokepoint + web-origin-only Cloudflare repoint with mail protection, covering migration + a symmetric rollback path (4); unit tests + security audit (5). All additive; no existing behaviour changed; token AES-GCM encrypted + never echoed; destructive ops gated. NOT executed against a live Cloudflare account / MongoDB (no such environment here) — verification is build + vet + unit tests + typecheck/bundle.
**Date:** 2026-08-18
**Author:** Claude (orchestrated, 5 parallel read-only audit agents)
**Scope of this document:** the complete Phase-1 audit (maps + risk report) and the proposed
phased, safety-gated integration plan. **No production code has been changed to produce this.**

> Ground rule carried from the request: this is an existing production system. Every change
> below is **additive and backward-compatible**. Nothing existing is deleted, dropped, reset,
> or rewritten for style. Any destructive step is gated behind explicit human approval.

---

## 0. Executive summary

- **There is no existing Cloudflare integration.** The only `cloudflare` strings in the code are
  test fixtures, help copy, and a registrar-input placeholder. This is a greenfield feature layered
  onto an existing system — not a refactor of one.
- **DNS today is PowerDNS**, driven through `pdnsutil`/`pdns_control` shell calls. There is **no
  provider abstraction**; PowerDNS is hardwired and even leaks above the agent layer into mail setup.
- **The hardest technical constraint:** PowerDNS writes by *replacing an entire rrset atomically* and
  addresses records by `(type,name,value)` — there is **no external record ID stored**. Cloudflare
  mutates *individual records by ID*. A Cloudflare provider therefore needs its own diff-based sync
  path plus new `cf_zone_id` / `cf_record_id` fields; it cannot be dropped behind the existing
  reconcile loop.
- **There is no stable server identity.** A "server" is only its current primary IPv4, re-derived live
  from `hostname -I`. `server_id`/`ip_history` is purely additive greenfield — safe *if* we never
  repurpose an existing IP field (the migration sweep depends on the literal current IP being stored).
- **Mail and web are co-located on one IP** with **no data-level marker** separating mail DNS from web
  DNS. SPF embeds the IP literally (`v=spf1 ip4:<ip>`), and the `mail` A record shares the web IP. A
  Cloudflare "web-IP changed" sync must exclude mail records or it silently breaks mail.
- **Most of the "live progress / persistent job / recovery" machinery already has a working
  precedent:** the SSL bulk-issue job (Mongo-doc-as-source-of-truth + 1.5s polling + boot recovery)
  and the transfer engine (`TransferJob` doc + `ResumeRunningTransfers` on boot). We extend these
  patterns; we do not invent an "AI-agent-watching" dashboard.
- **Secret-at-rest is solved:** AES-256-GCM (`pkg/crypto/aesgcm.go`) keyed off `APP_ENCRYPTION_KEY`,
  already used for the SMTP password and GitHub PATs. The Cloudflare token copies that pattern exactly.
- **New Cloudflare fields on domain/DNS rows round-trip through the DR backup for free** — it's a
  whole-DB `mongodump`/`mongorestore` with no field allowlist.

---

## 1. Architecture map

```
Single Go Fiber API process (backend/cmd/server)
├── Handlers  (internal/handlers/*)      thin HTTP → Service pass-through
├── Services  (internal/services/*)      business logic + Mongo + shell-outs
├── Middleware(internal/middleware/*)    Auth (JWT) → InjectScope → RBAC → Audit
├── Models    (internal/models/*)        BSON structs
├── agent pkg (internal/agent/*)         SSH/pdnsutil/system shell helpers
└── Mongo (serverpanel DB)               single source of truth for panel state
        │
        ├── PowerDNS (pdnsutil / pdns_control, local)   ← authoritative DNS today
        ├── Postfix/Dovecot/OpenDKIM (local, same box)  ← mail, co-located
        └── SSH → source/dest servers                    ← server-to-server transfer

Two SPAs: /whm/* (owner) and /user-panel/* (tenants). WHM axios base = /api/v1/whm.

Standalone agent daemon (backend/cmd/agent) — near-empty, X-Agent-Key auth,
optional ONE-WAY TLS. NOT mTLS (CLAUDE.md says mTLS:8443 — doc/code mismatch).
The real migration path does NOT use this daemon; it SSHes directly.
```

**Reusable building blocks identified (do not reinvent):**

| Need | Existing pattern to reuse | Location |
|---|---|---|
| Encrypted secret at rest | `EncryptGCM`/`DecryptGCM` + `MaskToken`, key from `APP_ENCRYPTION_KEY` | [aesgcm.go](backend/pkg/crypto/aesgcm.go) |
| Encrypted-secret settings form (never echo) | SMTP password on `server_config` singleton | [panel_mail_service.go](backend/internal/services/panel_mail_service.go) |
| Persistent job + progress + cancel + boot-recover | SSL bulk-issue job | [ssl_bulk_job_service.go](backend/internal/services/ssl_bulk_job_service.go) |
| Long-running resumable job (survives restart) | `TransferJob` + `ResumeRunningTransfers` | [transfer_service.go](backend/internal/services/transfer_service.go) |
| Live progress UI (poll + progress modal + per-row table) | SSL page bulk modal | [SSLPage.tsx](frontend/apps/whm/src/pages/SSLPage.tsx) |
| Settings singleton | `server_config` key/value upsert (`ui_settings`) | [config_service.go](backend/internal/services/config_service.go) |
| Auto-audit every mutation | `AuditLogger` middleware | [audit.go](backend/internal/middleware/audit.go) |
| Event/email notify | `NotifierService`, `EmitEvent` webhook bus | [notifier_service.go](backend/internal/services/notifier_service.go), [events.go](backend/internal/services/events.go) |
| Destructive-op confirm (frontend) | `confirmAction`/`ConfirmHost` | `packages/ui` |
| IP rewrite chokepoint | `ReassignServerIP(oldIP,newIP)` | [config_service.go](backend/internal/services/config_service.go) |
| Idempotent boot migration/backfill | `BackfillTenantIDs` pattern | [migration.go](backend/internal/services/migration.go) |

---

## 2. Database map (collections touched by this work)

| Collection | Role today | Cloudflare-relevant change (all additive) |
|---|---|---|
| `dns_zones` | one doc per apex zone; `ServerIP`, `Nameservers`, `Status` | + `provider`, `cf_zone_id`, `cf_account_id`, `cf_status`, `cf_nameservers[]`, `sync_state`, `last_sync_at`, `last_error` |
| `dns_records` | records; addressed by `(type,name,value)`, **no external id** | + `cf_record_id`, `proxied *bool`, `managed_by`/`source` (`web`/`mail`/`user`) |
| `domains` | domain rows; has `Nameservers`, `ResolvedIP`; carries an untyped `server_ip` write | + optional `dns_provider` selector; + nullable `server_id` (later) |
| `server_config` | key/value singletons (`nginx`,`php`,`ui_settings`,`panel_mail`…) | + new singleton `key:"cloudflare"` = `{account_id, api_token_cipher, enabled, default_provider, connection_status, last_test_at, last_sync_at, last_error}` |
| **`servers`** (new) | — none — | new collection: `{server_id(uuid), hostname, current_ip, previous_ip, ip_history[], created_at, updated_at}`, seeded idempotently at boot |
| **`cloudflare_sync_jobs`** (new) | — none — | job doc mirroring `SSLBulkJob` (status/progress/items/cancel) for bulk sync + per-domain sync |
| `transfer_jobs` | migration job state; resumable on boot | + optional nullable `source_server_id`/`dest_server_id`; + optional Cloudflare-sync sub-step |
| `audit_logs` | auto-populated by middleware | new routes audited automatically |

**Round-trip safety:** the authoritative DR backup (`scripts/bzpanel-backup.sh` → `mongodump --gzip
--archive` of the whole DB; restore via `mongorestore --drop`) serializes **every field of every
document including `_id`** — so all new fields above survive backup/restore with **zero** code changes
and **no allowlist** to maintain. (The per-account backup captures Mongo metadata as ExtJSON but does
not re-import it — not a regression, just not a restore vector for those fields.)

---

## 3. Server / IP map

- **No stable identity exists.** Grep for `server_id|ServerID|server_uuid|node_id` = zero matches.
  Identity = current primary IPv4, resolved live: `config.getEnvOrDetectIP("SERVER_IP")`
  ([config.go:146,189](backend/internal/config/config.go#L146)), `hostname -I` fallbacks throughout.
- **IP is stored in exactly these panel-owned places** (all keep meaning "current IPv4" — do not touch):
  - `dns_zones.server_ip`, `domains.server_ip` (untyped write), `server_config{key:"server_ip"}` mirror,
    `.env SERVER_IP`, plus DNS record **values** (A/AAAA/SPF — unavoidable, that's their purpose).
- **Single IP-change chokepoint:** `ConfigService.ReassignServerIP(oldIP,newIP)`
  ([config_service.go:1509](backend/internal/services/config_service.go#L1509)) rewrites every
  IP-bearing surface (pdns A/AAAA/SPF, Mongo mirrors, `.env`, panel vhost, ftp). This is the one place
  to append `ip_history` and (later) trigger the Cloudflare web-record update.
- **Additive identity plan:** new `servers` collection + UUID, seeded once at boot via the
  `BackfillTenantIDs` idempotent-migration pattern; `ip_history` appended inside `ReassignServerIP` and
  the two transfer call sites; nullable `ServerID` refs added to `Domain`/`DNSZone`/`TransferJob` only
  where useful, never filtered on in existing queries.

---

## 4. Domain / DNS map

- **Provider seam:** the de-facto provider surface is the 8 `pdnsutil` wrappers in
  [agent/dns.go](backend/internal/agent/dns.go) (`CreateDNSZone`, `DeleteDNSZone`, `ReplaceDNSRecordSet`,
  `DeleteDNSRecord`, `ListAllZones`, `ListZoneRecords`, …) — but PowerDNS **also leaks above** the agent
  layer into `GetOrCreateZone`, `setupMailServer`, `SetupSubdomainMail`, `ReconcileZone` in
  [dns_service.go](backend/internal/services/dns_service.go). A real `DNSProvider` interface must cover
  both.
- **Source of truth is split:** zones → PowerDNS is read-authority; record **writes** → Mongo is source
  of truth, projected to pdns via `reconcileRRSet` → `ReplaceDNSRecordSet` (whole-rrset replace); record
  **reads** → pdns is authority with heal-on-read into Mongo.
- **Domain lifecycle:**
  - *Create (primary):* create zone → seed `@ A`(serverIP), `www` CNAME, `cname` CNAME, `NS` per ns →
    `setupMailServer` adds `mail A`, `@ MX`, SPF, DKIM, DMARC.
  - *Create (subdomain):* records added into the **parent** zone; no separate zone row.
  - *Edit:* DNS-neutral (resource limits + environment only).
  - *Delete:* subdomain → delete exact-name records in parent; primary → `pdnsutil delete-zone` + delete
    Mongo `dns_records`/`dns_zones`.
- **Supported record types (validation enum):** A AAAA AFSDB ALIAS CAA CNAME DMARC DNAME DS HINFO HTTPS
  LOC MX NAPTR NS PTR RP SOA SPF SRV TXT.
- **Key additive gap:** `DNSRecord` has no external record id → **`cf_record_id` is required** for
  Cloudflare update/delete to be addressable.

---

## 5. Mail map

- **Mail is co-located on the web IP.** `EmailServerConfig` and `MailSuiteDeployment` have **no IP
  field**; mail hostname is always derived (`mail.<domain>`); mail IP = the panel's own server IP.
- **Mail DNS records (generated in [dns_service.go](backend/internal/services/dns_service.go)):**
  `mail` A → serverIP; `@` MX `10 mail.<d>.`; SPF `v=spf1 ip4:<serverIP> ~all`; DKIM TXT
  `mail._domainkey`; DMARC TXT `_dmarc`. Subdomains get MX/SPF/DMARC (+conditional DKIM) in the parent
  zone.
- **No marker distinguishes mail from web records** — today only heuristics (name `mail`, type `MX`,
  TXT containing `v=spf1`/`v=DMARC1`, `_dmarc*`, `*_domainkey*`).
- **Records a web-IP sync MUST NOT touch/proxy:** `mail`/`mail.<sub>` A, all `v=spf1` TXT, MX, DKIM
  TXT, DMARC TXT. The apex `@` collision (web A + SPF/DMARC TXT share name `@`) means sync must split by
  **type**.
- **`ReassignServerIP` is the anti-pattern to invert, not reuse:** it moves mail *with* web (correct for
  a whole-box move, wrong for a web-only Cloudflare origin change).
- **Latent gap:** transfer/`EnsureDKIMForDomain`-onboarded domains can sign mail but have no published
  DKIM TXT; a reconcile that prunes "unknown" records must never delete manually-added mail records.

**Design consequence:** add an additive `managed_by`/`source` marker to `DNSRecord` so mail records are
protected by an **explicit flag** going forward, with the heuristic as fallback for legacy rows.

---

## 6. Backup / restore map

- **Authoritative DR path:** `scripts/bzpanel-backup.sh` (`mongodump --gzip --archive` of the whole
  `serverpanel` DB + app DBs + /home + MySQL + mail + DKIM + PowerDNS + letsencrypt + configs; optional
  AES-256; off-site rclone/FTP; retention) / `scripts/bzpanel-restore.sh` (`mongorestore --gzip --drop`).
  **All new Cloudflare fields + `servers` + `cloudflare_sync_jobs` round-trip automatically, IDs
  preserved, no allowlist.**
- **Per-account backup** ([backup_service.go](backend/internal/services/backup_service.go)) serializes
  on-disk artifacts + Mongo metadata ExtJSON, but restore does **not** re-import that metadata (one-way).
- **Requirement to enforce:** restore stays non-destructive/no-blind-overwrite; validate backup before
  restore and restored state after. (Current DR restore is `--drop` whole-DB replace — acceptable for
  full DR but must be gated behind explicit approval; never auto-run.)

---

## 7. Migration map

- **Model:** `TransferJob` Mongo doc holds all state (status/progress/steps/logs). Runs async in an
  in-process `go executeTransfer(...)` goroutine — **no external queue**.
- **~15 stages:** validate → discover → packages → config → software → domains&files → DNS → SSL →
  databases → email/cron/ftp/firewall/node/ssh → mail suite → sync panel records → verify → post-verify
  IP sweep (`ReassignServerIP`) + DKIM repair.
- **Recovery = idempotent replay:** `ResumeRunningTransfers` on boot restarts stuck jobs from step 1
  (steps written idempotently). **No rollback/compensation exists** — "migration rollback" is genuinely
  new work.
- **Cancel** is cooperative (`isCancelled()` between steps). **Verify** is best-effort health-check
  (nginx -t, php-fpm, `dig` per domain), not a data-integrity reconciliation.
- **Cloudflare hook point:** after the post-verify IP sweep, add a step that updates **web-origin
  records only** for Cloudflare-enabled domains on the moved server, protecting mail records.

---

## 8. Auth / RBAC map

- **Roles:** `vendor_owner`, `vendor_admin`, `vendor_staff`, `developer`, `support`, `customer`.
- **`server.manage` is owner-only** and is the existing gate for the SMTP secret + reassign-IP.
- **Guards:** `RequireRole(...)` (exact role) and `RequirePermission(...)` — the latter is **bypassed by
  `vendor_owner` and by `is_super_admin`**. So the *only* guard a super-admin `vendor_admin` cannot pass
  is `RequireRole("vendor_owner")`.
- **Recommendation:** mount Cloudflare config under the existing `serverCfg` group; gate **reads** on
  `config.manage` and **writes** on `server.manage` (mirrors `/config/mail`). If you want strictly
  platform-owner-only with *no* super-admin bypass, use `RequireRole("vendor_owner")` on the write.
  → **Open decision D2 below.**
- **Token exposure:** store encrypted, never echo (GET returns `has_token: true` + masked preview),
  server-side only. Satisfies Phase 12.

---

## 9. Notification / job map

- **Audit:** automatic via `AuditLogger` middleware on every `POST/PUT/PATCH/DELETE` — new Cloudflare
  routes are audited for free; add a `parseRoute` case only for nicer verbs.
- **Email:** add `NotifyCloudflare…` methods to `NotifierService` (no-ops silently when SMTP
  unconfigured).
- **Webhooks:** `EmitEvent(ctx, event, tenantUserID, payload)` fan-out to customer-configured webhooks.
- **Live progress:** durable answer is **Mongo-doc + ~1.5s polling** (survives refresh), per the SSL
  bulk job. The only push hub (install terminal WS) is **in-memory and refresh-lossy** — not suitable
  for durable progress. No SSE anywhere.
- **Notify sparingly:** task started / phase completed / blocked / critical failure / sync completed /
  migration completed / final — aggregated, not per-record.

---

## 10. Risk report (ranked)

| # | Risk | Severity | Mitigation |
|---|---|---|---|
| R1 | **rrset-replace vs per-record-id mismatch** — CF has no "replace rrset"; needs diff-by-id sync | High | Dedicated CF write path keyed by `cf_record_id`; never route CF zones through `reconcileRRSet` |
| R2 | **Mail records clobbered by web-IP sync** (mail A + SPF share the web IP; no marker) | High | Add `managed_by=mail` marker + heuristic fallback; web-IP sync touches web A/www/cname only; never proxy mail records |
| R3 | **PowerDNS leaks above agent layer** (mail DKIM/SPF/DMARC written straight to pdnsutil) | High | `DNSProvider` interface must cover mail-setup paths; for CF zones route mail records through CF API too |
| R4 | **IP used as identity** — repurposing an IP field breaks the sweep's `{server_ip:oldIP}` match | High | `server_id` is a NEW column/collection; never replace existing IP fields |
| R5 | **CF token exposure** to frontend/logs/URL | High | AES-GCM at rest, never echo, server-side only, `server.manage`/owner gate |
| R6 | **Partial CF write / rate-limit corrupts local state** | Med | Idempotent ops, per-record verify, structured errors, retry+backoff; local write only after CF ack |
| R7 | **Duplicate zones/records** on re-sync | Med | findZone-before-create; match by `cf_record_id`; reconcile diff, never blind create |
| R8 | **No migration rollback** exists today | Med | Add checkpoint + explicit rollback path as new work; gate destructive migration behind approval |
| R9 | **Proxied (orange-cloud) forces Auto TTL** — fights bulk-TTL sweep and min-TTL reconcile | Med | Exclude proxied records from TTL sweeps; per-provider TTL rules |
| R10 | **NS defaults hardcoded** (`dns1-4.betazeninfotech.com`) don't apply to CF zones | Med | Suppress NS seeding for CF zones; use CF-assigned nameservers |
| R11 | **DKIM TXT may be absent** for transferred domains; prune could delete manual mail records | Med | Reconcile never deletes mail-classified/unknown records without approval |
| R12 | **`domains.server_ip` phantom field** (written untyped, no Go field) | Low | Leave write path intact; reconcile typing separately, additively |
| R13 | **Agent daemon is X-Agent-Key, not mTLS** (contradicts CLAUDE.md) | Low | Doc/code reconcile; not on the Cloudflare critical path |

---

## 11. Cloudflare integration plan (architecture decisions)

- **D-A DNS provider abstraction.** Introduce `DNSProvider` interface. Existing PowerDNS = default impl
  (`""`/`"powerdns"`). Add `cloudflare` impl over the CF REST API. Select **per-zone** via additive
  `DNSZone.Provider`. CF impl uses its own diff-based sync (not rrset-replace).
- **D-B Centralized credential, per-zone mapping.** One account-level API token, stored encrypted in
  the `server_config{key:"cloudflare"}` singleton. Each domain carries its own `cf_zone_id`,
  `cf_status`, `cf_nameservers`, per-record `cf_record_id`. Never per-domain credentials.
- **D-C Mail protection is first-class.** New `managed_by`/`source` marker on `DNSRecord`; web-IP/origin
  syncs operate on web records only; mail records never proxied and never auto-rewritten by a web-IP
  change.
- **D-D Jobs & progress reuse.** Clone the `SSLBulkJob` pattern into `cloudflare_sync_jobs` (bulk sync
  all + per-domain sync), and add a Cloudflare sub-step to the existing `TransferJob` for migration.
  Durable progress via Mongo-doc + polling; boot-recovery per the existing `RecoverStaleRunningJobs`.
- **D-E Reconciliation, never silent.** Compare view classifies each record LOCAL-ONLY / CF-ONLY /
  CONFLICT / MATCHED; provides Sync Local→CF, Sync CF→Local, Resolve — conflicts never auto-overwritten.
- **D-F Approval gates for destructive ops.** Delete zone, delete records, bulk overwrite, mass sync,
  destructive migration → backend dry-run producing an impact summary + explicit confirm token;
  frontend `confirmAction` modal. No approval = no proceed.
- **D-G Server identity.** New `servers` collection + UUID seeded at boot; `ip_history` appended at the
  `ReassignServerIP` chokepoint; web-origin CF records updated after an IP change for CF-enabled domains.
- **D-H Live-progress UI = real operations, not agent-watching.** The progress dashboard tracks
  **Cloudflare bulk sync** and **server migration** jobs (the things with real persistent state), reusing
  the SSL bulk-job progress UI. We do **not** build a "watch the AI agents" panel.

---

## 12. File-change plan (by increment; all additive)

**Increment 0 — safe foundations (no behavior change to existing DNS):**
- Models: add omitempty fields to `dns.go` (`DNSZone`, `DNSRecord`), new `models/server.go`,
  new `CloudflareConfig` struct.
- `database/collections.go`: `ColServers`, `ColCloudflareSyncJobs`.
- `config_service.go`: `GetCloudflareConfig`/`SetCloudflareConfig` (encrypted token) + connection test.
- `migration.go` + `cmd/server/main.go`: idempotent `servers` seed at boot.
- New `handlers/config_handler.go` routes `GET/PUT /config/cloudflare`, `POST /config/cloudflare/test`
  in the `serverCfg` group.
- Frontend: new `CloudflarePage.tsx` (settings form, masked token), sidebar item `adminOnly:true`,
  `packages/api-client/cloudflare.ts`, `packages/types/cloudflare.ts`.

**Increment 1 — Cloudflare service layer (read-only):**
- New `services/cloudflare_service.go` + `pkg/cloudflare/` client (auth, zones, records, nameservers,
  verify, list) with timeout/retry/backoff/rate-limit/structured errors.
- Read-only compare/reconcile endpoint + UI (no writes).

**Increment 2 — per-domain Cloudflare DNS provider:**
- `DNSProvider` interface; wrap existing pdns calls as `powerdns` impl; `cloudflare` impl.
- Domain Add/Edit provider selection; create/reuse zone; save zone id + nameservers; per-record CRUD
  with `cf_record_id`; mail-record markers stamped.

**Increment 3 — bulk sync + jobs + reconciliation:**
- `services/cloudflare_sync_job_service.go` (clone of SSL bulk pattern) + routes + boot-recover.
- Bulk "Sync All", per-domain sync, progress UI (reuse SSL modal; optionally extract `useJobPolling`).
- Reconciliation resolve actions behind approval gates.

**Increment 4 — IP change + migration integration:**
- `ip_history` append in `ReassignServerIP`; web-origin-only CF update on IP change.
- `TransferJob` Cloudflare sub-step; migration progress surfacing; rollback/checkpoint design.

**Increment 5 — hardening:**
- Failure simulation, security audit, regression + new unit/integration tests, backup/restore
  verification.

---

## 13. Test plan (maps to request Phases 14–15)

Unit + integration for: config CRUD & token encryption/never-echo; valid/invalid/permission-failed
token; zone create/reuse/duplicate-guard; nameserver retrieval; record CRUD for A/AAAA/CNAME/MX/TXT/
SRV/CAA/NS/DS; proxied toggle; subdomain; **mail-DNS protection** (web-IP change leaves mail A/SPF/MX/
DKIM/DMARC intact); IP change; migration + rollback; backup/restore round-trip of new fields; CF API
failure/rate-limit/retry; cancel/boot-restart/progress-recovery; concurrent ops; bulk sync; RBAC;
token-exposure; DB integrity; regression of existing PowerDNS flows. Failure simulation: CF
unavailable, invalid token, rate limited, DB down, worker restart, partial DNS update, migration/restore
interruption — assert no data loss, no state corruption, no duplicate records, no mail loss, no domain
detach, no server-identity change.

---

## 14. Open decisions (need human input before build)

- **D1 — Starting scope.** Recommend building **Increment 0 only** first (pure additive foundations,
  zero change to existing DNS behavior), then stop at its checkpoint for review.
- **D2 — RBAC strictness** for Cloudflare config writes: `server.manage` (matches `/config/mail`, but
  super-admin can pass) vs strict `RequireRole("vendor_owner")` (no super-admin bypass).
- **D3 — Mail marker.** Add the additive `managed_by`/`source` field to `DNSRecord` now (recommended)
  vs rely on heuristics only.
- **D4 — Live-progress UI.** Confirm progress tracks real Cloudflare/migration jobs (SSL-bulk pattern),
  **not** an AI-agent-watching dashboard.
- **D5 — Reconciliation direction default.** When CF and local disagree, default to *manual resolve*
  (recommended) vs a chosen authoritative side.

---

## 15. Rollback strategy (for the integration work itself)

Every increment is additive and independently revertible: new fields are `omitempty` (absent = today's
behavior), new collections are unused by existing queries, provider defaults to `powerdns`, and the
Cloudflare feature is gated by the `enabled` flag in the config singleton. Disabling the flag or
reverting the commit returns the system to exact prior behavior with no data migration. Destructive
runtime operations (zone/record delete, bulk overwrite, destructive migration/restore) are gated behind
dry-run + explicit human approval and are never executed automatically.
