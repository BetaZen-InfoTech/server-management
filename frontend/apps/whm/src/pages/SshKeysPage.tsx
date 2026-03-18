import { useState, useEffect } from "react";
import { Card, Button, Table, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Key, Plus, RefreshCw, Search, Trash2, Copy } from "lucide-react";

interface SshKey {
  id: string;
  name: string;
  fingerprint: string;
  type: string;
  createdAt: string;
}

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

export default function SshKeysPage() {
  const [keys, setKeys] = useState<SshKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ name: "", public_key: "", type: "ed25519" });

  useEffect(() => {
    fetchKeys();
  }, []);

  const fetchKeys = async () => {
    setLoading(true);
    try {
      const res = await api.get("/ssh-keys/root");
      setKeys(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name || !form.public_key) {
      toast.error("Please fill all required fields");
      return;
    }
    setCreating(true);
    try {
      await api.post("/ssh-keys/root", form);
      toast.success(`SSH key "${form.name}" added`);
      setShowCreate(false);
      setForm({ name: "", public_key: "", type: "ed25519" });
      fetchKeys();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to add SSH key");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`Are you sure you want to delete SSH key "${name}"?`)) return;
    try {
      await api.delete(`/ssh-keys/root/${id}`);
      toast.success(`SSH key ${name} deleted`);
      fetchKeys();
    } catch {
      toast.error("Failed to delete SSH key");
    }
  };

  const handleCopyFingerprint = (fingerprint: string) => {
    navigator.clipboard.writeText(fingerprint);
    toast.success("Fingerprint copied to clipboard");
  };

  const filtered = keys.filter(
    (k) =>
      k.name.toLowerCase().includes(search.toLowerCase()) ||
      k.fingerprint.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Name",
      accessor: (k: SshKey) => (
        <div className="flex items-center gap-2">
          <Key size={14} className="text-yellow-400" />
          <span className="font-medium text-panel-text">{k.name}</span>
        </div>
      ),
    },
    {
      header: "Type",
      accessor: (k: SshKey) => (
        <span className="inline-flex items-center px-2 py-0.5 rounded bg-panel-bg text-xs font-medium text-panel-muted uppercase">
          {k.type}
        </span>
      ),
    },
    {
      header: "Fingerprint",
      accessor: (k: SshKey) => (
        <div className="flex items-center gap-2">
          <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono truncate max-w-[250px]">
            {k.fingerprint}
          </code>
          <button
            onClick={() => handleCopyFingerprint(k.fingerprint)}
            className="p-1 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors shrink-0"
          >
            <Copy size={12} />
          </button>
        </div>
      ),
    },
    {
      header: "Created",
      accessor: (k: SshKey) => (
        <span className="text-panel-muted text-sm">{k.createdAt}</span>
      ),
    },
    {
      header: "Actions",
      accessor: (k: SshKey) => (
        <button
          onClick={() => handleDelete(k.id, k.name)}
          className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
        >
          <Trash2 size={14} />
        </button>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">SSH Keys</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage SSH keys for secure server access
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchKeys}
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
            Add SSH Key
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search SSH keys..."
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
              {[1, 2, 3].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={filtered} />
        ) : (
          <div className="text-center py-16 px-4">
            <Key size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No SSH keys found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No SSH keys match your search. Try a different search term."
                : "Add SSH keys to enable secure passwordless access to your server."}
            </p>
            {!search && (
              <Button
                onClick={() => setShowCreate(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Add SSH Key
              </Button>
            )}
          </div>
        )}
      </Card>

      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Add SSH Key">
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className={labelClass}>Key Name *</label>
            <input type="text" required placeholder="My Laptop Key" value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>Key Type</label>
            <div className="flex gap-2">
              {["ed25519", "rsa", "ecdsa"].map((t) => (
                <button key={t} type="button" onClick={() => setForm({ ...form, type: t })}
                  className={`px-3 py-1.5 rounded-lg text-xs font-medium uppercase transition-colors ${
                    form.type === t
                      ? "bg-blue-600 text-white"
                      : "bg-panel-bg text-panel-muted border border-panel-border hover:text-panel-text"
                  }`}>
                  {t}
                </button>
              ))}
            </div>
          </div>
          <div>
            <label className={labelClass}>Public Key *</label>
            <textarea required placeholder="ssh-ed25519 AAAA... user@hostname" value={form.public_key}
              onChange={(e) => setForm({ ...form, public_key: e.target.value })}
              className={`${inputClass} min-h-[100px] resize-y font-mono text-xs`} rows={4} />
            <p className="text-xs text-panel-muted mt-1">
              Paste the contents of your public key file (e.g., ~/.ssh/id_ed25519.pub)
            </p>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Adding..." : "Add SSH Key"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
