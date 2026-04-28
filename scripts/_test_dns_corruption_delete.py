"""Reproduces the user's production failure mode and verifies the
v3.0.12 fix:

  * Mongo zone has multiple legacy rows for the same logical record,
    with three different name shapes (the pre-3.0.11 corruption that
    a long-lived production zone accumulates from the FQDN-input bug).
  * pdns has the rrset under the canonical relative name.
  * Operator opens the WHM DNS Zones page → heal-on-read inserts a
    fresh canonical row, listing renders rows with ID = the canonical
    one's _id.
  * Operator clicks DELETE on the visible row.
  * Pre-3.0.12: pdnsutil delete-rrset with the legacy FQDN name
    double-suffixed and missed; rrset stayed in pdns; next listing
    re-healed the row; "delete not work" loop.
  * v3.0.12: DeleteRecord wipes every name-shape duplicate, reconcile
    uses normalized name → pdnsutil gets the relative form → rrset
    actually leaves pdns.

Asserts after a single delete click:
  - 0 Mongo rows remain for that record (every shape gone)
  - 0 pdns rrsets remain at that name (no double-suffix junk either)
  - Re-listing returns nothing for that name (no resurrection)
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
ZONE = "delete-corruption-smoketest.invalid"


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

# Setup
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
mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
         'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); }',
      show=False)
mongo(c, f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
         f'db.domains.insertOne({{domain:"{ZONE}", '
         f'user_id: ObjectId("{admin["id"]}"), tenant_id: ObjectId("{admin["tenant_id"]}"), '
         'is_active:true, created_at:new Date(), updated_at:new Date()}});', show=False)
r(c, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
mongo(c, f'db.otp_requests.deleteMany({{email:"{admin["email"]}"}});', show=False)

r(c, f"rm -f /tmp/dc.jar; curl -s -c /tmp/dc.jar -X POST {BACKEND}/api/v1/auth/otp/request "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"email\":\"{admin['email']}\",\"surface\":\"whm\"}}'", show=False)
mongo(c,
      'const crypto = require("crypto"); '
      'const h = crypto.createHash("sha256").update("DELCORR1234").digest("hex"); '
      f'db.otp_requests.updateMany({{email:"{admin["email"]}", used:false, expires_at:{{$gt:new Date()}}}}, '
      '{$set:{code_hash:h}});', show=False)
out = r(c, f"curl -s -b /tmp/dc.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
       f"-H 'Content-Type: application/json' "
       f"-d '{{\"email\":\"{admin['email']}\",\"code\":\"DELCORR1234\"}}'", show=False)
token = json.loads(out).get("data", {}).get("access_token", "")
H = f"-H 'Authorization: Bearer {token}'"


print("=== 1. Create zone ===")
r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones {H} "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"domain\":\"{ZONE}\",\"server_ip\":\"82.112.234.8\","
     f"\"nameservers\":[\"ns1.{ZONE}\"]}}' "
     "-w '\\nHTTP=%{http_code}\\n'", show=False)


print("=== 2. Seed pdns with the canonical record (operator's actual zone state) ===")
r(c, f"pdnsutil add-record {ZONE} ns5 A 60 82.112.234.8")
r(c, "pdns_control reload", show=False)


print("=== 3. Inject LEGACY non-canonical Mongo rows (pre-3.0.11 corruption shape) ===")
zone_id_out = mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); print(z._id.toString());', show=False)
zone_id = ""
for line in reversed(zone_id_out.splitlines()):
    s = line.strip()
    if len(s) == 24 and all(ch in "0123456789abcdef" for ch in s):
        zone_id = s; break

mongo(c,
      'const z_id = ObjectId("' + zone_id + '"); '
      'const now = new Date(); '
      'db.dns_records.insertMany([ '
      '{zone_id: z_id, type:"A", name:"ns5", value:"82.112.234.8", ttl:60, created_at:now, updated_at:now}, '
      f'{{zone_id: z_id, type:"A", name:"ns5.{ZONE}", value:"82.112.234.8", ttl:60, created_at:now, updated_at:now}}, '
      f'{{zone_id: z_id, type:"A", name:"ns5.{ZONE}.", value:"82.112.234.8", ttl:60, created_at:now, updated_at:now}} '
      ']); '
      'print("seeded 3 legacy-shape rows for ns5 A");')


print("=== 4. List records — heal-on-read kicks in; figure out what ID the row has ===")
out = r(c, f"curl -s {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H}", show=False)
data = json.loads(out).get("data", [])
ns5 = next((d for d in data if d.get("type") == "A" and d.get("name") == "ns5"), None)
print(f"   ns5 row in API response: {ns5}")


print("=== 5. Mongo state before delete (should show all 3 legacy + canonical from heal) ===")
mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
         'const rs = db.dns_records.find({zone_id: z._id, type:"A", '
         'name: /ns5/}).toArray(); '
         'print("rows: " + rs.length);')


print(f"\n=== 6. CLICK DELETE — frontend hits DELETE /records/{ns5['id'][:8]}… ===")
out = r(c, f"curl -s -X DELETE {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records/{ns5['id']} {H} "
       "-w '\\nHTTP=%{http_code}\\n'")


print("\n=== 7. Mongo after delete — every name shape for ns5 A 82.112.234.8 must be GONE ===")
mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
         'const rs = db.dns_records.find({zone_id: z._id, type:"A", '
         'name: /ns5/}, {name:1, value:1}).toArray(); '
         'print(JSON.stringify({surviving: rs.length, rows: rs}));')


print("\n=== 8. PowerDNS after delete — ns5 A rrset must be GONE (no double-suffix junk) ===")
r(c, f"pdnsutil list-zone {ZONE} | grep -E 'ns5' || echo '  (clean — no ns5 anywhere)'")


print("\n=== 9. Re-list records — ns5 must NOT resurrect via heal-on-read ===")
out = r(c, f"curl -s {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H}", show=False)
data = json.loads(out).get("data", [])
ns5_after = [d for d in data if d.get("name") == "ns5"]
if ns5_after:
    print(f"   !! FAIL: ns5 reappeared in listing: {ns5_after}")
else:
    print("   ✓ ns5 stayed deleted (no resurrection)")


# Cleanup
mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
         'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); } '
         f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
         f'db.users.updateOne({{email:"{admin["email"]}"}}, {{$unset:{{refresh_token:"", refresh_expires_at:""}}}}); '
         f'db.otp_requests.deleteMany({{email:"{admin["email"]}"}});', show=False)
r(c, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
r(c, "rm -f /tmp/dc.jar", show=False)
c.close()
