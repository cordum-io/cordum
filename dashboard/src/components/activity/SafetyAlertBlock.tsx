import { ShieldAlert } from "lucide-react";
import { cn } from "@/lib/utils";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { formatRelative } from "../../lib/format";
import { decisionVariant } from "@/lib/badgeVariants";
import type { ActivityItem } from "../../types/activity";

type Props = {
  activity: ActivityItem;
  onApprove?: (jobId: string) => void;
  onReject?: (jobId: string) => void;
};

export function SafetyAlertBlock({ activity, onApprove, onReject }: Props) {
  const decision = activity.payload?.decision || "PENDING";
  // Safety-alert UX: unknown decisions render as "info" rather than gray —
  // an alert block should never look like inert metadata.
  const computedVariant = decisionVariant(decision);
  const variant = computedVariant === "default" ? "info" : computedVariant;
  const jobId = activity.metadata?.job_id;
  const requiresAction = activity.payload?.requires_action && decision === "REQUIRE_APPROVAL";

  return (
    <div className={cn(
      "rounded-2xl border-l-4 p-4",
      decision === "DENY" ? "border-l-[var(--color-governance)] bg-[var(--color-governance)]/5" :
      decision === "REQUIRE_APPROVAL" ? "border-l-[var(--color-warning)] bg-[var(--color-warning)]/5" :
      "border-l-[var(--color-success)] bg-[var(--color-success)]/5"
    )}>
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <div className="flex h-8 w-8 items-center justify-center rounded-xl bg-warning/10 text-warning">
            <ShieldAlert className="h-4 w-4" />
          </div>
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-sm font-semibold text-ink">
                {activity.payload?.policy_name || "Safety check"}
              </span>
              <Badge variant={variant}>{decision.replace("_", " ")}</Badge>
            </div>
            <div className="mt-1 text-xs text-muted-foreground">{activity.content}</div>
          </div>
        </div>
        <span className="text-xs text-muted-foreground">{formatRelative(activity.timestamp)}</span>
      </div>
      {requiresAction && jobId ? (
        <div className="mt-3 flex flex-wrap gap-2">
          <Button variant="primary" size="sm" type="button" onClick={() => onApprove?.(jobId)}>
            Approve
          </Button>
          <Button variant="danger" size="sm" type="button" onClick={() => onReject?.(jobId)}>
            Deny
          </Button>
        </div>
      ) : null}
    </div>
  );
}
