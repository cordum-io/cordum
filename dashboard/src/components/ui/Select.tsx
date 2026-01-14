import type { SelectHTMLAttributes } from "react";
import { cn } from "../../lib/utils";

export function Select({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        "w-full rounded-2xl border border-border bg-white/70 px-4 py-2.5 text-sm text-ink shadow-sm transition-all duration-200 ease-[cubic-bezier(0.16,1,0.3,1)] cursor-pointer hover:border-[color:rgba(15,127,122,0.4)] hover:shadow-soft focus:outline-none focus:border-accent focus:ring-2 focus:ring-[color:var(--ring)]",
        className
      )}
      {...props}
    />
  );
}
