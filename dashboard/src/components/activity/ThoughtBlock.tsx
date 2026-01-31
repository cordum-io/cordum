import { Brain } from "lucide-react";
import { formatRelative } from "../../lib/format";
import type { ActivityItem } from "../../types/activity";

type Props = { activity: ActivityItem };

export function ThoughtBlock({ activity }: Props) {
  return (
    <div className="rounded-2xl border border-border bg-white/80 p-4">
      <div className="flex items-center justify-between text-xs text-muted">
        <div className="flex items-center gap-2">
          <Brain className="h-3 w-3" />
          <span className="uppercase tracking-[0.2em]">Agent Thought</span>
        </div>
        <span>{formatRelative(activity.timestamp)}</span>
      </div>
      <div className="mt-2 text-sm text-muted italic whitespace-pre-wrap">{activity.content}</div>
    </div>
  );
}
