import { Wrench, Loader2, CheckCircle2, AlertTriangle } from "lucide-react";
import { formatRelative } from "../../lib/format";
import type { ActivityItem } from "../../types/activity";

const statusIcon = {
  pending: Loader2,
  running: Loader2,
  success: CheckCircle2,
  error: AlertTriangle,
};

type Props = { activity: ActivityItem };

export function ToolCallBlock({ activity }: Props) {
  const status = activity.payload?.tool_status ?? "pending";
  const StatusIcon = statusIcon[status] || Loader2;

  return (
    <div className="rounded-2xl border border-border bg-white/70 p-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <div className="flex h-8 w-8 items-center justify-center rounded-xl bg-accent/10 text-accent">
            <Wrench className="h-4 w-4" />
          </div>
          <div>
            <div className="text-sm font-semibold text-ink">{activity.payload?.tool_name || "Tool call"}</div>
            <div className="text-[10px] text-muted">{formatRelative(activity.timestamp)}</div>
          </div>
        </div>
        <div className="flex items-center gap-1 text-xs text-muted">
          <StatusIcon className={`h-3 w-3 ${status === "running" || status === "pending" ? "animate-spin" : ""}`} />
          <span className="capitalize">{status}</span>
        </div>
      </div>
      {activity.payload?.tool_inputs ? (
        <pre className="mt-3 rounded-xl bg-white/80 p-3 text-[11px] text-ink">
          {JSON.stringify(activity.payload.tool_inputs, null, 2)}
        </pre>
      ) : null}
    </div>
  );
}
