import { Children, isValidElement, type ButtonHTMLAttributes, type ReactNode } from "react";
import { cn } from "../../lib/utils";

const baseStyles =
  "inline-flex items-center justify-center gap-2 rounded-md border border-transparent font-sans font-semibold transition-all duration-micro ease-out focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/35 focus-visible:ring-offset-2 focus-visible:ring-offset-surface-0 disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-55 active:scale-[0.985]";

const variants: Record<string, string> = {
  primary: "bg-accent text-background hover:bg-accent-bright",
  secondary: "border-border bg-surface-2 text-foreground hover:bg-surface-3",
  outline: "border-border bg-surface-1/40 text-foreground hover:bg-surface-2/50",
  ghost: "bg-transparent text-secondary-foreground hover:bg-surface-2/50 hover:text-foreground",
  destructive: "bg-danger text-background hover:brightness-110",
};

const sizes: Record<string, string> = {
  sm: "h-8 px-3 text-xs uppercase tracking-[0.12em]",
  default: "h-9 px-4 text-sm",
  control: "h-9 px-3 text-xs uppercase tracking-[0.14em]",
  lg: "h-10 px-5 text-sm",
  icon: "h-9 w-9 p-0",
};

function hasVisibleText(children: ReactNode): boolean {
  return Children.toArray(children).some((child) => {
    if (typeof child === "string") return child.trim().length > 0;
    if (typeof child === "number") return true;
    if (!isValidElement(child)) return false;
    return hasVisibleText(child.props.children);
  });
}

export function Button({
  className,
  variant = "primary",
  size = "default",
  children,
  type = "button",
  "aria-label": ariaLabel,
  "aria-labelledby": ariaLabelledBy,
  title,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: keyof typeof variants;
  size?: keyof typeof sizes;
}) {
  const needsIconLabel = size === "icon" && !ariaLabel && !ariaLabelledBy && !hasVisibleText(children);
  const derivedAriaLabel = needsIconLabel
    ? (typeof title === "string" && title.trim() ? title.trim() : "Action")
    : ariaLabel;

  return (
    <button
      type={type}
      aria-label={derivedAriaLabel}
      aria-labelledby={ariaLabelledBy}
      title={title}
      className={cn(baseStyles, variants[variant], sizes[size], className)}
      {...props}
    >
      {children}
    </button>
  );
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ variant = "primary", size = "md", loading, className, children, disabled, type = "button", ...props }, ref) => {
    return (
      <button
        ref={ref}
        type={type}
        disabled={disabled || loading}
        className={cn(
          "inline-flex items-center justify-center font-medium transition-all duration-150 whitespace-nowrap",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cordum/40 focus-visible:ring-offset-2 focus-visible:ring-offset-background",
          "disabled:opacity-50 disabled:pointer-events-none",
          "active:scale-[0.98]",
          variantStyles[variant],
          sizeStyles[size],
          className,
        )}
        {...props}
      >
        {loading && <Loader2 className="w-3.5 h-3.5 animate-spin" />}
        {children}
      </button>
    );
  },
);

Button.displayName = "Button";
