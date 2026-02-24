import { useState, useEffect, useRef, useCallback } from "react";
import { Card, Button, Modal, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Mail, Server, Shield, Key, Bug, Settings,
  CheckCircle, XCircle, Loader, Circle, Play,
  RefreshCw, ChevronDown, ChevronUp, Terminal
} from "lucide-react";
import EmailServerSettings from "./EmailServerSettings";

interface EmailComponentStatus {
  name: string;
  installed: boolean;
  running: boolean;
  enabled: boolean;
  version: string;
}

interface InstallStep {
  name: string;
  description: string;
  status: "pending" | "running" | "completed" | "failed" | "skipped";
  output: string;
  error?: string;
}

interface EmailInstallation {
  id: string;
  status: "pending" | "running" | "completed" | "failed";
  steps: InstallStep[];
  current_step: number;
  total_steps: number;
  error_message?: string;
}

interface EmailServerConfig {
  id: string;
  hostname: string;
  domain: string;
  postfix_enabled: boolean;
  dovecot_enabled: boolean;
  spamassassin_enabled: boolean;
  opendkim_enabled: boolean;
  clamav_enabled: boolean;
  status: string;
}

interface EmailStatus {
  installed: boolean;
  status: string;
  components: EmailComponentStatus[];
  config: EmailServerConfig | null;
  installation?: EmailInstallation;
}

interface InstallForm {
  hostname: string;
  domain: string;
  spamassassin_enabled: boolean;
  opendkim_enabled: boolean;
  clamav_enabled: boolean;
}

interface WSMessage {
  type: string;
  step: number;
  step_name: string;
  output: string;
  error?: string;
  total: number;
  timestamp: string;
}

export default function EmailServerInstall() {
  const [status, setStatus] = useState<EmailStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [installing, setInstalling] = useState(false);
  const [showInstallModal, setShowInstallModal] = useState(false);
  const [showSettingsModal, setShowSettingsModal] = useState(false);
  const [expandedStep, setExpandedStep] = useState<number | null>(null);
  const [installation, setInstallation] = useState<EmailInstallation | null>(null);
  const [terminalLines, setTerminalLines] = useState<string[]>([]);
  const [wsConnected, setWsConnected] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const terminalRef = useRef<HTMLDivElement>(null);

  const [form, setForm] = useState<InstallForm>({
    hostname: "",
    domain: "",
    spamassassin_enabled: true,
    opendkim_enabled: true,
    clamav_enabled: false,
  });

  useEffect(() => {
    fetchStatus();
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
      disconnectWS();
    };
  }, []);

  // Auto-scroll terminal to bottom
  useEffect(() => {
    if (terminalRef.current) {
      terminalRef.current.scrollTop = terminalRef.current.scrollHeight;
    }
  }, [terminalLines]);

  // Connect WebSocket + start polling when installing
  useEffect(() => {
    if (
      status?.status === "installing" &&
      status?.installation?.id
    ) {
      connectWS();
      startPolling(status.installation.id);
    }
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [status?.status, status?.installation?.id]);

  const connectWS = useCallback(() => {
    if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) return;

    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const wsUrl = `${protocol}//${window.location.host}/ws/install-terminal`;

    try {
      const ws = new WebSocket(wsUrl);
      wsRef.current = ws;

      ws.onopen = () => {
        setWsConnected(true);
        setTerminalLines((prev) => [...prev, "$ Connected to installation terminal..."]);
      };

      ws.onmessage = (event) => {
        try {
          const msg: WSMessage = JSON.parse(event.data);
          handleWSMessage(msg);
        } catch {
          // Raw text message
          setTerminalLines((prev) => [...prev, event.data]);
        }
      };

      ws.onclose = () => {
        setWsConnected(false);
        wsRef.current = null;
      };

      ws.onerror = () => {
        setWsConnected(false);
      };
    } catch {
      // WebSocket not available, fall back to polling only
    }
  }, []);

  const disconnectWS = () => {
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    setWsConnected(false);
  };

  const handleWSMessage = (msg: WSMessage) => {
    const timestamp = new Date(msg.timestamp).toLocaleTimeString();

    switch (msg.type) {
      case "step_start":
        setTerminalLines((prev) => [
          ...prev,
          "",
          `[${timestamp}] ${msg.output}`,
        ]);
        break;
      case "step_output":
        setTerminalLines((prev) => [...prev, msg.output]);
        break;
      case "step_complete":
        setTerminalLines((prev) => [
          ...prev,
          `[${timestamp}] ✓ ${msg.step_name} completed`,
          ...(msg.output ? msg.output.split("\n").slice(0, 10) : []),
        ]);
        break;
      case "step_error":
        setTerminalLines((prev) => [
          ...prev,
          `[${timestamp}] ✗ ${msg.step_name} FAILED`,
          ...(msg.output ? msg.output.split("\n") : []),
          `ERROR: ${msg.error}`,
        ]);
        break;
      case "install_complete":
        setTerminalLines((prev) => [
          ...prev,
          "",
          `[${timestamp}] ========================================`,
          `[${timestamp}] ${msg.output}`,
          `[${timestamp}] ========================================`,
        ]);
        disconnectWS();
        fetchStatus();
        toast.success("Email server installed successfully!");
        break;
      case "install_failed":
        setTerminalLines((prev) => [
          ...prev,
          "",
          `[${timestamp}] ========================================`,
          `[${timestamp}] INSTALLATION FAILED: ${msg.error}`,
          `[${timestamp}] ========================================`,
        ]);
        disconnectWS();
        fetchStatus();
        toast.error("Email server installation failed");
        break;
    }
  };

  const fetchStatus = async () => {
    setLoading(true);
    try {
      const res = await api.get("/whm/software/email-status");
      const data = res.data?.data || res.data;
      setStatus(data);
      if (data?.installation) {
        setInstallation(data.installation);
      }
    } catch {
      setStatus(null);
    } finally {
      setLoading(false);
    }
  };

  const startPolling = (installationId: string) => {
    if (pollRef.current) clearInterval(pollRef.current);
    pollRef.current = setInterval(async () => {
      try {
        const res = await api.get(`/whm/software/email-installation/${installationId}`);
        const data = res.data?.data || res.data;
        setInstallation(data);
        if (data.status === "completed" || data.status === "failed") {
          if (pollRef.current) clearInterval(pollRef.current);
          pollRef.current = null;
          fetchStatus();
        }
      } catch {
        // Ignore polling errors
      }
    }, 2000);
  };

  const handleInstall = async () => {
    if (!form.hostname || !form.domain) {
      toast.error("Hostname and domain are required");
      return;
    }

    setInstalling(true);
    setTerminalLines(["$ Starting email server installation..."]);
    try {
      const res = await api.post("/whm/software/install-email", form);
      const data = res.data?.data || res.data;
      toast.success("Installation started!");
      setShowInstallModal(false);

      // Connect WebSocket for live terminal
      connectWS();

      // Start polling for step status
      if (data.installation_id) {
        startPolling(data.installation_id);
      }

      await fetchStatus();
    } catch (err: any) {
      const msg = err.response?.data?.error?.message || "Failed to start installation";
      toast.error(msg);
      setTerminalLines((prev) => [...prev, `ERROR: ${msg}`]);
    } finally {
      setInstalling(false);
    }
  };

  const getStepIcon = (stepStatus: string) => {
    switch (stepStatus) {
      case "completed":
        return <CheckCircle size={16} className="text-green-400" />;
      case "running":
        return <Loader size={16} className="text-blue-400 animate-spin" />;
      case "failed":
        return <XCircle size={16} className="text-red-400" />;
      case "skipped":
        return <Circle size={16} className="text-panel-muted" />;
      default:
        return <Circle size={16} className="text-panel-border" />;
    }
  };

  const getComponentIcon = (name: string) => {
    switch (name) {
      case "Postfix": return <Mail size={20} className="text-blue-400" />;
      case "Dovecot": return <Server size={20} className="text-purple-400" />;
      case "SpamAssassin": return <Shield size={20} className="text-yellow-400" />;
      case "OpenDKIM": return <Key size={20} className="text-green-400" />;
      case "ClamAV": return <Bug size={20} className="text-red-400" />;
      default: return <Mail size={20} className="text-panel-muted" />;
    }
  };

  const progressPercent = installation
    ? Math.round((installation.current_step / installation.total_steps) * 100)
    : 0;

  // Loading state
  if (loading) {
    return (
      <Card>
        <div className="p-6">
          <div className="h-24 bg-panel-border/20 rounded animate-pulse" />
        </div>
      </Card>
    );
  }

  // Installing state - show progress + live terminal
  const isInstalling = status?.status === "installing" || (installation && installation.status === "running");
  if (isInstalling && installation) {
    return (
      <div className="space-y-4">
        <Card>
          <div className="p-6 space-y-4">
            <div className="flex items-center justify-between">
              <div>
                <h2 className="text-lg font-semibold text-panel-text flex items-center gap-2">
                  <Mail size={20} className="text-blue-400" />
                  Email Server Installation
                </h2>
                <p className="text-sm text-panel-muted mt-1">
                  Step {Math.min(installation.current_step + 1, installation.total_steps)} of {installation.total_steps}
                  {installation.steps[installation.current_step] &&
                    ` — ${installation.steps[installation.current_step].description}`}
                </p>
              </div>
              <div className="flex items-center gap-2">
                {wsConnected && (
                  <span className="flex items-center gap-1.5 text-xs text-green-400">
                    <span className="w-1.5 h-1.5 bg-green-400 rounded-full animate-pulse" />
                    Live
                  </span>
                )}
                <StatusBadge status="installing" />
              </div>
            </div>

            {/* Progress bar */}
            <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden">
              <div
                className="h-full bg-blue-500 rounded-full transition-all duration-500"
                style={{ width: `${progressPercent}%` }}
              />
            </div>
            <p className="text-xs text-panel-muted text-right">{progressPercent}% complete</p>

            {/* Step list */}
            <div className="space-y-1">
              {installation.steps.map((step, i) => (
                <div key={step.name}>
                  <button
                    onClick={() => setExpandedStep(expandedStep === i ? null : i)}
                    className="w-full flex items-center gap-3 px-3 py-2 rounded-lg hover:bg-panel-bg/50 transition-colors text-left"
                  >
                    {getStepIcon(step.status)}
                    <span className={`text-sm flex-1 ${
                      step.status === "running" ? "text-blue-400 font-medium" :
                      step.status === "completed" ? "text-panel-text" :
                      step.status === "failed" ? "text-red-400" :
                      "text-panel-muted"
                    }`}>
                      {step.description}
                    </span>
                    {(step.output || step.error) && (
                      expandedStep === i
                        ? <ChevronUp size={14} className="text-panel-muted" />
                        : <ChevronDown size={14} className="text-panel-muted" />
                    )}
                  </button>
                  {expandedStep === i && (step.output || step.error) && (
                    <div className="ml-8 mb-2">
                      <pre className="p-3 bg-panel-bg rounded-lg text-xs text-panel-muted font-mono overflow-x-auto max-h-40 overflow-y-auto whitespace-pre-wrap">
                        {step.output}
                        {step.error && (
                          <span className="text-red-400">{"\n"}Error: {step.error}</span>
                        )}
                      </pre>
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        </Card>

        {/* Live Terminal Output */}
        <Card>
          <div className="p-4">
            <div className="flex items-center gap-2 mb-3">
              <Terminal size={16} className="text-green-400" />
              <h3 className="text-sm font-medium text-panel-text">Terminal Output</h3>
              {wsConnected && (
                <span className="flex items-center gap-1 ml-auto text-xs text-green-400">
                  <span className="w-1.5 h-1.5 bg-green-400 rounded-full animate-pulse" />
                  Connected
                </span>
              )}
            </div>
            <div
              ref={terminalRef}
              className="bg-[#0d1117] border border-panel-border rounded-lg p-4 font-mono text-xs text-green-400 overflow-y-auto max-h-80 min-h-[200px]"
            >
              {terminalLines.map((line, i) => (
                <div
                  key={i}
                  className={`leading-5 ${
                    line.startsWith("ERROR") || line.startsWith("[") && line.includes("FAILED")
                      ? "text-red-400"
                      : line.startsWith(">>>")
                      ? "text-blue-400 font-semibold"
                      : line.includes("✓")
                      ? "text-green-400"
                      : line.startsWith("$")
                      ? "text-yellow-400"
                      : line.startsWith("[") && line.includes("====")
                      ? "text-cyan-400 font-semibold"
                      : "text-gray-400"
                  }`}
                >
                  {line || "\u00A0"}
                </div>
              ))}
              <div className="flex items-center gap-1 text-green-400 mt-1">
                <span className="animate-pulse">_</span>
              </div>
            </div>
          </div>
        </Card>
      </div>
    );
  }

  // Installed state
  if (status?.installed && status?.config) {
    return (
      <>
        <Card>
          <div className="p-6 space-y-4">
            <div className="flex items-center justify-between">
              <div>
                <h2 className="text-lg font-semibold text-panel-text flex items-center gap-2">
                  <Mail size={20} className="text-blue-400" />
                  Email Server
                </h2>
                <p className="text-sm text-panel-muted mt-1">
                  {status.config.hostname} &middot; {status.config.domain}
                </p>
              </div>
              <div className="flex items-center gap-2">
                <Button
                  onClick={fetchStatus}
                  className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
                >
                  <RefreshCw size={14} />
                  Refresh
                </Button>
                <Button
                  onClick={() => setShowSettingsModal(true)}
                  className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
                >
                  <Settings size={14} />
                  Settings
                </Button>
              </div>
            </div>

            {/* Component status grid */}
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3">
              {status.components.map((comp) => (
                <div
                  key={comp.name}
                  className="flex items-center gap-3 p-3 rounded-lg bg-panel-bg border border-panel-border"
                >
                  {getComponentIcon(comp.name)}
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium text-panel-text truncate">{comp.name}</p>
                    <p className="text-xs text-panel-muted">
                      {comp.installed ? (comp.version || "Installed") : "Not installed"}
                    </p>
                  </div>
                  <div className={`w-2 h-2 rounded-full ${
                    comp.running ? "bg-green-400" :
                    comp.installed ? "bg-yellow-400" :
                    comp.enabled ? "bg-panel-muted" :
                    "bg-panel-border"
                  }`} />
                </div>
              ))}
            </div>
          </div>
        </Card>

        {showSettingsModal && status.config && (
          <EmailServerSettings
            config={status.config}
            onClose={() => setShowSettingsModal(false)}
            onSaved={() => {
              setShowSettingsModal(false);
              fetchStatus();
            }}
          />
        )}
      </>
    );
  }

  // Failed state
  if (status?.status === "failed") {
    return (
      <Card>
        <div className="p-6">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-lg font-semibold text-panel-text flex items-center gap-2">
                <Mail size={20} className="text-red-400" />
                Email Server
              </h2>
              <p className="text-sm text-red-400 mt-1">
                Installation failed. You can retry the installation.
              </p>
            </div>
            <Button
              onClick={() => setShowInstallModal(true)}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
            >
              <Play size={14} />
              Retry Installation
            </Button>
          </div>

          {installation?.error_message && (
            <div className="mt-4 p-3 bg-red-500/10 border border-red-500/20 rounded-lg">
              <p className="text-sm text-red-400">{installation.error_message}</p>
            </div>
          )}
        </div>
      </Card>
    );
  }

  // Not installed state (default)
  return (
    <>
      <Card>
        <div className="p-6">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-4">
              <div className="p-3 rounded-xl bg-blue-500/10">
                <Mail size={24} className="text-blue-400" />
              </div>
              <div>
                <h2 className="text-lg font-semibold text-panel-text">Email Server</h2>
                <p className="text-sm text-panel-muted mt-0.5">
                  Install a complete email server stack with Postfix, Dovecot, and optional spam/virus protection
                </p>
              </div>
            </div>
            <div className="flex items-center gap-3">
              <StatusBadge status="not_installed" />
              <Button
                onClick={() => setShowInstallModal(true)}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Play size={14} />
                Install Email Server
              </Button>
            </div>
          </div>

          {/* Feature highlights */}
          <div className="mt-4 grid grid-cols-2 lg:grid-cols-5 gap-3">
            {[
              { icon: <Mail size={16} />, label: "Postfix SMTP", desc: "Send & receive email", required: true },
              { icon: <Server size={16} />, label: "Dovecot IMAP", desc: "Mail access protocol", required: true },
              { icon: <Shield size={16} />, label: "SpamAssassin", desc: "Spam filtering", required: false },
              { icon: <Key size={16} />, label: "OpenDKIM", desc: "Email authentication", required: false },
              { icon: <Bug size={16} />, label: "ClamAV", desc: "Virus scanning", required: false },
            ].map((item) => (
              <div key={item.label} className="flex items-center gap-2 p-2 rounded-lg bg-panel-bg/50">
                <span className="text-panel-muted">{item.icon}</span>
                <div>
                  <p className="text-xs font-medium text-panel-text">{item.label}</p>
                  <p className="text-xs text-panel-muted">{item.desc}</p>
                </div>
              </div>
            ))}
          </div>
        </div>
      </Card>

      {/* Install Configuration Modal */}
      <Modal
        isOpen={showInstallModal}
        onClose={() => setShowInstallModal(false)}
        title="Install Email Server"
        size="lg"
      >
        <div className="space-y-5">
          <p className="text-sm text-panel-muted">
            Configure and install a complete email server stack. Postfix and Dovecot are required core components.
            Optional components can be enabled or disabled.
          </p>

          {/* Hostname & Domain */}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Mail Hostname <span className="text-red-400">*</span>
              </label>
              <input
                type="text"
                value={form.hostname}
                onChange={(e) => setForm({ ...form, hostname: e.target.value })}
                placeholder="mail.example.com"
                className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Domain <span className="text-red-400">*</span>
              </label>
              <input
                type="text"
                value={form.domain}
                onChange={(e) => setForm({ ...form, domain: e.target.value })}
                placeholder="example.com"
                className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
              />
            </div>
          </div>

          {/* Core components (always enabled) */}
          <div>
            <h3 className="text-sm font-medium text-panel-text mb-2">Core Components (Required)</h3>
            <div className="space-y-2">
              {[
                { icon: <Mail size={16} className="text-blue-400" />, label: "Postfix SMTP Server", desc: "Handles sending and receiving email" },
                { icon: <Server size={16} className="text-purple-400" />, label: "Dovecot IMAP/POP3", desc: "Email access via IMAP and POP3 protocols" },
              ].map((item) => (
                <div key={item.label} className="flex items-center gap-3 p-3 rounded-lg bg-panel-bg border border-panel-border">
                  {item.icon}
                  <div className="flex-1">
                    <p className="text-sm font-medium text-panel-text">{item.label}</p>
                    <p className="text-xs text-panel-muted">{item.desc}</p>
                  </div>
                  <div className="px-2 py-0.5 bg-blue-500/10 text-blue-400 text-xs rounded">Required</div>
                </div>
              ))}
            </div>
          </div>

          {/* Optional components with toggles */}
          <div>
            <h3 className="text-sm font-medium text-panel-text mb-2">Optional Components</h3>
            <div className="space-y-2">
              {[
                {
                  key: "spamassassin_enabled" as const,
                  icon: <Shield size={16} className="text-yellow-400" />,
                  label: "SpamAssassin",
                  desc: "Filter incoming spam emails with Bayesian scoring",
                },
                {
                  key: "opendkim_enabled" as const,
                  icon: <Key size={16} className="text-green-400" />,
                  label: "OpenDKIM",
                  desc: "Sign outgoing emails with DKIM for better deliverability",
                },
                {
                  key: "clamav_enabled" as const,
                  icon: <Bug size={16} className="text-red-400" />,
                  label: "ClamAV Antivirus",
                  desc: "Scan emails for viruses and malware (uses more RAM)",
                },
              ].map((item) => (
                <div key={item.key} className="flex items-center gap-3 p-3 rounded-lg bg-panel-bg border border-panel-border">
                  {item.icon}
                  <div className="flex-1">
                    <p className="text-sm font-medium text-panel-text">{item.label}</p>
                    <p className="text-xs text-panel-muted">{item.desc}</p>
                  </div>
                  <button
                    onClick={() => setForm({ ...form, [item.key]: !form[item.key] })}
                    className={`relative w-10 h-5 rounded-full transition-colors ${
                      form[item.key] ? "bg-blue-500" : "bg-panel-border"
                    }`}
                  >
                    <span
                      className={`absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-white transition-transform ${
                        form[item.key] ? "translate-x-5" : "translate-x-0"
                      }`}
                    />
                  </button>
                </div>
              ))}
            </div>
          </div>

          {/* Actions */}
          <div className="flex justify-end gap-3 pt-2 border-t border-panel-border">
            <Button
              onClick={() => setShowInstallModal(false)}
              className="px-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
            >
              Cancel
            </Button>
            <Button
              onClick={handleInstall}
              disabled={installing || !form.hostname || !form.domain}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:bg-blue-600/50 disabled:cursor-not-allowed text-white rounded-lg text-sm font-medium transition-colors"
            >
              {installing ? (
                <>
                  <Loader size={14} className="animate-spin" />
                  Installing...
                </>
              ) : (
                <>
                  <Play size={14} />
                  Install Email Server
                </>
              )}
            </Button>
          </div>
        </div>
      </Modal>
    </>
  );
}
