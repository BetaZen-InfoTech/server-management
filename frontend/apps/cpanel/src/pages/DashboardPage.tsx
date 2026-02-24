import React, { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Card, Button } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Globe,
  Rocket,
  Database,
  HardDrive,
  Mail,
  ShieldCheck,
  Plus,
  ArrowRight,
  Activity,
  Clock,
} from "lucide-react";

interface DashboardStats {
  domains: number;
  apps: number;
  databases: number;
  storageUsed: string;
  storageTotal: string;
  emailAccounts: number;
  sslCerts: number;
}

interface RecentActivity {
  id: string;
  action: string;
  resource: string;
  timestamp: string;
}

export default function DashboardPage() {
  const navigate = useNavigate();
  const [stats, setStats] = useState<DashboardStats>({
    domains: 0,
    apps: 0,
    databases: 0,
    storageUsed: "0 GB",
    storageTotal: "50 GB",
    emailAccounts: 0,
    sslCerts: 0,
  });
  const [activities, setActivities] = useState<RecentActivity[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const fetchDashboard = async () => {
      try {
        const [statsRes, activityRes] = await Promise.allSettled([
          api.get("/dashboard/stats"),
          api.get("/dashboard/activity"),
        ]);
        if (statsRes.status === "fulfilled") setStats(statsRes.value.data.data);
        if (activityRes.status === "fulfilled") setActivities(activityRes.value.data.data || []);
      } catch {
        toast.error("Failed to load dashboard data");
      } finally {
        setLoading(false);
      }
    };
    fetchDashboard();
  }, []);

  const statCards = [
    {
      label: "My Domains",
      value: stats.domains,
      icon: <Globe size={20} className="text-blue-400" />,
      path: "/domains",
      color: "bg-blue-500/10",
    },
    {
      label: "Active Apps",
      value: stats.apps,
      icon: <Rocket size={20} className="text-purple-400" />,
      path: "/apps",
      color: "bg-purple-500/10",
    },
    {
      label: "Databases",
      value: stats.databases,
      icon: <Database size={20} className="text-green-400" />,
      path: "/databases",
      color: "bg-green-500/10",
    },
    {
      label: "Storage Used",
      value: stats.storageUsed,
      icon: <HardDrive size={20} className="text-orange-400" />,
      path: "/files",
      color: "bg-orange-500/10",
      sub: `of ${stats.storageTotal}`,
    },
    {
      label: "Email Accounts",
      value: stats.emailAccounts,
      icon: <Mail size={20} className="text-cyan-400" />,
      path: "/email",
      color: "bg-cyan-500/10",
    },
    {
      label: "SSL Certificates",
      value: stats.sslCerts,
      icon: <ShieldCheck size={20} className="text-emerald-400" />,
      path: "/ssl",
      color: "bg-emerald-500/10",
    },
  ];

  const quickActions = [
    { label: "Add Domain", icon: <Globe size={16} />, path: "/domains" },
    { label: "Deploy App", icon: <Rocket size={16} />, path: "/apps" },
    { label: "Create Database", icon: <Database size={16} />, path: "/databases" },
    { label: "Create Mailbox", icon: <Mail size={16} />, path: "/email" },
    { label: "Request SSL", icon: <ShieldCheck size={16} />, path: "/ssl" },
    { label: "Create Backup", icon: <HardDrive size={16} />, path: "/backups" },
  ];

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-brand-400" />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Stats Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {statCards.map((card) => (
          <button
            key={card.label}
            onClick={() => navigate(card.path)}
            className="bg-panel-surface border border-panel-border rounded-xl p-5 flex items-start gap-4 hover:border-brand-500/30 transition-colors text-left"
          >
            <div className={`p-3 rounded-lg ${card.color}`}>{card.icon}</div>
            <div>
              <p className="text-sm text-panel-muted">{card.label}</p>
              <p className="text-2xl font-bold text-white mt-1">{card.value}</p>
              {card.sub && (
                <p className="text-xs text-panel-muted mt-0.5">{card.sub}</p>
              )}
            </div>
          </button>
        ))}
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Quick Actions */}
        <Card title="Quick Actions" description="Common tasks at your fingertips">
          <div className="grid grid-cols-2 gap-3">
            {quickActions.map((action) => (
              <Button
                key={action.label}
                variant="secondary"
                size="sm"
                onClick={() => navigate(action.path)}
                className="justify-start gap-2"
              >
                {action.icon}
                {action.label}
              </Button>
            ))}
          </div>
        </Card>

        {/* Recent Activity */}
        <Card title="Recent Activity" description="Latest actions on your account">
          {activities.length === 0 ? (
            <div className="text-center py-8">
              <Activity size={32} className="mx-auto text-panel-muted mb-3" />
              <p className="text-panel-muted text-sm">No recent activity</p>
            </div>
          ) : (
            <div className="space-y-3">
              {activities.slice(0, 5).map((activity) => (
                <div
                  key={activity.id}
                  className="flex items-start gap-3 text-sm"
                >
                  <Clock
                    size={14}
                    className="text-panel-muted mt-0.5 flex-shrink-0"
                  />
                  <div className="flex-1">
                    <p className="text-panel-text">
                      <span className="font-medium">{activity.action}</span>{" "}
                      {activity.resource}
                    </p>
                    <p className="text-xs text-panel-muted">
                      {activity.timestamp}
                    </p>
                  </div>
                </div>
              ))}
            </div>
          )}
          {activities.length > 0 && (
            <div className="mt-4 pt-3 border-t border-panel-border">
              <button className="text-sm text-brand-400 hover:text-brand-300 flex items-center gap-1">
                View all activity <ArrowRight size={14} />
              </button>
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
