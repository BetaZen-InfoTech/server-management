import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Globe2, Plus, RefreshCw, Search, Trash2, Pencil, FileText } from "lucide-react";

interface DnsZone {
  id: string;
  domain: string;
  recordsCount: number;
  status: string;
  lastUpdated: string;
}

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

export default function DnsPage() {
  const [zones, setZones] = useState<DnsZone[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ domain: "" });

  useEffect(() => {
    fetchZones();
  }, []);

  const fetchZones = async () => {
    setLoading(true);
    try {
      const res = await api.get("/dns/zones");
      setZones(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.domain) {
      toast.error("Please enter a domain name");
      return;
    }
    setCreating(true);
    try {
      await api.post("/dns/zones", form);
      toast.success(`DNS zone for ${form.domain} created`);
      setShowCreate(false);
      setForm({ domain: "" });
      fetchZones();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create DNS zone");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string, domain: string) => {
    if (!confirm(`Are you sure you want to delete DNS zone for ${domain}?`)) return;
    try {
      await api.delete(`/dns/zones/${domain}`);
      toast.success(`DNS zone for ${domain} deleted`);
      fetchZones();
    } catch {
      toast.error("Failed to delete DNS zone");
    }
  };

  const filtered = zones.filter((z) =>
    z.domain.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Domain",
      accessor: (z: DnsZone) => (
        <div className="flex items-center gap-2">
          <Globe2 size={14} className="text-cyan-400" />
          <span className="font-medium text-panel-text">{z.domain}</span>
        </div>
      ),
    },
    {
      header: "Records",
      accessor: (z: DnsZone) => (
        <div className="flex items-center gap-1.5 text-panel-muted">
          <FileText size={12} />
          <span>{z.recordsCount} records</span>
        </div>
      ),
    },
    {
      header: "Status",
      accessor: (z: DnsZone) => <StatusBadge status={z.status} />,
    },
    {
      header: "Last Updated",
      accessor: (z: DnsZone) => (
        <span className="text-panel-muted text-sm">{z.lastUpdated}</span>
      ),
    },
    {
      header: "Actions",
      accessor: (z: DnsZone) => (
        <div className="flex items-center gap-1">
          <button
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
            title="Edit"
          >
            <Pencil size={14} />
          </button>
          <button
            onClick={() => handleDelete(z.id, z.domain)}
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
          <h1 className="text-xl font-bold text-panel-text">DNS Zones</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage DNS zones and records for your domains
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchZones}
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
            Add Zone
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search DNS zones..."
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
            <Globe2 size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No DNS zones found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No DNS zones match your search. Try a different search term."
                : "Add a zone to manage DNS records for your domains."}
            </p>
            {!search && (
              <Button
                onClick={() => setShowCreate(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Add Zone
              </Button>
            )}
          </div>
        )}
      </Card>

      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Add DNS Zone">
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className={labelClass}>Domain Name *</label>
            <input type="text" required placeholder="example.com" value={form.domain}
              onChange={(e) => setForm({ ...form, domain: e.target.value })} className={inputClass} />
            <p className="text-xs text-panel-muted mt-1">
              Enter the root domain. Standard DNS records (A, CNAME, MX, etc.) will be created automatically.
            </p>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Creating..." : "Add Zone"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
