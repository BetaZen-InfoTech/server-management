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
		fmt.Println("  0) Exit")
		fmt.Println()
		choice := prompt("Select [0-9]: ")
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
		default:
			fmt.Printf("Unknown choice %q — please pick 0-9.\n", choice)
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
