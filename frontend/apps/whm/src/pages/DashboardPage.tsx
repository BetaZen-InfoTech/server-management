import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { Card, Button, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import { useAuthStore } from "@/store/auth";
import toast from "react-hot-toast";
import {
  Globe, AppWindow, Database, ShieldCheck, Activity,
  ArrowUpRight, Server, HardDrive, Cpu, MemoryStick,
  RefreshCw, Plus
} from "lucide-react";

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

  useEffect(() => {
    fetchDashboardData();
  }, []);

  const fetchDashboardData = async () => {
    setLoading(true);
    try {
      const [statsRes, activityRes] = await Promise.allSettled([
        api.get("/dashboard/stats"),
        api.get("/dashboard/activity"),
      ]);

      if (statsRes.status === "fulfilled") {
        setStats(statsRes.value.data);
      }
      if (activityRes.status === "fulfilled") {
        setRecentActivity(activityRes.value.data);
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
                  <span className="text-sm font-medium text-panel-text">24%</span>
                </div>
                <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden">
                  <div className="h-full bg-blue-500 rounded-full" style={{ width: "24%" }} />
                </div>
              </div>
              <div>
                <div className="flex items-center justify-between mb-1">
                  <span className="text-sm text-panel-muted flex items-center gap-2">
                    <MemoryStick size={14} /> Memory
                  </span>
                  <span className="text-sm font-medium text-panel-text">58%</span>
                </div>
                <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden">
                  <div className="h-full bg-green-500 rounded-full" style={{ width: "58%" }} />
                </div>
              </div>
              <div>
                <div className="flex items-center justify-between mb-1">
                  <span className="text-sm text-panel-muted flex items-center gap-2">
                    <HardDrive size={14} /> Disk
                  </span>
                  <span className="text-sm font-medium text-panel-text">41%</span>
                </div>
                <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden">
                  <div className="h-full bg-yellow-500 rounded-full" style={{ width: "41%" }} />
                </div>
              </div>
              <div className="pt-2 border-t border-panel-border">
                <div className="flex items-center justify-between text-sm">
                  <span className="text-panel-muted flex items-center gap-2">
                    <Server size={14} /> Uptime
                  </span>
                  <span className="text-panel-text font-medium">42 days, 7h</span>
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
      </div>
    </div>
  );
}
