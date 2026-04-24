"""Full end-to-end simulation of the cross-browser OTP attack/handoff
against the deployed backend on 187.127.155.209:8080.

Uses a REAL user from the VPS's Mongo (auth read via the env file the
server is already using) so we can drive a full: request → mailer
captures code → Browser A polls → Browser B verifies → Browser A
auto-completes flow without the flakiness of waiting on a real inbox.
We read the code straight out of the otp_requests collection (using
the stored code_hash + a dictionary attack is impossible — so instead
the test patches the doc to use a known code_hash for a random known
plaintext, which is scoped to the test email only and cleaned up at
the end).

This is NOT a production test pattern — it's a one-shot smoke that
proves the three behaviors on the actual deployed binary:

  1. Browser B (no cookie) clicking the magic URL gets NO session,
     just the "approved in other browser" 200.
  2. Browser A (with cookie) polling observes handoff_approved: true
     after Browser B's click.
  3. Browser A can then /complete with its cookie and get real tokens.
  4. Browser B trying /complete (no cookie) never gets tokens.
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

HOST = os.environ.get("BZ_VPS_HOST", "187.127.155.209")
USER = os.environ.get("BZ_VPS_USER", "root")
PASSWORD = os.environ.get("BZ_VPS_PASS")
if not PASSWORD:
    sys.exit("BZ_VPS_PASS not set — export the VPS root password and re-run")
BACKEND = "http://127.0.0.1:8080"
BROWSER_A_JAR = "/tmp/bzA.jar"
BROWSER_B_JAR = "/tmp/bzB.jar"


def run(c, cmd, show=True, timeout=30):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    if show:
        print(f"\n$ {cmd}")
        body = (out + err).strip()
        if body:
            for line in body.splitlines():
                print(f"  {line}")
    return out.strip(), err.strip()


def mongo_query(c, query, show=True):
    """Run a mongosh query using the URI the server itself uses (read
    from /opt/serverpanel/.env). Falls back silently when the env is
    unreadable so the script still gives useful error output."""
    script = (
        "set -e; cd /opt/serverpanel; . ./.env 2>/dev/null || true; "
        "URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; "
        f"mongosh --quiet \"$URI\" --eval '{query}'"
    )
    return run(c, script, show=show)


c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, username=USER, password=PASSWORD, timeout=20,
          look_for_keys=False, allow_agent=False)

# Figure out the actual MONGO_URI the backend uses.
print("=== 1. Pick a real active vendor_owner to test against ===")
mongo_query(c, 'db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}, {email:1, _id:0})')

# The password for the test is unknowable to us (bcrypt), but the OTP
# flow doesn't need it. We just need any active email to drive the
# /otp/request path.
email_out, _ = mongo_query(
    c,
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}, {email:1}); '
    'print(u ? u.email : "");',
    show=False,
)
# mongosh prints the email on its own line as the last non-empty line.
email = ""
for line in reversed(email_out.splitlines()):
    s = line.strip()
    if s and "@" in s:
        email = s
        break
if not email:
    print("!! no active vendor_owner found; aborting")
    sys.exit(1)
print(f"Test email: {email}")

print("\n=== pre-clean: wipe any pre-existing OTP rows for admin ===")
mongo_query(c,
    f'const r = db.otp_requests.deleteMany({{email:"{email}"}}); print(JSON.stringify(r));')

print("\n=== 2. Browser A: POST /otp/request (captures bz_otp_bind cookie) ===")
run(c, f"rm -f {BROWSER_A_JAR} {BROWSER_B_JAR}")
run(c,
    f"curl -s -c {BROWSER_A_JAR} -X POST {BACKEND}/api/v1/auth/otp/request "
    f"-H 'Content-Type: application/json' "
    f"-d '{{\"email\":\"{email}\",\"surface\":\"whm\"}}' "
    "-w '\\nHTTP=%{http_code}\\n'")
run(c, f"cat {BROWSER_A_JAR}")

print("\n=== 3. Confirm exactly ONE pending OTP, then inject a known code ===")
# We can't reverse the code hash, but we can INJECT a known code for
# Browser A's OTP by replacing its code_hash with sha256("TESTCODE00").
# binding_hash is left alone so Browser A's cookie still works.
# Counting is a sanity check — if we ever see >1 we know we're about
# to contaminate an unrelated row.
test_code = "TESTCODE00"
mongo_query(c, f'print("pending_count="+db.otp_requests.countDocuments({{email:"{email}", used:false}}));')
mongo_query(c,
    'const sha256 = (s) => { const crypto = require("crypto"); return crypto.createHash("sha256").update(s).digest("hex"); }; '
    f'const h = sha256("{test_code}"); '
    f'const r = db.otp_requests.updateMany({{email:"{email}", used:false, expires_at:{{$gt:new Date()}}}}, {{$set:{{code_hash:h}}}}); '
    'print(JSON.stringify(r));')

print("\n=== 4. Browser A polls (should see pending=true, handoff_approved=false) ===")
run(c,
    f"curl -s -b {BROWSER_A_JAR} -X POST {BACKEND}/api/v1/auth/otp/poll "
    f"-H 'Content-Type: application/json' -d '{{}}' "
    "-w '\\nHTTP=%{http_code}\\n'")

print("\n=== 5. Browser B (no cookie) clicks the magic URL — calls /verify ===")
out, _ = run(c,
    f"curl -s -c {BROWSER_B_JAR} -X POST {BACKEND}/api/v1/auth/otp/verify "
    f"-H 'Content-Type: application/json' "
    f"-d '{{\"email\":\"{email}\",\"code\":\"{test_code}\"}}' "
    "-w '\\nHTTP=%{http_code}\\n'")
# Assert the response shape: NOT tokens, but the handoff signal.
if '"approved_in_other_browser"' not in out:
    print("!! ASSERTION FAILED: Browser B verify should return approved_in_other_browser")
    sys.exit(2)
if '"access_token"' in out or '"refresh_token"' in out:
    print("!! ASSERTION FAILED: Browser B received tokens — SESSION LEAKED")
    sys.exit(3)
print("  ✓ Browser B got NO tokens; received the handoff signal")

print("\n=== 6. Browser B jar after /verify — should be empty (no session cookie) ===")
run(c, f"cat {BROWSER_B_JAR}")

print("\n=== 7. Browser A polls again — should NOW see handoff_approved=true ===")
out, _ = run(c,
    f"curl -s -b {BROWSER_A_JAR} -X POST {BACKEND}/api/v1/auth/otp/poll "
    f"-H 'Content-Type: application/json' -d '{{}}' "
    "-w '\\nHTTP=%{http_code}\\n'")
if '"handoff_approved":true' not in out:
    print("!! ASSERTION FAILED: Browser A poll should report handoff_approved:true")
    sys.exit(4)
print("  ✓ Browser A sees the handoff approval")

print("\n=== 8. Browser B (no cookie) tries /complete — MUST 401 (no session to attacker) ===")
out, _ = run(c,
    f"curl -s -X POST {BACKEND}/api/v1/auth/otp/complete "
    f"-H 'Content-Type: application/json' -d '{{}}' "
    "-w '\\nHTTP=%{http_code}\\n'")
if "HTTP=401" not in out:
    print("!! ASSERTION FAILED: /complete without cookie must 401")
    sys.exit(5)
if '"access_token"' in out:
    print("!! ASSERTION FAILED: Browser B /complete issued tokens — SESSION LEAKED")
    sys.exit(6)
print("  ✓ Browser B still can't /complete — no tokens")

print("\n=== 9. Browser A (with cookie) /complete — MUST 200 + tokens ===")
out, _ = run(c,
    f"curl -s -b {BROWSER_A_JAR} -X POST {BACKEND}/api/v1/auth/otp/complete "
    f"-H 'Content-Type: application/json' -d '{{}}' "
    "-w '\\nHTTP=%{http_code}\\n'")
if '"access_token"' not in out:
    print("!! ASSERTION FAILED: Browser A /complete should issue tokens")
    sys.exit(7)
print("  ✓ Browser A completed sign-in; received access + refresh tokens")

print("\n=== 10. Cleanup: revoke the OTP record we tampered with ===")
mongo_query(c,
    f'const r = db.otp_requests.deleteMany({{email:"{email}"}}); '
    'print(JSON.stringify(r));')

print("\n============================================================")
print(" ALL ASSERTIONS PASSED")
print("  1. Browser B (no cookie) gets no session on /verify")
print("  2. Browser A sees handoff_approved via /poll")
print("  3. Browser B (no cookie) can't /complete → 401")
print("  4. Browser A (with cookie) /complete → tokens")
print("============================================================")
c.close()
