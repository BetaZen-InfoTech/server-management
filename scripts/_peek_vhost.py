"""Rerun the multi-domain test but also check the correct vhost path."""
import json, os, sys, paramiko

try: sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception: pass

PASSWORD = os.environ.get("BZ_VPS_PASS")
if not PASSWORD: sys.exit("BZ_VPS_PASS not set")

HOST = os.environ.get("BZ_VPS_HOST", "187.127.155.209")
USER = os.environ.get("BZ_VPS_USER", "root")
BACKEND = "http://127.0.0.1:8080"

TEST_PRIMARY = "multi-domain-smoketest-primary.invalid"
TEST_ALIAS_1 = "multi-domain-smoketest-alias-one.invalid"
TEST_ALIAS_2 = "multi-domain-smoketest-alias-two.invalid"
TEST_PORT = 34999


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
            for line in body.splitlines()[:40]:
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

# Seed + owner (same as before)
owner_out, _, _ = mongo(c,
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    'print(JSON.stringify({id: u._id.toString(), email: u.email, '
    'tenant_id: (u.tenant_id && u.tenant_id.toString()) || u._id.toString()}));',
    show=False)
owner = None
for line in reversed(owner_out.splitlines()):
    s = line.strip()
    if s.startswith("{"):
        try: owner = json.loads(s); break
        except: continue

mongo(c, f'db.projects.deleteMany({{slug:"multi-domain-smoketest"}}); '
         f'db.project_services.deleteMany({{primary_domain:"{TEST_PRIMARY}"}});',
      show=False)

seed_js = f"""
const now = new Date();
const pr = db.projects.insertOne({{
    slug: "multi-domain-smoketest", name: "Multi-domain smoketest",
    user_id: ObjectId("{owner['id']}"), tenant_id: ObjectId("{owner['tenant_id']}"),
    created_at: now, updated_at: now,
}});
const sv = db.project_services.insertOne({{
    project_id: pr.insertedId, name: "web", role: "backend", framework: "nodejs",
    git_repo_url: "https://example.invalid/fake.git", git_branch: "main",
    primary_domain: "{TEST_PRIMARY}", alias_domains: [],
    port: {TEST_PORT}, path_prefix: "/",
    build_dir: "/tmp/multi-domain-smoketest", install_dir: "/tmp/multi-domain-smoketest",
    systemd_unit: "sp-proj-multi-domain-smoketest", status: "running",
    created_at: now, updated_at: now,
}});
print(JSON.stringify({{project_id: pr.insertedId.toString(), service_id: sv.insertedId.toString()}}));
""".strip()
seed_out, _, _ = mongo(c, seed_js, show=False)
seed = None
for line in reversed(seed_out.splitlines()):
    s = line.strip()
    if s.startswith("{"):
        try: seed = json.loads(s); break
        except: continue
print(f"Seeded project_id={seed['project_id']}  service_id={seed['service_id']}")

# Token mint
admin_email = owner['email']
mongo(c, f'db.otp_requests.deleteMany({{email:"{admin_email}"}});', show=False)
r(c, f"rm -f /tmp/md.jar; curl -s -c /tmp/md.jar -X POST {BACKEND}/api/v1/auth/otp/request "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"email\":\"{admin_email}\",\"surface\":\"whm\"}}'",
  show=False)
mongo(c,
      'const crypto = require("crypto"); '
      'const h = crypto.createHash("sha256").update("MULTIDOM99").digest("hex"); '
      f'db.otp_requests.updateMany({{email:"{admin_email}", used:false, expires_at:{{$gt:new Date()}}}}, '
      '{$set:{code_hash:h}});', show=False)
out, _, _ = r(c,
    f"curl -s -b /tmp/md.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
    f"-H 'Content-Type: application/json' "
    f"-d '{{\"email\":\"{admin_email}\",\"code\":\"MULTIDOM99\"}}'",
    show=False)
token = json.loads(out).get("data", {}).get("access_token", "")

# Hit both aliases
for alias in (TEST_ALIAS_1, TEST_ALIAS_2):
    r(c, f"curl -s -X POST "
         f"{BACKEND}/api/v1/whm/projects/{seed['project_id']}/services/{seed['service_id']}/aliases "
         f"-H 'Authorization: Bearer {token}' -H 'Content-Type: application/json' "
         f"-d '{{\"domain\":\"{alias}\"}}' -w '\\nHTTP=%{{http_code}}\\n'",
      show=False)

# ─── THE REAL VHOST FILE CHECK ───
# Vhost filenames have NO .conf extension in this panel (see nginx.go:155).
print("\n=== vhost file presence (no .conf extension) ===")
r(c, f"ls -la /etc/nginx/sites-available/ | grep multi-domain-smoketest")
r(c, f"ls -la /etc/nginx/sites-enabled/ | grep multi-domain-smoketest")

print("\n=== Full vhost contents ===")
r(c, f"cat /etc/nginx/sites-available/{TEST_PRIMARY} 2>&1")

print("\n=== Assert server_name lists all three ===")
for d in (TEST_PRIMARY, TEST_ALIAS_1, TEST_ALIAS_2):
    r(c, f"grep -c '{d}' /etc/nginx/sites-available/{TEST_PRIMARY} 2>&1 || true")

# Cleanup
print("\n=== cleanup ===")
r(c, f"rm -f /etc/nginx/sites-available/{TEST_PRIMARY} /etc/nginx/sites-enabled/{TEST_PRIMARY}")
r(c, "nginx -s reload 2>&1 || systemctl reload nginx")
mongo(c, f'db.project_services.deleteOne({{_id: ObjectId("{seed["service_id"]}")}}); '
         f'db.projects.deleteOne({{_id: ObjectId("{seed["project_id"]}")}}); '
         f'db.users.updateOne({{email:"{admin_email}"}}, {{$unset:{{refresh_token:"", refresh_expires_at:""}}}}); '
         f'db.otp_requests.deleteMany({{email:"{admin_email}"}});', show=False)
r(c, "rm -f /tmp/md.jar")
c.close()
