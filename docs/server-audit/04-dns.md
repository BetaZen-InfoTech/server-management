# 04 — Domain & DNS Audit — Server 1 (89.116.34.207)

**Auditor:** Agent 4 (Domain & DNS)
**Date:** 2026-06-29
**Box:** Server 1 — `89.116.34.207` / IPv6 `2a02:4780:12:aaa3::1` (DEMO, read-only audit)
**Authoritative DNS:** PowerDNS Authoritative Server **4.8.3**, backend **gsqlite3** (`/var/lib/powerdns/pdns.sqlite3`)
**Domains audited (6):** `mail.demo-one.local`, `company-demo.local`, `mail.demo-two.local`, `examplemail.local`, `testing-domain.local`, `internal.demo.local`

---

## Summary

PowerDNS is healthy and authoritative for all 6 demo zones (19 records each, 6 zones, served on `0.0.0.0:53`; API/webserver bound to `127.0.0.1:8081`). Every zone has the full record set populated — SOA, NS×4, A, AAAA, MX, SPF, DKIM, DMARC, generic TXT, CNAMEs (www/webmail/cname), and CAA — so **completeness is good**.

**Correctness is not.** Four material problems affect all 6 domains uniformly:

1. **DKIM is fully broken** (HIGH). The private key opendkim signs with (`mail.private`), the on-disk public record (`mail.txt`), and the key published in DNS are **three different keys**. Every outbound message will fail DKIM verification.
2. **PowerDNS ↔ Mongo IP drift** (HIGH). Mongo holds the correct Server 1 IP `89.116.34.207`; PowerDNS actually serves `195.35.7.64` (a different/prod-clone host) for every A record and inside every SPF string. What resolves points at the wrong server.
3. **Duplicate / conflicting policy records** (MEDIUM). Each zone publishes **two** apex SPF TXT records and **two** `_dmarc` TXT records with conflicting policies (`p=none` and `p=quarantine`). Both are RFC violations that void SPF and DMARC.
4. **Placeholder / non-production values** (MEDIUM–LOW). AAAA is `2001:db8::1` (RFC 3849 documentation range), NS delegated to `dns1–4.betazeninfotech.com` while SOA serial is stuck at `2` (Mongo thinks `12`), and `.local` TLD + CAA `letsencrypt.org` cannot work on the public internet.

---

## Per-domain completeness matrix

Legend: ✅ present & well-formed · ⚠️ present but wrong/placeholder/duplicated · ❌ missing

| Domain | A | AAAA | MX | SPF | DKIM | DMARC | TXT | CNAME | CAA |
|---|---|---|---|---|---|---|---|---|---|
| mail.demo-one.local   | ⚠️ | ⚠️ | ⚠️ | ⚠️ | ❌ | ⚠️ | ✅ | ✅ | ⚠️ |
| company-demo.local    | ⚠️ | ⚠️ | ✅ | ⚠️ | ❌ | ⚠️ | ✅ | ✅ | ⚠️ |
| mail.demo-two.local   | ⚠️ | ⚠️ | ⚠️ | ⚠️ | ❌ | ⚠️ | ✅ | ✅ | ⚠️ |
| examplemail.local     | ⚠️ | ⚠️ | ✅ | ⚠️ | ❌ | ⚠️ | ✅ | ✅ | ⚠️ |
| testing-domain.local  | ⚠️ | ⚠️ | ✅ | ⚠️ | ❌ | ⚠️ | ✅ | ✅ | ⚠️ |
| internal.demo.local   | ⚠️ | ⚠️ | ✅ | ⚠️ | ❌ | ⚠️ | ✅ | ✅ | ⚠️ |

**Cell rationale (uniform across all 6 unless noted):**

- **A** ⚠️ — record present and resolves, but serves `195.35.7.64` (wrong host; should be `89.116.34.207`). `@` and `mail` both affected.
- **AAAA** ⚠️ — `2001:db8::1`, RFC 3849 documentation prefix, not the box's real `2a02:4780:12:aaa3::1`.
- **MX** — `10 mail.<zone>`; target has an A record in every zone (✅). For the two `mail.*` zones the MX is `mail.mail.demo-one.local` / `mail.mail.demo-two.local`, which also has an A record but is an awkward double-`mail` host (⚠️).
- **SPF** ⚠️ — two apex TXT records (`v=spf1 ip4:195.35.7.64 ~all` AND `v=spf1 a mx ip4:195.35.7.64 ~all`). Multiple SPF records = PermError (RFC 7208 §3.2); also the IP is the wrong host.
- **DKIM** ❌ — published key does not match the signing key (see Issue #1). Functionally non-working.
- **DMARC** ⚠️ — two `_dmarc` TXT records with conflicting policies; per RFC 7489 §6.6.3 a domain with >1 DMARC record is treated as if none exists.
- **TXT** ✅ — generic `_demo` verification TXT present and well-formed.
- **CNAME** ✅ — `www`, `webmail`, `cname` all present, all → zone apex.
- **CAA** ⚠️ — `0 issue "letsencrypt.org"` present and syntactically valid, but cannot function for a `.local` domain in production.

---

## PowerDNS vs Mongo consistency

| Dimension | Mongo (`dns_zones`/`dns_records`) | PowerDNS (served) | Consistent? |
|---|---|---|---|
| Zone count | 6 | 6 | ✅ |
| Record count | 114 total (19/zone) | 114 (19/zone) | ✅ structurally |
| Record type mix per zone | A2 AAAA1 CAA1 CNAME3 MX1 NS4 SOA1 TXT6 | identical | ✅ |
| **A / mail A value** | `89.116.34.207` | `195.35.7.64` | ❌ **DRIFT** |
| **SPF ip4 value** | `ip4:89.116.34.207` | `ip4:195.35.7.64` | ❌ **DRIFT** |
| AAAA value | `2001:db8::1` | `2001:db8::1` | ✅ (both placeholder) |
| MX / DMARC / DKIM / TXT / CNAME / CAA values | match | match | ✅ |
| **SOA serial** | `dns_zones.serial = 12` | SOA serial `2` | ❌ **DRIFT** |

**Interpretation:** The two stores share the same *structure* (record types/counts line up exactly), but the **A records and SPF carry different IPs** — Mongo has this box's real IP, PowerDNS serves a foreign IP (`195.35.7.64`). PowerDNS is what resolvers see, so as-served the demo points clients at the wrong server. The **SOA serial is also out of sync** (Mongo 12 vs served 2), indicating the panel's serial bookkeeping is not being pushed into the gsqlite3 backend — a sign the sync path from Mongo → PowerDNS is broken or one-time-seeded and never reconciled.

---

## Issues (by severity)

### HIGH-1 — DKIM completely non-functional (all 6 domains)
Three different keys are in play for `mail._domainkey.<zone>`:
- **Signing key** opendkim loads (`/etc/opendkim/keys/<zone>/mail.private`, per `/etc/opendkim/key.table`)
- **On-disk public record** (`/etc/opendkim/keys/<zone>/mail.txt`)
- **Published DNS key** (PowerDNS / Mongo TXT)

Verified by deriving the public key from each `mail.private` with `openssl rsa -pubout` and comparing full base64: `priv == dns` → **NO** for every domain, and `priv == disk.txt` → **NO** as well. Example tails for `mail.demo-one.local`: signing key ends `...OvMoGkQf7u7Sa1x5Lp0VeuwIDAQAB`, DNS key ends `...ktLNkQk9U0o1/CC2PBlA85QIDAQAB`. Because the published key never matches the signing key, **every DKIM signature this box emits will fail verification** at the receiver. In production this drives mail to spam/reject (especially combined with the broken DMARC below).
Additionally `/etc/opendkim/key.table` and `/etc/opendkim/signing.table` each list **every domain twice** (duplicate entries) — harmless to signing but indicates a re-seed that appended rather than replaced.

### HIGH-2 — A-record & SPF IP drift (PowerDNS serves the wrong host)
PowerDNS answers `195.35.7.64` for all apex and `mail` A records and embeds the same IP in SPF, while the actual box is `89.116.34.207` (which is what Mongo holds). Clients/MX following DNS would be steered to a different server. In a real deploy this is a hard outage / mis-delivery.

### MEDIUM-1 — Multiple SPF records per domain
Two apex `v=spf1` TXT records coexist (`ip4:...` and `a mx ip4:...`). RFC 7208 mandates exactly one; receivers return **PermError**, neutralizing SPF entirely.

### MEDIUM-2 — Multiple / conflicting DMARC records
Two `_dmarc` TXT records per zone with opposing policies (`p=none` rua=admin@ and `p=quarantine` rua=dmarc@). RFC 7489 says a name with >1 DMARC record is treated as **no DMARC**, so the intended `p=quarantine` enforcement never applies.

### MEDIUM-3 — SOA serial frozen / not synced
Served SOA serial is `2` for all zones while Mongo records serial `12`. Secondaries (the `dns1–4.betazeninfotech.com` NS set) would never see updates because the serial never advances. The Mongo→gsqlite3 sync is not propagating serial bumps.

### LOW-1 — Placeholder AAAA
`2001:db8::1` is the RFC 3849 documentation prefix, not a routable address (and not the box's real IPv6). Would black-hole IPv6 traffic in production.

### LOW-2 — Non-production-viable by construction
`.local` is a reserved/mDNS TLD (RFC 6762) — not delegatable on the public internet; the `dns1–4.betazeninfotech.com` NS delegation and `letsencrypt.org` CAA cannot issue real certs or be queried publicly. Expected for a demo, flagged for production readiness.

### INFO — Awkward MX target on `mail.*` zones
`mail.demo-one.local` / `mail.demo-two.local` use MX `mail.mail.demo-one.local` (double `mail`). It resolves (A record present) but is an unusual host name that would confuse operators in production.

---

## Fixes applied

**None.** This is a DEMO box and audit is read-only. Every issue found is either (a) a value-drift / key-regeneration problem that is NOT trivially safe to mutate (rewriting A/SPF/DKIM/DMARC could break the running demo and the Mongo↔PowerDNS sync), or (b) inherent to the `.local` demo design. Per the mandate, these are recorded as recommendations rather than mutated. No SSH/firewall/zone-transfer settings were touched.

---

## Recommendations (for a real deploy)

1. **Fix DKIM end-to-end (HIGH).** Pick one source of truth. Re-publish the public key derived from each live `mail.private` (`opendkim-genkey` output) into both Mongo and PowerDNS so `priv == dns`, and regenerate `mail.txt` to match. De-duplicate `key.table` / `signing.table`. Then test with `opendkim-testkey -d <domain> -s mail -vvv`.
2. **Resolve the IP drift (HIGH).** Decide the authoritative IP and reconcile: either repoint PowerDNS A/SPF to `89.116.34.207` (this box) or fix Mongo if `195.35.7.64` is intended. Critically, **repair the Mongo→PowerDNS sync** so the two never diverge again (and so the SOA serial advances on every change).
3. **Collapse to one SPF record per domain (MEDIUM).** Keep a single `v=spf1 a mx ip4:<correct-ip> ~all`.
4. **Collapse to one DMARC record per domain (MEDIUM).** Keep the intended `p=quarantine` (or `p=reject` for production), single `rua` mailbox; delete the `p=none` duplicate.
5. **Advance SOA serials on change (MEDIUM).** Have the panel write the Mongo serial into the gsqlite3 SOA (use `pdnsutil increase-serial` or panel-driven update) so secondaries pick up changes.
6. **Replace placeholder AAAA (LOW).** Use the real IPv6 `2a02:4780:12:aaa3::1` or remove AAAA until IPv6 mail is ready.
7. **For production, swap `.local` for a real, delegatable domain** with public NS and valid CAA; only then can Let's Encrypt and public DNS function.
8. **Normalize MX hostnames** on the `mail.*` zones to avoid the double-`mail` target.

---

## Appendix — Evidence highlights

- PowerDNS: `pdnsutil list-all-zones` → 6 zones; `pdns_server --version` → 4.8.3; backend `launch=gsqlite3`.
- Served SOA (all zones): `dns1.betazeninfotech.com. hostmaster.<zone>. 2 10800 3600 604800 3600`.
- A drift: `dig +short @127.0.0.1 <zone> A` → `195.35.7.64`; Mongo `dns_records` A value → `89.116.34.207`.
- SPF drift + duplication: served `"v=spf1 a mx ip4:195.35.7.64 ~all"` **and** `"v=spf1 ip4:195.35.7.64 ~all"`; Mongo uses `ip4:89.116.34.207`.
- DMARC duplication: served `p=none` (rua admin@) **and** `p=quarantine` (rua dmarc@) per zone.
- DKIM: `openssl rsa -in mail.private -pubout` ≠ DNS `p=` for all 6 (`priv==dns? NO`), and `priv==disk.txt? NO`. KeyTable/SigningTable have duplicate per-domain lines.
- Mongo inventory: `dns_zones`=6 (serial 12), `dns_records`=114 (19/zone), type mix matches PowerDNS exactly.
