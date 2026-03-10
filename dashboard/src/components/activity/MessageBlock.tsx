import { Bot, User, Info, ShieldCheck } from "lucide-react";
import { formatRelative } from "../../lib/format";
import type { ActivityItem } from "../../types/activity";

const roleConfig = {
  user: {
    icon: User,
    label: "You",
    containerClass: "ml-12 bg-accent/10 border-accent/20",
    iconClass: "bg-accent/20 text-accent",
  },
  agent: {
    icon: Bot,
    label: "Agent",
    containerClass: "mr-12 bg-card/80 border-border",
    iconClass: "bg-accent2/20 text-accent2",
  },
  system: {
    icon: Info,
    label: "System",
    containerClass: "mx-6 bg-warning/10 border-warning/20 italic",
    iconClass: "bg-warning/20 text-warning",
  },
  governance: {
    icon: ShieldCheck,
    label: "Governance",
    containerClass: "mx-6 bg-success/10 border-success/20",
    iconClass: "bg-success/20 text-success",
  },
};

type Props = {
  activity: ActivityItem;
};

export function MessageBlock({ activity }: Props) {
  const config = roleConfig[activity.role] ?? roleConfig.system;
  const Icon = config.icon;

  return (
    <div className={`rounded-2xl border p-4 transition-all duration-200 ${config.containerClass}`}>
      <div className="flex items-start gap-3">
        <div className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-xl ${config.iconClass}`}>
          <Icon className="h-4 w-4" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="mb-1 flex items-center justify-between gap-2">
            <span className="text-xs font-semibold uppercase tracking-wide text-ink">
              {activity.metadata?.step_id ? `${config.label} · ${activity.metadata.step_id}` : config.label}
            </span>
            <span className="text-[10px] text-muted-foreground">{formatRelative(activity.timestamp)}</span>
          </div>
          <div className="text-sm text-ink whitespace-pre-wrap break-words">{activity.content}</div>
          {(activity.metadata?.step_id || activity.metadata?.job_id) && (
            <div className="mt-2 flex flex-wrap gap-2">
              {activity.metadata?.step_id && (
                <span className="rounded-lg bg-accent/10 px-2 py-0.5 text-[10px] font-medium text-accent">
                  Step: {activity.metadata.step_id}
                </span>
              )}
              {activity.metadata?.job_id && (
                <span className="rounded-lg bg-muted/20 px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
                  Job: {activity.metadata.job_id.slice(0, 8)}
                </span>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
