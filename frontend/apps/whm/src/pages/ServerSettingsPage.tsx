import { useState, useEffect } from "react";
import { Card, Button, PasswordInput } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Server, Save, RefreshCw, Globe, Clock, Mail, Link2, ShieldCheck,
  AlertTriangle, CheckCircle2, Loader2, Send, Eye, ArrowLeftRight,
  Image as ImageIcon, Upload, Trash2,
} from "lucide-react";

// PanelMailConfig is the shape /api/v1/whm/config/mail returns. The
// password is intentionally represented by `has_password: true/false`
// so the form can render "credentials saved" without ever receiving
// the plaintext password from the server.
interface PanelMailConfig {
  host: string;
  port: number;
  username: string;
  has_password: boolean;
  tls_mode: "none" | "starttls" | "tls" | string;
  from_addr: string;
  from_name: string;
  reply_to: string;
  configured: boolean;
}

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

// Branding view shape — mirrors backend BrandingView. Empty data
// URLs mean "no custom asset configured; the panel uses defaults".
interface BrandingView {
  panel_name: string;
  logo_data_url?: string;
  favicon_data_url?: string;
}

// Hard cap on uploaded asset size. Mirrors the backend's
// MaxBrandingAssetBytes (256 KB). Frontend rejects oversize first so
// the operator gets immediate feedback instead of a server-side 400.
const MAX_BRANDING_ASSET_BYTES = 256 * 1024;

// readFileAsDataURL turns a chosen <input type=file> blob into a
// "data:image/...;base64,..." string. Browsers do this natively via
// FileReader. We don't compress / down-scale here — keeping the
// implementation tiny — so the operator is responsible for picking
// images under 256 KB.
function readFileAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

export default function ServerSettingsPage() {
  const [hostname, setHostname] = useState("");
  const [timezone, setTimezone] = useState("UTC");
  const [contactEmail, setContactEmail] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  // Branding state — independent fetch + save flow, parallel to the
  // panel-mail card below. Default panel name keeps the fresh-install
  // experience identical to the hardcoded label the chrome used to
  // ship.
  const [brand, setBrand] = useState<BrandingView>({ panel_name: "Betazen Server Panel" });
  const [brandLoading, setBrandLoading] = useState(true);
  const [brandSaving, setBrandSaving] = useState(false);

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

  // SSL Certificate state — issued_at / expires_at / days_remaining /
  // issuer surface the live cert on disk, independent of the
  // "connect a domain" form above. Lets the operator install or renew
  // SSL without retyping the domain.
  interface PanelSSLState {
    domain: string;
    ssl_active: boolean;
    issuer?: string;
    issued_at?: string;
    expires_at?: string;
    days_remaining?: number;
    is_ip_domain?: boolean;
  }
  const [sslState, setSslState] = useState<PanelSSLState | null>(null);
  const [installingSSL, setInstallingSSL] = useState(false);

  // Outgoing Mail (SMTP) — the relay the panel uses to send password
  // resets, notifications, expiry warnings. Server stores the SMTP
  // password AES-GCM encrypted; the UI only ever sees has_password.
  const [mailCfg, setMailCfg] = useState<PanelMailConfig | null>(null);
  const [mailInput, setMailInput] = useState({
    host: "",
    port: 587,
    username: "",
    password: "", // empty = keep existing
    tls_mode: "starttls" as "none" | "starttls" | "tls",
    from_addr: "",
    from_name: "Betazen Server Panel",
    reply_to: "",
  });
  const [savingMail, setSavingMail] = useState(false);
  const [testMailTo, setTestMailTo] = useState("");
  const [sendingTest, setSendingTest] = useState(false);

  // Demo-hint feature flags — toggles for the "demo credentials" block
  // on the login pages and the sample-values card on the transfer wizard.
  // Persists through /api/v1/whm/config/ui-settings and is read by the
  // login pages via the unauthenticated /api/v1/public-settings.
  const [uiShowLoginDemo, setUIShowLoginDemo] = useState(true);
  const [uiShowTransferDemo, setUIShowTransferDemo] = useState(true);
  const [uiOriginal, setUIOriginal] = useState({ login: true, transfer: true });
  const [savingUI, setSavingUI] = useState(false);

  useEffect(() => {
    fetchSettings();
    fetchPanelDomain();
    fetchPanelSSL();
    fetchMailConfig();
    fetchUISettings();
    fetchBranding();
  }, []);

  // Branding fetch — same singleton pattern as fetchMailConfig.
  // Defaults baked in so a 404 / network error still renders a usable
  // form (operator can save fresh values without seeing an error toast).
  const fetchBranding = async () => {
    try {
      const res = await api.get("/config/branding");
      const d = res.data?.data || {};
      setBrand({
        panel_name: d.panel_name || "Betazen Server Panel",
        logo_data_url: d.logo_data_url || "",
        favicon_data_url: d.favicon_data_url || "",
      });
    } catch {
      // keep defaults
    } finally {
      setBrandLoading(false);
    }
  };

  // handleBrandUpload reads the chosen file as a data URL, validates
  // size client-side (mirrors the 256 KB backend cap), and stages the
  // result onto the brand state. The operator hits Save to persist —
  // we don't auto-save on file pick because they may want to clear
  // the field afterwards before submitting.
  const handleBrandUpload = async (
    field: "logo_data_url" | "favicon_data_url",
    file: File | null,
  ) => {
    if (!file) return;
    if (file.size > MAX_BRANDING_ASSET_BYTES) {
      toast.error(`Image is too large — keep under ${Math.round(MAX_BRANDING_ASSET_BYTES / 1024)} KB`);
      return;
    }
    if (!file.type.startsWith("image/")) {
      toast.error("Please pick an image file (PNG / JPG / SVG / ICO)");
      return;
    }
    try {
      const dataURL = await readFileAsDataURL(file);
      setBrand((b) => ({ ...b, [field]: dataURL }));
    } catch {
      toast.error("Failed to read image — try a different file");
    }
  };

  const handleBrandSave = async () => {
    if (!brand.panel_name.trim()) {
      toast.error("Panel name is required");
      return;
    }
    setBrandSaving(true);
    try {
      const res = await api.put("/config/branding", {
        panel_name: brand.panel_name.trim(),
        logo_data_url: brand.logo_data_url || "",
        favicon_data_url: brand.favicon_data_url || "",
      });
      const d = res.data?.data || {};
      setBrand({
        panel_name: d.panel_name || brand.panel_name,
        logo_data_url: d.logo_data_url || "",
        favicon_data_url: d.favicon_data_url || "",
      });
      toast.success("Branding updated — refresh the page to see the new logo / favicon");
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to save branding");
    } finally {
      setBrandSaving(false);
    }
  };

  const fetchUISettings = async () => {
    try {
      const res = await api.get("/config/ui-settings");
      const d = res.data?.data || {};
      const login = d.show_demo_login_credentials !== false;
      const transfer = d.show_demo_transfer_settings !== false;
      setUIShowLoginDemo(login);
      setUIShowTransferDemo(transfer);
      setUIOriginal({ login, transfer });
    } catch {
      /* first load falls back to the shown-by-default state above */
    }
  };

  const handleSaveUISettings = async () => {
    setSavingUI(true);
    try {
      await api.put("/config/ui-settings", {
        show_demo_login_credentials: uiShowLoginDemo,
        show_demo_transfer_settings: uiShowTransferDemo,
      });
      setUIOriginal({ login: uiShowLoginDemo, transfer: uiShowTransferDemo });
      toast.success("Demo hint settings updated");
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update demo hints");
    } finally {
      setSavingUI(false);
    }
  };

  const fetchPanelSSL = async () => {
    try {
      const res = await api.get("/config/panel-ssl");
      setSslState((res.data?.data as PanelSSLState) || null);
    } catch {
      /* not fatal — the card just shows "not issued" */
    }
  };

  const handleInstallSSL = async (forceRenew: boolean) => {
    setInstallingSSL(true);
    try {
      const res = await api.post("/config/panel-ssl", {
        email: sslEmail || contactEmail,
        force_renew: forceRenew,
      });
      const next = res.data?.data as PanelSSLState;
      setSslState(next);
      if (next?.ssl_active) {
        toast.success(
          forceRenew
            ? `Certificate renewed — valid for ${next.days_remaining} more days`
            : `SSL installed — valid for ${next.days_remaining} days`
        );
        // Panel SSL flag on the Panel Access Domain card also needs to flip.
        setPanelDomainSSL(true);
      } else {
        toast.error("Certbot ran but no cert is on disk — check the backend log");
      }
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "SSL install failed");
    } finally {
      setInstallingSSL(false);
    }
  };

  // fetchMailConfig pulls the current SMTP settings (no plaintext
  // password — just has_password flag) so the form renders with the
  // saved Host/Port/From/etc. pre-filled. Run on mount and after every
  // successful Save so the "Configured" badge flips state.
  const fetchMailConfig = async () => {
    try {
      const res = await api.get("/config/mail");
      const cfg = res.data?.data as PanelMailConfig;
      setMailCfg(cfg);
      setMailInput({
        host: cfg.host || "",
        port: cfg.port || 587,
        username: cfg.username || "",
        password: "",
        tls_mode: (cfg.tls_mode as "none" | "starttls" | "tls") || "starttls",
        from_addr: cfg.from_addr || "",
        from_name: cfg.from_name || "Betazen Server Panel",
        reply_to: cfg.reply_to || "",
      });
    } catch {
      // Non-fatal: a fresh install has no config yet.
    }
  };

  // handleSaveMail upserts the SMTP config. Empty password means "keep
  // the existing cipher" — backend preserves it, so editing the Host
  // alone doesn't force the admin to re-type the relay password. The
  // Save response now carries a synchronous confirmation-send result:
  //   - send_status "ok"      → relay works, confirmation email landed
  //   - send_status "failed"  → relay auth/network failed; show the
  //                             real reason (e.g. Gmail requires an
  //                             App Password) so the operator doesn't
  //                             have to chase it in the server log
  //   - send_status "skipped" → mailer still disabled (missing host /
  //                             port / from)
  const handleSaveMail = async () => {
    setSavingMail(true);
    try {
      const res = await api.put("/config/mail", mailInput);
      const data = (res.data?.data || {}) as {
        send_status?: string;
        send_error?: string;
      };
      if (data.send_status === "failed") {
        toast.error(
          `SMTP saved, but confirmation email failed: ${data.send_error || "unknown error"}`,
          { duration: 10000 }
        );
      } else if (data.send_status === "ok") {
        toast.success("SMTP settings saved — confirmation email sent");
      } else if (data.send_status === "skipped") {
        toast.success("SMTP settings saved (mailer still disabled — fill host, port, and from to enable)");
      } else {
        toast.success("SMTP settings saved");
      }
      setMailInput((p) => ({ ...p, password: "" })); // clear the typed password from state
      fetchMailConfig();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to save SMTP settings");
    } finally {
      setSavingMail(false);
    }
  };

  // handleTestMail asks the server to actually send a message via the
  // saved SMTP settings. The operator types a destination in the input;
  // we default to their Contact Email when they haven't.
  const handleTestMail = async () => {
    const to = (testMailTo.trim() || contactEmail.trim()).trim();
    if (!to) {
      toast.error("Enter a recipient address (or set Contact Email above)");
      return;
    }
    setSendingTest(true);
    try {
      await api.post("/config/mail/test", { to });
      toast.success(`Test email queued to ${to} — check the inbox in a few seconds`);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Test send failed");
    } finally {
      setSendingTest(false);
    }
  };

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

      {/* Demo / Example Hints — owner decides whether the login pages and
          the transfer wizard surface sample credentials & sample values.
          Off by default on production panels; on for fresh installs. */}
      <Card>
        <div className="p-5 border-b border-panel-border">
          <div className="flex items-center gap-2">
            <Eye size={16} className="text-amber-400" />
            <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
              Demo &amp; Example Hints
            </h3>
          </div>
        </div>
        <div className="p-6 space-y-5">
          <p className="text-sm text-panel-muted">
            Toggle the demo-credentials block on the login pages and the sample-values hint on the Transfer wizard. Turn these off on a production panel so staff and customers don't see pre-filled example logins or sample tokens.
          </p>

          <label className="flex items-start gap-3 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={uiShowLoginDemo}
              onChange={(e) => setUIShowLoginDemo(e.target.checked)}
              className="mt-0.5 accent-blue-500"
            />
            <span className="flex-1">
              <span className="flex items-center gap-2 text-sm text-panel-text">
                <Globe size={14} className="text-blue-400" />
                Show demo login credentials on login pages
              </span>
              <span className="block text-xs text-panel-muted mt-0.5">
                Controls the "Demo Login" card visible on <code className="text-panel-text font-mono">/whm/login</code> and <code className="text-panel-text font-mono">/user-panel/login</code>.
              </span>
            </span>
          </label>

          <label className="flex items-start gap-3 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={uiShowTransferDemo}
              onChange={(e) => setUIShowTransferDemo(e.target.checked)}
              className="mt-0.5 accent-blue-500"
            />
            <span className="flex-1">
              <span className="flex items-center gap-2 text-sm text-panel-text">
                <ArrowLeftRight size={14} className="text-green-400" />
                Show demo values on Transfer settings
              </span>
              <span className="block text-xs text-panel-muted mt-0.5">
                Controls the "Demo / Example" hint with sample IP / token / panel URL on the Transfer wizard's connection step.
              </span>
            </span>
          </label>

          <div className="flex justify-end pt-2 border-t border-panel-border">
            <Button
              onClick={handleSaveUISettings}
              disabled={
                savingUI ||
                (uiShowLoginDemo === uiOriginal.login &&
                  uiShowTransferDemo === uiOriginal.transfer)
              }
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
            >
              {savingUI ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}
              {savingUI ? "Saving..." : "Save"}
            </Button>
          </div>
        </div>
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

      {/* SSL Certificate — install or renew the panel's Let's Encrypt
          cert independently of the domain change flow. Visible after a
          panel domain has been set; disabled for bare-IP "domains" since
          LE won't issue for them. */}
      <Card>
        <div className="p-5 border-b border-panel-border">
          <div className="flex items-center gap-2">
            <ShieldCheck size={16} className="text-green-400" />
            <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
              SSL Certificate
            </h3>
          </div>
        </div>
        <div className="p-6 space-y-5">
          <p className="text-sm text-panel-muted">
            Install or renew the Let's Encrypt certificate for the panel
            domain. Uses HTTP-01 via <code className="text-panel-text font-mono">/var/www/certbot</code>;
            DNS must resolve to this server before issuance.
          </p>

          {/* Current cert state */}
          <div className="p-3 rounded-lg bg-panel-bg border border-panel-border text-sm">
            {!sslState || !sslState.domain ? (
              <span className="text-panel-muted italic">No panel domain configured yet.</span>
            ) : sslState.is_ip_domain ? (
              <div className="flex items-start gap-2">
                <AlertTriangle size={14} className="text-amber-400 mt-0.5 shrink-0" />
                <div>
                  <p className="text-amber-400 font-medium">Cannot issue SSL for a raw IP</p>
                  <p className="text-xs text-panel-muted mt-1">
                    Let's Encrypt only issues certificates for domain names.
                    Connect a real domain above, then return here to install SSL.
                  </p>
                </div>
              </div>
            ) : sslState.ssl_active ? (
              <div className="space-y-1">
                <div className="flex items-center gap-2">
                  <ShieldCheck size={14} className="text-green-400" />
                  <span className="text-panel-text">Active for </span>
                  <code className="text-panel-text font-mono">{sslState.domain}</code>
                </div>
                <p className="text-xs text-panel-muted">Issuer: {sslState.issuer || "—"}</p>
                <p className="text-xs text-panel-muted">
                  Expires: {sslState.expires_at ? new Date(sslState.expires_at).toLocaleDateString() : "—"}
                  {typeof sslState.days_remaining === "number" && (
                    <span className={`ml-2 ${sslState.days_remaining < 14 ? "text-amber-400" : "text-green-400"}`}>
                      ({sslState.days_remaining} days remaining)
                    </span>
                  )}
                </p>
              </div>
            ) : (
              <div className="flex items-center gap-2">
                <AlertTriangle size={14} className="text-amber-400" />
                <span className="text-amber-400">No certificate issued for </span>
                <code className="text-panel-text font-mono">{sslState.domain}</code>
              </div>
            )}
          </div>

          {/* SSL contact email (shared with the Panel Access Domain card above
              so operators don't have to retype it). */}
          {!sslState?.is_ip_domain && sslState?.domain && (
            <div>
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
                disabled={installingSSL}
              />
              <p className="text-xs text-panel-muted mt-1">
                Used by Let's Encrypt for expiry warnings.
              </p>
            </div>
          )}

          {/* Action buttons — Install vs Renew depending on current state */}
          {!sslState?.is_ip_domain && sslState?.domain && (
            <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
              {sslState.ssl_active ? (
                <Button
                  onClick={() => handleInstallSSL(true)}
                  disabled={installingSSL}
                  className="flex items-center gap-2 px-4 py-2 bg-amber-600 hover:bg-amber-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
                >
                  {installingSSL ? <Loader2 size={14} className="animate-spin" /> : <RefreshCw size={14} />}
                  {installingSSL ? "Renewing..." : "Force Renew"}
                </Button>
              ) : (
                <Button
                  onClick={() => handleInstallSSL(false)}
                  disabled={installingSSL}
                  className="flex items-center gap-2 px-4 py-2 bg-green-600 hover:bg-green-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
                >
                  {installingSSL ? <Loader2 size={14} className="animate-spin" /> : <ShieldCheck size={14} />}
                  {installingSSL ? "Installing..." : "Install SSL"}
                </Button>
              )}
            </div>
          )}
        </div>
      </Card>

      {/* Outgoing Mail (SMTP) — the relay the panel uses to send
          password-reset emails, notifications, and domain-expiry
          warnings. Separate from tenant mailboxes. */}
      <Card>
        <div className="p-6 space-y-4">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h2 className="text-base font-semibold text-panel-text flex items-center gap-2">
                <Mail size={16} className="text-blue-400" /> Outgoing Mail (SMTP)
              </h2>
              <p className="text-xs text-panel-muted mt-1">
                Used for password resets, account recovery, and notification emails. The panel reads these settings every time it sends — changes take effect immediately, no restart needed. The SMTP password is encrypted at rest with the panel's <code>APP_ENCRYPTION_KEY</code>.
              </p>
            </div>
            {mailCfg?.configured ? (
              <span className="inline-flex items-center gap-1 px-2 py-1 rounded text-[11px] bg-green-500/10 text-green-400 border border-green-500/20 shrink-0">
                <CheckCircle2 size={12} /> Configured
              </span>
            ) : (
              <span className="inline-flex items-center gap-1 px-2 py-1 rounded text-[11px] bg-amber-500/10 text-amber-400 border border-amber-500/20 shrink-0">
                <AlertTriangle size={12} /> Not configured
              </span>
            )}
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">SMTP Host *</label>
              <input
                type="text"
                value={mailInput.host}
                onChange={(e) => setMailInput({ ...mailInput, host: e.target.value })}
                placeholder="smtp.gmail.com"
                className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 text-sm"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">Port *</label>
              <input
                type="number"
                min={1}
                max={65535}
                value={mailInput.port}
                onChange={(e) => setMailInput({ ...mailInput, port: parseInt(e.target.value) || 0 })}
                placeholder="587"
                className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 text-sm"
              />
              <p className="text-[11px] text-panel-muted mt-1">
                Common: <b>587</b> (STARTTLS), <b>465</b> (TLS), <b>25</b> (plain)
              </p>
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-panel-text mb-1">Encryption *</label>
            <select
              value={mailInput.tls_mode}
              onChange={(e) => setMailInput({ ...mailInput, tls_mode: e.target.value as "none" | "starttls" | "tls" })}
              className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 text-sm"
            >
              <option value="starttls">STARTTLS (upgrade on port 587)</option>
              <option value="tls">TLS / SMTPS (implicit TLS on port 465)</option>
              <option value="none">None (plaintext — dev only)</option>
            </select>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">Username</label>
              <input
                type="text"
                value={mailInput.username}
                onChange={(e) => setMailInput({ ...mailInput, username: e.target.value })}
                placeholder="apikey / user@example.com"
                className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 text-sm"
              />
              <p className="text-[11px] text-panel-muted mt-1">Leave blank for unauthenticated relays</p>
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">Password</label>
              {/* hideGenerator: SMTP relay credential issued by the upstream
                  mail provider (Gmail app password, SES SMTP, etc) — operator
                  pastes what the provider gave them. */}
              <PasswordInput
                autoComplete="new-password"
                value={mailInput.password}
                onChange={(v) => setMailInput({ ...mailInput, password: v })}
                placeholder={mailCfg?.has_password ? "•••••••• (leave blank to keep current)" : "SMTP or app password"}
                inputClassName="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 text-sm"
                hideGenerator
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">From Address *</label>
              <input
                type="email"
                value={mailInput.from_addr}
                onChange={(e) => setMailInput({ ...mailInput, from_addr: e.target.value })}
                placeholder="noreply@panel.example.com"
                className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 text-sm"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">From Name</label>
              <input
                type="text"
                value={mailInput.from_name}
                onChange={(e) => setMailInput({ ...mailInput, from_name: e.target.value })}
                placeholder="Betazen Server Panel"
                className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 text-sm"
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-panel-text mb-1">Reply-To (optional)</label>
            <input
              type="email"
              value={mailInput.reply_to}
              onChange={(e) => setMailInput({ ...mailInput, reply_to: e.target.value })}
              placeholder="support@panel.example.com"
              className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 text-sm"
            />
          </div>

          <div className="flex flex-wrap items-center gap-2 pt-2 border-t border-panel-border">
            <Button
              onClick={handleSaveMail}
              disabled={savingMail}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
            >
              {savingMail ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}
              {savingMail ? "Saving…" : "Save settings"}
            </Button>

            <div className="ml-auto flex items-center gap-2">
              <input
                type="email"
                placeholder="Test recipient"
                value={testMailTo}
                onChange={(e) => setTestMailTo(e.target.value)}
                className="px-3 py-2 w-56 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 text-sm"
              />
              <Button
                onClick={handleTestMail}
                disabled={sendingTest || !mailCfg?.configured}
                title={mailCfg?.configured ? "Send a test email to verify the relay works" : "Save settings first"}
                className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border hover:border-blue-500/40 rounded-lg text-panel-text text-sm transition-colors disabled:opacity-50"
              >
                {sendingTest ? <Loader2 size={14} className="animate-spin" /> : <Send size={14} />}
                Send test
              </Button>
            </div>
          </div>
        </div>
      </Card>

      {/* Branding — panel name + logo + favicon. The whole panel
          (sidebar, top bar, login page, browser tab) reads these
          values from /api/v1/branding on page load, so a refresh is
          enough to apply the change. No restart required. */}
      <Card>
        <div className="p-6 space-y-6">
          <div className="flex items-center gap-2 text-panel-text">
            <ImageIcon size={18} className="text-blue-400" />
            <h2 className="font-semibold">Branding</h2>
            {brandLoading && <Loader2 size={14} className="animate-spin text-panel-muted" />}
          </div>
          <p className="text-xs text-panel-muted -mt-3">
            Panel name + logo + favicon. Public — used by the login page and browser tab BEFORE any user is signed in.
          </p>

          <div>
            <label className={labelClass}>Panel name</label>
            <input
              type="text"
              value={brand.panel_name}
              onChange={(e) => setBrand({ ...brand, panel_name: e.target.value })}
              placeholder="Betazen Server Panel"
              maxLength={80}
              className={inputClass}
            />
            <p className="text-[11px] text-panel-muted mt-1">
              Shown in the top bar, sidebar, login page, and outgoing notification emails.
            </p>
          </div>

          <div className="grid md:grid-cols-2 gap-6">
            {/* Logo */}
            <div>
              <label className={labelClass}>Logo</label>
              <div className="flex items-center gap-3 p-3 rounded-lg bg-panel-bg border border-panel-border">
                <div className="w-16 h-16 rounded-md bg-panel-surface border border-panel-border flex items-center justify-center overflow-hidden shrink-0">
                  {brand.logo_data_url ? (
                    <img src={brand.logo_data_url} alt="logo" className="max-w-full max-h-full" />
                  ) : (
                    <ImageIcon size={28} className="text-panel-muted" />
                  )}
                </div>
                <div className="flex-1 min-w-0">
                  <label className="inline-flex items-center gap-2 px-3 py-1.5 text-xs bg-panel-surface border border-panel-border hover:border-blue-500/40 rounded-md cursor-pointer text-panel-text transition-colors">
                    <Upload size={12} />
                    {brand.logo_data_url ? "Replace" : "Upload"}
                    <input
                      type="file"
                      accept="image/png,image/jpeg,image/svg+xml,image/webp"
                      className="hidden"
                      onChange={(e) => handleBrandUpload("logo_data_url", e.target.files?.[0] ?? null)}
                    />
                  </label>
                  {brand.logo_data_url && (
                    <button
                      type="button"
                      onClick={() => setBrand((b) => ({ ...b, logo_data_url: "" }))}
                      className="ml-2 inline-flex items-center gap-1 px-2 py-1.5 text-xs text-red-400 hover:text-red-300 rounded-md transition-colors"
                      title="Remove logo (revert to default)"
                    >
                      <Trash2 size={12} /> Clear
                    </button>
                  )}
                  <p className="text-[11px] text-panel-muted mt-1">
                    PNG / JPG / SVG. Recommended 256×256 or wider banner. Max 256 KB.
                  </p>
                </div>
              </div>
            </div>

            {/* Favicon */}
            <div>
              <label className={labelClass}>Favicon</label>
              <div className="flex items-center gap-3 p-3 rounded-lg bg-panel-bg border border-panel-border">
                <div className="w-16 h-16 rounded-md bg-panel-surface border border-panel-border flex items-center justify-center overflow-hidden shrink-0">
                  {brand.favicon_data_url ? (
                    <img src={brand.favicon_data_url} alt="favicon" className="max-w-full max-h-full" />
                  ) : (
                    <Globe size={28} className="text-panel-muted" />
                  )}
                </div>
                <div className="flex-1 min-w-0">
                  <label className="inline-flex items-center gap-2 px-3 py-1.5 text-xs bg-panel-surface border border-panel-border hover:border-blue-500/40 rounded-md cursor-pointer text-panel-text transition-colors">
                    <Upload size={12} />
                    {brand.favicon_data_url ? "Replace" : "Upload"}
                    <input
                      type="file"
                      accept="image/png,image/x-icon,image/svg+xml,image/webp"
                      className="hidden"
                      onChange={(e) => handleBrandUpload("favicon_data_url", e.target.files?.[0] ?? null)}
                    />
                  </label>
                  {brand.favicon_data_url && (
                    <button
                      type="button"
                      onClick={() => setBrand((b) => ({ ...b, favicon_data_url: "" }))}
                      className="ml-2 inline-flex items-center gap-1 px-2 py-1.5 text-xs text-red-400 hover:text-red-300 rounded-md transition-colors"
                      title="Remove favicon"
                    >
                      <Trash2 size={12} /> Clear
                    </button>
                  )}
                  <p className="text-[11px] text-panel-muted mt-1">
                    32×32 PNG or .ico. Shown in the browser tab. Max 256 KB.
                  </p>
                </div>
              </div>
            </div>
          </div>

          <div className="flex justify-end pt-2">
            <Button
              onClick={handleBrandSave}
              disabled={brandSaving || !brand.panel_name.trim()}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm transition-colors disabled:opacity-50"
            >
              {brandSaving ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}
              Save branding
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}
