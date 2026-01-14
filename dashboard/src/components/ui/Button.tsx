import type { ButtonHTMLAttributes } from "react";
import { cn } from "../../lib/utils";

const baseStyles =
  "inline-flex items-center justify-center gap-2 rounded-full px-4 py-2 text-sm font-semibold transition-all duration-200 ease-[cubic-bezier(0.16,1,0.3,1)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[color:var(--ring)] disabled:cursor-not-allowed disabled:opacity-60 active:scale-[0.98]";

const variants: Record<string, string> = {
  primary: "bg-accent text-white shadow-glow hover:translate-y-[-1px] hover:brightness-110",
  outline: "border border-border text-ink hover:border-accent hover:text-accent hover:bg-[color:rgba(15,127,122,0.04)]",
  ghost: "text-ink hover:bg-[color:rgba(15,127,122,0.08)]",
  subtle: "bg-[color:rgba(15,127,122,0.08)] text-ink hover:bg-[color:rgba(15,127,122,0.14)]",
  danger: "bg-danger text-white shadow-lift hover:translate-y-[-1px] hover:brightness-110",
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
