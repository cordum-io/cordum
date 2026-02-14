import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import { __workflowsInternal, useCreateWorkflow, useWorkflow, useWorkflows } from "./useWorkflows";
import type { Workflow, WorkflowRun } from "../api/types";

const { addToastMock, loggerMock } = vi.hoisted(() => ({
  addToastMock: vi.fn(),
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

const { mockConfigState } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "",
    tenantId: "",
    principalId: "",
    principalRole: "",
    user: null,
    logout: vi.fn(),
  },
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../state/toast", () => ({
  useToastStore: {
    getState: () => ({ addToast: addToastMock }),
  },
}));

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

describe("useWorkflows internals", () => {
  it("buildQuery handles empty values and arrays", () => {
    expect(__workflowsInternal.buildQuery({})).toBe("");
    expect(
      __workflowsInternal.buildQuery({
        org_id: "org-1",
        tags: ["a", "b"],
        skip: undefined,
        empty: "",
      }),
    ).toBe("?org_id=org-1&tags=a&tags=b");
  });

  it("toStringArray handles arrays, comma strings, and invalid input", () => {
    expect(__workflowsInternal.toStringArray([" a ", "b", 3])).toEqual(["a", "b", "3"]);
    expect(__workflowsInternal.toStringArray("a, b, ,c")).toEqual(["a", "b", "c"]);
    expect(__workflowsInternal.toStringArray({})).toEqual([]);
  });

  it("parseDurationSeconds parses numeric and unit strings", () => {
    expect(__workflowsInternal.parseDurationSeconds(12)).toBe(12);
    expect(__workflowsInternal.parseDurationSeconds("1500ms")).toBe(2);
    expect(__workflowsInternal.parseDurationSeconds("3s")).toBe(3);
    expect(__workflowsInternal.parseDurationSeconds("2m")).toBe(120);
    expect(__workflowsInternal.parseDurationSeconds("1h")).toBe(3600);
    expect(__workflowsInternal.parseDurationSeconds("1d")).toBe(86400);
    expect(__workflowsInternal.parseDurationSeconds("-1m")).toBeUndefined();
    expect(__workflowsInternal.parseDurationSeconds("abc")).toBeUndefined();
  });

  it("parseDateToISO returns ISO for valid dates and undefined for invalid", () => {
    expect(__workflowsInternal.parseDateToISO("2026-02-13T10:00:00.000Z")).toBe(
      "2026-02-13T10:00:00.000Z",
    );
    expect(__workflowsInternal.parseDateToISO("not-a-date")).toBeUndefined();
  });

  it("buildStepPayload maps minimal and full step definitions", () => {
    const minimal = __workflowsInternal.buildStepPayload({
      id: "s1",
      name: "Step 1",
      type: "agent-task",
    } as Workflow["steps"][number]);
    expect(minimal).toEqual({
      id: "s1",
      name: "Step 1",
      type: "job",
    });

    const full = __workflowsInternal.buildStepPayload({
      id: "s2",
      name: "Step 2",
      type: "pack-action",
      depends_on: ["s1"],
      config: {
        backendType: "job",
        topic: "sys.job.submit",
        workerId: "worker-a",
        expression: "$.ok",
        forEach: "$.items[*]",
        parallelism: 3,
        timeout: "30s",
        duration: "10s",
        retryMax: 2,
        inputSchemaId: "schema-in",
        outputSchemaId: "schema-out",
        outputPath: "$.out",
        routeLabels: { region: "us-east" },
        messageTemplate: "hello",
        channel: "slack",
        prompt: "summarize",
        maxInputTokens: 10,
        maxOutputTokens: 20,
        maxTotalTokens: 30,
        capabilities: ["cap.primary", "cap.secondary"],
        requires: ["req.1"],
        riskTags: ["pii"],
        labels: { team: "ops" },
        packId: "pack-1",
        actorId: "actor-1",
        actorType: "service",
      },
    } as Workflow["steps"][number]);

    expect(full).toMatchObject({
      id: "s2",
      name: "Step 2",
      type: "job",
      depends_on: ["s1"],
      topic: "sys.job.submit",
      worker_id: "worker-a",
      condition: "$.ok",
      for_each: "$.items[*]",
      max_parallel: 3,
      timeout_sec: 30,
      delay_sec: 10,
      retry: { max_retries: 2 },
      input_schema_id: "schema-in",
      output_schema_id: "schema-out",
      output_path: "$.out",
      route_labels: { region: "us-east" },
    });
    expect(full.input).toMatchObject({
      message: "hello",
      component: "slack",
      prompt: "summarize",
      budget: {
        input_tokens: 10,
        output_tokens: 20,
        total_tokens: 30,
      },
    });
    expect(full.meta).toMatchObject({
      capability: "cap.primary",
      requires: ["cap.secondary", "req.1"],
      risk_tags: ["pii"],
      labels: { team: "ops" },
      pack_id: "pack-1",
      actor_id: "actor-1",
      actor_type: "service",
    });
  });

  it("toWorkflowUpsertPayload maps workflow payload and steps by id", () => {
    const payload = __workflowsInternal.toWorkflowUpsertPayload({
      id: "wf-1",
      name: "Workflow 1",
      description: "desc",
      orgId: "org-1",
      timeout: 60,
      steps: [{ id: "a", name: "A", type: "agent-task" }],
    } as Partial<Workflow> & { id?: string });

    expect(payload).toMatchObject({
      id: "wf-1",
      name: "Workflow 1",
      description: "desc",
      org_id: "org-1",
      timeout_sec: 60,
      steps: {
        a: { id: "a", name: "A", type: "job" },
      },
    });
  });

  it("getAttentionPriority, sortByAttention, and computeWorkflowStats behave correctly", () => {
    const runs: WorkflowRun[] = [
      {
        id: "r1",
        workflowId: "wf",
        status: "running",
        steps: [{ id: "s1", name: "s1", type: "step", status: "waiting" }],
        startedAt: "2026-02-13T01:00:00.000Z",
      },
      {
        id: "r2",
        workflowId: "wf",
        status: "running",
        steps: [{ id: "s2", name: "s2", type: "step", status: "failed" }],
        startedAt: "2026-02-13T02:00:00.000Z",
      },
      {
        id: "r3",
        workflowId: "wf",
        status: "queued",
        steps: [],
        startedAt: "2026-02-13T03:00:00.000Z",
      },
      {
        id: "r4",
        workflowId: "wf",
        status: "succeeded",
        steps: [],
        startedAt: "2026-02-13T04:00:00.000Z",
      },
    ] as WorkflowRun[];

    expect(__workflowsInternal.getAttentionPriority(runs[0])).toBe(0);
    expect(__workflowsInternal.getAttentionPriority(runs[1])).toBe(1);
    expect(__workflowsInternal.getAttentionPriority(runs[2])).toBe(3);

    const sorted = __workflowsInternal.sortByAttention(runs);
    expect(sorted.map((r) => r.id)).toEqual(["r1", "r2", "r3"]);

    const emptyStats = __workflowsInternal.computeWorkflowStats([]);
    expect(emptyStats).toEqual({
      successRate: 0,
      lastRunStatus: null,
      lastRunTime: null,
      sparkline: [],
    });

    const stats = __workflowsInternal.computeWorkflowStats(runs);
    expect(stats.successRate).toBe(100);
    expect(stats.lastRunStatus).toBe("running");
    expect(stats.sparkline).toEqual(["running", "running", "queued", "succeeded"]);
  });
});

describe("useWorkflows hooks", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000123");
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("useWorkflows fetches and maps backend workflows", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/workflows?org_id=org-1",
        method: "GET",
        body: [{ id: "wf-1", name: "Workflow 1", steps: {} }],
      },
    ]);
    const hook = renderWithQueryClient(() => useWorkflows({ orgId: "org-1" }));

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    expect(hook.result.current?.data?.[0]).toMatchObject({
      id: "wf-1",
      name: "Workflow 1",
    });
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    hook.unmount();
  });

  it("useWorkflow fetches single workflow by id", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/workflows/wf-99",
        method: "GET",
        body: { id: "wf-99", name: "Single Workflow", steps: {} },
      },
    ]);
    const hook = renderWithQueryClient(() => useWorkflow("wf-99"));

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    expect(hook.result.current?.data).toMatchObject({ id: "wf-99", name: "Single Workflow" });
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    hook.unmount();
  });

  it("useCreateWorkflow posts to /workflows and invalidates caches", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/workflows",
        method: "POST",
        body: { id: "wf-created" },
      },
    ]);
    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useCreateWorkflow(), queryClient);

    await act(async () => {
      await hook.result.current?.mutateAsync({
        name: "Created Workflow",
        steps: [{ id: "step-a", name: "Step A", type: "agent-task" }],
      });
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit];
    const payload = JSON.parse(String(init.body)) as Record<string, unknown>;
    expect(payload.name).toBe("Created Workflow");
    expect(payload.steps).toMatchObject({
      "step-a": { id: "step-a", name: "Step A", type: "job" },
    });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["workflows"] });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["workflow", "wf-created"] });
    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "Workflow created" });

    hook.unmount();
  });
});

