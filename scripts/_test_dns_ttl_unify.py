"""Verify the v3.0.13 TTL unification: Mongo rows for one rrset always
share the same TTL, matching what pdns actually serves.

Three scenarios:
  1. Add a 2nd value with DIFFERENT TTL to an existing single-value
     rrset — both Mongo rows + pdns must end up at the new TTL
     (last-write-wins).
  2. Edit one row's TTL — sibling row's TTL must follow.
  3. Seed a corrupted state directly in Mongo (mismatched TTLs from
     pre-3.0.13 writes), trigger reconcile via any add/update — both
     rows must converge.
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
ZONE = "ttl-unify-smoketest.invalid"


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

# Bootstrap
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
         'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); }', show=False)
mongo(c, f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
         f'db.domains.insertOne({{domain:"{ZONE}", '
         f'user_id: ObjectId("{admin["id"]}"), tenant_id: ObjectId("{admin["tenant_id"]}"), '
         'is_active:true, created_at:new Date(), updated_at:new Date()}});', show=False)
r(c, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
mongo(c, f'db.otp_requests.deleteMany({{email:"{admin["email"]}"}});', show=False)
r(c, f"rm -f /tmp/t.jar; curl -s -c /tmp/t.jar -X POST {BACKEND}/api/v1/auth/otp/request "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"email\":\"{admin['email']}\",\"surface\":\"whm\"}}'", show=False)
mongo(c,
      'const crypto = require("crypto"); '
      'const h = crypto.createHash("sha256").update("TTL999999").digest("hex"); '
      f'db.otp_requests.updateMany({{email:"{admin["email"]}", used:false, expires_at:{{$gt:new Date()}}}}, '
      '{$set:{code_hash:h}});', show=False)
out = r(c, f"curl -s -b /tmp/t.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
       f"-H 'Content-Type: application/json' "
       f"-d '{{\"email\":\"{admin['email']}\",\"code\":\"TTL999999\"}}'", show=False)
token = json.loads(out).get("data", {}).get("access_token", "")
H = f"-H 'Authorization: Bearer {token}'"

r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones {H} "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"domain\":\"{ZONE}\",\"server_ip\":\"82.112.234.8\","
     f"\"nameservers\":[\"ns1.{ZONE}\"]}}'", show=False)


def show_ttls(label):
    print(f"\n   {label}:")
    mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
             'const rs = db.dns_records.find({zone_id: z._id, type:"A", '
             'name:"web"}, {value:1, ttl:1, _id:0}).sort({value:1}).toArray(); '
             'print("   Mongo: " + JSON.stringify(rs));')
    r(c, f'pdnsutil list-zone {ZONE} 2>/dev/null | awk \'$1=="web.{ZONE}." && $4=="A" {{print "   pdns:  " $5 " ttl=" $2}}\' | sort')


# ── 1. Add first record with TTL=3600 ─────────────────────────────────
print("=== 1. Add `web A 1.1.1.1 ttl=3600` ===")
r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H} "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"name\":\"web\",\"type\":\"A\",\"value\":\"1.1.1.1\",\"ttl\":3600}}' "
     "-w '\\nHTTP=%{http_code}\\n'")


# ── 2. Add 2nd value at TTL=60 (THE common scenario) ─────────────────
print("\n=== 2. Add `web A 2.2.2.2 ttl=60` (different TTL!) ===")
r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H} "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"name\":\"web\",\"type\":\"A\",\"value\":\"2.2.2.2\",\"ttl\":60}}' "
     "-w '\\nHTTP=%{http_code}\\n'")
show_ttls("After Add (both Mongo rows + pdns must agree on TTL=60)")


# ── 3. Edit row 1 to TTL=900 (rrset must follow) ─────────────────────
print("\n=== 3. Edit `web A 1.1.1.1` TTL → 900 ===")
out = r(c, f"curl -s {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H}", show=False)
data = json.loads(out).get("data", [])
row1 = next((d for d in data if d.get("name") == "web" and d.get("value") == "1.1.1.1"), None)
r(c, f"curl -s -X PUT {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records/{row1['id']} {H} "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"type\":\"A\",\"name\":\"web\",\"value\":\"1.1.1.1\",\"ttl\":900}}' "
     "-w '\\nHTTP=%{http_code}\\n'")
show_ttls("After edit (both rows + pdns must show TTL=900)")


# ── 4. Seed mismatched Mongo state (simulate pre-3.0.13 corruption) ──
print("\n=== 4. Inject TTL mismatch directly in Mongo (legacy state) ===")
mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
         'db.dns_records.updateOne({zone_id: z._id, type:"A", name:"web", value:"1.1.1.1"}, '
         '{$set: {ttl: 7200}}); '
         'db.dns_records.updateOne({zone_id: z._id, type:"A", name:"web", value:"2.2.2.2"}, '
         '{$set: {ttl: 30}});', show=False)
show_ttls("Pre-reconcile mismatch (Mongo: 7200 + 30; pdns still serves whatever it had)")

print("\n   Trigger reconcile by editing one row (TTL=120) — both must unify to 120:")
r(c, f"curl -s -X PUT {BACKEND}/api/v1/whm/dns/zones/{ZONE}/records/{row1['id']} {H} "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"type\":\"A\",\"name\":\"web\",\"value\":\"1.1.1.1\",\"ttl\":120}}' "
     "-w '\\nHTTP=%{http_code}\\n'")
show_ttls("Post-reconcile (both Mongo rows + pdns must show TTL=120)")


# Cleanup
print("\n=== Cleanup ===")
r(c, f"curl -s -X DELETE {BACKEND}/api/v1/whm/dns/zones/{ZONE} {H}", show=False)
r(c, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
mongo(c, f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
         'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); } '
         f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
         f'db.users.updateOne({{email:"{admin["email"]}"}}, {{$unset:{{refresh_token:"", refresh_expires_at:""}}}}); '
         f'db.otp_requests.deleteMany({{email:"{admin["email"]}"}});', show=False)
r(c, "rm -f /tmp/t.jar", show=False)
c.close()
