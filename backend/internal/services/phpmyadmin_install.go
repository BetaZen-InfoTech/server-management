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

	// config.inc.php — only generate when missing so re-runs preserve the
	// existing blowfish secret (active sessions stay valid).
	if _, err := os.Stat("/etc/phpmyadmin/config.inc.php"); err != nil {
		secretBytes, _ := exec.Command("openssl", "rand", "-hex", "16").Output()
		secret := strings.TrimSpace(string(secretBytes))
		if secret == "" {
			secret = fmt.Sprintf("%x", time.Now().UnixNano())
		}
		conf := `<?php
$cfg['blowfish_secret'] = '` + secret + `';
$i = 0;
$i++;
$cfg['Servers'][$i]['auth_type'] = 'cookie';
$cfg['Servers'][$i]['host'] = '127.0.0.1';
$cfg['Servers'][$i]['compress'] = false;
$cfg['Servers'][$i]['AllowNoPassword'] = false;
$cfg['Servers'][$i]['hide_db'] = '^(information_schema|performance_schema|mysql|sys|phpmyadmin)$';
$cfg['UploadDir'] = '/var/lib/phpmyadmin/tmp';
$cfg['SaveDir'] = '/var/lib/phpmyadmin/tmp';
$cfg['TempDir'] = '/var/lib/phpmyadmin/tmp';
$cfg['ShowPhpInfo'] = false;
$cfg['ShowServerInfo'] = false;
$cfg['ShowChgPassword'] = false;
`
		if err := os.WriteFile("/etc/phpmyadmin/config.inc.php", []byte(conf), 0644); err != nil {
			log("error", "write phpMyAdmin config: "+err.Error())
			return
		}
		log("info", "Generated /etc/phpmyadmin/config.inc.php with fresh blowfish secret")
	}
	agent.RunCommand(ctx, "ln", "-sf", "/etc/phpmyadmin/config.inc.php", "/usr/share/phpmyadmin/config.inc.php")

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
