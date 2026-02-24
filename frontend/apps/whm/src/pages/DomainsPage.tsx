import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Globe, Plus, RefreshCw, Search, Trash2, Edit, ExternalLink } from "lucide-react";

interface Domain {
  id: string;
  domain: string;
  status: "active" | "suspended" | "pending";
  ssl: boolean;
  phpVersion: string;
  createdAt: string;
}

export default function DomainsPage() {
  const [domains, setDomains] = useState<Domain[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  useEffect(() => {
    fetchDomains();
  }, []);

  const fetchDomains = async () => {
    setLoading(true);
    try {
      const res = await api.get("/domains");
      setDomains(res.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async (id: string, domain: string) => {
    if (!confirm(`Are you sure you want to delete ${domain}?`)) return;
    try {
      await api.delete(`/domains/${id}`);
      toast.success(`Domain ${domain} deleted`);
      fetchDomains();
    } catch {
      toast.error("Failed to delete domain");
    }
  };

  const filtered = domains.filter((d) =>
    d.domain.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Domain",
      accessor: (d: Domain) => (
        <div className="flex items-center gap-2">
          <Globe size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{d.domain}</span>
        </div>
      ),
    },
    {
      header: "Status",
      accessor: (d: Domain) => <StatusBadge status={d.status} />,
    },
    {
      header: "SSL",
      accessor: (d: Domain) => (
        <span className={d.ssl ? "text-green-400" : "text-red-400"}>
          {d.ssl ? "Active" : "None"}
        </span>
      ),
    },
    {
      header: "PHP Version",
      accessor: (d: Domain) => (
        <span className="text-panel-muted">{d.phpVersion}</span>
      ),
    },
    {
      header: "Created",
      accessor: (d: Domain) => (
        <span className="text-panel-muted text-sm">{d.createdAt}</span>
      ),
    },
    {
      header: "Actions",
      accessor: (d: Domain) => (
        <div className="flex items-center gap-1">
          <button className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors">
            <Edit size={14} />
          </button>
          <button className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-panel-text transition-colors">
            <ExternalLink size={14} />
          </button>
          <button
            onClick={() => handleDelete(d.id, d.domain)}
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
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Domains</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage your server domains and virtual hosts
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchDomains}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => toast("Add Domain modal coming soon")}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Add Domain
          </Button>
        </div>
      </div>

      {/* Search and Filters */}
      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search domains..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
        </div>
      </Card>

      {/* Table */}
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
            <Globe size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No domains found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No domains match your search query. Try a different search term."
                : "Get started by adding your first domain to the server."}
            </p>
            {!search && (
              <Button
                onClick={() => toast("Add Domain modal coming soon")}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Add Domain
              </Button>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
