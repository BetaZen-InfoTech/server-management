package agent

import (
	"strings"
	"testing"
)

// Tests for the v3.1.51 mail-content transfer fix. Pre-3.1.51 two
// bugs combined to drop every old message during a transfer:
//
//  1. RemoteBackupEmail's source-side script never checked
//     /var/vmail/<domain>/ — the panel's actual Maildir location for
//     domains with no Linux owner. Result: empty tar shipped across.
//  2. RestoreEmail extracted to /var/mail/vhosts/, which Dovecot
//     never reads from. Result: even non-empty archives were
//     dumped where nothing on the destination could see them.
//
// These tests pin both fixes so a regression to either pre-3.1.51
// behavior fails the test loudly.

// TestEmailBackupScript_SearchOrder pins the source-side path search.
// /var/vmail must come BEFORE /home/* and BEFORE /var/mail/vhosts —
// otherwise panel-managed domains (the common case) get an empty
// tarball when /home/<owner>/mail/<domain>/ doesn't happen to exist.
func TestEmailBackupScript_SearchOrder(t *testing.T) {
	script := buildEmailBackupScript("example.com", "/tmp/x.tar.gz")

	for _, want := range []string{
		`/var/vmail/$DOMAIN`,
		`$o/mail/$DOMAIN`,
		`/var/mail/vhosts/$DOMAIN`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q\nfull script:\n%s", want, script)
		}
	}

	idxVmail := strings.Index(script, `/var/vmail/$DOMAIN`)
	idxHome := strings.Index(script, `$o/mail/$DOMAIN`)
	idxLegacy := strings.Index(script, `/var/mail/vhosts/$DOMAIN`)

	if idxVmail < 0 || idxHome < 0 || idxLegacy < 0 {
		t.Fatalf("missing one of the path checks: vmail=%d home=%d legacy=%d", idxVmail, idxHome, idxLegacy)
	}
	if !(idxVmail < idxHome && idxHome < idxLegacy) {
		t.Fatalf("search order regression — must be /var/vmail < /home/<o>/mail < /var/mail/vhosts; got vmail=%d home=%d legacy=%d",
			idxVmail, idxHome, idxLegacy)
	}
}

// TestEmailBackupScript_DomainAndOutPath_ShellQuoted ensures the
// caller-supplied values are %q-quoted so a domain with quotes /
// dollar-signs can't break out into the shell.
func TestEmailBackupScript_DomainAndOutPath_ShellQuoted(t *testing.T) {
	dom := `evil"domain$(rm -rf /).com`
	out := `/tmp/x";reboot;tar.gz`
	script := buildEmailBackupScript(dom, out)

	// %q wraps in double-quotes and escapes embedded ". The literal
	// `rm -rf /` substring should appear inside a quoted string and
	// NEVER unquoted on its own line.
	if !strings.Contains(script, `DOMAIN="evil\"domain$(rm -rf /).com"`) {
		t.Fatalf("DOMAIN not properly quoted; injection risk\nscript:\n%s", script)
	}
	if !strings.Contains(script, `OUT="/tmp/x\";reboot;tar.gz"`) {
		t.Fatalf("OUT not properly quoted; injection risk\nscript:\n%s", script)
	}
}

// TestEmailRestoreDir pins the destination extract path to /var/vmail.
// /var/mail/vhosts (the pre-3.1.51 value) is the legacy cPanel layout
// that no Betazen Dovecot install reads from — extracting there sends
// every transferred message into a black hole.
func TestEmailRestoreDir(t *testing.T) {
	if EmailRestoreDir != "/var/vmail" {
		t.Fatalf("EmailRestoreDir = %q, want /var/vmail (regression: pre-3.1.51 used /var/mail/vhosts which Dovecot doesn't read)", EmailRestoreDir)
	}
}
