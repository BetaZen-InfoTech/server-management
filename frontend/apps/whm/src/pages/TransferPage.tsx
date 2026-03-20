import { useState, useEffect, useCallback } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  ArrowLeftRight, Plus, RefreshCw, Search, Eye, XCircle,
  CheckCircle2, Clock, AlertTriangle, Loader2, Server,
  Globe, Database, Mail, Shield, Key, Terminal, HardDrive, Flame
} from "lucide-react";

interface TransferStep {
  name: string;
  status: string;
  started_at?: string;
  completed_at?: string;
  error?: string;
  details?: string;
}

interface TransferLog {
  timestamp: string;
  level: string;
  message: string;
  component?: string;
}

interface DiscoveredData {
  hostname: string;
  domains: string[];
  databases: string[];
  email_domains: string[];
  cron_users: string[];
  ssl_domains: string[];
  dns_zones: string[];
  ftp_users: string[];
}

interface TransferJob {
  id: string;
  type: string;
  direction: string;
  source_server: { hostname: string; ip: string; port: number; protocol: string };
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

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

const stepIcons: Record<string, React.ReactNode> = {
  pending: <Clock size={14} className="text-panel-muted" />,
  in_progress: <Loader2 size={14} className="text-blue-400 animate-spin" />,
  completed: <CheckCircle2 size={14} className="text-green-400" />,
  failed: <AlertTriangle size={14} className="text-red-400" />,
  skipped: <XCircle size={14} className="text-panel-muted" />,
};

const componentLabels: Record<string, { label: string; icon: React.ReactNode }> = {
  hostname: { label: "Hostname", icon: <Server size={16} /> },
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
};

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

  const [connForm, setConnForm] = useState({ ip: "", port: "22", username: "root", password: "" });
  const [components, setComponents] = useState<Record<string, boolean>>({
    hostname: true, dns: true, ssl: true, domains: true, files: true,
    databases: true, email_data: true, ftp_accounts: true, cron_jobs: true,
    firewall: true, server_config: true,
  });

  useEffect(() => { fetchTransfers(); }, []);

  const fetchTransfers = async () => {
    setLoading(true);
    try {
      const res = await api.get("/transfers");
      setTransfers(res.data.data || []);
    } catch { /* empty */ } finally {
      setLoading(false);
    }
  };

  // Auto-refresh detail view for in-progress transfers
  const refreshDetail = useCallback(async (id: string) => {
    try {
      const res = await api.get(`/transfers/${id}`);
      setShowDetail(res.data.data);
    } catch { /* ignore */ }
  }, []);

  useEffect(() => {
    if (!showDetail || (showDetail.status !== "in_progress" && showDetail.status !== "pending")) return;
    const interval = setInterval(() => refreshDetail(showDetail.id), 3000);
    return () => clearInterval(interval);
  }, [showDetail, refreshDetail]);

  const handleTestConnection = async () => {
    if (!connForm.ip || !connForm.username || !connForm.password) {
      toast.error("Fill all connection fields"); return;
    }
    setTesting(true);
    try {
      await api.post("/transfers/test-connection", {
        protocol: "ssh", host: connForm.ip,
        port: parseInt(connForm.port) || 22,
        username: connForm.username, password: connForm.password,
      });
      toast.success("SSH connection successful");
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Connection failed");
    } finally {
      setTesting(false);
    }
  };

  const handleDiscover = async () => {
    if (!connForm.ip || !connForm.username || !connForm.password) {
      toast.error("Fill all connection fields"); return;
    }
    setDiscovering(true);
    try {
      const res = await api.post("/transfers/discover", {
        source_ip: connForm.ip, port: parseInt(connForm.port) || 22,
        username: connForm.username, password: connForm.password,
      });
      setDiscovered(res.data.data);
      toast.success("Discovery complete");
      setWizardStep(2);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Discovery failed");
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
        username: connForm.username,
        password: connForm.password,
        protocol: "ssh",
        components: components,
      });
      toast.success("Transfer started");
      setShowWizard(false);
      setWizardStep(1);
      setDiscovered(null);
      setConnForm({ ip: "", port: "22", username: "root", password: "" });
      fetchTransfers();
      // Open detail view
      setShowDetail(res.data.data);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to start transfer");
    } finally {
      setCreating(false);
    }
  };

  const handleCancel = async (id: string) => {
    if (!confirm("Cancel this transfer?")) return;
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
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className={labelClass}>Username *</label>
                  <input type="text" placeholder="root" value={connForm.username} onChange={(e) => setConnForm({ ...connForm, username: e.target.value })} className={inputClass} />
                </div>
                <div>
                  <label className={labelClass}>Password *</label>
                  <input type="password" value={connForm.password} onChange={(e) => setConnForm({ ...connForm, password: e.target.value })} className={inputClass} />
                </div>
              </div>
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

          {/* Step 2: Discovery Results */}
          {wizardStep === 2 && discovered && (
            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-4">
                <Card>
                  <div className="p-4">
                    <h4 className="text-sm font-medium text-panel-text mb-2">Hostname</h4>
                    <p className="text-sm text-panel-muted">{discovered.hostname || "N/A"}</p>
                  </div>
                </Card>
                <Card>
                  <div className="p-4">
                    <h4 className="text-sm font-medium text-panel-text mb-2">Domains ({discovered.domains?.length || 0})</h4>
                    <div className="text-sm text-panel-muted max-h-20 overflow-y-auto">
                      {discovered.domains?.length ? discovered.domains.join(", ") : "None found"}
                    </div>
                  </div>
                </Card>
                <Card>
                  <div className="p-4">
                    <h4 className="text-sm font-medium text-panel-text mb-2">Databases ({discovered.databases?.length || 0})</h4>
                    <div className="text-sm text-panel-muted max-h-20 overflow-y-auto">
                      {discovered.databases?.length ? discovered.databases.join(", ") : "None found"}
                    </div>
                  </div>
                </Card>
                <Card>
                  <div className="p-4">
                    <h4 className="text-sm font-medium text-panel-text mb-2">Email Domains ({discovered.email_domains?.length || 0})</h4>
                    <div className="text-sm text-panel-muted max-h-20 overflow-y-auto">
                      {discovered.email_domains?.length ? discovered.email_domains.join(", ") : "None found"}
                    </div>
                  </div>
                </Card>
                <Card>
                  <div className="p-4">
                    <h4 className="text-sm font-medium text-panel-text mb-2">DNS Zones ({discovered.dns_zones?.length || 0})</h4>
                    <div className="text-sm text-panel-muted max-h-20 overflow-y-auto">
                      {discovered.dns_zones?.length ? discovered.dns_zones.join(", ") : "None found"}
                    </div>
                  </div>
                </Card>
                <Card>
                  <div className="p-4">
                    <h4 className="text-sm font-medium text-panel-text mb-2">SSL Certificates ({discovered.ssl_domains?.length || 0})</h4>
                    <div className="text-sm text-panel-muted max-h-20 overflow-y-auto">
                      {discovered.ssl_domains?.length ? discovered.ssl_domains.join(", ") : "None found"}
                    </div>
                  </div>
                </Card>
              </div>
              <div className="flex justify-between pt-2">
                <button type="button" onClick={() => setWizardStep(1)} className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Back</button>
                <button type="button" onClick={() => setWizardStep(3)} className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors">
                  Next: Select Components
                </button>
              </div>
            </div>
          )}

          {/* Step 3: Select Components & Start */}
          {wizardStep === 3 && (
            <div className="space-y-4">
              <p className="text-sm text-panel-muted">Select which components to transfer from the source server:</p>
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
            {/* Summary */}
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

            {/* Progress bar */}
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

            {/* Steps */}
            <div>
              <h4 className="text-sm font-medium text-panel-text mb-2">Steps</h4>
              <div className="space-y-1">
                {showDetail.steps?.map((step, idx) => (
                  <div key={idx} className="flex items-center gap-3 p-2 rounded-lg hover:bg-panel-bg/30">
                    {stepIcons[step.status] || stepIcons.pending}
                    <span className={`text-sm flex-1 ${step.status === "completed" ? "text-panel-text" : step.status === "failed" ? "text-red-400" : "text-panel-muted"}`}>
                      {step.name}
                    </span>
                    {step.details && <span className="text-xs text-panel-muted">{step.details}</span>}
                    {step.error && <span className="text-xs text-red-400">{step.error}</span>}
                  </div>
                ))}
              </div>
            </div>

            {/* Logs */}
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
