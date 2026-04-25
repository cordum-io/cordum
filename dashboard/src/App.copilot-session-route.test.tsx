import { describe, expect, it, beforeEach } from "vitest";
import { Routes, Route } from "react-router-dom";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test-utils/render";
import { http, HttpResponse, server } from "@/test-utils/msw";
import CopilotSessionPage from "./pages/CopilotSessionPage";
import NotFoundPage from "./pages/NotFoundPage";
import appSource from "./App.tsx?raw";

const SESSION_ID = "sess-abc123";

function renderApp(initial: string) {
  return renderWithProviders(
    <Routes>
      <Route path="/copilot/sessions/:sessionId" element={<CopilotSessionPage />} />
      <Route path="*" element={<NotFoundPage />} />
    </Routes>,
    { initialEntries: [initial] },
  );
}

describe("App routing — /copilot/sessions/:sessionId resolves to CopilotSessionPage, not NotFoundPage (task-7e200bec)", () => {
  beforeEach(() => {
    server.resetHandlers();
    server.use(
      http.get("*/api/v1/jobs", () => HttpResponse.json({ items: [] })),
    );
  });

  // Source-level guard: catches drift where the in-isolation render below
  // still passes but the route is not actually registered in production
  // App.tsx (the original review concern on this test file).
  it("registers /copilot/sessions/:sessionId route in App.tsx (registration guard)", () => {
    expect(appSource).toMatch(/path="\/copilot\/sessions\/:sessionId"/);
    expect(appSource).toMatch(/element=\{<CopilotSessionPage\b/);
  });

  it("/copilot/sessions/<id> mounts CopilotSessionPage (NOT NotFoundPage)", async () => {
    renderApp(`/copilot/sessions/${SESSION_ID}`);

    // POSITIVE: roadmap-banner testid is the stable marker that CopilotSessionPage mounted
    await waitFor(() => {
      expect(screen.queryByTestId("copilot-session-roadmap-banner")).not.toBeNull();
    });

    // NEGATIVE: NotFoundPage's distinctive copy must NOT be present
    expect(screen.queryByText(/page not found/i)).toBeNull();

    // Header shows full sessionId verbatim (truncation would mean wrong path)
    expect(screen.queryByText(SESSION_ID)).not.toBeNull();
  });

  it("/copilot/sessions/<unknown> still mounts CopilotSessionPage (no NotFound on bookmark)", async () => {
    renderApp(`/copilot/sessions/sess-xyz999`);

    await waitFor(() => {
      expect(screen.queryByTestId("copilot-session-roadmap-banner")).not.toBeNull();
    });

    expect(screen.queryByText(/page not found/i)).toBeNull();
    expect(screen.queryByText("sess-xyz999")).not.toBeNull();
  });

  it("/some/garbage/path still resolves to NotFoundPage (route precedence baseline)", async () => {
    renderApp(`/some/garbage/path`);

    await waitFor(() => {
      expect(screen.queryByText(/page not found/i)).not.toBeNull();
    });

    expect(screen.queryByTestId("copilot-session-roadmap-banner")).toBeNull();
  });
});
