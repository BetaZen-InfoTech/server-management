import { useEffect, useState, type ReactNode } from "react";
import { Button, Card, Modal, PasswordInput, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Database,
  Save,
  RefreshCw,
  ShieldCheck,
  ShieldAlert,
  RotateCcw,
  Power,
  Plus,
  Trash2,
  AlertTriangle,
  Server,
  Copy,
  KeyRound,
  Link2,
} from "lucide-react";

// Matches backend models.MongoDBConfig (json tags).
interface MongoConfig {
  storage_engine: string;
  cache_size_gb: number;
  max_connections: number;
  journal_enabled: boolean;
  slow_query_threshold_ms: number;
  profiling_level: number;
  bind_ip: string;
  auth_enabled: boolean;
  remote_access_ips: string[];
}
interface MongoStatus {
  version: string;
  uptime: number;
  connections_current: number;
  connections_available: number;
}
interface MongoDBInfo {
  name: string;
  size_on_disk: number;
  empty: boolean;
}
interface MongoUserInfo {
  user: string;
  db: string;
  roles: string[];
}

const inp =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500/40";
const lbl = "block text-sm font-medium text-panel-text mb-1";

const ADMIN_ROLES = [
  "root",
  "userAdminAnyDatabase",
  "readWriteAnyDatabase",
  "dbAdminAnyDatabase",
  "readAnyDatabase",
  "clusterAdmin",
];

function fmtBytes(n: number): string {
  if (!n || n <= 0) return "—";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${u[i]}`;
}

export default function EditMongoDBConfigPage() {
  const [cfg, setCfg] = useState<MongoConfig | null>(null);
  const [status, setStatus] = useState<MongoStatus | null>(null);
  const [dbs, setDbs] = useState<MongoDBInfo[]>([]);
  const [users, setUsers] = useState<MongoUserInfo[]>([]);
  const [raw, setRaw] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [savingRaw, setSavingRaw] = useState(false);
  const [busy, setBusy] = useState(false);

  const [showCreate, setShowCreate] = useState(false);
  const [newUser, setNewUser] = useState<{ username: string; password: string; roles: string[] }>({
    username: "",
    password: "",
    roles: ["root"],
  });
  const [customRole, setCustomRole] = useState("");
  const [creating, setCreating] = useState(false);
  const [newIP, setNewIP] = useState("");
  const [uriModal, setUriModal] = useState<{ open: boolean; title: string; uri: string; hasPassword: boolean; note?: string }>(
    { open: false, title: "", uri: "", hasPassword: true }
  );
  const [showRotate, setShowRotate] = useState(false);
  const [rotate, setRotate] = useState({ new_username: "", new_password: "" });
  const [rotating, setRotating] = useState(false);

  const copyUri = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast.success("Connection URI copied");
    } catch {
      toast.error("Copy failed — select and copy manually");
    }
  };
  const toggleRole = (r: string) => {
    setNewUser((u) => ({ ...u, roles: u.roles.includes(r) ? u.roles.filter((x) => x !== r) : [...u.roles, r] }));
  };
  const addCustomRole = () => {
    const v = customRole.trim();
    if (!v) return;
    setNewUser((u) => (u.roles.includes(v) ? u : { ...u, roles: [...u.roles, v] }));
    setCustomRole("");
  };
  const getUri = async (username: string) => {
    try {
      const r = await api.get(`/config/mongodb/users/${encodeURIComponent(username)}/uri`);
      const d = r.data?.data || {};
      setUriModal({
        open: true,
        title: `Connection URI — ${username}`,
        uri: d.uri || "",
        hasPassword: !!d.has_password,
        note: d.has_password ? undefined : "This admin's password isn't stored — replace <password> with the real one.",
      });
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to get connection URI");
    }
  };
  const doRotate = async () => {
    if (!rotate.new_username || rotate.new_password.length < 8) {
      toast.error("Username required and password must be at least 8 characters");
      return;
    }
    const ok = await confirmAction({
      title: "Rotate the main MongoDB admin?",
      description:
        "Creates a new admin with the panel's current roles, repoints /opt/serverpanel/.env at it, deletes the old account, and RESTARTS the panel to reconnect. The panel will briefly go offline. Make sure you save the new connection URI.",
      danger: true,
      confirmLabel: "Rotate main admin",
    });
    if (!ok) return;
    setRotating(true);
    try {
      const r = await api.post("/config/mongodb/rotate-main", rotate);
      const d = r.data?.data || {};
      setShowRotate(false);
      setRotate({ new_username: "", new_password: "" });
      setUriModal({
        open: true,
        title: "Main admin rotated — new connection URI",
        uri: d.uri || "",
        hasPassword: true,
        note: "Save this now. The panel is restarting to apply the new credentials; re-login in a few seconds.",
      });
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Rotation failed (rolled back)");
    } finally {
      setRotating(false);
    }
  };

  const addIP = () => {
    if (!cfg) return;
    const v = newIP.trim();
    if (!v) return;
    const list = cfg.remote_access_ips || [];
    if (list.includes(v)) {
      toast.error("That IP is already in the list");
      return;
    }
    setCfg({ ...cfg, remote_access_ips: [...list, v] });
    setNewIP("");
  };
  const removeIP = (ip: string) => {
    if (!cfg) return;
    setCfg({ ...cfg, remote_access_ips: (cfg.remote_access_ips || []).filter((x) => x !== ip) });
  };

  const load = async () => {
    setLoading(true);
    try {
      const [c, s, d, u, r] = await Promise.allSettled([
        api.get("/config/mongodb"),
        api.get("/config/mongodb/status"),
        api.get("/config/mongodb/databases"),
        api.get("/config/mongodb/users"),
        api.get("/config/mongodb/raw"),
      ]);
      if (c.status === "fulfilled") setCfg(c.value.data?.data || null);
      if (s.status === "fulfilled") setStatus(s.value.data?.data || null);
      if (d.status === "fulfilled") setDbs(d.value.data?.data || []);
      if (u.status === "fulfilled") setUsers(u.value.data?.data || []);
      if (r.status === "fulfilled") setRaw(r.value.data?.data?.content || "");
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to load MongoDB admin data");
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    void load();
  }, []);

  // PUT the templated config. When auth is being turned OFF or remote access
  // turned ON, warn first — both are sensitive on the mongod the panel itself
  // uses. The backend wraps the write in applyMongodChangeSafely (auto-rollback).
  const saveConfig = async () => {
    if (!cfg) return;
    if (!cfg.auth_enabled) {
      const ok = await confirmAction({
        title: "Disable MongoDB authentication?",
        description:
          "The panel stores its own data in this MongoDB. Disabling authentication makes every database reachable without credentials to anyone who can reach the port. Only do this temporarily for recovery. mongod will be restarted; if it can't come back the change auto-rolls-back.",
        danger: true,
        confirmLabel: "Disable auth",
      });
      if (!ok) return;
    }
    setSaving(true);
    try {
      await api.put("/config/mongodb", cfg);
      toast.success("MongoDB config saved — mongod restarted");
      await load();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to save MongoDB config");
    } finally {
      setSaving(false);
    }
  };

  const saveRaw = async () => {
    const ok = await confirmAction({
      title: "Overwrite /etc/mongod.conf?",
      description:
        "This replaces the raw mongod configuration and restarts mongod. A bad value can stop MongoDB — but the panel verifies mongod comes back and AUTO-ROLLS-BACK to the previous file if it doesn't.",
      danger: true,
      confirmLabel: "Write & restart",
    });
    if (!ok) return;
    setSavingRaw(true);
    try {
      await api.put("/config/mongodb/raw", { content: raw });
      toast.success("mongod.conf written — mongod restarted");
      await load();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to write mongod.conf (rolled back)");
    } finally {
      setSavingRaw(false);
    }
  };

  const restartMongod = async () => {
    const ok = await confirmAction({
      title: "Restart mongod?",
      description: "Briefly interrupts every MongoDB connection, including the panel's own.",
      confirmLabel: "Restart",
    });
    if (!ok) return;
    setBusy(true);
    try {
      await api.post("/config/mongod/restart");
      toast.success("mongod restarted");
      await load();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Restart failed");
    } finally {
      setBusy(false);
    }
  };

  const reboot = async (kind: "graceful" | "forceful") => {
    const ok = await confirmAction({
      title: `${kind === "graceful" ? "Graceful" : "Forceful"} server reboot?`,
      description:
        kind === "graceful"
          ? "Schedules a full server reboot in ~1 minute. Every service goes down."
          : "Reboots the ENTIRE server immediately with no graceful shutdown. Use only if the box is unresponsive.",
      danger: true,
      confirmLabel: kind === "graceful" ? "Schedule reboot" : "Force reboot now",
    });
    if (!ok) return;
    setBusy(true);
    try {
      await api.post(`/config/reboot/${kind}`);
      toast.success(kind === "graceful" ? "Reboot scheduled in ~1 minute" : "Forceful reboot issued");
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Reboot failed");
    } finally {
      setBusy(false);
    }
  };

  const createUser = async () => {
    if (!newUser.username || newUser.password.length < 8) {
      toast.error("Username required and password must be at least 8 characters");
      return;
    }
    if (newUser.roles.length === 0) {
      toast.error("Select at least one role");
      return;
    }
    setCreating(true);
    try {
      const r = await api.post("/config/mongodb/users", {
        username: newUser.username,
        password: newUser.password,
        roles: newUser.roles,
      });
      const d = r.data?.data || {};
      setShowCreate(false);
      const created = newUser.username;
      setNewUser({ username: "", password: "", roles: ["root"] });
      await load();
      setUriModal({
        open: true,
        title: `Admin "${created}" created — connection URI`,
        uri: d.uri || "",
        hasPassword: true,
        note: "Save this connection URI now.",
      });
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to create admin user");
    } finally {
      setCreating(false);
    }
  };

  const deleteUser = async (username: string) => {
    const ok = await confirmAction({
      title: `Delete MongoDB admin user "${username}"?`,
      description: "Removes this super-admin account from the admin database. This cannot be undone.",
      danger: true,
      confirmLabel: "Delete user",
    });
    if (!ok) return;
    try {
      await api.delete(`/config/mongodb/users/${encodeURIComponent(username)}`);
      toast.success("Admin user deleted");
      await load();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to delete admin user");
    }
  };

  const remoteEnabled = !!cfg && cfg.bind_ip.trim() !== "" && cfg.bind_ip !== "127.0.0.1" && cfg.bind_ip !== "localhost";

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
            <Database size={20} /> MongoDB Administration
          </h1>
          <p className="text-panel-muted text-sm mt-1">
            Server-level MongoDB controls: authentication, remote access, raw{" "}
            <code className="text-panel-text">/etc/mongod.conf</code>, all databases, super-admin users,
            and service restart. Every config change restarts mongod with an automatic rollback if it
            doesn't come back.
          </p>
        </div>
        <Button variant="ghost" onClick={load} disabled={loading || saving}>
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
        </Button>
      </div>

      {/* Warning banner */}
      <div className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-300">
        <AlertTriangle size={16} className="mt-0.5 shrink-0" />
        <span>
          The panel keeps its <strong>own data</strong> in this MongoDB instance. Disabling auth, editing
          the raw config, or restarting affects the panel too. Destructive actions are confirmed and
          config writes auto-roll-back if mongod fails to restart.
        </span>
      </div>

      {/* Service / status */}
      <Card>
        <div className="p-5">
          <h2 className="text-xs uppercase tracking-wider text-panel-muted mb-3 flex items-center gap-2">
            <Server size={14} /> Service
          </h2>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
            <Stat label="Version" value={status?.version || "—"} />
            <Stat label="Uptime" value={status ? `${Math.floor(status.uptime / 3600)}h` : "—"} />
            <Stat label="Connections" value={status ? String(status.connections_current) : "—"} />
            <Stat label="Available" value={status ? String(status.connections_available) : "—"} />
          </div>
          <div className="flex flex-wrap gap-2">
            <Button variant="secondary" onClick={restartMongod} disabled={busy}>
              <RefreshCw size={14} /> Restart mongod
            </Button>
            <Button variant="ghost" onClick={() => reboot("graceful")} disabled={busy}>
              <RotateCcw size={14} /> Graceful reboot
            </Button>
            <Button variant="ghost" onClick={() => reboot("forceful")} disabled={busy}>
              <Power size={14} /> Forceful reboot
            </Button>
          </div>
        </div>
      </Card>

      {loading && !cfg ? (
        <Card>
          <div className="p-6 text-sm text-panel-muted">Loading…</div>
        </Card>
      ) : cfg ? (
        <>
          {/* Authentication & Remote Access + basic config */}
          <Card>
            <div className="p-5 space-y-5">
              <div className="flex items-center justify-between">
                <h2 className="text-xs uppercase tracking-wider text-panel-muted flex items-center gap-2">
                  {cfg.auth_enabled ? <ShieldCheck size={14} /> : <ShieldAlert size={14} className="text-amber-400" />}
                  Authentication &amp; Remote Access
                </h2>
                <Button onClick={saveConfig} loading={saving}>
                  <Save size={14} /> Save &amp; restart
                </Button>
              </div>

              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <label className="flex items-center gap-2 text-sm text-panel-text cursor-pointer">
                  <input
                    type="checkbox"
                    checked={cfg.auth_enabled}
                    onChange={(e) => setCfg({ ...cfg, auth_enabled: e.target.checked })}
                  />
                  Require authentication ({cfg.auth_enabled ? "enabled" : "DISABLED"})
                </label>
                <label className="flex items-center gap-2 text-sm text-panel-text cursor-pointer">
                  <input
                    type="checkbox"
                    checked={remoteEnabled}
                    onChange={(e) => setCfg({ ...cfg, bind_ip: e.target.checked ? "0.0.0.0" : "127.0.0.1" })}
                  />
                  Allow remote connections (bind 0.0.0.0)
                </label>
              </div>

              <div>
                <label className={lbl}>
                  Bind IP <span className="text-xs text-panel-muted font-mono">(net.bindIp)</span>
                </label>
                <input
                  className={inp}
                  value={cfg.bind_ip}
                  placeholder="127.0.0.1"
                  onChange={(e) => setCfg({ ...cfg, bind_ip: e.target.value })}
                />
                <p className="text-[11px] text-panel-muted mt-1">
                  Comma-separated. <code className="text-panel-text">127.0.0.1</code> = local only.{" "}
                  <code className="text-panel-text">0.0.0.0</code> = listen on all interfaces (then add
                  per-database remote IPs on the Databases page, which open the firewall on :27017). Keep
                  authentication ON when exposing MongoDB remotely.
                </p>
              </div>

              {/* Server-level remote-access allowlist (firewall :27017) */}
              <div>
                <label className={lbl}>
                  Remote Access Allowlist <span className="text-xs text-panel-muted font-mono">(firewall :27017)</span>
                </label>
                <div className="flex gap-2">
                  <input
                    className={inp}
                    value={newIP}
                    placeholder="e.g. 203.0.113.7  or  10.0.0.0/24"
                    onChange={(e) => setNewIP(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        e.preventDefault();
                        addIP();
                      }
                    }}
                  />
                  <Button variant="secondary" type="button" onClick={addIP}>
                    <Plus size={14} /> Add IP
                  </Button>
                </div>
                {(cfg.remote_access_ips?.length ?? 0) > 0 ? (
                  <div className="flex flex-wrap gap-2 mt-2">
                    {cfg.remote_access_ips.map((ip) => (
                      <span
                        key={ip}
                        className="inline-flex items-center gap-1.5 rounded-md border border-panel-border bg-panel-bg px-2 py-1 text-xs font-mono text-panel-text"
                      >
                        {ip}
                        <button
                          type="button"
                          onClick={() => removeIP(ip)}
                          title="Remove IP"
                          className="text-panel-muted hover:text-red-400 transition-colors"
                        >
                          <Trash2 size={12} />
                        </button>
                      </span>
                    ))}
                  </div>
                ) : (
                  <p className="text-[11px] text-panel-muted mt-1">
                    No remote IPs allowed yet — MongoDB is reachable only from the host and any IPs already
                    permitted on the Databases page.
                  </p>
                )}
                <p className="text-[11px] text-panel-muted mt-1">
                  Multiple client IPs / CIDR blocks allowed to connect on{" "}
                  <code className="text-panel-text">:27017</code>. Saving opens the firewall for each (and
                  revokes removed ones). Only effective when remote connections are enabled above; keep
                  authentication ON.
                </p>
              </div>

              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <Field label="Storage Engine" hint="storage.engine">
                  <input className={inp} value={cfg.storage_engine} onChange={(e) => setCfg({ ...cfg, storage_engine: e.target.value })} />
                </Field>
                <Field label="Cache Size (GB)" hint="wiredTiger.cacheSizeGB">
                  <input type="number" step="0.1" className={inp} value={cfg.cache_size_gb} onChange={(e) => setCfg({ ...cfg, cache_size_gb: Number(e.target.value) })} />
                </Field>
                <Field label="Max Connections" hint="net.maxIncomingConnections">
                  <input type="number" className={inp} value={cfg.max_connections} onChange={(e) => setCfg({ ...cfg, max_connections: Number(e.target.value) })} />
                </Field>
                <Field label="Slow Query Threshold (ms)" hint="operationProfiling.slowOpThresholdMs">
                  <input type="number" className={inp} value={cfg.slow_query_threshold_ms} onChange={(e) => setCfg({ ...cfg, slow_query_threshold_ms: Number(e.target.value) })} />
                </Field>
                <Field label="Profiling Level" hint="0 off · 1 slow · 2 all">
                  <select className={inp} value={cfg.profiling_level} onChange={(e) => setCfg({ ...cfg, profiling_level: Number(e.target.value) })}>
                    <option value={0}>0 (off)</option>
                    <option value={1}>1 (slow ops)</option>
                    <option value={2}>2 (all ops)</option>
                  </select>
                </Field>
                <Field label="Journal" hint="storage.journal.enabled">
                  <label className="flex items-center gap-2 text-sm text-panel-text cursor-pointer h-[38px]">
                    <input type="checkbox" checked={cfg.journal_enabled} onChange={(e) => setCfg({ ...cfg, journal_enabled: e.target.checked })} />
                    {cfg.journal_enabled ? "Enabled" : "Disabled"}
                  </label>
                </Field>
              </div>
            </div>
          </Card>

          {/* Admin users */}
          <Card>
            <div className="p-5">
              <div className="flex items-center justify-between mb-3">
                <h2 className="text-xs uppercase tracking-wider text-panel-muted">
                  Super-admin Users <span className="text-panel-muted/70">(admin database)</span>
                </h2>
                <div className="flex gap-2">
                  <Button variant="secondary" onClick={() => setShowRotate(true)}>
                    <RotateCcw size={14} /> Rotate main admin
                  </Button>
                  <Button onClick={() => setShowCreate(true)}>
                    <Plus size={14} /> Add admin user
                  </Button>
                </div>
              </div>
              {users.length === 0 ? (
                <p className="text-sm text-panel-muted">No admin users found (or MongoDB unreachable).</p>
              ) : (
                <div className="divide-y divide-panel-border">
                  {users.map((u) => (
                    <div key={u.user} className="flex items-center justify-between py-2">
                      <div>
                        <span className="text-sm font-mono text-panel-text">{u.user}</span>
                        <span className="ml-2 text-xs text-panel-muted">{u.roles.join(", ")}</span>
                      </div>
                      <div className="flex items-center gap-1">
                        <button
                          onClick={() => getUri(u.user)}
                          title="Get connection URI"
                          className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
                        >
                          <Link2 size={15} />
                        </button>
                        <button
                          onClick={() => deleteUser(u.user)}
                          title="Delete admin user"
                          className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
                        >
                          <Trash2 size={15} />
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
              )}
              <p className="text-[11px] text-panel-muted mt-2">
                The account the panel authenticates with (and <code className="text-panel-text">admin</code>)
                are protected from deletion by the server.
              </p>
            </div>
          </Card>

          {/* All databases */}
          <Card>
            <div className="p-5">
              <h2 className="text-xs uppercase tracking-wider text-panel-muted mb-3">
                All Databases <span className="text-panel-muted/70">({dbs.length})</span>
              </h2>
              {dbs.length === 0 ? (
                <p className="text-sm text-panel-muted">No databases (or MongoDB unreachable).</p>
              ) : (
                <div className="divide-y divide-panel-border">
                  {dbs.map((d) => (
                    <div key={d.name} className="flex items-center justify-between py-2 text-sm">
                      <span className="font-mono text-panel-text">{d.name}</span>
                      <span className="text-panel-muted">{d.empty ? "empty" : fmtBytes(d.size_on_disk)}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </Card>

          {/* Raw config */}
          <Card>
            <div className="p-5">
              <div className="flex items-center justify-between mb-3">
                <h2 className="text-xs uppercase tracking-wider text-panel-muted">
                  Raw <code className="text-panel-text">/etc/mongod.conf</code>
                </h2>
                <Button variant="secondary" onClick={saveRaw} loading={savingRaw}>
                  <Save size={14} /> Write &amp; restart
                </Button>
              </div>
              <textarea
                className={inp + " min-h-[280px] leading-relaxed"}
                spellCheck={false}
                value={raw}
                onChange={(e) => setRaw(e.target.value)}
              />
              <p className="text-[11px] text-panel-muted mt-1">
                Advanced. Saving overwrites the file and restarts mongod; if mongod fails to come back the
                previous file is restored automatically.
              </p>
            </div>
          </Card>
        </>
      ) : (
        <Card>
          <div className="p-6 text-sm text-panel-muted">
            Could not load MongoDB config. Is mongod running on this server?
          </div>
        </Card>
      )}

      {/* Create admin user modal */}
      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Add MongoDB Super-admin">
        <div className="space-y-4">
          <div>
            <label className={lbl}>Username</label>
            <input
              className={inp}
              value={newUser.username}
              placeholder="superadmin"
              onChange={(e) => setNewUser({ ...newUser, username: e.target.value.replace(/[^a-zA-Z0-9_]/g, "") })}
            />
          </div>
          <div>
            <label className={lbl}>Password</label>
            <PasswordInput
              value={newUser.password}
              onChange={(v: string) => setNewUser({ ...newUser, password: v })}
              placeholder="Minimum 8 characters"
            />
          </div>
          <div>
            <label className={lbl}>Roles <span className="text-xs text-panel-muted">(pick one or more)</span></label>
            <div className="flex flex-wrap gap-1.5">
              {ADMIN_ROLES.map((r) => {
                const on = newUser.roles.includes(r);
                return (
                  <button
                    key={r}
                    type="button"
                    onClick={() => toggleRole(r)}
                    className={`px-2 py-1 rounded-md text-xs font-mono border transition-colors ${
                      on
                        ? "border-blue-500/60 bg-blue-500/15 text-blue-300"
                        : "border-panel-border bg-panel-bg text-panel-muted hover:text-panel-text"
                    }`}
                  >
                    {r}
                  </button>
                );
              })}
            </div>
            <div className="flex gap-2 mt-2">
              <input
                className={inp}
                value={customRole}
                placeholder="custom role, e.g. readWrite@shopdb"
                onChange={(e) => setCustomRole(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    addCustomRole();
                  }
                }}
              />
              <Button variant="secondary" type="button" onClick={addCustomRole}>
                <Plus size={14} /> Add role
              </Button>
            </div>
            {newUser.roles.length > 0 && (
              <div className="flex flex-wrap gap-1.5 mt-2">
                {newUser.roles.map((r) => (
                  <span
                    key={r}
                    className="inline-flex items-center gap-1 rounded-md border border-panel-border bg-panel-bg px-2 py-0.5 text-xs font-mono text-panel-text"
                  >
                    {r}
                    <button type="button" onClick={() => toggleRole(r)} className="text-panel-muted hover:text-red-400">
                      <Trash2 size={11} />
                    </button>
                  </span>
                ))}
              </div>
            )}
            <p className="text-[11px] text-panel-muted mt-1">
              Bare roles apply on the <code className="text-panel-text">admin</code> database; use{" "}
              <code className="text-panel-text">role@db</code> to scope a role to one database (e.g.{" "}
              <code className="text-panel-text">readWrite@shopdb</code>).
            </p>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="ghost" onClick={() => setShowCreate(false)}>
              Cancel
            </Button>
            <Button onClick={createUser} loading={creating}>
              Create user
            </Button>
          </div>
        </div>
      </Modal>

      {/* Connection URI modal (create / reveal / rotate) */}
      <Modal isOpen={uriModal.open} onClose={() => setUriModal({ ...uriModal, open: false })} title={uriModal.title}>
        <div className="space-y-3">
          {uriModal.note && (
            <div className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">
              <AlertTriangle size={14} className="mt-0.5 shrink-0" />
              <span>{uriModal.note}</span>
            </div>
          )}
          <div className="flex gap-2">
            <input
              className={inp + " flex-1"}
              readOnly
              value={uriModal.uri}
              onFocus={(e) => e.currentTarget.select()}
            />
            <Button variant="secondary" type="button" onClick={() => copyUri(uriModal.uri)}>
              <Copy size={14} /> Copy
            </Button>
          </div>
          <p className="text-[11px] text-panel-muted">
            Connects on <code className="text-panel-text">:27017</code> with{" "}
            <code className="text-panel-text">authSource=admin</code>. The host is the server's public IP — ensure
            remote connections + the firewall allowlist permit your client.
          </p>
          <div className="flex justify-end">
            <Button onClick={() => setUriModal({ ...uriModal, open: false })}>Done</Button>
          </div>
        </div>
      </Modal>

      {/* Rotate main admin modal */}
      <Modal isOpen={showRotate} onClose={() => setShowRotate(false)} title="Rotate main MongoDB admin">
        <div className="space-y-4">
          <div className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">
            <KeyRound size={14} className="mt-0.5 shrink-0" />
            <span>
              Creates a new admin carrying the panel's current roles, repoints the panel's <code>.env</code>, deletes
              the old account, and restarts the panel to reconnect. Save the new connection URI it returns.
            </span>
          </div>
          <div>
            <label className={lbl}>New username</label>
            <input
              className={inp}
              value={rotate.new_username}
              placeholder="e.g. bzadmin"
              onChange={(e) => setRotate({ ...rotate, new_username: e.target.value.replace(/[^a-zA-Z0-9_]/g, "") })}
            />
          </div>
          <div>
            <label className={lbl}>New password</label>
            <PasswordInput
              value={rotate.new_password}
              onChange={(v: string) => setRotate({ ...rotate, new_password: v })}
              placeholder="Minimum 8 characters"
            />
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="ghost" onClick={() => setShowRotate(false)}>
              Cancel
            </Button>
            <Button onClick={doRotate} loading={rotating}>
              Rotate
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-panel-border bg-panel-bg px-3 py-2">
      <div className="text-[11px] uppercase tracking-wider text-panel-muted">{label}</div>
      <div className="text-sm font-mono text-panel-text mt-0.5">{value}</div>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div>
      <label className={lbl}>
        {label} {hint && <span className="text-xs text-panel-muted font-mono">({hint})</span>}
      </label>
      {children}
    </div>
  );
}
