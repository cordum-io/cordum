import type { InputHTMLAttributes } from "react";
import { cn } from "../../lib/utils";

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={cn(
        "w-full rounded-md border border-border bg-input px-4 py-2.5 text-sm text-foreground shadow-sm transition-all duration-micro ease-[cubic-bezier(0.16,1,0.3,1)] placeholder:text-muted-foreground/60 hover:border-accent/40 hover:shadow-soft focus:outline-none focus:border-accent focus:ring-2 focus:ring-accent/30 aria-[invalid=true]:border-danger disabled:bg-surface-1 disabled:text-muted-foreground disabled:cursor-not-allowed",
        className
      )}
      {...props}
    />
  );
}

export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ className, icon, ...props }, ref) => {
    // Fall back to placeholder as aria-label when no explicit label association
    // exists (no id for htmlFor, no aria-label). Hidden inputs are exempt.
    const effectiveAriaLabel =
      props["aria-label"] ?? (props.id || props.type === "hidden" ? undefined : props.placeholder);

    return (
      <div className="relative">
        {icon && (
          <div className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground">
            {icon}
          </div>
        )}
        <input
          ref={ref}
          aria-label={effectiveAriaLabel}
          className={cn(
            "flex h-9 w-full rounded-2xl border border-border bg-surface-2/50 px-3 py-2 text-sm text-foreground",
            "placeholder:text-muted-foreground/60",
            "focus:outline-none focus:ring-2 focus:ring-cordum/30 focus:border-cordum/40",
            "disabled:opacity-50 disabled:cursor-not-allowed",
            "transition-all duration-150",
            icon && "pl-9",
            className,
          )}
          {...props}
        />
      </div>
    );
  },
);

Input.displayName = "Input";
