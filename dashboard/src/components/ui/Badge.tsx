import type { HTMLAttributes } from "react";
import { cn } from "../../lib/utils";

const variants: Record<string, string> = {
  default: "bg-surface2 text-ink",
  success: "bg-success/10 text-success",
  warning: "bg-warning/15 text-warning",
  danger: "bg-danger/10 text-danger",
  info: "bg-accent/10 text-accent",
  enterprise: "bg-purple-500/10 text-purple-600 border border-purple-200/50",
};

export function Badge({
  className,
  variant = "default",
  ...props
}: HTMLAttributes<HTMLSpanElement> & { variant?: keyof typeof variants }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium",
        variants[variant],
        className
      )}
      {...props}
    />
  );
}
