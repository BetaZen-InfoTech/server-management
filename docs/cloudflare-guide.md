# Cloudflare Integration — Complete Guide

*Betazen Server Panel · how to create a Cloudflare API token, add it to the panel, connect your domains, get the Cloudflare nameservers, and sync all your DNS records (including subdomains).*

> **এক লাইনে (TL;DR):** Cloudflare-এ একটা **API token** বানাবে → সেটা panel-এর **Settings → Cloudflare**-এ বসাবে → প্রতিটা primary domain **Connect** করবে → panel তোমাকে **Cloudflare nameserver** দিবে (registrar-এ বসাতে হবে) → তারপর **Sync** করলে ওই domain-এর সব DNS record (subdomain সহ) Cloudflare-এ চলে যাবে। Server migration করলে Cloudflare-এর web record গুলো নতুন IP-তে **নিজে থেকেই** update হবে (mail record অক্ষত থাকবে)।

---

## ⭐ Quick recipe — main domain → Cloudflare (connect → nameservers → push DNS)

> এই ৭ ধাপে একটা **main/primary domain** (apex, যেমন `example.com`) Cloudflare-এ চলে যাবে: nameserver পাবে, আর ওই domain-এর **সব DNS record (subdomain সহ)** Cloudflare-এ push হবে। বিস্তারিত §3–§5-এ।

**শর্ত (আগে নিশ্চিত করো):**
- Settings → Cloudflare-এ token বসানো ও **Test connection = Connected** (§1–§2)। *(ভুল token চিনবে কীভাবে: token-এ `cfat_` prefix বা S3/R2 credential থাকলে ওটা **ভুল** — Zone·DNS·Edit + Zone·Zone·Read ওয়ালা user token লাগবে।)*
- Domain + তার DNS record গুলো **এই panel-এ** থাকতে হবে — Sync panel-এর নিজের (PowerDNS) record গুলোই Cloudflare-এ পাঠায়। (WHM → DNS Zones-এ দেখে নাও।)

1. **WHM → Network → Cloudflare** খোলো।
2. **"Reconcile (read-only)"** card-এ main domain লেখো (apex, `example.com`) → **Compare**। local ও Cloudflare record পাশাপাশি দেখাবে (Matched / Local only / Cloudflare only / Conflict)।
3. **Connect to Cloudflare** ক্লিক করো → panel zone **find-or-create** করে আর **২টা Cloudflare nameserver দেখায়**, যেমন:
   ```
   dana.ns.cloudflare.com
   rob.ns.cloudflare.com
   ```
   **👉 এখানেই তোমার main domain-এর nameserver।** (পরে যেকোনো সময় Compare বা **Check nameservers** দিয়ে আবার দেখা যায়। Connect-এর **আগে** কোনো Cloudflare nameserver থাকে না।)
4. **"Sync & live progress"** card → **Sync `example.com`** ক্লিক করো → apex + `www` + সব subdomain + MX/SPF/DKIM/DMARC Cloudflare-এ push হয় (live progress সহ)। **Mail record protected** — কখনো proxy/delete হয় না। Subdomain আলাদা করতে হয় না (parent zone-এর ভেতরেই যায়)।
5. আবার **Compare** → সব record **Matched** কিনা দেখো (মানে DNS Cloudflare-এ পৌঁছেছে)।
6. **registrar-এ** (domain যেখানে কেনা) গিয়ে nameserver ২টা Cloudflare-এরটা বসাও।
7. Panel-এ **Check nameservers** → state `active` না হওয়া পর্যন্ত অপেক্ষা (`nameserver_update_required` → `pending_activation` → `active`)।

**অনেকগুলো domain একসাথে:** step 2–4 এক এক করে না করে, "Sync & live progress" card-এ **Reconcile all (connect + sync)** ক্লিক করো — সব eligible domain এক ক্লিকে connect + sync হয় (background job, live progress সহ)। তারপর প্রতিটার registrar-এ NS বসাতে হবে।

**⚠️ Order গুরুত্বপূর্ণ:** আগে **Sync** (record Cloudflare-এ ঢোকাও) → *তারপর* registrar-এ nameserver পাল্টাও। record যাওয়ার আগেই NS পাল্টালে Cloudflare zone খালি থাকবে → **website down**। Compare-এ "Matched" দেখে তবেই NS পাল্টাও।

---

## 0. What this integration does (and doesn't)

- **Additive.** Your existing PowerDNS setup keeps working exactly as before. Cloudflare is opt‑in **per domain**. Nothing is deleted or changed until you explicitly connect + sync a domain.
- **One centralized token.** You add **one** account‑level Cloudflare API token to the panel (as the platform owner). Every domain uses it — customers never need their own Cloudflare login.
- **Mail is protected.** MX, SPF, DKIM, DMARC and the `mail` A record are never proxied and never moved/deleted by a web‑IP change. (আলাদা করে mail নিয়ে চিন্তা করতে হবে না।)
- **Server migration aware.** After you migrate a server to a new IP, the Cloudflare **web** records repoint to the new IP automatically; mail records stay put.

---

## 1. Create a Cloudflare API token

Do this once, at Cloudflare (not in the panel).

1. Log in to **https://dash.cloudflare.com**.
2. Top‑right avatar → **My Profile** → **API Tokens** → **Create Token**.
3. Use the **"Edit zone DNS"** template, **or** **Create Custom Token** with these permissions:

   | Type | Item | Access |
   |---|---|---|
   | Zone | **DNS** | **Edit** |
   | Zone | **Zone** | **Read** |
   | Zone | **Zone Settings** | **Read** *(optional, for status)* |

   - **Zone Resources:** *Include → All zones* (so you can add new zones), or a specific account.
   - **Account Resources** (needed to **create** new zones): add **Account → All accounts → Read**, or pick your account. Simplest: use a token that can **create zones** in your account.
4. Click **Continue to summary → Create Token**. **Copy the token now** — Cloudflare shows it only once. It looks like a long random string (no fixed prefix).
5. Get your **Account ID**: Cloudflare dashboard → any domain → **Overview** → right sidebar → **Account ID** (a 32‑char hex string). Copy it.

> **টোকেন কোথায় পাবে:** Cloudflare dashboard → My Profile → API Tokens → Create Token → "Edit zone DNS" → Copy. Account ID পাবে যেকোনো domain-এর Overview পেজের ডান পাশে।

**Keep the token secret.** The panel stores it encrypted and never shows it again.

---

## 2. Add the token to the panel

1. Open **WHM** (owner panel) → **Network → Cloudflare** (or **Settings → Cloudflare**). *This page is owner‑only (`server.manage`).*
2. Fill in:
   - **Cloudflare Account ID** — the 32‑char hex from step 1.5.
   - **Cloudflare API Token** — paste the token from step 1.4.
   - **Default DNS Provider** — leave **Existing DNS (PowerDNS)** to keep new domains on PowerDNS by default, or pick **Cloudflare DNS**.
   - **Integration** — leave **Disabled** for now if you want; enable when ready.
3. Click **Save**. The panel encrypts the token (AES‑256‑GCM) and shows only a masked preview like `cfTESTtoken_****1234` — it never echoes the real token back.
4. Click **Test connection**. It calls Cloudflare's read‑only token‑verify endpoint. You should see **Connected**. If it says *Invalid API Token*, re‑check the token permissions.
5. Flip **Integration → Enabled** and **Save** again.

> **Production note:** set `APP_ENCRYPTION_KEY` in `/opt/serverpanel/.env` (a 32‑byte hex/base64 key). Without it the token won't survive a panel restart — same rule as the SMTP password.

---

## 3. Connect a primary domain and get its Cloudflare nameservers

You do this **per primary domain** (apex, e.g. `example.com`). Subdomains do **not** need connecting separately — see §5.

**In the WHM Cloudflare page → "Reconcile (read‑only)" card:**

1. Type the domain (`example.com`) and click **Compare**.
2. If the domain has no Cloudflare zone yet, you'll see a **Connect to Cloudflare** button. Click it.
3. The panel **finds or creates** the zone (it never creates a duplicate) and shows the **assigned Cloudflare nameservers**, e.g.:

   ```
   dana.ns.cloudflare.com
   rob.ns.cloudflare.com
   ```

4. **Point the domain's registrar at these two nameservers.** (At your registrar — GoDaddy, Namecheap, etc. — replace the existing nameservers with the two Cloudflare ones.) Cloudflare then activates the zone (status goes `pending` → `active`, usually minutes to a few hours).

> **Nameserver কই পাবো:** Connect করার পর panel-ই তোমাকে দুইটা Cloudflare nameserver দিবে। ওই দুইটা তোমার registrar-এ (domain কেনার জায়গায়) বসাতে হবে। এটা না করলে Cloudflare zone `active` হবে না।

You can re‑fetch the nameservers any time from the same page (Compare shows them), or via the API (§7).

---

## 4. Do the same for all your primary domains

Repeat §3 for each apex domain. Two ways:

- **One by one:** Compare → Connect on the Cloudflare page.
- **Bulk (recommended once you're comfortable):** the **Sync & live progress** card has **Sync all connected** — but that only *syncs records* for already‑connected zones. To connect many domains at once programmatically, use the API (§7) in a loop, or connect each on the page.

Each connected domain gets its own zone id + nameservers; the **credentials stay centralized** (the one token).

---

## 5. Sync DNS records — primary **and** subdomains

**Important — how subdomains work here:** in this panel a subdomain (e.g. `shop.example.com`) is **not** a separate zone. Its records live **inside the parent zone** (`example.com`). So:

> **তুমি শুধু primary domain sync করবে — ওই domain-এর সব subdomain-এর record এমনিতেই একসাথে Cloudflare-এ চলে যাবে।** আলাদা করে subdomain sync করার দরকার নাই।

**To sync (push local → Cloudflare):**

1. WHM Cloudflare page → **Sync & live progress** card.
2. Click **Sync `example.com`** (single domain) or **Sync all connected** (every Cloudflare‑connected domain).
3. Watch the live progress bar + event stream. It survives a page refresh (progress is stored server‑side). You can **Cancel** a running job.

**What the sync does:**
- **Creates** every local record that's missing in Cloudflare (apex A, `www`, all subdomain A/CNAME, MX, SPF/DKIM/DMARC TXT, etc.).
- **Updates** records whose value changed (and **preserves** your Cloudflare proxied "orange‑cloud" setting — a sync never turns off proxy on a web record).
- **Skips** records already identical (idempotent — running it twice creates nothing).
- **Never proxies or deletes mail records.** MX/SPF/DKIM/DMARC/`mail` A are always DNS‑only.
- **Delete is opt‑in.** By default the sync never deletes anything in Cloudflare. Tick **Apply deletes** (with a confirmation) if you also want to remove Cloudflare records that don't exist locally — even then, mail records are kept.

**The "Reconcile" compare** (read‑only) shows, per record, whether it's **Matched**, **Local only**, **Cloudflare only**, or **Conflict** — so you can review before syncing.

---

## 5b. Auto‑connect, reconcile‑all, per‑domain disable, nameserver status

These make Cloudflare behave "on by default" and manageable per domain.

### Auto‑connect new domains (Cloudflare ON by default)

In **Settings → Cloudflare**, tick **Auto‑connect new domains to Cloudflare** (under Integration; requires the integration enabled). With it on, **every domain added afterwards** — from WHM, the user panel, bulk upload, or the API — is automatically connected to Cloudflare and synced in the background; the operator/user doesn't have to do anything. The domain's Cloudflare nameservers are then available on its page / via the API.

> **ON by default kivabe:** Settings → Cloudflare → **Auto‑connect new domains** on koro. Er por je domain add hobe, seta niজে theke Cloudflare‑e connect + sync hoye jabe. (Default‑e eta **off** rakha, jate purono panel‑e হঠাৎ শত শত zone tori na hoye — on korle tabe hobe.)

### Reconcile existing domains (backfill)

For domains that already existed **before** you added the Cloudflare token, click **Reconcile all (connect + sync)** on the Cloudflare page. It queues a background job that, for every eligible domain, finds/creates its Cloudflare zone and syncs its records — with the same live progress + event stream as a normal sync. Disabled domains are skipped.

### Disable Cloudflare for ONE domain (WHM only)

On the Cloudflare page's compare result, **Disable (this domain)** turns Cloudflare off for that single domain. It:
- **Never** affects any other domain.
- **Never** deletes the Cloudflare zone (zone deletion is a separate, explicit action).
- Makes every auto/bulk sync and the migration repoint **skip** that domain.

**Enable** re‑enables it. (একটা domain disable করলে বাকি সব domain আগের মতোই sync হতে থাকবে।)

### Check nameserver delegation ("Check nameservers")

**Check nameservers** does a **live DNS NS lookup** for the domain and compares it to Cloudflare's assigned nameservers + the Cloudflare zone status, returning a state:

| State | Meaning |
|---|---|
| `not_connected` | No Cloudflare zone yet — connect first |
| `nameserver_update_required` | Registrar not yet pointed at Cloudflare's nameservers |
| `pending_activation` | Nameservers point to Cloudflare; waiting for Cloudflare to activate |
| `active` | Cloudflare is authoritative and serving the zone |
| `paused` | The Cloudflare zone is paused |

Integrators can poll this via the API (§7).

---

## 5c. Proxy web records (orange cloud) — CDN / WAF

By default the panel syncs every record **DNS‑only** (grey cloud) — Cloudflare only answers DNS and traffic goes straight to your server. Turn on **proxying** (orange cloud) to route web traffic through Cloudflare: CDN cache, DDoS/WAF protection, hidden origin IP, and edge SSL.

**Enable it:** WHM → Network → Cloudflare → tick **Proxy web records (orange cloud)** → **Save**. Then **Sync** the domain (or **Reconcile all**) — eligible web records get orange‑clouded.

**What gets proxied:**
- ✅ **A / AAAA / CNAME** for apex, `www`, and subdomains → **Proxied**.
- ❌ **Mail** — MX, SPF, DKIM, DMARC and the `mail` A record → **always DNS‑only** (proxying them breaks mail). Enforced no matter what.
- ❌ **Non‑web types** (MX, TXT, NS, SRV, CAA) → DNS‑only (Cloudflare proxies only A/AAAA/CNAME).

> **অন করা:** Settings → Cloudflare → **Proxy web records (orange cloud)** টিক দাও → Save → domain **Sync** করো। apex/www/subdomain-এর A/AAAA/CNAME orange-cloud হবে; mail record (MX/SPF/DKIM/DMARC/mail) সবসময় DNS-only থাকবে।

**⚠️ SSL mode (গুরুত্বপূর্ণ).** Proxy ON করলে Cloudflare **SSL/TLS → Overview → Full (strict)** সেট করো — origin-এ panel-এর Let's Encrypt cert আছে, তাই Full (strict) ঠিক ও নিরাপদ। **Flexible** দিলে redirect loop হয়।

**বন্ধ করা:** setting untick করে আবার Sync করো — panel আর proxy force করবে না (তখন Cloudflare-এ যা আছে তা preserve করে; দরকার হলে Cloudflare dashboard-এ per-record grey করে দাও)।

**Note:** proxy তখনই কাজ করে যখন domain Cloudflare-এ **active** (nameserver pointed + verified)। তার আগে orange-cloud setting stored থাকে, serving হয় না।

---

## 6. Server migration — Cloudflare records auto‑update

When you migrate a domain/server to a **new IP** (via **Transfer**, or **Server Settings → Reassign IP**):

- The panel carries your **Cloudflare config** (token, re‑encrypted for the new box) and the domains' `cf_zone_id` to the destination.
- After the move, it **repoints the web origin records** — **A (IPv4)** and, on dual‑stack servers, **AAAA (IPv6)** — for apex, `www`, and every subdomain in Cloudflare from the old IP → new IP. Only records whose value equals the **old** server IP are touched; a record deliberately pointing somewhere else is left alone.
- **Mail records are protected** — the `mail` A/AAAA record and SPF `ip4:` are **not** moved by a web‑IP change (mail usually stays on its own box).
- The proxied (orange‑cloud) state of each web record is preserved, and the sweep is **update‑only** (it never deletes a Cloudflare record).
- **Scope:** only domains that are **Cloudflare‑enabled** on the destination are repointed; a per‑domain‑disabled domain (§5b) is skipped. Domains you did **not** migrate are never touched.

> **Migration-এর পর কিছু করতে হবে না:** নতুন server-এ গেলে Cloudflare-এর web record গুলো নতুন IP-তে নিজে থেকেই বসে যাবে, mail record ঠিক থাকবে। (শর্ত: destination panel-এও Cloudflare enabled থাকতে হবে — token migration-এ চলে যায়।)

If the destination is a brand‑new box where the encryption key differs and the token couldn't be re‑keyed, the panel drops the token and asks you to **re‑enter it** on Settings → Cloudflare; then a re‑sync/reassign repoints everything.

---

## 7. Do it via the bpanel API (for resellers / automation)

If you add domains through the **bpanel API token** (the panel's programmatic API), you can connect + fetch nameservers programmatically. This is the flow to give a customer their nameservers automatically.

### 7.1 Create a bpanel API token with Cloudflare scopes

WHM → **Developer → API & Webhooks → Create token**, and include the scopes:
- `domain:write` (to create domains), `domain:read`
- **`cloudflare:read`** — read a domain's zone status + nameservers
- **`cloudflare:write`** — connect a domain to Cloudflare

Copy the returned `plaintext_token` (starts with `btz_…`) — shown once.

### 7.2 The calls (all `Authorization: Bearer btz_…`)

```bash
BASE="https://panel.yourhost.com"
TOK="btz_dev_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

# 1) Add the domain (if not already)
curl -X POST "$BASE/api/v1/external/domains" \
  -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -d '{"domain":"example.com","user":"customer1","php_version":"8.1"}'

# 2) Connect it to Cloudflare (find-or-create zone) → returns nameservers
curl -X POST "$BASE/api/v1/external/cloudflare/example.com/connect" \
  -H "Authorization: Bearer $TOK"
# → {"success":true,"data":{"domain":"example.com","zone_id":"...","status":"pending",
#     "nameservers":["dana.ns.cloudflare.com","rob.ns.cloudflare.com"],"created":true}}

# 3) Get the nameservers any time (to show the customer for their registrar)
curl "$BASE/api/v1/external/cloudflare/example.com/nameservers" \
  -H "Authorization: Bearer $TOK"
# → {"success":true,"data":{"domain":"example.com","status":"pending",
#     "nameservers":["dana.ns.cloudflare.com","rob.ns.cloudflare.com"]}}

# 4) Check status later (connected? active yet?)
curl "$BASE/api/v1/external/cloudflare/example.com" \
  -H "Authorization: Bearer $TOK"
# → {"success":true,"data":{"connected":true,"status":"active","nameservers":[...]}}

# 5) Live delegation check — has the registrar been pointed at Cloudflare yet?
curl "$BASE/api/v1/external/cloudflare/example.com/nameserver-status" \
  -H "Authorization: Bearer $TOK"
# → {"success":true,"data":{"state":"nameserver_update_required",
#     "cf_nameservers":["dana.ns.cloudflare.com","rob.ns.cloudflare.com"],
#     "current_nameservers":["ns1.oldhost.com","ns2.oldhost.com"]}}
# state ∈ not_connected | nameserver_update_required | pending_activation | active | paused
```

- **404** on `/nameservers` means the domain isn't connected yet — call `/connect` first.
- **400** means Cloudflare isn't enabled on the panel (owner must set it up in §2).
- **403** means the token lacks the scope, or the token's tenant doesn't own that domain.

> DNS record push (sync) is currently an owner/WHM operation; the API surface covers **connect + nameservers + status**, which is the reseller flow. (Full external sync can be added later if needed.)

Full spec: `docs/api/openapi.yaml` and the human reference `docs/postman/API-Reference.md` (§8b).

---

## 8. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| **Test connection → "Invalid API Token"** | Token missing **Zone:DNS:Edit** + **Zone:Zone:Read**, or it expired. Re‑create it (§1). |
| **Connect → error, can't create zone** | Token can't create zones — add **Account:Read** (or use a token scoped to your account with zone‑create). |
| **Zone stays `pending`** | You haven't pointed the registrar at the Cloudflare nameservers yet (§3.4), or DNS is still propagating. |
| **A subdomain didn't sync** | Make sure it exists as a record under the **parent** zone in the panel's DNS, then re‑sync the **primary** domain (§5). |
| **Mail broke after connecting** | It shouldn't — mail records are DNS‑only and untouched. Check MX/SPF still point at your mail host; never orange‑cloud MX or `mail`. |
| **Token gone after panel restart** | `APP_ENCRYPTION_KEY` not set in `/opt/serverpanel/.env`. Set it, re‑enter the token. |
| **After migration, Cloudflare didn't update** | The destination panel needs Cloudflare **enabled** (token carried over, or re‑entered). Then run **Reassign IP** / re‑sync. |

---

## 9. Safety summary

- Cloudflare API token: **encrypted at rest**, **never** returned by any API, **owner‑only** to configure.
- **Deletes are gated:** the WHM record‑delete needs an explicit confirm; the bulk sync only deletes with **Apply deletes** ticked; **mail records are never deleted**.
- **No duplicates:** connect is find‑first; records are matched by Cloudflare record id + value, so re‑syncing is safe.
- **Server identity is IP‑independent** (a stable `server_id` UUID), so migrations/IP changes never lose track of a server or its Cloudflare mapping.

---

*Questions or a flow you want automated end‑to‑end (e.g. connect‑all + sync‑all in one API call)? That's a small addition — ask and it can be wired in.*
