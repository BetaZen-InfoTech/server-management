import { useEffect, useState } from "react";
import { Button, Card } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { FileCode2, Save, RefreshCw, Info } from "lucide-react";

interface PHPIniDirective { key: string; value: string; info: string; }

export default function MultiPhpIniEditorPage() {
  const [versions, setVersions] = useState<string[]>([]);
  const [version, setVersion] = useState<string>("");
  const [dirs, setDirs] = useState<PHPIniDirective[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  const loadVersions = async () => {
    try {
      const res = await api.get("/config/php/versions");
      const vs: string[] = res.data?.data || [];
      setVersions(vs);
      if (vs.length && !version) setVersion(vs[0]);
    } catch {
      toast.error("Failed to load PHP versions");
    }
  };
  const loadDirs = async (v: string) => {
    if (!v) return;
    setLoading(true);
    try {
      const res = await api.get(`/config/php/${encodeURIComponent(v)}/ini`);
      setDirs(res.data?.data || []);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to load php.ini");
    } finally { setLoading(false); }
  };
  useEffect(() => { void loadVersions(); }, []);
  useEffect(() => { if (version) void loadDirs(version); }, [version]);

  const save = async () => {
    setSaving(true);
    try {
      await api.put(`/config/php/${encodeURIComponent(version)}/ini`, { directives: dirs });
      toast.success(`php.ini updated for PHP ${version}`);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to save");
    } finally { setSaving(false); }
  };

  const setVal = (i: number, v: string) =>
    setDirs((d) => d.map((row, ix) => ix === i ? { ...row, value: v } : row));

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
            <FileCode2 size={20} /> MultiPHP INI Editor
          </h1>
          <p className="text-panel-muted text-sm mt-1">
            Edits the selected PHP version's default php.ini. Rewrites
            matching lines in place; php-fpm reloads on save.
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="ghost" onClick={() => version && loadDirs(version)} disabled={loading || saving}>
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
          </Button>
          <Button onClick={save} loading={saving} disabled={!version || dirs.length === 0}>
            <Save size={14} /> Apply
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-5">
          <label className="block text-sm font-medium text-panel-text mb-1">PHP Version</label>
          <select
            value={version}
            onChange={(e) => setVersion(e.target.value)}
            className="w-full md:w-64 px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm"
          >
            <option value="">— Select a PHP version —</option>
            {versions.map((v) => <option key={v} value={v}>PHP {v}</option>)}
          </select>
          {versions.length === 0 && (
            <p className="text-xs text-amber-400 mt-1">No PHP versions detected on this server.</p>
          )}
        </div>
      </Card>

      {version && (
        <Card>
          <div className="p-5">
            {loading ? (
              <div className="h-20 bg-panel-border/20 rounded animate-pulse" />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="text-left text-xs uppercase tracking-wider text-panel-muted border-b border-panel-border">
                      <th className="py-2 pr-4 w-[280px]">PHP Directive</th>
                      <th className="py-2 pr-4">Information</th>
                      <th className="py-2 pr-4 w-[280px]">Setting</th>
                    </tr>
                  </thead>
                  <tbody>
                    {dirs.map((d, i) => (
                      <tr key={d.key} className="border-b border-panel-border/50">
                        <td className="py-2 pr-4 font-mono text-xs text-panel-text">{d.key}</td>
                        <td className="py-2 pr-4 text-xs text-panel-muted">
                          <span className="inline-flex items-center gap-1"><Info size={10} /> {d.info}</span>
                        </td>
                        <td className="py-2 pr-4">
                          <input
                            type="text"
                            value={d.value}
                            onChange={(e) => setVal(i, e.target.value)}
                            className="w-full px-2 py-1.5 bg-panel-bg border border-panel-border rounded text-panel-text text-xs font-mono"
                          />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </Card>
      )}
    </div>
  );
}
