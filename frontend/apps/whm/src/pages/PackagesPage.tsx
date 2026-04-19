import { useState, useEffect } from "react";
import { Card, Button, Table, Modal, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { useAuthStore } from "@/store/auth";
import {
  Box, Plus, RefreshCw, Search, Trash2, Pencil, HardDrive,
  Wifi, Mail, Database, Globe, Users, Infinity, ArrowUpCircle,
  Check, X as XIcon, Clock, CreditCard, AlertCircle,
} from "lucide-react";

// PackageChangeRequest mirrors models.PackageChangeRequest. Used on the
// vendor view (my pending request) and the admin review queue.
interface PackageChangeRequest {
  id: string;
  vendor_id: string;
  vendor_username: string;
  vendor_name: string;
  from_package_name: string;
  to_package_id: string;
  to_package_name: string;
  note: string;
  status: "pending" | "approved" | "rejected";
  payment_reference?: string;
  admin_response?: string;
  created_at: string;
  resolved_at?: string;
  resolved_by?: string;
}

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
  is_default: boolean;
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
  const authUser = useAuthStore((s) => s.user);
  // Only the platform owner runs the package catalog (create/edit/
  // delete). Vendors see a catalog-readonly view with a plan-switch
  // request flow gated on external payment confirmation.
  const isOwner = authUser?.role === "vendor_owner";
  const currentPackageId = authUser?.package_id || "";

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

  // Vendor-side "request plan switch" modal + the vendor's own
  // pending request (if any) for the waiting-banner.
  const [switchTarget, setSwitchTarget] = useState<HostingPackage | null>(null);
  const [switchNote, setSwitchNote] = useState("");
  const [switchSubmitting, setSwitchSubmitting] = useState(false);
  const [myRequest, setMyRequest] = useState<PackageChangeRequest | null>(null);

  // Owner-side review queue of pending change requests + the
  // approve/reject modal.
  const [pendingRequests, setPendingRequests] = useState<PackageChangeRequest[]>([]);
  const [reviewTarget, setReviewTarget] = useState<PackageChangeRequest | null>(null);
  const [reviewMode, setReviewMode] = useState<"approve" | "reject" | null>(null);
  const [reviewPayRef, setReviewPayRef] = useState("");
  const [reviewNote, setReviewNote] = useState("");
  const [reviewSubmitting, setReviewSubmitting] = useState(false);

  useEffect(() => {
    fetchPackages();
    if (!isOwner) {
      fetchMyRequest();
    } else {
      fetchPendingRequests();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Vendor's own pending change request — powers the "waiting on admin
  // review" banner that blocks re-submits until resolved.
  const fetchMyRequest = async () => {
    try {
      const res = await api.get("/packages/my-request");
      setMyRequest(res.data?.data || null);
    } catch {
      setMyRequest(null);
    }
  };

  const fetchPendingRequests = async () => {
    try {
      const res = await api.get("/admin/package-requests?status=pending");
      setPendingRequests(res.data?.data || []);
    } catch {
      setPendingRequests([]);
    }
  };

  const submitSwitchRequest = async () => {
    if (!switchTarget) return;
    setSwitchSubmitting(true);
    try {
      await api.post("/packages/request-change", {
        target_package_id: switchTarget.id,
        note: switchNote,
      });
      toast.success("Request submitted — you'll be notified once an admin reviews it");
      setSwitchTarget(null);
      setSwitchNote("");
      fetchMyRequest();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to submit request");
    } finally {
      setSwitchSubmitting(false);
    }
  };

  const submitReview = async () => {
    if (!reviewTarget || !reviewMode) return;
    setReviewSubmitting(true);
    try {
      if (reviewMode === "approve") {
        await api.post(`/admin/package-requests/${reviewTarget.id}/approve`, {
          payment_reference: reviewPayRef,
          note: reviewNote,
        });
        toast.success(`Approved — ${reviewTarget.vendor_username} moved to ${reviewTarget.to_package_name}`);
      } else {
        await api.post(`/admin/package-requests/${reviewTarget.id}/reject`, {
          reason: reviewNote,
        });
        toast.success(`Rejected ${reviewTarget.vendor_username}'s request`);
      }
      setReviewTarget(null);
      setReviewMode(null);
      setReviewPayRef("");
      setReviewNote("");
      fetchPendingRequests();
      fetchPackages();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to submit review");
    } finally {
      setReviewSubmitting(false);
    }
  };

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
    if (!await confirmAction({ title: "Delete?", description: `Are you sure you want to delete package "${name}"?`, danger: true, confirmLabel: "Delete" })) return;
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
            disabled={p.account_count > 0 || p.is_default}
            className={`p-1.5 rounded transition-colors ${
              p.account_count > 0 || p.is_default
                ? "text-panel-muted/30 cursor-not-allowed"
                : "hover:bg-panel-bg text-panel-muted hover:text-red-400"
            }`}
            title={p.is_default ? "Cannot delete the default package" : p.account_count > 0 ? `Cannot delete: ${p.account_count} active accounts` : "Delete Package"}
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
      {/* Header — title + description differ by role. Owner sees
          "manage the catalog", vendor sees "browse plans". Only the
          owner gets the Add Package button; vendors don't manage the
          catalog from this page. */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">
            {isOwner ? "Hosting Packages" : "Hosting Plans"}
          </h1>
          <p className="text-panel-muted text-sm mt-1">
            {isOwner
              ? "Manage hosting packages with resource limits and settings"
              : "Browse available plans and request an upgrade or downgrade"}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={() => { fetchPackages(); isOwner ? fetchPendingRequests() : fetchMyRequest(); }}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          {isOwner && (
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
          )}
        </div>
      </div>

      {/* Vendor-side "you already asked for a switch" banner. Blocks
          further re-submissions so the admin queue doesn't get
          flooded. Shows the target plan + when it was submitted. */}
      {!isOwner && myRequest && myRequest.status === "pending" && (
        <Card>
          <div className="p-4 flex items-start gap-3 bg-amber-500/5 border border-amber-500/20 rounded-lg">
            <Clock size={18} className="text-amber-400 shrink-0 mt-0.5" />
            <div className="flex-1 min-w-0">
              <div className="text-sm font-medium text-panel-text">
                Plan change request pending admin review
              </div>
              <div className="text-xs text-panel-muted mt-1">
                Switching from <strong className="text-panel-text">{myRequest.from_package_name || "—"}</strong> →{" "}
                <strong className="text-panel-text">{myRequest.to_package_name}</strong>.
                Submitted {new Date(myRequest.created_at).toLocaleString()}. An admin will confirm your payment and apply the change — you'll see it reflected here once approved.
              </div>
              {myRequest.note && (
                <div className="text-xs text-panel-muted/70 mt-1 italic">
                  Your note: "{myRequest.note}"
                </div>
              )}
            </div>
          </div>
        </Card>
      )}

      {/* Owner-side review queue. Shows pending requests at the top
          with Approve / Reject buttons so the admin can process them
          without hunting through Vendors. Hidden when empty. */}
      {isOwner && pendingRequests.length > 0 && (
        <Card>
          <div className="p-4 border-b border-panel-border flex items-center gap-2">
            <AlertCircle size={14} className="text-amber-400" />
            <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
              Pending plan change requests ({pendingRequests.length})
            </h3>
          </div>
          <div className="divide-y divide-panel-border">
            {pendingRequests.map((r) => (
              <div key={r.id} className="p-4 flex items-center justify-between gap-3">
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium text-panel-text">
                    {r.vendor_name} <span className="text-panel-muted text-xs font-normal">· {r.vendor_username}</span>
                  </div>
                  <div className="text-xs text-panel-muted mt-0.5">
                    <span className="font-mono text-panel-text">{r.from_package_name || "—"}</span>
                    <span className="mx-2">→</span>
                    <span className="font-mono text-blue-400">{r.to_package_name}</span>
                    <span className="ml-3">· requested {new Date(r.created_at).toLocaleString()}</span>
                  </div>
                  {r.note && (
                    <div className="text-xs text-panel-muted/70 mt-1 italic truncate">
                      "{r.note}"
                    </div>
                  )}
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <button
                    type="button"
                    onClick={() => { setReviewTarget(r); setReviewMode("approve"); setReviewPayRef(""); setReviewNote(""); }}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-green-600 hover:bg-green-700 text-white rounded-lg text-xs font-medium"
                  >
                    <Check size={12} /> Approve
                  </button>
                  <button
                    type="button"
                    onClick={() => { setReviewTarget(r); setReviewMode("reject"); setReviewNote(""); }}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-red-500/10 hover:bg-red-500/20 text-red-300 border border-red-500/30 rounded-lg text-xs"
                  >
                    <XIcon size={12} /> Reject
                  </button>
                </div>
              </div>
            ))}
          </div>
        </Card>
      )}

      {/* Search — owner-only. Vendors see a small catalog as a card
          grid, so a search bar is noise. */}
      {isOwner && (
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
      )}

      {/* Owner gets the CRUD-capable table; vendor gets a plan-switcher
          grid where each card is a plan with a "Request this plan"
          button. Current plan is visually marked and has no button. */}
      {isOwner ? (
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
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {loading ? (
            [...Array(3)].map((_, i) => (
              <Card key={i}>
                <div className="p-6 h-48 bg-panel-bg rounded-lg animate-pulse" />
              </Card>
            ))
          ) : packages.length === 0 ? (
            <Card>
              <div className="p-12 text-center col-span-full">
                <Box size={40} className="mx-auto text-panel-muted/30 mb-3" />
                <p className="text-panel-muted">No hosting plans available yet.</p>
              </div>
            </Card>
          ) : (
            packages.map((p) => {
              const isCurrent = p.id === currentPackageId;
              const hasPending = Boolean(myRequest && myRequest.status === "pending");
              const isPendingTarget = hasPending && myRequest?.to_package_id === p.id;
              return (
                <Card key={p.id}>
                  <div className={`p-5 ${isCurrent ? "border-t-2 border-blue-500" : ""}`}>
                    <div className="flex items-start justify-between mb-3">
                      <div className="flex items-center gap-2 min-w-0">
                        <Box size={16} className="text-blue-400 shrink-0" />
                        <span className="font-semibold text-panel-text truncate">{p.name}</span>
                      </div>
                      {isCurrent && (
                        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-semibold uppercase bg-blue-500/10 text-blue-300 border border-blue-500/30 shrink-0">
                          <Check size={10} /> Current
                        </span>
                      )}
                      {isPendingTarget && (
                        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-semibold uppercase bg-amber-500/10 text-amber-300 border border-amber-500/30 shrink-0">
                          <Clock size={10} /> Pending
                        </span>
                      )}
                    </div>
                    <div className="grid grid-cols-2 gap-2 text-xs mb-4">
                      <PlanStat icon={<HardDrive size={11} className="text-panel-muted" />} label="Disk" value={formatResource(p.disk_quota_mb, p.disk_quota_unlimited)} />
                      <PlanStat icon={<Wifi size={11} className="text-panel-muted" />} label="Bandwidth" value={formatResource(p.bandwidth_mb, p.bandwidth_unlimited)} />
                      <PlanStat icon={<Mail size={11} className="text-panel-muted" />} label="Email" value={formatResource(p.max_email_accounts, p.max_email_unlimited, "")} />
                      <PlanStat icon={<Database size={11} className="text-panel-muted" />} label="DBs" value={formatResource(p.max_databases, p.max_databases_unlimited, "")} />
                      <PlanStat icon={<Globe size={11} className="text-panel-muted" />} label="Subdomains" value={formatResource(p.max_subdomains, p.max_subdomains_unlimited, "")} />
                      <PlanStat icon={<Box size={11} className="text-panel-muted" />} label="Apps" value={formatResource(p.max_passenger_apps, p.max_passenger_unlimited, "")} />
                    </div>
                    {isCurrent ? (
                      <div className="text-xs text-panel-muted text-center py-2 border-t border-panel-border">
                        This is your active plan
                      </div>
                    ) : hasPending ? (
                      <button
                        type="button"
                        disabled
                        className="w-full py-2 text-xs bg-panel-bg border border-panel-border rounded-lg text-panel-muted cursor-not-allowed"
                        title="You already have a pending request — wait for it to be resolved"
                      >
                        Pending request in progress
                      </button>
                    ) : (
                      <button
                        type="button"
                        onClick={() => { setSwitchTarget(p); setSwitchNote(""); }}
                        className="w-full inline-flex items-center justify-center gap-2 py-2 text-xs bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors"
                      >
                        <ArrowUpCircle size={12} /> Request this plan
                      </button>
                    )}
                  </div>
                </Card>
              );
            })
          )}
        </div>
      )}

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

      {/* Vendor-side Switch modal. Collects an optional note that
          travels with the request so the admin sees why the vendor
          wants to switch (e.g. "need more disk", "downgrading while
          projects are paused"). */}
      <Modal
        isOpen={!!switchTarget && !isOwner}
        onClose={() => { if (!switchSubmitting) { setSwitchTarget(null); setSwitchNote(""); } }}
        title={switchTarget ? `Request plan switch — ${switchTarget.name}` : "Request plan switch"}
      >
        {switchTarget && (
          <div className="p-5 space-y-4">
            <div className="flex items-start gap-3 p-3 bg-amber-500/5 border border-amber-500/20 rounded-lg text-xs">
              <CreditCard size={14} className="text-amber-400 shrink-0 mt-0.5" />
              <div>
                <p className="text-panel-text font-medium">Payment required</p>
                <p className="text-panel-muted mt-1">
                  This request is <strong className="text-panel-text">pending</strong> until an admin confirms your payment. Submit the request, then complete payment through your normal billing channel — the admin will apply the plan change after it clears.
                </p>
              </div>
            </div>
            <div className="grid grid-cols-2 gap-3 text-sm">
              <div>
                <div className="text-[10px] uppercase tracking-wider text-panel-muted">Your current plan</div>
                <div className="text-panel-text font-medium mt-1">{authUser?.package_name || "—"}</div>
              </div>
              <div>
                <div className="text-[10px] uppercase tracking-wider text-panel-muted">Requested plan</div>
                <div className="text-blue-400 font-medium mt-1">{switchTarget.name}</div>
              </div>
            </div>
            <div>
              <label className="block text-xs text-panel-muted mb-1">Note for the admin (optional)</label>
              <textarea
                rows={3}
                value={switchNote}
                onChange={(e) => setSwitchNote(e.target.value)}
                placeholder="e.g. Running out of disk on current plan; please upgrade after I pay invoice #1234"
                className={`${inputClass} resize-y`}
              />
            </div>
            <div className="flex justify-end gap-3 pt-2 border-t border-panel-border">
              <button type="button" onClick={() => { setSwitchTarget(null); setSwitchNote(""); }}
                disabled={switchSubmitting}
                className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg disabled:opacity-50">
                Cancel
              </button>
              <button type="button" onClick={submitSwitchRequest} disabled={switchSubmitting}
                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium disabled:opacity-50">
                {switchSubmitting ? "Submitting…" : "Submit request"}
              </button>
            </div>
          </div>
        )}
      </Modal>

      {/* Admin Approve/Reject modal. Approve takes a payment
          reference that gets stored on the request row for audit.
          Reject takes a reason that the vendor can see. */}
      <Modal
        isOpen={!!reviewTarget && !!reviewMode && isOwner}
        onClose={() => { if (!reviewSubmitting) { setReviewTarget(null); setReviewMode(null); } }}
        title={reviewTarget ? `${reviewMode === "approve" ? "Approve" : "Reject"} — ${reviewTarget.vendor_name}` : "Review request"}
      >
        {reviewTarget && reviewMode && (
          <div className="p-5 space-y-4">
            <div className="text-sm">
              <div className="text-panel-muted mb-1">Plan switch</div>
              <div className="flex items-center gap-2">
                <span className="font-mono text-panel-text">{reviewTarget.from_package_name || "—"}</span>
                <span className="text-panel-muted">→</span>
                <span className="font-mono text-blue-400">{reviewTarget.to_package_name}</span>
              </div>
            </div>
            {reviewTarget.note && (
              <div className="p-3 bg-panel-bg border border-panel-border rounded-lg text-xs text-panel-muted italic">
                Vendor note: "{reviewTarget.note}"
              </div>
            )}
            {reviewMode === "approve" ? (
              <>
                <div>
                  <label className="block text-xs text-panel-muted mb-1">Payment reference (optional)</label>
                  <input
                    type="text"
                    value={reviewPayRef}
                    onChange={(e) => setReviewPayRef(e.target.value)}
                    placeholder="e.g. Stripe ch_1ABC..., Invoice #1234"
                    className={inputClass}
                  />
                  <p className="text-xs text-panel-muted/70 mt-1">Stored on the request for audit.</p>
                </div>
                <div>
                  <label className="block text-xs text-panel-muted mb-1">Note (optional)</label>
                  <textarea
                    rows={2}
                    value={reviewNote}
                    onChange={(e) => setReviewNote(e.target.value)}
                    placeholder="Internal note or message visible to the vendor"
                    className={`${inputClass} resize-y`}
                  />
                </div>
              </>
            ) : (
              <div>
                <label className="block text-xs text-panel-muted mb-1">Reason *</label>
                <textarea
                  rows={3}
                  required
                  value={reviewNote}
                  onChange={(e) => setReviewNote(e.target.value)}
                  placeholder="Why is this being rejected? (Shown to the vendor)"
                  className={`${inputClass} resize-y`}
                />
              </div>
            )}
            <div className="flex justify-end gap-3 pt-2 border-t border-panel-border">
              <button type="button" onClick={() => { setReviewTarget(null); setReviewMode(null); }}
                disabled={reviewSubmitting}
                className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg disabled:opacity-50">
                Cancel
              </button>
              <button type="button" onClick={submitReview} disabled={reviewSubmitting || (reviewMode === "reject" && !reviewNote.trim())}
                className={`px-4 py-2 text-sm rounded-lg font-medium text-white disabled:opacity-50 ${
                  reviewMode === "approve"
                    ? "bg-green-600 hover:bg-green-700"
                    : "bg-red-600 hover:bg-red-700"
                }`}>
                {reviewSubmitting
                  ? "Submitting…"
                  : reviewMode === "approve"
                    ? "Approve + apply plan"
                    : "Reject request"}
              </button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}

// PlanStat is a compact label+value tile used inside each vendor-side
// plan card. Keeps the grid visually consistent across plans even when
// some fields are "Unlimited" and render as a JSX span.
function PlanStat({ icon, label, value }: { icon: React.ReactNode; label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-2 px-2 py-1.5 bg-panel-bg rounded-md border border-panel-border/60">
      <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-panel-muted">
        {icon}{label}
      </div>
      <div className="text-panel-text font-medium truncate">{value}</div>
    </div>
  );
}
