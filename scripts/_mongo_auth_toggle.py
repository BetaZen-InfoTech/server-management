"""Toggle mongo authorization on/off across both test VPS so the
DB transfer test can drive mongodump/restore without admin creds.

Usage: python scripts/_mongo_auth_toggle.py off
       python scripts/_mongo_auth_toggle.py on
"""
import re, sys, paramiko
from pathlib import Path
sys.stdout.reconfigure(encoding="utf-8", errors="replace")

if len(sys.argv) != 2 or sys.argv[1] not in ("on", "off"):
    sys.exit("usage: _mongo_auth_toggle.py {on|off}")
ACTION = sys.argv[1]

text = Path("testing-vps-details.md").read_text()
for label in ("Old", "New"):
    pwd = re.search(rf"#+\s*{label}\s+server.*?password[^`]*`([^`]+)`",
                    text, re.IGNORECASE | re.DOTALL).group(1)
    host = "187.127.155.209" if label == "Old" else "187.127.156.87"
    c = paramiko.SSHClient(); c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, username="root", password=pwd, timeout=20,
              look_for_keys=False, allow_agent=False)

    def run(cmd):
        _, so, se = c.exec_command(cmd, timeout=60)
        return so.read().decode("utf-8", errors="replace") + se.read().decode("utf-8", errors="replace")

    if ACTION == "off":
        # Comment out `authorization: enabled`. Backup once.
        cmd = ("[ -f /etc/mongod.conf.bz-bak ] || cp /etc/mongod.conf /etc/mongod.conf.bz-bak; "
               "sed -i 's/^\\([[:space:]]*\\)authorization:[[:space:]]*enabled/\\1#authorization: enabled/' "
               "/etc/mongod.conf && systemctl restart mongod && sleep 2 && "
               "grep authorization /etc/mongod.conf | head")
    else:
        cmd = ("if [ -f /etc/mongod.conf.bz-bak ]; then "
               "cp /etc/mongod.conf.bz-bak /etc/mongod.conf && rm /etc/mongod.conf.bz-bak; fi; "
               "systemctl restart mongod && sleep 2 && "
               "grep authorization /etc/mongod.conf | head")
    print(f"\n=== {label} ({host}) ===")
    print(run(cmd))
    print(run("systemctl is-active mongod"))
    c.close()
