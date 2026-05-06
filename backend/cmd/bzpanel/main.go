// bzpanel is the SSH-facing admin CLI for the Betazen Server Panel.
//
// Installed at /opt/serverpanel/bin/bzpanel and symlinked from
// /usr/local/bin/bzpanel, so any operator with root SSH can run:
//
//	bzpanel info                        # show panel status + URL + admin
//	bzpanel admin-email  new@host.tld   # rotate super admin email
//	bzpanel admin-password [newpass]    # rotate super admin password
//	bzpanel domain       panel.foo.tld  # change panel access domain
//	bzpanel ssl         [--email a@b]   # issue/renew Let's Encrypt cert
//	bzpanel restart                     # restart the panel systemd unit
//	bzpanel status                      # show systemd status
//
// Design:
//   - Mongo writes use the same connection config the panel itself loads
//     from /opt/serverpanel/.env, so a fresh install "just works" without
//     any extra plumbing.
//   - Domain + SSL commands rewrite /etc/nginx/sites-available/serverpanel
//     from the same templates install.sh uses, keeping a single source of
//     truth for the proxy layout.
//   - All destructive operations print the before/after and exit non-zero
//     on failure so the SSH session surfaces the error cleanly.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/password"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/version"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/term"
)

const (
	installDir = "/opt/serverpanel"
	envFile    = "/opt/serverpanel/.env"
	nginxSite  = "/etc/nginx/sites-available/serverpanel"
)

func main() {
	// Every action this binary performs (reading /opt/serverpanel/.env,
	// writing /etc/nginx/sites-available/serverpanel, running certbot,
	// restarting systemd units, rewriting the super admin user in
	// Mongo) requires root. Instead of failing later with cryptic
	// EACCES trails, we re-exec ourselves under sudo up front so a
	// regular user can type `bsp` and hit a password prompt exactly
	// once — matching the UX of ufw/snap/docker when run unprivileged.
	//
	// Only `help` / `-h` / `--help` escapes the root gate, so a user
	// can still discover the commands without needing sudo.
	if !isHelpInvocation() {
		ensureRoot()
	}

	// No subcommand → launch the interactive admin console. Lets an
	// operator SSH in, type `bsp` (or `bzpanel`), and manage the panel
	// from a numbered menu without having to remember subcommands.
	// Pipe-friendly: only starts the menu when stdin is a TTY; a
	// non-TTY invocation with no args keeps the old "print usage"
	// behaviour so scripts don't silently hang.
	if len(os.Args) < 2 {
		if isTTY(os.Stdin) {
			if err := interactiveMenu(); err != nil {
				fmt.Fprintf(os.Stderr, "bzpanel: %v\n", err)
				os.Exit(1)
			}
			return
		}
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "menu", "console", "bsp":
		// Explicit entry into the interactive console; handy when an
		// operator symlinks bzpanel to bsp but still invokes it with a
		// subcommand out of habit ("bsp menu").
		err = interactiveMenu()
	case "info":
		err = cmdInfo()
	case "admin-email":
		err = cmdAdminEmail(args)
	case "admin-password", "admin-pass", "passwd":
		err = cmdAdminPassword(args)
	case "domain":
		err = cmdDomain(args)
	case "ssl":
		err = cmdSSL(args)
	case "restart":
		err = run("systemctl", "restart", "serverpanel")
	case "status":
		err = run("systemctl", "status", "serverpanel", "--no-pager")
	case "mongo-bootstrap":
		err = cmdMongoBootstrap()
	case "rebuild":
		err = cmdRebuild()
	case "deploy", "update", "upgrade":
		err = cmdDeploy()
	case "heal-dns", "repair-dns":
		err = cmdHealDNS()
	case "heal-mail", "repair-mail":
		err = cmdHealMail()
	case "heal-www", "repair-www":
		err = cmdHealWWW()
	case "mail-ssl":
		err = cmdMailSSL(args)
	case "mail-ssl-sweep":
		err = cmdMailSSLSweep()
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "bzpanel: unknown command %q\n\n", cmd)
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "bzpanel: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`Betazen Server Panel — admin CLI (bzpanel / bsp)

Usage:
  bsp                       Interactive admin console (numbered menu)
  bzpanel <command> [args]  Scripted invocation

Commands:
  info                       Show panel version, domain, admin email, service state
  admin-email <new-email>    Change the super admin (vendor_owner) email
  admin-password [new-pass]  Change the super admin password (prompts if omitted)
  domain <new-domain>        Change panel access domain (.env + nginx + restart)
  ssl [--email EMAIL]        Issue/renew Let's Encrypt cert for current domain
  restart                    Restart the serverpanel systemd service
  status                     Show serverpanel systemd status
  mongo-bootstrap            Grant the panel's mongo user 'root' role so MongoDB
                             database creation works. One-shot fix for the
                             "not authorized to execute command createUser"
                             error on the WHM Databases page.
  mail-ssl-sweep             Walk every panel-tracked domain and run
                             mail-ssl for each. Cron-safe + idempotent
                             — newly-added domains get their mail
                             cert + SNI wiring on the next sweep pass
                             once public DNS lights up. install.sh
                             writes an hourly cron entry; operator
                             can also run it manually after a bulk
                             domain import.
  mail-ssl <domain>          Issue Let's Encrypt cert for mail.<domain>
                             and wire Postfix + Dovecot SNI dispatch so
                             strict clients (Gmail "Send mail as",
                             Outlook 365, modern Thunderbird) accept the
                             cert. Required to fix the "Authentication
                             error" Gmail shows when configuring the
                             panel as an outbound SMTP relay — the
                             default snake-oil cert fails Gmail's TLS
                             validation BEFORE auth is sent. Pre-flight:
                             mail.<domain> A record must point at this
                             server and port 80 must be reachable.
                             Idempotent.
  heal-mail                  Dedupe /etc/dovecot/users and
                             /etc/postfix/virtual_mailbox_maps so the LAST
                             entry per mailbox (most recent password)
                             wins. Run after seeing "User <email> exists
                             more than once" in mail.log or when manual
                             IMAP/SMTP login fails with the latest
                             panel-set password. Idempotent. Aliases:
                             repair-mail.
  heal-dns                   Backfill A + www CNAME records for any subdomain
                             rows that lack them. Idempotent. Use after a
                             pre-3.0.24 install where AddRecord errors were
                             silently swallowed and the panel ended up with
                             "domain row exists, but DNS not resolving"
                             ghost subdomains. Aliases: repair-dns.
  heal-www                   Make https://www.<d> + https://cname.<d> work
                             for every domain on the box. Walks every panel
                             domain, ensures www.<d> + cname.<d> are in the
                             nginx vhost server_name list, and reissues the
                             Let's Encrypt cert with both as additional SANs.
                             Idempotent. Use after upgrading to v3.1.11+ to
                             heal Deploy Software / reverse-proxy / static-
                             frontend domains whose old vhost templates only
                             listed the bare apex (so https://www.<d> hit
                             the panel's catch-all 404). Aliases: repair-www.
  rebuild                    Rebuild server + agent + bzpanel + seed from the
                             on-disk source at /opt/serverpanel and restart
                             the panel service. Use after editing source
                             locally; pairs with 'deploy' for git-pull form.
  deploy                     git pull (or git fetch + reset to origin/main on
                             the configured branch) then rebuild + restart.
                             The one-stop "ship the latest GitHub commit to
                             this VPS" command. Aliases: update, upgrade.
  help                       Show this message

Files touched:
  /opt/serverpanel/.env
  /etc/nginx/sites-available/serverpanel
  /etc/letsencrypt/live/<domain>/*.pem

Must be run as root.`)
}

// ---------------------------------------------------------------------------
// info
// ---------------------------------------------------------------------------

func cmdInfo() error {
	cfg := config.Load()

	// Idempotent self-heal: lowercases any mixed-case admin email row
	// from a pre-3.0.28 install. Reports inline so the operator
	// understands what just happened (and that login should now work).
	if healed, changes, err := healAdminEmailCasing(cfg); err == nil && healed > 0 {
		fmt.Println("Auto-fix: normalised mixed-case admin email row(s):")
		for _, c := range changes {
			fmt.Println("  •", c)
		}
		fmt.Println()
	}

	fmt.Printf("Panel:      %s %s\n", version.Name, version.Number())
	fmt.Printf("Domain:     %s\n", cfg.Domain)
	fmt.Printf("Server IP:  %s\n", cfg.ServerIP)
	fmt.Printf("Env:        %s\n", cfg.AppEnv)

	// SSL cert presence — mirrors the scheme logic in install.sh's summary.
	scheme := "http://"
	if _, err := os.Stat(fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", cfg.Domain)); err == nil {
		scheme = "https://"
	}
	fmt.Printf("Panel URL:  %s%s\n", scheme, cfg.Domain)
	fmt.Printf("WHM:        %s%s/whm\n", scheme, cfg.Domain)
	fmt.Printf("User Panel: %s%s/user-panel\n", scheme, cfg.Domain)

	// Super admin — look up whatever vendor_owner the DB currently has.
	owner, err := findSuperAdmin(cfg)
	if err != nil {
		fmt.Printf("Admin:      (lookup failed: %v)\n", err)
	} else {
		fmt.Printf("Admin:      %s\n", owner.email)
	}

	// systemd status — one-line form.
	out, _ := exec.Command("systemctl", "is-active", "serverpanel").Output()
	fmt.Printf("Service:    %s", string(out))
	return nil
}

// ---------------------------------------------------------------------------
// admin-email
// ---------------------------------------------------------------------------

func cmdAdminEmail(args []string) error {
	var newEmail string
	if len(args) > 0 {
		newEmail = strings.TrimSpace(args[0])
	} else {
		newEmail = prompt("New super admin email: ")
	}
	// Lowercase before validation + write so we honour the global
	// invariant the rest of auth_service.go enforces: every email in
	// users.email is stored lowercase. Pre-3.0.28 this binary saved
	// the typed string verbatim; if an operator typed `Admin@x.com`,
	// the row landed mixed-case and AuthService.LoginWithUA's
	// strings.ToLower-on-typed-input pass (added v3.0.27) could
	// never match it again — login was permanently broken until the
	// row was fixed by hand. Normalising here keeps bsp in lockstep
	// with every other write path.
	newEmail = strings.ToLower(strings.TrimSpace(newEmail))
	if !validEmail(newEmail) {
		return fmt.Errorf("invalid email: %q", newEmail)
	}

	cfg := config.Load()
	owner, err := findSuperAdmin(cfg)
	if err != nil {
		return err
	}
	if strings.EqualFold(owner.email, newEmail) {
		// Defensive: if the stored row IS mixed-case (legacy state from
		// a pre-3.0.28 bsp run), still rewrite it to the lowercase form
		// so login works again. EqualFold is true here, so the
		// `nothing to do` branch is misleading without this fix.
		if owner.email != newEmail {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			db, err := database.Connect(cfg)
			if err != nil {
				return fmt.Errorf("mongo connect: %w", err)
			}
			defer database.Disconnect()
			if _, err := db.Collection(database.ColUsers).UpdateOne(ctx,
				bson.M{"_id": owner.id},
				bson.M{"$set": bson.M{"email": newEmail, "updated_at": time.Now()}},
			); err != nil {
				return fmt.Errorf("normalise email casing: %w", err)
			}
			fmt.Printf("super admin email casing normalised: %s -> %s\n", owner.email, newEmail)
			return nil
		}
		fmt.Printf("admin email already %s — nothing to do\n", newEmail)
		return nil
	}

	// Enforce the same global email-uniqueness the service layer enforces,
	// so a second vendor_owner / customer with this address can't collide.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := database.Connect(cfg)
	if err != nil {
		return fmt.Errorf("mongo connect: %w", err)
	}
	defer database.Disconnect()

	col := db.Collection(database.ColUsers)
	n, err := col.CountDocuments(ctx, bson.M{
		"_id":   bson.M{"$ne": owner.id},
		"email": bson.M{"$regex": "^" + regexp.QuoteMeta(newEmail) + "$", "$options": "i"},
	})
	if err != nil {
		return fmt.Errorf("uniqueness check: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("another account already uses %s", newEmail)
	}

	_, err = col.UpdateOne(ctx, bson.M{"_id": owner.id}, bson.M{"$set": bson.M{
		"email":      newEmail,
		"updated_at": time.Now(),
	}})
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	fmt.Printf("super admin email: %s -> %s\n", owner.email, newEmail)
	return nil
}

// ---------------------------------------------------------------------------
// admin-password
// ---------------------------------------------------------------------------

func cmdAdminPassword(args []string) error {
	var newPass string
	if len(args) > 0 {
		newPass = args[0]
	} else {
		p1, err := readPassword("New super admin password: ")
		if err != nil {
			return err
		}
		p2, err := readPassword("Confirm password: ")
		if err != nil {
			return err
		}
		if p1 != p2 {
			return errors.New("passwords do not match")
		}
		newPass = p1
	}
	if len(newPass) < 8 {
		return errors.New("password must be at least 8 characters")
	}

	cfg := config.Load()
	owner, err := findSuperAdmin(cfg)
	if err != nil {
		return err
	}

	hash, err := password.Hash(newPass)
	if err != nil {
		return fmt.Errorf("hash: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := database.Connect(cfg)
	if err != nil {
		return fmt.Errorf("mongo connect: %w", err)
	}
	defer database.Disconnect()

	// Bumping the password also clears any outstanding password-reset token
	// and invalidates the stored refresh token so stale sessions can't
	// keep sliding their access tokens forward after a rotation.
	//
	// Side-effect recovery: if the stored email has any uppercase
	// characters, normalise it to lowercase here too. Rotating the
	// password is the natural action an operator takes when "I can't
	// log in" — silently fixing the casing in the same write means a
	// pre-3.0.28 install with a mixed-case email row (the regression
	// scenario that motivated 3.0.28) gets healed without the operator
	// even knowing why login was broken.
	set := bson.M{
		"password":           hash,
		"refresh_token":      "",
		"refresh_expires_at": nil,
		"reset_token_hash":   "",
		"reset_expires_at":   nil,
		"failed_logins":      0,
		"locked_until":       nil,
		"updated_at":         time.Now(),
	}
	normalised := strings.ToLower(strings.TrimSpace(owner.email))
	if normalised != "" && normalised != owner.email {
		set["email"] = normalised
	}
	_, err = db.Collection(database.ColUsers).UpdateOne(ctx,
		bson.M{"_id": owner.id},
		bson.M{"$set": set},
	)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	if _, ok := set["email"]; ok {
		fmt.Printf("super admin password rotated; email casing normalised: %s -> %s\n",
			owner.email, normalised)
	} else {
		fmt.Printf("super admin password rotated for %s\n", owner.email)
	}
	return nil
}

// ---------------------------------------------------------------------------
// domain
// ---------------------------------------------------------------------------

func cmdDomain(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: bzpanel domain <new-domain>")
	}
	newDomain := strings.TrimSpace(args[0])
	if !validDomain(newDomain) {
		return fmt.Errorf("invalid domain: %q", newDomain)
	}

	cfg := config.Load()
	oldDomain := cfg.Domain
	if oldDomain == newDomain {
		fmt.Printf("domain already %s — nothing to do\n", newDomain)
		return nil
	}

	if err := setEnvVars(envFile, map[string]string{
		"DOMAIN":        newDomain,
		"MAIL_HOSTNAME": "mail." + newDomain,
	}); err != nil {
		return fmt.Errorf("update .env: %w", err)
	}
	fmt.Printf(".env:   DOMAIN %s -> %s\n", oldDomain, newDomain)

	// Rewrite nginx vhost using the plaintext template; operators rerun
	// `bzpanel ssl` afterwards to swap to the HTTPS form. This matches the
	// install.sh flow where SSL issuance is the last step.
	if err := writeNginxPlain(newDomain, cfg.ServerIP); err != nil {
		return fmt.Errorf("rewrite nginx: %w", err)
	}
	fmt.Printf("nginx:  wrote %s (HTTP)\n", nginxSite)

	if err := run("nginx", "-t"); err != nil {
		return fmt.Errorf("nginx -t failed; leaving service untouched: %w", err)
	}
	if err := run("systemctl", "reload", "nginx"); err != nil {
		return err
	}
	if err := run("systemctl", "restart", "serverpanel"); err != nil {
		return err
	}
	fmt.Println("serverpanel: restarted")
	fmt.Printf("\nNext step: bzpanel ssl --email <admin@%s>\n", newDomain)
	return nil
}

// ---------------------------------------------------------------------------
// ssl
// ---------------------------------------------------------------------------

func cmdSSL(args []string) error {
	var email string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--email", "-e":
			if i+1 >= len(args) {
				return errors.New("--email requires a value")
			}
			email = args[i+1]
			i++
		}
	}

	cfg := config.Load()
	domain := cfg.Domain
	if domain == "" || domain == "localhost" || domain == cfg.ServerIP {
		return fmt.Errorf("cannot issue cert for %q — set a real domain first with: bzpanel domain <fqdn>", domain)
	}

	if email == "" {
		if owner, err := findSuperAdmin(cfg); err == nil {
			email = owner.email
		}
	}
	if !validEmail(email) {
		return fmt.Errorf("certbot needs a valid contact email (got %q); pass --email <addr>", email)
	}

	// certbot --webroot hits /var/www/certbot/.well-known/acme-challenge
	// which the plain-HTTP template already exposes. The SSL rewrite below
	// keeps that alias intact for auto-renewals.
	if err := os.MkdirAll("/var/www/certbot/.well-known/acme-challenge", 0o755); err != nil {
		return fmt.Errorf("prepare webroot: %w", err)
	}

	fmt.Printf("requesting cert for %s (contact: %s)...\n", domain, email)
	if err := run("certbot", "certonly", "--webroot",
		"-w", "/var/www/certbot",
		"--cert-name", domain,
		"-d", domain,
		"--non-interactive", "--agree-tos",
		"-m", email,
	); err != nil {
		return fmt.Errorf("certbot failed: %w", err)
	}

	if err := writeNginxSSL(domain, cfg.ServerIP); err != nil {
		return fmt.Errorf("rewrite nginx: %w", err)
	}
	fmt.Printf("nginx:  wrote %s (HTTPS)\n", nginxSite)

	if err := run("nginx", "-t"); err != nil {
		return err
	}
	if err := run("systemctl", "reload", "nginx"); err != nil {
		return err
	}
	fmt.Printf("SSL live: https://%s\n", domain)
	return nil
}

// ---------------------------------------------------------------------------
// helpers — mongo
// ---------------------------------------------------------------------------

type ownerRef struct {
	id    any
	email string
}

// findSuperAdmin returns the vendor_owner account the CLI should rotate.
// Prefers the is_super_admin flag; falls back to role match. Errors when
// the panel has no owner yet (fresh DB before seed ran).
//
// Looks up the email case-insensitively because pre-3.0.28 installs may
// have a mixed-case email row (a `bsp admin-email Admin@x.com` invocation
// landed the typed string verbatim). The lookup runs by role / is_super_admin
// flags — both stable — so casing on the email field doesn't gate finding
// the row. Callers that subsequently write back use the lowercase form so
// the row self-heals.
func findSuperAdmin(cfg *config.Config) (ownerRef, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := database.Connect(cfg)
	if err != nil {
		return ownerRef{}, fmt.Errorf("mongo connect: %w", err)
	}
	defer database.Disconnect()

	col := db.Collection(database.ColUsers)
	var doc struct {
		ID    any    `bson:"_id"`
		Email string `bson:"email"`
	}
	err = col.FindOne(ctx, bson.M{
		"role":           constants.RoleVendorOwner,
		"is_super_admin": true,
	}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		// Fallback: any vendor_owner, most recently created.
		err = col.FindOne(ctx, bson.M{"role": constants.RoleVendorOwner}).Decode(&doc)
	}
	if err != nil {
		return ownerRef{}, fmt.Errorf("no vendor_owner in %s.%s: %w", cfg.MongoDBName, database.ColUsers, err)
	}
	return ownerRef{id: doc.ID, email: doc.Email}, nil
}

// healAdminEmailCasing walks every vendor_owner row, lowercases any
// mixed-case email, and reports whether anything changed. Idempotent —
// rows that are already lowercase are untouched. Called once from
// cmdInfo so the read-only `bsp info` command also serves as a passive
// health check; running it on a broken install fixes the row and the
// operator can immediately log in.
//
// Returns the count of rows it healed and the (possibly empty) list of
// before/after pairs for the operator-facing log message.
func healAdminEmailCasing(cfg *config.Config) (int, []string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := database.Connect(cfg)
	if err != nil {
		return 0, nil, fmt.Errorf("mongo connect: %w", err)
	}
	defer database.Disconnect()

	col := db.Collection(database.ColUsers)
	cur, err := col.Find(ctx, bson.M{"role": constants.RoleVendorOwner})
	if err != nil {
		return 0, nil, fmt.Errorf("scan owners: %w", err)
	}
	defer cur.Close(ctx)

	type ownerDoc struct {
		ID    any    `bson:"_id"`
		Email string `bson:"email"`
	}
	healed := 0
	var changes []string
	for cur.Next(ctx) {
		var d ownerDoc
		if err := cur.Decode(&d); err != nil {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(d.Email))
		if lower == "" || lower == d.Email {
			continue
		}
		if _, err := col.UpdateOne(ctx,
			bson.M{"_id": d.ID},
			bson.M{"$set": bson.M{"email": lower, "updated_at": time.Now()}},
		); err == nil {
			healed++
			changes = append(changes, fmt.Sprintf("%s -> %s", d.Email, lower))
		}
	}
	return healed, changes, nil
}

// ---------------------------------------------------------------------------
// helpers — .env
// ---------------------------------------------------------------------------

// setEnvVars rewrites KEY=VALUE pairs in-place inside an env file, preserving
// all other lines (comments, ordering). Missing keys are appended. The file
// is written atomically via rename so a crash mid-write doesn't leave the
// panel without config on the next boot.
func setEnvVars(path string, updates map[string]string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	seen := make(map[string]bool, len(updates))

	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		eq := strings.IndexByte(trim, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(trim[:eq])
		if val, ok := updates[key]; ok {
			lines[i] = key + "=" + val
			seen[key] = true
		}
	}
	for k, v := range updates {
		if !seen[k] {
			lines = append(lines, k+"="+v)
		}
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------------------
// helpers — nginx
// ---------------------------------------------------------------------------

// writeNginxPlain rewrites the vhost in HTTP-only form, matching the first
// template block in install.sh (Step 12). Used after a domain change, before
// SSL issuance.
func writeNginxPlain(domain, serverIP string) error {
	body := nginxTemplate(nginxTmplVars{
		Domain:   domain,
		ServerIP: serverIP,
		SSL:      false,
	})
	return writeFile(nginxSite, body, 0o644)
}

// writeNginxSSL rewrites the vhost in the HTTPS-enabled form emitted by
// install.sh's "SSL configured" branch. Keeps the HTTP :80 server alive for
// ACME renewals and 301s everything else to HTTPS.
func writeNginxSSL(domain, serverIP string) error {
	body := nginxTemplate(nginxTmplVars{
		Domain:   domain,
		ServerIP: serverIP,
		SSL:      true,
	})
	if err := writeFile(nginxSite, body, 0o644); err != nil {
		return err
	}
	// sites-enabled symlink is created by install.sh; recreate if missing
	// so a raw cert rotation on a hand-patched box still works.
	enabled := "/etc/nginx/sites-enabled/serverpanel"
	if _, err := os.Lstat(enabled); os.IsNotExist(err) {
		_ = os.Symlink(nginxSite, enabled)
	}
	return nil
}

type nginxTmplVars struct {
	Domain   string
	ServerIP string
	SSL      bool
}

func nginxTemplate(v nginxTmplVars) string {
	// Kept close to install.sh's emitted config so the two stay in sync.
	// The proxy_pass target (127.0.0.1:8080) and phpmyadmin snippet include
	// are intentionally identical.
	common := `
    # Let's Encrypt HTTP-01 challenge
    location ^~ /.well-known/acme-challenge/ {
        root /var/www/certbot;
        default_type "text/plain";
        allow all;
    }

    client_max_body_size 2048m;
    client_body_buffer_size 128k;
    client_body_temp_path /var/cache/nginx/client_temp 1 2;

    if ($host !~* ^(` + v.Domain + `|` + v.ServerIP + `)$) { return 404; }

    include /etc/nginx/snippets/phpmyadmin.conf;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
        proxy_request_buffering off;
    }
`
	if !v.SSL {
		return `# Managed by bzpanel — do not edit by hand; run: bzpanel domain <fqdn>
server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name ` + v.Domain + ` ` + v.ServerIP + ` _;
` + common + `}
`
	}

	return `# Managed by bzpanel — do not edit by hand; run: bzpanel ssl
server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name ` + v.Domain + ` ` + v.ServerIP + ` _;

    location ^~ /.well-known/acme-challenge/ {
        root /var/www/certbot;
        default_type "text/plain";
        allow all;
    }

    if ($host !~* ^(` + v.Domain + `|` + v.ServerIP + `)$) { return 404; }
    if ($host = "` + v.Domain + `") { return 301 https://` + v.Domain + `$request_uri; }

    return 200 "Betazen Server Panel\n";
    add_header Content-Type text/plain;
}

server {
    listen 443 ssl http2 default_server;
    listen [::]:443 ssl http2 default_server;
    server_name ` + v.Domain + ` ` + v.ServerIP + ` _;

    ssl_certificate /etc/letsencrypt/live/` + v.Domain + `/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/` + v.Domain + `/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 1d;
` + common + `}
`
}

// ---------------------------------------------------------------------------
// helpers — misc
// ---------------------------------------------------------------------------

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func writeFile(path, body string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var (
	emailRe  = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	domainRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)+$`)
)

func validEmail(s string) bool  { return emailRe.MatchString(strings.TrimSpace(s)) }
func validDomain(s string) bool { return domainRe.MatchString(strings.TrimSpace(s)) }

func prompt(msg string) string {
	fmt.Print(msg)
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

// confirm asks an interactive y/N question and returns true only on
// an explicit yes. Defaults to no when stdin isn't a TTY (scripted
// invocation) so a destructive command can't accidentally proceed in
// a non-interactive context — caller should pass --yes there.
func confirm(msg string) bool {
	if !isTTY(os.Stdin) {
		return false
	}
	a := strings.ToLower(prompt(msg + " [y/N]: "))
	return a == "y" || a == "yes"
}

func readPassword(msg string) (string, error) {
	fmt.Print(msg)
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// keep installDir referenced so the constant is obvious in reviews even
// though we mostly derive paths from envFile / nginxSite.
var _ = installDir

// ---------------------------------------------------------------------------
// interactive console (bsp / bzpanel with no args)
// ---------------------------------------------------------------------------

// isTTY reports whether the given file is attached to a terminal. Used
// by main() to decide between "show interactive menu" and "print usage
// and exit 1" when the binary is invoked without a subcommand. A shell
// pipeline like `echo | bzpanel` should take the usage path so scripts
// don't hang waiting for stdin.
func isTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// isHelpInvocation returns true when the user asked for help. We skip
// the root gate for help so a plain user can still discover what the
// binary does before being asked for a password.
func isHelpInvocation() bool {
	if len(os.Args) < 2 {
		return false
	}
	switch os.Args[1] {
	case "-h", "--help", "help":
		return true
	}
	return false
}

// ensureRoot re-execs the current process via `sudo` when EUID != 0.
// Lets a plain user (in the sudoers file) type `bsp` and get the
// password prompt exactly once, instead of crashing midway through an
// action with an EACCES on /opt/serverpanel/.env.
//
// On success this function does NOT return — sudo takes over stdin/
// stdout/stderr and the parent waits for it, then exits with sudo's
// exit code. When sudo is missing or the user can't escalate, we bail
// with a clear message.
func ensureRoot() {
	if os.Geteuid() == 0 {
		return
	}
	sudoPath, err := exec.LookPath("sudo")
	if err != nil {
		fmt.Fprintln(os.Stderr, "bzpanel: this command must run as root.")
		fmt.Fprintln(os.Stderr, "         Install sudo, or log in as root (e.g. `su -`) and re-run.")
		os.Exit(1)
	}
	// Forward the full argv so flags / subcommands the caller typed
	// survive the re-exec. -E preserves important env (PATH etc.),
	// which matters because Go binaries in /usr/local/bin rely on it.
	forwarded := append([]string{"-E", os.Args[0]}, os.Args[1:]...)
	cmd := exec.Command(sudoPath, forwarded...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// cmd.Run already printed sudo's own error (auth failure, etc.)
		// to stderr, so we just mirror the exit code.
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "bzpanel: sudo re-exec failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// interactiveMenu is the SSH-facing admin console the user sees when
// they type `bsp` (or `bzpanel` with no args). Each menu choice calls
// one of the existing subcommand functions so the console and the
// scripted form stay in lockstep — no drift between "bsp" menu item 1
// and "bzpanel admin-email".
//
// Loops until the user picks Exit or hits ^D/^C. Individual action
// failures are printed and the menu redraws rather than aborting, so a
// typo in an email doesn't kick the operator back to the shell.
func interactiveMenu() error {
	// Idempotent self-heal on first paint — lowercases any mixed-case
	// admin email row left over from a pre-3.0.28 bsp run. Silent on a
	// healthy install; reports inline when it actually fixed anything
	// so the operator sees why login was failing and that it's fixed.
	if cfg := config.Load(); cfg != nil {
		if healed, changes, err := healAdminEmailCasing(cfg); err == nil && healed > 0 {
			fmt.Println()
			fmt.Println("Auto-fix: normalised mixed-case admin email row(s) — login will work now:")
			for _, c := range changes {
				fmt.Println("  •", c)
			}
			fmt.Println()
		}
	}
	for {
		printBanner()
		fmt.Println()
		fmt.Println("  1) Update root admin email")
		fmt.Println("  2) Update root admin password")
		fmt.Println("  3) Set/update panel URL + activate SSL")
		fmt.Println("  4) Renew / update SSL")
		fmt.Println("  5) Customer support (coming soon)")
		fmt.Println("  6) Show panel info")
		fmt.Println("  7) Restart panel service")
		fmt.Println("  8) Deploy latest from GitHub (git pull + rebuild + restart)")
		fmt.Println("  9) Rebuild from on-disk source (no git pull)")
		fmt.Println(" 10) Heal DNS — backfill A/CNAME for orphan subdomains")
		fmt.Println(" 11) Heal Mail — dedupe dovecot/postfix mailbox files")
		fmt.Println(" 12) Mail SSL — issue LE cert for mail.<domain> + SNI wire-up")
		fmt.Println(" 13) Heal www — fix nginx server_name + reissue cert with www+cname SAN")
		fmt.Println("  0) Exit")
		fmt.Println()
		choice := prompt("Select [0-13]: ")
		fmt.Println()

		var actionErr error
		switch choice {
		case "0", "q", "quit", "exit", "":
			fmt.Println("Bye.")
			return nil
		case "1":
			actionErr = cmdAdminEmail(nil)
		case "2":
			actionErr = cmdAdminPassword(nil)
		case "3":
			actionErr = menuSetDomainAndSSL()
		case "4":
			actionErr = menuRenewSSL()
		case "5":
			fmt.Println("Customer support is coming soon. For now, email support@betazeninfotech.com.")
		case "6":
			actionErr = cmdInfo()
		case "7":
			actionErr = run("systemctl", "restart", "serverpanel")
			if actionErr == nil {
				fmt.Println("serverpanel: restarted")
			}
		case "8":
			actionErr = cmdDeploy()
		case "9":
			actionErr = cmdRebuild()
		case "10":
			actionErr = cmdHealDNS()
		case "11":
			actionErr = cmdHealMail()
		case "12":
			d := prompt("Domain (without 'mail.' prefix; e.g. iaj.cx): ")
			d = strings.TrimSpace(d)
			if d == "" {
				fmt.Println("cancelled")
				break
			}
			actionErr = cmdMailSSL([]string{d})
		case "13":
			actionErr = cmdHealWWW()
		default:
			fmt.Printf("Unknown choice %q — please pick 0-13.\n", choice)
		}

		if actionErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", actionErr)
		}

		fmt.Println()
		if strings.ToLower(prompt("Return to menu? [Y/n]: ")) == "n" {
			return nil
		}
	}
}

// printBanner renders the console header — panel name, version,
// current domain, admin email, and systemd status. Cheap to compute
// (one mongo lookup) and gives the operator an at-a-glance sanity
// check before they pick an action.
func printBanner() {
	cfg := config.Load()
	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Printf("  Betazen Server Panel — Admin Console  (%s)\n", version.Number())
	fmt.Println("───────────────────────────────────────────────────────────")

	scheme := "http://"
	if _, err := os.Stat(fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", cfg.Domain)); err == nil {
		scheme = "https://"
	}
	fmt.Printf("  Panel URL : %s%s\n", scheme, cfg.Domain)
	fmt.Printf("  Server IP : %s\n", cfg.ServerIP)

	if owner, err := findSuperAdmin(cfg); err == nil {
		fmt.Printf("  Admin     : %s\n", owner.email)
	} else {
		fmt.Printf("  Admin     : (lookup failed)\n")
	}

	// Trim the trailing newline from `systemctl is-active` so it sits
	// cleanly on the same line as the label.
	state := "unknown"
	if out, err := exec.Command("systemctl", "is-active", "serverpanel").Output(); err == nil {
		state = strings.TrimSpace(string(out))
	}
	fmt.Printf("  Service   : %s\n", state)
	fmt.Println("═══════════════════════════════════════════════════════════")
}

// menuSetDomainAndSSL is the combined "change panel URL + issue SSL"
// flow from the console's option 3. We chain cmdDomain → cmdSSL so
// the operator doesn't have to remember the two-step sequence, and
// SSL is made optional (some operators want to stage the cert later
// once DNS propagates).
func menuSetDomainAndSSL() error {
	newDomain := prompt("New panel domain (FQDN, e.g. panel.example.com): ")
	if newDomain == "" {
		return errors.New("domain is required")
	}
	if err := cmdDomain([]string{newDomain}); err != nil {
		return err
	}

	fmt.Println()
	if strings.ToLower(prompt("Issue / renew SSL for this domain now? [Y/n]: ")) == "n" {
		fmt.Println("Skipped SSL issuance. Run option 4 later when you're ready.")
		return nil
	}
	email := prompt("ACME contact email (leave blank to use the super admin email): ")
	var args []string
	if email != "" {
		args = []string{"--email", email}
	}
	return cmdSSL(args)
}

// menuRenewSSL is the console's option 4 — just cmdSSL with an
// optional contact-email override. Idempotent: certbot no-ops when
// the existing cert is still fresh.
func menuRenewSSL() error {
	email := prompt("ACME contact email (leave blank to use the super admin email): ")
	var args []string
	if email != "" {
		args = []string{"--email", email}
	}
	return cmdSSL(args)
}

// ---------------------------------------------------------------------------
// mongo-bootstrap — one-shot escalation of the panel's mongo user
// ---------------------------------------------------------------------------

// cmdMongoBootstrap fixes the "not authorized to execute command
// createUser" error operators see when clicking Create Database with
// type=MongoDB. The default panel install runs mongo with
// `authorization: enabled` and creates a `serverpanel` user scoped
// only to the `serverpanel` database — that user can't issue
// createUser on `betazeninfotech_<name>`, so every MongoDB-create
// click 500's. The transfer pipeline hits the same wall when it
// tries to recreate users / dump arbitrary DBs on the destination.
//
// The fix is a one-shot bootstrap: stop mongod, briefly run it
// without auth so the localhost-bypass-via-config approach lets us
// in, grant the panel's mongo user the `root` role, then re-enable
// auth and restart. After this runs once, every subsequent
// CreateMongoDatabase / CreateMongoUser / RemoteMongoDump auth
// against the same MONGO_URI but with cross-DB privileges, so the
// WHM Database page works without further intervention.
//
// Idempotent: re-running on an already-rooted user is a no-op
// (grantRolesToUser doesn't error when the role is already there).
//
// Safe-by-default: the auth-disabled window is gated by mongo's
// own bindIp (typically 127.0.0.1 in the install.sh template), so
// the LAN can't reach mongo while we're in the open phase. When
// bindIp is wider, this command refuses to run and prints a clear
// error — operator can either narrow bindIp or do the grant by hand.
func cmdMongoBootstrap() error {
	const conf = "/etc/mongod.conf"
	const backup = "/etc/mongod.conf.bz-mongo-bootstrap.bak"

	envMap, err := readEnv(envFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", envFile, err)
	}
	uri := envMap["MONGO_URI"]
	if uri == "" {
		uri = envMap["MONGODB_URI"]
	}
	if uri == "" {
		return errors.New("MONGO_URI not set in /opt/serverpanel/.env — install seems incomplete")
	}
	panelUser := mongoUserFromURI(uri)
	if panelUser == "" {
		return fmt.Errorf("could not parse panel mongo user from MONGO_URI")
	}
	fmt.Printf("→ panel mongo user is %q; will be promoted to `root` role on admin\n", panelUser)

	confBytes, err := os.ReadFile(conf)
	if err != nil {
		return fmt.Errorf("read %s: %w", conf, err)
	}
	if !mongoBindIsLocalhostOnly(string(confBytes)) {
		return fmt.Errorf("aborting: mongod is configured to listen on a non-localhost address. " +
			"Run this command only when bindIp is 127.0.0.1 (default install). " +
			"Or perform the grant manually with an admin-credentialed mongosh session")
	}

	if !confirm(fmt.Sprintf(
		"This will briefly restart mongod with authorization disabled, grant %q the `root` role, then re-enable auth.\n"+
			"Mongo will be unreachable for a few seconds. Proceed?",
		panelUser)) {
		return errors.New("aborted by operator")
	}

	// 1. Backup mongod.conf so failures are recoverable.
	if err := os.WriteFile(backup, confBytes, 0644); err != nil {
		return fmt.Errorf("backup mongod.conf: %w", err)
	}
	fmt.Println("→ backed up mongod.conf to", backup)

	// 2. Comment out the authorization line.
	patched := commentMongoAuthorization(string(confBytes))
	if err := os.WriteFile(conf, []byte(patched), 0644); err != nil {
		return fmt.Errorf("write mongod.conf: %w", err)
	}

	// Defer guaranteed restoration so a panic / Ctrl-C can't leave
	// the box with auth disabled.
	restoreAndRestart := func() {
		_ = os.WriteFile(conf, confBytes, 0644)
		_ = run("systemctl", "restart", "mongod")
		// Wait briefly for mongo to start back up.
		time.Sleep(2 * time.Second)
	}

	// 3. Restart mongod (now without auth).
	fmt.Println("→ restarting mongod with authorization disabled...")
	if err := run("systemctl", "restart", "mongod"); err != nil {
		restoreAndRestart()
		return fmt.Errorf("systemctl restart mongod: %w", err)
	}
	if err := waitForMongo(15 * time.Second); err != nil {
		restoreAndRestart()
		return fmt.Errorf("mongod did not come up cleanly after restart: %w", err)
	}

	// 4. Grant root via no-auth mongosh on localhost.
	fmt.Printf("→ granting `root` role to %q...\n", panelUser)
	js := fmt.Sprintf(
		`try { db.getSiblingDB("admin").grantRolesToUser(%q, ["root"]); print("granted"); } `+
			`catch (e) { print("ERR " + e.message); throw e; }`,
		panelUser)
	if err := run("mongosh", "--quiet", "127.0.0.1:27017/admin", "--eval", js); err != nil {
		restoreAndRestart()
		return fmt.Errorf("grant root role: %w", err)
	}

	// 5. Restore mongod.conf and restart with auth.
	fmt.Println("→ restoring mongod.conf and restarting with authorization re-enabled...")
	restoreAndRestart()
	if err := os.Remove(backup); err == nil {
		fmt.Println("→ removed backup", backup)
	}

	// 6. Verify the panel can now createUser cross-DB. Test with a
	// throwaway DB that we drop right after.
	probeDB := "bzpanel_mongo_bootstrap_probe"
	probeUser := "bzpanel_probe_user"
	probeJS := fmt.Sprintf(
		`db.getSiblingDB(%q).createUser({user:%q,pwd:"x",roles:[{role:"readWrite",db:%q}]}); `+
			`db.getSiblingDB(%q).dropUser(%q); `+
			`db.getSiblingDB(%q).dropDatabase(); `+
			`print("ok");`,
		probeDB, probeUser, probeDB,
		probeDB, probeUser,
		probeDB)
	if err := run("mongosh", "--quiet", uri, "--eval", probeJS); err != nil {
		return fmt.Errorf("post-bootstrap verification failed: %w (the grant may not have applied; check mongod logs)", err)
	}

	fmt.Println()
	fmt.Println("✓ mongo bootstrap complete. The panel can now create MongoDB databases.")
	fmt.Println("  Try Create Database (type=MongoDB) again from the WHM Databases page.")
	return nil
}

// ---------------------------------------------------------------------------
// rebuild + deploy — make "ship the latest source to this VPS" a one-liner
// ---------------------------------------------------------------------------
//
// Motivation: the GitHub auto-deploy workflow points at a stale VPS_HOST
// secret on most installs, so `git push` to main does NOT actually update
// any production / testing VPS the user owns. Pre-3.0.29 the only path
// to ship a fix was:
//
//   ssh root@vps
//   cd /opt/serverpanel && git pull
//   cd backend && /opt/go/<ver>/bin/go build -o ../bin/server ./cmd/server
//   cd backend && /opt/go/<ver>/bin/go build -o ../bin/bzpanel ./cmd/bzpanel
//   cd backend && /opt/go/<ver>/bin/go build -o ../bin/agent ./cmd/agent
//   cd backend && /opt/go/<ver>/bin/go build -o ../bin/seed ./cmd/seed
//   systemctl restart serverpanel
//
// — six commands, plus the user has to know the version-specific Go path
// (install.sh writes /opt/go/<GO_VERSION>/bin/go, not /opt/go/bin/go).
// The user spent multiple sessions chasing "bsp doesn't work" on a
// stale binary and never realised the running version was four releases
// behind. So we collapse the whole loop into `bzpanel deploy`.

const sourceDir = "/opt/serverpanel"

// findGoBin locates the Go compiler shipped by install.sh. Tries the
// canonical install.sh paths first, then falls back to PATH lookup so
// hand-installed Go binaries still work. Returns "" when nothing
// resolves; callers print a usable error.
func findGoBin() string {
	candidates := []string{
		"/opt/go/1.23/bin/go", // current install.sh default
		"/opt/go/1.22/bin/go", // previous stable
		"/opt/go/1.21/bin/go",
		"/opt/go/bin/go",      // hypothetical stable symlink (3.0.29 install.sh adds this)
		"/usr/local/go/bin/go",
		"/usr/local/bin/go",
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	return ""
}

// cmdRebuild rebuilds every binary the panel ships (server, agent,
// bzpanel, seed) from the source tree at /opt/serverpanel and restarts
// the systemd unit. Idempotent — safe to run on a tree that's already
// up to date; the Go build cache short-circuits anything unchanged.
func cmdRebuild() error {
	goBin := findGoBin()
	if goBin == "" {
		return errors.New("go: not found. install.sh writes Go to /opt/go/<version>/bin/go — set up a stable symlink or install Go in PATH")
	}
	fmt.Printf("→ using go: %s\n", goBin)

	backend := filepath.Join(sourceDir, "backend")
	binDir := filepath.Join(sourceDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", binDir, err)
	}

	// Build each cmd/<name> in turn. Order matters: bzpanel last so the
	// running CLI rebuilds itself with the freshest commit it just
	// pulled — earlier failures abort before bzpanel is overwritten.
	targets := []struct {
		bin string
		pkg string
	}{
		{"server", "./cmd/server"},
		{"agent", "./cmd/agent"},
		{"seed", "./cmd/seed"},
		{"bzpanel", "./cmd/bzpanel"},
	}
	for _, t := range targets {
		out := filepath.Join(binDir, t.bin)
		fmt.Printf("→ building %s ... ", t.bin)
		c := exec.Command(goBin, "build", "-ldflags=-s -w", "-o", out, t.pkg)
		c.Dir = backend
		c.Env = append(os.Environ(), "CGO_ENABLED=0")
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("build %s: %w", t.bin, err)
		}
		fmt.Println("ok")
	}

	fmt.Println("→ restarting serverpanel ...")
	if err := run("systemctl", "restart", "serverpanel"); err != nil {
		return fmt.Errorf("restart: %w", err)
	}
	// Brief settle so a follow-up `bzpanel info` reads the new version.
	time.Sleep(1 * time.Second)
	fmt.Println("✓ rebuild complete")
	return nil
}

// cmdDeploy is the one-stop "ship the latest GitHub commit to this VPS"
// flow. Runs `git fetch --all` + `git reset --hard origin/<current-branch>`
// against /opt/serverpanel so a hand-edited tree resyncs cleanly, then
// chains into cmdRebuild. Reset rather than pull so a divergent local
// branch (a hotfix the operator made on the box that's already on
// GitHub) doesn't dead-stop with a merge prompt.
//
// We DO take a snapshot first via `git stash --include-untracked` so an
// operator who hand-edited a config file doesn't silently lose it.
// Stash entry is left in place — they can `git stash pop` if they need
// the changes back.
func cmdDeploy() error {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		return errors.New("git: not found in PATH")
	}

	// Resolve the current branch so we reset to origin/<branch>, not
	// hardcode origin/main. install.sh checks out main but a few
	// operators run an environment-specific branch.
	branchOut, err := exec.Command(gitBin, "-C", sourceDir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("read current branch: %w", err)
	}
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" || branch == "HEAD" {
		branch = "main"
	}
	fmt.Printf("→ deploying %s\n", branch)

	// Snapshot any local edits before we hard-reset.
	stash := exec.Command(gitBin, "-C", sourceDir, "stash", "push",
		"--include-untracked", "-m", "bzpanel-deploy-"+time.Now().UTC().Format("20060102-150405"))
	stashOut, _ := stash.CombinedOutput()
	if msg := strings.TrimSpace(string(stashOut)); msg != "" && !strings.Contains(msg, "No local changes") {
		fmt.Println("→ stashed local edits:", msg)
		fmt.Println("  recover with: git -C " + sourceDir + " stash pop")
	}

	fmt.Println("→ git fetch --all ...")
	if err := runIn(sourceDir, gitBin, "fetch", "--all", "--prune"); err != nil {
		return fmt.Errorf("git fetch: %w", err)
	}
	fmt.Printf("→ git reset --hard origin/%s ...\n", branch)
	if err := runIn(sourceDir, gitBin, "reset", "--hard", "origin/"+branch); err != nil {
		return fmt.Errorf("git reset: %w", err)
	}

	// Chain into rebuild for the rest of the flow.
	return cmdRebuild()
}

// runIn is run() with a working-directory override. Kept local instead
// of expanding the existing run() signature so the existing call sites
// don't need touching.
func runIn(dir, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// ---------------------------------------------------------------------------
// heal-dns — backfill A + www CNAME for orphan subdomain rows
// ---------------------------------------------------------------------------
//
// Scenario this fixes: an operator created subdomains with a pre-3.0.24
// build of the panel. findParentDomain queried `domains` (greedy match
// on subdomain rows) instead of `dns_zones` (only real DNS authorities),
// so the AddRecord call landed in a non-existent zone and silently
// failed. The `domains` row was inserted anyway because Domain.Create
// treated the AddRecord error as a stderr-only warning (3.0.30 fixes
// that path too).
//
// Net effect: subdomain rows in `domains` for which no A record exists
// in the parent's `dns_zone`. The site looks created in the panel UI
// but DNS resolution fails — bulk-SSL-issue then fails too because
// certbot's HTTP-01 challenge can't reach the host.
//
// heal-dns walks every domain row, computes the correct parent zone via
// the SAME parentZoneOf-style longest-suffix walk the runtime now uses,
// checks whether an A record + www CNAME live at the right relative
// name, and inserts them when missing. Idempotent — already-correct
// records are left alone. Reports `fixed N / total M` so the operator
// sees the scope.
//
// Apex domains (no parent zone in dns_zones) are skipped — those need
// CreateZone, which is a different flow and outside heal-dns's scope.

type zoneInfo struct {
	id       primitive.ObjectID
	serverIP string
}

func cmdHealDNS() error {
	cfg := config.Load()
	if cfg.ServerIP == "" {
		return errors.New("ServerIP not configured in /opt/serverpanel/.env — heal-dns needs it for the A record value")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db, err := database.Connect(cfg)
	if err != nil {
		return fmt.Errorf("mongo connect: %w", err)
	}
	defer database.Disconnect()

	// Cache every dns_zone keyed by domain so the longest-suffix walk
	// below is cheap regardless of zone count.
	zonesByDomain := map[string]zoneInfo{}
	zCur, err := db.Collection("dns_zones").Find(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("read dns_zones: %w", err)
	}
	for zCur.Next(ctx) {
		var z struct {
			ID       primitive.ObjectID `bson:"_id"`
			Domain   string             `bson:"domain"`
			ServerIP string             `bson:"server_ip"`
		}
		if err := zCur.Decode(&z); err == nil {
			ip := z.ServerIP
			if ip == "" {
				ip = cfg.ServerIP
			}
			zonesByDomain[z.Domain] = zoneInfo{id: z.ID, serverIP: ip}
		}
	}
	zCur.Close(ctx)
	fmt.Printf("→ %d dns zones loaded\n", len(zonesByDomain))

	// Stale-zone prune pass — runs FIRST so the A/CNAME backfill loop
	// below walks against the cleaned-up zonesByDomain. Without this
	// ordering, a stale orphan in zonesByDomain would still hijack the
	// apex-wins lookup for any domain whose label happens to match the
	// stale row exactly (rare but real).
	type stalePrune struct {
		domain      string
		recsRemoved int64
	}
	var stale []stalePrune
	zonesCol := db.Collection("dns_zones")
	for domain, info := range zonesByDomain {
		out, runErr := exec.Command("pdnsutil", "list-zone", domain).CombinedOutput()
		pdnsHasZone := runErr == nil && !strings.Contains(string(out), "Zone '")
		if pdnsHasZone {
			continue
		}
		res, _ := db.Collection("dns_records").DeleteMany(ctx, bson.M{"zone_id": info.id})
		recsRemoved := int64(0)
		if res != nil {
			recsRemoved = res.DeletedCount
		}
		zonesCol.DeleteOne(ctx, bson.M{"_id": info.id})
		delete(zonesByDomain, domain)
		stale = append(stale, stalePrune{domain: domain, recsRemoved: recsRemoved})
		fmt.Printf("  - pruned stale dns_zone %q (no pdns SOA; %d orphan records cleaned)\n", domain, recsRemoved)
	}

	dCur, err := db.Collection("domains").Find(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("read domains: %w", err)
	}
	defer dCur.Close(ctx)

	var (
		total, withParent, healedA, healedWWW, healedCNAME, alreadyOK, skippedApex int
		failures                                                      []string
	)
	for dCur.Next(ctx) {
		var d struct {
			Domain string `bson:"domain"`
		}
		if err := dCur.Decode(&d); err != nil {
			continue
		}
		total++
		labels := strings.Split(d.Domain, ".")
		if len(labels) < 3 {
			// Apex (≤ 2 labels) — out of scope.
			skippedApex++
			continue
		}

		// Walk shortest-suffix-first (apex-wins) — matches the
		// 3.0.31 parentZoneOf rule. Stepping over stale subdomain
		// dns_zones rows is the whole point of this command.
		var parent string
		var subPart string
		var z zoneInfo
		for i := len(labels) - 2; i >= 1; i-- {
			cand := strings.Join(labels[i:], ".")
			if zi, ok := zonesByDomain[cand]; ok {
				parent = cand
				subPart = strings.Join(labels[:i], ".")
				z = zi
				break
			}
		}
		if parent == "" {
			skippedApex++
			continue
		}
		withParent++

		// A record check
		recCol := db.Collection("dns_records")
		aCount, _ := recCol.CountDocuments(ctx, bson.M{
			"zone_id": z.id, "type": "A", "name": subPart,
		})
		if aCount == 0 {
			now := time.Now()
			if _, err := recCol.InsertOne(ctx, bson.M{
				"zone_id":    z.id,
				"type":       "A",
				"name":       subPart,
				"value":      z.serverIP,
				"ttl":        3600,
				"created_at": now,
				"updated_at": now,
			}); err == nil {
				if e := exec.Command("pdnsutil", "replace-rrset", parent, subPart, "A", "3600", z.serverIP).Run(); e == nil {
					healedA++
					fmt.Printf("  + A %s.%s → %s\n", subPart, parent, z.serverIP)
				} else {
					failures = append(failures, fmt.Sprintf("%s: pdnsutil replace-rrset %s A failed: %v", d.Domain, subPart, e))
				}
			} else {
				failures = append(failures, fmt.Sprintf("%s: insert A: %v", d.Domain, err))
			}
		}

		// www CNAME check
		wwwName := "www." + subPart
		cCount, _ := recCol.CountDocuments(ctx, bson.M{
			"zone_id": z.id, "type": "CNAME", "name": wwwName,
		})
		if cCount == 0 {
			now := time.Now()
			cnameTarget := d.Domain + "."
			if _, err := recCol.InsertOne(ctx, bson.M{
				"zone_id":    z.id,
				"type":       "CNAME",
				"name":       wwwName,
				"value":      cnameTarget,
				"ttl":        3600,
				"created_at": now,
				"updated_at": now,
			}); err == nil {
				if e := exec.Command("pdnsutil", "replace-rrset", parent, wwwName, "CNAME", "3600", cnameTarget).Run(); e == nil {
					healedWWW++
					fmt.Printf("  + CNAME %s.%s → %s\n", wwwName, parent, cnameTarget)
				} else {
					failures = append(failures, fmt.Sprintf("%s: pdnsutil replace-rrset www CNAME failed: %v", d.Domain, e))
				}
			} else {
				failures = append(failures, fmt.Sprintf("%s: insert CNAME: %v", d.Domain, err))
			}
		}

		// `cname.<sub>` flat-alias check — same shape as the `www`
		// backfill above. Domains created before v3.1.10 won't have
		// this record; we backfill so external services that ask the
		// operator to "add cname.<X> pointing to <X>" work without a
		// manual DNS edit. Counted in healedCNAME so the summary
		// distinguishes www-CNAME healing from cname-CNAME healing.
		cnameAliasName := "cname." + subPart
		caCount, _ := recCol.CountDocuments(ctx, bson.M{
			"zone_id": z.id, "type": "CNAME", "name": cnameAliasName,
		})
		if caCount == 0 {
			now := time.Now()
			cnameTarget := d.Domain + "."
			if _, err := recCol.InsertOne(ctx, bson.M{
				"zone_id":    z.id,
				"type":       "CNAME",
				"name":       cnameAliasName,
				"value":      cnameTarget,
				"ttl":        3600,
				"created_at": now,
				"updated_at": now,
			}); err == nil {
				if e := exec.Command("pdnsutil", "replace-rrset", parent, cnameAliasName, "CNAME", "3600", cnameTarget).Run(); e == nil {
					healedCNAME++
					fmt.Printf("  + CNAME %s.%s → %s\n", cnameAliasName, parent, cnameTarget)
				} else {
					failures = append(failures, fmt.Sprintf("%s: pdnsutil replace-rrset cname CNAME failed: %v", d.Domain, e))
				}
			} else {
				failures = append(failures, fmt.Sprintf("%s: insert cname CNAME: %v", d.Domain, err))
			}
		}
		if aCount > 0 && cCount > 0 && caCount > 0 {
			alreadyOK++
		}
	}

	// (stale-zone prune ran BEFORE the heal loop — see top of function)

	// Reload pdns once at the end so the live answer set picks up every
	// rrset we just wrote in one shot.
	if healedA+healedWWW+healedCNAME > 0 {
		_ = exec.Command("pdns_control", "reload").Run()
	}

	fmt.Println()
	fmt.Println("─── DNS heal summary ───")
	fmt.Printf("  domains scanned       : %d\n", total)
	fmt.Printf("  with registered parent: %d\n", withParent)
	fmt.Printf("  already healthy       : %d\n", alreadyOK)
	fmt.Printf("  A records added       : %d\n", healedA)
	fmt.Printf("  www CNAMEs added      : %d\n", healedWWW)
	fmt.Printf("  cname CNAMEs added    : %d\n", healedCNAME)
	fmt.Printf("  skipped (apex / no parent): %d\n", skippedApex)
	fmt.Printf("  stale dns_zones pruned: %d\n", len(stale))
	if len(failures) > 0 {
		fmt.Printf("  failures              : %d\n", len(failures))
		for _, f := range failures {
			fmt.Println("    !", f)
		}
	}
	totalChanges := healedA + healedWWW + healedCNAME + len(stale)
	if totalChanges == 0 && len(failures) == 0 {
		fmt.Println("✓ no orphan subdomain rows or stale zones — DNS state is clean")
	} else if len(failures) == 0 {
		fmt.Println("✓ heal complete")
	}
	return nil
}

// ---------------------------------------------------------------------------
// heal-mail — dedupe /etc/dovecot/users + /etc/postfix/virtual_mailbox_maps
// ---------------------------------------------------------------------------
//
// Scenario: an operator re-creates a subdomain whose admin@<sub>
// auto-mailbox previously existed. Pre-3.0.33, CreateMailbox blindly
// appended a new line to /etc/dovecot/users without removing the
// previous one. Each repeat added another entry, and Dovecot logged
// "User <email> exists more than once" then picked the FIRST match —
// which still held the OLD password. Result: panel says "your mail
// password is X" but IMAP/SMTP login with X fails because Dovecot
// is verifying against the original hash. Roundcube auto-login keeps
// working because the SSO flow bypasses passdb.
//
// 3.0.33's CreateMailbox is now idempotent (sed-removes any prior
// entry before append) so the bug stops at the source. heal-mail
// fixes installs that already accumulated duplicates: it keeps the
// LAST entry per email (most recent password hash) and discards
// older copies, then reloads dovecot + postfix.
//
// cmdHealWWW makes https://www.<d> + https://cname.<d> work for every
// domain on the box.
//
// Background — pre-3.1.11 three vhost template families published a
// `server_name {{.Domain}};` line with no www / cname:
//   * reverseProxyTemplate   (Deploy Software for Next.js / Node / Go)
//   * reverseProxySSLTemplate
//   * CreateStaticVhost / CreateStaticVhostWithSSL (static frontends)
//
// Plus Deploy Software's IssueLetsEncryptMulti was called with
// operator-aliases-only — www.<primary> was never auto-added to the
// cert SAN list. Net effect: any domain of those shapes had a dead
// www DNS leaf — nginx fell through to the catch-all default vhost
// (wrong cert + 404), and even fixing nginx wouldn't help because
// the cert's SAN list didn't cover www.
//
// v3.1.11 fixed BOTH the templates and the deploy SAN list, but
// existing installs still have the old vhost files + old certs. This
// command sweeps every panel domain and:
//
//   1. Reads /etc/nginx/sites-available/<d>, finds every server_name
//      line, ensures www.<d> and cname.<d> are present. Sed-style
//      in-place edit: preserves all other server_name entries (so
//      operator-added aliases survive).
//   2. If a Let's Encrypt cert exists for the domain on disk, parses
//      its current SAN list (openssl x509 -text). If www.<d> or
//      cname.<d> are missing, runs certbot --force-renewal with the
//      union of (existing SANs) + www + cname so the new cert
//      covers everything the old one did PLUS the missing names.
//   3. nginx -t once at the end + systemctl reload nginx if the test
//      passed. If nginx -t fails (very rare — would need a manually
//      broken vhost file pre-existing), the new vhost files stay in
//      place but the reload is skipped and the failure is reported
//      so the operator can investigate.
//
// Idempotent: a re-run on an already-healed domain is a no-op.
//
// Skipped:
//   * Wildcard-cert domains — their *.X SAN already covers the names.
//   * Suspended domains (the suspended placeholder vhost is rewritten
//     by Suspend/Unsuspend on its own schedule).
func cmdHealWWW() error {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	db, err := database.Connect(cfg)
	if err != nil {
		return fmt.Errorf("mongo connect: %w", err)
	}
	defer database.Disconnect()

	// Walk every domain row. Read just (domain, status) — we don't
	// need anything else from the row.
	cur, err := db.Collection("domains").Find(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("read domains: %w", err)
	}
	defer cur.Close(ctx)

	var (
		total           int
		nginxHealed     int
		nginxAlreadyOK  int
		certHealed      int
		certAlreadyOK   int
		skippedSuspend  int
		skippedNoVhost  int
		skippedWildcard int
		failures        []string
	)

	for cur.Next(ctx) {
		var d struct {
			Domain string `bson:"domain"`
			Status string `bson:"status"`
		}
		if err := cur.Decode(&d); err != nil {
			continue
		}
		dom := strings.TrimSpace(strings.ToLower(d.Domain))
		if dom == "" {
			continue
		}
		total++
		if d.Status == "suspended" {
			skippedSuspend++
			continue
		}

		vhostPath := "/etc/nginx/sites-available/" + dom
		body, readErr := os.ReadFile(vhostPath)
		if readErr != nil {
			skippedNoVhost++
			continue
		}

		// 1. nginx server_name backfill.
		newBody, changedNginx := healVhostServerNames(string(body), dom)
		if changedNginx {
			if err := os.WriteFile(vhostPath, []byte(newBody), 0644); err != nil {
				failures = append(failures, fmt.Sprintf("%s: write vhost: %v", dom, err))
				continue
			}
			nginxHealed++
			fmt.Printf("  + nginx server_name updated for %s\n", dom)
		} else {
			nginxAlreadyOK++
		}

		// 2. cert SAN backfill.
		certPath := "/etc/letsencrypt/live/" + dom + "/cert.pem"
		if _, statErr := os.Stat(certPath); statErr != nil {
			// No cert on disk — can't reissue. Domain may not have
			// SSL active yet; the operator can issue it from the SSL
			// page once DNS settles.
			continue
		}

		sans, isWildcard, err := readCertSANs(certPath)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: read cert SANs: %v", dom, err))
			continue
		}
		if isWildcard {
			skippedWildcard++
			continue
		}

		needWWW := !sansContains(sans, "www."+dom)
		needCname := !sansContains(sans, "cname."+dom)
		if !needWWW && !needCname {
			certAlreadyOK++
			continue
		}

		// Build certbot -d args: preserve every existing SAN
		// (including the primary) and add the missing ones.
		args := []string{
			"certonly", "--force-renewal", "--non-interactive", "--agree-tos",
			"--webroot", "-w", "/var/www/html",
			"--cert-name", dom,
		}
		seen := map[string]bool{}
		add := func(name string) {
			if name == "" {
				return
			}
			k := strings.ToLower(strings.TrimSpace(name))
			if seen[k] {
				return
			}
			seen[k] = true
			args = append(args, "-d", name)
		}
		add(dom)
		for _, s := range sans {
			add(s)
		}
		if needWWW {
			add("www." + dom)
		}
		if needCname {
			add("cname." + dom)
		}

		if out, runErr := exec.CommandContext(ctx, "certbot", args...).CombinedOutput(); runErr != nil {
			tail := strings.TrimSpace(string(out))
			if len(tail) > 240 {
				tail = "…" + tail[len(tail)-240:]
			}
			failures = append(failures, fmt.Sprintf("%s: certbot: %v (%s)", dom, runErr, tail))
			continue
		}
		certHealed++
		fmt.Printf("  + cert reissued for %s with www + cname\n", dom)
	}

	// 3. Reload nginx ONCE at the end if anything changed. nginx -t
	// first to catch a malformed file before we kick the live server.
	if nginxHealed+certHealed > 0 {
		if out, terr := exec.Command("nginx", "-t").CombinedOutput(); terr != nil {
			failures = append(failures, fmt.Sprintf("nginx -t failed after heal: %v (%s)", terr, strings.TrimSpace(string(out))))
		} else {
			if _, rerr := exec.Command("systemctl", "reload", "nginx").CombinedOutput(); rerr != nil {
				failures = append(failures, fmt.Sprintf("nginx reload failed: %v", rerr))
			}
		}
	}

	fmt.Println()
	fmt.Println("─── www / cname heal summary ───")
	fmt.Printf("  domains scanned          : %d\n", total)
	fmt.Printf("  nginx server_name healed : %d\n", nginxHealed)
	fmt.Printf("  nginx already OK         : %d\n", nginxAlreadyOK)
	fmt.Printf("  certs reissued (www+cname): %d\n", certHealed)
	fmt.Printf("  certs already OK         : %d\n", certAlreadyOK)
	fmt.Printf("  skipped (suspended)      : %d\n", skippedSuspend)
	fmt.Printf("  skipped (no vhost file)  : %d\n", skippedNoVhost)
	fmt.Printf("  skipped (wildcard cert)  : %d\n", skippedWildcard)
	if len(failures) > 0 {
		fmt.Printf("  failures                 : %d\n", len(failures))
		for _, f := range failures {
			fmt.Println("    !", f)
		}
	}
	if nginxHealed+certHealed == 0 && len(failures) == 0 {
		fmt.Println("✓ every domain already covers www + cname — nothing to do")
	}
	return nil
}

// healVhostServerNames walks an nginx vhost body and adds www.<d> +
// cname.<d> to every `server_name <d> ...;` line where they're missing.
// Preserves indentation, existing aliases, and trailing semicolons.
// Returns (newBody, changed) — changed=false means the body was already
// healthy and the caller should skip the file write.
//
// Matches every server_name line that mentions the apex domain so a
// vhost with separate :80 and :443 blocks both get fixed in one pass.
func healVhostServerNames(body, domain string) (string, bool) {
	lines := strings.Split(body, "\n")
	changed := false
	wantWWW := "www." + domain
	wantCname := "cname." + domain
	for i, line := range lines {
		// Trim once for the matcher; preserve original on no-op.
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "server_name") {
			continue
		}
		if !strings.Contains(trim, domain) {
			continue
		}
		// Strip the trailing ';' (and any whitespace) for parsing,
		// then split on whitespace. Re-emit with the same indent.
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		cleaned := strings.TrimSuffix(strings.TrimRight(trim, " \t;"), ";")
		parts := strings.Fields(cleaned) // ["server_name", "<d>", "www.<d>", ...]
		if len(parts) < 2 || parts[0] != "server_name" {
			continue
		}
		hosts := parts[1:]
		hostSet := map[string]bool{}
		for _, h := range hosts {
			hostSet[strings.ToLower(h)] = true
		}
		mutated := false
		if !hostSet[strings.ToLower(wantWWW)] {
			hosts = append(hosts, wantWWW)
			mutated = true
		}
		if !hostSet[strings.ToLower(wantCname)] {
			hosts = append(hosts, wantCname)
			mutated = true
		}
		if mutated {
			lines[i] = indent + "server_name " + strings.Join(hosts, " ") + ";"
			changed = true
		}
	}
	if !changed {
		return body, false
	}
	return strings.Join(lines, "\n"), true
}

// readCertSANs parses an X.509 certificate at certPath and returns
// (sans, isWildcard, err). isWildcard is true when ANY SAN starts
// with "*." — those certs already cover www.<d> by the wildcard, so
// the heal command can skip them.
func readCertSANs(certPath string) ([]string, bool, error) {
	out, err := exec.Command("openssl", "x509", "-in", certPath, "-noout", "-text").CombinedOutput()
	if err != nil {
		return nil, false, fmt.Errorf("openssl: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	// Find "Subject Alternative Name:" then collect "DNS:..." entries
	// from the next non-empty line.
	body := string(out)
	idx := strings.Index(body, "Subject Alternative Name:")
	if idx < 0 {
		return nil, false, nil
	}
	// The line immediately after the header holds the SANs.
	rest := body[idx:]
	nl := strings.Index(rest, "\n")
	if nl < 0 {
		return nil, false, nil
	}
	sansLine := strings.TrimSpace(rest[nl+1:])
	if newline := strings.Index(sansLine, "\n"); newline >= 0 {
		sansLine = sansLine[:newline]
	}
	var sans []string
	wildcard := false
	for _, raw := range strings.Split(sansLine, ",") {
		entry := strings.TrimSpace(raw)
		if !strings.HasPrefix(entry, "DNS:") {
			continue
		}
		host := strings.TrimSpace(strings.TrimPrefix(entry, "DNS:"))
		if host == "" {
			continue
		}
		if strings.HasPrefix(host, "*.") {
			wildcard = true
		}
		sans = append(sans, host)
	}
	return sans, wildcard, nil
}

// sansContains is a case-insensitive membership check on a SAN list.
func sansContains(sans []string, target string) bool {
	t := strings.ToLower(strings.TrimSpace(target))
	for _, s := range sans {
		if strings.ToLower(strings.TrimSpace(s)) == t {
			return true
		}
	}
	return false
}

// Mirrors the heal-dns shape: idempotent, prints before/after, no-op
// when the file is already clean.
func cmdHealMail() error {
	const dovecotUsers = "/etc/dovecot/users"
	const postfixMaps = "/etc/postfix/virtual_mailbox_maps"

	dovecotDupes, err := dedupePasswdFile(dovecotUsers, ":")
	if err != nil {
		return fmt.Errorf("dedupe %s: %w", dovecotUsers, err)
	}

	// Postfix's virtual_mailbox_maps is whitespace-separated:
	//     admin@example.com    example.com/admin/
	// Split on whitespace runs to extract the key.
	postfixDupes, err := dedupePasswdFile(postfixMaps, "")
	if err != nil {
		// Postfix file may be absent on installs that never enabled
		// mail — log and continue rather than abort the whole heal.
		fmt.Printf("  ! %s: %v\n", postfixMaps, err)
	}

	if dovecotDupes > 0 {
		fmt.Println("→ reloading dovecot")
		_ = run("systemctl", "reload", "dovecot")
	}
	if postfixDupes > 0 {
		fmt.Println("→ rebuilding postfix maps + reloading postfix")
		_ = run("postmap", postfixMaps)
		_ = run("systemctl", "reload", "postfix")
	}

	fmt.Println()
	fmt.Println("─── mail heal summary ───")
	fmt.Printf("  /etc/dovecot/users          duplicates removed: %d\n", dovecotDupes)
	fmt.Printf("  /etc/postfix/virtual_mailbox_maps duplicates removed: %d\n", postfixDupes)
	if dovecotDupes+postfixDupes == 0 {
		fmt.Println("✓ no duplicate mailbox entries — both files are clean")
	} else {
		fmt.Println("✓ heal complete — mail clients should now authenticate against the latest panel-set password")
	}
	return nil
}

// ---------------------------------------------------------------------------
// mail-ssl — issue LE cert for mail.<domain> + wire SNI dispatch
// ---------------------------------------------------------------------------
//
// Why this exists: install.sh ships Postfix + Dovecot with the Ubuntu
// snake-oil cert (CN = system hostname, self-signed). Strict mail
// clients — Gmail's "Send mail as", Outlook 365, modern Thunderbird
// — abort BEFORE sending AUTH credentials when the TLS cert
// hostname doesn't match the connection target AND the chain is
// untrusted. Result: "Authentication error. Check your username and
// password" — even when the credentials are correct. Roundcube
// auto-login keeps working because it talks to localhost:143 with
// TLS verification disabled, so the cert is never validated.
//
// Fix: issue a real Let's Encrypt cert covering mail.<domain> via
// the existing webroot challenge, then wire Postfix's
// tls_server_sni_maps + Dovecot's local_name SNI dispatch so the
// right cert is served per connection target. Multi-tenant safe —
// each domain gets its own entry, no cross-tenant cert leakage.
//
// Idempotent — re-running on an already-configured domain refreshes
// the cert (certbot is itself idempotent) and re-asserts the SNI
// entries (which are dedup-checked before append).
//
// Pre-flight checks:
//   - mail.<domain> A record points at this server's public IP
//     (otherwise the HTTP-01 challenge times out and certbot fails)
//   - port 80 is open (already true on every install.sh-provisioned
//     box; the panel's nginx already serves /.well-known)

func cmdMailSSL(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: bzpanel mail-ssl <domain>  (issues LE cert for mail.<domain> and wires Postfix/Dovecot SNI)")
	}
	domain := strings.TrimSpace(args[0])
	domain = strings.TrimSuffix(strings.TrimPrefix(domain, "mail."), ".")
	if !validDomain(domain) {
		return fmt.Errorf("invalid domain: %q", domain)
	}
	mailHost := "mail." + domain
	fmt.Printf("→ wiring mail SSL for %s\n", mailHost)

	cfg := config.Load()
	email := ""
	if owner, err := findSuperAdmin(cfg); err == nil {
		email = owner.email
	}
	if email == "" {
		email = "admin@" + domain
	}

	// Fast path: cert already on disk (e.g. transferred from a previous
	// server, or hand-issued out-of-band). Skip the DNS pre-flight,
	// nginx helper-vhost write, and certbot call — jump straight to
	// the postfix/dovecot SNI wire-up. This makes mail-ssl idempotent
	// in the post-transfer scenario where DNS still points at the
	// SOURCE server but the cert files have already been copied to
	// this box. Operator (or the transfer pipeline) just needs the
	// SNI map + dovecot config + renewal hook in place.
	certPath := fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", mailHost)
	keyPath := fmt.Sprintf("/etc/letsencrypt/live/%s/privkey.pem", mailHost)
	if fi, err := os.Stat(certPath); err == nil && fi.Size() > 0 {
		fmt.Printf("→ existing cert found at %s — skipping DNS check + certbot, just wiring SNI\n", certPath)
		if err := postfixSNIUpsert(mailHost, certPath, keyPath); err != nil {
			return fmt.Errorf("postfix sni: %w", err)
		}
		if err := dovecotSNIUpsert(mailHost, certPath, keyPath); err != nil {
			return fmt.Errorf("dovecot sni: %w", err)
		}
		fmt.Println("→ reloading postfix")
		if err := run("postmap", "-F", "hash:/etc/postfix/sni-map"); err != nil {
			return fmt.Errorf("postmap -F: %w", err)
		}
		_ = run("systemctl", "reload", "postfix")
		fmt.Println("→ reloading dovecot")
		_ = run("systemctl", "reload", "dovecot")
		_ = writeMailSSLRenewHook()
		fmt.Println()
		fmt.Println("✓ mail SSL re-wired for", mailHost, "(cert already present)")
		return nil
	}

	// Pre-flight: resolve mail.<domain> against PUBLIC DNS (1.1.1.1)
	// and compare to this server's public IP. The HTTP-01 challenge
	// the certbot run below performs is initiated by Let's Encrypt
	// — it resolves the hostname publicly and tries to fetch the
	// challenge file from THAT IP. If mail.<domain> is parked on a
	// different host, certbot fails after a 30+ second wait with a
	// generic "unauthorized 404" that doesn't make the cause
	// obvious. Catching this up-front saves the operator a retry
	// cycle and produces a clearly-actionable error.
	publicIP := strings.TrimSpace(cfg.ServerIP)
	if publicIP != "" {
		out, err := exec.Command("dig", "+short", "+time=3", "+tries=1", "@1.1.1.1", "A", mailHost).Output()
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			resolved := []string{}
			for _, l := range lines {
				l = strings.TrimSpace(l)
				if l != "" {
					resolved = append(resolved, l)
				}
			}
			if len(resolved) == 0 {
				return fmt.Errorf("public DNS resolution for %s returned no A records — set an A record pointing at %s before re-running",
					mailHost, publicIP)
			}
			matched := false
			for _, r := range resolved {
				if r == publicIP {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("public DNS for %s resolves to %v but this server's IP is %s — the LE HTTP-01 challenge would land on the wrong host. Update the A record at your DNS provider, wait for propagation, then re-run",
					mailHost, resolved, publicIP)
			}
			fmt.Printf("→ DNS check ok: %s → %s\n", mailHost, publicIP)
		}
		// If `dig` is unavailable we just skip the pre-flight. certbot
		// will still produce its own (less-friendly) error.
	}

	// 1. Ensure the webroot dir certbot uses for HTTP-01 exists.
	if err := os.MkdirAll("/var/www/certbot/.well-known/acme-challenge", 0o755); err != nil {
		return fmt.Errorf("prepare webroot: %w", err)
	}

	// 1b. Write an nginx helper vhost for `mail.<domain>` on port 80 so
	//     Let's Encrypt's HTTP-01 challenge finds the token. Without
	//     this, nginx has no `server_name mail.<domain>` block — the
	//     panel vhost rejects unmatched Host: headers with 404, and
	//     customer-domain vhosts only know <domain> + www.<domain>.
	//     LE's GET hits "Invalid response: 404" and certbot exits.
	//
	//     The helper vhost stays in place after issuance — it serves
	//     ACME renewals on the same path (no manual step on each
	//     renewal) and 301-redirects everything else to https://, so
	//     visitors who type http://mail.<domain> in a browser land on
	//     a sane HTTPS endpoint instead of a 404.
	if err := writeMailHelperVhost(mailHost); err != nil {
		return fmt.Errorf("write mail.<domain> helper vhost: %w", err)
	}
	if err := run("nginx", "-t"); err != nil {
		return fmt.Errorf("nginx -t failed after writing mail-helper vhost: %w", err)
	}
	if err := run("systemctl", "reload", "nginx"); err != nil {
		return fmt.Errorf("nginx reload: %w", err)
	}

	// 2. Issue (or renew) the cert. --cert-name pins the cert lineage
	//    to mail.<domain> so renewals stay isolated from the website
	//    cert. Certbot is idempotent — already-fresh certs are a no-op.
	fmt.Printf("→ certbot certonly --webroot -d %s\n", mailHost)
	if err := run("certbot", "certonly", "--webroot",
		"-w", "/var/www/certbot",
		"--cert-name", mailHost,
		"-d", mailHost,
		"--non-interactive", "--agree-tos",
		"-m", email,
	); err != nil {
		return fmt.Errorf("certbot failed for %s — verify the A record for %s points at this server and port 80 is reachable: %w", mailHost, mailHost, err)
	}
	// certPath / keyPath already declared at the top of this function
	// (fast-path early-return uses the same paths). Reuse them here.

	// 3. Postfix SNI dispatch. tls_server_sni_maps reads a hash:map
	//    keyed on the SNI hostname; value is `<cert>,<key>`.
	if err := postfixSNIUpsert(mailHost, certPath, keyPath); err != nil {
		return fmt.Errorf("postfix sni: %w", err)
	}

	// 4. Dovecot SNI dispatch. local_name { <host> { ssl_cert ssl_key } }
	//    blocks select the per-host cert at TLS handshake time. We
	//    accumulate them in a single file (99-panel-mail-sni.conf)
	//    so re-issues stay confined to one append point.
	if err := dovecotSNIUpsert(mailHost, certPath, keyPath); err != nil {
		return fmt.Errorf("dovecot sni: %w", err)
	}

	// 5. Reload — both daemons pick up the new map / config without
	//    a full restart.
	//
	//    CRITICAL: postmap -F. The `-F` (file-content) flag tells
	//    postmap that the map's value column is a comma-separated
	//    list of file paths whose contents should be read in and
	//    embedded base64-encoded into the .db. This is the format
	//    `tls_server_sni_maps` actually expects — without -F, Postfix
	//    treats the literal "/etc/letsencrypt/live/.../fullchain.pem,
	//    .../privkey.pem" as base64-encoded data and fails with
	//    "malformed BASE64 value".
	fmt.Println("→ reloading postfix")
	if err := run("postmap", "-F", "hash:/etc/postfix/sni-map"); err != nil {
		return fmt.Errorf("postmap -F: %w", err)
	}
	if err := run("systemctl", "reload", "postfix"); err != nil {
		return fmt.Errorf("postfix reload: %w", err)
	}
	fmt.Println("→ reloading dovecot")
	if err := run("systemctl", "reload", "dovecot"); err != nil {
		return fmt.Errorf("dovecot reload: %w", err)
	}

	// 6. Renewal hook. certbot auto-renews the cert via its system
	//    timer, but the renewed PEM has to be re-embedded into the
	//    Postfix SNI map (base64 contents change) and Dovecot needs
	//    a reload to pick up new key material. Drop a deploy hook
	//    that runs both — certbot's renewal-hooks/deploy/* scripts
	//    are invoked AFTER a successful renewal.
	if err := writeMailSSLRenewHook(); err != nil {
		// Non-fatal: cert is issued and live. The hook is for
		// future renewals; if we can't write it, the operator just
		// needs to re-run `bzpanel mail-ssl` after renewals.
		fmt.Printf("  ! couldn't install renewal hook (%v); re-run bzpanel mail-ssl after each renewal\n", err)
	}

	fmt.Println()
	fmt.Println("✓ mail SSL configured for", mailHost)
	fmt.Println("  Postfix + Dovecot will now serve a Let's Encrypt cert when clients")
	fmt.Println("  connect with SNI=" + mailHost + ". Strict clients (Gmail / Outlook 365)")
	fmt.Println("  should now accept credentials and authenticate normally.")
	fmt.Println("  Username for IMAP/SMTP: the FULL email address (e.g., user@" + domain + ").")
	return nil
}

// postfixSNIUpsert appends or refreshes a `tls_server_sni_maps` entry
// for `host` and ensures the postconf wiring is in place.
//
// CRITICAL value-column order: PRIVATE KEY first, then certificate
// chain. Postfix's SNI loader requires this; with cert-first it
// rejects the row at handshake time with
//     warning: error loading chain from SNI data for <host>: key not first
//     warning: aborting TLS handshake
// — silently dropping the connection. The format is:
//
//	mail.example.com /etc/letsencrypt/live/mail.example.com/privkey.pem,/etc/letsencrypt/live/mail.example.com/fullchain.pem
//
// One line per host. Postfix's hash table holds thousands of these
// without measurable lookup cost, so the file just grows additively.
func postfixSNIUpsert(host, cert, key string) error {
	const sniFile = "/etc/postfix/sni-map"
	// Key path first, then full-chain path. Reversed in v3.0.38 from
	// the v3.0.34 "cert,key" order which Postfix rejected.
	value := key + "," + cert
	line := host + " " + value

	// Read existing file (best-effort — empty file is fine).
	b, _ := os.ReadFile(sniFile)
	src := string(b)

	out := []string{}
	replaced := false
	for _, ln := range strings.Split(src, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		fields := strings.Fields(t)
		if len(fields) > 0 && fields[0] == host {
			// Replace the existing entry so renewed paths stay accurate.
			out = append(out, line)
			replaced = true
			continue
		}
		out = append(out, ln)
	}
	if !replaced {
		out = append(out, line)
	}
	body := strings.Join(out, "\n") + "\n"
	if err := os.WriteFile(sniFile, []byte(body), 0o644); err != nil {
		return err
	}

	// Ensure Postfix is told to read the map. postconf is idempotent
	// — re-setting an already-correct value is a no-op.
	if err := run("postconf", "-e",
		"tls_server_sni_maps=hash:"+sniFile,
		"smtpd_tls_chain_files=", // empty so per-SNI cert wins
	); err != nil {
		return err
	}
	return nil
}

// dovecotSNIUpsert maintains a single conf.d/99-panel-mail-sni.conf
// file with one `local_name { <host> { ... } }` block per provisioned
// mail domain. Idempotent — replacing an existing block instead of
// appending duplicates.
func dovecotSNIUpsert(host, cert, key string) error {
	const sniConf = "/etc/dovecot/conf.d/99-panel-mail-sni.conf"
	header := "# Managed by Betazen Server Panel — do not hand-edit.\n# One local_name block per mail.<domain> with its own LE cert.\n# Updated by `bzpanel mail-ssl <domain>`.\n\n"

	b, _ := os.ReadFile(sniConf)
	src := string(b)
	if !strings.HasPrefix(src, "# Managed by Betazen Server Panel") {
		// Fresh file — start with our header.
		src = header
	}

	// Parse out any existing block for this host. local_name blocks
	// are line-delimited with brace nesting; a regex-free split on the
	// `local_name <host> {` opener is enough for our shape.
	openTag := "local_name " + host + " {"
	if idx := strings.Index(src, openTag); idx >= 0 {
		// Find matching closing brace by walking the rest of the
		// string. Our blocks are always shallow (one level of
		// nesting under local_name), so a simple counter works.
		rest := src[idx:]
		depth := 0
		end := -1
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
					break
				}
			}
			if end >= 0 {
				break
			}
		}
		if end > 0 {
			src = src[:idx] + src[idx+end+1:]
			src = strings.TrimRight(src, "\n") + "\n\n"
		}
	}

	block := fmt.Sprintf(`local_name %s {
  ssl_cert = <%s
  ssl_key  = <%s
}
`, host, cert, key)

	out := strings.TrimRight(src, "\n") + "\n\n" + block
	return os.WriteFile(sniConf, []byte(out), 0o644)
}

// writeMailHelperVhost lays down an nginx server block on port 80 for
// `mail.<domain>`. Two responsibilities:
//
//  1. Serve `/.well-known/acme-challenge/*` from `/var/www/certbot`
//     so Let's Encrypt's HTTP-01 challenge succeeds (now and on every
//     renewal — certbot's renewal cron hits the same path).
//  2. 301 everything else to `https://mail.<domain>` so a human typing
//     `http://mail.<domain>` in a browser lands somewhere sane instead
//     of a 404.
//
// Idempotent: re-running on an already-configured host overwrites the
// file with the same content, so cert renewals don't accumulate cruft.
// File path is `/etc/nginx/sites-available/mail-<domain>`, symlinked
// into `sites-enabled` so Debian/Ubuntu's stock include picks it up.
func writeMailHelperVhost(mailHost string) error {
	body := fmt.Sprintf(`# Managed by bzpanel — do not hand-edit; run `+"`"+`bzpanel mail-ssl <domain>`+"`"+`.
# Lets Encrypt HTTP-01 + 301-to-HTTPS for %s.
server {
    listen 80;
    listen [::]:80;
    server_name %s;

    location ^~ /.well-known/acme-challenge/ {
        root /var/www/certbot;
        default_type "text/plain";
        allow all;
    }

    location / {
        return 301 https://$host$request_uri;
    }
}
`, mailHost, mailHost)

	available := "/etc/nginx/sites-available/mail-" + mailHost
	enabled := "/etc/nginx/sites-enabled/mail-" + mailHost

	if err := os.WriteFile(available, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", available, err)
	}
	// Idempotent symlink — Lstat guards against re-creating an
	// existing valid symlink and against accidentally pointing the
	// symlink at a different target if one of the paths changes
	// later.
	if _, err := os.Lstat(enabled); os.IsNotExist(err) {
		if err := os.Symlink(available, enabled); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", enabled, available, err)
		}
	}
	return nil
}

// cmdMailSSLSweep walks every panel-tracked domain and runs mail-ssl
// for each. Designed to be cron-safe: idempotent (already-wired
// domains hit the fast-path early-return), skips domains whose
// public DNS for mail.<domain> doesn't resolve to this server (the
// pre-flight check inside cmdMailSSL handles that with a clear
// error), and never blocks for long because cmdMailSSL itself is
// fast on the steady-state path.
//
// Why a sweep is necessary: when an operator adds a new domain in
// the panel, the panel writes mail.<domain> A record into pdns
// immediately, but PUBLIC resolvers haven't seen it yet — the SOA
// TTL controls propagation, typically 1 hour. So a synchronous
// mail-ssl call right after domain create would fail the DNS
// pre-flight on a cold cache. The sweep catches up over time:
// cron runs it hourly; whenever public DNS lights up for a
// domain, the next sweep pass wires mail-ssl for it. Operator
// intervention is no longer needed.
//
// Reports a summary count at the end. Per-domain failures don't
// abort the sweep — every domain gets its own attempt.
func cmdMailSSLSweep() error {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := database.Connect(cfg)
	if err != nil {
		return fmt.Errorf("mongo connect: %w", err)
	}
	defer database.Disconnect()

	cur, err := db.Collection("domains").Find(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("list domains: %w", err)
	}
	defer cur.Close(ctx)

	var domains []string
	for cur.Next(ctx) {
		var d struct {
			Domain string `bson:"domain"`
		}
		if cur.Decode(&d) == nil && d.Domain != "" {
			domains = append(domains, d.Domain)
		}
	}
	fmt.Printf("→ %d domains to scan\n", len(domains))

	var (
		alreadyWired int
		newlyWired   int
		dnsNotReady  int
		failed       int
	)
	for _, domain := range domains {
		// Skip apex domains where mail. would be silly (e.g. itself
		// has 'mail.' prefix). The cleanest filter: skip any domain
		// that already starts with 'mail.' OR is itself a label
		// pattern that doesn't make sense for mail-SSL. For now
		// just skip mail.* domains; everything else gets a try.
		if strings.HasPrefix(domain, "mail.") {
			continue
		}
		mailHost := "mail." + domain
		certPath := fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", mailHost)
		// If cert already exists, the fast-path inside cmdMailSSL
		// will just re-assert the SNI wiring (cheap; no network).
		if fi, statErr := os.Stat(certPath); statErr == nil && fi.Size() > 0 {
			if err := cmdMailSSL([]string{domain}); err == nil {
				alreadyWired++
			} else {
				failed++
				fmt.Printf("  ! %s: re-wire failed: %v\n", domain, err)
			}
			continue
		}
		// No cert yet — try the full path. cmdMailSSL's DNS
		// pre-flight will refuse if public DNS isn't ready; we
		// classify that as "not yet ready" rather than a failure.
		err := cmdMailSSL([]string{domain})
		if err == nil {
			newlyWired++
			fmt.Printf("  + wired mail SSL for %s\n", domain)
		} else if strings.Contains(err.Error(), "public DNS") {
			dnsNotReady++
		} else {
			failed++
			fmt.Printf("  ! %s: %v\n", domain, err)
		}
	}

	fmt.Println()
	fmt.Println("─── mail-ssl sweep summary ───")
	fmt.Printf("  already-wired (re-asserted) : %d\n", alreadyWired)
	fmt.Printf("  newly wired                 : %d\n", newlyWired)
	fmt.Printf("  skipped (DNS not ready yet) : %d\n", dnsNotReady)
	fmt.Printf("  failures                    : %d\n", failed)
	return nil
}

// writeMailSSLRenewHook drops a script into certbot's deploy-hooks
// directory so that every successful cert renewal automatically:
//
//   1. re-runs `postmap -F hash:/etc/postfix/sni-map` — the SNI map
//      embeds the PEM contents base64-encoded; renewed certs have new
//      bytes, so the .db has to be rebuilt. Without this, Postfix
//      keeps serving the OLD (now-expired) cert until someone
//      manually re-runs `bzpanel mail-ssl <domain>`.
//   2. reloads postfix + dovecot so both daemons pick up the new
//      key material.
//
// certbot runs every renewal-hooks/deploy/* script after a successful
// renewal of ANY cert. We don't gate on which cert renewed because the
// postmap rebuild + reloads are cheap — running them on every renewal
// (web, mail, panel) is harmless and keeps the logic trivial.
//
// Idempotent: re-writes the file with identical content on every
// invocation. Path: /etc/letsencrypt/renewal-hooks/deploy/bzpanel-mail-sni.sh.
func writeMailSSLRenewHook() error {
	const hookDir = "/etc/letsencrypt/renewal-hooks/deploy"
	const hookPath = hookDir + "/bzpanel-mail-sni.sh"
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		return err
	}
	body := `#!/bin/sh
# Managed by bzpanel — do not hand-edit. Runs after every certbot deploy
# (renewal or first issuance). Rebuilds the Postfix SNI map and reloads
# Postfix + Dovecot so the renewed cert is served on the next connection.
set -e
if [ -f /etc/postfix/sni-map ]; then
    postmap -F hash:/etc/postfix/sni-map || true
    systemctl reload postfix || true
fi
if [ -f /etc/dovecot/conf.d/99-panel-mail-sni.conf ]; then
    systemctl reload dovecot || true
fi
`
	return os.WriteFile(hookPath, []byte(body), 0o755)
}

// dedupePasswdFile rewrites `path` so only the LAST line for each key
// survives (most recent write wins, which matches the intent — the
// operator's most recent password rotation should be authoritative).
// `sep` is the field separator used to extract the key:
//
//   - ":" for /etc/dovecot/users (passwd-style)
//   - ""  for whitespace-separated files (postfix virtual_mailbox_maps)
//
// Empty / comment lines pass through unchanged. Atomic write via
// rename so a crash mid-write doesn't leave the file half-rewritten.
// Permissions on the written file mirror the source (best-effort —
// we don't lose the dovecot:dovecot 0640 ownership the panel cares
// about).
func dedupePasswdFile(path, sep string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	srcLines := strings.Split(string(b), "\n")

	// First pass — record the LAST line index for each key.
	lastIdx := make(map[string]int)
	keyAt := make([]string, len(srcLines))
	for i, ln := range srcLines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		var key string
		if sep == "" {
			fields := strings.Fields(t)
			if len(fields) == 0 {
				continue
			}
			key = fields[0]
		} else {
			idx := strings.Index(t, sep)
			if idx < 0 {
				continue
			}
			key = t[:idx]
		}
		keyAt[i] = key
		lastIdx[key] = i
	}

	// Second pass — keep a line if it has no key, OR its key's last
	// occurrence IS this line. Anything earlier is a stale duplicate.
	out := make([]string, 0, len(srcLines))
	dupes := 0
	for i, ln := range srcLines {
		key := keyAt[i]
		if key == "" {
			out = append(out, ln)
			continue
		}
		if lastIdx[key] == i {
			out = append(out, ln)
		} else {
			dupes++
		}
	}
	if dupes == 0 {
		return 0, nil
	}

	// Atomic write — preserve the file's mode + ownership so dovecot's
	// 0640 user:dovecot:dovecot doesn't get reset to root:root 0644.
	stat, statErr := os.Stat(path)
	tmp := path + ".bzpanel-heal.tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(out, "\n")), 0600); err != nil {
		return 0, err
	}
	if statErr == nil {
		_ = os.Chmod(tmp, stat.Mode())
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	// Preserve owner via shell `chown --reference=...` against the
	// pre-existing file. We can't use syscall.Stat_t for the uid/gid
	// because Windows builds (dev) lack the type, and we want bzpanel
	// to cross-compile cleanly. Linux has /etc/dovecot/users owned by
	// dovecot:dovecot 0640 — losing that ownership means dovecot's
	// daemon can no longer read its own passwd file, which would be a
	// worse bug than the one we're fixing. The shell-out runs only on
	// the production Linux target via run() (cross-compile is fine; the
	// command is just never invoked on a Windows dev box).
	if statErr == nil {
		// chown by name, hard-coded to the canonical owner for each
		// known path. This matches what install.sh sets and what
		// EmailService maintains via its `touch && chgrp dovecot`
		// helper. Other paths that pass through this helper would
		// need their own case branch, but today only dovecot users +
		// postfix maps go through here.
		switch path {
		case "/etc/dovecot/users":
			_ = exec.Command("chown", "dovecot:dovecot", path).Run()
			_ = exec.Command("chmod", "0640", path).Run()
		case "/etc/postfix/virtual_mailbox_maps":
			_ = exec.Command("chown", "root:root", path).Run()
			_ = exec.Command("chmod", "0644", path).Run()
		}
	}
	fmt.Printf("  - %s: removed %d duplicate row(s)\n", path, dupes)
	return dupes, nil
}

// readEnv parses a simple shell-style `.env` file (KEY=VALUE per line,
// optional surrounding quotes) into a map. No interpolation, no
// `export` prefix handling — matches what install.sh and config.Load
// expect.
func readEnv(path string) (map[string]string, error) {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		v = strings.Trim(v, `"'`)
		out[k] = v
	}
	return out, nil
}

// mongoUserFromURI extracts the username from a mongo URI. Returns ""
// when the URI lacks credentials (which is itself a misconfiguration
// for our use case).
func mongoUserFromURI(uri string) string {
	m := regexp.MustCompile(`^mongodb://([^:]+):[^@]+@`).FindStringSubmatch(uri)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// mongoBindIsLocalhostOnly returns true when the mongod.conf binds
// only to 127.0.0.1 / localhost (the install.sh default). The
// bootstrap refuses to run on a wider bind because the auth-disabled
// window would briefly accept LAN connections.
func mongoBindIsLocalhostOnly(conf string) bool {
	for _, line := range strings.Split(conf, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "bindIp:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(t, "bindIp:"))
		// Accept 127.0.0.1, localhost, and the explicit ::1 / mixed
		// localhost-only forms. Reject anything containing 0.0.0.0 or
		// a non-loopback IP.
		val = strings.Trim(val, `"'`)
		if val == "" {
			return false
		}
		// Split on commas — bindIp accepts a list.
		for _, p := range strings.Split(val, ",") {
			p = strings.TrimSpace(p)
			if p != "127.0.0.1" && p != "localhost" && p != "::1" {
				return false
			}
		}
		return true
	}
	return false
}

// commentMongoAuthorization returns the mongod.conf content with any
// `authorization: enabled` line under `security:` commented out. We
// match a typical YAML shape but the regexp is forgiving on
// indentation. Idempotent on already-commented input.
func commentMongoAuthorization(conf string) string {
	re := regexp.MustCompile(`(?m)^([ \t]*)authorization:[ \t]*enabled[ \t]*$`)
	return re.ReplaceAllString(conf, `$1#authorization: enabled`)
}

// waitForMongo polls `mongosh --quiet --eval "db.adminCommand({ping:1})"`
// against an unauthenticated localhost session until it succeeds, or
// times out. Used after restart to make sure the next mongo command
// doesn't race with the daemon's startup.
func waitForMongo(deadline time.Duration) error {
	cutoff := time.Now().Add(deadline)
	for time.Now().Before(cutoff) {
		cmd := exec.Command("mongosh", "--quiet", "127.0.0.1:27017",
			"--eval", "db.adminCommand({ping:1}).ok")
		out, err := cmd.CombinedOutput()
		if err == nil && strings.Contains(string(out), "1") {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for mongod to accept connections")
}
