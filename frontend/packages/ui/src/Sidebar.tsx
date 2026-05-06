import React, { useEffect, useMemo, useState } from "react";
import { X } from "lucide-react";

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
  // Optional data: URL for the panel logo. Rendered next to the brand
  // text at the top of the sidebar. Falls back cleanly when absent —
  // operators who haven't uploaded a logo see brand-text only.
  logo?: string;
  // Mobile drawer state — when defined, the sidebar acts as an
  // off-canvas drawer below the `md` breakpoint (768px). On wider
  // screens it stays docked as before. Both layouts pass these props.
  mobileOpen?: boolean;
  onMobileClose?: () => void;
}

// Sidebar with a built-in search box and optional section grouping. The
// search filters items by label (case-insensitive substring) AND by
// section name, so typing "ssl" matches the SSL/TLS item directly while
// typing "files" matches every item whose section is "Files & Code". When
// search is active, sections collapse to a flat result list — no group
// headers — so the operator sees just the matches.
//
// Mobile (below md / 768px): the sidebar is hidden by default. When the
// host layout opens it via `mobileOpen=true`, it slides in from the left
// over a darkened backdrop. Tapping a nav item OR the backdrop closes
// the drawer via `onMobileClose`. Above md it's docked as a static
// 256-px column the way it always has been.
export function Sidebar({
  items,
  currentPath,
  onNavigate,
  brand,
  logo,
  mobileOpen = false,
  onMobileClose,
}: SidebarProps) {
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

  // Track viewport so we can flip aria-hidden / focus behaviour off
  // for the docked desktop sidebar. SSR-safe (initial state false on
  // the server, snaps to the right value on hydrate via the listener
  // below).
  const [isMobile, setIsMobile] = useState<boolean>(() => {
    if (typeof window === "undefined" || !window.matchMedia) return false;
    return window.matchMedia("(max-width: 767px)").matches;
  });
  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const mq = window.matchMedia("(max-width: 767px)");
    const sync = () => setIsMobile(mq.matches);
    sync();
    if (mq.addEventListener) {
      mq.addEventListener("change", sync);
      return () => mq.removeEventListener("change", sync);
    }
    mq.addListener(sync);
    return () => mq.removeListener(sync);
  }, []);

  // Lock body scroll while the mobile drawer is open so the underlying
  // page doesn't scroll behind the overlay.
  useEffect(() => {
    if (!mobileOpen) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [mobileOpen]);

  // ESC closes the drawer — same affordance Modal has, so users who
  // discover ESC anywhere in the panel get consistent behaviour.
  useEffect(() => {
    if (!mobileOpen || !onMobileClose) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onMobileClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [mobileOpen, onMobileClose]);

  // When the viewport crosses up past the md breakpoint (768px) the
  // sidebar visually docks regardless of mobileOpen, so we must also
  // flip the state to false — otherwise the body-scroll-lock useEffect
  // above stays active and the now-docked desktop layout becomes
  // un-scrollable. Reproduces by opening the drawer on a phone-width
  // window then resizing to desktop. matchMedia fires once on the
  // exact transition; we don't poll.
  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia || !onMobileClose) return;
    const mq = window.matchMedia("(min-width: 768px)");
    const handler = (e: MediaQueryListEvent) => {
      if (e.matches && mobileOpen) onMobileClose();
    };
    // addEventListener is the modern API; addListener is the legacy
    // one Safari < 14 still needs. Cover both for the same reason
    // the rest of the panel does.
    if (mq.addEventListener) {
      mq.addEventListener("change", handler);
      return () => mq.removeEventListener("change", handler);
    }
    mq.addListener(handler);
    return () => mq.removeListener(handler);
  }, [mobileOpen, onMobileClose]);

  // Wraps onNavigate so a tap on a mobile nav row also closes the drawer.
  const handleNavigate = (path: string) => {
    onNavigate(path);
    if (onMobileClose) onMobileClose();
  };

  return (
    <>
      {/* Backdrop — only rendered + visible when the mobile drawer is open.
          Above md the sidebar is docked so this never paints. */}
      {mobileOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/60 md:hidden"
          onClick={onMobileClose}
          aria-hidden="true"
        />
      )}
      <aside
        className={`bg-panel-bg border-r border-panel-border h-screen flex flex-col w-64 shrink-0
          fixed md:static inset-y-0 left-0 z-50 transition-transform duration-200 ease-out
          ${mobileOpen ? "translate-x-0" : "-translate-x-full"} md:translate-x-0`}
        aria-label="Primary navigation"
        // a11y: hide the off-screen drawer from screen readers + the tab
        // order ONLY when we're actually below md AND the drawer is
        // closed. Above md the sidebar is docked + always visible, so
        // it must remain reachable. isMobile is synced via matchMedia
        // so the attribute flips immediately when the viewport crosses
        // 768 px in either direction.
        aria-hidden={isMobile && !mobileOpen ? "true" : undefined}
      >
        <div className="px-5 py-4 border-b border-panel-border flex items-center gap-2.5">
          {logo ? (
            <img src={logo} alt="" className="w-8 h-8 rounded object-contain shrink-0" />
          ) : null}
          <h1 className="text-base font-bold text-white leading-tight truncate flex-1">{brand}</h1>
          {/* Close button — only visible on mobile so users can dismiss the
              drawer without tapping a nav item or the backdrop. */}
          {onMobileClose && (
            <button
              type="button"
              onClick={onMobileClose}
              className="md:hidden p-1.5 rounded text-panel-muted hover:text-panel-text hover:bg-panel-surface"
              aria-label="Close navigation"
            >
              <X size={18} />
            </button>
          )}
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
                <NavRow key={item.path} item={item} currentPath={currentPath} onNavigate={handleNavigate} />
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
                  <NavRow key={item.path} item={item} currentPath={currentPath} onNavigate={handleNavigate} />
                ))}
              </div>
            ))
          )}
        </nav>
      </aside>
    </>
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
