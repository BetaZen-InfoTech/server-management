package agent

import (
	"context"
	"fmt"
)

// CreateFTPAccount creates a Pure-FTPd virtual user.
// The user is jailed to the specified home directory.
func CreateFTPAccount(ctx context.Context, username, password, homeDir string) error {
	// Create virtual user with pure-pw
	// -u ftpuser -g ftpgroup sets the system uid/gid for the virtual user
	// -d sets the home directory (user is chrooted here)
	_, err := RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("echo '%s\n%s' | pure-pw useradd %s -u www-data -g www-data -d %s",
			password, password, username, homeDir))
	if err != nil {
		return fmt.Errorf("failed to create FTP user: %w", err)
	}

	// Rebuild the PureDB database
	_, err = RunCommand(ctx, "pure-pw", "mkdb")
	if err != nil {
		return fmt.Errorf("failed to rebuild PureDB: %w", err)
	}

	return nil
}

// DeleteFTPAccount removes a Pure-FTPd virtual user.
func DeleteFTPAccount(ctx context.Context, username string) error {
	_, _ = RunCommand(ctx, "pure-pw", "userdel", username)
	_, _ = RunCommand(ctx, "pure-pw", "mkdb")
	return nil
}

// UpdateFTPPassword changes the password for a Pure-FTPd virtual user.
func UpdateFTPPassword(ctx context.Context, username, password string) error {
	_, err := RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("echo '%s\n%s' | pure-pw passwd %s", password, password, username))
	if err != nil {
		return fmt.Errorf("failed to update FTP password: %w", err)
	}
	_, _ = RunCommand(ctx, "pure-pw", "mkdb")
	return nil
}
