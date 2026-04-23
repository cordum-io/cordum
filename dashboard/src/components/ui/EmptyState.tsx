import type { HTMLAttributes, ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { cn } from "../../lib/utils";

interface EmptyStateProps extends HTMLAttributes<HTMLDivElement> {
  icon?: LucideIcon;
  title: string;
  description?: string;
  action?: ReactNode;
  tone?: "success" | "warning" | "danger" | "info" | "muted";
}

const toneIconStyles: Record<string, string> = {
  success: "text-success opacity-60",
  warning: "text-warning opacity-60",
  danger: "text-danger opacity-60",
  info: "text-accent opacity-60",
  muted: "text-muted opacity-60",
};

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  tone,
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
      {Icon && (
        <Icon
          className={cn(
            "mb-3 h-12 w-12",
            tone ? toneIconStyles[tone] : "text-muted opacity-60",
          )}
        />
      )}
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
