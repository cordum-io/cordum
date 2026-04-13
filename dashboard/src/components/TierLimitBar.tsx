import type { ReactNode } from "react";
import type { TierUsageMetric } from "@/api/types";
import { Progress } from "@/components/ui/progress";
import { cn } from "@/lib/utils";

export interface TierLimitBarProps {
  label: string;
  metric?: TierUsageMetric<number> | null;
  detail?: ReactNode;
  className?: string;
  valueFormatter?: (value: number) => string;
}

type UsageTone = "healthy" | "warning" | "danger" | "idle";

export function usageRatio(metric?: TierUsageMetric<number> | null): number | null {
  const allowed = metric?.allowed;
  if (typeof allowed !== "number" || !Number.isFinite(allowed) || allowed <= 0) {
    return null;
  }
  const current = typeof metric?.current === "number" ? metric.current : 0;
  return current / allowed;
}

export function usageTone(metric?: TierUsageMetric<number> | null): UsageTone {
  const ratio = usageRatio(metric);
  if (ratio === null) return "idle";
  if (ratio >= 1) return "danger";
  if (ratio >= 0.8) return "warning";
  return "healthy";
}

function formatValue(value: number): string {
  return Number.isFinite(value) ? value.toLocaleString() : "—";
}

const TONE_STYLES: Record<UsageTone, { text: string; indicator: string }> = {
  healthy: {
    text: "text-cordum",
    indicator: "bg-cordum",
  },
  warning: {
    text: "text-[var(--color-warning)]",
    indicator: "bg-[var(--color-warning)]",
  },
  danger: {
    text: "text-destructive",
    indicator: "bg-destructive",
  },
  idle: {
    text: "text-muted-foreground",
    indicator: "bg-muted-foreground/40",
  },
};

export function TierLimitBar({
  label,
  metric,
  detail,
  className,
  valueFormatter = formatValue,
}: TierLimitBarProps) {
  const current =
    typeof metric?.current === "number" && Number.isFinite(metric.current)
      ? metric.current
      : 0;
  const allowed =
    typeof metric?.allowed === "number" && Number.isFinite(metric.allowed)
      ? metric.allowed
      : undefined;
  const ratio = usageRatio(metric);
  const tone = usageTone(metric);
  const percent = ratio === null ? 0 : Math.max(0, Math.min(100, ratio * 100));
  const styles = TONE_STYLES[tone];
  const summary =
    typeof allowed === "number"
      ? typeof metric?.current === "number"
        ? `${valueFormatter(current)} / ${valueFormatter(allowed)}`
        : `Limit ${valueFormatter(allowed)}`
      : "Limit unavailable";

  return (
    <div className={cn("rounded-3xl border border-border bg-surface-1/80 p-4", className)}>
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
            {label}
          </p>
          {detail && (
            <div className="mt-1 text-xs leading-relaxed text-muted-foreground">
              {detail}
            </div>
          )}
        </div>
        <span className={cn("shrink-0 text-xs font-mono font-semibold", styles.text)}>
          {summary}
        </span>
      </div>

      <div className="mt-3">
        <Progress
          value={percent}
          className="h-2 bg-surface-2"
          indicatorClassName={styles.indicator}
        />
      </div>
    </div>
  );
}
