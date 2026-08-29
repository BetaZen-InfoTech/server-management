import { useState, useRef, useEffect } from "react";
import { Modal } from "./Modal";
import { Button } from "./Button";

// BulkUploadDomainsModal is the file-picker UI behind the WHM and User
// Panel "Bulk Upload" buttons on the Domains page. Identical UX on both
// surfaces; the caller passes the submit + downloadTemplate callbacks so
// the modal stays free of any axios / fetch wrapper assumptions and the
// per-surface tenant scoping (cPanel clobbers the row's `user`, WHM
// honours it) is handled server-side.
//
// File handling is FormData → multipart upload — never base64-in-JSON,
// because a 10 MB CSV would balloon to ~14 MB after base64 + JSON
// quoting and chew up an extra 30s on slow uplinks.

export interface BulkUploadDomainsRow {
  row_number: number;
  domain: string;
  user: string;
  success: boolean;
  error?: string;
  ssl_issued: boolean;
  ssl_forced: boolean;
  ssl_message?: string;
  // setup_warnings forwards DomainService.Create's per-step status
  // for non-fatal failures (zone create, mail setup, SSL retry give-up,
  // admin-mailbox creation). Empty / absent on a clean create.
  setup_warnings?: string[];
  // admin_mailbox + admin_mailbox_password surface the auto-created
  // admin@<domain> credentials. Operator should save the password
  // before the modal closes — it's not retrievable later (mailbox
  // password rotation is the only way to recover access).
  admin_mailbox?: string;
  admin_mailbox_password?: string;
}

export interface BulkUploadDomainsResponse {
  format: "csv" | "xlsx";
  total_rows: number;
  successes: number;
  failures: number;
  ssl_issued: number;
  ssl_forced: number;
  items: BulkUploadDomainsRow[];
}

// BulkUploadJobItem is one row's LIVE state — the same row shape plus a status
// that moves pending → creating → done/failed as the background worker runs.
export interface BulkUploadJobItem extends BulkUploadDomainsRow {
  status: "pending" | "creating" | "done" | "failed" | string;
}

// BulkUploadJob mirrors the backend models.DomainBulkJob returned by
// GET /domains/bulk-upload/jobs/{id}. The modal polls it for live progress.
export interface BulkUploadJob {
  id: string;
  status: "queued" | "running" | "completed" | "failed" | "cancelled" | string;
  total: number;
  processed: number;
  successes: number;
  failures: number;
  ssl_issued: number;
  ssl_forced: number;
  progress: number;
  current_domain?: string;
  items: BulkUploadJobItem[];
  error?: string;
}

export interface BulkUploadDomainsModalProps {
  isOpen: boolean;
  onClose: () => void;
  // startJob performs the multipart POST which now STARTS an async job and
  // returns its id. Caller wraps axios + adds the bearer token. issue_ssl /
  // force_ssl are passed as form fields so the operator can opt out.
  startJob: (file: File, opts: { issue_ssl: boolean; force_ssl: boolean }) => Promise<{ job_id: string; total: number }>;
  // pollJob fetches GET /domains/bulk-upload/jobs/{id} for live progress. The
  // modal polls this every ~1.5s until the job reaches a terminal state.
  pollJob: (jobId: string) => Promise<BulkUploadJob>;
  // cancelJob (optional) requests cancellation of a running job. The worker
  // stops between rows; already-created domains stay.
  cancelJob?: (jobId: string) => Promise<void>;
  // downloadTemplate fetches the CSV/XLSX template from
  // /domains/bulk-upload/template?format=… and triggers a browser
  // download. Caller is responsible for the network call so this
  // component doesn't need to know the API base path.
  downloadTemplate: (format: "csv" | "xlsx") => Promise<void>;
  // scopeLabel — same pattern as BulkTTLModal. "all vendors" on WHM,
  // "your account" on cPanel — surfaced in the helper text so an
  // operator can't mistake one surface's behavior for the other's.
  scopeLabel: string;
  // onUploaded — called after a successful upload (any successes ≥ 1)
  // so the parent can refresh the domain list without the operator
  // hitting Refresh manually.
  onUploaded?: () => void;
}

const JOB_TERMINAL = ["completed", "failed", "cancelled"];

export function BulkUploadDomainsModal({
  isOpen,
  onClose,
  startJob,
  pollJob,
  cancelJob,
  downloadTemplate,
  scopeLabel,
  onUploaded,
}: BulkUploadDomainsModalProps) {
  const [file, setFile] = useState<File | null>(null);
  const [issueSSL, setIssueSSL] = useState(true);
  const [forceSSL, setForceSSL] = useState(true);
  const [uploading, setUploading] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [downloading, setDownloading] = useState<"csv" | "xlsx" | null>(null);
  const [error, setError] = useState<string>("");
  const [job, setJob] = useState<BulkUploadJob | null>(null);
  const [jobId, setJobId] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  // runId invalidates stale in-flight polls: reset/close bumps it, so a poll
  // that resolves after the modal moved on can't resurrect an old job.
  const runIdRef = useRef(0);

  const stopPoll = () => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  };
  // Stop polling when the modal unmounts.
  useEffect(() => stopPoll, []);

  const reset = () => {
    stopPoll();
    runIdRef.current += 1;
    setFile(null);
    setError("");
    setJob(null);
    setJobId(null);
    setUploading(false);
    setCancelling(false);
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const close = () => {
    // Refresh the parent list on close too — the operator may close while the
    // job is still running in the background, and some domains are already live.
    if (job && onUploaded) onUploaded();
    reset();
    onClose();
  };

  const onFilePicked = (f: File | null) => {
    setError("");
    setJob(null);
    if (!f) {
      setFile(null);
      return;
    }
    // Pre-validate filename + size client-side so a 50 MB upload
    // doesn't burn an HTTP round-trip just to be rejected by the
    // 10 MB server cap.
    const lower = f.name.toLowerCase();
    if (!lower.endsWith(".csv") && !lower.endsWith(".xlsx") && !lower.endsWith(".xls")) {
      setError("Only .csv, .xlsx, .xls files are accepted.");
      setFile(null);
      return;
    }
    if (f.size > 10 * 1024 * 1024) {
      setError("File is larger than 10 MB. Split it into batches and upload each.");
      setFile(null);
      return;
    }
    setFile(f);
  };

  const onSubmit = async () => {
    if (!file) {
      setError("Pick a file first.");
      return;
    }
    setUploading(true);
    setError("");
    setJob(null);
    runIdRef.current += 1;
    const myRun = runIdRef.current;
    try {
      const { job_id } = await startJob(file, { issue_ssl: issueSSL, force_ssl: forceSSL });
      if (runIdRef.current !== myRun) return; // modal moved on while starting
      setJobId(job_id);
      let notified = false;
      let errs = 0;
      let inFlight = false;
      const tick = async () => {
        // Re-entrancy guard: if a poll is slower than the 1500ms interval, skip
        // this tick instead of firing an overlapping request. Without it,
        // concurrent in-flight failures each bump the shared `errs` counter and
        // can trip the 8-failure give-up from a brief latency spell.
        if (inFlight) return;
        inFlight = true;
        try {
          let j: BulkUploadJob | null = null;
          try {
            j = await pollJob(job_id);
            errs = 0;
          } catch {
            // Give up after a run of consecutive failures instead of polling
            // forever (server down / job gone) — surface it so the UI isn't stuck.
            if (++errs >= 8) {
              stopPoll();
              if (runIdRef.current === myRun) {
                setUploading(false);
                setError("Lost connection to the job. Domains may still be creating — refresh the Domains page.");
              }
            }
            return;
          }
          if (!j) return;
          if (runIdRef.current !== myRun) return; // stale poll — modal moved on
          setJob(j);
          if (JOB_TERMINAL.includes(j.status)) {
            stopPoll();
            setUploading(false);
            if (!notified && j.successes > 0 && onUploaded) {
              notified = true;
              onUploaded();
            }
          }
        } finally {
          inFlight = false;
        }
      };
      stopPoll();
      pollRef.current = setInterval(tick, 1500);
      tick();
    } catch (e) {
      if (runIdRef.current !== myRun) return;
      setUploading(false);
      const msg = (e as { message?: string; response?: { data?: { message?: string } } });
      setError(msg.response?.data?.message || msg.message || "Upload failed");
    }
  };

  const onCancel = async () => {
    if (!jobId || !cancelJob) return;
    setCancelling(true);
    try {
      await cancelJob(jobId);
    } catch {
      /* best-effort — the worker stops between rows */
    } finally {
      setCancelling(false);
    }
  };

  const onDownload = async (format: "csv" | "xlsx") => {
    setDownloading(format);
    setError("");
    try {
      await downloadTemplate(format);
    } catch (e) {
      setError((e as { message?: string }).message || "Template download failed");
    } finally {
      setDownloading(null);
    }
  };

  const showJob = job !== null;
  const jobDone = job !== null && JOB_TERMINAL.includes(job.status);

  return (
    <Modal
      isOpen={isOpen}
      title="Bulk Upload Domains"
      onClose={close}
      size="lg"
    >
      <div className="space-y-4">
        {/* Helper banner */}
        <div className="px-4 py-3 rounded-lg bg-blue-500/10 border border-blue-500/30 text-sm text-blue-200">
          Upload a CSV or Excel file with one domain per row. Each domain
          will be created under <strong>{scopeLabel}</strong>. SSL +
          force-HTTPS will be applied automatically (toggle below to
          opt out per upload).
        </div>

        {!showJob && (
          <>
            {/* Template download buttons */}
            <div className="border border-panel-border rounded-lg p-4 bg-panel-bg/30">
              <div className="flex items-center justify-between gap-2">
                <div>
                  <div className="text-sm font-medium text-panel-text">Need a template?</div>
                  <div className="text-xs text-panel-muted mt-0.5">
                    Download a sample with the right column headers + two example rows.
                  </div>
                </div>
                <div className="flex gap-2 shrink-0">
                  <Button
                    onClick={() => onDownload("csv")}
                    disabled={downloading !== null}
                    className="px-3 py-1.5 text-xs bg-panel-surface border border-panel-border rounded text-panel-text hover:bg-panel-border/40 transition-colors"
                  >
                    {downloading === "csv" ? "Downloading…" : "CSV"}
                  </Button>
                  <Button
                    onClick={() => onDownload("xlsx")}
                    disabled={downloading !== null}
                    className="px-3 py-1.5 text-xs bg-panel-surface border border-panel-border rounded text-panel-text hover:bg-panel-border/40 transition-colors"
                  >
                    {downloading === "xlsx" ? "Downloading…" : "Excel (.xlsx)"}
                  </Button>
                </div>
              </div>
            </div>

            {/* File picker */}
            <div>
              <label className="block text-sm font-medium text-panel-text mb-2">
                File <span className="text-red-400">*</span>
              </label>
              <input
                ref={fileInputRef}
                type="file"
                accept=".csv,.xlsx,.xls,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,application/vnd.ms-excel"
                onChange={(e) => onFilePicked(e.target.files?.[0] || null)}
                className="block w-full text-sm text-panel-text file:mr-3 file:py-2 file:px-4 file:rounded file:border-0 file:bg-blue-600 file:text-white file:cursor-pointer file:hover:bg-blue-700 file:text-sm bg-panel-bg border border-panel-border rounded-lg cursor-pointer"
              />
              {file && (
                <div className="text-xs text-panel-muted mt-1.5">
                  Selected: <span className="text-panel-text">{file.name}</span> ({(file.size / 1024).toFixed(1)} KB)
                </div>
              )}
            </div>

            {/* SSL toggles */}
            <div className="grid grid-cols-2 gap-3">
              <label className="flex items-start gap-2 px-3 py-2.5 bg-panel-bg/30 border border-panel-border rounded-lg cursor-pointer hover:bg-panel-bg/60">
                <input
                  type="checkbox"
                  checked={issueSSL}
                  onChange={(e) => {
                    setIssueSSL(e.target.checked);
                    if (!e.target.checked) setForceSSL(false);
                  }}
                  className="mt-0.5"
                />
                <div className="text-xs">
                  <div className="font-medium text-panel-text">Issue Let's Encrypt SSL</div>
                  <div className="text-panel-muted mt-0.5">Per-row, after create. Failures don't block other rows.</div>
                </div>
              </label>
              <label className={`flex items-start gap-2 px-3 py-2.5 bg-panel-bg/30 border border-panel-border rounded-lg cursor-pointer hover:bg-panel-bg/60 ${!issueSSL ? "opacity-40 pointer-events-none" : ""}`}>
                <input
                  type="checkbox"
                  checked={forceSSL}
                  onChange={(e) => setForceSSL(e.target.checked)}
                  disabled={!issueSSL}
                  className="mt-0.5"
                />
                <div className="text-xs">
                  <div className="font-medium text-panel-text">Force HTTPS redirect</div>
                  <div className="text-panel-muted mt-0.5">301-redirect HTTP→HTTPS once SSL is live.</div>
                </div>
              </label>
            </div>

            {/* Error */}
            {error && (
              <div className="px-3 py-2 rounded bg-red-500/10 border border-red-500/30 text-sm text-red-200">
                {error}
              </div>
            )}
          </>
        )}

        {/* Live progress + per-domain list */}
        {showJob && job && (
          <div className="space-y-3">
            {/* Progress bar */}
            <div>
              <div className="flex items-center justify-between text-xs mb-1">
                <span className={jobDone ? "text-panel-muted" : "text-blue-300"}>
                  {jobDone
                    ? job.status === "completed"
                      ? "Done — SSL is issuing in the background."
                      : job.status === "cancelled"
                        ? "Cancelled."
                        : "Failed."
                    : job.current_domain
                      ? `Creating ${job.current_domain}…`
                      : "Starting…"}
                </span>
                <span className="text-panel-muted">{job.processed}/{job.total}</span>
              </div>
              <div className="h-2 w-full rounded-full bg-panel-bg overflow-hidden">
                <div
                  className={`h-full transition-all duration-300 ${job.status === "failed" ? "bg-red-500" : job.status === "cancelled" ? "bg-amber-500" : "bg-blue-500"}`}
                  style={{ width: `${job.progress}%` }}
                />
              </div>
            </div>
            {job.error && (
              <div className="px-3 py-2 rounded bg-red-500/10 border border-red-500/30 text-xs text-red-200">{job.error}</div>
            )}
            {/* Client-side errors (e.g. poll give-up after lost connection) must
                render here too — the {!showJob} error banner is hidden once the
                live-progress view is up, which otherwise left a frozen bar. */}
            {error && (
              <div className="px-3 py-2 rounded bg-red-500/10 border border-red-500/30 text-xs text-red-200">{error}</div>
            )}
            {/* Counters */}
            <div className="grid grid-cols-4 gap-2">
              <Stat label="Rows" value={job.total} tone="muted" />
              <Stat label="Created" value={job.successes} tone="success" />
              <Stat label="Failed" value={job.failures} tone="danger" />
              <Stat label="Remaining" value={Math.max(0, job.total - job.processed)} tone="info" />
            </div>
            {/* Live per-domain list */}
            <div className="border border-panel-border rounded-lg overflow-hidden">
              <div className="max-h-96 overflow-y-auto">
                <table className="w-full text-xs">
                  <thead className="bg-panel-surface sticky top-0">
                    <tr className="text-left text-panel-muted">
                      <th className="px-3 py-2 font-medium">#</th>
                      <th className="px-3 py-2 font-medium">Domain</th>
                      <th className="px-3 py-2 font-medium">Owner</th>
                      <th className="px-3 py-2 font-medium">Status</th>
                      <th className="px-3 py-2 font-medium">SSL</th>
                      <th className="px-3 py-2 font-medium">Admin Mailbox</th>
                    </tr>
                  </thead>
                  <tbody>
                    {job.items.map((item) => (
                      <RowDetail key={item.row_number} item={item} />
                    ))}
                    {job.items.length === 0 && (
                      <tr>
                        <td colSpan={6} className="px-3 py-6 text-center text-panel-muted">
                          No data rows in the file.
                        </td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        )}

        {/* Footer actions */}
        <div className="flex items-center justify-end gap-2 pt-2 border-t border-panel-border">
          {showJob ? (
            jobDone ? (
              <>
                <Button onClick={reset} className="px-3 py-1.5 text-sm bg-panel-surface border border-panel-border rounded text-panel-text hover:bg-panel-border/40">
                  Upload Another
                </Button>
                <Button onClick={close} className="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded">
                  Close
                </Button>
              </>
            ) : (
              <>
                {cancelJob && jobId && (
                  <Button
                    onClick={onCancel}
                    disabled={cancelling}
                    className="px-3 py-1.5 text-sm bg-panel-surface border border-red-500/40 rounded text-red-300 hover:bg-red-500/10 disabled:opacity-50"
                  >
                    {cancelling ? "Cancelling…" : "Cancel job"}
                  </Button>
                )}
                <Button onClick={close} className="px-3 py-1.5 text-sm bg-panel-surface border border-panel-border rounded text-panel-text hover:bg-panel-border/40">
                  Close (keeps running in background)
                </Button>
              </>
            )
          ) : (
            <>
              <Button onClick={close} className="px-3 py-1.5 text-sm bg-panel-surface border border-panel-border rounded text-panel-text hover:bg-panel-border/40">
                Cancel
              </Button>
              <Button
                onClick={onSubmit}
                disabled={!file || uploading}
                className="px-4 py-1.5 text-sm bg-blue-600 hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded font-medium"
              >
                {uploading ? "Starting…" : "Upload"}
              </Button>
            </>
          )}
        </div>
      </div>
    </Modal>
  );
}

// RowDetail renders one bulk-upload row outcome: result, SSL state,
// and the auto-created admin mailbox credentials. The password is
// click-to-copy because the operator MUST save it before closing
// the modal — pre-3.1.16 it was discarded by the backend, now
// it's surfaced once and never again. setup_warnings render as a
// secondary line beneath the main row in subdued text.
function RowDetail({ item }: { item: BulkUploadJobItem }) {
  const copyPwd = () => {
    if (!item.admin_mailbox_password) return;
    navigator.clipboard?.writeText(item.admin_mailbox_password).catch(() => {/* noop */});
  };
  const warningCount = item.setup_warnings?.length ?? 0;
  const warningTitle = warningCount > 0 ? item.setup_warnings!.join("\n") : "";
  return (
    <>
      <tr className="border-t border-panel-border">
        <td className="px-3 py-2 text-panel-muted align-top">{item.row_number}</td>
        <td className="px-3 py-2 text-panel-text font-mono align-top">{item.domain || "—"}</td>
        <td className="px-3 py-2 text-panel-muted align-top">{item.user || "—"}</td>
        <td className="px-3 py-2 align-top">
          {item.status === "pending" ? (
            <span className="text-panel-muted">queued</span>
          ) : item.status === "creating" ? (
            <span className="text-blue-300 inline-flex items-center gap-1">
              <span className="inline-block h-2 w-2 rounded-full bg-blue-400 animate-pulse" /> creating…
            </span>
          ) : item.success ? (
            <span className="text-green-400">✓ created</span>
          ) : (
            <span className="text-red-300" title={item.error}>
              ✗ {(item.error || "failed").slice(0, 60)}
            </span>
          )}
          {warningCount > 0 && (
            <span
              className="ml-2 px-1.5 py-0.5 rounded text-[10px] bg-amber-500/10 text-amber-300 border border-amber-500/30 cursor-help"
              title={warningTitle}
            >
              {warningCount} warning{warningCount === 1 ? "" : "s"}
            </span>
          )}
        </td>
        <td className="px-3 py-2 align-top">
          {item.ssl_forced ? (
            <span className="text-green-400">force-https</span>
          ) : item.ssl_issued ? (
            <span className="text-blue-300">issued</span>
          ) : item.success ? (
            <span className="text-amber-300" title={item.ssl_message}>
              {(item.ssl_message || "skipped").slice(0, 30)}
            </span>
          ) : (
            <span className="text-panel-muted">—</span>
          )}
        </td>
        <td className="px-3 py-2 align-top">
          {item.admin_mailbox_password ? (
            <div className="flex flex-col gap-0.5">
              <div className="text-panel-text font-mono text-[11px]">{item.admin_mailbox}</div>
              <button
                onClick={copyPwd}
                className="text-left text-blue-300 hover:text-blue-200 font-mono text-[11px] underline-offset-2 hover:underline"
                title="Click to copy — save it now, the panel won't show it again"
              >
                {item.admin_mailbox_password}
              </button>
            </div>
          ) : item.success ? (
            <span className="text-panel-muted text-[11px]" title="Auto-create skipped — see warnings">—</span>
          ) : (
            <span className="text-panel-muted">—</span>
          )}
        </td>
      </tr>
      {warningCount > 0 && (
        <tr className="border-t border-panel-border/50 bg-amber-500/5">
          <td colSpan={6} className="px-3 py-1.5 text-[11px] text-amber-200/80">
            <ul className="list-disc list-inside space-y-0.5">
              {item.setup_warnings!.map((w, i) => (
                <li key={i}>{w}</li>
              ))}
            </ul>
          </td>
        </tr>
      )}
    </>
  );
}

function Stat({ label, value, tone }: { label: string; value: number; tone: "muted" | "success" | "danger" | "info" }) {
  const toneClass = {
    muted: "text-panel-muted",
    success: "text-green-400",
    danger: "text-red-300",
    info: "text-blue-300",
  }[tone];
  return (
    <div className="px-3 py-2 bg-panel-bg/30 border border-panel-border rounded-lg">
      <div className="text-xs text-panel-muted">{label}</div>
      <div className={`text-lg font-medium ${toneClass}`}>{value}</div>
    </div>
  );
}
