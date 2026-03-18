import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { ShieldCheck, Plus, RefreshCw, Search, Trash2, Download, Eye } from "lucide-react";

interface SslCertificate {
  id: string;
  domain: string;
  type: "letsencrypt" | "custom";
  expiresAt: string;
  daysUntilExpiry: number;
  status: "active" | "expired" | "pending";
  issuedAt: string;
}

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

export default function SslPage() {
  const [certificates, setCertificates] = useState<SslCertificate[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ domain: "", type: "letsencrypt" as "letsencrypt" | "custom" });

  useEffect(() => {
    fetchCertificates();
  }, []);

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

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.domain) {
      toast.error("Please enter a domain name");
      return;
    }
    setCreating(true);
    try {
      const endpoint = form.type === "letsencrypt" ? "/ssl/letsencrypt" : "/ssl/custom";
      await api.post(endpoint, form);
      toast.success(`SSL certificate for ${form.domain} issued`);
      setShowCreate(false);
      setForm({ domain: "", type: "letsencrypt" });
      fetchCertificates();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to issue certificate");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (domain: string) => {
    if (!confirm(`Are you sure you want to delete SSL certificate for ${domain}?`)) return;
    try {
      await api.delete(`/ssl/${domain}`);
      toast.success(`SSL certificate for ${domain} deleted`);
      fetchCertificates();
    } catch {
      toast.error("Failed to delete certificate");
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

  const filtered = certificates.filter((c) =>
    c.domain.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Domain",
      accessor: (c: SslCertificate) => (
        <div className="flex items-center gap-2">
          <ShieldCheck size={14} className="text-green-400" />
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
          <span className="text-panel-muted text-sm">{c.expiresAt}</span>
          {c.daysUntilExpiry <= 30 && c.daysUntilExpiry > 0 && (
            <span className="ml-2 text-xs text-yellow-400">
              ({c.daysUntilExpiry}d left)
            </span>
          )}
        </div>
      ),
    },
    {
      header: "Status",
      accessor: (c: SslCertificate) => <StatusBadge status={c.status} />,
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
            onClick={() => setShowCreate(true)}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Issue Certificate
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search certificates..."
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
            <ShieldCheck size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No SSL certificates found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No certificates match your search. Try a different search term."
                : "Issue your first SSL certificate to secure your domains with HTTPS."}
            </p>
            {!search && (
              <Button
                onClick={() => setShowCreate(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Issue Certificate
              </Button>
            )}
          </div>
        )}
      </Card>

      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Issue SSL Certificate">
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className={labelClass}>Domain *</label>
            <input type="text" required placeholder="example.com" value={form.domain}
              onChange={(e) => setForm({ ...form, domain: e.target.value })} className={inputClass} />
            <p className="text-xs text-panel-muted mt-1">
              Wildcard domains are supported (e.g., *.example.com)
            </p>
          </div>
          <div>
            <label className={labelClass}>Certificate Type</label>
            <div className="flex gap-2">
              {([
                { value: "letsencrypt" as const, label: "Let's Encrypt (Free)", desc: "Auto-renewing, 90-day certificate" },
                { value: "custom" as const, label: "Custom Certificate", desc: "Upload your own SSL certificate" },
              ]).map((t) => (
                <button key={t.value} type="button" onClick={() => setForm({ ...form, type: t.value })}
                  className={`flex-1 p-3 rounded-lg text-left transition-colors ${
                    form.type === t.value
                      ? "bg-blue-600/10 border-2 border-blue-500"
                      : "bg-panel-bg border border-panel-border hover:border-panel-muted"
                  }`}>
                  <p className={`text-sm font-medium ${form.type === t.value ? "text-blue-400" : "text-panel-text"}`}>{t.label}</p>
                  <p className="text-xs text-panel-muted mt-0.5">{t.desc}</p>
                </button>
              ))}
            </div>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Issuing..." : "Issue Certificate"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
