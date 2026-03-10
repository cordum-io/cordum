import { Check, X } from "lucide-react";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import type { ExplainRuleStep, ExplainCondition } from "@/hooks/usePolicies";
import { getDecisionDisplayVariant } from "./SimulatorDecisionSummary";

interface SimulatorResultsChainProps {
  chain: ExplainRuleStep[];
}

/** @internal exported for testing */
export function getConditionIcon(passed: boolean) {
  return passed
    ? <Check className="w-3 h-3 text-[var(--color-success)] shrink-0" />
    : <X className="w-3 h-3 text-destructive shrink-0" />;
}

function ConditionRow({ condition }: { condition: ExplainCondition }) {
  return (
    <div className="flex items-start gap-2 text-[11px] py-0.5">
      {getConditionIcon(condition.passed)}
      <span className="font-mono text-muted-foreground min-w-[80px]">
        {condition.field}
      </span>
      <span className="text-muted-foreground/60">{condition.operator}</span>
      <span className={condition.passed ? "text-[var(--color-success)]/80" : "text-destructive/80"}>
        {condition.expected}
      </span>
      <span className="text-muted-foreground/40 ml-auto">
        (got: {condition.actual})
      </span>
    </div>
  );
}

function ChainStep({
  step,
  index,
  isLast,
}: {
  step: ExplainRuleStep;
  index: number;
  isLast: boolean;
}) {
  return (
    <div className="relative pl-6">
      {/* Timeline connector */}
      <div className={cn("absolute left-[9px] top-0 bottom-0 w-px bg-border/40", index === 0 && "top-3")} />
      <div
        className={`absolute left-[4px] top-[6px] w-[11px] h-[11px] rounded-full border-2 z-10 ${
          step.matched
            ? "border-cordum bg-cordum/30 glow-cordum"
            : "border-border bg-surface-2"
        }`}
      />

      <div className={`pb-5 ${isLast ? "pb-0" : ""}`}>
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-[10px] font-mono text-muted-foreground/50">
            #{index + 1}
          </span>
          <span className="text-xs font-mono font-medium text-foreground">
            {step.ruleName || step.ruleId}
          </span>
          <StatusBadge variant={step.matched ? getDecisionDisplayVariant(step.decision) : "muted"}>
            {step.matched ? step.decision : "skipped"}
          </StatusBadge>
        </div>

        {step.reason && (
          <p className="text-[11px] text-muted-foreground mt-1.5 italic leading-relaxed">{step.reason}</p>
        )}

        {step.conditions.length > 0 && (
          <div className="mt-2.5 space-y-0.5 surface-inset p-2.5">
            {step.conditions.map((cond, i) => (
              <ConditionRow key={`${cond.field}-${i}`} condition={cond} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

import { cn } from "@/lib/utils";

export function SimulatorResultsChain({ chain }: SimulatorResultsChainProps) {
  if (chain.length === 0) {
    return (
      <InstrumentCard accent="muted" className="text-center py-8">
        <p className="text-xs text-muted-foreground italic">
          No evaluation chain returned. The policy engine may have used a direct decision path.
        </p>
      </InstrumentCard>
    );
  }

  return (
    <InstrumentCard>
      <h3 className="text-sm font-display font-semibold text-foreground mb-5 tracking-tight">
        Evaluation chain
      </h3>
      <div>
        {chain.map((step, i) => (
          <ChainStep
            key={`${step.ruleId}-${i}`}
            step={step}
            index={i}
            isLast={i === chain.length - 1}
          />
        ))}
      </div>
    </InstrumentCard>
  );
}
