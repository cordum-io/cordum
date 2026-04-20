import { forwardRef, type ButtonHTMLAttributes } from "react";
import { cn } from "@/lib/utils";
import { Loader2 } from "lucide-react";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "outline" | "default" | "subtle";
type ButtonSize = "sm" | "md" | "lg" | "icon";

/* Exact match to showcase button styles */
const variantStyles: Record<ButtonVariant, string> = {
  primary:
    "bg-primary text-primary-foreground hover:bg-primary/85 font-semibold shadow-glow",
  default:
    "bg-primary text-primary-foreground hover:bg-primary/85 font-semibold shadow-glow",
  subtle:
    "text-muted-foreground hover:text-foreground hover:bg-secondary",
  secondary:
    "bg-secondary text-secondary-foreground hover:bg-secondary/70",
  ghost:
    "text-muted-foreground hover:text-foreground hover:bg-secondary",
  danger:
    "bg-destructive/15 text-destructive hover:bg-destructive/25 border border-destructive/20 shadow-lift",
  outline:
    "border border-border text-foreground hover:bg-secondary",
};

const sizeStyles: Record<ButtonSize, string> = {
  sm: "h-8 px-3 text-xs rounded-full gap-1.5",
  md: "h-9 px-4 text-sm rounded-full gap-2",
  lg: "h-11 px-6 text-base rounded-full gap-2",
  icon: "h-9 w-9 rounded-full",
};

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
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
