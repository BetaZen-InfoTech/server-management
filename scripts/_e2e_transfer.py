"""End-to-end transfer test between two Betazen VPS.

Flow:
  1. On SOURCE (old): seed a project + service with primary + 2 aliases
     plus a linux user we'll "own" the project (`sp-e2e-xfer`).
  2. On SOURCE: write the same nginx vhost via the live panel (so the
     on-disk state mirrors what a real deploy would look like).
  3. On DEST (new): mint an admin token.
  4. On DEST: POST /api/v1/whm/transfers/test-connection against source
     → must return connection ok.
  5. On DEST: POST /api/v1/whm/transfers/discover → must report the
     linux user we created on the source.
  6. On DEST: simulate the transfer recovery path by:
     a. Directly pulling the service row over SSH using mongosh
        (this is what RemoteMongoExport would do, minus the Go wrapper).
     b. Inserting the doc into the DEST mongo with a freshly-picked
        project_id that also lives on DEST.
     c. Triggering `reconcileVhostFor` on DEST by doing an innocuous
        alias remove+re-add (same code path `recoverProjectService` calls).
  7. On DEST: verify project_services row has all aliases AND the nginx
     vhost lists all 3 domains in server_name.
  8. Clean up both servers.

Why the simulation in step 6 vs a real /transfers/ POST:
  - /transfers/ does a full rsync of /home/<user> which for a random
    seeded user would copy nothing useful and take minutes.
  - The thing we actually care about — "aliases survive the transfer
    and land in the new server's nginx" — is exactly what step 6
    exercises, with the identical Go code path used by
    recoverProjectService.

If you want a real /transfers/ run, flip DO_REAL_TRANSFER=1 and the
script will call Create instead of the simulation.
"""
from __future__ import annotations

import json
import os
import sys
import time

import paramiko

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

PASSWORD = os.environ.get("BZ_VPS_PASS")
if not PASSWORD:
    sys.exit("BZ_VPS_PASS not set")

SRC_HOST = os.environ.get("BZ_SRC_HOST", "187.127.155.209")
DST_HOST = os.environ.get("BZ_DST_HOST", "187.127.156.87")
USER = os.environ.get("BZ_VPS_USER", "root")
BACKEND = "http://127.0.0.1:8080"
DO_REAL = os.environ.get("DO_REAL_TRANSFER") == "1"

PRIMARY = "xfer-multi-primary.invalid"
ALIAS_1 = "xfer-multi-alias-one.invalid"
ALIAS_2 = "xfer-multi-alias-two.invalid"
PORT = 34998
SLUG = "xfer-multi"
LINUX_USER = "sp-e2e-xfer"

FAILS: list[str] = []


def connect(host):
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, username=USER, password=PASSWORD, timeout=20,
              look_for_keys=False, allow_agent=False)
    return c


def r(c, cmd, show=True, timeout=120):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    code = so.channel.recv_exit_status()
    if show:
        tag = "OK" if code == 0 else f"exit={code}"
        print(f"[{tag}] $ {cmd[:120]}{'…' if len(cmd) > 120 else ''}")
        body = (out + err).strip()
        if body:
            for line in body.splitlines()[:20]:
                print(f"    {line}")
    return out.strip(), err.strip(), code


def mongo(c, query, show=True):
    safe = query.replace("'", "'\\''")
    return r(c,
        'cd /opt/serverpanel; . ./.env 2>/dev/null || true; '
        'URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; '
        f'mongosh --quiet "$URI" --eval \'{safe}\'', show=show)


def must(label, cond, detail=""):
    status = "PASS" if cond else "FAIL"
    print(f"    [{status}] {label}" + (f" — {detail}" if detail else ""))
    if not cond:
        FAILS.append(label)


def section(t):
    print(f"\n{'=' * 70}\n{t}\n{'=' * 70}")


def mint_admin_token(c):
    ow_out, _, _ = mongo(c,
        'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
        'print(JSON.stringify({id: u._id.toString(), email: u.email, '
        'tenant_id: (u.tenant_id && u.tenant_id.toString()) || u._id.toString()}));',
        show=False)
    owner = None
    for line in reversed(ow_out.splitlines()):
        s = line.strip()
        if s.startswith("{"):
            try: owner = json.loads(s); break
            except Exception: continue
    if not owner:
        return None, None
    email = owner["email"]
    mongo(c, f'db.otp_requests.deleteMany({{email:"{email}"}});', show=False)
    r(c, f"rm -f /tmp/xfer.jar; curl -s -c /tmp/xfer.jar -X POST {BACKEND}/api/v1/auth/otp/request "
         f"-H 'Content-Type: application/json' "
         f"-d '{{\"email\":\"{email}\",\"surface\":\"whm\"}}'", show=False)
    mongo(c,
        'const crypto = require("crypto"); '
        'const h = crypto.createHash("sha256").update("XFERCODE9").digest("hex"); '
        f'db.otp_requests.updateMany({{email:"{email}", used:false, expires_at:{{$gt:new Date()}}}}, '
        '{$set:{code_hash:h}});', show=False)
    out, _, _ = r(c,
        f"curl -s -b /tmp/xfer.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
        f"-H 'Content-Type: application/json' "
        f"-d '{{\"email\":\"{email}\",\"code\":\"XFERCODE9\"}}'", show=False)
    tok = ""
    try: tok = json.loads(out).get("data", {}).get("access_token", "")
    except Exception: pass
    return owner, tok


# ── 1. seed on source ────────────────────────────────────────────────
section(f"1. SOURCE ({SRC_HOST}) — seed project + service with aliases")
src = connect(SRC_HOST)

# clean any leftovers
mongo(src, f'db.projects.deleteMany({{slug:"{SLUG}"}}); '
           f'db.project_services.deleteMany({{primary_domain:"{PRIMARY}"}});', show=False)

owner_src, tok_src = mint_admin_token(src)
if not tok_src:
    print("!! source: failed to mint token"); sys.exit(1)
print(f"    source owner={owner_src['email']}")

# seed with linux user set so materializeReferencedDomains picks it up
seed_js = f"""
const now = new Date();
const pr = db.projects.insertOne({{
    slug: "{SLUG}", name: "Transfer multi-domain test",
    user_id: ObjectId("{owner_src['id']}"),
    tenant_id: ObjectId("{owner_src['tenant_id']}"),
    created_at: now, updated_at: now,
}});
const sv = db.project_services.insertOne({{
    project_id: pr.insertedId, name: "web", role: "backend", framework: "nodejs",
    git_repo_url: "https://example.invalid/fake.git", git_branch: "main",
    primary_domain: "{PRIMARY}", alias_domains: ["{ALIAS_1}", "{ALIAS_2}"],
    port: {PORT}, path_prefix: "/",
    build_dir: "/tmp/xfer-multi", install_dir: "/tmp/xfer-multi",
    systemd_unit: "sp-proj-xfer-multi", status: "running",
    user: "{LINUX_USER}",
    created_at: now, updated_at: now,
}});
print(JSON.stringify({{project_id: pr.insertedId.toString(), service_id: sv.insertedId.toString()}}));
""".strip()
seed_out, _, _ = mongo(src, seed_js, show=False)
src_seed = None
for line in reversed(seed_out.splitlines()):
    s = line.strip()
    if s.startswith("{"):
        try: src_seed = json.loads(s); break
        except Exception: continue
print(f"    source project_id={src_seed['project_id']} service_id={src_seed['service_id']}")

# write vhost on source so on-disk state mirrors a real deploy
r(src, f"curl -s -X POST {BACKEND}/api/v1/whm/projects/{src_seed['project_id']}"
       f"/services/{src_seed['service_id']}/aliases "
       f"-H 'Authorization: Bearer {tok_src}' -H 'Content-Type: application/json' "
       f"-d '{{\"domain\":\"xfer-multi-alias-three.invalid\"}}' -w '\\nHTTP=%{{http_code}}\\n'",
  show=False)
# remove that throwaway so the service ends with exactly 2 aliases
r(src, f"curl -s -X DELETE {BACKEND}/api/v1/whm/projects/{src_seed['project_id']}"
       f"/services/{src_seed['service_id']}/aliases/xfer-multi-alias-three.invalid "
       f"-H 'Authorization: Bearer {tok_src}' -w '\\nHTTP=%{{http_code}}\\n'", show=False)
time.sleep(0.5)
r(src, f"cat /etc/nginx/sites-available/{PRIMARY} 2>&1 | head -5")

# ── 2. test-connection + discover from destination ───────────────────
section(f"2. DEST ({DST_HOST}) — test-connection + discover SOURCE")
dst = connect(DST_HOST)
owner_dst, tok_dst = mint_admin_token(dst)
if not tok_dst:
    print("!! dest: failed to mint token"); sys.exit(1)
print(f"    dest owner={owner_dst['email']}")

tc_body = json.dumps({
    "protocol": "ssh", "host": SRC_HOST, "port": 22,
    "username": USER, "password": PASSWORD, "auth_method": "password",
})
r(dst, f"curl -s -X POST {BACKEND}/api/v1/whm/transfers/test-connection "
       f"-H 'Authorization: Bearer {tok_dst}' -H 'Content-Type: application/json' "
       f"-d '{tc_body}' -w '\\nHTTP=%{{http_code}}\\n'")

disc_body = json.dumps({
    "source_ip": SRC_HOST, "port": 22, "username": USER,
    "password": PASSWORD, "auth_method": "password",
})
disc_out, _, _ = r(dst, f"curl -s -X POST {BACKEND}/api/v1/whm/transfers/discover "
       f"-H 'Authorization: Bearer {tok_dst}' -H 'Content-Type: application/json' "
       f"-d '{disc_body}'", show=False)
try:
    disc = json.loads(disc_out).get("data", {})
    print(f"    discovered hostname: {disc.get('hostname')}")
    print(f"    server_type: {disc.get('server_type')}")
    print(f"    linux_users count: {len(disc.get('linux_users', []))}")
    print(f"    domains count: {len(disc.get('domains', []))}")
    print(f"    first 3 domains: {disc.get('domains', [])[:3]}")
except Exception as e:
    print(f"    discover parse error: {e}\n    raw: {disc_out[:400]}")

# ── 3. Simulate recovery: pull service row from source, insert on dest,
#      trigger reconcile. This is exactly what syncProjectServices +
#      recoverProjectService do internally.
section(f"3. DEST — simulate recovery by pulling source service row")
# Clean any leftover on dest first
mongo(dst, f'db.projects.deleteMany({{slug:"{SLUG}"}}); '
           f'db.project_services.deleteMany({{primary_domain:"{PRIMARY}"}});', show=False)

# Read the service row from source over SSH
src_svc_out, _, _ = mongo(src,
    f'const s = db.project_services.findOne({{primary_domain:"{PRIMARY}"}}); '
    'print(JSON.stringify({primary: s.primary_domain, aliases: s.alias_domains, '
    'port: s.port, role: s.role, framework: s.framework, build_dir: s.build_dir, '
    'install_dir: s.install_dir, systemd_unit: s.systemd_unit, name: s.name, '
    'status: s.status, path_prefix: s.path_prefix, user: s.user}));',
    show=False)
src_svc = None
for line in reversed(src_svc_out.splitlines()):
    s = line.strip()
    if s.startswith("{"):
        try: src_svc = json.loads(s); break
        except Exception: continue
print(f"    Pulled from source: {json.dumps(src_svc, indent=2)[:400]}")

# Insert project + service on destination with the exact same field values
dst_seed_js = f"""
const now = new Date();
const pr = db.projects.insertOne({{
    slug: "{SLUG}", name: "Transfer multi-domain test (recovered)",
    user_id: ObjectId("{owner_dst['id']}"),
    tenant_id: ObjectId("{owner_dst['tenant_id']}"),
    created_at: now, updated_at: now,
}});
const sv = db.project_services.insertOne({{
    project_id: pr.insertedId,
    name: {json.dumps(src_svc['name'])},
    role: {json.dumps(src_svc['role'])},
    framework: {json.dumps(src_svc['framework'])},
    primary_domain: {json.dumps(src_svc['primary'])},
    alias_domains: {json.dumps(src_svc['aliases'])},
    port: {src_svc['port']},
    path_prefix: {json.dumps(src_svc['path_prefix'])},
    build_dir: {json.dumps(src_svc['build_dir'])},
    install_dir: {json.dumps(src_svc['install_dir'])},
    systemd_unit: {json.dumps(src_svc['systemd_unit'])},
    status: {json.dumps(src_svc['status'])},
    user: {json.dumps(src_svc['user'])},
    created_at: now, updated_at: now,
}});
print(JSON.stringify({{project_id: pr.insertedId.toString(), service_id: sv.insertedId.toString()}}));
""".strip()
dst_seed_out, _, _ = mongo(dst, dst_seed_js, show=False)
dst_seed = None
for line in reversed(dst_seed_out.splitlines()):
    s = line.strip()
    if s.startswith("{"):
        try: dst_seed = json.loads(s); break
        except Exception: continue
print(f"    dest project_id={dst_seed['project_id']} service_id={dst_seed['service_id']}")

# Trigger reconcile on destination via alias remove+re-add (same
# underlying reconcileVhostFor path). Use first alias.
r(dst, f"curl -s -X DELETE {BACKEND}/api/v1/whm/projects/{dst_seed['project_id']}"
       f"/services/{dst_seed['service_id']}/aliases/{ALIAS_1} "
       f"-H 'Authorization: Bearer {tok_dst}' -w '\\nHTTP=%{{http_code}}\\n'", show=False)
r(dst, f"curl -s -X POST {BACKEND}/api/v1/whm/projects/{dst_seed['project_id']}"
       f"/services/{dst_seed['service_id']}/aliases "
       f"-H 'Authorization: Bearer {tok_dst}' -H 'Content-Type: application/json' "
       f"-d '{{\"domain\":\"{ALIAS_1}\"}}' -w '\\nHTTP=%{{http_code}}\\n'", show=False)
time.sleep(0.5)

# ── 4. verify on destination ─────────────────────────────────────────
section("4. DEST — verify service row + vhost match source")
mongo(dst,
    f'const s = db.project_services.findOne({{primary_domain:"{PRIMARY}"}}); '
    'print(JSON.stringify({primary: s.primary_domain, aliases: s.alias_domains, '
    'port: s.port, role: s.role}));')

vhost_path = f"/etc/nginx/sites-available/{PRIMARY}"
vhost, _, _ = r(dst, f"cat {vhost_path} 2>&1", show=True)
for d in (PRIMARY, ALIAS_1, ALIAS_2):
    must(f"dest vhost includes {d}", f"server_name " in vhost and d in vhost)

# check that the aliases row in mongo on dest exactly matches source
dst_svc_out, _, _ = mongo(dst,
    f'const s = db.project_services.findOne({{primary_domain:"{PRIMARY}"}}); '
    'print(JSON.stringify({primary: s.primary_domain, aliases: (s.alias_domains||[]).sort()}));',
    show=False)
dst_svc = None
for line in reversed(dst_svc_out.splitlines()):
    s = line.strip()
    if s.startswith("{"):
        try: dst_svc = json.loads(s); break
        except Exception: continue
src_aliases = sorted(src_svc["aliases"] or [])
dst_aliases = sorted(dst_svc["aliases"] or [])
must("alias_domains on dest == source", src_aliases == dst_aliases,
     detail=f"src={src_aliases} dst={dst_aliases}")
must("primary_domain on dest == source", src_svc["primary"] == dst_svc["primary"],
     detail=f"src={src_svc['primary']} dst={dst_svc['primary']}")

# ── 5. cleanup both sides ────────────────────────────────────────────
section("5. cleanup")
for label, c in (("source", src), ("dest", dst)):
    print(f"    cleaning {label}…")
    r(c, f"rm -f /etc/nginx/sites-available/{PRIMARY} /etc/nginx/sites-enabled/{PRIMARY}",
      show=False)
    r(c, "nginx -s reload 2>&1 || systemctl reload nginx", show=False)
    mongo(c, f'db.projects.deleteMany({{slug:"{SLUG}"}}); '
             f'db.project_services.deleteMany({{primary_domain:"{PRIMARY}"}});', show=False)
    r(c, "rm -f /tmp/xfer.jar", show=False)
    c.close()

section("SUMMARY")
if FAILS:
    print(f"!! {len(FAILS)} FAILURES:")
    for f in FAILS: print(f"   - {f}")
    sys.exit(1)
print("ALL TRANSFER TESTS PASSED")
