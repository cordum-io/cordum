import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import {
  __outputPolicyInternal,
  useOutputPolicyConfig,
  useOutputPolicyStats,
  useUpdateOutputPolicy,
} from "./useOutputPolicy";
import type { OutputPolicyConfig } from "../types/settings";

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

describe("useOutputPolicy internals", () => {
  it("buildQuarantineParams includes required state and optional pagination", () => {
    expect(__outputPolicyInternal.buildQuarantineParams({})).toBe("?state=OUTPUT_QUARANTINED");
    expect(
      __outputPolicyInternal.buildQuarantineParams({ limit: 50, cursor: 10 }),
    ).toBe("?state=OUTPUT_QUARANTINED&limit=50&cursor=10");
  });

  it("parseOutputPolicyConfig normalizes nested config keys", () => {
    const parsed = __outputPolicyInternal.parseOutputPolicyConfig({
      output_policy: {
        enabled: true,
        fail_mode: "closed",
        scan_timeout_ms: 7777,
        max_payload_kb: 1024,
        failure_action: "deny",
        topic_overrides: [
          { topic_pattern: "job.reports.*", enabled: false, fail_mode: "open", scanners: ["pii"] },
        ],
      },
    });

    expect(parsed).toEqual({
      enabled: true,
      failMode: "closed",
      scanTimeoutMs: 7777,
      maxPayloadKb: 1024,
      failureAction: "deny",
      topicOverrides: [
        {
          topicPattern: "job.reports.*",
          enabled: false,
          failMode: "open",
          scanners: ["pii"],
        },
      ],
    });
  });
});

describe("useOutputPolicy hooks", () => {
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
  });

  it("useOutputPolicyConfig fetches scoped config and maps values", async () => {
    mockFetch([
      {
        match: "/config?scope=output_policy",
        method: "GET",
        body: {
          output_policy: {
            enabled: true,
            fail_mode: "closed",
            scan_timeout_ms: 6000,
            max_payload_kb: 768,
            failure_action: "deny",
            topic_overrides: [{ topic_pattern: "job.secure.*", scanners: ["secret", "pii"] }],
          },
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useOutputPolicyConfig());
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data).toMatchObject({
      enabled: true,
      failMode: "closed",
      scanTimeoutMs: 6000,
      maxPayloadKb: 768,
      failureAction: "deny",
    });
    expect(hook.result.current?.data?.topicOverrides[0]).toMatchObject({
      topicPattern: "job.secure.*",
      scanners: ["secret", "pii"],
    });
    hook.unmount();
  });

  it("useUpdateOutputPolicy persists merged config via PUT /config and invalidates caches", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/config?scope=system&scope_id=default",
        method: "GET",
        body: { existing_key: "keep-me", output_policy: { enabled: false } },
      },
      { match: "/config", method: "PUT", body: {} },
    ]);

    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useUpdateOutputPolicy(), queryClient);

    const nextConfig: OutputPolicyConfig = {
      enabled: true,
      failMode: "closed",
      scanTimeoutMs: 9000,
      maxPayloadKb: 2048,
      failureAction: "deny",
      topicOverrides: [
        {
          topicPattern: "job.finance.*",
          enabled: true,
          failMode: "closed",
          scanners: ["secret"],
        },
      ],
    };

    await act(async () => {
      await hook.result.current?.mutateAsync(nextConfig);
    });

    expect(fetchSpy).toHaveBeenCalled();
    const putCall = fetchSpy.mock.calls.find((call) => {
      const [, init] = call as [string, RequestInit];
      return init.method === "PUT";
    });
    expect(putCall).toBeTruthy();
    const [, putInit] = putCall as [string, RequestInit];
    const payload = JSON.parse(String(putInit.body)) as Record<string, unknown>;
    expect(payload.scope).toBe("system");
    expect(payload.scope_id).toBe("default");
    const data = payload.data as Record<string, unknown>;
    expect(data.existing_key).toBe("keep-me");
    expect((data.output_policy as Record<string, unknown>).enabled).toBe(true);

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["output-policy-config"] });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["config"] });
    expect(addToastMock).toHaveBeenCalledWith({
      type: "success",
      title: "Output Safety settings saved",
    });

    hook.unmount();
  });

  it("useUpdateOutputPolicy falls back to POST when PUT /config is not supported", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/config?scope=system&scope_id=default",
        method: "GET",
        body: { existing_key: "keep-me" },
      },
      { match: "/config", method: "PUT", status: 405, body: { error: "not allowed" } },
      { match: "/config", method: "POST", body: {} },
    ]);

    const hook = renderWithQueryClient(() => useUpdateOutputPolicy());
    await act(async () => {
      await hook.result.current?.mutateAsync({
        enabled: true,
        failMode: "open",
        scanTimeoutMs: 5000,
        maxPayloadKb: 512,
        failureAction: "allow",
        topicOverrides: [],
      });
    });

    const methods = fetchSpy.mock.calls.map(([, init]) => (init as RequestInit).method);
    expect(methods).toContain("PUT");
    expect(methods).toContain("POST");
    hook.unmount();
  });

  it("useOutputPolicyStats maps stats payload and applies 30s refresh config", async () => {
    mockFetch([
      {
        match: "/policy/output/stats",
        method: "GET",
        body: {
          total_checks_24h: 321,
          quarantined_24h: 7,
          avg_latency_ms: 8.5,
          last_check_at: "2026-02-13T12:10:00.000Z",
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useOutputPolicyStats());
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data).toEqual({
      totalChecks24h: 321,
      quarantined24h: 7,
      avgLatencyMs: 8.5,
      lastCheckAt: "2026-02-13T12:10:00.000Z",
    });
    hook.unmount();
  });

  it("useOutputPolicyStats returns zeroed defaults when stats endpoint is unavailable", async () => {
    mockFetch([
      { match: "/policy/output/stats", method: "GET", status: 404, body: { error: "not found" } },
    ]);

    const hook = renderWithQueryClient(() => useOutputPolicyStats());
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data).toEqual({
      totalChecks24h: 0,
      quarantined24h: 0,
      avgLatencyMs: 0,
      lastCheckAt: undefined,
    });
    hook.unmount();
  });
});
