import { useState, useEffect, useMemo } from "react";
import { Card, Button, Table, StatusBadge, Modal, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import { useAuthStore } from "@/store/auth";
import toast from "react-hot-toast";
import {
  AppWindow, Plus, RefreshCw, Search, Trash2, Play, Square, RotateCw,
  Archive, Upload, ArrowRightLeft, ChevronDown, ChevronUp, X, Package,
  Pencil, FileText, ExternalLink, GitBranch, HelpCircle, Check, Copy, Webhook,
} from "lucide-react";
import { BuildErrorModal, tryExtractBuildError, type BuildErrorInfo } from "@/components/BuildErrorModal";

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
  install_cmd?: string;
  build_cmd?: string;
  start_cmd?: string;
  health_check_path?: string;
  git_url?: string;
  git_branch?: string;
  repo_subpath?: string;
  auto_deploy?: boolean;
  webhook_id?: string;
  env_vars?: Record<string, string>;
  install_path?: string;
  path?: string;
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
// endpoint fails. When a preset is chosen, the form auto-fills the install /
// build / start / port fields, but the user can still override any of them.
type Preset = {
  framework?: string;
  label: string;
  app_type: string;
  install_cmd: string;
  build_cmd: string;
  start_cmd: string;
  default_port: number;
  is_static?: boolean;
};
const FALLBACK_PRESETS: Record<string, Preset> = {
  "node-express": {
    label: "Node.js (Express / vanilla)",
    app_type: "node",
    install_cmd: "npm install --omit=dev --no-audit --no-fund --loglevel=error",
    build_cmd: "",
    start_cmd: "node server.js",
    default_port: 3000,
  },
  "nextjs": {
    label: "Next.js 14 (App Router)",
    app_type: "node",
    install_cmd: "npm install --no-audit --no-fund --loglevel=error",
    build_cmd: "npm run build",
    start_cmd: "npx next start -p ${PORT}",
    default_port: 3000,
  },
  "react-vite": {
    label: "React + Vite (static)",
    app_type: "static",
    install_cmd: "npm install --no-audit --no-fund --loglevel=error",
    build_cmd: "npm run build",
    start_cmd: "",
    default_port: 0,
    is_static: true,
  },
  "python-flask": {
    label: "Python (Flask + gunicorn)",
    app_type: "python",
    install_cmd: "python3 -m venv venv && ./venv/bin/pip install --quiet flask gunicorn",
    build_cmd: "",
    start_cmd: "./venv/bin/gunicorn --bind 0.0.0.0:${PORT} --workers 2 app:app",
    default_port: 5000,
  },
  "ruby-sinatra": {
    label: "Ruby (Sinatra)",
    app_type: "ruby",
    install_cmd: "bundle config set --local path 'vendor/bundle' && bundle install --quiet",
    build_cmd: "",
    start_cmd: "bundle exec ruby app.rb -o 0.0.0.0 -p ${PORT}",
    default_port: 4567,
  },
  "go-vanilla": {
    label: "Go (net/http, stdlib)",
    app_type: "go",
    install_cmd: "go mod download",
    build_cmd: "go build -o app .",
    start_cmd: "./app",
    default_port: 8080,
  },
  "go-gin": {
    label: "Go (Gin)",
    app_type: "go",
    install_cmd: "go mod download",
    build_cmd: "go build -o app .",
    start_cmd: "./app",
    default_port: 8080,
  },
  "go-fiber": {
    label: "Go (Fiber)",
    app_type: "go",
    install_cmd: "go mod download",
    build_cmd: "go build -o app .",
    start_cmd: "./app",
    default_port: 8080,
  },
  "go-echo": {
    label: "Go (Echo)",
    app_type: "go",
    install_cmd: "go mod download",
    build_cmd: "go build -o app .",
    start_cmd: "./app",
    default_port: 8080,
  },
  "go-chi": {
    label: "Go (Chi router)",
    app_type: "go",
    install_cmd: "go mod download",
    build_cmd: "go build -o app .",
    start_cmd: "./app",
    default_port: 8080,
  },
  "custom": {
    label: "Custom (fill everything manually)",
    app_type: "node",
    install_cmd: "",
    build_cmd: "",
    start_cmd: "",
    default_port: 0,
  },
};

// installCmdHint mirrors the backend's missingBuildCmdHint so the inline
// error message we show matches what the server enforces.
const installCmdHint = (appType: string): string => {
  switch (appType) {
    case "node":
    case "nodejs":
      return "npm install --omit=dev";
    case "python":
      return "python3 -m venv venv && ./venv/bin/pip install -r requirements.txt";
    case "ruby":
      return "bundle config set --local path 'vendor/bundle' && bundle install";
    case "go":
      return "go mod download";
    case "rust":
      return "cargo fetch";
    default:
      return "";
  }
};

const buildCmdHint = (appType: string): string => {
  switch (appType) {
    case "node":
    case "nodejs":
      return "npm run build";
    case "go":
      return "go build -o app .";
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
  repo_subpath: string;
  auto_deploy: boolean;
  install_cmd: string;
  build_cmd: string;
  start_cmd: string;
  runtime_version: string;
  health_check_path: string;
  min_instances: number;
  max_instances: number;
};

const emptyForm: DeployForm = {
  name: "", domain: "", path: "/", framework: "node-express", app_type: "node",
  deploy_method: "scaffold", user: "", install_path: "", port: 0, auto_port: true,
  git_url: "", git_branch: "main", git_token: "", repo_subpath: "", auto_deploy: false,
  install_cmd: "", build_cmd: "", start_cmd: "", runtime_version: "", health_check_path: "/",
  min_instances: 1, max_instances: 1,
};

function appTypeToRuntimeKey(appType: string): string {
  switch (appType) {
    case "node": case "nodejs": return "node";
    case "python": return "python";
    case "ruby": return "ruby";
    case "go": return "go";
    default: return "";
  }
}

interface DomainOption { id: string; domain: string; user?: string }

function detectGitProvider(url: string): "github" | "gitlab" | "bitbucket" | "generic" {
  const u = url.toLowerCase();
  if (u.includes("github.com")) return "github";
  if (u.includes("gitlab.com") || u.includes("/gitlab/")) return "gitlab";
  if (u.includes("bitbucket.org")) return "bitbucket";
  return "generic";
}

function tokenGenUrl(provider: "github" | "gitlab" | "bitbucket" | "generic"): string {
  switch (provider) {
    case "github":
      return "https://github.com/settings/tokens/new?scopes=repo&description=ServerPanel%20deploy%20token";
    case "gitlab":
      return "https://gitlab.com/-/profile/personal_access_tokens";
    case "bitbucket":
      return "https://bitbucket.org/account/settings/app-passwords/";
    default:
      return "";
  }
}

type RuntimeVersionInfo = { version: string; installed?: boolean; active?: boolean };

export default function AppsPage() {
  // cPanel caller is always themselves — the backend auto-scopes all app
  // operations to the caller's tenant via callerCtx(role, tenantID), so
  // we auto-fill the Linux user field from the auth store and hide the
  // vendor picker the WHM page shows. No vendor_owner branches here.
  const authUser = useAuthStore((s) => s.user);
  const callerUsername = authUser?.username || "";

  const [apps, setApps] = useState<Application[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [creating, setCreating] = useState(false);
  const [buildError, setBuildError] = useState<BuildErrorInfo | null>(null);
  const [form, setForm] = useState<DeployForm>(emptyForm);
  const [envRows, setEnvRows] = useState<{ key: string; value: string }[]>([]);
  const [availableDomains, setAvailableDomains] = useState<DomainOption[]>([]);
  const [selectedDomain, setSelectedDomain] = useState<string>("");
  const [presets, setPresets] = useState<Record<string, Preset>>(FALLBACK_PRESETS);
  const [installCmdError, setInstallCmdError] = useState<string>("");
  // cPanel has no /software/runtimes endpoint — leave this empty so the
  // Advanced → Runtime Version dropdown shows just "System default".
  const [runtimes] = useState<Record<string, RuntimeVersionInfo[]>>({});
  const [webhookReveal, setWebhookReveal] = useState<{
    appName: string; url: string; secret: string;
  } | null>(null);

  // Backup/Restore/Transfer/Package modal state
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

  // Logs viewer
  const [logsApp, setLogsApp] = useState<Application | null>(null);
  const [logsLines, setLogsLines] = useState<string[]>([]);
  const [logsLoading, setLogsLoading] = useState(false);
  const [logsAuto, setLogsAuto] = useState(true);

  // Edit Application modal
  const [editApp, setEditApp] = useState<Application | null>(null);
  const [editForm, setEditForm] = useState<{
    domain: string; path: string; install_cmd: string; build_cmd: string; start_cmd: string;
    health_check_path: string; git_url: string; git_branch: string;
    env_rows: { key: string; value: string }[]; restart: boolean;
  }>({
    domain: "", path: "/", install_cmd: "", build_cmd: "", start_cmd: "",
    health_check_path: "/", git_url: "", git_branch: "main",
    env_rows: [], restart: true,
  });
  const [editSaving, setEditSaving] = useState(false);

  const [redeploying, setRedeploying] = useState<string | null>(null);
  const [showTokenHelp, setShowTokenHelp] = useState(false);

  useEffect(() => { fetchApps(); fetchDomains(); fetchPresets(); }, []);

  // Lock the form's Linux user to the authenticated caller. Re-runs when
  // the identity changes (e.g. after login / token refresh).
  useEffect(() => {
    if (callerUsername) {
      setForm((f) => (f.user === callerUsername ? f : { ...f, user: callerUsername }));
    }
  }, [callerUsername]);

  useEffect(() => {
    if (!logsApp || !logsAuto) return;
    const t = setInterval(() => fetchLogs(logsApp.name, false), 3000);
    return () => clearInterval(t);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [logsApp, logsAuto]);

  const fetchPresets = async () => {
    try {
      const res = await api.get("/apps/presets");
      const data = res.data?.data;
      if (data && typeof data === "object" && Object.keys(data).length > 0) {
        setPresets({
          ...(data as Record<string, Preset>),
          custom: {
            label: "Custom (fill everything manually)",
            app_type: "node",
            install_cmd: "",
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

  const applyPreset = (framework: string) => {
    const p = presets[framework];
    if (!p) return;
    setForm((prev) => ({
      ...prev,
      framework,
      app_type: p.app_type,
      install_cmd: p.install_cmd || "",
      build_cmd: p.build_cmd || "",
      start_cmd: p.start_cmd || "",
      port: p.default_port || prev.port,
      auto_port: p.is_static ? true : prev.auto_port,
    }));
    setInstallCmdError("");
  };

  const resetForm = () => {
    setForm({ ...emptyForm, user: callerUsername });
    setEnvRows([]);
    setShowAdvanced(false);
    setSelectedDomain("");
  };

  const selectDomain = (dom: string) => {
    setSelectedDomain(dom);
    setForm((f) => ({ ...f, domain: dom }));
  };

  const filteredDomains = useMemo(() => availableDomains, [availableDomains]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    const effectiveUser = callerUsername || form.user;
    if (!form.name || !selectedDomain || !effectiveUser) {
      toast.error("Name and domain are required");
      return;
    }
    if (!/^[a-z][a-z0-9-]{1,31}$/.test(form.name)) {
      toast.error("App name must be lowercase, start with a letter, and use only a-z 0-9 and '-'");
      return;
    }
    const iHint = installCmdHint(form.app_type);
    if (iHint && !form.install_cmd.trim() && !form.build_cmd.trim()) {
      const msg = `${form.app_type} apps require an install command (e.g. "${iHint}")`;
      setInstallCmdError(msg);
      toast.error(msg);
      return;
    }
    setInstallCmdError("");
    setCreating(true);
    const env_vars: Record<string, string> = {};
    envRows.forEach((r) => { if (r.key.trim()) env_vars[r.key.trim()] = r.value; });
    const payload: Record<string, unknown> = {
      name: form.name,
      domain: selectedDomain,
      path: form.path || "/",
      framework: form.framework === "custom" ? "" : form.framework,
      app_type: form.app_type,
      deploy_method: form.deploy_method,
      user: effectiveUser,
      install_path: form.install_path.trim(),
      port: form.auto_port ? 0 : form.port,
      git_url: form.git_url,
      git_branch: form.git_branch,
      git_token: form.git_token,
      repo_subpath: form.repo_subpath.trim(),
      auto_deploy: form.auto_deploy,
      install_cmd: form.install_cmd,
      build_cmd: form.build_cmd,
      start_cmd: form.start_cmd,
      runtime_version: form.runtime_version,
      health_check_path: form.health_check_path,
      min_instances: form.min_instances,
      max_instances: form.max_instances,
      env_vars,
    };
    try {
      const res = await api.post("/apps/deploy", payload);
      toast.success(`Application ${form.name} deployed`);
      const webhookId = res.data?.data?.webhook_id;
      const secretOnce = res.data?.webhook_secret_once;
      if (webhookId && secretOnce) {
        setWebhookReveal({
          appName: form.name,
          url: `${window.location.origin}/api/v1/webhooks/github/${webhookId}`,
          secret: secretOnce,
        });
      }
      setShowCreate(false);
      resetForm();
      fetchApps();
    } catch (err) {
      const be = tryExtractBuildError(err);
      if (be) {
        setBuildError(be);
        toast.error(`${be.stage} failed: ${be.summary}`);
      } else {
        const e = err as { response?: { data?: { error?: { message?: string } } } };
        toast.error(e?.response?.data?.error?.message || "Failed to deploy application");
      }
    } finally { setCreating(false); }
  };

  const handleAction = async (name: string, action: string) => {
    try { await api.post(`/apps/${name}/${action}`); toast.success(`App ${action} successful`); fetchApps(); }
    catch { toast.error(`Failed to ${action} app`); }
  };
  const handleDelete = async (name: string) => {
    if (!await confirmAction({ title: "Delete?", description: `Delete app "${name}"? Code, service and nginx config will be removed.`, danger: true, confirmLabel: "Delete" })) return;
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
    if (!await confirmAction({ title: "Restore?", description: `Restore ${backupApp.name} from this backup? Current state will be snapshotted first.`, confirmLabel: "Restore" })) return;
    try {
      await api.post(`/apps/${backupApp.name}/restore`, { file });
      toast.success("Restore completed");
      fetchApps();
    } catch (err) {
      const e = err as { response?: { data?: { error?: { message?: string } } } };
      toast.error(e?.response?.data?.error?.message || "Restore failed");
    }
  };

  // --- Rollback to previous deployment ---
  const handleRollback = async (app: Application) => {
    if (!await confirmAction({ title: "Rollback?", description: `Roll "${app.name}" back to its previous deployment? The current code will be snapshotted first.`, confirmLabel: "Rollback" })) return;
    try {
      await api.post(`/apps/${app.name}/rollback`, {});
      toast.success(`${app.name} rolled back`);
      fetchApps();
    } catch (err) {
      const e = err as { response?: { data?: { error?: { message?: string } } } };
      toast.error(e?.response?.data?.error?.message || "Rollback failed");
    }
  };

  // --- Install packages ---
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

  // --- Logs viewer ---
  const openLogs = async (app: Application) => {
    setLogsApp(app);
    setLogsLines([]);
    setLogsAuto(true);
    await fetchLogs(app.name, true);
  };
  const fetchLogs = async (name: string, showSpinner: boolean) => {
    if (showSpinner) setLogsLoading(true);
    try {
      const res = await api.get(`/apps/${name}/logs?lines=300`);
      const data = res.data?.data;
      if (Array.isArray(data)) setLogsLines(data);
      else if (Array.isArray(data?.lines)) setLogsLines(data.lines);
    } catch {
      setLogsLines((prev) => (prev.length === 0 ? ["(failed to load logs)"] : prev));
    } finally {
      if (showSpinner) setLogsLoading(false);
    }
  };

  // --- Edit application ---
  const openEdit = (app: Application) => {
    setEditApp(app);
    setEditForm({
      domain: app.domain || "",
      path: app.path || "/",
      install_cmd: app.install_cmd || "",
      build_cmd: app.build_cmd || "",
      start_cmd: app.start_cmd || "",
      health_check_path: app.health_check_path || "/",
      git_url: app.git_url || "",
      git_branch: app.git_branch || "main",
      env_rows: Object.entries(app.env_vars || {}).map(([k, v]) => ({ key: k, value: v })),
      restart: true,
    });
  };
  const saveEdit = async () => {
    if (!editApp) return;
    setEditSaving(true);
    try {
      const env_vars: Record<string, string> = {};
      editForm.env_rows.forEach((r) => {
        const k = r.key.trim();
        if (k) env_vars[k] = r.value;
      });
      await api.put(`/apps/${editApp.name}`, {
        domain: editForm.domain.trim(),
        path: editForm.path,
        install_cmd: editForm.install_cmd,
        build_cmd: editForm.build_cmd,
        start_cmd: editForm.start_cmd,
        health_check_path: editForm.health_check_path,
        git_url: editForm.git_url,
        git_branch: editForm.git_branch,
        env_vars,
        restart: editForm.restart,
      });
      toast.success(`${editApp.name} updated${editForm.restart ? " and restarted" : ""}`);
      setEditApp(null);
      fetchApps();
    } catch (err) {
      const e = err as { response?: { data?: { error?: { message?: string } } } };
      toast.error(e?.response?.data?.error?.message || "Failed to update application");
    } finally {
      setEditSaving(false);
    }
  };

  // --- Redeploy ---
  const handleRedeploy = async (app: Application) => {
    if (!await confirmAction({ title: "Redeploy?", description: `Redeploy "${app.name}"?\n\nThis will pull the latest code (git apps), re-run the build command, regenerate the PM2 config, and restart the service.`, confirmLabel: "Redeploy" })) return;
    setRedeploying(app.name);
    try {
      await api.post(`/apps/${app.name}/redeploy`);
      toast.success(`${app.name} redeployed`);
      fetchApps();
    } catch (err) {
      const e = err as { response?: { data?: { error?: { message?: string } } } };
      toast.error(e?.response?.data?.error?.message || "Redeploy failed");
    } finally {
      setRedeploying(null);
    }
  };

  const openUrl = (app: Application) => {
    if (!app.domain) {
      toast.error("App has no domain configured");
      return;
    }
    window.open(`https://${app.domain}${app.path && app.path !== "/" ? app.path : ""}`, "_blank", "noopener,noreferrer");
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
    { header: "Status", accessor: (a: Application) => <StatusBadge status={a.status === "running" ? "active" : a.status} /> },
    { header: "Port", accessor: (a: Application) => (
      a.port ? <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">:{a.port}</code> : <span className="text-panel-muted/50 text-xs">—</span>
    )},
    { header: "Deployed", accessor: (a: Application) => (
      <span className="text-panel-muted text-xs">{a.created_at ? new Date(a.created_at).toLocaleDateString() : "—"}</span>
    )},
    { header: "Actions", accessor: (a: Application) => (
      <div className="flex items-center gap-0.5">
        {a.domain && (
          <button onClick={() => openUrl(a)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors" title="Open in browser"><ExternalLink size={14} /></button>
        )}
        {a.app_type !== "static" && (a.status === "stopped" ? (
          <button onClick={() => handleAction(a.name, "start")} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors" title="Start"><Play size={14} /></button>
        ) : (
          <button onClick={() => handleAction(a.name, "stop")} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors" title="Stop"><Square size={14} /></button>
        ))}
        {a.app_type !== "static" && (
          <button onClick={() => handleAction(a.name, "restart")} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors" title="Restart service"><RotateCw size={14} /></button>
        )}
        <button onClick={() => handleRedeploy(a)} disabled={redeploying === a.name} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-orange-400 transition-colors disabled:opacity-50" title="Redeploy (git pull + rebuild)">
          <GitBranch size={14} className={redeploying === a.name ? "animate-pulse" : ""} />
        </button>
        <button onClick={() => handleRollback(a)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors" title="Rollback to previous deployment"><RotateCw size={14} className="-scale-x-100" /></button>
        <button onClick={() => openLogs(a)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-sky-400 transition-colors" title="View logs"><FileText size={14} /></button>
        <button onClick={() => openEdit(a)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-amber-400 transition-colors" title="Edit application"><Pencil size={14} /></button>
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

          {/* Domain dropdown — cpanel caller is auto-scoped to their own
              tenant so the list already contains only their domains. */}
          <div>
            <label className={labelClass}>Domain *</label>
            {filteredDomains.length === 0 ? (
              <div className="text-xs text-amber-400">
                You don't own any domains yet. Add one from the Domains page first.
              </div>
            ) : (
              <select
                required
                value={selectedDomain}
                onChange={(e) => selectDomain(e.target.value)}
                className={selectClass}
              >
                <option value="">Select a domain...</option>
                {filteredDomains.map((d) => (
                  <option key={d.id} value={d.domain}>{d.domain}</option>
                ))}
              </select>
            )}
            {selectedDomain && (
              <p className="text-xs text-blue-400 mt-1 font-mono">
                {selectedDomain}{form.path && form.path !== "/" ? form.path : ""}
              </p>
            )}
          </div>

          <div>
            <label className={labelClass}>App Name *</label>
            <input type="text" required placeholder="my-app" value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })} className={inputClass} />
            <p className="text-xs text-panel-muted/70 mt-1">lowercase, a-z 0-9 and dashes, 2-32 chars</p>
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
              placeholder={`/home/${callerUsername || "you"}/apps/${form.name || "my-app"}`}
              value={form.install_path}
              onChange={(e) => setForm({ ...form, install_path: e.target.value })}
              className={inputClass}
            />
            <p className="text-xs text-panel-muted/70 mt-1">
              Absolute filesystem path on the server where the project files will be stored. Leave blank to use the default <code className="font-mono">/home/{callerUsername || "you"}/apps/{"{name}"}</code>.
            </p>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Deploy method *</label>
              <select value={form.deploy_method} onChange={(e) => setForm({ ...form, deploy_method: e.target.value as DeployForm["deploy_method"] })} className={selectClass}>
                <option value="scaffold">Fresh install (scaffold demo)</option>
                <option value="git">Git repository</option>
                <option value="local">Local (files already on disk)</option>
              </select>
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
                {(() => {
                  const provider = detectGitProvider(form.git_url);
                  const genUrl = tokenGenUrl(provider);
                  const providerLabel = provider === "generic" ? "your Git provider" : provider.charAt(0).toUpperCase() + provider.slice(1);
                  return (
                    <>
                      <div className="flex items-center justify-between mb-1">
                        <label className={labelClass + " mb-0"}>Git token <span className="text-xs font-normal text-panel-muted">(required for private repos)</span></label>
                        <button type="button"
                          onClick={() => setShowTokenHelp(!showTokenHelp)}
                          className="text-xs text-blue-400 hover:text-blue-300 flex items-center gap-1 transition-colors">
                          <HelpCircle size={12} /> {showTokenHelp ? "Hide help" : "How to generate?"}
                        </button>
                      </div>
                      <input type="password" placeholder="ghp_... / glpat-... / ATBB..." value={form.git_token}
                        onChange={(e) => setForm({ ...form, git_token: e.target.value })} className={inputClass} />
                      {showTokenHelp && (
                        <div className="mt-2 p-3 bg-blue-500/5 border border-blue-500/20 rounded-lg text-xs text-panel-muted space-y-2">
                          <p className="text-panel-text">
                            A personal access token lets ServerPanel <code className="text-blue-300">git clone</code> your private repo without storing your password.
                            Only the <strong className="text-panel-text">read (clone) scope</strong> is needed.
                          </p>

                          {provider === "github" && (
                            <ol className="list-decimal list-inside space-y-1 pl-1">
                              <li>
                                <a href={genUrl} target="_blank" rel="noopener noreferrer"
                                  className="text-blue-400 hover:text-blue-300 inline-flex items-center gap-1">
                                  Open GitHub token page <ExternalLink size={10} />
                                </a>{" "}
                                <span>(already pre-filled: name = <code>ServerPanel deploy token</code>, scope = <code>repo</code>)</span>
                              </li>
                              <li>Set an <strong className="text-panel-text">Expiration</strong> (90 days recommended). Short-lived tokens = safer if leaked.</li>
                              <li>Click <strong className="text-panel-text">Generate token</strong> at the bottom.</li>
                              <li>Copy the <code className="text-amber-300">ghp_…</code> string (shown once only) and paste it above.</li>
                            </ol>
                          )}

                          {provider === "gitlab" && (
                            <ol className="list-decimal list-inside space-y-1 pl-1">
                              <li>
                                <a href={genUrl} target="_blank" rel="noopener noreferrer"
                                  className="text-blue-400 hover:text-blue-300 inline-flex items-center gap-1">
                                  Open GitLab tokens page <ExternalLink size={10} />
                                </a>
                              </li>
                              <li>Name it <code>ServerPanel deploy</code>, set expiry, check the <code>read_repository</code> scope.</li>
                              <li>Click <strong className="text-panel-text">Create personal access token</strong> and copy the <code className="text-amber-300">glpat-…</code> value.</li>
                              <li>Paste it above.</li>
                            </ol>
                          )}

                          {provider === "bitbucket" && (
                            <ol className="list-decimal list-inside space-y-1 pl-1">
                              <li>
                                <a href={genUrl} target="_blank" rel="noopener noreferrer"
                                  className="text-blue-400 hover:text-blue-300 inline-flex items-center gap-1">
                                  Open Bitbucket app passwords <ExternalLink size={10} />
                                </a>
                              </li>
                              <li>Create App password with <code>Repositories: Read</code> permission only.</li>
                              <li>Copy the generated password (starts with <code className="text-amber-300">ATBB</code>) and paste it above.</li>
                            </ol>
                          )}

                          {provider === "generic" && (
                            <>
                              <p>
                                The Git URL doesn't look like GitHub / GitLab / Bitbucket. The token format depends on {providerLabel}:
                              </p>
                              <ul className="list-disc list-inside space-y-1 pl-1">
                                <li><strong className="text-panel-text">Gitea / Forgejo:</strong> Settings → Applications → Generate New Token → scope <code>read:repository</code>.</li>
                                <li><strong className="text-panel-text">Self-hosted GitLab:</strong> User Settings → Access Tokens → scope <code>read_repository</code>.</li>
                                <li><strong className="text-panel-text">Azure DevOps:</strong> User Settings → Personal Access Tokens → scope <code>Code: Read</code>.</li>
                              </ul>
                            </>
                          )}

                          <div className="flex items-start gap-2 pt-2 border-t border-blue-500/20">
                            <Check size={12} className="text-green-400 mt-0.5 shrink-0" />
                            <p><strong className="text-panel-text">ServerPanel only stores the token encrypted in the app record.</strong> It's used once to clone the repo and on every Redeploy. You can revoke it from {providerLabel} at any time — ServerPanel won't use it for anything else.</p>
                          </div>
                        </div>
                      )}
                    </>
                  );
                })()}
              </div>

              {/* Monorepo subpath */}
              <div className="col-span-3">
                <label className={labelClass}>
                  Repo subpath <span className="text-xs font-normal text-panel-muted">(monorepo only)</span>
                </label>
                <input
                  type="text"
                  placeholder="apps/admin   or   packages/api   (leave blank for single-app repos)"
                  value={form.repo_subpath}
                  onChange={(e) => setForm({ ...form, repo_subpath: e.target.value })}
                  className={inputClass}
                />
                <p className="text-xs text-panel-muted/70 mt-1">
                  Directory inside the cloned repo where this app lives. Install + build + the systemd start command all run from <code className="font-mono">/{form.repo_subpath ? form.repo_subpath.replace(/^\/+|\/+$/g, "") : "&lt;repo-root&gt;"}/</code>. Leave blank for a regular single-app repository.
                </p>
              </div>

              {/* Auto-deploy via webhook */}
              <div className="col-span-3">
                <label className="inline-flex items-start gap-2 text-sm cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={form.auto_deploy}
                    onChange={(e) => setForm({ ...form, auto_deploy: e.target.checked })}
                    className="mt-0.5"
                  />
                  <span>
                    <span className="text-panel-text font-medium flex items-center gap-1.5">
                      <Webhook size={14} className="text-blue-400" /> Auto-deploy on push (GitHub webhook)
                    </span>
                    <span className="block text-xs text-panel-muted/80 mt-0.5">
                      Generates a webhook URL + signing secret on deploy. Pushes to <code className="font-mono">{form.git_branch || "main"}</code> trigger a redeploy automatically. You'll see the URL/secret right after deploy — copy them into GitHub → Settings → Webhooks.
                    </span>
                  </span>
                </label>
              </div>
            </div>
          )}

          {/* Install / Build / Start */}
          {(() => {
            const iHint = installCmdHint(form.app_type);
            const bHint = buildCmdHint(form.app_type);
            const installRequired = iHint !== "";
            const hasInstallError = installCmdError && installRequired && !form.install_cmd.trim() && !form.build_cmd.trim();
            return (
              <>
                <div>
                  <label className={labelClass}>
                    1. Install packages command
                    {installRequired && <span className="text-red-400"> *</span>}
                  </label>
                  <input
                    type="text"
                    placeholder={iHint || "e.g. npm install, pip install, go mod download"}
                    value={form.install_cmd}
                    onChange={(e) => {
                      setForm({ ...form, install_cmd: e.target.value });
                      if (e.target.value.trim()) setInstallCmdError("");
                    }}
                    className={`${inputClass} ${hasInstallError ? "!border-red-500 !ring-red-500/30" : ""}`}
                  />
                  {hasInstallError ? (
                    <p className="text-xs text-red-400 mt-1">{installCmdError}</p>
                  ) : installRequired ? (
                    <p className="text-xs text-panel-muted/70 mt-1">
                      Runs first — fetches dependencies. Example: <code className="font-mono">{iHint}</code>. Pick a Framework preset above to auto-fill.
                    </p>
                  ) : (
                    <p className="text-xs text-panel-muted/70 mt-1">
                      Optional — fetches dependencies before build. Leave blank to skip.
                    </p>
                  )}
                </div>
                <div>
                  <label className={labelClass}>2. Build command</label>
                  <input
                    type="text"
                    placeholder={bHint || "e.g. npm run build, go build -o app ."}
                    value={form.build_cmd}
                    onChange={(e) => setForm({ ...form, build_cmd: e.target.value })}
                    className={inputClass}
                  />
                  <p className="text-xs text-panel-muted/70 mt-1">
                    {bHint
                      ? <>Compile / bundle step — example: <code className="font-mono">{bHint}</code>. Leave blank for interpreted apps that don't need a build.</>
                      : <>Optional. Leave blank for interpreted apps (Python, Ruby, plain Node).</>}
                  </p>
                </div>
              </>
            );
          })()}
          <div>
            <label className={labelClass}>3. Start command</label>
            <input type="text" placeholder="e.g. node server.js, ./app, ./venv/bin/gunicorn app:app" value={form.start_cmd}
              onChange={(e) => setForm({ ...form, start_cmd: e.target.value })} className={inputClass} />
            <p className="text-xs text-panel-muted/70 mt-1">Used as systemd <code className="font-mono">ExecStart</code>. Use <code className="font-mono">${`{PORT}`}</code> to reference the allocated port.</p>
          </div>

          {/* .env disclosure */}
          <div className="rounded-lg border border-blue-500/20 bg-blue-500/5 p-3 text-xs">
            <p className="text-panel-text font-medium mb-1">.env file setup</p>
            <p className="text-panel-muted leading-relaxed">
              Any variables added below are written to <code className="font-mono text-blue-300">{(form.install_path.trim() || `/home/${callerUsername || "you"}/apps/${form.name || "<name>"}`) + "/.env"}</code> (mode 0600)
              AND injected into the systemd service as <code className="font-mono">Environment=</code> lines, so <code className="font-mono">process.env.X</code> / <code className="font-mono">os.getenv("X")</code> / <code className="font-mono">os.Getenv("X")</code> see them
              at runtime. <code className="font-mono">PORT</code> is added automatically.
            </p>
          </div>

          {(form.install_cmd.trim() || form.build_cmd.trim() || form.start_cmd.trim()) && (
            <div className="rounded-lg border border-panel-border bg-panel-bg/50 p-3">
              <p className="text-xs font-medium text-panel-muted mb-2">Will run on deploy</p>
              {form.install_cmd.trim() && (
                <div className="mb-2">
                  <p className="text-[10px] uppercase text-panel-muted/60 mb-1">1. Install</p>
                  <code className="text-xs font-mono text-emerald-400 break-all">$ {form.install_cmd}</code>
                </div>
              )}
              {form.build_cmd.trim() && (
                <div className="mb-2">
                  <p className="text-[10px] uppercase text-panel-muted/60 mb-1">2. Build</p>
                  <code className="text-xs font-mono text-green-400 break-all">$ {form.build_cmd}</code>
                </div>
              )}
              {form.start_cmd.trim() && (
                <div>
                  <p className="text-[10px] uppercase text-panel-muted/60 mb-1">3. Start (systemd ExecStart)</p>
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
            {showAdvanced && (() => {
              const runtimeKey = appTypeToRuntimeKey(form.app_type);
              const installedVersions = (runtimes[runtimeKey] || []).filter((v) => v.installed !== false);
              const isNodeApp = form.app_type === "node" || form.app_type === "nodejs";
              return (
                <div className="mt-3 space-y-3 border-l-2 border-panel-border pl-4">
                  <div className="grid grid-cols-2 gap-4">
                    <div>
                      <label className={labelClass}>App type (override)</label>
                      <select value={form.app_type} onChange={(e) => setForm({ ...form, app_type: e.target.value })} className={selectClass}>
                        <option value="node">Node.js</option>
                        <option value="python">Python</option>
                        <option value="ruby">Ruby</option>
                        <option value="go">Go</option>
                        <option value="rust">Rust</option>
                        <option value="static">Static</option>
                        <option value="php">PHP</option>
                        <option value="java">Java</option>
                        <option value="docker">Docker</option>
                      </select>
                    </div>
                    <div>
                      <label className={labelClass}>Runtime version</label>
                      {runtimeKey ? (
                        <>
                          <select value={form.runtime_version}
                            onChange={(e) => setForm({ ...form, runtime_version: e.target.value })}
                            className={selectClass}>
                            <option value="">System default</option>
                            {installedVersions.map((v) => (
                              <option key={v.version} value={v.version}>
                                {v.version}{v.active ? " (active)" : ""}
                              </option>
                            ))}
                          </select>
                          <p className="text-xs text-panel-muted/70 mt-1">
                            {installedVersions.length === 0
                              ? <>Leave blank to use the system default {runtimeKey} version. Your owner manages installed runtimes on the server.</>
                              : <>Pins <code className="font-mono">PATH</code> so build + runtime use this exact {runtimeKey} version.</>}
                          </p>
                        </>
                      ) : (
                        <>
                          <input type="text" disabled value="" placeholder="N/A for this app type"
                            className={inputClass + " opacity-50"} />
                          <p className="text-xs text-panel-muted/70 mt-1">Not applicable for {form.app_type} apps.</p>
                        </>
                      )}
                    </div>
                  </div>
                  <div>
                    <label className={labelClass}>Health check path</label>
                    <input type="text" placeholder="/" value={form.health_check_path}
                      onChange={(e) => setForm({ ...form, health_check_path: e.target.value })} className={inputClass} />
                    <p className="text-xs text-panel-muted/70 mt-1">
                      HTTP-probed once after deploy. Non-2xx/3xx responses log a warning (deploy still succeeds). Leave as <code className="font-mono">/</code> or blank to skip.
                    </p>
                  </div>
                  {isNodeApp && (
                    <div className="grid grid-cols-2 gap-4">
                      <div>
                        <label className={labelClass}>Min instances</label>
                        <input type="number" min={1} max={32} value={form.min_instances}
                          onChange={(e) => setForm({ ...form, min_instances: Math.max(1, parseInt(e.target.value) || 1) })}
                          className={inputClass} />
                      </div>
                      <div>
                        <label className={labelClass}>Max instances</label>
                        <input type="number" min={1} max={32} value={form.max_instances}
                          onChange={(e) => setForm({ ...form, max_instances: Math.max(1, parseInt(e.target.value) || 1) })}
                          className={inputClass} />
                      </div>
                      <p className="col-span-2 text-xs text-panel-muted/70 -mt-2">
                        Switches PM2 to <code className="font-mono">cluster</code> mode when &gt; 1 — spawns N workers behind a single listening socket for CPU-bound Node apps. Set both to 1 (default) for a single-process fork.
                      </p>
                    </div>
                  )}
                </div>
              );
            })()}
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

      {/* ---------- Webhook reveal ---------- */}
      <Modal
        isOpen={!!webhookReveal}
        onClose={() => setWebhookReveal(null)}
        title={webhookReveal ? `Auto-deploy webhook — ${webhookReveal.appName}` : "Webhook"}
        size="lg"
      >
        {webhookReveal && (
          <div className="space-y-4">
            <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-xs text-amber-200">
              <strong className="text-amber-100">Copy these now.</strong> The signing secret is shown one time only — if you lose it you'll need to disable + re-enable the webhook to issue a new pair.
            </div>

            <div>
              <label className={labelClass}>Payload URL</label>
              <div className="flex items-center gap-2">
                <input readOnly value={webhookReveal.url}
                  className={inputClass + " font-mono text-xs"} onFocus={(e) => e.currentTarget.select()} />
                <button type="button"
                  onClick={() => { navigator.clipboard.writeText(webhookReveal.url); toast.success("URL copied"); }}
                  className="p-2 rounded border border-panel-border hover:bg-panel-bg text-panel-muted hover:text-panel-text"
                  title="Copy URL">
                  <Copy size={14} />
                </button>
              </div>
            </div>

            <div>
              <label className={labelClass}>Secret</label>
              <div className="flex items-center gap-2">
                <input readOnly value={webhookReveal.secret}
                  className={inputClass + " font-mono text-xs"} onFocus={(e) => e.currentTarget.select()} />
                <button type="button"
                  onClick={() => { navigator.clipboard.writeText(webhookReveal.secret); toast.success("Secret copied"); }}
                  className="p-2 rounded border border-panel-border hover:bg-panel-bg text-panel-muted hover:text-panel-text"
                  title="Copy secret">
                  <Copy size={14} />
                </button>
              </div>
            </div>

            <div className="rounded-lg border border-blue-500/20 bg-blue-500/5 p-3 text-xs text-panel-muted space-y-1.5">
              <p className="text-panel-text font-medium">Setup in GitHub</p>
              <ol className="list-decimal list-inside space-y-0.5 pl-1">
                <li>Open your repo → <strong className="text-panel-text">Settings</strong> → <strong className="text-panel-text">Webhooks</strong> → <strong className="text-panel-text">Add webhook</strong>.</li>
                <li>Paste the <strong className="text-panel-text">Payload URL</strong> above.</li>
                <li>Set <strong className="text-panel-text">Content type</strong> to <code className="font-mono">application/json</code>.</li>
                <li>Paste the <strong className="text-panel-text">Secret</strong> above.</li>
                <li>Pick <strong className="text-panel-text">Just the push event</strong>, click <strong className="text-panel-text">Add webhook</strong>.</li>
              </ol>
              <p className="pt-1">GitLab works the same — paste the URL and put the Secret in <code className="font-mono">Secret token</code>. Pings get a <code className="font-mono">200 OK</code>; pushes to the configured branch trigger a Redeploy.</p>
            </div>

            <div className="flex justify-end pt-2 border-t border-panel-border">
              <button type="button" onClick={() => setWebhookReveal(null)}
                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium">
                I've saved them
              </button>
            </div>
          </div>
        )}
      </Modal>

      {/* ---------- Backup / Restore ---------- */}
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

      {/* ---------- Transfer ---------- */}
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
              <input type="text" placeholder="target-user" value={transferUser}
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

      {/* ---------- Package Install ---------- */}
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
                  <button type="button" onClick={() => setPkgCmd("./venv/bin/pip install -r requirements.txt")} className="text-xs px-2 py-1 rounded bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border">pip install -r requirements.txt</button>
                )}
                {pkgApp.app_type === "ruby" && (
                  <button type="button" onClick={() => setPkgCmd("bundle install")} className="text-xs px-2 py-1 rounded bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border">bundle install</button>
                )}
                {pkgApp.app_type === "go" && (
                  <>
                    <button type="button" onClick={() => setPkgCmd("go mod download")} className="text-xs px-2 py-1 rounded bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border">go mod download</button>
                    <button type="button" onClick={() => setPkgCmd("go mod tidy")} className="text-xs px-2 py-1 rounded bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border">go mod tidy</button>
                    <button type="button" onClick={() => setPkgCmd("go build -o app .")} className="text-xs px-2 py-1 rounded bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border">go build</button>
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
                      {pkgSuccess ? "success" : "failed"}
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

      {/* ---------- Logs viewer ---------- */}
      <Modal isOpen={!!logsApp} onClose={() => setLogsApp(null)}
        title={logsApp ? `Logs — ${logsApp.name}` : "Logs"} size="xl">
        {logsApp && (
          <div className="space-y-3">
            <div className="flex items-center justify-between text-sm">
              <div className="text-panel-muted">
                <code className="text-xs bg-panel-bg px-2 py-0.5 rounded">journalctl -u sp-app-{logsApp.name}</code>
              </div>
              <div className="flex items-center gap-3">
                <label className="flex items-center gap-1.5 text-xs text-panel-muted cursor-pointer">
                  <input type="checkbox" checked={logsAuto} onChange={(e) => setLogsAuto(e.target.checked)}
                    className="w-3.5 h-3.5 rounded border-panel-border text-blue-600 focus:ring-blue-500/40" />
                  Auto-refresh (3s)
                </label>
                <Button onClick={() => fetchLogs(logsApp.name, true)}
                  className="flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-xs">
                  <RefreshCw size={12} className={logsLoading ? "animate-spin" : ""} /> Refresh
                </Button>
              </div>
            </div>
            <div className="bg-black/60 border border-panel-border rounded-lg p-3 font-mono text-xs text-green-300 max-h-[480px] overflow-auto whitespace-pre-wrap">
              {logsLoading && logsLines.length === 0 ? (
                <span className="text-panel-muted">Loading…</span>
              ) : logsLines.length === 0 ? (
                <span className="text-panel-muted">(no log entries)</span>
              ) : (
                logsLines.map((line, i) => <div key={i}>{line}</div>)
              )}
            </div>
          </div>
        )}
      </Modal>

      {/* ---------- Edit Application ---------- */}
      <Modal isOpen={!!editApp} onClose={() => setEditApp(null)}
        title={editApp ? `Edit — ${editApp.name}` : "Edit Application"} size="lg">
        {editApp && (
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className={labelClass}>Domain</label>
                <input type="text" value={editForm.domain}
                  onChange={(e) => setEditForm({ ...editForm, domain: e.target.value })}
                  className={inputClass} />
              </div>
              <div>
                <label className={labelClass}>Path</label>
                <input type="text" value={editForm.path}
                  onChange={(e) => setEditForm({ ...editForm, path: e.target.value })}
                  className={inputClass} />
              </div>
            </div>

            <div>
              <label className={labelClass}>Install Command</label>
              <input type="text" value={editForm.install_cmd}
                onChange={(e) => setEditForm({ ...editForm, install_cmd: e.target.value })}
                placeholder="e.g. npm install, pip install -r requirements.txt, go mod download"
                className={inputClass} />
              <p className="text-xs text-panel-muted mt-1">Runs first on Redeploy. Fetches dependencies.</p>
            </div>

            <div>
              <label className={labelClass}>Build Command</label>
              <input type="text" value={editForm.build_cmd}
                onChange={(e) => setEditForm({ ...editForm, build_cmd: e.target.value })}
                placeholder="e.g. npm run build, go build -o app ."
                className={inputClass} />
              <p className="text-xs text-panel-muted mt-1">Runs after Install on Redeploy. Leave blank for interpreted apps.</p>
            </div>

            <div>
              <label className={labelClass}>Start Command</label>
              <input type="text" value={editForm.start_cmd}
                onChange={(e) => setEditForm({ ...editForm, start_cmd: e.target.value })}
                placeholder="e.g. node server.js  or  ./venv/bin/gunicorn app:app"
                className={inputClass} />
              <p className="text-xs text-panel-muted mt-1">Use <code>${"{PORT}"}</code> for the assigned port. Node apps regenerate ecosystem.config.js automatically.</p>
            </div>

            <div className="grid grid-cols-3 gap-3">
              <div>
                <label className={labelClass}>Health-check path</label>
                <input type="text" value={editForm.health_check_path}
                  onChange={(e) => setEditForm({ ...editForm, health_check_path: e.target.value })}
                  className={inputClass} />
              </div>
              <div>
                <label className={labelClass}>Git URL</label>
                <input type="text" value={editForm.git_url}
                  onChange={(e) => setEditForm({ ...editForm, git_url: e.target.value })}
                  className={inputClass} />
              </div>
              <div>
                <label className={labelClass}>Git branch</label>
                <input type="text" value={editForm.git_branch}
                  onChange={(e) => setEditForm({ ...editForm, git_branch: e.target.value })}
                  className={inputClass} />
              </div>
            </div>

            <div>
              <div className="flex items-center justify-between mb-1">
                <label className={labelClass}>Environment Variables</label>
                <button type="button"
                  onClick={() => setEditForm({ ...editForm, env_rows: [...editForm.env_rows, { key: "", value: "" }] })}
                  className="text-xs text-blue-400 hover:text-blue-300">+ Add</button>
              </div>
              <div className="space-y-1 max-h-48 overflow-y-auto">
                {editForm.env_rows.length === 0 ? (
                  <p className="text-xs text-panel-muted/70">No env vars set.</p>
                ) : editForm.env_rows.map((row, i) => (
                  <div key={i} className="flex items-center gap-1.5">
                    <input type="text" value={row.key} placeholder="KEY"
                      onChange={(e) => {
                        const next = [...editForm.env_rows];
                        next[i] = { ...next[i], key: e.target.value };
                        setEditForm({ ...editForm, env_rows: next });
                      }}
                      className={`${inputClass} font-mono text-xs flex-1`} />
                    <input type="text" value={row.value} placeholder="value"
                      onChange={(e) => {
                        const next = [...editForm.env_rows];
                        next[i] = { ...next[i], value: e.target.value };
                        setEditForm({ ...editForm, env_rows: next });
                      }}
                      className={`${inputClass} font-mono text-xs flex-[2]`} />
                    <button type="button"
                      onClick={() => setEditForm({ ...editForm, env_rows: editForm.env_rows.filter((_, j) => j !== i) })}
                      className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400">
                      <X size={14} />
                    </button>
                  </div>
                ))}
              </div>
            </div>

            <div className="flex items-center justify-between pt-2 border-t border-panel-border">
              <label className="flex items-center gap-2 text-sm text-panel-muted cursor-pointer">
                <input type="checkbox" checked={editForm.restart}
                  onChange={(e) => setEditForm({ ...editForm, restart: e.target.checked })}
                  className="w-4 h-4 rounded border-panel-border text-blue-600 focus:ring-blue-500/40" />
                Restart service after save
              </label>
              <div className="flex gap-2">
                <button type="button" onClick={() => setEditApp(null)} disabled={editSaving}
                  className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted hover:text-panel-text disabled:opacity-50">
                  Cancel
                </button>
                <button type="button" onClick={saveEdit} disabled={editSaving}
                  className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium disabled:opacity-50 flex items-center gap-2">
                  {editSaving && <RotateCw size={14} className="animate-spin" />}
                  {editSaving ? "Saving…" : "Save changes"}
                </button>
              </div>
            </div>
          </div>
        )}
      </Modal>

      {buildError && (
        <BuildErrorModal info={buildError} onClose={() => setBuildError(null)} />
      )}
    </div>
  );
}
