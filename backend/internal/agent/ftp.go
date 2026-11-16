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
	// Feed the password (twice, as pure-pw prompts) over STDIN and pass the
	// username/home as argv — never a `bash -c` string — so none of them can
	// inject a shell command even if a future caller supplies a user-chosen
	// password or a crafted name. See [[executor.RunCommandStdin]].
	_, err := RunCommandStdin(ctx, password+"\n"+password+"\n",
		"pure-pw", "useradd", username, "-u", "www-data", "-g", "www-data", "-d", homeDir)
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
	_, err := RunCommandStdin(ctx, password+"\n"+password+"\n",
		"pure-pw", "passwd", username)
	if err != nil {
		return fmt.Errorf("failed to update FTP password: %w", err)
	}
	_, _ = RunCommand(ctx, "pure-pw", "mkdb")
	return nil
}
