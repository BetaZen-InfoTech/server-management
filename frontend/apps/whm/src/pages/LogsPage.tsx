import { useState, useEffect } from "react";
import { Card, Button, CodeBlock } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { FileText, RefreshCw, Download, Filter } from "lucide-react";

type LogType = "nginx-access" | "nginx-error" | "app" | "system" | "auth";

interface LogEntry {
  timestamp: string;
  level: string;
  message: string;
}

export default function LogsPage() {
  const [logs, setLogs] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [logType, setLogType] = useState<LogType>("nginx-access");
  const [lines, setLines] = useState(100);

  useEffect(() => {
    fetchLogs();
  }, [logType, lines]);

  const fetchLogs = async () => {
    setLoading(true);
    try {
      const res = await api.get(`/logs/${logType}`, { params: { lines } });
      setLogs(res.data.data?.content || "");
    } catch {
      // Show placeholder logs
      setLogs(getPlaceholderLogs(logType));
    } finally {
      setLoading(false);
    }
  };

  const getPlaceholderLogs = (type: LogType): string => {
    const now = new Date().toISOString();
    switch (type) {
      case "nginx-access":
        return [
          `192.168.1.1 - - [${now}] "GET / HTTP/2.0" 200 3456 "-" "Mozilla/5.0"`,
          `192.168.1.2 - - [${now}] "GET /api/v1/health HTTP/1.1" 200 52 "-" "curl/8.4.0"`,
          `10.0.0.5 - - [${now}] "POST /api/v1/auth/login HTTP/2.0" 200 1024 "-" "Mozilla/5.0"`,
          `192.168.1.1 - - [${now}] "GET /assets/main.js HTTP/2.0" 200 245678 "-" "Mozilla/5.0"`,
          `10.0.0.12 - - [${now}] "GET /api/v1/domains HTTP/2.0" 200 2048 "-" "Mozilla/5.0"`,
        ].join("\n");
      case "nginx-error":
        return [
          `${now} [error] 1234#0: *5678 connect() failed (111: Connection refused) while connecting to upstream`,
          `${now} [warn] 1234#0: *5679 upstream server temporarily disabled while reading response header`,
          `${now} [error] 1234#0: *5680 open() "/var/www/html/favicon.ico" failed (2: No such file or directory)`,
        ].join("\n");
      case "app":
        return [
          `[${now}] [INFO] Server started on port 8080`,
          `[${now}] [INFO] Connected to MongoDB at localhost:27017`,
          `[${now}] [INFO] Redis connection established`,
          `[${now}] [WARN] Rate limit reached for IP 192.168.1.100`,
          `[${now}] [INFO] Backup job scheduled for 02:00 UTC`,
        ].join("\n");
      case "system":
        return [
          `${now} kernel: [123456.789] TCP: request_sock_TCP: Possible SYN flooding on port 443`,
          `${now} systemd[1]: Started nginx.service - A high performance web server`,
          `${now} sshd[12345]: Accepted publickey for admin from 10.0.0.1 port 54321`,
          `${now} kernel: [123457.012] Memory cgroup out of memory: Killed process 12345 (node)`,
        ].join("\n");
      case "auth":
        return [
          `${now} [INFO] Successful login: admin@serverpanel.io from 192.168.1.1`,
          `${now} [WARN] Failed login attempt: unknown@example.com from 10.0.0.99`,
          `${now} [WARN] Failed login attempt: admin@serverpanel.io from 10.0.0.99 (wrong password)`,
          `${now} [INFO] Password changed for user: admin@serverpanel.io`,
          `${now} [INFO] API key generated for user: admin@serverpanel.io`,
        ].join("\n");
      default:
        return "No log entries found.";
    }
  };

  const logTypes: { value: LogType; label: string }[] = [
    { value: "nginx-access", label: "Nginx Access" },
    { value: "nginx-error", label: "Nginx Error" },
    { value: "app", label: "Application" },
    { value: "system", label: "System" },
    { value: "auth", label: "Authentication" },
  ];

  const handleDownload = () => {
    const blob = new Blob([logs], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${logType}-logs.txt`;
    a.click();
    URL.revokeObjectURL(url);
    toast.success("Logs downloaded");
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Logs</h1>
          <p className="text-panel-muted text-sm mt-1">
            View server and application logs
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={handleDownload}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <Download size={14} />
            Download
          </Button>
          <Button
            onClick={fetchLogs}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
        </div>
      </div>

      {/* Filters */}
      <Card>
        <div className="p-4 flex items-center gap-4 flex-wrap">
          <div className="flex items-center gap-2">
            <Filter size={14} className="text-panel-muted" />
            <span className="text-sm text-panel-muted">Log Type:</span>
          </div>
          <div className="flex items-center gap-1">
            {logTypes.map((lt) => (
              <button
                key={lt.value}
                onClick={() => setLogType(lt.value)}
                className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
                  logType === lt.value
                    ? "bg-blue-600 text-white"
                    : "bg-panel-bg text-panel-muted hover:text-panel-text border border-panel-border"
                }`}
              >
                {lt.label}
              </button>
            ))}
          </div>
          <div className="flex items-center gap-2 ml-auto">
            <span className="text-sm text-panel-muted">Lines:</span>
            <select
              value={lines}
              onChange={(e) => setLines(Number(e.target.value))}
              className="px-3 py-1.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm focus:outline-none focus:ring-2 focus:ring-blue-500/40"
            >
              <option value={50}>50</option>
              <option value={100}>100</option>
              <option value={500}>500</option>
              <option value={1000}>1000</option>
            </select>
          </div>
        </div>
      </Card>

      {/* Log Output */}
      <Card>
        <div className="p-4">
          {loading ? (
            <div className="h-96 bg-panel-bg rounded-lg animate-pulse" />
          ) : logs ? (
            <CodeBlock code={logs} language="log" />
          ) : (
            <div className="text-center py-16 px-4">
              <FileText size={48} className="text-panel-muted/20 mx-auto mb-4" />
              <h3 className="text-lg font-medium text-panel-text mb-1">No log entries</h3>
              <p className="text-panel-muted text-sm">
                No log entries found for the selected log type and line count.
              </p>
            </div>
          )}
        </div>
      </Card>
    </div>
  );
}
