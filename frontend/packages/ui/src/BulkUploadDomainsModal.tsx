import { useState, useRef } from "react";
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

export interface BulkUploadDomainsModalProps {
  isOpen: boolean;
  onClose: () => void;
  // submit performs the multipart POST. Caller wraps axios + adds the
  // bearer token. issue_ssl / force_ssl are passed as form fields so
  // the operator can opt out before kicking off a 50-row upload.
  submit: (file: File, opts: { issue_ssl: boolean; force_ssl: boolean }) => Promise<BulkUploadDomainsResponse>;
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

export function BulkUploadDomainsModal({
  isOpen,
  onClose,
  submit,
  downloadTemplate,
  scopeLabel,
  onUploaded,
}: BulkUploadDomainsModalProps) {
  const [file, setFile] = useState<File | null>(null);
  const [issueSSL, setIssueSSL] = useState(true);
  const [forceSSL, setForceSSL] = useState(true);
  const [uploading, setUploading] = useState(false);
  const [downloading, setDownloading] = useState<"csv" | "xlsx" | null>(null);
  const [error, setError] = useState<string>("");
  const [result, setResult] = useState<BulkUploadDomainsResponse | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const reset = () => {
    setFile(null);
    setError("");
    setResult(null);
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const close = () => {
    reset();
    onClose();
  };

  const onFilePicked = (f: File | null) => {
    setError("");
    setResult(null);
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
    try {
      const r = await submit(file, { issue_ssl: issueSSL, force_ssl: forceSSL });
      setResult(r);
      if (r.successes > 0 && onUploaded) onUploaded();
    } catch (e) {
      const msg = (e as { message?: string; response?: { data?: { message?: string } } });
      setError(msg.response?.data?.message || msg.message || "Upload failed");
    } finally {
      setUploading(false);
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

  const showResults = result !== null;

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

        {!showResults && (
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

        {/* Results table */}
        {showResults && result && (
          <div className="space-y-3">
            <div className="grid grid-cols-4 gap-2">
              <Stat label="Rows" value={result.total_rows} tone="muted" />
              <Stat label="Created" value={result.successes} tone="success" />
              <Stat label="Failed" value={result.failures} tone="danger" />
              <Stat label="SSL Active" value={result.ssl_forced} tone="info" />
            </div>
            <div className="border border-panel-border rounded-lg overflow-hidden">
              <div className="max-h-96 overflow-y-auto">
                <table className="w-full text-xs">
                  <thead className="bg-panel-surface sticky top-0">
                    <tr className="text-left text-panel-muted">
                      <th className="px-3 py-2 font-medium">#</th>
                      <th className="px-3 py-2 font-medium">Domain</th>
                      <th className="px-3 py-2 font-medium">Owner</th>
                      <th className="px-3 py-2 font-medium">Result</th>
                      <th className="px-3 py-2 font-medium">SSL</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.items.map((item) => (
                      <tr key={item.row_number} className="border-t border-panel-border">
                        <td className="px-3 py-2 text-panel-muted">{item.row_number}</td>
                        <td className="px-3 py-2 text-panel-text font-mono">{item.domain || "—"}</td>
                        <td className="px-3 py-2 text-panel-muted">{item.user || "—"}</td>
                        <td className="px-3 py-2">
                          {item.success ? (
                            <span className="text-green-400">✓ created</span>
                          ) : (
                            <span className="text-red-300" title={item.error}>
                              ✗ {(item.error || "failed").slice(0, 60)}
                            </span>
                          )}
                        </td>
                        <td className="px-3 py-2">
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
                      </tr>
                    ))}
                    {result.items.length === 0 && (
                      <tr>
                        <td colSpan={5} className="px-3 py-6 text-center text-panel-muted">
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
          {showResults ? (
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
              <Button onClick={close} className="px-3 py-1.5 text-sm bg-panel-surface border border-panel-border rounded text-panel-text hover:bg-panel-border/40">
                Cancel
              </Button>
              <Button
                onClick={onSubmit}
                disabled={!file || uploading}
                className="px-4 py-1.5 text-sm bg-blue-600 hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded font-medium"
              >
                {uploading ? "Uploading…" : "Upload"}
              </Button>
            </>
          )}
        </div>
      </div>
    </Modal>
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
