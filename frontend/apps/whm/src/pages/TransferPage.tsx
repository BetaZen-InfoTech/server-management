import { useState, useEffect, useCallback } from "react";
import { Card, Button, Table, StatusBadge, Modal, PasswordInput, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import axios from "axios";
import toast from "react-hot-toast";
import {
  ArrowLeftRight, Plus, RefreshCw, Search, Eye, XCircle,
  CheckCircle2, Clock, AlertTriangle, Loader2, Server,
  Globe, Database, Mail, Shield, Key, Terminal, HardDrive, Flame, Boxes,
  KeyRound, Lock, Users, Box, Copy, Check, Lightbulb, MinusCircle
} from "lucide-react";

interface TransferStep {
  name: string;
  status: string;
  started_at?: string;
  completed_at?: string;
  error?: string;
  details?: string;
  progress?: number;
  bytes_done?: number;
  bytes_total?: number;
  throughput_mbps?: number;
  eta_seconds?: number;
  current_item?: string;
}

interface TransferLog {
  timestamp: string;
  level: string;
  message: string;
  component?: string;
}

interface NodeApp {
  name: string;
  cwd: string;
  script: string;
  exec_mode: string;
  instances: number;
  node_version?: string;
  npm_manager?: string;
}

// LinuxUser is a hosting account on the source. The wizard treats this as
// the primary selection unit — selecting a user implicitly opts in their
// domains, mailboxes, FTP accounts, MySQL DBs, cron jobs, node apps, and
// WordPress sites. The backend cascades the per-user selection into the
// per-resource whitelists so the existing executor stays unchanged.
interface LinuxUser {
  username: string;
  uid: number;
  home: string;
  shell: string;
  active: boolean;
  locked: boolean;
  domains: number;
  mailboxes: number;
  databases: number;
  ftp_users: number;
  cron_jobs: number;
  node_apps: number;
  wp_sites: number;
  home_bytes: number;
  // True when the source's panel mongo has a `users` row for this
  // linux account. False for OS-level accounts under /home (cloud-init
  // ubuntu, manually useradd'd shell users, etc.) — those still appear
  // in the list so the operator can opt them in if they really want
  // to, but they're not pre-checked.
  panel_managed: boolean;
}

interface DomainSetting {
  domain: string;
  owner: string;
  document_root: string;
  php_version: string;
  has_ssl: boolean;
  wp_installed: boolean;
}

interface DiscoveredData {
  hostname: string;
  server_type: string;
  domains: string[];
  databases: string[];
  mysql_databases: string[];
  email_domains: string[];
  cron_users: string[];
  ssl_domains: string[];
  dns_zones: string[];
  ftp_users: string[];
  node_apps: NodeApp[];
  linux_users: LinuxUser[];
  domain_settings: DomainSetting[];
  // Present when one or more probes could not complete inside discovery's
  // time budget. The affected lists come back empty, so we tell the
  // operator which ones rather than letting an empty section read as
  // "the source has none of these".
  warnings?: string[];
  partial?: boolean;
}

interface TransferJob {
  id: string;
  type: string;
  direction: string;
  source_server: { hostname: string; ip: string; port: number; protocol: string; auth_method?: string };
  components: Record<string, boolean>;
  domains?: string[];
  status: string;
  progress: number;
  steps: TransferStep[];
  logs: TransferLog[];
  discovered?: DiscoveredData;
  started_at?: string;
  completed_at?: string;
  created_at: string;
}

// discoveryErrorMessage turns the three ways discovery can fail into
// something the operator can act on. Previously every failure produced
// the same "Discovery failed" toast, which was actively misleading for
// the timeout case: the source server was fine and reachable, the probe
// pass simply took longer than the proxy in front of the panel allows,
// and the operator had no way to tell that from bad credentials.
function discoveryErrorMessage(e: any): string {
  const apiMsg = e?.response?.data?.error?.message;
  if (apiMsg) return apiMsg;
  const status = e?.response?.status;
  if (status === 504 || status === 502 || status === 408) {
    return "Discovery timed out at the gateway before the panel answered. The source server is reachable but slow to enumerate — retry, and if it keeps happening check the panel logs for which probe is stalling.";
  }
  if (e?.code === "ECONNABORTED") {
    return "Discovery took longer than 60s and was cancelled. The source server is reachable but slow to enumerate.";
  }
  if (e?.code === "ERR_NETWORK") {
    return "Could not reach the panel API. Check that the panel service is running.";
  }
  return "Discovery failed";
}

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

const stepIcons: Record<string, React.ReactNode> = {
  pending: <Clock size={14} className="text-panel-muted" />,
  in_progress: <Loader2 size={14} className="text-blue-400 animate-spin" />,
  completed: <CheckCircle2 size={14} className="text-green-400" />,
  failed: <AlertTriangle size={14} className="text-red-400" />,
  skipped: <MinusCircle size={14} className="text-panel-muted" />,
};

function formatEta(sec?: number): string {
  if (!sec || sec <= 0) return "";
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ${sec % 60}s`;
  const h = Math.floor(sec / 3600); const m = Math.floor((sec % 3600) / 60);
  return `${h}h ${m}m`;
}

const componentLabels: Record<string, { label: string; icon: React.ReactNode }> = {
  hostname: { label: "Hostname", icon: <Server size={16} /> },
  software: { label: "Software (PHP Versions)", icon: <Terminal size={16} /> },
  dns: { label: "DNS Zones & Records", icon: <Globe size={16} /> },
  ssl: { label: "SSL Certificates", icon: <Shield size={16} /> },
  domains: { label: "Domains", icon: <Globe size={16} /> },
  files: { label: "Website Files", icon: <HardDrive size={16} /> },
  databases: { label: "Databases", icon: <Database size={16} /> },
  email_data: { label: "Email Accounts & Data", icon: <Mail size={16} /> },
  ftp_accounts: { label: "FTP Accounts", icon: <Key size={16} /> },
  cron_jobs: { label: "Cron Jobs", icon: <Terminal size={16} /> },
  firewall: { label: "Firewall Rules", icon: <Flame size={16} /> },
  server_config: { label: "Server Configuration", icon: <Server size={16} /> },
  node_apps: { label: "Node.js Apps (PM2)", icon: <Boxes size={16} /> },
  ssh_keys: { label: "SSH Keys (authorized_keys)", icon: <Key size={16} /> },
  packages: { label: "Hosting Packages catalog", icon: <Box size={16} /> },
};

function formatBytes(n?: number): string {
  if (!n || n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

export default function TransferPage() {
  const [transfers, setTransfers] = useState<TransferJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showWizard, setShowWizard] = useState(false);
  const [showDetail, setShowDetail] = useState<TransferJob | null>(null);
  const [wizardStep, setWizardStep] = useState(1);
  const [testing, setTesting] = useState(false);
  const [discovering, setDiscovering] = useState(false);
  const [creating, setCreating] = useState(false);
  const [discovered, setDiscovered] = useState<DiscoveredData | null>(null);

  // authMode toggles between the legacy root-password flow and the new
  // transfer-token flow (token minted on the source panel, redeemed by
  // this destination). Token is the recommended default — the password
  // mode stays available so non-Betazen sources can still be migrated.
  const [authMode, setAuthMode] = useState<"token" | "password">("token");
  const [connForm, setConnForm] = useState({
    ip: "", port: "22", username: "root", password: "", token: "", panelUrl: "",
  });
  const [components, setComponents] = useState<Record<string, boolean>>({
    hostname: true, software: true, dns: true, ssl: true, domains: true, files: true,
    databases: true, email_data: true, ftp_accounts: true, cron_jobs: true,
    firewall: true, server_config: true, node_apps: true,
    ssh_keys: true, packages: true,
  });

  // Selection holds the per-resource whitelists. After the user-centric
  // rewrite, the wizard primarily drives off `linux_users` — picking a
  // user cascades to their domains, dbs, mailboxes, etc. on the backend.
  // The other lists are kept for two reasons: server-wide resources
  // (DNS / SSL / MongoDB) that don't belong to a single linux user, and
  // back-compat with older clients.
  type Selection = {
    linux_users: string[];
    domains: string[]; mysql_dbs: string[]; mongo_dbs: string[];
    email_domains: string[]; dns_zones: string[]; ssl_domains: string[];
    ftp_users: string[]; cron_users: string[]; node_apps: string[];
  };
  const emptySelection = (): Selection => ({
    linux_users: [],
    domains: [], mysql_dbs: [], mongo_dbs: [], email_domains: [],
    dns_zones: [], ssl_domains: [], ftp_users: [], cron_users: [], node_apps: [],
  });
  const [selection, setSelection] = useState<Selection>(emptySelection());

  const toggleSelectionItem = (key: keyof Selection, item: string) => {
    setSelection((prev) => {
      const current = prev[key];
      const next = current.includes(item) ? current.filter((x) => x !== item) : [...current, item];
      return { ...prev, [key]: next };
    });
  };
  const toggleSelectionAll = (key: keyof Selection, items: string[]) => {
    setSelection((prev) => {
      const allSelected = items.length > 0 && items.every((i) => prev[key].includes(i));
      return { ...prev, [key]: allSelected ? [] : [...items] };
    });
  };

  // Owner-controlled flag — whether to render the "Demo / Example values"
  // hint on the wizard's connection step. Mirrors the login pages' demo
  // block toggle and is read from the unauthenticated public-settings
  // endpoint so it stays consistent across surfaces.
  const [showDemoTransfer, setShowDemoTransfer] = useState(false);
  const [demoFilled, setDemoFilled] = useState(false);

  const demoTransfer = {
    ip: "203.0.113.42",
    port: "22",
    token: "bzn_xfer_demo_0123456789abcdef",
    panelUrl: "https://panel.old-server.com",
    username: "root",
  };

  const handleDemoTransferFill = () => {
    setAuthMode("token");
    setConnForm({
      ip: demoTransfer.ip,
      port: demoTransfer.port,
      username: demoTransfer.username,
      password: "",
      token: demoTransfer.token,
      panelUrl: demoTransfer.panelUrl,
    });
    setDemoFilled(true);
    toast.success("Demo values filled");
    setTimeout(() => setDemoFilled(false), 2000);
  };

  useEffect(() => { fetchTransfers(); }, []);
  useEffect(() => {
    axios
      .get("/api/v1/public-settings")
      .then((r) => setShowDemoTransfer(!!r?.data?.data?.show_demo_transfer_settings))
      .catch(() => setShowDemoTransfer(false));
  }, []);

  const fetchTransfers = async () => {
    setLoading(true);
    try {
      const res = await api.get("/transfers");
      setTransfers(res.data.data || []);
    } catch { /* empty */ } finally {
      setLoading(false);
    }
  };

  const refreshDetail = useCallback(async (id: string) => {
    try {
      const res = await api.get(`/transfers/${id}`);
      setShowDetail(res.data.data);
    } catch { /* ignore */ }
  }, []);

  useEffect(() => {
    if (!showDetail || (showDetail.status !== "in_progress" && showDetail.status !== "pending")) return;
    // 1s while in-flight so the bandwidth meter updates in near real time;
    // the backend ticker writes updates every 500ms.
    const interval = setInterval(() => refreshDetail(showDetail.id), 1000);
    return () => clearInterval(interval);
  }, [showDetail, refreshDetail]);

  // buildAuthPayload assembles the credential subset the API expects for
  // the active auth mode, so the three connect endpoints (test, discover,
  // create) all stay in sync without a per-call switch.
  const buildAuthPayload = () => {
    if (authMode === "token") {
      return {
        auth_method: "token",
        token: connForm.token.trim(),
        panel_url: connForm.panelUrl.trim() || undefined,
      };
    }
    return {
      auth_method: "password",
      username: connForm.username,
      password: connForm.password,
    };
  };

  const validateAuthForm = (): string | null => {
    if (!connForm.ip) return "Source IP is required";
    if (authMode === "token") {
      if (!connForm.token.trim()) return "Paste a transfer token from the source panel";
    } else {
      if (!connForm.username || !connForm.password) return "Username and password are required";
    }
    return null;
  };

  const handleTestConnection = async () => {
    const err = validateAuthForm();
    if (err) { toast.error(err); return; }
    setTesting(true);
    try {
      await api.post("/transfers/test-connection", {
        protocol: "ssh",
        host: connForm.ip,
        port: parseInt(connForm.port) || 22,
        ...buildAuthPayload(),
      });
      toast.success("SSH connection successful");
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Connection failed");
    } finally {
      setTesting(false);
    }
  };

  const handleDiscover = async () => {
    const err = validateAuthForm();
    if (err) { toast.error(err); return; }
    setDiscovering(true);
    try {
      const res = await api.post("/transfers/discover", {
        source_ip: connForm.ip,
        port: parseInt(connForm.port) || 22,
        ...buildAuthPayload(),
      });
      const d: DiscoveredData = res.data.data;
      setDiscovered(d);
      // Default state: every PANEL-MANAGED linux user pre-selected, plus
      // the server-wide resources (DNS / SSL / MongoDB) since those don't
      // belong to any single user. OS accounts under /home (cloud-init
      // ubuntu, manually useradd'd shells) are still shown but NOT
      // pre-checked — migrating them would just clutter the destination.
      // The per-user resources (mysql / mailboxes / ftp / cron / node) are
      // intentionally left empty — the backend cascade fills them in from
      // linux_users so the operator doesn't have to micro-pick.
      setSelection({
        linux_users: (d.linux_users || []).filter((u) => u.panel_managed).map((u) => u.username),
        domains: [...(d.domains || [])],
        mysql_dbs: [],
        mongo_dbs: [...(d.databases || [])],
        email_domains: [],
        dns_zones: [...(d.dns_zones || [])],
        ssl_domains: [...(d.ssl_domains || [])],
        ftp_users: [],
        cron_users: [],
        node_apps: [],
      });
      if (d.partial && d.warnings?.length) {
        toast(
          `Discovery finished, but ${d.warnings.length} section(s) timed out:\n` +
            d.warnings.join("\n"),
          { icon: "⚠️", duration: 9000 }
        );
      } else {
        toast.success("Discovery complete");
      }
      setWizardStep(2);
    } catch (e: any) {
      toast.error(discoveryErrorMessage(e), { duration: 9000 });
    } finally {
      setDiscovering(false);
    }
  };

  const handleStartTransfer = async () => {
    setCreating(true);
    try {
      const res = await api.post("/transfers", {
        source_ip: connForm.ip,
        source_port: parseInt(connForm.port) || 22,
        protocol: "ssh",
        components,
        selection,
        ...buildAuthPayload(),
      });
      toast.success("Transfer started");
      setShowWizard(false);
      setWizardStep(1);
      setDiscovered(null);
      setSelection(emptySelection());
      setConnForm({ ip: "", port: "22", username: "root", password: "", token: "", panelUrl: "" });
      fetchTransfers();
      setShowDetail(res.data.data);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to start transfer");
    } finally {
      setCreating(false);
    }
  };

  const handleCancel = async (id: string) => {
    if (!await confirmAction({ title: "Cancel?", description: "Cancel this transfer?", danger: true, confirmLabel: "Cancel" })) return;
    try {
      await api.post(`/transfers/${id}/cancel`);
      toast.success("Transfer cancelled");
      fetchTransfers();
      if (showDetail?.id === id) refreshDetail(id);
    } catch { toast.error("Failed to cancel"); }
  };

  const filtered = transfers.filter((t) =>
    (t.source_server?.ip || "").includes(search) ||
    (t.status || "").toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      header: "Source",
      accessor: (t: TransferJob) => (
        <div className="flex items-center gap-2">
          <Server size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{t.source_server?.ip || "Unknown"}</span>
          {t.source_server?.auth_method === "token" && (
            <span className="px-1.5 py-0.5 text-[10px] uppercase rounded bg-emerald-500/15 text-emerald-300 font-medium" title="Authenticated with a one-shot transfer token">
              token
            </span>
          )}
        </div>
      ),
    },
    {
      header: "Type",
      accessor: (t: TransferJob) => (
        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium capitalize bg-blue-500/10 text-blue-400">
          {t.type}
        </span>
      ),
    },
    {
      header: "Progress",
      accessor: (t: TransferJob) => (
        <div className="flex items-center gap-2 min-w-[120px]">
          <div className="flex-1 h-2 bg-panel-border/30 rounded-full overflow-hidden">
            <div className={`h-full rounded-full transition-all ${t.status === "failed" ? "bg-red-500" : t.status === "completed" ? "bg-green-500" : "bg-blue-500"}`}
              style={{ width: `${t.progress}%` }} />
          </div>
          <span className="text-xs text-panel-muted w-8">{t.progress}%</span>
        </div>
      ),
    },
    {
      header: "Status",
      accessor: (t: TransferJob) => <StatusBadge status={t.status === "in_progress" ? "deploying" : t.status === "partial" ? "warning" : t.status} />,
    },
    {
      header: "Created",
      accessor: (t: TransferJob) => (
        <span className="text-panel-muted text-sm">{t.created_at ? new Date(t.created_at).toLocaleString() : "--"}</span>
      ),
    },
    {
      header: "Actions",
      accessor: (t: TransferJob) => (
        <div className="flex items-center gap-1">
          <button onClick={() => { setShowDetail(t); refreshDetail(t.id); }}
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors" title="View Details">
            <Eye size={14} />
          </button>
          {(t.status === "in_progress" || t.status === "pending") && (
            <button onClick={() => handleCancel(t.id)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors" title="Cancel">
              <XCircle size={14} />
            </button>
          )}
        </div>
      ),
    },
  ];

  // domainSettingsByName lets step 2 attach the per-domain PHP version /
  // SSL / WP chips to each row in the Domains card without an O(n²) lookup.
  const domainSettingsByName = (discovered?.domain_settings || []).reduce<Record<string, DomainSetting>>((acc, ds) => {
    acc[ds.domain] = ds;
    return acc;
  }, {});

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Server Transfer</h1>
          <p className="text-panel-muted text-sm mt-1">Migrate servers — transfer domains, DNS, SSL, databases, email, and more</p>
        </div>
        <div className="flex items-center gap-2">
          <Button onClick={fetchTransfers} className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm">
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
          </Button>
          <Button onClick={() => { setShowWizard(true); setWizardStep(1); setDiscovered(null); }}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
            <Plus size={14} /> New Transfer
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input type="text" placeholder="Search transfers..." value={search} onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm" />
          </div>
        </div>
      </Card>

      <Card>
        {loading ? (
          <div className="p-8"><div className="space-y-3">{[1, 2, 3].map((i) => (<div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />))}</div></div>
        ) : filtered.length > 0 ? (
          <Table columns={columns} data={filtered} />
        ) : (
          <div className="text-center py-16 px-4">
            <ArrowLeftRight size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">No transfers found</h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search ? "No transfers match your search." : "Start a new transfer to migrate a server."}
            </p>
            {!search && (
              <Button onClick={() => { setShowWizard(true); setWizardStep(1); }}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
                <Plus size={14} /> New Transfer
              </Button>
            )}
          </div>
        )}
      </Card>

      {/* New Transfer Wizard */}
      <Modal isOpen={showWizard} onClose={() => setShowWizard(false)} title={`New Transfer — Step ${wizardStep} of 3`} size="xl">
        <div className="space-y-4">
          {/* Step indicators */}
          <div className="flex items-center gap-2 mb-4">
            {[1, 2, 3].map((s) => (
              <div key={s} className="flex items-center gap-2">
                <div className={`w-8 h-8 rounded-full flex items-center justify-center text-xs font-bold ${wizardStep >= s ? "bg-blue-600 text-white" : "bg-panel-border/30 text-panel-muted"}`}>{s}</div>
                {s < 3 && <div className={`w-12 h-0.5 ${wizardStep > s ? "bg-blue-600" : "bg-panel-border/30"}`} />}
              </div>
            ))}
            <div className="ml-3 text-sm text-panel-muted">
              {wizardStep === 1 && "Source Server Connection"}
              {wizardStep === 2 && "Review Discovered Resources"}
              {wizardStep === 3 && "Select Components & Start"}
            </div>
          </div>

          {/* Step 1: Connection */}
          {wizardStep === 1 && (
            <div className="space-y-4">
              {/* Demo / example values — owner-gated so production panels
                  can hide sample credentials from staff running real
                  migrations. Matches the login-page demo toggle. */}
              {showDemoTransfer && (
                <div className="bg-panel-bg/60 border border-dashed border-blue-500/30 rounded-xl p-3">
                  <div className="flex items-center justify-between mb-2">
                    <span className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wider text-blue-400">
                      <Lightbulb size={12} /> Demo / Example
                    </span>
                    <button
                      type="button"
                      onClick={handleDemoTransferFill}
                      className="flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-lg bg-blue-600/10 border border-blue-500/20 text-blue-400 hover:bg-blue-600/20 hover:border-blue-500/40 transition-colors"
                    >
                      {demoFilled ? <Check size={11} /> : <Copy size={11} />}
                      {demoFilled ? "Filled!" : "Fill with demo values"}
                    </button>
                  </div>
                  <div className="grid grid-cols-2 gap-1.5 text-[11px] font-mono text-panel-muted">
                    <div className="flex justify-between bg-panel-bg rounded px-2 py-1">
                      <span>IP</span><span className="text-panel-text">{demoTransfer.ip}</span>
                    </div>
                    <div className="flex justify-between bg-panel-bg rounded px-2 py-1">
                      <span>Port</span><span className="text-panel-text">{demoTransfer.port}</span>
                    </div>
                    <div className="flex justify-between bg-panel-bg rounded px-2 py-1 col-span-2">
                      <span>Token</span><span className="text-panel-text truncate ml-2">{demoTransfer.token}</span>
                    </div>
                    <div className="flex justify-between bg-panel-bg rounded px-2 py-1 col-span-2">
                      <span>Panel URL</span><span className="text-panel-text">{demoTransfer.panelUrl}</span>
                    </div>
                  </div>
                </div>
              )}

              {/* Auth mode toggle — token preferred, password kept for non-Betazen sources */}
              <div className="grid grid-cols-2 gap-2">
                <button type="button" onClick={() => setAuthMode("token")}
                  className={`flex items-center gap-3 p-3 rounded-lg border transition-colors ${authMode === "token" ? "border-blue-500/60 bg-blue-500/10" : "border-panel-border bg-panel-bg/40 hover:border-panel-border/70"}`}>
                  <KeyRound size={16} className={authMode === "token" ? "text-blue-400" : "text-panel-muted"} />
                  <div className="text-left">
                    <p className="text-sm font-medium text-panel-text">Transfer Token</p>
                    <p className="text-[11px] text-panel-muted">Generated on the old server (recommended)</p>
                  </div>
                </button>
                <button type="button" onClick={() => setAuthMode("password")}
                  className={`flex items-center gap-3 p-3 rounded-lg border transition-colors ${authMode === "password" ? "border-blue-500/60 bg-blue-500/10" : "border-panel-border bg-panel-bg/40 hover:border-panel-border/70"}`}>
                  <Lock size={16} className={authMode === "password" ? "text-blue-400" : "text-panel-muted"} />
                  <div className="text-left">
                    <p className="text-sm font-medium text-panel-text">Root Password</p>
                    <p className="text-[11px] text-panel-muted">For non-Betazen source servers</p>
                  </div>
                </button>
              </div>

              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className={labelClass}>Source Server IP *</label>
                  <input type="text" placeholder="192.168.1.100" value={connForm.ip} onChange={(e) => setConnForm({ ...connForm, ip: e.target.value })} className={inputClass} />
                </div>
                <div>
                  <label className={labelClass}>SSH Port *</label>
                  <input type="number" value={connForm.port} onChange={(e) => setConnForm({ ...connForm, port: e.target.value })} className={inputClass} />
                </div>
              </div>

              {authMode === "token" ? (
                <div className="space-y-3">
                  <div>
                    <label className={labelClass}>Transfer Token *</label>
                    <input type="text" placeholder="bzn_xfer_…" value={connForm.token} onChange={(e) => setConnForm({ ...connForm, token: e.target.value })} className={`${inputClass} font-mono`} />
                    <p className="text-[11px] text-panel-muted mt-1">
                      Generate on the old server: <span className="text-panel-text">WHM → Transfer Tokens → Generate Token</span>. The token is one-shot and expires.
                    </p>
                  </div>
                  <div>
                    <label className={labelClass}>Source Panel URL <span className="text-panel-muted font-normal">(optional)</span></label>
                    <input type="text" placeholder="https://panel.old-server.com" value={connForm.panelUrl} onChange={(e) => setConnForm({ ...connForm, panelUrl: e.target.value })} className={inputClass} />
                    <p className="text-[11px] text-panel-muted mt-1">Leave blank to auto-try http://&lt;ip&gt; and https://&lt;ip&gt;.</p>
                  </div>
                </div>
              ) : (
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className={labelClass}>Username *</label>
                    <input type="text" placeholder="root" value={connForm.username} onChange={(e) => setConnForm({ ...connForm, username: e.target.value })} className={inputClass} />
                  </div>
                  <div>
                    <label className={labelClass}>Password *</label>
                    {/* hideGenerator: source-server SSH password — operator
                        types the existing root password of the box being
                        migrated FROM. */}
                    <PasswordInput value={connForm.password} onChange={(v) => setConnForm({ ...connForm, password: v })} inputClassName={inputClass} hideGenerator />
                  </div>
                </div>
              )}

              <div className="flex justify-between pt-2">
                <button type="button" onClick={handleTestConnection} disabled={testing}
                  className="px-4 py-2 text-sm bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors disabled:opacity-50">
                  {testing ? "Testing..." : "Test Connection"}
                </button>
                <div className="flex gap-3">
                  <button type="button" onClick={() => setShowWizard(false)} className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Cancel</button>
                  <button type="button" onClick={handleDiscover} disabled={discovering}
                    className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
                    {discovering ? "Discovering..." : "Connect & Discover"}
                  </button>
                </div>
              </div>
            </div>
          )}

          {/* Step 2: Discovery Results — user-centric selection */}
          {wizardStep === 2 && discovered && (() => {
            const linuxUsers = discovered.linux_users || [];

            return (
              <div className="space-y-4">
                <p className="text-sm text-panel-muted">
                  Pick the linux users you want to migrate — their domains, applications, WordPress sites, mailboxes, FTP accounts, databases, and cron jobs come along automatically. Server-wide items (DNS / SSL / MongoDB) sit in their own cards below.
                </p>

                {/* ============ Linux Users (primary selector) ============ */}
                <Card>
                  <div className="p-4">
                    <div className="flex items-center justify-between mb-3 gap-2">
                      <h4 className="text-sm font-medium text-panel-text flex items-center gap-2">
                        <Users size={14} className="text-blue-400" />
                        Linux Users
                        <span className="text-xs text-panel-muted font-normal">
                          {selection.linux_users.length}/{linuxUsers.length}
                        </span>
                      </h4>
                      {linuxUsers.length > 0 && (() => {
                        // "Select all" picks PANEL-MANAGED users only — same
                        // default the wizard ships with after Discover. The
                        // operator can still tick OS users individually.
                        const targets = linuxUsers.filter((u) => u.panel_managed).map((u) => u.username);
                        const everyTargetPicked = targets.length > 0 && targets.every((n) => selection.linux_users.includes(n));
                        return (
                          <button type="button"
                            onClick={() => toggleSelectionAll("linux_users", targets)}
                            className="text-xs text-blue-400 hover:text-blue-300 transition-colors whitespace-nowrap">
                            {everyTargetPicked ? "Deselect all" : "Select all"}
                          </button>
                        );
                      })()}
                    </div>

                    {linuxUsers.length === 0 ? (
                      <p className="text-sm text-panel-muted">No /home/* accounts found on the source.</p>
                    ) : (
                      <div className="grid grid-cols-1 md:grid-cols-2 gap-2 max-h-72 overflow-y-auto pr-1">
                        {linuxUsers.map((u) => {
                          const checked = selection.linux_users.includes(u.username);
                          return (
                            <label key={u.username}
                              className={`flex items-start gap-3 px-3 py-2 rounded border cursor-pointer transition-colors ${checked ? "border-blue-500/40 bg-blue-500/5" : "border-panel-border/40 bg-panel-bg/30 hover:border-panel-border"}`}>
                              <input type="checkbox" checked={checked}
                                onChange={() => toggleSelectionItem("linux_users", u.username)}
                                className="mt-1 w-3.5 h-3.5 rounded border-panel-border text-blue-600 focus:ring-blue-500/40 shrink-0" />
                              <div className="min-w-0 flex-1">
                                <div className="flex items-center gap-2 flex-wrap">
                                  <span className="text-sm text-panel-text font-medium truncate">{u.username}</span>
                                  <span className={`text-[10px] uppercase tracking-wider px-1.5 py-0.5 rounded ${u.active ? "bg-emerald-500/15 text-emerald-300" : "bg-panel-border/40 text-panel-muted"}`}>
                                    {u.active ? "active" : "inactive"}
                                  </span>
                                  {u.locked && (
                                    <span className="text-[10px] uppercase tracking-wider px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-300" title="Unix password is locked. SSH-key access still works — this is normal for panel-created tenants.">
                                      no-pw
                                    </span>
                                  )}
                                  {!u.panel_managed && (
                                    <span className="text-[10px] uppercase tracking-wider px-1.5 py-0.5 rounded bg-orange-500/15 text-orange-300" title="OS-level account, not a panel vendor. Tick to migrate anyway.">
                                      OS user
                                    </span>
                                  )}
                                  <span className="text-[10px] text-panel-muted">UID {u.uid}</span>
                                </div>
                                <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-panel-muted">
                                  {u.domains > 0 && <span>{u.domains} site{u.domains !== 1 ? "s" : ""}</span>}
                                  {u.databases > 0 && <span>{u.databases} db{u.databases !== 1 ? "s" : ""}</span>}
                                  {u.mailboxes > 0 && <span>{u.mailboxes} mailbox{u.mailboxes !== 1 ? "es" : ""}</span>}
                                  {u.ftp_users > 0 && <span>{u.ftp_users} ftp</span>}
                                  {u.cron_jobs > 0 && <span>{u.cron_jobs} cron</span>}
                                  {u.node_apps > 0 && <span>{u.node_apps} node</span>}
                                  {u.wp_sites > 0 && <span>{u.wp_sites} WordPress</span>}
                                  {u.home_bytes > 0 && <span>{formatBytes(u.home_bytes)}</span>}
                                </div>
                              </div>
                            </label>
                          );
                        })}
                      </div>
                    )}
                  </div>
                </Card>

                {/* ============ Hostname (info only) ============ */}
                <Card>
                  <div className="p-4 flex items-start justify-between gap-4">
                    <div>
                      <h4 className="text-sm font-medium text-panel-text mb-1">Hostname</h4>
                      <p className="text-sm text-panel-muted">{discovered.hostname || "N/A"}</p>
                    </div>
                    <div>
                      {discovered.server_type && discovered.server_type !== "bare" ? (
                        <span className="inline-block px-2 py-0.5 text-xs font-medium rounded bg-blue-500/20 text-blue-400 capitalize">
                          {discovered.server_type}
                        </span>
                      ) : (
                        <span className="inline-block px-2 py-0.5 text-xs font-medium rounded bg-panel-border text-panel-muted">
                          No control panel
                        </span>
                      )}
                    </div>
                  </div>
                </Card>

                {/* ============ Domains (with PHP / SSL / WP chips) ============ */}
                <Card>
                  <div className="p-4">
                    <div className="flex items-center justify-between mb-2 gap-2">
                      <h4 className="text-sm font-medium text-panel-text flex items-center gap-2">
                        <Globe size={14} className="text-panel-muted" />
                        Domains
                        <span className="text-xs text-panel-muted font-normal">
                          {selection.domains.length}/{(discovered.domains || []).length}
                        </span>
                      </h4>
                      {(discovered.domains || []).length > 0 && (
                        <button type="button"
                          onClick={() => toggleSelectionAll("domains", discovered.domains || [])}
                          className="text-xs text-blue-400 hover:text-blue-300 transition-colors whitespace-nowrap">
                          {selection.domains.length === (discovered.domains || []).length ? "Deselect all" : "Select all"}
                        </button>
                      )}
                    </div>
                    {(discovered.domains || []).length === 0 ? (
                      <p className="text-sm text-panel-muted">No domains discovered.</p>
                    ) : (
                      <div className="max-h-48 overflow-y-auto space-y-1 pr-1">
                        {(discovered.domains || []).map((dom) => {
                          const checked = selection.domains.includes(dom);
                          const meta = domainSettingsByName[dom];
                          return (
                            <label key={dom}
                              className={`flex items-center gap-2 px-2 py-1.5 rounded border cursor-pointer transition-colors ${checked ? "border-blue-500/40 bg-blue-500/5" : "border-panel-border/40 bg-panel-bg/30 hover:border-panel-border"}`}>
                              <input type="checkbox" checked={checked}
                                onChange={() => toggleSelectionItem("domains", dom)}
                                className="w-3.5 h-3.5 rounded border-panel-border text-blue-600 focus:ring-blue-500/40 shrink-0" />
                              <span className="text-xs text-panel-text truncate flex-1">{dom}</span>
                              {meta?.php_version && (
                                <span className="px-1.5 py-0.5 text-[10px] rounded bg-purple-500/15 text-purple-300 font-medium">PHP {meta.php_version}</span>
                              )}
                              {meta?.has_ssl && (
                                <span className="px-1.5 py-0.5 text-[10px] rounded bg-emerald-500/15 text-emerald-300 font-medium">SSL</span>
                              )}
                              {meta?.wp_installed && (
                                <span className="px-1.5 py-0.5 text-[10px] rounded bg-blue-500/15 text-blue-300 font-medium">WP</span>
                              )}
                              {meta?.owner && (
                                <span className="text-[10px] text-panel-muted shrink-0">{meta.owner}</span>
                              )}
                            </label>
                          );
                        })}
                      </div>
                    )}
                  </div>
                </Card>

                {/* ============ Server-wide items (don't belong to a single user) ============ */}
                <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                  <Card>
                    <div className="p-4">
                      <div className="flex items-center justify-between mb-2 gap-2">
                        <h4 className="text-sm font-medium text-panel-text">DNS Zones <span className="text-xs text-panel-muted font-normal">{selection.dns_zones.length}/{(discovered.dns_zones || []).length}</span></h4>
                        {(discovered.dns_zones || []).length > 0 && (
                          <button type="button"
                            onClick={() => toggleSelectionAll("dns_zones", discovered.dns_zones || [])}
                            className="text-xs text-blue-400 hover:text-blue-300 transition-colors whitespace-nowrap">
                            {selection.dns_zones.length === (discovered.dns_zones || []).length ? "Deselect all" : "Select all"}
                          </button>
                        )}
                      </div>
                      {(discovered.dns_zones || []).length === 0 ? (
                        <p className="text-xs text-panel-muted">None</p>
                      ) : (
                        <div className="max-h-32 overflow-y-auto space-y-1 pr-1">
                          {(discovered.dns_zones || []).map((z) => {
                            const checked = selection.dns_zones.includes(z);
                            return (
                              <label key={z} className={`flex items-center gap-2 px-2 py-1 rounded border cursor-pointer transition-colors ${checked ? "border-blue-500/40 bg-blue-500/5" : "border-panel-border/40 bg-panel-bg/30 hover:border-panel-border"}`}>
                                <input type="checkbox" checked={checked} onChange={() => toggleSelectionItem("dns_zones", z)} className="w-3.5 h-3.5 rounded border-panel-border text-blue-600 focus:ring-blue-500/40 shrink-0" />
                                <span className="text-xs text-panel-text truncate">{z}</span>
                              </label>
                            );
                          })}
                        </div>
                      )}
                    </div>
                  </Card>

                  <Card>
                    <div className="p-4">
                      <div className="flex items-center justify-between mb-2 gap-2">
                        <h4 className="text-sm font-medium text-panel-text">SSL Certs <span className="text-xs text-panel-muted font-normal">{selection.ssl_domains.length}/{(discovered.ssl_domains || []).length}</span></h4>
                        {(discovered.ssl_domains || []).length > 0 && (
                          <button type="button"
                            onClick={() => toggleSelectionAll("ssl_domains", discovered.ssl_domains || [])}
                            className="text-xs text-blue-400 hover:text-blue-300 transition-colors whitespace-nowrap">
                            {selection.ssl_domains.length === (discovered.ssl_domains || []).length ? "Deselect all" : "Select all"}
                          </button>
                        )}
                      </div>
                      {(discovered.ssl_domains || []).length === 0 ? (
                        <p className="text-xs text-panel-muted">None</p>
                      ) : (
                        <div className="max-h-32 overflow-y-auto space-y-1 pr-1">
                          {(discovered.ssl_domains || []).map((d) => {
                            const checked = selection.ssl_domains.includes(d);
                            return (
                              <label key={d} className={`flex items-center gap-2 px-2 py-1 rounded border cursor-pointer transition-colors ${checked ? "border-blue-500/40 bg-blue-500/5" : "border-panel-border/40 bg-panel-bg/30 hover:border-panel-border"}`}>
                                <input type="checkbox" checked={checked} onChange={() => toggleSelectionItem("ssl_domains", d)} className="w-3.5 h-3.5 rounded border-panel-border text-blue-600 focus:ring-blue-500/40 shrink-0" />
                                <span className="text-xs text-panel-text truncate">{d}</span>
                              </label>
                            );
                          })}
                        </div>
                      )}
                    </div>
                  </Card>

                  <Card>
                    <div className="p-4">
                      <div className="flex items-center justify-between mb-2 gap-2">
                        <h4 className="text-sm font-medium text-panel-text">MongoDB <span className="text-xs text-panel-muted font-normal">{selection.mongo_dbs.length}/{(discovered.databases || []).length}</span></h4>
                        {(discovered.databases || []).length > 0 && (
                          <button type="button"
                            onClick={() => toggleSelectionAll("mongo_dbs", discovered.databases || [])}
                            className="text-xs text-blue-400 hover:text-blue-300 transition-colors whitespace-nowrap">
                            {selection.mongo_dbs.length === (discovered.databases || []).length ? "Deselect all" : "Select all"}
                          </button>
                        )}
                      </div>
                      {(discovered.databases || []).length === 0 ? (
                        <p className="text-xs text-panel-muted">None</p>
                      ) : (
                        <div className="max-h-32 overflow-y-auto space-y-1 pr-1">
                          {(discovered.databases || []).map((d) => {
                            const checked = selection.mongo_dbs.includes(d);
                            return (
                              <label key={d} className={`flex items-center gap-2 px-2 py-1 rounded border cursor-pointer transition-colors ${checked ? "border-blue-500/40 bg-blue-500/5" : "border-panel-border/40 bg-panel-bg/30 hover:border-panel-border"}`}>
                                <input type="checkbox" checked={checked} onChange={() => toggleSelectionItem("mongo_dbs", d)} className="w-3.5 h-3.5 rounded border-panel-border text-blue-600 focus:ring-blue-500/40 shrink-0" />
                                <span className="text-xs text-panel-text truncate">{d}</span>
                              </label>
                            );
                          })}
                        </div>
                      )}
                    </div>
                  </Card>
                </div>

                {/* Cascade preview banner */}
                {selection.linux_users.length > 0 && (
                  <div className="p-3 rounded border border-emerald-500/20 bg-emerald-500/5 text-xs text-emerald-200/90">
                    {selection.linux_users.length} user{selection.linux_users.length !== 1 ? "s" : ""} selected — every application, deployed software, WordPress site, mailbox, FTP account, MySQL database, and cron job belonging to {selection.linux_users.length === 1 ? "that user" : "those users"} will be transferred automatically.
                  </div>
                )}

                <div className="flex justify-between pt-2">
                  <button type="button" onClick={() => setWizardStep(1)} className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Back</button>
                  <button type="button" onClick={() => setWizardStep(3)} className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors">
                    Next: Select Components
                  </button>
                </div>
              </div>
            );
          })()}

          {/* Step 3: Select Components & Start */}
          {wizardStep === 3 && (
            <div className="space-y-4">
              <p className="text-sm text-panel-muted">Choose which categories to transfer. Individual items inside each category were picked on the previous step.</p>
              <div className="grid grid-cols-2 gap-3">
                {Object.entries(componentLabels).map(([key, { label, icon }]) => (
                  <label key={key} className={`flex items-center gap-3 p-3 rounded-lg border cursor-pointer transition-colors ${components[key] ? "border-blue-500/40 bg-blue-500/5" : "border-panel-border bg-panel-bg/50 hover:border-panel-border"}`}>
                    <input type="checkbox" checked={components[key] || false}
                      onChange={(e) => setComponents({ ...components, [key]: e.target.checked })}
                      className="w-4 h-4 rounded border-panel-border text-blue-600 focus:ring-blue-500/40" />
                    <span className="text-panel-muted">{icon}</span>
                    <span className="text-sm text-panel-text">{label}</span>
                  </label>
                ))}
              </div>

              <div className="p-3 bg-yellow-500/5 border border-yellow-500/20 rounded-lg">
                <p className="text-sm text-yellow-400">This will transfer data from <strong>{connForm.ip}</strong> to this server. Existing data may be overwritten. Make sure you have backups before proceeding.</p>
              </div>
              <div className="flex justify-between pt-2">
                <button type="button" onClick={() => setWizardStep(2)} className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Back</button>
                <button type="button" onClick={handleStartTransfer} disabled={creating}
                  className="px-4 py-2 text-sm bg-green-600 hover:bg-green-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
                  {creating ? "Starting..." : "Start Transfer"}
                </button>
              </div>
            </div>
          )}
        </div>
      </Modal>

      {/* Transfer Detail Modal */}
      <Modal isOpen={!!showDetail} onClose={() => setShowDetail(null)} title="Transfer Details" size="xl">
        {showDetail && (
          <div className="space-y-4">
            <div className="flex items-center justify-between p-3 bg-panel-bg/50 rounded-lg border border-panel-border">
              <div>
                <p className="text-sm text-panel-text font-medium">Source: {showDetail.source_server?.ip}</p>
                <p className="text-xs text-panel-muted">Started: {showDetail.started_at ? new Date(showDetail.started_at).toLocaleString() : "Pending"}</p>
              </div>
              <div className="flex items-center gap-3">
                <StatusBadge status={showDetail.status === "in_progress" ? "deploying" : showDetail.status === "partial" ? "warning" : showDetail.status} />
                {(showDetail.status === "in_progress" || showDetail.status === "pending") && (
                  <button onClick={() => handleCancel(showDetail.id)}
                    className="px-3 py-1.5 text-xs bg-red-600 hover:bg-red-700 text-white rounded-lg transition-colors">Cancel</button>
                )}
              </div>
            </div>

            <div>
              <div className="flex justify-between text-xs text-panel-muted mb-1">
                <span>Overall Progress</span>
                <span>{showDetail.progress}%</span>
              </div>
              <div className="w-full h-3 bg-panel-border/30 rounded-full overflow-hidden">
                <div className={`h-full rounded-full transition-all duration-500 ${showDetail.status === "failed" ? "bg-red-500" : showDetail.status === "completed" ? "bg-green-500" : "bg-blue-500"}`}
                  style={{ width: `${showDetail.progress}%` }} />
              </div>
            </div>

            <div>
              <h4 className="text-sm font-medium text-panel-text mb-2">Steps</h4>
              <div className="space-y-1">
                {showDetail.steps?.map((step, idx) => {
                  const live = step.status === "in_progress" && (
                    (step.bytes_total || 0) > 0 || (step.throughput_mbps || 0) > 0 || !!step.current_item
                  );
                  return (
                    <div key={idx} className="p-2 rounded-lg hover:bg-panel-bg/30">
                      <div className="flex items-center gap-3">
                        {stepIcons[step.status] || stepIcons.pending}
                        <span className={`text-sm flex-1 ${step.status === "completed" ? "text-panel-text" : step.status === "failed" ? "text-red-400" : "text-panel-muted"}`}>
                          {step.name}
                        </span>
                        {step.details && <span className="text-xs text-panel-muted">{step.details}</span>}
                        {step.error && <span className="text-xs text-red-400">{step.error}</span>}
                      </div>
                      {live && (
                        <div className="mt-2 ml-7 space-y-1">
                          {(step.bytes_total || 0) > 0 && (
                            <div className="w-full h-1.5 bg-panel-border/30 rounded-full overflow-hidden">
                              <div className="h-full bg-blue-500 rounded-full transition-all duration-300"
                                style={{ width: `${Math.min(100, step.progress || 0)}%` }} />
                            </div>
                          )}
                          <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-panel-muted">
                            {step.current_item && <span className="text-panel-text/80">{step.current_item}</span>}
                            {(step.bytes_total || 0) > 0 && (
                              <span>{formatBytes(step.bytes_done)} / {formatBytes(step.bytes_total)} ({step.progress || 0}%)</span>
                            )}
                            {(step.throughput_mbps || 0) > 0 && (
                              <span className="text-blue-300">{step.throughput_mbps!.toFixed(1)} Mbps</span>
                            )}
                            {(step.eta_seconds || 0) > 0 && (
                              <span>ETA {formatEta(step.eta_seconds)}</span>
                            )}
                          </div>
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>

            {showDetail.logs && showDetail.logs.length > 0 && (
              <div>
                <h4 className="text-sm font-medium text-panel-text mb-2">Logs</h4>
                <div className="max-h-48 overflow-y-auto bg-panel-bg rounded-lg border border-panel-border p-3 space-y-1 font-mono text-xs">
                  {showDetail.logs.map((log, idx) => (
                    <div key={idx} className={`${log.level === "error" ? "text-red-400" : log.level === "warn" ? "text-yellow-400" : "text-panel-muted"}`}>
                      <span className="text-panel-muted/50">[{new Date(log.timestamp).toLocaleTimeString()}]</span>{" "}
                      {log.component && <span className="text-blue-400">[{log.component}]</span>}{" "}
                      {log.message}
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </Modal>
    </div>
  );
}
