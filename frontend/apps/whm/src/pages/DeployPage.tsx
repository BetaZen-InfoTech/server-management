import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  GitBranch, Plus, RefreshCw, Search, Trash2,
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

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const selectClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";

export default function DeployPage() {
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({
    domain: "", repo: "", branch: "main", app_type: "nodejs", auto_deploy: true,
    build_command: "", start_command: "", node_version: "20",
  });

  useEffect(() => { fetchDeployments(); }, []);

  const fetchDeployments = async () => {
    setLoading(true);
    try { const res = await api.get("/deploy"); setDeployments(res.data.data || []); }
    catch { /* empty */ }
    finally { setLoading(false); }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.domain || !form.repo || !form.branch) { toast.error("Please fill all required fields"); return; }
    setCreating(true);
    try {
      await api.post("/deploy/", form);
      toast.success("Deployment created successfully");
      setShowCreate(false);
      setForm({ domain: "", repo: "", branch: "main", app_type: "nodejs", auto_deploy: true, build_command: "", start_command: "", node_version: "20" });
      fetchDeployments();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create deployment");
    } finally { setCreating(false); }
  };

  const handleRedeploy = async (id: string) => {
    try { await api.post(`/deploy/${id}/redeploy`); toast.success("Redeployment initiated"); fetchDeployments(); }
    catch { toast.error("Failed to redeploy"); }
  };

  const handleRollback = async (id: string) => {
    if (!confirm("Are you sure you want to rollback to this deployment?")) return;
    try { await api.post(`/deploy/${id}/rollback`); toast.success("Rollback initiated"); fetchDeployments(); }
    catch { toast.error("Failed to rollback"); }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("Are you sure you want to delete this deployment record?")) return;
    try { await api.delete(`/deploy/${id}`); toast.success("Deployment record deleted"); fetchDeployments(); }
    catch { toast.error("Failed to delete deployment"); }
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

  const filtered = deployments.filter((d) =>
    (d.repo || "").toLowerCase().includes(search.toLowerCase()) ||
    (d.branch || "").toLowerCase().includes(search.toLowerCase()) ||
    (d.commit_message || "").toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    { header: "Repository", accessor: (d: Deployment) => (
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
    )},
    { header: "Commit", accessor: (d: Deployment) => (
      <span className="text-panel-muted text-sm truncate max-w-[200px] block">{d.commit_message || "--"}</span>
    )},
    { header: "Status", accessor: (d: Deployment) => (
      <div className="flex items-center gap-1.5">
        {getStatusIcon(d.status)}
        <StatusBadge status={d.status === "success" ? "active" : d.status === "failed" ? "error" : d.status === "deploying" ? "warning" : "pending"} />
      </div>
    )},
    { header: "Domain", accessor: (d: Deployment) => <span className="text-panel-muted text-sm">{d.domain || "--"}</span> },
    { header: "Deployed", accessor: (d: Deployment) => (
      <div>
        <span className="text-panel-muted text-sm block">{d.updated_at ? new Date(d.updated_at).toLocaleDateString() : "--"}</span>
        {d.commit_author && <span className="text-xs text-panel-muted/60">by {d.commit_author}</span>}
      </div>
    )},
    { header: "Actions", accessor: (d: Deployment) => (
      <div className="flex items-center gap-1">
        <button onClick={() => handleRedeploy(d.id)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors" title="Redeploy"><Play size={14} /></button>
        <button onClick={() => handleRollback(d.id)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors" title="Rollback"><RotateCw size={14} /></button>
        <button onClick={() => handleDelete(d.id)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors" title="Delete"><Trash2 size={14} /></button>
      </div>
    )},
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Deployments</h1>
          <p className="text-panel-muted text-sm mt-1">Manage GitHub deployments and CI/CD pipelines</p>
        </div>
        <div className="flex items-center gap-2">
          <Button onClick={fetchDeployments} className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm">
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
          </Button>
          <Button onClick={() => setShowCreate(true)} className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
            <Plus size={14} /> New Deployment
          </Button>
        </div>
      </div>

      <Card><div className="p-4"><div className="relative">
        <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
        <input type="text" placeholder="Search by repository, branch, or commit message..." value={search} onChange={(e) => setSearch(e.target.value)}
          className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm" />
      </div></div></Card>

      <Card>
        {loading ? (
          <div className="p-8"><div className="space-y-3">{[1,2,3,4,5].map((i) => <div key={i} className="h-14 bg-panel-border/20 rounded animate-pulse" />)}</div></div>
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={filtered} />
        ) : (
          <div className="text-center py-16 px-4">
            <GitBranch size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No deployments found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search ? "No deployments match your search." : "Connect a GitHub repository and deploy your application with a single click."}
            </p>
            {!search && (
              <Button onClick={() => setShowCreate(true)} className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
                <Plus size={14} /> New Deployment
              </Button>
            )}
          </div>
        )}
      </Card>

      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="New GitHub Deployment" size="lg">
        <form onSubmit={handleCreate} className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Domain *</label>
              <input type="text" required placeholder="example.com" value={form.domain}
                onChange={(e) => setForm({ ...form, domain: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Repository *</label>
              <input type="text" required placeholder="user/repo" value={form.repo}
                onChange={(e) => setForm({ ...form, repo: e.target.value })} className={inputClass} />
            </div>
          </div>
          <div className="grid grid-cols-3 gap-4">
            <div>
              <label className={labelClass}>Branch *</label>
              <input type="text" required placeholder="main" value={form.branch}
                onChange={(e) => setForm({ ...form, branch: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>App Type *</label>
              <select value={form.app_type} onChange={(e) => setForm({ ...form, app_type: e.target.value })} className={selectClass}>
                <option value="nodejs">Node.js</option>
                <option value="static">Static</option>
                <option value="php">PHP</option>
                <option value="python">Python</option>
                <option value="go">Go</option>
                <option value="docker">Docker</option>
              </select>
            </div>
            <div>
              <label className={labelClass}>Node Version</label>
              <select value={form.node_version} onChange={(e) => setForm({ ...form, node_version: e.target.value })} className={selectClass}>
                <option value="20">20 LTS</option>
                <option value="18">18 LTS</option>
                <option value="22">22</option>
              </select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Build Command</label>
              <input type="text" placeholder="npm run build" value={form.build_command}
                onChange={(e) => setForm({ ...form, build_command: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Start Command</label>
              <input type="text" placeholder="npm start" value={form.start_command}
                onChange={(e) => setForm({ ...form, start_command: e.target.value })} className={inputClass} />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <input type="checkbox" id="auto_deploy" checked={form.auto_deploy}
              onChange={(e) => setForm({ ...form, auto_deploy: e.target.checked })}
              className="rounded border-panel-border bg-panel-bg text-blue-600 focus:ring-blue-500" />
            <label htmlFor="auto_deploy" className="text-sm text-panel-text">Auto-deploy on push</label>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Creating..." : "Create Deployment"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
