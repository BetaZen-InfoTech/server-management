import React from "react";
import { WifiOff, RefreshCw } from "lucide-react";
import { useOnlineStatus } from "./onlineStatus";

interface OfflineOverlayProps {
  // Optional override — useful in Storybook / tests where you want to
  // force the overlay without flipping navigator.onLine. Defaults to
  // the live useOnlineStatus value.
  forceOffline?: boolean;
  // Optional title string so a host that wants different wording per
  // surface (login vs dashboard) can override. Default works for both.
  title?: string;
  // Optional descriptive paragraph below the title. Default explains
  // that the panel will resume automatically when the connection is
  // back, so the operator knows they don't have to manually retry.
  message?: string;
  // Optional secondary action — a "Retry" button that triggers the
  // host's reconnect logic (e.g. force-refetch /version). Defaults to
  // window.location.reload() when not supplied, which works for the
  // common case (an SPA that needs to re-bootstrap auth + branding).
  onRetry?: () => void;
}

// OfflineOverlay paints a full-screen modal-style block when the
// browser is offline. Sits at z-[60] (above the mobile drawer's z-50
// + Modal's z-50) so nothing in the panel can be interacted with
// while the network is gone — preventing the operator from filling
// in a long form, hitting Save, and getting a generic axios error
// after an unexplained spinner.
//
// Mount once near the top of each app's render tree (DashboardLayout
// + LoginPage) so every screen is gated. Cheap when online — returns
// null and renders nothing.
export function OfflineOverlay({
  forceOffline,
  title = "You are offline",
  message,
  onRetry,
}: OfflineOverlayProps) {
  const live = useOnlineStatus();
  const offline = forceOffline ?? !live;
  if (!offline) return null;

  const finalMessage =
    message ??
    "The panel can't reach the network right now. Any in-flight actions are paused; everything will resume automatically as soon as your connection is back.";

  const handleRetry = () => {
    if (onRetry) {
      onRetry();
      return;
    }
    // Reload re-bootstraps the SPA + re-runs auth refresh + re-fetches
    // branding/version. Cheaper than threading a full app-level retry
    // for the rare offline-then-online-then-want-to-act-now flow.
    window.location.reload();
  };

  return (
    <div
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="offline-overlay-title"
      aria-describedby="offline-overlay-desc"
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/70 backdrop-blur-sm px-4"
    >
      <div className="bg-panel-surface border border-panel-border rounded-2xl shadow-2xl max-w-md w-full p-6 sm:p-8">
        <div className="flex items-center gap-3 mb-4">
          <div className="p-2.5 rounded-full bg-red-500/15 text-red-400 shrink-0">
            <WifiOff size={28} aria-hidden="true" />
          </div>
          <h2
            id="offline-overlay-title"
            className="text-xl font-semibold text-white"
          >
            {title}
          </h2>
        </div>
        <p
          id="offline-overlay-desc"
          className="text-sm text-panel-muted leading-relaxed mb-6"
        >
          {finalMessage}
        </p>
        <div className="flex items-center justify-between gap-3">
          <span className="inline-flex items-center gap-1.5 text-xs text-red-400">
            <span className="w-2 h-2 rounded-full bg-red-500 animate-pulse" />
            No internet
          </span>
          <button
            type="button"
            onClick={handleRetry}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-brand-600 hover:bg-brand-500 text-white text-sm font-medium transition-colors"
          >
            <RefreshCw size={14} aria-hidden="true" />
            Retry
          </button>
        </div>
      </div>
    </div>
  );
}

// OnlineStatusBadge renders a compact indicator suitable for the
// TopBar — a green dot when connected, red pulsing dot + "Offline"
// label when not. Hides itself when online by default; pass
// `alwaysVisible` to keep the green state on screen.
export function OnlineStatusBadge({
  alwaysVisible = false,
  className = "",
}: {
  alwaysVisible?: boolean;
  className?: string;
}) {
  const online = useOnlineStatus();
  if (online && !alwaysVisible) return null;
  return (
    <span
      className={
        "inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md text-xs " +
        (online
          ? "bg-green-500/10 text-green-400 border border-green-500/30"
          : "bg-red-500/15 text-red-400 border border-red-500/40 animate-pulse") +
        " " +
        className
      }
      aria-live="polite"
      title={online ? "Connected" : "Offline — actions are paused"}
    >
      <span
        className={
          "w-1.5 h-1.5 rounded-full " + (online ? "bg-green-400" : "bg-red-500")
        }
      />
      {online ? "Online" : "Offline"}
    </span>
  );
}
