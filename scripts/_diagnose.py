"""Quick diagnostic: inspect the version.go on the VPS vs the running binary."""
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

cmds = [
    # Where does the panel domain resolve to? Compare with this VPS IP.
    "echo VPS_IP=$(curl -s ifconfig.me); dig +short panel.betazeninfotech.com; getent hosts panel.betazeninfotech.com",
    # Check which backend (if any) is behind the public TLS
    "curl -sI https://panel.betazeninfotech.com/api/v1/version | head -20",
    "curl -s https://panel.betazeninfotech.com/api/v1/version",
    "curl -s -H 'Host: panel.betazeninfotech.com' http://127.0.0.1:80/api/v1/version",
    # Is nginx maybe serving on :443 via a different config that ss missed?
    "ss -tlnp | head -30",
    "ls /etc/nginx/sites-enabled/ 2>/dev/null",
    "nginx -T 2>/dev/null | grep -E 'listen |server_name |proxy_pass ' | head -40",
    # Cloudflare check — the response headers will reveal if a CDN is caching
    "curl -sv https://panel.betazeninfotech.com/api/v1/version 2>&1 | grep -iE 'server:|cf-|via:|x-cache|age:' | head",
]
for cmd in cmds:
    print(f"\n$ {cmd}")
    _, stdout, stderr = c.exec_command(cmd, timeout=30)
    out = stdout.read().decode("utf-8", errors="replace")
    err = stderr.read().decode("utf-8", errors="replace")
    print((out + err).strip())

c.close()
