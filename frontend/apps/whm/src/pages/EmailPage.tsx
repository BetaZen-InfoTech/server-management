import { useState, useEffect } from "react";
import { Card, Button, Table, StatusBadge, Modal, PasswordInput, SearchableSelect, confirmAction, usePagination } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Mail, Plus, RefreshCw, Search, Trash2, Edit, Eye, ExternalLink,
  Send, Shield, ArrowRight, Copy, Settings, X, Key, Download, Upload,
  AlertTriangle, KeyRound,
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

const inputClass = "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

type Tab = "mailboxes" | "forwarders" | "spam";

export default function EmailPage() {
  const [activeTab, setActiveTab] = useState<Tab>("mailboxes");
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [forwarders, setForwarders] = useState<Forwarder[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  // Create mailbox modal
  const [showCreate, setShowCreate] = useState(false);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ username: "", password: "", domain: "", quota_mb: 500, send_limit_per_hour: 100 });
  const [domainsList, setDomainsList] = useState<{ domain: string }[]>([]);

  // View details modal
  const [showDetails, setShowDetails] = useState(false);
  const [selectedMailbox, setSelectedMailbox] = useState<Mailbox | null>(null);

  // Edit mailbox modal
  const [showEdit, setShowEdit] = useState(false);
  const [editForm, setEditForm] = useState({ quota_mb: 500, send_limit_per_hour: 100, password: "" });
  const [saving, setSaving] = useState(false);

  // Create forwarder modal
  const [showCreateForwarder, setShowCreateForwarder] = useState(false);
  const [creatingForwarder, setCreatingForwarder] = useState(false);
  const [forwarderForm, setForwarderForm] = useState({ source: "", destinations: "", keep_copy: true });

  // Spam settings
  const [spamDomain, setSpamDomain] = useState("");
  const [spamForm, setSpamForm] = useState({ spam_threshold: 5.0, spam_action: "flag", whitelist: "", blacklist: "", clamav_enabled: false });
  const [savingSpam, setSavingSpam] = useState(false);

  // DKIM
  const [dkimDomain, setDkimDomain] = useState("");
  const [settingUpDkim, setSettingUpDkim] = useState(false);

  // Bulk operations — selection is a Set of mailbox ids that survives
  // pagination (operator can select page 1 row 3, page 2 row 7, etc.,
  // and the bulk action targets every checked row across pages).
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

  // Forwarder bulk operations — mirrors the mailbox set above, separate
  // state so the two surfaces don't step on each other when an operator
  // tabs back and forth. selectedForwarderIDs survives pagination the
  // same way selectedIDs does for mailboxes.
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

  useEffect(() => { fetchMailboxes(); fetchDomains(); }, []);
  useEffect(() => { if (activeTab === "forwarders") fetchForwarders(); }, [activeTab]);

  const fetchDomains = async () => {
    try {
      const res = await api.get("/domains?limit=500");
      setDomainsList(res.data.data || []);
    } catch { /* keep empty */ }
  };

  const fetchMailboxes = async () => {
    setLoading(true);
    try {
      const res = await api.get("/email/", { params: { limit: 10000 } });
      setMailboxes(res.data.data || []);
    } catch { /* keep empty */ } finally { setLoading(false); }
  };

  const fetchForwarders = async () => {
    setLoading(true);
    try {
      const res = await api.get("/email/forwarders", { params: { limit: 10000 } });
      setForwarders(res.data.data || []);
    } catch { /* keep empty */ } finally { setLoading(false); }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.username || !form.password || !form.domain) { toast.error("Please fill all required fields"); return; }
    const email = `${form.username}@${form.domain}`;
    setCreating(true);
    try {
      await api.post("/email/", { email, password: form.password, domain: form.domain, quota_mb: form.quota_mb, send_limit_per_hour: form.send_limit_per_hour });
      toast.success(`Mailbox ${email} created`);
      setShowCreate(false);
      setForm({ username: "", password: "", domain: "", quota_mb: 500, send_limit_per_hour: 100 });
      fetchMailboxes();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create mailbox");
    } finally { setCreating(false); }
  };

  const handleEdit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedMailbox) return;
    setSaving(true);
    try {
      const updates: any = { quota_mb: editForm.quota_mb, send_limit_per_hour: editForm.send_limit_per_hour };
      if (editForm.password) updates.password = editForm.password;
      await api.put(`/email/${selectedMailbox.id}`, updates);
      toast.success(`Mailbox ${selectedMailbox.email} updated`);
      setShowEdit(false);
      fetchMailboxes();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update mailbox");
    } finally { setSaving(false); }
  };

  const handleDelete = async (id: string, email: string) => {
    if (!await confirmAction({ title: "Delete?", description: `Are you sure you want to delete mailbox ${email}?`, danger: true, confirmLabel: "Delete" })) return;
    try {
      await api.delete(`/email/${id}`);
      toast.success(`Mailbox ${email} deleted`);
      fetchMailboxes();
    } catch { toast.error("Failed to delete mailbox"); }
  };

  const handleCreateForwarder = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!forwarderForm.source || !forwarderForm.destinations) { toast.error("Please fill all required fields"); return; }
    setCreatingForwarder(true);
    try {
      await api.post("/email/forwarders", {
        source: forwarderForm.source,
        destinations: forwarderForm.destinations.split(",").map((d) => d.trim()).filter(Boolean),
        keep_copy: forwarderForm.keep_copy,
      });
      toast.success("Forwarder created");
      setShowCreateForwarder(false);
      setForwarderForm({ source: "", destinations: "", keep_copy: true });
      fetchForwarders();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create forwarder");
    } finally { setCreatingForwarder(false); }
  };

  const handleDeleteForwarder = async (id: string, source: string) => {
    if (!await confirmAction({ title: "Delete?", description: `Delete forwarder for ${source}?`, danger: true, confirmLabel: "Delete" })) return;
    try {
      await api.delete(`/email/forwarders/${id}`);
      toast.success("Forwarder deleted");
      fetchForwarders();
    } catch { toast.error("Failed to delete forwarder"); }
  };

  const handleSaveSpam = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!spamDomain) { toast.error("Enter a domain"); return; }
    setSavingSpam(true);
    try {
      await api.put(`/email/spam-settings/${spamDomain}`, {
        ...spamForm,
        whitelist: spamForm.whitelist ? spamForm.whitelist.split(",").map((s) => s.trim()).filter(Boolean) : [],
        blacklist: spamForm.blacklist ? spamForm.blacklist.split(",").map((s) => s.trim()).filter(Boolean) : [],
      });
      toast.success("Spam settings updated");
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update spam settings");
    } finally { setSavingSpam(false); }
  };

  const handleSetupDkim = async () => {
    if (!dkimDomain) { toast.error("Enter a domain"); return; }
    setSettingUpDkim(true);
    try {
      await api.post(`/email/dkim/${dkimDomain}`);
      toast.success(`DKIM setup complete for ${dkimDomain}`);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to setup DKIM");
    } finally { setSettingUpDkim(false); }
  };

  // Connect modal
  const [showConnect, setShowConnect] = useState(false);
  const [connectMailbox, setConnectMailbox] = useState<Mailbox | null>(null);

  const openDetails = (m: Mailbox) => { setSelectedMailbox(m); setShowDetails(true); };
  const openEdit = (m: Mailbox) => {
    setSelectedMailbox(m);
    setEditForm({ quota_mb: m.quota_mb, send_limit_per_hour: m.send_limit_per_hour, password: "" });
    setShowEdit(true);
  };
  const openConnect = (m: Mailbox) => { setConnectMailbox(m); setShowConnect(true); };

  const openWebmail = async (email?: string) => {
    if (!email) {
      window.open("/webmail/", "_blank");
      return;
    }
    try {
      const res = await api.post("/email/webmail-token", { email });
      const url = res.data.data?.url;
      if (url) {
        window.open(url, "_blank");
      } else {
        window.open("/webmail/", "_blank");
      }
    } catch {
      toast.error("Failed to generate webmail login. Opening login page instead.");
      window.open("/webmail/", "_blank");
    }
  };

  const filteredMailboxes = mailboxes.filter((m) => (m.email || "").toLowerCase().includes(search.toLowerCase()));
  const filteredForwarders = forwarders.filter((f) => (f.source || "").toLowerCase().includes(search.toLowerCase()));
  const pgM = usePagination("whm-mailboxes");
  useEffect(() => { pgM.setTotal(filteredMailboxes.length); pgM.setPage(1); }, [search, filteredMailboxes.length]);
  const pagedMailboxes = filteredMailboxes.slice((pgM.page - 1) * pgM.limit, pgM.page * pgM.limit);

  // ── Bulk-operation helpers ──────────────────────────────────────
  // Selection-set mutation. Wrapped in helpers (rather than inlined
  // into every checkbox handler) so a future "shift-click range
  // select" or "ctrl-A select-all" can plug in here without hunting
  // through render code.
  const toggleSelected = (id: string) => {
    setSelectedIDs((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const selectAllVisible = () => {
    // "Visible" = the post-filter, post-search set — NOT just the
    // current page. Operator's mental model: if I filtered to
    // example.com mailboxes, "select all" means every example.com
    // mailbox, not just the 20 on this page.
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

  // Bulk Upload handler — accepts CSV / XLSX, server returns a
  // per-row result table (successes / failures / generated-password
  // map). Result is rendered inline in the modal so the operator
  // can copy the auto-generated passwords for any blank-password
  // rows without losing them.
  const handleBulkUpload = async () => {
    if (!bulkUploadFile) return;
    setBulkUploading(true);
    try {
      const fd = new FormData();
      fd.append("file", bulkUploadFile);
      // Do NOT pass `Content-Type: multipart/form-data` here — axios +
      // the browser auto-set it together with the random boundary
      // marker the backend's multipart parser needs to find each form
      // field. Setting the bare type ourselves can result in the
      // header being sent WITHOUT a boundary and the server then
      // rejects every field as "missing", making the bulk upload look
      // like it silently failed even though the request did land.
      // Same fix applied across the other 8 FormData upload sites in
      // this PR (v3.1.41).
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

  // saveBlob is the JWT-aware download helper. window.open() can't
  // be used because the WHM API requires a Bearer token in the
  // Authorization header — the JWT lives in localStorage and only
  // axios's interceptor attaches it, never a browser-driven nav.
  // Pattern: fetch through axios with responseType=blob, materialise
  // the result as an object URL, click a synthetic <a download>, and
  // free the URL. Filename comes from Content-Disposition; fallback
  // protects against backends that forget to send it.
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

  // Bulk Delete — request OTP first, then confirm with code. The
  // request-otp call already filters to mailboxes the caller owns
  // (CallerScope inside the service), so the modal's preview shows
  // only the in-scope subset of the operator's selection.
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

  // Bulk Export — when "include passwords" is checked, request OTP
  // first; otherwise download immediately. Goes through authenticated
  // axios (NOT window.open) for the same JWT reason as the template
  // download — a tab opened by window.open carries no Authorization
  // header and the WHM API responds with 401 unauthorized.
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
      // Server-side errors come through with an `error` JSON body but
      // because we asked for a blob, axios delivers a Blob containing
      // that JSON — read it back to surface the message instead of a
      // generic "Export failed".
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
    // The Export endpoint validates the OTP itself and triggers
    // the download. We just open it in a new tab so the browser's
    // native file-save flow takes over.
    if (!bulkExportOTP.code) {
      toast.error("Enter the 6-digit code");
      return;
    }
    downloadExport(bulkExportFormat, bulkExportOTP.token, bulkExportOTP.code);
    setShowBulkExport(false);
    setBulkExportStep("request");
    setBulkExportOTP({ token: "", code: "", email: "", count: 0 });
  };

  // ── Forwarder bulk helpers ──────────────────────────────────────
  // Same shape as the mailbox helpers above — different URLs +
  // selection set. Comments live on the mailbox side; everything
  // here is a 1:1 mirror.
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
        } catch { /* not JSON — keep default */ }
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
      // Header omitted on purpose — see comment on the mailbox bulk
      // upload above. axios + browser set Content-Type with the
      // boundary; setting it ourselves drops the boundary.
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

  const pgF = usePagination("whm-forwarders");
  useEffect(() => { pgF.setTotal(filteredForwarders.length); pgF.setPage(1); }, [search, filteredForwarders.length]);
  const pagedForwarders = filteredForwarders.slice((pgF.page - 1) * pgF.limit, pgF.page * pgF.limit);
  const uniqueDomains = [...new Set(mailboxes.map((m) => m.domain).filter(Boolean))];

  const tabs: { key: Tab; label: string; icon: any }[] = [
    { key: "mailboxes", label: "Mailboxes", icon: Mail },
    { key: "forwarders", label: "Forwarders", icon: Send },
    { key: "spam", label: "Spam & DKIM", icon: Shield },
  ];

  const mailboxColumns = [
    {
      // Header is a "select all visible" checkbox; per-row cells are
      // individual selection checkboxes. Both feed the same selectedIDs
      // Set so a multi-page selection survives navigation.
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
      header: "Address",
      accessor: (m: Mailbox) => (
        <button onClick={() => openDetails(m)} className="flex items-center gap-2 hover:text-blue-400 transition-colors">
          <Mail size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{m.email}</span>
        </button>
      ),
    },
    {
      header: "Domain",
      accessor: (m: Mailbox) => <span className="text-panel-muted text-sm">{m.domain}</span>,
    },
    {
      header: "Quota",
      accessor: (m: Mailbox) => {
        const usedMB = m.used_mb || 0;
        const totalMB = m.quota_mb || 0;
        const percent = totalMB > 0 ? Math.round((usedMB / totalMB) * 100) : 0;
        return (
          <div className="min-w-[120px]">
            <div className="flex items-center justify-between mb-1">
              <span className="text-xs text-panel-muted">{usedMB} MB / {totalMB} MB</span>
              <span className="text-xs text-panel-muted">{percent}%</span>
            </div>
            <div className="w-full h-1.5 bg-panel-bg rounded-full overflow-hidden">
              <div className={`h-full rounded-full ${percent > 90 ? "bg-red-500" : percent > 70 ? "bg-yellow-500" : "bg-blue-500"}`} style={{ width: `${percent}%` }} />
            </div>
          </div>
        );
      },
    },
    {
      header: "Send Limit",
      accessor: (m: Mailbox) => <span className="text-panel-muted text-sm">{m.send_limit_per_hour}/hr</span>,
    },
    {
      header: "Actions",
      accessor: (m: Mailbox) => (
        <div className="flex items-center gap-1">
          <button onClick={() => openDetails(m)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors" title="View Details">
            <Eye size={14} />
          </button>
          <button onClick={() => openEdit(m)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-yellow-400 transition-colors" title="Edit Configuration">
            <Edit size={14} />
          </button>
          <button onClick={() => openConnect(m)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors" title="Mail Client Setup">
            <Settings size={14} />
          </button>
          <button onClick={() => openWebmail(m.email)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-cyan-400 transition-colors" title="Open Webmail">
            <ExternalLink size={14} />
          </button>
          <button onClick={() => handleDelete(m.id, m.email)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors" title="Delete">
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
          <Mail size={14} className="text-blue-400" />
          <span className="font-medium text-panel-text">{f.source}</span>
        </div>
      ),
    },
    {
      header: "Forwards To",
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
      header: "Keep Copy",
      accessor: (f: Forwarder) => (
        <StatusBadge status={f.keep_copy ? "active" : "inactive"} />
      ),
    },
    {
      header: "Actions",
      accessor: (f: Forwarder) => (
        <button onClick={() => handleDeleteForwarder(f.id, f.source)} className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors" title="Delete">
          <Trash2 size={14} />
        </button>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Email</h1>
          <p className="text-panel-muted text-sm mt-1">Manage email mailboxes, forwarders, and security</p>
        </div>
        <div className="flex items-center gap-2">
          <Button onClick={() => openWebmail()}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm">
            <ExternalLink size={14} /> Webmail
          </Button>
          <Button onClick={() => { activeTab === "mailboxes" ? fetchMailboxes() : fetchForwarders(); }}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm">
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} /> Refresh
          </Button>
          {activeTab === "mailboxes" && (
            <>
              <Button
                onClick={() => { setShowBulkUpload(true); setBulkUploadFile(null); setBulkUploadResult(null); }}
                className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
                title="Upload CSV / XLSX with one row per mailbox"
              >
                <Upload size={14} /> Bulk Upload
              </Button>
              <Button
                onClick={() => downloadExport("csv")}
                className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
                title={selectedIDs.size > 0 ? `Export ${selectedIDs.size} selected to CSV` : "Export all mailboxes to CSV"}
              >
                <Download size={14} /> Export
              </Button>
              <Button
                onClick={() => { setShowBulkExport(true); setBulkExportStep("request"); setBulkExportOTP({ token: "", code: "", email: "", count: 0 }); }}
                className="flex items-center gap-2 px-3 py-2 bg-amber-600/10 border border-amber-500/40 text-amber-300 hover:bg-amber-600/20 rounded-lg transition-colors text-sm"
                title="Export with passwords — requires OTP confirmation by email"
              >
                <KeyRound size={14} /> Export w/ Passwords
              </Button>
              {selectedIDs.size > 0 && (
                <Button
                  onClick={() => { setShowBulkDelete(true); setBulkDeleteStep("request"); setBulkDeleteResult(null); setBulkDeleteOTP({ token: "", code: "", email: "", count: 0, addresses: [] }); }}
                  className="flex items-center gap-2 px-3 py-2 bg-red-600/10 border border-red-500/40 text-red-300 hover:bg-red-600/20 rounded-lg transition-colors text-sm"
                  title={`Delete ${selectedIDs.size} selected mailbox(es) — OTP confirmation required`}
                >
                  <Trash2 size={14} /> Delete {selectedIDs.size}
                </Button>
              )}
              <Button onClick={() => setShowCreate(true)}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
                <Plus size={14} /> Create Mailbox
              </Button>
            </>
          )}
          {activeTab === "forwarders" && (
            <>
              <Button
                onClick={() => { setShowFwdBulkUpload(true); setFwdBulkUploadFile(null); setFwdBulkUploadResult(null); }}
                className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
                title="Upload CSV / XLSX with one row per forwarder"
              >
                <Upload size={14} /> Bulk Upload
              </Button>
              <Button
                onClick={() => downloadForwarderExport("csv")}
                className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
                title={selectedForwarderIDs.size > 0 ? `Export ${selectedForwarderIDs.size} selected to CSV` : "Export all forwarders to CSV"}
              >
                <Download size={14} /> Export CSV
              </Button>
              <Button
                onClick={() => downloadForwarderExport("xlsx")}
                className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
                title={selectedForwarderIDs.size > 0 ? `Export ${selectedForwarderIDs.size} selected to XLSX` : "Export all forwarders to XLSX"}
              >
                <Download size={14} /> Export XLSX
              </Button>
              {selectedForwarderIDs.size > 0 && (
                <Button
                  onClick={() => { setShowFwdBulkDelete(true); setFwdBulkDeleteStep("request"); setFwdBulkDeleteResult(null); setFwdBulkDeleteOTP({ token: "", code: "", email: "", count: 0, sources: [] }); }}
                  className="flex items-center gap-2 px-3 py-2 bg-red-600/10 border border-red-500/40 text-red-300 hover:bg-red-600/20 rounded-lg transition-colors text-sm"
                  title={`Delete ${selectedForwarderIDs.size} selected forwarder(s) — OTP confirmation required`}
                >
                  <Trash2 size={14} /> Delete {selectedForwarderIDs.size}
                </Button>
              )}
              <Button onClick={() => setShowCreateForwarder(true)}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
                <Plus size={14} /> Add Forwarder
              </Button>
            </>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 bg-panel-surface/50 p-1 rounded-lg border border-panel-border w-fit">
        {tabs.map((tab) => (
          <button key={tab.key} onClick={() => { setActiveTab(tab.key); setSearch(""); }}
            className={`flex items-center gap-2 px-4 py-2 rounded-md text-sm font-medium transition-colors ${activeTab === tab.key ? "bg-blue-600 text-white" : "text-panel-muted hover:text-panel-text hover:bg-panel-surface"}`}>
            <tab.icon size={14} /> {tab.label}
          </button>
        ))}
      </div>

      {/* Mailboxes Tab */}
      {activeTab === "mailboxes" && (
        <>
          <Card>
            <div className="p-4">
              <div className="relative">
                <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
                <input type="text" placeholder="Search mailboxes..." value={search} onChange={(e) => setSearch(e.target.value)}
                  className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm" />
              </div>
            </div>
          </Card>
          <Card>
            {loading ? (
              <div className="p-8"><div className="space-y-3">{[1, 2, 3].map((i) => (<div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />))}</div></div>
            ) : filteredMailboxes.length > 0 ? (
              <Table columns={mailboxColumns} data={pagedMailboxes}
                page={pgM.page} limit={pgM.limit} total={pgM.total}
                onPageChange={pgM.setPage} onLimitChange={pgM.setLimit} />
            ) : (
              <div className="text-center py-16 px-4">
                <Mail size={48} className="text-panel-muted/20 mx-auto mb-4" />
                <h3 className="text-lg font-medium text-panel-text mb-1">No mailboxes found</h3>
                <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
                  {search ? "No mailboxes match your search." : "Create your first email mailbox to start receiving mail."}
                </p>
                {!search && (
                  <Button onClick={() => setShowCreate(true)} className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
                    <Plus size={14} /> Create Mailbox
                  </Button>
                )}
              </div>
            )}
          </Card>
        </>
      )}

      {/* Forwarders Tab */}
      {activeTab === "forwarders" && (
        <>
          <Card>
            <div className="p-4">
              <div className="relative">
                <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
                <input type="text" placeholder="Search forwarders..." value={search} onChange={(e) => setSearch(e.target.value)}
                  className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm" />
              </div>
            </div>
          </Card>
          <Card>
            {loading ? (
              <div className="p-8"><div className="space-y-3">{[1, 2, 3].map((i) => (<div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />))}</div></div>
            ) : filteredForwarders.length > 0 ? (
              <Table columns={forwarderColumns} data={pagedForwarders}
                page={pgF.page} limit={pgF.limit} total={pgF.total}
                onPageChange={pgF.setPage} onLimitChange={pgF.setLimit} />
            ) : (
              <div className="text-center py-16 px-4">
                <Send size={48} className="text-panel-muted/20 mx-auto mb-4" />
                <h3 className="text-lg font-medium text-panel-text mb-1">No forwarders found</h3>
                <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
                  Create email forwarders to redirect mail from one address to another.
                </p>
                <Button onClick={() => setShowCreateForwarder(true)} className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors">
                  <Plus size={14} /> Add Forwarder
                </Button>
              </div>
            )}
          </Card>
        </>
      )}

      {/* Spam & DKIM Tab */}
      {activeTab === "spam" && (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Spam Settings */}
          <Card>
            <div className="p-6">
              <div className="flex items-center gap-2 mb-4">
                <Shield size={18} className="text-orange-400" />
                <h3 className="text-base font-semibold text-panel-text">Spam Filter Settings</h3>
              </div>
              <form onSubmit={handleSaveSpam} className="space-y-4">
                <div>
                  <label className={labelClass}>Domain *</label>
                  <SearchableSelect
                    required
                    value={spamDomain}
                    onChange={setSpamDomain}
                    options={uniqueDomains.map((d) => ({ value: d, label: d }))}
                    placeholder="Select domain…"
                    emptyMessage="No domains match the filter"
                  />
                </div>
                <div>
                  <label className={labelClass}>Spam Threshold</label>
                  <input type="number" step="0.5" min={1} max={10} value={spamForm.spam_threshold}
                    onChange={(e) => setSpamForm({ ...spamForm, spam_threshold: parseFloat(e.target.value) || 5 })} className={inputClass} />
                  <p className="text-xs text-panel-muted mt-1">Lower value = stricter filtering (recommended: 5.0)</p>
                </div>
                <div>
                  <label className={labelClass}>Action on Spam</label>
                  <select value={spamForm.spam_action} onChange={(e) => setSpamForm({ ...spamForm, spam_action: e.target.value })} className={inputClass}>
                    <option value="flag">Flag (mark as spam)</option>
                    <option value="quarantine">Quarantine</option>
                    <option value="reject">Reject</option>
                  </select>
                </div>
                <div>
                  <label className={labelClass}>Whitelist (comma-separated emails)</label>
                  <input type="text" placeholder="trusted@example.com, safe@domain.com" value={spamForm.whitelist}
                    onChange={(e) => setSpamForm({ ...spamForm, whitelist: e.target.value })} className={inputClass} />
                </div>
                <div>
                  <label className={labelClass}>Blacklist (comma-separated emails)</label>
                  <input type="text" placeholder="spam@bad.com" value={spamForm.blacklist}
                    onChange={(e) => setSpamForm({ ...spamForm, blacklist: e.target.value })} className={inputClass} />
                </div>
                <div className="flex items-center gap-2">
                  <input type="checkbox" id="clamav" checked={spamForm.clamav_enabled}
                    onChange={(e) => setSpamForm({ ...spamForm, clamav_enabled: e.target.checked })}
                    className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 focus:ring-blue-500/40" />
                  <label htmlFor="clamav" className="text-sm text-panel-text">Enable ClamAV antivirus scanning</label>
                </div>
                <button type="submit" disabled={savingSpam}
                  className="w-full px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
                  {savingSpam ? "Saving..." : "Save Spam Settings"}
                </button>
              </form>
            </div>
          </Card>

          {/* DKIM Setup */}
          <Card>
            <div className="p-6">
              <div className="flex items-center gap-2 mb-4">
                <Key size={18} className="text-green-400" />
                <h3 className="text-base font-semibold text-panel-text">DKIM Email Authentication</h3>
              </div>
              <p className="text-sm text-panel-muted mb-4">
                DKIM (DomainKeys Identified Mail) adds a digital signature to outgoing emails,
                helping prevent spoofing and improving deliverability.
              </p>
              <div className="space-y-4">
                <div>
                  <label className={labelClass}>Domain *</label>
                  <SearchableSelect
                    value={dkimDomain}
                    onChange={setDkimDomain}
                    options={uniqueDomains.map((d) => ({ value: d, label: d }))}
                    placeholder="Select domain…"
                    emptyMessage="No domains match the filter"
                  />
                </div>
                <button onClick={handleSetupDkim} disabled={settingUpDkim || !dkimDomain}
                  className="w-full px-4 py-2 text-sm bg-green-600 hover:bg-green-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50 flex items-center justify-center gap-2">
                  <Key size={14} />
                  {settingUpDkim ? "Setting up DKIM..." : "Generate & Setup DKIM"}
                </button>
              </div>

              {/* Webmail / Mail Client Config */}
              <div className="mt-8 pt-6 border-t border-panel-border">
                <div className="flex items-center gap-2 mb-3">
                  <ExternalLink size={18} className="text-blue-400" />
                  <h3 className="text-base font-semibold text-panel-text">Mail Client Configuration</h3>
                </div>
                <p className="text-sm text-panel-muted mb-3">
                  Use these settings to connect any email client. Replace <span className="font-mono text-panel-text">yourdomain.com</span> with your actual domain.
                </p>
                <div className="rounded-lg overflow-hidden border border-panel-border">
                  <div className="bg-blue-600 px-4 py-2">
                    <h4 className="text-xs font-semibold text-white">Secure SSL/TLS Settings (Recommended)</h4>
                  </div>
                  <table className="w-full text-sm">
                    <tbody>
                      <tr className="border-b border-panel-border">
                        <td className="px-3 py-2 text-panel-muted font-medium w-[130px] bg-panel-bg/50 text-xs">Incoming Server:</td>
                        <td className="px-3 py-2 text-xs">
                          <span className="text-panel-text font-mono">mail.yourdomain.com</span>
                          <span className="ml-3 text-panel-muted">IMAP: <span className="text-panel-text font-mono">993</span></span>
                          <span className="ml-2 text-panel-muted">POP3: <span className="text-panel-text font-mono">995</span></span>
                        </td>
                      </tr>
                      <tr>
                        <td className="px-3 py-2 text-panel-muted font-medium bg-panel-bg/50 text-xs">Outgoing Server:</td>
                        <td className="px-3 py-2 text-xs">
                          <span className="text-panel-text font-mono">mail.yourdomain.com</span>
                          <span className="ml-3 text-panel-muted">SMTP: <span className="text-panel-text font-mono">465</span></span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          </Card>
        </div>
      )}

      {/* Create Mailbox Modal */}
      <Modal isOpen={showCreate} onClose={() => setShowCreate(false)} title="Create Mailbox">
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className={labelClass}>Domain *</label>
            <SearchableSelect
              required
              value={form.domain}
              onChange={(v) => setForm({ ...form, domain: v })}
              options={domainsList.map((d) => ({ value: d.domain, label: d.domain }))}
              placeholder="Select domain…"
              emptyMessage="No domains match the filter"
            />
          </div>
          <div>
            <label className={labelClass}>Username *</label>
            <div className="flex items-center gap-0">
              <input type="text" required placeholder="username" value={form.username}
                onChange={(e) => setForm({ ...form, username: e.target.value.toLowerCase().replace(/[^a-z0-9._-]/g, "") })}
                className={inputClass + " rounded-r-none border-r-0"} />
              <span className="px-3 py-2 bg-panel-accent border border-panel-border text-panel-muted text-sm rounded-r-lg whitespace-nowrap">
                @{form.domain || "domain.com"}
              </span>
            </div>
            {form.username && form.domain && (
              <p className="text-xs text-panel-muted mt-1">Full address: <span className="text-blue-400">{form.username}@{form.domain}</span></p>
            )}
          </div>
          <div>
            <label className={labelClass}>Password *</label>
            <PasswordInput required minLength={8} placeholder="Minimum 8 characters" value={form.password}
              onChange={(v) => setForm({ ...form, password: v })} inputClassName={inputClass} />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Quota (MB)</label>
              <input type="number" min={0} value={form.quota_mb}
                onChange={(e) => setForm({ ...form, quota_mb: parseInt(e.target.value) || 0 })} className={inputClass} />
            </div>
            <div>
              <label className={labelClass}>Send Limit/Hour</label>
              <input type="number" min={0} value={form.send_limit_per_hour}
                onChange={(e) => setForm({ ...form, send_limit_per_hour: parseInt(e.target.value) || 0 })} className={inputClass} />
            </div>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreate(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Cancel</button>
            <button type="submit" disabled={creating}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creating ? "Creating..." : "Create Mailbox"}
            </button>
          </div>
        </form>
      </Modal>

      {/* View Details Modal */}
      <Modal isOpen={showDetails} onClose={() => setShowDetails(false)} title="Mailbox Details" size="lg">
        {selectedMailbox && (
          <div className="space-y-6">
            {/* Email & Domain Header */}
            <div className="flex items-center gap-3 p-4 bg-panel-bg rounded-lg border border-panel-border">
              <div className="p-3 bg-blue-600/20 rounded-lg"><Mail size={24} className="text-blue-400" /></div>
              <div>
                <h3 className="text-lg font-semibold text-panel-text">{selectedMailbox.email}</h3>
                <p className="text-sm text-panel-muted">{selectedMailbox.domain}</p>
              </div>
            </div>

            {/* Quota & Limits */}
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

            {/* Secure SSL/TLS Settings Table (cPanel style) */}
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

            {/* Non-SSL Settings (collapsible) */}
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

            {/* Dates */}
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

            {/* Action Buttons */}
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

      {/* Edit Mailbox Modal */}
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
            <button type="submit" disabled={saving}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {saving ? "Saving..." : "Save Changes"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Create Forwarder Modal */}
      <Modal isOpen={showCreateForwarder} onClose={() => setShowCreateForwarder(false)} title="Create Email Forwarder">
        <form onSubmit={handleCreateForwarder} className="space-y-4">
          <div>
            <label className={labelClass}>Source Email *</label>
            <input type="email" required placeholder="source@example.com" value={forwarderForm.source}
              onChange={(e) => setForwarderForm({ ...forwarderForm, source: e.target.value })} className={inputClass} />
          </div>
          <div>
            <label className={labelClass}>Forward To (comma-separated) *</label>
            <input type="text" required placeholder="dest1@example.com, dest2@example.com" value={forwarderForm.destinations}
              onChange={(e) => setForwarderForm({ ...forwarderForm, destinations: e.target.value })} className={inputClass} />
          </div>
          <div className="flex items-center gap-2">
            <input type="checkbox" id="keepCopy" checked={forwarderForm.keep_copy}
              onChange={(e) => setForwarderForm({ ...forwarderForm, keep_copy: e.target.checked })}
              className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 focus:ring-blue-500/40" />
            <label htmlFor="keepCopy" className="text-sm text-panel-text">Keep a copy in the original mailbox</label>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button type="button" onClick={() => setShowCreateForwarder(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors">Cancel</button>
            <button type="submit" disabled={creatingForwarder}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50">
              {creatingForwarder ? "Creating..." : "Create Forwarder"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Mail Client Setup Modal */}
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

            {/* Username + TLS warnings — these two trip up most operators
                when configuring Gmail "Send mail as", Outlook 365, or
                strict IMAP clients. Calling them out explicitly cuts
                support load. */}
            <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-xs text-panel-text space-y-1.5">
              <div className="font-semibold text-amber-300 flex items-center gap-1.5"><Shield size={12} /> Two things to know before you connect</div>
              <p>1. <strong>Username MUST be the FULL email</strong> ({connectMailbox.email}). Mail clients that auto-fill just the local part ("{connectMailbox.email.split("@")[0]}") will fail with "authentication error".</p>
              <p>2. <strong>Strict clients (Gmail / Outlook 365)</strong> validate the TLS cert hostname. If they reject auth even with the right password, the server's mail cert may not cover <code className="text-amber-200">mail.{connectMailbox.domain}</code>. Run <code className="text-amber-200">bzpanel mail-ssl {connectMailbox.domain}</code> on the server to issue a Let's Encrypt cert + wire SNI.</p>
            </div>

            {/* SSL/TLS Settings */}
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

            {/* Non-SSL (collapsible) */}
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

            {/* Setup Guide */}
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
                radio buttons in a way that trips most operators. The two
                terms below mean opposite things: "SSL" = implicit TLS
                from byte 0 (port 465); "TLS" = STARTTLS upgrade after a
                plaintext greeting (port 587). Picking SSL+587 or
                TLS+465 produces a "Couldn't connect to server" error
                because the wire protocol mismatches the port. */}
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
                "SSL" + 587 or "TLS" + 465 are the two wrong combinations — Gmail returns "Couldn't connect to the server" because the wire protocol doesn't match the port. Username always = the FULL email; never just the local part.
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
              Required column: <code className="font-mono">email</code>. Optional: <code className="font-mono">domain</code> (auto-derived from email), <code className="font-mono">password</code> (BLANK = auto-generate, returned in result), <code className="font-mono">quota_mb</code>, <code className="font-mono">send_limit_per_hour</code>. CSV or XLSX accepted.
            </div>
            <div className="flex gap-2">
              <Button onClick={() => downloadTemplate("csv")} className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm">
                <Download size={12} /> Download CSV template
              </Button>
              <Button onClick={() => downloadTemplate("xlsx")} className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm">
                <Download size={12} /> Download XLSX template
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
              <Button onClick={handleBulkUpload} disabled={!bulkUploadFile || bulkUploading} className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white rounded-lg text-sm">
                <Upload size={14} /> {bulkUploading ? "Uploading…" : "Upload"}
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
              <Button onClick={() => { setShowBulkUpload(false); setBulkUploadResult(null); setBulkUploadFile(null); }} className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm">Done</Button>
            </div>
          </div>
        )}
      </Modal>

      {/* ─── Bulk Delete (OTP-gated) Modal ────────────────────────── */}
      <Modal isOpen={showBulkDelete} onClose={() => setShowBulkDelete(false)} title="Bulk Delete Mailboxes" size="lg">
        {bulkDeleteStep === "request" && (
          <div className="space-y-3">
            <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-300">
              <AlertTriangle size={14} className="inline mr-1" /> About to delete <strong>{selectedIDs.size}</strong> mailbox(es). A 6-digit confirmation code will be emailed before any deletion runs. The code expires in 10 minutes.
            </div>
            <p className="text-xs text-panel-muted">Click "Send code" to receive the OTP, then enter it on the next step. Up to 5 wrong codes per request.</p>
            <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
              <button onClick={() => setShowBulkDelete(false)} className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted">Cancel</button>
              <Button onClick={handleBulkDeleteRequestOTP} disabled={bulkDeleteBusy || selectedIDs.size === 0} className="px-4 py-2 bg-red-600 hover:bg-red-700 text-white rounded-lg text-sm disabled:opacity-50">
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
              <Button onClick={handleBulkDeleteConfirm} disabled={bulkDeleteBusy || bulkDeleteOTP.code.length !== 6} className="px-4 py-2 bg-red-600 hover:bg-red-700 text-white rounded-lg text-sm disabled:opacity-50">
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
              <Button onClick={() => setShowBulkDelete(false)} className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm">Done</Button>
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
              <Button onClick={handleBulkExportRequestOTP} disabled={bulkExportBusy} className="px-4 py-2 bg-amber-600 hover:bg-amber-700 text-white rounded-lg text-sm disabled:opacity-50">
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
              <Button onClick={handleBulkExportConfirm} disabled={bulkExportOTP.code.length !== 6} className="px-4 py-2 bg-amber-600 hover:bg-amber-700 text-white rounded-lg text-sm disabled:opacity-50">
                Download {bulkExportFormat.toUpperCase()}
              </Button>
            </div>
          </div>
        )}
      </Modal>

      {/* ─── Forwarder Bulk Upload Modal ──────────────────────────── */}
      <Modal isOpen={showFwdBulkUpload} onClose={() => setShowFwdBulkUpload(false)} title="Bulk Upload Forwarders" size="lg">
        {!fwdBulkUploadResult ? (
          <div className="space-y-4">
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 px-3 py-2 text-xs text-blue-200/80">
              Required columns: <code className="font-mono">source</code>, <code className="font-mono">destinations</code>. Optional: <code className="font-mono">keep_copy</code> (true/false), <code className="font-mono">user</code> (WHM only — pick the owner). <code className="font-mono">destinations</code> accepts a comma- OR semicolon-separated list. Idempotent — re-uploading the same source overwrites its destinations.
            </div>
            <div className="flex gap-2">
              <Button onClick={() => downloadForwarderTemplate("csv")} className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm">
                <Download size={12} /> Download CSV template
              </Button>
              <Button onClick={() => downloadForwarderTemplate("xlsx")} className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm">
                <Download size={12} /> Download XLSX template
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
              <Button onClick={handleFwdBulkUpload} disabled={!fwdBulkUploadFile || fwdBulkUploading} className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white rounded-lg text-sm">
                <Upload size={14} /> {fwdBulkUploading ? "Uploading…" : "Upload"}
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
              <Button onClick={() => { setShowFwdBulkUpload(false); setFwdBulkUploadResult(null); setFwdBulkUploadFile(null); }} className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm">Done</Button>
            </div>
          </div>
        )}
      </Modal>

      {/* ─── Forwarder Bulk Delete (OTP-gated) Modal ──────────────── */}
      <Modal isOpen={showFwdBulkDelete} onClose={() => setShowFwdBulkDelete(false)} title="Bulk Delete Forwarders" size="lg">
        {fwdBulkDeleteStep === "request" && (
          <div className="space-y-3">
            <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-300">
              <AlertTriangle size={14} className="inline mr-1" /> About to delete <strong>{selectedForwarderIDs.size}</strong> forwarder(s). A 6-digit confirmation code will be emailed before any deletion runs. The code expires in 10 minutes.
            </div>
            <p className="text-xs text-panel-muted">Click "Send code" to receive the OTP, then enter it on the next step. Up to 5 wrong codes per request.</p>
            <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
              <button onClick={() => setShowFwdBulkDelete(false)} className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted">Cancel</button>
              <Button onClick={handleFwdBulkDeleteRequestOTP} disabled={fwdBulkDeleteBusy || selectedForwarderIDs.size === 0} className="px-4 py-2 bg-red-600 hover:bg-red-700 text-white rounded-lg text-sm disabled:opacity-50">
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
              <Button onClick={handleFwdBulkDeleteConfirm} disabled={fwdBulkDeleteBusy || fwdBulkDeleteOTP.code.length !== 6} className="px-4 py-2 bg-red-600 hover:bg-red-700 text-white rounded-lg text-sm disabled:opacity-50">
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
              <Button onClick={() => setShowFwdBulkDelete(false)} className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm">Done</Button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
