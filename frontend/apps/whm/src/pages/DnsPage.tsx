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
      await api.delete(`/dns/zones/${id}`);
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
      key: "domain",
      header: "Domain",
      render: (item: DnsZone) => (
        <div className="flex items-center gap-2">
          <Globe2 size={14} className="text-cyan-400" />
          <span className="font-medium text-panel-text">{item.domain}</span>
        </div>
      ),
    },
    {
      key: "recordsCount",
      header: "Records",
      render: (item: DnsZone) => (
        <div className="flex items-center gap-1.5 text-panel-muted">
          <FileText size={12} />
          <span>{item.recordsCount} records</span>
        </div>
      ),
    },
    {
      key: "status",
      header: "Status",
      render: (item: DnsZone) => <StatusBadge status={item.status} />,
    },
    {
      key: "lastUpdated",
      header: "Last Updated",
      render: (item: DnsZone) => (
        <span className="text-panel-muted text-sm">{item.lastUpdated}</span>
      ),
    },
    {
      key: "actions",
      header: "",
      render: (item: DnsZone) => (
        <div className="flex items-center gap-1 justify-end">
          <button className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors">
            <Pencil size={14} />
          </button>
          <button
            onClick={() => handleDelete(item.id, item.domain)}
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
      <Card
        title="DNS Zones"
        description="Manage DNS zones and records for your domains"
        actions={
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="sm" onClick={fetchZones}>
              <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus size={14} className="mr-1" /> Add Zone
            </Button>
          </div>
        }
      >
        <div className="mb-4">
          <div className="relative max-w-xs">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search DNS zones..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
        </div>
        <Table
          columns={columns}
          data={filtered as any}
          loading={loading}
          emptyMessage="No DNS zones found. Add a zone to manage DNS records."
        />
      </Card>

      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Add DNS Zone" size="sm">
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
