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

// sshKeyContextKey is the context key under which transfer-token mode
// stashes a PEM-encoded private key. When present, sshDial uses public-key
// auth and skips password/keyboard-interactive entirely. Unexported so
// callers must go through WithSSHKey.
type sshKeyContextKey struct{}

// WithSSHKey returns a derived context that carries privateKeyPEM. Any
// agent SSH operation (SSHCommand, SCPDownload, SCPUpload, …) executed
// with this context will authenticate using the key instead of the
// password argument. The pass argument is still accepted by the function
// signatures but is ignored when a key is supplied — keeping the
// signatures stable means the per-discovery-function call sites in
// transfer_service.go don't need to grow a second parameter.
func WithSSHKey(ctx context.Context, privateKeyPEM string) context.Context {
	if privateKeyPEM == "" {
		return ctx
	}
	return context.WithValue(ctx, sshKeyContextKey{}, privateKeyPEM)
}

// sshDial creates an SSH client connection using native Go SSH. When the
// context carries a private key (see WithSSHKey), public-key auth is used
// and the password is ignored.
func sshDial(ctx context.Context, host string, port int, user, pass string) (*ssh.Client, error) {
	auths := []ssh.AuthMethod{}
	if keyPEM, _ := ctx.Value(sshKeyContextKey{}).(string); keyPEM != "" {
		signer, err := ssh.ParsePrivateKey([]byte(keyPEM))
		if err != nil {
			return nil, fmt.Errorf("parse transfer-token private key: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	} else {
		auths = append(auths,
			ssh.Password(pass),
			ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = pass
				}
				return answers, nil
			}),
		)
	}
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	return ssh.Dial("tcp", addr, config)
}

// SSHCommand runs a command on a remote server via native Go SSH.
func SSHCommand(ctx context.Context, host string, port int, user, pass, command string) (*CommandResult, error) {
	client, err := sshDial(ctx, host, port, user, pass)
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
	client, err := sshDial(ctx, host, port, user, pass)
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
	client, err := sshDial(ctx, host, port, user, pass)
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
	// Strip "www." aliases — nginx's server_name typically lists both
	// "example.com www.example.com" and the script picks both up. Treating
	// www.* as a separate domain causes the transfer step to either dup
	// the directory structure or, worse, synthesise a fake "www_example_com"
	// linux user when /home/*/domains/www.example.com doesn't exist on
	// the source. The bare domain row already covers the www variant via
	// the vhost's server_name list and the SSL cert's --expand entry.
	bare := make([]string, 0, len(result.Output))
	seen := map[string]bool{}
	for _, d := range parseLines(result.Output) {
		d = strings.TrimSpace(d)
		stripped := strings.TrimPrefix(d, "www.")
		if seen[stripped] {
			continue
		}
		seen[stripped] = true
		bare = append(bare, stripped)
	}
	return bare, nil
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

// DiscoverLinuxUsers enumerates every hosting account under /home on the
// source. The wizard uses this list as the primary selection unit — when
// the operator picks a user, the transfer cascades to that user's domains,
// mailboxes, FTP accounts, MySQL DBs, cron jobs, etc., so they don't have
// to tick eight cards individually.
//
// The remote script emits one tab-separated line per user:
//
//	username UID home shell locked_or_active domains mailboxes dbs ftp cron node wp home_bytes
//
// Inactive (locked / nologin) accounts are still returned because their
// data may still need to migrate. The caller decides whether to default
// them on or off.
// panelManagedUsernames returns the set of linux usernames that have a
// row in the source's panel `users` collection — i.e. the operator
// created them through the WHM, so they're real vendors worth
// migrating. Empty result on non-Betazen sources or when mongo isn't
// reachable; the caller treats every user as panel-managed in that
// case so cPanel/Plesk/bare migrations still default-select something
// sensible.
func panelManagedUsernames(ctx context.Context, host string, port int, user, pass string) map[string]bool {
	cmd := `set +e
URI=$(grep -E '^(MONGODB_URI|MONGO_URI)=' /opt/serverpanel/.env 2>/dev/null | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'")
[ -z "$URI" ] && URI="mongodb://localhost:27017/serverpanel"
if command -v mongosh >/dev/null 2>&1; then
    mongosh "$URI" --quiet --eval 'db.users.find({username:{$ne:""}},{username:1,_id:0}).forEach(d => print(d.username))' 2>/dev/null
fi
exit 0`
	result, err := SSHCommand(ctx, host, port, user, pass, cmd)
	if err != nil || result == nil {
		return nil
	}
	out := map[string]bool{}
	for _, line := range parseLines(result.Output) {
		line = strings.TrimSpace(line)
		if line != "" {
			out[line] = true
		}
	}
	return out
}

func DiscoverLinuxUsers(ctx context.Context, host string, port int, user, pass string) ([]models.LinuxUser, error) {
	// One-shot probe of every /home/* account on the source. The output
	// format is tab-separated; the parser below is order-sensitive so
	// don't reorder fields without updating it too.
	//
	// Counting nuances we hit on real customer boxes during testing:
	//
	//   * FTP must come from pure-pw — the panel creates Pure-FTPd
	//     virtual users, NOT system FTP accounts. The first cut grepped
	//     /etc/passwd for "^${name}:" which always matched the user's
	//     own row and reported "1 ftp" for everyone.
	//
	//   * `passwd -S` returns "L" for any account without a password
	//     set, including SSH-key-only tenant accounts the panel creates.
	//     Treating that as "locked" mislabels every functional vendor as
	//     locked. We now classify on shell only — a user with a real
	//     login shell is "active" regardless of password state. The
	//     legacy `Locked` field is still populated from passwd -S so the
	//     model can surface it as a secondary signal.
	//
	//   * Mailboxes live in several places depending on how email was
	//     installed: /etc/dovecot/users{,.d/*}, /etc/dovecot/passwd, or
	//     per-domain Maildirs under /home/<user>/mail/<domain>/<box>.
	//     We sum all three so the count matches what the operator sees
	//     in the Email page.
	cmd := `set +e
list_users() {
    awk -F: '$3 >= 1000 && $3 < 65534 && $6 ~ /^\/home\// {print $1":"$3":"$6":"$7}' /etc/passwd
}

# Pre-compute the FTP roster ONCE — pure-pw list is slow and we don't
# want to fork it per user. Output is "<account>\t<chroot-dir>" per row.
ftp_roster=""
if command -v pure-pw >/dev/null 2>&1; then
    ftp_roster=$(pure-pw list 2>/dev/null)
fi

# Pre-compute the mysql DB list ONCE for the same reason.
mysql_dbs=""
if command -v mysql >/dev/null 2>&1; then
    mysql_dbs=$(mysql -N -e "SHOW DATABASES" 2>/dev/null)
fi

for entry in $(list_users); do
    name=${entry%%:*}
    rest=${entry#*:}
    uid=${rest%%:*}
    rest=${rest#*:}
    home=${rest%:*}
    shell=${rest##*:}

    # active = "shell allows login". Independent of password state, since
    # SSH-key-only accounts without a unix password are still functional
    # hosting users.
    case "$shell" in
        */nologin|*/false|"") active=0 ;;
        *) active=1 ;;
    esac

    # locked = "unix password is locked". Reported separately so the UI
    # can show it as a secondary chip, but does NOT flip active to 0.
    locked=0
    pst=$(passwd -S "$name" 2>/dev/null | awk '{print $2}')
    if [ "$pst" = "L" ] || [ "$pst" = "LK" ]; then locked=1; fi

    domains=$(ls -1 "$home/domains/" 2>/dev/null | wc -l)

    # Mailboxes — sum of dovecot virtual users (passwd-style or users.d
    # snippets) plus per-domain Maildirs on disk. Any path missing just
    # contributes 0.
    mb_pw=$(grep -c "^${name}@" /etc/dovecot/passwd 2>/dev/null)
    [ -z "$mb_pw" ] && mb_pw=0
    mb_users=$(find /etc/dovecot/users.d -maxdepth 1 -type f -name "${name}.*" 2>/dev/null | wc -l)
    mb_maildir=$(find "$home/mail" -mindepth 2 -maxdepth 2 -type d 2>/dev/null | wc -l)
    mb_top=$(find "$home/Maildir" -maxdepth 0 -type d 2>/dev/null | wc -l)
    mailboxes=$((mb_pw + mb_users + mb_maildir + mb_top))

    # MySQL DBs whose name carries the panel-standard "<user>_" prefix.
    dbs=$(printf '%s\n' "$mysql_dbs" | awk -v p="${name}_" 'index($0, p)==1' | wc -l)

    # FTP accounts — pure-pw entries whose chroot dir lives under this
    # user's home. Account name conventions vary ("vendor", "vendor_dev",
    # "vendor@example.com", …) so the chroot path is the reliable key.
    if [ -n "$ftp_roster" ]; then
        ftp=$(printf '%s\n' "$ftp_roster" | awk -v h="$home" '$0 ~ h {n++} END{print n+0}')
    else
        ftp=0
    fi

    cron=$(crontab -u "$name" -l 2>/dev/null | grep -cv '^[[:space:]]*\($\|#\)')
    [ -z "$cron" ] && cron=0

    # PM2 dump file existence (1 = at least one app), good enough for
    # the wizard's preview chip without parsing the binary dump.
    nodeapps=$(ls -1 "$home/.pm2/dump.pm2" 2>/dev/null | wc -l)

    wp=$(find "$home/domains" -mindepth 2 -maxdepth 4 -type f -name wp-config.php 2>/dev/null | wc -l)
    bytes=$(du -sb "$home" 2>/dev/null | awk '{print $1}')
    [ -z "$bytes" ] && bytes=0

    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$name" "$uid" "$home" "$shell" "$active$locked" \
        "$domains" "$mailboxes" "$dbs" "$ftp" "$cron" "$nodeapps" "$wp" "$bytes"
done
exit 0`
	result, err := SSHCommand(ctx, host, port, user, pass, cmd)
	if err != nil {
		return []models.LinuxUser{}, err
	}
	// Panel-managed lookup runs in parallel-ish (sequential SSH but the
	// roundtrip is cheap once we already have a session warmed up by
	// the main script). Fail-soft: a nil set means "we don't know", and
	// the caller below treats every user as panel-managed in that case
	// so the wizard still defaults something on for non-Betazen sources.
	panelSet := panelManagedUsernames(ctx, host, port, user, pass)
	knowsPanelSet := panelSet != nil

	out := []models.LinuxUser{}
	for _, line := range parseLines(result.Output) {
		fields := strings.Split(line, "\t")
		if len(fields) < 13 {
			continue
		}
		state := fields[4] // "AL" — first char active(1/0), second locked(1/0).
		active := len(state) >= 1 && state[0] == '1'
		locked := len(state) >= 2 && state[1] == '1'
		// Active and Locked are independent now: an SSH-key-only tenant
		// account has no unix password (locked) but still has a real
		// shell (active). The wizard wants both bits to draw the right
		// chip set without conflating them.
		username := fields[0]
		domains := atoiSafe(fields[5])
		mailboxes := atoiSafe(fields[6])
		databases := atoiSafe(fields[7])
		ftpUsers := atoiSafe(fields[8])
		nodeApps := atoiSafe(fields[10])
		wpSites := atoiSafe(fields[11])

		// "Panel-managed" = "this account is worth migrating as a hosting
		// account". Two ways to qualify:
		//
		//   1. The source's panel `users` collection has a row for it
		//      (cleanest signal — operator created them through WHM).
		//   2. The account owns hosting data on disk (domains, mailboxes,
		//      databases, FTP, Node apps, or WordPress installs). Many
		//      panel-created vendors predate the users-table convention or
		//      were provisioned through auxiliary paths and have no row;
		//      excluding them just because the table is empty would make
		//      the wizard skip the very accounts the operator wants.
		//
		// `ubuntu`-style OS accounts have zero hosting data and no users
		// row, so they fail both checks → flagged as OS user, not
		// pre-selected.
		hasData := domains > 0 || mailboxes > 0 || databases > 0 ||
			ftpUsers > 0 || nodeApps > 0 || wpSites > 0
		panelManaged := hasData || (knowsPanelSet && panelSet[username]) || !knowsPanelSet

		out = append(out, models.LinuxUser{
			Username:     username,
			UID:          atoiSafe(fields[1]),
			Home:         fields[2],
			Shell:        fields[3],
			Active:       active,
			Locked:       locked,
			Domains:      domains,
			Mailboxes:    mailboxes,
			Databases:    databases,
			FTPUsers:     ftpUsers,
			CronJobs:     atoiSafe(fields[9]),
			NodeApps:     nodeApps,
			WPSites:      wpSites,
			HomeBytes:    atoi64Safe(fields[12]),
			PanelManaged: panelManaged,
		})
	}
	return out, nil
}

// DiscoverDomainSettings returns per-domain configuration the wizard
// surfaces in step 2: PHP version, document root, SSL state, WP marker.
// We parse nginx vhosts (the source's authoritative web config) and
// fall back to public_html scans for owners. Best-effort; on a sparse
// source the list may be shorter than DiscoverDomains.
func DiscoverDomainSettings(ctx context.Context, host string, port int, user, pass string) ([]models.DomainSetting, error) {
	cmd := `set +e
shopt -s nullglob 2>/dev/null
for f in /etc/nginx/sites-available/* /etc/nginx/sites-enabled/* /etc/nginx/conf.d/*.conf; do
    [ -f "$f" ] || continue
    name=$(grep -m1 -E '^\s*server_name\s+' "$f" 2>/dev/null | awk '{print $2}' | tr -d ';')
    [ -z "$name" ] && continue
    case "$name" in default|_|localhost|"") continue;; esac
    docroot=$(grep -m1 -E '^\s*root\s+' "$f" 2>/dev/null | awk '{print $2}' | tr -d ';')
    php=$(grep -m1 -oE 'php[0-9]+\.[0-9]+' "$f" 2>/dev/null | sed 's/php//')
    ssl=0
    grep -q 'ssl_certificate' "$f" 2>/dev/null && ssl=1
    owner=""
    if [ -n "$docroot" ]; then
        owner=$(stat -c '%U' "$docroot" 2>/dev/null)
        case "$docroot" in
            /home/*) [ -z "$owner" ] && owner=$(echo "$docroot" | awk -F/ '{print $3}') ;;
        esac
    fi
    wp=0
    if [ -n "$docroot" ] && [ -f "$docroot/wp-config.php" ]; then wp=1; fi
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$name" "$owner" "${docroot:-}" "${php:-}" "$ssl" "$wp"
done | sort -u
exit 0`
	result, err := SSHCommand(ctx, host, port, user, pass, cmd)
	if err != nil {
		return []models.DomainSetting{}, err
	}
	out := []models.DomainSetting{}
	for _, line := range parseLines(result.Output) {
		fields := strings.Split(line, "\t")
		if len(fields) < 6 {
			continue
		}
		out = append(out, models.DomainSetting{
			Domain:       fields[0],
			Owner:        fields[1],
			DocumentRoot: fields[2],
			PHPVersion:   fields[3],
			HasSSL:       fields[4] == "1",
			WPInstalled:  fields[5] == "1",
		})
	}
	return out, nil
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// RemoteMongoExport runs mongoexport on the SOURCE server (where the
// source's panel mongo lives) and returns the result as parsed bson.M
// documents. The query is a JSON string passed straight to
// `mongoexport --query` — pass `{}` to dump the whole collection.
//
// Why this lives in agent/: it executes a remote shell command over the
// existing SSH session, identical to every other discover function.
// The destination panel's transfer service then inserts the docs into
// its own local mongo via the regular Go driver — there's no need to
// reach through SSH twice.
//
// We try mongoexport first because it emits one document per line in
// MongoDB extended JSON, which the Go driver parses unambiguously. If
// mongoexport is missing (older boxes), we fall back to mongosh's
// EJSON.stringify path, which produces a single JSON array.
func RemoteMongoExport(ctx context.Context, host string, port int, user, pass, dbName, collection, queryJSON string) ([]map[string]any, error) {
	if queryJSON == "" {
		queryJSON = "{}"
	}
	// shellSingleQuote isn't enough for the query — JSON has its own
	// quoting rules and we want it to land literally inside a `--query=`
	// arg. Wrap in single quotes and rely on the JSON not containing any.
	cmd := fmt.Sprintf(`set +e
URI=$(grep -E '^(MONGODB_URI|MONGO_URI)=' /opt/serverpanel/.env 2>/dev/null | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'")
if [ -z "$URI" ]; then URI="mongodb://localhost:27017/%s"; fi
DB="%s"
COL="%s"
QUERY=%s

if command -v mongoexport >/dev/null 2>&1; then
    mongoexport --uri "$URI" --collection "$COL" --query "$QUERY" --jsonArray 2>/dev/null
    exit 0
fi

if command -v mongosh >/dev/null 2>&1; then
    mongosh "$URI" --quiet --eval "EJSON.stringify(db.getCollection('$COL').find($QUERY).toArray())" 2>/dev/null
    exit 0
fi
echo '[]'
exit 0`, dbName, dbName, collection, shellSingleQuote(queryJSON))

	result, err := SSHCommand(ctx, host, port, user, pass, cmd)
	if err != nil {
		return nil, fmt.Errorf("ssh mongoexport: %w", err)
	}
	out := strings.TrimSpace(result.Output)
	if out == "" || out == "[]" || out == "null" {
		return []map[string]any{}, nil
	}
	// mongoexport --jsonArray gives a JSON array; mongosh path also gives
	// an array. Both are extended JSON. Use json.Unmarshal first (works
	// for relaxed extended JSON in most cases) — if a doc has $oid /
	// $date wrappers, downstream insert handles them as plain strings,
	// which is fine since we re-stamp _id and timestamps anyway.
	var docs []map[string]any
	if err := json.Unmarshal([]byte(out), &docs); err != nil {
		return nil, fmt.Errorf("parse mongo export json: %w (head=%q)", err, head(out, 120))
	}
	return docs, nil
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func atoi64Safe(s string) int64 {
	var n int64
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
