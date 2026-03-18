import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal } from "@serverpanel/ui";
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

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

export default function FirewallPage() {
  const [rules, setRules] = useState<FirewallRule[]>([]);
  const [blockedIps, setBlockedIps] = useState<BlockedIp[]>([]);
  const [loading, setLoading] = useState(true);
  const [firewallEnabled, setFirewallEnabled] = useState(true);
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ type: "allow" as "allow" | "block", source: "", port: "", protocol: "tcp", description: "" });

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

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.port || !form.protocol) {
      toast.error("Please fill all required fields");
      return;
    }
    setCreating(true);
    try {
      const endpoint = form.type === "allow" ? "/firewall/allow" : "/firewall/deny";
      await api.post(endpoint, form);
      toast.success("Firewall rule added");
      setShowCreate(false);
      setForm({ type: "allow", source: "", port: "", protocol: "tcp", description: "" });
      fetchFirewallData();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to add rule");
    } finally {
      setCreating(false);
    }
  };

  const toggleFirewall = async () => {
    try {
      toast.success("Firewall status is managed via UFW on the server");
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

  const handleUnblock = async (_id: string, ip: string) => {
    try {
      await api.post("/firewall/unblock-ip", { ip });
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
            onClick={() => setShowCreate(true)}
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

      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Add Firewall Rule">
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className={labelClass}>Action *</label>
            <div className="flex gap-2">
              {(["allow", "block"] as const).map((t) => (
                <button key={t} type="button" onClick={() => setForm({ ...form, type: t })}
                  className={`flex-1 px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
                    form.type === t
                      ? t === "allow" ? "bg-green-600 text-white" : "bg-red-600 text-white"
                      : "bg-panel-bg text-panel-muted border border-panel-border hover:text-panel-text"
                  }`}>
                  {t === "allow" ? "Allow" : "Block"}
                </button>
              ))}
            </div>
          </div>
          <div>
            <label className={labelClass}>Source IP</label>
            <input type="text" placeholder="Any (leave empty for all)" value={form.source}
              onChange={(e) => setForm({ ...form, source: e.target.value })} className={inputClass} />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Port *</label>
              <input type="text" required placeholder="80, 443, 8080-8090" value={form.port}
                onChange={(e) => setForm({ ...form, port: e.target.value })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Protocol *</label>
              <select value={form.protocol} onChange={(e) => setForm({ ...form, protocol: e.target.value })} className={inputClass}>
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
                <option value="both">TCP & UDP</option>
              </select>
            </div>
          </div>
          <div>
            <label className={labelClass}>Description</label>
            <input type="text" placeholder="Optional description" value={form.description}
              onChange={(e) => setForm({ ...form, description: e.target.value })} className={inputClass} />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Adding..." : "Add Rule"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
