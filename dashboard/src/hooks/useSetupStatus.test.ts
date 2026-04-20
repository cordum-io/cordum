import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithQueryClient } from "./__tests__/test-utils";
import { useSetupStatus } from "./useSetupStatus";

const { mockState } = vi.hoisted(() => ({
  mockState: {
    apiKeys: { data: { items: [] as unknown[] }, isLoading: false },
    users: { data: { items: [] as unknown[] }, isLoading: false },
    config: { data: {} as Record<string, unknown>, isLoading: false },
    bundles: { data: { items: [] as unknown[] }, isLoading: false },
    status: { data: { workers: { count: 0 } }, isLoading: false },
    channels: { data: [] as unknown[] },
    authConfig: { data: { saml_enabled: false, oidc_enabled: false } },
  },
}));

vi.mock("./useSettings", () => ({
  useApiKeys: () => mockState.apiKeys,
  useUsers: () => mockState.users,
  useConfig: () => mockState.config,
  useNotificationChannels: () => mockState.channels,
  useAuthConfigAdmin: () => mockState.authConfig,
}));

vi.mock("./usePolicies", () => ({
  usePolicyBundles: () => mockState.bundles,
}));

vi.mock("./useStatus", () => ({
  useStatus: () => mockState.status,
}));

function resetMocks() {
  mockState.apiKeys = { data: { items: [] }, isLoading: false };
  mockState.users = { data: { items: [] }, isLoading: false };
  mockState.config = { data: {}, isLoading: false };
  mockState.bundles = { data: { items: [] }, isLoading: false };
  mockState.status = { data: { workers: { count: 0 } }, isLoading: false };
  mockState.channels = { data: [] };
  mockState.authConfig = { data: { saml_enabled: false, oidc_enabled: false } };
}

describe("useSetupStatus", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.clear();
    resetMocks();
  });

  it("fresh install returns isNewInstall=true with required items incomplete", () => {
    const hook = renderWithQueryClient(() => useSetupStatus());

    expect(hook.result.current?.isNewInstall).toBe(true);
    expect(hook.result.current?.totalRequired).toBe(5);
    expect(hook.result.current?.completedCount).toBe(0);

    hook.unmount();
  });

  it("partial setup computes completedCount 2/5", () => {
    mockState.apiKeys = { data: { items: [{ id: "k1" }] }, isLoading: false };
    mockState.users = { data: { items: [{ id: "u1" }] }, isLoading: false };

    const hook = renderWithQueryClient(() => useSetupStatus());

    expect(hook.result.current?.completedCount).toBe(2);
    expect(hook.result.current?.totalRequired).toBe(5);

    hook.unmount();
  });

  it("full setup marks all required items complete and isNewInstall=false", () => {
    mockState.apiKeys = { data: { items: [{ id: "k1" }] }, isLoading: false };
    mockState.users = { data: { items: [{ id: "u1" }, { id: "u2" }] }, isLoading: false };
    mockState.config = { data: { safetyStance: "balanced" }, isLoading: false };
    mockState.bundles = { data: { items: [{ id: "bundle-1" }] }, isLoading: false };
    mockState.status = { data: { workers: { count: 2 } }, isLoading: false };

    const hook = renderWithQueryClient(() => useSetupStatus());

    expect(hook.result.current?.completedCount).toBe(5);
    expect(hook.result.current?.totalRequired).toBe(5);
    expect(hook.result.current?.isNewInstall).toBe(false);

    hook.unmount();
  });

  it("tracks optional SSO and notifications items separately", () => {
    mockState.authConfig = { data: { saml_enabled: true, oidc_enabled: false } };
    mockState.channels = { data: [{ id: "n1" }] };

    const hook = renderWithQueryClient(() => useSetupStatus());

    const sso = hook.result.current?.items.find((i) => i.id === "sso");
    const notifications = hook.result.current?.items.find((i) => i.id === "notifications");

    expect(sso).toMatchObject({ optional: true, completed: true });
    expect(notifications).toMatchObject({ optional: true, completed: true });

    hook.unmount();
  });

  it("dismiss writes localStorage and dismissed reads from stored value", () => {
    const hook = renderWithQueryClient(() => useSetupStatus());

    expect(hook.result.current?.dismissed).toBe(false);
    hook.result.current?.dismiss();
    expect(window.localStorage.getItem("cordum-setup-dismissed")).toBe("true");

    hook.unmount();

    const remount = renderWithQueryClient(() => useSetupStatus());
    expect(remount.result.current?.dismissed).toBe(true);
    remount.unmount();
  });
});
