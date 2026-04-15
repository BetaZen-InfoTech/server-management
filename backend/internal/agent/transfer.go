package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"golang.org/x/crypto/ssh"
)

// sshDial creates an SSH client connection using native Go SSH.
func sshDial(host string, port int, user, pass string) (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
			ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = pass
				}
				return answers, nil
			}),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	return ssh.Dial("tcp", addr, config)
}

// SSHCommand runs a command on a remote server via native Go SSH.
func SSHCommand(ctx context.Context, host string, port int, user, pass, command string) (*CommandResult, error) {
	client, err := sshDial(host, port, user, pass)
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh session failed: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Run(command)
	result := &CommandResult{
		Output: stdout.String(),
		Error:  stderr.String(),
	}
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			result.ExitCode = exitErr.ExitStatus()
			return result, nil
		}
		return result, fmt.Errorf("ssh exec failed: %w", err)
	}
	return result, nil
}

// SCPDownload downloads a single remote FILE to localPath by streaming the
// remote file's bytes over an SSH session's stdout. The remote path MUST
// reference a regular file — for directory transfers, tar the directory on
// the remote first and download the resulting tarball.
//
// History: an earlier implementation tried to be clever about supporting
// both files and directories by tar-piping over SSH and calling
// `os.MkdirAll(localPath, 0755)`. That turned every caller's intended
// *file* path into a *directory*, and the tar pipe was never wired to a
// reader, so nothing was ever extracted. Downstream `tar -xzf` then
// failed with "Cannot read: Is a directory" — which is exactly the
// transfer error this function caused. Keep this simple.
func SCPDownload(ctx context.Context, host string, port int, user, pass, remotePath, localPath string) error {
	client, err := sshDial(host, port, user, pass)
	if err != nil {
		return fmt.Errorf("ssh connect failed: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session failed: %w", err)
	}
	defer session.Close()

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("create local dir failed: %w", err)
	}
	// If a previous broken run left a directory at localPath, clear it so
	// os.Create can produce a regular file in its place.
	if info, statErr := os.Stat(localPath); statErr == nil && info.IsDir() {
		_ = os.RemoveAll(localPath)
	}
	out, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create local file failed: %w", err)
	}
	defer out.Close()

	var stderr bytes.Buffer
	session.Stdout = out
	session.Stderr = &stderr

	if err := session.Run(fmt.Sprintf("cat %s", shellSingleQuote(remotePath))); err != nil {
		_ = os.Remove(localPath)
		return fmt.Errorf("remote cat failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	// Reject empty downloads — usually means the remote file did not exist
	// but `cat` exited 0 because of a redirect quirk.
	if info, statErr := os.Stat(localPath); statErr == nil && info.Size() == 0 {
		_ = os.Remove(localPath)
		return fmt.Errorf("downloaded file is empty (remote: %s)", remotePath)
	}
	return nil
}

// shellSingleQuote wraps s in POSIX single-quotes, escaping any embedded
// single quote with the standard '\'' sequence.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// SCPUpload uploads a file/directory to a remote server using native SSH.
func SCPUpload(ctx context.Context, host string, port int, user, pass, localPath, remotePath string) error {
	client, err := sshDial(host, port, user, pass)
	if err != nil {
		return fmt.Errorf("ssh connect failed: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session failed: %w", err)
	}
	defer session.Close()

	// Create tar locally, pipe to remote
	var stderr bytes.Buffer
	session.Stderr = &stderr

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe failed: %w", err)
	}

	remoteCmd := fmt.Sprintf("mkdir -p '%s' && tar -xf - -C '%s'", remotePath, remotePath)
	if err := session.Start(remoteCmd); err != nil {
		return fmt.Errorf("remote extract failed: %w", err)
	}

	// Create tar from local path and pipe to session stdin
	tarResult, err := RunCommand(ctx, "bash", "-c", fmt.Sprintf("tar -cf - -C '%s' .", localPath))
	if err != nil {
		stdinPipe.Close()
		return fmt.Errorf("local tar failed: %w", err)
	}
	stdinPipe.Write([]byte(tarResult.Output))
	stdinPipe.Close()

	session.Wait()
	return nil
}

// DetectServerType identifies the control panel / server type on the source.
func DetectServerType(ctx context.Context, host string, port int, user, pass string) (string, error) {
	cmd := `if [ -f /usr/local/cpanel/cpanel ]; then echo cpanel;
elif [ -f /usr/local/psa/version ]; then echo plesk;
elif [ -f /usr/local/directadmin/directadmin ]; then echo directadmin;
elif [ -d /opt/serverpanel ]; then echo serverpanel;
elif [ -f /etc/cyberpanel/machineIP ]; then echo cyberpanel;
else echo bare; fi`
	result, _ := SSHCommand(ctx, host, port, user, pass, cmd)
	if result == nil {
		return "bare", nil
	}
	out := strings.TrimSpace(result.Output)
	if out == "" {
		return "bare", nil
	}
	return out, nil
}

// DiscoverDomains lists domains from multiple common locations on the source server.
// Supports: ServerPanel, cPanel/WHM, Plesk, DirectAdmin, bare nginx/apache setups.
func DiscoverDomains(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	cmd := `{
		# ServerPanel / custom setups
		ls /home/*/domains/ 2>/dev/null;
		# cPanel/WHM
		cat /etc/trueuserdomains 2>/dev/null | awk '{print $1}' | tr -d ':';
		cat /etc/localdomains 2>/dev/null;
		cat /etc/userdatadomains 2>/dev/null | awk -F'==' '{print $1}' | awk -F: '{print $1}';
		# Plesk
		ls /var/www/vhosts/ 2>/dev/null;
		cat /etc/psa/psa.conf 2>/dev/null && mysql -N -e "SELECT name FROM domains" psa 2>/dev/null;
		# DirectAdmin
		cat /etc/virtual/domainowners 2>/dev/null | awk -F: '{print $1}';
		# Nginx configs
		grep -rh 'server_name ' /etc/nginx/sites-available/ /etc/nginx/conf.d/ 2>/dev/null | sed 's/.*server_name //;s/;.*//' | tr ' ' '\n';
		# Apache configs
		grep -rh 'ServerName\|ServerAlias' /etc/apache2/sites-available/ /etc/httpd/conf.d/ /etc/apache2/conf.d/ /usr/local/apache/conf/ 2>/dev/null | awk '{print $2}' | tr ' ' '\n';
		# Home dirs with public_html (common layout)
		for d in /home/*/public_html; do [ -d "$d" ] && basename $(dirname "$d"); done 2>/dev/null;
	} | sort -u | awk 'NF && /\./ && !/default|localhost|_|ssl|cgi-bin|error|chroot/' || true`
	result, err := SSHCommand(ctx, host, port, user, pass, cmd)
	if err != nil {
		return []string{}, err
	}
	return parseLines(result.Output), nil
}

// DiscoverDatabases lists MongoDB databases on the source server.
// Tries direct mongosh first, then falls back to ServerPanel's .env MONGODB_URI.
func DiscoverDatabases(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	cmd := `set +e
EVAL='db.adminCommand({listDatabases:1}).databases.forEach(function(d){print(d.name)})'
out=$(mongosh --quiet --eval "$EVAL" 2>/dev/null)
if [ -z "$out" ]; then
  for env in /opt/serverpanel/.env /opt/serverpanel/backend/.env; do
    if [ -f "$env" ]; then
      uri=$(grep -E '^(MONGODB_URI|MONGO_URI)=' "$env" | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'")
      if [ -n "$uri" ]; then
        out=$(mongosh "$uri" --quiet --eval "$EVAL" 2>/dev/null)
        [ -n "$out" ] && break
      fi
    fi
  done
fi
echo "$out"
exit 0`
	result, err := SSHCommand(ctx, host, port, user, pass, cmd)
	if err != nil {
		return []string{}, err
	}
	dbs := []string{}
	for _, line := range parseLines(result.Output) {
		if line != "admin" && line != "local" && line != "config" {
			dbs = append(dbs, line)
		}
	}
	return dbs, nil
}

// DiscoverMySQLDatabases lists MySQL/MariaDB databases on the source server.
func DiscoverMySQLDatabases(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass,
		`mysql -N -e "SHOW DATABASES" 2>/dev/null || echo ''`)
	if err != nil {
		return []string{}, err
	}
	dbs := []string{}
	systemDBs := map[string]bool{"information_schema": true, "mysql": true, "performance_schema": true, "sys": true, "phpmyadmin": true}
	for _, line := range parseLines(result.Output) {
		if !systemDBs[line] {
			dbs = append(dbs, line)
		}
	}
	return dbs, nil
}

// RemoteMySQLDump runs mysqldump on the source and downloads the archive.
func RemoteMySQLDump(ctx context.Context, host string, port int, user, pass, dbName, localPath string) error {
	remoteTmp := fmt.Sprintf("/tmp/transfer-mysqldump-%s.sql.gz", dbName)
	_, err := SSHCommand(ctx, host, port, user, pass,
		fmt.Sprintf("mysqldump --single-transaction --routines --triggers --events %s 2>/dev/null | gzip > %s", dbName, remoteTmp))
	if err != nil {
		return fmt.Errorf("remote mysqldump failed: %w", err)
	}
	if err := SCPDownload(ctx, host, port, user, pass, remoteTmp, localPath); err != nil {
		return fmt.Errorf("download mysql dump failed: %w", err)
	}
	SSHCommand(ctx, host, port, user, pass, fmt.Sprintf("rm -f %s", remoteTmp))
	return nil
}

// RestoreMySQL restores a MySQL dump on the local server.
func RestoreMySQL(ctx context.Context, dbName, archivePath string) error {
	// Create database if not exists
	RunCommand(ctx, "mysql", "-e", fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`;", dbName))
	// Restore from gzipped dump
	_, err := RunCommand(ctx, "bash", "-c", fmt.Sprintf("gunzip -c %s | mysql %s", archivePath, dbName))
	return err
}

// DiscoverMySQLUsers lists MySQL users and their grants for a specific database from the source.
func DiscoverMySQLUsers(ctx context.Context, host string, port int, user, pass, dbName string) ([]map[string]string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass,
		fmt.Sprintf(`mysql -N -e "SELECT user, host FROM mysql.db WHERE db='%s'" 2>/dev/null || echo ''`, dbName))
	if err != nil {
		return nil, err
	}
	var users []map[string]string
	for _, line := range parseLines(result.Output) {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			users = append(users, map[string]string{"username": parts[0], "host": parts[1]})
		}
	}
	return users, nil
}

// DiscoverEmailForwarders exports email alias/forwarder mappings from source.
func DiscoverEmailForwarders(ctx context.Context, host string, port int, user, pass, domain string) (string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass,
		fmt.Sprintf(`grep '@%s' /etc/postfix/virtual_alias_maps 2>/dev/null || grep '@%s' /etc/aliases 2>/dev/null || echo ''`, domain, domain))
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

// DiscoverEmailDomains lists mail domains on the source server.
func DiscoverEmailDomains(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	cmd := `{
		ls /var/mail/vhosts/ 2>/dev/null;
		awk '{print $1}' /etc/postfix/virtual_domains 2>/dev/null;
		awk '{print $1}' /etc/postfix/virtual_mailbox_domains 2>/dev/null;
		awk -F: '{print $1}' /etc/dovecot/users 2>/dev/null | awk -F@ 'NF==2{print $2}';
		for d in /home/*/mail/*; do [ -d "$d" ] && basename "$d"; done 2>/dev/null;
	} 2>/dev/null | sort -u | awk 'NF && /^[A-Za-z0-9._-]+\.[A-Za-z0-9._-]+$/' || true`
	result, err := SSHCommand(ctx, host, port, user, pass, cmd)
	if err != nil {
		return []string{}, err
	}
	return parseLines(result.Output), nil
}

// DiscoverHostname returns the hostname of the source server.
func DiscoverHostname(ctx context.Context, host string, port int, user, pass string) (string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass,
		`h=$(hostname -f 2>/dev/null); [ -n "$h" ] || h=$(hostname 2>/dev/null); [ -n "$h" ] || h=$(cat /etc/hostname 2>/dev/null); echo "$h"`)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Output), nil
}

// DiscoverDNSZones lists DNS zones from PowerDNS, BIND, or zone files on the source.
func DiscoverDNSZones(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	cmd := `{
		pdnsutil list-all-zones 2>/dev/null;
		ls /etc/bind/zones/ 2>/dev/null | sed 's/\.zone$//';
		grep 'zone "' /etc/bind/named.conf.local 2>/dev/null | awk -F'"' '{print $2}';
		ls /var/named/ 2>/dev/null | sed 's/\.zone$//';
	} 2>/dev/null | sort -u | awk 'NF && /^[A-Za-z0-9._-]+\.[A-Za-z0-9._-]+$/' || true`
	result, err := SSHCommand(ctx, host, port, user, pass, cmd)
	if err != nil {
		return []string{}, err
	}
	return parseLines(result.Output), nil
}

// DiscoverSSLDomains lists domains that have SSL certificates on the source.
func DiscoverSSLDomains(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	cmd := `{
		ls /etc/letsencrypt/live/ 2>/dev/null;
		ls /etc/ssl/custom/ 2>/dev/null;
	} 2>/dev/null | sort -u | awk 'NF && /\./ && !/README|snakeoil|ca-certificates/' || true`
	result, err := SSHCommand(ctx, host, port, user, pass, cmd)
	if err != nil {
		return []string{}, err
	}
	return parseLines(result.Output), nil
}

// DiscoverCronUsers lists users who have crontabs on the source.
func DiscoverCronUsers(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass, `ls /var/spool/cron/crontabs/ 2>/dev/null || ls /var/spool/cron/ 2>/dev/null || echo ''`)
	if err != nil {
		return []string{}, err
	}
	return parseLines(result.Output), nil
}

// DiscoverNodeApps lists PM2-managed Node.js apps on the source server.
// Uses `pm2 jlist` (JSON) to get the authoritative runtime state. Falls back
// to scanning for ecosystem.config.js / package.json in common app roots
// when PM2 isn't installed so at least *something* is reported.
func DiscoverNodeApps(ctx context.Context, host string, port int, user, pass string) ([]models.NodeApp, error) {
	apps := []models.NodeApp{}

	// Primary: pm2 jlist gives us every live process with exec mode + cwd.
	result, _ := SSHCommand(ctx, host, port, user, pass,
		`pm2 jlist 2>/dev/null || su - root -c 'pm2 jlist' 2>/dev/null || echo '[]'`)
	if result != nil {
		raw := strings.TrimSpace(result.Output)
		if strings.HasPrefix(raw, "[") {
			var procs []struct {
				Name        string `json:"name"`
				PmExecPath  string `json:"pm_exec_path"`
				PmCwd       string `json:"pm_cwd"`
				PmExecMode  string `json:"exec_mode"`
				PmInstances int    `json:"instances"`
				PM2Env      struct {
					PmCwd      string      `json:"pm_cwd"`
					PmExecPath string      `json:"pm_exec_path"`
					ExecMode   string      `json:"exec_mode"`
					Instances  interface{} `json:"instances"`
					NodeVer    string      `json:"node_version"`
				} `json:"pm2_env"`
			}
			if err := json.Unmarshal([]byte(raw), &procs); err == nil {
				for _, p := range procs {
					// Skip PM2's own helper modules (pm2-logrotate, pm2-health,
					// pm2-auto-pull, ...). They're installed via `pm2 install`
					// and show up in `pm2 jlist` just like user apps, but they
					// aren't code the operator wants to migrate — their data
					// lives in ~/.pm2 and they re-install as modules on the
					// destination.
					if isPM2InternalModule(p.Name) {
						continue
					}
					cwd := p.PmCwd
					if cwd == "" {
						cwd = p.PM2Env.PmCwd
					}
					script := p.PmExecPath
					if script == "" {
						script = p.PM2Env.PmExecPath
					}
					mode := p.PmExecMode
					if mode == "" {
						mode = p.PM2Env.ExecMode
					}
					instances := p.PmInstances
					if instances == 0 {
						if n, ok := p.PM2Env.Instances.(float64); ok {
							instances = int(n)
						}
					}
					if instances == 0 {
						instances = 1
					}
					apps = append(apps, models.NodeApp{
						Name:      p.Name,
						Cwd:       cwd,
						Script:    script,
						ExecMode:  mode,
						Instances: instances,
						NodeVer:   p.PM2Env.NodeVer,
					})
				}
			}
		}
	}

	// If pm2 returned nothing, fall back to filesystem scan for Node projects.
	if len(apps) == 0 {
		fbRes, _ := SSHCommand(ctx, host, port, user, pass,
			`find /home /var/www /opt -maxdepth 4 -type f \( -name ecosystem.config.js -o -name ecosystem.config.cjs \) 2>/dev/null | head -30`)
		if fbRes != nil {
			for _, line := range parseLines(fbRes.Output) {
				dir := filepath.Dir(line)
				name := filepath.Base(dir)
				apps = append(apps, models.NodeApp{
					Name:     name,
					Cwd:      dir,
					Script:   line,
					ExecMode: "fork",
				})
			}
		}
	}

	// Annotate with npm manager (lockfile presence).
	for i := range apps {
		if apps[i].Cwd == "" {
			continue
		}
		probe := fmt.Sprintf(
			`if [ -f %q/pnpm-lock.yaml ]; then echo pnpm; elif [ -f %q/yarn.lock ]; then echo yarn; elif [ -f %q/package-lock.json ]; then echo npm; else echo npm; fi`,
			apps[i].Cwd, apps[i].Cwd, apps[i].Cwd)
		if r, _ := SSHCommand(ctx, host, port, user, pass, probe); r != nil {
			apps[i].NpmManager = strings.TrimSpace(r.Output)
		}
	}

	return apps, nil
}

// isPM2InternalModule reports whether a PM2 process name belongs to a PM2
// module rather than a user application. Modules are installed with
// `pm2 install <pkg>`, live as regular processes in `pm2 jlist`, and should
// never be migrated as source code — `pm2 install` on the destination
// recreates them. The canonical examples are pm2-logrotate, pm2-health,
// pm2-auto-pull, pm2-server-monit, pm2-slack.
func isPM2InternalModule(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	return strings.HasPrefix(n, "pm2-") || strings.HasPrefix(n, "pm2_")
}

// DiscoverFTPUsers lists FTP users from Pure-FTPd on the source.
func DiscoverFTPUsers(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	result, err := SSHCommand(ctx, host, port, user, pass, `pure-pw list 2>/dev/null | awk '{print $1}' || echo ''`)
	if err != nil {
		return []string{}, err
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
// The cert dir is a directory tree, and SCPDownload only handles regular
// files, so we tar it on the remote, download the tarball, and extract it
// into localDir. The resulting layout is localDir/{domain}/<cert files>,
// which matches what the caller in transfer_service.go expects.
func ExportSSLFromRemote(ctx context.Context, host string, port int, user, pass, domain, localDir string) error {
	remoteTmp := fmt.Sprintf("/tmp/transfer-ssl-%s.tar.gz", domain)
	tarCmd := fmt.Sprintf("tar -czf %s -C /etc/letsencrypt/live %s 2>/dev/null",
		shellSingleQuote(remoteTmp), shellSingleQuote(domain))
	if _, err := SSHCommand(ctx, host, port, user, pass, tarCmd); err != nil {
		return fmt.Errorf("remote tar ssl failed: %w", err)
	}
	defer SSHCommand(ctx, host, port, user, pass, fmt.Sprintf("rm -f %s", shellSingleQuote(remoteTmp)))

	if err := os.MkdirAll(localDir, 0750); err != nil {
		return fmt.Errorf("create local ssl dir failed: %w", err)
	}
	localTar := filepath.Join(localDir, fmt.Sprintf("ssl-%s.tar.gz", domain))
	if err := SCPDownload(ctx, host, port, user, pass, remoteTmp, localTar); err != nil {
		return fmt.Errorf("download ssl tar failed: %w", err)
	}
	defer os.Remove(localTar)

	if _, err := RunCommand(ctx, "tar", "-xzf", localTar, "-C", localDir); err != nil {
		return fmt.Errorf("extract ssl tar failed: %w", err)
	}
	return nil
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

// RemoteTarNodeApp tarballs a Node app's working directory on the source
// (excluding node_modules to keep transfer small), downloads the archive,
// and returns the local path to the .tar.gz.
func RemoteTarNodeApp(ctx context.Context, host string, port int, user, pass, remoteCwd, localPath string) error {
	if remoteCwd == "" {
		return fmt.Errorf("empty remote cwd")
	}
	remoteTmp := fmt.Sprintf("/tmp/transfer-nodeapp-%d.tar.gz", time.Now().UnixNano())
	// Exclude node_modules + .next cache + common build artefacts — the
	// destination will reinstall them cleanly. Also exclude .git unless
	// the app relies on git metadata at runtime (rare).
	tarCmd := fmt.Sprintf(
		`tar --exclude=node_modules --exclude=.next/cache --exclude=.cache --exclude=.pnpm-store --exclude=dist/cache -czf %s -C %q . 2>/dev/null`,
		remoteTmp, remoteCwd)
	if _, err := SSHCommand(ctx, host, port, user, pass, tarCmd); err != nil {
		return fmt.Errorf("remote tar failed: %w", err)
	}
	if err := SCPDownload(ctx, host, port, user, pass, remoteTmp, localPath); err != nil {
		return fmt.Errorf("download node app tar failed: %w", err)
	}
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
