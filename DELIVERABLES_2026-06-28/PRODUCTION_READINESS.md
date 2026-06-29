# Production Readiness Checklist — Betazen Server Panel v3.1.108

Legend: [x] done/verified · [~] done on S1, pending PROD/S2 · [ ] operator action needed

## Application
- [x] Email-log captures ALL sources (webmail/SMTP/API/3rd-party clients) — verified S1
- [x] MongoDB tenant DB creation works (create/delete lifecycle) — verified S1
- [x] Mailbox create + IMAP retrieval + webmail SSO — verified S1
- [x] Auth: login/refresh/RBAC, snake_case tokens, tampered-JWT rejected — verified S1
- [~] Mail-log UI tab in WHM/cPanel (backend API live; front-end tab is remaining UI work)
- [~] Deploy v3.1.108 to S2 and PROD (runbook ready, backups taken)

## Security
- [x] SQL injection in MySQL provisioning — FIXED (identifier whitelist + value escaping)
- [x] SSRF on outbound webhooks/notifications — FIXED (private-IP guard)
- [x] Rate limiting per-IP behind nginx — FIXED (Fiber proxy-aware)
- [x] Refresh-token honors lock/soft-delete — FIXED
- [x] nginx security headers + server_tokens off — applied S1
- [x] FTP requires TLS (pure-ftpd TLS=2) — applied S1
- [x] Secrets hygiene: .env 0600, none in git — verified
- [ ] **Panel + webmail TLS (HTTPS)** — needs a real FQDN + Let's Encrypt (IP-only can't). PROD
      has `panel.betazeninfotech.com` → enable/verify 443 + HSTS there.
- [ ] **SSH hardening** — deploy admin key, then `PermitRootLogin prohibit-password` +
      `PasswordAuthentication no` + `MaxAuthTries 3` (do LAST; will require key-based automation)
- [ ] CORS: restrict `AllowOrigins` from `*` to the panel host(s)
- [ ] systemd hardening of `serverpanel.service` (NoNewPrivileges, Protect*, RestrictSUIDSGID)
- [ ] Bind Go API to 127.0.0.1 (add SERVER_HOST; UFW is the current compensating control)
- [ ] Agent: implement documented mTLS on :8443 or correct the docs; widen 128-bit aux keys

## Infrastructure (apply to all hosts; done on S1)
- [x] Swap (8 GiB) + swappiness/vfs_cache_pressure
- [x] THP=never before mongod
- [x] Security updates applied + unattended-upgrades tuned
- [x] Dead services removed/masked; apt cache cleaned
- [ ] IPv6 prefix /48→/64 in netplan (human-reviewed; lose-remote-access risk)

## Mail (latent until domains exist; recommend before heavy mail use)
- [ ] Wire SpamAssassin into Postfix (spamass-milter) — spamd runs but scans nothing
- [ ] Install + wire OpenDMARC (panel publishes DMARC but doesn't enforce inbound)
- [ ] Replace snake-oil SMTP/IMAP cert with LE cert for a real mail FQDN
- [ ] Align size limits (php upload_max_filesize 2M ≪ postfix 10M)
- [ ] Sieve `.svbin` precompiled (avoids dovecot ProtectSystem read-only warning) — done S1

## DNS (design decisions)
- [ ] **Authority model**: `dns1-4.betazeninfotech.com` → 195.35.7.161 only; zones served on
      other boxes are lame-delegated. Either glue NS to each serving IP, or AXFR/notify to .161.
- [x] PowerDNS SOA auto-serial (`default-soa-edit=INCEPTION-INCREMENT`) — applied S1
- [ ] Wire DNSSEC (code exists, never called); set real PTR for mail FQDN at provider

## Data / DR
- [x] PROD backups taken pre-deploy (mongodump, mysqldump, binary, .env, configs)
- [x] Each deploy keeps `bin/server.bak.<ts>`; source revertible via git
- [ ] Establish a recurring backup schedule (panel BACKUP_DIR + offsite)
