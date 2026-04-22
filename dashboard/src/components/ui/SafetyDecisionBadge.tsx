import { cn } from "@/lib/utils";

type SafetyDecisionType =
  | "allow"
  | "deny"
  | "require_approval"
  | "allow_with_constraints"
  | "constrain"
  | "throttle"
  | "redact"
  | "monitor"
  | "quarantine";

const decisionConfig: Record<string, { color: string; bg: string; label: string }> = {
  allow: { color: "text-[var(--color-success)]", bg: "bg-[var(--color-success)]/10", label: "ALLOW" },
  deny: { color: "text-[var(--color-governance)]", bg: "bg-[var(--color-governance)]/10", label: "DENY" },
  require_approval: { color: "text-[var(--color-violet-500)]", bg: "bg-[var(--color-violet-500)]/10", label: "APPROVAL" },
  allow_with_constraints: { color: "text-[var(--color-warning)]", bg: "bg-[var(--color-warning)]/10", label: "CONSTRAINED" },
  constrain: { color: "text-[var(--color-warning)]", bg: "bg-[var(--color-warning)]/10", label: "CONSTRAINED" },
  throttle: { color: "text-muted-foreground", bg: "bg-surface-2", label: "THROTTLE" },
  redact: { color: "text-primary", bg: "bg-primary/10", label: "REDACT" },
  monitor: { color: "text-[var(--color-info)]", bg: "bg-[var(--color-info)]/10", label: "MONITOR" },
  quarantine: { color: "text-destructive", bg: "bg-destructive/10", label: "QUARANTINE" },
};

interface SafetyDecisionBadgeProps {
  decision?: string;
  matchedRules?: string[];
  className?: string;
  ariaLabel?: string;
}

export function SafetyDecisionBadge({
  decision,
  matchedRules,
  className,
  ariaLabel,
}: SafetyDecisionBadgeProps) {
  const c = decisionConfig[decision as SafetyDecisionType] ?? {
    color: "text-muted-foreground",
    bg: "bg-surface-2",
    label: decision?.toUpperCase() || "\u2014",
  };

  return (
    <span
      aria-label={ariaLabel}
      className={cn(
        "group relative inline-flex items-center gap-1 px-2 py-0.5 rounded font-mono text-xs font-semibold tracking-wider cursor-default",
        c.color,
        c.bg,
        className,
      )}
    >
      {c.label}
      {matchedRules && matchedRules.length > 0 && (
        <span className="absolute bottom-full left-1/2 -translate-x-1/2 mb-2 hidden group-hover:block z-50 min-w-[180px]">
          <span className="block bg-surface-3 border border-border rounded-lg p-2 shadow-xl text-xs text-muted-foreground font-normal tracking-normal">
            <span className="block text-foreground font-semibold mb-1">
              {matchedRules.length} matched rule{matchedRules.length > 1 ? "s" : ""}
            </span>
            {matchedRules.map((r, i) => (
              <span key={i} className="block truncate">{r}</span>
            ))}
          </span>
        </span>
      )}
    </span>
  );
}
