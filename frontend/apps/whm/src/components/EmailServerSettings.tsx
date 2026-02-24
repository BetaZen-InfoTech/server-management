import { useState } from "react";
import { Modal, Button } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Mail, Server, Shield, Key, Bug, Loader, Save } from "lucide-react";

interface EmailServerConfig {
  id: string;
  hostname: string;
  domain: string;
  postfix_enabled: boolean;
  dovecot_enabled: boolean;
  spamassassin_enabled: boolean;
  opendkim_enabled: boolean;
  clamav_enabled: boolean;
  status: string;
}

interface Props {
  config: EmailServerConfig;
  onClose: () => void;
  onSaved: () => void;
}

export default function EmailServerSettings({ config, onClose, onSaved }: Props) {
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({
    hostname: config.hostname,
    domain: config.domain,
    spamassassin_enabled: config.spamassassin_enabled,
    opendkim_enabled: config.opendkim_enabled,
    clamav_enabled: config.clamav_enabled,
  });

  const handleSave = async () => {
    setSaving(true);
    try {
      await api.put("/whm/software/email-settings", form);
      toast.success("Email server settings updated");
      onSaved();
    } catch (err: any) {
      const msg = err.response?.data?.error?.message || "Failed to update settings";
      toast.error(msg);
    } finally {
      setSaving(false);
    }
  };

  const components = [
    {
      key: "postfix",
      icon: <Mail size={16} className="text-blue-400" />,
      label: "Postfix SMTP Server",
      desc: "Core mail transfer agent for sending and receiving email",
      enabled: true,
      locked: true,
    },
    {
      key: "dovecot",
      icon: <Server size={16} className="text-purple-400" />,
      label: "Dovecot IMAP/POP3",
      desc: "Mail delivery agent for email access via IMAP and POP3",
      enabled: true,
      locked: true,
    },
    {
      key: "spamassassin_enabled" as const,
      icon: <Shield size={16} className="text-yellow-400" />,
      label: "SpamAssassin",
      desc: "Spam filtering with Bayesian scoring and auto-learning",
      enabled: form.spamassassin_enabled,
      locked: false,
    },
    {
      key: "opendkim_enabled" as const,
      icon: <Key size={16} className="text-green-400" />,
      label: "OpenDKIM",
      desc: "DKIM email signing for better deliverability and authentication",
      enabled: form.opendkim_enabled,
      locked: false,
    },
    {
      key: "clamav_enabled" as const,
      icon: <Bug size={16} className="text-red-400" />,
      label: "ClamAV Antivirus",
      desc: "Scan incoming emails for viruses and malware",
      enabled: form.clamav_enabled,
      locked: false,
    },
  ];

  return (
    <Modal isOpen={true} onClose={onClose} title="Email Server Settings" size="lg">
      <div className="space-y-5">
        {/* Hostname & Domain */}
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Mail Hostname
            </label>
            <input
              type="text"
              value={form.hostname}
              onChange={(e) => setForm({ ...form, hostname: e.target.value })}
              placeholder="mail.example.com"
              className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Domain
            </label>
            <input
              type="text"
              value={form.domain}
              onChange={(e) => setForm({ ...form, domain: e.target.value })}
              placeholder="example.com"
              className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
        </div>

        {/* Component toggles */}
        <div>
          <h3 className="text-sm font-medium text-panel-text mb-3">Components</h3>
          <div className="space-y-2">
            {components.map((comp) => (
              <div
                key={comp.key}
                className="flex items-center gap-3 p-3 rounded-lg bg-panel-bg border border-panel-border"
              >
                {comp.icon}
                <div className="flex-1">
                  <p className="text-sm font-medium text-panel-text">{comp.label}</p>
                  <p className="text-xs text-panel-muted">{comp.desc}</p>
                </div>
                {comp.locked ? (
                  <div className="px-2 py-0.5 bg-blue-500/10 text-blue-400 text-xs rounded">
                    Required
                  </div>
                ) : (
                  <button
                    onClick={() =>
                      setForm({
                        ...form,
                        [comp.key]: !form[comp.key as keyof typeof form],
                      })
                    }
                    className={`relative w-10 h-5 rounded-full transition-colors ${
                      comp.enabled ? "bg-blue-500" : "bg-panel-border"
                    }`}
                  >
                    <span
                      className={`absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-white transition-transform ${
                        comp.enabled ? "translate-x-5" : "translate-x-0"
                      }`}
                    />
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>

        <p className="text-xs text-panel-muted">
          Toggling a component will update the configuration. You may need to manually install or remove
          the component using the software manager if changing after initial setup.
        </p>

        {/* Actions */}
        <div className="flex justify-end gap-3 pt-2 border-t border-panel-border">
          <Button
            onClick={onClose}
            className="px-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            Cancel
          </Button>
          <Button
            onClick={handleSave}
            disabled={saving}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:bg-blue-600/50 disabled:cursor-not-allowed text-white rounded-lg text-sm font-medium transition-colors"
          >
            {saving ? (
              <>
                <Loader size={14} className="animate-spin" />
                Saving...
              </>
            ) : (
              <>
                <Save size={14} />
                Save Settings
              </>
            )}
          </Button>
        </div>
      </div>
    </Modal>
  );
}
