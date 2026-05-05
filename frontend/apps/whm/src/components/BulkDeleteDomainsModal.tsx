import { useState } from "react";
import { Modal, Button } from "@serverpanel/ui";
import { AlertTriangle, Mail, KeyRound } from "lucide-react";

// BulkDeleteDomainsModal is the WHM-only two-step OTP-gated flow
// behind the Domains page's "Bulk Delete" button. Lives in apps/whm
// (not packages/ui) because the User Panel deliberately doesn't
// expose this surface — vendors delete one row at a time via the
// per-row trash icon, which already has its own confirmation.
//
// Step 1 ("review"):
//   - List the domains queued for deletion (with a "+N more" cap so
//     a 200-row selection doesn't push everything else off-screen).
//   - "Send OTP" button calls /domains/bulk-delete/request-otp,
//     which mails a 6-digit code to the admin's email and returns
//     a token + the masked email + an expiry timestamp.
// Step 2 ("verify"):
//   - 6-digit code input + "Confirm Delete" button which calls
//     /domains/bulk-delete/confirm with the token + code. On
//     success the per-row result table replaces the form.
// Step 3 ("result"):
//   - Counters (created vs failed) + per-row outcome list. Same
//     shape as the bulk-upload result render so operators see
//     consistent feedback across destructive bulk operations.

export interface BulkDeleteRequestResult {
  token: string;
  email: string;
  domain_count: number;
  domain_names: string[];
  expires_at: string;
  mailer_enabled: boolean;
}

export interface BulkDeleteRowResult {
  id: string;
  domain: string;
  success: boolean;
  error?: string;
}

export interface BulkDeleteConfirmResult {
  total_rows: number;
  successes: number;
  failures: number;
  items: BulkDeleteRowResult[];
}

export interface BulkDeleteDomainsModalProps {
  isOpen: boolean;
  onClose: () => void;
  selectedIds: string[];
  selectedNames: string[];
  // requestOtp + confirm are caller-supplied so the modal stays
  // axios-agnostic. Same callback pattern as BulkUploadDomainsModal.
  requestOtp: (ids: string[]) => Promise<BulkDeleteRequestResult>;
  confirm: (token: string, code: string) => Promise<BulkDeleteConfirmResult>;
  // onConfirmed fires after a successful delete so the parent can
  // refresh the domains list and clear the selection.
  onConfirmed?: () => void;
}

type Step = "review" | "verify" | "result";

export function BulkDeleteDomainsModal({
  isOpen,
  onClose,
  selectedIds,
  selectedNames,
  requestOtp,
  confirm,
  onConfirmed,
}: BulkDeleteDomainsModalProps) {
  const [step, setStep] = useState<Step>("review");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>("");
  const [request, setRequest] = useState<BulkDeleteRequestResult | null>(null);
  const [code, setCode] = useState("");
  const [result, setResult] = useState<BulkDeleteConfirmResult | null>(null);

  const reset = () => {
    setStep("review");
    setBusy(false);
    setError("");
    setRequest(null);
    setCode("");
    setResult(null);
  };

  const close = () => {
    reset();
    onClose();
  };

  const onSendOtp = async () => {
    setBusy(true);
    setError("");
    try {
      const r = await requestOtp(selectedIds);
      setRequest(r);
      setStep("verify");
    } catch (e) {
      const msg = (e as { message?: string; response?: { data?: { message?: string } } });
      setError(msg.response?.data?.message || msg.message || "Could not send OTP");
    } finally {
      setBusy(false);
    }
  };

  const onConfirm = async () => {
    if (!request) return;
    if (code.trim().length !== 6) {
      setError("Enter the 6-digit code from the email.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const r = await confirm(request.token, code.trim());
      setResult(r);
      setStep("result");
      if (r.successes > 0 && onConfirmed) onConfirmed();
    } catch (e) {
      const msg = (e as { message?: string; response?: { data?: { message?: string } } });
      setError(msg.response?.data?.message || msg.message || "Confirmation failed");
    } finally {
      setBusy(false);
    }
  };

  const previewNames = selectedNames.slice(0, 8);
  const moreCount = Math.max(0, selectedNames.length - previewNames.length);

  return (
    <Modal isOpen={isOpen} title="Bulk Delete Domains" onClose={close} size="lg">
      <div className="space-y-4">
        {step === "review" && (
          <>
            <div className="px-4 py-3 rounded-lg bg-red-500/10 border border-red-500/30 text-sm text-red-200 flex gap-2">
              <AlertTriangle size={18} className="shrink-0 mt-0.5" />
              <div>
                <div className="font-medium">This action is destructive and cannot be undone.</div>
                <div className="text-red-300/80 mt-1">
                  Each deletion removes the nginx vhost, PHP-FPM pool, SSL records,
                  and DNS entries. Files under <code>/home/&lt;user&gt;/domains/&lt;domain&gt;</code> are preserved.
                </div>
              </div>
            </div>

            <div>
              <div className="text-sm text-panel-muted mb-1.5">
                You're about to delete <span className="text-panel-text font-medium">{selectedIds.length}</span> domain(s):
              </div>
              <div className="border border-panel-border rounded-lg p-3 max-h-44 overflow-y-auto text-xs font-mono">
                {previewNames.map((n) => (
                  <div key={n} className="text-panel-text">{n}</div>
                ))}
                {moreCount > 0 && (
                  <div className="text-panel-muted mt-1">… and {moreCount} more</div>
                )}
                {selectedNames.length === 0 && (
                  <div className="text-panel-muted">{selectedIds.length} selected</div>
                )}
              </div>
            </div>

            <div className="px-3 py-2.5 rounded-lg bg-blue-500/10 border border-blue-500/30 text-sm text-blue-200 flex gap-2">
              <Mail size={16} className="shrink-0 mt-0.5" />
              <div>
                Clicking "Send OTP" mails a 6-digit code to your admin email.
                You'll have 10 minutes to enter it. No domains will be deleted
                until you confirm with the code.
              </div>
            </div>

            {error && (
              <div className="px-3 py-2 rounded bg-red-500/10 border border-red-500/30 text-sm text-red-200">
                {error}
              </div>
            )}

            <div className="flex items-center justify-end gap-2 pt-2 border-t border-panel-border">
              <Button onClick={close} className="px-3 py-1.5 text-sm bg-panel-surface border border-panel-border rounded text-panel-text hover:bg-panel-border/40">
                Cancel
              </Button>
              <Button
                onClick={onSendOtp}
                disabled={busy || selectedIds.length === 0}
                className="px-4 py-1.5 text-sm bg-red-600 hover:bg-red-700 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded font-medium"
              >
                {busy ? "Sending…" : "Send OTP"}
              </Button>
            </div>
          </>
        )}

        {step === "verify" && request && (
          <>
            <div className="px-4 py-3 rounded-lg bg-blue-500/10 border border-blue-500/30 text-sm text-blue-200 flex gap-2">
              <Mail size={18} className="shrink-0 mt-0.5" />
              <div>
                A 6-digit code was sent to <strong>{request.email}</strong>.
                {!request.mailer_enabled && (
                  <div className="text-amber-300 mt-1">
                    SMTP isn't configured on this server — the code is in the
                    panel's stderr (run <code>journalctl -u serverpanel -n 50</code> to find it).
                  </div>
                )}
              </div>
            </div>

            <div>
              <label className="block text-sm font-medium text-panel-text mb-2 flex items-center gap-2">
                <KeyRound size={14} /> Enter the 6-digit code
              </label>
              <input
                autoFocus
                type="text"
                inputMode="numeric"
                pattern="[0-9]*"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, "").slice(0, 6))}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && code.length === 6 && !busy) onConfirm();
                }}
                className="w-full px-4 py-3 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-2xl tracking-[0.5em] font-mono text-center focus:outline-none focus:ring-2 focus:ring-red-500/50 focus:border-red-500"
                placeholder="······"
              />
              <div className="text-xs text-panel-muted mt-1.5">
                Code expires {new Date(request.expires_at).toLocaleString()}.
              </div>
            </div>

            <div className="px-3 py-2 rounded-lg bg-panel-bg/30 border border-panel-border text-xs text-panel-muted">
              Confirming will permanently delete <strong className="text-panel-text">{request.domain_count}</strong> domain(s).
            </div>

            {error && (
              <div className="px-3 py-2 rounded bg-red-500/10 border border-red-500/30 text-sm text-red-200">
                {error}
              </div>
            )}

            <div className="flex items-center justify-end gap-2 pt-2 border-t border-panel-border">
              <Button onClick={close} className="px-3 py-1.5 text-sm bg-panel-surface border border-panel-border rounded text-panel-text hover:bg-panel-border/40">
                Cancel
              </Button>
              <Button
                onClick={onConfirm}
                disabled={busy || code.length !== 6}
                className="px-4 py-1.5 text-sm bg-red-600 hover:bg-red-700 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded font-medium"
              >
                {busy ? "Deleting…" : "Confirm Delete"}
              </Button>
            </div>
          </>
        )}

        {step === "result" && result && (
          <>
            <div className="grid grid-cols-3 gap-2">
              <Stat label="Requested" value={result.total_rows} tone="muted" />
              <Stat label="Deleted" value={result.successes} tone="success" />
              <Stat label="Failed" value={result.failures} tone="danger" />
            </div>
            <div className="border border-panel-border rounded-lg overflow-hidden">
              <div className="max-h-72 overflow-y-auto">
                <table className="w-full text-xs">
                  <thead className="bg-panel-surface sticky top-0">
                    <tr className="text-left text-panel-muted">
                      <th className="px-3 py-2 font-medium">Domain</th>
                      <th className="px-3 py-2 font-medium">Result</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.items.map((item) => (
                      <tr key={item.id} className="border-t border-panel-border">
                        <td className="px-3 py-2 text-panel-text font-mono">{item.domain || item.id}</td>
                        <td className="px-3 py-2">
                          {item.success ? (
                            <span className="text-green-400">✓ deleted</span>
                          ) : (
                            <span className="text-red-300" title={item.error}>
                              ✗ {(item.error || "failed").slice(0, 80)}
                            </span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
            <div className="flex items-center justify-end gap-2 pt-2 border-t border-panel-border">
              <Button onClick={close} className="px-4 py-1.5 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded">
                Close
              </Button>
            </div>
          </>
        )}
      </div>
    </Modal>
  );
}

function Stat({ label, value, tone }: { label: string; value: number; tone: "muted" | "success" | "danger" }) {
  const toneClass = {
    muted: "text-panel-muted",
    success: "text-green-400",
    danger: "text-red-300",
  }[tone];
  return (
    <div className="px-3 py-2 bg-panel-bg/30 border border-panel-border rounded-lg">
      <div className="text-xs text-panel-muted">{label}</div>
      <div className={`text-lg font-medium ${toneClass}`}>{value}</div>
    </div>
  );
}
