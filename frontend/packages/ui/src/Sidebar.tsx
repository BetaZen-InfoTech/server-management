import React, { useMemo, useState } from "react";

export interface SidebarItem {
  label: string;
  icon: React.ReactNode;
  path: string;
  badge?: string | number;
  // Optional section header. Items sharing the same section render under
  // one collapsible group with the section name as a tiny uppercase label.
  // Items without a section appear ungrouped at the top of the nav.
  section?: string;
}

interface SidebarProps {
  items: SidebarItem[];
  currentPath: string;
  onNavigate: (path: string) => void;
  brand: string;
}

// Sidebar with a built-in search box and optional section grouping. The
// search filters items by label (case-insensitive substring) AND by
// section name, so typing "ssl" matches the SSL/TLS item directly while
// typing "files" matches every item whose section is "Files & Code". When
// search is active, sections collapse to a flat result list — no group
// headers — so the operator sees just the matches.
export function Sidebar({ items, currentPath, onNavigate, brand }: SidebarProps) {
  const [query, setQuery] = useState("");
  const trimmed = query.trim().toLowerCase();

  // Group items by section, preserving the original order. Items without
  // a section land in the special "" bucket which renders first with no
  // header. When the operator types in the search box we skip grouping
  // and just render a flat filtered list — matches what they expect.
  const grouped = useMemo(() => {
    const map = new Map<string, SidebarItem[]>();
    const orderedSections: string[] = [];
    for (const item of items) {
      const sec = item.section || "";
      if (!map.has(sec)) {
        map.set(sec, []);
        orderedSections.push(sec);
      }
      map.get(sec)!.push(item);
    }
    return orderedSections.map((sec) => ({ section: sec, items: map.get(sec)! }));
  }, [items]);

  const filteredFlat = useMemo(() => {
    if (!trimmed) return null;
    return items.filter(
      (it) =>
        it.label.toLowerCase().includes(trimmed) ||
        (it.section || "").toLowerCase().includes(trimmed)
    );
  }, [items, trimmed]);

  return (
    <aside className="w-64 bg-panel-bg border-r border-panel-border h-screen flex flex-col">
      <div className="px-5 py-4 border-b border-panel-border">
        <h1 className="text-base font-bold text-white leading-tight">{brand}</h1>
      </div>
      <div className="px-3 pt-3 pb-2 border-b border-panel-border">
        <div className="relative">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            className="absolute left-2.5 top-1/2 -translate-y-1/2 text-panel-muted/70"
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <circle cx="11" cy="11" r="8" />
            <path d="m21 21-4.3-4.3" />
          </svg>
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search menu…"
            aria-label="Search sidebar menu"
            className="w-full pl-8 pr-7 py-1.5 text-xs bg-panel-surface border border-panel-border rounded-md text-panel-text placeholder-panel-muted/60 focus:outline-none focus:ring-1 focus:ring-brand-500/40 focus:border-brand-500/40 transition-colors"
          />
          {query && (
            <button
              type="button"
              onClick={() => setQuery("")}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-panel-muted/60 hover:text-panel-text text-xs"
              title="Clear search"
              aria-label="Clear search"
            >
              ×
            </button>
          )}
        </div>
      </div>
      <nav className="flex-1 overflow-y-auto py-2 px-2">
        {filteredFlat ? (
          filteredFlat.length === 0 ? (
            <p className="px-3 py-4 text-xs text-panel-muted/70 text-center">No menu items match "{query}".</p>
          ) : (
            filteredFlat.map((item) => (
              <NavRow key={item.path} item={item} currentPath={currentPath} onNavigate={onNavigate} />
            ))
          )
        ) : (
          grouped.map((group) => (
            <div key={group.section || "_default"} className="mb-2">
              {group.section && (
                <h3 className="px-3 pt-2 pb-1 text-[10px] font-semibold uppercase tracking-wider text-panel-muted/60">
                  {group.section}
                </h3>
              )}
              {group.items.map((item) => (
                <NavRow key={item.path} item={item} currentPath={currentPath} onNavigate={onNavigate} />
              ))}
            </div>
          ))
        )}
      </nav>
    </aside>
  );
}

function NavRow({
  item,
  currentPath,
  onNavigate,
}: {
  item: SidebarItem;
  currentPath: string;
  onNavigate: (path: string) => void;
}) {
  const isActive = currentPath.startsWith(item.path);
  return (
    <button
      onClick={() => onNavigate(item.path)}
      className={`w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm mb-0.5 transition-colors ${
        isActive
          ? "bg-brand-600/10 text-brand-400 font-medium"
          : "text-panel-muted hover:text-panel-text hover:bg-panel-surface"
      }`}
    >
      {item.icon}
      <span className="flex-1 text-left truncate">{item.label}</span>
      {item.badge !== undefined && (
        <span className="bg-brand-600 text-white text-xs px-2 py-0.5 rounded-full">{item.badge}</span>
      )}
    </button>
  );
}
