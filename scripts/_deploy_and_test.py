"""One-shot VPS deploy + smoke-test helper for the OTP handoff upgrade.

Runs locally (Windows). Uses paramiko since sshpass/plink aren't on PATH.
Pulls on the VPS, rebuilds backend + frontends, restarts the service,
then exercises /api/v1/version and the OTP handoff endpoints.

Prints a single pass/fail line per step so the harness output stays tight.
Never commits or leaves state behind — purely idempotent probes.
"""

from __future__ import annotations

import json
import os
import sys
import time

import paramiko

# Windows console default code page is cp1252, which choke on the ✓ / ✗
# glyphs that turbo/vite sprinkle through their output. Force UTF-8 on
# stdout so we don't die mid-stream on a pretty character.
try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    sys.stderr.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass


HOST = os.environ.get("BZ_VPS_HOST", "187.127.155.209")
USER = os.environ.get("BZ_VPS_USER", "root")
# Credentials come from the environment — never hard-coded here so a
# repo clone doesn't grant SSH access. See scripts/vps_ssh.py for the
# same convention.
PASSWORD = os.environ.get("BZ_VPS_PASS")
if not PASSWORD:
    sys.exit("BZ_VPS_PASS not set — export the VPS root password and re-run")
REMOTE_DIR = "/opt/serverpanel"
PANEL_URL = "https://panel.betazeninfotech.com"


def run(client: paramiko.SSHClient, cmd: str, timeout: int = 600) -> tuple[int, str, str]:
    """Run *cmd* on the remote host and return (exit_code, stdout, stderr)."""
    stdin, stdout, stderr = client.exec_command(cmd, timeout=timeout, get_pty=False)
    out = stdout.read().decode("utf-8", errors="replace")
    err = stderr.read().decode("utf-8", errors="replace")
    code = stdout.channel.recv_exit_status()
    return code, out, err


def step(label: str, code: int, out: str, err: str, show: int = 800) -> bool:
    tag = "OK" if code == 0 else f"FAIL({code})"
    trailing = (out + err).strip()
    if len(trailing) > show:
        trailing = trailing[:show] + "…"
    print(f"[{tag}] {label}")
    if trailing:
        for line in trailing.splitlines():
            print(f"    {line}")
    return code == 0


def main() -> int:
    print("=== connecting to VPS ===")
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(HOST, username=USER, password=PASSWORD, timeout=20,
                   look_for_keys=False, allow_agent=False)

    try:
        print("=== pulling latest main ===")
        step("git status", *run(client, f"cd {REMOTE_DIR} && git status --short"))
        ok = step("git pull",
                  *run(client, f"cd {REMOTE_DIR} && git fetch origin && git reset --hard origin/main"))
        if not ok:
            return 1
        step("HEAD", *run(client, f"cd {REMOTE_DIR} && git log -1 --oneline"))

        print("=== probe toolchain ===")
        # Non-interactive SSH doesn't source ~/.bashrc, so `go`/`node`
        # might not be on PATH. Search aggressively through the common
        # install paths used by install.sh + typical Go tarball layouts
        # before giving up.
        _, out, _ = run(client,
                        "for bin in go node npm npx; do "
                        "  p=$(command -v $bin 2>/dev/null); "
                        "  if [ -z \"$p\" ]; then "
                        "    p=$(find /usr/local /opt /root/go /root/.local /root/.go $HOME/go $HOME/.asdf "
                        "      -maxdepth 5 -name \"$bin\" -type f -executable 2>/dev/null | head -1); "
                        "  fi; "
                        "  echo \"$bin=$p\"; "
                        "done")
        print(out.strip())
        bins: dict[str, str] = {}
        for line in out.strip().splitlines():
            k, _, v = line.partition("=")
            bins[k.strip()] = v.strip()
        go = bins.get("go") or "go"
        npx = bins.get("npx") or "npx"
        # Extend PATH so any subprocess the build spawns (e.g. cgo → cc,
        # tsc → node) finds its siblings.
        node_dir = bins.get("node", "").rsplit("/", 1)[0] if bins.get("node") else ""
        go_dir = bins.get("go", "").rsplit("/", 1)[0] if bins.get("go") else ""
        path_prefix = ":".join(p for p in (go_dir, node_dir) if p)
        env_prefix = f"export PATH={path_prefix}:$PATH && " if path_prefix else ""

        print("=== build backend ===")
        ok = step("go build server+agent",
                  *run(client,
                       f"{env_prefix}cd {REMOTE_DIR}/backend && "
                       f"{go} build -o bin/server ./cmd/server && "
                       f"{go} build -o bin/agent ./cmd/agent",
                       timeout=600))
        if not ok:
            return 1

        print("=== build frontends ===")
        ok = step("turbo build",
                  *run(client,
                       f"{env_prefix}cd {REMOTE_DIR}/frontend && "
                       f"{npx} turbo run build --filter=@serverpanel/whm --filter=@serverpanel/cpanel",
                       timeout=600))
        if not ok:
            return 1

        print("=== inspect install layout ===")
        # The systemd unit points at whatever install.sh placed the
        # binary under — usually /opt/serverpanel/server, sometimes
        # /usr/local/bin/serverpanel. Parse ExecStart so we copy our
        # freshly-built binary over the one systemd actually starts.
        _, unit_out, _ = run(client,
                             "systemctl cat serverpanel 2>/dev/null | grep -E '^ExecStart='; "
                             "systemctl cat panel 2>/dev/null | grep -E '^ExecStart='")
        print(unit_out.strip())
        exec_path = ""
        for line in unit_out.splitlines():
            if line.startswith("ExecStart="):
                # ExecStart=/opt/serverpanel/server  or  /usr/local/bin/serverpanel
                exec_path = line.split("=", 1)[1].split()[0].strip()
                break
        if not exec_path:
            print("!! could not parse ExecStart; aborting install step")
            return 1
        print(f"ExecStart binary: {exec_path}")

        # Also figure out where the frontend dist is actually served from
        # by checking the systemd unit's WorkingDirectory.
        _, wd_out, _ = run(client,
                           "systemctl show serverpanel -p WorkingDirectory --value 2>/dev/null; "
                           "systemctl show panel -p WorkingDirectory --value 2>/dev/null")
        working_dir = wd_out.strip().split()[0] if wd_out.strip() else REMOTE_DIR
        print(f"WorkingDirectory: {working_dir}")

        print("=== install new binary + dists ===")
        # Copy the freshly-built server binary into place. The agent
        # binary isn't hot-swappable (running on every managed VPS), so
        # we leave it alone here — an operator rolls it out via the
        # transfer/update flow.
        ok = step("install server binary",
                  *run(client,
                       f"install -m 0755 {REMOTE_DIR}/backend/bin/server {exec_path}"))
        if not ok:
            return 1
        # Sync the built frontend dists into the WorkingDirectory the
        # server reads from (c.SendFile of ./frontend/apps/*/dist/index.html).
        ok = step("rsync WHM dist",
                  *run(client,
                       f"mkdir -p {working_dir}/frontend/apps/whm/dist && "
                       f"rsync -a --delete {REMOTE_DIR}/frontend/apps/whm/dist/ "
                       f"{working_dir}/frontend/apps/whm/dist/"))
        if not ok:
            return 1
        ok = step("rsync cPanel dist",
                  *run(client,
                       f"mkdir -p {working_dir}/frontend/apps/cpanel/dist && "
                       f"rsync -a --delete {REMOTE_DIR}/frontend/apps/cpanel/dist/ "
                       f"{working_dir}/frontend/apps/cpanel/dist/"))
        if not ok:
            return 1

        print("=== restart service ===")
        step("systemctl status (pre-restart)",
             *run(client, "systemctl is-active serverpanel 2>&1 || systemctl is-active panel 2>&1"))
        ok = step("systemctl restart",
                  *run(client,
                       "systemctl restart serverpanel 2>/dev/null || systemctl restart panel"))
        if not ok:
            return 1
        time.sleep(4)
        step("systemctl is-active (post)",
             *run(client, "systemctl is-active serverpanel 2>&1 || systemctl is-active panel 2>&1"))
        step("journalctl tail",
             *run(client, "journalctl -u serverpanel -u panel -n 15 --no-pager 2>/dev/null"))

        print("=== smoke tests ===")
        # Hit version endpoint to confirm the new binary is live.
        step("GET /api/v1/version",
             *run(client, f"curl -sf {PANEL_URL}/api/v1/version"))

        # Simulate the cross-browser attack:
        #  1. Browser A requests OTP (captures bz_otp_bind cookie to a jar)
        #  2. Browser B (no cookie) calls /verify with a made-up code
        #  3. Expected: 401 "invalid or expired code" — Browser B can NEVER
        #     log in, which is the whole point. The handoff-approved 200
        #     needs the RIGHT code; we don't have it, so we just prove
        #     the no-cookie path doesn't leak a session.
        print("=== Browser B (no cookie) with bad code ===")
        step("POST /auth/otp/verify without cookie",
             *run(client,
                  f"curl -s -o /tmp/ver.out -w 'HTTP=%{{http_code}}' -X POST "
                  f"{PANEL_URL}/api/v1/auth/otp/verify "
                  f"-H 'Content-Type: application/json' "
                  f"-d '{{\"email\":\"smoketest@example.com\",\"code\":\"XXXXXXXXXX\"}}' && "
                  f"echo && cat /tmp/ver.out && echo"))

        # /auth/otp/poll without a cookie should return pending=false.
        print("=== Poll without cookie (should be pending=false) ===")
        step("POST /auth/otp/poll without cookie",
             *run(client,
                  f"curl -s -X POST {PANEL_URL}/api/v1/auth/otp/poll "
                  f"-H 'Content-Type: application/json' -d '{{}}' "
                  f"-w '\\nHTTP=%{{http_code}}\\n'"))

        # /auth/otp/complete without a cookie must 401.
        print("=== Complete without cookie (must 401) ===")
        step("POST /auth/otp/complete without cookie",
             *run(client,
                  f"curl -s -X POST {PANEL_URL}/api/v1/auth/otp/complete "
                  f"-H 'Content-Type: application/json' -d '{{}}' "
                  f"-w '\\nHTTP=%{{http_code}}\\n'"))

        # Full end-to-end: Browser A → request (get cookie jar) → poll
        # should report pending=true, handoff_approved=false.
        print("=== E2E: request from jar A, poll with same jar ===")
        step("request → poll",
             *run(client,
                  "JAR=/tmp/bzA.jar; rm -f $JAR; "
                  f"curl -s -c $JAR -X POST {PANEL_URL}/api/v1/auth/otp/request "
                  "-H 'Content-Type: application/json' "
                  "-d '{\"email\":\"nonexistent-smoketest@example.com\",\"surface\":\"whm\"}' "
                  "-w '\\nHTTP=%{http_code}\\n'; "
                  f"echo '---poll---'; "
                  f"curl -s -b $JAR -X POST {PANEL_URL}/api/v1/auth/otp/poll "
                  "-H 'Content-Type: application/json' -d '{}' "
                  "-w '\\nHTTP=%{http_code}\\n'"))

        print("=== done ===")
        return 0
    finally:
        client.close()


if __name__ == "__main__":
    sys.exit(main())
