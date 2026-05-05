import { Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentActionEvent, AgentExecution, EdgeSession } from "@/api/types";
import { fireEvent, renderWithProviders, screen, within } from "@/test-utils/render";
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
    setupHooks({
      events: [
        makeEvent({ toolUseId: "use-A" }),
        makeEvent({ eventId: "edge_evt_2", seq: 2, decision: "ALLOW", toolUseId: "use-B" }),
      ],
    });
    renderPage();
    expect(screen.getByText("edge_sess_1")).toBeTruthy();
    expect(screen.getByTestId("edge-session-facts").textContent).toContain("user-a");
    expect(screen.getByTestId("edge-session-facts").textContent).toContain("claude-code");
    // EDGE-050: each distinct toolUseId becomes its own collapsed group.
    const groups = screen.getAllByTestId("edge-event-group");
    expect(groups).toHaveLength(2);
    expect(screen.getByText(/2 events/)).toBeTruthy();
  });

  it("orders events by seq within an execution (one group per distinct hook fire)", () => {
    setupHooks({
      events: [
        // Two events with DIFFERENT toolUseId so they don't collapse
        // into one group — each becomes its own headline row.
        makeEvent({ eventId: "edge_evt_2", seq: 2, decision: "ALLOW", toolUseId: "use-2" }),
        makeEvent({ eventId: "edge_evt_1", seq: 1, decision: "DENY", toolUseId: "use-1" }),
      ],
    });
    renderPage();
    const groups = screen.getAllByTestId("edge-event-group");
    // Two groups, one per distinct toolUseId. EDGE-050 grouping preserves
    // chronological order via the underlying sortByOrder + selector
    // single-pass walk.
    expect(groups).toHaveLength(2);
  });

  it("filters events by decision (filter applies before grouping)", () => {
    setupHooks({
      events: [
        makeEvent({ eventId: "edge_evt_1", decision: "DENY", toolUseId: "use-A" }),
        makeEvent({ eventId: "edge_evt_2", seq: 2, decision: "ALLOW", toolUseId: "use-B" }),
      ],
    });
    renderPage();
    fireEvent.change(screen.getByTestId("edge-filter-decision"), { target: { value: "DENY" } });
    const groups = screen.getAllByTestId("edge-event-group");
    expect(groups).toHaveLength(1);
  });

  it("opens the event inspector when a single-event group headline is clicked", () => {
    setupHooks({ events: [makeEvent()] });
    renderPage();
    expect(screen.queryByTestId("edge-event-inspector")).toBeNull();
    // Single-event group: clicking the headline opens the inspector
    // directly (no caret because there's nothing to expand).
    fireEvent.click(screen.getAllByTestId("edge-event-group-headline")[0]);
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

  // EDGE-050 — 3-events-per-hook collapse + expand + divergence chip.

  it("collapses 3 events from one hook fire into a single group row", () => {
    setupHooks({
      events: [
        makeEvent({
          eventId: "agentd-receipt-A",
          seq: 1,
          ts: "2026-05-02T16:00:00.100Z",
          kind: "hook.pre_tool_use",
          toolUseId: "tool-A",
          status: "degraded",
          decisionReason: "received by cordum-agentd; evaluation not ready",
        }),
        makeEvent({
          eventId: "evt-gateway-A",
          seq: 2,
          ts: "2026-05-02T16:00:00.200Z",
          kind: "hook.policy_decision",
          toolUseId: "tool-A",
          decision: "DENY",
          ruleId: "claude-code.deny-secret-reads",
          policySnapshot: "policy-v3",
        }),
        makeEvent({
          eventId: "agentd-evidence-A",
          seq: 3,
          ts: "2026-05-02T16:00:00.300Z",
          kind: "hook.policy_decision",
          toolUseId: "tool-A",
          decision: "DENY",
          ruleId: "claude-code.deny-secret-reads",
          policySnapshot: "policy-v3",
        }),
      ],
    });
    renderPage();
    const groups = screen.getAllByTestId("edge-event-group");
    expect(groups).toHaveLength(1);
    // Headline reports 3 events from the underlying hook fire.
    expect(within(groups[0]).getByTestId("edge-event-group-count").textContent).toContain("3 events");
  });

  it("expand caret reveals all 3 underlying witness events", () => {
    setupHooks({
      events: [
        makeEvent({
          eventId: "agentd-receipt-B",
          seq: 1,
          ts: "2026-05-02T16:01:00.100Z",
          kind: "hook.pre_tool_use",
          toolUseId: "tool-B",
          status: "degraded",
          decisionReason: "received by cordum-agentd; evaluation not ready",
        }),
        makeEvent({
          eventId: "evt-gateway-B",
          seq: 2,
          ts: "2026-05-02T16:01:00.200Z",
          kind: "hook.policy_decision",
          toolUseId: "tool-B",
          decision: "ALLOW",
        }),
        makeEvent({
          eventId: "agentd-evidence-B",
          seq: 3,
          ts: "2026-05-02T16:01:00.300Z",
          kind: "hook.policy_decision",
          toolUseId: "tool-B",
          decision: "ALLOW",
        }),
      ],
    });
    renderPage();
    expect(screen.queryByTestId("edge-event-group-expanded")).toBeNull();
    fireEvent.click(screen.getByTestId("edge-event-group-headline"));
    const expanded = screen.getByTestId("edge-event-group-expanded");
    const witnesses = within(expanded).getAllByTestId("edge-event-group-witness");
    expect(witnesses).toHaveLength(3);
    const ids = witnesses.map((w) => w.getAttribute("data-event-id"));
    expect(ids).toContain("agentd-receipt-B");
    expect(ids).toContain("evt-gateway-B");
    expect(ids).toContain("agentd-evidence-B");
  });

  it("renders an audit-divergence chip when gateway and agentd disagree", () => {
    setupHooks({
      events: [
        makeEvent({
          eventId: "evt-gateway-D",
          seq: 1,
          ts: "2026-05-02T16:02:00.000Z",
          kind: "hook.policy_decision",
          toolUseId: "tool-D",
          decision: "ALLOW",
          ruleId: "rule-1",
          policySnapshot: "policy-v3",
        }),
        makeEvent({
          eventId: "agentd-evidence-D",
          seq: 2,
          ts: "2026-05-02T16:02:00.150Z",
          kind: "hook.policy_decision",
          toolUseId: "tool-D",
          decision: "DENY", // disagrees with gateway
          ruleId: "rule-1",
          policySnapshot: "policy-v3",
        }),
      ],
    });
    renderPage();
    const group = screen.getByTestId("edge-event-group");
    expect(group.getAttribute("data-divergent")).toBe("true");
    expect(within(group).getByTestId("edge-event-divergence-chip")).toBeTruthy();
  });

  it("toggle 'show pre-evaluation receipts' reveals receipt summary on collapsed groups", () => {
    setupHooks({
      events: [
        makeEvent({
          eventId: "agentd-receipt-T",
          seq: 1,
          ts: "2026-05-02T16:03:00.000Z",
          kind: "hook.pre_tool_use",
          toolUseId: "tool-T",
          status: "degraded",
          decisionReason: "received by cordum-agentd; evaluation not ready",
        }),
        makeEvent({
          eventId: "evt-gateway-T",
          seq: 2,
          ts: "2026-05-02T16:03:00.150Z",
          kind: "hook.policy_decision",
          toolUseId: "tool-T",
          decision: "ALLOW",
        }),
      ],
    });
    renderPage();
    expect(screen.queryByTestId("edge-event-group-receipt-summary")).toBeNull();
    fireEvent.click(screen.getByTestId("edge-toggle-receipts"));
    expect(screen.getByTestId("edge-event-group-receipt-summary")).toBeTruthy();
  });
});
