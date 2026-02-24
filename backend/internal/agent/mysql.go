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
	if host == "" {
		host = "localhost"
	}
	sql := fmt.Sprintf(
		"CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY '%s'; GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%s'; FLUSH PRIVILEGES;",
		username, host, password, dbName, username, host,
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
