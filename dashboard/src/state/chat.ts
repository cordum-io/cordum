import { create } from "zustand";
import type { ChatMessage, ChatThread } from "../types/chat";

type ChatState = {
  threads: Map<string, ChatThread>;
  activeRunId: string | null;
  addMessage: (runId: string, message: ChatMessage) => void;
  setMessages: (runId: string, messages: ChatMessage[]) => void;
  setActiveRun: (runId: string | null) => void;
  clearThread: (runId: string) => void;
  updateThreadStatus: (runId: string, status: ChatThread["status"]) => void;
};

export const useChatStore = create<ChatState>((set) => ({
  threads: new Map(),
  activeRunId: null,

  addMessage: (runId, message) =>
    set((state) => {
      const threads = new Map(state.threads);
      const existing = threads.get(runId);
      const thread: ChatThread = existing || {
        run_id: runId,
        messages: [],
        status: "active",
      };
      // Avoid duplicates
      if (thread.messages.some((m) => m.id === message.id)) {
        return state;
      }
      threads.set(runId, {
        ...thread,
        messages: [...thread.messages, message],
      });
      return { threads };
    }),

  setMessages: (runId, messages) =>
    set((state) => {
      const threads = new Map(state.threads);
      threads.set(runId, {
        run_id: runId,
        messages,
        status: "active",
      });
      return { threads };
    }),

  setActiveRun: (runId) => set({ activeRunId: runId }),

  clearThread: (runId) =>
    set((state) => {
      const threads = new Map(state.threads);
      threads.delete(runId);
      return { threads };
    }),

  updateThreadStatus: (runId, status) =>
    set((state) => {
      const threads = new Map(state.threads);
      const thread = threads.get(runId);
      if (thread) {
        threads.set(runId, { ...thread, status });
      }
      return { threads };
    }),
}));
