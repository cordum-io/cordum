import { GitCommit } from "lucide-react";
import { formatRelative } from "../../lib/format";
import type { ActivityItem } from "../../types/activity";

type Props = { activity: ActivityItem };

export function StateChangeBlock({ activity }: Props) {
  const stepLabel = activity.metadata?.step_id ? `Step ${activity.metadata.step_id}` : "State change";

  return (
    <div className="rounded-2xl border border-border bg-card/80 p-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <GitCommit className="h-3 w-3 text-accent" />
          <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">{stepLabel}</div>
        </div>
        <span className="text-[10px] text-muted-foreground">{formatRelative(activity.timestamp)}</span>
      </div>
      <div className="mt-2 text-sm text-ink">
        {activity.content}
      </div>
      {(activity.metadata?.job_id || activity.payload?.to_step) && (
        <div className="mt-2 flex flex-wrap gap-2 text-[10px] text-muted-foreground">
          {activity.payload?.from_step && activity.payload?.to_step ? (
            <span>{activity.payload.from_step} → {activity.payload.to_step}</span>
          ) : null}
          {activity.metadata?.job_id ? <span>Job {activity.metadata.job_id.slice(0, 8)}</span> : null}
        </div>
      )}
    </div>
  );
}
