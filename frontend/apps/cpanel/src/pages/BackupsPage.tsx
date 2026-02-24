import React, { useEffect, useState } from "react";
import { Card, Button, Table, Modal, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Archive,
  Plus,
  Download,
  RotateCcw,
  Trash2,
  Search,
  HardDrive,
  Clock,
} from "lucide-react";

interface Backup {
  id: string;
  name: string;
  type: string;
  size: string;
  status: string;
  createdAt: string;
  includes: string[];
}

export default function BackupsPage() {
  const [backups, setBackups] = useState<Backup[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);
  const [search, setSearch] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [form, setForm] = useState({
    type: "full",
    includeFiles: true,
    includeDatabases: true,
    includeEmails: true,
  });

  const fetchBackups = async () => {
    try {
      const res = await api.get("/backups");
      setBackups(res.data);
    } catch {
      toast.error("Failed to load backups");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchBackups();
  }, []);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    try {
      await api.post("/backups", form);
      toast.success("Backup creation initiated");
      setShowCreate(false);
      fetchBackups();
    } catch (err: any) {
      toast.error(err.response?.data?.message || "Failed to create backup");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDownload = async (id: string) => {
    try {
      const res = await api.get(`/backups/${id}/download`);
      if (res.data.url) {
        window.open(res.data.url, "_blank");
      }
      toast.success("Download started");
    } catch {
      toast.error("Failed to download backup");
    }
  };

  const handleRestore = async (id: string) => {
    if (
      !confirm(
        "Are you sure you want to restore this backup? This will overwrite current data."
      )
    )
      return;
    try {
      await api.post(`/backups/${id}/restore`);
      toast.success("Restore initiated. This may take a few minutes.");
    } catch {
      toast.error("Failed to restore backup");
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this backup permanently?")) return;
    try {
      await api.delete(`/backups/${id}`);
      toast.success("Backup deleted");
      setBackups((prev) => prev.filter((b) => b.id !== id));
    } catch {
      toast.error("Failed to delete backup");
    }
  };

  const filtered = backups.filter((b) =>
    b.name.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      key: "name",
      header: "Backup",
      render: (item: Backup) => (
        <div className="flex items-center gap-2">
          <Archive size={16} className="text-orange-400" />
          <div>
            <p className="font-medium text-white">{item.name}</p>
            <p className="text-xs text-panel-muted capitalize">{item.type} backup</p>
          </div>
        </div>
      ),
    },
    {
      key: "includes",
      header: "Includes",
      render: (item: Backup) => (
        <div className="flex items-center gap-1.5">
          {(item.includes || []).map((inc) => (
            <span
              key={inc}
              className="inline-flex items-center px-2 py-0.5 rounded bg-panel-bg text-xs text-panel-muted"
            >
              {inc}
            </span>
          ))}
        </div>
      ),
    },
    {
      key: "size",
      header: "Size",
      render: (item: Backup) => (
        <div className="flex items-center gap-1.5">
          <HardDrive size={14} className="text-panel-muted" />
          <span className="text-panel-muted">{item.size}</span>
        </div>
      ),
    },
    {
      key: "status",
      header: "Status",
      render: (item: Backup) => <StatusBadge status={item.status} />,
    },
    {
      key: "createdAt",
      header: "Created",
      render: (item: Backup) => (
        <div className="flex items-center gap-1.5">
          <Clock size={14} className="text-panel-muted" />
          <span className="text-panel-muted">{item.createdAt}</span>
        </div>
      ),
    },
    {
      key: "actions",
      header: "",
      render: (item: Backup) => (
        <div className="flex items-center gap-2 justify-end">
          {item.status === "completed" && (
            <>
              <button
                onClick={() => handleDownload(item.id)}
                className="text-panel-muted hover:text-brand-400 transition-colors"
                title="Download"
              >
                <Download size={16} />
              </button>
              <button
                onClick={() => handleRestore(item.id)}
                className="text-panel-muted hover:text-yellow-400 transition-colors"
                title="Restore"
              >
                <RotateCcw size={16} />
              </button>
            </>
          )}
          <button
            onClick={() => handleDelete(item.id)}
            className="text-panel-muted hover:text-red-400 transition-colors"
            title="Delete"
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
        title="Backups"
        description="Create and manage backups of your account data"
        actions={
          <Button size="sm" onClick={() => setShowCreate(true)}>
            <Plus size={16} className="mr-1" /> Create Backup
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
              placeholder="Search backups..."
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
          emptyMessage="No backups found. Create your first backup to protect your data."
        />
      </Card>

      <Modal
        isOpen={showCreate}
        onClose={() => setShowCreate(false)}
        title="Create Backup"
      >
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Backup Type
            </label>
            <select
              value={form.type}
              onChange={(e) => setForm({ ...form, type: e.target.value })}
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500"
            >
              <option value="full">Full Backup</option>
              <option value="partial">Partial Backup</option>
            </select>
          </div>
          <div className="space-y-3">
            <p className="text-sm font-medium text-panel-text">Include:</p>
            <label className="flex items-center gap-2 text-sm text-panel-text">
              <input
                type="checkbox"
                checked={form.includeFiles}
                onChange={(e) =>
                  setForm({ ...form, includeFiles: e.target.checked })
                }
                className="rounded border-panel-border bg-panel-bg text-brand-600 focus:ring-brand-500"
              />
              Files & directories
            </label>
            <label className="flex items-center gap-2 text-sm text-panel-text">
              <input
                type="checkbox"
                checked={form.includeDatabases}
                onChange={(e) =>
                  setForm({ ...form, includeDatabases: e.target.checked })
                }
                className="rounded border-panel-border bg-panel-bg text-brand-600 focus:ring-brand-500"
              />
              Databases
            </label>
            <label className="flex items-center gap-2 text-sm text-panel-text">
              <input
                type="checkbox"
                checked={form.includeEmails}
                onChange={(e) =>
                  setForm({ ...form, includeEmails: e.target.checked })
                }
                className="rounded border-panel-border bg-panel-bg text-brand-600 focus:ring-brand-500"
              />
              Email accounts
            </label>
          </div>
          <p className="text-xs text-panel-muted">
            Backup creation may take several minutes depending on your account
            size.
          </p>
          <div className="flex justify-end gap-3 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowCreate(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Create Backup
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
