import { useState, useEffect } from "react";
import { Card, Button, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  HardDrive, RefreshCw, Globe, ArrowUpRight, Database,
  ChevronRight, Mail, AppWindow, FileText, User,
  ArrowDownToLine, ArrowUpFromLine, ExternalLink, X,
} from "lucide-react";

interface DiskQuota {
  path: string;
  used: number;
  total: number;
  percent: number;
}

interface DomainBandwidth {
  domain: string;
  bytesIn: string;
  bytesOut: string;
  totalTransfer: string;
  percent: number;
}

interface AppDetail {
  name: string;
  framework: string;
  app_type: string;
  install_path: string;
  status: string;
  port: number;
  bytes: number;
}

interface DBDetail {
  name: string;
  type: string;
  bytes: number;
}

interface RecentRequest {
  ip: string;
  time: string;
  method: string;
  path: string;
  status: number;
  bytes: number;
}

// DomainDetail mirrors the shape ResourceService.DomainUsage returns.
// Kept deliberately loose on the `counts` map so the UI doesn't have to
// be updated every time the backend adds a new counted resource type.
interface DomainDetail {
  domain: string;
  user: string;
  php_version?: string;
  disk: {
    home_bytes: number;
    apps: AppDetail[];
    databases: DBDetail[];
    databases_bytes: number;
    mail_bytes: number;
    public_html_bytes: number;
  };
  bandwidth: {
    bytes_in: number;
    bytes_out: number;
    total_bytes: number;
    request_count: number;
  };
  recent_requests: RecentRequest[];
  counts: Record<string, number>;
}

// bytesToHuman renders a byte count as KB / MB / GB. Matches what the
// backend's bytesToHuman does for the bandwidth column, so the detail
// drawer's per-app figures read consistently with the list.
function bytesToHuman(n: number | undefined | null): string {
  if (!n || n < 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let val = n;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return `${val.toFixed(val < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
}

export default function ResourcesPage() {
  const [diskQuotas, setDiskQuotas] = useState<DiskQuota[]>([]);
  const [bandwidth, setBandwidth] = useState<DomainBandwidth[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Domain detail drawer state — clicking a bandwidth card fetches the
  // rich payload (apps/db/mail breakdown + request log tail) and opens
  // the modal. `detailDomain` is only the string used by the title / loader;
  // the fetched payload lives in `detail`.
  const [detailDomain, setDetailDomain] = useState<string | null>(null);
  const [detail, setDetail] = useState<DomainDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  useEffect(() => {
    fetchResources();
  }, []);

  const openDomainDetail = async (domain: string) => {
    setDetailDomain(domain);
    setDetail(null);
    setDetailLoading(true);
    try {
      const res = await api.get(`/resources/domains/${encodeURIComponent(domain)}`);
      setDetail(res.data?.data || null);
    } catch {
      toast.error(`Failed to load details for ${domain}`);
    } finally {
      setDetailLoading(false);
    }
  };

  const fetchResources = async () => {
    setLoading(true);
    setError(null);
    try {
      const [diskRes, bwRes] = await Promise.allSettled([
        api.get("/resources/summary"),
        api.get("/resources/bandwidth"),
      ]);

      if (diskRes.status === "fulfilled") {
        const data = diskRes.value.data.data;
        setDiskQuotas(Array.isArray(data) ? data : []);
      } else {
        console.error("Failed to fetch disk data:", diskRes.reason);
      }

      if (bwRes.status === "fulfilled") {
        const data = bwRes.value.data.data;
        setBandwidth(Array.isArray(data) ? data : []);
      } else {
        console.error("Failed to fetch bandwidth data:", bwRes.reason);
      }
    } catch (err) {
      setError("Failed to fetch resource data");
      toast.error("Failed to fetch resource data");
    } finally {
      setLoading(false);
    }
  };

  const getColorForPercent = (percent: number) => {
    if (percent >= 90) return "bg-red-500";
    if (percent >= 70) return "bg-yellow-500";
    if (percent >= 50) return "bg-blue-500";
    return "bg-green-500";
  };

  const getTextColorForPercent = (percent: number) => {
    if (percent >= 90) return "text-red-400";
    if (percent >= 70) return "text-yellow-400";
    return "text-panel-muted";
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Resources</h1>
          <p className="text-panel-muted text-sm mt-1">
            Monitor disk usage and bandwidth consumption
          </p>
        </div>
        <Button
          onClick={fetchResources}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
        >
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
          Refresh
        </Button>
      </div>

      {/* Disk Quotas */}
      <div>
        <h2 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-3 flex items-center gap-2">
          <HardDrive size={14} />
          Disk Quotas
        </h2>
        <div className="space-y-3">
          {loading ? (
            [1, 2, 3].map((i) => (
              <Card key={i}>
                <div className="p-4 h-16 bg-panel-border/20 rounded animate-pulse" />
              </Card>
            ))
          ) : diskQuotas.length === 0 ? (
            <Card>
              <div className="p-6 text-center text-panel-muted text-sm">
                No disk usage data available. Ensure the server agent is running.
              </div>
            </Card>
          ) : (
            diskQuotas.map((disk) => (
              <Card key={disk.path}>
                <div className="p-4">
                  <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <Database size={14} className="text-blue-400" />
                      <code className="text-sm font-mono text-panel-text">{disk.path}</code>
                    </div>
                    <span className={`text-sm font-medium ${getTextColorForPercent(disk.percent)}`}>
                      {disk.used} GB / {disk.total} GB ({disk.percent}%)
                    </span>
                  </div>
                  <div className="w-full h-2.5 bg-panel-bg rounded-full overflow-hidden">
                    <div
                      className={`h-full rounded-full transition-all ${getColorForPercent(disk.percent)}`}
                      style={{ width: `${disk.percent}%` }}
                    />
                  </div>
                </div>
              </Card>
            ))
          )}
        </div>
      </div>

      {/* Bandwidth Usage */}
      <div>
        <h2 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-3 flex items-center gap-2">
          <ArrowUpRight size={14} />
          Bandwidth Usage (This Month)
        </h2>
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {loading ? (
            [1, 2, 3, 4].map((i) => (
              <Card key={i}>
                <div className="p-5 h-28 bg-panel-border/20 rounded animate-pulse" />
              </Card>
            ))
          ) : bandwidth.length === 0 ? (
            <Card>
              <div className="p-6 text-center text-panel-muted text-sm">
                No bandwidth data available. Bandwidth is tracked from nginx access logs.
              </div>
            </Card>
          ) : (
            bandwidth.map((bw) => (
              <Card key={bw.domain}>
                <button
                  type="button"
                  onClick={() => openDomainDetail(bw.domain)}
                  className="w-full text-left p-5 group"
                  title="Click for full statistics — apps, databases, mail, recent requests"
                >
                  <div className="flex items-center justify-between mb-3">
                    <div className="flex items-center gap-2">
                      <Globe size={14} className="text-cyan-400" />
                      <span className="font-medium text-panel-text group-hover:text-blue-400 transition-colors">{bw.domain}</span>
                    </div>
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium text-panel-text">{bw.totalTransfer}</span>
                      <ChevronRight size={14} className="text-panel-muted group-hover:text-blue-400 transition-colors" />
                    </div>
                  </div>
                  <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden mb-3">
                    <div
                      className={`h-full rounded-full transition-all ${getColorForPercent(bw.percent)}`}
                      style={{ width: `${bw.percent}%` }}
                    />
                  </div>
                  <div className="flex items-center justify-between text-xs text-panel-muted">
                    <span className="inline-flex items-center gap-1"><ArrowDownToLine size={11} /> In: {bw.bytesIn}</span>
                    <span className="inline-flex items-center gap-1"><ArrowUpFromLine size={11} /> Out: {bw.bytesOut}</span>
                  </div>
                </button>
              </Card>
            ))
          )}
        </div>
      </div>

      {error && (
        <div className="text-center py-2">
          <p className="text-xs text-red-400">{error}</p>
        </div>
      )}

      {/* Domain detail drawer — full per-domain breakdown.
          Opens when an operator clicks any bandwidth card. Renders:
          - Overview (owner, PHP, counts)
          - Disk breakdown (home / apps / databases / mail / public_html)
          - Per-app list with install path + byte count + status
          - Per-database list with byte count
          - Bandwidth + request count
          - Tail of the nginx access log for "what just happened" visibility
      */}
      {detailDomain && (
        <Modal
          isOpen
          onClose={() => { setDetailDomain(null); setDetail(null); }}
          title={`Details — ${detailDomain}`}
          size="xl"
        >
          {detailLoading ? (
            <div className="space-y-3">
              {[1, 2, 3].map((i) => (
                <div key={i} className="h-20 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          ) : !detail ? (
            <div className="text-center text-sm text-panel-muted py-6">
              Failed to load — the domain record may be missing or the server agent not reachable.
            </div>
          ) : (
            <div className="space-y-5 max-h-[75vh] overflow-y-auto pr-1">
              {/* Overview */}
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-xs">
                <OverviewStat icon={<User size={13} className="text-purple-400" />} label="Owner" value={detail.user || "—"} />
                <OverviewStat icon={<FileText size={13} className="text-amber-400" />} label="PHP" value={detail.php_version || "—"} />
                <OverviewStat icon={<AppWindow size={13} className="text-green-400" />} label="Apps" value={String(detail.counts?.apps ?? 0)} />
                <OverviewStat icon={<Database size={13} className="text-blue-400" />} label="Databases" value={String(detail.counts?.databases ?? 0)} />
                <OverviewStat icon={<Mail size={13} className="text-cyan-400" />} label="Mailboxes" value={String(detail.counts?.mailboxes ?? 0)} />
                <OverviewStat icon={<Globe size={13} className="text-indigo-400" />} label="Subdomains" value={String(detail.counts?.subdomains ?? 0)} />
                <OverviewStat icon={<ArrowDownToLine size={13} className="text-green-400" />} label="Bytes In" value={bytesToHuman(detail.bandwidth?.bytes_in)} />
                <OverviewStat icon={<ArrowUpFromLine size={13} className="text-red-400" />} label="Bytes Out" value={bytesToHuman(detail.bandwidth?.bytes_out)} />
              </div>

              {/* Disk breakdown */}
              <div>
                <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-2 flex items-center gap-2">
                  <HardDrive size={13} /> Disk Breakdown
                </h3>
                <DiskBreakdownBars detail={detail} />
              </div>

              {/* Apps */}
              {detail.disk.apps.length > 0 && (
                <div>
                  <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-2 flex items-center gap-2">
                    <AppWindow size={13} /> Applications ({detail.disk.apps.length})
                  </h3>
                  <div className="border border-panel-border rounded-lg divide-y divide-panel-border">
                    {detail.disk.apps.map((a) => (
                      <div key={a.name} className="px-3 py-2 text-sm flex items-center justify-between">
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-2">
                            <span className="font-medium text-panel-text truncate">{a.name}</span>
                            {a.framework && (
                              <span className="text-[10px] px-1.5 py-0.5 rounded bg-blue-500/10 text-blue-400 border border-blue-500/20">{a.framework}</span>
                            )}
                            <span className={`text-[10px] px-1.5 py-0.5 rounded ${a.status === "running" ? "bg-green-500/10 text-green-400 border border-green-500/20" : "bg-panel-border/30 text-panel-muted border border-panel-border"}`}>
                              {a.status || "unknown"}
                            </span>
                            {a.port > 0 && (
                              <code className="text-[10px] text-panel-muted">:{a.port}</code>
                            )}
                          </div>
                          <code className="block text-[10px] text-panel-muted/70 mt-0.5 truncate">{a.install_path}</code>
                        </div>
                        <span className="text-xs text-panel-text font-mono whitespace-nowrap">{bytesToHuman(a.bytes)}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {/* Databases */}
              {detail.disk.databases.length > 0 && (
                <div>
                  <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-2 flex items-center gap-2">
                    <Database size={13} /> Databases ({detail.disk.databases.length})
                  </h3>
                  <div className="border border-panel-border rounded-lg divide-y divide-panel-border">
                    {detail.disk.databases.map((db) => (
                      <div key={db.name} className="px-3 py-2 text-sm flex items-center justify-between">
                        <div className="flex items-center gap-2">
                          <span className={`text-[10px] px-1.5 py-0.5 rounded ${db.type === "mysql" ? "bg-amber-500/10 text-amber-400 border border-amber-500/20" : "bg-green-500/10 text-green-400 border border-green-500/20"}`}>
                            {db.type}
                          </span>
                          <code className="text-panel-text">{db.name}</code>
                        </div>
                        <span className="text-xs text-panel-text font-mono">{bytesToHuman(db.bytes)}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {/* Recent requests */}
              {detail.recent_requests && detail.recent_requests.length > 0 && (
                <div>
                  <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-2 flex items-center gap-2">
                    <ArrowUpRight size={13} /> Recent Requests (last {detail.recent_requests.length})
                  </h3>
                  <div className="border border-panel-border rounded-lg overflow-hidden">
                    <div className="max-h-60 overflow-y-auto">
                      <table className="w-full text-xs">
                        <thead className="bg-panel-surface sticky top-0">
                          <tr className="text-left text-panel-muted">
                            <th className="px-2 py-1.5 font-medium">Status</th>
                            <th className="px-2 py-1.5 font-medium">Method</th>
                            <th className="px-2 py-1.5 font-medium">Path</th>
                            <th className="px-2 py-1.5 font-medium">IP</th>
                            <th className="px-2 py-1.5 font-medium text-right">Bytes</th>
                          </tr>
                        </thead>
                        <tbody className="divide-y divide-panel-border">
                          {detail.recent_requests.map((r, i) => (
                            <tr key={i} className="hover:bg-panel-bg/50">
                              <td className={`px-2 py-1 font-mono ${statusColor(r.status)}`}>{r.status || "—"}</td>
                              <td className="px-2 py-1 font-mono text-panel-muted">{r.method}</td>
                              <td className="px-2 py-1 font-mono text-panel-text truncate max-w-[240px]">{r.path}</td>
                              <td className="px-2 py-1 font-mono text-panel-muted">{r.ip}</td>
                              <td className="px-2 py-1 font-mono text-panel-muted text-right">{bytesToHuman(r.bytes)}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </div>
                </div>
              )}

              {/* Total requests */}
              <div className="flex items-center justify-between text-xs text-panel-muted pt-2 border-t border-panel-border">
                <span>Total requests served (all-time): <b className="text-panel-text">{(detail.bandwidth?.request_count ?? 0).toLocaleString()}</b></span>
                <a
                  href={`https://${detailDomain}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 text-blue-400 hover:underline"
                >
                  <ExternalLink size={11} /> Open site
                </a>
              </div>
            </div>
          )}
        </Modal>
      )}
    </div>
  );
}

// OverviewStat is the small label+value tile at the top of the detail
// drawer. Keeps the grid rows visually consistent regardless of whether a
// given field was set (falls back to "—").
function OverviewStat({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <div className="border border-panel-border rounded-lg px-3 py-2 bg-panel-bg/50">
      <div className="flex items-center gap-1.5 text-panel-muted text-[10px] uppercase tracking-wider">
        {icon} {label}
      </div>
      <div className="text-panel-text font-medium mt-0.5 text-sm truncate">{value}</div>
    </div>
  );
}

// DiskBreakdownBars renders one bar per category (apps / databases / mail /
// public_html). Each bar's width is the category's share of the home dir
// total so the operator can see at a glance where the vendor's disk is
// actually going.
function DiskBreakdownBars({ detail }: { detail: DomainDetail }) {
  const appsBytes = detail.disk.apps.reduce((sum, a) => sum + (a.bytes || 0), 0);
  const dbBytes = detail.disk.databases_bytes || 0;
  const mailBytes = detail.disk.mail_bytes || 0;
  const publicBytes = detail.disk.public_html_bytes || 0;
  const home = detail.disk.home_bytes || 0;
  const other = Math.max(0, home - appsBytes - mailBytes - publicBytes);
  const total = Math.max(home, appsBytes + dbBytes + mailBytes + publicBytes);
  const rows = [
    { label: "Applications", bytes: appsBytes, color: "bg-green-500" },
    { label: "Databases", bytes: dbBytes, color: "bg-blue-500" },
    { label: "Mail", bytes: mailBytes, color: "bg-cyan-500" },
    { label: "public_html (legacy)", bytes: publicBytes, color: "bg-purple-500" },
    { label: "Other files in home", bytes: other, color: "bg-amber-500" },
  ].filter((r) => r.bytes > 0);
  if (rows.length === 0) {
    return <div className="text-xs text-panel-muted py-2">No disk usage detected yet.</div>;
  }
  return (
    <div className="space-y-2">
      {rows.map((r) => (
        <div key={r.label}>
          <div className="flex items-center justify-between text-xs mb-1">
            <span className="text-panel-muted">{r.label}</span>
            <span className="text-panel-text font-mono">{bytesToHuman(r.bytes)}</span>
          </div>
          <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden">
            <div
              className={`h-full rounded-full transition-all ${r.color}`}
              style={{ width: `${total > 0 ? Math.min(100, (r.bytes / total) * 100) : 0}%` }}
            />
          </div>
        </div>
      ))}
    </div>
  );
}

// statusColor picks a text colour for HTTP status codes in the recent-
// requests table. 2xx = green, 3xx = blue, 4xx = amber, 5xx = red,
// everything else (rare / unparsed) = neutral.
function statusColor(status: number): string {
  if (status >= 500) return "text-red-400";
  if (status >= 400) return "text-amber-400";
  if (status >= 300) return "text-blue-400";
  if (status >= 200) return "text-green-400";
  return "text-panel-muted";
}
