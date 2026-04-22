import { cn } from "@/lib/utils";

export type BadgeVariant = "healthy" | "warning" | "danger" | "info" | "muted" | "cordum" | "governance";

export const statusToneTextClasses: Record<BadgeVariant, string> = {
  healthy: "text-[var(--color-success)]",
  warning: "text-[var(--color-warning)]",
  danger: "text-destructive",
  info: "text-[var(--color-info)]",
  muted: "text-muted-foreground",
  cordum: "text-cordum",
  governance: "text-[var(--color-governance)]",
};

export const statusToneBorderClasses: Record<BadgeVariant, string> = {
  healthy: "border-[var(--color-success)]/20",
  warning: "border-[var(--color-warning)]/20",
  danger: "border-destructive/20",
  info: "border-[var(--color-info)]/20",
  muted: "border-muted-foreground/20",
  cordum: "border-cordum/20",
  governance: "border-[var(--color-governance)]/20",
};

export const statusToneBorderTextClasses: Record<BadgeVariant, string> = {
  healthy: "border-[var(--color-success)] text-[var(--color-success)]",
  warning: "border-[var(--color-warning)] text-[var(--color-warning)]",
  danger: "border-destructive text-destructive",
  info: "border-[var(--color-info)] text-[var(--color-info)]",
  muted: "border-border text-muted-foreground",
  cordum: "border-cordum text-cordum",
  governance: "border-[var(--color-governance)] text-[var(--color-governance)]",
};

export const statusToneGradientClasses: Record<BadgeVariant, string> = {
  healthy: "from-[var(--color-success)]/12 to-transparent",
  warning: "from-[var(--color-warning)]/12 to-transparent",
  danger: "from-destructive/12 to-transparent",
  info: "from-[var(--color-info)]/12 to-transparent",
  muted: "from-muted-foreground/8 to-transparent",
  cordum: "from-cordum/12 to-transparent",
  governance: "from-[var(--color-governance)]/12 to-transparent",
};

const variants: Record<BadgeVariant, string> = {
  healthy: `bg-[var(--color-success)]/15 ${statusToneTextClasses.healthy} ${statusToneBorderClasses.healthy}`,
  warning: `bg-[var(--color-warning)]/15 ${statusToneTextClasses.warning} ${statusToneBorderClasses.warning}`,
  danger: `bg-destructive/15 ${statusToneTextClasses.danger} ${statusToneBorderClasses.danger}`,
  info: `bg-[var(--color-info)]/15 ${statusToneTextClasses.info} ${statusToneBorderClasses.info}`,
  muted: `bg-muted-foreground/15 ${statusToneTextClasses.muted} ${statusToneBorderClasses.muted}`,
  cordum: `bg-cordum/12 ${statusToneTextClasses.cordum} ${statusToneBorderClasses.cordum}`,
  governance: `bg-[var(--color-governance)]/15 ${statusToneTextClasses.governance} ${statusToneBorderClasses.governance}`,
};

export const statusToneDotClasses: Record<BadgeVariant, string> = {
  healthy: "bg-[var(--color-success)]",
  warning: "bg-[var(--color-warning)]",
  danger: "bg-destructive",
  info: "bg-[var(--color-info)]",
  muted: "bg-muted-foreground",
  cordum: "bg-cordum",
  governance: "bg-[var(--color-governance)]",
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
        "inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium border",
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
                statusToneDotClasses[variant],
              )}
            />
          )}
          <span
            className={cn(
              "relative inline-flex rounded-full h-1.5 w-1.5",
              statusToneDotClasses[variant],
            )}
          />
        </span>
      )}
      {children}
    </span>
  );
}
