import { describe, expect, it, beforeEach } from "vitest";
import { Routes, Route } from "react-router-dom";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test-utils/render";
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
      http.get("*/api/v1/jobs", () => HttpResponse.json({ items: [] })),
    );
    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    await waitFor(() => {
      expect(screen.queryByText(SESSION_ID)).not.toBeNull();
    });
  });

  it("renders the roadmap banner with the stable testid", async () => {
    server.use(
      http.get("*/api/v1/jobs", () => HttpResponse.json({ items: [] })),
    );
    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    await waitFor(() => {
      expect(
        screen.queryByTestId("copilot-session-roadmap-banner"),
      ).not.toBeNull();
    });
  });

  it("renders linked jobs returned by /api/v1/jobs?session_id=<sid>", async () => {
    server.use(
      http.get("*/api/v1/jobs", ({ request }) => {
        const url = new URL(request.url);
        if (url.searchParams.get("session_id") === SESSION_ID) {
          return HttpResponse.json({
            items: [
              { id: "job-1", topic: "topic.one", state: "succeeded", updated_at: 1 },
              { id: "job-2", topic: "topic.two", state: "running", updated_at: 2 },
            ],
          });
        }
        return HttpResponse.json({ items: [] });
      }),
    );
    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    await waitFor(() => {
      expect(screen.queryByText("topic.one")).not.toBeNull();
    });
    expect(screen.queryByText("topic.two")).not.toBeNull();
  });

  it("shows an empty-state message when no jobs are linked", async () => {
    server.use(
      http.get("*/api/v1/jobs", () => HttpResponse.json({ items: [] })),
    );
    renderRoute(`/copilot/sessions/${SESSION_ID}`);
    await waitFor(() => {
      expect(
        screen.queryByText(/no jobs linked to this session yet/i),
      ).not.toBeNull();
    });
  });

  it("Back to Jobs button navigates to /jobs", async () => {
    server.use(
      http.get("*/api/v1/jobs", () => HttpResponse.json({ items: [] })),
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
