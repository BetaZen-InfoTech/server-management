package agent

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// jailUsernameRegex mirrors services.usernameRegex so we can validate inline
// without pulling in a services import (which would cycle).
var jailUsernameRegex = regexp.MustCompile(`^[a-z][a-z0-9]{2,15}$`)

// sshdJailConfPath is the drop-in sshd config that locks down jailed users.
const sshdJailConfPath = "/etc/ssh/sshd_config.d/99-serverpanel-jail.conf"

const sshdJailConfBody = `# Managed by Betazen Server Panel — restrictions for jailkit-jailed users
Match User *,!root
    AllowTcpForwarding no
    X11Forwarding no
    PermitTunnel no
    AllowAgentForwarding no
`

// EnsureJailkitInstalled makes sure the jailkit package is installed on the
// local host. It is idempotent — if dpkg already reports jailkit as installed,
// this is a no-op. Otherwise it runs apt-get update && apt-get install -y
// jailkit via RunLongCommand so the 20-minute timeout applies.
func EnsureJailkitInstalled(ctx context.Context) error {
	if _, err := RunCommand(ctx, "dpkg", "-s", "jailkit"); err == nil {
		return nil
	}
	if _, err := RunLongCommand(ctx, "bash", "-c", "apt-get update && apt-get install -y jailkit"); err != nil {
		return fmt.Errorf("install jailkit: %w", err)
	}
	return nil
}

// JailUser converts an existing linux user into a jailkit chroot jail rooted
// at /home/<username>. The linux account and home directory must already
// exist; this function only populates the jail skeleton and rewrites the
// user's login shell to /usr/sbin/jk_chrootsh via jk_jailuser.
func JailUser(ctx context.Context, username string) error {
	if !jailUsernameRegex.MatchString(username) {
		return fmt.Errorf("invalid username %q", username)
	}

	// Best effort: make sure jailkit is available. If install fails we still
	// try the jail calls below so the error surfaces from the real operation.
	if err := EnsureJailkitInstalled(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "warning: EnsureJailkitInstalled: %v\n", err)
	}

	home := fmt.Sprintf("/home/%s", username)
	if _, err := os.Stat(home); err != nil {
		return fmt.Errorf("home dir %s missing: %w", home, err)
	}

	// Populate the chroot skeleton with the standard jailkit sections. This
	// is idempotent in practice — jk_init prints "already exists" warnings
	// but exits 0 on rerun, so any real failure is still surfaced.
	if _, err := RunLongCommand(ctx, "jk_init", "-v", "-j", home,
		"basicshell", "editors", "netutils", "ssh", "sftp", "scp", "jk_lsh"); err != nil {
		if !isAlreadyExistsErr(err) {
			return fmt.Errorf("jk_init: %w", err)
		}
	}

	// Move the user into the jail and switch their shell to jk_chrootsh.
	if _, err := RunCommand(ctx, "jk_jailuser", "-m", "-j", home, username); err != nil {
		if !isAlreadyJailedErr(err) {
			return fmt.Errorf("jk_jailuser: %w", err)
		}
	}

	if err := ensureSSHJailMatchBlock(ctx); err != nil {
		return fmt.Errorf("sshd jail config: %w", err)
	}
	return nil
}

// UnjailUser restores a user's login shell to /bin/bash so SSH works normally
// again. We intentionally do NOT try to move files back out of the chroot —
// that is irreversible without data loss. The home directory stays as-is.
func UnjailUser(ctx context.Context, username string) error {
	if !jailUsernameRegex.MatchString(username) {
		return fmt.Errorf("invalid username %q", username)
	}
	if _, err := RunCommand(ctx, "usermod", "-s", "/bin/bash", username); err != nil {
		return fmt.Errorf("usermod: %w", err)
	}
	return nil
}

// ensureSSHJailMatchBlock writes a drop-in sshd config that denies tunneling
// and forwarding for any non-root user. Idempotent: we always overwrite the
// file with the canonical body and then run sshd -t. If sshd -t rejects the
// config we remove the file and bubble the error up so SSH is never left in
// a broken state. A successful write is followed by systemctl reload ssh
// (best effort — the file will take effect on the next reload either way).
func ensureSSHJailMatchBlock(ctx context.Context) error {
	if err := os.WriteFile(sshdJailConfPath, []byte(sshdJailConfBody), 0644); err != nil {
		return fmt.Errorf("write %s: %w", sshdJailConfPath, err)
	}
	if _, err := RunCommand(ctx, "sshd", "-t"); err != nil {
		_ = os.Remove(sshdJailConfPath)
		return fmt.Errorf("sshd -t: %w", err)
	}
	_, _ = RunCommand(ctx, "systemctl", "reload", "ssh")
	return nil
}

func isAlreadyExistsErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already exists") || strings.Contains(s, "already present")
}

func isAlreadyJailedErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already") && (strings.Contains(s, "jail") || strings.Contains(s, "chroot"))
}
