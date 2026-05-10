#!/usr/bin/env python3
"""End-to-end smoke test for bulk forwarder upload + keep_copy honouring.

Runs ON THE VPS (paste after `ssh root@<vps>`). Stdlib-only.

Reproduces the user's exact CSV (4 rows, all keep_copy=TRUE,
multi-destination), POSTs it via multipart, then verifies for every
row:

  1. Mongo `email_forwarders` row exists with keep_copy=true
  2. /etc/postfix/virtual_alias_maps line ends with the SOURCE
     address appended (the v3.1.44 keep_copy fix — pre-3.1.44 this
     didn't happen and forwarded mail vanished from the source mbox)
  3. postmap .db is fresh

Run:
    cd /opt/serverpanel
    sudo python3 scripts/_smoke_bulk_forwarders_local.py
"""
from __future__ import annotations

import hashlib
import json
import os
import re
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request

BACKEND = "http://127.0.0.1:8080"
TEST_DOMAIN = "smoke-fwd.invalid"
SOURCES = [f"user_jfvuyjhgf_{i:02d}@{TEST_DOMAIN}" for i in (1, 2, 3, 4)]
DESTS = "betazeninfotech.sayantan@gmail.com, iamsayantankar@gmail.com"
OTP_CODE = "FWDKC9"
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


def http(method: str, path: str, *, body=None, headers=None, files=None) -> tuple[int, str]:
    url = BACKEND + path
    hdrs = dict(headers or {})
    data: bytes | None = None
    if files:
        boundary = "----BzPanelFwd" + secrets.token_hex(8)
        hdrs["Content-Type"] = f"multipart/form-data; boundary={boundary}"
        chunks: list[bytes] = []
        for field, (fname, content, ctype) in files.items():
            if isinstance(content, str):
                content = content.encode("utf-8")
            chunks.append(f"--{boundary}\r\n".encode())
            chunks.append(f'Content-Disposition: form-data; name="{field}"; filename="{fname}"\r\n'.encode())
            chunks.append(f"Content-Type: {ctype}\r\n\r\n".encode())
            chunks.append(content)
            chunks.append(b"\r\n")
        chunks.append(f"--{boundary}--\r\n".encode())
        data = b"".join(chunks)
    elif body is not None:
        data = body if isinstance(body, bytes) else json.dumps(body).encode("utf-8")
        hdrs.setdefault("Content-Type", "application/json")
    req = urllib.request.Request(url, data=data, method=method, headers=hdrs)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.status, resp.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", errors="replace")


def cleanup(owner_email: str | None = None) -> None:
    print("--- cleanup ---")
    mongo(
        f'db.email_forwarders.deleteMany({{domain:"{TEST_DOMAIN}"}}); '
        f'db.domains.deleteMany({{domain:"{TEST_DOMAIN}"}}); '
        + (f'db.otp_requests.deleteMany({{email:"{owner_email}"}});' if owner_email else ""),
        show=False,
    )
    sh(f"sed -i '/@{re.escape(TEST_DOMAIN)}\\s/d' /etc/postfix/virtual_alias_maps 2>/dev/null", show=False)
    sh("postmap /etc/postfix/virtual_alias_maps 2>/dev/null", show=False)
    sh("systemctl reload postfix 2>/dev/null", show=False)


# ── 1. Resolve owner ─────────────────────────────────────────────
print("=== Bulk Forwarder Upload + keep_copy Smoke Test ===")
ow_out = mongo(
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    "print(JSON.stringify({id:u._id.toString(),email:u.email,username:u.username,"
    "tenant_id:(u.tenant_id&&u.tenant_id.toString())||u._id.toString()}));",
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
    sys.exit("!! no platform owner found")
print(f"owner: {owner['email']} (username={owner['username']})")

cleanup(owner["email"])

# ── 2. Seed test domain ──────────────────────────────────────────
mongo(
    f'const now=new Date(); db.domains.insertOne({{ '
    f'domain:"{TEST_DOMAIN}", user:"{owner["username"]}", '
    f'php_version:"8.1", ssl_active:false, suspended:false, '
    f'user_id:ObjectId("{owner["id"]}"), tenant_id:ObjectId("{owner["tenant_id"]}"), '
    f'created_at:now, updated_at:now }});',
    show=False,
)

# ── 3. Mint admin JWT ────────────────────────────────────────────
print("--- minting JWT ---")
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
    cleanup(owner["email"])
    sys.exit(f"!! OTP verify failed: {body[:300]}")
JWT = json.loads(body)["data"]["access_token"]
print(f"    JWT length: {len(JWT)}")

# ── 4. Build the EXACT CSV from the user's report ────────────────
csv_body = "source,destinations,keep_copy\n"
for src in SOURCES:
    csv_body += f"{src},\"{DESTS}\",TRUE\n"
print(f"\nCSV uploaded:\n{csv_body}")

# ── 5. POST as multipart ─────────────────────────────────────────
print("--- POST /api/v1/whm/email/forwarders/bulk-upload ---")
status, body = http(
    "POST",
    "/api/v1/whm/email/forwarders/bulk-upload",
    headers={"Authorization": f"Bearer {JWT}"},
    files={"file": ("smoke_forwarders.csv", csv_body, "text/csv")},
)
print(f"    HTTP {status}")
print(f"    Body: {body[:600]}")
must("HTTP 200 from bulk-upload", status == 200, f"got {status}")

payload = {}
try:
    payload = json.loads(body).get("data", {})
except Exception:
    pass
must(f"response.successes == {len(SOURCES)}", payload.get("successes") == len(SOURCES), json.dumps(payload, default=str)[:300])
must("response.failures == 0", payload.get("failures") == 0, json.dumps(payload, default=str)[:300])

# ── 6. Verify Mongo + Postfix per row ────────────────────────────
print("--- Mongo state ---")
state = mongo(
    f'printjson(db.email_forwarders.find({{domain:"{TEST_DOMAIN}"}}, '
    f'{{source:1, destinations:1, keep_copy:1, _id:0}}).toArray());',
    show=True,
)
for src in SOURCES:
    must(f"Mongo has {src}", src in state, "")
must(f"all rows have keep_copy:true", state.count("keep_copy: true") == len(SOURCES), f"got {state.count('keep_copy: true')}")

# ── 7. THE BIG ONE — virtual_alias_maps lines must include the
#       SOURCE itself appended (because keep_copy=true). Pre-3.1.44
#       the line was just `source → joined dests` and the source
#       mailbox got NO copy. ───────────────────────────────────────
print("--- /etc/postfix/virtual_alias_maps ---")
for src in SOURCES:
    line, _, code = sh(f"grep -E '^{re.escape(src)}\\s' /etc/postfix/virtual_alias_maps || true", show=False)
    print(f"    {line[:200]}")
    must(f"{src} present in virtual_alias_maps", src in line and code == 0)
    must(
        f"{src} keep_copy honoured (source appended to destinations)",
        # The composeForwarderDestinations helper appends the source
        # AFTER the destinations. Check both gmail addresses + the
        # source itself appear on the line.
        "betazeninfotech.sayantan@gmail.com" in line
        and "iamsayantankar@gmail.com" in line
        and line.count(src) == 2,  # once at start (key), once appended in dests
        f"line: {line[:200]}",
    )

# ── 8. postmap .db fresh ────────────────────────────────────────
print("--- postmap .db ---")
out, _, _ = sh("stat -c '%Y' /etc/postfix/virtual_alias_maps.db && date +%s", show=False)
try:
    parts = [int(x) for x in out.split()]
    age = parts[1] - parts[0]
    must("postmap .db rebuilt within 60s", age < 60, f"age={age}s")
except Exception:
    must("postmap .db stat parsed", False, out[:100])

# ── Cleanup + summary ───────────────────────────────────────────
time.sleep(0.3)
cleanup(owner["email"])

print("\n=== summary ===")
if FAILS:
    print(f"FAILED: {len(FAILS)} check(s)")
    for f in FAILS:
        print(f"  - {f}")
    sys.exit(1)
print("ALL CHECKS PASSED — bulk forwarder upload + keep_copy honouring works end-to-end.")
