import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Database, Plus, RefreshCw, Search, Trash2, Users, Edit } from "lucide-react";

interface DatabaseItem {
  id: string;
  name: string;
  type: "mongodb";
  size: string;
  users: number;
  status: "active" | "inactive";
  createdAt: string;
}

export default function DatabasesPage() {
  const [databases, setDatabases] = useState<DatabaseItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

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
    d.name.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Name",
      accessor: (d: DatabaseItem) => (
        <div className="flex items-center gap-2">
          <Database size={14} className="text-purple-400" />
          <span className="font-medium text-panel-text">{d.name}</span>
        </div>
      ),
    },
    {
      header: "Type",
      accessor: (d: DatabaseItem) => (
        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-green-500/10 text-green-400 text-xs font-medium">
          {d.type === "mongodb" ? "MongoDB" : d.type}
        </span>
      ),
    },
    {
      header: "Size",
      accessor: (d: DatabaseItem) => (
        <span className="text-panel-muted">{d.size}</span>
      ),
    },
    {
      header: "Users",
      accessor: (d: DatabaseItem) => (
        <div className="flex items-center gap-1 text-panel-muted">
          <Users size={12} />
          <span>{d.users}</span>
        </div>
      ),
    },
    {
      header: "Status",
      accessor: (d: DatabaseItem) => <StatusBadge status={d.status} />,
    },
    {
      header: "Created",
      accessor: (d: DatabaseItem) => (
        <span className="text-panel-muted text-sm">{d.createdAt}</span>
      ),
    },
    {
      header: "Actions",
      accessor: (d: DatabaseItem) => (
        <div className="flex items-center gap-1">
          <button className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors">
            <Edit size={14} />
          </button>
          <button
            onClick={() => handleDelete(d.id, d.name)}
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
            onClick={() => toast("Create Database modal coming soon")}
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
                : "Create your first MongoDB database to store application data."}
            </p>
            {!search && (
              <Button
                onClick={() => toast("Create Database modal coming soon")}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Create Database
              </Button>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
