"""Upload a local script to a box and run it — keeping any credential logic
inside the uploaded file (not in the shell command line). Prints only the
script's own stdout/stderr, so a script that authenticates server-side and
never echoes secrets keeps them off the transcript.

Usage: python scripts/bz_remote.py <host> <local_script> <remote_name> [args...]
"""
import sys
sys.path.insert(0, "scripts")
import deploy_mail_suite as d  # reuses connect() + password loading

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass


def main() -> int:
    if len(sys.argv) < 4:
        sys.exit("usage: bz_remote.py <host> <local_script> <remote_name> [args...]")
    host, local, remote = sys.argv[1], sys.argv[2], sys.argv[3]
    args = " ".join(sys.argv[4:])
    c = d.connect(host)
    try:
        sftp = c.open_sftp()
        sftp.put(local, "/tmp/" + remote)
        sftp.chmod("/tmp/" + remote, 0o700)
        sftp.close()
        code, out, err = d.run(c, f"bash /tmp/{remote} {args}".strip(), timeout=2400)
        body = (out + err).rstrip()
        if body:
            print(body)
        print(f"[exit {code}]")
        return code
    finally:
        c.close()


if __name__ == "__main__":
    sys.exit(main())
