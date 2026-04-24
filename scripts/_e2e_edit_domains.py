"""End-to-end test for the v3.0.4 'edit domains in service modal' feature.

Three scenarios, each over the LIVE update API the modal calls:
  PUT /api/v1/whm/projects/<id>/services/<svc>
    body includes primary_domain + alias_domains.

  Scenario A — primary domain rename:
    initial primary=p1.invalid, aliases=[a1.invalid, a2.invalid]
    PUT primary=p2.invalid (aliases unchanged)
    expect:
      - mongo: primary_domain=p2.invalid, alias_domains unchanged
      - on disk: /etc/nginx/sites-available/p1.invalid is GONE
      - on disk: /etc/nginx/sites-available/p2.invalid exists
      - server_name lists [p2.invalid, a1.invalid, a2.invalid]
      - HTTP 200 served on each domain via Host header

  Scenario B — replace whole alias list:
    PUT alias_domains=[b1.invalid, b2.invalid, b3.invalid]
    expect:
      - mongo aliases match exactly (sorted compare)
      - vhost server_name lists primary + b1/b2/b3 only
      - removed aliases (a1, a2) are GONE from server_name

  Scenario C — clear all aliases:
    PUT alias_domains=[]
    expect:
      - mongo aliases==[]
      - vhost server_name has only the primary
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

P1 = "edit-dom-p1.invalid"
P2 = "edit-dom-p2.invalid"
A1 = "edit-dom-a1.invalid"
A2 = "edit-dom-a2.invalid"
B1 = "edit-dom-b1.invalid"
B2 = "edit-dom-b2.invalid"
B3 = "edit-dom-b3.invalid"
PORT = 34980
SLUG = "edit-dom"
FAILS: list[str] = []


def r(c, cmd, show=False, timeout=60):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    code = so.channel.recv_exit_status()
    if show:
        print(f"[{'OK' if code == 0 else f'exit={code}'}] $ {cmd[:120]}")
        for ln in (out + err).strip().splitlines()[:15]: print(f"    {ln}")
    return out.strip(), err.strip(), code


def mongo(c, q, show=False):
    safe = q.replace("'", "'\\''")
    return r(c, 'cd /opt/serverpanel; . ./.env 2>/dev/null || true; '
               'URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; '
               f'mongosh --quiet "$URI" --eval \'{safe}\'', show=show)


def must(label, cond, detail=""):
    tag = "PASS" if cond else "FAIL"
    print(f"    [{tag}] {label}" + (f" — {detail}" if detail else ""))
    if not cond: FAILS.append(label)


def section(t):
    print(f"\n{'=' * 70}\n{t}\n{'=' * 70}")


def get_state(c, svc_id, primary):
    """Return (mongo_doc, on_disk_vhost_text)."""
    mout, _, _ = mongo(c,
        f'const s = db.project_services.findOne({{_id: ObjectId("{svc_id}")}}); '
        'print(JSON.stringify({primary:s.primary_domain, aliases:(s.alias_domains||[]).slice().sort()}));',
        show=False)
    mdoc = None
    for ln in reversed(mout.splitlines()):
        s = ln.strip()
        if s.startswith("{"):
            try: mdoc = json.loads(s); break
            except Exception: continue
    vh, _, _ = r(c, f"cat /etc/nginx/sites-available/{primary} 2>&1", show=False)
    return mdoc, vh


c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, username=USER, password=PASSWORD, timeout=20,
          look_for_keys=False, allow_agent=False)

print(f"=== edit-domains test on {HOST} ===")

# ── Setup: clean state, seed a backend project + service, start a tiny
#    HTTP echo on PORT so we can also assert HTTP 200 per domain.
section("0. setup")
mongo(c, f'db.projects.deleteMany({{slug:"{SLUG}"}}); '
         f'db.project_services.deleteMany({{primary_domain:{{$in:["{P1}","{P2}"]}}}});',
      show=False)
for d in (P1, P2, A1, A2, B1, B2, B3):
    r(c, f"rm -f /etc/nginx/sites-available/{d} /etc/nginx/sites-enabled/{d}", show=False)
r(c, "nginx -s reload 2>&1 || systemctl reload nginx", show=False)

# Tiny echo server so we can curl it through nginx to verify routing.
r(c, "pkill -f edit_dom_echo || true; sleep 0.3", show=False)
echo_py = f"""
import http.server, socketserver, json
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        host = self.headers.get('Host', '')
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps({{'host': host}}).encode())
    def log_message(self, *a): pass
socketserver.TCPServer.allow_reuse_address = True
with socketserver.TCPServer(('127.0.0.1', {PORT}), H) as s: s.serve_forever()
"""
r(c, f"cat > /tmp/edit_dom_echo.py <<'EOF'\n{echo_py}\nEOF", show=False)
r(c, "nohup python3 /tmp/edit_dom_echo.py > /tmp/edit_dom_echo.log 2>&1 &", show=False)
time.sleep(1)

# Owner + token
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
mongo(c, f'db.otp_requests.deleteMany({{email:"{owner["email"]}"}});', show=False)
r(c, f"rm -f /tmp/ed.jar; curl -s -c /tmp/ed.jar -X POST {BACKEND}/api/v1/auth/otp/request "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"email\":\"{owner['email']}\",\"surface\":\"whm\"}}'", show=False)
mongo(c, 'const crypto = require("crypto"); '
         'const h = crypto.createHash("sha256").update("EDITDOM9").digest("hex"); '
         f'db.otp_requests.updateMany({{email:"{owner["email"]}", used:false, expires_at:{{$gt:new Date()}}}}, '
         '{$set:{code_hash:h}});', show=False)
out, _, _ = r(c, f"curl -s -b /tmp/ed.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
                f"-H 'Content-Type: application/json' "
                f"-d '{{\"email\":\"{owner['email']}\",\"code\":\"EDITDOM9\"}}'", show=False)
tok = ""
try: tok = json.loads(out).get("data", {}).get("access_token", "")
except Exception: pass
if not tok: print("!! no token"); sys.exit(1)

# Seed project + service: primary=P1, aliases=[A1, A2]
seed_js = f"""
const now = new Date();
const pr = db.projects.insertOne({{slug:"{SLUG}", name:"edit-dom test",
    user_id: ObjectId("{owner['id']}"), tenant_id: ObjectId("{owner['tenant_id']}"),
    created_at: now, updated_at: now}});
const sv = db.project_services.insertOne({{project_id: pr.insertedId,
    name:"web", role:"backend", framework:"nodejs",
    primary_domain:"{P1}", alias_domains:["{A1}","{A2}"],
    port:{PORT}, path_prefix:"/", build_dir:"/tmp/{SLUG}",
    install_dir:"/tmp/{SLUG}", systemd_unit:"sp-proj-{SLUG}",
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

# Trigger initial vhost write via dummy alias add+remove (idempotent
# reconcile). The modal save would do the same internally, but we
# need an existing vhost to test the rename path.
dummy = "edit-dom-init-trigger.invalid"
r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/projects/{seed['project_id']}"
     f"/services/{seed['service_id']}/aliases "
     f"-H 'Authorization: Bearer {tok}' -H 'Content-Type: application/json' "
     f"-d '{{\"domain\":\"{dummy}\"}}'", show=False)
r(c, f"curl -s -X DELETE {BACKEND}/api/v1/whm/projects/{seed['project_id']}"
     f"/services/{seed['service_id']}/aliases/{dummy} "
     f"-H 'Authorization: Bearer {tok}'", show=False)
time.sleep(0.4)

# Confirm initial state on disk
mdoc, vh = get_state(c, seed["service_id"], P1)
must("setup: P1 vhost exists", "server_name " in vh and P1 in vh, detail=f"vhost head: {vh.splitlines()[:3]}")
must("setup: A1 in initial vhost", A1 in vh)
must("setup: A2 in initial vhost", A2 in vh)


def put_update(body: dict) -> tuple[int, str]:
    out, _, _ = r(c,
        f"curl -s -X PUT {BACKEND}/api/v1/whm/projects/{seed['project_id']}"
        f"/services/{seed['service_id']} "
        f"-H 'Authorization: Bearer {tok}' -H 'Content-Type: application/json' "
        f"-d '{json.dumps(body)}' -w '\\n__HTTP=%{{http_code}}__'",
        show=False)
    http = ""
    body_lines = []
    for ln in out.splitlines():
        if "__HTTP=" in ln:
            http = ln
        else:
            body_lines.append(ln)
    return http, "\n".join(body_lines)


# ── Scenario A: rename primary ──────────────────────────────────────
section(f"A. rename primary {P1} → {P2}")
http, resp = put_update({"primary_domain": P2})
must("PUT returned 200", "__HTTP=200__" in http, detail=f"{http}, body={resp[:200]}")
time.sleep(0.4)
mdoc, _ = get_state(c, seed["service_id"], P2)
must("mongo primary_domain == P2", mdoc and mdoc["primary"] == P2, detail=str(mdoc))
must("mongo aliases unchanged", mdoc and mdoc["aliases"] == sorted([A1, A2]),
     detail=f"got {mdoc['aliases'] if mdoc else 'None'}")
# Old vhost gone
old_exists, _, _ = r(c, f"test -f /etc/nginx/sites-available/{P1} && echo YES || echo NO", show=False)
must("old vhost file removed", old_exists.strip() == "NO")
# New vhost has all three names. r() returns (stdout, stderr, code).
new_vh, _, _ = r(c, f"cat /etc/nginx/sites-available/{P2}", show=False)
for d in (P2, A1, A2):
    must(f"new vhost server_name contains {d}", d in new_vh)
# HTTP 200 on each domain
for d in (P2, A1, A2):
    body_out, _, _ = r(c, f"curl -s -H 'Host: {d}' http://127.0.0.1/ -w '\\n__HTTP=%{{http_code}}__'",
                       show=False, timeout=10)
    js = ""
    for ln in body_out.splitlines():
        if ln.strip().startswith("{"): js = ln; break
    try: parsed = json.loads(js)
    except Exception: parsed = {}
    must(f"HTTP 200 + Host echo for {d}",
         "__HTTP=200__" in body_out and parsed.get("host") == d,
         detail=f"{body_out[:200]!r}")

# ── Scenario B: replace alias list ──────────────────────────────────
section(f"B. replace aliases → [{B1}, {B2}, {B3}]")
http, resp = put_update({"alias_domains": [B1, B2, B3]})
must("PUT returned 200", "__HTTP=200__" in http, detail=f"{http}, body={resp[:200]}")
time.sleep(0.4)
mdoc, vh = get_state(c, seed["service_id"], P2)
must("mongo aliases == [B1,B2,B3] (sorted)", mdoc and mdoc["aliases"] == sorted([B1, B2, B3]),
     detail=f"got {mdoc['aliases'] if mdoc else 'None'}")
for d in (P2, B1, B2, B3):
    must(f"vhost server_name contains {d}", d in vh)
for d in (A1, A2):
    must(f"vhost server_name does NOT contain old alias {d}", d not in vh)

# ── Scenario C: clear all aliases ───────────────────────────────────
section("C. clear all aliases (alias_domains=[])")
http, resp = put_update({"alias_domains": []})
must("PUT returned 200", "__HTTP=200__" in http, detail=f"{http}, body={resp[:200]}")
time.sleep(0.4)
mdoc, vh = get_state(c, seed["service_id"], P2)
must("mongo aliases == []", mdoc and mdoc["aliases"] == [], detail=f"got {mdoc['aliases'] if mdoc else 'None'}")
must("vhost server_name still contains primary", P2 in vh)
for d in (B1, B2, B3):
    must(f"vhost server_name does NOT contain cleared alias {d}", d not in vh)

# ── Cleanup ─────────────────────────────────────────────────────────
section("cleanup")
r(c, "pkill -f edit_dom_echo 2>&1 || true", show=False)
for d in (P1, P2, A1, A2, B1, B2, B3):
    r(c, f"rm -f /etc/nginx/sites-available/{d} /etc/nginx/sites-enabled/{d}", show=False)
r(c, "nginx -s reload 2>&1 || systemctl reload nginx", show=False)
mongo(c, f'db.project_services.deleteOne({{_id: ObjectId("{seed["service_id"]}")}}); '
         f'db.projects.deleteOne({{_id: ObjectId("{seed["project_id"]}")}}); '
         f'db.users.updateOne({{email:"{owner["email"]}"}}, {{$unset:{{refresh_token:"", refresh_expires_at:""}}}}); '
         f'db.otp_requests.deleteMany({{email:"{owner["email"]}"}});', show=False)
r(c, "rm -f /tmp/ed.jar /tmp/edit_dom_echo.py /tmp/edit_dom_echo.log", show=False)
c.close()

section("SUMMARY")
if FAILS:
    print(f"!! {len(FAILS)} FAILURES:")
    for f in FAILS: print(f"   - {f}")
    sys.exit(1)
print("ALL EDIT-DOMAIN TESTS PASSED")
