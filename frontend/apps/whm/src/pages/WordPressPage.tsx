import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Blocks, Plus, RefreshCw, Search, Trash2, ExternalLink, RotateCw } from "lucide-react";

interface WordPressSite {
  id: string;
  site: string;
  domain: string;
  wpVersion: string;
  status: "active" | "inactive" | "updating";
  phpVersion: string;
}

export default function WordPressPage() {
  const [sites, setSites] = useState<WordPressSite[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  useEffect(() => {
    fetchSites();
  }, []);

  const fetchSites = async () => {
    setLoading(true);
    try {
      const res = await api.get("/wordpress");
      setSites(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleUpdate = async (id: string) => {
    try {
      await api.post(`/wordpress/${id}/update`);
      toast.success("WordPress update initiated");
      fetchSites();
    } catch {
      toast.error("Failed to update WordPress");
    }
  };

  const handleDelete = async (id: string, site: string) => {
    if (!confirm(`Are you sure you want to delete WordPress site "${site}"? All data will be lost.`)) return;
    try {
      await api.delete(`/wordpress/${id}`);
      toast.success(`WordPress site ${site} deleted`);
      fetchSites();
    } catch {
      toast.error("Failed to delete WordPress site");
    }
  };

  const filtered = sites.filter(
    (s) =>
      s.site.toLowerCase().includes(search.toLowerCase()) ||
      s.domain.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Site",
      accessor: (s: WordPressSite) => (
        <div className="flex items-center gap-2">
          <Blocks size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{s.site}</span>
        </div>
      ),
    },
    {
      header: "Domain",
      accessor: (s: WordPressSite) => (
        <span className="text-panel-muted">{s.domain}</span>
      ),
    },
    {
      header: "WP Version",
      accessor: (s: WordPressSite) => (
        <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">
          v{s.wpVersion}
        </code>
      ),
    },
    {
      header: "Status",
      accessor: (s: WordPressSite) => <StatusBadge status={s.status} />,
    },
    {
      header: "Actions",
      accessor: (s: WordPressSite) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => window.open(`https://${s.domain}/wp-admin`, "_blank")}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
            title="Open WP Admin"
          >
            <ExternalLink size={14} />
          </button>
          <button
            onClick={() => handleUpdate(s.id)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
            title="Update"
          >
            <RotateCw size={14} />
          </button>
          <button
            onClick={() => handleDelete(s.id, s.site)}
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
          <h1 className="text-xl font-bold text-panel-text">WordPress</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage WordPress installations on your server
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchSites}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => toast("Install WordPress modal coming soon")}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Install WordPress
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search WordPress sites..."
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
            <Blocks size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No WordPress sites found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No WordPress sites match your search. Try a different search term."
                : "Install WordPress on any of your domains with one click."}
            </p>
            {!search && (
              <Button
                onClick={() => toast("Install WordPress modal coming soon")}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Install WordPress
              </Button>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
