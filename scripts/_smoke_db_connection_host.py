"""Smoke test: GET /databases/<id>/connection must NOT return host=localhost
when SERVER_IP is set. v3.0.10 rewrites the response on the way out.

Seeds a fake mongodb db row with Host="localhost", mints an admin
token, hits the connection endpoint, asserts the response uses the
panel's public IP (or MONGO_PUBLIC_HOST when configured).
"""
from __future__ import annotations
import json, os, sys, paramiko

try: sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception: pass

PASSWORD = os.environ.get("BZ_VPS_PASS")
if not PASSWORD: sys.exit("BZ_VPS_PASS not set")
HOST = os.environ.get("BZ_VPS_HOST", "187.127.155.209")
USER = os.environ.get("BZ_VPS_USER", "root")
BACKEND = "http://127.0.0.1:8080"
DB_NAME = "smoke_conn_host_test"
FAILS: list[str] = []


def r(c, cmd, show=False, timeout=30):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    code = so.channel.recv_exit_status()
    if show:
        print(f"[{'OK' if code == 0 else f'exit={code}'}] $ {cmd[:120]}")
        for ln in (out + err).strip().splitlines()[:10]: print(f"    {ln}")
    return out.strip(), err.strip(), code


def mongo(c, q, show=False):
    safe = q.replace("'", "'\\''")
    return r(c, 'cd /opt/serverpanel; . ./.env 2>/dev/null || true; '
               'URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; '
               f'mongosh --quiet "$URI" --eval \'{safe}\'', show=show)


def must(label, cond, detail=""):
    tag = "PASS" if cond else "FAIL"
    print(f"[{tag}] {label}" + (f" — {detail}" if detail else ""))
    if not cond: FAILS.append(label)


c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, username=USER, password=PASSWORD, timeout=20,
          look_for_keys=False, allow_agent=False)

print(f"=== smoke test on {HOST}: connection-modal host rewrite ===")

# Pick an admin (vendor_owner) and mint a token
ow_out, _, _ = mongo(c,
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    'print(JSON.stringify({email: u.email}));', show=False)
owner = None
for ln in reversed(ow_out.splitlines()):
    s = ln.strip()
    if s.startswith("{"):
        try: owner = json.loads(s); break
        except Exception: continue
mongo(c, f'db.otp_requests.deleteMany({{email:"{owner["email"]}"}});', show=False)
r(c, f"rm -f /tmp/sm.jar; curl -s -c /tmp/sm.jar -X POST {BACKEND}/api/v1/auth/otp/request "
     f"-H 'Content-Type: application/json' "
     f"-d '{{\"email\":\"{owner['email']}\",\"surface\":\"whm\"}}'", show=False)
mongo(c, 'const crypto = require("crypto"); '
         'const h = crypto.createHash("sha256").update("SMOKE9").digest("hex"); '
         f'db.otp_requests.updateMany({{email:"{owner["email"]}", used:false, expires_at:{{$gt:new Date()}}}}, '
         '{$set:{code_hash:h}});', show=False)
out, _, _ = r(c, f"curl -s -b /tmp/sm.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
                f"-H 'Content-Type: application/json' "
                f"-d '{{\"email\":\"{owner['email']}\",\"code\":\"SMOKE9\"}}'", show=False)
tok = ""
try: tok = json.loads(out).get("data", {}).get("access_token", "")
except Exception: pass
if not tok: print("!! no token"); sys.exit(1)

# Seed a fake mongodb db row with Host="localhost" — exactly what
# CreateDatabase writes today, and what the user's screenshot shows.
mongo(c, f'db.databases.deleteMany({{db_name:"{DB_NAME}"}});', show=False)
seed_js = f"""
const now = new Date();
const r = db.databases.insertOne({{
  db_name: "{DB_NAME}", type: "mongodb",
  username: "smoke_user", password: "smoke_pass_x",
  domain: "", host: "localhost", port: 27017,
  connection_string: "mongodb://smoke_user:smoke_pass_x@localhost:27017/{DB_NAME}",
  created_at: now, updated_at: now,
}});
print(JSON.stringify({{id: r.insertedId.toString()}}));
""".strip()
seed_out, _, _ = mongo(c, seed_js, show=False)
seed = None
for ln in reversed(seed_out.splitlines()):
    s = ln.strip()
    if s.startswith("{"):
        try: seed = json.loads(s); break
        except Exception: continue
print(f"seeded db row id={seed['id']}")

# Hit the connection endpoint and inspect the JSON
out, _, _ = r(c, f"curl -s {BACKEND}/api/v1/whm/databases/{seed['id']}/connection "
                f"-H 'Authorization: Bearer {tok}'", show=False)
print(f"raw response: {out}")
data = {}
try:
    data = json.loads(out).get("data", {}) or {}
except Exception:
    pass

returned_host = data.get("host", "")
returned_uri = data.get("connection_string", "")
returned_cli = data.get("cli_command", "")
print(f"  host={returned_host!r}")
print(f"  uri={returned_uri!r}")
print(f"  cli={returned_cli!r}")

must("returned host is NOT 'localhost'", returned_host not in ("", "localhost", "127.0.0.1"),
     detail=f"got {returned_host!r}")
must("connection_string does NOT contain '@localhost:'",
     "@localhost:" not in returned_uri and "@127.0.0.1:" not in returned_uri,
     detail=f"got {returned_uri!r}")
must("cli command does NOT contain 'localhost'",
     "localhost" not in returned_cli and "127.0.0.1" not in returned_cli,
     detail=f"got {returned_cli!r}")
must("returned host equals SERVER_IP (panel's public IP)",
     returned_host == HOST, detail=f"got {returned_host!r}, expected {HOST}")

# Cleanup
mongo(c, f'db.databases.deleteOne({{db_name:"{DB_NAME}"}}); '
         f'db.users.updateOne({{email:"{owner["email"]}"}}, {{$unset:{{refresh_token:"", refresh_expires_at:""}}}}); '
         f'db.otp_requests.deleteMany({{email:"{owner["email"]}"}});', show=False)
r(c, "rm -f /tmp/sm.jar", show=False)
c.close()

if FAILS:
    print(f"\n!! {len(FAILS)} FAILURES:")
    for f in FAILS: print(f"   - {f}")
    sys.exit(1)
print("\nALL SMOKE CHECKS PASSED")
