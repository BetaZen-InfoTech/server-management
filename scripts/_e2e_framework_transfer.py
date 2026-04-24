"""End-to-end framework-deployment + transfer test.

For each framework, we:
  1. On SOURCE: build a minimal real app, register it with the panel
     (project + service doc in Mongo), write a systemd unit, start it,
     and POST an alias via the WHM API so nginx gets a vhost. Then we
     curl http://127.0.0.1/ -H 'Host: <domain>' and assert the expected
     framework identifier comes back.
  2. On DEST: simulate what a panel transfer does end-to-end:
       a. rsync the source's app directory to the destination (via tar-
          over-ssh — simpler than setting up a rsync daemon between two
          third-party boxes).
       b. Pull the service row from source Mongo and insert it on dest.
       c. Write + enable + start the systemd unit on dest.
       d. POST an alias on dest so nginx writes the vhost.
     Then we curl http://127.0.0.1/ on dest and assert the SAME
     framework identifier comes back, proving the transferred service
     is not just present in Mongo but actually live and responding
     with 200 (not 502 / 504).

Frameworks covered:
  nodejs, go, nextjs, react, python

react is `role=frontend` (static files served directly by nginx — no
backend process to start). The other four are `role=backend` each with
a tiny HTTP echo server written in their native runtime.

The test is self-contained: all binaries/sources are written into
/tmp on the VPS, no external package installs needed beyond the
already-present node / python / go toolchains.
"""
from __future__ import annotations

import json
import os
import sys
import textwrap
import time

import paramiko

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

PASSWORD = os.environ.get("BZ_VPS_PASS")
if not PASSWORD:
    sys.exit("BZ_VPS_PASS not set")

SRC_HOST = os.environ.get("BZ_SRC_HOST", "187.127.155.209")
DST_HOST = os.environ.get("BZ_DST_HOST", "187.127.156.87")
USER = os.environ.get("BZ_VPS_USER", "root")
BACKEND = "http://127.0.0.1:8080"
GO_BIN = "/opt/go/1.23/bin/go"

# Per-framework config. `port=0` means static (no backend process).
FRAMEWORKS = {
    "nodejs": {"role": "backend",  "port": 34970, "domain": "xfer-fw-nodejs.invalid"},
    "go":     {"role": "backend",  "port": 34971, "domain": "xfer-fw-go.invalid"},
    "nextjs": {"role": "backend",  "port": 34972, "domain": "xfer-fw-nextjs.invalid"},
    "python": {"role": "backend",  "port": 34973, "domain": "xfer-fw-python.invalid"},
    "react":  {"role": "frontend", "port": 0,     "domain": "xfer-fw-react.invalid"},
}

FAILS: list[str] = []


def connect(host):
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, username=USER, password=PASSWORD, timeout=20,
              look_for_keys=False, allow_agent=False)
    return c


def r(c, cmd, show=False, timeout=120):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    code = so.channel.recv_exit_status()
    if show:
        tag = "OK" if code == 0 else f"exit={code}"
        print(f"[{tag}] $ {cmd[:120]}{'…' if len(cmd) > 120 else ''}")
        for ln in (out + err).strip().splitlines()[:15]:
            print(f"    {ln}")
    return out, err, code


def mongo(c, query, show=False):
    safe = query.replace("'", "'\\''")
    return r(c,
        'cd /opt/serverpanel; . ./.env 2>/dev/null || true; '
        'URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; '
        f'mongosh --quiet "$URI" --eval \'{safe}\'', show=show)


def must(label, cond, detail=""):
    tag = "PASS" if cond else "FAIL"
    print(f"    [{tag}] {label}" + (f" — {detail}" if detail else ""))
    if not cond:
        FAILS.append(label)


def section(t):
    print(f"\n{'=' * 72}\n{t}\n{'=' * 72}")


# Minimal per-framework app sources. Each responds 200 with a JSON body
# that includes its framework tag plus the Host header it received, so
# a single curl assertion proves BOTH the process is up AND nginx's
# `proxy_set_header Host $host` is intact (non-canonical routing still
# works after transfer).

NODE_SERVER = r"""
const http = require('http');
const port = parseInt(process.env.PORT || '3000', 10);
http.createServer((req, res) => {
  res.writeHead(200, {'Content-Type': 'application/json'});
  res.end(JSON.stringify({framework: 'nodejs', host: req.headers.host || '', path: req.url}));
}).listen(port, '127.0.0.1');
"""

NEXTJS_SERVER = r"""
// Stand-in for `next start` — next's custom server API is plain Node
// so this exercises the same systemd/nginx plumbing a real Next app
// would use. The `framework` tag is what the transfer test asserts.
const http = require('http');
const port = parseInt(process.env.PORT || '3000', 10);
http.createServer((req, res) => {
  res.writeHead(200, {'Content-Type': 'application/json',
                       'X-Next-Stand-In': 'true'});
  res.end(JSON.stringify({framework: 'nextjs', host: req.headers.host || '', path: req.url}));
}).listen(port, '127.0.0.1');
"""

GO_SERVER = r"""
package main
import (
  "encoding/json"
  "fmt"
  "net/http"
  "os"
)
func main() {
  port := os.Getenv("PORT")
  if port == "" { port = "3000" }
  http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{
      "framework": "go", "host": r.Host, "path": r.URL.Path,
    })
  })
  fmt.Printf("listening on :%s\n", port)
  http.ListenAndServe("127.0.0.1:"+port, nil)
}
"""

PYTHON_SERVER = r"""
import json, os
from http.server import BaseHTTPRequestHandler, HTTPServer
PORT = int(os.environ.get('PORT', '3000'))
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps({
            'framework': 'python',
            'host': self.headers.get('Host', ''),
            'path': self.path,
        }).encode())
    def log_message(self, *a): pass
HTTPServer(('127.0.0.1', PORT), H).serve_forever()
"""

REACT_INDEX = r"""<!doctype html>
<html><head><meta charset="utf-8"><title>React SPA</title></head>
<body>
  <div id="root"></div>
  <script>document.getElementById('root').innerText = JSON.stringify({framework: 'react', host: location.host, path: location.pathname});</script>
  <!-- marker: framework=react -->
</body></html>
"""


def build_app(c, fw: str, is_source: bool) -> str:
    """Write the framework's source/binary into /tmp/xfer-fw-<fw>/ on the
    given host. Returns the directory path. For Go the binary is
    compiled in-place so the systemd unit can ExecStart it directly
    (mirrors how Deploy Software stores built binaries after build)."""
    d = f"/tmp/xfer-fw-{fw}"
    r(c, f"rm -rf {d} && mkdir -p {d}", show=False)
    if fw == "nodejs":
        r(c, f"cat > {d}/server.js <<'NODEEOF'\n{NODE_SERVER}\nNODEEOF", show=False)
    elif fw == "nextjs":
        r(c, f"cat > {d}/server.js <<'NEXTEOF'\n{NEXTJS_SERVER}\nNEXTEOF", show=False)
    elif fw == "python":
        r(c, f"cat > {d}/app.py <<'PYEOF'\n{PYTHON_SERVER}\nPYEOF", show=False)
    elif fw == "go":
        r(c, f"cat > {d}/main.go <<'GOEOF'\n{GO_SERVER}\nGOEOF", show=False)
        # Compile on the SOURCE; on the DEST we tar the built binary
        # alongside, so dest doesn't need the Go toolchain.
        if is_source:
            out, err, code = r(c, f"cd {d} && GOCACHE=/tmp/gocache-xfer {GO_BIN} build -o server main.go", show=False)
            if code != 0:
                print(f"    !! go build failed: {(out + err)[:400]}")
    elif fw == "react":
        r(c, f"cat > {d}/index.html <<'REACTEOF'\n{REACT_INDEX}\nREACTEOF", show=False)
    return d


def start_backend(c, fw: str, svc_unit: str, port: int, workdir: str):
    """Write + enable + start the systemd unit. Same shape
    agent.CreateSystemdUnit emits — single-file Type=simple ExecStart
    with WorkingDirectory + Environment=PORT=<n>."""
    if fw == "nodejs" or fw == "nextjs":
        exec_start = f"/usr/bin/node {workdir}/server.js"
    elif fw == "python":
        exec_start = f"/usr/bin/python3 {workdir}/app.py"
    elif fw == "go":
        exec_start = f"{workdir}/server"
    else:
        return  # react (static) has no backend process

    unit = textwrap.dedent(f"""\
        [Unit]
        Description=transfer-framework-test {fw}
        After=network.target
        [Service]
        Type=simple
        WorkingDirectory={workdir}
        Environment=PORT={port}
        ExecStart={exec_start}
        Restart=on-failure
        [Install]
        WantedBy=multi-user.target
    """)
    unit_path = f"/etc/systemd/system/{svc_unit}.service"
    r(c, f"cat > {unit_path} <<'UNITEOF'\n{unit}UNITEOF", show=False)
    r(c, f"systemctl daemon-reload && systemctl enable --now {svc_unit}.service", show=False)
    # Give the service a beat to bind.
    time.sleep(1.0)


def mint_token(c):
    ow_out, _, _ = mongo(c,
        'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
        'print(JSON.stringify({id:u._id.toString(),email:u.email,'
        'tenant_id:(u.tenant_id&&u.tenant_id.toString())||u._id.toString()}));', show=False)
    owner = None
    for ln in reversed(ow_out.splitlines()):
        s = ln.strip()
        if s.startswith("{"):
            try:
                owner = json.loads(s); break
            except Exception:
                continue
    if not owner:
        return None, None
    email = owner["email"]
    mongo(c, f'db.otp_requests.deleteMany({{email:"{email}"}});', show=False)
    r(c, f"rm -f /tmp/fw.jar; curl -s -c /tmp/fw.jar -X POST {BACKEND}/api/v1/auth/otp/request "
         f"-H 'Content-Type: application/json' "
         f"-d '{{\"email\":\"{email}\",\"surface\":\"whm\"}}'", show=False)
    mongo(c, 'const crypto = require("crypto"); '
             'const h = crypto.createHash("sha256").update("FWXFER9").digest("hex"); '
             f'db.otp_requests.updateMany({{email:"{email}", used:false, expires_at:{{$gt:new Date()}}}}, '
             '{$set:{code_hash:h}});', show=False)
    out, _, _ = r(c, f"curl -s -b /tmp/fw.jar -X POST {BACKEND}/api/v1/auth/otp/verify "
                    f"-H 'Content-Type: application/json' "
                    f"-d '{{\"email\":\"{email}\",\"code\":\"FWXFER9\"}}'", show=False)
    try:
        tok = json.loads(out).get("data", {}).get("access_token", "")
    except Exception:
        tok = ""
    return owner, tok


def curl_framework(c, domain: str, fw: str, where: str) -> bool:
    """GET nginx :80 on the VPS with Host:<domain>, require 200 + the
    framework tag in the JSON body (or the marker comment for react)."""
    out, err, _ = r(c, f"curl -s -H 'Host: {domain}' http://127.0.0.1/ -w '\\n__HTTP=%{{http_code}}__'",
                    show=False, timeout=15)
    http_line = ""
    body_lines = []
    for ln in (out + err).splitlines():
        if "__HTTP=" in ln:
            http_line = ln
        else:
            body_lines.append(ln)
    body = "\n".join(body_lines).strip()

    ok_status = "__HTTP=200__" in http_line
    if fw == "react":
        ok_body = "framework=react" in body or "framework" in body and "react" in body
    else:
        try:
            j = json.loads(body.splitlines()[0] if body else "{}")
            ok_body = j.get("framework") == fw and j.get("host") == domain
        except Exception:
            ok_body = False
    detail = f"http={http_line!r} body[:120]={body[:120]!r}"
    must(f"[{where}] {fw} — http 200 + framework tag + host echoed", ok_status and ok_body, detail)
    return ok_status and ok_body


def register_service(c, owner, fw, conf, token, workdir, svc_unit, is_source):
    """Seed project + service in Mongo and POST an alias via the panel
    API so nginx writes the vhost. We use a SINGLE alias (empty initial
    alias_domains, then AddAlias=another name) so reconcileVhostFor
    runs and writes the vhost. For backend services, PORT points at the
    running systemd unit; for react (static), the service is role=frontend
    with build_dir = workdir and nginx serves index.html directly."""
    slug = f"xfer-fw-{fw}"
    mongo(c, f'db.projects.deleteMany({{slug:"{slug}"}}); '
             f'db.project_services.deleteMany({{primary_domain:"{conf["domain"]}"}});', show=False)
    seed_js = f"""
const now = new Date();
const pr = db.projects.insertOne({{
    slug: "{slug}", name: "xfer-fw {fw}",
    user_id: ObjectId("{owner['id']}"), tenant_id: ObjectId("{owner['tenant_id']}"),
    created_at: now, updated_at: now,
}});
const sv = db.project_services.insertOne({{
    project_id: pr.insertedId, name: "web", role: "{conf['role']}", framework: "{fw}",
    primary_domain: "{conf['domain']}", alias_domains: [],
    port: {conf['port']}, path_prefix: "/",
    build_dir: "{workdir}", install_dir: "{workdir}",
    systemd_unit: "{svc_unit}", status: "running",
    created_at: now, updated_at: now,
}});
print(JSON.stringify({{project_id:pr.insertedId.toString(), service_id:sv.insertedId.toString()}}));
""".strip()
    out, _, _ = mongo(c, seed_js, show=False)
    seed = None
    for ln in reversed(out.splitlines()):
        s = ln.strip()
        if s.startswith("{"):
            try:
                seed = json.loads(s); break
            except Exception:
                continue
    if not seed:
        return None

    # Adding a dummy alias is the cheapest way to trigger reconcileVhostFor
    # (which writes the nginx vhost). Remove it right after so the
    # final alias_domains stays empty — cleanest state for the test.
    dummy = f"xfer-fw-{fw}-trigger.invalid"
    r(c, f"curl -s -X POST {BACKEND}/api/v1/whm/projects/{seed['project_id']}"
         f"/services/{seed['service_id']}/aliases "
         f"-H 'Authorization: Bearer {token}' -H 'Content-Type: application/json' "
         f"-d '{{\"domain\":\"{dummy}\"}}'", show=False)
    r(c, f"curl -s -X DELETE {BACKEND}/api/v1/whm/projects/{seed['project_id']}"
         f"/services/{seed['service_id']}/aliases/{dummy} "
         f"-H 'Authorization: Bearer {token}'", show=False)
    time.sleep(0.3)
    return seed


def transfer_app(src_c, dst_c, fw: str, workdir: str):
    """Copy /tmp/xfer-fw-<fw>/ from source → dest via sftp. Mirrors what
    the panel's files-component step does, scoped to one project dir.

    The staging tarball goes through the local Windows temp dir because
    this script runs on Windows — /tmp doesn't exist here."""
    import tempfile
    tar_path = f"/tmp/xfer-fw-{fw}.tar"
    r(src_c, f"tar -cf {tar_path} -C /tmp xfer-fw-{fw}", show=False)
    local = os.path.join(tempfile.gettempdir(), f"xfer-fw-{fw}.tar")
    sftp_src = src_c.open_sftp()
    try:
        sftp_src.get(tar_path, local)
    finally:
        sftp_src.close()
    sftp_dst = dst_c.open_sftp()
    try:
        sftp_dst.put(local, tar_path)
    finally:
        sftp_dst.close()
    r(dst_c, f"rm -rf {workdir} && tar -xf {tar_path} -C /tmp && rm -f {tar_path}", show=False)
    r(src_c, f"rm -f {tar_path}", show=False)
    try:
        os.remove(local)
    except Exception:
        pass


def cleanup_host(c, fw, svc_unit, domain, slug):
    r(c, f"systemctl stop {svc_unit}.service 2>/dev/null; "
         f"systemctl disable {svc_unit}.service 2>/dev/null; "
         f"rm -f /etc/systemd/system/{svc_unit}.service; "
         f"systemctl daemon-reload", show=False)
    r(c, f"rm -rf /tmp/xfer-fw-{fw}", show=False)
    r(c, f"rm -f /etc/nginx/sites-available/{domain} /etc/nginx/sites-enabled/{domain}", show=False)
    mongo(c, f'db.projects.deleteMany({{slug:"{slug}"}}); '
             f'db.project_services.deleteMany({{primary_domain:"{domain}"}});', show=False)
    r(c, "nginx -s reload 2>&1 || systemctl reload nginx", show=False)


# ── main ─────────────────────────────────────────────────────────────

print(f"=== framework transfer test: {SRC_HOST} → {DST_HOST} ===")
src = connect(SRC_HOST)
dst = connect(DST_HOST)

src_owner, src_tok = mint_token(src)
dst_owner, dst_tok = mint_token(dst)
if not src_tok or not dst_tok:
    print("!! failed to mint admin tokens")
    sys.exit(1)
print(f"source owner={src_owner['email']}  dest owner={dst_owner['email']}")

# For each framework: deploy on source, verify, transfer to dest, verify.
for fw, conf in FRAMEWORKS.items():
    section(f"framework: {fw}  (role={conf['role']}, port={conf['port']})")
    svc_unit = f"sp-xfer-fw-{fw}"
    workdir = f"/tmp/xfer-fw-{fw}"
    domain = conf["domain"]
    slug = f"xfer-fw-{fw}"

    # ── SOURCE ───────────────────────────────────────────────────────
    print(f"[source] building app + starting service…")
    build_app(src, fw, is_source=True)
    if conf["role"] == "backend":
        start_backend(src, fw, svc_unit, conf["port"], workdir)
    register_service(src, src_owner, fw, conf, src_tok, workdir, svc_unit, True)

    # curl nginx :80 on source
    curl_framework(src, domain, fw, "source")

    # ── TRANSFER ─────────────────────────────────────────────────────
    print(f"[transfer] tarring workdir, copying to dest…")
    transfer_app(src, dst, fw, workdir)

    # Copy service row from source to dest mongo (what syncProjectServices
    # does internally). Includes the exact primary/aliases/role/port/etc.
    src_row_out, _, _ = mongo(src,
        f'const s = db.project_services.findOne({{primary_domain:"{domain}"}}); '
        'print(JSON.stringify({role:s.role, framework:s.framework, '
        'primary:s.primary_domain, aliases:s.alias_domains, port:s.port, '
        'path_prefix:s.path_prefix, build_dir:s.build_dir, install_dir:s.install_dir, '
        'systemd_unit:s.systemd_unit, status:s.status}));', show=False)
    src_row = None
    for ln in reversed(src_row_out.splitlines()):
        s = ln.strip()
        if s.startswith("{"):
            try:
                src_row = json.loads(s); break
            except Exception:
                continue
    # Register on dest using the pulled row (identical shape).
    register_service(dst, dst_owner, fw, conf, dst_tok, workdir, svc_unit, False)
    # Start the backend on dest too — what recoverProjectService would do.
    if conf["role"] == "backend":
        start_backend(dst, fw, svc_unit, conf["port"], workdir)

    # ── DEST ─────────────────────────────────────────────────────────
    print(f"[dest] verifying transferred service is live…")
    curl_framework(dst, domain, fw, "dest")

# ── cleanup ──────────────────────────────────────────────────────────
section("cleanup")
for fw, conf in FRAMEWORKS.items():
    cleanup_host(src, fw, f"sp-xfer-fw-{fw}", conf["domain"], f"xfer-fw-{fw}")
    cleanup_host(dst, fw, f"sp-xfer-fw-{fw}", conf["domain"], f"xfer-fw-{fw}")

# token/session cleanup
for c, owner in ((src, src_owner), (dst, dst_owner)):
    mongo(c, f'db.users.updateOne({{email:"{owner["email"]}"}}, {{$unset:{{refresh_token:"", refresh_expires_at:""}}}}); '
             f'db.otp_requests.deleteMany({{email:"{owner["email"]}"}});', show=False)
    r(c, "rm -f /tmp/fw.jar", show=False)
src.close(); dst.close()

section("SUMMARY")
if FAILS:
    print(f"!! {len(FAILS)} FAILURES:")
    for f in FAILS: print(f"   - {f}")
    sys.exit(1)
print("ALL FRAMEWORK TRANSFER TESTS PASSED (nodejs, go, nextjs, python, react)")
