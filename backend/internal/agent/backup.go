package agent

import (
	"context"
	"fmt"
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

// TransferViaSFTP uploads a local file to a remote server using SFTP.
func TransferViaSFTP(ctx context.Context, localPath, host string, port int, user, pass, remotePath string) error {
	cmd := fmt.Sprintf(
		`sshpass -p '%s' sftp -o StrictHostKeyChecking=no -o ConnectTimeout=30 -P %d %s@%s <<'SFTP_EOF'
put %s %s
bye
SFTP_EOF`,
		pass, port, user, host, localPath, remotePath,
	)
	_, err := RunLongCommand(ctx, "bash", "-c", cmd)
	return err
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

// TransferViaSCP uploads a local file to a remote server using SCP.
func TransferViaSCP(ctx context.Context, localPath, host string, port int, user, pass, remotePath string) error {
	dest := fmt.Sprintf("%s@%s:%s", user, host, remotePath)
	_, err := RunLongCommand(ctx, "sshpass", "-p", pass,
		"scp", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=30",
		"-P", fmt.Sprintf("%d", port), localPath, dest,
	)
	return err
}

// DownloadViaSFTP downloads a file from a remote server using SFTP.
func DownloadViaSFTP(ctx context.Context, host string, port int, user, pass, remotePath, localPath string) error {
	cmd := fmt.Sprintf(
		`sshpass -p '%s' sftp -o StrictHostKeyChecking=no -o ConnectTimeout=30 -P %d %s@%s <<'SFTP_EOF'
get %s %s
bye
SFTP_EOF`,
		pass, port, user, host, remotePath, localPath,
	)
	_, err := RunLongCommand(ctx, "bash", "-c", cmd)
	return err
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

// DownloadViaSCP downloads a file from a remote server using SCP.
func DownloadViaSCP(ctx context.Context, host string, port int, user, pass, remotePath, localPath string) error {
	src := fmt.Sprintf("%s@%s:%s", user, host, remotePath)
	_, err := RunLongCommand(ctx, "sshpass", "-p", pass,
		"scp", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=30",
		"-P", fmt.Sprintf("%d", port), src, localPath,
	)
	return err
}

// TestRemoteConnection tests connectivity to a remote server.
func TestRemoteConnection(ctx context.Context, protocol, host string, port int, user, pass string) error {
	switch protocol {
	case "sftp", "ssh", "scp":
		_, err := RunCommand(ctx, "sshpass", "-p", pass,
			"ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=10",
			"-p", fmt.Sprintf("%d", port),
			fmt.Sprintf("%s@%s", user, host), "echo", "ok",
		)
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
