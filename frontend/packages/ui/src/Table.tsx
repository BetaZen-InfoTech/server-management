import React from "react";

interface Column<T> {
  key: string;
  header: string;
  render?: (item: T) => React.ReactNode;
}

interface TableProps<T> {
  columns: Column<T>[];
  data: T[];
  loading?: boolean;
  emptyMessage?: string;
}

export function Table<T extends Record<string, unknown>>({
  columns,
  data,
  loading = false,
  emptyMessage = "No data found",
}: TableProps<T>) {
  return (
    <div className="overflow-x-auto rounded-lg border border-panel-border">
      <table className="w-full text-sm text-left">
        <thead className="bg-panel-surface text-panel-muted uppercase text-xs">
          <tr>
            {columns.map((col) => (
              <th key={col.key} className="px-4 py-3 font-medium">{col.header}</th>
            ))}
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
              <tr key={i} className="hover:bg-panel-surface/50 transition-colors">
                {columns.map((col) => (
                  <td key={col.key} className="px-4 py-3 text-panel-text">
                    {col.render ? col.render(item) : String(item[col.key] ?? "")}
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
