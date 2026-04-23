import { useEffect, useState } from "react";
import { Card } from "@serverpanel/ui";
import { apiClient } from "@serverpanel/api-client";
import toast from "react-hot-toast";
import {
  Monitor, Smartphone, Tablet, Bot, Globe, MapPin, RefreshCw, Shield, KeyRound,
} from "lucide-react";

/**
 * Account → Sessions page (User Panel). Mirrors the WHM variant:
 * shows each past login's device, IP, and geolocation so the user can
 * flag anything that wasn't them.
 */
interface Session {
  id?: string;
  email: string;
  role: string;
  method: "password" | "otp" | string;
  ip: string;
  country?: string;
  region?: string;
  city?: string;
  user_agent: string;
  browser?: string;
  os?: string;
  device?: "desktop" | "mobile" | "tablet" | "bot" | "unknown" | string;
  login_at: string;
}

function DeviceIcon({ kind }: { kind?: string }) {
  const sz = 16;
  switch (kind) {
    case "mobile":
      return <Smartphone size={sz} className="text-emerald-400" />;
    case "tablet":
      return <Tablet size={sz} className="text-emerald-400" />;
    case "bot":
      return <Bot size={sz} className="text-amber-400" />;
    case "desktop":
      return <Monitor size={sz} className="text-brand-400" />;
    default:
      return <Globe size={sz} className="text-panel-muted" />;
  }
}

function formatWhen(iso: string) {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const now = Date.now();
  const diff = (now - d.getTime()) / 1000;
  if (diff < 60) return "just now";
  if (diff < 3600) return `${Math.round(diff / 60)} min ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)} h ago`;
  return d.toLocaleString();
}

export default function SessionsPage() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchSessions = async () => {
    setLoading(true);
    try {
      const res = await apiClient.get("/api/v1/auth/me/sessions?limit=50");
      setSessions(res.data?.data || []);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Could not load sessions");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchSessions();
  }, []);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
            <Shield size={20} className="text-brand-400" />
            Login Sessions
          </h1>
          <p className="text-panel-muted text-sm mt-1">
            Recent logins to this account. If anything looks unfamiliar, change your password immediately.
          </p>
        </div>
        <button
          onClick={fetchSessions}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
        >
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
          Refresh
        </button>
      </div>

      <Card>
        {loading ? (
          <div className="p-8 space-y-3">
            {[1, 2, 3, 4].map((i) => (
              <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
            ))}
          </div>
        ) : sessions.length === 0 ? (
          <div className="text-center py-16 px-4">
            <Shield size={40} className="text-panel-muted/30 mx-auto mb-3" />
            <p className="text-panel-text font-medium">No login history yet</p>
            <p className="text-panel-muted text-sm mt-1">Your next sign-in will show up here.</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-panel-border text-left text-panel-muted text-xs uppercase tracking-wider">
                  <th className="p-3">When</th>
                  <th className="p-3">Method</th>
                  <th className="p-3">Device</th>
                  <th className="p-3">Location</th>
                  <th className="p-3">IP</th>
                </tr>
              </thead>
              <tbody>
                {sessions.map((s, idx) => {
                  const loc = [s.city, s.region, s.country].filter(Boolean).join(", ");
                  return (
                    <tr key={s.id || idx} className="border-b border-panel-border/50 hover:bg-panel-bg/50 transition-colors">
                      <td className="p-3 align-top">
                        <div className="text-panel-text">{formatWhen(s.login_at)}</div>
                        <div className="text-[11px] text-panel-muted">{new Date(s.login_at).toLocaleString()}</div>
                      </td>
                      <td className="p-3 align-top">
                        <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium ${
                          s.method === "otp"
                            ? "bg-purple-500/10 text-purple-300 border border-purple-500/20"
                            : "bg-brand-500/10 text-brand-300 border border-brand-500/20"
                        }`}>
                          {s.method === "otp" ? <KeyRound size={10} /> : <Shield size={10} />}
                          {s.method.toUpperCase()}
                        </span>
                      </td>
                      <td className="p-3 align-top">
                        <div className="flex items-center gap-2">
                          <DeviceIcon kind={s.device} />
                          <div>
                            <div className="text-panel-text">
                              {s.browser || "Unknown"} {s.os ? `· ${s.os}` : ""}
                            </div>
                            <div className="text-[11px] text-panel-muted truncate max-w-xs" title={s.user_agent}>
                              {s.user_agent || "—"}
                            </div>
                          </div>
                        </div>
                      </td>
                      <td className="p-3 align-top">
                        {loc ? (
                          <div className="flex items-center gap-1.5 text-panel-text">
                            <MapPin size={12} className="text-panel-muted" />
                            {loc}
                          </div>
                        ) : (
                          <span className="text-panel-muted">—</span>
                        )}
                      </td>
                      <td className="p-3 align-top font-mono text-panel-muted text-xs">
                        {s.ip || "—"}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}
