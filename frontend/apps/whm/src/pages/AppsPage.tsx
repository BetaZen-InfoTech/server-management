import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { AppWindow, Plus, RefreshCw, Search, Trash2, Play, Square, RotateCw } from "lucide-react";

interface Application {
  id: string;
  name: string;
  type: "nodejs" | "python" | "go" | "static";
  domain: string;
  status: "running" | "stopped" | "error" | "deploying";
  port: number;
  createdAt: string;
}

const typeLabels: Record<string, string> = {
  nodejs: "Node.js",
  python: "Python",
  go: "Go",
  static: "Static",
};

const typeColors: Record<string, string> = {
  nodejs: "text-green-400",
  python: "text-yellow-400",
  go: "text-cyan-400",
  static: "text-purple-400",
};

export default function AppsPage() {
  const [apps, setApps] = useState<Application[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  useEffect(() => {
    fetchApps();
  }, []);

  const fetchApps = async () => {
    setLoading(true);
    try {
      const res = await api.get("/apps");
      setApps(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleAction = async (id: string, action: string) => {
    try {
      await api.post(`/apps/${id}/${action}`);
      toast.success(`App ${action} successful`);
      fetchApps();
    } catch {
      toast.error(`Failed to ${action} app`);
    }
  };

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Are you sure you want to delete ${name}?`)) return;
    try {
      await api.delete(`/apps/${id}`);
      toast.success(`Application ${name} deleted`);
      fetchApps();
    } catch {
      toast.error("Failed to delete application");
    }
  };

  const filtered = apps.filter(
    (a) =>
      a.name.toLowerCase().includes(search.toLowerCase()) ||
      a.domain.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Name",
      accessor: (a: Application) => (
        <div className="flex items-center gap-2">
          <AppWindow size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{a.name}</span>
        </div>
      ),
    },
    {
      header: "Type",
      accessor: (a: Application) => (
        <span className={`font-medium ${typeColors[a.type] || "text-panel-muted"}`}>
          {typeLabels[a.type] || a.type}
        </span>
      ),
    },
    {
      header: "Domain",
      accessor: (a: Application) => (
        <span className="text-panel-muted">{a.domain}</span>
      ),
    },
    {
      header: "Status",
      accessor: (a: Application) => <StatusBadge status={a.status} />,
    },
    {
      header: "Port",
      accessor: (a: Application) => (
        <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">
          :{a.port}
        </code>
      ),
    },
    {
      header: "Actions",
      accessor: (a: Application) => (
        <div className="flex items-center gap-1">
          {a.status === "stopped" ? (
            <button
              onClick={() => handleAction(a.id, "start")}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
              title="Start"
            >
              <Play size={14} />
            </button>
          ) : (
            <button
              onClick={() => handleAction(a.id, "stop")}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors"
              title="Stop"
            >
              <Square size={14} />
            </button>
          )}
          <button
            onClick={() => handleAction(a.id, "restart")}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
            title="Restart"
          >
            <RotateCw size={14} />
          </button>
          <button
            onClick={() => handleDelete(a.id, a.name)}
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
          <h1 className="text-xl font-bold text-panel-text">Applications</h1>
          <p className="text-panel-muted text-sm mt-1">
            Deploy and manage your applications
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchApps}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => toast("Deploy App modal coming soon")}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Deploy App
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search applications..."
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
            <AppWindow size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No applications found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No applications match your search. Try a different search term."
                : "Deploy your first application to get started with Node.js, Python, Go, or static sites."}
            </p>
            {!search && (
              <Button
                onClick={() => toast("Deploy App modal coming soon")}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Deploy App
              </Button>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
