import { useState, useEffect, useMemo } from "react";
import { Card, Button, Table, StatusBadge, Modal, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  ShieldCheck,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  Download,
  Eye,
  Lock,
  LockOpen,
  Upload,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  RotateCw,
} from "lucide-react";

interface SslCertificate {
  id: string;
  domain: string;
  type: "letsencrypt" | "custom";
  expires_at: string | null;
  days_remaining: number;
  force_ssl: boolean;
  issued_at: string | null;
  issuer: string;
  auto_renew: boolean;
  domains: string[];
}

interface DomainOption {
  id: string;
  domain: string;
  ssl_active: boolean;
  ssl_expires?: string | null;
  user?: string;
  owner_email?: string;
}

interface BulkItem {
  domain: string;
  success: boolean;
  error?: string;
  cert_id?: string;
  expires_at?: string | null;
  // action is "issued" for fresh issuance or "reissued" when an
  // existing cert was force-replaced. Lets the result panel show
  // a clear breakdown instead of one ambiguous "success" pill.
  action?: "issued" | "reissued" | string;
}

interface BulkResponse {
  total: number;
  success: number;
  failed: number;
  issued?: number;
  reissued?: number;
  items: BulkItem[];
}

// SSLBulkJob is the live progress doc mirrored from the backend
// model with the same name. v3.1.60 swapped the synchronous bulk
// POST for a job_id + poll model so the operator sees per-domain
// progress in real time instead of a "Reissuing 27..." spinner.
//
// status flows: queued → running → completed | cancelled | failed.
// current_domain points at whichever certbot run is in flight right
// now; cleared between rows + on terminal state. progress is 0..100
// rounded down (server-computed so the UI doesn't have to math it).
interface SSLBulkJob {
  id: string;
  status: "queued" | "running" | "completed" | "cancelled" | "failed";
  total: number;
  success: number;
  failed: number;
  issued: number;
  reissued: number;
  progress: number;
  current_domain?: string;
  items: BulkItem[];
  wildcard: boolean;
  reissue: boolean;
  cancel_requested: boolean;
  started_at: string;
  finished_at?: string | null;
}

// SslRow is one rendered row in the SSL table. ALWAYS keyed on the
// domain — `cert` is null when no SSL has been issued for that domain
// yet, so the row can render a "No SSL" badge + an Issue CTA instead
// of leaving the operator to guess what's missing.
interface SslRow {
  domain: string;
  cert: SslCertificate | null;
  owner_email?: string;
}

type CertStatus = "active" | "expiring" | "expired";
type CertFilter = "all" | "active" | "inactive";
type DomainFilter = "all" | "active" | "inactive";

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

function getCertStatus(cert: SslCertificate): CertStatus {
  if (!cert.expires_at) return "active";
  if (cert.days_remaining <= 0) return "expired";
  if (cert.days_remaining <= 30) return "expiring";
  return "active";
}

// Domain "active" SSL = a panel-tracked cert exists and isn't expired.
// Mirrors the badge colour rules so the filter and the visual stay in
// lockstep — clicking "Active" only ever shows green-badged rows.
function domainSslActive(d: DomainOption): boolean {
  if (!d.ssl_active) return false;
  if (!d.ssl_expires) return true;
  const exp = new Date(d.ssl_expires).getTime();
  if (Number.isNaN(exp)) return true;
  return exp > Date.now();
}

function formatDate(dateStr: string | null | undefined): string {
  if (!dateStr) return "N/A";
  try {
    return new Date(dateStr).toLocaleDateString("en-US", {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
  } catch {
    return dateStr;
  }
}

export default function SslPage() {
  const [certificates, setCertificates] = useState<SslCertificate[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<CertFilter>("all");

  const [showIssue, setShowIssue] = useState(false);
  const [showUpload, setShowUpload] = useState(false);
  const [showResults, setShowResults] = useState(false);
  // Bulk Force-HTTPS state. The result modal renders a per-row outcome
  // table; the running flag spins the header button while the request
  // is in flight (a 90-domain batch can take a few seconds because
  // each row touches nginx + reload).
  const [forceSSLBulkRunning, setForceSSLBulkRunning] = useState(false);
  const [forceSSLBulkResult, setForceSSLBulkResult] = useState<any>(null);
  const [bulkResult, setBulkResult] = useState<BulkResponse | null>(null);
  // bulkJob is the LIVE job doc the backend serialises every ~1.5s
  // while a bulk issuance runs. Drives the live-progress modal:
  // progress bar, current-domain indicator, per-row table. When
  // status hits a terminal value (completed / cancelled / failed)
  // polling stops; the modal stays open so the operator can read
  // the final outcomes before closing.
  const [bulkJob, setBulkJob] = useState<SSLBulkJob | null>(null);
  const [cancelling, setCancelling] = useState(false);
  const [issuing, setIssuing] = useState(false);
  const [uploading, setUploading] = useState(false);

  const [domains, setDomains] = useState<DomainOption[]>([]);
  const [domainsLoading, setDomainsLoading] = useState(false);

  // Bulk-issue form state. There used to be an `issueEmail` field
  // here that the UI tried to keep in sync with the first selected
  // domain's owner_email — but the user explicitly asked us to drop
  // it: every cert should be registered under the OWNING vendor's
  // email per-domain, automatically, with no shared field for the
  // operator to fiddle with. The backend now resolves the email
  // per-domain inside IssueLetsEncrypt, so the bulk request just
  // sends {domains, wildcard} and the cert for konsultkaro.in lands
  // under konsultkaro@gmail.com while the cert for betazeninfotech.com
  // lands under its own vendor in the same batch.
  const [pickerSearch, setPickerSearch] = useState("");
  const [pickerFilter, setPickerFilter] = useState<DomainFilter>("all");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [wildcard, setWildcard] = useState(false);
  // Reissue forces a fresh certbot run even when a valid cert is
  // already on disk. Defaults to true for the bulk modal because the
  // operator typically opens this from the "Active SSL" tab to
  // explicitly replace existing certs — toggling off restores the
  // historic "skip already-issued domains" behaviour for legacy
  // workflows.
  const [reissue, setReissue] = useState(true);

  // Single-domain custom upload
  const [uploadForm, setUploadForm] = useState({
    domain: "",
    certificate: "",
    private_key: "",
    ca_bundle: "",
  });

  useEffect(() => {
    fetchCertificates();
    fetchDomains();
  }, []);

  // Email auto-fill effect was removed when the email field itself
  // was dropped from the modal — see the comment on the bulk-issue
  // state block above.

  const fetchDomains = async () => {
    setDomainsLoading(true);
    try {
      // limit=500 matches the cPanel pattern — single page gives the
      // operator the full inventory in the picker without paging UI.
      const res = await api.get("/domains", { params: { limit: 500 } });
      setDomains(res.data.data || []);
    } catch {
      setDomains([]);
    } finally {
      setDomainsLoading(false);
    }
  };

  const fetchCertificates = async () => {
    setLoading(true);
    try {
      const res = await api.get("/ssl/");
      setCertificates(res.data.data || []);
    } catch {
      // Keep empty state
    } finally {
      setLoading(false);
    }
  };

  const openIssue = () => {
    setSelected(new Set());
    setPickerSearch("");
    setPickerFilter("all");
    setWildcard(false);
    setShowIssue(true);
    // Re-fetch in case domains were added in another tab.
    fetchDomains();
  };

  const filteredPickerDomains = useMemo(() => {
    const q = pickerSearch.trim().toLowerCase();
    return domains.filter((d) => {
      if (q && !d.domain.toLowerCase().includes(q)) return false;
      if (pickerFilter === "active" && !domainSslActive(d)) return false;
      if (pickerFilter === "inactive" && domainSslActive(d)) return false;
      return true;
    });
  }, [domains, pickerSearch, pickerFilter]);

  const toggleOne = (domain: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(domain)) next.delete(domain);
      else next.add(domain);
      return next;
    });
  };

  const toggleAllVisible = () => {
    const visibleDomains = filteredPickerDomains.map((d) => d.domain);
    const allSelected = visibleDomains.every((d) => selected.has(d));
    setSelected((prev) => {
      const next = new Set(prev);
      if (allSelected) {
        visibleDomains.forEach((d) => next.delete(d));
      } else {
        visibleDomains.forEach((d) => next.add(d));
      }
      return next;
    });
  };

  // handleBulkIssue (v3.1.60) — starts a server-side bulk-issue
  // JOB and immediately swaps to the live-progress modal. The
  // backend returns a job_id within ~50ms (it spawns a detached
  // goroutine for the actual certbot calls), and we poll
  // /ssl/bulk-jobs/:id every 1.5s for per-row updates.
  //
  // Pre-3.1.60 this was a single await POST that blocked for the
  // full duration of the batch — a 27-cert run pinned the modal at
  // "Reissuing 27..." for 10+ minutes with no per-domain feedback.
  const handleBulkIssue = async (e: React.FormEvent) => {
    e.preventDefault();
    if (selected.size === 0) {
      toast.error("Select at least one domain");
      return;
    }
    setIssuing(true);
    const submittedDomains = Array.from(selected);
    try {
      // No `email` in the payload — the backend resolves it
      // per-domain from each domain's owning vendor (so a multi-
      // vendor batch lands each cert under the right address).
      const res = await api.post("/ssl/letsencrypt/bulk", {
        domains: submittedDomains,
        wildcard,
        reissue,
      });
      const startResp = res.data.data as { job_id: string; total: number; status: string };
      if (!startResp?.job_id) {
        toast.error("Server didn't return a job id");
        setIssuing(false);
        return;
      }
      // Seed the modal with a queued job so the operator sees the
      // full domain list immediately, even before the first poll
      // arrives. The poll loop will replace these placeholder
      // entries with the real items array (same domain ordering).
      setBulkJob({
        id: startResp.job_id,
        status: "queued",
        total: startResp.total,
        success: 0,
        failed: 0,
        issued: 0,
        reissued: 0,
        progress: 0,
        items: submittedDomains.map((d) => ({ domain: d, success: false })),
        wildcard,
        reissue,
        cancel_requested: false,
        started_at: new Date().toISOString(),
      });
      setShowIssue(false);
      setShowResults(true);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Bulk issue failed to start");
    } finally {
      setIssuing(false);
    }
  };

  // Cancel button on the live-progress modal — flips the server's
  // cancel_requested flag. The detached goroutine sees it between
  // rows and exits cleanly. In-flight certbot calls are NOT killed
  // (forcing a half-written cert would be worse than letting the
  // current row finish), so the operator may see one more row land
  // after pressing Cancel.
  const handleBulkJobCancel = async () => {
    if (!bulkJob || bulkJob.status !== "running") return;
    setCancelling(true);
    try {
      await api.post(`/ssl/bulk-jobs/${bulkJob.id}/cancel`);
      toast("Cancellation requested — finishing the current row…", { icon: "⏸" });
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Cancel failed");
    } finally {
      setCancelling(false);
    }
  };

  // Live-progress polling effect. Fires whenever a bulk job is
  // active (modal open + job not in a terminal state). Uses
  // setInterval at 1500ms — empirically fast enough that the
  // operator sees rows tick in under a second of real time, slow
  // enough to keep request volume low on a long-running 50-domain
  // batch (~40 polls / minute).
  //
  // On terminal status: stop polling, fire a summary toast,
  // refresh the certs + domains tables so the SSL listing shows
  // every newly-issued cert. The modal stays open until the
  // operator hits Close so they can review per-row outcomes.
  useEffect(() => {
    if (!bulkJob || !showResults) return;
    const terminal = ["completed", "cancelled", "failed"];
    if (terminal.includes(bulkJob.status)) return;
    let cancelled = false;
    const tick = async () => {
      try {
        const res = await api.get(`/ssl/bulk-jobs/${bulkJob.id}`);
        const next = res.data?.data as SSLBulkJob | undefined;
        if (cancelled || !next) return;
        setBulkJob(next);
        if (terminal.includes(next.status)) {
          // Summary toast — same wording as pre-3.1.60 so the
          // operator's habit reading "Issued 4, reissued 32, failed 1"
          // carries over.
          const parts: string[] = [];
          if ((next.issued ?? 0) > 0) parts.push(`issued ${next.issued}`);
          if ((next.reissued ?? 0) > 0) parts.push(`reissued ${next.reissued}`);
          const summary = parts.length > 0
            ? parts.join(", ")
            : `${next.success} certificate${next.success === 1 ? "" : "s"}`;
          if (next.status === "cancelled") {
            toast(`Cancelled — ${summary}, ${next.failed} failed, ${next.total - next.success - next.failed} skipped`, { icon: "⏸" });
          } else if (next.failed === 0 && next.success > 0) {
            toast.success(summary.charAt(0).toUpperCase() + summary.slice(1));
          } else if (next.success === 0 && next.failed > 0) {
            toast.error(`Failed: ${next.failed} certificate${next.failed === 1 ? "" : "s"}`);
          } else if (next.success > 0 || next.failed > 0) {
            toast(`${summary}, failed ${next.failed}`, { icon: "⚠️" });
          }
          fetchCertificates();
          fetchDomains();
        }
      } catch {
        // Transient poll failure — try again on the next tick.
        // The modal keeps showing the last good snapshot.
      }
    };
    const id = setInterval(tick, 1500);
    // Fire an immediate tick so the operator doesn't wait 1.5s for
    // the first server-side state update after pressing Issue.
    tick();
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [bulkJob?.id, bulkJob?.status, showResults]);

  const handleUploadCustom = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!uploadForm.domain) {
      toast.error("Select a domain");
      return;
    }
    if (!uploadForm.certificate.trim() || !uploadForm.private_key.trim()) {
      toast.error("Certificate and private key are required");
      return;
    }
    setUploading(true);
    try {
      await api.post("/ssl/custom", uploadForm);
      toast.success(`Custom certificate installed on ${uploadForm.domain}`);
      setShowUpload(false);
      setUploadForm({ domain: "", certificate: "", private_key: "", ca_bundle: "" });
      fetchCertificates();
      fetchDomains();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to upload certificate");
    } finally {
      setUploading(false);
    }
  };

  const handleDelete = async (domain: string) => {
    if (
      !(await confirmAction({
        title: "Delete SSL?",
        description: `Remove the SSL certificate for ${domain}? On-disk material is preserved.`,
        danger: true,
        confirmLabel: "Delete",
      }))
    )
      return;
    try {
      await api.delete(`/ssl/${domain}`);
      toast.success(`SSL certificate for ${domain} deleted`);
      fetchCertificates();
      fetchDomains();
    } catch {
      toast.error("Failed to delete certificate");
    }
  };

  const handleForceSSL = async (domain: string, enable: boolean) => {
    try {
      await api.post(`/ssl/${domain}/force-ssl`, { enable });
      toast.success(enable ? "Force SSL enabled" : "Force SSL disabled");
      setCertificates((prev) =>
        prev.map((c) => (c.domain === domain ? { ...c, force_ssl: enable } : c))
      );
    } catch {
      toast.error("Failed to update Force SSL");
    }
  };

  // handleBulkForceSSL — flips the force_ssl flag on every visible
  // domain in one shot. Used by the "Force HTTPS for All" header
  // button. Domains without a live cert are SKIPPED server-side (so
  // we don't 502 an HTTP-only site) and surface in the result modal
  // with a "no SSL cert" reason instead of being counted as a
  // failure. Confirms first because this hits nginx + reload across
  // potentially every domain on the box.
  const handleBulkForceSSL = async (enable: boolean) => {
    const targetCount = certificates.length;
    if (targetCount === 0) {
      toast.error("No certificates to update");
      return;
    }
    const ok = await confirmAction({
      title: `${enable ? "Force HTTPS" : "Disable Force HTTPS"} for ${targetCount} domain${targetCount === 1 ? "" : "s"}?`,
      description: enable
        ? `Enables HTTPS-only redirect (301 from http:// to https://) on every visible domain that has a live SSL cert. ` +
          `Domains without a cert are SKIPPED so we don't 502 an HTTP-only site.`
        : `Disables the HTTPS-only redirect on every visible domain — visitors arriving at http:// will stay on http://. Use only if you're temporarily debugging a redirect loop or rolling back.`,
      confirmLabel: enable ? "Force HTTPS for all" : "Disable Force HTTPS for all",
      danger: !enable,
    });
    if (!ok) return;
    setForceSSLBulkRunning(true);
    setForceSSLBulkResult(null);
    try {
      const res = await api.post("/ssl/force-ssl-bulk", { all: true, enable });
      const data = res.data?.data;
      setForceSSLBulkResult(data);
      await fetchCertificates();
      toast.success(
        `${enable ? "Force HTTPS enabled" : "Force HTTPS disabled"} on ${data?.successes ?? 0} domain${(data?.successes ?? 0) === 1 ? "" : "s"}` +
          (data?.skipped ? `, ${data.skipped} skipped` : "") +
          (data?.failures ? `, ${data.failures} failed` : "")
      );
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Bulk Force SSL failed");
    } finally {
      setForceSSLBulkRunning(false);
    }
  };

  const handleRenew = async (domain: string) => {
    try {
      await api.post(`/ssl/${domain}/renew`);
      toast.success("Certificate renewal initiated");
      fetchCertificates();
    } catch {
      toast.error("Failed to renew certificate");
    }
  };

  // handleReissue is the per-row force-fresh-cert action. Distinct from
  // Renew (which uses `certbot renew --force-renewal` and only works
  // when the live cert is on disk): Reissue uses `certbot certonly
  // --force-renewal` which works in both cases and re-runs the full
  // post-issue pipeline (vhost upgrade, mail-SSL retrigger, ssl.issued
  // webhook). Use this when:
  //   - the private key was exposed and you want a clean rotation
  //   - the live cert lineage is corrupt / missing a SAN the panel
  //     thinks it should have
  //   - a transfer brought in a stale cert and you want a fresh
  //     issuance under THIS server's account
  const handleReissue = async (domain: string) => {
    if (
      !(await confirmAction({
        title: "Reissue SSL?",
        description: `Force a fresh Let's Encrypt certificate for ${domain}? The current cert will be replaced. Reissues count against Let's Encrypt's per-week duplicate-cert limit (5/week).`,
        confirmLabel: "Reissue",
      }))
    )
      return;
    try {
      await api.post(`/ssl/${domain}/reissue`);
      toast.success(`Certificate reissued for ${domain}`);
      fetchCertificates();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to reissue certificate");
    }
  };

  // mergedRows is the canonical data model for the SSL list: ONE row
  // per domain, with the matching cert attached when one exists. A
  // domain that has no cert in the panel still gets a row so the
  // operator can see it and trigger Issue from the same surface — the
  // previous version showed only domains-with-certs, hiding the very
  // domains that needed action.
  const mergedRows = useMemo<SslRow[]>(() => {
    const certByDomain = new Map<string, SslCertificate>();
    for (const c of certificates) certByDomain.set(c.domain, c);

    const rows: SslRow[] = [];
    const seen = new Set<string>();

    // Start from the domain inventory so every domain appears in the
    // list — even ones the panel hasn't issued a cert for yet.
    for (const d of domains) {
      const cert = certByDomain.get(d.domain) || null;
      rows.push({ domain: d.domain, cert, owner_email: d.owner_email });
      seen.add(d.domain);
    }
    // Catch certs whose Domain doc was deleted but the cert record
    // still exists (rare, but a real edge case after a domain delete
    // race). Keeps the operator from being confused by an SSL record
    // that has nowhere to act on.
    for (const c of certificates) {
      if (seen.has(c.domain)) continue;
      rows.push({ domain: c.domain, cert: c, owner_email: undefined });
    }
    rows.sort((a, b) => a.domain.localeCompare(b.domain));
    return rows;
  }, [certificates, domains]);

  // Active = a cert exists AND it's not yet expired.
  // Inactive = no cert at all OR cert is expired. Matches the user's
  // mental model: "do I need to act on this row?"
  const isActiveRow = (r: SslRow): boolean => {
    if (!r.cert) return false;
    return getCertStatus(r.cert) !== "expired";
  };

  // 3.1.92 — Column sort state. `key` is the field, `dir` is the
  // direction. Default sort: domain ASC (matches the existing
  // mergedRows alphabetical default). Clicking a header toggles ASC →
  // DESC → back to default. Persisted to localStorage so the
  // operator's preferred sort survives a refresh.
  type SortKey = "domain" | "type" | "expires" | "status" | "force_ssl";
  type SortDir = "asc" | "desc";
  const [sortKey, setSortKey] = useState<SortKey>(() => {
    const v = localStorage.getItem("whm-ssl-sort-key");
    return (v === "type" || v === "expires" || v === "status" || v === "force_ssl") ? v : "domain";
  });
  const [sortDir, setSortDir] = useState<SortDir>(() => {
    return localStorage.getItem("whm-ssl-sort-dir") === "desc" ? "desc" : "asc";
  });
  useEffect(() => { localStorage.setItem("whm-ssl-sort-key", sortKey); }, [sortKey]);
  useEffect(() => { localStorage.setItem("whm-ssl-sort-dir", sortDir); }, [sortDir]);

  // Toggle handler: clicking the same header flips ASC ↔ DESC; clicking
  // a different header resets to ASC (matches the convention every
  // table on the web uses, so the operator doesn't have to think).
  const toggleSort = (key: SortKey) => {
    if (key === sortKey) setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    else { setSortKey(key); setSortDir("asc"); }
  };

  // Comparator. Each branch produces a stable ordering even when the
  // cert is null (sorted to the end so "no cert yet" rows don't bury
  // the row with the soonest expiry).
  const sortedRows = useMemo(() => {
    const rows = [...mergedRows];
    rows.sort((a, b) => {
      let cmp = 0;
      switch (sortKey) {
        case "domain":
          cmp = a.domain.localeCompare(b.domain);
          break;
        case "type": {
          // No cert sorts last; letsencrypt before custom alphabetically.
          const av = a.cert?.type || "~";
          const bv = b.cert?.type || "~";
          cmp = av.localeCompare(bv);
          break;
        }
        case "expires": {
          // Soonest-expiring first on ASC. No-cert rows always last.
          const at = a.cert?.expires_at ? new Date(a.cert.expires_at).getTime() : Number.MAX_SAFE_INTEGER;
          const bt = b.cert?.expires_at ? new Date(b.cert.expires_at).getTime() : Number.MAX_SAFE_INTEGER;
          cmp = at - bt;
          break;
        }
        case "status": {
          // active < warning < expired < inactive — so ASC = "healthy
          // first, problems at the bottom". Flip with DESC to see what
          // needs urgent attention up top.
          const rank = (r: SslRow): number => {
            if (!r.cert) return 3;
            const s = getCertStatus(r.cert);
            return s === "active" ? 0 : s === "expiring" ? 1 : s === "expired" ? 2 : 3;
          };
          cmp = rank(a) - rank(b);
          break;
        }
        case "force_ssl": {
          // Enabled first on ASC; no-cert rows last.
          const av = a.cert ? (a.cert.force_ssl ? 0 : 1) : 2;
          const bv = b.cert ? (b.cert.force_ssl ? 0 : 1) : 2;
          cmp = av - bv;
          break;
        }
      }
      if (cmp === 0) cmp = a.domain.localeCompare(b.domain); // tiebreaker
      return sortDir === "asc" ? cmp : -cmp;
    });
    return rows;
  }, [mergedRows, sortKey, sortDir]);

  const filteredRows = useMemo(() => {
    const q = search.trim().toLowerCase();
    return sortedRows.filter((r) => {
      if (q && !r.domain.toLowerCase().includes(q)) return false;
      if (statusFilter === "active" && !isActiveRow(r)) return false;
      if (statusFilter === "inactive" && isActiveRow(r)) return false;
      return true;
    });
  }, [sortedRows, search, statusFilter]);

  // Small helper to render a sortable header label + indicator arrow.
  // Renders as a button so the whole header row stays keyboard-
  // accessible (Enter/Space toggle the sort).
  const sortHeader = (label: string, key: SortKey) => {
    const active = sortKey === key;
    const arrow = !active ? "↕" : sortDir === "asc" ? "↑" : "↓";
    return (
      <button
        type="button"
        onClick={() => toggleSort(key)}
        className={"inline-flex items-center gap-1 transition-colors " + (active ? "text-panel-text" : "text-panel-muted hover:text-panel-text")}
        title={active
          ? `Sorted by ${label.toLowerCase()} ${sortDir === "asc" ? "ascending" : "descending"} — click to flip`
          : `Sort by ${label.toLowerCase()}`}
      >
        {label}
        <span className={"text-[10px] " + (active ? "opacity-100" : "opacity-50")}>{arrow}</span>
      </button>
    );
  };

  const counts = useMemo(() => {
    let active = 0;
    for (const r of mergedRows) if (isActiveRow(r)) active++;
    return { all: mergedRows.length, active, inactive: mergedRows.length - active };
  }, [mergedRows]);

  // Open the bulk-issue modal pre-selected with a single domain. Same
  // flow as bulk Issue but lets the operator act on a specific row
  // without manually scrolling the picker. Per-domain ACME email is
  // auto-resolved server-side from the domain's owning vendor.
  const issueForOne = (domain: string) => {
    setSelected(new Set([domain]));
    setPickerSearch("");
    setPickerFilter("all");
    setWildcard(false);
    setShowIssue(true);
    fetchDomains();
  };

  const columns = [
    {
      header: sortHeader("Domain", "domain"),
      accessor: (r: SslRow) => (
        <div className="flex items-center gap-2">
          <ShieldCheck
            size={14}
            className={
              !r.cert
                ? "text-panel-muted/50"
                : getCertStatus(r.cert) === "expired"
                  ? "text-red-400"
                  : "text-green-400"
            }
          />
          <span className="font-medium text-panel-text">{r.domain}</span>
        </div>
      ),
    },
    {
      header: sortHeader("Type", "type"),
      accessor: (r: SslRow) =>
        r.cert ? (
          <span
            className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
              r.cert.type === "letsencrypt"
                ? "bg-green-500/10 text-green-400"
                : "bg-blue-500/10 text-blue-400"
            }`}
          >
            {r.cert.type === "letsencrypt" ? "Let's Encrypt" : "Custom"}
          </span>
        ) : (
          <span className="text-xs text-panel-muted">—</span>
        ),
    },
    {
      header: sortHeader("Expires", "expires"),
      accessor: (r: SslRow) =>
        r.cert ? (
          <div>
            <span className="text-panel-muted text-sm">{formatDate(r.cert.expires_at)}</span>
            {r.cert.days_remaining > 0 && r.cert.days_remaining <= 30 && (
              <span className="ml-2 text-xs text-yellow-400">
                ({r.cert.days_remaining}d left)
              </span>
            )}
            {r.cert.days_remaining <= 0 && r.cert.expires_at && (
              <span className="ml-2 text-xs text-red-400">Expired</span>
            )}
          </div>
        ) : (
          <span className="text-xs text-panel-muted">—</span>
        ),
    },
    {
      header: sortHeader("Status", "status"),
      accessor: (r: SslRow) =>
        !r.cert ? (
          <StatusBadge status="inactive" />
        ) : (
          <StatusBadge
            status={
              getCertStatus(r.cert) === "expired"
                ? "expired"
                : getCertStatus(r.cert) === "expiring"
                  ? "warning"
                  : "active"
            }
          />
        ),
    },
    {
      header: sortHeader("Force SSL", "force_ssl"),
      accessor: (r: SslRow) =>
        r.cert ? (
          <button
            onClick={() => handleForceSSL(r.cert!.domain, !r.cert!.force_ssl)}
            className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium transition-colors ${
              r.cert.force_ssl
                ? "bg-green-500/10 text-green-400 hover:bg-green-500/20"
                : "bg-panel-border/30 text-panel-muted hover:bg-panel-border/50"
            }`}
            title={r.cert.force_ssl ? "Click to disable Force SSL" : "Click to enable Force SSL"}
          >
            {r.cert.force_ssl ? <Lock size={12} /> : <LockOpen size={12} />}
            {r.cert.force_ssl ? "Enabled" : "Disabled"}
          </button>
        ) : (
          <span className="text-xs text-panel-muted">—</span>
        ),
    },
    {
      header: "Actions",
      accessor: (r: SslRow) =>
        !r.cert ? (
          // No cert yet — single CTA that opens the bulk modal
          // pre-selected with this row, so the operator goes straight
          // from "I see this domain has no SSL" to "issuing one click".
          <button
            onClick={() => issueForOne(r.domain)}
            className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-medium bg-blue-600/10 text-blue-400 hover:bg-blue-600/20 transition-colors"
            title="Issue Let's Encrypt certificate for this domain"
          >
            <Plus size={12} />
            Issue SSL
          </button>
        ) : (
          <div className="flex items-center gap-1">
            <button className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors">
              <Eye size={14} />
            </button>
            <button
              onClick={() => handleRenew(r.cert!.domain)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-green-400 transition-colors"
              title="Renew (extend expiry)"
            >
              <Download size={14} />
            </button>
            <button
              onClick={() => handleReissue(r.cert!.domain)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-amber-400 transition-colors"
              title="Reissue (force a fresh certificate)"
            >
              <RotateCw size={14} />
            </button>
            <button
              onClick={() => handleDelete(r.cert!.domain)}
              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
              title="Delete"
            >
              <Trash2 size={14} />
            </button>
          </div>
        ),
    },
  ];

  // mixedOwners detection used to drive an "ACME email mismatch"
  // warning when one shared email had to cover the whole batch.
  // The shared field is gone now — backend resolves the email
  // per-domain — so this is no longer needed.

  const visibleAllSelected =
    filteredPickerDomains.length > 0 &&
    filteredPickerDomains.every((d) => selected.has(d.domain));

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">SSL/TLS Certificates</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage SSL certificates for your domains
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchCertificates}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => setShowUpload(true)}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm"
          >
            <Upload size={14} />
            Upload Custom
          </Button>
          <Button
            onClick={() => handleBulkForceSSL(true)}
            disabled={forceSSLBulkRunning || certificates.length === 0}
            title="Enable HTTPS-only redirect on every domain with a live cert. Domains without a cert are skipped."
            className="flex items-center gap-2 px-3 py-2 bg-emerald-600/15 border border-emerald-500/40 text-emerald-300 hover:bg-emerald-600/25 rounded-lg text-sm disabled:opacity-50"
          >
            <Lock size={14} className={forceSSLBulkRunning ? "animate-pulse" : ""} />
            {forceSSLBulkRunning ? "Forcing…" : "Force HTTPS for All"}
          </Button>
          <Button
            onClick={openIssue}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Issue / Reissue Certificate
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-4 flex flex-col md:flex-row md:items-center gap-3">
          <div className="relative flex-1">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search certificates..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-10 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm"
            />
          </div>
          <div className="flex items-center gap-1 bg-panel-bg border border-panel-border rounded-lg p-1">
            {(
              [
                { value: "all" as const, label: `All (${counts.all})` },
                { value: "active" as const, label: `Active (${counts.active})` },
                { value: "inactive" as const, label: `Inactive (${counts.inactive})` },
              ]
            ).map((f) => (
              <button
                key={f.value}
                onClick={() => setStatusFilter(f.value)}
                className={`px-3 py-1.5 rounded-md text-xs font-medium transition-colors ${
                  statusFilter === f.value
                    ? "bg-blue-600 text-white"
                    : "text-panel-muted hover:text-panel-text"
                }`}
              >
                {f.label}
              </button>
            ))}
          </div>
        </div>
      </Card>

      <Card>
        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2, 3, 4].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : filteredRows.length > 0 ? (
          <Table columns={columns} data={filteredRows} />
        ) : (
          <div className="text-center py-16 px-4">
            <ShieldCheck size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">
              {mergedRows.length === 0 ? "No domains yet" : "Nothing matches your filters"}
            </h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {mergedRows.length === 0
                ? "Add a domain first, then come back here to issue an SSL certificate."
                : "Try a different search term or status filter."}
            </p>
            {mergedRows.length > 0 && (
              <Button
                onClick={openIssue}
                className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} />
                Issue Certificate
              </Button>
            )}
          </div>
        )}
      </Card>

      {/* Bulk Let's Encrypt issue / reissue modal */}
      <Modal
        isOpen={showIssue}
        onClose={() => setShowIssue(false)}
        title="Issue / Reissue Let's Encrypt Certificates"
        size="xl"
      >
        <form onSubmit={handleBulkIssue} className="space-y-4">
          <div className="flex items-center gap-3">
            <div className="relative flex-1">
              <Search
                size={14}
                className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
              />
              <input
                type="text"
                placeholder="Search domains..."
                value={pickerSearch}
                onChange={(e) => setPickerSearch(e.target.value)}
                className="w-full pl-9 pr-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder-panel-muted/60 focus:outline-none focus:ring-2 focus:ring-blue-500/40"
              />
            </div>
            <div className="flex items-center gap-1 bg-panel-bg border border-panel-border rounded-lg p-1">
              {(
                [
                  { value: "all" as const, label: "All" },
                  { value: "active" as const, label: "Active SSL" },
                  { value: "inactive" as const, label: "No SSL" },
                ]
              ).map((f) => (
                <button
                  key={f.value}
                  type="button"
                  onClick={() => setPickerFilter(f.value)}
                  className={`px-3 py-1.5 rounded-md text-xs font-medium transition-colors ${
                    pickerFilter === f.value
                      ? "bg-blue-600 text-white"
                      : "text-panel-muted hover:text-panel-text"
                  }`}
                >
                  {f.label}
                </button>
              ))}
            </div>
          </div>

          <div className="border border-panel-border rounded-lg overflow-hidden">
            <div className="flex items-center justify-between px-3 py-2 bg-panel-bg/50 border-b border-panel-border">
              <label className="flex items-center gap-2 text-xs text-panel-muted cursor-pointer">
                <input
                  type="checkbox"
                  checked={visibleAllSelected}
                  onChange={toggleAllVisible}
                  className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600"
                />
                {visibleAllSelected ? "Deselect all" : "Select all visible"}
              </label>
              <span className="text-xs text-panel-muted">
                {selected.size} selected · {filteredPickerDomains.length} shown
              </span>
            </div>
            <div className="max-h-72 overflow-y-auto divide-y divide-panel-border/40">
              {domainsLoading ? (
                <div className="p-6 text-center text-sm text-panel-muted">
                  Loading domains...
                </div>
              ) : filteredPickerDomains.length === 0 ? (
                <div className="p-6 text-center text-sm text-panel-muted">
                  No domains match your filter.
                </div>
              ) : (
                filteredPickerDomains.map((d) => {
                  const checked = selected.has(d.domain);
                  const active = domainSslActive(d);
                  return (
                    <label
                      key={d.id}
                      className={`flex items-center gap-3 px-3 py-2 cursor-pointer hover:bg-panel-bg/40 transition-colors ${
                        checked ? "bg-blue-500/5" : ""
                      }`}
                    >
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={() => toggleOne(d.domain)}
                        className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600"
                      />
                      <ShieldCheck
                        size={14}
                        className={active ? "text-green-400" : "text-panel-muted/50"}
                      />
                      <span className="font-mono text-sm text-panel-text flex-1 truncate">
                        {d.domain}
                      </span>
                      {d.owner_email && (
                        <span className="text-xs text-panel-muted hidden md:inline">
                          {d.owner_email}
                        </span>
                      )}
                      <span
                        className={`text-xs px-2 py-0.5 rounded-full ${
                          active
                            ? "bg-green-500/10 text-green-400"
                            : "bg-panel-border/30 text-panel-muted"
                        }`}
                      >
                        {active ? "active" : "no SSL"}
                      </span>
                    </label>
                  );
                })
              )}
            </div>
          </div>

          {/* No shared email field — each cert in the batch is
              registered under its own owning vendor's email
              (server-resolved per-domain). The picker rows above
              already display each vendor's email so the operator
              can sanity-check the routing before clicking Issue. */}
          <div className="rounded-lg border border-blue-500/20 bg-blue-500/5 px-3 py-2 text-xs text-blue-200/80 flex items-start gap-2">
            <AlertTriangle size={12} className="text-blue-300 mt-0.5 shrink-0" />
            <span>
              ACME registration email is set automatically per domain
              from each domain's owning vendor — a multi-vendor batch
              lands every cert under the right address. Vendor emails
              are shown next to each row above.
            </span>
          </div>

          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <input
                type="checkbox"
                id="wildcard"
                checked={wildcard}
                onChange={(e) => setWildcard(e.target.checked)}
                className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 focus:ring-blue-500/40"
              />
              <label htmlFor="wildcard" className="text-sm text-panel-text">
                Wildcard certificate (*.&lt;domain&gt;) for every selected domain
              </label>
            </div>
            <div className="flex items-center gap-2">
              <input
                type="checkbox"
                id="reissue"
                checked={reissue}
                onChange={(e) => setReissue(e.target.checked)}
                className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-600 focus:ring-blue-500/40"
              />
              <label htmlFor="reissue" className="text-sm text-panel-text">
                Force reissue (replace existing certificate, even if not near expiry)
              </label>
            </div>
            <p className="text-[11px] text-panel-muted pl-6">
              {reissue
                ? "Domains in the batch with a current cert will be REPLACED with a fresh one. Counts against Let's Encrypt's 5-duplicates-per-week limit."
                : "Domains with a current cert will be skipped (no certbot call). Use this for batches that mix new and already-issued domains and you only want to issue the missing ones."}
            </p>
          </div>

          <div className="flex items-center justify-between pt-2 border-t border-panel-border/40">
            <p className="text-xs text-panel-muted">
              Certificates are issued one at a time on the server — large batches may take a few minutes.
            </p>
            <div className="flex gap-3">
              <button
                type="button"
                onClick={() => setShowIssue(false)}
                className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={issuing || selected.size === 0}
                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {issuing
                  ? `${reissue ? "Reissuing" : "Issuing"} ${selected.size}...`
                  : `${reissue ? "Issue / Reissue" : "Issue"} ${selected.size || ""} Certificate${selected.size === 1 ? "" : "s"}`.trim()}
              </button>
            </div>
          </div>
        </form>
      </Modal>

      {/* Custom upload modal — single domain on purpose */}
      <Modal
        isOpen={showUpload}
        onClose={() => setShowUpload(false)}
        title="Upload Custom Certificate"
        size="lg"
      >
        <form onSubmit={handleUploadCustom} className="space-y-4">
          <div>
            <label className={labelClass}>Domain *</label>
            <select
              required
              value={uploadForm.domain}
              onChange={(e) => setUploadForm({ ...uploadForm, domain: e.target.value })}
              className={inputClass}
            >
              <option value="">Select a domain</option>
              {domains.map((d) => (
                <option key={d.id} value={d.domain}>
                  {d.domain}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className={labelClass}>Certificate (PEM) *</label>
            <textarea
              required
              value={uploadForm.certificate}
              onChange={(e) => setUploadForm({ ...uploadForm, certificate: e.target.value })}
              placeholder="-----BEGIN CERTIFICATE-----"
              className={`${inputClass} h-24 font-mono text-xs`}
            />
          </div>
          <div>
            <label className={labelClass}>Private Key (PEM) *</label>
            <textarea
              required
              value={uploadForm.private_key}
              onChange={(e) => setUploadForm({ ...uploadForm, private_key: e.target.value })}
              placeholder="-----BEGIN PRIVATE KEY-----"
              className={`${inputClass} h-24 font-mono text-xs`}
            />
          </div>
          <div>
            <label className={labelClass}>CA Bundle (optional)</label>
            <textarea
              value={uploadForm.ca_bundle}
              onChange={(e) => setUploadForm({ ...uploadForm, ca_bundle: e.target.value })}
              placeholder="-----BEGIN CERTIFICATE-----"
              className={`${inputClass} h-24 font-mono text-xs`}
            />
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <button
              type="button"
              onClick={() => setShowUpload(false)}
              className="px-4 py-2 text-sm text-panel-muted hover:text-panel-text border border-panel-border rounded-lg transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={uploading}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50"
            >
              {uploading ? "Uploading..." : "Upload Certificate"}
            </button>
          </div>
        </form>
      </Modal>

      {/* Live-progress + final results modal (v3.1.60).
          Renders the SSLBulkJob doc the backend updates after every
          certbot run — progress bar, current-domain indicator, per-
          row table that fills in as each domain completes. Once the
          job reaches a terminal state (completed / cancelled /
          failed) the polling effect stops and the modal stays open
          so the operator can review outcomes before closing. */}
      <Modal
        isOpen={showResults}
        onClose={() => {
          // Block closing the modal mid-run so the operator doesn't
          // think the issuance died when they navigate away — they
          // can hit Cancel explicitly to abort. Once the job hits a
          // terminal state we allow Close as usual.
          if (bulkJob && bulkJob.status === "running") return;
          setShowResults(false);
          setBulkJob(null);
          setBulkResult(null);
        }}
        title={
          bulkJob
            ? bulkJob.status === "running" || bulkJob.status === "queued"
              ? `${bulkJob.reissue ? "Reissuing" : "Issuing"} ${bulkJob.total} Certificate${bulkJob.total === 1 ? "" : "s"}`
              : bulkJob.status === "cancelled"
                ? "Cancelled"
                : bulkJob.status === "failed"
                  ? "Bulk Issue Failed"
                  : "Bulk Issue Results"
            : "Bulk Issue Results"
        }
        size="lg"
      >
        {bulkJob && (
          <div className="space-y-4">
            {/* Progress bar + current-domain indicator (only while
                the job is active — terminal states hide it to make
                the final outcome the focus). */}
            {(bulkJob.status === "running" || bulkJob.status === "queued") && (
              <div className="space-y-2">
                <div className="flex items-center justify-between text-xs">
                  <span className="text-panel-muted">
                    {bulkJob.success + bulkJob.failed} of {bulkJob.total} processed
                  </span>
                  <span className="text-panel-text font-medium">{bulkJob.progress}%</span>
                </div>
                <div className="h-2 bg-panel-bg rounded-full overflow-hidden border border-panel-border">
                  <div
                    className="h-full bg-blue-500 transition-all duration-500 ease-out"
                    style={{ width: `${bulkJob.progress}%` }}
                  />
                </div>
                {bulkJob.current_domain && (
                  <div className="text-xs text-blue-300 flex items-center gap-2">
                    <span className="inline-block w-2 h-2 rounded-full bg-blue-400 animate-pulse" />
                    <span className="text-panel-muted">Currently issuing:</span>
                    <span className="font-mono text-panel-text">{bulkJob.current_domain}</span>
                  </div>
                )}
                {bulkJob.cancel_requested && (
                  <div className="text-xs text-amber-300">
                    Cancellation requested — finishing the current row, then stopping.
                  </div>
                )}
              </div>
            )}

            {/* Counters row — same shape as the legacy results
                modal so the operator's eyes land on the same
                numbers. While running, the numbers tick up live. */}
            <div className="grid grid-cols-4 gap-2">
              <div className="rounded-lg border border-panel-border bg-panel-bg/30 p-2.5">
                <div className="text-[10px] text-panel-muted uppercase tracking-wider">Total</div>
                <div className="text-lg font-semibold text-panel-text">{bulkJob.total}</div>
              </div>
              <div className="rounded-lg border border-green-500/30 bg-green-500/5 p-2.5">
                <div className="text-[10px] text-green-300 uppercase tracking-wider">Succeeded</div>
                <div className="text-lg font-semibold text-green-400">{bulkJob.success}</div>
              </div>
              <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-2.5">
                <div className="text-[10px] text-red-300 uppercase tracking-wider">Failed</div>
                <div className="text-lg font-semibold text-red-400">{bulkJob.failed}</div>
              </div>
              <div className="rounded-lg border border-panel-border bg-panel-bg/30 p-2.5">
                <div className="text-[10px] text-panel-muted uppercase tracking-wider">Pending</div>
                <div className="text-lg font-semibold text-panel-muted">
                  {Math.max(0, bulkJob.total - bulkJob.success - bulkJob.failed)}
                </div>
              </div>
            </div>

            {/* Per-row table — every domain in the batch, with its
                current state. Rows update in-place as the backend
                processes each one: pending → in-progress → done. */}
            <div className="border border-panel-border rounded-lg max-h-96 overflow-y-auto divide-y divide-panel-border/40">
              {bulkJob.items.map((item) => {
                const isCurrent = bulkJob.current_domain === item.domain;
                // A row is "completed" once it has either success=true
                // OR a non-empty error message. Anything else (initial
                // placeholder, in-flight) renders the pending / spinner
                // state.
                const isDone = item.success || !!item.error;
                return (
                  <div
                    key={item.domain}
                    className={
                      "flex items-start gap-3 px-3 py-2 transition-colors " +
                      (isCurrent ? "bg-blue-500/10" : "")
                    }
                  >
                    <div className="mt-0.5 shrink-0">
                      {isDone ? (
                        item.success ? (
                          <CheckCircle2 size={16} className="text-green-400" />
                        ) : (
                          <XCircle size={16} className="text-red-400" />
                        )
                      ) : isCurrent ? (
                        <span className="inline-block w-4 h-4 rounded-full border-2 border-blue-400 border-t-transparent animate-spin" />
                      ) : (
                        <span className="inline-block w-3 h-3 rounded-full bg-panel-muted/30 mt-0.5 ml-0.5" />
                      )}
                    </div>
                    <div className="flex-1 min-w-0">
                      <div className="font-mono text-sm text-panel-text truncate">{item.domain}</div>
                      {isDone ? (
                        item.success ? (
                          <div className="text-xs text-panel-muted">
                            {item.action === "reissued" ? "Reissued" : "Issued"} · expires {formatDate(item.expires_at)}
                          </div>
                        ) : (
                          <div className="text-xs text-red-300 break-words">{item.error}</div>
                        )
                      ) : isCurrent ? (
                        <div className="text-xs text-blue-300">Running certbot…</div>
                      ) : (
                        <div className="text-xs text-panel-muted/60">Pending</div>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>

            {/* Footer — Cancel while running, Close when done. The
                Close button is the only path that resets bulkJob, so
                a stale terminal job won't bleed into the next click
                on the Issue button. */}
            <div className="flex justify-between items-center pt-2">
              <div className="text-[11px] text-panel-muted">
                {bulkJob.status === "running" && "Certificates are issued one at a time. Large batches take a few minutes."}
                {bulkJob.status === "queued" && "Queued — starting in a moment…"}
                {bulkJob.status === "completed" && "Done."}
                {bulkJob.status === "cancelled" && "Run cancelled. Already-issued rows are preserved."}
                {bulkJob.status === "failed" && "The job aborted unexpectedly. Already-issued rows are preserved."}
              </div>
              {bulkJob.status === "running" || bulkJob.status === "queued" ? (
                <button
                  type="button"
                  onClick={handleBulkJobCancel}
                  disabled={cancelling || bulkJob.cancel_requested}
                  className="px-4 py-2 text-sm bg-panel-surface border border-red-500/30 hover:bg-red-500/10 text-red-300 rounded-lg font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {bulkJob.cancel_requested ? "Cancelling…" : cancelling ? "Cancelling…" : "Cancel"}
                </button>
              ) : (
                <button
                  type="button"
                  onClick={() => {
                    setShowResults(false);
                    setBulkJob(null);
                    setBulkResult(null);
                  }}
                  className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors"
                >
                  Close
                </button>
              )}
            </div>
          </div>
        )}
        {/* Fallback for any leftover-state edge case where the
            legacy bulkResult was set but bulkJob wasn't — keeps the
            old code path renderable so nothing breaks on a partial
            rollout. */}
        {!bulkJob && bulkResult && (
          <div className="space-y-3 text-sm text-panel-muted">
            Result: {bulkResult.success} issued, {bulkResult.failed} failed.
          </div>
        )}
      </Modal>

      {/* ─── Bulk Force HTTPS Result Modal ─────────────────────────── */}
      <Modal
        isOpen={forceSSLBulkResult !== null}
        onClose={() => setForceSSLBulkResult(null)}
        title={`Force HTTPS for All — ${forceSSLBulkResult?.enable === false ? "Disabled" : "Enabled"}`}
        size="lg"
      >
        {forceSSLBulkResult && (
          <div className="space-y-3">
            <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-300">
              <strong>{forceSSLBulkResult.successes ?? 0}</strong> updated
              {(forceSSLBulkResult.skipped ?? 0) > 0 && (
                <> · <strong className="text-amber-300">{forceSSLBulkResult.skipped}</strong> skipped (no live cert)</>
              )}
              {(forceSSLBulkResult.failures ?? 0) > 0 && (
                <> · <strong className="text-red-300">{forceSSLBulkResult.failures}</strong> failed</>
              )}
            </div>
            <div className="rounded-lg border border-panel-border max-h-96 overflow-y-auto">
              <table className="w-full text-xs">
                <thead className="bg-panel-bg/60 sticky top-0">
                  <tr className="text-left text-panel-muted">
                    <th className="px-3 py-2 font-medium">Domain</th>
                    <th className="px-3 py-2 font-medium">Status</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-panel-border/40">
                  {(forceSSLBulkResult.items || []).map((it: any) => (
                    <tr key={it.id}>
                      <td className="px-3 py-1.5 font-mono text-panel-text">{it.domain}</td>
                      <td className="px-3 py-1.5">
                        {it.skipped ? (
                          <span className="text-amber-300" title={it.skipped}>skipped — {it.skipped}</span>
                        ) : it.success ? (
                          <span className="text-emerald-400">{forceSSLBulkResult.enable ? "force-https on" : "force-https off"}</span>
                        ) : (
                          <span className="text-red-400" title={it.error}>{it.error || "failed"}</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="flex justify-end pt-2">
              <Button
                onClick={() => setForceSSLBulkResult(null)}
                className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm"
              >
                Done
              </Button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
