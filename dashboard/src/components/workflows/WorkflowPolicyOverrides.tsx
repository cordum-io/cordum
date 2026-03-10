import { Shield } from "lucide-react";
import type { PolicyConstraints } from "@/api/types";
import { WorkflowPolicyOverridesBudgets } from "./WorkflowPolicyOverridesBudgets";
import { WorkflowPolicyOverridesSandbox } from "./WorkflowPolicyOverridesSandbox";
import { WorkflowPolicyOverridesToolchain } from "./WorkflowPolicyOverridesToolchain";
import { WorkflowPolicyOverridesDiff } from "./WorkflowPolicyOverridesDiff";

export interface WorkflowPolicyOverridesProps {
  constraints: PolicyConstraints | null;
  readOnly: boolean;
  onChange?: (next: PolicyConstraints) => void;
}

/**
 * Extract PolicyConstraints from a workflow's config or metadata bag.
 * Returns null when no constraints are present.
 */
export function extractConstraints(
  config?: Record<string, unknown> | null,
  metadata?: Record<string, unknown> | null,
): PolicyConstraints | null {
  const raw =
    (config?.constraints as Record<string, unknown> | undefined) ??
    (config?.policy_constraints as Record<string, unknown> | undefined) ??
    (metadata?.constraints as Record<string, unknown> | undefined) ??
    null;
  if (!raw || typeof raw !== "object") return null;
  return raw as PolicyConstraints;
}

/**
 * True when at least one constraint field is populated.
 */
export function hasAnyConstraints(c: PolicyConstraints | null): boolean {
  if (!c) return false;
  if (c.budgets && Object.keys(c.budgets).length > 0) return true;
  if (c.sandbox && Object.keys(c.sandbox).length > 0) return true;
  if (c.toolchain && Object.keys(c.toolchain).length > 0) return true;
  if (c.diff && Object.keys(c.diff).length > 0) return true;
  if (c.redaction_level) return true;
  return false;
}

export function WorkflowPolicyOverrides({ constraints, readOnly, onChange }: WorkflowPolicyOverridesProps) {
  const current = constraints ?? {};
  const hasData = hasAnyConstraints(constraints);

  const update = (patch: Partial<PolicyConstraints>) => {
    if (readOnly || !onChange) return;
    onChange({ ...current, ...patch });
  };

  return (
    <div className="instrument-card">
      <div className="flex items-center gap-2 mb-4">
        <Shield className="w-4 h-4 text-cordum" />
        <h3 className="font-display font-semibold text-sm text-foreground">Policy Overrides</h3>
      </div>
      <p className="text-xs text-muted-foreground mb-4">
        Workflow-scoped constraints applied to every job dispatched by this workflow.
        These override the global policy defaults for budget, sandbox, toolchain, and diff limits.
      </p>

      {!hasData && readOnly && (
        <p className="text-xs text-muted-foreground italic">
          No workflow-specific constraints configured. Jobs use global policy defaults.
        </p>
      )}

      <div className="space-y-4">
        <WorkflowPolicyOverridesBudgets
          budgets={current.budgets ?? null}
          readOnly={readOnly}
          onChange={(next) => update({ budgets: next })}
        />
        <WorkflowPolicyOverridesSandbox
          sandbox={current.sandbox ?? null}
          readOnly={readOnly}
          onChange={(next) => update({ sandbox: next })}
        />
        <WorkflowPolicyOverridesToolchain
          toolchain={current.toolchain ?? null}
          readOnly={readOnly}
          onChange={(next) => update({ toolchain: next })}
        />
        <WorkflowPolicyOverridesDiff
          diff={current.diff ?? null}
          readOnly={readOnly}
          onChange={(next) => update({ diff: next })}
        />
      </div>
    </div>
  );
}
