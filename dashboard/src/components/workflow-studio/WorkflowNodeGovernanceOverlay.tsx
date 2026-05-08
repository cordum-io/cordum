/**
 * WorkflowNodeGovernanceOverlay (task-dd5e1d8f, Phase 3 wk6)
 *
 * Three-indicator inline overlay for workflow nodes — design-time studio
 * (UnifiedNode) AND run-time execution view (RunDetailPage step list) share
 * this component so the visual contract stays identical across surfaces.
 *
 * Indicators (left → right):
 *   1. policy-gate icon — design-time hint of which gate guards the step
 *   2. safety-decision badge — runtime decision applied to the job/run-step
 *   3. audit hash chip — first 8 chars of the audit-chain hash for the event
 *
 * Graceful degradation: each indicator is optional. When the source data
 * is not available (the prop is `undefined`), the slot renders a muted
 * placeholder dash rather than disappearing — the component visibly carries
 * the contract for all three even when only one is populated.
 *
 * Data wiring:
 *   - safetyDecision: already shipped on `WorkflowRunStep.output.safetyDecision`
 *     and `UnifiedNodeData.safetyDecision`.
 *   - policyGate + auditHash: API additions tracked in **task-913b6c6c**
 *     (cordum-core: `WorkflowStep.policyGate`, `WorkflowRunStep.auditHash`).
 *     Until that lands, callers pass `undefined` for these slots and the
 *     overlay renders the muted "data pending" state.
 *
 * Design-time vs runtime: pass `runtime={true}` on RunDetailPage so the
 * decision badges render at full saturation; design-time defaults render
 * the same indicators muted to signal "this is the gate that WILL apply"
 * vs "this is the decision that WAS applied."
 */
import { Shield, ShieldAlert, ShieldCheck, ShieldQuestion } from "lucide-react";
import type { ReactNode } from "react";
import { CodeBlock } from "@/components/ui/CodeBlock";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";
import { cn } from "@/lib/utils";

export type PolicyGate = "allow" | "deny" | "require_approval";

export interface WorkflowNodeGovernanceOverlayProps {
  /** Design-time policy gate hint. `undefined` renders a muted placeholder. */
  policyGate?: PolicyGate;
  /** Runtime safety decision (already shipped on job/run records). */
  safetyDecision?: string;
  /** Audit-chain hash. Renders the first 8 chars as a copy-on-click chip. */
  auditHash?: string;
  /** When true, decision rendering is saturated; when false, muted. */
  runtime?: boolean;
  className?: string;
  /** Aria label override for the overlay group. */
  ariaLabel?: string;
}

const POLICY_GATE_ICON: Record<PolicyGate, { Icon: typeof Shield; tone: string; title: string }> = {
  allow: { Icon: ShieldCheck, tone: "text-[var(--color-success)]", title: "Policy gate: allow" },
  deny: { Icon: ShieldAlert, tone: "text-[var(--color-governance)]", title: "Policy gate: deny" },
  require_approval: { Icon: Shield, tone: "text-[var(--color-warning)]", title: "Policy gate: require approval" },
};

function PolicyGateIcon({ gate, runtime }: { gate?: PolicyGate; runtime: boolean }) {
  if (!gate) {
    return (
      <span
        className="inline-flex h-4 w-4 items-center justify-center text-muted-foreground/60"
        title="Policy gate: pending API (task-913b6c6c)"
        data-pending-api="task-913b6c6c"
        data-slot="policy-gate"
      >
        <ShieldQuestion className="h-3.5 w-3.5" aria-hidden="true" />
      </span>
    );
  }
  const { Icon, tone, title } = POLICY_GATE_ICON[gate];
  return (
    <span
      className={cn("inline-flex h-4 w-4 items-center justify-center", tone, !runtime && "opacity-60")}
      title={title}
      data-slot="policy-gate"
      data-policy-gate={gate}
    >
      <Icon className="h-3.5 w-3.5" aria-hidden="true" />
    </span>
  );
}

function AuditHashChip({ hash, runtime }: { hash?: string; runtime: boolean }) {
  if (!hash) {
    return (
      <span
        className="inline-flex h-4 items-center rounded font-mono text-[10px] tracking-tight px-1 text-muted-foreground/60 bg-surface-2/40"
        title="Audit hash: pending API (task-913b6c6c)"
        data-pending-api="task-913b6c6c"
        data-slot="audit-hash"
      >
        —
      </span>
    );
  }
  // The chip is the shared CodeBlock primitive in compact-inline form. The
  // wrapper span carries the data-slot + opacity + hover cursor + click stopPropagation
  // so the canvas drag-handler doesn't fire when the user copies the hash.
  return (
    <span
      data-slot="audit-hash"
      data-row-action
      onClick={(event) => event.stopPropagation()}
      className={cn("inline-flex", !runtime && "opacity-60")}
    >
      <CodeBlock
        inline
        copyable
        inlineMaxLength={8}
        ariaLabel={`Copy audit hash ${hash}`}
        inlineTitle={`Audit hash ${hash} — click to copy`}
      >
        {hash}
      </CodeBlock>
    </span>
  );
}

function SafetyDecisionSlot({
  decision,
  runtime,
}: {
  decision?: string;
  runtime: boolean;
}): ReactNode {
  if (!decision) {
    return (
      <span
        className="inline-flex h-4 items-center rounded font-mono text-[10px] tracking-wider px-1 text-muted-foreground/60 bg-surface-2/40"
        title="Safety decision: not yet applied"
        data-slot="safety-decision"
      >
        —
      </span>
    );
  }
  return (
    <span data-slot="safety-decision" aria-label={`Safety decision: ${decision}`}>
      <SafetyDecisionBadge
        decision={decision}
        className={cn("h-4 text-[10px] py-0", !runtime && "opacity-60")}
      />
    </span>
  );
}

export function WorkflowNodeGovernanceOverlay({
  policyGate,
  safetyDecision,
  auditHash,
  runtime = false,
  className,
  ariaLabel,
}: WorkflowNodeGovernanceOverlayProps) {
  return (
    <div
      role="group"
      aria-label={ariaLabel ?? "Governance indicators"}
      data-governance-overlay
      data-runtime={runtime ? "true" : "false"}
      className={cn(
        "inline-flex items-center gap-1 rounded-md bg-card/80 backdrop-blur-sm border border-border/60 px-1 py-0.5",
        className,
      )}
    >
      <PolicyGateIcon gate={policyGate} runtime={runtime} />
      <SafetyDecisionSlot decision={safetyDecision} runtime={runtime} />
      <AuditHashChip hash={auditHash} runtime={runtime} />
    </div>
  );
}
