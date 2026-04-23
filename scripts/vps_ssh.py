#!/usr/bin/env python3
"""
Tiny SSH helper for the BetaZen panel VPS.

Reads command from stdin or argv, runs it on root@187.127.155.209
via paramiko, prints stdout + stderr, exits with the remote exit code.

Lives in scripts/ so it's discoverable but is intentionally NOT shipped
to the live VPS — it's a developer-side convenience for the agent
workflow. Credentials are passed via env vars so they don't end up in
shell history or argv:

    BZ_VPS_HOST=187.127.155.209 BZ_VPS_USER=root BZ_VPS_PASS=... \\
        python scripts/vps_ssh.py "uptime"

Falls back to the documented production defaults when env is empty,
which matches the workflow saved in the agent's memory.
"""
from __future__ import annotations

import os
import sys
import paramiko


def run(host: str, user: str, password: str, cmd: str, timeout: int = 600) -> int:
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(
        hostname=host,
        username=user,
        password=password,
        timeout=30,
        banner_timeout=30,
        auth_timeout=30,
        look_for_keys=False,
        allow_agent=False,
    )
    try:
        stdin, stdout, stderr = client.exec_command(cmd, timeout=timeout, get_pty=False)
        # Stream as it arrives so long-running commands surface progress.
        out_chan = stdout.channel
        out_chan.settimeout(timeout)
        while True:
            if out_chan.recv_ready():
                data = out_chan.recv(65535)
                if data:
                    sys.stdout.write(data.decode("utf-8", "replace"))
                    sys.stdout.flush()
            if out_chan.recv_stderr_ready():
                data = out_chan.recv_stderr(65535)
                if data:
                    sys.stderr.write(data.decode("utf-8", "replace"))
                    sys.stderr.flush()
            if out_chan.exit_status_ready():
                # drain remaining
                while out_chan.recv_ready():
                    sys.stdout.write(out_chan.recv(65535).decode("utf-8", "replace"))
                while out_chan.recv_stderr_ready():
                    sys.stderr.write(out_chan.recv_stderr(65535).decode("utf-8", "replace"))
                break
        return out_chan.recv_exit_status()
    finally:
        client.close()


def main() -> int:
    host = os.environ.get("BZ_VPS_HOST", "187.127.155.209")
    user = os.environ.get("BZ_VPS_USER", "root")
    password = os.environ.get("BZ_VPS_PASS", "BetaZen@2023")
    if len(sys.argv) > 1:
        cmd = " ".join(sys.argv[1:])
    else:
        cmd = sys.stdin.read()
    cmd = cmd.strip()
    if not cmd:
        print("usage: vps_ssh.py '<command>'  OR  echo cmd | vps_ssh.py", file=sys.stderr)
        return 2
    return run(host, user, password, cmd)


if __name__ == "__main__":
    sys.exit(main())
