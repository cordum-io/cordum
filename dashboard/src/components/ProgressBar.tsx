import type { HTMLAttributes } from "react";
import { cn } from "../lib/utils";

export function ProgressBar({
  value,
  className,
  ...props
}: HTMLAttributes<HTMLDivElement> & { value: number }) {
  const clamped = Math.min(100, Math.max(0, value));
  return (
    <div
      className={cn(
        "h-2 w-full overflow-hidden rounded-full bg-[color:rgba(90,106,112,0.2)]",
        className
      )}
      {...props}
    >
      <div
        className="h-full rounded-full bg-gradient-to-r from-[color:rgba(15,127,122,0.9)] to-[color:rgba(212,131,58,0.9)]"
        style={{ width: `${clamped}%` }}
      />
    </div>
  );
}
