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
  # Reuse panel admin URI but strip default-db so getSiblingDB lands
  # the operation on the right database. The auth side of the URI
  # (creds + authSource=admin) stays.
  base=$(echo "$URI" | sed -E 's#(mongodb://[^@]+@[^/]+)/[^?]*(\?.*)?#\1/admin\2#')
  exec mongosh --quiet "$base" --eval "$1"
fi
exec mongosh --quiet --eval "$1"
`
	_, err := RunCommand(ctx, "bash", "-c", wrapper, "--", jsAgainstDB)
	return err
}

func CreateMongoDatabase(ctx context.Context, dbName, username, password string) error {
	js := fmt.Sprintf(
		`db.getSiblingDB(%q).createUser({user: %q, pwd: %q, roles: [{role: "readWrite", db: %q}]})`,
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
