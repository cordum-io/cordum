import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import { useCopilotSession } from "./useCopilotSession";

const { mockConfigState, loggerMock } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "",
    tenantId: "",
    principalId: "",
    principalRole: "",
    user: null,
    logout: vi.fn(),
    isLoggingOut: false,
  },
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../lib/logger", () => ({ logger: loggerMock }));

const sessionPayload = {
  session: {
    id: "sess-abc123",
    title: "Investigate deployment",
    userId: "alice",
    createdAt: "2026-04-26T07:00:00Z",
    updatedAt: "2026-04-26T07:05:00Z",
    messages: [
      {
        id: "msg-1",
        role: "user",
        content: "what failed?",
        timestamp: "2026-04-26T07:00:00Z",
        jobIds: ["job-1"],
      },
    ],
    metadata: { source: "copilot" },
  },
  jobs: [
    {
      id: "job-1",
      type: "job.deploy",
      topic: "job.deploy",
      status: "failed",
      pool: "job.deploy",
      capabilities: [],
      riskTags: [],
      metadata: {},
      createdAt: "2026-04-26T07:00:10Z",
      updatedAt: "2026-04-26T07:02:00Z",
    },
  ],
  decisions: [
    {
      jobId: "job-1",
      topic: "job.deploy",
      matchedRule: "rule-deny",
      verdict: "deny",
      reason: "deployment blocked",
      agentId: "agent-1",
      timestamp: "2026-04-26T07:00:15Z",
    },
  ],
  truncated: false,
};

describe("useCopilotSession", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    mockConfigState.isLoggingOut = false;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue(
      "00000000-0000-0000-0000-000000000777",
    );
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("fetches a full copilot session detail shape", async () => {
    mockFetch([
      {
        match: "/copilot/sessions/sess-abc123",
        method: "GET",
        body: sessionPayload,
      },
    ]);

    const hook = renderWithQueryClient(() => useCopilotSession("sess-abc123"));

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.session.id).toBe("sess-abc123");
    expect(hook.result.current?.data?.jobs.map((job) => job.id)).toEqual(["job-1"]);
    expect(hook.result.current?.data?.decisions.map((decision) => decision.matchedRule)).toEqual([
      "rule-deny",
    ]);
    expect(hook.result.current?.data?.truncated).toBe(false);
    hook.unmount();
  });

  it("maps 404 to a stable session not found error", async () => {
    mockFetch([
      {
        match: "/copilot/sessions/missing",
        method: "GET",
        status: 404,
        body: { error: "not found" },
      },
    ]);

    const hook = renderWithQueryClient(() => useCopilotSession("missing"));

    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });

    expect(hook.result.current?.error).toEqual(new Error("session not found"));
    hook.unmount();
  });

  it("rethrows non-404 API and network errors", async () => {
    mockFetch([
      {
        match: "/copilot/sessions/boom",
        method: "GET",
        status: 500,
        body: { error: "internal error" },
      },
    ]);

    const hook = renderWithQueryClient(() => useCopilotSession("boom"));

    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });

    expect(hook.result.current?.error).toEqual(
      new ApiError(500, "internal error", { error: "internal error" }),
    );
    hook.unmount();
  });

  it("refetches when the session id changes", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/copilot/sessions/sess-one",
        method: "GET",
        body: { ...sessionPayload, session: { ...sessionPayload.session, id: "sess-one" } },
      },
      {
        match: "/copilot/sessions/sess-two",
        method: "GET",
        body: { ...sessionPayload, session: { ...sessionPayload.session, id: "sess-two" } },
      },
    ]);
    const queryClient = createTestQueryClient();
    const hook = renderWithQueryClient(() => useCopilotSession("sess-one"), queryClient);

    await hook.waitFor(() => {
      expect(hook.result.current?.data?.session.id).toBe("sess-one");
    });

    hook.rerender(() => useCopilotSession("sess-two"));
    await hook.waitFor(() => {
      expect(hook.result.current?.data?.session.id).toBe("sess-two");
    });

    const requestedURLs = fetchSpy.mock.calls.map((call) => String(call[0]));
    expect(requestedURLs.filter((url) => url.includes("/copilot/sessions/"))).toEqual([
      "/api/v1/copilot/sessions/sess-one",
      "/api/v1/copilot/sessions/sess-two",
    ]);
    hook.unmount();
  });

  it("is disabled when the session id is empty or whitespace", async () => {
    const fetchSpy = mockFetch([]);
    const hook = renderWithQueryClient(() => useCopilotSession("   "));

    await hook.waitFor(() => {
      expect(hook.result.current?.fetchStatus).toBe("idle");
    });

    expect(fetchSpy).not.toHaveBeenCalled();
    hook.unmount();
  });
});
