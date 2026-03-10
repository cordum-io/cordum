import { type ReactNode } from "react";
import { cn } from "@/lib/utils";

type AccentVariant = "healthy" | "warning" | "danger" | "info" | "muted" | "cordum";

const statusClass: Record<AccentVariant, string> = {
  healthy: "",
  warning: "status-warning glow-warning",
  danger: "status-danger glow-danger",
  info: "status-info",
  muted: "status-muted",
  cordum: "glow-cordum",
};

interface InstrumentCardProps {
  accent?: AccentVariant;
  className?: string;
  children: ReactNode;
  onClick?: () => void;
  hoverable?: boolean;
}

export function InstrumentCard({
  accent = "cordum",
  className,
  children,
  onClick,
  hoverable = false,
}: InstrumentCardProps) {
  return (
    <div
      onClick={onClick}
      className={cn(
        "instrument-card flex flex-col",
        statusClass[accent],
        hoverable && "instrument-card-hover cursor-pointer",
        onClick && "cursor-pointer",
        className,
      )}
    >
      {children}
    </div>
  );
}

interface InstrumentCardHeaderProps {
  title: string;
  subtitle?: string;
  action?: ReactNode;
  icon?: ReactNode;
  className?: string;
}

export function InstrumentCardHeader({
  title,
  subtitle,
  action,
  icon,
  className,
}: InstrumentCardHeaderProps) {
  return (
    <div className={cn("flex items-start justify-between mb-4 gap-4", className)}>
      <div className="min-w-0">
        <h3 className="text-sm font-semibold font-display text-foreground tracking-tight leading-snug truncate">
          {title}
        </h3>
        {subtitle && (
          <p className="text-[11px] text-muted-foreground mt-1 leading-normal">{subtitle}</p>
        )}
      </div>
      <div className="flex items-center gap-3 shrink-0">
        {icon && (
          <div className="text-cordum/60">
            {icon}
          </div>
        )}
        {action && <div>{action}</div>}
      </div>
    </div>
  );
}

interface InstrumentCardBodyProps {
  className?: string;
  children: ReactNode;
}

export function InstrumentCardBody({ className, children }: InstrumentCardBodyProps) {
  return <div className={cn("min-w-0 flex-1", className)}>{children}</div>;
}

interface InstrumentCardFooterProps {
  className?: string;
  children: ReactNode;
}

export function InstrumentCardFooter({ className, children }: InstrumentCardFooterProps) {
  return (
    <div className={cn("-mx-5 -mb-5 mt-5 px-5 py-3 border-t border-border/40 bg-surface-2/30", className)}>
      {children}
    </div>
  );
}
