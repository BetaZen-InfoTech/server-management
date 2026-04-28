package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"go.mongodb.org/mongo-driver/mongo"
)

// pmaInstallLock serialises the install-phpMyAdmin probe across concurrent
// transfer jobs — running it once is enough; running it twice in parallel
// races on the apt lock and on /usr/share/phpmyadmin/.
var pmaInstallLock sync.Mutex

// ensurePhpMyAdminInstalled runs the same idempotent steps as install.sh's
// install_phpmyadmin() function but from inside the running panel binary, so
// transfers (and other panel ops that need /phpmyadmin/ to work on the
// destination) can self-heal a deploy that pre-dates the phpMyAdmin install.
//
// All work is best-effort and logged via the supplied logger; failure does
// NOT abort the caller. Steps:
//  1. If /usr/share/phpmyadmin already exists, exit fast (most common case).
//  2. Download phpMyAdmin tarball + extract to /usr/share/phpmyadmin.
//  3. Install PHP extensions, generate config.inc.php with fresh blowfish
//     secret, write the nginx snippet, patch the panel vhost.
//  4. nginx -t + systemctl reload nginx.
//
// Logger signature is (level, message) so it slots into the transfer
// service's addLog without the helper depending on the transfer model.
func ensurePhpMyAdminInstalled(_ *mongo.Database, _ string, log func(level, msg string)) {
	pmaInstallLock.Lock()
	defer pmaInstallLock.Unlock()

	ctx := context.Background()

	if _, err := os.Stat("/usr/share/phpmyadmin"); err == nil {
		// Already present — verify the nginx snippet exists too; if it
		// doesn't, we still need to write it. Otherwise we're done.
		if _, err := os.Stat("/etc/nginx/snippets/phpmyadmin.conf"); err == nil {
			return
		}
		log("info", "phpMyAdmin webroot found but nginx snippet missing — backfilling")
	} else {
		log("info", "phpMyAdmin not installed — installing 5.2.1 from upstream tarball")
		const ver = "5.2.1"
		dlCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		_, err := agent.RunCommand(dlCtx, "bash", "-c", fmt.Sprintf(
			"set -e; cd /tmp && wget -q https://files.phpmyadmin.net/phpMyAdmin/%[1]s/phpMyAdmin-%[1]s-all-languages.tar.gz && tar xzf phpMyAdmin-%[1]s-all-languages.tar.gz && mv phpMyAdmin-%[1]s-all-languages /usr/share/phpmyadmin && rm -f phpMyAdmin-%[1]s-all-languages.tar.gz && chown -R www-data:www-data /usr/share/phpmyadmin",
			ver))
		cancel()
		if err != nil {
			log("error", "phpMyAdmin tarball install failed: "+err.Error())
			return
		}
		log("info", "phpMyAdmin tarball extracted")
	}

	// PHP extensions — apt-get is a no-op when already installed.
	apCtx, apCancel := context.WithTimeout(ctx, 5*time.Minute)
	agent.RunCommand(apCtx, "apt-get", "install", "-yq", "php-mbstring", "php-mysql", "php-zip", "php-gd", "php-json", "php-xml")
	apCancel()

	// Runtime dirs.
	agent.RunCommand(ctx, "mkdir", "-p", "/var/lib/phpmyadmin/tmp", "/etc/phpmyadmin")
	agent.RunCommand(ctx, "chown", "-R", "www-data:www-data", "/var/lib/phpmyadmin")
	agent.RunCommand(ctx, "chmod", "770", "/var/lib/phpmyadmin/tmp")

	// signon-secret — HMAC key shared between the panel binary and
	// /usr/share/phpmyadmin/_signon.php. Pre-3.0.19 the self-heal
	// generated only the cookie-auth half of the install, so post-
	// transfer the WHM Database page's "Open in phpMyAdmin (auto-
	// login)" button fell back to the plain /phpmyadmin/ URL (no
	// auto-login) and the operator had to copy/paste credentials.
	// Now we generate (or preserve) the secret AND echo it into
	// /opt/serverpanel/.env so the running panel + the next restart
	// both pick it up.
	secretFile := "/etc/phpmyadmin/signon-secret"
	if _, err := os.Stat(secretFile); err != nil {
		secretBytes, _ := exec.Command("openssl", "rand", "-hex", "32").Output()
		secret := strings.TrimSpace(string(secretBytes))
		if secret == "" {
			secret = fmt.Sprintf("%x%x", time.Now().UnixNano(), os.Getpid())
		}
		if err := os.WriteFile(secretFile, []byte(secret+"\n"), 0640); err != nil {
			log("warn", "write signon-secret: "+err.Error())
		} else {
			agent.RunCommand(ctx, "chown", "root:www-data", secretFile)
			log("info", "Generated "+secretFile)
		}
	}
	// Append PMA_SIGNON_SECRET to /opt/serverpanel/.env if missing,
	// so the panel reads the correct value at next start. The running
	// process also re-reads this file on each GetPhpMyAdminInfo call
	// (see database_service.go) so a self-healed install works
	// without a restart.
	if envBytes, err := os.ReadFile("/opt/serverpanel/.env"); err == nil &&
		!strings.Contains(string(envBytes), "PMA_SIGNON_SECRET=") {
		if secret, err := os.ReadFile(secretFile); err == nil {
			line := "PMA_SIGNON_SECRET=" + strings.TrimSpace(string(secret)) + "\n"
			_ = os.WriteFile("/opt/serverpanel/.env", append(envBytes, []byte(line)...), 0640)
		}
	}

	// config.inc.php — only generate when missing so re-runs preserve
	// the existing blowfish secret (active sessions stay valid). The
	// generated config has TWO servers: $i=1 is cookie-auth (manual
	// login at /phpmyadmin/) and $i=2 is signon-auth fed by the
	// auto-login shim. Missing the second server would break the
	// signed URL even when the secret + shim are present.
	if _, err := os.Stat("/etc/phpmyadmin/config.inc.php"); err != nil {
		secretBytes, _ := exec.Command("openssl", "rand", "-hex", "16").Output()
		secret := strings.TrimSpace(string(secretBytes))
		if secret == "" {
			secret = fmt.Sprintf("%x", time.Now().UnixNano())
		}
		conf := `<?php
$cfg['blowfish_secret'] = '` + secret + `';
$cfg['UploadDir'] = '/var/lib/phpmyadmin/tmp';
$cfg['SaveDir'] = '/var/lib/phpmyadmin/tmp';
$cfg['TempDir'] = '/var/lib/phpmyadmin/tmp';
$cfg['ShowPhpInfo'] = false;
$cfg['ShowServerInfo'] = false;
$cfg['ShowChgPassword'] = false;

// Server 1 — cookie auth, used for direct manual logins at /phpmyadmin/.
$i = 1;
$cfg['Servers'][$i]['auth_type'] = 'cookie';
$cfg['Servers'][$i]['host'] = '127.0.0.1';
$cfg['Servers'][$i]['compress'] = false;
$cfg['Servers'][$i]['AllowNoPassword'] = false;
$cfg['Servers'][$i]['hide_db'] = '^(information_schema|performance_schema|mysql|sys|phpmyadmin)$';

// Server 2 — signon auth. _signon.php sets PMA_single_signon_user/pass
// in the named PHP session and redirects to /phpmyadmin/?server=2&db=...
$i = 2;
$cfg['Servers'][$i]['auth_type'] = 'signon';
$cfg['Servers'][$i]['host'] = '127.0.0.1';
$cfg['Servers'][$i]['compress'] = false;
$cfg['Servers'][$i]['SignonSession'] = 'panel_pma_signon';
$cfg['Servers'][$i]['SignonURL'] = '/phpmyadmin/_signon.php';
$cfg['Servers'][$i]['LogoutURL'] = '/phpmyadmin/?logout=1';
$cfg['Servers'][$i]['hide_db'] = '^(information_schema|performance_schema|mysql|sys|phpmyadmin)$';

$cfg['ServerDefault'] = 1;
`
		if err := os.WriteFile("/etc/phpmyadmin/config.inc.php", []byte(conf), 0644); err != nil {
			log("error", "write phpMyAdmin config: "+err.Error())
			return
		}
		log("info", "Generated /etc/phpmyadmin/config.inc.php with cookie+signon auth")
	}
	agent.RunCommand(ctx, "ln", "-sf", "/etc/phpmyadmin/config.inc.php", "/usr/share/phpmyadmin/config.inc.php")

	// _signon.php — the auto-login shim that verifies the panel's
	// HMAC token and primes the phpMyAdmin signon session. Always
	// overwrite (panel-owned, no operator-mutable data inside) so a
	// stale shim from an older install gets refreshed.
	signonPHP := `<?php
// Auto-login shim for the panel. The panel signs a short-lived token with
// the secret in /etc/phpmyadmin/signon-secret; we verify the HMAC, set the
// PMA signon session, and redirect to the requested database. Token is
// "<base64url(json{user,pass,db,exp})>.<hex hmac-sha256>".

$secretFile = '/etc/phpmyadmin/signon-secret';
if (!is_readable($secretFile)) { http_response_code(500); exit('signon disabled: secret file unreadable'); }
$secret = trim(file_get_contents($secretFile));
if ($secret === '') { http_response_code(500); exit('signon disabled: empty secret'); }

$tok = $_GET['t'] ?? '';
$parts = explode('.', $tok, 2);
if (count($parts) !== 2) { http_response_code(400); exit('bad token shape'); }
list($encPayload, $sig) = $parts;
$expected = hash_hmac('sha256', $encPayload, $secret);
if (!hash_equals($expected, $sig)) { http_response_code(403); exit('signature mismatch'); }
$json = base64_decode(strtr($encPayload, '-_', '+/') . str_repeat('=', (4 - strlen($encPayload) % 4) % 4));
$data = $json ? json_decode($json, true) : null;
if (!is_array($data)) { http_response_code(400); exit('bad payload'); }
if (!isset($data['user'], $data['pass'], $data['db'], $data['exp'])) { http_response_code(400); exit('missing fields'); }
if ((int)$data['exp'] < time()) { http_response_code(410); exit('token expired'); }

session_name('panel_pma_signon');
session_start();
$_SESSION['PMA_single_signon_user']     = $data['user'];
$_SESSION['PMA_single_signon_password'] = $data['pass'];
$_SESSION['PMA_single_signon_host']     = '127.0.0.1';
$_SESSION['PMA_single_signon_port']     = 3306;
session_write_close();

$db = rawurlencode($data['db']);
header('Location: /phpmyadmin/index.php?server=2&db=' . $db . '&route=/database/structure');
exit;
`
	if err := os.WriteFile("/usr/share/phpmyadmin/_signon.php", []byte(signonPHP), 0644); err != nil {
		log("warn", "write _signon.php: "+err.Error())
	} else {
		agent.RunCommand(ctx, "chown", "www-data:www-data", "/usr/share/phpmyadmin/_signon.php")
		log("info", "Wrote /usr/share/phpmyadmin/_signon.php (auto-login shim)")
	}

	// Detect PHP-FPM socket.
	sock := ""
	for _, candidate := range []string{
		"/run/php/php8.2-fpm.sock",
		"/run/php/php8.1-fpm.sock",
		"/run/php/php-fpm.sock",
	} {
		if _, err := os.Stat(candidate); err == nil {
			sock = candidate
			break
		}
	}
	if sock == "" {
		log("warn", "no PHP-FPM socket found at /run/php/ — phpMyAdmin nginx snippet not written")
		return
	}

	snippet := fmt.Sprintf(`# phpMyAdmin location — included from the panel vhost.
location ^~ /phpmyadmin/ {
    alias /usr/share/phpmyadmin/;
    index index.php;
    try_files $uri $uri/ =404;
    location ~ ^/phpmyadmin/(.+\.php)$ {
        alias /usr/share/phpmyadmin/$1;
        fastcgi_pass unix:%s;
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME /usr/share/phpmyadmin/$1;
        include fastcgi_params;
    }
    location ~* ^/phpmyadmin/(.+\.(jpg|jpeg|gif|css|png|js|ico|html|xml|txt|svg|woff|woff2))$ {
        alias /usr/share/phpmyadmin/$1;
    }
}
`, sock)
	agent.RunCommand(ctx, "mkdir", "-p", "/etc/nginx/snippets")
	if err := os.WriteFile("/etc/nginx/snippets/phpmyadmin.conf", []byte(snippet), 0644); err != nil {
		log("error", "write nginx snippet: "+err.Error())
		return
	}
	log("info", "Wrote /etc/nginx/snippets/phpmyadmin.conf (PHP-FPM: "+sock+")")

	// Patch the panel vhost to include the snippet — only if missing.
	vhost := "/etc/nginx/sites-enabled/serverpanel"
	if _, err := os.Stat(vhost); err != nil {
		vhost = "/etc/nginx/sites-available/serverpanel"
	}
	if _, err := os.Stat(vhost); err == nil {
		raw, _ := os.ReadFile(vhost)
		if !strings.Contains(string(raw), "snippets/phpmyadmin.conf") {
			// Insert "include snippets/phpmyadmin.conf;" before the FIRST
			// "location /" line in each server block. Match the OPENING
			// brace of the first catch-all and inject right before.
			lines := strings.Split(string(raw), "\n")
			out := make([]string, 0, len(lines)+1)
			added := false
			for _, ln := range lines {
				if !added {
					trimmed := strings.TrimSpace(ln)
					if strings.HasPrefix(trimmed, "location /") && strings.Contains(trimmed, "{") &&
						!strings.HasPrefix(trimmed, "location //") {
						// Detect the indentation and reuse it.
						indent := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
						out = append(out, indent+"include snippets/phpmyadmin.conf;")
						added = true
					}
				}
				out = append(out, ln)
			}
			if added {
				_ = os.WriteFile(vhost, []byte(strings.Join(out, "\n")), 0644)
				log("info", "Patched "+vhost+" to include phpmyadmin snippet")
			} else {
				log("warn", "could not find 'location /' in "+vhost+" — manual patch needed")
			}
		}
	}

	// nginx -t + reload — best-effort; agent.RunCommand returns the exit
	// status which we surface to the operator log.
	if _, err := agent.RunCommand(ctx, "nginx", "-t"); err != nil {
		log("error", "nginx config test failed after phpmyadmin install: "+err.Error())
		return
	}
	if _, err := agent.RunCommand(ctx, "systemctl", "reload", "nginx"); err != nil {
		log("error", "nginx reload failed: "+err.Error())
		return
	}
	log("info", "phpMyAdmin install complete and nginx reloaded — /phpmyadmin/ is live")
}
