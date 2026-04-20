import type { HTMLAttributes } from "react";
import { cn } from "../../lib/utils";

export interface CardProps extends HTMLAttributes<HTMLDivElement> {
  variant?: "default" | "warning" | "danger" | "destructive" | "info";
}

const variantLineClasses: Record<NonNullable<CardProps["variant"]>, string> = {
  default: "before:bg-accent",
  warning: "before:bg-warning",
  danger: "before:bg-danger",
  destructive: "before:bg-danger",
  info: "before:bg-info",
};

export function Card({ className, variant, ...props }: CardProps) {
  const resolvedVariant = variant ?? "default";

const cardVariantStyles: Record<string, string> = {
  default: "before:bg-accent",
  warning: "before:bg-warning",
  destructive: "before:bg-danger",
};

export function Card({
  variant = "default",
  className,
  ...props
}: HTMLAttributes<HTMLDivElement> & { variant?: "default" | "warning" | "destructive" }) {
  return (
    <div
      className={cn(
        "surface-card relative rounded-3xl p-6 transition-shadow duration-300 overflow-hidden before:absolute before:inset-y-0 before:left-0 before:w-[2px]",
        cardVariantStyles[variant],
        className,
      )}
      {...props}
    />
  );
}

export function CardHeader({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("px-6 py-4 border-b border-border/50 flex items-center justify-between", className)} {...props} />;
}

export function CardTitle({ className, ...props }: HTMLAttributes<HTMLHeadingElement>) {
  return (
    <h3
      className={cn("font-display text-base font-semibold text-foreground", className)}
      {...props}
    />
  );
}

export function CardDescription({ className, ...props }: HTMLAttributes<HTMLParagraphElement>) {
  return (
    <p className={cn("text-[11px] leading-relaxed text-muted-foreground", className)} {...props} />
  );
}

export function CardContent({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("p-6", className)} {...props} />;
}

export function CardFooter({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("px-6 py-4 border-t border-border/50 bg-surface-2/30", className)} {...props} />;
}
