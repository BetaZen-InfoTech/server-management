#!/usr/bin/env bash
#
# reconcile-email.sh — one-shot heal for the panel's mail stack.
#
# Problem it solves:
#   The earlier installer used fragile `sed` patches against stock
#   Ubuntu 24.04 Dovecot + Postfix config files. When those files
#   didn't match the exact byte sequences the patches looked for, the
#   patches silently no-opped:
#     * /var/spool/postfix/private/auth never got created → Roundcube
#       Compose toasts "SMTP Error (): Authentication failed".
#     * virtual_mailbox_{domains,maps} weren't the file names the panel
#       was writing to → no inbound mail ever accepted.
#     * Roundcube config used plain `localhost:587`, but Postfix sets
#       smtpd_tls_auth_only=yes → AUTH refused before creds are checked.
#
# This script writes deterministic override files, re-points Postfix
# at the correct virtual_mailbox_* paths, rewrites the Roundcube SMTP
# config to use tls://, restarts every service, and prints a short
# verification report so you can see what actually happened.
#
# Idempotent — safe to run repeatedly. Run via:
#   curl -sSL https://raw.githubusercontent.com/BetaZen-InfoTech/server-management/main/scripts/reconcile-email.sh | bash
# or, if your VPS clones the panel source:
#   bash /opt/serverpanel/scripts/reconcile-email.sh

set -u

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

say()  { echo -e "${CYAN}==>${NC} $1"; }
ok()   { echo -e "  ${GREEN}✓${NC} $1"; }
warn() { echo -e "  ${YELLOW}!${NC} $1"; }
fail() { echo -e "  ${RED}✗${NC} $1"; }

if [ "$(id -u)" -ne 0 ]; then
    fail "must be run as root"
    exit 1
fi

# --------------------------------------------------------------------
# 1. Dovecot override file
# --------------------------------------------------------------------
say "Writing /etc/dovecot/conf.d/99-panel.conf"
cat > /etc/dovecot/conf.d/99-panel.conf <<'DOVE99'
# Managed by Betazen Server Panel reconcile-email.sh — do not hand-edit.
# Lexical order: 99- wins against any stock 10-*.conf setting.

protocols = imap pop3 lmtp

passdb {
  driver = passwd-file
  args = scheme=SHA512-CRYPT username_format=%u /etc/dovecot/users
}
userdb {
  driver = passwd-file
  args = username_format=%u /etc/dovecot/users
  default_fields = uid=5000 gid=5000 home=/var/mail/vhosts/%d/%n
}

mail_location = maildir:~/Maildir
mail_privileged_group = vmail

# Allow PLAIN/LOGIN over TLS. Postfix's smtpd_tls_auth_only keeps this
# off the plaintext wire.
disable_plaintext_auth = no
auth_mechanisms = plain login

service auth {
  # The socket Postfix smtpd reads for SMTP-AUTH. Absence of this file
  # is what produced the "SMTP Error (): Authentication failed" bug.
  unix_listener /var/spool/postfix/private/auth {
    mode = 0660
    user = postfix
    group = postfix
  }
}

service lmtp {
  # Postfix delivers virtual mail via LMTP over this socket.
  unix_listener /var/spool/postfix/private/dovecot-lmtp {
    mode = 0600
    user = postfix
    group = postfix
  }
}
DOVE99
ok "wrote 99-panel.conf"

# Disable the PAM include so system users can't auth into Dovecot.
if [ -f /etc/dovecot/conf.d/10-auth.conf ]; then
    sed -i 's|^!include auth-system.conf.ext|#!include auth-system.conf.ext|' \
        /etc/dovecot/conf.d/10-auth.conf || true
    ok "disabled PAM (auth-system.conf.ext) include"
fi

# Ensure the panel's users file exists with the right ownership.
touch /etc/dovecot/users
chgrp dovecot /etc/dovecot/users 2>/dev/null || true
chmod 0640 /etc/dovecot/users
ok "normalised /etc/dovecot/users perms"

# --------------------------------------------------------------------
# 2. Postfix directives
# --------------------------------------------------------------------
say "Setting Postfix directives via postconf -e"
for d in \
    "smtpd_sasl_type=dovecot" \
    "smtpd_sasl_path=private/auth" \
    "smtpd_sasl_auth_enable=yes" \
    "smtpd_sasl_security_options=noanonymous" \
    "broken_sasl_auth_clients=yes" \
    "inet_protocols=ipv4" \
    "virtual_mailbox_domains=hash:/etc/postfix/virtual_mailbox_domains" \
    "virtual_mailbox_maps=hash:/etc/postfix/virtual_mailbox_maps" \
    "virtual_transport=lmtp:unix:private/dovecot-lmtp"; do
    postconf -e "$d" && ok "$d" || fail "postconf -e $d"
done

# Make sure the referenced map files exist so postmap doesn't error.
touch /etc/postfix/virtual_mailbox_domains /etc/postfix/virtual_mailbox_maps
postmap /etc/postfix/virtual_mailbox_domains 2>/dev/null || true
postmap /etc/postfix/virtual_mailbox_maps 2>/dev/null || true
ok "postmap virtual_mailbox_{domains,maps}"

# --------------------------------------------------------------------
# 2b. Postfix chroot resolver files
# --------------------------------------------------------------------
# Postfix smtp(8) runs chroot=y, so it reads /var/spool/postfix/etc/
# not /etc/. When the host's resolv.conf is a systemd-resolved stub
# pointing at 127.0.0.53, that address isn't reachable from the chroot
# — DNS lookups for gmail.com / yahoo.com / etc. fail with "Name
# service error, Host not found, try again" and every outbound message
# gets stuck in the queue. Syncing the resolver files is idempotent.
say "Syncing postfix chroot /etc/ (resolv.conf, hosts, nsswitch, services)"
install -d -m 0755 /var/spool/postfix/etc
cp -fL /etc/resolv.conf    /var/spool/postfix/etc/resolv.conf   2>/dev/null
cp -fL /etc/hosts          /var/spool/postfix/etc/hosts         2>/dev/null
cp -fL /etc/nsswitch.conf  /var/spool/postfix/etc/nsswitch.conf 2>/dev/null
cp -fL /etc/host.conf      /var/spool/postfix/etc/host.conf     2>/dev/null || true
cp -fL /etc/services       /var/spool/postfix/etc/services      2>/dev/null
# 127.0.0.53 isn't reachable from inside the chroot — rewrite.
if grep -q '127.0.0.53' /var/spool/postfix/etc/resolv.conf 2>/dev/null; then
    cat > /var/spool/postfix/etc/resolv.conf <<'RESOLVEOF'
nameserver 8.8.8.8
nameserver 1.1.1.1
nameserver 8.8.4.4
options timeout:3 attempts:2
RESOLVEOF
    ok "rewrote 127.0.0.53 stub to 8.8.8.8/1.1.1.1/8.8.4.4"
else
    ok "chroot resolv.conf uses upstream resolvers"
fi
chmod 0644 /var/spool/postfix/etc/*.conf /var/spool/postfix/etc/services /var/spool/postfix/etc/hosts 2>/dev/null || true

# Install systemd path watcher that re-syncs on /etc/resolv.conf changes
# (netplan apply, DHCP lease renewal). Without this, a resolver change
# silently re-breaks the chroot.
cat > /etc/systemd/system/postfix-chroot-sync.service <<'SVCEOF'
[Unit]
Description=Sync /etc resolver files into postfix chroot
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/bin/install -d -m 0755 /var/spool/postfix/etc
ExecStart=/bin/cp -fL /etc/resolv.conf /var/spool/postfix/etc/resolv.conf
ExecStart=/bin/cp -fL /etc/hosts /var/spool/postfix/etc/hosts
ExecStart=/bin/cp -fL /etc/nsswitch.conf /var/spool/postfix/etc/nsswitch.conf
SVCEOF
cat > /etc/systemd/system/postfix-chroot-sync.path <<'PATHEOF'
[Unit]
Description=Watch /etc/resolv.conf for changes and resync postfix chroot

[Path]
PathChanged=/etc/resolv.conf
Unit=postfix-chroot-sync.service

[Install]
WantedBy=multi-user.target
PATHEOF
systemctl daemon-reload 2>/dev/null
systemctl enable --now postfix-chroot-sync.path 2>/dev/null \
    && ok "postfix-chroot-sync.path enabled (auto-resyncs on resolver change)" \
    || warn "could not enable postfix-chroot-sync.path"

# --------------------------------------------------------------------
# 3. Roundcube SMTP config — force STARTTLS
# --------------------------------------------------------------------
say "Patching /etc/roundcube/config.inc.php"
RC=/etc/roundcube/config.inc.php
if [ ! -f "$RC" ]; then
    warn "$RC missing — skipping (Roundcube may not be installed)"
else
    python3 - <<PYEOF
import re
p = "/etc/roundcube/config.inc.php"
with open(p) as f: src = f.read()
orig = src
# Rewrite any existing smtp_host assignment to the tls:// form.
src = re.sub(
    r"\$config\[\s*['\"]smtp_host['\"]\s*\]\s*=\s*['\"][^'\"]*['\"]\s*;",
    "\$config['smtp_host'] = 'tls://localhost:587';",
    src,
)
if "smtp_conn_options" not in src:
    src = src.rstrip() + """

// Managed by Betazen Server Panel reconcile — snake-oil cert on localhost is OK.
\$config['smtp_conn_options'] = [
    'ssl' => [
        'verify_peer'       => false,
        'verify_peer_name'  => false,
        'allow_self_signed' => true,
    ],
];
"""
if src != orig:
    with open(p, "w") as f: f.write(src)
    print("  roundcube: updated")
else:
    print("  roundcube: already correct")
PYEOF
    ok "Roundcube SMTP config patched"
fi

# --------------------------------------------------------------------
# 4. Restart services
# --------------------------------------------------------------------
say "Restarting dovecot, postfix, php-fpm"
systemctl restart dovecot && ok "dovecot restarted" || fail "dovecot failed to restart"
systemctl restart postfix && ok "postfix restarted" || fail "postfix failed to restart"
# PHP-FPM restart clears Roundcube's opcache so the new config.inc.php
# is actually loaded on the next request.
systemctl reload php8.2-fpm 2>/dev/null && ok "php8.2-fpm reloaded" \
    || systemctl reload php-fpm 2>/dev/null && ok "php-fpm reloaded" \
    || warn "could not reload php-fpm (skipping)"

# --------------------------------------------------------------------
# 5. Verify
# --------------------------------------------------------------------
say "Verification"
if [ -S /var/spool/postfix/private/auth ]; then
    ok "SASL socket present: /var/spool/postfix/private/auth"
    ls -l /var/spool/postfix/private/auth 2>&1 | sed 's/^/     /'
else
    fail "SASL socket MISSING — dovecot isn't exposing it."
    fail "  Check: journalctl -u dovecot -n 50"
fi

if [ -S /var/spool/postfix/private/dovecot-lmtp ]; then
    ok "LMTP socket present: /var/spool/postfix/private/dovecot-lmtp"
else
    warn "LMTP socket missing — inbound delivery will defer."
fi

# Confirm main.cf picked up the virtual_mailbox_* directives.
VMD=$(postconf -h virtual_mailbox_domains 2>/dev/null)
VMM=$(postconf -h virtual_mailbox_maps 2>/dev/null)
if [[ "$VMD" == hash:/etc/postfix/virtual_mailbox_domains ]]; then
    ok "virtual_mailbox_domains = $VMD"
else
    fail "virtual_mailbox_domains mismatch: $VMD"
fi
if [[ "$VMM" == hash:/etc/postfix/virtual_mailbox_maps ]]; then
    ok "virtual_mailbox_maps = $VMM"
else
    fail "virtual_mailbox_maps mismatch: $VMM"
fi

# Confirm inet_protocols=ipv4 (pinned so Gmail 5.7.25 "no PTR" bounces
# on IPv6 can't sneak back in on a host that happens to have v6
# connectivity without matching reverse DNS).
IP_PROTO=$(postconf -h inet_protocols 2>/dev/null)
if [[ "$IP_PROTO" == "ipv4" ]]; then
    ok "inet_protocols = ipv4"
else
    fail "inet_protocols = $IP_PROTO (should be ipv4 — expect Gmail 5.7.25 bounces on IPv6)"
fi

# Confirm chroot resolv.conf is reachable and NOT the systemd stub.
if [ -f /var/spool/postfix/etc/resolv.conf ]; then
    if grep -q '127.0.0.53' /var/spool/postfix/etc/resolv.conf; then
        fail "chroot resolv.conf still points at 127.0.0.53 (unreachable from chroot → outbound DNS fails)"
    else
        ok "chroot resolv.conf uses reachable resolvers"
    fi
else
    fail "chroot resolv.conf MISSING — outbound mail will defer with 'Host not found'"
fi

# Is the Roundcube SMTP host using tls://?
if grep -q "smtp_host.*tls://localhost:587" /etc/roundcube/config.inc.php 2>/dev/null; then
    ok "Roundcube smtp_host uses tls://"
else
    warn "Roundcube smtp_host still plaintext — check $RC"
fi

echo ""
say "Done. Try Compose → Send in Roundcube now."
echo "  If it still fails, run:  tail -n 50 /var/log/mail.log"
echo "  and paste the AUTH-related lines back."
