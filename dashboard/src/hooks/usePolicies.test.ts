import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";

const { addToastMock, loggerMock } = vi.hoisted(() => ({
  addToastMock: vi.fn(),
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

const { mockConfigState } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "",
    tenantId: "",
    principalId: "",
    principalRole: "",
    user: null,
    logout: vi.fn(),
  },
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../state/toast", () => ({
  useToastStore: {
    getState: () => ({ addToast: addToastMock }),
  },
}));

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

async function loadPoliciesModule(configSupported?: boolean) {
  vi.resetModules();
  if (configSupported === undefined) {
    vi.stubEnv("VITE_POLICY_CONFIG_SUPPORTED", "false");
  } else {
    vi.stubEnv("VITE_POLICY_CONFIG_SUPPORTED", configSupported ? "true" : "false");
  }
  vi.stubEnv("VITE_POLICY_STATS_SUPPORTED", "false");
  return await import("./usePolicies");
}

describe("usePolicies internals", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllEnvs();
  });

  it("encodes bundle IDs and builds policy paths", async () => {
    const mod = await loadPoliciesModule();
    expect(mod.encodePolicyBundleId("secops/default")).toBe("secops~default");
    expect(mod.__policiesInternal.policyBundlePath("secops/default")).toBe(
      "/policy/bundles/secops~default",
    );
    expect(
      mod.__policiesInternal.policyBundleRulePath("secops/default", "rule:1"),
    ).toBe("/policy/bundles/secops~default/rules/rule%3A1");
    expect(mod.__policiesInternal.policyBundleSimulatePath("secops/default")).toBe(
      "/policy/bundles/secops~default/simulate",
    );
  });

  it("readPolicyBundleContent extracts string, content, policy, and data fields", async () => {
    const mod = await loadPoliciesModule();
    expect(mod.__policiesInternal.readPolicyBundleContent("raw-policy")).toBe("raw-policy");
    expect(mod.__policiesInternal.readPolicyBundleContent({ content: "c" })).toBe("c");
    expect(mod.__policiesInternal.readPolicyBundleContent({ policy: "p" })).toBe("p");
    expect(mod.__policiesInternal.readPolicyBundleContent({ data: "d" })).toBe("d");
    expect(mod.__policiesInternal.readPolicyBundleContent({ nope: true })).toBe("");
  });
});

describe("usePolicies hooks", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000123");
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllEnvs();
  });

  it("usePolicyBundles fetches and maps summary + bundled content", async () => {
    const mod = await loadPoliciesModule();
    mockFetch([
      {
        match: "/policy/bundles",
        method: "GET",
        body: {
          items: [
            {
              id: "secops/default",
              enabled: true,
              version: "2",
              updated_at: "2026-02-12T00:00:00.000Z",
            },
          ],
          bundles: {
            "secops/default": {
              content: "rules:\n  - id: r1\n    decision: ALLOW\n",
            },
          },
        },
      },
    ]);

    const hook = renderWithQueryClient(() => mod.usePolicyBundles());
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    expect(hook.result.current?.data?.items[0]).toMatchObject({
      id: "secops/default",
      version: 2,
      enabled: true,
    });
    expect(hook.result.current?.data?.items[0].rules).toHaveLength(1);
    hook.unmount();
  });

  it("usePolicyApprovals derives pending policy changes from bundles", async () => {
    const mod = await loadPoliciesModule();
    mockFetch([
      {
        match: "/policy/bundles",
        method: "GET",
        body: {
          items: [
            { id: "secops/default" },
            { id: "empty/no-rules" },
          ],
          bundles: {
            "secops/default": {
              content: "rules:\n  - id: r1\n    decision: DENY\n",
            },
            "empty/no-rules": {
              content: "rules: []\n",
            },
          },
        },
      },
    ]);

    const hook = renderWithQueryClient(() => mod.usePolicyApprovals());
    await hook.waitFor(() => {
      expect(hook.result.current?.pending.length).toBe(1);
    });
    expect(hook.result.current?.pending[0].bundle.id).toBe("secops/default");
    expect(hook.result.current?.pending[0].changeSummary).toBe("1 new rule");
    hook.unmount();
  });

  it("useSimulatePolicy posts to encoded simulate endpoint and normalizes result", async () => {
    const mod = await loadPoliciesModule();
    const fetchSpy = mockFetch([
      {
        match: "/policy/bundles/secops~default/simulate",
        method: "POST",
        body: {
          decision: "DECISION_TYPE_REQUIRE_HUMAN",
          matched_rule_id: "rule-9",
          reason: "manual review",
          eval_time_ms: 17,
        },
      },
    ]);

    const hook = renderWithQueryClient(() => mod.useSimulatePolicy());
    let result;
    await act(async () => {
      result = await hook.result.current?.mutateAsync({
        bundleId: "secops/default",
        request: { topic: "sys.job.submit" },
      });
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(result).toMatchObject({
      decision: "require_approval",
      matchedRule: "rule-9",
      reason: "manual review",
      evaluationTimeMs: 17,
    });
    hook.unmount();
  });

  it("useUpdatePolicyBundle sends encoded PUT payload and invalidates queries", async () => {
    const mod = await loadPoliciesModule();
    const fetchSpy = mockFetch([
      {
        match: "/policy/bundles/secops~default",
        method: "PUT",
        body: { id: "secops/default", updated_at: "2026-02-13T15:00:00Z" },
      },
    ]);
    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");

    const hook = renderWithQueryClient(() => mod.useUpdatePolicyBundle(), queryClient);
    await act(async () => {
      await hook.result.current?.mutateAsync({
        id: "secops/default",
        content: "rules:\n  - id: rule-1\n    decision: allow\n",
      });
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const requestInit = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    expect(requestInit?.method).toBe("PUT");
    const body = JSON.parse(String(requestInit?.body));
    expect(body).toMatchObject({
      content: "rules:\n  - id: rule-1\n    decision: allow",
    });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["policy-bundles"] });
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ["policy-bundle", "secops/default"],
    });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["policy-rules"] });
    expect(addToastMock).toHaveBeenCalledWith({
      type: "success",
      title: "Policy bundle updated",
    });

    hook.unmount();
  });

  it("usePolicyConfig returns default config when feature flag is disabled", async () => {
    const mod = await loadPoliciesModule(false);
    const hook = renderWithQueryClient(() => mod.usePolicyConfig());

    await hook.waitFor(() => {
      expect(hook.result.current?.data).toEqual(mod.__policiesInternal.DEFAULT_POLICY_CONFIG);
    });
    hook.unmount();
  });

  it("useActivateLockdown and useDeactivateLockdown call endpoints when enabled", async () => {
    const mod = await loadPoliciesModule(true);
    const fetchSpy = mockFetch([
      { match: "/policy/lockdown", method: "POST", body: {} },
      { match: "/policy/lockdown", method: "DELETE", body: {} },
    ]);
    const queryClient = createTestQueryClient();

    const activateHook = renderWithQueryClient(() => mod.useActivateLockdown(), queryClient);
    await act(async () => {
      await activateHook.result.current?.mutateAsync({ reason: "incident response" });
    });
    activateHook.unmount();

    const deactivateHook = renderWithQueryClient(() => mod.useDeactivateLockdown(), queryClient);
    await act(async () => {
      await deactivateHook.result.current?.mutateAsync();
    });
    deactivateHook.unmount();

    expect(fetchSpy).toHaveBeenCalledTimes(2);
    expect(addToastMock).toHaveBeenCalledWith({
      type: "warning",
      title: "Lockdown activated",
    });
    expect(addToastMock).toHaveBeenCalledWith({
      type: "success",
      title: "Lockdown deactivated",
    });
  });
});

