import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Archive, Plus, RefreshCw, Search, Trash2, Download, HardDrive, Upload, Server, Wifi, RotateCcw } from "lucide-react";

interface RemoteDestination {
  protocol: string;
  host: string;
  port: number;
  username: string;
  password: string;
  path: string;
}

interface Backup {
  id: string;
  type: "full" | "files" | "database" | "email" | "config";
  domain: string;
  user: string;
  storage: string;
  status: string;
  size_mb: number;
  path: string;
  encrypted: boolean;
  compression: string;
  remote_destination?: RemoteDestination;
  created_at: string;
  completed_at: string;
}

const typeColors: Record<string, string> = {
  full: "bg-blue-500/10 text-blue-400",
  files: "bg-green-500/10 text-green-400",
  database: "bg-purple-500/10 text-purple-400",
  email: "bg-yellow-500/10 text-yellow-400",
  config: "bg-cyan-500/10 text-cyan-400",
};

const storageLabels: Record<string, string> = {
  local: "Local",
  remote: "Remote",
  both: "Local + Remote",
  s3: "S3",
};

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const selectClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";

export default function BackupsPage() {
  const [backups, setBackups] = useState<Backup[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [showRestore, setShowRestore] = useState(false);
  const [creating, setCreating] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const [testingConnection, setTestingConnection] = useState(false);
  const [form, setForm] = useState({
    type: "full", domain: "", user: "", storage: "local", compression: "gzip",
    remote_protocol: "sftp", remote_host: "", remote_port: "22", remote_user: "", remote_pass: "", remote_path: "/backups/",
  });
  const [restoreTab, setRestoreTab] = useState<"server" | "upload" | "remote">("server");
  const [restoreForm, setRestoreForm] = useState({
    backup_id: "", restore_type: "full", user: "", domain: "",
    remote_protocol: "sftp", remote_host: "", remote_port: "22", remote_user: "", remote_pass: "", remote_path: "",
  });
  const [restoreFile, setRestoreFile] = useState<File | null>(null);

  useEffect(() => {
    fetchBackups();
  }, []);

  const fetchBackups = async () => {
    setLoading(true);
    try {
      const res = await api.get("/backups");
      setBackups(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.domain || !form.user) {
      toast.error("Please fill all required fields");
      return;
    }
    setCreating(true);
    try {
      const payload: any = {
        type: form.type, domain: form.domain, user: form.user,
        storage: form.storage, compression: form.compression,
      };
      if (form.storage === "remote" || form.storage === "both") {
        if (!form.remote_host || !form.remote_user || !form.remote_pass) {
          toast.error("Please fill remote connection details");
          setCreating(false);
          return;
        }
        payload.remote_destination = {
          protocol: form.remote_protocol,
          host: form.remote_host,
          port: parseInt(form.remote_port) || 22,
          username: form.remote_user,
          password: form.remote_pass,
          path: form.remote_path,
        };
      }
      await api.post("/backups/", payload);
      toast.success("Backup created successfully");
      setShowCreate(false);
      setForm({ type: "full", domain: "", user: "", storage: "local", compression: "gzip", remote_protocol: "sftp", remote_host: "", remote_port: "22", remote_user: "", remote_pass: "", remote_path: "/backups/" });
      fetchBackups();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create backup");
    } finally {
      setCreating(false);
    }
  };

  const handleTestConnection = async () => {
    if (!form.remote_host || !form.remote_user || !form.remote_pass) {
      toast.error("Fill connection details first");
      return;
    }
    setTestingConnection(true);
    try {
      await api.post("/backups/test-connection", {
        protocol: form.remote_protocol,
        host: form.remote_host,
        port: parseInt(form.remote_port) || 22,
        username: form.remote_user,
        password: form.remote_pass,
      });
      toast.success("Connection successful");
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Connection failed");
    } finally {
      setTestingConnection(false);
    }
  };

  const handleRestore = async (e: React.FormEvent) => {
    e.preventDefault();
    setRestoring(true);
    try {
      if (restoreTab === "server") {
        if (!restoreForm.backup_id) { toast.error("Select a backup"); setRestoring(false); return; }
        await api.post("/backups/restore", {
          backup_id: restoreForm.backup_id,
          source: "server",
          restore_type: restoreForm.restore_type,
        });
      } else if (restoreTab === "upload") {
        if (!restoreFile || !restoreForm.user || !restoreForm.domain) {
          toast.error("Please fill all fields and select a file");
          setRestoring(false);
          return;
        }
        const fd = new FormData();
        fd.append("file", restoreFile);
        fd.append("restore_type", restoreForm.restore_type);
        fd.append("user", restoreForm.user);
        fd.append("domain", restoreForm.domain);
        await api.post("/backups/restore/upload", fd, { headers: { "Content-Type": "multipart/form-data" } });
      } else if (restoreTab === "remote") {
        if (!restoreForm.remote_host || !restoreForm.remote_user || !restoreForm.remote_pass || !restoreForm.remote_path) {
          toast.error("Please fill all remote connection details");
          setRestoring(false);
          return;
        }
        await api.post("/backups/restore", {
          source: "remote",
          restore_type: restoreForm.restore_type,
          user: restoreForm.user,
          domain: restoreForm.domain,
          remote_destination: {
            protocol: restoreForm.remote_protocol,
            host: restoreForm.remote_host,
            port: parseInt(restoreForm.remote_port) || 22,
            username: restoreForm.remote_user,
            password: restoreForm.remote_pass,
            path: restoreForm.remote_path,
          },
        });
      }
      toast.success("Restore completed successfully");
      setShowRestore(false);
      fetchBackups();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Restore failed");
    } finally {
      setRestoring(false);
    }
  };

  const handleDelete = async (id: string, domain: string) => {
    if (!confirm(`Are you sure you want to delete backup for "${domain}"?`)) return;
    try {
      await api.delete(`/backups/${id}`);
      toast.success(`Backup deleted`);
      fetchBackups();
    } catch {
      toast.error("Failed to delete backup");
    }
  };

  const handleDownload = async (id: string) => {
    try {
      toast.success("Backup download started");
      window.open(`/api/v1/whm/backups/${id}/download`, "_blank");
    } catch {
      toast.error("Failed to download backup");
    }
  };

  const filtered = backups.filter((b) =>
    (b.domain || "").toLowerCase().includes(search.toLowerCase()) ||
    (b.type || "").toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Domain",
      accessor: (b: Backup) => (
        <div className="flex items-center gap-2">
          <Archive size={14} className="text-orange-400" />
          <span className="font-medium text-panel-text">{b.domain}</span>
        </div>
      ),
    },
    {
      header: "Type",
      accessor: (b: Backup) => (
        <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium capitalize ${typeColors[b.type] || "bg-panel-bg text-panel-muted"}`}>
          {b.type}
        </span>
      ),
    },
    {
      header: "Storage",
      accessor: (b: Backup) => (
        <div className="flex items-center gap-1.5">
          {b.storage === "remote" || b.storage === "both" ? <Wifi size={12} className="text-blue-400" /> : <Server size={12} className="text-panel-muted" />}
          <span className="text-panel-muted text-sm">{storageLabels[b.storage] || b.storage}</span>
        </div>
      ),
    },
    {
      header: "Size",
      accessor: (b: Backup) => (
        <div className="flex items-center gap-1.5 text-panel-muted">
          <HardDrive size={12} />
          <span>{b.size_mb ? `${b.size_mb.toFixed(1)} MB` : "--"}</span>
        </div>
      ),
    },
    {
      header: "Status",
      accessor: (b: Backup) => <StatusBadge status={b.status === "in_progress" ? "pending" : b.status === "completed" ? "active" : b.status === "failed" ? "failed" : b.status} />,
    },
    {
      header: "Created",
      accessor: (b: Backup) => (
        <span className="text-panel-muted text-sm">{b.created_at ? new Date(b.created_at).toLocaleDateString() : "--"}</span>
      ),
    },
    {
      header: "Actions",
      accessor: (b: Backup) => (
        <div className="flex items-center gap-1">
          {b.path && (
            <button onClick={() => handleDownload(b.id)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors" title="Download">
              <Download size={14} />
            </button>
          )}
          <button onClick={() => handleDelete(b.id, b.domain)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors" title="Delete">
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  const remoteFields = (prefix: string, vals: any, setVals: (v: any) => void) => (
    <div className="space-y-3 p-3 bg-panel-bg/50 rounded-lg border border-panel-border">
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className={labelClass}>Protocol</label>
          <select value={vals[`${prefix}_protocol`]} onChange={(e) => {
            const proto = e.target.value;
            const port = proto === "ftp" ? "21" : "22";
            setVals({ ...vals, [`${prefix}_protocol`]: proto, [`${prefix}_port`]: port });
          }} className={selectClass}>
            <option value="sftp">SFTP</option>
            <option value="ftp">FTP</option>
            <option value="scp">SCP</option>
          </select>
        </div>
        <div>
          <label className={labelClass}>Port</label>
          <input type="number" value={vals[`${prefix}_port`]} onChange={(e) => setVals({ ...vals, [`${prefix}_port`]: e.target.value })} className={inputClass} />
        </div>
      </div>
      <div>
        <label className={labelClass}>Host *</label>
        <input type="text" placeholder="192.168.1.100" value={vals[`${prefix}_host`]} onChange={(e) => setVals({ ...vals, [`${prefix}_host`]: e.target.value })} className={inputClass} />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className={labelClass}>Username *</label>
          <input type="text" placeholder="root" value={vals[`${prefix}_user`]} onChange={(e) => setVals({ ...vals, [`${prefix}_user`]: e.target.value })} className={inputClass} />
        </div>
        <div>
          <label className={labelClass}>Password *</label>
          <input type="password" value={vals[`${prefix}_pass`]} onChange={(e) => setVals({ ...vals, [`${prefix}_pass`]: e.target.value })} className={inputClass} />
        </div>
      </div>
      <div>
        <label className={labelClass}>Remote Path *</label>
        <input type="text" placeholder="/backups/" value={vals[`${prefix}_path`]} onChange={(e) => setVals({ ...vals, [`${prefix}_path`]: e.target.value })} className={inputClass} />
      </div>
    </div>
  );

  const tabClass = (active: boolean) =>
    `px-4 py-2 text-sm font-medium rounded-t-lg transition-colors ${active ? "bg-panel-surface text-panel-text border border-panel-border border-b-transparent" : "text-panel-muted hover:text-panel-text"}`;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Backups</h1>
          <p className="text-panel-muted text-sm mt-1">Create and manage server backups</p>
        </div>
        <div className="flex items-center gap-2">
          <Button onClick={fetchBackups} className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm">
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
          </Button>
          <Button onClick={() => setShowRestore(true)} className="flex items-center gap-2 px-4 py-2 bg-green-600 hover:bg-green-700 text-white rounded-lg text-sm font-medium transition-colors">
            <RotateCcw size={14} /> Restore
          </Button>
          <Button onClick={() => setShowCreate(true)} className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
            <Plus size={14} /> Create Backup
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input type="text" placeholder="Search backups..." value={search} onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm" />
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
            <Archive size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No backups found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search ? "No backups match your search. Try a different search term." : "Create your first backup to protect your server data and configurations."}
            </p>
            {!search && (
              <Button onClick={() => setShowCreate(true)} className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
                <Plus size={14} /> Create Backup
              </Button>
            )}
          </div>
        )}
      </Card>

      {/* Create Backup Modal */}
      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Create Backup" size="lg">
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className={labelClass}>Backup Type *</label>
            <select value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })} className={selectClass}>
              <option value="full">Full Backup</option>
              <option value="files">Files Only</option>
              <option value="database">Database Only</option>
              <option value="email">Email Only</option>
              <option value="config">Config Only</option>
            </select>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Domain *</label>
              <input type="text" required placeholder="example.com" value={form.domain} onChange={(e) => setForm({ ...form, domain: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>System User *</label>
              <input type="text" required placeholder="username" value={form.user} onChange={(e) => setForm({ ...form, user: e.target.value })} className={inputClass} />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Storage Destination *</label>
              <select value={form.storage} onChange={(e) => setForm({ ...form, storage: e.target.value })} className={selectClass}>
                <option value="local">Local Server</option>
                <option value="remote">Remote (FTP/SFTP)</option>
                <option value="both">Local + Remote</option>
              </select>
            </div>
            <div>
              <label className={labelClass}>Compression</label>
              <select value={form.compression} onChange={(e) => setForm({ ...form, compression: e.target.value })} className={selectClass}>
                <option value="gzip">Gzip</option>
                <option value="zstd">Zstandard</option>
              </select>
            </div>
          </div>

          {(form.storage === "remote" || form.storage === "both") && (
            <>
              <div className="flex items-center justify-between">
                <label className="block text-sm font-medium text-panel-text">Remote Connection</label>
                <button type="button" onClick={handleTestConnection} disabled={testingConnection}
                  className="px-3 py-1 text-xs bg-panel-bg border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors disabled:opacity-50">
                  {testingConnection ? "Testing..." : "Test Connection"}
                </button>
              </div>
              {remoteFields("remote", form, setForm)}
            </>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)} className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Cancel</button>
            <button type="submit" disabled={creating} className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Creating..." : "Create Backup"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Restore Modal */}
      <Modal isOpen={showRestore} onClose={() => setShowRestore(false)} title="Restore Backup" size="lg">
        <div className="space-y-4">
          <div className="flex gap-1 border-b border-panel-border">
            <button onClick={() => setRestoreTab("server")} className={tabClass(restoreTab === "server")}>
              <span className="flex items-center gap-1.5"><Server size={14} /> From Server</span>
            </button>
            <button onClick={() => setRestoreTab("upload")} className={tabClass(restoreTab === "upload")}>
              <span className="flex items-center gap-1.5"><Upload size={14} /> Upload File</span>
            </button>
            <button onClick={() => setRestoreTab("remote")} className={tabClass(restoreTab === "remote")}>
              <span className="flex items-center gap-1.5"><Wifi size={14} /> From Remote</span>
            </button>
          </div>

          <form onSubmit={handleRestore} className="space-y-4">
            {restoreTab === "server" && (
              <>
                <div>
                  <label className={labelClass}>Select Backup *</label>
                  <select value={restoreForm.backup_id} onChange={(e) => setRestoreForm({ ...restoreForm, backup_id: e.target.value })} className={selectClass}>
                    <option value="">-- Select a backup --</option>
                    {backups.filter(b => b.status === "completed" && b.path).map(b => (
                      <option key={b.id} value={b.id}>{b.domain} - {b.type} ({new Date(b.created_at).toLocaleDateString()})</option>
                    ))}
                  </select>
                </div>
                <div>
                  <label className={labelClass}>Restore Type</label>
                  <select value={restoreForm.restore_type} onChange={(e) => setRestoreForm({ ...restoreForm, restore_type: e.target.value })} className={selectClass}>
                    <option value="full">Full Restore</option>
                    <option value="files">Files Only</option>
                    <option value="database">Database Only</option>
                    <option value="email">Email Only</option>
                  </select>
                </div>
              </>
            )}

            {restoreTab === "upload" && (
              <>
                <div>
                  <label className={labelClass}>Backup File *</label>
                  <div className="border-2 border-dashed border-panel-border rounded-lg p-6 text-center hover:border-blue-500/40 transition-colors">
                    <input type="file" onChange={(e) => setRestoreFile(e.target.files?.[0] || null)}
                      className="w-full text-sm text-panel-muted file:mr-4 file:py-2 file:px-4 file:rounded-lg file:border-0 file:text-sm file:font-medium file:bg-blue-600 file:text-white hover:file:bg-blue-700" />
                    {restoreFile && <p className="text-sm text-panel-muted mt-2">{restoreFile.name} ({(restoreFile.size / 1024 / 1024).toFixed(1)} MB)</p>}
                  </div>
                </div>
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className={labelClass}>Domain *</label>
                    <input type="text" placeholder="example.com" value={restoreForm.domain} onChange={(e) => setRestoreForm({ ...restoreForm, domain: e.target.value })} className={inputClass} />
                  </div>
                  <div>
                    <label className={labelClass}>System User *</label>
                    <input type="text" placeholder="username" value={restoreForm.user} onChange={(e) => setRestoreForm({ ...restoreForm, user: e.target.value })} className={inputClass} />
                  </div>
                </div>
                <div>
                  <label className={labelClass}>Restore Type</label>
                  <select value={restoreForm.restore_type} onChange={(e) => setRestoreForm({ ...restoreForm, restore_type: e.target.value })} className={selectClass}>
                    <option value="full">Full Restore</option>
                    <option value="files">Files Only</option>
                    <option value="database">Database Only</option>
                    <option value="email">Email Only</option>
                  </select>
                </div>
              </>
            )}

            {restoreTab === "remote" && (
              <>
                {remoteFields("remote", restoreForm, setRestoreForm)}
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className={labelClass}>Domain *</label>
                    <input type="text" placeholder="example.com" value={restoreForm.domain} onChange={(e) => setRestoreForm({ ...restoreForm, domain: e.target.value })} className={inputClass} />
                  </div>
                  <div>
                    <label className={labelClass}>System User *</label>
                    <input type="text" placeholder="username" value={restoreForm.user} onChange={(e) => setRestoreForm({ ...restoreForm, user: e.target.value })} className={inputClass} />
                  </div>
                </div>
                <div>
                  <label className={labelClass}>Restore Type</label>
                  <select value={restoreForm.restore_type} onChange={(e) => setRestoreForm({ ...restoreForm, restore_type: e.target.value })} className={selectClass}>
                    <option value="full">Full Restore</option>
                    <option value="files">Files Only</option>
                    <option value="database">Database Only</option>
                    <option value="email">Email Only</option>
                  </select>
                </div>
              </>
            )}

            <div className="flex justify-end gap-3 pt-2">
              <button type="button" onClick={() => setShowRestore(false)} className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Cancel</button>
              <button type="submit" disabled={restoring} className="px-4 py-2 text-sm bg-green-600 hover:bg-green-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
                {restoring ? "Restoring..." : "Restore"}
              </button>
            </div>
          </form>
        </div>
      </Modal>
    </div>
  );
}
