"""Verify v3.0.15 transfer DNS import preserves every third-party
record the operator configured on the source — only the apex NS set
gets replaced with the destination's own.

Reproduces the user's reported failure: source zone has
  app NS ns1.thirdparty.com.   ← operator-configured subdomain delegation
  external A 8.8.8.8           ← third-party A value
  @ A 187.127.SRC.IP            ← source's own IP (must rewrite)
  @ TXT "v=spf1 ip4:SRC ~all"  ← SPF (must rewrite)

After the import logic runs on the destination, assert:
  * `app NS ns1.thirdparty.com.`  preserved (was being dropped)
  * `external A 8.8.8.8`          preserved (was being rewritten)
  * `@ A` flipped to destIP
  * `@ TXT v=spf1 ip4:destIP ~all` flipped
  * `@ NS` is the destination's own set (not the source's)
"""
import json, os, re, sys
from pathlib import Path
import paramiko

try: sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception: pass

md = Path(__file__).resolve().parent.parent / "testing-vps-details.md"
text = md.read_text(encoding="utf-8", errors="replace")
def pwd(s):
    m = re.search(rf"#+\s*{s}\s+server.*?password[^`]*`([^`]+)`", text, re.IGNORECASE | re.DOTALL)
    return m.group(1)
SRC_PWD = pwd("Old"); DST_PWD = pwd("New")
SRC_HOST = "187.127.155.209"; DST_HOST = "187.127.156.87"
SRC_IP = SRC_HOST; DST_IP = DST_HOST
ZONE = "transfer-preserve.invalid"


def open_ssh(host, p):
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, username="root", password=p, timeout=20, look_for_keys=False, allow_agent=False)
    return c


def r(c, cmd, show=True, timeout=60):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    if show:
        print(f"\n$ {cmd[:130]}{'…' if len(cmd) > 130 else ''}")
        for line in (out+err).strip().splitlines()[:15]:
            print(f"    {line}")
    return out.strip()


src = open_ssh(SRC_HOST, SRC_PWD)
dst = open_ssh(DST_HOST, DST_PWD)

# ── 1. Source zone with the realistic record mix ─────────────────────
print(f"=== 1. SOURCE ({SRC_HOST}): build zone with the user's mix ===")
r(src, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
r(src, f"pdnsutil create-zone {ZONE} ns1.{ZONE}.")
# Operator-configured records
r(src, f"pdnsutil add-record {ZONE} @       A   3600 {SRC_IP}")
r(src, f"pdnsutil add-record {ZONE} app     NS  60 'ns1.betazeninfotech.com.'")
r(src, f"pdnsutil add-record {ZONE} dev     NS  60 'ns.thirdparty.example.'")
r(src, f"pdnsutil add-record {ZONE} external A  300 8.8.8.8")
r(src, f"pdnsutil add-record {ZONE} mail    A  60 {SRC_IP}")
r(src, f"pdnsutil add-record {ZONE} @       TXT 3600 '\"v=spf1 ip4:{SRC_IP} ~all\"'")
r(src, "pdns_control reload", show=False)
print("\n   Source zone export:")
src_export = r(src, f"pdnsutil list-zone {ZONE}")


# ── 2. Run the v3.0.15 DNS import logic on dest ──────────────────────
print(f"\n=== 2. DEST ({DST_HOST}): pre-clean + apply v3.0.15 import logic ===")
r(dst, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
r(dst, f"pdnsutil create-zone {ZONE} ns1.betazeninfotech.com.")  # destination's own NS

# The import-loop logic, mirrored from transfer_service.go:1656-1721
# (post-fix). The only thing that's different from the production code
# path is that we skip the Mongo write — we just want to verify the
# pdns side here.
old_ip = SRC_IP
new_ip = DST_IP

records_imported = []
for line in src_export.splitlines():
    line = line.strip()
    if not line or line.startswith(";"):
        continue
    parts = line.split()
    if len(parts) < 4:
        continue
    name = parts[0]
    ttl = parts[1]
    idx = 2
    if parts[idx].upper() == "IN":
        idx += 1
    if idx >= len(parts):
        continue
    rtype = parts[idx].upper()
    idx += 1
    value = " ".join(parts[idx:])
    if not rtype or not value:
        continue

    # v3.0.15 skip rules
    if rtype == "SOA":
        continue
    if rtype == "NS":
        apex_fqdn = ZONE + "."
        if name == ZONE or name == apex_fqdn or name == "@":
            continue

    # v3.0.15 IP rewrite rules
    if rtype == "A" and old_ip and value == old_ip:
        value = new_ip
    if rtype == "TXT" and "v=spf1" in value:
        value = re.sub(r"ip4:[^ \"]+", f"ip4:{new_ip}", value)

    # FQDN → relative
    rec_name = name
    if rec_name == ZONE or rec_name == ZONE + ".":
        rec_name = "@"
    elif rec_name.endswith("." + ZONE + "."):
        rec_name = rec_name[:-(len(ZONE) + 2)]
    elif rec_name.endswith("." + ZONE):
        rec_name = rec_name[:-(len(ZONE) + 1)]
    if not rec_name:
        rec_name = "@"

    r(dst, f"pdnsutil add-record {ZONE} {rec_name} {rtype} {ttl} {value!r}", show=False)
    records_imported.append((rec_name, rtype, value))

r(dst, "pdns_control reload", show=False)
print(f"   imported {len(records_imported)} records to destination")


# ── 3. Assertions ────────────────────────────────────────────────────
print("\n=== 3. Destination zone state (post-import) ===")
dst_dump = r(dst, f"pdnsutil list-zone {ZONE} | grep -vE 'SOA' | sort")


def has_record(name_fqdn, rtype, value):
    """Match `<name>\\t<ttl>\\tIN\\t<rtype>\\t<value-substring>`."""
    for line in dst_dump.splitlines():
        if not line.startswith(name_fqdn):
            continue
        rest = line[len(name_fqdn):]
        if not rest or rest[0] not in (" ", "\t", "."):
            continue
        if f"\t{rtype}\t" in line and value in line:
            return True
    return False


tests = [
    # The bug-from-the-screenshot: subdomain NS preserved
    (f"app.{ZONE}", "NS", "ns1.betazeninfotech.com.", True,
     "subdomain `app NS` PRESERVED (the user's bug)"),
    (f"dev.{ZONE}", "NS", "ns.thirdparty.example.", True,
     "subdomain `dev NS` PRESERVED (third-party delegation)"),

    # Third-party A preserved
    (f"external.{ZONE}", "A", "8.8.8.8", True,
     "third-party A `external 8.8.8.8` PRESERVED (was being rewritten)"),

    # IP rewrites where they should
    (f"{ZONE}", "A", DST_IP, True, "@ A flipped to dest IP"),
    (f"{ZONE}", "A", SRC_IP, False, "@ A no longer carries source IP"),
    (f"mail.{ZONE}", "A", DST_IP, True, "mail A flipped to dest IP"),
    (f"{ZONE}", "TXT", f"ip4:{DST_IP}", True, "@ SPF flipped to dest IP"),
    (f"{ZONE}", "TXT", f"ip4:{SRC_IP}", False, "@ SPF no longer mentions source IP"),

    # Apex NS replaced (destination's own, not source's)
    (f"{ZONE}", "NS", "ns1.betazeninfotech.com.", True,
     "@ NS = destination's own set"),
]

passed, failed = 0, 0
for name, rtype, val, expect, desc in tests:
    got = has_record(name, rtype, val)
    if got == expect:
        passed += 1
        print(f"   ✓ {desc}")
    else:
        failed += 1
        print(f"   ✗ FAIL: {desc} — expected {expect}, got {got}")


# ── Cleanup ──────────────────────────────────────────────────────────
print("\n=== Cleanup ===")
r(src, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
r(dst, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
src.close(); dst.close()

print(f"\n{'='*55}")
print(f"  {passed}/{passed+failed} assertions passed")
if failed:
    sys.exit(1)
print(f"{'='*55}")
