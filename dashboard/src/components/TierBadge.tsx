import { cn } from "@/lib/utils";

export type ResolvedLicensePlan = "community" | "team" | "enterprise";

interface TierBadgeProps {
  plan?: string | null;
  className?: string;
}

const PLAN_STYLES: Record<ResolvedLicensePlan, { label: string; className: string }> = {
  community: {
    label: "Community",
    className: "border border-border bg-surface-2 text-muted-foreground",
  },
  team: {
    label: "Team",
    className: "border border-sky-500/20 bg-sky-500/10 text-sky-300",
  },
  enterprise: {
    label: "Enterprise",
    className: "border border-violet-500/20 bg-violet-500/12 text-violet-300",
  },
};

export function normalizeLicensePlan(plan?: string | null): ResolvedLicensePlan {
  const normalized = (plan ?? "").trim().toLowerCase();
  if (normalized.includes("enterprise")) return "enterprise";
  if (normalized.includes("team")) return "team";
  return "community";
}

export function TierBadge({ plan, className }: TierBadgeProps) {
  const resolvedPlan = normalizeLicensePlan(plan);
  const meta = PLAN_STYLES[resolvedPlan];

  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-3 py-1 text-xs font-semibold tracking-wide",
        meta.className,
        className,
      )}
    >
      {meta.label}
    </span>
  );
}
