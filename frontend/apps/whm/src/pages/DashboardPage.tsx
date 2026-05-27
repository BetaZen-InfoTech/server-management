import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { Card, Button, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import { useAuthStore } from "@/store/auth";
import toast from "react-hot-toast";
import {
  Globe, AppWindow, Database, ShieldCheck, Activity,
  ArrowUpRight, Server, HardDrive, Cpu, MemoryStick,
  RefreshCw, Plus, Calendar, AlertCircle,
} from "lucide-react";

// Domain expiry subset — the dashboard only needs the bits it renders.
// v3.1.59: added registered_on (Purchased on) + nameservers so the
// widget can surface the full registrar picture without a second
// fetch when the operator clicks into a row.
interface ExpiringDomain {
  id: string;
  domain: string;
  expires_on?: string | null;
  registered_on?: string | null;
  registrar?: string;
  auto_renew?: boolean;
  nameservers?: string[];
}

// Bucket ladder (server-driven via /domains/expiring/buckets) —
// the values come from services.ExpiryBuckets() on the backend so
// the UI doesn't have to hard-code anything the cron disagrees
// with. Falls back to the canonical set if the bucket fetch fails
// (so the filter row never goes blank on a transient API error).
const FALLBACK_BUCKETS = [60, 45, 30, 15, 7, 5, 4, 3, 2, 1];
function daysUntilDate(iso?: string | null): number {
  if (!iso) return -999999;
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return -999999;
  return Math.floor((t - Date.now()) / (24 * 60 * 60 * 1000));
}

interface DashboardStats {
  totalDomains: number;
  activeApps: number;
  databases: number;
  sslCertificates: number;
}

interface RecentActivity {
  id: string;
  action: string;
  resource: string;
  timestamp: string;
  status: "success" | "error" | "warning";
}

export default function DashboardPage() {
  const navigate = useNavigate();
  const { user } = useAuthStore();
  const [loading, setLoading] = useState(true);
  const [stats, setStats] = useState<DashboardStats>({
    totalDomains: 0,
    activeApps: 0,
    databases: 0,
    sslCertificates: 0,
  });
  const [recentActivity, setRecentActivity] = useState<RecentActivity[]>([]);
  const [serverStatus, setServerStatus] = useState({
    cpuPercent: 0,
    memoryPercent: 0,
    diskPercent: 0,
    uptimeString: "N/A",
  });
  // Domains whose registration expires within `expiringDays` from
  // today. Fed by /domains/expiring (tenant-scoped). v3.1.59 made
  // the window operator-selectable via the bucket filter pills so
  // the same widget answers "what's expiring in the next 7 days"
  // and "in the next 60" without a separate page.
  const [expiring, setExpiring] = useState<ExpiringDomain[]>([]);
  const [expiringDays, setExpiringDays] = useState<number>(30);
  // Cumulative per-bucket counts so each pill can show a badge —
  // operators see "60: 12 · 30: 5 · 7: 1" at a glance and know
  // which filter has actionable data without clicking through.
  const [bucketCounts, setBucketCounts] = useState<Record<string, number>>({});
  const [bucketLadder, setBucketLadder] = useState<number[]>(FALLBACK_BUCKETS);

  useEffect(() => {
    fetchDashboardData();
  }, []);

  // Re-fetch only the expiring list when the operator clicks a
  // different bucket pill — no need to also refetch stats / activity
  // / server status (those don't depend on the days window).
  useEffect(() => {
    api.get(`/domains/expiring?days=${expiringDays}`)
      .then((r) => setExpiring(r.data?.data || []))
      .catch(() => {/* keep previous list rather than flashing empty on a transient error */});
  }, [expiringDays]);

  const fetchDashboardData = async () => {
    setLoading(true);
    try {
      const [statsRes, activityRes, serverRes, expiringRes, bucketsRes] = await Promise.allSettled([
        api.get("/dashboard/stats"),
        api.get("/dashboard/activity"),
        api.get("/dashboard/server-status"),
        api.get(`/domains/expiring?days=${expiringDays}`),
        api.get("/domains/expiring/buckets"),
      ]);

      if (statsRes.status === "fulfilled") {
        setStats(statsRes.value.data.data);
      }
      if (activityRes.status === "fulfilled") {
        setRecentActivity(activityRes.value.data.data || []);
      }
      if (serverRes.status === "fulfilled") {
        setServerStatus(serverRes.value.data.data);
      }
      if (expiringRes.status === "fulfilled") {
        setExpiring(expiringRes.value.data.data || []);
      }
      if (bucketsRes.status === "fulfilled") {
        const payload = bucketsRes.value.data?.data || {};
        if (Array.isArray(payload.buckets) && payload.buckets.length > 0) {
          setBucketLadder(payload.buckets);
        }
        setBucketCounts(payload.counts || {});
      }
    } catch {
      // Use placeholder data on error
    } finally {
      setLoading(false);
    }
  };

  const statCards = [
    {
      title: "Total Domains",
      value: stats.totalDomains,
      icon: <Globe size={20} className="text-blue-400" />,
      color: "blue",
      path: "/domains",
    },
    {
      title: "Active Apps",
      value: stats.activeApps,
      icon: <AppWindow size={20} className="text-green-400" />,
      color: "green",
      path: "/apps",
    },
    {
      title: "Databases",
      value: stats.databases,
      icon: <Database size={20} className="text-purple-400" />,
      color: "purple",
      path: "/databases",
    },
    {
      title: "SSL Certificates",
      value: stats.sslCertificates,
      icon: <ShieldCheck size={20} className="text-yellow-400" />,
      color: "yellow",
      path: "/ssl",
    },
  ];

  const quickActions = [
    { label: "Add Domain", icon: <Globe size={16} />, path: "/domains" },
    { label: "Deploy App", icon: <AppWindow size={16} />, path: "/apps" },
    { label: "Create Database", icon: <Database size={16} />, path: "/databases" },
    { label: "Issue SSL", icon: <ShieldCheck size={16} />, path: "/ssl" },
    { label: "Create Backup", icon: <HardDrive size={16} />, path: "/backups" },
    { label: "New Deployment", icon: <ArrowUpRight size={16} />, path: "/deploy" },
  ];

  return (
    <div className="space-y-6">
      {/* Welcome Banner */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-panel-text">
            Welcome back, {user?.name || "Admin"}
          </h1>
          <p className="text-panel-muted mt-1">
            Here is an overview of your server infrastructure.
          </p>
        </div>
        <Button
          onClick={fetchDashboardData}
          className="flex items-center gap-2 px-4 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors"
        >
          <RefreshCw size={16} className={loading ? "animate-spin" : ""} />
          Refresh
        </Button>
      </div>

      {/* Stats Grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        {statCards.map((stat) => (
          <Card key={stat.title}>
            <div
              className="p-5 cursor-pointer hover:bg-panel-border/10 transition-colors rounded-lg"
              onClick={() => navigate(stat.path)}
            >
              <div className="flex items-center justify-between mb-3">
                <span className="text-panel-muted text-sm font-medium">{stat.title}</span>
                <div className="p-2 rounded-lg bg-panel-bg">{stat.icon}</div>
              </div>
              <div className="flex items-end justify-between">
                <span className="text-3xl font-bold text-panel-text">
                  {loading ? (
                    <div className="h-9 w-16 bg-panel-border/30 rounded animate-pulse" />
                  ) : (
                    stat.value
                  )}
                </span>
                <ArrowUpRight size={16} className="text-panel-muted" />
              </div>
            </div>
          </Card>
        ))}
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        {/* Server Status */}
        <Card>
          <div className="p-5">
            <div className="flex items-center justify-between mb-4">
              <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
                Server Status
              </h3>
              <StatusBadge status="active" />
            </div>
            <div className="space-y-4">
              <div>
                <div className="flex items-center justify-between mb-1">
                  <span className="text-sm text-panel-muted flex items-center gap-2">
                    <Cpu size={14} /> CPU Usage
                  </span>
                  <span className="text-sm font-medium text-panel-text">{serverStatus.cpuPercent}%</span>
                </div>
                <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden">
                  <div className="h-full bg-blue-500 rounded-full" style={{ width: `${serverStatus.cpuPercent}%` }} />
                </div>
              </div>
              <div>
                <div className="flex items-center justify-between mb-1">
                  <span className="text-sm text-panel-muted flex items-center gap-2">
                    <MemoryStick size={14} /> Memory
                  </span>
                  <span className="text-sm font-medium text-panel-text">{serverStatus.memoryPercent}%</span>
                </div>
                <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden">
                  <div className="h-full bg-green-500 rounded-full" style={{ width: `${serverStatus.memoryPercent}%` }} />
                </div>
              </div>
              <div>
                <div className="flex items-center justify-between mb-1">
                  <span className="text-sm text-panel-muted flex items-center gap-2">
                    <HardDrive size={14} /> Disk
                  </span>
                  <span className="text-sm font-medium text-panel-text">{serverStatus.diskPercent}%</span>
                </div>
                <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden">
                  <div className="h-full bg-yellow-500 rounded-full" style={{ width: `${serverStatus.diskPercent}%` }} />
                </div>
              </div>
              <div className="pt-2 border-t border-panel-border">
                <div className="flex items-center justify-between text-sm">
                  <span className="text-panel-muted flex items-center gap-2">
                    <Server size={14} /> Uptime
                  </span>
                  <span className="text-panel-text font-medium">{serverStatus.uptimeString}</span>
                </div>
              </div>
            </div>
          </div>
        </Card>

        {/* Recent Activity */}
        <Card>
          <div className="p-5">
            <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-4">
              Recent Activity
            </h3>
            {loading ? (
              <div className="space-y-3">
                {[1, 2, 3, 4].map((i) => (
                  <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
                ))}
              </div>
            ) : recentActivity.length > 0 ? (
              <div className="space-y-3">
                {recentActivity.slice(0, 5).map((activity) => (
                  <div
                    key={activity.id}
                    className="flex items-start gap-3 p-2 rounded-lg hover:bg-panel-bg transition-colors"
                  >
                    <Activity size={14} className="text-panel-muted mt-0.5 shrink-0" />
                    <div className="min-w-0 flex-1">
                      <p className="text-sm text-panel-text truncate">{activity.action}</p>
                      <p className="text-xs text-panel-muted">{activity.resource}</p>
                    </div>
                    <StatusBadge status={activity.status} />
                  </div>
                ))}
              </div>
            ) : (
              <div className="text-center py-8">
                <Activity size={32} className="text-panel-muted/30 mx-auto mb-2" />
                <p className="text-sm text-panel-muted">No recent activity</p>
                <p className="text-xs text-panel-muted/60 mt-1">
                  Activity will appear here as you manage your server
                </p>
              </div>
            )}
          </div>
        </Card>

        {/* Quick Actions */}
        <Card>
          <div className="p-5">
            <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-4">
              Quick Actions
            </h3>
            <div className="grid grid-cols-2 gap-2">
              {quickActions.map((action) => (
                <button
                  key={action.label}
                  onClick={() => navigate(action.path)}
                  className="flex items-center gap-2 p-3 rounded-lg bg-panel-bg border border-panel-border hover:border-blue-500/30 hover:bg-blue-500/5 text-panel-muted hover:text-blue-400 transition-all text-sm"
                >
                  <Plus size={14} />
                  {action.label}
                </button>
              ))}
            </div>
          </div>
        </Card>

        {/* Domains expiring — operator-selectable window via the
            filter pills (60 / 45 / 30 / 15 / 7 / 5 / 4 / 3 / 2 / 1
            days). Each pill carries a count badge showing how many
            domains fall inside that bucket; clicking re-fetches
            the list at that window. Counts are CUMULATIVE — 60
            includes 30, 30 includes 7, etc. Per-row details show
            registrar / purchased-on / expires-on / nameservers so
            a vendor diagnosing a renewal can see everything at a
            glance without leaving the dashboard.

            Tenant scope is enforced backend-side — vendors only
            see their own domains + counts. Daily WHOIS-refresh
            cron keeps the registrar fields fresh (v3.1.59). */}
        <Card>
          <div className="p-5">
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider flex items-center gap-2">
                <Calendar size={14} className="text-amber-400" />
                Domains expiring soon
              </h3>
              <span className="text-xs text-panel-muted">next {expiringDays} days</span>
            </div>

            {/* Bucket filter pills */}
            <div className="flex flex-wrap gap-1.5 mb-3">
              {bucketLadder.map((b) => {
                const count = bucketCounts[String(b)] ?? 0;
                const isActive = b === expiringDays;
                const urgencyClass =
                  b <= 3 ? "text-red-300 border-red-500/40" :
                  b <= 7 ? "text-red-300 border-red-500/30" :
                  b <= 15 ? "text-amber-300 border-amber-500/30" :
                  "text-panel-muted border-panel-border";
                return (
                  <button
                    key={b}
                    onClick={() => setExpiringDays(b)}
                    className={
                      `inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md border text-[11px] font-medium transition-all ` +
                      (isActive
                        ? "bg-blue-500/20 text-blue-200 border-blue-500/40 shadow-inner"
                        : `bg-panel-bg/40 ${urgencyClass} hover:bg-panel-surface`)
                    }
                    title={`Show domains expiring in the next ${b} day${b === 1 ? "" : "s"}`}
                  >
                    {b}d
                    <span
                      className={
                        "px-1 rounded text-[10px] " +
                        (count > 0
                          ? (isActive ? "bg-blue-500/30 text-blue-100" : "bg-panel-surface text-panel-text")
                          : "bg-panel-surface/50 text-panel-muted/60")
                      }
                    >
                      {count}
                    </span>
                  </button>
                );
              })}
            </div>

            {expiring.length === 0 ? (
              <div className="text-center py-6 text-xs text-panel-muted">
                <Calendar size={24} className="text-panel-muted/30 mx-auto mb-2" />
                No domains expire in the next {expiringDays} day{expiringDays === 1 ? "" : "s"}.
                <div className="mt-1 text-[10px] text-panel-muted/70">
                  Registrar fields auto-refresh nightly via WHOIS. Set purchase / expiry from the domain's Edit Registration action to surface entries here sooner.
                </div>
              </div>
            ) : (
              <div className="space-y-2 max-h-96 overflow-y-auto">
                {expiring.slice(0, 20).map((d) => {
                  const days = daysUntilDate(d.expires_on);
                  const urgency =
                    days < 0 ? "bg-red-500/15 text-red-300 border-red-500/30" :
                    days <= 7 ? "bg-red-500/10 text-red-300 border-red-500/30" :
                    days <= 30 ? "bg-amber-500/10 text-amber-300 border-amber-500/30" :
                    "bg-emerald-500/10 text-emerald-300 border-emerald-500/30";
                  return (
                    <button
                      key={d.id}
                      onClick={() => navigate(`/domains?search=${encodeURIComponent(d.domain)}`)}
                      className="w-full flex flex-col gap-1.5 p-2.5 rounded-lg bg-panel-bg border border-panel-border hover:border-blue-500/30 hover:bg-blue-500/5 transition-all text-left"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <div className="min-w-0 flex-1">
                          <div className="text-sm text-panel-text truncate font-mono">{d.domain}</div>
                          <div className="text-[10px] text-panel-muted mt-0.5 flex items-center gap-2 flex-wrap">
                            {d.registrar && <span>{d.registrar}</span>}
                            {d.auto_renew && (
                              <span className="inline-flex items-center gap-0.5 text-emerald-400">
                                <ShieldCheck size={9} /> auto-renew
                              </span>
                            )}
                          </div>
                        </div>
                        <div className="shrink-0 text-right">
                          <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded border text-[11px] font-medium ${urgency}`}>
                            {days < 0 ? <AlertCircle size={10} /> : <Calendar size={10} />}
                            {days < 0 ? `expired ${-days}d ago` : days === 0 ? "today" : `${days}d left`}
                          </span>
                        </div>
                      </div>
                      {/* Details strip — Purchased on · Expires on · Nameservers.
                          Surfaces every field the backend stores so a vendor
                          diagnosing a renewal doesn't have to open the row. */}
                      <div className="grid grid-cols-1 sm:grid-cols-3 gap-1 text-[10px] text-panel-muted/80">
                        {d.registered_on && (
                          <div>
                            <span className="text-panel-muted/60">Purchased: </span>
                            <span className="text-panel-text/80">{new Date(d.registered_on).toISOString().slice(0, 10)}</span>
                          </div>
                        )}
                        {d.expires_on && (
                          <div>
                            <span className="text-panel-muted/60">Expires: </span>
                            <span className="text-panel-text/80">{new Date(d.expires_on).toISOString().slice(0, 10)}</span>
                          </div>
                        )}
                        {d.nameservers && d.nameservers.length > 0 && (
                          <div className="truncate" title={d.nameservers.join(", ")}>
                            <span className="text-panel-muted/60">NS: </span>
                            <span className="text-panel-text/80 font-mono">{d.nameservers[0]}{d.nameservers.length > 1 ? ` +${d.nameservers.length - 1}` : ""}</span>
                          </div>
                        )}
                      </div>
                    </button>
                  );
                })}
                {expiring.length > 20 && (
                  <button
                    onClick={() => navigate("/domains")}
                    className="w-full text-xs text-blue-400 hover:text-blue-300 py-1"
                  >
                    View all {expiring.length} expiring domains →
                  </button>
                )}
              </div>
            )}
          </div>
        </Card>
      </div>
    </div>
  );
}
