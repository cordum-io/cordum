import type { HTMLAttributes, ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { cn } from "../../lib/utils";

interface EmptyStateProps extends HTMLAttributes<HTMLDivElement> {
  icon?: LucideIcon;
  title: string;
  description?: string;
  action?: ReactNode;
}

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  className,
  ...props
}: EmptyStateProps) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center py-16 text-center",
        className,
      )}
      {...props}
    >
      {Icon && <Icon className="mb-3 h-12 w-12 text-muted opacity-60" />}
      <p className="text-sm font-semibold text-ink">{title}</p>
      {description && (
        <p className="mt-1 max-w-sm text-xs text-muted">{description}</p>
      )}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

export function TableEmptyState({
  colSpan,
  className,
  ...props
}: EmptyStateProps & { colSpan: number }) {
  return (
    <tr>
      <td colSpan={colSpan} className="px-4">
        <EmptyState className={className} {...props} />
      </td>
    </tr>
  );
}
