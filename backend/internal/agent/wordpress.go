package agent

import (
	"context"
	"fmt"
	"strings"
)

// InstallWordPressOptions bundles the install knobs that grew past what a
// flat positional arg list could carry without becoming unreadable. Only
// User / Domain / DBName / DBUser / DBPass / DBHost / SiteURL / Title /
// AdminUser / AdminPass / AdminEmail are required; the rest are
// best-effort hints that map to wp-cli flags when set.
type InstallWordPressOptions struct {
	User       string
	Domain     string
	Path       string // subpath under public_html (e.g. "/blog"); "" = doc root
	DBName     string
	DBUser     string
	DBPass     string
	DBHost     string
	SiteURL    string
	Title      string
	AdminUser  string
	AdminPass  string
	AdminEmail string
	Version    string // wp-cli --version=… ; "" or "latest" → latest
	Locale     string // wp-cli --locale=… ; "" → en_US
}

// InstallWordPress drives a fresh WP-CLI install. The flow is:
//
//  1. Ensure the install directory exists and is owned by the domain
//     user (errors here are surfaced — the previous build swallowed them
//     and let downstream wp-cli calls fail with confusing "command
//     failed: exit status 1" messages).
//  2. Strip the placeholder index.html / index.htm that domain
//     creation drops at /home/<u>/domains/<d>/public_html. nginx and
//     Apache both rank index.html above index.php, so leaving it in
//     place caused the "WordPress installed but the site still shows
//     the placeholder" symptom — operators reported this as
//     "WordPress not installing properly".
//  3. wp core download — honours --version and --locale from the
//     install wizard. Pre-3.0.22 the frontend sent these fields but
//     the backend's request model didn't have them, so the operator's
//     choice was silently discarded.
//  4. wp config create — every dynamic value is properly POSIX-quoted
//     via shellSingleQuote. The 3.0.21 build single-quoted only
//     --dbpass, leaving everything else open to shell injection /
//     accidental breakage when a manual-mode db_user contained a
//     dot-aware character or when the operator-typed password had a
//     literal apostrophe.
//  5. wp core install — same quoting fix, plus --skip-email so the
//     install doesn't sit waiting for the local MTA to deliver the
//     "Welcome" mail (often 30–90s on a box without postfix tuning).
//     Operators were complaining the install hung.
//  6. EnsureWebPerms restores ownership across the tree.
func InstallWordPress(ctx context.Context, opts InstallWordPressOptions) error {
	if opts.User == "" || opts.Domain == "" {
		return fmt.Errorf("InstallWordPress: user and domain are required")
	}

	// Normalise the subpath to "/foo" or "" — caller may pass "/blog/" or
	// "blog" depending on whether they pre-cleaned it. Keeps the wpPath
	// computation predictable.
	subPath := strings.TrimSpace(opts.Path)
	subPath = strings.Trim(subPath, "/")
	if subPath != "" {
		subPath = "/" + subPath
	}
	wpPath := fmt.Sprintf("/home/%s/domains/%s/public_html%s", opts.User, opts.Domain, subPath)

	// 1. Make sure the install directory exists and is owned by the user.
	// Both errors are now propagated — a failed mkdir was the source of
	// most "wp core download failed" reports because every subsequent
	// step ran against a directory the user couldn't write to.
	if _, err := RunCommand(ctx, "mkdir", "-p", wpPath); err != nil {
		return fmt.Errorf("create install dir %q: %w", wpPath, err)
	}
	if _, err := RunCommand(ctx, "chown", "-R", opts.User+":"+opts.User, wpPath); err != nil {
		return fmt.Errorf("chown install dir %q: %w", wpPath, err)
	}

	// 2. Remove placeholder files that would shadow WordPress's
	// index.php in the web server's index priority. We only target the
	// well-known names that domain creation drops; arbitrary files the
	// operator put there stay untouched.
	for _, placeholder := range []string{"index.html", "index.htm", "default.html"} {
		// Best-effort: missing file is fine, errors are not fatal.
		_, _ = RunCommand(ctx, "rm", "-f", fmt.Sprintf("%s/%s", wpPath, placeholder))
	}

	// 3. wp core download. --force is on so a re-run after a half-
	// completed install (no wp-config yet, but partial wp-includes)
	// doesn't bail out with "WordPress files seem to already be present
	// here." --skip-content keeps the download lean by skipping
	// default themes/plugins; wp core install will pull the bundled
	// twentytwentyfour theme as needed.
	dl := strings.Builder{}
	dl.WriteString("wp core download --force")
	dl.WriteString(" --path=" + shellSingleQuote(wpPath))
	if v := strings.TrimSpace(opts.Version); v != "" && !strings.EqualFold(v, "latest") {
		dl.WriteString(" --version=" + shellSingleQuote(v))
	}
	if loc := strings.TrimSpace(opts.Locale); loc != "" {
		dl.WriteString(" --locale=" + shellSingleQuote(loc))
	}
	if res, err := RunCommandAsUser(ctx, opts.User, dl.String()); err != nil {
		return fmt.Errorf("wp core download failed: %s", trimErr(res, err))
	}

	// 4. wp config create. --skip-check avoids wp-cli probing the DB
	// before the user is granted; with auto/manual mode we just CREATE-d
	// the user a few lines up so the probe would succeed, but on
	// existing-mode the operator's user may need replication catch-up
	// or per-host grants we can't assume. Skipping the probe matches
	// every modern provisioner.
	cfg := strings.Builder{}
	cfg.WriteString("wp config create --skip-check")
	cfg.WriteString(" --path=" + shellSingleQuote(wpPath))
	cfg.WriteString(" --dbname=" + shellSingleQuote(opts.DBName))
	cfg.WriteString(" --dbuser=" + shellSingleQuote(opts.DBUser))
	cfg.WriteString(" --dbpass=" + shellSingleQuote(opts.DBPass))
	cfg.WriteString(" --dbhost=" + shellSingleQuote(opts.DBHost))
	if res, err := RunCommandAsUser(ctx, opts.User, cfg.String()); err != nil {
		return fmt.Errorf("wp config create failed: %s", trimErr(res, err))
	}

	// 5. wp core install. --skip-email keeps the request from
	// blocking on the local MTA — every panel install was waiting up
	// to two minutes for the "Welcome to WordPress" mail to either
	// deliver or time out. The admin can change their email later if
	// they need verification mails.
	in := strings.Builder{}
	in.WriteString("wp core install --skip-email")
	in.WriteString(" --path=" + shellSingleQuote(wpPath))
	in.WriteString(" --url=" + shellSingleQuote(opts.SiteURL))
	in.WriteString(" --title=" + shellSingleQuote(opts.Title))
	in.WriteString(" --admin_user=" + shellSingleQuote(opts.AdminUser))
	in.WriteString(" --admin_password=" + shellSingleQuote(opts.AdminPass))
	in.WriteString(" --admin_email=" + shellSingleQuote(opts.AdminEmail))
	if res, err := RunCommandAsUser(ctx, opts.User, in.String()); err != nil {
		return fmt.Errorf("wp core install failed: %s", trimErr(res, err))
	}

	// 6. Re-normalise ownership / perms so nginx (www-data) can serve
	// every file we just created.
	return EnsureWebPerms(ctx, opts.User, opts.Domain)
}

// trimErr produces a compact one-line error string from a CommandResult
// and the wrapping error. Keeps the operator-facing message useful
// without dragging the whole 4 KB stderr into a toast.
func trimErr(res *CommandResult, err error) string {
	if res != nil {
		stderr := strings.TrimSpace(res.Error)
		if stderr != "" {
			// wp-cli typically prints "Error: <message>" — surface that.
			for _, line := range strings.Split(stderr, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "Error:") {
					return strings.TrimSpace(strings.TrimPrefix(line, "Error:"))
				}
			}
			// Fall back to the last non-empty stderr line.
			lines := strings.Split(stderr, "\n")
			for i := len(lines) - 1; i >= 0; i-- {
				if s := strings.TrimSpace(lines[i]); s != "" {
					return s
				}
			}
		}
	}
	if err != nil {
		return err.Error()
	}
	return "unknown error"
}

func WPCLICommand(ctx context.Context, user, wpPath, command string) (string, error) {
	full := fmt.Sprintf("wp %s --path=%s", command, shellSingleQuote(wpPath))
	result, err := RunCommandAsUser(ctx, user, full)
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

func WPSecurityScan(ctx context.Context, user, wpPath string) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	if out, err := WPCLICommand(ctx, user, wpPath, "core verify-checksums"); err == nil {
		result["core_integrity"] = out
	}
	if out, err := WPCLICommand(ctx, user, wpPath, "plugin list --update=available --format=json"); err == nil {
		result["outdated_plugins"] = out
	}
	return result, nil
}
