/*
 * DESIGN: "Control Surface" — Workflow Run Detail
 * PRD Section 13: Real-time workflow run view with animated graph + chat
 */
import { useState, useRef, useEffect, useCallback, useMemo } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { SkeletonCard } from "@/components/ui/Skeleton";
import {
  ArrowLeft, Send, Briefcase, Shield, GitBranch, Clock,
  CheckCircle2, XCircle, Loader2, MessageSquare, AlertTriangle,
  ChevronDown, Copy, RotateCcw,
} from "lucide-react";
import { cn, formatRelativeTime, formatDuration } from "@/lib/utils";
import { toast } from "sonner";
import { get, post } from "@/api/client";
import {
  useRun,
  useRunTimeline,
  useCancelRun,
  useRerunRun,
  type RunTimelineEvent,
} from "@/hooks/useWorkflows";
import type { RunStatus } from "@/api/types";

interface ChatMessage {
  id: string;
  role: "user" | "system" | "agent";
  content: string;
  timestamp: string;
}

interface RunStep {
  id: string;
  label: string;
  type: "worker" | "approval" | "condition" | "delay";
  status: "succeeded" | "running" | "failed" | "pending" | "skipped";
  duration?: string;
  output?: string;
}

function mapStepType(type: string): RunStep["type"] {
  switch (type) {
    case "approval": return "approval";
    case "condition":
    case "switch": return "condition";
    case "delay": return "delay";
    default: return "worker";
  }
}

function mapStepStatus(status?: string): RunStep["status"] {
  switch (status) {
    case "succeeded": return "succeeded";
    case "running":
    case "waiting": return "running";
    case "failed":
    case "timed_out": return "failed";
    case "cancelled": return "skipped";
    default: return "pending";
  }
}

function runStatusVariant(status: RunStatus): "healthy" | "warning" | "danger" | "info" | "muted" {
  switch (status) {
    case "succeeded": return "healthy";
    case "running": return "info";
    case "waiting": return "warning";
    case "failed":
    case "timed_out": return "danger";
    case "cancelled": return "muted";
    default: return "muted";
  }
}

export default function WorkflowRunDetailPage() {
  const { workflowId, runId } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [chatInput, setChatInput] = useState("");
  const [selectedStep, setSelectedStep] = useState<RunStep | null>(null);
  const [cancelOpen, setCancelOpen] = useState(false);
  const chatEndRef = useRef<HTMLDivElement>(null);

  // Real data hooks
  const { data: run, isLoading: runLoading, error: runError } = useRun(runId);
  const { data: timeline, isLoading: timelineLoading } = useRunTimeline(runId);
  const cancelMutation = useCancelRun();
  const rerunMutation = useRerunRun();

  // Chat: try real endpoint, fall back to timeline events as messages
  const { data: chatData, error: chatError } = useQuery<ChatMessage[]>({
    queryKey: ["run-chat", runId],
    queryFn: async () => {
      if (!runId) throw new Error("run id required");
      const res = await get<ChatMessage[]>(`/workflow-runs/${runId}/chat`);
      return res ?? [];
    },
    enabled: !!runId,
    retry: false,
    staleTime: 5_000,
  });

  const chatMutation = useMutation({
    mutationFn: (content: string) => {
      if (!runId) throw new Error("run id required");
      return post<ChatMessage>(`/workflow-runs/${runId}/chat`, { content });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["run-chat", runId] });
    },
  });

  // Derive messages: prefer chat API data, fall back to timeline events
  const messages = useMemo<ChatMessage[]>(() => {
    if (chatData && chatData.length > 0) return chatData;
    if (!timeline) return [];
    return timeline
      .filter(e => e.message || e.status)
      .map((e, i) => ({
        id: String(i),
        role: "system" as const,
        content: e.message || `Step ${e.step_id ?? "?"}: ${e.type} \u2192 ${e.status ?? ""}`,
        timestamp: e.time ? formatRelativeTime(e.time) : "",
      }));
  }, [chatData, timeline]);

  // True when chat API failed or returned empty and we're showing timeline-derived messages
  const isChatFallback = (!chatData || chatData.length === 0) && messages.length > 0;

  // Derive steps from run data + timeline
  const steps = useMemo<RunStep[]>(() => {
    if (!run?.steps) return [];

    const timelineByStep = new Map<string, RunTimelineEvent[]>();
    if (timeline) {
      for (const evt of timeline) {
        if (!evt.step_id) continue;
        const existing = timelineByStep.get(evt.step_id) ?? [];
        existing.push(evt);
        timelineByStep.set(evt.step_id, existing);
      }
    }

    return run.steps.map((step): RunStep => {
      const events = timelineByStep.get(step.id) ?? [];

      // Compute duration from timeline events
      let duration: string | undefined;
      if (events.length >= 2) {
        const times = events.map(e => new Date(e.time).getTime()).filter(t => !isNaN(t));
        if (times.length >= 2) {
          const durationMs = Math.max(...times) - Math.min(...times);
          duration = formatDuration(durationMs);
        }
      }

      // Get output from step output or latest timeline event
      let output: string | undefined;
      if (step.output) {
        try { output = JSON.stringify(step.output); } catch { /* ignore */ }
      } else {
        const lastEvent = events.filter(e => e.result_ptr || e.data).pop();
        if (lastEvent?.data) {
          try { output = JSON.stringify(lastEvent.data); } catch { /* ignore */ }
        } else if (lastEvent?.result_ptr) {
          output = lastEvent.result_ptr;
        }
      }

      // Prefer step.status from run data, fall back to timeline
      const stepStatus = step.status ?? events.filter(e => e.status).pop()?.status;

      return {
        id: step.id,
        label: step.name,
        type: mapStepType(step.type),
        status: mapStepStatus(stepStatus),
        duration: stepStatus === "running" ? (duration ? `${duration}...` : undefined) : duration,
        output,
      };
    });
  }, [run, timeline]);

  const completedCount = steps.filter(s => s.status === "succeeded").length;
  const totalSteps = steps.length;

  // Auto-select first running or first step
  useEffect(() => {
    if (steps.length > 0 && !selectedStep) {
      const running = steps.find(s => s.status === "running");
      setSelectedStep(running ?? steps[0]);
    }
  }, [steps, selectedStep]);

  // Auto-scroll chat
  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  const sendMessage = useCallback(() => {
    if (!chatInput.trim()) return;
    chatMutation.mutate(chatInput.trim());
    setChatInput("");
  }, [chatInput, chatMutation]);

  const handleCancel = () => {
    if (!workflowId || !runId) return;
    cancelMutation.mutate(
      { workflowId, runId },
      { onSuccess: () => { setCancelOpen(false); navigate(`/workflows/${workflowId}`); } },
    );
  };

  const handleRetry = () => {
    if (!runId) return;
    rerunMutation.mutate(
      { runId },
      {
        onSuccess: (data) => {
          if (data?.run_id && workflowId) {
            navigate(`/workflows/${workflowId}/runs/${data.run_id}`);
          }
        },
      },
    );
  };

  const stepIcon = (type: string) => {
    switch (type) {
      case "worker": return Briefcase;
      case "approval": return Shield;
      case "condition": return GitBranch;
      default: return Clock;
    }
  };

  const stepStatusIcon = (status: string) => {
    switch (status) {
      case "succeeded": return <CheckCircle2 className="w-4 h-4 text-[var(--color-success)]" />;
      case "running": return <Loader2 className="w-4 h-4 text-cordum animate-spin" />;
      case "failed": return <XCircle className="w-4 h-4 text-destructive" />;
      case "skipped": return <ChevronDown className="w-4 h-4 text-muted-foreground" />;
      default: return <div className="w-4 h-4 rounded-full border-2 border-border" />;
    }
  };

  // Loading state
  if (runLoading) {
    return (
      <div className="h-[calc(100vh-64px)] flex flex-col -m-6">
        <div className="px-5 py-3 border-b border-border bg-surface-0">
          <SkeletonCard />
        </div>
        <div className="flex flex-1 overflow-hidden">
          <div className="w-80 border-r border-border bg-surface-0 p-4 space-y-3">
            <SkeletonCard />
            <SkeletonCard />
            <SkeletonCard />
          </div>
          <div className="flex-1 p-5">
            <SkeletonCard />
          </div>
        </div>
      </div>
    );
  }

  // Error state
  if (runError) {
    return (
      <div className="h-[calc(100vh-64px)] flex flex-col -m-6 items-center justify-center">
        <AlertTriangle className="w-10 h-10 text-destructive mb-4" />
        <p className="text-sm font-medium text-foreground mb-1">Failed to load run</p>
        <p className="text-xs text-muted-foreground mb-4">
          {runError instanceof Error ? runError.message : "An unexpected error occurred"}
        </p>
        <Button variant="outline" size="sm" onClick={() => navigate(-1)}>
          <ArrowLeft className="w-3 h-3 mr-1" />
          Go Back
        </Button>
      </div>
    );
  }

  const isRunning = run?.status === "running" || run?.status === "pending" || run?.status === "waiting";

  return (
    <div className="h-[calc(100vh-64px)] flex flex-col -m-6">
      {/* Header */}
      <div className="flex items-center justify-between px-5 py-3 border-b border-border bg-surface-0 shrink-0">
        <div className="flex items-center gap-3">
          <button onClick={() => navigate(`/workflows/${workflowId}`)} className="p-1.5 rounded-full hover:bg-surface-2 transition-colors">
            <ArrowLeft className="w-4 h-4 text-muted-foreground" />
          </button>
          <div>
            <div className="flex items-center gap-2">
              <span className="text-sm font-display font-semibold text-foreground">Run {runId?.slice(0, 8)}</span>
              <StatusBadge
                variant={runStatusVariant(run?.status ?? "pending")}
                dot
                pulse={run?.status === "running"}
              >
                {run?.status ?? "unknown"}
              </StatusBadge>
              <span className="text-xs font-mono text-muted-foreground">{completedCount}/{totalSteps} steps</span>
            </div>
            <p className="text-xs text-muted-foreground font-mono">Workflow: {run?.workflowId ?? workflowId}</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={handleRetry}
            disabled={rerunMutation.isPending}
          >
            <RotateCcw className="w-3 h-3 mr-1" />
            {rerunMutation.isPending ? "Retrying..." : "Retry"}
          </Button>
          <Button
            variant="danger"
            size="sm"
            onClick={() => setCancelOpen(true)}
            disabled={!isRunning || cancelMutation.isPending}
          >
            <XCircle className="w-3 h-3 mr-1" />
            Cancel Run
          </Button>
        </div>
      </div>

      {/* Progress Bar */}
      <div className="h-1 bg-surface-1 shrink-0">
        <motion.div
          className="h-full bg-cordum"
          initial={{ width: 0 }}
          animate={{ width: totalSteps > 0 ? `${(completedCount / totalSteps) * 100}%` : "0%" }}
          transition={{ duration: 0.8, ease: "easeOut" }}
        />
      </div>

      {/* Split Layout: Steps + Chat */}
      <div className="flex flex-1 overflow-hidden">
        {/* Steps Panel — Animated Graph */}
        <div className="w-80 border-r border-border bg-surface-0 overflow-y-auto shrink-0">
          <div className="p-4">
            <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-3">
              Execution Graph ({completedCount}/{totalSteps})
            </p>
            {timelineLoading && steps.length === 0 ? (
              <div className="space-y-3">
                <SkeletonCard />
                <SkeletonCard />
              </div>
            ) : steps.length === 0 ? (
              <p className="text-xs text-muted-foreground">No steps in this run</p>
            ) : (
              <div className="relative">
                {/* Vertical connector line */}
                <div className="absolute left-[17px] top-0 bottom-0 w-px bg-border" />

                <div className="space-y-0.5">
                  {steps.map((step, i) => {
                    const Icon = stepIcon(step.type);
                    const isActive = step.status === "running";
                    return (
                      <motion.div
                        key={step.id}
                        initial={{ opacity: 0, x: -12 }}
                        animate={{ opacity: 1, x: 0 }}
                        transition={{ delay: i * 0.08 }}
                        onClick={() => setSelectedStep(step)}
                        className={cn(
                          "relative flex items-center gap-3 px-3 py-3 rounded-2xl transition-colors cursor-pointer",
                          isActive ? "bg-cordum/5 border border-cordum/20" : "hover:bg-surface-1",
                          selectedStep?.id === step.id && !isActive && "bg-surface-1 border border-border",
                          step.status === "pending" && "opacity-50",
                        )}
                      >
                        <div className="relative z-10">
                          {stepStatusIcon(step.status)}
                        </div>
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <p className={cn("text-xs font-medium", step.status === "pending" ? "text-muted-foreground" : "text-foreground")}>{step.label}</p>
                            <Icon className="w-3 h-3 text-muted-foreground" />
                          </div>
                          <div className="flex items-center gap-2 mt-0.5">
                            <span className="text-[10px] text-muted-foreground capitalize">{step.type}</span>
                            {step.duration && (
                              <span className={cn("text-[10px] font-mono", isActive ? "text-cordum" : "text-muted-foreground")}>
                                {step.duration}
                              </span>
                            )}
                          </div>
                        </div>
                        <span className="text-[10px] font-mono text-muted-foreground">{i + 1}</span>
                      </motion.div>
                    );
                  })}
                </div>
              </div>
            )}
          </div>

          {/* Selected Step Output */}
          <AnimatePresence mode="wait">
            {selectedStep && (
              <motion.div
                key={selectedStep.id}
                initial={{ opacity: 0, height: 0 }}
                animate={{ opacity: 1, height: "auto" }}
                exit={{ opacity: 0, height: 0 }}
                className="border-t border-border overflow-hidden"
              >
                <div className="p-4">
                  <div className="flex items-center justify-between mb-3">
                    <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Step Output</p>
                    {selectedStep.output && (
                      <button
                        onClick={() => { if (selectedStep.output) { navigator.clipboard.writeText(selectedStep.output); toast.success("Copied"); } }}
                        className="p-1 rounded hover:bg-surface-2 transition-colors"
                      >
                        <Copy className="w-3 h-3 text-muted-foreground" />
                      </button>
                    )}
                  </div>
                  {selectedStep.output ? (
                    <div className="rounded-2xl bg-surface-1 border border-border p-3 font-mono text-xs text-foreground max-h-48 overflow-auto">
                      <pre>{(() => {
                        try { return JSON.stringify(JSON.parse(selectedStep.output), null, 2); }
                        catch { return selectedStep.output; }
                      })()}</pre>
                    </div>
                  ) : selectedStep.status === "running" ? (
                    <div className="flex items-center gap-2 text-xs text-muted-foreground">
                      <Loader2 className="w-3 h-3 animate-spin text-cordum" />
                      Processing...
                    </div>
                  ) : (
                    <p className="text-xs text-muted-foreground">Waiting to execute</p>
                  )}
                </div>
              </motion.div>
            )}
          </AnimatePresence>
        </div>

        {/* Chat Panel */}
        <div className="flex-1 flex flex-col">
          <div className="flex items-center gap-2 px-5 py-3 border-b border-border bg-surface-0">
            <MessageSquare className="w-4 h-4 text-cordum" />
            <span className="text-sm font-display font-semibold text-foreground">Run Chat</span>
            <span className="text-[10px] font-mono text-muted-foreground ml-auto">{messages.length} messages</span>
          </div>
          {isChatFallback && (
            <div className="flex items-center gap-2 px-5 py-1.5 border-b border-[var(--color-warning)]/20 bg-[var(--color-warning)]/5 text-[11px] text-[var(--color-warning)]">
              <AlertTriangle className="w-3 h-3 shrink-0" />
              Showing timeline events {chatError ? "(chat unavailable)" : "(no chat messages)"}
            </div>
          )}

          {/* Messages */}
          <div className="flex-1 overflow-y-auto p-5 space-y-3">
            {messages.length === 0 && (
              <p className="text-xs text-muted-foreground text-center py-8">No messages yet</p>
            )}
            {messages.map((msg, i) => (
              <motion.div
                key={msg.id}
                initial={{ opacity: 0, y: 8 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: i * 0.03 }}
                className={cn(
                  "max-w-[80%] rounded-2xl p-3",
                  msg.role === "user" ? "ml-auto bg-cordum/10 border border-cordum/20" :
                  msg.role === "agent" ? "bg-[var(--color-info)]/10 border border-[var(--color-info)]/20" :
                  "bg-surface-1 border border-border",
                )}
              >
                <div className="flex items-center gap-2 mb-1">
                  <span className={cn(
                    "text-[10px] font-mono uppercase",
                    msg.role === "user" ? "text-cordum" :
                    msg.role === "agent" ? "text-[var(--color-info)]" :
                    "text-muted-foreground",
                  )}>
                    {msg.role}
                  </span>
                  <span className="text-[10px] text-muted-foreground">{msg.timestamp}</span>
                </div>
                <p className="text-sm text-foreground leading-relaxed">{msg.content}</p>
              </motion.div>
            ))}
            <div ref={chatEndRef} />
          </div>

          {/* Input */}
          <div className="p-4 border-t border-border bg-surface-0">
            <div className="flex items-center gap-2">
              <input
                type="text"
                value={chatInput}
                onChange={(e) => setChatInput(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && sendMessage()}
                placeholder="Send a message to the workflow..."
                className="flex-1 h-9 px-3 text-sm bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
              />
              <Button variant="primary" size="sm" onClick={sendMessage} disabled={!chatInput.trim() || chatMutation.isPending}>
                <Send className="w-3.5 h-3.5" />
              </Button>
            </div>
            <p className="text-[9px] text-muted-foreground mt-1.5">Press Enter to send. Messages are visible to all participants.</p>
          </div>
        </div>
      </div>

      {/* Cancel Confirmation */}
      <ConfirmDialog
        open={cancelOpen}
        onClose={() => setCancelOpen(false)}
        onConfirm={handleCancel}
        title="Cancel Workflow Run"
        description="This will terminate the currently running step and mark all pending steps as skipped. This action cannot be undone."
        confirmLabel={cancelMutation.isPending ? "Cancelling..." : "Cancel Run"}
        variant="destructive"
      />
    </div>
  );
}
