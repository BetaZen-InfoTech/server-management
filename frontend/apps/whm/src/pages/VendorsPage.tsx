import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal, PasswordInput, confirmAction, usePagination } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Building2, Plus, RefreshCw, Search, Trash2, Eye, Power,
  User, Mail, Users as UsersIcon, Globe, KeyRound, Package as PackageIcon,
  HardDrive, RotateCcw, AlertCircle, LogIn,
} from "lucide-react";

interface VendorItem {
  id: string;
  username: string;
  name: string;
  email: string;
  status: "active" | "suspended" | "trashed";
  package_id?: string;
  package_name?: string;
  team_count: number;
  domain_count: number;
  storage_bytes: number;
  createdAt: string;
  lastLogin: string;
  deleted_at?: string;
  trash_expires_at?: string;
}

// humanBytes turns a byte count into a short string ops can scan at a
// glance. Falls back to "—" for 0 so the column is less noisy when a
// vendor has no provisioned home directory yet.
function humanBytes(n: number): string {
  if (!n || n <= 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
}

interface VendorStats {
  total_vendors: number;
  active_vendors: number;
  total_team_members: number;
  total_managed_users: number;
  total_domains: number;
}

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

export default function VendorsPage() {
  const [vendors, setVendors] = useState<VendorItem[]>([]);
  const [stats, setStats] = useState<VendorStats>({
    total_vendors: 0,
    active_vendors: 0,
    total_team_members: 0,
    total_managed_users: 0,
    total_domains: 0,
  });
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({
    username: "",
    name: "",
    email: "",
    password: "",
    package_id: "",
    primary_domain: "",
  });
  const [packages, setPackages] = useState<{ id: string; name: string; is_default?: boolean }[]>([]);

  // Tab — "active" shows live vendors, "trash" shows soft-deleted.
  // Each tab refetches its own list; stats stay constant at the top.
  const [tab, setTab] = useState<"active" | "trash">("active");
  const [trashed, setTrashed] = useState<VendorItem[]>([]);
  const [trashLoading, setTrashLoading] = useState(false);

  // Password modal.
  const [pwdTarget, setPwdTarget] = useState<VendorItem | null>(null);
  const [pwdValue, setPwdValue] = useState("");
  const [pwdSaving, setPwdSaving] = useState(false);

  // Package modal.
  const [pkgTarget, setPkgTarget] = useState<VendorItem | null>(null);
  const [pkgChoice, setPkgChoice] = useState("");
  const [pkgSaving, setPkgSaving] = useState(false);

  useEffect(() => {
    fetchVendors();
    fetchStats();
    fetchPackages();
  }, []);

  const fetchVendors = async () => {
    setLoading(true);
    try {
      const res = await api.get("/admin/vendors", { params: { limit: 10000 } });
      setVendors(res.data.data || []);
    } catch {
      // keep empty
    } finally {
      setLoading(false);
    }
  };

  const fetchStats = async () => {
    try {
      const res = await api.get("/admin/vendors/stats");
      setStats(res.data.data || {
        total_vendors: 0,
        active_vendors: 0,
        total_team_members: 0,
        total_managed_users: 0,
        total_domains: 0,
      });
    } catch { /* keep zeros */ }
  };

  const fetchPackages = async () => {
    try {
      const res = await api.get("/packages");
      setPackages(res.data.data || []);
    } catch { /* keep empty */ }
  };

  const refresh = () => {
    fetchVendors();
    fetchStats();
    if (tab === "trash") fetchTrash();
  };

  const fetchTrash = async () => {
    setTrashLoading(true);
    try {
      const res = await api.get("/admin/vendors/trash", { params: { limit: 10000 } });
      setTrashed(res.data.data || []);
    } catch {
      setTrashed([]);
    } finally {
      setTrashLoading(false);
    }
  };

  // Auto-refetch the trash list when the tab is opened for the first
  // time — otherwise the user clicks the tab and sees an empty state
  // even when the server has rows.
  useEffect(() => {
    if (tab === "trash") fetchTrash();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab]);

  // --- Per-row actions ---

  const openPasswordModal = (v: VendorItem) => {
    setPwdTarget(v);
    setPwdValue("");
  };
  const savePassword = async () => {
    if (!pwdTarget) return;
    if (pwdValue.length < 8) {
      toast.error("Password must be at least 8 characters");
      return;
    }
    setPwdSaving(true);
    try {
      await api.post(`/users/${pwdTarget.id}/reset-password`, { password: pwdValue });
      toast.success(`Password updated for ${pwdTarget.name}`);
      setPwdTarget(null);
      setPwdValue("");
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update password");
    } finally {
      setPwdSaving(false);
    }
  };

  const openPackageModal = (v: VendorItem) => {
    setPkgTarget(v);
    setPkgChoice(v.package_id || "");
  };
  const savePackage = async () => {
    if (!pkgTarget || !pkgChoice) return;
    setPkgSaving(true);
    try {
      await api.put(`/admin/vendors/${pkgTarget.id}/package`, { package_id: pkgChoice });
      toast.success(`Package updated for ${pkgTarget.name}`);
      setPkgTarget(null);
      refresh();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update package");
    } finally {
      setPkgSaving(false);
    }
  };

  const handleTrash = async (v: VendorItem) => {
    const ok = await confirmAction({
      title: "Move to trash?",
      description: `Send "${v.name}" to the trash? Their linux account will be locked, apps stopped, and cron jobs cleared. You have 15 days to restore before permanent deletion.`,
      danger: true,
      confirmLabel: "Move to trash",
    });
    if (!ok) return;
    try {
      await api.post(`/admin/vendors/${v.id}/trash`);
      toast.success(`${v.name} moved to trash`);
      refresh();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to trash vendor");
    }
  };

  const handleRestore = async (v: VendorItem) => {
    try {
      await api.post(`/admin/vendors/${v.id}/restore`);
      toast.success(`${v.name} restored`);
      refresh();
      fetchTrash();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to restore vendor");
    }
  };

  const handleNameChange = (value: string) => {
    setForm((prev) => ({
      ...prev,
      name: value,
      username: prev.username || value.replace(/[^a-z0-9]/gi, "").slice(0, 16).toLowerCase(),
    }));
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.username || !form.name || !form.email || !form.password) {
      toast.error("Please fill all required fields");
      return;
    }
    if (!/^[a-z][a-z0-9]{2,15}$/.test(form.username)) {
      toast.error("Username must be 3-16 lowercase alphanumeric characters, starting with a letter");
      return;
    }
    if (!form.package_id) {
      toast.error("Please select a hosting package");
      return;
    }
    setCreating(true);
    try {
      await api.post("/users", { ...form, role: "vendor" });
      toast.success(`Vendor ${form.name} created`);
      setShowCreate(false);
      setForm({ username: "", name: "", email: "", password: "", package_id: "", primary_domain: "" });
      refresh();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create vendor");
    } finally {
      setCreating(false);
    }
  };

  const handleSuspend = async (id: string, name: string) => {
    if (!await confirmAction({ title: "Suspend?", description: `Suspend vendor "${name}"?`, danger: true, confirmLabel: "Suspend" })) return;
    try {
      await api.post(`/users/${id}/suspend`);
      toast.success(`Vendor ${name} suspended`);
      refresh();
    } catch {
      toast.error("Failed to suspend vendor");
    }
  };

  const handleActivate = async (id: string, name: string) => {
    try {
      await api.post(`/users/${id}/activate`);
      toast.success(`Vendor ${name} activated`);
      refresh();
    } catch {
      toast.error("Failed to activate vendor");
    }
  };

  // handleDelete is kept for the Trash tab's "Delete permanently now"
  // action. For the Active tab, deletion goes through handleTrash which
  // provides the 15-day safety net.
  //
  // The backend renames the linux account to <username>-deleted-<ts>
  // and preserves the home directory — so a future `useradd <same>`
  // doesn't collide with orphan uid/gid entries but files stay
  // recoverable for an operator who needs them.
  const handleDelete = async (id: string, name: string) => {
    if (!await confirmAction({
      title: "Delete permanently?",
      description: `Immediately delete "${name}"? Every domain, mailbox, DNS zone, and FTP account is torn down. The linux account is renamed to <username>-deleted-<timestamp>; the home directory is preserved under the new name so files stay recoverable. The panel record itself is gone for good — skipping the trash window.`,
      danger: true,
      confirmLabel: "Delete now",
    })) return;
    try {
      await api.delete(`/admin/vendors/${id}/purge`);
      toast.success(`Vendor ${name} permanently deleted`);
      refresh();
      fetchTrash();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to delete vendor");
    }
  };

  const handleView = async (id: string) => {
    try {
      const res = await api.get(`/users/${id}`);
      const d = res.data.data || {};
      toast.success(`${d.name || "Vendor"} — ${d.email || ""}`);
    } catch {
      toast.error("Failed to load vendor");
    }
  };

  // Impersonate the vendor: request a short-lived access token from the
  // backend, swap our own admin token for it in localStorage (stashing the
  // admin token under a restore key), and reload so the new token takes
  // effect for every subsequent API call. A banner + "Return to admin"
  // button on the top bar handles the swap back — but even without that,
  // the impersonation token expires in 15 minutes and the user falls back
  // to the admin account on next login.
  const handleImpersonate = async (v: VendorItem) => {
    if (!await confirmAction({
      title: `Log in as ${v.name}?`,
      description: `You will be signed in as this vendor inside their tenant panel for up to 15 minutes. Every action during that session is audit-logged as you.`,
      confirmLabel: "Log in as vendor",
    })) return;
    try {
      const res = await api.post(`/admin/vendors/${v.id}/impersonate`);
      const payload = res.data.data || {};
      if (!payload.access_token) {
        toast.error("Impersonation token missing");
        return;
      }
      // Preserve the admin token so we can restore it when the vendor
      // session ends (or the admin clicks "Return to admin").
      const adminToken = localStorage.getItem("access_token");
      if (adminToken) {
        localStorage.setItem("admin_restore_token", adminToken);
      }
      const adminRefresh = localStorage.getItem("refresh_token");
      if (adminRefresh) {
        localStorage.setItem("admin_restore_refresh", adminRefresh);
      }
      localStorage.setItem("access_token", payload.access_token);
      // No refresh token — impersonation sessions are explicitly one-shot.
      localStorage.removeItem("refresh_token");
      toast.success(`Now logged in as ${v.name}`);
      // Navigate to the panel root; the router + auth store will rehydrate
      // from the new token.
      setTimeout(() => { window.location.href = "/whm"; }, 300);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Impersonation failed");
    }
  };

  const filteredVendors = vendors.filter((v) => {
    const q = search.toLowerCase();
    return (
      v.name.toLowerCase().includes(q) ||
      v.email.toLowerCase().includes(q) ||
      (v.username || "").toLowerCase().includes(q)
    );
  });
  const pg = usePagination("whm-vendors");
  useEffect(() => { pg.setTotal(filteredVendors.length); pg.setPage(1); }, [search, filteredVendors.length]);
  const filtered = filteredVendors.slice((pg.page - 1) * pg.limit, pg.page * pg.limit);

  const columns = [
    {
      header: "Vendor",
      accessor: (v: VendorItem) => (
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-full bg-blue-500/10 border border-blue-500/20 flex items-center justify-center shrink-0">
            <span className="text-blue-400 text-xs font-bold">
              {v.name.charAt(0).toUpperCase()}
            </span>
          </div>
          <div>
            <span className="font-medium text-panel-text block">{v.name}</span>
            <div className="flex items-center gap-2 text-xs text-panel-muted">
              <span className="flex items-center gap-1">
                <User size={10} />
                {v.username}
              </span>
              <span className="flex items-center gap-1">
                <Mail size={10} />
                {v.email}
              </span>
            </div>
          </div>
        </div>
      ),
    },
    {
      header: "Users",
      accessor: (v: VendorItem) => (
        <span
          className="inline-flex items-center gap-1 px-2 py-0.5 rounded border text-xs font-medium bg-purple-500/10 text-purple-400 border-purple-500/20"
          title="Team members + customers + developers + support under this vendor"
        >
          <UsersIcon size={10} />
          {v.team_count}
        </span>
      ),
    },
    {
      header: "Domains",
      accessor: (v: VendorItem) => (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded border text-xs font-medium bg-blue-500/10 text-blue-400 border-blue-500/20">
          <Globe size={10} />
          {v.domain_count}
        </span>
      ),
    },
    {
      header: "Package",
      accessor: (v: VendorItem) => (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded border text-xs font-medium bg-cyan-500/10 text-cyan-400 border-cyan-500/20" title="Hosting package">
          <PackageIcon size={10} />
          {v.package_name || "—"}
        </span>
      ),
    },
    {
      header: "Storage",
      accessor: (v: VendorItem) => (
        <span className="inline-flex items-center gap-1 text-xs text-panel-muted" title="Disk usage under /home (cached 5 min)">
          <HardDrive size={10} />
          {humanBytes(v.storage_bytes)}
        </span>
      ),
    },
    {
      header: "Status",
      accessor: (v: VendorItem) => <StatusBadge status={v.status} />,
    },
    {
      header: "Last Login",
      accessor: (v: VendorItem) => (
        <span className="text-panel-muted text-sm">{v.lastLogin || "Never"}</span>
      ),
    },
    {
      header: "Actions",
      accessor: (v: VendorItem) => (
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => handleView(v.id)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
            title="View details"
          >
            <Eye size={14} />
          </button>
          <button
            type="button"
            onClick={() => openPasswordModal(v)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-amber-400 transition-colors"
            title="Reset password"
          >
            <KeyRound size={14} />
          </button>
          <button
            type="button"
            onClick={() => handleImpersonate(v)}
            disabled={v.status !== "active"}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-purple-400 transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
            title={v.status === "active" ? "Log in as this vendor (15 min impersonation)" : "Cannot impersonate a suspended vendor"}
          >
            <LogIn size={14} />
          </button>
          <button
            type="button"
            onClick={() => openPackageModal(v)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-cyan-400 transition-colors"
            title="Change package"
          >
            <PackageIcon size={14} />
          </button>
          {v.status === "active" ? (
            <button
              type="button"
              onClick={() => handleSuspend(v.id, v.name)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors"
              title="Suspend (locks linux account + stops apps)"
            >
              <Power size={14} />
            </button>
          ) : (
            <button
              type="button"
              onClick={() => handleActivate(v.id, v.name)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
              title="Unsuspend"
            >
              <Power size={14} />
            </button>
          )}
          <button
            type="button"
            onClick={() => handleTrash(v)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
            title="Move to trash (15-day recovery window)"
          >
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  const statCards = [
    { label: "Total Vendors", value: stats.total_vendors, color: "text-blue-400" },
    { label: "Active Vendors", value: stats.active_vendors, color: "text-green-400" },
    { label: "Team Members", value: stats.total_team_members, color: "text-purple-400", hint: "Users with role = vendor_staff across all active vendors" },
    { label: "Managed Users", value: stats.total_managed_users, color: "text-amber-400", hint: "All tenant users (staff + customers + developers + support) under active vendors" },
    { label: "Total Domains", value: stats.total_domains, color: "text-cyan-400", hint: "Platform-wide domain count" },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Vendors</h1>
          <p className="text-panel-muted text-sm mt-1">
            Reseller accounts and their tenants
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={refresh}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => setShowCreate(true)}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Create Vendor
          </Button>
        </div>
      </div>

      {/* Stats — 5 cards, tooltip on hover spells out what each count
          includes so the header card and per-row "Users" badge don't
          look contradictory. */}
      <div className="grid grid-cols-2 sm:grid-cols-5 gap-4">
        {statCards.map((s) => (
          <Card key={s.label}>
            <div className="p-4 text-center" title={s.hint}>
              <p className={`text-2xl font-bold ${s.color}`}>{s.value}</p>
              <p className="text-xs text-panel-muted mt-1">{s.label}</p>
            </div>
          </Card>
        ))}
      </div>

      {/* Tab switcher — Active vs Trash. Active count comes from the
          header stat; trash count refreshes on demand. */}
      <div className="flex items-center gap-1 border-b border-panel-border">
        <button
          type="button"
          onClick={() => setTab("active")}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
            tab === "active"
              ? "border-blue-500 text-panel-text"
              : "border-transparent text-panel-muted hover:text-panel-text"
          }`}
        >
          <span className="inline-flex items-center gap-2">
            <Building2 size={14} /> Active
            <span className="text-xs px-1.5 py-0.5 rounded bg-panel-bg border border-panel-border text-panel-muted font-mono">
              {stats.active_vendors}
            </span>
          </span>
        </button>
        <button
          type="button"
          onClick={() => setTab("trash")}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
            tab === "trash"
              ? "border-red-500 text-panel-text"
              : "border-transparent text-panel-muted hover:text-panel-text"
          }`}
        >
          <span className="inline-flex items-center gap-2">
            <Trash2 size={14} /> Trash
            {trashed.length > 0 && (
              <span className="text-xs px-1.5 py-0.5 rounded bg-red-500/10 border border-red-500/20 text-red-300 font-mono">
                {trashed.length}
              </span>
            )}
          </span>
        </button>
      </div>

      {tab === "active" && (
        <>
          {/* Search */}
          <Card>
            <div className="p-4 flex items-center gap-4 flex-wrap">
              <div className="relative flex-1 min-w-[200px]">
                <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
                <input
                  type="text"
                  placeholder="Search vendors by name, username, or email..."
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
                    <div key={i} className="h-14 bg-panel-border/20 rounded animate-pulse" />
                  ))}
                </div>
              </div>
            ) : filteredVendors.length > 0 ? (
              <Table columns={columns} data={filtered}
                page={pg.page} limit={pg.limit} total={pg.total}
                onPageChange={pg.setPage} onLimitChange={pg.setLimit} />
            ) : (
              <div className="text-center py-16 px-4">
                <Building2 size={48} className="text-panel-muted/20 mx-auto mb-4" />
                <h3 className="text-lg font-medium text-panel-text mb-1">No vendors found</h3>
                <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
                  {search
                    ? "No vendors match your search."
                    : "Create reseller accounts to delegate hosting management to external partners."}
                </p>
                {!search && (
                  <Button
                    onClick={() => setShowCreate(true)}
                    className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
                  >
                    <Plus size={14} />
                    Create Vendor
                  </Button>
                )}
              </div>
            )}
          </Card>
        </>
      )}

      {tab === "trash" && (
        <Card>
          <div className="p-4 border-b border-panel-border flex items-center gap-2 text-xs text-amber-300 bg-amber-500/5">
            <AlertCircle size={14} />
            Soft-deleted vendors. Records are permanently removed 15 days after deletion. Restore brings the linux account + DB row back; services stay stopped until you start them individually.
          </div>
          {trashLoading ? (
            <div className="p-8">
              <div className="space-y-3">
                {[1, 2, 3].map((i) => (
                  <div key={i} className="h-14 bg-panel-border/20 rounded animate-pulse" />
                ))}
              </div>
            </div>
          ) : trashed.length === 0 ? (
            <div className="text-center py-16 px-4">
              <Trash2 size={48} className="text-panel-muted/20 mx-auto mb-4" />
              <h3 className="text-lg font-medium text-panel-text mb-1">Trash is empty</h3>
              <p className="text-panel-muted text-sm">Soft-deleted vendors will appear here for 15 days before permanent deletion.</p>
            </div>
          ) : (
            <div className="divide-y divide-panel-border">
              {trashed.map((v) => (
                <div key={v.id} className="flex items-center justify-between p-4 gap-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-panel-text">{v.name}</span>
                      <span className="text-xs text-panel-muted">· {v.username}</span>
                    </div>
                    <div className="text-xs text-panel-muted mt-0.5 flex items-center gap-3 flex-wrap">
                      <span>Deleted: <span className="text-panel-text">{v.deleted_at}</span></span>
                      <span>Auto-purge: <span className="text-amber-300">{v.trash_expires_at}</span></span>
                    </div>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <button
                      type="button"
                      onClick={() => handleRestore(v)}
                      className="flex items-center gap-1.5 px-3 py-1.5 bg-green-600 hover:bg-green-700 text-white rounded-lg text-xs font-medium"
                    >
                      <RotateCcw size={12} /> Restore
                    </button>
                    <button
                      type="button"
                      onClick={() => handleDelete(v.id, v.name)}
                      className="flex items-center gap-1.5 px-3 py-1.5 bg-red-500/10 hover:bg-red-500/20 text-red-300 border border-red-500/30 rounded-lg text-xs"
                      title="Delete permanently without waiting 15 days"
                    >
                      <Trash2 size={12} /> Delete now
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </Card>
      )}

      {/* Create Modal */}
      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Create Vendor Account">
        <form onSubmit={handleCreate} className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Full Name *</label>
              <input type="text" required placeholder="Jane Doe" value={form.name}
                onChange={(e) => handleNameChange(e.target.value)} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Username *</label>
              <input type="text" required placeholder="janedoe" value={form.username}
                onChange={(e) => setForm({ ...form, username: e.target.value.toLowerCase().replace(/[^a-z0-9]/g, "").slice(0, 16) })} className={inputClass} />
              <p className="text-xs text-panel-muted mt-1">System account & prefix (3-16 chars, a-z, 0-9)</p>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Email *</label>
              <input type="email" required placeholder="jane@example.com" value={form.email}
                onChange={(e) => setForm({ ...form, email: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Password *</label>
              <PasswordInput required minLength={8} placeholder="Min. 8 characters" value={form.password}
                onChange={(v) => setForm({ ...form, password: v })} inputClassName={inputClass} />
            </div>
          </div>
          <div>
            <label className={labelClass}>Hosting Package *</label>
            <select required value={form.package_id}
              onChange={(e) => setForm({ ...form, package_id: e.target.value })} className={inputClass}>
              <option value="">Select package...</option>
              {packages.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}{p.is_default ? " (Default)" : ""}
                </option>
              ))}
            </select>
            {packages.length === 0 && (
              <p className="text-xs text-amber-400 mt-1">No packages found. Create a package first in the Packages page.</p>
            )}
          </div>
          <div>
            <label className={labelClass}>Primary Domain (optional)</label>
            <input
              type="text"
              value={form.primary_domain}
              onChange={(e) => setForm({ ...form, primary_domain: e.target.value.trim().toLowerCase() })}
              placeholder="example.com"
              className={inputClass}
            />
            <p className="text-xs text-panel-muted mt-1">
              If set, a full hosting stack (vhost, PHP-FPM, DNS zone, SSL, admin@ mailbox, FTP) is provisioned automatically.
            </p>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Creating..." : "Create Vendor"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Reset password — owner-only. The panel never reads the old
          password; bcrypt-hashed replacement is sent as plaintext over
          the already-HTTPS API, then stored hashed. */}
      <Modal
        isOpen={!!pwdTarget}
        onClose={() => { if (!pwdSaving) { setPwdTarget(null); setPwdValue(""); } }}
        title={pwdTarget ? `Reset password — ${pwdTarget.name}` : "Reset password"}
      >
        {pwdTarget && (
          <div className="space-y-4">
            <p className="text-xs text-panel-muted">
              Setting a new password takes effect immediately. The vendor's current session is not forcibly logged out — they'll stay signed in until their access token expires (15 min by default) or they log out.
            </p>
            <div>
              <label className={labelClass}>New password *</label>
              <PasswordInput
                autoFocus
                value={pwdValue}
                onChange={setPwdValue}
                placeholder="Min. 8 characters"
                minLength={8}
                inputClassName={inputClass}
                onKeyDown={(e) => { if (e.key === "Enter" && pwdValue.length >= 8 && !pwdSaving) savePassword(); }}
              />
              <p className="text-xs text-panel-muted mt-1">
                Strong password recommended — this account can spend quota, deploy code, and manage customer domains.
              </p>
            </div>
            <div className="flex justify-end gap-3 pt-2">
              <button type="button" onClick={() => { setPwdTarget(null); setPwdValue(""); }}
                disabled={pwdSaving}
                className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg disabled:opacity-50">
                Cancel
              </button>
              <button type="button" onClick={savePassword}
                disabled={pwdSaving || pwdValue.length < 8}
                className="px-4 py-2 text-sm bg-amber-600 hover:bg-amber-700 text-white rounded-lg font-medium disabled:opacity-50">
                {pwdSaving ? "Saving…" : "Update password"}
              </button>
            </div>
          </div>
        )}
      </Modal>

      {/* Change hosting package. Doesn't migrate existing domains' per-
          domain quotas — new limits take effect on the next create /
          resize. Admins who need to retroactively apply must re-save
          each domain from the Domains page. */}
      <Modal
        isOpen={!!pkgTarget}
        onClose={() => { if (!pkgSaving) setPkgTarget(null); }}
        title={pkgTarget ? `Change package — ${pkgTarget.name}` : "Change package"}
      >
        {pkgTarget && (
          <div className="space-y-4">
            <p className="text-xs text-panel-muted">
              Current package: <code className="font-mono text-panel-text">{pkgTarget.package_name || "(none)"}</code>. Switching updates future domain provisions; existing domains keep their current quotas until individually resized.
            </p>
            <div>
              <label className={labelClass}>Hosting package *</label>
              <select value={pkgChoice}
                onChange={(e) => setPkgChoice(e.target.value)}
                className={inputClass}>
                <option value="">Select package...</option>
                {packages.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}{p.is_default ? " (Default)" : ""}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex justify-end gap-3 pt-2">
              <button type="button" onClick={() => setPkgTarget(null)} disabled={pkgSaving}
                className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg disabled:opacity-50">
                Cancel
              </button>
              <button type="button" onClick={savePackage}
                disabled={pkgSaving || !pkgChoice || pkgChoice === pkgTarget.package_id}
                className="px-4 py-2 text-sm bg-cyan-600 hover:bg-cyan-700 text-white rounded-lg font-medium disabled:opacity-50">
                {pkgSaving ? "Saving…" : "Update package"}
              </button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
