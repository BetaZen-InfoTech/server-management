package agent

import (
	"context"
	"fmt"
	"path/filepath"
)

// --- Backup ---

func BackupFiles(ctx context.Context, user, outputPath string) error {
	_, err := RunCommand(ctx, "tar", "-czf", outputPath, "-C", "/home", user)
	return err
}

func BackupMongoDB(ctx context.Context, dbName, outputPath string) error {
	_, err := RunCommand(ctx, "mongodump", "--archive="+outputPath, "--gzip", "--db", dbName)
	return err
}

func BackupEmail(ctx context.Context, domain, outputPath string) error {
	_, err := RunCommand(ctx, "tar", "-czf", outputPath, "-C", "/var/mail/vhosts", domain)
	return err
}

// BackupNginxConfig archives nginx site configs for a domain.
func BackupNginxConfig(ctx context.Context, domain, outputPath string) error {
	_, err := RunCommand(ctx, "tar", "-czf", outputPath,
		"-C", "/etc/nginx",
		fmt.Sprintf("sites-available/%s", domain),
	)
	return err
}

// --- Restore ---

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

func RestoreEmail(ctx context.Context, domain, archivePath string) error {
	_, err := RunCommand(ctx, "tar", "-xzf", archivePath, "-C", "/var/mail/vhosts")
	if err != nil {
		return err
	}
	_, err = RunCommand(ctx, "chown", "-R", "vmail:vmail", fmt.Sprintf("/var/mail/vhosts/%s", domain))
	return err
}

// --- Remote Transfer (FTP/SFTP/SCP) ---

// TransferViaSFTP uploads a local file to a remote server using native Go SSH.
func TransferViaSFTP(ctx context.Context, localPath, host string, port int, user, pass, remotePath string) error {
	// Read local file and transfer via SSH cat
	_, err := SSHCommand(ctx, host, port, user, pass,
		fmt.Sprintf("mkdir -p $(dirname '%s')", remotePath))
	if err != nil {
		return err
	}
	return SCPUpload(ctx, host, port, user, pass, localPath, filepath.Dir(remotePath))
}

// TransferViaFTP uploads a local file to a remote server using FTP (curl).
func TransferViaFTP(ctx context.Context, localPath, host string, port int, user, pass, remotePath string) error {
	url := fmt.Sprintf("ftp://%s:%d%s", host, port, remotePath)
	_, err := RunLongCommand(ctx, "curl", "-T", localPath,
		"--user", fmt.Sprintf("%s:%s", user, pass),
		"--ftp-create-dirs",
		"--connect-timeout", "30",
		url,
	)
	return err
}

// TransferViaSCP uploads a local file to a remote server using native SSH.
func TransferViaSCP(ctx context.Context, localPath, host string, port int, user, pass, remotePath string) error {
	return SCPUpload(ctx, host, port, user, pass, localPath, filepath.Dir(remotePath))
}

// DownloadViaSFTP downloads a file from a remote server using native SSH.
func DownloadViaSFTP(ctx context.Context, host string, port int, user, pass, remotePath, localPath string) error {
	return SCPDownload(ctx, host, port, user, pass, remotePath, localPath)
}

// DownloadViaFTP downloads a file from a remote server using FTP (curl).
func DownloadViaFTP(ctx context.Context, host string, port int, user, pass, remotePath, localPath string) error {
	url := fmt.Sprintf("ftp://%s:%d%s", host, port, remotePath)
	_, err := RunLongCommand(ctx, "curl", "-o", localPath,
		"--user", fmt.Sprintf("%s:%s", user, pass),
		"--connect-timeout", "30",
		url,
	)
	return err
}

// DownloadViaSCP downloads a file from a remote server using native SSH.
func DownloadViaSCP(ctx context.Context, host string, port int, user, pass, remotePath, localPath string) error {
	return SCPDownload(ctx, host, port, user, pass, remotePath, localPath)
}

// TestRemoteConnection tests connectivity to a remote server.
func TestRemoteConnection(ctx context.Context, protocol, host string, port int, user, pass string) error {
	switch protocol {
	case "sftp", "ssh", "scp":
		// Use native Go SSH instead of sshpass
		_, err := SSHCommand(ctx, host, port, user, pass, "echo ok")
		return err
	case "ftp":
		url := fmt.Sprintf("ftp://%s:%d/", host, port)
		_, err := RunCommand(ctx, "curl", "--user", fmt.Sprintf("%s:%s", user, pass),
			"--connect-timeout", "10", "--list-only", url,
		)
		return err
	default:
		return fmt.Errorf("unsupported protocol: %s", protocol)
	}
}
