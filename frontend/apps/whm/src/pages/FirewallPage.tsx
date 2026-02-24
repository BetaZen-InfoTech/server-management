import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Flame, Plus, RefreshCw, Trash2, Shield, ShieldOff, Lock, Unlock } from "lucide-react";

interface FirewallRule {
  id: string;
  type: "allow" | "block";
  source: string;
  port: string;
  protocol: string;
  description: string;
  status: "active" | "inactive";
}

interface BlockedIp {
  id: string;
  ip: string;
  reason: string;
  blockedAt: string;
}

export default function FirewallPage() {
  const [rules, setRules] = useState<FirewallRule[]>([]);
  const [blockedIps, setBlockedIps] = useState<BlockedIp[]>([]);
  const [loading, setLoading] = useState(true);
  const [firewallEnabled, setFirewallEnabled] = useState(true);

  useEffect(() => {
    fetchFirewallData();
  }, []);

  const fetchFirewallData = async () => {
    setLoading(true);
    try {
      const [rulesRes, ipsRes, statusRes] = await Promise.allSettled([
        api.get("/firewall/rules"),
        api.get("/firewall/blocked-ips"),
        api.get("/firewall/status"),
      ]);

      if (rulesRes.status === "fulfilled") setRules(rulesRes.value.data.data || []);
      if (ipsRes.status === "fulfilled") setBlockedIps(ipsRes.value.data.data || []);
      if (statusRes.status === "fulfilled") setFirewallEnabled(statusRes.value.data.data?.enabled ?? true);
    } catch {
      // Keep defaults
    } finally {
      setLoading(false);
    }
  };

  const toggleFirewall = async () => {
    try {
      await api.post("/firewall/toggle", { enabled: !firewallEnabled });
      setFirewallEnabled(!firewallEnabled);
      toast.success(`Firewall ${!firewallEnabled ? "enabled" : "disabled"}`);
    } catch {
      toast.error("Failed to toggle firewall");
    }
  };

  const handleDeleteRule = async (id: string) => {
    try {
      await api.delete(`/firewall/rules/${id}`);
      toast.success("Rule deleted");
      fetchFirewallData();
    } catch {
      toast.error("Failed to delete rule");
    }
  };

  const handleUnblock = async (id: string, ip: string) => {
    try {
      await api.delete(`/firewall/blocked-ips/${id}`);
      toast.success(`IP ${ip} unblocked`);
      fetchFirewallData();
    } catch {
      toast.error("Failed to unblock IP");
    }
  };

  const ruleColumns = [
    {
      header: "Type",
      accessor: (r: FirewallRule) => (
        <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium ${
          r.type === "allow" ? "bg-green-500/10 text-green-400" : "bg-red-500/10 text-red-400"
        }`}>
          {r.type === "allow" ? <Unlock size={10} /> : <Lock size={10} />}
          {r.type.toUpperCase()}
        </span>
      ),
    },
    {
      header: "Source",
      accessor: (r: FirewallRule) => (
        <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">{r.source}</code>
      ),
    },
    {
      header: "Port",
      accessor: (r: FirewallRule) => (
        <span className="text-panel-muted">{r.port}</span>
      ),
    },
    {
      header: "Protocol",
      accessor: (r: FirewallRule) => (
        <span className="text-panel-muted uppercase text-xs">{r.protocol}</span>
      ),
    },
    {
      header: "Description",
      accessor: (r: FirewallRule) => (
        <span className="text-panel-muted text-sm">{r.description}</span>
      ),
    },
    {
      header: "Status",
      accessor: (r: FirewallRule) => <StatusBadge status={r.status} />,
    },
    {
      header: "",
      accessor: (r: FirewallRule) => (
        <button
          onClick={() => handleDeleteRule(r.id)}
          className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
        >
          <Trash2 size={14} />
        </button>
      ),
    },
  ];

  const blockedIpColumns = [
    {
      header: "IP Address",
      accessor: (b: BlockedIp) => (
        <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-red-400 font-mono">{b.ip}</code>
      ),
    },
    {
      header: "Reason",
      accessor: (b: BlockedIp) => (
        <span className="text-panel-muted text-sm">{b.reason}</span>
      ),
    },
    {
      header: "Blocked At",
      accessor: (b: BlockedIp) => (
        <span className="text-panel-muted text-sm">{b.blockedAt}</span>
      ),
    },
    {
      header: "",
      accessor: (b: BlockedIp) => (
        <button
          onClick={() => handleUnblock(b.id, b.ip)}
          className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
          title="Unblock"
        >
          <Unlock size={14} />
        </button>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Firewall</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage firewall rules and blocked IPs
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchFirewallData}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => toast("Add Rule modal coming soon")}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Add Rule
          </Button>
        </div>
      </div>

      {/* Firewall Status Card */}
      <Card>
        <div className="p-5 flex items-center justify-between">
          <div className="flex items-center gap-4">
            <div className={`p-3 rounded-xl ${firewallEnabled ? "bg-green-500/10" : "bg-red-500/10"}`}>
              {firewallEnabled ? (
                <Shield size={24} className="text-green-400" />
              ) : (
                <ShieldOff size={24} className="text-red-400" />
              )}
            </div>
            <div>
              <h3 className="text-lg font-semibold text-panel-text">
                Firewall is {firewallEnabled ? "Active" : "Disabled"}
              </h3>
              <p className="text-sm text-panel-muted">
                {firewallEnabled
                  ? "Your server is protected by the firewall"
                  : "Warning: Your server is not protected by the firewall"}
              </p>
            </div>
          </div>
          <Button
            onClick={toggleFirewall}
            className={`px-4 py-2 rounded-lg text-sm font-medium transition-colors ${
              firewallEnabled
                ? "bg-red-600/10 text-red-400 hover:bg-red-600/20 border border-red-600/20"
                : "bg-green-600 text-white hover:bg-green-700"
            }`}
          >
            {firewallEnabled ? "Disable Firewall" : "Enable Firewall"}
          </Button>
        </div>
      </Card>

      {/* Allowed Ports Summary */}
      <Card>
        <div className="p-5">
          <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider mb-3">
            Common Allowed Ports
          </h3>
          <div className="flex flex-wrap gap-2">
            {[
              { port: "22", label: "SSH" },
              { port: "80", label: "HTTP" },
              { port: "443", label: "HTTPS" },
              { port: "3000", label: "Dev" },
              { port: "8080", label: "API" },
              { port: "27017", label: "MongoDB" },
            ].map((p) => (
              <span
                key={p.port}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-panel-bg border border-panel-border text-sm"
              >
                <span className="text-green-400 font-mono text-xs">{p.port}</span>
                <span className="text-panel-muted">{p.label}</span>
              </span>
            ))}
          </div>
        </div>
      </Card>

      {/* Firewall Rules */}
      <Card>
        <div className="p-5 border-b border-panel-border">
          <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
            Firewall Rules ({rules.length})
          </h3>
        </div>
        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2, 3].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : rules.length > 0 ? (
          <Table columns={ruleColumns} data={rules} />
        ) : (
          <div className="text-center py-12 px-4">
            <Flame size={36} className="text-panel-muted/20 mx-auto mb-3" />
            <p className="text-panel-muted text-sm">No custom firewall rules configured</p>
          </div>
        )}
      </Card>

      {/* Blocked IPs */}
      <Card>
        <div className="p-5 border-b border-panel-border">
          <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
            Blocked IPs ({blockedIps.length})
          </h3>
        </div>
        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : blockedIps.length > 0 ? (
          <Table columns={blockedIpColumns} data={blockedIps} />
        ) : (
          <div className="text-center py-12 px-4">
            <Shield size={36} className="text-panel-muted/20 mx-auto mb-3" />
            <p className="text-panel-muted text-sm">No blocked IP addresses</p>
          </div>
        )}
      </Card>
    </div>
  );
}
