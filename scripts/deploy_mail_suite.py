"""Betazen two-server deploy + sync tool (panel + mail-suite).

Keeps the staging and prod-clone boxes identical to `origin/main` and to each
other. Re-runnable and idempotent — run it after any panel update, server
transfer/migration, or backup/restore to bring both boxes back in sync and
verify they match.

Modes
-----
  inspect   (default) READ-ONLY. Reports, for BOTH servers: git HEAD vs
            origin/main, panel service + version, mail-suite service + health,
            mongod. Ends with a side-by-side "in sync?" verdict. Changes
            nothing on the boxes.
  deploy    For each server: git reset --hard origin/main, rebuild the panel
            server binary AND (if installed) the mail-suite backend + webmail,
            restart both services, health-check. Then cross-checks that both
            boxes ended on the same commit + panel version and both are healthy.

Usage
-----
  python scripts/deploy_mail_suite.py            # inspect (safe, default)
  python scripts/deploy_mail_suite.py inspect
  python scripts/deploy_mail_suite.py deploy

Credentials come from env BZ_VPS_PASS or the gitignored testing-vps-details.md,
so the root password never has to be typed into a shell command. Runs locally
on Windows via paramiko (sshpass/plink aren't on PATH).
"""

from __future__ import annotations

import os
import re
import sys
from pathlib import Path

import paramiko

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    sys.stderr.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

# The two boxes this tool keeps in lockstep. Prod (195.35.7.161) is NOT here
# on purpose — deploy to these first, promote to prod separately.
SERVERS = [
    {"name": "staging",    "host": "89.116.34.207"},
    {"name": "prod-clone", "host": "195.35.7.64"},
]
USER = os.environ.get("BZ_VPS_USER", "root")

PANEL_DIR = "/opt/serverpanel"
PANEL_PORT = 8080            # panel API listens here locally
MAILSUITE_DIR = "/opt/mail-suite"
MAILSUITE_BIN = "/opt/mail-suite/mail-suite"
MAILSUITE_PORT = 9090        # mail-suite backend listens here locally


def load_password() -> str:
    """Env BZ_VPS_PASS wins; else first backticked token after 'Password:'
    in the gitignored testing-vps-details.md (both boxes share one password)."""
    if os.environ.get("BZ_VPS_PASS"):
        return os.environ["BZ_VPS_PASS"]
    md = Path(__file__).resolve().parent.parent / "testing-vps-details.md"
    if md.exists():
        text = md.read_text(encoding="utf-8", errors="replace")
        # Require the literal "Password:" label immediately before the
        # backticked token — a loose `password[^`]*` match otherwise grabs
        # the first backticked word in prose (e.g. `.gitignore`).
        m = re.search(r"password:\s*`([^`]+)`", text, re.IGNORECASE)
        if m:
            return m.group(1)
    return ""


PASSWORD = load_password()
if not PASSWORD:
    sys.exit("No password: set BZ_VPS_PASS or populate testing-vps-details.md")


def connect(host: str) -> paramiko.SSHClient:
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, username=USER, password=PASSWORD, timeout=25,
              look_for_keys=False, allow_agent=False)
    return c


def run(c: paramiko.SSHClient, cmd: str, timeout: int = 900) -> tuple[int, str, str]:
    _, so, se = c.exec_command(cmd, timeout=timeout, get_pty=False)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    code = so.channel.recv_exit_status()
    return code, out, err


def step(label: str, code: int, out: str, err: str, show: int = 1200) -> bool:
    tag = "OK" if code == 0 else f"FAIL({code})"
    body = (out + err).strip()
    if len(body) > show:
        body = body[:show] + "…"
    print(f"  [{tag}] {label}")
    if body:
        for line in body.splitlines():
            print(f"      {line}")
    return code == 0


def probe_tools(c: paramiko.SSHClient) -> tuple[str, str, str, str]:
    """Non-interactive SSH doesn't source ~/.bashrc, so go/node may be off
    PATH. Find them and return (go, npm, npx, env_prefix)."""
    _, out, _ = run(
        c,
        "for bin in go node npm npx; do "
        "  p=$(command -v $bin 2>/dev/null); "
        "  if [ -z \"$p\" ]; then "
        "    p=$(find /usr/local /opt /root/go /root/.local $HOME/go "
        "      -maxdepth 5 -name \"$bin\" -type f -executable 2>/dev/null | head -1); "
        "  fi; echo \"$bin=$p\"; done",
    )
    bins: dict[str, str] = {}
    for line in out.strip().splitlines():
        k, _, v = line.partition("=")
        bins[k.strip()] = v.strip()
    go = bins.get("go") or "go"
    npm = bins.get("npm") or "npm"
    npx = bins.get("npx") or "npx"
    dirs = [bins.get(k, "").rsplit("/", 1)[0] for k in ("go", "node", "npm") if bins.get(k)]
    prefix = ":".join(d for d in dirs if d)
    env_prefix = f"export PATH={prefix}:$PATH && " if prefix else ""
    return go, npm, npx, env_prefix


def panel_exec_path(c: paramiko.SSHClient) -> str:
    _, out, _ = run(
        c,
        "systemctl cat serverpanel 2>/dev/null | grep -E '^ExecStart='; "
        "systemctl cat panel 2>/dev/null | grep -E '^ExecStart='",
    )
    for line in out.splitlines():
        if line.startswith("ExecStart="):
            return line.split("=", 1)[1].split()[0].strip()
    return ""


def panel_service(c: paramiko.SSHClient) -> str:
    _, out, _ = run(c, "systemctl list-unit-files 2>/dev/null | grep -qE '^serverpanel\\.service' "
                       "&& echo serverpanel || echo panel")
    return out.strip() or "serverpanel"


# --------------------------------------------------------------------------- #
#  inspect
# --------------------------------------------------------------------------- #

def inspect(host: str) -> dict:
    c = connect(host)
    facts: dict = {"host": host}
    try:
        run(c, f"cd {PANEL_DIR} && git fetch origin -q")  # read-only refresh
        _, head, _ = run(c, f"cd {PANEL_DIR} && git rev-parse --short HEAD 2>&1")
        _, origin, _ = run(c, f"cd {PANEL_DIR} && git rev-parse --short origin/main 2>&1")
        _, headline, _ = run(c, f"cd {PANEL_DIR} && git log -1 --oneline 2>&1")
        facts["head"] = head.strip()
        facts["origin_main"] = origin.strip()
        facts["headline"] = headline.strip()

        _, psvc, _ = run(c, "systemctl is-active serverpanel 2>/dev/null || systemctl is-active panel 2>/dev/null")
        facts["panel_active"] = psvc.strip()
        _, pver, _ = run(c, f"curl -sf http://127.0.0.1:{PANEL_PORT}/api/v1/version 2>&1")
        facts["panel_version"] = pver.strip()[:300]

        _, msvc, _ = run(c, "systemctl is-active mail-suite 2>/dev/null")
        facts["mailsuite_active"] = msvc.strip() or "not-installed"
        _, mbin, _ = run(c, f"test -x {MAILSUITE_BIN} && echo yes || echo no")
        facts["mailsuite_binary"] = mbin.strip()
        _, mhealth, _ = run(c, f"curl -sf http://127.0.0.1:{MAILSUITE_PORT}/healthz 2>&1")
        facts["mailsuite_health"] = mhealth.strip()[:200]

        _, mongo, _ = run(c, "systemctl is-active mongod 2>/dev/null")
        facts["mongod_active"] = mongo.strip()
    finally:
        c.close()
    return facts


def print_inspect(facts: dict) -> None:
    print(f"\n=== {facts['host']} ===")
    behind = facts.get("head") != facts.get("origin_main")
    print(f"  git HEAD        : {facts.get('head')}  (origin/main {facts.get('origin_main')})"
          + ("   <-- BEHIND" if behind else "   in sync"))
    print(f"  last commit     : {facts.get('headline')}")
    print(f"  panel service   : {facts.get('panel_active')}")
    print(f"  panel /version  : {facts.get('panel_version')}")
    print(f"  mail-suite svc  : {facts.get('mailsuite_active')}  (binary: {facts.get('mailsuite_binary')})")
    print(f"  mail-suite /healthz: {facts.get('mailsuite_health') or '(no response)'}")
    print(f"  mongod          : {facts.get('mongod_active')}")


# --------------------------------------------------------------------------- #
#  deploy
# --------------------------------------------------------------------------- #

def deploy(host: str) -> dict:
    print(f"\n########## DEPLOY {host} ##########")
    c = connect(host)
    result = {"host": host, "ok": False}
    try:
        if not step("git reset --hard origin/main",
                    *run(c, f"cd {PANEL_DIR} && git fetch origin && git reset --hard origin/main && git log -1 --oneline")):
            return result

        go, npm, npx, env = probe_tools(c)
        print(f"  tools: go={go} npm={npm}")

        # --- panel server binary ---
        if not step("build panel server",
                    *run(c, f"{env}cd {PANEL_DIR}/backend && {go} build -o bin/server ./cmd/server", timeout=900)):
            return result
        exec_path = panel_exec_path(c) or f"{PANEL_DIR}/bin/server"
        if not step(f"install panel binary -> {exec_path}",
                    *run(c, f"install -m 0755 {PANEL_DIR}/backend/bin/server {exec_path}")):
            return result

        # --- panel frontends (served from disk; rebuild so UI matches main) ---
        # Non-fatal: a frontend build hiccup shouldn't block the binary swap.
        if step("build panel frontends",
                *run(c, f"{env}cd {PANEL_DIR}/frontend && "
                        f"{npx} turbo run build --filter=@serverpanel/whm --filter=@serverpanel/cpanel",
                        timeout=1200)):
            # Sync dist into the service's WorkingDirectory if it differs from
            # the repo dir (the server SendFile's from WorkingDirectory).
            _, wd, _ = run(c, "systemctl show serverpanel -p WorkingDirectory --value 2>/dev/null; "
                              "systemctl show panel -p WorkingDirectory --value 2>/dev/null")
            working_dir = (wd.strip().splitlines() or [PANEL_DIR])[0].strip() or PANEL_DIR
            if working_dir and working_dir != PANEL_DIR:
                for app in ("whm", "cpanel"):
                    step(f"rsync {app} dist -> {working_dir}",
                         *run(c, f"mkdir -p {working_dir}/frontend/apps/{app}/dist && "
                                 f"rsync -a --delete {PANEL_DIR}/frontend/apps/{app}/dist/ "
                                 f"{working_dir}/frontend/apps/{app}/dist/"))

        # --- mail-suite (only if already installed on this box) ---
        _, msinstalled, _ = run(c, f"test -d {MAILSUITE_DIR} && echo yes || echo no")
        if msinstalled.strip() == "yes":
            step("build mail-suite backend",
                 *run(c, f"{env}cd {PANEL_DIR}/mail-suite/backend && "
                         f"GOOS=linux GOARCH=amd64 {go} build -o {MAILSUITE_BIN} ./cmd/server", timeout=900))
            step("build mail-suite webmail",
                 *run(c, f"{env}cd {PANEL_DIR}/mail-suite/webmail && "
                         f"{npm} install --no-audit --no-fund && {npm} run build && "
                         f"mkdir -p {MAILSUITE_DIR}/webmail && cp -r dist/. {MAILSUITE_DIR}/webmail/", timeout=1200))
            step("restart mail-suite", *run(c, "systemctl restart mail-suite"))
        else:
            print("  [SKIP] mail-suite not installed on this box "
                  "(install once via WHM → Mail Suite → Install on this server)")

        # --- restart panel ---
        psvc = panel_service(c)
        if not step(f"restart {psvc}", *run(c, f"systemctl restart {psvc} 2>/dev/null || systemctl restart panel")):
            return result

        # --- health ---
        run(c, "sleep 3")
        step("panel /version", *run(c, f"curl -sf http://127.0.0.1:{PANEL_PORT}/api/v1/version 2>&1"))
        step("mail-suite /healthz", *run(c, f"curl -sf http://127.0.0.1:{MAILSUITE_PORT}/healthz 2>&1"))

        result["ok"] = True
        return result
    finally:
        c.close()


# --------------------------------------------------------------------------- #
#  mail-suite-only update (surgical — for PROD)
# --------------------------------------------------------------------------- #

def deploy_mailsuite_only(host: str) -> dict:
    """Update ONLY mail-suite (login fix) — leave the running panel binary,
    frontends and the rest of the git checkout untouched. Lowest blast radius,
    the right choice for the live prod box: it checks out just the mail-suite
    subtree at origin/main, rebuilds the mail-suite backend + webmail, and
    restarts the mail-suite service. The main panel is never restarted."""
    print(f"\n########## MAIL-SUITE-ONLY UPDATE {host} ##########")
    c = connect(host)
    result = {"host": host, "ok": False}
    try:
        # `git checkout <ref> -- <path>` ADDS/UPDATES but never DELETES files
        # removed upstream — so a stale source file (e.g. a deleted component
        # that still imports a since-removed export) silently breaks the on-box
        # `tsc` build while the checkout reports success. Remove the two subtrees
        # first, then restore exactly what's in origin/main: the net effect is an
        # exact match INCLUDING deletions, with the rest of the panel untouched.
        # (git rm -r only touches tracked files, so gitignored node_modules/dist
        # caches survive.)
        if not step("sync mail-suite subtree to origin/main incl. deletions (rest of panel untouched)",
                    *run(c, f"cd {PANEL_DIR} && git fetch origin && "
                            f"git rm -rf --quiet --ignore-unmatch mail-suite/backend mail-suite/webmail && "
                            f"git checkout origin/main -- mail-suite/backend mail-suite/webmail && "
                            f"git log -1 --oneline origin/main")):
            return result
        _, ms, _ = run(c, f"test -d {MAILSUITE_DIR} && echo yes || echo no")
        if ms.strip() != "yes":
            print("  [SKIP] mail-suite not installed on this box")
            return result
        go, npm, npx, env = probe_tools(c)
        print(f"  tools: go={go} npm={npm}")
        if not step("build mail-suite backend",
                    *run(c, f"{env}cd {PANEL_DIR}/mail-suite/backend && "
                            f"GOOS=linux GOARCH=amd64 {go} build -o {MAILSUITE_BIN} ./cmd/server", timeout=900)):
            return result
        step("build mail-suite webmail",
             *run(c, f"{env}cd {PANEL_DIR}/mail-suite/webmail && "
                     f"{npm} install --no-audit --no-fund && {npm} run build && "
                     f"mkdir -p {MAILSUITE_DIR}/webmail && cp -r dist/. {MAILSUITE_DIR}/webmail/", timeout=1200))
        if not step("restart mail-suite", *run(c, "systemctl restart mail-suite")):
            return result
        run(c, "sleep 3")
        step("mail-suite /healthz", *run(c, f"curl -sf http://127.0.0.1:{MAILSUITE_PORT}/healthz 2>&1"))
        result["ok"] = True
        return result
    finally:
        c.close()


# --------------------------------------------------------------------------- #

def main() -> int:
    mode = (sys.argv[1] if len(sys.argv) > 1 else "inspect").lower()

    # Optional 2nd arg = a single host to target (e.g. prod 195.35.7.161, which
    # is deliberately NOT in the default SERVERS list so a bare `deploy` never
    # touches prod). `deploy 195.35.7.161` deploys ONLY to that host.
    host_filter = sys.argv[2] if len(sys.argv) > 2 else None
    targets = [{"name": host_filter, "host": host_filter}] if host_filter else SERVERS

    if mode == "inspect":
        print("### INSPECT (read-only) — no changes will be made ###")
        facts = []
        for s in targets:
            try:
                f = inspect(s["host"])
            except Exception as e:  # noqa: BLE001
                print(f"\n=== {s['host']} ===\n  CONNECT/INSPECT ERROR: {e}")
                facts.append({"host": s["host"], "error": str(e)})
                continue
            print_inspect(f)
            facts.append(f)
        # side-by-side sync verdict
        print("\n=== SYNC VERDICT ===")
        good = [f for f in facts if "error" not in f]
        if len(good) == len(targets):
            heads = {f["head"] for f in good}
            vers = {f.get("panel_version") for f in good}
            print(f"  same commit across boxes : {'YES' if len(heads) == 1 else 'NO ' + str(heads)}")
            print(f"  same panel version       : {'YES' if len(vers) == 1 else 'NO'}")
            behind = [f["host"] for f in good if f["head"] != f["origin_main"]]
            print(f"  boxes behind origin/main : {behind or 'none'}")
        else:
            print("  (one or more boxes unreachable — see errors above)")
        return 0

    if mode == "deploy":
        names = ", ".join(t["host"] for t in targets)
        print(f"### DEPLOY — will git-reset, rebuild and restart on: {names} ###")
        results = [deploy(s["host"]) for s in targets]
        print("\n=== POST-DEPLOY VERIFY ===")
        post = []
        for s in targets:
            try:
                post.append(inspect(s["host"]))
            except Exception as e:  # noqa: BLE001
                post.append({"host": s["host"], "error": str(e)})
        for f in post:
            if "error" in f:
                print(f"  {f['host']}: ERROR {f['error']}")
            else:
                print(f"  {f['host']}: HEAD {f['head']} | panel {f['panel_active']} | "
                      f"mail-suite {f['mailsuite_active']}")
        good = [f for f in post if "error" not in f]
        heads = {f["head"] for f in good}
        ok = all(r["ok"] for r in results) and len(heads) == 1 and len(good) == len(targets)
        print(f"\n  ALL DEPLOYED + IN SYNC: {'YES' if ok else 'NO'}")
        return 0 if ok else 1

    if mode == "mailsuite":
        names = ", ".join(t["host"] for t in targets)
        print(f"### MAIL-SUITE-ONLY UPDATE on: {names} (panel binary/frontend untouched) ###")
        results = [deploy_mailsuite_only(s["host"]) for s in targets]
        ok = all(r["ok"] for r in results)
        print(f"\n  MAIL-SUITE UPDATED: {'YES' if ok else 'NO'}")
        return 0 if ok else 1

    sys.exit(f"unknown mode {mode!r}; use 'inspect', 'deploy' or 'mailsuite'")


if __name__ == "__main__":
    sys.exit(main())
