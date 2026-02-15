import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithQueryClient } from "./__tests__/test-utils";

class MockBroadcastChannel {
  static instances: MockBroadcastChannel[] = [];

  readonly name: string;
  readonly postMessage = vi.fn();
  private listeners = new Set<(event: MessageEvent) => void>();

  constructor(name: string) {
    this.name = name;
    MockBroadcastChannel.instances.push(this);
  }

  addEventListener(type: string, listener: EventListener) {
    if (type === "message") {
      this.listeners.add(listener as (event: MessageEvent) => void);
    }
  }

  removeEventListener(type: string, listener: EventListener) {
    if (type === "message") {
      this.listeners.delete(listener as (event: MessageEvent) => void);
    }
  }

  emit(data: unknown) {
    const event = { data } as MessageEvent;
    for (const listener of this.listeners) {
      listener(event);
    }
  }

  close() {
    this.listeners.clear();
  }
}

vi.stubGlobal("BroadcastChannel", MockBroadcastChannel);

const { navigateMock, loginMock, logoutMock, setThemeMock, configState, uiState } = vi.hoisted(() => ({
  navigateMock: vi.fn(),
  loginMock: vi.fn(),
  logoutMock: vi.fn(),
  setThemeMock: vi.fn(),
  configState: {
    login: vi.fn(),
    logout: vi.fn(),
  },
  uiState: {
    theme: "light" as "light" | "dark" | "system",
    setTheme: vi.fn(),
  },
}));

vi.mock("react-router-dom", () => ({
  useNavigate: () => navigateMock,
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => configState,
  },
}));

vi.mock("../state/ui", () => ({
  useUiStore: {
    getState: () => uiState,
  },
}));

const { broadcastSync, useCrossTabSync } = await import("./useCrossTabSync");

function channel(): MockBroadcastChannel {
  const c = MockBroadcastChannel.instances[0];
  if (!c) throw new Error("Expected BroadcastChannel instance");
  return c;
}

/** Create a StorageEvent with the given key and newValue. */
function makeStorageEvent(props: { key: string; newValue: string | null }): StorageEvent {
  // StorageEvent constructor accepts StorageEventInit per DOM spec.
  // lgtm[js/superfluous-trailing-arguments]
  return new StorageEvent("storage", { key: props.key, newValue: props.newValue });
}

describe("useCrossTabSync", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.clear();
    configState.login = loginMock;
    configState.logout = logoutMock;
    uiState.theme = "light";
    uiState.setTheme = setThemeMock.mockImplementation((theme: "light" | "dark" | "system") => {
      uiState.theme = theme;
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("broadcastSync posts messages when not syncing", () => {
    broadcastSync({ type: "auth-logout" });
    expect(channel().postMessage).toHaveBeenCalledWith({ type: "auth-logout" });
  });

  it("broadcastSync is suppressed while handling incoming sync messages", async () => {
    configState.logout = vi.fn(() => {
      broadcastSync({ type: "auth-logout" });
    });

    const hook = renderWithQueryClient(() => useCrossTabSync());

    channel().emit({ type: "auth-logout" });

    await hook.waitFor(() => {
      expect(configState.logout).toHaveBeenCalledTimes(1);
    });
    expect(channel().postMessage).not.toHaveBeenCalled();
    expect(navigateMock).toHaveBeenCalledWith("/login", { replace: true });

    hook.unmount();
  });

  it("handles auth-login message using payload directly, not localStorage", async () => {
    // localStorage has stale/different data — should NOT be used for BroadcastChannel
    window.localStorage.setItem("cordum-api-key", "stale-key");
    window.localStorage.setItem(
      "cordum-user",
      JSON.stringify({ id: "stale-user", username: "stale", email: "", display_name: "", roles: [], tenant: "stale" }),
    );

    const hook = renderWithQueryClient(() => useCrossTabSync());

    channel().emit({
      type: "auth-login",
      token: "fresh-token",
      user: {
        id: "u1",
        username: "alice",
        email: "alice@example.com",
        display_name: "Alice",
        roles: ["admin"],
        tenant: "tenant-1",
      },
    });

    await hook.waitFor(() => {
      expect(loginMock).toHaveBeenCalledWith(
        "fresh-token",
        expect.objectContaining({ id: "u1", tenant: "tenant-1" }),
      );
    });
    // Verify it used message payload, not localStorage
    expect(loginMock).not.toHaveBeenCalledWith(
      "stale-key",
      expect.anything(),
    );

    hook.unmount();
  });

  it("storage-event login fallback reads token and user from localStorage", async () => {
    window.localStorage.setItem(
      "cordum-user",
      JSON.stringify({
        id: "u2",
        username: "bob",
        email: "bob@example.com",
        display_name: "Bob",
        roles: ["viewer"],
        tenant: "tenant-2",
      }),
    );

    const hook = renderWithQueryClient(() => useCrossTabSync());

    window.dispatchEvent(makeStorageEvent({ key: "cordum-api-key", newValue: "storage-token" }));

    await hook.waitFor(() => {
      expect(loginMock).toHaveBeenCalledWith(
        "storage-token",
        expect.objectContaining({ id: "u2", tenant: "tenant-2" }),
      );
    });

    hook.unmount();
  });

  it("storage-event login fallback skips login when user data is corrupt", async () => {
    window.localStorage.setItem("cordum-user", "not-json{{{");

    const hook = renderWithQueryClient(() => useCrossTabSync());

    window.dispatchEvent(makeStorageEvent({ key: "cordum-api-key", newValue: "some-token" }));

    // Give it a tick to process
    await new Promise((r) => setTimeout(r, 50));
    expect(loginMock).not.toHaveBeenCalled();

    hook.unmount();
  });

  it("handles theme-change and only updates when the theme differs", async () => {
    const hook = renderWithQueryClient(() => useCrossTabSync());

    channel().emit({ type: "theme-change", theme: "dark" });
    await hook.waitFor(() => {
      expect(setThemeMock).toHaveBeenCalledWith("dark");
    });

    setThemeMock.mockClear();
    channel().emit({ type: "theme-change", theme: "dark" });
    expect(setThemeMock).not.toHaveBeenCalled();

    hook.unmount();
  });

  it("handles storage-event fallback for logout and theme changes", async () => {
    const hook = renderWithQueryClient(() => useCrossTabSync());

    window.dispatchEvent(makeStorageEvent({ key: "cordum-api-key", newValue: null }));
    await hook.waitFor(() => {
      expect(logoutMock).toHaveBeenCalledTimes(1);
    });
    expect(navigateMock).toHaveBeenCalledWith("/login", { replace: true });

    window.dispatchEvent(makeStorageEvent({ key: "cordum-theme", newValue: "dark" }));
    await hook.waitFor(() => {
      expect(setThemeMock).toHaveBeenCalledWith("dark");
    });

    hook.unmount();
  });
});
