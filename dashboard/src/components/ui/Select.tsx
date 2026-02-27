import type { SelectHTMLAttributes } from "react";
import { cn } from "../../lib/utils";

export function Select({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        "w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink cursor-pointer transition-colors hover:border-accent/40 focus:outline-none focus:border-accent focus:ring-2 focus:ring-[color:var(--ring)]",
        className
      )}
      {...props}
    />
  );
}
