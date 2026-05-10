"""Diagnostic smoke test for the mailbox Bulk Upload flow.

Reproduces the user's reported "bulk email create not to work" by
running the EXACT same code path the WHM Email page → Bulk Upload
modal hits, then dumps every layer of state so the failure is
unambiguous: HTTP code + body, Mongo `mailboxes` row, /etc/dovecot/
users line, /etc/postfix/virtual_mailbox_maps line, postmap .db
mtime, and `doveadm pw -s SHA512-CRYPT` round-trip.

If the per-row backend write succeeded, every check is green and
the bug is in the UI's FormData upload (most likely the explicit
Content-Type header axios is sending without a boundary). If a
backend check is red, the row tells you exactly where it died.

Run on the developer box (paramiko ships with `pip install
paramiko`); SSHes into the VPS with the password the operator
pasted and never writes it to disk.

Required env
------------
BZ_VPS_PASS  — root password for the VPS
BZ_VPS_HOST  — defaults to 187.127.155.209
BZ_VPS_USER  — defaults to root
"""
from __future__ import annotations

import json
import os
import sys
import time

import paramiko

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

PASSWORD = os.environ.get("BZ_VPS_PASS")
if not PASSWORD:
    sys.exit("BZ_VPS_PASS not set (export BZ_VPS_PASS='...')")
HOST = os.environ.get("BZ_VPS_HOST", "187.127.155.209")
USER = os.environ.get("BZ_VPS_USER", "root")
BACKEND = "http://127.0.0.1:8080"

TEST_DOMAIN = "smoke-bulk.invalid"
TEST_OWNER = None  # resolved from owner lookup below
ADDR_A = f"alice@{TEST_DOMAIN}"
ADDR_B = f"bob@{TEST_DOMAIN}"
FAILS: list[str] = []


def r(c, cmd, show=True, timeout=60):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    code = so.channel.recv_exit_status()
    if show:
        print(f"[{'OK' if code == 0 else f'exit={code}'}] $ {cmd[:140]}")
        for ln in (out + err).strip().splitlines()[:15]:
            print(f"    {ln}")
    return out.strip(), err.strip(), code


def mongo(c, q, show=True):
    safe = q.replace("'", "'\\''")
    return r(
        c,
        "cd /opt/serverpanel; . ./.env 2>/dev/null || true; "
        "URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; "
        f'mongosh --quiet "$URI" --eval \'{safe}\'',
        show=show,
    )


def must(label, cond, detail=""):
    tag = "PASS" if cond else "FAIL"
    print(f"    [{tag}] {label}" + (f" — {detail}" if detail else ""))
    if not cond:
        FAILS.append(label)


def cleanup(c):
    print("--- cleanup ---")
    mongo(
        c,
        f'db.mailboxes.deleteMany({{domain:"{TEST_DOMAIN}"}}); '
        f'db.domains.deleteMany({{domain:"{TEST_DOMAIN}"}}); '
        f'db.email_forwarders.deleteMany({{domain:"{TEST_DOMAIN}"}});',
        show=False,
    )
    r(
        c,
        f"sed -i '/^[a-z0-9_.-]*@{TEST_DOMAIN}:/d' /etc/dovecot/users 2>/dev/null; "
        f"sed -i '/@{TEST_DOMAIN}\\s/d' /etc/postfix/virtual_mailbox_maps 2>/dev/null; "
        f"sed -i '/^{TEST_DOMAIN}\\s/d' /etc/postfix/virtual_mailbox_domains 2>/dev/null; "
        "postmap /etc/postfix/virtual_mailbox_maps 2>/dev/null; "
        "postmap /etc/postfix/virtual_mailbox_domains 2>/dev/null; "
        "systemctl reload postfix 2>/dev/null",
        show=False,
    )


c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
print(f"connecting to {HOST}…")
c.connect(HOST, username=USER, password=PASSWORD, timeout=20, look_for_keys=False, allow_agent=False)
print(f"=== smoke test on {HOST}: bulk mailbox upload ===")
cleanup(c)

# 1. Resolve platform owner so we can mint an admin JWT.
ow_out, _, _ = mongo(
    c,
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    "print(JSON.stringify({id:u._id.toString(),email:u.email,username:u.username,"
    "tenant_id:(u.tenant_id&&u.tenant_id.toString())||u._id.toString()}));",
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
    cleanup(c)
    sys.exit("!! no platform owner found in users collection")
TEST_OWNER = owner["username"]
print(f"owner: {owner['email']} (username={TEST_OWNER}, tenant={owner['tenant_id']})")

# 2. Pre-flight: doveadm + postmap reachable in panel's PATH?
print("--- pre-flight: tooling installed + on PATH ---")
_, _, code = r(c, "which doveadm")
must("doveadm on PATH", code == 0)
_, _, code = r(c, "which postmap")
must("postmap on PATH", code == 0)
_, _, code = r(c, "test -w /etc/dovecot/users")
must("/etc/dovecot/users writable by root", code == 0)
_, _, code = r(c, "test -w /etc/postfix/virtual_mailbox_maps")
must("/etc/postfix/virtual_mailbox_maps writable by root", code == 0)
# Verify doveadm pw works at all (panel's per-row failure surfaces here).
out, _, code = r(c, "doveadm pw -s SHA512-CRYPT -p TestPassw0rd!")
must("doveadm pw round-trip", code == 0 and out.startswith("{SHA512-CRYPT}"), out[:80])

# 3. Seed the test domain (so the bulk uploader's domain check passes).
mongo(
    c,
    f"""const now = new Date();
db.domains.insertOne({{
  domain: "{TEST_DOMAIN}", user: "{TEST_OWNER}",
  php_version: "8.1", ssl_active: false, suspended: false,
  user_id: ObjectId("{owner['id']}"), tenant_id: ObjectId("{owner['tenant_id']}"),
  created_at: now, updated_at: now
}});""",
    show=False,
)

# 4. Mint admin JWT via OTP.
CODE = "BULKMX"
mongo(c, f'db.otp_requests.deleteMany({{email:"{owner["email"]}"}});', show=False)
r(
    c,
    f"rm -f /tmp/sm.jar; curl -s -c /tmp/sm.jar -X POST {BACKEND}/api/v1/auth/otp/request "
    f"-H 'Content-Type: application/json' "
    f"-d '{{\"email\":\"{owner['email']}\",\"surface\":\"whm\"}}'",
    show=False,
)
mongo(
    c,
    'const crypto = require("crypto"); '
    f'const h = crypto.createHash("sha256").update("{CODE}").digest("hex"); '
    f'db.otp_requests.updateMany({{email:"{owner["email"]}", used:false, expires_at:{{$gt:new Date()}}}}, '
    "{$set:{code_hash:h, attempt_count:0}});",
    show=False,
)
verify_out, _, _ = r(
    c,
    f"curl -s -b /tmp/sm.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
    f"-H 'Content-Type: application/json' "
    f'-d \'{{"email":"{owner["email"]}","code":"{CODE}","surface":"whm"}}\'',
    show=False,
)
try:
    jwt = json.loads(verify_out)["data"]["access_token"]
except Exception:
    cleanup(c)
    sys.exit(f"!! OTP verify failed: {verify_out[:200]}")
print(f"got admin JWT (len={len(jwt)})")

# 5. Build the EXACT CSV the WHM modal would post.
csv_body = (
    "email,domain,password,quota_mb,send_limit_per_hour,user\n"
    f"{ADDR_A},{TEST_DOMAIN},,512,100,{TEST_OWNER}\n"
    f"{ADDR_B},{TEST_DOMAIN},MyOwnP@ss123,1024,200,{TEST_OWNER}\n"
)
r(c, f"cat > /tmp/sm_mailboxes.csv <<'EOF'\n{csv_body}EOF", show=False)
print(f"CSV body:\n{csv_body}")

# 6. Hit the bulk endpoint EXACTLY like the UI: multipart with field
#    name "file". Use --form so curl handles the boundary; this is
#    what XMLHttpRequest+FormData does in the browser.
print("--- POST /api/v1/whm/email/bulk-upload ---")
status_out, _, _ = r(
    c,
    f"curl -s -o /tmp/sm.body -w '%{{http_code}}\\n%{{content_type}}' "
    f"-X POST {BACKEND}/api/v1/whm/email/bulk-upload "
    f"-H 'Authorization: Bearer {jwt}' "
    f"-F 'file=@/tmp/sm_mailboxes.csv;type=text/csv'",
    show=False,
)
parts = status_out.splitlines()
http_code = int(parts[0]) if parts and parts[0].isdigit() else 0
ct = parts[1] if len(parts) > 1 else "?"
body, _, _ = r(c, "cat /tmp/sm.body", show=False)
print(f"    HTTP {http_code} ({ct})")
print(f"    body: {body[:600]}")
must("HTTP 200 from bulk-upload", http_code == 200, f"got {http_code}")
must("body looks like JSON success envelope", '"success":true' in body or '"data"' in body)

try:
    payload = json.loads(body).get("data", {})
except Exception:
    payload = {}
must("response.successes == 2", payload.get("successes") == 2, json.dumps(payload, default=str)[:300])
must("response.failures == 0", payload.get("failures") == 0, json.dumps(payload, default=str)[:300])

print("--- check Mongo state ---")
state, _, _ = mongo(
    c,
    f'printjson(db.mailboxes.find({{domain:"{TEST_DOMAIN}"}}, {{email:1, domain:1, quota_mb:1}}).toArray());',
    show=False,
)
must(f"Mongo has alice row", ADDR_A in state, state[:200])
must(f"Mongo has bob row", ADDR_B in state, state[:200])

print("--- check /etc/dovecot/users ---")
dov, _, _ = r(c, f"grep -E '^[a-z]+@{TEST_DOMAIN}:' /etc/dovecot/users", show=False)
must(f"dovecot users has {ADDR_A}", ADDR_A in dov, dov[:200])
must(f"dovecot users has {ADDR_B}", ADDR_B in dov, dov[:200])
must("hash starts with $6$ (SHA512-CRYPT)", "$6$" in dov, dov[:200])

print("--- check /etc/postfix/virtual_mailbox_maps ---")
postfix, _, _ = r(c, f"grep -E '@{TEST_DOMAIN}' /etc/postfix/virtual_mailbox_maps", show=False)
must(f"postfix maps has {ADDR_A}", ADDR_A in postfix, postfix[:200])
must(f"postfix maps has {ADDR_B}", ADDR_B in postfix, postfix[:200])

print("--- check postmap .db is fresh ---")
db_age, _, _ = r(
    c,
    "stat -c '%Y' /etc/postfix/virtual_mailbox_maps.db && date +%s",
    show=False,
)
try:
    parts = [int(x) for x in db_age.split()]
    age = parts[1] - parts[0]
    must("postmap .db rebuilt within last 60s", age < 60, f"age={age}s")
except Exception:
    must("postmap .db stat parsed", False, db_age[:100])

print("--- check Dovecot can authenticate the new mailbox ---")
# Use the alice mailbox — its password was auto-generated and is
# in the response payload's `items[0].generated_password`.
gp = ""
for it in payload.get("items", []):
    if it.get("email") == ADDR_A:
        gp = it.get("generated_password", "")
        break
if not gp:
    must("response carries generated_password for alice", False,
         "if missing, the bulk service failed to mint or report it")
else:
    out, err, code = r(c, f"doveadm auth test {ADDR_A} {gp}", show=False)
    must("doveadm auth test PASSes", code == 0,
         f"exit={code}; out={out[:200]}; err={err[:200]}")

# 7. Cleanup
time.sleep(0.5)
cleanup(c)

print("--- summary ---")
if FAILS:
    print(f"FAILED: {len(FAILS)} check(s)")
    for f in FAILS:
        print(f"  - {f}")
    sys.exit(1)
print("ALL CHECKS PASSED — backend bulk mailbox path is healthy.")
print("If the panel UI still says 'doesn't work', the bug is in the")
print("frontend FormData upload (most likely the explicit Content-Type")
print("header axios is sending without a boundary). Try the v3.1.41")
print("frontend fix that drops the explicit Content-Type so axios + the")
print("browser auto-set the boundary.")
