import { useEffect, useState } from "react";
import { Card, Button, Table } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  BarChart3, Globe, Link2, Network, RefreshCw, Loader2,
} from "lucide-react";

// Wire types match the backend's TrafficStats envelope. Server-wide
// reports populate `domains`; per-domain reports leave it empty (the
// frontend hides the domains tab in that mode).
interface IPStat { ip: string; requests: number; bytes: number }
interface URLStat { url: string; requests: number; bytes: number }
interface DomainStat { domain: string; requests: number; bytes: number }
interface TrafficStats {
  domain: string;
  log_file: string;
  total_requests: number;
  total_bytes: number;
  top_ips: IPStat[];
  top_urls: URLStat[];
  domains: DomainStat[];
}

interface DomainOption {
  id: string;
  domain: string;
}

type Tab = "ips" | "urls" | "domains";

// Compact byte formatter so the table cells fit (e.g. "1.2 MB" /
// "456 KB"). Mirrors what the dashboard uses elsewhere — we keep a
// local copy to avoid pulling a util lib for one helper.
function bytesHuman(n: number): string {
  if (!n) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(n) / Math.log(k)), sizes.length - 1);
  return (n / Math.pow(k, i)).toFixed(i === 0 ? 0 : 1) + " " + sizes[i];
}

// numberHuman formats request counts with thousands separators so big
// totals (1234567) read as 1,234,567 instead of a wall of digits.
function numberHuman(n: number): string {
  return (n || 0).toLocaleString();
}

export default function ReportsPage() {
  const [stats, setStats] = useState<TrafficStats | null>(null);
  const [domains, setDomains] = useState<DomainOption[]>([]);
  const [domain, setDomain] = useState("");
  const [tab, setTab] = useState<Tab>("ips");
  const [loading, setLoading] = useState(false);

  const fetchDomains = async () => {
    try {
      const res = await api.get("/domains?limit=500");
      setDomains(res.data?.data || []);
    } catch {
      // empty list is fine — operators can still pull the server-wide
      // report from the default "(all)" option.
    }
  };

  // fetchStats hits the backend traffic-stats endpoint with the
  // current domain selection. Empty domain → server-wide report.
  // Errors surface as a toast; loading flips off either way so the
  // refresh button isn't permanently stuck.
  const fetchStats = async (d: string) => {
    setLoading(true);
    try {
      const res = await api.get("/resources/traffic-stats", {
        params: d ? { domain: d } : {},
      });
      setStats(res.data?.data || null);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to load reports");
      setStats(null);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchDomains();
    fetchStats("");
  }, []);

  const ipColumns = [
    { header: "IP Address", accessor: (r: IPStat) => <code className="text-xs text-panel-text">{r.ip}</code> },
    { header: "Requests", accessor: (r: IPStat) => <span className="text-panel-text">{numberHuman(r.requests)}</span> },
    { header: "Bytes Sent", accessor: (r: IPStat) => <span className="text-panel-muted text-sm">{bytesHuman(r.bytes)}</span> },
  ];
  const urlColumns = [
    { header: "URL Path", accessor: (r: URLStat) => <code className="text-xs text-panel-text break-all">{r.url || "—"}</code> },
    { header: "Hits", accessor: (r: URLStat) => <span className="text-panel-text">{numberHuman(r.requests)}</span> },
    { header: "Bytes", accessor: (r: URLStat) => <span className="text-panel-muted text-sm">{bytesHuman(r.bytes)}</span> },
  ];
  const domainColumns = [
    { header: "Domain", accessor: (r: DomainStat) => (
      <button onClick={() => { setDomain(r.domain); fetchStats(r.domain); }}
        className="text-blue-400 hover:underline">{r.domain}</button>
    ) },
    { header: "Requests", accessor: (r: DomainStat) => <span className="text-panel-text">{numberHuman(r.requests)}</span> },
    { header: "Bytes", accessor: (r: DomainStat) => <span className="text-panel-muted text-sm">{bytesHuman(r.bytes)}</span> },
  ];

  const isServerWide = !domain;
  const tabs: { key: Tab; label: string; icon: any }[] = [
    { key: "ips", label: "Top IPs", icon: Network },
    { key: "urls", label: "Top URLs", icon: Link2 },
    ...(isServerWide
      ? [{ key: "domains" as Tab, label: "Per-Domain", icon: Globe }]
      : []),
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <BarChart3 size={20} className="text-blue-400" />
            <h1 className="text-xl font-bold text-panel-text">Reports</h1>
          </div>
          <p className="text-panel-muted text-sm mt-1">
            Top IP addresses, URL paths, and per-domain traffic — sourced from nginx access logs on this server.
          </p>
        </div>
        <Button
          onClick={() => fetchStats(domain)}
          disabled={loading}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border hover:border-blue-500/40 rounded-lg text-panel-text text-sm transition-colors disabled:opacity-50"
        >
          {loading ? <Loader2 size={14} className="animate-spin" /> : <RefreshCw size={14} />}
          Refresh
        </Button>
      </div>

      {/* Scope: domain dropdown. (all) is the unified server view that
          includes the per-domain breakdown. Picking a specific domain
          drills in — top IPs / URLs are then for that one site only. */}
      <Card>
        <div className="p-4 flex items-center gap-3">
          <span className="text-sm text-panel-muted">Scope</span>
          <select
            value={domain}
            onChange={(e) => { setDomain(e.target.value); fetchStats(e.target.value); }}
            className="px-3 py-1.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm focus:outline-none focus:ring-2 focus:ring-blue-500/40"
          >
            <option value="">(all sites)</option>
            {domains.map((d) => (
              <option key={d.id} value={d.domain}>{d.domain}</option>
            ))}
          </select>
          {stats && (
            <div className="ml-auto flex items-center gap-6 text-sm">
              <span className="text-panel-muted">Requests: <span className="text-panel-text font-mono">{numberHuman(stats.total_requests)}</span></span>
              <span className="text-panel-muted">Bytes: <span className="text-panel-text font-mono">{bytesHuman(stats.total_bytes)}</span></span>
            </div>
          )}
        </div>
      </Card>

      {/* Tabs */}
      <div className="flex gap-2 border-b border-panel-border">
        {tabs.map((t) => {
          const Icon = t.icon;
          return (
            <button
              key={t.key}
              onClick={() => setTab(t.key)}
              className={`flex items-center gap-2 px-4 py-2 text-sm border-b-2 -mb-px transition-colors ${
                tab === t.key
                  ? "border-blue-500 text-blue-400"
                  : "border-transparent text-panel-muted hover:text-panel-text"
              }`}
            >
              <Icon size={14} />
              {t.label}
            </button>
          );
        })}
      </div>

      {/* Empty state — covers fresh install where nginx logs are empty
          and the operator-already-saw-this case where the domain has
          no traffic. */}
      {!loading && (!stats || (stats.total_requests === 0 && (stats.top_ips || []).length === 0)) && (
        <Card>
          <div className="p-8 text-center text-panel-muted">
            No traffic data found in {stats?.log_file || "/var/log/nginx/access.log"}.
            <div className="text-xs mt-1">Once requests start hitting nginx, top IPs and URLs will appear here.</div>
          </div>
        </Card>
      )}

      {/* Tab content */}
      {stats && tab === "ips" && stats.top_ips.length > 0 && (
        <Card>
          <Table data={stats.top_ips} columns={ipColumns} />
        </Card>
      )}
      {stats && tab === "urls" && stats.top_urls.length > 0 && (
        <Card>
          <Table data={stats.top_urls} columns={urlColumns} />
        </Card>
      )}
      {stats && tab === "domains" && isServerWide && (stats.domains || []).length > 0 && (
        <Card>
          <Table data={stats.domains} columns={domainColumns} />
        </Card>
      )}
    </div>
  );
}
