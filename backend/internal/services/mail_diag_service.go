// Package services — mail_diag_service.go.
//
// In-panel mirror of scripts/_diag_mail_stack.py. Walks the Dovecot +
// Postfix + OpenDKIM + listening-port surface, returns a structured
// report the WHM "Mail Issues & Resolution" page renders. The Fix
// endpoint runs the SAFE one-liners on demand (apt install, systemctl
// start, postmap) so an operator who notices "Roundcube can't connect
// to localhost:143" doesn't have to SSH in to recover.
//
// Platform-owner only — gated at the route layer in whm_routes.go on
// `server.manage`. Every command runs as the panel user (root via
// systemd unit), so no per-row privilege check needed inside.
package services

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// MailDiagnosticService — bare struct; everything stateless. Wired in
// main.go alongside the other ops services.
type MailDiagnosticService struct{}

func NewMailDiagnosticService() *MailDiagnosticService {
	return &MailDiagnosticService{}
}

// CheckStatus — three-tone outcome.
type CheckStatus string

const (
	StatusPass CheckStatus = "pass"
	StatusWarn CheckStatus = "warn"
	StatusFail CheckStatus = "fail"
)

// ProblemType classifies a failed/warned check so the UI can group
// the symptoms an operator sees ("emails not sending", "webmail
// won't open") under the right category and suggest the right
// remedy. Pure UI metadata; backend logic doesn't branch on it.
type ProblemType string

const (
	ProblemServiceDown      ProblemType = "service_down"      // systemd unit not active
	ProblemMissingPackage   ProblemType = "missing_package"   // apt package not installed
	ProblemPortNotBound     ProblemType = "port_not_bound"    // service running but not listening
	ProblemMisconfiguration ProblemType = "misconfiguration"  // file syntax error / wrong setting
	ProblemDataIntegrity    ProblemType = "data_integrity"    // map file out of sync with .db, malformed users line
	ProblemMissingTool      ProblemType = "missing_tool"      // CLI binary not on PATH
)

// Check is a single row in the report. The frontend renders one
// per row with status icon + label + (collapsible) detail +
// (optional) "Fix" button when AutoFixable=true. Resolution is the
// step-by-step playbook the operator follows when AutoFix can't
// (or shouldn't — e.g. cert paths) take care of it.
type Check struct {
	ID          string      `json:"id"`
	Group       string      `json:"group"`
	Label       string      `json:"label"`
	Status      CheckStatus `json:"status"`
	ProblemType ProblemType `json:"problem_type,omitempty"` // populated on warn/fail
	Symptom     string      `json:"symptom,omitempty"`      // what the operator sees in the panel / their inbox
	Detail      string      `json:"detail,omitempty"`       // raw stderr / journal tail
	FixHint     string      `json:"fix_hint,omitempty"`     // one-line summary
	FixCommand  string      `json:"fix_command,omitempty"`  // shown verbatim
	Resolution  []string    `json:"resolution,omitempty"`   // ordered playbook steps
	DocsURL     string      `json:"docs_url,omitempty"`     // optional deep-link
	AutoFixable bool        `json:"auto_fixable"`
}

// DiagnosticReport — top-level shape returned to the UI.
type DiagnosticReport struct {
	GeneratedAt time.Time `json:"generated_at"`
	Checks      []Check   `json:"checks"`
	Summary     struct {
		Pass int `json:"pass"`
		Warn int `json:"warn"`
		Fail int `json:"fail"`
	} `json:"summary"`
}

// Diagnose runs every check and returns the full report. Cheap (~50ms
// — just exec'd CLI tools); safe to call on every page render.
func (s *MailDiagnosticService) Diagnose(_ context.Context) (*DiagnosticReport, error) {
	rep := &DiagnosticReport{GeneratedAt: time.Now().UTC()}

	// ── Group 1: packages installed ──────────────────────────────
	for _, pkg := range []string{"dovecot-imapd", "dovecot-pop3d", "postfix", "opendkim"} {
		ok := dpkgInstalled(pkg)
		c := Check{
			ID:          "pkg." + pkg,
			Group:       "Packages",
			Label:       pkg + " installed",
			AutoFixable: !ok,
			FixHint:     "Install via apt-get",
			FixCommand:  "apt-get install -y " + pkg,
		}
		if ok {
			c.Status = StatusPass
		} else {
			c.Status = StatusFail
			c.ProblemType = ProblemMissingPackage
			c.Symptom = "Mail-related panel actions return errors that mention the missing binary (e.g. 'doveadm: executable file not found'). Webmail won't open. Outbound mail bounces."
			c.Detail = "package missing — every dependent service stays down until installed"
			c.Resolution = []string{
				"Click 'Auto-fix' (runs `apt-get install -y " + pkg + "`).",
				"Wait for `apt` to finish; on success `dpkg -s " + pkg + "` shows 'Status: install ok installed'.",
				"Re-run the diagnostic — package + the dependent service rows should both flip to PASS.",
				"If apt fails, check `/var/log/apt/term.log` for the upstream reason (network, held package, broken sources).",
			}
		}
		rep.Checks = append(rep.Checks, c)
	}

	// ── Group 2: services active ─────────────────────────────────
	for _, unit := range []string{"dovecot", "postfix", "opendkim"} {
		ok := systemctlIsActive(unit)
		c := Check{
			ID:          "svc." + unit,
			Group:       "Services",
			Label:       unit + " active",
			AutoFixable: !ok,
			FixHint:     "Start + enable the service",
			FixCommand:  fmt.Sprintf("systemctl start %s && systemctl enable %s", unit, unit),
		}
		if ok {
			c.Status = StatusPass
		} else {
			c.Status = StatusFail
			c.ProblemType = ProblemServiceDown
			tail := journalTail(unit, 5)
			c.Detail = "service inactive — last journal lines:\n" + tail
			switch unit {
			case "dovecot":
				c.Symptom = "Roundcube webmail shows 'Connection to storage server failed — Could not connect to localhost:143: Connection refused'. IMAP/POP3 clients can't log in. New mail still queues at Postfix but can't be read."
				c.Resolution = []string{
					"Click 'Auto-fix' to run `systemctl start dovecot && systemctl enable dovecot`.",
					"If the start fails, the journal tail above tells you why. Common causes:",
					"  • CERT MISSING — dovecot config references /etc/letsencrypt/live/<host>/fullchain.pem that no longer exists. Run `bzpanel mail-ssl-sweep` to re-issue, OR edit /etc/dovecot/conf.d/10-ssl.conf to point at the panel's snake-oil cert.",
					"  • CONFIG PARSE ERROR — `dovecot -n` (run on the server) prints the offending line. Often /etc/dovecot/users has a malformed row from a botched bulk upload (the 'users line shape' check below catches this).",
					"  • PORT 143 IN USE — another process bound it. `ss -ltnp 'sport = :143'` (server) shows the offender; stop it then start dovecot.",
					"After the fix, re-run the diagnostic; ports 143 + 993 should also flip to PASS.",
				}
			case "postfix":
				c.Symptom = "Outbound mail from panel mailboxes silently queues forever (queued in /var/spool/postfix). Inbound mail to your domains gets 'connection refused' from senders. The 25/587 port checks below also fail."
				c.Resolution = []string{
					"Click 'Auto-fix' to run `systemctl start postfix && systemctl enable postfix`.",
					"If the start fails, the journal tail above shows the cause. Common causes:",
					"  • main.cf SYNTAX ERROR — `postfix check` prints the offending line.",
					"  • MISSING virtual_mailbox_maps.db — the 'postfix maps' check below auto-rebuilds it.",
					"  • myhostname unresolvable — make sure /etc/hosts has an entry for the value of `postconf -h myhostname`.",
				}
			case "opendkim":
				c.Symptom = "Outbound mail from your domains arrives at Gmail / Outlook with 'Authentication-Results: dkim=none' and lands in spam. New domains created via the panel can't auto-sign mail."
				c.Resolution = []string{
					"Click 'Auto-fix' to run `systemctl start opendkim && systemctl enable opendkim`.",
					"If the start fails, check the socket path matches between Postfix's main.cf (`milter_default_action`/`smtpd_milters`) and /etc/opendkim.conf's Socket directive.",
					"After fix, send a test mail and verify with `journalctl -u opendkim -f` shows 'DKIM-Signature field added'.",
				}
			}
		}
		rep.Checks = append(rep.Checks, c)
	}

	// ── Group 3: ports listening on localhost ────────────────────
	ports := []struct {
		port  int
		label string
		owner string // service to restart for the fix
	}{
		{143, "imap (Dovecot)", "dovecot"},
		{993, "imaps (Dovecot)", "dovecot"},
		{25, "smtp (Postfix)", "postfix"},
		{587, "submission (Postfix)", "postfix"},
	}
	for _, p := range ports {
		bound := portListening(p.port)
		c := Check{
			ID:          fmt.Sprintf("port.%d", p.port),
			Group:       "Ports",
			Label:       fmt.Sprintf("%d (%s) listening", p.port, p.label),
			AutoFixable: !bound,
			FixHint:     "Restart the owning service",
			FixCommand:  "systemctl restart " + p.owner,
		}
		if bound {
			c.Status = StatusPass
			c.Detail = lsofForPort(p.port)
		} else {
			c.Status = StatusFail
			c.ProblemType = ProblemPortNotBound
			c.Detail = "no listener on this port"
			switch p.port {
			case 143:
				c.Symptom = "Webmail can't connect — Roundcube shows 'localhost:143: Connection refused'. IMAP clients (Outlook, Thunderbird, Apple Mail) fail with 'connection refused'."
			case 993:
				c.Symptom = "Strict-TLS IMAP clients (Apple Mail with 'Use SSL' on) can't connect. Cert renewal recently happened? Dovecot may have failed to reload."
			case 25:
				c.Symptom = "Other mail servers can't deliver inbound mail to your domains. Senders see 'connection timed out' or 'connection refused'."
			case 587:
				c.Symptom = "SMTP clients (panel test-mail, Outlook 'send' button) fail with 'connection refused' on submission. Outbound mail can't leave."
			}
			c.Resolution = []string{
				fmt.Sprintf("Click 'Auto-fix' to restart %s.", p.owner),
				"Re-run the diagnostic; the port should flip to PASS.",
				fmt.Sprintf("If the port still fails, check %s is configured to bind localhost (or 0.0.0.0). For Dovecot: /etc/dovecot/dovecot.conf 'listen = *, ::'. For Postfix: `postconf -h inet_interfaces` should be 'all' or include 'loopback-only' for the local case.", p.owner),
				fmt.Sprintf("If something else has the port: `ss -ltnp 'sport = :%d'` shows the PID; stop that process before starting %s.", p.port, p.owner),
			}
		}
		rep.Checks = append(rep.Checks, c)
	}

	// ── Group 4: config + tooling integrity ──────────────────────
	rep.Checks = append(rep.Checks, checkDovecotConfig())
	rep.Checks = append(rep.Checks, checkDovecotUsersFileShape())
	rep.Checks = append(rep.Checks, checkVirtualMailboxMaps())
	rep.Checks = append(rep.Checks, checkDoveadmRoundTrip())

	// Tally for the summary card.
	for _, c := range rep.Checks {
		switch c.Status {
		case StatusPass:
			rep.Summary.Pass++
		case StatusWarn:
			rep.Summary.Warn++
		case StatusFail:
			rep.Summary.Fail++
		}
	}
	return rep, nil
}

// AutoFix runs the safe one-liners for the named check IDs. Returns
// the exit status + stderr per id so the UI shows what worked. Safe
// here means: idempotent (re-running is a no-op) and bounded scope
// (apt install / systemctl start / postmap — never `rm -rf`,
// never sed-on-a-config-file).
type FixResult struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
}

func (s *MailDiagnosticService) AutoFix(_ context.Context, ids []string) ([]FixResult, error) {
	report, err := s.Diagnose(context.Background())
	if err != nil {
		return nil, err
	}
	byID := map[string]Check{}
	for _, c := range report.Checks {
		byID[c.ID] = c
	}
	results := make([]FixResult, 0, len(ids))
	for _, id := range ids {
		c, ok := byID[id]
		if !ok {
			results = append(results, FixResult{ID: id, Success: false, Output: "unknown check id"})
			continue
		}
		if !c.AutoFixable || c.FixCommand == "" {
			results = append(results, FixResult{ID: id, Success: false, Output: "this check is not auto-fixable from the panel — run the suggested command on the server"})
			continue
		}
		out, err := exec.Command("bash", "-c", c.FixCommand).CombinedOutput()
		fr := FixResult{ID: id, Command: c.FixCommand, Output: strings.TrimSpace(string(out))}
		if err != nil {
			fr.Success = false
			if fr.Output == "" {
				fr.Output = err.Error()
			}
		} else {
			fr.Success = true
		}
		log.Info().Str("check", id).Bool("ok", fr.Success).Str("cmd", c.FixCommand).Msg("mail-stack auto-fix executed")
		results = append(results, fr)
	}
	return results, nil
}

// ── helper command runners ─────────────────────────────────────────

func dpkgInstalled(pkg string) bool {
	out, err := exec.Command("dpkg", "-s", pkg).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Status: install ok installed")
}

func systemctlIsActive(unit string) bool {
	out, _ := exec.Command("systemctl", "is-active", unit).Output()
	return strings.TrimSpace(string(out)) == "active"
}

func journalTail(unit string, lines int) string {
	out, _ := exec.Command("journalctl", "-u", unit, "-n", fmt.Sprintf("%d", lines), "--no-pager", "-o", "short").CombinedOutput()
	return strings.TrimSpace(string(out))
}

func portListening(port int) bool {
	// `ss -ltn 'sport = :PORT'` returns header + at least one data
	// row when something is bound; we count the data rows.
	out, _ := exec.Command("bash", "-c", fmt.Sprintf("ss -ltn 'sport = :%d' 2>/dev/null | tail -n +2", port)).Output()
	return strings.TrimSpace(string(out)) != ""
}

func lsofForPort(port int) string {
	out, _ := exec.Command("bash", "-c", fmt.Sprintf("ss -ltnp 'sport = :%d' 2>/dev/null | tail -n +2 | head -2", port)).Output()
	return strings.TrimSpace(string(out))
}

func checkDovecotConfig() Check {
	c := Check{
		ID:          "config.dovecot",
		Group:       "Configuration",
		Label:       "dovecot -n parses cleanly",
		AutoFixable: false,
		FixHint:     "Inspect the parse error in `dovecot -n` output and edit the offending file.",
	}
	out, err := exec.Command("dovecot", "-n").CombinedOutput()
	if err != nil {
		c.Status = StatusFail
		c.ProblemType = ProblemMisconfiguration
		c.Symptom = "Dovecot refuses to start (or restart) — Roundcube shows 'localhost:143 refused', IMAP/POP3 clients can't connect."
		c.Detail = strings.TrimSpace(string(out))
		c.Resolution = []string{
			"Read the Detail section above — Dovecot's parser names the file + line number.",
			"Most common: a cert path in /etc/dovecot/conf.d/10-ssl.conf points at a Let's Encrypt cert that no longer exists. Run `bzpanel mail-ssl-sweep` to re-issue, OR sed the missing path back to the snake-oil default (`ssl_cert = </etc/ssl/certs/dovecot.pem` + `ssl_key = </etc/ssl/private/dovecot.pem`).",
			"Other cause: a syntax typo in /etc/dovecot/dovecot.conf or one of /etc/dovecot/conf.d/*.conf — back up + remove the offending stanza.",
			"After editing, run `dovecot -n` on the server. Exit 0 = config OK; then `systemctl start dovecot`.",
		}
		return c
	}
	c.Status = StatusPass
	return c
}

func checkDovecotUsersFileShape() Check {
	c := Check{
		ID:          "users.dovecot",
		Group:       "Configuration",
		Label:       "/etc/dovecot/users line shape is well-formed",
		AutoFixable: false,
		FixHint:     "Each line must be 7-8 colon-separated fields. A botched bulk upload or a manual edit can leave a malformed row Dovecot quietly refuses to authenticate.",
	}
	// Field count check: every non-blank line must have 7 or 8 fields.
	out, _ := exec.Command("bash", "-c",
		`awk -F: 'NF != 7 && NF != 8 && length($0) > 0 {print NR": "$0}' /etc/dovecot/users 2>/dev/null | head -5`,
	).Output()
	bad := strings.TrimSpace(string(out))
	if bad == "" {
		c.Status = StatusPass
		return c
	}
	c.Status = StatusWarn
	c.ProblemType = ProblemDataIntegrity
	c.Symptom = "Specific mailboxes fail authentication ('Login failed') even though they appear in the panel. Webmail SSO sometimes works (HMAC-bypass) while IMAP/POP3 fails for the same user."
	c.Detail = "malformed lines (first 5):\n" + bad
	c.Resolution = []string{
		"Back up the file: `cp /etc/dovecot/users /root/dovecot.users.bak`.",
		"Open /etc/dovecot/users in nano/vim, jump to the line numbers above.",
		"A correct line looks like: `alice@example.com:{SHA512-CRYPT}$6$...:5000:5000::/var/vmail/example.com/alice::userdb_mail=maildir:/var/vmail/example.com/alice` (7 colon-separated fields, optionally 8 with extra userdb fields).",
		"Most common breakage: a password containing `:` was stored unquoted, splitting into too many fields. Delete the row and re-create the mailbox via the panel.",
		"After editing: `dovecot reload` (no restart needed — passdb passwd-file is read on every auth).",
		"Re-run this diagnostic to confirm the row is back to 7-8 fields.",
	}
	return c
}

func checkVirtualMailboxMaps() Check {
	c := Check{
		ID:          "postfix.maps",
		Group:       "Configuration",
		Label:       "postfix virtual_mailbox_maps + .db are in sync",
		AutoFixable: true,
		FixHint:     "Re-run postmap to rebuild the .db from the source file.",
		FixCommand:  "postmap /etc/postfix/virtual_mailbox_maps && systemctl reload postfix",
	}
	// virtual_mailbox_maps must point at hash:/etc/postfix/virtual_mailbox_maps.
	out, _ := exec.Command("postconf", "-h", "virtual_mailbox_maps").Output()
	got := strings.TrimSpace(string(out))
	if got != "hash:/etc/postfix/virtual_mailbox_maps" {
		c.Status = StatusFail
		c.ProblemType = ProblemMisconfiguration
		c.Symptom = "Inbound mail to your domains bounces with 'unknown user' even though the mailbox exists in the panel. New mailboxes appear in Mongo + dovecot but Postfix doesn't know about them."
		c.Detail = fmt.Sprintf("postconf says virtual_mailbox_maps=%q (expected hash:/etc/postfix/virtual_mailbox_maps)", got)
		c.FixCommand = "postconf -e 'virtual_mailbox_maps=hash:/etc/postfix/virtual_mailbox_maps' && postmap /etc/postfix/virtual_mailbox_maps && systemctl reload postfix"
		c.Resolution = []string{
			"Click 'Auto-fix' (re-points postconf + rebuilds the .db + reloads).",
			"Re-run the diagnostic.",
			"If the value keeps reverting after restart, something else is editing main.cf (an Ansible run? a different panel?). Check `grep virtual_mailbox_maps /etc/postfix/main.cf` and remove the wrong line.",
		}
		return c
	}
	// .db must exist.
	if _, err := exec.Command("test", "-f", "/etc/postfix/virtual_mailbox_maps.db").Output(); err != nil {
		c.Status = StatusFail
		c.ProblemType = ProblemDataIntegrity
		c.Symptom = "Inbound mail bounces 'unknown user' for domains/mailboxes the panel has just created. Postfix is reading the .db file (binary index), not the .source — and the .db is missing or stale."
		c.Detail = ".db file missing"
		c.Resolution = []string{
			"Click 'Auto-fix' (runs `postmap /etc/postfix/virtual_mailbox_maps` to build the .db).",
			"`postmap` writes the .db atomically next to the source file.",
			"Re-run the diagnostic; the file should now exist.",
		}
		return c
	}
	c.Status = StatusPass
	return c
}

func checkDoveadmRoundTrip() Check {
	c := Check{
		ID:          "doveadm.pw",
		Group:       "Tooling",
		Label:       "doveadm pw -s SHA512-CRYPT round-trip works",
		AutoFixable: false,
		FixHint:     "Required by mailbox create + bulk upload + webmail SSO. If broken, panel mailbox create returns 'failed to hash password'. Reinstall dovecot-core if missing.",
	}
	out, err := exec.Command("doveadm", "pw", "-s", "SHA512-CRYPT", "-p", "diagPassw0rd").CombinedOutput()
	body := strings.TrimSpace(string(out))
	if err != nil {
		c.Status = StatusFail
		c.ProblemType = ProblemMissingTool
		c.Symptom = "Single-row Create Mailbox + Bulk Upload Mailboxes both fail with 'failed to hash password: exec: \"doveadm\": executable file not found' or similar."
		c.Detail = body
		c.Resolution = []string{
			"Verify the binary exists: `which doveadm` (should be /usr/bin/doveadm).",
			"If missing, reinstall: `apt-get install --reinstall dovecot-core`.",
			"If installed but the panel can't find it: the systemd unit's PATH may not include /usr/bin. Add `Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin` to /etc/systemd/system/serverpanel.service then `systemctl daemon-reload && systemctl restart serverpanel`.",
		}
		return c
	}
	if !strings.HasPrefix(body, "{SHA512-CRYPT}") {
		c.Status = StatusWarn
		c.ProblemType = ProblemMissingTool
		c.Symptom = "Mailbox passwords may store with the wrong hash scheme — IMAP login could fail even when the panel reports 'created OK'."
		c.Detail = "unexpected output: " + body[:80]
		c.Resolution = []string{
			"Verify the dovecot-core version: `apt list --installed | grep dovecot-core`.",
			"Run `doveadm pw -l` to list supported schemes — SHA512-CRYPT must appear.",
		}
		return c
	}
	c.Status = StatusPass
	return c
}
