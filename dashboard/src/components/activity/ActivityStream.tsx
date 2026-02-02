import { useEffect, useRef, useState } from "react";
import { Activity, Loader2, Send } from "lucide-react";
import { Button } from "../ui/Button";
import { Textarea } from "../ui/Textarea";
import { ActivityBlock } from "./ActivityBlock";
import type { ActivityItem } from "../../types/activity";

const ACTIVE_RUN_STATUSES = ["running", "pending", "waiting", "blocked"];

type Props = {
  items: ActivityItem[];
  isLoading?: boolean;
  runStatus: string;
  isSending?: boolean;
  onSendMessage?: (content: string) => void;
  onApprove?: (jobId: string) => void;
  onReject?: (jobId: string) => void;
};

export function ActivityStream({
  items,
  isLoading,
  runStatus,
  isSending,
  onSendMessage,
  onApprove,
  onReject,
}: Props) {
  const [input, setInput] = useState("");
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const isRunActive = ACTIVE_RUN_STATUSES.includes(runStatus);

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [items]);

  const handleSend = () => {
    const content = input.trim();
    if (!content || !onSendMessage || isSending) {
      return;
    }
    onSendMessage(content);
    setInput("");
    inputRef.current?.focus();
  };

  const handleKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      handleSend();
    }
  };

  return (
    <div className="flex h-full min-h-[520px] flex-col rounded-3xl border border-border bg-white/70 overflow-hidden">
      <div className="flex items-center justify-between border-b border-border bg-white/50 px-5 py-4">
        <div className="flex items-center gap-3">
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-accent/10">
            <Activity className="h-4 w-4 text-accent" />
          </div>
          <div>
            <div className="text-sm font-semibold text-ink">Activity Stream</div>
            <div className="text-[10px] uppercase tracking-wide text-muted">
              {isRunActive ? "Live run narrative" : "History"}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <span className={`h-2 w-2 rounded-full ${isRunActive ? "bg-success animate-pulse" : "bg-muted"}`} />
          <span className="text-xs text-muted capitalize">{runStatus}</span>
        </div>
      </div>

      <div ref={scrollRef} className="flex-1 overflow-y-auto p-4 space-y-3 scroll-smooth">
        {isLoading ? (
          <div className="flex items-center justify-center py-16">
            <Loader2 className="h-6 w-6 text-muted animate-spin" />
          </div>
        ) : items.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-center">
            <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-accent/10 mb-4">
              <Activity className="h-8 w-8 text-accent" />
            </div>
            <div className="text-sm font-medium text-ink mb-1">No activity yet</div>
            <div className="text-xs text-muted max-w-xs">
              {isRunActive
                ? "Waiting for the first event. Messages, policy checks, and tool calls will appear here."
                : "No activity recorded for this run."}
            </div>
          </div>
        ) : (
          items.map((item) => (
            <ActivityBlock
              key={item.id}
              activity={item}
              onApprove={onApprove}
              onReject={onReject}
            />
          ))
        )}
      </div>

      {isRunActive && onSendMessage ? (
        <div className="border-t border-border bg-white/50 p-4">
          <div className="flex gap-3">
            <Textarea
              ref={inputRef}
              value={input}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Ask about this workflow run..."
              rows={2}
              className="flex-1 resize-none"
              disabled={isSending}
            />
            <Button
              variant="primary"
              onClick={handleSend}
              disabled={!input.trim() || isSending}
              className="self-end"
            >
              {isSending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
              <span className="sr-only">Send</span>
            </Button>
          </div>
          <div className="mt-2 text-[10px] text-muted">Press Enter to send, Shift+Enter for new line</div>
        </div>
      ) : null}
    </div>
  );
}
