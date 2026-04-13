package agent

import (
	"context"
	"fmt"
	"strings"
)

func CreateMySQLDatabase(ctx context.Context, dbName string) error {
	_, err := RunCommand(ctx, "mysql", "-e", fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`;", dbName))
	return err
}

func DropMySQLDatabase(ctx context.Context, dbName string) error {
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
	grants := mysqlGrantsForRole(role)
	sql := fmt.Sprintf(
		"CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY '%s'; GRANT %s ON `%s`.* TO '%s'@'%s'; FLUSH PRIVILEGES;",
		username, host, password, grants, dbName, username, host,
	)
	_, err := RunCommand(ctx, "mysql", "-e", sql)
	return err
}

func UpdateMySQLUserPassword(ctx context.Context, username, host, password string) error {
	if host == "" {
		host = "localhost"
	}
	sql := fmt.Sprintf(
		"ALTER USER '%s'@'%s' IDENTIFIED BY '%s'; FLUSH PRIVILEGES;",
		username, host, password,
	)
	_, err := RunCommand(ctx, "mysql", "-e", sql)
	return err
}

// UpdateMySQLUserRole revokes all current privileges on dbName and re-grants per role.
func UpdateMySQLUserRole(ctx context.Context, dbName, username, host, role string) error {
	if host == "" {
		host = "localhost"
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
	_, err := RunCommand(ctx, "mysql", "-e", fmt.Sprintf("DROP USER IF EXISTS '%s'@'%s'; FLUSH PRIVILEGES;", username, host))
	return err
}

func GetMySQLDatabaseSize(ctx context.Context, dbName string) (float64, error) {
	sql := fmt.Sprintf(
		"SELECT ROUND(SUM(data_length + index_length) / 1024 / 1024, 2) AS size_mb FROM information_schema.TABLES WHERE table_schema = '%s';",
		dbName,
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
