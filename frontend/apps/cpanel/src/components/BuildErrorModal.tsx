import { useState } from "react";
import { Modal, copyToClipboard } from "@serverpanel/ui";
import { AlertCircle, Check, Copy } from "lucide-react";

// BuildErrorInfo is the structured payload the backend returns (HTTP 422
// with error.code = "BUILD_FAILED") when a deploy fails at a user-code
// step (install / build / start). Both the /apps Deploy endpoint and the
// Deploy Software /projects/provision endpoint return the same shape so
// one modal handles both surfaces.
export interface BuildErrorInfo {
  service: string;
  stage: string; // "install" | "build" | "start"
  summary: string;
  output: string;
}

// tryExtractBuildError picks a BuildErrorInfo out of an axios-style error
// response if the backend returned the BUILD_FAILED shape. Returns null
// otherwise so the caller can fall back to its existing toast behavior.
export function tryExtractBuildError(err: unknown): BuildErrorInfo | null {
  const e = err as { response?: { data?: { error?: { code?: string; message?: string; details?: { service?: string; stage?: string; summary?: string; output?: string } } } } };
  const body = e?.response?.data?.error;
  if (body?.code !== "BUILD_FAILED" || !body?.details?.output) return null;
  return {
    service: body.details.service || "",
    stage: body.details.stage || "build",
    summary: body.details.summary || body.message || "",
    output: body.details.output,
  };
}

export function BuildErrorModal({
  info,
  onClose,
}: {
  info: BuildErrorInfo;
  onClose: () => void;
}) {
  const stageTitle =
    info.stage === "install" ? "Install"
    : info.stage === "build" ? "Build"
    : info.stage === "start" ? "Start"
    : info.stage.charAt(0).toUpperCase() + info.stage.slice(1);
  const [copied, setCopied] = useState(false);

  async function copyOutput() {
    if (await copyToClipboard(info.output)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  }

  return (
    <Modal isOpen onClose={onClose} title={`${stageTitle} failed`} size="xl">
      <div className="space-y-3">
        <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-sm">
          <div className="flex items-start gap-2">
            <AlertCircle size={15} className="text-red-400 mt-0.5 shrink-0" />
            <div className="flex-1 space-y-1">
              <div className="text-red-400 font-medium">
                {info.service ? (<>Service "{info.service}" — {info.stage} step failed</>) : (<>{stageTitle} step failed</>)}
              </div>
              {info.summary && (
                <div className="text-panel-text font-mono text-xs break-all">{info.summary}</div>
              )}
              <div className="text-panel-muted text-[11px]">
                Fix the error in your repo or in your commands above, then retry.
              </div>
            </div>
          </div>
        </div>
        <div>
          <div className="flex items-center justify-between mb-1">
            <span className="text-xs text-panel-muted">Full output</span>
            <button
              onClick={copyOutput}
              className="inline-flex items-center gap-1 text-[11px] text-panel-muted hover:text-panel-text"
            >
              {copied ? <Check size={11} className="text-green-400" /> : <Copy size={11} />}
              {copied ? "Copied" : "Copy"}
            </button>
          </div>
          <pre className="max-h-[50vh] overflow-auto p-3 bg-panel-bg border border-panel-border rounded-lg text-[11px] text-panel-text whitespace-pre-wrap font-mono">
            {info.output}
          </pre>
        </div>
        <div className="flex justify-end">
          <button onClick={onClose} className="px-4 py-2 text-sm bg-panel-surface border border-panel-border rounded-lg text-panel-text">
            Close
          </button>
        </div>
      </div>
    </Modal>
  );
}
