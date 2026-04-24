"""Fast path: rebuild backend on VPS, install, restart. No frontend."""
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


def r(cmd, timeout=600):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    code = so.channel.recv_exit_status()
    tag = "OK" if code == 0 else f"FAIL({code})"
    print(f"\n[{tag}] $ {cmd}")
    body = (out + err).strip()
    if body:
        for line in body.splitlines()[:30]:
            print(f"  {line}")
    return code


r("cd /opt/serverpanel && git fetch origin && git reset --hard origin/main && git log -1 --oneline")
r("cd /opt/serverpanel/backend && /opt/go/1.23/bin/go build -o bin/server ./cmd/server")
r("install -m 0755 /opt/serverpanel/backend/bin/server /opt/serverpanel/bin/server")
r("systemctl restart serverpanel")
r("sleep 2 && curl -s http://127.0.0.1:8080/api/v1/version")
c.close()
