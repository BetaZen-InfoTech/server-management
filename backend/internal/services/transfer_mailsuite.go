package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// migrateMailSuite carries the standalone Mail Suite product from the SOURCE
// box to THIS (destination) panel during a server transfer. Server transfer +
// backup/restore historically dropped mail-suite entirely (only the panel and
// the classic Roundcube mail stack moved), so a migrated box lost its Gmail-
// style mail app and its domain.
//
// What moves:
//   - /opt/mail-suite               — the binary, the .env (JWT secret + panel
//                                     service token — regenerating these would
//                                     break existing sessions AND the panel's
//                                     registered deployment token), and the
//                                     built webmail dist.
//   - /etc/systemd/system/mail-suite.service   — the unit.
//   - /etc/nginx/sites-available/mail-suite.conf + sites-enabled symlink.
//   - a mail_suite_deployments row in THIS panel's DB (so WHM → Mail Suite
//     lists it), read from the migrated .env's PUBLIC_URL + PANEL_SERVICE_TOKEN.
//
// What does NOT move (documented in the step summary): the mail_suite mongo
// user/account rows. Those re-provision automatically on the next IMAP login
// (the mailbox is the source of truth), so functional continuity holds;
// signatures/forwarders are the only soft loss. This keeps the step decoupled
// from the entangled panel-DB sync, since current installs point mail-suite at
// the shared serverpanel DB.
//
// Same-domain transfers only: the migrated .env keeps its PUBLIC_URL, so the
// destination serves the same mail hostname (which the operator re-points at
// the new IP like every other domain). Self-skips when the source has no
// mail-suite, so it's a no-op on cPanel/Plesk/bare sources and panels that
// never installed it.
func (s *TransferService) migrateMailSuite(ctx context.Context, jobID, host string, port int, user, pass string) {
	const step = "Transfer Mail Suite"

	chk, err := agent.SSHCommand(ctx, host, port, user, pass,
		"test -f /opt/mail-suite/.env && echo yes || echo no")
	if err != nil || chk == nil || strings.TrimSpace(chk.Output) != "yes" {
		s.completeStep(ctx, jobID, step, "Source has no Mail Suite install — skipped")
		return
	}
	s.addLog(ctx, jobID, "info", "Source has Mail Suite — migrating install + config", "mail-suite")

	tmpDir := filepath.Join(os.TempDir(), "bz-mailsuite-"+jobID)
	_ = os.MkdirAll(tmpDir, 0o755)
	defer os.RemoveAll(tmpDir)

	// 1. /opt/mail-suite (binary + .env + webmail dist). Tarred `-C /opt
	//    mail-suite` on the source, so extracting into /opt recreates the dir.
	optTar := filepath.Join(tmpDir, "opt-mail-suite.tar.gz")
	if err := agent.RemoteTarPath(ctx, host, port, user, pass, "/opt/mail-suite", optTar); err != nil {
		s.failStep(ctx, jobID, step, "download /opt/mail-suite: "+err.Error())
		return
	}
	if err := agent.LocalUntar(ctx, optTar, "/opt"); err != nil {
		s.failStep(ctx, jobID, step, "extract /opt/mail-suite: "+err.Error())
		return
	}

	// 2. systemd unit (best-effort — the install dir is the critical part).
	unitTar := filepath.Join(tmpDir, "unit.tar.gz")
	if err := agent.RemoteTarPath(ctx, host, port, user, pass, "/etc/systemd/system/mail-suite.service", unitTar); err == nil {
		_ = agent.LocalUntar(ctx, unitTar, "/etc/systemd/system")
	} else {
		s.addLog(ctx, jobID, "warn", "mail-suite.service unit not copied: "+err.Error(), "mail-suite")
	}

	// 3. nginx vhost (best-effort) + re-enable the sites-enabled symlink.
	vhostTar := filepath.Join(tmpDir, "vhost.tar.gz")
	if err := agent.RemoteTarPath(ctx, host, port, user, pass, "/etc/nginx/sites-available/mail-suite.conf", vhostTar); err == nil {
		if err := agent.LocalUntar(ctx, vhostTar, "/etc/nginx/sites-available"); err == nil {
			agent.RunCommand(ctx, "ln", "-sf",
				"/etc/nginx/sites-available/mail-suite.conf",
				"/etc/nginx/sites-enabled/mail-suite.conf")
		}
	} else {
		s.addLog(ctx, jobID, "warn", "mail-suite nginx vhost not copied: "+err.Error(), "mail-suite")
	}

	// 4. Read the public domain + service token from the migrated .env.
	publicURL := readEnvKeyLocal("/opt/mail-suite/.env", "PUBLIC_URL")
	token := readEnvKeyLocal("/opt/mail-suite/.env", "PANEL_SERVICE_TOKEN")

	// 5. Enable + start the service; reload nginx if its config still parses.
	agent.RunCommand(ctx, "systemctl", "daemon-reload")
	if _, err := agent.RunCommand(ctx, "systemctl", "enable", "--now", "mail-suite.service"); err != nil {
		s.addLog(ctx, jobID, "warn", "systemctl enable mail-suite: "+err.Error(), "mail-suite")
	}
	if _, err := agent.RunCommand(ctx, "nginx", "-t"); err == nil {
		agent.RunCommand(ctx, "systemctl", "reload", "nginx")
	} else {
		s.addLog(ctx, jobID, "warn", "nginx -t failed after mail-suite vhost — review manually", "mail-suite")
	}

	// 6. Register the deployment in THIS panel's DB so WHM → Mail Suite sees it.
	if publicURL != "" {
		s.registerMailSuiteDeployment(ctx, publicURL, token)
	}

	msg := "Mail Suite install + config migrated"
	if publicURL != "" {
		msg += " (" + publicURL + ")"
	}
	msg += "; mailbox users re-provision on first IMAP login (signatures/forwarders not carried)"
	s.completeStep(ctx, jobID, step, msg)
}

// registerMailSuiteDeployment upserts (by URL) the mail_suite_deployments row
// the WHM Mail Suite page reads, so a migrated deployment shows up without the
// operator re-registering it by hand.
func (s *TransferService) registerMailSuiteDeployment(ctx context.Context, url, token string) {
	now := time.Now()
	webmail := strings.TrimRight(url, "/") + "/mail/"
	_, _ = s.db.Collection(database.ColMailSuiteDeployments).UpdateOne(ctx,
		bson.M{"url": url},
		bson.M{
			"$set":         bson.M{"service_token": token, "webmail_url": webmail, "updated_at": now},
			"$setOnInsert": bson.M{"label": "mail-suite", "url": url, "created_at": now},
		},
		options.Update().SetUpsert(true),
	)
}

// readEnvKeyLocal returns the value of KEY=... from a local dotenv file,
// trimming surrounding quotes. Empty string when the file or key is absent.
func readEnvKeyLocal(file, key string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+"=") {
			v := strings.TrimSpace(strings.TrimPrefix(line, key+"="))
			v = strings.Trim(v, `"'`)
			return v
		}
	}
	return ""
}
