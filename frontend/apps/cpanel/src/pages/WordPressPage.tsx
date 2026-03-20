import React, { useEffect, useState, useCallback } from "react";
import { Card, Button, Modal, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  FileCode2,
  Plus,
  ExternalLink,
  Trash2,
  RefreshCw,
  Search,
  Settings,
  AlertTriangle,
} from "lucide-react";

interface WordPressSite {
  id: string;
  domain: string;
  path: string;
  version: string;
  site_url: string;
  admin_url: string;
  auto_update: boolean;
  maintenance_mode: boolean;
  created_at: string;
}

interface DomainItem {
  id: string;
  domain: string;
  status: string;
}

const inputClass = "w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500";
const selectClass = "w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500";
const labelClass = "block text-sm font-medium text-panel-text mb-1.5";

const defaultForm = { site_title: "", domain: "", path: "", admin_user: "admin", admin_email: "", admin_pass: "" };

export default function WordPressPage() {
  const [sites, setSites] = useState<WordPressSite[]>([]);
  const [domains, setDomains] = useState<DomainItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [showInstall, setShowInstall] = useState(false);
  const [search, setSearch] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [form, setForm] = useState(defaultForm);
  const [conflict, setConflict] = useState<string | null>(null);

  const fetchSites = async () => {
    try {
      const res = await api.get("/wordpress");
      setSites(res.data.data || []);
    } catch {
      toast.error("Failed to load WordPress sites");
    } finally {
      setLoading(false);
    }
  };

  const fetchDomains = async () => {
    try {
      const res = await api.get("/domains");
      setDomains((res.data.data || []).filter((d: DomainItem) => d.status === "active"));
    } catch {
      // Keep empty
    }
  };

  useEffect(() => {
    fetchSites();
    fetchDomains();
  }, []);

  const checkConflict = useCallback(async (domain: string, path: string) => {
    if (!domain) { setConflict(null); return; }
    try {
      const res = await api.get("/wordpress/check-conflict", { params: { domain, path } });
      const data = res.data.data;
      setConflict(data?.conflict ? data.message : null);
    } catch {
      setConflict(null);
    }
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => {
      if (form.domain) checkConflict(form.domain, form.path);
      else setConflict(null);
    }, 300);
    return () => clearTimeout(timer);
  }, [form.domain, form.path, checkConflict]);

  const handleInstall = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.domain || !form.site_title || !form.admin_email || !form.admin_pass || !form.admin_user) {
      toast.error("Please fill in all required fields");
      return;
    }
    if (conflict) {
      toast.error("Cannot install — a WordPress site already exists at this location");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/wordpress/install", form);
      toast.success("WordPress installation started");
      setShowInstall(false);
      setForm(defaultForm);
      fetchSites();
    } catch (err: any) {
      toast.error(err.response?.data?.error?.message || "Failed to install WordPress");
    } finally {
      setSubmitting(false);
    }
  };

  const handleUpdate = async (id: string) => {
    try {
      await api.post(`/wordpress/${id}/update`);
      toast.success("WordPress update initiated");
      fetchSites();
    } catch {
      toast.error("Failed to update WordPress");
    }
  };

  const handleDelete = async (id: string, domain: string) => {
    if (!confirm(`Delete WordPress site on "${domain}"? This will remove all files and database.`)) return;
    try {
      await api.delete(`/wordpress/${id}`);
      toast.success("WordPress site removed");
      setSites((prev) => prev.filter((s) => s.id !== id));
    } catch {
      toast.error("Failed to delete WordPress site");
    }
  };

  const filtered = sites.filter(
    (s) =>
      s.domain.toLowerCase().includes(search.toLowerCase()) ||
      (s.site_url || "").toLowerCase().includes(search.toLowerCase())
  );

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-brand-400" />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <Card
        title="WordPress Sites"
        description="Install and manage WordPress installations"
        actions={
          <Button size="sm" onClick={() => setShowInstall(true)}>
            <Plus size={16} className="mr-1" /> Install WordPress
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
              placeholder="Search sites..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
        </div>

        {filtered.length === 0 ? (
          <div className="text-center py-12">
            <FileCode2 size={40} className="mx-auto text-panel-muted mb-3" />
            <p className="text-panel-muted">No WordPress sites installed</p>
            <p className="text-sm text-panel-muted mt-1">
              Install WordPress with one click
            </p>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {filtered.map((site) => (
              <div
                key={site.id}
                className="bg-panel-bg border border-panel-border rounded-lg p-4"
              >
                <div className="flex items-start justify-between mb-3">
                  <div className="flex items-center gap-2">
                    <div className="w-8 h-8 bg-blue-500/10 rounded-lg flex items-center justify-center">
                      <FileCode2 size={16} className="text-blue-400" />
                    </div>
                    <div>
                      <h3 className="font-medium text-white">{site.domain}</h3>
                      <p className="text-xs text-panel-muted">{site.domain}{site.path || "/"}</p>
                    </div>
                  </div>
                  <StatusBadge status={site.maintenance_mode ? "warning" : "active"} />
                </div>
                <div className="text-sm text-panel-muted space-y-1 mb-3">
                  <p>
                    WordPress:{" "}
                    <span className="text-panel-text">v{site.version || "?"}</span>
                  </p>
                  <p>
                    Auto-update:{" "}
                    <span className={site.auto_update ? "text-green-400" : "text-yellow-400"}>
                      {site.auto_update ? "Enabled" : "Disabled"}
                    </span>
                  </p>
                </div>
                <div className="flex items-center gap-2 pt-3 border-t border-panel-border">
                  <a
                    href={site.site_url || `https://${site.domain}${site.path || "/"}`}
                    target="_blank"
                    rel="noreferrer"
                    className="text-panel-muted hover:text-brand-400 transition-colors"
                    title="Visit Site"
                  >
                    <ExternalLink size={16} />
                  </a>
                  <a
                    href={site.admin_url || `https://${site.domain}${site.path || ""}/wp-admin`}
                    target="_blank"
                    rel="noreferrer"
                    className="text-panel-muted hover:text-brand-400 transition-colors"
                    title="WP Admin"
                  >
                    <Settings size={16} />
                  </a>
                  <button
                    onClick={() => handleUpdate(site.id)}
                    className="text-panel-muted hover:text-blue-400 transition-colors"
                    title="Update WordPress"
                  >
                    <RefreshCw size={16} />
                  </button>
                  <button
                    onClick={() => handleDelete(site.id, site.domain)}
                    className="text-panel-muted hover:text-red-400 transition-colors ml-auto"
                    title="Delete"
                  >
                    <Trash2 size={16} />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      <Modal
        isOpen={showInstall}
        onClose={() => setShowInstall(false)}
        title="Install WordPress"
        size="lg"
      >
        <form onSubmit={handleInstall} className="space-y-4">
          <div>
            <label className={labelClass}>Site Title *</label>
            <input type="text" required placeholder="My WordPress Site" value={form.site_title}
              onChange={(e) => setForm({ ...form, site_title: e.target.value })} className={inputClass} />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Domain *</label>
              <select required value={form.domain}
                onChange={(e) => setForm({ ...form, domain: e.target.value })} className={selectClass}>
                <option value="">Select a domain</option>
                {domains.map((d) => (
                  <option key={d.id} value={d.domain}>{d.domain}</option>
                ))}
              </select>
            </div>
            <div>
              <label className={labelClass}>Install Path</label>
              <input type="text" placeholder="(empty for root)" value={form.path}
                onChange={(e) => setForm({ ...form, path: e.target.value })} className={inputClass} />
              <p className="text-xs text-panel-muted mt-1">e.g. "blog" or leave empty</p>
            </div>
          </div>
          {conflict && (
            <div className="flex items-start gap-2 p-3 bg-amber-500/10 border border-amber-500/30 rounded-lg">
              <AlertTriangle size={16} className="text-amber-400 shrink-0 mt-0.5" />
              <p className="text-sm text-amber-300">{conflict}</p>
            </div>
          )}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Admin Username *</label>
              <input type="text" required placeholder="admin" value={form.admin_user}
                onChange={(e) => setForm({ ...form, admin_user: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Admin Email *</label>
              <input type="email" required placeholder="admin@example.com" value={form.admin_email}
                onChange={(e) => setForm({ ...form, admin_email: e.target.value })} className={inputClass} />
            </div>
          </div>
          <div>
            <label className={labelClass}>Admin Password *</label>
            <input type="password" required minLength={8} placeholder="Min. 8 characters" value={form.admin_pass}
              onChange={(e) => setForm({ ...form, admin_pass: e.target.value })} className={inputClass} />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button variant="secondary" type="button" onClick={() => setShowInstall(false)}>
              Cancel
            </Button>
            <Button type="submit" loading={submitting} disabled={!!conflict}>
              Install WordPress
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
