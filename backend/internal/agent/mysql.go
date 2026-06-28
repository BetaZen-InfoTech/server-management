package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// MySQL identifier / host / value sanitization.
//
// Every statement below is executed via `mysql -e "<sql>"` running as the
// local MariaDB root over the unix socket. Pre-3.1.108 the database name,
// username, host and — critically — the user-chosen PASSWORD were
// fmt.Sprintf'd straight into the SQL with no escaping or validation, so a
// tenant creating a DB/user with a crafted name or password (e.g. a password
// containing  '; GRANT ALL ON *.* TO me@'%'; --  ) executed arbitrary SQL as
// MariaDB root — full cross-tenant DB compromise / privilege escalation.
//
// Fix: identifiers (db/user) are whitelisted to a strict charset and
// backtick/quote-wrapped after validation; the host is whitelisted; and any
// value that must appear inside a '...' string literal (passwords, schema
// names in information_schema lookups) is backslash-escaped. Callers get a
// clear error instead of executing a malformed/injected statement.
var (
	mysqlIdentRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	mysqlHostRe  = regexp.MustCompile(`^[A-Za-z0-9_.%:\-]{1,255}$`)
)

func validMySQLIdent(name string) error {
	if !mysqlIdentRe.MatchString(name) {
		return fmt.Errorf("invalid MySQL identifier %q (allowed: A-Z a-z 0-9 _ , max 64 chars)", name)
	}
	return nil
}

func validMySQLHost(h string) error {
	if !mysqlHostRe.MatchString(h) {
		return fmt.Errorf("invalid MySQL host %q", h)
	}
	return nil
}

// mysqlQuoteLiteral backslash-escapes a value for safe inclusion inside a
// MySQL '...' string literal (passwords, schema-name lookups).
func mysqlQuoteLiteral(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\x00", "", "\n", `\n`, "\r", `\r`, "\x1a", `\Z`)
	return r.Replace(s)
}

func CreateMySQLDatabase(ctx context.Context, dbName string) error {
	if err := validMySQLIdent(dbName); err != nil {
		return err
	}
	_, err := RunCommand(ctx, "mysql", "-e", fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`;", dbName))
	return err
}

func DropMySQLDatabase(ctx context.Context, dbName string) error {
	if err := validMySQLIdent(dbName); err != nil {
		return err
	}
	_, err := RunCommand(ctx, "mysql", "-e", fmt.Sprintf("DROP DATABASE IF EXISTS `%s`;", dbName))
	return err
}

func CreateMySQLUser(ctx context.Context, dbName, username, password, host string) error {
	return CreateMySQLUserWithRole(ctx, dbName, username, password, host, "readWrite")
}

// mysqlGrantsForRole maps high-level role names to MySQL privilege strings.
func mysqlGrantsForRole(role string) string {
	switch role {
	case "read":
		return "SELECT, SHOW VIEW"
	case "readWrite":
		return "SELECT, INSERT, UPDATE, DELETE, SHOW VIEW, EXECUTE"
	case "dbAdmin":
		return "SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, ALTER, INDEX, REFERENCES, CREATE VIEW, SHOW VIEW, CREATE ROUTINE, ALTER ROUTINE, EXECUTE, EVENT, TRIGGER, LOCK TABLES"
	case "userAdmin":
		return "ALL PRIVILEGES"
	case "dbOwner":
		fallthrough
	default:
		return "ALL PRIVILEGES"
	}
}

func CreateMySQLUserWithRole(ctx context.Context, dbName, username, password, host, role string) error {
	if host == "" {
		host = "localhost"
	}
	if err := validMySQLIdent(dbName); err != nil {
		return err
	}
	if err := validMySQLIdent(username); err != nil {
		return err
	}
	if err := validMySQLHost(host); err != nil {
		return err
	}
	grants := mysqlGrantsForRole(role)
	sql := fmt.Sprintf(
		"CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY '%s'; GRANT %s ON `%s`.* TO '%s'@'%s'; FLUSH PRIVILEGES;",
		username, host, mysqlQuoteLiteral(password), grants, dbName, username, host,
	)
	_, err := RunCommand(ctx, "mysql", "-e", sql)
	return err
}

func UpdateMySQLUserPassword(ctx context.Context, username, host, password string) error {
	if host == "" {
		host = "localhost"
	}
	if err := validMySQLIdent(username); err != nil {
		return err
	}
	if err := validMySQLHost(host); err != nil {
		return err
	}
	sql := fmt.Sprintf(
		"ALTER USER '%s'@'%s' IDENTIFIED BY '%s'; FLUSH PRIVILEGES;",
		username, host, mysqlQuoteLiteral(password),
	)
	_, err := RunCommand(ctx, "mysql", "-e", sql)
	return err
}

// UpdateMySQLUserRole revokes all current privileges on dbName and re-grants per role.
func UpdateMySQLUserRole(ctx context.Context, dbName, username, host, role string) error {
	if host == "" {
		host = "localhost"
	}
	if err := validMySQLIdent(dbName); err != nil {
		return err
	}
	if err := validMySQLIdent(username); err != nil {
		return err
	}
	if err := validMySQLHost(host); err != nil {
		return err
	}
	grants := mysqlGrantsForRole(role)
	sql := fmt.Sprintf(
		"REVOKE ALL PRIVILEGES ON `%s`.* FROM '%s'@'%s'; GRANT %s ON `%s`.* TO '%s'@'%s'; FLUSH PRIVILEGES;",
		dbName, username, host, grants, dbName, username, host,
	)
	_, err := RunCommand(ctx, "mysql", "-e", sql)
	return err
}

func DropMySQLUser(ctx context.Context, username, host string) error {
	if host == "" {
		host = "localhost"
	}
	if err := validMySQLIdent(username); err != nil {
		return err
	}
	if err := validMySQLHost(host); err != nil {
		return err
	}
	_, err := RunCommand(ctx, "mysql", "-e", fmt.Sprintf("DROP USER IF EXISTS '%s'@'%s'; FLUSH PRIVILEGES;", username, host))
	return err
}

func GetMySQLDatabaseSize(ctx context.Context, dbName string) (float64, error) {
	if err := validMySQLIdent(dbName); err != nil {
		return 0, err
	}
	sql := fmt.Sprintf(
		"SELECT ROUND(SUM(data_length + index_length) / 1024 / 1024, 2) AS size_mb FROM information_schema.TABLES WHERE table_schema = '%s';",
		mysqlQuoteLiteral(dbName),
	)
	result, err := RunCommand(ctx, "mysql", "-N", "-e", sql)
	if err != nil {
		return 0, err
	}
	output := strings.TrimSpace(result.Output)
	if output == "" || output == "NULL" {
		return 0, nil
	}
	var size float64
	fmt.Sscanf(output, "%f", &size)
	return size, nil
}
