import { forwardRef, type InputHTMLAttributes, type ReactNode } from "react";
import { cn } from "@/lib/utils";

interface CheckboxProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "type"> {
  label?: ReactNode;
  description?: ReactNode;
  wrapperClassName?: string;
}

export const Checkbox = forwardRef<HTMLInputElement, CheckboxProps>(
  ({ className, label, description, wrapperClassName, ...props }, ref) => {
    const checkbox = (
      <input
        ref={ref}
        type="checkbox"
        className={cn(
          "h-4 w-4 rounded border-border bg-surface-0 text-cordum accent-[oklch(0.82_0.18_165)] focus:ring-cordum",
          className,
        )}
        {...props}
      />
    );

    if (!label && !description) {
      return checkbox;
    }

    return (
      <label
        className={cn(
          "flex cursor-pointer items-start gap-2 rounded-xl px-2 py-1 transition-colors hover:bg-surface-2/40",
          props.disabled && "cursor-not-allowed opacity-50",
          wrapperClassName,
        )}
      >
        <span className="pt-0.5">{checkbox}</span>
        <span className="min-w-0">
          {label && <span className="block text-sm text-foreground">{label}</span>}
          {description && (
            <span className="block text-xs text-muted-foreground">
              {description}
            </span>
          )}
        </span>
      </label>
    );
  },
);

Checkbox.displayName = "Checkbox";
