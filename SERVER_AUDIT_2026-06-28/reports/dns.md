# AGENT 4 — DOMAIN & DNS AUDIT

**Date:** 2026-06-28
**Scope:** PowerDNS + panel DNS integration on both demo VPS clones.
**Servers:** S1 = 89.116.34.207 (migration SOURCE, `srv1785162`) · S2 = 195.35.7.64 (migration DEST, `srv1789639`)
**Deployed panel:** Betazen Server Panel v3.1.109 (rev 466b52e) — confirmed live via `GET /api/v1/version` on both boxes.
**Mode:** READ-ONLY. No service was restarted/reloaded, no config edited, no zone/record created or deleted.

---

## 1. Executive summary

Both servers run an **identical, healthy** DNS stack: **PowerDNS Authoritative Server 4.8.3** with the **`gsqlite3`** backend (`/var/lib/powerdns/pdns.sqlite3`), authoritative-only (no recursor binary, no `allow-recursion`, queries for foreign zones return `REFUSED`), listening on `0.0.0.0:53` (UDP+TCP) with the HTTP API enabled on `127.0.0.1:8081`.

The PowerDNS HTTP API is **enabled and answering** (`api=yes`, `api-key` set, webserver bound to localhost only), **but the panel does not use it.** The entire panel↔PowerDNS integration is driven through the **`pdnsutil` CLI** (`create-zone`, `add-record`, `replace-rrset`, `delete-rrset`, `list-zone`, `list-all-zones`, `delete-zone`) plus `pdns_control reload`, executed locally by the Go server via `agent.RunCommand`. CLI, SQLite backend, and HTTP API all agree: **0 zones / 0 records** on both boxes, and the panel's `GET /whm/dns/zones` returns `[]`. Mongo `dns_zones`/`dns_records`/`domains` are all 0 too — matching the stated demo data state.

DKIM keys are generated on-demand by the panel at zone-create time via `opendkim-genkey` into `/etc/opendkim/keys/<domain>/`; `opendkim` is active+enabled and `opendkim-genkey` is installed on both boxes. No per-domain keys exist yet (no domains).

**Drift S1 vs S2:** none of substance. The only differences are the expected per-host values — IP, IPv6, hostname, `MAIL_HOSTNAME`, and the PowerDNS `api-key` (S1 `9ec489b29e1f059d0a10be5a87522ae6`, S2 `b69feb95961e23b9f26ab7f2f49ace84`). Package versions, backend type, config layout, firewall rules, resolver config, and counts are byte-for-byte equivalent.

**Two code-level issues worth flagging for demo/migration planning** (neither is a runtime fault today because there are 0 zones): (a) the panel accepts record-type aliases `SPF`/`DMARC`/`ALIAS` at the API but never translates them to real PowerDNS qtypes before calling `pdnsutil`, so a manual "Add Record" with those types will fail; (b) the panel's default nameservers are hard-coded to `dns1..dns4.betazeninfotech.com`, which are **not** delegated to these demo IPs — fine for demo data, but real public resolution would require either real delegation or overriding nameservers per zone.

---

## 2. Integration mechanism (code, read locally)

**Source of truth design:** Mongo is the source of truth; PowerDNS is the projection. Every panel DNS write goes Mongo-first, then `reconcileRRSet` rewrites the matching PowerDNS rrset via `pdnsutil replace-rrset` (rollback of the Mongo insert on PowerDNS failure). Reads (`ListZones`/`ListRecords`) enrich `pdnsutil list-*` output with Mongo metadata and "heal on read" (backfill Mongo rows for orphan PowerDNS records).

| Operation | Code path | Underlying command |
|---|---|---|
| Create zone | `DNSService.CreateZone` → `agent.CreateDNSZone` (`backend/internal/agent/dns.go:9`) | `pdnsutil create-zone`, `replace-rrset … SOA`, `add-record … NS/A/CNAME`, `pdns_control reload` |
| List zones | `DNSService.ListZones` → `agent.ListAllZones` (`dns.go:145`) | `pdnsutil list-all-zones` |
| List records | `DNSService.ListRecords` → `agent.ListZoneRecords` (`dns.go:170`) | `pdnsutil list-zone` |
| Add record | `DNSService.AddRecord` → `reconcileRRSet` → `agent.ReplaceDNSRecordSet` (`dns.go:114`) | `pdnsutil replace-rrset`, `pdns_control reload` |
| Update / Delete record | `UpdateRecord` / `DeleteRecord` → `reconcileRRSet` | `pdnsutil replace-rrset` / `delete-rrset`, `pdns_control reload` |
| Export zone | `DNSService.ExportZone` → `agent.ExportDNSZone` (`dns.go:127`) | `pdnsutil list-zone` |
| Delete zone | `DNSService.DeleteZone` → `agent.DeleteDNSZone` (`dns.go:62`) | `pdnsutil delete-zone`, `pdns_control reload` |
| Reconcile zone | `DNSService.ReconcileZone` (`dns_service.go:1077`) | walks Mongo, replace-rrset per (name,type) |
| Bulk TTL | `DNSService.BulkUpdateTTL` (`dns_service.go:1456`) | UpdateMany in Mongo + reconcileRRSet per rrset |

**Key conclusion:** the live `api=yes` / `webserver=yes` on `127.0.0.1:8081` is **dead weight for the panel** — grep of the whole `backend/` tree shows no PowerDNS-API HTTP client (no `X-API-Key` send, no `:8081` dial) in any DNS path. It is bound to localhost-only and `webserver-allow-from=127.0.0.1`, so it is not externally exposed, but it does hold the zone-management API open locally. Closing it would not affect the panel.

**Routes (WHM):** `backend/internal/routes/whm_routes.go:270-296` — `GET/POST /dns/zones`, `GET/DELETE /dns/zones/:domain`, `GET/POST /dns/zones/:domain/records`, `POST /dns/zones/:domain/records/bulk`, `PUT/DELETE /dns/zones/:domain/records/:id`, `GET /dns/zones/:domain/export`, `POST /dns/zones/:domain/reconcile`, `POST /dns/bulk-ttl`. Gated by `dns.view` / `dns.manage`. The same handlers are mounted on the cPanel tree with tenant scoping enforced in the service layer (`assertCallerOwnsDomain`, `CallerScope.TenantDomains`).

### Supported record types
From `models/dns.go:55` (`CreateRecordRequest.Type` enum, validated strict):
`A, AAAA, AFSDB, ALIAS, CAA, CNAME, DMARC, DNAME, DS, HINFO, HTTPS, LOC, MX, NAPTR, NS, PTR, RP, SOA, SPF, SRV, TXT`.

The asked-about set is covered: **A / AAAA / MX / TXT / CNAME / CAA** are native; **SPF / DKIM / DMARC** are authored as **TXT** records (the panel's auto-mail path writes them correctly as `Type:"TXT"` with `_dmarc`/`mail._domainkey` names and `v=spf1`/`v=DMARC1`/DKIM content — `dns_service.go:1390-1411`). Per-type wire formatting (TXT quoting, MX priority prefix, SRV/CAA fields) is handled in `formatRecordValueForPDNS` (`dns_service.go:557`).

### DKIM key generation
`DNSService.setupMailServer` (`dns_service.go:1344`) runs on every zone create:
- `mkdir -p /etc/opendkim/keys/<domain>`
- `opendkim-genkey -s mail -d <domain> -D /etc/opendkim/keys/<domain>` → produces `mail.private` + `mail.txt`
- `chown -R opendkim:opendkim …`, appends to `signing.table` / `key.table` / `trusted.hosts`, restarts opendkim
- reads `mail.txt`, parses the public key (`parseDKIMPublicKey`, `email_service.go:1611`), and publishes it as a `TXT` at `mail._domainkey` in PowerDNS + Mongo.
Selector is always `mail`. Subdomains reuse the parent's key (`SetupSubdomainMail`, `dns_service.go:1218`).

### Migration / DNS transfer feature
- **Export from source** is done over SSH with `pdnsutil list-zone <domain>` (`agent.ExportDNSZoneFromRemote`, `backend/internal/agent/transfer.go:881-887`) — i.e. the source is read via CLI, not its HTTP API.
- **Import on dest** (`transfer_service.go:1757-1992`, "Transfer DNS Zones" step): for each zone it does `pdnsutil delete-zone` then `create-zone`, stamps the dest's own SOA + `dns1..dns4.betazeninfotech.com` NS, then re-adds every non-SOA/non-apex-NS record via `pdnsutil add-record`, converting FQDN→relative names, rewriting A records that match the detected source IP to the dest IP, and rewriting `ip4:` tokens in `v=spf1` TXT. After all zones it runs **`systemctl restart pdns`** (one-time, to flush cache after delete+recreate). Records are mirrored into Mongo `dns_zones`/`dns_records`.
- **Source repoint** (`transfer_panel_records.go:2012`, `repointSourceDNSToDestination`): rewrites the SOURCE's A/SPF records to the dest IP so split-delegation (dns1/2→src, dns3/4→dest) doesn't half-serve the old IP.
- **Dest IP sweep** (`ConfigService.ReassignServerIP`) re-stamps any leftover stale A/SPF/NS/SOA on the dest.

---

## 3. Live evidence

### 3.1 PowerDNS version / backend (identical S1 & S2)
```
$ pdns_server --version
PowerDNS Authoritative Server 4.8.3 (C) 2001-2022 PowerDNS.COM BV
Features: … lua lua-records PKCS#11 protobuf sodium curl scrypt
$ dpkg -l | grep -iE 'pdns|powerdns'
ii pdns-backend-bind 4.8.3-4build3
ii pdns-backend-sqlite3 4.8.3-4build3
ii pdns-server 4.8.3-4build3
```
Note: the `pdns-backend-bind` package is installed but **not launched** — `launch=gsqlite3`. `/etc/powerdns/named.conf` exists but is the Debian default stub (only `include "…/supermaster.conf"` + commented examples); it has no effect under the sqlite3 backend.

### 3.2 `/etc/powerdns/pdns.conf` (non-comment lines)
**S1 (89.116.34.207):**
```
setgid=pdns
setuid=pdns
launch=gsqlite3
gsqlite3-database=/var/lib/powerdns/pdns.sqlite3
local-address=0.0.0.0
local-port=53
api=yes
api-key=9ec489b29e1f059d0a10be5a87522ae6
webserver=yes
webserver-address=127.0.0.1
webserver-port=8081
webserver-allow-from=127.0.0.1
```
**S2 (195.35.7.64):** byte-identical except `api-key=b69feb95961e23b9f26ab7f2f49ace84`.
`/etc/powerdns/pdns.d/` is **empty** on both — no overrides.

### 3.3 Listening sockets (both)
```
udp UNCONN  0.0.0.0:53     pdns_server
tcp LISTEN  0.0.0.0:53     pdns_server
tcp LISTEN  127.0.0.1:8081 pdns_server     # API/webserver, localhost-only
```

### 3.4 Zone/record counts — CLI, SQLite, API, panel, Mongo all agree on ZERO (both)
```
$ pdnsutil list-all-zones        # (empty), exit=0
$ sqlite3 /var/lib/powerdns/pdns.sqlite3 "SELECT COUNT(*) FROM domains;"   -> 0
$ sqlite3 …                       "SELECT COUNT(*) FROM records;"   -> 0
$ sqlite3 …                       "SELECT COUNT(*) FROM cryptokeys;"-> 0   # no DNSSEC
$ curl -s -H "X-API-Key:…" http://127.0.0.1:8081/api/v1/servers/localhost/zones  -> []
$ curl … POST /api/v1/auth/login … ; GET /api/v1/whm/dns/zones
  {"success":true,"data":[]}
$ mongosh "$MONGO_URI" --eval '…'  -> dns_zones=0 dns_records=0 domains=0
```
SQLite `records` schema present and standard (`type VARCHAR(10)`, `prio`, `disabled`, `ordername`, `auth`) — confirms the gmysql-equivalent generic schema on sqlite3.

### 3.5 API server identity (both)
```
$ curl -s -H "X-API-Key:…" http://127.0.0.1:8081/api/v1/servers/localhost
{"daemon_type":"authoritative","id":"localhost","version":"4.8.3","zones_url":"…/zones{/zone}", …}
```

### 3.6 Authoritative-only confirmation (both)
```
no pdns_recursor binary ; pdns-recursor not active ; no recursor/allow-recursion directives
$ dig @127.0.0.1 version.bind chaos txt +short   -> "PowerDNS Authoritative Server 4.8.3"
$ dig @127.0.0.1 example.com SOA                  -> status: REFUSED   # no recursion, foreign zone refused
```

### 3.7 Resolver config (both)
```
$ cat /etc/resolv.conf
nameserver 8.8.8.8
nameserver 8.8.4.4
$ systemctl is-active systemd-resolved   -> inactive
```
The box's own stub resolver points at Google DNS (8.8.8.8/8.8.4.4); systemd-resolved is off. PowerDNS authoritative on :53 and the host's recursive lookups via 8.8.8.8 do not conflict (pdns answers only its own zones; the host queries 8.8.8.8 for everything). No local recursor.

### 3.8 OpenDKIM (both)
```
$ systemctl is-active opendkim    -> active   ($ is-enabled -> enabled)
$ which opendkim-genkey           -> /usr/sbin/opendkim-genkey
$ ls /etc/opendkim/keys/          -> (empty — no domains yet)
signing.table: 0 lines ; key.table: 0 lines ; trusted.hosts: 127.0.0.1 / ::1
```
Ready for on-demand key generation; no per-domain material because there are no domains.

### 3.9 Firewall exposure (both)
```
ufw: active ; 53/tcp + 53/udp ALLOW Anywhere (v4+v6)
iptables: --dport 53 tcp/udp ACCEPT ; NO rule referencing 8081 (API stays localhost-only via webserver-address)
```
Port 53 is intentionally public (authoritative NS must be). 8081 is bound to 127.0.0.1 and `webserver-allow-from=127.0.0.1`, so not reachable off-box.

### 3.10 Panel + nameserver defaults (both)
```
$ curl … /api/v1/version   -> {"version":"3.1.109", …}   (process /opt/serverpanel/bin/server)
/opt/serverpanel/.env: SERVER_IP=<box IP> ; MAIL_HOSTNAME=mail.<box IP>   (no DNS/PDNS/NAMESERVER vars)
$ grep dns[0-9]\.betazeninfotech\.com /opt/serverpanel/  -> dns1..dns4.betazeninfotech.com
```

---

## 4. Drift S1 vs S2

| Item | S1 (89.116.34.207) | S2 (195.35.7.64) | Drift |
|---|---|---|---|
| PowerDNS version | 4.8.3 | 4.8.3 | none |
| Backend | gsqlite3 @ /var/lib/powerdns/pdns.sqlite3 | same | none |
| API / webserver | yes / 127.0.0.1:8081 | yes / 127.0.0.1:8081 | none |
| API key | `9ec489b2…22ae6` | `b69feb95…ce84` | expected (per-host secret) |
| `pdns.d/` overrides | empty | empty | none |
| Zones / records | 0 / 0 (CLI=API=sqlite=Mongo=panel) | 0 / 0 | none |
| DNSSEC cryptokeys | 0 | 0 | none |
| Recursor | none (authoritative-only) | none | none |
| resolv.conf | 8.8.8.8 / 8.8.4.4 | 8.8.8.8 / 8.8.4.4 | none |
| OpenDKIM | active+enabled, genkey present, 0 keys | same | none |
| ufw / iptables (53,8081) | 53 public, 8081 localhost | same | none |
| Panel version | 3.1.109 | 3.1.109 | none |
| Default NS | dns1..dns4.betazeninfotech.com | same | none |

**Verdict: no operational drift.** The two boxes are interchangeable for DNS purposes — exactly what you want for a SOURCE→DEST migration demo.

---

## 5. Findings

### F1 (low, both) — PowerDNS HTTP API enabled but unused by the panel
`api=yes` + `api-key` + `webserver` on `127.0.0.1:8081` are live on both boxes, yet the panel drives PowerDNS exclusively via `pdnsutil`. The API is bound localhost-only and `webserver-allow-from=127.0.0.1`, so it is not externally exposed; risk is low. It does, however, leave a full zone-management API reachable to anything that can run as a local user / hit localhost (and the api-key sits in `pdns.conf`, mode `0640 root:pdns`). Recommendation: either adopt the API as the integration (more robust than CLI parsing) or set `api=no`/`webserver=no` to shrink the local surface. **Do not auto-change** — it's harmless and may be intentional headroom.

### F2 (medium, repo) — Record-type aliases `SPF` / `DMARC` / `ALIAS` are accepted at the API but never translated to a real PowerDNS qtype
`models/dns.go:55` validates `Type` against an enum that includes `SPF`, `DMARC`, `ALIAS`. There is no mapping from these aliases to `TXT`/`CNAME` anywhere between the handler and `pdnsutil` (grep of `dns_service.go` + `agent/dns.go` shows the type string is passed straight through to `add-record`/`replace-rrset`). PowerDNS 4.8.3's generic backend will reject `DMARC`/`SPF`/`ALIAS` as unknown qtypes, so `reconcileRRSet` fails and `AddRecord` rolls back the Mongo insert and returns 500. The panel's own auto-mail path is unaffected because it correctly emits `Type:"TXT"`. Impact for the demo: do **not** seed demo records using the `SPF`/`DMARC`/`ALIAS` types — author DKIM/SPF/DMARC as `TXT` (names `mail._domainkey`, `@`, `_dmarc`). Recommendation: add an alias→TXT/CNAME translation in `AddRecord`/`formatRecordValueForPDNS`, or drop the aliases from the enum. Not auto-fixable safely (changes behaviour).

### F3 (low, both) — Default nameservers (`dns1..dns4.betazeninfotech.com`) are not delegated to these IPs
Hard-coded in `agent/dns.go:28` and `transfer_service.go:1767`. Zones created/migrated will carry NS = dns1..dns4.betazeninfotech.com, which do not point at 89.116.34.207 / 195.35.7.64. For internal demo data and on-box `dig @127.0.0.1` this is fine; for real public resolution the operator must either delegate those NS hostnames to these IPs or override `Nameservers` per zone at create time (`CreateZoneRequest.Nameservers`). Informational for migration planning.

### F4 (info, both) — Migration DNS path is consistent and CLI-based end to end
Source export = `pdnsutil list-zone` over SSH; dest import = `delete-zone`+`create-zone`+`add-record` then one `systemctl restart pdns`; SPF/A IP-rewrite and FQDN→relative normalization are handled. Because both boxes are identical PowerDNS 4.8.3/gsqlite3 with 0 existing zones, a SOURCE→DEST DNS transfer has no backend/version impedance. The transfer also writes zones into Mongo, so the dest panel UI will reflect them.

---

## 6. Demo-data & migration planning notes

- Create zones through the panel (`POST /whm/dns/zones` with `domain`, `server_ip`, optional `admin_email`, `nameservers`) — this triggers the full mail-DNS + DKIM bootstrap (A/MX/SPF/DKIM/DMARC + opendkim key) automatically. Bootstrap TTLs are deliberately low (A=30s, others=60s).
- For extra records, use real qtypes (`A/AAAA/MX/TXT/CNAME/CAA/NS/SRV`); express SPF/DKIM/DMARC as **TXT** (see F2).
- After seeding, both panels' `GET /whm/dns/zones` and `pdnsutil list-all-zones`/SQLite should show the new zones; verify with `dig @127.0.0.1 <zone> SOA`.
- A SOURCE→DEST migration of DNS is low-risk given identical stacks; expect the dest to end with `dns1..dns4.betazeninfotech.com` NS and dest-IP-rewritten A/SPF.
