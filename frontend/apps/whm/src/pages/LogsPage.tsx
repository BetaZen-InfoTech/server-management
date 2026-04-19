import { useState, useEffect, useMemo } from "react";
import { Card, Button, CodeBlock } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { FileText, RefreshCw, Download, Filter } from "lucide-react";
import { useAuthStore } from "@/store/auth";

type LogType = "all" | "nginx-access" | "nginx-error" | "app" | "system" | "auth" | "mail" | "mongodb";

export default function LogsPage() {
  const authUser = useAuthStore((s) => s.user);
  // Only vendor_owner sees server-wide logs (system / auth / mail / mongodb /
  // the serverpanel application journal). Tenants — vendor_admin, vendor_staff,
  // developer, support — get just nginx access + error tabs, filtered server-
  // side to their own domains. Backend enforces the same rule in log_service.go,
  // so even a crafted URL that picks a hidden slug returns an empty payload.
  const isOwner = authUser?.role === "vendor_owner";

  const [logs, setLogs] = useState<string>("");
  const [loading, setLoading] = useState(true);
  // Default to "all" — operators usually want the overview first.
  const [logType, setLogType] = useState<LogType>("all");
  const [lines, setLines] = useState(100);

  const logTypes = useMemo<{ value: LogType; label: string }[]>(() => {
    const base: { value: LogType; label: string }[] = [
      { value: "all", label: "All" },
      { value: "nginx-access", label: "Nginx Access" },
      { value: "nginx-error", label: "Nginx Error" },
    ];
    if (isOwner) {
      base.push(
        { value: "app", label: "Application" },
        { value: "system", label: "System" },
        { value: "auth", label: "Authentication" },
        { value: "mail", label: "Mail" },
        { value: "mongodb", label: "MongoDB" },
      );
    }
    return base;
  }, [isOwner]);

  // If the operator was viewing a now-hidden tab (e.g. the role changed,
  // or they navigated in with a stale `?type=system`), snap back to "all".
  useEffect(() => {
    if (!logTypes.find((t) => t.value === logType)) {
      setLogType("all");
    }
  }, [logTypes, logType]);

  useEffect(() => {
    fetchLogs();
  }, [logType, lines]);

  const fetchLogs = async () => {
    setLoading(true);
    try {
      const res = await api.get(`/logs/${logType}`, { params: { lines } });
      setLogs(res.data.data?.content || "");
    } catch {
      setLogs("");
    } finally {
      setLoading(false);
    }
  };

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
