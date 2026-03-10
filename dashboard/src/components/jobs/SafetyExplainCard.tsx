import { ShieldCheck } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Card } from "../ui/Card";
import { cn } from "../../lib/utils";
import type { SafetyDecision } from "../../api/types";

// ---------------------------------------------------------------------------
// Variant mapping
// ---------------------------------------------------------------------------

const decisionVariant: Record<string, "success" | "danger" | "warning" | "info"> = {
  allow: "success",
  deny: "danger",
  require_approval: "warning",
  throttle: "info",
};

const borderColor: Record<string, string> = {
  allow: "border-l-success",
  deny: "border-l-danger",
  require_approval: "border-l-warning",
  throttle: "border-l-accent",
};

// ---------------------------------------------------------------------------
// SafetyExplainCard
// ---------------------------------------------------------------------------

export function SafetyExplainCard({ decision }: { decision: SafetyDecision }) {
  return (
    <Card className={cn("border-l-4", borderColor[decision.type] ?? "border-l-border")}>
      <div className="flex items-start gap-3">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-surface2">
          <ShieldCheck className="h-5 w-5 text-muted-foreground" />
        </div>
        <div className="flex-1 space-y-3">
          {/* Decision badge */}
          <div className="flex items-center gap-2">
            <Badge variant={decisionVariant[decision.type] ?? "default"} className="text-sm px-3 py-1">
              {decision.type.replace(/_/g, " ")}
            </Badge>
            {typeof decision.evalTimeMs === "number" && (
              <span className="text-xs text-muted-foreground font-mono">{decision.evalTimeMs}ms</span>
            )}
          </div>

          {/* Matched rule */}
          {decision.matchedRule && (
            <div>
              <dt className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                Matched Rule
              </dt>
              <dd className="mt-0.5 text-sm text-ink font-mono">{decision.matchedRule}</dd>
            </div>
          )}

          {/* Reason */}
          {decision.reason && (
            <div>
              <dt className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                Reason
              </dt>
              <dd className="mt-0.5 text-sm text-ink">{decision.reason}</dd>
            </div>
          )}

          {/* Eval path */}
          {decision.evalPath && decision.evalPath.length > 0 && (
            <div>
              <dt className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                Evaluation Path
              </dt>
              <dd className="mt-1">
                <ol className="space-y-1">
                  {decision.evalPath.map((rule, i) => (
                    <li key={i} className="flex items-center gap-2 text-xs text-muted-foreground">
                      <span className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-surface2 text-[9px] font-bold">
                        {i + 1}
                      </span>
                      <span className="font-mono">{rule}</span>
                    </li>
                  ))}
                </ol>
              </dd>
            </div>
          )}
        </div>
      </div>
    </Card>
  );
}
