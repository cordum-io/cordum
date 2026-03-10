import { ShieldCheck, ShieldAlert, ShieldQuestion, Clock } from "lucide-react";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { InstrumentCard } from "@/components/ui/InstrumentCard";

interface SimulatorDecisionSummaryProps {
  decision: string;
  matchedRule?: string;
  reason?: string;
  evaluationTimeMs?: number;
}

export function getDecisionDisplayVariant(
  decision: string,
): "healthy" | "danger" | "warning" | "info" | "muted" {
  const normalized = decision.toLowerCase();
  if (normalized === "allow") return "healthy";
  if (normalized === "deny") return "danger";
  if (normalized === "quarantine") return "warning";
  if (normalized === "require_approval") return "info";
  if (normalized === "allow_with_constraints" || normalized === "throttle") return "info";
  return "muted";
}

function DecisionIcon({ decision }: { decision: string }) {
  const normalized = decision.toLowerCase();
  if (normalized === "allow" || normalized === "allow_with_constraints")
    return <ShieldCheck className="w-5 h-5 text-[var(--color-success)]" />;
  if (normalized === "deny")
    return <ShieldAlert className="w-5 h-5 text-destructive" />;
  return <ShieldQuestion className="w-5 h-5 text-[var(--color-warning)]" />;
}

export function SimulatorDecisionSummary({
  decision,
  matchedRule,
  reason,
  evaluationTimeMs,
}: SimulatorDecisionSummaryProps) {
  const variant = getDecisionDisplayVariant(decision);

  return (
    <InstrumentCard accent={variant as any} className="space-y-3">
      <div className="flex items-center gap-3">
        <DecisionIcon decision={decision} />
        <div className="flex-1 min-w-0">
          <p className="text-sm font-display font-semibold text-foreground">
            Final decision
          </p>
          <div className="flex items-center gap-2 mt-1">
            <StatusBadge variant={variant}>
              {decision}
            </StatusBadge>
            {matchedRule && (
              <span className="text-[11px] font-mono text-muted-foreground truncate">
                matched: {matchedRule}
              </span>
            )}
          </div>
        </div>
        {evaluationTimeMs !== undefined && (
          <div className="flex items-center gap-1 text-[10px] text-muted-foreground shrink-0">
            <Clock className="w-3 h-3" />
            {evaluationTimeMs}ms
          </div>
        )}
      </div>
      {reason && (
        <p className="text-xs text-muted-foreground border-t border-border/40 pt-3">
          {reason}
        </p>
      )}
    </InstrumentCard>
  );
}
