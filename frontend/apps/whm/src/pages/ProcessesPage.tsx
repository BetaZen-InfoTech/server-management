import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal, confirmAction } from "@serverpanel/ui";
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
  ports?: number[];
  vsz?: number;
  rss?: number;
  start?: string;
  tty?: string;
}

interface ProcessDetail extends Process {
  ppid?: string;
  uid?: string;
  threads?: string;
  cmdline?: string;
  exe?: string;
}

type SortKey = "pid" | "command" | "user" | "cpu" | "memory" | "ports";

export default function ProcessesPage() {
  const [processes, setProcesses] = useState<Process[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  // sortBy: backend-side pre-sort hint. We keep it so the API returns
  // the most relevant 150 processes first (CPU-heavy or RAM-heavy);
  // client-side column sorting then re-orders that subset.
  const [sortBy, setSortBy] = useState<"cpu" | "mem">("cpu");
  // Client-side column sorting — clicking a column header rotates
  // through asc → desc → asc on the same column, or jumps to desc
  // when a different column is clicked.
  const [sortKey, setSortKey] = useState<SortKey>("cpu");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("desc");

  // Detail modal (opens on row click)
  const [detailPid, setDetailPid] = useState<string | null>(null);
  const [detail, setDetail] = useState<ProcessDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const handleColumnSort = (key: string) => {
    const k = key as SortKey;
    setSortKey((cur) => {
      if (cur === k) {
        setSortDir((d) => (d === "asc" ? "desc" : "asc"));
        return cur;
      }
      setSortDir("desc");
      return k;
    });
  };

  const openDetail = async (p: Process) => {
    setDetailPid(p.pid);
    setDetail(null);
    setDetailLoading(true);
    try {
      const res = await api.get(`/processes/${p.pid}`);
      setDetail(res.data?.data || null);
    } catch {
      toast.error("Failed to load process details");
      setDetail(p as ProcessDetail);
    } finally {
      setDetailLoading(false);
    }
  };

  useEffect(() => {
    fetchProcesses();
  }, [sortBy]);

  const fetchProcesses = async () => {
    setLoading(true);
    try {
      const res = await api.get("/processes", {
        params: { sort: sortBy === "mem" ? "memory" : "cpu", limit: 150 },
      });
      setProcesses(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleKill = async (pid: string, command: string) => {
    if (!await confirmAction({ title: "Kill process?", description: `Are you sure you want to kill process "${command}" (PID: ${pid})?`, danger: true, confirmLabel: "Kill" })) return;
    try {
      await api.post(`/processes/${pid}/kill`);
      toast.success(`Process (PID: ${pid}) killed`);
      fetchProcesses();
    } catch {
      toast.error("Failed to kill process");
    }
  };

  const filtered = processes
    .filter((p) => {
      const q = search.trim().toLowerCase();
      if (!q) return true;
      if ((p.command || "").toLowerCase().includes(q)) return true;
      if (String(p.pid).includes(q)) return true;
      if ((p.user || "").toLowerCase().includes(q)) return true;
      // Match port numbers — operator types ":3000" or "3000" to find the
      // process on that port.
      const stripped = q.replace(/^:/, "");
      if (/^\d+$/.test(stripped) && (p.ports || []).some((port) => String(port).includes(stripped))) return true;
      return false;
    })
    .sort((a, b) => {
      const dir = sortDir === "asc" ? 1 : -1;
      switch (sortKey) {
        case "pid":
          return dir * ((Number(a.pid) || 0) - (Number(b.pid) || 0));
        case "command":
          return dir * (a.command || "").localeCompare(b.command || "");
        case "user":
          return dir * (a.user || "").localeCompare(b.user || "");
        case "ports": {
          const ap = (a.ports && a.ports[0]) ?? Infinity;
          const bp = (b.ports && b.ports[0]) ?? Infinity;
          return dir * (ap - bp);
        }
        case "memory":
          return dir * ((a.memory || 0) - (b.memory || 0));
        case "cpu":
        default:
          return dir * ((a.cpu || 0) - (b.cpu || 0));
      }
    });

  const columns = [
    {
      header: "PID",
      sortKey: "pid",
      accessor: (p: Process) => (
        <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">
          {p.pid}
        </code>
      ),
    },
    {
      header: "Command",
      sortKey: "command",
      accessor: (p: Process) => (
        <div className="flex items-center gap-2">
          <Cpu size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text truncate max-w-[300px]">{p.command}</span>
        </div>
      ),
    },
    {
      header: "User",
      sortKey: "user",
      accessor: (p: Process) => (
        <span className="text-panel-muted text-sm">{p.user}</span>
      ),
    },
    {
      header: "CPU %",
      sortKey: "cpu",
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
      sortKey: "memory",
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
      header: "Ports",
      sortKey: "ports",
      accessor: (p: Process) => {
        const ports = p.ports || [];
        if (ports.length === 0) {
          return <span className="text-panel-muted/40 text-xs">—</span>;
        }
        // Show first 3 ports inline; collapse the rest into "+N more"
        const shown = ports.slice(0, 3);
        const extra = ports.length - shown.length;
        return (
          <div className="flex items-center gap-1 flex-wrap" title={`Listening on: ${ports.join(", ")}`}>
            {shown.map((port) => (
              <code key={port}
                className="text-[11px] bg-blue-500/10 text-blue-300 border border-blue-500/30 px-1.5 py-0.5 rounded font-mono">
                :{port}
              </code>
            ))}
            {extra > 0 && (
              <span className="text-[11px] text-panel-muted">+{extra}</span>
            )}
          </div>
        );
      },
    },
    {
      header: "Status",
      accessor: (p: Process) => {
        const s = p.stat || "";
        let status = "running";
        if (s.startsWith("Z")) {
          status = "failed";
        } else if (s.startsWith("T") || s.startsWith("t")) {
          status = "stopped";
        } else if (s.startsWith("X")) {
          status = "stopped";
        } else if (s.startsWith("R")) {
          status = "running";
        } else if (s.startsWith("S") || s.startsWith("D") || s.startsWith("I")) {
          status = "active";
        }
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
          <Table
            columns={columns}
            data={filtered}
            sortKey={sortKey}
            sortDir={sortDir}
            onSort={handleColumnSort}
            onRowClick={openDetail}
          />
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
              Click any row to see full details for that process.
            </p>
          </div>
        </div>
      </Card>

      {/* Process Detail Modal */}
      <Modal
        isOpen={!!detailPid}
        onClose={() => { setDetailPid(null); setDetail(null); }}
        title={detailPid ? `Process ${detailPid}` : "Process"}
        size="lg"
      >
        {detailLoading && !detail ? (
          <div className="space-y-3 p-2">
            {[1,2,3,4].map((i) => (
              <div key={i} className="h-8 bg-panel-border/20 rounded animate-pulse" />
            ))}
          </div>
        ) : detail ? (
          <div className="space-y-4">
            {/* Top row: identity + headline metrics */}
            <div className="grid grid-cols-2 gap-3">
              <div className="bg-panel-bg/50 rounded-lg p-3">
                <div className="text-[10px] uppercase tracking-wider text-panel-muted mb-1">PID / PPID</div>
                <div className="font-mono text-sm text-panel-text">{detail.pid}{detail.ppid ? <span className="text-panel-muted"> / {detail.ppid}</span> : null}</div>
              </div>
              <div className="bg-panel-bg/50 rounded-lg p-3">
                <div className="text-[10px] uppercase tracking-wider text-panel-muted mb-1">User</div>
                <div className="text-sm text-panel-text">{detail.user}</div>
              </div>
              <div className="bg-panel-bg/50 rounded-lg p-3">
                <div className="text-[10px] uppercase tracking-wider text-panel-muted mb-1">CPU</div>
                <div className="text-sm text-panel-text">{(detail.cpu || 0).toFixed(1)}%</div>
              </div>
              <div className="bg-panel-bg/50 rounded-lg p-3">
                <div className="text-[10px] uppercase tracking-wider text-panel-muted mb-1">Memory</div>
                <div className="text-sm text-panel-text">{(detail.memory || 0).toFixed(1)}%{detail.rss ? <span className="text-panel-muted text-xs ml-2">({Math.round(Number(detail.rss)/1024)} MB RSS)</span> : null}</div>
              </div>
            </div>

            {/* Listening ports */}
            {detail.ports && detail.ports.length > 0 && (
              <div>
                <div className="text-[10px] uppercase tracking-wider text-panel-muted mb-1">Listening Ports</div>
                <div className="flex flex-wrap gap-1.5">
                  {detail.ports.map((port) => (
                    <code key={port} className="text-xs bg-blue-500/10 text-blue-300 border border-blue-500/30 px-2 py-1 rounded font-mono">
                      :{port}
                    </code>
                  ))}
                </div>
              </div>
            )}

            {/* Full command line */}
            {(detail.cmdline || detail.command) && (
              <div>
                <div className="text-[10px] uppercase tracking-wider text-panel-muted mb-1">Command Line</div>
                <pre className="bg-black/40 border border-panel-border rounded-lg p-3 text-xs text-green-300 font-mono whitespace-pre-wrap break-all max-h-32 overflow-auto">
                  {(detail.cmdline || detail.command)}
                </pre>
              </div>
            )}

            {/* Misc one-liners */}
            <div className="grid grid-cols-2 gap-3 text-xs">
              {detail.exe && (
                <div className="bg-panel-bg/50 rounded-lg p-2">
                  <div className="text-[10px] uppercase tracking-wider text-panel-muted">Executable</div>
                  <div className="text-panel-text font-mono break-all">{detail.exe}</div>
                </div>
              )}
              {detail.start && (
                <div className="bg-panel-bg/50 rounded-lg p-2">
                  <div className="text-[10px] uppercase tracking-wider text-panel-muted">Started</div>
                  <div className="text-panel-text">{detail.start}</div>
                </div>
              )}
              {detail.threads && (
                <div className="bg-panel-bg/50 rounded-lg p-2">
                  <div className="text-[10px] uppercase tracking-wider text-panel-muted">Threads</div>
                  <div className="text-panel-text">{detail.threads}</div>
                </div>
              )}
              {detail.tty && detail.tty !== "?" && (
                <div className="bg-panel-bg/50 rounded-lg p-2">
                  <div className="text-[10px] uppercase tracking-wider text-panel-muted">TTY</div>
                  <div className="text-panel-text">{detail.tty}</div>
                </div>
              )}
            </div>

            <div className="flex justify-between pt-2 border-t border-panel-border">
              <button
                onClick={() => { if (detail) { handleKill(detail.pid, detail.command || ""); setDetailPid(null); } }}
                className="px-4 py-2 text-sm bg-red-600/10 hover:bg-red-600/20 text-red-400 border border-red-500/40 rounded-lg flex items-center gap-2 transition-colors"
              >
                <XCircle size={14} /> Kill process
              </button>
              <button
                onClick={() => { setDetailPid(null); setDetail(null); }}
                className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted hover:text-panel-text"
              >
                Close
              </button>
            </div>
          </div>
        ) : (
          <p className="text-sm text-panel-muted">No details available.</p>
        )}
      </Modal>
    </div>
  );
}
