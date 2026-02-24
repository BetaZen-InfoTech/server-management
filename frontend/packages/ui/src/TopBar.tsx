import React from "react";
import { Bell, User, LogOut } from "lucide-react";

interface TopBarProps {
  title: string;
  userName?: string;
  onLogout: () => void;
}

export function TopBar({ title, userName, onLogout }: TopBarProps) {
  return (
    <header className="h-16 bg-panel-surface border-b border-panel-border flex items-center justify-between px-6">
      <h2 className="text-lg font-semibold text-panel-text">{title}</h2>
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
