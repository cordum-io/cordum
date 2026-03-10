import { type ReactNode } from "react";
import { Info, AlertTriangle, AlertCircle, ShieldCheck } from "lucide-react";
import { cn } from "@/lib/utils";

type BannerVariant = "info" | "warning" | "error" | "success" | "cordum";

interface InfoBannerProps {
  variant?: BannerVariant;
  title?: string;
  children: ReactNode;
  icon?: ReactNode;
  className?: string;
  id?: string;
}

const variantStyles: Record<BannerVariant, string> = {
  info: "border-[var(--color-info)]/20 bg-[var(--color-info)]/10 text-[var(--color-info)] after:bg-[var(--color-info)]",
  warning: "border-[var(--color-warning)]/20 bg-[var(--color-warning)]/10 text-[var(--color-warning)] after:bg-[var(--color-warning)]",
  error: "border-destructive/20 bg-destructive/10 text-destructive after:bg-destructive",
  success: "border-[var(--color-success)]/20 bg-[var(--color-success)]/10 text-[var(--color-success)] after:bg-[var(--color-success)]",
  cordum: "border-cordum/20 bg-cordum/10 text-cordum-foreground after:bg-cordum",
};

const iconMap: Record<BannerVariant, typeof Info> = {
  info: Info,
  warning: AlertTriangle,
  error: AlertCircle,
  success: ShieldCheck,
  cordum: ShieldCheck,
};

export function InfoBanner({
  variant = "info",
  title,
  children,
  icon,
  className,
  id,
}: InfoBannerProps) {
  const Icon = iconMap[variant];

  return (
    <div
      id={id}
      className={cn(
        "rounded-lg border p-4 text-xs relative overflow-hidden after:absolute after:left-0 after:top-0 after:bottom-0 after:w-1 shadow-sm",
        variantStyles[variant],
        className
      )}
    >
      <div className="flex gap-3">
        <div className="shrink-0 mt-0.5">
          {icon || <Icon className="h-3.5 w-3.5" />}
        </div>
        <div className="flex-1 min-w-0">
          {title && (
            <div className="mb-1 font-semibold leading-none tracking-tight">
              {title}
            </div>
          )}
          <div className="text-muted-foreground leading-relaxed">
            {children}
          </div>
        </div>
      </div>
    </div>
  );
}
