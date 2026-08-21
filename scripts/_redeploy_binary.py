"""Fast path: rebuild backend on VPS, install, restart. No frontend."""
import os
import re
import sys
from pathlib import Path

import paramiko

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass


def _load_creds_from_local_md(host_keyword: str) -> str:
    """Pull a password from the gitignored `testing-vps-details.md` so
    the script can run without anyone typing the credential into a
    shell command (and thus into the harness transcript)."""
    md = Path(__file__).resolve().parent.parent / "testing-vps-details.md"
    if not md.exists():
        return ""
    text = md.read_text(encoding="utf-8", errors="replace")
    # Pull the section matching the requested keyword ("Old"/"New") and
    # take the first backticked token after `Password:` inside it. Fall
    # back to the whole-file first match if no section header is present.
    section_re = re.compile(rf"#+\s*{host_keyword}\s+server.*?(?=^#|\Z)",
                            re.IGNORECASE | re.DOTALL | re.MULTILINE)
    section = section_re.search(text)
    blob = section.group(0) if section else text
    pwd = re.search(r"password[^`]*`([^`]+)`", blob, re.IGNORECASE)
    return pwd.group(1) if pwd else ""


PASSWORD = os.environ.get("BZ_VPS_PASS") or _load_creds_from_local_md(
    os.environ.get("BZ_VPS_SECTION", "Old"))
if not PASSWORD:
    sys.exit("BZ_VPS_PASS not set and no testing-vps-details.md fallback "
             "found — set BZ_VPS_PASS or populate testing-vps-details.md")

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


def capture(cmd, timeout=60):
    """Run *cmd* quietly and return its stdout, stripped."""
    _, so, _ = c.exec_command(cmd, timeout=timeout)
    return so.read().decode("utf-8", errors="replace").strip()


# --- Stale-frontend guard -------------------------------------------------
# The panel serves each SPA straight from frontend/apps/*/dist, which is
# gitignored — `git reset --hard` NEVER refreshes it. This fast path
# rebuilds ONLY the Go binary, so if the frontend has moved on, a
# binary-only redeploy ships a new backend against a STALE UI. That is
# exactly how the Cloudflare settings page once went live in the API yet
# never appeared in the panel. Refuse in that case; the full deploy
# (_deploy_and_test.py) rebuilds + rsyncs the dists.
prev = capture("cd /opt/serverpanel && git rev-parse HEAD")
r("cd /opt/serverpanel && git fetch origin && git reset --hard origin/main && git log -1 --oneline")
new = capture("cd /opt/serverpanel && git rev-parse HEAD")

# Preferred signal: the build stamp the full deploy writes into the dist,
# recording the commit the served SPA was actually built from. Comparing
# it to HEAD catches *accumulated* staleness (a frontend change shipped by
# an earlier fast-path deploy), not just what this pull introduces.
stamp = capture(
    "cat /opt/serverpanel/frontend/apps/whm/dist/.build-commit 2>/dev/null")
if stamp:
    pending = capture(
        f"cd /opt/serverpanel && git log --oneline {stamp}..HEAD -- frontend/ "
        f"2>/dev/null | head -20")
    reason = (f"the served SPA was built from {stamp[:12]}, which predates "
              f"newer frontend commits")
else:
    # No stamp yet (dist predates this guard): fall back to the delta this
    # pull introduces. Go-forward protection — the first full deploy writes
    # a stamp and upgrades this to full coverage.
    if prev and new and prev != new:
        pending = capture(
            f"cd /opt/serverpanel && git diff --name-only {prev} {new} -- frontend/")
    else:
        pending = ""
    reason = "this update changes frontend files"
if pending:
    print(f"\n[ABORT] {reason} — a binary-only redeploy would leave the "
          f"served SPA stale:")
    for line in pending.splitlines()[:20]:
        print(f"  {line}")
    print("\nUse the full deploy instead (rebuilds + rsyncs the dists):")
    print("  python scripts/_deploy_and_test.py")
    c.close()
    sys.exit(2)

r("cd /opt/serverpanel/backend && /opt/go/1.23/bin/go build -o bin/server ./cmd/server")
r("install -m 0755 /opt/serverpanel/backend/bin/server /opt/serverpanel/bin/server")
r("systemctl restart serverpanel")
r("sleep 2 && curl -s http://127.0.0.1:8080/api/v1/version")
c.close()
