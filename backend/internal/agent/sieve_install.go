// Package agent — sieve_install.go installs the Dovecot Pigeonhole Sieve
// pipeline that lets the panel fire `email.message.received` webhooks for
// every delivered message. Idempotent: safe to run on every panel boot
// and as part of the existing email-stack reconcile path.
//
// The pipeline:
//
//  1. Dovecot LMTP receives the message from Postfix and writes it to the
//     recipient's maildir.
//  2. The `sieve` plugin runs against a global "after" script
//     (/etc/dovecot/sieve/global-after.sieve) that calls vnd.dovecot.pipe
//     with the program "betazen-mail-hook".
//  3. /usr/local/lib/dovecot/sieve-pipe/betazen-mail-hook is a small bash
//     script that reads the message from stdin, extracts a handful of
//     headers (Message-ID, From, Subject, Date), and POSTs to the panel's
//     internal endpoint with a shared bearer token.
//  4. The panel handler resolves the recipient to a mailbox row and
//     dispatches to every webhook subscribed to email.message.received
//     (filtered by mailbox_email if the endpoint is per-mailbox).
//
// Failures inside the helper script never bounce mail — `pipe :copy`
// hands a COPY of the message to the program; the original delivery is
// already complete by then. Worst case is a missed webhook for one
// message, which is logged in /var/log/mail.log via stderr.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MailHookConfDir holds the bearer token + the panel callback URL the
// helper script reads. Both files are root-owned 0600 so unprivileged
// users on the box never see the bearer.
const (
	mailHookConfDir   = "/etc/serverpanel"
	mailHookTokenFile = "/etc/serverpanel/mail-hook.token"
	mailHookConfFile  = "/etc/serverpanel/mail-hook.conf"

	sievePipeBinDir   = "/usr/local/lib/dovecot/sieve-pipe"
	sievePipeBinName  = "betazen-mail-hook"
	sieveScriptDir    = "/etc/dovecot/sieve"
	sieveAfterDir     = "/etc/dovecot/sieve/after.d"
	sieveAfterScript  = "/etc/dovecot/sieve/after.d/10-betazen-hook.sieve"
	sieveDovecotConf  = "/etc/dovecot/conf.d/95-betazen-sieve.conf"
)

// EnsureMailHookInstalled wires up the Sieve pipe + helper + dovecot config.
//
// callbackURL is the panel's mail-incoming URL (typically
// http://127.0.0.1:<port>/api/v1/agent/mail/incoming). When empty, defaults
// to http://127.0.0.1:8080/api/v1/agent/mail/incoming so a default install
// "just works"; operators with a non-default port set this explicitly.
//
// Returns (log, error) — log is the human-readable step trace appended
// onto the existing email-install output for the operator UI.
func EnsureMailHookInstalled(ctx context.Context, callbackURL string) (string, error) {
	if strings.TrimSpace(callbackURL) == "" {
		callbackURL = "http://127.0.0.1:8080/api/v1/agent/mail/incoming"
	}
	var log strings.Builder
	step := func(label string, err error) {
		if err != nil {
			fmt.Fprintf(&log, "• %s: ERROR %v\n", label, err)
		} else {
			fmt.Fprintf(&log, "• %s\n", label)
		}
	}

	// 1. Install dovecot-sieve if not already present. Apt is idempotent
	// — a second invocation prints "already the newest version" and
	// returns 0, so we don't gate on a probe.
	if _, err := RunCommandLong(ctx, 5*time.Minute, "bash", "-c",
		"DEBIAN_FRONTEND=noninteractive apt-get install -y dovecot-sieve"); err != nil {
		step("apt-get install dovecot-sieve", err)
		return log.String(), fmt.Errorf("install dovecot-sieve: %w", err)
	}
	step("apt-get install dovecot-sieve", nil)

	// 2. /etc/serverpanel/{mail-hook.token,mail-hook.conf}.
	if err := os.MkdirAll(mailHookConfDir, 0755); err != nil {
		step("mkdir /etc/serverpanel", err)
		return log.String(), err
	}
	step("mkdir /etc/serverpanel", nil)

	token, err := readOrCreateMailHookToken()
	if err != nil {
		step("ensure mail-hook.token", err)
		return log.String(), err
	}
	step("ensure mail-hook.token (root:root 0600)", nil)

	conf := fmt.Sprintf("BETAZEN_HOOK_URL=%q\nBETAZEN_HOOK_TOKEN=%q\n", callbackURL, token)
	if err := writeFileSecure(mailHookConfFile, []byte(conf), 0600); err != nil {
		step("write mail-hook.conf", err)
		return log.String(), err
	}
	step("write /etc/serverpanel/mail-hook.conf", nil)

	// 3. Helper bash script under sieve_pipe_bin_dir. Pigeonhole's
	// vnd.dovecot.pipe extension only allows binaries that live in this
	// directory — putting the script under /usr/local/bin would silently
	// fail with "execution of program betazen-mail-hook failed".
	if err := os.MkdirAll(sievePipeBinDir, 0755); err != nil {
		step("mkdir "+sievePipeBinDir, err)
		return log.String(), err
	}
	helperPath := filepath.Join(sievePipeBinDir, sievePipeBinName)
	if err := writeFileSecure(helperPath, []byte(mailHookHelperScript), 0755); err != nil {
		step("write helper script", err)
		return log.String(), err
	}
	// dovecot user must own the helper for sieve-pipe to invoke it.
	RunCommand(ctx, "chown", "dovecot:dovecot", helperPath)
	step("install /usr/local/lib/dovecot/sieve-pipe/betazen-mail-hook", nil)

	// 4. Global "after" sieve script. Lives in /etc/dovecot/sieve/after.d/
	// so it runs AFTER any user's personal sieve filter — that way per-
	// user sorting/filtering still happens first and the webhook fires
	// regardless of where the message lands.
	if err := os.MkdirAll(sieveAfterDir, 0755); err != nil {
		step("mkdir "+sieveAfterDir, err)
		return log.String(), err
	}
	if err := writeFileSecure(sieveAfterScript, []byte(sieveAfterScriptBody), 0644); err != nil {
		step("write after.d/10-betazen-hook.sieve", err)
		return log.String(), err
	}
	// Compile so dovecot doesn't have to recompile on every delivery.
	RunCommand(ctx, "sievec", sieveAfterScript)
	step("compile global Sieve script", nil)

	// 5. Dovecot conf snippet. We append our own file in conf.d/ rather
	// than mutating an existing one — easier to remove cleanly later
	// and doesn't fight whatever the distro ships in 90-sieve.conf.
	if err := writeFileSecure(sieveDovecotConf, []byte(dovecotSieveConfSnippet), 0644); err != nil {
		step("write 95-betazen-sieve.conf", err)
		return log.String(), err
	}
	step("write /etc/dovecot/conf.d/95-betazen-sieve.conf", nil)

	// 6. Restart dovecot so the new config takes effect. Reload would
	// be lighter, but on some distros it doesn't pick up changes to
	// the sieve plugin's pipe-bin-dir. Restart is reliable.
	if _, err := RunCommand(ctx, "systemctl", "restart", "dovecot"); err != nil {
		step("systemctl restart dovecot", err)
		return log.String(), err
	}
	step("systemctl restart dovecot", nil)

	return log.String(), nil
}

// readOrCreateMailHookToken returns the existing bearer if one is on disk,
// or mints a fresh 32-byte hex token and writes it. Same flow as the
// Roundcube SSO HMAC secret — generated once at install time, stable for
// the lifetime of the box (rotated only if the operator manually deletes
// the file).
func readOrCreateMailHookToken() (string, error) {
	if b, err := os.ReadFile(mailHookTokenFile); err == nil {
		t := strings.TrimSpace(string(b))
		if t != "" {
			return t, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	tok := hex.EncodeToString(buf)
	if err := writeFileSecure(mailHookTokenFile, []byte(tok+"\n"), 0600); err != nil {
		return "", err
	}
	return tok, nil
}

// writeFileSecure writes to path atomically (write to .tmp, rename) so a
// concurrent reader never sees a half-written file. mode is applied via a
// post-write chmod because os.WriteFile honours umask.
func writeFileSecure(path string, content []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// dovecotSieveConfSnippet enables the Sieve plugin in LMTP (delivery from
// Postfix) and LDA (legacy mbox delivery), points the global script
// directory at our after.d, and whitelists the pipe binary.
const dovecotSieveConfSnippet = `# Managed by Betazen Server Panel — DO NOT EDIT MANUALLY.
# Re-generated by EnsureMailHookInstalled on each panel boot.
#
# Wires the Pigeonhole Sieve plugin into LMTP + LDA so the global
# after.d/10-betazen-hook.sieve runs once per delivered message and pipes a
# copy to /usr/local/lib/dovecot/sieve-pipe/betazen-mail-hook.
protocol lmtp {
  mail_plugins = $mail_plugins sieve
}
protocol lda {
  mail_plugins = $mail_plugins sieve
}
plugin {
  sieve = file:~/sieve;active=~/.dovecot.sieve
  sieve_after = /etc/dovecot/sieve/after.d
  sieve_pipe_bin_dir = /usr/local/lib/dovecot/sieve-pipe
  # sieve_plugins MUST load sieve_extprograms before the vnd.dovecot.pipe
  # extension below can be referenced — without this line, every inbound
  # message fails Sieve compile ("unknown Sieve capability vnd.dovecot.pipe")
  # and Dovecot LMTP rejects delivery with a 451 temp-fail. Postfix then
  # defers the mail; for migrated mailboxes the operator sees "email not
  # receiving" until they read mail.log. The plugin .so is shipped by
  # dovecot-sieve on Ubuntu 24.04 (lib90_sieve_extprograms_plugin.so) and
  # equivalent on Debian, so no extra apt install is needed.
  sieve_plugins = sieve_extprograms
  sieve_extensions = +vnd.dovecot.pipe +vnd.dovecot.environment
}
`

// sieveAfterScriptBody is the Sieve script that fires for every delivered
// message. We use `pipe :copy` so the helper gets a duplicate of the
// message — the original delivery to the maildir is already complete and
// is unaffected by anything the helper does (or fails to do).
const sieveAfterScriptBody = `# Managed by Betazen Server Panel — DO NOT EDIT MANUALLY.
# Pipes a copy of every delivered message to the panel webhook helper.
# Failures here never bounce mail (pipe :copy is non-blocking on error).
require ["vnd.dovecot.pipe", "copy"];

pipe :copy "betazen-mail-hook";
`

// mailHookHelperScript is the bash receiver. It reads the raw message
// from stdin, extracts header values via awk (no python/ruby dependency),
// and POSTs metadata to the panel. Body is intentionally NOT forwarded —
// receivers fetch via IMAP/POP3 with mailbox creds.
//
// The Sieve plugin invokes this with the recipient as argv[1] and sender
// as argv[2] (Pigeonhole convention for vnd.dovecot.pipe).
const mailHookHelperScript = `#!/bin/bash
# Managed by Betazen Server Panel — DO NOT EDIT MANUALLY.
# Receives a copy of each delivered message via Pigeonhole's vnd.dovecot.pipe,
# extracts a few headers, and POSTs metadata to the panel webhook callback.
# Body is never forwarded — receivers fetch via IMAP/POP3 with mailbox creds.

set -u

CONF="/etc/serverpanel/mail-hook.conf"
[[ -r "$CONF" ]] || exit 0
# shellcheck disable=SC1090
source "$CONF"

: "${BETAZEN_HOOK_URL:=}"
: "${BETAZEN_HOOK_TOKEN:=}"
[[ -z "$BETAZEN_HOOK_URL" || -z "$BETAZEN_HOOK_TOKEN" ]] && exit 0

# Sieve passes recipient as $1 and sender as $2 by convention. Both may be
# empty if the script is invoked directly (e.g. for a manual test); we
# fall back to header values in that case.
RECIPIENT_ARG="${1:-}"
SENDER_ARG="${2:-}"

# Read up to ~256 KiB of headers — the entire message is on stdin but we
# only care about the header block. Stop reading at the first blank line.
HEADERS=$(awk 'BEGIN{RS=""} {print; exit}' 2>/dev/null | head -c 262144)

extract_header() {
  # $1 = header name (case-insensitive). Joins folded lines.
  echo "$HEADERS" | awk -v name="$(echo "$1" | tr '[:upper:]' '[:lower:]')" '
    BEGIN { IGNORECASE=1; current=""; found=0 }
    /^[A-Za-z][A-Za-z0-9-]*:/ {
      if (found) { print current; exit }
      hdr=tolower($1); sub(":", "", hdr)
      if (hdr == name) {
        current=$0; sub(/^[A-Za-z][A-Za-z0-9-]*:[[:space:]]*/, "", current)
        found=1; next
      } else { current=""; found=0 }
    }
    found && /^[[:space:]]/ { sub(/^[[:space:]]+/, " ", $0); current=current $0 }
    END { if (found) print current }
  ' | head -c 1024
}

MESSAGE_ID=$(extract_header "Message-ID")
SUBJECT=$(extract_header "Subject")
FROM_HDR=$(extract_header "From")
DATE_HDR=$(extract_header "Date")

RECIPIENT="${RECIPIENT_ARG}"
[[ -z "$RECIPIENT" ]] && RECIPIENT=$(extract_header "Delivered-To")
[[ -z "$RECIPIENT" ]] && RECIPIENT=$(extract_header "To")

SENDER="${SENDER_ARG}"
[[ -z "$SENDER" ]] && SENDER="$FROM_HDR"

# Tiny JSON builder — no jq dependency, but we have to escape quotes,
# backslashes, and control chars in each value.
json_escape() {
  printf '%s' "$1" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' 2>/dev/null \
    || printf '"%s"' "$(printf '%s' "$1" | sed 's/\\\\/\\\\\\\\/g; s/"/\\\\"/g; s/\\r//g; s/\\n/ /g; s/\\t/ /g')"
}

BODY=$(printf '{"recipient":%s,"sender":%s,"subject":%s,"message_id":%s,"date":%s,"folder":"INBOX"}' \
  "$(json_escape "$RECIPIENT")" \
  "$(json_escape "$SENDER")" \
  "$(json_escape "$SUBJECT")" \
  "$(json_escape "$MESSAGE_ID")" \
  "$(json_escape "$DATE_HDR")")

# Fire and forget — even if the panel is down we don't want to delay
# delivery (we already received a copy; nothing left to do for THIS
# message). 5-second timeout caps the worst case.
curl -sS --max-time 5 \
  -X POST "$BETAZEN_HOOK_URL" \
  -H "Content-Type: application/json" \
  -H "X-Mail-Hook-Token: $BETAZEN_HOOK_TOKEN" \
  --data-binary "$BODY" >/dev/null 2>&1 &

exit 0
`
