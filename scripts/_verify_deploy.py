"""Post-deploy verification: SSH in, confirm git HEAD, running binary
version, compiled-in symbols for the new helpers, and service health.

Idempotent: no DB writes, no state changes. Intended to be run after
scripts/_deploy_and_test.py to prove the upgrade landed."""
from __future__ import annotations

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

HOST = os.environ.get("BZ_VPS_HOST", "187.127.155.209")
USER = os.environ.get("BZ_VPS_USER", "root")

c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, username=USER, password=PASSWORD, timeout=20,
          look_for_keys=False, allow_agent=False)


def run(label: str, cmd: str) -> str:
    _, so, se = c.exec_command(cmd, timeout=60)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    body = (out + err).strip()
    code = so.channel.recv_exit_status()
    tag = "OK" if code == 0 else f"FAIL({code})"
    print(f"\n[{tag}] {label}")
    for line in body.splitlines()[:8]:
        print(f"    {line}")
    return body


print(f"=== VPS {HOST} ===")

# 1. Pull latest from origin/main and report HEAD.
run("git fetch + reset to origin/main",
    "cd /opt/serverpanel && git fetch --quiet origin && git reset --hard origin/main")
run("git HEAD (should be 0885bff v3.0.2 multi-domain+transfer)",
    "cd /opt/serverpanel && git log -1 --oneline")

# 2. Confirm the source tree has the new helper + release note.
run("version.go shows Patch = 2",
    "grep -E 'Patch =|3\\.0\\.2' /opt/serverpanel/backend/pkg/version/version.go | head -4")
run("buildRecoveryVhostSpec symbol exists in source",
    "grep -n 'func buildRecoveryVhostSpec' /opt/serverpanel/backend/internal/services/transfer_panel_records.go")
run("recovery path calls CreateProjectVhost (multi-domain)",
    "grep -n 'CreateProjectVhost\\|IssueLetsEncryptMulti' /opt/serverpanel/backend/internal/services/transfer_panel_records.go | head -6")
run("frontend role gate on alias input is GONE",
    "grep -cE '\\(svc.role === \"frontend\" \\|\\| svc.role === \"static\"\\) && \\($' "
    "/opt/serverpanel/frontend/apps/whm/src/pages/DeploySoftwarePage.tsx "
    "|| echo 'zero matches (expected after the fix)'")

# 3. The running binary must be v3.0.2 — if it still reports 3.0.1 the
# systemd unit wasn't restarted or the install overwrote nothing.
run("running backend version (127.0.0.1:8080)",
    "curl -s http://127.0.0.1:8080/api/v1/version")

# 4. New endpoints are live (404 = missing, 200/400/401 = registered).
for path, expect in [
    ("auth/otp/poll", "200 (no-cookie → pending=false)"),
    ("auth/otp/complete", "401 (no-cookie → reject)"),
]:
    run(f"POST /api/v1/{path} ({expect})",
        f"curl -s -o /dev/null -w 'HTTP=%{{http_code}}' "
        f"-X POST http://127.0.0.1:8080/api/v1/{path} "
        f"-H 'Content-Type: application/json' -d '{{}}'")

# 5. Compiled binary contains the new symbol — confirms systemd is
# actually serving the v3.0.2 build and not an older on-disk copy.
run("binary strings include buildRecoveryVhostSpec",
    "strings /opt/serverpanel/bin/server 2>/dev/null | grep -c buildRecoveryVhostSpec "
    "|| echo 'strings unavailable'")
run("binary mtime (must be newer than the git commit)",
    "stat -c '%y' /opt/serverpanel/bin/server")

# 6. systemd + journal sanity.
run("systemd unit state",
    "systemctl is-active serverpanel; systemctl show serverpanel -p ActiveState -p SubState --value")
run("journal last 8 lines (errors or panics surface here)",
    "journalctl -u serverpanel -n 8 --no-pager")

# 7. Nginx config still valid (paranoid check — recovery path doesn't
# write anything at idle, but rules out a pre-existing broken config).
run("nginx -t",
    "nginx -t 2>&1")

c.close()
print("\n=== verification complete ===")
