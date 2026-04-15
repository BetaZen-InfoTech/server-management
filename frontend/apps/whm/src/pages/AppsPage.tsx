import { useState, useEffect, useMemo } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  AppWindow, Plus, RefreshCw, Search, Trash2, Play, Square, RotateCw,
  Archive, Upload, ArrowRightLeft, ChevronDown, ChevronUp, X, Package,
} from "lucide-react";

interface Application {
  id: string;
  name: string;
  app_type: string;
  framework?: string;
  domain: string;
  status: string;
  port: number;
  user: string;
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

// Framework presets. At runtime these are fetched from GET /apps/presets so
// the frontend and backend can never drift — but we keep a hard-coded copy
// as a fallback for offline dev and for the (rare) case where the preset
// endpoint fails. When a preset is chosen, the form auto-fills the build /
// start / port fields, but the user can still override any of them.
type Preset = {
  framework?: string;
  label: string;
  app_type: string;
  build_cmd: string;
  start_cmd: string;
  default_port: number;
  is_static?: boolean;
};
const FALLBACK_PRESETS: Record<string, Preset> = {
  "node-express": {
    label: "Node.js (Express / vanilla)",
    app_type: "node",
    build_cmd: "npm install --omit=dev --no-audit --no-fund --loglevel=error",
    start_cmd: "/usr/local/bin/node server.js",
    default_port: 3000,
  },
  "nextjs": {
    label: "Next.js 14 (App Router)",
    app_type: "node",
    build_cmd: "npm install --no-audit --no-fund --loglevel=error && npm run build",
    start_cmd: "/usr/local/bin/npx next start -p ${PORT}",
    default_port: 3000,
  },
  "react-vite": {
    label: "React (Vite) — static build",
    app_type: "static",
    build_cmd: "npm install --no-audit --no-fund --loglevel=error && npm run build",
    start_cmd: "",
    default_port: 0,
    is_static: true,
  },
  "python-flask": {
    label: "Python — Flask + gunicorn",
    app_type: "python",
    build_cmd: "python3 -m venv venv && ./venv/bin/pip install --quiet flask gunicorn",
    start_cmd: "./venv/bin/gunicorn --bind 0.0.0.0:${PORT} --workers 2 app:app",
    default_port: 5000,
  },
  "ruby-sinatra": {
    label: "Ruby — Sinatra",
    app_type: "ruby",
    build_cmd: "bundle config set --local path 'vendor/bundle' && bundle install --quiet",
    start_cmd: "bundle exec ruby app.rb -o 0.0.0.0 -p ${PORT}",
    default_port: 4567,
  },
  "custom": {
    label: "Custom (fill everything manually)",
    app_type: "node",
    build_cmd: "",
    start_cmd: "",
    default_port: 0,
  },
};

// buildCmdHint mirrors services.missingBuildCmdHint on the backend so the
// frontend and server error messages stay identical. Returns an example
// build command for types that need one, or "" when no build step is
// required (docker, static, php, java, prebuilt binary).
const buildCmdHint = (appType: string): string => {
  switch (appType) {
    case "node":
    case "nodejs":
      return "npm install --omit=dev";
    case "python":
      return "python3 -m venv venv && ./venv/bin/pip install -r requirements.txt";
    case "ruby":
      return "bundle config set --local path 'vendor/bundle' && bundle install";
    case "go":
      return "go build -o app ./...";
    case "rust":
      return "cargo build --release";
    default:
      return "";
  }
};

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const selectClass = inputClass;

type DeployForm = {
  name: string;
  domain: string;
  path: string;
  framework: string;
  app_type: string;
  deploy_method: "scaffold" | "git" | "local";
  user: string;
  install_path: string;
  port: number;
  auto_port: boolean;
  git_url: string;
  git_branch: string;
  git_token: string;
  build_cmd: string;
  start_cmd: string;
  runtime_version: string;
  health_check_path: string;
};

const emptyForm: DeployForm = {
  name: "", domain: "", path: "/", framework: "node-express", app_type: "node",
  deploy_method: "scaffold", user: "ubuntu", install_path: "", port: 0, auto_port: true,
  git_url: "", git_branch: "main", git_token: "",
  build_cmd: "", start_cmd: "", runtime_version: "", health_check_path: "/",
};

interface DomainOption { id: string; domain: string; user: string }

export default function AppsPage() {
  const [apps, setApps] = useState<Application[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState<DeployForm>(emptyForm);
  const [envRows, setEnvRows] = useState<{ key: string; value: string }[]>([]);
  const [availableDomains, setAvailableDomains] = useState<DomainOption[]>([]);
  const [selectedDomains, setSelectedDomains] = useState<string[]>([]);
  const [presets, setPresets] = useState<Record<string, Preset>>(FALLBACK_PRESETS);
  const [buildCmdError, setBuildCmdError] = useState<string>("");

  // Backup/Restore/Transfer modal state
  const [backupApp, setBackupApp] = useState<Application | null>(null);
  const [backups, setBackups] = useState<{ file: string; size: number; created_at: string }[]>([]);
  const [transferApp, setTransferApp] = useState<Application | null>(null);
  const [transferUser, setTransferUser] = useState("");
  const [pkgApp, setPkgApp] = useState<Application | null>(null);
  const [pkgCmd, setPkgCmd] = useState("");
  const [pkgRunning, setPkgRunning] = useState(false);
  const [pkgOutput, setPkgOutput] = useState<string>("");
  const [pkgSuccess, setPkgSuccess] = useState<boolean | null>(null);
  const [pkgDurationMs, setPkgDurationMs] = useState<number | null>(null);

  useEffect(() => { fetchApps(); fetchDomains(); fetchPresets(); }, []);

  // Fetch the authoritative preset catalogue from the backend so the deploy
  // modal can't drift out of sync with what the server actually runs. Falls
  // back to the bundled FALLBACK_PRESETS on any error so the page still works
  // in offline dev.
  const fetchPresets = async () => {
    try {
      const res = await api.get("/apps/presets");
      const data = res.data?.data;
      if (data && typeof data === "object" && Object.keys(data).length > 0) {
        // Backend never ships a "custom" preset — it's a UI-only escape hatch
        // that forces the user to fill everything themselves.
        setPresets({
          ...(data as Record<string, Preset>),
          custom: {
            label: "Custom (fill everything manually)",
            app_type: "node",
            build_cmd: "",
            start_cmd: "",
            default_port: 0,
          },
        });
      }
    } catch { /* keep FALLBACK_PRESETS */ }
  };

  const fetchApps = async () => {
    setLoading(true);
    try { const res = await api.get("/apps"); setApps(res.data.data || []); }
    catch { /* empty */ }
    finally { setLoading(false); }
  };

  const fetchDomains = async () => {
    try {
      const res = await api.get("/domains?limit=500");
      setAvailableDomains(res.data.data || []);
    } catch { /* keep empty */ }
  };

  // When framework changes, autofill build/start/port. Unlike the old
  // behaviour, this OVERWRITES the build_cmd / start_cmd even if the user
  // already typed something, because picking a preset is the clearest signal
  // that they want the server's defaults. The custom preset clears both so
  // there's no residue from a previously-picked framework.
  const applyPreset = (framework: string) => {
    const p = presets[framework];
    if (!p) return;
    setForm((prev) => ({
      ...prev,
      framework,
      app_type: p.app_type,
      build_cmd: p.build_cmd,
      start_cmd: p.start_cmd,
      port: p.default_port || prev.port,
      auto_port: p.is_static ? true : prev.auto_port,
    }));
    setBuildCmdError("");
  };

  const resetForm = () => {
    setForm(emptyForm);
    setEnvRows([]);
    setShowAdvanced(false);
    setSelectedDomains([]);
  };

  // Single-select dropdown replaces the old checkbox list. Still backed by
  // selectedDomains so the submit payload can keep sending `domains: [...]`
  // for backend back-compat.
  const selectDomain = (dom: string) => {
    if (!dom) {
      setSelectedDomains([]);
      setForm((f) => ({ ...f, domain: "" }));
      return;
    }
    setSelectedDomains([dom]);
    const owner = availableDomains.find((d) => d.domain === dom)?.user || "";
    setForm((f) => ({ ...f, domain: dom, user: owner || f.user }));
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name || selectedDomains.length === 0 || !form.user) {
      toast.error("Name, at least one domain, and system user are required");
      return;
    }
    if (!/^[a-z][a-z0-9-]{1,31}$/.test(form.name)) {
      toast.error("App name must be lowercase, start with a letter, and use only a-z 0-9 and '-'");
      return;
    }
    // Interpreted / build-step runtimes always need an install/build step
    // before the service can start. Reject here with an inline field error
    // (not just a toast) so the user sees exactly which field to fix. A
    // framework preset auto-fills the command so the check only fires for
    // Custom + a type that needs a build.
    const hint = buildCmdHint(form.app_type);
    if (hint && !form.build_cmd.trim()) {
      const msg = `${form.app_type} apps require a build command (e.g. "${hint}")`;
      setBuildCmdError(msg);
      toast.error(msg);
      return;
    }
    setBuildCmdError("");
    setCreating(true);
    const env_vars: Record<string, string> = {};
    envRows.forEach((r) => { if (r.key.trim()) env_vars[r.key.trim()] = r.value; });
    const payload: Record<string, unknown> = {
      name: form.name,
      domain: selectedDomains[0],
      domains: selectedDomains,
      path: form.path || "/",
      framework: form.framework === "custom" ? "" : form.framework,
      app_type: form.app_type,
      deploy_method: form.deploy_method,
      user: form.user,
      install_path: form.install_path.trim(),
      port: form.auto_port ? 0 : form.port,
      git_url: form.git_url,
      git_branch: form.git_branch,
      git_token: form.git_token,
      build_cmd: form.build_cmd,
      start_cmd: form.start_cmd,
      runtime_version: form.runtime_version,
      health_check_path: form.health_check_path,
      env_vars,
    };
    try {
      await api.post("/apps/deploy", payload);
      toast.success(`Application ${form.name} deployed`);
      setShowCreate(false);
      resetForm();
      fetchApps();
    } catch (err) {
      const e = err as { response?: { data?: { error?: { message?: string } } } };
      toast.error(e?.response?.data?.error?.message || "Failed to deploy application");
    } finally { setCreating(false); }
  };

  const handleAction = async (name: string, action: string) => {
    try { await api.post(`/apps/${name}/${action}`); toast.success(`App ${action} successful`); fetchApps(); }
    catch { toast.error(`Failed to ${action} app`); }
  };
  const handleDelete = async (name: string) => {
    if (!confirm(`Delete app "${name}"? Code, service and nginx config will be removed.`)) return;
    try { await api.delete(`/apps/${name}`); toast.success(`Application ${name} deleted`); fetchApps(); }
    catch { toast.error("Failed to delete application"); }
  };

  // --- Backup / Restore ---
  const openBackups = async (app: Application) => {
    setBackupApp(app);
    try {
      const res = await api.get(`/apps/${app.name}/backups`);
      setBackups(res.data.data || []);
    } catch { setBackups([]); }
  };
  const runBackup = async () => {
    if (!backupApp) return;
    try {
      await api.post(`/apps/${backupApp.name}/backup`);
      toast.success("Backup created");
      const res = await api.get(`/apps/${backupApp.name}/backups`);
      setBackups(res.data.data || []);
    } catch (err) {
      const e = err as { response?: { data?: { error?: { message?: string } } } };
      toast.error(e?.response?.data?.error?.message || "Backup failed");
    }
  };
  const runRestore = async (file: string) => {
    if (!backupApp) return;
    if (!confirm(`Restore ${backupApp.name} from this backup? Current state will be snapshotted first.`)) return;
    try {
      await api.post(`/apps/${backupApp.name}/restore`, { file });
      toast.success("Restore completed");
      fetchApps();
    } catch (err) {
      const e = err as { response?: { data?: { error?: { message?: string } } } };
      toast.error(e?.response?.data?.error?.message || "Restore failed");
    }
  };

  // --- Install packages (one-shot) ---
  const openPackageInstall = (app: Application) => {
    setPkgApp(app);
    setPkgCmd("");
    setPkgOutput("");
    setPkgSuccess(null);
    setPkgDurationMs(null);
  };
  const runPackageInstall = async () => {
    if (!pkgApp) return;
    setPkgRunning(true);
    setPkgOutput("");
    setPkgSuccess(null);
    setPkgDurationMs(null);
    try {
      const res = await api.post(`/apps/${pkgApp.name}/install-packages`, {
        cmd: pkgCmd.trim(),
      });
      const data = res.data.data || {};
      setPkgOutput(data.output || "(no output)");
      setPkgSuccess(!!data.success);
      setPkgDurationMs(typeof data.duration_ms === "number" ? data.duration_ms : null);
      if (data.success) {
        toast.success("Package install finished");
      } else {
        toast.error("Package install failed — see output");
      }
    } catch (err) {
      const e = err as { response?: { data?: { error?: { message?: string } } } };
      setPkgOutput(e?.response?.data?.error?.message || "Request failed");
      setPkgSuccess(false);
      toast.error("Package install request failed");
    } finally {
      setPkgRunning(false);
    }
  };

  // --- Transfer ---
  const runTransfer = async () => {
    if (!transferApp || !transferUser.trim()) return;
    try {
      await api.post(`/apps/${transferApp.name}/transfer`, { target_user: transferUser.trim() });
      toast.success(`Transferred to ${transferUser}`);
      setTransferApp(null); setTransferUser("");
      fetchApps();
    } catch (err) {
      const e = err as { response?: { data?: { error?: { message?: string } } } };
      toast.error(e?.response?.data?.error?.message || "Transfer failed");
    }
  };

  const filtered = useMemo(() => apps.filter((a) =>
    (a.name || "").toLowerCase().includes(search.toLowerCase()) ||
    (a.domain || "").toLowerCase().includes(search.toLowerCase())
  ), [apps, search]);

  const columns = [
    { header: "Name", accessor: (a: Application) => (
      <div className="flex items-center gap-2"><AppWindow size={14} className="text-blue-400" /><span className="font-medium text-panel-text">{a.name}</span></div>
    )},
    { header: "Type", accessor: (a: Application) => (
      <span className={`font-medium ${typeColors[a.app_type] || "text-panel-muted"}`}>
        {typeLabels[a.app_type] || a.app_type}
        {a.framework && <span className="text-xs text-panel-muted/70 ml-1">({a.framework})</span>}
      </span>
    )},
    { header: "Domain", accessor: (a: Application) => <span className="text-panel-muted">{a.domain}</span> },
    { header: "User", accessor: (a: Application) => <span className="text-panel-muted text-xs">{a.user}</span> },
    { header: "Status", accessor: (a: Application) => <StatusBadge status={a.status === "running" ? "active" : a.status} /> },
    { header: "Port", accessor: (a: Application) => (
      a.port ? <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">:{a.port}</code> : <span className="text-panel-muted/50 text-xs">—</span>
    )},
    { header: "Actions", accessor: (a: Application) => (
      <div className="flex items-center gap-1">
        {a.app_type !== "static" && (a.status === "stopped" ? (
          <button onClick={() => handleAction(a.name, "start")} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors" title="Start"><Play size={14} /></button>
        ) : (
          <button onClick={() => handleAction(a.name, "stop")} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors" title="Stop"><Square size={14} /></button>
        ))}
        {a.app_type !== "static" && (
          <button onClick={() => handleAction(a.name, "restart")} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors" title="Restart"><RotateCw size={14} /></button>
        )}
        {a.app_type !== "static" && (
          <button onClick={() => openPackageInstall(a)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-emerald-400 transition-colors" title="Install packages"><Package size={14} /></button>
        )}
        <button onClick={() => openBackups(a)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-purple-400 transition-colors" title="Backup / Restore"><Archive size={14} /></button>
        <button onClick={() => setTransferApp(a)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-cyan-400 transition-colors" title="Transfer to user"><ArrowRightLeft size={14} /></button>
        <button onClick={() => handleDelete(a.name)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors" title="Delete"><Trash2 size={14} /></button>
      </div>
    )},
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Applications</h1>
          <p className="text-panel-muted text-sm mt-1">Deploy, backup, restore and transfer your apps</p>
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
          <div className="p-8"><div className="space-y-3">{[1, 2, 3, 4, 5].map((i) => <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />)}</div></div>
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

      {/* ---------- Deploy Modal ---------- */}
      <Modal isOpen={showCreate} onClose={() => { setShowCreate(false); resetForm(); }} title="Deploy Application" size="lg">
        <form onSubmit={handleCreate} className="space-y-4 max-h-[70vh] overflow-y-auto pr-1">
          {/* Framework preset */}
          <div>
            <label className={labelClass}>Framework preset</label>
            <select value={form.framework} onChange={(e) => applyPreset(e.target.value)} className={selectClass}>
              {(Object.entries(presets) as [string, Preset][]).map(([k, v]) => (
                <option key={k} value={k}>{v.label}</option>
              ))}
            </select>
            <p className="text-xs text-panel-muted/70 mt-1">
              Auto-fills build/start commands from the server-side catalogue. You can still edit them below.
            </p>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>App Name *</label>
              <input type="text" required placeholder="my-app" value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })} className={inputClass} />
              <p className="text-xs text-panel-muted/70 mt-1">lowercase, a-z 0-9 and dashes, 2-32 chars</p>
            </div>
            <div>
              <label className={labelClass}>Domain *</label>
              {availableDomains.length === 0 ? (
                <p className="text-xs text-amber-400">No domains available. Create a domain first.</p>
              ) : (
                <select
                  required
                  value={selectedDomains[0] || ""}
                  onChange={(e) => selectDomain(e.target.value)}
                  className={selectClass}
                >
                  <option value="">Select a domain...</option>
                  {availableDomains.map((d) => (
                    <option key={d.id} value={d.domain}>
                      {d.domain} ({d.user})
                    </option>
                  ))}
                </select>
              )}
              {selectedDomains.length > 0 && (
                <p className="text-xs text-blue-400 mt-1 font-mono">
                  {selectedDomains[0]}{form.path && form.path !== "/" ? form.path : ""}
                </p>
              )}
            </div>
          </div>

          <div>
            <label className={labelClass}>Path</label>
            <input
              type="text"
              placeholder="/"
              value={form.path}
              onChange={(e) => {
                let v = e.target.value.trim();
                if (v && !v.startsWith("/")) v = "/" + v;
                setForm({ ...form, path: v });
              }}
              className={inputClass}
            />
            <p className="text-xs text-panel-muted/70 mt-1">
              URL path where the app is mounted under the domain. Leave as <code className="font-mono">/</code> to serve at the domain root, or set e.g. <code className="font-mono">/app</code>.
            </p>
          </div>

          <div>
            <label className={labelClass}>Install path</label>
            <input
              type="text"
              placeholder={`/home/${form.user || "ubuntu"}/apps/${form.name || "my-app"}`}
              value={form.install_path}
              onChange={(e) => setForm({ ...form, install_path: e.target.value })}
              className={inputClass}
            />
            <p className="text-xs text-panel-muted/70 mt-1">
              Absolute filesystem path on the server where the project files will be stored. Leave blank to use the default <code className="font-mono">/home/{"{user}"}/apps/{"{name}"}</code>.
            </p>
          </div>

          <div className="grid grid-cols-3 gap-4">
            <div>
              <label className={labelClass}>Deploy method *</label>
              <select value={form.deploy_method} onChange={(e) => setForm({ ...form, deploy_method: e.target.value as DeployForm["deploy_method"] })} className={selectClass}>
                <option value="scaffold">Fresh install (scaffold demo)</option>
                <option value="git">Git repository</option>
                <option value="local">Local (files already on disk)</option>
              </select>
            </div>
            <div>
              <label className={labelClass}>System user *</label>
              <input type="text" required placeholder="ubuntu" value={form.user}
                onChange={(e) => setForm({ ...form, user: e.target.value })} className={inputClass} />
              <p className="text-xs text-panel-muted/70 mt-1">Created if missing</p>
            </div>
            <div>
              <label className={labelClass}>Port</label>
              <div className="flex items-center gap-2">
                <input type="number" min={0} max={65535} disabled={form.auto_port} value={form.auto_port ? 0 : form.port}
                  onChange={(e) => setForm({ ...form, port: parseInt(e.target.value) || 0 })}
                  className={inputClass + (form.auto_port ? " opacity-50" : "")} placeholder="auto" />
              </div>
              <label className="inline-flex items-center gap-2 mt-1 text-xs text-panel-muted cursor-pointer">
                <input type="checkbox" checked={form.auto_port} onChange={(e) => setForm({ ...form, auto_port: e.target.checked })} />
                Auto-allocate
              </label>
            </div>
          </div>

          {form.deploy_method === "git" && (
            <div className="grid grid-cols-3 gap-4">
              <div className="col-span-2">
                <label className={labelClass}>Git URL</label>
                <input type="text" placeholder="https://github.com/user/repo.git" value={form.git_url}
                  onChange={(e) => setForm({ ...form, git_url: e.target.value })} className={inputClass} />
              </div>
              <div>
                <label className={labelClass}>Branch</label>
                <input type="text" placeholder="main" value={form.git_branch}
                  onChange={(e) => setForm({ ...form, git_branch: e.target.value })} className={inputClass} />
              </div>
              <div className="col-span-3">
                <label className={labelClass}>Git token (for private repos)</label>
                <input type="password" placeholder="ghp_..." value={form.git_token}
                  onChange={(e) => setForm({ ...form, git_token: e.target.value })} className={inputClass} />
              </div>
            </div>
          )}

          {(() => {
            const hint = buildCmdHint(form.app_type);
            const required = hint !== "";
            const hasError = buildCmdError && required && !form.build_cmd.trim();
            return (
              <div>
                <label className={labelClass}>
                  Build command
                  {required && <span className="text-red-400"> *</span>}
                </label>
                <input
                  type="text"
                  placeholder={hint || "npm install && npm run build"}
                  value={form.build_cmd}
                  onChange={(e) => {
                    setForm({ ...form, build_cmd: e.target.value });
                    if (e.target.value.trim()) setBuildCmdError("");
                  }}
                  className={`${inputClass} ${hasError ? "!border-red-500 !ring-red-500/30" : ""}`}
                />
                {hasError ? (
                  <p className="text-xs text-red-400 mt-1">{buildCmdError}</p>
                ) : required ? (
                  <p className="text-xs text-panel-muted/70 mt-1">
                    Required for {form.app_type} apps — example: <code className="font-mono">{hint}</code>. Pick a Framework preset above to auto-fill.
                  </p>
                ) : (
                  <p className="text-xs text-panel-muted/70 mt-1">
                    Optional for this app type. Leave blank to skip.
                  </p>
                )}
              </div>
            );
          })()}
          <div>
            <label className={labelClass}>Start command</label>
            <input type="text" placeholder="node server.js" value={form.start_cmd}
              onChange={(e) => setForm({ ...form, start_cmd: e.target.value })} className={inputClass} />
            <p className="text-xs text-panel-muted/70 mt-1">Use <code className="font-mono">${`{PORT}`}</code> to reference the allocated port.</p>
          </div>

          {(form.build_cmd.trim() || form.start_cmd.trim()) && (
            <div className="rounded-lg border border-panel-border bg-panel-bg/50 p-3">
              <p className="text-xs font-medium text-panel-muted mb-2">Will run on deploy</p>
              {form.build_cmd.trim() && (
                <div className="mb-2">
                  <p className="text-[10px] uppercase text-panel-muted/60 mb-1">Build</p>
                  <code className="text-xs font-mono text-green-400 break-all">$ {form.build_cmd}</code>
                </div>
              )}
              {form.start_cmd.trim() && (
                <div>
                  <p className="text-[10px] uppercase text-panel-muted/60 mb-1">Start (systemd ExecStart)</p>
                  <code className="text-xs font-mono text-blue-400 break-all">
                    $ {form.start_cmd.replace(/\$\{PORT\}/g, form.auto_port ? "<auto>" : String(form.port || "<auto>"))}
                  </code>
                </div>
              )}
            </div>
          )}

          {/* Environment variables */}
          <div>
            <div className="flex items-center justify-between mb-1">
              <label className={labelClass}>Environment variables</label>
              <button type="button" onClick={() => setEnvRows([...envRows, { key: "", value: "" }])}
                className="text-xs text-blue-400 hover:text-blue-300 flex items-center gap-1">
                <Plus size={12} /> Add
              </button>
            </div>
            {envRows.length === 0 && <p className="text-xs text-panel-muted/70">No env vars. Click "Add" to set one.</p>}
            <div className="space-y-2">
              {envRows.map((row, i) => (
                <div key={i} className="flex gap-2">
                  <input type="text" placeholder="KEY" value={row.key}
                    onChange={(e) => { const r = [...envRows]; r[i].key = e.target.value; setEnvRows(r); }}
                    className={inputClass + " flex-1"} />
                  <input type="text" placeholder="value" value={row.value}
                    onChange={(e) => { const r = [...envRows]; r[i].value = e.target.value; setEnvRows(r); }}
                    className={inputClass + " flex-1"} />
                  <button type="button" onClick={() => setEnvRows(envRows.filter((_, j) => j !== i))}
                    className="p-2 text-panel-muted hover:text-red-400"><X size={14} /></button>
                </div>
              ))}
            </div>
          </div>

          {/* Advanced */}
          <div>
            <button type="button" onClick={() => setShowAdvanced(!showAdvanced)}
              className="flex items-center gap-1 text-sm text-panel-muted hover:text-panel-text">
              {showAdvanced ? <ChevronUp size={14} /> : <ChevronDown size={14} />} Advanced options
            </button>
            {showAdvanced && (
              <div className="mt-3 space-y-3 border-l-2 border-panel-border pl-4">
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className={labelClass}>App type (override)</label>
                    <select value={form.app_type} onChange={(e) => setForm({ ...form, app_type: e.target.value })} className={selectClass}>
                      <option value="node">Node.js</option>
                      <option value="python">Python</option>
                      <option value="ruby">Ruby</option>
                      <option value="go">Go</option>
                      <option value="static">Static</option>
                      <option value="php">PHP</option>
                    </select>
                  </div>
                  <div>
                    <label className={labelClass}>Runtime version</label>
                    <input type="text" placeholder="e.g. node-20, python-3.12" value={form.runtime_version}
                      onChange={(e) => setForm({ ...form, runtime_version: e.target.value })} className={inputClass} />
                  </div>
                </div>
                <div>
                  <label className={labelClass}>Health check path</label>
                  <input type="text" placeholder="/" value={form.health_check_path}
                    onChange={(e) => setForm({ ...form, health_check_path: e.target.value })} className={inputClass} />
                </div>
              </div>
            )}
          </div>

          <div className="flex justify-end gap-3 pt-2 border-t border-panel-border">
            <button type="button" onClick={() => { setShowCreate(false); resetForm(); }}
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

      {/* ---------- Backup / Restore Modal ---------- */}
      <Modal isOpen={!!backupApp} onClose={() => { setBackupApp(null); setBackups([]); }}
        title={backupApp ? `Backups — ${backupApp.name}` : ""} size="lg">
        {backupApp && (
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <p className="text-sm text-panel-muted">Snapshots are stored at <code className="font-mono">/home/{backupApp.user}/backups/apps/{backupApp.name}/</code></p>
              <Button onClick={runBackup} className="flex items-center gap-2 px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm">
                <Archive size={14} /> Create backup
              </Button>
            </div>
            {backups.length === 0 ? (
              <div className="text-center py-8 text-panel-muted text-sm">No backups yet.</div>
            ) : (
              <div className="space-y-2 max-h-80 overflow-y-auto">
                {backups.map((b) => (
                  <div key={b.file} className="flex items-center justify-between p-3 bg-panel-bg border border-panel-border rounded-lg">
                    <div className="min-w-0 flex-1">
                      <p className="font-mono text-xs text-panel-text truncate">{b.file.split("/").pop()}</p>
                      <p className="text-xs text-panel-muted">{(b.size / 1024 / 1024).toFixed(2)} MB · {new Date(b.created_at).toLocaleString()}</p>
                    </div>
                    <button onClick={() => runRestore(b.file)}
                      className="ml-3 flex items-center gap-1 px-3 py-1.5 bg-purple-600 hover:bg-purple-700 text-white rounded text-xs">
                      <Upload size={12} /> Restore
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </Modal>

      {/* ---------- Transfer Modal ---------- */}
      <Modal isOpen={!!transferApp} onClose={() => { setTransferApp(null); setTransferUser(""); }}
        title={transferApp ? `Transfer ${transferApp.name}` : ""} size="md">
        {transferApp && (
          <div className="space-y-4">
            <p className="text-sm text-panel-muted">
              Move this app to a different system user on this server. A fresh backup is taken
              first, then the service is stopped, files are moved to the new user's home, and
              the systemd unit is recreated under the new owner. Nginx config is regenerated.
            </p>
            <div>
              <label className={labelClass}>Target system user *</label>
              <input type="text" placeholder="ubuntu" value={transferUser}
                onChange={(e) => setTransferUser(e.target.value)} className={inputClass} />
              <p className="text-xs text-panel-muted/70 mt-1">Will be created if it doesn't exist. Current: <code className="font-mono">{transferApp.user}</code></p>
            </div>
            <div className="flex justify-end gap-3 pt-2">
              <button onClick={() => { setTransferApp(null); setTransferUser(""); }}
                className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg">Cancel</button>
              <button onClick={runTransfer} disabled={!transferUser.trim()}
                className="px-4 py-2 text-sm bg-cyan-600 hover:bg-cyan-700 text-white rounded-lg font-medium disabled:opacity-50">
                Transfer
              </button>
            </div>
          </div>
        )}
      </Modal>

      {/* ---------- Package Install Modal ---------- */}
      <Modal isOpen={!!pkgApp} onClose={() => { if (!pkgRunning) setPkgApp(null); }}
        title={pkgApp ? `Install packages — ${pkgApp.name}` : ""} size="lg">
        {pkgApp && (
          <div className="space-y-4">
            <p className="text-sm text-panel-muted">
              Runs a shell command as <code className="font-mono text-panel-text">{pkgApp.user}</code> inside
              the app directory. Leave the field empty to re-run the app's original build command.
              The service is not restarted — use Restart afterwards if the install changed runtime code.
            </p>
            <div>
              <label className={labelClass}>Command (optional)</label>
              <input type="text"
                placeholder="e.g. npm install lodash@4 or pip install requests"
                value={pkgCmd}
                disabled={pkgRunning}
                onChange={(e) => setPkgCmd(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter" && !pkgRunning) runPackageInstall(); }}
                className={inputClass} />
              <div className="flex gap-2 mt-2 flex-wrap">
                {pkgApp.app_type === "node" && (
                  <>
                    <button type="button" onClick={() => setPkgCmd("npm install")} className="text-xs px-2 py-1 rounded bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border">npm install</button>
                    <button type="button" onClick={() => setPkgCmd("npm ci --omit=dev")} className="text-xs px-2 py-1 rounded bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border">npm ci --omit=dev</button>
                  </>
                )}
                {pkgApp.app_type === "python" && (
                  <>
                    <button type="button" onClick={() => setPkgCmd("./venv/bin/pip install -r requirements.txt")} className="text-xs px-2 py-1 rounded bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border">pip install -r requirements.txt</button>
                  </>
                )}
                {pkgApp.app_type === "ruby" && (
                  <>
                    <button type="button" onClick={() => setPkgCmd("bundle install")} className="text-xs px-2 py-1 rounded bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border">bundle install</button>
                  </>
                )}
              </div>
            </div>

            {(pkgRunning || pkgOutput) && (
              <div>
                <div className="flex items-center justify-between mb-1">
                  <label className={labelClass}>Output</label>
                  {pkgSuccess !== null && !pkgRunning && (
                    <span className={`text-xs ${pkgSuccess ? "text-green-400" : "text-red-400"}`}>
                      {pkgSuccess ? "✓ success" : "✗ failed"}
                      {pkgDurationMs !== null ? ` · ${(pkgDurationMs / 1000).toFixed(1)}s` : ""}
                    </span>
                  )}
                </div>
                <pre className="bg-[#0b0b12] border border-panel-border rounded-lg p-3 text-xs font-mono text-panel-text whitespace-pre-wrap overflow-auto max-h-[360px] min-h-[120px]">
                  {pkgRunning ? "Running…" : pkgOutput}
                </pre>
              </div>
            )}

            <div className="flex justify-end gap-3 pt-2">
              <button onClick={() => setPkgApp(null)} disabled={pkgRunning}
                className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg disabled:opacity-50">
                Close
              </button>
              <button onClick={runPackageInstall} disabled={pkgRunning}
                className="px-4 py-2 text-sm bg-emerald-600 hover:bg-emerald-700 text-white rounded-lg font-medium disabled:opacity-50 flex items-center gap-2">
                {pkgRunning && <RotateCw size={14} className="animate-spin" />}
                {pkgRunning ? "Installing…" : "Run"}
              </button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
