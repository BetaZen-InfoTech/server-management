package agent

import (
	"context"
	"fmt"
	"strings"
)

// SSHCommand runs a command on a remote server via SSH.
func SSHCommand(ctx context.Context, host string, port int, user, pass, command string) (*CommandResult, error) {
	return RunLongCommand(ctx, "sshpass", "-p", pass,
		"ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=30",
		"-p", fmt.Sprintf("%d", port),
		fmt.Sprintf("%s@%s", user, host),
		command,
	)
}

// SCPDownload downloads a file/directory from a remote server.
func SCPDownload(ctx context.Context, host string, port int, user, pass, remotePath, localPath string) error {
	_, err := RunLongCommand(ctx, "sshpass", "-p", pass,
		"scp", "-r", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=30",
		"-P", fmt.Sprintf("%d", port),
		fmt.Sprintf("%s@%s:%s", user, host, remotePath), localPath,
	)
	return err
}

// SCPUpload uploads a file/directory to a remote server.
func SCPUpload(ctx context.Context, host string, port int, user, pass, localPath, remotePath string) error {
	_, err := RunLongCommand(ctx, "sshpass", "-p", pass,
		"scp", "-r", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=30",
		"-P", fmt.Sprintf("%d", port),
		localPath, fmt.Sprintf("%s@%s:%s", user, host, remotePath),
	)
	return err
}

// DiscoverDomains lists domains from /home/*/domains on the source server.
func DiscoverDomains(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass, "ls /home/*/domains/ 2>/dev/null | sort -u || ls /var/www/vhosts/ 2>/dev/null | sort -u || echo ''")
	if err != nil {
		return nil, err
	}
	return parseLines(result.Output), nil
}

// DiscoverDatabases lists MongoDB databases on the source server.
func DiscoverDatabases(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass,
		`mongosh --quiet --eval "db.adminCommand('listDatabases').databases.forEach(d => print(d.name))" 2>/dev/null || mongo --quiet --eval "db.adminCommand('listDatabases').databases.forEach(function(d){print(d.name)})" 2>/dev/null || echo ''`)
	if err != nil {
		return nil, err
	}
	dbs := []string{}
	for _, line := range parseLines(result.Output) {
		if line != "admin" && line != "local" && line != "config" {
			dbs = append(dbs, line)
		}
	}
	return dbs, nil
}

// DiscoverEmailDomains lists mail domains on the source server.
func DiscoverEmailDomains(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass, "ls /var/mail/vhosts/ 2>/dev/null || echo ''")
	if err != nil {
		return nil, err
	}
	return parseLines(result.Output), nil
}

// DiscoverHostname returns the hostname of the source server.
func DiscoverHostname(ctx context.Context, host string, port int, user, pass string) (string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass, "hostname -f 2>/dev/null || hostname")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Output), nil
}

// DiscoverDNSZones lists DNS zones from PowerDNS on the source.
func DiscoverDNSZones(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass, `pdnsutil list-all-zones 2>/dev/null || echo ''`)
	if err != nil {
		return nil, err
	}
	return parseLines(result.Output), nil
}

// DiscoverSSLDomains lists domains that have SSL certificates on the source.
func DiscoverSSLDomains(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass, `ls /etc/letsencrypt/live/ 2>/dev/null | grep -v README || echo ''`)
	if err != nil {
		return nil, err
	}
	return parseLines(result.Output), nil
}

// DiscoverCronUsers lists users who have crontabs on the source.
func DiscoverCronUsers(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass, `ls /var/spool/cron/crontabs/ 2>/dev/null || ls /var/spool/cron/ 2>/dev/null || echo ''`)
	if err != nil {
		return nil, err
	}
	return parseLines(result.Output), nil
}

// DiscoverFTPUsers lists FTP users from Pure-FTPd on the source.
func DiscoverFTPUsers(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass, `pure-pw list 2>/dev/null | awk '{print $1}' || echo ''`)
	if err != nil {
		return nil, err
	}
	return parseLines(result.Output), nil
}

// ExportDNSZoneFromRemote exports a DNS zone file from the source server.
func ExportDNSZoneFromRemote(ctx context.Context, host string, port int, user, pass, domain string) (string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass, fmt.Sprintf(`pdnsutil list-zone %s 2>/dev/null`, domain))
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

// ExportSSLFromRemote downloads SSL cert files for a domain from the source.
func ExportSSLFromRemote(ctx context.Context, host string, port int, user, pass, domain, localDir string) error {
	certDir := fmt.Sprintf("/etc/letsencrypt/live/%s", domain)
	return SCPDownload(ctx, host, port, user, pass, certDir, localDir)
}

// RemoteMongoDump runs mongodump on the source and downloads the archive.
func RemoteMongoDump(ctx context.Context, host string, port int, user, pass, dbName, localPath string) error {
	remoteTmp := fmt.Sprintf("/tmp/transfer-dump-%s.gz", dbName)
	// Run mongodump on source
	_, err := SSHCommand(ctx, host, port, user, pass,
		fmt.Sprintf("mongodump --archive=%s --gzip --db %s", remoteTmp, dbName))
	if err != nil {
		return fmt.Errorf("remote mongodump failed: %w", err)
	}
	// Download the dump
	if err := SCPDownload(ctx, host, port, user, pass, remoteTmp, localPath); err != nil {
		return fmt.Errorf("download dump failed: %w", err)
	}
	// Cleanup remote temp file
	SSHCommand(ctx, host, port, user, pass, fmt.Sprintf("rm -f %s", remoteTmp))
	return nil
}

// RemoteBackupUserFiles creates a tarball of a user's home directory on source and downloads it.
func RemoteBackupUserFiles(ctx context.Context, host string, port int, user, pass, sysUser, localPath string) error {
	remoteTmp := fmt.Sprintf("/tmp/transfer-files-%s.tar.gz", sysUser)
	_, err := SSHCommand(ctx, host, port, user, pass,
		fmt.Sprintf("tar -czf %s -C /home %s 2>/dev/null", remoteTmp, sysUser))
	if err != nil {
		return fmt.Errorf("remote file backup failed: %w", err)
	}
	if err := SCPDownload(ctx, host, port, user, pass, remoteTmp, localPath); err != nil {
		return fmt.Errorf("download files failed: %w", err)
	}
	SSHCommand(ctx, host, port, user, pass, fmt.Sprintf("rm -f %s", remoteTmp))
	return nil
}

// RemoteBackupEmail creates a tarball of email data from source and downloads it.
func RemoteBackupEmail(ctx context.Context, host string, port int, user, pass, domain, localPath string) error {
	remoteTmp := fmt.Sprintf("/tmp/transfer-email-%s.tar.gz", domain)
	_, err := SSHCommand(ctx, host, port, user, pass,
		fmt.Sprintf("tar -czf %s -C /var/mail/vhosts %s 2>/dev/null", remoteTmp, domain))
	if err != nil {
		return fmt.Errorf("remote email backup failed: %w", err)
	}
	if err := SCPDownload(ctx, host, port, user, pass, remoteTmp, localPath); err != nil {
		return fmt.Errorf("download email failed: %w", err)
	}
	SSHCommand(ctx, host, port, user, pass, fmt.Sprintf("rm -f %s", remoteTmp))
	return nil
}

// ExportCrontabFromRemote gets crontab entries for a user from the source.
func ExportCrontabFromRemote(ctx context.Context, host string, port int, user, pass, cronUser string) (string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass,
		fmt.Sprintf("crontab -u %s -l 2>/dev/null || echo ''", cronUser))
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

func parseLines(output string) []string {
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
