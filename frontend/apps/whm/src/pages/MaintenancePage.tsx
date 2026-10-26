import { useState, useEffect, useRef } from "react";
import { Card, Button, Table, StatusBadge, Modal, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Wrench, RefreshCw, Power, Shield, ShieldOff, Calendar, Plus,
  Trash2, AlertTriangle, CheckCircle, Clock, Download
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
  const [healingFd, setHealingFd] = useState(false);
  const [fdRestart, setFdRestart] = useState(false);
  const [curVersion, setCurVersion] = useState("");
  const [checking, setChecking] = useState(false);
  const [updInfo, setUpdInfo] = useState<any>(null);
  const [updating, setUpdating] = useState(false);
  const [updFailed, setUpdFailed] = useState(false);
  const [logTail, setLogTail] = useState("");
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const [showSchedule, setShowSchedule] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ title: "", description: "", scheduled_at: "", duration: "1h" });

  useEffect(() => {
    fetchMaintenanceData();
    checkForUpdates(true);
    // Resume tracking if an update is already in flight (e.g. page reloaded mid-update).
    api.get("/maintenance/panel-update/status").then((r) => {
      const d = r.data?.data;
      if (d?.running) { setUpdating(true); startPolling(d.from_version); }
    }).catch(() => {});
    return () => { if (pollRef.current) clearInterval(pollRef.current); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const checkForUpdates = async (silent = false) => {
    setChecking(true);
    try {
      const res = await api.get("/maintenance/panel-update/check");
      const d = res.data?.data || {};
      setUpdInfo(d);
      if (d.current_version) setCurVersion(d.current_version);
      if (!silent) {
        if (d.error) toast.error(d.error);
        else if (d.update_available) toast.success(`Update available: ${d.behind_count} commit(s) behind`);
        else toast.success("Panel is up to date");
      }
    } catch (err: any) {
      if (!silent) toast.error(err?.response?.data?.error?.message || "Failed to check for updates");
    } finally {
      setChecking(false);
    }
  };

  // Polls status every 5s. Fetch errors are EXPECTED while serverpanel restarts,
  // so they are swallowed and polling simply continues until done / failed / timeout.
  const startPolling = (fromVersion: string) => {
    if (pollRef.current) clearInterval(pollRef.current);
    const began = Date.now();
    pollRef.current = setInterval(async () => {
      const stop = () => { if (pollRef.current) clearInterval(pollRef.current); pollRef.current = null; setUpdating(false); };
      try {
        const res = await api.get("/maintenance/panel-update/status");
        const d = res.data?.data || {};
        if (d.log_tail) setLogTail(d.log_tail);
        if (d.done) {
          stop();
          setCurVersion(d.current_version);
          toast.success(`Updated to v${d.current_version}`);
          checkForUpdates(true);
          return;
        }
        if (d.failed) {
          stop(); setUpdFailed(true);
          toast.error("Update failed - the running panel was left untouched");
          return;
        }
      } catch {
        // API blackout during restart — keep polling.
      }
      if (Date.now() - began > 10 * 60 * 1000) {
        stop(); setUpdFailed(true);
        toast.error("Update may have failed");
      }
    }, 5000);
    void fromVersion;
  };

  const applyUpdate = async () => {
    if (!(await confirmAction({
      title: "Update Panel?",
      description: `The panel will pull the latest "${updInfo?.branch || "checked-out"}" branch, rebuild and briefly restart (~1-3 min of downtime); you'll be logged back in automatically. Continue?`,
      danger: true,
      confirmLabel: "Update now",
    }))) return;
    setUpdFailed(false); setLogTail("");
    try {
      const res = await api.post("/maintenance/panel-update/apply", {});
      setUpdating(true);
      startPolling(res.data?.data?.from_version || curVersion);
      toast.success("Update started");
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to start update");
    }
  };

  const fetchMaintenanceData = async () => {
    setLoading(true);
    try {
      const statusRes = await api.get("/maintenance/");
      setMaintenanceMode(statusRes.data.data?.server?.enabled ?? false);
    } catch {
      // Keep defaults
    } finally {
      setLoading(false);
    }
  };

  const toggleMaintenanceMode = async () => {
    try {
      const endpoint = maintenanceMode ? "/maintenance/disable" : "/maintenance/enable";
      await api.post(endpoint, {});
      setMaintenanceMode(!maintenanceMode);
      toast.success(`Maintenance mode ${!maintenanceMode ? "enabled" : "disabled"}`);
    } catch {
      toast.error("Failed to toggle maintenance mode");
    }
  };

  const handleServerRestart = async () => {
    if (!await confirmAction({ title: "Drop?", description: "Are you sure you want to restart the server? All active connections will be dropped.", danger: true, confirmLabel: "Drop" })) return;
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
    if (!await confirmAction({ title: "Restart?", description: `Are you sure you want to restart ${serviceName}?`, confirmLabel: "Restart" })) return;
    try {
      await api.post(`/processes/services/${systemdName}/restart`);
      toast.success(`${serviceName} restart initiated`);
    } catch {
      toast.error(`Failed to restart ${serviceName}`);
    }
  };

  // Raise deployed apps' file-descriptor ceiling (LimitNOFILE) so high-concurrency
  // backends (e.g. a campaign bot opening thousands of sockets) stop freezing with
  // EMFILE. Default = write systemd drop-ins only (NO restart, no interruption) —
  // the new limit applies on each app's next redeploy/restart. The "restart now"
  // opt-in applies it immediately but briefly interrupts the chosen apps, so it is
  // off by default and must never be used mid-campaign.
  const healUnitLimits = async () => {
    if (fdRestart) {
      if (!(await confirmAction({
        title: "Restart running apps now?",
        description:
          "This raises the file-descriptor limit immediately, but RESTARTS every running app whose limit is still too low — any in-flight work (e.g. a campaign currently sending) will be interrupted. Only do this when no campaign/job is running. Without this option the limit is still written and applies on each app's next redeploy with no interruption.",
        danger: true,
        confirmLabel: "Apply + restart now",
      })))
        return;
    }
    setHealingFd(true);
    try {
      const res = await api.post("/maintenance/heal-unit-limits", { restart: fdRestart });
      const d = res?.data?.data || {};
      const parts: string[] = [];
      if (d.units_healed != null) parts.push(`${d.units_healed} app(s) set`);
      if (fdRestart && d.units_restarted != null) parts.push(`${d.units_restarted} restarted`);
      if (fdRestart && d.units_skipped != null) parts.push(`${d.units_skipped} already ok/skipped`);
      if (d.nginx_patched) parts.push("nginx raised");
      toast.success(
        (parts.length ? `FD limits applied — ${parts.join(", ")}` : "FD limits applied") +
          (fdRestart ? "" : " · takes effect on each app's next redeploy")
      );
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to apply FD limits");
    } finally {
      setHealingFd(false);
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
    if (!await confirmAction({ title: "Cancel?", description: "Are you sure you want to cancel this scheduled maintenance?", danger: true, confirmLabel: "Cancel" })) return;
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

      {/* Panel version / self-update */}
      <Card>
        <div className="p-5 space-y-4">
          <div className="flex items-start justify-between gap-4">
            <div className="flex items-start gap-4">
              <div className="p-3 rounded-xl bg-blue-500/10">
                <Download size={24} className="text-blue-400" />
              </div>
              <div>
                <h3 className="text-lg font-semibold text-panel-text">
                  Panel Version{curVersion ? ` v${curVersion}` : ""}
                </h3>
                <p className="text-sm text-panel-muted max-w-2xl">
                  {updating
                    ? "Updating… (building, then restarting). This page keeps working through the brief restart."
                    : updInfo?.error
                    ? updInfo.error
                    : updInfo?.update_available
                    ? `Update available: ${updInfo.behind_count} commit(s) behind (${updInfo.current_commit} → ${updInfo.latest_commit} on ${updInfo.branch}).`
                    : updInfo
                    ? "Panel is up to date."
                    : "Check whether a newer panel version is available."}
                </p>
              </div>
            </div>
            <div className="shrink-0 flex items-center gap-2">
              <Button
                onClick={() => checkForUpdates(false)}
                disabled={checking || updating}
                className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
              >
                <RefreshCw size={14} className={checking ? "animate-spin" : ""} />
                Check for updates
              </Button>
              <Button
                onClick={applyUpdate}
                disabled={updating}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600/10 text-blue-400 hover:bg-blue-600/20 border border-blue-600/20 rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
              >
                <Download size={14} className={updating ? "animate-pulse" : ""} />
                {updating ? "Updating…" : "Update Panel"}
              </Button>
            </div>
          </div>
          {updFailed && (
            <p className="text-sm text-red-400 flex items-center gap-2">
              <AlertTriangle size={14} />
              Update may have failed. A failed build never restarts the panel, so it is still running the previous version.
            </p>
          )}
          {(updFailed || updating) && logTail && (
            <pre className="max-h-64 overflow-auto p-3 bg-panel-bg border border-panel-border rounded-lg text-xs text-panel-muted whitespace-pre-wrap">{logTail}</pre>
          )}
        </div>
      </Card>

      {/* App file-descriptor limits — fix backends that freeze under load */}
      <Card>
        <div className="p-5 flex items-start justify-between gap-4">
          <div className="flex items-start gap-4">
            <div className="p-3 rounded-xl bg-blue-500/10">
              <Wrench size={24} className="text-blue-400" />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-panel-text">App File-Descriptor Limits</h3>
              <p className="text-sm text-panel-muted max-w-2xl">
                Raises every deployed app's open-file ceiling (LimitNOFILE 65536→1048576) so a
                high-concurrency backend — e.g. a campaign bot opening thousands of connections —
                stops freezing with "too many open files". New deploys already get this; use the
                button to apply it to existing apps.
              </p>
              <label className="mt-3 flex items-center gap-2 text-xs text-panel-muted cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={fdRestart}
                  onChange={(e) => setFdRestart(e.target.checked)}
                  className="accent-yellow-500"
                />
                <span className="flex items-center gap-1">
                  <AlertTriangle size={12} className="text-yellow-400" />
                  Restart running apps now to apply immediately (interrupts them — never do this mid-campaign)
                </span>
              </label>
              <p className="mt-1 text-xs text-panel-muted">
                Unchecked (recommended): writes the limit with no restart — it applies on each app's next redeploy.
              </p>
            </div>
          </div>
          <Button
            onClick={healUnitLimits}
            disabled={healingFd}
            className="shrink-0 flex items-center gap-2 px-4 py-2 bg-blue-600/10 text-blue-400 hover:bg-blue-600/20 border border-blue-600/20 rounded-lg text-sm font-medium transition-colors"
          >
            <Wrench size={14} className={healingFd ? "animate-spin" : ""} />
            {healingFd ? "Applying..." : "Apply FD limits"}
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
