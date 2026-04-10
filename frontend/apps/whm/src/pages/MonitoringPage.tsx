import { useState, useEffect } from "react";
import { Card, Button } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Activity, RefreshCw, Cpu, MemoryStick, HardDrive, Wifi, ArrowDown, ArrowUp, Clock } from "lucide-react";

interface DisplayMetrics {
  cpu: { usage: number; cores: number };
  memory: { used: number; total: number; percent: number };
  disk: { used: number; total: number; percent: number };
  network: { bytesIn: string; bytesOut: string };
  uptime: string;
  loadAvg: number[];
}

function formatBytes(bytes: number): string {
  if (bytes >= 1e12) return (bytes / 1e12).toFixed(1) + " TB";
  if (bytes >= 1e9) return (bytes / 1e9).toFixed(1) + " GB";
  if (bytes >= 1e6) return (bytes / 1e6).toFixed(1) + " MB";
  if (bytes >= 1e3) return (bytes / 1e3).toFixed(1) + " KB";
  return bytes + " B";
}

function bytesToGB(bytes: number): number {
  return Math.round((bytes / 1e9) * 10) / 10;
}

export default function MonitoringPage() {
  const [display, setDisplay] = useState<DisplayMetrics | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);

  useEffect(() => {
    fetchMetrics();
    const interval = setInterval(fetchMetrics, 30000);
    return () => clearInterval(interval);
  }, []);

  const fetchMetrics = async () => {
    setLoading(true);
    setError(false);
    try {
      const [metricsRes, systemRes] = await Promise.all([
        api.get("/monitor/metrics"),
        api.get("/monitor/system"),
      ]);
      const m = metricsRes.data.data;
      const s = systemRes.data.data;

      setDisplay({
        cpu: {
          usage: Math.round((m.cpu_percent || 0) * 10) / 10,
          cores: s.cpu_count || 0,
        },
        memory: {
          used: bytesToGB(m.memory?.used || 0),
          total: bytesToGB(m.memory?.total || 0),
          percent: Math.round(m.memory?.percent || 0),
        },
        disk: {
          used: bytesToGB(m.disk?.used || 0),
          total: bytesToGB(m.disk?.total || 0),
          percent: parseInt(m.disk?.percent || "0", 10),
        },
        network: {
          bytesIn: formatBytes(m.network?.rx_bytes || 0),
          bytesOut: formatBytes(m.network?.tx_bytes || 0),
        },
        uptime: s.uptime || "Unknown",
        loadAvg: [
          parseFloat(m.load_average?.["1m"] || "0"),
          parseFloat(m.load_average?.["5m"] || "0"),
          parseFloat(m.load_average?.["15m"] || "0"),
        ],
      });
    } catch {
      setError(true);
      setDisplay(null);
    } finally {
      setLoading(false);
    }
  };

  const getColorForPercent = (percent: number) => {
    if (percent >= 90) return "bg-red-500";
    if (percent >= 70) return "bg-yellow-500";
    return "bg-blue-500";
  };

  const getTextColorForPercent = (percent: number) => {
    if (percent >= 90) return "text-red-400";
    if (percent >= 70) return "text-yellow-400";
    return "text-blue-400";
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Monitoring</h1>
          <p className="text-panel-muted text-sm mt-1">
            Real-time server performance metrics
          </p>
        </div>
        <Button
          onClick={fetchMetrics}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
        >
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
          Refresh
        </Button>
      </div>

      {loading && !display ? (
        <div className="flex items-center justify-center py-20">
          <RefreshCw size={24} className="animate-spin text-panel-muted" />
          <span className="ml-3 text-panel-muted">Loading metrics...</span>
        </div>
      ) : error && !display ? (
        <div className="text-center py-20">
          <Activity size={40} className="text-panel-muted/30 mx-auto mb-3" />
          <p className="text-panel-muted">Failed to load server metrics.</p>
          <Button onClick={fetchMetrics} className="mt-4 px-4 py-2 bg-blue-600 text-white rounded-lg text-sm">
            Retry
          </Button>
        </div>
      ) : display ? (
        <>
          {/* Main Metrics Cards */}
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            {/* CPU */}
            <Card>
              <div className="p-5">
                <div className="flex items-center justify-between mb-4">
                  <div className="flex items-center gap-2">
                    <div className="p-2 rounded-lg bg-blue-500/10">
                      <Cpu size={18} className="text-blue-400" />
                    </div>
                    <span className="text-sm font-medium text-panel-muted">CPU</span>
                  </div>
                  <span className={`text-2xl font-bold ${getTextColorForPercent(display.cpu.usage)}`}>
                    {loading ? "--" : `${display.cpu.usage}%`}
                  </span>
                </div>
                <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden mb-3">
                  <div
                    className={`h-full rounded-full transition-all ${getColorForPercent(display.cpu.usage)}`}
                    style={{ width: `${display.cpu.usage}%` }}
                  />
                </div>
                <p className="text-xs text-panel-muted truncate">{display.cpu.cores} Cores</p>
              </div>
            </Card>

            {/* Memory */}
            <Card>
              <div className="p-5">
                <div className="flex items-center justify-between mb-4">
                  <div className="flex items-center gap-2">
                    <div className="p-2 rounded-lg bg-green-500/10">
                      <MemoryStick size={18} className="text-green-400" />
                    </div>
                    <span className="text-sm font-medium text-panel-muted">Memory</span>
                  </div>
                  <span className={`text-2xl font-bold ${getTextColorForPercent(display.memory.percent)}`}>
                    {loading ? "--" : `${display.memory.percent}%`}
                  </span>
                </div>
                <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden mb-3">
                  <div
                    className={`h-full rounded-full transition-all ${getColorForPercent(display.memory.percent)}`}
                    style={{ width: `${display.memory.percent}%` }}
                  />
                </div>
                <p className="text-xs text-panel-muted">
                  {display.memory.used} GB / {display.memory.total} GB
                </p>
              </div>
            </Card>

            {/* Disk */}
            <Card>
              <div className="p-5">
                <div className="flex items-center justify-between mb-4">
                  <div className="flex items-center gap-2">
                    <div className="p-2 rounded-lg bg-yellow-500/10">
                      <HardDrive size={18} className="text-yellow-400" />
                    </div>
                    <span className="text-sm font-medium text-panel-muted">Disk</span>
                  </div>
                  <span className={`text-2xl font-bold ${getTextColorForPercent(display.disk.percent)}`}>
                    {loading ? "--" : `${display.disk.percent}%`}
                  </span>
                </div>
                <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden mb-3">
                  <div
                    className={`h-full rounded-full transition-all ${getColorForPercent(display.disk.percent)}`}
                    style={{ width: `${display.disk.percent}%` }}
                  />
                </div>
                <p className="text-xs text-panel-muted">
                  {display.disk.used} GB / {display.disk.total} GB
                </p>
              </div>
            </Card>

            {/* Network */}
            <Card>
              <div className="p-5">
                <div className="flex items-center justify-between mb-4">
                  <div className="flex items-center gap-2">
                    <div className="p-2 rounded-lg bg-purple-500/10">
                      <Wifi size={18} className="text-purple-400" />
                    </div>
                    <span className="text-sm font-medium text-panel-muted">Network</span>
                  </div>
                  <Activity size={16} className="text-green-400" />
                </div>
                <div className="space-y-2">
                  <div className="flex items-center justify-between text-sm">
                    <span className="flex items-center gap-1 text-panel-muted">
                      <ArrowDown size={12} className="text-green-400" /> In
                    </span>
                    <span className="text-panel-text font-medium">{display.network.bytesIn}</span>
                  </div>
                  <div className="flex items-center justify-between text-sm">
                    <span className="flex items-center gap-1 text-panel-muted">
                      <ArrowUp size={12} className="text-blue-400" /> Out
                    </span>
                    <span className="text-panel-text font-medium">{display.network.bytesOut}</span>
                  </div>
                </div>
              </div>
            </Card>
          </div>

          {/* Additional Info */}
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            {/* Load Average */}
            <Card>
              <div className="p-5">
                <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-4">
                  Load Average
                </h3>
                <div className="grid grid-cols-3 gap-4">
                  {["1 min", "5 min", "15 min"].map((label, i) => (
                    <div key={label} className="text-center">
                      <p className="text-2xl font-bold text-panel-text">
                        {loading ? "--" : display.loadAvg[i]?.toFixed(2)}
                      </p>
                      <p className="text-xs text-panel-muted mt-1">{label}</p>
                    </div>
                  ))}
                </div>
              </div>
            </Card>

            {/* Uptime */}
            <Card>
              <div className="p-5">
                <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-4">
                  System Uptime
                </h3>
                <div className="flex items-center gap-3">
                  <div className="p-3 rounded-xl bg-green-500/10">
                    <Clock size={24} className="text-green-400" />
                  </div>
                  <div>
                    <p className="text-lg font-bold text-panel-text">
                      {loading ? "Loading..." : display.uptime}
                    </p>
                    <p className="text-xs text-panel-muted">Since last reboot</p>
                  </div>
                </div>
              </div>
            </Card>
          </div>

          {/* Charts Placeholder */}
          <Card>
            <div className="p-5">
              <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-4">
                Performance History
              </h3>
              <div className="h-64 bg-panel-bg rounded-lg border border-panel-border flex items-center justify-center">
                <div className="text-center">
                  <Activity size={40} className="text-panel-muted/20 mx-auto mb-2" />
                  <p className="text-panel-muted text-sm">Performance charts</p>
                  <p className="text-panel-muted/60 text-xs mt-1">
                    Historical CPU, memory, and disk usage graphs will appear here
                  </p>
                </div>
              </div>
            </div>
          </Card>
        </>
      ) : null}
    </div>
  );
}
