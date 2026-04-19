import React, { useEffect, useState } from "react";
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
}

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500";
const labelClass = "block text-sm font-medium text-panel-text mb-1.5";

function formatDate(dateStr: string | null): string {
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

export default function SslPage() {
  const [certs, setCerts] = useState<SslCertificate[]>([]);
  const [domains, setDomains] = useState<DomainOption[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showIssue, setShowIssue] = useState(false);
  const [showUpload, setShowUpload] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const [issueForm, setIssueForm] = useState({
    domain: "",
    email: "",
    wildcard: false,
  });
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
    try {
      const res = await api.get("/domains", { params: { limit: 500 } });
      setDomains(res.data.data || []);
    } catch {
      // keep empty; user can retry
    }
  };

  useEffect(() => {
    fetchCerts();
    fetchDomains();
  }, []);

  const handleIssue = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!issueForm.domain) {
      toast.error("Please select a domain");
      return;
    }
    if (!issueForm.email.trim()) {
      toast.error("Please enter an email address");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/ssl/letsencrypt", {
        domain: issueForm.domain,
        email: issueForm.email,
        wildcard: issueForm.wildcard,
      });
      toast.success("SSL certificate requested");
      setShowIssue(false);
      setIssueForm({ domain: "", email: "", wildcard: false });
      fetchCerts();
    } catch (err: any) {
      toast.error(
        err?.response?.data?.error?.message ||
          err?.response?.data?.message ||
          "Failed to request SSL certificate"
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
    } catch {
      toast.error("Failed to remove certificate");
    }
  };

  const handleForceSSL = async (domain: string, enable: boolean) => {
    // optimistic update
    setCerts((prev) =>
      prev.map((c) => (c.domain === domain ? { ...c, force_ssl: enable } : c))
    );
    try {
      await api.post(`/ssl/${encodeURIComponent(domain)}/force-ssl`, { enable });
      toast.success(enable ? "Force-SSL enabled" : "Force-SSL disabled");
    } catch {
      // revert on failure
      setCerts((prev) =>
        prev.map((c) =>
          c.domain === domain ? { ...c, force_ssl: !enable } : c
        )
      );
      toast.error("Failed to update Force-SSL");
    }
  };

  const filtered = certs.filter((c) =>
    c.domain.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      key: "domain",
      header: "Domain",
      render: (item: SslCertificate) => (
        <div className="flex items-center gap-2 min-w-0">
          <ShieldCheck
            size={16}
            className={
              item.days_remaining <= 0
                ? "text-red-400"
                : item.days_remaining < 30
                  ? "text-yellow-400"
                  : "text-emerald-400"
            }
          />
          <span className="font-mono font-medium text-white truncate">
            {item.domain}
          </span>
          {item.wildcard && (
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
      header: "Issuer",
      render: (item: SslCertificate) => (
        <div className="flex flex-col">
          <span className="text-panel-text text-sm">{item.issuer || "—"}</span>
          <span className="text-xs text-panel-muted capitalize">
            {item.type}
          </span>
        </div>
      ),
    },
    {
      key: "status",
      header: "Status",
      render: (item: SslCertificate) => <StatusBadge status={item.status} />,
    },
    {
      key: "expires",
      header: "Expires",
      render: (item: SslCertificate) => (
        <div className="flex flex-col">
          <span className="text-sm text-panel-text">
            {formatDate(item.expires_at)}
          </span>
          {item.expires_at && (
            <span className={`text-xs ${daysColor(item.days_remaining)}`}>
              {item.days_remaining <= 0
                ? "Expired"
                : `${item.days_remaining}d remaining`}
            </span>
          )}
        </div>
      ),
    },
    {
      key: "auto_renew",
      header: "Auto-renew",
      render: (item: SslCertificate) => (
        <span
          className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${
            item.auto_renew
              ? "bg-green-500/10 text-green-400"
              : "bg-panel-border/30 text-panel-muted"
          }`}
        >
          <Repeat size={11} />
          {item.auto_renew ? "on" : "off"}
        </span>
      ),
    },
    {
      key: "force_ssl",
      header: "Force-SSL",
      render: (item: SslCertificate) => (
        <button
          onClick={() => handleForceSSL(item.domain, !item.force_ssl)}
          className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium transition-colors ${
            item.force_ssl
              ? "bg-green-500/10 text-green-400 hover:bg-green-500/20"
              : "bg-panel-border/30 text-panel-muted hover:bg-panel-border/50"
          }`}
          title={
            item.force_ssl
              ? "Click to disable Force-SSL"
              : "Click to enable Force-SSL"
          }
        >
          {item.force_ssl ? <Lock size={12} /> : <LockOpen size={12} />}
          {item.force_ssl ? "Enabled" : "Disabled"}
        </button>
      ),
    },
    {
      key: "actions",
      header: "",
      render: (item: SslCertificate) => (
        <div className="flex items-center gap-2 justify-end">
          <button
            onClick={() => handleRenew(item.domain)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-brand-400 transition-colors"
            title="Renew"
          >
            <RefreshCw size={16} />
          </button>
          <button
            onClick={() => handleRevoke(item.domain)}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-orange-400 transition-colors"
            title="Revoke"
          >
            <Ban size={16} />
          </button>
          <button
            onClick={() => handleDelete(item.domain)}
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
            <Button
              size="sm"
              variant="secondary"
              onClick={() => setShowUpload(true)}
            >
              <Upload size={16} className="mr-1" /> Upload Custom
            </Button>
            <Button size="sm" onClick={() => setShowIssue(true)}>
              <Plus size={16} className="mr-1" /> Issue Let's Encrypt
            </Button>
          </div>
        }
      >
        <div className="mb-4">
          <div className="relative max-w-xs">
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
        </div>
        <Table
          columns={columns}
          data={filtered as any}
          loading={loading}
          emptyMessage="No SSL certificates found. Issue your first certificate to secure your domains."
        />
      </Card>

      {/* Issue Let's Encrypt modal */}
      <Modal
        isOpen={showIssue}
        onClose={() => setShowIssue(false)}
        title="Issue Let's Encrypt Certificate"
      >
        <form onSubmit={handleIssue} className="space-y-4">
          <div>
            <label className={labelClass}>Domain *</label>
            <select
              required
              value={issueForm.domain}
              onChange={(e) =>
                setIssueForm({ ...issueForm, domain: e.target.value })
              }
              className={inputClass}
            >
              <option value="">Select a domain</option>
              {domains.map((d) => (
                <option key={d.id} value={d.domain}>
                  {d.domain}
                </option>
              ))}
            </select>
            {domains.length === 0 && (
              <p className="text-xs text-yellow-400 mt-1">
                No domains found. Add a domain first to request certificates.
              </p>
            )}
          </div>
          <div>
            <label className={labelClass}>Email *</label>
            <input
              type="email"
              required
              value={issueForm.email}
              onChange={(e) =>
                setIssueForm({ ...issueForm, email: e.target.value })
              }
              placeholder="admin@example.com"
              className={inputClass}
            />
            <p className="text-xs text-panel-muted mt-1">
              Used for Let's Encrypt account registration and expiry notices
            </p>
          </div>
          <div className="flex items-center gap-2">
            <input
              id="wildcard"
              type="checkbox"
              checked={issueForm.wildcard}
              onChange={(e) =>
                setIssueForm({ ...issueForm, wildcard: e.target.checked })
              }
              className="w-4 h-4 rounded border-panel-border bg-panel-bg text-brand-500 focus:ring-brand-500/40"
            />
            <label htmlFor="wildcard" className="text-sm text-panel-text">
              Wildcard certificate (*.{issueForm.domain || "example.com"})
            </label>
          </div>
          <p className="text-xs text-panel-muted">
            Let's Encrypt certificates are free and auto-renew every 90 days.
            DNS must point to this server for HTTP-01 validation (or have DNS
            provider configured for wildcards).
          </p>
          <div className="flex justify-end gap-3 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowIssue(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Request Certificate
            </Button>
          </div>
        </form>
      </Modal>

      {/* Upload Custom Certificate modal */}
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
              onChange={(e) =>
                setUploadForm({ ...uploadForm, domain: e.target.value })
              }
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
              onChange={(e) =>
                setUploadForm({ ...uploadForm, cert: e.target.value })
              }
              placeholder="-----BEGIN CERTIFICATE-----"
              className={`${inputClass} h-28 font-mono text-xs`}
            />
          </div>
          <div>
            <label className={labelClass}>Private Key (PEM) *</label>
            <textarea
              required
              value={uploadForm.key}
              onChange={(e) =>
                setUploadForm({ ...uploadForm, key: e.target.value })
              }
              placeholder="-----BEGIN PRIVATE KEY-----"
              className={`${inputClass} h-28 font-mono text-xs`}
            />
          </div>
          <div>
            <label className={labelClass}>CA Bundle (PEM, optional)</label>
            <textarea
              value={uploadForm.ca_bundle}
              onChange={(e) =>
                setUploadForm({ ...uploadForm, ca_bundle: e.target.value })
              }
              placeholder="-----BEGIN CERTIFICATE-----"
              className={`${inputClass} h-24 font-mono text-xs`}
            />
            <p className="text-xs text-panel-muted mt-1">
              Intermediate / chain certificates provided by your CA.
            </p>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowUpload(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Upload Certificate
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
