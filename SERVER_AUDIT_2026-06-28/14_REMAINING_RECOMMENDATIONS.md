# 14 — Remaining Recommendations

Issues intentionally **not** auto-applied (would break access, need a full re-migration to validate,
are behavioural/product decisions, or are pure deliverability constraints of a bare-IP demo).
Ordered by priority.

## Security (apply on a real deployment)

1. **SSH hardening** — both boxes have `PermitRootLogin yes` + `PasswordAuthentication yes`. Not
   changed because this run authenticates over root+password (disabling it mid-run would lock out
   the automation). Recommended: install an admin SSH key, then set `PermitRootLogin prohibit-password`
   and `PasswordAuthentication no`; reconcile the conflicting `sshd_config.d` drop-ins.
2. **TLS for the panel & mail** — currently HTTP-only on a bare IP (no cert). Put a real domain on the
   panel and issue Let's Encrypt (the panel has `POST /config/panel-ssl`); then add HSTS + a CSP.
3. **Bind the panel to loopback** — the Go binary listens on `0.0.0.0:8080` (only UFW hides it).
   Bind `127.0.0.1:8080` since nginx proxies locally (defense-in-depth).
4. **CORS** — `AllowOrigins:"*"` on the admin API; tighten to the panel origin.
5. **Terminal WebSocket** — authenticates via `?token=` in the URL (leaks to logs) and skips the
   suspended-user check; move to a header/short-lived ticket and re-run the `isUserAllowed` check.
6. **FTP** — pure-ftpd allows cleartext (TLS mode 1); set TLS mode 2 to force TLS, or disable FTP.
7. **JWT access TTL** 4 h is long for a root-equivalent panel; consider 30–60 min + refresh.
8. **Command-injection defense-in-depth** — the model-layer validators (added this run) close the
   confirmed sinks; as follow-up, also route remaining `bash -c` interpolations through the existing
   `shellQuote`/`shellQuoteLocal` helpers (or argv-form writes) for belt-and-suspenders.

## Migration (code fixes to validate with a fresh re-migration)

9. **B-06 duplicate username** — in panel-records user sync, dedupe by `username` (update the existing
   row instead of inserting), or skip the `<user>@localhost` placeholder when `server_type=serverpanel`.
   The placeholder is created in `transfer_service.go` ("Transfer Domains & Files") before the real
   account syncs.
10. **B-07 unscoped `$unset encrypted_pass`** — scope `reencryptSyncedMailboxes`'
    `Find`/`UpdateMany` to the transfer's owned domains so a source with an unreadable `JWT_SECRET`
    can't wipe webmail-SSO ciphertext for unrelated destination tenants.
11. **B-11 ingestor restart gap** — persist the mail.log offset/inode (or back-scan the tail on
    startup) so mail logged during panel downtime is still ingested (idempotent via `log_key`).
12. **Mongo DB migration** — the native transfer's DB discovery enumerates MySQL only; panel-managed
    **MongoDB** app databases are not auto-migrated (handled manually here). Add Mongo DB
    discovery+transfer to the transfer pipeline for full coverage.
13. **DNS Mongo/PowerDNS sync** — after transfer, the destination serves all 114 records but the
    `dns_records` Mongo collection holds 84 (auto SOA/NS/apex not row-mirrored). Cosmetic, but the
    panel UI under-reports; reconcile the collection from PowerDNS post-transfer.

## Mail Suite (only if you intend to deploy it)

14. **B-10 service-token auth** — add `PANEL_SERVICE_TOKEN` handling in mail-suite so the panel's
    Enable-Mail / DNS proxy authenticates (currently the user-JWT middleware 401s the service token).
15. Mail Suite stores attached-mailbox IMAP/SMTP passwords in plaintext and falls back to a hardcoded
    `JWT_SECRET`; FCM push is a stub. Resolve before any deployment. (See `reports/mail-suite.md`.)

## Operations / hardening

16. **Backups** — no automated backups exist for Mongo, MariaDB, or maildirs on either server. Add
    scheduled `mongodump` + `mysqldump` + maildir snapshots (the panel has a Backups feature + the
    `BACKUP_*` env is configured but unused).
17. **Swap** — 0 swap on 31 GB. Cap MongoDB WiredTiger `cacheSizeGB` (defaults to ~15.5 GB) and/or add
    a few GB of swap to avoid OOM under load.
18. **Performance at scale** — add compound `filter+sort` indexes on `mail_logs`/`audit_logs`/`domains`;
    avoid unanchored `$regex` searches; raise MariaDB `innodb_buffer_pool_size` (stock 128 MB on 32 GB);
    add `gzip_types` to nginx. (See `reports/performance.md`.)
19. **SpamAssassin** runs but isn't wired into Postfix (no milter/content_filter); wire it (or rspamd)
    + add ClamAV if inbound scanning is required.
20. **External email deliverability** — a bare IP with no PTR/aligned SPF-DKIM-DMARC for a real domain
    will be spam-foldered by Gmail/Outlook/Yahoo/Proton regardless of correct config. Use a real
    domain + reverse DNS (or a smarthost/relay) for production mail.

## Cannot be validated from this environment (stated for transparency)

- Real inbox delivery to Gmail/Outlook/Yahoo/Proton (no controlled external mailboxes; SMTP path to
  Gmail was proven — it connected and returned a live `550` for a deliberately fake address).
- Native Thunderbird/Outlook-desktop/mobile **GUI** clients — simulated via the exact IMAP/SMTP
  protocols those clients use (auth submission + IMAP/POP3 verified).
- Full webmail/SPA **click-through UI** — verified at the IMAP/HTTP/API layer + login-page render;
  pixel-level UI needs a browser.
