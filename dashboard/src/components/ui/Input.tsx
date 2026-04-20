import { forwardRef, type InputHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  icon?: React.ReactNode;
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
