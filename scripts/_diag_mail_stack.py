#!/usr/bin/env python3
"""Diagnose + auto-heal the mail stack (Dovecot + Postfix + ports).

User reported: Roundcube shows
    Connection to storage server failed
    Server Error: Could not connect to localhost:143: Connection refused

Port 143 is IMAP — that's Dovecot. "Connection refused" means nothing
is bound to the port; the service is down (or crashed, or never
installed, or bound to a non-localhost interface).

This script runs ON THE VPS (paste after `ssh root@<vps>`) and walks
the full mail-stack health: package installed → service active →
ports bound → config parses → effective auth path. For every red
status it prints the exact one-liner to fix it.

Stdlib-only — no extra pip installs.

Run:
    cd /opt/serverpanel
    sudo python3 scripts/_diag_mail_stack.py            # diagnose only
    sudo python3 scripts/_diag_mail_stack.py --fix      # diagnose + auto-heal what's safe
"""
from __future__ import annotations

import shutil
import subprocess
import sys

FIX = "--fix" in sys.argv
FAILS: list[str] = []
HEALED: list[str] = []


def sh(cmd: str, *, show: bool = True, ok_codes=(0,)) -> tuple[str, str, int]:
    p = subprocess.run(["bash", "-c", cmd], capture_output=True)
    out = p.stdout.decode("utf-8", errors="replace").strip()
    err = p.stderr.decode("utf-8", errors="replace").strip()
    if show:
        tag = "OK" if p.returncode in ok_codes else f"exit={p.returncode}"
        print(f"[{tag}] $ {cmd[:140]}")
        for ln in (out + ("\n" + err if err else "")).splitlines()[:10]:
            print(f"    {ln}")
    return out, err, p.returncode


def must(label: str, cond: bool, fix_cmd: str = "") -> None:
    tag = "PASS" if cond else "FAIL"
    print(f"  [{tag}] {label}")
    if not cond:
        FAILS.append(label)
        if fix_cmd:
            print(f"        fix: {fix_cmd}")
            if FIX:
                print(f"        --fix → running: {fix_cmd}")
                _, err, code = sh(fix_cmd, show=False)
                if code == 0:
                    HEALED.append(label)
                    print(f"        healed.")
                else:
                    print(f"        heal failed: {err[:300]}")


# ─── 1. Packages installed ──────────────────────────────────────────
print("\n=== 1. Mail packages installed ===")
for pkg in ("dovecot-imapd", "dovecot-pop3d", "postfix", "opendkim"):
    have = shutil.which("dpkg") and sh(f"dpkg -s {pkg} 2>/dev/null | grep -q '^Status: install ok installed'", show=False)[2] == 0
    must(f"{pkg} installed", have, f"apt-get install -y {pkg}")

# ─── 2. Services active ─────────────────────────────────────────────
print("\n=== 2. Services active ===")
for unit in ("dovecot", "postfix", "opendkim"):
    out, _, _ = sh(f"systemctl is-active {unit}", show=False)
    must(f"{unit} is active", out == "active", f"systemctl start {unit} && systemctl enable {unit}")

# Dovecot detail when it's failing — show the journalctl tail so the
# real reason (cert missing, config parse error, port already bound,
# permission problem) is visible without a second round-trip.
out, _, _ = sh("systemctl is-active dovecot", show=False)
if out != "active":
    print("\n  Dovecot is NOT active — last 25 journal lines:")
    sh("journalctl -u dovecot -n 25 --no-pager 2>/dev/null", show=True)
    print("  Dovecot effective config (top 30 lines, look for parse errors):")
    sh("dovecot -n 2>&1 | head -30 || true", show=True)

# ─── 3. Ports bound to the right interfaces ─────────────────────────
print("\n=== 3. Ports bound on localhost ===")
ports = {
    143: "imap (Dovecot)",
    993: "imaps (Dovecot)",
    25: "smtp (Postfix)",
    587: "submission (Postfix)",
}
for port, label in ports.items():
    # ss is on every modern Ubuntu install; covers IPv4 + IPv6 listeners.
    out, _, _ = sh(f"ss -ltn 'sport = :{port}' 2>/dev/null | tail -n +2", show=False)
    bound = bool(out.strip())
    fix_for = {
        143: "systemctl restart dovecot",
        993: "systemctl restart dovecot",
        25: "systemctl restart postfix",
        587: "systemctl restart postfix",
    }.get(port, "")
    must(f"{port} ({label}) listening", bound, fix_for)
    if bound:
        print(f"        {out[:100]}")

# ─── 4. Auth path round-trip (only meaningful if dovecot is up) ─────
print("\n=== 4. Auth path round-trip ===")
out, _, _ = sh("systemctl is-active dovecot", show=False)
if out == "active":
    have_doveadm = shutil.which("doveadm") is not None
    must("doveadm on PATH", have_doveadm)
    if have_doveadm:
        out, err, code = sh("doveadm auth list 2>&1 | head -5", show=True)
        must("doveadm auth list returns rows", code == 0)
else:
    print("  (skipped — dovecot is down; bring it up first then re-run)")

# ─── 5. Postfix virtual maps look sane ──────────────────────────────
print("\n=== 5. Postfix virtual_mailbox_maps ===")
out, _, _ = sh("postconf -h virtual_mailbox_maps 2>/dev/null", show=False)
print(f"  virtual_mailbox_maps = {out or '(not set)'}")
must("virtual_mailbox_maps is hash:/etc/postfix/virtual_mailbox_maps",
     out == "hash:/etc/postfix/virtual_mailbox_maps",
     "postconf -e 'virtual_mailbox_maps=hash:/etc/postfix/virtual_mailbox_maps' && postmap /etc/postfix/virtual_mailbox_maps && systemctl reload postfix")
out, _, code = sh("test -f /etc/postfix/virtual_mailbox_maps.db", show=False)
must(".db file exists", code == 0,
     "postmap /etc/postfix/virtual_mailbox_maps")

# ─── 6. Dovecot users file shape ────────────────────────────────────
print("\n=== 6. /etc/dovecot/users shape ===")
out, _, code = sh("test -f /etc/dovecot/users && wc -l /etc/dovecot/users", show=False)
if code == 0:
    print(f"  {out} lines")
    # Spot-check a few lines: every row should be 7 colon-separated
    # fields. Malformed rows from a botched bulk-upload would show
    # here.
    bad, _, _ = sh(
        r"awk -F: 'NF != 7 && NF != 8 && length($0) > 0 {print NR\": \"$0}' /etc/dovecot/users 2>/dev/null | head -5",
        show=False,
    )
    must("every row has 7-8 colon-separated fields", not bad.strip(),
         "back up /etc/dovecot/users and review the listed lines")
    if bad.strip():
        print(f"        malformed:\n        {bad[:300]}")
else:
    must("/etc/dovecot/users exists", False,
         "touch /etc/dovecot/users && chown dovecot:dovecot /etc/dovecot/users && chmod 0640 /etc/dovecot/users")

# ─── 7. Reload + re-test once we've heal-ed (only with --fix) ───────
if FIX and HEALED:
    print("\n=== 7. Re-test after auto-heal ===")
    sh("systemctl reload postfix 2>/dev/null", show=False)
    sh("systemctl restart dovecot 2>/dev/null", show=False)
    import time
    time.sleep(2)
    out, _, _ = sh("systemctl is-active dovecot", show=False)
    must("dovecot active after heal", out == "active")
    out, _, _ = sh("ss -ltn 'sport = :143' 2>/dev/null | tail -n +2", show=False)
    must("port 143 listening after heal", bool(out.strip()))

# ─── Summary ────────────────────────────────────────────────────────
print("\n=== summary ===")
if not FAILS:
    print("ALL CHECKS PASSED — mail stack is healthy.")
    sys.exit(0)
print(f"FAILED: {len(FAILS)} check(s)")
for f in FAILS:
    print(f"  - {f}")
if HEALED:
    print(f"\nAUTO-HEALED ({len(HEALED)}):")
    for h in HEALED:
        print(f"  - {h}")
if not FIX:
    print("\nRe-run with `--fix` to attempt auto-heal of the safe items above.")
sys.exit(1 if (set(FAILS) - set(HEALED)) else 0)
