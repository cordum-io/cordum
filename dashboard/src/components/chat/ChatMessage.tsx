import { Bot, User, Info } from "lucide-react";
import { formatRelative } from "../../lib/format";
import type { ChatMessage as ChatMessageType } from "../../types/chat";

type Props = {
  message: ChatMessageType;
};

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
    containerClass: "mr-12 bg-white/80 border-border",
    iconClass: "bg-accent2/20 text-accent2",
  },
  system: {
    icon: Info,
    label: "System",
    containerClass: "mx-6 bg-warning/10 border-warning/20 italic",
    iconClass: "bg-warning/20 text-warning",
  },
};

export function ChatMessage({ message }: Props) {
  const config = roleConfig[message.role];
  const Icon = config.icon;
  const displayName = message.role === "agent"
    ? message.agent_name || message.agent_id || config.label
    : config.label;

  return (
    <div
      className={`chat-message rounded-2xl border p-4 transition-all duration-200 ${config.containerClass}`}
    >
      <div className="flex items-start gap-3">
        <div
          className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-xl ${config.iconClass}`}
        >
          <Icon className="h-4 w-4" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="mb-1 flex items-center justify-between gap-2">
            <span className="text-xs font-semibold uppercase tracking-wide text-ink">
              {displayName}
            </span>
            <span className="text-[10px] text-muted">
              {formatRelative(message.created_at)}
            </span>
          </div>
          <div className="text-sm text-ink whitespace-pre-wrap break-words">
            {message.content}
          </div>
          {(message.step_id || message.job_id) && (
            <div className="mt-2 flex flex-wrap gap-2">
              {message.step_id && (
                <span className="rounded-lg bg-accent/10 px-2 py-0.5 text-[10px] font-medium text-accent">
                  Step: {message.step_id}
                </span>
              )}
              {message.job_id && (
                <span className="rounded-lg bg-muted/20 px-2 py-0.5 text-[10px] font-medium text-muted">
                  Job: {message.job_id.slice(0, 8)}
                </span>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
