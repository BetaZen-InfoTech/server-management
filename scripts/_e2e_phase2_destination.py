#!/usr/bin/env python3
"""Phase 2 — run ON THE DESTINATION VPS after a transfer.

Takes the ROSTER:{...} line emitted by phase 1 (paste as first arg or
via $ROSTER env) and verifies that EVERY test mailbox transferred
correctly:

  1. Confirm panel build is >= 3.1.50.
  2. For each mailbox in the roster:
     a. Mongo row exists with field `email` matching.
     b. Line present in /etc/dovecot/users.
     c. Line present in /etc/postfix/virtual_mailbox_maps.
     d. IMAP login over 127.0.0.1:143 (STARTTLS) succeeds with the
        original password.
  3. Send a test email FROM mailbox-1 TO mailbox-2 via local SMTP and
     confirm mailbox-2's Maildir grew (proves destination delivers).
  4. (Optional) IMAP-poll the message via mailbox-2's account to prove
     login + delivery work end-to-end.

Stdlib-only. Run on DESTINATION:
    cd /opt/serverpanel
    sudo python3 scripts/_e2e_phase2_destination.py 'ROSTER:{...}'
"""
from __future__ import annotations

import imaplib
import json
import os
import smtplib
import ssl as ssllib
import subprocess
import sys
import time
import urllib.error
import urllib.request
from email.message import EmailMessage

BACKEND = "http://127.0.0.1:8080"
FAILS: list[str] = []


def sh(cmd: str, *, show: bool = False) -> tuple[str, str, int]:
    p = subprocess.run(["bash", "-c", cmd], capture_output=True)
    out = p.stdout.decode("utf-8", errors="replace").strip()
    err = p.stderr.decode("utf-8", errors="replace").strip()
    if show:
        tag = "OK" if p.returncode == 0 else f"exit={p.returncode}"
        print(f"[{tag}] $ {cmd[:140]}")
        for ln in (out + ("\n" + err if err else "")).splitlines()[:6]:
            print(f"    {ln}")
    return out, err, p.returncode


def must(label: str, cond: bool, detail: str = "") -> None:
    print(f"    [{'PASS' if cond else 'FAIL'}] {label}" + (f" — {detail}" if detail else ""))
    if not cond:
        FAILS.append(label)


def env(key: str) -> str:
    if not os.path.exists("/opt/serverpanel/.env"):
        return ""
    with open("/opt/serverpanel/.env") as f:
        for ln in f:
            if ln.startswith(key + "="):
                return ln.split("=", 1)[1].strip().strip('"').strip("'")
    return ""


def mongo(query: str) -> str:
    uri = env("MONGO_URI") or "mongodb://localhost:27017/serverpanel"
    safe = query.replace("'", "'\\''")
    out, _, _ = sh(f'mongosh --quiet "{uri}" --eval \'{safe}\'')
    return out


def http(method: str, path: str) -> tuple[int, str]:
    try:
        with urllib.request.urlopen(BACKEND + path, timeout=10) as r:
            return r.status, r.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", errors="replace")
    except Exception as e:
        return 0, str(e)


def parse_roster(raw: str) -> dict:
    raw = raw.strip()
    if raw.startswith("ROSTER:"):
        raw = raw[len("ROSTER:"):]
    return json.loads(raw)


def imap_login(host: str, addr: str, pw: str) -> tuple[bool, str]:
    try:
        ctx = ssllib.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssllib.CERT_NONE
        m = imaplib.IMAP4(host, 143)
        m.starttls(ssl_context=ctx)
        m.login(addr, pw)
        m.select("INBOX")
        m.logout()
        return True, "ok"
    except Exception as e:
        return False, str(e)


def imap_search(host: str, addr: str, pw: str, marker: str, *, retries: int = 15) -> bool:
    for _ in range(retries):
        time.sleep(1)
        try:
            ctx = ssllib.create_default_context()
            ctx.check_hostname = False
            ctx.verify_mode = ssllib.CERT_NONE
            m = imaplib.IMAP4(host, 143)
            m.starttls(ssl_context=ctx)
            m.login(addr, pw)
            m.select("INBOX")
            typ, data = m.search(None, "BODY", marker)
            ids = (data[0] or b"").split()
            m.logout()
            if ids:
                return True
        except Exception:
            pass
    return False


def main() -> int:
    if len(sys.argv) < 2 and not os.environ.get("ROSTER"):
        print(__doc__)
        return 2
    raw = sys.argv[1] if len(sys.argv) >= 2 else os.environ["ROSTER"]
    roster = parse_roster(raw)
    domain = roster["domain"]
    suffix = roster["suffix"]
    mailboxes = roster["mailboxes"]

    print("\n=== PHASE 2 — destination-side post-transfer verify ===\n")
    print(f"    domain: {domain}")
    print(f"    suffix: {suffix}")
    print(f"    mailboxes to verify: {len(mailboxes)}")

    print("\nstep 1 — version check")
    code, body = http("GET", "/api/v1/version")
    print(f"    /api/v1/version → {code} {body[:200]}")
    try:
        ver = json.loads(body).get("version", "")
    except Exception:
        ver = ""
    parts = [int(x) for x in ver.split(".") if x.isdigit()]
    while len(parts) < 3:
        parts.append(0)
    must("destination panel >= 3.1.50", tuple(parts[:3]) >= (3, 1, 50), f"got {ver}")

    print("\nstep 2 — verify each mailbox is on destination Mongo + dovecot + postfix")
    for r in mailboxes:
        addr = r["email"]

        # Mongo
        out = mongo(
            f'db.mailboxes.countDocuments({{email:"{addr}"}})'
        )
        try:
            n = int(out.strip().splitlines()[-1])
        except Exception:
            n = 0
        must(f"Mongo has {addr}", n >= 1, f"countDocuments={n}")

        # Dovecot users
        in_dv, _, rc = sh(f"grep -c -F '{addr}' /etc/dovecot/users || true")
        must(f"/etc/dovecot/users has {addr}", int(in_dv or 0) >= 1)

        # Postfix virtual_mailbox_maps
        in_pf, _, _ = sh(f"grep -c -F '{addr}' /etc/postfix/virtual_mailbox_maps || true")
        must(f"/etc/postfix/virtual_mailbox_maps has {addr}", int(in_pf or 0) >= 1)

    print("\nstep 3 — IMAP login per mailbox (127.0.0.1:143 STARTTLS)")
    for r in mailboxes:
        ok, info = imap_login("127.0.0.1", r["email"], r["password"])
        must(f"IMAP login {r['email']}", ok, info if not ok else "")

    print("\nstep 4 — send test email mailbox-1 → mailbox-2 on destination")
    if len(mailboxes) >= 2:
        sender = mailboxes[0]
        receiver = mailboxes[1]
        marker = f"PHASE2-MARK-{suffix}-dest"
        msg = EmailMessage()
        msg["From"] = sender["email"]
        msg["To"] = receiver["email"]
        msg["Subject"] = f"e2e phase2 dest {suffix}"
        msg.set_content(marker)
        try:
            with smtplib.SMTP("127.0.0.1", 25, timeout=15) as smtp:
                smtp.send_message(msg)
            must("destination SMTP submit", True)
        except Exception as e:
            must("destination SMTP submit", False, str(e))

        # Maildir poll
        local_part = receiver["email"].split("@")[0]
        domain_part = receiver["email"].split("@")[1]
        maildir = f"/var/vmail/{domain_part}/{local_part}"
        delivered = False
        for _ in range(20):
            time.sleep(1)
            out, _, _ = sh(f"grep -rl '{marker}' '{maildir}' 2>/dev/null | head -1")
            if out:
                delivered = True
                print(f"    delivered to: {out}")
                break
        must("destination local delivery", delivered, f"checked {maildir}")

        # IMAP search via receiver login
        seen = imap_search("127.0.0.1", receiver["email"], receiver["password"], marker)
        must("receiver IMAP sees the new message", seen)
    else:
        print("    SKIP: not enough mailboxes")

    print(f"\n=== summary: {len(FAILS)} failure(s) ===")
    for f in FAILS:
        print("  - " + f)
    return 0 if not FAILS else 1


if __name__ == "__main__":
    sys.exit(main())
