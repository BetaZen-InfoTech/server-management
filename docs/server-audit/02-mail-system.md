# 02 — Mail System Audit (Server 1, 89.116.34.207)

**Host:** `srv1785162.hstgr.cloud` · Ubuntu 24.04.4 LTS · Public IP 195.35.7.64
**Audited:** 2026-06-29 · **Mode:** read-only (demo box). No changes applied.
**Stack:** Postfix 3.8.6 · Dovecot 2.3.21 (Pigeonhole 0.5.21) · OpenDKIM (Mode sv) · SpamAssassin/spamd · Roundcube 1.6.6 · PowerDNS (authoritative, `.local`)

## Summary

Mail system is **healthy and fully functional**. The Postfix queue is **empty** (0 messages, no deferred). All 7 supporting services are active. Mailbox/forwarder/domain counts are **perfectly consistent** across MongoDB, Dovecot passwd-file, and Postfix virtual maps (42 mailboxes, 36 forwarders, 6 domains — zero drift). **SPF, DKIM, DMARC, MX and the `mail.` A record are present and correct for all 6 domains**, and the on-disk DKIM public key matches the published DNS TXT record (verified MATCH). OpenDKIM is actively signing outbound mail (confirmed in mail.log). The panel's mail-log ingestor is tailing `/var/log/mail.log` and populating `mail_logs`.

Notable gaps (none break mail flow): **SpamAssassin runs but is not wired into the mail path** (no milter / content_filter), **Dovecot has no quota plugin**, **Dovecot allows plaintext auth** (`disable_plaintext_auth = no`), **Postfix + Dovecot TLS still use the snakeoil self-signed cert**, webmail **PHP upload limit (2M) is far below the SMTP message size limit (10M)**, and two cosmetic config quirks (unexpanded `${PANEL_DOMAIN}` in `postmaster_address`; doubled `mail.mail.<domain>` MX host — which still resolves correctly).

## Findings

| Area | Status | Detail |
|------|--------|--------|
| Postfix version | OK | 3.8.6 |
| Postfix queue | OK | **Empty** — `postqueue -p` = "Mail queue is empty", 0 deferred |
| Submission (587) | OK | `smtpd_tls_security_level=encrypt`, `smtpd_sasl_auth_enable=yes`, client/recipient restrictions = `permit_sasl_authenticated,reject[_unauth_destination]` |
| SMTPS (465) | OK | `smtpd_tls_wrappermode=yes` + same SASL/reject restrictions |
| SMTP (25) | OK | `smtpd_relay_restrictions = permit_mynetworks permit_sasl_authenticated defer_unauth_destination`; `smtpd_tls_auth_only=yes` |
| Postfix TLS cert | WARN | Uses `ssl-cert-snakeoil.pem` (self-signed) for `smtpd_tls_cert_file` |
| header_checks | OK | `regexp:/etc/postfix/header_checks_betazen` — WARN-only on `^Subject:` and `^Content-Type:` (logs, never rejects/alters). Installed by panel; confirmed in serverpanel log. |
| message_size_limit | OK | 10240000 (10 MB); `virtual_mailbox_limit=51200000`; `mailbox_size_limit=0` (unlimited) |
| Virtual transport | OK | `lmtp:unix:private/dovecot-lmtp` → Dovecot LMTP |
| Dovecot protocols | OK | `imap pop3 lmtp` (143/993/110/995 + LMTP) |
| Dovecot auth | OK | passwd-file `/etc/dovecot/users`, `scheme=SHA512-CRYPT`, `username_format=%u` |
| Dovecot plaintext auth | WARN | `disable_plaintext_auth = no` — plaintext AUTH permitted even without STARTTLS |
| Dovecot TLS cert | WARN | `ssl_cert = </etc/dovecot/private/dovecot.pem` (self-signed) |
| Dovecot quota | INFO | No quota plugin loaded (only `lmtp_rcpt_check_quota=no`, `quota_full_tempfail=no` stubs) — mailboxes effectively unlimited |
| Sieve | OK | Pigeonhole active for lmtp+lda; global `sieve_after = /etc/dovecot/sieve/after.d` with compiled `10-betazen-hook.sieve`/`.svbin` |
| postmaster_address | INFO | `postmaster@${PANEL_DOMAIN}` — variable not expanded (cosmetic) |
| Auth consistency | OK | **42 = 42 = 42** — Mongo `mailboxes` (42) == `/etc/dovecot/users` (42) == `virtual_mailbox_maps` (42). No drift. |
| Forwarder consistency | OK | Mongo `email_forwarders` (36) == `virtual_alias_maps` (36) |
| Domain consistency | OK | Mongo `domains` (6) == `virtual_mailbox_domains` (6); per-domain user counts: demo-one 8, demo-two 8, company-demo 7, examplemail 7, internal.demo 6, testing-domain 6 |
| Catch-all | INFO | No catch-all (`@domain`) entries. `all@<domain>` are explicit distribution aliases (→ admin+support+sales), not catch-alls |
| Chained forwarder | OK | Spot-check: `contact-us@<domain>` → `contact@<domain>, help@<domain>` (multi-target). `helpdesk@<domain>` → `support@<domain>` (single hop). Resolves correctly. |
| OpenDKIM service | OK | `active`; Mode `sv`, Canonicalization `relaxed/simple` |
| OpenDKIM keys | OK | Per-domain RSA key dirs under `/etc/opendkim/keys/<domain>/{mail.private,mail.txt}` for all 6 domains; selector `mail` |
| OpenDKIM tables | OK (msg) | `key.table` + `signing.table` (refile). **Each domain listed twice** (duplicate lines) — harmless dedup-on-load, but messy |
| OpenDKIM socket | OK | `local:/var/spool/postfix/opendkim/opendkim.sock`; Postfix milters `local:/opendkim/opendkim.sock` resolve to the same path via the chrooted smtpd. DKIM signing confirmed working. |
| DKIM signing (live) | OK | mail.log: `opendkim ... DKIM-Signature field added (s=mail, d=company-demo.local)` and `... d=mail.demo-one.local` |
| DKIM disk==DNS | OK | company-demo.local public key on disk matches published `mail._domainkey` TXT (first 40 chars compared → **MATCH**) |
| SPF (all 6) | OK | All present. `v=spf1 ... ip4:195.35.7.64 ~all` (demo-one/company-demo/testing-domain also include `a mx`) |
| DKIM TXT (all 6) | OK | All resolve `v=DKIM1; h=sha256; k=rsa; p=...` at `mail._domainkey.<domain>` |
| DMARC (all 6) | OK | All present. `p=none` (demo-one, demo-two, testing-domain → rua admin@); `p=quarantine` (company-demo, examplemail, internal.demo → rua dmarc@) |
| MX (all 6) | OK | `10 mail.<domain>`; demo-one/demo-two render as `mail.mail.demo-…` (domain literally starts with `mail.`) — **still resolves** to 195.35.7.64 |
| `mail.<domain>` A | OK | All 6 → 195.35.7.64 (incl. `mail.mail.demo-one.local` A → 195.35.7.64) |
| SpamAssassin | WARN | `spamd` running (`/usr/sbin/spamd --max-children 5`), but **NOT wired into mail flow** — no spamass-milter in `smtpd_milters`, no `spamc`/content_filter in master.cf. Mail is not scanned. |
| Roundcube webmail | OK | 1.6.6 (`1.6.6+dfsg-2ubuntu0.1`), served at `/webmail/` (alias `/var/lib/roundcube/public_html/`) from the `serverpanel` nginx vhost; PHP-FPM; plugins `archive`, `zipdownload` |
| Roundcube DB | OK | MariaDB `roundcube` db reachable; `users`=0 (no webmail logins yet — expected on fresh demo) |
| Webmail upload limits | WARN | PHP `upload_max_filesize=2M`, `post_max_size=8M` (php 8.2 fpm) vs Postfix `message_size_limit=10M`. Webmail attachments capped at **2M** — well below SMTP allowance. |
| Mail-log ingestor | OK | serverpanel process `tail -n 5000 -F /var/log/mail.log` (PID 90897) running; log: "ingestor started (capturing ALL mail, every source)". `mail_logs`=8 → **webmail:4, smtp-client:2, api-local:2** |
| Bounce/retry | OK | Default Postfix deferred-queue retry (no custom override); queue empty, nothing deferred |
| Services | OK | postfix, dovecot, opendkim, spamd, pdns, nginx, mariadb — all `active` |

## Issues (by severity)

| # | Severity | Issue | Detail |
|---|----------|-------|--------|
| 1 | Medium | SpamAssassin not in mail path | `spamd` is up but no milter/content_filter wires it to Postfix — inbound mail is never spam-scored. The running daemon gives a false sense of protection. |
| 2 | Medium | Self-signed TLS on Postfix + Dovecot | smtpd/Dovecot present the snakeoil cert. Real external clients get cert warnings / can't validate. Acceptable for a `.local` demo; would need a real cert in prod. |
| 3 | Low | Plaintext auth allowed | Dovecot `disable_plaintext_auth = no`. Submission/SMTPS already force TLS, but IMAP/POP3 plaintext logins without STARTTLS are accepted. |
| 4 | Low | Webmail upload << SMTP limit | PHP 2M upload vs Postfix 10M `message_size_limit`. Users can't attach files between 2–10M via Roundcube. |
| 5 | Low | No Dovecot quota | No quota plugin → mailboxes are unbounded; a runaway sender/recipient could fill the disk. |
| 6 | Cosmetic | Duplicate DKIM table entries | Every domain appears twice in `/etc/opendkim/key.table` and `signing.table`. Harmless (last wins / dedup), but should be cleaned. |
| 7 | Cosmetic | Unexpanded `${PANEL_DOMAIN}` | `postmaster_address = postmaster@${PANEL_DOMAIN}` literal in doveconf. |
| 8 | Cosmetic | Doubled MX host | `mail.demo-one.local`/`mail.demo-two.local` MX = `mail.mail.demo-…` (domain starts with `mail.`). Resolves fine; just ugly. |

## Fixes applied

**None.** Per the read-only demo-box mandate, no change was trivially-safe-AND-reversible without risk to mail flow. All issues above are left as recommendations.

## Recommendations

1. **Wire SpamAssassin into Postfix** (Medium): install `spamass-milter` and add it to `smtpd_milters` alongside OpenDKIM, or run `spamc` via a content_filter / Dovecot sieve. Currently `spamd` burns CPU for nothing.
2. **Replace snakeoil TLS** with the panel's Let's Encrypt cert for `smtpd_tls_cert_file`/`smtpd_tls_key_file` and Dovecot `ssl_cert`/`ssl_key` once the mail host has a public, certbot-eligible name.
3. **Harden Dovecot auth**: set `disable_plaintext_auth = yes` (clients already use TLS on 993/995/587/465). Verify no plaintext-143/110 client depends on it first.
4. **Align upload limits**: raise PHP `upload_max_filesize`/`post_max_size` to ≥10–12M to match `message_size_limit`, OR document the 2M webmail cap intentionally.
5. **Add Dovecot quota** plugin with sane per-mailbox defaults to protect disk.
6. **Dedup** the OpenDKIM `key.table`/`signing.table` (one line per domain).
7. **Cosmetic**: expand `${PANEL_DOMAIN}` in `postmaster_address`; consider a non-doubled MX label for the `mail.*` demo domains.
