import React, { useEffect, useState } from "react";
import { Card, Button, Table, Modal, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  ShieldCheck,
  Plus,
  Trash2,
  Search,
  RefreshCw,
  AlertTriangle,
  CheckCircle2,
} from "lucide-react";

interface SslCertificate {
  id: string;
  domain: string;
  issuer: string;
  type: string;
  status: string;
  expiresAt: string;
  autoRenew: boolean;
  createdAt: string;
}

export default function SslPage() {
  const [certs, setCerts] = useState<SslCertificate[]>([]);
  const [loading, setLoading] = useState(true);
  const [showRequest, setShowRequest] = useState(false);
  const [search, setSearch] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [form, setForm] = useState({
    domain: "",
    type: "letsencrypt",
  });

  const fetchCerts = async () => {
    try {
      const res = await api.get("/ssl/certificates");
      setCerts(res.data);
    } catch {
      toast.error("Failed to load SSL certificates");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchCerts();
  }, []);

  const handleRequest = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.domain.trim()) {
      toast.error("Please enter a domain");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/ssl/certificates", form);
      toast.success("SSL certificate requested");
      setShowRequest(false);
      setForm({ domain: "", type: "letsencrypt" });
      fetchCerts();
    } catch (err: any) {
      toast.error(
        err.response?.data?.message || "Failed to request SSL certificate"
      );
    } finally {
      setSubmitting(false);
    }
  };

  const handleRenew = async (id: string) => {
    try {
      await api.post(`/ssl/certificates/${id}/renew`);
      toast.success("Certificate renewal initiated");
      fetchCerts();
    } catch {
      toast.error("Failed to renew certificate");
    }
  };

  const handleDelete = async (id: string, domain: string) => {
    if (!confirm(`Remove SSL certificate for ${domain}?`)) return;
    try {
      await api.delete(`/ssl/certificates/${id}`);
      toast.success("Certificate removed");
      setCerts((prev) => prev.filter((c) => c.id !== id));
    } catch {
      toast.error("Failed to remove certificate");
    }
  };

  const filtered = certs.filter((c) =>
    c.domain.toLowerCase().includes(search.toLowerCase())
  );

  const isExpiringSoon = (expiresAt: string) => {
    const days = Math.floor(
      (new Date(expiresAt).getTime() - Date.now()) / (1000 * 60 * 60 * 24)
    );
    return days <= 30;
  };

  const columns = [
    {
      key: "domain",
      header: "Domain",
      render: (item: SslCertificate) => (
        <div className="flex items-center gap-2">
          <ShieldCheck size={16} className="text-emerald-400" />
          <span className="font-medium text-white">{item.domain}</span>
        </div>
      ),
    },
    {
      key: "issuer",
      header: "Issuer",
      render: (item: SslCertificate) => (
        <span className="text-panel-muted">{item.issuer}</span>
      ),
    },
    {
      key: "type",
      header: "Type",
      render: (item: SslCertificate) => (
        <span className="text-panel-muted capitalize">{item.type}</span>
      ),
    },
    {
      key: "status",
      header: "Status",
      render: (item: SslCertificate) => <StatusBadge status={item.status} />,
    },
    {
      key: "expiresAt",
      header: "Expires",
      render: (item: SslCertificate) => (
        <div className="flex items-center gap-1.5">
          {isExpiringSoon(item.expiresAt) ? (
            <AlertTriangle size={14} className="text-yellow-400" />
          ) : (
            <CheckCircle2 size={14} className="text-green-400" />
          )}
          <span
            className={`text-sm ${
              isExpiringSoon(item.expiresAt)
                ? "text-yellow-400"
                : "text-panel-muted"
            }`}
          >
            {item.expiresAt}
          </span>
        </div>
      ),
    },
    {
      key: "actions",
      header: "",
      render: (item: SslCertificate) => (
        <div className="flex items-center gap-2 justify-end">
          <button
            onClick={() => handleRenew(item.id)}
            className="text-panel-muted hover:text-brand-400 transition-colors"
            title="Renew"
          >
            <RefreshCw size={16} />
          </button>
          <button
            onClick={() => handleDelete(item.id, item.domain)}
            className="text-panel-muted hover:text-red-400 transition-colors"
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
          <Button size="sm" onClick={() => setShowRequest(true)}>
            <Plus size={16} className="mr-1" /> Request SSL
          </Button>
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
          emptyMessage="No SSL certificates found. Request your first certificate."
        />
      </Card>

      <Modal
        isOpen={showRequest}
        onClose={() => setShowRequest(false)}
        title="Request SSL Certificate"
      >
        <form onSubmit={handleRequest} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Domain
            </label>
            <input
              type="text"
              value={form.domain}
              onChange={(e) => setForm({ ...form, domain: e.target.value })}
              placeholder="example.com"
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Certificate Type
            </label>
            <select
              value={form.type}
              onChange={(e) => setForm({ ...form, type: e.target.value })}
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500"
            >
              <option value="letsencrypt">Let's Encrypt (Free)</option>
              <option value="custom">Custom Certificate</option>
            </select>
          </div>
          <p className="text-xs text-panel-muted">
            Let's Encrypt certificates are free and auto-renew every 90 days.
            DNS must be pointing to this server for validation.
          </p>
          <div className="flex justify-end gap-3 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowRequest(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Request Certificate
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
