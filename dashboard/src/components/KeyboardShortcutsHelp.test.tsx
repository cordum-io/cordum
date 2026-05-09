import { fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { useUiStore } from "../state/ui";
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
  });

  it("opens exactly one surviving shortcuts help dialog from a real '?' keydown", async () => {
    renderWithProviders(<AppShellKeyboardHarness />);

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
  });

  it("does not open when '?' is typed inside an input", () => {
    renderWithProviders(<AppShellKeyboardHarness />);

    const input = screen.getByLabelText("Shortcut input");
    input.focus();
    fireEvent.keyDown(input, { key: "?" });

    expect(screen.queryByText("Keyboard Shortcuts")).toBeNull();
  });

  it("does not open when '?' is typed inside a textarea", () => {
    renderWithProviders(<AppShellKeyboardHarness />);

    const textarea = screen.getByLabelText("Shortcut textarea");
    textarea.focus();
    fireEvent.keyDown(textarea, { key: "?" });

    expect(screen.queryByText("Keyboard Shortcuts")).toBeNull();
  });

  it("closes the shortcuts help dialog from Escape", async () => {
    renderWithProviders(<AppShellKeyboardHarness />);

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
  });
});
