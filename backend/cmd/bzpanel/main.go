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
	if !validEmail(newEmail) {
		return fmt.Errorf("invalid email: %q", newEmail)
	}

	cfg := config.Load()
	owner, err := findSuperAdmin(cfg)
	if err != nil {
		return err
	}
	if strings.EqualFold(owner.email, newEmail) {
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
	_, err = db.Collection(database.ColUsers).UpdateOne(ctx,
		bson.M{"_id": owner.id},
		bson.M{"$set": bson.M{
			"password":           hash,
			"refresh_token":      "",
			"refresh_expires_at": nil,
			"reset_token_hash":   "",
			"reset_expires_at":   nil,
			"failed_logins":      0,
			"locked_until":       nil,
			"updated_at":         time.Now(),
		}},
	)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	fmt.Printf("super admin password rotated for %s\n", owner.email)
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
		fmt.Println("  0) Exit")
		fmt.Println()
		choice := prompt("Select [0-7]: ")
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
		default:
			fmt.Printf("Unknown choice %q — please pick 0-7.\n", choice)
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
