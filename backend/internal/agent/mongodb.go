package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// mongoEval wraps a JS snippet so it runs against the panel's mongo
// (admin auth via /opt/serverpanel/.env's MONGO_URI) when present,
// falling back to a no-auth localhost connection. Using getSiblingDB
// instead of `use <db>` is critical: in `mongosh --eval` mode `use X`
// does NOT switch the db context for subsequent statements (a
// long-standing mongosh quirk), so `use X; db.createUser(...)` would
// silently create the user in `test`, not in X. Every helper here
// uses getSiblingDB to be correct.
func mongoEvalOut(ctx context.Context, jsAgainstDB string) (string, error) {
	wrapper := `set -e
URI=""
for env in /opt/serverpanel/.env /opt/serverpanel/backend/.env; do
  [ -f "$env" ] || continue
  u=$(grep -E '^(MONGODB_URI|MONGO_URI)=' "$env" | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'")
  if [ -n "$u" ]; then URI="$u"; break; fi
done
if [ -n "$URI" ]; then
  # Provisioning (createUser / createCollection / dropDatabase on ARBITRARY
  # tenant DBs) requires an admin-scoped Mongo user. install.sh creates an
  # 'admin' user with the 'root' role using the SAME password as the
  # DB-scoped 'serverpanel' user (both = MONGO_PASS). 'serverpanel' only
  # holds readWrite+dbAdmin on the 'serverpanel' DB, so createUser on a
  # per-tenant DB failed with "not authorized" — the reason MongoDB
  # provisioning was disabled pre-3.1.108. Derive an admin URI from the
  # panel URI: keep the password + host, swap the username to 'admin', and
  # target /admin?authSource=admin.
  pass=$(printf '%s' "$URI" | sed -E 's#^mongodb(\+srv)?://[^:]+:([^@]+)@.*#\2#')
  hostport=$(printf '%s' "$URI" | sed -E 's#^mongodb(\+srv)?://[^@]+@([^/?]+).*#\2#')
  base="mongodb://admin:${pass}@${hostport}/admin?authSource=admin"
  exec mongosh --quiet "$base" --eval "$1"
fi
exec mongosh --quiet --eval "$1"
`
	res, err := RunCommand(ctx, "bash", "-c", wrapper, "--", jsAgainstDB)
	out := ""
	if res != nil {
		out = res.Output
	}
	return out, err
}

// mongoEval runs JS and discards stdout — used by the create/drop/update
// helpers that only care about success/failure.
func mongoEval(ctx context.Context, jsAgainstDB string) error {
	_, err := mongoEvalOut(ctx, jsAgainstDB)
	return err
}

// CreateMongoDatabase provisions a tenant MongoDB database: it creates the
// owning user (dbOwner — full control of just THIS db, mirroring the MySQL
// dbOwner grant) and an initial "data" collection so the database
// materializes and appears in `show dbs` / the panel listing immediately (a
// Mongo database with zero collections is invisible). Runs as the admin
// user via mongoEval.
func CreateMongoDatabase(ctx context.Context, dbName, username, password string) error {
	js := fmt.Sprintf(
		`var d = db.getSiblingDB(%q); `+
			`d.createUser({user: %q, pwd: %q, roles: [{role: "dbOwner", db: %q}]}); `+
			`if (!d.getCollectionNames().includes("data")) { d.createCollection("data"); }`,
		dbName, username, password, dbName)
	return mongoEval(ctx, js)
}

func DeleteMongoDatabase(ctx context.Context, dbName string) error {
	js := fmt.Sprintf(`db.getSiblingDB(%q).dropDatabase()`, dbName)
	return mongoEval(ctx, js)
}

func CreateMongoUser(ctx context.Context, dbName, username, password, role string) error {
	js := fmt.Sprintf(
		`db.getSiblingDB(%q).createUser({user: %q, pwd: %q, roles: [{role: %q, db: %q}]})`,
		dbName, username, password, role, dbName)
	return mongoEval(ctx, js)
}

func DeleteMongoUser(ctx context.Context, dbName, username string) error {
	js := fmt.Sprintf(`db.getSiblingDB(%q).dropUser(%q)`, dbName, username)
	return mongoEval(ctx, js)
}

func UpdateMongoUserPassword(ctx context.Context, dbName, username, password string) error {
	js := fmt.Sprintf(`db.getSiblingDB(%q).changeUserPassword(%q, %q)`,
		dbName, username, password)
	return mongoEval(ctx, js)
}

func UpdateMongoUserRole(ctx context.Context, dbName, username, role string) error {
	js := fmt.Sprintf(
		`db.getSiblingDB(%q).updateUser(%q, {roles: [{role: %q, db: %q}]})`,
		dbName, username, role, dbName)
	return mongoEval(ctx, js)
}

// RestoreMongoUsers recreates MongoDB user accounts on the LOCAL
// (destination) mongo from the EJSON that agent.RemoteMongoUsers exported
// from the source. Each document is written verbatim into
// admin.system.users — preserving its SCRAM credential sub-document — so
// the user keeps its ORIGINAL password and migrated apps authenticate with
// no credential change. After the writes it invalidates the in-memory user
// cache so the accounts work immediately without a mongod restart.
//
// Idempotent: a user that already exists (same _id, i.e. "<db>.<user>") is
// deleted and re-inserted. EJSON.parse is used (not a plain object literal)
// so BSON types like the userId UUID round-trip correctly. Returns the
// number of users written.
//
// Direct writes to admin.system.users are used deliberately: createUser
// only accepts a plaintext password, which the migration does not have —
// only the source's stored hash. Copying the credential document is the
// only way to move a user without knowing (or resetting) its password.
func RestoreMongoUsers(ctx context.Context, usersEJSON string) (int, error) {
	usersEJSON = strings.TrimSpace(usersEJSON)
	if usersEJSON == "" || usersEJSON == "[]" || usersEJSON == "null" {
		return 0, nil
	}

	// strconv.Quote yields a JS-safe double-quoted string literal; SCRAM
	// credentials and mongo user/db names are ASCII so the round-trip is
	// exact. The script deletes-then-inserts each doc (the verified-working
	// path) and prints the count.
	js := `var __docs = EJSON.parse(` + strconv.Quote(usersEJSON) + `);
var __admin = db.getSiblingDB("admin").getCollection("system.users");
var __n = 0;
__docs.forEach(function(d){
  try { __admin.deleteOne({_id: d._id}); __admin.insertOne(d); __n++; }
  catch (e) { print("skip " + d._id + ": " + e); }
});
db.adminCommand({invalidateUserCache: 1});
print("RESTORED_USERS=" + __n);
`
	tmp, err := os.CreateTemp("", "bz-mongo-users-*.js")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(js); err != nil {
		tmp.Close()
		return 0, err
	}
	tmp.Close()

	// Run the script against the admin-scoped local mongo (same URI
	// derivation as mongoEvalOut). --file avoids the argv length limit that
	// a big --eval would hit for a database with many users.
	wrapper := `set -e
URI=""
for env in /opt/serverpanel/.env /opt/serverpanel/backend/.env; do
  [ -f "$env" ] || continue
  u=$(grep -E '^(MONGODB_URI|MONGO_URI)=' "$env" | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'")
  if [ -n "$u" ]; then URI="$u"; break; fi
done
if [ -n "$URI" ]; then
  pass=$(printf '%s' "$URI" | sed -E 's#^mongodb(\+srv)?://[^:]+:([^@]+)@.*#\2#')
  hostport=$(printf '%s' "$URI" | sed -E 's#^mongodb(\+srv)?://[^@]+@([^/?]+).*#\2#')
  exec mongosh --quiet "mongodb://admin:${pass}@${hostport}/admin?authSource=admin" --file "$1"
fi
exec mongosh --quiet --file "$1"
`
	res, err := RunCommand(ctx, "bash", "-c", wrapper, "--", tmp.Name())
	if err != nil {
		return 0, fmt.Errorf("restore mongo users: %w", err)
	}
	n := 0
	if res != nil {
		for _, line := range strings.Split(res.Output, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "RESTORED_USERS=") {
				n = atoiSafe(strings.TrimPrefix(line, "RESTORED_USERS="))
			}
		}
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// Server-level MongoDB administration (v3.1.109)
//
// These back the WHM "MongoDB" admin page. They run as the admin (root)
// Mongo user via mongoEvalOut, parse mongosh's JSON stdout, and touch
// /etc/mongod.conf for the raw-config editor. The service restart + the
// post-change health gate + rollback live in the config service's
// applyMongodChangeSafely wrapper, never here.
// ---------------------------------------------------------------------------

// MongoDBInfo is one row of `listDatabases` for the all-databases table.
type MongoDBInfo struct {
	Name       string  `json:"name"`
	SizeOnDisk float64 `json:"size_on_disk"`
	Empty      bool    `json:"empty"`
}

// MongoUserInfo is one admin-db user (a Mongo "super admin").
type MongoUserInfo struct {
	User  string   `json:"user"`
	DB    string   `json:"db"`
	Roles []string `json:"roles"`
}

// MongoStatus is a small serverStatus slice for the page header.
type MongoStatus struct {
	Version              string `json:"version"`
	Uptime               int64  `json:"uptime"`
	ConnectionsCurrent   int    `json:"connections_current"`
	ConnectionsAvailable int    `json:"connections_available"`
}

// extractJSON slices the first JSON value out of mongosh stdout. mongosh
// --quiet normally prints only our print() payload, but this strips any
// stray banner/deprecation noise so json.Unmarshal stays robust.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	start := strings.IndexAny(s, "[{")
	if start < 0 {
		return s
	}
	end := strings.LastIndex(s, "]")
	if c := strings.LastIndex(s, "}"); c > end {
		end = c
	}
	if end < start {
		return s[start:]
	}
	return s[start : end+1]
}

// ListMongoDatabases returns every database via adminCommand listDatabases.
func ListMongoDatabases(ctx context.Context) ([]MongoDBInfo, error) {
	js := `print(JSON.stringify((db.adminCommand({listDatabases:1}).databases||[]).map(function(d){return {name:d.name,size_on_disk:Number(d.sizeOnDisk||0),empty:!!d.empty};})))`
	out, err := mongoEvalOut(ctx, js)
	if err != nil {
		return nil, err
	}
	var dbs []MongoDBInfo
	if err := json.Unmarshal([]byte(extractJSON(out)), &dbs); err != nil {
		return nil, fmt.Errorf("parse listDatabases: %w (raw: %s)", err, strings.TrimSpace(out))
	}
	return dbs, nil
}

// ListMongoAdminUsers returns the users defined on the admin database.
func ListMongoAdminUsers(ctx context.Context) ([]MongoUserInfo, error) {
	js := `var u=db.getSiblingDB("admin").getUsers(); var a=(u&&u.users)?u.users:u; print(JSON.stringify((a||[]).map(function(x){return {user:x.user,db:x.db,roles:(x.roles||[]).map(function(r){return r.role+"@"+r.db;})};})))`
	out, err := mongoEvalOut(ctx, js)
	if err != nil {
		return nil, err
	}
	var users []MongoUserInfo
	if err := json.Unmarshal([]byte(extractJSON(out)), &users); err != nil {
		return nil, fmt.Errorf("parse getUsers: %w (raw: %s)", err, strings.TrimSpace(out))
	}
	return users, nil
}

// GetMongoStatus returns version/uptime/connection counts for the page header.
func GetMongoStatus(ctx context.Context) (*MongoStatus, error) {
	js := `var s=db.getSiblingDB("admin").serverStatus(); print(JSON.stringify({version:s.version,uptime:Number(s.uptime||0),connections_current:Number((s.connections||{}).current||0),connections_available:Number((s.connections||{}).available||0)}))`
	out, err := mongoEvalOut(ctx, js)
	if err != nil {
		return nil, err
	}
	var st MongoStatus
	if err := json.Unmarshal([]byte(extractJSON(out)), &st); err != nil {
		return nil, fmt.Errorf("parse serverStatus: %w (raw: %s)", err, strings.TrimSpace(out))
	}
	return &st, nil
}

// CreateMongoAdminUser creates a user on the admin database with one or more
// roles. Each role is either a bare role name (applied on the admin database,
// e.g. "root") or "role@db" for a database-scoped grant (e.g. "readWrite@shop").
// The caller (config service) validates the username/roles before this runs.
func CreateMongoAdminUser(ctx context.Context, username, password string, roles []string) error {
	js := fmt.Sprintf(
		`db.getSiblingDB("admin").createUser({user: %q, pwd: %q, roles: [%s]})`,
		username, password, mongoRolesJS(roles))
	return mongoEval(ctx, js)
}

// SetMongoAdminRoles replaces the full role set of an existing admin-db user.
func SetMongoAdminRoles(ctx context.Context, username string, roles []string) error {
	js := fmt.Sprintf(
		`db.getSiblingDB("admin").updateUser(%q, {roles: [%s]})`,
		username, mongoRolesJS(roles))
	return mongoEval(ctx, js)
}

// mongoRolesJS renders a roles list into the `{role,db}` object array Mongo's
// createUser/updateUser expects. "role@db" scopes to that db; a bare role is
// granted on the admin database; an empty list defaults to root@admin.
func mongoRolesJS(roles []string) string {
	objs := make([]string, 0, len(roles))
	for _, r := range roles {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		name, db := r, "admin"
		if i := strings.LastIndex(r, "@"); i >= 0 {
			name, db = r[:i], r[i+1:]
		}
		objs = append(objs, fmt.Sprintf(`{role: %q, db: %q}`, name, db))
	}
	if len(objs) == 0 {
		objs = append(objs, `{role: "root", db: "admin"}`)
	}
	return strings.Join(objs, ", ")
}

// GetMongoAdminRoles returns the roles of an admin-db user as "role@db" tokens.
func GetMongoAdminRoles(ctx context.Context, username string) ([]string, error) {
	js := fmt.Sprintf(`var u=db.getSiblingDB("admin").getUser(%q); print(JSON.stringify((u&&u.roles?u.roles:[]).map(function(r){return r.role+"@"+r.db;})))`, username)
	out, err := mongoEvalOut(ctx, js)
	if err != nil {
		return nil, err
	}
	var roles []string
	if err := json.Unmarshal([]byte(extractJSON(out)), &roles); err != nil {
		return nil, fmt.Errorf("parse getUser roles: %w", err)
	}
	return roles, nil
}

// MongoServerIP returns the panel's public server IP (SERVER_IP in .env, else
// the first `hostname -I` address) for building externally-usable connection
// URIs. Falls back to 127.0.0.1.
func MongoServerIP(ctx context.Context) string {
	res, _ := RunCommand(ctx, "bash", "-c",
		`v=$(grep -E '^SERVER_IP=' /opt/serverpanel/.env 2>/dev/null | head -1 | cut -d= -f2- | tr -d '"'); [ -z "$v" ] && v=$(hostname -I 2>/dev/null | awk '{print $1}'); [ -z "$v" ] && v=127.0.0.1; printf '%s' "$v"`)
	if res != nil && strings.TrimSpace(res.Output) != "" {
		return strings.TrimSpace(res.Output)
	}
	return "127.0.0.1"
}

// PanelMongoURI returns the raw MONGO_URI from the panel .env (the credentials
// the panel itself authenticates with). Used to build the main admin's URI and
// as the source-of-truth for rotation.
func PanelMongoURI(ctx context.Context) string {
	res, _ := RunCommand(ctx, "bash", "-c",
		`for env in /opt/serverpanel/.env /opt/serverpanel/backend/.env; do [ -f "$env" ] || continue; u=$(grep -E '^(MONGODB_URI|MONGO_URI)=' "$env" | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'"); if [ -n "$u" ]; then printf '%s' "$u"; break; fi; done`)
	if res != nil {
		return strings.TrimSpace(res.Output)
	}
	return ""
}

// PatchPanelMongoURI rewrites MONGO_URI (and MONGODB_URI if present) in the
// panel .env so a restarted panel authenticates with the new credentials.
func PatchPanelMongoURI(ctx context.Context, newURI string) error {
	script := fmt.Sprintf(
		`f=/opt/serverpanel/.env; [ -f "$f" ] || exit 0; `+
			`if grep -q '^MONGO_URI=' "$f"; then sed -i 's|^MONGO_URI=.*|MONGO_URI=%s|' "$f"; else echo 'MONGO_URI=%s' >> "$f"; fi; `+
			`if grep -q '^MONGODB_URI=' "$f"; then sed -i 's|^MONGODB_URI=.*|MONGODB_URI=%s|' "$f"; fi`,
		newURI, newURI, newURI)
	_, err := RunCommand(ctx, "bash", "-c", script)
	return err
}

// MongoPing verifies a connection URI can authenticate and reach mongod.
func MongoPing(ctx context.Context, uri string) error {
	_, err := RunCommand(ctx, "mongosh", "--quiet", uri, "--eval", "db.runCommand({ping:1})")
	return err
}

// ScheduleMongoAndPanelRestart restarts the mongod service and then the panel a
// few seconds in the future, detached from this process, so the running request
// can finish first. Restarting mongod drops any sessions still authenticated as
// a deleted/rotated admin; restarting the panel reconnects it with the freshly
// written .env credentials. Best-effort and idempotent (a stale transient unit
// from a prior run is reset first).
func ScheduleMongoAndPanelRestart(ctx context.Context) error {
	_, err := RunCommand(ctx, "bash", "-c",
		`systemctl reset-failed bz-mongo-panel-restart 2>/dev/null; `+
			`systemd-run --on-active=4 --unit=bz-mongo-panel-restart bash -c 'systemctl restart mongod; sleep 2; systemctl restart serverpanel' 2>/dev/null `+
			`|| (sleep 4; systemctl restart mongod; sleep 2; systemctl restart serverpanel) &`)
	return err
}

// DeleteMongoAdminUser drops a user from the admin database.
func DeleteMongoAdminUser(ctx context.Context, username string) error {
	return DeleteMongoUser(ctx, "admin", username)
}

// PanelMongoUsername returns the username the panel itself authenticates as
// (parsed from MONGO_URI). Used to protect that account from deletion.
func PanelMongoUsername(ctx context.Context) string {
	res, _ := RunCommand(ctx, "bash", "-c",
		`for env in /opt/serverpanel/.env /opt/serverpanel/backend/.env; do [ -f "$env" ] || continue; u=$(grep -E '^(MONGODB_URI|MONGO_URI)=' "$env" | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'"); if [ -n "$u" ]; then printf '%s' "$u" | sed -E 's#^mongodb(\+srv)?://([^:]+):.*#\2#'; break; fi; done`)
	if res != nil {
		return strings.TrimSpace(res.Output)
	}
	return ""
}

// ReadMongodConf returns the raw contents of /etc/mongod.conf.
func ReadMongodConf(ctx context.Context) (string, error) {
	res, err := RunCommand(ctx, "cat", "/etc/mongod.conf")
	if res != nil {
		return res.Output, err
	}
	return "", err
}

// WriteMongodConf overwrites /etc/mongod.conf. No restart here — the caller's
// safety wrapper restarts, health-checks, and rolls back on failure.
func WriteMongodConf(ctx context.Context, content string) error {
	script := "cat > /etc/mongod.conf <<'BZ_MONGOD_EOF'\n" + content + "\nBZ_MONGOD_EOF\n"
	_, err := RunCommand(ctx, "bash", "-c", script)
	return err
}

// MongodHealthy reports whether mongod is active, retrying briefly to let the
// service finish (re)starting. Auth-agnostic — it only checks the unit is
// running, which is what protects against a config that won't boot.
func MongodHealthy(ctx context.Context) bool {
	_, err := RunCommand(ctx, "bash", "-c",
		`for i in $(seq 1 6); do if systemctl is-active --quiet mongod; then exit 0; fi; sleep 1; done; exit 1`)
	return err == nil
}
