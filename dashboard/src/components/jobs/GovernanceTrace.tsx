import { useMemo } from "react";
import { Link } from "react-router-dom";
import { ChevronRight } from "lucide-react";
import { cn } from "../../lib/utils";
import type { Job, SafetyDecision } from "../../api/types";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface GovernanceTraceProps {
  job: Job;
  decision?: SafetyDecision;
}

interface Pill {
  key: string;
  label: string;
  href?: string;
  active?: boolean;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function GovernanceTrace({ job, decision }: GovernanceTraceProps) {
  const pills = useMemo(() => {
    const result: Pill[] = [];

    // 1. Action — always visible, current page (no link)
    result.push({
      key: "action",
      label: `Action ${job.id.slice(0, 8)}`,
      active: true,
    });

    // 2. Workflow Run — only if workflowRunId exists
    if (job.workflowRunId) {
      result.push({
        key: "workflow",
        label: `Run ${job.workflowRunId.slice(0, 8)}`,
        href: `/runs/${job.workflowRunId}`,
      });
    }

    // 3. Rule — only if decision has matchedRule
    if (decision?.matchedRule) {
      result.push({
        key: "rule",
        label: `Rule ${decision.matchedRule}`,
        href: `/policies/rules#${decision.matchedRule}`,
      });
    }

    // 4. Approval — only if approvalRef exists
    if (job.approvalRef) {
      result.push({
        key: "approval",
        label: "Approval",
        href: `/approvals?id=${job.approvalRef}`,
      });
    }

    // 5. Audit Trail — only if traceId exists
    if (job.traceId) {
      result.push({
        key: "audit",
        label: "Audit Trail",
        href: `/audit?correlationId=${job.traceId}`,
      });
    }

    return result;
  }, [job, decision]);

  if (pills.length === 0) return null;

  return (
    <div className="flex items-center gap-1.5 rounded-md border border-border bg-surface1 px-3 py-2">
      {pills.map((pill, i) => (
        <div key={pill.key} className="flex items-center gap-1.5">
          {i > 0 && (
            <ChevronRight size={14} className="shrink-0 text-muted-foreground" />
          )}
          {pill.href ? (
            <Link
              to={pill.href}
              className={cn(
                "rounded bg-surface2 px-2 py-1 font-mono text-xs transition-colors",
                "text-ink hover:bg-surface3",
              )}
            >
              {pill.label}
            </Link>
          ) : (
            <span
              className={cn(
                "rounded bg-surface2 px-2 py-1 font-mono text-xs",
                pill.active && "ring-1 ring-[hsl(var(--accent))]/30",
              )}
            >
              {pill.label}
            </span>
          )}
        </div>
      ))}
    </div>
  );
}
