# MongoDB 7.0 â†’ 8.0 Upgrade Runbook (Betazen Panel)

> **Scope:** Self-hosted, **standalone** (NOT replica set / NOT Atlas / NOT sharded), apt-managed `mongodb-org`, auth **ON**, Ubuntu 24.04 "noble" x86_64. Single major step **7.0 â†’ 8.0**.
> **Hosts:** S1 = `89.116.34.207` (staging) Â· S2 = `195.35.7.64` (staging) Â· **PROD = `195.35.7.161`** (`panel.betazeninfotech.com`, real data).
> **Run order is mandatory:** complete **S1 end-to-end, then S2 end-to-end, then PROD last.** Never make PROD the first attempt.
> **Run everything as `root`.** Commands are exact and copy-paste ready.

---

## 0) Summary, Go/No-Go & Expected Downtime

### What we are doing and why it is dangerous

We swap the apt `mongodb-org` 7.0 binaries for 8.0 in place, restart `mongod`, **soak** on 8.0 with the panel verified healthy, and **only then** raise `featureCompatibilityVersion` (FCV) to `8.0` as a **separate, deliberate, gated step**.

Three hard MongoDB rules drive every decision below:

1. **You cannot skip a major.** 7.0 â†’ 8.0 is one step; 6.x must reach 7.0 first (separate runbook).
2. **FCV must equal `7.0` BEFORE swapping binaries.** If FCV is below 7.0, the 8.0 binary **refuses to start** â†’ unbootable `mongod` â†’ total panel outage.
3. **Raising FCV to `8.0` is the point of no cheap return.** Once FCV is `8.0`, **7.0 binaries can no longer open the data files** â€” binary-only rollback is impossible and rollback then requires a **dump restore**.

> âš ï¸ **The panel's `install.sh` upgrade path is NOT safe to run unmodified.** It (a) swaps binaries with **no FCV=7.0 precondition** (`install.sh:429/435/447`), (b) **deletes the old repo `.list` before** confirming the 8.0 candidate is fetchable (`install.sh:425`), and (c) raises FCV to 8.0 **automatically in the same run** via a best-effort/non-fatal call (`install.sh:579-582`, ends in `|| warn`). This runbook performs the upgrade **manually and gated**; do **not** lean on `install.sh` to do the cutover. Patch hand-offs for `install.sh` are listed in Â§6.

### Coupling you must respect

- `serverpanel` systemd unit has `Requires=mongod.service` (`install.sh:1695`), so **`mongod` downtime == panel downtime**. The panel is unreachable during the restart.
- Both Mongo users (`admin`=root, `serverpanel`=readWrite+dbAdmin on `serverpanel`) share the same password, read from `/opt/serverpanel/.env` `MONGO_PASS=` (`install.sh:508-521, 1585-1586`). `install.sh` generates this password as `openssl rand -hex 16` (32 hex chars, **no** quotes/special chars) and writes it **unquoted** in `.env` (`install.sh:1585`), so the quote-stripping in the loaders below is a safe no-op for this deployment.

### Expected downtime

| Phase | Panel down? | Duration |
|---|---|---|
| Pre-flight (read-only) | No | ~3â€“5 min |
| Backup â€” logical dump + verify (panel quiesced) | **Yes** | ~3â€“10 min (small dataset) |
| Backup â€” cold filesystem copy (`mongod` stopped) | **Yes** | ~1â€“3 min |
| Binary swap + restart on 8.0 | **Yes** | ~1â€“3 min |
| **Soak** on 8.0 (panel back up, FCV still 7.0) | No | **â‰¥ 30 min** (longer on PROD if possible) |
| FCV raise to 8.0 (online command) | No | ~seconds |

**Planned PROD maintenance window: book ~30 minutes of panel downtime** (backup + swap), then a soak with the panel live. Dataset is small (~192 domains, 32 mailboxes, 33 users, 189 SSL/FTP, 17 projects), so wall-clock is dominated by deliberate gates, not data volume.

### Go / No-Go checklist (ALL must be true to proceed past pre-flight)

- [ ] **Topology:** standalone â€” `rs.status()` errors, `replication=null`, `sharding=null`, `mongod` not `mongos`. (Â§1)
- [ ] **Version:** `mongod --version` is exactly `v7.0.x`. (Â§1)
- [ ] **Package source:** apt `mongodb-org` 7.0.x; **no** snap mongo; **no** distro `mongodb` pkg. (Â§1)
- [ ] **No apt holds** on `mongodb-org*` (or noted for unhold). (Â§1)
- [ ] **Auth:** **both** `admin` (root) and `serverpanel` authenticate with the `.env` password. (Â§1)
- [ ] **FCV == `7.0`** (raised to 7.0 on the running 7.0 binary first if it was lower, then re-verified). (Â§1)
- [ ] **8.0 noble repo Release is reachable** (proven before any repo file is touched). (Â§1)
- [ ] **Disk:** dump target has â‰¥ 1.5Ã— data-dir free **and** `/var/lib/mongodb` has headroom. (Â§1)
- [ ] **Baseline** doc counts captured to disk and headline counts match PROD inventory. (Â§1)
- [ ] **Verified, restorable backup** exists: gzip `mongodump` archive + sha256 + passing `--dryRun` + restore-into-throwaway count match, plus a cold data-dir copy. (Â§2)
- [ ] **S1 then S2 completed end-to-end** before PROD. (Â§3)

Any unchecked box â‡’ **NO-GO.** Stop and resolve.

---

## 1) Pre-flight & Detection

> Read-only. Changes **no** binaries and does **not** raise FCV. Any **GATE FAILED** â‡’ **STOP**.
> Where the original draft only *recommended* a check, this runbook makes it a **fail-closed gate** â€” safer, because a soft "WARN" lets an operator proceed into an unbootable state.

```bash
#!/usr/bin/env bash
# ===== MongoDB 7.0 -> 8.0 PRE-FLIGHT (run as root). Read-only. =====
set -Eeuo pipefail
fail(){ echo "GATE FAILED: $*" >&2; exit 1; }

# ---- 0. Load MONGO_PASS from the panel .env (never echo the value) ----
ENV_FILE="/opt/serverpanel/.env"
[ -r "$ENV_FILE" ] || fail "$ENV_FILE not readable"
MONGO_PASS="$(grep -E '^MONGO_PASS=' "$ENV_FILE" | head -1 | cut -d= -f2- | tr -d '"'\''')"
[ -n "$MONGO_PASS" ] || fail "MONGO_PASS empty/not found in $ENV_FILE"
echo "MONGO_PASS loaded (${#MONGO_PASS} chars)"   # install.sh default is openssl rand -hex 16 => 32 chars
grep -E '^(MONGO_URI|MONGO_DB_NAME)=' "$ENV_FILE" || true
# If MONGO_DB_NAME != serverpanel, substitute it in every 'serverpanel'-db command below.

AUTH_ADMIN=(--quiet -u admin       -p "$MONGO_PASS" --authenticationDatabase admin)
AUTH_PANEL=(--quiet -u serverpanel -p "$MONGO_PASS" --authenticationDatabase admin)

# ---- 1. Record current state VERBATIM (for the rollback note) ----
echo "== versions =="; mongod --version | head -1
dpkg -l | grep -E '^ii\s+mongodb' || true
echo "== data dir baseline =="; stat -c '%U:%G %a %n' /var/lib/mongodb /var/log/mongodb; id mongodb
echo "== AppArmor =="; aa-status 2>/dev/null | grep -i mongod || echo "no mongod apparmor profile"
echo "== THP =="; cat /sys/kernel/mm/transparent_hugepage/enabled 2>/dev/null || true

# ---- 2. GATE: apt mongodb-org, NOT snap / distro 'mongodb' ----
command -v mongod >/dev/null || fail "mongod not found"
dpkg -s mongodb-org >/dev/null 2>&1 || fail "mongodb-org metapackage not installed via apt"
if command -v snap >/dev/null && snap list 2>/dev/null | grep -qi mongo; then
  fail "a snap-installed mongo is present â€” resolve before upgrading"
fi
dpkg -l 2>/dev/null | grep -E '^ii\s+mongodb\s' && fail "distro 'mongodb' pkg present â€” conflicts with mongodb-org" || true

# ---- 3. GATE: current major is EXACTLY 7.0 (cannot skip majors) ----
# Same regex install.sh:417 uses, so detection here and in the installer agree.
CUR_MAJOR="$(mongod --version 2>/dev/null | sed -n 's/.*db version v\([0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | head -1)"
echo "Detected MongoDB major: ${CUR_MAJOR:-NONE}"
case "$CUR_MAJOR" in
  7.0) echo "OK: on 7.0 â€” single major step to 8.0 is valid." ;;
  6.*) fail "on 6.x. You CANNOT skip to 8.0. Upgrade 6.x->7.0 first (separate runbook)." ;;
  8.*) fail "already on 8.x. This upgrade is a no-op; do not re-run." ;;
  *)   fail "unexpected/unparseable version '$CUR_MAJOR'. Investigate." ;;
esac

# ---- 4. GATE: mongod active ----
systemctl is-active --quiet mongod || fail "mongod not active â€” fix before upgrading"

# ---- 5. GATE: BOTH admin and serverpanel can authenticate (stale MONGO_PASS trap) ----
mongosh "${AUTH_ADMIN[@]}" --eval "db.adminCommand('ping')" >/dev/null \
  || fail "admin auth FAILED â€” MONGO_PASS stale vs mongod (post-upgrade setFCV would fail)"
mongosh "${AUTH_PANEL[@]}" --eval "db.adminCommand('ping')" >/dev/null \
  || fail "serverpanel auth FAILED â€” MONGO_PASS stale vs mongod (panel would not connect)"
mongosh "${AUTH_ADMIN[@]}" --eval \
  'printjson(db.adminCommand({connectionStatus:1}).authInfo.authenticatedUserRoles)'   # expect role:'root', db:'admin'
echo "OK: auth verified for admin + serverpanel."

# ---- 6. GATE: standalone (NOT replica set, NOT sharded) ----
if mongosh "${AUTH_ADMIN[@]}" --eval "rs.status()" 2>/dev/null | grep -q '"set"'; then
  fail "this mongod is in a replica set â€” this runbook is standalone-only"
fi
mongosh "${AUTH_ADMIN[@]}" --eval \
  'const c=db.serverCmdLineOpts().parsed||{}; print("replication="+JSON.stringify(c.replication||null)); print("sharding="+JSON.stringify(c.sharding||null));'
# expect: replication=null  sharding=null
pgrep -a mongos && fail "mongos is running â€” sharded topology, not standalone" || echo "no mongos (good)"
grep -E '^\s*bindIp' /etc/mongod.conf || true   # expect 127.0.0.1 (or 127.0.0.1,::1). 0.0.0.0 => security note, not a blocker.

# ---- 7. GATE: FCV is EXACTLY 7.0 (the unbootable-8.0 trap) ----
FCV="$(mongosh "${AUTH_ADMIN[@]}" --eval \
  'print(db.adminCommand({getParameter:1,featureCompatibilityVersion:1}).featureCompatibilityVersion.version)' | tr -d '[:space:]')"
echo "Current FCV = ${FCV}"
case "$FCV" in
  7.0) echo "OK: FCV is 7.0 â€” safe to swap binaries to 8.0." ;;
  8.0) fail "FCV already 8.0 â€” binaries were likely already upgraded. Re-confirm 'mongod --version'." ;;
  *)   echo "STOP: FCV is '${FCV}', not 7.0. The 8.0 binary will REFUSE to start."
       echo "REMEDIATION (run ONLY while still on the 7.0 binary, then re-run pre-flight):"
       echo "  mongosh -u admin -p <PASS> --authenticationDatabase admin --eval \\"
       echo "    \"db.adminCommand({setFeatureCompatibilityVersion:'7.0', confirm:true})\""
       fail "FCV not 7.0" ;;
esac

# ---- 8. GATE: no apt holds on mongo packages ----
HELD="$(apt-mark showhold | grep -i '^mongodb' || true)"
[ -z "$HELD" ] || fail "apt holds present: $HELD â€” 'apt-mark unhold' them or the upgrade silently no-ops"

# ---- 9. GATE: 8.0 noble repo is reachable BEFORE any repo file is touched ----
CODENAME="$(lsb_release -cs)"
# MongoDB 8.0 DOES publish a 'noble' (24.04) apt repo (7.0 did not â€” that is why
# older installs force-mapped noble->jammy). On these hosts 'noble' is EXPECTED
# and correct; anything else means a non-24.04 box â€” confirm the repo path.
[ "$CODENAME" = "noble" ] || echo "WARN: codename is $CODENAME (expected noble) â€” confirm the 8.0 repo publishes for it"
curl -fsI "https://repo.mongodb.org/apt/ubuntu/dists/${CODENAME}/mongodb-org/8.0/Release" >/dev/null \
  || fail "MongoDB 8.0 ${CODENAME} repo Release unreachable (mirror down or egress blocked)"
echo "OK: 8.0/${CODENAME} repo reachable."

# ---- 10. GATE: tooling present (mongosh + database-tools) ----
command -v mongosh >/dev/null || fail "mongosh missing"
command -v mongodump >/dev/null || { echo "mongodump missing â€” installing mongodb-database-tools"; apt-get install -y mongodb-database-tools; }
command -v mongorestore >/dev/null || fail "mongorestore missing"

# ---- 11. GATE: disk headroom (dump target >= 1.5x data; data partition has slack) ----
DUMP_TARGET="/var/backups/mongo-upgrade"; mkdir -p "$DUMP_TARGET"
DATA_KB=$(du -sk /var/lib/mongodb | awk '{print $1}')
FREE_KB=$(df -Pk "$DUMP_TARGET" | awk 'NR==2{print $4}')
NEED_KB=$(( DATA_KB * 3 / 2 ))
echo "data=${DATA_KB}KB free@dump=${FREE_KB}KB need>=${NEED_KB}KB"
[ "$FREE_KB" -ge "$NEED_KB" ] || fail "insufficient free space on dump target ($(df -Pk "$DUMP_TARGET" | awk 'NR==2{print $6}'))"
FREE_DATA_KB=$(df -Pk /var/lib/mongodb | awk 'NR==2{print $4}')
[ "$FREE_DATA_KB" -ge $(( DATA_KB / 5 )) ] || fail "low headroom on /var/lib/mongodb partition (WT/journal could wedge)"
if [ "$(df -P /var/lib/mongodb | awk 'NR==2{print $1}')" = "$(df -P "$DUMP_TARGET" | awk 'NR==2{print $1}')" ]; then
  echo "WARN: dump target shares the data partition â€” prefer a separate disk/volume."
fi

# ---- 12. Baseline doc counts (verify step diffs against this) ----
BASELINE=/root/mongo-preupgrade-baseline.json
mongosh "${AUTH_PANEL[@]}" serverpanel --eval '
  const out = { capturedAt: new Date().toISOString(), db: db.getName(), collections: {} };
  let total = 0;
  db.getCollectionNames().sort().forEach(function(c){
    const n = db.getCollection(c).countDocuments({});   // exact, not estimated
    out.collections[c] = n; total += n;
  });
  out.totalDocs = total;
  print(JSON.stringify(out, null, 2));
' | tee "$BASELINE"
# Sanity-check the file is pure JSON before Â§5 trusts it (--quiet keeps mongosh from prepending a banner):
python3 -c "import json,sys; json.load(open('$BASELINE')); print('baseline JSON OK')" \
  || fail "baseline file is not clean JSON â€” inspect $BASELINE (a stray banner line will break Â§5's JSON.parse)"

mongosh "${AUTH_PANEL[@]}" serverpanel --eval \
  'const s=db.stats(); print(JSON.stringify({collections:s.collections,objects:s.objects,dataSize:s.dataSize,storageSize:s.storageSize,indexes:s.indexes},null,2));' \
  | tee /root/mongo-preupgrade-dbstats.json
ls -l "$BASELINE" /root/mongo-preupgrade-dbstats.json

echo "=== PRE-FLIGHT PASSED on $(hostname) ($(hostname -I | awk '{print $1}')). Box UNCHANGED, still 7.0. ==="
```

> **Cross-check the headline counts on PROD** against the verified inventory: `domainsâ‰ˆ192`, `mailboxesâ‰ˆ32`, `usersâ‰ˆ33`, `projectsâ‰ˆ17`, and `ssl_certificates`+`ftp_accounts` together â‰ˆ189. (Real collection names confirmed in `backend/internal/database/collections.go`: `domains`, `mailboxes`, `users`, `ssl_certificates`, `ftp_accounts`, `projects`.) If these don't match on PROD, **STOP** â€” you may be pointed at the wrong host/db.
> **Decision where draft and review disagreed:** the original draft used `estimatedDocumentCount()` for the baseline. We use `countDocuments({})` instead â€” **safer**, because the estimate reads cached metadata that can drift from the real count, and we are proving *zero data loss*, where an exact count is the whole point.

---

## 2) Backup â€” the "no data loss" safety net

> Run **on the target host before touching apt or `mongod`**, **after** pre-flight passes. Nothing in Â§4 proceeds until a **verified** logical dump **and** a cold filesystem copy both exist.
> **Why both:** the gzip `mongodump` archive is version-portable and is your **only** rollback once FCV is 8.0; the cold tar/copy is your **seconds-fast** rollback while FCV is still 7.0. Keep both.

```bash
#!/usr/bin/env bash
# ===== BACKUP & PRE-UPGRADE SAFETY (run as root). Fail-closed. =====
set -Eeuo pipefail
fail(){ echo "GATE FAILED: $*" >&2; exit 1; }

MONGO_PASS="$(grep -E '^MONGO_PASS=' /opt/serverpanel/.env | head -1 | cut -d= -f2- | tr -d '"'\''')"
[ -n "$MONGO_PASS" ] || fail "MONGO_PASS not found in /opt/serverpanel/.env"
AUTH=(-u admin -p "$MONGO_PASS" --authenticationDatabase admin)   # admin/root: also captures admin-db users

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_DIR="/var/backups/mongo-upgrade/${STAMP}"
mkdir -p "$BACKUP_DIR"; chmod 700 "$BACKUP_DIR"   # contains all panel data + secrets
ARCHIVE="${BACKUP_DIR}/mongodump-7.0-${STAMP}.archive.gz"

# ---- 1. Sanity: auth still works (cheap re-check) ----
mongosh --quiet "${AUTH[@]}" --eval 'db.adminCommand({ping:1})' >/dev/null || fail "auth/ping failed â€” STOP"

# ---- 2. QUIESCE the writer so the dump is consistent ----
# Standalone has no oplog dump; stop the panel so nothing writes during the dump.
# *** PANEL DOWNTIME (maintenance window) STARTS HERE. *** mongod stays up for the dump.
systemctl stop serverpanel
echo "serverpanel stopped â€” panel downtime begins. mongod still running."

# ---- 3. PRIMARY backup: single gzipped archive (whole instance: admin users + serverpanel data) ----
command -v mongodump >/dev/null || apt-get install -y mongodb-database-tools
mongodump "${AUTH[@]}" --host 127.0.0.1 --port 27017 --archive="$ARCHIVE" --gzip \
  2>&1 | tee "${BACKUP_DIR}/mongodump-${STAMP}.log"
echo "mongodump exit: ${PIPESTATUS[0]}"; [ "${PIPESTATUS[0]}" -eq 0 ] || fail "mongodump nonzero exit"
sha256sum "$ARCHIVE" | tee "${ARCHIVE}.sha256"
ls -lh "$ARCHIVE"

# ---- 4. VERIFY the dump is restorable â€” do NOT trust an unverified dump ----
# 4a. Parse the whole archive (no writes):
mongorestore "${AUTH[@]}" --gzip --archive="$ARCHIVE" --dryRun -v \
  2>&1 | tee "${BACKUP_DIR}/verify-dryrun-${STAMP}.log" || fail "--dryRun restore validation failed"
# 4b. Real restore into a throwaway namespace + count compare (gold standard):
LIVE_CC=$(mongosh --quiet "${AUTH[@]}" --eval 'db.getSiblingDB("serverpanel").getCollectionNames().length')
mongorestore "${AUTH[@]}" --gzip --archive="$ARCHIVE" \
  --nsFrom 'serverpanel.*' --nsTo 'serverpanel_restoretest.*' --drop \
  2>&1 | tee "${BACKUP_DIR}/verify-restore-${STAMP}.log"
TEST_CC=$(mongosh --quiet "${AUTH[@]}" --eval 'db.getSiblingDB("serverpanel_restoretest").getCollectionNames().length')
echo "live collections=${LIVE_CC}  restored-test=${TEST_CC}"
[ "$LIVE_CC" = "$TEST_CC" ] || fail "backup restore-verify mismatch â€” DO NOT UPGRADE"
mongosh --quiet "${AUTH[@]}" --eval 'db.getSiblingDB("serverpanel_restoretest").dropDatabase()'
echo "OK: backup verified restorable; test namespace dropped."

# ---- 5. COLD filesystem copy (fast pre-FCV rollback) â€” mongod MUST be stopped ----
systemctl stop mongod
sleep 2; systemctl is-active --quiet mongod && fail "mongod still active â€” cannot take consistent copy" || true
tar --numeric-owner -czf "${BACKUP_DIR}/datadir-7.0-${STAMP}.tar.gz" -C /var/lib mongodb \
  2>&1 | tee "${BACKUP_DIR}/tar-mongodb-${STAMP}.log"
gzip -t "${BACKUP_DIR}/datadir-7.0-${STAMP}.tar.gz" && echo "tar gzip OK"
ls -lh "${BACKUP_DIR}/datadir-7.0-${STAMP}.tar.gz"
# Bring mongod back so the upgrade picks up from a clean running 7.0:
systemctl start mongod
mongosh --quiet "${AUTH[@]}" --eval 'db.adminCommand({ping:1})' >/dev/null \
  || fail "mongod did not return healthy after cold copy â€” investigate before upgrading"

# ---- 6. Copy config + env + record exact rollback versions ----
cp -a /etc/mongod.conf      "${BACKUP_DIR}/mongod.conf.bak"
cp -a /opt/serverpanel/.env "${BACKUP_DIR}/serverpanel.env.bak"; chmod 600 "${BACKUP_DIR}/serverpanel.env.bak"
mongod --version | head -1                     >  "${BACKUP_DIR}/mongod-version-before.txt"
dpkg -l 'mongodb-org*' | grep '^ii'            >> "${BACKUP_DIR}/mongod-version-before.txt"
apt-mark showhold | grep -i '^mongodb'         >> "${BACKUP_DIR}/mongod-version-before.txt" || true
stat -c '%U:%G %a %n' /var/lib/mongodb /var/log/mongodb | tee "${BACKUP_DIR}/ownership-pre-${STAMP}.txt"

# ---- 7. Off-box copy (strongly recommended) ----
# scp/rsync the archive + .sha256 + *.bak to a second host or object storage so a disk
# failure can't take the data AND the backup together. Example:
#   rsync -a "$BACKUP_DIR"/{*.archive.gz,*.sha256,*.bak} backup-host:/srv/mongo-backups/$(hostname)-${STAMP}/

echo "=== BACKUP COMPLETE. Verified archive: $ARCHIVE  (sha256 saved). serverpanel is STOPPED. ==="
```

> **Optional but recommended â€” provider snapshot (Hostinger/Contabo VPS):** with `serverpanel` and `mongod` stopped, take an on-demand VPS snapshot named `serverpanel-pre-mongo8-<STAMP>` via the provider panel/CLI, then restart `mongod` (and only `mongod`, leaving `serverpanel` stopped per the exit gate) if you snapshotted with it down. A full-disk snapshot is the strongest single-step rollback. If the provider only offers periodic auto-backups, **do not rely on them** â€” the dump + tar above are then mandatory.

**Exit gate for Â§2 (all true):** `mongodump` exited 0; `--dryRun` listed the full `serverpanel` db with no errors; the throwaway-namespace restore matched live collection count; `gzip -t` passed on the cold tar; `mongod.conf` + `.env` copied; `mongod-version-before.txt` records the exact 7.0.x package versions. **`serverpanel` stays STOPPED into Â§4.**

---

## 3) Test on S1, then S2 (explicit â€” staging is a gate, not a courtesy)

PROD is **never** the first attempt. Run the **entire** runbook end-to-end on staging, in this exact order, and only advance when the prior host fully succeeds:

1. **S1 (`89.116.34.207`):** run Â§1 â†’ Â§2 â†’ Â§4 â†’ Â§4.5 â†’ confirm panel healthy on 8.0 â†’ raise FCV to 8.0 â†’ re-verify (Â§5). If anything fails, exercise Â§6 rollback on S1 and fix the procedure before continuing.
2. **S2 (`195.35.7.64`):** repeat the full Â§1 â†’ Â§5 sequence end-to-end. This is your second independent proof the steps are correct and idempotent.
3. **Only after S1 and S2 both reach "8.0, FCV 8.0, panel verified healthy"** do you schedule and execute PROD.

What staging must prove before PROD is touched:
- 8.0 binary **starts** and `mongod` is healthy (the FCV-7.0 precondition was correct).
- Panel comes back up and authenticates (the disable-authâ†’reset-usersâ†’re-enable-auth self-heal still works; see Â§4 note).
- The verified backup actually **restores** (you ran the Â§2 verify, and ideally a full Â§6 dump-restore drill on S1 to time it and prove Path B).
- The FCV raise (Â§4.5) succeeds and is reported `8.0`.

> If staging and PROD differ in any way (Mongo patch level, FCV, disk layout, apt holds, codename), pre-flight (Â§1) will catch it per-host â€” which is why Â§1 runs on **every** host, including PROD, not just S1.

---

## 4) Production upgrade (maintenance window)

> Pre-conditions: Â§1 PASSED and Â§2 produced a **verified** backup on this host; `serverpanel` is **STOPPED**; you are still on the **7.0** binary at **FCV 7.0**.
> This is performed **manually** (not via `install.sh`'s automatic path) so binary swap and FCV raise are **decoupled and gated**.

```bash
#!/usr/bin/env bash
# ===== IN-PLACE BINARY UPGRADE 7.0 -> 8.0 (run as root). =====
set -Eeuo pipefail
fail(){ echo "STEP FAILED: $*" >&2; exit 1; }
MONGO_PASS="$(grep -E '^MONGO_PASS=' /opt/serverpanel/.env | head -1 | cut -d= -f2- | tr -d '"'\''')"
[ -n "$MONGO_PASS" ] || fail "MONGO_PASS not found"
AUTH=(--quiet -u admin -p "$MONGO_PASS" --authenticationDatabase admin)
CODENAME="$(lsb_release -cs)"

# ---- 4.0 Re-assert the two preconditions that make 8.0 bootable ----
FCV="$(mongosh "${AUTH[@]}" --eval 'print(db.adminCommand({getParameter:1,featureCompatibilityVersion:1}).featureCompatibilityVersion.version)' | tr -d '[:space:]')"
[ "$FCV" = "7.0" ] || fail "FCV is '$FCV', not 7.0 â€” 8.0 will refuse to start. Abort."
systemctl is-active --quiet serverpanel && fail "serverpanel still running â€” quiesce first (see Â§2)." || true

# ---- 4.1 Write 8.0 key + repo (canonical pgp host), KEEPING the 7.0 list for now ----
# Decision: original draft/install.sh use www.mongodb.org/static/pgp; we use the canonical
# pgp.mongodb.com host â€” safer against the legacy URL 404'ing. Key path must match signed-by.
curl -fsSL "https://pgp.mongodb.com/server-8.0.asc" \
  | gpg --dearmor --yes -o /usr/share/keyrings/mongodb-server-8.0.gpg
echo "deb [arch=amd64,arm64 signed-by=/usr/share/keyrings/mongodb-server-8.0.gpg] https://repo.mongodb.org/apt/ubuntu ${CODENAME}/mongodb-org/8.0 multiverse" \
  > /etc/apt/sources.list.d/mongodb-org-8.0.list
# NOTE: do NOT delete the old mongodb-org-7.0.list yet (install.sh:425 deletes too early).

# ---- 4.2 Unhold (if held), update, and CONFIRM an 8.0 candidate exists BEFORE removing 7.0 repo ----
apt-mark unhold mongodb-org mongodb-org-server mongodb-org-database mongodb-org-mongos mongodb-org-tools mongodb-mongosh 2>/dev/null || true
apt-get update
apt-cache policy mongodb-org | sed -n '1,6p'
apt-cache policy mongodb-org | grep -qE 'Candidate:\s+8\.0\.' \
  || fail "no 8.0 candidate for mongodb-org â€” repo/egress problem. 7.0 repo still intact; nothing changed."
# Only now is it safe to remove stale repo lists (8.0 fetch is proven):
find /etc/apt/sources.list.d -name 'mongodb-org-*.list' ! -name 'mongodb-org-8.0.list' -delete || true
apt-get update

# ---- 4.3 In-place upgrade ----
apt-get install -y mongodb-org

# ---- 4.4 HARD-VERIFY the binary is actually 8.0 BEFORE restarting into it ----
mongod --version | head -1
mongod --version | grep -qE 'db version v8\.0\.' || fail "binary is not 8.0.x after install (silent no-op?). Do NOT restart."

# ---- 4.5 Re-assert data-dir ownership so the 8.0 mongod (user mongodb) can open it ----
chown -R mongodb:mongodb /var/lib/mongodb /var/log/mongodb

# ---- 4.6 Restart mongod on the 8.0 binary (FCV still 7.0 â€” fully reversible at this point) ----
systemctl restart mongod
systemctl is-active --quiet mongod || { journalctl -u mongod --no-pager -n 60; fail "mongod failed to start on 8.0 â€” see Â§6 rollback"; }
mongosh "${AUTH[@]}" --eval 'db.adminCommand({ping:1})' >/dev/null \
  || { journalctl -u mongod --no-pager -n 60; fail "mongod up but auth/ping failed â€” see Â§6"; }
# Confirm we are running 8.0 with FCV STILL 7.0:
mongosh "${AUTH[@]}" --eval 'print("serverStatus.version="+db.serverStatus().version);
  print("FCV="+db.adminCommand({getParameter:1,featureCompatibilityVersion:1}).featureCompatibilityVersion.version);'
# Expect: serverStatus.version=8.0.x   FCV=7.0

# ---- 4.7 Bring the panel back up ON 8.0 (still FCV 7.0) ----
systemctl start serverpanel
systemctl is-active --quiet serverpanel || fail "serverpanel failed to start on 8.0 mongod"
echo "=== mongod on 8.0, FCV STILL 7.0, panel UP. Begin SOAK. ==="
```

> **Note on the panel's user self-heal:** `install.sh:453-572` runs a disable-auth â†’ reset `admin`/`serverpanel` from `MONGO_PASS` â†’ re-enable-auth sync after a binary swap, so a drifted password self-heals. We do **not** invoke it here â€” pre-flight already proved auth works, so if the panel can't authenticate after restart, treat it as a **real upgrade problem** (investigate / consider Â§6), not benign drift.

### 4.5 â€” SOAK, then raise FCV to 8.0 (separate, deliberate, irreversible step)

> **Decision where draft and review disagreed:** `install.sh:579-582` raises FCV **automatically, in the same run, best-effort/non-fatal** (the call ends in `|| warn`, so a failure is logged and ignored). We **defer and gate it** â€” **safer**, because while FCV is still 7.0 the upgrade is reversible by reinstalling 7.0 (no restore); raising FCV converts that into a dump-restore-only rollback. Do not let `install.sh` fire this step during the upgrade.

**Soak for â‰¥ 30 minutes** (longer on PROD if the window allows) with the panel live on the 8.0 binary. During soak, confirm:
- `systemctl status mongod serverpanel` both active; no crash loops.
- `journalctl -u mongod --since "30 min ago"` shows no errors (THP/ulimit warnings are benign).
- Panel smoke test: log in, list domains, open a mailbox, view a project â€” i.e. real reads/writes succeed.

**Only after a clean soak**, raise FCV (this is the point of no cheap return):

```bash
MONGO_PASS="$(grep -E '^MONGO_PASS=' /opt/serverpanel/.env | head -1 | cut -d= -f2- | tr -d '"'\''')"
AUTH=(--quiet -u admin -p "$MONGO_PASS" --authenticationDatabase admin)
# Pre-check, then commit:
mongosh "${AUTH[@]}" --eval 'printjson(db.adminCommand({getParameter:1,featureCompatibilityVersion:1}))'   # expect 7.0
mongosh "${AUTH[@]}" --eval 'printjson(db.adminCommand({setFeatureCompatibilityVersion:"8.0", confirm:true}))' \
  || { echo "setFCV failed â€” investigate (do NOT assume success)"; exit 1; }
# Verify it stuck:
mongosh "${AUTH[@]}" --eval 'print("FCV="+db.adminCommand({getParameter:1,featureCompatibilityVersion:1}).featureCompatibilityVersion.version)'
# Expect: FCV=8.0
```

### 4.6 â€” Re-apply apt holds (optional, recommended)

```bash
apt-mark hold mongodb-org mongodb-org-server mongodb-org-database mongodb-org-mongos mongodb-org-tools mongodb-mongosh
```

---

## 5) Verification (zero-data-loss proof)

Run after Â§4 (binary swap) **and again** after the FCV raise (Â§4.5). Compare against the Â§1 baseline.

```bash
MONGO_PASS="$(grep -E '^MONGO_PASS=' /opt/serverpanel/.env | head -1 | cut -d= -f2- | tr -d '"'\''')"
AUTH_ADMIN=(--quiet -u admin       -p "$MONGO_PASS" --authenticationDatabase admin)
AUTH_PANEL=(--quiet -u serverpanel -p "$MONGO_PASS" --authenticationDatabase admin)

# 1. Server + FCV
mongosh "${AUTH_ADMIN[@]}" --eval 'print("version="+db.serverStatus().version);
  print("FCV="+db.adminCommand({getParameter:1,featureCompatibilityVersion:1}).featureCompatibilityVersion.version);'
# Expect version=8.0.x and (post-4.5) FCV=8.0

# 2. Per-collection count diff vs baseline â€” MUST be all OK
mongosh "${AUTH_PANEL[@]}" serverpanel --eval '
  const base = JSON.parse(cat("/root/mongo-preupgrade-baseline.json")).collections;
  let ok=true;
  Object.keys(base).sort().forEach(function(c){
    const now = db.getCollection(c).countDocuments({});
    const was = base[c];
    print((now===was?"OK   ":"DIFF ")+c+": was="+was+" now="+now);
    if (now!==was) ok=false;
  });
  print(ok ? "RESULT: ALL COLLECTION COUNTS MATCH (zero data loss)" : "RESULT: MISMATCH â€” INVESTIGATE / CONSIDER ROLLBACK");
'

# 3. Headline PROD inventory sanity
mongosh "${AUTH_PANEL[@]}" serverpanel --eval '
  ["domains","mailboxes","users","ssl_certificates","ftp_accounts","projects"].forEach(function(c){
    print(c+"="+db.getCollection(c).countDocuments({}));
  });'   # Expect domainsâ‰ˆ192, mailboxesâ‰ˆ32, usersâ‰ˆ33, projectsâ‰ˆ17, ssl+ftpâ‰ˆ189

# 4. dbStats (storage churn is fine; objects must match baseline totalDocs)
mongosh "${AUTH_PANEL[@]}" serverpanel --eval \
  'const s=db.stats(); printjson({collections:s.collections,objects:s.objects,indexes:s.indexes});'

# 5. Services + logs
systemctl is-active mongod serverpanel
journalctl -u mongod --since "15 min ago" --no-pager | grep -iE 'error|fail|abort' || echo "no mongod errors"

# 6. Panel end-to-end smoke (do by hand): log in, list domains, open a mailbox,
#    create+delete a throwaway DNS record or project to confirm WRITES persist.
```

**Sign-off (all true):** `version=8.0.x`; FCV=`8.0` (after Â§4.5); **all** collection counts match baseline; headline inventory matches; `mongod`+`serverpanel` active; no errors in logs; manual panel smoke (read **and** write) passes.

> Keep `/var/backups/mongo-upgrade/<STAMP>/` (archive + sha256 + cold tar + `.bak`s + verify logs) and any provider snapshot **until sign-off is formally approved.** Do not auto-rotate during the change window.

---

## 6) Rollback decision tree

The branch you take depends entirely on **whether FCV has been raised to 8.0 yet.** That is the irreversibility line.

```
                         â”Œâ”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”
                         â”‚  Has setFeatureCompatibilityVersion '8.0'    â”‚
                         â”‚  been run AND confirmed (Â§4.5)?              â”‚
                         â””â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”¬â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”¬â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”˜
                                   NO    â”‚             â”‚   YES
                  (binary rollback OK)   â”‚             â”‚   (dump-restore ONLY)
                                         â–¼             â–¼
                                  â”€â”€ PATH A â”€â”€    â”€â”€ PATH B â”€â”€
```

### PATH A â€” FCV is still 7.0 (binary-only rollback, fast)

Use when the 8.0 binary won't start, `mongod` is unhealthy, or the panel misbehaves on 8.0 **before** Â§4.5.

**A1 â€” Fastest: restore the cold filesystem copy (seconds).** Valid because the WiredTiger files were never written by an 8.0 binary at FCV 8.0.
```bash
STAMP=<the backup stamp>; BD="/var/backups/mongo-upgrade/${STAMP}"
systemctl stop serverpanel mongod
mv /var/lib/mongodb /var/lib/mongodb.broken.$(date +%s)
tar -xzf "${BD}/datadir-7.0-${STAMP}.tar.gz" -C /var/lib   # restores /var/lib/mongodb
chown -R mongodb:mongodb /var/lib/mongodb
# Reinstall 7.0 binaries (point repo back at 7.0, then downgrade):
apt-mark unhold mongodb-org mongodb-org-server mongodb-org-database mongodb-org-mongos mongodb-org-tools mongodb-mongosh 2>/dev/null || true
curl -fsSL https://pgp.mongodb.com/server-7.0.asc | gpg --dearmor --yes -o /usr/share/keyrings/mongodb-server-7.0.gpg
echo "deb [arch=amd64,arm64 signed-by=/usr/share/keyrings/mongodb-server-7.0.gpg] https://repo.mongodb.org/apt/ubuntu $(lsb_release -cs)/mongodb-org/7.0 multiverse" \
  > /etc/apt/sources.list.d/mongodb-org-7.0.list
find /etc/apt/sources.list.d -name 'mongodb-org-*.list' ! -name 'mongodb-org-7.0.list' -delete || true
apt-get update
# Exact 7.0.x recorded in Â§2's mongod-version-before.txt (the dpkg '^ii' line for mongodb-org):
PIN="$(awk '/^ii[[:space:]]+mongodb-org[[:space:]]/{print $3}' ${BD}/mongod-version-before.txt | head -1)"
[ -n "$PIN" ] || { echo "could not parse 7.0 version pin from ${BD}/mongod-version-before.txt â€” set PIN manually"; exit 1; }
apt-get install -y --allow-downgrades mongodb-org=${PIN} mongodb-org-server=${PIN} mongodb-org-database=${PIN} mongodb-org-mongos=${PIN} mongodb-org-tools=${PIN}
systemctl start mongod && systemctl start serverpanel
```

**A2 â€” Alternative: just reinstall the 7.0 binaries over the existing (untouched) data**, same `apt-get install -y --allow-downgrades ...=<7.0.x>` block as A1 **without** the tar restore. Safe only because FCV was never raised, so the on-disk files are still genuine 7.0 files. After either A1 or A2, confirm `mongod --version` is 7.0.x, FCV reports 7.0, and run Â§5 counts against the baseline.

### PATH B â€” FCV was already raised to 8.0 (binary rollback impossible â†’ dump restore)

Once FCV is `8.0`, **7.0 binaries cannot open the data files.** Neither the cold tar nor an in-place 7.0 reinstall will start against the 8.0-FCV data dir; the supported route is a clean 7.0 install + dump restore.

```bash
STAMP=<the backup stamp>; BD="/var/backups/mongo-upgrade/${STAMP}"
ARCHIVE="${BD}/mongodump-7.0-${STAMP}.archive.gz"
sha256sum -c "${ARCHIVE}.sha256"   # prove the archive is intact FIRST

systemctl stop serverpanel mongod
# Wipe the 8.0-FCV data dir and stand up a FRESH 7.0 install:
mv /var/lib/mongodb /var/lib/mongodb.fcv8.$(date +%s)
mkdir -p /var/lib/mongodb && chown mongodb:mongodb /var/lib/mongodb
# (reinstall 7.0.x binaries exactly as in PATH A1's repo+apt block, using the PIN from
#  mongod-version-before.txt, then ensure mongod.conf is restored from ${BD}/mongod.conf.bak)
cp -a "${BD}/mongod.conf.bak" /etc/mongod.conf
systemctl start mongod
# Restore the verified dump (recreates admin users + serverpanel data).
# A fresh data dir starts with NO users; if mongod.conf has auth enabled, mongorestore
# cannot authenticate yet. Two supported options:
#   (a) temporarily set 'security.authorization: disabled' in /etc/mongod.conf, restart
#       mongod, run the restore WITHOUT -u/-p (the dump re-creates the admin users), then
#       re-enable auth and restart; OR
#   (b) use the localhost exception to create the admin user first, then restore with auth.
# Example for option (a) â€” auth temporarily OFF:
mongorestore --gzip --archive="$ARCHIVE" --drop
# Re-enable auth in /etc/mongod.conf (or restore the saved one), restart, then verify:
MONGO_PASS="$(grep -E '^MONGO_PASS=' ${BD}/serverpanel.env.bak | head -1 | cut -d= -f2- | tr -d '"'\''')"
systemctl restart mongod
mongosh --quiet -u admin -p "$MONGO_PASS" --authenticationDatabase admin \
  --eval 'print("FCV="+db.adminCommand({getParameter:1,featureCompatibilityVersion:1}).featureCompatibilityVersion.version)'  # expect 7.0
systemctl start serverpanel
```
Then run **Â§5** and confirm every collection count matches the Â§1 baseline. **If the provider snapshot from Â§2 exists, restoring the whole VM is the simplest Path-B option** and supersedes the manual dump restore.

### Rollback triage quick reference

| Symptom | When | Action |
|---|---|---|
| 8.0 `mongod` won't start, log says `featureCompatibilityVersion ... too low` | After swap, FCV pre-flight was skipped | Should be impossible if Â§1 gate ran. Reinstall 7.0 (PATH A2); raise FCVâ†’7.0; redo Â§1. |
| `mongod` won't start, data-dir permission denied | After swap | `chown -R mongodb:mongodb /var/lib/mongodb /var/log/mongodb`; restart. If still failing â†’ PATH A1. |
| Panel can't authenticate after restart | After swap, FCV 7.0 | Treat as real (pre-flight proved auth). Re-run `install.sh` user-sync, or PATH A1/A2. |
| Counts mismatch / corruption suspected | Before FCV raise | PATH A1 (cold copy). |
| Counts mismatch / corruption suspected | After FCV raise | PATH B (dump restore) or provider snapshot. |
| Bad behavior on 8.0, data fine | Before FCV raise | PATH A2 (binaries only). |
| Bad behavior on 8.0, data fine | After FCV raise | PATH B / snapshot (no binary downgrade possible). |

---

### Hand-off: required `install.sh` patches (so the script matches this runbook)

1. **Gate the in-place upgrade on `FCV == "7.0"` and abort otherwise**, before `apt-get install -y mongodb-org` (currently `install.sh:429/435/447` swap with no check).
2. **Do NOT raise FCV automatically in the upgrade run.** Put `setFeatureCompatibilityVersion '8.0'` (`install.sh:579-582`, currently best-effort `|| warn`) behind an explicit `--commit-fcv` flag / separate step, run only after soak.
3. **Reorder repo handling:** don't `find ... -delete` the old `mongodb-org-*.list` (`install.sh:425`) until **after** `apt-get update` + `apt-cache policy` confirm an 8.0 candidate, so a failed fetch can't leave the box with no Mongo repo.
4. **Hard-verify `mongod --version` is `v8.0.x` before `systemctl restart mongod`**, and `chown -R mongodb:mongodb /var/lib/mongodb /var/log/mongodb` before restart.
5. Use the canonical key host `https://pgp.mongodb.com/server-8.0.asc` with a `signed-by=` path that exactly matches the written keyring.

**Repo source references (verified against the codebase):** `install.sh:417` (version regex `db version v\([0-9]*\.[0-9]*\)`), `:423-425` (key from `www.mongodb.org/static/pgp`, `signed-by=` repo line, old-list `find ... -delete`), `:429/435/447` (in-place `apt-get install -y mongodb-org`, no FCV precondition), `:453-572` (disable-authâ†’reset-usersâ†’re-enable-auth self-heal), `:508/515-518` (`admin`=root, `serverpanel`=readWrite+dbAdmin on `serverpanel`), `:579-582` (best-effort FCV raise ending in `|| warn`), `:1585-1586` (`MONGO_PASS=` unquoted line + full `MONGO_URI` with `authSource=admin`), `:1587` (`MONGO_DB_NAME=serverpanel`), `:1695` (`Requires=mongod.service`). Codename handling `install.sh:407-411` confirms 8.0 maps `noble` straight through (24.04 gets a real `noble` repo). Collection names in `backend/internal/database/collections.go`: `domains`, `mailboxes`, `users`, `ssl_certificates`, `ftp_accounts`, `projects`.
