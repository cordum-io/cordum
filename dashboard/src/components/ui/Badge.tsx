import type { HTMLAttributes } from "react";
import { cn } from "../../lib/utils";

const variants: Record<string, string> = {
  default: "bg-surface2 text-ink",
  success: "bg-[color:rgba(31,122,87,0.12)] text-success",
  warning: "bg-[color:rgba(197,138,28,0.18)] text-warning",
  danger: "bg-[color:rgba(184,58,58,0.14)] text-danger",
  info: "bg-[color:rgba(15,127,122,0.12)] text-accent",
};

export function Badge({
  className,
  variant = "default",
  ...props
}: HTMLAttributes<HTMLSpanElement> & { variant?: keyof typeof variants }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-3 py-1 text-xs font-semibold",
        variants[variant],
        className
      )}
      {...props}
    />
  );
}
