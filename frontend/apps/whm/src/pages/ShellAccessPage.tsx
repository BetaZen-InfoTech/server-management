import { useEffect, useMemo, useState } from "react";
import { Button, Card } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { TerminalSquare, RefreshCw, Search } from "lucide-react";

interface ShellRow { id: string; username: string; domain: string; mode: string; }
type Mode = "normal" | "jailed" | "disabled";
const MODES: Mode[] = ["normal", "jailed", "disabled"];

export default function ShellAccessPage() {
  const [rows, setRows] = useState<ShellRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [updating, setUpdating] = useState<string>(""); // userID being updated

  const load = async () => {
    setLoading(true);
    try {
      const res = await api.get("/users/shell-access");
      setRows(res.data?.data || []);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to load shell access");
    } finally { setLoading(false); }
  };
  useEffect(() => { void load(); }, []);

  const setMode = async (r: ShellRow, mode: Mode) => {
    if (r.mode === mode) return;
    setUpdating(r.id);
    try {
      await api.post(`/users/${r.id}/shell-access`, { mode });
      setRows((rs) => rs.map((x) => x.id === r.id ? { ...x, mode } : x));
      toast.success(`${r.username}: ${mode}`);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to update");
    } finally { setUpdating(""); }
  };

  const applyToAll = async (mode: Mode) => {
    if (!window.confirm(`Set shell to "${mode}" for every listed user?`)) return;
    const targets = filtered;
    let ok = 0;
    for (const r of targets) {
      try { await api.post(`/users/${r.id}/shell-access`, { mode }); ok++; } catch {}
    }
    toast.success(`Applied to ${ok}/${targets.length} users`);
    await load();
  };

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return rows;
    return rows.filter((r) =>
      r.username.toLowerCase().includes(q) || (r.domain || "").toLowerCase().includes(q)
    );
  }, [rows, search]);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
            <TerminalSquare size={20} /> Manage Shell Access
          </h1>
          <p className="text-panel-muted text-sm mt-1">
            Normal gives full <code>/bin/bash</code>; Jailed restricts to
            jailshell/rbash; Disabled sets the shell to <code>/sbin/nologin</code>.
          </p>
        </div>
        <Button variant="ghost" onClick={load} disabled={loading}>
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
        </Button>
      </div>

      <Card>
        <div className="p-4 flex flex-wrap items-center gap-3">
          <div className="relative flex-1 min-w-[200px]">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text" placeholder="Search by user or domain..."
              value={search} onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm"
            />
          </div>
          <div className="flex items-center gap-2">
            {MODES.map((m) => (
              <Button key={m} variant="ghost" onClick={() => applyToAll(m)}>
                Apply all: {m}
              </Button>
            ))}
          </div>
        </div>
      </Card>

      <Card>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-xs uppercase tracking-wider text-panel-muted border-b border-panel-border">
                <th className="py-2 px-4">User</th>
                <th className="py-2 px-4">Domain</th>
                <th className="py-2 px-4 w-[120px] text-center">Normal</th>
                <th className="py-2 px-4 w-[120px] text-center">Jailed</th>
                <th className="py-2 px-4 w-[120px] text-center">Disabled</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={5} className="py-8 text-center text-sm text-panel-muted">Loading…</td></tr>
              ) : filtered.length === 0 ? (
                <tr><td colSpan={5} className="py-8 text-center text-sm text-panel-muted">No linux users.</td></tr>
              ) : filtered.map((r) => (
                <tr key={r.id} className="border-b border-panel-border/50">
                  <td className="py-2 px-4 font-mono text-xs text-panel-text">{r.username}</td>
                  <td className="py-2 px-4 font-mono text-xs text-panel-muted">{r.domain || "—"}</td>
                  {MODES.map((m) => (
                    <td key={m} className="py-2 px-4 text-center">
                      <input
                        type="radio"
                        name={`mode-${r.id}`}
                        checked={r.mode === m}
                        disabled={updating === r.id}
                        onChange={() => setMode(r, m)}
                      />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  );
}
