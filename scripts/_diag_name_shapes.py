"""Reproduce the WHM 'already exists' rejection on FQDN-named A records.

Hypothesis: heal-on-read stores names in zone-relative form (ns1) but
the operator's form input lands as FQDN-with-trailing-dot
(ns1.zone.invalid.). The dup check uses an exact string match, so
adding a record by its FQDN shape can either:
  (a) succeed and create a parallel Mongo row (data-corruption bug), OR
  (b) fail with 'already exists' if the name happens to match in raw form
     (which would mean BOTH shapes coexist in Mongo for the same rrset).

Test: seed via ListRecords (which heals to relative), then try Add by
FQDN. Inspect every name shape Mongo carries.
"""
import json, os, re, sys
from pathlib import Path
import paramiko

try: sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception: pass

md = Path(__file__).resolve().parent.parent / "testing-vps-details.md"
text = md.read_text(encoding="utf-8", errors="replace")
m = re.search(r"#+\s*Old\s+server.*?password[^`]*`([^`]+)`", text, re.IGNORECASE | re.DOTALL)
PWD = m.group(1)
HOST = "187.127.155.209"
BACKEND = "http://127.0.0.1:8080"
ZONE = "name-shape-diag.invalid"


def r(c, cmd, show=True, timeout=60):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    if show:
        print(f"\n$ {cmd[:120]}{'…' if len(cmd) > 120 else ''}")
        for line in (out+err).strip().splitlines()[:25]:
            print(f"    {line}")
    return out.strip()


def mongo(c, q, show=True):
    safe = q.replace("'", "'\\''")
    return r(c, 'cd /opt/serverpanel; . ./.env 2>/dev/null || true; '
        'URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; '
        f'mongosh --quiet "$URI" --eval \'{safe}\'', show=show)


c = paramiko.SSHClient(); c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, username="root", password=PWD, timeout=20, look_for_keys=False, allow_agent=False)

# Setup: token + zone shell
oo = mongo(c,
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    'print(JSON.stringify({email: u.email, id: u._id.toString(), '
    'tenant_id: (u.tenant_id && u.tenant_id.toString()) || u._id.toString()}));', show=False)
admin = None
for line in reversed(oo.splitlines()):
    s = line.strip()
    if s.startswith("{"):
        try: admin = json.loads(s); break
        except: continue

# Pre-clean
mongo(c,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); }',
      show=False)
mongo(c, f'db.domains.deleteMany({{domain:"{ZONE}"}});'
         f'db.domains.insertOne({{domain:"{ZONE}", '
         f'user_id: ObjectId("{admin["id"]}"), tenant_id: ObjectId("{admin["tenant_id"]}"), '
         'is_active:true, created_at:new Date(), updated_at:new Date()}});',
      show=False)
r(c, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
mongo(c, f'db.otp_requests.deleteMany({{email:"{admin["email"]}"}});', show=False)
r(c, f"rm -f /tmp/d.jar; curl -s -c /tmp/d.jar -X POST {BACKEND}/api/v1/auth/otp/request "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"email\":\"{admin['email']}\",\"surface\":\"whm\"}}'", show=False)
mongo(c,
      'const crypto = require("crypto"); '
      'const h = crypto.createHash("sha256").update("DIAG12345").digest("hex"); '
      f'db.otp_requests.updateMany({{email:"{admin["email"]}", used:false, expires_at:{{$gt:new Date()}}}}, '
      '{$set:{code_hash:h}});', show=False)
out = r(c, f"curl -s -b /tmp/d.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
       f"-H 'Content-Type: application/json' "
       f"-d '{{\"email\":\"{admin['email']}\",\"code\":\"DIAG12345\"}}'", show=False)
token = json.loads(out).get("data", {}).get("access_token", "")
H = f"-H 'Authorization: Bearer {token}'"

print("=== 1. Create zone ===")
r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones {H} "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"domain\":\"{ZONE}\",\"server_ip\":\"82.112.234.8\","
     f"\"nameservers\":[\"ns1.{ZONE}\"]}}' "
     "-w '\\nHTTP=%{http_code}\\n'", show=False)

print("\n=== 2. Add ns1 A 82.112.234.8 via API (zone-relative name) ===")
r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H} "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"name\":\"ns1\",\"type\":\"A\",\"value\":\"82.112.234.8\",\"ttl\":60}}' "
     "-w '\\nHTTP=%{http_code}\\n'")

print("\n=== 3. Try to add SAME record via FQDN-with-trailing-dot ===")
r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H} "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"name\":\"ns1.{ZONE}.\",\"type\":\"A\",\"value\":\"82.112.234.8\",\"ttl\":60}}' "
     "-w '\\nHTTP=%{http_code}\\n'")

print("\n=== 4. Try to add SAME record via FQDN without trailing dot ===")
r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H} "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"name\":\"ns1.{ZONE}\",\"type\":\"A\",\"value\":\"82.112.234.8\",\"ttl\":60}}' "
     "-w '\\nHTTP=%{http_code}\\n'")

print("\n=== 5. Inspect Mongo state — what name shapes exist? ===")
mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
         'const rs = db.dns_records.find({zone_id: z._id, type:"A", '
         'name: /ns1/}, {name:1, value:1, _id:1}).toArray(); '
         'print(JSON.stringify(rs, null, 2));')

print("\n=== 6. Inspect PowerDNS state — was the FQDN add accepted? ===")
r(c, f"pdnsutil list-zone {ZONE} | grep -E 'ns1\\.' || echo '  (no ns1 in pdns)'")

# Cleanup
mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
         'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); } '
         f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
         f'db.users.updateOne({{email:"{admin["email"]}"}}, {{$unset:{{refresh_token:"", refresh_expires_at:""}}}}); '
         f'db.otp_requests.deleteMany({{email:"{admin["email"]}"}});', show=False)
r(c, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
r(c, "rm -f /tmp/d.jar", show=False)
c.close()
