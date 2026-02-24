import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { ClipboardList, RefreshCw, Search, Download, Filter, Calendar } from "lucide-react";

interface AuditEntry {
  id: string;
  timestamp: string;
  user: string;
  action: string;
  resource: string;
  resourceType: string;
  ip: string;
  status: "success" | "failure" | "warning";
  details: string;
}

type ActionFilter = "all" | "create" | "update" | "delete" | "login" | "config";

export default function AuditPage() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [actionFilter, setActionFilter] = useState<ActionFilter>("all");

  useEffect(() => {
    fetchAuditLog();
  }, [actionFilter]);

  const fetchAuditLog = async () => {
    setLoading(true);
    try {
      const params: Record<string, string> = {};
      if (actionFilter !== "all") params.action = actionFilter;
      const res = await api.get("/audit", { params });
      setEntries(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleExport = async () => {
    try {
      const res = await api.get("/audit/export", { responseType: "blob" });
      const url = URL.createObjectURL(res.data);
      const a = document.createElement("a");
      a.href = url;
      a.download = "audit-log.csv";
      a.click();
      URL.revokeObjectURL(url);
      toast.success("Audit log exported");
    } catch {
      toast.error("Failed to export audit log");
    }
  };

  const filtered = entries.filter(
    (e) =>
      e.user.toLowerCase().includes(search.toLowerCase()) ||
      e.action.toLowerCase().includes(search.toLowerCase()) ||
      e.resource.toLowerCase().includes(search.toLowerCase()) ||
      e.ip.includes(search)
  );

  const actionFilters: { value: ActionFilter; label: string }[] = [
    { value: "all", label: "All" },
    { value: "create", label: "Create" },
    { value: "update", label: "Update" },
    { value: "delete", label: "Delete" },
    { value: "login", label: "Login" },
    { value: "config", label: "Config" },
  ];

  const columns = [
    {
      header: "Timestamp",
      accessor: (e: AuditEntry) => (
        <div className="flex items-center gap-1.5">
          <Calendar size={12} className="text-panel-muted" />
          <span className="text-panel-muted text-xs font-mono">{e.timestamp}</span>
        </div>
      ),
    },
    {
      header: "User",
      accessor: (e: AuditEntry) => (
        <span className="font-medium text-panel-text text-sm">{e.user}</span>
      ),
    },
    {
      header: "Action",
      accessor: (e: AuditEntry) => {
        const colors: Record<string, string> = {
          create: "bg-green-500/10 text-green-400",
          update: "bg-blue-500/10 text-blue-400",
          delete: "bg-red-500/10 text-red-400",
          login: "bg-purple-500/10 text-purple-400",
          config: "bg-yellow-500/10 text-yellow-400",
        };
        const actionType = e.action.split(".")[0] || e.action;
        return (
          <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
            colors[actionType] || "bg-panel-bg text-panel-muted"
          }`}>
            {e.action}
          </span>
        );
      },
    },
    {
      header: "Resource",
      accessor: (e: AuditEntry) => (
        <div>
          <span className="text-panel-text text-sm">{e.resource}</span>
          <span className="text-panel-muted text-xs ml-1">({e.resourceType})</span>
        </div>
      ),
    },
    {
      header: "IP Address",
      accessor: (e: AuditEntry) => (
        <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">
          {e.ip}
        </code>
      ),
    },
    {
      header: "Status",
      accessor: (e: AuditEntry) => (
        <StatusBadge status={e.status === "success" ? "active" : e.status === "failure" ? "error" : "warning"} />
      ),
    },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Audit Log</h1>
          <p className="text-panel-muted text-sm mt-1">
            Track all user actions and system events
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={handleExport}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <Download size={14} />
            Export CSV
          </Button>
          <Button
            onClick={fetchAuditLog}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
        </div>
      </div>

      {/* Filters */}
      <Card>
        <div className="p-4 flex items-center gap-4 flex-wrap">
          <div className="relative flex-1 min-w-[200px]">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search by user, action, resource, or IP..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
          <div className="flex items-center gap-1">
            <Filter size={14} className="text-panel-muted mr-1" />
            {actionFilters.map((f) => (
              <button
                key={f.value}
                onClick={() => setActionFilter(f.value)}
                className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
                  actionFilter === f.value
                    ? "bg-blue-600 text-white"
                    : "bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border"
                }`}
              >
                {f.label}
              </button>
            ))}
          </div>
        </div>
      </Card>

      {/* Audit Table */}
      <Card>
        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2, 3, 4, 5, 6].map((i) => (
                <div key={i} className="h-10 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={filtered} />
        ) : (
          <div className="text-center py-16 px-4">
            <ClipboardList size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No audit entries found</h3>
            <p className="text-panel-muted text-sm max-w-md mx-auto">
              {search || actionFilter !== "all"
                ? "No entries match your current filters. Try adjusting the search or filter criteria."
                : "Audit log entries will appear here as users perform actions on the system."}
            </p>
          </div>
        )}
      </Card>
    </div>
  );
}
