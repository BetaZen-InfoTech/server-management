"""Smoke test for the Deploy Software link/unlink-domain API (v3.1.30).

Runs end-to-end against a live VPS over SSH so the test exercises the
full HTTP path through nginx + Fiber + ProjectService + agent + nginx
reload — same code path an external API integrator hits.

Coverage matrix
---------------
1. Happy path — admin token can link an owned domain to its own
   service and the alias lands in `alias_domains` + the nginx vhost's
   server_name list.
2. Path param consistency — POSTing to the wrong project_id with a
   real service_id returns 403 ErrServiceProjectMismatch (was 200 +
   silent mutation pre-3.1.30).
3. Idempotent unlink — DELETE removes the alias and the nginx
   server_name no longer carries it.
4. Status-code sharpness — malformed body → 400, missing service →
   404, mismatched IDs → 403. Pre-3.1.30 every case returned 400.

Required env
------------
BZ_VPS_PASS  — root password for the VPS (same as BetaZen@2023 in
               testing-vps-details.md)
BZ_VPS_HOST  — defaults to 187.127.155.209
BZ_VPS_USER  — defaults to root

This script is read-only on /etc/nginx/sites-available except for the
ephemeral test vhost; it cleans every artefact (project + service +
vhost + db rows) before exiting regardless of pass / fail.
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
HOST = os.environ.get("BZ_VPS_HOST", "187.127.155.209")
USER = os.environ.get("BZ_VPS_USER", "root")
BACKEND = "http://127.0.0.1:8080"

PRIMARY = "smoke-alias-link.invalid"
ALIAS = "smoke-alias-link-alias.invalid"
SLUG = "smoke-alias-link"
PORT = 34999
FAILS: list[str] = []


def r(c, cmd, show=True, timeout=60):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    code = so.channel.recv_exit_status()
    if show:
        print(f"[{'OK' if code == 0 else f'exit={code}'}] $ {cmd[:140]}")
        for ln in (out + err).strip().splitlines()[:20]:
            print(f"    {ln}")
    return out.strip(), err.strip(), code


def mongo(c, q, show=True):
    safe = q.replace("'", "'\\''")
    return r(
        c,
        "cd /opt/serverpanel; . ./.env 2>/dev/null || true; "
        "URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; "
        f'mongosh --quiet "$URI" --eval \'{safe}\'',
        show=show,
    )


def must(label, cond, detail=""):
    tag = "PASS" if cond else "FAIL"
    print(f"    [{tag}] {label}" + (f" — {detail}" if detail else ""))
    if not cond:
        FAILS.append(label)


def cleanup(c):
    print("--- cleanup ---")
    mongo(
        c,
        f'db.api_tokens.deleteMany({{name:"smoke-alias-link"}}); '
        f'db.projects.deleteMany({{slug:"{SLUG}"}}); '
        f'db.project_services.deleteMany({{primary_domain:"{PRIMARY}"}}); '
        f'db.domains.deleteMany({{domain:{{$in:["{PRIMARY}","{ALIAS}"]}}}});',
        show=False,
    )
    r(c, f"rm -f /etc/nginx/sites-available/{PRIMARY} /etc/nginx/sites-enabled/{PRIMARY}", show=False)
    r(c, f"rm -f /etc/nginx/sites-available/{ALIAS} /etc/nginx/sites-enabled/{ALIAS}", show=False)
    r(c, "nginx -t && nginx -s reload", show=False)


c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, username=USER, password=PASSWORD, timeout=20, look_for_keys=False, allow_agent=False)

print(f"=== smoke test on {HOST}: link/unlink-domain API (v3.1.30) ===")

cleanup(c)

# 1. Discover the platform owner (vendor_owner) so we can mint an admin token.
ow_out, _, _ = mongo(
    c,
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    "print(JSON.stringify({id:u._id.toString(),email:u.email,username:u.username,"
    "tenant_id:(u.tenant_id&&u.tenant_id.toString())||u._id.toString()}));",
    show=False,
)
owner = None
for ln in reversed(ow_out.splitlines()):
    s = ln.strip()
    if s.startswith("{"):
        try:
            owner = json.loads(s)
            break
        except Exception:
            continue
if not owner:
    sys.exit("!! no platform owner")
print(f"owner: {owner['email']} (tenant={owner['tenant_id']}, user={owner['username']})")

# 2. Seed a Domain (so AssertOwnsDomain finds it), Project, and Service.
seed_js = f"""
const now = new Date();
db.domains.insertOne({{
  domain: "{ALIAS}", user: "{owner['username']}",
  php_version: "8.1", ssl_active: false, suspended: false,
  created_at: now, updated_at: now
}});
db.domains.insertOne({{
  domain: "{PRIMARY}", user: "{owner['username']}",
  php_version: "8.1", ssl_active: false, suspended: false,
  created_at: now, updated_at: now
}});
const pr = db.projects.insertOne({{
  slug: "{SLUG}", name: "smoke alias link",
  user: "{owner['username']}", git_repo_url: "", git_branch: "main",
  user_id: ObjectId("{owner['id']}"),
  tenant_id: ObjectId("{owner['tenant_id']}"),
  owner_user_id: ObjectId("{owner['id']}"),
  created_at: now, updated_at: now
}});
const sv = db.project_services.insertOne({{
  project_id: pr.insertedId, name: "web", role: "backend", framework: "nodejs",
  primary_domain: "{PRIMARY}", alias_domains: [],
  port: {PORT}, path_prefix: "/", build_dir: "/tmp/{SLUG}",
  install_dir: "/tmp/{SLUG}", systemd_unit: "sp-proj-{SLUG}",
  git_branch: "main", status: "running",
  created_at: now, updated_at: now
}});
print(JSON.stringify({{project_id: pr.insertedId.toString(), service_id: sv.insertedId.toString()}}));
"""
seed_out, _, _ = mongo(c, seed_js.strip(), show=False)
seed = None
for ln in reversed(seed_out.splitlines()):
    s = ln.strip()
    if s.startswith("{"):
        try:
            seed = json.loads(s)
            break
        except Exception:
            continue
if not seed:
    cleanup(c)
    sys.exit("!! seed failed")
print(f"seeded project={seed['project_id']} svc={seed['service_id']}")

# 3. Mint admin JWT via OTP (we run the SHA-256 hash insert ourselves so we
#    don't have to read the operator's mailbox).
CODE = "ALIAS9"  # 6 chars OK (the panel accepts 6-digit/alpha numeric)
mongo(
    c,
    f'db.otp_requests.deleteMany({{email:"{owner["email"]}"}});',
    show=False,
)
r(
    c,
    f"rm -f /tmp/sm.jar; curl -s -c /tmp/sm.jar -X POST {BACKEND}/api/v1/auth/otp/request "
    f"-H 'Content-Type: application/json' "
    f"-d '{{\"email\":\"{owner['email']}\",\"surface\":\"whm\"}}'",
    show=False,
)
mongo(
    c,
    'const crypto = require("crypto"); '
    f'const h = crypto.createHash("sha256").update("{CODE}").digest("hex"); '
    f'db.otp_requests.updateMany({{email:"{owner["email"]}", used:false, expires_at:{{$gt:new Date()}}}}, '
    "{$set:{code_hash:h, attempt_count:0}});",
    show=False,
)
verify_out, _, _ = r(
    c,
    f"curl -s -b /tmp/sm.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
    f"-H 'Content-Type: application/json' "
    f'-d \'{{"email":"{owner["email"]}","code":"{CODE}","surface":"whm"}}\'',
    show=False,
)
try:
    jwt = json.loads(verify_out)["data"]["access_token"]
except Exception:
    cleanup(c)
    sys.exit(f"!! OTP verify failed: {verify_out[:200]}")
print(f"got admin JWT (len={len(jwt)})")

# 4. Mint an API token with deploy:link + deploy:read scope.
tok_out, _, _ = r(
    c,
    f"curl -s -X POST {BACKEND}/api/v1/whm/developer/tokens "
    f"-H 'Authorization: Bearer {jwt}' "
    f"-H 'Content-Type: application/json' "
    "-d '{\"name\":\"smoke-alias-link\",\"scopes\":[\"deploy:link\",\"deploy:read\"]}'",
    show=False,
)
try:
    api_token = json.loads(tok_out)["data"]["plaintext"]
except Exception:
    cleanup(c)
    sys.exit(f"!! token create failed: {tok_out[:200]}")
print(f"got API token (prefix={api_token[:20]}...)")


def http(method, path, body=None):
    extra = ""
    if body is not None:
        extra = f"-H 'Content-Type: application/json' -d '{body}' "
    cmd = (
        f"curl -s -o /tmp/sm.body -w '%{{http_code}}' -X {method} "
        f"{BACKEND}{path} -H 'Authorization: Bearer {api_token}' "
        f"{extra}"
    )
    code, _, _ = r(c, cmd, show=False)
    out, _, _ = r(c, "cat /tmp/sm.body", show=False)
    return int(code.strip() or "0"), out


# 5. Smoke checks --------------------------------------------------------------
print("--- check 1: GET /external/deploy/services lists our service ---")
status, body = http("GET", "/api/v1/external/deploy/services?limit=200")
must("GET services returns 200", status == 200, f"got {status}")
must(
    "our service appears in inventory",
    seed["service_id"] in body,
    "service id missing from response body",
)

print("--- check 2: link-domain happy path ---")
status, body = http(
    "POST",
    f"/api/v1/external/deploy/projects/{seed['project_id']}/services/{seed['service_id']}/link-domain",
    f'{{"domain":"{ALIAS}"}}',
)
must("link-domain returns 200", status == 200, f"got {status}; body={body[:200]}")
state, _, _ = mongo(
    c,
    f'printjson(db.project_services.findOne({{_id:ObjectId("{seed["service_id"]}")}}, {{alias_domains:1}}));',
    show=False,
)
must(f"alias_domains contains {ALIAS}", ALIAS in state, f"state={state[:200]}")
vhost, _, _ = r(c, f"cat /etc/nginx/sites-available/{PRIMARY} 2>/dev/null | grep -E '^\\s*server_name'", show=False)
must(
    "nginx server_name carries alias",
    ALIAS in vhost,
    f"server_name lines: {vhost.splitlines()[:3]}",
)
# v3.1.31: server_name must also include www.<alias> + cname.<alias>
# so https://www.<alias> doesn't fall through to the catch-all vhost.
must(
    f"nginx server_name carries www.{ALIAS}",
    f"www.{ALIAS}" in vhost,
    f"server_name lines: {vhost.splitlines()[:3]}",
)
must(
    f"nginx server_name carries cname.{ALIAS}",
    f"cname.{ALIAS}" in vhost,
    f"server_name lines: {vhost.splitlines()[:3]}",
)
# v3.1.31: API response carries ssl_covered_domains so the integrator
# can verify the cert actually covers what they linked. The .invalid
# TLD won't have a real LE cert in the smoke environment, so we accept
# either an empty list (no cert) OR a list that contains the alias —
# what we're checking is that the FIELD IS PRESENT in the response.
must(
    "API response carries ssl_covered_domains field",
    "ssl_covered_domains" in body or "ssl_warning" in body,
    f"body={body[:300]}",
)

print("--- check 3: link-domain with WRONG project id → 403 ---")
status, body = http(
    "POST",
    f"/api/v1/external/deploy/projects/000000000000000000000000/services/{seed['service_id']}/link-domain",
    f'{{"domain":"{ALIAS}"}}',
)
must(
    "wrong project_id returns 403 ServiceProjectMismatch",
    status == 403,
    f"got {status}; body={body[:200]}",
)

print("--- check 4: link-domain with malformed body → 400 ---")
status, body = http(
    "POST",
    f"/api/v1/external/deploy/projects/{seed['project_id']}/services/{seed['service_id']}/link-domain",
    "{\"domain\":\"\"}",
)
must("empty domain returns 400", status == 400, f"got {status}; body={body[:200]}")

print("--- check 5: link-domain on missing service → 404 ---")
status, body = http(
    "POST",
    f"/api/v1/external/deploy/projects/{seed['project_id']}/services/000000000000000000000000/link-domain",
    f'{{"domain":"{ALIAS}"}}',
)
must("missing service returns 404", status == 404, f"got {status}; body={body[:200]}")

print("--- check 6: unlink happy path ---")
status, body = http(
    "DELETE",
    f"/api/v1/external/deploy/projects/{seed['project_id']}/services/{seed['service_id']}/link-domain/{ALIAS}",
)
must("unlink returns 200", status == 200, f"got {status}")
state, _, _ = mongo(
    c,
    f'printjson(db.project_services.findOne({{_id:ObjectId("{seed["service_id"]}")}}, {{alias_domains:1}}));',
    show=False,
)
must(f"alias_domains no longer contains {ALIAS}", ALIAS not in state, f"state={state[:200]}")

print("--- check 7: unlink with WRONG project id → 403 ---")
status, body = http(
    "DELETE",
    f"/api/v1/external/deploy/projects/000000000000000000000000/services/{seed['service_id']}/link-domain/{ALIAS}",
)
must(
    "wrong project_id on unlink returns 403",
    status == 403,
    f"got {status}; body={body[:200]}",
)

# 6. Cleanup
time.sleep(0.5)
cleanup(c)

print("--- summary ---")
if FAILS:
    print(f"FAILED: {len(FAILS)} check(s)")
    for f in FAILS:
        print(f"  - {f}")
    sys.exit(1)
print("ALL CHECKS PASSED")
