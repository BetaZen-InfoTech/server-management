import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Clock, Plus, RefreshCw, Search, Trash2, Edit, Play, Pause } from "lucide-react";

interface CronJob {
  id: string;
  schedule: string;
  command: string;
  status: "active" | "paused" | "error";
  lastRun: string;
  nextRun: string;
}

export default function CronPage() {
  const [jobs, setJobs] = useState<CronJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

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

  const handleToggle = async (id: string, currentStatus: string) => {
    try {
      await api.patch(`/cron/${id}/toggle`);
      const action = currentStatus === "active" ? "pause" : "resume";
      toast.success(`Cron job ${action}d`);
      fetchJobs();
    } catch {
      toast.error("Failed to update cron job");
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
      accessor: (j: CronJob) => <StatusBadge status={j.status === "paused" ? "inactive" : j.status} />,
    },
    {
      header: "Last Run",
      accessor: (j: CronJob) => (
        <span className="text-panel-muted text-sm">{j.lastRun || "Never"}</span>
      ),
    },
    {
      header: "Next Run",
      accessor: (j: CronJob) => (
        <span className="text-panel-muted text-sm">{j.nextRun || "--"}</span>
      ),
    },
    {
      header: "Actions",
      accessor: (j: CronJob) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => handleToggle(j.id, j.status)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors"
            title={j.status === "active" ? "Pause" : "Resume"}
          >
            {j.status === "active" ? <Pause size={14} /> : <Play size={14} />}
          </button>
          <button className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors">
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
            onClick={() => toast("Add Cron Job modal coming soon")}
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
                onClick={() => toast("Add Cron Job modal coming soon")}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Add Cron Job
              </Button>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
