import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Archive, Plus, RefreshCw, Search, Trash2, Download, HardDrive } from "lucide-react";

interface Backup {
  id: string;
  type: "full" | "files" | "database" | "email" | "config";
  domain: string;
  user: string;
  storage: string;
  status: string;
  size_mb: number;
  path: string;
  encrypted: boolean;
  compression: string;
  created_at: string;
  completed_at: string;
}

const typeColors: Record<string, string> = {
  full: "bg-blue-500/10 text-blue-400",
  files: "bg-green-500/10 text-green-400",
  database: "bg-purple-500/10 text-purple-400",
  email: "bg-yellow-500/10 text-yellow-400",
  config: "bg-cyan-500/10 text-cyan-400",
};

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const selectClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";

export default function BackupsPage() {
  const [backups, setBackups] = useState<Backup[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ type: "full", domain: "", user: "", storage: "local", compression: "gzip" });

  useEffect(() => {
    fetchBackups();
  }, []);

  const fetchBackups = async () => {
    setLoading(true);
    try {
      const res = await api.get("/backups");
      setBackups(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.domain || !form.user) {
      toast.error("Please fill all required fields");
      return;
    }
    setCreating(true);
    try {
      await api.post("/backups/", form);
      toast.success("Backup created successfully");
      setShowCreate(false);
      setForm({ type: "full", domain: "", user: "", storage: "local", compression: "gzip" });
      fetchBackups();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create backup");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string, domain: string) => {
    if (!confirm(`Are you sure you want to delete backup for "${domain}"?`)) return;
    try {
      await api.delete(`/backups/${id}`);
      toast.success(`Backup deleted`);
      fetchBackups();
    } catch {
      toast.error("Failed to delete backup");
    }
  };

  const handleDownload = async (id: string) => {
    try {
      toast.success("Backup download started");
      window.open(`/api/v1/backups/${id}/download`, "_blank");
    } catch {
      toast.error("Failed to download backup");
    }
  };

  const filtered = backups.filter((b) =>
    (b.domain || "").toLowerCase().includes(search.toLowerCase()) ||
    (b.type || "").toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Domain",
      accessor: (b: Backup) => (
        <div className="flex items-center gap-2">
          <Archive size={14} className="text-orange-400" />
          <span className="font-medium text-panel-text">{b.domain}</span>
        </div>
      ),
    },
    {
      header: "Type",
      accessor: (b: Backup) => (
        <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium capitalize ${typeColors[b.type] || "bg-panel-bg text-panel-muted"}`}>
          {b.type}
        </span>
      ),
    },
    {
      header: "Size",
      accessor: (b: Backup) => (
        <div className="flex items-center gap-1.5 text-panel-muted">
          <HardDrive size={12} />
          <span>{b.size_mb ? `${b.size_mb} MB` : "--"}</span>
        </div>
      ),
    },
    {
      header: "Status",
      accessor: (b: Backup) => <StatusBadge status={b.status === "in_progress" ? "pending" : b.status === "completed" ? "active" : b.status === "failed" ? "error" : b.status} />,
    },
    {
      header: "Created",
      accessor: (b: Backup) => (
        <span className="text-panel-muted text-sm">{b.created_at ? new Date(b.created_at).toLocaleDateString() : "--"}</span>
      ),
    },
    {
      header: "Actions",
      accessor: (b: Backup) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => handleDownload(b.id)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
            title="Download"
          >
            <Download size={14} />
          </button>
          <button
            onClick={() => handleDelete(b.id, b.domain)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
            title="Delete"
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
          <h1 className="text-xl font-bold text-panel-text">Backups</h1>
          <p className="text-panel-muted text-sm mt-1">
            Create and manage server backups
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchBackups}
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
            Create Backup
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search backups..."
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
            <Archive size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No backups found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No backups match your search. Try a different search term."
                : "Create your first backup to protect your server data and configurations."}
            </p>
            {!search && (
              <Button
                onClick={() => setShowCreate(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Create Backup
              </Button>
            )}
          </div>
        )}
      </Card>

      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Create Backup">
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className={labelClass}>Backup Type *</label>
            <select value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })} className={selectClass}>
              <option value="full">Full Backup</option>
              <option value="files">Files Only</option>
              <option value="database">Database Only</option>
              <option value="email">Email Only</option>
              <option value="config">Config Only</option>
            </select>
          </div>
          <div>
            <label className={labelClass}>Domain *</label>
            <input type="text" required placeholder="example.com" value={form.domain}
              onChange={(e) => setForm({ ...form, domain: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>System User *</label>
            <input type="text" required placeholder="username" value={form.user}
              onChange={(e) => setForm({ ...form, user: e.target.value })} className={inputClass} />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Storage *</label>
              <select value={form.storage} onChange={(e) => setForm({ ...form, storage: e.target.value })} className={selectClass}>
                <option value="local">Local</option>
                <option value="s3">Amazon S3</option>
              </select>
            </div>
            <div>
              <label className={labelClass}>Compression</label>
              <select value={form.compression} onChange={(e) => setForm({ ...form, compression: e.target.value })} className={selectClass}>
                <option value="gzip">Gzip</option>
                <option value="zstd">Zstandard</option>
              </select>
            </div>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Creating..." : "Create Backup"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
