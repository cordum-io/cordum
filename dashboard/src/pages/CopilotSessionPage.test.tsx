import { describe, expect, it, beforeEach, vi } from "vitest";
import { Routes, Route } from "react-router-dom";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test-utils/render";
import { http, HttpResponse, server } from "@/test-utils/msw";
import CopilotSessionPage from "./CopilotSessionPage";

const SESSION_ID = "sess-abc123";

function renderRoute(initial: string) {
  return renderWithProviders(
    <Routes>
      <Route path="/copilot/sessions/:sessionId" element={<CopilotSessionPage />} />
      <Route path="/jobs" element={<div data-testid="jobs-page-stub">Jobs</div>} />
    </Routes>,
    { initialEntries: [initial] },
  );
}

describe("CopilotSessionPage", () => {
  beforeEach(() => {
    server.resetHandlers();
  });

  it("renders the full sessionId verbatim in the header", async () => {
    server.use(
      http.get("*/api/v1/copilot/sessions/:sessionId", ({ params }) =>
        HttpResponse.json(makeSessionResponse(String(params.sessionId))),
      ),
    );
    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    await waitFor(() => {
      expect(screen.queryByText(SESSION_ID)).not.toBeNull();
    });
  });

  it("renders the session timeline with the stable testid", async () => {
    server.use(
      http.get("*/api/v1/copilot/sessions/:sessionId", ({ params }) =>
        HttpResponse.json(makeSessionResponse(String(params.sessionId))),
      ),
    );
    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    await waitFor(() => {
      expect(screen.queryByTestId("copilot-session-timeline")).not.toBeNull();
    });
  });

  it("renders messages, per-turn job chips, governance decisions, and linked jobs from the dedicated endpoint", async () => {
    server.use(
      http.get("*/api/v1/copilot/sessions/:sessionId", () =>
        HttpResponse.json(
          makeSessionResponse(SESSION_ID, {
            messages: [
              {
                id: "msg-1",
                role: "user",
                content: "why did deployment fail?",
                timestamp: "2026-04-26T07:00:00Z",
                jobIds: ["job-1"],
              },
              {
                id: "msg-2",
                role: "assistant",
                content: "The deployment was denied by policy.",
                timestamp: "2026-04-26T07:01:00Z",
                jobIds: ["job-1"],
              },
            ],
            jobs: [
              makeJob({ id: "job-1", topic: "topic.one", status: "denied" }),
              makeJob({ id: "job-2", topic: "topic.two", status: "running" }),
            ],
            decisions: [
              {
                jobId: "job-1",
                topic: "topic.one",
                matchedRule: "rule-deploy-window",
                verdict: "deny",
                reason: "outside deploy window",
                agentId: "agent-7",
                timestamp: "2026-04-26T07:00:15Z",
              },
            ],
          }),
        ),
      ),
    );
    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    await waitFor(() => {
      expect(screen.queryByText("why did deployment fail?")).not.toBeNull();
    });
    expect(screen.queryByText("The deployment was denied by policy.")).not.toBeNull();
    expect(screen.getAllByRole("link", { name: /job-1/i }).length).toBeGreaterThan(0);
    expect(screen.queryByText("rule-deploy-window")).not.toBeNull();
    expect(screen.queryByText("agent-7")).not.toBeNull();
    expect(screen.queryByText("topic.one")).not.toBeNull();
    expect(screen.queryByText("topic.two")).not.toBeNull();
  });

  it("uses only /api/v1/copilot/sessions/:id and does not call the old /jobs?session_id fallback", async () => {
    const jobsHandler = vi.fn(() => HttpResponse.json({ items: [] }));
    server.use(
      http.get("*/api/v1/copilot/sessions/:sessionId", () =>
        HttpResponse.json(
          makeSessionResponse(SESSION_ID, {
            jobs: [makeJob({ id: "job-1", topic: "topic.one" })],
          }),
        ),
      ),
      http.get("*/api/v1/jobs", jobsHandler),
    );

    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    await waitFor(() => {
      expect(screen.queryByText("topic.one")).not.toBeNull();
    });
    expect(jobsHandler).not.toHaveBeenCalled();
  });

  it("still renders per-turn job chips when the referenced job is not in the detail response", async () => {
    // The detail endpoint caps `jobs` at copilotSessionAggregateLimit and may
    // also drop entries whose enriched metadata expired, but `message.jobIds`
    // is the authoritative source of references. The chip still routes to
    // /jobs/:jobId, which only needs the id; hiding the chip would lose a
    // valid drill-in target. The chip must render with "muted" styling so
    // users can distinguish jobs whose enriched metadata loaded.
    server.use(
      http.get("*/api/v1/copilot/sessions/:sessionId", () =>
        HttpResponse.json(
          makeSessionResponse(SESSION_ID, {
            messages: [
              {
                id: "msg-missing-job",
                role: "assistant",
                content: "I could not find the referenced job.",
                timestamp: "2026-04-26T07:01:00Z",
                jobIds: ["missing-job"],
              },
            ],
            jobs: [],
          }),
        ),
      ),
    );

    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    await waitFor(() => {
      expect(screen.queryByText("I could not find the referenced job.")).not.toBeNull();
    });
    const chip = screen.queryByRole("link", { name: /missing-job/i });
    expect(chip).not.toBeNull();
    expect(chip?.getAttribute("href")).toBe("/jobs/missing-job");
  });

  it("renders independent empty states for messages, decisions, and jobs", async () => {
    server.use(
      http.get("*/api/v1/copilot/sessions/:sessionId", ({ params }) =>
        HttpResponse.json(makeSessionResponse(String(params.sessionId))),
      ),
    );
    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    await waitFor(() => {
      expect(screen.queryByText(/no messages yet/i)).not.toBeNull();
    });
    expect(screen.queryByText(/no governance decisions for this session/i)).not.toBeNull();
    expect(screen.queryByText(/no jobs yet/i)).not.toBeNull();
  });

  it("renders the pending-backend banner for a 501 response while preserving the jobs section shell", async () => {
    server.use(
      http.get("*/api/v1/copilot/sessions/:sessionId", () =>
        HttpResponse.json(
          { error: "copilot_store_not_ready", status: 501 },
          { status: 501 },
        ),
      ),
    );

    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    await waitFor(() => {
      expect(
        screen.queryByText(/copilot session details are not available yet/i),
      ).not.toBeNull();
    });
    expect(screen.getByRole("heading", { name: /^linked jobs$/i })).not.toBeNull();
  });

  it("shows a truncated notice when the backend caps large sessions", async () => {
    server.use(
      http.get("*/api/v1/copilot/sessions/:sessionId", () =>
        HttpResponse.json(makeSessionResponse(SESSION_ID, { truncated: true })),
      ),
    );

    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    await waitFor(() => {
      expect(screen.queryByText(/showing first 500 entries/i)).not.toBeNull();
    });
  });

  it("Back to Jobs button navigates to /jobs", async () => {
    server.use(
      http.get("*/api/v1/copilot/sessions/:sessionId", ({ params }) =>
        HttpResponse.json(makeSessionResponse(String(params.sessionId))),
      ),
    );
    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    const backBtn = await waitFor(() =>
      screen.getByRole("button", { name: /back to jobs/i }),
    );
    fireEvent.click(backBtn);
    await waitFor(() => {
      expect(screen.queryByTestId("jobs-page-stub")).not.toBeNull();
    });
  });

  it("renders a friendly error + back button when sessionId is missing/whitespace", async () => {
    renderRoute("/copilot/sessions/%20");
    await waitFor(() => {
      expect(screen.queryByText(/missing session id/i)).not.toBeNull();
    });
    expect(screen.queryByRole("button", { name: /back to jobs/i })).not.toBeNull();
  });
});

function makeSessionResponse(
  id: string,
  overrides: Partial<{
    messages: unknown[];
    jobs: unknown[];
    decisions: unknown[];
    truncated: boolean;
  }> = {},
) {
  return {
    session: {
      id,
      title: "Investigate deployment",
      userId: "alice",
      createdAt: "2026-04-26T07:00:00Z",
      updatedAt: "2026-04-26T07:05:00Z",
      messages: overrides.messages ?? [],
      metadata: { source: "copilot" },
    },
    jobs: overrides.jobs ?? [],
    decisions: overrides.decisions ?? [],
    truncated: overrides.truncated ?? false,
  };
}

function makeJob(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: "job-default",
    type: "job.default",
    topic: "job.default",
    status: "succeeded",
    pool: "job.default",
    capabilities: [],
    riskTags: [],
    metadata: {},
    createdAt: "2026-04-26T07:00:10Z",
    updatedAt: "2026-04-26T07:02:00Z",
    ...overrides,
  };
}
