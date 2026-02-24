import React, { useEffect, useState } from "react";
import { Card, Button, Table, Modal, StatusBadge, CodeBlock } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Database, Plus, Trash2, Copy, Search, Eye, EyeOff } from "lucide-react";

interface DatabaseRecord {
  id: string;
  name: string;
  type: string;
  size: string;
  status: string;
  user: string;
  host: string;
  createdAt: string;
}

export default function DatabasesPage() {
  const [databases, setDatabases] = useState<DatabaseRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);
  const [search, setSearch] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [form, setForm] = useState({
    name: "",
    type: "mysql",
    username: "",
    password: "",
  });

  const fetchDatabases = async () => {
    try {
      const res = await api.get("/databases");
      setDatabases(res.data);
    } catch {
      toast.error("Failed to load databases");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchDatabases();
  }, []);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name.trim() || !form.username.trim() || !form.password.trim()) {
      toast.error("Please fill in all required fields");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/databases", form);
      toast.success("Database created successfully");
      setShowCreate(false);
      setForm({ name: "", type: "mysql", username: "", password: "" });
      fetchDatabases();
    } catch (err: any) {
      toast.error(err.response?.data?.message || "Failed to create database");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Delete database "${name}"? This cannot be undone.`)) return;
    try {
      await api.delete(`/databases/${id}`);
      toast.success("Database deleted");
      setDatabases((prev) => prev.filter((d) => d.id !== id));
    } catch {
      toast.error("Failed to delete database");
    }
  };

  const filtered = databases.filter((d) =>
    d.name.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      key: "name",
      header: "Database",
      render: (item: DatabaseRecord) => (
        <div className="flex items-center gap-2">
          <Database size={16} className="text-green-400" />
          <span className="font-medium text-white">{item.name}</span>
        </div>
      ),
    },
    {
      key: "type",
      header: "Type",
      render: (item: DatabaseRecord) => (
        <span className="text-panel-muted uppercase text-xs font-medium">
          {item.type}
        </span>
      ),
    },
    {
      key: "user",
      header: "User",
      render: (item: DatabaseRecord) => (
        <span className="text-panel-text">{item.user}</span>
      ),
    },
    {
      key: "size",
      header: "Size",
      render: (item: DatabaseRecord) => (
        <span className="text-panel-muted">{item.size}</span>
      ),
    },
    {
      key: "status",
      header: "Status",
      render: (item: DatabaseRecord) => <StatusBadge status={item.status} />,
    },
    {
      key: "actions",
      header: "",
      render: (item: DatabaseRecord) => (
        <div className="flex items-center gap-2 justify-end">
          <button
            onClick={() => {
              navigator.clipboard.writeText(
                `mysql -u ${item.user} -p -h ${item.host} ${item.name}`
              );
              toast.success("Connection string copied");
            }}
            className="text-panel-muted hover:text-brand-400 transition-colors"
            title="Copy connection string"
          >
            <Copy size={16} />
          </button>
          <button
            onClick={() => handleDelete(item.id, item.name)}
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
        title="Databases"
        description="Manage your MySQL and PostgreSQL databases"
        actions={
          <Button size="sm" onClick={() => setShowCreate(true)}>
            <Plus size={16} className="mr-1" /> Create Database
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
              placeholder="Search databases..."
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
          emptyMessage="No databases found. Create your first database to get started."
        />
      </Card>

      <Modal
        isOpen={showCreate}
        onClose={() => setShowCreate(false)}
        title="Create Database"
      >
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Database Type
            </label>
            <select
              value={form.type}
              onChange={(e) => setForm({ ...form, type: e.target.value })}
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500"
            >
              <option value="mysql">MySQL</option>
              <option value="postgresql">PostgreSQL</option>
            </select>
          </div>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Database Name
            </label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="my_database"
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Username
            </label>
            <input
              type="text"
              value={form.username}
              onChange={(e) => setForm({ ...form, username: e.target.value })}
              placeholder="db_user"
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Password
            </label>
            <div className="relative">
              <input
                type={showPassword ? "text" : "password"}
                value={form.password}
                onChange={(e) => setForm({ ...form, password: e.target.value })}
                placeholder="Strong password"
                className="w-full px-4 py-2.5 pr-10 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
              />
              <button
                type="button"
                onClick={() => setShowPassword(!showPassword)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-panel-muted hover:text-panel-text"
              >
                {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
              </button>
            </div>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowCreate(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Create Database
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
