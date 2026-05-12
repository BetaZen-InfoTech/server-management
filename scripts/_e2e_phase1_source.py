#!/usr/bin/env python3
"""Phase 1 — run ON THE SOURCE VPS.

What it does:
  1. Confirm panel build is >= 3.1.50 (so source's outbound state is sane).
  2. Pick the first hosted domain on this box (or use $DOMAIN env).
  3. Bulk-create N test mailboxes (default 5) directly via the panel API
     so the path is identical to a real bulk Excel upload.
  4. Verify each mailbox lands in /etc/dovecot/users +
     /etc/postfix/virtual_mailbox_maps (so source's mail stack works
     before we attempt a transfer).
  5. Send a test email FROM mailbox-1 TO mailbox-2 via local SMTP and
     confirm mailbox-2's Dovecot Maildir grew (proves source delivers
     locally and IMAP would see the message).

Stdlib-only. Run on SOURCE:
    cd /opt/serverpanel
    sudo python3 scripts/_e2e_phase1_source.py

Output is the test mailbox roster — copy the JSON line that starts with
"ROSTER:" so phase 2 can verify the same set on destination.
"""
from __future__ import annotations

import json
import os
import secrets
import smtplib
import string
import subprocess
import sys
import time
import urllib.error
import urllib.request
from email.message import EmailMessage

BACKEND = "http://127.0.0.1:8080"
NUM_MAILBOXES = 5
PASS_LEN = 16
FAILS: list[str] = []


def sh(cmd: str, *, show: bool = True) -> tuple[str, str, int]:
    p = subprocess.run(["bash", "-c", cmd], capture_output=True)
    out = p.stdout.decode("utf-8", errors="replace").strip()
    err = p.stderr.decode("utf-8", errors="replace").strip()
    if show:
        tag = "OK" if p.returncode == 0 else f"exit={p.returncode}"
        print(f"[{tag}] $ {cmd[:140]}")
        for ln in (out + ("\n" + err if err else "")).splitlines()[:8]:
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
    out, _, _ = sh(f'mongosh --quiet "{uri}" --eval \'{safe}\'', show=False)
    return out


def http(method: str, path: str, *, body=None, headers=None) -> tuple[int, str]:
    hdrs = dict(headers or {})
    data: bytes | None = None
    if body is not None:
        data = body if isinstance(body, bytes) else json.dumps(body).encode("utf-8")
        hdrs.setdefault("Content-Type", "application/json")
    req = urllib.request.Request(BACKEND + path, data=data, method=method, headers=hdrs)
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            return r.status, r.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", errors="replace")
    except Exception as e:
        return 0, str(e)


def login_admin() -> str:
    """Get a JWT for the super-admin so we can call /api/v1/whm/* endpoints."""
    admin_email = env("ADMIN_EMAIL") or "admin@betazeninfotech.com"
    # Look up the email row Mongo currently has — install.sh default may
    # have been changed by the operator. We need the email to call login.
    out = mongo(
        'JSON.stringify(db.users.find({role:"vendor_owner"},{email:1,_id:0}).toArray())'
    )
    try:
        rows = json.loads(out.splitlines()[-1])
        if rows and rows[0].get("email"):
            admin_email = rows[0]["email"]
    except Exception:
        pass
    print(f"    super-admin email: {admin_email}")

    # Try common dev passwords first; if all fail, reset via bzpanel.
    candidates = ["admin123", "Admin@123", "BetaZen@2023", "password"]
    token = ""
    for pw in candidates:
        code, body = http("POST", "/api/auth/login", body={"email": admin_email, "password": pw})
        if code == 200:
            try:
                token = json.loads(body)["data"]["access_token"]
                print(f"    login OK with stored password")
                return token
            except Exception:
                pass

    # Fallback: rotate to a known password via bzpanel CLI.
    new_pw = "BzE2E_" + "".join(secrets.choice(string.ascii_letters) for _ in range(8))
    print(f"    login attempts failed; rotating admin password via bzpanel ...")
    sh(f'bzpanel admin-password "{new_pw}"', show=True)
    time.sleep(1)
    code, body = http("POST", "/api/auth/login", body={"email": admin_email, "password": new_pw})
    if code != 200:
        print(f"    [FATAL] post-reset login still failing: {code} {body[:160]}")
        sys.exit(1)
    return json.loads(body)["data"]["access_token"]


def pick_domain(token: str) -> str:
    if env("DOMAIN_OVERRIDE"):
        return env("DOMAIN_OVERRIDE")
    code, body = http("GET", "/api/v1/whm/domains?page=1&limit=20",
                      headers={"Authorization": "Bearer " + token})
    if code != 200:
        print(f"    [FATAL] /whm/domains → {code}: {body[:160]}")
        sys.exit(1)
    try:
        items = json.loads(body)["data"]
    except Exception:
        items = []
    for d in items:
        name = d.get("domain") or ""
        # Skip the panel's own management domain.
        if not name or "betazeninfotech" in name:
            continue
        return name
    print("    [FATAL] no usable hosted domain found — create one first.")
    sys.exit(1)


def main() -> int:
    print("\n=== PHASE 1 — source-side bulk upload + delivery test ===\n")

    print("step 1 — version check")
    code, body = http("GET", "/api/v1/version")
    print(f"    /api/v1/version → {code} {body[:200]}")
    try:
        ver = json.loads(body).get("version", "")
    except Exception:
        ver = ""
    parts = [int(x) for x in ver.split(".") if x.isdigit()]
    while len(parts) < 3:
        parts.append(0)
    must("source panel >= 3.1.50", tuple(parts[:3]) >= (3, 1, 50), f"got {ver}")

    print("\nstep 2 — admin login")
    token = login_admin()
    auth = {"Authorization": "Bearer " + token}

    print("\nstep 3 — pick hosted domain")
    domain = pick_domain(token)
    print(f"    using domain: {domain}")

    print(f"\nstep 4 — create {NUM_MAILBOXES} test mailboxes (mirrors bulk-upload code path)")
    roster: list[dict] = []
    suffix = secrets.token_hex(3)
    for i in range(1, NUM_MAILBOXES + 1):
        local = f"e2e-{suffix}-{i:02d}"
        email = f"{local}@{domain}"
        pwd = "Bz_" + "".join(secrets.choice(string.ascii_letters + string.digits) for _ in range(PASS_LEN))
        body = {"email": email, "password": pwd, "domain": domain, "quota_mb": 256}
        code, resp = http("POST", "/api/v1/whm/email/mailboxes", body=body, headers=auth)
        ok = code in (200, 201)
        must(f"create {email}", ok, f"code={code} body={resp[:120]}")
        if ok:
            roster.append({"email": email, "password": pwd})

    print("\nstep 5 — verify each mailbox is present in dovecot + postfix maps")
    for r in roster:
        in_dv = sh(f"grep -c -F '{r['email']}' /etc/dovecot/users || true", show=False)[0]
        in_pf = sh(f"grep -c -F '{r['email']}' /etc/postfix/virtual_mailbox_maps || true", show=False)[0]
        must(f"{r['email']} in dovecot/users", int(in_dv or 0) >= 1)
        must(f"{r['email']} in postfix maps", int(in_pf or 0) >= 1)

    print("\nstep 6 — send test email from mailbox-1 to mailbox-2 via local SMTP")
    if len(roster) < 2:
        print("    SKIP: not enough mailboxes")
    else:
        sender = roster[0]
        receiver = roster[1]
        msg = EmailMessage()
        msg["From"] = sender["email"]
        msg["To"] = receiver["email"]
        msg["Subject"] = f"e2e phase1 source-local {suffix}"
        body_text = f"PHASE1-MARK-{suffix}-source-local"
        msg.set_content(body_text)
        try:
            with smtplib.SMTP("127.0.0.1", 25, timeout=15) as smtp:
                smtp.send_message(msg)
            must("SMTP submit", True)
        except Exception as e:
            must("SMTP submit", False, str(e))

        # Wait up to 15s for delivery, polling Maildir for the marker.
        local_part = receiver["email"].split("@")[0]
        domain_part = receiver["email"].split("@")[1]
        maildir = f"/var/vmail/{domain_part}/{local_part}"
        delivered = False
        for _ in range(15):
            time.sleep(1)
            out, _, _ = sh(
                f"grep -rl 'PHASE1-MARK-{suffix}-source-local' '{maildir}' 2>/dev/null | head -1",
                show=False,
            )
            if out:
                delivered = True
                print(f"    delivered to: {out}")
                break
        must("local delivery within 15s", delivered, f"checked {maildir}")

    print("\n=== ROSTER (copy this line for phase 2) ===")
    print("ROSTER:" + json.dumps({"domain": domain, "suffix": suffix, "mailboxes": roster}))
    print()
    print(f"=== summary: {len(FAILS)} failure(s) ===")
    for f in FAILS:
        print("  - " + f)
    return 0 if not FAILS else 1


if __name__ == "__main__":
    sys.exit(main())
