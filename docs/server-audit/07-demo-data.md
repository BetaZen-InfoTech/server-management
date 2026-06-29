# 07 — Demo Data Generation Report

**Date:** 2026-06-29  **Source box:** Server 1 `89.116.34.207`

This is a **demo environment** (no production data). A realistic dataset was already
present from the initial provisioning run (2026-06-28); this pass **verified** it and
**topped it up** with fresh, multi-source mail traffic. Nothing was destructively
regenerated — existing, correct data was left in place per the engagement's
"don't change working modules" rule.

## 1. Inventory verified present (Server 1)

| Category | Count | Detail |
|----------|-------|--------|
| Vendor/owner accounts | 5 users | `admin@betazeninfotech.com` (vendor_owner) · `demo@betazeninfotech.com` (customer) · `owner@demo-one.local`, `owner@demo-two.local`, `owner@internaldemo.local` (vendor_admin) |
| Demo domains | 6 | `mail.demo-one.local`, `company-demo.local`, `mail.demo-two.local`, `examplemail.local`, `testing-domain.local`, `internal.demo.local` |
| DNS zones / records | 6 / 119 | PowerDNS (gsqlite3) authoritative + Mongo mirror; each zone carries SOA, NS, A, AAAA, MX, SPF, DKIM (`mail` selector), DMARC, TXT, CNAME (www/webmail), CAA |
| Mailboxes | 42 | `admin/support/sales/billing/info/accounts/ceo/marketing@…` across the 6 domains; varied quotas + send limits; shared demo password `M@ilbox2026!` |
| Forwarders | 36 | incl. **6 chained** (`enquiries@… → helpdesk@…`, which itself forwards) and multi-target (`contact-us → contact + help`) — verified live |
| FTP accounts | 6 | one per domain |
| Apps | 6 | `demo-crm` (Go), `demo-erp` (Go), `demo-cms`, `flask-sample` (Python/gunicorn), `node-sample` (Node), 1 static site |
| Background services | 6 | `betazen-demo-{api,monitor,queue,scheduler,web,worker}` (systemd, active+enabled) |
| MariaDB | 2 DBs | `demotwo_appdb` (8 tables: users, companies, mailboxes, domains, settings, permissions, logs, notifications — all InnoDB+PK, utf8mb4, realistic rows) · `roundcube` (17-table webmail schema) |
| MongoDB | `serverpanel` (29 collections) + `demoone_appdata` | panel brain + a demo app DB |
| Webmail | Roundcube 1.6.6 | served at `/webmail/`, IMAP/submission reachable |

This satisfies every item the brief requested (5–6 domains, 30–50 mailboxes, chained
forwarders, full DNS record set, 5–6 apps, 5–6 services, demo MongoDB + SQL with the
named collections/tables).

## 2. Top-up performed this run

**Multi-source mail traffic** was generated to exercise — and visibly demonstrate — the
source-agnostic mail-log ingestor. A batch of authenticated and local-injected messages
(plain, HTML, unicode, with-attachment, multi-recipient) was sent between demo mailboxes
via three distinct paths:

- `:587` on the public IP → classified **`smtp-client`** (Thunderbird/Outlook/mobile/app style)
- `:587` on loopback → classified **`webmail`**
- local `sendmail` → classified **`api-local`**

Result — `mail_logs` now spans **all four sources**:

| source | count |
|--------|-------|
| webmail | 6 |
| smtp-client | 5 |
| api-local | 4 |
| inbound-smtp | 1 |
| **total** | **16** |

(Two planned sends failed only because `marketing@company-demo.local` is not a provisioned
mailbox — a test-address choice, not a system fault.)

## 3. Credentials in use (demo only — rotate before any real use)

| Surface | Login | Password |
|---------|-------|----------|
| WHM owner | `admin@betazeninfotech.com` | `Betazen!Demo-2026#Kx9pQ2` |
| Demo vendors | `owner@demo-one.local` / `owner@demo-two.local` / `owner@internaldemo.local` | `Demo!Pass2026` |
| Demo mailboxes | all 42 | `M@ilbox2026!` |

## 4. Notes

- The `.local` TLD is intentional (isolated demo); these domains are **not** internet-resolvable,
  so external deliverability/DKIM-verification by third parties is out of scope by design.
- All demo data is mirrored to Server 2 via the migration (see report 13).
