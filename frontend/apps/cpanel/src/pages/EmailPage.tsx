import React, { useEffect, useState } from "react";
import { Card, Button, Table, Modal, StatusBadge, PasswordInput, SearchableSelect, confirmAction, copyToClipboard, usePagination } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Mail,
  Plus,
  Trash2,
  Search,
  Eye,
  EyeOff,
  Edit,
  Settings,
  ExternalLink,
  Send,
  Shield,
  Key,
  Copy,
  RefreshCw,
  ArrowRight,
  Upload,
  Download,
  KeyRound,
  AlertTriangle,
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

  // Bulk operations — selection survives across pages so the operator
  // can pick row 3 on page 1, row 7 on page 2, then run the action
  // against everything checked. Same pattern as the WHM EmailPage.
  const [selectedIDs, setSelectedIDs] = useState<Set<string>>(new Set());
  const [showBulkUpload, setShowBulkUpload] = useState(false);
  const [bulkUploadFile, setBulkUploadFile] = useState<File | null>(null);
  const [bulkUploading, setBulkUploading] = useState(false);
  const [bulkUploadResult, setBulkUploadResult] = useState<any>(null);
  const [showBulkDelete, setShowBulkDelete] = useState(false);
  const [bulkDeleteStep, setBulkDeleteStep] = useState<"request" | "confirm" | "result">("request");
  const [bulkDeleteOTP, setBulkDeleteOTP] = useState({ token: "", code: "", email: "", count: 0, addresses: [] as string[] });
  const [bulkDeleteResult, setBulkDeleteResult] = useState<any>(null);
  const [bulkDeleteBusy, setBulkDeleteBusy] = useState(false);
  const [showBulkExport, setShowBulkExport] = useState(false);
  const [bulkExportStep, setBulkExportStep] = useState<"request" | "confirm">("request");
  const [bulkExportOTP, setBulkExportOTP] = useState({ token: "", code: "", email: "", count: 0 });
  const [bulkExportFormat, setBulkExportFormat] = useState<"csv" | "xlsx">("csv");
  const [bulkExportBusy, setBulkExportBusy] = useState(false);

  // Forwarder bulk operations — separate state from mailbox bulk so
  // tabbing back and forth doesn't lose selection. Mirrors WHM
  // EmailPage's parallel set.
  const [selectedForwarderIDs, setSelectedForwarderIDs] = useState<Set<string>>(new Set());
  const [showFwdBulkUpload, setShowFwdBulkUpload] = useState(false);
  const [fwdBulkUploadFile, setFwdBulkUploadFile] = useState<File | null>(null);
  const [fwdBulkUploading, setFwdBulkUploading] = useState(false);
  const [fwdBulkUploadResult, setFwdBulkUploadResult] = useState<any>(null);
  const [showFwdBulkDelete, setShowFwdBulkDelete] = useState(false);
  const [fwdBulkDeleteStep, setFwdBulkDeleteStep] = useState<"request" | "confirm" | "result">("request");
  const [fwdBulkDeleteOTP, setFwdBulkDeleteOTP] = useState({ token: "", code: "", email: "", count: 0, sources: [] as string[] });
  const [fwdBulkDeleteResult, setFwdBulkDeleteResult] = useState<any>(null);
  const [fwdBulkDeleteBusy, setFwdBulkDeleteBusy] = useState(false);

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

  // View Details / Edit Configuration / Mail Client Setup — ported from
  // the WHM EmailPage so the User Panel reaches per-row parity. All
  // three operate on the same `selectedMailbox`; the View Details modal
  // doubles as a launchpad to Edit + Mail Client Setup so the operator
  // can flow Inspect → Reconfigure or Inspect → Connect without
  // re-finding the row.
  const [showDetails, setShowDetails] = useState(false);
  const [selectedMailbox, setSelectedMailbox] = useState<Mailbox | null>(null);

  const [showEdit, setShowEdit] = useState(false);
  const [editForm, setEditForm] = useState({ quota_mb: 1024, send_limit_per_hour: 100, password: "" });
  const [savingEdit, setSavingEdit] = useState(false);

  const [showConnect, setShowConnect] = useState(false);
  const [connectMailbox, setConnectMailbox] = useState<Mailbox | null>(null);

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
      const res = await api.get("/email", { params: { limit: 10000 } });
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
      const res = await api.get("/email/forwarders", { params: { limit: 10000 } });
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

  // View / Edit / Connect openers. Mirror of the WHM page so the same
  // icon row order (Eye → Edit → Settings → Send-test → Webmail →
  // Trash) reads the same on both panels.
  const openDetails = (m: Mailbox) => {
    setSelectedMailbox(m);
    setShowDetails(true);
  };
  const openEdit = (m: Mailbox) => {
    setSelectedMailbox(m);
    setEditForm({ quota_mb: m.quota_mb, send_limit_per_hour: m.send_limit_per_hour, password: "" });
    setShowEdit(true);
  };
  const openConnect = (m: Mailbox) => {
    setConnectMailbox(m);
    setShowConnect(true);
  };

  const handleEdit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedMailbox) return;
    setSavingEdit(true);
    try {
      const updates: Record<string, unknown> = {
        quota_mb: editForm.quota_mb,
        send_limit_per_hour: editForm.send_limit_per_hour,
      };
      // Empty password = leave current password alone. Backend's
      // UpdateMailbox treats omitted/empty as no-op for the password
      // field, so this matches WHM's behaviour byte-for-byte.
      if (editForm.password) updates.password = editForm.password;
      await api.put(`/email/${selectedMailbox.id}`, updates);
      toast.success(`Mailbox ${selectedMailbox.email} updated`);
      setShowEdit(false);
      fetchMailboxes();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update mailbox");
    } finally {
      setSavingEdit(false);
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
  const pgM = usePagination("cpanel-mailboxes");
  useEffect(() => { pgM.setTotal(filteredMailboxes.length); pgM.setPage(1); }, [search, filteredMailboxes.length]);
  const pagedMailboxes = filteredMailboxes.slice((pgM.page - 1) * pgM.limit, pgM.page * pgM.limit);
  const pgF = usePagination("cpanel-forwarders");
  useEffect(() => { pgF.setTotal(filteredForwarders.length); pgF.setPage(1); }, [search, filteredForwarders.length]);
  const pagedForwarders = filteredForwarders.slice((pgF.page - 1) * pgF.limit, pgF.page * pgF.limit);

  // ── Bulk-operation helpers ──────────────────────────────────────
  const toggleSelected = (id: string) => {
    setSelectedIDs((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const selectAllVisible = () => {
    setSelectedIDs((prev) => {
      const next = new Set(prev);
      filteredMailboxes.forEach((m) => next.add(m.id));
      return next;
    });
  };
  const deselectAll = () => setSelectedIDs(new Set());
  const allVisibleSelected =
    filteredMailboxes.length > 0 &&
    filteredMailboxes.every((m) => selectedIDs.has(m.id));

  // saveBlob — JWT-aware download. window.open() can't reach the
  // cpanel API because the JWT lives in localStorage and only axios's
  // interceptor attaches it. Same fix the WHM EmailPage uses.
  const saveBlob = (data: Blob, headers: Record<string, string>, fallback: string) => {
    const url = URL.createObjectURL(data);
    const a = document.createElement("a");
    a.href = url;
    const cd = headers["content-disposition"] || "";
    const m = /filename=\"?([^\";]+)\"?/.exec(cd);
    a.download = m?.[1] || fallback;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  const downloadTemplate = async (format: "csv" | "xlsx") => {
    try {
      const res = await api.get("/email/bulk-upload/template", {
        params: { format },
        responseType: "blob",
      });
      saveBlob(res.data as Blob, res.headers as Record<string, string>, `mailboxes-template.${format}`);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || e?.message || "Template download failed");
    }
  };

  const downloadExport = async (format: "csv" | "xlsx", token?: string, code?: string) => {
    try {
      const params: Record<string, string> = { format };
      if (selectedIDs.size > 0) params.ids = Array.from(selectedIDs).join(",");
      else params.all = "true";
      if (token) {
        params.token = token;
        if (code) params.code = code;
      }
      const res = await api.get("/email/export", { params, responseType: "blob" });
      saveBlob(res.data as Blob, res.headers as Record<string, string>, `mailboxes.${format}`);
      const count = selectedIDs.size > 0 ? selectedIDs.size : filteredMailboxes.length;
      toast.success(`Exported ${count} mailbox${count === 1 ? "" : "es"}${token ? " (with passwords)" : ""}`);
    } catch (e: any) {
      // Server-side errors arrive as a Blob containing JSON when
      // responseType=blob — read it back to surface an actionable
      // message instead of a generic "Export failed".
      const blob = e?.response?.data;
      let msg = e?.message || "Export failed";
      if (blob && typeof blob.text === "function") {
        try {
          const txt = await blob.text();
          const parsed = JSON.parse(txt);
          msg = parsed?.error?.message || msg;
        } catch { /* not JSON — keep default msg */ }
      }
      toast.error(msg);
    }
  };

  const handleBulkUpload = async () => {
    if (!bulkUploadFile) return;
    setBulkUploading(true);
    try {
      const fd = new FormData();
      fd.append("file", bulkUploadFile);
      // No explicit Content-Type — axios + the browser auto-set it
      // together with the multipart boundary. See v3.1.41 fix.
      const res = await api.post("/email/bulk-upload", fd);
      setBulkUploadResult(res.data?.data);
      fetchMailboxes();
      const r = res.data?.data;
      toast.success(`Created ${r?.successes ?? 0} mailbox${(r?.successes ?? 0) === 1 ? "" : "es"}${r?.generated ? `, ${r.generated} auto-generated password(s)` : ""}`);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Bulk upload failed");
    } finally {
      setBulkUploading(false);
    }
  };

  const handleBulkDeleteRequestOTP = async () => {
    setBulkDeleteBusy(true);
    try {
      const res = await api.post("/email/bulk-delete/request-otp", {
        ids: Array.from(selectedIDs),
      });
      const d = res.data?.data;
      setBulkDeleteOTP({
        token: d?.token || "",
        code: "",
        email: d?.email || "",
        count: d?.mailbox_count || 0,
        addresses: d?.addresses || [],
      });
      setBulkDeleteStep("confirm");
      if (d?.mailer_enabled) {
        toast.success(`OTP sent to ${d.email}`);
      } else {
        toast(`SMTP not configured — code printed to journalctl -u serverpanel`);
      }
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to request OTP");
    } finally {
      setBulkDeleteBusy(false);
    }
  };
  const handleBulkDeleteConfirm = async () => {
    setBulkDeleteBusy(true);
    try {
      const res = await api.post("/email/bulk-delete/confirm", {
        token: bulkDeleteOTP.token,
        code: bulkDeleteOTP.code,
      });
      setBulkDeleteResult(res.data?.data);
      setBulkDeleteStep("result");
      const r = res.data?.data;
      toast.success(`Deleted ${r?.successes ?? 0} mailbox${(r?.successes ?? 0) === 1 ? "" : "es"}${r?.failures ? `, ${r.failures} failed` : ""}`);
      setSelectedIDs(new Set());
      fetchMailboxes();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Confirm failed");
    } finally {
      setBulkDeleteBusy(false);
    }
  };

  const handleBulkExportRequestOTP = async () => {
    setBulkExportBusy(true);
    try {
      const ids = selectedIDs.size > 0 ? Array.from(selectedIDs) : filteredMailboxes.map((m) => m.id);
      const res = await api.post("/email/bulk-export/request-otp", { ids });
      const d = res.data?.data;
      setBulkExportOTP({
        token: d?.token || "",
        code: "",
        email: d?.email || "",
        count: d?.mailbox_count || 0,
      });
      setBulkExportStep("confirm");
      if (d?.mailer_enabled) {
        toast.success(`OTP sent to ${d.email}`);
      } else {
        toast(`SMTP not configured — code printed to journalctl -u serverpanel`);
      }
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to request OTP");
    } finally {
      setBulkExportBusy(false);
    }
  };
  const handleBulkExportConfirm = () => {
    if (!bulkExportOTP.code) {
      toast.error("Enter the 6-digit code");
      return;
    }
    downloadExport(bulkExportFormat, bulkExportOTP.token, bulkExportOTP.code);
    setShowBulkExport(false);
    setBulkExportStep("request");
    setBulkExportOTP({ token: "", code: "", email: "", count: 0 });
  };

  // ── Forwarder bulk helpers (cpanel) ────────────────────────────
  // 1:1 mirror of the WHM EmailPage forwarder bulk handlers — same
  // URLs (cpanel routes are auto-tenant-scoped at the service layer).
  const toggleForwarderSelected = (id: string) => {
    setSelectedForwarderIDs((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const selectAllVisibleForwarders = () => {
    setSelectedForwarderIDs((prev) => {
      const next = new Set(prev);
      filteredForwarders.forEach((f) => next.add(f.id));
      return next;
    });
  };
  const deselectAllForwarders = () => setSelectedForwarderIDs(new Set());
  const allVisibleForwardersSelected =
    filteredForwarders.length > 0 &&
    filteredForwarders.every((f) => selectedForwarderIDs.has(f.id));

  const downloadForwarderTemplate = async (format: "csv" | "xlsx") => {
    try {
      const res = await api.get("/email/forwarders/bulk-upload/template", {
        params: { format },
        responseType: "blob",
      });
      saveBlob(res.data as Blob, res.headers as Record<string, string>, `forwarders-template.${format}`);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || e?.message || "Template download failed");
    }
  };

  const downloadForwarderExport = async (format: "csv" | "xlsx") => {
    try {
      const params: Record<string, string> = { format };
      if (selectedForwarderIDs.size > 0) params.ids = Array.from(selectedForwarderIDs).join(",");
      else params.all = "true";
      const res = await api.get("/email/forwarders/export", { params, responseType: "blob" });
      saveBlob(res.data as Blob, res.headers as Record<string, string>, `forwarders.${format}`);
      const count = selectedForwarderIDs.size > 0 ? selectedForwarderIDs.size : filteredForwarders.length;
      toast.success(`Exported ${count} forwarder${count === 1 ? "" : "s"}`);
    } catch (e: any) {
      const blob = e?.response?.data;
      let msg = e?.message || "Export failed";
      if (blob && typeof blob.text === "function") {
        try {
          const txt = await blob.text();
          const parsed = JSON.parse(txt);
          msg = parsed?.error?.message || msg;
        } catch { /* not JSON */ }
      }
      toast.error(msg);
    }
  };

  const handleFwdBulkUpload = async () => {
    if (!fwdBulkUploadFile) return;
    setFwdBulkUploading(true);
    try {
      const fd = new FormData();
      fd.append("file", fwdBulkUploadFile);
      // No explicit Content-Type — axios + the browser auto-set it
      // together with the multipart boundary. See v3.1.41 fix.
      const res = await api.post("/email/forwarders/bulk-upload", fd);
      setFwdBulkUploadResult(res.data?.data);
      fetchForwarders();
      const r = res.data?.data;
      const updated = r?.updates ?? 0;
      toast.success(
        `Created ${(r?.successes ?? 0) - updated} new, updated ${updated} forwarder${updated === 1 ? "" : "s"}` +
          (r?.failures ? `, ${r.failures} failed` : "")
      );
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Bulk upload failed");
    } finally {
      setFwdBulkUploading(false);
    }
  };

  const handleFwdBulkDeleteRequestOTP = async () => {
    setFwdBulkDeleteBusy(true);
    try {
      const res = await api.post("/email/forwarders/bulk-delete/request-otp", {
        ids: Array.from(selectedForwarderIDs),
      });
      const d = res.data?.data;
      setFwdBulkDeleteOTP({
        token: d?.token || "",
        code: "",
        email: d?.email || "",
        count: d?.forwarder_count || 0,
        sources: d?.sources || [],
      });
      setFwdBulkDeleteStep("confirm");
      if (d?.mailer_enabled) {
        toast.success(`OTP sent to ${d.email}`);
      } else {
        toast(`SMTP not configured — code printed to journalctl -u serverpanel`);
      }
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to request OTP");
    } finally {
      setFwdBulkDeleteBusy(false);
    }
  };

  const handleFwdBulkDeleteConfirm = async () => {
    setFwdBulkDeleteBusy(true);
    try {
      const res = await api.post("/email/forwarders/bulk-delete/confirm", {
        token: fwdBulkDeleteOTP.token,
        code: fwdBulkDeleteOTP.code,
      });
      setFwdBulkDeleteResult(res.data?.data);
      setFwdBulkDeleteStep("result");
      const r = res.data?.data;
      toast.success(
        `Deleted ${r?.successes ?? 0} forwarder${(r?.successes ?? 0) === 1 ? "" : "s"}` +
          (r?.failures ? `, ${r.failures} failed` : "")
      );
      setSelectedForwarderIDs(new Set());
      fetchForwarders();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Confirm failed");
    } finally {
      setFwdBulkDeleteBusy(false);
    }
  };

  const tabs: { key: Tab; label: string; icon: any }[] = [
    { key: "mailboxes", label: "Mailboxes", icon: Mail },
    { key: "forwarders", label: "Forwarders", icon: Send },
    { key: "spam", label: "Spam", icon: Shield },
    { key: "dkim", label: "DKIM", icon: Key },
  ];

  const mailboxColumns = [
    {
      // Header is "select all visible" (filter-aware: respects the
      // search box, not just the current page). Per-row cells are
      // individual checkboxes feeding the same Set.
      header: (
        <input
          type="checkbox"
          checked={allVisibleSelected}
          onChange={() => (allVisibleSelected ? deselectAll() : selectAllVisible())}
          className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 cursor-pointer"
          title={allVisibleSelected ? "Deselect all visible" : "Select all visible (filter-aware)"}
        />
      ),
      accessor: (m: Mailbox) => (
        <input
          type="checkbox"
          checked={selectedIDs.has(m.id)}
          onChange={() => toggleSelected(m.id)}
          className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 cursor-pointer"
        />
      ),
    },
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
            onClick={() => openDetails(m)}
            title="View Details"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
          >
            <Eye size={14} />
          </button>
          <button
            onClick={() => openEdit(m)}
            title="Edit Configuration"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors"
          >
            <Edit size={14} />
          </button>
          <button
            onClick={() => openConnect(m)}
            title="Mail Client Setup"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
          >
            <Settings size={14} />
          </button>
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
      header: (
        <input
          type="checkbox"
          checked={allVisibleForwardersSelected}
          onChange={() => (allVisibleForwardersSelected ? deselectAllForwarders() : selectAllVisibleForwarders())}
          className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 cursor-pointer"
          title={allVisibleForwardersSelected ? "Deselect all visible" : "Select all visible (filter-aware)"}
        />
      ),
      accessor: (f: Forwarder) => (
        <input
          type="checkbox"
          checked={selectedForwarderIDs.has(f.id)}
          onChange={() => toggleForwarderSelected(f.id)}
          className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 cursor-pointer"
        />
      ),
    },
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
            <>
              <span title={createTooltip}>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => { setShowBulkUpload(true); setBulkUploadFile(null); setBulkUploadResult(null); }}
                  disabled={!hasDomains}
                >
                  <Upload size={14} className="mr-1" /> Bulk Upload
                </Button>
              </span>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => downloadExport("csv")}
                title={selectedIDs.size > 0 ? `Export ${selectedIDs.size} selected to CSV` : "Export all your mailboxes to CSV"}
              >
                <Download size={14} className="mr-1" /> Export
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => { setShowBulkExport(true); setBulkExportStep("request"); setBulkExportOTP({ token: "", code: "", email: "", count: 0 }); }}
                title="Export with passwords — requires OTP confirmation by email"
              >
                <KeyRound size={14} className="mr-1" /> Export w/ Passwords
              </Button>
              {selectedIDs.size > 0 && (
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => { setShowBulkDelete(true); setBulkDeleteStep("request"); setBulkDeleteResult(null); setBulkDeleteOTP({ token: "", code: "", email: "", count: 0, addresses: [] }); }}
                  title={`Delete ${selectedIDs.size} selected mailbox(es) — OTP confirmation required`}
                >
                  <Trash2 size={14} className="mr-1" /> Delete {selectedIDs.size}
                </Button>
              )}
              <span title={createTooltip}>
                <Button size="sm" onClick={() => setShowCreate(true)} disabled={!hasDomains}>
                  <Plus size={14} className="mr-1" /> Create Mailbox
                </Button>
              </span>
            </>
          )}
          {activeTab === "forwarders" && (
            <>
              <span title={createTooltip}>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => { setShowFwdBulkUpload(true); setFwdBulkUploadFile(null); setFwdBulkUploadResult(null); }}
                  disabled={!hasDomains}
                >
                  <Upload size={14} className="mr-1" /> Bulk Upload
                </Button>
              </span>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => downloadForwarderExport("csv")}
                title={selectedForwarderIDs.size > 0 ? `Export ${selectedForwarderIDs.size} selected (CSV)` : "Export all (CSV)"}
              >
                <Download size={14} className="mr-1" /> Export CSV
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => downloadForwarderExport("xlsx")}
                title={selectedForwarderIDs.size > 0 ? `Export ${selectedForwarderIDs.size} selected (XLSX)` : "Export all (XLSX)"}
              >
                <Download size={14} className="mr-1" /> Export XLSX
              </Button>
              {selectedForwarderIDs.size > 0 && (
                <Button
                  variant="danger"
                  size="sm"
                  onClick={() => { setShowFwdBulkDelete(true); setFwdBulkDeleteStep("request"); setFwdBulkDeleteResult(null); setFwdBulkDeleteOTP({ token: "", code: "", email: "", count: 0, sources: [] }); }}
                  title={`Delete ${selectedForwarderIDs.size} selected (OTP confirmation required)`}
                >
                  <Trash2 size={14} className="mr-1" /> Delete {selectedForwarderIDs.size}
                </Button>
              )}
              <span title={createTooltip}>
                <Button size="sm" onClick={() => setShowCreateForwarder(true)} disabled={!hasDomains}>
                  <Plus size={14} className="mr-1" /> Add Forwarder
                </Button>
              </span>
            </>
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
            data={pagedMailboxes as any}
            loading={loading}
            emptyMessage="No mailboxes found. Create your first mailbox."
            page={pgM.page} limit={pgM.limit} total={pgM.total}
            onPageChange={pgM.setPage} onLimitChange={pgM.setLimit}
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
            data={pagedForwarders as any}
            loading={loading}
            emptyMessage="No forwarders configured."
            page={pgF.page} limit={pgF.limit} total={pgF.total}
            onPageChange={pgF.setPage} onLimitChange={pgF.setLimit}
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
            <SearchableSelect
              required
              value={form.domain}
              onChange={(v) => setForm({ ...form, domain: v })}
              options={domainList.map((d) => ({ value: d.domain, label: d.domain }))}
              placeholder="Select domain…"
              emptyMessage="No domains match the filter"
            />
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
            <SearchableSelect
              required
              value={forwarderForm.domain}
              onChange={(v) => setForwarderForm({ ...forwarderForm, domain: v })}
              options={domainList.map((d) => ({ value: d.domain, label: d.domain }))}
              placeholder="Select domain…"
              emptyMessage="No domains match the filter"
            />
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
            <SearchableSelect
              required
              value={spamForm.domain}
              onChange={(v) => setSpamForm({ ...spamForm, domain: v })}
              options={domainList.map((d) => ({ value: d.domain, label: d.domain }))}
              placeholder="Select domain…"
              emptyMessage="No domains match the filter"
            />
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
              <div className="flex-1">
                <SearchableSelect
                  value={dkimDomain}
                  onChange={(v) => {
                    setDkimDomain(v);
                    setDkimResult(null);
                  }}
                  options={domainList.map((d) => ({ value: d.domain, label: d.domain }))}
                  placeholder="Select domain…"
                  emptyMessage="No domains match the filter"
                />
              </div>
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

      {/* View Details Modal — ported from WHM EmailPage so the User
          Panel can inspect mailbox metadata (quota, send limit, IMAP/
          SMTP server config, dates) and pivot from there to Edit or
          Mail Client Setup without re-finding the row. Read-only. */}
      <Modal isOpen={showDetails} onClose={() => setShowDetails(false)} title="Mailbox Details" size="lg">
        {selectedMailbox && (
          <div className="space-y-6">
            <div className="flex items-center gap-3 p-4 bg-panel-bg rounded-lg border border-panel-border">
              <div className="p-3 bg-blue-600/20 rounded-lg"><Mail size={24} className="text-blue-400" /></div>
              <div>
                <h3 className="text-lg font-semibold text-panel-text">{selectedMailbox.email}</h3>
                <p className="text-sm text-panel-muted">{selectedMailbox.domain}</p>
              </div>
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div className="p-4 bg-panel-bg rounded-lg border border-panel-border">
                <p className="text-xs text-panel-muted uppercase tracking-wider mb-1">Quota</p>
                <p className="text-lg font-semibold text-panel-text">{selectedMailbox.used_mb || 0} / {selectedMailbox.quota_mb} MB</p>
                <div className="w-full h-2 bg-panel-border rounded-full mt-2 overflow-hidden">
                  <div className={`h-full rounded-full ${((selectedMailbox.used_mb || 0) / (selectedMailbox.quota_mb || 1)) * 100 > 90 ? "bg-red-500" : "bg-blue-500"}`}
                    style={{ width: `${Math.min(((selectedMailbox.used_mb || 0) / (selectedMailbox.quota_mb || 1)) * 100, 100)}%` }} />
                </div>
              </div>
              <div className="p-4 bg-panel-bg rounded-lg border border-panel-border">
                <p className="text-xs text-panel-muted uppercase tracking-wider mb-1">Send Limit</p>
                <p className="text-lg font-semibold text-panel-text">{selectedMailbox.send_limit_per_hour} / hour</p>
              </div>
            </div>

            <div className="rounded-lg overflow-hidden border border-panel-border">
              <div className="bg-blue-600 px-4 py-2.5">
                <h4 className="text-sm font-semibold text-white">Secure SSL/TLS Settings (Recommended)</h4>
              </div>
              <table className="w-full text-sm">
                <tbody>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium w-[140px] bg-panel-bg/50">Username:</td>
                    <td className="px-4 py-3 text-panel-text font-mono">{selectedMailbox.email}</td>
                  </tr>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Password:</td>
                    <td className="px-4 py-3 text-panel-muted italic">Use your mailbox password.</td>
                  </tr>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Incoming Server:</td>
                    <td className="px-4 py-3">
                      <span className="text-panel-text font-mono">mail.{selectedMailbox.domain}</span>
                      <div className="flex items-center gap-4 mt-1">
                        <span className="text-xs"><span className="text-blue-400 font-semibold underline">IMAP</span> Port: <span className="text-panel-text font-mono">993</span></span>
                        <span className="text-xs"><span className="text-blue-400 font-semibold underline">POP3</span> Port: <span className="text-panel-text font-mono">995</span></span>
                      </div>
                    </td>
                  </tr>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Outgoing Server:</td>
                    <td className="px-4 py-3">
                      <span className="text-panel-text font-mono">mail.{selectedMailbox.domain}</span>
                      <div className="mt-1">
                        <span className="text-xs"><span className="text-blue-400 font-semibold underline">SMTP</span> Port: <span className="text-panel-text font-mono">465</span></span>
                      </div>
                    </td>
                  </tr>
                  <tr>
                    <td colSpan={2} className="px-4 py-3 text-panel-muted text-xs bg-panel-bg/30">
                      IMAP, POP3, and SMTP require authentication.
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>

            <details className="group">
              <summary className="text-sm text-blue-400 cursor-pointer hover:text-blue-300 transition-colors flex items-center gap-1">
                Show Non SSL/TLS Settings
                <svg className="w-3 h-3 transition-transform group-open:rotate-180" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" /></svg>
              </summary>
              <div className="mt-3 rounded-lg overflow-hidden border border-panel-border">
                <div className="bg-panel-surface px-4 py-2.5">
                  <h4 className="text-sm font-semibold text-panel-text">Non-SSL Settings (Not Recommended)</h4>
                </div>
                <table className="w-full text-sm">
                  <tbody>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium w-[140px] bg-panel-bg/50">Username:</td>
                      <td className="px-4 py-3 text-panel-text font-mono">{selectedMailbox.email}</td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Password:</td>
                      <td className="px-4 py-3 text-panel-muted italic">Use your mailbox password.</td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Incoming Server:</td>
                      <td className="px-4 py-3">
                        <span className="text-panel-text font-mono">mail.{selectedMailbox.domain}</span>
                        <div className="flex items-center gap-4 mt-1">
                          <span className="text-xs">IMAP Port: <span className="text-panel-text font-mono">143</span></span>
                          <span className="text-xs">POP3 Port: <span className="text-panel-text font-mono">110</span></span>
                        </div>
                      </td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Outgoing Server:</td>
                      <td className="px-4 py-3">
                        <span className="text-panel-text font-mono">mail.{selectedMailbox.domain}</span>
                        <div className="mt-1">
                          <span className="text-xs">SMTP Port: <span className="text-panel-text font-mono">587</span></span>
                        </div>
                      </td>
                    </tr>
                    <tr>
                      <td colSpan={2} className="px-4 py-3 text-panel-muted text-xs bg-panel-bg/30">
                        IMAP, POP3, and SMTP require authentication.
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </details>

            <div className="grid grid-cols-2 gap-4">
              <div className="p-3 bg-panel-bg rounded-lg border border-panel-border">
                <p className="text-xs text-panel-muted uppercase tracking-wider mb-1">Created</p>
                <p className="text-sm font-medium text-panel-text">{selectedMailbox.created_at ? new Date(selectedMailbox.created_at).toLocaleString() : "-"}</p>
              </div>
              <div className="p-3 bg-panel-bg rounded-lg border border-panel-border">
                <p className="text-xs text-panel-muted uppercase tracking-wider mb-1">Last Updated</p>
                <p className="text-sm font-medium text-panel-text">{selectedMailbox.updated_at ? new Date(selectedMailbox.updated_at).toLocaleString() : "-"}</p>
              </div>
            </div>

            <div className="flex justify-end gap-3 pt-2 border-t border-panel-border">
              <button onClick={() => { setShowDetails(false); openEdit(selectedMailbox); }}
                className="px-4 py-2 text-sm bg-yellow-600 hover:bg-yellow-700 text-white rounded-lg font-medium transition-colors flex items-center gap-2">
                <Edit size={14} /> Edit Configuration
              </button>
              <button onClick={() => { setShowDetails(false); openConnect(selectedMailbox); }}
                className="px-4 py-2 text-sm bg-green-600 hover:bg-green-700 text-white rounded-lg font-medium transition-colors flex items-center gap-2">
                <Settings size={14} /> Mail Client Setup
              </button>
            </div>
          </div>
        )}
      </Modal>

      {/* Edit Configuration Modal — quota / send limit / optional new
          password. Empty password leaves the existing one alone (the
          backend treats omitted/empty as no-op). */}
      <Modal isOpen={showEdit} onClose={() => setShowEdit(false)} title={`Edit: ${selectedMailbox?.email || ""}`}>
        <form onSubmit={handleEdit} className="space-y-4">
          <div className="p-3 bg-blue-500/10 border border-blue-500/20 rounded-lg text-sm text-blue-300">
            Updating configuration for <strong>{selectedMailbox?.email}</strong>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Quota (MB)</label>
              <input type="number" min={0} value={editForm.quota_mb}
                onChange={(e) => setEditForm({ ...editForm, quota_mb: parseInt(e.target.value) || 0 })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Send Limit/Hour</label>
              <input type="number" min={0} value={editForm.send_limit_per_hour}
                onChange={(e) => setEditForm({ ...editForm, send_limit_per_hour: parseInt(e.target.value) || 0 })} className={inputClass} />
            </div>
          </div>
          <div>
            <label className={labelClass}>New Password (leave blank to keep current)</label>
            <PasswordInput minLength={8} placeholder="Enter new password" value={editForm.password}
              onChange={(v) => setEditForm({ ...editForm, password: v })} inputClassName={inputClass} />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowEdit(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Cancel</button>
            <button type="submit" disabled={savingEdit}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {savingEdit ? "Saving..." : "Save Changes"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Mail Client Setup Modal — read-only IMAP/SMTP cheat-sheet plus
          a short how-to-connect cribsheet for Outlook / Thunderbird /
          Gmail / Apple Mail. No backend call; all values are derived
          from the mailbox's domain. */}
      <Modal isOpen={showConnect} onClose={() => setShowConnect(false)} title="Mail Client Setup" size="lg">
        {connectMailbox && (
          <div className="space-y-5">
            <div className="flex items-center gap-3 p-4 bg-panel-bg rounded-lg border border-panel-border">
              <div className="p-3 bg-blue-600/20 rounded-lg"><Mail size={24} className="text-blue-400" /></div>
              <div>
                <h3 className="text-lg font-semibold text-panel-text">{connectMailbox.email}</h3>
                <p className="text-sm text-panel-muted">Use the settings below to configure your email client</p>
              </div>
            </div>

            {/* Two-line callout for the gotchas Gmail / Outlook 365 / strict
                clients hit. Username-must-be-full and TLS-cert-must-cover-mail
                are 95% of "auth fails with the right password" reports. */}
            <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-xs text-panel-text space-y-1.5">
              <div className="font-semibold text-amber-300 flex items-center gap-1.5"><Shield size={12} /> Two things to know before you connect</div>
              <p>1. <strong>Username MUST be the FULL email</strong> ({connectMailbox.email}). Mail clients that auto-fill just the local part ("{connectMailbox.email.split("@")[0]}") will fail with "authentication error".</p>
              <p>2. <strong>Strict clients (Gmail / Outlook 365)</strong> validate the TLS cert hostname. If they reject auth even with the right password, ask your provider to issue a Let's Encrypt cert covering <code className="text-amber-200">mail.{connectMailbox.domain}</code>.</p>
            </div>

            <div className="rounded-lg overflow-hidden border border-panel-border">
              <div className="bg-blue-600 px-4 py-2.5">
                <h4 className="text-sm font-semibold text-white">Secure SSL/TLS Settings (Recommended)</h4>
              </div>
              <table className="w-full text-sm">
                <tbody>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium w-[160px] bg-panel-bg/50">Username:</td>
                    <td className="px-4 py-3 text-panel-text font-mono">{connectMailbox.email}</td>
                  </tr>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Password:</td>
                    <td className="px-4 py-3 text-panel-muted italic">Use your mailbox password.</td>
                  </tr>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Incoming Server:</td>
                    <td className="px-4 py-3">
                      <span className="text-panel-text font-mono">mail.{connectMailbox.domain}</span>
                      <div className="flex items-center gap-4 mt-1">
                        <span className="text-xs"><span className="text-blue-400 font-semibold underline">IMAP</span> Port: <span className="text-panel-text font-mono">993</span></span>
                        <span className="text-xs"><span className="text-blue-400 font-semibold underline">POP3</span> Port: <span className="text-panel-text font-mono">995</span></span>
                      </div>
                    </td>
                  </tr>
                  <tr className="border-b border-panel-border">
                    <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Outgoing Server:</td>
                    <td className="px-4 py-3">
                      <span className="text-panel-text font-mono">mail.{connectMailbox.domain}</span>
                      <div className="mt-1">
                        <span className="text-xs"><span className="text-blue-400 font-semibold underline">SMTP</span> Port: <span className="text-panel-text font-mono">465</span></span>
                      </div>
                    </td>
                  </tr>
                  <tr>
                    <td colSpan={2} className="px-4 py-3 text-panel-muted text-xs bg-panel-bg/30">
                      IMAP, POP3, and SMTP require authentication.
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>

            <details className="group">
              <summary className="text-sm text-blue-400 cursor-pointer hover:text-blue-300 transition-colors flex items-center gap-1">
                Show Non SSL/TLS Settings
                <svg className="w-3 h-3 transition-transform group-open:rotate-180" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" /></svg>
              </summary>
              <div className="mt-3 rounded-lg overflow-hidden border border-panel-border">
                <div className="bg-panel-surface px-4 py-2.5">
                  <h4 className="text-sm font-semibold text-panel-text">Non-SSL Settings (Not Recommended)</h4>
                </div>
                <table className="w-full text-sm">
                  <tbody>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium w-[160px] bg-panel-bg/50">Username:</td>
                      <td className="px-4 py-3 text-panel-text font-mono">{connectMailbox.email}</td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Password:</td>
                      <td className="px-4 py-3 text-panel-muted italic">Use your mailbox password.</td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Incoming Server:</td>
                      <td className="px-4 py-3">
                        <span className="text-panel-text font-mono">mail.{connectMailbox.domain}</span>
                        <div className="flex items-center gap-4 mt-1">
                          <span className="text-xs">IMAP Port: <span className="text-panel-text font-mono">143</span></span>
                          <span className="text-xs">POP3 Port: <span className="text-panel-text font-mono">110</span></span>
                        </div>
                      </td>
                    </tr>
                    <tr className="border-b border-panel-border">
                      <td className="px-4 py-3 text-panel-muted font-medium bg-panel-bg/50">Outgoing Server:</td>
                      <td className="px-4 py-3">
                        <span className="text-panel-text font-mono">mail.{connectMailbox.domain}</span>
                        <div className="mt-1">
                          <span className="text-xs">SMTP Port: <span className="text-panel-text font-mono">587</span></span>
                        </div>
                      </td>
                    </tr>
                    <tr>
                      <td colSpan={2} className="px-4 py-3 text-panel-muted text-xs bg-panel-bg/30">
                        IMAP, POP3, and SMTP require authentication.
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </details>

            <div className="p-4 bg-panel-bg rounded-lg border border-panel-border">
              <h4 className="text-sm font-semibold text-panel-text mb-3">How to connect</h4>
              <div className="space-y-2 text-sm text-panel-muted">
                <p><strong className="text-panel-text">Outlook:</strong> File &gt; Add Account &gt; Manual setup &gt; IMAP &gt; Enter settings above</p>
                <p><strong className="text-panel-text">Thunderbird:</strong> Account Settings &gt; Add Mail Account &gt; Manual config &gt; Enter settings above</p>
                <p><strong className="text-panel-text">Gmail (Android/iOS):</strong> Settings &gt; Add Account &gt; Other &gt; IMAP &gt; Enter settings above</p>
                <p><strong className="text-panel-text">Apple Mail:</strong> Preferences &gt; Accounts &gt; Add &gt; Other Mail &gt; Enter settings above</p>
              </div>
            </div>

            {/* Port + encryption pairing — Gmail's wizard mislabels its
                radio buttons in a confusing way. "SSL" = implicit TLS
                (port 465); "TLS" = STARTTLS upgrade (port 587). Picking
                the wrong combination yields "Couldn't connect to
                server". */}
            <div className="p-4 bg-blue-500/5 rounded-lg border border-blue-500/30 text-sm">
              <h4 className="text-sm font-semibold text-panel-text mb-2 flex items-center gap-1.5">
                <Mail size={14} className="text-blue-400" />
                Gmail / Outlook 365 — pick the matching port and encryption
              </h4>
              <table className="w-full text-xs mt-2">
                <thead>
                  <tr className="text-panel-muted">
                    <th className="text-left py-1">Port</th>
                    <th className="text-left py-1">Encryption</th>
                    <th className="text-left py-1">Gmail "Send mail as" radio</th>
                  </tr>
                </thead>
                <tbody>
                  <tr className="border-t border-panel-border">
                    <td className="py-1.5 font-mono text-panel-text">465</td>
                    <td className="py-1.5">implicit TLS / SMTPS</td>
                    <td className="py-1.5"><strong className="text-blue-300">"SSL" radio</strong> (recommended)</td>
                  </tr>
                  <tr className="border-t border-panel-border">
                    <td className="py-1.5 font-mono text-panel-text">587</td>
                    <td className="py-1.5">STARTTLS upgrade</td>
                    <td className="py-1.5"><strong className="text-blue-300">"TLS" radio</strong></td>
                  </tr>
                </tbody>
              </table>
              <p className="text-xs text-panel-muted mt-2">
                "SSL" + 587 or "TLS" + 465 are the two wrong combinations — Gmail returns "Couldn't connect to the server". Username always = the FULL email; never just the local part.
              </p>
            </div>
          </div>
        )}
      </Modal>

      {/* ─── Bulk Upload Modal ────────────────────────────────────── */}
      <Modal isOpen={showBulkUpload} onClose={() => setShowBulkUpload(false)} title="Bulk Upload Mailboxes" size="lg">
        {!bulkUploadResult ? (
          <div className="space-y-4">
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 px-3 py-2 text-xs text-blue-200/80">
              Required column: <code className="font-mono">email</code>. Optional: <code className="font-mono">domain</code> (auto-derived from email), <code className="font-mono">password</code> (BLANK = auto-generate, returned in result), <code className="font-mono">quota_mb</code>, <code className="font-mono">send_limit_per_hour</code>. CSV or XLSX accepted. Each domain must already exist under your account; rows for unknown domains fail with a clear error.
            </div>
            <div className="flex gap-2">
              <Button variant="secondary" size="sm" onClick={() => downloadTemplate("csv")}>
                <Download size={12} className="mr-1" /> Download CSV template
              </Button>
              <Button variant="secondary" size="sm" onClick={() => downloadTemplate("xlsx")}>
                <Download size={12} className="mr-1" /> Download XLSX template
              </Button>
            </div>
            <div>
              <label className={labelClass}>File</label>
              <input
                type="file"
                accept=".csv,.xlsx,application/vnd.ms-excel,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,text/csv"
                onChange={(e) => setBulkUploadFile(e.target.files?.[0] || null)}
                className={inputClass}
              />
              {bulkUploadFile && <p className="text-[11px] text-panel-muted mt-1">{bulkUploadFile.name} · {(bulkUploadFile.size / 1024).toFixed(1)} KB</p>}
            </div>
            <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
              <button onClick={() => setShowBulkUpload(false)} className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted hover:text-panel-text">Cancel</button>
              <Button onClick={handleBulkUpload} disabled={!bulkUploadFile || bulkUploading}>
                <Upload size={14} className="mr-1" /> {bulkUploading ? "Uploading…" : "Upload"}
              </Button>
            </div>
          </div>
        ) : (
          <div className="space-y-3">
            <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-300">
              <strong>{bulkUploadResult.successes}</strong> created
              {bulkUploadResult.generated > 0 && <> · <strong>{bulkUploadResult.generated}</strong> auto-generated password(s)</>}
              {bulkUploadResult.failures > 0 && <> · <strong className="text-red-300">{bulkUploadResult.failures}</strong> failed</>}
            </div>
            {(bulkUploadResult.items || []).some((it: any) => it.generated_password) && (
              <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs text-amber-200/90">
                <AlertTriangle size={12} className="inline mr-1" /> Auto-generated passwords are shown ONCE — copy them now. They're encrypted at rest and can be re-exported (with OTP) later.
              </div>
            )}
            <div className="rounded-lg border border-panel-border max-h-72 overflow-y-auto">
              <table className="w-full text-xs">
                <thead className="bg-panel-bg/60 sticky top-0">
                  <tr className="text-left text-panel-muted">
                    <th className="px-3 py-2 font-medium">Row</th>
                    <th className="px-3 py-2 font-medium">Email</th>
                    <th className="px-3 py-2 font-medium">Status</th>
                    <th className="px-3 py-2 font-medium">Generated Password</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-panel-border/40">
                  {(bulkUploadResult.items || []).map((it: any) => (
                    <tr key={it.row_number}>
                      <td className="px-3 py-1.5 text-panel-muted">{it.row_number}</td>
                      <td className="px-3 py-1.5 font-mono text-panel-text">{it.email}</td>
                      <td className="px-3 py-1.5">{it.success ? <span className="text-emerald-400">ok</span> : <span className="text-red-400" title={it.error}>{it.error}</span>}</td>
                      <td className="px-3 py-1.5 font-mono">
                        {it.generated_password ? (
                          <button onClick={() => { navigator.clipboard.writeText(it.generated_password); toast.success("Copied"); }} className="text-amber-300 hover:text-amber-200" title="Click to copy">{it.generated_password}</button>
                        ) : "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="flex justify-end pt-2">
              <Button onClick={() => { setShowBulkUpload(false); setBulkUploadResult(null); setBulkUploadFile(null); }}>Done</Button>
            </div>
          </div>
        )}
      </Modal>

      {/* ─── Bulk Delete (OTP-gated) Modal ────────────────────────── */}
      <Modal isOpen={showBulkDelete} onClose={() => setShowBulkDelete(false)} title="Bulk Delete Mailboxes" size="lg">
        {bulkDeleteStep === "request" && (
          <div className="space-y-3">
            <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-300">
              <AlertTriangle size={14} className="inline mr-1" /> About to delete <strong>{selectedIDs.size}</strong> mailbox(es). A 6-digit confirmation code will be emailed to your registered address before any deletion runs. The code expires in 10 minutes.
            </div>
            <p className="text-xs text-panel-muted">Click "Send code" to receive the OTP, then enter it on the next step. Up to 5 wrong codes per request.</p>
            <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
              <button onClick={() => setShowBulkDelete(false)} className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted">Cancel</button>
              <Button onClick={handleBulkDeleteRequestOTP} disabled={bulkDeleteBusy || selectedIDs.size === 0}>
                {bulkDeleteBusy ? "Sending…" : "Send code"}
              </Button>
            </div>
          </div>
        )}
        {bulkDeleteStep === "confirm" && (
          <div className="space-y-3">
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 px-3 py-2 text-xs text-blue-200/80">
              Code sent to <strong>{bulkDeleteOTP.email}</strong> — covers <strong>{bulkDeleteOTP.count}</strong> mailbox(es).
            </div>
            <div>
              <label className={labelClass}>6-digit code</label>
              <input
                type="text"
                inputMode="numeric"
                maxLength={6}
                value={bulkDeleteOTP.code}
                onChange={(e) => setBulkDeleteOTP({ ...bulkDeleteOTP, code: e.target.value.replace(/\D/g, "") })}
                placeholder="123456"
                className={inputClass + " font-mono tracking-widest text-lg"}
                autoFocus
              />
            </div>
            <details className="text-xs">
              <summary className="cursor-pointer text-panel-muted hover:text-panel-text">Show {bulkDeleteOTP.addresses.length} mailbox(es)</summary>
              <ul className="mt-2 ml-4 max-h-40 overflow-y-auto text-panel-muted/80 font-mono">
                {bulkDeleteOTP.addresses.map((a) => <li key={a}>{a}</li>)}
              </ul>
            </details>
            <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
              <button onClick={() => setShowBulkDelete(false)} className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted">Cancel</button>
              <Button onClick={handleBulkDeleteConfirm} disabled={bulkDeleteBusy || bulkDeleteOTP.code.length !== 6}>
                {bulkDeleteBusy ? "Deleting…" : `Delete ${bulkDeleteOTP.count}`}
              </Button>
            </div>
          </div>
        )}
        {bulkDeleteStep === "result" && bulkDeleteResult && (
          <div className="space-y-3">
            <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-300">
              <strong>{bulkDeleteResult.successes}</strong> deleted
              {bulkDeleteResult.failures > 0 && <> · <strong className="text-red-300">{bulkDeleteResult.failures}</strong> failed</>}
            </div>
            <div className="rounded-lg border border-panel-border max-h-72 overflow-y-auto">
              <table className="w-full text-xs">
                <thead className="bg-panel-bg/60 sticky top-0">
                  <tr className="text-left text-panel-muted">
                    <th className="px-3 py-2 font-medium">Email</th>
                    <th className="px-3 py-2 font-medium">Status</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-panel-border/40">
                  {(bulkDeleteResult.items || []).map((it: any) => (
                    <tr key={it.id}>
                      <td className="px-3 py-1.5 font-mono text-panel-text">{it.email}</td>
                      <td className="px-3 py-1.5">{it.success ? <span className="text-emerald-400">deleted</span> : <span className="text-red-400" title={it.error}>{it.error}</span>}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="flex justify-end pt-2">
              <Button onClick={() => setShowBulkDelete(false)}>Done</Button>
            </div>
          </div>
        )}
      </Modal>

      {/* ─── Bulk Export with Passwords (OTP-gated) Modal ─────────── */}
      <Modal isOpen={showBulkExport} onClose={() => setShowBulkExport(false)} title="Export Mailboxes with Passwords" size="md">
        {bulkExportStep === "request" && (
          <div className="space-y-3">
            <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-300">
              <AlertTriangle size={14} className="inline mr-1" /> Plaintext passwords for <strong>{selectedIDs.size > 0 ? selectedIDs.size : filteredMailboxes.length}</strong> mailbox(es) will be written into the export file. A 6-digit confirmation code will be emailed first.
            </div>
            <div className="flex items-center gap-3">
              <label className={labelClass}>Format</label>
              <div className="flex gap-1 bg-panel-bg border border-panel-border rounded-lg p-1">
                {(["csv", "xlsx"] as const).map((f) => (
                  <button key={f} onClick={() => setBulkExportFormat(f)} className={`px-3 py-1 text-xs rounded ${bulkExportFormat === f ? "bg-blue-600 text-white" : "text-panel-muted"}`}>{f.toUpperCase()}</button>
                ))}
              </div>
            </div>
            <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
              <button onClick={() => setShowBulkExport(false)} className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted">Cancel</button>
              <Button onClick={handleBulkExportRequestOTP} disabled={bulkExportBusy}>
                {bulkExportBusy ? "Sending…" : "Send code"}
              </Button>
            </div>
          </div>
        )}
        {bulkExportStep === "confirm" && (
          <div className="space-y-3">
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 px-3 py-2 text-xs text-blue-200/80">
              Code sent to <strong>{bulkExportOTP.email}</strong> — covers <strong>{bulkExportOTP.count}</strong> mailbox(es). The file will download immediately after the code is verified.
            </div>
            <div>
              <label className={labelClass}>6-digit code</label>
              <input
                type="text"
                inputMode="numeric"
                maxLength={6}
                value={bulkExportOTP.code}
                onChange={(e) => setBulkExportOTP({ ...bulkExportOTP, code: e.target.value.replace(/\D/g, "") })}
                placeholder="123456"
                className={inputClass + " font-mono tracking-widest text-lg"}
                autoFocus
              />
            </div>
            <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
              <button onClick={() => setShowBulkExport(false)} className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted">Cancel</button>
              <Button onClick={handleBulkExportConfirm} disabled={bulkExportOTP.code.length !== 6}>
                Download {bulkExportFormat.toUpperCase()}
              </Button>
            </div>
          </div>
        )}
      </Modal>

      {/* ─── Forwarder Bulk Upload Modal (cpanel) ───────────────── */}
      <Modal isOpen={showFwdBulkUpload} onClose={() => setShowFwdBulkUpload(false)} title="Bulk Upload Forwarders" size="lg">
        {!fwdBulkUploadResult ? (
          <div className="space-y-4">
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 px-3 py-2 text-xs text-blue-200/80">
              Required columns: <code className="font-mono">source</code>, <code className="font-mono">destinations</code>. Optional: <code className="font-mono">keep_copy</code> (true/false). The source's @part determines the domain — only your own domains are accepted. <code className="font-mono">destinations</code> accepts a comma- OR semicolon-separated list. Idempotent — re-uploading the same source overwrites its destinations.
            </div>
            <div className="flex gap-2">
              <Button variant="secondary" size="sm" onClick={() => downloadForwarderTemplate("csv")}>
                <Download size={12} className="mr-1" /> Download CSV template
              </Button>
              <Button variant="secondary" size="sm" onClick={() => downloadForwarderTemplate("xlsx")}>
                <Download size={12} className="mr-1" /> Download XLSX template
              </Button>
            </div>
            <div>
              <label className={labelClass}>File</label>
              <input
                type="file"
                accept=".csv,.xlsx,application/vnd.ms-excel,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,text/csv"
                onChange={(e) => setFwdBulkUploadFile(e.target.files?.[0] || null)}
                className={inputClass}
              />
              {fwdBulkUploadFile && <p className="text-[11px] text-panel-muted mt-1">{fwdBulkUploadFile.name} · {(fwdBulkUploadFile.size / 1024).toFixed(1)} KB</p>}
            </div>
            <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
              <button onClick={() => setShowFwdBulkUpload(false)} className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted hover:text-panel-text">Cancel</button>
              <Button onClick={handleFwdBulkUpload} disabled={!fwdBulkUploadFile || fwdBulkUploading}>
                <Upload size={14} className="mr-1" /> {fwdBulkUploading ? "Uploading…" : "Upload"}
              </Button>
            </div>
          </div>
        ) : (
          <div className="space-y-3">
            <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-300">
              <strong>{fwdBulkUploadResult.successes ?? 0}</strong> processed
              {(fwdBulkUploadResult.updates ?? 0) > 0 && <> · <strong>{fwdBulkUploadResult.updates}</strong> updated</>}
              {(fwdBulkUploadResult.failures ?? 0) > 0 && <> · <strong className="text-red-300">{fwdBulkUploadResult.failures}</strong> failed</>}
            </div>
            <div className="rounded-lg border border-panel-border max-h-72 overflow-y-auto">
              <table className="w-full text-xs">
                <thead className="bg-panel-bg/60 sticky top-0">
                  <tr className="text-left text-panel-muted">
                    <th className="px-3 py-2 font-medium">Row</th>
                    <th className="px-3 py-2 font-medium">Source</th>
                    <th className="px-3 py-2 font-medium">→ Destinations</th>
                    <th className="px-3 py-2 font-medium">Status</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-panel-border/40">
                  {(fwdBulkUploadResult.items || []).map((it: any) => (
                    <tr key={it.row}>
                      <td className="px-3 py-1.5 text-panel-muted">{it.row}</td>
                      <td className="px-3 py-1.5 font-mono text-panel-text">{it.source}</td>
                      <td className="px-3 py-1.5 font-mono text-panel-muted/80">{(it.destinations || []).join(", ")}</td>
                      <td className="px-3 py-1.5">
                        {it.success
                          ? (it.updated ? <span className="text-amber-300">updated</span> : <span className="text-emerald-400">created</span>)
                          : <span className="text-red-400" title={it.error}>{it.error}</span>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="flex justify-end pt-2">
              <Button onClick={() => { setShowFwdBulkUpload(false); setFwdBulkUploadResult(null); setFwdBulkUploadFile(null); }}>Done</Button>
            </div>
          </div>
        )}
      </Modal>

      {/* ─── Forwarder Bulk Delete (OTP-gated) Modal (cpanel) ──── */}
      <Modal isOpen={showFwdBulkDelete} onClose={() => setShowFwdBulkDelete(false)} title="Bulk Delete Forwarders" size="lg">
        {fwdBulkDeleteStep === "request" && (
          <div className="space-y-3">
            <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-300">
              <AlertTriangle size={14} className="inline mr-1" /> About to delete <strong>{selectedForwarderIDs.size}</strong> forwarder(s). A 6-digit confirmation code will be emailed before any deletion runs. The code expires in 10 minutes.
            </div>
            <p className="text-xs text-panel-muted">Click "Send code" to receive the OTP, then enter it on the next step. Up to 5 wrong codes per request.</p>
            <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
              <button onClick={() => setShowFwdBulkDelete(false)} className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted">Cancel</button>
              <Button variant="danger" onClick={handleFwdBulkDeleteRequestOTP} disabled={fwdBulkDeleteBusy || selectedForwarderIDs.size === 0}>
                {fwdBulkDeleteBusy ? "Sending…" : "Send code"}
              </Button>
            </div>
          </div>
        )}
        {fwdBulkDeleteStep === "confirm" && (
          <div className="space-y-3">
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 px-3 py-2 text-xs text-blue-200/80">
              Code sent to <strong>{fwdBulkDeleteOTP.email}</strong> — covers <strong>{fwdBulkDeleteOTP.count}</strong> forwarder(s).
            </div>
            <div>
              <label className={labelClass}>6-digit code</label>
              <input
                type="text"
                inputMode="numeric"
                maxLength={6}
                value={fwdBulkDeleteOTP.code}
                onChange={(e) => setFwdBulkDeleteOTP({ ...fwdBulkDeleteOTP, code: e.target.value.replace(/\D/g, "") })}
                placeholder="123456"
                className={inputClass + " font-mono tracking-widest text-lg"}
                autoFocus
              />
            </div>
            <details className="text-xs">
              <summary className="cursor-pointer text-panel-muted hover:text-panel-text">Show {fwdBulkDeleteOTP.sources.length} forwarder(s)</summary>
              <ul className="mt-2 ml-4 max-h-40 overflow-y-auto text-panel-muted/80 font-mono">
                {fwdBulkDeleteOTP.sources.map((s) => <li key={s}>{s}</li>)}
              </ul>
            </details>
            <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
              <button onClick={() => setShowFwdBulkDelete(false)} className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted">Cancel</button>
              <Button variant="danger" onClick={handleFwdBulkDeleteConfirm} disabled={fwdBulkDeleteBusy || fwdBulkDeleteOTP.code.length !== 6}>
                {fwdBulkDeleteBusy ? "Deleting…" : `Delete ${fwdBulkDeleteOTP.count}`}
              </Button>
            </div>
          </div>
        )}
        {fwdBulkDeleteStep === "result" && fwdBulkDeleteResult && (
          <div className="space-y-3">
            <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-300">
              <strong>{fwdBulkDeleteResult.successes}</strong> deleted
              {fwdBulkDeleteResult.failures > 0 && <> · <strong className="text-red-300">{fwdBulkDeleteResult.failures}</strong> failed</>}
            </div>
            <div className="rounded-lg border border-panel-border max-h-72 overflow-y-auto">
              <table className="w-full text-xs">
                <thead className="bg-panel-bg/60 sticky top-0">
                  <tr className="text-left text-panel-muted">
                    <th className="px-3 py-2 font-medium">Source</th>
                    <th className="px-3 py-2 font-medium">Status</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-panel-border/40">
                  {(fwdBulkDeleteResult.items || []).map((it: any) => (
                    <tr key={it.id}>
                      <td className="px-3 py-1.5 font-mono text-panel-text">{it.source}</td>
                      <td className="px-3 py-1.5">{it.success ? <span className="text-emerald-400">deleted</span> : <span className="text-red-400" title={it.error}>{it.error}</span>}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="flex justify-end pt-2">
              <Button onClick={() => setShowFwdBulkDelete(false)}>Done</Button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
