import React from "react";
import { DEFAULT_PAGE_SIZES } from "./Table";

interface PaginationBarProps {
  page: number;
  limit: number;
  total: number;
  pageSizeOptions?: number[];
  onPageChange: (page: number) => void;
  onLimitChange?: (limit: number) => void;
}

// PaginationBar is the same control the Table renders below its rows,
// extracted so list pages with custom row layouts (Deploy Software's
// project cards, etc) can reuse it without going through Table.
export function PaginationBar({
  page,
  limit,
  total,
  pageSizeOptions = DEFAULT_PAGE_SIZES,
  onPageChange,
  onLimitChange,
}: PaginationBarProps) {
  const totalPages = Math.max(1, Math.ceil(total / Math.max(1, limit)));
  const cur = Math.min(Math.max(1, page), totalPages);
  const start = total > 0 ? (cur - 1) * limit + 1 : 0;
  const end = Math.min(total, cur * limit);
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 px-1 py-2 text-xs text-panel-muted">
      <div>
        {total > 0
          ? <>Showing <span className="text-panel-text">{start.toLocaleString()}–{end.toLocaleString()}</span> of <span className="text-panel-text">{total.toLocaleString()}</span></>
          : "No results"}
      </div>
      <div className="flex items-center gap-3">
        <div className="flex items-center gap-1">
          <button type="button" onClick={() => onPageChange(1)} disabled={cur <= 1}
            className="px-2 py-1 rounded border border-panel-border hover:bg-panel-bg disabled:opacity-40 disabled:cursor-not-allowed" title="First page">«</button>
          <button type="button" onClick={() => onPageChange(cur - 1)} disabled={cur <= 1}
            className="px-2 py-1 rounded border border-panel-border hover:bg-panel-bg disabled:opacity-40 disabled:cursor-not-allowed" title="Previous page">‹</button>
          <span className="px-2 text-panel-text">Page {cur} of {totalPages}</span>
          <button type="button" onClick={() => onPageChange(cur + 1)} disabled={cur >= totalPages}
            className="px-2 py-1 rounded border border-panel-border hover:bg-panel-bg disabled:opacity-40 disabled:cursor-not-allowed" title="Next page">›</button>
          <button type="button" onClick={() => onPageChange(totalPages)} disabled={cur >= totalPages}
            className="px-2 py-1 rounded border border-panel-border hover:bg-panel-bg disabled:opacity-40 disabled:cursor-not-allowed" title="Last page">»</button>
        </div>
        {onLimitChange && (
          <label className="flex items-center gap-1">
            <span>Per page</span>
            <select value={limit} onChange={(e) => onLimitChange(parseInt(e.target.value, 10))}
              className="px-2 py-1 bg-panel-bg border border-panel-border rounded text-panel-text">
              {pageSizeOptions.map((n) => (<option key={n} value={n}>{n}</option>))}
            </select>
          </label>
        )}
      </div>
    </div>
  );
}
