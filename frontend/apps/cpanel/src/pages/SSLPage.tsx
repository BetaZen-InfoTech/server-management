import React, { useEffect, useMemo, useState } from "react";
import { Card, Button, Table, Modal, StatusBadge, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  ShieldCheck,
  Plus,
  Trash2,
  Search,
  RefreshCw,
  Upload,
  Ban,
  Lock,
  LockOpen,
  Sparkles,
  Repeat,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  RotateCw,
} from "lucide-react";

interface SslCertificate {
  id: string;
  domain: string;
  issuer: string;
  type: string;
  status: string;
  expires_at: string | null;
  days_remaining: number;
  wildcard: boolean;
  auto_renew: boolean;
  force_ssl?: boolean;
  serial_number?: string;
  key_type?: string;
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

type CertFilter = "all" | "active" | "inactive";
type DomainFilter = "all" | "active" | "inactive";

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500";
const labelClass = "block text-sm font-medium text-panel-text mb-1.5";

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

function daysColor(days: number): string {
  if (days < 7) return "text-red-400";
  if (days < 30) return "text-yellow-400";
  return "text-green-400";
}

// "active" SSL on a domain row = panel-tracked + not yet expired.
// Same definition as the WHM page so the filter chip behaves
// identically across panels.
function domainSslActive(d: DomainOption): boolean {
  if (!d.ssl_active) return false;
  if (!d.ssl_expires) return true;
  const exp = new Date(d.ssl_expires).getTime();
  if (Number.isNaN(exp)) return true;
  return exp > Date.now();
}

function certIsExpired(c: SslCertificate): boolean {
  if (!c.expires_at) return false;
  return c.days_remaining <= 0;
}

export default function SslPage() {
  const [certs, setCerts] = useState<SslCertificate[]>([]);
  const [domains, setDomains] = useState<DomainOption[]>([]);
  const [loading, setLoading] = useState(true);
  const [domainsLoading, setDomainsLoading] = useState(false);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<CertFilter>("all");

  const [showIssue, setShowIssue] = useState(false);
  const [showUpload, setShowUpload] = useState(false);
  const [showResults, setShowResults] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [bulkResult, setBulkResult] = useState<BulkResponse | null>(null);

  // Bulk picker state. Email field intentionally absent — the
  // backend resolves the ACME registration email per-domain from
  // each domain's owning vendor (matches the WHM SSL page). For
  // a vendor logged into the User Panel that's almost always
  // their own email; for staff/customer accounts under a vendor
  // it's still the parent vendor's email — exactly what we want
  // to register the cert under.
  const [pickerSearch, setPickerSearch] = useState("");
  const [pickerFilter, setPickerFilter] = useState<DomainFilter>("all");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [wildcard, setWildcard] = useState(false);

  const [uploadForm, setUploadForm] = useState({
    domain: "",
    cert: "",
    key: "",
    ca_bundle: "",
  });

  const fetchCerts = async () => {
    setLoading(true);
    try {
      const res = await api.get("/ssl");
      setCerts(res.data.data || []);
    } catch {
      toast.error("Failed to load SSL certificates");
    } finally {
      setLoading(false);
    }
  };

  const fetchDomains = async () => {
    setDomainsLoading(true);
    try {
      const res = await api.get("/domains", { params: { limit: 500 } });
      setDomains(res.data.data || []);
    } catch {
      // keep empty; user can retry
    } finally {
      setDomainsLoading(false);
    }
  };

  useEffect(() => {
    fetchCerts();
    fetchDomains();
  }, []);

  // Email autofill effect was removed when the email field itself
  // was dropped from the modal. Backend resolves the ACME email
  // per-domain from each domain's owning vendor.

  const openIssue = () => {
    setSelected(new Set());
    setPickerSearch("");
    setPickerFilter("all");
    setWildcard(false);
    setShowIssue(true);
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

  const visibleAllSelected =
    filteredPickerDomains.length > 0 &&
    filteredPickerDomains.every((d) => selected.has(d.domain));

  // mixedOwners detection used to drive an "ACME email mismatch"
  // warning when one shared email had to cover the whole batch.
  // The shared field is gone now — backend resolves per-domain.

  const handleBulkIssue = async (e: React.FormEvent) => {
    e.preventDefault();
    if (selected.size === 0) {
      toast.error("Select at least one domain");
      return;
    }
    setSubmitting(true);
    try {
      // No `email` in the payload — backend auto-resolves per
      // domain from each domain's owning vendor.
      const res = await api.post("/ssl/letsencrypt/bulk", {
        domains: Array.from(selected),
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
      fetchCerts();
      fetchDomains();
    } catch (err: any) {
      toast.error(
        err?.response?.data?.error?.message ||
          err?.response?.data?.message ||
          "Bulk issue failed"
      );
    } finally {
      setSubmitting(false);
    }
  };

  const handleUpload = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!uploadForm.domain) {
      toast.error("Please select a domain");
      return;
    }
    if (!uploadForm.cert.trim() || !uploadForm.key.trim()) {
      toast.error("Certificate and private key are required");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/ssl/custom", {
        domain: uploadForm.domain,
        cert: uploadForm.cert,
        key: uploadForm.key,
        ca_bundle: uploadForm.ca_bundle || undefined,
      });
      toast.success("Custom certificate uploaded");
      setShowUpload(false);
      setUploadForm({ domain: "", cert: "", key: "", ca_bundle: "" });
      fetchCerts();
      fetchDomains();
    } catch (err: any) {
      toast.error(
        err?.response?.data?.error?.message ||
          err?.response?.data?.message ||
          "Failed to upload certificate"
      );
    } finally {
      setSubmitting(false);
    }
  };

  const handleRenew = async (domain: string) => {
    try {
      await api.post(`/ssl/${encodeURIComponent(domain)}/renew`);
      toast.success("Certificate renewal initiated");
      fetchCerts();
    } catch {
      toast.error("Failed to renew certificate");
    }
  };

  // Reissue forces a fresh Let's Encrypt cert for an existing domain.
  // Distinct from Renew: Renew uses `certbot renew --force-renewal`
  // (works only when the live cert exists on disk); Reissue uses
  // `certbot certonly --force-renewal` (works in both cases) and
  // re-runs the full post-issue pipeline including vhost upgrade and
  // mail-SSL retrigger.
  const handleReissue = async (domain: string) => {
    if (
      !(await confirmAction({
        title: "Reissue certificate?",
        description: `Force a fresh Let's Encrypt certificate for ${domain}? The current cert will be replaced. Reissues count against Let's Encrypt's per-week duplicate-cert limit (5/week).`,
        confirmLabel: "Reissue",
      }))
    )
      return;
    try {
      await api.post(`/ssl/${encodeURIComponent(domain)}/reissue`);
      toast.success(`Certificate reissued for ${domain}`);
      fetchCerts();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to reissue certificate");
    }
  };

  const handleRevoke = async (domain: string) => {
    if (
      !(await confirmAction({
        title: "Revoke certificate?",
        description: `Revoke the SSL certificate for ${domain}? Clients will see TLS errors immediately.`,
        danger: true,
        confirmLabel: "Revoke",
      }))
    )
      return;
    try {
      await api.post(`/ssl/${encodeURIComponent(domain)}/revoke`);
      toast.success("Certificate revoked");
      fetchCerts();
      fetchDomains();
    } catch {
      toast.error("Failed to revoke certificate");
    }
  };

  const handleDelete = async (domain: string) => {
    if (
      !(await confirmAction({
        title: "Remove certificate?",
        description: `Remove the SSL certificate for ${domain}?`,
        danger: true,
        confirmLabel: "Remove",
      }))
    )
      return;
    try {
      await api.delete(`/ssl/${encodeURIComponent(domain)}`);
      toast.success("Certificate removed");
      setCerts((prev) => prev.filter((c) => c.domain !== domain));
      fetchDomains();
    } catch {
      toast.error("Failed to remove certificate");
    }
  };

  const handleForceSSL = async (domain: string, enable: boolean) => {
    setCerts((prev) =>
      prev.map((c) => (c.domain === domain ? { ...c, force_ssl: enable } : c))
    );
    try {
      await api.post(`/ssl/${encodeURIComponent(domain)}/force-ssl`, { enable });
      toast.success(enable ? "Force-SSL enabled" : "Force-SSL disabled");
    } catch {
      setCerts((prev) =>
        prev.map((c) =>
          c.domain === domain ? { ...c, force_ssl: !enable } : c
        )
      );
      toast.error("Failed to update Force-SSL");
    }
  };

  // mergedRows is the canonical data model: ONE row per domain with
  // the matching cert attached when one exists. Domains that haven't
  // had a cert issued yet still appear so the vendor can act on them
  // from this page — the previous version only listed cert-bearing
  // domains, which made the "I have 6 domains but only 4 SSL rows"
  // experience confusing.
  const mergedRows = useMemo<SslRow[]>(() => {
    const certByDomain = new Map<string, SslCertificate>();
    for (const c of certs) certByDomain.set(c.domain, c);
    const rows: SslRow[] = [];
    const seen = new Set<string>();
    for (const d of domains) {
      const cert = certByDomain.get(d.domain) || null;
      rows.push({ domain: d.domain, cert, owner_email: d.owner_email });
      seen.add(d.domain);
    }
    // Surface orphan certs whose Domain doc was deleted but cert
    // record persists — rare, but the operator should still see it.
    for (const c of certs) {
      if (seen.has(c.domain)) continue;
      rows.push({ domain: c.domain, cert: c, owner_email: undefined });
    }
    rows.sort((a, b) => a.domain.localeCompare(b.domain));
    return rows;
  }, [certs, domains]);

  const isActiveRow = (r: SslRow): boolean => {
    if (!r.cert) return false;
    return !certIsExpired(r.cert);
  };

  // 3.1.92 — Column sort state for the cPanel SSL table. Same shape
  // as the WHM SSLPage's sort logic so behaviour stays consistent
  // across surfaces; keyset is a subset because the cPanel table
  // doesn't have Force-SSL or separate Type/Issuer columns.
  type SortKey = "domain" | "issuer" | "status" | "expires";
  type SortDir = "asc" | "desc";
  const [sortKey, setSortKey] = useState<SortKey>(() => {
    const v = localStorage.getItem("cp-ssl-sort-key");
    return (v === "issuer" || v === "status" || v === "expires") ? v : "domain";
  });
  const [sortDir, setSortDir] = useState<SortDir>(() => {
    return localStorage.getItem("cp-ssl-sort-dir") === "desc" ? "desc" : "asc";
  });
  useEffect(() => { localStorage.setItem("cp-ssl-sort-key", sortKey); }, [sortKey]);
  useEffect(() => { localStorage.setItem("cp-ssl-sort-dir", sortDir); }, [sortDir]);
  const toggleSort = (key: SortKey) => {
    if (key === sortKey) setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    else { setSortKey(key); setSortDir("asc"); }
  };

  const sortedRows = useMemo(() => {
    const rows = [...mergedRows];
    rows.sort((a, b) => {
      let cmp = 0;
      switch (sortKey) {
        case "domain":
          cmp = a.domain.localeCompare(b.domain);
          break;
        case "issuer": {
          const av = a.cert?.issuer || a.cert?.type || "~";
          const bv = b.cert?.issuer || b.cert?.type || "~";
          cmp = av.localeCompare(bv);
          break;
        }
        case "expires": {
          // Soonest first on ASC; no-cert rows last.
          const at = a.cert?.expires_at ? new Date(a.cert.expires_at).getTime() : Number.MAX_SAFE_INTEGER;
          const bt = b.cert?.expires_at ? new Date(b.cert.expires_at).getTime() : Number.MAX_SAFE_INTEGER;
          cmp = at - bt;
          break;
        }
        case "status": {
          // expired > warning > active on DESC, so DESC = "urgent first".
          const rank = (r: SslRow): number => {
            if (!r.cert) return 3;
            if (certIsExpired(r.cert)) return 2;
            if (r.cert.days_remaining < 30) return 1;
            return 0;
          };
          cmp = rank(a) - rank(b);
          break;
        }
      }
      if (cmp === 0) cmp = a.domain.localeCompare(b.domain);
      return sortDir === "asc" ? cmp : -cmp;
    });
    return rows;
  }, [mergedRows, sortKey, sortDir]);

  const filteredRows = useMemo(() => {
    const q = search.trim().toLowerCase();
    return sortedRows.filter((r) => {
      if (q && !r.domain.toLowerCase().includes(q)) return false;
      if (statusFilter === "active" && !isActiveRow(r)) return false;
      if (statusFilter === "inactive" && isActiveRow(r)) return false;
      return true;
    });
  }, [sortedRows, search, statusFilter]);

  const sortHeader = (label: string, key: SortKey) => {
    const active = sortKey === key;
    const arrow = !active ? "↕" : sortDir === "asc" ? "↑" : "↓";
    return (
      <button
        type="button"
        onClick={() => toggleSort(key)}
        className={"inline-flex items-center gap-1 transition-colors " + (active ? "text-panel-text" : "text-panel-muted hover:text-panel-text")}
        title={active
          ? `Sorted by ${label.toLowerCase()} ${sortDir === "asc" ? "ascending" : "descending"} — click to flip`
          : `Sort by ${label.toLowerCase()}`}
      >
        {label}
        <span className={"text-[10px] " + (active ? "opacity-100" : "opacity-50")}>{arrow}</span>
      </button>
    );
  };

  const counts = useMemo(() => {
    let active = 0;
    for (const r of mergedRows) if (isActiveRow(r)) active++;
    return { all: mergedRows.length, active, inactive: mergedRows.length - active };
  }, [mergedRows]);

  // Pre-select a single domain in the bulk modal — same flow as bulk
  // Issue but lets the vendor act on a specific row without touching
  // the picker. Per-domain ACME email is auto-resolved server-side.
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
      key: "domain",
      header: sortHeader("Domain", "domain"),
      render: (r: SslRow) => (
        <div className="flex items-center gap-2 min-w-0">
          <ShieldCheck
            size={16}
            className={
              !r.cert
                ? "text-panel-muted/50"
                : r.cert.days_remaining <= 0
                  ? "text-red-400"
                  : r.cert.days_remaining < 30
                    ? "text-yellow-400"
                    : "text-emerald-400"
            }
          />
          <span className="font-mono font-medium text-white truncate">{r.domain}</span>
          {r.cert?.wildcard && (
            <span
              className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium bg-purple-500/10 text-purple-300 border border-purple-500/20"
              title="Wildcard certificate"
            >
              <Sparkles size={10} /> wildcard
            </span>
          )}
        </div>
      ),
    },
    {
      key: "issuer",
      header: sortHeader("Issuer", "issuer"),
      render: (r: SslRow) =>
        r.cert ? (
          <div className="flex flex-col">
            <span className="text-panel-text text-sm">{r.cert.issuer || "—"}</span>
            <span className="text-xs text-panel-muted capitalize">{r.cert.type}</span>
          </div>
        ) : (
          <span className="text-xs text-panel-muted">—</span>
        ),
    },
    {
      key: "status",
      header: sortHeader("Status", "status"),
      render: (r: SslRow) =>
        !r.cert ? (
          <StatusBadge status="inactive" />
        ) : (
          <StatusBadge
            status={certIsExpired(r.cert) ? "expired" : r.cert.status || "active"}
          />
        ),
    },
    {
      key: "expires",
      header: sortHeader("Expires", "expires"),
      render: (r: SslRow) =>
        r.cert ? (
          <div className="flex flex-col">
            <span className="text-sm text-panel-text">{formatDate(r.cert.expires_at)}</span>
            {r.cert.expires_at && (
              <span className={`text-xs ${daysColor(r.cert.days_remaining)}`}>
                {r.cert.days_remaining <= 0
                  ? "Expired"
                  : `${r.cert.days_remaining}d remaining`}
              </span>
            )}
          </div>
        ) : (
          <span className="text-xs text-panel-muted">—</span>
        ),
    },
    {
      key: "auto_renew",
      header: "Auto-renew",
      render: (r: SslRow) =>
        r.cert ? (
          <span
            className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${
              r.cert.auto_renew
                ? "bg-green-500/10 text-green-400"
                : "bg-panel-border/30 text-panel-muted"
            }`}
          >
            <Repeat size={11} />
            {r.cert.auto_renew ? "on" : "off"}
          </span>
        ) : (
          <span className="text-xs text-panel-muted">—</span>
        ),
    },
    {
      key: "force_ssl",
      header: "Force-SSL",
      render: (r: SslRow) =>
        r.cert ? (
          <button
            onClick={() => handleForceSSL(r.cert!.domain, !r.cert!.force_ssl)}
            className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium transition-colors ${
              r.cert.force_ssl
                ? "bg-green-500/10 text-green-400 hover:bg-green-500/20"
                : "bg-panel-border/30 text-panel-muted hover:bg-panel-border/50"
            }`}
            title={
              r.cert.force_ssl
                ? "Click to disable Force-SSL"
                : "Click to enable Force-SSL"
            }
          >
            {r.cert.force_ssl ? <Lock size={12} /> : <LockOpen size={12} />}
            {r.cert.force_ssl ? "Enabled" : "Disabled"}
          </button>
        ) : (
          <span className="text-xs text-panel-muted">—</span>
        ),
    },
    {
      key: "actions",
      header: "",
      render: (r: SslRow) =>
        !r.cert ? (
          // No cert yet — single CTA into the bulk modal pre-selected
          // with this row, so the vendor goes from "this domain has
          // no SSL" to "issued in one click" without scrolling the picker.
          <div className="flex justify-end">
            <button
              onClick={() => issueForOne(r.domain)}
              className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-medium bg-brand-600/10 text-brand-400 hover:bg-brand-600/20 transition-colors"
              title="Issue Let's Encrypt certificate for this domain"
            >
              <Plus size={12} />
              Issue SSL
            </button>
          </div>
        ) : (
          <div className="flex items-center gap-2 justify-end">
            <button
              onClick={() => handleRenew(r.cert!.domain)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-brand-400 transition-colors"
              title="Renew (extend expiry)"
            >
              <RefreshCw size={16} />
            </button>
            <button
              onClick={() => handleReissue(r.cert!.domain)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-amber-400 transition-colors"
              title="Reissue (force a fresh certificate)"
            >
              <RotateCw size={16} />
            </button>
            <button
              onClick={() => handleRevoke(r.cert!.domain)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-orange-400 transition-colors"
              title="Revoke"
            >
              <Ban size={16} />
            </button>
            <button
              onClick={() => handleDelete(r.cert!.domain)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
              title="Remove"
            >
              <Trash2 size={16} />
            </button>
          </div>
        ),
    },
  ];

  return (
    <div className="space-y-6">
      <Card
        title="SSL/TLS Certificates"
        description="Manage SSL certificates for your domains"
        actions={
          <div className="flex items-center gap-2">
            <Button size="sm" variant="secondary" onClick={() => setShowUpload(true)}>
              <Upload size={16} className="mr-1" /> Upload Custom
            </Button>
            <Button size="sm" onClick={openIssue}>
              <Plus size={16} className="mr-1" /> Issue Let's Encrypt
            </Button>
          </div>
        }
      >
        <div className="mb-4 flex flex-col md:flex-row md:items-center gap-3">
          <div className="relative md:max-w-xs flex-1">
            <Search
              size={16}
              className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
            />
            <input
              type="text"
              placeholder="Search certificates..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
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
                    ? "bg-brand-600 text-white"
                    : "text-panel-muted hover:text-panel-text"
                }`}
              >
                {f.label}
              </button>
            ))}
          </div>
        </div>
        <Table
          columns={columns}
          data={filteredRows as any}
          loading={loading}
          emptyMessage={
            search || statusFilter !== "all"
              ? "No certificates match your filters."
              : "No SSL certificates found. Issue your first certificate to secure your domains."
          }
        />
      </Card>

      {/* Bulk Issue modal */}
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
                className="w-full pl-9 pr-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
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
                      ? "bg-brand-600 text-white"
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
                  className="w-4 h-4 rounded border-panel-border bg-panel-bg text-brand-500"
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
                  {domains.length === 0
                    ? "No domains found. Add a domain first to request certificates."
                    : "No domains match your filter."}
                </div>
              ) : (
                filteredPickerDomains.map((d) => {
                  const checked = selected.has(d.domain);
                  const active = domainSslActive(d);
                  return (
                    <label
                      key={d.id}
                      className={`flex items-center gap-3 px-3 py-2 cursor-pointer hover:bg-panel-bg/40 transition-colors ${
                        checked ? "bg-brand-500/5" : ""
                      }`}
                    >
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={() => toggleOne(d.domain)}
                        className="w-4 h-4 rounded border-panel-border bg-panel-bg text-brand-500"
                      />
                      <ShieldCheck
                        size={14}
                        className={active ? "text-green-400" : "text-panel-muted/50"}
                      />
                      <span className="font-mono text-sm text-panel-text flex-1 truncate">
                        {d.domain}
                      </span>
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

          {/* No shared email field — backend resolves the ACME
              registration email per-domain from each domain's
              owning vendor. The picker rows above already show
              the owner email so you can sanity-check. */}
          <div className="rounded-lg border border-brand-500/20 bg-brand-500/5 px-3 py-2 text-xs text-brand-200/80 flex items-start gap-2">
            <AlertTriangle size={12} className="text-brand-300 mt-0.5 shrink-0" />
            <span>
              ACME registration email is set automatically for each
              cert from the domain's owning vendor — no shared
              field to manage.
            </span>
          </div>

          <div className="flex items-center gap-2">
            <input
              id="wildcard"
              type="checkbox"
              checked={wildcard}
              onChange={(e) => setWildcard(e.target.checked)}
              className="w-4 h-4 rounded border-panel-border bg-panel-bg text-brand-500 focus:ring-brand-500/40"
            />
            <label htmlFor="wildcard" className="text-sm text-panel-text">
              Wildcard certificate (*.&lt;domain&gt;) for every selected domain
            </label>
          </div>

          <p className="text-xs text-panel-muted">
            Certificates are issued one at a time on the server — large batches may take a few minutes.
            DNS must point to this server for HTTP-01 validation (or have a DNS provider configured for wildcards).
          </p>

          <div className="flex justify-end gap-3 pt-2">
            <Button variant="secondary" type="button" onClick={() => setShowIssue(false)}>
              Cancel
            </Button>
            <Button type="submit" loading={submitting} disabled={selected.size === 0}>
              {submitting
                ? `Issuing ${selected.size}...`
                : `Issue ${selected.size || ""} Certificate${selected.size === 1 ? "" : "s"}`.trim()}
            </Button>
          </div>
        </form>
      </Modal>

      {/* Custom upload modal */}
      <Modal
        isOpen={showUpload}
        onClose={() => setShowUpload(false)}
        title="Upload Custom SSL Certificate"
      >
        <form onSubmit={handleUpload} className="space-y-4">
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
              value={uploadForm.cert}
              onChange={(e) => setUploadForm({ ...uploadForm, cert: e.target.value })}
              placeholder="-----BEGIN CERTIFICATE-----"
              className={`${inputClass} h-28 font-mono text-xs`}
            />
          </div>
          <div>
            <label className={labelClass}>Private Key (PEM) *</label>
            <textarea
              required
              value={uploadForm.key}
              onChange={(e) => setUploadForm({ ...uploadForm, key: e.target.value })}
              placeholder="-----BEGIN PRIVATE KEY-----"
              className={`${inputClass} h-28 font-mono text-xs`}
            />
          </div>
          <div>
            <label className={labelClass}>CA Bundle (PEM, optional)</label>
            <textarea
              value={uploadForm.ca_bundle}
              onChange={(e) => setUploadForm({ ...uploadForm, ca_bundle: e.target.value })}
              placeholder="-----BEGIN CERTIFICATE-----"
              className={`${inputClass} h-24 font-mono text-xs`}
            />
            <p className="text-xs text-panel-muted mt-1">
              Intermediate / chain certificates provided by your CA.
            </p>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button variant="secondary" type="button" onClick={() => setShowUpload(false)}>
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Upload Certificate
            </Button>
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
              <Button onClick={() => setShowResults(false)}>Done</Button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
