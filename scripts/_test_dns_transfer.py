"""DNS-portion transfer test: old VPS → new VPS.

Reproduces the DNS-import slice of TransferService (transfer_service.go
~1721-1746): on the source, capture `pdnsutil list-zone`; on the
destination, replay each parsed line via pdnsutil add-record and
insert a Mongo row mirroring the existing transfer code's shape.

Then confirms — on the destination — that the v3.0.8 fixes still hold
for transfer-imported records:

  1. Heal-on-read normalizes shape mismatches (TXT-with-quotes from
     pdns vs Mongo storage, MX with priority prefix, etc.) and gives
     every record a real Mongo ID.
  2. Multi-value rrsets survive (two `ns A` rows with different IPs).
  3. Edit / delete on a transfer-imported record returns 200 — no
     "record not found" toast.

Also runs the /reconcile endpoint on the destination to exercise the
heal path against transfer-imported state.
"""
from __future__ import annotations

import json
import os
import re
import sys
from pathlib import Path

import paramiko

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass


def _load_pwd(section: str) -> str:
    md = Path(__file__).resolve().parent.parent / "testing-vps-details.md"
    if not md.exists():
        return ""
    text = md.read_text(encoding="utf-8", errors="replace")
    s = re.search(rf"#+\s*{section}\s+server.*?(?=^#|\Z)",
                  text, re.IGNORECASE | re.DOTALL | re.MULTILINE)
    blob = s.group(0) if s else text
    p = re.search(r"password[^`]*`([^`]+)`", blob, re.IGNORECASE)
    return p.group(1) if p else ""


SRC_HOST = "187.127.155.209"
SRC_PWD = _load_pwd("Old")
DST_HOST = "187.127.156.87"
DST_PWD = _load_pwd("New")
if not SRC_PWD or not DST_PWD:
    sys.exit("Could not read both VPS passwords from testing-vps-details.md")

ZONE = "transfer-dns-smoketest.invalid"
DST_BACKEND = "http://127.0.0.1:8080"


def open_ssh(host, pwd):
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, username="root", password=pwd, timeout=20,
              look_for_keys=False, allow_agent=False)
    return c


def r(c, cmd, show=True, timeout=60):
    _, so, se = c.exec_command(cmd, timeout=timeout)
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    code = so.channel.recv_exit_status()
    if show:
        tag = "OK" if code == 0 else f"exit={code}"
        print(f"\n[{tag}] $ {cmd[:140]}{'…' if len(cmd) > 140 else ''}")
        body = (out + err).strip()
        if body:
            for line in body.splitlines()[:20]:
                print(f"    {line}")
    return out.strip(), err.strip(), code


def mongo(c, q, show=True):
    safe = q.replace("'", "'\\''")
    return r(c,
        'cd /opt/serverpanel; . ./.env 2>/dev/null || true; '
        'URI=${MONGO_URI:-mongodb://localhost:27017/serverpanel}; '
        f'mongosh --quiet "$URI" --eval \'{safe}\'', show=show)


src = open_ssh(SRC_HOST, SRC_PWD)
dst = open_ssh(DST_HOST, DST_PWD)


# ─── 1. SOURCE: build a realistic zone with the mix that's known to break ───
print(f"=== 1. SOURCE ({SRC_HOST}): create zone with mixed record shapes ===")
r(src, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
r(src, f"pdnsutil create-zone {ZONE} ns1.{ZONE}.")
r(src, f"pdnsutil add-record {ZONE} @     A    60   1.2.3.4")
# Multi-value rrset (the v3.0.7 case)
r(src, f"pdnsutil add-record {ZONE} ns    A    60   5.6.7.1")
r(src, f"pdnsutil add-record {ZONE} ns    A    60   5.6.7.2")
# TXT with the quoting that broke heal-on-read pre-3.0.8
r(src, f"pdnsutil add-record {ZONE} @     TXT  3600 '\"v=spf1 ip4:1.2.3.4 ~all\"'")
# MX with priority
r(src, f"pdnsutil add-record {ZONE} @     MX   3600 '10 mail.{ZONE}.'")
r(src, "pdns_control reload", show=False)

print("\n   Source zone export (the format the transfer pipeline reads):")
src_export, _, _ = r(src, f"pdnsutil list-zone {ZONE}")


# ─── 2. DESTINATION: replay the export the same way TransferService does ───
print(f"\n=== 2. DEST ({DST_HOST}): wipe any prior + import via transfer-shape ===")
r(dst, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
mongo(dst,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); }',
      show=False)

# We need a domain row + zone row for assertCallerOwnsDomain to pass.
admin_out, _, _ = mongo(dst,
    'const u = db.users.findOne({role:"vendor_owner", is_active:true, deleted_at:null}); '
    'print(JSON.stringify({id: u._id.toString(), email: u.email, '
    'tenant_id: (u.tenant_id && u.tenant_id.toString()) || u._id.toString()}));',
    show=False)
admin = None
for line in reversed(admin_out.splitlines()):
    s = line.strip()
    if s.startswith("{"):
        try: admin = json.loads(s); break
        except: continue

mongo(dst,
      f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
      f'db.domains.insertOne({{domain:"{ZONE}", '
      f'user_id: ObjectId("{admin["id"]}"), tenant_id: ObjectId("{admin["tenant_id"]}"), '
      'is_active:true, created_at:new Date(), updated_at:new Date()}});',
      show=False)

# Create the zone shell on dest
r(dst, f"pdnsutil create-zone {ZONE} ns1.{ZONE}.", show=False)
zone_oid_out, _, _ = mongo(dst,
    f'const id = db.dns_zones.insertOne({{domain:"{ZONE}", '
    f'user_id: ObjectId("{admin["id"]}"), '
    f'tenant_id: ObjectId("{admin["tenant_id"]}"), '
    f'server_ip:"127.0.0.1", nameservers:["ns1.{ZONE}"], '
    'serial:1, created_at:new Date(), updated_at:new Date()}).insertedId; '
    'print(id.toString());', show=False)
zone_oid = ""
for line in reversed(zone_oid_out.splitlines()):
    s = line.strip()
    if len(s) == 24 and all(ch in "0123456789abcdef" for ch in s):
        zone_oid = s; break
print(f"   created dest zone {ZONE}, _id={zone_oid}")

# Replay each parsed line, mirroring transfer_service.go:1721-1746.
# Skip SOA / NS / default records auto-created by pdnsutil create-zone.
imported_to_pdns = 0
imported_to_mongo = []
for line in src_export.splitlines():
    parts = line.split()
    if len(parts) < 5:
        continue
    name_fqdn, ttl, in_, rtype = parts[0], parts[1], parts[2], parts[3]
    if rtype in ("SOA",):
        continue
    value = " ".join(parts[4:])
    name = name_fqdn.rstrip(".")
    suffix = "." + ZONE
    if name == ZONE:
        name = "@"
    elif name.endswith(suffix):
        name = name[: -len(suffix)]

    # Skip the auto-created NS and SOA on dest's create-zone
    if rtype == "NS" and name == "@":
        continue

    r(dst, f"pdnsutil add-record {ZONE} {name} {rtype} {ttl} {value!r}", show=False)
    imported_to_pdns += 1

    # Mongo insert (mirrors transfer code shape: priority parsed off MX,
    # value stored raw including any TXT quotes from pdns output).
    rec = {
        "type": rtype, "name": name, "value": value, "ttl": int(ttl),
    }
    if rtype == "MX":
        fields = value.split(None, 1)
        if len(fields) == 2 and fields[0].isdigit():
            rec["priority"] = int(fields[0])
            rec["value"] = fields[1]
    imported_to_mongo.append(rec)

# Bulk insert into Mongo
ins_js = (
    'const z_id = ObjectId("' + zone_oid + '"); '
    'const now = new Date(); '
    'const docs = ' + json.dumps(imported_to_mongo) + '; '
    'docs.forEach(d => { d.zone_id = z_id; d.created_at = now; d.updated_at = now; '
    'if (d.priority !== undefined) d.priority = d.priority; }); '
    'const r = db.dns_records.insertMany(docs); '
    'print("inserted " + Object.keys(r.insertedIds).length);'
)
mongo(dst, ins_js)
r(dst, "pdns_control reload", show=False)

print(f"\n   imported to dest pdns: {imported_to_pdns} records")


# ─── 3. Mint admin token on dest, hit the API ────────────────────────────────
print(f"\n=== 3. DEST: GET /records — heal-on-read should normalize transfer shape ===")
mongo(dst, f'db.otp_requests.deleteMany({{email:"{admin["email"]}"}});', show=False)
r(dst, f"rm -f /tmp/t.jar; curl -s -c /tmp/t.jar -X POST {DST_BACKEND}/api/v1/auth/otp/request "
       f"-H 'Content-Type: application/json' "
       f"-d '{{\"email\":\"{admin['email']}\",\"surface\":\"whm\"}}'", show=False)
mongo(dst,
      'const crypto = require("crypto"); '
      'const h = crypto.createHash("sha256").update("XFER999").digest("hex"); '
      f'db.otp_requests.updateMany({{email:"{admin["email"]}", used:false, expires_at:{{$gt:new Date()}}}}, '
      '{$set:{code_hash:h}});', show=False)
out, _, _ = r(dst,
    f"curl -s -b /tmp/t.jar -X POST {DST_BACKEND}/api/v1/auth/otp/verify "
    f"-H 'Content-Type: application/json' "
    f"-d '{{\"email\":\"{admin['email']}\",\"code\":\"XFER999\"}}'", show=False)
token = json.loads(out).get("data", {}).get("access_token", "")
H = f"-H 'Authorization: Bearer {token}'"

# List records — exercises heal-on-read + value normalization
out, _, _ = r(dst, f"curl -s {DST_BACKEND}/api/v1/whm/dns/zones/{ZONE}/records {H}")
try:
    data = json.loads(out).get("data", [])
    zeros = [d for d in data if d.get("id") == "000000000000000000000000"]
    print(f"\n   total rows: {len(data)}, with REAL Mongo ID: {len(data) - len(zeros)}, zero-ID: {len(zeros)}")
    if zeros:
        for d in zeros[:3]:
            print(f"     - {d.get('type')} {d.get('name')} {str(d.get('value'))[:40]}")
        print("    !! ASSERTION FAILED: heal-on-read should backfill every row")
    else:
        print("   ✓ every transfer-imported row has a real Mongo ID")

    # Spot-check the multi-value `ns A` rrset arrived as 2 separate rows
    ns_rows = [d for d in data if d.get("type") == "A" and d.get("name") == "ns"]
    print(f"   multi-value ns A rows: {len(ns_rows)} (expected 2)")
    if len(ns_rows) == 2:
        print("   ✓ multi-value rrset preserved across transfer")
    else:
        print(f"   !! multi-value broken: {[(r['value'], r['ttl']) for r in ns_rows]}")
except Exception as e:
    print(f"!! parse failed: {e}\n{out[:400]}")
    data = []


# ─── 4. Edit a transfer-imported A record ────────────────────────────────────
print(f"\n=== 4. Edit transfer-imported `ns A 5.6.7.1` (multi-value) → TTL 600 ===")
ns_one = next((d for d in data if d.get("type") == "A" and d.get("name") == "ns"
               and d.get("value") == "5.6.7.1"), None)
if ns_one:
    out, _, _ = r(dst, f"curl -s -X PUT {DST_BACKEND}/api/v1/whm/dns/zones/{ZONE}/records/{ns_one['id']} "
                  f"{H} -H 'Content-Type: application/json' "
                  f"-d '{{\"type\":\"A\",\"name\":\"ns\",\"value\":\"5.6.7.1\",\"ttl\":600}}' "
                  "-w '\\nHTTP=%{http_code}\\n'")
    if "HTTP=200" in out:
        print("   ✓ edit returned 200")
    else:
        print(f"   !! {out[:200]}")


# ─── 5. Delete transfer-imported `ns A 5.6.7.2` (sibling must survive) ───────
print(f"\n=== 5. Delete `ns A 5.6.7.2`, expect `ns A 5.6.7.1` to survive ===")
ns_two = next((d for d in data if d.get("type") == "A" and d.get("name") == "ns"
               and d.get("value") == "5.6.7.2"), None)
if ns_two:
    out, _, _ = r(dst, f"curl -s -X DELETE {DST_BACKEND}/api/v1/whm/dns/zones/{ZONE}/records/{ns_two['id']} "
                  f"{H} -w '\\nHTTP=%{{http_code}}\\n'")
    if "HTTP=200" in out and '"success":true' in out:
        print("   ✓ delete returned 200")
    else:
        print(f"   !! {out[:200]}")

print("\n   PowerDNS state — `ns A` should have ONLY 5.6.7.1 left:")
r(dst, f"pdnsutil list-zone {ZONE} | grep -E 'ns\\..*\\sA\\s' | head -5")


# ─── 6. Reconcile endpoint on a transfer-imported zone ───────────────────────
print(f"\n=== 6. POST /reconcile (heal endpoint) on transfer-imported zone ===")
out, _, _ = r(dst, f"curl -s -X POST {DST_BACKEND}/api/v1/whm/dns/zones/{ZONE}/reconcile "
              f"{H} -w '\\nHTTP=%{{http_code}}\\n'")
print(f"   {out}")


# ─── 7. Cleanup ──────────────────────────────────────────────────────────────
print("\n=== 7. Cleanup ===")
r(dst, f"curl -s -X DELETE {DST_BACKEND}/api/v1/whm/dns/zones/{ZONE} {H}", show=False)
r(dst, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
mongo(dst,
      f'const z = db.dns_zones.findOne({{domain:"{ZONE}"}}); '
      'if (z) { db.dns_records.deleteMany({zone_id: z._id}); db.dns_zones.deleteOne({_id: z._id}); } '
      f'db.domains.deleteMany({{domain:"{ZONE}"}}); '
      f'db.users.updateOne({{email:"{admin["email"]}"}}, {{$unset:{{refresh_token:"", refresh_expires_at:""}}}}); '
      f'db.otp_requests.deleteMany({{email:"{admin["email"]}"}});',
      show=False)
r(src, f"pdnsutil delete-zone {ZONE} 2>&1 || true", show=False)
r(dst, "rm -f /tmp/t.jar", show=False)
src.close(); dst.close()
print("\n=== DNS-transfer test complete ===")
