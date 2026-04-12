import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Building2, Plus, RefreshCw, Search, Trash2, Eye, Power,
  User, Mail, Users as UsersIcon, Globe,
} from "lucide-react";

interface VendorItem {
  id: string;
  username: string;
  name: string;
  email: string;
  status: "active" | "suspended";
  team_count: number;
  domain_count: number;
  createdAt: string;
  lastLogin: string;
}

interface VendorStats {
  total_vendors: number;
  active_vendors: number;
  total_team_members: number;
  total_managed_users: number;
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
  });
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [page] = useState(1);
  const [limit] = useState(50);

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

  useEffect(() => {
    fetchVendors();
    fetchStats();
    fetchPackages();
  }, []);

  const fetchVendors = async () => {
    setLoading(true);
    try {
      const res = await api.get("/admin/vendors", { params: { page, limit } });
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
    if (!confirm(`Suspend vendor "${name}"?`)) return;
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

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Delete vendor "${name}"? This removes the tenant, system account, and all associated files.`)) return;
    try {
      await api.delete(`/users/${id}`);
      toast.success(`Vendor ${name} deleted`);
      refresh();
    } catch {
      toast.error("Failed to delete vendor");
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

  const filtered = vendors.filter((v) => {
    const q = search.toLowerCase();
    return (
      v.name.toLowerCase().includes(q) ||
      v.email.toLowerCase().includes(q) ||
      (v.username || "").toLowerCase().includes(q)
    );
  });

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
      header: "Team",
      accessor: (v: VendorItem) => (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded border text-xs font-medium bg-purple-500/10 text-purple-400 border-purple-500/20">
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
      header: "Status",
      accessor: (v: VendorItem) => <StatusBadge status={v.status} />,
    },
    {
      header: "Created",
      accessor: (v: VendorItem) => (
        <span className="text-panel-muted text-sm">{v.createdAt}</span>
      ),
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
            title="View"
          >
            <Eye size={14} />
          </button>
          {v.status === "active" ? (
            <button
              type="button"
              onClick={() => handleSuspend(v.id, v.name)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors"
              title="Suspend"
            >
              <Power size={14} />
            </button>
          ) : (
            <button
              type="button"
              onClick={() => handleActivate(v.id, v.name)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
              title="Activate"
            >
              <Power size={14} />
            </button>
          )}
          <button
            type="button"
            onClick={() => handleDelete(v.id, v.name)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
            title="Delete"
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
    { label: "Team Members", value: stats.total_team_members, color: "text-purple-400" },
    { label: "Managed Users", value: stats.total_managed_users, color: "text-amber-400" },
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

      {/* Stats */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
        {statCards.map((s) => (
          <Card key={s.label}>
            <div className="p-4 text-center">
              <p className={`text-2xl font-bold ${s.color}`}>{s.value}</p>
              <p className="text-xs text-panel-muted mt-1">{s.label}</p>
            </div>
          </Card>
        ))}
      </div>

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
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={filtered} />
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
              <input type="password" required minLength={8} placeholder="Min. 8 characters" value={form.password}
                onChange={(e) => setForm({ ...form, password: e.target.value })} className={inputClass} />
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
    </div>
  );
}
