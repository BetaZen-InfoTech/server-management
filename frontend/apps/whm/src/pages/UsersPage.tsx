import { useState, useEffect } from "react";
import { Link } from "react-router-dom";
import { Card, Button, Table, StatusBadge, Modal, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import { useAuthStore } from "@/store/auth";
import toast from "react-hot-toast";
import { Users, Plus, RefreshCw, Search, Trash2, Edit, Shield, Mail, User, KeyRound, Power, Info, Building2 } from "lucide-react";

interface UserItem {
  id: string;
  username: string;
  name: string;
  email: string;
  role: "admin" | "vendor" | "staff" | "operator" | "viewer";
  package_name?: string;
  status: "active" | "suspended" | "pending";
  createdAt: string;
  lastLogin: string;
}

const roleColors: Record<string, string> = {
  admin: "bg-red-500/10 text-red-400 border-red-500/20",
  vendor: "bg-blue-500/10 text-blue-400 border-blue-500/20",
  staff: "bg-purple-500/10 text-purple-400 border-purple-500/20",
  operator: "bg-green-500/10 text-green-400 border-green-500/20",
  viewer: "bg-gray-500/10 text-gray-400 border-gray-500/20",
};

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

export default function UsersPage() {
  const { user: currentUser } = useAuthStore();
  const [users, setUsers] = useState<UserItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [roleFilter, setRoleFilter] = useState<string>("all");
  const [showInvite, setShowInvite] = useState(false);
  const [creating, setCreating] = useState(false);
  // This page creates panel team members (admin / staff / operator / viewer).
  // Vendor accounts are separate — they're full hosting customers with
  // /home/<vendor>/ trees and hosting packages, and live under the dedicated
  // Vendors page at /whm/vendors. Keeping creation flows separated prevents
  // admins from accidentally creating a vendor from a team-member form (and
  // ending up with a half-provisioned hosting account).
  const [form, setForm] = useState({ username: "", name: "", email: "", password: "", role: "viewer" });
  // Packages are still needed in the Edit modal — an admin can move a
  // team member's hosting package without sending them through the
  // Vendor flow. Create-form intentionally omits the package field;
  // new team members don't own hosting assets.
  const [packages, setPackages] = useState<{ id: string; name: string; is_default?: boolean }[]>([]);

  // Edit modal state
  const [editing, setEditing] = useState<UserItem | null>(null);
  const [editForm, setEditForm] = useState({ name: "", email: "", role: "viewer", package_id: "", is_active: true });
  const [savingEdit, setSavingEdit] = useState(false);

  // Reset password modal state
  const [resetting, setResetting] = useState<UserItem | null>(null);
  const [resetPwd, setResetPwd] = useState("");
  const [savingReset, setSavingReset] = useState(false);

  useEffect(() => {
    fetchUsers();
    fetchPackages();
  }, []);

  const fetchPackages = async () => {
    try {
      const res = await api.get("/packages");
      setPackages(res.data.data || []);
    } catch { /* keep empty */ }
  };

  const fetchUsers = async () => {
    setLoading(true);
    try {
      const res = await api.get("/users");
      setUsers(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  // Username is optional — the backend auto-derives it from the email
  // local-part when blank, which is what most operators want anyway. We
  // deliberately DO NOT auto-fill it from the name field: pre-filling
  // then silently de-syncing from the email led to usernames that didn't
  // match any expected pattern for the account holder.
  const handleNameChange = (value: string) => {
    setForm((prev) => ({ ...prev, name: value }));
  };

  const handleInvite = async (e: React.FormEvent) => {
    e.preventDefault();
    // Username is optional for team roles — backend auto-derives it from
    // the email local-part (or name) when blank. Only name/email/password
    // are truly required here. If the operator DID type a username we
    // still validate the panel-login format so they get a clear message
    // instead of a 400 from the server.
    if (!form.name || !form.email || !form.password) {
      toast.error("Please fill all required fields");
      return;
    }
    if (form.username && !/^[a-z][a-z0-9_.\-]{2,31}$/.test(form.username)) {
      toast.error("Username must be 3-32 chars, start with a letter, and use only lowercase letters, digits, dot, dash, or underscore");
      return;
    }
    if (form.role === "vendor") {
      toast.error("Create vendor accounts from Vendors → Create Vendor");
      return;
    }
    setCreating(true);
    try {
      await api.post("/users", form);
      toast.success(`User ${form.name} created`);
      setShowInvite(false);
      setForm({ username: "", name: "", email: "", password: "", role: "viewer" });
      fetchUsers();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create user");
    } finally {
      setCreating(false);
    }
  };

  const handleSuspend = async (id: string, name: string) => {
    if (!await confirmAction({ title: "Suspend?", description: `Are you sure you want to suspend user "${name}"?`, danger: true, confirmLabel: "Suspend" })) return;
    try {
      await api.post(`/users/${id}/suspend`);
      toast.success(`User ${name} suspended`);
      fetchUsers();
    } catch {
      toast.error("Failed to suspend user");
    }
  };

  const handleActivate = async (id: string, name: string) => {
    try {
      await api.post(`/users/${id}/activate`);
      toast.success(`User ${name} activated`);
      fetchUsers();
    } catch {
      toast.error("Failed to activate user");
    }
  };

  const openEdit = async (u: UserItem) => {
    // Pre-fill from row, then refresh from /users/:id so package_id is current.
    setEditing(u);
    setEditForm({
      name: u.name,
      email: u.email,
      role: u.role,
      package_id: u.package_name ? "" : "",
      is_active: u.status === "active",
    });
    try {
      const res = await api.get(`/users/${u.id}`);
      const d = res.data.data || {};
      setEditForm((f) => ({
        ...f,
        name: d.name ?? f.name,
        email: d.email ?? f.email,
        role: d.role ?? f.role,
        package_id: d.package_id || "",
        is_active: (d.status ?? "active") === "active",
      }));
    } catch { /* fall back to row data */ }
  };

  const handleSaveEdit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editing) return;
    setSavingEdit(true);
    try {
      await api.put(`/users/${editing.id}`, {
        name: editForm.name,
        email: editForm.email,
        role: editForm.role,
        package_id: editForm.package_id || undefined,
        is_active: editForm.is_active,
      });
      toast.success(`User ${editForm.name} updated`);
      setEditing(null);
      fetchUsers();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update user");
    } finally {
      setSavingEdit(false);
    }
  };

  const openReset = (u: UserItem) => {
    setResetting(u);
    setResetPwd("");
  };

  const handleResetPassword = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!resetting) return;
    if (resetPwd.length < 8) {
      toast.error("Password must be at least 8 characters");
      return;
    }
    setSavingReset(true);
    try {
      await api.post(`/users/${resetting.id}/reset-password`, { password: resetPwd });
      toast.success(`Password reset for ${resetting.name}`);
      setResetting(null);
      setResetPwd("");
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to reset password");
    } finally {
      setSavingReset(false);
    }
  };

  const handleDelete = async (id: string, name: string) => {
    if (id === currentUser?.id) {
      toast.error("You cannot delete your own account");
      return;
    }
    if (!await confirmAction({ title: "Delete?", description: `Are you sure you want to delete user "${name}"? This will remove the system account and all associated files.`, danger: true, confirmLabel: "Delete" })) return;
    try {
      await api.delete(`/users/${id}`);
      toast.success(`User ${name} deleted`);
      fetchUsers();
    } catch {
      toast.error("Failed to delete user");
    }
  };

  const filtered = users.filter((u) => {
    const matchesSearch =
      u.name.toLowerCase().includes(search.toLowerCase()) ||
      u.email.toLowerCase().includes(search.toLowerCase()) ||
      (u.username || "").toLowerCase().includes(search.toLowerCase());
    const matchesRole = roleFilter === "all" || u.role === roleFilter;
    return matchesSearch && matchesRole;
  });

  const roles = ["all", "admin", "vendor", "staff", "operator", "viewer"];
  const isRestrictedCreator = currentUser?.role === "vendor" || currentUser?.role === "staff";

  const columns = [
    {
      header: "User",
      accessor: (u: UserItem) => (
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-full bg-blue-500/10 border border-blue-500/20 flex items-center justify-center shrink-0">
            <span className="text-blue-400 text-xs font-bold">
              {u.name.charAt(0).toUpperCase()}
            </span>
          </div>
          <div>
            <span className="font-medium text-panel-text block">{u.name}</span>
            <div className="flex items-center gap-2 text-xs text-panel-muted">
              <span className="flex items-center gap-1">
                <User size={10} />
                {u.username}
              </span>
              <span className="flex items-center gap-1">
                <Mail size={10} />
                {u.email}
              </span>
            </div>
          </div>
        </div>
      ),
    },
    {
      header: "Role",
      accessor: (u: UserItem) => (
        <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded border text-xs font-medium capitalize ${
          roleColors[u.role] || "bg-panel-bg text-panel-muted border-panel-border"
        }`}>
          <Shield size={10} />
          {u.role}
        </span>
      ),
    },
    {
      header: "Status",
      accessor: (u: UserItem) => <StatusBadge status={u.status} />,
    },
    {
      header: "Created",
      accessor: (u: UserItem) => (
        <span className="text-panel-muted text-sm">{u.createdAt}</span>
      ),
    },
    {
      header: "Last Login",
      accessor: (u: UserItem) => (
        <span className="text-panel-muted text-sm">{u.lastLogin || "Never"}</span>
      ),
    },
    {
      header: "Actions",
      accessor: (u: UserItem) => (
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => openEdit(u)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
            title="Edit user"
          >
            <Edit size={14} />
          </button>
          <button
            type="button"
            onClick={() => openReset(u)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-purple-400 transition-colors"
            title="Reset password"
          >
            <KeyRound size={14} />
          </button>
          {u.status === "active" ? (
            <button
              type="button"
              onClick={() => handleSuspend(u.id, u.name)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors"
              title="Suspend"
            >
              <Power size={14} />
            </button>
          ) : (
            <button
              type="button"
              onClick={() => handleActivate(u.id, u.name)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
              title="Activate"
            >
              <Power size={14} />
            </button>
          )}
          <button
            type="button"
            onClick={() => handleDelete(u.id, u.name)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
            title={u.id === currentUser?.id ? "You cannot delete your own account" : "Delete"}
            disabled={!!currentUser?.id && u.id === currentUser.id}
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
          <h1 className="text-xl font-bold text-panel-text">Users & Accounts</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage user accounts, roles, and system access
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchUsers}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => setShowInvite(true)}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Create User
          </Button>
        </div>
      </div>

      {/* Filters */}
      <Card>
        <div className="p-4 flex items-center gap-4 flex-wrap">
          <div className="relative flex-1 min-w-[200px]">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search by name, username, or email..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
          <div className="flex items-center gap-1">
            <span className="text-sm text-panel-muted mr-1">Role:</span>
            {roles.map((role) => (
              <button
                key={role}
                onClick={() => setRoleFilter(role)}
                className={`px-3 py-1.5 rounded-lg text-xs font-medium capitalize transition-colors ${
                  roleFilter === role
                    ? "bg-blue-600 text-white"
                    : "bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border"
                }`}
              >
                {role}
              </button>
            ))}
          </div>
        </div>
      </Card>

      {/* Role Summary Cards */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
        {[
          { role: "admin", label: "Admins", color: "text-red-400" },
          { role: "vendor", label: "Vendors", color: "text-blue-400" },
          { role: "operator", label: "Operators", color: "text-green-400" },
          { role: "viewer", label: "Viewers", color: "text-gray-400" },
        ].map(({ role, label, color }) => (
          <Card key={role}>
            <div className="p-4 text-center">
              <p className={`text-2xl font-bold ${color}`}>
                {users.filter((u) => u.role === role).length}
              </p>
              <p className="text-xs text-panel-muted mt-1">{label}</p>
            </div>
          </Card>
        ))}
      </div>

      {/* Users Table */}
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
            <Users size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No users found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search || roleFilter !== "all"
                ? "No users match your current filters. Try adjusting the search or role filter."
                : "Create user accounts to manage domains, databases, and email."}
            </p>
            {!search && roleFilter === "all" && (
              <Button
                onClick={() => setShowInvite(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Create User
              </Button>
            )}
          </div>
        )}
      </Card>

      <Modal isOpen={showInvite} onClose={() => setShowInvite(false)} title="Create User Account">
        <form onSubmit={handleInvite} className="space-y-4">
          {/* Separation of concerns: this form creates panel team members
              (admin / staff / operator / viewer). Vendor (hosting customer)
              creation needs a package, primary domain, DNS + mail + FTP
              provisioning, and lives on the dedicated Vendors page. */}
          <div className="flex items-start gap-2 p-3 rounded-lg bg-blue-500/5 border border-blue-500/20 text-xs">
            <Info size={14} className="text-blue-400 mt-0.5 shrink-0" />
            <div className="text-panel-muted">
              This creates a <span className="text-panel-text font-medium">panel team member</span> (admin, staff, operator, or viewer) — no hosting account, no domain. To onboard a hosting customer, use{" "}
              <Link to="/whm/vendors" className="text-blue-400 hover:underline inline-flex items-center gap-1">
                <Building2 size={11} /> Vendors → Create Vendor
              </Link>
              .
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Full Name *</label>
              <input type="text" required placeholder="John Doe" value={form.name}
                onChange={(e) => handleNameChange(e.target.value)} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>
                Username <span className="text-panel-muted text-xs font-normal">(optional)</span>
              </label>
              <input type="text" placeholder="auto-generated from email" value={form.username}
                onChange={(e) => setForm({ ...form, username: e.target.value.toLowerCase().replace(/[^a-z0-9_.\-]/g, "").slice(0, 32) })} className={inputClass} />
              <p className="text-xs text-panel-muted mt-1">
                Login identifier. Leave blank to derive from email. No /home/ directory is created for team accounts.
              </p>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Email *</label>
              <input type="email" required placeholder="john@example.com" value={form.email}
                onChange={(e) => setForm({ ...form, email: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Password *</label>
              <input type="password" required minLength={8} placeholder="Min. 8 characters" value={form.password}
                onChange={(e) => setForm({ ...form, password: e.target.value })} className={inputClass} />
            </div>
          </div>
          <div>
            <label className={labelClass}>Role *</label>
            <div className="grid grid-cols-2 gap-2">
              {/* Vendor role is intentionally omitted — vendors are
                  provisioned via the dedicated Vendors page (package +
                  primary domain + DNS/mail/FTP). Listing it here would
                  give a half-working flow that silently fails or creates
                  a vendor with no package. */}
              {([
                { value: "viewer", label: "Viewer", desc: "Read-only access" },
                { value: "operator", label: "Operator", desc: "Manage services" },
                { value: "staff", label: "Staff", desc: "Team member" },
                { value: "admin", label: "Admin", desc: "Full admin access" },
              ]).filter((r) => !isRestrictedCreator || r.value !== "admin").map((r) => (
                <button key={r.value} type="button" onClick={() => setForm({ ...form, role: r.value })}
                  className={`p-2.5 rounded-lg text-left transition-colors ${
                    form.role === r.value
                      ? "bg-blue-600/10 border-2 border-blue-500"
                      : "bg-panel-bg border border-panel-border hover:border-panel-muted"
                  }`}>
                  <p className={`text-sm font-medium ${form.role === r.value ? "text-blue-400" : "text-panel-text"}`}>{r.label}</p>
                  <p className="text-xs text-panel-muted">{r.desc}</p>
                </button>
              ))}
            </div>
            <p className="text-[11px] text-panel-muted mt-2">
              Need a hosting customer with a package + domain? Use <Link to="/whm/vendors" className="text-blue-400 hover:underline">Vendors</Link> instead.
            </p>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowInvite(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Creating..." : "Create User"}
            </button>
          </div>
        </form>
      </Modal>

      {/* ---------- Edit Modal ---------- */}
      <Modal isOpen={!!editing} onClose={() => setEditing(null)} title={editing ? `Edit ${editing.name}` : ""}>
        {editing && (
          <form onSubmit={handleSaveEdit} className="space-y-4">
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className={labelClass}>Full Name</label>
                <input type="text" required value={editForm.name}
                  onChange={(e) => setEditForm({ ...editForm, name: e.target.value })} className={inputClass} />
              </div>
              <div>
                <label className={labelClass}>Username</label>
                <input type="text" disabled value={editing.username || "—"} className={inputClass + " opacity-60"} />
              </div>
            </div>
            <div>
              <label className={labelClass}>Email</label>
              <input type="email" required value={editForm.email}
                onChange={(e) => setEditForm({ ...editForm, email: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Role</label>
              <div className="grid grid-cols-2 gap-2">
                {/* Match the Create form: team-member roles only. Changing
                    someone into a Vendor needs package + domain setup and
                    lives on the Vendors page. */}
                {([
                  { value: "viewer", label: "Viewer" },
                  { value: "operator", label: "Operator" },
                  { value: "staff", label: "Staff" },
                  { value: "admin", label: "Admin" },
                ]).map((r) => (
                  <button key={r.value} type="button" onClick={() => setEditForm({ ...editForm, role: r.value })}
                    className={`p-2 rounded-lg text-sm font-medium transition-colors ${
                      editForm.role === r.value
                        ? "bg-blue-600/10 border-2 border-blue-500 text-blue-400"
                        : "bg-panel-bg border border-panel-border text-panel-text hover:border-panel-muted"
                    }`}>
                    {r.label}
                  </button>
                ))}
              </div>
            </div>
            <div>
              <label className={labelClass}>Hosting Package</label>
              <select value={editForm.package_id}
                onChange={(e) => setEditForm({ ...editForm, package_id: e.target.value })} className={inputClass}>
                <option value="">— keep current —</option>
                {packages.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}{p.is_default ? " (Default)" : ""}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className="flex items-center gap-2 text-sm text-panel-text cursor-pointer">
                <input type="checkbox" checked={editForm.is_active}
                  onChange={(e) => setEditForm({ ...editForm, is_active: e.target.checked })} />
                Account active
              </label>
            </div>
            <div className="flex justify-end gap-3 pt-2">
              <button type="button" onClick={() => setEditing(null)}
                className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg">Cancel</button>
              <button type="submit" disabled={savingEdit}
                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium disabled:opacity-50">
                {savingEdit ? "Saving..." : "Save changes"}
              </button>
            </div>
          </form>
        )}
      </Modal>

      {/* ---------- Reset Password Modal ---------- */}
      <Modal isOpen={!!resetting} onClose={() => { setResetting(null); setResetPwd(""); }}
        title={resetting ? `Reset password — ${resetting.name}` : ""}>
        {resetting && (
          <form onSubmit={handleResetPassword} className="space-y-4">
            <p className="text-sm text-panel-muted">
              Sets a new password for this user. The Linux account password (used for SSH/FTP) is updated to match.
            </p>
            <div>
              <label className={labelClass}>New password *</label>
              <input type="text" required minLength={8} autoFocus value={resetPwd}
                onChange={(e) => setResetPwd(e.target.value)} placeholder="Min. 8 characters" className={inputClass} />
              <p className="text-xs text-panel-muted mt-1">Shown in plaintext so you can copy and share it.</p>
            </div>
            <div className="flex justify-end gap-3 pt-2">
              <button type="button" onClick={() => { setResetting(null); setResetPwd(""); }}
                className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg">Cancel</button>
              <button type="submit" disabled={savingReset || resetPwd.length < 8}
                className="px-4 py-2 text-sm bg-purple-600 hover:bg-purple-700 text-white rounded-lg font-medium disabled:opacity-50">
                {savingReset ? "Resetting..." : "Reset password"}
              </button>
            </div>
          </form>
        )}
      </Modal>
    </div>
  );
}
