import { useEffect, useMemo, useState } from "react";
import { Button, Card, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { useAuthStore } from "@/store/auth";
import { Terminal, RefreshCw, Search, Shield, Lock } from "lucide-react";

interface ShellRow {
  id: string;
  username: string;
  domain: string;
  mode: string;
}

type Mode = "normal" | "jailed" | "disabled";
const MODES: Mode[] = ["normal", "jailed", "disabled"];

// Color the badge the same way WHM does so operators reading both panels
// get the same visual cue. "normal" = permissive (amber), "jailed" =
// restricted-but-usable (blue), "disabled" = no shell (slate).
const modeBadge = (m: string) => {
  switch (m) {
    case "normal":
      return "bg-amber-500/15 text-amber-400 border-amber-500/30";
    case "jailed":
      return "bg-blue-500/15 text-blue-400 border-blue-500/30";
    case "disabled":
      return "bg-slate-500/15 text-slate-300 border-slate-500/30";
    default:
      return "bg-panel-border/30 text-panel-muted border-panel-border";
  }
};

const modeIcon = (m: Mode) => {
  if (m === "normal") return <Terminal size={12} />;
  if (m === "jailed") return <Shield size={12} />;
  return <Lock size={12} />;
};

export default function ShellAccessPage() {
  // Backend already filters the list to the caller's tenant, so there are
  // no admin branches here — a vendor_admin sees only their own team. Any
  // non-privileged role (vendor_staff / customer) will be 403'd by the
  // backend; the empty-state handles that gracefully.
  const permissions = useAuthStore((s) => s.user?.permissions) || [];
  const canManage = permissions.includes("user.create");

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
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    void load();
  }, []);

  // Optimistic update with revert on error — keeps the radio feeling
  // instant even over a slow connection; if the API fails we snap back
  // and toast the error.
  const setMode = async (r: ShellRow, mode: Mode) => {
    if (r.mode === mode) return;
    const prev = r.mode;
    setUpdating(r.id);
    setRows((rs) => rs.map((x) => (x.id === r.id ? { ...x, mode } : x)));
    try {
      await api.post(`/users/${r.id}/shell-access`, { mode });
      toast.success(`${r.username}: ${mode}`);
    } catch (e: any) {
      setRows((rs) => rs.map((x) => (x.id === r.id ? { ...x, mode: prev } : x)));
      toast.error(e?.response?.data?.error?.message || "Failed to update");
    } finally {
      setUpdating("");
    }
  };

  const applyToAll = async (mode: Mode) => {
    const targets = filtered;
    if (targets.length === 0) {
      toast.error("No users to apply to");
      return;
    }
    const ok = await confirmAction({
      title: "Apply to all users?",
      description: `Set shell to "${mode}" for ${targets.length} listed user${targets.length === 1 ? "" : "s"}?`,
      confirmLabel: `Apply: ${mode}`,
      danger: mode === "disabled",
    });
    if (!ok) return;
    let done = 0;
    for (const r of targets) {
      try {
        await api.post(`/users/${r.id}/shell-access`, { mode });
        done++;
      } catch {
        // keep going; final count reports failures
      }
    }
    toast.success(`Applied to ${done}/${targets.length} users`);
    await load();
  };

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return rows;
    return rows.filter(
      (r) =>
        r.username.toLowerCase().includes(q) ||
        (r.domain || "").toLowerCase().includes(q)
    );
  }, [rows, search]);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
            <Terminal size={20} /> Manage Shell Access
          </h1>
          <p className="text-panel-muted text-sm mt-1">
            Normal gives full <code className="text-panel-text">/bin/bash</code>; Jailed restricts to
            jailshell/rbash; Disabled sets the shell to{" "}
            <code className="text-panel-text">/sbin/nologin</code>.
          </p>
          {!canManage && (
            <p className="text-amber-400/80 text-xs mt-2">
              Only tenant admins can toggle shell access. Requests will be rejected.
            </p>
          )}
        </div>
        <Button
          onClick={load}
          disabled={loading}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
        >
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
          Refresh
        </Button>
      </div>

      <Card>
        <div className="flex flex-wrap items-center gap-3">
          <div className="relative flex-1 min-w-[200px]">
            <Search
              size={16}
              className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
            />
            <input
              type="text"
              placeholder="Search by user or domain..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-xs text-panel-muted">Bulk apply:</span>
            {MODES.map((m) => (
              <Button
                key={m}
                onClick={() => applyToAll(m)}
                disabled={!canManage}
                className="flex items-center gap-1.5 px-3 py-1.5 text-xs bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {modeIcon(m)}
                {m}
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
                <th className="py-2 px-4">Current</th>
                <th className="py-2 px-4 w-[110px] text-center">Normal</th>
                <th className="py-2 px-4 w-[110px] text-center">Jailed</th>
                <th className="py-2 px-4 w-[110px] text-center">Disabled</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={6} className="py-8 text-center text-sm text-panel-muted">
                    Loading…
                  </td>
                </tr>
              ) : filtered.length === 0 ? (
                <tr>
                  <td colSpan={6} className="py-8 text-center text-sm text-panel-muted">
                    {rows.length === 0
                      ? "No linux users."
                      : "No users match your search."}
                  </td>
                </tr>
              ) : (
                filtered.map((r) => (
                  <tr key={r.id} className="border-b border-panel-border/50 hover:bg-panel-bg/40">
                    <td className="py-2 px-4 font-mono text-xs text-panel-text">{r.username}</td>
                    <td className="py-2 px-4 font-mono text-xs text-panel-muted">
                      {r.domain || "—"}
                    </td>
                    <td className="py-2 px-4">
                      <span
                        className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] font-medium border ${modeBadge(r.mode)}`}
                      >
                        {r.mode}
                      </span>
                    </td>
                    {MODES.map((m) => (
                      <td key={m} className="py-2 px-4 text-center">
                        <input
                          type="radio"
                          name={`mode-${r.id}`}
                          checked={r.mode === m}
                          disabled={updating === r.id || !canManage}
                          onChange={() => setMode(r, m)}
                          className="cursor-pointer disabled:cursor-not-allowed"
                        />
                      </td>
                    ))}
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  );
}
