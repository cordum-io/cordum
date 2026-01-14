import type { HTMLAttributes } from "react";
import { cn } from "../lib/utils";

const variantStyles: Record<string, string> = {
  default: "bg-gradient-to-r from-accent to-accent2",
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
};

export function ProgressBar({
  value,
  variant = "default",
  className,
  ...props
}: HTMLAttributes<HTMLDivElement> & { value: number; variant?: keyof typeof variantStyles }) {
  const clamped = Math.min(100, Math.max(0, value));
  return (
    <div
      className={cn(
        "h-2 w-full overflow-hidden rounded-full bg-[color:rgba(90,106,112,0.15)]",
        className
      )}
      {...props}
    >
      <div
        className={cn(
          "h-full rounded-full transition-all duration-500 ease-[cubic-bezier(0.16,1,0.3,1)]",
          variantStyles[variant]
        )}
        style={{ width: `${clamped}%` }}
      />
    </div>
  );
}
