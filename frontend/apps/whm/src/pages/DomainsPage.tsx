import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { useNavigate } from "react-router-dom";
import {
  Globe, Plus, RefreshCw, Search, Trash2, ExternalLink,
  PauseCircle, PlayCircle, Code, HardDrive, Users, FolderOpen,
  Clock, Rocket, Eye, User
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
  status: "active" | "suspended" | "pending";
  coming_soon?: boolean;
  created_at: string;
}

interface UserOption {
  id: string;
  username: string;
  name: string;
}

const PHP_VERSIONS = ["7.4", "8.0", "8.1", "8.2", "8.3"];

export default function DomainsPage() {
  const navigate = useNavigate();
  const [domains, setDomains] = useState<Domain[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [usersList, setUsersList] = useState<UserOption[]>([]);

  // Add domain modal
  const [showAddModal, setShowAddModal] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({
    domain: "",
    user: "",
    php_version: "8.2",
    disk_quota_mb: 5120,
    bandwidth_limit_gb: 100,
    max_databases: 10,
    max_email_accounts: 50,
    max_subdomains: 20,
    max_apps: 5,
  });

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
      const res = await api.get("/domains");
      setDomains(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const fetchUsers = async () => {
    try {
      const res = await api.get("/users");
      setUsersList(res.data.data || []);
    } catch {
      // Keep empty
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
        domain: "", user: "", php_version: "8.2",
        disk_quota_mb: 5120, bandwidth_limit_gb: 100,
        max_databases: 10, max_email_accounts: 50, max_subdomains: 20, max_apps: 5,
      });
      fetchDomains();
    } catch (err: any) {
      const msg = err.response?.data?.error?.message || "Failed to create domain";
      toast.error(msg);
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string, domain: string) => {
    if (!confirm(`Are you sure you want to delete ${domain}? This will remove domain files, DNS zone, and all associated data.`)) return;
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

  const columns = [
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
        <span className={d.ssl_active ? "text-green-400 text-sm" : "text-panel-muted text-sm"}>
          {d.ssl_active ? "Active" : "None"}
        </span>
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
          <Table columns={columns} data={filtered} />
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

      {/* Add Domain Modal */}
      <Modal isOpen={showAddModal} title="Add New Domain" onClose={() => setShowAddModal(false)} size="lg">
        <div className="space-y-5">
          {/* Domain + User + PHP row */}
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1">Domain Name *</label>
            <input
              type="text"
              value={form.domain}
              onChange={(e) => setForm((p) => ({ ...p, domain: e.target.value }))}
              placeholder="example.com"
              className={inputClass}
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">Account (User) *</label>
              <select
                value={form.user}
                onChange={(e) => setForm((p) => ({ ...p, user: e.target.value }))}
                className={inputClass}
              >
                <option value="">Select a user...</option>
                {usersList.map((u) => (
                  <option key={u.id} value={u.username}>
                    {u.username} — {u.name}
                  </option>
                ))}
              </select>
              <p className="text-xs text-panel-muted mt-1">Domain will be created under this user's account</p>
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

          {/* Resource Limits */}
          <div className="border-t border-panel-border pt-4">
            <h4 className="text-sm font-medium text-panel-text mb-3 flex items-center gap-2">
              <Users size={14} />
              Resource Limits
            </h4>
            <div className="grid grid-cols-3 gap-4">
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
          </div>

          {/* Actions */}
          <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
            <Button
              onClick={() => setShowAddModal(false)}
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
                    Powered by ServerPanel &bull; BetaZen InfoTech
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
    </div>
  );
}
