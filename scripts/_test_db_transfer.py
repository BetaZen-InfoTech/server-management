"""End-to-end Database transfer test on v3.0.16.

Creates:
  - A vendor + Linux user on the SOURCE box
  - A MySQL database (with operator password)
  - A MongoDB database (with operator password)
  - A db_access_host (IP allowlist) on the MySQL DB
Then drives a transfer (source 155.209 → dest 156.87) via the
TransferService API and asserts on the destination:
  - MySQL DB row present, panel password matches MySQL's actual auth
    (autologin works) — connect to mysql with that password.
  - MongoDB DB row present, panel password matches MongoDB's actual
    auth — connect to mongo with that password.
  - db_access_hosts row present.
  - The host-scoped MySQL GRANT exists.

Direct-injects the source state into Mongo + MySQL/MongoDB so we don't
need to drive the WHM UI for create-database. The TRANSFER itself
goes through the real Create/Run flow so the bugs we fixed are
exercised.
"""
import json, os, re, sys, time
from pathlib import Path
import paramiko

try: sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception: pass

md = Path(__file__).resolve().parent.parent / "testing-vps-details.md"
text = md.read_text(encoding="utf-8", errors="replace")
def pwd(s):
    m = re.search(rf"#+\s*{s}\s+server.*?password[^`]*`([^`]+)`", text, re.IGNORECASE | re.DOTALL)
    return m.group(1)
SRC_PWD = pwd("Old"); DST_PWD = pwd("New")
SRC_HOST = "187.127.155.209"; DST_HOST = "187.127.156.87"

# Test fixture — vendor / dbs / passwords
VENDOR = "xfertest"
LINUX_USER = "xfertest"   # what panel auto-creates
DB_USER_NAME = "xfertest_app"        # panel pattern: <vendor>_<dbuser>
MYSQL_DB = "xfertest_mysqldb"
MONGO_DB = "xfertest_mongodb"
DB_PASS = "xfertest_secret_pwd_AB12"   # operator-set password
ACCESS_IP = "203.0.113.42"            # RFC 5737 docs IP
SRC_BACKEND = "http://127.0.0.1:8080"
DST_BACKEND = "http://127.0.0.1:8080"


def ssh(host, p):
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, username="root", password=p, timeout=20, look_for_keys=False, allow_agent=False)
    return c


def r(c, cmd, show=True, timeout=120):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    if show:
        print(f"\n$ {cmd[:140]}{'…' if len(cmd) > 140 else ''}")
        for line in (out+err).strip().splitlines()[:18]:
            print(f"    {line}")
    return out.strip()


def mongo(c, q, show=True):
    safe = q.replace("'", "'\\''")
    return r(c, 'cd /opt/serverpanel; . ./.env 2>/dev/null || true; '
        'URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; '
        f'mongosh --quiet "$URI" --eval \'{safe}\'', show=show)


src = ssh(SRC_HOST, SRC_PWD)
dst = ssh(DST_HOST, DST_PWD)


# ─── 1. Pre-clean both boxes (idempotent) ─────────────────────────────
print("=== 1. Pre-clean source + destination ===")
for c in (src, dst):
    # Drop any prior test state in mongo + actual db engines.
    mongo(c, f'db.databases.deleteMany({{db_name:{{$in:["{MYSQL_DB}","{MONGO_DB}"]}}}});'
             f'db.db_access_hosts.deleteMany({{}});'
             f'db.users.deleteMany({{username:"{LINUX_USER}"}});'
             f'db.transfer_jobs.deleteMany({{}});', show=False)
    r(c, f'mysql -e "DROP DATABASE IF EXISTS {MYSQL_DB}; DROP USER IF EXISTS \\"{DB_USER_NAME}\\"@\\"localhost\\"; DROP USER IF EXISTS \\"{DB_USER_NAME}\\"@\\"{ACCESS_IP}\\"; FLUSH PRIVILEGES;" 2>&1 || true', show=False)
    r(c, f'mongosh --quiet --eval \'db.getSiblingDB("{MONGO_DB}").dropDatabase()\' 2>&1 || true', show=False)
    r(c, f'id {LINUX_USER} 2>/dev/null && userdel -r {LINUX_USER} 2>&1 || true', show=False)


# ─── 2. Seed source state ─────────────────────────────────────────────
print("\n=== 2. Seed source state on 155.209 ===")
# Create the linux user (panel pattern) — needed for picked-user filter.
r(src, f'useradd -m -s /bin/bash {LINUX_USER} && id {LINUX_USER}')

# Pick a vendor_owner from each box separately — source's admin is what
# we'd reference for source state; destination's admin is who we drive
# the transfer API as.
def pick_admin(c):
    out = mongo(c,
        'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
        'print(JSON.stringify({email: u.email, id: u._id.toString(), '
        'tenant_id: (u.tenant_id && u.tenant_id.toString()) || u._id.toString()}));', show=False)
    for line in reversed(out.splitlines()):
        s = line.strip()
        if s.startswith("{"):
            try:
                return json.loads(s)
            except Exception:
                continue
    return None

src_admin = pick_admin(src)
dst_admin = pick_admin(dst)
admin = src_admin   # back-compat for downstream cleanup blocks
print(f"   src admin: {src_admin and src_admin['email']}")
print(f"   dst admin: {dst_admin and dst_admin['email']}")

# Create the actual MySQL DB + user with the operator's password
r(src, f'mysql -e "'
        f'CREATE DATABASE {MYSQL_DB} CHARACTER SET utf8mb4; '
        f'CREATE USER \\"{DB_USER_NAME}\\"@\\"localhost\\" IDENTIFIED BY \\"{DB_PASS}\\"; '
        f'GRANT ALL PRIVILEGES ON {MYSQL_DB}.* TO \\"{DB_USER_NAME}\\"@\\"localhost\\"; '
        f'FLUSH PRIVILEGES;"')
# Insert a small table + row so mysqldump has content
r(src, f'mysql {MYSQL_DB} -e "CREATE TABLE seed (id INT PRIMARY KEY, note VARCHAR(50)); INSERT INTO seed VALUES (1, \\"hello-from-source\\");"')

# Create the MongoDB DB + user with the operator's password.
# Use getSiblingDB to actually land the operations on the target DB
# — `use X` in --eval mode doesn't switch context (mongosh quirk).
mongo_seed_js = (
    f'db.getSiblingDB("{MONGO_DB}").seed.insertOne({{note:"hello-from-source", n:42}}); '
    f'db.getSiblingDB("{MONGO_DB}").createUser({{user:"{DB_USER_NAME}", pwd:"{DB_PASS}", '
    f' roles:[{{role:"readWrite", db:"{MONGO_DB}"}}]}});'
)
# Use the panel's own URI so we have admin auth to create the user
# even if mongo has authorization enabled.
r(src, f'. /opt/serverpanel/.env; mongosh --quiet "$MONGO_URI" --eval \'{mongo_seed_js}\' 2>&1')

# Insert panel `databases` rows (mongo + mysql) on source so transfer's
# panel-records sync sees them. Use the SAME password the actual MySQL/
# MongoDB user has so the post-fix logic finds it via resolvePanelDB.
mongo(src,
      f'const now = new Date(); '
      f'db.databases.insertMany([ '
      f'{{db_name:"{MYSQL_DB}", type:"mysql", username:"{DB_USER_NAME}", password:"{DB_PASS}", '
      f' host:"localhost", port:3306, user:"{LINUX_USER}", domain:"", '
      f' connection_string:"mysql://{DB_USER_NAME}@localhost:3306/{MYSQL_DB}", '
      f' created_at:now, updated_at:now}}, '
      f'{{db_name:"{MONGO_DB}", type:"mongodb", username:"{DB_USER_NAME}", password:"{DB_PASS}", '
      f' host:"localhost", port:27017, user:"{LINUX_USER}", domain:"", '
      f' connection_string:"mongodb://{DB_USER_NAME}:***@localhost:27017/{MONGO_DB}", '
      f' created_at:now, updated_at:now}} '
      f']); '
      f'const mysqlRow = db.databases.findOne({{db_name:"{MYSQL_DB}"}}); '
      f'db.db_access_hosts.insertOne({{database_id: mysqlRow._id, host:"{ACCESS_IP}", '
      f' comment:"transfer-test allowlist", created_at:now}}); '
      'print("seeded panel rows");')

# Verify source state
print("\n   Source MySQL connect (operator password):")
r(src, f'mysql -u {DB_USER_NAME} -p{DB_PASS} -e "SELECT note FROM {MYSQL_DB}.seed;" 2>&1')


# ─── 3. Drive the transfer via the destination's API ─────────────────
print(f"\n=== 3. Mint admin token on dest, kick off transfer ===")
dst_email = dst_admin["email"]
mongo(dst, f'db.otp_requests.deleteMany({{email:"{dst_email}"}});', show=False)
r(dst, f"rm -f /tmp/x.jar; curl -s -c /tmp/x.jar -X POST {DST_BACKEND}/api/v1/auth/otp/request "
       f"-H 'Content-Type: application/json' "
       f"-d '{{\"email\":\"{dst_email}\",\"surface\":\"whm\"}}'", show=False)
mongo(dst,
      'const crypto = require("crypto"); '
      'const h = crypto.createHash("sha256").update("XFERDB1234").digest("hex"); '
      f'db.otp_requests.updateMany({{email:"{dst_email}", used:false, expires_at:{{$gt:new Date()}}}}, '
      '{$set:{code_hash:h}});', show=False)
out = r(dst, f"curl -s -b /tmp/x.jar -X POST {DST_BACKEND}/api/v1/auth/otp/verify "
       f"-H 'Content-Type: application/json' "
       f"-d '{{\"email\":\"{dst_email}\",\"code\":\"XFERDB1234\"}}'", show=False)
token = json.loads(out).get("data", {}).get("access_token", "")
print(f"   token len={len(token)}")
H = f"-H 'Authorization: Bearer {token}'"

# POST transfers create with components selected
payload = {
    "source_ip": SRC_HOST,
    "source_port": 22,
    "username": "root",
    "password": SRC_PWD,
    "protocol": "ssh",
    "components": {
        "Files": False, "Databases": True, "EmailData": False, "DNSData": False,
        "WebData": False, "SSL": False, "Apps": False,
    },
    "selection": {
        "linux_users": [LINUX_USER],
        "mysql_dbs":   [MYSQL_DB],
        "mongo_dbs":   [MONGO_DB],
    },
}
import json as jj
out = r(dst, f"curl -s -X POST {DST_BACKEND}/api/v1/whm/transfers/ {H} "
       f"-H 'Content-Type: application/json' -d '{jj.dumps(payload)}' "
       "-w '\\nHTTP=%{http_code}\\n'")
job = None
try:
    job = json.loads(out.split("HTTP=")[0]).get("data", {})
except Exception:
    pass
job_id = (job or {}).get("id")
print(f"\n   transfer job id = {job_id}")
if not job_id:
    print(f"   !! could not extract job id; response: {out[:300]}")
    src.close(); dst.close(); sys.exit(1)


# ─── 4. Poll the job until done ──────────────────────────────────────
print(f"\n=== 4. Wait for transfer to complete ===")
final_status = ""
for i in range(120):
    time.sleep(5)
    out = r(dst, f"curl -s {DST_BACKEND}/api/v1/whm/transfers/{job_id} {H}", show=False)
    try:
        data = json.loads(out).get("data", {})
        st = data.get("status", "")
        if st in ("completed", "failed", "cancelled"):
            final_status = st
            print(f"   status={st} (after {i*5}s)")
            for step in data.get("steps", []):
                tag = step.get("status", "?")
                print(f"     [{tag}] {step.get('name')} {step.get('details') or step.get('error') or ''}")
            break
        if i % 4 == 0:
            print(f"   ... status={st} ({i*5}s)")
    except Exception as e:
        print(f"   !! poll parse: {e}")
        break

if final_status != "completed":
    print(f"\n   !! transfer ended with status={final_status}; tailing log")
    r(dst, f"curl -s {DST_BACKEND}/api/v1/whm/transfers/{job_id} {H}")
    src.close(); dst.close(); sys.exit(2)


# ─── 5. Assertions on the destination ────────────────────────────────
print(f"\n=== 5. Destination assertions ===")
results = []

# (a) Panel rows present
print("\n   (a) destination panel `databases` rows:")
out = mongo(dst, f'const rs = db.databases.find({{db_name:{{$in:["{MYSQL_DB}","{MONGO_DB}"]}}}}, '
                 '{db_name:1, type:1, username:1, password:1, _id:0}).toArray(); '
                 'print(JSON.stringify(rs, null, 2));', show=False)
print(out)
mysql_present = MYSQL_DB in out and DB_PASS in out
mongo_present = MONGO_DB in out and DB_PASS in out
results.append(("MySQL panel row + password preserved", mysql_present))
results.append(("MongoDB panel row + password preserved", mongo_present))

# (b) Real MySQL connect with the operator password
print("\n   (b) destination MySQL connect:")
out = r(dst, f'mysql -u {DB_USER_NAME} -p{DB_PASS} -e "SELECT note FROM {MYSQL_DB}.seed;" 2>&1')
results.append(("MySQL CLI auth with panel password", "hello-from-source" in out))

# (c) Real MongoDB connect with the operator password
print("\n   (c) destination MongoDB connect:")
out = r(dst, f'mongosh --quiet "mongodb://{DB_USER_NAME}:{DB_PASS}@localhost:27017/{MONGO_DB}" '
             f'--eval \'db.seed.findOne()\' 2>&1')
results.append(("MongoDB CLI auth with panel password", "hello-from-source" in out))

# (d) db_access_hosts row present
print("\n   (d) destination db_access_hosts:")
mongo(dst, f'const dbRow = db.databases.findOne({{db_name:"{MYSQL_DB}"}}); '
           'const ah = db.db_access_hosts.find({database_id: dbRow._id}).toArray(); '
           'print(JSON.stringify(ah.map(r => r.host)));')
out = mongo(dst, f'const dbRow = db.databases.findOne({{db_name:"{MYSQL_DB}"}}); '
                 'const ah = db.db_access_hosts.find({database_id: dbRow._id}).toArray(); '
                 'print(JSON.stringify(ah.map(r => r.host)));', show=False)
results.append(("db_access_hosts row present", ACCESS_IP in out))

# (e) MySQL GRANT for the access host actually exists
print("\n   (e) destination MySQL grants for the access host:")
out = r(dst, f'mysql -e "SELECT user, host FROM mysql.user WHERE user=\\"{DB_USER_NAME}\\";"')
results.append(("MySQL host-scoped GRANT exists", ACCESS_IP in out))


# ─── 5z. Dump the transfer job's full log before we cleanup ──────────
print(f"\n=== 5z. Transfer job logs ===")
mongo(dst, f'const j = db.transfer_jobs.findOne({{_id: ObjectId("{job_id}")}}); '
           'if (j) (j.logs||[]).forEach(l => print("[" + l.level + "]" + '
           '(l.step ? "[" + l.step + "]" : "") + " " + l.message));')


# ─── 6. Cleanup ──────────────────────────────────────────────────────
print(f"\n=== 6. Cleanup both boxes ===")
for c, ad in ((src, src_admin), (dst, dst_admin)):
    mongo(c, f'db.databases.deleteMany({{db_name:{{$in:["{MYSQL_DB}","{MONGO_DB}"]}}}}); '
             f'db.db_access_hosts.deleteMany({{}}); '
             f'db.users.updateOne({{email:"{ad["email"]}"}}, {{$unset:{{refresh_token:"", refresh_expires_at:""}}}}); '
             f'db.otp_requests.deleteMany({{email:"{ad["email"]}"}}); '
             f'db.transfer_jobs.deleteMany({{}});', show=False)
    r(c, f'mysql -e "DROP DATABASE IF EXISTS {MYSQL_DB}; DROP USER IF EXISTS \\"{DB_USER_NAME}\\"@\\"localhost\\"; DROP USER IF EXISTS \\"{DB_USER_NAME}\\"@\\"{ACCESS_IP}\\"; FLUSH PRIVILEGES;" 2>&1 || true', show=False)
    r(c, f'mongosh --quiet --eval \'db.getSiblingDB("{MONGO_DB}").dropDatabase()\' 2>&1 || true', show=False)
    r(c, f'id {LINUX_USER} 2>/dev/null && userdel -r {LINUX_USER} 2>&1 || true', show=False)
r(dst, "rm -f /tmp/x.jar", show=False)
src.close(); dst.close()


# ─── 7. Tally ────────────────────────────────────────────────────────
print(f"\n{'='*60}")
passed = sum(1 for _, ok in results if ok)
for desc, ok in results:
    print(f"   {'✓' if ok else '✗'} {desc}")
print(f"   {passed}/{len(results)} assertions passed")
print(f"{'='*60}")
if passed != len(results):
    sys.exit(1)
