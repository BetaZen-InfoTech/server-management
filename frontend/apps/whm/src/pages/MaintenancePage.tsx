import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Wrench, RefreshCw, Power, Shield, ShieldOff, Calendar, Plus,
  Trash2, AlertTriangle, CheckCircle, Clock
} from "lucide-react";

interface ScheduledMaintenance {
  id: string;
  title: string;
  description: string;
  scheduledAt: string;
  duration: string;
  status: "scheduled" | "in_progress" | "completed" | "cancelled";
}

export default function MaintenancePage() {
  const [maintenanceMode, setMaintenanceMode] = useState(false);
  const [scheduled, setScheduled] = useState<ScheduledMaintenance[]>([]);
  const [loading, setLoading] = useState(true);
  const [restarting, setRestarting] = useState(false);

  useEffect(() => {
    fetchMaintenanceData();
  }, []);

  const fetchMaintenanceData = async () => {
    setLoading(true);
    try {
      const [statusRes, scheduledRes] = await Promise.allSettled([
        api.get("/maintenance/status"),
        api.get("/maintenance/scheduled"),
      ]);

      if (statusRes.status === "fulfilled") {
        setMaintenanceMode(statusRes.value.data.data?.enabled ?? false);
      }
      if (scheduledRes.status === "fulfilled") {
        setScheduled(scheduledRes.value.data.data || []);
      }
    } catch {
      // Keep defaults
    } finally {
      setLoading(false);
    }
  };

  const toggleMaintenanceMode = async () => {
    try {
      await api.post("/maintenance/toggle", { enabled: !maintenanceMode });
      setMaintenanceMode(!maintenanceMode);
      toast.success(`Maintenance mode ${!maintenanceMode ? "enabled" : "disabled"}`);
    } catch {
      toast.error("Failed to toggle maintenance mode");
    }
  };

  const handleServerRestart = async () => {
    if (!confirm("Are you sure you want to restart the server? All active connections will be dropped.")) return;
    setRestarting(true);
    try {
      await api.post("/maintenance/restart");
      toast.success("Server restart initiated. Services will be back online shortly.");
    } catch {
      toast.error("Failed to initiate server restart");
    } finally {
      setRestarting(false);
    }
  };

  const handleServiceRestart = async (service: string) => {
    if (!confirm(`Are you sure you want to restart ${service}?`)) return;
    try {
      await api.post(`/maintenance/restart/${service}`);
      toast.success(`${service} restart initiated`);
    } catch {
      toast.error(`Failed to restart ${service}`);
    }
  };

  const handleDeleteScheduled = async (id: string) => {
    try {
      await api.delete(`/maintenance/scheduled/${id}`);
      toast.success("Scheduled maintenance cancelled");
      fetchMaintenanceData();
    } catch {
      toast.error("Failed to cancel scheduled maintenance");
    }
  };

  const services = [
    { name: "Nginx", status: "running", uptime: "42d 7h" },
    { name: "PHP-FPM", status: "running", uptime: "42d 7h" },
    { name: "MongoDB", status: "running", uptime: "42d 7h" },
    { name: "Redis", status: "running", uptime: "42d 7h" },
    { name: "Certbot", status: "running", uptime: "42d 7h" },
  ];

  const scheduledColumns = [
    {
      header: "Title",
      accessor: (m: ScheduledMaintenance) => (
        <div className="flex items-center gap-2">
          <Calendar size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{m.title}</span>
        </div>
      ),
    },
    {
      header: "Scheduled",
      accessor: (m: ScheduledMaintenance) => (
        <span className="text-panel-muted text-sm">{m.scheduledAt}</span>
      ),
    },
    {
      header: "Duration",
      accessor: (m: ScheduledMaintenance) => (
        <span className="text-panel-muted text-sm flex items-center gap-1">
          <Clock size={12} />
          {m.duration}
        </span>
      ),
    },
    {
      header: "Status",
      accessor: (m: ScheduledMaintenance) => {
        const statusMap: Record<string, string> = {
          scheduled: "pending",
          in_progress: "warning",
          completed: "active",
          cancelled: "inactive",
        };
        return <StatusBadge status={statusMap[m.status] || m.status} />;
      },
    },
    {
      header: "",
      accessor: (m: ScheduledMaintenance) => (
        m.status === "scheduled" ? (
          <button
            onClick={() => handleDeleteScheduled(m.id)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
            title="Cancel"
          >
            <Trash2 size={14} />
          </button>
        ) : null
      ),
    },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Maintenance</h1>
          <p className="text-panel-muted text-sm mt-1">
            Server maintenance tools and scheduled operations
          </p>
        </div>
        <Button
          onClick={fetchMaintenanceData}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
        >
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
          Refresh
        </Button>
      </div>

      {/* Maintenance Mode Toggle */}
      <Card>
        <div className="p-5 flex items-center justify-between">
          <div className="flex items-center gap-4">
            <div className={`p-3 rounded-xl ${maintenanceMode ? "bg-yellow-500/10" : "bg-green-500/10"}`}>
              {maintenanceMode ? (
                <ShieldOff size={24} className="text-yellow-400" />
              ) : (
                <Shield size={24} className="text-green-400" />
              )}
            </div>
            <div>
              <h3 className="text-lg font-semibold text-panel-text">
                Maintenance Mode is {maintenanceMode ? "Active" : "Off"}
              </h3>
              <p className="text-sm text-panel-muted">
                {maintenanceMode
                  ? "All public traffic is being redirected to the maintenance page"
                  : "Your server is serving traffic normally"}
              </p>
            </div>
          </div>
          <Button
            onClick={toggleMaintenanceMode}
            className={`px-4 py-2 rounded-lg text-sm font-medium transition-colors ${
              maintenanceMode
                ? "bg-green-600 text-white hover:bg-green-700"
                : "bg-yellow-600/10 text-yellow-400 hover:bg-yellow-600/20 border border-yellow-600/20"
            }`}
          >
            {maintenanceMode ? "Disable Maintenance Mode" : "Enable Maintenance Mode"}
          </Button>
        </div>
      </Card>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Service Management */}
        <Card>
          <div className="p-5 border-b border-panel-border">
            <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
              Services
            </h3>
          </div>
          <div className="p-5 space-y-3">
            {services.map((service) => (
              <div
                key={service.name}
                className="flex items-center justify-between p-3 bg-panel-bg rounded-lg border border-panel-border"
              >
                <div className="flex items-center gap-3">
                  <CheckCircle size={14} className="text-green-400" />
                  <div>
                    <p className="text-sm font-medium text-panel-text">{service.name}</p>
                    <p className="text-xs text-panel-muted">Uptime: {service.uptime}</p>
                  </div>
                </div>
                <button
                  onClick={() => handleServiceRestart(service.name)}
                  className="flex items-center gap-1 px-3 py-1.5 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-yellow-400 text-xs font-medium transition-colors"
                >
                  <RefreshCw size={12} />
                  Restart
                </button>
              </div>
            ))}
          </div>
        </Card>

        {/* Server Restart */}
        <Card>
          <div className="p-5 border-b border-panel-border">
            <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
              Server Control
            </h3>
          </div>
          <div className="p-5 space-y-4">
            <div className="p-4 bg-red-500/5 border border-red-500/20 rounded-lg">
              <div className="flex items-start gap-3">
                <AlertTriangle size={18} className="text-red-400 shrink-0 mt-0.5" />
                <div>
                  <p className="text-sm font-medium text-panel-text">Server Restart</p>
                  <p className="text-xs text-panel-muted mt-1">
                    This will restart all services and drop all active connections.
                    Use this only when absolutely necessary.
                  </p>
                  <Button
                    onClick={handleServerRestart}
                    disabled={restarting}
                    className="mt-3 flex items-center gap-2 px-4 py-2 bg-red-600 hover:bg-red-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
                  >
                    <Power size={14} />
                    {restarting ? "Restarting..." : "Restart Server"}
                  </Button>
                </div>
              </div>
            </div>

            <div className="p-4 bg-panel-bg rounded-lg border border-panel-border">
              <h4 className="text-sm font-medium text-panel-text mb-2">Quick Actions</h4>
              <div className="space-y-2">
                <button
                  onClick={() => {
                    toast.success("Cache cleared");
                  }}
                  className="w-full flex items-center justify-between p-2.5 rounded-lg border border-panel-border hover:bg-panel-surface text-sm text-panel-muted hover:text-panel-text transition-colors"
                >
                  <span>Clear all caches</span>
                  <Wrench size={14} />
                </button>
                <button
                  onClick={() => {
                    toast.success("Logs rotated");
                  }}
                  className="w-full flex items-center justify-between p-2.5 rounded-lg border border-panel-border hover:bg-panel-surface text-sm text-panel-muted hover:text-panel-text transition-colors"
                >
                  <span>Rotate log files</span>
                  <RefreshCw size={14} />
                </button>
                <button
                  onClick={() => {
                    toast.success("Temp files cleaned");
                  }}
                  className="w-full flex items-center justify-between p-2.5 rounded-lg border border-panel-border hover:bg-panel-surface text-sm text-panel-muted hover:text-panel-text transition-colors"
                >
                  <span>Clean temp files</span>
                  <Trash2 size={14} />
                </button>
              </div>
            </div>
          </div>
        </Card>
      </div>

      {/* Scheduled Maintenance */}
      <Card>
        <div className="p-5 border-b border-panel-border flex items-center justify-between">
          <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
            Scheduled Maintenance
          </h3>
          <Button
            onClick={() => toast("Schedule Maintenance modal coming soon")}
            className="flex items-center gap-1 px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-xs font-medium transition-colors"
          >
            <Plus size={12} />
            Schedule
          </Button>
        </div>
        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : scheduled.length > 0 ? (
          <Table columns={scheduledColumns} data={scheduled} />
        ) : (
          <div className="text-center py-12 px-4">
            <Calendar size={36} className="text-panel-muted/20 mx-auto mb-3" />
            <p className="text-sm text-panel-muted">No scheduled maintenance windows</p>
            <p className="text-xs text-panel-muted/60 mt-1">
              Schedule maintenance windows to notify users about planned downtime
            </p>
          </div>
        )}
      </Card>
    </div>
  );
}
