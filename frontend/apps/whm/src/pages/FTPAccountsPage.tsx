import { useState, useEffect } from "react";
import { Card, Button, Table, Modal, PasswordInput, confirmAction, copyToClipboard } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { useAuthStore } from "@/store/auth";
import { FolderKey, Plus, RefreshCw, Trash2, KeyRound, Copy, Server, AlertCircle, Users } from "lucide-react";

interface FTPAccount {
  id: string;
  username: string;
  domain: string;
  home_dir: string;
  user: string;
  is_root: boolean;
  host: string;
  port: number;
  created_at: string;
  password?: string;
}

interface DomainOption {
  id: string;
  domain: string;
}

export default function FTPAccountsPage() {
  const { user } = useAuthStore();
  const perms = user?.permissions || [];
  const isOwner = user?.role === "vendor_owner";
  const canCreate = isOwner || perms.includes("ftp.create");
  const canManage = isOwner || perms.includes("ftp.manage");

  const [accounts, setAccounts] = useState<FTPAccount[]>([]);
  const [domains, setDomains] = useState<DomainOption[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [creating, setCreating] = useState(false);

  const [showCreate, setShowCreate] = useState(false);
  const [formDomain, setFormDomain] = useState("");
  const [formUsername, setFormUsername] = useState("");
  const [formPassword, setFormPassword] = useState("");

  const [credential, setCredential] = useState<FTPAccount | null>(null);

  const [resetFor, setResetFor] = useState<FTPAccount | null>(null);
  const [resetPassword, setResetPassword] = useState("");
  const [resetting, setResetting] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const r = await api.get("/ftp");
      setAccounts(r.data?.data || []);
    } catch {
      toast.error("Failed to load FTP accounts");
    } finally {
      setLoading(false);
    }
  };

  const loadDomains = async () => {
    try {
      const r = await api.get("/domains?limit=1000");
      setDomains((r.data?.data || []) as DomainOption[]);
    } catch { /* non-fatal */ }
  };

  useEffect(() => {
    load();
    loadDomains();
  }, []);

  const openCreate = () => {
    setFormDomain(domains[0]?.domain || "");
    setFormUsername("");
    setFormPassword("");
    setShowCreate(true);
  };

  const submitCreate = async () => {
    if (!formDomain) { toast.error("Pick a domain"); return; }
    setCreating(true);
    try {
      const r = await api.post("/ftp", {
        domain: formDomain,
        username: formUsername.trim(),
        password: formPassword,
      });
      setShowCreate(false);
      setCredential(r.data?.data || null);
      toast.success("FTP account created");
      load();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to create FTP account");
    } finally {
      setCreating(false);
    }
  };

  const submitReset = async () => {
    if (!resetFor) return;
    setResetting(true);
    try {
      const r = await api.put(`/ftp/${resetFor.id}/password`, { password: resetPassword });
      setResetFor(null);
      setResetPassword("");
      setCredential(r.data?.data || null);
      toast.success("Password reset");
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to reset password");
    } finally {
      setResetting(false);
    }
  };

  const remove = async (a: FTPAccount) => {
    const ok = await confirmAction({
      title: "Delete FTP account",
      description: `Delete FTP account "${a.username}"? Anyone using these credentials will lose access immediately.`,
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    try {
      await api.delete(`/ftp/${a.id}`);
      toast.success("FTP account deleted");
      load();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to delete");
    }
  };

  const filtered = accounts.filter((a) => {
    const q = search.trim().toLowerCase();
    if (!q) return true;
    return a.username.toLowerCase().includes(q) || a.domain.toLowerCase().includes(q) || (a.user || "").toLowerCase().includes(q);
  });

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between flex-wrap gap-2">
        <div>
          <h1 className="text-xl font-semibold text-panel-text flex items-center gap-2">
            <FolderKey size={20} className="text-blue-400" /> FTP Accounts
          </h1>
          <p className="text-sm text-panel-muted mt-0.5">
            Pure-FTPd virtual users. One primary account per domain (auto-created); add extra accounts as needed.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search username / domain / owner"
            className="bg-panel-bg border border-panel-border rounded-lg px-3 py-2 text-sm text-panel-text w-64"
          />
          <Button variant="secondary" onClick={load}><RefreshCw size={14} /> Refresh</Button>
          {canCreate && <Button onClick={openCreate}><Plus size={14} /> New FTP Account</Button>}
        </div>
      </div>

      <Card className="p-3 flex items-start gap-2 text-xs text-panel-muted">
        <Server size={15} className="text-blue-400 mt-0.5 shrink-0" />
        <div>
          Host = <b className="text-panel-text">{accounts[0]?.host || "server IP"}</b>, Port <b className="text-panel-text">21</b> (explicit FTPS / TLS recommended).
          Passwords are shown <b className="text-panel-text">once</b> at creation / reset — Pure-FTPd stores only a hash.
        </div>
      </Card>

      <Card>
        <Table
          loading={loading}
          emptyMessage="No FTP accounts yet."
          columns={[
            { header: "Username", accessor: (a: FTPAccount) => (
              <div className="flex items-center gap-2">
                <span className="font-mono text-sm text-panel-text">{a.username}</span>
                {a.is_root && <span className="text-[10px] px-1.5 py-0.5 rounded bg-blue-500/10 border border-blue-500/30 text-blue-300">primary</span>}
              </div>
            ) },
            { header: "Domain", accessor: (a: FTPAccount) => <span className="text-sm">{a.domain}</span> },
            { header: "Owner", accessor: (a: FTPAccount) => (
              <div className="flex items-center gap-1 text-panel-muted text-sm"><Users size={12} /> {a.user || "—"}</div>
            ) },
            { header: "Directory", accessor: (a: FTPAccount) => <span className="font-mono text-xs text-panel-muted">{a.home_dir}</span> },
            { header: "", accessor: (a: FTPAccount) => (
              <div className="flex items-center justify-end gap-1">
                {canManage && (
                  <button onClick={() => { setResetFor(a); setResetPassword(""); }} className="p-1.5 text-panel-muted hover:text-blue-400" title="Reset password"><KeyRound size={15} /></button>
                )}
                {canManage && !a.is_root && (
                  <button onClick={() => remove(a)} className="p-1.5 text-panel-muted hover:text-red-400" title="Delete account"><Trash2 size={15} /></button>
                )}
                {a.is_root && <span className="text-[10px] text-panel-muted/70 pr-1" title="The domain's primary FTP account cannot be deleted">locked</span>}
              </div>
            ) },
          ]}
          data={filtered}
        />
      </Card>

      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="New FTP Account">
        <div className="space-y-3">
          <div>
            <label className="text-xs text-panel-muted">Domain</label>
            <select
              value={formDomain}
              onChange={(e) => setFormDomain(e.target.value)}
              className="w-full mt-1 bg-panel-bg border border-panel-border rounded-lg px-3 py-2 text-sm text-panel-text"
            >
              {domains.length === 0 && <option value="">No domains</option>}
              {domains.map((d) => <option key={d.id} value={d.domain}>{d.domain}</option>)}
            </select>
          </div>
          <div>
            <label className="text-xs text-panel-muted">Username (optional)</label>
            <input
              value={formUsername}
              onChange={(e) => setFormUsername(e.target.value)}
              placeholder="e.g. uploads"
              className="w-full mt-1 bg-panel-bg border border-panel-border rounded-lg px-3 py-2 text-sm text-panel-text"
            />
            <p className="text-[11px] text-panel-muted/70 mt-1">
              Prefixed with the domain owner's account name (letters, digits, underscores only). Blank uses the domain.
            </p>
          </div>
          <div>
            <label className="text-xs text-panel-muted">Password (optional)</label>
            <PasswordInput value={formPassword} onChange={setFormPassword} placeholder="Leave blank to auto-generate" />
          </div>
          <div className="flex justify-end gap-2 pt-1">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button onClick={submitCreate} disabled={creating}>{creating ? "Creating…" : "Create"}</Button>
          </div>
        </div>
      </Modal>

      <Modal isOpen={!!resetFor} onClose={() => setResetFor(null)} title={`Reset password — ${resetFor?.username || ""}`}>
        <div className="space-y-3">
          <div>
            <label className="text-xs text-panel-muted">New password (optional)</label>
            <PasswordInput value={resetPassword} onChange={setResetPassword} placeholder="Leave blank to auto-generate" />
          </div>
          <div className="flex justify-end gap-2 pt-1">
            <Button variant="secondary" onClick={() => setResetFor(null)}>Cancel</Button>
            <Button onClick={submitReset} disabled={resetting}>{resetting ? "Resetting…" : "Reset password"}</Button>
          </div>
        </div>
      </Modal>

      <Modal isOpen={!!credential} onClose={() => setCredential(null)} title="FTP credentials">
        {credential && (
          <div className="space-y-3">
            <div className="flex items-start gap-2 text-xs text-amber-300 bg-amber-500/10 border border-amber-500/30 rounded-lg p-2">
              <AlertCircle size={15} className="mt-0.5 shrink-0" />
              <span>Copy these now — the password is shown only this once and can't be retrieved later (only reset).</span>
            </div>
            {([
              ["Host", credential.host],
              ["Port", String(credential.port)],
              ["Username", credential.username],
              ["Password", credential.password || "(unchanged)"],
            ] as [string, string][]).map(([k, v]) => (
              <div key={k} className="flex items-center justify-between gap-2 bg-panel-bg border border-panel-border rounded-lg px-3 py-2">
                <div className="min-w-0">
                  <div className="text-[10px] uppercase tracking-wide text-panel-muted">{k}</div>
                  <div className="font-mono text-sm text-panel-text break-all">{v}</div>
                </div>
                <button onClick={() => copyToClipboard(v)} className="p-1.5 text-panel-muted hover:text-blue-400 shrink-0" title={`Copy ${k}`}><Copy size={15} /></button>
              </div>
            ))}
            <div className="flex justify-end pt-1">
              <Button onClick={() => setCredential(null)}>Done</Button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
