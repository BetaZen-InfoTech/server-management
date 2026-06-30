package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// BackupEmail captures a domain's live Maildir, PATH-PRESERVING (relative to
// /), so a local restore puts it back exactly where Dovecot reads it.
//
// Pre-fix this tarred /var/mail/vhosts/<domain> — the legacy cPanel location
// that is empty on every modern Betazen install, because mail actually lives
// at /home/<owner>/mail/<domain>/<user> (the per-row userdb_mail override in
// /etc/dovecot/users wins; see email_service.go:getMaildirPath). The result
// was an empty email backup. We now capture whichever of the three known
// layouts exist, keeping their absolute paths so RestoreEmailLocal is the
// exact inverse. (RestoreEmail / RemoteBackupEmail keep their /var/vmail
// layout for the transfer flow — see TestEmailRestoreDir.)
func BackupEmail(ctx context.Context, user, domain, outputPath string) error {
	script := fmt.Sprintf(`set -e
paths=""
for p in "home/%[1]s/mail/%[2]s" "var/vmail/%[2]s" "var/mail/vhosts/%[2]s"; do
  [ -d "/$p" ] && paths="$paths $p"
done
if [ -z "$paths" ]; then
  tar -czf %[3]s -T /dev/null            # valid empty archive — no mail to capture
else
  tar -czf %[3]s -C / $paths
fi`, user, domain, outputPath)
	_, err := RunCommand(ctx, "bash", "-c", script)
	return err
}

// MySQLDump streams a single MySQL/MariaDB database to a gzip file. Used by
// the per-domain "database" backup, which (pre-fix) wrongly called mongodump
// against a database named after the domain — a DB that never exists.
//
// --databases makes the dump self-contained (CREATE DATABASE + USE), so the
// restore side is a plain `gunzip | mysql` with no need to pre-create or
// select the target schema.
func MySQLDump(ctx context.Context, dbName, outputPath string) error {
	q := "'" + strings.ReplaceAll(dbName, "'", `'\''`) + "'"
	_, err := RunLongCommand(ctx, "bash", "-c", fmt.Sprintf(
		"mysqldump --single-transaction --routines --triggers --events --databases %s 2>/dev/null | gzip > %s",
		q, outputPath))
	return err
}

// RestoreEmailLocal is the exact inverse of BackupEmail: it extracts the
// path-preserving archive at / and re-asserts ownership on whichever maildir
// trees came back, so an owned domain's mail lands in /home/<owner>/mail/...
// (where Dovecot reads it) rather than the wrong /var/vmail.
func RestoreEmailLocal(ctx context.Context, user, domain, archivePath string) error {
	if _, err := RunCommand(ctx, "tar", "-xzf", archivePath, "-C", "/"); err != nil {
		if !strings.Contains(err.Error(), "Empty archive") &&
			!strings.Contains(err.Error(), "Unexpected EOF") {
			return fmt.Errorf("untar %s: %w", archivePath, err)
		}
	}
	RunCommand(ctx, "bash", "-c", fmt.Sprintf(`
[ -d /home/%[1]s/mail/%[2]s ] && chown -R %[1]s:%[1]s /home/%[1]s/mail/%[2]s
[ -d /var/vmail/%[2]s ] && chown -R vmail:vmail /var/vmail/%[2]s
[ -d /var/mail/vhosts/%[2]s ] && chown -R vmail:vmail /var/mail/vhosts/%[2]s
true`, user, domain))
	return nil
}

// EncryptFile / DecryptFile wrap openssl AES-256-CBC + PBKDF2 so in-panel
// backups can honour an EncryptionPassword (the model exposed the field for
// releases but never used it — the docs' "AES-256-CBC" claim was false until
// now). The same algorithm the whole-server DR script uses, so bundles are
// interchangeable.
func EncryptFile(ctx context.Context, inPath, outPath, key string) error {
	_, err := RunLongCommand(ctx, "openssl", "enc", "-aes-256-cbc", "-pbkdf2", "-salt",
		"-in", inPath, "-out", outPath, "-pass", "pass:"+key)
	return err
}

func DecryptFile(ctx context.Context, inPath, outPath, key string) error {
	_, err := RunLongCommand(ctx, "openssl", "enc", "-d", "-aes-256-cbc", "-pbkdf2", "-salt",
		"-in", inPath, "-out", outPath, "-pass", "pass:"+key)
	return err
}

// UploadViaRclone / DownloadViaRclone push a file to any rclone remote. The
// service builds an on-the-fly connection string from the request's S3
// credentials, so "storage=s3" finally does something instead of being a
// validated-but-dead path.
func UploadViaRclone(ctx context.Context, localPath, remoteSpec string) error {
	_, err := RunLongCommand(ctx, "rclone", "copy", localPath, remoteSpec, "--no-traverse")
	return err
}

func DownloadViaRclone(ctx context.Context, remoteSpec, localDir string) error {
	_, err := RunLongCommand(ctx, "rclone", "copy", remoteSpec, localDir, "--no-traverse")
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
	if _, err := RunCommand(ctx, "chown", "-R", user+":"+user, fmt.Sprintf("/home/%s", user)); err != nil {
		return err
	}
	// Backup archives preserve source perms which are often wrong for the
	// destination (e.g. files unreadable by www-data). Re-normalise.
	return EnsureUserWebPerms(ctx, user)
}

// RestoreMongoDB restores a gzip mongodump archive into `dbName`. mongo
// 100.7+ deprecated --db/--collection in favour of --nsInclude /
// --nsFrom/--nsTo, and modern mongorestore exits non-zero when given
// the deprecated form even when the restore would otherwise succeed.
//
// The archive itself recorded the source database name, so we use
// --nsFrom=<srcDB>.* / --nsTo=<dstDB>.* to map every collection from
// whatever the source named into our requested dbName. When the
// caller passes dbName matching the archive's source DB (the common
// case), this is a verbatim restore; when they differ, every
// collection lands under the new name. nsInclude=*.* lets every
// collection through; the rename happens via nsFrom/nsTo.
func RestoreMongoDB(ctx context.Context, dbName, archivePath string) error {
	// Authenticate as the root 'admin' user (derived from the panel's
	// MONGO_URI, same pattern as mongoEval) so the restore works on an
	// auth-enabled destination — a bare `mongorestore` with no credentials
	// fails there, which silently broke MongoDB server-transfer end-to-end.
	// Falls back to the URI as-is, then a no-auth localhost restore. (v3.1.120)
	script := `set +e
ARCHIVE="$1"; DB="$2"
A=(--archive="$ARCHIVE" --gzip --drop --nsInclude="$DB.*" --nsFrom="$DB.*" --nsTo="$DB.*")
URI=""
for env in /opt/serverpanel/.env /opt/serverpanel/backend/.env; do
  [ -f "$env" ] || continue
  u=$(grep -E '^(MONGODB_URI|MONGO_URI)=' "$env" | head -1 | cut -d= -f2- | tr -d '"' | tr -d "'")
  if [ -n "$u" ]; then URI="$u"; break; fi
done
if [ -n "$URI" ]; then
  pass=$(printf '%s' "$URI" | sed -E 's#^mongodb(\+srv)?://[^:]+:([^@]+)@.*#\2#')
  hostport=$(printf '%s' "$URI" | sed -E 's#^mongodb(\+srv)?://[^@]+@([^/?]+).*#\2#')
  mongorestore --uri="mongodb://admin:${pass}@${hostport}/?authSource=admin" "${A[@]}" && exit 0
  mongorestore --uri="$URI" "${A[@]}" && exit 0
fi
mongorestore "${A[@]}"`
	_, err := RunCommand(ctx, "bash", "-c", script, "--", archivePath, dbName)
	return err
}

// RestoreEmail untars a domain's Maildir archive into the path the
// destination's panel actually reads from.
//
// v3.1.51 — was hardcoded to /var/mail/vhosts/ which is the legacy
// cPanel location nothing on this panel ever reads from. Dovecot on
// every Betazen install reads from /var/vmail/<domain>/<user>/ (see
// EmailService.getMaildirPath at email_service.go:241), so extracting
// to /var/mail/vhosts/ landed every transferred mail message somewhere
// the destination could not see. Combined with the v3.1.51 fix to
// RemoteBackupEmail (which previously emitted empty tars for /var/vmail
// sources), this finally makes "Email Accounts & Data" actually
// transfer the old mail history.
//
// Empty archives (the source had no mail for this domain) are handled
// gracefully: tar prints "Empty archive" to stderr and exits non-zero;
// we treat that as success since there's nothing to restore.
//
// Always extracts to /var/vmail/. Some installs may also serve mail
// from /home/<owner>/mail/<domain>/ when the domain has a Linux owner;
// the post-extract chown + a symlink fallback below cover both.
// EmailRestoreDir is the destination path where Maildir archives are
// extracted. v3.1.51 changed this from the legacy /var/mail/vhosts to
// /var/vmail because Dovecot on every Betazen install reads from
// /var/vmail/<domain>/<user>/. Exposed as a package-level constant so
// regression tests can pin the value without invoking RestoreEmail.
const EmailRestoreDir = "/var/vmail"

func RestoreEmail(ctx context.Context, domain, archivePath string) error {
	if err := os.MkdirAll(EmailRestoreDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", EmailRestoreDir, err)
	}
	if _, err := RunCommand(ctx, "tar", "-xzf", archivePath, "-C", EmailRestoreDir); err != nil {
		// "Empty archive" is non-fatal — happens when source had no
		// stored mail for this domain. Anything else bubbles up.
		if !strings.Contains(err.Error(), "Empty archive") &&
			!strings.Contains(err.Error(), "Unexpected EOF") {
			return fmt.Errorf("untar %s: %w", archivePath, err)
		}
	}
	dst := fmt.Sprintf("%s/%s", EmailRestoreDir, domain)
	if _, err := RunCommand(ctx, "chown", "-R", "vmail:vmail", dst); err != nil {
		return fmt.Errorf("chown %s: %w", dst, err)
	}
	if _, err := RunCommand(ctx, "chmod", "-R", "u+rwX,g+rX", dst); err != nil {
		return fmt.Errorf("chmod %s: %w", dst, err)
	}
	return nil
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
