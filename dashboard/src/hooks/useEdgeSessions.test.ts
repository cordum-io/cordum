import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import { queryKeys } from "../lib/queryKeys";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import {
  approveEdgeApproval,
  edgeErrorFromApiError,
  exportEdgeSession,
  fetchEdgeApprovals,
  fetchEdgeExecutions,
  fetchEdgeExecutionEvents,
  fetchEdgeSessions,
  rejectEdgeApproval,
  useApproveEdgeApproval,
  useEdgeApprovals,
  useEdgeExecutions,
  useEdgeSession,
  useEdgeSessionEvents,
  useEdgeSessions,
  useExportEdgeSession,
  useRejectEdgeApproval,
  useWaitEdgeApproval,
  waitForEdgeApproval,
} from "./useEdgeSessions";

const { mockConfigState, loggerMock } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "test-key",
    tenantId: "tenant-a",
    principalId: "user-1",
    principalRole: "admin",
    user: null,
    isLoggingOut: false,
    logout: vi.fn(),
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

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

function edgeSessionWire(overrides: Record<string, unknown> = {}) {
  return {
    session_id: "edge_sess_1",
    tenant_id: "tenant-a",
    principal_id: "user-1",
    principal_type: "human",
    agent_product: "claude-code",
    mode: "local-dev",
    trace_id: "trace-1",
    policy_mode: "observe",
    status: "running",
    risk_summary: { denied_count: 1, approval_count: 0, artifact_count: 0 },
    started_at: "2026-05-02T10:00:00Z",
    ...overrides,
  };
}

function edgeExecutionWire(overrides: Record<string, unknown> = {}) {
  return {
    execution_id: "exec-1",
    session_id: "edge_sess_1",
    tenant_id: "tenant-a",
    adapter: "claude-code-hook",
    mode: "local-dev",
    workflow_run_id: "run-1",
    step_id: "step-1",
    job_id: "job-1",
    trace_id: "trace-1",
    status: "running",
    started_at: "2026-05-02T10:00:01Z",
    metrics: { events: 3, allow: 2, deny: 1, require_approval: 0 },
    ...overrides,
  };
}

function edgeApprovalWire(overrides: Record<string, unknown> = {}) {
  return {
    approval_ref: "edge_appr_1",
    tenant_id: "tenant-a",
    session_id: "edge_sess_1",
    execution_id: "exec-1",
    event_id: "evt-1",
    principal_id: "user-1",
    requester: "user-1",
    status: "pending",
    decision: "",
    reason: "requires approval",
    rule_id: "rule-1",
    policy_snapshot: "policy:v1",
    action_hash: "sha256:action",
    input_hash: "sha256:input",
    created_at: "2026-05-02T10:00:00Z",
    ...overrides,
  };
}

function expectJsonBody(init: RequestInit | undefined, expected: unknown): boolean {
  return init?.body === JSON.stringify(expected);
}

describe("useEdgeSessions fetchers", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000001");
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("serializes session filters and maps only matching Edge sessions", async () => {
    mockFetch([
      {
        match: (url, init) =>
          (init?.method ?? "GET") === "GET" &&
          url.includes("/edge/sessions?") &&
          url.includes("principal_id=user-1") &&
          url.includes("cursor=cur-1") &&
          url.includes("limit=25") &&
          !url.includes("status=") &&
          !url.includes("agentProduct="),
        body: {
          items: [
            edgeSessionWire({ session_id: "edge_sess_keep" }),
            edgeSessionWire({
              session_id: "edge_sess_drop_status",
              status: "ended",
            }),
            edgeSessionWire({
              session_id: "edge_sess_drop_agent",
              agent_product: "other-agent",
            }),
          ],
          next_cursor: "cur-2",
        },
      },
    ]);

    const page = await fetchEdgeSessions({
      principalId: "user-1",
      status: "running",
      agentProduct: "claude-code",
      cursor: "cur-1",
      limit: 25,
    });

    expect(page.nextCursor).toBe("cur-2");
    expect(page.items.map((session) => session.sessionId)).toEqual(["edge_sess_keep"]);
  });

  it("serializes execution list filters with linked job and workflow run indexes", async () => {
    mockFetch([
      {
        match: (url, init) =>
          (init?.method ?? "GET") === "GET" &&
          url.includes("/edge/executions?") &&
          url.includes("job_id=job-1") &&
          url.includes("workflow_run_id=run-1") &&
          url.includes("cursor=cur-1") &&
          url.includes("limit=10"),
        body: {
          items: [edgeExecutionWire({ execution_id: "exec-linked" })],
          next_cursor: null,
        },
      },
    ]);

    const page = await fetchEdgeExecutions({
      jobId: "job-1",
      workflowRunId: "run-1",
      cursor: "cur-1",
      limit: 10,
    });

    expect(page.nextCursor).toBeNull();
    expect(page.items[0]).toMatchObject({
      executionId: "exec-linked",
      jobId: "job-1",
      workflowRunId: "run-1",
      metrics: { allow: 2, deny: 1 },
    });
  });

  it("serializes event and approval filters with snake_case backend fields", async () => {
    mockFetch([
      {
        match: (url) =>
          url.includes("/edge/executions/exec-1/events?") &&
          url.includes("kind=hook.pre_tool_use") &&
          url.includes("decision=DENY") &&
          url.includes("since=2026-05-02T10%3A00%3A00Z") &&
          url.includes("until=2026-05-02T10%3A05%3A00Z") &&
          url.includes("limit=50"),
        body: {
          items: [
            {
              event_id: "evt-1",
              session_id: "edge_sess_1",
              execution_id: "exec-1",
              tenant_id: "tenant-a",
              seq: 1,
              ts: "2026-05-02T10:00:02Z",
              layer: "hook",
              kind: "hook.pre_tool_use",
              decision: "DENY",
              status: "blocked",
            },
          ],
          next_cursor: null,
        },
      },
      {
        match: (url) =>
          url.includes("/edge/approvals?") &&
          url.includes("status=pending") &&
          url.includes("session_id=edge_sess_1") &&
          url.includes("execution_id=exec-1") &&
          url.includes("action_hash=sha256%3Aaction"),
        body: { items: [edgeApprovalWire()], next_cursor: null },
      },
    ]);

    const events = await fetchEdgeExecutionEvents("exec-1", {
      kind: "hook.pre_tool_use",
      decision: "DENY",
      since: "2026-05-02T10:00:00Z",
      until: "2026-05-02T10:05:00Z",
      limit: 50,
    });
    const approvals = await fetchEdgeApprovals({
      status: "pending",
      sessionId: "edge_sess_1",
      executionId: "exec-1",
      actionHash: "sha256:action",
    });

    expect(events.items[0].eventId).toBe("evt-1");
    expect(approvals.items[0].approvalRef).toBe("edge_appr_1");
  });

  it("posts approve, reject, wait, and export bodies to the current Edge endpoints", async () => {
    mockFetch([
      {
        match: (url, init) =>
          url.includes("/edge/approvals/edge_appr_1/approve") &&
          expectJsonBody(init, { reason: "ship it" }),
        method: "POST",
        body: edgeApprovalWire({ status: "approved", decision: "approve" }),
      },
      {
        match: (url, init) =>
          url.includes("/edge/approvals/edge_appr_1/reject") &&
          expectJsonBody(init, { reason: "nope" }),
        method: "POST",
        body: edgeApprovalWire({ status: "rejected", decision: "reject" }),
      },
      {
        match: (url, init) =>
          url.includes("/edge/approvals/edge_appr_1/wait") &&
          expectJsonBody(init, { timeout_ms: 1500 }),
        method: "POST",
        body: edgeApprovalWire({ status: "approved", decision: "approve" }),
      },
      {
        match: (url, init) =>
          url.includes("/edge/sessions/edge_sess_1/export") &&
          expectJsonBody(init, { max_events: 10 }),
        method: "POST",
        body: {
          manifest_version: "edge.export.v1",
          generated_at: "2026-05-02T10:10:00Z",
          tenant_id: "tenant-a",
          session: edgeSessionWire(),
        },
      },
    ]);

    await expect(approveEdgeApproval({ approvalRef: "edge_appr_1", reason: " ship it " }))
      .resolves.toMatchObject({ status: "approved" });
    await expect(rejectEdgeApproval({ approvalRef: "edge_appr_1", reason: "nope" }))
      .resolves.toMatchObject({ status: "rejected" });
    await expect(waitForEdgeApproval({ approvalRef: "edge_appr_1", timeoutMs: 1500 }))
      .resolves.toMatchObject({ status: "approved" });
    await expect(exportEdgeSession({ sessionId: "edge_sess_1", request: { maxEvents: 10 } }))
      .resolves.toMatchObject({ manifestVersion: "edge.export.v1" });
  });
});

describe("useEdgeSessions hooks", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000002");
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns successful list, event, and approval query data through QueryClient hooks", async () => {
    mockFetch([
      {
        match: (url) =>
          url.includes("/edge/sessions?") &&
          url.includes("principal_id=user-1") &&
          url.includes("limit=2"),
        body: {
          items: [edgeSessionWire({ session_id: "edge_sess_query" })],
          next_cursor: null,
        },
      },
      {
        match: (url) =>
          url.includes("/edge/sessions/edge_sess_query/events?") &&
          url.includes("kind=hook.pre_tool_use") &&
          url.includes("limit=3"),
        body: {
          items: [
            {
              event_id: "evt-query",
              session_id: "edge_sess_query",
              execution_id: "exec-query",
              tenant_id: "tenant-a",
              seq: 2,
              ts: "2026-05-02T10:00:02Z",
              layer: "hook",
              kind: "hook.pre_tool_use",
              decision: "ALLOW",
              status: "recorded",
            },
          ],
          next_cursor: null,
        },
      },
      {
        match: (url) =>
          url.includes("/edge/executions?") &&
          url.includes("workflow_run_id=run-query") &&
          url.includes("limit=2"),
        body: {
          items: [
            edgeExecutionWire({
              execution_id: "exec-query",
              workflow_run_id: "run-query",
            }),
          ],
          next_cursor: null,
        },
      },
      {
        match: (url) =>
          url.includes("/edge/approvals?") &&
          url.includes("status=pending") &&
          url.includes("session_id=edge_sess_query"),
        body: {
          items: [edgeApprovalWire({ approval_ref: "edge_appr_query", session_id: "edge_sess_query" })],
          next_cursor: null,
        },
      },
    ]);

    const rendered = renderWithQueryClient(() => ({
      sessions: useEdgeSessions({
        principalId: "user-1",
        status: "running",
        agentProduct: "claude-code",
        limit: 2,
      }),
      events: useEdgeSessionEvents("edge_sess_query", {
        kind: "hook.pre_tool_use",
        limit: 3,
      }),
      executions: useEdgeExecutions({
        workflowRunId: "run-query",
        limit: 2,
      }),
      approvals: useEdgeApprovals({
        status: "pending",
        sessionId: "edge_sess_query",
      }),
    }));

    await rendered.waitFor(() => {
      expect(rendered.result.current?.sessions.isSuccess).toBe(true);
      expect(rendered.result.current?.events.isSuccess).toBe(true);
      expect(rendered.result.current?.executions.isSuccess).toBe(true);
      expect(rendered.result.current?.approvals.isSuccess).toBe(true);
    });

    expect(rendered.result.current?.sessions.data?.items[0].sessionId).toBe("edge_sess_query");
    expect(rendered.result.current?.events.data?.items[0].eventId).toBe("evt-query");
    expect(rendered.result.current?.executions.data?.items[0].executionId).toBe("exec-query");
    expect(rendered.result.current?.approvals.data?.items[0].approvalRef).toBe("edge_appr_query");
    rendered.unmount();
  });

  it("keeps detail queries disabled when IDs are absent", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const rendered = renderWithQueryClient(() => useEdgeSession(""));

    await act(async () => {
      await Promise.resolve();
    });

    expect(rendered.result.current?.fetchStatus).toBe("idle");
    expect(fetchSpy).not.toHaveBeenCalled();
    rendered.unmount();
  });

  it("surfaces successful detail data and typed Edge error bodies", async () => {
    mockFetch([
      {
        match: "/edge/sessions/edge_sess_1",
        body: edgeSessionWire({ session_id: "edge_sess_1" }),
      },
      {
        match: "/edge/sessions/missing",
        status: 404,
        body: {
          code: "not_found",
          message: "session not found",
          request_id: "req-1",
          details: { safe_code: "edge_not_found" },
        },
      },
    ]);

    const success = renderWithQueryClient(() => useEdgeSession("edge_sess_1"));
    await success.waitFor(() => {
      expect(success.result.current?.isSuccess).toBe(true);
    });
    expect(success.result.current?.data?.sessionId).toBe("edge_sess_1");
    success.unmount();

    const failure = renderWithQueryClient(() => useEdgeSession("missing"));
    await failure.waitFor(() => {
      expect(failure.result.current?.isError).toBe(true);
    });
    expect(failure.result.current?.error).toBeInstanceOf(ApiError);
    expect(edgeErrorFromApiError(failure.result.current?.error)).toEqual({
      code: "not_found",
      message: "session not found",
      requestId: "req-1",
      details: { safe_code: "edge_not_found" },
    });
    failure.unmount();
  });

  it("invalidates precise Edge query prefixes after approve/reject/wait/export mutations", async () => {
    mockFetch([
      {
        match: "/edge/approvals/edge_appr_1/approve",
        method: "POST",
        body: edgeApprovalWire({ status: "approved", decision: "approve" }),
      },
      {
        match: "/edge/approvals/edge_appr_2/reject",
        method: "POST",
        body: edgeApprovalWire({
          approval_ref: "edge_appr_2",
          status: "rejected",
          decision: "reject",
        }),
      },
      {
        match: "/edge/approvals/edge_appr_3/wait",
        method: "POST",
        body: edgeApprovalWire({
          approval_ref: "edge_appr_3",
          status: "approved",
          decision: "approve",
        }),
      },
      {
        match: "/edge/sessions/edge_sess_1/export",
        method: "POST",
        body: {
          manifest_version: "edge.export.v1",
          generated_at: "2026-05-02T10:10:00Z",
          tenant_id: "tenant-a",
          session: edgeSessionWire(),
          executions: [{ execution_id: "exec-1", session_id: "edge_sess_1", tenant_id: "tenant-a", adapter: "claude-code-hook", mode: "local-dev", status: "succeeded", started_at: "2026-05-02T10:00:01Z" }],
        },
      },
    ]);
    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const rendered = renderWithQueryClient(() => ({
      approve: useApproveEdgeApproval(),
      reject: useRejectEdgeApproval(),
      wait: useWaitEdgeApproval(),
      exportSession: useExportEdgeSession(),
    }), queryClient);

    await act(async () => {
      await rendered.result.current?.approve.mutateAsync({ approvalRef: "edge_appr_1" });
      await rendered.result.current?.reject.mutateAsync({ approvalRef: "edge_appr_2", reason: "no" });
      await rendered.result.current?.wait.mutateAsync({ approvalRef: "edge_appr_3", timeoutMs: 500 });
      await rendered.result.current?.exportSession.mutateAsync({
        sessionId: "edge_sess_1",
        request: { maxEvents: 5 },
      });
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.edge.approvals.lists() });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.edge.executions.lists() });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.edge.approvals.detail("edge_appr_1") });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.edge.sessions.detail("edge_sess_1") });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.edge.sessions.eventLists("edge_sess_1") });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.edge.executions.detail("exec-1") });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: queryKeys.edge.sessions.export("edge_sess_1") });
    rendered.unmount();
  });
});
