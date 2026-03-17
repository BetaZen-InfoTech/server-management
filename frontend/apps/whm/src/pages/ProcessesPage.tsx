import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Cpu, RefreshCw, Search, XCircle, AlertTriangle } from "lucide-react";

interface Process {
  pid: string;
  command: string;
  user: string;
  cpu: number;
  memory: number;
  stat: string;
  time: string;
}

export default function ProcessesPage() {
  const [processes, setProcesses] = useState<Process[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [sortBy, setSortBy] = useState<"cpu" | "mem">("cpu");

  useEffect(() => {
    fetchProcesses();
  }, []);

  const fetchProcesses = async () => {
    setLoading(true);
    try {
      const res = await api.get("/processes");
      setProcesses(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleKill = async (pid: string, command: string) => {
    if (!confirm(`Are you sure you want to kill process "${command}" (PID: ${pid})?`)) return;
    try {
      await api.post(`/processes/${pid}/kill`);
      toast.success(`Process (PID: ${pid}) killed`);
      fetchProcesses();
    } catch {
      toast.error("Failed to kill process");
    }
  };

  const filtered = processes
    .filter(
      (p) =>
        (p.command || "").toLowerCase().includes(search.toLowerCase()) ||
        String(p.pid).includes(search)
    )
    .sort((a, b) =>
      sortBy === "cpu" ? (b.cpu || 0) - (a.cpu || 0) : (b.memory || 0) - (a.memory || 0)
    );

  const columns = [
    {
      header: "PID",
      accessor: (p: Process) => (
        <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">
          {p.pid}
        </code>
      ),
    },
    {
      header: "Command",
      accessor: (p: Process) => (
        <div className="flex items-center gap-2">
          <Cpu size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text truncate max-w-[300px]">{p.command}</span>
        </div>
      ),
    },
    {
      header: "User",
      accessor: (p: Process) => (
        <span className="text-panel-muted text-sm">{p.user}</span>
      ),
    },
    {
      header: "CPU %",
      accessor: (p: Process) => (
        <div className="flex items-center gap-2">
          <div className="w-16 h-1.5 bg-panel-bg rounded-full overflow-hidden">
            <div
              className={`h-full rounded-full ${
                p.cpu > 80 ? "bg-red-500" : p.cpu > 50 ? "bg-yellow-500" : "bg-blue-500"
              }`}
              style={{ width: `${Math.min(p.cpu || 0, 100)}%` }}
            />
          </div>
          <span className={`text-sm font-medium ${
            p.cpu > 80 ? "text-red-400" : "text-panel-muted"
          }`}>
            {(p.cpu || 0).toFixed(1)}%
          </span>
        </div>
      ),
    },
    {
      header: "Memory %",
      accessor: (p: Process) => (
        <div className="flex items-center gap-2">
          <div className="w-16 h-1.5 bg-panel-bg rounded-full overflow-hidden">
            <div
              className={`h-full rounded-full ${
                p.memory > 80 ? "bg-red-500" : p.memory > 50 ? "bg-yellow-500" : "bg-green-500"
              }`}
              style={{ width: `${Math.min(p.memory || 0, 100)}%` }}
            />
          </div>
          <span className={`text-sm font-medium ${
            p.memory > 80 ? "text-red-400" : "text-panel-muted"
          }`}>
            {(p.memory || 0).toFixed(1)}%
          </span>
        </div>
      ),
    },
    {
      header: "Status",
      accessor: (p: Process) => {
        const s = p.stat || "";
        const status = s.startsWith("Z") ? "error" : s.startsWith("R") ? "active" : "inactive";
        return <StatusBadge status={status} />;
      },
    },
    {
      header: "Actions",
      accessor: (p: Process) => (
        <button
          onClick={() => handleKill(p.pid, p.command)}
          className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors flex items-center gap-1 text-xs"
          title="Kill Process"
        >
          <XCircle size={14} />
        </button>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Processes</h1>
          <p className="text-panel-muted text-sm mt-1">
            Monitor and manage running server processes
          </p>
        </div>
        <Button
          onClick={fetchProcesses}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
        >
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
          Refresh
        </Button>
      </div>

      <Card>
        <div className="p-4 flex items-center gap-4 flex-wrap">
          <div className="relative flex-1 min-w-[200px]">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search by command or PID..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
          <div className="flex items-center gap-1">
            <span className="text-sm text-panel-muted mr-2">Sort by:</span>
            <button
              onClick={() => setSortBy("cpu")}
              className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
                sortBy === "cpu"
                  ? "bg-blue-600 text-white"
                  : "bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border"
              }`}
            >
              CPU
            </button>
            <button
              onClick={() => setSortBy("mem")}
              className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
                sortBy === "mem"
                  ? "bg-blue-600 text-white"
                  : "bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border"
              }`}
            >
              Memory
            </button>
          </div>
        </div>
      </Card>

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
            <Cpu size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No processes found</h3>
            <p className="text-panel-muted text-sm max-w-md mx-auto">
              {search
                ? "No processes match your search. Try a different search term."
                : "Process information will appear here once connected to the server."}
            </p>
          </div>
        )}
      </Card>

      {/* Warning Note */}
      <Card>
        <div className="p-4 flex items-start gap-3">
          <AlertTriangle size={18} className="text-yellow-400 shrink-0 mt-0.5" />
          <div>
            <p className="text-sm font-medium text-panel-text">Caution</p>
            <p className="text-xs text-panel-muted mt-0.5">
              Killing system-critical processes may cause server instability. Only terminate processes you are certain about.
            </p>
          </div>
        </div>
      </Card>
    </div>
  );
}
