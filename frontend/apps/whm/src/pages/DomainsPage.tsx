import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal, SearchableSelect, confirmAction, usePagination, BulkUploadDomainsModal } from "@serverpanel/ui";
import type { BulkUploadDomainsResponse } from "@serverpanel/ui";
import { BulkDeleteDomainsModal } from "@/components/BulkDeleteDomainsModal";
import type { BulkDeleteRequestResult, BulkDeleteConfirmResult } from "@/components/BulkDeleteDomainsModal";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { useNavigate } from "react-router-dom";
import { useAuthStore } from "@/store/auth";
import {
  Globe, Plus, RefreshCw, Search, Trash2, ExternalLink,
  PauseCircle, PlayCircle, Code, HardDrive, Users, FolderOpen,
  Clock, Rocket, Eye, User, Calendar, FileText, ChevronDown, ChevronUp,
  Activity, CheckCircle2, XCircle, AlertTriangle, Upload,
} from "lucide-react";

interface Domain {
  id: string;
  domain: string;
  user: string;
  php_version: string;
  disk_quota_mb: number;
  bandwidth_limit_gb: number;
  max_databases: number;
  max_email_accounts: number;
  max_subdomains: number;
  max_apps: number;
  ssl_active: boolean;
  force_ssl: boolean;
  status: "active" | "suspended" | "pending";
  coming_soon?: boolean;
  maintenance_mode?: boolean;
  // Registration / whois tracking — every field optional because many
  // domains are tracked by the registrar externally; leaving them
  // blank just means the row doesn't show up in the expiry widget.
  registrar?: string;
  registered_on?: string | null;
  expires_on?: string | null;
  auto_renew?: boolean;
  nameservers?: string[];
  whois_synced_at?: string | null;
  // Preflight / connectivity tracking — populated by /domains/preflight
  // and /domains/:id/recheck. Optional because legacy rows created
  // before the preflight feature won't have these set.
  resolved_ip?: string;
  domain_type?: "primary" | "addon" | "subdomain" | "parked";
  ip_matches_server?: boolean;
  last_checked_at?: string;
  created_at: string;
}

// PreflightCheck mirrors one entry from the backend's `checks` array.
// Each named check is rendered as a row with a green/red icon.
interface PreflightCheck {
  name: string;
  ok: boolean;
  detail: string;
}

// PreflightResult is the full /domains/preflight response payload.
// Most fields are also surfaced individually so callers can render
// summary rows without re-walking the checks array.
interface PreflightResult {
  domain: string;
  registrar?: string;
  registered_on?: string | null;
  expires_on?: string | null;
  nameservers?: string[];
  resolved_ips?: string[];
  mx_records?: string[];
  domain_type?: "primary" | "addon" | "subdomain" | "parked";
  parent_domain?: string;
  server_ip?: string;
  ip_matches_server?: boolean;
  firewall_open_ports?: number[];
  firewall_ok?: boolean;
  checks: PreflightCheck[];
  checked_at?: string;
}

// Human-readable labels for the named checks coming back from the
// backend. Falls back to the raw key if the backend ever adds a new
// check name we don't know about yet.
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
// for past dates ("expired 3 days ago"), NaN sentinel (-999999) for
// unparseable / missing inputs so callers can treat them as "no info".
function daysUntil(iso?: string | null): number {
  if (!iso) return -999999;
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return -999999;
  return Math.floor((t - Date.now()) / (24 * 60 * 60 * 1000));
}

// expiryBadgeClass picks a colour based on how close the expiry is so
// ops can scan the list and spot the urgent ones. Same colour
// palette is reused by the dashboard widget.
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

interface UserOption {
  id: string;
  username: string;
  name: string;
  role: string;
}

const PHP_VERSIONS = ["7.4", "8.0", "8.1", "8.2", "8.3"];

export default function DomainsPage() {
  const navigate = useNavigate();
  const authUser = useAuthStore((s) => s.user);
  // Only the platform operator (vendor_owner) may create domains under
  // someone else's account — they get the "Select a vendor..." dropdown.
  // vendor_admin IS a vendor (the tenant themselves), so they should get
  // their own username locked into the field with no way to change it;
  // allowing them to pick from a dropdown lets one tenant create domains
  // under another tenant's account, which is a cross-tenant escalation.
  const isAdmin = authUser?.role === "vendor_owner";
  const [domains, setDomains] = useState<Domain[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [usersList, setUsersList] = useState<UserOption[]>([]);

  // Add domain modal
  const [showAddModal, setShowAddModal] = useState(false);
  const [showBulkModal, setShowBulkModal] = useState(false);
  const [showBulkDeleteModal, setShowBulkDeleteModal] = useState(false);
  // Selection state for the row-checkbox column. Stored as a Set so
  // toggle is O(1); cleared whenever the underlying list is refetched
  // (after an add / delete / bulk upload) so a selection from the
  // previous fetch can't reference now-stale ids.
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [exporting, setExporting] = useState<"csv" | "xlsx" | null>(null);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({
    domain: "",
    user: isAdmin ? "" : (authUser?.username || ""),
    php_version: "8.2",
    disk_quota_mb: 5120,
    bandwidth_limit_gb: 100,
    max_databases: 10,
    max_email_accounts: 50,
    max_subdomains: 20,
    max_apps: 5,
    // Registration — optional on create, editable later via the
    // "Edit registration" row action.
    registrar: "",
    registered_on: "",
    expires_on: "",
    auto_renew: false,
  });
  // Resource limits is now collapsed by default — they default from
  // the hosting package anyway, so surfacing them on every create
  // just meant operators tweaked numbers they didn't need to tweak.
  const [showAdvanced, setShowAdvanced] = useState(false);

  // Edit registration modal.
  const [regTarget, setRegTarget] = useState<Domain | null>(null);
  const [regForm, setRegForm] = useState({
    registrar: "",
    registered_on: "",
    expires_on: "",
    auto_renew: false,
    nameservers: "",
  });
  const [regSaving, setRegSaving] = useState(false);
  const [whoisLoading, setWhoisLoading] = useState(false);

  // Preflight panel state — driven by the Add Domain modal's onBlur.
  // `preflight` holds the most recent /domains/preflight response so
  // we can render its checks list; `preflightLoading` toggles a spinner
  // in the panel; `recheckingId` tracks per-row "Re-check connectivity"
  // clicks so we can disable the button while the request is in-flight.
  const [preflight, setPreflight] = useState<PreflightResult | null>(null);
  const [preflightLoading, setPreflightLoading] = useState(false);
  const [recheckingId, setRecheckingId] = useState<string | null>(null);

  // PHP switch modal
  const [showPhpModal, setShowPhpModal] = useState(false);
  const [phpTarget, setPhpTarget] = useState<Domain | null>(null);
  const [newPhpVersion, setNewPhpVersion] = useState("");
  const [switchingPhp, setSwitchingPhp] = useState(false);

  // Coming Soon preview modal
  const [showComingSoon, setShowComingSoon] = useState(false);
  const [comingSoonTarget, setComingSoonTarget] = useState<Domain | null>(null);

  useEffect(() => {
    fetchDomains();
    fetchUsers();
  }, []);

  const fetchDomains = async () => {
    setLoading(true);
    try {
      const res = await api.get("/domains", { params: { limit: 10000 } });
      const data = (res.data.data || []).map((d: Domain) => ({
        ...d,
        coming_soon: d.maintenance_mode || d.coming_soon || false,
      }));
      setDomains(data);
      // Drop any selection that referenced a now-stale id (post-delete,
      // post-bulk-upload, etc.) so the Export button never sends a
      // dangling id list to the backend.
      setSelectedIds(new Set());
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const fetchUsers = async () => {
    // Vendors live under /admin/vendors (owner-only), not /users. The
    // /users endpoint uses strict-tenant mode which intentionally hides
    // vendor_admin accounts — so fetching from there left the dropdown
    // empty and blocked domain creation.
    if (!isAdmin) return;
    try {
      const res = await api.get("/admin/vendors?limit=500");
      const rows = (res.data?.data || []) as Array<{ id: string; username: string; name: string; status?: string }>;
      setUsersList(
        rows
          .filter((r) => r.username && (r.status ?? "active") === "active")
          .map((r) => ({ id: r.id, username: r.username, name: r.name, role: "vendor" }))
      );
    } catch {
      // Keep empty
    }
  };

  // runPreflightForCreate calls /domains/preflight, which combines
  // whois + DNS + IP match + firewall checks into one round-trip. The
  // result drives both the new "Preflight checks" panel and the
  // existing Registration details auto-fill (registrar / dates). We
  // keep the "only overwrite empty fields" rule on those date /
  // registrar fields so an operator's manual edits aren't clobbered
  // by a slow async response. Silent on failure — preflight may fail
  // for legitimate reasons (whois rate-limit, brand-new domain, no
  // DNS record yet) and the operator can still create the domain.
  const runPreflightForCreate = async (rawName: string) => {
    const name = rawName.trim().toLowerCase();
    if (!name || !/^[a-z0-9][a-z0-9.-]*\.[a-z]{2,}$/i.test(name)) return;
    setPreflightLoading(true);
    setWhoisLoading(true);
    try {
      const res = await api.post("/domains/preflight", { domain: name });
      const data = res.data?.data as PreflightResult | undefined;
      if (!data) return;
      setPreflight(data);
      // Normalise the dates into yyyy-mm-dd for the <input type=date>.
      const normDate = (s?: string | null): string => {
        if (!s) return "";
        const d = new Date(s);
        if (!Number.isFinite(d.getTime())) return "";
        return d.toISOString().slice(0, 10);
      };
      setForm((p) => ({
        ...p,
        registrar: p.registrar || (data.registrar || "").trim(),
        registered_on: p.registered_on || normDate(data.registered_on),
        expires_on: p.expires_on || normDate(data.expires_on),
      }));
    } catch {
      // Non-fatal: the operator can still fill the fields manually.
    } finally {
      setPreflightLoading(false);
      setWhoisLoading(false);
    }
  };

  const handleCreate = async () => {
    if (!form.domain || !form.user) {
      toast.error("Domain and user are required");
      return;
    }
    setCreating(true);
    try {
      await api.post("/domains", form);
      toast.success(`Domain ${form.domain} created successfully`);
      setShowAddModal(false);
      setForm({
        domain: "", user: isAdmin ? "" : (authUser?.username || ""), php_version: "8.2",
        disk_quota_mb: 5120, bandwidth_limit_gb: 100,
        max_databases: 10, max_email_accounts: 50, max_subdomains: 20, max_apps: 5,
        registrar: "", registered_on: "", expires_on: "", auto_renew: false,
      });
      setShowAdvanced(false);
      setPreflight(null);
      fetchDomains();
    } catch (err: any) {
      const msg = err.response?.data?.error?.message || "Failed to create domain";
      toast.error(msg);
    } finally {
      setCreating(false);
    }
  };

  // recheckRow re-runs the connectivity preflight for an existing
  // domain row. The backend persists the new resolved_ip /
  // ip_matches_server / last_checked_at fields back onto the Domain
  // doc so a follow-up fetchDomains() picks up the fresh badge state.
  const recheckRow = async (d: Domain) => {
    setRecheckingId(d.id);
    try {
      const res = await api.post(`/domains/${d.id}/recheck`);
      const data = res.data?.data as PreflightResult | undefined;
      await fetchDomains();
      if (data && data.ip_matches_server === false) {
        toast(
          `Domain does not point to this server (resolves to ${
            (data.resolved_ips || []).join(", ") || "—"
          }, server is ${data.server_ip || "—"})`,
          { icon: "⚠️", style: { background: "#451a03", color: "#fde68a", border: "1px solid #b45309" } }
        );
      } else {
        toast.success(`Re-checked ${d.domain}`);
      }
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Re-check failed");
    } finally {
      setRecheckingId(null);
    }
  };

  const handleDelete = async (id: string, domain: string) => {
    if (!await confirmAction({ title: "Delete?", description: `Are you sure you want to delete ${domain}? This will remove domain files, DNS zone, and all associated data.`, danger: true, confirmLabel: "Delete" })) return;
    try {
      await api.delete(`/domains/${id}`, { data: { confirm: true } });
      toast.success(`Domain ${domain} deleted`);
      fetchDomains();
    } catch {
      toast.error("Failed to delete domain");
    }
  };

  const handleSuspend = async (id: string, domain: string) => {
    try {
      await api.patch(`/domains/${id}/suspend`);
      toast.success(`Domain ${domain} suspended`);
      fetchDomains();
    } catch {
      toast.error("Failed to suspend domain");
    }
  };

  // --- Edit registration (whois) ---
  const openEditRegistration = (d: Domain) => {
    setRegTarget(d);
    setRegForm({
      registrar: d.registrar || "",
      registered_on: (d.registered_on || "").slice(0, 10),
      expires_on: (d.expires_on || "").slice(0, 10),
      auto_renew: !!d.auto_renew,
      nameservers: (d.nameservers || []).join(", "),
    });
  };
  const saveRegistration = async () => {
    if (!regTarget) return;
    setRegSaving(true);
    try {
      const ns = regForm.nameservers
        .split(",")
        .map((n) => n.trim())
        .filter(Boolean);
      await api.patch(`/domains/${regTarget.id}/registration`, {
        registrar: regForm.registrar,
        registered_on: regForm.registered_on,
        expires_on: regForm.expires_on,
        auto_renew: regForm.auto_renew,
        nameservers: ns,
      });
      toast.success("Registration updated");
      setRegTarget(null);
      fetchDomains();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update registration");
    } finally {
      setRegSaving(false);
    }
  };
  const refreshWhois = async () => {
    if (!regTarget) return;
    setWhoisLoading(true);
    try {
      const res = await api.post(`/domains/${regTarget.id}/whois-refresh`);
      const data = res.data?.data || {};
      // The old implementation only sliced the first 10 chars when the
      // string started with YYYY-MM-DD. Real WHOIS output for many TLDs
      // ("17-Apr-2026", "2026.04.17", "2 Jan 2026") fails that test and
      // the field silently reverts to empty — which is the symptom that
      // made operators report "whois fills nothing, save does nothing".
      // normaliseDate is now permissive: Date.parse covers most shapes,
      // with an explicit fallback for the "2-Jan-2026" family that
      // older Chrome / Safari don't parse reliably.
      const normaliseDate = (v: string): string => {
        if (!v) return "";
        const t = Date.parse(v);
        if (Number.isFinite(t)) {
          return new Date(t).toISOString().slice(0, 10);
        }
        // Fallback: "DD-Mon-YYYY" / "DD Mon YYYY" → rebuild as Mon DD YYYY
        const m = v.match(/^(\d{1,2})[\s-](\w{3,})[\s-](\d{4})$/);
        if (m) {
          const t2 = Date.parse(`${m[2]} ${m[1]} ${m[3]}`);
          if (Number.isFinite(t2)) return new Date(t2).toISOString().slice(0, 10);
        }
        return "";
      };
      const newRegistered = normaliseDate(data.registered_on);
      const newExpires = normaliseDate(data.expires_on);
      const newRegistrar = (data.registrar || "").trim();
      const newNS = (data.nameservers || []).join(", ");
      // Prefer the freshly-fetched values — the whole point of clicking
      // "Refresh from whois" is that the server-side values should
      // override whatever was previously in the form. Only fall back
      // to the existing value when whois couldn't find a new one.
      setRegForm((prev) => ({
        ...prev,
        registrar: newRegistrar || prev.registrar,
        registered_on: newRegistered || prev.registered_on,
        expires_on: newExpires || prev.expires_on,
        nameservers: newNS || prev.nameservers,
      }));
      const got: string[] = [];
      if (newRegistrar) got.push(`registrar: ${newRegistrar}`);
      if (newRegistered) got.push(`purchased ${newRegistered}`);
      if (newExpires) got.push(`expires ${newExpires}`);
      if (got.length > 0) {
        toast.success(`WHOIS: ${got.join(" · ")} — click Save to persist`);
      } else {
        toast("WHOIS returned no parseable dates for this TLD — fill manually", { icon: "ℹ️" });
      }
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Whois lookup failed");
    } finally {
      setWhoisLoading(false);
    }
  };

  const handleUnsuspend = async (id: string, domain: string) => {
    try {
      await api.patch(`/domains/${id}/unsuspend`);
      toast.success(`Domain ${domain} unsuspended`);
      fetchDomains();
    } catch {
      toast.error("Failed to unsuspend domain");
    }
  };

  const openPhpSwitch = (d: Domain) => {
    setPhpTarget(d);
    setNewPhpVersion(d.php_version);
    setShowPhpModal(true);
  };

  const handleSwitchPhp = async () => {
    if (!phpTarget) return;
    setSwitchingPhp(true);
    try {
      await api.patch(`/domains/${phpTarget.id}/php`, { php_version: newPhpVersion });
      toast.success(`PHP switched to ${newPhpVersion} for ${phpTarget.domain}`);
      setShowPhpModal(false);
      fetchDomains();
    } catch {
      toast.error("Failed to switch PHP version");
    } finally {
      setSwitchingPhp(false);
    }
  };

  const handleToggleForceSSL = async (d: Domain) => {
    const enabling = !d.force_ssl;
    try {
      await api.post(`/ssl/${d.domain}/force-ssl`, { enable: enabling });
      toast.success(`Force HTTPS ${enabling ? "enabled" : "disabled"} for ${d.domain}`);
      setDomains((prev) =>
        prev.map((dom) => dom.id === d.id ? { ...dom, force_ssl: enabling } : dom)
      );
    } catch {
      toast.error("Failed to toggle Force HTTPS");
    }
  };

  const handleToggleComingSoon = async (d: Domain) => {
    const enabling = !d.coming_soon;
    try {
      const endpoint = enabling
        ? `/maintenance/domains/${d.domain}/enable`
        : `/maintenance/domains/${d.domain}/disable`;
      await api.post(endpoint);
      toast.success(`Coming Soon page ${enabling ? "enabled" : "disabled"} for ${d.domain}`);
      setDomains((prev) =>
        prev.map((dom) => dom.id === d.id ? { ...dom, coming_soon: enabling } : dom)
      );
    } catch {
      toast.error("Failed to toggle Coming Soon page");
    }
  };

  const openComingSoonPreview = (d: Domain) => {
    setComingSoonTarget(d);
    setShowComingSoon(true);
  };

  const openFileManager = (d: Domain) => {
    navigate(`/files?path=/home/${d.user}/domains/${d.domain}/public_html`);
  };

  const filtered = domains.filter((d) =>
    d.domain.toLowerCase().includes(search.toLowerCase()) ||
    (d.user || "").toLowerCase().includes(search.toLowerCase())
  );
  const pg = usePagination("whm-domains");
  useEffect(() => { pg.setTotal(filtered.length); pg.setPage(1); }, [search, filtered.length]);
  const paged = filtered.slice((pg.page - 1) * pg.limit, pg.page * pg.limit);

  // Selection helpers — kept inline so the column-render lambdas stay
  // readable. toggleOne flips one row; toggleAllVisible flips every
  // row currently in the filtered view (the "Select All" header
  // checkbox respects the operator's search state).
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

  // Export: download the current selection as CSV / XLSX. When
  // nothing's selected we send `all=true` so the file is the
  // operator's full list; the backend handler enforces tenant scope.
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
      // Row-selection checkbox column. Header renders the Select All
      // tri-state checkbox; the indeterminate ref-callback sets the
      // visual `-` glyph DOM-side because React doesn't surface the
      // indeterminate property as a regular controlled prop.
      header: (
        <input
          type="checkbox"
          aria-label="Select all visible domains"
          checked={allSelected}
          ref={(el) => { if (el) el.indeterminate = someSelected; }}
          onChange={toggleAllVisible}
          className="h-4 w-4 cursor-pointer accent-blue-500"
        />
      ),
      accessor: (d: Domain) => (
        <input
          type="checkbox"
          aria-label={`Select ${d.domain}`}
          checked={selectedIds.has(d.id)}
          onChange={() => toggleOne(d.id)}
          onClick={(e) => e.stopPropagation()}
          className="h-4 w-4 cursor-pointer accent-blue-500"
        />
      ),
    },
    {
      header: "Domain",
      accessor: (d: Domain) => (
        <div>
          <div className="flex items-center gap-2">
            <Globe size={14} className="text-blue-400" />
            <span className="font-medium text-panel-text">{d.domain}</span>
            {d.coming_soon && (
              <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-amber-500/10 text-amber-400 border border-amber-500/20">
                COMING SOON
              </span>
            )}
            {d.ip_matches_server === false && d.last_checked_at && (
              <span
                className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-red-500/10 text-red-400 border border-red-500/20"
                title={`Resolves to ${d.resolved_ip || "—"} — DNS update required`}
              >
                IP MISMATCH
              </span>
            )}
          </div>
          <span className="text-xs text-panel-muted ml-6 flex items-center gap-1">
              <User size={10} />
              {d.user}
              {(() => {
                const u = usersList.find((u) => u.username === d.user);
                return u ? <span className="text-panel-muted/60">({u.name})</span> : null;
              })()}
            </span>
        </div>
      ),
    },
    {
      header: "Status",
      accessor: (d: Domain) => <StatusBadge status={d.status} />,
    },
    {
      header: "SSL",
      accessor: (d: Domain) => (
        <div className="flex items-center gap-2">
          <span className={d.ssl_active ? "text-green-400 text-sm" : "text-panel-muted text-sm"}>
            {d.ssl_active ? "Active" : "None"}
          </span>
          {d.ssl_active && (
            <button
              onClick={() => handleToggleForceSSL(d)}
              className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${
                d.force_ssl ? "bg-green-500" : "bg-panel-border"
              }`}
              title={d.force_ssl ? "HTTPS forced — click to disable" : "Click to force HTTPS redirect"}
            >
              <span className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${
                d.force_ssl ? "translate-x-4" : "translate-x-0.5"
              }`} />
            </button>
          )}
        </div>
      ),
    },
    {
      header: "PHP",
      accessor: (d: Domain) => (
        <button
          onClick={() => openPhpSwitch(d)}
          className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-panel-bg border border-panel-border text-xs text-panel-muted hover:text-blue-400 hover:border-blue-500/30 transition-colors"
        >
          <Code size={10} />
          {d.php_version}
        </button>
      ),
    },
    {
      header: "Disk",
      accessor: (d: Domain) => (
        <span className="text-panel-muted text-sm flex items-center gap-1">
          <HardDrive size={12} />
          {d.disk_quota_mb >= 1024 ? `${(d.disk_quota_mb / 1024).toFixed(0)} GB` : `${d.disk_quota_mb} MB`}
        </span>
      ),
    },
    {
      header: "Expires",
      accessor: (d: Domain) => {
        const days = daysUntil(d.expires_on);
        return (
          <span
            className={`inline-flex items-center gap-1 px-2 py-0.5 rounded border text-xs font-medium ${expiryBadgeClass(days)}`}
            title={d.expires_on ? `Registrar: ${d.registrar || "—"} · ${d.auto_renew ? "Auto-renew: ON" : "Auto-renew: OFF"}` : "Registration not tracked"}
          >
            <Calendar size={10} />
            {expiryLabel(d.expires_on)}
          </span>
        );
      },
    },
    {
      header: "Created",
      accessor: (d: Domain) => (
        <span className="text-panel-muted text-xs">
          {new Date(d.created_at).toLocaleDateString()}
        </span>
      ),
    },
    {
      header: "Actions",
      accessor: (d: Domain) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => openFileManager(d)}
            title="File Manager"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
          >
            <FolderOpen size={14} />
          </button>
          <button
            onClick={() => openComingSoonPreview(d)}
            title="Coming Soon Page"
            className={`p-1.5 rounded hover:bg-panel-bg transition-colors ${
              d.coming_soon ? "text-amber-400" : "text-panel-muted hover:text-amber-400"
            }`}
          >
            <Clock size={14} />
          </button>
          {d.status === "active" ? (
            <button
              onClick={() => handleSuspend(d.id, d.domain)}
              title="Suspend"
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors"
            >
              <PauseCircle size={14} />
            </button>
          ) : (
            <button
              onClick={() => handleUnsuspend(d.id, d.domain)}
              title="Unsuspend"
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
            >
              <PlayCircle size={14} />
            </button>
          )}
          <button
            onClick={() => openEditRegistration(d)}
            title="Edit registration (registrar, purchase date, expiry, nameservers)"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-cyan-400 transition-colors"
          >
            <FileText size={14} />
          </button>
          <button
            onClick={() => recheckRow(d)}
            disabled={recheckingId === d.id}
            title="Re-check connectivity (DNS, IP match, firewall)"
            className={`p-1.5 rounded hover:bg-panel-bg transition-colors disabled:opacity-50 ${
              d.ip_matches_server === false && d.last_checked_at
                ? "text-red-400 hover:text-red-300"
                : "text-panel-muted hover:text-blue-400"
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
            title="Delete"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
          >
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  const inputClass =
    "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm placeholder-panel-muted/50 focus:outline-none focus:border-blue-500";

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Domains</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage your server domains and virtual hosts
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchDomains}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => downloadExport("csv")}
            disabled={exporting !== null}
            title={selectedIds.size > 0 ? `Export ${selectedIds.size} selected as CSV` : "Export all domains as CSV"}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-text hover:bg-panel-border/40 transition-colors text-sm disabled:opacity-50"
          >
            <FileText size={14} />
            {exporting === "csv" ? "Exporting…" : selectedIds.size > 0 ? `Export ${selectedIds.size} (CSV)` : "Export CSV"}
          </Button>
          <Button
            onClick={() => downloadExport("xlsx")}
            disabled={exporting !== null}
            title={selectedIds.size > 0 ? `Export ${selectedIds.size} selected as Excel` : "Export all domains as Excel"}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-text hover:bg-panel-border/40 transition-colors text-sm disabled:opacity-50"
          >
            <FileText size={14} />
            {exporting === "xlsx" ? "Exporting…" : selectedIds.size > 0 ? `Export ${selectedIds.size} (Excel)` : "Export Excel"}
          </Button>
          <Button
            onClick={() => setShowBulkModal(true)}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-text hover:bg-panel-border/40 transition-colors text-sm"
          >
            <Upload size={14} />
            Bulk Upload
          </Button>
          {selectedIds.size > 0 && (
            <Button
              onClick={() => setShowBulkDeleteModal(true)}
              className="flex items-center gap-2 px-3 py-2 bg-red-600/20 hover:bg-red-600/30 border border-red-500/40 rounded-lg text-red-200 hover:text-red-100 transition-colors text-sm font-medium"
              title={`Delete ${selectedIds.size} selected domains (OTP-gated)`}
            >
              <Trash2 size={14} />
              Delete {selectedIds.size} Selected
            </Button>
          )}
          <Button
            onClick={() => setShowAddModal(true)}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Add Domain
          </Button>
        </div>
      </div>

      {/* Search */}
      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search domains or users..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
        </div>
      </Card>

      {/* Table */}
      <Card>
        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2, 3, 4, 5].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={paged}
            page={pg.page} limit={pg.limit} total={pg.total}
            onPageChange={pg.setPage} onLimitChange={pg.setLimit} />
        ) : (
          <div className="text-center py-16 px-4">
            <Globe size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No domains found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No domains match your search query."
                : "Get started by adding your first domain to the server."}
            </p>
            {!search && (
              <Button
                onClick={() => setShowAddModal(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Add Domain
              </Button>
            )}
          </div>
        )}
      </Card>

      {/* Bulk Delete Modal — WHM-only, OTP-gated */}
      <BulkDeleteDomainsModal
        isOpen={showBulkDeleteModal}
        onClose={() => setShowBulkDeleteModal(false)}
        selectedIds={Array.from(selectedIds)}
        selectedNames={domains.filter((d) => selectedIds.has(d.id)).map((d) => d.domain)}
        onConfirmed={fetchDomains}
        requestOtp={async (ids) => {
          const { data } = await api.post<{ data: BulkDeleteRequestResult }>(
            "/domains/bulk-delete/request-otp",
            { ids },
          );
          return data.data;
        }}
        confirm={async (token, code) => {
          const { data } = await api.post<{ data: BulkDeleteConfirmResult }>(
            "/domains/bulk-delete/confirm",
            { token, code },
          );
          return data.data;
        }}
      />

      {/* Bulk Upload Modal */}
      <BulkUploadDomainsModal
        isOpen={showBulkModal}
        onClose={() => setShowBulkModal(false)}
        scopeLabel="any vendor / linux user named in the row"
        onUploaded={fetchDomains}
        submit={async (file, opts) => {
          const fd = new FormData();
          fd.append("file", file);
          fd.append("issue_ssl", opts.issue_ssl ? "true" : "false");
          fd.append("force_ssl", opts.force_ssl ? "true" : "false");
          const { data } = await api.post<{ data: BulkUploadDomainsResponse }>(
            "/domains/bulk-upload",
            fd,
            { headers: { "Content-Type": "multipart/form-data" } },
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
          // Pull suggested filename from Content-Disposition; fall back
          // to a sane default if the server didn't send one.
          const cd = (res.headers as Record<string, string>)["content-disposition"] || "";
          const m = /filename=\"?([^\";]+)\"?/.exec(cd);
          a.download = m?.[1] || `domains-bulk-upload-template.${format}`;
          document.body.appendChild(a);
          a.click();
          a.remove();
          URL.revokeObjectURL(url);
        }}
      />

      {/* Add Domain Modal */}
      <Modal
        isOpen={showAddModal}
        title="Add New Domain"
        onClose={() => { setShowAddModal(false); setPreflight(null); }}
        size="lg"
      >
        <div className="space-y-5">
          {/* Domain + User + PHP row */}
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1 flex items-center gap-2">
              Domain Name *
              {whoisLoading && (
                <span className="inline-flex items-center gap-1 text-[11px] text-panel-muted font-normal">
                  <RefreshCw size={10} className="animate-spin" /> looking up WHOIS…
                </span>
              )}
            </label>
            <input
              type="text"
              value={form.domain}
              onChange={(e) => setForm((p) => ({ ...p, domain: e.target.value }))}
              onBlur={() => runPreflightForCreate(form.domain)}
              placeholder="example.com"
              className={inputClass}
            />
            <p className="text-xs text-panel-muted mt-1">
              Tab out of this field to run preflight checks (WHOIS, DNS, IP match, firewall). Registration details below are auto-filled from the WHOIS portion of the result.
            </p>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">Account (User) *</label>
              {isAdmin ? (
                <SearchableSelect
                  required
                  value={form.user}
                  onChange={(v) => setForm((p) => ({ ...p, user: v }))}
                  options={usersList
                    .filter((u) => u.role === "vendor" && u.username)
                    .map((u) => ({
                      value: u.username,
                      label: u.username,
                      hint: u.name,
                    }))}
                  placeholder="Select a vendor…"
                  emptyMessage="No vendors match the filter"
                />
              ) : (
                <input
                  type="text"
                  value={authUser?.username || ""}
                  disabled
                  className={inputClass + " opacity-70 cursor-not-allowed"}
                />
              )}
              <p className="text-xs text-panel-muted mt-1">
                {isAdmin ? "Domain will be created under this user's account" : "Domain will be created under your account"}
              </p>
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">PHP Version</label>
              <select
                value={form.php_version}
                onChange={(e) => setForm((p) => ({ ...p, php_version: e.target.value }))}
                className={inputClass}
              >
                {PHP_VERSIONS.map((v) => (
                  <option key={v} value={v}>PHP {v}</option>
                ))}
              </select>
            </div>
          </div>

          {/* Preflight checks — populated by /domains/preflight on
              the domain field's onBlur. Surfaces WHOIS / DNS / IP /
              firewall results so the operator catches "this domain
              isn't pointed at us yet" BEFORE clicking Create. */}
          {(preflightLoading || preflight) && (
            <Card className="border-panel-border">
              <div className="p-4">
                <div className="flex items-center justify-between mb-3">
                  <h4 className="text-sm font-medium text-panel-text flex items-center gap-2">
                    <Activity size={14} className="text-blue-400" />
                    Preflight checks
                    {preflightLoading && (
                      <span className="inline-flex items-center gap-1 text-[11px] text-panel-muted font-normal">
                        <RefreshCw size={10} className="animate-spin" /> Running preflight…
                      </span>
                    )}
                  </h4>
                  {!preflightLoading && (
                    <button
                      type="button"
                      onClick={() => runPreflightForCreate(form.domain)}
                      disabled={!form.domain}
                      className="inline-flex items-center gap-1 px-2.5 py-1 text-xs bg-panel-surface border border-panel-border hover:border-blue-500/30 hover:text-blue-300 text-panel-muted rounded-lg disabled:opacity-50"
                    >
                      <RefreshCw size={11} /> Re-check
                    </button>
                  )}
                </div>
                {preflightLoading && !preflight && (
                  <div className="text-xs text-panel-muted py-2">
                    Resolving DNS, querying WHOIS, probing firewall ports…
                  </div>
                )}
                {preflight && (
                  <div className="space-y-1.5">
                    {preflight.checks.map((c, i) => (
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
                          {c.detail && (
                            <span className="text-panel-muted"> — {c.detail}</span>
                          )}
                        </div>
                      </div>
                    ))}
                    {preflight.ip_matches_server === false && (
                      <div className="mt-3 flex items-start gap-2 px-3 py-2 rounded-lg border border-amber-500/30 bg-amber-500/10 text-amber-300 text-xs">
                        <AlertTriangle size={14} className="shrink-0 mt-0.5" />
                        <span>
                          Domain does not point to this server (resolves to{" "}
                          <span className="font-mono">
                            {(preflight.resolved_ips || []).join(", ") || "—"}
                          </span>
                          , server is{" "}
                          <span className="font-mono">{preflight.server_ip || "—"}</span>) — DNS update required.
                        </span>
                      </div>
                    )}
                  </div>
                )}
              </div>
            </Card>
          )}

          {/* Registration details — the primary optional section now,
              sitting above the collapsed Resource Limits. Everything
              here is operator-entered; the background whois refresher
              will fill them in automatically when it's wired up. */}
          <div className="border-t border-panel-border pt-4">
            <h4 className="text-sm font-medium text-panel-text mb-3 flex items-center gap-2">
              <FileText size={14} />
              Registration details
              <span className="text-xs font-normal text-panel-muted ml-1">(optional)</span>
            </h4>
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="block text-xs text-panel-muted mb-1">Registrar</label>
                <input
                  type="text"
                  placeholder="GoDaddy, Namecheap, Cloudflare…"
                  value={form.registrar}
                  onChange={(e) => setForm((p) => ({ ...p, registrar: e.target.value }))}
                  className={inputClass}
                />
              </div>
              <div className="flex items-end gap-2">
                <label className="inline-flex items-center gap-2 text-xs text-panel-muted cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={form.auto_renew}
                    onChange={(e) => setForm((p) => ({ ...p, auto_renew: e.target.checked }))}
                  />
                  Auto-renew is ON at the registrar
                </label>
              </div>
              <div>
                <label className="block text-xs text-panel-muted mb-1">Purchased on</label>
                <input
                  type="date"
                  value={form.registered_on}
                  onChange={(e) => setForm((p) => ({ ...p, registered_on: e.target.value }))}
                  className={inputClass}
                />
              </div>
              <div>
                <label className="block text-xs text-panel-muted mb-1">Expires on</label>
                <input
                  type="date"
                  value={form.expires_on}
                  onChange={(e) => setForm((p) => ({ ...p, expires_on: e.target.value }))}
                  className={inputClass}
                />
              </div>
            </div>
            <p className="text-xs text-panel-muted/70 mt-2">
              Expiry fed into the Dashboard "Domains expiring soon" widget — leave blank to exclude from the alert list. You can edit any of these later from the row's <span className="text-panel-text">Edit registration</span> action.
            </p>
          </div>

          {/* Resource Limits — collapsed by default. The hosting
              package usually defines the right numbers and most
              operators don't need to override per-domain. Click the
              header to reveal. */}
          <div className="border-t border-panel-border pt-4">
            <button
              type="button"
              onClick={() => setShowAdvanced(!showAdvanced)}
              className="flex items-center gap-2 text-sm font-medium text-panel-muted hover:text-panel-text"
            >
              {showAdvanced ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
              <Users size={14} />
              Resource limits (override package defaults)
            </button>
            {showAdvanced && (
              <div className="grid grid-cols-3 gap-4 mt-3">
                <div>
                  <label className="block text-xs text-panel-muted mb-1">Disk Quota (MB)</label>
                  <input
                    type="number"
                    value={form.disk_quota_mb}
                    onChange={(e) => setForm((p) => ({ ...p, disk_quota_mb: parseInt(e.target.value) || 0 }))}
                    className={inputClass}
                  />
                </div>
                <div>
                  <label className="block text-xs text-panel-muted mb-1">Bandwidth (GB)</label>
                  <input
                    type="number"
                    value={form.bandwidth_limit_gb}
                    onChange={(e) => setForm((p) => ({ ...p, bandwidth_limit_gb: parseInt(e.target.value) || 0 }))}
                    className={inputClass}
                  />
                </div>
                <div>
                  <label className="block text-xs text-panel-muted mb-1">Max Databases</label>
                  <input
                    type="number"
                    value={form.max_databases}
                    onChange={(e) => setForm((p) => ({ ...p, max_databases: parseInt(e.target.value) || 0 }))}
                    className={inputClass}
                  />
                </div>
                <div>
                  <label className="block text-xs text-panel-muted mb-1">Max Email Accounts</label>
                  <input
                    type="number"
                    value={form.max_email_accounts}
                    onChange={(e) => setForm((p) => ({ ...p, max_email_accounts: parseInt(e.target.value) || 0 }))}
                    className={inputClass}
                  />
                </div>
                <div>
                  <label className="block text-xs text-panel-muted mb-1">Max Subdomains</label>
                  <input
                    type="number"
                    value={form.max_subdomains}
                    onChange={(e) => setForm((p) => ({ ...p, max_subdomains: parseInt(e.target.value) || 0 }))}
                    className={inputClass}
                  />
                </div>
                <div>
                  <label className="block text-xs text-panel-muted mb-1">Max Apps</label>
                  <input
                    type="number"
                    value={form.max_apps}
                    onChange={(e) => setForm((p) => ({ ...p, max_apps: parseInt(e.target.value) || 0 }))}
                    className={inputClass}
                  />
                </div>
              </div>
            )}
          </div>

          {/* Actions */}
          <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
            <Button
              onClick={() => { setShowAddModal(false); setPreflight(null); }}
              className="px-4 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm transition-colors"
            >
              Cancel
            </Button>
            <Button
              onClick={handleCreate}
              disabled={creating || !form.domain || !form.user}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white rounded-lg text-sm font-medium transition-colors"
            >
              {creating ? (
                <RefreshCw size={14} className="animate-spin" />
              ) : (
                <Plus size={14} />
              )}
              {creating ? "Creating..." : "Create Domain"}
            </Button>
          </div>
        </div>
      </Modal>

      {/* PHP Switch Modal */}
      <Modal isOpen={showPhpModal} title="Switch PHP Version" onClose={() => setShowPhpModal(false)} size="sm">
        <div className="space-y-4">
          <p className="text-sm text-panel-muted">
            Change PHP version for <span className="text-panel-text font-medium">{phpTarget?.domain}</span>
          </p>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1">New PHP Version</label>
            <select
              value={newPhpVersion}
              onChange={(e) => setNewPhpVersion(e.target.value)}
              className={inputClass}
            >
              {PHP_VERSIONS.map((v) => (
                <option key={v} value={v}>
                  PHP {v} {v === phpTarget?.php_version ? "(current)" : ""}
                </option>
              ))}
            </select>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button
              onClick={() => setShowPhpModal(false)}
              className="px-4 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm transition-colors"
            >
              Cancel
            </Button>
            <Button
              onClick={handleSwitchPhp}
              disabled={switchingPhp || newPhpVersion === phpTarget?.php_version}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white rounded-lg text-sm font-medium transition-colors"
            >
              {switchingPhp ? <RefreshCw size={14} className="animate-spin" /> : <Code size={14} />}
              {switchingPhp ? "Switching..." : "Switch PHP"}
            </Button>
          </div>
        </div>
      </Modal>

      {/* Coming Soon Modal */}
      <Modal isOpen={showComingSoon} title="Coming Soon Page" onClose={() => setShowComingSoon(false)} size="lg">
        {comingSoonTarget && (
          <div className="space-y-4">
            <div className="flex items-center justify-between p-4 bg-panel-bg rounded-lg border border-panel-border">
              <div>
                <p className="text-sm font-medium text-panel-text">{comingSoonTarget.domain}</p>
                <p className="text-xs text-panel-muted mt-0.5">
                  {comingSoonTarget.coming_soon
                    ? "Coming Soon page is currently active"
                    : "Coming Soon page is disabled"}
                </p>
              </div>
              <button
                onClick={() => handleToggleComingSoon(comingSoonTarget)}
                className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                  comingSoonTarget.coming_soon ? "bg-amber-500" : "bg-panel-border"
                }`}
              >
                <span
                  className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                    comingSoonTarget.coming_soon ? "translate-x-6" : "translate-x-1"
                  }`}
                />
              </button>
            </div>

            {/* Preview */}
            <div>
              <div className="flex items-center justify-between mb-2">
                <p className="text-xs font-medium text-panel-muted uppercase tracking-wider">Preview</p>
                <a
                  href={`https://${comingSoonTarget.domain}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="flex items-center gap-1 text-xs text-blue-400 hover:text-blue-300"
                >
                  <Eye size={12} /> View Live
                </a>
              </div>
              <div className="rounded-xl border border-panel-border overflow-hidden bg-gradient-to-br from-slate-900 via-blue-950 to-indigo-950 relative">
                {/* Decorative elements */}
                <div className="absolute inset-0 overflow-hidden">
                  <div className="absolute -top-24 -right-24 w-96 h-96 bg-blue-500/5 rounded-full blur-3xl" />
                  <div className="absolute -bottom-24 -left-24 w-96 h-96 bg-indigo-500/5 rounded-full blur-3xl" />
                  <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[600px] h-[600px] bg-blue-500/3 rounded-full blur-3xl" />
                </div>

                <div className="relative px-8 py-16 text-center">
                  {/* Logo/Icon */}
                  <div className="mx-auto w-16 h-16 rounded-2xl bg-gradient-to-br from-blue-500 to-indigo-600 flex items-center justify-center mb-6 shadow-lg shadow-blue-500/20">
                    <Rocket size={28} className="text-white" />
                  </div>

                  {/* Heading */}
                  <h2 className="text-2xl font-bold text-white mb-2">
                    Something Amazing is Coming
                  </h2>
                  <p className="text-blue-200/60 text-sm max-w-md mx-auto mb-8">
                    We're working hard to bring you an incredible experience. Stay tuned for the launch of{" "}
                    <span className="text-blue-300 font-medium">{comingSoonTarget.domain}</span>
                  </p>

                  {/* Progress bar */}
                  <div className="max-w-xs mx-auto mb-8">
                    <div className="flex justify-between text-xs text-blue-200/40 mb-1.5">
                      <span>Progress</span>
                      <span>Coming Soon</span>
                    </div>
                    <div className="w-full h-2 bg-white/5 rounded-full overflow-hidden">
                      <div
                        className="h-full rounded-full bg-gradient-to-r from-blue-500 to-indigo-500"
                        style={{ width: "72%" }}
                      />
                    </div>
                  </div>

                  {/* Email signup mock */}
                  <div className="max-w-sm mx-auto flex gap-2">
                    <div className="flex-1 px-4 py-2.5 bg-white/5 border border-white/10 rounded-lg text-white/30 text-sm text-left">
                      Enter your email for updates
                    </div>
                    <div className="px-5 py-2.5 bg-gradient-to-r from-blue-500 to-indigo-600 text-white text-sm font-medium rounded-lg">
                      Notify Me
                    </div>
                  </div>

                  {/* Social links mock */}
                  <div className="flex items-center justify-center gap-4 mt-8">
                    {["Twitter", "GitHub", "LinkedIn"].map((s) => (
                      <span key={s} className="text-xs text-blue-200/30 hover:text-blue-200/50 transition-colors cursor-default">
                        {s}
                      </span>
                    ))}
                  </div>
                </div>

                {/* Footer */}
                <div className="border-t border-white/5 px-8 py-3 text-center">
                  <p className="text-[10px] text-blue-200/20">
                    Powered by Betazen Server Panel &bull; betazeninfotech.com
                  </p>
                </div>
              </div>
            </div>

            {/* Actions */}
            <div className="flex items-center justify-between pt-2">
              <button
                onClick={() => openFileManager(comingSoonTarget)}
                className="flex items-center gap-2 text-sm text-blue-400 hover:text-blue-300 transition-colors"
              >
                <FolderOpen size={14} />
                Manage Root Directory
              </button>
              <div className="flex gap-2">
                <Button
                  onClick={() => setShowComingSoon(false)}
                  className="px-4 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm transition-colors"
                >
                  Close
                </Button>
                <Button
                  onClick={() => handleToggleComingSoon(comingSoonTarget)}
                  className={`flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium transition-colors ${
                    comingSoonTarget.coming_soon
                      ? "bg-red-600/10 text-red-400 hover:bg-red-600/20 border border-red-600/20"
                      : "bg-amber-600 hover:bg-amber-700 text-white"
                  }`}
                >
                  <Clock size={14} />
                  {comingSoonTarget.coming_soon ? "Disable Coming Soon" : "Enable Coming Soon"}
                </Button>
              </div>
            </div>
          </div>
        )}
      </Modal>

      {/* Edit registration — registrar / dates / nameservers / auto-renew.
          Separate modal from the create form so ops can update
          registration info post-hoc without touching PHP / quotas. */}
      <Modal
        isOpen={!!regTarget}
        onClose={() => { if (!regSaving) setRegTarget(null); }}
        title={regTarget ? `Edit registration — ${regTarget.domain}` : "Edit registration"}
        size="lg"
      >
        {regTarget && (
          <div className="space-y-4">
            <div className="flex items-center justify-between gap-3 text-xs text-panel-muted bg-blue-500/5 border border-blue-500/20 rounded-lg px-3 py-2">
              <span>
                Pulls registrar + dates + nameservers from the server's <code className="font-mono text-panel-text">whois</code> binary. Review the fields after a lookup before saving — every TLD formats its whois output differently.
              </span>
              <button
                type="button"
                onClick={refreshWhois}
                disabled={whoisLoading}
                className="shrink-0 px-3 py-1.5 text-xs bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium disabled:opacity-50 inline-flex items-center gap-1.5"
              >
                <RefreshCw size={12} className={whoisLoading ? "animate-spin" : ""} />
                {whoisLoading ? "Looking up…" : "Refresh from whois"}
              </button>
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="block text-xs text-panel-muted mb-1">Registrar</label>
                <input
                  type="text"
                  value={regForm.registrar}
                  onChange={(e) => setRegForm((p) => ({ ...p, registrar: e.target.value }))}
                  className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm focus:outline-none focus:border-blue-500"
                />
              </div>
              <div className="flex items-end">
                <label className="inline-flex items-center gap-2 text-xs text-panel-muted cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={regForm.auto_renew}
                    onChange={(e) => setRegForm((p) => ({ ...p, auto_renew: e.target.checked }))}
                  />
                  Auto-renew is ON at the registrar
                </label>
              </div>
              <div>
                <label className="block text-xs text-panel-muted mb-1">Purchased on</label>
                <input
                  type="date"
                  value={regForm.registered_on}
                  onChange={(e) => setRegForm((p) => ({ ...p, registered_on: e.target.value }))}
                  className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm focus:outline-none focus:border-blue-500"
                />
              </div>
              <div>
                <label className="block text-xs text-panel-muted mb-1">Expires on</label>
                <input
                  type="date"
                  value={regForm.expires_on}
                  onChange={(e) => setRegForm((p) => ({ ...p, expires_on: e.target.value }))}
                  className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm focus:outline-none focus:border-blue-500"
                />
              </div>
            </div>

            <div>
              <label className="block text-xs text-panel-muted mb-1">Nameservers (comma-separated)</label>
              <input
                type="text"
                value={regForm.nameservers}
                onChange={(e) => setRegForm((p) => ({ ...p, nameservers: e.target.value }))}
                placeholder="ns1.registrar.com, ns2.registrar.com"
                className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text font-mono text-sm focus:outline-none focus:border-blue-500"
              />
            </div>

            <div className="flex justify-end gap-3 pt-2 border-t border-panel-border">
              <button
                type="button"
                onClick={() => setRegTarget(null)}
                disabled={regSaving}
                className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={saveRegistration}
                disabled={regSaving}
                className="px-4 py-2 text-sm bg-cyan-600 hover:bg-cyan-700 text-white rounded-lg font-medium disabled:opacity-50"
              >
                {regSaving ? "Saving…" : "Save registration"}
              </button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
