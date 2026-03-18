import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import { useAuthStore } from "@/store/auth";
import toast from "react-hot-toast";
import { Users, Plus, RefreshCw, Search, Trash2, Edit, Shield, Mail, User } from "lucide-react";

interface UserItem {
  id: string;
  username: string;
  name: string;
  email: string;
  role: "admin" | "vendor" | "operator" | "viewer";
  status: "active" | "suspended" | "pending";
  createdAt: string;
  lastLogin: string;
}

const roleColors: Record<string, string> = {
  admin: "bg-red-500/10 text-red-400 border-red-500/20",
  vendor: "bg-blue-500/10 text-blue-400 border-blue-500/20",
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
  const [form, setForm] = useState({ username: "", name: "", email: "", password: "", role: "viewer" });

  useEffect(() => {
    fetchUsers();
  }, []);

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

  // Auto-suggest username from name
  const handleNameChange = (value: string) => {
    setForm((prev) => ({
      ...prev,
      name: value,
      username: prev.username || value.replace(/[^a-z0-9]/gi, "").slice(0, 16).toLowerCase(),
    }));
  };

  const handleInvite = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.username || !form.name || !form.email || !form.password) {
      toast.error("Please fill all required fields");
      return;
    }
    if (!/^[a-z][a-z0-9]{2,15}$/.test(form.username)) {
      toast.error("Username must be 3-16 lowercase alphanumeric characters, starting with a letter");
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
    if (!confirm(`Are you sure you want to suspend user "${name}"?`)) return;
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

  const handleDelete = async (id: string, name: string) => {
    if (id === currentUser?.id) {
      toast.error("You cannot delete your own account");
      return;
    }
    if (!confirm(`Are you sure you want to delete user "${name}"? This will remove the system account and all associated files.`)) return;
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

  const roles = ["all", "admin", "vendor", "operator", "viewer"];

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
          <button className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors" title="Edit">
            <Edit size={14} />
          </button>
          {u.status === "active" ? (
            <button
              onClick={() => handleSuspend(u.id, u.name)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors"
              title="Suspend"
            >
              <Shield size={14} />
            </button>
          ) : (
            <button
              onClick={() => handleActivate(u.id, u.name)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
              title="Activate"
            >
              <Shield size={14} />
            </button>
          )}
          <button
            onClick={() => handleDelete(u.id, u.name)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
            title="Delete"
            disabled={u.id === currentUser?.id}
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
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Full Name *</label>
              <input type="text" required placeholder="John Doe" value={form.name}
                onChange={(e) => handleNameChange(e.target.value)} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Username *</label>
              <input type="text" required placeholder="johndoe" value={form.username}
                onChange={(e) => setForm({ ...form, username: e.target.value.toLowerCase().replace(/[^a-z0-9]/g, "").slice(0, 16) })} className={inputClass} />
              <p className="text-xs text-panel-muted mt-1">System account & prefix (3-16 chars, a-z, 0-9)</p>
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
              {([
                { value: "viewer", label: "Viewer", desc: "Read-only access" },
                { value: "operator", label: "Operator", desc: "Manage services" },
                { value: "vendor", label: "Vendor", desc: "Full management" },
                { value: "admin", label: "Admin", desc: "Full admin access" },
              ]).map((r) => (
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
    </div>
  );
}
