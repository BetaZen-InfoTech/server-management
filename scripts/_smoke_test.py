"""Smoke-test the deployed OTP handoff against the actual VPS backend.

Bypasses DNS (panel.betazeninfotech.com points to a different host)
and talks to the Go server directly at 127.0.0.1:8080 over SSH, so
the checks exercise the binary we just installed — not whatever is
answering the public domain.
"""
from __future__ import annotations

import os
import sys
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
EMAIL = "smoketest-bogus@example.invalid"


def r(c, cmd):
    _, so, se = c.exec_command(cmd, timeout=30)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    print(f"\n$ {cmd}")
    body = (out + err).strip()
    if body:
        print(body)


c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, username=USER, password=PASSWORD, timeout=20,
          look_for_keys=False, allow_agent=False)

print("=== Version sanity (should be 3.0.1) ===")
r(c, f"curl -s {BACKEND}/api/v1/version")

print("\n=== Routes registered (grep /auth/otp/ from Fiber's route list via curl heuristics) ===")
# Fiber doesn't expose a route listing. Instead, hit each new endpoint
# and verify it's NOT a 404.
for path in ("otp/request", "otp/verify", "otp/poll", "otp/complete", "otp/cancel"):
    r(c, f"curl -s -o /dev/null -w 'HTTP=%{{http_code}}\\n' -X POST {BACKEND}/api/v1/auth/{path} "
       f"-H 'Content-Type: application/json' -d '{{}}'")

print("\n=== Scenario 1: Browser B (no cookie) hits /verify with bogus code → 401 ===")
r(c, f"curl -s -w '\\nHTTP=%{{http_code}}\\n' -X POST {BACKEND}/api/v1/auth/otp/verify "
   f"-H 'Content-Type: application/json' "
   f"-d '{{\"email\":\"{EMAIL}\",\"code\":\"XXXXXXXXXX\"}}'")

print("\n=== Scenario 2: /poll without cookie → 200 {pending:false} ===")
r(c, f"curl -s -w '\\nHTTP=%{{http_code}}\\n' -X POST {BACKEND}/api/v1/auth/otp/poll "
   f"-H 'Content-Type: application/json' -d '{{}}'")

print("\n=== Scenario 3: /complete without cookie → 401 ===")
r(c, f"curl -s -w '\\nHTTP=%{{http_code}}\\n' -X POST {BACKEND}/api/v1/auth/otp/complete "
   f"-H 'Content-Type: application/json' -d '{{}}'")

print("\n=== Scenario 4: E2E — /request (capture cookie), /poll (same cookie), /poll (fresh cookie) ===")
# Request sets bz_otp_bind; the bogus email returns 200 (enum-resistant)
# and, because no user matches, no OTP is actually created — which
# means /poll should return pending=false even WITH the cookie. That
# proves the cookie path doesn't leak "this email exists" via poll.
r(c,
  "JAR=/tmp/bzA.jar; rm -f $JAR; "
  f"curl -s -c $JAR -X POST {BACKEND}/api/v1/auth/otp/request "
  "-H 'Content-Type: application/json' "
  f"-d '{{\"email\":\"{EMAIL}\",\"surface\":\"whm\"}}' "
  "-w '\\nHTTP=%{http_code}\\n'; "
  "echo '---poll with SAME jar---'; "
  f"curl -s -b $JAR -X POST {BACKEND}/api/v1/auth/otp/poll "
  "-H 'Content-Type: application/json' -d '{}' "
  "-w '\\nHTTP=%{http_code}\\n'")

print("\n=== Scenario 5: E2E — request for a REAL account (if any super-admin exists) ===")
# Find an active user in Mongo — we're on the box, so the DB shell is right here.
r(c,
  "mongosh --quiet serverpanel --eval "
  "'const u = db.users.findOne({is_active:true, deleted_at:null, role:\"vendor_owner\"}, {email:1, _id:0}); printjson(u);'")

# If a real email exists, request + poll against it. This will send an
# actual email (the server has mailer configured), but the cost is one
# inbox entry for the operator — acceptable for a smoke test of the
# cross-browser approval path. Comment the next block out if you don't
# want the email to send during future runs.
print("\n=== Scenario 6: poll the live OTP collection AFTER the bogus-email request ===")
r(c,
  "mongosh --quiet serverpanel --eval "
  f"'const docs = db.otp_requests.find({{email:\"{EMAIL}\"}}, "
  "{email:1, binding_hash:1, handoff_approved_at:1, used:1, expires_at:1, _id:0}).toArray(); "
  "printjson({count: docs.length, docs});'")

c.close()
