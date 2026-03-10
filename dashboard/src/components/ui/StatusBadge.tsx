import { cn } from "@/lib/utils";

export type BadgeVariant = "healthy" | "warning" | "danger" | "info" | "muted" | "cordum";

/* Uses CSS variable-based semantic colors */
const variants: Record<BadgeVariant, string> = {
  healthy: "bg-[var(--color-success)]/15 text-[var(--color-success)] border-[var(--color-success)]/20",
  warning: "bg-[var(--color-warning)]/15 text-[var(--color-warning)] border-[var(--color-warning)]/20",
  danger: "bg-destructive/15 text-destructive border-destructive/20",
  info: "bg-[var(--color-info)]/15 text-[var(--color-info)] border-[var(--color-info)]/20",
  muted: "bg-muted-foreground/15 text-muted-foreground border-muted-foreground/20",
  cordum: "bg-cordum/12 text-cordum border-cordum/20",
};

const dotColors: Record<BadgeVariant, string> = {
  healthy: "bg-[var(--color-success)]",
  warning: "bg-[var(--color-warning)]",
  danger: "bg-destructive",
  info: "bg-[var(--color-info)]",
  muted: "bg-muted-foreground",
  cordum: "bg-cordum",
};

interface StatusBadgeProps {
  variant?: BadgeVariant;
  children: React.ReactNode;
  dot?: boolean;
  className?: string;
  pulse?: boolean;
}

export function StatusBadge({
  variant = "muted",
  children,
  dot = false,
  className,
  pulse = false,
}: StatusBadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium border",
        variants[variant],
        className,
      )}
    >
      {dot && (
        <span className="relative flex h-1.5 w-1.5">
          {pulse && (
            <span
              className={cn(
                "absolute inset-0 rounded-full status-pulse",
                dotColors[variant],
              )}
            />
          )}
          <span
            className={cn(
              "relative inline-flex rounded-full h-1.5 w-1.5",
              dotColors[variant],
            )}
          />
        </span>
      )}
      {children}
    </span>
  );
}
