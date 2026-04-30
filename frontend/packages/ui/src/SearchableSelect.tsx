import React, { useEffect, useMemo, useRef, useState } from "react";
import { Check, ChevronDown, Search, X } from "lucide-react";

// SearchableSelect is the type-ahead replacement for plain <select>
// dropdowns that pick a single value from a long, dynamic list — domains,
// vendors, mailboxes, packages. The visual shape matches the existing
// inputClass/selectClass patterns in the panel pages so dropping it in
// next to a regular <input> looks native.
//
// Behavior:
//   - Click the trigger or hit Enter/Space when focused → opens the panel
//   - Type into the search box → list filters case-insensitively against
//     option.label + option.hint (so "demo@" matches the email and
//     "demo-vendor" matches the vendor's username separately)
//   - Arrow keys cycle the highlighted row, Enter selects it, Escape closes
//   - Click outside, Escape, or selection → closes the panel
//   - Disabled / required pass through to the underlying form semantics
//
// Each option carries a `value` (what the form receives) and `label`
// (what the user reads). An optional `hint` renders smaller next to
// the label — used for "username — display name" or "domain — owner"
// shapes where two pieces of info help disambiguate.

export interface SearchableOption {
  value: string;
  label: string;
  hint?: string;
}

interface SearchableSelectProps {
  value: string;
  onChange: (value: string) => void;
  options: SearchableOption[];
  placeholder?: string;
  // Empty-state message when the filter matches nothing. Defaults to a
  // generic "No results"; pages can pass something specific like
  // "No domains match — clear the filter to pick from the full list."
  emptyMessage?: string;
  disabled?: boolean;
  required?: boolean;
  // Tailwind classes for the trigger element. Defaults match the rest
  // of the panel's inputs so a SearchableSelect drops in next to a
  // regular <input> without restyling.
  className?: string;
  // Class for the popup panel — rarely overridden, but exposed so the
  // panel can sit on top of a Modal's backdrop and not be z-clipped.
  panelClassName?: string;
  // Optional `id` so a parent <label htmlFor> still binds to it.
  id?: string;
  // Optional ARIA label for accessibility when the surrounding markup
  // doesn't already pair this with a <label>.
  "aria-label"?: string;
}

const defaultTriggerClass =
  "w-full flex items-center gap-2 px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm cursor-pointer hover:border-panel-border/70 disabled:cursor-not-allowed disabled:opacity-50";

const defaultPanelClass =
  "absolute z-50 mt-1 w-full bg-panel-surface border border-panel-border rounded-lg shadow-lg overflow-hidden";

export function SearchableSelect({
  value,
  onChange,
  options,
  placeholder = "Select…",
  emptyMessage = "No matches",
  disabled = false,
  required = false,
  className,
  panelClassName,
  id,
  "aria-label": ariaLabel,
}: SearchableSelectProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [highlight, setHighlight] = useState(0);
  const wrapperRef = useRef<HTMLDivElement | null>(null);
  const searchRef = useRef<HTMLInputElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);

  // Selected option lookup — drives the trigger's display label and the
  // initial highlight when the panel opens. Falls back to showing the raw
  // value so a stale option (e.g., domain renamed since selection) doesn't
  // make the field appear empty.
  const selected = useMemo(
    () => options.find((o) => o.value === value),
    [options, value]
  );

  // Filtered list — case-insensitive match across label, value and hint
  // so a vendor selector matching "demo" finds both
  // "demo@betazeninfotech.com" (label) and "demo-vendor" (hint).
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return options;
    return options.filter((o) => {
      const haystack = `${o.label} ${o.value} ${o.hint ?? ""}`.toLowerCase();
      return haystack.includes(q);
    });
  }, [options, query]);

  // Keep the highlight in range whenever the filter changes. Without
  // this, a previous-highlight=5 + new filter that returns 2 rows would
  // leave the highlight pointing past the end of the list and Enter
  // would select nothing.
  useEffect(() => {
    if (highlight >= filtered.length) {
      setHighlight(0);
    }
  }, [filtered.length, highlight]);

  // Click outside closes the panel. Bound only while open so we don't
  // pay for a global listener on every page that contains a closed
  // SearchableSelect.
  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, [open]);

  // Focus the search input the moment the panel opens so the user can
  // start typing immediately. Also reset the filter to a clean slate so
  // re-opening doesn't show the residue of last time's search.
  useEffect(() => {
    if (open) {
      setQuery("");
      const idx = options.findIndex((o) => o.value === value);
      setHighlight(idx >= 0 ? idx : 0);
      // Run on next tick so the DOM has rendered the input.
      setTimeout(() => searchRef.current?.focus(), 0);
    }
  }, [open, options, value]);

  // Scroll the highlighted row into view as the user arrows through the
  // list — without this, arrow-down past the visible bottom silently
  // moves selection but the panel doesn't follow.
  useEffect(() => {
    if (!open || !listRef.current) return;
    const el = listRef.current.querySelector<HTMLElement>(
      `[data-idx="${highlight}"]`
    );
    if (el) el.scrollIntoView({ block: "nearest" });
  }, [highlight, open]);

  const commit = (v: string) => {
    onChange(v);
    setOpen(false);
  };

  const onTriggerKey = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (disabled) return;
    if (e.key === "Enter" || e.key === " " || e.key === "ArrowDown") {
      e.preventDefault();
      setOpen(true);
    }
  };

  const onSearchKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setHighlight((h) => Math.min(h + 1, Math.max(filtered.length - 1, 0)));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setHighlight((h) => Math.max(h - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      const opt = filtered[highlight];
      if (opt) commit(opt.value);
    } else if (e.key === "Escape") {
      e.preventDefault();
      setOpen(false);
    } else if (e.key === "Tab") {
      // Tab out closes the panel without forcing a selection so the
      // form's normal tab order stays intact.
      setOpen(false);
    }
  };

  return (
    <div ref={wrapperRef} className="relative">
      <div
        id={id}
        role="combobox"
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-disabled={disabled}
        aria-label={ariaLabel}
        tabIndex={disabled ? -1 : 0}
        className={className ?? defaultTriggerClass}
        onClick={() => !disabled && setOpen((v) => !v)}
        onKeyDown={onTriggerKey}
      >
        <span
          className={`flex-1 truncate ${
            selected ? "text-panel-text" : "text-panel-muted/60"
          }`}
        >
          {selected ? selected.label : placeholder}
        </span>
        {selected && !required && !disabled && (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              commit("");
            }}
            className="text-panel-muted hover:text-panel-text"
            aria-label="Clear selection"
          >
            <X size={14} />
          </button>
        )}
        <ChevronDown
          size={14}
          className={`text-panel-muted transition-transform ${
            open ? "rotate-180" : ""
          }`}
        />
      </div>

      {open && (
        <div className={panelClassName ?? defaultPanelClass}>
          <div className="flex items-center gap-2 px-3 py-2 border-b border-panel-border">
            <Search size={14} className="text-panel-muted shrink-0" />
            <input
              ref={searchRef}
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={onSearchKey}
              placeholder="Type to filter…"
              className="w-full bg-transparent text-panel-text placeholder-panel-muted/60 text-sm focus:outline-none"
            />
            <span className="text-[11px] text-panel-muted shrink-0 tabular-nums">
              {filtered.length}/{options.length}
            </span>
          </div>

          <div ref={listRef} className="max-h-64 overflow-y-auto py-1">
            {filtered.length === 0 ? (
              <div className="px-3 py-3 text-xs text-panel-muted text-center">
                {emptyMessage}
              </div>
            ) : (
              filtered.map((opt, i) => {
                const isSelected = opt.value === value;
                const isHighlighted = i === highlight;
                return (
                  <div
                    key={opt.value}
                    data-idx={i}
                    role="option"
                    aria-selected={isSelected}
                    onMouseEnter={() => setHighlight(i)}
                    onClick={() => commit(opt.value)}
                    className={`flex items-center gap-2 px-3 py-2 text-sm cursor-pointer transition-colors ${
                      isHighlighted
                        ? "bg-blue-500/15 text-panel-text"
                        : "text-panel-text hover:bg-panel-bg/60"
                    }`}
                  >
                    <Check
                      size={14}
                      className={`shrink-0 ${
                        isSelected ? "text-blue-400" : "opacity-0"
                      }`}
                    />
                    <span className="flex-1 truncate">{opt.label}</span>
                    {opt.hint && (
                      <span className="text-[11px] text-panel-muted truncate max-w-[40%]">
                        {opt.hint}
                      </span>
                    )}
                  </div>
                );
              })
            )}
          </div>
        </div>
      )}

      {/* Hidden native input keeps `required` validation working with the
          surrounding <form>. Reflects the current value so a submit
          without a selection still surfaces the browser's "please pick
          one" tooltip. */}
      {required && (
        <input
          tabIndex={-1}
          aria-hidden="true"
          required
          value={value}
          onChange={() => {}}
          className="sr-only"
        />
      )}
    </div>
  );
}
