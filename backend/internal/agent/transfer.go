package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"golang.org/x/crypto/ssh"
)

// ProgressWriter wraps an io.Writer and counts bytes written. Read the
// counter from any goroutine via Bytes(). Safe for concurrent reads.
type ProgressWriter struct {
	w     io.Writer
	count int64
}

func NewProgressWriter(w io.Writer) *ProgressWriter { return &ProgressWriter{w: w} }

func (p *ProgressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	atomic.AddInt64(&p.count, int64(n))
	return n, err
}

func (p *ProgressWriter) Bytes() int64 { return atomic.LoadInt64(&p.count) }

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

// SCPDownloadProgress is SCPDownload with a live byte counter exposed via
// the returned ProgressWriter. The caller polls pw.Bytes() from a separate
// goroutine while the download runs to compute throughput / ETA. Returns
// the writer so callers can read the final count after completion too.
func SCPDownloadProgress(ctx context.Context, host string, port int, user, pass, remotePath, localPath string, pw *ProgressWriter) error {
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
	if info, statErr := os.Stat(localPath); statErr == nil && info.IsDir() {
		_ = os.RemoveAll(localPath)
	}
	out, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create local file failed: %w", err)
	}
	defer out.Close()

	pw.w = out
	var stderr bytes.Buffer
	session.Stdout = pw
	session.Stderr = &stderr

	if err := session.Run(fmt.Sprintf("cat %s", shellSingleQuote(remotePath))); err != nil {
		_ = os.Remove(localPath)
		return fmt.Errorf("remote cat failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	if info, statErr := os.Stat(localPath); statErr == nil && info.Size() == 0 {
		_ = os.Remove(localPath)
		return fmt.Errorf("downloaded file is empty (remote: %s)", remotePath)
	}
	return nil
}

// RemoteSizeBytes returns the on-disk byte count of remotePath via
// `du -sb`. Used to give SCPDownloadProgress a denominator before the
// tarball is downloaded so the UI can compute a real percent + ETA.
// Returns 0 with no error if the path is missing — callers should treat 0
// as "unknown total" rather than failing the transfer.
func RemoteSizeBytes(ctx context.Context, host string, port int, user, pass, remotePath string) (int64, error) {
	out, err := SSHCommand(ctx, host, port, user, pass,
		fmt.Sprintf("du -sb %s 2>/dev/null | awk '{print $1}'", shellSingleQuote(remotePath)))
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(out.Output)
	if s == "" {
		return 0, nil
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n, nil
}

// RemoteUserHomeBytesFiltered reports the on-disk byte count of
// /home/<sysUser> with the same excludes the file-transfer tar applies,
// so the live progress bar's denominator matches what we'll actually
// download (modulo gzip compression). Falls back to 0 — meaning
// "unknown total" — on any source-side error.
func RemoteUserHomeBytesFiltered(ctx context.Context, host string, port int, user, pass, sysUser string) (int64, error) {
	var excludes strings.Builder
	for _, p := range transferTarExcludes {
		excludes.WriteString(" --exclude=")
		excludes.WriteString(shellSingleQuote(p))
	}
	cmd := fmt.Sprintf("du -sb%s /home/%s 2>/dev/null | awk '{print $1}'", excludes.String(), sysUser)
	out, err := SSHCommand(ctx, host, port, user, pass, cmd)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(out.Output)
	if s == "" {
		return 0, nil
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n, nil
}

// remoteHasPigz reports whether `pigz` is on the source PATH. We prefer it
// because it parallelises gzip across CPU cores — on a 4-core source box
// tar creation runs ~3-4× faster than single-threaded gzip. Falls back to
// gzip silently when pigz isn't installed (no extra apt step required).
func remoteHasPigz(ctx context.Context, host string, port int, user, pass string) bool {
	r, err := SSHCommand(ctx, host, port, user, pass, "command -v pigz >/dev/null 2>&1 && echo y || echo n")
	if err != nil {
		return false
	}
	return strings.TrimSpace(r.Output) == "y"
}

// transferTarExcludes are paths that should NEVER ride along in the
// per-user file tarball. They re-materialise from package.json /
// requirements.txt / Gemfile on the destination at install time, and
// shipping them just bloats transfers (a single node_modules tree can
// easily be 500 MB+; a Python venv 100 MB+) and risks pulling in
// architecture-specific binaries that don't run on the destination.
//
// Pattern is glob-relative to each tar entry, NOT absolute — so
// "node_modules" matches any node_modules/ at any depth under
// /home/<user>/.
var transferTarExcludes = []string{
	// Node.js
	"node_modules",
	".npm",
	".yarn",
	".pnpm-store",
	".pnpm",
	".next/cache",
	// Python
	"venv",
	".venv",
	"env",
	"__pycache__",
	"*.pyc",
	".pytest_cache",
	".mypy_cache",
	".tox",
	"pip-wheel-metadata",
	// Ruby
	".gem",
	".bundle",
	"vendor/bundle",
	// Build / general caches
	".cache",
	".local/share/virtualenvs",
	"target",         // Rust / Maven / Java build dirs
	"build",
	"dist",
	".gradle",
	".m2/repository",
}

// RemoteBackupUserFilesProgress is the parallel-friendly variant of
// RemoteBackupUserFiles. It uses pigz when available, exposes a live byte
// counter (pw.Bytes()) for the SCP download phase, and returns the
// archive's pre-download size so the caller can render a percentage.
//
// Runtime caches (node_modules, venvs, .gem, etc — see
// transferTarExcludes) are stripped at tar time. They re-build on the
// destination from package.json / requirements.txt / Gemfile and
// otherwise just bloat the transfer (a single node_modules tree can be
// 500 MB+; a Python venv 100 MB+).
func RemoteBackupUserFilesProgress(ctx context.Context, host string, port int, user, pass, sysUser, localPath string, pw *ProgressWriter) (int64, error) {
	remoteTmp := fmt.Sprintf("/tmp/transfer-files-%s.tar.gz", sysUser)
	compressor := "gzip"
	if remoteHasPigz(ctx, host, port, user, pass) {
		compressor = "pigz"
	}
	var excludes strings.Builder
	for _, p := range transferTarExcludes {
		excludes.WriteString(" --exclude=")
		excludes.WriteString(shellSingleQuote(p))
	}
	tarCmd := fmt.Sprintf("tar --use-compress-program=%s%s -cf %s -C /home %s 2>/dev/null",
		compressor, excludes.String(), remoteTmp, sysUser)
	if _, err := SSHCommand(ctx, host, port, user, pass, tarCmd); err != nil {
		return 0, fmt.Errorf("remote file backup failed: %w", err)
	}
	total, _ := RemoteSizeBytes(ctx, host, port, user, pass, remoteTmp)
	if err := SCPDownloadProgress(ctx, host, port, user, pass, remoteTmp, localPath, pw); err != nil {
		return total, fmt.Errorf("download files failed: %w", err)
	}
	SSHCommand(ctx, host, port, user, pass, fmt.Sprintf("rm -f %s", remoteTmp))
	return total, nil
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
// Supports: Betazen Server Panel, cPanel/WHM, Plesk, DirectAdmin, bare nginx/apache setups.
//
// Filter pipeline:
//   - Drop the panel's own vhost files (serverpanel, default, 000-default,
//     default-ssl) so the panel hostname + IP + `_` catch-all don't leak
//     as "domains".
//   - Drop bare IPv4 addresses (4 dotted decimal groups) that come from the
//     panel's server_name catch-all list.
//   - Drop the distro-default placeholder hosts (example.com / example.org
//     / localhost / www.example.*) that bare apache installs ship with.
func DiscoverDomains(ctx context.Context, host string, port int, user, pass string) ([]string, error) {
	cmd := `{
		# Betazen Server Panel / custom setups
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
		# Nginx configs — scan every file EXCEPT the panel's own vhost
		# (serverpanel) and the stock distro default so the panel hostname,
		# raw IP catchall, and "example.com" placeholder don't leak.
		for f in /etc/nginx/sites-available/* /etc/nginx/conf.d/*.conf; do
		    [ -f "$f" ] || continue
		    case "$(basename "$f")" in serverpanel|default|default.conf|default-ssl|default-ssl.conf|000-default|000-default.conf) continue;; esac
		    grep -h 'server_name ' "$f" 2>/dev/null | sed 's/.*server_name //;s/;.*//' | tr ' ' '\n'
		done;
		# Apache configs — same filename skip list.
		for f in /etc/apache2/sites-available/* /etc/httpd/conf.d/*.conf /etc/apache2/conf.d/*.conf /usr/local/apache/conf/*.conf; do
		    [ -f "$f" ] || continue
		    case "$(basename "$f")" in default|default.conf|default-ssl|default-ssl.conf|000-default|000-default.conf) continue;; esac
		    grep -h 'ServerName\|ServerAlias' "$f" 2>/dev/null | awk '{print $2}' | tr ' ' '\n'
		done;
		# Home dirs with public_html (common layout)
		for d in /home/*/public_html; do [ -d "$d" ] && basename $(dirname "$d"); done 2>/dev/null;
	} | sort -u | awk '
	    NF && /\./ \
	    && !/default|localhost|_|ssl|cgi-bin|error|chroot/ \
	    && !/^example\.(com|org|net)$/ \
	    && !/^www\.example\.(com|org|net)$/ \
	    && !/^([0-9]+\.){3}[0-9]+$/ \
	' || true`
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
// Tries direct mongosh first, then falls back to Betazen Server Panel's .env MONGODB_URI.
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
	// Skip mongo's own system DBs AND the panel's own database. Without
	// the "serverpanel" exclusion, a destination-side restore would
	// overwrite the destination panel's users / apps / projects mongo
	// with the source's — which is exactly what the transfer flow
	// is trying NOT to do (Sync Panel Records is the right path; full
	// db-overwrite is catastrophic).
	skip := map[string]bool{"admin": true, "local": true, "config": true, "serverpanel": true}
	for _, line := range parseLines(result.Output) {
		if !skip[line] {
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
	// Tar BOTH /etc/letsencrypt/live/<domain>/ AND archive/<domain>/ in
	// one go, rooted at /etc/letsencrypt so the destination can extract
	// straight back into /etc/letsencrypt/ and the symlink layout is
	// preserved verbatim. live/<domain>/{cert,chain,fullchain,privkey}.pem
	// are symlinks into archive/<domain>/{cert,chain,fullchain,privkey}<n>.pem
	// — without the archive payload, nginx fails to load every cert
	// with "BIO_new_file()... no such file" and the SSL upgrade silently
	// rolls back, so the destination ends up with no :443 vhost. Also
	// pull renewal/<domain>.conf so `certbot renew` works without
	// re-issuing.
	remoteTmp := fmt.Sprintf("/tmp/transfer-ssl-%s.tar.gz", domain)
	tarCmd := fmt.Sprintf(
		"tar -czhf %s -C /etc/letsencrypt --ignore-failed-read live/%s archive/%s renewal/%s.conf 2>/dev/null",
		shellSingleQuote(remoteTmp),
		shellSingleQuote(domain), shellSingleQuote(domain), shellSingleQuote(domain))
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

// RemoteBackupUserFiles creates a tarball of a user's home directory on
// source and downloads it. Same exclude list as
// RemoteBackupUserFilesProgress — runtime caches (node_modules, venvs,
// .gem, etc) are stripped at tar time.
func RemoteBackupUserFiles(ctx context.Context, host string, port int, user, pass, sysUser, localPath string) error {
	remoteTmp := fmt.Sprintf("/tmp/transfer-files-%s.tar.gz", sysUser)
	var excludes strings.Builder
	for _, p := range transferTarExcludes {
		excludes.WriteString(" --exclude=")
		excludes.WriteString(shellSingleQuote(p))
	}
	_, err := SSHCommand(ctx, host, port, user, pass,
		fmt.Sprintf("tar%s -czf %s -C /home %s 2>/dev/null", excludes.String(), remoteTmp, sysUser))
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
	// The panel stores Maildirs under /home/<owner>/mail/<domain>/ (the
	// panel's CreateMailbox path); /var/mail/vhosts is the cPanel/legacy
	// location. Tar whichever exists. Without the /home/<owner>/mail
	// branch, every transfer reported "downloaded file is empty" and
	// the actual mail data never moved across.
	cmd := fmt.Sprintf(`set +e
DOMAIN=%q
OUT=%q
SRC=""
for o in /home/*; do
  [ -d "$o/mail/$DOMAIN" ] && SRC="$o/mail/$DOMAIN" && break
done
if [ -z "$SRC" ] && [ -d "/var/mail/vhosts/$DOMAIN" ]; then
  SRC="/var/mail/vhosts/$DOMAIN"
fi
if [ -z "$SRC" ]; then
  # No mail data on disk for this domain — emit an empty tarball so the
  # downstream restore exits cleanly instead of failing on a missing file.
  tar -czf "$OUT" -T /dev/null
  exit 0
fi
tar -czf "$OUT" -C "$(dirname "$SRC")" "$(basename "$SRC")" 2>/dev/null
exit 0`, domain, remoteTmp)
	if _, err := SSHCommand(ctx, host, port, user, pass, cmd); err != nil {
		return fmt.Errorf("remote email backup failed: %w", err)
	}
	if err := SCPDownload(ctx, host, port, user, pass, remoteTmp, localPath); err != nil {
		return fmt.Errorf("download email failed: %w", err)
	}
	SSHCommand(ctx, host, port, user, pass, fmt.Sprintf("rm -f %s", remoteTmp))
	return nil
}

// RemoteTarPath tars a remote path on the source via SSH and downloads
// the resulting archive to localPath. Returns an error if the source
// path doesn't exist or is empty (so the caller can fall back).
func RemoteTarPath(ctx context.Context, host string, port int, user, pass, remotePath, localPath string) error {
	remoteTmp := fmt.Sprintf("/tmp/transfer-tar-%d.tar.gz", time.Now().UnixNano())
	cmd := fmt.Sprintf(`set +e
P=%s
[ -e "$P" ] || { echo "MISSING"; exit 1; }
tar -czf %s -C "$(dirname "$P")" "$(basename "$P")" 2>/dev/null
[ -s %s ] || { echo "EMPTY_TAR"; exit 1; }
exit 0`, shellSingleQuote(remotePath), shellSingleQuote(remoteTmp), shellSingleQuote(remoteTmp))
	if _, err := SSHCommand(ctx, host, port, user, pass, cmd); err != nil {
		return fmt.Errorf("remote tar %s: %w", remotePath, err)
	}
	if err := SCPDownload(ctx, host, port, user, pass, remoteTmp, localPath); err != nil {
		return fmt.Errorf("download %s: %w", remoteTmp, err)
	}
	SSHCommand(ctx, host, port, user, pass, fmt.Sprintf("rm -f %s", shellSingleQuote(remoteTmp)))
	return nil
}

// LocalUntar extracts a local .tar.gz into destDir. Used by the transfer
// flow to land files retrieved via RemoteTarPath onto the destination.
func LocalUntar(ctx context.Context, archivePath, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	if _, err := RunCommand(ctx, "tar", "-xzf", archivePath, "-C", destDir); err != nil {
		return fmt.Errorf("local untar %s: %w", archivePath, err)
	}
	return nil
}

// DiscoverSourcePanelDomain returns the source's own panel management
// domain (whatever the operator typed at install.sh's "Enter panel
// domain" prompt). Read from /opt/serverpanel/.env's DOMAIN= line on
// the source via SSH, with two fallbacks: PANEL_DOMAIN= for older
// installs, and the server_name in the panel's own nginx vhost.
//
// Empty string when the source isn't a Betazen panel, when neither file
// exists, or when the value is the literal "localhost" / a bare IP
// (the install-time defaults that aren't real domains and shouldn't
// match anything).
func DiscoverSourcePanelDomain(ctx context.Context, host string, port int, user, pass string) string {
	cmd := `set +e
val=""
for f in /opt/serverpanel/.env /opt/serverpanel/backend/.env; do
    [ -f "$f" ] || continue
    val=$(grep -E '^(DOMAIN|PANEL_DOMAIN)=' "$f" 2>/dev/null | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'")
    [ -n "$val" ] && break
done
if [ -z "$val" ]; then
    # Fallback: parse the panel's own nginx vhost.
    for f in /etc/nginx/sites-available/serverpanel /etc/nginx/sites-enabled/serverpanel; do
        [ -f "$f" ] || continue
        val=$(grep -m1 -E '^\s*server_name\s+' "$f" | awk '{print $2}' | tr -d ';')
        [ -n "$val" ] && break
    done
fi
echo "$val"
exit 0`
	result, err := SSHCommand(ctx, host, port, user, pass, cmd)
	if err != nil || result == nil {
		return ""
	}
	v := strings.TrimSpace(result.Output)
	v = strings.ToLower(v)
	if v == "" || v == "localhost" {
		return ""
	}
	// Bare IPv4 — treat as "no real panel domain set".
	if isIPv4(v) {
		return ""
	}
	return v
}

func isIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 3 {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// ExportAuthorizedKeysFromRemote returns the contents of
// /home/<user>/.ssh/authorized_keys (or root's keys when user="root"),
// stripped of empty/comment lines. Each returned line is one full
// "<keytype> <base64> [comment]" entry suitable for writing back into
// authorized_keys verbatim on the destination.
//
// Empty result on missing file (typical for vendors who never added a
// key) — the caller treats that as "no SSH keys to migrate".
func ExportAuthorizedKeysFromRemote(ctx context.Context, host string, port int, user, pass, sysUser string) ([]string, error) {
	path := "/home/" + sysUser + "/.ssh/authorized_keys"
	if sysUser == "root" {
		path = "/root/.ssh/authorized_keys"
	}
	result, err := SSHCommand(ctx, host, port, user, pass,
		fmt.Sprintf(`cat %s 2>/dev/null || echo ''`, shellSingleQuote(path)))
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for _, ln := range parseLines(result.Output) {
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		keys = append(keys, ln)
	}
	return keys, nil
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
	// docroot extraction: every vhost template we ship now emits an
	// `/.well-known/acme-challenge/ { root /var/www/certbot; }` location
	// BEFORE the site's real server-level `root`. The earlier
	// `grep -m1 '^\s*root\s+'` matched the ACME block first and
	// reported /var/www/certbot as the site's document root — which
	// then cascaded into wrong owner (`stat -c %U /var/www/certbot`
	// = root) and wrong wp detection (no wp-config.php there).
	//
	// Prefer `root /home/...` (the canonical location for user-owned
	// sites), then `root /var/www/html/...`, then fall back to the
	// first root line that ISN'T /var/www/certbot.
	cmd := `set +e
shopt -s nullglob 2>/dev/null
for f in /etc/nginx/sites-available/* /etc/nginx/sites-enabled/* /etc/nginx/conf.d/*.conf; do
    [ -f "$f" ] || continue
    case "$(basename "$f")" in serverpanel|default|default.conf|default-ssl|default-ssl.conf|000-default|000-default.conf) continue;; esac
    name=$(grep -m1 -E '^\s*server_name\s+' "$f" 2>/dev/null | awk '{print $2}' | tr -d ';')
    [ -z "$name" ] && continue
    case "$name" in default|_|localhost|"") continue;; esac
    # Skip bare-IP server_names from panel catch-alls.
    case "$name" in [0-9]*.[0-9]*.[0-9]*.[0-9]*) continue;; esac
    docroot=$(grep -E '^\s*root\s+/home/' "$f" 2>/dev/null | head -1 | awk '{print $2}' | tr -d ';')
    if [ -z "$docroot" ]; then
        docroot=$(grep -E '^\s*root\s+/var/www/html' "$f" 2>/dev/null | head -1 | awk '{print $2}' | tr -d ';')
    fi
    if [ -z "$docroot" ]; then
        docroot=$(grep -E '^\s*root\s+' "$f" 2>/dev/null | awk '{print $2}' | tr -d ';' | grep -v '/var/www/certbot$' | head -1)
    fi
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
