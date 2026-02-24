import React, { useEffect, useState } from "react";
import { Card, Button, Table, Modal, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Mail, Plus, Trash2, Search, Settings, Eye, EyeOff } from "lucide-react";

interface EmailAccount {
  id: string;
  email: string;
  domain: string;
  quotaUsed: string;
  quotaTotal: string;
  status: string;
  createdAt: string;
}

export default function EmailPage() {
  const [accounts, setAccounts] = useState<EmailAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);
  const [search, setSearch] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [form, setForm] = useState({
    username: "",
    domain: "",
    password: "",
    quota: "1024",
  });

  const fetchAccounts = async () => {
    try {
      const res = await api.get("/email/accounts");
      setAccounts(res.data);
    } catch {
      toast.error("Failed to load email accounts");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchAccounts();
  }, []);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.username.trim() || !form.domain.trim() || !form.password.trim()) {
      toast.error("Please fill in all required fields");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/email/accounts", form);
      toast.success("Email account created");
      setShowCreate(false);
      setForm({ username: "", domain: "", password: "", quota: "1024" });
      fetchAccounts();
    } catch (err: any) {
      toast.error(err.response?.data?.message || "Failed to create email account");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async (id: string, email: string) => {
    if (!confirm(`Delete email account "${email}"?`)) return;
    try {
      await api.delete(`/email/accounts/${id}`);
      toast.success("Email account deleted");
      setAccounts((prev) => prev.filter((a) => a.id !== id));
    } catch {
      toast.error("Failed to delete email account");
    }
  };

  const filtered = accounts.filter((a) =>
    a.email.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      key: "email",
      header: "Email Address",
      render: (item: EmailAccount) => (
        <div className="flex items-center gap-2">
          <Mail size={16} className="text-cyan-400" />
          <span className="font-medium text-white">{item.email}</span>
        </div>
      ),
    },
    {
      key: "quota",
      header: "Storage",
      render: (item: EmailAccount) => (
        <div>
          <span className="text-panel-text">{item.quotaUsed}</span>
          <span className="text-panel-muted"> / {item.quotaTotal}</span>
        </div>
      ),
    },
    {
      key: "status",
      header: "Status",
      render: (item: EmailAccount) => <StatusBadge status={item.status} />,
    },
    {
      key: "createdAt",
      header: "Created",
      render: (item: EmailAccount) => (
        <span className="text-panel-muted">{item.createdAt}</span>
      ),
    },
    {
      key: "actions",
      header: "",
      render: (item: EmailAccount) => (
        <div className="flex items-center gap-2 justify-end">
          <button
            className="text-panel-muted hover:text-brand-400 transition-colors"
            title="Settings"
          >
            <Settings size={16} />
          </button>
          <button
            onClick={() => handleDelete(item.id, item.email)}
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
        title="Email Accounts"
        description="Create and manage email accounts for your domains"
        actions={
          <Button size="sm" onClick={() => setShowCreate(true)}>
            <Plus size={16} className="mr-1" /> Create Mailbox
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
              placeholder="Search email accounts..."
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
          emptyMessage="No email accounts found. Create your first mailbox."
        />
      </Card>

      <Modal
        isOpen={showCreate}
        onClose={() => setShowCreate(false)}
        title="Create Mailbox"
      >
        <form onSubmit={handleCreate} className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Username
              </label>
              <input
                type="text"
                value={form.username}
                onChange={(e) => setForm({ ...form, username: e.target.value })}
                placeholder="john"
                className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
              />
            </div>
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
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Storage Quota (MB)
            </label>
            <input
              type="number"
              value={form.quota}
              onChange={(e) => setForm({ ...form, quota: e.target.value })}
              placeholder="1024"
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
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
              Create Mailbox
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
