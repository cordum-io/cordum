import type { ButtonHTMLAttributes } from "react";
import { cn } from "../../lib/utils";

const baseStyles =
  "inline-flex items-center justify-center gap-2 rounded-lg px-4 py-2 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[color:var(--ring)] disabled:cursor-not-allowed disabled:opacity-50";

const variants: Record<string, string> = {
  primary: "bg-accent text-white hover:bg-accent/90",
  outline: "border border-border text-ink hover:border-accent hover:text-accent hover:bg-accent/5",
  ghost: "text-ink hover:bg-surface2",
  subtle: "bg-accent/10 text-accent hover:bg-accent/15",
  danger: "bg-danger text-white hover:bg-danger/90",
};

const sizes: Record<string, string> = {
  sm: "px-3 py-1.5 text-xs",
  md: "px-4 py-2 text-sm",
  lg: "px-5 py-2.5 text-base",
};

export function Button({
  className,
  variant = "primary",
  size = "md",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: keyof typeof variants;
  size?: keyof typeof sizes;
}) {
  return (
    <button className={cn(baseStyles, variants[variant], sizes[size], className)} {...props} />
  );
}
