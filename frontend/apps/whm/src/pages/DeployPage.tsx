import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  GitBranch, Plus, RefreshCw, Search, Trash2, ExternalLink,
  RotateCw, Play, CheckCircle, XCircle, Clock, GitCommit
} from "lucide-react";

interface Deployment {
  id: string;
  repo: string;
  branch: string;
  current_commit: string;
  commit_message: string;
  commit_author: string;
  domain: string;
  status: string;
  created_at: string;
  updated_at: string;
}

export default function DeployPage() {
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  useEffect(() => {
    fetchDeployments();
  }, []);

  const fetchDeployments = async () => {
    setLoading(true);
    try {
      const res = await api.get("/deploy");
      setDeployments(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleRedeploy = async (id: string) => {
    try {
      await api.post(`/deploy/${id}/redeploy`);
      toast.success("Redeployment initiated");
      fetchDeployments();
    } catch {
      toast.error("Failed to redeploy");
    }
  };

  const handleRollback = async (id: string) => {
    if (!confirm("Are you sure you want to rollback to this deployment?")) return;
    try {
      await api.post(`/deploy/${id}/rollback`);
      toast.success("Rollback initiated");
      fetchDeployments();
    } catch {
      toast.error("Failed to rollback");
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("Are you sure you want to delete this deployment record?")) return;
    try {
      await api.delete(`/deploy/${id}`);
      toast.success("Deployment record deleted");
      fetchDeployments();
    } catch {
      toast.error("Failed to delete deployment");
    }
  };

  const getStatusIcon = (status: string) => {
    switch (status) {
      case "success": return <CheckCircle size={14} className="text-green-400" />;
      case "failed": return <XCircle size={14} className="text-red-400" />;
      case "deploying": return <RotateCw size={14} className="text-blue-400 animate-spin" />;
      case "pending": return <Clock size={14} className="text-yellow-400" />;
      default: return <GitBranch size={14} className="text-panel-muted" />;
    }
  };

  const filtered = deployments.filter(
    (d) =>
      (d.repo || "").toLowerCase().includes(search.toLowerCase()) ||
      (d.branch || "").toLowerCase().includes(search.toLowerCase()) ||
      (d.commit_message || "").toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Repository",
      accessor: (d: Deployment) => (
        <div className="flex items-center gap-2">
          <GitBranch size={14} className="text-purple-400" />
          <div>
            <span className="font-medium text-panel-text block">{d.repo}</span>
            <span className="text-xs text-panel-muted flex items-center gap-1">
              <GitCommit size={10} />
              {d.branch} {d.current_commit ? `@ ${d.current_commit.substring(0, 7)}` : ""}
            </span>
          </div>
        </div>
      ),
    },
    {
      header: "Commit",
      accessor: (d: Deployment) => (
        <span className="text-panel-muted text-sm truncate max-w-[200px] block">
          {d.commit_message || "--"}
        </span>
      ),
    },
    {
      header: "Status",
      accessor: (d: Deployment) => (
        <div className="flex items-center gap-1.5">
          {getStatusIcon(d.status)}
          <StatusBadge status={
            d.status === "success" ? "active" :
            d.status === "failed" ? "error" :
            d.status === "deploying" ? "warning" :
            "pending"
          } />
        </div>
      ),
    },
    {
      header: "Domain",
      accessor: (d: Deployment) => (
        <span className="text-panel-muted text-sm">{d.domain || "--"}</span>
      ),
    },
    {
      header: "Deployed",
      accessor: (d: Deployment) => (
        <div>
          <span className="text-panel-muted text-sm block">{d.updated_at ? new Date(d.updated_at).toLocaleDateString() : "--"}</span>
          {d.commit_author && <span className="text-xs text-panel-muted/60">by {d.commit_author}</span>}
        </div>
      ),
    },
    {
      header: "Actions",
      accessor: (d: Deployment) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => handleRedeploy(d.id)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
            title="Redeploy"
          >
            <Play size={14} />
          </button>
          <button
            onClick={() => handleRollback(d.id)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors"
            title="Rollback"
          >
            <RotateCw size={14} />
          </button>
          <button
            onClick={() => handleDelete(d.id)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
            title="Delete"
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
          <h1 className="text-xl font-bold text-panel-text">Deployments</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage GitHub deployments and CI/CD pipelines
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchDeployments}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => toast("New Deployment modal coming soon")}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            New Deployment
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search by repository, branch, or commit message..."
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
              {[1, 2, 3, 4, 5].map((i) => (
                <div key={i} className="h-14 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={filtered} />
        ) : (
          <div className="text-center py-16 px-4">
            <GitBranch size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No deployments found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No deployments match your search. Try a different search term."
                : "Connect a GitHub repository and deploy your application with a single click."}
            </p>
            {!search && (
              <Button
                onClick={() => toast("New Deployment modal coming soon")}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                New Deployment
              </Button>
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
