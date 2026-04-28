"""Reproduces the user's exact action on v3.0.12 to confirm it works:

  Existing rrset:  dns2 A 187.127.145.5  (single-value)
  Operator types:  Name = dns2.betazeninfotech.com.   (FQDN with dot)
                   Type = A
                   Value = 192.168.1.50               (DIFFERENT value)
                   TTL  = 60
  Click Save.

Expected on v3.0.12:
  * The FQDN name canonicalizes to `dns2`.
  * The new value is DIFFERENT from the existing one, so the dup
    check passes — Mongo gets a SECOND row, the rrset becomes
    multi-value.
  * pdns rrset now serves [187.127.145.5, 192.168.1.50] at TTL=60
    (the min — DNS spec says rrset shares one TTL).
  * Listing shows TWO `dns2 A` rows, both with real Mongo IDs.
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
ZONE = "user-add-fqdn-smoketest.invalid"


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

# Setup token
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

mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
         'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); }',
      show=False)
mongo(c, f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
         f'db.domains.insertOne({{domain:"{ZONE}", '
         f'user_id: ObjectId("{admin["id"]}"), tenant_id: ObjectId("{admin["tenant_id"]}"), '
         'is_active:true, created_at:new Date(), updated_at:new Date()}});', show=False)
r(c, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
mongo(c, f'db.otp_requests.deleteMany({{email:"{admin["email"]}"}});', show=False)
r(c, f"rm -f /tmp/u.jar; curl -s -c /tmp/u.jar -X POST {BACKEND}/api/v1/auth/otp/request "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"email\":\"{admin['email']}\",\"surface\":\"whm\"}}'", show=False)
mongo(c,
      'const crypto = require("crypto"); '
      'const h = crypto.createHash("sha256").update("USRSCN1234").digest("hex"); '
      f'db.otp_requests.updateMany({{email:"{admin["email"]}", used:false, expires_at:{{$gt:new Date()}}}}, '
      '{$set:{code_hash:h}});', show=False)
out = r(c, f"curl -s -b /tmp/u.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
       f"-H 'Content-Type: application/json' "
       f"-d '{{\"email\":\"{admin['email']}\",\"code\":\"USRSCN1234\"}}'", show=False)
token = json.loads(out).get("data", {}).get("access_token", "")
H = f"-H 'Authorization: Bearer {token}'"


print("=== 1. Create zone (server_ip=187.127.145.5 like user's prod) ===")
r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones {H} "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"domain\":\"{ZONE}\",\"server_ip\":\"187.127.145.5\","
     f"\"nameservers\":[\"ns1.{ZONE}\"]}}' "
     "-w '\\nHTTP=%{http_code}\\n'", show=False)


print("=== 2. Pre-seed an existing dns2 A record (mirrors user's prod state) ===")
r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H} "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"name\":\"dns2\",\"type\":\"A\",\"value\":\"187.127.145.5\",\"ttl\":60}}'")


print("=== 3. THE USER'S ACTION: add the FQDN-with-dot form, NEW value ===")
print("    Form payload: {name: 'dns2." + ZONE + ".', type: 'A', value: '192.168.1.50', ttl: 60}")
out = r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H} "
        f"-H 'Content-Type: application/json' "
        f"-d '{{\"name\":\"dns2.{ZONE}.\",\"type\":\"A\",\"value\":\"192.168.1.50\",\"ttl\":60}}' "
        "-w '\\nHTTP=%{http_code}\\n'")
print()


print("=== 4. List records — both dns2 values must be visible ===")
out = r(c, f"curl -s {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H}", show=False)
data = json.loads(out).get("data", [])
dns2_rows = [d for d in data if d.get("type") == "A" and d.get("name") == "dns2"]
print(f"\n   dns2 A rows in API response: {len(dns2_rows)} (expected 2)")
for d in dns2_rows:
    print(f"     - id={d['id'][:8]}…  value={d['value']}  ttl={d['ttl']}")


print("\n=== 5. PowerDNS state — rrset must list BOTH values at min TTL ===")
r(c, f"pdnsutil list-zone {ZONE} | grep -E '^dns2\\.' | head")


print("\n=== 6. Mongo state — sanity check there's no FQDN-shape orphan row ===")
mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
         'const rs = db.dns_records.find({zone_id: z._id, type:"A", '
         'name: /dns2/}, {name:1, value:1}).toArray(); '
         'print(JSON.stringify(rs, null, 2));')


print("\n=== 7. Now DELETE the new value (the click that previously didn't work) ===")
new_row = next((d for d in dns2_rows if d.get("value") == "192.168.1.50"), None)
if new_row:
    out = r(c, f"curl -s -X DELETE {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records/{new_row['id']} {H} "
           "-w '\\nHTTP=%{http_code}\\n'")


print("\n=== 8. Final state — only the original dns2 value should remain ===")
r(c, f"pdnsutil list-zone {ZONE} | grep -E '^dns2\\.' | head")
mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
         'const rs = db.dns_records.find({zone_id: z._id, type:"A", '
         'name: /dns2/}, {name:1, value:1}).toArray(); '
         'print("rows: " + rs.length + " " + JSON.stringify(rs.map(r => r.value)));')


# Cleanup
mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
         'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); } '
         f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
         f'db.users.updateOne({{email:"{admin["email"]}"}}, {{$unset:{{refresh_token:"", refresh_expires_at:""}}}}); '
         f'db.otp_requests.deleteMany({{email:"{admin["email"]}"}});', show=False)
r(c, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
r(c, "rm -f /tmp/u.jar", show=False)
c.close()
