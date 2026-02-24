import React, { useEffect, useState } from "react";
import { Card, Button, Table, Modal, StatusBadge, CodeBlock } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  GitBranch,
  Plus,
  Trash2,
  Search,
  RotateCcw,
  ExternalLink,
  Clock,
  GitCommit,
  CheckCircle2,
  XCircle,
  Loader2,
} from "lucide-react";

interface Deployment {
  id: string;
  repository: string;
  branch: string;
  domain: string;
  status: string;
  commit: string;
  commitMessage: string;
  author: string;
  deployedAt: string;
  buildDuration: string;
}

interface DeployConfig {
  id: string;
  repository: string;
  branch: string;
  domain: string;
  autoDeploy: boolean;
  buildCommand: string;
  outputDir: string;
  envVars: number;
  lastDeployment: string;
  status: string;
}

export default function DeployPage() {
  const [configs, setConfigs] = useState<DeployConfig[]>([]);
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [loading, setLoading] = useState(true);
  const [showConnect, setShowConnect] = useState(false);
  const [selectedConfig, setSelectedConfig] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [form, setForm] = useState({
    repository: "",
    branch: "main",
    domain: "",
    buildCommand: "npm run build",
    outputDir: "dist",
    autoDeploy: true,
  });

  const fetchConfigs = async () => {
    try {
      const res = await api.get("/deployments/configs");
      setConfigs(res.data);
    } catch {
      toast.error("Failed to load deployment configs");
    } finally {
      setLoading(false);
    }
  };

  const fetchDeployments = async (configId: string) => {
    try {
      const res = await api.get(`/deployments/configs/${configId}/history`);
      setDeployments(res.data);
    } catch {
      toast.error("Failed to load deployment history");
    }
  };

  useEffect(() => {
    fetchConfigs();
  }, []);

  useEffect(() => {
    if (selectedConfig) {
      fetchDeployments(selectedConfig);
    }
  }, [selectedConfig]);

  const handleConnect = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.repository.trim() || !form.domain.trim()) {
      toast.error("Repository and domain are required");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/deployments/configs", form);
      toast.success("Repository connected");
      setShowConnect(false);
      setForm({
        repository: "",
        branch: "main",
        domain: "",
        buildCommand: "npm run build",
        outputDir: "dist",
        autoDeploy: true,
      });
      fetchConfigs();
    } catch (err: any) {
      toast.error(
        err.response?.data?.message || "Failed to connect repository"
      );
    } finally {
      setSubmitting(false);
    }
  };

  const handleTriggerDeploy = async (configId: string) => {
    try {
      await api.post(`/deployments/configs/${configId}/deploy`);
      toast.success("Deployment triggered");
      fetchConfigs();
      if (selectedConfig === configId) {
        fetchDeployments(configId);
      }
    } catch {
      toast.error("Failed to trigger deployment");
    }
  };

  const handleDeleteConfig = async (configId: string) => {
    if (!confirm("Disconnect this repository? Existing deployments will remain."))
      return;
    try {
      await api.delete(`/deployments/configs/${configId}`);
      toast.success("Repository disconnected");
      setConfigs((prev) => prev.filter((c) => c.id !== configId));
      if (selectedConfig === configId) {
        setSelectedConfig(null);
        setDeployments([]);
      }
    } catch {
      toast.error("Failed to disconnect repository");
    }
  };

  const getStatusIcon = (status: string) => {
    switch (status.toLowerCase()) {
      case "success":
      case "completed":
      case "live":
        return <CheckCircle2 size={16} className="text-green-400" />;
      case "failed":
        return <XCircle size={16} className="text-red-400" />;
      case "building":
      case "deploying":
        return <Loader2 size={16} className="text-blue-400 animate-spin" />;
      default:
        return <Clock size={16} className="text-panel-muted" />;
    }
  };

  const deploymentColumns = [
    {
      key: "status",
      header: "Status",
      render: (item: Deployment) => (
        <div className="flex items-center gap-2">
          {getStatusIcon(item.status)}
          <StatusBadge status={item.status} />
        </div>
      ),
    },
    {
      key: "commit",
      header: "Commit",
      render: (item: Deployment) => (
        <div>
          <div className="flex items-center gap-1.5">
            <GitCommit size={14} className="text-panel-muted" />
            <code className="text-xs font-mono text-brand-400">
              {item.commit?.slice(0, 7)}
            </code>
          </div>
          <p className="text-xs text-panel-muted mt-0.5 truncate max-w-xs">
            {item.commitMessage}
          </p>
        </div>
      ),
    },
    {
      key: "branch",
      header: "Branch",
      render: (item: Deployment) => (
        <div className="flex items-center gap-1.5">
          <GitBranch size={14} className="text-panel-muted" />
          <span className="text-sm text-panel-text">{item.branch}</span>
        </div>
      ),
    },
    {
      key: "author",
      header: "Author",
      render: (item: Deployment) => (
        <span className="text-panel-muted">{item.author}</span>
      ),
    },
    {
      key: "buildDuration",
      header: "Duration",
      render: (item: Deployment) => (
        <span className="text-panel-muted">{item.buildDuration}</span>
      ),
    },
    {
      key: "deployedAt",
      header: "Deployed",
      render: (item: Deployment) => (
        <span className="text-panel-muted">{item.deployedAt}</span>
      ),
    },
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
      {/* Deployment Configs */}
      <Card
        title="Connected Repositories"
        description="Manage your GitHub deployment configurations"
        actions={
          <Button size="sm" onClick={() => setShowConnect(true)}>
            <Plus size={16} className="mr-1" /> Connect Repo
          </Button>
        }
      >
        {configs.length === 0 ? (
          <div className="text-center py-12">
            <GitBranch size={40} className="mx-auto text-panel-muted mb-3" />
            <p className="text-panel-muted">No repositories connected</p>
            <p className="text-sm text-panel-muted mt-1">
              Connect a GitHub repository to enable auto-deployments
            </p>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {configs.map((config) => (
              <div
                key={config.id}
                onClick={() => setSelectedConfig(config.id)}
                className={`bg-panel-bg border rounded-lg p-4 cursor-pointer transition-colors ${
                  selectedConfig === config.id
                    ? "border-brand-500"
                    : "border-panel-border hover:border-panel-border/80"
                }`}
              >
                <div className="flex items-start justify-between mb-3">
                  <div className="flex items-center gap-2">
                    <GitBranch size={16} className="text-purple-400" />
                    <h3 className="font-medium text-white">
                      {config.repository}
                    </h3>
                  </div>
                  <StatusBadge status={config.status} />
                </div>
                <div className="text-sm text-panel-muted space-y-1 mb-3">
                  <p>
                    Branch:{" "}
                    <span className="text-panel-text">{config.branch}</span>
                  </p>
                  <p>
                    Domain:{" "}
                    <span className="text-panel-text">{config.domain}</span>
                  </p>
                  <p>
                    Auto-deploy:{" "}
                    <span
                      className={
                        config.autoDeploy
                          ? "text-green-400"
                          : "text-yellow-400"
                      }
                    >
                      {config.autoDeploy ? "Enabled" : "Disabled"}
                    </span>
                  </p>
                  <p>
                    Last deploy:{" "}
                    <span className="text-panel-text">
                      {config.lastDeployment || "Never"}
                    </span>
                  </p>
                </div>
                <div className="flex items-center gap-2 pt-3 border-t border-panel-border">
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={(e) => {
                      e.stopPropagation();
                      handleTriggerDeploy(config.id);
                    }}
                  >
                    <RotateCcw size={14} className="mr-1" /> Deploy
                  </Button>
                  {config.domain && (
                    <a
                      href={`https://${config.domain}`}
                      target="_blank"
                      rel="noreferrer"
                      onClick={(e) => e.stopPropagation()}
                      className="text-panel-muted hover:text-brand-400 transition-colors"
                      title="Visit"
                    >
                      <ExternalLink size={16} />
                    </a>
                  )}
                  <button
                    onClick={(e) => {
                      e.stopPropagation();
                      handleDeleteConfig(config.id);
                    }}
                    className="text-panel-muted hover:text-red-400 transition-colors ml-auto"
                    title="Disconnect"
                  >
                    <Trash2 size={16} />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      {/* Deployment History */}
      {selectedConfig && (
        <Card
          title="Deployment History"
          description="Recent deployments for the selected repository"
        >
          <div className="mb-4">
            <div className="relative max-w-xs">
              <Search
                size={16}
                className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
              />
              <input
                type="text"
                placeholder="Search deployments..."
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
              />
            </div>
          </div>
          <Table
            columns={deploymentColumns}
            data={
              deployments.filter(
                (d) =>
                  d.commitMessage
                    ?.toLowerCase()
                    .includes(search.toLowerCase()) ||
                  d.author?.toLowerCase().includes(search.toLowerCase()) ||
                  d.commit?.toLowerCase().includes(search.toLowerCase())
              ) as any
            }
            loading={false}
            emptyMessage="No deployments yet for this repository."
          />
        </Card>
      )}

      {/* Connect Repo Modal */}
      <Modal
        isOpen={showConnect}
        onClose={() => setShowConnect(false)}
        title="Connect Repository"
        size="lg"
      >
        <form onSubmit={handleConnect} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              GitHub Repository
            </label>
            <input
              type="text"
              value={form.repository}
              onChange={(e) => setForm({ ...form, repository: e.target.value })}
              placeholder="username/repository"
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Branch
              </label>
              <input
                type="text"
                value={form.branch}
                onChange={(e) => setForm({ ...form, branch: e.target.value })}
                placeholder="main"
                className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Domain
              </label>
              <input
                type="text"
                value={form.domain}
                onChange={(e) => setForm({ ...form, domain: e.target.value })}
                placeholder="app.example.com"
                className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
              />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Build Command
              </label>
              <input
                type="text"
                value={form.buildCommand}
                onChange={(e) =>
                  setForm({ ...form, buildCommand: e.target.value })
                }
                placeholder="npm run build"
                className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500 font-mono"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Output Directory
              </label>
              <input
                type="text"
                value={form.outputDir}
                onChange={(e) =>
                  setForm({ ...form, outputDir: e.target.value })
                }
                placeholder="dist"
                className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500 font-mono"
              />
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm text-panel-text">
            <input
              type="checkbox"
              checked={form.autoDeploy}
              onChange={(e) =>
                setForm({ ...form, autoDeploy: e.target.checked })
              }
              className="rounded border-panel-border bg-panel-bg text-brand-600 focus:ring-brand-500"
            />
            Enable auto-deploy on push
          </label>
          <div className="flex justify-end gap-3 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowConnect(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Connect Repository
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
