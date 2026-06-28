package agent

import (
	"context"
	"fmt"
)

// mongoEval wraps a JS snippet so it runs against the panel's mongo
// (admin auth via /opt/serverpanel/.env's MONGO_URI) when present,
// falling back to a no-auth localhost connection. Using getSiblingDB
// instead of `use <db>` is critical: in `mongosh --eval` mode `use X`
// does NOT switch the db context for subsequent statements (a
// long-standing mongosh quirk), so `use X; db.createUser(...)` would
// silently create the user in `test`, not in X. Every helper here
// uses getSiblingDB to be correct.
func mongoEval(ctx context.Context, jsAgainstDB string) error {
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
	_, err := RunCommand(ctx, "bash", "-c", wrapper, "--", jsAgainstDB)
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
