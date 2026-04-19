import { useEffect, useState } from "react";
import { Button, Card } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Database, Wrench, RefreshCw } from "lucide-react";

export default function RepairDatabasesPage() {
  const [dbs, setDbs] = useState<string[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [running, setRunning] = useState(false);
  const [output, setOutput] = useState<string>("");

  const load = async () => {
    setLoading(true);
    try {
      const res = await api.get("/config/mysql/databases");
      const rows: string[] = res.data?.data || [];
      setDbs(rows);
      if (rows.length && !selected) setSelected(rows[0]);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to load databases");
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => { void load(); }, []);

  const run = async () => {
    if (!selected) return;
    setRunning(true);
    setOutput("");
    try {
      const res = await api.post("/config/mysql/repair", { database: selected });
      setOutput(res.data?.data?.output || "(no output)");
      toast.success(`Repair finished for ${selected}`);
    } catch (e: any) {
      setOutput(e?.response?.data?.data?.output || "");
      toast.error(e?.response?.data?.error?.message || "Repair failed");
    } finally {
      setRunning(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
          <Database size={20} /> Repair Databases
        </h1>
        <p className="text-panel-muted text-sm mt-1">
          Runs <code className="text-panel-text">mysqlcheck --auto-repair --optimize</code> on
          the selected MySQL/MariaDB database.
        </p>
      </div>

      <Card>
        <div className="p-5 space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1">Select a database to repair:</label>
            <select
              value={selected}
              onChange={(e) => setSelected(e.target.value)}
              className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm focus:outline-none focus:ring-2 focus:ring-blue-500/40"
              size={Math.min(8, Math.max(3, dbs.length))}
            >
              {dbs.map((d) => (
                <option key={d} value={d}>{d}</option>
              ))}
            </select>
            {!loading && dbs.length === 0 && (
              <p className="text-xs text-amber-400 mt-1">No MySQL databases found.</p>
            )}
          </div>

          <div className="flex items-center gap-2">
            <Button onClick={run} loading={running} disabled={!selected || loading}>
              <Wrench size={16} /> Repair Database
            </Button>
            <Button variant="ghost" onClick={load} disabled={loading || running}>
              <RefreshCw size={16} className={loading ? "animate-spin" : ""} /> Refresh
            </Button>
          </div>

          {output && (
            <div>
              <div className="text-xs uppercase tracking-wider text-panel-muted mb-1">Output</div>
              <pre className="font-mono text-xs text-panel-text leading-relaxed whitespace-pre overflow-x-auto rounded-lg border border-panel-border bg-panel-bg p-3">
                {output}
              </pre>
            </div>
          )}
        </div>
      </Card>
    </div>
  );
}
