import type { InputHTMLAttributes } from "react";
import { cn } from "../../lib/utils";

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={cn(
        "w-full rounded-2xl border border-border bg-white/70 px-4 py-2 text-sm text-ink shadow-sm transition focus:outline-none focus:ring-2 focus:ring-[color:var(--ring)]",
        className
      )}
      {...props}
    />
  );
}
