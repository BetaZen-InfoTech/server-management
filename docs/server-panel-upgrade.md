# ServerPanel Upgrade Guide

Safe, reversible procedure for upgrading an existing ServerPanel installation to a newer version of the backend and frontend. For a **fresh install**, use [`install.sh`](../install.sh). For **migrating an existing VPS into ServerPanel**, see [`server-transfer.md`](./server-transfer.md).

---

## TL;DR — Routine Patch

For a minor patch with no schema or env changes:

```bash
cd /opt/serverpanel
git fetch --tags
git status --porcelain                              # must be empty
sudo /opt/serverpanel/scripts/upgrade.sh            # if the script is present
# — or the manual steps below —
```

The [**Staged Upgrade**](#staged-upgrade-procedure) below is safer for every other scenario.

---

## When to use which path

| Scenario | Use |
|---|---|
| Bug-fix commit, no new dependencies, no schema touched | [Routine one-liner](#routine-one-liner) |
| New backend env var, new Mongo index, new frontend dependency, new agent binary | [Staged upgrade](#staged-upgrade-procedure) |
| Cross-major upgrade (v1.x → v2.x), breaking API change, migration required | [Staged upgrade](#staged-upgrade-procedure) + read the release notes for the target tag |
| Upgrade went wrong and the panel is broken | [Rollback](#rollback) |

---

## Before you start

### 1. Announce a maintenance window

Upgrades involve a `systemctl restart serverpanel` — typical downtime is 5–30 seconds for the restart itself, plus the frontend rebuild time (~20–60 s) while clients load the new bundle. Deployed apps (`/apps`) and Deploy Software projects keep running; only the control panel is unavailable.

### 2. Check current state

```bash
# What's running right now
systemctl is-active serverpanel nginx mongod mariadb
cd /opt/serverpanel && git log --oneline -1

# What's on the remote
git fetch --tags
git log --oneline HEAD..origin/main | head -20

# Disk + memory headroom (a full build needs ~1 GB free)
df -h /opt /tmp
free -h
```

### 3. Snapshot the risky bits

```bash
sudo /opt/serverpanel/scripts/pre-upgrade-backup.sh   # if present
# — or manually —
TS=$(date +%Y%m%d-%H%M%S)
sudo mkdir -p /var/backups/serverpanel/$TS
sudo cp /opt/serverpanel/.env                /var/backups/serverpanel/$TS/
sudo cp /opt/serverpanel/bin/server          /var/backups/serverpanel/$TS/server.bak
sudo cp -r /opt/serverpanel/frontend/apps/whm/dist /var/backups/serverpanel/$TS/whm-dist
sudo cp -r /opt/serverpanel/frontend/apps/cpanel/dist /var/backups/serverpanel/$TS/cpanel-dist
sudo tar czf /var/backups/serverpanel/$TS/mongo-dump.tar.gz -C /tmp \
  "$(mongodump --uri="$(grep ^MONGO_URI= /opt/serverpanel/.env | cut -d= -f2-)" \
     --out=/tmp/sp-dump-$TS --gzip >/dev/null && echo sp-dump-$TS)"
echo "Backup: /var/backups/serverpanel/$TS"
```

What this snapshot covers:

- `.env` — the only mutable config on disk that's genuinely irreplaceable (encryption key, JWT secret, Mongo password).
- `bin/server` — one-line rollback if the new binary crashes.
- `frontend/apps/{whm,cpanel}/dist` — instant serve-the-old-bundle rollback without a rebuild.
- Mongo dump — all users, apps, domains, projects, SSL records, DNS zones.

---

## Staged Upgrade Procedure

The cardinal rule: **never overwrite the running binary or the served frontend bundle until the new one has been successfully built AND tested.**

### Step 1 — Fetch, don't pull

```bash
cd /opt/serverpanel
git fetch origin main
git log --oneline HEAD..origin/main            # review what's incoming
```

Stop here and read the commit messages. Anything mentioning migrations, env vars, new dependencies, or breaking changes → read the relevant commit's message body.

### Step 2 — Check for new env vars

```bash
# What does .env.example define that .env doesn't?
comm -23 \
  <(grep -E '^[A-Z_]+=' .env.example  | cut -d= -f1 | sort) \
  <(grep -E '^[A-Z_]+=' .env          | cut -d= -f1 | sort)
```

Common additions between versions:

| Variable | Why you might need it |
|---|---|
| `APP_ENCRYPTION_KEY` | AES-GCM key for encrypting Deploy Software PATs at rest. Required in production; if missing, PATs reset on every panel restart. Generate with `openssl rand -hex 32`. |
| `PUBLIC_WEBHOOK_BASE_URL` | Used to build the copy-paste webhook URL shown in the Deploy Software UI. Defaults to `https://<DOMAIN>` if unset. |
| `SERVER_IP` | Used for DNS A records + DNS pre-flight checks. Falls back to `hostname -I` if unset. |
| `BACKUP_ENCRYPTION_KEY` | Encrypts backup archives. |

Add missing ones to `.env` **before** rebuilding:

```bash
sudo nano /opt/serverpanel/.env
sudo chmod 600 /opt/serverpanel/.env         # make sure perms stay tight
```

### Step 3 — Check out the new commit

```bash
cd /opt/serverpanel
sudo git -c safe.directory=/opt/serverpanel reset --keep origin/main
# `reset --keep` refuses to run if it would discard uncommitted local changes,
# so a dirty working tree (from e.g. a live hotfix) fails loud instead of
# silently losing work.
```

If `reset --keep` complains about local changes, either commit them, stash them, or run `git status --porcelain` to find them.

### Step 4 — Build the backend out-of-tree

```bash
cd /opt/serverpanel/backend
/opt/go/1.23/bin/go build -o /opt/serverpanel/bin/server.new ./cmd/server
/opt/go/1.23/bin/go build -o /opt/serverpanel/bin/seed.new   ./cmd/seed
```

Building to `server.new` (not `server`) means:

- A compile error leaves the running binary untouched; you can keep serving traffic while you fix.
- The atomic swap in step 6 is just a `mv` — no downtime window where the file is half-written.

If the build fails, stop here. Read the Go error, fix it (usually a new missing env or a missed migration); `server` is still the old binary and the panel is still up.

### Step 5 — Build the frontend out-of-tree

```bash
cd /opt/serverpanel/frontend
npm install --legacy-peer-deps
npx turbo build
```

Turbo writes to `frontend/apps/whm/dist` and `frontend/apps/cpanel/dist` — these are the directories nginx serves. The build is atomic *per file* (Vite writes to a temp then renames), so in practice you can run this while the panel is serving traffic. A failed build leaves a hybrid dist dir though, so:

```bash
# Snapshot the new dist dirs to a staging path that nginx isn't serving:
TS=$(date +%Y%m%d-%H%M%S)
sudo cp -r frontend/apps/whm/dist    /opt/serverpanel/frontend/apps/whm/dist.new
sudo cp -r frontend/apps/cpanel/dist /opt/serverpanel/frontend/apps/cpanel/dist.new
```

This is belt-and-suspenders for big refactors. Routine patches can skip it.

### Step 6 — Atomic swap

```bash
# Backend: swap binaries, restart the service
sudo mv /opt/serverpanel/bin/server      /opt/serverpanel/bin/server.old
sudo mv /opt/serverpanel/bin/server.new  /opt/serverpanel/bin/server
sudo mv /opt/serverpanel/bin/seed        /opt/serverpanel/bin/seed.old
sudo mv /opt/serverpanel/bin/seed.new    /opt/serverpanel/bin/seed

sudo systemctl restart serverpanel

# Verify it came up:
sleep 2
sudo systemctl is-active serverpanel || journalctl -u serverpanel -n 40 --no-pager
curl -sf http://127.0.0.1:8080/api/v1/health >/dev/null && echo "backend ok"

# Frontend (only if you staged dist.new in step 5):
sudo mv /opt/serverpanel/frontend/apps/whm/dist /opt/serverpanel/frontend/apps/whm/dist.old
sudo mv /opt/serverpanel/frontend/apps/whm/dist.new /opt/serverpanel/frontend/apps/whm/dist
sudo mv /opt/serverpanel/frontend/apps/cpanel/dist /opt/serverpanel/frontend/apps/cpanel/dist.old
sudo mv /opt/serverpanel/frontend/apps/cpanel/dist.new /opt/serverpanel/frontend/apps/cpanel/dist
```

No nginx reload needed — it reads files from disk per request.

### Step 7 — Verify

```bash
# Backend health
curl -sf http://127.0.0.1:8080/api/v1/health

# Version endpoint reflects the new commit
curl -s http://127.0.0.1:8080/api/v1/version | jq .

# Logs clean (no fatal on startup, no panic recovery)
sudo journalctl -u serverpanel --since "2 minutes ago" --no-pager | tail -40

# Deployed services still running
systemctl list-units --all 'sp-app-*' 'sp-proj-*' --no-pager --no-legend | head
```

Hit `https://<your-panel-domain>/whm` in a browser with a **hard reload** (Ctrl+Shift+R / Cmd+Shift+R) to bypass the cached bundle. Visit the pages most likely to have changed — Dashboard, Apps, Deploy Software — and spot-check that nothing 404s or 500s.

### Step 8 — Clean up old artefacts (after a day or two)

Keep `.old` files around for at least 24 hours as a known-good rollback target. After you're confident:

```bash
sudo rm -f /opt/serverpanel/bin/server.old /opt/serverpanel/bin/seed.old
sudo rm -rf /opt/serverpanel/frontend/apps/whm/dist.old
sudo rm -rf /opt/serverpanel/frontend/apps/cpanel/dist.old
# Keep the /var/backups/serverpanel/<TS>/ snapshot — it's tiny and useful.
```

---

## Routine one-liner

When you've verified the incoming commits are safe (no new env vars, no dependency changes) and you don't need a build-failure safety net:

```bash
cd /opt/serverpanel && \
  git fetch origin main && git -c safe.directory=/opt/serverpanel reset --keep origin/main && \
  (cd backend  && /opt/go/1.23/bin/go build -o ../bin/server ./cmd/server) && \
  (cd frontend && npm install --legacy-peer-deps && npx turbo build) && \
  sudo systemctl restart serverpanel && \
  sleep 2 && \
  sudo systemctl is-active serverpanel && \
  curl -sf http://127.0.0.1:8080/api/v1/health >/dev/null && echo "OK"
```

The `&&` chain makes every step depend on the previous one succeeding. If anything fails, the chain stops and the restart doesn't happen.

---

## Rollback

If the upgrade is broken and you still have the `.old` artefacts:

```bash
cd /opt/serverpanel
sudo systemctl stop serverpanel
sudo mv bin/server.old bin/server              # restore binary
sudo mv bin/seed.old   bin/seed
sudo mv frontend/apps/whm/dist    frontend/apps/whm/dist.broken     # optional
sudo mv frontend/apps/whm/dist.old frontend/apps/whm/dist
sudo mv frontend/apps/cpanel/dist frontend/apps/cpanel/dist.broken
sudo mv frontend/apps/cpanel/dist.old frontend/apps/cpanel/dist
git -c safe.directory=/opt/serverpanel reset --hard HEAD~1          # or specific SHA
sudo systemctl start serverpanel
```

If you only kept the `/var/backups/serverpanel/<TS>/` snapshot:

```bash
TS=<your-timestamp>
sudo cp /var/backups/serverpanel/$TS/server.bak /opt/serverpanel/bin/server
sudo cp /var/backups/serverpanel/$TS/.env       /opt/serverpanel/.env
sudo rm -rf /opt/serverpanel/frontend/apps/whm/dist /opt/serverpanel/frontend/apps/cpanel/dist
sudo cp -r /var/backups/serverpanel/$TS/whm-dist    /opt/serverpanel/frontend/apps/whm/dist
sudo cp -r /var/backups/serverpanel/$TS/cpanel-dist /opt/serverpanel/frontend/apps/cpanel/dist
# Restore Mongo only if schema actually changed — the old dump is useless for data newer than the upgrade:
# mongorestore --uri="..." --drop --gzip /var/backups/serverpanel/$TS/mongo-dump.tar.gz/
sudo systemctl restart serverpanel
```

**Important**: a Mongo restore rolls back *all* user-facing data (domains added after the upgrade, deploys created, users signed up). Only use it when a schema migration actually corrupted data — otherwise stick to the binary + .env rollback.

---

## Database migrations

ServerPanel's models evolve over time. The backend applies idempotent migrations at startup:

- **Tenant backfill** (`services.BackfillTenantIDs`) — sets `tenant_id` on legacy user records so vendor scoping has data to filter on. Runs on every boot, no-op after first.
- **Index creation** (`database.EnsureIndexes`) — creates collection indexes if missing; idempotent. Warnings for existing indexes are logged but not fatal.
- **Ad-hoc backfills** — individual commits occasionally add backfills (e.g. generating webhook secrets for older app records). These log `{"level":"info","migrated":N}` on startup.

To see what ran during a restart:

```bash
sudo journalctl -u serverpanel --since "5 minutes ago" --no-pager | grep -iE "migrat|backfill|index|ensure"
```

If a migration fails you'll see a fatal log message and the service won't start. Common causes:

| Symptom | Fix |
|---|---|
| `Expression not supported in partial index: $not` | Partial-index expression using `$ne`; the binary you're rolling out has the fix. |
| `APP_ENCRYPTION_KEY is required in production` | Add the env var and restart. Earlier versions made this fatal; current versions warn and use an ephemeral key. |
| `index already exists with different options` | Drop the old index manually: `mongosh "$MONGO_URI" --eval 'db.<collection>.dropIndex("<name>")'` and restart. |

---

## Dependency changes

### Go dependency bump

If `backend/go.mod` or `go.sum` changed, `go build` will automatically fetch the new versions. No manual step needed.

### Node dependency bump

If `frontend/package.json` or any app's `package.json` changed, `npm install --legacy-peer-deps` picks them up. If you see errors like `peer dependency not satisfied`, check the diff for a known-good peer range.

### System package bump

If a commit adds a new apt package (e.g. a new email-stack component), the installer (`install.sh`) will install it on a fresh server but **won't** run on an upgrade. Read the commit message for manual `apt install` steps.

---

## Post-upgrade checklist

- [ ] `systemctl is-active serverpanel` → `active`
- [ ] `curl -sf http://127.0.0.1:8080/api/v1/health` → 200
- [ ] `curl -s http://127.0.0.1:8080/api/v1/version` → new version
- [ ] Browse to WHM, hard-reload, log in
- [ ] Spot-check the pages you actually use (Apps / Deploy Software / Domains)
- [ ] `systemctl list-units 'sp-app-*' 'sp-proj-*' --failed` is empty
- [ ] `nginx -t` passes, no reload needed unless explicitly required
- [ ] Auto-SSL / certbot timer still scheduled: `systemctl list-timers | grep certbot`
- [ ] `/var/backups/serverpanel/<TS>/` snapshot retained for at least 24 h

---

## Common issues after an upgrade

| Symptom | Likely cause | Fix |
|---|---|---|
| Panel 502s after restart | Backend crashed on startup. | `journalctl -u serverpanel -n 40` — usually a missing env var. |
| "Please log in" loop | JWT secret changed (e.g. you regenerated `.env`). | Use the old secret, OR accept that all sessions are invalidated. |
| Deploy Software PATs all reset | `APP_ENCRYPTION_KEY` changed or unset. | Re-paste PATs via the UI's Rotate button; persist the key in `.env` before next restart. |
| `/apps` list empty | Tenant scoping mismatch — user's `tenant_id` is missing. | Run the panel once; `BackfillTenantIDs` fills it on next restart. |
| `sp-proj-*` / `sp-app-*` service crash-loops | Hardcoded port mismatch or missing entry file. | The new builds surface this via the BuildErrorModal. Click Logs for the actual stack trace. |
| Frontend shows old version | Browser cached the hashed bundle. | Hard-reload (Ctrl+Shift+R). The index.html has `Cache-Control: no-store` so a hard reload is enough. |
| `nginx -t` fails after deploy | An nginx config was generated against an older template. | `systemctl reload nginx` loads the latest files; test with `nginx -T` to see the merged config. |

---

## Upgrading a remote server you don't have interactive access to

Use the panel's built-in update flow (if available) or a deploy key on the target:

```bash
ssh root@<target> 'cd /opt/serverpanel && \
  git fetch origin main && \
  git -c safe.directory=/opt/serverpanel reset --keep origin/main && \
  cd backend  && /opt/go/1.23/bin/go build -o ../bin/server.new ./cmd/server && \
  cd ../frontend && npm install --legacy-peer-deps && npx turbo build && \
  cd .. && mv bin/server bin/server.old && mv bin/server.new bin/server && \
  systemctl restart serverpanel && \
  sleep 2 && systemctl is-active serverpanel && curl -sf http://127.0.0.1:8080/api/v1/health'
```

The staged-swap pattern (`server.new` → `server`) means if any step fails, the panel stays up with the old binary.

---

## Scheduling upgrades

Recommended cadence:

- **Security patches** — apply same day, via the routine one-liner if confirmed safe.
- **Minor version bumps** — weekly or bi-weekly window, use the staged procedure.
- **Major version bumps** — read release notes, announce a window, staged procedure + Mongo dump.

A simple cron + Slack/email notifier works well:

```cron
# /etc/cron.d/serverpanel-check
0 9 * * 1  root  cd /opt/serverpanel && git fetch origin main >/dev/null 2>&1 && \
  count=$(git log --oneline HEAD..origin/main | wc -l) && \
  [ "$count" -gt 0 ] && echo "ServerPanel has $count new commits pending" | mail -s "Upgrade available" admin@betazeninfotech.com
```

Don't auto-upgrade: the control plane for every tenant's infra is not something to upgrade unattended.
