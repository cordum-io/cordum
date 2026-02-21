import { Gauge } from "lucide-react";
import { cn } from "../../lib/utils";

interface RateLimiterModeBadgeProps {
  mode?: string;
}

const MODES: Record<string, { label: string; variant: "distributed" | "local"; tooltip: string }> = {
  redis: {
    label: "Distributed",
    variant: "distributed",
    tooltip:
      "Rate limits are coordinated across all replicas via Redis. Limits are shared and enforced globally.",
  },
  memory: {
    label: "Local",
    variant: "local",
    tooltip:
      "Rate limits are applied per-replica in-memory. Each replica enforces its own counters independently — effective limits multiply with replica count.",
  },
};

export function RateLimiterModeBadge({ mode }: RateLimiterModeBadgeProps) {
  if (!mode) return null;

  const key = mode.toLowerCase() === "redis" ? "redis" : "memory";
  const info = MODES[key];

  return (
    <div
      className={cn(
        "inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-xs",
        key === "redis"
          ? "border-success/30 bg-success/5 text-success"
          : "border-warning/30 bg-warning/5 text-warning",
      )}
      title={info.tooltip}
    >
      <Gauge className="h-3 w-3" />
      <span>Rate Limiting: {info.label}</span>
    </div>
  );
}
