package agent

import (
	"context"
	"fmt"
)

func BackupFiles(ctx context.Context, user, outputPath string) error {
	_, err := RunCommand(ctx, "tar", "-czf", outputPath, "-C", "/home", user)
	return err
}

func BackupMongoDB(ctx context.Context, dbName, outputPath string) error {
	_, err := RunCommand(ctx, "mongodump", "--archive="+outputPath, "--gzip", "--db", dbName)
	return err
}

func BackupEmail(ctx context.Context, domain, outputPath string) error {
	src := fmt.Sprintf("/var/mail/vhosts/%s/", domain)
	_, err := RunCommand(ctx, "tar", "-czf", outputPath, "-C", "/var/mail/vhosts", domain)
	_ = src
	return err
}

func RestoreFiles(ctx context.Context, user, archivePath string) error {
	_, err := RunCommand(ctx, "tar", "-xzf", archivePath, "-C", "/home")
	if err != nil {
		return err
	}
	_, err = RunCommand(ctx, "chown", "-R", user+":"+user, fmt.Sprintf("/home/%s", user))
	return err
}

func RestoreMongoDB(ctx context.Context, dbName, archivePath string) error {
	_, err := RunCommand(ctx, "mongorestore", "--archive="+archivePath, "--gzip", "--drop", "--db", dbName)
	return err
}
