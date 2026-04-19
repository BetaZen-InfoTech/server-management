#!/usr/bin/env bash
# install-phpmyadmin.sh — one-shot installer that backports the phpMyAdmin
# setup from install.sh into an EXISTING ServerPanel deploy. Safe to re-run.
#
# Run on the panel host as root:
#   bash scripts/install-phpmyadmin.sh
#
# What it does:
#   1. Downloads phpMyAdmin 5.2.1 to /usr/share/phpmyadmin (skips if present)
#   2. Installs PHP extensions phpMyAdmin needs (mbstring, mysql, zip, gd, xml)
#   3. Writes /etc/phpmyadmin/config.inc.php with cookie auth + a fresh
#      blowfish secret (skips if config already exists)
#   4. Writes /etc/nginx/snippets/phpmyadmin.conf with the nginx location block
#   5. Adds `include /etc/nginx/snippets/phpmyadmin.conf;` to the panel vhost
#      at /etc/nginx/sites-available/serverpanel (and copies to sites-enabled
#      if needed)
#   6. nginx -t + systemctl reload nginx
#
# After this runs, https://<panel-domain>/phpmyadmin/ serves phpMyAdmin and
# the Databases page's "Open in phpMyAdmin" button auto-logs in via form POST.

set -e

if [ "$(id -u)" != "0" ]; then
    echo "Run as root (sudo bash $0)"
    exit 1
fi

PMA_VER=5.2.1

# 1. phpMyAdmin tarball
if [ ! -d /usr/share/phpmyadmin ]; then
    echo "==> Downloading phpMyAdmin ${PMA_VER}…"
    cd /tmp
    wget -q "https://files.phpmyadmin.net/phpMyAdmin/${PMA_VER}/phpMyAdmin-${PMA_VER}-all-languages.tar.gz"
    tar xzf "phpMyAdmin-${PMA_VER}-all-languages.tar.gz"
    mv "phpMyAdmin-${PMA_VER}-all-languages" /usr/share/phpmyadmin
    rm -f "phpMyAdmin-${PMA_VER}-all-languages.tar.gz"
    chown -R www-data:www-data /usr/share/phpmyadmin
    echo "    installed to /usr/share/phpmyadmin"
else
    echo "==> phpMyAdmin already at /usr/share/phpmyadmin (skipping download)"
fi

# 2. PHP extensions
echo "==> Installing PHP extensions (mbstring, mysql, zip, gd, xml)…"
apt-get install -yq php-mbstring php-mysql php-zip php-gd php-json php-xml >/dev/null 2>&1 || true

# 3. /etc/phpmyadmin/config.inc.php
mkdir -p /etc/phpmyadmin /var/lib/phpmyadmin/tmp
chown -R www-data:www-data /var/lib/phpmyadmin
chmod 770 /var/lib/phpmyadmin/tmp
if [ ! -f /etc/phpmyadmin/config.inc.php ]; then
    echo "==> Generating /etc/phpmyadmin/config.inc.php with fresh blowfish secret"
    SECRET=$(openssl rand -hex 16)
    cat > /etc/phpmyadmin/config.inc.php <<PMACONF
<?php
\$cfg['blowfish_secret'] = '${SECRET}';
\$i = 0;
\$i++;
\$cfg['Servers'][\$i]['auth_type'] = 'cookie';
\$cfg['Servers'][\$i]['host'] = '127.0.0.1';
\$cfg['Servers'][\$i]['compress'] = false;
\$cfg['Servers'][\$i]['AllowNoPassword'] = false;
\$cfg['Servers'][\$i]['hide_db'] = '^(information_schema|performance_schema|mysql|sys|phpmyadmin)\$';
\$cfg['UploadDir'] = '/var/lib/phpmyadmin/tmp';
\$cfg['SaveDir'] = '/var/lib/phpmyadmin/tmp';
\$cfg['TempDir'] = '/var/lib/phpmyadmin/tmp';
\$cfg['ShowPhpInfo'] = false;
\$cfg['ShowServerInfo'] = false;
\$cfg['ShowChgPassword'] = false;
PMACONF
    ln -sf /etc/phpmyadmin/config.inc.php /usr/share/phpmyadmin/config.inc.php
else
    echo "==> /etc/phpmyadmin/config.inc.php already exists (leaving as-is)"
fi

# 4. nginx snippet
PMA_FPM_SOCK=$(ls /run/php/php8.2-fpm.sock 2>/dev/null || ls /run/php/php8.1-fpm.sock 2>/dev/null || ls /run/php/php-fpm.sock 2>/dev/null | head -1)
if [ -z "$PMA_FPM_SOCK" ]; then
    echo "ERROR: no PHP-FPM socket found at /run/php/. Install php-fpm first."
    exit 1
fi
mkdir -p /etc/nginx/snippets
echo "==> Writing /etc/nginx/snippets/phpmyadmin.conf (PHP-FPM: ${PMA_FPM_SOCK})"
cat > /etc/nginx/snippets/phpmyadmin.conf <<NGCONF
# phpMyAdmin location — included from the panel vhost.
location ^~ /phpmyadmin/ {
    alias /usr/share/phpmyadmin/;
    index index.php;
    try_files \$uri \$uri/ =404;
    location ~ ^/phpmyadmin/(.+\.php)\$ {
        alias /usr/share/phpmyadmin/\$1;
        fastcgi_pass unix:${PMA_FPM_SOCK};
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME /usr/share/phpmyadmin/\$1;
        include fastcgi_params;
    }
    location ~* ^/phpmyadmin/(.+\.(jpg|jpeg|gif|css|png|js|ico|html|xml|txt|svg|woff|woff2))\$ {
        alias /usr/share/phpmyadmin/\$1;
    }
}
NGCONF

# 5. patch panel vhost to include the snippet
VHOST=/etc/nginx/sites-enabled/serverpanel
[ -f "$VHOST" ] || VHOST=/etc/nginx/sites-available/serverpanel
if [ ! -f "$VHOST" ]; then
    echo "ERROR: panel vhost not found at /etc/nginx/sites-{enabled,available}/serverpanel"
    exit 1
fi
if grep -q 'snippets/phpmyadmin.conf' "$VHOST"; then
    echo "==> $VHOST already includes phpmyadmin snippet"
else
    echo "==> Patching $VHOST to include phpmyadmin snippet (before catch-all 'location /')"
    awk 'BEGIN{added=0} /^[[:space:]]*location[[:space:]]+\/[[:space:]]*\{/ && !added{print "    include snippets/phpmyadmin.conf;"; added=1} {print}' "$VHOST" > "$VHOST.new"
    if grep -q 'snippets/phpmyadmin.conf' "$VHOST.new"; then
        mv "$VHOST.new" "$VHOST"
    else
        rm -f "$VHOST.new"
        echo "    WARN: couldn't find 'location /' in $VHOST — add 'include snippets/phpmyadmin.conf;' manually inside the server block."
    fi
fi

# 6. test + reload
echo "==> nginx -t"
nginx -t
echo "==> reloading nginx"
systemctl reload nginx
echo
echo "DONE. Open https://<panel-domain>/phpmyadmin/ to verify, or click 'Open in phpMyAdmin' on the Databases page for auto-login."
