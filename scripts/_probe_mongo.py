"""Probe mongo state on source to understand auth + dump variants."""
import re, sys, paramiko
from pathlib import Path
sys.stdout.reconfigure(encoding="utf-8", errors="replace")

text = Path("testing-vps-details.md").read_text()
pwd = re.search(r"#+\s*Old\s+server.*?password[^`]*`([^`]+)`", text, re.IGNORECASE | re.DOTALL).group(1)
c = paramiko.SSHClient(); c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect("187.127.155.209", username="root", password=pwd, timeout=20,
          look_for_keys=False, allow_agent=False)

def run(cmd):
    _, so, se = c.exec_command(cmd, timeout=30)
    return so.read().decode("utf-8", errors="replace") + se.read().decode("utf-8", errors="replace")

# Get MONGO_URI from .env
uri = run(". /opt/serverpanel/.env >/dev/null 2>&1; printf '%s' \"$MONGO_URI\"").strip()
print(f"URI present: {bool(uri)}")

# Strip default-db from path so we can do --db <X>
# Pattern: mongodb://user:pass@host:port/dbname?authSource=admin
m = re.match(r"^(mongodb://[^@]+@[^/]+)/[^?]*(\?.*)?$", uri or "")
if m:
    base_uri = m.group(1) + "/" + (m.group(2) or "")
else:
    base_uri = uri or ""
print(f"base_uri: {base_uri[:60]}...")

# Re-seed source state
seed_db = "xfertest_mongodb"
seed_user = "xfertest_app"
seed_pass = "xfertest_secret_pwd_AB12"

# Drop + recreate
print("\n=== drop existing ===")
print(run(f'mongosh --quiet "{uri}" --eval \'db.getSiblingDB("{seed_db}").dropDatabase()\' 2>&1'))
print(run(f'mongosh --quiet "{uri}" --eval \'try {{ db.getSiblingDB("{seed_db}").dropUser("{seed_user}"); }} catch(e) {{ print("nouser") }}\' 2>&1'))

print("\n=== seed: insert doc + create user via admin URI ===")
print(run(f'mongosh --quiet "{uri}" --eval \''
          f'db.getSiblingDB("{seed_db}").seed.insertOne({{note:"hello-from-source"}}); '
          f'db.getSiblingDB("{seed_db}").createUser({{user:"{seed_user}",pwd:"{seed_pass}",roles:[{{role:"readWrite",db:"{seed_db}"}}]}});'
          f'\' 2>&1'))

print("\n=== verify user exists ===")
print(run(f'mongosh --quiet "{uri}" --eval \'db.getSiblingDB("{seed_db}").getUsers().forEach(u => print(u.user))\' 2>&1'))

print("\n=== mongodump variant A: --uri base_uri --db ===")
print(run(f'rm -f /tmp/d.gz; mongodump --uri "{base_uri}" --archive=/tmp/d.gz --gzip --db {seed_db} 2>&1; ls -la /tmp/d.gz 2>&1'))

print("\n=== mongodump variant B: --host + --username + --password ===")
# Extract host:port + user:pass from URI
mm = re.match(r"^mongodb://([^:]+):([^@]+)@([^/]+)/", uri or "")
if mm:
    user, password, hostport = mm.group(1), mm.group(2), mm.group(3)
    print(run(f'rm -f /tmp/d.gz; mongodump --host "{hostport}" --username "{user}" --password "{password}" '
              f'--authenticationDatabase admin --archive=/tmp/d.gz --gzip --db {seed_db} 2>&1; '
              f'ls -la /tmp/d.gz 2>&1'))

print("\n=== mongodump variant C: as DB user ===")
print(run(f'rm -f /tmp/d.gz; mongodump --host 127.0.0.1:27017 --username "{seed_user}" --password "{seed_pass}" '
          f'--authenticationDatabase {seed_db} --archive=/tmp/d.gz --gzip --db {seed_db} 2>&1; '
          f'ls -la /tmp/d.gz 2>&1'))

# Cleanup
print("\n=== cleanup ===")
print(run(f'mongosh --quiet "{uri}" --eval \''
          f'try {{ db.getSiblingDB("{seed_db}").dropUser("{seed_user}"); }} catch(e) {{}} '
          f'db.getSiblingDB("{seed_db}").dropDatabase();'
          f'\' 2>&1'))
print(run("rm -f /tmp/d.gz"))
c.close()
