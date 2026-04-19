import { useEffect, useState } from "react";
import { Button, Card } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Database, Save, RefreshCw } from "lucide-react";

interface MySQLConfig {
  max_allowed_packet: number; max_connect_errors: number; max_connections: number;
  open_files_limit: number; performance_schema: boolean; sql_mode: string;
  thread_cache_size: number; interactive_timeout: number; wait_timeout: number;
  log_output: string; log_error: string; log_warnings: number;
  general_log: boolean; general_log_file: string;
  slow_query_log: boolean; slow_query_log_file: string; long_query_time: number;
  join_buffer_size: number; key_buffer_size: number; read_buffer_size: number;
  read_rnd_buffer_size: number; sort_buffer_size: number;
  innodb_log_buffer_size: number; innodb_log_file_size: number;
  innodb_sort_buffer_size: number; innodb_buffer_pool_size: number;
  max_heap_table_size: number; tmp_table_size: number;
  query_cache_size: number; query_cache_type: number;
}

type Section = "General" | "Timeouts" | "Logs" | "Buffers" | "InnoDB" | "Memory Tables" | "Query Cache";

const inp = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500/40";
const lbl = "block text-sm font-medium text-panel-text mb-1";

const rows: Array<{ key: keyof MySQLConfig; label: string; section: Section; type?: "text" | "number" | "bool" | "select"; options?: Array<{ value: any; label: string }> }> = [
  { key: "max_allowed_packet", label: "Max Allowed Packet", section: "General", type: "number" },
  { key: "max_connect_errors", label: "Max Connect Errors", section: "General", type: "number" },
  { key: "max_connections", label: "Max Connections", section: "General", type: "number" },
  { key: "open_files_limit", label: "Open Files Limit", section: "General", type: "number" },
  { key: "performance_schema", label: "Performance Schema", section: "General", type: "bool" },
  { key: "sql_mode", label: "SQL Mode", section: "General", type: "text" },
  { key: "thread_cache_size", label: "Thread Cache Size", section: "General", type: "number" },
  { key: "interactive_timeout", label: "Interactive Timeout", section: "Timeouts", type: "number" },
  { key: "wait_timeout", label: "Wait Timeout", section: "Timeouts", type: "number" },
  { key: "log_output", label: "Log Output", section: "Logs", type: "select", options: [{value:"FILE",label:"FILE"},{value:"TABLE",label:"TABLE"},{value:"NONE",label:"NONE"}]},
  { key: "log_error", label: "Error Log File Name", section: "Logs", type: "text" },
  { key: "log_warnings", label: "Log Warnings", section: "Logs", type: "number" },
  { key: "general_log", label: "General Logging", section: "Logs", type: "bool" },
  { key: "general_log_file", label: "General Log File Name", section: "Logs", type: "text" },
  { key: "slow_query_log", label: "Log Slow Queries", section: "Logs", type: "bool" },
  { key: "slow_query_log_file", label: "Slow Query Log File Name", section: "Logs", type: "text" },
  { key: "long_query_time", label: "Long Query Time", section: "Logs", type: "number" },
  { key: "join_buffer_size", label: "Join Buffer Size", section: "Buffers", type: "number" },
  { key: "key_buffer_size", label: "Key Buffer Size", section: "Buffers", type: "number" },
  { key: "read_buffer_size", label: "Read Buffer Size", section: "Buffers", type: "number" },
  { key: "read_rnd_buffer_size", label: "Read Random Buffer Size", section: "Buffers", type: "number" },
  { key: "sort_buffer_size", label: "Sort Buffer Size", section: "Buffers", type: "number" },
  { key: "innodb_log_buffer_size", label: "InnoDB Log Buffer Size", section: "InnoDB", type: "number" },
  { key: "innodb_log_file_size", label: "InnoDB Log File Size", section: "InnoDB", type: "number" },
  { key: "innodb_sort_buffer_size", label: "InnoDB Sort Buffer Size", section: "InnoDB", type: "number" },
  { key: "innodb_buffer_pool_size", label: "InnoDB Buffer Pool Size", section: "InnoDB", type: "number" },
  { key: "max_heap_table_size", label: "Max Heap Table Size", section: "Memory Tables", type: "number" },
  { key: "tmp_table_size", label: "Temporary Table Size", section: "Memory Tables", type: "number" },
  { key: "query_cache_size", label: "Query Cache Size", section: "Query Cache", type: "number" },
  { key: "query_cache_type", label: "Query Cache Type", section: "Query Cache", type: "select", options: [{value:0,label:"0 (OFF)"},{value:1,label:"1 (ON)"},{value:2,label:"2 (DEMAND)"}]},
];

export default function EditDatabaseConfigPage() {
  const [cfg, setCfg] = useState<MySQLConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const res = await api.get("/config/mysql");
      setCfg(res.data?.data || null);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to load MySQL config");
    } finally { setLoading(false); }
  };
  useEffect(() => { void load(); }, []);

  const save = async () => {
    if (!cfg) return;
    setSaving(true);
    try {
      await api.put("/config/mysql", cfg);
      toast.success("MySQL/MariaDB config updated — service restarted");
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to save");
    } finally { setSaving(false); }
  };

  const bySection: Record<string, typeof rows> = {} as any;
  for (const r of rows) { (bySection[r.section] ||= [] as any).push(r); }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
            <Database size={20} /> Edit Database Configuration
          </h1>
          <p className="text-panel-muted text-sm mt-1">
            Modifies MariaDB/MySQL config. Changes are written to a panel-owned
            drop-in under <code className="text-panel-text">/etc/mysql/mariadb.conf.d/99-panel.cnf</code> and
            the service is restarted.
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="ghost" onClick={load} disabled={loading || saving}>
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
          </Button>
          <Button onClick={save} loading={saving} disabled={!cfg}>
            <Save size={14} /> Submit
          </Button>
        </div>
      </div>

      {loading && !cfg ? (
        <Card><div className="p-6 text-sm text-panel-muted">Loading…</div></Card>
      ) : cfg && (
        <div className="space-y-5">
          {Object.entries(bySection).map(([section, items]) => (
            <Card key={section}>
              <div className="p-5">
                <h2 className="text-xs uppercase tracking-wider text-panel-muted mb-3">{section}</h2>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  {items.map((r) => (
                    <div key={r.key as string}>
                      <label className={lbl}>
                        {r.label} <span className="text-xs text-panel-muted font-mono">({r.key as string})</span>
                      </label>
                      {r.type === "bool" ? (
                        <label className="flex items-center gap-2 text-sm text-panel-text cursor-pointer">
                          <input type="checkbox"
                            checked={Boolean((cfg as any)[r.key])}
                            onChange={(e) => setCfg({ ...cfg, [r.key]: e.target.checked } as MySQLConfig)}
                          />
                          {(cfg as any)[r.key] ? "Enabled" : "Disabled"}
                        </label>
                      ) : r.type === "select" ? (
                        <select className={inp}
                          value={(cfg as any)[r.key] as any}
                          onChange={(e) => {
                            const v = r.options?.[0] && typeof r.options[0].value === "number"
                              ? Number(e.target.value) : e.target.value;
                            setCfg({ ...cfg, [r.key]: v } as MySQLConfig);
                          }}>
                          {r.options?.map((o) => <option key={String(o.value)} value={o.value as any}>{o.label}</option>)}
                        </select>
                      ) : r.type === "number" ? (
                        <input type="number" className={inp}
                          value={(cfg as any)[r.key] as number}
                          onChange={(e) => setCfg({ ...cfg, [r.key]: Number(e.target.value) } as MySQLConfig)}
                        />
                      ) : (
                        <input type="text" className={inp}
                          value={(cfg as any)[r.key] as string}
                          onChange={(e) => setCfg({ ...cfg, [r.key]: e.target.value } as MySQLConfig)}
                        />
                      )}
                    </div>
                  ))}
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
