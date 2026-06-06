import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { Card, Button, Modal, StatusBadge, PasswordInput, SearchableSelect, confirmAction, copyToClipboard, usePagination, PaginationBar } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Rocket, Plus, RefreshCw, Trash2, Play, Copy, HelpCircle, X,
  ChevronDown, ChevronRight, GitBranch, Globe, Shield, ExternalLink,
  KeyRound, Webhook, Server, PackageOpen, Layers, AlertCircle, AlertTriangle, CheckCircle,
  Eye, EyeOff, Pause, Power, RotateCw, Square, Pencil, Check, Package, Hammer, Code2,
  Download, Upload, FileJson, Search, FolderOpen,
} from "lucide-react";
import { BuildErrorModal, tryExtractBuildError, type BuildErrorInfo } from "@/components/BuildErrorModal";
import { BulkUploadServicesModal } from "@/components/BulkUploadServicesModal";
import { useAuthStore } from "@/store/auth";

// ──────────────────────────────────────────────────────────────────────────
// Types — mirror internal/models/project.go on the backend
// ──────────────────────────────────────────────────────────────────────────

interface Project {
  id: string;
  name: string;
  slug: string;
  description: string;
  github_pat_masked: string;
  // Project-level shared repo: every service clones from this URL into
  // /home/<user>/projects/<slug>/<subpath>. Empty for legacy projects
  // that pre-date the shared-clone refactor (those use per-service URLs).
  git_repo_url: string;
  // Project-wide branch every service tracks (3.1.27 hoist).
  git_branch: string;
  project_dir: string;
  user: string;
  auto_deploy: boolean;
  paused: boolean;
  last_webhook_at: string | null;
  last_webhook_event: string;
  // 3.1.73 — last *failed* webhook delivery. Surfaced in the LastWebhookBadge
  // so signature-mismatch deliveries no longer hide behind "Waiting for first
  // delivery". Cleared on the next successful verification.
  last_webhook_error?: string;
  last_webhook_error_at?: string | null;
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
  runtime_version: string;
  port: number;
  env_vars: Record<string, string>;
  user: string;
  install_dir: string;
  status: string;
  last_commit_sha: string;
  last_deployed_at: string | null;
  // Populated by the backend after Provision when the cloned repo's
  // .env.example declared keys the operator left blank. status is set
  // to "needs_env_vars" until they fill the keys via the Edit modal.
  missing_env_keys?: string[];
}

// RuntimeVersionInfo mirrors /software/runtimes — one entry per version
// installed on the host. The picker only shows versions with installed:true.
// is_default reflects the host operator's "Set as default" pin from the
// Software page — surfaced in the picker so the operator can see what
// version "System default" actually resolves to at deploy time.
type RuntimeVersionInfo = { version: string; installed?: boolean; active?: boolean; is_default?: boolean };

// presetToRuntimeKey returns the /software/runtimes key for a given preset's
// app_type (or role fallback). Static/PHP/Java/Docker return "" — those
// roles have no interpreter to pin, so the picker hides itself.
//
// Keys MUST match the field names the backend's ListAllRuntimes returns
// (see software_service.go): "nodejs" / "python" / "ruby" / "go" / "php".
// The previous mapping returned "node" for Node services but the API
// emits "nodejs", so runtimes["node"] was always undefined and the
// picker fell back to a single "System default" entry — even when 3
// Node versions were installed.
function presetToRuntimeKey(appType: string | undefined, role: string): string {
  const t = (appType || "").toLowerCase();
  if (t === "node" || t === "nodejs") return "nodejs";
  if (t === "python") return "python";
  if (t === "ruby") return "ruby";
  if (t === "go" || t === "golang") return "go";
  // Fallback for a "custom" service (no framework preset) on a backend
  // role: we don't know the language, so default to Node (the most
  // common backend on this panel) — the operator can leave the dropdown
  // on "System default" if that guess is wrong.
  if (t === "" && role === "backend") return "nodejs";
  return "";
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

// ProjectActivity matches the backend's ProjectActivity payload returned
// by GET /projects/:id/activity. Drives the new "Activity" card in the
// detail drawer (last commit, deploy stats, webhook activity, recent
// deploys, per-service runtime).
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
  user: string;
}

interface VendorOption {
  id: string;
  username: string;
  name: string;
  role: string;
  email: string;
}

// BuildErrorInfo + BuildErrorModal are imported from @/components/BuildErrorModal
// so the /apps Deploy path and the Deploy Software Provision path share one
// implementation. Keep types referencing BuildErrorInfo below untouched.

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
      className="inline-flex items-center gap-1 text-[9px] font-mono px-1.5 py-0.5 rounded border border-panel-border bg-panel-bg text-panel-muted hover:text-blue-400 hover:border-blue-500/40 transition-colors"
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
  const [showImport, setShowImport] = useState(false);
  const [detailProject, setDetailProject] = useState<Project | null>(null);
  const [serverIP, setServerIP] = useState<string>("");
  const [presets, setPresets] = useState<Record<string, Preset>>({});
  const [availableDomains, setAvailableDomains] = useState<DomainOption[]>([]);
  const [availableVendors, setAvailableVendors] = useState<VendorOption[]>([]);
  // Installed runtime versions, keyed by runtime ("node"/"python"/"ruby"/"go").
  // Fed to the per-service Runtime-version dropdown so operators can only pick
  // versions actually installed on the host.
  const [runtimes, setRuntimes] = useState<Record<string, RuntimeVersionInfo[]>>({});

  useEffect(() => {
    fetchProjects();
    fetchServerIP();
    fetchPresets();
    fetchDomains();
    fetchVendors();
    fetchRuntimes();
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

  // 3.1.79 — header search box. Filters by name / slug / git_repo_url
  // / description, case-insensitive, every term must match somewhere
  // ("backend api" finds rows that have BOTH "backend" and "api" in
  // any of the searched fields). Pagination operates on the filtered
  // set so paging buttons reflect what the operator actually sees;
  // resetting the page to 1 on a new query stops "page 4 of 1" UI
  // states when the filter shrinks the result count under the
  // current page.
  const [projectSearch, setProjectSearch] = useState("");
  const filteredProjects = useMemo(() => {
    const q = projectSearch.trim().toLowerCase();
    if (!q) return projects;
    const terms = q.split(/\s+/).filter(Boolean);
    return projects.filter((p) => {
      const hay = [p.name, p.slug, p.git_repo_url, p.description, p.user]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return terms.every((t) => hay.includes(t));
    });
  }, [projects, projectSearch]);
  const pgProj = usePagination("whm-projects");
  useEffect(() => { pgProj.setTotal(filteredProjects.length); }, [filteredProjects.length]);
  useEffect(() => { if (projectSearch) pgProj.setPage(1); }, [projectSearch]);
  const pagedProjects = filteredProjects.slice((pgProj.page - 1) * pgProj.limit, pgProj.page * pgProj.limit);

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

  // Vendors = users that can OWN a project (its files live under their
  // /home/<username>/). Includes vendor_owner / vendor_admin / vendor_staff
  // / customer / developer — basically everyone except plain "support". The
  // wizard's Basics step shows this list as a dropdown to admins so they
  // can place the project under any user; non-admins (vendors logging in)
  // skip the dropdown and the wizard auto-pins their own username.
  async function fetchVendors() {
    // /users hides vendor_admin (strict-tenant mode) so it returned an
    // empty set here. Vendors live under /admin/vendors; that's the
    // list the wizard actually wants — the Linux user owning the
    // project files is always a tenant root. Owner-only endpoint; a
    // tenant-scoped caller lands in the isAdmin=false branch anyway.
    try {
      const res = await api.get("/admin/vendors?limit=500");
      const rows = (res.data?.data || []) as Array<{ id: string; username: string; name: string; email?: string; status?: string }>;
      setAvailableVendors(
        rows
          .filter((r) => r.username && (r.status ?? "active") === "active")
          .map((r) => ({ id: r.id, username: r.username, name: r.name, role: "vendor", email: r.email ?? "" }))
      );
    } catch {
      /* leave empty — wizard falls back to current user's username */
    }
  }

  async function fetchRuntimes() {
    try {
      const res = await api.get("/software/runtimes");
      const data = res.data?.data;
      if (data && typeof data === "object") {
        setRuntimes(data as Record<string, RuntimeVersionInfo[]>);
      }
    } catch { /* keep empty — dropdown falls back to "System default" */ }
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
            onClick={() => setShowImport(true)}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
            title="Create a new project from a previously-exported JSON manifest"
          >
            <Upload size={14} /> Import JSON
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

      {/* 3.1.79 — search box. Hidden on the empty-state path (no
          projects yet) so a fresh install isn't visually noisy with
          search affordances that have nothing to filter. */}
      {!loading && projects.length > 0 && (
        <div className="relative">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted pointer-events-none" />
          <input
            type="text"
            value={projectSearch}
            onChange={(e) => setProjectSearch(e.target.value)}
            placeholder="Search by name, slug, repo URL, or description…"
            className="w-full pl-9 pr-24 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/60 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 text-sm transition-colors"
            spellCheck={false}
          />
          {/* Match counter + clear, both inside the right edge so they
              don't move when the input expands on focus. */}
          {projectSearch && (
            <div className="absolute right-2 top-1/2 -translate-y-1/2 flex items-center gap-1.5">
              <span className="text-[11px] text-panel-muted">{filteredProjects.length} of {projects.length}</span>
              <button
                onClick={() => setProjectSearch("")}
                className="p-1 text-panel-muted hover:text-panel-text"
                title="Clear search"
                type="button"
              >
                <X size={12} />
              </button>
            </div>
          )}
        </div>
      )}

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
        ) : filteredProjects.length === 0 ? (
          <div className="text-center py-12 px-4">
            <Search size={36} className="text-panel-muted/30 mx-auto mb-3" />
            <h3 className="text-base font-medium text-panel-text mb-1">No projects match "{projectSearch}"</h3>
            <p className="text-panel-muted text-sm mb-4">Try a shorter query or a different field.</p>
            <button
              onClick={() => setProjectSearch("")}
              className="text-sm text-blue-400 hover:text-blue-300"
              type="button"
            >
              Clear search
            </button>
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
          runtimes={runtimes}
          availableDomains={availableDomains}
          availableVendors={availableVendors}
          onClose={() => setShowCreate(false)}
          onCreated={(created) => {
            setShowCreate(false);
            fetchProjects();
            // Drop the operator straight into the new project's detail
            // drawer so the per-service deploy timeline is visible without
            // hunting in the list. The drawer's own polling picks up
            // status transitions in real time.
            if (created) setDetailProject(created);
          }}
        />
      )}

      {showImport && (
        <ImportProjectModal
          onClose={() => setShowImport(false)}
          onImported={(created) => {
            setShowImport(false);
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
          runtimes={runtimes}
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
  runtime_version: string;
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
  runtime_version: "",
  port: 0,
  env_vars: {},
});

function CreateProjectWizard({
  serverIP, presets, runtimes, availableDomains, availableVendors, onClose, onCreated,
}: {
  serverIP: string;
  presets: Record<string, Preset>;
  runtimes: Record<string, RuntimeVersionInfo[]>;
  availableDomains: DomainOption[];
  availableVendors: VendorOption[];
  onClose: () => void;
  onCreated: (created: Project | null) => void;
}) {
  // Current logged-in user — drives whether the Vendor dropdown is shown
  // (admins see + can pick), and what value it defaults to (vendors get
  // their own username pinned automatically).
  const currentUser = useAuthStore((s) => s.user);
  const isAdmin = currentUser?.role === "vendor_owner" || currentUser?.role === "admin" || (currentUser?.permissions?.includes("server.manage") ?? false);
  const ownUsername = currentUser?.username || "";

  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [pat, setPat] = useState("");
  // Project-level repo URL — every service in this project clones from it.
  // Each service can still have its own branch + subpath (monorepo-friendly),
  // but the repo itself is shared. Per-service git_repo_url is hidden in
  // the UI and stamped from this value when the project is created.
  const [repoURL, setRepoURL] = useState("");
  // Project-level branch — every service in the project tracks the
  // same branch (3.1.27 hoist). Per-service git_branch field on the
  // wire still exists for back-compat but the wizard collects ONE
  // value here and stamps it onto every service request.
  const [projectBranch, setProjectBranch] = useState("main");
  // vendor = the system user the project's files will live under
  // (/home/<vendor>/projects/<slug>/). Admins pick from the dropdown;
  // non-admin users get their own username and can't change it.
  const [vendor, setVendor] = useState<string>(isAdmin ? "" : ownUsername);
  const [autoDeploy, setAutoDeploy] = useState(true);
  const [services, setServices] = useState<NewServiceForm[]>([emptyService()]);
  const [saving, setSaving] = useState(false);
  const [buildError, setBuildError] = useState<BuildErrorInfo | null>(null);
  // Time-based progress for the wizard's Create & deploy click. Provision
  // is a synchronous 30-90s round-trip with no per-step backend signal yet
  // (true async provisioning is a bigger refactor) — this gives the
  // operator real "something's happening" feedback derived from elapsed
  // time + the known step list, instead of a frozen "Creating…" button.
  const [provisionStartedAt, setProvisionStartedAt] = useState<number | null>(null);
  const [tickNow, setTickNow] = useState(Date.now());
  useEffect(() => {
    if (!saving) return;
    const id = setInterval(() => setTickNow(Date.now()), 500);
    return () => clearInterval(id);
  }, [saving]);
  // Heuristic step list — order + cumulative-second thresholds match what
  // the backend's runDeploy actually does. Per-service so a project with
  // N services shows N timelines.
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
    if (!vendor) return toast.error("Vendor (project owner) is required");
    // Duplicate service names inside a single project would fail the
    // unique-index insert on the backend anyway; catch here so the error
    // is obvious instead of showing up as a cryptic Mongo message.
    const seen = new Set<string>();
    for (const s of services) {
      const nameErr = validateServiceName(s.name);
      if (nameErr) return toast.error(`Service name "${s.name || "(empty)"}": ${nameErr}`);
      if (seen.has(s.name)) return toast.error(`Duplicate service name "${s.name}" — each service in a project must have a unique name`);
      seen.add(s.name);
      if (!s.primary_domain) return toast.error(`Service "${s.name}": primary domain required`);
    }
    // Stamp the project-level repoURL + branch onto every service (in
    // case the user jumped between steps without re-triggering the
    // Step 1 → 2 transition). The backend also propagates branch
    // server-side, so this is belt-and-suspenders.
    const branchClean = projectBranch.trim() || "main";
    const servicesWithRepo = services.map((s) => ({
      ...s,
      git_repo_url: repoURL.trim(),
      git_branch: branchClean,
    }));
    setSaving(true);
    setProvisionStartedAt(Date.now());
    try {
      // Single atomic call — backend rolls back on any service failure so
      // we never leave a stranded project row (the bug that caused the
      // "duplicate slug" error on every retry).
      const res = await api.post("/projects/provision", {
        name, description, github_pat: pat, auto_deploy: autoDeploy,
        git_repo_url: repoURL.trim(),
        git_branch: branchClean,
        user: vendor || undefined,
        services: servicesWithRepo,
      });
      toast.success("Project created and first deploy running");
      // Pass the created project up so the parent can auto-open the detail
      // drawer — operator drops straight into the live per-service deploy
      // timeline instead of having to find the new project in the list.
      const created = res?.data?.data?.project ?? null;
      onCreated(created);
    } catch (e: any) {
      // BUILD_FAILED: show the ANSI-stripped output in a dedicated modal
      // instead of cramming it into a toast. Same shape the /apps Deploy
      // endpoint returns now, handled by the shared tryExtractBuildError.
      const be = tryExtractBuildError(e);
      if (be) {
        setBuildError(be);
        toast.error(`${be.stage} failed: ${be.summary}`);
      } else {
        const raw = e?.response?.data?.error?.message || "Failed to create project";
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
            {/* Vendor (project owner) — admin-only. Non-admins see a
                 read-only label with their own username; the project always
                 lands under their /home/<username>/ tree. */}
            {isAdmin ? (
              <div>
                <LabelWithHint required hint="The system user this project belongs to. Project files live under /home/<vendor>/projects/<slug>/ and the Primary domain dropdown on the next step is filtered to domains owned by this user.">Vendor (project owner)</LabelWithHint>
                <select
                  className={inputCls}
                  value={vendor}
                  onChange={(e) => {
                    setVendor(e.target.value);
                    // When the vendor changes, clear primary_domain on every
                    // service — the previous selection might not belong to
                    // the new vendor and would silently fail validation.
                    setServices((ss) => ss.map((s) => ({ ...s, primary_domain: "" })));
                  }}
                >
                  <option value="">— select a vendor —</option>
                  {availableVendors.map((v) => (
                    <option key={v.id} value={v.username}>
                      {v.username} {v.name ? `— ${v.name}` : ""} ({v.role})
                    </option>
                  ))}
                </select>
                {availableVendors.length === 0 && (
                  <p className="text-[11px] text-amber-400 mt-1">No vendors found — create a user account first via Users &amp; RBAC.</p>
                )}
              </div>
            ) : (
              <div>
                <LabelWithHint hint="Project files will live under your /home/ tree. Only an admin can place a project under a different user.">Vendor (project owner)</LabelWithHint>
                <div className={inputCls + " bg-panel-bg/30 text-panel-muted cursor-not-allowed flex items-center"}>
                  {ownUsername || "(no username on your account)"}
                </div>
              </div>
            )}
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
                  // Stamp the project-level repoURL onto every service so the
                  // backend (which is per-service) sees the same URL for all.
                  setServices((ss) => ss.map((s) => ({ ...s, git_repo_url: repoURL.trim() })));
                  setStep(2);
                }}
                disabled={!name.trim() || !repoURL.trim() || !vendor}
                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50"
              >
                Next: Services
              </button>
            </div>
          </div>
        )}

        {step === 2 && (() => {
          // Filter the domain dropdown to only domains owned by the picked
          // vendor — the project's files live under /home/<vendor>/, so
          // pointing it at someone else's domain would be misleading and
          // SSL issuance would fail when it can't write to /home/<other>/.
          const vendorDomains = vendor ? availableDomains.filter((d) => d.user === vendor) : availableDomains;
          return (
          <div className="space-y-4 max-h-[70vh] overflow-y-auto pr-1">
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 px-3 py-2 text-[11px] text-blue-200/80 flex items-center gap-2">
              <GitBranch size={12} className="shrink-0" />
              <span>
                All services in this project clone from <code className="text-panel-text">{repoURL || "(repo URL)"}</code> and live under <code className="text-panel-text">/home/{vendor || "(vendor)"}/projects/</code>. Each service can pick its own branch + subpath.
              </span>
            </div>
            {vendor && vendorDomains.length === 0 && (
              <div className="rounded-lg border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-[11px] text-amber-300">
                <strong className="text-amber-200">No domains owned by {vendor}.</strong> Add a domain under that user's account first (Domains → Add Domain → select user "{vendor}"), then come back here.
              </div>
            )}
            {services.map((svc, i) => (
              <ServiceCard
                key={i}
                idx={i}
                svc={svc}
                presets={presets}
                runtimes={runtimes}
                serverIP={serverIP}
                availableDomains={vendorDomains}
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
          );
        })()}

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
                <span className="inline-flex items-center gap-1"><Server size={11} /> /home/<code className="text-panel-text">{vendor || "?"}</code>/projects/{slugifyProjectName(name) || "?"}/</span>
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
                      {s.primary_domain || "(no domain)"}{(s.alias_domains || []).length > 0 && <> + {(s.alias_domains || []).length} alias{(s.alias_domains || []).length === 1 ? "" : "es"}</>}
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
            {/* Live provisioning progress: per-service step list, derived
                from elapsed seconds + the known step durations. The wizard
                stays open until the backend Provision call returns, then
                auto-closes via onCreated() to drop the operator into the
                detail drawer. */}
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
              {/* Per-service breakdown — every service goes through the same
                  step list. Backend processes them sequentially so we render
                  the same in-flight step indicator across every service for
                  honesty (we don't know which one's currently running without
                  per-step backend signal). */}
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

// BuildErrorModal has moved to @/components/BuildErrorModal so AppsPage can
// use the same implementation.

// ──────────────────────────────────────────────────────────────────────────
// Per-service form card (inside the wizard + inside the detail drawer "add")
// ──────────────────────────────────────────────────────────────────────────

// RuntimeVersionPicker renders a dropdown of installed interpreter versions
// for the given runtime, plus a "System default" option that ships
// runtime_version="" to the backend (which then falls back to PATH).
// When runtimeKey is "" (static / PHP / Java / Docker / unknown), the
// picker renders nothing — those stacks have nothing to pin.
function RuntimeVersionPicker({
  runtimeKey, value, runtimes, onChange,
}: {
  runtimeKey: string;
  value: string;
  runtimes: Record<string, RuntimeVersionInfo[]>;
  onChange: (version: string) => void;
}) {
  if (!runtimeKey) return null;
  const installed = (runtimes[runtimeKey] || []).filter((v) => v.installed !== false);
  // Make sure the currently-pinned version is visible in the dropdown even
  // if it's no longer installed (otherwise editing an existing service
  // silently flips it to "System default" on first save).
  const seen = new Set(installed.map((v) => v.version));
  if (value && !seen.has(value)) {
    installed.push({ version: value, installed: false });
  }
  const defaultVer = installed.find((v) => v.is_default)?.version;
  return (
    <div>
      <LabelWithHint hint={`Pin this service's ${runtimeKey} version. Install more under WHM → Software. Blank = use the host's default version (Software → "Set as default").`}>
        Runtime version ({runtimeKey})
      </LabelWithHint>
      <select className={selectCls} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">
          System default{defaultVer ? ` (${defaultVer})` : ""}
        </option>
        {installed.map((v) => (
          <option key={v.version} value={v.version}>
            {v.version}
            {v.is_default ? " ★ default" : v.active ? " (active)" : ""}
            {v.installed === false ? " (not installed)" : ""}
          </option>
        ))}
      </select>
      {installed.length === 0 && (
        <p className="text-[11px] text-panel-muted/70 mt-1">
          No {runtimeKey} versions installed yet — add one under <code className="font-mono">/software</code>.
        </p>
      )}
    </div>
  );
}

function ServiceCard({
  idx, svc, presets, runtimes, serverIP, availableDomains, onChange, onPreset, onRemove, hideRepoURL,
}: {
  idx: number;
  svc: NewServiceForm;
  presets: Record<string, Preset>;
  runtimes: Record<string, RuntimeVersionInfo[]>;
  serverIP: string;
  availableDomains: DomainOption[];
  onChange: (patch: Partial<NewServiceForm>) => void;
  onPreset: (key: string) => void;
  onRemove?: () => void;
  // hideRepoURL hides the per-service Repository URL input — used when the
  // project owns a single shared repo URL set on the wizard's Basics step
  // (and on the detail drawer's Add-service modal, which inherits the
  // project's repo automatically).
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
    if ((svc.alias_domains || []).includes(d) || d === svc.primary_domain) {
      toast.error(`"${d}" is already in the list`);
      return;
    }
    onChange({ alias_domains: [...(svc.alias_domains || []), d] });
    setAliasInput("");
  }

  function removeAlias(d: string) {
    onChange({ alias_domains: (svc.alias_domains || []).filter((a) => a !== d) });
  }

  function addEnv() {
    if (!envKey.trim()) return;
    onChange({ env_vars: { ...(svc.env_vars || {}), [envKey]: envVal } });
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

      {/* Branch was hoisted to the project level in 3.1.27 — every
          service in a project shares the same branch on the shared
          clone, so collecting it per service was redundant. The
          wizard's Basics step has the input now; Add Service inherits
          from the project automatically. Repository URL also lives at
          the project level when hideRepoURL is set; the legacy per-
          service URL field stays here for old standalone-repo flows. */}
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

      {/* Alias domains — extra server_name entries on the same nginx
          vhost so one service (and one backend port) answers on any
          number of hostnames. Renders for every role because nginx
          doesn't care what the upstream is: whether the location block
          is a proxy_pass to a backend port, a static root, or both
          (fullstack), aliases just expand the server_name list and
          share the same SAN cert. */}
      <div>
        <LabelWithHint hint="Extra domains that should hit this same service. All domains share one nginx vhost and one Let's Encrypt cert (SAN list). Each alias needs its own A record pointing at this server's IP — or CNAME-ing to the primary works too.">Alias domains</LabelWithHint>
        <div className="flex gap-2">
          <input
            className={inputCls}
            value={aliasInput}
            onChange={(e) => setAliasInput(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); addAlias(); } }}
            placeholder="www.example.com  or  another-domain.com"
          />
          <button onClick={addAlias} className="px-3 py-2 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text">Add</button>
        </div>
        {(svc.alias_domains || []).length > 0 && (
          <div className="flex flex-wrap gap-1 mt-2">
            {(svc.alias_domains || []).map((d) => (
              <span key={d} className="inline-flex items-center gap-1 px-2 py-0.5 text-[11px] bg-panel-bg border border-panel-border rounded text-panel-muted">
                {d}
                <button onClick={() => removeAlias(d)} className="text-panel-muted/60 hover:text-red-400"><X size={10} /></button>
              </span>
            ))}
          </div>
        )}
      </div>

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
            <RuntimeVersionPicker
              runtimeKey={presetToRuntimeKey(presets[svc.framework]?.app_type, svc.role)}
              value={svc.runtime_version}
              runtimes={runtimes}
              onChange={(v) => onChange({ runtime_version: v })}
            />
          </div>
        </Disclosure>
      )}

      <Disclosure title={`Environment variables (${Object.keys(svc.env_vars || {}).length})`} icon={<KeyRound size={13} />}>
        <div className="space-y-2">
          {Object.entries(svc.env_vars || {}).map(([k, v]) => (
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
//
// Switched from a plain <select> to SearchableSelect because the dropdown
// can carry hundreds of domains on a busy panel — scrolling for
// "wl-vrndor.web.restro.easycrm4u.com" through 200 lines is a UX
// failure. The searchable variant filters as the operator types and
// renders the optional `owner` hint next to each row so look-alike
// subdomains stay disambiguated.
//
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
  const options = domains.map((d) => ({
    value: d.domain,
    label: d.domain,
    // Owner hint helps disambiguate look-alike subdomains across
    // multiple vendors. DomainOption may not carry owner info on
    // every code path; guarded so it just doesn't render when blank.
    hint: (d as any).user || (d as any).owner_email,
  }));
  // Preserve a stored value that's no longer in the live list (e.g.
  // domain deleted after this service was created) — append a
  // sentinel option so editing the service doesn't silently wipe it.
  if (value && !options.some((o) => o.value === value)) {
    options.push({ value, label: value, hint: "(not registered)" });
  }
  return (
    <SearchableSelect
      value={value}
      onChange={onChange}
      options={options}
      placeholder="— select a domain —"
      emptyMessage="No domains match — clear the filter to pick from the full list, or register the domain under WHM → Domains first."
    />
  );
}

// ──────────────────────────────────────────────────────────────────────────
// DNS hint block — shows the exact A + CNAME records needed
// ──────────────────────────────────────────────────────────────────────────

function DnsHint({ role, primary, aliases, serverIP }: { role: string; primary: string; aliases: string[]; serverIP: string }) {
  // One hint block for every role. Every domain — primary OR alias —
  // needs to point at THIS server. For aliases we show A records as
  // the default recommendation because when the alias is a completely
  // independent domain (e.g. betazeninfotech.com + bipvtltd.com) the
  // CNAME-to-primary advice the old UI gave was confusing. If the
  // alias is a subdomain of the primary (e.g. www.primary.com), a
  // CNAME to the primary is equally valid and slightly simpler.
  const ip = serverIP || "YOUR_SERVER_IP";
  return (
    <div className="rounded-lg border border-blue-500/20 bg-blue-500/5 p-3 text-[11px] space-y-1">
      <div className="flex items-center gap-1.5 text-blue-400 font-medium">
        <Globe size={12} /> Required DNS {aliases.length > 0 ? "records" : ""}
      </div>
      <div className="text-panel-muted space-y-0.5">
        <div><code>A      {primary}{"  "}→  {ip}</code></div>
        {aliases.map((a) => (
          <div key={a}><code>A      {a}{"  "}→  {ip}</code></div>
        ))}
      </div>
      {aliases.length > 0 && (
        <div className="text-panel-muted/70 text-[10px] pt-1">
          Let's Encrypt issues one certificate covering primary + all aliases (SAN list) after DNS propagates. Subdomains of the primary can CNAME to it instead of repeating the A record.
        </div>
      )}
    </div>
  );
}

// downloadProjectExport fetches the JSON manifest via the auth-bearing api
// client (so the JWT travels with the request, unlike a bare anchor href)
// and triggers a Blob download with a friendly filename. The backend sets
// Content-Disposition too but browsers ignore that when JS reads the body
// via XHR/fetch, so we build the anchor ourselves.
async function downloadProjectExport(project: Project) {
  try {
    const res = await api.get(`/projects/${project.id}/export`);
    // api.get() returns the parsed JSON body, not a Response — wrap it
    // back into a Blob so we get a proper download instead of inlining
    // the response into the page.
    const body = JSON.stringify(res?.data ?? res, null, 2);
    const blob = new Blob([body], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const slug = (project.slug || project.name || "project").replace(/[^a-zA-Z0-9-]+/g, "-");
    const a = document.createElement("a");
    a.href = url;
    a.download = `${slug}.deploy.json`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
    toast.success("Manifest downloaded");
  } catch (e: any) {
    toast.error(e?.response?.data?.error?.message || e?.message || "Export failed");
  }
}

// downloadServicesExport is the per-project services snapshot — the
// "Export JSON" toolbar button next to Deploy all. Distinct from the
// project-wide downloadProjectExport above: this manifest is just the
// services array (with project id + slug for context), in the shape
// the Import JSON + Edit JSON endpoints accept on the wire.
async function downloadServicesExport(project: Project) {
  try {
    const res = await api.get(`/projects/${project.id}/services/export`);
    const body = JSON.stringify(res?.data ?? res, null, 2);
    const blob = new Blob([body], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const slug = (project.slug || project.name || "project").replace(/[^a-zA-Z0-9-]+/g, "-");
    const a = document.createElement("a");
    a.href = url;
    a.download = `${slug}.services.json`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
    toast.success("Services exported");
  } catch (e: any) {
    toast.error(e?.response?.data?.error?.message || e?.message || "Export failed");
  }
}

// ──────────────────────────────────────────────────────────────────────────
// Project detail drawer — services table, webhook card, PAT rotate, logs
// ──────────────────────────────────────────────────────────────────────────

function ProjectDetailDrawer({
  project: initialProject, serverIP, presets, runtimes, availableDomains, onClose, onChanged,
}: {
  project: Project;
  serverIP: string;
  presets: Record<string, Preset>;
  runtimes: Record<string, RuntimeVersionInfo[]>;
  availableDomains: DomainOption[];
  onClose: () => void;
  onChanged: () => void;
}) {
  // 3.1.80 — local mirror of the project so reopening the drawer doesn't
  // show stale last_webhook_at / paused / auto_deploy fields from the
  // cached project-list snapshot. Refetched on mount + after every
  // mutating action; rest of the drawer body still reads `project.X`
  // unchanged via the alias below.
  const [liveProject, setLiveProject] = useState<Project>(initialProject);
  const project = liveProject;
  const refreshProject = async () => {
    try {
      const r = await api.get(`/projects/${initialProject.id}`);
      const p = r?.data?.data;
      if (p) setLiveProject(p);
    } catch { /* ignore — stale prop still renders something */ }
  };

  const [services, setServices] = useState<ProjectService[]>([]);
  const [webhook, setWebhook] = useState<WebhookInfo | null>(null);
  const [activity, setActivity] = useState<ProjectActivity | null>(null);
  // 3.1.80 — Activity now takes a limit. Default 10 in the card;
  // "Show all" bumps to 500 (the backend's max) without a re-mount.
  const [activityLimit, setActivityLimit] = useState<number>(10);
  const fetchActivity = (limit?: number) => {
    const n = limit ?? activityLimit;
    api.get(`/projects/${initialProject.id}/activity?limit=${n}`)
      .then((r) => setActivity(r.data?.data || null))
      .catch(() => {});
  };
  const [logsFor, setLogsFor] = useState<ProjectService | null>(null);
  const [addingService, setAddingService] = useState(false);
  const [bulkUploading, setBulkUploading] = useState(false);
  // 3.1.76 — JSON-flavoured bulk operations on the Services toolbar.
  // importingJSON opens the additive "Import JSON" modal; editingJSON
  // opens the in-place "Edit JSON" modal; the Export action triggers
  // an immediate download via downloadServicesExport (no modal).
  const [importingJSON, setImportingJSON] = useState(false);
  const [editingJSON, setEditingJSON] = useState(false);
  const [rotating, setRotating] = useState(false);
  const [newPAT, setNewPAT] = useState("");
  const [secretRevealed, setSecretRevealed] = useState(false);
  // Webhook secret regeneration state (v3.1.64): two-step UX so the
  // operator can't accidentally invalidate a working webhook with one
  // misclick. Click 1 sets confirmingRegen=true and swaps the button
  // for a 5-second "Click again to confirm" affordance; click 2 fires
  // the request and shows a persistent toast pinning the new secret
  // until the operator dismisses it (so they have time to paste into
  // GitHub even if they tabbed away).
  const [regenerating, setRegenerating] = useState(false);
  const [confirmingRegen, setConfirmingRegen] = useState(false);
  const [editingProject, setEditingProject] = useState(false);
  const [editingService, setEditingService] = useState<ProjectService | null>(null);

  // Per-action loading state — drives both spinner-on-button and disabled
  // state so the operator sees instant feedback the click registered, and
  // can't double-click into a queued duplicate action.
  const [actionInFlight, setActionInFlight] = useState<null | "deploy" | "restart" | "stop" | "start" | "pause" | "pull">(null);

  useEffect(() => {
    // 3.1.80 — refetch EVERYTHING on mount so reopening the drawer
    // never shows stale data. The project prop coming in from the list
    // is a snapshot from the last /projects fetch and can be minutes
    // old by the time the operator clicks Open again.
    refreshProject();
    refresh();
    api.get(`/projects/${initialProject.id}/webhook`).then((r) => setWebhook(r.data?.data || null)).catch(() => {});
    fetchActivity(10);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialProject.id]);

  // 3.1.80 — burst-poll helper. Called after every mutating action
  // (Deploy all, Pull, Restart, Stop, Start, Pause/Resume) so the
  // drawer reflects the backend's new state within ~1s without forcing
  // the operator to click Refresh. The slow 3s background poll
  // already covers steady-state deploying → running transitions; this
  // burst covers the gap between the action firing and the queue
  // actually picking it up. Fires 4 times at 400/900/1500/2500 ms;
  // also re-pulls activity + the live project so the badge times
  // re-render.
  function burstRefresh() {
    [400, 900, 1500, 2500].forEach((delay) => {
      setTimeout(() => {
        refresh();
        fetchActivity();
        refreshProject();
      }, delay);
    });
  }

  // Aggregate state across the project's backend services. Drives which
  // toolbar buttons are visually emphasised vs. dimmed — Stop only matters
  // when something is running; Start only matters when something is stopped.
  const backendSvcs = services.filter((s) => s.role === "backend");
  const runningCount = backendSvcs.filter((s) => s.status === "running").length;
  const stoppedCount = backendSvcs.filter((s) => s.status === "stopped").length;
  const deployingCount = backendSvcs.filter((s) => s.status === "deploying" || s.status === "pending").length;
  const errorCount = backendSvcs.filter((s) => s.status === "error" || s.status === "failed").length;
  const totalBackends = backendSvcs.length;
  const allRunning = totalBackends > 0 && runningCount === totalBackends;
  const allStopped = totalBackends > 0 && stoppedCount === totalBackends;

  // Poll services while any of them is transitioning (deploying / pending /
  // queue-full). Background worker finishes asynchronously — without this
  // the drawer shows "deploying" forever until the user clicks Refresh.
  // Polling stops automatically once every service is in a terminal
  // state (running / stopped / error / static).
  useEffect(() => {
    const pending = services.some((s) => s.status === "deploying" || s.status === "pending" || s.status === "queue-full");
    if (!pending) return;
    // 3.1.80 — while a deploy is mid-flight, also tick Activity so
    // the recent-deployments list grows in real time + LastDeploy
    // updates without a manual click. Same 3s cadence as services.
    const id = setInterval(() => { refresh(); fetchActivity(); }, 3000);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [services, initialProject.id]);

  async function refresh() {
    try {
      const r = await api.get(`/projects/${initialProject.id}/services`);
      setServices(r.data?.data || []);
    } catch { /* ignore */ }
  }

  async function handleDeployService(svc: ProjectService) {
    try {
      await api.post(`/projects/${project.id}/services/${svc.id}/deploy`);
      toast.success(`${svc.name}: deploy queued`);
      // 3.1.80 — same burst pattern as Deploy all so the single-
      // service flow has matching UX. Activity also ticks so the
      // new row appears in the recent-deployments list.
      refresh();
      fetchActivity();
      burstRefresh();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Deploy failed");
    }
  }

  async function handleDeployAll() {
    setActionInFlight("deploy");
    try {
      await api.post(`/projects/${project.id}/deploy`);
      toast.success("Deploy all queued");
      // 3.1.80 — burst-poll covers the gap between "queued" and the
      // backend worker picking up the job. Pre-fix the operator saw
      // "active" status until the next manual Refresh; now the
      // deploying → running transition surfaces automatically within
      // ~1s. The 3s background poll keeps ticking once one service
      // is mid-deploy.
      await refresh();
      fetchActivity();
      burstRefresh();
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
      // Persistent toast with the post-rotate checklist: the previous
      // one-word "PAT rotated" toast left operators wondering whether
      // the next push would still trigger a deploy, whether they had
      // to re-test the webhook, etc. Spell out exactly what changed
      // and what didn't, with a "Run a test deploy" CTA so they can
      // verify the new token clones cleanly without leaving the page.
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
              <div className="flex gap-2 pt-1">
                <button
                  onClick={() => { toast.dismiss(t.id); handleDeployAll(); }}
                  className="px-2 py-0.5 text-[11px] bg-blue-600 hover:bg-blue-700 text-white rounded"
                >
                  Run test deploy
                </button>
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
        { duration: 12000 }
      );
      setNewPAT("");
      onChanged();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    } finally {
      setRotating(false);
    }
  }

  // Two-step confirm so a single misclick can't invalidate a working
  // webhook. First click swaps the button into a 5s "Click again to
  // confirm" affordance; second click within the window fires. Outside
  // the window the state resets and the next click starts fresh.
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
      if (!newSecret) {
        throw new Error("Server didn't return a new secret");
      }
      // Copy the new secret to clipboard so the operator's first paste
      // into GitHub's Secret field is the right value, even before they
      // notice the toast. copyToClipboard is the shared util used
      // across the panel — silent failure-tolerant.
      copyToClipboard(newSecret);
      // Persistent toast that pins the value until dismissed AND offers
      // a one-click open-in-GitHub link (the panel can't infer the repo
      // page reliably for every git host, so we route through the
      // configured git_repo_url's HTML view when it looks like GitHub).
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
                  className="px-2 py-0.5 text-[11px] bg-blue-600 hover:bg-blue-700 text-white rounded inline-flex items-center gap-1"
                >
                  <Copy size={11} /> Copy
                </button>
                {/^https:\/\/github\.com\//i.test(project.git_repo_url || "") && (
                  <a
                    href={ghSettingsURL}
                    target="_blank"
                    rel="noreferrer"
                    className="px-2 py-0.5 text-[11px] bg-panel-bg border border-panel-border hover:border-blue-500/50 text-panel-text rounded inline-flex items-center gap-1"
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
      // Force a refetch of the webhook info card so the Secret field on
      // the page (still showing the OLD value) updates to the new one.
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
      // Backend writes the new status to MongoDB synchronously inside
      // ServiceAction, so a single refresh() is enough — no setTimeout
      // race. We refresh explicitly so the toolbar buttons re-evaluate
      // (allRunning / allStopped) before the loading spinner clears.
      await refresh();
      // 3.1.80 — Pull writes a deployment record too; tick Activity
      // so the new entry surfaces without a manual click.
      if (action === "pull") fetchActivity();
      burstRefresh();
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
      // 3.1.80 — pull the updated project so the badge flips
      // immediately, without waiting on the parent list reload that
      // onChanged kicks off.
      refreshProject();
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
        {/* Project toolbar — project-wide actions. Each button:
              - shows a spinner while its own action is in flight
              - disables every button (so a user can't fire Restart-all
                while Stop-all is still finishing)
              - dims when not applicable (Stop dims when nothing is
                running; Start dims when nothing is stopped) */}
        <div className="flex flex-wrap items-center gap-2 text-xs">
          {/* Aggregate status pill */}
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
            title="git pull on the project's shared clone — fetches new commits without rebuild. Then click Redeploy on individual services to apply."
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
            onClick={() => {
              // Open the project's on-disk folder in the WHM File
              // Manager, in a new tab. project_dir was stamped at
              // Provision time and survives every code update — for
              // the rare legacy project where it's blank, fall back
              // to the user's /home root and let the operator
              // navigate. `noopener,noreferrer` is standard hygiene
              // for window.open targets opened from JS.
              const path = project.project_dir || (project.user ? `/home/${project.user}` : "/home");
              window.open(`/whm/files?path=${encodeURIComponent(path)}`, "_blank", "noopener,noreferrer");
            }}
            disabled={actionInFlight !== null}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border border-panel-border hover:border-blue-500/40 disabled:opacity-50 disabled:cursor-not-allowed rounded-lg text-panel-text"
            title={project.project_dir ? `Open ${project.project_dir} in the File Manager (new tab)` : "Open the project's user home in the File Manager (project_dir not set)"}
          >
            <FolderOpen size={13} /> Open folder
          </button>

          <button
            onClick={() => downloadProjectExport(project)}
            disabled={actionInFlight !== null}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-panel-surface border border-panel-border hover:border-blue-500/40 disabled:opacity-50 disabled:cursor-not-allowed rounded-lg text-panel-text"
            title="Download project deploy settings as JSON (no secrets — paste a fresh PAT on import)"
          >
            <Download size={13} /> Export JSON
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
            doesn't clutter the day-to-day ops view. The user asked
            "how can I copy id/name for deploy software for api
            developer mode" — this is the answer: every id, slug,
            user, and webhook URL in one place with one-click copy.

            Service-level ids live next to each service name in the
            Services list below — same CopyButton component. */}
        <Disclosure
          title="API / Developer IDs — for /api/v1/external/* calls"
          icon={<Code2 size={13} className="text-blue-400" />}
        >
          <div className="space-y-2 text-xs">
            <p className="text-panel-muted">
              Use these ids with the Programmatic API. Per-service ids appear next to each service name in the Services list below. See <a href="/docs/api/" target="_blank" rel="noopener noreferrer" className="text-blue-400 hover:underline">API docs</a> for the full route set.
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

        {/* Activity card — last commit, deploy stats, recent deploys, runtime */}
        {activity && (
          <Card>
            <div className="p-4 space-y-3">
              <div className="flex items-center justify-between">
                <div className="inline-flex items-center gap-2 text-sm font-medium text-panel-text">
                  <RotateCw size={15} className="text-blue-400" /> Activity
                </div>
                <button onClick={() => fetchActivity()} className="text-xs text-panel-muted hover:text-panel-text inline-flex items-center gap-1">
                  <RefreshCw size={11} /> Refresh
                </button>
              </div>

              {/* Top stat tiles */}
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

              {/* Last commit */}
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

              {/* Recent deployments table (3.1.80 — full row with trigger
                  badge, absolute timestamp tooltip, error preview +
                  copy-to-clipboard, "Show all (N)" affordance). */}
              {(activity.recent_deployments?.length ?? 0) > 0 && (
                <div>
                  <div className="flex items-center justify-between mb-1.5">
                    <div className="text-[10px] uppercase tracking-wider text-panel-muted">
                      Recent deployments
                      <span className="ml-1 normal-case text-panel-muted/60">
                        showing {activity.recent_deployments?.length ?? 0} of {activity.deploys.total}
                      </span>
                    </div>
                    {/* Toggle between the 10-default and the full window.
                        Backend caps at 500 — enough for any sane project's
                        lifetime history; older entries can still be queried
                        directly via the API. */}
                    {(activity.deploys.total > 10 || activityLimit !== 10) && (
                      <button
                        onClick={() => {
                          const next = activityLimit === 10 ? 500 : 10;
                          setActivityLimit(next);
                          fetchActivity(next);
                        }}
                        className="text-[11px] text-blue-400 hover:text-blue-300 inline-flex items-center gap-1"
                        type="button"
                      >
                        {activityLimit === 10
                          ? <>Show all ({activity.deploys.total}) <ChevronDown size={11} /></>
                          : <>Show only 10 <ChevronRight size={11} className="rotate-90" /></>}
                      </button>
                    )}
                  </div>
                  <div className={"divide-y divide-panel-border rounded-lg border border-panel-border overflow-hidden " + (activityLimit !== 10 ? "max-h-96 overflow-y-auto" : "")}>
                    {(activity.recent_deployments || []).map((d) => (
                      <DeploymentRow key={d.id} d={d} />
                    ))}
                  </div>
                </div>
              )}

              {/* Per-service runtime */}
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
                <LastWebhookBadge
                  at={project.last_webhook_at}
                  event={project.last_webhook_event}
                  errorMsg={project.last_webhook_error}
                  errorAt={project.last_webhook_error_at}
                />
              </div>
              {project.last_webhook_error && (
                (() => {
                  const okTs = project.last_webhook_at ? new Date(project.last_webhook_at).getTime() : 0;
                  const errTs = project.last_webhook_error_at ? new Date(project.last_webhook_error_at).getTime() : 0;
                  if (errTs <= okTs) return null;
                  return (
                    <div className="px-3 py-2 rounded border border-red-500/30 bg-red-500/10 text-[12px] text-red-300 leading-snug">
                      <div className="font-medium text-red-200 mb-0.5">Last delivery rejected</div>
                      <div>{project.last_webhook_error}</div>
                    </div>
                  );
                })()
              )}
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
                  {/* Regenerate: two-click confirm so a misclick can't kill a working webhook */}
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
            <div className="flex items-center gap-2 flex-wrap justify-end">
              <button onClick={handleDeployAll} className="px-3 py-1.5 text-xs bg-blue-600 hover:bg-blue-700 text-white rounded-lg">Deploy all</button>
              <button onClick={() => downloadServicesExport(project)} disabled={services.length === 0} className="px-3 py-1.5 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text disabled:opacity-50 inline-flex items-center gap-1" title="Download all services on this project as a portable JSON manifest"><Download size={11} /> Export JSON</button>
              <button onClick={() => setImportingJSON(true)} className="px-3 py-1.5 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text inline-flex items-center gap-1" title="Add new services to this project from a JSON manifest"><Upload size={11} /> Import JSON</button>
              <button onClick={() => setEditingJSON(true)} disabled={services.length === 0} className="px-3 py-1.5 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text disabled:opacity-50 inline-flex items-center gap-1" title="Edit existing services in bulk via JSON editor"><FileJson size={11} /> Edit JSON</button>
              <button onClick={() => setBulkUploading(true)} className="px-3 py-1.5 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text" title="Bulk add services from CSV / Excel">Bulk upload</button>
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
          // Prefer the project-level repo URL (the canonical, post-refactor
          // field shown in the Edit Project modal). The services-fallback
          // is kept only for legacy projects that pre-date the shared-clone
          // refactor and still carry per-service URLs without a project
          // URL set. Without this, a brand-new project with zero services
          // rendered "(no repo set)" in the Add Service banner — even
          // though the project DID have a repo URL — and the operator
          // assumed they had to enter one (it was always read from the
          // project anyway).
          projectRepoURL={
            project.git_repo_url
            || services.find((s) => s.git_repo_url)?.git_repo_url
            || ""
          }
          presets={presets}
          runtimes={runtimes}
          serverIP={serverIP}
          // Tenant-scope the dropdown: only show domains owned by THIS
          // project's vendor. The project's files live under
          // /home/<project.user>/projects/<slug>/, so picking a domain
          // owned by some other vendor would either fail to bind (SSL
          // issuance can't write to /home/<other>/) or worse, create a
          // vhost that points at the wrong tenant's home directory.
          // Pre-3.1.17 the WHM admin saw EVERY domain on the box in
          // this dropdown — across every vendor — making cross-tenant
          // mistakes one click away. The cPanel side already filtered
          // by ListOwn naturally; this fix brings the WHM admin path
          // to the same scope.
          availableDomains={availableDomains.filter((d) => !project.user || d.user === project.user)}
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
          runtimes={runtimes}
          // Same tenant-scope filter as Add Service above — the
          // service belongs to a project that belongs to a vendor;
          // editing it should never offer a domain owned by a
          // different vendor as a primary or alias candidate.
          availableDomains={availableDomains.filter((d) => !project.user || d.user === project.user)}
          serverIP={serverIP}
          onClose={() => setEditingService(null)}
          onSaved={() => { setEditingService(null); refresh(); }}
        />
      )}

      {logsFor && (
        <LogsModal projectId={project.id} svc={logsFor} onClose={() => setLogsFor(null)} />
      )}

      {bulkUploading && (
        <BulkUploadServicesModal
          isOpen
          projectId={project.id}
          projectName={project.name}
          onClose={() => setBulkUploading(false)}
          onUploaded={refresh}
        />
      )}

      {importingJSON && (
        <ImportServicesJSONModal
          projectId={project.id}
          projectName={project.name}
          onClose={() => setImportingJSON(false)}
          onImported={refresh}
        />
      )}

      {editingJSON && (
        <EditServicesJSONModal
          projectId={project.id}
          projectName={project.name}
          services={services}
          onClose={() => setEditingJSON(false)}
          onSaved={refresh}
        />
      )}
    </Modal>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Import project from JSON manifest
//
// Drag-and-drop / file-picker for a ProjectExport JSON, plus a per-service
// domain override grid for the common "clone onto same panel" case where
// the source's primary_domain values would collide with the unique-domain
// constraint. Optional GitHub PAT field for private repos.
//
// The Submit call goes to POST /whm/projects/import which, under the hood,
// re-runs the same Provision pipeline as the manual New Project wizard —
// atomic rollback on any service-level failure, slug allocation, webhook
// secret minting all behave identically.
// ──────────────────────────────────────────────────────────────────────────

type ManifestService = {
  name: string;
  role: string;
  primary_domain: string;
  git_subpath?: string;
  port?: number;
};
type ManifestShape = {
  schema_version?: number;
  panel_version?: string;
  exported_at?: string;
  project?: {
    name?: string;
    description?: string;
    git_repo_url?: string;
    git_branch?: string;
    auto_deploy?: boolean;
    user?: string;
  };
  services?: ManifestService[];
};

function ImportProjectModal({ onClose, onImported }: { onClose: () => void; onImported: (created?: Project) => void }) {
  const [manifestText, setManifestText] = useState<string>("");
  const [manifest, setManifest] = useState<ManifestShape | null>(null);
  const [parseError, setParseError] = useState<string>("");
  const [overrideName, setOverrideName] = useState<string>("");
  const [overrideDomains, setOverrideDomains] = useState<Record<string, string>>({});
  const [githubPAT, setGithubPAT] = useState<string>("");
  const [user, setUser] = useState<string>("");
  const [importing, setImporting] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const [importError, setImportError] = useState<string>("");

  function ingest(text: string) {
    setManifestText(text);
    setImportError("");
    if (!text.trim()) {
      setManifest(null);
      setParseError("");
      return;
    }
    try {
      const parsed = JSON.parse(text) as ManifestShape;
      if (!parsed || typeof parsed !== "object") throw new Error("Top-level value must be a JSON object.");
      if (!parsed.schema_version) throw new Error("Missing schema_version — this doesn't look like a project export.");
      if (!parsed.project?.git_repo_url) throw new Error("Missing project.git_repo_url.");
      if (!parsed.services?.length) throw new Error("Manifest has zero services.");
      setManifest(parsed);
      setParseError("");
      // Seed defaults from the manifest so the operator can submit
      // immediately if they're importing onto a fresh panel where no
      // overrides are needed.
      setOverrideName(parsed.project?.name || "");
      setUser(parsed.project?.user || "");
      const seeded: Record<string, string> = {};
      for (const svc of parsed.services) {
        if (svc.primary_domain) seeded[svc.primary_domain] = svc.primary_domain;
      }
      setOverrideDomains(seeded);
    } catch (e: any) {
      setManifest(null);
      setParseError(e?.message || "Invalid JSON");
    }
  }

  async function onFile(file: File) {
    const text = await file.text();
    ingest(text);
  }

  async function submit() {
    if (!manifest) return;
    setImporting(true);
    setImportError("");
    try {
      // Pass only override_domains entries that actually differ from the
      // manifest value — sending the identity map would still work but
      // makes the request body confusingly large.
      const diffDomains: Record<string, string> = {};
      for (const svc of manifest.services || []) {
        const replacement = overrideDomains[svc.primary_domain]?.trim();
        if (replacement && replacement !== svc.primary_domain) {
          diffDomains[svc.primary_domain] = replacement;
        }
      }
      const payload = {
        manifest,
        github_pat: githubPAT.trim() || undefined,
        override_name: overrideName.trim() && overrideName.trim() !== manifest.project?.name ? overrideName.trim() : undefined,
        override_domains: Object.keys(diffDomains).length > 0 ? diffDomains : undefined,
        user: user.trim() || undefined,
      };
      const res = await api.post<{ data: { project: Project } }>("/projects/import", payload);
      toast.success("Project imported");
      onImported(res?.data?.data?.project);
    } catch (e: any) {
      const apiErr = e?.response?.data?.error;
      const msg = apiErr?.message || e?.message || "Import failed";
      // BUILD_FAILED carries the per-service build output — surface it
      // inline so the operator can see the install/build log without
      // hunting in the project drawer (the project has already been
      // rolled back by Provision at this point).
      if (apiErr?.code === "BUILD_FAILED" && apiErr?.details?.output) {
        setImportError(`${msg}\n\n${apiErr.details.output}`);
      } else {
        setImportError(msg);
      }
    } finally {
      setImporting(false);
    }
  }

  return (
    <Modal isOpen onClose={onClose} title="Import project from JSON" size="lg">
      <div className="space-y-4">
        {/* Drop zone / textarea pair */}
        {!manifest && (
          <div
            onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
            onDragLeave={() => setDragOver(false)}
            onDrop={(e) => {
              e.preventDefault();
              setDragOver(false);
              const f = e.dataTransfer.files?.[0];
              if (f) onFile(f);
            }}
            className={
              "border-2 border-dashed rounded-lg p-6 text-center transition-colors " +
              (dragOver ? "border-blue-500 bg-blue-500/5" : "border-panel-border")
            }
          >
            <FileJson size={28} className="text-blue-400 mx-auto mb-2" />
            <p className="text-sm text-panel-text mb-1">Drop a <code className="text-blue-300">.deploy.json</code> file here</p>
            <p className="text-[11px] text-panel-muted mb-3">Exported from any Betazen Server Panel — your own or a colleague's.</p>
            <label className="inline-block px-3 py-1.5 text-xs bg-panel-bg border border-panel-border rounded-lg text-panel-text cursor-pointer hover:border-blue-500/50">
              Choose file…
              <input
                type="file"
                accept=".json,application/json"
                className="hidden"
                onChange={(e) => { const f = e.target.files?.[0]; if (f) onFile(f); }}
              />
            </label>
            <div className="mt-4">
              <details className="text-left">
                <summary className="text-[11px] text-panel-muted cursor-pointer hover:text-panel-text">…or paste the JSON directly</summary>
                <textarea
                  className={inputCls + " mt-2 font-mono text-[11px]"}
                  rows={6}
                  placeholder='{"schema_version": 1, "project": {...}, "services": [...]}'
                  onChange={(e) => ingest(e.target.value)}
                  value={manifestText}
                />
              </details>
            </div>
          </div>
        )}

        {parseError && (
          <div className="px-3 py-2 rounded border border-red-500/30 bg-red-500/10 text-[12px] text-red-300">
            <div className="font-medium text-red-200 mb-0.5">Couldn't read manifest</div>
            <div>{parseError}</div>
          </div>
        )}

        {manifest && (
          <>
            <div className="px-3 py-2 rounded border border-blue-500/20 bg-blue-500/5 text-[12px] text-panel-text">
              <div className="flex items-center justify-between">
                <div className="font-medium inline-flex items-center gap-1.5">
                  <FileJson size={13} className="text-blue-400" />
                  Manifest loaded
                </div>
                <button
                  className="text-[11px] text-panel-muted hover:text-panel-text inline-flex items-center gap-1"
                  onClick={() => { setManifest(null); setManifestText(""); setOverrideDomains({}); setOverrideName(""); }}
                >
                  <X size={11} /> Clear
                </button>
              </div>
              <div className="grid grid-cols-2 gap-x-4 gap-y-1 mt-2 text-[11px] text-panel-muted">
                <div>Schema: <code className="text-panel-text">v{manifest.schema_version}</code></div>
                <div>Source panel: <code className="text-panel-text">{manifest.panel_version || "—"}</code></div>
                <div className="col-span-2">Repo: <code className="text-panel-text break-all">{manifest.project?.git_repo_url}</code></div>
                <div>Branch: <code className="text-panel-text">{manifest.project?.git_branch || "main"}</code></div>
                <div>Services: <code className="text-panel-text">{manifest.services?.length}</code></div>
              </div>
            </div>

            <div>
              <label className={labelCls}>Project name</label>
              <input
                className={inputCls}
                value={overrideName}
                onChange={(e) => setOverrideName(e.target.value)}
                placeholder={manifest.project?.name || ""}
              />
              <p className="text-[11px] text-panel-muted mt-1">
                Change this when re-importing onto the same panel — the slug derived from the name has a unique index, so the import will land under "{manifest.project?.name}-2" otherwise.
              </p>
            </div>

            <div>
              <label className={labelCls}>System user (optional)</label>
              <input
                className={inputCls}
                value={user}
                onChange={(e) => setUser(e.target.value)}
                placeholder={manifest.project?.user || "auto"}
              />
              <p className="text-[11px] text-panel-muted mt-1">
                Linux account that will own the project directory. Leave blank to auto-derive from the first service's primary domain owner.
              </p>
            </div>

            <div>
              <label className={labelCls}>GitHub Personal Access Token (optional)</label>
              <input
                className={inputCls}
                type="password"
                value={githubPAT}
                onChange={(e) => setGithubPAT(e.target.value)}
                placeholder="ghp_…"
                autoComplete="off"
                spellCheck={false}
              />
              <p className="text-[11px] text-panel-muted mt-1">
                Required only for private repos. The export never includes a PAT — paste a fresh one here. Stored encrypted at rest with AES-GCM.
              </p>
            </div>

            <div>
              <div className="flex items-center justify-between mb-1">
                <label className={labelCls + " mb-0"}>Domains</label>
                <span className="text-[11px] text-panel-muted">Remap to free domains if importing onto the source panel</span>
              </div>
              <div className="space-y-2 border border-panel-border rounded-lg p-2 bg-panel-bg/40">
                {(manifest.services || []).map((svc) => (
                  <div key={svc.name} className="grid grid-cols-12 gap-2 items-center text-[12px]">
                    <div className="col-span-3 truncate" title={svc.name}>
                      <code className="text-panel-text">{svc.name}</code>
                      <div className="text-[10px] text-panel-muted">{svc.role}</div>
                    </div>
                    <div className="col-span-4 truncate text-panel-muted text-[11px]" title={svc.primary_domain}>
                      {svc.primary_domain}
                    </div>
                    <div className="col-span-1 text-center text-panel-muted">→</div>
                    <div className="col-span-4">
                      <input
                        className={inputCls + " text-[11px] py-1"}
                        value={overrideDomains[svc.primary_domain] || ""}
                        onChange={(e) => setOverrideDomains((m) => ({ ...m, [svc.primary_domain]: e.target.value }))}
                        placeholder={svc.primary_domain}
                        spellCheck={false}
                      />
                    </div>
                  </div>
                ))}
              </div>
            </div>

            {importError && (
              <div className="px-3 py-2 rounded border border-red-500/30 bg-red-500/10 text-[12px] text-red-300">
                <div className="font-medium text-red-200 mb-0.5">Import failed — project rolled back</div>
                <pre className="whitespace-pre-wrap break-words font-mono text-[11px] max-h-48 overflow-y-auto">{importError}</pre>
              </div>
            )}
          </>
        )}

        <div className="flex justify-end gap-2 pt-2">
          <button onClick={onClose} className="px-4 py-2 text-sm text-panel-muted border border-panel-border rounded-lg">Cancel</button>
          <button
            onClick={submit}
            disabled={!manifest || importing}
            className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50 inline-flex items-center gap-2"
          >
            {importing && <RefreshCw size={14} className="animate-spin" />}
            {importing ? "Importing…" : "Import project"}
          </button>
        </div>
      </div>
    </Modal>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// FindReplaceTextarea — a JSON-editor-shaped <textarea> with a built-in
// find/replace toolbar.
//
// Why custom rather than dropping in Monaco/CodeMirror: the rest of the
// panel is plain text inputs; pulling in a 1 MB editor for two modals
// would inflate the WHM bundle and break the visual rhythm of every
// other form field. A textarea + find/replace bar covers ~95% of the
// real operator workflow ("find that one env var" / "rename a domain
// across every service") with ~250 lines of code and zero extra deps.
//
// Features:
//   - Find with case-sensitive and regex toggles
//   - Match counter "n / total" + Find prev / Find next
//   - Replace, Replace prev, Replace next, Replace all
//   - Keyboard shortcuts:
//       Ctrl/Cmd+F      open & focus Find
//       Ctrl/Cmd+H      open & focus Replace
//       F3, Enter       next match (Shift = previous)
//       Escape          close the bar (keeps text + selection)
// ──────────────────────────────────────────────────────────────────────────

type FindMatch = { start: number; end: number; groups?: string[] };

// escapeRegex turns the user's plain-find query into a safe RegExp source.
// Without this, a search for "[" or "$" would throw at construction time
// or quietly match against arbitrary substrings.
function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// scanMatches returns every match of `query` in `text` with the chosen
// flags. Returns [] for empty queries or invalid regex patterns (the bar
// still renders; the operator just sees "0 / 0" until they fix the
// expression).
function scanMatches(text: string, query: string, caseSensitive: boolean, useRegex: boolean): FindMatch[] {
  if (!query) return [];
  let re: RegExp;
  try {
    const flags = caseSensitive ? "g" : "gi";
    re = new RegExp(useRegex ? query : escapeRegex(query), flags);
  } catch {
    return [];
  }
  const out: FindMatch[] = [];
  let m: RegExpExecArray | null;
  // Manual exec loop so we can handle zero-width matches (e.g. `^`)
  // without spinning forever — bump lastIndex by 1 when the match
  // consumes no characters.
  while ((m = re.exec(text)) !== null) {
    out.push({ start: m.index, end: m.index + m[0].length, groups: m.slice(1) });
    if (m[0].length === 0) re.lastIndex++;
  }
  return out;
}

// expandReplace handles regex backreferences ($1, $2, ...) when regex
// mode is on. Plain (non-regex) mode replaces with the literal string —
// no $ interpolation, so an operator pasting a password containing $1
// doesn't get mangled.
function expandReplace(replacement: string, match: FindMatch, fullMatch: string, useRegex: boolean): string {
  if (!useRegex) return replacement;
  return replacement.replace(/\$([0-9]+|&)/g, (_, key) => {
    if (key === "&") return fullMatch;
    const idx = parseInt(key, 10) - 1;
    return match.groups && idx >= 0 && idx < match.groups.length ? match.groups[idx] : "";
  });
}

// ──────────────────────────────────────────────────────────────────────────
// DeploymentRow — one entry in the Activity card's Recent deployments
// list. Renders trigger as a colour-coded pill (manual / webhook / auto /
// api / custom / first-deploy), commit short SHA, full + relative
// timestamps, duration, and — on failure — an inline error preview with
// a one-click copy-to-clipboard button.
// ──────────────────────────────────────────────────────────────────────────

type RecentDeployment = ProjectActivity["recent_deployments"][number];

function CopyTextButton({ value, label = "Copy", okLabel = "Copied" }: { value: string; label?: string; okLabel?: string }) {
  const [ok, setOk] = useState(false);
  return (
    <button
      type="button"
      onClick={async (e) => {
        e.stopPropagation();
        if (await copyToClipboard(value)) {
          setOk(true);
          setTimeout(() => setOk(false), 1500);
        }
      }}
      className={"inline-flex items-center gap-1 px-1.5 py-0.5 rounded border text-[10px] " + (ok ? "border-green-500/40 text-green-300 bg-green-500/10" : "border-panel-border text-panel-muted hover:text-panel-text")}
      title="Copy error message to clipboard"
    >
      {ok ? <Check size={10} /> : <Copy size={10} />}
      {ok ? okLabel : label}
    </button>
  );
}

function DeploymentRow({ d }: { d: RecentDeployment }) {
  const [expanded, setExpanded] = useState(false);
  const dur = d.finished_at && d.started_at
    ? Math.max(0, Math.round((new Date(d.finished_at).getTime() - new Date(d.started_at).getTime()) / 1000))
    : null;
  const statusColor =
    d.status === "running" || d.status === "success" ? "text-green-400 bg-green-500/10 border-green-500/30"
      : d.status === "error" || d.status === "failed" ? "text-red-400 bg-red-500/10 border-red-500/30"
        : "text-blue-400 bg-blue-500/10 border-blue-500/30";

  // Trigger pill — every existing or future trigger string maps to a
  // colour so the operator can scan the column at a glance.
  // manual = blue (operator click)
  // webhook / auto / gitpush = purple (GitHub push)
  // api = amber (external programmatic call)
  // custom / other = panel-muted
  const triggerLabel = (() => {
    switch (d.trigger) {
      case "webhook": return "github push";
      case "auto":    return "auto-deploy";
      case "gitpush": return "github push";
      case "manual":  return "manual";
      case "api":     return "api";
      default:        return d.trigger || "custom";
    }
  })();
  const triggerColor = (() => {
    switch (d.trigger) {
      case "webhook":
      case "auto":
      case "gitpush":
        return "text-purple-300 bg-purple-500/10 border-purple-500/30";
      case "manual":
        return "text-blue-300 bg-blue-500/10 border-blue-500/30";
      case "api":
        return "text-amber-300 bg-amber-500/10 border-amber-500/30";
      default:
        return "text-panel-muted bg-panel-bg/40 border-panel-border";
    }
  })();

  const startedAbs = new Date(d.started_at).toLocaleString();
  const hasError = !!d.error_msg && (d.status === "error" || d.status === "failed");

  return (
    <div className="text-[11px]">
      <div className="px-3 py-2 flex items-center gap-2.5 hover:bg-panel-bg/30">
        <span className={`px-1.5 py-0.5 rounded border text-[10px] ${statusColor}`}>{d.status}</span>
        <span className={`px-1.5 py-0.5 rounded border text-[10px] ${triggerColor}`}>{triggerLabel}</span>
        <code className="text-blue-300 font-mono text-[10px]">{d.commit_sha ? d.commit_sha.substring(0, 7) : "—"}</code>
        <span className="text-panel-muted/80 ml-auto tabular-nums" title={`Started ${startedAbs}${d.finished_at ? ` · finished ${new Date(d.finished_at).toLocaleString()}` : ""}`}>
          {relativeTime(d.started_at)}
        </span>
        {dur !== null && <span className="text-panel-muted/60 tabular-nums w-12 text-right">{dur}s</span>}
        {hasError && (
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            className="text-panel-muted hover:text-red-300"
            title={expanded ? "Hide error" : "Show error"}
          >
            {expanded ? <ChevronDown size={11} /> : <ChevronRight size={11} />}
          </button>
        )}
      </div>
      {hasError && expanded && (
        <div className="px-3 pb-2 -mt-1">
          <div className="rounded border border-red-500/30 bg-red-500/5 p-2 space-y-1.5">
            <div className="flex items-center justify-between">
              <span className="text-[10px] uppercase tracking-wider text-red-400/70">Error</span>
              <CopyTextButton value={d.error_msg || ""} />
            </div>
            <pre className="text-[11px] text-red-300 font-mono whitespace-pre-wrap break-words max-h-40 overflow-y-auto">
              {d.error_msg}
            </pre>
          </div>
        </div>
      )}
    </div>
  );
}

function FindReplaceTextarea({
  value, onChange, rows, className, placeholder, spellCheck,
}: {
  value: string;
  onChange: (v: string) => void;
  rows?: number;
  className?: string;
  placeholder?: string;
  spellCheck?: boolean;
}) {
  const taRef = useRef<HTMLTextAreaElement | null>(null);
  const findRef = useRef<HTMLInputElement | null>(null);
  const replaceRef = useRef<HTMLInputElement | null>(null);

  const [open, setOpen] = useState(false);
  const [showReplace, setShowReplace] = useState(false);
  const [find, setFind] = useState("");
  const [replace, setReplace] = useState("");
  const [caseSensitive, setCaseSensitive] = useState(false);
  const [useRegex, setUseRegex] = useState(false);
  const [currentIdx, setCurrentIdx] = useState(0);

  // Re-scan on every text / query / flag change. Cheap enough for the
  // sub-MB JSON the editor holds — useMemo means scrolling/typing in
  // the textarea doesn't re-scan unless the inputs actually change.
  const matches = useMemo(
    () => scanMatches(value, find, caseSensitive, useRegex),
    [value, find, caseSensitive, useRegex],
  );

  // Keep currentIdx in range when matches shrink under us (e.g. after
  // a Replace removed the active match).
  useEffect(() => {
    if (currentIdx >= matches.length) setCurrentIdx(Math.max(0, matches.length - 1));
  }, [matches, currentIdx]);

  // selectMatch focuses the textarea and selects the match at idx. The
  // browser's native "scroll selection into view" kicks in on focus —
  // works in Chrome/Firefox/Safari without manual scrollTop maths.
  const selectMatch = (idx: number) => {
    const ta = taRef.current;
    if (!ta || !matches.length) return;
    const m = matches[idx];
    ta.focus();
    ta.setSelectionRange(m.start, m.end);
  };

  const next = () => {
    if (!matches.length) return;
    const i = (currentIdx + 1) % matches.length;
    setCurrentIdx(i);
    selectMatch(i);
  };
  const prev = () => {
    if (!matches.length) return;
    const i = (currentIdx - 1 + matches.length) % matches.length;
    setCurrentIdx(i);
    selectMatch(i);
  };

  // replaceCurrent patches one match and returns the new value + the
  // index where the next match starts (so the caller can move the
  // cursor without waiting for the matches array to re-scan via state).
  const replaceCurrent = (alsoMove: "next" | "prev" | "none"): void => {
    if (!matches.length) return;
    const m = matches[currentIdx];
    const fullMatch = value.slice(m.start, m.end);
    const repl = expandReplace(replace, m, fullMatch, useRegex);
    const newValue = value.slice(0, m.start) + repl + value.slice(m.end);
    onChange(newValue);
    // After the value changes, matches re-derive on next render. We
    // schedule the move-to-next/prev on a microtask so the new matches
    // are in place before we re-select.
    if (alsoMove === "none") return;
    queueMicrotask(() => {
      const refreshed = scanMatches(newValue, find, caseSensitive, useRegex);
      if (!refreshed.length) return;
      // Find the first match at-or-after the replacement's end (for
      // "next") or at-or-before its start (for "prev"). Falls through
      // to wrap-around if none exist on that side.
      const cursor = m.start + repl.length;
      let target: number;
      if (alsoMove === "next") {
        const idx = refreshed.findIndex((r) => r.start >= cursor);
        target = idx === -1 ? 0 : idx;
      } else {
        const before = refreshed
          .map((r, i) => ({ i, r }))
          .filter((x) => x.r.end <= m.start);
        target = before.length ? before[before.length - 1].i : refreshed.length - 1;
      }
      setCurrentIdx(target);
      const t = refreshed[target];
      const ta = taRef.current;
      if (ta) {
        ta.focus();
        ta.setSelectionRange(t.start, t.end);
      }
    });
  };

  const replaceAll = () => {
    if (!matches.length) return;
    // Walk matches in reverse so earlier offsets stay valid as we splice.
    let out = value;
    for (let i = matches.length - 1; i >= 0; i--) {
      const m = matches[i];
      const fullMatch = value.slice(m.start, m.end);
      const repl = expandReplace(replace, m, fullMatch, useRegex);
      out = out.slice(0, m.start) + repl + out.slice(m.end);
    }
    onChange(out);
    setCurrentIdx(0);
  };

  const openFind = (focusReplace = false) => {
    setOpen(true);
    if (focusReplace) setShowReplace(true);
    queueMicrotask(() => {
      (focusReplace ? replaceRef.current : findRef.current)?.focus();
      (focusReplace ? replaceRef.current : findRef.current)?.select();
    });
  };

  // Keyboard handler attached to BOTH the textarea and the find/replace
  // inputs so the shortcuts work no matter which has focus.
  const onKey = (e: ReactKeyboardEvent) => {
    const mod = e.ctrlKey || e.metaKey;
    if (mod && e.key.toLowerCase() === "f") {
      e.preventDefault();
      openFind(false);
      return;
    }
    if (mod && e.key.toLowerCase() === "h") {
      e.preventDefault();
      openFind(true);
      return;
    }
    if (!open) return;
    if (e.key === "Escape") {
      e.preventDefault();
      setOpen(false);
      taRef.current?.focus();
      return;
    }
    if (e.key === "F3") {
      e.preventDefault();
      e.shiftKey ? prev() : next();
      return;
    }
    // Enter in the Find box = next; Shift+Enter = prev. Doesn't fire
    // when focus is in the textarea (Enter there inserts a newline,
    // which is what the operator wants).
    if (e.key === "Enter" && (e.target === findRef.current || e.target === replaceRef.current)) {
      e.preventDefault();
      e.shiftKey ? prev() : next();
      return;
    }
  };

  const status = matches.length === 0
    ? (find ? "0 / 0" : "")
    : `${currentIdx + 1} / ${matches.length}`;

  return (
    <div className="space-y-1">
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => (open ? setOpen(false) : openFind(false))}
          className={"px-2 py-1 text-[11px] border rounded inline-flex items-center gap-1 " + (open ? "border-blue-500/50 text-blue-300 bg-blue-500/10" : "border-panel-border text-panel-muted hover:text-panel-text")}
          title="Find / Replace (Ctrl+F)"
        >
          <Search size={11} /> Find{open ? "" : "…"}
        </button>
        {!open && (
          <button
            type="button"
            onClick={() => openFind(true)}
            className="px-2 py-1 text-[11px] border border-panel-border rounded text-panel-muted hover:text-panel-text"
            title="Find & Replace (Ctrl+H)"
          >
            Replace…
          </button>
        )}
      </div>

      {open && (
        <div className="border border-panel-border rounded-lg bg-panel-bg/60 p-2 space-y-1 text-[11px]">
          <div className="flex items-center gap-1 flex-wrap">
            <input
              ref={findRef}
              value={find}
              onChange={(e) => setFind(e.target.value)}
              onKeyDown={onKey}
              placeholder="Find"
              className="flex-1 min-w-[140px] px-2 py-1 bg-panel-surface border border-panel-border rounded text-panel-text font-mono"
              spellCheck={false}
            />
            <button
              type="button"
              onClick={() => setCaseSensitive((v) => !v)}
              className={"px-1.5 py-1 border rounded font-mono " + (caseSensitive ? "border-blue-500/50 text-blue-300 bg-blue-500/10" : "border-panel-border text-panel-muted hover:text-panel-text")}
              title="Match case"
            >Aa</button>
            <button
              type="button"
              onClick={() => setUseRegex((v) => !v)}
              className={"px-1.5 py-1 border rounded font-mono " + (useRegex ? "border-blue-500/50 text-blue-300 bg-blue-500/10" : "border-panel-border text-panel-muted hover:text-panel-text")}
              title="Regular expression"
            >.*</button>
            <span className="px-1.5 py-1 text-panel-muted min-w-[52px] text-center font-mono">{status}</span>
            <button type="button" onClick={prev} disabled={!matches.length} className="px-2 py-1 border border-panel-border rounded text-panel-muted hover:text-panel-text disabled:opacity-40" title="Previous match (Shift+F3)">◀</button>
            <button type="button" onClick={next} disabled={!matches.length} className="px-2 py-1 border border-panel-border rounded text-panel-muted hover:text-panel-text disabled:opacity-40" title="Next match (F3)">▶</button>
            <button
              type="button"
              onClick={() => setShowReplace((v) => !v)}
              className={"px-2 py-1 border rounded " + (showReplace ? "border-blue-500/50 text-blue-300 bg-blue-500/10" : "border-panel-border text-panel-muted hover:text-panel-text")}
              title="Toggle replace row"
            >Replace ▾</button>
            <button type="button" onClick={() => setOpen(false)} className="px-1.5 py-1 border border-panel-border rounded text-panel-muted hover:text-panel-text" title="Close (Escape)"><X size={11} /></button>
          </div>
          {showReplace && (
            <div className="flex items-center gap-1 flex-wrap">
              <input
                ref={replaceRef}
                value={replace}
                onChange={(e) => setReplace(e.target.value)}
                onKeyDown={onKey}
                placeholder={useRegex ? "Replace (supports $1, $2…)" : "Replace"}
                className="flex-1 min-w-[140px] px-2 py-1 bg-panel-surface border border-panel-border rounded text-panel-text font-mono"
                spellCheck={false}
              />
              <button type="button" onClick={() => replaceCurrent("none")} disabled={!matches.length} className="px-2 py-1 border border-panel-border rounded text-panel-muted hover:text-panel-text disabled:opacity-40" title="Replace the current match">Replace</button>
              <button type="button" onClick={() => replaceCurrent("prev")} disabled={!matches.length} className="px-2 py-1 border border-panel-border rounded text-panel-muted hover:text-panel-text disabled:opacity-40" title="Replace then jump to previous match">Repl ◀</button>
              <button type="button" onClick={() => replaceCurrent("next")} disabled={!matches.length} className="px-2 py-1 border border-panel-border rounded text-panel-muted hover:text-panel-text disabled:opacity-40" title="Replace then jump to next match">Repl ▶</button>
              <button type="button" onClick={replaceAll} disabled={!matches.length} className="px-2 py-1 bg-blue-600 hover:bg-blue-700 text-white rounded disabled:opacity-40" title="Replace every match">Replace all</button>
            </div>
          )}
        </div>
      )}

      <textarea
        ref={taRef}
        className={className}
        rows={rows}
        placeholder={placeholder}
        spellCheck={spellCheck}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={onKey}
      />
    </div>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Services JSON — Import (additive) + Edit (in-place) modals
//
// Both modals consume the same shape downloadServicesExport emits, so the
// canonical operator workflow is "download → tweak in editor → re-upload":
//   - Import JSON adds the rows as fresh services. IDs in the manifest
//     are ignored — fresh ObjectIDs get minted, fresh ports allocated,
//     fresh vhosts written. Cheaper UX than running the wizard 8 times.
//   - Edit JSON expects every row to carry the id of an existing service
//     on this project; rows are patched in place via the same path the
//     per-service Edit modal uses. Saves manual click-through when the
//     same env-var update needs to land on a dozen services at once.
//
// Backend (POST /services/import-json, PUT /services/bulk-edit) returns a
// per-row outcome list — we render it as a summary card with collapsible
// row details so partial failures (one bad domain, the rest succeeded)
// don't hide behind a flat success/error toast.
// ──────────────────────────────────────────────────────────────────────────

type ServicesJSONRowResult = {
  row_number?: number;
  service_id?: string;
  name?: string;
  role?: string;
  framework?: string;
  primary_domain?: string;
  port?: number;
  final_port?: number;
  success: boolean;
  error?: string;
  missing_env_keys?: string[];
};
type ServicesJSONResponse = {
  format?: string;
  total_rows?: number;
  successes?: number;
  failures?: number;
  items?: ServicesJSONRowResult[];
};

function ServicesJSONResultCard({ resp }: { resp: ServicesJSONResponse }) {
  const succ = resp.successes || 0;
  const fail = resp.failures || 0;
  const total = resp.total_rows || (resp.items?.length ?? 0);
  const allGreen = fail === 0 && succ > 0;
  return (
    <div className={`px-3 py-2 rounded border text-[12px] ${allGreen ? "border-green-500/30 bg-green-500/10 text-green-300" : "border-amber-500/30 bg-amber-500/10 text-amber-300"}`}>
      <div className="font-medium mb-1">
        {allGreen ? "All rows applied" : "Partial result"} · {succ}/{total} succeeded{fail > 0 ? `, ${fail} failed` : ""}
      </div>
      <div className="space-y-1 max-h-64 overflow-y-auto">
        {(resp.items || []).map((it, i) => (
          <div key={i} className={`flex items-start gap-2 text-[11px] ${it.success ? "text-green-300/90" : "text-red-300"}`}>
            <span>{it.success ? "✓" : "✗"}</span>
            <div className="flex-1">
              <code className="text-panel-text">{it.name || it.service_id || `row ${it.row_number ?? i + 1}`}</code>
              {it.primary_domain && <span className="text-panel-muted"> · {it.primary_domain}</span>}
              {(it.final_port || it.port) ? <span className="text-panel-muted"> · :{it.final_port || it.port}</span> : null}
              {!it.success && it.error && <div className="text-red-300/90 mt-0.5 font-mono break-words">{it.error}</div>}
              {it.success && (it.missing_env_keys?.length ?? 0) > 0 && (
                <div className="text-amber-300/90 mt-0.5">needs env vars: {it.missing_env_keys!.join(", ")}</div>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function ImportServicesJSONModal({
  projectId, projectName, onClose, onImported,
}: { projectId: string; projectName: string; onClose: () => void; onImported: () => void }) {
  const [jsonText, setJsonText] = useState<string>("");
  const [parseError, setParseError] = useState<string>("");
  const [parsedCount, setParsedCount] = useState<number>(0);
  const [importing, setImporting] = useState(false);
  const [result, setResult] = useState<ServicesJSONResponse | null>(null);
  const [dragOver, setDragOver] = useState(false);

  function ingest(text: string) {
    setJsonText(text);
    setParseError("");
    setResult(null);
    if (!text.trim()) { setParsedCount(0); return; }
    try {
      const parsed = JSON.parse(text);
      // Accept either { services: [...] } envelope OR bare array — matches
      // the backend parseServicesJSONBody contract.
      const arr = Array.isArray(parsed) ? parsed : Array.isArray(parsed?.services) ? parsed.services : null;
      if (!arr) throw new Error("Expected a 'services' array or a bare array.");
      if (arr.length === 0) throw new Error("Services array is empty.");
      setParsedCount(arr.length);
    } catch (e: any) {
      setParseError(e?.message || "Invalid JSON");
      setParsedCount(0);
    }
  }

  async function onFile(file: File) { ingest(await file.text()); }

  async function submit() {
    if (!parsedCount) return;
    setImporting(true);
    try {
      const body = JSON.parse(jsonText);
      const res = await api.post<{ data: ServicesJSONResponse }>(`/projects/${projectId}/services/import-json`, body);
      const data = res?.data?.data ?? (res?.data as any);
      setResult(data);
      if ((data?.successes || 0) > 0) onImported();
      if ((data?.failures || 0) === 0) toast.success(`Imported ${data?.successes} service${data?.successes === 1 ? "" : "s"}`);
      else toast.error(`Imported ${data?.successes || 0} · ${data?.failures || 0} failed`);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || e?.message || "Import failed");
    } finally {
      setImporting(false);
    }
  }

  return (
    <Modal isOpen onClose={onClose} title={`Import services into "${projectName}"`} size="lg">
      <div className="space-y-3">
        {!result && (
          <>
            <div
              onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
              onDragLeave={() => setDragOver(false)}
              onDrop={(e) => { e.preventDefault(); setDragOver(false); const f = e.dataTransfer.files?.[0]; if (f) onFile(f); }}
              className={"border-2 border-dashed rounded-lg p-4 text-center transition-colors " + (dragOver ? "border-blue-500 bg-blue-500/5" : "border-panel-border")}
            >
              <FileJson size={22} className="text-blue-400 mx-auto mb-1" />
              <p className="text-xs text-panel-muted mb-2">Drop a <code className="text-blue-300">.services.json</code> file or paste below</p>
              <label className="inline-block px-3 py-1 text-[11px] bg-panel-bg border border-panel-border rounded text-panel-text cursor-pointer hover:border-blue-500/50">
                Choose file…
                <input type="file" accept=".json,application/json" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) onFile(f); }} />
              </label>
            </div>
            <FindReplaceTextarea
              className={inputCls + " font-mono text-[11px]"}
              rows={10}
              placeholder='{"services": [{"name":"api","role":"backend","primary_domain":"api.example.com"}]}'
              value={jsonText}
              onChange={ingest}
              spellCheck={false}
            />
            {parseError && (
              <div className="px-3 py-2 rounded border border-red-500/30 bg-red-500/10 text-[12px] text-red-300">{parseError}</div>
            )}
            {parsedCount > 0 && !parseError && (
              <div className="text-[12px] text-panel-muted">Ready to import <span className="text-panel-text font-medium">{parsedCount}</span> service{parsedCount === 1 ? "" : "s"}. Existing services on this project are untouched.</div>
            )}
          </>
        )}

        {result && <ServicesJSONResultCard resp={result} />}

        <div className="flex justify-end gap-2 pt-1">
          <button onClick={onClose} className="px-4 py-2 text-sm text-panel-muted border border-panel-border rounded-lg">{result ? "Close" : "Cancel"}</button>
          {!result && (
            <button onClick={submit} disabled={!parsedCount || importing} className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50 inline-flex items-center gap-2">
              {importing && <RefreshCw size={14} className="animate-spin" />}
              {importing ? "Importing…" : `Import ${parsedCount || ""} service${parsedCount === 1 ? "" : "s"}`}
            </button>
          )}
        </div>
      </div>
    </Modal>
  );
}

function EditServicesJSONModal({
  projectId, projectName, services, onClose, onSaved,
}: { projectId: string; projectName: string; services: ProjectService[]; onClose: () => void; onSaved: () => void }) {
  // Seed the editor with a portable subset of the current services so
  // the operator sees what they're editing instead of an empty textbox.
  // Mirrors the backend ExportServices shape (id is preserved on edit;
  // host paths + runtime state stripped) so a paste from the
  // downloadable export Just Works.
  const initial = useMemo(() => {
    const portable = services.map((s) => ({
      id: s.id,
      name: s.name,
      role: s.role,
      framework: s.framework || undefined,
      git_subpath: s.git_subpath || undefined,
      git_branch: s.git_branch || undefined,
      path_prefix: s.path_prefix || undefined,
      primary_domain: s.primary_domain,
      alias_domains: s.alias_domains && s.alias_domains.length > 0 ? s.alias_domains : undefined,
      install_cmd: s.install_cmd || undefined,
      build_cmd: s.build_cmd || undefined,
      start_cmd: s.start_cmd || undefined,
      runtime_version: s.runtime_version || undefined,
      port: s.port || undefined,
      env_vars: s.env_vars && Object.keys(s.env_vars).length > 0 ? s.env_vars : undefined,
    }));
    return JSON.stringify({ services: portable }, null, 2);
  }, [services]);

  const [jsonText, setJsonText] = useState<string>(initial);
  const [parseError, setParseError] = useState<string>("");
  const [saving, setSaving] = useState(false);
  const [result, setResult] = useState<ServicesJSONResponse | null>(null);

  function validate(text: string): { ok: boolean; count: number } {
    setParseError("");
    try {
      const parsed = JSON.parse(text);
      const arr = Array.isArray(parsed) ? parsed : Array.isArray(parsed?.services) ? parsed.services : null;
      if (!arr) throw new Error("Expected a 'services' array or a bare array.");
      const missingId = arr.findIndex((r: any) => !r?.id);
      if (missingId >= 0) throw new Error(`Row ${missingId + 1} is missing an 'id'. Every entry must carry its existing service id on this endpoint — use Import JSON to add new services.`);
      return { ok: true, count: arr.length };
    } catch (e: any) {
      setParseError(e?.message || "Invalid JSON");
      return { ok: false, count: 0 };
    }
  }

  async function submit() {
    const v = validate(jsonText);
    if (!v.ok) return;
    setSaving(true);
    try {
      const body = JSON.parse(jsonText);
      const res = await api.put<{ data: ServicesJSONResponse }>(`/projects/${projectId}/services/bulk-edit`, body);
      const data = res?.data?.data ?? (res?.data as any);
      setResult(data);
      if ((data?.successes || 0) > 0) onSaved();
      if ((data?.failures || 0) === 0) toast.success(`Updated ${data?.successes} service${data?.successes === 1 ? "" : "s"}`);
      else toast.error(`Updated ${data?.successes || 0} · ${data?.failures || 0} failed`);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || e?.message || "Save failed");
    } finally {
      setSaving(false);
    }
  }

  function reset() { setJsonText(initial); setParseError(""); setResult(null); }

  return (
    <Modal isOpen onClose={onClose} title={`Edit services on "${projectName}" as JSON`} size="lg">
      <div className="space-y-3">
        {!result && (
          <>
            <div className="text-[11px] text-panel-muted">
              <FileJson size={11} className="inline mr-1 text-blue-400" />
              Editing <span className="text-panel-text font-medium">{services.length}</span> service{services.length === 1 ? "" : "s"}.
              Every entry's <code className="text-panel-text">id</code> must match an existing row on this project; omitted fields leave the existing value unchanged.
              Changing <code className="text-panel-text">primary_domain</code> rewrites the nginx vhost and reissues SSL — slower than other edits.
            </div>
            <FindReplaceTextarea
              className={inputCls + " font-mono text-[11px]"}
              rows={20}
              value={jsonText}
              onChange={(v) => { setJsonText(v); setParseError(""); }}
              spellCheck={false}
            />
            {parseError && (
              <div className="px-3 py-2 rounded border border-red-500/30 bg-red-500/10 text-[12px] text-red-300">{parseError}</div>
            )}
          </>
        )}

        {result && <ServicesJSONResultCard resp={result} />}

        <div className="flex justify-between items-center gap-2 pt-1">
          {!result && (
            <button onClick={reset} className="text-[11px] text-panel-muted hover:text-panel-text inline-flex items-center gap-1" title="Discard edits and reload from the current services state">
              <RotateCw size={11} /> Reset
            </button>
          )}
          <div className="flex gap-2 ml-auto">
            <button onClick={onClose} className="px-4 py-2 text-sm text-panel-muted border border-panel-border rounded-lg">{result ? "Close" : "Cancel"}</button>
            {!result && (
              <button onClick={submit} disabled={saving} className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50 inline-flex items-center gap-2">
                {saving && <RefreshCw size={14} className="animate-spin" />}
                {saving ? "Saving…" : "Apply changes"}
              </button>
            )}
          </div>
        </div>
      </div>
    </Modal>
  );
}

// ──────────────────────────────────────────────────────────────────────────
// Webhook "last delivery" badge — live confirmation the wiring works
// ──────────────────────────────────────────────────────────────────────────

function LastWebhookBadge({
  at,
  event,
  errorMsg,
  errorAt,
}: {
  at: string | null;
  event: string;
  errorMsg?: string;
  errorAt?: string | null;
}) {
  // 3.1.73 — error state takes precedence when the most recent failure is
  // newer than the most recent success (the common case: operator pasted
  // the secret wrong on first setup, so there's never been a success).
  // If a success arrived AFTER the last error, that means the operator
  // already fixed it — show the green state, not a stale red one.
  const okTs = at ? new Date(at).getTime() : 0;
  const errTs = errorAt ? new Date(errorAt).getTime() : 0;
  if (errorMsg && errTs > okTs) {
    const ageMin = Math.round((Date.now() - errTs) / 60000);
    const ageLabel = ageMin < 1 ? "just now" : ageMin < 60 ? `${ageMin}m ago` : ageMin < 1440 ? `${Math.round(ageMin / 60)}h ago` : new Date(errTs).toLocaleDateString();
    return (
      <span
        className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] bg-red-500/10 text-red-400 border border-red-500/20"
        title={`${errorMsg} · ${new Date(errTs).toLocaleString()}`}
      >
        <AlertTriangle size={11} /> Delivery failed · {ageLabel}
      </span>
    );
  }
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
  const [gitRepoURL, setGitRepoURL] = useState(project.git_repo_url || "");
  const [gitBranch, setGitBranch] = useState(project.git_branch || "main");
  const [autoDeploy, setAutoDeploy] = useState(project.auto_deploy);
  const [saving, setSaving] = useState(false);

  async function save() {
    setSaving(true);
    try {
      const payload: Record<string, unknown> = { name, description, auto_deploy: autoDeploy };
      // Only send git_repo_url when it actually changed — sending the
      // same value triggers a remote-URL rewrite + DB UpdateMany on the
      // backend even when it's a no-op.
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

function EditServiceModal({ projectId, svc, presets, runtimes, availableDomains, serverIP, onClose, onSaved }: {
  projectId: string;
  svc: ProjectService;
  presets: Record<string, Preset>;
  runtimes: Record<string, RuntimeVersionInfo[]>;
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
  const [runtimeVersion, setRuntimeVersion] = useState(svc.runtime_version || "");
  const [port, setPort] = useState(svc.port || 0);
  const [envVars, setEnvVars] = useState<Record<string, string>>(svc.env_vars || {});
  const [envKey, setEnvKey] = useState("");
  const [envVal, setEnvVal] = useState("");
  // Domain management. Same UI as the Add Service modal: primary picks
  // from the registered-domain dropdown, aliases use the chip+input
  // pattern. Local-only until Save — the backend reconciles vhost +
  // cert in a single PUT (rename + alias replace in one round trip).
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
        runtime_version: runtimeVersion,
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
            {/* Branch is project-level (3.1.27). Show it read-only here
                so the operator can see what they're deploying from
                without being able to set it per-service. To change,
                use the Edit Project modal. */}
            <LabelWithHint hint="Set on the project, not per service. Use the Edit Project modal to change.">Branch (project-wide)</LabelWithHint>
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
        <RuntimeVersionPicker
          runtimeKey={presetToRuntimeKey(presets[framework]?.app_type, svc.role)}
          value={runtimeVersion}
          runtimes={runtimes}
          onChange={setRuntimeVersion}
        />
        {/* Domains — mirrors the Add Service modal so add and edit are
            visually identical. The backend's PUT /services/<id> accepts
            primary_domain + alias_domains, so a save here can rename
            the vhost AND replace the alias list in one round trip. */}
        <div>
          <LabelWithHint required hint="Pick a domain registered in the WHM Domains page. Renaming this triggers a vhost rename + SAN cert reissue under the new --cert-name; the old vhost file is removed automatically.">Primary domain</LabelWithHint>
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

  // Poll the latest deployment record while this service is mid-deploy. The
  // panel renders a step-by-step timeline + progress bar inline below the
  // service row so the operator sees clone → install → build → restart →
  // health-check unfold in real time without opening the logs modal.
  type DepStep = { name: string; status: string; details?: string; error?: string; started_at?: string; completed_at?: string; };
  type Deployment = { id: string; status: string; progress: number; trigger: string; commit_sha?: string; started_at: string; finished_at?: string; error_msg?: string; steps?: DepStep[]; };
  const [dep, setDep] = useState<Deployment | null>(null);
  const [showDep, setShowDep] = useState(false);
  // Click-to-view modal for the error badge — fetches the latest deployment
  // on demand so the operator gets a focused error view without scrolling
  // through the deploy timeline.
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
      // 404 is fine — no deployment yet to show.
    } finally {
      setErrorModalLoading(false);
    }
  }
  // Auto-show whenever the service is transitioning so operators don't have
  // to expand it manually after clicking Deploy. Also auto-show on error so
  // the failed step + stderr is surfaced immediately.
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
      } catch {
        // No deployment yet — leave dep null. Endpoint 404s before the
        // first deploy is enqueued.
      }
    };
    fetchOnce();
    // Terminal = backend wrote finished_at. The backend uses status="running"
    // for BOTH "in progress" and "completed successfully" (the service-status
    // field is overloaded), so we can't rely on status alone — finished_at
    // is the only reliable signal that the deploy is done.
    const terminal = !!(dep && (
      dep.finished_at ||
      dep.status === "success" ||
      dep.status === "error" ||
      dep.status === "failed"
    ));
    if (terminal) {
      // One more fetch after 1s to ensure we caught the final step transition.
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
                ObjectID hex used by /api/v1/external/deploy/projects/
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
              <StatusBadge status={(svc.status === "running" || svc.status === "success") ? "active" : svc.status === "deploying" ? "warning" : svc.status === "stopped" ? "inactive" : svc.status === "needs_env_vars" ? "warning" : svc.status === "error" || svc.status === "failed" ? "inactive" : "pending"} />
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
          <button onClick={onDeploy} className="p-1.5 text-panel-muted hover:text-blue-400" title="Redeploy (install + build + restart on existing source — use project-level Pull to fetch new commits first)"><Rocket size={14} /></button>
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
      {/* Env-vars warning banner — shows when the deploy completed BUT the
           operator left .env.example keys blank. The systemd unit + nginx
           vhost are in place; we just refused to start the service because
           it would crash-loop on the missing vars. Click "Add env vars" to
           open the Edit modal pre-focused on the env section. */}
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

      {/* Error summary banner — shows the most-recent failure inline so the
           operator doesn't have to expand "Show deploy progress" or open the
           logs modal to know WHY the service is in error state. The full
           timeline + raw stderr is still rendered below for deep diagnosis. */}
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
                  <div className="mt-2 flex items-center gap-2 flex-wrap">
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
                    {/* 3.1.80 — one-click copy of the error summary so the
                        operator can paste into a chat / issue tracker
                        without scrolling-and-selecting the <pre> by hand. */}
                    <CopyTextButton value={summary} />
                  </div>
                </div>
              </div>
            </div>
          );
        })()
      )}

      {/* Deploy progress timeline — shown automatically while mid-deploy,
           also collapsible after the fact via the button on the right. */}
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
          {/* Progress bar */}
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
          {/* Per-step list */}
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
            <div className="rounded border border-red-500/30 bg-red-500/10 p-2 space-y-1">
              <div className="flex items-center justify-between">
                <span className="text-[10px] uppercase tracking-wider text-red-400/70">Error</span>
                <CopyTextButton value={dep.error_msg} />
              </div>
              <pre className="text-[10px] text-red-300 whitespace-pre-wrap break-all max-h-24 overflow-auto">
                {dep.error_msg}
              </pre>
            </div>
          )}
        </div>
      )}
      {/* Domains */}
      <div className="mt-2 space-y-2">
        <div className="flex items-center flex-wrap gap-1 text-[11px]">
          <Shield size={11} className="text-green-400" />
          <code className="px-1.5 py-0.5 bg-panel-bg border border-panel-border rounded text-panel-text">{svc.primary_domain}</code>
          {(svc.alias_domains || []).map((a) => (
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
                    if (d === svc.primary_domain || (svc.alias_domains || []).includes(d)) { toast.error(`"${d}" is already in the list`); return; }
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
                  <div className="flex items-center justify-between mb-1">
                    <div className="text-panel-muted/70 uppercase text-[10px] tracking-wide">Error</div>
                    <CopyTextButton value={summary} />
                  </div>
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

// Add-service inside the detail drawer — same card the wizard uses.
// projectRepoURL is inherited from the project's existing services so the
// new service automatically clones from the same repo (each service still
// gets its own install_dir + git pull because subpath/branch can differ).
function AddServiceModal({
  projectId, projectRepoURL, presets, runtimes, serverIP, availableDomains, onClose, onAdded,
}: {
  projectId: string;
  projectRepoURL: string;
  presets: Record<string, Preset>;
  runtimes: Record<string, RuntimeVersionInfo[]>;
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
    // git_repo_url is force-set from projectRepoURL — operator can't override.
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

  // The vendor that owns THIS project — the caller already passes
  // an availableDomains list pre-filtered to this vendor's domains
  // (see the AddServiceModal mount in ProjectModal). Knowing the
  // vendor's username here lets us render a helpful "no domains
  // owned by <vendor>" hint instead of a silently empty dropdown.
  const projectVendor = availableDomains.length > 0 ? availableDomains[0].user : "";
  return (
    <Modal isOpen onClose={onClose} title="Add Service" size="lg">
      <div className="space-y-4 max-h-[70vh] overflow-y-auto pr-1">
        <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 px-3 py-2 text-[11px] text-blue-200/80 flex items-center gap-2">
          <GitBranch size={12} className="shrink-0" />
          <span>Cloning from project's repo: <code className="text-panel-text">{projectRepoURL || "(no repo set)"}</code>. Pick a different branch / subpath below if needed.</span>
        </div>
        {availableDomains.length === 0 && (
          <div className="rounded-lg border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-[11px] text-amber-300">
            <strong className="text-amber-200">No domains available{projectVendor ? ` for ${projectVendor}` : ""}.</strong> Add a domain under this project's vendor account first (WHM → Domains → Add Domain), then come back here.
          </div>
        )}
        <ServiceCard
          idx={0}
          svc={svc}
          presets={presets}
          runtimes={runtimes}
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
