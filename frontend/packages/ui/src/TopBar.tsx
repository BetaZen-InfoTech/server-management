import React from "react";
import { Bell, User, LogOut, Server, Tag, Menu } from "lucide-react";

interface TopBarProps {
  title: string;
  userName?: string;
  serverIP?: string;
  versionName?: string;
  versionNumber?: string;
  onLogout: () => void;
  // When set, the user chip becomes a button that calls this handler —
  // used by both panels to navigate to the self-service Profile page.
  onProfileClick?: () => void;
  // Mobile hamburger toggle. When set, a Menu icon renders to the left
  // of the page title and is only visible below the `md` breakpoint
  // (where the sidebar collapses to an off-canvas drawer). Layouts
  // wire this to flip the same `mobileNavOpen` state they pass to
  // the Sidebar.
  onMenuClick?: () => void;
}

export function TopBar({ title, userName, serverIP, versionName, versionNumber, onLogout, onProfileClick, onMenuClick }: TopBarProps) {
  return (
    // h-auto + py-3 instead of fixed h-16 so badges + user chip can wrap
    // to a second line on narrow phones without the row clipping.
    // gap-2 instead of gap-4 so a wrap-onto-two-lines layout doesn't
    // burn 16 px of vertical space between badges.
    <header className="bg-panel-surface border-b border-panel-border flex items-center justify-between gap-3 px-4 sm:px-6 py-3 flex-wrap">
      <div className="flex items-center gap-3 min-w-0 flex-1">
        {onMenuClick && (
          <button
            type="button"
            onClick={onMenuClick}
            className="md:hidden p-1.5 -ml-1 rounded text-panel-muted hover:text-panel-text hover:bg-panel-bg"
            aria-label="Open navigation menu"
          >
            <Menu size={22} />
          </button>
        )}
        <h2 className="text-base sm:text-lg font-semibold text-panel-text truncate">{title}</h2>
        {/* Server-IP + version badges hide below sm so the title + user
            chip can both fit on a 360-px-wide phone without truncation
            piling up. They reappear on tablet (sm: 640px+). */}
        {serverIP && (
          <div className="hidden sm:flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-panel-bg border border-panel-border text-xs">
            <Server size={12} className="text-green-400" />
            <span className="text-panel-muted">{serverIP}</span>
          </div>
        )}
        {(versionName || versionNumber) && (
          <div
            className="hidden sm:flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-panel-bg border border-panel-border text-xs"
            title={`${versionName ?? ""} ${versionNumber ?? ""}`.trim()}
          >
            <Tag size={12} className="text-blue-400" />
            {versionName && <span className="text-panel-text font-medium">{versionName}</span>}
            {versionNumber && (
              <span className="text-panel-muted font-mono">v{versionNumber}</span>
            )}
          </div>
        )}
      </div>
      <div className="flex items-center gap-2 sm:gap-4">
        <button className="p-1.5 rounded text-panel-muted hover:text-panel-text hover:bg-panel-bg transition-colors" aria-label="Notifications">
          <Bell size={20} />
        </button>
        {onProfileClick ? (
          <button
            type="button"
            onClick={onProfileClick}
            className="flex items-center gap-2 text-sm text-panel-muted hover:text-panel-text transition-colors p-1 rounded hover:bg-panel-bg"
            title="Manage your profile and password"
          >
            <User size={18} />
            <span className="hidden sm:inline truncate max-w-[10rem]">{userName}</span>
          </button>
        ) : (
          <div className="flex items-center gap-2 text-sm text-panel-muted">
            <User size={18} />
            <span className="hidden sm:inline truncate max-w-[10rem]">{userName}</span>
          </div>
        )}
        <button onClick={onLogout} className="p-1.5 rounded text-panel-muted hover:text-red-400 hover:bg-panel-bg transition-colors" title="Logout" aria-label="Logout">
          <LogOut size={18} />
        </button>
      </div>
    </header>
  );
}
