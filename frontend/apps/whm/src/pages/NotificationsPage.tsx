import { useState, useEffect } from "react";
import { Card, Button, Table, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Bell, Plus, RefreshCw, Trash2, Mail, MessageSquare, Webhook, Settings } from "lucide-react";

interface NotificationChannel {
  id: string;
  type: "email" | "slack" | "webhook" | "telegram";
  name: string;
  target: string;
  enabled: boolean;
}

interface WebhookEntry {
  id: string;
  url: string;
  events: string[];
  enabled: boolean;
  lastTriggered: string;
}

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

const availableEvents = [
  { id: "server_down", label: "Server Down" },
  { id: "high_cpu", label: "High CPU" },
  { id: "high_memory", label: "High Memory" },
  { id: "disk_full", label: "Disk Full" },
  { id: "ssl_expiry", label: "SSL Expiry" },
  { id: "backup_failed", label: "Backup Failed" },
  { id: "deployment", label: "Deployment" },
];

export default function NotificationsPage() {
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [webhooks, setWebhooks] = useState<WebhookEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [showAddChannel, setShowAddChannel] = useState(false);
  const [showAddWebhook, setShowAddWebhook] = useState(false);
  const [creating, setCreating] = useState(false);
  const [channelForm, setChannelForm] = useState({ type: "email" as string, name: "", target: "" });
  const [webhookForm, setWebhookForm] = useState({ url: "", events: [] as string[] });

  useEffect(() => {
    fetchNotifications();
  }, []);

  const fetchNotifications = async () => {
    setLoading(true);
    try {
      const [settingsRes, webhooksRes] = await Promise.allSettled([
        api.get("/notifications/settings"),
        api.get("/webhooks/"),
      ]);

      if (settingsRes.status === "fulfilled") setChannels(settingsRes.value.data.data || []);
      if (webhooksRes.status === "fulfilled") setWebhooks(webhooksRes.value.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const handleAddChannel = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!channelForm.name || !channelForm.target) {
      toast.error("Please fill all required fields");
      return;
    }
    setCreating(true);
    try {
      await api.put("/notifications/settings", channelForm);
      toast.success("Channel added");
      setShowAddChannel(false);
      setChannelForm({ type: "email", name: "", target: "" });
      fetchNotifications();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to add channel");
    } finally {
      setCreating(false);
    }
  };

  const handleAddWebhook = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!webhookForm.url || webhookForm.events.length === 0) {
      toast.error("Please provide a URL and select at least one event");
      return;
    }
    setCreating(true);
    try {
      await api.post("/webhooks/", webhookForm);
      toast.success("Webhook added");
      setShowAddWebhook(false);
      setWebhookForm({ url: "", events: [] });
      fetchNotifications();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to add webhook");
    } finally {
      setCreating(false);
    }
  };

  const toggleChannel = async (id: string, enabled: boolean) => {
    try {
      await api.put("/notifications/settings", { id, enabled: !enabled });
      setChannels((prev) =>
        prev.map((c) => (c.id === id ? { ...c, enabled: !enabled } : c))
      );
      toast.success(`Channel ${!enabled ? "enabled" : "disabled"}`);
    } catch {
      toast.error("Failed to update channel");
    }
  };

  const handleDeleteWebhook = async (id: string) => {
    if (!confirm("Are you sure you want to delete this webhook?")) return;
    try {
      await api.delete(`/webhooks/${id}`);
      toast.success("Webhook deleted");
      fetchNotifications();
    } catch {
      toast.error("Failed to delete webhook");
    }
  };

  const getChannelIcon = (type: string) => {
    switch (type) {
      case "email": return <Mail size={16} className="text-blue-400" />;
      case "slack": return <MessageSquare size={16} className="text-purple-400" />;
      case "webhook": return <Webhook size={16} className="text-green-400" />;
      case "telegram": return <MessageSquare size={16} className="text-cyan-400" />;
      default: return <Bell size={16} className="text-panel-muted" />;
    }
  };

  const eventTypes = [
    { id: "server_down", label: "Server Down", description: "Alert when server becomes unreachable", enabled: true },
    { id: "high_cpu", label: "High CPU Usage", description: "Alert when CPU exceeds 90% for 5 minutes", enabled: true },
    { id: "high_memory", label: "High Memory Usage", description: "Alert when memory exceeds 90%", enabled: true },
    { id: "disk_full", label: "Disk Space Low", description: "Alert when disk usage exceeds 85%", enabled: true },
    { id: "ssl_expiry", label: "SSL Certificate Expiry", description: "Alert 30 days before SSL expiration", enabled: true },
    { id: "backup_failed", label: "Backup Failed", description: "Alert when a scheduled backup fails", enabled: false },
    { id: "login_failed", label: "Failed Login Attempts", description: "Alert after 5 failed login attempts", enabled: false },
    { id: "deployment", label: "Deployment Status", description: "Notify on deployment success or failure", enabled: true },
  ];

  const [events, setEvents] = useState(eventTypes);

  const toggleEvent = (id: string) => {
    setEvents((prev) =>
      prev.map((e) => (e.id === id ? { ...e, enabled: !e.enabled } : e))
    );
    toast.success("Notification preference updated");
  };

  const toggleWebhookEvent = (eventId: string) => {
    setWebhookForm((prev) => ({
      ...prev,
      events: prev.events.includes(eventId)
        ? prev.events.filter((e) => e !== eventId)
        : [...prev.events, eventId],
    }));
  };

  const webhookColumns = [
    {
      header: "URL",
      accessor: (w: WebhookEntry) => (
        <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono truncate max-w-[300px] block">
          {w.url}
        </code>
      ),
    },
    {
      header: "Events",
      accessor: (w: WebhookEntry) => (
        <div className="flex flex-wrap gap-1">
          {w.events.map((e) => (
            <span key={e} className="px-1.5 py-0.5 bg-panel-bg rounded text-xs text-panel-muted">
              {e}
            </span>
          ))}
        </div>
      ),
    },
    {
      header: "Enabled",
      accessor: (w: WebhookEntry) => (
        <span className={w.enabled ? "text-green-400" : "text-red-400"}>
          {w.enabled ? "Yes" : "No"}
        </span>
      ),
    },
    {
      header: "Last Triggered",
      accessor: (w: WebhookEntry) => (
        <span className="text-panel-muted text-sm">{w.lastTriggered || "Never"}</span>
      ),
    },
    {
      header: "",
      accessor: (w: WebhookEntry) => (
        <button
          onClick={() => handleDeleteWebhook(w.id)}
          className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
        >
          <Trash2 size={14} />
        </button>
      ),
    },
  ];

  const channelTypes = [
    { value: "email", label: "Email", placeholder: "user@example.com" },
    { value: "slack", label: "Slack", placeholder: "https://hooks.slack.com/services/..." },
    { value: "telegram", label: "Telegram", placeholder: "Chat ID or Bot Token" },
    { value: "webhook", label: "Webhook", placeholder: "https://your-webhook-url.com" },
  ];

  const selectedType = channelTypes.find((t) => t.value === channelForm.type);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Notifications</h1>
          <p className="text-panel-muted text-sm mt-1">
            Configure notification channels and alert preferences
          </p>
        </div>
        <Button
          onClick={fetchNotifications}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
        >
          <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
          Refresh
        </Button>
      </div>

      {/* Notification Channels */}
      <Card>
        <div className="p-5 border-b border-panel-border flex items-center justify-between">
          <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
            Notification Channels
          </h3>
          <Button
            onClick={() => setShowAddChannel(true)}
            className="flex items-center gap-1 px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-xs font-medium transition-colors"
          >
            <Plus size={12} />
            Add Channel
          </Button>
        </div>
        <div className="p-5">
          {loading ? (
            <div className="space-y-3">
              {[1, 2, 3].map((i) => (
                <div key={i} className="h-14 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          ) : channels.length > 0 ? (
            <div className="space-y-3">
              {channels.map((channel) => (
                <div
                  key={channel.id}
                  className="flex items-center justify-between p-3 bg-panel-bg rounded-lg border border-panel-border"
                >
                  <div className="flex items-center gap-3">
                    {getChannelIcon(channel.type)}
                    <div>
                      <p className="text-sm font-medium text-panel-text">{channel.name}</p>
                      <p className="text-xs text-panel-muted">{channel.target}</p>
                    </div>
                  </div>
                  <button
                    onClick={() => toggleChannel(channel.id, channel.enabled)}
                    className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                      channel.enabled ? "bg-blue-600" : "bg-panel-border"
                    }`}
                  >
                    <span
                      className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                        channel.enabled ? "translate-x-6" : "translate-x-1"
                      }`}
                    />
                  </button>
                </div>
              ))}
            </div>
          ) : (
            <div className="text-center py-8">
              <Bell size={36} className="text-panel-muted/20 mx-auto mb-3" />
              <p className="text-sm text-panel-muted">No notification channels configured</p>
              <p className="text-xs text-panel-muted/60 mt-1">
                Add email, Slack, or webhook channels to receive alerts
              </p>
            </div>
          )}
        </div>
      </Card>

      {/* Alert Preferences */}
      <Card>
        <div className="p-5 border-b border-panel-border">
          <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider flex items-center gap-2">
            <Settings size={14} />
            Alert Preferences
          </h3>
        </div>
        <div className="p-5">
          <div className="space-y-3">
            {events.map((event) => (
              <div
                key={event.id}
                className="flex items-center justify-between p-3 bg-panel-bg rounded-lg border border-panel-border"
              >
                <div>
                  <p className="text-sm font-medium text-panel-text">{event.label}</p>
                  <p className="text-xs text-panel-muted">{event.description}</p>
                </div>
                <button
                  onClick={() => toggleEvent(event.id)}
                  className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                    event.enabled ? "bg-blue-600" : "bg-panel-border"
                  }`}
                >
                  <span
                    className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                      event.enabled ? "translate-x-6" : "translate-x-1"
                    }`}
                  />
                </button>
              </div>
            ))}
          </div>
        </div>
      </Card>

      {/* Webhooks */}
      <Card>
        <div className="p-5 border-b border-panel-border flex items-center justify-between">
          <h3 className="text-sm font-semibold text-panel-text uppercase tracking-wider">
            Webhooks
          </h3>
          <Button
            onClick={() => setShowAddWebhook(true)}
            className="flex items-center gap-1 px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-xs font-medium transition-colors"
          >
            <Plus size={12} />
            Add Webhook
          </Button>
        </div>
        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : webhooks.length > 0 ? (
          <Table columns={webhookColumns} data={webhooks} />
        ) : (
          <div className="text-center py-12 px-4">
            <Webhook size={36} className="text-panel-muted/20 mx-auto mb-3" />
            <p className="text-sm text-panel-muted">No webhooks configured</p>
            <p className="text-xs text-panel-muted/60 mt-1">
              Add webhooks to integrate with external services
            </p>
          </div>
        )}
      </Card>

      {/* Add Channel Modal */}
      <Modal isOpen={showAddChannel} onClose={() => setShowAddChannel(false)} title="Add Notification Channel">
        <form onSubmit={handleAddChannel} className="space-y-4">
          <div>
            <label className={labelClass}>Channel Type *</label>
            <div className="grid grid-cols-2 gap-2">
              {channelTypes.map((t) => (
                <button key={t.value} type="button" onClick={() => setChannelForm({ ...channelForm, type: t.value })}
                  className={`px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
                    channelForm.type === t.value
                      ? "bg-blue-600 text-white"
                      : "bg-panel-bg text-panel-muted border border-panel-border hover:text-panel-text"
                  }`}>
                  {t.label}
                </button>
              ))}
            </div>
          </div>
          <div>
            <label className={labelClass}>Channel Name *</label>
            <input type="text" required placeholder="My Email Alert" value={channelForm.name}
              onChange={(e) => setChannelForm({ ...channelForm, name: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>Target *</label>
            <input type="text" required placeholder={selectedType?.placeholder || ""} value={channelForm.target}
              onChange={(e) => setChannelForm({ ...channelForm, target: e.target.value })} className={inputClass} />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowAddChannel(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Adding..." : "Add Channel"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Add Webhook Modal */}
      <Modal isOpen={showAddWebhook} onClose={() => setShowAddWebhook(false)} title="Add Webhook">
        <form onSubmit={handleAddWebhook} className="space-y-4">
          <div>
            <label className={labelClass}>Webhook URL *</label>
            <input type="url" required placeholder="https://your-service.com/webhook" value={webhookForm.url}
              onChange={(e) => setWebhookForm({ ...webhookForm, url: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>Events *</label>
            <p className="text-xs text-panel-muted mb-2">Select which events trigger this webhook</p>
            <div className="flex flex-wrap gap-2">
              {availableEvents.map((event) => (
                <button key={event.id} type="button" onClick={() => toggleWebhookEvent(event.id)}
                  className={`px-2.5 py-1.5 rounded-lg text-xs font-medium transition-colors ${
                    webhookForm.events.includes(event.id)
                      ? "bg-blue-600 text-white"
                      : "bg-panel-bg text-panel-muted border border-panel-border hover:text-panel-text"
                  }`}>
                  {event.label}
                </button>
              ))}
            </div>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowAddWebhook(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">
              Cancel
            </button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Adding..." : "Add Webhook"}
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
