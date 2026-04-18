import React from "react";

interface Column<T> {
  key?: string;
  header: string;
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
}

export function Table<T extends Record<string, any>>({
  columns,
  data,
  loading = false,
  emptyMessage = "No data found",
  onRowClick,
  sortKey,
  sortDir = "desc",
  onSort,
}: TableProps<T>) {
  return (
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
  );
}
