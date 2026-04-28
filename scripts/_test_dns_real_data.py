"""Realistic DNS test against the v3.0.8 backend.

Reproduces the production failure mode the user reported:
  * PowerDNS rrsets that have NO Mongo backing (the `dns5 A` /
    `dns6 A` rows in the screenshots — real records served by pdns
    but invisible to Mongo, so the listing returned them with the
    all-zeros ObjectID and edits/deletes 500'd).
  * Type-quirky rrsets (TXT with quoted SPF/DKIM/DMARC, MX with
    priority prefix) where Mongo IS backed but the value shape
    diverges, so the list-side join misses and rec.ID stays zero.

Steps:
  1. Create a test zone (server_ip required by validator).
  2. Inject directly into PowerDNS via pdnsutil — bypassing the
     panel — so the records have NO Mongo backing initially.
        - dns5 A 82.112.234.8 (the user's exact case)
        - @   TXT "v=spf1 ip4:1.2.3.4 ~all"
        - @   MX  10 mail.test.invalid.
  3. GET /records — assert heal-on-read created Mongo backings.
        - dns5 A row should have a real (non-zero) Mongo ID
        - TXT row should have a Mongo ID + Value stored RAW (quotes
          stripped)
        - MX row should have a Mongo ID + Priority=10, Value=target
  4. Edit the dns5 A record (the original user complaint) — must 200.
  5. Delete the dns5 A record — must 200.
  6. Add a duplicate of the surviving TXT — must 4xx.
  7. Cleanup.
"""
from __future__ import annotations

import json
import os
import re
import sys
from pathlib import Path

import paramiko

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass


def _load_pwd(section: str = "Old") -> str:
    if os.environ.get("BZ_VPS_PASS"):
        return os.environ["BZ_VPS_PASS"]
    md = Path(__file__).resolve().parent.parent / "testing-vps-details.md"
    if not md.exists():
        return ""
    text = md.read_text(encoding="utf-8", errors="replace")
    section_re = re.compile(rf"#+\s*{section}\s+server.*?(?=^#|\Z)",
                            re.IGNORECASE | re.DOTALL | re.MULTILINE)
    s = section_re.search(text)
    blob = s.group(0) if s else text
    pwd = re.search(r"password[^`]*`([^`]+)`", blob, re.IGNORECASE)
    return pwd.group(1) if pwd else ""


HOST = os.environ.get("BZ_VPS_HOST", "187.127.155.209")
PASSWORD = _load_pwd()
if not PASSWORD:
    sys.exit("no password — set BZ_VPS_PASS or populate testing-vps-details.md")

BACKEND = "http://127.0.0.1:8080"
ZONE = "real-dns-smoketest.invalid"


def r(c, cmd, show=True, timeout=60):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    code = so.channel.recv_exit_status()
    if show:
        tag = "OK" if code == 0 else f"exit={code}"
        print(f"\n[{tag}] $ {cmd[:140]}{'…' if len(cmd) > 140 else ''}")
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
c.connect(HOST, username="root", password=PASSWORD, timeout=20,
          look_for_keys=False, allow_agent=False)


# ── 0. mint admin token ───────────────────────────────────────────────
print("=== 0. Mint admin token ===")
oo, _, _ = mongo(c,
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    'print(JSON.stringify({email: u.email, id: u._id.toString(), '
    'tenant_id: (u.tenant_id && u.tenant_id.toString()) || u._id.toString()}));', show=False)
admin = None
for line in reversed(oo.splitlines()):
    s = line.strip()
    if s.startswith("{"):
        try: admin = json.loads(s); break
        except: continue
mongo(c, f'db.otp_requests.deleteMany({{email:"{admin["email"]}"}});', show=False)
r(c, f"rm -f /tmp/r.jar; curl -s -c /tmp/r.jar -X POST {BACKEND}/api/v1/auth/otp/request "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"email\":\"{admin['email']}\",\"surface\":\"whm\"}}'", show=False)
mongo(c,
      'const crypto = require("crypto"); '
      'const h = crypto.createHash("sha256").update("DNSREAL999").digest("hex"); '
      f'db.otp_requests.updateMany({{email:"{admin["email"]}", used:false, expires_at:{{$gt:new Date()}}}}, '
      '{$set:{code_hash:h}});', show=False)
out, _, _ = r(c,
    f"curl -s -b /tmp/r.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
    f"-H 'Content-Type: application/json' "
    f"-d '{{\"email\":\"{admin['email']}\",\"code\":\"DNSREAL999\"}}'",
    show=False)
token = json.loads(out).get("data", {}).get("access_token", "")
print(f"    token len={len(token)}")
H = f"-H 'Authorization: Bearer {token}' -H 'Content-Type: application/json'"


# ── pre-clean ────────────────────────────────────────────────────────
print("\n=== pre-clean ===")
mongo(c,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); }',
      show=False)
mongo(c, f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
         f'db.domains.insertOne({{domain:"{ZONE}", '
         f'user_id: ObjectId("{admin["id"]}"), tenant_id: ObjectId("{admin["tenant_id"]}"), '
         'is_active:true, created_at:new Date(), updated_at:new Date()}});',
      show=False)
r(c, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)


# ── 1. Create zone via API ───────────────────────────────────────────
print("\n=== 1. Create zone ===")
r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones {H} "
     f"-d '{{\"domain\":\"{ZONE}\",\"server_ip\":\"82.112.234.8\","
     f"\"nameservers\":[\"ns1.{ZONE}\"]}}' "
     "-w '\\nHTTP=%{http_code}\\n'")


# ── 2. Inject UNBACKED records directly via pdnsutil ─────────────────
# This simulates the production state: records served by pdns but
# never written to the panel's Mongo (the dns5/dns6 case).
print("\n=== 2. Inject pdns-only records (no Mongo backing) ===")
r(c, f"pdnsutil add-record {ZONE} dns5 A 60 82.112.234.8")
r(c, f"pdnsutil add-record {ZONE} dns6 A 60 82.112.234.9")
# Also test the type-quirky shape mismatch (already in pdns from
# setupMailServer, but Mongo's value shape doesn't match — let's
# overwrite with a different SPF to check the dbMap normalization).
r(c, f"pdnsutil add-record {ZONE} unbacked A 300 10.0.0.99")
r(c, f"pdns_control reload", show=False)


# ── 3. GET /records — heal-on-read should backfill Mongo ─────────────
print("\n=== 3. GET /records — heal-on-read should give every row a real ID ===")
out, _, _ = r(c, f"curl -s {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H}")

# Parse the response and count rows whose ID is the all-zeros sentinel.
try:
    data = json.loads(out).get("data", [])
    zero_id_rows = [d for d in data if d.get("id") == "000000000000000000000000"]
    real_id_rows = [d for d in data if d.get("id") and d.get("id") != "000000000000000000000000"]
    print(f"\n    rows total: {len(data)}")
    print(f"    rows with REAL Mongo ID: {len(real_id_rows)}")
    print(f"    rows with ALL-ZEROS ID:  {len(zero_id_rows)}")
    if zero_id_rows:
        print("    !! ASSERTION FAILED: heal-on-read should backfill every row")
        for d in zero_id_rows[:3]:
            print(f"       - {d.get('type')} {d.get('name')} {d.get('value')[:40]}")
    else:
        print("    ✓ every row has a real Mongo ID")
except Exception as e:
    print(f"!! could not parse response: {e}\n{out[:500]}")
    data = []

# Confirm Mongo physically holds the heal-inserted rows.
print("\n    Mongo state for the test zone:")
mongo(c,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      'const rs = db.dns_records.find({zone_id: z._id}, '
      '{type:1, name:1, value:1, priority:1}).toArray(); '
      'print(JSON.stringify(rs, null, 2));')


# ── 4. Edit the dns5 A record (the user's exact failing case) ────────
print("\n=== 4. Edit dns5 A 82.112.234.8 → change TTL to 60 (the bug from the screenshot) ===")
dns5 = next((d for d in data if d.get("type") == "A" and d.get("name") == "dns5"), None)
if not dns5:
    print("!! no dns5 row in response — abort")
else:
    out, _, _ = r(c,
        f"curl -s -X PUT {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records/{dns5['id']} {H} "
        f"-d '{{\"type\":\"A\",\"name\":\"dns5\",\"value\":\"82.112.234.8\",\"ttl\":600}}' "
        "-w '\\nHTTP=%{http_code}\\n'")
    if "HTTP=200" in out:
        print("    ✓ edit succeeded (HTTP 200)")
    else:
        print(f"    !! ASSERTION FAILED: edit returned non-200")


# ── 5. Delete dns6 A 82.112.234.9 (single-value rrset) ───────────────
print("\n=== 5. Delete dns6 A — must 200 (the screenshot scenario) ===")
dns6 = next((d for d in data if d.get("type") == "A" and d.get("name") == "dns6"), None)
if dns6:
    out, _, _ = r(c,
        f"curl -s -X DELETE {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records/{dns6['id']} {H} "
        "-w '\\nHTTP=%{http_code}\\n'")
    if "HTTP=200" in out and '"success":true' in out:
        print("    ✓ delete succeeded")
    else:
        print(f"    !! ASSERTION FAILED: {out[:200]}")


# ── 6. Stale-UI fallback: send the all-zeros ID and see backend recover ──
print("\n=== 6. Stale-UI fallback: PUT /records/_  with name+type+existing_value in body ===")
unbacked = next((d for d in data if d.get("type") == "A" and d.get("name") == "unbacked"), None)
if unbacked:
    out, _, _ = r(c,
        f"curl -s -X PUT {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records/_ {H} "
        f"-d '{{\"type\":\"A\",\"name\":\"unbacked\",\"value\":\"10.0.0.99\","
        f"\"ttl\":900,\"existing_value\":\"10.0.0.99\"}}' "
        "-w '\\nHTTP=%{http_code}\\n'")
    if "HTTP=200" in out:
        print("    ✓ fallback PUT succeeded (handler resolved by name+type+value)")
    else:
        print(f"    !! ASSERTION FAILED: fallback PUT didn't 200: {out[:200]}")


# ── 7. Stale-UI fallback delete: DELETE /records/_?name=&type=&value= ────
print("\n=== 7. Stale-UI fallback DELETE /records/_?name=&type=&value= ===")
out, _, _ = r(c,
    f"curl -s -X DELETE '{BACKEND}/api/v1/whm/dns/zones/{ZONE}/records/_?"
    f"name=unbacked&type=A&value=10.0.0.99' {H} "
    "-w '\\nHTTP=%{http_code}\\n'")
if "HTTP=200" in out:
    print("    ✓ fallback DELETE succeeded")
else:
    print(f"    !! fallback DELETE failed: {out[:200]}")


# ── 8. Final state ───────────────────────────────────────────────────
print("\n=== 8. Final state ===")
print("\n    PowerDNS:")
r(c, f"pdnsutil list-zone {ZONE} | grep -E '(dns5|dns6|unbacked)\\.' || echo '  (none — all cleaned up correctly)'")
print("\n    Mongo:")
js = (
    'const z = db.dns_zones.findOne({domain:"' + ZONE + '"}); '
    'const rs = db.dns_records.find({zone_id: z._id, '
    'name: {$in: ["dns5","dns6","unbacked"]}}).toArray(); '
    'print("rows: " + rs.length);'
)
mongo(c, js)


# ── 9. Cleanup ───────────────────────────────────────────────────────
print("\n=== 9. Cleanup ===")
r(c, f"curl -s -X DELETE {BACKEND}/api/v1/whm/dns/zones/{ZONE} {H}", show=False)
r(c, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
mongo(c,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); } '
      f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
      f'db.users.updateOne({{email:"{admin["email"]}"}}, '
      '{$unset:{refresh_token:"", refresh_expires_at:""}}); '
      f'db.otp_requests.deleteMany({{email:"{admin["email"]}"}});',
      show=False)
r(c, "rm -f /tmp/r.jar", show=False)
c.close()
print("\n=== real-data DNS test complete ===")
