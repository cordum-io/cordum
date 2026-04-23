import type { HTMLAttributes, ReactNode } from "react";
import { Card } from "./ui/Card";
import { cn } from "../lib/utils";

const toneBorder: Record<string, string> = {
  success: "border-l-success",
  warning: "border-l-warning",
  danger: "border-l-danger",
  info: "border-l-accent",
  muted: "border-l-muted",
};

type MetricCardTone = "default" | "success" | "warning" | "danger" | "info";

const toneCardVariant: Record<MetricCardTone, CardProps["variant"]> = {
  default: "default",
  success: "default",
  warning: "warning",
  danger: "danger",
  info: "info",
};

const toneAccentClass: Record<MetricCardTone, string> = {
  default: "",
  success: "before:bg-success",
  warning: "",
  danger: "",
  info: "",
};

const toneIconShellClass: Record<MetricCardTone, string> = {
  default: "bg-surface-2/65 text-muted-foreground border-border/70",
  success: "bg-status-success-bg text-success border-status-success-border",
  warning: "bg-status-warning-bg text-warning border-status-warning-border",
  danger: "bg-status-danger-bg text-danger border-status-danger-border",
  info: "bg-status-info-bg text-info border-status-info-border",
};

export function MetricCard({
  title,
  value,
  detail,
  icon,
  tone,
  onClick,
  className,
}: {
  title: string;
  value: ReactNode;
  detail?: ReactNode;
  icon?: ReactNode;
  tone?: "success" | "warning" | "danger" | "info" | "muted";
  onClick?: HTMLAttributes<HTMLDivElement>["onClick"];
  className?: string;
}) {
  const interactive = typeof onClick === "function";

  if (isLoading) {
    return (
      <Card className={cn("flex min-h-[128px] flex-col gap-3 px-4 py-3", className)}>
        <div className="flex items-center justify-between gap-2">
          <div className="h-3 w-24 animate-pulse rounded bg-surface-2/70" />
          <div className="h-7 w-7 animate-pulse rounded-md bg-surface-2/70" />
        </div>
        <div className="h-8 w-20 animate-pulse rounded bg-surface-2/70" />
        <div className="h-3 w-28 animate-pulse rounded bg-surface-2/60" />
      </Card>
    );
  }

  return (
    <Card
      className={cn(
        "flex flex-col gap-3",
        onClick && "cursor-pointer hover:shadow-md",
        className,
      )}
      onClick={onClick}
    >
      <div className="flex items-center justify-between">
        <p className="font-mono text-[10px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">{title}</p>
        {icon ? (
          <span className={cn("inline-flex h-7 w-7 items-center justify-center rounded-md border", toneIconShellClass[tone])}>
            {icon}
          </span>
        ) : null}
      </div>
      <div className="font-mono text-2xl font-semibold leading-tight text-foreground">{value}</div>
      {detail ? <div className="text-[11px] text-muted-foreground">{detail}</div> : null}
    </Card>
  );
}
