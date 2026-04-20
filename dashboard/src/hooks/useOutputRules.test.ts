import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import {
  __outputRulesInternal,
  useOutputRuleAudit,
  useOutputRules,
  useUpsertOutputRule,
  useToggleOutputRule,
} from "./useOutputRules";

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

describe("useOutputRules hooks", () => {
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

  it("useOutputRules fetches and maps rules from backend shape", async () => {
    mockFetch([
      {
        match: "/policy/output/rules",
        method: "GET",
        body: {
          items: [
            {
              id: "out-secret",
              description: "Block secrets",
              topics: ["job.*"],
              scanners: ["secret"],
              patterns: ["AKIA[0-9A-Z]{16}"],
              pattern_preview: "AKIA...",
              decision: "quarantine",
              severity: "critical",
              enabled: true,
              reason: "secret leak",
              trigger_count_24h: 9,
              last_triggered: "2026-02-13T11:00:00Z",
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useOutputRules());
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data).toEqual([
      {
        id: "out-secret",
        description: "Block secrets",
        topics: ["job.*"],
        scanners: ["secret"],
        patterns: ["AKIA[0-9A-Z]{16}"],
        patternPreview: "AKIA...",
        decision: "quarantine",
        severity: "critical",
        enabled: true,
        reason: "secret leak",
        match: undefined,
        source: undefined,
        triggerCount24h: 9,
        lastTriggered: "2026-02-13T11:00:00Z",
      },
    ]);
    hook.unmount();
  });

  it("useToggleOutputRule sends PUT and updates cached rule state", async () => {
    const queryClient = createTestQueryClient();
    queryClient.setQueryData(["output-rules"], [
      {
        id: "out-secret",
        topics: ["job.*"],
        scanners: ["secret"],
        patterns: [],
        decision: "quarantine",
        severity: "critical",
        enabled: false,
      },
    ]);

    const fetchSpy = mockFetch([
      {
        match: "/policy/output/rules/out-secret",
        method: "PUT",
        body: { id: "out-secret", enabled: true, bundle_id: "secops/default" },
      },
    ]);

    const hook = renderWithQueryClient(() => useToggleOutputRule(), queryClient);
    await act(async () => {
      await hook.result.current?.mutateAsync({ id: "out-secret", enabled: true });
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const call = fetchSpy.mock.calls[0] as [string, RequestInit];
    const payload = JSON.parse(String(call[1].body)) as Record<string, unknown>;
    expect(payload).toEqual({ enabled: true });

    expect(
      queryClient.getQueryData<Array<{ id: string; enabled: boolean }>>(["output-rules"]),
    ).toEqual([
      expect.objectContaining({
        id: "out-secret",
        enabled: true,
      }),
    ]);
    expect(addToastMock).toHaveBeenCalledWith({
      type: "success",
      title: "Output rule updated",
    });
    hook.unmount();
  });

  it("useOutputRuleAudit requests output audit entries and maps findings", async () => {
    mockFetch([
      {
        match: (url) => url.includes("/policy/audit?") && url.includes("type=output") && url.includes("rule_id=out-secret"),
        method: "GET",
        body: {
          items: [
            {
              id: "audit-1",
              created_at: "2026-02-13T12:00:00Z",
              rule_id: "out-secret",
              job_id: "job-1",
              decision: "quarantine",
              reason: "secret found",
              findings: [
                {
                  type: "secret_leak",
                  severity: "critical",
                  detail: "aws key exposed",
                  scanner: "regex",
                  confidence: 0.98,
                  matched_pattern: "AKIA[0-9A-Z]{16}",
                },
              ],
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useOutputRuleAudit("out-secret"));
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.[0]).toEqual({
      id: "audit-1",
      timestamp: "2026-02-13T12:00:00Z",
      ruleId: "out-secret",
      jobId: "job-1",
      decision: "quarantine",
      reason: "secret found",
      phase: undefined,
      originalPtr: undefined,
      redactedPtr: undefined,
      findings: [
        {
          type: "secret_leak",
          severity: "critical",
          detail: "aws key exposed",
          scanner: "regex",
          confidence: 0.98,
          matchedPattern: "AKIA[0-9A-Z]{16}",
        },
      ],
    });
    hook.unmount();
  });

  it("useUpsertOutputRule updates output_rules in bundle content and saves via PUT", async () => {
    const queryClient = createTestQueryClient();
    const fetchSpy = mockFetch([
      {
        match: "/policy/bundles/secops~default",
        method: "GET",
        body: {
          id: "secops/default",
          content:
            "output_rules:\n  - id: out-secret\n    description: old\n    decision: deny\n    severity: high\n    enabled: true\n    match:\n      topics: [job.*]\n      scanners: [regex]\n      content_patterns: [\"old\"]\n",
        },
      },
      {
        match: "/policy/bundles/secops~default",
        method: "PUT",
        body: { id: "secops/default", updated_at: "2026-02-13T12:00:00Z" },
      },
    ]);

    const hook = renderWithQueryClient(() => useUpsertOutputRule(), queryClient);
    await act(async () => {
      await hook.result.current?.mutateAsync({
        bundleId: "secops/default",
        existingRuleId: "out-secret",
        draft: {
          id: "out-secret",
          description: "updated",
          pattern: "AKIA[0-9A-Z]{16}",
          decision: "deny",
          severity: "critical",
          enabled: true,
          reason: "credential leak",
          topics: ["job.*"],
          scanners: ["regex", "secret"],
        },
      });
    });

    expect(fetchSpy).toHaveBeenCalledTimes(2);
    const putCall = fetchSpy.mock.calls[1] as [string, RequestInit];
    expect(putCall[1].method).toBe("PUT");
    const payload = JSON.parse(String(putCall[1].body)) as { content: string };
    expect(payload.content).toContain("id: out-secret");
    expect(payload.content).toContain("AKIA[0-9A-Z]{16}");
    expect(payload.content).toContain("content_patterns:");
    expect(addToastMock).toHaveBeenCalledWith({
      type: "success",
      title: "Output rule saved",
    });
    hook.unmount();
  });
});

describe("useOutputRules internals", () => {
  it("buildOutputAuditPath encodes type and rule_id", () => {
    expect(__outputRulesInternal.buildOutputAuditPath("out-secret")).toBe(
      "/policy/audit?type=output&rule_id=out-secret",
    );
  });
});
