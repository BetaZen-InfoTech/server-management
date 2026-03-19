import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Clock, Plus, RefreshCw, Search, Trash2, Edit, Play, Pause } from "lucide-react";

interface CronJob {
  id: string;
  schedule: string;
  command: string;
  description: string;
  domain: string;
  user: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

const presets = [
  { label: "Every minute", value: "* * * * *" },
  { label: "Every 5 minutes", value: "*/5 * * * *" },
  { label: "Every hour", value: "0 * * * *" },
  { label: "Every day at midnight", value: "0 0 * * *" },
  { label: "Every Sunday", value: "0 0 * * 0" },
  { label: "Every month", value: "0 0 1 * *" },
];

export default function CronPage() {
  const [jobs, setJobs] = useState<CronJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [showEdit, setShowEdit] = useState(false);
  const [creating, setCreating] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editingJob, setEditingJob] = useState<CronJob | null>(null);
  const [form, setForm] = useState({ schedule: "", command: "", description: "" });
  const [editForm, setEditForm] = useState({ schedule: "", command: "", description: "" });

  useEffect(() => {
    fetchJobs();
  }, []);

  const fetchJobs = async () => {
    setLoading(true);
    try {
      const res = await api.get("/cron");
      setJobs(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.schedule || !form.command) {
      toast.error("Please fill all required fields");
      return;
    }
    setCreating(true);
    try {
      await api.post("/cron", form);
      toast.success("Cron job created");
      setShowCreate(false);
      setForm({ schedule: "", command: "", description: "" });
      fetchJobs();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create cron job");
    } finally {
      setCreating(false);
    }
  };

  const handleToggle = async (id: string, enabled: boolean) => {
    try {
      await api.patch(`/cron/${id}/toggle`);
      const action = enabled ? "paused" : "resumed";
      toast.success(`Cron job ${action}`);
      fetchJobs();
    } catch {
      toast.error("Failed to update cron job");
    }
  };

  const startEdit = (job: CronJob) => {
    setEditingJob(job);
    setEditForm({ schedule: job.schedule, command: job.command, description: "" });
    setShowEdit(true);
  };

  const handleEdit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editingJob || !editForm.schedule || !editForm.command) {
      toast.error("Please fill all required fields");
      return;
    }
    setSaving(true);
    try {
      await api.put(`/cron/${editingJob.id}`, editForm);
      toast.success("Cron job updated");
      setShowEdit(false);
      setEditingJob(null);
      fetchJobs();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update cron job");
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("Are you sure you want to delete this cron job?")) return;
    try {
      await api.delete(`/cron/${id}`);
      toast.success("Cron job deleted");
      fetchJobs();
    } catch {
      toast.error("Failed to delete cron job");
    }
  };

  const filtered = jobs.filter(
    (j) =>
      j.command.toLowerCase().includes(search.toLowerCase()) ||
      j.schedule.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Schedule",
      accessor: (j: CronJob) => (
        <code className="text-xs bg-panel-bg px-2 py-1 rounded text-blue-400 font-mono">
          {j.schedule}
        </code>
      ),
    },
    {
      header: "Command",
      accessor: (j: CronJob) => (
        <code className="text-xs text-panel-muted font-mono truncate max-w-[300px] block">
          {j.command}
        </code>
      ),
    },
    {
      header: "Status",
      accessor: (j: CronJob) => <StatusBadge status={j.enabled ? "active" : "inactive"} />,
    },
    {
      header: "Description",
      accessor: (j: CronJob) => (
        <span className="text-panel-muted text-sm">{j.description || "-"}</span>
      ),
    },
    {
      header: "Created",
      accessor: (j: CronJob) => (
        <span className="text-panel-muted text-sm">
          {j.created_at ? new Date(j.created_at).toLocaleDateString("en-US", { month: "short", day: "numeric" }) : "-"}
        </span>
      ),
    },
    {
      header: "Actions",
      accessor: (j: CronJob) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => handleToggle(j.id, j.enabled)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors"
            title={j.enabled ? "Pause" : "Resume"}
          >
            {j.enabled ? <Pause size={14} /> : <Play size={14} />}
          </button>
          <button onClick={() => startEdit(j)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors" title="Edit">
            <Edit size={14} />
          </button>
          <button
            onClick={() => handleDelete(j.id)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
          >
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Cron Jobs</h1>
          <p className="text-panel-muted text-sm mt-1">
            Schedule and manage automated tasks
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchJobs}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => setShowCreate(true)}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Add Cron Job
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search cron jobs..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
        </div>
      </Card>

      <Card>
        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2, 3, 4].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={filtered} />
        ) : (
          <div className="text-center py-16 px-4">
            <Clock size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No cron jobs found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No cron jobs match your search. Try a different search term."
                : "Schedule automated tasks like backups, log rotation, and cleanup jobs."}
            </p>
            {!search && (
              <Button
                onClick={() => setShowCreate(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Add Cron Job
              </Button>
            )}
          </div>
        )}
      </Card>

      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Add Cron Job">
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className={labelClass}>Schedule (Cron Expression) *</label>
            <input type="text" required placeholder="* * * * *" value={form.schedule}
              onChange={(e) => setForm({ ...form, schedule: e.target.value })} className={inputClass} />
            <div className="mt-2 flex flex-wrap gap-1.5">
              {presets.map((p) => (
                <button key={p.value} type="button"
                  onClick={() => setForm({ ...form, schedule: p.value })}
                  className={`px-2 py-1 rounded text-xs transition-colors ${form.schedule === p.value ? "bg-blue-600 text-white" : "bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border"}`}>
                  {p.label}
                </button>
              ))}
            </div>
          </div>
          <div>
            <label className={labelClass}>Command *</label>
            <input type="text" required placeholder="/usr/bin/php /var/www/html/cron.php" value={form.command}
              onChange={(e) => setForm({ ...form, command: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>Description</label>
            <input type="text" placeholder="Optional description" value={form.description}
              onChange={(e) => setForm({ ...form, description: e.target.value })} className={inputClass} />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Creating..." : "Add Cron Job"}
            </button>
          </div>
        </form>
      </Modal>

      <Modal isOpen={showEdit} onClose={() => { setShowEdit(false); setEditingJob(null); }} title="Edit Cron Job">
        <form onSubmit={handleEdit} className="space-y-4">
          <div>
            <label className={labelClass}>Schedule (Cron Expression) *</label>
            <input type="text" required placeholder="* * * * *" value={editForm.schedule}
              onChange={(e) => setEditForm({ ...editForm, schedule: e.target.value })} className={inputClass} />
            <div className="mt-2 flex flex-wrap gap-1.5">
              {presets.map((p) => (
                <button key={p.value} type="button"
                  onClick={() => setEditForm({ ...editForm, schedule: p.value })}
                  className={`px-2 py-1 rounded text-xs transition-colors ${editForm.schedule === p.value ? "bg-blue-600 text-white" : "bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border"}`}>
                  {p.label}
                </button>
              ))}
            </div>
          </div>
          <div>
            <label className={labelClass}>Command *</label>
            <input type="text" required placeholder="/usr/bin/php /var/www/html/cron.php" value={editForm.command}
              onChange={(e) => setEditForm({ ...editForm, command: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>Description</label>
            <input type="text" placeholder="Optional description" value={editForm.description}
              onChange={(e) => setEditForm({ ...editForm, description: e.target.value })} className={inputClass} />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => { setShowEdit(false); setEditingJob(null); }}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={saving}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {saving ? "Saving..." : "Update Cron Job"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
