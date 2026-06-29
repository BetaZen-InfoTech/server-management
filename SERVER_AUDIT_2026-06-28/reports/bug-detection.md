# Agent 12 — Bug Detection (code-focused, runtime-confirmed)

**Audit date:** 2026-06-28
**Deployed code:** local git repo `c:/Users/Administrator/Downloads/Project/server-management` — v3.1.109 / rev 466b52e
**Servers:** Server 1 = 89.116.34.207 (migration SOURCE), Server 2 = 195.35.7.64 (migration DEST)
**Mode:** READ-ONLY. No mutating commands issued.

---

## Scope & method

Three priorities from the assignment:

1. **Third-party (587/465) mail-logging defect** — does the v3.1.108 "Source-agnostic mail log" actually capture Postfix-submitted mail?
2. **Server transfer / migration** — data-loss / correctness bugs.
3. **Recent fixes (v3.1.107 wrong-cert / server_name pollution)** — integrity.

Method: read the deployed code, then confirm behaviour against live runtime (panel version, Postfix master.cf, `/var/log/mail.log` format, mongo `mail_logs` collection + indexes, journald ingestor logs). I also wrote a standalone Go harness that replays the **actual** `mail_log_service.go` regexes against realistic Postfix 587/465 submission log lines to prove (or disprove) capture.

### Runtime baseline (both servers, no drift)

```
$ curl -s http://127.0.0.1:8080/api/v1/version          # srv1 AND srv2
{"data":{"name":"Betazen Server Panel","version":"3.1.109",...},"success":true}

$ journalctl -u serverpanel | grep -iE 'mail-log|header_checks'   # srv1 AND srv2
... "message":"mail-log: ingestor started (capturing ALL mail, every source)"
... "message":"mail-log: Postfix header_checks installed (Subject + Content-Type logging)"

$ postconf -h header_checks                              # srv1 AND srv2
regexp:/etc/postfix/header_checks_betazen

$ grep -E '^(submission|smtps)' /etc/postfix/master.cf   # srv1 AND srv2
submission inet  n  -  y  -  -  smtpd
smtps      inet  n  -  y  -  -  smtpd

# mongo serverpanel.mail_logs (srv1 AND srv2)
mail_logs: 0
indexes: _id_, log_key_1 (unique:true), first_seen_-1, status_1, direction_1,
         source_1, domains_1, created_at_1 (expireAfterSeconds:7776000 = 90d TTL)
```

The two servers are byte-for-byte identical on every mail-log-relevant surface; **no drift**. The ingestor is running on both. `mail_logs` is empty on both (no mail has flowed in this demo yet), so the bugs below are confirmed by code + schema + parser replay, not by observing a populated collection.

---

## Priority 1 — Does the panel capture third-party-client (587/465) mail?

### Verdict: the ingestion path is CORRECT for the happy path. It DOES capture Postfix-submitted mail. But there is a real, confirmed **duplicate-row / phantom-stuck-mail bug** for any message that defers > 3 minutes (Finding BUG-1), and a smaller restart-gap (Finding BUG-2).

I replayed the real regexes from `backend/internal/services/mail_log_service.go` (lines 105-129) against realistic submission lines. All fields parse correctly, including the `postfix/submission/smtpd` and `postfix/smtps/smtpd` three-segment program names, SASL user/method, header_checks Subject WARN, qmgr from/size/nrcpt, and the delivery status line:

```
LINE: ...srv1785162 postfix/submission/smtpd[60001]: B1C2D3E4F5: client=mail.example.com[203.0.113.9], sasl_method=PLAIN, sasl_username=alice@tenant.com
  prog="postfix/submission/smtpd" sub="smtpd" service="submission" qid="B1C2D3E4F5"
    client="mail.example.com" ip="203.0.113.9"  sasl_user="alice@tenant.com"  sasl_method="PLAIN"
LINE: ...postfix/cleanup[60002]: B1C2D3E4F5: warning: header Subject: Quarterly report from mail.example.com[203.0.113.9]; from=<alice@tenant.com> ...
  HEADER Subject = "Quarterly report"
LINE: ...postfix/qmgr[42170]: B1C2D3E4F5: from=<alice@tenant.com>, size=2048, nrcpt=1 (queue active)
  from="alice@tenant.com" size="2048" nrcpt="1"
LINE: ...postfix/smtp[60003]: B1C2D3E4F5: to=<bob@gmail.com>, relay=gmail-smtp-in.l.google.com[...]:25, delay=1.4, dsn=2.0.0, status=sent (250 2.0.0 OK)
  to="bob@gmail.com" relay="..." status="sent" resp="250 2.0.0 OK"
LINE: ...postfix/smtps/smtpd[60010]: A9B8C7D6E5: client=unknown[198.51.100.7], sasl_method=LOGIN, sasl_username=carol@tenant.com
  prog="postfix/smtps/smtpd" sub="smtpd" service="smtps" qid="A9B8C7D6E5"
```

So the v3.1.108 fix genuinely closes the "third-party clients never appear" gap: an authenticated remote submitter is classified `source=smtp-client`, its sender domain lands in `domains[]`, and a tenant filter `domains $in tenantDomains` surfaces it. `classify()` (lines 524-567), the unique `log_key` index, and the 90-day TTL are all wired and present in the live DB. **This part works.**

The defects are in the *deferred-mail* lifecycle, below.

---

## FINDINGS

### BUG-1 (MEDIUM) — Deferred mail flushed by the idle timer then re-touched produces a DUPLICATE mail_logs row + a phantom "stuck/deferred" row that never clears

**File:** `backend/internal/services/mail_log_service.go`
**Lines:** flusher `483-505` (esp. `delete(s.partial, qid)` at 499), recreate at `228-237` (esp. `firstSeen: ts` at 235), LogKey derivation at `411`, idle constant at `51`.

**Mechanism.** A queue item is held in memory keyed by qid. The flusher runs every 30s and, for any item idle longer than `mailLogIdleFlush = 3 * time.Minute` (line 51), calls `finalizeLocked(e)` **and then `delete(s.partial, qid)`** (lines 494-500). The finalized row's natural key is

```go
LogKey = fmt.Sprintf("%s:%d", e.queueID, e.firstSeen.Unix())   // line 411
```

When Postfix later logs another line for that **same** qid (a deferred message retrying, a `removed`, a second recipient delivery), `parseLine` finds `s.partial[qid] == nil` (it was deleted) and creates a **fresh** partialEntry with

```go
e = &partialEntry{queueID: qid, firstSeen: ts, ...}   // line 235 — firstSeen = the NEW line's timestamp
```

so its `firstSeen` is now T1 (the retry time), not the original T0. The second finalize therefore writes `LogKey = qid:T1`, which is a **different** key from the first flush's `qid:T0`.

The code comment at lines 496-498 explicitly claims this is safe ("upsert merges by log_key (same first_seen)") — **that claim is wrong.** `firstSeen` is reset on recreate, so the keys differ.

**Why the unique index makes it worse, not better (runtime-confirmed).** `db.mail_logs` has `log_key_1` as `unique:true`. Two *different* log_keys are not a uniqueness violation, so both `UpdateOne(..., upsert:true)` calls insert successfully → **two documents for one physical message**:

```
serverpanel.mail_logs indexes (live): log_key_1 {unique:true}, created_at_1 {expireAfterSeconds:7776000}
```

- Row A (`qid:T0`): the early idle-flush — `queued=true`, `status=deferred` (or `queued`). It is **orphaned**: the qid was deleted from memory, so the later `removed` never updates this row. It sits in the panel forever showing mail "stuck in queue" that actually delivered.
- Row B (`qid:T1`): the final state — `status=sent`. Correct, but a duplicate of the same message.

**Blast radius.** Any message that defers longer than 3 minutes — i.e. exactly the third-party / external-recipient case that v3.1.108 set out to make visible (greylisting, temporary 4xx from the remote MX, a queued-then-retried send from Thunderbird/Outlook). The Stats cards (`Stats`, lines 680-725) double-count these and over-report `deferred`. The List view shows phantom stuck mail.

**Evidence it is reachable, not theoretical:** `mailLogIdleFlush` is 3 min; Gmail/Outlook greylisting and 4xx deferrals routinely exceed that. `finalizeLocked` + `delete` is also hit by `evictOldestLocked` (lines 508-520) under a > 8000-in-flight flood, with the same key-reset consequence.

**Safe, surgical fix (recommended, low risk):** When the idle flusher writes out an item that has **not** yet been `removed`, do **not** delete it from `s.partial` — keep it so later lines update the same in-memory entry (and thus the same `firstSeen`/log_key). Only `delete` once `e.removed` is true. Concretely, in the flusher (lines 493-500):

```go
for qid, e := range s.partial {
    if e.lastEvent.Before(cutoff) {
        s.finalizeLocked(e)
        if e.removed {            // only drop terminal items
            delete(s.partial, qid)
        }
    }
}
```

To bound memory, add a hard-age cap (e.g. drop after 1h regardless) so a never-`removed` qid can't pin a slot forever. Alternatively, stamp `firstSeen` once and never reset it: on recreate, look the row's existing `first_seen` back from Mongo — but the keep-in-memory approach is simpler and is the minimal change. **safeAutoFix: yes** (logic-local, no API/schema change, fails toward "one correct row").

---

### BUG-2 (LOW) — Mail submitted while the panel is down is never logged (tail starts at EOF on every restart)

**File:** `backend/internal/services/mail_log_service.go`, `tailLoop` lines 152-185 (`tail -n 0 -F`), design note lines 14-17.

`tail -n 0 -F` starts at end-of-file. On every panel restart / redeploy / crash, all mail Postfix logged during the downtime is skipped. The header comment hand-waves this as a "sub-second gap", but a deploy or crash-loop is seconds-to-minutes, and those messages are silently invisible in the panel forever — re-opening the exact "this message isn't in the logs" complaint class the feature was built to kill, just narrowed to downtime windows.

**Evidence:** code uses `exec.CommandContext(ctx, "tail", "-n", "0", "-F", mailLogPath)` (line 159); there is no offset/inode persistence.

**Fix (not auto-safe — behavioural change):** persist last-seen position (byte offset + inode, or last log_key timestamp) and resume from there; or on startup do a bounded back-scan of the tail of `mail.log` (e.g. last N KB) and re-ingest, relying on `log_key` idempotency to avoid dupes. Either is more than a one-liner. **safeAutoFix: no.**

---

### BUG-3 (MEDIUM) — Mailbox SSO re-encryption can $unset `encrypted_pass` on EVERY mailbox on the destination, including pre-existing rows unrelated to the transfer

**File:** `backend/internal/services/transfer_panel_records.go`, `reencryptSyncedMailboxes` lines 3106-3179 (esp. the unscoped `UpdateMany` at 3130-3134, and the per-row clear at 3162).

During a server transfer, when the source's `JWT_SECRET` can't be read over SSH (`srcJWT == ""`), the recovery path runs:

```go
mbCol.UpdateMany(ctx,
    bson.M{"encrypted_pass": bson.M{"$exists": true, "$ne": ""}},
    bson.M{"$unset": bson.M{"encrypted_pass": ""}})    // lines 3131-3133
```

This filter is **not scoped to the just-transferred tenant/domains** — it matches *all* mailboxes in the destination DB. On a destination that already hosts other tenants' mailboxes (the normal consolidation-migration case), a single transfer whose source `.env` is unreadable wipes the webmail-SSO ciphertext for **every** unrelated mailbox on the box. The same unscoped breadth applies to the success path's `Find(bson.M{"encrypted_pass": ...})` at line 3138 — it re-keys/clears every mailbox, not just the imported ones.

**Severity rationale:** IMAP/SMTP login keeps working (those key off the SHA512-CRYPT `password` field, per the code's own comment), so this is **not** mail-delivery data loss — it degrades the one-click webmail "Open" SSO for unrelated tenants until each password is reset. Medium, not high.

**Fix (not auto-safe — needs the tenant/domain set threaded in):** scope both the `Find` and the `UpdateMany` to the transfer's `ownedDomains` (e.g. `{"domain": {"$in": ownedDomains}}` or `{"email": {"$regex": domain-suffix}}`). The function currently takes no domain list, so this is a signature change, not a one-liner. **safeAutoFix: no.**

---

### BUG-4 (LOW) — `detectSourceIP` only matches the FIRST `IN A` line in the zone, which may be a sub-record, mis-detecting the source IP and skipping the A-record rewrite

**File:** `backend/internal/services/transfer_service.go`, `detectSourceIP` lines 965-974; consumed at 1784 and gated rewrite at 1883-1886.

```go
re := regexp.MustCompile(`\s+IN\s+A\s+(\d+\.\d+\.\d+\.\d+)`)   // line 967 — returns the FIRST A in the whole zone
```

The migration rewrites A records to the destination IP **only** when `value == oldIP` (lines 1884-1886) — a deliberately conservative guard added to stop clobbering third-party A values. But `oldIP` comes from the *first* `IN A` line in `pdnsutil list-zone` output, which is not guaranteed to be the apex/server A. If the first A encountered is a subdomain pointing elsewhere (a third-party host, a `mail.` A on a different box), `oldIP` is wrong, and the apex A that genuinely needs rewriting (`== real source IP`) won't match `oldIP` → **the apex keeps pointing at the source server after cutover.**

Runtime note: confirmed PowerDNS **4.8.3** on both boxes (`pdnsutil 4.8.3`); its `list-zone` output *does* include the `IN` class, so the regex matches at all (the import loop separately tolerates IN-absent at lines 1838-1841, so the two code paths assume different formats — a latent inconsistency). The "first A in file" assumption is the actual risk. No zones currently exist on either box to sample (DNS data is 0), so this is code-confirmed only.

**Fix (auto-safe-ish):** prefer the apex A — match `^(?:<zone>\.?|@)\s+\d+\s+IN\s+A\s+(...)` first, fall back to first-A only if no apex A exists. Low risk because it only changes which IP is treated as "old". **safeAutoFix: yes** (narrow, conservative).

---

### BUG-5 (LOW) — Mail-log read path swallows the `TenantDomains` error (fails closed, but masks DB faults)

**File:** `backend/internal/services/mail_log_service.go`, `List` lines 651-657 and `Stats` lines 685-691.

```go
domains, _ := scope.TenantDomains(ctx, s.db)   // error dropped
if len(domains) == 0 { return []…, 0, nil }     // transient DB error -> "you have no mail"
```

A transient failure resolving the caller's tenant domains is indistinguishable from "tenant owns no domains": the tenant silently sees an empty mail log instead of an error. It **fails closed** (no cross-tenant leak), so this is low severity / correctness-of-error-reporting, not a security hole. Same pattern in `Stats`.

**Fix (auto-safe):** propagate the error (`if err != nil { return nil, 0, err }`) so the handler returns 500 rather than a misleading empty page. **safeAutoFix: yes.**

---

## Items verified CORRECT (no bug) — integrity of recent fixes

- **v3.1.107 server_name pollution guard** — `AddAliasWithProject` (`project_service.go` 2558-2626) rejects linking a domain that is already another service's `primary_domain` via a `CountDocuments({primary_domain: domain, _id: {$ne}})` check (lines 2584-2589). Persist-before-reconcile ordering is correct (comment 2592-2598). **Intact.**
- **v3.1.107 cert lineage pinning** — `agent/certbot.go` pins `--cert-name <domain>` on issue (139, 192), forced re-issue (192), renew (150), revoke (203), and `--cert-name <primary> --expand` for multi-domain (218-235). No `-NNNN` lineage minting. **Intact.**
- **v3.1.107 mail.* recursion fix** — `agent.MailHostFor` (`transfer.go` 714-718) strips a leading `mail.` before re-prefixing; `DiscoverSSLDomains` awk excludes `^mail\.` and `-[0-9]{4}$` lineages (`transfer.go` 731-734). **Intact.**
- **v3.1.50 mailbox dedup** — `mailboxNaturalKey` queries `email` (`transfer_panel_records.go` 3072-3074), matching the Mailbox model; guarded by `mailbox_dedup_test.go`. **Intact** (not regressed).
- **DNS subdomain-delegation preservation** — the import loop keeps non-apex `NS` records and only skips apex NS/SOA (`transfer_service.go` 1865-1873); FQDN→relative double-suffix strip handles both dotted and bare forms (1911-1923). **Intact.**
- **header_checks safety** — `EnsureHeaderChecks` uses `regexp:` (not `pcre:`), validates with `postmap -q` before wiring, and rolls back on reload failure (`mail_log_service.go` 744-798). Live `postconf -h header_checks` = `regexp:/etc/postfix/header_checks_betazen` on both boxes. **Intact.**
- **cpanel mail-log tenant scoping** — `/api/v1/cpanel/email/logs` runs under `middleware.InjectScope()` (cpanel_routes.go 19), and `MailLogService.List` scopes tenant callers by `domains $in tenantDomains` (651-657). No cross-tenant leak. **Intact.**

---

## Summary table

| ID | Sev | Server | Area | Auto-fix |
|----|-----|--------|------|----------|
| BUG-1 | medium | both | mail_log_service deferred-flush duplicate / phantom-stuck row | yes |
| BUG-2 | low | both | mail_log tail starts at EOF; downtime mail lost | no |
| BUG-3 | medium | both (transfer) | unscoped $unset of encrypted_pass across all mailboxes | no |
| BUG-4 | low | both (transfer) | detectSourceIP picks first A, may skip apex rewrite | yes |
| BUG-5 | low | both | mail_log read swallows TenantDomains error | yes |

All findings are code-confirmed against the deployed v3.1.109 binary; runtime state (ingestor running, unique log_key index, 90-day TTL, regexp header_checks, identical on both servers) corroborates BUG-1/BUG-2/BUG-5. No mutating commands were run.
