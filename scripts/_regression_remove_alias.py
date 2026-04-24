"""Regression test for the v3.0.3 RemoveAlias bug.

Scenario: add alias A, B, C → DELETE alias B → vhost server_name must
list only [primary, A, C]. Before v3.0.3, B stayed in server_name
because reconcileVhostFor walked the caller's own DB row (still
carrying B) and unioned it back in.

Asserts both the Mongo state AND the on-disk vhost.
"""
from __future__ import annotations
import json, os, sys, time, paramiko

try: sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception: pass

PASSWORD = os.environ.get("BZ_VPS_PASS")
if not PASSWORD: sys.exit("BZ_VPS_PASS not set")
HOST = os.environ.get("BZ_VPS_HOST", "187.127.155.209")
USER = os.environ.get("BZ_VPS_USER", "root")
BACKEND = "http://127.0.0.1:8080"

PRIMARY = "regr-rma-primary.invalid"
A = "regr-rma-alias-a.invalid"
B = "regr-rma-alias-b.invalid"
C = "regr-rma-alias-c.invalid"
PORT = 34988
SLUG = "regr-rma"
FAILS: list[str] = []


def r(c, cmd, show=True, timeout=60):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    code = so.channel.recv_exit_status()
    if show:
        print(f"[{'OK' if code == 0 else f'exit={code}'}] $ {cmd[:120]}")
        for ln in (out + err).strip().splitlines()[:20]: print(f"    {ln}")
    return out.strip(), err.strip(), code


def mongo(c, q, show=True):
    safe = q.replace("'", "'\\''")
    return r(c, 'cd /opt/serverpanel; . ./.env 2>/dev/null || true; '
               'URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; '
               f'mongosh --quiet "$URI" --eval \'{safe}\'', show=show)


def must(label, cond, detail=""):
    tag = "PASS" if cond else "FAIL"
    print(f"    [{tag}] {label}" + (f" — {detail}" if detail else ""))
    if not cond: FAILS.append(label)


c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, username=USER, password=PASSWORD, timeout=20,
          look_for_keys=False, allow_agent=False)

print(f"=== regression test on {HOST}: RemoveAlias must drop B from server_name ===")

# 1. Clean state
mongo(c, f'db.projects.deleteMany({{slug:"{SLUG}"}}); '
         f'db.project_services.deleteMany({{primary_domain:"{PRIMARY}"}});', show=False)
r(c, f"rm -f /etc/nginx/sites-available/{PRIMARY} /etc/nginx/sites-enabled/{PRIMARY}", show=False)

# 2. Seed owner + project + service
ow_out, _, _ = mongo(c,
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    'print(JSON.stringify({id:u._id.toString(),email:u.email,'
    'tenant_id:(u.tenant_id&&u.tenant_id.toString())||u._id.toString()}));', show=False)
owner = None
for ln in reversed(ow_out.splitlines()):
    s = ln.strip()
    if s.startswith("{"):
        try: owner = json.loads(s); break
        except Exception: continue
if not owner: print("!! no owner"); sys.exit(1)

seed_js = f"""
const now = new Date();
const pr = db.projects.insertOne({{slug:"{SLUG}",name:"regr rma",
  user_id: ObjectId("{owner['id']}"), tenant_id: ObjectId("{owner['tenant_id']}"),
  created_at: now, updated_at: now}});
const sv = db.project_services.insertOne({{project_id: pr.insertedId,
  name:"web", role:"backend", framework:"nodejs",
  primary_domain:"{PRIMARY}", alias_domains:[],
  port:{PORT}, path_prefix:"/", build_dir:"/tmp/regr-rma",
  install_dir:"/tmp/regr-rma", systemd_unit:"sp-proj-regr-rma",
  status:"running", created_at: now, updated_at: now}});
print(JSON.stringify({{project_id:pr.insertedId.toString(), service_id:sv.insertedId.toString()}}));
""".strip()
seed_out, _, _ = mongo(c, seed_js, show=False)
seed = None
for ln in reversed(seed_out.splitlines()):
    s = ln.strip()
    if s.startswith("{"):
        try: seed = json.loads(s); break
        except Exception: continue
print(f"seeded project={seed['project_id']} svc={seed['service_id']}")

# 3. Mint admin token
mongo(c, f'db.otp_requests.deleteMany({{email:"{owner["email"]}"}});', show=False)
r(c, f"rm -f /tmp/reg.jar; curl -s -c /tmp/reg.jar -X POST {BACKEND}/api/v1/auth/otp/request "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"email\":\"{owner['email']}\",\"surface\":\"whm\"}}'", show=False)
mongo(c, 'const crypto = require("crypto"); '
         'const h = crypto.createHash("sha256").update("REGRRMA99").digest("hex"); '
         f'db.otp_requests.updateMany({{email:"{owner["email"]}", used:false, expires_at:{{$gt:new Date()}}}}, '
         '{$set:{code_hash:h}});', show=False)
out, _, _ = r(c, f"curl -s -b /tmp/reg.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
                f"-H 'Content-Type: application/json' "
                f"-d '{{\"email\":\"{owner['email']}\",\"code\":\"REGRRMA99\"}}'", show=False)
tok = ""
try: tok = json.loads(out).get("data", {}).get("access_token", "")
except Exception: pass
if not tok: print("!! no token"); sys.exit(1)

# 4. Add A, B, C
for d in (A, B, C):
    r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/projects/{seed['project_id']}"
         f"/services/{seed['service_id']}/aliases "
         f"-H 'Authorization: Bearer {tok}' -H 'Content-Type: application/json' "
         f"-d '{{\"domain\":\"{d}\"}}' -w '\\nHTTP=%{{http_code}}\\n'", show=False)
time.sleep(0.3)

vhost_path = f"/etc/nginx/sites-available/{PRIMARY}"
before, _, _ = r(c, f"grep 'server_name' {vhost_path}", show=True)
for d in (PRIMARY, A, B, C):
    must(f"before-delete: {d} in server_name", d in before)

# 5. DELETE B
print("\n--- DELETE alias B ---")
r(c, f"curl -s -X DELETE {BACKEND}/api/v1/whm/projects/{seed['project_id']}"
     f"/services/{seed['service_id']}/aliases/{B} "
     f"-H 'Authorization: Bearer {tok}' -w '\\nHTTP=%{{http_code}}\\n'")
time.sleep(0.3)

# 6. ASSERTIONS
print("\n--- post-delete state ---")
# Mongo
mg_out, _, _ = mongo(c,
    f'const s = db.project_services.findOne({{_id: ObjectId("{seed["service_id"]}")}}); '
    'print(JSON.stringify({aliases:(s.alias_domains||[]).sort()}));', show=False)
mg = None
for ln in reversed(mg_out.splitlines()):
    s = ln.strip()
    if s.startswith("{"):
        try: mg = json.loads(s); break
        except Exception: continue
must("mongo: alias_domains does NOT contain B", B not in (mg["aliases"] if mg else []),
     detail=f"aliases={mg['aliases'] if mg else 'None'}")
must("mongo: alias_domains contains A and C",
     A in (mg["aliases"] if mg else []) and C in (mg["aliases"] if mg else []),
     detail=f"aliases={mg['aliases'] if mg else 'None'}")

# Vhost
after, _, _ = r(c, f"grep 'server_name' {vhost_path}", show=True)
must("vhost: primary still in server_name", PRIMARY in after)
must("vhost: alias A still in server_name", A in after)
must("vhost: alias C still in server_name", C in after)
must("vhost: alias B NOT in server_name (THIS IS THE FIX)",
     B not in after, detail=f"server_name line: {after.strip()}")

# Cleanup
print("\n--- cleanup ---")
r(c, f"rm -f /etc/nginx/sites-available/{PRIMARY} /etc/nginx/sites-enabled/{PRIMARY}", show=False)
r(c, "nginx -s reload 2>&1 || systemctl reload nginx", show=False)
mongo(c, f'db.project_services.deleteOne({{_id: ObjectId("{seed["service_id"]}")}}); '
         f'db.projects.deleteOne({{_id: ObjectId("{seed["project_id"]}")}}); '
         f'db.users.updateOne({{email:"{owner["email"]}"}}, {{$unset:{{refresh_token:"", refresh_expires_at:""}}}}); '
         f'db.otp_requests.deleteMany({{email:"{owner["email"]}"}});', show=False)
r(c, "rm -f /tmp/reg.jar", show=False)
c.close()

print("\n=== summary ===")
if FAILS:
    print(f"!! {len(FAILS)} FAILURES:")
    for f in FAILS: print(f"   - {f}")
    sys.exit(1)
print("REGRESSION TEST PASSED — RemoveAlias now correctly drops B")
