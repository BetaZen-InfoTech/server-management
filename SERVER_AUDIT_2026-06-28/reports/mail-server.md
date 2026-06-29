# Mail Server Audit — Postfix + Dovecot + OpenDKIM + SpamAssassin + Roundcube

**Auditor:** Agent 2 (read-only)
**Date:** 2026-06-28
**Deployed code:** local git repo @ `c:/Users/Administrator/Downloads/Project/server-management` (v3.1.109, rev 466b52e)
**Servers:**
- **Server 1 (S1)** = `89.116.34.207`, hostname `srv1785162.hstgr.cloud` — migration SOURCE
- **Server 2 (S2)** = `195.35.7.64`, hostname `srv1789639.hstgr.cloud` — migration DEST

> Both are Ubuntu 24.04.4 LTS, Postfix 3.8.6, Dovecot 2.3.21, OpenDKIM 2.11.0, SpamAssassin 4.0.0, Roundcube 1.6.6. No Docker. No TLS/443. Snakeoil/self-signed certs only.

---

## Executive Summary

The mail stack (Postfix, Dovecot, OpenDKIM, SpamAssassin, Roundcube) is **installed, running, and config-valid on both servers**, and the auth/TLS data paths verify end-to-end (submission 587 + smtps 465 advertise `AUTH PLAIN LOGIN` after STARTTLS; IMAPS/POP3S up; LMTP + SASL sockets wired to Postfix; OpenDKIM milter socket present). However there are several functional gaps that affect **deliverability and spam protection on BOTH servers**, plus **one server-specific (S2) defect that breaks outbound mail to external domains**.

**One critical drift:** S2's Postfix **chroot resolver is the systemd-resolved stub (`127.0.0.53`)**, which is unreachable from inside the chroot, so the chrooted `smtp(8)` client cannot resolve external MX records → all outbound mail to external recipients will defer/bounce on S2. S1's chroot resolver is correctly `8.8.8.8 / 8.8.4.4` and resolves MX fine.

**Stack-wide gaps (S1 == S2):** SpamAssassin runs but is **not wired into Postfix** (no milter / no `content_filter`) → inbound mail is never spam-scanned; `postmaster_address` in Dovecot is the **unexpanded literal `postmaster@${PANEL_DOMAIN}`**; `message_size_limit` is the stock **10 MB** (the panel code intends 50 MB); fail2ban has **only the `sshd` jail** (no postfix/dovecot/sasl jails); **no Sieve/ManageSieve** (port 4190 not listening); and the bare-IP hostname (`*.hstgr.cloud` default, `/etc/mailname` = the raw IP) means **outbound deliverability and TLS cert names will not pass external scrutiny** until a real domain is pointed at the box.

**Overall health: warnings** (S2 outbound is effectively broken = the one critical item; the rest are stack-wide functional/hardening gaps that are consistent across both clones).

---

## 1. Postfix — `main.cf` (`postconf -n`)

Command: `postconf -n` (both servers). Output is **identical** between S1 and S2 except `myhostname`/`mailname`. Key lines:

```
compatibility_level = 3.6
inet_interfaces = all
inet_protocols = ipv4
myhostname = srv1785162.hstgr.cloud           # S2: srv1789639.hstgr.cloud
mydomain  = hstgr.cloud
myorigin  = /etc/mailname                       # contents: the bare IP (see §8)
mydestination = localhost.$mydomain, localhost
mynetworks = 127.0.0.0/8 [::ffff:127.0.0.0]/104 [::1]/128
header_checks = regexp:/etc/postfix/header_checks_betazen

virtual_mailbox_domains = hash:/etc/postfix/virtual_mailbox_domains
virtual_mailbox_maps    = hash:/etc/postfix/virtual_mailbox_maps
virtual_alias_maps      = hash:/etc/postfix/virtual_alias_maps
virtual_mailbox_base    = /var/mail/vhosts
virtual_minimum_uid     = 100
virtual_uid_maps        = static:5000
virtual_gid_maps        = static:5000
virtual_transport       = lmtp:unix:private/dovecot-lmtp

smtpd_sasl_type = dovecot
smtpd_sasl_path = private/auth
smtpd_sasl_auth_enable = yes
smtpd_sasl_security_options = noanonymous
broken_sasl_auth_clients = yes
smtpd_tls_auth_only = yes
smtpd_relay_restrictions = permit_mynetworks permit_sasl_authenticated defer_unauth_destination

smtpd_milters     = local:/opendkim/opendkim.sock
non_smtpd_milters = local:/opendkim/opendkim.sock
milter_protocol = 6
milter_default_action = accept

smtpd_tls_cert_file = /etc/ssl/certs/ssl-cert-snakeoil.pem
smtpd_tls_key_file  = /etc/ssl/private/ssl-cert-snakeoil.key
smtpd_tls_security_level = may
smtp_tls_security_level  = may

mailbox_size_limit = 0
```

- Matches `install.sh` (step 6/13), **not** `backend/internal/agent/email_install.go` (that path uses `inet:localhost:12301` for the milter and `message_size_limit = 52428800`; the live boxes use install.sh's `local:/opendkim/opendkim.sock` and the stock size). The live deployment is the `install.sh` path.
- `message_size_limit` is **absent** from `postconf -n` → effective value is the Postfix default. Confirmed:
  ```
  default:   message_size_limit = 10240000
  effective: message_size_limit = 10240000      # ~9.77 MB, both servers
  ```
  SMTP `EHLO` advertises `250-SIZE 10240000` on 25/587/465 and Roundcube inherits it. `install.sh` never sets `message_size_limit`; `email_install.go` (unused path) sets 50 MB. **Drift: deployed limit (10 MB) ≠ panel-intended limit (50 MB).**
- `postfix check` → **exit 0** on S1 (clean). On **S2 it emits a warning** (see §7).

`header_checks_betazen` (both servers, identical) — log-only, never rejects:
```
/^Subject:/ WARN
/^Content-Type:/ WARN
```
This is the v3.1.108 "source-agnostic mail log" ingestor hook (surfaces Subject/Content-Type into `/var/log/mail.log`). Benign.

---

## 2. Postfix — `master.cf` (submission 587 / smtps 465)

Command: `postconf -M`. **Identical on both servers.** Submission and smtps are declared via `postconf -M`/`-P` (install.sh lines 1085–1095):

```
submission inet n - y - - smtpd
  -o smtpd_tls_security_level=encrypt
  -o smtpd_sasl_auth_enable=yes
  -o smtpd_client_restrictions=permit_sasl_authenticated,reject
  -o smtpd_recipient_restrictions=permit_sasl_authenticated,reject_unauth_destination
smtps inet n - y - - smtpd
  -o smtpd_tls_wrappermode=yes
  -o smtpd_sasl_auth_enable=yes
  -o smtpd_client_restrictions=permit_sasl_authenticated,reject
  -o smtpd_recipient_restrictions=permit_sasl_authenticated,reject_unauth_destination
```

- Both restrict to `permit_sasl_authenticated,reject` (client) / `permit_sasl_authenticated,reject_unauth_destination` (recipient) → **no open relay** (verified: an unauthenticated relay attempt would hit `reject_unauth_destination`).
- `smtpd_tls_auth_only = yes` means AUTH is hidden until STARTTLS — correct. Plain `EHLO` to 587 shows STARTTLS but **no** `250-AUTH` (this is intended, not a bug).

### Live AUTH/TLS verification (read-only `openssl s_client`)

587 after STARTTLS, and 465 wrappermode — **both servers, identical**:
```
250-AUTH PLAIN LOGIN
250-AUTH=PLAIN LOGIN
```
Cert presented by 587/465: `subject=CN = srv17856..  issuer=CN = srv17856..` (Postfix snakeoil, self-signed, CN = hostname).
IMAPS 993 capability: `... AUTH=PLAIN AUTH=LOGIN` (no `LOGINDISABLED`); cert `subject=CN = srv17856..` (Dovecot's own self-signed).

> SASL auth path (Postfix → Dovecot `private/auth` socket) is fully wired and advertised on both clones. The blocker for actually using it is that **0 mailboxes exist** (empty `/etc/dovecot/users`), consistent with the stated data state.

---

## 3. Virtual mailbox / alias / forwarder storage

**Where mail identities live:** flat files, not MySQL/Mongo for the MTA layer.

- **Mailbox domains / mailboxes:** Postfix `hash:` maps at `/etc/postfix/virtual_mailbox_domains` and `/etc/postfix/virtual_mailbox_maps`.
- **Aliases / forwarders / catch-all:** `/etc/postfix/virtual_alias_maps` (catch-all is expressed here as `@domain → target`).
- **Mailbox passwords (Dovecot passdb + userdb):** `/etc/dovecot/users` passwd-file, `scheme=SHA512-CRYPT`.
- The panel (`EmailService` / `agent/postfix.go`) writes these files at runtime and runs `postmap`; the authoritative records also live in Mongo (`mailboxes`, `email_forwarders`), which the panel reconciles to the files.

Current state — **all empty on both servers** (matches "0 domains/mailboxes/forwarders"):
```
/etc/postfix/virtual_mailbox_domains   size=0 lines=0   (.db present: 12288 bytes)
/etc/postfix/virtual_mailbox_maps      size=0 lines=0   (.db present: 12288 bytes)
/etc/postfix/virtual_alias_maps        size=0 lines=0   (.db present: 12288 bytes)
/etc/postfix/virtual                    (absent)
/etc/dovecot/users                      size=0 lines=0  perms=640 root:dovecot
doveadm user '*'                        (no users)
/var/mail/vhosts/                        (empty, owned vmail:vmail)
```
All three `.db` files are compiled (present, 12 KB each), so Postfix won't error on lookup. **No drift** — S1 and S2 are byte-identically empty here.

---

## 4. Dovecot (`doveconf -n`)

Command: `doveconf -n`. **Identical on both servers** (only `Hostname:` differs). Key points:

```
protocols = imap pop3 lmtp
auth_mechanisms = plain login
disable_plaintext_auth = no
mail_location = maildir:~/Maildir
passdb { driver=passwd-file  args=scheme=SHA512-CRYPT username_format=%u /etc/dovecot/users }
userdb { driver=passwd-file  args=username_format=%u /etc/dovecot/users
         default_fields = uid=5000 gid=5000 home=/var/mail/vhosts/%d/%n }
service auth  { unix_listener /var/spool/postfix/private/auth        { mode=0660 user=postfix group=postfix } }
service lmtp  { unix_listener /var/spool/postfix/private/dovecot-lmtp { mode=0600 user=postfix group=postfix } }
ssl_cert = </etc/dovecot/private/dovecot.pem
ssl_key  = </etc/dovecot/private/dovecot.key
ssl_min_protocol = TLSv1.2
postmaster_address = postmaster@${PANEL_DOMAIN}
```

- `doveconf -c` config check → **exit 0** on both (valid).
- Sockets present and correctly owned on both:
  ```
  srw-rw---- postfix postfix /var/spool/postfix/private/auth
  srw------- postfix postfix /var/spool/postfix/private/dovecot-lmtp
  ```
- Config is from `install.sh` `/etc/dovecot/conf.d/99-panel.conf` override (not `email_install.go`'s `ConfigureDovecot`, which uses `BLF-CRYPT` + snakeoil cert; the live boxes use `SHA512-CRYPT` + Dovecot's own `dovecot.pem`).

### Dovecot findings (both servers)
- **`postmaster_address = postmaster@${PANEL_DOMAIN}` — variable NOT expanded.** Dovecot does not perform shell expansion, so the literal string `${PANEL_DOMAIN}` is the postmaster address. Origin: `install.sh` writes `postmaster_address = postmaster@${PANEL_DOMAIN}` inside a single-quoted heredoc (`<< 'DOVE99'`) so the shell never substituted it. This yields an invalid/odd `Postmaster` address on LMTP bounces/DSNs. (Low/medium — affects bounce hygiene, not auth.)
- **TLS cert = Dovecot's own self-signed `/etc/dovecot/private/dovecot.pem`** (CN = hostname), *different cert* from Postfix's snakeoil. Two distinct self-signed certs on one box; both have CN = `srvXXXX.hstgr.cloud`, neither matches the bare IP clients connect to → name-mismatch warnings for external IMAP/SMTP clients.
- **`disable_plaintext_auth = no`** — plaintext AUTH is permitted on the plain (non-TLS) listeners. Mitigated by `smtpd_tls_auth_only=yes` on the Postfix side and IMAP/POP3 not advertising `LOGINDISABLED`, but it is weaker than `disable_plaintext_auth = yes`. Documented as intentional in `99-panel.conf` for Roundcube/legacy clients.
- **No Sieve / ManageSieve.** `protocols = imap pop3 lmtp` (no `sieve`), no `dovecot-sieve`/`dovecot-managesieved` packages, **port 4190 not listening** on either server — despite `backend/internal/agent/sieve_install.go` existing in the repo. Server-side mail filtering / vacation rules are unavailable.

---

## 5. IMAP / POP3 / LMTP / doveadm / queue

- Listeners (both servers, `ss -ltnp`): **25, 465, 587** (Postfix `master`), **110, 143, 993, 995** (Dovecot), **783** (spamd, loopback only). All up.
- POP3 (110) and IMAP (143) are open in addition to the TLS variants (993/995) — plaintext ports are listening (auth still gated by Dovecot).
- `doveadm who` → no sessions (expected). `doveadm user '*'` → no users.
- **Mail queue empty on both** (`postqueue -p` / `mailq` → "Mail queue is empty"). No stuck/deferred mail at audit time (but note S2 cannot deliver outbound — see §7; nothing has been sent yet so the queue is simply empty).
- Outbound self-test (`EHLO; QUIT` to `127.0.0.1:25`) returns a clean 220/250 banner on both, advertising `SIZE 10240000`, `STARTTLS`, `8BITMIME`, `SMTPUTF8`, `CHUNKING`.

---

## 6. OpenDKIM

Command set: `opendkim -n`, dump of `/etc/opendkim.conf` + tables. **Identical on both servers.**

```
/etc/opendkim.conf:
  Mode sv   Canonicalization relaxed/simple   OversignHeaders From
  Socket local:/var/spool/postfix/opendkim/opendkim.sock
  KeyTable     refile:/etc/opendkim/key.table
  SigningTable refile:/etc/opendkim/signing.table
  ExternalIgnoreList/InternalHosts refile:/etc/opendkim/trusted.hosts
opendkim -n  -> exit 0 (valid)
milter socket: srwxrwxr-x opendkim opendkim /var/spool/postfix/opendkim/opendkim.sock   (present, both)
```

- Service **active + enabled** on both; milter socket exists and Postfix points at it (`smtpd_milters = local:/opendkim/opendkim.sock`). Socket dir is `opendkim:postfix 0750` so Postfix can read it.
- **`key.table` and `signing.table` are EMPTY**, `keys/` dir empty, no `mail.private`/`mail.txt` per-domain keys. Expected (0 domains). DKIM signing only activates once a domain is added (the panel runs `opendkim-genkey` per domain). At present **no outbound mail would be DKIM-signed** because there is no key/signing entry. No drift.
- `trusted.hosts` = `127.0.0.1`, `::1` (both). Differs from `email_install.go`'s `*.domain` form — install.sh's minimal form is what's deployed.

---

## 7. ⚠ CRITICAL DRIFT — Postfix chroot DNS (outbound delivery) — S2 broken

`smtp(8)` runs `chroot=y` (default), so it resolves DNS via `/var/spool/postfix/etc/resolv.conf`, **not** the host's `/etc/resolv.conf`.

Host `/etc/resolv.conf` on **both** servers is a static plain file (not a systemd symlink):
```
S1 & S2:  /etc/resolv.conf  ->  nameserver 8.8.8.8 / nameserver 8.8.4.4   (38 bytes)
```

Postfix **chroot** copy:
```
S1: /var/spool/postfix/etc/resolv.conf  ->  nameserver 8.8.8.8 / 8.8.4.4   (38 bytes)   ✅ matches host
S2: /var/spool/postfix/etc/resolv.conf  ->  systemd-resolved STUB, 920 bytes,
        nameserver 127.0.0.53
        options edns0 trust-ad
        search .                                                            ❌ STALE STUB
```

`postfix check` on **S2** explicitly warns (and it is logged in `/var/log/mail.log` at 19:38:41):
```
postfix/postlog: warning: /var/spool/postfix/etc/resolv.conf and /etc/resolv.conf differ
diff host vs chroot resolv.conf: (exit 1) — host has 8.8.8.8, chroot has 127.0.0.53
```

**Live impact test (read-only `dig` against the chroot's own nameserver):**
```
S1: chroot nameserver = 8.8.8.8
    dig @8.8.8.8 MX gmail.com  -> 5 records (alt1..alt4.gmail-smtp-in.l.google.com)   ✅
S2: chroot nameserver = 127.0.0.53
    dig @127.0.0.53 MX gmail.com -> (EMPTY — no answer)                                ❌
    (host-level dig MX gmail.com works fine; only the chroot path is broken)
```

`127.0.0.53` is the systemd-resolved stub listener bound in the host's network namespace; it is **not reachable from inside the Postfix chroot** (and on these boxes systemd-resolved's dbus unit is even absent — `resolvectl status` → "Unit dbus-org.freedesktop.resolve1.service not found"). Net effect on **S2**: the chrooted `smtp(8)` client **cannot resolve external MX records**, so **every message to an external recipient will fail MX lookup and defer/bounce** (`Host or domain name not found. Name service error for name=… type=MX`). This is exactly the regression `install.sh` (lines 870–906) was written to prevent.

**Root cause:** install.sh copies the host's resolver into the chroot, then rewrites it to 8.8.8.8 **only if it contains `127.0.0.53`**, and installs a `postfix-chroot-sync.path` unit that re-copies on host-resolv changes. On S2 the chroot ended up holding the stale stub: the `.path` watcher (active/waiting on both) re-copied `/etc/resolv.conf` at a moment it was still the stub, and the later swap of the host file to the static `8.8.8.8` form did not re-trigger a clean rewrite. The sync units are present and `enabled` on both, so a single re-sync would fix S2 — but **this is read-only; no remediation performed.**

`postfix-chroot-sync.path`: `active (waiting)` and `enabled` on both servers; chroot `/etc/` files timestamped at install time (S1 19:09, S2 19:17). S1's rewrite landed correctly; S2's did not.

---

## 8. Bare-IP hostname / deliverability implications (both servers)

- `myhostname = srv1785162.hstgr.cloud` (S1) / `srv1789639.hstgr.cloud` (S2) — the **Hostinger default PTR-style hostname**, *not* the `mail.<IP>` the brief mentions. `mydomain = hstgr.cloud`.
- `/etc/mailname` = the **bare IP** (`89.116.34.207` on S1, `195.35.7.64` on S2); `myorigin = /etc/mailname`. So locally-originated mail without an explicit From gets envelope sender `something@<bare-IP>`, which RFC 5321 §4.1.2 rejects (`501 5.1.7 Bad sender address syntax`). The panel's `isUsableMailDomain()` (panel_mail_service.go) specifically guards the panel's *own* FromAddr against this, but `/etc/mailname` itself is still the raw IP at the MTA layer.
- **TLS certs all have CN = `srvXXXX.hstgr.cloud`** (Postfix snakeoil and Dovecot self-signed), so any client connecting by **bare IP** gets a name mismatch; external receivers see a self-signed cert on opportunistic TLS.
- **Deliverability:** with no real domain, no published SPF/DKIM/DMARC, self-signed TLS, and `EHLO` = a generic `hstgr.cloud` host, outbound to Gmail/Microsoft will be heavily spam-filtered or rejected. `inet_protocols = ipv4` is correctly pinned (install.sh comment: avoids IPv6 no-PTR 5.7.25 bounces). These are **expected for an IP-only demo** and apply equally to S1 and S2 — but they compound the S2 chroot-DNS break (S2 can't even reach the MX to be filtered).

---

## 9. Spam protection (both servers) — SpamAssassin running but NOT wired in

- `spamd` (SpamAssassin 4.0.0) is **active + enabled**, listening on `127.0.0.1:783` + `[::1]:783`; loaded 388 body rules; child processes spawned. Config `/etc/spamassassin/local.cf` is the **stock package default** (only the `Shortcircuit` ifplugin block); the `required_score/report_safe/use_bayes` block from `email_install.go` is **absent** (that code path isn't the deployed one).
- **Postfix is not configured to call SpamAssassin at all:**
  ```
  postconf content_filter        -> content_filter =        (empty)
  master.cf                       -> no spamc / spamass refs
  dpkg                            -> no spamass-milter, no rspamd
  ```
  No `content_filter`, no spamass-milter, no milter pointing at spamd, no rspamd. **Inbound mail is never scanned** — spamd is effectively dead weight on both servers. (Origin: `install.sh` installs the `spamassassin` package and the panel only ensures the `spamd` unit is started; nothing ever integrates it into the delivery path.)
- **No antivirus:** `clamav-daemon` is `inactive` + `not-found` (not installed; install.sh doesn't install ClamAV in the email step — only the unused `email_install.go` does).
- The `spamassassin` systemd unit reports `inactive / not-found` (the real unit is `spamd`, which is what's running) — consistent with the code's documented "unit is spamd not spamassassin" fix.

**No drift between S1 and S2 for spam/AV** — both have the same gap.

---

## 10. fail2ban (both servers)

`fail2ban-client status` → **only the `sshd` jail** is active on both:
```
Number of jail: 1
Jail list: sshd
```
`postfix`, `postfix-sasl`, `dovecot`, `sasl` jails **do not exist**. The brief says "fail2ban active" — it is, but it provides **no brute-force protection for SMTP-AUTH / IMAP / POP3 login** on either server. Given `permit_sasl_authenticated` submission/smtps and IMAP/POP3 exposed publicly, mail-auth endpoints are unprotected against credential stuffing. Consistent across both clones (no drift).

---

## 11. Roundcube webmail (both servers)

- Packages: `roundcube 1.6.6+dfsg-2ubuntu0.1` (+ `roundcube-mysql`, `-plugins`, `-core`, skins). Code at `/usr/share/roundcube`; webroot `/var/lib/roundcube/public_html` (served by nginx at `^~ /webmail/`); config at `/etc/roundcube/config.inc.php` (and a duplicate at `/var/lib/roundcube/config/config.inc.php` — **identical content**).
- DB = MariaDB `roundcube` (via `roundcube-mysql`); DSN included from `/etc/roundcube/debian-db-roundcube.php`. `roundcube.users` count = **0** (no webmail logins yet). `session_storage = db`.
- Effective config (both servers, identical):
  ```
  imap_host = ["localhost:143"]
  smtp_host = 'tls://localhost:587'   (STARTTLS to local submission)
  smtp_user/%u  smtp_pass/%p           (passes the logged-in user's creds upstream)
  smtp_conn_options: verify_peer=false, allow_self_signed=true   (needed for snakeoil cert)
  product_name = 'Betazen Server Panel Webmail'   skin = elastic
  cookie_path = /webmail/   use_secure_urls=false   ip_check=false   referer_check=false
  proxy_whitelist = ['127.0.0.1']
  des_key = <set>   (redacted; present)
  plugins = [archive, zipdownload]
  ```
- Notes:
  - `verify_peer=false / allow_self_signed=true` is a pragmatic workaround for the self-signed Postfix/Dovecot certs — fine for localhost, but means Roundcube does not validate the mail server cert.
  - `ip_check` / `referer_check` disabled — acceptable behind a single nginx, but reduces session hardening; combined with **no TLS/443** (cookies travel over plain HTTP by bare IP), webmail credentials and sessions are exposed in transit. (Out of strict mail-stack scope but relevant to webmail security.)
  - Roundcube relies on Dovecot 143 + Postfix 587; both up. With 0 mailboxes there is nothing to log into yet.
- **No drift** S1 vs S2 (config byte-identical aside from being two copies of the same file).

---

## 12. Config validity summary

| Check | S1 | S2 |
|---|---|---|
| `postfix check` | exit 0 (clean) | exit 0 **but warns: chroot resolv.conf differs** |
| `doveconf -c` | exit 0 | exit 0 |
| `opendkim -n` | exit 0 | exit 0 |
| Mail queue | empty | empty |
| 587/465 AUTH after STARTTLS | `AUTH PLAIN LOGIN` ✅ | `AUTH PLAIN LOGIN` ✅ |
| IMAPS 993 capability | `AUTH=PLAIN AUTH=LOGIN` ✅ | same ✅ |
| Chroot MX resolution | works (8.8.8.8) ✅ | **fails (127.0.0.53)** ❌ |

---

## 13. Drift S1 vs S2 — consolidated

| Item | S1 (89.116.34.207) | S2 (195.35.7.64) | Severity |
|---|---|---|---|
| **Postfix chroot resolver** | `8.8.8.8 / 8.8.4.4`, MX resolves | **`127.0.0.53` stub, MX fails → outbound defers/bounces** | **CRITICAL** |
| `postfix check` | clean | warns (resolv.conf differ) | high (same root cause) |
| Everything else (main.cf, master.cf, dovecot, opendkim, spamd, roundcube, fail2ban, certs, ports, queue) | — | byte-identical to S1 | n/a |

Apart from the chroot-DNS defect, the two boxes are configuration-identical clones.

---

## 14. Stack-wide issues (apply to BOTH servers)

1. **SpamAssassin not integrated** — `spamd` runs but Postfix has no `content_filter`/milter calling it; inbound mail unscanned. (medium)
2. **`message_size_limit` = 10 MB** (stock default), not the 50 MB the panel code (`email_install.go`) intends. Attachment cap is smaller than expected. (low/medium)
3. **`postmaster_address = postmaster@${PANEL_DOMAIN}`** — unexpanded literal in Dovecot (single-quoted heredoc in install.sh). Invalid postmaster on DSNs. (low/medium)
4. **fail2ban: only `sshd` jail** — no postfix/dovecot/sasl jails; mail-auth brute-force unprotected. (medium)
5. **No Sieve/ManageSieve** (4190 down, no packages) — no server-side filters/vacation. (low)
6. **No ClamAV** — `clamav-daemon` not installed/active; no AV scanning. (low/medium)
7. **Bare-IP identity** — `/etc/mailname` = raw IP, hostname = `*.hstgr.cloud`, self-signed certs (CN ≠ IP), no SPF/DKIM/DMARC published → poor external deliverability + cert name mismatches. Expected for IP-only demo. (info/medium)
8. **`disable_plaintext_auth = no`** in Dovecot — plaintext AUTH allowed on non-TLS listeners (intentional, but a hardening note). (low)

---

## Appendix — exact commands used (read-only)

```bash
# connectivity / identity
python bz.py 1 'hostname; hostname -f; cat /etc/hostname; date; uptime'
# Postfix (postfix_audit.sh): postconf -n / -M / -P, master.cf, postqueue -p, mailq,
#   postfix check, virtual map files, sasl/milter/limits/restrictions
python bz.py {1,2} --file postfix_audit.sh
# extras (extras.sh): /etc/mailname, header_checks_betazen, host vs chroot resolv.conf diff,
#   chroot-sync units, snakeoil cert dates, /etc/hosts, mail port listeners
python bz.py {1,2} --file extras.sh
# Dovecot (dovecot_audit.sh): dovecot --version, doveconf -n, doveconf -c, /etc/dovecot/users,
#   99-panel.conf, passdb/userdb, sieve check, auth socket, doveadm who/user, ssl, mail_location
python bz.py {1,2} --file dovecot_audit.sh
# services (services_audit.sh): resolv.conf symlink, systemctl is-active/enabled for all daemons,
#   opendkim socket + conf + tables + opendkim -n, spam wiring (content_filter/master.cf/dpkg),
#   spamd config
python bz.py {1,2} --file services_audit.sh
# roundcube + DNS (roundcube_dns.sh): roundcube pkgs/config/db, nginx /webmail,
#   dig MX gmail.com via chroot NS, EHLO to :25
python bz.py {1,2} --file roundcube_dns.sh
# logs + queue (logs_audit.sh): message_size_limit default vs effective, fail2ban jails,
#   mail.log/journal DNS/defer/milter, spamd scan history, 4190, EHLO 587
python bz.py {1,2} --file logs_audit.sh
# auth + TLS (auth_tls.sh): openssl s_client STARTTLS 587 / wrappermode 465 / IMAPS 993 caps + certs
python bz.py {1,2} --file auth_tls.sh
```
