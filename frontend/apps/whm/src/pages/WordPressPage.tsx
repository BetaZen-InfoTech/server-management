import { useState, useEffect, useCallback } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Blocks, Plus, RefreshCw, Search, Trash2, ExternalLink, RotateCw, AlertTriangle, LogIn, Users, UserPlus, X, Settings } from "lucide-react";

interface WordPressSite {
  id: string;
  domain: string;
  user: string;
  path: string;
  version: string;
  site_url: string;
  admin_url: string;
  auto_update: boolean;
  maintenance_mode: boolean;
  created_at: string;
}

interface DomainItem {
  id: string;
  domain: string;
  status: string;
}

interface WPUser {
  ID: string;
  user_login: string;
  user_email: string;
  display_name: string;
  roles: string;
}

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const selectClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

const defaultForm = { site_title: "", domain: "", path: "", admin_email: "", admin_user: "admin", admin_pass: "", auto_update: true };

export default function WordPressPage() {
  const [sites, setSites] = useState<WordPressSite[]>([]);
  const [domains, setDomains] = useState<DomainItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState(defaultForm);
  const [conflict, setConflict] = useState<string | null>(null);
  const [checkingConflict, setCheckingConflict] = useState(false);
  const [showUsers, setShowUsers] = useState(false);
  const [selectedSite, setSelectedSite] = useState<WordPressSite | null>(null);
  const [wpUsers, setWpUsers] = useState<WPUser[]>([]);
  const [loadingUsers, setLoadingUsers] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [settingsSite, setSettingsSite] = useState<WordPressSite | null>(null);
  const [savingSetting, setSavingSetting] = useState<string | null>(null);
  const [showAddUser, setShowAddUser] = useState(false);
  const [userForm, setUserForm] = useState({ username: "", email: "", password: "", role: "editor" });
  const [creatingUser, setCreatingUser] = useState(false);

  useEffect(() => {
    fetchSites();
    fetchDomains();
  }, []);

  const fetchSites = async () => {
    setLoading(true);
    try {
      const res = await api.get("/wordpress");
      setSites(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const fetchDomains = async () => {
    try {
      const res = await api.get("/domains");
      setDomains((res.data.data || []).filter((d: DomainItem) => d.status === "active"));
    } catch {
      // Keep empty
    }
  };

  const checkConflict = useCallback(async (domain: string, path: string) => {
    if (!domain) { setConflict(null); return; }
    setCheckingConflict(true);
    try {
      const res = await api.get("/wordpress/check-conflict", { params: { domain, path } });
      const data = res.data.data;
      setConflict(data?.conflict ? data.message : null);
    } catch {
      setConflict(null);
    } finally {
      setCheckingConflict(false);
    }
  }, []);

  // Check conflict when domain or path changes
  useEffect(() => {
    const timer = setTimeout(() => {
      if (form.domain) checkConflict(form.domain, form.path);
      else setConflict(null);
    }, 300);
    return () => clearTimeout(timer);
  }, [form.domain, form.path, checkConflict]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.site_title || !form.domain || !form.admin_email || !form.admin_pass || !form.admin_user) {
      toast.error("Please fill all required fields");
      return;
    }
    if (conflict) {
      toast.error("Cannot install — a WordPress site already exists at this location");
      return;
    }
    setCreating(true);
    try {
      await api.post("/wordpress/install", form);
      toast.success(`WordPress installed on ${form.domain}`);
      setShowCreate(false);
      setForm(defaultForm);
      fetchSites();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to install WordPress");
    } finally {
      setCreating(false);
    }
  };

  const handleUpdate = async (id: string) => {
    try {
      await api.post(`/wordpress/${id}/update`);
      toast.success("WordPress update initiated");
      fetchSites();
    } catch {
      toast.error("Failed to update WordPress");
    }
  };

  const handleAutoLogin = async (site: WordPressSite) => {
    try {
      toast.loading("Generating login link...", { id: "auto-login" });
      const res = await api.post(`/wordpress/${site.id}/auto-login`);
      const url = res.data.data?.login_url;
      toast.dismiss("auto-login");
      if (url) {
        window.open(url, "_blank");
      } else {
        toast.error("Failed to get login URL");
      }
    } catch {
      toast.dismiss("auto-login");
      toast.error("Failed to auto-login");
    }
  };

  const openUsersModal = async (site: WordPressSite) => {
    setSelectedSite(site);
    setShowUsers(true);
    setLoadingUsers(true);
    try {
      const res = await api.get(`/wordpress/${site.id}/users`);
      setWpUsers(res.data.data || []);
    } catch {
      toast.error("Failed to load users");
    } finally {
      setLoadingUsers(false);
    }
  };

  const handleCreateUser = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedSite) return;
    setCreatingUser(true);
    try {
      await api.post(`/wordpress/${selectedSite.id}/users`, userForm);
      toast.success(`User "${userForm.username}" created`);
      setShowAddUser(false);
      setUserForm({ username: "", email: "", password: "", role: "editor" });
      // Refresh users
      const res = await api.get(`/wordpress/${selectedSite.id}/users`);
      setWpUsers(res.data.data || []);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create user");
    } finally {
      setCreatingUser(false);
    }
  };

  const handleDeleteUser = async (wpUserID: string, username: string) => {
    if (!selectedSite) return;
    if (!confirm(`Delete WordPress user "${username}"? Their content will be reassigned to the primary admin.`)) return;
    try {
      await api.delete(`/wordpress/${selectedSite.id}/users/${wpUserID}`);
      toast.success(`User "${username}" deleted`);
      setWpUsers(wpUsers.filter((u) => u.ID !== wpUserID));
    } catch {
      toast.error("Failed to delete user");
    }
  };

  const handleChangeRole = async (wpUserID: string, role: string) => {
    if (!selectedSite) return;
    try {
      await api.patch(`/wordpress/${selectedSite.id}/users/${wpUserID}`, { role });
      toast.success("Role updated");
      setWpUsers(wpUsers.map((u) => (u.ID === wpUserID ? { ...u, roles: role } : u)));
    } catch {
      toast.error("Failed to update role");
    }
  };

  const openSettings = (site: WordPressSite) => {
    setSettingsSite(site);
    setShowSettings(true);
  };

  const toggleAutoUpdate = async (enabled: boolean) => {
    if (!settingsSite) return;
    setSavingSetting("auto_update");
    try {
      await api.patch(`/wordpress/${settingsSite.id}/auto-update`, { enabled });
      toast.success(`Auto-update ${enabled ? "enabled" : "disabled"}`);
      const updated = { ...settingsSite, auto_update: enabled };
      setSettingsSite(updated);
      setSites((prev) => prev.map((s) => (s.id === updated.id ? updated : s)));
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update auto-update setting");
    } finally {
      setSavingSetting(null);
    }
  };

  const toggleMaintenance = async (enabled: boolean) => {
    if (!settingsSite) return;
    setSavingSetting("maintenance");
    try {
      await api.patch(`/wordpress/${settingsSite.id}/maintenance`, { enabled });
      toast.success(`Maintenance mode ${enabled ? "enabled" : "disabled"}`);
      const updated = { ...settingsSite, maintenance_mode: enabled };
      setSettingsSite(updated);
      setSites((prev) => prev.map((s) => (s.id === updated.id ? updated : s)));
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update maintenance mode");
    } finally {
      setSavingSetting(null);
    }
  };

  const handleDelete = async (id: string, domain: string) => {
    if (!confirm(`Are you sure you want to delete WordPress site on "${domain}"? All data will be lost.`)) return;
    try {
      await api.delete(`/wordpress/${id}`);
      toast.success(`WordPress site on ${domain} deleted`);
      fetchSites();
    } catch {
      toast.error("Failed to delete WordPress site");
    }
  };

  const filtered = sites.filter(
    (s) =>
      s.domain.toLowerCase().includes(search.toLowerCase()) ||
      (s.site_url || "").toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Domain",
      accessor: (s: WordPressSite) => (
        <div className="flex items-center gap-2">
          <Blocks size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{s.domain}</span>
        </div>
      ),
    },
    {
      header: "Path",
      accessor: (s: WordPressSite) => (
        <code className="text-xs text-panel-muted font-mono">{s.path || "/"}</code>
      ),
    },
    {
      header: "WP Version",
      accessor: (s: WordPressSite) => (
        <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">
          v{s.version || "?"}
        </code>
      ),
    },
    {
      header: "Status",
      accessor: (s: WordPressSite) => <StatusBadge status={s.maintenance_mode ? "warning" : "active"} />,
    },
    {
      header: "Actions",
      accessor: (s: WordPressSite) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => handleAutoLogin(s)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-emerald-400 transition-colors"
            title="Auto Login to WP Admin"
          >
            <LogIn size={14} />
          </button>
          <button
            onClick={() => openUsersModal(s)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-purple-400 transition-colors"
            title="Manage Users"
          >
            <Users size={14} />
          </button>
          <button
            onClick={() => window.open(s.admin_url || `https://${s.domain}/wp-admin`, "_blank")}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
            title="Open WP Admin"
          >
            <ExternalLink size={14} />
          </button>
          <button
            onClick={() => handleUpdate(s.id)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
            title="Update"
          >
            <RotateCw size={14} />
          </button>
          <button
            onClick={() => openSettings(s)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-amber-400 transition-colors"
            title="Site Settings"
          >
            <Settings size={14} />
          </button>
          <button
            onClick={() => handleDelete(s.id, s.domain)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
            title="Delete"
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
          <h1 className="text-xl font-bold text-panel-text">WordPress</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage WordPress installations on your server
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchSites}
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
            Install WordPress
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search WordPress sites..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
        </div>
      </Card>

      <Card>
        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2, 3, 4].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={filtered} />
        ) : (
          <div className="text-center py-16 px-4">
            <Blocks size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No WordPress sites found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No WordPress sites match your search. Try a different search term."
                : "Install WordPress on any of your domains with one click."}
            </p>
            {!search && (
              <Button
                onClick={() => setShowCreate(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Install WordPress
              </Button>
            )}
          </div>
        )}
      </Card>

      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Install WordPress">
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className={labelClass}>Site Title *</label>
            <input type="text" required placeholder="My Blog" value={form.site_title}
              onChange={(e) => setForm({ ...form, site_title: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>Domain *</label>
            <select required value={form.domain}
              onChange={(e) => setForm({ ...form, domain: e.target.value })} className={selectClass}>
              <option value="">Select a domain</option>
              {domains.map((d) => (
                <option key={d.id} value={d.domain}>{d.domain}</option>
              ))}
            </select>
          </div>
          <div>
            <label className={labelClass}>Install Path</label>
            <div className="flex items-center gap-2">
              <span className="text-sm text-panel-muted whitespace-nowrap">{form.domain || "example.com"}/</span>
              <input type="text" placeholder="(leave empty for document root)" value={form.path}
                onChange={(e) => setForm({ ...form, path: e.target.value })} className={inputClass} />
            </div>
            <p className="text-xs text-panel-muted mt-1">Leave empty to install in the document root, or enter a subdirectory (e.g. "blog", "wp")</p>
          </div>
          {conflict && (
            <div className="flex items-start gap-2 p-3 bg-amber-500/10 border border-amber-500/30 rounded-lg">
              <AlertTriangle size={16} className="text-amber-400 shrink-0 mt-0.5" />
              <p className="text-sm text-amber-300">{conflict}</p>
            </div>
          )}
          <div>
            <label className={labelClass}>Admin Email *</label>
            <input type="email" required placeholder="admin@example.com" value={form.admin_email}
              onChange={(e) => setForm({ ...form, admin_email: e.target.value })} className={inputClass} />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Admin Username *</label>
              <input type="text" required placeholder="admin" value={form.admin_user}
                onChange={(e) => setForm({ ...form, admin_user: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Admin Password *</label>
              <input type="password" required minLength={8} placeholder="Min. 8 characters" value={form.admin_pass}
                onChange={(e) => setForm({ ...form, admin_pass: e.target.value })} className={inputClass} />
            </div>
          </div>
          <label className="flex items-start gap-3 p-3 bg-panel-bg border border-panel-border rounded-lg cursor-pointer hover:border-panel-border/60">
            <input
              type="checkbox"
              checked={form.auto_update}
              onChange={(e) => setForm({ ...form, auto_update: e.target.checked })}
              className="mt-0.5 h-4 w-4 rounded border-panel-border bg-panel-bg text-emerald-600 focus:ring-emerald-500/40"
            />
            <div className="flex-1">
              <div className="text-sm font-medium text-panel-text">Enable WordPress core auto-updates</div>
              <p className="text-xs text-panel-muted mt-0.5">
                Automatically install minor core releases (security and maintenance). Can be changed later from Site Settings.
              </p>
            </div>
          </label>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating || !!conflict}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Installing..." : "Install WordPress"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Site Settings Modal */}
      <Modal isOpen={showSettings} onClose={() => setShowSettings(false)} title={`Site Settings — ${settingsSite?.domain || ""}`}>
        <div className="space-y-4">
          <div className="flex items-start justify-between p-4 bg-panel-bg border border-panel-border rounded-lg">
            <div className="flex-1 pr-4">
              <div className="font-medium text-sm text-panel-text">WordPress Core Auto-Update</div>
              <p className="text-xs text-panel-muted mt-1">
                When enabled, WordPress will automatically install minor core releases (security and maintenance updates).
                Sets <code className="text-panel-text">WP_AUTO_UPDATE_CORE</code> in <code className="text-panel-text">wp-config.php</code>.
              </p>
            </div>
            <button
              type="button"
              disabled={savingSetting === "auto_update"}
              onClick={() => toggleAutoUpdate(!settingsSite?.auto_update)}
              className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors focus:outline-none disabled:opacity-50 ${
                settingsSite?.auto_update ? "bg-emerald-600" : "bg-panel-border"
              }`}
            >
              <span
                className={`pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ${
                  settingsSite?.auto_update ? "translate-x-5" : "translate-x-0"
                }`}
              />
            </button>
          </div>

          <div className="flex items-start justify-between p-4 bg-panel-bg border border-panel-border rounded-lg">
            <div className="flex-1 pr-4">
              <div className="font-medium text-sm text-panel-text">Maintenance Mode</div>
              <p className="text-xs text-panel-muted mt-1">
                Displays a maintenance page to visitors while keeping the admin accessible.
              </p>
            </div>
            <button
              type="button"
              disabled={savingSetting === "maintenance"}
              onClick={() => toggleMaintenance(!settingsSite?.maintenance_mode)}
              className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors focus:outline-none disabled:opacity-50 ${
                settingsSite?.maintenance_mode ? "bg-amber-600" : "bg-panel-border"
              }`}
            >
              <span
                className={`pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ${
                  settingsSite?.maintenance_mode ? "translate-x-5" : "translate-x-0"
                }`}
              />
            </button>
          </div>
        </div>
      </Modal>

      {/* Manage Users Modal */}
      <Modal isOpen={showUsers} onClose={() => { setShowUsers(false); setShowAddUser(false); }} title={`WordPress Users — ${selectedSite?.domain || ""}`}>
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <p className="text-sm text-panel-muted">{wpUsers.length} user{wpUsers.length !== 1 ? "s" : ""}</p>
            <button
              onClick={() => setShowAddUser(!showAddUser)}
              className="flex items-center gap-1.5 px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-xs font-medium transition-colors"
            >
              <UserPlus size={12} />
              Add User
            </button>
          </div>

          {showAddUser && (
            <form onSubmit={handleCreateUser} className="p-3 bg-panel-bg border border-panel-border rounded-lg space-y-3">
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className={labelClass}>Username *</label>
                  <input type="text" required placeholder="johndoe" value={userForm.username}
                    onChange={(e) => setUserForm({ ...userForm, username: e.target.value })} className={inputClass} />
                </div>
                <div>
                  <label className={labelClass}>Email *</label>
                  <input type="email" required placeholder="john@example.com" value={userForm.email}
                    onChange={(e) => setUserForm({ ...userForm, email: e.target.value })} className={inputClass} />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className={labelClass}>Password *</label>
                  <input type="password" required minLength={6} placeholder="Min. 6 characters" value={userForm.password}
                    onChange={(e) => setUserForm({ ...userForm, password: e.target.value })} className={inputClass} />
                </div>
                <div>
                  <label className={labelClass}>Role</label>
                  <select value={userForm.role} onChange={(e) => setUserForm({ ...userForm, role: e.target.value })} className={selectClass}>
                    <option value="administrator">Administrator</option>
                    <option value="editor">Editor</option>
                    <option value="author">Author</option>
                    <option value="contributor">Contributor</option>
                    <option value="subscriber">Subscriber</option>
                  </select>
                </div>
              </div>
              <div className="flex justify-end gap-2">
                <button type="button" onClick={() => setShowAddUser(false)}
                  className="px-3 py-1.5 text-xs text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
                  Cancel
                </button>
                <button type="submit" disabled={creatingUser}
                  className="px-3 py-1.5 text-xs bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
                  {creatingUser ? "Creating..." : "Create User"}
                </button>
              </div>
            </form>
          )}

          {loadingUsers ? (
            <div className="space-y-2">
              {[1, 2, 3].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          ) : wpUsers.length > 0 ? (
            <div className="divide-y divide-panel-border border border-panel-border rounded-lg overflow-hidden">
              {wpUsers.map((u) => (
                <div key={u.ID} className="flex items-center justify-between p-3 bg-panel-surface hover:bg-panel-bg transition-colors">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-sm text-panel-text truncate">{u.user_login}</span>
                      <span className="text-xs text-panel-muted truncate">{u.user_email}</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-2 ml-3">
                    <select
                      value={u.roles}
                      onChange={(e) => handleChangeRole(u.ID, e.target.value)}
                      className="px-2 py-1 bg-panel-bg border border-panel-border rounded text-xs text-panel-text focus:outline-none focus:ring-1 focus:ring-blue-500"
                    >
                      <option value="administrator">Administrator</option>
                      <option value="editor">Editor</option>
                      <option value="author">Author</option>
                      <option value="contributor">Contributor</option>
                      <option value="subscriber">Subscriber</option>
                    </select>
                    {u.ID !== "1" && (
                      <button
                        onClick={() => handleDeleteUser(u.ID, u.user_login)}
                        className="p-1 rounded hover:bg-red-500/10 text-panel-muted hover:text-red-400 transition-colors"
                        title="Delete user"
                      >
                        <Trash2 size={12} />
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-center text-panel-muted text-sm py-6">No users found</p>
          )}
        </div>
      </Modal>
    </div>
  );
}
