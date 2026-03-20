import { useState, useEffect } from "react";
import { Card, Button, Table, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Box, Plus, RefreshCw, Search, Trash2, Pencil, HardDrive,
  Wifi, Mail, Database, Globe, Users, Infinity
} from "lucide-react";

interface HostingPackage {
  id: string;
  name: string;
  created_by: string;
  disk_quota_mb: number;
  disk_quota_unlimited: boolean;
  bandwidth_mb: number;
  bandwidth_unlimited: boolean;
  max_ftp_accounts: number;
  max_ftp_unlimited: boolean;
  max_email_accounts: number;
  max_email_unlimited: boolean;
  max_mailing_lists: number;
  max_mailing_unlimited: boolean;
  max_databases: number;
  max_databases_unlimited: boolean;
  max_subdomains: number;
  max_subdomains_unlimited: boolean;
  max_parked_domains: number;
  max_parked_unlimited: boolean;
  max_addon_domains: number;
  max_addon_unlimited: boolean;
  max_passenger_apps: number;
  max_passenger_unlimited: boolean;
  max_hourly_email: number;
  max_hourly_email_unlimited: boolean;
  max_fail_percent: number;
  max_email_quota_mb: number;
  max_email_quota_unlimited: boolean;
  dedicated_ip: boolean;
  shell_access: boolean;
  cgi_access: boolean;
  digest_auth: boolean;
  theme: string;
  feature_list: string;
  locale: string;
  wp_toolkit: boolean;
  lve_enabled: boolean;
  lve_speed: number;
  lve_speed_mysql: number;
  lve_vmem: number;
  lve_pmem: number;
  lve_io: number;
  lve_mysql_io: string;
  lve_iops: number;
  lve_ep: number;
  lve_nproc: number;
  lve_inodes_soft: number;
  lve_inodes_hard: number;
  account_count: number;
  created_at: string;
  updated_at: string;
}

const defaultForm = {
  name: "",
  disk_quota_mb: 5120,
  disk_quota_unlimited: false,
  bandwidth_mb: 102400,
  bandwidth_unlimited: false,
  max_ftp_accounts: 10,
  max_ftp_unlimited: false,
  max_email_accounts: 50,
  max_email_unlimited: false,
  max_mailing_lists: 5,
  max_mailing_unlimited: false,
  max_databases: 10,
  max_databases_unlimited: false,
  max_subdomains: 20,
  max_subdomains_unlimited: false,
  max_parked_domains: 5,
  max_parked_unlimited: false,
  max_addon_domains: 5,
  max_addon_unlimited: false,
  max_passenger_apps: 5,
  max_passenger_unlimited: false,
  max_hourly_email: 500,
  max_hourly_email_unlimited: false,
  max_fail_percent: 30,
  max_email_quota_mb: 250,
  max_email_quota_unlimited: false,
  dedicated_ip: false,
  shell_access: false,
  cgi_access: true,
  digest_auth: false,
  theme: "jupiter",
  feature_list: "default",
  locale: "en",
  wp_toolkit: true,
  lve_enabled: false,
  lve_speed: 100,
  lve_speed_mysql: 0,
  lve_vmem: 0,
  lve_pmem: 256,
  lve_io: 4096,
  lve_mysql_io: "0",
  lve_iops: 1024,
  lve_ep: 20,
  lve_nproc: 100,
  lve_inodes_soft: 0,
  lve_inodes_hard: 0,
};

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const selectClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";

function ResourceField({
  label,
  value,
  unlimited,
  onChange,
  onUnlimitedChange,
  unit,
}: {
  label: string;
  value: number;
  unlimited: boolean;
  onChange: (v: number) => void;
  onUnlimitedChange: (v: boolean) => void;
  unit?: string;
}) {
  return (
    <div>
      <label className={labelClass}>
        {label} {unit && <span className="text-panel-muted font-normal">({unit})</span>}
      </label>
      <div className="flex items-center gap-2">
        <input
          type="number"
          value={unlimited ? "" : value}
          onChange={(e) => onChange(parseInt(e.target.value) || 0)}
          disabled={unlimited}
          placeholder={unlimited ? "Unlimited" : ""}
          className={`${inputClass} flex-1 ${unlimited ? "opacity-50" : ""}`}
          min={0}
        />
        <label className="flex items-center gap-1.5 text-xs text-panel-muted whitespace-nowrap cursor-pointer">
          <input
            type="checkbox"
            checked={unlimited}
            onChange={(e) => onUnlimitedChange(e.target.checked)}
            className="rounded border-panel-border bg-panel-bg text-blue-500 focus:ring-blue-500/40"
          />
          <Infinity size={12} />
        </label>
      </div>
    </div>
  );
}

function formatResource(value: number, unlimited: boolean, unit = "MB") {
  if (unlimited) return <span className="text-cyan-400 flex items-center gap-1"><Infinity size={12} /> Unlimited</span>;
  if (unit === "MB" && value >= 1024) return `${(value / 1024).toFixed(1)} GB`;
  return `${value} ${unit}`;
}

export default function PackagesPage() {
  const [packages, setPackages] = useState<HostingPackage[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  const [showAddModal, setShowAddModal] = useState(false);
  const [showEditModal, setShowEditModal] = useState(false);
  const [creating, setCreating] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editPackage, setEditPackage] = useState<HostingPackage | null>(null);
  const [form, setForm] = useState({ ...defaultForm });
  const [activeTab, setActiveTab] = useState<"resources" | "settings" | "extensions">("resources");

  useEffect(() => {
    fetchPackages();
  }, []);

  const fetchPackages = async () => {
    setLoading(true);
    try {
      const res = await api.get("/packages", { params: search ? { search } : {} });
      setPackages(res.data.data || []);
    } catch {
      // keep empty
    } finally {
      setLoading(false);
    }
  };

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault();
    fetchPackages();
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name.trim()) {
      toast.error("Package name is required");
      return;
    }
    setCreating(true);
    try {
      await api.post("/packages", form);
      toast.success(`Package "${form.name}" created`);
      setShowAddModal(false);
      setForm({ ...defaultForm });
      setActiveTab("resources");
      fetchPackages();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create package");
    } finally {
      setCreating(false);
    }
  };

  const openEdit = (pkg: HostingPackage) => {
    setEditPackage(pkg);
    setForm({
      name: pkg.name,
      disk_quota_mb: pkg.disk_quota_mb,
      disk_quota_unlimited: pkg.disk_quota_unlimited,
      bandwidth_mb: pkg.bandwidth_mb,
      bandwidth_unlimited: pkg.bandwidth_unlimited,
      max_ftp_accounts: pkg.max_ftp_accounts,
      max_ftp_unlimited: pkg.max_ftp_unlimited,
      max_email_accounts: pkg.max_email_accounts,
      max_email_unlimited: pkg.max_email_unlimited,
      max_mailing_lists: pkg.max_mailing_lists,
      max_mailing_unlimited: pkg.max_mailing_unlimited,
      max_databases: pkg.max_databases,
      max_databases_unlimited: pkg.max_databases_unlimited,
      max_subdomains: pkg.max_subdomains,
      max_subdomains_unlimited: pkg.max_subdomains_unlimited,
      max_parked_domains: pkg.max_parked_domains,
      max_parked_unlimited: pkg.max_parked_unlimited,
      max_addon_domains: pkg.max_addon_domains,
      max_addon_unlimited: pkg.max_addon_unlimited,
      max_passenger_apps: pkg.max_passenger_apps,
      max_passenger_unlimited: pkg.max_passenger_unlimited,
      max_hourly_email: pkg.max_hourly_email,
      max_hourly_email_unlimited: pkg.max_hourly_email_unlimited,
      max_fail_percent: pkg.max_fail_percent,
      max_email_quota_mb: pkg.max_email_quota_mb,
      max_email_quota_unlimited: pkg.max_email_quota_unlimited,
      dedicated_ip: pkg.dedicated_ip,
      shell_access: pkg.shell_access,
      cgi_access: pkg.cgi_access,
      digest_auth: pkg.digest_auth,
      theme: pkg.theme,
      feature_list: pkg.feature_list,
      locale: pkg.locale,
      wp_toolkit: pkg.wp_toolkit,
      lve_enabled: pkg.lve_enabled,
      lve_speed: pkg.lve_speed,
      lve_speed_mysql: pkg.lve_speed_mysql,
      lve_vmem: pkg.lve_vmem,
      lve_pmem: pkg.lve_pmem,
      lve_io: pkg.lve_io,
      lve_mysql_io: pkg.lve_mysql_io,
      lve_iops: pkg.lve_iops,
      lve_ep: pkg.lve_ep,
      lve_nproc: pkg.lve_nproc,
      lve_inodes_soft: pkg.lve_inodes_soft,
      lve_inodes_hard: pkg.lve_inodes_hard,
    });
    setActiveTab("resources");
    setShowEditModal(true);
  };

  const handleEdit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editPackage) return;
    setSaving(true);
    try {
      await api.put(`/packages/${editPackage.id}`, form);
      toast.success(`Package "${form.name}" updated`);
      setShowEditModal(false);
      setEditPackage(null);
      fetchPackages();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update package");
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Are you sure you want to delete package "${name}"?`)) return;
    try {
      await api.delete(`/packages/${id}`);
      toast.success(`Package "${name}" deleted`);
      fetchPackages();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to delete package");
    }
  };

  const columns = [
    {
      header: "Package Name",
      accessor: (p: HostingPackage) => (
        <div className="flex items-center gap-2">
          <Box size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{p.name}</span>
        </div>
      ),
    },
    {
      header: "Disk Quota",
      accessor: (p: HostingPackage) => (
        <div className="flex items-center gap-1.5 text-sm">
          <HardDrive size={12} className="text-panel-muted" />
          {formatResource(p.disk_quota_mb, p.disk_quota_unlimited)}
        </div>
      ),
    },
    {
      header: "Bandwidth",
      accessor: (p: HostingPackage) => (
        <div className="flex items-center gap-1.5 text-sm">
          <Wifi size={12} className="text-panel-muted" />
          {formatResource(p.bandwidth_mb, p.bandwidth_unlimited)}
        </div>
      ),
    },
    {
      header: "Email",
      accessor: (p: HostingPackage) => (
        <div className="flex items-center gap-1.5 text-sm">
          <Mail size={12} className="text-panel-muted" />
          {formatResource(p.max_email_accounts, p.max_email_unlimited, "")}
        </div>
      ),
    },
    {
      header: "Databases",
      accessor: (p: HostingPackage) => (
        <div className="flex items-center gap-1.5 text-sm">
          <Database size={12} className="text-panel-muted" />
          {formatResource(p.max_databases, p.max_databases_unlimited, "")}
        </div>
      ),
    },
    {
      header: "Accounts",
      accessor: (p: HostingPackage) => (
        <div className="flex items-center gap-1.5 text-sm">
          <Users size={12} className="text-panel-muted" />
          <span>{p.account_count}</span>
        </div>
      ),
    },
    {
      header: "Actions",
      accessor: (p: HostingPackage) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => openEdit(p)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
            title="Edit Package"
          >
            <Pencil size={14} />
          </button>
          <button
            onClick={() => handleDelete(p.id, p.name)}
            disabled={p.account_count > 0}
            className={`p-1.5 rounded transition-colors ${
              p.account_count > 0
                ? "text-panel-muted/30 cursor-not-allowed"
                : "hover:bg-panel-bg text-panel-muted hover:text-red-400"
            }`}
            title={p.account_count > 0 ? `Cannot delete: ${p.account_count} active accounts` : "Delete Package"}
          >
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  const updateForm = (key: string, value: any) => setForm((prev) => ({ ...prev, [key]: value }));

  const renderFormTabs = () => (
    <div className="flex gap-1 mb-4 border-b border-panel-border">
      {(["resources", "settings", "extensions"] as const).map((tab) => (
        <button
          key={tab}
          type="button"
          onClick={() => setActiveTab(tab)}
          className={`px-4 py-2 text-sm font-medium capitalize transition-colors border-b-2 -mb-px ${
            activeTab === tab
              ? "border-blue-500 text-blue-400"
              : "border-transparent text-panel-muted hover:text-panel-text"
          }`}
        >
          {tab}
        </button>
      ))}
    </div>
  );

  const renderResourcesTab = () => (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
      <ResourceField label="Disk Quota" unit="MB" value={form.disk_quota_mb} unlimited={form.disk_quota_unlimited} onChange={(v) => updateForm("disk_quota_mb", v)} onUnlimitedChange={(v) => updateForm("disk_quota_unlimited", v)} />
      <ResourceField label="Bandwidth" unit="MB" value={form.bandwidth_mb} unlimited={form.bandwidth_unlimited} onChange={(v) => updateForm("bandwidth_mb", v)} onUnlimitedChange={(v) => updateForm("bandwidth_unlimited", v)} />
      <ResourceField label="FTP Accounts" value={form.max_ftp_accounts} unlimited={form.max_ftp_unlimited} onChange={(v) => updateForm("max_ftp_accounts", v)} onUnlimitedChange={(v) => updateForm("max_ftp_unlimited", v)} />
      <ResourceField label="Email Accounts" value={form.max_email_accounts} unlimited={form.max_email_unlimited} onChange={(v) => updateForm("max_email_accounts", v)} onUnlimitedChange={(v) => updateForm("max_email_unlimited", v)} />
      <ResourceField label="Mailing Lists" value={form.max_mailing_lists} unlimited={form.max_mailing_unlimited} onChange={(v) => updateForm("max_mailing_lists", v)} onUnlimitedChange={(v) => updateForm("max_mailing_unlimited", v)} />
      <ResourceField label="Databases" value={form.max_databases} unlimited={form.max_databases_unlimited} onChange={(v) => updateForm("max_databases", v)} onUnlimitedChange={(v) => updateForm("max_databases_unlimited", v)} />
      <ResourceField label="Subdomains" value={form.max_subdomains} unlimited={form.max_subdomains_unlimited} onChange={(v) => updateForm("max_subdomains", v)} onUnlimitedChange={(v) => updateForm("max_subdomains_unlimited", v)} />
      <ResourceField label="Parked Domains" value={form.max_parked_domains} unlimited={form.max_parked_unlimited} onChange={(v) => updateForm("max_parked_domains", v)} onUnlimitedChange={(v) => updateForm("max_parked_unlimited", v)} />
      <ResourceField label="Addon Domains" value={form.max_addon_domains} unlimited={form.max_addon_unlimited} onChange={(v) => updateForm("max_addon_domains", v)} onUnlimitedChange={(v) => updateForm("max_addon_unlimited", v)} />
      <ResourceField label="Passenger Apps" value={form.max_passenger_apps} unlimited={form.max_passenger_unlimited} onChange={(v) => updateForm("max_passenger_apps", v)} onUnlimitedChange={(v) => updateForm("max_passenger_unlimited", v)} />
      <ResourceField label="Hourly Email Limit" value={form.max_hourly_email} unlimited={form.max_hourly_email_unlimited} onChange={(v) => updateForm("max_hourly_email", v)} onUnlimitedChange={(v) => updateForm("max_hourly_email_unlimited", v)} />
      <ResourceField label="Email Quota" unit="MB" value={form.max_email_quota_mb} unlimited={form.max_email_quota_unlimited} onChange={(v) => updateForm("max_email_quota_mb", v)} onUnlimitedChange={(v) => updateForm("max_email_quota_unlimited", v)} />
      <div>
        <label className={labelClass}>Max Fail Percent (%)</label>
        <input type="number" value={form.max_fail_percent} onChange={(e) => updateForm("max_fail_percent", parseInt(e.target.value) || 0)} className={inputClass} min={0} max={100} />
      </div>
    </div>
  );

  const renderSettingsTab = () => (
    <div className="space-y-6">
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        {([
          { key: "dedicated_ip", label: "Dedicated IP" },
          { key: "shell_access", label: "Shell Access" },
          { key: "cgi_access", label: "CGI Access" },
          { key: "digest_auth", label: "Digest Authentication" },
        ] as const).map(({ key, label }) => (
          <label key={key} className="flex items-center gap-3 p-3 bg-panel-bg rounded-lg cursor-pointer hover:bg-panel-border/20 transition-colors">
            <input
              type="checkbox"
              checked={form[key]}
              onChange={(e) => updateForm(key, e.target.checked)}
              className="rounded border-panel-border bg-panel-bg text-blue-500 focus:ring-blue-500/40"
            />
            <span className="text-sm text-panel-text">{label}</span>
          </label>
        ))}
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <div>
          <label className={labelClass}>Theme</label>
          <input type="text" value={form.theme} onChange={(e) => updateForm("theme", e.target.value)} className={inputClass} placeholder="jupiter" />
        </div>
        <div>
          <label className={labelClass}>Feature List</label>
          <input type="text" value={form.feature_list} onChange={(e) => updateForm("feature_list", e.target.value)} className={inputClass} placeholder="default" />
        </div>
        <div>
          <label className={labelClass}>Locale</label>
          <input type="text" value={form.locale} onChange={(e) => updateForm("locale", e.target.value)} className={inputClass} placeholder="en" />
        </div>
      </div>
    </div>
  );

  const renderExtensionsTab = () => (
    <div className="space-y-6">
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <label className="flex items-center gap-3 p-3 bg-panel-bg rounded-lg cursor-pointer hover:bg-panel-border/20 transition-colors">
          <input type="checkbox" checked={form.wp_toolkit} onChange={(e) => updateForm("wp_toolkit", e.target.checked)} className="rounded border-panel-border bg-panel-bg text-blue-500 focus:ring-blue-500/40" />
          <span className="text-sm text-panel-text">WordPress Toolkit</span>
        </label>
        <label className="flex items-center gap-3 p-3 bg-panel-bg rounded-lg cursor-pointer hover:bg-panel-border/20 transition-colors">
          <input type="checkbox" checked={form.lve_enabled} onChange={(e) => updateForm("lve_enabled", e.target.checked)} className="rounded border-panel-border bg-panel-bg text-blue-500 focus:ring-blue-500/40" />
          <span className="text-sm text-panel-text">LVE (Lightweight Virtual Environment)</span>
        </label>
      </div>

      {form.lve_enabled && (
        <div>
          <h4 className="text-sm font-medium text-panel-text mb-3">LVE Resource Limits</h4>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            <div>
              <label className={labelClass}>CPU Speed (%)</label>
              <input type="number" value={form.lve_speed} onChange={(e) => updateForm("lve_speed", parseInt(e.target.value) || 0)} className={inputClass} min={0} />
            </div>
            <div>
              <label className={labelClass}>MySQL CPU (%)</label>
              <input type="number" value={form.lve_speed_mysql} onChange={(e) => updateForm("lve_speed_mysql", parseInt(e.target.value) || 0)} className={inputClass} min={0} />
            </div>
            <div>
              <label className={labelClass}>Virtual Memory (MB)</label>
              <input type="number" value={form.lve_vmem} onChange={(e) => updateForm("lve_vmem", parseInt(e.target.value) || 0)} className={inputClass} min={0} />
            </div>
            <div>
              <label className={labelClass}>Physical Memory (MB)</label>
              <input type="number" value={form.lve_pmem} onChange={(e) => updateForm("lve_pmem", parseInt(e.target.value) || 0)} className={inputClass} min={0} />
            </div>
            <div>
              <label className={labelClass}>I/O (KB/s)</label>
              <input type="number" value={form.lve_io} onChange={(e) => updateForm("lve_io", parseInt(e.target.value) || 0)} className={inputClass} min={0} />
            </div>
            <div>
              <label className={labelClass}>MySQL I/O</label>
              <input type="text" value={form.lve_mysql_io} onChange={(e) => updateForm("lve_mysql_io", e.target.value)} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>IOPS</label>
              <input type="number" value={form.lve_iops} onChange={(e) => updateForm("lve_iops", parseInt(e.target.value) || 0)} className={inputClass} min={0} />
            </div>
            <div>
              <label className={labelClass}>Entry Processes</label>
              <input type="number" value={form.lve_ep} onChange={(e) => updateForm("lve_ep", parseInt(e.target.value) || 0)} className={inputClass} min={0} />
            </div>
            <div>
              <label className={labelClass}>Max Processes (NPROC)</label>
              <input type="number" value={form.lve_nproc} onChange={(e) => updateForm("lve_nproc", parseInt(e.target.value) || 0)} className={inputClass} min={0} />
            </div>
            <div>
              <label className={labelClass}>Inodes (Soft Limit)</label>
              <input type="number" value={form.lve_inodes_soft} onChange={(e) => updateForm("lve_inodes_soft", parseInt(e.target.value) || 0)} className={inputClass} min={0} />
            </div>
            <div>
              <label className={labelClass}>Inodes (Hard Limit)</label>
              <input type="number" value={form.lve_inodes_hard} onChange={(e) => updateForm("lve_inodes_hard", parseInt(e.target.value) || 0)} className={inputClass} min={0} />
            </div>
          </div>
        </div>
      )}
    </div>
  );

  const renderFormBody = (onSubmit: (e: React.FormEvent) => void, isEdit: boolean) => (
    <form onSubmit={onSubmit} className="space-y-4">
      <div>
        <label className={labelClass}>Package Name *</label>
        <input
          type="text"
          value={form.name}
          onChange={(e) => updateForm("name", e.target.value)}
          className={inputClass}
          placeholder="e.g. Basic, Pro, Enterprise"
          required
        />
      </div>

      {renderFormTabs()}

      {activeTab === "resources" && renderResourcesTab()}
      {activeTab === "settings" && renderSettingsTab()}
      {activeTab === "extensions" && renderExtensionsTab()}

      <div className="flex justify-end gap-2 pt-4 border-t border-panel-border">
        <Button
          type="button"
          onClick={() => { isEdit ? setShowEditModal(false) : setShowAddModal(false); }}
          className="px-4 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm transition-colors"
        >
          Cancel
        </Button>
        <Button
          type="submit"
          disabled={isEdit ? saving : creating}
          className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
        >
          {(isEdit ? saving : creating) ? "Saving..." : isEdit ? "Update Package" : "Create Package"}
        </Button>
      </div>
    </form>
  );

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Hosting Packages</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage hosting packages with resource limits and settings
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchPackages}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => {
              setForm({ ...defaultForm });
              setActiveTab("resources");
              setShowAddModal(true);
            }}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Add Package
          </Button>
        </div>
      </div>

      {/* Search */}
      <Card>
        <form onSubmit={handleSearch} className="p-4 flex gap-3">
          <div className="relative flex-1">
            <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search packages..."
              className={`${inputClass} pl-9`}
            />
          </div>
          <Button type="submit" className="px-4 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm transition-colors">
            Search
          </Button>
        </form>
      </Card>

      {/* Table */}
      <Card>
        <div className="p-5 border-b border-panel-border">
          <div className="flex items-center gap-2">
            <Box size={16} className="text-blue-400" />
            <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
              Packages ({packages.length})
            </h3>
          </div>
        </div>
        {loading ? (
          <div className="p-6 space-y-3">
            {[...Array(5)].map((_, i) => (
              <div key={i} className="h-12 bg-panel-bg rounded-lg animate-pulse" />
            ))}
          </div>
        ) : packages.length === 0 ? (
          <div className="p-12 text-center">
            <Box size={40} className="mx-auto text-panel-muted/30 mb-3" />
            <p className="text-panel-muted">No hosting packages found</p>
            <p className="text-panel-muted/60 text-sm mt-1">Create your first package to get started</p>
          </div>
        ) : (
          <Table columns={columns} data={packages} />
        )}
      </Card>

      {/* Create Modal */}
      <Modal
        isOpen={showAddModal}
        onClose={() => setShowAddModal(false)}
        title="Create Hosting Package"
        size="xl"
      >
        <div className="p-5">
          {renderFormBody(handleCreate, false)}
        </div>
      </Modal>

      {/* Edit Modal */}
      <Modal
        isOpen={showEditModal}
        onClose={() => setShowEditModal(false)}
        title={`Edit Package: ${editPackage?.name || ""}`}
        size="xl"
      >
        <div className="p-5">
          {renderFormBody(handleEdit, true)}
        </div>
      </Modal>
    </div>
  );
}
