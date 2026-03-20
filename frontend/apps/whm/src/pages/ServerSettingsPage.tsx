import { useState, useEffect } from "react";
import { Card, Button } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Server, Save, RefreshCw, Globe, Clock, Mail } from "lucide-react";

const TIMEZONES = [
  "UTC",
  "America/New_York",
  "America/Chicago",
  "America/Denver",
  "America/Los_Angeles",
  "America/Sao_Paulo",
  "America/Argentina/Buenos_Aires",
  "America/Mexico_City",
  "America/Toronto",
  "Europe/London",
  "Europe/Paris",
  "Europe/Berlin",
  "Europe/Moscow",
  "Europe/Istanbul",
  "Europe/Amsterdam",
  "Asia/Kolkata",
  "Asia/Shanghai",
  "Asia/Tokyo",
  "Asia/Seoul",
  "Asia/Singapore",
  "Asia/Dubai",
  "Asia/Hong_Kong",
  "Asia/Karachi",
  "Asia/Dhaka",
  "Asia/Jakarta",
  "Australia/Sydney",
  "Australia/Melbourne",
  "Pacific/Auckland",
  "Pacific/Honolulu",
  "Africa/Cairo",
  "Africa/Lagos",
  "Africa/Johannesburg",
];

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const selectClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";

export default function ServerSettingsPage() {
  const [hostname, setHostname] = useState("");
  const [timezone, setTimezone] = useState("UTC");
  const [contactEmail, setContactEmail] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const [original, setOriginal] = useState({ hostname: "", timezone: "UTC", contactEmail: "" });

  useEffect(() => {
    fetchSettings();
  }, []);

  const fetchSettings = async () => {
    setLoading(true);
    try {
      const res = await api.get("/config");
      const data = res.data.data || {};
      const h = (data.hostname as string) || "";
      const t = (data.timezone as string) || "UTC";
      const e = (data.contact_email as string) || "";
      setHostname(h);
      setTimezone(t);
      setContactEmail(e);
      setOriginal({ hostname: h, timezone: t, contactEmail: e });
    } catch {
      // keep defaults
    } finally {
      setLoading(false);
    }
  };

  const hasChanges =
    hostname !== original.hostname ||
    timezone !== original.timezone ||
    contactEmail !== original.contactEmail;

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      const promises: Promise<any>[] = [];
      if (hostname !== original.hostname) {
        promises.push(api.put("/config/hostname", { hostname }));
      }
      if (timezone !== original.timezone) {
        promises.push(api.put("/config/timezone", { timezone }));
      }
      if (contactEmail !== original.contactEmail) {
        promises.push(api.put("/config/contact-email", { email: contactEmail }));
      }
      await Promise.all(promises);
      toast.success("Server settings updated");
      setOriginal({ hostname, timezone, contactEmail });
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update settings");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Server Settings</h1>
          <p className="text-panel-muted text-sm mt-1">
            Configure server hostname, timezone, and contact information
          </p>
        </div>
        <Button
          onClick={fetchSettings}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
        >
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
          Refresh
        </Button>
      </div>

      {/* Settings Form */}
      <Card>
        <div className="p-5 border-b border-panel-border">
          <div className="flex items-center gap-2">
            <Server size={16} className="text-blue-400" />
            <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
              General Settings
            </h3>
          </div>
        </div>
        {loading ? (
          <div className="p-6 space-y-4">
            {[...Array(3)].map((_, i) => (
              <div key={i} className="h-16 bg-panel-bg rounded-lg animate-pulse" />
            ))}
          </div>
        ) : (
          <form onSubmit={handleSave} className="p-6 space-y-6">
            <div>
              <label className={labelClass}>
                <div className="flex items-center gap-2 mb-1">
                  <Globe size={14} className="text-green-400" />
                  Hostname
                </div>
              </label>
              <input
                type="text"
                value={hostname}
                onChange={(e) => setHostname(e.target.value)}
                className={inputClass}
                placeholder="e.g. server1.example.com"
              />
              <p className="text-xs text-panel-muted mt-1">
                The fully qualified domain name (FQDN) for this server
              </p>
            </div>

            <div>
              <label className={labelClass}>
                <div className="flex items-center gap-2 mb-1">
                  <Clock size={14} className="text-yellow-400" />
                  Timezone
                </div>
              </label>
              <select
                value={timezone}
                onChange={(e) => setTimezone(e.target.value)}
                className={selectClass}
              >
                {TIMEZONES.map((tz) => (
                  <option key={tz} value={tz}>
                    {tz}
                  </option>
                ))}
              </select>
              <p className="text-xs text-panel-muted mt-1">
                Server timezone used for cron jobs, logs, and system operations
              </p>
            </div>

            <div>
              <label className={labelClass}>
                <div className="flex items-center gap-2 mb-1">
                  <Mail size={14} className="text-purple-400" />
                  Contact Email
                </div>
              </label>
              <input
                type="email"
                value={contactEmail}
                onChange={(e) => setContactEmail(e.target.value)}
                className={inputClass}
                placeholder="admin@example.com"
              />
              <p className="text-xs text-panel-muted mt-1">
                Server administrator contact email for alerts and notifications
              </p>
            </div>

            <div className="flex justify-end pt-4 border-t border-panel-border">
              <Button
                type="submit"
                disabled={saving || !hasChanges}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
              >
                <Save size={14} />
                {saving ? "Saving..." : "Save Changes"}
              </Button>
            </div>
          </form>
        )}
      </Card>
    </div>
  );
}
