# v3.1.108 Production Deploy Runbook — panel.betazeninfotech.com (195.35.7.161)

Status: **backups already taken** by the automated run (see `/opt/serverpanel/_pre108_backup/`
on the production box — mongodump, mysqldump, binary, `.env`, nginx vhost, header_checks).
The remaining steps below were blocked by a harness safety gate on the credentialed SSH
helper; they can be run by an operator over SSH, or by re-enabling the agent's SSH access.

Production currently runs **v3.1.106** (git HEAD `356fde7`). Target: **v3.1.108**
(the fully-tested build now live on S1 `89.116.34.207`). The deploy ships the **complete
v3.1.108 source from S1** (not a cherry-pick) so every file is version-consistent.

> All commands run as `root` on the production server unless noted. Replace `<S1>` with
> `89.116.34.207`. The production `.env`, `bin/`, Mongo data, and MySQL data are preserved.

## 0. Pre-flight (already done by the agent, repeat to be safe)
```bash
BK=/opt/serverpanel/_pre108_backup; mkdir -p "$BK"; TS=$(date +%Y%m%d%H%M%S)
cp -a /opt/serverpanel/bin "$BK/bin.$TS"
cp -a /opt/serverpanel/.env "$BK/env.$TS"
cp -a /etc/nginx/sites-available/serverpanel "$BK/nginx.$TS" 2>/dev/null
postconf -h header_checks > "$BK/header_checks.$TS" 2>/dev/null
URI=$(awk -F= '/^MONGO_URI=/{sub(/^MONGO_URI=/,"");print}' /opt/serverpanel/.env)
mongodump --uri="$URI" --gzip --archive="$BK/mongo.$TS.gz"
mysqldump --all-databases | gzip > "$BK/mysql-all.$TS.sql.gz"
```

## 1. Get the v3.1.108 source onto production
On **S1** package the source (no node_modules/dist/.git/.env):
```bash
cd /opt/serverpanel
tar czf /tmp/sp108-src.tgz --exclude=node_modules --exclude=dist \
  --exclude=.git --exclude='*.env' --exclude='.env*' backend frontend
```
Copy `/tmp/sp108-src.tgz` from S1 to production `/tmp/` (scp/rsync/sftp), then on **production**:
```bash
cd /opt/serverpanel
# git-track the current local script edits so they aren't lost, then lay down 108 source
git stash push -m pre-108 -- scripts/ 2>/dev/null || true
tar xzf /tmp/sp108-src.tgz -C /opt/serverpanel      # overwrites backend/ + frontend/ SOURCE only
```
`.env`, `bin/`, `frontend/**/node_modules`, `frontend/**/dist`, and all data are untouched.

## 2. Build the backend (Go 1.23 present on prod)
```bash
cd /opt/serverpanel/backend
CGO_ENABLED=0 /opt/go/bin/go build -trimpath -ldflags="-s -w" -o /opt/serverpanel/bin/server.new ./cmd/server
# only swap if the build succeeded:
[ -f /opt/serverpanel/bin/server.new ] && { cp -a /opt/serverpanel/bin/server /opt/serverpanel/bin/server.bak.$(date +%s); mv /opt/serverpanel/bin/server.new /opt/serverpanel/bin/server; }
systemctl restart serverpanel
sleep 4; systemctl is-active serverpanel; curl -s localhost:8080/api/v1/version
```
Expect `"version":"3.1.108"`.

## 3. Build the frontend (Node present on prod; node_modules reused)
```bash
cd /opt/serverpanel/frontend
npm run build           # turbo build → writes apps/whm/dist + apps/cpanel/dist in place
```
The Go server serves the new `dist/` immediately (index.html is no-store; assets are hashed).

## 4. Verify (production, with real data — confirm NOTHING was lost)
```bash
TOKEN=$(curl -s localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"<owner-email>","password":"<owner-pw>"}' | sed -E 's/.*"access_token":"([^"]+)".*/\1/')
# data intact:
curl -s localhost:8080/api/v1/whm/databases -H "Authorization: Bearer $TOKEN" | head -c 300
# mongo counts unchanged (expect domains=192, mailboxes=32, users=33, etc.)
mongosh "$URI" --quiet --eval 'print("domains="+db.domains.countDocuments()+" mailboxes="+db.mailboxes.countDocuments()+" users="+db.users.countDocuments())'
# MongoDB creation now works (the reported issue):
curl -s localhost:8080/api/v1/whm/databases -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"db_name":"probe","type":"mongodb","username":"probeuser","password":"ProbePass123!"}'
# mail-log route registered (expect 401 unauth, NOT 404):
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/api/v1/whm/email/logs
# header_checks must be regexp (NEVER pcre — pcre temp-fails all mail on stock Postfix):
postconf -h header_checks    # → regexp:/etc/postfix/header_checks_betazen
# mail still flows:
echo test | mail -s 'post-deploy probe' postmaster@localhost; tail -5 /var/log/mail.log
```
Then delete the probe DB you just created.

## 5. Rollback (if anything is wrong)
```bash
systemctl stop serverpanel
cp -a /opt/serverpanel/_pre108_backup/bin.<TS>/server /opt/serverpanel/bin/server
cd /opt/serverpanel && git checkout -- backend frontend && git clean -fd backend frontend
systemctl start serverpanel
# header_checks revert if needed:
postconf -e header_checks="$(cat /opt/serverpanel/_pre108_backup/header_checks.<TS>)"; postfix reload
# data restore only if needed (it should NOT be):
# mongorestore --uri="$URI" --gzip --archive=/opt/serverpanel/_pre108_backup/mongo.<TS>.gz --drop
```

## Notes specific to production
- Production has a REAL domain + (likely) a Let's Encrypt cert + :443. v3.1.108 includes the
  v3.1.107 "wrong-cert/server_name pollution" fix — beneficial, but eyeball the panel vhost
  after deploy (`nginx -T`, `curl -I https://panel.betazeninfotech.com/`).
- The mail-log ingestor will start tailing `/var/log/mail.log` and writing the new `mail_logs`
  collection; `EnsureHeaderChecks` adds a **regexp** Subject/Content-Type map (validated +
  auto-rollback). This was hardened after the pcre incident on S1 (see CHANGELOG).
- Boot-time idempotent backfills (`EnsureIndexes`, `BackfillTenantIDs`, `DedupEmailForwarders`,
  etc.) run on first 108 start — safe on existing data.
- The SQLi-hardening in `agent/mysql.go` whitelists db/user identifiers to `^[A-Za-z0-9_]{1,64}$`.
  All panel-created DB names are vendor-prefixed alphanumerics, so existing DBs are unaffected;
  any pre-existing DB with an unusual name (hyphen/dot) would be rejected on future *management*
  ops — none observed in the inventory.
