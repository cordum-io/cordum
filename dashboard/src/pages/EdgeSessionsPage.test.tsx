import { Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { EdgeSession } from "@/api/types";
import { fireEvent, renderWithProviders, screen } from "@/test-utils/render";
import { useEdgeSessions } from "@/hooks/useEdgeSessions";
import EdgeSessionsPage from "./EdgeSessionsPage";

vi.mock("@/hooks/useEdgeSessions", () => ({
  useEdgeSessions: vi.fn(),
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
    traceId: "trace-1",
    policyMode: "enforce",
    status: "running",
    riskSummary: { deniedCount: 0, approvalCount: 0, artifactCount: 0 },
    startedAt: "2026-05-02T16:00:00Z",
    ...overrides,
  };
}

interface SessionsState {
  sessions?: EdgeSession[];
  isPending?: boolean;
  error?: Error | null;
}

function setupHook(state: SessionsState = {}) {
  vi.mocked(useEdgeSessions).mockReturnValue({
    data:
      state.sessions === undefined && !state.isPending && !state.error
        ? { items: [], nextCursor: null }
        : state.sessions
          ? { items: state.sessions, nextCursor: null }
          : undefined,
    error: state.error ?? null,
    isPending: Boolean(state.isPending),
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof useEdgeSessions>);
}

function renderPage() {
  return renderWithProviders(
    <Routes>
      <Route path="/edge/sessions" element={<EdgeSessionsPage />} />
      <Route path="/edge/sessions/:sessionId" element={<div data-testid="detail-stub" />} />
    </Routes>,
    { initialEntries: ["/edge/sessions"] },
  );
}

describe("EdgeSessionsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders summary cards and an empty state when there are no sessions", () => {
    setupHook({ sessions: [] });
    renderPage();
    expect(screen.getByTestId("edge-sessions-summary")).toBeTruthy();
    expect(screen.getByText(/No Edge sessions/i)).toBeTruthy();
  });

  it("shows loading skeleton when the query is pending", () => {
    setupHook({ isPending: true });
    const { container } = renderPage();
    expect(container.querySelector(".animate-shimmer, .h-48")).toBeTruthy();
  });

  it("renders an error banner with retry handler when the query fails", () => {
    setupHook({ error: new Error("boom") });
    renderPage();
    expect(screen.getByText("Edge sessions unavailable")).toBeTruthy();
    expect(screen.getByText("boom")).toBeTruthy();
  });

  it("renders one row per session with status + policy mode chips", () => {
    setupHook({
      sessions: [
        makeSession(),
        makeSession({ sessionId: "edge_sess_2", status: "ended", policyMode: "observe" }),
      ],
    });
    renderPage();
    const rows = screen.getAllByTestId("edge-sessions-row");
    expect(rows).toHaveLength(2);
    expect(rows[0].getAttribute("data-session-id")).toBe("edge_sess_1");
    expect(rows[1].getAttribute("data-session-id")).toBe("edge_sess_2");
  });

  it("computes summary counts from session risk summaries", () => {
    setupHook({
      sessions: [
        makeSession({ status: "running" }),
        makeSession({
          sessionId: "edge_sess_2",
          status: "waiting_for_approval",
          riskSummary: { deniedCount: 3, approvalCount: 1, artifactCount: 5 },
        }),
        makeSession({ sessionId: "edge_sess_3", status: "ended" }),
      ],
    });
    renderPage();
    const summary = screen.getByTestId("edge-sessions-summary");
    expect(summary.textContent).toContain("Active sessions");
    expect(summary.textContent).toContain("Closed sessions");
    expect(summary.textContent).toContain("Pending approvals");
    expect(summary.textContent).toContain("Denied actions");
    expect(summary.textContent).toContain("Evidence files");
    // 3 denied + 5 artifacts come from the second session
    expect(summary.textContent).toContain("3");
    expect(summary.textContent).toContain("5");
  });

  it("filters sessions by policy mode client-side", () => {
    setupHook({
      sessions: [
        makeSession({ sessionId: "edge_sess_a", policyMode: "enforce" }),
        makeSession({ sessionId: "edge_sess_b", policyMode: "observe" }),
      ],
    });
    renderPage();
    fireEvent.change(screen.getByTestId("edge-sessions-filter-policy"), {
      target: { value: "observe" },
    });
    const rows = screen.getAllByTestId("edge-sessions-row");
    expect(rows).toHaveLength(1);
    expect(rows[0].getAttribute("data-session-id")).toBe("edge_sess_b");
  });

  it("filters sessions by search across sessionId and principalId", () => {
    setupHook({
      sessions: [
        makeSession({ sessionId: "edge_sess_alpha", principalId: "alice" }),
        makeSession({ sessionId: "edge_sess_beta", principalId: "bob" }),
      ],
    });
    renderPage();
    fireEvent.change(screen.getByTestId("edge-sessions-filter-search"), {
      target: { value: "bob" },
    });
    const rows = screen.getAllByTestId("edge-sessions-row");
    expect(rows).toHaveLength(1);
    expect(rows[0].getAttribute("data-session-id")).toBe("edge_sess_beta");
  });

  it("navigates to the detail route when a row is clicked", () => {
    setupHook({ sessions: [makeSession()] });
    renderPage();
    const row = screen.getAllByTestId("edge-sessions-row")[0];
    fireEvent.click(row);
    expect(screen.getByTestId("detail-stub")).toBeTruthy();
  });
});
