import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { broadcastSyncMock, loggerMock } = vi.hoisted(() => ({
  broadcastSyncMock: vi.fn(),
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("../hooks/useCrossTabSync", () => ({
  broadcastSync: broadcastSyncMock,
}));

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

async function loadConfigModule() {
  vi.resetModules();
  return await import("./config");
}

const TOKEN_KEY = "cordum-api-key";
const USER_KEY = "cordum-user";
const LOGIN_TS_KEY = "cordum-login-ts";

describe("useConfigStore", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("loads initial state from localStorage user (token not persisted) and applies defaults", async () => {
    // Legacy token in localStorage should be cleared on init
    window.localStorage.setItem(TOKEN_KEY, "token-in-storage");
    window.localStorage.setItem(
      USER_KEY,
      JSON.stringify({
        id: "user-1",
        username: "alice",
        email: "alice@example.com",
        display_name: "Alice",
        roles: ["admin"],
        tenant: "tenant-a",
      }),
    );
    window.localStorage.setItem(LOGIN_TS_KEY, "1700000000000");

    const { useConfigStore } = await loadConfigModule();
    const state = useConfigStore.getState();

    expect(state.apiBaseUrl).toBe("");
    expect(state.apiKey).toBe(""); // token NOT loaded from localStorage
    expect(state.tenantId).toBe("tenant-a");
    expect(state.principalId).toBe("user-1");
    expect(state.principalRole).toBe("admin");
    expect(state.traceUrlTemplate).toBe("");
    expect(state.approvalSlaMs).toBe(900_000);
    expect(state.isAuthenticated).toBe(true); // based on user, not token
    // Persisted user implies a prior session-mode login; cookie is the auth
    // artefact and is preserved across tab reloads by the browser.
    expect(state.authMode).toBe("session");
    expect(state.isLoggingOut).toBe(false);
    expect(state.loginTimestamp).toBe(1700000000000);
    // Legacy token should have been cleared
    expect(window.localStorage.getItem(TOKEN_KEY)).toBeNull();
  });

  it("update merges patch into memory state (apiKey not persisted to localStorage)", async () => {
    const { useConfigStore } = await loadConfigModule();

    useConfigStore.getState().update({
      apiBaseUrl: "https://api.example.test",
      apiKey: "new-token",
      tenantId: "tenant-x",
    });
    let state = useConfigStore.getState();
    expect(state.apiBaseUrl).toBe("https://api.example.test");
    expect(state.apiKey).toBe("new-token");
    expect(state.tenantId).toBe("tenant-x");
    expect(state.isAuthenticated).toBe(true);
    // Patching apiKey via update() is the embedded-mode escape hatch (runtime
    // config). Authmode must flip to "apikey" so apiClient sends X-API-Key.
    expect(state.authMode).toBe("apikey");
    // Token should NOT be in localStorage (httpOnly cookie handles persistence)
    expect(window.localStorage.getItem(TOKEN_KEY)).toBeNull();

    useConfigStore.getState().update({ apiKey: "" });
    state = useConfigStore.getState();
    expect(state.apiKey).toBe("");
    expect(state.isAuthenticated).toBe(false);
    expect(state.authMode).toBe("anonymous");
  });

  it("login sets auth fields, persists state, and broadcasts auth-login", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-13T06:00:00.000Z"));

    const { useConfigStore } = await loadConfigModule();
    useConfigStore.getState().login(
      { mode: "apikey", key: "login-token" },
      {
        id: "user-2",
        username: "bob",
        email: "bob@example.com",
        display_name: "Bob",
        roles: ["operator", "viewer"],
        tenant: "tenant-b",
      },
    );

    const state = useConfigStore.getState();
    expect(state.apiKey).toBe("login-token");
    expect(state.authMode).toBe("apikey");
    expect(state.user?.id).toBe("user-2");
    expect(state.isAuthenticated).toBe(true);
    expect(state.isLoggingOut).toBe(false);
    expect(state.tenantId).toBe("tenant-b");
    expect(state.principalId).toBe("user-2");
    expect(state.principalRole).toBe("operator");
    expect(state.loginTimestamp).toBe(new Date("2026-02-13T06:00:00.000Z").getTime());

    // Token NOT persisted to localStorage (httpOnly cookie handles auth)
    expect(window.localStorage.getItem(TOKEN_KEY)).toBeNull();
    // User metadata IS persisted (no secrets, used for initial state on refresh)
    expect(window.localStorage.getItem(USER_KEY)).toContain("\"id\":\"user-2\"");
    expect(window.localStorage.getItem(LOGIN_TS_KEY)).toBe(
      String(new Date("2026-02-13T06:00:00.000Z").getTime()),
    );
    expect(broadcastSyncMock).toHaveBeenCalledWith({
      type: "auth-login",
      creds: { mode: "apikey", key: "login-token" },
      user: expect.objectContaining({ id: "user-2", tenant: "tenant-b" }),
    });
  });

  it("session-mode login does NOT store any value as apiKey (prevents X-API-Key login-loop)", async () => {
    // Customer-reported bug regression: password and SSO login flows are
    // session-based. The gateway sets the httpOnly `cordum_session` cookie;
    // the dashboard must NOT store the session token (echoed in the response
    // body for non-browser clients) as `apiKey`. If it did, apiClient would
    // send it as `X-API-Key: session-xxx` on every subsequent request, the
    // gateway would reject that header (it's for real API keys only), and
    // the dashboard would interpret 401 → logout → /login → infinite loop.
    const { useConfigStore } = await loadConfigModule();
    useConfigStore.getState().login(
      { mode: "session" },
      {
        id: "admin",
        username: "admin",
        email: "",
        display_name: "admin",
        roles: ["admin"],
        tenant: "default",
      },
    );

    const state = useConfigStore.getState();
    expect(state.apiKey).toBe(""); // CRITICAL: no token in apiKey slot
    expect(state.authMode).toBe("session");
    expect(state.isAuthenticated).toBe(true);
    expect(state.user?.id).toBe("admin");
    expect(state.tenantId).toBe("default");
    // Broadcast carries the discriminator only — no token to leak across tabs
    // (session cookie syncs automatically; nothing for apikey-bound header).
    expect(broadcastSyncMock).toHaveBeenCalledWith({
      type: "auth-login",
      creds: { mode: "session" },
      user: expect.objectContaining({ id: "admin" }),
    });
  });

  it("logout clears auth fields, clears localStorage, and broadcasts auth-logout", async () => {
    const { useConfigStore } = await loadConfigModule();
    useConfigStore.setState({
      apiBaseUrl: "",
      apiKey: "token-to-clear",
      tenantId: "tenant-z",
      principalId: "user-z",
      principalRole: "admin",
      traceUrlTemplate: "",
      approvalSlaMs: 900_000,
      user: {
        id: "user-z",
        username: "zed",
        email: "zed@example.com",
        display_name: "Zed",
        roles: ["admin"],
        tenant: "tenant-z",
      },
      isAuthenticated: true,
      loginTimestamp: 1700000000000,
    });
    window.localStorage.setItem(TOKEN_KEY, "token-to-clear");
    window.localStorage.setItem(USER_KEY, "{\"id\":\"user-z\"}");
    window.localStorage.setItem(LOGIN_TS_KEY, "1700000000000");

    useConfigStore.getState().logout();
    const state = useConfigStore.getState();

    expect(state.apiKey).toBe("");
    expect(state.authMode).toBe("anonymous");
    expect(state.user).toBeNull();
    expect(state.isAuthenticated).toBe(false);
    expect(state.isLoggingOut).toBe(true);
    expect(state.loginTimestamp).toBeNull();
    expect(state.tenantId).toBe("");
    expect(state.principalId).toBe("");
    expect(state.principalRole).toBe("");

    expect(window.localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(window.localStorage.getItem(USER_KEY)).toBeNull();
    expect(window.localStorage.getItem(LOGIN_TS_KEY)).toBeNull();
    expect(broadcastSyncMock).toHaveBeenCalledWith({ type: "auth-logout" });
  });

  it("ignores duplicate logout calls until the next login", async () => {
    const { useConfigStore } = await loadConfigModule();
    useConfigStore.getState().login(
      { mode: "apikey", key: "token-1" },
      {
        id: "user-1",
        username: "alice",
        email: "alice@example.com",
        display_name: "Alice",
        roles: ["admin"],
        tenant: "tenant-a",
      },
    );

    useConfigStore.getState().logout();
    useConfigStore.getState().logout();

    expect(useConfigStore.getState().isLoggingOut).toBe(true);
    expect(broadcastSyncMock).toHaveBeenCalledTimes(2);
    expect(broadcastSyncMock).toHaveBeenNthCalledWith(1, {
      type: "auth-login",
      creds: { mode: "apikey", key: "token-1" },
      user: expect.objectContaining({ id: "user-1", tenant: "tenant-a" }),
    });
    expect(broadcastSyncMock).toHaveBeenNthCalledWith(2, { type: "auth-logout" });

    useConfigStore.getState().login(
      { mode: "apikey", key: "token-2" },
      {
        id: "user-2",
        username: "bob",
        email: "bob@example.com",
        display_name: "Bob",
        roles: ["operator"],
        tenant: "tenant-b",
      },
    );

    expect(useConfigStore.getState().isLoggingOut).toBe(false);
  });

  it("refreshLoginTimestamp updates and persists timestamp", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-13T06:30:00.000Z"));

    const { useConfigStore } = await loadConfigModule();
    useConfigStore.getState().refreshLoginTimestamp();

    const ts = new Date("2026-02-13T06:30:00.000Z").getTime();
    expect(useConfigStore.getState().loginTimestamp).toBe(ts);
    expect(window.localStorage.getItem(LOGIN_TS_KEY)).toBe(String(ts));
  });
});

describe("tenant lock (defense-in-depth)", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
  });

  it("locks tenantId after login and blocks subsequent changes via update()", async () => {
    const { useConfigStore } = await loadConfigModule();
    useConfigStore.getState().login(
      { mode: "apikey", key: "token-1" },
      {
        id: "user-1",
        username: "alice",
        email: "a@example.com",
        display_name: "Alice",
        roles: ["admin"],
        tenant: "tenant-a",
      },
    );

    expect(useConfigStore.getState().tenantLocked).toBe(true);
    expect(useConfigStore.getState().tenantId).toBe("tenant-a");

    // Attempt to change tenant — should be blocked
    useConfigStore.getState().update({ tenantId: "tenant-evil" });
    expect(useConfigStore.getState().tenantId).toBe("tenant-a");
    expect(loggerMock.warn).toHaveBeenCalledWith(
      "config-store",
      "Blocked tenantId change while locked",
      expect.objectContaining({ current: "tenant-a", attempted: "tenant-evil" }),
    );
  });

  it("allows tenantId to be set initially via update() when not locked", async () => {
    const { useConfigStore } = await loadConfigModule();
    expect(useConfigStore.getState().tenantLocked).toBe(false);

    useConfigStore.getState().update({ tenantId: "tenant-first" });
    expect(useConfigStore.getState().tenantId).toBe("tenant-first");
    expect(useConfigStore.getState().tenantLocked).toBe(true);
  });

  it("resets tenantLocked on logout so a new login can set tenant", async () => {
    const { useConfigStore } = await loadConfigModule();
    useConfigStore.getState().login(
      { mode: "apikey", key: "token-1" },
      {
        id: "user-1",
        username: "alice",
        email: "a@example.com",
        display_name: "Alice",
        roles: ["admin"],
        tenant: "tenant-a",
      },
    );
    expect(useConfigStore.getState().tenantLocked).toBe(true);

    useConfigStore.getState().logout();
    expect(useConfigStore.getState().tenantLocked).toBe(false);
    expect(useConfigStore.getState().tenantId).toBe("");

    // Can now set a different tenant
    useConfigStore.getState().login(
      { mode: "apikey", key: "token-2" },
      {
        id: "user-2",
        username: "bob",
        email: "b@example.com",
        display_name: "Bob",
        roles: ["operator"],
        tenant: "tenant-b",
      },
    );
    expect(useConfigStore.getState().tenantId).toBe("tenant-b");
    expect(useConfigStore.getState().tenantLocked).toBe(true);
  });

  it("still applies non-tenant fields from a patch that includes a blocked tenant change", async () => {
    const { useConfigStore } = await loadConfigModule();
    useConfigStore.getState().login(
      { mode: "apikey", key: "token-1" },
      {
        id: "user-1",
        username: "alice",
        email: "a@example.com",
        display_name: "Alice",
        roles: ["admin"],
        tenant: "tenant-a",
      },
    );

    useConfigStore.getState().update({ tenantId: "tenant-evil", apiBaseUrl: "https://new-api.test" });
    expect(useConfigStore.getState().tenantId).toBe("tenant-a");
    expect(useConfigStore.getState().apiBaseUrl).toBe("https://new-api.test");
  });
});

describe("SLA helpers", () => {
  it("isSlaBreach returns true only when wait exceeds SLA", async () => {
    const { isSlaBreach } = await loadConfigModule();
    expect(isSlaBreach(900_001, 900_000)).toBe(true);
    expect(isSlaBreach(900_000, 900_000)).toBe(false);
    expect(isSlaBreach(100_000, 900_000)).toBe(false);
  });

  it("slaRemainingMs returns SLA minus wait", async () => {
    const { slaRemainingMs } = await loadConfigModule();
    expect(slaRemainingMs(100_000, 900_000)).toBe(800_000);
    expect(slaRemainingMs(900_000, 900_000)).toBe(0);
    expect(slaRemainingMs(950_000, 900_000)).toBe(-50_000);
  });
});
