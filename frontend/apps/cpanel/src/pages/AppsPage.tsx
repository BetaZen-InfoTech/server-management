import React, { useEffect, useState } from "react";
import { Card, Button, Modal, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Rocket,
  Plus,
  Play,
  Square,
  RotateCcw,
  Trash2,
  ExternalLink,
  Search,
} from "lucide-react";

interface App {
  id: string;
  name: string;
  type: string;
  status: string;
  domain: string;
  port: number;
  runtime: string;
  createdAt: string;
}

export default function AppsPage() {
  const [apps, setApps] = useState<App[]>([]);
  const [loading, setLoading] = useState(true);
  const [showDeploy, setShowDeploy] = useState(false);
  const [search, setSearch] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [form, setForm] = useState({
    name: "",
    type: "nodejs",
    domain: "",
    runtime: "node-20",
  });

  const fetchApps = async () => {
    try {
      const res = await api.get("/apps");
      setApps(res.data.data || []);
    } catch {
      toast.error("Failed to load applications");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchApps();
  }, []);

  const handleDeploy = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name.trim()) {
      toast.error("App name is required");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/apps", form);
      toast.success("Application created successfully");
      setShowDeploy(false);
      setForm({ name: "", type: "nodejs", domain: "", runtime: "node-20" });
      fetchApps();
    } catch (err: any) {
      toast.error(err.response?.data?.message || "Failed to create application");
    } finally {
      setSubmitting(false);
    }
  };

  const handleAction = async (
    id: string,
    action: "start" | "stop" | "restart" | "delete"
  ) => {
    if (action === "delete" && !confirm("Delete this application?")) return;
    try {
      if (action === "delete") {
        await api.delete(`/apps/${id}`);
        toast.success("Application deleted");
        setApps((prev) => prev.filter((a) => a.id !== id));
      } else {
        await api.post(`/apps/${id}/${action}`);
        toast.success(`Application ${action}ed successfully`);
        fetchApps();
      }
    } catch {
      toast.error(`Failed to ${action} application`);
    }
  };

  const filtered = apps.filter(
    (a) =>
      a.name.toLowerCase().includes(search.toLowerCase()) ||
      a.domain.toLowerCase().includes(search.toLowerCase())
  );

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-brand-400" />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <Card
        title="My Applications"
        description="Manage your deployed applications"
        actions={
          <Button size="sm" onClick={() => setShowDeploy(true)}>
            <Plus size={16} className="mr-1" /> Deploy App
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
              placeholder="Search apps..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
        </div>

        {filtered.length === 0 ? (
          <div className="text-center py-12">
            <Rocket size={40} className="mx-auto text-panel-muted mb-3" />
            <p className="text-panel-muted">No applications deployed yet</p>
            <p className="text-sm text-panel-muted mt-1">
              Deploy your first app to get started
            </p>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {filtered.map((app) => (
              <div
                key={app.id}
                className="bg-panel-bg border border-panel-border rounded-lg p-4"
              >
                <div className="flex items-start justify-between mb-3">
                  <div>
                    <h3 className="font-medium text-white">{app.name}</h3>
                    <p className="text-sm text-panel-muted mt-0.5">
                      {app.type} &middot; {app.runtime}
                    </p>
                  </div>
                  <StatusBadge status={app.status} />
                </div>
                <div className="text-sm text-panel-muted mb-3">
                  <p>
                    Domain:{" "}
                    <span className="text-panel-text">{app.domain || "Not assigned"}</span>
                  </p>
                  <p>
                    Port: <span className="text-panel-text">{app.port}</span>
                  </p>
                </div>
                <div className="flex items-center gap-2 pt-3 border-t border-panel-border">
                  {app.status === "stopped" ? (
                    <button
                      onClick={() => handleAction(app.id, "start")}
                      className="text-green-400 hover:text-green-300 transition-colors"
                      title="Start"
                    >
                      <Play size={16} />
                    </button>
                  ) : (
                    <button
                      onClick={() => handleAction(app.id, "stop")}
                      className="text-yellow-400 hover:text-yellow-300 transition-colors"
                      title="Stop"
                    >
                      <Square size={16} />
                    </button>
                  )}
                  <button
                    onClick={() => handleAction(app.id, "restart")}
                    className="text-blue-400 hover:text-blue-300 transition-colors"
                    title="Restart"
                  >
                    <RotateCcw size={16} />
                  </button>
                  {app.domain && (
                    <a
                      href={`https://${app.domain}`}
                      target="_blank"
                      rel="noreferrer"
                      className="text-panel-muted hover:text-brand-400 transition-colors"
                      title="Visit"
                    >
                      <ExternalLink size={16} />
                    </a>
                  )}
                  <button
                    onClick={() => handleAction(app.id, "delete")}
                    className="text-panel-muted hover:text-red-400 transition-colors ml-auto"
                    title="Delete"
                  >
                    <Trash2 size={16} />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      <Modal
        isOpen={showDeploy}
        onClose={() => setShowDeploy(false)}
        title="Deploy Application"
        size="lg"
      >
        <form onSubmit={handleDeploy} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Application Name
            </label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="my-app"
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Application Type
              </label>
              <select
                value={form.type}
                onChange={(e) => setForm({ ...form, type: e.target.value })}
                className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500"
              >
                <option value="nodejs">Node.js</option>
                <option value="python">Python</option>
                <option value="php">PHP</option>
                <option value="ruby">Ruby</option>
                <option value="go">Go</option>
                <option value="static">Static Site</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Runtime
              </label>
              <select
                value={form.runtime}
                onChange={(e) => setForm({ ...form, runtime: e.target.value })}
                className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500"
              >
                <option value="node-20">Node.js 20 LTS</option>
                <option value="node-18">Node.js 18 LTS</option>
                <option value="python-3.12">Python 3.12</option>
                <option value="python-3.11">Python 3.11</option>
                <option value="php-8.3">PHP 8.3</option>
                <option value="ruby-3.3">Ruby 3.3</option>
                <option value="go-1.22">Go 1.22</option>
              </select>
            </div>
          </div>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Domain (optional)
            </label>
            <input
              type="text"
              value={form.domain}
              onChange={(e) => setForm({ ...form, domain: e.target.value })}
              placeholder="app.example.com"
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowDeploy(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Deploy
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
