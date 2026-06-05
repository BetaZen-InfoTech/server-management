// MailSuitePage — owner-only admin for the SEPARATE mail-suite product.
//
// Two responsibilities:
//   1. Register / list / delete known mail-suite deployments (URL +
//      service token). Each deployment maps to a running mail-suite
//      backend somewhere; we record where + how to authenticate to it.
//   2. Per-domain "Enable Mail" + status — proxied through the panel
//      to the registered mail-suite backend's /api/v1/dns endpoints.
//
// Does NOT touch the existing Email page or Roundcube SSO flow.

import { useEffect, useState, FormEvent } from "react";
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
  const [form, setForm] = useState({ label: "", url: "", service_token: "", webmail_url: "" });
  const [domain, setDomain] = useState("");
  const [status, setStatus] = useState<DNSStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  function flash(kind: "ok" | "err", text: string) {
    setMsg({ kind, text });
    window.setTimeout(() => setMsg(null), 4000);
  }

  async function loadDeps() {
    setLoadingDeps(true);
    try {
      const r = await api.get("/mail-suite/deployments");
      setDeps(r.data?.data || []);
    } catch (e: any) {
      flash("err", e?.response?.data?.error || "Could not load deployments");
    } finally {
      setLoadingDeps(false);
    }
  }

  useEffect(() => { void loadDeps(); }, []);

  async function register(e: FormEvent) {
    e.preventDefault();
    try {
      await api.post("/mail-suite/deployments", form);
      setForm({ label: "", url: "", service_token: "", webmail_url: "" });
      flash("ok", "Deployment registered");
      void loadDeps();
    } catch (e: any) {
      flash("err", e?.response?.data?.error || "Register failed");
    }
  }

  async function removeDep(id: string) {
    if (!confirm("Remove this deployment registration?")) return;
    try {
      await api.delete(`/mail-suite/deployments/${id}`);
      flash("ok", "Removed");
      void loadDeps();
    } catch (e: any) {
      flash("err", e?.response?.data?.error || "Delete failed");
    }
  }

  async function enableMail(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await api.post(`/mail-suite/domains/${encodeURIComponent(domain)}/enable-mail`);
      setStatus(r.data?.data);
      flash("ok", "Records upserted — verify below");
    } catch (e: any) {
      flash("err", e?.response?.data?.error || "Enable failed");
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
    <div className="p-6 space-y-6 max-w-5xl mx-auto">
      <header>
        <h1 className="text-2xl font-semibold">Mail Suite</h1>
        <p className="text-sm text-gray-500">
          Register mail-suite deployments and enable per-domain mail records. The classic
          Email page and Roundcube SSO are unaffected.
        </p>
      </header>

      {msg && (
        <div className={`rounded-lg px-4 py-2 text-sm ${msg.kind === "ok" ? "bg-green-50 text-green-800" : "bg-red-50 text-red-800"}`}>
          {msg.text}
        </div>
      )}

      <section className="space-y-3">
        <h2 className="font-medium">Registered deployments</h2>
        <div className="rounded-lg border bg-white overflow-hidden">
          {loadingDeps && <div className="p-4 text-sm text-gray-500">Loading…</div>}
          {!loadingDeps && deps.length === 0 && (
            <div className="p-4 text-sm text-gray-500">No deployments yet.</div>
          )}
          {deps.map((d) => (
            <div key={d.id} className="flex items-center justify-between p-3 border-b last:border-b-0">
              <div className="min-w-0">
                <div className="font-medium truncate">{d.label}</div>
                <div className="text-xs text-gray-500 truncate">{d.url}</div>
              </div>
              <div className="flex items-center gap-2">
                <a className="text-sm text-indigo-600 underline" href={d.webmail_url} target="_blank" rel="noreferrer">Open webmail</a>
                <button onClick={() => removeDep(d.id)} className="text-sm text-red-600">Remove</button>
              </div>
            </div>
          ))}
        </div>

        <form onSubmit={register} className="grid grid-cols-2 gap-3 rounded-lg border bg-white p-4">
          <input className="rounded border px-3 py-2 text-sm" placeholder="Label (e.g. Primary)" value={form.label} onChange={(e) => setForm({ ...form, label: e.target.value })} required />
          <input className="rounded border px-3 py-2 text-sm" placeholder="API URL (https://mail-suite.example.com)" value={form.url} onChange={(e) => setForm({ ...form, url: e.target.value })} required />
          <input className="rounded border px-3 py-2 text-sm col-span-2" placeholder="Service token" value={form.service_token} onChange={(e) => setForm({ ...form, service_token: e.target.value })} required />
          <input className="rounded border px-3 py-2 text-sm col-span-2" placeholder="Webmail URL (optional, defaults to <url>/mail/)" value={form.webmail_url} onChange={(e) => setForm({ ...form, webmail_url: e.target.value })} />
          <div className="col-span-2">
            <button className="rounded bg-indigo-600 text-white px-4 py-2 text-sm" type="submit">Register deployment</button>
          </div>
        </form>
      </section>

      <section className="space-y-3">
        <h2 className="font-medium">Per-domain mail DNS</h2>
        <p className="text-sm text-gray-500">
          Upserts MX, A (mail.&lt;domain&gt;), SPF, DKIM, and DMARC for the chosen domain via the registered mail-suite deployment.
        </p>
        <form onSubmit={enableMail} className="flex gap-2 items-center">
          <input className="flex-1 rounded border px-3 py-2 text-sm" placeholder="example.com" value={domain} onChange={(e) => setDomain(e.target.value)} required />
          <button className="rounded bg-indigo-600 text-white px-4 py-2 text-sm" type="submit" disabled={busy}>Enable mail</button>
          <button type="button" className="rounded border px-4 py-2 text-sm" disabled={busy || !domain} onClick={verify}>Verify</button>
        </form>

        {status && (
          <div className="rounded-lg border overflow-hidden bg-white">
            <div className="px-4 py-2 border-b text-sm">
              {status.domain} — {status.all_ok ? <span className="text-green-700">all OK</span> : <span className="text-amber-700">not yet propagated</span>}
            </div>
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-gray-500">
                <tr>
                  <th className="text-left px-3 py-2">Type</th>
                  <th className="text-left px-3 py-2">Name</th>
                  <th className="text-left px-3 py-2">Expected</th>
                  <th className="text-left px-3 py-2">Found</th>
                  <th className="text-left px-3 py-2">OK</th>
                </tr>
              </thead>
              <tbody>
                {status.records.map((r, i) => (
                  <tr key={i} className="border-t">
                    <td className="px-3 py-2">{r.type}</td>
                    <td className="px-3 py-2">{r.name}</td>
                    <td className="px-3 py-2 text-gray-600">{r.expected}</td>
                    <td className="px-3 py-2 text-gray-600">{r.found || "—"}</td>
                    <td className="px-3 py-2">{r.ok ? "✅" : "⏳"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
