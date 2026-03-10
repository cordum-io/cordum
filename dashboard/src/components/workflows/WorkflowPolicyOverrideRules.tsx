import { Shield } from "lucide-react";
import { StatusBadge } from "@/components/ui/StatusBadge";
import type { SafetyDecisionType } from "@/api/types";

export interface WorkflowScopedRule {
  id: string;
  decision: SafetyDecisionType;
  description?: string;
  topics?: string[];
  capabilities?: string[];
}

export interface WorkflowPolicyOverrideRulesProps {
  rules: WorkflowScopedRule[];
}

function decisionVariant(d: SafetyDecisionType): "healthy" | "danger" | "warning" | "info" | "muted" {
  switch (d) {
    case "allow":
      return "healthy";
    case "deny":
      return "danger";
    case "require_approval":
      return "info";
    case "allow_with_constraints":
      return "warning";
    case "throttle":
      return "warning";
    default:
      return "muted";
  }
}

/**
 * Extract workflow-scoped rule overrides from config/metadata.
 * Returns an empty array when no workflow-level rules are defined.
 */
export function extractWorkflowRules(
  config?: Record<string, unknown> | null,
  metadata?: Record<string, unknown> | null,
): WorkflowScopedRule[] {
  const raw =
    (config?.policy_rules as unknown[] | undefined) ??
    (config?.rules as unknown[] | undefined) ??
    (metadata?.policy_rules as unknown[] | undefined) ??
    null;
  if (!Array.isArray(raw)) return [];
  return raw
    .filter((r): r is Record<string, unknown> => r !== null && typeof r === "object")
    .map((r) => ({
      id: String(r.id ?? r.rule_id ?? ""),
      decision: (r.decision ?? "allow") as SafetyDecisionType,
      description: r.description as string | undefined,
      topics: Array.isArray(r.topics) ? (r.topics as string[]) : undefined,
      capabilities: Array.isArray(r.capabilities) ? (r.capabilities as string[]) : undefined,
    }))
    .filter((r) => r.id !== "");
}

export function WorkflowPolicyOverrideRules({ rules }: WorkflowPolicyOverrideRulesProps) {
  if (rules.length === 0) {
    return (
      <div className="instrument-card">
        <div className="flex items-center gap-2 mb-2">
          <Shield className="w-4 h-4 text-cordum" />
          <h3 className="font-display font-semibold text-sm text-foreground">Workflow-Scoped Rules</h3>
        </div>
        <p className="text-xs text-muted-foreground">
          No workflow-specific rule overrides. All jobs inherit global policy rules.
          Use the simulator to see merged evaluation results for specific job contexts.
        </p>
      </div>
    );
  }

  return (
    <div className="instrument-card">
      <div className="flex items-center gap-2 mb-4">
        <Shield className="w-4 h-4 text-cordum" />
        <h3 className="font-display font-semibold text-sm text-foreground">Workflow-Scoped Rules</h3>
        <span className="text-[10px] font-mono text-muted-foreground ml-auto">
          {rules.length} rule{rules.length !== 1 ? "s" : ""}
        </span>
      </div>
      <p className="text-xs text-muted-foreground mb-3">
        These rules apply only to jobs dispatched by this workflow. They are merged with global rules during evaluation; the simulator shows the combined result.
      </p>
      <div className="space-y-2">
        {rules.map((rule) => (
          <div key={rule.id} className="rounded-lg bg-surface-0 border border-border p-3">
            <div className="flex items-center gap-2 mb-1">
              <span className="text-xs font-mono font-medium text-foreground">{rule.id}</span>
              <StatusBadge variant={decisionVariant(rule.decision)}>{rule.decision}</StatusBadge>
            </div>
            {rule.description && (
              <p className="text-[11px] text-muted-foreground">{rule.description}</p>
            )}
            {rule.topics && rule.topics.length > 0 && (
              <div className="flex items-center gap-1 mt-1">
                <span className="text-[10px] text-muted-foreground">topics:</span>
                {rule.topics.map((t) => (
                  <span key={t} className="text-[10px] font-mono px-1.5 py-0.5 rounded-full bg-surface-2 border border-border text-muted-foreground">{t}</span>
                ))}
              </div>
            )}
            {rule.capabilities && rule.capabilities.length > 0 && (
              <div className="flex items-center gap-1 mt-1">
                <span className="text-[10px] text-muted-foreground">capabilities:</span>
                {rule.capabilities.map((c) => (
                  <span key={c} className="text-[10px] font-mono px-1.5 py-0.5 rounded-full bg-surface-2 border border-border text-muted-foreground">{c}</span>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
