import { useEffect, useState } from "react";
import { Button, Card } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Server, RefreshCw, Check, AlertTriangle } from "lucide-react";

export default function ChangeHostnamePage() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const res = await api.get("/monitor/system");
      const host = res.data?.data?.hostname || "";
      setCurrent(host);
      setNext(host);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to load hostname");
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => { void load(); }, []);

  const save = async () => {
    const v = next.trim().toLowerCase();
    if (!v || !/^[a-z]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/.test(v)) {
      toast.error("Enter a valid FQDN (e.g. srv1.example.com)");
      return;
    }
    setSaving(true);
    try {
      await api.put("/config/hostname", { hostname: v });
      toast.success("Hostname updated");
      setCurrent(v);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to update hostname");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
          <Server size={20} /> Change Hostname
        </h1>
        <p className="text-panel-muted text-sm mt-1">
          The hostname appears in outgoing mail headers and cPanel license
          checks. Use a FQDN that will NOT be a customer domain.
        </p>
      </div>

      <Card>
        <div className="p-5 space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1">Current Hostname</label>
            <div className="px-3 py-2 rounded-lg bg-panel-bg border border-panel-border text-sm font-mono text-panel-text">
              {loading ? "…" : (current || "unknown")}
            </div>
          </div>

          <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-xs text-amber-200/90 flex gap-2">
            <AlertTriangle size={14} className="mt-0.5 shrink-0" />
            <span>
              Changing hostname restarts hostnamectl + rewrites /etc/hosts.
              Avoid a potential service subdomain (e.g. <code>cpanel.example.com</code>)
              — hostnames should not match any customer site.
            </span>
          </div>

          <div>
            <label className="block text-sm font-medium text-panel-text mb-1">New Hostname</label>
            <input
              type="text"
              value={next}
              onChange={(e) => setNext(e.target.value)}
              placeholder="srv1.example.com"
              className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500"
            />
          </div>

          <div className="flex items-center gap-2">
            <Button onClick={save} loading={saving} disabled={loading || !next.trim() || next.trim() === current}>
              <Check size={16} /> Update Hostname
            </Button>
            <Button variant="ghost" onClick={load} disabled={loading || saving}>
              <RefreshCw size={16} className={loading ? "animate-spin" : ""} /> Refresh
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}
