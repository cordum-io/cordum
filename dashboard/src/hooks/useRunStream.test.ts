import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { createTestQueryClient, renderWithQueryClient } from "./__tests__/test-utils";
import { __runStreamInternal, useRunStream } from "./useRunStream";
import { useEventStore } from "../state/events";
import type { StreamEvent, WorkflowRun } from "../api/types";
import type { WorkflowRunListResponse } from "./useWorkflows";

const { loggerMock } = vi.hoisted(() => ({
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

function baseRun(): WorkflowRun {
  return {
    id: "run-1",
    workflowId: "wf-1",
    status: "running",
    startedAt: "2026-02-13T10:00:00.000Z",
    updatedAt: "2026-02-13T10:00:00.000Z",
    steps: [
      {
        id: "step-1",
        name: "step-1",
        type: "job",
        status: "running",
        config: { jobId: "job-1" },
        output: { job_id: "job-1" },
      },
    ],
  };
}

function setRunCaches(queryClient: QueryClient, run: WorkflowRun): void {
  queryClient.setQueryData(["workflow-run", run.id], run);
  queryClient.setQueryData(["workflow-runs", run.workflowId], [run]);
  queryClient.setQueryData<WorkflowRunListResponse>(["workflow-runs", "active"], { items: [run] });
}

function event(input: Partial<StreamEvent>): StreamEvent {
  return {
    id: "ev-default",
    type: "run.status_changed",
    timestamp: "2026-02-13T10:01:00.000Z",
    payload: {},
    ...input,
  };
}

describe("useRunStream internals", () => {
  it("isRunEvent supports exact match, prefix, and unknown", () => {
    expect(__runStreamInternal.isRunEvent(event({ type: "run.status_changed" }))).toBe(true);
    expect(__runStreamInternal.isRunEvent(event({ type: "job.result.failed.extra" }))).toBe(true);
    expect(__runStreamInternal.isRunEvent(event({ type: "system.alert" }))).toBe(false);
  });

  it("extract helpers read canonical and snake-case fields", () => {
    const e = event({
      payload: {
        run_id: "run-1",
        step_id: "step-1",
        new_status: "failed",
        job_id: "job-1",
      },
    });

    expect(__runStreamInternal.extractRunId(e)).toBe("run-1");
    expect(__runStreamInternal.extractStepId(e)).toBe("step-1");
    expect(__runStreamInternal.extractStatus(e)).toBe("failed");
    expect(__runStreamInternal.extractJobId(e)).toBe("job-1");
  });

  it("patchRunInCache updates individual, array, and active response caches", () => {
    const queryClient = createTestQueryClient();
    const run = baseRun();
    setRunCaches(queryClient, run);

    __runStreamInternal.patchRunInCache(queryClient, "run-1", (r) => ({
      ...r,
      status: "succeeded",
    }));

    expect(queryClient.getQueryData<WorkflowRun>(["workflow-run", "run-1"])?.status).toBe("succeeded");
    expect(queryClient.getQueryData<WorkflowRun[]>(["workflow-runs", "wf-1"])?.[0]?.status).toBe("succeeded");
    expect(queryClient.getQueryData<WorkflowRunListResponse>(["workflow-runs", "active"])?.items[0]?.status).toBe("succeeded");
  });
});

describe("useRunStream hook", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useEventStore.setState({ events: [], safetyDecisions: [], status: "disconnected" });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("processes step-level status changes", async () => {
    const queryClient = createTestQueryClient();
    setRunCaches(queryClient, baseRun());
    const hook = renderWithQueryClient(() => useRunStream("run-1"), queryClient);

    act(() => {
      useEventStore.getState().addEvent(
        event({
          id: "ev-step",
          type: "run.step_status_changed",
          payload: { runId: "run-1", stepId: "step-1", status: "failed", errorMessage: "boom" },
        }),
      );
    });

    await hook.waitFor(() => {
      expect(queryClient.getQueryData<WorkflowRun>(["workflow-run", "run-1"])?.steps[0].status).toBe("failed");
    });
    expect(queryClient.getQueryData<WorkflowRun>(["workflow-run", "run-1"])?.steps[0].error).toBe("boom");

    hook.unmount();
  });

  it("processes run-level status changes", async () => {
    const queryClient = createTestQueryClient();
    setRunCaches(queryClient, baseRun());
    const hook = renderWithQueryClient(() => useRunStream("run-1"), queryClient);

    act(() => {
      useEventStore.getState().addEvent(
        event({
          id: "ev-run",
          type: "run.status_changed",
          timestamp: "2026-02-13T10:05:00.000Z",
          payload: { runId: "run-1", status: "succeeded" },
        }),
      );
    });

    await hook.waitFor(() => {
      expect(queryClient.getQueryData<WorkflowRun>(["workflow-run", "run-1"])?.status).toBe("succeeded");
    });
    expect(queryClient.getQueryData<WorkflowRun>(["workflow-run", "run-1"])?.completedAt).toBe("2026-02-13T10:05:00.000Z");

    hook.unmount();
  });

  it("maps job.result events to matching step by jobId", async () => {
    const queryClient = createTestQueryClient();
    setRunCaches(queryClient, baseRun());
    const hook = renderWithQueryClient(() => useRunStream("run-1"), queryClient);

    act(() => {
      useEventStore.getState().addEvent(
        event({
          id: "ev-job",
          type: "job.result.failed",
          payload: { runId: "run-1", jobId: "job-1", status: "failed", errorMessage: "job failed" },
        }),
      );
    });

    await hook.waitFor(() => {
      expect(queryClient.getQueryData<WorkflowRun>(["workflow-run", "run-1"])?.steps[0].status).toBe("failed");
    });
    expect(queryClient.getQueryData<WorkflowRun>(["workflow-run", "run-1"])?.steps[0].error).toBe("job failed");

    hook.unmount();
  });

  it("deduplicates repeated event ids", async () => {
    const queryClient = createTestQueryClient();
    setRunCaches(queryClient, baseRun());
    const hook = renderWithQueryClient(() => useRunStream("run-1"), queryClient);

    act(() => {
      useEventStore.getState().addEvent(
        event({
          id: "ev-dupe",
          type: "run.status_changed",
          payload: { runId: "run-1", status: "succeeded" },
        }),
      );
      useEventStore.getState().addEvent(
        event({
          id: "ev-dupe",
          type: "run.status_changed",
          payload: { runId: "run-1", status: "failed" },
        }),
      );
    });

    await hook.waitFor(() => {
      expect(queryClient.getQueryData<WorkflowRun>(["workflow-run", "run-1"])?.status).toBe("succeeded");
    });

    hook.unmount();
  });

  it("skips events for other run ids when runId filter is set", async () => {
    const queryClient = createTestQueryClient();
    setRunCaches(queryClient, baseRun());
    const hook = renderWithQueryClient(() => useRunStream("run-1"), queryClient);

    act(() => {
      useEventStore.getState().addEvent(
        event({
          id: "ev-other",
          type: "run.status_changed",
          payload: { runId: "run-2", status: "failed" },
        }),
      );
    });

    await hook.waitFor(() => {
      expect(queryClient.getQueryData<WorkflowRun>(["workflow-run", "run-1"])?.status).toBe("running");
    });

    hook.unmount();
  });
});
