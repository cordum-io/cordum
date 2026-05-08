import { type ReactNode } from "react";
import { ArrowUp, ArrowDown, ArrowUpDown } from "lucide-react";
import { cn } from "@/lib/utils";

interface Column<T> {
  key: string;
  header: string;
  width?: string;
  align?: "left" | "center" | "right";
  sortable?: boolean;
  sortKey?: string;
  render: (row: T, index: number) => ReactNode;
}

interface LegacyDataTableProps<T> {
  columns: Column<T>[];
  data: T[];
  keyExtractor: (row: T, index: number) => string;
  onRowClick?: (row: T) => void;
  emptyMessage?: string;
  className?: string;
  compact?: boolean;
  sortKey?: string;
  sortDir?: "asc" | "desc";
  onSort?: (key: string) => void;
}

function SortIndicator({ active, dir }: { active: boolean; dir?: "asc" | "desc" }) {
  if (!active) return <ArrowUpDown className="w-3 h-3 opacity-30" />;
  return dir === "asc"
    ? <ArrowUp className="w-3 h-3" />
    : <ArrowDown className="w-3 h-3" />;
}

export function LegacyDataTable<T>({
  columns,
  data,
  keyExtractor,
  onRowClick,
  emptyMessage = "No data",
  className,
  compact = false,
  sortKey,
  sortDir,
  onSort,
}: LegacyDataTableProps<T>) {
  return (
    <div className={cn("overflow-x-auto", className)}>
      <table className="w-full">
        <thead>
          <tr className="border-b border-border bg-surface-0">
            {columns.map((col) => {
              const colSortKey = col.sortKey ?? col.key;
              const isSortable = col.sortable && onSort;
              const isActive = isSortable && sortKey === colSortKey;

              return (
                <th
                  key={col.key}
                  className={cn(
                    "text-left text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest",
                    compact ? "px-3 py-2" : "px-5 py-3",
                    col.align === "right" && "text-right",
                    col.align === "center" && "text-center",
                    isSortable && "cursor-pointer select-none hover:text-foreground transition-colors",
                  )}
                  style={{ width: col.width }}
                  role="columnheader"
                  aria-sort={isSortable ? (isActive ? (sortDir === "asc" ? "ascending" : "descending") : "none") : undefined}
                  tabIndex={isSortable ? 0 : undefined}
                  onClick={isSortable ? () => onSort(colSortKey) : undefined}
                  onKeyDown={isSortable ? (e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      onSort(colSortKey);
                    }
                  } : undefined}
                >
                  <span className={cn("inline-flex items-center gap-1.5", col.align === "right" && "flex-row-reverse")}>
                    {col.header}
                    {isSortable && <SortIndicator active={!!isActive} dir={sortDir} />}
                  </span>
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {data.length === 0 ? (
            <tr>
              <td
                colSpan={columns.length}
                className="text-center text-sm text-muted-foreground py-12"
              >
                {emptyMessage}
              </td>
            </tr>
          ) : (
            data.map((row, i) => (
              <tr
                key={keyExtractor(row, i)}
                onClick={() => onRowClick?.(row)}
                className={cn(
                  "border-b border-border hover:bg-surface-1 transition-colors",
                  onRowClick && "cursor-pointer",
                )}
              >
                {columns.map((col) => (
                  <td
                    key={col.key}
                    className={cn(
                      "text-sm",
                      compact ? "px-3 py-2" : "px-5 py-3",
                      col.align === "right" && "text-right",
                      col.align === "center" && "text-center",
                    )}
                  >
                    {col.render(row, i)}
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
