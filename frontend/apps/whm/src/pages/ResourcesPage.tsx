import { useState, useEffect } from "react";
import { Card, Button } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { HardDrive, RefreshCw, Globe, ArrowUpRight, Database } from "lucide-react";

interface DiskQuota {
  path: string;
  used: number;
  total: number;
  percent: number;
}

interface DomainBandwidth {
  domain: string;
  bytesIn: string;
  bytesOut: string;
  totalTransfer: string;
  percent: number;
}

export default function ResourcesPage() {
  const [diskQuotas, setDiskQuotas] = useState<DiskQuota[]>([]);
  const [bandwidth, setBandwidth] = useState<DomainBandwidth[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetchResources();
  }, []);

  const fetchResources = async () => {
    setLoading(true);
    try {
      const [diskRes, bwRes] = await Promise.allSettled([
        api.get("/resources/summary"),
        api.get("/resources/bandwidth"),
      ]);

      if (diskRes.status === "fulfilled") setDiskQuotas(diskRes.value.data.data || []);
      if (bwRes.status === "fulfilled") setBandwidth(bwRes.value.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const getColorForPercent = (percent: number) => {
    if (percent >= 90) return "bg-red-500";
    if (percent >= 70) return "bg-yellow-500";
    if (percent >= 50) return "bg-blue-500";
    return "bg-green-500";
  };

  const getTextColorForPercent = (percent: number) => {
    if (percent >= 90) return "text-red-400";
    if (percent >= 70) return "text-yellow-400";
    return "text-panel-muted";
  };

  // Placeholder data
  const placeholderDisks: DiskQuota[] = [
    { path: "/", used: 42, total: 100, percent: 42 },
    { path: "/var/www", used: 28, total: 80, percent: 35 },
    { path: "/var/lib/mongodb", used: 12, total: 50, percent: 24 },
    { path: "/var/mail", used: 3, total: 20, percent: 15 },
    { path: "/tmp", used: 1, total: 10, percent: 10 },
  ];

  const placeholderBandwidth: DomainBandwidth[] = [
    { domain: "example.com", bytesIn: "2.4 GB", bytesOut: "12.8 GB", totalTransfer: "15.2 GB", percent: 76 },
    { domain: "api.example.com", bytesIn: "890 MB", bytesOut: "3.2 GB", totalTransfer: "4.1 GB", percent: 41 },
    { domain: "blog.example.com", bytesIn: "340 MB", bytesOut: "1.6 GB", totalTransfer: "1.9 GB", percent: 19 },
    { domain: "staging.example.com", bytesIn: "120 MB", bytesOut: "450 MB", totalTransfer: "570 MB", percent: 6 },
  ];

  const displayDisks = diskQuotas.length > 0 ? diskQuotas : placeholderDisks;
  const displayBandwidth = bandwidth.length > 0 ? bandwidth : placeholderBandwidth;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Resources</h1>
          <p className="text-panel-muted text-sm mt-1">
            Monitor disk usage and bandwidth consumption
          </p>
        </div>
        <Button
          onClick={fetchResources}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
        >
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
          Refresh
        </Button>
      </div>

      {/* Disk Quotas */}
      <div>
        <h2 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-3 flex items-center gap-2">
          <HardDrive size={14} />
          Disk Quotas
        </h2>
        <div className="space-y-3">
          {loading ? (
            [1, 2, 3].map((i) => (
              <Card key={i}>
                <div className="p-4 h-16 bg-panel-border/20 rounded animate-pulse" />
              </Card>
            ))
          ) : (
            displayDisks.map((disk) => (
              <Card key={disk.path}>
                <div className="p-4">
                  <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <Database size={14} className="text-blue-400" />
                      <code className="text-sm font-mono text-panel-text">{disk.path}</code>
                    </div>
                    <span className={`text-sm font-medium ${getTextColorForPercent(disk.percent)}`}>
                      {disk.used} GB / {disk.total} GB ({disk.percent}%)
                    </span>
                  </div>
                  <div className="w-full h-2.5 bg-panel-bg rounded-full overflow-hidden">
                    <div
                      className={`h-full rounded-full transition-all ${getColorForPercent(disk.percent)}`}
                      style={{ width: `${disk.percent}%` }}
                    />
                  </div>
                </div>
              </Card>
            ))
          )}
        </div>
      </div>

      {/* Bandwidth Usage */}
      <div>
        <h2 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-3 flex items-center gap-2">
          <ArrowUpRight size={14} />
          Bandwidth Usage (This Month)
        </h2>
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {loading ? (
            [1, 2, 3, 4].map((i) => (
              <Card key={i}>
                <div className="p-5 h-28 bg-panel-border/20 rounded animate-pulse" />
              </Card>
            ))
          ) : (
            displayBandwidth.map((bw) => (
              <Card key={bw.domain}>
                <div className="p-5">
                  <div className="flex items-center justify-between mb-3">
                    <div className="flex items-center gap-2">
                      <Globe size={14} className="text-cyan-400" />
                      <span className="font-medium text-panel-text">{bw.domain}</span>
                    </div>
                    <span className="text-sm font-medium text-panel-text">
                      {bw.totalTransfer}
                    </span>
                  </div>
                  <div className="w-full h-2 bg-panel-bg rounded-full overflow-hidden mb-3">
                    <div
                      className={`h-full rounded-full transition-all ${getColorForPercent(bw.percent)}`}
                      style={{ width: `${bw.percent}%` }}
                    />
                  </div>
                  <div className="flex items-center justify-between text-xs text-panel-muted">
                    <span>In: {bw.bytesIn}</span>
                    <span>Out: {bw.bytesOut}</span>
                  </div>
                </div>
              </Card>
            ))
          )}
        </div>
      </div>

      {diskQuotas.length === 0 && bandwidth.length === 0 && !loading && (
        <div className="text-center py-2">
          <p className="text-xs text-panel-muted">
            Showing placeholder data. Connect to the API to see actual resource usage.
          </p>
        </div>
      )}
    </div>
  );
}
