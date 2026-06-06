// MailSuitePage — owner-only admin for the SEPARATE mail-suite product.
//
// One-domain flow: the operator types the public domain they want
// mail-suite served at (e.g. mail.example.com), clicks Install, and
// the panel handles binary build/copy, .env (with auto-generated JWT +
// service token), systemd, nginx vhost, and Let's Encrypt — then
// auto-registers the resulting deployment so the rest of the page
// (per-domain "Enable Mail", Open webmail) starts working immediately.
//
// The "Register existing deployment" form is kept behind a disclosure
// for ops who want to point the panel at an already-running mail-suite
// instance hosted elsewhere.

import { useEffect, useState, FormEvent } from "react";
import { Card, Button } from "@serverpanel/ui";
import { Mails, Trash2, ExternalLink, ChevronDown, ChevronRight, Globe2 } from "lucide-react";
import toast from "react-hot-toast";
import api from "@/lib/api";

type Deployment = {
  id: string;
  label: string;
  url: string;
  webmail_url: string;
  created_at: string;
};

type DNSRecord = { type: string; name: string; expected: string; found?: string; ok: boolean };
type DNSStatus = { domain: string; records: DNSRecord[]; all_ok: boolean };

export default function MailSuitePage() {
  const [deps, setDeps] = useState<Deployment[]>([]);
  const [loadingDeps, setLoadingDeps] = useState(false);

  const [installDomain, setInstallDomain] = useState("");
  const [installing, setInstalling] = useState(false);

  const [showAdvanced, setShowAdvanced] = useState(false);
  const [form, setForm] = useState({ label: "", url: "", service_token: "", webmail_url: "" });

  const [domain, setDomain] = useState("");
  const [status, setStatus] = useState<DNSStatus | null>(null);
  const [busy, setBusy] = useState(false);

  async function loadDeps() {
    setLoadingDeps(true);
    try {
      const r = await api.get("/mail-suite/deployments");
      setDeps(r.data?.data || []);
    } catch (e: any) {
      toast.error(e?.response?.data?.error || "Could not load deployments");
    } finally {
      setLoadingDeps(false);
    }
  }

  useEffect(() => { void loadDeps(); }, []);

  async function install(e: FormEvent) {
    e.preventDefault();
    if (!installDomain) return;
    if (!confirm(
      `Install mail-suite on this server, served at https://${installDomain}?\n\n` +
      `The panel will: build the binary, write /opt/mail-suite/.env with auto-generated ` +
      `secrets, install a systemd unit, configure nginx, and request a Let's Encrypt cert. ` +
      `It will then register the deployment automatically.`,
    )) return;
    setInstalling(true);
    try {
      const r = await api.post("/mail-suite/install", { domain: installDomain });
      toast.success(`Installed at ${r.data?.data?.url || installDomain}`);
      setInstallDomain("");
      void loadDeps();
    } catch (e: any) {
      toast.error(e?.response?.data?.error || "Install failed — see server logs");
    } finally {
      setInstalling(false);
    }
  }

  async function register(e: FormEvent) {
    e.preventDefault();
    try {
      await api.post("/mail-suite/deployments", form);
      setForm({ label: "", url: "", service_token: "", webmail_url: "" });
      toast.success("Deployment registered");
      void loadDeps();
    } catch (e: any) {
      toast.error(e?.response?.data?.error || "Register failed");
    }
  }

  async function removeDep(id: string) {
    if (!confirm("Remove this deployment registration? (The mail-suite backend itself is NOT uninstalled.)")) return;
    try {
      await api.delete(`/mail-suite/deployments/${id}`);
      toast.success("Removed");
      void loadDeps();
    } catch (e: any) {
      toast.error(e?.response?.data?.error || "Delete failed");
    }
  }

  async function enableMail(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await api.post(`/mail-suite/domains/${encodeURIComponent(domain)}/enable-mail`);
      setStatus(r.data?.data);
      toast.success("Records upserted — verify below");
    } catch (e: any) {
      toast.error(e?.response?.data?.error || "Enable failed");
    } finally {
      setBusy(false);
    }
  }

  async function verify() {
    if (!domain) return;
    setBusy(true);
    try {
      const r = await api.get(`/mail-suite/domains/${encodeURIComponent(domain)}/status`);
      setStatus(r.data?.data);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="p-6 space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-panel-text flex items-center gap-2">
          <Mails className="h-6 w-6" /> Mail Suite
        </h1>
        <p className="text-sm text-panel-muted mt-1">
          A Gmail/Zoho-style mail platform separate from the classic Email page. Type a domain and
          the panel installs the backend, writes its config, opens the firewall via nginx, and
          gets a TLS cert — no SSH needed. The classic Email page and Roundcube SSO are unaffected.
        </p>
      </div>

      {/* One-domain installer */}
      <Card
        title="Install mail-suite"
        description="Provision the mail-suite backend on this server. You only need to choose the public domain it will be served at."
      >
        <form onSubmit={install} className="flex flex-col sm:flex-row gap-3 items-stretch sm:items-end">
          <div className="flex-1">
            <label className="block text-sm text-panel-muted mb-1">Public domain</label>
            <div className="relative">
              <Globe2 className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-panel-muted" />
              <input
                className="w-full bg-panel-bg border border-panel-border rounded-lg pl-9 pr-3 py-2 text-panel-text placeholder:text-panel-muted focus:outline-none focus:border-brand-500"
                placeholder="mail.example.com"
                value={installDomain}
                onChange={(e) => setInstallDomain(e.target.value.trim())}
                required
              />
            </div>
            <p className="text-xs text-panel-muted mt-1">
              The panel auto-generates the JWT secret and service token, writes the .env, installs
              a systemd unit, configures nginx, and runs certbot.
            </p>
          </div>
          <Button type="submit" loading={installing}>
            {installing ? "Installing…" : "Install on this server"}
          </Button>
        </form>
      </Card>

      {/* Registered deployments */}
      <Card title="Registered deployments" description="Each row is a mail-suite backend this panel knows about.">
        {loadingDeps && <div className="text-sm text-panel-muted">Loading…</div>}
        {!loadingDeps && deps.length === 0 && (
          <div className="text-sm text-panel-muted">No deployments yet — install one above, or add an existing remote deployment.</div>
        )}
        <div className="divide-y divide-panel-border -mx-6">
          {deps.map((d) => (
            <div key={d.id} className="flex items-center justify-between px-6 py-3">
              <div className="min-w-0">
                <div className="font-medium text-panel-text truncate">{d.label}</div>
                <div className="text-xs text-panel-muted truncate">{d.url}</div>
              </div>
              <div className="flex items-center gap-2">
                <a
                  href={d.webmail_url}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1 text-sm text-brand-400 hover:text-brand-300"
                >
                  Open webmail <ExternalLink className="h-3.5 w-3.5" />
                </a>
                <Button variant="ghost" size="sm" onClick={() => removeDep(d.id)} aria-label="Remove deployment">
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            </div>
          ))}
        </div>

        {/* Advanced: register an existing remote mail-suite */}
        <button
          type="button"
          onClick={() => setShowAdvanced((v) => !v)}
          className="mt-4 inline-flex items-center gap-1 text-sm text-panel-muted hover:text-panel-text"
        >
          {showAdvanced ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
          Advanced: register an existing remote deployment
        </button>
        {showAdvanced && (
          <form onSubmit={register} className="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-3 p-4 rounded-lg bg-panel-bg border border-panel-border">
            <input
              className="bg-panel-surface border border-panel-border rounded-lg px-3 py-2 text-panel-text placeholder:text-panel-muted focus:outline-none focus:border-brand-500"
              placeholder="Label (e.g. EU-1)"
              value={form.label}
              onChange={(e) => setForm({ ...form, label: e.target.value })}
              required
            />
            <input
              className="bg-panel-surface border border-panel-border rounded-lg px-3 py-2 text-panel-text placeholder:text-panel-muted focus:outline-none focus:border-brand-500"
              placeholder="API URL (https://mail.example.com)"
              value={form.url}
              onChange={(e) => setForm({ ...form, url: e.target.value })}
              required
            />
            <input
              className="sm:col-span-2 bg-panel-surface border border-panel-border rounded-lg px-3 py-2 text-panel-text placeholder:text-panel-muted focus:outline-none focus:border-brand-500"
              placeholder="Service token"
              value={form.service_token}
              onChange={(e) => setForm({ ...form, service_token: e.target.value })}
              required
            />
            <input
              className="sm:col-span-2 bg-panel-surface border border-panel-border rounded-lg px-3 py-2 text-panel-text placeholder:text-panel-muted focus:outline-none focus:border-brand-500"
              placeholder="Webmail URL (optional, defaults to <url>/mail/)"
              value={form.webmail_url}
              onChange={(e) => setForm({ ...form, webmail_url: e.target.value })}
            />
            <div className="sm:col-span-2">
              <Button type="submit" variant="secondary">Register deployment</Button>
            </div>
          </form>
        )}
      </Card>

      {/* Per-domain mail DNS */}
      <Card
        title="Per-domain mail DNS"
        description="Upserts MX, A (mail.<domain>), SPF, DKIM, and DMARC for the chosen domain via the registered mail-suite deployment."
      >
        <form onSubmit={enableMail} className="flex flex-col sm:flex-row gap-2 items-stretch sm:items-center">
          <input
            className="flex-1 bg-panel-bg border border-panel-border rounded-lg px-3 py-2 text-panel-text placeholder:text-panel-muted focus:outline-none focus:border-brand-500"
            placeholder="example.com"
            value={domain}
            onChange={(e) => setDomain(e.target.value.trim())}
            required
          />
          <Button type="submit" loading={busy}>Enable mail</Button>
          <Button type="button" variant="secondary" disabled={busy || !domain} onClick={verify}>
            Verify
          </Button>
        </form>

        {status && (
          <div className="mt-4 rounded-lg border border-panel-border overflow-hidden">
            <div className="px-4 py-2 border-b border-panel-border text-sm bg-panel-bg">
              <span className="text-panel-text font-medium">{status.domain}</span>{" "}
              — {status.all_ok ? (
                <span className="text-green-400">all OK</span>
              ) : (
                <span className="text-amber-400">not yet propagated</span>
              )}
            </div>
            <table className="w-full text-sm">
              <thead className="bg-panel-bg text-panel-muted">
                <tr>
                  <th className="text-left px-4 py-2 font-normal">Type</th>
                  <th className="text-left px-4 py-2 font-normal">Name</th>
                  <th className="text-left px-4 py-2 font-normal">Expected</th>
                  <th className="text-left px-4 py-2 font-normal">Found</th>
                  <th className="text-left px-4 py-2 font-normal">OK</th>
                </tr>
              </thead>
              <tbody>
                {status.records.map((r, i) => (
                  <tr key={i} className="border-t border-panel-border">
                    <td className="px-4 py-2 text-panel-text">{r.type}</td>
                    <td className="px-4 py-2 text-panel-text">{r.name}</td>
                    <td className="px-4 py-2 text-panel-muted">{r.expected}</td>
                    <td className="px-4 py-2 text-panel-muted">{r.found || "—"}</td>
                    <td className="px-4 py-2">{r.ok ? "✅" : "⏳"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}
