# Server Migration — Domain Safety, Root-Cause Fixes & Runbook

*Betazen Server Panel · why server-to-server migration was losing/omitting domains, what changed to make it safe, and the exact backup → dry-run → verify → migrate → rollback procedure.*

> **TL;DR (এক লাইনে):** Migration-এর সময় কিছু domain **নতুন server-এ add হচ্ছিল না**, আর কিছু domain **"remove"/unreachable** হয়ে যাচ্ছিল। মূল কারণ ছিল — (1) source-এর mongo export fail করলে সেটা "empty but success" ধরা হতো, (2) discovery fail করলেও transfer "success" দেখাতো, (3) nginx vhost rollback **সব domain-এর** symlink মুছে ফেলতো, (4) roster mirror **delete-then-reinsert** করতো (idempotent ছিল না), (5) domain-owner overwrite হয়ে tenant scope থেকে হারিয়ে যেতো। এখন সব fix করা — migration **idempotent, resumable, non-destructive**, আর খালি/incomplete data দিয়ে কোনো domain **কখনো delete হবে না**।

---

## 1. Reported symptoms

- After a migration, **attached domains were not added** to the new server.
- **Some domains got removed** — the row was there in the panel but the site fell to the catch-all 404, or the domain disappeared from a vendor's Domains list.

Both symptoms have distinct mechanisms; the domain-import path only ever *inserts* (never deletes) domain rows, so "removed" was either **unreachable** (nginx/DNS) or **invisible** (tenant scope), and "not added" was a **silent skip**.

---

## 2. Root causes found (adversarially verified) + fixes

Each fix is **idempotent, resumable, and non-destructive** — no fix deletes or hides a domain because incoming data is empty/incomplete.

| # | Severity | Root cause | File | Fix |
|---|---|---|---|---|
| **A** | Critical (removed / unreachable) | `healDisabledVhostSymlinks` rolled back **every** domain's `sites-enabled` symlink on any `nginx -t` failure — one imported vhost with a dangling cert path stripped every other domain; the next reload dropped them all to the default vhost. | `transfer_panel_records.go` | Track only the symlinks **this pass created** and roll back exactly those. |
| **B** | Critical (not added) | `RemoteMongoExport` swallowed every source-side failure (auth error, wrong DB, tool/version mismatch) as an **empty-but-successful** result → 0 domains synced, step reported success. | `agent/transfer.go` | Distinguish failure from empty: read the URI from both env files (tolerate `export ` prefix), honour the caller's DB via `--db`, try raw→admin(`authSource=admin`)→localhost with mongoexport then mongosh, emit `__BZ_EXPORT_FAILED__` on total failure → Go returns an **error** so callers warn/skip (never sync zero as "done"). |
| **C** | Critical (not added) | A failed/timed-out **Discover** left `discovered==nil`; the executor fell through, the file step transferred nothing, and Sync Panel Records was skipped yet marked "complete" — a zero-domain "successful" migration. | `transfer_service.go` | On Discover error or nil, **fail the whole job and return** (non-destructive — nothing has been written yet; operator retries). |
| **D** | High (removed / invisible) | The file-step domain upsert put `user` (owner) and `php_version` in `$set`, so a re-run/consolidation **overwrote an existing domain's owner** with a filesystem-arbitrary account → the domain dropped out of its real tenant's scoped view. | `transfer_service.go` | Move `user` + `php_version` to `$setOnInsert`; only `status`/`updated_at` refresh on an existing row. |
| **E** | High (not added) | Domain rows were pulled by **byte-exact `domains.user ∈ picked`**, but the file/cascade ownership model is broader (docroot/parent-subdomain heuristics). A domain whose stored `user` was empty/case-mismatched/parent-owned transferred as files but got **no row** (so `healMissingVhosts` couldn't heal it either). | `transfer_panel_records.go` | Backfill domain rows **by domain name** from the heuristic `sel.Domains` set (insert-only, dedup by `{domain}`). |
| **F** | High (removed / dark) | `repointSourceDNSToDestination` rewrote A/SPF for **every** source PowerDNS zone (`pdnsutil list-all-zones`). In a partial migration, **non-migrated co-hosted domains** were flipped to the destination IP and went dark. | `transfer_panel_records.go` | Scope the repoint to the **owned/migrated** domain set (and their parent zones); if nothing is owned, repoint nothing. Domain strings are sanitised before shell interpolation. |
| **G** | Critical (removed / idempotency) | The roster mirror did **DeleteMany(collide) + InsertOne(new _id)**: non-idempotent (churned `_id`s → orphaned destination-only dependents), a crash-in-window + source-unreachable-on-resume **permanently deleted users** and orphaned their domains, and a unique-email insert failure left a username with **zero** user rows. | `transfer_panel_records.go` | **Upsert-by-email** with a **stable `_id`** (update colliding rows in place, insert new ones), **no destructive delete** of destination accounts, delete-after-confirm for placeholders, and a partial-export warning for unresolved tenant roots. |
| **H** | High (hardening) | (1) No concurrency guard — a double-clicked Start ran two roster mirrors that cross-deleted each other. (2) `insertDeduped`'s `{domain:null}` false-matched an unrelated row and skipped a real insert (the v3.1.50 mailbox bug shape). (3) `durablyAttachAliasDomains` ran **before** the rows it needs were materialized. | `transfer_service.go`, `transfer_panel_records.go` | (1) Refuse a second active transfer from the same source. (2) Skip the dedup lookup when the natural key has an empty/nil component (insert instead of silently dropping). (3) Run `materializeReferencedDomains` **before** `durablyAttachAliasDomains`. |
| **I** | High (attached domains) — v3.1.187 | Deploy Software project **attached/addon domains** all vanished from the destination. A domain attached to a project service carries `proxy_service_id` + `proxy_port` on its row (the durable model), but the file step makes a bare row, the domains sync is insert-only (skip-if-exists), and `enrichDomainRegistration` only backfilled WHOIS — so the proxy binding never crossed (108 attached on source → 0 on destination, even though the reverse-proxy vhosts were built correctly). | `transfer_panel_records.go` | `enrichDomainRegistration` now also `$set`s `proxy_service_id` (only when the referenced service exists on the destination — never a dangling link), `proxy_port`, `force_ssl`, `document_root`. Confirmed against a live migration: 108/108 attached domains restored. |

### Guarantees now in force

- **Non-destructive:** the roster mirror performs **no destructive delete** of destination accounts; empty/incomplete/partial source data can no longer delete or hide a domain.
- **Idempotent:** re-running a migration updates in place (stable `_id`s, dedup by natural key) — no duplicate domains, no churned identities.
- **Resumable:** no delete→reinsert window; a resume after a crash re-runs safely.
- **Fails loud:** a source-read or discovery failure now **fails the job / warns**, instead of silently reporting a zero-domain success.

---

## 3. Migration → Cloudflare DNS auto-update

When a migration reassigns the server IP (`Transfer` end-of-run, or **Server Settings → Reassign IP**), the panel repoints Cloudflare **web** origin records:

- **A (IPv4)** and, on dual-stack servers, **AAAA (IPv6)** records whose value equals the **old** server IP → the **new** IP.
- **Only** Cloudflare-enabled zones (`cloudflare_enabled != false`, `cf_zone_id` set); per-domain-disabled and non-migrated domains are skipped.
- **Mail is protected** — `mail` A/AAAA and SPF `ip4:` are never moved on a web-IP change.
- **Update-only** — the sweep never deletes a Cloudflare record; proxied (orange-cloud) state, TTL and priority are preserved.

Wiring: `transfer` → `ConfigService.ReassignServerIP` → `CloudflareService.UpdateWebRecordsForServerIPChange` (A) + `UpdateWebAAAARecordsForServerIPChange` (AAAA). See `docs/cloudflare-guide.md` §6.

---

## 4. Production runbook (backup → dry-run → verify → migrate → rollback)

> Run these **on the destination panel host** unless noted. Replace `SRC`/`DST` with the real IPs. **Rotate the SSH password after the cutover** — a shared root-equivalent password is a standing risk.

### 4.1 Backup first (both boxes)

```bash
# On BOTH servers — snapshot the panel DB and the nginx/pdns state.
TS=$(date +%F-%H%M)
mongodump --uri "$(grep -E '^(export )?(MONGO_URI|MONGODB_URI)=' /opt/serverpanel/.env | head -1 | sed -E 's/^[^=]+=//' | tr -d '"'"'"'"'"'')" \
  --out "/root/bzpanel-backup-$TS"
tar czf "/root/nginx-$TS.tgz"  /etc/nginx/sites-available /etc/nginx/sites-enabled
pdnsutil list-all-zones > "/root/pdns-zones-$TS.txt" 2>/dev/null || true
```

### 4.2 Domain count BEFORE (baseline)

```bash
# Count domains on the SOURCE panel DB (authoritative baseline).
mongosh "$SRC_URI" --quiet --eval 'db.getSiblingDB("serverpanel").domains.countDocuments({})'
# Save the full list too, for the after-diff:
mongosh "$SRC_URI" --quiet --eval 'db.getSiblingDB("serverpanel").domains.find({},{_id:0,domain:1}).toArray().map(d=>d.domain).sort().join("\n")' > /root/domains-before.txt
```

### 4.3 Dry-run (no destructive prod change)

The transfer is now safe to trial because it is non-destructive and idempotent:

1. In WHM → **Transfer**, run a transfer with **only the DB/panel-records components** (leave file-copy off) against a **staging** destination, or against the real destination with the understanding that panel-records sync is insert/upsert-only.
2. Watch the transfer log. Confirm you see, per step, real counts (`Synced N domains`, `Roster mirror: X new, Y updated in place`) and **no** `__BZ_EXPORT_FAILED__` / "Discovery failed" errors. If Discover or the source mongo read fails, the job now **fails loudly** — fix source auth/`authSource=admin` before proceeding.
3. Repeat the transfer (idempotency check): the second run should report ~0 new domains and 0 deletions.

### 4.4 Real migration

Run the full transfer (files + panel records + DNS). The safeguards apply automatically:
- Discover failure aborts (retry rather than a silent empty run).
- Roster mirror upserts in place (no user/domain loss).
- vhost heal rolls back only its own changes.
- Source DNS repoint touches only migrated zones.

### 4.5 Verify AFTER (against the baseline)

```bash
# Count + list on the DESTINATION panel DB.
mongosh "$DST_URI" --quiet --eval 'db.getSiblingDB("serverpanel").domains.countDocuments({})'
mongosh "$DST_URI" --quiet --eval 'db.getSiblingDB("serverpanel").domains.find({},{_id:0,domain:1}).toArray().map(d=>d.domain).sort().join("\n")' > /root/domains-after.txt

# Any domain present before but missing after (should be EMPTY):
comm -23 /root/domains-before.txt /root/domains-after.txt

# Every domain has a vhost + is enabled + responds:
while read -r d; do
  [ -f "/etc/nginx/sites-available/$d" ] || echo "MISSING vhost: $d"
  [ -L "/etc/nginx/sites-enabled/$d" ]   || echo "NOT enabled:   $d"
  code=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: $d" http://127.0.0.1/ || echo ERR)
  echo "$d -> $code"
done < /root/domains-after.txt
nginx -t
```

Also confirm in the panel UI: WHM Domains count, a spot-check vendor's User-Panel Domains list (tenant scope intact), SSL status, and HTTPS responses for a few domains.

### 4.6 Rollback

Because every fix is non-destructive, rollback is rarely needed for domain loss. If you must revert an IP change:

```bash
# Reverse the Cloudflare/pdns repoint by reassigning back:
#   WHM → Server Settings → Reassign IP  (new = old server IP)
# or restore the DB + nginx/pdns snapshots taken in 4.1:
mongorestore --uri "$DST_URI" --drop "/root/bzpanel-backup-$TS/"
tar xzf "/root/nginx-$TS.tgz" -C /
systemctl reload nginx && systemctl restart pdns
```

Keep DNS TTLs low during a cutover so a rollback propagates fast.

---

## 5. Remaining risks / manual steps

- **Source `authSource=admin`.** If the source `.env` `MONGO_URI` can't authenticate, the panel-records sync now **fails loudly** (good) but transfers nothing. Ensure the source URI includes `?authSource=admin` (or the admin fallback can derive it) before migrating.
- **Registrar nameservers** for Cloudflare zones are outside the panel's control — the operator must point the registrar at the Cloudflare nameservers (see the Cloudflare guide) for a zone to go `active`.
- **Password hygiene:** rotate the shared SSH/root password after cutover and prefer key-based auth.
- **IPv6:** the AAAA sweep only fires when the panel can infer the old/new v6 pair from the host + zones; a lone AAAA pointing elsewhere is intentionally left untouched.

---

*See also: `docs/cloudflare-guide.md` (Cloudflare setup + migration DNS), `docs/api/openapi.yaml` and `docs/postman/API-Reference.md` (§8b Cloudflare external API).*
