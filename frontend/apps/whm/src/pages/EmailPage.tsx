import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Mail, Plus, RefreshCw, Search, Trash2, Edit, X } from "lucide-react";

interface Mailbox {
  id: string;
  email: string;
  domain: string;
  quota_mb: number;
  used_mb: number;
  send_limit_per_hour: number;
  created_at: string;
}

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

export default function EmailPage() {
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ email: "", password: "", domain: "", quota_mb: 500, send_limit_per_hour: 100 });

  useEffect(() => {
    fetchMailboxes();
  }, []);

  const fetchMailboxes = async () => {
    setLoading(true);
    try {
      const res = await api.get("/email/");
      setMailboxes(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.email || !form.password || !form.domain) {
      toast.error("Please fill all required fields");
      return;
    }
    setCreating(true);
    try {
      await api.post("/email/", form);
      toast.success(`Mailbox ${form.email} created`);
      setShowCreate(false);
      setForm({ email: "", password: "", domain: "", quota_mb: 500, send_limit_per_hour: 100 });
      fetchMailboxes();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create mailbox");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string, email: string) => {
    if (!confirm(`Are you sure you want to delete mailbox ${email}?`)) return;
    try {
      await api.delete(`/email/${id}`);
      toast.success(`Mailbox ${email} deleted`);
      fetchMailboxes();
    } catch {
      toast.error("Failed to delete mailbox");
    }
  };

  const filtered = mailboxes.filter((m) =>
    (m.email || "").toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Address",
      accessor: (m: Mailbox) => (
        <div className="flex items-center gap-2">
          <Mail size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{m.email}</span>
        </div>
      ),
    },
    {
      header: "Domain",
      accessor: (m: Mailbox) => (
        <span className="text-panel-muted text-sm">{m.domain}</span>
      ),
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
              <span className="text-xs text-panel-muted">
                {usedMB} MB / {totalMB} MB
              </span>
              <span className="text-xs text-panel-muted">{percent}%</span>
            </div>
            <div className="w-full h-1.5 bg-panel-bg rounded-full overflow-hidden">
              <div
                className={`h-full rounded-full ${
                  percent > 90 ? "bg-red-500" : percent > 70 ? "bg-yellow-500" : "bg-blue-500"
                }`}
                style={{ width: `${percent}%` }}
              />
            </div>
          </div>
        );
      },
    },
    {
      header: "Actions",
      accessor: (m: Mailbox) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => handleDelete(m.id, m.email)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
          >
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Email</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage email mailboxes and configurations
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchMailboxes}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => setShowCreate(true)}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Create Mailbox
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search mailboxes..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
        </div>
      </Card>

      <Card>
        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2, 3, 4].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={filtered} />
        ) : (
          <div className="text-center py-16 px-4">
            <Mail size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No mailboxes found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No mailboxes match your search. Try a different search term."
                : "Create your first email mailbox to start receiving mail on your domains."}
            </p>
            {!search && (
              <Button
                onClick={() => setShowCreate(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Create Mailbox
              </Button>
            )}
          </div>
        )}
      </Card>

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
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Creating..." : "Create Mailbox"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
