import { useState } from "react";
import { Link } from "react-router-dom";
import { Check, ChevronDown, ChevronRight, X } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Card } from "../ui/Card";
import { cn } from "../../lib/utils";
import type { ExplainResult, ExplainRuleStep, ExplainCondition } from "../../hooks/usePolicies";

// ---------------------------------------------------------------------------
// Variant helpers
// ---------------------------------------------------------------------------

const decisionBadge: Record<string, "success" | "governance" | "warning" | "info" | "default"> = {
  allow: "success",
  deny: "governance",
  require_approval: "warning",
  throttle: "info",
};

const decisionBorder: Record<string, string> = {
  allow: "border-success",
  deny: "border-danger",
  require_approval: "border-warning",
  throttle: "border-info",
};

// ---------------------------------------------------------------------------
// Condition row
// ---------------------------------------------------------------------------

function ConditionRow({ condition }: { condition: ExplainCondition }) {
  return (
    <tr className={cn(!condition.passed && "bg-danger/5")}>
      <td className="px-3 py-1.5 font-mono text-xs text-ink">{condition.field}</td>
      <td className="px-3 py-1.5 text-xs text-muted-foreground">{condition.operator}</td>
      <td className="px-3 py-1.5 font-mono text-xs text-muted-foreground">{condition.expected}</td>
      <td className="px-3 py-1.5 font-mono text-xs text-muted-foreground">{condition.actual}</td>
      <td className="px-3 py-1.5 text-center">
        {condition.passed ? (
          <Check className="inline h-3.5 w-3.5 text-success" />
        ) : (
          <X className="inline h-3.5 w-3.5 text-danger" />
        )}
      </td>
    </tr>
  );
}

// ---------------------------------------------------------------------------
// Collapsible rule step
// ---------------------------------------------------------------------------

function RuleStepCard({ step, index }: { step: ExplainRuleStep; index: number }) {
  // Initial open state mirrors `step.matched` — matched rules start
  // expanded so the operator can inspect the decision inline. After
  // mount this becomes user-controlled collapse state; we intentionally
  // do not re-sync on prop change so a background refetch cannot
  // auto-close a panel the user just opened.
  const [open, setOpen] = useState(step.matched);

  return (
    <div
      className={cn(
        "rounded-xl border-2 transition-all",
        step.matched
          ? cn(decisionBorder[step.decision] ?? "border-accent", "shadow-sm")
          : "border-border opacity-70",
      )}
    >
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex w-full items-center justify-between px-4 py-3 text-left"
      >
        <div className="flex items-center gap-2">
          {open ? (
            <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
          )}
          <span className="font-mono text-xs text-muted-foreground">#{index + 1}</span>
          <span className="text-sm font-medium text-ink">{step.ruleId}</span>
          {step.ruleName && (
            <span className="text-xs text-muted-foreground">({step.ruleName})</span>
          )}
        </div>
        <Badge variant={step.matched ? (decisionBadge[step.decision] ?? "default") : "default"}>
          {step.matched ? "MATCH" : "skip"}
        </Badge>
      </button>

      {open && (
        <div className="border-t border-border px-4 py-3 space-y-2">
          {step.reason && (
            <p className="text-xs text-muted-foreground">{step.reason}</p>
          )}

          {step.conditions.length > 0 ? (
            <div className="overflow-x-auto rounded-lg border border-border">
              <table className="w-full text-xs">
                <thead>
                  <tr className="border-b border-border bg-surface2/50">
                    <th className="px-3 py-1.5 text-left font-semibold text-muted-foreground">Field</th>
                    <th className="px-3 py-1.5 text-left font-semibold text-muted-foreground">Operator</th>
                    <th className="px-3 py-1.5 text-left font-semibold text-muted-foreground">Expected</th>
                    <th className="px-3 py-1.5 text-left font-semibold text-muted-foreground">Actual</th>
                    <th className="px-3 py-1.5 text-center font-semibold text-muted-foreground">Result</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {step.conditions.map((cond, i) => (
                    <ConditionRow key={`${cond.field}-${i}`} condition={cond} />
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">No condition details available for this rule.</p>
          )}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// ExplainResultPanel
// ---------------------------------------------------------------------------

export function ExplainResultPanel({ result }: { result: ExplainResult }) {
  return (
    <div className="space-y-4">
      {/* Decision summary */}
      <Card className={cn("border-2", decisionBorder[result.decision] ?? "border-border")}>
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <h3 className="font-display text-lg font-semibold text-ink">Explain Result</h3>
            <Badge variant={decisionBadge[result.decision] ?? "default"} className="text-sm px-3 py-1">
              {result.decision.replace(/_/g, " ")}
            </Badge>
          </div>

          <div className="flex flex-wrap items-center gap-4 text-xs text-muted-foreground">
            {result.matchedRule && (
              <span>
                Matched rule:{" "}
                <Link
                  to={`/govern/overview?tab=input-rules&highlight=${encodeURIComponent(result.matchedRule)}`}
                  className="font-mono font-semibold text-accent hover:underline"
                >
                  {result.matchedRule}
                </Link>
              </span>
            )}
            {typeof result.evaluationTimeMs === "number" && (
              <span>Eval: <span className="font-mono">{result.evaluationTimeMs}ms</span></span>
            )}
            {result.policySnapshot && (
              <span>Snapshot: <span className="font-mono">{result.policySnapshot}</span></span>
            )}
          </div>

          {result.reason && (
            <p className="text-sm text-muted-foreground">{result.reason}</p>
          )}
        </div>
      </Card>

      {/* Evaluation chain */}
      {result.evaluationChain.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            Evaluation Chain ({result.evaluationChain.length} rules)
          </h3>
          {result.evaluationChain.map((step, i) => (
            <RuleStepCard key={step.ruleId} step={step} index={i} />
          ))}
        </div>
      )}

      {result.evaluationChain.length === 0 && (
        <Card>
          <p className="text-sm text-muted-foreground">
            No detailed evaluation chain was returned. The backend may not populate
            evaluation_path for this policy mode.
          </p>
        </Card>
      )}
    </div>
  );
}
