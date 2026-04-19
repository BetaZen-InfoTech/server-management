import { useState, useEffect } from "react";
import { Card, Button, Table, Modal, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Database,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  Users,
  Copy,
  Eye,
  EyeOff,
  KeyRound,
  Link2,
  ExternalLink,
  ShieldCheck,
  Globe,
  AlertCircle,
} from "lucide-react";

interface DatabaseItem {
  id: string;
  db_name: string;
  type: "mongodb" | "mysql";
  size_mb: number;
  username: string;
  domain: string;
  host: string;
  port: number;
  created_at: string;
}

interface DatabaseUser {
  id: string;
  database_id: string;
  username: string;
  role: string;
  created_at: string;
}

interface AccessHost {
  id: string;
  database_id: string;
  host: string;
  comment: string;
  created_at: string;
}

// DomainOption mirrors the /domains endpoint shape. `user` is the Linux
// account that owns the domain's filesystem tree — the backend uses it as
// the `<user>_` prefix on new database + user names, exactly like cPanel.
interface DomainOption {
  id: string;
  domain: string;
  user: string;
}

interface ConnectionInfo {
  type: string;
  host: string;
  port: number;
  database: string;
  username: string;
  password: string;
  connection_string: string;
  cli_command: string;
}

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const selectClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";

const MONGO_ROLES = [
  { value: "read", label: "Read only" },
  { value: "readWrite", label: "Read / Write" },
  { value: "dbAdmin", label: "DB Admin" },
  { value: "dbOwner", label: "DB Owner" },
  { value: "userAdmin", label: "User Admin" },
];

const MYSQL_ROLES = [
  { value: "read", label: "Read only (SELECT)" },
  { value: "readWrite", label: "Read / Write" },
  { value: "dbAdmin", label: "DB Admin" },
  { value: "dbOwner", label: "DB Owner (ALL)" },
];

const roleLabel = (role: string) => {
  const all = [...MONGO_ROLES, ...MYSQL_ROLES];
  return all.find((r) => r.value === role)?.label || role;
};

const roleColor = (role: string) => {
  switch (role) {
    case "read":
      return "bg-slate-500/15 text-slate-300 border-slate-500/30";
    case "readWrite":
      return "bg-blue-500/15 text-blue-400 border-blue-500/30";
    case "dbAdmin":
      return "bg-purple-500/15 text-purple-400 border-purple-500/30";
    case "dbOwner":
    case "userAdmin":
      return "bg-amber-500/15 text-amber-400 border-amber-500/30";
    default:
      return "bg-panel-border/30 text-panel-muted border-panel-border";
  }
};

export default function DatabasesPage() {
  const [databases, setDatabases] = useState<DatabaseItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ db_name: "", type: "mongodb", username: "", password: "", domain: "" });

  // Connection / users / phpMyAdmin / access-hosts modal state
  const [activeDB, setActiveDB] = useState<DatabaseItem | null>(null);
  const [modal, setModal] = useState<"connection" | "users" | "phpmyadmin" | "access-hosts" | null>(null);
  const [connection, setConnection] = useState<ConnectionInfo | null>(null);
  const [users, setUsers] = useState<DatabaseUser[]>([]);
  const [showSecret, setShowSecret] = useState(false);

  // Remote Database Access (access hosts) modal state
  const [accessHosts, setAccessHosts] = useState<AccessHost[]>([]);
  const [accessHostForm, setAccessHostForm] = useState({ host: "", comment: "" });
  const [addingHost, setAddingHost] = useState(false);

  // Add user form
  const [showAddUser, setShowAddUser] = useState(false);
  const [newUser, setNewUser] = useState({ username: "", password: "", role: "readWrite" });
  const [savingUser, setSavingUser] = useState(false);

  // Password change inline state — keyed by user id ("owner" key for primary)
  const [pwForms, setPwForms] = useState<Record<string, string>>({});
  const [pwOpen, setPwOpen] = useState<Record<string, boolean>>({});

  // Domains owned by the current vendor — used to populate the Create
  // Database dialog's Domain dropdown and derive the `<user>_` prefix the
  // backend will apply to the DB name and username.
  const [availableDomains, setAvailableDomains] = useState<DomainOption[]>([]);

  useEffect(() => {
    fetchDatabases();
    fetchDomainsForCreate();
  }, []);

  const fetchDomainsForCreate = async () => {
    try {
      const res = await api.get("/domains?limit=500");
      setAvailableDomains(res.data?.data || []);
    } catch {
      // empty — the Domain dropdown will show a helpful "add a domain first" hint
    }
  };

  // Derive the prefix (`<owner>_`) from the currently-selected domain.
  // Backend enforces the same lookup in database_service.go; showing it here
  // means the operator can see exactly what the final DB / user name will be
  // before clicking Create, so they stop typing things like
  // "adminbetazeninfotechcom" as the username by hand.
  const selectedDomainOwner = availableDomains.find((d) => d.domain === form.domain)?.user || "";
  const dbPrefix = selectedDomainOwner ? `${selectedDomainOwner}_` : "";
  const dbNameFull = dbPrefix && form.db_name ? `${dbPrefix}${form.db_name}` : "";
  const dbUserFull = dbPrefix && form.username ? `${dbPrefix}${form.username}` : "";

  // Generate a random 16-char password with mixed case + digits + symbols.
  // Used by the "Generate" button next to the password field — matches
  // cPanel's password helper so operators aren't tempted to type weak ones.
  const generatePassword = () => {
    const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*";
    const arr = new Uint32Array(16);
    (typeof crypto !== "undefined" ? crypto : window.crypto).getRandomValues(arr);
    const pw = Array.from(arr, (n) => alphabet[n % alphabet.length]).join("");
    setForm((f) => ({ ...f, password: pw }));
    setShowSecret(true);
  };

  const fetchDatabases = async () => {
    setLoading(true);
    try {
      const res = await api.get("/databases");
      setDatabases(res.data.data || []);
    } catch {
      // empty
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.db_name || !form.username || !form.password || !form.domain) {
      toast.error("Please fill all required fields");
      return;
    }
    setCreating(true);
    try {
      await api.post("/databases/", form);
      toast.success(`Database ${form.db_name} created`);
      setShowCreate(false);
      setForm({ db_name: "", type: "mongodb", username: "", password: "", domain: "" });
      fetchDatabases();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create database");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string, name: string) => {
    if (!await confirmAction({ title: "Delete?", description: `Delete database "${name}"? This action cannot be undone.`, danger: true, confirmLabel: "Delete" })) return;
    try {
      await api.delete(`/databases/${id}`);
      toast.success(`Database ${name} deleted`);
      fetchDatabases();
    } catch {
      toast.error("Failed to delete database");
    }
  };

  const openConnection = async (d: DatabaseItem) => {
    setActiveDB(d);
    setModal("connection");
    setShowSecret(false);
    setConnection(null);
    try {
      const res = await api.get(`/databases/${d.id}/connection`);
      setConnection(res.data.data);
    } catch {
      toast.error("Failed to load connection info");
    }
  };

  const openUsers = async (d: DatabaseItem) => {
    setActiveDB(d);
    setModal("users");
    setUsers([]);
    setShowAddUser(false);
    setNewUser({ username: "", password: "", role: "readWrite" });
    setPwForms({});
    setPwOpen({});
    try {
      const res = await api.get(`/databases/${d.id}/users`);
      setUsers(res.data.data || []);
    } catch {
      toast.error("Failed to load users");
    }
  };

  const openAccessHosts = async (d: DatabaseItem) => {
    setActiveDB(d);
    setModal("access-hosts");
    setAccessHosts([]);
    setAccessHostForm({ host: "", comment: "" });
    try {
      const res = await api.get(`/databases/${d.id}/access-hosts`);
      setAccessHosts(res.data.data || []);
    } catch {
      toast.error("Failed to load access hosts");
    }
  };

  const handleAddAccessHost = async () => {
    if (!activeDB) return;
    const host = accessHostForm.host.trim();
    if (!host) return toast.error("Host is required");
    setAddingHost(true);
    try {
      await api.post(`/databases/${activeDB.id}/access-hosts`, {
        host,
        comment: accessHostForm.comment.trim(),
      });
      setAccessHostForm({ host: "", comment: "" });
      const res = await api.get(`/databases/${activeDB.id}/access-hosts`);
      setAccessHosts(res.data.data || []);
      toast.success(`Host "${host}" authorised`);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to add host");
    } finally {
      setAddingHost(false);
    }
  };

  const handleRemoveAccessHost = async (hostId: string, host: string) => {
    if (!activeDB) return;
    const ok = await confirmAction({
      title: "Remove access host?",
      description: `Drop remote access for "${host}"? The MySQL host-scoped user is dropped; firewall rules are left in place.`,
      confirmLabel: "Remove",
      danger: true,
    });
    if (!ok) return;
    try {
      await api.delete(`/databases/${activeDB.id}/access-hosts/${hostId}`);
      setAccessHosts((prev) => prev.filter((h) => h.id !== hostId));
      toast.success("Access host removed");
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to remove host");
    }
  };

  const openPhpMyAdmin = async (d: DatabaseItem) => {
    if (d.type !== "mysql") {
      toast.error("phpMyAdmin is only available for MySQL");
      return;
    }
    setActiveDB(d);
    setModal("phpmyadmin");
    setShowSecret(false);
    try {
      const res = await api.get(`/databases/${d.id}/phpmyadmin`);
      setConnection({
        type: "mysql",
        host: res.data.data.server,
        port: 3306,
        database: res.data.data.database,
        username: res.data.data.username,
        password: res.data.data.password,
        connection_string: res.data.data.url,
        cli_command: "",
      });
    } catch {
      toast.error("Failed to load phpMyAdmin info");
    }
  };

  const refreshUsers = async () => {
    if (!activeDB) return;
    try {
      const res = await api.get(`/databases/${activeDB.id}/users`);
      setUsers(res.data.data || []);
    } catch {
      // skip
    }
  };

  const handleAddUser = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!activeDB) return;
    if (!newUser.username || newUser.password.length < 8) {
      toast.error("Username and 8+ char password required");
      return;
    }
    setSavingUser(true);
    try {
      await api.post(`/databases/${activeDB.id}/users`, newUser);
      toast.success(`User ${newUser.username} added`);
      setNewUser({ username: "", password: "", role: "readWrite" });
      setShowAddUser(false);
      refreshUsers();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to add user");
    } finally {
      setSavingUser(false);
    }
  };

  const handleDeleteUser = async (userId: string, username: string) => {
    if (!activeDB) return;
    if (username === activeDB.username) {
      toast.error("Cannot delete the database owner");
      return;
    }
    if (!await confirmAction({ title: "Remove?", description: `Remove user "${username}"?`, danger: true, confirmLabel: "Remove" })) return;
    try {
      await api.delete(`/databases/${activeDB.id}/users/${userId}`);
      toast.success("User removed");
      refreshUsers();
    } catch {
      toast.error("Failed to remove user");
    }
  };

  const handleChangeUserPassword = async (userId: string) => {
    if (!activeDB) return;
    const password = pwForms[userId];
    if (!password || password.length < 8) {
      toast.error("Password must be 8+ characters");
      return;
    }
    try {
      if (userId === "owner") {
        await api.put(`/databases/${activeDB.id}/password`, { password });
      } else {
        await api.put(`/databases/${activeDB.id}/users/${userId}/password`, { password });
      }
      toast.success("Password updated");
      setPwForms({ ...pwForms, [userId]: "" });
      setPwOpen({ ...pwOpen, [userId]: false });
      if (userId === "owner") fetchDatabases();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update password");
    }
  };

  const handleChangeUserRole = async (userId: string, role: string) => {
    if (!activeDB) return;
    try {
      await api.put(`/databases/${activeDB.id}/users/${userId}/role`, { role });
      toast.success("Role updated");
      refreshUsers();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update role");
    }
  };

  const copy = async (text: string, label = "Copied") => {
    try {
      await navigator.clipboard.writeText(text);
      toast.success(label);
    } catch {
      toast.error("Copy failed");
    }
  };

  const filtered = databases.filter((d) => (d.db_name || "").toLowerCase().includes(search.toLowerCase()));

  const columns = [
    {
      header: "Name",
      accessor: (d: DatabaseItem) => (
        <div className="flex items-center gap-2">
          <Database size={14} className="text-purple-400" />
          <span className="font-medium text-panel-text">{d.db_name}</span>
        </div>
      ),
    },
    {
      header: "Type",
      accessor: (d: DatabaseItem) => (
        <span
          className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-xs font-medium ${
            d.type === "mongodb" ? "bg-green-500/10 text-green-400" : "bg-blue-500/10 text-blue-400"
          }`}
        >
          {d.type === "mongodb" ? "MongoDB" : "MySQL"}
        </span>
      ),
    },
    {
      header: "Size",
      accessor: (d: DatabaseItem) => (
        <span className="text-panel-muted">{d.size_mb ? `${d.size_mb} MB` : "--"}</span>
      ),
    },
    {
      header: "Owner",
      accessor: (d: DatabaseItem) => (
        <div className="flex items-center gap-1 text-panel-muted">
          <Users size={12} />
          <span>{d.username}</span>
        </div>
      ),
    },
    {
      header: "Domain",
      accessor: (d: DatabaseItem) => <span className="text-panel-muted text-sm">{d.domain}</span>,
    },
    {
      header: "Created",
      accessor: (d: DatabaseItem) => (
        <span className="text-panel-muted text-sm">
          {d.created_at ? new Date(d.created_at).toLocaleDateString() : "--"}
        </span>
      ),
    },
    {
      header: "Actions",
      accessor: (d: DatabaseItem) => (
        <div className="flex items-center gap-1">
          <button
            onClick={() => openConnection(d)}
            title="Connection info"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
          >
            <Link2 size={14} />
          </button>
          <button
            onClick={() => openUsers(d)}
            title="Manage users"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-purple-400 transition-colors"
          >
            <Users size={14} />
          </button>
          <button
            onClick={() => openAccessHosts(d)}
            title="Remote Database Access"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
          >
            <Globe size={14} />
          </button>
          {d.type === "mysql" && (
            <button
              onClick={() => openPhpMyAdmin(d)}
              title="Open in phpMyAdmin"
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-amber-400 transition-colors"
            >
              <ExternalLink size={14} />
            </button>
          )}
          <button
            onClick={() => handleDelete(d.id, d.db_name)}
            title="Delete database"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
          >
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  const roleOptions = activeDB?.type === "mysql" ? MYSQL_ROLES : MONGO_ROLES;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Databases</h1>
          <p className="text-panel-muted text-sm mt-1">Manage MongoDB and MySQL databases, users and access</p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchDatabases}
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
            Create Database
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search databases..."
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
              {[1, 2, 3, 4, 5].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={filtered} />
        ) : (
          <div className="text-center py-16 px-4">
            <Database size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No databases found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No databases match your search. Try a different search term."
                : "Create your first database to store application data."}
            </p>
            {!search && (
              <Button
                onClick={() => setShowCreate(true)}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Create Database
              </Button>
            )}
          </div>
        )}
      </Card>

      {/* Create database modal */}
      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Create Database">
        <form onSubmit={handleCreate} className="space-y-4">
          {/* Domain first — selecting it determines the <user>_ prefix that
              gets applied to the DB name and username below, so putting it
              at the top lets the operator see the final computed names as
              they fill the rest of the form. */}
          <div>
            <label className={labelClass}>Domain *</label>
            {availableDomains.length === 0 ? (
              <>
                <select className={selectClass} disabled>
                  <option>No domains yet</option>
                </select>
                <p className="text-xs text-amber-400/80 mt-1">
                  Add a domain under <b>WHM → Domains</b> first. The database's <code>user_</code> prefix is derived from the domain owner.
                </p>
              </>
            ) : (
              <>
                <select
                  required
                  value={form.domain}
                  onChange={(e) => setForm({ ...form, domain: e.target.value })}
                  className={selectClass}
                >
                  <option value="">— pick a domain —</option>
                  {availableDomains.map((d) => (
                    <option key={d.id} value={d.domain}>
                      {d.domain} — owner: {d.user}
                    </option>
                  ))}
                </select>
                {selectedDomainOwner && (
                  <p className="text-xs text-panel-muted mt-1">
                    Prefix <code className="px-1 py-0.5 rounded bg-panel-bg border border-panel-border text-panel-text">{dbPrefix}</code> will be applied to the database and username below.
                  </p>
                )}
              </>
            )}
          </div>

          <div>
            <label className={labelClass}>Database Name *</label>
            <div className="flex gap-0 items-stretch">
              {dbPrefix && (
                <span className="inline-flex items-center px-3 py-2 border border-r-0 border-panel-border rounded-l-lg bg-panel-surface text-panel-muted text-sm font-mono select-none">
                  {dbPrefix}
                </span>
              )}
              <input
                type="text"
                required
                placeholder="shop"
                value={form.db_name}
                onChange={(e) => setForm({ ...form, db_name: e.target.value.replace(/[^a-zA-Z0-9_]/g, "") })}
                className={dbPrefix ? inputClass + " rounded-l-none" : inputClass}
              />
            </div>
            {dbNameFull && (
              <p className="text-xs text-panel-muted mt-1">Will create <code className="text-panel-text">{dbNameFull}</code></p>
            )}
          </div>

          <div>
            <label className={labelClass}>Type *</label>
            <select value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })} className={selectClass}>
              <option value="mongodb">MongoDB</option>
              <option value="mysql">MySQL</option>
            </select>
          </div>

          <div>
            <label className={labelClass}>Username *</label>
            <div className="flex gap-0 items-stretch">
              {dbPrefix && (
                <span className="inline-flex items-center px-3 py-2 border border-r-0 border-panel-border rounded-l-lg bg-panel-surface text-panel-muted text-sm font-mono select-none">
                  {dbPrefix}
                </span>
              )}
              <input
                type="text"
                required
                placeholder="app"
                value={form.username}
                onChange={(e) => setForm({ ...form, username: e.target.value.replace(/[^a-zA-Z0-9_]/g, "") })}
                className={dbPrefix ? inputClass + " rounded-l-none" : inputClass}
              />
            </div>
            {dbUserFull && (
              <p className="text-xs text-panel-muted mt-1">Will create DB user <code className="text-panel-text">{dbUserFull}</code></p>
            )}
          </div>

          <div>
            <label className={labelClass}>Password *</label>
            <div className="flex gap-2">
              <input
                type={showSecret ? "text" : "password"}
                required
                minLength={8}
                placeholder="Minimum 8 characters"
                value={form.password}
                onChange={(e) => setForm({ ...form, password: e.target.value })}
                className={inputClass + " flex-1"}
              />
              <button
                type="button"
                onClick={() => setShowSecret((s) => !s)}
                className="px-3 py-2 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text"
                title={showSecret ? "Hide password" : "Show password"}
              >
                {showSecret ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
              <button
                type="button"
                onClick={generatePassword}
                className="px-3 py-2 text-xs border border-panel-border rounded-lg text-panel-muted hover:text-panel-text"
                title="Generate a strong password"
              >
                <KeyRound size={14} />
              </button>
            </div>
          </div>

          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={creating || !form.domain}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50"
            >
              {creating ? "Creating..." : "Create Database"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Connection info modal */}
      <Modal
        isOpen={modal === "connection"}
        onClose={() => {
          setModal(null);
          setActiveDB(null);
        }}
        title={`Connection — ${activeDB?.db_name || ""}`}
      >
        {!connection ? (
          <div className="py-8 text-center text-panel-muted text-sm">Loading…</div>
        ) : (
          <div className="space-y-3">
            <ConnectionField label="Type" value={connection.type === "mongodb" ? "MongoDB" : "MySQL"} copyable={false} />
            <ConnectionField label="Host" value={connection.host} onCopy={copy} />
            <ConnectionField label="Port" value={String(connection.port)} onCopy={copy} />
            <ConnectionField label="Database" value={connection.database} onCopy={copy} />
            <ConnectionField label="Username" value={connection.username} onCopy={copy} />
            <ConnectionField
              label="Password"
              value={connection.password}
              secret
              revealed={showSecret}
              onToggle={() => setShowSecret(!showSecret)}
              onCopy={copy}
            />
            <div>
              <label className={labelClass}>Connection URI</label>
              <div className="flex gap-2">
                <input readOnly value={connection.connection_string} className={`${inputClass} font-mono text-xs`} />
                <button
                  onClick={() => copy(connection.connection_string, "URI copied")}
                  className="px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text"
                >
                  <Copy size={14} />
                </button>
              </div>
            </div>
            {connection.cli_command && (
              <div>
                <label className={labelClass}>CLI Command</label>
                <div className="flex gap-2">
                  <input readOnly value={connection.cli_command} className={`${inputClass} font-mono text-xs`} />
                  <button
                    onClick={() => copy(connection.cli_command, "Command copied")}
                    className="px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text"
                  >
                    <Copy size={14} />
                  </button>
                </div>
              </div>
            )}
          </div>
        )}
      </Modal>

      {/* phpMyAdmin modal */}
      <Modal
        isOpen={modal === "phpmyadmin"}
        onClose={() => {
          setModal(null);
          setActiveDB(null);
        }}
        title="phpMyAdmin Access"
      >
        {!connection ? (
          <div className="py-8 text-center text-panel-muted text-sm">Loading…</div>
        ) : (
          <div className="space-y-4">
            <div className="flex items-start gap-3 p-3 rounded-lg bg-amber-500/10 border border-amber-500/30 text-sm">
              <ShieldCheck size={18} className="text-amber-400 mt-0.5 shrink-0" />
              <div className="text-panel-text">
                Use these credentials on the phpMyAdmin login screen. Only the database owner has full
                access — additional users are limited by their assigned role.
              </div>
            </div>
            <ConnectionField label="Server" value={connection.host} onCopy={copy} />
            <ConnectionField label="Username" value={connection.username} onCopy={copy} />
            <ConnectionField
              label="Password"
              value={connection.password}
              secret
              revealed={showSecret}
              onToggle={() => setShowSecret(!showSecret)}
              onCopy={copy}
            />
            <ConnectionField label="Database" value={connection.database} onCopy={copy} />
            <button
              onClick={() => {
                // Auto-login: build a hidden HTML form that POSTs the
                // panel-known credentials to phpMyAdmin's index.php login
                // handler, opening the result in a new tab. phpMyAdmin
                // recognises pma_username/pma_password under cookie auth
                // and lands the operator straight on the target db's
                // structure page (selected via the `db` field) — no need
                // to copy-paste the credentials.
                const url = connection.connection_string || "/phpmyadmin/";
                const form = document.createElement("form");
                form.method = "POST";
                form.action = url.endsWith("/") ? url + "index.php" : url + "/index.php";
                form.target = "_blank";
                form.style.display = "none";
                const fields: Record<string, string> = {
                  pma_username: connection.username,
                  pma_password: connection.password,
                  server: "1",
                  db: connection.database,
                  target: "db_structure.php",
                };
                for (const [k, v] of Object.entries(fields)) {
                  const inp = document.createElement("input");
                  inp.type = "hidden";
                  inp.name = k;
                  inp.value = v;
                  form.appendChild(inp);
                }
                document.body.appendChild(form);
                form.submit();
                setTimeout(() => document.body.removeChild(form), 1000);
              }}
              className="flex items-center justify-center gap-2 w-full px-4 py-2.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
            >
              <ExternalLink size={14} />
              Open in phpMyAdmin (auto-login)
            </button>
            <p className="text-[11px] text-panel-muted text-center">
              Opens in a new tab and logs you in directly to <code className="text-panel-text">{connection.database}</code>. Credentials shown above are for manual login if needed.
            </p>
          </div>
        )}
      </Modal>

      {/* Users modal */}
      <Modal
        isOpen={modal === "users"}
        onClose={() => {
          setModal(null);
          setActiveDB(null);
        }}
        title={`Users — ${activeDB?.db_name || ""}`}
      >
        <div className="space-y-3">
          {/* Database owner row */}
          {activeDB && (
            <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3">
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2 min-w-0">
                  <ShieldCheck size={16} className="text-amber-400 shrink-0" />
                  <div className="min-w-0">
                    <div className="text-sm font-medium text-panel-text truncate">{activeDB.username}</div>
                    <div className="text-xs text-panel-muted">Database owner</div>
                  </div>
                </div>
                <button
                  onClick={() => setPwOpen({ ...pwOpen, owner: !pwOpen.owner })}
                  className="flex items-center gap-1 px-2.5 py-1.5 text-xs bg-panel-surface border border-panel-border rounded-md text-panel-muted hover:text-panel-text transition-colors"
                >
                  <KeyRound size={12} />
                  Change password
                </button>
              </div>
              {pwOpen.owner && (
                <div className="mt-3 flex gap-2">
                  <input
                    type="password"
                    placeholder="New password (min 8 chars)"
                    value={pwForms.owner || ""}
                    onChange={(e) => setPwForms({ ...pwForms, owner: e.target.value })}
                    className={`${inputClass} text-xs`}
                  />
                  <button
                    onClick={() => handleChangeUserPassword("owner")}
                    className="px-3 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-md text-xs font-medium"
                  >
                    Save
                  </button>
                </div>
              )}
            </div>
          )}

          {/* Additional users */}
          {users
            .filter((u) => u.username !== activeDB?.username)
            .map((u) => (
              <div key={u.id} className="rounded-lg border border-panel-border bg-panel-bg p-3">
                <div className="flex items-center justify-between gap-2">
                  <div className="flex items-center gap-2 min-w-0">
                    <Users size={16} className="text-panel-muted shrink-0" />
                    <div className="min-w-0">
                      <div className="text-sm font-medium text-panel-text truncate">{u.username}</div>
                      <div className="text-xs text-panel-muted">Added {new Date(u.created_at).toLocaleDateString()}</div>
                    </div>
                  </div>
                  <div className="flex items-center gap-1.5">
                    <select
                      value={u.role}
                      onChange={(e) => handleChangeUserRole(u.id, e.target.value)}
                      className={`text-xs px-2 py-1 rounded border ${roleColor(u.role)} bg-transparent focus:outline-none cursor-pointer`}
                    >
                      {roleOptions.map((r) => (
                        <option key={r.value} value={r.value} className="bg-panel-bg text-panel-text">
                          {r.label}
                        </option>
                      ))}
                    </select>
                    <button
                      onClick={() => setPwOpen({ ...pwOpen, [u.id]: !pwOpen[u.id] })}
                      title="Change password"
                      className="p-1.5 text-panel-muted hover:text-blue-400 transition-colors"
                    >
                      <KeyRound size={14} />
                    </button>
                    <button
                      onClick={() => handleDeleteUser(u.id, u.username)}
                      title="Remove user"
                      className="p-1.5 text-panel-muted hover:text-red-400 transition-colors"
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                </div>
                {pwOpen[u.id] && (
                  <div className="mt-3 flex gap-2">
                    <input
                      type="password"
                      placeholder="New password (min 8 chars)"
                      value={pwForms[u.id] || ""}
                      onChange={(e) => setPwForms({ ...pwForms, [u.id]: e.target.value })}
                      className={`${inputClass} text-xs`}
                    />
                    <button
                      onClick={() => handleChangeUserPassword(u.id)}
                      className="px-3 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-md text-xs font-medium"
                    >
                      Save
                    </button>
                  </div>
                )}
              </div>
            ))}

          {users.filter((u) => u.username !== activeDB?.username).length === 0 && !showAddUser && (
            <div className="text-center py-4 text-xs text-panel-muted">No additional users</div>
          )}

          {/* Add user form */}
          {showAddUser ? (
            <form onSubmit={handleAddUser} className="rounded-lg border border-blue-500/30 bg-blue-500/5 p-3 space-y-3">
              <div className="grid grid-cols-2 gap-2">
                <input
                  type="text"
                  placeholder="Username"
                  value={newUser.username}
                  onChange={(e) => setNewUser({ ...newUser, username: e.target.value.replace(/[^a-zA-Z0-9_]/g, "") })}
                  className={`${inputClass} text-xs`}
                  required
                />
                <input
                  type="password"
                  placeholder="Password (8+)"
                  value={newUser.password}
                  onChange={(e) => setNewUser({ ...newUser, password: e.target.value })}
                  className={`${inputClass} text-xs`}
                  required
                  minLength={8}
                />
              </div>
              <select
                value={newUser.role}
                onChange={(e) => setNewUser({ ...newUser, role: e.target.value })}
                className={`${selectClass} text-xs`}
              >
                {roleOptions.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.label}
                  </option>
                ))}
              </select>
              <div className="flex gap-2 justify-end">
                <button
                  type="button"
                  onClick={() => setShowAddUser(false)}
                  className="px-3 py-1.5 text-xs text-panel-muted border border-panel-border rounded-md"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={savingUser}
                  className="px-3 py-1.5 text-xs bg-blue-600 hover:bg-blue-700 text-white rounded-md font-medium disabled:opacity-50"
                >
                  {savingUser ? "Adding…" : "Add User"}
                </button>
              </div>
            </form>
          ) : (
            <button
              onClick={() => setShowAddUser(true)}
              className="w-full flex items-center justify-center gap-2 px-3 py-2 border border-dashed border-panel-border rounded-lg text-sm text-panel-muted hover:text-panel-text hover:border-blue-500/50 transition-colors"
            >
              <Plus size={14} />
              Add user
            </button>
          )}
        </div>
      </Modal>

      {/* Remote Database Access modal — mirrors cPanel "Remote Database Access".
          Add IPs / CIDRs / hostnames / % wildcards; each one creates a MySQL
          host-scoped user and (for concrete IPs) opens the firewall. */}
      <Modal
        isOpen={modal === "access-hosts"}
        onClose={() => setModal(null)}
        title={`Remote Database Access — ${activeDB?.db_name || ""}`}
        size="lg"
      >
        {activeDB && (
          <div className="space-y-4">
            <div className="rounded-lg border border-blue-500/20 bg-blue-500/5 p-3 text-[11px] flex items-start gap-2">
              <AlertCircle size={13} className="text-blue-400 mt-0.5 shrink-0" />
              <div className="text-panel-muted space-y-1">
                <div>
                  <b className="text-panel-text">What this does:</b> authorises a remote host to connect to{" "}
                  <code className="px-1 py-0.5 rounded bg-panel-bg border border-panel-border text-panel-text">{activeDB.db_name}</code>.
                  {activeDB.type === "mysql"
                    ? " For MySQL, a host-scoped grant is created for the owner username; concrete IPs / CIDRs also open the firewall on :3306."
                    : " For MongoDB, the firewall is opened on :27017. MongoDB auth still requires valid credentials."}
                </div>
                <div>
                  Valid host formats: <code>192.168.1.50</code>, <code>10.0.0.0/24</code>, <code>192.168.1.%</code> (MySQL wildcard), <code>db.example.com</code>, or <code>%</code> (any — firewall opens to 0.0.0.0/0).
                </div>
              </div>
            </div>

            {/* Add form */}
            <div className="border border-panel-border rounded-lg p-3 space-y-3">
              <div className="text-sm font-medium text-panel-text">Add Access Host</div>
              <div className="grid grid-cols-5 gap-2">
                <input
                  className={inputClass + " col-span-2"}
                  placeholder="Host (IP / CIDR / % / hostname)"
                  value={accessHostForm.host}
                  onChange={(e) => setAccessHostForm((f) => ({ ...f, host: e.target.value }))}
                  onKeyDown={(e) => { if (e.key === "Enter") handleAddAccessHost(); }}
                />
                <input
                  className={inputClass + " col-span-2"}
                  placeholder="Comment (optional)"
                  value={accessHostForm.comment}
                  onChange={(e) => setAccessHostForm((f) => ({ ...f, comment: e.target.value }))}
                />
                <button
                  onClick={handleAddAccessHost}
                  disabled={addingHost || !accessHostForm.host.trim()}
                  className="px-3 py-2 text-xs bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50"
                >
                  {addingHost ? "Adding…" : "Add Host"}
                </button>
              </div>
            </div>

            {/* List */}
            <div>
              <div className="text-sm font-medium text-panel-text mb-2">
                Authorised Hosts ({accessHosts.length})
              </div>
              {accessHosts.length === 0 ? (
                <div className="text-center text-sm text-panel-muted py-6 border border-dashed border-panel-border rounded-lg">
                  No remote hosts configured. Local connections (from this server) work without an entry here.
                </div>
              ) : (
                <div className="border border-panel-border rounded-lg divide-y divide-panel-border">
                  {accessHosts.map((h) => (
                    <div key={h.id} className="flex items-center justify-between px-3 py-2 text-sm">
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2">
                          <Globe size={12} className="text-green-400 shrink-0" />
                          <code className="px-1.5 py-0.5 rounded bg-panel-bg border border-panel-border text-panel-text">{h.host}</code>
                          {h.comment && (
                            <span className="text-xs text-panel-muted truncate">— {h.comment}</span>
                          )}
                        </div>
                        <div className="text-[11px] text-panel-muted/70 mt-0.5">
                          Added {new Date(h.created_at).toLocaleDateString()}
                        </div>
                      </div>
                      <button
                        onClick={() => handleRemoveAccessHost(h.id, h.host)}
                        title="Remove host"
                        className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}

function ConnectionField({
  label,
  value,
  secret,
  revealed,
  onToggle,
  onCopy,
  copyable = true,
}: {
  label: string;
  value: string;
  secret?: boolean;
  revealed?: boolean;
  onToggle?: () => void;
  onCopy?: (text: string, label?: string) => void;
  copyable?: boolean;
}) {
  const display = secret && !revealed ? "•".repeat(Math.min(value.length, 16) || 8) : value;
  return (
    <div>
      <label className="block text-xs uppercase tracking-wide text-panel-muted mb-1 font-semibold">{label}</label>
      <div className="flex gap-2">
        <input
          readOnly
          value={display}
          className="flex-1 px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text font-mono text-xs"
        />
        {secret && onToggle && (
          <button
            onClick={onToggle}
            className="px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text"
          >
            {revealed ? <EyeOff size={14} /> : <Eye size={14} />}
          </button>
        )}
        {copyable && onCopy && (
          <button
            onClick={() => onCopy(value, `${label} copied`)}
            className="px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text"
          >
            <Copy size={14} />
          </button>
        )}
      </div>
    </div>
  );
}
