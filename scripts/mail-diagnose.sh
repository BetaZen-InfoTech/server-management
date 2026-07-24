#!/usr/bin/env bash
#
# mail-diagnose.sh — exhaustive read-only inspection of the mail stack.
#
# Run this on the VPS after a failed send-and-receive test. The output
# names every common cause of "mail sends but Gmail doesn't receive"
# and "external senders can't reach this mailbox" so we can identify
# the right fix in a single round trip.
#
#   ssh root@<vps> 'curl -sSL https://raw.githubusercontent.com/BetaZen-InfoTech/server-management/main/scripts/mail-diagnose.sh | bash'
#
# Read-only. Does not modify any config, restart any service, or send
# any mail. Safe to run on a production box at any time.

set -u

# Accept a domain argument so checks like `dig MX` have a target. If
# absent we pick the first domain out of virtual_mailbox_domains and
# use it for every domain-scoped check.
DOMAIN="${1:-}"
if [ -z "$DOMAIN" ] && [ -f /etc/postfix/virtual_mailbox_domains ]; then
    DOMAIN=$(awk 'NF && !/^#/ {print $1; exit}' /etc/postfix/virtual_mailbox_domains || true)
fi

hdr() { echo; echo "================================================================"; echo "# $1"; echo "================================================================"; }

hdr "1. Service status"
systemctl is-active postfix dovecot opendkim 2>&1 | paste <(echo postfix; echo dovecot; echo opendkim) -

hdr "2. SASL + LMTP sockets (required for Roundcube send + Postfix delivery)"
ls -l /var/spool/postfix/private/auth 2>&1
ls -l /var/spool/postfix/private/dovecot-lmtp 2>&1

hdr "3. Postfix key directives"
for d in myhostname mydomain myorigin mydestination inet_interfaces inet_protocols \
         virtual_mailbox_domains virtual_mailbox_maps virtual_transport \
         smtpd_sasl_type smtpd_sasl_path smtpd_sasl_auth_enable smtpd_tls_auth_only \
         smtp_tls_security_level smtpd_tls_security_level relayhost; do
    printf "%-28s = %s\n" "$d" "$(postconf -h "$d" 2>/dev/null)"
done

hdr "4. Virtual maps content"
echo "--- virtual_mailbox_domains ---"
cat /etc/postfix/virtual_mailbox_domains 2>/dev/null || echo "(missing)"
echo "--- virtual_mailbox_maps ---"
cat /etc/postfix/virtual_mailbox_maps 2>/dev/null || echo "(missing)"

hdr "5. Dovecot users file"
ls -l /etc/dovecot/users 2>&1
# Just show the user names, never the password hashes, to keep this
# output pasteable in a chat.
awk -F: '{print $1}' /etc/dovecot/users 2>/dev/null | head -20

hdr "6. Dovecot listeners"
ss -xl 2>/dev/null | grep -E 'dovecot|private/(auth|dovecot-lmtp)' | head -10
echo '---'
ss -tlnp 2>/dev/null | awk 'NR==1 || /:(25|465|587|143|993|995)/ {print}'

hdr "7. Outbound port 25 egress (many VPS hosts block this by default)"
for host in gmail-smtp-in.l.google.com mx.zoho.com; do
    echo -n "$host:25 — "
    if timeout 5 bash -c "exec 3<>/dev/tcp/$host/25" 2>/dev/null; then
        echo OK
    else
        echo "BLOCKED or unreachable  (this alone explains 'Gmail doesn't receive')"
    fi
done

hdr "8. Inbound port 25 reachable from this host"
ss -tlnp 2>/dev/null | awk '$4 ~ /:25$/ {print}'
ufw status 2>/dev/null | head -20

hdr "9. Reverse DNS on our outbound IP"
OUR_IP=$(curl -s --max-time 5 https://api.ipify.org || hostname -I | awk '{print $1}')
echo "Outbound IP: $OUR_IP"
echo "PTR: $(dig +short -x "$OUR_IP" 2>/dev/null || echo '(none — Gmail rejects 550 5.7.1 without PTR)')"

hdr "9b. Postfix chroot resolver (Postfix smtp(8) runs chroot=y and reads THIS, not /etc/resolv.conf)"
if [ -f /var/spool/postfix/etc/resolv.conf ]; then
    echo "--- /var/spool/postfix/etc/resolv.conf ---"
    cat /var/spool/postfix/etc/resolv.conf
    if grep -q '127.0.0.53' /var/spool/postfix/etc/resolv.conf 2>/dev/null; then
        echo "  ⚠ chroot resolv.conf points at 127.0.0.53 — that address isn't reachable from"
        echo "    inside /var/spool/postfix, so external DNS lookups (gmail.com MX etc.) will"
        echo "    fail and every outbound message will defer. Run reconcile-email.sh to fix."
    fi
else
    echo "(missing) — outbound DNS will fail: 'Name service error for name=<host> type=MX'"
    echo "  Run:  bash /opt/serverpanel/scripts/reconcile-email.sh"
fi
echo "--- dig from INSIDE the chroot view ---"
# Can postfix's resolver reach its configured nameservers?
if [ -f /var/spool/postfix/etc/resolv.conf ]; then
    FIRST_NS=$(awk '/^nameserver/ {print $2; exit}' /var/spool/postfix/etc/resolv.conf)
    if [ -n "$FIRST_NS" ]; then
        echo -n "  dig @$FIRST_NS MX gmail.com → "
        dig +short +timeout=3 @"$FIRST_NS" MX gmail.com 2>/dev/null | head -1 || echo "(fail)"
    fi
fi

if [ -n "$DOMAIN" ]; then
    hdr "10. DNS for $DOMAIN — public authority"
    echo "--- NS @ authority ---"
    dig +short NS "$DOMAIN" 2>/dev/null | head -10
    echo "--- MX @ authority ---"
    dig +short MX "$DOMAIN" 2>/dev/null | head -10
    echo "--- A for mail.$DOMAIN @ authority ---"
    dig +short A "mail.$DOMAIN" 2>/dev/null | head -5
    echo "--- SPF @ authority ---"
    dig +short TXT "$DOMAIN" 2>/dev/null | grep -i spf1 | head -3
    echo "--- DKIM mail._domainkey.$DOMAIN ---"
    dig +short TXT "mail._domainkey.$DOMAIN" 2>/dev/null | head -3
    echo "--- DMARC _dmarc.$DOMAIN ---"
    dig +short TXT "_dmarc.$DOMAIN" 2>/dev/null | head -3

    hdr "11. DNS for $DOMAIN — local PowerDNS view (should match above)"
    echo "--- local MX ---"
    dig +short @127.0.0.1 MX "$DOMAIN" 2>/dev/null | head -5
    echo "--- local A mail.$DOMAIN ---"
    dig +short @127.0.0.1 A "mail.$DOMAIN" 2>/dev/null | head -3
fi

hdr "12. OpenDKIM signing table"
cat /etc/opendkim/signing.table 2>/dev/null | head -20
echo "---"
cat /etc/opendkim/key.table 2>/dev/null | head -20

hdr "13. Postfix queue (deferred = could not deliver; hold = stuck)"
postqueue -p 2>&1 | head -40 || true
echo "--- queue summary ---"
mailq 2>&1 | tail -3 || true

hdr "14. Recent mail.log (last 40 lines, AUTH/reject/defer highlighted)"
tail -n 200 /var/log/mail.log 2>/dev/null | grep -E 'sasl|auth|reject|defer|bounce|status=(sent|deferred|bounced)|status=|connect to|relay=' | tail -40
echo "--- plain last 20 ---"
tail -n 20 /var/log/mail.log 2>/dev/null

hdr "15. Roundcube SMTP config (just the relevant lines)"
grep -E "smtp_host|smtp_user|smtp_pass|smtp_conn_options" /etc/roundcube/config.inc.php 2>/dev/null | head -10

echo
echo "=== DIAGNOSIS DONE ==="
echo "Paste the entire output above back to the operator."
