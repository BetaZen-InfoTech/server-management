import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Database, Plus, RefreshCw, Search, Trash2, Users, Edit } from "lucide-react";

interface DatabaseItem {
  id: string;
  db_name: string;
  type: "mongodb" | "mysql";
  size_mb: number;
  username: string;
  domain: string;
  host: string;
  port: number;
  created_at: string;
}

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const selectClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";

export default function DatabasesPage() {
  const [databases, setDatabases] = useState<DatabaseItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ db_name: "", type: "mongodb", username: "", password: "", domain: "" });

  useEffect(() => {
    fetchDatabases();
  }, []);

  const fetchDatabases = async () => {
    setLoading(true);
    try {
      const res = await api.get("/databases");
      setDatabases(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.db_name || !form.username || !form.password || !form.domain) {
      toast.error("Please fill all required fields");
      return;
    }
    setCreating(true);
    try {
      await api.post("/databases/", form);
      toast.success(`Database ${form.db_name} created`);
      setShowCreate(false);
      setForm({ db_name: "", type: "mongodb", username: "", password: "", domain: "" });
      fetchDatabases();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create database");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Are you sure you want to delete database "${name}"? This action cannot be undone.`)) return;
    try {
      await api.delete(`/databases/${id}`);
      toast.success(`Database ${name} deleted`);
      fetchDatabases();
    } catch {
      toast.error("Failed to delete database");
    }
  };

  const filtered = databases.filter((d) =>
    (d.db_name || "").toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Name",
      accessor: (d: DatabaseItem) => (
        <div className="flex items-center gap-2">
          <Database size={14} className="text-purple-400" />
          <span className="font-medium text-panel-text">{d.db_name}</span>
        </div>
      ),
    },
    {
      header: "Type",
      accessor: (d: DatabaseItem) => (
        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-green-500/10 text-green-400 text-xs font-medium">
          {d.type === "mongodb" ? "MongoDB" : d.type === "mysql" ? "MySQL" : d.type}
        </span>
      ),
    },
    {
      header: "Size",
      accessor: (d: DatabaseItem) => (
        <span className="text-panel-muted">{d.size_mb ? `${d.size_mb} MB` : "--"}</span>
      ),
    },
    {
      header: "User",
      accessor: (d: DatabaseItem) => (
        <div className="flex items-center gap-1 text-panel-muted">
          <Users size={12} />
          <span>{d.username}</span>
        </div>
      ),
    },
    {
      header: "Domain",
      accessor: (d: DatabaseItem) => (
        <span className="text-panel-muted text-sm">{d.domain}</span>
      ),
    },
    {
      header: "Created",
      accessor: (d: DatabaseItem) => (
        <span className="text-panel-muted text-sm">{d.created_at ? new Date(d.created_at).toLocaleDateString() : "--"}</span>
      ),
    },
    {
      header: "Actions",
      accessor: (d: DatabaseItem) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => handleDelete(d.id, d.db_name)}
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
          <h1 className="text-xl font-bold text-panel-text">Databases</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage MongoDB databases and users
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchDatabases}
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
            Create Database
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search databases..."
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
              {[1, 2, 3, 4, 5].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={filtered} />
        ) : (
          <div className="text-center py-16 px-4">
            <Database size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No databases found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No databases match your search. Try a different search term."
                : "Create your first database to store application data."}
            </p>
            {!search && (
              <Button
                onClick={() => setShowCreate(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Create Database
              </Button>
            )}
          </div>
        )}
      </Card>

      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Create Database">
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className={labelClass}>Database Name *</label>
            <input type="text" required placeholder="my_database" value={form.db_name}
              onChange={(e) => setForm({ ...form, db_name: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>Type *</label>
            <select value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })} className={selectClass}>
              <option value="mongodb">MongoDB</option>
              <option value="mysql">MySQL</option>
            </select>
          </div>
          <div>
            <label className={labelClass}>Username *</label>
            <input type="text" required placeholder="db_user" value={form.username}
              onChange={(e) => setForm({ ...form, username: e.target.value })} className={inputClass} />
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
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Creating..." : "Create Database"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
