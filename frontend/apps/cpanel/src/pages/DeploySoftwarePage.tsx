import React, { useEffect, useState } from "react";
import { Card, Button, Modal, StatusBadge, PasswordInput, SearchableSelect, confirmAction, copyToClipboard, usePagination, PaginationBar } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Rocket, Plus, RefreshCw, Trash2, Play, Copy, HelpCircle, X,
  ChevronDown, ChevronRight, GitBranch, Globe, Shield, ExternalLink,
  KeyRound, Webhook, Server, PackageOpen, Layers, AlertCircle, AlertTriangle, CheckCircle,
  Eye, EyeOff, Pause, Power, RotateCw, Square, Pencil, Check, Package, Hammer, Code2,
} from "lucide-react";

// ──────────────────────────────────────────────────────────────────────────
// Types — mirror internal/models/project.go on the backend
// ──────────────────────────────────────────────────────────────────────────

interface Project {
  id: string;
  name: string;
  slug: string;
  description: string;
  github_pat_masked: string;
  git_repo_url: string;
  // Project-wide branch every service tracks (3.1.27 hoist).
  git_branch: string;
  project_dir: string;
  user: string;
  auto_deploy: boolean;
  paused: boolean;
  last_webhook_at: string | null;
  last_webhook_event: string;
  created_at: string;
  updated_at: string;
}

interface ProjectService {
  id: string;
  project_id: string;
  name: string;
  role: "backend" | "frontend" | "static";
  framework: string;
  git_repo_url: string;
  git_subpath: string;
  git_branch: string;
  path_prefix: string;
  primary_domain: string;
  alias_domains: string[];
  install_cmd: string;
  build_cmd: string;
  start_cmd: string;
  port: number;
  env_vars: Record<string, string>;
  user: string;
  install_dir: string;
  status: string;
  last_commit_sha: string;
  last_deployed_at: string | null;
  missing_env_keys?: string[];
}

interface Preset {
  framework?: string;
  label: string;
  app_type: string;
  install_cmd: string;
  build_cmd: string;
  start_cmd: string;
  default_port: number;
  is_static?: boolean;
}

interface DeployLog {
  deployment_id: string;
  trigger: string;
  status: string;
  started_at: string;
  finished_at: string | null;
  commit: string;
  error: string;
  output: string;
}

interface WebhookInfo {
  url: string;
  secret: string;
}

interface ProjectActivity {
  last_commit?: { sha: string; short: string; message: string; author: string; date: string };
  deploys: {
    total: number;
    successful: number;
    failed: number;
    last_at?: string;
    last_by?: string;
    last_manual?: { id: string; trigger: string; status: string; started_at: string; commit_sha?: string };
    last_auto?: { id: string; trigger: string; status: string; started_at: string; commit_sha?: string };
  };
  webhook: { last_at?: string; last_event?: string; configured?: boolean };
  recent_deployments: Array<{ id: string; trigger: string; status: string; commit_sha?: string; started_at: string; finished_at?: string; error_msg?: string; progress?: number }>;
  runtime: Record<string, { service_id: string; name: string; status: string; unit_state: string; uptime_sec: number; main_pid: string; memory_mb: number; num_restarts: number }>;
}

interface DomainOption {
  id: string;
  domain: string;
  user?: string;
}

// Local inline copy of the BuildErrorModal implementation. The WHM panel
// ships it as @/components/BuildErrorModal so /apps and /projects can share
// it, but the cPanel surface doesn't have a components dir yet and the
// page is the only consumer, so we keep it inline here.
interface BuildErrorInfo {
  service: string;
  stage: string;
  summary: string;
  output: string;
}

function tryExtractBuildError(err: any): BuildErrorInfo | null {
  const body = err?.response?.data?.error;
  if (body?.code !== "BUILD_FAILED" || !body?.details?.output) return null;
  return {
    service: body.details.service || "",
    stage: body.details.stage || "build",
    summary: body.details.summary || body.message || "",
    output: body.details.output,
  };
}

function BuildErrorModal({ info, onClose }: { info: BuildErrorInfo; onClose: () => void }) {
  const stageTitle =
    info.stage === "install" ? "Install"
      : info.stage === "build" ? "Build"
        : info.stage === "start" ? "Start"
          : info.stage.charAt(0).toUpperCase() + info.stage.slice(1);
  const [copied, setCopied] = useState(false);
  async function copyOutput() {
    if (await copyToClipboard(info.output)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  }
  return (
    <Modal isOpen onClose={onClose} title={`${stageTitle} failed`} size="xl">
      <div className="space-y-3">
        <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-sm">
          <div className="flex items-start gap-2">
            <AlertCircle size={15} className="text-red-400 mt-0.5 shrink-0" />
            <div className="flex-1 space-y-1">
              <div className="text-red-400 font-medium">
                {info.service ? (<>Service "{info.service}" — {info.stage} step failed</>) : (<>{stageTitle} step failed</>)}
              </div>
              {info.summary && (
                <div className="text-panel-text font-mono text-xs break-all">{info.summary}</div>
              )}
              <div className="text-panel-muted text-[11px]">
                Fix the error in your repo or in your commands above, then retry.
              </div>
            </div>
          </div>
        </div>
        <div>
          <div className="flex items-center justify-between mb-1">
            <span className="text-xs text-panel-muted">Full output</span>
            <button onClick={copyOutput} className="inline-flex items-center gap-1 text-[11px] text-panel-muted hover:text-panel-text">
              {copied ? <Check size={11} className="text-green-400" /> : <Copy size={11} />}
              {copied ? "Copied" : "Copy"}
            </button>
          </div>
          <pre className="max-h-[50vh] overflow-auto p-3 bg-panel-bg border border-panel-border rounded-lg text-[11px] text-panel-text whitespace-pre-wrap font-mono">
            {info.output}
          </pre>
        </div>
        <div className="flex justify-end">
          <button onClick={onClose} className="px-4 py-2 text-sm bg-panel-surface border border-panel-border rounded-lg text-panel-text">Close</button>
        </div>
      </div>
    </Modal>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Small reusable UI primitives
// ──────────────────────────────────────────────────────────────────────────

const inputCls =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const inputInvalidCls = inputCls.replace("border-panel-border", "border-red-500/60").replace("focus:ring-blue-500/40", "focus:ring-red-500/40").replace("focus:border-blue-500", "focus:border-red-500");
const labelCls = "block text-sm font-medium text-panel-text mb-1";
const selectCls = inputCls + " appearance-none";

function sanitizeServiceName(raw: string): string {
  let s = raw.toLowerCase();
  s = s.replace(/[^a-z0-9-]+/g, "-");
  s = s.replace(/-+/g, "-");
  s = s.replace(/^[^a-z]+/, "");
  if (s.length > 32) s = s.slice(0, 32);
  return s;
}

function validateServiceName(name: string): string | null {
  if (name === "") return "Required";
  if (name.length < 2) return "At least 2 characters";
  if (name.length > 32) return "Max 32 characters";
  if (!/^[a-z][a-z0-9-]*$/.test(name)) return "Must start with a letter; only a-z 0-9 and '-'";
  return null;
}

function slugifyProjectName(raw: string): string {
  let s = raw.toLowerCase().trim();
  s = s.replace(/[^a-z0-9]+/g, "-");
  s = s.replace(/^-+|-+$/g, "");
  if (s.length > 40) s = s.slice(0, 40);
  return s;
}

function isLikelyDomain(d: string): boolean {
  const t = d.trim().toLowerCase();
  return /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/.test(t);
}

function relativeTime(iso?: string | null): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (!t || Number.isNaN(t)) return "—";
  const diff = Math.max(0, (Date.now() - t) / 1000);
  if (diff < 60) return `${Math.floor(diff)}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  if (diff < 86400 * 30) return `${Math.floor(diff / 86400)}d ago`;
  return new Date(iso).toLocaleDateString();
}

function formatUptime(s: number): string {
  if (!s || s < 0) return "—";
  if (s < 60) return `${Math.floor(s)}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`;
}

function FieldHint({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  return (
    <span className="relative inline-flex items-center">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        onBlur={() => setOpen(false)}
        className="text-panel-muted/60 hover:text-blue-400 transition-colors"
        tabIndex={-1}
      >
        <HelpCircle size={13} />
      </button>
      {open && (
        <span className="absolute left-5 top-0 z-20 w-64 px-3 py-2 rounded-lg bg-panel-surface border border-panel-border text-[11px] leading-relaxed text-panel-muted shadow-lg">
          {text}
        </span>
      )}
    </span>
  );
}

function LabelWithHint({ children, hint, required }: { children: React.ReactNode; hint: string; required?: boolean }) {
  return (
    <label className={labelCls}>
      <span className="inline-flex items-center gap-1.5">
        <span>{children}</span>
        {required && <span className="text-red-400">*</span>}
        <FieldHint text={hint} />
      </span>
    </label>
  );
}

function Disclosure({
  title, icon, defaultOpen, children,
}: {
  title: string;
  icon?: React.ReactNode;
  defaultOpen?: boolean;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(!!defaultOpen);
  return (
    <div className="border border-panel-border rounded-lg overflow-hidden">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="w-full flex items-center justify-between px-4 py-3 bg-panel-bg/40 hover:bg-panel-bg transition-colors text-left"
      >
        <span className="inline-flex items-center gap-2 text-sm font-medium text-panel-text">
          {icon} {title}
        </span>
        {open ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
      </button>
      {open && <div className="px-4 py-3 text-[13px] text-panel-muted leading-relaxed space-y-2">{children}</div>}
    </div>
  );
}

// ServiceIdCopy renders a tiny "id" chip inline next to a service
// name. Hover reveals the truncated id; click copies the full
// ObjectID. Matches the API-IDs panel pattern at the project level
// so an integrator has one consistent affordance everywhere they
// need to grab an id.
function ServiceIdCopy({ id, name }: { id: string; name: string }) {
  const [ok, setOk] = useState(false);
  if (!id) return null;
  return (
    <button
      type="button"
      onClick={async (e) => {
        e.stopPropagation();
        if (await copyToClipboard(id)) {
          setOk(true);
          setTimeout(() => setOk(false), 1400);
        }
      }}
      title={`Copy service id (${id}) — service "${name}"`}
      className="inline-flex items-center gap-1 text-[9px] font-mono px-1.5 py-0.5 rounded border border-panel-border bg-panel-bg text-panel-muted hover:text-brand-400 hover:border-brand-500/40 transition-colors"
    >
      {ok ? <Check size={9} className="text-green-400" /> : <Copy size={9} />}
      {ok ? "id copied" : `id: ${id.slice(0, 6)}…`}
    </button>
  );
}

function CopyButton({ value, label = "Copy" }: { value: string; label?: string }) {
  const [ok, setOk] = useState(false);
  return (
    <button
      type="button"
      onClick={async () => {
        if (await copyToClipboard(value)) {
          setOk(true);
          setTimeout(() => setOk(false), 1500);
        }
      }}
      className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs border border-panel-border text-panel-muted hover:text-blue-400 hover:border-blue-500/40 transition-colors"
    >
      {ok ? <CheckCircle size={12} className="text-green-400" /> : <Copy size={12} />} {ok ? "Copied" : label}
    </button>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Main page component
// ──────────────────────────────────────────────────────────────────────────

export default function DeploySoftwarePage() {
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);
  const [detailProject, setDetailProject] = useState<Project | null>(null);
  const [presets, setPresets] = useState<Record<string, Preset>>({});
  const [availableDomains, setAvailableDomains] = useState<DomainOption[]>([]);
  // cPanel doesn't have access to /monitor/system (that's WHM-only). We
  // leave the server-IP hint as a placeholder unless the operator knows it
  // and pastes it into their DNS records manually. Kept as state so the
  // same child components that expect a `serverIP` prop still work.
  const serverIP = "";

  useEffect(() => {
    fetchProjects();
    fetchPresets();
    fetchDomains();
  }, []);

  async function fetchProjects() {
    setLoading(true);
    try {
      const res = await api.get("/projects", { params: { limit: 10000 } });
      setProjects(res.data?.data || []);
    } catch {
      /* ignore */
    } finally {
      setLoading(false);
    }
  }

  const pgProj = usePagination("cpanel-projects");
  useEffect(() => { pgProj.setTotal(projects.length); }, [projects.length]);
  const pagedProjects = projects.slice((pgProj.page - 1) * pgProj.limit, pgProj.page * pgProj.limit);

  async function fetchDomains() {
    try {
      const res = await api.get("/domains?limit=500");
      setAvailableDomains(res.data?.data || []);
    } catch {
      /* keep empty */
    }
  }

  async function fetchPresets() {
    try {
      const res = await api.get("/apps/presets");
      const data = res.data?.data;
      if (data && typeof data === "object") {
        const map: Record<string, Preset> = {};
        for (const [k, v] of Object.entries(data as Record<string, Preset>)) {
          map[k] = { ...v, framework: v.framework || k };
        }
        setPresets(map);
      }
    } catch {
      /* ignore */
    }
  }

  async function handleDelete(p: Project) {
    if (!await confirmAction({ title: "Delete?", description: `Delete project "${p.name}" and all its services? This removes code, systemd units, and nginx configs.`, danger: true, confirmLabel: "Delete" })) return;
    try {
      await api.delete(`/projects/${p.id}`);
      toast.success("Project deleted");
      fetchProjects();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to delete");
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
            <Rocket size={20} className="text-blue-400" /> Deploy Software
          </h1>
          <p className="text-panel-muted text-sm mt-1">
            Deploy multi-service software projects (backend + frontend + static) with auto-deploy, multi-domain support, and automatic SSL.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchProjects}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
          </Button>
          <Button
            onClick={() => setShowCreate(true)}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} /> New Project
          </Button>
        </div>
      </div>

      <SetupGuide serverIP={serverIP} />

      <Card>
        {loading ? (
          <div className="p-8 space-y-3">
            {[1, 2, 3].map((i) => <div key={i} className="h-14 bg-panel-border/20 rounded animate-pulse" />)}
          </div>
        ) : projects.length === 0 ? (
          <div className="text-center py-16 px-4">
            <Rocket size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No software projects yet</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              A project is a bundle of 1+ backend services and 1+ frontend services that share a GitHub PAT and a single webhook for auto-deploys.
            </p>
            <Button
              onClick={() => setShowCreate(true)}
              className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
            >
              <Plus size={14} /> New Project
            </Button>
          </div>
        ) : (
          <>
            <div className="divide-y divide-panel-border">
              {pagedProjects.map((p) => (
                <ProjectRow key={p.id} project={p} onOpen={() => setDetailProject(p)} onDelete={() => handleDelete(p)} />
              ))}
            </div>
            <PaginationBar page={pgProj.page} limit={pgProj.limit} total={pgProj.total}
              onPageChange={pgProj.setPage} onLimitChange={pgProj.setLimit} />
          </>
        )}
      </Card>

      {showCreate && (
        <CreateProjectWizard
          serverIP={serverIP}
          presets={presets}
          availableDomains={availableDomains}
          onClose={() => setShowCreate(false)}
          onCreated={(created) => {
            setShowCreate(false);
            fetchProjects();
            if (created) setDetailProject(created);
          }}
        />
      )}

      {detailProject && (
        <ProjectDetailDrawer
          project={detailProject}
          serverIP={serverIP}
          presets={presets}
          availableDomains={availableDomains}
          onClose={() => setDetailProject(null)}
          onChanged={fetchProjects}
        />
      )}
    </div>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Setup guide
// ──────────────────────────────────────────────────────────────────────────

function SetupGuide({ serverIP }: { serverIP: string }) {
  return (
    <Disclosure title="How to set up a software project" icon={<HelpCircle size={14} className="text-blue-400" />} defaultOpen={false}>
      <ol className="list-decimal ml-5 space-y-2">
        <li>
          <b>Create a Project</b> — pick a name and paste your GitHub Personal Access Token (PAT) if any service clones from a private repo. Tokens are AES-GCM encrypted on disk; the UI only ever shows a <code>ghp_****xyz9</code> preview after save.
        </li>
        <li>
          <b>Add services</b> — for each deployable piece of the software, pick the role:
          <ul className="list-disc ml-5 mt-1 space-y-0.5">
            <li><b>backend</b> — Node/Python/Go process, runs as a systemd unit, reverse-proxied.</li>
            <li><b>frontend</b> — Vite/Next build, output served statically by nginx.</li>
            <li><b>static</b> — plain HTML/CSS/JS, no build step.</li>
          </ul>
          Each service picks its own branch, subpath (monorepo support), and framework preset.
        </li>
        <li>
          <b>Point domains at the server</b> — for every frontend service:
          <ul className="list-disc ml-5 mt-1 space-y-0.5">
            <li>The <b>primary</b> domain needs an <b>A record</b> → <code>{serverIP || "YOUR_SERVER_IP"}</code></li>
            <li>Each <b>alias</b> domain needs a <b>CNAME record</b> → the primary domain</li>
          </ul>
          Let's Encrypt issues one certificate covering primary + every alias via <code>certbot --expand</code>.
        </li>
        <li>
          <b>Wire the webhook</b> — after creating the project, open its detail drawer and copy the webhook URL + secret. On GitHub: <i>Repo → Settings → Webhooks → Add webhook</i>. Payload URL = the copied URL, Content type = <code>application/json</code>, Secret = the copied secret, Events = <i>Just the push event</i>.
        </li>
        <li>
          <b>Push to deploy</b> — with Auto-deploy enabled, a push to the configured branch triggers redeploys of only the services whose subpath changed. You can also hit <i>Deploy</i> on any service manually.
        </li>
      </ol>
    </Disclosure>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Project row (collapsed view)
// ──────────────────────────────────────────────────────────────────────────

function ProjectRow({ project, onOpen, onDelete }: { project: Project; onOpen: () => void; onDelete: () => void }) {
  return (
    <div className="flex items-center justify-between px-5 py-4 hover:bg-panel-bg/40 transition-colors">
      <button onClick={onOpen} className="flex-1 text-left">
        <div className="flex items-center gap-2">
          <PackageOpen size={16} className="text-blue-400" />
          <span className="font-medium text-panel-text">{project.name}</span>
          <span className="text-[11px] text-panel-muted/60">/{project.slug}</span>
          {project.auto_deploy ? (
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-green-500/10 text-green-400 border border-green-500/20">auto-deploy</span>
          ) : (
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-panel-bg text-panel-muted border border-panel-border">manual</span>
          )}
          {project.paused && (
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-yellow-500/10 text-yellow-400 border border-yellow-500/20">paused</span>
          )}
        </div>
        {project.description && <p className="text-xs text-panel-muted mt-1">{project.description}</p>}
      </button>
      <div className="flex items-center gap-2">
        <Button
          onClick={onOpen}
          className="px-3 py-1.5 bg-panel-bg border border-panel-border rounded text-xs text-panel-muted hover:text-panel-text"
        >
          Open
        </Button>
        <button
          onClick={onDelete}
          className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
          title="Delete"
        >
          <Trash2 size={14} />
        </button>
      </div>
    </div>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Create wizard (multi-step modal)
// ──────────────────────────────────────────────────────────────────────────

interface NewServiceForm {
  name: string;
  role: "backend" | "frontend" | "static";
  framework: string;
  git_repo_url: string;
  git_subpath: string;
  git_branch: string;
  path_prefix: string;
  primary_domain: string;
  alias_domains: string[];
  install_cmd: string;
  build_cmd: string;
  start_cmd: string;
  port: number;
  env_vars: Record<string, string>;
}

const emptyService = (): NewServiceForm => ({
  name: "",
  role: "backend",
  framework: "",
  git_repo_url: "",
  git_subpath: "",
  git_branch: "main",
  path_prefix: "",
  primary_domain: "",
  alias_domains: [],
  install_cmd: "",
  build_cmd: "",
  start_cmd: "",
  port: 0,
  env_vars: {},
});

function CreateProjectWizard({
  serverIP, presets, availableDomains, onClose, onCreated,
}: {
  serverIP: string;
  presets: Record<string, Preset>;
  availableDomains: DomainOption[];
  onClose: () => void;
  onCreated: (created: Project | null) => void;
}) {
  // cPanel always deploys under the vendor's own username — the backend
  // picks that up from the JWT (`callerCtx.tenantID`) so we don't send
  // `user` in the Provision payload at all. No vendor picker UI here.
  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [pat, setPat] = useState("");
  const [repoURL, setRepoURL] = useState("");
  // Project-level branch (3.1.27 hoist) — every service inherits it.
  const [projectBranch, setProjectBranch] = useState("main");
  const [autoDeploy, setAutoDeploy] = useState(true);
  const [services, setServices] = useState<NewServiceForm[]>([emptyService()]);
  const [saving, setSaving] = useState(false);
  const [buildError, setBuildError] = useState<BuildErrorInfo | null>(null);
  const [provisionStartedAt, setProvisionStartedAt] = useState<number | null>(null);
  const [tickNow, setTickNow] = useState(Date.now());
  useEffect(() => {
    if (!saving) return;
    const id = setInterval(() => setTickNow(Date.now()), 500);
    return () => clearInterval(id);
  }, [saving]);
  const provisionSteps = [
    { label: "Creating project record", seconds: 1 },
    { label: "Cloning repository", seconds: 8 },
    { label: "Installing dependencies", seconds: 35 },
    { label: "Running build", seconds: 25 },
    { label: "Starting service + binding port", seconds: 8 },
    { label: "Configuring nginx + SSL", seconds: 10 },
  ];
  const totalEstSeconds = provisionSteps.reduce((a, s) => a + s.seconds, 0);
  const elapsedSec = provisionStartedAt ? Math.max(0, (tickNow - provisionStartedAt) / 1000) : 0;
  const currentStepIdx = (() => {
    let acc = 0;
    for (let i = 0; i < provisionSteps.length; i++) {
      acc += provisionSteps[i].seconds;
      if (elapsedSec < acc) return i;
    }
    return provisionSteps.length - 1;
  })();
  const progressPct = Math.min(99, Math.round((elapsedSec / totalEstSeconds) * 100));

  function updateService(i: number, patch: Partial<NewServiceForm>) {
    setServices((ss) => ss.map((s, idx) => (idx === i ? { ...s, ...patch } : s)));
  }

  function applyPreset(i: number, key: string) {
    const p = presets[key];
    if (!p) return updateService(i, { framework: key });
    updateService(i, {
      framework: key,
      install_cmd: p.install_cmd || "",
      build_cmd: p.build_cmd || "",
      start_cmd: p.start_cmd || "",
      port: p.default_port || 0,
      role: p.is_static ? "frontend" : services[i].role === "static" ? "static" : "backend",
    });
  }

  async function handleCreate() {
    if (!name.trim()) return toast.error("Project name is required");
    if (slugifyProjectName(name) === "") return toast.error("Project name must contain at least one letter or digit");
    if (!repoURL.trim()) return toast.error("Repository URL is required");
    const seen = new Set<string>();
    for (const s of services) {
      const nameErr = validateServiceName(s.name);
      if (nameErr) return toast.error(`Service name "${s.name || "(empty)"}": ${nameErr}`);
      if (seen.has(s.name)) return toast.error(`Duplicate service name "${s.name}" — each service in a project must have a unique name`);
      seen.add(s.name);
      if (!s.primary_domain) return toast.error(`Service "${s.name}": primary domain required`);
    }
    const branchClean = projectBranch.trim() || "main";
    const servicesWithRepo = services.map((s) => ({
      ...s,
      git_repo_url: repoURL.trim(),
      git_branch: branchClean,
    }));
    setSaving(true);
    setProvisionStartedAt(Date.now());
    try {
      // Vendor-scoped: backend derives `user` from the JWT, we don't send it.
      const res = await api.post("/projects/provision", {
        name, description, github_pat: pat, auto_deploy: autoDeploy,
        git_repo_url: repoURL.trim(),
        git_branch: branchClean,
        services: servicesWithRepo,
      });
      toast.success("Project created and first deploy running");
      const created = res?.data?.data?.project ?? null;
      onCreated(created);
    } catch (e: any) {
      const be = tryExtractBuildError(e);
      if (be) {
        setBuildError(be);
        toast.error(`${be.stage} failed: ${be.summary}`);
      } else {
        const raw = e?.response?.data?.error?.message || "Failed to create project";
        const msg = raw.includes("duplicate key") && raw.includes("slug")
          ? "A project with a very similar name already exists — try a different name."
          : raw;
        toast.error(msg);
      }
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal isOpen onClose={onClose} title="New Software Project" size="xl">
      <div className="space-y-4">
        {/* Step indicator */}
        <div className="flex items-center gap-2 text-xs">
          {([1, 2, 3] as const).map((n, i) => (
            <div key={n} className="flex items-center gap-2">
              <span className={`w-6 h-6 rounded-full flex items-center justify-center text-[10px] ${step >= n ? "bg-blue-600 text-white" : "bg-panel-bg text-panel-muted border border-panel-border"}`}>{n}</span>
              <span className={step === n ? "text-panel-text font-medium" : "text-panel-muted"}>
                {n === 1 ? "Basics" : n === 2 ? "Services" : "Review"}
              </span>
              {i < 2 && <span className="text-panel-muted/40 mx-1">→</span>}
            </div>
          ))}
        </div>

        {step === 1 && (
          <div className="space-y-4">
            <div>
              <LabelWithHint required hint="A human-friendly name for this software. A url-safe slug is derived automatically and used for install paths, systemd unit names, and the webhook URL.">Project name</LabelWithHint>
              <input className={inputCls} value={name} onChange={(e) => setName(e.target.value)} placeholder="MyShop" />
              {name.trim() !== "" && (
                <p className="text-[11px] text-panel-muted mt-1">
                  URL slug: <code className="px-1 py-0.5 bg-panel-bg border border-panel-border rounded text-panel-text">{slugifyProjectName(name) || "(empty — pick a name with at least one letter or digit)"}</code>
                </p>
              )}
            </div>
            <div>
              <LabelWithHint hint="Optional short description shown in the project list. Doesn't affect deploys.">Description</LabelWithHint>
              <input className={inputCls} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Customer-facing storefront + admin panel" />
            </div>
            <div className="grid grid-cols-3 gap-3">
              <div className="col-span-2">
                <LabelWithHint required hint="The HTTPS URL of the GitHub repository this project is deployed from. Every service in the project clones from this URL — each service can still pick a different subpath in the next step (monorepo-friendly).">Repository URL</LabelWithHint>
                <input
                  className={inputCls}
                  value={repoURL}
                  onChange={(e) => setRepoURL(e.target.value.trim())}
                  placeholder="https://github.com/owner/repo.git"
                  autoComplete="off"
                  spellCheck={false}
                />
                {repoURL.trim() !== "" && !/^https:\/\/[^\s]+\/[^\s]+\/[^\s]+/.test(repoURL.trim()) && (
                  <p className="text-[11px] text-amber-400 mt-1">URL doesn't look like https://host/owner/repo — double-check before continuing.</p>
                )}
              </div>
              <div>
                <LabelWithHint required hint="Git branch every service in this project tracks. Single shared clone means one branch — split monorepo branches into separate projects if you need both.">Branch</LabelWithHint>
                <input
                  className={inputCls}
                  value={projectBranch}
                  onChange={(e) => setProjectBranch(e.target.value.trim())}
                  placeholder="main"
                  autoComplete="off"
                  spellCheck={false}
                />
              </div>
            </div>
            <div>
              <LabelWithHint hint="GitHub Personal Access Token used to clone private repos. Stored AES-GCM encrypted; only a masked preview is ever returned. Generate one at github.com/settings/tokens with 'repo' scope.">GitHub PAT</LabelWithHint>
              <PasswordInput
                autoComplete="new-password"
                inputClassName={inputCls}
                value={pat}
                onChange={setPat}
                placeholder="ghp_… (leave blank for public repos)"
                hideGenerator
              />
              <a
                href="https://github.com/settings/tokens/new?scopes=repo&description=Betazen%20Server%20Panel%20deploy%20token"
                target="_blank" rel="noopener noreferrer"
                className="inline-flex items-center gap-1 text-[11px] text-blue-400 hover:underline mt-1"
              >
                <ExternalLink size={11} /> Generate a PAT on GitHub
              </a>
            </div>
            <div className="flex items-center gap-2">
              <input type="checkbox" id="auto_deploy" checked={autoDeploy} onChange={(e) => setAutoDeploy(e.target.checked)}
                className="rounded border-panel-border bg-panel-bg text-blue-600 focus:ring-blue-500" />
              <label htmlFor="auto_deploy" className="text-sm text-panel-text inline-flex items-center gap-1">
                Auto-deploy on push <FieldHint text="When enabled, a GitHub push triggers redeploys of services whose subpath matches the changed files." />
              </label>
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <button onClick={onClose} className="px-4 py-2 text-sm text-panel-muted border border-panel-border rounded-lg">Cancel</button>
              <button
                onClick={() => {
                  setServices((ss) => ss.map((s) => ({ ...s, git_repo_url: repoURL.trim() })));
                  setStep(2);
                }}
                disabled={!name.trim() || !repoURL.trim()}
                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50"
              >
                Next: Services
              </button>
            </div>
          </div>
        )}

        {step === 2 && (
          <div className="space-y-4 max-h-[70vh] overflow-y-auto pr-1">
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 px-3 py-2 text-[11px] text-blue-200/80 flex items-center gap-2">
              <GitBranch size={12} className="shrink-0" />
              <span>
                All services in this project clone from <code className="text-panel-text">{repoURL || "(repo URL)"}</code> and live under your home directory. Each service can pick its own branch + subpath.
              </span>
            </div>
            {availableDomains.length === 0 && (
              <div className="rounded-lg border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-[11px] text-amber-300">
                <strong className="text-amber-200">No domains available.</strong> Add a domain under <b>My Domains</b> first, then come back here.
              </div>
            )}
            {services.map((svc, i) => (
              <ServiceCard
                key={i}
                idx={i}
                svc={svc}
                presets={presets}
                serverIP={serverIP}
                availableDomains={availableDomains}
                hideRepoURL
                onChange={(patch) => updateService(i, patch)}
                onPreset={(key) => applyPreset(i, key)}
                onRemove={services.length > 1 ? () => setServices((ss) => ss.filter((_, j) => j !== i)) : undefined}
              />
            ))}
            <button
              onClick={() => setServices((ss) => [...ss, { ...emptyService(), git_repo_url: repoURL.trim() }])}
              className="w-full px-4 py-3 border-2 border-dashed border-panel-border rounded-lg text-sm text-panel-muted hover:text-blue-400 hover:border-blue-500/40 transition-colors"
            >
              + Add another service
            </button>
            <div className="flex justify-between pt-2">
              <button onClick={() => setStep(1)} className="px-4 py-2 text-sm text-panel-muted border border-panel-border rounded-lg">Back</button>
              <button
                onClick={() => setStep(3)}
                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg"
              >
                Next: Review
              </button>
            </div>
          </div>
        )}

        {step === 3 && !saving && (
          <div className="space-y-4">
            <div className="rounded-lg border border-panel-border p-4 space-y-3">
              <div className="flex items-center gap-2">
                <PackageOpen size={14} className="text-blue-400" />
                <span className="font-medium text-panel-text">{name || "(unnamed)"}</span>
                {autoDeploy && <span className="text-[10px] px-1.5 py-0.5 rounded bg-green-500/10 text-green-400 border border-green-500/20">auto-deploy</span>}
              </div>
              {description && <p className="text-xs text-panel-muted">{description}</p>}
              <div className="text-xs text-panel-muted flex items-center gap-3">
                <span className="inline-flex items-center gap-1"><Server size={11} /> slug: <code className="text-panel-text">{slugifyProjectName(name) || "?"}</code></span>
                {pat ? <span className="inline-flex items-center gap-1"><KeyRound size={11} /> PAT encrypted</span> : <span>No PAT (public repos only)</span>}
              </div>
              <div className="divide-y divide-panel-border pt-2 -mx-4">
                {services.map((s, i) => (
                  <div key={i} className="px-4 py-2 text-xs text-panel-muted">
                    <div className="flex items-center gap-2 text-panel-text">
                      <Layers size={11} />
                      <b>{s.name || "(no name)"}</b>
                      <span className="text-[10px] px-1 py-0.5 rounded bg-panel-bg border border-panel-border">{s.role}</span>
                      {s.framework && <span className="text-[10px] text-blue-400">{s.framework}</span>}
                    </div>
                    <div className="mt-1">
                      {s.primary_domain || "(no domain)"}{s.alias_domains.length > 0 && <> + {s.alias_domains.length} alias{s.alias_domains.length === 1 ? "" : "es"}</>}
                      {s.path_prefix && s.role === "backend" && <> • mounted at <code>{s.path_prefix}</code></>}
                    </div>
                  </div>
                ))}
              </div>
            </div>
            <div className="rounded-lg border border-yellow-500/20 bg-yellow-500/5 p-3 text-[12px] text-yellow-200/80 flex gap-2">
              <AlertCircle size={14} className="mt-0.5 flex-shrink-0" />
              <span>After create: the wizard will show live progress for each service. Typical clone + install + build takes 30–90 seconds; SSL issuance adds ~10s on new domains.</span>
            </div>
            <div className="flex justify-between pt-2">
              <button onClick={() => setStep(2)} className="px-4 py-2 text-sm text-panel-muted border border-panel-border rounded-lg">Back</button>
              <button
                onClick={handleCreate}
                disabled={saving}
                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50"
              >
                Create & deploy
              </button>
            </div>
          </div>
        )}

        {step === 3 && saving && (
          <div className="space-y-4">
            <div className="rounded-lg border border-panel-border bg-panel-bg/40 p-4 space-y-3">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2 text-panel-text">
                  <Rocket size={14} className="text-blue-400 animate-pulse" />
                  <span className="font-semibold">Deploying <code className="text-panel-text">{name}</code>…</span>
                </div>
                <div className="flex items-center gap-2 text-xs text-panel-muted tabular-nums">
                  <span>{Math.floor(elapsedSec)}s</span>
                  <span className="text-blue-300 font-mono">{progressPct}%</span>
                </div>
              </div>
              <div className="h-2 w-full bg-panel-bg rounded-full overflow-hidden">
                <div
                  className="h-full bg-gradient-to-r from-blue-500 to-blue-400 rounded-full transition-all duration-500"
                  style={{ width: `${progressPct}%` }}
                />
              </div>
              <div className="space-y-2 pt-2">
                {services.map((s, i) => (
                  <div key={i} className="rounded-lg border border-panel-border/60 bg-panel-bg/50 p-3">
                    <div className="flex items-center gap-2 text-xs text-panel-text mb-2">
                      <Layers size={11} className="text-blue-400" />
                      <b>{s.name}</b>
                      <span className="text-[10px] px-1 py-0.5 rounded bg-panel-bg border border-panel-border">{s.role}</span>
                      {s.framework && <span className="text-[10px] text-blue-400">{s.framework}</span>}
                      <span className="ml-auto text-panel-muted/70">{s.primary_domain}</span>
                    </div>
                    <div className="space-y-1">
                      {provisionSteps.map((stp, idx) => {
                        const done = idx < currentStepIdx;
                        const active = idx === currentStepIdx;
                        return (
                          <div key={idx} className="flex items-center gap-2 text-[11px]">
                            <span className="w-3.5 flex items-center justify-center">
                              {done ? <Check size={11} className="text-green-400" /> :
                                active ? <RotateCw size={11} className="text-blue-400 animate-spin" /> :
                                  <span className="w-2 h-2 rounded-full bg-panel-border/40" />}
                            </span>
                            <span className={
                              done ? "text-panel-muted/70" :
                                active ? "text-blue-300" :
                                  "text-panel-muted/60"
                            }>
                              {stp.label}
                            </span>
                            {active && <span className="text-panel-muted/50 text-[10px] tabular-nums">~{stp.seconds}s</span>}
                          </div>
                        );
                      })}
                    </div>
                  </div>
                ))}
              </div>
              <div className="text-[11px] text-panel-muted/70 pt-1 flex items-start gap-1.5">
                <AlertCircle size={11} className="mt-0.5 flex-shrink-0" />
                <span>The wizard will close automatically when provisioning completes. If a step fails, the full output appears here. Don't close this dialog — the deploy is still running on the server.</span>
              </div>
            </div>
            <div className="flex justify-end pt-2">
              <button
                disabled
                className="px-4 py-2 text-sm bg-blue-600 text-white rounded-lg opacity-50 cursor-not-allowed flex items-center gap-2"
              >
                <RotateCw size={14} className="animate-spin" />
                Provisioning… ({Math.floor(elapsedSec)}s)
              </button>
            </div>
          </div>
        )}
      </div>
      {buildError && (
        <BuildErrorModal info={buildError} onClose={() => setBuildError(null)} />
      )}
    </Modal>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Per-service form card
// ──────────────────────────────────────────────────────────────────────────

function ServiceCard({
  idx, svc, presets, serverIP, availableDomains, onChange, onPreset, onRemove, hideRepoURL,
}: {
  idx: number;
  svc: NewServiceForm;
  presets: Record<string, Preset>;
  serverIP: string;
  availableDomains: DomainOption[];
  onChange: (patch: Partial<NewServiceForm>) => void;
  onPreset: (key: string) => void;
  onRemove?: () => void;
  hideRepoURL?: boolean;
}) {
  const [aliasInput, setAliasInput] = useState("");
  const [envKey, setEnvKey] = useState("");
  const [envVal, setEnvVal] = useState("");

  function addAlias() {
    const d = aliasInput.trim().toLowerCase();
    if (!d) return;
    if (!isLikelyDomain(d)) {
      toast.error(`"${d}" doesn't look like a domain (need at least one dot, only a-z 0-9 and '-')`);
      return;
    }
    if (svc.alias_domains.includes(d) || d === svc.primary_domain) {
      toast.error(`"${d}" is already in the list`);
      return;
    }
    onChange({ alias_domains: [...svc.alias_domains, d] });
    setAliasInput("");
  }

  function removeAlias(d: string) {
    onChange({ alias_domains: svc.alias_domains.filter((a) => a !== d) });
  }

  function addEnv() {
    if (!envKey.trim()) return;
    onChange({ env_vars: { ...svc.env_vars, [envKey]: envVal } });
    setEnvKey(""); setEnvVal("");
  }

  function removeEnv(k: string) {
    const next = { ...svc.env_vars };
    delete next[k];
    onChange({ env_vars: next });
  }

  return (
    <div className="border border-panel-border rounded-lg p-4 space-y-3 bg-panel-bg/30">
      <div className="flex items-center justify-between">
        <div className="text-sm font-medium text-panel-text">Service #{idx + 1}</div>
        {onRemove && (
          <button onClick={onRemove} className="text-panel-muted hover:text-red-400 transition-colors">
            <X size={14} />
          </button>
        )}
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div>
          <LabelWithHint required hint="Unique per project. Used as the systemd unit suffix and the on-disk directory name. Lowercase, a-z 0-9 and dashes. Spaces are auto-converted to dashes.">Name</LabelWithHint>
          {(() => {
            const err = svc.name === "" ? null : validateServiceName(svc.name);
            return (
              <>
                <div className="relative">
                  <input
                    className={err ? inputInvalidCls : inputCls}
                    value={svc.name}
                    onChange={(e) => onChange({ name: sanitizeServiceName(e.target.value) })}
                    placeholder="api / web / admin"
                  />
                  {svc.name && !err && (
                    <Check size={14} className="absolute right-2 top-1/2 -translate-y-1/2 text-green-400 pointer-events-none" />
                  )}
                </div>
                {err && <p className="text-[11px] text-red-400 mt-1">{err}</p>}
              </>
            );
          })()}
        </div>
        <div>
          <LabelWithHint required hint="backend = long-running process proxied by nginx. frontend = built bundle served statically. static = plain HTML/CSS/JS with no build step.">Role</LabelWithHint>
          <select className={selectCls} value={svc.role} onChange={(e) => onChange({ role: e.target.value as any })}>
            <option value="backend">backend</option>
            <option value="frontend">frontend</option>
            <option value="static">static</option>
          </select>
        </div>
      </div>

      <div>
        <LabelWithHint hint="Pre-fills install/build/start commands and the default port. Pick 'Custom' (leave blank) to enter commands manually.">Framework preset</LabelWithHint>
        <select className={selectCls} value={svc.framework} onChange={(e) => onPreset(e.target.value)}>
          <option value="">— custom —</option>
          {Object.entries(presets).map(([k, p]) => (
            <option key={k} value={k}>{p.label}</option>
          ))}
        </select>
      </div>

      {/* Branch hoisted to project level in 3.1.27. Wizard's Basics
          step + Add Service inherits from the project; per-service
          card no longer collects it. */}
      {!hideRepoURL && (
        <div>
          <LabelWithHint required hint="HTTPS URL to the Git repo. For private repos the project's stored PAT is injected into the URL for git operations.">Repository URL</LabelWithHint>
          <input className={inputCls} value={svc.git_repo_url} onChange={(e) => onChange({ git_repo_url: e.target.value })} placeholder="https://github.com/org/repo.git" />
        </div>
      )}

      <div className="grid grid-cols-2 gap-3">
        <div>
          <LabelWithHint hint="Monorepo subdirectory, e.g. 'apps/api'. Only the subdirectory is moved into the install dir, and the webhook only redeploys this service when files under this subpath change.">Subpath (monorepo)</LabelWithHint>
          <input className={inputCls} value={svc.git_subpath} onChange={(e) => onChange({ git_subpath: e.target.value })} placeholder="apps/api" />
        </div>
        {svc.role === "backend" && (
          <div>
            <LabelWithHint hint="nginx location prefix under which the backend is mounted. Use '/' for the whole domain, or '/api' to run alongside a frontend on the same domain.">Path prefix</LabelWithHint>
            <input className={inputCls} value={svc.path_prefix} onChange={(e) => onChange({ path_prefix: e.target.value })} placeholder="/ or /api" />
          </div>
        )}
      </div>

      <div className="grid grid-cols-3 gap-3">
        <div className="col-span-2">
          <LabelWithHint required hint="Pick a domain already registered under My Domains. The DNS A record must point at this server's IP.">Primary domain</LabelWithHint>
          <PrimaryDomainSelect
            value={svc.primary_domain}
            domains={availableDomains}
            onChange={(v) => onChange({ primary_domain: v })}
          />
        </div>
        {svc.role === "backend" && (
          <div>
            <LabelWithHint hint="TCP port the backend listens on. Leave 0 to auto-allocate a free port.">Port</LabelWithHint>
            <input type="number" className={inputCls} value={svc.port || ""} onChange={(e) => onChange({ port: parseInt(e.target.value) || 0 })} placeholder="3000" />
          </div>
        )}
      </div>

      {(svc.role === "frontend" || svc.role === "static") && (
        <div>
          <LabelWithHint hint="Additional domains that should serve the same site. Each alias needs a CNAME record pointing at the primary domain.">Alias domains</LabelWithHint>
          <div className="flex gap-2">
            <input
              className={inputCls}
              value={aliasInput}
              onChange={(e) => setAliasInput(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); addAlias(); } }}
              placeholder="www.example.com"
            />
            <button onClick={addAlias} className="px-3 py-2 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text">Add</button>
          </div>
          {svc.alias_domains.length > 0 && (
            <div className="flex flex-wrap gap-1 mt-2">
              {svc.alias_domains.map((d) => (
                <span key={d} className="inline-flex items-center gap-1 px-2 py-0.5 text-[11px] bg-panel-bg border border-panel-border rounded text-panel-muted">
                  {d}
                  <button onClick={() => removeAlias(d)} className="text-panel-muted/60 hover:text-red-400"><X size={10} /></button>
                </span>
              ))}
            </div>
          )}
        </div>
      )}

      {svc.primary_domain && (
        <DnsHint role={svc.role} primary={svc.primary_domain} aliases={svc.alias_domains} serverIP={serverIP} />
      )}

      {(svc.role === "backend" || svc.framework) && (
        <Disclosure title="Build commands (install / build / start)" icon={<Server size={13} />} defaultOpen={!svc.framework}>
          <div className="space-y-2">
            <div>
              <LabelWithHint hint="Runs once per deploy to fetch dependencies (npm install, pip install, go mod download, …).">Install command</LabelWithHint>
              <input className={inputCls} value={svc.install_cmd} onChange={(e) => onChange({ install_cmd: e.target.value })} placeholder="npm install --omit=dev" />
            </div>
            <div>
              <LabelWithHint hint="Optional build/bundle/compile step (npm run build, go build, …). Leave blank for interpreted-only stacks.">Build command</LabelWithHint>
              <input className={inputCls} value={svc.build_cmd} onChange={(e) => onChange({ build_cmd: e.target.value })} placeholder="npm run build" />
            </div>
            {svc.role === "backend" && (
              <div>
                <LabelWithHint required hint="What systemd ExecStarts. Use ${PORT} to substitute the allocated port at runtime.">Start command</LabelWithHint>
                <input className={inputCls} value={svc.start_cmd} onChange={(e) => onChange({ start_cmd: e.target.value })} placeholder="node server.js" />
              </div>
            )}
          </div>
        </Disclosure>
      )}

      <Disclosure title={`Environment variables (${Object.keys(svc.env_vars).length})`} icon={<KeyRound size={13} />}>
        <div className="space-y-2">
          {Object.entries(svc.env_vars).map(([k, v]) => (
            <div key={k} className="flex items-center gap-2 text-xs">
              <code className="px-2 py-1 bg-panel-bg border border-panel-border rounded text-panel-muted flex-1">{k}={v}</code>
              <button onClick={() => removeEnv(k)} className="p-1 text-panel-muted hover:text-red-400"><X size={12} /></button>
            </div>
          ))}
          <div className="flex gap-2">
            <input className={inputCls} value={envKey} onChange={(e) => setEnvKey(e.target.value)} placeholder="KEY" />
            <input className={inputCls} value={envVal} onChange={(e) => setEnvVal(e.target.value)} placeholder="value" />
            <button onClick={addEnv} className="px-3 py-2 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text">Add</button>
          </div>
        </div>
      </Disclosure>
    </div>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Primary domain picker
// ──────────────────────────────────────────────────────────────────────────

function PrimaryDomainSelect({ value, domains, onChange }: { value: string; domains: DomainOption[]; onChange: (v: string) => void }) {
  if (domains.length === 0) {
    return (
      <div className="space-y-1">
        <select className={selectCls} disabled>
          <option>No domains registered yet</option>
        </select>
        <div className="text-[11px] text-amber-400/80">
          Add a domain under <b>My Domains</b> first, then come back here to deploy.
        </div>
      </div>
    );
  }
  // Type-ahead dropdown — same component the WHM Deploy Software
  // page uses. A vendor's My Domains list can run into the dozens
  // for active resellers; scrolling without filter is hostile.
  const options: { value: string; label: string; hint?: string }[] =
    domains.map((d) => ({ value: d.domain, label: d.domain }));
  if (value && !options.some((o) => o.value === value)) {
    options.push({ value, label: value, hint: "(not registered)" });
  }
  return (
    <SearchableSelect
      value={value}
      onChange={onChange}
      options={options}
      placeholder="— select a domain —"
      emptyMessage="No domains match — clear the filter to pick from the full list."
    />
  );
}

// ──────────────────────────────────────────────────────────────────────────
// DNS hint block
// ──────────────────────────────────────────────────────────────────────────

function DnsHint({ role, primary, aliases, serverIP }: { role: string; primary: string; aliases: string[]; serverIP: string }) {
  if (role === "backend") {
    return (
      <div className="rounded-lg border border-blue-500/20 bg-blue-500/5 p-3 text-[11px] space-y-1">
        <div className="flex items-center gap-1.5 text-blue-400 font-medium"><Globe size={12} /> Required DNS</div>
        <div className="text-panel-muted">
          <code>A  {primary}  →  {serverIP || "YOUR_SERVER_IP"}</code>
        </div>
      </div>
    );
  }
  return (
    <div className="rounded-lg border border-blue-500/20 bg-blue-500/5 p-3 text-[11px] space-y-1">
      <div className="flex items-center gap-1.5 text-blue-400 font-medium"><Globe size={12} /> Required DNS records</div>
      <div className="text-panel-muted space-y-0.5">
        <div><code>A      {primary}{"  "}→  {serverIP || "YOUR_SERVER_IP"}</code></div>
        {aliases.map((a) => (
          <div key={a}><code>CNAME  {a}{"  "}→  {primary}</code></div>
        ))}
      </div>
      <div className="text-panel-muted/70 text-[10px] pt-1">Let's Encrypt issues one certificate covering all of these (SAN list) after DNS propagates.</div>
    </div>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Project detail drawer
// ──────────────────────────────────────────────────────────────────────────

function ProjectDetailDrawer({
  project, serverIP, presets, availableDomains, onClose, onChanged,
}: {
  project: Project;
  serverIP: string;
  presets: Record<string, Preset>;
  availableDomains: DomainOption[];
  onClose: () => void;
  onChanged: () => void;
}) {
  const [services, setServices] = useState<ProjectService[]>([]);
  const [webhook, setWebhook] = useState<WebhookInfo | null>(null);
  const [activity, setActivity] = useState<ProjectActivity | null>(null);
  const fetchActivity = () => {
    api.get(`/projects/${project.id}/activity`).then((r) => setActivity(r.data?.data || null)).catch(() => {});
  };
  const [logsFor, setLogsFor] = useState<ProjectService | null>(null);
  const [addingService, setAddingService] = useState(false);
  const [rotating, setRotating] = useState(false);
  const [newPAT, setNewPAT] = useState("");
  const [secretRevealed, setSecretRevealed] = useState(false);
  const [regenerating, setRegenerating] = useState(false);
  const [confirmingRegen, setConfirmingRegen] = useState(false);
  const [editingProject, setEditingProject] = useState(false);
  const [editingService, setEditingService] = useState<ProjectService | null>(null);

  const [actionInFlight, setActionInFlight] = useState<null | "deploy" | "restart" | "stop" | "start" | "pause" | "pull">(null);

  useEffect(() => {
    refresh();
    api.get(`/projects/${project.id}/webhook`).then((r) => setWebhook(r.data?.data || null)).catch(() => {});
    fetchActivity();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project.id]);

  const backendSvcs = services.filter((s) => s.role === "backend");
  const runningCount = backendSvcs.filter((s) => s.status === "running").length;
  const stoppedCount = backendSvcs.filter((s) => s.status === "stopped").length;
  const deployingCount = backendSvcs.filter((s) => s.status === "deploying" || s.status === "pending").length;
  const errorCount = backendSvcs.filter((s) => s.status === "error" || s.status === "failed").length;
  const totalBackends = backendSvcs.length;
  const allRunning = totalBackends > 0 && runningCount === totalBackends;
  const allStopped = totalBackends > 0 && stoppedCount === totalBackends;

  useEffect(() => {
    const pending = services.some((s) => s.status === "deploying" || s.status === "pending" || s.status === "queue-full");
    if (!pending) return;
    const id = setInterval(refresh, 3000);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [services, project.id]);

  async function refresh() {
    try {
      const r = await api.get(`/projects/${project.id}/services`);
      setServices(r.data?.data || []);
    } catch { /* ignore */ }
  }

  async function handleDeployService(svc: ProjectService) {
    try {
      await api.post(`/projects/${project.id}/services/${svc.id}/deploy`);
      toast.success(`${svc.name}: deploy queued`);
      setTimeout(refresh, 1000);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Deploy failed");
    }
  }

  async function handleDeployAll() {
    setActionInFlight("deploy");
    try {
      await api.post(`/projects/${project.id}/deploy`);
      toast.success("Deploy all queued");
      await refresh();
      setTimeout(refresh, 1500);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    } finally {
      setActionInFlight(null);
    }
  }

  async function handleRemoveService(svc: ProjectService) {
    if (!await confirmAction({ title: "Delete?", description: `Remove service "${svc.name}"? This stops the process, removes nginx config, and deletes the code.`, danger: true, confirmLabel: "Remove" })) return;
    try {
      await api.delete(`/projects/${project.id}/services/${svc.id}`);
      toast.success("Service removed");
      refresh();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    }
  }

  async function handleRotatePAT() {
    if (!newPAT.trim()) return;
    setRotating(true);
    try {
      await api.post(`/projects/${project.id}/rotate-pat`, { github_pat: newPAT });
      toast.success(
        (t) => (
          <div className="flex items-start gap-2 text-xs leading-relaxed">
            <CheckCircle size={16} className="text-green-400 mt-0.5 shrink-0" />
            <div className="space-y-1">
              <div className="font-semibold text-panel-text">GitHub PAT rotated</div>
              <div className="text-panel-muted">
                Future clones / pulls use the new token. The webhook URL +
                secret are unchanged — GitHub deliveries keep working.
              </div>
              <button
                onClick={() => toast.dismiss(t.id)}
                className="px-2 py-0.5 text-[11px] text-panel-muted hover:text-panel-text"
              >
                Dismiss
              </button>
            </div>
          </div>
        ),
        { duration: 10000 }
      );
      setNewPAT("");
      onChanged();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    } finally {
      setRotating(false);
    }
  }

  // Two-step confirm regen — same UX as the WHM app.
  async function handleRegenerateWebhookSecret() {
    if (!confirmingRegen) {
      setConfirmingRegen(true);
      setTimeout(() => setConfirmingRegen(false), 5000);
      return;
    }
    setConfirmingRegen(false);
    setRegenerating(true);
    try {
      const res = await api.post(`/projects/${project.id}/regenerate-webhook-secret`);
      const newSecret: string = res.data?.data?.secret || "";
      if (!newSecret) throw new Error("Server didn't return a new secret");
      copyToClipboard(newSecret);
      const ghSettingsURL = (project.git_repo_url || "").replace(/\.git$/i, "") + "/settings/hooks";
      toast.success(
        (t) => (
          <div className="flex items-start gap-2 text-xs leading-relaxed max-w-md">
            <AlertTriangle size={16} className="text-amber-400 mt-0.5 shrink-0" />
            <div className="space-y-1.5">
              <div className="font-semibold text-panel-text">
                Webhook secret regenerated &amp; copied to clipboard
              </div>
              <div className="text-panel-muted">
                The old secret is gone. GitHub deliveries will fail
                signature verification until you update the webhook's
                Secret field on GitHub.
              </div>
              <code className="block px-2 py-1 bg-panel-bg border border-panel-border rounded text-[10px] break-all">
                {newSecret}
              </code>
              <div className="flex gap-2 pt-0.5 flex-wrap">
                <button
                  onClick={() => { copyToClipboard(newSecret); toast.success("Copied again", { duration: 1500 }); }}
                  className="px-2 py-0.5 text-[11px] bg-blue-600 hover:bg-blue-700 text-white rounded"
                >
                  Copy
                </button>
                {/^https:\/\/github\.com\//i.test(project.git_repo_url || "") && (
                  <a
                    href={ghSettingsURL}
                    target="_blank"
                    rel="noreferrer"
                    className="px-2 py-0.5 text-[11px] bg-panel-bg border border-panel-border hover:border-blue-500/50 text-panel-text rounded"
                  >
                    Open GitHub webhooks
                  </a>
                )}
                <button
                  onClick={() => toast.dismiss(t.id)}
                  className="px-2 py-0.5 text-[11px] text-panel-muted hover:text-panel-text"
                >
                  Dismiss
                </button>
              </div>
            </div>
          </div>
        ),
        { duration: 30000 }
      );
      refresh();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || e?.message || "Failed to regenerate webhook secret");
    } finally {
      setRegenerating(false);
    }
  }

  async function handleAddAlias(svc: ProjectService, domain: string) {
    try {
      await api.post(`/projects/${project.id}/services/${svc.id}/aliases`, { domain });
      toast.success("Alias added");
      refresh();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    }
  }

  async function handleRemoveAlias(svc: ProjectService, domain: string) {
    try {
      await api.delete(`/projects/${project.id}/services/${svc.id}/aliases/${encodeURIComponent(domain)}`);
      toast.success("Alias removed");
      refresh();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    }
  }

  async function handleServiceAction(svc: ProjectService, action: "start" | "stop" | "restart" | "run" | "install" | "build") {
    const verb = action === "install" ? "Installing packages" : action === "build" ? "Building" : action === "run" ? "Starting" : `${action.charAt(0).toUpperCase()}${action.slice(1)}ing`;
    const t = toast.loading(`${svc.name}: ${verb}…`);
    try {
      await api.post(`/projects/${project.id}/services/${svc.id}/action/${action}`);
      toast.success(`${svc.name}: ${action} complete`, { id: t });
      refresh();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || `${action} failed`, { id: t });
    }
  }

  async function handleProjectAction(action: "start" | "stop" | "restart" | "pull") {
    if (action === "stop" && !await confirmAction({ title: "Stop?", description: `Stop every backend service in "${project.name}"?`, danger: true, confirmLabel: "Stop" })) return;
    setActionInFlight(action);
    try {
      await api.post(`/projects/${project.id}/action/${action}`);
      toast.success(`Project ${action} complete`);
      await refresh();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    } finally {
      setActionInFlight(null);
    }
  }

  async function handleTogglePause() {
    const nextPaused = !project.paused;
    setActionInFlight("pause");
    try {
      await api.post(`/projects/${project.id}/${nextPaused ? "pause" : "resume"}`);
      toast.success(nextPaused ? "Auto-deploy paused" : "Auto-deploy resumed");
      onChanged();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    } finally {
      setActionInFlight(null);
    }
  }

  return (
    <Modal isOpen onClose={onClose} title={project.name} size="xl">
      <div className="space-y-5">
        {/* Project toolbar */}
        <div className="flex flex-wrap items-center gap-2 text-xs">
          {totalBackends > 0 && (
            <span
              className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-panel-bg border border-panel-border text-panel-muted"
              title={`${runningCount} running · ${stoppedCount} stopped${deployingCount ? ` · ${deployingCount} deploying` : ""}${errorCount ? ` · ${errorCount} error` : ""}`}
            >
              <span className={`w-1.5 h-1.5 rounded-full ${
                deployingCount > 0 ? "bg-blue-400 animate-pulse" :
                  errorCount > 0 ? "bg-red-400" :
                    allRunning ? "bg-green-400" :
                      allStopped ? "bg-panel-muted" :
                        "bg-amber-400"
              }`} />
              <span className="text-panel-text font-medium tabular-nums">{runningCount}</span>
              <span>/</span>
              <span className="tabular-nums">{totalBackends}</span>
              <span>running</span>
              {deployingCount > 0 && <span className="text-blue-300 ml-1">· {deployingCount} deploying</span>}
              {errorCount > 0 && <span className="text-red-400 ml-1">· {errorCount} error</span>}
            </span>
          )}

          <button
            onClick={handleDeployAll}
            disabled={actionInFlight !== null}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg transition-opacity"
            title="Pull + rebuild + restart every service"
          >
            {actionInFlight === "deploy" ? <RotateCw size={13} className="animate-spin" /> : <Rocket size={13} />}
            {actionInFlight === "deploy" ? "Queueing…" : "Deploy all"}
          </button>

          <button
            onClick={() => handleProjectAction("pull")}
            disabled={actionInFlight !== null}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border border-panel-border hover:border-blue-500/40 disabled:opacity-50 disabled:cursor-not-allowed rounded-lg text-panel-text transition-opacity"
            title="git pull on the project's shared clone — fetches new commits without rebuild."
          >
            {actionInFlight === "pull" ? <RotateCw size={13} className="animate-spin text-blue-400" /> : <GitBranch size={13} />}
            {actionInFlight === "pull" ? "Pulling…" : "Pull"}
          </button>

          <button
            onClick={() => handleProjectAction("restart")}
            disabled={actionInFlight !== null || totalBackends === 0}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border border-panel-border hover:border-blue-500/40 disabled:opacity-50 disabled:cursor-not-allowed rounded-lg text-panel-text transition-opacity"
            title="systemctl restart every backend (no rebuild)"
          >
            {actionInFlight === "restart" ? <RotateCw size={13} className="animate-spin text-blue-400" /> : <RotateCw size={13} />}
            {actionInFlight === "restart" ? "Restarting…" : "Restart all"}
          </button>

          <button
            onClick={() => handleProjectAction("stop")}
            disabled={actionInFlight !== null || totalBackends === 0 || allStopped}
            className={`inline-flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border rounded-lg transition-opacity disabled:cursor-not-allowed ${
              allStopped ? "border-panel-border text-panel-muted/40" : "border-panel-border hover:border-amber-500/40 text-panel-text"
            } disabled:opacity-50`}
            title={allStopped ? "All services already stopped" : "systemctl stop every backend"}
          >
            {actionInFlight === "stop" ? <RotateCw size={13} className="animate-spin text-amber-400" /> : <Square size={13} />}
            {actionInFlight === "stop" ? "Stopping…" : "Stop all"}
          </button>

          <button
            onClick={() => handleProjectAction("start")}
            disabled={actionInFlight !== null || totalBackends === 0 || allRunning}
            className={`inline-flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border rounded-lg transition-opacity disabled:cursor-not-allowed ${
              allRunning ? "border-panel-border text-panel-muted/40" : "border-panel-border hover:border-green-500/40 text-panel-text"
            } disabled:opacity-50`}
            title={allRunning ? "All services already running" : "systemctl start every stopped backend"}
          >
            {actionInFlight === "start" ? <RotateCw size={13} className="animate-spin text-green-400" /> : <Power size={13} />}
            {actionInFlight === "start" ? "Starting…" : "Start all"}
          </button>

          <button
            onClick={handleTogglePause}
            disabled={actionInFlight !== null}
            className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border disabled:opacity-50 disabled:cursor-not-allowed transition-opacity ${
              project.paused ? "border-amber-500/40 text-amber-400 bg-amber-500/5" : "border-panel-border text-panel-text bg-panel-surface"
            }`}
            title={project.paused ? "Webhooks still recorded, but deploys are paused" : "Pause auto-deploy (webhooks still recorded)"}
          >
            {actionInFlight === "pause"
              ? <RotateCw size={13} className="animate-spin" />
              : project.paused ? <Play size={13} /> : <Pause size={13} />}
            {project.paused ? "Resume auto-deploy" : "Pause auto-deploy"}
          </button>

          <button
            onClick={() => setEditingProject(true)}
            disabled={actionInFlight !== null}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border border-panel-border hover:border-blue-500/40 disabled:opacity-50 disabled:cursor-not-allowed rounded-lg text-panel-text"
            title="Edit name, description, auto-deploy toggle"
          >
            <Pencil size={13} /> Edit
          </button>

          <button
            onClick={refresh}
            disabled={actionInFlight !== null}
            className="ml-auto inline-flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border border-panel-border hover:border-blue-500/40 disabled:opacity-50 disabled:cursor-not-allowed rounded-lg text-panel-muted hover:text-panel-text"
            title="Refresh service status now"
          >
            <RefreshCw size={13} /> Refresh
          </button>
        </div>

        {/* API IDs — copy-to-clipboard panel for the values an
            integrator needs when calling the External programmatic API
            (or the JWT panel API). Hidden behind a Disclosure so it
            doesn't clutter day-to-day ops. Service-level ids appear
            next to each service name in the Services list below. */}
        <Disclosure
          title="API / Developer IDs — for /api/v1/external/* calls"
          icon={<Code2 size={13} className="text-brand-400" />}
        >
          <div className="space-y-2 text-xs">
            <p className="text-panel-muted">
              Use these ids with the Programmatic API. Per-service ids appear next to each service name in the Services list below. See <a href="/docs/api/" target="_blank" rel="noopener noreferrer" className="text-brand-400 hover:underline">API docs</a> for the full route set.
            </p>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              <div>
                <div className="text-[10px] uppercase tracking-wider text-panel-muted/70 mb-1">Project ID</div>
                <div className="flex items-center gap-2">
                  <code className="flex-1 px-2 py-1 bg-panel-bg border border-panel-border rounded text-panel-text font-mono text-[11px] truncate">{project.id}</code>
                  <CopyButton value={project.id} />
                </div>
              </div>
              <div>
                <div className="text-[10px] uppercase tracking-wider text-panel-muted/70 mb-1">Project Slug</div>
                <div className="flex items-center gap-2">
                  <code className="flex-1 px-2 py-1 bg-panel-bg border border-panel-border rounded text-panel-text font-mono text-[11px] truncate">{project.slug}</code>
                  <CopyButton value={project.slug} />
                </div>
              </div>
              {project.user && (
                <div>
                  <div className="text-[10px] uppercase tracking-wider text-panel-muted/70 mb-1">Linux user</div>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 px-2 py-1 bg-panel-bg border border-panel-border rounded text-panel-text font-mono text-[11px] truncate">{project.user}</code>
                    <CopyButton value={project.user} />
                  </div>
                </div>
              )}
              {webhook?.url && (
                <div>
                  <div className="text-[10px] uppercase tracking-wider text-panel-muted/70 mb-1">GitHub webhook URL</div>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 px-2 py-1 bg-panel-bg border border-panel-border rounded text-panel-text font-mono text-[11px] truncate">{webhook.url}</code>
                    <CopyButton value={webhook.url} />
                  </div>
                </div>
              )}
            </div>
            <div className="pt-2 border-t border-panel-border/40">
              <div className="text-[10px] uppercase tracking-wider text-panel-muted/70 mb-1.5">Quick example</div>
              <pre className="text-[11px] bg-panel-bg border border-panel-border rounded p-2 overflow-x-auto whitespace-pre-wrap break-all leading-relaxed text-panel-text/90">{`# All services in this project (External API)
curl -H "Authorization: Bearer btz_…" \\
  "$PANEL/api/v1/external/deploy/services?search=${project.slug}"

# Link a domain to a service
curl -H "Authorization: Bearer btz_…" -H "Content-Type: application/json" \\
  -X POST "$PANEL/api/v1/external/deploy/projects/${project.id}/services/<service-id>/link-domain" \\
  -d '{"domain":"shop.example.com"}'`}</pre>
            </div>
          </div>
        </Disclosure>

        {/* Activity card */}
        {activity && (
          <Card>
            <div className="p-4 space-y-3">
              <div className="flex items-center justify-between">
                <div className="inline-flex items-center gap-2 text-sm font-medium text-panel-text">
                  <RotateCw size={15} className="text-blue-400" /> Activity
                </div>
                <button onClick={fetchActivity} className="text-xs text-panel-muted hover:text-panel-text inline-flex items-center gap-1">
                  <RefreshCw size={11} /> Refresh
                </button>
              </div>

              <div className="grid grid-cols-4 gap-2 text-xs">
                <div className="bg-panel-bg/50 rounded-lg p-2.5 border border-panel-border">
                  <div className="text-[10px] uppercase tracking-wider text-panel-muted">Total deploys</div>
                  <div className="text-lg font-semibold text-panel-text tabular-nums">{activity.deploys.total}</div>
                </div>
                <div className="bg-green-500/5 rounded-lg p-2.5 border border-green-500/20">
                  <div className="text-[10px] uppercase tracking-wider text-green-400/70">Successful</div>
                  <div className="text-lg font-semibold text-green-300 tabular-nums">{activity.deploys.successful}</div>
                </div>
                <div className="bg-red-500/5 rounded-lg p-2.5 border border-red-500/20">
                  <div className="text-[10px] uppercase tracking-wider text-red-400/70">Failed</div>
                  <div className="text-lg font-semibold text-red-300 tabular-nums">{activity.deploys.failed}</div>
                </div>
                <div className="bg-panel-bg/50 rounded-lg p-2.5 border border-panel-border">
                  <div className="text-[10px] uppercase tracking-wider text-panel-muted">Last deploy</div>
                  <div className="text-xs font-medium text-panel-text truncate" title={activity.deploys.last_at}>
                    {activity.deploys.last_at ? relativeTime(activity.deploys.last_at) : "—"}
                  </div>
                  {activity.deploys.last_by && (
                    <div className="text-[10px] text-panel-muted/70">via {activity.deploys.last_by}</div>
                  )}
                </div>
              </div>

              {activity.last_commit && (
                <div className="rounded-lg border border-panel-border bg-panel-bg/30 p-2.5 text-xs">
                  <div className="flex items-center gap-2 text-[10px] uppercase tracking-wider text-panel-muted mb-1">
                    <GitBranch size={11} /> Latest commit on disk
                  </div>
                  <div className="flex items-baseline gap-2">
                    <code className="px-1.5 py-0.5 bg-panel-bg rounded text-blue-300 font-mono text-[11px]">{activity.last_commit.short}</code>
                    <span className="text-panel-text font-medium truncate flex-1">{activity.last_commit.message}</span>
                  </div>
                  <div className="text-[10px] text-panel-muted/70 mt-0.5">
                    by {activity.last_commit.author} · {relativeTime(activity.last_commit.date)}
                  </div>
                </div>
              )}

              {(activity.recent_deployments?.length ?? 0) > 0 && (
                <div>
                  <div className="text-[10px] uppercase tracking-wider text-panel-muted mb-1.5">Recent deployments</div>
                  <div className="divide-y divide-panel-border rounded-lg border border-panel-border overflow-hidden">
                    {(activity.recent_deployments || []).map((d) => {
                      const dur = d.finished_at && d.started_at
                        ? Math.max(0, Math.round((new Date(d.finished_at).getTime() - new Date(d.started_at).getTime()) / 1000))
                        : null;
                      const statusColor = d.status === "running" || d.status === "success" ? "text-green-400 bg-green-500/10 border-green-500/30"
                        : d.status === "error" || d.status === "failed" ? "text-red-400 bg-red-500/10 border-red-500/30"
                          : "text-blue-400 bg-blue-500/10 border-blue-500/30";
                      return (
                        <div key={d.id} className="px-3 py-2 flex items-center gap-3 text-[11px] hover:bg-panel-bg/30">
                          <span className={`px-1.5 py-0.5 rounded border text-[10px] ${statusColor}`}>{d.status}</span>
                          <span className="text-panel-muted w-16 truncate">{d.trigger}</span>
                          <code className="text-blue-300 font-mono text-[10px]">{d.commit_sha ? d.commit_sha.substring(0, 7) : "—"}</code>
                          <span className="text-panel-muted/70 ml-auto" title={d.started_at}>{relativeTime(d.started_at)}</span>
                          {dur !== null && <span className="text-panel-muted/60 tabular-nums w-10 text-right">{dur}s</span>}
                        </div>
                      );
                    })}
                  </div>
                </div>
              )}

              {Object.keys(activity.runtime || {}).length > 0 && (
                <div>
                  <div className="text-[10px] uppercase tracking-wider text-panel-muted mb-1.5">Runtime</div>
                  <div className="divide-y divide-panel-border rounded-lg border border-panel-border overflow-hidden">
                    {Object.values(activity.runtime || {}).map((r) => (
                      <div key={r.service_id} className="px-3 py-2 flex items-center gap-3 text-[11px]">
                        <span className={`w-1.5 h-1.5 rounded-full ${
                          r.unit_state === "active" ? "bg-green-400" :
                            r.unit_state === "failed" ? "bg-red-400" : "bg-panel-muted"
                        }`} />
                        <span className="text-panel-text font-medium">{r.name}</span>
                        <span className="text-panel-muted">{r.unit_state || "—"}</span>
                        {r.uptime_sec > 0 && <span className="text-panel-muted ml-auto tabular-nums">up {formatUptime(r.uptime_sec)}</span>}
                        {r.memory_mb > 0 && <span className="text-panel-muted tabular-nums">{r.memory_mb} MB</span>}
                        {r.num_restarts > 0 && <span className="text-amber-300 tabular-nums">↻ {r.num_restarts}</span>}
                        {r.main_pid && r.main_pid !== "0" && <code className="text-panel-muted/70 text-[10px]">pid {r.main_pid}</code>}
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </Card>
        )}

        {/* Webhook card */}
        {webhook && (
          <Card>
            <div className="p-4 space-y-3">
              <div className="flex items-center justify-between">
                <div className="inline-flex items-center gap-2 text-sm font-medium text-panel-text">
                  <Webhook size={15} className="text-blue-400" /> GitHub Webhook
                </div>
                <LastWebhookBadge at={project.last_webhook_at} event={project.last_webhook_event} />
              </div>
              <div className="space-y-2 text-xs">
                <div className="flex items-center gap-2">
                  <span className="w-20 text-panel-muted">Payload URL</span>
                  <code className="flex-1 px-2 py-1 bg-panel-bg border border-panel-border rounded text-panel-text truncate">{webhook.url}</code>
                  <CopyButton value={webhook.url} />
                </div>
                <div className="flex items-center gap-2">
                  <span className="w-20 text-panel-muted">Secret</span>
                  <code className="flex-1 px-2 py-1 bg-panel-bg border border-panel-border rounded text-panel-text truncate">
                    {secretRevealed ? webhook.secret : "•".repeat(Math.min(webhook.secret.length, 32))}
                  </code>
                  <button
                    onClick={() => setSecretRevealed((r) => !r)}
                    className="p-1.5 text-panel-muted hover:text-panel-text"
                    title={secretRevealed ? "Hide" : "Reveal"}
                  >
                    {secretRevealed ? <EyeOff size={13} /> : <Eye size={13} />}
                  </button>
                  <CopyButton value={webhook.secret} />
                  <button
                    onClick={handleRegenerateWebhookSecret}
                    disabled={regenerating}
                    className={
                      "px-2 py-1.5 text-[11px] rounded inline-flex items-center gap-1 disabled:opacity-50 " +
                      (confirmingRegen
                        ? "bg-amber-600 hover:bg-amber-700 text-white"
                        : "bg-panel-bg border border-panel-border text-panel-muted hover:text-panel-text hover:border-amber-500/50")
                    }
                    title={confirmingRegen
                      ? "Click again to confirm. Old secret will stop working immediately — paste the new one into GitHub right after."
                      : "Mint a new secret. The old secret stops working immediately; you'll need to update GitHub's webhook Secret field."}
                  >
                    <RotateCw size={11} className={regenerating ? "animate-spin" : ""} />
                    {regenerating ? "…" : confirmingRegen ? "Confirm" : "Regenerate"}
                  </button>
                </div>
                <div className="flex items-center gap-2">
                  <span className="w-20 text-panel-muted">Content</span>
                  <code className="flex-1 px-2 py-1 bg-panel-bg border border-panel-border rounded text-panel-text">application/json</code>
                </div>
                <div className="flex items-center gap-2">
                  <span className="w-20 text-panel-muted">Events</span>
                  <code className="flex-1 px-2 py-1 bg-panel-bg border border-panel-border rounded text-panel-text">Just the push event</code>
                </div>
              </div>
              <Disclosure title="How to add this webhook in GitHub" icon={<HelpCircle size={13} className="text-blue-400" />}>
                <ol className="list-decimal ml-5 space-y-1.5">
                  <li>Open your repo on GitHub.</li>
                  <li>Go to <b>Settings → Webhooks → Add webhook</b>.</li>
                  <li>Paste the <b>Payload URL</b> above into <i>Payload URL</i>.</li>
                  <li>Set <b>Content type</b> to <code>application/json</code> (required — we parse JSON).</li>
                  <li>Paste the <b>Secret</b> above into <i>Secret</i> (used for HMAC-SHA256 verification).</li>
                  <li>Leave <b>SSL verification</b> on <i>Enable</i> (the panel serves a valid Let's Encrypt cert).</li>
                  <li>
                    Under <b>Which events would you like to trigger this webhook?</b> choose <b>Just the push event</b>.
                    <div className="text-[11px] text-amber-400/80 mt-0.5">
                      Don't pick <i>Send me everything</i> — we silently ignore non-push events, but they waste GitHub retries and clutter delivery history.
                    </div>
                  </li>
                  <li>Ensure <b>Active</b> is checked, click <b>Add webhook</b>.</li>
                </ol>
                <div className="text-[11px] pt-2 border-t border-panel-border/40 mt-2">
                  <b>What happens next:</b> GitHub sends a <i>ping</i> event — you should see the "Last delivery" badge above update within seconds. After that, every push to the configured branch triggers a redeploy of the services whose <i>Subpath</i> matches the changed files.
                </div>
              </Disclosure>
            </div>
          </Card>
        )}

        {/* PAT card */}
        <Card>
          <div className="p-4 space-y-2">
            <div className="flex items-center justify-between text-sm">
              <span className="inline-flex items-center gap-2 font-medium text-panel-text"><KeyRound size={15} className="text-blue-400" /> GitHub PAT</span>
              <span className="text-panel-muted">
                {project.github_pat_masked ? <code>{project.github_pat_masked}</code> : <i>not set</i>}
              </span>
            </div>
            <div className="flex gap-2">
              <PasswordInput
                autoComplete="new-password"
                inputClassName={inputCls}
                value={newPAT}
                onChange={setNewPAT}
                placeholder="Paste new PAT to rotate"
                hideGenerator
                wrapperClassName="flex-1"
              />
              <button
                onClick={handleRotatePAT}
                disabled={!newPAT.trim() || rotating}
                className="px-3 py-2 text-xs bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50"
              >
                {rotating ? "Rotating…" : "Rotate"}
              </button>
            </div>
          </div>
        </Card>

        {/* Services */}
        <Card>
          <div className="p-4 flex items-center justify-between">
            <span className="inline-flex items-center gap-2 text-sm font-medium text-panel-text"><Layers size={15} className="text-blue-400" /> Services ({services.length})</span>
            <div className="flex items-center gap-2">
              <button onClick={handleDeployAll} className="px-3 py-1.5 text-xs bg-blue-600 hover:bg-blue-700 text-white rounded-lg">Deploy all</button>
              <button onClick={() => setAddingService(true)} className="px-3 py-1.5 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text">+ Add service</button>
            </div>
          </div>
          <div className="divide-y divide-panel-border">
            {services.length === 0 ? (
              <div className="text-center text-sm text-panel-muted py-8">No services yet. Click <b>+ Add service</b>.</div>
            ) : services.map((svc) => (
              <ServiceDetail
                key={svc.id}
                svc={svc}
                projectId={project.id}
                onDeploy={() => handleDeployService(svc)}
                onRemove={() => handleRemoveService(svc)}
                onLogs={() => setLogsFor(svc)}
                onEdit={() => setEditingService(svc)}
                onAction={(a) => handleServiceAction(svc, a)}
                onAddAlias={(d) => handleAddAlias(svc, d)}
                onRemoveAlias={(d) => handleRemoveAlias(svc, d)}
                serverIP={serverIP}
              />
            ))}
          </div>
        </Card>
      </div>

      {addingService && (
        <AddServiceModal
          projectId={project.id}
          // Prefer the project-level repo URL (set when the project was
          // created and visible in the Edit Project modal). The
          // services-fallback is kept only for legacy projects that
          // pre-date the shared-clone refactor and still carry per-
          // service URLs without a project URL set. Without this fix,
          // a project with zero services rendered "(no repo set)" in
          // the Add Service banner — even though the project DID have
          // a repo URL — and the operator assumed they had to enter one.
          projectRepoURL={
            project.git_repo_url
            || services.find((s) => s.git_repo_url)?.git_repo_url
            || ""
          }
          presets={presets}
          serverIP={serverIP}
          availableDomains={availableDomains}
          onClose={() => setAddingService(false)}
          onAdded={() => { setAddingService(false); refresh(); }}
        />
      )}

      {editingProject && (
        <EditProjectModal
          project={project}
          onClose={() => setEditingProject(false)}
          onSaved={() => { setEditingProject(false); onChanged(); }}
        />
      )}

      {editingService && (
        <EditServiceModal
          projectId={project.id}
          svc={editingService}
          presets={presets}
          availableDomains={availableDomains}
          serverIP={serverIP}
          onClose={() => setEditingService(null)}
          onSaved={() => { setEditingService(null); refresh(); }}
        />
      )}

      {logsFor && (
        <LogsModal projectId={project.id} svc={logsFor} onClose={() => setLogsFor(null)} />
      )}
    </Modal>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Webhook last-delivery badge
// ──────────────────────────────────────────────────────────────────────────

function LastWebhookBadge({ at, event }: { at: string | null; event: string }) {
  if (!at) {
    return (
      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] bg-amber-500/10 text-amber-400 border border-amber-500/20">
        <AlertCircle size={11} /> Waiting for first delivery
      </span>
    );
  }
  const ts = new Date(at);
  const ageMin = Math.round((Date.now() - ts.getTime()) / 60000);
  const ageLabel = ageMin < 1 ? "just now" : ageMin < 60 ? `${ageMin}m ago` : ageMin < 1440 ? `${Math.round(ageMin / 60)}h ago` : ts.toLocaleDateString();
  return (
    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] bg-green-500/10 text-green-400 border border-green-500/20" title={ts.toLocaleString()}>
      <CheckCircle size={11} /> Last delivery: {event || "event"} · {ageLabel}
    </span>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Edit modals
// ──────────────────────────────────────────────────────────────────────────

function EditProjectModal({ project, onClose, onSaved }: { project: Project; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(project.name);
  const [description, setDescription] = useState(project.description);
  const [gitRepoURL, setGitRepoURL] = useState(project.git_repo_url || "");
  const [gitBranch, setGitBranch] = useState(project.git_branch || "main");
  const [autoDeploy, setAutoDeploy] = useState(project.auto_deploy);
  const [saving, setSaving] = useState(false);

  async function save() {
    setSaving(true);
    try {
      const payload: Record<string, unknown> = { name, description, auto_deploy: autoDeploy };
      if (gitRepoURL.trim() !== (project.git_repo_url || "").trim()) {
        payload.git_repo_url = gitRepoURL.trim();
      }
      if ((gitBranch.trim() || "main") !== (project.git_branch || "main")) {
        payload.git_branch = gitBranch.trim() || "main";
      }
      await api.put(`/projects/${project.id}`, payload);
      toast.success("Project updated");
      onSaved();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    } finally {
      setSaving(false);
    }
  }
  return (
    <Modal isOpen onClose={onClose} title="Edit project" size="md">
      <div className="space-y-3">
        <div>
          <label className={labelCls}>Name</label>
          <input className={inputCls} value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div>
          <label className={labelCls}>Description</label>
          <textarea className={inputCls} rows={3} value={description} onChange={(e) => setDescription(e.target.value)} />
        </div>
        <div className="grid grid-cols-3 gap-3">
          <div className="col-span-2">
            <label className={labelCls}>Repository URL</label>
            <input
              className={inputCls}
              value={gitRepoURL}
              onChange={(e) => setGitRepoURL(e.target.value.trim())}
              placeholder="https://github.com/owner/repo.git"
              spellCheck={false}
            />
            <p className="text-[11px] text-panel-muted mt-1">
              Changing this rewrites every service's remote and the on-disk git origin. The next Pull will fetch from the new URL.
            </p>
          </div>
          <div>
            <label className={labelCls}>Branch</label>
            <input
              className={inputCls}
              value={gitBranch}
              onChange={(e) => setGitBranch(e.target.value.trim())}
              placeholder="main"
              spellCheck={false}
            />
            <p className="text-[11px] text-panel-muted mt-1">
              Project-wide. The next Pull / deploy fetches origin/&lt;new&gt;.
            </p>
          </div>
        </div>
        <label className="inline-flex items-center gap-2 text-sm text-panel-text cursor-pointer">
          <input type="checkbox" checked={autoDeploy} onChange={(e) => setAutoDeploy(e.target.checked)} />
          Auto-deploy on GitHub push
        </label>
        <div className="flex justify-end gap-2 pt-2">
          <button onClick={onClose} className="px-4 py-2 text-sm text-panel-muted border border-panel-border rounded-lg">Cancel</button>
          <button onClick={save} disabled={saving} className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50">
            {saving ? "Saving…" : "Save"}
          </button>
        </div>
      </div>
    </Modal>
  );
}

function EditServiceModal({ projectId, svc, presets, availableDomains, serverIP, onClose, onSaved }: {
  projectId: string;
  svc: ProjectService;
  presets: Record<string, Preset>;
  availableDomains: DomainOption[];
  serverIP: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [framework, setFramework] = useState(svc.framework);
  const [branch, setBranch] = useState(svc.git_branch);
  const [subpath, setSubpath] = useState(svc.git_subpath);
  const [pathPrefix, setPathPrefix] = useState(svc.path_prefix);
  const [installCmd, setInstallCmd] = useState(svc.install_cmd);
  const [buildCmd, setBuildCmd] = useState(svc.build_cmd);
  const [startCmd, setStartCmd] = useState(svc.start_cmd);
  const [port, setPort] = useState(svc.port || 0);
  const [envVars, setEnvVars] = useState<Record<string, string>>(svc.env_vars || {});
  const [envKey, setEnvKey] = useState("");
  const [envVal, setEnvVal] = useState("");
  // Same domain UI as the Add Service modal: primary picks from the
  // registered-domain dropdown, aliases use the chip+input pattern.
  // Local-only until Save — the backend handles vhost rename + SAN
  // cert reissue in one round trip.
  const [primaryDomain, setPrimaryDomain] = useState(svc.primary_domain || "");
  const [aliases, setAliases] = useState<string[]>(svc.alias_domains || []);
  const [aliasInput, setAliasInput] = useState("");
  const [saving, setSaving] = useState(false);

  function addAliasRow() {
    const a = aliasInput.trim().toLowerCase();
    if (!a) return;
    if (a === primaryDomain.trim().toLowerCase()) {
      toast.error(`"${a}" is already the primary domain`);
      return;
    }
    if (aliases.includes(a)) {
      toast.error(`"${a}" is already in the list`);
      return;
    }
    setAliases([...aliases, a]);
    setAliasInput("");
  }

  async function save() {
    const p = primaryDomain.trim().toLowerCase();
    if (!p) {
      toast.error("Primary domain is required");
      return;
    }
    setSaving(true);
    try {
      await api.put(`/projects/${projectId}/services/${svc.id}`, {
        framework, git_branch: branch, git_subpath: subpath, path_prefix: pathPrefix,
        install_cmd: installCmd, build_cmd: buildCmd, start_cmd: startCmd,
        port, env_vars: envVars,
        primary_domain: p,
        alias_domains: aliases,
      });
      toast.success("Service updated (restarting)");
      onSaved();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    } finally {
      setSaving(false);
    }
  }
  return (
    <Modal isOpen onClose={onClose} title={`Edit service: ${svc.name}`} size="lg">
      <div className="space-y-3 max-h-[70vh] overflow-y-auto pr-1">
        <div className="grid grid-cols-2 gap-3">
          <div>
            <LabelWithHint hint="Change the framework preset — does NOT rewrite install/build/start automatically, edit those below if you want preset defaults.">Framework</LabelWithHint>
            <select className={selectCls} value={framework} onChange={(e) => setFramework(e.target.value)}>
              <option value="">— custom —</option>
              {Object.entries(presets).map(([k, p]) => <option key={k} value={k}>{p.label}</option>)}
            </select>
          </div>
          <div>
            {/* Branch is project-level (3.1.27). Read-only here. */}
            <LabelWithHint hint="Set on the project, not per service. Use Edit Project to change.">Branch (project-wide)</LabelWithHint>
            <div className={inputCls + " bg-panel-bg/30 text-panel-muted cursor-not-allowed"}>
              {branch || "main"}
            </div>
          </div>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div>
            <LabelWithHint hint="Monorepo subdirectory. Only pushes touching this path trigger a redeploy.">Subpath</LabelWithHint>
            <input className={inputCls} value={subpath} onChange={(e) => setSubpath(e.target.value)} />
          </div>
          {svc.role === "backend" && (
            <div>
              <LabelWithHint hint="nginx location prefix.">Path prefix</LabelWithHint>
              <input className={inputCls} value={pathPrefix} onChange={(e) => setPathPrefix(e.target.value)} />
            </div>
          )}
        </div>
        {svc.role === "backend" && (
          <div>
            <LabelWithHint hint="TCP port the backend listens on.">Port</LabelWithHint>
            <input type="number" className={inputCls} value={port || ""} onChange={(e) => setPort(parseInt(e.target.value) || 0)} />
          </div>
        )}
        <div>
          <LabelWithHint hint="Runs first on every deploy to install dependencies.">Install command</LabelWithHint>
          <input className={inputCls} value={installCmd} onChange={(e) => setInstallCmd(e.target.value)} />
        </div>
        <div>
          <LabelWithHint hint="Optional build step. Leave blank for interpreted stacks.">Build command</LabelWithHint>
          <input className={inputCls} value={buildCmd} onChange={(e) => setBuildCmd(e.target.value)} />
        </div>
        {svc.role === "backend" && (
          <div>
            <LabelWithHint hint="systemd ExecStart. ${PORT} is substituted with the allocated port.">Start command</LabelWithHint>
            <input className={inputCls} value={startCmd} onChange={(e) => setStartCmd(e.target.value)} />
          </div>
        )}
        {/* Domains — visually identical to the Add Service modal so
            add and edit feel like the same flow. The PUT body merges
            primary + alias changes into one reconcile on the backend. */}
        <div>
          <LabelWithHint required hint="Pick a domain registered under Domains. Renaming this triggers a vhost rename + SAN cert reissue under the new --cert-name; the old vhost file is removed automatically.">Primary domain</LabelWithHint>
          <PrimaryDomainSelect
            value={primaryDomain}
            domains={availableDomains}
            onChange={setPrimaryDomain}
          />
        </div>
        <div>
          <LabelWithHint hint="Extra domains that should hit this same service. All domains share one nginx vhost and one Let's Encrypt cert (SAN list). Each alias needs its own A record pointing at this server's IP — or CNAME-ing to the primary works too.">Alias domains</LabelWithHint>
          <div className="flex gap-2">
            <input
              className={inputCls}
              value={aliasInput}
              onChange={(e) => setAliasInput(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); addAliasRow(); } }}
              placeholder="www.example.com  or  another-domain.com"
            />
            <button onClick={addAliasRow} className="px-3 py-2 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text">Add</button>
          </div>
          {aliases.length > 0 && (
            <div className="flex flex-wrap gap-1 mt-2">
              {aliases.map((d) => (
                <span key={d} className="inline-flex items-center gap-1 px-2 py-0.5 text-[11px] bg-panel-bg border border-panel-border rounded text-panel-muted">
                  {d}
                  <button onClick={() => setAliases(aliases.filter((x) => x !== d))} className="text-panel-muted/60 hover:text-red-400"><X size={10} /></button>
                </span>
              ))}
            </div>
          )}
        </div>
        {primaryDomain && (
          <DnsHint role={svc.role} primary={primaryDomain} aliases={aliases} serverIP={serverIP} />
        )}
        <div>
          <LabelWithHint hint="Environment variables injected into the process and written to .env in the install dir.">Environment variables</LabelWithHint>
          <div className="space-y-1">
            {Object.entries(envVars).map(([k, v]) => (
              <div key={k} className="flex items-center gap-2 text-xs">
                <code className="px-2 py-1 bg-panel-bg border border-panel-border rounded text-panel-muted flex-1">{k}={v}</code>
                <button onClick={() => { const n = { ...envVars }; delete n[k]; setEnvVars(n); }} className="p-1 text-panel-muted hover:text-red-400"><X size={12} /></button>
              </div>
            ))}
            <div className="flex gap-2">
              <input className={inputCls} value={envKey} onChange={(e) => setEnvKey(e.target.value)} placeholder="KEY" />
              <input className={inputCls} value={envVal} onChange={(e) => setEnvVal(e.target.value)} placeholder="value" />
              <button
                onClick={() => { if (envKey) { setEnvVars({ ...envVars, [envKey]: envVal }); setEnvKey(""); setEnvVal(""); } }}
                className="px-3 py-2 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text"
              >
                Add
              </button>
            </div>
          </div>
        </div>
        <div className="flex justify-end gap-2 pt-2">
          <button onClick={onClose} className="px-4 py-2 text-sm text-panel-muted border border-panel-border rounded-lg">Cancel</button>
          <button onClick={save} disabled={saving} className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50">
            {saving ? "Saving…" : "Save"}
          </button>
        </div>
      </div>
    </Modal>
  );
}

function ServiceDetail({
  svc, serverIP, projectId, onDeploy, onRemove, onLogs, onEdit, onAction, onAddAlias, onRemoveAlias,
}: {
  svc: ProjectService;
  serverIP: string;
  projectId: string;
  onDeploy: () => void;
  onRemove: () => void;
  onLogs: () => void;
  onEdit: () => void;
  onAction: (a: "start" | "stop" | "restart" | "run" | "install" | "build") => void;
  onAddAlias: (d: string) => void;
  onRemoveAlias: (d: string) => void;
}) {
  const [aliasInput, setAliasInput] = useState("");
  const isBackend = svc.role === "backend";
  const isRunning = svc.status === "running";

  type DepStep = { name: string; status: string; details?: string; error?: string; started_at?: string; completed_at?: string; };
  type Deployment = { id: string; status: string; progress: number; trigger: string; commit_sha?: string; started_at: string; finished_at?: string; error_msg?: string; steps?: DepStep[]; };
  const [dep, setDep] = useState<Deployment | null>(null);
  const [showDep, setShowDep] = useState(false);
  const [showErrorModal, setShowErrorModal] = useState(false);
  const [errorModalDep, setErrorModalDep] = useState<Deployment | null>(null);
  const [errorModalLoading, setErrorModalLoading] = useState(false);
  async function openErrorModal() {
    setShowErrorModal(true);
    if (errorModalDep) return;
    setErrorModalLoading(true);
    try {
      const r = await api.get(`/projects/${projectId}/services/${svc.id}/deployments/latest`);
      setErrorModalDep(r.data?.data ?? null);
    } catch {
      /* 404 is fine */
    } finally {
      setErrorModalLoading(false);
    }
  }
  const transitioning = svc.status === "deploying" || svc.status === "pending" || svc.status === "queue-full";
  const errored = svc.status === "error" || svc.status === "failed";
  useEffect(() => {
    if (transitioning || errored) setShowDep(true);
  }, [transitioning, errored]);
  useEffect(() => {
    if (!showDep) return;
    let cancelled = false;
    const fetchOnce = async () => {
      try {
        const r = await api.get(`/projects/${projectId}/services/${svc.id}/deployments/latest`);
        if (!cancelled) setDep(r.data?.data ?? null);
      } catch { /* noop */ }
    };
    fetchOnce();
    const terminal = !!(dep && (
      dep.finished_at ||
      dep.status === "success" ||
      dep.status === "error" ||
      dep.status === "failed"
    ));
    if (terminal) {
      const t = setTimeout(fetchOnce, 1000);
      return () => { cancelled = true; clearTimeout(t); };
    }
    const interval = setInterval(fetchOnce, 1500);
    return () => { cancelled = true; clearInterval(interval); };
  }, [showDep, projectId, svc.id, dep?.status, dep?.finished_at]);

  return (
    <div className="px-4 py-3">
      <div className="flex items-center justify-between">
        <div>
          <div className="flex items-center gap-2 text-sm">
            <span className="font-medium text-panel-text">{svc.name}</span>
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-panel-bg border border-panel-border">{svc.role}</span>
            {svc.framework && <span className="text-[10px] text-blue-400">{svc.framework}</span>}
            {/* Service-id copy chip — paired with the project-level
                "API / Developer IDs" panel above. Click to copy the
                ObjectID for /api/v1/external/deploy/projects/
                {project_id}/services/{svc_id}/* routes. */}
            <ServiceIdCopy id={svc.id} name={svc.name} />
            {errored ? (
              <button
                type="button"
                onClick={openErrorModal}
                className="inline-flex items-center gap-1 cursor-pointer hover:opacity-80 transition-opacity"
                title="Click to view error details"
              >
                <StatusBadge status="error" />
                <span className="text-[9px] text-red-300/70 underline decoration-dotted">view</span>
              </button>
            ) : (
              <StatusBadge status={svc.status === "running" ? "active" : svc.status === "deploying" ? "warning" : svc.status === "stopped" ? "inactive" : svc.status === "needs_env_vars" ? "warning" : "pending"} />
            )}
          </div>
          <div className="text-[11px] text-panel-muted mt-1 flex items-center gap-3">
            <span><GitBranch size={10} className="inline" /> {svc.git_branch}{svc.git_subpath && <> · {svc.git_subpath}</>}</span>
            {svc.last_commit_sha && <span>@ {svc.last_commit_sha.substring(0, 7)}</span>}
            {svc.port > 0 && <span>:{svc.port}</span>}
          </div>
        </div>
        <div className="flex items-center gap-1">
          {svc.primary_domain && (
            <a
              href={`https://${svc.primary_domain}${svc.path_prefix || ""}`}
              target="_blank" rel="noopener noreferrer"
              className="p-1.5 text-panel-muted hover:text-blue-400"
              title="Open URL"
            >
              <ExternalLink size={14} />
            </a>
          )}
          <button onClick={onLogs} className="p-1.5 text-panel-muted hover:text-blue-400" title="Logs"><Server size={14} /></button>
          <button onClick={onDeploy} className="p-1.5 text-panel-muted hover:text-blue-400" title="Redeploy (install + build + restart on existing source)"><Rocket size={14} /></button>
          {svc.install_cmd && (
            <button
              onClick={() => onAction("install")}
              className="p-1.5 text-panel-muted hover:text-blue-400"
              title={`Install packages\n$ ${svc.install_cmd}`}
            >
              <Package size={14} />
            </button>
          )}
          {svc.build_cmd && (
            <button
              onClick={() => onAction("build")}
              className="p-1.5 text-panel-muted hover:text-blue-400"
              title={`Build only (no restart)\n$ ${svc.build_cmd}`}
            >
              <Hammer size={14} />
            </button>
          )}
          {isBackend && (
            <>
              <button onClick={() => onAction("restart")} className="p-1.5 text-panel-muted hover:text-blue-400" title="Restart (systemctl restart — no rebuild)"><RotateCw size={14} /></button>
              {isRunning ? (
                <button onClick={() => onAction("stop")} className="p-1.5 text-panel-muted hover:text-amber-400" title="Stop"><Square size={14} /></button>
              ) : (
                <button
                  onClick={() => onAction("run")}
                  className="p-1.5 text-panel-muted hover:text-green-400"
                  title={svc.start_cmd ? `Run\n$ ${svc.start_cmd}` : "Run (systemctl start)"}
                >
                  <Play size={14} />
                </button>
              )}
            </>
          )}
          <button onClick={onEdit} className="p-1.5 text-panel-muted hover:text-blue-400" title="Edit commands / env / port"><Pencil size={14} /></button>
          <button onClick={onRemove} className="p-1.5 text-panel-muted hover:text-red-400" title="Remove"><Trash2 size={14} /></button>
        </div>
      </div>

      {svc.status === "needs_env_vars" && (svc.missing_env_keys || []).length > 0 && (
        <div className="mt-2 rounded-lg border border-amber-500/40 bg-amber-500/5 p-3 space-y-2">
          <div className="flex items-start gap-2">
            <AlertTriangle size={14} className="text-amber-400 mt-0.5 flex-shrink-0" />
            <div className="flex-1 min-w-0">
              <div className="text-xs font-semibold text-amber-300">
                Service paused — {svc.missing_env_keys!.length} required env var{svc.missing_env_keys!.length === 1 ? "" : "s"} not set
              </div>
              <div className="text-[11px] text-panel-muted mt-0.5">
                Your repo's <code className="text-panel-text">.env.example</code> declares these keys but the wizard left them blank. Fill them in and the service will start.
              </div>
              <div className="mt-2 max-h-24 overflow-y-auto pr-1 grid grid-cols-2 gap-x-4 gap-y-0.5">
                {(svc.missing_env_keys || []).map((k) => (
                  <code key={k} className="text-[10px] text-amber-200/90 truncate" title={k}>{k}</code>
                ))}
              </div>
              <button
                onClick={onEdit}
                className="mt-2 inline-flex items-center gap-1.5 px-2.5 py-1 text-[11px] bg-amber-500/20 hover:bg-amber-500/30 text-amber-200 border border-amber-500/40 rounded-md transition-colors"
              >
                <KeyRound size={11} /> Add env vars + start
              </button>
            </div>
          </div>
        </div>
      )}

      {errored && dep && (dep.status === "error" || dep.status === "failed") && (
        (() => {
          const failedStep = (dep.steps || []).find((s) => s.status === "failed");
          const summary = failedStep?.error || dep.error_msg || failedStep?.details || "Deployment failed — see timeline below";
          return (
            <div className="mt-2 rounded-lg border border-red-500/40 bg-red-500/5 p-3 space-y-2">
              <div className="flex items-start gap-2">
                <AlertTriangle size={14} className="text-red-400 mt-0.5 flex-shrink-0" />
                <div className="flex-1 min-w-0">
                  <div className="text-xs font-semibold text-red-300">
                    Deploy failed{failedStep ? ` at "${failedStep.name}"` : ""}
                  </div>
                  <pre className="text-[11px] text-red-200/90 mt-1 font-mono whitespace-pre-wrap break-all max-h-32 overflow-auto">
                    {summary}
                  </pre>
                  <div className="mt-2 flex items-center gap-2">
                    <button
                      onClick={onLogs}
                      className="inline-flex items-center gap-1.5 px-2.5 py-1 text-[11px] bg-red-500/15 hover:bg-red-500/25 text-red-200 border border-red-500/40 rounded-md transition-colors"
                    >
                      <Server size={11} /> View full logs
                    </button>
                    <button
                      onClick={onDeploy}
                      className="inline-flex items-center gap-1.5 px-2.5 py-1 text-[11px] bg-red-500/15 hover:bg-red-500/25 text-red-200 border border-red-500/40 rounded-md transition-colors"
                    >
                      <Rocket size={11} /> Retry deploy
                    </button>
                  </div>
                </div>
              </div>
            </div>
          );
        })()
      )}

      <div className="mt-2 flex items-center justify-between text-[11px]">
        <button
          type="button"
          onClick={() => setShowDep((v) => !v)}
          className="text-panel-muted hover:text-panel-text inline-flex items-center gap-1"
        >
          <Rocket size={11} /> {showDep ? "Hide deploy progress" : "Show deploy progress"}
        </button>
      </div>
      {showDep && dep && (
        <div className="mt-2 rounded-lg border border-panel-border bg-panel-bg/40 p-3 space-y-2">
          <div className="flex items-center justify-between text-[11px]">
            <div className="flex items-center gap-2">
              <span className="text-panel-text font-medium">
                {dep.status === "running"
                  ? (dep.finished_at ? "Last deploy succeeded" : "Deploying…")
                  : dep.status === "success"
                    ? "Deploy succeeded"
                    : dep.status === "error" || dep.status === "failed"
                      ? "Deploy failed"
                      : `Deploy ${dep.status}`}
              </span>
              <span className="text-panel-muted">· {dep.trigger}</span>
              {dep.commit_sha && (
                <code className="text-[10px] bg-panel-bg px-1.5 py-0.5 rounded text-panel-muted">@ {dep.commit_sha.substring(0, 7)}</code>
              )}
            </div>
            <span className={`tabular-nums font-mono ${dep.status === "error" || dep.status === "failed" ? "text-red-400" : "text-blue-300"}`}>
              {dep.progress ?? 0}%
            </span>
          </div>
          <div className="h-1.5 w-full bg-panel-bg rounded-full overflow-hidden">
            <div
              className={`h-full rounded-full transition-all ${
                dep.status === "error" || dep.status === "failed"
                  ? "bg-red-500"
                  : dep.status === "success" || (dep.status === "running" && dep.finished_at)
                    ? "bg-green-500"
                    : "bg-blue-500"
              } ${dep.status === "running" && !dep.finished_at ? "animate-pulse" : ""}`}
              style={{ width: `${Math.min(dep.progress ?? 0, 100)}%` }}
            />
          </div>
          <div className="space-y-1">
            {(dep.steps || []).map((step, idx) => {
              const dur = step.started_at && step.completed_at
                ? Math.max(0, Math.round((new Date(step.completed_at).getTime() - new Date(step.started_at).getTime()) / 100) / 10)
                : null;
              const icon = step.status === "completed"
                ? <Check size={11} className="text-green-400" />
                : step.status === "failed"
                  ? <X size={11} className="text-red-400" />
                  : step.status === "in_progress"
                    ? <RotateCw size={11} className="text-blue-400 animate-spin" />
                    : step.status === "skipped"
                      ? <span className="w-2.5 h-2.5 inline-block rounded-full border border-panel-border" />
                      : <span className="w-2.5 h-2.5 inline-block rounded-full bg-panel-border/40" />;
              return (
                <div key={idx} className="flex items-start gap-2 text-[11px]">
                  <span className="mt-0.5 shrink-0 w-3.5 flex items-center justify-center">{icon}</span>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className={
                        step.status === "completed" ? "text-panel-text"
                          : step.status === "failed" ? "text-red-400"
                            : step.status === "in_progress" ? "text-blue-300"
                              : step.status === "skipped" ? "text-panel-muted/60 line-through"
                                : "text-panel-muted/70"
                      }>
                        {step.name}
                      </span>
                      {dur !== null && step.status !== "in_progress" && (
                        <span className="text-panel-muted/60 text-[10px] tabular-nums">{dur}s</span>
                      )}
                    </div>
                    {step.details && (
                      <div className="text-panel-muted/80 truncate font-mono text-[10px]" title={step.details}>
                        {step.details}
                      </div>
                    )}
                    {step.error && (
                      <pre className="mt-1 bg-red-500/10 border border-red-500/30 rounded p-1.5 text-[10px] text-red-300 whitespace-pre-wrap break-all max-h-20 overflow-auto">
                        {step.error}
                      </pre>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
          {dep.error_msg && (
            <pre className="bg-red-500/10 border border-red-500/30 rounded p-2 text-[10px] text-red-300 whitespace-pre-wrap break-all max-h-24 overflow-auto">
              {dep.error_msg}
            </pre>
          )}
        </div>
      )}

      <div className="mt-2 space-y-2">
        <div className="flex items-center flex-wrap gap-1 text-[11px]">
          <Shield size={11} className="text-green-400" />
          <code className="px-1.5 py-0.5 bg-panel-bg border border-panel-border rounded text-panel-text">{svc.primary_domain}</code>
          {svc.alias_domains.map((a) => (
            <span key={a} className="inline-flex items-center gap-1 px-1.5 py-0.5 bg-panel-bg border border-panel-border rounded text-panel-muted">
              {a}
              <button onClick={() => onRemoveAlias(a)} className="text-panel-muted/60 hover:text-red-400"><X size={9} /></button>
            </span>
          ))}
          {(svc.role === "frontend" || svc.role === "static") && (
            <input
              className="ml-1 px-2 py-0.5 text-[11px] bg-panel-bg border border-panel-border rounded text-panel-text placeholder-panel-muted/50 w-40"
              value={aliasInput}
              onChange={(e) => setAliasInput(e.target.value.toLowerCase())}
              onKeyDown={(e) => {
                if (e.key === "Enter" && aliasInput) {
                  const d = aliasInput.trim().toLowerCase();
                  if (!isLikelyDomain(d)) { toast.error(`"${d}" doesn't look like a domain`); return; }
                  if (d === svc.primary_domain || svc.alias_domains.includes(d)) { toast.error(`"${d}" is already in the list`); return; }
                  onAddAlias(d);
                  setAliasInput("");
                }
              }}
              placeholder="add alias…"
            />
          )}
        </div>
        {(svc.role === "frontend" || svc.role === "static") && svc.primary_domain && (
          <DnsHint role={svc.role} primary={svc.primary_domain} aliases={svc.alias_domains} serverIP={serverIP} />
        )}
      </div>
      {showErrorModal && (
        <Modal isOpen={showErrorModal} title={`${svc.name} — Deploy error`} onClose={() => setShowErrorModal(false)}>
          {errorModalLoading && (
            <div className="text-sm text-panel-muted py-6 text-center">Loading latest deployment…</div>
          )}
          {!errorModalLoading && !errorModalDep && (
            <div className="text-sm text-panel-muted py-6 text-center">No deployment record found for this service yet.</div>
          )}
          {!errorModalLoading && errorModalDep && (() => {
            const d = errorModalDep;
            const failed = (d.steps || []).find((s) => s.status === "failed");
            const summary = failed?.error || d.error_msg || failed?.details || "Deploy failed — no error message captured";
            const startedAgo = relativeTime(d.started_at);
            return (
              <div className="space-y-4">
                <div className="grid grid-cols-2 gap-3 text-xs">
                  <div>
                    <div className="text-panel-muted/70 uppercase text-[10px] tracking-wide">When</div>
                    <div className="text-panel-text mt-0.5">{startedAgo}</div>
                    <div className="text-panel-muted/60 text-[10px]">{new Date(d.started_at).toLocaleString()}</div>
                  </div>
                  <div>
                    <div className="text-panel-muted/70 uppercase text-[10px] tracking-wide">Trigger</div>
                    <div className="text-panel-text mt-0.5 capitalize">{d.trigger}</div>
                    {d.commit_sha && <code className="text-[10px] text-panel-muted">@ {d.commit_sha.substring(0, 7)}</code>}
                  </div>
                </div>
                {failed && (
                  <div>
                    <div className="text-panel-muted/70 uppercase text-[10px] tracking-wide mb-1">Failed step</div>
                    <div className="text-sm text-red-400 font-medium">{failed.name}</div>
                  </div>
                )}
                <div>
                  <div className="text-panel-muted/70 uppercase text-[10px] tracking-wide mb-1">Error</div>
                  <pre className="bg-red-500/10 border border-red-500/30 rounded p-3 text-[11px] text-red-300 whitespace-pre-wrap break-all max-h-64 overflow-auto font-mono">
                    {summary}
                  </pre>
                </div>
                {(d.steps || []).length > 0 && (
                  <div>
                    <div className="text-panel-muted/70 uppercase text-[10px] tracking-wide mb-1">Steps</div>
                    <div className="space-y-1">
                      {(d.steps || []).map((s, i) => (
                        <div key={i} className="flex items-center gap-2 text-[11px]">
                          {s.status === "completed" ? <Check size={11} className="text-green-400" />
                            : s.status === "failed" ? <X size={11} className="text-red-400" />
                              : s.status === "skipped" ? <span className="w-2.5 h-2.5 inline-block rounded-full border border-panel-border" />
                                : <span className="w-2.5 h-2.5 inline-block rounded-full bg-panel-border/40" />}
                          <span className={s.status === "failed" ? "text-red-300" : s.status === "completed" ? "text-panel-text" : "text-panel-muted"}>
                            {s.name}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                )}
                <div className="flex items-center justify-end gap-2 pt-2 border-t border-panel-border">
                  <Button variant="ghost" onClick={() => { setShowErrorModal(false); onLogs(); }}>
                    <Server size={14} className="mr-1.5" /> View full logs
                  </Button>
                  <Button variant="primary" onClick={() => { setShowErrorModal(false); onDeploy(); }}>
                    <Rocket size={14} className="mr-1.5" /> Retry deploy
                  </Button>
                </div>
              </div>
            );
          })()}
        </Modal>
      )}
    </div>
  );
}

// Add-service modal — reuses the wizard's ServiceCard
function AddServiceModal({
  projectId, projectRepoURL, presets, serverIP, availableDomains, onClose, onAdded,
}: {
  projectId: string;
  projectRepoURL: string;
  presets: Record<string, Preset>;
  serverIP: string;
  availableDomains: DomainOption[];
  onClose: () => void;
  onAdded: () => void;
}) {
  const [svc, setSvc] = useState<NewServiceForm>(() => ({ ...emptyService(), git_repo_url: projectRepoURL }));
  const [saving, setSaving] = useState(false);

  function applyPreset(key: string) {
    const p = presets[key];
    if (!p) return setSvc((s) => ({ ...s, framework: key }));
    setSvc((s) => ({
      ...s,
      framework: key,
      install_cmd: p.install_cmd || "",
      build_cmd: p.build_cmd || "",
      start_cmd: p.start_cmd || "",
      port: p.default_port || 0,
      role: p.is_static ? "frontend" : s.role,
    }));
  }

  async function save() {
    const nameErr = validateServiceName(svc.name);
    if (nameErr) return toast.error(`Service name: ${nameErr}`);
    const payload = { ...svc, git_repo_url: projectRepoURL };
    if (!payload.git_repo_url) return toast.error("Project has no Repository URL set");
    if (!payload.primary_domain) return toast.error("Primary domain is required");
    setSaving(true);
    try {
      await api.post(`/projects/${projectId}/services`, payload);
      toast.success("Service added and deploying");
      onAdded();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal isOpen onClose={onClose} title="Add Service" size="lg">
      <div className="space-y-4 max-h-[70vh] overflow-y-auto pr-1">
        <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 px-3 py-2 text-[11px] text-blue-200/80 flex items-center gap-2">
          <GitBranch size={12} className="shrink-0" />
          <span>Cloning from project's repo: <code className="text-panel-text">{projectRepoURL || "(no repo set)"}</code>. Pick a different branch / subpath below if needed.</span>
        </div>
        <ServiceCard
          idx={0}
          svc={svc}
          presets={presets}
          serverIP={serverIP}
          availableDomains={availableDomains}
          hideRepoURL
          onChange={(patch) => setSvc((s) => ({ ...s, ...patch }))}
          onPreset={applyPreset}
        />
        <div className="flex justify-end gap-2 pt-2">
          <button onClick={onClose} className="px-4 py-2 text-sm text-panel-muted border border-panel-border rounded-lg">Cancel</button>
          <button onClick={save} disabled={saving} className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50">
            {saving ? "Deploying…" : "Add & deploy"}
          </button>
        </div>
      </div>
    </Modal>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Logs modal — tail of last N deployments
// ──────────────────────────────────────────────────────────────────────────

function LogsModal({ projectId, svc, onClose }: { projectId: string; svc: ProjectService; onClose: () => void }) {
  const [logs, setLogs] = useState<DeployLog[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    (async () => {
      setLoading(true);
      try {
        const r = await api.get(`/projects/${projectId}/services/${svc.id}/logs`);
        setLogs(r.data?.data || []);
      } catch {
        setLogs([]);
      } finally {
        setLoading(false);
      }
    })();
  }, [projectId, svc.id]);

  return (
    <Modal isOpen onClose={onClose} title={`Logs — ${svc.name}`} size="xl">
      <div className="space-y-3 max-h-[70vh] overflow-y-auto">
        {loading ? (
          <div className="h-20 bg-panel-border/20 rounded animate-pulse" />
        ) : logs.length === 0 ? (
          <div className="text-center py-8 text-sm text-panel-muted">No deploys yet.</div>
        ) : logs.map((l) => (
          <div key={l.deployment_id} className="rounded-lg border border-panel-border">
            <div className="px-3 py-2 flex items-center justify-between text-xs bg-panel-bg/40">
              <span className="inline-flex items-center gap-2">
                <StatusBadge status={l.status === "running" ? "active" : l.status === "error" ? "error" : l.status === "deploying" ? "warning" : "pending"} />
                <span className="text-panel-muted">{l.trigger}</span>
                {l.commit && <code className="text-panel-muted">@ {l.commit.substring(0, 7)}</code>}
              </span>
              <span className="text-panel-muted">{new Date(l.started_at).toLocaleString()}</span>
            </div>
            {l.error && <div className="px-3 py-1 text-xs text-red-400">{l.error}</div>}
            {l.output && (
              <pre className="px-3 py-2 text-[11px] text-panel-muted whitespace-pre-wrap max-h-80 overflow-y-auto bg-panel-bg/20">{l.output}</pre>
            )}
          </div>
        ))}
      </div>
    </Modal>
  );
}
