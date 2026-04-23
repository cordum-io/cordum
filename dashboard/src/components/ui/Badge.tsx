import type { HTMLAttributes } from "react";
import { cn } from "../../lib/utils";

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  variant?: "default" | "success" | "warning" | "danger" | "destructive" | "info" | "enterprise";
  density?: "default" | "compact";
  icon?: React.ReactNode;
}

const variantStyles: Record<NonNullable<BadgeProps["variant"]>, string> = {
  default: "border-border bg-surface-2 text-foreground",
  success: "border-status-success-border bg-status-success-bg text-success",
  warning: "border-status-warning-border bg-status-warning-bg text-warning",
  danger: "border-status-danger-border bg-status-danger-bg text-danger",
  destructive: "border-status-danger-border bg-status-danger-bg text-danger",
  info: "border-status-info-border bg-status-info-bg text-info",
  enterprise: "border-status-info-border bg-status-info-bg text-info",
};

export function Badge({
  className,
  variant = "default",
  density = "default",
  icon,
  children,
  ...props
}: BadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 border font-mono font-semibold uppercase transition-colors duration-micro",
        density === "compact" ? "rounded-sm px-1.5 py-0 text-[10px] tracking-[0.1em]" : "rounded-sm px-2 py-0.5 text-[10px] tracking-[0.12em]",
        variantStyles[variant],
        className
      )}
      {...props}
    >
      {icon && <span className="h-3 w-3 shrink-0">{icon}</span>}
      {children}
    </span>
  );
}
