import React from "react";

interface Column<T> {
  key?: string;
  // header accepts a ReactNode so callers can render an interactive
  // element in the column heading (e.g. a "Select All" checkbox).
  // Most callers still pass a plain string — that path is unchanged.
  header: React.ReactNode;
  render?: (item: T) => React.ReactNode;
  accessor?: (item: T) => React.ReactNode;
  // sortKey identifies a column as sortable. When set, the header
  // becomes a button and clicking it calls the parent's onSort handler
  // with this key. Repeated clicks on the same column flip direction;
  // the parent owns the actual sorted order.
  sortKey?: string;
  // headerClass adds extra utility classes to the <th> (alignment etc.)
  headerClass?: string;
}

interface TableProps<T> {
  columns: Column<T>[];
  data: T[];
  loading?: boolean;
  emptyMessage?: string;
  // onRowClick makes every row clickable. Use it to open a detail
  // modal / navigate to a sub-route. The cursor flips to pointer when
  // set, and the row gets a stronger hover highlight.
  onRowClick?: (item: T) => void;
  // Sort state lives in the parent. The Table just renders the
  // direction arrow next to the active column.
  sortKey?: string;
  sortDir?: "asc" | "desc";
  onSort?: (key: string) => void;
  // Optional pagination footer. When `total` is provided the Table
  // renders a `Showing X-Y of Z · page chooser · per-page selector`
  // bar under the rows. Pagination is fully controlled — the parent
  // owns page/limit state and re-fetches in onPageChange/onLimitChange.
  page?: number;
  limit?: number;
  total?: number;
  pageSizeOptions?: number[];
  onPageChange?: (page: number) => void;
  onLimitChange?: (limit: number) => void;
}

// DEFAULT_PAGE_SIZES is the canonical page-size menu used across every
// list page in both SPAs. Defined here so a future change to the
// available steps lands in one place.
export const DEFAULT_PAGE_SIZES = [50, 100, 250, 500, 1000];

export function Table<T extends Record<string, any>>({
  columns,
  data,
  loading = false,
  emptyMessage = "No data found",
  onRowClick,
  sortKey,
  sortDir = "desc",
  onSort,
  page,
  limit,
  total,
  pageSizeOptions = DEFAULT_PAGE_SIZES,
  onPageChange,
  onLimitChange,
}: TableProps<T>) {
  const showPagination = typeof total === "number" && typeof page === "number" && typeof limit === "number" && limit > 0;
  const totalPages = showPagination ? Math.max(1, Math.ceil((total as number) / (limit as number))) : 1;
  const curPage = showPagination ? Math.min(Math.max(1, page as number), totalPages) : 1;
  const startIdx = showPagination && (total as number) > 0 ? (curPage - 1) * (limit as number) + 1 : 0;
  const endIdx = showPagination ? Math.min((total as number), curPage * (limit as number)) : 0;
  return (
    <div className="space-y-2">
    <div className="overflow-x-auto rounded-lg border border-panel-border">
      <table className="w-full text-sm text-left">
        <thead className="bg-panel-surface text-panel-muted uppercase text-xs">
          <tr>
            {columns.map((col, idx) => {
              const isSortable = !!(col.sortKey && onSort);
              const isActive = isSortable && sortKey === col.sortKey;
              const arrow = isActive ? (sortDir === "asc" ? " ↑" : " ↓") : "";
              return (
                <th
                  key={col.key ?? idx}
                  className={`px-4 py-3 font-medium ${col.headerClass ?? ""} ${
                    isSortable ? "select-none" : ""
                  }`}
                >
                  {isSortable ? (
                    <button
                      type="button"
                      onClick={() => onSort!(col.sortKey!)}
                      className={`inline-flex items-center gap-1 -mx-2 px-2 py-1 rounded transition-colors hover:bg-panel-bg/60 ${
                        isActive ? "text-blue-400" : ""
                      }`}
                    >
                      {col.header}
                      <span className="text-[10px] w-3 inline-block text-left">{arrow}</span>
                    </button>
                  ) : (
                    col.header
                  )}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody className="divide-y divide-panel-border">
          {loading ? (
            <tr>
              <td colSpan={columns.length} className="px-4 py-8 text-center text-panel-muted">Loading...</td>
            </tr>
          ) : data.length === 0 ? (
            <tr>
              <td colSpan={columns.length} className="px-4 py-8 text-center text-panel-muted">{emptyMessage}</td>
            </tr>
          ) : (
            data.map((item, i) => (
              <tr
                key={i}
                onClick={onRowClick ? () => onRowClick(item) : undefined}
                className={`transition-colors ${
                  onRowClick
                    ? "cursor-pointer hover:bg-panel-surface/80"
                    : "hover:bg-panel-surface/50"
                }`}
              >
                {columns.map((col, idx) => (
                  <td
                    key={col.key ?? idx}
                    className="px-4 py-3 text-panel-text"
                    // Stop click bubbling for cells that contain action
                    // buttons (Kill, Edit, ...) so clicking them doesn't
                    // also trigger the row-click handler.
                    onClick={onRowClick ? (e) => {
                      const target = e.target as HTMLElement;
                      if (target.closest("button, a, input, label, [data-stop-row-click]")) {
                        e.stopPropagation();
                      }
                    } : undefined}
                  >
                    {col.render
                      ? col.render(item)
                      : col.accessor
                        ? col.accessor(item)
                        : col.key
                          ? String(item[col.key] ?? "")
                          : ""}
                  </td>
                ))}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
    {showPagination && (
      <div className="flex flex-wrap items-center justify-between gap-3 px-1 text-xs text-panel-muted">
        <div>
          {(total as number) > 0
            ? <>Showing <span className="text-panel-text">{startIdx.toLocaleString()}–{endIdx.toLocaleString()}</span> of <span className="text-panel-text">{(total as number).toLocaleString()}</span></>
            : "No results"}
        </div>
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={() => onPageChange && onPageChange(1)}
              disabled={curPage <= 1}
              className="min-w-9 min-h-9 px-2.5 py-1.5 rounded border border-panel-border hover:bg-panel-bg disabled:opacity-40 disabled:cursor-not-allowed"
              title="First page"
            >«</button>
            <button
              type="button"
              onClick={() => onPageChange && onPageChange(curPage - 1)}
              disabled={curPage <= 1}
              className="min-w-9 min-h-9 px-2.5 py-1.5 rounded border border-panel-border hover:bg-panel-bg disabled:opacity-40 disabled:cursor-not-allowed"
              title="Previous page"
            >‹</button>
            <span className="px-2 text-panel-text">Page {curPage} of {totalPages}</span>
            <button
              type="button"
              onClick={() => onPageChange && onPageChange(curPage + 1)}
              disabled={curPage >= totalPages}
              className="min-w-9 min-h-9 px-2.5 py-1.5 rounded border border-panel-border hover:bg-panel-bg disabled:opacity-40 disabled:cursor-not-allowed"
              title="Next page"
            >›</button>
            <button
              type="button"
              onClick={() => onPageChange && onPageChange(totalPages)}
              disabled={curPage >= totalPages}
              className="min-w-9 min-h-9 px-2.5 py-1.5 rounded border border-panel-border hover:bg-panel-bg disabled:opacity-40 disabled:cursor-not-allowed"
              title="Last page"
            >»</button>
          </div>
          {onLimitChange && (
            <label className="flex items-center gap-1">
              <span>Per page</span>
              <select
                value={limit}
                onChange={(e) => onLimitChange(parseInt(e.target.value, 10))}
                className="px-2 py-1 bg-panel-bg border border-panel-border rounded text-panel-text"
              >
                {pageSizeOptions.map((n) => (
                  <option key={n} value={n}>{n}</option>
                ))}
              </select>
            </label>
          )}
        </div>
      </div>
    )}
    </div>
  );
}
