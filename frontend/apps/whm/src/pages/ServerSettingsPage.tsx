import { useState, useEffect } from "react";
import { Card, Button } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Server, Save, RefreshCw, Globe, Clock, Mail, Link2, ShieldCheck,
  AlertTriangle, CheckCircle2, Loader2,
} from "lucide-react";

const TIMEZONES = [
  "UTC",
  "America/New_York",
  "America/Chicago",
  "America/Denver",
  "America/Los_Angeles",
  "America/Sao_Paulo",
  "America/Argentina/Buenos_Aires",
  "America/Mexico_City",
  "America/Toronto",
  "Europe/London",
  "Europe/Paris",
  "Europe/Berlin",
  "Europe/Moscow",
  "Europe/Istanbul",
  "Europe/Amsterdam",
  "Asia/Kolkata",
  "Asia/Shanghai",
  "Asia/Tokyo",
  "Asia/Seoul",
  "Asia/Singapore",
  "Asia/Dubai",
  "Asia/Hong_Kong",
  "Asia/Karachi",
  "Asia/Dhaka",
  "Asia/Jakarta",
  "Australia/Sydney",
  "Australia/Melbourne",
  "Pacific/Auckland",
  "Pacific/Honolulu",
  "Africa/Cairo",
  "Africa/Lagos",
  "Africa/Johannesburg",
];

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const selectClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";

export default function ServerSettingsPage() {
  const [hostname, setHostname] = useState("");
  const [timezone, setTimezone] = useState("UTC");
  const [contactEmail, setContactEmail] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const [original, setOriginal] = useState({ hostname: "", timezone: "UTC", contactEmail: "" });

  // Panel Access Domain — connect a custom domain to the WHM UI itself
  // (e.g. panel.mycompany.com). Separate from the per-customer domains
  // managed on the Domains page.
  const [panelDomain, setPanelDomain] = useState("");
  const [panelDomainInput, setPanelDomainInput] = useState("");
  const [panelDomainSSL, setPanelDomainSSL] = useState(false);
  const [serverIP, setServerIP] = useState("");
  const [issueSSL, setIssueSSL] = useState(true);
  const [sslEmail, setSslEmail] = useState("");
  const [connectingDomain, setConnectingDomain] = useState(false);

  useEffect(() => {
    fetchSettings();
    fetchPanelDomain();
  }, []);

  const fetchPanelDomain = async () => {
    try {
      const res = await api.get("/config/panel-domain");
      const data = res.data.data || {};
      setPanelDomain(data.domain || "");
      setPanelDomainInput(data.domain || "");
      setPanelDomainSSL(!!data.ssl_active);
      setServerIP(data.server_ip || "");
    } catch { /* keep defaults */ }
  };

  const handleConnectDomain = async () => {
    const d = panelDomainInput.trim().toLowerCase();
    if (!d) return toast.error("Enter a domain");
    // Soft format check — server does the strict one
    if (!/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$/.test(d)) {
      return toast.error("Invalid domain format");
    }
    if (!confirm(
      `Point ${d} at this panel?\n\n` +
      `This will rewrite /etc/nginx/sites-available/serverpanel and reload nginx.\n` +
      (issueSSL ? "Let's Encrypt will attempt to issue an SSL cert for this domain.\n" : "") +
      `\nMake sure ${d} has an A record pointing to ${serverIP || "this server"}.`
    )) return;

    setConnectingDomain(true);
    try {
      const res = await api.put("/config/panel-domain", {
        domain: d,
        issue_ssl: issueSSL,
        email: sslEmail || contactEmail,
      });
      const data = res.data.data || {};
      setPanelDomain(data.domain || d);
      setPanelDomainSSL(!!data.ssl_active);
      if (data.ssl_error) {
        toast.error(`Domain connected but SSL failed: ${data.ssl_error}`);
      } else if (data.dns_matches === false && data.dns_resolved_to) {
        toast.success(`Domain connected (DNS resolves to ${data.dns_resolved_to}, not ${serverIP} — may need time to propagate)`);
      } else {
        toast.success(`Panel now accessible at ${data.domain}`);
      }
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to connect domain");
    } finally {
      setConnectingDomain(false);
    }
  };

  const fetchSettings = async () => {
    setLoading(true);
    try {
      const res = await api.get("/config");
      const data = res.data.data || {};
      const h = (data.hostname as string) || "";
      const t = (data.timezone as string) || "UTC";
      const e = (data.contact_email as string) || "";
      setHostname(h);
      setTimezone(t);
      setContactEmail(e);
      setOriginal({ hostname: h, timezone: t, contactEmail: e });
    } catch {
      // keep defaults
    } finally {
      setLoading(false);
    }
  };

  const hasChanges =
    hostname !== original.hostname ||
    timezone !== original.timezone ||
    contactEmail !== original.contactEmail;

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      const promises: Promise<any>[] = [];
      if (hostname !== original.hostname) {
        promises.push(api.put("/config/hostname", { hostname }));
      }
      if (timezone !== original.timezone) {
        promises.push(api.put("/config/timezone", { timezone }));
      }
      if (contactEmail !== original.contactEmail) {
        promises.push(api.put("/config/contact-email", { email: contactEmail }));
      }
      await Promise.all(promises);
      toast.success("Server settings updated");
      setOriginal({ hostname, timezone, contactEmail });
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update settings");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Server Settings</h1>
          <p className="text-panel-muted text-sm mt-1">
            Configure server hostname, timezone, and contact information
          </p>
        </div>
        <Button
          onClick={fetchSettings}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
        >
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
          Refresh
        </Button>
      </div>

      {/* Settings Form */}
      <Card>
        <div className="p-5 border-b border-panel-border">
          <div className="flex items-center gap-2">
            <Server size={16} className="text-blue-400" />
            <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
              General Settings
            </h3>
          </div>
        </div>
        {loading ? (
          <div className="p-6 space-y-4">
            {[...Array(3)].map((_, i) => (
              <div key={i} className="h-16 bg-panel-bg rounded-lg animate-pulse" />
            ))}
          </div>
        ) : (
          <form onSubmit={handleSave} className="p-6 space-y-6">
            <div>
              <label className={labelClass}>
                <div className="flex items-center gap-2 mb-1">
                  <Globe size={14} className="text-green-400" />
                  Hostname
                </div>
              </label>
              <input
                type="text"
                value={hostname}
                onChange={(e) => setHostname(e.target.value)}
                className={inputClass}
                placeholder="e.g. server1.example.com"
              />
              <p className="text-xs text-panel-muted mt-1">
                The fully qualified domain name (FQDN) for this server
              </p>
            </div>

            <div>
              <label className={labelClass}>
                <div className="flex items-center gap-2 mb-1">
                  <Clock size={14} className="text-yellow-400" />
                  Timezone
                </div>
              </label>
              <select
                value={timezone}
                onChange={(e) => setTimezone(e.target.value)}
                className={selectClass}
              >
                {TIMEZONES.map((tz) => (
                  <option key={tz} value={tz}>
                    {tz}
                  </option>
                ))}
              </select>
              <p className="text-xs text-panel-muted mt-1">
                Server timezone used for cron jobs, logs, and system operations
              </p>
            </div>

            <div>
              <label className={labelClass}>
                <div className="flex items-center gap-2 mb-1">
                  <Mail size={14} className="text-purple-400" />
                  Contact Email
                </div>
              </label>
              <input
                type="email"
                value={contactEmail}
                onChange={(e) => setContactEmail(e.target.value)}
                className={inputClass}
                placeholder="admin@example.com"
              />
              <p className="text-xs text-panel-muted mt-1">
                Server administrator contact email for alerts and notifications
              </p>
            </div>

            <div className="flex justify-end pt-4 border-t border-panel-border">
              <Button
                type="submit"
                disabled={saving || !hasChanges}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
              >
                <Save size={14} />
                {saving ? "Saving..." : "Save Changes"}
              </Button>
            </div>
          </form>
        )}
      </Card>

      {/* Panel Access Domain — connect a custom domain to this WHM UI */}
      <Card>
        <div className="p-5 border-b border-panel-border">
          <div className="flex items-center gap-2">
            <Link2 size={16} className="text-cyan-400" />
            <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
              Panel Access Domain
            </h3>
          </div>
        </div>
        <div className="p-6 space-y-5">
          <p className="text-sm text-panel-muted">
            Connect a custom domain (e.g. <code className="text-panel-text font-mono">panel.mycompany.com</code>)
            to this management panel. This rewrites the nginx vhost and updates the backend's canonical URL.
          </p>

          {/* Current state */}
          <div className="flex items-start gap-3 p-3 rounded-lg bg-panel-bg border border-panel-border">
            <Server size={16} className="text-panel-muted mt-0.5 shrink-0" />
            <div className="flex-1 min-w-0 text-sm">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="text-panel-muted">Currently reachable at</span>
                {panelDomain ? (
                  <code className="text-panel-text font-mono">{panelDomain}</code>
                ) : (
                  <span className="text-panel-muted/70 italic">not configured</span>
                )}
                {panelDomain && panelDomainSSL && (
                  <span className="inline-flex items-center gap-1 text-xs text-green-400">
                    <ShieldCheck size={12} /> HTTPS
                  </span>
                )}
                {panelDomain && !panelDomainSSL && (
                  <span className="inline-flex items-center gap-1 text-xs text-amber-400">
                    <AlertTriangle size={12} /> No SSL
                  </span>
                )}
              </div>
              {serverIP && (
                <p className="text-xs text-panel-muted mt-1">
                  Server IP: <code className="text-panel-text font-mono">{serverIP}</code>
                </p>
              )}
            </div>
          </div>

          {/* DNS instructions */}
          <div className="p-3 rounded-lg bg-blue-500/5 border border-blue-500/20 text-sm space-y-2">
            <p className="font-medium text-blue-400 flex items-center gap-2">
              <CheckCircle2 size={14} /> Before connecting
            </p>
            <p className="text-panel-muted">
              At your DNS provider, create an <code className="text-panel-text font-mono">A</code> record
              for the domain pointing to <code className="text-panel-text font-mono">{serverIP || "<server IP>"}</code>.
              DNS can take a few minutes to propagate — SSL issuance needs it to resolve.
            </p>
          </div>

          {/* Input */}
          <div>
            <label className={labelClass}>
              <div className="flex items-center gap-2 mb-1">
                <Globe size={14} className="text-cyan-400" />
                New panel domain
              </div>
            </label>
            <input
              type="text"
              value={panelDomainInput}
              onChange={(e) => setPanelDomainInput(e.target.value)}
              className={inputClass}
              placeholder="panel.mycompany.com"
              disabled={connectingDomain}
            />
          </div>

          {/* Auto-SSL toggle */}
          <label className="flex items-center gap-3 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={issueSSL}
              onChange={(e) => setIssueSSL(e.target.checked)}
              className="accent-blue-500"
              disabled={connectingDomain}
            />
            <span className="flex items-center gap-2 text-sm text-panel-text">
              <ShieldCheck size={14} className="text-green-400" />
              Auto-issue Let's Encrypt SSL certificate
            </span>
          </label>

          {issueSSL && (
            <div className="pl-6">
              <label className={labelClass}>
                <div className="flex items-center gap-2 mb-1">
                  <Mail size={14} className="text-purple-400" />
                  SSL contact email
                </div>
              </label>
              <input
                type="email"
                value={sslEmail}
                onChange={(e) => setSslEmail(e.target.value)}
                className={inputClass}
                placeholder={contactEmail || "admin@yourdomain.com"}
                disabled={connectingDomain}
              />
              <p className="text-xs text-panel-muted mt-1">
                Let's Encrypt uses this for expiry warnings. Defaults to your Contact Email.
              </p>
            </div>
          )}

          {/* Connect button */}
          <div className="flex justify-end pt-2 border-t border-panel-border">
            <Button
              onClick={handleConnectDomain}
              disabled={connectingDomain || !panelDomainInput.trim() || panelDomainInput.trim() === panelDomain}
              className="flex items-center gap-2 px-4 py-2 bg-cyan-600 hover:bg-cyan-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
            >
              {connectingDomain ? <Loader2 size={14} className="animate-spin" /> : <Link2 size={14} />}
              {connectingDomain ? "Connecting..." : panelDomain ? "Update Domain" : "Connect Domain"}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}
