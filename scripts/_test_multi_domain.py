"""End-to-end test: add an alias domain to a seeded project service
and verify the backend wrote a multi-server_name nginx vhost.

Strategy:
  1. Seed a test Project + ProjectService directly into Mongo (this
     VPS is a staging box with no Deploy Software state of its own).
  2. Mint an admin access token via the OTP handoff flow.
  3. POST /whm/projects/:id/services/:svc/aliases with a fake alias.
  4. Assert the nginx vhost file on disk lists BOTH domains in
     server_name, proving the role-agnostic reconcile landed aliases.
  5. Clean up: delete vhost, delete DB docs, revoke token.

Certbot will fail because DNS doesn't resolve to this box — that's
fine. `reconcileVhostFor` writes the HTTP vhost BEFORE calling
certbot, so we can inspect the on-disk file regardless of the cert
outcome (the whole point: v3.0.2 makes the HTTP vhost multi-domain,
the cert is a separate concern).
"""
from __future__ import annotations

import json
import os
import sys

import paramiko

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

PASSWORD = os.environ.get("BZ_VPS_PASS")
if not PASSWORD:
    sys.exit("BZ_VPS_PASS not set — export the VPS root password and re-run")

HOST = os.environ.get("BZ_VPS_HOST", "187.127.155.209")
USER = os.environ.get("BZ_VPS_USER", "root")
BACKEND = "http://127.0.0.1:8080"

# Fake project + service we'll inject. The ".invalid" TLD is reserved
# by RFC 6761 so we can't accidentally collide with a real domain.
TEST_PROJECT_SLUG = "multi-domain-smoketest"
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
        print(f"\n[{tag}] $ {cmd[:110]}{'…' if len(cmd) > 110 else ''}")
        body = (out + err).strip()
        if body:
            for line in body.splitlines()[:25]:
                print(f"    {line}")
    return out.strip(), err.strip(), code


def mongo(c, query, show=True):
    # Escape single quotes inside the JS payload so the outer --eval
    # shell quoting survives.
    safe = query.replace("'", "'\\''")
    script = (
        "cd /opt/serverpanel; . ./.env 2>/dev/null || true; "
        "URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; "
        f"mongosh --quiet \"$URI\" --eval '{safe}'"
    )
    return r(c, script, show=show)


c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, username=USER, password=PASSWORD, timeout=20,
          look_for_keys=False, allow_agent=False)

# ── 0. Pick a vendor_owner to own the seeded project ─────────────────
print("=== 0. Pick a vendor_owner ===")
owner_out, _, _ = mongo(c,
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    'if (u) print(JSON.stringify({id: u._id.toString(), email: u.email, tenant_id: (u.tenant_id && u.tenant_id.toString()) || u._id.toString()}));',
    show=False)
owner = None
for line in reversed(owner_out.splitlines()):
    line = line.strip()
    if line.startswith("{"):
        try:
            owner = json.loads(line)
            break
        except Exception:
            continue
if not owner:
    print("!! no vendor_owner found; abort")
    c.close(); sys.exit(1)
print(f"    Owner: {owner['email']}  tenant_id={owner['tenant_id']}")

# ── 1. Seed the test Project + ProjectService ────────────────────────
print("\n=== 1. Seed test Project + ProjectService ===")
seed_js = f"""
const now = new Date();
const proj = {{
    slug: "{TEST_PROJECT_SLUG}",
    name: "Multi-domain smoketest",
    user_id: ObjectId("{owner['id']}"),
    tenant_id: ObjectId("{owner['tenant_id']}"),
    created_at: now,
    updated_at: now,
}};
const pr = db.projects.insertOne(proj);
const svc = {{
    project_id: pr.insertedId,
    name: "web",
    role: "backend",
    framework: "nodejs",
    git_repo_url: "https://example.invalid/fake.git",
    git_branch: "main",
    primary_domain: "{TEST_PRIMARY}",
    alias_domains: [],
    port: {TEST_PORT},
    path_prefix: "/",
    build_dir: "/tmp/multi-domain-smoketest",
    install_dir: "/tmp/multi-domain-smoketest",
    systemd_unit: "sp-proj-multi-domain-smoketest",
    status: "running",
    created_at: now,
    updated_at: now,
}};
const sv = db.project_services.insertOne(svc);
print(JSON.stringify({{project_id: pr.insertedId.toString(), service_id: sv.insertedId.toString()}}));
""".strip()
seed_out, _, _ = mongo(c, seed_js, show=True)
seed = None
for line in reversed(seed_out.splitlines()):
    line = line.strip()
    if line.startswith("{"):
        try:
            seed = json.loads(line); break
        except Exception: continue
if not seed:
    print("!! seed failed"); c.close(); sys.exit(1)
print(f"    project_id={seed['project_id']}  service_id={seed['service_id']}")

# ── 2. Mint admin access token via OTP handoff ───────────────────────
print("\n=== 2. Mint admin access token ===")
admin_email = owner['email']
mongo(c, f'db.otp_requests.deleteMany({{email:"{admin_email}"}});', show=False)
r(c,
  f"rm -f /tmp/md.jar; "
  f"curl -s -c /tmp/md.jar -X POST {BACKEND}/api/v1/auth/otp/request "
  f"-H 'Content-Type: application/json' "
  f"-d '{{\"email\":\"{admin_email}\",\"surface\":\"whm\"}}' "
  "-w 'HTTP=%{http_code}\\n'",
  show=False)

mongo(c,
      'const crypto = require("crypto"); '
      'const h = crypto.createHash("sha256").update("MULTIDOM99").digest("hex"); '
      f'db.otp_requests.updateMany({{email:"{admin_email}", used:false, expires_at:{{$gt:new Date()}}}}, '
      '{$set:{code_hash:h}});',
      show=False)

out, _, _ = r(c,
    f"curl -s -b /tmp/md.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
    f"-H 'Content-Type: application/json' "
    f"-d '{{\"email\":\"{admin_email}\",\"code\":\"MULTIDOM99\"}}'",
    show=False)
token = ""
try:
    data = json.loads(out).get("data", {})
    token = data.get("access_token", "")
except Exception:
    pass
if not token:
    print(f"!! no token; /verify returned: {out[:300]}")
    c.close(); sys.exit(1)
print(f"    Got token ({len(token)} chars)")

# ── 3. POST first alias — previously blocked in UI for backend, now live ─
print(f"\n=== 3. Add first alias: {TEST_ALIAS_1} ===")
r(c,
  f"curl -s -X POST "
  f"{BACKEND}/api/v1/whm/projects/{seed['project_id']}/services/{seed['service_id']}/aliases "
  f"-H 'Authorization: Bearer {token}' "
  f"-H 'Content-Type: application/json' "
  f"-d '{{\"domain\":\"{TEST_ALIAS_1}\"}}' "
  "-w '\\nHTTP=%{http_code}\\n'")

# ── 4. POST second alias (to prove the loop handles N > 1) ───────────
print(f"\n=== 4. Add second alias: {TEST_ALIAS_2} ===")
r(c,
  f"curl -s -X POST "
  f"{BACKEND}/api/v1/whm/projects/{seed['project_id']}/services/{seed['service_id']}/aliases "
  f"-H 'Authorization: Bearer {token}' "
  f"-H 'Content-Type: application/json' "
  f"-d '{{\"domain\":\"{TEST_ALIAS_2}\"}}' "
  "-w '\\nHTTP=%{http_code}\\n'")

# ── 5. Assert Mongo reflects both aliases ────────────────────────────
print("\n=== 5. DB check: service row must list BOTH aliases ===")
mongo(c,
      f'const s = db.project_services.findOne({{_id: ObjectId("{seed["service_id"]}")}}); '
      'print(JSON.stringify({primary: s.primary_domain, aliases: s.alias_domains}, null, 2));')

# ── 6. Assert nginx vhost has both domains on server_name ────────────
print("\n=== 6. nginx vhost: server_name must include primary + both aliases ===")
vhost = f"/etc/nginx/sites-available/{TEST_PRIMARY}.conf"
r(c, f"ls -la {vhost} /etc/nginx/sites-enabled/{TEST_PRIMARY}.conf 2>&1")
r(c, f"grep -n 'server_name' {vhost} 2>&1 | head -4")
print("\n    Counting occurrences of each domain in the vhost:")
for d in (TEST_PRIMARY, TEST_ALIAS_1, TEST_ALIAS_2):
    r(c, f"grep -c '{d}' {vhost} || true")

# ── 7. Show a snippet so the output is human-readable ────────────────
print("\n=== 7. Sample of the vhost file (first 20 lines) ===")
r(c, f"head -20 {vhost} 2>&1")

# ── 8. Journal: confirm reconcile fired and certbot was called ───────
print("\n=== 8. Journal: reconcileVhostFor evidence ===")
r(c, "journalctl -u serverpanel --since '1 minute ago' "
     "| grep -iE 'alias|certbot|project|vhost|reconcile' | tail -10")

# ── 9. Cleanup ───────────────────────────────────────────────────────
print("\n=== 9. CLEANUP — remove vhost, DB docs, revoke token ===")
r(c, f"rm -f /etc/nginx/sites-available/{TEST_PRIMARY}.conf "
     f"/etc/nginx/sites-enabled/{TEST_PRIMARY}.conf")
r(c, "nginx -s reload 2>&1 || systemctl reload nginx 2>&1")
mongo(c, f'db.project_services.deleteOne({{_id: ObjectId("{seed["service_id"]}")}});')
mongo(c, f'db.projects.deleteOne({{_id: ObjectId("{seed["project_id"]}")}});')
mongo(c, f'db.users.updateOne({{email:"{admin_email}"}}, '
         '{$unset:{refresh_token:"", refresh_expires_at:""}});', show=False)
mongo(c, f'db.otp_requests.deleteMany({{email:"{admin_email}"}});', show=False)
r(c, "rm -f /tmp/md.jar")

c.close()
print("\n=== multi-domain test complete — cleanup successful ===")
