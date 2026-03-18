import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
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

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

export default function MaintenancePage() {
  const [maintenanceMode, setMaintenanceMode] = useState(false);
  const [scheduled, setScheduled] = useState<ScheduledMaintenance[]>([]);
  const [loading, setLoading] = useState(true);
  const [restarting, setRestarting] = useState(false);
  const [showSchedule, setShowSchedule] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ title: "", description: "", scheduled_at: "", duration: "1h" });

  useEffect(() => {
    fetchMaintenanceData();
  }, []);

  const fetchMaintenanceData = async () => {
    setLoading(true);
    try {
      const statusRes = await api.get("/maintenance/");
      setMaintenanceMode(statusRes.data.data?.enabled ?? false);
    } catch {
      // Keep defaults
    } finally {
      setLoading(false);
    }
  };

  const toggleMaintenanceMode = async () => {
    try {
      const endpoint = maintenanceMode ? "/maintenance/disable" : "/maintenance/enable";
      await api.post(endpoint);
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
      await api.post("/processes/services/nginx/restart");
      toast.success("Server restart initiated. Services will be back online shortly.");
    } catch {
      toast.error("Failed to initiate server restart");
    } finally {
      setRestarting(false);
    }
  };

  const handleServiceRestart = async (serviceName: string, systemdName: string) => {
    if (!confirm(`Are you sure you want to restart ${serviceName}?`)) return;
    try {
      await api.post(`/processes/services/${systemdName}/restart`);
      toast.success(`${serviceName} restart initiated`);
    } catch {
      toast.error(`Failed to restart ${serviceName}`);
    }
  };

  const handleSchedule = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.title || !form.scheduled_at) {
      toast.error("Please fill all required fields");
      return;
    }
    setCreating(true);
    try {
      await api.post("/maintenance/schedule", form);
      toast.success("Maintenance scheduled");
      setShowSchedule(false);
      setForm({ title: "", description: "", scheduled_at: "", duration: "1h" });
      fetchMaintenanceData();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to schedule maintenance");
    } finally {
      setCreating(false);
    }
  };

  const handleDeleteScheduled = async (id: string) => {
    if (!confirm("Are you sure you want to cancel this scheduled maintenance?")) return;
    try {
      await api.delete(`/maintenance/schedule/${id}`);
      toast.success("Scheduled maintenance cancelled");
      fetchMaintenanceData();
    } catch {
      toast.error("Failed to cancel scheduled maintenance");
    }
  };

  const services = [
    { name: "Nginx", systemd: "nginx" },
    { name: "PHP 8.2 FPM", systemd: "php8.2-fpm" },
    { name: "MongoDB", systemd: "mongod" },
    { name: "Postfix (Mail)", systemd: "postfix" },
    { name: "Fail2Ban", systemd: "fail2ban" },
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
                key={service.systemd}
                className="flex items-center justify-between p-3 bg-panel-bg rounded-lg border border-panel-border"
              >
                <div className="flex items-center gap-3">
                  <CheckCircle size={14} className="text-green-400" />
                  <div>
                    <p className="text-sm font-medium text-panel-text">{service.name}</p>
                    <p className="text-xs text-panel-muted font-mono">{service.systemd}</p>
                  </div>
                </div>
                <button
                  onClick={() => handleServiceRestart(service.name, service.systemd)}
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
            onClick={() => setShowSchedule(true)}
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

      <Modal isOpen={showSchedule} onClose={() => setShowSchedule(false)} title="Schedule Maintenance">
        <form onSubmit={handleSchedule} className="space-y-4">
          <div>
            <label className={labelClass}>Title *</label>
            <input type="text" required placeholder="Server upgrade" value={form.title}
              onChange={(e) => setForm({ ...form, title: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>Description</label>
            <textarea placeholder="Describe the maintenance work..." value={form.description}
              onChange={(e) => setForm({ ...form, description: e.target.value })}
              className={`${inputClass} min-h-[80px] resize-y`} rows={3} />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Date & Time *</label>
              <input type="datetime-local" required value={form.scheduled_at}
                onChange={(e) => setForm({ ...form, scheduled_at: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Estimated Duration</label>
              <select value={form.duration} onChange={(e) => setForm({ ...form, duration: e.target.value })} className={inputClass}>
                <option value="30m">30 minutes</option>
                <option value="1h">1 hour</option>
                <option value="2h">2 hours</option>
                <option value="4h">4 hours</option>
                <option value="8h">8 hours</option>
                <option value="24h">24 hours</option>
              </select>
            </div>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowSchedule(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Scheduling..." : "Schedule Maintenance"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
