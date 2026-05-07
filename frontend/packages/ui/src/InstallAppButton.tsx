import React from "react";
import { Download } from "lucide-react";
import { useInstallPrompt } from "./onlineStatus";

// InstallAppButton renders an "Install app" button when the browser
// has fired beforeinstallprompt and the panel isn't already running
// as an installed PWA. Hidden entirely otherwise — operators on iOS
// (which doesn't fire beforeinstallprompt) or already-installed
// users see nothing.
//
// Mount in the TopBar / Footer / Settings → About pane — anywhere
// that benefits from the optional affordance without cluttering the
// main UI when the install path isn't available.
export function InstallAppButton({
  className = "",
  label = "Install app",
}: {
  className?: string;
  label?: string;
}) {
  const { canInstall, promptInstall } = useInstallPrompt();
  if (!canInstall) return null;
  return (
    <button
      type="button"
      onClick={() => {
        // Fire-and-forget; outcome is reflected in canInstall on next render.
        void promptInstall();
      }}
      className={
        "inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs " +
        "bg-brand-600/15 text-brand-400 border border-brand-500/30 " +
        "hover:bg-brand-600/25 transition-colors " +
        className
      }
      title="Install Betazen Server Panel as a desktop / home-screen app"
    >
      <Download size={12} aria-hidden="true" />
      <span className="hidden sm:inline">{label}</span>
    </button>
  );
}
