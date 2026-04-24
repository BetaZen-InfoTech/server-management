"""Diagnose: does /auth/otp/request actually send Set-Cookie?"""
import os
import sys
import paramiko

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

PASSWORD = os.environ.get("BZ_VPS_PASS")
if not PASSWORD:
    sys.exit("BZ_VPS_PASS not set — export the VPS root password and re-run")

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(os.environ.get("BZ_VPS_HOST", "187.127.155.209"),
          username=os.environ.get("BZ_VPS_USER", "root"),
          password=PASSWORD, timeout=20,
          look_for_keys=False, allow_agent=False)


def r(cmd):
    _, so, se = c.exec_command(cmd, timeout=30)
    o = so.read().decode("utf-8", errors="replace")
    e = se.read().decode("utf-8", errors="replace")
    print(f"\n$ {cmd}")
    for line in (o + e).strip().splitlines():
        print(f"  {line}")


# Clear any admin OTP rate-limit state by wiping the pending rows.
r('cd /opt/serverpanel; . ./.env 2>/dev/null || true; '
  'URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; '
  'mongosh --quiet "$URI" --eval '
  '"db.otp_requests.deleteMany({email:\\"admin@betazeninfotech.com\\"})"')

# Now re-request with full header dump.
r("curl -s -D /tmp/hdr.txt -o /tmp/body.txt -X POST "
  "http://127.0.0.1:8080/api/v1/auth/otp/request "
  "-H 'Content-Type: application/json' "
  "-d '{\"email\":\"admin@betazeninfotech.com\",\"surface\":\"whm\"}' "
  "-w 'HTTP=%{http_code}\\n'")
r("echo '--- headers ---'; cat /tmp/hdr.txt; echo '--- body ---'; cat /tmp/body.txt; echo")

# Also check journalctl for otp-request logs to see if the service path
# hit the mailer-disabled fallback (which would return a token) or the
# "no such user" early return.
r("journalctl -u serverpanel --since '1 minute ago' | grep -iE 'otp|mailer|smtp' | tail -20")

c.close()
