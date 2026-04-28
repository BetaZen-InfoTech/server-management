"""Dump the most recent transfer job's logs from the dest VPS."""
import sys, re, paramiko
from pathlib import Path
sys.stdout.reconfigure(encoding="utf-8", errors="replace")

text = Path("testing-vps-details.md").read_text()
pwd = re.search(r"#+\s*New\s+server.*?password[^`]*`([^`]+)`", text, re.IGNORECASE | re.DOTALL).group(1)
c = paramiko.SSHClient(); c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect("187.127.156.87", username="root", password=pwd, timeout=20,
          look_for_keys=False, allow_agent=False)

js = (
    'const j = db.transfer_jobs.find({}).sort({_id:-1}).limit(1).toArray()[0]; '
    'if (j) { '
    '  print("job=" + j._id + " status=" + j.status); '
    '  (j.logs||[]).forEach(l => print("[" + l.level + "]" + (l.step ? "[" + l.step + "]" : "") + " " + l.message)); '
    '}'
)
shell = ("cd /opt/serverpanel; . ./.env 2>/dev/null || true; "
         "URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; "
         f"mongosh --quiet \"$URI\" --eval '{js}'")
_, so, se = c.exec_command(shell, timeout=30)
print(so.read().decode("utf-8", errors="replace"))
print(se.read().decode("utf-8", errors="replace"))
c.close()
