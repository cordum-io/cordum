import { useMemo, useRef, useState, type ReactNode, type CSSProperties, type MouseEvent as ReactMouseEvent } from "react";
import {
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
  type ColumnDef,
  type SortingState,
} from "@tanstack/react-table";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";
import { cn } from "@/lib/utils";

export type DecisionTier =
  | "allow"
  | "deny"
  | "require_approval"
  | "allow_with_constraints"
  | "throttle";

export interface DataTableProps<T> {
  columns: ColumnDef<T, unknown>[];
  data: T[];
  emptyState: ReactNode;
  onRowClick?: (row: T) => void;
  decisionAccessor?: (row: T) => DecisionTier | undefined;
  className?: string;
  compact?: boolean;
  initialSorting?: SortingState;
  /** Estimated row pixel height for the virtualizer. Default 44. */
  estimatedRowHeight?: number;
  /** Scrollable container height when virtualization is active. Default 480. */
  virtualizedHeight?: number;
}

export const VIRTUALIZE_THRESHOLD = 100;

const DECISION_EDGE: Record<DecisionTier, string> = {
  allow: "var(--color-success)",
  deny: "var(--color-danger)",
  require_approval: "var(--color-warning)",
  allow_with_constraints: "var(--color-warning)",
  throttle: "var(--color-warning)",
};

function isInteractiveTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.closest("button, a, input, select, textarea, [role='button'], [data-row-action]") !== null;
}

function rowEdgeStyle(tier: DecisionTier | undefined): CSSProperties | undefined {
  if (!tier) return undefined;
  return { boxShadow: `inset 3px 0 0 0 ${DECISION_EDGE[tier]}` };
}

/** Sortable, virtualization-aware table primitive. Variants: default; decision-identity (`decisionAccessor` → 3px left edge). Virtualizes when `data.length > VIRTUALIZE_THRESHOLD` (=100). Client-side sort. See `./README.md` for usage + migration. */
export function DataTable<T>({
  columns,
  data,
  emptyState,
  onRowClick,
  decisionAccessor,
  className,
  compact = false,
  initialSorting,
  estimatedRowHeight = 44,
  virtualizedHeight = 480,
}: DataTableProps<T>) {
  const [sorting, setSorting] = useState<SortingState>(initialSorting ?? []);

  const table = useReactTable({
    data,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });

  const rows = table.getRowModel().rows;
  const shouldVirtualize = rows.length > VIRTUALIZE_THRESHOLD;

  const handleRowClick = useMemo(() => {
    if (!onRowClick) return undefined;
    return (row: T, event: ReactMouseEvent<HTMLTableRowElement>) => {
      if (isInteractiveTarget(event.target)) return;
      onRowClick(row);
    };
  }, [onRowClick]);

  const headerRow = (
    <thead>
      {table.getHeaderGroups().map((hg) => (
        <tr key={hg.id} className="border-b border-border bg-surface-0">
          {hg.headers.map((header) => {
            const canSort = header.column.getCanSort();
            const sortDir = header.column.getIsSorted();
            const align = (header.column.columnDef.meta as { align?: "left" | "center" | "right" } | undefined)?.align ?? "left";
            return (
              <th
                key={header.id}
                scope="col"
                role="columnheader"
                aria-sort={canSort ? (sortDir === "asc" ? "ascending" : sortDir === "desc" ? "descending" : "none") : undefined}
                tabIndex={canSort ? 0 : undefined}
                onClick={canSort ? header.column.getToggleSortingHandler() : undefined}
                onKeyDown={canSort ? (e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    header.column.toggleSorting();
                  }
                } : undefined}
                className={cn(
                  "text-left text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest",
                  compact ? "px-3 py-2" : "px-5 py-3",
                  align === "right" && "text-right",
                  align === "center" && "text-center",
                  canSort && "cursor-pointer select-none hover:text-foreground transition-colors",
                )}
                style={{ width: header.getSize() === 150 ? undefined : header.getSize() }}
              >
                <span className={cn("inline-flex items-center gap-1.5", align === "right" && "flex-row-reverse")}>
                  {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}
                  {canSort && (
                    sortDir === "asc" ? <ArrowUp className="w-3 h-3" />
                    : sortDir === "desc" ? <ArrowDown className="w-3 h-3" />
                    : <ArrowUpDown className="w-3 h-3 opacity-30" />
                  )}
                </span>
              </th>
            );
          })}
        </tr>
      ))}
    </thead>
  );

  const renderCells = (row: typeof rows[number]) =>
    row.getVisibleCells().map((cell) => {
      const align = (cell.column.columnDef.meta as { align?: "left" | "center" | "right" } | undefined)?.align ?? "left";
      return (
        <td
          key={cell.id}
          className={cn(
            "text-sm",
            compact ? "px-3 py-2" : "px-5 py-3",
            align === "right" && "text-right",
            align === "center" && "text-center",
          )}
        >
          {flexRender(cell.column.columnDef.cell, cell.getContext())}
        </td>
      );
    });

  if (rows.length === 0) {
    return (
      <div className={cn("overflow-x-auto", className)}>
        <table className="w-full">
          {headerRow}
          <tbody>
            <tr>
              <td colSpan={table.getVisibleFlatColumns().length} className="p-0">
                {emptyState}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    );
  }

  if (!shouldVirtualize) {
    return (
      <div className={cn("overflow-x-auto", className)}>
        <table className="w-full">
          {headerRow}
          <tbody>
            {rows.map((row) => {
              const tier = decisionAccessor?.(row.original);
              return (
                <tr
                  key={row.id}
                  onClick={handleRowClick ? (e) => handleRowClick(row.original, e) : undefined}
                  className={cn(
                    "border-b border-border hover:bg-surface-1 transition-colors",
                    handleRowClick && "cursor-pointer",
                  )}
                  style={rowEdgeStyle(tier)}
                  data-decision-tier={tier}
                >
                  {renderCells(row)}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    );
  }

  return (
    <VirtualizedBody
      rows={rows}
      columnsCount={table.getVisibleFlatColumns().length}
      headerRow={headerRow}
      renderCells={renderCells}
      onRowClick={handleRowClick}
      decisionAccessor={decisionAccessor}
      estimatedRowHeight={estimatedRowHeight}
      virtualizedHeight={virtualizedHeight}
      className={className}
    />
  );
}

interface VirtualizedBodyProps<T> {
  rows: ReturnType<ReturnType<typeof useReactTable<T>>["getRowModel"]>["rows"];
  columnsCount: number;
  headerRow: ReactNode;
  renderCells: (row: ReturnType<ReturnType<typeof useReactTable<T>>["getRowModel"]>["rows"][number]) => ReactNode;
  onRowClick?: (row: T, event: ReactMouseEvent<HTMLTableRowElement>) => void;
  decisionAccessor?: (row: T) => DecisionTier | undefined;
  estimatedRowHeight: number;
  virtualizedHeight: number;
  className?: string;
}

function VirtualizedBody<T>({
  rows,
  columnsCount,
  headerRow,
  renderCells,
  onRowClick,
  decisionAccessor,
  estimatedRowHeight,
  virtualizedHeight,
  className,
}: VirtualizedBodyProps<T>) {
  const containerRef = useRef<HTMLDivElement>(null);

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => containerRef.current,
    estimateSize: () => estimatedRowHeight,
    overscan: 8,
  });

  const virtualRows = virtualizer.getVirtualItems();
  const totalSize = virtualizer.getTotalSize();
  const paddingTop = virtualRows.length > 0 ? virtualRows[0].start : 0;
  const paddingBottom = virtualRows.length > 0 ? totalSize - virtualRows[virtualRows.length - 1].end : 0;

  return (
    <div
      ref={containerRef}
      className={cn("overflow-auto", className)}
      style={{ height: virtualizedHeight }}
      data-virtualized="true"
    >
      <table className="w-full">
        {headerRow}
        <tbody>
          {paddingTop > 0 && (
            <tr aria-hidden="true">
              <td colSpan={columnsCount} style={{ height: paddingTop, padding: 0, border: 0 }} />
            </tr>
          )}
          {virtualRows.map((vi) => {
            const row = rows[vi.index];
            const tier = decisionAccessor?.(row.original);
            return (
              <tr
                key={row.id}
                onClick={onRowClick ? (e) => onRowClick(row.original, e) : undefined}
                className={cn(
                  "border-b border-border hover:bg-surface-1 transition-colors",
                  onRowClick && "cursor-pointer",
                )}
                style={rowEdgeStyle(tier)}
                data-decision-tier={tier}
                data-index={vi.index}
              >
                {renderCells(row)}
              </tr>
            );
          })}
          {paddingBottom > 0 && (
            <tr aria-hidden="true">
              <td colSpan={columnsCount} style={{ height: paddingBottom, padding: 0, border: 0 }} />
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
