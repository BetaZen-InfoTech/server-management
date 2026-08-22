import { useState, useEffect } from "react";
import { Card, Button, PasswordInput } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Cloud, Save, RefreshCw, Loader2, CheckCircle2, AlertTriangle, ShieldCheck,
  Play, XCircle, Activity,
} from "lucide-react";

// CloudflareConfigView mirrors the backend services.CloudflareConfigView.
// The API token is NEVER returned — the form shows `has_token` + a masked
// `token_preview` so a stolen JWT can't exfiltrate the credential.
interface CloudflareConfigView {
  account_id: string;
  has_token: boolean;
  token_preview?: string;
  enabled: boolean;
  auto_enable: boolean;
  proxy_web_records?: boolean;
  default_provider: string;
  connection_status?: "" | "ok" | "failed" | string;
  last_test_at?: string | null;
  last_sync_at?: string | null;
  last_error?: string;
  configured: boolean;
  test_status?: string;
  test_error?: string;
}

// CompareRow / CompareResult mirror the backend services.CompareResult — the
// read-only local↔Cloudflare reconciliation. No mutations happen; applying
// changes (sync) arrives later behind an approval prompt.
interface CompareRow {
  type: string;
  name: string;
  status: "matched" | "local_only" | "cf_only" | "conflict" | string;
  local_value?: string;
  cf_value?: string;
  local_ttl?: number;
  cf_ttl?: number;
  proxied?: boolean;
  cf_record_id?: string;
}
interface CompareResult {
  domain: string;
  zone_found: boolean;
  cf_zone_id?: string;
  cf_status?: string;
  cf_nameservers?: string[];
  counts: { matched: number; local_only: number; cf_only: number; conflict: number };
  rows: CompareRow[];
  note?: string;
}

const STATUS_META: Record<string, { label: string; cls: string }> = {
  matched: { label: "Matched", cls: "text-green-400 bg-green-500/10 border-green-500/20" },
  conflict: { label: "Conflict", cls: "text-amber-400 bg-amber-500/10 border-amber-500/20" },
  local_only: { label: "Local only", cls: "text-blue-400 bg-blue-500/10 border-blue-500/20" },
  cf_only: { label: "Cloudflare only", cls: "text-purple-400 bg-purple-500/10 border-purple-500/20" },
};

// SyncJob / item / event mirror backend models.CloudflareSyncJob. The events
// stream is durable (stored in the job doc), so it replays after a refresh.
interface SyncItem {
  domain: string; status: string;
  created: number; updated: number; deleted: number; skipped: number; error?: string;
}
interface SyncEvent { time: string; level: string; domain?: string; message: string; }
interface SyncJob {
  id: string; kind: string; status: string; apply_deletes: boolean;
  total: number; created: number; updated: number; deleted: number; skipped: number; failed: number;
  progress: number; current_domain: string; cancel_requested: boolean;
  items: SyncItem[]; events: SyncEvent[]; error?: string;
  started_at: string; completed_at?: string | null;
}

const TERMINAL = ["completed", "failed", "cancelled"];
function jobStatusColor(s: string) {
  return s === "completed" ? "text-green-400"
    : s === "failed" ? "text-red-400"
    : s === "cancelled" ? "text-amber-400"
    : "text-blue-400";
}
function eventColor(level: string) {
  return level === "error" ? "text-red-400" : level === "warn" ? "text-amber-400" : "text-panel-muted";
}

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const selectClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";

function fmtTime(iso?: string | null): string {
  if (!iso) return "never";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "never";
  return d.toLocaleString();
}

export default function CloudflarePage() {
  const [cfg, setCfg] = useState<CloudflareConfigView | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);

  // Form state. api_token starts blank; blank on save means "keep the
  // existing encrypted token" so an operator can toggle Enabled or edit the
  // Account ID without re-pasting the token.
  const [accountId, setAccountId] = useState("");
  const [apiToken, setApiToken] = useState("");
  const [enabled, setEnabled] = useState(false);
  const [autoEnable, setAutoEnable] = useState(false);
  const [proxyWebRecords, setProxyWebRecords] = useState(false);
  const [defaultProvider, setDefaultProvider] = useState("powerdns");

  // Read-only reconciliation compare state.
  const [compareDomain, setCompareDomain] = useState("");
  const [comparing, setComparing] = useState(false);
  const [compareResult, setCompareResult] = useState<CompareResult | null>(null);
  const [connecting, setConnecting] = useState(false);

  // Sync + live-progress state.
  const [applyDeletes, setApplyDeletes] = useState(false);
  const [starting, setStarting] = useState(false);
  const [activeJobId, setActiveJobId] = useState<string | null>(null);
  const [job, setJob] = useState<SyncJob | null>(null);
  const [recentJobs, setRecentJobs] = useState<SyncJob[]>([]);

  useEffect(() => {
    fetchConfig();
    loadRecentJobs();
  }, []);

  // Poll the active job until it reaches a terminal state. Because job state
  // lives in Mongo, re-selecting a job after a refresh replays its progress +
  // event stream.
  useEffect(() => {
    if (!activeJobId) return;
    let stopped = false;
    const tick = async () => {
      try {
        const res = await api.get(`/cloudflare/sync/jobs/${activeJobId}`);
        const j: SyncJob = res.data?.data ?? res.data;
        if (stopped) return;
        setJob(j);
        if (TERMINAL.includes(j.status)) {
          stopped = true;
          clearInterval(iv);
          loadRecentJobs();
        }
      } catch {
        /* transient — keep polling */
      }
    };
    const iv = setInterval(tick, 1500);
    tick();
    return () => {
      stopped = true;
      clearInterval(iv);
    };
  }, [activeJobId]);

  async function fetchConfig() {
    setLoading(true);
    try {
      const res = await api.get("/config/cloudflare");
      const view: CloudflareConfigView = res.data?.data ?? res.data;
      applyView(view);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to load Cloudflare configuration");
    } finally {
      setLoading(false);
    }
  }

  function applyView(view: CloudflareConfigView) {
    setCfg(view);
    setAccountId(view.account_id || "");
    setEnabled(!!view.enabled);
    setAutoEnable(!!view.auto_enable);
    setProxyWebRecords(!!view.proxy_web_records);
    setDefaultProvider(view.default_provider || "powerdns");
    // Never prefill the token — it's write-only. Clear the input so a save
    // that didn't touch it sends "" (keep existing).
    setApiToken("");
  }

  async function handleSave() {
    setSaving(true);
    try {
      const res = await api.put("/config/cloudflare", {
        account_id: accountId.trim(),
        api_token: apiToken, // "" = keep existing cipher
        enabled,
        auto_enable: autoEnable,
        proxy_web_records: proxyWebRecords,
        default_provider: defaultProvider,
      });
      const view: CloudflareConfigView = res.data?.data ?? res.data;
      applyView(view);
      toast.success("Cloudflare configuration saved");
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to save configuration");
    } finally {
      setSaving(false);
    }
  }

  async function handleTest() {
    setTesting(true);
    try {
      const res = await api.post("/config/cloudflare/test");
      const view: CloudflareConfigView = res.data?.data ?? res.data;
      setCfg(view);
      if (view.test_status === "ok") {
        toast.success("Cloudflare API token verified");
      } else {
        toast.error(view.test_error || "Cloudflare connection failed");
      }
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Connection test failed");
    } finally {
      setTesting(false);
    }
  }

  async function handleCompare() {
    const domain = compareDomain.trim().replace(/\.$/, "").toLowerCase();
    if (!domain) {
      toast.error("Enter a domain to compare");
      return;
    }
    setComparing(true);
    setCompareResult(null);
    try {
      const res = await api.get(`/cloudflare/compare/${encodeURIComponent(domain)}`);
      const result: CompareResult = res.data?.data ?? res.data;
      setCompareResult(result);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Compare failed");
    } finally {
      setComparing(false);
    }
  }

  async function handleConnect() {
    const domain = (compareResult?.domain || compareDomain).trim().replace(/\.$/, "").toLowerCase();
    if (!domain) return;
    setConnecting(true);
    try {
      const res = await api.post(`/cloudflare/zones/${encodeURIComponent(domain)}/connect`);
      const r = res.data?.data ?? res.data;
      toast.success(r?.created ? "Cloudflare zone created" : "Cloudflare zone connected");
      await handleCompare(); // refresh — the zone (with nameservers) now shows
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Connect failed");
    } finally {
      setConnecting(false);
    }
  }

  async function loadRecentJobs() {
    try {
      const res = await api.get("/cloudflare/sync/jobs");
      setRecentJobs(res.data?.data?.jobs ?? []);
    } catch {
      /* ignore */
    }
  }

  async function startSync(url: string) {
    if (
      applyDeletes &&
      !window.confirm(
        "Apply deletes will remove Cloudflare records that don't exist locally (mail records are kept). Continue?"
      )
    ) {
      return;
    }
    setStarting(true);
    try {
      const res = await api.post(url, { apply_deletes: applyDeletes });
      const j: SyncJob = res.data?.data ?? res.data;
      setJob(j);
      setActiveJobId(j.id);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to start sync");
    } finally {
      setStarting(false);
    }
  }
  function startSyncAll() {
    startSync("/cloudflare/sync/all");
  }
  function startSyncDomain() {
    const d = (compareResult?.domain || compareDomain).trim().replace(/\.$/, "").toLowerCase();
    if (!d) {
      toast.error("Enter a domain first");
      return;
    }
    startSync(`/cloudflare/sync/domains/${encodeURIComponent(d)}`);
  }
  function startReconcileAll() {
    if (!window.confirm("Connect + sync EVERY eligible domain to Cloudflare (creates zones as needed). Continue?")) return;
    startSync("/cloudflare/reconcile-all");
  }
  async function checkNameservers() {
    const d = (compareResult?.domain || compareDomain).trim().replace(/\.$/, "").toLowerCase();
    if (!d) return;
    try {
      const res = await api.get(`/cloudflare/zones/${encodeURIComponent(d)}/nameserver-status`);
      const s = res.data?.data ?? res.data;
      toast.success(`Nameservers: ${s.state}${s.message ? " — " + s.message : ""}`);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Nameserver check failed");
    }
  }
  async function setDomainEnabled(enabled: boolean) {
    const d = (compareResult?.domain || compareDomain).trim().replace(/\.$/, "").toLowerCase();
    if (!d) return;
    try {
      await api.post(`/cloudflare/zones/${encodeURIComponent(d)}/${enabled ? "enable" : "disable"}`);
      toast.success(`Cloudflare ${enabled ? "enabled" : "disabled"} for ${d}`);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed");
    }
  }
  async function cancelJob() {
    if (!job) return;
    try {
      await api.post(`/cloudflare/sync/jobs/${job.id}/cancel`);
      toast.success("Cancellation requested");
    } catch {
      toast.error("Cancel failed");
    }
  }

  const status = cfg?.connection_status || "";
  const statusBadge =
    status === "ok" ? (
      <span className="inline-flex items-center gap-1 text-xs font-medium text-green-400">
        <CheckCircle2 size={14} /> Connected
      </span>
    ) : status === "failed" ? (
      <span className="inline-flex items-center gap-1 text-xs font-medium text-red-400">
        <AlertTriangle size={14} /> Failed
      </span>
    ) : (
      <span className="inline-flex items-center gap-1 text-xs font-medium text-panel-muted">
        Not tested
      </span>
    );

  return (
    <div className="max-w-3xl space-y-6">
      <div className="flex items-center gap-3">
        <Cloud size={22} className="text-orange-400" />
        <div>
          <h1 className="text-lg font-semibold text-panel-text">Cloudflare</h1>
          <p className="text-sm text-panel-muted">
            Centralized Cloudflare account configuration. One account-level API
            token secures every Cloudflare-managed zone; the token is encrypted
            at rest and never leaves the server.
          </p>
        </div>
      </div>

      {loading ? (
        <div className="flex items-center gap-2 text-panel-muted text-sm">
          <Loader2 size={16} className="animate-spin" /> Loading…
        </div>
      ) : (
        <Card>
          <div className="p-5 space-y-5">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold text-panel-text flex items-center gap-2">
                <ShieldCheck size={16} className="text-blue-400" /> API Credentials
              </h2>
              {statusBadge}
            </div>

            <div>
              <label className={labelClass}>Cloudflare Account ID</label>
              <input
                className={inputClass}
                value={accountId}
                onChange={(e) => setAccountId(e.target.value)}
                placeholder="e.g. 023e105f4ecef8ad9ca31a8372d0c353"
                autoComplete="off"
              />
            </div>

            <div>
              <label className={labelClass}>
                Cloudflare API Token
                {cfg?.has_token && (
                  <span className="ml-2 text-xs font-normal text-panel-muted">
                    saved{cfg.token_preview ? ` (${cfg.token_preview})` : ""} — leave blank to keep
                  </span>
                )}
              </label>
              <PasswordInput
                value={apiToken}
                onChange={(v: string) => setApiToken(v)}
                placeholder={cfg?.has_token ? "•••••••• (unchanged)" : "Paste an account-scoped API token"}
                hideGenerator
              />
              <p className="mt-1 text-xs text-panel-muted">
                Create a token with <b>Zone · DNS · Edit</b> and <b>Zone · Zone · Read</b>{" "}
                permissions. It is stored AES-GCM encrypted and never shown again.
              </p>
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label className={labelClass}>Default DNS Provider</label>
                <select
                  className={selectClass}
                  value={defaultProvider}
                  onChange={(e) => setDefaultProvider(e.target.value)}
                >
                  <option value="powerdns">Existing DNS (PowerDNS)</option>
                  <option value="cloudflare">Cloudflare DNS</option>
                </select>
                <p className="mt-1 text-xs text-panel-muted">
                  Applies to newly added domains. Existing zones keep their
                  current provider until changed per-domain.
                </p>
              </div>
              <div>
                <label className={labelClass}>Integration</label>
                <label className="flex items-center gap-2 px-3 py-2 bg-panel-bg border border-panel-border rounded-lg cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={enabled}
                    onChange={(e) => setEnabled(e.target.checked)}
                    className="accent-blue-500"
                  />
                  <span className="text-sm text-panel-text">
                    {enabled ? "Enabled" : "Disabled"}
                  </span>
                </label>
                <p className="mt-1 text-xs text-panel-muted">
                  When disabled, no Cloudflare calls are made and existing DNS
                  behaviour is untouched.
                </p>
                <label className="flex items-center gap-2 mt-2 cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={autoEnable}
                    onChange={(e) => setAutoEnable(e.target.checked)}
                    className="accent-blue-500"
                    disabled={!enabled}
                  />
                  <span className="text-xs text-panel-text">
                    Auto-connect new domains to Cloudflare
                  </span>
                </label>
                <p className="mt-1 text-xs text-panel-muted">
                  New domains are connected + synced automatically. Use
                  <b> Reconcile all</b> below for existing domains.
                </p>
                <label className="flex items-center gap-2 mt-2 cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={proxyWebRecords}
                    onChange={(e) => setProxyWebRecords(e.target.checked)}
                    className="accent-blue-500"
                    disabled={!enabled}
                  />
                  <span className="text-xs text-panel-text">
                    Proxy web records (orange cloud)
                  </span>
                </label>
                <p className="mt-1 text-xs text-panel-muted">
                  On sync, orange-clouds eligible web records (A/AAAA/CNAME for
                  apex, www &amp; subdomains) for Cloudflare's CDN/WAF. Mail
                  records (MX/SPF/DKIM/DMARC/mail) always stay DNS-only. Re-sync
                  or <b>Reconcile all</b> to apply to existing records. Set
                  Cloudflare SSL/TLS mode to <b>Full (strict)</b> to avoid redirect loops.
                </p>
              </div>
            </div>

            {cfg?.last_error && status === "failed" && (
              <div className="flex items-start gap-2 text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2">
                <AlertTriangle size={14} className="mt-0.5 shrink-0" />
                <span>{cfg.last_error}</span>
              </div>
            )}

            <div className="text-xs text-panel-muted">
              Last connection test: {fmtTime(cfg?.last_test_at)} · Last sync:{" "}
              {fmtTime(cfg?.last_sync_at)}
            </div>

            <div className="flex items-center gap-3 pt-1">
              <Button onClick={handleSave} disabled={saving}>
                {saving ? <Loader2 size={16} className="animate-spin" /> : <Save size={16} />}
                {saving ? "Saving…" : "Save"}
              </Button>
              <Button variant="secondary" onClick={handleTest} disabled={testing || !cfg?.has_token}>
                {testing ? <Loader2 size={16} className="animate-spin" /> : <RefreshCw size={16} />}
                {testing ? "Testing…" : "Test connection"}
              </Button>
              {!cfg?.has_token && (
                <span className="text-xs text-panel-muted">Save a token before testing.</span>
              )}
            </div>
          </div>
        </Card>
      )}

      {/* Reconcile (read-only) — compare local DNS vs the Cloudflare zone.
          Nothing is mutated; applying changes arrives later behind approval. */}
      <Card>
        <div className="p-5 space-y-4">
          <div>
            <h2 className="text-sm font-semibold text-panel-text flex items-center gap-2">
              <RefreshCw size={16} className="text-blue-400" /> Reconcile (read-only)
            </h2>
            <p className="text-xs text-panel-muted mt-1">
              Compare a domain's local DNS records against its Cloudflare zone.
              This is read-only — nothing is changed on either side. Applying
              changes will be added later behind an approval prompt.
            </p>
          </div>

          <div className="flex items-end gap-2">
            <div className="flex-1">
              <label className={labelClass}>Domain</label>
              <input
                className={inputClass}
                value={compareDomain}
                onChange={(e) => setCompareDomain(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") handleCompare(); }}
                placeholder="example.com"
                autoComplete="off"
              />
            </div>
            <Button onClick={handleCompare} disabled={comparing || !cfg?.enabled}>
              {comparing ? <Loader2 size={16} className="animate-spin" /> : <RefreshCw size={16} />}
              {comparing ? "Comparing…" : "Compare"}
            </Button>
          </div>
          {!cfg?.enabled && (
            <p className="text-xs text-panel-muted">
              Enable the integration above to compare zones.
            </p>
          )}

          {compareResult && (
            <div className="space-y-3">
              <div className="text-xs text-panel-muted">
                {compareResult.zone_found ? (
                  <>
                    Cloudflare zone{" "}
                    <span className="text-panel-text font-mono">{compareResult.cf_zone_id}</span>{" "}
                    · status <span className="text-panel-text">{compareResult.cf_status}</span>
                    {compareResult.cf_nameservers?.length ? (
                      <> · nameservers: {compareResult.cf_nameservers.join(", ")}</>
                    ) : null}
                  </>
                ) : (
                  <div className="flex items-center justify-between gap-3 flex-wrap">
                    <span>{compareResult.note || "No Cloudflare zone for this domain."}</span>
                    <Button size="sm" onClick={handleConnect} disabled={connecting}>
                      {connecting ? <Loader2 size={14} className="animate-spin" /> : <Cloud size={14} />}
                      {connecting ? "Connecting…" : "Connect to Cloudflare"}
                    </Button>
                  </div>
                )}
              </div>

              {compareResult.zone_found && (
                <div className="flex flex-wrap gap-2">
                  <Button size="sm" variant="secondary" onClick={checkNameservers}>
                    Check nameservers
                  </Button>
                  <Button size="sm" variant="secondary" onClick={() => setDomainEnabled(false)}>
                    Disable (this domain)
                  </Button>
                  <Button size="sm" variant="secondary" onClick={() => setDomainEnabled(true)}>
                    Enable
                  </Button>
                </div>
              )}

              <div className="flex flex-wrap gap-2">
                {(["matched", "conflict", "local_only", "cf_only"] as const).map((k) => (
                  <span key={k} className={`text-xs px-2 py-1 rounded-lg border ${STATUS_META[k].cls}`}>
                    {STATUS_META[k].label}: {compareResult.counts[k] ?? 0}
                  </span>
                ))}
              </div>

              {compareResult.rows.length > 0 ? (
                <div className="overflow-x-auto border border-panel-border rounded-lg">
                  <table className="w-full text-xs">
                    <thead className="bg-panel-bg text-panel-muted">
                      <tr>
                        <th className="text-left px-3 py-2 font-medium">Type</th>
                        <th className="text-left px-3 py-2 font-medium">Name</th>
                        <th className="text-left px-3 py-2 font-medium">Status</th>
                        <th className="text-left px-3 py-2 font-medium">Local</th>
                        <th className="text-left px-3 py-2 font-medium">Cloudflare</th>
                      </tr>
                    </thead>
                    <tbody>
                      {compareResult.rows.map((r, i) => {
                        const meta = STATUS_META[r.status] || {
                          label: r.status,
                          cls: "text-panel-muted border-panel-border",
                        };
                        return (
                          <tr key={i} className="border-t border-panel-border/60">
                            <td className="px-3 py-2 text-panel-text font-mono">{r.type}</td>
                            <td className="px-3 py-2 text-panel-text font-mono break-all">{r.name}</td>
                            <td className="px-3 py-2">
                              <span className={`px-2 py-0.5 rounded border ${meta.cls}`}>{meta.label}</span>
                            </td>
                            <td className="px-3 py-2 text-panel-muted font-mono break-all">{r.local_value || "—"}</td>
                            <td className="px-3 py-2 text-panel-muted font-mono break-all">
                              {r.cf_value || "—"}
                              {r.proxied ? <span className="ml-1 text-orange-400">(proxied)</span> : null}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              ) : (
                <p className="text-xs text-panel-muted">No records to compare.</p>
              )}
            </div>
          )}
        </div>
      </Card>

      {/* Sync & live progress — the durable "watch it work" panel. Progress +
          the event stream come from the persisted job doc, so they survive a
          page refresh. */}
      <Card>
        <div className="p-5 space-y-4">
          <div>
            <h2 className="text-sm font-semibold text-panel-text flex items-center gap-2">
              <Activity size={16} className="text-blue-400" /> Sync &amp; live progress
            </h2>
            <p className="text-xs text-panel-muted mt-1">
              Push local DNS records into Cloudflare. Runs in the background —
              progress and the event stream below survive a page refresh. Mail
              records (MX / SPF / DKIM / DMARC / mail-A) are never proxied or
              deleted.
            </p>
          </div>

          <label className="flex items-start gap-2 text-xs text-panel-text">
            <input
              type="checkbox"
              className="accent-amber-500 mt-0.5"
              checked={applyDeletes}
              onChange={(e) => setApplyDeletes(e.target.checked)}
            />
            <span>
              <b className="text-amber-400">Apply deletes</b> — also remove
              Cloudflare records that don't exist locally. Mail records are still
              kept. You'll be asked to confirm.
            </span>
          </label>

          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" onClick={startSyncAll} disabled={starting || !cfg?.enabled}>
              {starting ? <Loader2 size={14} className="animate-spin" /> : <Play size={14} />}
              Sync all connected
            </Button>
            <Button size="sm" variant="secondary" onClick={startReconcileAll} disabled={starting || !cfg?.enabled}>
              <Cloud size={14} /> Reconcile all (connect + sync)
            </Button>
            <Button
              size="sm"
              variant="secondary"
              onClick={startSyncDomain}
              disabled={starting || !cfg?.enabled || !(compareResult?.domain || compareDomain)}
            >
              <Play size={14} /> Sync {compareResult?.domain || compareDomain || "domain"}
            </Button>
            {!cfg?.enabled && (
              <span className="text-xs text-panel-muted">Enable the integration above to sync.</span>
            )}
          </div>

          {job && (
            <div className="space-y-3 border border-panel-border rounded-lg p-3">
              <div className="flex items-center justify-between gap-2 flex-wrap">
                <span className="text-xs text-panel-text">
                  Job <span className="font-mono">{job.id.slice(-6)}</span> · {job.kind} ·{" "}
                  <b className={jobStatusColor(job.status)}>{job.status}</b>
                  {job.current_domain ? <> · {job.current_domain}</> : null}
                </span>
                {["queued", "running"].includes(job.status) && (
                  <Button size="sm" variant="danger" onClick={cancelJob}>
                    <XCircle size={14} /> Cancel
                  </Button>
                )}
              </div>

              <div className="h-2 w-full bg-panel-bg rounded-full overflow-hidden">
                <div className="h-full bg-blue-500 transition-all" style={{ width: `${job.progress}%` }} />
              </div>

              <div className="flex flex-wrap gap-3 text-xs">
                <span className="text-green-400">Created {job.created}</span>
                <span className="text-blue-400">Updated {job.updated}</span>
                <span className="text-amber-400">Deleted {job.deleted}</span>
                <span className="text-panel-muted">Unchanged {job.skipped}</span>
                <span className="text-red-400">Failed {job.failed}</span>
              </div>
              {job.error && <div className="text-xs text-red-400">{job.error}</div>}

              {job.items?.length > 1 && (
                <div className="overflow-x-auto max-h-40 overflow-y-auto border border-panel-border rounded">
                  <table className="w-full text-xs">
                    <tbody>
                      {job.items.map((it, i) => (
                        <tr key={i} className="border-t border-panel-border/50">
                          <td className="px-2 py-1 font-mono text-panel-text break-all">{it.domain}</td>
                          <td className="px-2 py-1 text-panel-muted">{it.status}</td>
                          <td className="px-2 py-1 text-panel-muted whitespace-nowrap">
                            +{it.created} ~{it.updated} -{it.deleted}
                          </td>
                          <td className="px-2 py-1 text-red-400 break-all">{it.error || ""}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}

              {job.events?.length > 0 && (
                <div className="bg-panel-bg rounded p-2 max-h-48 overflow-y-auto font-mono text-[11px] space-y-0.5">
                  {job.events.map((ev, i) => (
                    <div key={i} className={eventColor(ev.level)}>
                      [{new Date(ev.time).toLocaleTimeString()}] {ev.domain ? `${ev.domain}: ` : ""}
                      {ev.message}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {recentJobs.length > 0 && (
            <div className="text-xs text-panel-muted">
              <div className="mb-1">Recent jobs:</div>
              <div className="flex flex-wrap gap-2">
                {recentJobs.map((rj) => (
                  <button
                    key={rj.id}
                    onClick={() => {
                      setJob(rj);
                      setActiveJobId(rj.id);
                    }}
                    className="px-2 py-1 rounded border border-panel-border hover:border-blue-500 text-panel-text"
                  >
                    {rj.kind} · {rj.status} · {new Date(rj.started_at).toLocaleTimeString()}
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      </Card>
    </div>
  );
}
