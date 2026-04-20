import {
  Bot,
  Gauge,
  ShieldAlert,
  ShieldCheck,
  ShieldX,
  Sparkles,
} from "lucide-react";
import { motion, useReducedMotion } from "framer-motion";
import type { GovernanceDecision } from "@/api/types";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { CollapsibleSection } from "@/components/ui/CollapsibleSection";
import { cn, formatRelativeTime } from "@/lib/utils";
import { ApprovalBadge } from "./ApprovalBadge";
import { ConstraintsPanel } from "./ConstraintsPanel";

interface DecisionNodeProps {
  decision: GovernanceDecision;
  index: number;
  isLast?: boolean;
}

type VerdictTone = {
  icon: typeof ShieldCheck;
  leadingClassName: string;
  mobileAccentClassName: string;
  badgeDecision: string;
  label: string;
};

function verdictTone(verdict: GovernanceDecision["verdict"]): VerdictTone {
  switch (verdict) {
    case "allow":
      return {
        icon: ShieldCheck,
        leadingClassName:
          "border-[var(--color-success)]/30 bg-[var(--color-success)]/10 text-[var(--color-success)]",
        mobileAccentClassName: "max-sm:border-[var(--color-success)]",
        badgeDecision: "allow",
        label: "Allow",
      };
    case "constrain":
      return {
        icon: Sparkles,
        leadingClassName:
          "border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 text-[var(--color-warning)]",
        mobileAccentClassName: "max-sm:border-[var(--color-warning)]",
        badgeDecision: "constrain",
        label: "Constrain",
      };
    case "require_approval":
      return {
        icon: ShieldAlert,
        leadingClassName:
          "border-[var(--color-violet-500)]/30 bg-[var(--color-violet-500)]/10 text-[var(--color-violet-500)]",
        mobileAccentClassName: "max-sm:border-[var(--color-violet-500)]",
        badgeDecision: "require_approval",
        label: "Require approval",
      };
    case "throttle":
      return {
        icon: Gauge,
        leadingClassName:
          "border-border bg-surface-2 text-muted-foreground",
        mobileAccentClassName: "max-sm:border-border",
        badgeDecision: "throttle",
        label: "Throttle",
      };
    case "deny":
    default:
      return {
        icon: ShieldX,
        leadingClassName:
          "border-[var(--color-governance)]/30 bg-[var(--color-governance)]/10 text-[var(--color-governance)]",
        mobileAccentClassName: "max-sm:border-[var(--color-governance)]",
        badgeDecision: "deny",
        label: "Deny",
      };
  }
}

export function DecisionNode({
  decision,
  index,
  isLast = false,
}: DecisionNodeProps) {
  const tone = verdictTone(decision.verdict);
  const Icon = tone.icon;
  const decisionTitle = decision.ruleName ?? decision.matchedRule ?? "Matched rule";
  const prefersReducedMotion = useReducedMotion();

  function handleKeyDown(event: React.KeyboardEvent<HTMLLIElement>) {
    const current = event.currentTarget;
    if (event.key === "ArrowDown") {
      const next = current.nextElementSibling;
      if (next instanceof HTMLElement) {
        event.preventDefault();
        next.focus();
      }
    }
    if (event.key === "ArrowUp") {
      const previous = current.previousElementSibling;
      if (previous instanceof HTMLElement) {
        event.preventDefault();
        previous.focus();
      }
    }
  }

  return (
    <motion.li
      tabIndex={0}
      initial={prefersReducedMotion ? false : { opacity: 0, y: 12 }}
      animate={prefersReducedMotion ? undefined : { opacity: 1, y: 0 }}
      transition={
        prefersReducedMotion
          ? undefined
          : { delay: Math.min(index, 20) * 0.04, duration: 0.24 }
      }
      className="relative outline-none focus-visible:ring-2 focus-visible:ring-cordum/40 focus-visible:ring-offset-2 focus-visible:ring-offset-background"
      aria-labelledby={`governance-rule-${index}`}
      onKeyDown={handleKeyDown}
    >
      <div className="flex gap-4 max-sm:flex-col max-sm:gap-3">
        <div className="flex w-10 shrink-0 flex-col items-center max-sm:hidden">
          <span
            className={cn(
              "flex h-10 w-10 items-center justify-center rounded-full border shadow-soft",
              tone.leadingClassName,
            )}
            aria-hidden="true"
          >
            <Icon className="h-4 w-4" />
          </span>
          {!isLast && <span className="mt-2 h-full min-h-8 w-px bg-border" />}
        </div>

        <div
          className={cn(
            "min-w-0 flex-1 pb-6 max-sm:border-l-2 max-sm:pl-3",
            tone.mobileAccentClassName,
            isLast && "pb-0",
          )}
        >
          <CollapsibleSection
            title={decisionTitle}
            description={decision.topic}
            defaultOpen={index === 0}
            badge={
              <SafetyDecisionBadge
                decision={tone.badgeDecision}
                ariaLabel={`Verdict: ${decision.verdict}`}
              />
            }
            leading={
              <span
                className={cn(
                  "flex h-8 w-8 items-center justify-center rounded-full border",
                  tone.leadingClassName,
                )}
                aria-hidden="true"
              >
                <Icon className="h-3.5 w-3.5" />
              </span>
            }
            trailing={
              <ApprovalBadge
                status={decision.approvalStatus}
                decision={decision.approvalDecision}
              />
            }
            className="rounded-3xl border border-border bg-card px-4 py-3 shadow-soft"
            buttonClassName="mx-0 rounded-2xl px-0 py-0 hover:bg-transparent"
            contentClassName="space-y-3"
          >
            <div className="flex flex-wrap items-center gap-2">
              <span
                id={`governance-rule-${index}`}
                className="text-sm font-semibold text-foreground"
              >
                {decisionTitle}
              </span>
              <StatusBadge variant="muted">{tone.label}</StatusBadge>
              <StatusBadge variant="muted">
                {formatRelativeTime(decision.timestamp)}
              </StatusBadge>
            </div>

            <p className="text-sm leading-relaxed text-foreground">
              {decision.reason || "No policy rationale was recorded for this decision."}
            </p>

            <div className="flex flex-wrap gap-2">
              <StatusBadge variant="muted">{decision.topic}</StatusBadge>
              <StatusBadge variant="muted">{decision.matchedRule}</StatusBadge>
              {decision.policyVersion && (
                <StatusBadge variant="muted">
                  Policy {decision.policyVersion}
                </StatusBadge>
              )}
              {decision.agentId && (
                <StatusBadge variant="muted">
                  <Bot className="h-3 w-3" />
                  {decision.agentId}
                </StatusBadge>
              )}
            </div>

            <ConstraintsPanel constraints={decision.constraints} />
          </CollapsibleSection>
        </div>
      </div>
    </motion.li>
  );
}
