// Owner-facing "How to use WHM" guide. Same shape as the cpanel
// HelpPage (left nav + content pane + search) but the content
// targets the panel owner, not vendors. Covers tasks the owner
// reaches from the WHM sidebar: managing vendors, server settings,
// software installs, transfers, monitoring.

import { useState, useMemo } from "react";
import { Link } from "react-router-dom";
import { Card } from "@serverpanel/ui";
import {
  Rocket,
  Users,
  Box,
  Server,
  Settings,
  ShieldCheck,
  Activity,
  Archive,
  Globe2,
  Mail,
  HardDrive,
  HelpCircle,
  ChevronRight,
  Search,
  ExternalLink,
  Terminal,
  Wrench,
} from "lucide-react";

type Step = string | { kind: "code"; value: string } | { kind: "note"; value: string };

interface DocSection {
  id: string;
  label: string;
  icon: React.ReactNode;
  intro: string;
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
      "WHM is the owner-side panel. From here you create vendors, set their resource quotas, install runtimes, and run the host-level admin tasks the vendor panel doesn't expose. This guide walks the common owner workflows in the order most operators do them.",
    blocks: [
      {
        title: "First-time setup checklist",
        body: [
          "Confirm the panel is reachable via the panel.<your-domain> URL you set during install. The top-bar shows the server IP — that's the value DNS for panel.<your-domain> must point at.",
          "Visit Server Settings → Hostname and confirm the FQDN is what you want mail headers to advertise.",
          "Visit Configuration → Branding (if available) to set the panel name and logo your vendors see.",
          "Visit Software and pick a default Node / Go / Python / PHP version. Vendors who don't override pick this up.",
          "Create your first hosting Package (Packages page) before adding a Vendor — vendors are assigned a package on creation.",
          { kind: "note", value: "Most server-level options require the server.manage permission. The default vendor_owner has it; staff need it granted via Users & RBAC." },
        ],
      },
      {
        title: "Sign in as a vendor for testing",
        body: [
          "Open Vendors → click the vendor row → 'Impersonate'.",
          "A new tab opens already signed in as that vendor in /user-panel. Useful for verifying their package limits actually take effect.",
          "Close the tab when done — impersonation sessions are scoped and don't persist after sign-out.",
        ],
        link: { label: "Open Vendors", to: "/vendors" },
      },
    ],
  },
  {
    id: "vendors",
    label: "Vendors",
    icon: <Users size={16} />,
    intro:
      "A vendor is a tenant — a person or company that hosts websites on your server. Each vendor has its own home directory, mailboxes, databases, and team. Quotas come from the package you assign.",
    blocks: [
      {
        title: "Create a vendor",
        body: [
          "Open Vendors → '+ Add Vendor'.",
          "Username (used in the linux user + db prefix), email (unique across the whole panel), password (8+ chars), package (pick from the list).",
          "Submit. The panel creates the linux user, home directory, dovecot mail dir, default mongo+mysql prefixes, and a tenant row.",
          { kind: "note", value: "Email is GLOBALLY UNIQUE in this panel — across every vendor, their team members, and customer accounts. If creation fails with 'email already in use', that address belongs to someone else's tenant somewhere on this server." },
        ],
        link: { label: "Open Vendors", to: "/vendors" },
      },
      {
        title: "Suspend / unsuspend",
        body: [
          "Click the vendor row, hit 'Suspend'. Their domains stop answering, mailboxes reject incoming mail, the vendor can't sign into the panel.",
          "Files on disk are preserved — unsuspending restores everything.",
          "Use this when payment lapses or you need to freeze an account during an investigation. Use Delete only when you're certain you won't restore it.",
        ],
      },
      {
        title: "Change a vendor's package",
        body: [
          "Vendor row → 'Change package' dropdown.",
          "The new quotas take effect immediately for new resources. Existing resources over the new quota stay, but the vendor can't grow further until they're under the cap.",
        ],
      },
    ],
  },
  {
    id: "packages",
    label: "Packages",
    icon: <Box size={16} />,
    intro:
      "Resource bundles you assign to vendors. Disk, bandwidth, mailbox count, db count, max domains — every quota the panel enforces lives here.",
    blocks: [
      {
        title: "Create a package",
        body: [
          "Packages → '+ Add Package'. Name it descriptively (Starter, Business, Pro).",
          "Set each quota. Leave a field at 0 / blank for 'unlimited' (depends on the field; the form labels which is which).",
          "Submit. The package is available to assign to vendors immediately.",
        ],
        link: { label: "Open Packages", to: "/packages" },
      },
      {
        title: "Edit a package",
        body: [
          "Editing a package updates quotas for every vendor on it.",
          "Vendors over the new quota get flagged on the dashboard — they can't grow further but their existing resources keep working until they free space.",
        ],
      },
    ],
  },
  {
    id: "server-settings",
    label: "Server Settings",
    icon: <Settings size={16} />,
    intro:
      "Host-level configuration: hostname, branding, mail relay, default package, system tweaks. Most options here change the whole panel's behaviour for every vendor.",
    blocks: [
      {
        title: "Change the panel hostname",
        body: [
          "Server Settings → Change Hostname. Enter the new FQDN (panel.example.com).",
          "The panel rewrites mail headers, /etc/hostname, and the public URL it advertises in OTP links.",
          { kind: "note", value: "Make sure the new hostname has DNS pointing at this server BEFORE you change it — the SSL renewal next runs against the new name." },
        ],
        link: { label: "Open Change Hostname", to: "/change-hostname" },
      },
      {
        title: "Outbound mail (panel notifications)",
        body: [
          "Server Settings → Mail. By default the panel uses the local Postfix instance (auto-configured at install).",
          "For higher deliverability point it at an external SMTP relay (SendGrid, AWS SES). Set host, port, username, password — the panel tests the connection before saving.",
          { kind: "note", value: "Mail-issues page shows the last 50 outbound mail delivery attempts. Useful when a vendor reports the password-reset email never arrived." },
        ],
        link: { label: "Open Server Settings", to: "/server-settings" },
      },
      {
        title: "Branding",
        body: [
          "Server Settings → Branding (or Configuration → Branding depending on the build). Upload a logo, set the product name, choose a brand colour.",
          "Both WHM and user-panel pick the branding up on the next page load — no restart needed.",
        ],
      },
    ],
  },
  {
    id: "software",
    label: "Software / runtimes",
    icon: <HardDrive size={16} />,
    intro:
      "Install Node.js, Go, Python, Ruby, PHP versions side-by-side. Set one as the default per language so Deploy Software picks it up for new services.",
    blocks: [
      {
        title: "Install a new runtime version",
        body: [
          "Software page → pick the language → '+ Install version'.",
          "Pick a version from the list (LTS / current / older). Install runs in the background; progress appears on the row.",
          { kind: "note", value: "Install is non-blocking — multiple versions can install in parallel. The panel won't let you install over a running install for the same version." },
        ],
        link: { label: "Open Software", to: "/software" },
      },
      {
        title: "Set the default version",
        body: [
          "Click the radio next to a version. New Deploy Software services with empty runtime_version pick this up.",
          "Existing services aren't affected — they keep the version chosen at deploy time. Vendors can rebuild against the new default by clearing runtime_version on the Edit modal and redeploying.",
        ],
      },
      {
        title: "Uninstall a version",
        body: [
          "The trash icon next to a version disables uninstall if any service references it — you'd orphan running processes.",
          "Find which services use the version: the warning tooltip lists them. Migrate those services first.",
        ],
      },
    ],
  },
  {
    id: "deploy-software",
    label: "Deploy Software (owner view)",
    icon: <Rocket size={16} />,
    intro:
      "The same Deploy Software page vendors see — but as the owner you see ALL tenants' projects. Use it to audit deploys, fix a failing one, or run a one-off rebuild.",
    blocks: [
      {
        title: "Where to look for failures",
        body: [
          "Deploy Software → filter by vendor (top-left dropdown).",
          "Failing deploys show a red badge on the service row. Click the badge for the full step log.",
          "Common failures: missing env vars (the service status flips to needs_env_vars), build script crashes, port already in use.",
        ],
        link: { label: "Open Deploy Software", to: "/deploy-software" },
      },
      {
        title: "Edit a vendor's service",
        body: [
          "Pencil on the service row. The modal is identical to the vendor's, with one extra capability: you can change the primary domain to one outside the vendor's normally-allowed set (useful for migrations).",
          "The Domains section accepts add / edit / delete of the primary AND alias domains. Save commits the new vhost + SAN cert in one round trip.",
        ],
      },
    ],
  },
  {
    id: "transfer",
    label: "Transfer (account migration)",
    icon: <ExternalLink size={16} />,
    intro:
      "Move entire accounts (files, mailboxes, databases, DNS zones, Deploy Software projects) from another server onto this one. Works against another Betazen panel, a cPanel box, or a bare /home tree.",
    blocks: [
      {
        title: "Test the connection first",
        body: [
          "Open Transfer → 'Test Connection'.",
          "Enter the source server's IP, SSH port, and root credentials (or a Transfer Token issued by the source panel — preferred, since the password never leaves the source).",
          "If the test fails, the panel reports whether it's SSH (network/auth) or panel discovery (the source isn't a Betazen panel — choose 'bare' server type then).",
        ],
        link: { label: "Open Transfer", to: "/transfer" },
      },
      {
        title: "Discover what's on the source",
        body: [
          "Click 'Discover'. The panel SSHs in, runs a lightweight probe, and reports linux users, domains, mail accounts, databases, deploy-software projects.",
          "Pick which linux users to transfer (their owned resources expand automatically). Or pick per-resource if you want a partial migration.",
        ],
      },
      {
        title: "Run the transfer",
        body: [
          "Click 'Start Transfer'. The job shows a per-step timeline: hostname → DNS → files → databases → mail → SSL → systemd reconcile → IP repoint.",
          { kind: "note", value: "Files transfer streams over SSH with progress + ETA per linux user. Long-running steps (rsync of large /home dirs, mailbox copies) won't block the panel — you can navigate away and come back." },
          "After completion, the Reports page shows what landed and what failed. Re-run a failed transfer against the same source — successful steps are idempotent.",
        ],
      },
    ],
  },
  {
    id: "monitoring",
    label: "Monitoring / Reports",
    icon: <Activity size={16} />,
    intro:
      "Server health and per-vendor activity. Use Monitoring for live CPU / RAM / disk / network; Reports for usage trends.",
    blocks: [
      {
        title: "Live health",
        body: [
          "Monitoring page shows current CPU, memory, swap, disk, network throughput, top processes, and active connections.",
          "Each metric has a 60-minute sparkline so you can spot a spike that just happened.",
        ],
        link: { label: "Open Monitoring", to: "/monitoring" },
      },
      {
        title: "Audit log",
        body: [
          "Audit Log records every administrative action (vendor create / delete, password reset, package change, deploy trigger, transfer start, …).",
          "Filter by actor (which user did it), action type, or time window.",
        ],
        link: { label: "Open Audit Log", to: "/audit" },
      },
    ],
  },
  {
    id: "dns",
    label: "DNS (server-wide)",
    icon: <Globe2 size={16} />,
    intro:
      "Every vendor's DNS zone in one place. Bulk-edit, set TTL across many zones, audit which zones the panel actually controls.",
    blocks: [
      {
        title: "Bulk TTL change",
        body: [
          "DNS Zones → multi-select zones → 'Bulk TTL'. Set a single TTL applied to every record in selected zones.",
          { kind: "note", value: "Useful before a server migration: drop TTLs to 300 a few days ahead so propagation after the cutover is fast." },
        ],
        link: { label: "Open DNS Zones", to: "/dns" },
      },
      {
        title: "Reconcile a drifted zone",
        body: [
          "If a zone's Mongo state and PowerDNS state diverged (rare, but possible after a manual pdnsutil edit), DNS Zones → zone → 'Reconcile' replays every rrset from Mongo into PowerDNS.",
          "Returns a count of records repaired. Idempotent — safe to run on healthy zones.",
        ],
      },
    ],
  },
  {
    id: "email-issues",
    label: "Email / Mail Issues",
    icon: <Mail size={16} />,
    intro:
      "Diagnose mail delivery problems across the whole panel. The vendor side has per-mailbox controls; this page surfaces server-wide failure patterns.",
    blocks: [
      {
        title: "Read the Mail Issues page",
        body: [
          "Mail Issues lists the last N outbound delivery attempts: from, to, status (Sent / Deferred / Bounced), error code.",
          "Bounced rows show the remote server's full response so you know whether it's an SPF / DKIM / DMARC issue, a reputation issue, or just a typo'd recipient.",
        ],
        link: { label: "Open Mail Issues", to: "/mail-issues" },
      },
      {
        title: "Fix a vendor's failing SPF / DKIM / DMARC",
        body: [
          "Open the vendor's user-panel via Impersonate → Email → the domain → 'Mail authentication' card. The vendor view tells the operator exactly which records to publish in DNS to satisfy receivers.",
          "If the vendor doesn't manage their own DNS, paste the suggested records into DNS Zones for them (with their consent).",
        ],
      },
    ],
  },
  {
    id: "backups",
    label: "Backups (server-wide)",
    icon: <Archive size={16} />,
    intro:
      "Schedule full or per-vendor backups, download archives, restore in place or to another server.",
    blocks: [
      {
        title: "Schedule",
        body: [
          "Backups → 'Schedule'. Pick frequency, what to include (files / db / mail / dns), retention.",
          "Archives are encrypted with a server-side key. Store the key safely — without it the archives are unreadable.",
        ],
        link: { label: "Open Backups", to: "/backups" },
      },
      {
        title: "Restore",
        body: [
          "Backups → archive → Restore. The panel previews exactly what files / dbs / mailboxes will be overwritten before committing.",
          "Restoring on the same server is fast. Restoring on a different server is the same flow as Transfer with the archive as source.",
        ],
      },
    ],
  },
  {
    id: "firewall-shell",
    label: "Firewall + Shell access",
    icon: <ShieldCheck size={16} />,
    intro:
      "Network-level access controls + per-user shell controls. Both matter for security; both can lock you out if you're not careful.",
    blocks: [
      {
        title: "Open / close a port",
        body: [
          "Firewall → '+ Rule'. Source IP (or 0.0.0.0/0 for anywhere), destination port, action (Allow / Deny).",
          { kind: "note", value: "Be careful around port 22 (SSH) — closing it locks you out unless you keep a console available. The panel won't let you delete the rule allowing your own current IP." },
        ],
        link: { label: "Open Firewall", to: "/firewall" },
      },
      {
        title: "Grant / revoke shell access",
        body: [
          "Shell Access → user row → toggle: Normal shell (full bash), Jailed (chroot'd to their home), Disabled.",
          "Default for new vendors is Disabled — they manage everything through the panel. Grant only when needed; jailed is safer than normal.",
        ],
        link: { label: "Open Shell Access", to: "/shell-access" },
      },
    ],
  },
  {
    id: "maintenance",
    label: "Maintenance",
    icon: <Wrench size={16} />,
    intro:
      "Repair tools when something's off. Most operators never need these — they exist for the bad days.",
    blocks: [
      {
        title: "Repair databases",
        body: [
          "Repair Databases page runs a MyISAM/InnoDB integrity check across every MySQL database, surfaces corruption, and runs CHECK / REPAIR / OPTIMIZE on the affected tables.",
          "Don't run during peak hours — REPAIR locks the affected table.",
        ],
        link: { label: "Open Repair Databases", to: "/repair-databases" },
      },
      {
        title: "Edit DB configuration",
        body: [
          "MySQL my.cnf / MongoDB mongod.conf editor with syntax checks before saving.",
          "Used by support staff after the panel made a guess about RAM / connection limits that turned out wrong for this workload.",
        ],
        link: { label: "Open Edit DB Config", to: "/edit-db-config" },
      },
      {
        title: "Multi-PHP INI editor",
        body: [
          "Per-PHP-version php.ini overrides — memory_limit, upload_max_filesize, post_max_size, etc.",
          "Edits live in /etc/phpX.Y/fpm/conf.d/99-panel.ini so they survive PHP upgrades.",
        ],
        link: { label: "Open Multi-PHP INI Editor", to: "/multiphp-ini" },
      },
      {
        title: "Graceful vs forceful reboot",
        body: [
          "Maintenance → Reboot (graceful). Drains services, syncs disks, then reboots. Vendors see brief downtime; nothing is corrupted.",
          "Forceful reboot is reserved for hung kernels. Open this page only if the regular reboot doesn't respond.",
        ],
      },
    ],
  },
  {
    id: "terminal",
    label: "Terminal",
    icon: <Terminal size={16} />,
    intro:
      "Browser shell, with all the contexts the host exposes. Use it for one-off admin commands the GUI doesn't expose.",
    blocks: [
      {
        title: "Open as root",
        body: [
          "Terminal → 'root (Server)' from the context dropdown. Full root shell on the host.",
          { kind: "note", value: "Every command is logged to the audit log. Use sparingly — most things should go through the panel so other admins can see what happened." },
        ],
        link: { label: "Open Terminal", to: "/terminal" },
      },
      {
        title: "Open as a vendor",
        body: [
          "Pick the vendor's username from the dropdown. The shell drops into their home as them — useful for diagnosing 'works for me' bugs without leaving WHM.",
        ],
      },
    ],
  },
  {
    id: "faq",
    label: "FAQ",
    icon: <HelpCircle size={16} />,
    intro: "Quick answers to operator questions.",
    blocks: [
      {
        title: "Vendor says their site is down",
        body: [
          "1) Service Status — is the matching service (nginx, mariadb, dovecot, postfix, mongod) running?",
          "2) Monitoring — any CPU / disk / RAM pressure that would cause a process to be OOM-killed?",
          "3) Mail Issues / Audit Log — anything recently changed for this vendor's domain?",
          "4) Impersonate the vendor → open the failing surface → see what they see.",
        ],
      },
      {
        title: "Disk usage suddenly spiked",
        body: [
          "Reports → Disk usage by vendor. Sorts vendors by current home directory size.",
          "Top suspects: backup archives in /home/<user>/backups, big logs in /var/log/ (system, not vendor), forgotten mongo dumps.",
        ],
      },
      {
        title: "Let's Encrypt is rate-limiting me",
        body: [
          "5 failed validations per hour per domain, 50 issued certs per registered domain per week.",
          "Wait for the rate-limit window to clear. While waiting, audit which vendors are repeatedly failing — usually a wrong A record they keep retrying.",
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
              <HelpCircle size={18} className="text-blue-400" /> Owner guide
            </h1>
            <p className="text-xs text-panel-muted mt-0.5">
              How to run WHM — vendor management, server settings, transfers, monitoring.
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
              Looking for what vendors see? Impersonate a vendor (Vendors page → row → Impersonate), then open <code className="px-1 py-0.5 rounded bg-panel-bg border border-panel-border">/help</code> in the vendor panel.
            </div>
          </main>
        </div>
      </Card>
    </div>
  );
}
