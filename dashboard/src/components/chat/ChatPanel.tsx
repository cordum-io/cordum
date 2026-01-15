import { useState, useRef, useEffect } from "react";
import { Send, Loader2, MessageSquare } from "lucide-react";
import { ChatMessage } from "./ChatMessage";
import { Button } from "../ui/Button";
import { Textarea } from "../ui/Textarea";
import type { ChatMessage as ChatMessageType } from "../../types/chat";

type Props = {
  runId: string;
  runStatus: string;
  messages: ChatMessageType[];
  isLoading: boolean;
  isSending: boolean;
  onSendMessage: (content: string) => void;
};

export function ChatPanel({
  runId,
  runStatus,
  messages,
  isLoading,
  isSending,
  onSendMessage,
}: Props) {
  const [input, setInput] = useState("");
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const isRunActive = ["running", "pending", "waiting", "blocked"].includes(runStatus);

  // Auto-scroll to bottom on new messages
  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages]);

  const handleSend = () => {
    const content = input.trim();
    if (!content || isSending) return;
    onSendMessage(content);
    setInput("");
    inputRef.current?.focus();
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  return (
    <div className="chat-panel flex flex-col h-[600px] rounded-3xl border border-border bg-white/70 overflow-hidden">
      {/* Header */}
      <div className="flex items-center justify-between px-5 py-4 border-b border-border bg-white/50">
        <div className="flex items-center gap-3">
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-accent/10">
            <MessageSquare className="h-4 w-4 text-accent" />
          </div>
          <div>
            <div className="text-sm font-semibold text-ink">Run Chat</div>
            <div className="text-[10px] text-muted uppercase tracking-wide">
              {isRunActive ? "Active conversation" : "Read-only"}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <span
            className={`h-2 w-2 rounded-full ${
              isRunActive ? "bg-success animate-pulse" : "bg-muted"
            }`}
          />
          <span className="text-xs text-muted capitalize">{runStatus}</span>
        </div>
      </div>

      {/* Messages area */}
      <div
        ref={scrollRef}
        className="flex-1 overflow-y-auto p-4 space-y-3 scroll-smooth"
      >
        {isLoading ? (
          <div className="flex items-center justify-center py-16">
            <Loader2 className="h-6 w-6 text-muted animate-spin" />
          </div>
        ) : messages.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-center">
            <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-accent/10 mb-4">
              <MessageSquare className="h-8 w-8 text-accent" />
            </div>
            <div className="text-sm font-medium text-ink mb-1">No messages yet</div>
            <div className="text-xs text-muted max-w-xs">
              {isRunActive
                ? "Start a conversation by typing below. Agent responses will appear here."
                : "This run has no chat history."}
            </div>
          </div>
        ) : (
          messages.map((msg) => <ChatMessage key={msg.id} message={msg} />)
        )}
      </div>

      {/* Input area */}
      {isRunActive && (
        <div className="p-4 border-t border-border bg-white/50">
          <div className="flex gap-3">
            <Textarea
              ref={inputRef}
              value={input}
              onChange={(e) => setInput(e.target.value)}
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
              {isSending ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <Send className="h-4 w-4" />
              )}
              <span className="sr-only">Send</span>
            </Button>
          </div>
          <div className="mt-2 text-[10px] text-muted">
            Press Enter to send, Shift+Enter for new line
          </div>
        </div>
      )}
    </div>
  );
}
