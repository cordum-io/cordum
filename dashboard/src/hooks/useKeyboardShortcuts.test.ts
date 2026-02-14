import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithQueryClient } from "./__tests__/test-utils";
import {
  __keyboardShortcutsInternal,
  SHORTCUTS,
  useKeyboardShortcuts,
} from "./useKeyboardShortcuts";

const { navigateMock, uiState } = vi.hoisted(() => ({
  navigateMock: vi.fn(),
  uiState: {
    shortcutsHelpOpen: false,
    setShortcutsHelpOpen: vi.fn(),
  },
}));

vi.mock("react-router-dom", () => ({
  useNavigate: () => navigateMock,
}));

vi.mock("../state/ui", () => ({
  useUiStore: {
    getState: () => uiState,
  },
}));

function keydown(key: string, init: KeyboardEventInit = {}) {
  document.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, ...init }));
}

describe("useKeyboardShortcuts", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    uiState.shortcutsHelpOpen = false;
    uiState.setShortcutsHelpOpen = vi.fn((open: boolean) => {
      uiState.shortcutsHelpOpen = open;
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("isEditableTarget detects form/editable elements", () => {
    const input = document.createElement("input");
    const textarea = document.createElement("textarea");
    const select = document.createElement("select");
    const editable = document.createElement("div");
    editable.setAttribute("contenteditable", "true");
    Object.defineProperty(editable, "isContentEditable", { value: true });
    const div = document.createElement("div");

    expect(__keyboardShortcutsInternal.isEditableTarget(input)).toBe(true);
    expect(__keyboardShortcutsInternal.isEditableTarget(textarea)).toBe(true);
    expect(__keyboardShortcutsInternal.isEditableTarget(select)).toBe(true);
    expect(__keyboardShortcutsInternal.isEditableTarget(editable)).toBe(true);
    expect(__keyboardShortcutsInternal.isEditableTarget(div)).toBe(false);
  });

  it("SHORTCUTS and NAV_MAP expose expected navigation bindings", () => {
    expect(SHORTCUTS).toHaveLength(10);
    expect(SHORTCUTS[0]).toMatchObject({ keys: ["g", "o"], action: "/" });
    expect(SHORTCUTS.find((s) => s.label === "g j")?.action).toBe("/jobs");
    expect(__keyboardShortcutsInternal.NAV_MAP.get("o")).toBe("/");
    expect(__keyboardShortcutsInternal.NAV_MAP.get("j")).toBe("/jobs");
  });

  it("toggles shortcuts help on '?' and closes on Escape", async () => {
    const hook = renderWithQueryClient(() => useKeyboardShortcuts());

    keydown("?");
    await hook.waitFor(() => {
      expect(uiState.setShortcutsHelpOpen).toHaveBeenCalledWith(true);
    });

    uiState.setShortcutsHelpOpen.mockClear();
    uiState.shortcutsHelpOpen = true;
    keydown("Escape");
    await hook.waitFor(() => {
      expect(uiState.setShortcutsHelpOpen).toHaveBeenCalledWith(false);
    });

    hook.unmount();
  });

  it("navigates with 'g' + second key sequence", async () => {
    const hook = renderWithQueryClient(() => useKeyboardShortcuts());

    keydown("g");
    keydown("o");
    await hook.waitFor(() => {
      expect(navigateMock).toHaveBeenCalledWith("/");
    });

    navigateMock.mockClear();
    keydown("g");
    keydown("j");
    await hook.waitFor(() => {
      expect(navigateMock).toHaveBeenCalledWith("/jobs");
    });

    hook.unmount();
  });

  it("expires prefix window after timeout", () => {
    vi.useFakeTimers();
    const hook = renderWithQueryClient(() => useKeyboardShortcuts());

    keydown("g");
    vi.advanceTimersByTime(1001);
    keydown("o");

    expect(navigateMock).not.toHaveBeenCalled();

    hook.unmount();
    vi.useRealTimers();
  });

  it("ignores modifier-key shortcuts", () => {
    const hook = renderWithQueryClient(() => useKeyboardShortcuts());

    keydown("g", { ctrlKey: true });
    keydown("o");

    expect(navigateMock).not.toHaveBeenCalled();

    hook.unmount();
  });

  it("ignores keystrokes from editable targets", () => {
    const hook = renderWithQueryClient(() => useKeyboardShortcuts());
    const input = document.createElement("input");

    input.dispatchEvent(new KeyboardEvent("keydown", { key: "g", bubbles: true }));
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "o", bubbles: true }));

    expect(navigateMock).not.toHaveBeenCalled();
    hook.unmount();
  });
});
