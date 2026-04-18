import React, { useEffect, useState } from "react";
import type { LucideIcon } from "lucide-react";
import { AlertTriangle, AlertCircle, HelpCircle, Loader2 } from "lucide-react";

// ConfirmDialog provides a styled replacement for window.confirm() that
// matches the panel's dark theme. Use the imperative confirmAction()
// API from anywhere in the app — it returns a Promise<boolean>.
//
// Usage:
//   import { confirmAction } from "@serverpanel/ui";
//   if (!(await confirmAction({
//     title: "Delete app",
//     description: `Remove "${name}" and its files?`,
//     danger: true,
//     confirmLabel: "Delete",
//   }))) return;
//
// The host component <ConfirmHost /> must be mounted ONCE at the app
// root (App.tsx). It owns the state for the queue of open prompts.

export type ConfirmTone = "danger" | "warning" | "info";

export interface ConfirmOptions {
  title: string;
  // Body of the prompt. Plain string — supports \n for line breaks. Use
  // a custom JSX body via the `body` prop when richer formatting is
  // needed.
  description?: string;
  body?: React.ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  // danger=true is shorthand for tone="danger".
  danger?: boolean;
  tone?: ConfirmTone;
  // When set, the user must type this exact string into the confirm
  // input before the confirm button is enabled. Used for high-risk
  // destructive actions (delete tenant, drop all data, etc.).
  confirmText?: string;
}

interface QueueItem extends ConfirmOptions {
  id: number;
  resolve: (ok: boolean) => void;
}

// Module-level subscriber set so the host can re-render when an action
// is queued from anywhere. Keeps the dialog truly singleton without
// needing React Context plumbing.
let nextId = 1;
const subscribers = new Set<(items: QueueItem[]) => void>();
let queue: QueueItem[] = [];

function notify() {
  subscribers.forEach((cb) => cb([...queue]));
}

export function confirmAction(opts: ConfirmOptions): Promise<boolean> {
  return new Promise<boolean>((resolve) => {
    queue = [...queue, { ...opts, id: nextId++, resolve }];
    notify();
  });
}

function dismiss(id: number, ok: boolean) {
  const item = queue.find((q) => q.id === id);
  queue = queue.filter((q) => q.id !== id);
  notify();
  item?.resolve(ok);
}

const toneStyles: Record<ConfirmTone, {
  ring: string; bg: string; iconColor: string; btn: string; Icon: LucideIcon;
}> = {
  danger: {
    ring: "ring-red-500/30",
    bg: "bg-red-500/10",
    iconColor: "text-red-400",
    btn: "bg-red-600 hover:bg-red-700 text-white",
    Icon: AlertTriangle,
  },
  warning: {
    ring: "ring-yellow-500/30",
    bg: "bg-yellow-500/10",
    iconColor: "text-yellow-400",
    btn: "bg-yellow-600 hover:bg-yellow-700 text-white",
    Icon: AlertCircle,
  },
  info: {
    ring: "ring-blue-500/30",
    bg: "bg-blue-500/10",
    iconColor: "text-blue-400",
    btn: "bg-blue-600 hover:bg-blue-700 text-white",
    Icon: HelpCircle,
  },
};

function ConfirmModal({ item }: { item: QueueItem }) {
  const tone: ConfirmTone = item.tone ?? (item.danger ? "danger" : "info");
  const style = toneStyles[tone];
  const { Icon } = style;
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const needsType = !!item.confirmText;
  const canConfirm = !needsType || typed === item.confirmText;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") dismiss(item.id, false);
      if (e.key === "Enter" && canConfirm && !busy) {
        setBusy(true);
        dismiss(item.id, true);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [canConfirm, busy, item.id]);

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center" role="dialog" aria-modal="true">
      <div
        className="fixed inset-0 bg-black/60 backdrop-blur-sm animate-fade-in"
        onClick={() => !busy && dismiss(item.id, false)}
      />
      <div className={`relative bg-panel-surface border border-panel-border rounded-xl shadow-2xl w-full max-w-md mx-4 ring-1 ${style.ring} animate-scale-in`}>
        <div className="px-6 pt-6 pb-4">
          <div className="flex items-start gap-4">
            <div className={`shrink-0 w-12 h-12 rounded-full ${style.bg} flex items-center justify-center`}>
              <Icon size={22} className={style.iconColor} />
            </div>
            <div className="min-w-0 flex-1">
              <h3 className="text-base font-semibold text-panel-text leading-tight">{item.title}</h3>
              {item.description && (
                <div className="mt-1.5 text-sm text-panel-muted whitespace-pre-line leading-relaxed">
                  {item.description}
                </div>
              )}
              {item.body && <div className="mt-2 text-sm">{item.body}</div>}
              {needsType && (
                <div className="mt-3">
                  <label className="block text-xs text-panel-muted mb-1">
                    Type <code className="text-panel-text bg-panel-bg px-1.5 py-0.5 rounded">{item.confirmText}</code> to confirm:
                  </label>
                  <input
                    type="text"
                    autoFocus
                    value={typed}
                    onChange={(e) => setTyped(e.target.value)}
                    className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500"
                  />
                </div>
              )}
            </div>
          </div>
        </div>
        <div className="flex items-center justify-end gap-2 px-6 py-3 border-t border-panel-border bg-panel-bg/40 rounded-b-xl">
          <button
            type="button"
            disabled={busy}
            onClick={() => dismiss(item.id, false)}
            className="px-4 py-2 text-sm border border-panel-border rounded-lg text-panel-muted hover:text-panel-text hover:bg-panel-bg transition-colors disabled:opacity-50"
          >
            {item.cancelLabel ?? "Cancel"}
          </button>
          <button
            type="button"
            disabled={!canConfirm || busy}
            onClick={() => { setBusy(true); dismiss(item.id, true); }}
            className={`px-4 py-2 text-sm rounded-lg font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2 ${style.btn}`}
          >
            {busy && <Loader2 size={14} className="animate-spin" />}
            {item.confirmLabel ?? "Confirm"}
          </button>
        </div>
      </div>
      <style>{`
        @keyframes sp-fade-in { from { opacity: 0; } to { opacity: 1; } }
        @keyframes sp-scale-in { from { opacity: 0; transform: scale(.96); } to { opacity: 1; transform: scale(1); } }
        .animate-fade-in { animation: sp-fade-in 120ms ease-out; }
        .animate-scale-in { animation: sp-scale-in 140ms cubic-bezier(.2,.8,.2,1); }
      `}</style>
    </div>
  );
}

export function ConfirmHost() {
  const [items, setItems] = useState<QueueItem[]>([]);
  useEffect(() => {
    subscribers.add(setItems);
    setItems([...queue]);
    return () => { subscribers.delete(setItems); };
  }, []);
  // Render the LAST item only — confirm prompts are inherently
  // sequential: the user must answer the current one before the next
  // shows. The earlier items remain in the queue, awaiting their turn.
  const top = items[items.length - 1];
  if (!top) return null;
  return <ConfirmModal item={top} />;
}
