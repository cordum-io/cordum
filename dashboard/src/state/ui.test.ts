import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { broadcastSyncMock } = vi.hoisted(() => ({
  broadcastSyncMock: vi.fn(),
}));

vi.mock("../hooks/useCrossTabSync", () => ({
  broadcastSync: broadcastSyncMock,
}));

function setMatchMedia(isDark: boolean): void {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: query === "(prefers-color-scheme: dark)" ? isDark : false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

async function loadUiModule() {
  vi.resetModules();
  return await import("./ui");
}

describe("useUiStore", () => {
  beforeEach(() => {
    window.localStorage.clear();
    setMatchMedia(false);
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("defaults to system theme and table view when nothing is stored", async () => {
    const { useUiStore } = await loadUiModule();
    const state = useUiStore.getState();

    expect(state.theme).toBe("system");
    expect(state.resolvedTheme).toBe("light");
    expect(state.commandOpen).toBe(false);
    expect(state.agentsView).toBe("table");
    expect(state.shortcutsHelpOpen).toBe(false);
  });

  it("loads stored theme and agents view from localStorage", async () => {
    window.localStorage.setItem("cordum-theme", "dark");
    window.localStorage.setItem("cordum-agents-view", "cards");

    const { useUiStore } = await loadUiModule();
    const state = useUiStore.getState();

    expect(state.theme).toBe("dark");
    expect(state.resolvedTheme).toBe("dark");
    expect(state.agentsView).toBe("cards");
  });

  it("ignores invalid stored theme values", async () => {
    window.localStorage.setItem("cordum-theme", "solarized");

    const { useUiStore } = await loadUiModule();
    const state = useUiStore.getState();

    expect(state.theme).toBe("system");
    expect(state.resolvedTheme).toBe("light");
  });

  it("does not crash when localStorage is unavailable", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("storage unavailable");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("storage unavailable");
    });

    const { useUiStore } = await loadUiModule();

    expect(useUiStore.getState().theme).toBe("system");
    expect(() => useUiStore.getState().setTheme("dark")).not.toThrow();
    expect(() => useUiStore.getState().setAgentsView("cards")).not.toThrow();
    expect(useUiStore.getState().theme).toBe("dark");
    expect(useUiStore.getState().agentsView).toBe("cards");
  });

  it("toggleTheme cycles light -> dark -> system -> light and broadcasts", async () => {
    setMatchMedia(true);
    const { useUiStore } = await loadUiModule();
    const store = useUiStore.getState();

    store.setTheme("light");
    expect(useUiStore.getState().theme).toBe("light");
    expect(useUiStore.getState().resolvedTheme).toBe("light");

    useUiStore.getState().toggleTheme();
    expect(useUiStore.getState().theme).toBe("dark");
    expect(useUiStore.getState().resolvedTheme).toBe("dark");

    useUiStore.getState().toggleTheme();
    expect(useUiStore.getState().theme).toBe("system");
    expect(useUiStore.getState().resolvedTheme).toBe("dark");

    useUiStore.getState().toggleTheme();
    expect(useUiStore.getState().theme).toBe("light");
    expect(useUiStore.getState().resolvedTheme).toBe("light");

    expect(broadcastSyncMock).toHaveBeenNthCalledWith(1, {
      type: "theme-change",
      theme: "dark",
    });
    expect(broadcastSyncMock).toHaveBeenNthCalledWith(2, {
      type: "theme-change",
      theme: "system",
    });
    expect(broadcastSyncMock).toHaveBeenNthCalledWith(3, {
      type: "theme-change",
      theme: "light",
    });
  });

  it("setTheme applies explicit themes and resolves system via matchMedia", async () => {
    setMatchMedia(true);
    const { useUiStore } = await loadUiModule();

    useUiStore.getState().setTheme("system");
    expect(useUiStore.getState().theme).toBe("system");
    expect(useUiStore.getState().resolvedTheme).toBe("dark");

    useUiStore.getState().setTheme("light");
    expect(useUiStore.getState().theme).toBe("light");
    expect(useUiStore.getState().resolvedTheme).toBe("light");
  });

  it("syncSystemTheme only updates resolvedTheme when theme is system", async () => {
    setMatchMedia(true);
    const { useUiStore } = await loadUiModule();

    useUiStore.getState().setTheme("dark");
    setMatchMedia(false);
    useUiStore.getState().syncSystemTheme();
    expect(useUiStore.getState().theme).toBe("dark");
    expect(useUiStore.getState().resolvedTheme).toBe("dark");

    useUiStore.getState().setTheme("system");
    expect(useUiStore.getState().resolvedTheme).toBe("light");
    setMatchMedia(true);
    useUiStore.getState().syncSystemTheme();
    expect(useUiStore.getState().theme).toBe("system");
    expect(useUiStore.getState().resolvedTheme).toBe("dark");
  });

  it("updates simple UI setters and persists agents view", async () => {
    const { useUiStore } = await loadUiModule();

    useUiStore.getState().setGlobalSearch("gpu worker");
    useUiStore.getState().setCommandOpen(true);
    useUiStore.getState().setShortcutsHelpOpen(true);
    useUiStore.getState().setAgentsView("cards");

    const state = useUiStore.getState();
    expect(state.globalSearch).toBe("gpu worker");
    expect(state.commandOpen).toBe(true);
    expect(state.shortcutsHelpOpen).toBe(true);
    expect(state.agentsView).toBe("cards");
    expect(window.localStorage.getItem("cordum-agents-view")).toBe("cards");
  });
});
