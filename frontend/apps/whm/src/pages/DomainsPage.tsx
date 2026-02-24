import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Globe, Plus, RefreshCw, Search, Trash2, ExternalLink,
  PauseCircle, PlayCircle, Code, HardDrive, Users
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
  created_at: string;
}

const PHP_VERSIONS = ["7.4", "8.0", "8.1", "8.2", "8.3"];

export default function DomainsPage() {
  const [domains, setDomains] = useState<Domain[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  // Add domain modal
  const [showAddModal, setShowAddModal] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({
    domain: "",
    user: "",
    password: "",
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

  useEffect(() => {
    fetchDomains();
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

  const handleCreate = async () => {
    if (!form.domain || !form.user || !form.password) {
      toast.error("Domain, username, and password are required");
      return;
    }
    if (form.password.length < 8) {
      toast.error("Password must be at least 8 characters");
      return;
    }
    setCreating(true);
    try {
      await api.post("/domains", form);
      toast.success(`Domain ${form.domain} created successfully`);
      setShowAddModal(false);
      setForm({
        domain: "", user: "", password: "", php_version: "8.2",
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
    if (!confirm(`Are you sure you want to delete ${domain}? This will remove the user, files, and all associated data.`)) return;
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

  // Auto-generate username from domain
  const handleDomainChange = (value: string) => {
    setForm((prev) => ({
      ...prev,
      domain: value,
      user: prev.user || value.replace(/[^a-z0-9]/gi, "").slice(0, 16).toLowerCase(),
    }));
  };

  const filtered = domains.filter((d) =>
    d.domain.toLowerCase().includes(search.toLowerCase()) ||
    d.user.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Domain",
      accessor: (d: Domain) => (
        <div>
          <div className="flex items-center gap-2">
            <Globe size={14} className="text-blue-400" />
            <span className="font-medium text-panel-text">{d.domain}</span>
          </div>
          <span className="text-xs text-panel-muted ml-6">{d.user}</span>
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
          {/* Domain + User row */}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">Domain Name *</label>
              <input
                type="text"
                value={form.domain}
                onChange={(e) => handleDomainChange(e.target.value)}
                placeholder="example.com"
                className={inputClass}
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">Linux Username *</label>
              <input
                type="text"
                value={form.user}
                onChange={(e) => setForm((p) => ({ ...p, user: e.target.value }))}
                placeholder="exampleuser"
                className={inputClass}
              />
              <p className="text-xs text-panel-muted mt-1">System user for this domain</p>
            </div>
          </div>

          {/* Password + PHP */}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">Password *</label>
              <input
                type="password"
                value={form.password}
                onChange={(e) => setForm((p) => ({ ...p, password: e.target.value }))}
                placeholder="Min 8 characters"
                className={inputClass}
              />
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
              disabled={creating || !form.domain || !form.user || !form.password}
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
    </div>
  );
}
