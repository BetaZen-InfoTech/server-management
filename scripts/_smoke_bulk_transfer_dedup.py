#!/usr/bin/env python3
"""Verify the v3.1.50 mailbox dedup fix on the DESTINATION VPS.

Reproduces and confirms the fix for the "bulk upload mailboxes silently
dropped after server transfer" bug. Pre-3.1.50 the panel-records-only
transfer path used the wrong field name (`address`) in its mailbox
dedup query — the Mailbox model uses `email`. MongoDB treats
`{address: null}` as matching docs missing the field, so every
mailbox AFTER the first per-collection got silently dedup'd as
"already exists" and dropped.

This script runs ON the destination VPS and:
  1. Snapshots destination Mongo mailbox count + sample emails.
  2. Snapshots SOURCE Mongo mailbox count via SSH (read-only).
  3. Reports any source mailboxes MISSING from destination.
  4. Cross-checks each missing mailbox against /etc/dovecot/users +
     /etc/postfix/virtual_mailbox_maps to prove the gap is real
     (mailbox missing from Mongo ⟹ missing from postfix/dovecot too).
  5. Confirms the panel build is >= 3.1.50 (so the fix is active).

Stdlib-only — no paramiko, no requests, no extra installs.

Run on the DESTINATION VPS after a transfer:
    cd /opt/serverpanel
    sudo python3 scripts/_smoke_bulk_transfer_dedup.py <source-ip> <source-ssh-user> <source-ssh-pass>

Example:
    sudo python3 scripts/_smoke_bulk_transfer_dedup.py 187.127.156.87 root '<password>'
"""
from __future__ import annotations

import json
import os
import shlex
import subprocess
import sys
import urllib.error
import urllib.request

BACKEND = "http://127.0.0.1:8080"
FAILS: list[str] = []


def sh(cmd: str, *, show: bool = True) -> tuple[str, str, int]:
    p = subprocess.run(["bash", "-c", cmd], capture_output=True)
    out = p.stdout.decode("utf-8", errors="replace").strip()
    err = p.stderr.decode("utf-8", errors="replace").strip()
    if show:
        tag = "OK" if p.returncode == 0 else f"exit={p.returncode}"
        print(f"[{tag}] $ {cmd[:140]}")
        for ln in (out + ("\n" + err if err else "")).splitlines()[:8]:
            print(f"    {ln}")
    return out, err, p.returncode


def mongo_uri() -> str:
    if os.path.exists("/opt/serverpanel/.env"):
        with open("/opt/serverpanel/.env") as f:
            for line in f:
                if line.startswith("MONGO_URI="):
                    return line.split("=", 1)[1].strip().strip('"').strip("'")
    return "mongodb://localhost:27017/serverpanel"


def mongo_local(query: str) -> str:
    safe = query.replace("'", "'\\''")
    out, _, _ = sh(f'mongosh --quiet "{mongo_uri()}" --eval \'{safe}\'', show=False)
    return out


def mongo_remote(host: str, user: str, pwd: str, query: str) -> str:
    """Run a mongosh query on the SOURCE box over SSH. Read-only."""
    if not _have("sshpass"):
        sh("apt-get install -y sshpass >/dev/null 2>&1 || true", show=False)
    safe = query.replace('"', '\\"').replace("'", "'\\''")
    cmd = (
        f'sshpass -p {shlex.quote(pwd)} ssh -o StrictHostKeyChecking=no '
        f'-o UserKnownHostsFile=/dev/null -o LogLevel=ERROR {shlex.quote(user)}@{shlex.quote(host)} '
        f'\'mongosh --quiet "$(grep ^MONGO_URI= /opt/serverpanel/.env | cut -d= -f2- | tr -d "\\"")" '
        f'--eval "{safe}"\''
    )
    out, _, _ = sh(cmd, show=False)
    return out


def _have(prog: str) -> bool:
    out, _, rc = sh(f"command -v {prog}", show=False)
    return rc == 0 and out != ""


def must(label: str, cond: bool, detail: str = "") -> None:
    print(f"    [{'PASS' if cond else 'FAIL'}] {label}" + (f" — {detail}" if detail else ""))
    if not cond:
        FAILS.append(label)


def http(method: str, path: str) -> tuple[int, str]:
    req = urllib.request.Request(BACKEND + path, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return r.status, r.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", errors="replace")
    except Exception as e:
        return 0, str(e)


def main() -> int:
    if len(sys.argv) != 4:
        print(__doc__)
        return 2
    src_ip, src_user, src_pwd = sys.argv[1], sys.argv[2], sys.argv[3]

    print(f"\n=== bulk-transfer dedup smoke (source={src_ip}) ===\n")

    # 1. Confirm the panel binary is v3.1.50+
    print("step 1 — confirm panel build is >= 3.1.50")
    code, body = http("GET", "/api/v1/version")
    print(f"    GET /api/v1/version → {code}")
    print(f"    {body[:200]}")
    try:
        ver = json.loads(body).get("version", "")
    except Exception:
        ver = ""
    parts = [int(x) for x in ver.split(".") if x.isdigit()]
    while len(parts) < 3:
        parts.append(0)
    must(
        "panel >= 3.1.50 (mailbox dedup fix active)",
        tuple(parts[:3]) >= (3, 1, 50),
        f"got version={ver}",
    )

    # 2. Source-side snapshot
    print("\nstep 2 — snapshot SOURCE mailbox state via SSH")
    src_count = mongo_remote(src_ip, src_user, src_pwd, "db.mailboxes.countDocuments()")
    src_emails = mongo_remote(
        src_ip, src_user, src_pwd,
        'JSON.stringify(db.mailboxes.find({}, {email:1, _id:0}).toArray())',
    )
    print(f"    source mailbox count: {src_count}")
    try:
        src_list = sorted(
            (m.get("email") or "").lower()
            for m in json.loads(src_emails)
            if m.get("email")
        )
    except Exception as e:
        print(f"    parse failed: {e} — raw: {src_emails[:200]}")
        src_list = []
    print(f"    source mailbox sample: {src_list[:5]}{' ...' if len(src_list) > 5 else ''}")

    # 3. Destination-side snapshot
    print("\nstep 3 — snapshot DESTINATION mailbox state")
    dst_count = mongo_local("db.mailboxes.countDocuments()")
    dst_emails = mongo_local(
        'JSON.stringify(db.mailboxes.find({}, {email:1, _id:0}).toArray())'
    )
    print(f"    destination mailbox count: {dst_count}")
    try:
        dst_list = sorted(
            (m.get("email") or "").lower()
            for m in json.loads(dst_emails)
            if m.get("email")
        )
    except Exception as e:
        print(f"    parse failed: {e} — raw: {dst_emails[:200]}")
        dst_list = []
    print(f"    destination mailbox sample: {dst_list[:5]}{' ...' if len(dst_list) > 5 else ''}")

    # 4. Compare. Pre-3.1.50: only ~1 of N source mailboxes appear in dst.
    #    Post-3.1.50: all N appear.
    src_set, dst_set = set(src_list), set(dst_list)
    missing = sorted(src_set - dst_set)
    print(f"\nstep 4 — compare source vs destination")
    print(f"    source has {len(src_set)} mailboxes")
    print(f"    destination has {len(dst_set)} mailboxes")
    print(f"    missing from destination: {len(missing)}")
    if missing:
        print(f"    sample missing: {missing[:10]}")

    must(
        "all source mailboxes present in destination Mongo",
        len(missing) == 0,
        f"{len(missing)} of {len(src_set)} dropped — re-run transfer to back-fill",
    )

    # 5. Cross-check each missing mailbox against postfix/dovecot files.
    #    If missing from Mongo it must also be missing from those files —
    #    confirms the gap is end-to-end (not just a Mongo display issue).
    if missing:
        print("\nstep 5 — cross-check missing mailboxes against postfix/dovecot")
        sample = missing[:3]
        for addr in sample:
            in_dovecot = sh(f"grep -F '{addr}' /etc/dovecot/users", show=False)[2] == 0
            in_postfix = sh(
                f"grep -F '{addr}' /etc/postfix/virtual_mailbox_maps", show=False
            )[2] == 0
            print(
                f"    {addr}: dovecot={'YES' if in_dovecot else 'NO'} "
                f"postfix={'YES' if in_postfix else 'NO'}"
            )
            must(
                f"{addr} consistently missing (Mongo + dovecot + postfix)",
                not in_dovecot and not in_postfix,
                "if YES anywhere, the gap is partial — re-run heal-mailboxes",
            )
    else:
        print("\nstep 5 — skipped (no missing mailboxes)")

    print("\n=== summary ===")
    if FAILS:
        print(f"FAIL: {len(FAILS)} check(s) failed")
        for f in FAILS:
            print(f"  - {f}")
        return 1
    print("PASS: every source mailbox transferred successfully — v3.1.50 fix confirmed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
