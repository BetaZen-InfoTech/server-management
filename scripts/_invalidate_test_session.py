"""Revoke the refresh token the E2E test just minted for admin, so the
printed JWT in the transcript isn't a live session anyone could reuse."""
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

_, so, se = c.exec_command(
    'cd /opt/serverpanel; . ./.env 2>/dev/null || true; '
    'URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; '
    'mongosh --quiet "$URI" --eval '
    '\'const r = db.users.updateOne({email:"admin@betazeninfotech.com"}, '
    '{$unset:{refresh_token:"", refresh_expires_at:""}}); '
    'print(JSON.stringify(r));\''
)
out = so.read().decode("utf-8", errors="replace")
err = se.read().decode("utf-8", errors="replace")
print((out + err).strip())
c.close()
