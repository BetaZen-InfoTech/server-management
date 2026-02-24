import React, { useEffect, useState } from "react";
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
  Shield,
} from "lucide-react";

interface WordPressSite {
  id: string;
  name: string;
  domain: string;
  path: string;
  version: string;
  status: string;
  phpVersion: string;
  autoUpdate: boolean;
  createdAt: string;
}

export default function WordPressPage() {
  const [sites, setSites] = useState<WordPressSite[]>([]);
  const [loading, setLoading] = useState(true);
  const [showInstall, setShowInstall] = useState(false);
  const [search, setSearch] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [form, setForm] = useState({
    domain: "",
    path: "/",
    siteTitle: "",
    adminUser: "admin",
    adminEmail: "",
    adminPassword: "",
  });

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

  useEffect(() => {
    fetchSites();
  }, []);

  const handleInstall = async (e: React.FormEvent) => {
    e.preventDefault();
    if (
      !form.domain.trim() ||
      !form.siteTitle.trim() ||
      !form.adminEmail.trim() ||
      !form.adminPassword.trim()
    ) {
      toast.error("Please fill in all required fields");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/wordpress", form);
      toast.success("WordPress installation started");
      setShowInstall(false);
      setForm({
        domain: "",
        path: "/",
        siteTitle: "",
        adminUser: "admin",
        adminEmail: "",
        adminPassword: "",
      });
      fetchSites();
    } catch (err: any) {
      toast.error(
        err.response?.data?.message || "Failed to install WordPress"
      );
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

  const handleDelete = async (id: string, name: string) => {
    if (
      !confirm(
        `Delete WordPress site "${name}"? This will remove all files and database.`
      )
    )
      return;
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
      s.name.toLowerCase().includes(search.toLowerCase()) ||
      s.domain.toLowerCase().includes(search.toLowerCase())
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
                      <h3 className="font-medium text-white">{site.name}</h3>
                      <p className="text-xs text-panel-muted">{site.domain}{site.path}</p>
                    </div>
                  </div>
                  <StatusBadge status={site.status} />
                </div>
                <div className="text-sm text-panel-muted space-y-1 mb-3">
                  <p>
                    WordPress:{" "}
                    <span className="text-panel-text">v{site.version}</span>
                  </p>
                  <p>
                    PHP:{" "}
                    <span className="text-panel-text">{site.phpVersion}</span>
                  </p>
                  <p>
                    Auto-update:{" "}
                    <span
                      className={
                        site.autoUpdate ? "text-green-400" : "text-yellow-400"
                      }
                    >
                      {site.autoUpdate ? "Enabled" : "Disabled"}
                    </span>
                  </p>
                </div>
                <div className="flex items-center gap-2 pt-3 border-t border-panel-border">
                  <a
                    href={`https://${site.domain}${site.path}`}
                    target="_blank"
                    rel="noreferrer"
                    className="text-panel-muted hover:text-brand-400 transition-colors"
                    title="Visit Site"
                  >
                    <ExternalLink size={16} />
                  </a>
                  <a
                    href={`https://${site.domain}${site.path}wp-admin`}
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
                    onClick={() => handleDelete(site.id, site.name)}
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
          <div className="grid grid-cols-2 gap-4">
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
                Install Path
              </label>
              <input
                type="text"
                value={form.path}
                onChange={(e) => setForm({ ...form, path: e.target.value })}
                placeholder="/"
                className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
              />
            </div>
          </div>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Site Title
            </label>
            <input
              type="text"
              value={form.siteTitle}
              onChange={(e) => setForm({ ...form, siteTitle: e.target.value })}
              placeholder="My WordPress Site"
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Admin Username
              </label>
              <input
                type="text"
                value={form.adminUser}
                onChange={(e) =>
                  setForm({ ...form, adminUser: e.target.value })
                }
                placeholder="admin"
                className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Admin Email
              </label>
              <input
                type="email"
                value={form.adminEmail}
                onChange={(e) =>
                  setForm({ ...form, adminEmail: e.target.value })
                }
                placeholder="admin@example.com"
                className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
              />
            </div>
          </div>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Admin Password
            </label>
            <input
              type="password"
              value={form.adminPassword}
              onChange={(e) =>
                setForm({ ...form, adminPassword: e.target.value })
              }
              placeholder="Strong password"
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowInstall(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Install WordPress
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
