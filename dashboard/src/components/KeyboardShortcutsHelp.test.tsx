import { fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { useUiStore } from "../state/ui";
import { http, HttpResponse, server } from "../test-utils/msw";
import { renderWithProviders } from "../test-utils/render";
import { AppShell } from "./layout/AppShell";

function AppShellKeyboardHarness() {
  return (
    <AppShell>
      <div>
        <label htmlFor="shortcut-input">Shortcut input</label>
        <input id="shortcut-input" />
        <label htmlFor="shortcut-textarea">Shortcut textarea</label>
        <textarea id="shortcut-textarea" />
      </div>
    </AppShell>
  );
}

describe("AppShell keyboard shortcuts help", () => {
  beforeEach(() => {
    useUiStore.setState({ commandOpen: false, shortcutsHelpOpen: false });
    // These AppShell queries had no handler, so MSW passed them to the real network, where they
    // failed after the test ended and logged during worker teardown (task-04eef2b3). Answering them
    // in-process keeps the work inside the test. telemetry/status gets a response, not an error:
    // its query retries after 1s, which would outlive the test; "anonymous" renders no banner.
    server.use(
      http.get("*/api/v1/jobs", () => HttpResponse.error()),
      http.get("*/api/v1/telemetry/status", () => HttpResponse.json({ mode: "anonymous" })),
      http.get("*/api/v1/dlq/page", () => HttpResponse.error()),
      http.get("*/api/v1/status", () => HttpResponse.error()),
    );
  });

  it("opens exactly one surviving shortcuts help dialog from a real '?' keydown", async () => {
    const { queryClient } = renderWithProviders(<AppShellKeyboardHarness />);

    expect(screen.queryByText("Keyboard Shortcuts")).toBeNull();

    fireEvent.keyDown(document.body, { key: "?" });

    await waitFor(() => {
      expect(screen.getAllByText("Keyboard Shortcuts")).toHaveLength(1);
    });
    expect(
      screen.getByRole("dialog", { name: "Keyboard Shortcuts" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Close keyboard shortcuts" }),
    ).toBeTruthy();
    expect(screen.getByText("Toggle shortcuts help")).toBeTruthy();
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));
  });

  it("does not open when '?' is typed inside an input", async () => {
    const { queryClient } = renderWithProviders(<AppShellKeyboardHarness />);

    const input = screen.getByLabelText("Shortcut input");
    input.focus();
    fireEvent.keyDown(input, { key: "?" });

    expect(screen.queryByText("Keyboard Shortcuts")).toBeNull();
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));
  });

  it("does not open when '?' is typed inside a textarea", async () => {
    const { queryClient } = renderWithProviders(<AppShellKeyboardHarness />);

    const textarea = screen.getByLabelText("Shortcut textarea");
    textarea.focus();
    fireEvent.keyDown(textarea, { key: "?" });

    expect(screen.queryByText("Keyboard Shortcuts")).toBeNull();
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));
  });

  it("closes the shortcuts help dialog from Escape", async () => {
    const { queryClient } = renderWithProviders(<AppShellKeyboardHarness />);

    fireEvent.keyDown(document.body, { key: "?" });
    await waitFor(() => {
      expect(screen.getAllByText("Keyboard Shortcuts")).toHaveLength(1);
    });

    fireEvent.keyDown(document.body, { key: "Escape" });

    await waitFor(() => {
      expect(
        screen.queryByRole("dialog", { name: "Keyboard Shortcuts" }),
      ).toBeNull();
    });
    expect(screen.queryByText("Keyboard Shortcuts")).toBeNull();
    await waitFor(() => expect(queryClient.isFetching()).toBe(0));
  });
});
