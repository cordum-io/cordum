import type { SelectHTMLAttributes } from "react";
import { cn } from "../../lib/utils";

export function Select({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        "w-full rounded-md border border-border bg-input px-4 py-2.5 text-sm text-foreground shadow-sm transition-all duration-micro ease-[cubic-bezier(0.16,1,0.3,1)] cursor-pointer hover:border-accent/40 hover:shadow-soft focus:outline-none focus:border-accent focus:ring-2 focus:ring-accent/30 aria-[invalid=true]:border-danger disabled:bg-surface-1 disabled:text-muted-foreground disabled:cursor-not-allowed",
        className
      )}
      {...props}
    />
  );
}

export const Select = forwardRef<HTMLSelectElement, SelectProps>(
  ({ className, options, placeholder, children, ...props }, ref) => {
    return (
      <div className="relative">
        <select
          ref={ref}
          className={cn(
            "flex h-9 w-full appearance-none rounded-2xl border border-border bg-surface-2/50 px-3 pr-8 py-2 text-sm text-foreground",
            "focus:outline-none focus:ring-2 focus:ring-cordum/30 focus:border-cordum/40",
            "disabled:opacity-50 disabled:cursor-not-allowed",
            "transition-all duration-150",
            className,
          )}
          {...props}
        >
          {placeholder && (
            <option value="" disabled>
              {placeholder}
            </option>
          )}
          {options
            ? options.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))
            : children}
        </select>
        <ChevronDown className="absolute right-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground pointer-events-none" />
      </div>
    );
  },
);

Select.displayName = "Select";
