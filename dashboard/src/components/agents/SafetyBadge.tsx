import { cn } from "@/lib/utils";

/**
 * SafetyBadge renders a compact, color-coded governance-decision chip
 * (ALLOW / DENY / REQUIRE_APPROVAL / CONSTRAINED / THROTTLE). Extracted from
 * AgentDetailPage so the per-agent job table and the governance-decision
 * timeline (AgentDecisionsPanel) render decisions identically.
 *
 * Unknown decisions fall back to an upper-cased label (never throwing); an
 * empty decision renders a neutral em-dash so non-decision audit rows don't
 * show a blank chip.
 */
const SAFETY_BADGE_CONFIG: Record<
  string,
  { color: string; bg: string; label: string }
> = {
  allow: {
    color: "text-[var(--color-success)]",
    bg: "bg-[var(--color-success)]/10",
    label: "ALLOW",
  },
  deny: {
    color: "text-[var(--color-governance)]",
    bg: "bg-[var(--color-governance)]/10",
    label: "DENY",
  },
  require_approval: {
    color: "text-[var(--color-warning)]",
    bg: "bg-[var(--color-warning)]/10",
    label: "REQUIRE_APPROVAL",
  },
  allow_with_constraints: {
    color: "text-[var(--color-info)]",
    bg: "bg-[var(--color-info)]/10",
    label: "CONSTRAINED",
  },
  throttle: {
    color: "text-[var(--color-warning)]",
    bg: "bg-[var(--color-warning)]/10",
    label: "THROTTLE",
  },
};

export function SafetyBadge({
  decision,
  className,
}: {
  decision: string;
  className?: string;
}) {
  const c = SAFETY_BADGE_CONFIG[decision] ?? {
    color: "text-muted-foreground",
    bg: "bg-surface-2",
    label: decision ? decision.toUpperCase() : "—",
  };
  return (
    <span
      className={cn(
        "px-1.5 py-0.5 rounded font-mono text-xs font-semibold",
        c.color,
        c.bg,
        className,
      )}
    >
      {c.label}
    </span>
  );
}
