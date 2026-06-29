# 15 — Bug Report

**Date:** 2026-06-29  Consolidated from 9 audit agents + orchestrator verification.
Each item was **independently confirmed** (not taken on a single agent's word); two cross-agent
claims were adversarially re-checked and one was overturned as a false alarm (see §C).

## A. Confirmed code bug (FIXED this run)

| ID | Severity | Bug | Evidence | Status |
|----|----------|-----|----------|--------|
| **B1** | Medium | `POST /whm/transfers/test-connection` returns **HTTP 500 INTERNAL_ERROR** when the connectivity probe fails for ordinary caller-supplied reasons (unreachable host, closed/filtered port, bad credentials). A failed *test* is an expected outcome, not a server fault; the sibling `backups/test-connection` correctly returns 400. | `transfer_handler.go:69-70` returned `response.InternalError`; reproduced by the API agent against an unreachable host. | ✅ **Fixed** → returns `400 BAD_REQUEST` with the failure detail (`transfer_handler.go`, v3.1.114). Cross-compiles clean for linux/amd64. Committed; ships on next deploy. |

## B. Confirmed data-quality / config defects on Server 1 (documented, not auto-fixed)

These are **low real-world impact** because the demo uses the non-public `.local` TLD, and the
safe panel-native remediation (a bulk DNS rewrite) was intentionally **not forced** — both because
the harness flags mass DNS mutation for explicit human authorization and because the
"don't change working modules" rule applies to a working demo. Exact remediation is given in report 17.

| ID | Severity | Defect | Evidence |
|----|----------|--------|----------|
| **B2** | High (cosmetic on `.local`) | **PowerDNS on S1 serves `195.35.7.64` (Server 2's IP)** for every A record, while Mongo correctly holds `89.116.34.207`. The Mongo→PowerDNS sync for A records is stale; SPF text also still embeds `ip4:195.35.7.64`. | `dig @127.0.0.1 company-demo.local A` → `195.35.7.64`; Mongo `dns_records.value` → `89.116.34.207` (all 12 A); PowerDNS SOA serial `2` vs Mongo `12`. |
| **B3** | Medium | **Duplicate SPF (2×) and DMARC (2×) records** per zone (the two variants differ: `a mx ip4:` vs bare `ip4:`, and `p=none` vs `p=quarantine`), plus **duplicate OpenDKIM `key.table`/`signing.table` entries** (12 lines, 6 unique). Two SPF/DMARC records void SPF (PermError) and DMARC. | `dig` shows 2 SPF + 2 `_dmarc` TXT per zone; `wc -l key.table` = 12, `sort -u` = 6. |
| **B4** | Low (`.local`) | **DKIM published key ≠ signing key** (selector `mail`) on S1 — the DNS-published `mail._domainkey` TXT does not match the public key derived from `mail.private`. No external verifier exists for `.local`, so impact is internal-consistency only. | `openssl rsa -pubout` of `mail.private` vs `dig mail._domainkey.<zone> TXT` → mismatch on all 6. |

> Note: after the 2026-06-29 migration, **Server 2 is free of B2/B3** — `rehydrate-all`'s
> `replace-rrset` collapsed the duplicates and S2 correctly serves its own IP. These defects
> remain only on the source box S1.

## C. Reported issue re-verified — NOT a bug

| Claim | Verdict | Evidence |
|-------|---------|----------|
| "**Emails sent through third-party SMTP clients do not appear in application logs**" (the user's headline concern) | ✅ **RESOLVED — works correctly.** Was the pre-v3.1.108 behaviour; the deployed v3.1.112 source-agnostic ingestor tails Postfix's authoritative log and captures **every** message from every source. | Live end-to-end test on **both** servers: an authenticated submission from a non-loopback client was captured in `mail_logs` within 3s as `source=smtp-client` with real client IP, auth method, Message-ID, SMTP response, queue status, delivery result, and attachment flag. `mail_logs` now spans webmail/smtp-client/api-local/inbound-smtp. |
| "**`metrics` collection grows unbounded — needs a TTL**" (flagged by 3 agents) | ⚠️ **Overstated — already bounded.** | `monitoring_service.go:280` runs `DeleteMany({timestamp:{$lt: now-7d}})` every tick ("keep 7 days"). The growth observed during the audit is the initial 7-day fill, not unbounded. A 90-day TTL index was added at runtime as harmless defence-in-depth; **`indexes.go` was deliberately left unchanged** to avoid duplicating the working app-level sweep. |

## D. Security / hardening findings (not bugs — see reports 08, 17)

Plaintext HTTP on the panel, SSH root+password exposure, legacy TLS 1.0/1.1 on mail/nginx,
missing HSTS/CSP, no whole-server backup, SpamAssassin not wired into Postfix, and no
alerting/monitoring. Risk-ranked with remediation in **08-security.md** and **17-recommendations.md**.
