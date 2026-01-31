import { ShieldAlert } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { formatRelative } from "../../lib/format";
import type { ActivityItem } from "../../types/activity";

const decisionVariant: Record<string, "success" | "danger" | "warning" | "info"> = {
  ALLOW: "success",
  DENY: "danger",
  REQUIRE_APPROVAL: "warning",
  CONSTRAIN: "info",
  PENDING: "info",
};

type Props = {
  activity: ActivityItem;
  onApprove?: (jobId: string) => void;
  onReject?: (jobId: string) => void;
};

export function SafetyAlertBlock({ activity, onApprove, onReject }: Props) {
  const decision = activity.payload?.decision || "PENDING";
  const variant = decisionVariant[decision] || "info";
  const jobId = activity.metadata?.job_id;
  const requiresAction = activity.payload?.requires_action && decision === "REQUIRE_APPROVAL";

  return (
    <div className="rounded-2xl border border-border bg-white/70 p-4">
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
            <div className="mt-1 text-xs text-muted">{activity.content}</div>
          </div>
        </div>
        <span className="text-[10px] text-muted">{formatRelative(activity.timestamp)}</span>
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
