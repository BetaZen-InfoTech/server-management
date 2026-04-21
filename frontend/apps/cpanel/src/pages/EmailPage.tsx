import React, { useEffect, useState } from "react";
import { Card, Button, Table, Modal, StatusBadge, confirmAction, copyToClipboard } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Mail,
  Plus,
  Trash2,
  Search,
  Eye,
  EyeOff,
  ExternalLink,
  Send,
  Shield,
  Key,
  Copy,
  RefreshCw,
  ArrowRight,
} from "lucide-react";

interface Mailbox {
  id: string;
  email: string;
  domain: string;
  quota_mb: number;
  used_mb: number;
  send_limit_per_hour: number;
  created_at: string;
  updated_at?: string;
}

interface Forwarder {
  id: string;
  source: string;
  destinations: string[];
  keep_copy: boolean;
  domain: string;
  created_at: string;
}

interface DomainOption {
  id: string;
  domain: string;
}

interface DkimInfo {
  domain: string;
  selector: string;
  dns_record: string;
  record_type: string;
  record_name: string;
}

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

type Tab = "mailboxes" | "forwarders" | "spam" | "dkim";

export default function EmailPage() {
  const [activeTab, setActiveTab] = useState<Tab>("mailboxes");
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [forwarders, setForwarders] = useState<Forwarder[]>([]);
  const [domainList, setDomainList] = useState<DomainOption[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  // Create mailbox
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [form, setForm] = useState({
    username: "",
    domain: "",
    password: "",
    quota_mb: 1024,
    send_limit_per_hour: 100,
  });

  // Create forwarder
  const [showCreateForwarder, setShowCreateForwarder] = useState(false);
  const [creatingForwarder, setCreatingForwarder] = useState(false);
  const [forwarderForm, setForwarderForm] = useState({
    source: "",
    domain: "",
    destinations: "",
    keep_copy: true,
  });

  // Spam settings
  const [showSpam, setShowSpam] = useState(false);
  const [savingSpam, setSavingSpam] = useState(false);
  const [spamForm, setSpamForm] = useState({
    domain: "",
    threshold: 5.0,
    spam_action: "flag",
    whitelist: "",
    blacklist: "",
    clamav_enabled: false,
  });

  // DKIM
  const [showDkim, setShowDkim] = useState(false);
  const [dkimDomain, setDkimDomain] = useState("");
  const [dkimLoading, setDkimLoading] = useState(false);
  const [dkimResult, setDkimResult] = useState<DkimInfo | null>(null);

  useEffect(() => {
    fetchMailboxes();
    fetchDomains();
  }, []);

  useEffect(() => {
    if (activeTab === "forwarders") fetchForwarders();
  }, [activeTab]);

  const fetchDomains = async () => {
    try {
      const res = await api.get("/domains?limit=500");
      setDomainList(res.data.data || []);
    } catch {
      // keep empty; create buttons disable themselves
    }
  };

  const fetchMailboxes = async () => {
    setLoading(true);
    try {
      const res = await api.get("/email");
      setMailboxes(res.data.data || []);
    } catch {
      toast.error("Failed to load email accounts");
    } finally {
      setLoading(false);
    }
  };

  const fetchForwarders = async () => {
    setLoading(true);
    try {
      const res = await api.get("/email/forwarders");
      setForwarders(res.data.data || []);
    } catch {
      toast.error("Failed to load forwarders");
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.username.trim() || !form.domain.trim() || !form.password.trim()) {
      toast.error("Please fill in all required fields");
      return;
    }
    setCreating(true);
    try {
      const email = `${form.username}@${form.domain}`;
      await api.post("/email", {
        email,
        password: form.password,
        domain: form.domain,
        quota_mb: form.quota_mb || 1024,
        send_limit_per_hour: form.send_limit_per_hour || 100,
      });
      toast.success(`Mailbox ${email} created`);
      setShowCreate(false);
      setForm({ username: "", domain: "", password: "", quota_mb: 1024, send_limit_per_hour: 100 });
      fetchMailboxes();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || err?.response?.data?.message || "Failed to create mailbox");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string, email: string) => {
    if (
      !(await confirmAction({
        title: "Delete mailbox?",
        description: `Delete mailbox "${email}"? This cannot be undone.`,
        danger: true,
        confirmLabel: "Delete",
      }))
    )
      return;
    try {
      await api.delete(`/email/${id}`);
      toast.success("Mailbox deleted");
      setMailboxes((prev) => prev.filter((m) => m.id !== id));
    } catch {
      toast.error("Failed to delete mailbox");
    }
  };

  const handleCreateForwarder = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!forwarderForm.source.trim() || !forwarderForm.domain.trim() || !forwarderForm.destinations.trim()) {
      toast.error("Please fill in all required fields");
      return;
    }
    setCreatingForwarder(true);
    try {
      // source may be a local part ("sales") or full "sales@domain.com"
      const source = forwarderForm.source.includes("@")
        ? forwarderForm.source
        : `${forwarderForm.source}@${forwarderForm.domain}`;
      await api.post("/email/forwarders", {
        source,
        destinations: forwarderForm.destinations
          .split(",")
          .map((d) => d.trim())
          .filter(Boolean),
        // backend CreateForwarder accepts a single destination string too —
        // we always send the array variant to keep shapes consistent.
        destination: forwarderForm.destinations.split(",")[0].trim(),
        domain: forwarderForm.domain,
        keep_copy: forwarderForm.keep_copy,
      });
      toast.success("Forwarder created");
      setShowCreateForwarder(false);
      setForwarderForm({ source: "", domain: "", destinations: "", keep_copy: true });
      fetchForwarders();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || err?.response?.data?.message || "Failed to create forwarder");
    } finally {
      setCreatingForwarder(false);
    }
  };

  const handleDeleteForwarder = async (id: string, source: string) => {
    if (
      !(await confirmAction({
        title: "Delete forwarder?",
        description: `Delete forwarder for ${source}?`,
        danger: true,
        confirmLabel: "Delete",
      }))
    )
      return;
    try {
      await api.delete(`/email/forwarders/${id}`);
      toast.success("Forwarder deleted");
      setForwarders((prev) => prev.filter((f) => f.id !== id));
    } catch {
      toast.error("Failed to delete forwarder");
    }
  };

  const openSpam = () => {
    setSpamForm({
      domain: domainList[0]?.domain || "",
      threshold: 5.0,
      spam_action: "flag",
      whitelist: "",
      blacklist: "",
      clamav_enabled: false,
    });
    setShowSpam(true);
  };

  const handleSaveSpam = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!spamForm.domain) {
      toast.error("Please select a domain");
      return;
    }
    setSavingSpam(true);
    try {
      await api.put(`/email/spam-settings/${spamForm.domain}`, {
        threshold: spamForm.threshold,
        spam_threshold: spamForm.threshold,
        spam_action: spamForm.spam_action,
        whitelist: spamForm.whitelist
          ? spamForm.whitelist.split(",").map((s) => s.trim()).filter(Boolean)
          : [],
        blacklist: spamForm.blacklist
          ? spamForm.blacklist.split(",").map((s) => s.trim()).filter(Boolean)
          : [],
        clamav_enabled: spamForm.clamav_enabled,
      });
      toast.success("Spam settings saved");
      setShowSpam(false);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to save spam settings");
    } finally {
      setSavingSpam(false);
    }
  };

  const openDkim = () => {
    setDkimDomain(domainList[0]?.domain || "");
    setDkimResult(null);
    setShowDkim(true);
  };

  const handleSetupDkim = async () => {
    if (!dkimDomain) {
      toast.error("Please select a domain");
      return;
    }
    setDkimLoading(true);
    setDkimResult(null);
    try {
      const res = await api.post(`/email/dkim/${dkimDomain}`);
      setDkimResult(res.data.data as DkimInfo);
      toast.success(`DKIM configured for ${dkimDomain}`);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to set up DKIM");
    } finally {
      setDkimLoading(false);
    }
  };

  const openWebmail = async (email: string) => {
    try {
      const res = await api.post("/email/webmail-token", { email });
      const url = res.data.data?.url;
      if (url) {
        window.open(url, "_blank");
      } else {
        window.open("/webmail/", "_blank");
      }
    } catch {
      toast.error("Failed to generate webmail login");
      window.open("/webmail/", "_blank");
    }
  };

  // Test-email state + handler. Prompts for a recipient and calls the
  // new /email/:id/test endpoint. On success we toast + optionally
  // reveal the SMTP trace for debugging; on failure the trace is the
  // interesting bit (auth failure, relay rejection, etc.), so we show
  // it in a modal so the operator can copy/paste it.
  const [testTarget, setTestTarget] = useState<{ id: string; email: string } | null>(null);
  const [testTo, setTestTo] = useState("");
  const [testBusy, setTestBusy] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; trace: string } | null>(null);
  const openTest = (m: { id: string; email: string }) => {
    setTestTarget(m);
    setTestTo("");
    setTestResult(null);
  };
  const runTest = async () => {
    if (!testTarget) return;
    const to = testTo.trim();
    if (!to || !to.includes("@")) {
      toast.error("Enter a recipient email");
      return;
    }
    setTestBusy(true);
    setTestResult(null);
    try {
      const res = await api.post(`/email/${testTarget.id}/test`, { to });
      setTestResult({ ok: true, trace: res.data.data?.trace || "Sent." });
      toast.success(`Test email sent from ${testTarget.email}`);
    } catch (err: any) {
      const data = err?.response?.data?.error;
      setTestResult({
        ok: false,
        trace: data?.details?.trace || data?.message || "Unknown failure",
      });
      toast.error(data?.message || "Test email failed");
    } finally {
      setTestBusy(false);
    }
  };

  const copy = async (text: string, label = "Copied") => {
    if (await copyToClipboard(text)) toast.success(label);
    else toast.error("Copy failed");
  };

  const hasDomains = domainList.length > 0;

  const filteredMailboxes = mailboxes.filter((m) =>
    (m.email || "").toLowerCase().includes(search.toLowerCase())
  );
  const filteredForwarders = forwarders.filter((f) =>
    (f.source || "").toLowerCase().includes(search.toLowerCase())
  );

  const tabs: { key: Tab; label: string; icon: any }[] = [
    { key: "mailboxes", label: "Mailboxes", icon: Mail },
    { key: "forwarders", label: "Forwarders", icon: Send },
    { key: "spam", label: "Spam", icon: Shield },
    { key: "dkim", label: "DKIM", icon: Key },
  ];

  const mailboxColumns = [
    {
      header: "Email Address",
      accessor: (m: Mailbox) => (
        <div className="flex items-center gap-2">
          <Mail size={14} className="text-cyan-400" />
          <span className="font-medium text-panel-text">{m.email}</span>
        </div>
      ),
    },
    {
      header: "Domain",
      accessor: (m: Mailbox) => (
        <span className="text-panel-muted text-sm font-mono">{m.domain || "—"}</span>
      ),
    },
    {
      header: "Storage",
      accessor: (m: Mailbox) => {
        const used = m.used_mb || 0;
        const total = m.quota_mb || 0;
        const percent = total > 0 ? Math.round((used / total) * 100) : 0;
        return (
          <div className="min-w-[120px]">
            <div className="flex items-center justify-between mb-1">
              <span className="text-xs text-panel-muted">
                {used} / {total} MB
              </span>
              <span className="text-xs text-panel-muted">{percent}%</span>
            </div>
            <div className="w-full h-1.5 bg-panel-bg rounded-full overflow-hidden">
              <div
                className={`h-full rounded-full ${
                  percent > 90 ? "bg-red-500" : percent > 70 ? "bg-yellow-500" : "bg-blue-500"
                }`}
                style={{ width: `${Math.min(percent, 100)}%` }}
              />
            </div>
          </div>
        );
      },
    },
    {
      header: "Send Limit",
      accessor: (m: Mailbox) => (
        <span className="text-panel-muted text-sm">{m.send_limit_per_hour || 0}/hr</span>
      ),
    },
    {
      header: "Created",
      accessor: (m: Mailbox) => (
        <span className="text-panel-muted text-sm">
          {m.created_at ? new Date(m.created_at).toLocaleDateString() : "—"}
        </span>
      ),
    },
    {
      header: "Actions",
      accessor: (m: Mailbox) => (
        <div className="flex items-center justify-end gap-1">
          <button
            onClick={() => openTest({ id: m.id, email: m.email })}
            title="Send test email"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-emerald-400 transition-colors"
          >
            <Send size={14} />
          </button>
          <button
            onClick={() => openWebmail(m.email)}
            title="Open Webmail"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-cyan-400 transition-colors"
          >
            <ExternalLink size={14} />
          </button>
          <button
            onClick={() => handleDelete(m.id, m.email)}
            title="Delete"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
          >
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  const forwarderColumns = [
    {
      header: "Source",
      accessor: (f: Forwarder) => (
        <div className="flex items-center gap-2">
          <Mail size={14} className="text-cyan-400" />
          <span className="font-medium text-panel-text">{f.source}</span>
        </div>
      ),
    },
    {
      header: "Destination",
      accessor: (f: Forwarder) => (
        <div className="flex flex-col gap-1">
          {(f.destinations || []).map((d, i) => (
            <div key={i} className="flex items-center gap-1 text-sm text-panel-muted">
              <ArrowRight size={12} className="text-green-400" />
              {d}
            </div>
          ))}
        </div>
      ),
    },
    {
      header: "Domain",
      accessor: (f: Forwarder) => (
        <span className="text-panel-muted text-sm font-mono">{f.domain || "—"}</span>
      ),
    },
    {
      header: "Keep Copy",
      accessor: (f: Forwarder) => (
        <StatusBadge status={f.keep_copy ? "active" : "inactive"} />
      ),
    },
    {
      header: "Actions",
      accessor: (f: Forwarder) => (
        <div className="flex items-center justify-end gap-1">
          <button
            onClick={() => handleDeleteForwarder(f.id, f.source)}
            title="Delete"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
          >
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  const createTooltip = hasDomains ? "" : "Add a domain first";

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Email</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage mailboxes, forwarders, spam filtering and DKIM
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              if (activeTab === "mailboxes") fetchMailboxes();
              else if (activeTab === "forwarders") fetchForwarders();
              fetchDomains();
            }}
          >
            <RefreshCw size={14} className={loading ? "animate-spin mr-1" : "mr-1"} /> Refresh
          </Button>
          {activeTab === "mailboxes" && (
            <span title={createTooltip}>
              <Button size="sm" onClick={() => setShowCreate(true)} disabled={!hasDomains}>
                <Plus size={14} className="mr-1" /> Create Mailbox
              </Button>
            </span>
          )}
          {activeTab === "forwarders" && (
            <span title={createTooltip}>
              <Button size="sm" onClick={() => setShowCreateForwarder(true)} disabled={!hasDomains}>
                <Plus size={14} className="mr-1" /> Add Forwarder
              </Button>
            </span>
          )}
          {activeTab === "spam" && (
            <span title={createTooltip}>
              <Button size="sm" onClick={openSpam} disabled={!hasDomains}>
                <Shield size={14} className="mr-1" /> Configure Spam
              </Button>
            </span>
          )}
          {activeTab === "dkim" && (
            <span title={createTooltip}>
              <Button size="sm" onClick={openDkim} disabled={!hasDomains}>
                <Key size={14} className="mr-1" /> Generate DKIM
              </Button>
            </span>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 bg-panel-surface/50 p-1 rounded-lg border border-panel-border w-fit">
        {tabs.map((tab) => (
          <button
            key={tab.key}
            onClick={() => {
              setActiveTab(tab.key);
              setSearch("");
            }}
            className={`flex items-center gap-2 px-4 py-2 rounded-md text-sm font-medium transition-colors ${
              activeTab === tab.key
                ? "bg-blue-600 text-white"
                : "text-panel-muted hover:text-panel-text hover:bg-panel-surface"
            }`}
          >
            <tab.icon size={14} /> {tab.label}
          </button>
        ))}
      </div>

      {!hasDomains && (
        <Card>
          <div className="p-4 flex items-start gap-3 text-sm">
            <Shield size={16} className="text-amber-400 mt-0.5 shrink-0" />
            <div className="text-panel-muted">
              No domains yet. Add a domain on the{" "}
              <span className="text-panel-text font-medium">Domains</span> page before
              creating mailboxes, forwarders, or configuring spam/DKIM.
            </div>
          </div>
        </Card>
      )}

      {/* Mailboxes */}
      {activeTab === "mailboxes" && (
        <Card
          title="Email Accounts"
          description="Create and manage mailboxes for your domains"
        >
          <div className="mb-4">
            <div className="relative max-w-xs">
              <Search
                size={16}
                className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
              />
              <input
                type="text"
                placeholder="Search mailboxes..."
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-blue-500/40"
              />
            </div>
          </div>
          <Table
            columns={mailboxColumns}
            data={filteredMailboxes as any}
            loading={loading}
            emptyMessage="No mailboxes found. Create your first mailbox."
          />
        </Card>
      )}

      {/* Forwarders */}
      {activeTab === "forwarders" && (
        <Card
          title="Email Forwarders"
          description="Redirect mail from one address to one or more destinations"
        >
          <div className="mb-4">
            <div className="relative max-w-xs">
              <Search
                size={16}
                className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
              />
              <input
                type="text"
                placeholder="Search forwarders..."
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-blue-500/40"
              />
            </div>
          </div>
          <Table
            columns={forwarderColumns}
            data={filteredForwarders as any}
            loading={loading}
            emptyMessage="No forwarders configured."
          />
        </Card>
      )}

      {/* Spam placeholder */}
      {activeTab === "spam" && (
        <Card
          title="Spam Filtering"
          description="Configure per-domain spam thresholds, allow/block lists, and antivirus"
        >
          <div className="p-4 text-sm text-panel-muted">
            Click <span className="text-panel-text font-medium">Configure Spam</span> in the
            top-right to pick a domain and tune the SpamAssassin threshold, white/blacklists,
            spam action, and ClamAV antivirus scanning.
          </div>
        </Card>
      )}

      {/* DKIM placeholder */}
      {activeTab === "dkim" && (
        <Card
          title="DKIM (DomainKeys Identified Mail)"
          description="Generate DKIM signing keys to improve outbound deliverability"
        >
          <div className="p-4 text-sm text-panel-muted space-y-2">
            <p>
              DKIM adds a cryptographic signature to mail leaving your server. Receiving
              servers use the published public key to verify the message wasn't tampered
              with — this significantly reduces the chance of your mail landing in spam.
            </p>
            <p>
              Click <span className="text-panel-text font-medium">Generate DKIM</span> to
              pick a domain. We'll generate the key, wire OpenDKIM, and show you the TXT
              record to publish at your DNS provider.
            </p>
          </div>
        </Card>
      )}

      {/* Create Mailbox Modal */}
      <Modal
        isOpen={showCreate}
        onClose={() => setShowCreate(false)}
        title="Create Mailbox"
      >
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className={labelClass}>Domain *</label>
            <select
              required
              value={form.domain}
              onChange={(e) => setForm({ ...form, domain: e.target.value })}
              className={inputClass}
            >
              <option value="">Select domain...</option>
              {domainList.map((d) => (
                <option key={d.id || d.domain} value={d.domain}>
                  {d.domain}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className={labelClass}>Username *</label>
            <div className="flex items-stretch">
              <input
                type="text"
                required
                placeholder="john"
                value={form.username}
                onChange={(e) =>
                  setForm({
                    ...form,
                    username: e.target.value.toLowerCase().replace(/[^a-z0-9._-]/g, ""),
                  })
                }
                className={inputClass + " rounded-r-none border-r-0"}
              />
              <span className="px-3 py-2 bg-panel-surface border border-panel-border text-panel-muted text-sm rounded-r-lg whitespace-nowrap flex items-center">
                @{form.domain || "domain.com"}
              </span>
            </div>
            {form.username && form.domain && (
              <p className="text-xs text-panel-muted mt-1">
                Full address:{" "}
                <span className="text-blue-400 font-mono">
                  {form.username}@{form.domain}
                </span>
              </p>
            )}
          </div>
          <div>
            <label className={labelClass}>Password *</label>
            <div className="relative">
              <input
                type={showPassword ? "text" : "password"}
                required
                minLength={8}
                value={form.password}
                onChange={(e) => setForm({ ...form, password: e.target.value })}
                placeholder="Strong password (min 8 chars)"
                className={inputClass + " pr-10"}
              />
              <button
                type="button"
                onClick={() => setShowPassword(!showPassword)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-panel-muted hover:text-panel-text"
              >
                {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
              </button>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Storage Quota (MB)</label>
              <input
                type="number"
                min={0}
                value={form.quota_mb}
                onChange={(e) =>
                  setForm({ ...form, quota_mb: parseInt(e.target.value, 10) || 0 })
                }
                className={inputClass}
              />
            </div>
            <div>
              <label className={labelClass}>Send Limit / Hour</label>
              <input
                type="number"
                min={0}
                value={form.send_limit_per_hour}
                onChange={(e) =>
                  setForm({
                    ...form,
                    send_limit_per_hour: parseInt(e.target.value, 10) || 0,
                  })
                }
                className={inputClass}
              />
            </div>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button variant="secondary" type="button" onClick={() => setShowCreate(false)}>
              Cancel
            </Button>
            <Button type="submit" loading={creating}>
              Create Mailbox
            </Button>
          </div>
        </form>
      </Modal>

      {/* Test Email Modal */}
      <Modal
        isOpen={!!testTarget}
        onClose={() => { if (!testBusy) { setTestTarget(null); setTestResult(null); } }}
        title={testTarget ? `Test send — ${testTarget.email}` : "Test email"}
      >
        <div className="space-y-4">
          <p className="text-xs text-panel-muted">
            Authenticates as <span className="text-panel-text font-medium">{testTarget?.email}</span> on
            <code className="mx-1 px-1 py-0.5 rounded bg-panel-bg border border-panel-border">localhost:587</code>
            and submits a short test message. The full SMTP exchange is shown below — useful to diagnose auth, DKIM, or relay failures.
          </p>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1">Recipient</label>
            <input
              type="email"
              autoFocus
              value={testTo}
              onChange={(e) => setTestTo(e.target.value)}
              placeholder="you@example.com"
              disabled={testBusy}
              className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40"
            />
          </div>
          {testResult && (
            <div
              className={`p-3 rounded-lg border text-xs font-mono whitespace-pre-wrap max-h-64 overflow-auto ${
                testResult.ok
                  ? "bg-emerald-500/5 border-emerald-500/30 text-emerald-300"
                  : "bg-red-500/5 border-red-500/30 text-red-300"
              }`}
            >
              {testResult.trace}
            </div>
          )}
          <div className="flex items-center justify-end gap-2">
            <Button variant="ghost" onClick={() => { setTestTarget(null); setTestResult(null); }} disabled={testBusy}>
              Close
            </Button>
            <Button onClick={runTest} loading={testBusy}>
              <Send size={14} className="mr-1" /> Send test
            </Button>
          </div>
        </div>
      </Modal>

      {/* Create Forwarder Modal */}
      <Modal
        isOpen={showCreateForwarder}
        onClose={() => setShowCreateForwarder(false)}
        title="Create Email Forwarder"
      >
        <form onSubmit={handleCreateForwarder} className="space-y-4">
          <div>
            <label className={labelClass}>Domain *</label>
            <select
              required
              value={forwarderForm.domain}
              onChange={(e) =>
                setForwarderForm({ ...forwarderForm, domain: e.target.value })
              }
              className={inputClass}
            >
              <option value="">Select domain...</option>
              {domainList.map((d) => (
                <option key={d.id || d.domain} value={d.domain}>
                  {d.domain}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className={labelClass}>Source Address *</label>
            <div className="flex items-stretch">
              <input
                type="text"
                required
                placeholder="sales"
                value={forwarderForm.source}
                onChange={(e) =>
                  setForwarderForm({
                    ...forwarderForm,
                    source: e.target.value.toLowerCase(),
                  })
                }
                className={inputClass + " rounded-r-none border-r-0"}
              />
              <span className="px-3 py-2 bg-panel-surface border border-panel-border text-panel-muted text-sm rounded-r-lg whitespace-nowrap flex items-center">
                @{forwarderForm.domain || "domain.com"}
              </span>
            </div>
            <p className="text-xs text-panel-muted mt-1">
              Enter just the local part — we'll combine it with the selected domain.
            </p>
          </div>
          <div>
            <label className={labelClass}>Forward To *</label>
            <input
              type="text"
              required
              placeholder="dest1@example.com, dest2@example.com"
              value={forwarderForm.destinations}
              onChange={(e) =>
                setForwarderForm({ ...forwarderForm, destinations: e.target.value })
              }
              className={inputClass}
            />
            <p className="text-xs text-panel-muted mt-1">
              Comma-separate multiple destinations.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="keepCopy"
              checked={forwarderForm.keep_copy}
              onChange={(e) =>
                setForwarderForm({ ...forwarderForm, keep_copy: e.target.checked })
              }
              className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 focus:ring-blue-500/40"
            />
            <label htmlFor="keepCopy" className="text-sm text-panel-text">
              Keep a copy in the source mailbox
            </label>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowCreateForwarder(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={creatingForwarder}>
              Create Forwarder
            </Button>
          </div>
        </form>
      </Modal>

      {/* Spam Settings Modal */}
      <Modal isOpen={showSpam} onClose={() => setShowSpam(false)} title="Spam Settings" size="lg">
        <form onSubmit={handleSaveSpam} className="space-y-4">
          <div>
            <label className={labelClass}>Domain *</label>
            <select
              required
              value={spamForm.domain}
              onChange={(e) => setSpamForm({ ...spamForm, domain: e.target.value })}
              className={inputClass}
            >
              <option value="">Select domain...</option>
              {domainList.map((d) => (
                <option key={d.id || d.domain} value={d.domain}>
                  {d.domain}
                </option>
              ))}
            </select>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Spam Threshold</label>
              <input
                type="number"
                step="0.5"
                min={1}
                max={10}
                value={spamForm.threshold}
                onChange={(e) =>
                  setSpamForm({ ...spamForm, threshold: parseFloat(e.target.value) || 5 })
                }
                className={inputClass}
              />
              <p className="text-xs text-panel-muted mt-1">
                Lower = stricter. Recommended: 5.0
              </p>
            </div>
            <div>
              <label className={labelClass}>Action on Spam</label>
              <select
                value={spamForm.spam_action}
                onChange={(e) =>
                  setSpamForm({ ...spamForm, spam_action: e.target.value })
                }
                className={inputClass}
              >
                <option value="flag">Flag (mark subject)</option>
                <option value="move-to-junk">Move to Junk</option>
                <option value="discard">Discard</option>
              </select>
            </div>
          </div>
          <div>
            <label className={labelClass}>Whitelist (comma-separated)</label>
            <input
              type="text"
              placeholder="trusted@example.com, safe@domain.com"
              value={spamForm.whitelist}
              onChange={(e) => setSpamForm({ ...spamForm, whitelist: e.target.value })}
              className={inputClass}
            />
          </div>
          <div>
            <label className={labelClass}>Blacklist (comma-separated)</label>
            <input
              type="text"
              placeholder="spam@bad.com"
              value={spamForm.blacklist}
              onChange={(e) => setSpamForm({ ...spamForm, blacklist: e.target.value })}
              className={inputClass}
            />
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="clamav"
              checked={spamForm.clamav_enabled}
              onChange={(e) =>
                setSpamForm({ ...spamForm, clamav_enabled: e.target.checked })
              }
              className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 focus:ring-blue-500/40"
            />
            <label htmlFor="clamav" className="text-sm text-panel-text">
              Enable ClamAV antivirus scanning
            </label>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button variant="secondary" type="button" onClick={() => setShowSpam(false)}>
              Cancel
            </Button>
            <Button type="submit" loading={savingSpam}>
              Save Spam Settings
            </Button>
          </div>
        </form>
      </Modal>

      {/* DKIM Modal */}
      <Modal isOpen={showDkim} onClose={() => setShowDkim(false)} title="DKIM Setup" size="lg">
        <div className="space-y-4">
          <div className="p-3 bg-blue-500/10 border border-blue-500/20 rounded-lg text-sm text-panel-muted">
            Generate an OpenDKIM signing key for the selected domain. After generation,
            publish the TXT record below at your DNS provider so receiving mail servers
            can verify your signatures.
          </div>
          <div>
            <label className={labelClass}>Domain *</label>
            <div className="flex gap-2">
              <select
                value={dkimDomain}
                onChange={(e) => {
                  setDkimDomain(e.target.value);
                  setDkimResult(null);
                }}
                className={inputClass}
              >
                <option value="">Select domain...</option>
                {domainList.map((d) => (
                  <option key={d.id || d.domain} value={d.domain}>
                    {d.domain}
                  </option>
                ))}
              </select>
              <Button type="button" onClick={handleSetupDkim} loading={dkimLoading}>
                <Key size={14} className="mr-1" /> Generate
              </Button>
            </div>
          </div>

          {dkimResult && (
            <div className="space-y-3">
              <div className="rounded-lg overflow-hidden border border-panel-border">
                <div className="bg-blue-600 px-4 py-2">
                  <h4 className="text-sm font-semibold text-white">
                    DKIM DNS Record — publish this at your DNS provider
                  </h4>
                </div>
                <table className="w-full text-sm">
                  <tbody>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-2.5 text-panel-muted font-medium bg-panel-bg/50 w-[130px]">
                        Type
                      </td>
                      <td className="px-4 py-2.5 text-panel-text font-mono">
                        {dkimResult.record_type || "TXT"}
                      </td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-2.5 text-panel-muted font-medium bg-panel-bg/50">
                        Selector
                      </td>
                      <td className="px-4 py-2.5 text-panel-text font-mono">
                        {dkimResult.selector}
                      </td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-2.5 text-panel-muted font-medium bg-panel-bg/50">
                        Name / Host
                      </td>
                      <td className="px-4 py-2.5">
                        <div className="flex items-center gap-2">
                          <code className="text-panel-text font-mono text-xs break-all">
                            {dkimResult.record_name}
                          </code>
                          <button
                            onClick={() => copy(dkimResult.record_name, "Name copied")}
                            className="p-1 text-panel-muted hover:text-panel-text"
                            title="Copy name"
                          >
                            <Copy size={12} />
                          </button>
                        </div>
                      </td>
                    </tr>
                    <tr>
                      <td className="px-4 py-2.5 text-panel-muted font-medium bg-panel-bg/50 align-top">
                        Value
                      </td>
                      <td className="px-4 py-2.5">
                        <div className="flex items-start gap-2">
                          <code className="text-panel-text font-mono text-xs break-all whitespace-pre-wrap flex-1">
                            {dkimResult.dns_record || "(key generated — fetch from /etc/opendkim/keys/)"}
                          </code>
                          <button
                            onClick={() =>
                              copy(dkimResult.dns_record || "", "DKIM record copied")
                            }
                            className="p-1 text-panel-muted hover:text-panel-text shrink-0"
                            title="Copy value"
                          >
                            <Copy size={12} />
                          </button>
                        </div>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <p className="text-xs text-panel-muted">
                DNS changes can take up to 24 hours to propagate. Use{" "}
                <a
                  href={`https://mxtoolbox.com/SuperTool.aspx?action=dkim%3a${dkimResult.domain}%3a${dkimResult.selector}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-blue-400 hover:underline"
                >
                  MXToolbox
                </a>{" "}
                to verify your record once published.
              </p>
            </div>
          )}

          <div className="flex justify-end gap-3 pt-2">
            <Button variant="secondary" type="button" onClick={() => setShowDkim(false)}>
              Close
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
