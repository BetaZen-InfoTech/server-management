#!/usr/bin/env python3
"""End-to-end smoke test for the mailbox Bulk Upload flow — runs ON
THE VPS itself (no SSH layer), so it works as the operator pastes
into a root shell after `ssh root@<vps>`.

Reproduces the user's reported "Bulk email create not to work" by
running the EXACT same code path the WHM Email page → Bulk Upload
modal hits, then dumps every layer of state so the failure is
unambiguous: HTTP code + body, Mongo `mailboxes` row, /etc/dovecot/
users line, /etc/postfix/virtual_mailbox_maps line, postmap .db
mtime, and `doveadm pw` round-trip.

Stdlib-only — no paramiko, no requests, no extra pip installs. Talks
to the panel over localhost:8080 and shells out to mongosh / grep /
sed for filesystem checks.

Run:
    cd /opt/serverpanel
    python3 scripts/_smoke_bulk_mailboxes_local.py

If every check is green the backend bulk path is healthy and any
remaining UI breakage is the v3.1.41 frontend Content-Type fix
(refresh the panel, try again). If a check is red, the row tells you
exactly which layer died.
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
import urllib.request
import urllib.error

BACKEND = "http://127.0.0.1:8080"
TEST_DOMAIN = "smoke-bulk.invalid"
ADDR_A = f"alice@{TEST_DOMAIN}"
ADDR_B = f"bob@{TEST_DOMAIN}"
OTP_CODE = "SMOKE9"
FAILS: list[str] = []


def sh(cmd: str, *, check: bool = False, show: bool = True, input_bytes: bytes | None = None) -> tuple[str, str, int]:
    """Run a shell command, return (stdout, stderr, exit_code)."""
    p = subprocess.run(
        ["bash", "-c", cmd],
        capture_output=True,
        input=input_bytes,
    )
    out = p.stdout.decode("utf-8", errors="replace").strip()
    err = p.stderr.decode("utf-8", errors="replace").strip()
    if show:
        tag = "OK" if p.returncode == 0 else f"exit={p.returncode}"
        print(f"[{tag}] $ {cmd[:140]}")
        for ln in (out + ("\n" + err if err else "")).splitlines()[:15]:
            print(f"    {ln}")
    if check and p.returncode != 0:
        sys.exit(f"!! command failed: {cmd}\n{err}")
    return out, err, p.returncode


def mongo_uri() -> str:
    """Read MONGO_URI from /opt/serverpanel/.env, fall back to local default."""
    env_path = "/opt/serverpanel/.env"
    if os.path.exists(env_path):
        with open(env_path) as f:
            for line in f:
                if line.startswith("MONGO_URI="):
                    return line.split("=", 1)[1].strip().strip('"').strip("'")
    return "mongodb://localhost:27017/serverpanel"


def mongo(query: str, *, show: bool = False) -> str:
    """Run a mongosh query and return its stdout."""
    safe = query.replace("'", "'\\''")
    out, err, code = sh(
        f'mongosh --quiet "{mongo_uri()}" --eval \'{safe}\'',
        show=show,
    )
    if code != 0 and show:
        print(f"    !! mongosh error: {err[:200]}")
    return out


def must(label: str, cond: bool, detail: str = "") -> None:
    tag = "PASS" if cond else "FAIL"
    print(f"    [{tag}] {label}" + (f" — {detail}" if detail else ""))
    if not cond:
        FAILS.append(label)


def http_request(method: str, path: str, *, body=None, headers=None, files=None) -> tuple[int, str, dict]:
    """Make an HTTP request to the local panel. Supports JSON body and
    multipart file upload (when `files` is supplied as
    {field_name: (filename, content, content_type)})."""
    url = BACKEND + path
    hdrs = dict(headers or {})

    data: bytes | None = None
    if files:
        boundary = "----BzPanelSmoke" + secrets.token_hex(8)
        hdrs["Content-Type"] = f"multipart/form-data; boundary={boundary}"
        chunks: list[bytes] = []
        for field, (fname, content, ctype) in files.items():
            if isinstance(content, str):
                content = content.encode("utf-8")
            chunks.append(f"--{boundary}\r\n".encode())
            chunks.append(
                f'Content-Disposition: form-data; name="{field}"; filename="{fname}"\r\n'.encode()
            )
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
            return resp.status, resp.read().decode("utf-8", errors="replace"), dict(resp.headers)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", errors="replace"), dict(e.headers)


def cleanup() -> None:
    print("--- cleanup ---")
    mongo(
        f'db.mailboxes.deleteMany({{domain:"{TEST_DOMAIN}"}}); '
        f'db.domains.deleteMany({{domain:"{TEST_DOMAIN}"}}); '
        f'db.otp_requests.deleteMany({{email:OWNER_EMAIL}});'.replace("OWNER_EMAIL", f'"{OWNER_EMAIL}"'),
        show=False,
    )
    sh(f"sed -i '/@{re.escape(TEST_DOMAIN)}:/d' /etc/dovecot/users 2>/dev/null", show=False)
    sh(f"sed -i '/@{re.escape(TEST_DOMAIN)} /d' /etc/postfix/virtual_mailbox_maps 2>/dev/null", show=False)
    sh(f"sed -i '/^{re.escape(TEST_DOMAIN)} /d' /etc/postfix/virtual_mailbox_domains 2>/dev/null", show=False)
    sh("postmap /etc/postfix/virtual_mailbox_maps 2>/dev/null", show=False)
    sh("postmap /etc/postfix/virtual_mailbox_domains 2>/dev/null", show=False)
    sh("systemctl reload postfix 2>/dev/null", show=False)


# ───────────────────────────────────────────────────────────────────
# 1. Resolve platform owner
# ───────────────────────────────────────────────────────────────────
print("=== Bulk Mailbox Upload Smoke Test ===")
print(f"Mongo URI: {mongo_uri()}")
ow_out = mongo(
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    'print(JSON.stringify({id:u._id.toString(),email:u.email,username:u.username,'
    'tenant_id:(u.tenant_id&&u.tenant_id.toString())||u._id.toString()}));',
    show=False,
)
owner = None
for ln in reversed(ow_out.splitlines()):
    s = ln.strip()
    if s.startswith("{"):
        try:
            owner = json.loads(s)
            break
        except Exception:
            continue
if not owner:
    sys.exit("!! no platform owner found in users collection — is mongosh installed + reachable?")
OWNER_EMAIL = owner["email"]
OWNER_USERNAME = owner["username"]
print(f"owner: {OWNER_EMAIL} (username={OWNER_USERNAME})")

cleanup()

# ───────────────────────────────────────────────────────────────────
# 2. Pre-flight: tooling installed + on PATH (as root, what panel sees)
# ───────────────────────────────────────────────────────────────────
print("--- pre-flight: tooling + permissions ---")
_, _, code = sh("which doveadm", show=False)
must("doveadm on PATH (root shell)", code == 0)
_, _, code = sh("which postmap", show=False)
must("postmap on PATH (root shell)", code == 0)
_, _, code = sh("test -w /etc/dovecot/users", show=False)
must("/etc/dovecot/users writable", code == 0)
_, _, code = sh("test -w /etc/postfix/virtual_mailbox_maps", show=False)
must("/etc/postfix/virtual_mailbox_maps writable", code == 0)
out, _, code = sh("doveadm pw -s SHA512-CRYPT -p TestPassw0rd!", show=False)
must("doveadm pw round-trip", code == 0 and out.startswith("{SHA512-CRYPT}"), out[:80])

# Also — the systemd unit's PATH (this is what the panel ACTUALLY sees, not
# what root's shell sees). If `doveadm` is on root's PATH but not on the
# unit's PATH, the panel's CreateMailbox call hits "exec: doveadm:
# executable file not found" silently.
unit_path, _, _ = sh(
    "systemctl show serverpanel.service -p Environment --value | tr ' ' '\\n' | grep '^PATH=' || echo '(no PATH override; uses systemd default /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin)'",
    show=False,
)
print(f"    serverpanel.service PATH: {unit_path or '(default)'}")
# Resolve doveadm path to compare
doveadm_path, _, _ = sh("which doveadm", show=False)
must(
    f"doveadm dir ({os.path.dirname(doveadm_path)}) reachable from systemd default PATH",
    os.path.dirname(doveadm_path) in "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
    or "PATH=" in unit_path,
    f"doveadm at {doveadm_path}",
)

# ───────────────────────────────────────────────────────────────────
# 3. Seed the test domain so bulkResolveDomain passes
# ───────────────────────────────────────────────────────────────────
mongo(
    f'const now = new Date(); '
    f'db.domains.insertOne({{ '
    f'domain: "{TEST_DOMAIN}", user: "{OWNER_USERNAME}", '
    f'php_version: "8.1", ssl_active: false, suspended: false, '
    f'user_id: ObjectId("{owner["id"]}"), tenant_id: ObjectId("{owner["tenant_id"]}"), '
    f'created_at: now, updated_at: now '
    f'}});',
    show=False,
)

# ───────────────────────────────────────────────────────────────────
# 4. Mint admin JWT via OTP
# ───────────────────────────────────────────────────────────────────
print("--- minting admin JWT via OTP ---")
status, body, _ = http_request("POST", "/api/v1/auth/otp/request", body={
    "email": OWNER_EMAIL,
    "surface": "whm",
})
must("OTP request returns 200", status == 200, body[:200])
code_hash = hashlib.sha256(OTP_CODE.encode()).hexdigest()
mongo(
    f'db.otp_requests.updateMany({{email:"{OWNER_EMAIL}", used:false, expires_at:{{$gt:new Date()}}}}, '
    f'{{$set:{{code_hash:"{code_hash}", attempt_count:0}}}});',
    show=False,
)
status, body, _ = http_request("POST", "/api/v1/auth/otp/verify", body={
    "email": OWNER_EMAIL,
    "code": OTP_CODE,
    "surface": "whm",
})
if status != 200:
    print(f"!! OTP verify failed: status={status} body={body[:300]}")
    cleanup()
    sys.exit(1)
JWT = json.loads(body)["data"]["access_token"]
print(f"    JWT length: {len(JWT)}")

# ───────────────────────────────────────────────────────────────────
# 5. POST the bulk upload — multipart with proper boundary (mimics
#    a healthy browser XHR after the v3.1.41 frontend fix)
# ───────────────────────────────────────────────────────────────────
print("--- POST /api/v1/whm/email/bulk-upload ---")
csv_body = (
    "email,domain,password,quota_mb,send_limit_per_hour,user\n"
    f"{ADDR_A},{TEST_DOMAIN},,512,100,{OWNER_USERNAME}\n"
    f"{ADDR_B},{TEST_DOMAIN},MyOwnP@ss123,1024,200,{OWNER_USERNAME}\n"
)
print(f"CSV:\n{csv_body}")
status, body, _ = http_request(
    "POST",
    "/api/v1/whm/email/bulk-upload",
    headers={"Authorization": f"Bearer {JWT}"},
    files={"file": ("smoke_mailboxes.csv", csv_body, "text/csv")},
)
print(f"    HTTP {status}")
print(f"    Body: {body[:600]}")
must("HTTP 200 from bulk-upload", status == 200, f"got {status}")

payload = {}
try:
    payload = json.loads(body).get("data", {})
except Exception:
    pass
must("response.successes == 2", payload.get("successes") == 2, json.dumps(payload, default=str)[:300])
must("response.failures == 0", payload.get("failures") == 0, json.dumps(payload, default=str)[:300])

# ───────────────────────────────────────────────────────────────────
# 6. Verify every layer of state
# ───────────────────────────────────────────────────────────────────
print("--- check Mongo state ---")
state = mongo(
    f'printjson(db.mailboxes.find({{domain:"{TEST_DOMAIN}"}}, {{email:1, domain:1, quota_mb:1}}).toArray());',
    show=True,
)
must(f"Mongo has {ADDR_A}", ADDR_A in state, state[:200])
must(f"Mongo has {ADDR_B}", ADDR_B in state, state[:200])

print("--- check /etc/dovecot/users ---")
dov, _, _ = sh(f"grep -E '^[a-z]+@{re.escape(TEST_DOMAIN)}:' /etc/dovecot/users || true", show=True)
must(f"dovecot users has {ADDR_A}", ADDR_A in dov, dov[:200])
must(f"dovecot users has {ADDR_B}", ADDR_B in dov, dov[:200])
must("hash starts with $6$ (SHA512-CRYPT)", "$6$" in dov, dov[:200])

print("--- check /etc/postfix/virtual_mailbox_maps ---")
postfix, _, _ = sh(f"grep -E '@{re.escape(TEST_DOMAIN)}' /etc/postfix/virtual_mailbox_maps || true", show=True)
must(f"postfix maps has {ADDR_A}", ADDR_A in postfix, postfix[:200])
must(f"postfix maps has {ADDR_B}", ADDR_B in postfix, postfix[:200])

print("--- check postmap .db is fresh ---")
db_age, _, _ = sh(
    "stat -c '%Y' /etc/postfix/virtual_mailbox_maps.db && date +%s",
    show=False,
)
try:
    parts = [int(x) for x in db_age.split()]
    age = parts[1] - parts[0]
    must("postmap .db rebuilt within last 60s", age < 60, f"age={age}s")
except Exception:
    must("postmap .db stat parsed", False, db_age[:100])

print("--- check Dovecot can authenticate (using auto-generated alice password) ---")
gp = ""
for it in payload.get("items", []):
    if it.get("email") == ADDR_A:
        gp = it.get("generated_password", "")
        break
if not gp:
    must("response carries generated_password for alice", False, "blank → backend never minted it")
else:
    out, err, code = sh(f"doveadm auth test {ADDR_A} {gp!r}", show=True)
    must("doveadm auth test PASSes", code == 0,
         f"exit={code}; out={out[:200]}; err={err[:200]}")

# ───────────────────────────────────────────────────────────────────
# 7. Tail the structured logs from the server while we ran (proves the
#    v3.1.41 logging is in place + shows any silent per-row warnings)
# ───────────────────────────────────────────────────────────────────
print("--- recent serverpanel logs (last 60s, bulk-mailbox lines) ---")
sh("journalctl -u serverpanel --since '60s ago' --no-pager 2>/dev/null | grep -i 'bulk-mailbox\\|mailbox' | tail -40 || true", show=True)

# ───────────────────────────────────────────────────────────────────
# 8. Cleanup + summary
# ───────────────────────────────────────────────────────────────────
time.sleep(0.5)
cleanup()

print("=== summary ===")
if FAILS:
    print(f"FAILED: {len(FAILS)} check(s)")
    for f in FAILS:
        print(f"  - {f}")
    sys.exit(1)
print("ALL CHECKS PASSED — backend bulk mailbox path is healthy.")
print()
print("If the WHM panel UI still says 'doesn't work', the bug is fixed in")
print("v3.1.41 (which dropped the explicit `Content-Type: multipart/form-")
print("data` header that was breaking the browser's boundary). Make sure")
print("the panel was rebuilt + restarted, then HARD-REFRESH the browser")
print("(Ctrl+Shift+R) so the new SPA bundle loads.")
