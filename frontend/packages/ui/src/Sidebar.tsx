import React from "react";

export interface SidebarItem {
  label: string;
  icon: React.ReactNode;
  path: string;
  badge?: string | number;
}

interface SidebarProps {
  items: SidebarItem[];
  currentPath: string;
  onNavigate: (path: string) => void;
  brand: string;
}

export function Sidebar({ items, currentPath, onNavigate, brand }: SidebarProps) {
  return (
    <aside className="w-64 bg-panel-bg border-r border-panel-border h-screen flex flex-col">
      <div className="px-6 py-5 border-b border-panel-border">
        <h1 className="text-xl font-bold text-white">{brand}</h1>
      </div>
      <nav className="flex-1 overflow-y-auto py-4 px-3">
        {items.map((item) => {
          const isActive = currentPath.startsWith(item.path);
          return (
            <button
              key={item.path}
              onClick={() => onNavigate(item.path)}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm mb-1 transition-colors ${
                isActive
                  ? "bg-brand-600/10 text-brand-400 font-medium"
                  : "text-panel-muted hover:text-panel-text hover:bg-panel-surface"
              }`}
            >
              {item.icon}
              <span className="flex-1 text-left">{item.label}</span>
              {item.badge !== undefined && (
                <span className="bg-brand-600 text-white text-xs px-2 py-0.5 rounded-full">{item.badge}</span>
              )}
            </button>
          );
        })}
      </nav>
    </aside>
  );
}
