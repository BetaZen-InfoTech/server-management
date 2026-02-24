import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Archive, Plus, RefreshCw, Search, Trash2, Download, HardDrive } from "lucide-react";

interface Backup {
  id: string;
  name: string;
  type: "full" | "partial" | "database" | "files";
  size: string;
  status: "completed" | "in_progress" | "failed";
  createdAt: string;
}

const typeColors: Record<string, string> = {
  full: "bg-blue-500/10 text-blue-400",
  partial: "bg-yellow-500/10 text-yellow-400",
  database: "bg-purple-500/10 text-purple-400",
  files: "bg-green-500/10 text-green-400",
};

export default function BackupsPage() {
  const [backups, setBackups] = useState<Backup[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  useEffect(() => {
    fetchBackups();
  }, []);

  const fetchBackups = async () => {
    setLoading(true);
    try {
      const res = await api.get("/backups");
      setBackups(res.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Are you sure you want to delete backup "${name}"?`)) return;
    try {
      await api.delete(`/backups/${id}`);
      toast.success(`Backup ${name} deleted`);
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
    b.name.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Name",
      accessor: (b: Backup) => (
        <div className="flex items-center gap-2">
          <Archive size={14} className="text-orange-400" />
          <span className="font-medium text-panel-text">{b.name}</span>
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
          <span>{b.size}</span>
        </div>
      ),
    },
    {
      header: "Status",
      accessor: (b: Backup) => <StatusBadge status={b.status === "in_progress" ? "pending" : b.status === "completed" ? "active" : b.status} />,
    },
    {
      header: "Created",
      accessor: (b: Backup) => (
        <span className="text-panel-muted text-sm">{b.createdAt}</span>
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
            onClick={() => handleDelete(b.id, b.name)}
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
            onClick={() => toast("Create Backup modal coming soon")}
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
                onClick={() => toast("Create Backup modal coming soon")}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Create Backup
              </Button>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
