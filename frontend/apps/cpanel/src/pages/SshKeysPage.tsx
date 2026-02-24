import React, { useEffect, useState } from "react";
import { Card, Button, Table, Modal, CodeBlock } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Key, Plus, Trash2, Search, Copy, Fingerprint } from "lucide-react";

interface SshKey {
  id: string;
  name: string;
  fingerprint: string;
  type: string;
  createdAt: string;
  lastUsed: string;
}

export default function SshKeysPage() {
  const [keys, setKeys] = useState<SshKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [showAdd, setShowAdd] = useState(false);
  const [search, setSearch] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [form, setForm] = useState({
    name: "",
    publicKey: "",
  });

  const fetchKeys = async () => {
    try {
      const res = await api.get("/ssh-keys");
      setKeys(res.data.data || []);
    } catch {
      toast.error("Failed to load SSH keys");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchKeys();
  }, []);

  const handleAdd = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name.trim() || !form.publicKey.trim()) {
      toast.error("Please fill in all fields");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/ssh-keys", form);
      toast.success("SSH key added successfully");
      setShowAdd(false);
      setForm({ name: "", publicKey: "" });
      fetchKeys();
    } catch (err: any) {
      toast.error(err.response?.data?.message || "Failed to add SSH key");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Remove SSH key "${name}"?`)) return;
    try {
      await api.delete(`/ssh-keys/${id}`);
      toast.success("SSH key removed");
      setKeys((prev) => prev.filter((k) => k.id !== id));
    } catch {
      toast.error("Failed to remove SSH key");
    }
  };

  const filtered = keys.filter(
    (k) =>
      k.name.toLowerCase().includes(search.toLowerCase()) ||
      k.fingerprint.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      key: "name",
      header: "Name",
      render: (item: SshKey) => (
        <div className="flex items-center gap-2">
          <Key size={16} className="text-amber-400" />
          <span className="font-medium text-white">{item.name}</span>
        </div>
      ),
    },
    {
      key: "fingerprint",
      header: "Fingerprint",
      render: (item: SshKey) => (
        <div className="flex items-center gap-2">
          <Fingerprint size={14} className="text-panel-muted" />
          <code className="text-xs font-mono text-panel-muted">
            {item.fingerprint}
          </code>
        </div>
      ),
    },
    {
      key: "type",
      header: "Type",
      render: (item: SshKey) => (
        <span className="text-panel-muted uppercase text-xs font-medium">
          {item.type}
        </span>
      ),
    },
    {
      key: "createdAt",
      header: "Added",
      render: (item: SshKey) => (
        <span className="text-panel-muted">{item.createdAt}</span>
      ),
    },
    {
      key: "lastUsed",
      header: "Last Used",
      render: (item: SshKey) => (
        <span className="text-panel-muted">
          {item.lastUsed || "Never"}
        </span>
      ),
    },
    {
      key: "actions",
      header: "",
      render: (item: SshKey) => (
        <div className="flex items-center gap-2 justify-end">
          <button
            onClick={() => {
              navigator.clipboard.writeText(item.fingerprint);
              toast.success("Fingerprint copied");
            }}
            className="text-panel-muted hover:text-brand-400 transition-colors"
            title="Copy fingerprint"
          >
            <Copy size={16} />
          </button>
          <button
            onClick={() => handleDelete(item.id, item.name)}
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
        title="SSH Keys"
        description="Manage SSH public keys for secure server access"
        actions={
          <Button size="sm" onClick={() => setShowAdd(true)}>
            <Plus size={16} className="mr-1" /> Add Key
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
              placeholder="Search keys..."
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
          emptyMessage="No SSH keys found. Add a key to enable SSH access."
        />
      </Card>

      <Card title="SSH Connection Info">
        <div className="space-y-3">
          <p className="text-sm text-panel-muted">
            Use the following command to connect to your server via SSH:
          </p>
          <CodeBlock
            code="ssh username@server.example.com -p 22"
            language="bash"
          />
          <p className="text-xs text-panel-muted">
            Replace <code className="text-brand-400">username</code> with your
            account username and{" "}
            <code className="text-brand-400">server.example.com</code> with
            your server hostname.
          </p>
        </div>
      </Card>

      <Modal
        isOpen={showAdd}
        onClose={() => setShowAdd(false)}
        title="Add SSH Key"
        size="lg"
      >
        <form onSubmit={handleAdd} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Key Name
            </label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="My Laptop"
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Public Key
            </label>
            <textarea
              value={form.publicKey}
              onChange={(e) => setForm({ ...form, publicKey: e.target.value })}
              placeholder="ssh-rsa AAAAB3... or ssh-ed25519 AAAAC3..."
              rows={5}
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500 font-mono"
            />
            <p className="text-xs text-panel-muted mt-1">
              Paste your public key (usually found in ~/.ssh/id_rsa.pub or
              ~/.ssh/id_ed25519.pub)
            </p>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowAdd(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Add Key
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
