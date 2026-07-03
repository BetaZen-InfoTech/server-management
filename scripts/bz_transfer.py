"""Phase 5 orchestrator: on the DESTINATION box (server 2), authenticate as
owner, drop the SOURCE root password into /tmp/_srcpass via SFTP (never through
a shell arg), then run the transfer script pulling from the SOURCE. Only
non-secret output is surfaced.

Usage: python scripts/bz_transfer.py <dest_ip> <source_ip> <scratchdir>
"""
import sys
sys.path.insert(0, "scripts")
import deploy_mail_suite as d

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass


def main() -> int:
    dest, src, scratch = sys.argv[1], sys.argv[2], sys.argv[3].rstrip("/")
    c = d.connect(dest)
    try:
        sftp = c.open_sftp()
        # 1. auth as owner (token -> /tmp/_bztok, no secret echoed)
        sftp.put(f"{scratch}/bz_auth.sh", "/tmp/bz_auth.sh"); sftp.chmod("/tmp/bz_auth.sh", 0o700)
        _, o, e = d.run(c, "bash /tmp/bz_auth.sh", timeout=120)
        print("--- auth ---"); print((o + e).strip())
        # 2. source root password onto the box (same shared VPS password), 0600, never printed
        with sftp.open("/tmp/_srcpass", "w") as f:
            f.write(d.PASSWORD)
        sftp.chmod("/tmp/_srcpass", 0o600)
        # 3. run the transfer
        sftp.put(f"{scratch}/bz_transfer.sh", "/tmp/bz_transfer.sh"); sftp.chmod("/tmp/bz_transfer.sh", 0o700)
        sftp.close()
        print("--- transfer ---")
        code, o, e = d.run(c, f"bash /tmp/bz_transfer.sh {src}", timeout=2200)
        print((o + e).strip())
        # 4. wipe the source password file
        d.run(c, "rm -f /tmp/_srcpass", timeout=20)
        return code
    finally:
        c.close()


if __name__ == "__main__":
    sys.exit(main())
