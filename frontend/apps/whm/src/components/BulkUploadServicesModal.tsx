import { useState, useRef } from "react";
import { Modal, Button } from "@serverpanel/ui";
import api from "@/lib/api";

// BulkUploadServicesModal is the file-picker UI behind the WHM
// "Bulk Upload Services" button on the Deploy Software project
// detail drawer. The shape mirrors BulkUploadDomainsModal (multipart
// upload, template download, results table after upload) but the
// row model is service-shaped — name + role + framework + port
// instead of domain + user + SSL.
//
// Why local (apps/whm/src/components) instead of shared (packages/ui):
// the service row model carries fields the domain UI never needs
// (final_port, missing_env_keys, framework, role) and the field
// layout has to communicate that a row is provisioning a full app
// (clone + build + nginx + SSL), not just a DNS row. A shared
// component would either bloat with conditional rendering or pull
// these service-specific concerns into a domain-shaped abstraction.
//
// Server side: POST /projects/:id/services/bulk runs each row through
// the same AddService pipeline the single-create form uses, so port
// conflicts / subpath uniqueness / preset defaults / vhost + SSL all
// behave identically. Partial success is normal — the result table
// shows per-row outcomes including the auto-allocated port (operators
// uploading 10 backends with no port column need to know what they
// got).

interface ServiceRowResult {
  row_number: number;
  name: string;
  role: string;
  framework: string;
  primary_domain: string;
  success: boolean;
  error?: string;
  service_id?: string;
  final_port?: number;
  missing_env_keys?: string[];
}

interface BulkServicesUploadResponse {
  format: "csv" | "xlsx";
  total_rows: number;
  successes: number;
  failures: number;
  items: ServiceRowResult[];
}

interface Props {
  projectId: string;
  projectName: string;
  isOpen: boolean;
  onClose: () => void;
  // onUploaded fires after any row succeeds so the parent drawer
  // can refresh its services list without the operator hitting
  // Refresh manually.
  onUploaded?: () => void;
}

export function BulkUploadServicesModal({ projectId, projectName, isOpen, onClose, onUploaded }: Props) {
  const [file, setFile] = useState<File | null>(null);
  const [uploading, setUploading] = useState(false);
  const [downloading, setDownloading] = useState<"csv" | "xlsx" | null>(null);
  const [error, setError] = useState("");
  const [result, setResult] = useState<BulkServicesUploadResponse | null>(null);
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
    // Client-side pre-validate filename + size so a 50 MB upload
    // doesn't burn an HTTP round-trip just to be rejected by the
    // 10 MB server cap.
    const lower = f.name.toLowerCase();
    if (!lower.endsWith(".csv") && !lower.endsWith(".xlsx") && !lower.endsWith(".xls")) {
      setError("Only .csv, .xlsx, .xls files are accepted.");
      setFile(null);
      return;
    }
    if (f.size > 10 * 1024 * 1024) {
      setError("File is larger than 10 MB. Split it into batches.");
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
      const fd = new FormData();
      fd.append("file", file);
      const { data } = await api.post(`/projects/${projectId}/services/bulk`, fd);
      const r = (data?.data || data) as BulkServicesUploadResponse;
      setResult(r);
      if (r.successes > 0 && onUploaded) onUploaded();
    } catch (e) {
      const ax = e as { message?: string; response?: { data?: { error?: { message?: string }; message?: string } } };
      setError(
        ax.response?.data?.error?.message ||
        ax.response?.data?.message ||
        ax.message ||
        "Upload failed"
      );
    } finally {
      setUploading(false);
    }
  };

  const onDownload = async (format: "csv" | "xlsx") => {
    setDownloading(format);
    setError("");
    try {
      // Use the same axios instance so the bearer token is attached;
      // ask for a blob so we can trigger a browser download instead
      // of dumping the bytes into the JSON response interceptor.
      const resp = await api.get(`/projects/${projectId}/services/bulk/template`, {
        params: { format },
        responseType: "blob",
      });
      const blob = resp.data as Blob;
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = format === "xlsx"
        ? `services-bulk-upload-template.xlsx`
        : `services-bulk-upload-template.csv`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    } catch (e) {
      setError((e as { message?: string }).message || "Template download failed");
    } finally {
      setDownloading(null);
    }
  };

  const showResults = result !== null;

  return (
    <Modal isOpen={isOpen} title={`Bulk Upload Services — ${projectName}`} onClose={close} size="lg">
      <div className="space-y-4">
        {/* Helper banner */}
        <div className="px-4 py-3 rounded-lg bg-blue-500/10 border border-blue-500/30 text-sm text-blue-200">
          Upload a CSV or Excel file with one service per row. Each row
          goes through the same Add Service pipeline as the manual form —
          clone, framework preset, install + build, port allocation,
          nginx vhost, and Let's Encrypt SSL. Per-row failures don't
          abort the batch.
        </div>

        {!showResults && (
          <>
            {/* Template download */}
            <div className="border border-panel-border rounded-lg p-4 bg-panel-bg/30">
              <div className="flex items-center justify-between gap-2">
                <div>
                  <div className="text-sm font-medium text-panel-text">Need a template?</div>
                  <div className="text-xs text-panel-muted mt-0.5">
                    Sample with the right columns + three example rows
                    (backend with explicit port, static frontend on apex,
                    Next.js minimal). alias_domains + env_vars use
                    semicolons.
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

            {/* Column quick reference */}
            <details className="border border-panel-border rounded-lg p-3 bg-panel-bg/20">
              <summary className="cursor-pointer text-xs font-medium text-panel-text">Column quick reference</summary>
              <div className="mt-2 text-[11px] text-panel-muted leading-relaxed space-y-1">
                <div><b className="text-panel-text">name</b> — required, unique within the project</div>
                <div><b className="text-panel-text">role</b> — backend / frontend / static. Blank → derived from framework (static preset → frontend; else backend)</div>
                <div><b className="text-panel-text">framework</b> — node-express, nextjs, nestjs, nuxt, react-vite, vue-vite, vue-express, python-flask, ruby-sinatra, go-vanilla / -gin / -fiber / -echo / -chi</div>
                <div><b className="text-panel-text">subpath</b> — monorepo subdir (e.g. apps/api)</div>
                <div><b className="text-panel-text">path_prefix</b> — nginx location (e.g. /api when backend shares domain)</div>
                <div><b className="text-panel-text">primary_domain</b> — optional; when set, must already exist in the panel. Omit it for an attached-only service (use alias_domains) or a port-only service (no public vhost/SSL; attach a domain later)</div>
                <div><b className="text-panel-text">port</b> — backend only; blank = auto-allocate</div>
                <div><b className="text-panel-text">alias_domains</b> — semicolon-separated list</div>
                <div><b className="text-panel-text">env_vars</b> — semicolon-separated KEY=VALUE pairs</div>
                <div><b className="text-panel-text">install_cmd / build_cmd / start_cmd</b> — leave blank to use the framework preset's defaults</div>
              </div>
            </details>

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
            <div className="grid grid-cols-3 gap-2">
              <Stat label="Rows" value={result.total_rows} tone="muted" />
              <Stat label="Created" value={result.successes} tone="success" />
              <Stat label="Failed" value={result.failures} tone="danger" />
            </div>
            <div className="border border-panel-border rounded-lg overflow-hidden">
              <div className="max-h-96 overflow-y-auto">
                <table className="w-full text-xs">
                  <thead className="bg-panel-surface sticky top-0">
                    <tr className="text-left text-panel-muted">
                      <th className="px-3 py-2 font-medium">#</th>
                      <th className="px-3 py-2 font-medium">Name</th>
                      <th className="px-3 py-2 font-medium">Role / FW</th>
                      <th className="px-3 py-2 font-medium">Domain</th>
                      <th className="px-3 py-2 font-medium">Port</th>
                      <th className="px-3 py-2 font-medium">Result</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.items.map((it) => (
                      <RowDetail key={it.row_number} item={it} />
                    ))}
                    {result.items.length === 0 && (
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

function RowDetail({ item }: { item: ServiceRowResult }) {
  const missing = item.missing_env_keys?.length ?? 0;
  return (
    <>
      <tr className="border-t border-panel-border align-top">
        <td className="px-3 py-2 text-panel-muted">{item.row_number}</td>
        <td className="px-3 py-2 text-panel-text font-mono">{item.name || "—"}</td>
        <td className="px-3 py-2 text-panel-muted">
          <div>{item.role || "—"}</div>
          {item.framework && <div className="text-[10px] opacity-70">{item.framework}</div>}
        </td>
        <td className="px-3 py-2 text-panel-text font-mono break-all">{item.primary_domain || "—"}</td>
        <td className="px-3 py-2 text-panel-muted font-mono">
          {item.final_port && item.final_port > 0 ? item.final_port : "—"}
        </td>
        <td className="px-3 py-2">
          {item.success ? (
            <span className="text-green-400">✓ created</span>
          ) : (
            <span className="text-red-300" title={item.error}>
              ✗ {(item.error || "failed").slice(0, 80)}
            </span>
          )}
          {missing > 0 && (
            <span
              className="ml-2 px-1.5 py-0.5 rounded text-[10px] bg-amber-500/10 text-amber-300 border border-amber-500/30 cursor-help"
              title={`Missing env vars: ${item.missing_env_keys!.join(", ")}`}
            >
              needs env vars
            </span>
          )}
        </td>
      </tr>
    </>
  );
}

function Stat({ label, value, tone }: { label: string; value: number; tone: "muted" | "success" | "danger" }) {
  const cls = {
    muted: "text-panel-muted",
    success: "text-green-400",
    danger: "text-red-300",
  }[tone];
  return (
    <div className="px-3 py-2 bg-panel-bg/30 border border-panel-border rounded-lg">
      <div className="text-xs text-panel-muted">{label}</div>
      <div className={`text-lg font-medium ${cls}`}>{value}</div>
    </div>
  );
}
