// Vendor-facing "How to use the panel" guide. Single-page, left-nav
// + content-pane layout — same shape as a typical docs site so a new
// vendor can scan the table of contents and click into the topic
// they need. Renders entirely client-side from a static content
// array; no API calls, no auth-scoped data.
//
// Every section maps to a real panel surface and links to it with a
// React Router <Link>, so "Open Domains" actually deep-links to
// /domains. The links double as a navigation crutch for vendors who
// don't yet know the sidebar layout.

import { useState, useMemo } from "react";
import { Link } from "react-router-dom";
import { Card } from "@serverpanel/ui";
import {
  Rocket,
  Globe,
  Globe2,
  Mail,
  Database,
  ShieldCheck,
  FileCode2,
  FolderOpen,
  Archive,
  Users,
  KeyRound,
  HelpCircle,
  ChevronRight,
  Search,
  ExternalLink,
  Terminal,
} from "lucide-react";

type Step = string | { kind: "code"; value: string } | { kind: "note"; value: string };

interface DocSection {
  id: string;
  label: string;
  icon: React.ReactNode;
  intro: string;
  // Each block is a single self-contained "how to" subhead. Keep
  // them short — vendors skim, they don't read top-to-bottom.
  blocks: {
    title: string;
    body: Step[];
    link?: { label: string; to: string };
  }[];
}

const sections: DocSection[] = [
  {
    id: "getting-started",
    label: "Getting started",
    icon: <Rocket size={16} />,
    intro:
      "Your panel hosts websites, email, databases, and apps on a single server. Everything you need is in the left sidebar. Use this guide to find the right page for what you want to do.",
    blocks: [
      {
        title: "Sign in",
        body: [
          "Go to your panel URL (the address bar above).",
          "Enter your email and password — your panel administrator created this account for you.",
          "Forgot your password? Click 'Forgot password?' on the login page to email yourself a reset link.",
          { kind: "note", value: "You can also sign in with a one-time code mailed to your address — pick 'Sign in with a code' on the login page. Useful if you don't remember your password and don't want to reset it." },
        ],
      },
      {
        title: "What the dashboard shows",
        body: [
          "The Dashboard lists what you currently have: domains, applications, mailboxes, databases. Each tile is a shortcut to that section.",
          "Resource usage (disk, bandwidth, mail storage) is at the top — if any bar turns amber/red, your hosting package's quota is close to its limit. Talk to your administrator about a bigger package.",
        ],
        link: { label: "Open the dashboard", to: "/dashboard" },
      },
      {
        title: "Change your password",
        body: [
          "Click 'My Profile' in the sidebar.",
          "Scroll to the 'Change password' card.",
          "Enter your current password once and the new password twice. Minimum length is 8 characters.",
          { kind: "note", value: "After changing the password your other browser sessions stay signed in. Go to 'My Profile → Sessions' if you want to kick all other devices off." },
        ],
        link: { label: "Open My Profile", to: "/profile" },
      },
    ],
  },
  {
    id: "domains",
    label: "Domains",
    icon: <Globe size={16} />,
    intro:
      "A domain is the public name a website answers on (example.com, shop.example.com, etc.). Add the domain here first; every other feature (email, SSL, deploy) attaches to it.",
    blocks: [
      {
        title: "Add a new domain",
        body: [
          "Click 'My Domains' in the sidebar, then '+ Add Domain'.",
          "Enter the domain (without https:// or trailing slashes). Pick the PHP version if you're hosting PHP/WordPress; leave default for static or Deploy Software apps.",
          "Submit. The panel creates a vhost, a document root under your home directory, and starts the SSL pipeline.",
          { kind: "note", value: "Before the panel can serve traffic for your domain, you need to point its DNS at this server's IP — see the DNS section below. Until DNS resolves, the SSL certificate cannot be issued automatically." },
        ],
        link: { label: "Open My Domains", to: "/domains" },
      },
      {
        title: "Set the DNS at your registrar",
        body: [
          "At the registrar where you bought the domain (GoDaddy, Namecheap, Hostinger, …), find 'DNS' / 'Nameservers' / 'DNS records'.",
          { kind: "note", value: "Easiest: use your registrar's default nameservers and add a single A record pointing the apex (@) and www to the server IP shown at the top of My Domains. The IP also appears in the chip on every page header." },
          "Or: point the domain's nameservers at the panel's nameservers if your administrator gave you a list. This delegates the whole zone to the panel and you'll manage DNS under the panel's DNS page.",
          "DNS changes take up to a few hours to propagate. The SSL section retries automatically once your domain resolves.",
        ],
      },
      {
        title: "Remove a domain",
        body: [
          "Open My Domains, click the trash icon on the row.",
          { kind: "note", value: "Removing a domain unlinks emails, SSL, and the vhost. Files in the home directory are preserved — you can re-add the domain later and the original files reappear." },
        ],
      },
    ],
  },
  {
    id: "dns",
    label: "DNS",
    icon: <Globe2 size={16} />,
    intro:
      "DNS records tell the internet where to find your services (web on a server, email at a provider, etc.). The panel manages DNS for domains whose nameservers point at it. If you use a third-party DNS (Cloudflare, registrar DNS), make the same changes there instead.",
    blocks: [
      {
        title: "View and edit a zone",
        body: [
          "Click 'DNS' in the sidebar, pick the domain.",
          "Each row is one record: name, type (A / AAAA / MX / CNAME / TXT / NS / SRV), value, TTL.",
          "Hit 'Add Record' to add a row, pencil icon to edit, trash to delete. 'Save All' commits multiple changes in one click.",
          { kind: "note", value: "Lower TTL during a migration (300 = 5 minutes) so changes propagate fast. Raise it back to 3600 once stable so resolvers cache the value." },
        ],
        link: { label: "Open DNS", to: "/dns" },
      },
      {
        title: "Common records",
        body: [
          { kind: "code", value: "A      @      <server-ip>      3600   # apex (example.com)\nA      www    <server-ip>      3600   # www subdomain\nMX     @      mail.example.com 10     # incoming mail\nTXT    @      v=spf1 a mx ~all 3600   # SPF (anti-spoof)" },
          "Replace <server-ip> with the IP at the top of My Domains. The MX value points to a mailserver you control (often itself).",
        ],
      },
    ],
  },
  {
    id: "ssl",
    label: "SSL / HTTPS",
    icon: <ShieldCheck size={16} />,
    intro:
      "Every domain hosted by the panel gets a free Let's Encrypt certificate automatically once DNS resolves to the server. You don't normally need to touch this page — but here's where to look when something's off.",
    blocks: [
      {
        title: "Issue / re-issue a certificate",
        body: [
          "Open 'SSL/TLS' from the sidebar.",
          "Find the row for your domain. The badge shows 'Active' (valid), 'Expiring' (renew soon), or 'Missing' (no cert yet).",
          "Click 'Issue' on a Missing row, or 'Renew' to force a fresh cert before expiry.",
          { kind: "note", value: "If the issue fails, the most common reason is DNS — the domain isn't pointing at this server. Fix the A record, then click Issue again. Let's Encrypt rate-limits to 5 failures per hour per domain — wait an hour if you've retried a lot." },
        ],
        link: { label: "Open SSL/TLS", to: "/ssl" },
      },
      {
        title: "Wildcard certs",
        body: [
          "Wildcards (*.example.com) require DNS-01 verification — the SSL page prompts you to add a TXT record to your DNS during issuance.",
          "Once the TXT record propagates, click 'Continue' on the SSL page and the cert is issued.",
        ],
      },
    ],
  },
  {
    id: "email",
    label: "Email",
    icon: <Mail size={16} />,
    intro:
      "Create mailboxes under your domains, set autoresponders, forward to other addresses, and read mail in the bundled webmail.",
    blocks: [
      {
        title: "Create a mailbox",
        body: [
          "Click 'Email' in the sidebar, pick the domain, hit '+ Add mailbox'.",
          "Set the local part (e.g. 'sales' → sales@example.com), choose a password (8+ chars), pick a storage quota in MB.",
          "Click Create. Test the password by clicking the webmail icon — it opens Roundcube prefilled.",
          { kind: "note", value: "Before mail flows in, your domain needs an MX record pointing at this server (see DNS section). The panel auto-suggests it when you create the first mailbox." },
        ],
        link: { label: "Open Email", to: "/email" },
      },
      {
        title: "Forward / autoresponder",
        body: [
          "Open the mailbox row, click the cog icon.",
          "'Forwarders' lists addresses every incoming mail is copied to. Add an external email to bounce mail elsewhere without keeping a copy here.",
          "'Autoresponder' is the vacation message — toggle on, set start/end dates, the panel mails the reply automatically.",
        ],
      },
      {
        title: "Webmail",
        body: [
          "Each mailbox row has a webmail icon — open it to log into Roundcube with the mailbox credentials.",
          "Send / receive / search work like any other webmail client. Drafts and Sent folders are server-side, so they sync between webmail and IMAP clients.",
        ],
      },
    ],
  },
  {
    id: "databases",
    label: "Databases",
    icon: <Database size={16} />,
    intro:
      "Create MySQL or MongoDB databases for your apps. The panel hands you a connection string and CLI command ready to copy.",
    blocks: [
      {
        title: "Create a database",
        body: [
          "Click 'Databases' in the sidebar, hit '+ Create Database'.",
          "Enter a short name (e.g. 'shop'); the panel prefixes it with your vendor name automatically.",
          "Pick MySQL or MongoDB.",
          "Set a username (a separate prefixed name) and a strong password (Generate button picks one for you).",
          "Click Create. The Connection card shows host, port, database name, user, password, and a one-line connection URL you can paste into your app's .env.",
          { kind: "note", value: "The Domain field on the create form is optional — it just groups the db on the dashboard. Leave it blank if the db isn't tied to one specific website." },
        ],
        link: { label: "Open Databases", to: "/databases" },
      },
      {
        title: "Extra DB users",
        body: [
          "Each database can have additional users with limited roles (read-only, read-write, dbAdmin for MongoDB; SELECT / ALL for MySQL).",
          "Open the database row, click the users icon, '+ Add User'.",
          "Drop a user with the trash icon. The original owner user can't be removed — delete the database instead.",
        ],
      },
      {
        title: "External access",
        body: [
          "The Connection URL uses your server's public IP / hostname so it works from outside the box.",
          { kind: "note", value: "If you connect from your laptop or a different server and it refuses the connection, your administrator may need to open the database port in the firewall and bind the database to 0.0.0.0 — by default both MySQL and MongoDB only listen on localhost." },
        ],
      },
    ],
  },
  {
    id: "deploy-software",
    label: "Deploy Software",
    icon: <Rocket size={16} />,
    intro:
      "Push code from GitHub straight onto this server. One project can run multiple services (frontend + backend), each on its own port. Serve a service on its own domain, attach it to one or more domains, or run it port-only with no public domain and attach one later.",
    blocks: [
      {
        title: "Create a project",
        body: [
          "Click 'Deploy Software' in the sidebar.",
          "Hit '+ Project'. Enter a name, a slug, the GitHub repo URL, and a Personal Access Token if the repo is private.",
          "Toggle 'Auto-deploy on push' if you want every git push to trigger a redeploy via the webhook the panel mints for you.",
          { kind: "note", value: "The webhook URL + secret appear in the project drawer after creation — paste them into your repo's Settings → Webhooks → Add webhook on GitHub." },
        ],
        link: { label: "Open Deploy Software", to: "/deploy-software" },
      },
      {
        title: "Add a service",
        body: [
          "Open the project, click '+ Add Service'.",
          "Pick a framework preset (Next.js, NestJS, Node, Go, Python, Vue, Nuxt, plain static, …). The preset fills in install / build / start commands.",
          "Optionally pick a primary domain (one you've added under My Domains) — the service then gets its own nginx vhost and a Let’s Encrypt cert on it.",
          { kind: "note", value: "A domain is no longer required. Leave the primary blank to run the service port-only — it listens on 127.0.0.1:PORT with no public vhost or SSL (reachable locally or over an SSH tunnel), and you can attach a domain later. You can also skip the primary and attach one or more domains below." },
          "Add alias domains in the same form if the service should also answer on extra names (one nginx vhost serves all of them with a shared SAN cert).",
          "Set the port for backend / fullstack services. Frontend / static services don't need one.",
          "Click Add. The panel clones the repo, runs install + build, writes a systemd unit, writes an nginx vhost, requests Let's Encrypt — then shows you live deploy progress.",
        ],
      },
      {
        title: "Edit a service",
        body: [
          "On the service row click the pencil icon. The Edit modal shows every field you set at creation — including primary domain and aliases.",
          "Change the primary domain to a new one and save: the old vhost file is removed, the new vhost is written with the alias list intact, and a fresh SAN cert is issued under the new --cert-name.",
          "You can also clear the primary domain entirely and save — the public vhost and its Let’s Encrypt cert are removed and the service reverts to port-only (reachable on 127.0.0.1:PORT / via an SSH tunnel). Attach a domain again any time from this same modal.",
          "Add / remove aliases in the same form. The list you submit on Save replaces the existing aliases in one go.",
        ],
      },
      {
        title: "Redeploy / start / stop",
        body: [
          "Each service row has buttons: Redeploy (rebuild + restart), Start, Stop, Restart, Logs.",
          "Redeploy fetches the latest commit on the configured branch, re-runs install + build, then restarts the systemd unit.",
          "If a deploy fails, the row shows the failed step; click the error badge for the full log. Common causes: missing env vars, build script crashes, port already in use.",
        ],
      },
      {
        title: "Environment variables",
        body: [
          "Edit a service, scroll to 'Environment variables'. Add KEY=value rows.",
          "On save, the panel writes them to .env in the install directory AND injects them into the systemd unit's Environment= lines, so both build-time and runtime have access.",
          { kind: "note", value: "Restarting the service picks up env-var changes; build-time changes need a Redeploy because they're baked into the build output." },
        ],
      },
    ],
  },
  {
    id: "wordpress",
    label: "WordPress",
    icon: <FileCode2 size={16} />,
    intro:
      "One-click WordPress installs. The panel sets up the PHP-FPM pool, creates the database, downloads WP, and runs the installer with credentials you supply.",
    blocks: [
      {
        title: "Install WordPress",
        body: [
          "Click 'WordPress' in the sidebar, then '+ Install'.",
          "Pick the target domain, set the admin username + password + email.",
          "Click Install. The panel creates the database, downloads core, writes wp-config.php, and you can hit the site immediately.",
        ],
        link: { label: "Open WordPress", to: "/wordpress" },
      },
      {
        title: "Manage an existing site",
        body: [
          "The WordPress page lists every install with quick actions: Open admin (auto-login), Clone, Backup, Delete.",
          "Auto-login uses a single-use signed URL — no need to remember the wp-admin password from the install screen.",
        ],
      },
    ],
  },
  {
    id: "files",
    label: "File Manager",
    icon: <FolderOpen size={16} />,
    intro:
      "Browse, edit, upload, and extract files inside your home directory. Same access an SFTP client gives you, in the browser.",
    blocks: [
      {
        title: "Upload files",
        body: [
          "Open 'File Manager', navigate to the destination folder.",
          "Drag & drop into the browser, or click the upload icon and pick files. Multiple files at once work.",
          { kind: "note", value: "ZIP / TAR archives can be extracted in place — right-click the archive → Extract." },
        ],
        link: { label: "Open File Manager", to: "/files" },
      },
      {
        title: "Edit code in the browser",
        body: [
          "Click any text file (HTML, JS, PHP, conf, …). The built-in editor opens with syntax highlighting.",
          "Ctrl/Cmd+S saves. The editor warns if you're about to overwrite a file changed since you opened it.",
        ],
      },
    ],
  },
  {
    id: "terminal",
    label: "Terminal",
    icon: <Terminal size={16} />,
    intro:
      "Browser shell into the server, scoped to your user where applicable. Useful for one-off commands when the GUI doesn't have a button for what you want.",
    blocks: [
      {
        title: "Open a shell",
        body: [
          "Click 'Terminal' in the sidebar.",
          "Pick the target (your home directory shell, a specific project's install dir, …) — the dropdown lists the contexts available to your role.",
          { kind: "note", value: "If 'Terminal' is missing from your sidebar, your administrator hasn't enabled shell access for your role. Talk to them — they can grant it on the Shell Access page (admin side)." },
        ],
        link: { label: "Open Terminal", to: "/terminal" },
      },
    ],
  },
  {
    id: "backups",
    label: "Backups",
    icon: <Archive size={16} />,
    intro:
      "Take backups of files, databases, and email — manually or on a schedule. Restore to recover from a bad change or migrate to another server.",
    blocks: [
      {
        title: "Take a manual backup",
        body: [
          "Click 'Backups' in the sidebar, then '+ Create Backup'.",
          "Pick what to include: files, databases, email, DNS zones.",
          "Click Create. The job streams progress; finished archives appear in the list with size + age.",
          { kind: "note", value: "Backups are encrypted with a server-side key — only the panel can decrypt them. Download a copy off-server for disaster-recovery insurance." },
        ],
        link: { label: "Open Backups", to: "/backups" },
      },
      {
        title: "Restore",
        body: [
          "Find the archive, click Restore. The panel previews what will be overwritten before committing.",
          "Confirm. Files / databases / mail are written back; the panel restarts dependent services so you don't have to.",
        ],
      },
    ],
  },
  {
    id: "team",
    label: "Team / sub-users",
    icon: <Users size={16} />,
    intro:
      "Tenant admins can create staff / developer / support / customer accounts that share the tenant's resources. Each role has a different permission set the panel enforces server-side.",
    blocks: [
      {
        title: "Create a team member",
        body: [
          "Click 'Team' in the sidebar (only visible if your role can create users).",
          "Hit '+ Add User', enter name + email + password + role.",
          "Submit. The new user can log in immediately with the email they were registered under.",
          { kind: "note", value: "Roles: staff (most admin perms, no billing), developer (Deploy Software + DNS + files), support (read-only on most surfaces), customer (single end-user with isolated resources)." },
        ],
        link: { label: "Open Team", to: "/team" },
      },
      {
        title: "Change a member's role / password",
        body: [
          "Click the pencil on the user row. Rotate password / update name / change role / disable account.",
          "Disabled accounts can't sign in but their data is preserved. Re-enable any time.",
        ],
      },
    ],
  },
  {
    id: "security",
    label: "Security",
    icon: <KeyRound size={16} />,
    intro:
      "Keep your account safe: rotate the password, review active sessions, opt for OTP login.",
    blocks: [
      {
        title: "Review your sessions",
        body: [
          "Open 'My Profile → Sessions'. Each row shows device, IP, geo, and last activity.",
          "Click 'Revoke' on a session you don't recognise. The targeted browser is signed out within seconds.",
        ],
        link: { label: "Open Sessions", to: "/sessions" },
      },
      {
        title: "Use one-time codes instead of a password",
        body: [
          "On the login page, click 'Sign in with a code'.",
          "Enter your email — a code arrives by mail. Enter it to sign in.",
          "Bonus: when you click the magic-link in the email from a different browser, the original tab finishes logging in automatically — no copy-pasting the code.",
        ],
      },
    ],
  },
  {
    id: "faq",
    label: "FAQ",
    icon: <HelpCircle size={16} />,
    intro: "Quick answers to questions vendors ask most often.",
    blocks: [
      {
        title: "My SSL says 'Issuance failed'",
        body: [
          "99% of the time it's DNS — the domain isn't pointing at this server yet, or the A record was changed in the last few minutes and hasn't propagated.",
          "Check the current A record at https://dnschecker.org and confirm it matches the server IP shown on My Domains.",
          "Once propagated, click 'Issue' again on the SSL page. Let's Encrypt rate-limits five failures per hour per domain — if you've burned through that, wait an hour.",
        ],
      },
      {
        title: "Webhook fired but nothing happened",
        body: [
          "Open Deploy Software → the project. The 'Last delivery' badge tells you whether the most recent push reached the panel.",
          "If green, the deploy ran (check Recent deployments). If amber, GitHub couldn't reach the panel — verify the Payload URL + Secret on GitHub's Webhooks page match what the panel shows.",
          "Auto-deploy is per-project — if the toggle is off, pushes are received but no deploy is queued.",
        ],
      },
      {
        title: "My emails go to spam",
        body: [
          "Three records make external providers trust your mail: SPF (v=spf1 a mx ~all TXT at apex), DKIM (per-domain key the panel publishes for you on the Email page), and DMARC (a TXT record at _dmarc).",
          "Open Email → the domain → 'Mail authentication' card. The panel lists which records are missing and shows exact values to paste into DNS.",
        ],
      },
      {
        title: "I lost my password",
        body: [
          "Click 'Forgot password?' on the login page. Enter your email — a reset link arrives by mail. Click it to set a new password.",
          "If your account doesn't have email yet (or the reset mail doesn't arrive), your administrator can rotate it for you from the WHM Vendors / Users page.",
        ],
      },
    ],
  },
];

const sectionByID = (id: string) => sections.find((s) => s.id === id) || sections[0];

export default function HelpPage() {
  const [activeID, setActiveID] = useState<string>(sections[0].id);
  const [query, setQuery] = useState("");

  const filtered = useMemo(() => {
    if (!query.trim()) return sections;
    const q = query.trim().toLowerCase();
    return sections.filter((s) => {
      if (s.label.toLowerCase().includes(q)) return true;
      if (s.intro.toLowerCase().includes(q)) return true;
      return s.blocks.some((b) => {
        if (b.title.toLowerCase().includes(q)) return true;
        return b.body.some((step) => {
          if (typeof step === "string") return step.toLowerCase().includes(q);
          return step.value.toLowerCase().includes(q);
        });
      });
    });
  }, [query]);

  const active = sectionByID(activeID);

  return (
    <div className="max-w-7xl mx-auto p-4 lg:p-6 space-y-4">
      <Card>
        <div className="p-4 border-b border-panel-border flex flex-col gap-2 md:flex-row md:items-center md:justify-between">
          <div>
            <h1 className="text-lg font-semibold text-panel-text flex items-center gap-2">
              <HelpCircle size={18} className="text-blue-400" /> How to use the panel
            </h1>
            <p className="text-xs text-panel-muted mt-0.5">
              Vendor guide — pick a topic on the left, or search across every section.
            </p>
          </div>
          <div className="relative">
            <Search
              size={14}
              className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted/60 pointer-events-none"
            />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search guides…"
              className="w-full md:w-64 pl-9 pr-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-[14rem_1fr]">
          {/* Left nav — sticky on desktop so it stays visible while
              scrolling long sections like Deploy Software. */}
          <aside className="border-b lg:border-b-0 lg:border-r border-panel-border p-2 lg:p-3 lg:sticky lg:top-0 lg:max-h-[80vh] lg:overflow-y-auto">
            <nav className="flex lg:flex-col flex-wrap gap-1">
              {filtered.map((s) => (
                <button
                  key={s.id}
                  onClick={() => setActiveID(s.id)}
                  className={`flex items-center gap-2 px-2 py-1.5 text-xs rounded-md transition-colors text-left ${
                    activeID === s.id
                      ? "bg-blue-500/10 text-panel-text border border-blue-500/30"
                      : "text-panel-muted hover:text-panel-text hover:bg-panel-bg border border-transparent"
                  }`}
                >
                  <span className="text-blue-400">{s.icon}</span>
                  <span className="flex-1">{s.label}</span>
                  <ChevronRight size={12} className="opacity-40" />
                </button>
              ))}
              {filtered.length === 0 && (
                <div className="text-[11px] text-panel-muted px-2 py-1">No sections match "{query}".</div>
              )}
            </nav>
          </aside>

          {/* Content pane. */}
          <main className="p-4 lg:p-6 space-y-5">
            <div>
              <h2 className="text-base font-semibold text-panel-text flex items-center gap-2 mb-1">
                <span className="text-blue-400">{active.icon}</span>
                {active.label}
              </h2>
              <p className="text-sm text-panel-muted">{active.intro}</p>
            </div>

            <div className="space-y-4">
              {active.blocks.map((b, i) => (
                <div key={i} className="border border-panel-border rounded-lg p-3 bg-panel-bg/30">
                  <h3 className="text-sm font-medium text-panel-text mb-2">{b.title}</h3>
                  <ol className="space-y-1.5 text-xs text-panel-muted list-decimal list-inside">
                    {b.body.map((step, j) => {
                      if (typeof step === "string") {
                        return <li key={j} className="leading-relaxed">{step}</li>;
                      }
                      if (step.kind === "code") {
                        return (
                          <li key={j} className="list-none">
                            <pre className="my-1 px-3 py-2 bg-panel-bg border border-panel-border rounded text-[11px] text-panel-text font-mono whitespace-pre-wrap leading-relaxed">
                              {step.value}
                            </pre>
                          </li>
                        );
                      }
                      // note
                      return (
                        <li key={j} className="list-none">
                          <div className="my-1 px-3 py-2 bg-blue-500/5 border border-blue-500/20 rounded text-[11px] text-panel-text leading-relaxed">
                            <b className="text-blue-400">Note:</b> {step.value}
                          </div>
                        </li>
                      );
                    })}
                  </ol>
                  {b.link && (
                    <div className="mt-2">
                      <Link
                        to={b.link.to}
                        className="inline-flex items-center gap-1 text-[11px] text-blue-400 hover:text-blue-300"
                      >
                        {b.link.label} <ExternalLink size={11} />
                      </Link>
                    </div>
                  )}
                </div>
              ))}
            </div>

            <div className="border-t border-panel-border pt-3 text-[11px] text-panel-muted">
              Don't see what you need? Open the relevant section's page from the sidebar — most surfaces have a small <code className="px-1 py-0.5 rounded bg-panel-bg border border-panel-border">?</code> icon next to each field with field-level help.
            </div>
          </main>
        </div>
      </Card>
    </div>
  );
}
