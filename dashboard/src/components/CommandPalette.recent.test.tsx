import { describe, it, expect, beforeEach } from "vitest";
import { fireEvent } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { server } from "../test-utils/msw";
import { renderWithProviders } from "../test-utils/render";
import { useUiStore } from "../state/ui";
import { useKeyboardShortcuts } from "../hooks/useKeyboardShortcuts";
import { CommandPalette } from "./CommandPalette";
import { KeyboardShortcutsHelp } from "./KeyboardShortcutsHelp";

function KeyboardShortcutsHarness() {
  useKeyboardShortcuts();

  return (
    <>
      <label htmlFor="shortcut-input">Shortcut input</label>
      <input id="shortcut-input" />
      <KeyboardShortcutsHelp />
    </>
  );
}

describe("CommandPalette recent jobs/agents", () => {
  beforeEach(() => {
    useUiStore.setState({ commandOpen: false, shortcutsHelpOpen: false });
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

  it("filters recent jobs by user search query (fuzzy keyword match)", async () => {
    server.use(
      http.get("*/api/v1/jobs", () =>
        HttpResponse.json({
          items: [
            {
              id: "job-deploy-A1B2C3D4",
              topic: "job.deploy.production",
              status: "succeeded",
              tenant_id: "default",
            },
            {
              id: "job-fraud-E5F6G7H8",
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

    const { findByText, findByPlaceholderText, queryByText } = renderWithProviders(
      <CommandPalette />,
    );

    // Open palette
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "k", metaKey: true }));

    // Both jobs render before filtering
    expect(await findByText(/job\.deploy\.production/)).toBeTruthy();
    expect(await findByText(/job\.fraud-detection\.process/)).toBeTruthy();

    // Type a search query that only matches the deploy job
    const input = await findByPlaceholderText("Type a command or search...");
    fireEvent.change(input, { target: { value: "deploy" } });

    // The matching deploy job remains visible
    expect(await findByText(/job\.deploy\.production/)).toBeTruthy();
    // The non-matching fraud job is filtered out
    expect(queryByText(/job\.fraud-detection\.process/)).toBeNull();
  });
});

describe("KeyboardShortcutsHelp dialog opens on `?`", () => {
  beforeEach(() => {
    useUiStore.setState({ shortcutsHelpOpen: false });
  });

  it("opens from a real '?' keydown and closes from Escape", () => {
    const { queryByText, getByText } = renderWithProviders(
      <KeyboardShortcutsHarness />,
    );

    expect(queryByText("Keyboard Shortcuts")).toBeNull();

    fireEvent.keyDown(document, { key: "?" });
    expect(getByText("Keyboard Shortcuts")).toBeTruthy();

    fireEvent.keyDown(document, { key: "Escape" });
    expect(queryByText("Keyboard Shortcuts")).toBeNull();
  });

  it("does not open when '?' is typed inside an input", () => {
    const { getByLabelText, queryByText } = renderWithProviders(
      <KeyboardShortcutsHarness />,
    );
    const input = getByLabelText("Shortcut input");

    input.focus();
    fireEvent.keyDown(input, { key: "?" });

    expect(queryByText("Keyboard Shortcuts")).toBeNull();
  });
});
