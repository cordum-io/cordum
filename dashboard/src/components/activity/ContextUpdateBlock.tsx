import { Database } from "lucide-react";
import { formatRelative } from "../../lib/format";
import type { ActivityItem } from "../../types/activity";

type Props = { activity: ActivityItem };

export function ContextUpdateBlock({ activity }: Props) {
  return (
    <div className="rounded-2xl border border-border bg-white/80 p-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">
          <Database className="h-3 w-3" />
          Context update
        </div>
        <span className="text-[10px] text-muted">{formatRelative(activity.timestamp)}</span>
      </div>
      <div className="mt-2 text-sm text-ink">
        {activity.content}
      </div>
      {activity.payload?.memory_key ? (
        <div className="mt-2 text-[10px] text-muted">
          {activity.payload.memory_operation || "update"} · {activity.payload.memory_key}
        </div>
      ) : null}
    </div>
  );
}
