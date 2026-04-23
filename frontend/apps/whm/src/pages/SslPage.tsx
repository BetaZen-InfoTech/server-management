import { useState, useEffect, useMemo } from "react";
import { Card, Button, Table, StatusBadge, Modal, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  ShieldCheck,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  Download,
  Eye,
  Lock,
  LockOpen,
  Upload,
  CheckCircle2,
  XCircle,
  AlertTriangle,
} from "lucide-react";

interface SslCertificate {
  id: string;
  domain: string;
  type: "letsencrypt" | "custom";
  expires_at: string | null;
  days_remaining: number;
  force_ssl: boolean;
  issued_at: string | null;
  issuer: string;
  auto_renew: boolean;
  domains: string[];
}

interface DomainOption {
  id: string;
  domain: string;
  ssl_active: boolean;
  ssl_expires?: string | null;
  user?: string;
  owner_email?: string;
}

interface BulkItem {
  domain: string;
  success: boolean;
  error?: string;
  cert_id?: string;
  expires_at?: string | null;
}

interface BulkResponse {
  total: number;
  success: number;
  failed: number;
  items: BulkItem[];
}

type CertStatus = "active" | "expiring" | "expired";
type CertFilter = "all" | "active" | "inactive";
type DomainFilter = "all" | "active" | "inactive";

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

function getCertStatus(cert: SslCertificate): CertStatus {
  if (!cert.expires_at) return "active";
  if (cert.days_remaining <= 0) return "expired";
  if (cert.days_remaining <= 30) return "expiring";
  return "active";
}

// Domain "active" SSL = a panel-tracked cert exists and isn't expired.
// Mirrors the badge colour rules so the filter and the visual stay in
// lockstep — clicking "Active" only ever shows green-badged rows.
function domainSslActive(d: DomainOption): boolean {
  if (!d.ssl_active) return false;
  if (!d.ssl_expires) return true;
  const exp = new Date(d.ssl_expires).getTime();
  if (Number.isNaN(exp)) return true;
  return exp > Date.now();
}

function formatDate(dateStr: string | null | undefined): string {
  if (!dateStr) return "N/A";
  try {
    return new Date(dateStr).toLocaleDateString("en-US", {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
  } catch {
    return dateStr;
  }
}

export default function SslPage() {
  const [certificates, setCertificates] = useState<SslCertificate[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<CertFilter>("all");

  const [showIssue, setShowIssue] = useState(false);
  const [showUpload, setShowUpload] = useState(false);
  const [showResults, setShowResults] = useState(false);
  const [bulkResult, setBulkResult] = useState<BulkResponse | null>(null);
  const [issuing, setIssuing] = useState(false);
  const [uploading, setUploading] = useState(false);

  const [domains, setDomains] = useState<DomainOption[]>([]);
  const [domainsLoading, setDomainsLoading] = useState(false);

  // Bulk-issue form state
  const [pickerSearch, setPickerSearch] = useState("");
  const [pickerFilter, setPickerFilter] = useState<DomainFilter>("all");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [issueEmail, setIssueEmail] = useState("");
  const [emailEdited, setEmailEdited] = useState(false);
  const [wildcard, setWildcard] = useState(false);

  // Single-domain custom upload
  const [uploadForm, setUploadForm] = useState({
    domain: "",
    certificate: "",
    private_key: "",
    ca_bundle: "",
  });

  useEffect(() => {
    fetchCertificates();
    fetchDomains();
  }, []);

  // Auto-fill email whenever the selection changes — but back off the
  // moment the operator types into the field manually so we don't fight
  // their edits. Picks the first selected domain's owner_email; a
  // batch crossing two vendors stays on whichever one was selected
  // first (the operator can override).
  useEffect(() => {
    if (emailEdited) return;
    if (selected.size === 0) {
      setIssueEmail("");
      return;
    }
    const firstSelected = domains.find(
      (d) => selected.has(d.domain) && d.owner_email
    );
    if (firstSelected?.owner_email) {
      setIssueEmail(firstSelected.owner_email);
    }
  }, [selected, domains, emailEdited]);

  const fetchDomains = async () => {
    setDomainsLoading(true);
    try {
      // limit=500 matches the cPanel pattern — single page gives the
      // operator the full inventory in the picker without paging UI.
      const res = await api.get("/domains", { params: { limit: 500 } });
      setDomains(res.data.data || []);
    } catch {
      setDomains([]);
    } finally {
      setDomainsLoading(false);
    }
  };

  const fetchCertificates = async () => {
    setLoading(true);
    try {
      const res = await api.get("/ssl/");
      setCertificates(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const openIssue = () => {
    setSelected(new Set());
    setPickerSearch("");
    setPickerFilter("all");
    setIssueEmail("");
    setEmailEdited(false);
    setWildcard(false);
    setShowIssue(true);
    // Re-fetch in case domains were added in another tab.
    fetchDomains();
  };

  const filteredPickerDomains = useMemo(() => {
    const q = pickerSearch.trim().toLowerCase();
    return domains.filter((d) => {
      if (q && !d.domain.toLowerCase().includes(q)) return false;
      if (pickerFilter === "active" && !domainSslActive(d)) return false;
      if (pickerFilter === "inactive" && domainSslActive(d)) return false;
      return true;
    });
  }, [domains, pickerSearch, pickerFilter]);

  const toggleOne = (domain: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(domain)) next.delete(domain);
      else next.add(domain);
      return next;
    });
  };

  const toggleAllVisible = () => {
    const visibleDomains = filteredPickerDomains.map((d) => d.domain);
    const allSelected = visibleDomains.every((d) => selected.has(d));
    setSelected((prev) => {
      const next = new Set(prev);
      if (allSelected) {
        visibleDomains.forEach((d) => next.delete(d));
      } else {
        visibleDomains.forEach((d) => next.add(d));
      }
      return next;
    });
  };

  const handleBulkIssue = async (e: React.FormEvent) => {
    e.preventDefault();
    if (selected.size === 0) {
      toast.error("Select at least one domain");
      return;
    }
    if (!issueEmail.trim()) {
      toast.error("Email is required for Let's Encrypt registration");
      return;
    }
    setIssuing(true);
    const submittedDomains = Array.from(selected);
    try {
      const res = await api.post("/ssl/letsencrypt/bulk", {
        domains: submittedDomains,
        email: issueEmail.trim(),
        wildcard,
      });
      const data: BulkResponse = res.data.data;
      setBulkResult(data);
      setShowIssue(false);
      setShowResults(true);
      if (data.failed === 0) {
        toast.success(`Issued ${data.success} certificate${data.success === 1 ? "" : "s"}`);
      } else if (data.success === 0) {
        toast.error(`Failed to issue ${data.failed} certificate${data.failed === 1 ? "" : "s"}`);
      } else {
        toast(`Issued ${data.success}, failed ${data.failed}`, { icon: "⚠️" });
      }
      fetchCertificates();
      fetchDomains();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Bulk issue failed");
    } finally {
      setIssuing(false);
    }
  };

  const handleUploadCustom = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!uploadForm.domain) {
      toast.error("Select a domain");
      return;
    }
    if (!uploadForm.certificate.trim() || !uploadForm.private_key.trim()) {
      toast.error("Certificate and private key are required");
      return;
    }
    setUploading(true);
    try {
      await api.post("/ssl/custom", uploadForm);
      toast.success(`Custom certificate installed on ${uploadForm.domain}`);
      setShowUpload(false);
      setUploadForm({ domain: "", certificate: "", private_key: "", ca_bundle: "" });
      fetchCertificates();
      fetchDomains();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to upload certificate");
    } finally {
      setUploading(false);
    }
  };

  const handleDelete = async (domain: string) => {
    if (
      !(await confirmAction({
        title: "Delete SSL?",
        description: `Remove the SSL certificate for ${domain}? On-disk material is preserved.`,
        danger: true,
        confirmLabel: "Delete",
      }))
    )
      return;
    try {
      await api.delete(`/ssl/${domain}`);
      toast.success(`SSL certificate for ${domain} deleted`);
      fetchCertificates();
      fetchDomains();
    } catch {
      toast.error("Failed to delete certificate");
    }
  };

  const handleForceSSL = async (domain: string, enable: boolean) => {
    try {
      await api.post(`/ssl/${domain}/force-ssl`, { enable });
      toast.success(enable ? "Force SSL enabled" : "Force SSL disabled");
      setCertificates((prev) =>
        prev.map((c) => (c.domain === domain ? { ...c, force_ssl: enable } : c))
      );
    } catch {
      toast.error("Failed to update Force SSL");
    }
  };

  const handleRenew = async (domain: string) => {
    try {
      await api.post(`/ssl/${domain}/renew`);
      toast.success("Certificate renewal initiated");
      fetchCertificates();
    } catch {
      toast.error("Failed to renew certificate");
    }
  };

  const filteredCerts = useMemo(() => {
    const q = search.trim().toLowerCase();
    return certificates.filter((c) => {
      if (q && !c.domain.toLowerCase().includes(q)) return false;
      if (statusFilter !== "all") {
        const st = getCertStatus(c);
        // "Active" = healthy or expiring (still serving traffic).
        // "Inactive" = expired only (already broken). Matches the user's
        // mental model from the screenshot's status column.
        if (statusFilter === "active" && st === "expired") return false;
        if (statusFilter === "inactive" && st !== "expired") return false;
      }
      return true;
    });
  }, [certificates, search, statusFilter]);

  const counts = useMemo(() => {
    const all = certificates.length;
    let expired = 0;
    let expiring = 0;
    for (const c of certificates) {
      const st = getCertStatus(c);
      if (st === "expired") expired++;
      if (st === "expiring") expiring++;
    }
    return { all, active: all - expired, inactive: expired, expiring };
  }, [certificates]);

  const columns = [
    {
      header: "Domain",
      accessor: (c: SslCertificate) => (
        <div className="flex items-center gap-2">
          <ShieldCheck
            size={14}
            className={getCertStatus(c) === "expired" ? "text-red-400" : "text-green-400"}
          />
          <span className="font-medium text-panel-text">{c.domain}</span>
        </div>
      ),
    },
    {
      header: "Type",
      accessor: (c: SslCertificate) => (
        <span
          className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
            c.type === "letsencrypt"
              ? "bg-green-500/10 text-green-400"
              : "bg-blue-500/10 text-blue-400"
          }`}
        >
          {c.type === "letsencrypt" ? "Let's Encrypt" : "Custom"}
        </span>
      ),
    },
    {
      header: "Expires",
      accessor: (c: SslCertificate) => (
        <div>
          <span className="text-panel-muted text-sm">{formatDate(c.expires_at)}</span>
          {c.days_remaining > 0 && c.days_remaining <= 30 && (
            <span className="ml-2 text-xs text-yellow-400">({c.days_remaining}d left)</span>
          )}
          {c.days_remaining <= 0 && c.expires_at && (
            <span className="ml-2 text-xs text-red-400">Expired</span>
          )}
        </div>
      ),
    },
    {
      header: "Status",
      accessor: (c: SslCertificate) => (
        <StatusBadge
          status={
            getCertStatus(c) === "expired"
              ? "expired"
              : getCertStatus(c) === "expiring"
                ? "warning"
                : "active"
          }
        />
      ),
    },
    {
      header: "Force SSL",
      accessor: (c: SslCertificate) => (
        <button
          onClick={() => handleForceSSL(c.domain, !c.force_ssl)}
          className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium transition-colors ${
            c.force_ssl
              ? "bg-green-500/10 text-green-400 hover:bg-green-500/20"
              : "bg-panel-border/30 text-panel-muted hover:bg-panel-border/50"
          }`}
          title={c.force_ssl ? "Click to disable Force SSL" : "Click to enable Force SSL"}
        >
          {c.force_ssl ? <Lock size={12} /> : <LockOpen size={12} />}
          {c.force_ssl ? "Enabled" : "Disabled"}
        </button>
      ),
    },
    {
      header: "Actions",
      accessor: (c: SslCertificate) => (
        <div className="flex items-center gap-1">
          <button className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors">
            <Eye size={14} />
          </button>
          <button
            onClick={() => handleRenew(c.domain)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
            title="Renew"
          >
            <Download size={14} />
          </button>
          <button
            onClick={() => handleDelete(c.domain)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
          >
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  // Identify whether the auto-filled email maps to *all* selected
  // rows. When mixed (different vendors), surface an inline note so
  // the operator notices instead of silently registering the LE
  // account under one vendor's address for someone else's domain.
  const mixedOwners = useMemo(() => {
    if (selected.size < 2) return false;
    const owners = new Set<string>();
    for (const d of domains) {
      if (selected.has(d.domain) && d.owner_email) owners.add(d.owner_email);
    }
    return owners.size > 1;
  }, [selected, domains]);

  const visibleAllSelected =
    filteredPickerDomains.length > 0 &&
    filteredPickerDomains.every((d) => selected.has(d.domain));

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">SSL/TLS Certificates</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage SSL certificates for your domains
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchCertificates}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => setShowUpload(true)}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm"
          >
            <Upload size={14} />
            Upload Custom
          </Button>
          <Button
            onClick={openIssue}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Issue Certificate
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4 flex flex-col md:flex-row md:items-center gap-3">
          <div className="relative flex-1">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search certificates..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
          <div className="flex items-center gap-1 bg-panel-bg border border-panel-border rounded-lg p-1">
            {(
              [
                { value: "all" as const, label: `All (${counts.all})` },
                { value: "active" as const, label: `Active (${counts.active})` },
                { value: "inactive" as const, label: `Inactive (${counts.inactive})` },
              ]
            ).map((f) => (
              <button
                key={f.value}
                onClick={() => setStatusFilter(f.value)}
                className={`px-3 py-1.5 rounded-md text-xs font-medium transition-colors ${
                  statusFilter === f.value
                    ? "bg-blue-600 text-white"
                    : "text-panel-muted hover:text-panel-text"
                }`}
              >
                {f.label}
              </button>
            ))}
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
        ) : filteredCerts.length > 0 ? (
          <Table columns={columns} data={filteredCerts} />
        ) : (
          <div className="text-center py-16 px-4">
            <ShieldCheck size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">
              No SSL certificates found
            </h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search || statusFilter !== "all"
                ? "No certificates match your filters."
                : "Issue your first SSL certificate to secure your domains with HTTPS."}
            </p>
            {!search && statusFilter === "all" && (
              <Button
                onClick={openIssue}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Issue Certificate
              </Button>
            )}
          </div>
        )}
      </Card>

      {/* Bulk Let's Encrypt issue modal */}
      <Modal
        isOpen={showIssue}
        onClose={() => setShowIssue(false)}
        title="Issue Let's Encrypt Certificates"
        size="xl"
      >
        <form onSubmit={handleBulkIssue} className="space-y-4">
          <div className="flex items-center gap-3">
            <div className="relative flex-1">
              <Search
                size={14}
                className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
              />
              <input
                type="text"
                placeholder="Search domains..."
                value={pickerSearch}
                onChange={(e) => setPickerSearch(e.target.value)}
                className="w-full pl-9 pr-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder-panel-muted/60 focus:outline-none focus:ring-2 focus:ring-blue-500/40"
              />
            </div>
            <div className="flex items-center gap-1 bg-panel-bg border border-panel-border rounded-lg p-1">
              {(
                [
                  { value: "all" as const, label: "All" },
                  { value: "active" as const, label: "Active SSL" },
                  { value: "inactive" as const, label: "No SSL" },
                ]
              ).map((f) => (
                <button
                  key={f.value}
                  type="button"
                  onClick={() => setPickerFilter(f.value)}
                  className={`px-3 py-1.5 rounded-md text-xs font-medium transition-colors ${
                    pickerFilter === f.value
                      ? "bg-blue-600 text-white"
                      : "text-panel-muted hover:text-panel-text"
                  }`}
                >
                  {f.label}
                </button>
              ))}
            </div>
          </div>

          <div className="border border-panel-border rounded-lg overflow-hidden">
            <div className="flex items-center justify-between px-3 py-2 bg-panel-bg/50 border-b border-panel-border">
              <label className="flex items-center gap-2 text-xs text-panel-muted cursor-pointer">
                <input
                  type="checkbox"
                  checked={visibleAllSelected}
                  onChange={toggleAllVisible}
                  className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600"
                />
                {visibleAllSelected ? "Deselect all" : "Select all visible"}
              </label>
              <span className="text-xs text-panel-muted">
                {selected.size} selected · {filteredPickerDomains.length} shown
              </span>
            </div>
            <div className="max-h-72 overflow-y-auto divide-y divide-panel-border/40">
              {domainsLoading ? (
                <div className="p-6 text-center text-sm text-panel-muted">
                  Loading domains...
                </div>
              ) : filteredPickerDomains.length === 0 ? (
                <div className="p-6 text-center text-sm text-panel-muted">
                  No domains match your filter.
                </div>
              ) : (
                filteredPickerDomains.map((d) => {
                  const checked = selected.has(d.domain);
                  const active = domainSslActive(d);
                  return (
                    <label
                      key={d.id}
                      className={`flex items-center gap-3 px-3 py-2 cursor-pointer hover:bg-panel-bg/40 transition-colors ${
                        checked ? "bg-blue-500/5" : ""
                      }`}
                    >
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={() => toggleOne(d.domain)}
                        className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600"
                      />
                      <ShieldCheck
                        size={14}
                        className={active ? "text-green-400" : "text-panel-muted/50"}
                      />
                      <span className="font-mono text-sm text-panel-text flex-1 truncate">
                        {d.domain}
                      </span>
                      {d.owner_email && (
                        <span className="text-xs text-panel-muted hidden md:inline">
                          {d.owner_email}
                        </span>
                      )}
                      <span
                        className={`text-xs px-2 py-0.5 rounded-full ${
                          active
                            ? "bg-green-500/10 text-green-400"
                            : "bg-panel-border/30 text-panel-muted"
                        }`}
                      >
                        {active ? "active" : "no SSL"}
                      </span>
                    </label>
                  );
                })
              )}
            </div>
          </div>

          <div>
            <label className={labelClass}>Email *</label>
            <input
              type="email"
              required
              value={issueEmail}
              onChange={(e) => {
                setIssueEmail(e.target.value);
                setEmailEdited(true);
              }}
              placeholder="vendor@example.com"
              className={inputClass}
            />
            <p className="text-xs text-panel-muted mt-1">
              Auto-filled from the selected domain's owning vendor. Used for
              Let's Encrypt account registration and expiry notices.
            </p>
            {mixedOwners && (
              <p className="text-xs text-yellow-400 mt-1 flex items-center gap-1">
                <AlertTriangle size={12} />
                Selected domains belong to different vendors — confirm the email is correct.
              </p>
            )}
          </div>

          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="wildcard"
              checked={wildcard}
              onChange={(e) => setWildcard(e.target.checked)}
              className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 focus:ring-blue-500/40"
            />
            <label htmlFor="wildcard" className="text-sm text-panel-text">
              Wildcard certificate (*.&lt;domain&gt;) for every selected domain
            </label>
          </div>

          <div className="flex items-center justify-between pt-2 border-t border-panel-border/40">
            <p className="text-xs text-panel-muted">
              Certificates are issued one at a time on the server — large batches may take a few minutes.
            </p>
            <div className="flex gap-3">
              <button
                type="button"
                onClick={() => setShowIssue(false)}
                className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={issuing || selected.size === 0}
                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {issuing
                  ? `Issuing ${selected.size}...`
                  : `Issue ${selected.size || ""} Certificate${selected.size === 1 ? "" : "s"}`.trim()}
              </button>
            </div>
          </div>
        </form>
      </Modal>

      {/* Custom upload modal — single domain on purpose */}
      <Modal
        isOpen={showUpload}
        onClose={() => setShowUpload(false)}
        title="Upload Custom Certificate"
        size="lg"
      >
        <form onSubmit={handleUploadCustom} className="space-y-4">
          <div>
            <label className={labelClass}>Domain *</label>
            <select
              required
              value={uploadForm.domain}
              onChange={(e) => setUploadForm({ ...uploadForm, domain: e.target.value })}
              className={inputClass}
            >
              <option value="">Select a domain</option>
              {domains.map((d) => (
                <option key={d.id} value={d.domain}>
                  {d.domain}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className={labelClass}>Certificate (PEM) *</label>
            <textarea
              required
              value={uploadForm.certificate}
              onChange={(e) => setUploadForm({ ...uploadForm, certificate: e.target.value })}
              placeholder="-----BEGIN CERTIFICATE-----"
              className={`${inputClass} h-24 font-mono text-xs`}
            />
          </div>
          <div>
            <label className={labelClass}>Private Key (PEM) *</label>
            <textarea
              required
              value={uploadForm.private_key}
              onChange={(e) => setUploadForm({ ...uploadForm, private_key: e.target.value })}
              placeholder="-----BEGIN PRIVATE KEY-----"
              className={`${inputClass} h-24 font-mono text-xs`}
            />
          </div>
          <div>
            <label className={labelClass}>CA Bundle (optional)</label>
            <textarea
              value={uploadForm.ca_bundle}
              onChange={(e) => setUploadForm({ ...uploadForm, ca_bundle: e.target.value })}
              placeholder="-----BEGIN CERTIFICATE-----"
              className={`${inputClass} h-24 font-mono text-xs`}
            />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setShowUpload(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={uploading}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50"
            >
              {uploading ? "Uploading..." : "Upload Certificate"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Per-domain bulk results */}
      <Modal
        isOpen={showResults}
        onClose={() => setShowResults(false)}
        title="Bulk Issue Results"
        size="lg"
      >
        {bulkResult && (
          <div className="space-y-4">
            <div className="grid grid-cols-3 gap-3">
              <div className="rounded-lg border border-panel-border bg-panel-bg/30 p-3">
                <div className="text-xs text-panel-muted">Total</div>
                <div className="text-xl font-semibold text-panel-text">{bulkResult.total}</div>
              </div>
              <div className="rounded-lg border border-green-500/30 bg-green-500/5 p-3">
                <div className="text-xs text-green-300">Succeeded</div>
                <div className="text-xl font-semibold text-green-400">{bulkResult.success}</div>
              </div>
              <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3">
                <div className="text-xs text-red-300">Failed</div>
                <div className="text-xl font-semibold text-red-400">{bulkResult.failed}</div>
              </div>
            </div>
            <div className="border border-panel-border rounded-lg max-h-80 overflow-y-auto divide-y divide-panel-border/40">
              {bulkResult.items.map((item) => (
                <div key={item.domain} className="flex items-start gap-3 px-3 py-2">
                  {item.success ? (
                    <CheckCircle2 size={16} className="text-green-400 mt-0.5 shrink-0" />
                  ) : (
                    <XCircle size={16} className="text-red-400 mt-0.5 shrink-0" />
                  )}
                  <div className="flex-1 min-w-0">
                    <div className="font-mono text-sm text-panel-text truncate">{item.domain}</div>
                    {item.success ? (
                      <div className="text-xs text-panel-muted">
                        Issued · expires {formatDate(item.expires_at)}
                      </div>
                    ) : (
                      <div className="text-xs text-red-300 break-words">{item.error}</div>
                    )}
                  </div>
                </div>
              ))}
            </div>
            <div className="flex justify-end pt-2">
              <button
                type="button"
                onClick={() => setShowResults(false)}
                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors"
              >
                Done
              </button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
