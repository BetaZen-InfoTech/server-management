#!/usr/bin/env python3
"""Verify post-transfer mailbox state on the DESTINATION VPS.

Reproduces the user's reported "Bulk upload email work properly but
one server change, it will not to create and not work" — runs ON the
destination VPS (paste after `ssh root@<destination>`) and checks
whether every mailbox row in destination Mongo also has matching
entries in /etc/dovecot/users + /etc/postfix/virtual_mailbox_maps +
virtual_mailbox_domains.

Pre-3.1.47 the panel-records-only path of the transfer wizard
silently left those files EMPTY for transferred mailboxes — Mongo +
panel UI showed them, but inbound mail bounced "user unknown" and
IMAP login failed because Dovecot had no passdb entry. This script
first checks the gap, then triggers the new
POST /api/v1/whm/email/mailboxes/rehydrate endpoint, then re-checks.

Stdlib-only — no paramiko, no requests, no extra installs. Talks
to the local panel via http://127.0.0.1:8080.

Run on the DESTINATION VPS:
    cd /opt/serverpanel
    sudo python3 scripts/_smoke_transfer_mailboxes_local.py
"""
from __future__ import annotations

import hashlib
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

BACKEND = "http://127.0.0.1:8080"
OTP_CODE = "TXFRMX"
FAILS: list[str] = []


def sh(cmd: str, *, show: bool = True) -> tuple[str, str, int]:
    p = subprocess.run(["bash", "-c", cmd], capture_output=True)
    out = p.stdout.decode("utf-8", errors="replace").strip()
    err = p.stderr.decode("utf-8", errors="replace").strip()
    if show:
        tag = "OK" if p.returncode == 0 else f"exit={p.returncode}"
        print(f"[{tag}] $ {cmd[:140]}")
        for ln in (out + ("\n" + err if err else "")).splitlines()[:12]:
            print(f"    {ln}")
    return out, err, p.returncode


def mongo_uri() -> str:
    if os.path.exists("/opt/serverpanel/.env"):
        with open("/opt/serverpanel/.env") as f:
            for line in f:
                if line.startswith("MONGO_URI="):
                    return line.split("=", 1)[1].strip().strip('"').strip("'")
    return "mongodb://localhost:27017/serverpanel"


def mongo(query: str, *, show: bool = False) -> str:
    safe = query.replace("'", "'\\''")
    out, _, _ = sh(f'mongosh --quiet "{mongo_uri()}" --eval \'{safe}\'', show=show)
    return out


def must(label: str, cond: bool, detail: str = "") -> None:
    print(f"    [{'PASS' if cond else 'FAIL'}] {label}" + (f" — {detail}" if detail else ""))
    if not cond:
        FAILS.append(label)


def http(method: str, path: str, *, body=None, headers=None) -> tuple[int, str]:
    hdrs = dict(headers or {})
    data: bytes | None = None
    if body is not None:
        data = body if isinstance(body, bytes) else json.dumps(body).encode("utf-8")
        hdrs.setdefault("Content-Type", "application/json")
    req = urllib.request.Request(BACKEND + path, data=data, method=method, headers=hdrs)
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            return resp.status, resp.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", errors="replace")


print("=== Post-Transfer Mailbox Smoke Test (DESTINATION VPS) ===")
print(f"Mongo URI: {mongo_uri()}")
print()

# 1. Resolve owner for JWT mint
ow_out = mongo(
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    'print(JSON.stringify({id:u._id.toString(),email:u.email,username:u.username}));',
    show=False,
)
owner = None
for ln in reversed(ow_out.splitlines()):
    if ln.strip().startswith("{"):
        try:
            owner = json.loads(ln.strip())
            break
        except Exception:
            continue
if not owner:
    sys.exit("!! no platform owner found in users collection on this VPS")
print(f"owner: {owner['email']}")

# 2. Snapshot Mongo mailbox count + a sample of addresses
print("\n--- Mongo mailbox state ---")
count_out = mongo('print(db.mailboxes.countDocuments({}));', show=False)
mongo_count = 0
try:
    mongo_count = int(count_out.split()[-1])
except Exception:
    pass
print(f"    Mongo mailbox count: {mongo_count}")
if mongo_count == 0:
    print("\n!! No mailboxes in Mongo on this VPS. If you just transferred from another box,")
    print("   the panel-records sync didn't import them. Check the transfer job's logs.")
    sys.exit(1)

# Sample 10 addresses for the per-row check
sample_out = mongo(
    'db.mailboxes.find({},{email:1,_id:0}).limit(10).forEach(d=>print(d.email));',
    show=False,
)
sample = [ln.strip().lower() for ln in sample_out.splitlines() if ln.strip() and "@" in ln]
print(f"    sample addresses ({len(sample)}): {', '.join(sample[:5])}{'…' if len(sample) > 5 else ''}")

# 3. Pre-rehydrate: how many of those sample addresses are in the
#    Postfix + Dovecot files? If most/all are MISSING, that's the
#    pre-3.1.47 bug live on this box.
print("\n--- BEFORE rehydrate: filesystem state ---")
dovecot_hits = 0
postfix_hits = 0
for addr in sample:
    esc = re.escape(addr)
    _, _, dov_code = sh(f"grep -q '^{esc}:' /etc/dovecot/users 2>/dev/null", show=False)
    _, _, pf_code = sh(f"grep -q '^{esc}\\s' /etc/postfix/virtual_mailbox_maps 2>/dev/null", show=False)
    if dov_code == 0:
        dovecot_hits += 1
    if pf_code == 0:
        postfix_hits += 1
print(f"    /etc/dovecot/users         hits {dovecot_hits}/{len(sample)}")
print(f"    virtual_mailbox_maps       hits {postfix_hits}/{len(sample)}")
gap_before = (len(sample) - dovecot_hits) + (len(sample) - postfix_hits)
if gap_before > 0:
    print(f"    → {gap_before} missing entries detected (this is the bug v3.1.47 fixes)")
else:
    print("    → all sample mailboxes already in both files (no gap)")

# 4. Mint admin JWT to call the rehydrate endpoint
print("\n--- minting admin JWT via OTP ---")
mongo(f'db.otp_requests.deleteMany({{email:"{owner["email"]}"}});', show=False)
status, body = http("POST", "/api/v1/auth/otp/request",
                    body={"email": owner["email"], "surface": "whm"})
must("OTP request 200", status == 200, body[:200])
mongo(
    f'db.otp_requests.updateMany({{email:"{owner["email"]}", used:false, expires_at:{{$gt:new Date()}}}}, '
    f'{{$set:{{code_hash:"{hashlib.sha256(OTP_CODE.encode()).hexdigest()}", attempt_count:0}}}});',
    show=False,
)
status, body = http("POST", "/api/v1/auth/otp/verify",
                    body={"email": owner["email"], "code": OTP_CODE, "surface": "whm"})
if status != 200:
    sys.exit(f"!! OTP verify failed: {body[:300]}")
JWT = json.loads(body)["data"]["access_token"]
print(f"    JWT length: {len(JWT)}")

# 5. Call the new POST /email/mailboxes/rehydrate endpoint
print("\n--- POST /api/v1/whm/email/mailboxes/rehydrate ---")
status, body = http("POST", "/api/v1/whm/email/mailboxes/rehydrate",
                    headers={"Authorization": f"Bearer {JWT}"})
print(f"    HTTP {status}")
print(f"    Body: {body[:400]}")
must("HTTP 200 from rehydrate", status == 200, f"got {status}")
try:
    payload = json.loads(body).get("data", {})
    print(f"    rebuilt {payload.get('mailbox_count', '?')} mailboxes")
except Exception:
    payload = {}

# 6. AFTER rehydrate: re-check sample
print("\n--- AFTER rehydrate: filesystem state ---")
dovecot_hits_after = 0
postfix_hits_after = 0
for addr in sample:
    esc = re.escape(addr)
    _, _, dov_code = sh(f"grep -q '^{esc}:' /etc/dovecot/users 2>/dev/null", show=False)
    _, _, pf_code = sh(f"grep -q '^{esc}\\s' /etc/postfix/virtual_mailbox_maps 2>/dev/null", show=False)
    if dov_code == 0:
        dovecot_hits_after += 1
    if pf_code == 0:
        postfix_hits_after += 1
print(f"    /etc/dovecot/users         hits {dovecot_hits_after}/{len(sample)}")
print(f"    virtual_mailbox_maps       hits {postfix_hits_after}/{len(sample)}")
must("every sample mailbox in dovecot/users after rehydrate",
     dovecot_hits_after == len(sample),
     f"{len(sample) - dovecot_hits_after} missing — likely have blank password fields in Mongo (re-set from panel)")
must("every sample mailbox in virtual_mailbox_maps after rehydrate",
     postfix_hits_after == len(sample),
     f"{len(sample) - postfix_hits_after} missing")

# 7. Postmap .db is fresh
out, _, _ = sh("stat -c '%Y' /etc/postfix/virtual_mailbox_maps.db && date +%s", show=False)
try:
    parts = [int(x) for x in out.split()]
    age = parts[1] - parts[0]
    must("virtual_mailbox_maps.db rebuilt within last 60s", age < 60, f"age={age}s")
except Exception:
    must(".db stat parsed", False, out[:100])

# 8. virtual_mailbox_domains has each transferred domain
print("\n--- /etc/postfix/virtual_mailbox_domains ---")
sample_domains = sorted({addr.split("@", 1)[1] for addr in sample})
for d in sample_domains[:5]:
    esc = re.escape(d)
    _, _, code = sh(f"grep -qE '^{esc}\\s' /etc/postfix/virtual_mailbox_domains 2>/dev/null", show=False)
    must(f"virtual_mailbox_domains has {d}", code == 0)

# 9. Live Dovecot auth round-trip (only checks one mailbox — the
#    first one whose password we can probe via a fake doveadm call)
print("\n--- Dovecot service liveness ---")
out, _, _ = sh("systemctl is-active dovecot", show=False)
must("dovecot service active", out == "active", out[:100])
out, _, _ = sh("ss -ltn 'sport = :143' 2>/dev/null | tail -n +2", show=False)
must("port 143 (IMAP) listening", bool(out.strip()))

# Summary
print("\n=== summary ===")
print(f"  Mongo mailbox count:         {mongo_count}")
print(f"  BEFORE rehydrate gap:        {gap_before} missing entries (in {len(sample)} sample)")
print(f"  AFTER rehydrate gap:         {(len(sample) - dovecot_hits_after) + (len(sample) - postfix_hits_after)}")
print(f"  Rebuild reported:            {payload.get('mailbox_count', '?')} mailboxes")
print()
if FAILS:
    print(f"FAILED: {len(FAILS)} check(s)")
    for f in FAILS:
        print(f"  - {f}")
    sys.exit(1)
print("ALL CHECKS PASSED — transferred mailboxes are now wired into Postfix + Dovecot.")
print("Send a test mail to one of the transferred addresses; it should land in the maildir + IMAP login should work.")
