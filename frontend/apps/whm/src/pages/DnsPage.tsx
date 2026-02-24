import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
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

export default function DnsPage() {
  const [zones, setZones] = useState<DnsZone[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  useEffect(() => {
    fetchZones();
  }, []);

  const fetchZones = async () => {
    setLoading(true);
    try {
      const res = await api.get("/dns/zones");
      setZones(res.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
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
            <Button size="sm" onClick={() => toast("Add Zone modal coming soon")}>
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
    </div>
  );
}
