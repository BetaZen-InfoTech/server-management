import { useEffect, useState } from "react";
import { Card, Button } from "@serverpanel/ui";
import api from "@/lib/api";
import { Activity, RefreshCw, CheckCircle2, XCircle, Gauge, MemoryStick, HardDrive } from "lucide-react";

interface ServiceRow {
  name: string;
  version: string;
  active: boolean;
  status: string;
}

interface DiskInfo {
  device: string;
  size: string;
  used: string;
  avail: string;
  use_percent: number;
  mount_point: string;
}

interface ServiceSummary {
  services: ServiceRow[];
  load_average: number[];
  cpu_count: number;
  memory_total: number;
  memory_used: number;
  swap_total: number;
  swap_used: number;
  disks: DiskInfo[];
}

function fmtBytes(n: number): string {
  if (!n || n <= 0) return "0";
  return n.toLocaleString();
}

function fmtPct(used: number, total: number): string {
  if (!total) return "0%";
  return ((used / total) * 100).toFixed(2) + "%";
}

function usageBarColor(pct: number): string {
  if (pct >= 90) return "bg-red-500";
  if (pct >= 75) return "bg-amber-500";
  return "bg-emerald-500";
}

const sectionTitle = "flex items-center gap-2 text-sm font-semibold text-panel-text mb-3";

export default function ServiceStatusPage() {
  const [data, setData] = useState<ServiceSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);

  const load = async () => {
    setLoading(true);
    setErr(null);
    try {
      const res = await api.get("/monitor/service-status");
      setData(res.data?.data || null);
    } catch (e: any) {
      setErr(e?.response?.data?.error?.message || "Failed to load service status");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { void load(); }, []);

  const memPct = data?.memory_total ? (data.memory_used / data.memory_total) * 100 : 0;
  const swapPct = data?.swap_total ? (data.swap_used / data.swap_total) * 100 : 0;
  const load1 = data?.load_average?.[0] ?? 0;
  const loadPerCPU = data?.cpu_count ? load1 / data.cpu_count : 0;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
            <Activity size={20} /> Service Status
          </h1>
          <p className="text-panel-muted text-sm mt-1">
            Running state of the key system services plus a rollup of load,
            memory, swap and disks.
          </p>
        </div>
        <Button onClick={load} disabled={loading}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm">
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
        </Button>
      </div>

      {err && <Card><div className="p-4 text-sm text-red-400">{err}</div></Card>}

      {/* Service Information */}
      <Card>
        <div className="p-5">
          <h2 className={sectionTitle}><Activity size={16} className="text-blue-400" /> Service Information</h2>
          {loading && !data ? (
            <div className="space-y-2">
              {[1,2,3,4,5,6].map(i => <div key={i} className="h-10 bg-panel-border/20 rounded animate-pulse" />)}
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-left text-xs uppercase tracking-wider text-panel-muted border-b border-panel-border">
                    <th className="py-2 pr-4">Service</th>
                    <th className="py-2 pr-4">Version</th>
                    <th className="py-2 pr-4 w-24">Status</th>
                  </tr>
                </thead>
                <tbody>
                  {(data?.services || []).map((s, i) => (
                    <tr key={`${s.name}-${i}`} className="border-b border-panel-border/50">
                      <td className="py-2 pr-4 font-mono text-xs text-panel-text">{s.name}</td>
                      <td className="py-2 pr-4 font-mono text-xs text-panel-muted">{s.version || "—"}</td>
                      <td className="py-2 pr-4">
                        {s.active ? (
                          <span className="inline-flex items-center gap-1.5 text-xs text-emerald-400">
                            <CheckCircle2 size={14} /> up
                          </span>
                        ) : (
                          <span className="inline-flex items-center gap-1.5 text-xs text-panel-muted">
                            <XCircle size={14} /> {s.status || "inactive"}
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                  {(data?.services || []).length === 0 && (
                    <tr><td colSpan={3} className="py-4 text-center text-sm text-panel-muted">No services reported.</td></tr>
                  )}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </Card>

      {/* System Information rollup */}
      <Card>
        <div className="p-5">
          <h2 className={sectionTitle}><Gauge size={16} className="text-amber-400" /> System Information</h2>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs uppercase tracking-wider text-panel-muted border-b border-panel-border">
                  <th className="py-2 pr-4 w-48">System Item</th>
                  <th className="py-2 pr-4">Details</th>
                  <th className="py-2 pr-4 w-24">Status</th>
                </tr>
              </thead>
              <tbody>
                <tr className="border-b border-panel-border/50">
                  <td className="py-2 pr-4 font-medium text-panel-text">Server Load</td>
                  <td className="py-2 pr-4 text-panel-text">
                    {(data?.load_average || []).map(v => v.toFixed(2)).join(" / ") || "—"}
                    {data?.cpu_count ? ` (${data.cpu_count} CPUs)` : ""}
                  </td>
                  <td className="py-2 pr-4">
                    {loadPerCPU < 0.7 ? (
                      <CheckCircle2 size={16} className="text-emerald-400" />
                    ) : loadPerCPU < 1 ? (
                      <Gauge size={16} className="text-amber-400" />
                    ) : (
                      <XCircle size={16} className="text-red-400" />
                    )}
                  </td>
                </tr>
                <tr className="border-b border-panel-border/50">
                  <td className="py-2 pr-4 font-medium text-panel-text">Memory Used</td>
                  <td className="py-2 pr-4 text-panel-text">
                    {fmtPct(data?.memory_used ?? 0, data?.memory_total ?? 0)} ({fmtBytes(data?.memory_used ?? 0)} of {fmtBytes(data?.memory_total ?? 0)})
                  </td>
                  <td className="py-2 pr-4">
                    {memPct < 80 ? (
                      <CheckCircle2 size={16} className="text-emerald-400" />
                    ) : memPct < 95 ? (
                      <Gauge size={16} className="text-amber-400" />
                    ) : (
                      <XCircle size={16} className="text-red-400" />
                    )}
                  </td>
                </tr>
                <tr>
                  <td className="py-2 pr-4 font-medium text-panel-text">Swap Used</td>
                  <td className="py-2 pr-4 text-panel-text">
                    {fmtPct(data?.swap_used ?? 0, data?.swap_total ?? 0)} ({fmtBytes(data?.swap_used ?? 0)} of {fmtBytes(data?.swap_total ?? 0)})
                  </td>
                  <td className="py-2 pr-4">
                    {swapPct < 50 ? (
                      <CheckCircle2 size={16} className="text-emerald-400" />
                    ) : swapPct < 90 ? (
                      <Gauge size={16} className="text-amber-400" />
                    ) : (
                      <XCircle size={16} className="text-red-400" />
                    )}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </Card>

      {/* Disk Information rollup */}
      <Card>
        <div className="p-5">
          <h2 className={sectionTitle}><HardDrive size={16} className="text-cyan-400" /> Disk Information</h2>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs uppercase tracking-wider text-panel-muted border-b border-panel-border">
                  <th className="py-2 pr-4">Device</th>
                  <th className="py-2 pr-4">Mount Point</th>
                  <th className="py-2 pr-4 w-[160px]">Usage</th>
                  <th className="py-2 pr-4 w-24">Status</th>
                </tr>
              </thead>
              <tbody>
                {(data?.disks || []).map((d, i) => (
                  <tr key={`${d.device}-${i}`} className="border-b border-panel-border/50">
                    <td className="py-2 pr-4 font-mono text-xs text-panel-text">{d.device}</td>
                    <td className="py-2 pr-4 font-mono text-xs text-panel-muted">{d.mount_point}</td>
                    <td className="py-2 pr-4">
                      <div className="flex items-center gap-2">
                        <div className="h-1.5 flex-1 rounded-full bg-panel-border/50 overflow-hidden">
                          <div className={`h-full ${usageBarColor(d.use_percent)}`} style={{ width: `${Math.min(100, Math.max(0, d.use_percent))}%` }} />
                        </div>
                        <span className="text-xs text-panel-muted w-16 text-right">{d.use_percent}% ({d.used}/{d.size})</span>
                      </div>
                    </td>
                    <td className="py-2 pr-4">
                      {d.use_percent < 80 ? (
                        <CheckCircle2 size={16} className="text-emerald-400" />
                      ) : d.use_percent < 90 ? (
                        <Gauge size={16} className="text-amber-400" />
                      ) : (
                        <XCircle size={16} className="text-red-400" />
                      )}
                    </td>
                  </tr>
                ))}
                {(data?.disks || []).length === 0 && (
                  <tr><td colSpan={4} className="py-4 text-center text-sm text-panel-muted">No disk data available.</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      </Card>

      {/* Quick footer with live memory icon for visual parity */}
      <div className="text-xs text-panel-muted flex items-center gap-2">
        <MemoryStick size={12} /> Data from <code className="text-panel-text">free -b</code>, <code className="text-panel-text">/proc/loadavg</code> and <code className="text-panel-text">df -hT</code>. Auto-refresh via the button above.
      </div>
    </div>
  );
}
