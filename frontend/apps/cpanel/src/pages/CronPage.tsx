import React, { useEffect, useState } from "react";
import { Card, Button, Table, Modal, StatusBadge, CodeBlock } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Clock, Plus, Trash2, Pencil, Search, Play, Pause } from "lucide-react";

interface CronJob {
  id: string;
  command: string;
  schedule: string;
  description: string;
  domain: string;
  user: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

const presetSchedules = [
  { label: "Every minute", value: "* * * * *" },
  { label: "Every 5 minutes", value: "*/5 * * * *" },
  { label: "Every 15 minutes", value: "*/15 * * * *" },
  { label: "Every hour", value: "0 * * * *" },
  { label: "Every 6 hours", value: "0 */6 * * *" },
  { label: "Daily at midnight", value: "0 0 * * *" },
  { label: "Weekly (Sunday)", value: "0 0 * * 0" },
  { label: "Monthly (1st)", value: "0 0 1 * *" },
  { label: "Custom", value: "custom" },
];

export default function CronPage() {
  const [jobs, setJobs] = useState<CronJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [showAdd, setShowAdd] = useState(false);
  const [showEdit, setShowEdit] = useState(false);
  const [search, setSearch] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [editJob, setEditJob] = useState<CronJob | null>(null);
  const [selectedPreset, setSelectedPreset] = useState("0 0 * * *");
  const [form, setForm] = useState({
    command: "",
    schedule: "0 0 * * *",
    description: "",
  });

  const fetchJobs = async () => {
    try {
      const res = await api.get("/cron");
      setJobs(res.data.data || []);
    } catch {
      toast.error("Failed to load cron jobs");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchJobs();
  }, []);

  const handleAdd = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.command.trim() || !form.schedule.trim()) {
      toast.error("Command and schedule are required");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/cron", form);
      toast.success("Cron job added");
      setShowAdd(false);
      setForm({ command: "", schedule: "0 0 * * *", description: "" });
      fetchJobs();
    } catch (err: any) {
      toast.error(err.response?.data?.error?.message || "Failed to add cron job");
    } finally {
      setSubmitting(false);
    }
  };

  const handleEdit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editJob) return;
    setSubmitting(true);
    try {
      await api.put(`/cron/${editJob.id}`, form);
      toast.success("Cron job updated");
      setShowEdit(false);
      setEditJob(null);
      fetchJobs();
    } catch (err: any) {
      toast.error(err.response?.data?.error?.message || "Failed to update cron job");
    } finally {
      setSubmitting(false);
    }
  };

  const handleToggle = async (id: string, enabled: boolean) => {
    try {
      await api.patch(`/cron/${id}/toggle`);
      const action = enabled ? "paused" : "resumed";
      toast.success(`Cron job ${action}`);
      fetchJobs();
    } catch {
      toast.error("Failed to toggle cron job");
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this cron job?")) return;
    try {
      await api.delete(`/cron/${id}`);
      toast.success("Cron job deleted");
      setJobs((prev) => prev.filter((j) => j.id !== id));
    } catch {
      toast.error("Failed to delete cron job");
    }
  };

  const openEdit = (job: CronJob) => {
    setEditJob(job);
    setForm({
      command: job.command,
      schedule: job.schedule,
      description: job.description || "",
    });
    const matchingPreset = presetSchedules.find(
      (p) => p.value === job.schedule
    );
    setSelectedPreset(matchingPreset ? job.schedule : "custom");
    setShowEdit(true);
  };

  const handlePresetChange = (preset: string) => {
    setSelectedPreset(preset);
    if (preset !== "custom") {
      setForm({ ...form, schedule: preset });
    }
  };

  const filtered = jobs.filter(
    (j) =>
      j.command.toLowerCase().includes(search.toLowerCase()) ||
      (j.description || "").toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Job",
      accessor: (item: CronJob) => (
        <div>
          <p className="font-medium text-white">
            {item.description || "Unnamed job"}
          </p>
          <code className="text-xs font-mono text-panel-muted">
            {item.command}
          </code>
        </div>
      ),
    },
    {
      header: "Schedule",
      accessor: (item: CronJob) => (
        <code className="text-xs font-mono text-brand-400 bg-panel-bg px-2 py-0.5 rounded">
          {item.schedule}
        </code>
      ),
    },
    {
      header: "Status",
      accessor: (item: CronJob) => (
        <StatusBadge status={item.enabled ? "active" : "inactive"} />
      ),
    },
    {
      header: "Created",
      accessor: (item: CronJob) => (
        <span className="text-panel-muted text-sm">
          {item.created_at
            ? new Date(item.created_at).toLocaleDateString("en-US", { month: "short", day: "numeric" })
            : "-"}
        </span>
      ),
    },
    {
      header: "Actions",
      accessor: (item: CronJob) => (
        <div className="flex items-center gap-2 justify-end">
          <button
            onClick={() => handleToggle(item.id, item.enabled)}
            className="text-panel-muted hover:text-brand-400 transition-colors"
            title={item.enabled ? "Pause" : "Resume"}
          >
            {item.enabled ? (
              <Pause size={16} />
            ) : (
              <Play size={16} />
            )}
          </button>
          <button
            onClick={() => openEdit(item)}
            className="text-panel-muted hover:text-brand-400 transition-colors"
            title="Edit"
          >
            <Pencil size={16} />
          </button>
          <button
            onClick={() => handleDelete(item.id)}
            className="text-panel-muted hover:text-red-400 transition-colors"
            title="Delete"
          >
            <Trash2 size={16} />
          </button>
        </div>
      ),
    },
  ];

  const cronForm = (onSubmit: (e: React.FormEvent) => void) => (
    <form onSubmit={onSubmit} className="space-y-4">
      <div>
        <label className="block text-sm font-medium text-panel-text mb-1.5">
          Description
        </label>
        <input
          type="text"
          value={form.description}
          onChange={(e) => setForm({ ...form, description: e.target.value })}
          placeholder="Daily database backup"
          className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
        />
      </div>
      <div>
        <label className="block text-sm font-medium text-panel-text mb-1.5">
          Command
        </label>
        <input
          type="text"
          value={form.command}
          onChange={(e) => setForm({ ...form, command: e.target.value })}
          placeholder="/usr/bin/php /home/user/public_html/cron.php"
          className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500 font-mono"
        />
      </div>
      <div>
        <label className="block text-sm font-medium text-panel-text mb-1.5">
          Schedule Preset
        </label>
        <select
          value={selectedPreset}
          onChange={(e) => handlePresetChange(e.target.value)}
          className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500"
        >
          {presetSchedules.map((p) => (
            <option key={p.value} value={p.value}>
              {p.label} {p.value !== "custom" ? `(${p.value})` : ""}
            </option>
          ))}
        </select>
      </div>
      {selectedPreset === "custom" && (
        <div>
          <label className="block text-sm font-medium text-panel-text mb-1.5">
            Cron Expression
          </label>
          <input
            type="text"
            value={form.schedule}
            onChange={(e) => setForm({ ...form, schedule: e.target.value })}
            placeholder="* * * * *"
            className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500 font-mono"
          />
          <p className="text-xs text-panel-muted mt-1">
            Format: minute hour day month weekday
          </p>
        </div>
      )}
      <div className="flex justify-end gap-3 pt-2">
        <Button
          variant="secondary"
          type="button"
          onClick={() => {
            setShowAdd(false);
            setShowEdit(false);
            setEditJob(null);
          }}
        >
          Cancel
        </Button>
        <Button type="submit" loading={submitting}>
          {editJob ? "Update Cron" : "Add Cron"}
        </Button>
      </div>
    </form>
  );

  return (
    <div className="space-y-6">
      <Card
        title="Cron Jobs"
        description="Schedule automated tasks on your account"
        actions={
          <Button
            size="sm"
            onClick={() => {
              setForm({ command: "", schedule: "0 0 * * *", description: "" });
              setSelectedPreset("0 0 * * *");
              setShowAdd(true);
            }}
          >
            <Plus size={16} className="mr-1" /> Add Cron
          </Button>
        }
      >
        <div className="mb-4">
          <div className="relative max-w-xs">
            <Search
              size={16}
              className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
            />
            <input
              type="text"
              placeholder="Search cron jobs..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
        </div>
        <Table
          columns={columns}
          data={filtered}
          loading={loading}
          emptyMessage="No cron jobs configured. Add a cron job to automate tasks."
        />
      </Card>

      <Card title="Cron Expression Reference">
        <CodeBlock
          code={`# Cron Expression Format
# ┌──────────── minute (0-59)
# │ ┌────────── hour (0-23)
# │ │ ┌──────── day of month (1-31)
# │ │ │ ┌────── month (1-12)
# │ │ │ │ ┌──── day of week (0-6, Sun=0)
# │ │ │ │ │
# * * * * *

# Examples:
# 0 * * * *     Every hour
# 0 0 * * *     Daily at midnight
# 0 0 * * 0     Weekly on Sunday
# */5 * * * *   Every 5 minutes`}
          language="cron"
        />
      </Card>

      <Modal
        isOpen={showAdd}
        onClose={() => setShowAdd(false)}
        title="Add Cron Job"
      >
        {cronForm(handleAdd)}
      </Modal>

      <Modal
        isOpen={showEdit}
        onClose={() => {
          setShowEdit(false);
          setEditJob(null);
        }}
        title="Edit Cron Job"
      >
        {cronForm(handleEdit)}
      </Modal>
    </div>
  );
}
