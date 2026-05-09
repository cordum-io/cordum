import { describe, it, expect, beforeEach } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "../test-utils/msw";
import { renderWithProviders } from "../test-utils/render";
import { useUiStore } from "../state/ui";
import { CommandPalette } from "./CommandPalette";

describe("CommandPalette recent jobs/agents", () => {
  beforeEach(() => {
    useUiStore.setState({ commandOpen: false });
  });

  it("renders recent jobs section when /jobs returns items", async () => {
    server.use(
      http.get("*/api/v1/jobs", () =>
        HttpResponse.json({
          items: [
            {
              id: "job-deploy-123",
              topic: "job.deploy",
              status: "succeeded",
              tenant_id: "default",
            },
            {
              id: "job-fraud-456",
              topic: "job.fraud-detection.process",
              status: "running",
              tenant_id: "default",
            },
          ],
          next_cursor: null,
        }),
      ),
      http.get("*/api/v1/workers", () => HttpResponse.json({ items: [] })),
    );

    const { findByText } = renderWithProviders(<CommandPalette />);

    // Open the palette via the global keydown listener
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "k", metaKey: true }));

    // The "Recent Jobs" section header renders only when jobs query returns items
    expect(await findByText("Recent Jobs")).toBeTruthy();
    // First job's label includes its topic (the label format is "${topic} · ${id-prefix-8}")
    expect(await findByText(/job\.deploy/)).toBeTruthy();
  });

  it("renders recent agents section when /workers returns items", async () => {
    server.use(
      http.get("*/api/v1/jobs", () =>
        HttpResponse.json({ items: [], next_cursor: null }),
      ),
      http.get("*/api/v1/workers", () =>
        HttpResponse.json({
          items: [
            {
              worker_id: "worker-1",
              labels: { name: "deploy-worker-prod" },
              pool: "default",
              status: "idle",
              active_jobs: 0,
              max_parallel_jobs: 4,
            },
          ],
        }),
      ),
    );

    const { findByText } = renderWithProviders(<CommandPalette />);

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "k", metaKey: true }));

    expect(await findByText("Recent Agents")).toBeTruthy();
    expect(await findByText("deploy-worker-prod")).toBeTruthy();
  });

  it("does not render Recent sections when both queries return empty", async () => {
    server.use(
      http.get("*/api/v1/jobs", () =>
        HttpResponse.json({ items: [], next_cursor: null }),
      ),
      http.get("*/api/v1/workers", () => HttpResponse.json({ items: [] })),
    );

    const { findByText, queryByText } = renderWithProviders(<CommandPalette />);

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "k", metaKey: true }));

    // Static section should still render
    expect(await findByText("Navigate")).toBeTruthy();
    // Recent sections must not appear
    expect(queryByText("Recent Jobs")).toBeNull();
    expect(queryByText("Recent Agents")).toBeNull();
  });
});
