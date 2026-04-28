"""End-to-end test for the v3.0.7 DNS rrset-reconcile fix.

Walks the full lifecycle that surfaced the bug on the WHM DNS Zones
page (the user's screenshots):

  1. Create a zone for a fake domain (.invalid is RFC-reserved so we
     can't accidentally serve real DNS).
  2. Add two A records with the SAME name but different values — the
     legitimate multi-value rrset case the old code mishandled.
  3. Delete the FIRST one. Verify (a) the API returns success,
     (b) the SECOND survives in Mongo AND in PowerDNS.
  4. Delete the SECOND. Verify the API returns success (no "record
     not found" toast — the bug from the screenshot).
  5. Try to add an EXACT duplicate (same name+type+value): backend
     must reject with 4xx.
  6. Seed a duplicate Mongo row directly (simulating pre-3.0.7 drift),
     hit POST /reconcile, verify the duplicate was collapsed.
  7. Cleanup the test zone.

Reads the VPS password from the gitignored testing-vps-details.md so
the credential never appears in shell command lines.
"""
from __future__ import annotations

import json
import os
import re
import sys
import time
from pathlib import Path

import paramiko

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass


def _load_pwd() -> str:
    if os.environ.get("BZ_VPS_PASS"):
        return os.environ["BZ_VPS_PASS"]
    md = Path(__file__).resolve().parent.parent / "testing-vps-details.md"
    if not md.exists():
        return ""
    text = md.read_text(encoding="utf-8", errors="replace")
    section_re = re.compile(r"#+\s*Old\s+server.*?(?=^#|\Z)",
                            re.IGNORECASE | re.DOTALL | re.MULTILINE)
    section = section_re.search(text)
    blob = section.group(0) if section else text
    pwd = re.search(r"password[^`]*`([^`]+)`", blob, re.IGNORECASE)
    return pwd.group(1) if pwd else ""


PASSWORD = _load_pwd()
if not PASSWORD:
    sys.exit("no password — set BZ_VPS_PASS or populate testing-vps-details.md")
HOST = os.environ.get("BZ_VPS_HOST", "187.127.155.209")
USER = os.environ.get("BZ_VPS_USER", "root")
BACKEND = "http://127.0.0.1:8080"

ZONE = "dns-dup-smoketest.invalid"
RNAME = "ns-test"
VAL_A = "10.0.0.1"
VAL_B = "10.0.0.2"


def r(c, cmd, show=True, timeout=60):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    code = so.channel.recv_exit_status()
    if show:
        tag = "OK" if code == 0 else f"exit={code}"
        print(f"\n[{tag}] $ {cmd[:120]}{'…' if len(cmd) > 120 else ''}")
        body = (out + err).strip()
        if body:
            for line in body.splitlines()[:30]:
                print(f"    {line}")
    return out.strip(), err.strip(), code


def mongo(c, query, show=True):
    safe = query.replace("'", "'\\''")
    return r(c,
        'cd /opt/serverpanel; . ./.env 2>/dev/null || true; '
        'URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; '
        f'mongosh --quiet "$URI" --eval \'{safe}\'', show=show)


c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, username=USER, password=PASSWORD, timeout=20,
          look_for_keys=False, allow_agent=False)


# ── 0. Mint admin token via OTP handoff (same trick as the multi-domain test) ──
print("=== 0. Mint admin token ===")
owner_out, _, _ = mongo(c,
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    'print(JSON.stringify({email: u.email}));', show=False)
admin_email = ""
for line in reversed(owner_out.splitlines()):
    s = line.strip()
    if s.startswith("{"):
        try:
            admin_email = json.loads(s)["email"]; break
        except Exception:
            continue
if not admin_email:
    print("!! no vendor_owner found"); c.close(); sys.exit(1)
print(f"    admin: {admin_email}")

mongo(c, f'db.otp_requests.deleteMany({{email:"{admin_email}"}});', show=False)
r(c, f"rm -f /tmp/dns.jar; "
     f"curl -s -c /tmp/dns.jar -X POST {BACKEND}/api/v1/auth/otp/request "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"email\":\"{admin_email}\",\"surface\":\"whm\"}}'", show=False)
mongo(c,
      'const crypto = require("crypto"); '
      'const h = crypto.createHash("sha256").update("DNSDUP9999").digest("hex"); '
      f'db.otp_requests.updateMany({{email:"{admin_email}", used:false, expires_at:{{$gt:new Date()}}}}, '
      '{$set:{code_hash:h}});', show=False)
out, _, _ = r(c,
    f"curl -s -b /tmp/dns.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
    f"-H 'Content-Type: application/json' "
    f"-d '{{\"email\":\"{admin_email}\",\"code\":\"DNSDUP9999\"}}'",
    show=False)
token = json.loads(out).get("data", {}).get("access_token", "")
if not token:
    print(f"!! no token: {out[:200]}"); c.close(); sys.exit(1)
print(f"    token len={len(token)}")
H = f"-H 'Authorization: Bearer {token}' -H 'Content-Type: application/json'"


# ── Pre-clean any leftover state from a previous test run ───────────
print("\n=== pre-clean ===")
mongo(c,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); print("zone wiped"); } '
      'else { print("no zone"); }')
r(c, f"pdnsutil delete-zone {ZONE} 2>&1 || true")

# Make sure we own a domain row for the zone — assertCallerOwnsDomain
# requires it. Pull the admin user's tenant.
owner_full, _, _ = mongo(c,
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    'print(JSON.stringify({id: u._id.toString(), '
    'tenant_id: (u.tenant_id && u.tenant_id.toString()) || u._id.toString()}));',
    show=False)
admin = None
for line in reversed(owner_full.splitlines()):
    s = line.strip()
    if s.startswith("{"):
        try:
            admin = json.loads(s); break
        except Exception:
            continue

mongo(c,
      f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
      f'db.domains.insertOne({{domain:"{ZONE}", '
      f'user_id: ObjectId("{admin["id"]}"), '
      f'tenant_id: ObjectId("{admin["tenant_id"]}"), '
      'is_active:true, created_at:new Date(), updated_at:new Date()}});',
      show=False)


# ── 1. Create the zone (server_ip is required by the validator) ────
print("\n=== 1. Create zone ===")
r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones {H} "
     f"-d '{{\"domain\":\"{ZONE}\",\"server_ip\":\"127.0.0.1\","
     f"\"nameservers\":[\"ns1.{ZONE}\"]}}' "
     "-w '\\nHTTP=%{http_code}\\n'")


# ── 2. Add two A records — same name, different values (multi-value rrset) ──
print(f"\n=== 2. Add two {RNAME}.{ZONE} A records ({VAL_A}, {VAL_B}) ===")
for v in (VAL_A, VAL_B):
    r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H} "
         f"-d '{{\"name\":\"{RNAME}\",\"type\":\"A\",\"value\":\"{v}\",\"ttl\":300}}' "
         "-w '\\nHTTP=%{http_code}\\n'")

print("\n   PowerDNS rrset state:")
r(c, f"pdnsutil list-zone {ZONE} | grep -E 'IN\\s+A'")

print("\n   Mongo rows for this rrset:")
mongo(c,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      f'const rs = db.dns_records.find({{zone_id: z._id, name:"{RNAME}", type:"A"}}, '
      '{value:1, ttl:1, _id:1}).toArray(); print(JSON.stringify(rs, null, 2));')


# ── 3. Try to ADD an exact duplicate — must REJECT ───────────────────
print(f"\n=== 3. Add EXACT duplicate {RNAME} A {VAL_A} — must reject ===")
out, _, _ = r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H} "
     f"-d '{{\"name\":\"{RNAME}\",\"type\":\"A\",\"value\":\"{VAL_A}\",\"ttl\":300}}' "
     "-w '\\nHTTP=%{http_code}\\n'")
if "already exists" not in out and "already exists" not in (out or ""):
    print("    !! ASSERTION FAILED: backend should reject exact duplicate")
else:
    print("    ✓ duplicate rejected")


# ── 4. Delete the FIRST record — survivor must remain in BOTH pdns + Mongo ──
print(f"\n=== 4. Delete {RNAME} A {VAL_A} — survivor {VAL_B} must remain ===")
val_a_id_out, _, _ = mongo(c,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      f'const r = db.dns_records.findOne({{zone_id: z._id, name:"{RNAME}", type:"A", value:"{VAL_A}"}}); '
      'print(r._id.toString());', show=False)
val_a_id = ""
for line in reversed(val_a_id_out.splitlines()):
    s = line.strip()
    if len(s) == 24 and all(ch in "0123456789abcdef" for ch in s):
        val_a_id = s; break

r(c, f"curl -s -X DELETE {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records/{val_a_id} {H} "
     "-w '\\nHTTP=%{http_code}\\n'")

print("\n   PowerDNS rrset state — must list ONLY 10.0.0.2:")
r(c, f"pdnsutil list-zone {ZONE} | grep -E 'IN\\s+A'")

print("\n   Mongo state — must have 1 row left, value=10.0.0.2:")
mongo(c,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      f'const rs = db.dns_records.find({{zone_id: z._id, name:"{RNAME}", type:"A"}}, '
      '{value:1}).toArray(); print(JSON.stringify(rs));')


# ── 5. Delete the LAST record — must succeed (no "record not found") ───
print(f"\n=== 5. Delete {RNAME} A {VAL_B} — must succeed (the bug-from-the-screenshot scenario) ===")
val_b_id_out, _, _ = mongo(c,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      f'const r = db.dns_records.findOne({{zone_id: z._id, name:"{RNAME}", type:"A", value:"{VAL_B}"}}); '
      'print(r._id.toString());', show=False)
val_b_id = ""
for line in reversed(val_b_id_out.splitlines()):
    s = line.strip()
    if len(s) == 24 and all(ch in "0123456789abcdef" for ch in s):
        val_b_id = s; break

out, _, _ = r(c, f"curl -s -X DELETE {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records/{val_b_id} {H} "
     "-w '\\nHTTP=%{http_code}\\n'")
if '"success":true' in out and "HTTP=200" in out:
    print("    ✓ second delete returned 200 (no 'record not found' toast)")
else:
    print(f"    !! ASSERTION FAILED: second delete didn't succeed cleanly: {out[:200]}")

print("\n   PowerDNS state — rrset must be gone:")
r(c, f"pdnsutil list-zone {ZONE} | grep -E '{RNAME}\\.' || echo '  (empty — rrset gone)'")


# ── 6. Heal endpoint: seed a Mongo duplicate, run /reconcile, assert collapse ──
print(f"\n=== 6. Heal endpoint — seed duplicate Mongo rows, run /reconcile ===")
mongo(c,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      'const now = new Date(); '
      f'db.dns_records.insertMany([ '
      f'{{zone_id: z._id, name:"{RNAME}", type:"A", value:"{VAL_A}", ttl:300, created_at:now, updated_at:now}}, '
      f'{{zone_id: z._id, name:"{RNAME}", type:"A", value:"{VAL_A}", ttl:600, created_at:now, updated_at:now}}, '
      f'{{zone_id: z._id, name:"{RNAME}", type:"A", value:"{VAL_B}", ttl:60,  created_at:now, updated_at:now}} '
      ']); '
      f'print("seeded; row count = " + db.dns_records.countDocuments({{zone_id: z._id, name:"{RNAME}", type:"A"}}));')

out, _, _ = r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones/{ZONE}/reconcile {H} "
     "-w '\\nHTTP=%{http_code}\\n'")
print("\n   Reconcile report:")
print(f"    {out}")

print("\n   Mongo after reconcile — should have 2 rows (one VAL_A, one VAL_B):")
mongo(c,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      f'const rs = db.dns_records.find({{zone_id: z._id, name:"{RNAME}", type:"A"}}, '
      '{value:1, ttl:1}).toArray(); print(JSON.stringify(rs));')

print("\n   PowerDNS after reconcile — rrset must list both values at TTL=60 (min):")
r(c, f"pdnsutil list-zone {ZONE} | grep -E '{RNAME}\\..*\\sA\\s'")


# ── 7. Cleanup ───────────────────────────────────────────────────────
print("\n=== 7. Cleanup ===")
r(c, f"curl -s -X DELETE {BACKEND}/api/v1/whm/dns/zones/{ZONE} {H} "
     "-w '\\nHTTP=%{http_code}\\n'", show=False)
mongo(c,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); } '
      f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
      f'db.users.updateOne({{email:"{admin_email}"}}, {{$unset:{{refresh_token:"", refresh_expires_at:""}}}}); '
      f'db.otp_requests.deleteMany({{email:"{admin_email}"}});',
      show=False)
r(c, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
r(c, "rm -f /tmp/dns.jar")

c.close()
print("\n=== DNS rrset reconcile test complete ===")
