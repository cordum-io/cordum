import React from "react";

export type Column<T> = {
  key: string;
  header: React.ReactNode;
  render: (row: T) => React.ReactNode;
  className?: string;
};

export default function DataTable<T>({
  columns,
  rows,
  rowKey,
  onRowClick,
}: {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  onRowClick?: (row: T) => void;
}) {
  return (
    <div className="overflow-hidden rounded-xl border border-primary-border">
      <table className="w-full table-fixed text-sm">
        <thead className="bg-secondary-background/60 text-left text-xs uppercase tracking-wider text-tertiary-text">
          <tr>
            {columns.map((c) => (
              <th key={c.key} className={"px-3 py-2 " + (c.className || "")}>
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-primary-border">
          {rows.map((r) => (
            <tr
              key={rowKey(r)}
              className={["hover:bg-tertiary-background", onRowClick ? "cursor-pointer" : ""].join(" ")}
              onClick={(e) => {
                if (!onRowClick) {
                  return;
                }
                const target = e.target as HTMLElement | null;
                if (target?.closest?.("a,button,input,textarea,select,label")) {
                  return;
                }
                onRowClick(r);
              }}
            >
              {columns.map((c) => (
                <td key={c.key} className={"px-3 py-2 " + (c.className || "")}>
                  {c.render(r)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
