# 7 — Demo Data Generation Report

**Target:** Server 1 (`89.116.34.207`). Generated via the panel REST API (so every entity is
panel-managed and migratable) plus direct provisioning for standalone systemd services.
All entities created with clean, shell-safe names (also exercising the new input validation).

## Accounts (3 vendor_admin, with real Linux users + /home)

| Username | Email | Role | Linux user/home |
|----------|-------|------|-----------------|
| demoone | owner@demo-one.local | vendor_admin | ✅ /home/demoone |
| demotwo | owner@demo-two.local | vendor_admin | ✅ /home/demotwo |
| internaldemo | owner@internaldemo.local | vendor_admin | ✅ /home/internaldemo |

Password: `Demo!Pass2026`. Default hosting package assigned.

## Domains (6) — each with nginx vhost + DNS zone + mail setup

`mail.demo-one.local`, `company-demo.local` (demoone) · `mail.demo-two.local`,
`examplemail.local` (demotwo) · `testing-domain.local`, `internal.demo.local` (internaldemo).

> SSL: Let's Encrypt correctly refused (`.local` is not a public-suffix TLD) — expected for demo
> domains, not a fault. Domains were created successfully regardless.

## DNS records — full type coverage per zone (114 records total across 6 zones)

Every required record type is present and served by PowerDNS: **A, AAAA, MX, SPF (TXT), DKIM
(`mail._domainkey` 2048-bit RSA + opendkim signing table), DMARC (`_dmarc` TXT), TXT, CNAME
(`www`/`webmail`), CAA, NS, SOA**. DKIM keys generated for all 6 domains.

## Mailboxes (42) — varied quotas & send limits

7–8 mailboxes per domain from {admin, support, sales, billing, info, accounts, ceo, marketing,
help, contact}. Quotas vary 512 MB–10 GB; hourly send limits vary 100–500. Stored in Mongo +
`/etc/dovecot/users` (SHA512-CRYPT) + Postfix virtual maps (all consistent, 42 = 42 = 42).
Mailbox password: `M@ilbox2026!`.

## Forwarders (36) — including one-to-many and chained

Per domain: `helpdesk→support`, `sales-team→marketing`, `billing-dept→accounts`,
`all→{admin,support,sales}` (one-to-many), `enquiries→helpdesk` (**chain**:
enquiries→helpdesk→support), `contact-us→{contact,help}`. Wired into Postfix
`virtual_alias_maps` (36 = 36). Delivery verified in Phase 5.

## Applications (6) — deployed via the panel, reverse-proxied per domain

| App | Type/Framework | Domain | Runtime state |
|-----|----------------|--------|---------------|
| demo-erp | Go / go-fiber | mail.demo-one.local | systemd `sp-app-demo-erp` running :8091 |
| demo-crm | Node / express | company-demo.local | running :3102 |
| node-sample | Node / express | examplemail.local | running :3101 |
| demo-cms | Node / express | internal.demo.local | running :3103 |
| flask-sample | Python / Flask+gunicorn | testing-domain.local | running :5101 |
| static-site | Static / react-vite | mail.demo-two.local | static (nginx-served, no daemon) |

Each dynamic app is a managed systemd service with journald logging + nginx reverse proxy; all
respond `200` with a JSON health payload through their domain vhost.
(Config fix applied: Go was installed at `/opt/go` but not on the build PATH — symlinked to
`/usr/local/{go,bin/go}` so panel Go builds work.)

## Services (6) — standalone systemd demo services

`betazen-demo-{worker,queue,scheduler,monitor,api,web}` under `/opt/betazen-demo/`. All
**active + enabled**, restart-tested, journald logging verified; api/web expose `/health` (200).
Demonstrates the worker/queue/scheduler/monitoring/api/web service types with
startup/restart/logging/health.

A Deploy-Software project (`betazen-demo-platform`) was also created (its git-backed services
require a repo URL — documented, see `14`).

## Demo MongoDB database — `demoone_appdata` (panel-managed)

8 collections, **109 documents**: users (12), companies (6), domains (6), mailboxes (15),
mail_logs (25), audit_logs (25), notifications (12), settings (8). User `demoone_appmongo`.

## Demo SQL database — `demotwo_appdb` (panel-managed MySQL)

8 tables with realistic, FK-linked rows: companies (6), users (10), domains (6), mailboxes (8),
settings (8), permissions (10), logs (6), notifications (6). User `demotwo_appsql`.

## Verification

Mongo/Dovecot/Postfix/PowerDNS counts are mutually consistent (e.g. 42 mailbox rows = 42 dovecot
users; 36 forwarder rows = 36 postfix aliases; 6 zones served with 114 records). All demo data
subsequently migrated to Server 2 (see `10`/`11`).
