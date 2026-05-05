import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentExecution, EdgeSession } from "@/api/types";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test-utils/render";
import {
  AgentExecutionsPanel,
  edgeSessionDetailPath,
  executionMatchesScope,
} from "./AgentExecutionsPanel";

const { fetchEdgeExecutionsMock, fetchEdgeSessionMock, edgeErrorFromApiErrorMock } = vi.hoisted(() => ({
  fetchEdgeExecutionsMock: vi.fn(),
  fetchEdgeSessionMock: vi.fn(),
  edgeErrorFromApiErrorMock: vi.fn(),
}));

vi.mock("@/hooks/useEdgeSessions", () => ({
  fetchEdgeExecutions: fetchEdgeExecutionsMock,
  fetchEdgeSession: fetchEdgeSessionMock,
  edgeErrorFromApiError: edgeErrorFromApiErrorMock,
}));

function makeExecution(overrides: Partial<AgentExecution> = {}): AgentExecution {
  return {
    executionId: "exec-1",
    sessionId: "edge_sess_1",
    tenantId: "tenant-a",
    adapter: "claude-code-hook",
    mode: "local-dev",
    workflowRunId: "run-1",
    stepId: "step-1",
    jobId: "job-1",
    status: "running",
    startedAt: "2026-05-02T10:00:01.000Z",
    metrics: {
      events: 6,
      allow: 3,
      deny: 1,
      requireApproval: 2,
    },
    ...overrides,
  };
}

function makeSession(overrides: Partial<EdgeSession> = {}): EdgeSession {
  return {
    sessionId: "edge_sess_1",
    tenantId: "tenant-a",
    principalType: "human",
    mode: "local-dev",
    traceId: "trace-1",
    policyMode: "observe",
    status: "running",
    riskSummary: {
      deniedCount: 1,
      approvalCount: 2,
      artifactCount: 0,
      maxRisk: "high",
    },
    startedAt: "2026-05-02T10:00:00.000Z",
    ...overrides,
  };
}

describe("AgentExecutionsPanel", () => {
  beforeEach(() => {
    fetchEdgeExecutionsMock.mockReset();
    fetchEdgeSessionMock.mockReset();
    edgeErrorFromApiErrorMock.mockReset();
  });

  it("stays silent when the linked job has no Edge executions", async () => {
    fetchEdgeExecutionsMock.mockResolvedValue({ items: [], nextCursor: null });

    renderWithProviders(<AgentExecutionsPanel jobId="job-1" />);

    await waitFor(() => {
      expect(fetchEdgeExecutionsMock).toHaveBeenCalledWith(expect.objectContaining({ jobId: "job-1" }));
    });
    expect(screen.queryByLabelText("Linked Agent Executions")).toBeNull();
    expect(screen.queryByText("Agent Executions")).toBeNull();
  });

  it("renders job-linked executions with status, adapter, decisions, max risk, and session link", async () => {
    fetchEdgeExecutionsMock.mockResolvedValue({ items: [makeExecution()], nextCursor: null });
    fetchEdgeSessionMock.mockResolvedValue(makeSession({ riskSummary: { deniedCount: 1, approvalCount: 2, artifactCount: 0, maxRisk: "high" } }));

    renderWithProviders(<AgentExecutionsPanel jobId="job-1" />);

    expect(await screen.findByRole("heading", { name: "Agent Executions" })).not.toBeNull();
    expect(screen.getByText("running")).not.toBeNull();
    expect(screen.getByText("claude-code-hook")).not.toBeNull();
    expect(screen.getByText("allow 3")).not.toBeNull();
    expect(screen.getByText("deny 1")).not.toBeNull();
    expect(screen.getByText("approval 2")).not.toBeNull();
    expect(screen.getByText("high")).not.toBeNull();
    expect(screen.getByRole("link", { name: /session/i }).getAttribute("href")).toBe("/edge/sessions/edge_sess_1");
  });

  it("renders workflow-run linked executions with step context where available", async () => {
    fetchEdgeExecutionsMock.mockResolvedValue({
      items: [makeExecution({ jobId: undefined, workflowRunId: "run-1", stepId: "step-review", status: "succeeded" })],
      nextCursor: null,
    });
    fetchEdgeSessionMock.mockResolvedValue(makeSession({ riskSummary: { deniedCount: 0, approvalCount: 0, artifactCount: 1, maxRisk: "critical" } }));

    renderWithProviders(<AgentExecutionsPanel workflowRunId="run-1" />);

    expect(await screen.findByText("succeeded")).not.toBeNull();
    expect(screen.getByText("step step-review")).not.toBeNull();
    expect(screen.getByText("critical")).not.toBeNull();
    expect(fetchEdgeExecutionsMock).toHaveBeenCalledWith(expect.objectContaining({ workflowRunId: "run-1" }));
  });

  it("falls back to a compact warning when the linked evidence query fails", async () => {
    fetchEdgeExecutionsMock.mockRejectedValue(new Error("backend down"));

    renderWithProviders(<AgentExecutionsPanel jobId="job-1" />);

    expect(await screen.findByText("Edge evidence unavailable")).not.toBeNull();
    expect(screen.getByText("backend down")).not.toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => {
      expect(fetchEdgeExecutionsMock).toHaveBeenCalledTimes(2);
    });
  });

  it("does not show ghost executions returned for an unrelated job", async () => {
    fetchEdgeExecutionsMock.mockResolvedValue({
      items: [makeExecution({ jobId: "other-job", executionId: "exec-ghost" })],
      nextCursor: null,
    });

    renderWithProviders(<AgentExecutionsPanel jobId="job-1" />);

    await waitFor(() => {
      expect(fetchEdgeExecutionsMock).toHaveBeenCalled();
    });
    expect(fetchEdgeSessionMock).not.toHaveBeenCalled();
    expect(screen.queryByText("exec-ghost")).toBeNull();
    expect(screen.queryByLabelText("Linked Agent Executions")).toBeNull();
  });
});

describe("AgentExecutionsPanel helpers", () => {
  it("builds encoded Edge session detail paths", () => {
    expect(edgeSessionDetailPath("sess/1")).toBe("/edge/sessions/sess%2F1");
  });

  it("requires linked executions to match the requested scope", () => {
    expect(executionMatchesScope(makeExecution(), { jobId: "job-1" })).toBe(true);
    expect(executionMatchesScope(makeExecution(), { workflowRunId: "run-1" })).toBe(true);
    expect(executionMatchesScope(makeExecution(), { jobId: "other" })).toBe(false);
  });
});
