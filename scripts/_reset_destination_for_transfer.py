#!/usr/bin/env python3
"""DESTRUCTIVE — wipe hosted-data state on a destination VPS so a
fresh server transfer can be re-run cleanly.

PRESERVES (panel keeps working):
  - super-admin user row in Mongo
  - panel auth state (JWT secret, encryption key in /opt/serverpanel/.env)
  - panel nginx vhost + Let's Encrypt cert
  - panel systemd unit
  - betazeninfotech.com DNS zone
  - mysql/sys/serverpanel mysql users + DBs (panel internals)

DELETES (on confirm):
  Mongo collections (whole-collection drop):
    mailboxes, forwarders, autoresponders, spam_settings, dkim_keys,
    domains, dns_zones, dns_records, ssl_certificates, subdomains,
    apps, projects, project_services, project_deployments, packages,
    databases, db_users, db_access_hosts, ftp_accounts, ssh_keys,
    wordpress_installs, cron_jobs, file_permissions, webhook_endpoints,
    webhook_deliveries, api_tokens
  Mongo users (filtered): every users row whose role != 'vendor_owner'
  Filesystem:
    /var/vmail/*  (every Maildir)
    /home/<u>/    for every linux user with uid >= 1000 (panel-created
                  vendor accounts) EXCEPT 'ubuntu' / system users
    /etc/dovecot/users          (zeroed)
    /etc/postfix/virtual_mailbox_maps + .db
    /etc/postfix/virtual_mailbox_domains + .db
    /etc/postfix/virtual_alias_maps + .db
    /etc/pure-ftpd/pureftpd.passwd + .pdb
  PowerDNS zones (every zone EXCEPT betazeninfotech.com)
  MySQL: every database + user EXCEPT mysql, information_schema,
    performance_schema, sys, serverpanel
  Linux users with uid >= 1000 (after their /home/ is wiped)

USAGE:
  Step 1 — dry run (shows counts, deletes nothing):
      sudo python3 scripts/_reset_destination_for_transfer.py

  Step 2 — actual delete (only after reviewing dry-run output):
      sudo python3 scripts/_reset_destination_for_transfer.py --confirm

  Step 3 — re-run the transfer from WHM UI; everything should land
           clean with v3.1.50/51/52 fixes active.
"""
from __future__ import annotations

import os
import shutil
import subprocess
import sys

DRY_RUN = "--confirm" not in sys.argv
DESTRUCTIVE_MONGO_COLLECTIONS = [
    "mailboxes", "forwarders", "autoresponders", "spam_settings", "dkim_keys",
    "domains", "dns_zones", "dns_records", "ssl_certificates", "subdomains",
    "apps", "projects", "project_services", "project_deployments", "packages",
    "databases", "db_users", "db_access_hosts", "ftp_accounts", "ssh_keys",
    "wordpress_installs", "cron_jobs", "file_permissions",
    "webhook_endpoints", "webhook_deliveries", "api_tokens",
    "transfer_jobs", "transfer_logs",
]
PRESERVE_MYSQL_DBS = {"mysql", "information_schema", "performance_schema", "sys", "serverpanel"}
PRESERVE_MYSQL_USERS = {"root", "mysql.session", "mysql.sys", "mysql.infoschema", "debian-sys-maint", "serverpanel"}
PRESERVE_LINUX_USERS = {"root", "ubuntu", "vmail", "dovecot", "dovenull", "postfix", "pulse", "www-data", "redis", "mysql", "pdns", "nobody", "systemd-network", "systemd-resolve", "systemd-coredump", "systemd-timesync", "messagebus", "sshd", "_apt", "_chrony", "syslog", "tss", "tcpdump", "_rpc", "uuidd", "landscape", "fwupd-refresh", "git", "memcache"}
PRESERVE_DNS_ZONES = {"betazeninfotech.com"}


def banner(msg: str) -> None:
    print()
    print("=" * 72)
    print("  " + msg)
    print("=" * 72)


def sh(cmd: str, *, show: bool = True, dry_run_safe: bool = False) -> tuple[str, str, int]:
    """Run a shell command. If DRY_RUN and not dry_run_safe, just print it."""
    if DRY_RUN and not dry_run_safe:
        print(f"  [DRY-RUN] would run: {cmd}")
        return ("", "", 0)
    p = subprocess.run(["bash", "-c", cmd], capture_output=True)
    out = p.stdout.decode("utf-8", errors="replace").strip()
    err = p.stderr.decode("utf-8", errors="replace").strip()
    if show:
        tag = "OK" if p.returncode == 0 else f"exit={p.returncode}"
        print(f"  [{tag}] $ {cmd[:160]}")
        for ln in (out + ("\n" + err if err else "")).splitlines()[:6]:
            print(f"      {ln}")
    return out, err, p.returncode


def env(key: str) -> str:
    if not os.path.exists("/opt/serverpanel/.env"):
        return ""
    with open("/opt/serverpanel/.env") as f:
        for ln in f:
            if ln.startswith(key + "="):
                return ln.split("=", 1)[1].strip().strip('"').strip("'")
    return ""


def mongo_uri() -> str:
    return env("MONGO_URI") or "mongodb://localhost:27017/serverpanel"


def mongo(query: str, *, dry_run_safe: bool = True) -> str:
    """Run a mongosh eval. Read-only by default (dry_run_safe=True)."""
    safe = query.replace("'", "'\\''")
    out, _, _ = sh(
        f"mongosh --quiet '{mongo_uri()}' --eval '{safe}'",
        show=False, dry_run_safe=dry_run_safe,
    )
    return out


def section_mongo() -> None:
    banner("Mongo wipe — collections + non-owner users")
    for col in DESTRUCTIVE_MONGO_COLLECTIONS:
        n = mongo(f"db.{col}.countDocuments()")
        try:
            count = int(n.strip().splitlines()[-1])
        except Exception:
            count = 0
        print(f"  {col:<28s}  rows: {count}")
        if count > 0:
            mongo(f"db.{col}.drop()", dry_run_safe=False)
    print()
    # users — keep vendor_owner only
    n = mongo('db.users.countDocuments({role: {$ne: "vendor_owner"}})')
    try:
        deletable_users = int(n.strip().splitlines()[-1])
    except Exception:
        deletable_users = 0
    n2 = mongo('db.users.countDocuments({role: "vendor_owner"})')
    try:
        owners = int(n2.strip().splitlines()[-1])
    except Exception:
        owners = 0
    print(f"  users non-owner             rows: {deletable_users}  (delete)")
    print(f"  users vendor_owner          rows: {owners}            (PRESERVE)")
    if deletable_users > 0:
        mongo('db.users.deleteMany({role: {$ne: "vendor_owner"}})', dry_run_safe=False)


def section_filesystem() -> None:
    banner("Filesystem wipe — Maildirs + /home + dovecot/postfix maps")

    # /var/vmail
    if os.path.isdir("/var/vmail"):
        entries = [e for e in os.listdir("/var/vmail") if e != "lost+found"]
        print(f"  /var/vmail/  domain dirs: {len(entries)}")
        for e in entries[:5]:
            print(f"    - {e}")
        if entries and not DRY_RUN:
            for e in entries:
                shutil.rmtree(f"/var/vmail/{e}", ignore_errors=True)

    # /home — list panel-created users (uid>=1000, not in preserve list)
    out, _, _ = sh(
        "awk -F: '$3 >= 1000 && $1 != \"nobody\" {print $1}' /etc/passwd",
        show=False, dry_run_safe=True,
    )
    home_users = [u for u in out.splitlines() if u and u not in PRESERVE_LINUX_USERS]
    print(f"\n  /home/<u>/  panel-created linux users: {len(home_users)}")
    for u in home_users[:10]:
        print(f"    - {u}  (will rm -rf /home/{u} + userdel)")
    for u in home_users:
        sh(f"deluser --remove-home --quiet {u} 2>/dev/null || userdel -rf {u} 2>/dev/null", show=False)

    # dovecot + postfix
    print()
    for path in [
        "/etc/dovecot/users",
        "/etc/postfix/virtual_mailbox_maps", "/etc/postfix/virtual_mailbox_maps.db",
        "/etc/postfix/virtual_mailbox_domains", "/etc/postfix/virtual_mailbox_domains.db",
        "/etc/postfix/virtual_alias_maps", "/etc/postfix/virtual_alias_maps.db",
        "/etc/pure-ftpd/pureftpd.passwd", "/etc/pure-ftpd/pureftpd.pdb",
    ]:
        if os.path.exists(path):
            sz = os.path.getsize(path)
            print(f"  {path}  size: {sz}  → truncate / remove")
            if not DRY_RUN:
                if path.endswith(".db") or path.endswith(".pdb"):
                    try:
                        os.remove(path)
                    except OSError:
                        pass
                else:
                    open(path, "w").close()
        else:
            print(f"  {path}  (absent)")


def section_dns() -> None:
    banner("PowerDNS — drop every zone except betazeninfotech.com")
    out, _, _ = sh("pdnsutil list-all-zones 2>/dev/null", show=False, dry_run_safe=True)
    zones = [z.strip() for z in out.splitlines() if z.strip()]
    deletable = [z for z in zones if z not in PRESERVE_DNS_ZONES]
    print(f"  zones total: {len(zones)}, deletable: {len(deletable)}")
    for z in deletable[:10]:
        print(f"    - {z}")
    for z in deletable:
        sh(f"pdnsutil delete-zone {z} 2>/dev/null", show=False)


def section_mysql() -> None:
    banner("MySQL — drop hosted DBs + non-system users")
    out, _, _ = sh(
        'mysql -N -e "SELECT schema_name FROM information_schema.schemata"',
        show=False, dry_run_safe=True,
    )
    dbs = [d.strip() for d in out.splitlines() if d.strip()]
    deletable_dbs = [d for d in dbs if d.lower() not in PRESERVE_MYSQL_DBS]
    print(f"  databases: {len(dbs)} total, {len(deletable_dbs)} deletable")
    for d in deletable_dbs[:10]:
        print(f"    - {d}")
    for d in deletable_dbs:
        sh(f"mysql -e 'DROP DATABASE IF EXISTS `{d}`'", show=False)

    out, _, _ = sh(
        'mysql -N -e "SELECT CONCAT(User,\\\"@\\\",Host) FROM mysql.user"',
        show=False, dry_run_safe=True,
    )
    users = [u.strip() for u in out.splitlines() if u.strip() and "@" in u]
    deletable_users = [u for u in users if u.split("@", 1)[0] not in PRESERVE_MYSQL_USERS]
    print(f"\n  mysql users: {len(users)} total, {len(deletable_users)} deletable")
    for u in deletable_users[:10]:
        print(f"    - {u}")
    for u in deletable_users:
        name, host = u.split("@", 1)
        sh(f"mysql -e \"DROP USER IF EXISTS '{name}'@'{host}'\"", show=False)
    if not DRY_RUN:
        sh("mysql -e 'FLUSH PRIVILEGES'", show=False)


def section_services_reload() -> None:
    if DRY_RUN:
        return
    banner("Reload mail + ftp services")
    sh("systemctl reload postfix 2>/dev/null", show=True)
    sh("systemctl reload dovecot 2>/dev/null", show=True)
    sh("pure-pw mkdb 2>/dev/null", show=True)
    sh("systemctl reload pure-ftpd 2>/dev/null", show=True)


def section_safety_check() -> None:
    """Sanity check that /opt/serverpanel/.env exists and the panel
    super-admin row is intact. Abort if either is missing — running
    this script on a non-Betazen box would nuke real data."""
    if not os.path.exists("/opt/serverpanel/.env"):
        print("[ABORT] /opt/serverpanel/.env not found — refusing to run on a non-Betazen box.")
        sys.exit(2)
    n = mongo('db.users.countDocuments({role: "vendor_owner"})')
    try:
        owners = int(n.strip().splitlines()[-1])
    except Exception:
        owners = 0
    if owners < 1:
        print("[ABORT] zero vendor_owner users in Mongo — wrong DB? refusing to run.")
        sys.exit(2)


def main() -> int:
    print("\n" + ("DRY-RUN MODE" if DRY_RUN else "DESTRUCTIVE MODE — --confirm provided"))
    section_safety_check()
    section_mongo()
    section_filesystem()
    section_dns()
    section_mysql()
    section_services_reload()
    print()
    if DRY_RUN:
        print("Dry run complete. Re-run with --confirm to actually delete.")
    else:
        print("✓ Destination wiped. Now re-run the transfer from WHM UI.")
        print("  After it completes:")
        print("    bzpanel diag-mail-login <one-of-your-emails>")
        print("  to confirm every link in the IMAP-login chain is wired.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
