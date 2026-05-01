import { useState } from "react";
import { Modal } from "./Modal";
import { Button } from "./Button";
import { RECORD_TYPES } from "./dns";

// BulkTTLModal renders the multi-select record-type checkboxes + TTL
// input that drive the "Bulk TTL update" sweep. The modal is identical
// on the WHM and User Panel surfaces — only the API path differs (WHM
// hits /api/v1/whm/dns/bulk-ttl, cPanel hits /api/v1/cpanel/dns/bulk-ttl).
// Caller is responsible for the network call so this stays free of any
// axios / fetch wrapper assumptions.
//
// SOA is excluded from the picker on purpose — the backend rejects it
// (its TTL is the negative-cache duration, not regular content) and
// putting the checkbox here would be a footgun. NSEC* / DNSSEC are
// likewise omitted since DNSSEC manages those records itself.

// SELECTABLE_TYPES is the picker's enabled set. Mirrors the backend's
// bulkTTLAllowedTypes whitelist; adding a type here without adding it
// in dns_service.go's whitelist would bounce off server-side validation
// with "type X is not supported".
const SELECTABLE_TYPES = RECORD_TYPES.filter((t) => t !== "DMARC"); // DMARC is a TXT alias on the server side; pick TXT explicitly

// TTLPreset offers human-friendly common values so an operator doesn't
// have to remember "30 minutes is 1800". Custom input still wins.
const TTL_PRESETS: Array<{ label: string; seconds: number }> = [
  { label: "5 min", seconds: 300 },
  { label: "30 min", seconds: 1800 },
  { label: "1 hour", seconds: 3600 },
  { label: "4 hours", seconds: 14400 },
  { label: "1 day", seconds: 86400 },
];

export interface BulkTTLZoneResult {
  domain: string;
  updated_count: number;
  rrsets_affected: number;
  error?: string;
}

export interface BulkTTLResponse {
  total_records_updated: number;
  domains_affected: number;
  domains_considered: number;
  items: BulkTTLZoneResult[];
}

export interface BulkTTLModalProps {
  isOpen: boolean;
  onClose: () => void;
  // submit takes the form fields and is responsible for the network
  // call. Returning the response renders the per-zone result panel
  // before the modal is dismissed; throwing surfaces an error banner.
  submit: (types: string[], ttl: number) => Promise<BulkTTLResponse>;
  // scopeLabel describes who the sweep will affect — surfaced in the
  // confirmation banner so an operator can't mistake an all-tenants
  // run for a single-tenant one (or vice versa). Pass something like
  // "all vendors, all domains" on WHM and "your domains" on cPanel.
  scopeLabel: string;
}

export function BulkTTLModal({
  isOpen,
  onClose,
  submit,
  scopeLabel,
}: BulkTTLModalProps) {
  const [selected, setSelected] = useState<Set<string>>(new Set(["A", "AAAA"]));
  const [ttl, setTtl] = useState<number>(3600);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string>("");
  const [result, setResult] = useState<BulkTTLResponse | null>(null);

  const toggleType = (t: string) => {
    setSelected((s) => {
      const next = new Set(s);
      if (next.has(t)) next.delete(t);
      else next.add(t);
      return next;
    });
  };

  const reset = () => {
    setError("");
    setResult(null);
    setSelected(new Set(["A", "AAAA"]));
    setTtl(3600);
  };

  const close = () => {
    reset();
    onClose();
  };

  const onSubmit = async () => {
    setError("");
    if (selected.size === 0) {
      setError("Pick at least one record type.");
      return;
    }
    if (ttl < 30 || ttl > 604800) {
      setError("TTL must be between 30 seconds and 1 week (604800 seconds).");
      return;
    }
    setSubmitting(true);
    try {
      const r = await submit(Array.from(selected), ttl);
      setResult(r);
    } catch (e: any) {
      setError(
        e?.response?.data?.error?.message || e?.message || "Bulk TTL update failed"
      );
    } finally {
      setSubmitting(false);
    }
  };

  const inputCls =
    "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";

  return (
    <Modal isOpen={isOpen} onClose={close} title="Bulk TTL update" size="lg">
      {result ? (
        <div className="space-y-4">
          <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-4 py-3 text-sm text-emerald-300">
            <div className="font-medium">
              Updated {result.total_records_updated} record
              {result.total_records_updated === 1 ? "" : "s"} across{" "}
              {result.domains_affected} domain
              {result.domains_affected === 1 ? "" : "s"}.
            </div>
            <div className="text-xs mt-1 text-emerald-300/80">
              {result.domains_considered} zone
              {result.domains_considered === 1 ? "" : "s"} were searched.
            </div>
          </div>

          {result.items.length > 0 && (
            <div className="rounded-lg border border-panel-border max-h-64 overflow-y-auto">
              <table className="w-full text-xs">
                <thead className="sticky top-0 bg-panel-bg/80 backdrop-blur">
                  <tr className="text-left text-panel-muted">
                    <th className="px-3 py-2 font-medium">Domain</th>
                    <th className="px-3 py-2 font-medium text-right">Records</th>
                    <th className="px-3 py-2 font-medium text-right">RRSets</th>
                    <th className="px-3 py-2 font-medium">Status</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-panel-border/40">
                  {result.items.map((it) => (
                    <tr key={it.domain}>
                      <td className="px-3 py-1.5 font-mono text-panel-text">
                        {it.domain}
                      </td>
                      <td className="px-3 py-1.5 text-right text-panel-muted">
                        {it.updated_count}
                      </td>
                      <td className="px-3 py-1.5 text-right text-panel-muted">
                        {it.rrsets_affected}
                      </td>
                      <td className="px-3 py-1.5">
                        {it.error ? (
                          <span className="text-red-400" title={it.error}>
                            error
                          </span>
                        ) : it.updated_count > 0 ? (
                          <span className="text-emerald-400">ok</span>
                        ) : (
                          <span className="text-panel-muted/60">no match</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <div className="flex justify-end gap-2">
            <Button
              onClick={() => reset()}
              className="px-3 py-2 text-sm bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text"
            >
              Run again
            </Button>
            <Button
              onClick={close}
              className="px-3 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg"
            >
              Done
            </Button>
          </div>
        </div>
      ) : (
        <div className="space-y-5">
          <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">
            This will update the TTL on <strong>{scopeLabel}</strong> for the
            record types you select below. Operation is per-zone — a failure on
            one domain doesn't roll back others.
          </div>

          <div>
            <label className="block text-sm font-medium text-panel-text mb-2">
              Record types
            </label>
            <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 gap-2">
              {SELECTABLE_TYPES.map((t) => {
                const active = selected.has(t);
                return (
                  <button
                    key={t}
                    type="button"
                    onClick={() => toggleType(t)}
                    className={`px-3 py-1.5 text-xs font-mono border rounded-lg transition-colors ${
                      active
                        ? "bg-blue-600/20 border-blue-500/60 text-blue-200"
                        : "bg-panel-bg border-panel-border text-panel-muted hover:text-panel-text"
                    }`}
                  >
                    {t}
                  </button>
                );
              })}
            </div>
            <p className="text-[11px] text-panel-muted mt-1">
              SOA is omitted on purpose — its TTL is the zone's negative-cache
              duration and shouldn't be swept.
            </p>
          </div>

          <div>
            <label className="block text-sm font-medium text-panel-text mb-2">
              New TTL (seconds)
            </label>
            <div className="flex items-center gap-2">
              <input
                type="number"
                min={30}
                max={604800}
                value={ttl}
                onChange={(e) => setTtl(parseInt(e.target.value || "0", 10))}
                className={inputCls + " max-w-[180px]"}
              />
              <div className="flex flex-wrap gap-1">
                {TTL_PRESETS.map((p) => (
                  <button
                    key={p.seconds}
                    type="button"
                    onClick={() => setTtl(p.seconds)}
                    className={`px-2 py-1 text-[11px] border rounded transition-colors ${
                      ttl === p.seconds
                        ? "bg-blue-600/20 border-blue-500/60 text-blue-200"
                        : "bg-panel-bg border-panel-border text-panel-muted hover:text-panel-text"
                    }`}
                  >
                    {p.label}
                  </button>
                ))}
              </div>
            </div>
            <p className="text-[11px] text-panel-muted mt-1">
              30 sec – 1 week (604800).
            </p>
          </div>

          {error && (
            <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2 pt-2 border-t border-panel-border">
            <Button
              onClick={close}
              disabled={submitting}
              className="px-3 py-2 text-sm bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text disabled:opacity-50"
            >
              Cancel
            </Button>
            <Button
              onClick={onSubmit}
              disabled={submitting || selected.size === 0}
              className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50"
            >
              {submitting
                ? "Updating…"
                : `Update TTL on ${selected.size} type${
                    selected.size === 1 ? "" : "s"
                  }`}
            </Button>
          </div>
        </div>
      )}
    </Modal>
  );
}
