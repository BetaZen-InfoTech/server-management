#!/usr/bin/env python3
"""
Tiny SSH helper for the BetaZen panel VPS.

Reads command from stdin or argv, runs it on root@<BZ_VPS_HOST>
via paramiko, prints stdout + stderr, exits with the remote exit code.

Lives in scripts/ so it's discoverable but is intentionally NOT shipped
to the live VPS — it's a developer-side convenience for the agent
workflow. Credentials are passed via env vars so they don't end up in
shell history or argv:

    BZ_VPS_HOST=187.127.179.98 BZ_VPS_USER=root BZ_VPS_PASS=... \\
        python scripts/vps_ssh.py "uptime"

Optional env:
    BZ_VPS_PORT   SSH port (default 22)
    BZ_VPS_PASS   root/user password (no key auth by default)

Before attempting the SSH handshake we do a fast TCP preflight so an
unreachable port produces a one-line, actionable diagnostic instead of
a 30s hang and a paramiko traceback. Falls back to the documented
production defaults when env is empty, which matches the workflow saved
in the agent's memory.
"""
from __future__ import annotations

import os
import socket
import sys

import paramiko

# Windows consoles default to a legacy code page (cp1252); remote command
# output frequently contains UTF-8 (emoji, non-Latin app logs). Force UTF-8
# on our stdio so streaming never dies with UnicodeEncodeError mid-output.
for _stream in (sys.stdout, sys.stderr):
    try:
        _stream.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass


def preflight(host: str, port: int, timeout: float = 8.0) -> str | None:
    """Return None if host:port accepts a TCP connection, else a short
    human-readable reason. Catches the 'SSH is firewalled but the box is
    up' case before we waste 30s on the SSH auth timeout."""
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return None
    except socket.timeout:
        return (f"TCP connect to {host}:{port} timed out after {timeout:.0f}s -- "
                f"port {port} is filtered/closed or the host is dropping packets. "
                f"This is a server-side firewall/network issue, not a script bug.")
    except socket.gaierror as e:
        return f"DNS/address error resolving {host!r}: {e}"
    except OSError as e:
        return f"TCP connect to {host}:{port} failed: {e}"


def run(host: str, port: int, user: str, password: str, cmd: str,
        timeout: int = 600) -> int:
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    try:
        client.connect(
            hostname=host,
            port=port,
            username=user,
            password=password,
            timeout=30,
            banner_timeout=30,
            auth_timeout=30,
            look_for_keys=False,
            allow_agent=False,
        )
    except paramiko.AuthenticationException:
        print(f"error: authentication failed for {user}@{host}:{port} — "
              f"check BZ_VPS_USER / BZ_VPS_PASS.", file=sys.stderr)
        return 4
    except (paramiko.SSHException, OSError) as e:
        print(f"error: could not establish SSH session to {host}:{port}: {e}",
              file=sys.stderr)
        return 3
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
    host = os.environ.get("BZ_VPS_HOST", "187.127.179.98")
    user = os.environ.get("BZ_VPS_USER", "root")
    password = os.environ.get("BZ_VPS_PASS", "")
    try:
        port = int(os.environ.get("BZ_VPS_PORT", "22"))
    except ValueError:
        print("error: BZ_VPS_PORT must be an integer", file=sys.stderr)
        return 2
    if not password:
        print("error: BZ_VPS_PASS is empty -- set the password via env so it "
              "stays out of argv/shell history.", file=sys.stderr)
        return 2

    if len(sys.argv) > 1:
        cmd = " ".join(sys.argv[1:])
    else:
        cmd = sys.stdin.read()
    cmd = cmd.strip()
    if not cmd:
        print("usage: vps_ssh.py '<command>'  OR  echo cmd | vps_ssh.py", file=sys.stderr)
        return 2

    reason = preflight(host, port)
    if reason is not None:
        print(f"error: {reason}", file=sys.stderr)
        print(f"hint: the box may still be reachable on other ports (e.g. 443 "
              f"for the panel). Verify SSH is listening and the firewall allows "
              f"your IP on port {port}.", file=sys.stderr)
        return 5

    return run(host, port, user, password, cmd)


if __name__ == "__main__":
    sys.exit(main())
