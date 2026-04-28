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

// SslRow is one rendered row in the SSL table. ALWAYS keyed on the
// domain — `cert` is null when no SSL has been issued for that domain
// yet, so the row can render a "No SSL" badge + an Issue CTA instead
// of leaving the operator to guess what's missing.
interface SslRow {
  domain: string;
  cert: SslCertificate | null;
  owner_email?: string;
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

  // Bulk-issue form state. There used to be an `issueEmail` field
  // here that the UI tried to keep in sync with the first selected
  // domain's owner_email — but the user explicitly asked us to drop
  // it: every cert should be registered under the OWNING vendor's
  // email per-domain, automatically, with no shared field for the
  // operator to fiddle with. The backend now resolves the email
  // per-domain inside IssueLetsEncrypt, so the bulk request just
  // sends {domains, wildcard} and the cert for konsultkaro.in lands
  // under konsultkaro@gmail.com while the cert for betazeninfotech.com
  // lands under its own vendor in the same batch.
  const [pickerSearch, setPickerSearch] = useState("");
  const [pickerFilter, setPickerFilter] = useState<DomainFilter>("all");
  const [selected, setSelected] = useState<Set<string>>(new Set());
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

  // Email auto-fill effect was removed when the email field itself
  // was dropped from the modal — see the comment on the bulk-issue
  // state block above.

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
    setIssuing(true);
    const submittedDomains = Array.from(selected);
    try {
      // No `email` in the payload — the backend resolves it
      // per-domain from each domain's owning vendor (so a multi-
      // vendor batch lands each cert under the right address).
      const res = await api.post("/ssl/letsencrypt/bulk", {
        domains: submittedDomains,
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

  // mergedRows is the canonical data model for the SSL list: ONE row
  // per domain, with the matching cert attached when one exists. A
  // domain that has no cert in the panel still gets a row so the
  // operator can see it and trigger Issue from the same surface — the
  // previous version showed only domains-with-certs, hiding the very
  // domains that needed action.
  const mergedRows = useMemo<SslRow[]>(() => {
    const certByDomain = new Map<string, SslCertificate>();
    for (const c of certificates) certByDomain.set(c.domain, c);

    const rows: SslRow[] = [];
    const seen = new Set<string>();

    // Start from the domain inventory so every domain appears in the
    // list — even ones the panel hasn't issued a cert for yet.
    for (const d of domains) {
      const cert = certByDomain.get(d.domain) || null;
      rows.push({ domain: d.domain, cert, owner_email: d.owner_email });
      seen.add(d.domain);
    }
    // Catch certs whose Domain doc was deleted but the cert record
    // still exists (rare, but a real edge case after a domain delete
    // race). Keeps the operator from being confused by an SSL record
    // that has nowhere to act on.
    for (const c of certificates) {
      if (seen.has(c.domain)) continue;
      rows.push({ domain: c.domain, cert: c, owner_email: undefined });
    }
    rows.sort((a, b) => a.domain.localeCompare(b.domain));
    return rows;
  }, [certificates, domains]);

  // Active = a cert exists AND it's not yet expired.
  // Inactive = no cert at all OR cert is expired. Matches the user's
  // mental model: "do I need to act on this row?"
  const isActiveRow = (r: SslRow): boolean => {
    if (!r.cert) return false;
    return getCertStatus(r.cert) !== "expired";
  };

  const filteredRows = useMemo(() => {
    const q = search.trim().toLowerCase();
    return mergedRows.filter((r) => {
      if (q && !r.domain.toLowerCase().includes(q)) return false;
      if (statusFilter === "active" && !isActiveRow(r)) return false;
      if (statusFilter === "inactive" && isActiveRow(r)) return false;
      return true;
    });
  }, [mergedRows, search, statusFilter]);

  const counts = useMemo(() => {
    let active = 0;
    for (const r of mergedRows) if (isActiveRow(r)) active++;
    return { all: mergedRows.length, active, inactive: mergedRows.length - active };
  }, [mergedRows]);

  // Open the bulk-issue modal pre-selected with a single domain. Same
  // flow as bulk Issue but lets the operator act on a specific row
  // without manually scrolling the picker. Per-domain ACME email is
  // auto-resolved server-side from the domain's owning vendor.
  const issueForOne = (domain: string) => {
    setSelected(new Set([domain]));
    setPickerSearch("");
    setPickerFilter("all");
    setWildcard(false);
    setShowIssue(true);
    fetchDomains();
  };

  const columns = [
    {
      header: "Domain",
      accessor: (r: SslRow) => (
        <div className="flex items-center gap-2">
          <ShieldCheck
            size={14}
            className={
              !r.cert
                ? "text-panel-muted/50"
                : getCertStatus(r.cert) === "expired"
                  ? "text-red-400"
                  : "text-green-400"
            }
          />
          <span className="font-medium text-panel-text">{r.domain}</span>
        </div>
      ),
    },
    {
      header: "Type",
      accessor: (r: SslRow) =>
        r.cert ? (
          <span
            className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
              r.cert.type === "letsencrypt"
                ? "bg-green-500/10 text-green-400"
                : "bg-blue-500/10 text-blue-400"
            }`}
          >
            {r.cert.type === "letsencrypt" ? "Let's Encrypt" : "Custom"}
          </span>
        ) : (
          <span className="text-xs text-panel-muted">—</span>
        ),
    },
    {
      header: "Expires",
      accessor: (r: SslRow) =>
        r.cert ? (
          <div>
            <span className="text-panel-muted text-sm">{formatDate(r.cert.expires_at)}</span>
            {r.cert.days_remaining > 0 && r.cert.days_remaining <= 30 && (
              <span className="ml-2 text-xs text-yellow-400">
                ({r.cert.days_remaining}d left)
              </span>
            )}
            {r.cert.days_remaining <= 0 && r.cert.expires_at && (
              <span className="ml-2 text-xs text-red-400">Expired</span>
            )}
          </div>
        ) : (
          <span className="text-xs text-panel-muted">—</span>
        ),
    },
    {
      header: "Status",
      accessor: (r: SslRow) =>
        !r.cert ? (
          <StatusBadge status="inactive" />
        ) : (
          <StatusBadge
            status={
              getCertStatus(r.cert) === "expired"
                ? "expired"
                : getCertStatus(r.cert) === "expiring"
                  ? "warning"
                  : "active"
            }
          />
        ),
    },
    {
      header: "Force SSL",
      accessor: (r: SslRow) =>
        r.cert ? (
          <button
            onClick={() => handleForceSSL(r.cert!.domain, !r.cert!.force_ssl)}
            className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium transition-colors ${
              r.cert.force_ssl
                ? "bg-green-500/10 text-green-400 hover:bg-green-500/20"
                : "bg-panel-border/30 text-panel-muted hover:bg-panel-border/50"
            }`}
            title={r.cert.force_ssl ? "Click to disable Force SSL" : "Click to enable Force SSL"}
          >
            {r.cert.force_ssl ? <Lock size={12} /> : <LockOpen size={12} />}
            {r.cert.force_ssl ? "Enabled" : "Disabled"}
          </button>
        ) : (
          <span className="text-xs text-panel-muted">—</span>
        ),
    },
    {
      header: "Actions",
      accessor: (r: SslRow) =>
        !r.cert ? (
          // No cert yet — single CTA that opens the bulk modal
          // pre-selected with this row, so the operator goes straight
          // from "I see this domain has no SSL" to "issuing one click".
          <button
            onClick={() => issueForOne(r.domain)}
            className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-medium bg-blue-600/10 text-blue-400 hover:bg-blue-600/20 transition-colors"
            title="Issue Let's Encrypt certificate for this domain"
          >
            <Plus size={12} />
            Issue SSL
          </button>
        ) : (
          <div className="flex items-center gap-1">
            <button className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors">
              <Eye size={14} />
            </button>
            <button
              onClick={() => handleRenew(r.cert!.domain)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
              title="Renew"
            >
              <Download size={14} />
            </button>
            <button
              onClick={() => handleDelete(r.cert!.domain)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
              title="Delete"
            >
              <Trash2 size={14} />
            </button>
          </div>
        ),
    },
  ];

  // mixedOwners detection used to drive an "ACME email mismatch"
  // warning when one shared email had to cover the whole batch.
  // The shared field is gone now — backend resolves the email
  // per-domain — so this is no longer needed.

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
        ) : filteredRows.length > 0 ? (
          <Table columns={columns} data={filteredRows} />
        ) : (
          <div className="text-center py-16 px-4">
            <ShieldCheck size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">
              {mergedRows.length === 0 ? "No domains yet" : "Nothing matches your filters"}
            </h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {mergedRows.length === 0
                ? "Add a domain first, then come back here to issue an SSL certificate."
                : "Try a different search term or status filter."}
            </p>
            {mergedRows.length > 0 && (
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

          {/* No shared email field — each cert in the batch is
              registered under its own owning vendor's email
              (server-resolved per-domain). The picker rows above
              already display each vendor's email so the operator
              can sanity-check the routing before clicking Issue. */}
          <div className="rounded-lg border border-blue-500/20 bg-blue-500/5 px-3 py-2 text-xs text-blue-200/80 flex items-start gap-2">
            <AlertTriangle size={12} className="text-blue-300 mt-0.5 shrink-0" />
            <span>
              ACME registration email is set automatically per domain
              from each domain's owning vendor — a multi-vendor batch
              lands every cert under the right address. Vendor emails
              are shown next to each row above.
            </span>
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
