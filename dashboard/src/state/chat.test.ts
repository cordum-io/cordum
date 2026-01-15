import { describe, it, expect, beforeEach } from "vitest";
import { useChatStore } from "./chat";
import type { ChatMessage } from "../types/chat";

describe("useChatStore", () => {
  beforeEach(() => {
    // Reset store before each test
    useChatStore.setState({
      threads: new Map(),
      activeRunId: null,
    });
  });

  describe("addMessage", () => {
    it("should add a message to a new thread", () => {
      const message: ChatMessage = {
        id: "msg-1",
        run_id: "run-1",
        role: "agent",
        content: "Hello from agent",
        created_at: "2024-01-15T10:00:00Z",
      };

      useChatStore.getState().addMessage("run-1", message);

      const thread = useChatStore.getState().threads.get("run-1");
      expect(thread).toBeDefined();
      expect(thread?.messages).toHaveLength(1);
      expect(thread?.messages[0]).toEqual(message);
    });

    it("should add a message to an existing thread", () => {
      const msg1: ChatMessage = {
        id: "msg-1",
        run_id: "run-1",
        role: "agent",
        content: "First message",
        created_at: "2024-01-15T10:00:00Z",
      };

      const msg2: ChatMessage = {
        id: "msg-2",
        run_id: "run-1",
        role: "user",
        content: "Second message",
        created_at: "2024-01-15T10:01:00Z",
      };

      useChatStore.getState().addMessage("run-1", msg1);
      useChatStore.getState().addMessage("run-1", msg2);

      const thread = useChatStore.getState().threads.get("run-1");
      expect(thread?.messages).toHaveLength(2);
      expect(thread?.messages[1]).toEqual(msg2);
    });

    it("should not add duplicate messages", () => {
      const message: ChatMessage = {
        id: "msg-1",
        run_id: "run-1",
        role: "agent",
        content: "Hello",
        created_at: "2024-01-15T10:00:00Z",
      };

      useChatStore.getState().addMessage("run-1", message);
      useChatStore.getState().addMessage("run-1", message);

      const thread = useChatStore.getState().threads.get("run-1");
      expect(thread?.messages).toHaveLength(1);
    });

    it("should create thread with messages array", () => {
      const message: ChatMessage = {
        id: "msg-1",
        run_id: "run-1",
        role: "agent",
        content: "Hello",
        created_at: "2024-01-15T10:00:00Z",
      };

      useChatStore.getState().addMessage("run-1", message);

      const thread = useChatStore.getState().threads.get("run-1");
      expect(thread).toBeDefined();
      expect(Array.isArray(thread?.messages)).toBe(true);
    });
  });

  describe("setMessages", () => {
    it("should replace all messages in a thread", () => {
      const initialMsg: ChatMessage = {
        id: "msg-1",
        run_id: "run-1",
        role: "agent",
        content: "Initial",
        created_at: "2024-01-15T10:00:00Z",
      };

      useChatStore.getState().addMessage("run-1", initialMsg);

      const newMessages: ChatMessage[] = [
        {
          id: "msg-2",
          run_id: "run-1",
          role: "user",
          content: "New message 1",
          created_at: "2024-01-15T11:00:00Z",
        },
        {
          id: "msg-3",
          run_id: "run-1",
          role: "agent",
          content: "New message 2",
          created_at: "2024-01-15T11:01:00Z",
        },
      ];

      useChatStore.getState().setMessages("run-1", newMessages);

      const thread = useChatStore.getState().threads.get("run-1");
      expect(thread?.messages).toHaveLength(2);
      expect(thread?.messages).toEqual(newMessages);
    });

    it("should create a new thread if it does not exist", () => {
      const messages: ChatMessage[] = [
        {
          id: "msg-1",
          run_id: "run-1",
          role: "agent",
          content: "Hello",
          created_at: "2024-01-15T10:00:00Z",
        },
      ];

      useChatStore.getState().setMessages("run-1", messages);

      const thread = useChatStore.getState().threads.get("run-1");
      expect(thread).toBeDefined();
      expect(thread?.messages).toEqual(messages);
    });
  });

  describe("setActiveRun", () => {
    it("should set the active run ID", () => {
      useChatStore.getState().setActiveRun("run-1");
      expect(useChatStore.getState().activeRunId).toBe("run-1");
    });

    it("should clear the active run ID when null", () => {
      useChatStore.getState().setActiveRun("run-1");
      useChatStore.getState().setActiveRun(null);
      expect(useChatStore.getState().activeRunId).toBeNull();
    });
  });

  describe("clearThread", () => {
    it("should remove a thread", () => {
      const message: ChatMessage = {
        id: "msg-1",
        run_id: "run-1",
        role: "agent",
        content: "Hello",
        created_at: "2024-01-15T10:00:00Z",
      };

      useChatStore.getState().addMessage("run-1", message);
      expect(useChatStore.getState().threads.has("run-1")).toBe(true);

      useChatStore.getState().clearThread("run-1");
      expect(useChatStore.getState().threads.has("run-1")).toBe(false);
    });

    it("should not error when clearing non-existent thread", () => {
      expect(() => {
        useChatStore.getState().clearThread("non-existent");
      }).not.toThrow();
    });
  });

  describe("updateThreadStatus", () => {
    it("should update thread status", () => {
      const message: ChatMessage = {
        id: "msg-1",
        run_id: "run-1",
        role: "agent",
        content: "Hello",
        created_at: "2024-01-15T10:00:00Z",
      };

      useChatStore.getState().addMessage("run-1", message);
      useChatStore.getState().updateThreadStatus("run-1", "active");

      const thread = useChatStore.getState().threads.get("run-1");
      expect(thread?.status).toBe("active");
    });

    it("should not create a thread if it does not exist", () => {
      useChatStore.getState().updateThreadStatus("run-1", "active");
      expect(useChatStore.getState().threads.has("run-1")).toBe(false);
    });
  });
});

describe("ChatMessage role types", () => {
  it("should accept valid role types", () => {
    const agentMsg: ChatMessage = {
      id: "1",
      run_id: "run-1",
      role: "agent",
      content: "Agent message",
      created_at: "2024-01-15T10:00:00Z",
    };

    const userMsg: ChatMessage = {
      id: "2",
      run_id: "run-1",
      role: "user",
      content: "User message",
      created_at: "2024-01-15T10:00:00Z",
    };

    const systemMsg: ChatMessage = {
      id: "3",
      run_id: "run-1",
      role: "system",
      content: "System message",
      created_at: "2024-01-15T10:00:00Z",
    };

    expect(agentMsg.role).toBe("agent");
    expect(userMsg.role).toBe("user");
    expect(systemMsg.role).toBe("system");
  });

  it("should include optional fields", () => {
    const message: ChatMessage = {
      id: "1",
      run_id: "run-1",
      role: "agent",
      content: "Hello",
      created_at: "2024-01-15T10:00:00Z",
      step_id: "step-1",
      job_id: "job-1",
      agent_id: "agent-1",
      agent_name: "Code Assistant",
      metadata: { tool: "search", query: "test" },
    };

    expect(message.step_id).toBe("step-1");
    expect(message.job_id).toBe("job-1");
    expect(message.agent_id).toBe("agent-1");
    expect(message.agent_name).toBe("Code Assistant");
    expect(message.metadata).toEqual({ tool: "search", query: "test" });
  });
});
