import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { AppWindow, Plus, RefreshCw, Search, Trash2, Play, Square, RotateCw } from "lucide-react";

interface Application {
  id: string;
  name: string;
  app_type: string;
  domain: string;
  status: string;
  port: number;
  created_at: string;
}

const typeLabels: Record<string, string> = {
  nodejs: "Node.js", node: "Node.js", python: "Python", go: "Go",
  static: "Static", docker: "Docker", ruby: "Ruby", rust: "Rust", java: "Java", php: "PHP",
};

const typeColors: Record<string, string> = {
  nodejs: "text-green-400", node: "text-green-400", python: "text-yellow-400", go: "text-cyan-400",
  static: "text-purple-400", docker: "text-blue-400", ruby: "text-red-400", rust: "text-orange-400",
  java: "text-red-300", php: "text-indigo-400",
};

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const selectClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";

export default function AppsPage() {
  const [apps, setApps] = useState<Application[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({
    name: "", domain: "", app_type: "node", deploy_method: "git", user: "", port: 3000,
    git_url: "", git_branch: "main", build_cmd: "", start_cmd: "",
  });

  useEffect(() => { fetchApps(); }, []);

  const fetchApps = async () => {
    setLoading(true);
    try { const res = await api.get("/apps"); setApps(res.data.data || []); }
    catch { /* empty */ }
    finally { setLoading(false); }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name || !form.domain || !form.user) { toast.error("Please fill all required fields"); return; }
    setCreating(true);
    try {
      await api.post("/apps/", form);
      toast.success(`Application ${form.name} deployed`);
      setShowCreate(false);
      setForm({ name: "", domain: "", app_type: "node", deploy_method: "git", user: "", port: 3000, git_url: "", git_branch: "main", build_cmd: "", start_cmd: "" });
      fetchApps();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to deploy application");
    } finally { setCreating(false); }
  };

  const handleAction = async (id: string, action: string) => {
    try { await api.post(`/apps/${id}/${action}`); toast.success(`App ${action} successful`); fetchApps(); }
    catch { toast.error(`Failed to ${action} app`); }
  };

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Are you sure you want to delete ${name}?`)) return;
    try { await api.delete(`/apps/${id}`); toast.success(`Application ${name} deleted`); fetchApps(); }
    catch { toast.error("Failed to delete application"); }
  };

  const filtered = apps.filter((a) =>
    (a.name || "").toLowerCase().includes(search.toLowerCase()) ||
    (a.domain || "").toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    { header: "Name", accessor: (a: Application) => (
      <div className="flex items-center gap-2"><AppWindow size={14} className="text-blue-400" /><span className="font-medium text-panel-text">{a.name}</span></div>
    )},
    { header: "Type", accessor: (a: Application) => (
      <span className={`font-medium ${typeColors[a.app_type] || "text-panel-muted"}`}>{typeLabels[a.app_type] || a.app_type}</span>
    )},
    { header: "Domain", accessor: (a: Application) => <span className="text-panel-muted">{a.domain}</span> },
    { header: "Status", accessor: (a: Application) => <StatusBadge status={a.status === "running" ? "active" : a.status} /> },
    { header: "Port", accessor: (a: Application) => (
      <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">:{a.port}</code>
    )},
    { header: "Actions", accessor: (a: Application) => (
      <div className="flex items-center gap-1">
        {a.status === "stopped" ? (
          <button onClick={() => handleAction(a.id, "start")} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors" title="Start"><Play size={14} /></button>
        ) : (
          <button onClick={() => handleAction(a.id, "stop")} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors" title="Stop"><Square size={14} /></button>
        )}
        <button onClick={() => handleAction(a.id, "restart")} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors" title="Restart"><RotateCw size={14} /></button>
        <button onClick={() => handleDelete(a.id, a.name)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors" title="Delete"><Trash2 size={14} /></button>
      </div>
    )},
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Applications</h1>
          <p className="text-panel-muted text-sm mt-1">Deploy and manage your applications</p>
        </div>
        <div className="flex items-center gap-2">
          <Button onClick={fetchApps} className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm">
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
          </Button>
          <Button onClick={() => setShowCreate(true)} className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
            <Plus size={14} /> Deploy App
          </Button>
        </div>
      </div>

      <Card><div className="p-4"><div className="relative">
        <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
        <input type="text" placeholder="Search applications..." value={search} onChange={(e) => setSearch(e.target.value)}
          className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm" />
      </div></div></Card>

      <Card>
        {loading ? (
          <div className="p-8"><div className="space-y-3">{[1,2,3,4,5].map((i) => <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />)}</div></div>
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={filtered} />
        ) : (
          <div className="text-center py-16 px-4">
            <AppWindow size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No applications found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search ? "No applications match your search." : "Deploy your first application to get started."}
            </p>
            {!search && (
              <Button onClick={() => setShowCreate(true)} className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
                <Plus size={14} /> Deploy App
              </Button>
            )}
          </div>
        )}
      </Card>

      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Deploy Application" size="lg">
        <form onSubmit={handleCreate} className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>App Name *</label>
              <input type="text" required placeholder="my-app" value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Domain *</label>
              <input type="text" required placeholder="example.com" value={form.domain}
                onChange={(e) => setForm({ ...form, domain: e.target.value })} className={inputClass} />
            </div>
          </div>
          <div className="grid grid-cols-3 gap-4">
            <div>
              <label className={labelClass}>Type *</label>
              <select value={form.app_type} onChange={(e) => setForm({ ...form, app_type: e.target.value })} className={selectClass}>
                <option value="node">Node.js</option>
                <option value="python">Python</option>
                <option value="go">Go</option>
                <option value="static">Static</option>
                <option value="php">PHP</option>
                <option value="docker">Docker</option>
              </select>
            </div>
            <div>
              <label className={labelClass}>Deploy Method *</label>
              <select value={form.deploy_method} onChange={(e) => setForm({ ...form, deploy_method: e.target.value })} className={selectClass}>
                <option value="git">Git</option>
                <option value="zip">Upload ZIP</option>
                <option value="docker">Docker</option>
              </select>
            </div>
            <div>
              <label className={labelClass}>Port *</label>
              <input type="number" required min={1} max={65535} value={form.port}
                onChange={(e) => setForm({ ...form, port: parseInt(e.target.value) || 3000 })} className={inputClass} />
            </div>
          </div>
          <div>
            <label className={labelClass}>System User *</label>
            <input type="text" required placeholder="username" value={form.user}
              onChange={(e) => setForm({ ...form, user: e.target.value })} className={inputClass} />
          </div>
          {form.deploy_method === "git" && (
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className={labelClass}>Git URL</label>
                <input type="text" placeholder="https://github.com/user/repo.git" value={form.git_url}
                  onChange={(e) => setForm({ ...form, git_url: e.target.value })} className={inputClass} />
              </div>
              <div>
                <label className={labelClass}>Branch</label>
                <input type="text" placeholder="main" value={form.git_branch}
                  onChange={(e) => setForm({ ...form, git_branch: e.target.value })} className={inputClass} />
              </div>
            </div>
          )}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Build Command</label>
              <input type="text" placeholder="npm run build" value={form.build_cmd}
                onChange={(e) => setForm({ ...form, build_cmd: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Start Command</label>
              <input type="text" placeholder="npm start" value={form.start_cmd}
                onChange={(e) => setForm({ ...form, start_cmd: e.target.value })} className={inputClass} />
            </div>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Deploying..." : "Deploy Application"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
