import { fireEvent, screen, within } from "@testing-library/react";
import { Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentActionEvent, AgentExecution, EdgeSession } from "@/api/types";
import { renderWithProviders } from "@/test-utils/render";
import {
  useApproveEdgeApproval,
  useEdgeApprovals,
  useEdgeExecutions,
  useEdgeSession,
  useEdgeSessionEvents,
  useExportEdgeSession,
  useRejectEdgeApproval,
} from "@/hooks/useEdgeSessions";
import EdgeSessionDetailPage from "./EdgeSessionDetailPage";

vi.mock("@/hooks/useEdgeSessions", () => ({
  useEdgeSession: vi.fn(),
  useEdgeSessionEvents: vi.fn(),
  useEdgeExecutions: vi.fn(),
  useEdgeApprovals: vi.fn(),
  useApproveEdgeApproval: vi.fn(),
  useRejectEdgeApproval: vi.fn(),
  useExportEdgeSession: vi.fn(),
}));

function makeSession(overrides: Partial<EdgeSession> = {}): EdgeSession {
  return {
    sessionId: "edge_sess_1",
    tenantId: "tenant-a",
    principalId: "user-a",
    principalType: "user",
    agentProduct: "claude-code",
    agentVersion: "1.0",
    mode: "local-dev",
    repo: "github.com/cordum-io/cordum",
    gitRemote: "origin",
    gitBranch: "feature/cordum-edge-p0",
    cwd: "/repo",
    traceId: "trace-1",
    policySnapshot: "policy-v3",
    enforcementLayers: { hook: true },
    policyMode: "enforce",
    status: "running",
    riskSummary: { deniedCount: 0, approvalCount: 0, artifactCount: 0 },
    startedAt: "2026-05-02T16:00:00Z",
    ...overrides,
  };
}

function makeEvent(overrides: Partial<AgentActionEvent> = {}): AgentActionEvent {
  return {
    eventId: "edge_evt_1",
    sessionId: "edge_sess_1",
    executionId: "edge_exec_a",
    tenantId: "tenant-a",
    principalId: "user-a",
    seq: 1,
    ts: "2026-05-02T16:00:01Z",
    layer: "pre_tool_use",
    kind: "hook.pre_tool_use",
    toolName: "Read",
    capability: "filesystem.read",
    riskTags: ["secret_access"],
    inputRedacted: { path_class: "secret" },
    inputHash: "hash-1",
    decision: "DENY",
    decisionReason: "deny-secret-reads",
    ruleId: "rule-1",
    policySnapshot: "policy-v3",
    artifactPtrs: [],
    status: "recorded",
    ...overrides,
  };
}

const execution: AgentExecution = {
  executionId: "edge_exec_a",
  sessionId: "edge_sess_1",
  tenantId: "tenant-a",
  adapter: "claude-code-hook",
  mode: "local-dev",
  status: "running",
  startedAt: "2026-05-02T16:00:00Z",
};

function setupHooks(opts: {
  session?: EdgeSession | null;
  events?: AgentActionEvent[];
  executions?: AgentExecution[];
  sessionPending?: boolean;
  sessionError?: Error | null;
} = {}) {
  const session = opts.session === undefined ? makeSession() : opts.session;
  const events = opts.events ?? [];
  const executions = opts.executions ?? [execution];
  vi.mocked(useEdgeSession).mockReturnValue({
    data: session ?? undefined,
    error: opts.sessionError ?? null,
    isPending: Boolean(opts.sessionPending),
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof useEdgeSession>);
  vi.mocked(useEdgeSessionEvents).mockReturnValue({
    data: { items: events, nextCursor: null },
    error: null,
    isPending: false,
  } as unknown as ReturnType<typeof useEdgeSessionEvents>);
  vi.mocked(useEdgeExecutions).mockReturnValue({
    data: { items: executions, nextCursor: null },
    error: null,
    isPending: false,
  } as unknown as ReturnType<typeof useEdgeExecutions>);
  vi.mocked(useEdgeApprovals).mockReturnValue({
    data: { items: [], nextCursor: null },
    error: null,
    isPending: false,
  } as unknown as ReturnType<typeof useEdgeApprovals>);
  vi.mocked(useApproveEdgeApproval).mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
    error: null,
  } as unknown as ReturnType<typeof useApproveEdgeApproval>);
  vi.mocked(useRejectEdgeApproval).mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
    error: null,
  } as unknown as ReturnType<typeof useRejectEdgeApproval>);
  vi.mocked(useExportEdgeSession).mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
    data: undefined,
    error: null,
  } as unknown as ReturnType<typeof useExportEdgeSession>);
}

function renderPage(initialEntries: string[] = ["/edge/sessions/edge_sess_1"]) {
  return renderWithProviders(
    <Routes>
      <Route path="/edge/sessions/:sessionId" element={<EdgeSessionDetailPage />} />
    </Routes>,
    { initialEntries },
  );
}

describe("EdgeSessionDetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders session metadata and timeline header", () => {
    setupHooks({ events: [makeEvent(), makeEvent({ eventId: "edge_evt_2", seq: 2, decision: "ALLOW" })] });
    renderPage();
    expect(screen.getByText("edge_sess_1")).toBeTruthy();
    expect(screen.getByTestId("edge-session-facts").textContent).toContain("user-a");
    expect(screen.getByTestId("edge-session-facts").textContent).toContain("claude-code");
    const rows = screen.getAllByTestId("edge-event-row");
    expect(rows).toHaveLength(2);
    expect(screen.getByText(/2 events/)).toBeTruthy();
  });

  it("orders events by seq within an execution", () => {
    setupHooks({
      events: [
        makeEvent({ eventId: "edge_evt_2", seq: 2, decision: "ALLOW" }),
        makeEvent({ eventId: "edge_evt_1", seq: 1, decision: "DENY" }),
      ],
    });
    renderPage();
    const rows = screen.getAllByTestId("edge-event-row");
    expect(rows[0].getAttribute("data-event-id")).toBe("edge_evt_1");
    expect(rows[1].getAttribute("data-event-id")).toBe("edge_evt_2");
  });

  it("filters events by decision", () => {
    setupHooks({
      events: [
        makeEvent({ eventId: "edge_evt_1", decision: "DENY" }),
        makeEvent({ eventId: "edge_evt_2", seq: 2, decision: "ALLOW" }),
      ],
    });
    renderPage();
    fireEvent.change(screen.getByTestId("edge-filter-decision"), { target: { value: "DENY" } });
    const rows = screen.getAllByTestId("edge-event-row");
    expect(rows).toHaveLength(1);
    expect(rows[0].getAttribute("data-event-id")).toBe("edge_evt_1");
  });

  it("opens the event inspector when a timeline row is clicked", () => {
    setupHooks({ events: [makeEvent()] });
    renderPage();
    expect(screen.queryByTestId("edge-event-inspector")).toBeNull();
    fireEvent.click(screen.getAllByTestId("edge-event-row")[0]);
    const inspector = screen.getByTestId("edge-event-inspector");
    expect(within(inspector).getByTestId("edge-event-id").textContent).toContain("edge_evt_1");
  });

  it("renders an empty state when no events match filter", () => {
    setupHooks({
      events: [
        makeEvent({ eventId: "edge_evt_1", decision: "ALLOW", kind: "hook.pre_tool_use" }),
        makeEvent({ eventId: "edge_evt_2", seq: 2, decision: "DENY", kind: "hook.post_tool_use" }),
      ],
    });
    renderPage();
    // ALLOW event is pre_tool_use; DENY event is post_tool_use. Filtering
    // decision=ALLOW + kind=hook.post_tool_use eliminates both.
    fireEvent.change(screen.getByTestId("edge-filter-decision"), { target: { value: "ALLOW" } });
    fireEvent.change(screen.getByTestId("edge-filter-kind"), { target: { value: "hook.post_tool_use" } });
    expect(screen.getByText(/No events match/i)).toBeTruthy();
  });

  it("renders an error banner when the session query fails", () => {
    setupHooks({ session: null, sessionError: new Error("boom"), sessionPending: false });
    renderPage();
    expect(screen.getByText("Edge session unavailable")).toBeTruthy();
    expect(screen.getByText("boom")).toBeTruthy();
  });
});
