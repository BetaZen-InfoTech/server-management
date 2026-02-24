import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Mail, Plus, RefreshCw, Search, Trash2, Edit } from "lucide-react";

interface Mailbox {
  id: string;
  address: string;
  quotaUsed: string;
  quotaTotal: string;
  quotaPercent: number;
  status: "active" | "suspended" | "inactive";
}

export default function EmailPage() {
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  useEffect(() => {
    fetchMailboxes();
  }, []);

  const fetchMailboxes = async () => {
    setLoading(true);
    try {
      const res = await api.get("/email/mailboxes");
      setMailboxes(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async (id: string, address: string) => {
    if (!confirm(`Are you sure you want to delete mailbox ${address}?`)) return;
    try {
      await api.delete(`/email/mailboxes/${id}`);
      toast.success(`Mailbox ${address} deleted`);
      fetchMailboxes();
    } catch {
      toast.error("Failed to delete mailbox");
    }
  };

  const filtered = mailboxes.filter((m) =>
    m.address.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Address",
      accessor: (m: Mailbox) => (
        <div className="flex items-center gap-2">
          <Mail size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{m.address}</span>
        </div>
      ),
    },
    {
      header: "Quota Used",
      accessor: (m: Mailbox) => (
        <div className="min-w-[120px]">
          <div className="flex items-center justify-between mb-1">
            <span className="text-xs text-panel-muted">
              {m.quotaUsed} / {m.quotaTotal}
            </span>
            <span className="text-xs text-panel-muted">{m.quotaPercent}%</span>
          </div>
          <div className="w-full h-1.5 bg-panel-bg rounded-full overflow-hidden">
            <div
              className={`h-full rounded-full ${
                m.quotaPercent > 90
                  ? "bg-red-500"
                  : m.quotaPercent > 70
                  ? "bg-yellow-500"
                  : "bg-blue-500"
              }`}
              style={{ width: `${m.quotaPercent}%` }}
            />
          </div>
        </div>
      ),
    },
    {
      header: "Status",
      accessor: (m: Mailbox) => <StatusBadge status={m.status} />,
    },
    {
      header: "Actions",
      accessor: (m: Mailbox) => (
        <div className="flex items-center gap-1">
          <button className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors">
            <Edit size={14} />
          </button>
          <button
            onClick={() => handleDelete(m.id, m.address)}
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
          <h1 className="text-xl font-bold text-panel-text">Email</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage email mailboxes and configurations
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchMailboxes}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => toast("Create Mailbox modal coming soon")}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Create Mailbox
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search mailboxes..."
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
            <Mail size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No mailboxes found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No mailboxes match your search. Try a different search term."
                : "Create your first email mailbox to start receiving mail on your domains."}
            </p>
            {!search && (
              <Button
                onClick={() => toast("Create Mailbox modal coming soon")}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Create Mailbox
              </Button>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
