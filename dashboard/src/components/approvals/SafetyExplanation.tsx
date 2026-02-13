import { Link } from "react-router-dom";
import { ShieldCheck, ShieldAlert, ChevronRight } from "lucide-react";
import { Badge } from "../ui/Badge";
import { cn } from "../../lib/utils";
import type { SafetyDecision } from "../../api/types";

// ---------------------------------------------------------------------------
// Decision badge variant
// ---------------------------------------------------------------------------

const decisionVariant: Record<string, "success" | "warning" | "danger" | "info"> = {
  allow: "success",
  deny: "danger",
  require_approval: "warning",
  throttle: "info",
};

const decisionLabel: Record<string, string> = {
  allow: "Allow",
  deny: "Deny",
  require_approval: "Require Approval",
  throttle: "Throttle",
};

// ---------------------------------------------------------------------------
// SafetyExplanation
// ---------------------------------------------------------------------------

interface SafetyExplanationProps {
  policyRule?: string;
  reason?: string;
  riskTags?: string[];
  capabilities?: string[];
  safetyDecision?: SafetyDecision;
}

export function SafetyExplanation({
  policyRule,
  reason,
  riskTags,
  capabilities,
  safetyDecision,
}: SafetyExplanationProps) {
  const hasData = policyRule || reason || safetyDecision || (riskTags?.length ?? 0) > 0;

  if (!hasData) {
    return <p className="text-xs text-muted">No safety evaluation data available.</p>;
  }

  const matchedRule = policyRule || safetyDecision?.matchedRule;
  const decisionType = safetyDecision?.type ?? "require_approval";
  const decisionReason = reason || safetyDecision?.reason;

  return (
    <div className="space-y-3">
      {/* Matched rule + decision */}
      <div className="flex items-center gap-2 flex-wrap">
        {matchedRule && (
          <Link
            to={`/policies?rule=${encodeURIComponent(matchedRule)}`}
            className="inline-flex items-center gap-1.5 font-mono text-xs bg-surface2 px-2.5 py-1 rounded-md text-accent hover:underline"
          >
            <ShieldCheck className="h-3.5 w-3.5" />
            {matchedRule}
          </Link>
        )}
        <Badge variant={decisionVariant[decisionType] ?? "warning"}>
          {decisionLabel[decisionType] ?? decisionType}
        </Badge>
        {safetyDecision?.evalTimeMs != null && (
          <span className="text-[10px] text-muted font-mono">
            {safetyDecision.evalTimeMs}ms
          </span>
        )}
      </div>

      {/* Reason */}
      {decisionReason && (
        <p className="text-xs text-muted">{decisionReason}</p>
      )}

      {/* Matching criteria */}
      {((capabilities?.length ?? 0) > 0 || (riskTags?.length ?? 0) > 0) && (
        <div className="space-y-1.5 text-xs">
          {(capabilities?.length ?? 0) > 0 && (
            <div className="flex items-center gap-1.5 flex-wrap">
              <span className="text-muted">Capabilities:</span>
              {capabilities?.map((c) => (
                <Badge key={c} variant="info">{c}</Badge>
              ))}
            </div>
          )}
          {(riskTags?.length ?? 0) > 0 && (
            <div className="flex items-center gap-1.5 flex-wrap">
              <span className="text-muted">Risk tags:</span>
              {riskTags?.map((t) => (
                <Badge key={t} variant="warning">{t}</Badge>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Evaluation path (if available) */}
      {safetyDecision?.evalPath && safetyDecision.evalPath.length > 0 && (
        <div className="space-y-1">
          <p className="text-[10px] font-semibold uppercase tracking-wide text-muted">
            Evaluation Path
          </p>
          <ol className="space-y-0.5">
            {safetyDecision.evalPath.map((step, i) => {
              const isMatch = step.toLowerCase().includes("match");
              const isSkip = step.toLowerCase().includes("skip");
              return (
                <li
                  key={i}
                  className={cn(
                    "flex items-center gap-1.5 text-xs font-mono",
                    isMatch && "text-warning font-medium",
                    isSkip && "text-muted",
                    !isMatch && !isSkip && "text-muted",
                  )}
                >
                  <ChevronRight className="h-3 w-3 shrink-0" />
                  {step}
                </li>
              );
            })}
          </ol>
        </div>
      )}
    </div>
  );
}
