import { useEffect, useCallback } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useChatStore } from "../state/chat";
import { useEventStore } from "../state/events";
import type { ChatMessage } from "../types/chat";

export function useRunChat(runId: string | undefined) {
  const queryClient = useQueryClient();
  const { addMessage, setMessages, threads } = useChatStore();
  const latestEvent = useEventStore((state) => state.events[0]);

  // Fetch existing chat history
  const chatQuery = useQuery({
    queryKey: ["chat", runId],
    queryFn: () => api.getRunChat(runId as string),
    enabled: Boolean(runId),
    staleTime: 30_000,
  });

  // Sync initial messages to store
  useEffect(() => {
    if (chatQuery.data?.items && runId) {
      setMessages(runId, chatQuery.data.items);
    }
  }, [chatQuery.data, runId, setMessages]);

  // Listen for live chat events from WebSocket
  useEffect(() => {
    if (!latestEvent || !runId) return;

    // Check if this is a chat event for our run
    const eventData = latestEvent as { eventType?: string; runId?: string; chatData?: unknown };
    if (eventData.eventType === "chat_message" && eventData.runId === runId) {
      const chatData = eventData.chatData as {
        id?: string;
        role?: string;
        content?: string;
        stepId?: string;
        jobId?: string;
        agentId?: string;
        agentName?: string;
        createdAt?: string;
        metadata?: Record<string, unknown>;
      } | undefined;

      if (chatData) {
        const message: ChatMessage = {
          id: chatData.id || latestEvent.id,
          run_id: runId,
          role: (chatData.role as ChatMessage["role"]) || "agent",
          content: chatData.content || "",
          step_id: chatData.stepId,
          job_id: chatData.jobId,
          agent_id: chatData.agentId,
          agent_name: chatData.agentName,
          created_at: chatData.createdAt || new Date().toISOString(),
          metadata: chatData.metadata,
        };
        addMessage(runId, message);
      }
    }
  }, [latestEvent, runId, addMessage]);

  // Send message mutation
  const sendMessageMutation = useMutation({
    mutationFn: (content: string) => api.sendChatMessage(runId as string, { content }),
    onSuccess: (data) => {
      if (runId && data) {
        addMessage(runId, data);
      }
      // Invalidate to sync with server
      queryClient.invalidateQueries({ queryKey: ["chat", runId] });
    },
  });

  // Get current thread messages
  const thread = runId ? threads.get(runId) : null;

  const sendMessage = useCallback(
    (content: string) => {
      if (!runId || !content.trim()) return;
      sendMessageMutation.mutate(content);
    },
    [runId, sendMessageMutation]
  );

  return {
    messages: thread?.messages || [],
    isLoading: chatQuery.isLoading,
    isError: chatQuery.isError,
    error: chatQuery.error,
    sendMessage,
    isSending: sendMessageMutation.isPending,
    refetch: chatQuery.refetch,
  };
}
