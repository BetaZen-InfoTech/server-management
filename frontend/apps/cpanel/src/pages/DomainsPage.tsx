import React, { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Card,
  Button,
  Table,
  Modal,
  StatusBadge,
  confirmAction,
  usePagination,
  BulkUploadDomainsModal,
} from "@serverpanel/ui";
import type { BulkUploadDomainsResponse } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Globe,
  Plus,
  Upload,
  Trash2,
  ExternalLink,
  Search,
  RefreshCw,
  Code,
  HardDrive,
  Calendar,
  FolderOpen,
  FileText,
  Activity,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Cloud,
  ShieldCheck,
  Copy,
  Lock,
} from "lucide-react";

// CfNsStatus mirrors backend services.NameserverStatus from the tenant-scoped
// /domains/{id}/cloudflare/nameserver-status + /verify endpoints.
interface CfNsStatus {
  domain: string;
  connected: boolean;
  zone_status?: string;
  cf_nameservers?: string[];
  current_nameservers?: string[];
  delegated?: boolean;
  state: "not_connected" | "nameserver_update_required" | "pending_activation" | "active" | "paused" | "error" | string;
  message?: string;
}

// Domain shape mirrors the backend model (Go struct field names land
// here as snake_case via the standard JSON tags). Keep this in sync
// with backend/internal/models/domain.go — anything missing here
// silently breaks a column render.
interface Domain {
  id: string;
  domain: string;
  user: string;
  php_version: string;
  disk_quota_mb: number;
  bandwidth_limit_gb: number;
  ssl_active: boolean;
  force_ssl: boolean;
  status: "active" | "suspended" | "pending";
  registrar?: string;
  registered_on?: string | null;
  expires_on?: string | null;
  auto_renew?: boolean;
  nameservers?: string[];
  resolved_ip?: string;
  // 3.1.102 — operator-chosen deployment tier ("prod" / "dev" / "test"
  // / "local"). Empty on legacy rows; UI treats empty as "prod".
  environment?: string;
  domain_type?: "primary" | "addon" | "subdomain" | "parked";
  ip_matches_server?: boolean;
  last_checked_at?: string;
  created_at: string;
}

interface PreflightCheck {
  name: string;
  ok: boolean;
  detail: string;
}

interface PreflightResult {
  domain: string;
  resolved_ips?: string[];
  server_ip?: string;
  ip_matches_server?: boolean;
  checks: PreflightCheck[];
}

const PHP_VERSIONS = ["7.4", "8.0", "8.1", "8.2", "8.3"];

// 3.1.102 — Deployment-tier (environment) options + badge metadata.
// Mirrors the WHM DomainsPage helper so both surfaces render identical
// labels + colours. Empty / unknown values fall back to "prod".
const ENVIRONMENTS = ["prod", "dev", "test", "local"] as const;

function envMeta(env?: string): { key: string; label: string; cls: string; title: string } {
  switch ((env || "prod").toLowerCase()) {
    case "dev":
      return { key: "dev", label: "Dev", cls: "bg-blue-500/10 text-blue-300 border-blue-500/30", title: "Development environment" };
    case "test":
      return { key: "test", label: "Test", cls: "bg-amber-500/10 text-amber-300 border-amber-500/30", title: "Testing / staging environment" };
    case "local":
      return { key: "local", label: "Local", cls: "bg-slate-500/15 text-slate-300 border-slate-500/30", title: "Local environment" };
    case "prod":
    default:
      return { key: "prod", label: "Prod", cls: "bg-emerald-500/10 text-emerald-300 border-emerald-500/30", title: "Production environment" };
  }
}

const PREFLIGHT_CHECK_LABELS: Record<string, string> = {
  whois: "Registrar lookup",
  dns_a: "Resolves to",
  dns_ns: "Nameservers",
  dns_mx: "Mail (MX)",
  domain_type: "Domain type",
  ip_match: "Server IP match",
  firewall: "Firewall ports",
};

// daysUntil returns days between now and an ISO date string. Negative
// for past dates ("expired 3 days ago"), -999999 sentinel for missing
// inputs so callers can treat them as "no info". Mirrors WHM helper.
function daysUntil(iso?: string | null): number {
  if (!iso) return -999999;
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return -999999;
  return Math.floor((t - Date.now()) / (24 * 60 * 60 * 1000));
}

function expiryBadgeClass(d: number): string {
  if (d === -999999) return "bg-panel-bg border-panel-border text-panel-muted";
  if (d < 0) return "bg-red-500/15 border-red-500/30 text-red-300";
  if (d <= 7) return "bg-red-500/10 border-red-500/30 text-red-300";
  if (d <= 30) return "bg-amber-500/10 border-amber-500/30 text-amber-300";
  return "bg-emerald-500/10 border-emerald-500/30 text-emerald-300";
}

function expiryLabel(iso?: string | null): string {
  const d = daysUntil(iso);
  if (d === -999999) return "—";
  if (d < 0) return `expired ${-d}d ago`;
  if (d === 0) return "expires today";
  if (d <= 30) return `expires in ${d}d`;
  return iso ? new Date(iso).toISOString().slice(0, 10) : "—";
}

const inputCls =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-brand-500";

export default function DomainsPage() {
  const navigate = useNavigate();
  const [domains, setDomains] = useState<Domain[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  // 3.1.102 — Environment filter. Empty = "All". Legacy rows with no
  // `environment` are treated as "prod" to match the badge fallback.
  const [environmentFilter, setEnvironmentFilter] = useState<string>("");

  // Add Domain
  const [showAdd, setShowAdd] = useState(false);
  const [showBulk, setShowBulk] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [addForm, setAddForm] = useState({ domain: "", type: "addon", environment: "prod", php_version: "8.2" });

  // Switch PHP modal
  const [phpTarget, setPhpTarget] = useState<Domain | null>(null);
  const [newPhp, setNewPhp] = useState("8.2");
  const [switchingPhp, setSwitchingPhp] = useState(false);

  // Edit Registration modal
  const [regTarget, setRegTarget] = useState<Domain | null>(null);
  const [regForm, setRegForm] = useState({
    registrar: "",
    registered_on: "",
    expires_on: "",
    auto_renew: false,
    nameservers: "",
  });
  const [savingReg, setSavingReg] = useState(false);

  // Re-check (preflight) modal
  const [recheckTarget, setRecheckTarget] = useState<Domain | null>(null);
  const [recheckLoading, setRecheckLoading] = useState(false);
  const [recheckResult, setRecheckResult] = useState<PreflightResult | null>(null);
  const [recheckingId, setRecheckingId] = useState<string | null>(null);

  // Selection state for the row-checkbox column. Same shape as the
  // WHM DomainsPage; backend tenant scoping inside FetchDomainsForExport
  // ensures a vendor can only export their own rows even if the UI
  // somehow sends `all=true`.
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [exporting, setExporting] = useState<"csv" | "xlsx" | null>(null);

  // Domain detail modal + its Cloudflare nameserver status (tenant-scoped).
  const [infoTarget, setInfoTarget] = useState<Domain | null>(null);
  const [cfNs, setCfNs] = useState<CfNsStatus | null>(null);
  const [cfNsLoading, setCfNsLoading] = useState(false);
  const [verifyingDomain, setVerifyingDomain] = useState(false);

  useEffect(() => {
    if (!infoTarget) { setCfNs(null); return; }
    let cancelled = false;
    setCfNs(null);
    setCfNsLoading(true);
    api
      .get(`/domains/${infoTarget.id}/cloudflare/nameserver-status`)
      .then((r) => { if (!cancelled) setCfNs(r.data?.data ?? r.data); })
      .catch(() => { if (!cancelled) setCfNs(null); })
      .finally(() => { if (!cancelled) setCfNsLoading(false); });
    return () => { cancelled = true; };
  }, [infoTarget]);

  function copyText(text: string, label = "Copied") {
    if (!text) return;
    const done = () => toast.success(label);
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(done).catch(() => toast.error("Copy failed"));
    } else {
      const ta = document.createElement("textarea");
      ta.value = text; ta.style.position = "fixed"; ta.style.opacity = "0";
      document.body.appendChild(ta); ta.select();
      try { document.execCommand("copy"); done(); } catch { toast.error("Copy failed"); }
      document.body.removeChild(ta);
    }
  }

  async function verifyDomainOnCloudflare(id: string) {
    setVerifyingDomain(true);
    try {
      const res = await api.post(`/domains/${id}/cloudflare/verify`);
      const st: CfNsStatus = res.data?.data ?? res.data;
      setCfNs(st);
      if (st.state === "active") toast.success("Domain is active on Cloudflare ✓");
      else if (st.state === "pending_activation") toast.success("Verification requested — Cloudflare is activating; check again shortly");
      else toast(st.message || `State: ${st.state}`);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Verify failed");
    } finally {
      setVerifyingDomain(false);
    }
  }

  const fetchDomains = async () => {
    setLoading(true);
    try {
      const res = await api.get("/domains", { params: { limit: 10000 } });
      setDomains(res.data.data || []);
      setSelectedIds(new Set());
    } catch {
      toast.error("Failed to load domains");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchDomains();
  }, []);

  const handleAddDomain = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!addForm.domain.trim()) {
      toast.error("Please enter a domain name");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/domains", {
        domain: addForm.domain.trim(),
        type: addForm.type,
        environment: addForm.environment,
        php_version: addForm.php_version,
      });
      toast.success("Domain added");
      setShowAdd(false);
      setAddForm({ domain: "", type: "addon", environment: "prod", php_version: "8.2" });
      fetchDomains();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || err?.response?.data?.message || "Failed to add domain");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async (id: string, domain: string) => {
    if (
      !(await confirmAction({
        title: "Remove?",
        description: `Are you sure you want to remove ${domain}? This deletes the vhost, files, and DNS zone.`,
        danger: true,
        confirmLabel: "Remove",
      }))
    )
      return;
    try {
      await api.delete(`/domains/${id}`, { data: { confirm: true } });
      toast.success(`Removed ${domain}`);
      setDomains((prev) => prev.filter((d) => d.id !== id));
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to remove domain");
    }
  };

  // Force-SSL toggle reuses the SSL service's per-domain endpoint.
  // Optimistically flip the switch, revert on failure.
  const handleToggleForceSSL = async (d: Domain) => {
    const enabling = !d.force_ssl;
    setDomains((prev) => prev.map((dom) => (dom.id === d.id ? { ...dom, force_ssl: enabling } : dom)));
    try {
      await api.post(`/ssl/${d.domain}/force-ssl`, { enable: enabling });
      toast.success(`Force HTTPS ${enabling ? "enabled" : "disabled"}`);
    } catch {
      setDomains((prev) => prev.map((dom) => (dom.id === d.id ? { ...dom, force_ssl: !enabling } : dom)));
      toast.error("Failed to toggle Force HTTPS");
    }
  };

  const openPhpSwitch = (d: Domain) => {
    setPhpTarget(d);
    setNewPhp(d.php_version || "8.2");
  };

  const handleSwitchPhp = async () => {
    if (!phpTarget) return;
    setSwitchingPhp(true);
    try {
      await api.patch(`/domains/${phpTarget.id}/php`, { php_version: newPhp });
      toast.success(`PHP switched to ${newPhp} for ${phpTarget.domain}`);
      setPhpTarget(null);
      fetchDomains();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to switch PHP");
    } finally {
      setSwitchingPhp(false);
    }
  };

  const openEditRegistration = (d: Domain) => {
    setRegTarget(d);
    setRegForm({
      registrar: d.registrar || "",
      registered_on: d.registered_on ? d.registered_on.slice(0, 10) : "",
      expires_on: d.expires_on ? d.expires_on.slice(0, 10) : "",
      auto_renew: !!d.auto_renew,
      nameservers: (d.nameservers || []).join(", "),
    });
  };

  const handleSaveRegistration = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!regTarget) return;
    setSavingReg(true);
    try {
      await api.patch(`/domains/${regTarget.id}/registration`, {
        registrar: regForm.registrar.trim(),
        registered_on: regForm.registered_on,
        expires_on: regForm.expires_on,
        auto_renew: regForm.auto_renew,
        nameservers: regForm.nameservers
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
      });
      toast.success("Registration updated");
      setRegTarget(null);
      fetchDomains();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update registration");
    } finally {
      setSavingReg(false);
    }
  };

  // Re-check connectivity — runs the same preflight the WHM page does.
  // Opens an inline modal instead of toasting because the result is
  // multi-line (DNS + IP + firewall) and worth keeping on screen.
  const openRecheck = async (d: Domain) => {
    setRecheckTarget(d);
    setRecheckResult(null);
    setRecheckLoading(true);
    setRecheckingId(d.id);
    try {
      const res = await api.post(`/domains/${d.id}/recheck`);
      setRecheckResult(res.data.data);
      // Refresh the row so the IP-mismatch badge updates if changed.
      fetchDomains();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Re-check failed");
      setRecheckTarget(null);
    } finally {
      setRecheckLoading(false);
      setRecheckingId(null);
    }
  };

  const openFileManager = (d: Domain) => {
    navigate(`/files?path=/home/${d.user}/domains/${d.domain}/public_html`);
  };

  const filtered = domains.filter((d) => {
    if (!d.domain.toLowerCase().includes(search.toLowerCase())) return false;
    if (environmentFilter && (d.environment || "prod").toLowerCase() !== environmentFilter) return false;
    return true;
  });
  // Per-environment counts shown next to each filter pill.
  const envCounts = useMemo(() => {
    const counts: Record<string, number> = { prod: 0, dev: 0, test: 0, local: 0 };
    for (const d of domains) {
      const k = envMeta(d.environment).key;
      counts[k] = (counts[k] || 0) + 1;
    }
    return counts;
  }, [domains]);
  const pg = usePagination("cpanel-domains");
  useEffect(() => {
    pg.setTotal(filtered.length);
    pg.setPage(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [search, environmentFilter, filtered.length]);
  const paged = filtered.slice((pg.page - 1) * pg.limit, pg.page * pg.limit);

  // Selection helpers — same shape as the WHM DomainsPage. Select All
  // toggles every row in the current filtered view (respects search).
  const toggleOne = (id: string) =>
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  const allFilteredIds = filtered.map((d) => d.id);
  const allSelected = allFilteredIds.length > 0 && allFilteredIds.every((id) => selectedIds.has(id));
  const someSelected = !allSelected && allFilteredIds.some((id) => selectedIds.has(id));
  const toggleAllVisible = () => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (allSelected) for (const id of allFilteredIds) next.delete(id);
      else for (const id of allFilteredIds) next.add(id);
      return next;
    });
  };

  const downloadExport = async (format: "csv" | "xlsx") => {
    setExporting(format);
    try {
      const params: Record<string, string> = { format };
      if (selectedIds.size > 0) params.ids = Array.from(selectedIds).join(",");
      else params.all = "true";
      const res = await api.get("/domains/export", { params, responseType: "blob" });
      const blob = res.data as Blob;
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      const cd = (res.headers as Record<string, string>)["content-disposition"] || "";
      const m = /filename=\"?([^\";]+)\"?/.exec(cd);
      a.download = m?.[1] || `domains-export.${format}`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      const count = selectedIds.size > 0 ? selectedIds.size : filtered.length;
      toast.success(`Exported ${count} domain${count === 1 ? "" : "s"} as ${format.toUpperCase()}`);
    } catch (e) {
      toast.error((e as { message?: string }).message || "Export failed");
    } finally {
      setExporting(null);
    }
  };

  const columns = [
    {
      key: "select",
      header: (
        <input
          type="checkbox"
          aria-label="Select all visible domains"
          checked={allSelected}
          ref={(el) => { if (el) el.indeterminate = someSelected; }}
          onChange={toggleAllVisible}
          className="h-4 w-4 cursor-pointer accent-brand-500"
        />
      ),
      render: (d: Domain) => (
        <input
          type="checkbox"
          aria-label={`Select ${d.domain}`}
          checked={selectedIds.has(d.id)}
          onChange={() => toggleOne(d.id)}
          onClick={(e) => e.stopPropagation()}
          className="h-4 w-4 cursor-pointer accent-brand-500"
        />
      ),
    },
    {
      key: "domain",
      header: "Domain",
      render: (d: Domain) => (
        <div className="flex items-center gap-2 min-w-0">
          <Globe size={14} className="text-brand-400 shrink-0" />
          <button
            type="button"
            onClick={() => setInfoTarget(d)}
            className="font-medium text-panel-text truncate hover:text-blue-400 text-left"
            title="View domain details"
          >
            {d.domain}
          </button>
          {d.ip_matches_server === false && d.last_checked_at && (
            <span
              className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-red-500/10 text-red-400 border border-red-500/20 shrink-0"
              title={`Resolves to ${d.resolved_ip || "—"} — DNS update required`}
            >
              IP MISMATCH
            </span>
          )}
        </div>
      ),
    },
    {
      key: "status",
      header: "Status",
      render: (d: Domain) => <StatusBadge status={d.status} />,
    },
    {
      key: "environment",
      header: "Env",
      render: (d: Domain) => {
        const m = envMeta(d.environment);
        return (
          <span className={`px-2 py-0.5 rounded text-[10px] font-medium border ${m.cls}`} title={m.title}>
            {m.label}
          </span>
        );
      },
    },
    {
      key: "ssl",
      header: "SSL",
      render: (d: Domain) => (
        <div className="flex items-center gap-2">
          <span className={d.ssl_active ? "text-green-400 text-sm" : "text-yellow-400 text-sm"}>
            {d.ssl_active ? "Active" : "Not Configured"}
          </span>
          {d.ssl_active && (
            <button
              onClick={() => handleToggleForceSSL(d)}
              className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${
                d.force_ssl ? "bg-green-500" : "bg-panel-border"
              }`}
              title={d.force_ssl ? "HTTPS forced — click to disable" : "Click to force HTTPS redirect"}
            >
              <span
                className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${
                  d.force_ssl ? "translate-x-4" : "translate-x-0.5"
                }`}
              />
            </button>
          )}
        </div>
      ),
    },
    {
      key: "php",
      header: "PHP",
      render: (d: Domain) => (
        <button
          onClick={() => openPhpSwitch(d)}
          className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-panel-bg border border-panel-border text-xs text-panel-muted hover:text-brand-400 hover:border-brand-500/30 transition-colors"
          title="Switch PHP version"
        >
          <Code size={10} />
          {d.php_version || "—"}
        </button>
      ),
    },
    {
      key: "disk",
      header: "Disk",
      render: (d: Domain) => (
        <span className="text-panel-muted text-sm flex items-center gap-1">
          <HardDrive size={12} />
          {d.disk_quota_mb >= 1024
            ? `${(d.disk_quota_mb / 1024).toFixed(0)} GB`
            : `${d.disk_quota_mb || 0} MB`}
        </span>
      ),
    },
    {
      key: "expires",
      header: "Expires",
      render: (d: Domain) => {
        const days = daysUntil(d.expires_on);
        return (
          <span
            className={`inline-flex items-center gap-1 px-2 py-0.5 rounded border text-xs font-medium ${expiryBadgeClass(days)}`}
            title={
              d.expires_on
                ? `Registrar: ${d.registrar || "—"} · ${d.auto_renew ? "Auto-renew: ON" : "Auto-renew: OFF"}`
                : "Registration not tracked — click the file icon to add it"
            }
          >
            <Calendar size={10} />
            {expiryLabel(d.expires_on)}
          </span>
        );
      },
    },
    {
      key: "created",
      header: "Created",
      render: (d: Domain) => (
        <span className="text-panel-muted text-xs">
          {d.created_at ? new Date(d.created_at).toLocaleDateString() : "—"}
        </span>
      ),
    },
    {
      key: "actions",
      header: "",
      render: (d: Domain) => (
        <div className="flex items-center gap-1 justify-end">
          <button
            onClick={() => openFileManager(d)}
            title="File Manager"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-brand-400 transition-colors"
          >
            <FolderOpen size={14} />
          </button>
          <button
            onClick={() => openEditRegistration(d)}
            title="Edit registration (registrar, expiry, nameservers)"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-cyan-400 transition-colors"
          >
            <FileText size={14} />
          </button>
          <button
            onClick={() => openRecheck(d)}
            disabled={recheckingId === d.id}
            title="Re-check connectivity (DNS, IP match, firewall)"
            className={`p-1.5 rounded hover:bg-panel-bg transition-colors disabled:opacity-50 ${
              d.ip_matches_server === false && d.last_checked_at
                ? "text-red-400 hover:text-red-300"
                : "text-panel-muted hover:text-brand-400"
            }`}
          >
            <Activity size={14} className={recheckingId === d.id ? "animate-pulse" : ""} />
          </button>
          <a
            href={`https://${d.domain}`}
            target="_blank"
            rel="noopener noreferrer"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-panel-text transition-colors"
            title="Visit site"
          >
            <ExternalLink size={14} />
          </a>
          <button
            onClick={() => handleDelete(d.id, d.domain)}
            title="Remove"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
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
          <h1 className="text-xl font-bold text-panel-text">My Domains</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage domains associated with your account
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="secondary" size="sm" onClick={fetchDomains}>
            <RefreshCw size={14} className={loading ? "animate-spin mr-1" : "mr-1"} />
            Refresh
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => downloadExport("csv")}
            disabled={exporting !== null}
            title={selectedIds.size > 0 ? `Export ${selectedIds.size} selected as CSV` : "Export all your domains as CSV"}
          >
            <FileText size={14} className="mr-1" />
            {exporting === "csv" ? "Exporting…" : selectedIds.size > 0 ? `Export ${selectedIds.size} (CSV)` : "Export CSV"}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => downloadExport("xlsx")}
            disabled={exporting !== null}
            title={selectedIds.size > 0 ? `Export ${selectedIds.size} selected as Excel` : "Export all your domains as Excel"}
          >
            <FileText size={14} className="mr-1" />
            {exporting === "xlsx" ? "Exporting…" : selectedIds.size > 0 ? `Export ${selectedIds.size} (Excel)` : "Export Excel"}
          </Button>
          <Button variant="secondary" size="sm" onClick={() => setShowBulk(true)}>
            <Upload size={14} className="mr-1" /> Bulk Upload
          </Button>
          <Button size="sm" onClick={() => setShowAdd(true)}>
            <Plus size={16} className="mr-1" /> Add Domain
          </Button>
        </div>
      </div>

      <Card>
        <div className="mb-4 flex flex-col sm:flex-row sm:items-center gap-3">
          <div className="relative max-w-xs">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search domains..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          {/* 3.1.102 — Environment filter pills. Default "All". */}
          <div className="flex items-center gap-1.5 flex-wrap">
            <span className="text-[10px] uppercase tracking-wide text-panel-muted/70 mr-0.5">Env</span>
            {[
              { key: "",      label: "All",   cnt: domains.length },
              { key: "prod",  label: "Prod",  cnt: envCounts.prod || 0 },
              { key: "dev",   label: "Dev",   cnt: envCounts.dev || 0 },
              { key: "test",  label: "Test",  cnt: envCounts.test || 0 },
              { key: "local", label: "Local", cnt: envCounts.local || 0 },
            ].map((opt) => {
              const active = environmentFilter === opt.key;
              const m = opt.key ? envMeta(opt.key) : null;
              return (
                <button
                  key={opt.key || "all"}
                  type="button"
                  onClick={() => setEnvironmentFilter(opt.key)}
                  className={
                    "px-2.5 py-1.5 text-xs rounded-lg border transition-colors inline-flex items-center gap-1.5 " +
                    (active
                      ? (m ? m.cls : "bg-brand-500/10 text-brand-300 border-brand-500/30")
                      : "bg-panel-bg border-panel-border text-panel-muted hover:text-panel-text")
                  }
                  title={m ? m.title : "Show all environments"}
                >
                  {opt.label}
                  <span className={"text-[10px] tabular-nums " + (active ? "opacity-80" : "text-panel-muted/70")}>
                    {opt.cnt}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
        <Table
          columns={columns}
          data={paged as any}
          loading={loading}
          emptyMessage="No domains yet. Add your first domain to get started."
          page={pg.page}
          limit={pg.limit}
          total={pg.total}
          onPageChange={pg.setPage}
          onLimitChange={pg.setLimit}
        />
      </Card>

      {/* Bulk Upload modal */}
      <BulkUploadDomainsModal
        isOpen={showBulk}
        onClose={() => setShowBulk(false)}
        scopeLabel="your account"
        onUploaded={fetchDomains}
        submit={async (file, opts) => {
          const fd = new FormData();
          fd.append("file", file);
          fd.append("issue_ssl", opts.issue_ssl ? "true" : "false");
          fd.append("force_ssl", opts.force_ssl ? "true" : "false");
          // Header omitted — axios + browser auto-set Content-Type
          // with the multipart boundary. See v3.1.41 fix.
          const { data } = await api.post<{ data: BulkUploadDomainsResponse }>(
            "/domains/bulk-upload",
            fd,
            // No client-side timeout — a large bulk can take minutes (SSL is
            // deferred to the background). nginx caps at 3600s upstream.
            { timeout: 0 },
          );
          return data.data;
        }}
        downloadTemplate={async (format) => {
          const res = await api.get("/domains/bulk-upload/template", {
            params: { format },
            responseType: "blob",
          });
          const blob = res.data as Blob;
          const url = URL.createObjectURL(blob);
          const a = document.createElement("a");
          a.href = url;
          const cd = (res.headers as Record<string, string>)["content-disposition"] || "";
          const m = /filename=\"?([^\";]+)\"?/.exec(cd);
          a.download = m?.[1] || `domains-bulk-upload-template.${format}`;
          document.body.appendChild(a);
          a.click();
          a.remove();
          URL.revokeObjectURL(url);
        }}
      />

      {/* Add Domain modal */}
      <Modal isOpen={showAdd} onClose={() => setShowAdd(false)} title="Add Domain">
        <form onSubmit={handleAddDomain} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">Domain Name *</label>
            <input
              type="text"
              required
              value={addForm.domain}
              onChange={(e) => setAddForm({ ...addForm, domain: e.target.value })}
              placeholder="example.com"
              className={inputCls}
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">Domain Type</label>
              <select
                value={addForm.type}
                onChange={(e) => setAddForm({ ...addForm, type: e.target.value })}
                className={inputCls}
              >
                <option value="addon">Addon Domain</option>
                <option value="subdomain">Subdomain</option>
                <option value="alias">Alias Domain</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">PHP Version</label>
              <select
                value={addForm.php_version}
                onChange={(e) => setAddForm({ ...addForm, php_version: e.target.value })}
                className={inputCls}
              >
                {PHP_VERSIONS.map((v) => (
                  <option key={v} value={v}>
                    PHP {v}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">Environment</label>
              <select
                value={addForm.environment}
                onChange={(e) => setAddForm({ ...addForm, environment: e.target.value })}
                className={inputCls}
              >
                {ENVIRONMENTS.map((env) => (
                  <option key={env} value={env}>
                    {envMeta(env).label}
                  </option>
                ))}
              </select>
              <p className="text-xs text-panel-muted mt-1">Deployment tier — defaults to Production.</p>
            </div>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button variant="secondary" type="button" onClick={() => setShowAdd(false)}>
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Add Domain
            </Button>
          </div>
        </form>
      </Modal>

      {/* Switch PHP modal */}
      <Modal
        isOpen={!!phpTarget}
        onClose={() => setPhpTarget(null)}
        title={phpTarget ? `Switch PHP — ${phpTarget.domain}` : "Switch PHP"}
        size="sm"
      >
        <div className="space-y-4">
          <p className="text-sm text-panel-muted">
            Currently <span className="text-panel-text font-mono">PHP {phpTarget?.php_version}</span>.
            Pick a new version. The pool restarts automatically.
          </p>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">New PHP Version</label>
            <select value={newPhp} onChange={(e) => setNewPhp(e.target.value)} className={inputCls}>
              {PHP_VERSIONS.map((v) => (
                <option key={v} value={v}>
                  PHP {v}
                </option>
              ))}
            </select>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button variant="secondary" type="button" onClick={() => setPhpTarget(null)}>
              Cancel
            </Button>
            <Button onClick={handleSwitchPhp} loading={switchingPhp}>
              Switch
            </Button>
          </div>
        </div>
      </Modal>

      {/* Edit Registration modal */}
      <Modal
        isOpen={!!regTarget}
        onClose={() => setRegTarget(null)}
        title={regTarget ? `Edit registration — ${regTarget.domain}` : "Edit registration"}
      >
        <form onSubmit={handleSaveRegistration} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">Registrar</label>
            <input
              type="text"
              value={regForm.registrar}
              onChange={(e) => setRegForm({ ...regForm, registrar: e.target.value })}
              placeholder="GoDaddy / Namecheap / …"
              className={inputCls}
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">Registered on</label>
              <input
                type="date"
                value={regForm.registered_on}
                onChange={(e) => setRegForm({ ...regForm, registered_on: e.target.value })}
                className={inputCls}
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">Expires on</label>
              <input
                type="date"
                value={regForm.expires_on}
                onChange={(e) => setRegForm({ ...regForm, expires_on: e.target.value })}
                className={inputCls}
              />
            </div>
          </div>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">Nameservers</label>
            <input
              type="text"
              value={regForm.nameservers}
              onChange={(e) => setRegForm({ ...regForm, nameservers: e.target.value })}
              placeholder="ns1.example.com, ns2.example.com"
              className={inputCls}
            />
            <p className="text-xs text-panel-muted mt-1">Comma-separated.</p>
          </div>
          <label className="flex items-center gap-2 text-sm text-panel-text">
            <input
              type="checkbox"
              checked={regForm.auto_renew}
              onChange={(e) => setRegForm({ ...regForm, auto_renew: e.target.checked })}
              className="rounded border-panel-border bg-panel-bg text-brand-600 focus:ring-brand-500"
            />
            Auto-renew enabled at registrar
          </label>
          <div className="flex justify-end gap-3 pt-2">
            <Button variant="secondary" type="button" onClick={() => setRegTarget(null)}>
              Cancel
            </Button>
            <Button type="submit" loading={savingReg}>
              Save
            </Button>
          </div>
        </form>
      </Modal>

      {/* Recheck modal — shows the preflight result inline so the
          vendor can see DNS / IP / firewall status without leaving
          the page. */}
      <Modal
        isOpen={!!recheckTarget}
        onClose={() => {
          setRecheckTarget(null);
          setRecheckResult(null);
        }}
        title={recheckTarget ? `Connectivity — ${recheckTarget.domain}` : "Connectivity"}
      >
        <div className="space-y-3">
          {recheckLoading && !recheckResult && (
            <div className="flex items-center gap-2 text-sm text-panel-muted">
              <RefreshCw size={14} className="animate-spin" />
              Resolving DNS, querying WHOIS, probing firewall ports…
            </div>
          )}
          {recheckResult && (
            <>
              <div className="space-y-1.5">
                {recheckResult.checks.map((c, i) => (
                  <div key={`${c.name}-${i}`} className="flex items-start gap-2 text-xs">
                    {c.ok ? (
                      <CheckCircle2 size={14} className="text-emerald-400 shrink-0 mt-0.5" />
                    ) : (
                      <XCircle size={14} className="text-red-400 shrink-0 mt-0.5" />
                    )}
                    <div className="flex-1 min-w-0">
                      <span className="text-panel-text font-medium">
                        {PREFLIGHT_CHECK_LABELS[c.name] || c.name}
                      </span>
                      {c.detail && <span className="text-panel-muted"> — {c.detail}</span>}
                    </div>
                  </div>
                ))}
              </div>
              {recheckResult.ip_matches_server === false && (
                <div className="flex items-start gap-2 px-3 py-2 rounded-lg border border-amber-500/30 bg-amber-500/10 text-amber-300 text-xs">
                  <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                  <span>
                    Domain does not point to this server (resolves to{" "}
                    <span className="font-mono">{(recheckResult.resolved_ips || []).join(", ") || "—"}</span>,
                    server is <span className="font-mono">{recheckResult.server_ip || "—"}</span>) — DNS update
                    required.
                  </span>
                </div>
              )}
            </>
          )}
          <div className="flex justify-end pt-2">
            <Button
              variant="secondary"
              onClick={() => {
                setRecheckTarget(null);
                setRecheckResult(null);
              }}
            >
              Close
            </Button>
          </div>
        </div>
      </Modal>

      <Modal
        isOpen={infoTarget !== null}
        onClose={() => setInfoTarget(null)}
        title={infoTarget ? infoTarget.domain : "Domain"}
      >
        {infoTarget && (() => {
          const d = infoTarget;
          const act = (fn: () => void) => { setInfoTarget(null); fn(); };
          const btn = "inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-panel-border text-sm text-panel-muted hover:text-panel-text hover:border-blue-500/40 transition-colors";
          const nsList = (cfNs?.current_nameservers?.length ? cfNs.current_nameservers : d.nameservers) || [];
          const rows: Array<[string, React.ReactNode]> = [
            ["Status", d.status],
            ["Environment", d.environment || "—"],
            ["SSL", d.ssl_active ? `Active · Force HTTPS: ${d.force_ssl ? "ON" : "OFF"}` : "None"],
            ["PHP", d.php_version],
            ["Domain type", d.domain_type || "—"],
            ["Resolved IP", d.resolved_ip || "—"],
            ["Registrar", d.registrar || "—"],
            ["Expires", d.expires_on || "—"],
            ["Nameservers", nsList.length ? nsList.join(", ") : "—"],
            ["Created", new Date(d.created_at).toLocaleString()],
          ];
          return (
            <div className="space-y-4">
              <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1 text-sm">
                {rows.map(([label, val]) => (
                  <div key={label} className="flex justify-between gap-3 border-b border-panel-border/40 py-1">
                    <dt className="text-panel-muted shrink-0">{label}</dt>
                    <dd className="text-panel-text text-right break-all">{val}</dd>
                  </div>
                ))}
              </dl>

              {(cfNsLoading || (cfNs && cfNs.connected)) && (
                <div className="rounded-lg border border-panel-border/60 bg-panel-bg/40 p-3">
                  <div className="flex items-center gap-2 text-xs uppercase tracking-wide text-panel-muted mb-2">
                    <Cloud size={14} className="text-blue-400" /> Cloudflare
                  </div>
                  {cfNsLoading ? (
                    <div className="text-sm text-panel-muted">Checking Cloudflare status…</div>
                  ) : cfNs && cfNs.connected ? (
                    <div className="space-y-2 text-sm">
                      <div>
                        <div className="flex items-center justify-between gap-3 mb-1">
                          <span className="text-panel-muted shrink-0">Cloudflare nameservers</span>
                          {(cfNs.cf_nameservers?.length ?? 0) > 1 && (
                            <button
                              className="inline-flex items-center gap-1 text-xs text-panel-muted hover:text-blue-400"
                              onClick={() => copyText((cfNs.cf_nameservers || []).join("\n"), "All nameservers copied")}
                              title="Copy all Cloudflare nameservers"
                            >
                              <Copy size={12} /> Copy all
                            </button>
                          )}
                        </div>
                        <div className="space-y-1">
                          {(cfNs.cf_nameservers?.length ? cfNs.cf_nameservers : ["—"]).map((ns, i) => (
                            <div key={`${ns}-${i}`} className="flex items-center justify-between gap-2 rounded bg-panel-bg/60 border border-panel-border/40 px-2 py-1">
                              <span className="font-mono text-xs break-all text-panel-text">{ns}</span>
                              {ns !== "—" && (
                                <button
                                  className="shrink-0 text-panel-muted hover:text-blue-400"
                                  onClick={() => copyText(ns, "Nameserver copied")}
                                  title="Copy this nameserver"
                                >
                                  <Copy size={13} />
                                </button>
                              )}
                            </div>
                          ))}
                        </div>
                      </div>
                      <div className="flex items-center justify-between gap-3 pt-1">
                        <span className="text-panel-muted shrink-0">Status</span>
                        {cfNs.state === "active" ? (
                          <span className="text-green-400 inline-flex items-center gap-1">
                            <CheckCircle2 size={14} /> Active on Cloudflare
                          </span>
                        ) : cfNs.delegated ? (
                          <button
                            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-blue-500/40 text-sm text-blue-300 hover:bg-blue-500/10 transition-colors disabled:opacity-50"
                            disabled={verifyingDomain}
                            onClick={() => verifyDomainOnCloudflare(d.id)}
                          >
                            <ShieldCheck size={14} /> {verifyingDomain ? "Verifying…" : "Verify Domain"}
                          </button>
                        ) : (
                          <span className="text-amber-400 text-xs text-right">
                            Point your registrar's nameservers to the Cloudflare ones above, then Verify.
                          </span>
                        )}
                      </div>
                    </div>
                  ) : null}
                </div>
              )}

              <div>
                <div className="text-xs uppercase tracking-wide text-panel-muted mb-2">Actions</div>
                <div className="flex flex-wrap gap-2">
                  <button className={btn} onClick={() => act(() => openFileManager(d))}>
                    <FolderOpen size={14} /> File Manager
                  </button>
                  <button className={btn} onClick={() => act(() => openEditRegistration(d))}>
                    <FileText size={14} /> Edit Registration
                  </button>
                  <button className={btn} onClick={() => act(() => openPhpSwitch(d))}>
                    <Code size={14} /> Switch PHP
                  </button>
                  {d.ssl_active && (
                    <button className={btn} onClick={() => act(() => handleToggleForceSSL(d))}>
                      <Lock size={14} /> {d.force_ssl ? "Disable Force HTTPS" : "Force HTTPS"}
                    </button>
                  )}
                  <button className={btn} onClick={() => act(() => openRecheck(d))}>
                    <Activity size={14} /> Re-check
                  </button>
                  <a
                    className={btn}
                    href={`https://${d.domain}`}
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={() => setInfoTarget(null)}
                  >
                    <ExternalLink size={14} /> Visit Site
                  </a>
                  <button
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-panel-border text-sm text-panel-muted hover:text-red-400 hover:border-red-500/40 transition-colors"
                    onClick={() => act(() => handleDelete(d.id, d.domain))}
                  >
                    <Trash2 size={14} /> Delete
                  </button>
                </div>
              </div>
            </div>
          );
        })()}
      </Modal>
    </div>
  );
}
