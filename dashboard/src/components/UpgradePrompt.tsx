import type { TierUsageMetric } from "@/api/types";
import { ArrowUpRight, AlertTriangle, Sparkles } from "lucide-react";
import { cn } from "@/lib/utils";

interface UpgradePromptProps {
  label: string;
  metric?: TierUsageMetric<number> | null;
  plan?: string | null;
  className?: string;
  href?: string;
}

export function shouldShowUpgradePrompt(
  metric?: TierUsageMetric<number> | null,
  threshold = 0.8,
): boolean {
  const allowed = metric?.allowed;
  if (typeof allowed !== "number" || !Number.isFinite(allowed) || allowed <= 0) {
    return false;
  }
  const current = typeof metric?.current === "number" ? metric.current : 0;
  return current / allowed >= threshold;
}

export function UpgradePrompt({
  label,
  metric,
  plan,
  className,
  href = "https://cordum.io/pricing",
}: UpgradePromptProps) {
  if (!shouldShowUpgradePrompt(metric)) {
    return null;
  }

  const current = typeof metric?.current === "number" ? metric.current : 0;
  const allowed = typeof metric?.allowed === "number" ? metric.allowed : 0;
  const atLimit = allowed > 0 && current >= allowed;
  const Icon = atLimit ? AlertTriangle : Sparkles;

  return (
    <div
      className={cn(
        "rounded-3xl border px-4 py-3",
        atLimit
          ? "border-destructive/25 bg-destructive/10"
          : "border-[var(--color-warning)]/25 bg-[var(--color-warning)]/10",
        className,
      )}
    >
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
            <Icon
              className={cn(
                "h-4 w-4 shrink-0",
                atLimit ? "text-destructive" : "text-[var(--color-warning)]",
              )}
            />
            {atLimit ? `${label} limit reached` : `${label} nearing its tier limit`}
          </div>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
            You are using {current.toLocaleString()} of {allowed.toLocaleString()} {label.toLowerCase()}
            {plan ? ` on ${plan}.` : "."} Upgrade before automation slows down.
          </p>
        </div>

        <a
          href={href}
          target="_blank"
          rel="noreferrer"
          className={cn(
            "inline-flex shrink-0 items-center gap-1.5 rounded-full px-3 py-2 text-xs font-medium transition-colors",
            atLimit
              ? "bg-destructive text-destructive-foreground hover:bg-destructive/90"
              : "border border-[var(--color-warning)]/25 text-foreground hover:bg-[var(--color-warning)]/15",
          )}
        >
          View pricing
          <ArrowUpRight className="h-3.5 w-3.5" />
        </a>
      </div>
    </div>
  );
}
