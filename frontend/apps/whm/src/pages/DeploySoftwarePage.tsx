import { useEffect, useMemo, useState } from "react";
import { Card, Button, Modal, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Rocket, Plus, RefreshCw, Trash2, Play, Copy, HelpCircle, X,
  ChevronDown, ChevronRight, GitBranch, Globe, Shield, ExternalLink,
  KeyRound, Webhook, Server, PackageOpen, Layers, AlertCircle, CheckCircle,
  Eye, EyeOff, Pause, Power, RotateCw, Square, Pencil, Check,
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

interface DomainOption {
  id: string;
  domain: string;
  user: string;
}

// BuildErrorInfo is the structured payload the backend returns when a deploy
// fails at the user's install/build step. Renders in its own modal so the
// full (ANSI-stripped) output is readable instead of squeezed into a toast.
interface BuildErrorInfo {
  service: string;
  stage: string;  // "install" | "build"
  summary: string;
  output: string;
}

// ──────────────────────────────────────────────────────────────────────────
// Small reusable UI primitives
// ──────────────────────────────────────────────────────────────────────────

const inputCls =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const inputInvalidCls = inputCls.replace("border-panel-border", "border-red-500/60").replace("focus:ring-blue-500/40", "focus:ring-red-500/40").replace("focus:border-blue-500", "focus:border-red-500");
const labelCls = "block text-sm font-medium text-panel-text mb-1";
const selectCls = inputCls + " appearance-none";

// sanitizeServiceName applies the exact transformation the backend would
// accept: lowercase only, a-z starts, then any mix of a-z, 0-9, and '-'.
// Spaces and other characters collapse to a single dash so typing
// "vendor b" ends up as "vendor-b" in the field without the user having
// to think about the rules. Applied on every keystroke so the visible
// value is always submit-safe.
function sanitizeServiceName(raw: string): string {
  let s = raw.toLowerCase();
  s = s.replace(/[^a-z0-9-]+/g, "-"); // any run of invalid chars → '-'
  s = s.replace(/-+/g, "-");          // collapse repeated dashes
  s = s.replace(/^[^a-z]+/, "");      // must start with a letter
  if (s.length > 32) s = s.slice(0, 32);
  return s;
}

// validateServiceName mirrors the Go-side serviceNamePattern exactly so
// the frontend can show the same error the backend would without having
// to round-trip. Returns null for valid names.
function validateServiceName(name: string): string | null {
  if (name === "") return "Required";
  if (name.length < 2) return "At least 2 characters";
  if (name.length > 32) return "Max 32 characters";
  if (!/^[a-z][a-z0-9-]*$/.test(name)) return "Must start with a letter; only a-z 0-9 and '-'";
  return null;
}

// slugifyProjectName mirrors the backend's slugify() so the wizard can
// show the operator what their project's URL slug will be (and therefore
// what its webhook URL and install directory will look like) BEFORE they
// click Create.
function slugifyProjectName(raw: string): string {
  let s = raw.toLowerCase().trim();
  s = s.replace(/[^a-z0-9]+/g, "-");
  s = s.replace(/^-+|-+$/g, "");
  if (s.length > 40) s = s.slice(0, 40);
  return s;
}

// isLikelyDomain is a loose sanity check for alias inputs — just confirms
// there's at least one dot and no whitespace. Full DNS validation is
// deferred to Let's Encrypt, which will reject bad domains with a clear
// error anyway.
function isLikelyDomain(d: string): boolean {
  const t = d.trim().toLowerCase();
  return /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/.test(t);
}

// FieldHint renders an inline (?) icon that reveals a short explanation on
// hover/focus. Used on every form input so new users don't have to guess
// what the field means. Content is short (< 25 words) so the tooltip stays
// readable on smaller screens.
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

// Accordion section used for "How to set up" / "How to add the webhook"
// explanations. Defaults open on create, closed on detail — caller decides.
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

function CopyButton({ value, label = "Copy" }: { value: string; label?: string }) {
  const [ok, setOk] = useState(false);
  return (
    <button
      type="button"
      onClick={async () => {
        await navigator.clipboard.writeText(value);
        setOk(true);
        setTimeout(() => setOk(false), 1500);
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
  const [serverIP, setServerIP] = useState<string>("");
  const [presets, setPresets] = useState<Record<string, Preset>>({});
  const [availableDomains, setAvailableDomains] = useState<DomainOption[]>([]);

  useEffect(() => {
    fetchProjects();
    fetchServerIP();
    fetchPresets();
    fetchDomains();
  }, []);

  async function fetchProjects() {
    setLoading(true);
    try {
      const res = await api.get("/projects");
      setProjects(res.data?.data || []);
    } catch {
      /* ignore */
    } finally {
      setLoading(false);
    }
  }

  async function fetchServerIP() {
    try {
      const res = await api.get("/monitor/system");
      setServerIP(res.data?.data?.ip || "");
    } catch {
      /* ignore */
    }
  }

  async function fetchDomains() {
    try {
      const res = await api.get("/domains?limit=500");
      setAvailableDomains(res.data?.data || []);
    } catch {
      /* keep empty — the Primary domain dropdown will show a helpful empty state */
    }
  }

  async function fetchPresets() {
    try {
      const res = await api.get("/apps/presets");
      // Backend returns a map keyed by framework id (see services.GetPresets),
      // not an array. Some entries in that map don't set the `framework` JSON
      // field (legacy records), so we copy the key in as a fallback.
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
    if (!confirm(`Delete project "${p.name}" and all its services? This removes code, systemd units, and nginx configs.`)) return;
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
          <div className="divide-y divide-panel-border">
            {projects.map((p) => (
              <ProjectRow key={p.id} project={p} onOpen={() => setDetailProject(p)} onDelete={() => handleDelete(p)} />
            ))}
          </div>
        )}
      </Card>

      {showCreate && (
        <CreateProjectWizard
          serverIP={serverIP}
          presets={presets}
          availableDomains={availableDomains}
          onClose={() => setShowCreate(false)}
          onCreated={() => {
            setShowCreate(false);
            fetchProjects();
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
// Setup guide — visible by default so operators learn the workflow
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
          Each service picks its own repo URL, branch, subpath (monorepo support), and framework preset.
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
  onCreated: () => void;
}) {
  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [pat, setPat] = useState("");
  const [autoDeploy, setAutoDeploy] = useState(true);
  const [services, setServices] = useState<NewServiceForm[]>([emptyService()]);
  const [saving, setSaving] = useState(false);
  const [buildError, setBuildError] = useState<BuildErrorInfo | null>(null);

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
    // Duplicate service names inside a single project would fail the
    // unique-index insert on the backend anyway; catch here so the error
    // is obvious instead of showing up as a cryptic Mongo message.
    const seen = new Set<string>();
    for (const s of services) {
      const nameErr = validateServiceName(s.name);
      if (nameErr) return toast.error(`Service name "${s.name || "(empty)"}": ${nameErr}`);
      if (seen.has(s.name)) return toast.error(`Duplicate service name "${s.name}" — each service in a project must have a unique name`);
      seen.add(s.name);
      if (!s.git_repo_url) return toast.error(`Service "${s.name}": repository URL required`);
      if (!s.primary_domain) return toast.error(`Service "${s.name}": primary domain required`);
    }
    setSaving(true);
    try {
      // Single atomic call — backend rolls back on any service failure so
      // we never leave a stranded project row (the bug that caused the
      // "duplicate slug" error on every retry).
      await api.post("/projects/provision", {
        name, description, github_pat: pat, auto_deploy: autoDeploy, services,
      });
      toast.success("Project created and first deploy running");
      onCreated();
    } catch (e: any) {
      const body = e?.response?.data?.error;
      // Backend returns code=BUILD_FAILED with structured details on a
      // user-code build failure (missing import, type error, etc.). Show
      // the full output in a dedicated modal instead of cramming it into
      // a toast as unreadable ANSI-escaped text.
      if (body?.code === "BUILD_FAILED" && body?.details?.output) {
        setBuildError({
          service: body.details.service || "",
          stage: body.details.stage || "build",
          summary: body.details.summary || body.message || "",
          output: body.details.output,
        });
        toast.error(`Build failed: ${body.details.summary || body.message}`);
      } else {
        const raw = body?.message || "Failed to create project";
        // Translate the Mongo unique-index message into something an operator
        // can act on. Shouldn't fire now that slug auto-uniquifies, but
        // defence in depth so one leaked Mongo string doesn't confuse users.
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
            <div>
              <LabelWithHint hint="GitHub Personal Access Token used to clone private repos. Stored AES-GCM encrypted; only a masked preview is ever returned. Generate one at github.com/settings/tokens with 'repo' scope.">GitHub PAT</LabelWithHint>
              <input
                type="password"
                autoComplete="new-password"
                className={inputCls}
                value={pat}
                onChange={(e) => setPat(e.target.value)}
                placeholder="ghp_… (leave blank for public repos)"
              />
              <a
                href="https://github.com/settings/tokens/new?scopes=repo&description=ServerPanel%20deploy%20token"
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
                onClick={() => setStep(2)}
                disabled={!name.trim()}
                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50"
              >
                Next: Services
              </button>
            </div>
          </div>
        )}

        {step === 2 && (
          <div className="space-y-4 max-h-[70vh] overflow-y-auto pr-1">
            {services.map((svc, i) => (
              <ServiceCard
                key={i}
                idx={i}
                svc={svc}
                presets={presets}
                serverIP={serverIP}
                availableDomains={availableDomains}
                onChange={(patch) => updateService(i, patch)}
                onPreset={(key) => applyPreset(i, key)}
                onRemove={services.length > 1 ? () => setServices((ss) => ss.filter((_, j) => j !== i)) : undefined}
              />
            ))}
            <button
              onClick={() => setServices((ss) => [...ss, emptyService()])}
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

        {step === 3 && (
          <div className="space-y-4">
            <div className="rounded-lg border border-panel-border p-4 space-y-3">
              <div className="flex items-center gap-2">
                <PackageOpen size={14} className="text-blue-400" />
                <span className="font-medium text-panel-text">{name || "(unnamed)"}</span>
                {autoDeploy && <span className="text-[10px] px-1.5 py-0.5 rounded bg-green-500/10 text-green-400 border border-green-500/20">auto-deploy</span>}
              </div>
              {description && <p className="text-xs text-panel-muted">{description}</p>}
              <div className="text-xs text-panel-muted">
                {pat ? <span className="inline-flex items-center gap-1"><KeyRound size={11} /> PAT will be encrypted</span> : "No PAT (public repos only)"}
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
              <span>After create: the first deploy runs in the background. Watch each service's logs from the detail drawer; SSL issuance can take up to a minute on new domains.</span>
            </div>
            <div className="flex justify-between pt-2">
              <button onClick={() => setStep(2)} className="px-4 py-2 text-sm text-panel-muted border border-panel-border rounded-lg">Back</button>
              <button
                onClick={handleCreate}
                disabled={saving}
                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50"
              >
                {saving ? "Creating…" : "Create & deploy"}
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
// Build error modal — pretty-prints the ANSI-stripped tsc / next / webpack
// output with a clear summary at the top and a copy button.
// ──────────────────────────────────────────────────────────────────────────

function BuildErrorModal({ info, onClose }: { info: BuildErrorInfo; onClose: () => void }) {
  return (
    <Modal isOpen onClose={onClose} title={`${info.stage === "install" ? "Install" : "Build"} failed`} size="xl">
      <div className="space-y-3">
        <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-sm">
          <div className="flex items-start gap-2">
            <AlertCircle size={15} className="text-red-400 mt-0.5 shrink-0" />
            <div className="flex-1 space-y-1">
              <div className="text-red-400 font-medium">
                Service "{info.service}" — {info.stage} step failed
              </div>
              {info.summary && (
                <div className="text-panel-text font-mono text-xs break-all">{info.summary}</div>
              )}
              <div className="text-panel-muted text-[11px]">
                The project was rolled back. Fix the error in your repo, push, then retry <b>Create &amp; deploy</b>.
              </div>
            </div>
          </div>
        </div>
        <div>
          <div className="flex items-center justify-between mb-1">
            <span className="text-xs text-panel-muted">Full build output</span>
            <CopyButton value={info.output} />
          </div>
          <pre className="max-h-[50vh] overflow-auto p-3 bg-panel-bg border border-panel-border rounded-lg text-[11px] text-panel-text whitespace-pre-wrap font-mono">
            {info.output}
          </pre>
        </div>
        <div className="flex justify-end">
          <button onClick={onClose} className="px-4 py-2 text-sm bg-panel-surface border border-panel-border rounded-lg text-panel-text">
            Close
          </button>
        </div>
      </div>
    </Modal>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Per-service form card (inside the wizard + inside the detail drawer "add")
// ──────────────────────────────────────────────────────────────────────────

function ServiceCard({
  idx, svc, presets, serverIP, availableDomains, onChange, onPreset, onRemove,
}: {
  idx: number;
  svc: NewServiceForm;
  presets: Record<string, Preset>;
  serverIP: string;
  availableDomains: DomainOption[];
  onChange: (patch: Partial<NewServiceForm>) => void;
  onPreset: (key: string) => void;
  onRemove?: () => void;
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

      <div className="grid grid-cols-2 gap-3">
        <div>
          <LabelWithHint required hint="HTTPS URL to the Git repo. For private repos the project's stored PAT is injected into the URL for git operations.">Repository URL</LabelWithHint>
          <input className={inputCls} value={svc.git_repo_url} onChange={(e) => onChange({ git_repo_url: e.target.value })} placeholder="https://github.com/org/repo.git" />
        </div>
        <div>
          <LabelWithHint required hint="Branch to clone and redeploy from. Only pushes to this branch trigger auto-deploy.">Branch</LabelWithHint>
          <input className={inputCls} value={svc.git_branch} onChange={(e) => onChange({ git_branch: e.target.value })} placeholder="main" />
        </div>
      </div>

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
          <LabelWithHint required hint="Pick a domain already registered in the WHM Domains page. The DNS A record must point at this server's IP. Add new domains under WHM → Domains if yours isn't listed.">Primary domain</LabelWithHint>
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
// Primary domain picker — dropdown of WHM-registered domains
// ──────────────────────────────────────────────────────────────────────────

// PrimaryDomainSelect constrains the Primary domain field to the set of
// domains already registered under WHM → Domains. That gives us two things:
// (1) the A record is known to already point here (operators register a
// domain only once it resolves), and (2) we avoid typos at the point where
// Let's Encrypt would otherwise return an opaque "verification failed".
// If the stored value isn't in the list (e.g. domain was deleted), we
// render it anyway so editing existing services doesn't silently lose data.
function PrimaryDomainSelect({ value, domains, onChange }: { value: string; domains: DomainOption[]; onChange: (v: string) => void }) {
  if (domains.length === 0) {
    return (
      <div className="space-y-1">
        <select className={selectCls} disabled>
          <option>No domains registered yet</option>
        </select>
        <div className="text-[11px] text-amber-400/80">
          Add a domain under <b>WHM → Domains</b> first, then come back here to deploy.
        </div>
      </div>
    );
  }
  const hasValue = !value || domains.some((d) => d.domain === value);
  return (
    <select className={selectCls} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">— select a domain —</option>
      {domains.map((d) => (
        <option key={d.id} value={d.domain}>{d.domain}</option>
      ))}
      {!hasValue && <option value={value}>{value} (not registered)</option>}
    </select>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// DNS hint block — shows the exact A + CNAME records needed
// ──────────────────────────────────────────────────────────────────────────

function DnsHint({ role, primary, aliases, serverIP }: { role: string; primary: string; aliases: string[]; serverIP: string }) {
  if (role === "backend") {
    // Backend-only services still need their primary domain's A record to
    // resolve here; aliases are a frontend-only concept in this UI.
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
// Project detail drawer — services table, webhook card, PAT rotate, logs
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
  const [logsFor, setLogsFor] = useState<ProjectService | null>(null);
  const [addingService, setAddingService] = useState(false);
  const [rotating, setRotating] = useState(false);
  const [newPAT, setNewPAT] = useState("");
  const [secretRevealed, setSecretRevealed] = useState(false);
  const [editingProject, setEditingProject] = useState(false);
  const [editingService, setEditingService] = useState<ProjectService | null>(null);

  useEffect(() => {
    refresh();
    api.get(`/projects/${project.id}/webhook`).then((r) => setWebhook(r.data?.data || null)).catch(() => {});
  }, [project.id]);

  // Poll services while any of them is transitioning (deploying / pending /
  // queue-full). Background worker finishes asynchronously — without this
  // the drawer shows "deploying" forever until the user clicks Refresh.
  // Polling stops automatically once every service is in a terminal
  // state (running / stopped / error / static).
  useEffect(() => {
    const pending = services.some((s) => s.status === "deploying" || s.status === "pending" || s.status === "queue-full");
    if (!pending) return;
    const id = setInterval(refresh, 3000);
    return () => clearInterval(id);
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
    try {
      await api.post(`/projects/${project.id}/deploy`);
      toast.success("Deploy all queued");
      setTimeout(refresh, 1000);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    }
  }

  async function handleRemoveService(svc: ProjectService) {
    if (!confirm(`Remove service "${svc.name}"? This stops the process, removes nginx config, and deletes the code.`)) return;
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
      toast.success("PAT rotated");
      setNewPAT("");
      onChanged();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    } finally {
      setRotating(false);
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

  async function handleServiceAction(svc: ProjectService, action: "start" | "stop" | "restart") {
    try {
      await api.post(`/projects/${project.id}/services/${svc.id}/action/${action}`);
      toast.success(`${svc.name}: ${action}`);
      refresh();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    }
  }

  async function handleProjectAction(action: "start" | "stop" | "restart") {
    if (action === "stop" && !confirm(`Stop every backend service in "${project.name}"?`)) return;
    try {
      await api.post(`/projects/${project.id}/action/${action}`);
      toast.success(`Project ${action} complete`);
      refresh();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    }
  }

  async function handleTogglePause() {
    const nextPaused = !project.paused;
    try {
      await api.post(`/projects/${project.id}/${nextPaused ? "pause" : "resume"}`);
      toast.success(nextPaused ? "Auto-deploy paused" : "Auto-deploy resumed");
      onChanged();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    }
  }

  return (
    <Modal isOpen onClose={onClose} title={project.name} size="xl">
      <div className="space-y-5">
        {/* Project toolbar — project-wide actions */}
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <button onClick={handleDeployAll} className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg" title="Pull + rebuild + restart every service">
            <Rocket size={13} /> Deploy all
          </button>
          <button onClick={() => handleProjectAction("restart")} className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border border-panel-border hover:border-blue-500/40 rounded-lg text-panel-text" title="systemctl restart every backend (no rebuild)">
            <RotateCw size={13} /> Restart all
          </button>
          <button onClick={() => handleProjectAction("stop")} className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border border-panel-border hover:border-amber-500/40 rounded-lg text-panel-text" title="systemctl stop every backend">
            <Square size={13} /> Stop all
          </button>
          <button onClick={() => handleProjectAction("start")} className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border border-panel-border hover:border-green-500/40 rounded-lg text-panel-text" title="systemctl start every stopped backend">
            <Power size={13} /> Start all
          </button>
          <button onClick={handleTogglePause} className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border ${project.paused ? "border-amber-500/40 text-amber-400 bg-amber-500/5" : "border-panel-border text-panel-text bg-panel-surface"}`} title={project.paused ? "Webhooks still recorded, but deploys are paused" : "Pause auto-deploy (webhooks still recorded)"}>
            {project.paused ? <><Play size={13} /> Resume auto-deploy</> : <><Pause size={13} /> Pause auto-deploy</>}
          </button>
          <button onClick={() => setEditingProject(true)} className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border border-panel-border hover:border-blue-500/40 rounded-lg text-panel-text" title="Edit name, description, auto-deploy toggle">
            <Pencil size={13} /> Edit
          </button>
        </div>

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
              <input
                type="password"
                autoComplete="new-password"
                className={inputCls}
                value={newPAT}
                onChange={(e) => setNewPAT(e.target.value)}
                placeholder="Paste new PAT to rotate"
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
// Webhook "last delivery" badge — live confirmation the wiring works
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
// Edit modals — thin wrappers over PUT /projects/:id and PUT /services/:svc
// ──────────────────────────────────────────────────────────────────────────

function EditProjectModal({ project, onClose, onSaved }: { project: Project; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(project.name);
  const [description, setDescription] = useState(project.description);
  const [autoDeploy, setAutoDeploy] = useState(project.auto_deploy);
  const [saving, setSaving] = useState(false);

  async function save() {
    setSaving(true);
    try {
      await api.put(`/projects/${project.id}`, {
        name, description, auto_deploy: autoDeploy,
      });
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

function EditServiceModal({ projectId, svc, presets, onClose, onSaved }: {
  projectId: string;
  svc: ProjectService;
  presets: Record<string, Preset>;
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
  const [saving, setSaving] = useState(false);

  async function save() {
    setSaving(true);
    try {
      await api.put(`/projects/${projectId}/services/${svc.id}`, {
        framework, git_branch: branch, git_subpath: subpath, path_prefix: pathPrefix,
        install_cmd: installCmd, build_cmd: buildCmd, start_cmd: startCmd,
        port, env_vars: envVars,
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
            <LabelWithHint hint="Branch to pull from. Must match the branch configured in the GitHub webhook for pushes to trigger a deploy.">Branch</LabelWithHint>
            <input className={inputCls} value={branch} onChange={(e) => setBranch(e.target.value)} />
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
  svc, serverIP, onDeploy, onRemove, onLogs, onEdit, onAction, onAddAlias, onRemoveAlias,
}: {
  svc: ProjectService;
  serverIP: string;
  onDeploy: () => void;
  onRemove: () => void;
  onLogs: () => void;
  onEdit: () => void;
  onAction: (a: "start" | "stop" | "restart") => void;
  onAddAlias: (d: string) => void;
  onRemoveAlias: (d: string) => void;
}) {
  const [aliasInput, setAliasInput] = useState("");
  const isBackend = svc.role === "backend";
  const isRunning = svc.status === "running";
  return (
    <div className="px-4 py-3">
      <div className="flex items-center justify-between">
        <div>
          <div className="flex items-center gap-2 text-sm">
            <span className="font-medium text-panel-text">{svc.name}</span>
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-panel-bg border border-panel-border">{svc.role}</span>
            {svc.framework && <span className="text-[10px] text-blue-400">{svc.framework}</span>}
            <StatusBadge status={svc.status === "running" ? "active" : svc.status === "deploying" ? "warning" : svc.status === "error" ? "error" : svc.status === "stopped" ? "inactive" : "pending"} />
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
          <button onClick={onDeploy} className="p-1.5 text-panel-muted hover:text-blue-400" title="Redeploy (pull + build + restart)"><Rocket size={14} /></button>
          {isBackend && (
            <>
              <button onClick={() => onAction("restart")} className="p-1.5 text-panel-muted hover:text-blue-400" title="Restart (systemctl restart — no rebuild)"><RotateCw size={14} /></button>
              {isRunning ? (
                <button onClick={() => onAction("stop")} className="p-1.5 text-panel-muted hover:text-amber-400" title="Stop"><Square size={14} /></button>
              ) : (
                <button onClick={() => onAction("start")} className="p-1.5 text-panel-muted hover:text-green-400" title="Start"><Power size={14} /></button>
              )}
            </>
          )}
          <button onClick={onEdit} className="p-1.5 text-panel-muted hover:text-blue-400" title="Edit commands / env / port"><Pencil size={14} /></button>
          <button onClick={onRemove} className="p-1.5 text-panel-muted hover:text-red-400" title="Remove"><Trash2 size={14} /></button>
        </div>
      </div>
      {/* Domains */}
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
            <>
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
            </>
          )}
        </div>
        {(svc.role === "frontend" || svc.role === "static") && svc.primary_domain && (
          <DnsHint role={svc.role} primary={svc.primary_domain} aliases={svc.alias_domains} serverIP={serverIP} />
        )}
      </div>
    </div>
  );
}

// Add-service inside the detail drawer — same card the wizard uses.
function AddServiceModal({
  projectId, presets, serverIP, availableDomains, onClose, onAdded,
}: {
  projectId: string;
  presets: Record<string, Preset>;
  serverIP: string;
  availableDomains: DomainOption[];
  onClose: () => void;
  onAdded: () => void;
}) {
  const [svc, setSvc] = useState<NewServiceForm>(emptyService());
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
    if (!svc.git_repo_url) return toast.error("Repository URL is required");
    if (!svc.primary_domain) return toast.error("Primary domain is required");
    setSaving(true);
    try {
      await api.post(`/projects/${projectId}/services`, svc);
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
        <ServiceCard
          idx={0}
          svc={svc}
          presets={presets}
          serverIP={serverIP}
          availableDomains={availableDomains}
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
