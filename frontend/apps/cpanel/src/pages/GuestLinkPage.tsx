import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { Card, Button, Modal, PasswordInput, generatePassword, confirmAction } from "@serverpanel/ui";
import toast, { Toaster } from "react-hot-toast";
import guestApi from "@/lib/guestApi";
import {
  Mail, Globe, Plus, Trash2, RefreshCw, ExternalLink, Clock,
  ShieldCheck, KeyRound, Forward, AlertTriangle,
} from "lucide-react";

// GuestLinkPage is the no-login magic-link surface (/user-panel/m/:token). It
// renders ONLY the single domain's email (and, for main domains, DNS) — no
// sidebar, no dashboard, no other data. Auth is the HttpOnly cookies set by
// POST /api/v1/guest/redeem; every call goes through guestApi (withCredentials).

interface Session {
  domain: string;
  link_type: "email" | "email_dns";
  max_mailboxes: number;
  default_quota_mb: number;
  default_send_per_hour: number;
  mailbox_count: number;
  window_expires_at: string;
}
interface Mailbox { id: string; email: string; quota_mb: number; used_mb: number; send_limit_per_hour: number; }
interface Forwarder { id: string; source: string; destinations: string[]; keep_copy: boolean; }
interface DnsRecord { id: string; type: string; name: string; value: string; ttl: number; priority?: number | null; }

const localPart = (email: string) => email.split("@")[0];
const errMsg = (e: any, fallback: string) =>
  e?.response?.data?.error?.message || e?.response?.data?.message || fallback;

const inputCls =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-brand-500";

const DNS_TYPES = ["A", "AAAA", "CNAME", "MX", "TXT", "SRV", "CAA", "NS"];

export default function GuestLinkPage() {
  const { token } = useParams();
  const [phase, setPhase] = useState<"loading" | "ready" | "error" | "expired">("loading");
  const [error, setError] = useState("");
  const [session, setSession] = useState<Session | null>(null);
  const [tab, setTab] = useState<"email" | "dns">("email");
  const [remaining, setRemaining] = useState(0);

  // --- redeem on mount, then load session ---------------------------------
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        await guestApi.post("/redeem", { token });
        // Drop the secret from the address bar + history immediately.
        try { window.history.replaceState(null, "", "/user-panel/m"); } catch { /* ignore */ }
        const s = (await guestApi.get("/session")).data.data as Session;
        if (cancelled) return;
        setSession(s);
        setPhase("ready");
      } catch (e: any) {
        if (cancelled) return;
        setError(errMsg(e, "This guest link is invalid, expired, or was opened in another browser."));
        setPhase("error");
      }
    })();
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // --- countdown ----------------------------------------------------------
  useEffect(() => {
    if (!session) return;
    const end = new Date(session.window_expires_at).getTime();
    const tick = () => {
      const secs = Math.max(0, Math.floor((end - Date.now()) / 1000));
      setRemaining(secs);
      if (secs <= 0) setPhase("expired");
    };
    tick();
    const id = window.setInterval(tick, 1000);
    return () => window.clearInterval(id);
  }, [session]);

  const mmss = (s: number) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;

  if (phase === "loading") {
    return <CenteredCard icon={<RefreshCw className="animate-spin text-brand-400" size={28} />} title="Opening secure session…" />;
  }
  if (phase === "error" || phase === "expired") {
    return (
      <CenteredCard
        icon={<AlertTriangle className="text-amber-400" size={28} />}
        title={phase === "expired" ? "Session expired" : "Link unavailable"}
        body={phase === "expired"
          ? "Your 30-minute access window has ended. Ask for a fresh link to continue."
          : error}
      />
    );
  }

  const s = session!;
  const showDns = s.link_type === "email_dns";

  return (
    <div className="min-h-screen bg-panel-bg text-panel-text">
      <Toaster position="top-right" />
      {/* Header */}
      <header className="border-b border-panel-border bg-panel-surface">
        <div className="max-w-4xl mx-auto px-4 py-4 flex items-center justify-between gap-4">
          <div className="flex items-center gap-3 min-w-0">
            <ShieldCheck size={22} className="text-brand-400 shrink-0" />
            <div className="min-w-0">
              <div className="font-semibold truncate flex items-center gap-2">
                <Globe size={14} className="text-brand-400" /> {s.domain}
              </div>
              <div className="text-xs text-panel-muted">
                Temporary access · {showDns ? "Email + DNS" : "Email"} only
              </div>
            </div>
          </div>
          <div className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg border text-sm font-medium tabular-nums ${
            remaining <= 120 ? "border-red-500/30 bg-red-500/10 text-red-300" : "border-panel-border bg-panel-bg text-panel-muted"
          }`} title="Time left in this session">
            <Clock size={14} /> {mmss(remaining)}
          </div>
        </div>
      </header>

      <main className="max-w-4xl mx-auto px-4 py-6 space-y-6">
        {/* Tabs */}
        {showDns && (
          <div className="flex items-center gap-2">
            <TabButton active={tab === "email"} onClick={() => setTab("email")} icon={<Mail size={14} />} label="Email" />
            <TabButton active={tab === "dns"} onClick={() => setTab("dns")} icon={<Globe size={14} />} label="DNS" />
          </div>
        )}

        {tab === "email" || !showDns ? (
          <EmailPanel session={s} onCount={(n) => setSession((p) => (p ? { ...p, mailbox_count: n } : p))} />
        ) : (
          <DnsPanel domain={s.domain} />
        )}

        <p className="text-center text-xs text-panel-muted/70">
          You're using a one-time guest link. It works only in this browser and expires when the timer runs out.
        </p>
      </main>
    </div>
  );
}

// ---- shared bits ---------------------------------------------------------

function CenteredCard({ icon, title, body }: { icon: React.ReactNode; title: string; body?: string }) {
  return (
    <div className="min-h-screen bg-panel-bg text-panel-text flex items-center justify-center px-4">
      <Card className="max-w-md w-full">
        <div className="p-8 text-center space-y-3">
          <div className="flex justify-center">{icon}</div>
          <h1 className="text-lg font-semibold">{title}</h1>
          {body && <p className="text-sm text-panel-muted">{body}</p>}
        </div>
      </Card>
    </div>
  );
}

function TabButton({ active, onClick, icon, label }: { active: boolean; onClick: () => void; icon: React.ReactNode; label: string }) {
  return (
    <button
      onClick={onClick}
      className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border text-sm transition-colors ${
        active ? "bg-brand-500/10 border-brand-500/30 text-brand-300" : "bg-panel-bg border-panel-border text-panel-muted hover:text-panel-text"
      }`}
    >
      {icon} {label}
    </button>
  );
}

// ---- Email panel ---------------------------------------------------------

function EmailPanel({ session, onCount }: { session: Session; onCount: (n: number) => void }) {
  const domain = session.domain;
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [forwarders, setForwarders] = useState<Forwarder[]>([]);
  const [loading, setLoading] = useState(true);

  const [showAdd, setShowAdd] = useState(false);
  const [newLocal, setNewLocal] = useState("");
  const [newPass, setNewPass] = useState("");
  const [saving, setSaving] = useState(false);

  const [pwTarget, setPwTarget] = useState<Mailbox | null>(null);
  const [pwValue, setPwValue] = useState("");

  const [showFwd, setShowFwd] = useState(false);
  const [fwdSource, setFwdSource] = useState("");
  const [fwdDest, setFwdDest] = useState("");

  const load = async () => {
    setLoading(true);
    try {
      const [mb, fw] = await Promise.all([
        guestApi.get("/mailboxes", { params: { limit: 1000 } }),
        guestApi.get("/forwarders"),
      ]);
      const rows = (mb.data.data || []) as Mailbox[];
      setMailboxes(rows);
      onCount(rows.length);
      setForwarders((fw.data.data || []) as Forwarder[]);
    } catch (e: any) {
      toast.error(errMsg(e, "Failed to load email"));
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => { load(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, []);

  const atCap = mailboxes.length >= session.max_mailboxes;

  const createMailbox = async () => {
    if (!newLocal.trim() || newPass.length < 8) {
      toast.error("Enter a name and a password of at least 8 characters");
      return;
    }
    setSaving(true);
    try {
      await guestApi.post("/mailboxes", { email: newLocal.trim(), password: newPass });
      toast.success("Mailbox created");
      setShowAdd(false); setNewLocal(""); setNewPass("");
      load();
    } catch (e: any) {
      toast.error(errMsg(e, "Failed to create mailbox"));
    } finally { setSaving(false); }
  };

  const resetPassword = async () => {
    if (!pwTarget || pwValue.length < 8) { toast.error("Password must be at least 8 characters"); return; }
    try {
      await guestApi.post(`/mailboxes/${localPart(pwTarget.email)}/password`, { password: pwValue });
      toast.success("Password updated");
      setPwTarget(null); setPwValue("");
    } catch (e: any) { toast.error(errMsg(e, "Failed to reset password")); }
  };

  const deleteMailbox = async (mb: Mailbox) => {
    if (!(await confirmAction({ title: "Delete mailbox?", description: `Delete ${mb.email}? This removes the account and its mail.`, danger: true, confirmLabel: "Delete" }))) return;
    try {
      await guestApi.delete(`/mailboxes/${localPart(mb.email)}`);
      toast.success("Mailbox deleted");
      load();
    } catch (e: any) { toast.error(errMsg(e, "Failed to delete mailbox")); }
  };

  const openWebmail = async (mb: Mailbox) => {
    try {
      const url = (await guestApi.post(`/mailboxes/${localPart(mb.email)}/webmail-link`)).data.data.url as string;
      window.open(url, "_blank", "noopener");
    } catch (e: any) { toast.error(errMsg(e, "Failed to open webmail")); }
  };

  const createForwarder = async () => {
    const dests = fwdDest.split(",").map((d) => d.trim()).filter(Boolean);
    if (!fwdSource.trim() || dests.length === 0) { toast.error("Enter a source and at least one destination"); return; }
    try {
      await guestApi.post("/forwarders", { source: fwdSource.trim(), destinations: dests, keep_copy: false });
      toast.success("Forwarder created");
      setShowFwd(false); setFwdSource(""); setFwdDest("");
      load();
    } catch (e: any) { toast.error(errMsg(e, "Failed to create forwarder")); }
  };

  const deleteForwarder = async (f: Forwarder) => {
    if (!(await confirmAction({ title: "Delete forwarder?", description: `Delete the forwarder for ${f.source}?`, danger: true, confirmLabel: "Delete" }))) return;
    try {
      await guestApi.delete(`/forwarders/${f.id}`);
      toast.success("Forwarder deleted");
      load();
    } catch (e: any) { toast.error(errMsg(e, "Failed to delete forwarder")); }
  };

  return (
    <div className="space-y-6">
      {/* Mailboxes */}
      <Card>
        <div className="p-4">
          <div className="flex items-center justify-between mb-3">
            <div>
              <h2 className="font-semibold flex items-center gap-2"><Mail size={16} className="text-brand-400" /> Mailboxes</h2>
              <p className="text-xs text-panel-muted">{mailboxes.length} of {session.max_mailboxes} used · {session.default_quota_mb} MB each</p>
            </div>
            <Button size="sm" disabled={atCap} onClick={() => setShowAdd(true)} title={atCap ? "Mailbox limit reached" : "Add mailbox"}>
              <Plus size={14} className="mr-1" /> Add
            </Button>
          </div>
          {loading ? <SkeletonRows /> : mailboxes.length === 0 ? (
            <Empty label="No mailboxes yet." />
          ) : (
            <table className="w-full text-sm">
              <thead><tr className="text-left text-xs text-panel-muted border-b border-panel-border">
                <th className="py-2">Address</th><th className="py-2">Quota</th><th className="py-2 text-right">Actions</th>
              </tr></thead>
              <tbody>
                {mailboxes.map((mb) => (
                  <tr key={mb.id} className="border-b border-panel-border/50">
                    <td className="py-2 font-medium">{mb.email}</td>
                    <td className="py-2 text-panel-muted">{mb.used_mb?.toFixed?.(0) ?? 0} / {mb.quota_mb} MB</td>
                    <td className="py-2">
                      <div className="flex items-center gap-1 justify-end">
                        <IconBtn title="Open webmail" onClick={() => openWebmail(mb)}><ExternalLink size={14} /></IconBtn>
                        <IconBtn title="Reset password" onClick={() => { setPwTarget(mb); setPwValue(""); }}><KeyRound size={14} /></IconBtn>
                        <IconBtn title="Delete" danger onClick={() => deleteMailbox(mb)}><Trash2 size={14} /></IconBtn>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </Card>

      {/* Forwarders */}
      <Card>
        <div className="p-4">
          <div className="flex items-center justify-between mb-3">
            <h2 className="font-semibold flex items-center gap-2"><Forward size={16} className="text-brand-400" /> Forwarders</h2>
            <Button size="sm" variant="secondary" onClick={() => setShowFwd(true)}><Plus size={14} className="mr-1" /> Add</Button>
          </div>
          {loading ? <SkeletonRows /> : forwarders.length === 0 ? (
            <Empty label="No forwarders yet." />
          ) : (
            <table className="w-full text-sm">
              <thead><tr className="text-left text-xs text-panel-muted border-b border-panel-border">
                <th className="py-2">Source</th><th className="py-2">Forwards to</th><th className="py-2 text-right">Actions</th>
              </tr></thead>
              <tbody>
                {forwarders.map((f) => (
                  <tr key={f.id} className="border-b border-panel-border/50">
                    <td className="py-2 font-medium">{f.source}</td>
                    <td className="py-2 text-panel-muted">{(f.destinations || []).join(", ")}</td>
                    <td className="py-2 text-right"><IconBtn title="Delete" danger onClick={() => deleteForwarder(f)}><Trash2 size={14} /></IconBtn></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </Card>

      {/* Add mailbox modal */}
      <Modal isOpen={showAdd} onClose={() => setShowAdd(false)} title="Add mailbox">
        <div className="space-y-4">
          <div>
            <label className="block text-sm font-medium mb-1.5">Address</label>
            <div className="flex items-center gap-2">
              <input className={inputCls} value={newLocal} onChange={(e) => setNewLocal(e.target.value)} placeholder="info" />
              <span className="text-panel-muted text-sm whitespace-nowrap">@{domain}</span>
            </div>
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">Password</label>
            <div className="flex items-center gap-2">
              <PasswordInput value={newPass} onChange={setNewPass} placeholder="At least 8 characters" />
              <Button type="button" variant="secondary" size="sm" onClick={() => setNewPass(generatePassword())}>Generate</Button>
            </div>
          </div>
          <div className="flex justify-end gap-2 pt-1">
            <Button variant="secondary" onClick={() => setShowAdd(false)}>Cancel</Button>
            <Button onClick={createMailbox} loading={saving}>Create</Button>
          </div>
        </div>
      </Modal>

      {/* Reset password modal */}
      <Modal isOpen={!!pwTarget} onClose={() => setPwTarget(null)} title={pwTarget ? `Reset password — ${pwTarget.email}` : "Reset password"} size="sm">
        <div className="space-y-4">
          <div className="flex items-center gap-2">
            <PasswordInput value={pwValue} onChange={setPwValue} placeholder="New password (min 8)" />
            <Button type="button" variant="secondary" size="sm" onClick={() => setPwValue(generatePassword())}>Generate</Button>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setPwTarget(null)}>Cancel</Button>
            <Button onClick={resetPassword}>Update</Button>
          </div>
        </div>
      </Modal>

      {/* Add forwarder modal */}
      <Modal isOpen={showFwd} onClose={() => setShowFwd(false)} title="Add forwarder">
        <div className="space-y-4">
          <div>
            <label className="block text-sm font-medium mb-1.5">Source</label>
            <div className="flex items-center gap-2">
              <input className={inputCls} value={fwdSource} onChange={(e) => setFwdSource(e.target.value)} placeholder="sales" />
              <span className="text-panel-muted text-sm whitespace-nowrap">@{domain}</span>
            </div>
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">Forward to</label>
            <input className={inputCls} value={fwdDest} onChange={(e) => setFwdDest(e.target.value)} placeholder="a@example.com, b@example.com" />
            <p className="text-xs text-panel-muted mt-1">Comma-separated.</p>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setShowFwd(false)}>Cancel</Button>
            <Button onClick={createForwarder}>Create</Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}

// ---- DNS panel -----------------------------------------------------------

function DnsPanel({ domain }: { domain: string }) {
  const [records, setRecords] = useState<DnsRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [showAdd, setShowAdd] = useState(false);
  const [form, setForm] = useState({ type: "A", name: "", value: "", ttl: 3600, priority: 10 });
  const [saving, setSaving] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      setRecords(((await guestApi.get("/dns/records")).data.data || []) as DnsRecord[]);
    } catch (e: any) {
      toast.error(errMsg(e, "Failed to load records"));
    } finally { setLoading(false); }
  };
  useEffect(() => { load(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, []);

  const addRecord = async () => {
    if (!form.name.trim() || !form.value.trim()) { toast.error("Name and value are required"); return; }
    setSaving(true);
    try {
      const body: any = { type: form.type, name: form.name.trim(), value: form.value.trim(), ttl: Number(form.ttl) || 3600 };
      if (form.type === "MX" || form.type === "SRV") body.priority = Number(form.priority) || 10;
      await guestApi.post("/dns/records", body);
      toast.success("Record added");
      setShowAdd(false); setForm({ type: "A", name: "", value: "", ttl: 3600, priority: 10 });
      load();
    } catch (e: any) {
      toast.error(errMsg(e, "Failed to add record"));
    } finally { setSaving(false); }
  };

  const deleteRecord = async (r: DnsRecord) => {
    if (!(await confirmAction({ title: "Delete record?", description: `Delete ${r.type} ${r.name}?`, danger: true, confirmLabel: "Delete" }))) return;
    try {
      await guestApi.delete(`/dns/records/${r.id}`);
      toast.success("Record deleted");
      load();
    } catch (e: any) { toast.error(errMsg(e, "Failed to delete record")); }
  };

  return (
    <Card>
      <div className="p-4">
        <div className="flex items-center justify-between mb-3">
          <div>
            <h2 className="font-semibold flex items-center gap-2"><Globe size={16} className="text-brand-400" /> DNS records</h2>
            <p className="text-xs text-panel-muted">A/AAAA records for the root (@) are managed by your provider and can't be changed here.</p>
          </div>
          <Button size="sm" onClick={() => setShowAdd(true)}><Plus size={14} className="mr-1" /> Add</Button>
        </div>
        {loading ? <SkeletonRows /> : records.length === 0 ? (
          <Empty label="No records." />
        ) : (
          <table className="w-full text-sm">
            <thead><tr className="text-left text-xs text-panel-muted border-b border-panel-border">
              <th className="py-2">Type</th><th className="py-2">Name</th><th className="py-2">Value</th><th className="py-2">TTL</th><th className="py-2 text-right">Actions</th>
            </tr></thead>
            <tbody>
              {records.map((r) => (
                <tr key={r.id} className="border-b border-panel-border/50">
                  <td className="py-2 font-medium">{r.type}</td>
                  <td className="py-2">{r.name}</td>
                  <td className="py-2 text-panel-muted break-all">{r.priority != null ? `${r.priority} ` : ""}{r.value}</td>
                  <td className="py-2 text-panel-muted">{r.ttl}</td>
                  <td className="py-2 text-right"><IconBtn title="Delete" danger onClick={() => deleteRecord(r)}><Trash2 size={14} /></IconBtn></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <Modal isOpen={showAdd} onClose={() => setShowAdd(false)} title="Add DNS record">
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium mb-1.5">Type</label>
              <select className={inputCls} value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })}>
                {DNS_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5">TTL</label>
              <input className={inputCls} type="number" value={form.ttl} onChange={(e) => setForm({ ...form, ttl: Number(e.target.value) })} />
            </div>
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">Name</label>
            <div className="flex items-center gap-2">
              <input className={inputCls} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="www (or a subdomain label)" />
              <span className="text-panel-muted text-xs whitespace-nowrap">.{domain}</span>
            </div>
          </div>
          {(form.type === "MX" || form.type === "SRV") && (
            <div>
              <label className="block text-sm font-medium mb-1.5">Priority</label>
              <input className={inputCls} type="number" value={form.priority} onChange={(e) => setForm({ ...form, priority: Number(e.target.value) })} />
            </div>
          )}
          <div>
            <label className="block text-sm font-medium mb-1.5">Value</label>
            <input className={inputCls} value={form.value} onChange={(e) => setForm({ ...form, value: e.target.value })} placeholder="Target / IP / text" />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setShowAdd(false)}>Cancel</Button>
            <Button onClick={addRecord} loading={saving}>Add</Button>
          </div>
        </div>
      </Modal>
    </Card>
  );
}

// ---- tiny shared UI ------------------------------------------------------

function IconBtn({ children, title, onClick, danger }: { children: React.ReactNode; title: string; onClick: () => void; danger?: boolean }) {
  return (
    <button
      title={title}
      onClick={onClick}
      className={`p-1.5 rounded hover:bg-panel-bg text-panel-muted transition-colors ${danger ? "hover:text-red-400" : "hover:text-brand-400"}`}
    >
      {children}
    </button>
  );
}
function Empty({ label }: { label: string }) {
  return <div className="text-center py-8 text-sm text-panel-muted">{label}</div>;
}
function SkeletonRows() {
  return <div className="space-y-2">{[1, 2, 3].map((i) => <div key={i} className="h-9 bg-panel-border/20 rounded animate-pulse" />)}</div>;
}
