import { useEffect, useMemo, useState } from "react";
import { Button, Card } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Gauge, RefreshCw, Search, Check } from "lucide-react";

interface Row {
  id: string;
  domain: string;
  user: string;
  bandwidth_limit_mb: number;
  bandwidth_used_mb: number;
}

function fmt(mb: number): string {
  if (!mb || mb <= 0) return "0 MB";
  if (mb >= 1024) return `${(mb / 1024).toFixed(mb % 1024 ? 1 : 0)} GB`;
  return `${mb} MB`;
}

export default function BandwidthLimitPage() {
  const [rows, setRows] = useState<Row[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState<string>("");

  const load = async () => {
    setLoading(true);
    try {
      const res = await api.get("/bandwidth-limits");
      const data: Row[] = res.data?.data || [];
      setRows(data);
      setDraft(Object.fromEntries(data.map((r) => [r.id, String(r.bandwidth_limit_mb ?? 0)])));
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to load bandwidth limits");
    } finally { setLoading(false); }
  };
  useEffect(() => { void load(); }, []);

  const save = async (r: Row) => {
    const v = Number(draft[r.id] ?? "0");
    if (!Number.isFinite(v) || v < 0) { toast.error("Enter a non-negative MB value (0 = unlimited)"); return; }
    setSaving(r.id);
    try {
      await api.put(`/bandwidth-limits/${r.id}`, { monthly_mb: Math.floor(v) });
      toast.success(`${r.domain}: ${v ? fmt(Math.floor(v)) + "/mo" : "unlimited"}`);
      setRows((rs) => rs.map((x) => x.id === r.id ? { ...x, bandwidth_limit_mb: Math.floor(v) } : x));
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to save");
    } finally { setSaving(""); }
  };

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return rows;
    return rows.filter((r) =>
      r.domain.toLowerCase().includes(q) || (r.user || "").toLowerCase().includes(q)
    );
  }, [rows, search]);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
            <Gauge size={20} /> Limit Bandwidth on an Account
          </h1>
          <p className="text-panel-muted text-sm mt-1">
            Set a monthly bandwidth cap per domain in MB. 0 = unlimited. The
            resource-usage cron suspends domains that exceed their cap.
          </p>
        </div>
        <Button variant="ghost" onClick={load} disabled={loading}>
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
        </Button>
      </div>

      <Card>
        <div className="p-4">
          <div className="relative max-w-md">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text" placeholder="Search by domain or user..."
              value={search} onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm"
            />
          </div>
        </div>
      </Card>

      <Card>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-xs uppercase tracking-wider text-panel-muted border-b border-panel-border">
                <th className="py-2 px-4">Domain</th>
                <th className="py-2 px-4">User</th>
                <th className="py-2 px-4 w-[180px]">Used this month</th>
                <th className="py-2 px-4 w-[200px]">Monthly Limit (MB)</th>
                <th className="py-2 px-4 w-[120px]"></th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={5} className="py-8 text-center text-sm text-panel-muted">Loading…</td></tr>
              ) : filtered.length === 0 ? (
                <tr><td colSpan={5} className="py-8 text-center text-sm text-panel-muted">No domains.</td></tr>
              ) : filtered.map((r) => {
                const cap = r.bandwidth_limit_mb || 0;
                const usedPct = cap > 0 ? Math.min(100, (r.bandwidth_used_mb / cap) * 100) : 0;
                return (
                  <tr key={r.id} className="border-b border-panel-border/50">
                    <td className="py-2 px-4 text-panel-text">{r.domain}</td>
                    <td className="py-2 px-4 font-mono text-xs text-panel-muted">{r.user}</td>
                    <td className="py-2 px-4">
                      <div className="text-xs text-panel-text">{fmt(r.bandwidth_used_mb || 0)}</div>
                      {cap > 0 && (
                        <div className="mt-1 h-1 rounded bg-panel-border/50 overflow-hidden">
                          <div className={`h-full ${usedPct > 90 ? "bg-red-500" : usedPct > 75 ? "bg-amber-500" : "bg-emerald-500"}`}
                            style={{ width: `${usedPct}%` }} />
                        </div>
                      )}
                    </td>
                    <td className="py-2 px-4">
                      <input
                        type="number" min={0}
                        value={draft[r.id] ?? ""}
                        onChange={(e) => setDraft((d) => ({ ...d, [r.id]: e.target.value }))}
                        placeholder="0 = unlimited"
                        className="w-full px-2 py-1.5 bg-panel-bg border border-panel-border rounded text-panel-text text-xs font-mono"
                      />
                    </td>
                    <td className="py-2 px-4">
                      <Button variant="ghost" onClick={() => save(r)} loading={saving === r.id}>
                        <Check size={14} /> Save
                      </Button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  );
}
