import React from "react";
import { Bell, User, LogOut, Server, Tag } from "lucide-react";

interface TopBarProps {
  title: string;
  userName?: string;
  serverIP?: string;
  versionName?: string;
  versionNumber?: string;
  onLogout: () => void;
}

export function TopBar({ title, userName, serverIP, versionName, versionNumber, onLogout }: TopBarProps) {
  return (
    <header className="h-16 bg-panel-surface border-b border-panel-border flex items-center justify-between px-6">
      <div className="flex items-center gap-4">
        <h2 className="text-lg font-semibold text-panel-text">{title}</h2>
        {serverIP && (
          <div className="flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-panel-bg border border-panel-border text-xs">
            <Server size={12} className="text-green-400" />
            <span className="text-panel-muted">{serverIP}</span>
          </div>
        )}
        {(versionName || versionNumber) && (
          <div
            className="flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-panel-bg border border-panel-border text-xs"
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
      <div className="flex items-center gap-4">
        <button className="text-panel-muted hover:text-panel-text transition-colors">
          <Bell size={20} />
        </button>
        <div className="flex items-center gap-2 text-sm text-panel-muted">
          <User size={18} />
          <span>{userName}</span>
        </div>
        <button onClick={onLogout} className="text-panel-muted hover:text-red-400 transition-colors" title="Logout">
          <LogOut size={18} />
        </button>
      </div>
    </header>
  );
}
