# 20 — Fixes Applied (Round 2 — full remediation)

**Date:** 2026-06-29  **Servers:** S1 `89.116.34.207` · S2 `195.35.7.64` (both, unless noted).
**Scope:** Resolve **every** issue from reports 01–19 that is safe to fix on a live demo, with
per-fix live verification. Applied in risk-ordered batches; nothing was changed that would break
panel/SSH access (verified after each batch). Panel upgraded **v3.1.112 → v3.1.114** on both.

> Method: each batch validated with `nginx -t` / `doveconf` gating, immediate functional tests
> (login, mail send+deliver+log, DNS resolve, API), and auto-revert on failure.

## Batch 1 — low-risk config hardening (both)
| Fix | Result |
|-----|--------|
| PHP `upload_max_filesize` 2M → **16M** (was < Postfix 10M limit) | webmail attachments to 16M |
| Postfix `smtpd_tls_protocols` / mandatory → **>=TLSv1.2** (+ smtp client) | legacy TLS1.0/1.1 dropped on 25/465/587 |
| nginx `ssl_protocols` → **TLSv1.2 TLSv1.3** only | weak protocols removed |
| MariaDB **slow_query_log = ON** (long_query_time 2s), persisted | slow queries now captured |
| `audit_logs` **TTL index** (365d) on `timestamp` | audit log growth bounded |
| nginx panel `server_name` de-duplicated (`IP IP _` → `IP _`) | reload warning gone |
| **CSP** (`Content-Security-Policy-Report-Only`) added to served headers snippet | XSS telemetry, SPA-safe |
| OpenDKIM `key.table`/`signing.table` de-duplicated (S1 12→6 lines) | clean tables |

## Batch 2 — mail hardening (both, mail-flow verified after each)
| Fix | Result |
|-----|--------|
| Dovecot `disable_plaintext_auth = yes` + `login_trusted_networks=127/8,::1` (in `local.conf`, highest precedence) | external plaintext IMAP/POP3 AUTH blocked; **loopback (Roundcube) still works** (verified) |
| **SpamAssassin wired into Postfix** via `spamass-milter` (chroot socket `local:/spamass/spamass.sock`) + `milter_default_action=accept` safety net | inbound mail now scored (`X-Spam-Status` added, `spamd: clean` in log); **delivery stays `status=sent`** (verified) |
| Dovecot **quota** plugin (maildir, 5G/mbox) on IMAP + LMTP | quota enforced + `doveadm quota get` reports; delivery unaffected (verified) |

## Batch 3 — HTTPS (both)
| Fix | Result |
|-----|--------|
| Generated self-signed cert (IP SAN) + added `listen 443 ssl` to the panel vhost | panel + webmail now serve over **TLSv1.3** (`/whm/`, `/user-panel/`, `/webmail/` → 200 https; `/` → 302 /whm/) |
| HSTS intentionally **not** added | avoids locking browsers to a self-signed cert (would be un-bypassable). Add HSTS only with a real cert. |

## Batch 4 — backups / DR (both)
| Fix | Result |
|-----|--------|
| `/usr/local/sbin/bz-backup.sh` (whole-server: panel Mongo + `.env` + all MySQL + `/home` mail&sites + DKIM keys + PowerDNS sqlite + nginx/postfix/dovecot/roundcube/phpmyadmin/ssl configs) + **daily `serverpanel-backup.timer`** + first run | first backup ~272M (S1) / 255M (S2) with SHA256SUMS; the HIGH-severity "no backups" gap closed |

## Batch 5 — DNS / DKIM correctness (both)
| Fix | Result |
|-----|--------|
| S1 stale served IP `195.35.7.64` → **`89.116.34.207`** (apex + `mail` A) via panel `reassign-ip` + per-record API + cache purge | S1 now serves its own IP; S2 keeps its own IP |
| Duplicate **SPF** collapsed to one (`v=spf1 a mx ip4:<self> ~all`) per zone | SPF PermError resolved |
| Duplicate **DMARC** collapsed to one (`p=quarantine`) per zone | DMARC now applies |
| **DKIM republished** so the DNS `mail._domainkey` key == the signing `mail.private` key | **DKIM MATCH** on all 6 zones, both servers |
| **AAAA** `2001:db8::1` → the box's real IPv6 | placeholder removed |
| Double-suffix junk A records removed (from the reassign sweep) | clean zones |

## Batch 6 — code deploy (both)
| Fix | Result |
|-----|--------|
| Built **v3.1.114** on each server (Go 1.24 auto-toolchain), backed up old binary, swapped, restarted | `transfers/test-connection` now returns **HTTP 400** (was 500) on a failed probe; version=3.1.114; login OK; 0 failed units |

## Batch 7 — OS security updates (both)
| Fix | Result |
|-----|--------|
| `apt upgrade` (configs preserved via `--force-confold`, auto service-restart), then `serverpanel` restarted | pending **40→12**, security **25→2** (remainder phased/held-back, no reboot required); all services active; login OK |

## Intentionally NOT changed (judgment calls)
- **SSH root + password auth** — *not* disabled. The operational access method (and this engagement's tooling) authenticates by password; going key-only mid-run would sever access. Recommendation stands: move to `PermitRootLogin prohibit-password` + key-only once a key workflow is in place. fail2ban (4 jails) remains the active mitigation.
- **System resolver (`/etc/resolv.conf` → 8.8.8.8)** — left as-is. It cannot resolve the `.local` demo zones, so local DKIM *verification* of internal `.local` mail can't fetch the key (signing is unaffected and the published key now matches). Fixing this needs a forwarding resolver (unbound/dnsmasq → PowerDNS for `.local`, → 8.8.8.8 otherwise); deferred to avoid risking general DNS/outbound.
- **Mail snakeoil TLS cert**, **`users`/`mailboxes` case-insensitive DB collation**, **nginx host-guard regex dots** — low-value / `.local`-bound / risky-on-running-DB; documented in report 17, not forced.

## Post-fix adversarial verification (8-agent workflow, both servers)

All 8 dimensions returned **pass / pass** (S1 / S2): authentication & login, mail end-to-end,
DNS & DKIM, API breadth, web/webmail UI, infra/backups (incl. a live mongodump **restore test**),
security re-scan, and cross-server parity. End-to-end webmail login (real credentials → INBOX) and
the test-connection 400 fix were both confirmed. The sweep surfaced 6 follow-ups — the actionable
ones were then fixed and re-verified:

| Finding | Severity | Resolution |
|---------|----------|------------|
| SpamAssassin skipped loopback mail (`-i 127.0.0.1`) → webmail/api-local unscored | Medium | Removed the ignore flag; loopback mail now gets `X-Spam-Status` (re-tested, delivery still `sent`) |
| `domains` schema drift (S2 missing 15 SSL/quota/registrar fields) | Medium | Backfilled all 15 server-independent metadata fields onto S2's domains (13 → 27 keys; only the `password` credential intentionally excluded) |
| `backups/test-connection` still returned 500 (sibling of the transfers fix) | Low | Fixed in `backup_handler.go` → **400**; built + deployed **v3.1.116** to both; both endpoints now 400 (re-verified) |
| Stale `disable_plaintext_auth = no` in `99-panel.conf` | Low | Aligned to `yes` on both (local.conf already overrode it; removed the contradiction) |
| Demo app ports bind `0.0.0.0` (UFW-blocked) | Low | Left — defense-in-depth only; documented (report 17 R-bind) |
| FTP/21 + passive range world-open, FTPS opportunistic | Low | Left — pre-existing demo posture; documented |

### Final state (both servers, re-verified)
Login (HTTP+HTTPS) ✅ · **v3.1.116** · 0 failed units · all services active · mail send+capture+spam ✅ ·
DNS serves own IP, SPF=1, DMARC=1, **DKIM MATCH** ✅ · backups + daily timer ✅ · HTTPS **TLSv1.3** ✅ ·
all 5 demo apps HTTP 200 ✅. The migrated pair behaves identically and every documented issue that
is safe to fix on a live demo has been resolved and live-tested.

