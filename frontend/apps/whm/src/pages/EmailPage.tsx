import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Mail, Plus, RefreshCw, Search, Trash2, Edit, Eye, ExternalLink,
  Send, Shield, ArrowRight, Copy, Settings, X, Key
} from "lucide-react";

interface Mailbox {
  id: string;
  email: string;
  domain: string;
  quota_mb: number;
  used_mb: number;
  send_limit_per_hour: number;
  created_at: string;
  updated_at?: string;
}

interface Forwarder {
  id: string;
  source: string;
  destinations: string[];
  keep_copy: boolean;
  domain: string;
  created_at: string;
}

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

type Tab = "mailboxes" | "forwarders" | "spam";

export default function EmailPage() {
  const [activeTab, setActiveTab] = useState<Tab>("mailboxes");
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [forwarders, setForwarders] = useState<Forwarder[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  // Create mailbox modal
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ email: "", password: "", domain: "", quota_mb: 500, send_limit_per_hour: 100 });

  // View details modal
  const [showDetails, setShowDetails] = useState(false);
  const [selectedMailbox, setSelectedMailbox] = useState<Mailbox | null>(null);

  // Edit mailbox modal
  const [showEdit, setShowEdit] = useState(false);
  const [editForm, setEditForm] = useState({ quota_mb: 500, send_limit_per_hour: 100, password: "" });
  const [saving, setSaving] = useState(false);

  // Create forwarder modal
  const [showCreateForwarder, setShowCreateForwarder] = useState(false);
  const [creatingForwarder, setCreatingForwarder] = useState(false);
  const [forwarderForm, setForwarderForm] = useState({ source: "", destinations: "", keep_copy: true });

  // Spam settings
  const [spamDomain, setSpamDomain] = useState("");
  const [spamForm, setSpamForm] = useState({ spam_threshold: 5.0, spam_action: "flag", whitelist: "", blacklist: "", clamav_enabled: false });
  const [savingSpam, setSavingSpam] = useState(false);

  // DKIM
  const [dkimDomain, setDkimDomain] = useState("");
  const [settingUpDkim, setSettingUpDkim] = useState(false);

  useEffect(() => { fetchMailboxes(); }, []);
  useEffect(() => { if (activeTab === "forwarders") fetchForwarders(); }, [activeTab]);

  const fetchMailboxes = async () => {
    setLoading(true);
    try {
      const res = await api.get("/email/");
      setMailboxes(res.data.data || []);
    } catch { /* keep empty */ } finally { setLoading(false); }
  };

  const fetchForwarders = async () => {
    setLoading(true);
    try {
      const res = await api.get("/email/forwarders");
      setForwarders(res.data.data || []);
    } catch { /* keep empty */ } finally { setLoading(false); }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.email || !form.password || !form.domain) { toast.error("Please fill all required fields"); return; }
    setCreating(true);
    try {
      await api.post("/email/", form);
      toast.success(`Mailbox ${form.email} created`);
      setShowCreate(false);
      setForm({ email: "", password: "", domain: "", quota_mb: 500, send_limit_per_hour: 100 });
      fetchMailboxes();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create mailbox");
    } finally { setCreating(false); }
  };

  const handleEdit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedMailbox) return;
    setSaving(true);
    try {
      const updates: any = { quota_mb: editForm.quota_mb, send_limit_per_hour: editForm.send_limit_per_hour };
      if (editForm.password) updates.password = editForm.password;
      await api.put(`/email/${selectedMailbox.id}`, updates);
      toast.success(`Mailbox ${selectedMailbox.email} updated`);
      setShowEdit(false);
      fetchMailboxes();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update mailbox");
    } finally { setSaving(false); }
  };

  const handleDelete = async (id: string, email: string) => {
    if (!confirm(`Are you sure you want to delete mailbox ${email}?`)) return;
    try {
      await api.delete(`/email/${id}`);
      toast.success(`Mailbox ${email} deleted`);
      fetchMailboxes();
    } catch { toast.error("Failed to delete mailbox"); }
  };

  const handleCreateForwarder = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!forwarderForm.source || !forwarderForm.destinations) { toast.error("Please fill all required fields"); return; }
    setCreatingForwarder(true);
    try {
      await api.post("/email/forwarders", {
        source: forwarderForm.source,
        destinations: forwarderForm.destinations.split(",").map((d) => d.trim()).filter(Boolean),
        keep_copy: forwarderForm.keep_copy,
      });
      toast.success("Forwarder created");
      setShowCreateForwarder(false);
      setForwarderForm({ source: "", destinations: "", keep_copy: true });
      fetchForwarders();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create forwarder");
    } finally { setCreatingForwarder(false); }
  };

  const handleDeleteForwarder = async (id: string, source: string) => {
    if (!confirm(`Delete forwarder for ${source}?`)) return;
    try {
      await api.delete(`/email/forwarders/${id}`);
      toast.success("Forwarder deleted");
      fetchForwarders();
    } catch { toast.error("Failed to delete forwarder"); }
  };

  const handleSaveSpam = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!spamDomain) { toast.error("Enter a domain"); return; }
    setSavingSpam(true);
    try {
      await api.put(`/email/spam-settings/${spamDomain}`, {
        ...spamForm,
        whitelist: spamForm.whitelist ? spamForm.whitelist.split(",").map((s) => s.trim()).filter(Boolean) : [],
        blacklist: spamForm.blacklist ? spamForm.blacklist.split(",").map((s) => s.trim()).filter(Boolean) : [],
      });
      toast.success("Spam settings updated");
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update spam settings");
    } finally { setSavingSpam(false); }
  };

  const handleSetupDkim = async () => {
    if (!dkimDomain) { toast.error("Enter a domain"); return; }
    setSettingUpDkim(true);
    try {
      await api.post(`/email/dkim/${dkimDomain}`);
      toast.success(`DKIM setup complete for ${dkimDomain}`);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to setup DKIM");
    } finally { setSettingUpDkim(false); }
  };

  // Connect modal
  const [showConnect, setShowConnect] = useState(false);
  const [connectMailbox, setConnectMailbox] = useState<Mailbox | null>(null);

  const openDetails = (m: Mailbox) => { setSelectedMailbox(m); setShowDetails(true); };
  const openEdit = (m: Mailbox) => {
    setSelectedMailbox(m);
    setEditForm({ quota_mb: m.quota_mb, send_limit_per_hour: m.send_limit_per_hour, password: "" });
    setShowEdit(true);
  };
  const openConnect = (m: Mailbox) => { setConnectMailbox(m); setShowConnect(true); };

  const filteredMailboxes = mailboxes.filter((m) => (m.email || "").toLowerCase().includes(search.toLowerCase()));
  const filteredForwarders = forwarders.filter((f) => (f.source || "").toLowerCase().includes(search.toLowerCase()));
  const uniqueDomains = [...new Set(mailboxes.map((m) => m.domain).filter(Boolean))];

  const tabs: { key: Tab; label: string; icon: any }[] = [
    { key: "mailboxes", label: "Mailboxes", icon: Mail },
    { key: "forwarders", label: "Forwarders", icon: Send },
    { key: "spam", label: "Spam & DKIM", icon: Shield },
  ];

  const mailboxColumns = [
    {
      header: "Address",
      accessor: (m: Mailbox) => (
        <button onClick={() => openDetails(m)} className="flex items-center gap-2 hover:text-blue-400 transition-colors">
          <Mail size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{m.email}</span>
        </button>
      ),
    },
    {
      header: "Domain",
      accessor: (m: Mailbox) => <span className="text-panel-muted text-sm">{m.domain}</span>,
    },
    {
      header: "Quota",
      accessor: (m: Mailbox) => {
        const usedMB = m.used_mb || 0;
        const totalMB = m.quota_mb || 0;
        const percent = totalMB > 0 ? Math.round((usedMB / totalMB) * 100) : 0;
        return (
          <div className="min-w-[120px]">
            <div className="flex items-center justify-between mb-1">
              <span className="text-xs text-panel-muted">{usedMB} MB / {totalMB} MB</span>
              <span className="text-xs text-panel-muted">{percent}%</span>
            </div>
            <div className="w-full h-1.5 bg-panel-bg rounded-full overflow-hidden">
              <div className={`h-full rounded-full ${percent > 90 ? "bg-red-500" : percent > 70 ? "bg-yellow-500" : "bg-blue-500"}`} style={{ width: `${percent}%` }} />
            </div>
          </div>
        );
      },
    },
    {
      header: "Send Limit",
      accessor: (m: Mailbox) => <span className="text-panel-muted text-sm">{m.send_limit_per_hour}/hr</span>,
    },
    {
      header: "Actions",
      accessor: (m: Mailbox) => (
        <div className="flex items-center gap-1">
          <button onClick={() => openDetails(m)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors" title="View Details">
            <Eye size={14} />
          </button>
          <button onClick={() => openEdit(m)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors" title="Edit Configuration">
            <Edit size={14} />
          </button>
          <button onClick={() => openConnect(m)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors" title="Mail Client Setup">
            <Settings size={14} />
          </button>
          <button onClick={() => handleDelete(m.id, m.email)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors" title="Delete">
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  const forwarderColumns = [
    {
      header: "Source",
      accessor: (f: Forwarder) => (
        <div className="flex items-center gap-2">
          <Mail size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{f.source}</span>
        </div>
      ),
    },
    {
      header: "Forwards To",
      accessor: (f: Forwarder) => (
        <div className="flex flex-col gap-1">
          {(f.destinations || []).map((d, i) => (
            <div key={i} className="flex items-center gap-1 text-sm text-panel-muted">
              <ArrowRight size={12} className="text-green-400" />
              {d}
            </div>
          ))}
        </div>
      ),
    },
    {
      header: "Keep Copy",
      accessor: (f: Forwarder) => (
        <StatusBadge status={f.keep_copy ? "active" : "inactive"} />
      ),
    },
    {
      header: "Actions",
      accessor: (f: Forwarder) => (
        <button onClick={() => handleDeleteForwarder(f.id, f.source)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors" title="Delete">
          <Trash2 size={14} />
        </button>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Email</h1>
          <p className="text-panel-muted text-sm mt-1">Manage email mailboxes, forwarders, and security</p>
        </div>
        <div className="flex items-center gap-2">
          <Button onClick={() => { activeTab === "mailboxes" ? fetchMailboxes() : fetchForwarders(); }}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm">
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
          </Button>
          {activeTab === "mailboxes" && (
            <Button onClick={() => setShowCreate(true)}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
              <Plus size={14} /> Create Mailbox
            </Button>
          )}
          {activeTab === "forwarders" && (
            <Button onClick={() => setShowCreateForwarder(true)}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
              <Plus size={14} /> Add Forwarder
            </Button>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 bg-panel-surface/50 p-1 rounded-lg border border-panel-border w-fit">
        {tabs.map((tab) => (
          <button key={tab.key} onClick={() => { setActiveTab(tab.key); setSearch(""); }}
            className={`flex items-center gap-2 px-4 py-2 rounded-md text-sm font-medium transition-colors ${activeTab === tab.key ? "bg-blue-600 text-white" : "text-panel-muted hover:text-panel-text hover:bg-panel-surface"}`}>
            <tab.icon size={14} /> {tab.label}
          </button>
        ))}
      </div>

      {/* Mailboxes Tab */}
      {activeTab === "mailboxes" && (
        <>
          <Card>
            <div className="p-4">
              <div className="relative">
                <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
                <input type="text" placeholder="Search mailboxes..." value={search} onChange={(e) => setSearch(e.target.value)}
                  className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm" />
              </div>
            </div>
          </Card>
          <Card>
            {loading ? (
              <div className="p-8"><div className="space-y-3">{[1, 2, 3].map((i) => (<div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />))}</div></div>
            ) : filteredMailboxes.length > 0 ? (
              <Table columns={mailboxColumns} data={filteredMailboxes} />
            ) : (
              <div className="text-center py-16 px-4">
                <Mail size={48} className="text-panel-muted/20 mx-auto mb-4" />
                <h3 className="text-lg font-medium text-panel-text mb-1">No mailboxes found</h3>
                <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
                  {search ? "No mailboxes match your search." : "Create your first email mailbox to start receiving mail."}
                </p>
                {!search && (
                  <Button onClick={() => setShowCreate(true)} className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
                    <Plus size={14} /> Create Mailbox
                  </Button>
                )}
              </div>
            )}
          </Card>
        </>
      )}

      {/* Forwarders Tab */}
      {activeTab === "forwarders" && (
        <>
          <Card>
            <div className="p-4">
              <div className="relative">
                <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
                <input type="text" placeholder="Search forwarders..." value={search} onChange={(e) => setSearch(e.target.value)}
                  className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm" />
              </div>
            </div>
          </Card>
          <Card>
            {loading ? (
              <div className="p-8"><div className="space-y-3">{[1, 2, 3].map((i) => (<div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />))}</div></div>
            ) : filteredForwarders.length > 0 ? (
              <Table columns={forwarderColumns} data={filteredForwarders} />
            ) : (
              <div className="text-center py-16 px-4">
                <Send size={48} className="text-panel-muted/20 mx-auto mb-4" />
                <h3 className="text-lg font-medium text-panel-text mb-1">No forwarders found</h3>
                <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
                  Create email forwarders to redirect mail from one address to another.
                </p>
                <Button onClick={() => setShowCreateForwarder(true)} className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
                  <Plus size={14} /> Add Forwarder
                </Button>
              </div>
            )}
          </Card>
        </>
      )}

      {/* Spam & DKIM Tab */}
      {activeTab === "spam" && (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Spam Settings */}
          <Card>
            <div className="p-6">
              <div className="flex items-center gap-2 mb-4">
                <Shield size={18} className="text-orange-400" />
                <h3 className="text-base font-semibold text-panel-text">Spam Filter Settings</h3>
              </div>
              <form onSubmit={handleSaveSpam} className="space-y-4">
                <div>
                  <label className={labelClass}>Domain *</label>
                  <select required value={spamDomain} onChange={(e) => setSpamDomain(e.target.value)} className={inputClass}>
                    <option value="">Select domain...</option>
                    {uniqueDomains.map((d) => (<option key={d} value={d}>{d}</option>))}
                  </select>
                </div>
                <div>
                  <label className={labelClass}>Spam Threshold</label>
                  <input type="number" step="0.5" min={1} max={10} value={spamForm.spam_threshold}
                    onChange={(e) => setSpamForm({ ...spamForm, spam_threshold: parseFloat(e.target.value) || 5 })} className={inputClass} />
                  <p className="text-xs text-panel-muted mt-1">Lower value = stricter filtering (recommended: 5.0)</p>
                </div>
                <div>
                  <label className={labelClass}>Action on Spam</label>
                  <select value={spamForm.spam_action} onChange={(e) => setSpamForm({ ...spamForm, spam_action: e.target.value })} className={inputClass}>
                    <option value="flag">Flag (mark as spam)</option>
                    <option value="quarantine">Quarantine</option>
                    <option value="reject">Reject</option>
                  </select>
                </div>
                <div>
                  <label className={labelClass}>Whitelist (comma-separated emails)</label>
                  <input type="text" placeholder="trusted@example.com, safe@domain.com" value={spamForm.whitelist}
                    onChange={(e) => setSpamForm({ ...spamForm, whitelist: e.target.value })} className={inputClass} />
                </div>
                <div>
                  <label className={labelClass}>Blacklist (comma-separated emails)</label>
                  <input type="text" placeholder="spam@bad.com" value={spamForm.blacklist}
                    onChange={(e) => setSpamForm({ ...spamForm, blacklist: e.target.value })} className={inputClass} />
                </div>
                <div className="flex items-center gap-2">
                  <input type="checkbox" id="clamav" checked={spamForm.clamav_enabled}
                    onChange={(e) => setSpamForm({ ...spamForm, clamav_enabled: e.target.checked })}
                    className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 focus:ring-blue-500/40" />
                  <label htmlFor="clamav" className="text-sm text-panel-text">Enable ClamAV antivirus scanning</label>
                </div>
                <button type="submit" disabled={savingSpam}
                  className="w-full px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
                  {savingSpam ? "Saving..." : "Save Spam Settings"}
                </button>
              </form>
            </div>
          </Card>

          {/* DKIM Setup */}
          <Card>
            <div className="p-6">
              <div className="flex items-center gap-2 mb-4">
                <Key size={18} className="text-green-400" />
                <h3 className="text-base font-semibold text-panel-text">DKIM Email Authentication</h3>
              </div>
              <p className="text-sm text-panel-muted mb-4">
                DKIM (DomainKeys Identified Mail) adds a digital signature to outgoing emails,
                helping prevent spoofing and improving deliverability.
              </p>
              <div className="space-y-4">
                <div>
                  <label className={labelClass}>Domain *</label>
                  <select value={dkimDomain} onChange={(e) => setDkimDomain(e.target.value)} className={inputClass}>
                    <option value="">Select domain...</option>
                    {uniqueDomains.map((d) => (<option key={d} value={d}>{d}</option>))}
                  </select>
                </div>
                <button onClick={handleSetupDkim} disabled={settingUpDkim || !dkimDomain}
                  className="w-full px-4 py-2 text-sm bg-green-600 hover:bg-green-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50 flex items-center justify-center gap-2">
                  <Key size={14} />
                  {settingUpDkim ? "Setting up DKIM..." : "Generate & Setup DKIM"}
                </button>
              </div>

              {/* Webmail / Mail Client Config */}
              <div className="mt-8 pt-6 border-t border-panel-border">
                <div className="flex items-center gap-2 mb-3">
                  <ExternalLink size={18} className="text-blue-400" />
                  <h3 className="text-base font-semibold text-panel-text">Mail Client Configuration</h3>
                </div>
                <p className="text-sm text-panel-muted mb-3">
                  Use these settings to connect any email client. Replace <span className="font-mono text-panel-text">yourdomain.com</span> with your actual domain.
                </p>
                <div className="rounded-lg overflow-hidden border border-panel-border">
                  <div className="bg-blue-600 px-4 py-2">
                    <h4 className="text-xs font-semibold text-white">Secure SSL/TLS Settings (Recommended)</h4>
                  </div>
                  <table className="w-full text-sm">
                    <tbody>
                      <tr className="border-b border-panel-border">
                        <td className="px-3 py-2 text-panel-muted font-medium w-[130px] bg-panel-bg/50 text-xs">Incoming Server:</td>
                        <td className="px-3 py-2 text-xs">
                          <span className="text-panel-text font-mono">mail.yourdomain.com</span>
                          <span className="ml-3 text-panel-muted">IMAP: <span className="text-panel-text font-mono">993</span></span>
                          <span className="ml-2 text-panel-muted">POP3: <span className="text-panel-text font-mono">995</span></span>
                        </td>
                      </tr>
                      <tr>
                        <td className="px-3 py-2 text-panel-muted font-medium bg-panel-bg/50 text-xs">Outgoing Server:</td>
                        <td className="px-3 py-2 text-xs">
                          <span className="text-panel-text font-mono">mail.yourdomain.com</span>
                          <span className="ml-3 text-panel-muted">SMTP: <span className="text-panel-text font-mono">465</span></span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          </Card>
        </div>
      )}

      {/* Create Mailbox Modal */}
      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Create Mailbox">
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className={labelClass}>Email Address *</label>
            <input type="email" required placeholder="user@example.com" value={form.email}
              onChange={(e) => setForm({ ...form, email: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>Password *</label>
            <input type="password" required minLength={8} placeholder="Minimum 8 characters" value={form.password}
              onChange={(e) => setForm({ ...form, password: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>Domain *</label>
            <input type="text" required placeholder="example.com" value={form.domain}
              onChange={(e) => setForm({ ...form, domain: e.target.value })} className={inputClass} />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Quota (MB)</label>
              <input type="number" min={0} value={form.quota_mb}
                onChange={(e) => setForm({ ...form, quota_mb: parseInt(e.target.value) || 0 })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Send Limit/Hour</label>
              <input type="number" min={0} value={form.send_limit_per_hour}
                onChange={(e) => setForm({ ...form, send_limit_per_hour: parseInt(e.target.value) || 0 })} className={inputClass} />
            </div>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Cancel</button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Creating..." : "Create Mailbox"}
            </button>
          </div>
        </form>
      </Modal>

      {/* View Details Modal */}
      <Modal isOpen={showDetails} onClose={() => setShowDetails(false)} title="Mailbox Details" size="lg">
        {selectedMailbox && (
          <div className="space-y-6">
            {/* Email & Domain Header */}
            <div className="flex items-center gap-3 p-4 bg-panel-bg rounded-lg border border-panel-border">
              <div className="p-3 bg-blue-600/20 rounded-lg"><Mail size={24} className="text-blue-400" /></div>
              <div>
                <h3 className="text-lg font-semibold text-panel-text">{selectedMailbox.email}</h3>
                <p className="text-sm text-panel-muted">{selectedMailbox.domain}</p>
              </div>
            </div>

            {/* Quota & Limits */}
            <div className="grid grid-cols-2 gap-4">
              <div className="p-4 bg-panel-bg rounded-lg border border-panel-border">
                <p className="text-xs text-panel-muted uppercase tracking-wider mb-1">Quota</p>
                <p className="text-lg font-semibold text-panel-text">{selectedMailbox.used_mb || 0} / {selectedMailbox.quota_mb} MB</p>
                <div className="w-full h-2 bg-panel-border rounded-full mt-2 overflow-hidden">
                  <div className={`h-full rounded-full ${((selectedMailbox.used_mb || 0) / (selectedMailbox.quota_mb || 1)) * 100 > 90 ? "bg-red-500" : "bg-blue-500"}`}
                    style={{ width: `${Math.min(((selectedMailbox.used_mb || 0) / (selectedMailbox.quota_mb || 1)) * 100, 100)}%` }} />
                </div>
              </div>
              <div className="p-4 bg-panel-bg rounded-lg border border-panel-border">
                <p className="text-xs text-panel-muted uppercase tracking-wider mb-1">Send Limit</p>
                <p className="text-lg font-semibold text-panel-text">{selectedMailbox.send_limit_per_hour} / hour</p>
              </div>
            </div>

            {/* Secure SSL/TLS Settings Table (cPanel style) */}
            <div className="rounded-lg overflow-hidden border border-panel-border">
              <div className="bg-blue-600 px-4 py-2.5">
                <h4 className="text-sm font-semibold text-white">Secure SSL/TLS Settings (Recommended)</h4>
              </div>
              <table className="w-full text-sm">
                <tbody>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium w-[140px] bg-panel-bg/50">Username:</td>
                    <td className="px-4 py-3 text-panel-text font-mono">{selectedMailbox.email}</td>
                  </tr>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Password:</td>
                    <td className="px-4 py-3 text-panel-muted italic">Use your mailbox password.</td>
                  </tr>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Incoming Server:</td>
                    <td className="px-4 py-3">
                      <span className="text-panel-text font-mono">mail.{selectedMailbox.domain}</span>
                      <div className="flex items-center gap-4 mt-1">
                        <span className="text-xs"><span className="text-blue-400 font-semibold underline">IMAP</span> Port: <span className="text-panel-text font-mono">993</span></span>
                        <span className="text-xs"><span className="text-blue-400 font-semibold underline">POP3</span> Port: <span className="text-panel-text font-mono">995</span></span>
                      </div>
                    </td>
                  </tr>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Outgoing Server:</td>
                    <td className="px-4 py-3">
                      <span className="text-panel-text font-mono">mail.{selectedMailbox.domain}</span>
                      <div className="mt-1">
                        <span className="text-xs"><span className="text-blue-400 font-semibold underline">SMTP</span> Port: <span className="text-panel-text font-mono">465</span></span>
                      </div>
                    </td>
                  </tr>
                  <tr>
                    <td colSpan={2} className="px-4 py-3 text-panel-muted text-xs bg-panel-bg/30">
                      IMAP, POP3, and SMTP require authentication.
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>

            {/* Non-SSL Settings (collapsible) */}
            <details className="group">
              <summary className="text-sm text-blue-400 cursor-pointer hover:text-blue-300 transition-colors flex items-center gap-1">
                Show Non SSL/TLS Settings
                <svg className="w-3 h-3 transition-transform group-open:rotate-180" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" /></svg>
              </summary>
              <div className="mt-3 rounded-lg overflow-hidden border border-panel-border">
                <div className="bg-panel-surface px-4 py-2.5">
                  <h4 className="text-sm font-semibold text-panel-text">Non-SSL Settings (Not Recommended)</h4>
                </div>
                <table className="w-full text-sm">
                  <tbody>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium w-[140px] bg-panel-bg/50">Username:</td>
                      <td className="px-4 py-3 text-panel-text font-mono">{selectedMailbox.email}</td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Password:</td>
                      <td className="px-4 py-3 text-panel-muted italic">Use your mailbox password.</td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Incoming Server:</td>
                      <td className="px-4 py-3">
                        <span className="text-panel-text font-mono">mail.{selectedMailbox.domain}</span>
                        <div className="flex items-center gap-4 mt-1">
                          <span className="text-xs">IMAP Port: <span className="text-panel-text font-mono">143</span></span>
                          <span className="text-xs">POP3 Port: <span className="text-panel-text font-mono">110</span></span>
                        </div>
                      </td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Outgoing Server:</td>
                      <td className="px-4 py-3">
                        <span className="text-panel-text font-mono">mail.{selectedMailbox.domain}</span>
                        <div className="mt-1">
                          <span className="text-xs">SMTP Port: <span className="text-panel-text font-mono">587</span></span>
                        </div>
                      </td>
                    </tr>
                    <tr>
                      <td colSpan={2} className="px-4 py-3 text-panel-muted text-xs bg-panel-bg/30">
                        IMAP, POP3, and SMTP require authentication.
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </details>

            {/* Dates */}
            <div className="grid grid-cols-2 gap-4">
              <div className="p-3 bg-panel-bg rounded-lg border border-panel-border">
                <p className="text-xs text-panel-muted uppercase tracking-wider mb-1">Created</p>
                <p className="text-sm font-medium text-panel-text">{selectedMailbox.created_at ? new Date(selectedMailbox.created_at).toLocaleString() : "-"}</p>
              </div>
              <div className="p-3 bg-panel-bg rounded-lg border border-panel-border">
                <p className="text-xs text-panel-muted uppercase tracking-wider mb-1">Last Updated</p>
                <p className="text-sm font-medium text-panel-text">{selectedMailbox.updated_at ? new Date(selectedMailbox.updated_at).toLocaleString() : "-"}</p>
              </div>
            </div>

            {/* Action Buttons */}
            <div className="flex justify-end gap-3 pt-2 border-t border-panel-border">
              <button onClick={() => { setShowDetails(false); openEdit(selectedMailbox); }}
                className="px-4 py-2 text-sm bg-yellow-600 hover:bg-yellow-700 text-white rounded-lg font-medium transition-colors flex items-center gap-2">
                <Edit size={14} /> Edit Configuration
              </button>
              <button onClick={() => { setShowDetails(false); openConnect(selectedMailbox); }}
                className="px-4 py-2 text-sm bg-green-600 hover:bg-green-700 text-white rounded-lg font-medium transition-colors flex items-center gap-2">
                <Settings size={14} /> Mail Client Setup
              </button>
            </div>
          </div>
        )}
      </Modal>

      {/* Edit Mailbox Modal */}
      <Modal isOpen={showEdit} onClose={() => setShowEdit(false)} title={`Edit: ${selectedMailbox?.email || ""}`}>
        <form onSubmit={handleEdit} className="space-y-4">
          <div className="p-3 bg-blue-500/10 border border-blue-500/20 rounded-lg text-sm text-blue-300">
            Updating configuration for <strong>{selectedMailbox?.email}</strong>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Quota (MB)</label>
              <input type="number" min={0} value={editForm.quota_mb}
                onChange={(e) => setEditForm({ ...editForm, quota_mb: parseInt(e.target.value) || 0 })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Send Limit/Hour</label>
              <input type="number" min={0} value={editForm.send_limit_per_hour}
                onChange={(e) => setEditForm({ ...editForm, send_limit_per_hour: parseInt(e.target.value) || 0 })} className={inputClass} />
            </div>
          </div>
          <div>
            <label className={labelClass}>New Password (leave blank to keep current)</label>
            <input type="password" minLength={8} placeholder="Enter new password" value={editForm.password}
              onChange={(e) => setEditForm({ ...editForm, password: e.target.value })} className={inputClass} />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowEdit(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Cancel</button>
            <button type="submit" disabled={saving}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {saving ? "Saving..." : "Save Changes"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Create Forwarder Modal */}
      <Modal isOpen={showCreateForwarder} onClose={() => setShowCreateForwarder(false)} title="Create Email Forwarder">
        <form onSubmit={handleCreateForwarder} className="space-y-4">
          <div>
            <label className={labelClass}>Source Email *</label>
            <input type="email" required placeholder="source@example.com" value={forwarderForm.source}
              onChange={(e) => setForwarderForm({ ...forwarderForm, source: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>Forward To (comma-separated) *</label>
            <input type="text" required placeholder="dest1@example.com, dest2@example.com" value={forwarderForm.destinations}
              onChange={(e) => setForwarderForm({ ...forwarderForm, destinations: e.target.value })} className={inputClass} />
          </div>
          <div className="flex items-center gap-2">
            <input type="checkbox" id="keepCopy" checked={forwarderForm.keep_copy}
              onChange={(e) => setForwarderForm({ ...forwarderForm, keep_copy: e.target.checked })}
              className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 focus:ring-blue-500/40" />
            <label htmlFor="keepCopy" className="text-sm text-panel-text">Keep a copy in the original mailbox</label>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreateForwarder(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Cancel</button>
            <button type="submit" disabled={creatingForwarder}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creatingForwarder ? "Creating..." : "Create Forwarder"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Mail Client Setup Modal */}
      <Modal isOpen={showConnect} onClose={() => setShowConnect(false)} title="Mail Client Setup" size="lg">
        {connectMailbox && (
          <div className="space-y-5">
            <div className="flex items-center gap-3 p-4 bg-panel-bg rounded-lg border border-panel-border">
              <div className="p-3 bg-blue-600/20 rounded-lg"><Mail size={24} className="text-blue-400" /></div>
              <div>
                <h3 className="text-lg font-semibold text-panel-text">{connectMailbox.email}</h3>
                <p className="text-sm text-panel-muted">Use the settings below to configure your email client</p>
              </div>
            </div>

            {/* SSL/TLS Settings */}
            <div className="rounded-lg overflow-hidden border border-panel-border">
              <div className="bg-blue-600 px-4 py-2.5">
                <h4 className="text-sm font-semibold text-white">Secure SSL/TLS Settings (Recommended)</h4>
              </div>
              <table className="w-full text-sm">
                <tbody>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium w-[160px] bg-panel-bg/50">Username:</td>
                    <td className="px-4 py-3 text-panel-text font-mono">{connectMailbox.email}</td>
                  </tr>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Password:</td>
                    <td className="px-4 py-3 text-panel-muted italic">Use your mailbox password.</td>
                  </tr>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Incoming Server:</td>
                    <td className="px-4 py-3">
                      <span className="text-panel-text font-mono">mail.{connectMailbox.domain}</span>
                      <div className="flex items-center gap-4 mt-1">
                        <span className="text-xs"><span className="text-blue-400 font-semibold underline">IMAP</span> Port: <span className="text-panel-text font-mono">993</span></span>
                        <span className="text-xs"><span className="text-blue-400 font-semibold underline">POP3</span> Port: <span className="text-panel-text font-mono">995</span></span>
                      </div>
                    </td>
                  </tr>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Outgoing Server:</td>
                    <td className="px-4 py-3">
                      <span className="text-panel-text font-mono">mail.{connectMailbox.domain}</span>
                      <div className="mt-1">
                        <span className="text-xs"><span className="text-blue-400 font-semibold underline">SMTP</span> Port: <span className="text-panel-text font-mono">465</span></span>
                      </div>
                    </td>
                  </tr>
                  <tr>
                    <td colSpan={2} className="px-4 py-3 text-panel-muted text-xs bg-panel-bg/30">
                      IMAP, POP3, and SMTP require authentication.
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>

            {/* Non-SSL (collapsible) */}
            <details className="group">
              <summary className="text-sm text-blue-400 cursor-pointer hover:text-blue-300 transition-colors flex items-center gap-1">
                Show Non SSL/TLS Settings
                <svg className="w-3 h-3 transition-transform group-open:rotate-180" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" /></svg>
              </summary>
              <div className="mt-3 rounded-lg overflow-hidden border border-panel-border">
                <div className="bg-panel-surface px-4 py-2.5">
                  <h4 className="text-sm font-semibold text-panel-text">Non-SSL Settings (Not Recommended)</h4>
                </div>
                <table className="w-full text-sm">
                  <tbody>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium w-[160px] bg-panel-bg/50">Username:</td>
                      <td className="px-4 py-3 text-panel-text font-mono">{connectMailbox.email}</td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Password:</td>
                      <td className="px-4 py-3 text-panel-muted italic">Use your mailbox password.</td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Incoming Server:</td>
                      <td className="px-4 py-3">
                        <span className="text-panel-text font-mono">mail.{connectMailbox.domain}</span>
                        <div className="flex items-center gap-4 mt-1">
                          <span className="text-xs">IMAP Port: <span className="text-panel-text font-mono">143</span></span>
                          <span className="text-xs">POP3 Port: <span className="text-panel-text font-mono">110</span></span>
                        </div>
                      </td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Outgoing Server:</td>
                      <td className="px-4 py-3">
                        <span className="text-panel-text font-mono">mail.{connectMailbox.domain}</span>
                        <div className="mt-1">
                          <span className="text-xs">SMTP Port: <span className="text-panel-text font-mono">587</span></span>
                        </div>
                      </td>
                    </tr>
                    <tr>
                      <td colSpan={2} className="px-4 py-3 text-panel-muted text-xs bg-panel-bg/30">
                        IMAP, POP3, and SMTP require authentication.
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </details>

            {/* Setup Guide */}
            <div className="p-4 bg-panel-bg rounded-lg border border-panel-border">
              <h4 className="text-sm font-semibold text-panel-text mb-3">How to connect</h4>
              <div className="space-y-2 text-sm text-panel-muted">
                <p><strong className="text-panel-text">Outlook:</strong> File &gt; Add Account &gt; Manual setup &gt; IMAP &gt; Enter settings above</p>
                <p><strong className="text-panel-text">Thunderbird:</strong> Account Settings &gt; Add Mail Account &gt; Manual config &gt; Enter settings above</p>
                <p><strong className="text-panel-text">Gmail (Android/iOS):</strong> Settings &gt; Add Account &gt; Other &gt; IMAP &gt; Enter settings above</p>
                <p><strong className="text-panel-text">Apple Mail:</strong> Preferences &gt; Accounts &gt; Add &gt; Other Mail &gt; Enter settings above</p>
              </div>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
