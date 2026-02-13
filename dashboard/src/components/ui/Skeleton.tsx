import type { HTMLAttributes } from "react";
import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Skeleton — base primitive
// ---------------------------------------------------------------------------

export function Skeleton({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "animate-pulse rounded bg-surface2 motion-reduce:animate-none",
        className,
      )}
      {...props}
    />
  );
}

// ---------------------------------------------------------------------------
// SkeletonText — multiple lines with varying widths
// ---------------------------------------------------------------------------

const LINE_WIDTHS = ["w-full", "w-4/5", "w-3/5"];

export function SkeletonText({
  lines = 3,
  className,
}: {
  lines?: number;
  className?: string;
}) {
  return (
    <div className={cn("space-y-2", className)}>
      {Array.from({ length: lines }, (_, i) => (
        <Skeleton
          key={i}
          className={cn("h-4", LINE_WIDTHS[i % LINE_WIDTHS.length])}
        />
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// SkeletonRow — table row with N skeleton cells
// ---------------------------------------------------------------------------

export function SkeletonRow({
  columns,
  className,
}: {
  columns: number;
  className?: string;
}) {
  return (
    <tr className={cn("animate-pulse motion-reduce:animate-none", className)}>
      {Array.from({ length: columns }, (_, j) => (
        <td key={j} className="px-4 py-3">
          <div className="h-4 rounded bg-surface2 w-3/4" />
        </td>
      ))}
    </tr>
  );
}

// ---------------------------------------------------------------------------
// SkeletonCard — card-shaped loading placeholder
// ---------------------------------------------------------------------------

export function SkeletonCard({ className }: { className?: string }) {
  return (
    <div
      className={cn(
        "surface-card animate-pulse rounded-3xl p-6 motion-reduce:animate-none",
        className,
      )}
    >
      <Skeleton className="mb-4 h-4 w-1/3" />
      <Skeleton className="mb-3 h-4 w-2/3" />
      <Skeleton className="h-4 w-1/2" />
    </div>
  );
}
