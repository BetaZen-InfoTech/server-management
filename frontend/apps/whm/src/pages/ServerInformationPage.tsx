import { useEffect, useState } from "react";
import { Card, Button } from "@serverpanel/ui";
import api from "@/lib/api";
import { Server, Cpu, HardDrive, RefreshCw, Terminal, MemoryStick } from "lucide-react";

interface ProcessorDetail {
  index: number;
  vendor: string;
  name: string;
  speed: string;
  cache: string;
}

interface DiskInfo {
  device: string;
  size: string;
  used: string;
  avail: string;
  use_percent: number;
  mount_point: string;
}

interface ServerDetails {
  processors: ProcessorDetail[];
  memory_boot: string;
  system: string;
  physical_disks: string[];
  current_memory_raw: string;
  current_disks: DiskInfo[];
}

const mono = "font-mono text-xs text-panel-text leading-relaxed whitespace-pre overflow-x-auto";
const sectionTitle = "flex items-center gap-2 text-sm font-semibold text-panel-text mb-3";
const labelCol = "text-panel-muted text-xs uppercase tracking-wider";

function usageBarColor(pct: number): string {
  if (pct >= 90) return "bg-red-500";
  if (pct >= 75) return "bg-amber-500";
  return "bg-emerald-500";
}

export default function ServerInformationPage() {
  const [data, setData] = useState<ServerDetails | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);

  const load = async () => {
    setLoading(true);
    setErr(null);
    try {
      const res = await api.get("/monitor/server-info");
      setData(res.data?.data || null);
    } catch (e: any) {
      setErr(e?.response?.data?.error?.message || "Failed to load server information");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { void load(); }, []);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
            <Server size={20} /> Server Information
          </h1>
          <p className="text-panel-muted text-sm mt-1">
            Static facts about the box — CPU topology, memory at boot,
            kernel version, physical disks, and the current disk / memory
            footprint.
          </p>
        </div>
        <Button onClick={load} disabled={loading}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm">
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
        </Button>
      </div>

      {err && (
        <Card>
          <div className="p-4 text-sm text-red-400">{err}</div>
        </Card>
      )}

      {/* Processor Information */}
      <Card>
        <div className="p-5">
          <h2 className={sectionTitle}>
            <Cpu size={16} className="text-blue-400" /> Processor Information
          </h2>
          {loading && !data ? (
            <div className="h-20 bg-panel-border/20 rounded animate-pulse" />
          ) : (
            <>
              <p className="text-sm text-panel-muted mb-3">
                Total processors: <span className="text-panel-text font-medium">{data?.processors.length ?? 0}</span>
              </p>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                {(data?.processors || []).map((p) => (
                  <div key={p.index} className="rounded-lg border border-panel-border bg-panel-bg/40 p-3">
                    <div className="text-sm font-semibold text-panel-text">Processor #{p.index + 1}</div>
                    <dl className="mt-2 text-xs space-y-1">
                      <div className="flex"><dt className={labelCol + " w-20"}>Vendor</dt><dd className="text-panel-text">{p.vendor || "—"}</dd></div>
                      <div className="flex"><dt className={labelCol + " w-20"}>Name</dt><dd className="text-panel-text">{p.name || "—"}</dd></div>
                      <div className="flex"><dt className={labelCol + " w-20"}>Speed</dt><dd className="text-panel-text">{p.speed || "—"}</dd></div>
                      <div className="flex"><dt className={labelCol + " w-20"}>Cache</dt><dd className="text-panel-text">{p.cache || "—"}</dd></div>
                    </dl>
                  </div>
                ))}
              </div>
              {(data?.processors || []).length === 0 && (
                <p className="text-sm text-panel-muted">No processor information available.</p>
              )}
            </>
          )}
        </div>
      </Card>

      {/* Memory Information (boot line) */}
      <Card>
        <div className="p-5">
          <h2 className={sectionTitle}><MemoryStick size={16} className="text-purple-400" /> Memory Information</h2>
          <pre className={mono}>
            {data?.memory_boot || (loading ? "Loading…" : "Kernel log not readable — boot memory line unavailable.")}
          </pre>
        </div>
      </Card>

      {/* System Information */}
      <Card>
        <div className="p-5">
          <h2 className={sectionTitle}><Terminal size={16} className="text-green-400" /> System Information</h2>
          <pre className={mono}>{data?.system || (loading ? "Loading…" : "—")}</pre>
        </div>
      </Card>

      {/* Physical Disks */}
      <Card>
        <div className="p-5">
          <h2 className={sectionTitle}><HardDrive size={16} className="text-cyan-400" /> Physical Disks</h2>
          <pre className={mono}>
            {(data?.physical_disks || []).join("\n") || (loading ? "Loading…" : "No disk information available.")}
          </pre>
        </div>
      </Card>

      {/* Current Memory Usage */}
      <Card>
        <div className="p-5">
          <h2 className={sectionTitle}><MemoryStick size={16} className="text-indigo-400" /> Current Memory Usage</h2>
          <pre className={mono}>{data?.current_memory_raw || (loading ? "Loading…" : "—")}</pre>
        </div>
      </Card>

      {/* Current Disk Usage */}
      <Card>
        <div className="p-5">
          <h2 className={sectionTitle}><HardDrive size={16} className="text-amber-400" /> Current Disk Usage</h2>
          {loading && !data ? (
            <div className="h-20 bg-panel-border/20 rounded animate-pulse" />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-left text-xs uppercase tracking-wider text-panel-muted border-b border-panel-border">
                    <th className="py-2 pr-4">Filesystem</th>
                    <th className="py-2 pr-4">Size</th>
                    <th className="py-2 pr-4">Used</th>
                    <th className="py-2 pr-4">Avail</th>
                    <th className="py-2 pr-4 w-[160px]">Use%</th>
                    <th className="py-2 pr-4">Mounted on</th>
                  </tr>
                </thead>
                <tbody>
                  {(data?.current_disks || []).map((d, i) => (
                    <tr key={`${d.device}-${i}`} className="border-b border-panel-border/50">
                      <td className="py-2 pr-4 font-mono text-xs text-panel-text">{d.device}</td>
                      <td className="py-2 pr-4 text-panel-text">{d.size}</td>
                      <td className="py-2 pr-4 text-panel-text">{d.used}</td>
                      <td className="py-2 pr-4 text-panel-text">{d.avail}</td>
                      <td className="py-2 pr-4">
                        <div className="flex items-center gap-2">
                          <div className="h-1.5 flex-1 rounded-full bg-panel-border/50 overflow-hidden">
                            <div
                              className={`h-full ${usageBarColor(d.use_percent)}`}
                              style={{ width: `${Math.min(100, Math.max(0, d.use_percent))}%` }}
                            />
                          </div>
                          <span className="text-xs text-panel-muted w-10 text-right">{d.use_percent}%</span>
                        </div>
                      </td>
                      <td className="py-2 pr-4 font-mono text-xs text-panel-muted">{d.mount_point}</td>
                    </tr>
                  ))}
                  {(data?.current_disks || []).length === 0 && (
                    <tr><td colSpan={6} className="py-4 text-center text-sm text-panel-muted">No filesystem data available.</td></tr>
                  )}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </Card>
    </div>
  );
}
