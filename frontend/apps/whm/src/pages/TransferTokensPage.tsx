import { useEffect, useState } from "react";
import { Card, Button, Modal, confirmAction, copyToClipboard } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Key, Plus, RefreshCw, Copy, Check, Trash2, AlertTriangle, ShieldCheck, Clock } from "lucide-react";

interface TransferToken {
  id: string;
  label: string;
  token_prefix: string;
  ssh_user: string;
  ssh_port: number;
  status: string; // active | revoked | expired
  created_at: string;
  expires_at: string;
  redeemed_at?: string;
  redeemed_from_ip?: string;
  revoked_at?: string;
  created_by_email?: string;
  // Server returns plain_token only on the issue response — never on list.
  plain_token?: string;
}

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

function formatExpiry(iso: string): string {
  const d = new Date(iso);
  const diffMs = d.getTime() - Date.now();
  if (diffMs <= 0) return "expired";
  const mins = Math.floor(diffMs / 60000);
  if (mins < 60) return `in ${mins}m`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 48) return `in ${hrs}h`;
  return `in ${Math.floor(hrs / 24)}d`;
}

export default function TransferTokensPage() {
  const [tokens, setTokens] = useState<TransferToken[]>([]);
  const [loading, setLoading] = useState(true);
  const [showIssue, setShowIssue] = useState(false);
  const [issuing, setIssuing] = useState(false);
  const [issued, setIssued] = useState<TransferToken | null>(null);
  const [copied, setCopied] = useState(false);

  const [form, setForm] = useState({ label: "", ttl_hours: "24", ssh_port: "22" });

  const fetchTokens = async () => {
    setLoading(true);
    try {
      const res = await api.get("/transfers/tokens");
      setTokens(res.data.data || []);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to load tokens");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchTokens(); }, []);

  const handleIssue = async () => {
    setIssuing(true);
    try {
      const res = await api.post("/transfers/tokens", {
        label: form.label.trim(),
        ttl_hours: parseInt(form.ttl_hours) || 24,
        ssh_port: parseInt(form.ssh_port) || 22,
      });
      const tok: TransferToken = res.data.data;
      setIssued(tok);
      setShowIssue(false);
      setForm({ label: "", ttl_hours: "24", ssh_port: "22" });
      fetchTokens();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to issue token");
    } finally {
      setIssuing(false);
    }
  };

  const handleRevoke = async (id: string) => {
    if (!await confirmAction({ title: "Revoke token?", description: "The destination panel using this token will lose SSH access immediately. This cannot be undone.", danger: true, confirmLabel: "Revoke" })) return;
    try {
      await api.delete(`/transfers/tokens/${id}`);
      toast.success("Token revoked");
      fetchTokens();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to revoke");
    }
  };

  const copyTokenToClipboard = async (text: string) => {
    if (await copyToClipboard(text)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } else {
      toast.error("Copy failed — select manually");
    }
  };

  const statusPill = (status: string, expiresAt: string) => {
    const expired = status === "expired" || (status === "active" && new Date(expiresAt) <= new Date());
    if (status === "revoked") {
      return <span className="px-2 py-0.5 text-[11px] uppercase rounded bg-red-500/15 text-red-300 font-medium">Revoked</span>;
    }
    if (expired) {
      return <span className="px-2 py-0.5 text-[11px] uppercase rounded bg-panel-border/40 text-panel-muted font-medium">Expired</span>;
    }
    return <span className="px-2 py-0.5 text-[11px] uppercase rounded bg-emerald-500/15 text-emerald-300 font-medium">Active</span>;
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Transfer Tokens</h1>
          <p className="text-panel-muted text-sm mt-1">
            One-shot tokens for migrating away from this server. The destination panel pastes the token in step 1 of its transfer wizard — no root password leaves this box.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button onClick={fetchTokens} className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm">
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
          </Button>
          <Button onClick={() => setShowIssue(true)}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
            <Plus size={14} /> Generate Token
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-0">
          {loading ? (
            <div className="p-8 space-y-3">
              {[1, 2, 3].map((i) => (<div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />))}
            </div>
          ) : tokens.length === 0 ? (
            <div className="text-center py-16 px-4">
              <Key size={48} className="text-panel-muted/20 mx-auto mb-4" />
              <h3 className="text-lg font-medium text-panel-text mb-1">No tokens yet</h3>
              <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
                Generate a transfer token here, then paste it on the destination Betazen Server Panel under Server → Transfer.
              </p>
              <Button onClick={() => setShowIssue(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
                <Plus size={14} /> Generate Token
              </Button>
            </div>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-panel-border text-left text-xs uppercase tracking-wider text-panel-muted">
                  <th className="px-4 py-3 font-medium">Label</th>
                  <th className="px-4 py-3 font-medium">Token</th>
                  <th className="px-4 py-3 font-medium">Status</th>
                  <th className="px-4 py-3 font-medium">Expires</th>
                  <th className="px-4 py-3 font-medium">Last Redeemed</th>
                  <th className="px-4 py-3 font-medium text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-panel-border/40">
                {tokens.map((t) => (
                  <tr key={t.id} className="hover:bg-panel-bg/30">
                    <td className="px-4 py-3">
                      <div className="text-panel-text font-medium">{t.label}</div>
                      {t.created_by_email && (
                        <div className="text-[11px] text-panel-muted">by {t.created_by_email}</div>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <code className="text-xs text-panel-muted">{t.token_prefix}…</code>
                    </td>
                    <td className="px-4 py-3">{statusPill(t.status, t.expires_at)}</td>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-1.5 text-panel-muted text-xs">
                        <Clock size={12} />
                        <span>{formatExpiry(t.expires_at)}</span>
                      </div>
                      <div className="text-[11px] text-panel-muted">{new Date(t.expires_at).toLocaleString()}</div>
                    </td>
                    <td className="px-4 py-3 text-xs">
                      {t.redeemed_at ? (
                        <div>
                          <div className="text-panel-text">{new Date(t.redeemed_at).toLocaleString()}</div>
                          {t.redeemed_from_ip && (
                            <div className="text-[11px] text-panel-muted">from {t.redeemed_from_ip}</div>
                          )}
                        </div>
                      ) : (
                        <span className="text-panel-muted">—</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-right">
                      {t.status === "active" && (
                        <button onClick={() => handleRevoke(t.id)}
                          className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-panel-muted hover:text-red-400 hover:bg-red-500/10 transition-colors"
                          title="Revoke">
                          <Trash2 size={12} /> Revoke
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </Card>

      {/* Issue modal */}
      <Modal isOpen={showIssue} onClose={() => setShowIssue(false)} title="Generate Transfer Token">
        <div className="space-y-4">
          <div className="p-3 rounded border border-blue-500/20 bg-blue-500/5 text-xs text-blue-200/90">
            This token will let the destination panel SSH into this server as <code>root</code> for the lifetime you set below — and only that long. Behind the scenes we install a one-shot SSH key in <code>/root/.ssh/authorized_keys</code> and remove it as soon as the token expires or you revoke it.
          </div>

          <div>
            <label className={labelClass}>Label <span className="text-panel-muted font-normal">(for your records)</span></label>
            <input type="text" placeholder="e.g. cutover to dc2-vps-01"
              value={form.label} onChange={(e) => setForm({ ...form, label: e.target.value })}
              className={inputClass} />
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Lifetime (hours)</label>
              <input type="number" min={1} max={168}
                value={form.ttl_hours} onChange={(e) => setForm({ ...form, ttl_hours: e.target.value })}
                className={inputClass} />
              <p className="text-[11px] text-panel-muted mt-1">Max 168 (7 days). The key is removed automatically when the token expires.</p>
            </div>
            <div>
              <label className={labelClass}>SSH Port</label>
              <input type="number" value={form.ssh_port}
                onChange={(e) => setForm({ ...form, ssh_port: e.target.value })}
                className={inputClass} />
              <p className="text-[11px] text-panel-muted mt-1">Whatever port this server's sshd listens on.</p>
            </div>
          </div>

          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowIssue(false)} className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Cancel</button>
            <button type="button" onClick={handleIssue} disabled={issuing}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50 inline-flex items-center gap-2">
              {issuing ? "Generating..." : (<><ShieldCheck size={14} /> Generate</>)}
            </button>
          </div>
        </div>
      </Modal>

      {/* Reveal modal — shown ONCE after issue */}
      <Modal isOpen={!!issued} onClose={() => setIssued(null)} title="Copy this token now" size="lg">
        {issued && (
          <div className="space-y-4">
            <div className="flex items-start gap-3 p-3 rounded border border-amber-500/30 bg-amber-500/10">
              <AlertTriangle size={16} className="text-amber-400 mt-0.5 shrink-0" />
              <div className="text-xs text-amber-100/90">
                <p className="font-medium text-amber-200 mb-1">This is the only time this token will be shown.</p>
                <p>Copy it now and paste it into the destination panel's transfer wizard. After you close this dialog, only the prefix is recoverable.</p>
              </div>
            </div>

            <div>
              <label className={labelClass}>Token</label>
              <div className="flex items-stretch gap-2">
                <code className="flex-1 px-3 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-xs text-panel-text font-mono break-all">
                  {issued.plain_token}
                </code>
                <button type="button" onClick={() => issued.plain_token && copyTokenToClipboard(issued.plain_token)}
                  className="px-3 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors inline-flex items-center gap-1.5">
                  {copied ? <Check size={14} /> : <Copy size={14} />}
                  {copied ? "Copied" : "Copy"}
                </button>
              </div>
            </div>

            <div className="grid grid-cols-3 gap-3 text-xs">
              <div>
                <p className="text-panel-muted">Label</p>
                <p className="text-panel-text">{issued.label}</p>
              </div>
              <div>
                <p className="text-panel-muted">SSH</p>
                <p className="text-panel-text">{issued.ssh_user}@…:{issued.ssh_port}</p>
              </div>
              <div>
                <p className="text-panel-muted">Expires</p>
                <p className="text-panel-text">{new Date(issued.expires_at).toLocaleString()}</p>
              </div>
            </div>

            <div className="flex justify-end pt-2">
              <button type="button" onClick={() => setIssued(null)}
                className="px-4 py-2 text-sm bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors">
                Done
              </button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
