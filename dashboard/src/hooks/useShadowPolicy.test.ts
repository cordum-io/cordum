// Type-shape coverage for the shadow-policy hook contracts.
// Runtime URL/method coverage is at the bottom (task-44807b2c): the hooks
// must call the new `/policy/shadows/{id}` surface with the correct HTTP
// method (PUT for activate, DELETE for deactivate) and emit `to=` as the
// upper-bound query param, not `until=`.
import { act } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import {
  useActivateShadow,
  useDeactivateShadow,
  useShadowPolicy,
  useShadowResultsComparisons,
  useShadowResultsSummary,
  useShadowResultsTimeseries,
} from "./useShadowPolicy";
import type {
  ShadowComparisonEntry,
  ShadowComparisonsResponse,
  ShadowPolicy,
  ShadowPolicySummary,
  ShadowResultsSummary,
  ShadowTimeseriesBucket,
  ShadowTimeseriesResponse,
} from "../api/types";

describe("ShadowPolicy types", () => {
  it("accepts the full shadow policy payload", () => {
    const sp: ShadowPolicy = {
      shadow_bundle_id: "shadow-abcdef012345",
      bundle_id: "secops/bundle-a",
      tenant_id: "default",
      content: "version: 1\nrules: []",
      created_at: "2026-04-18T00:00:00Z",
      activated_at: "2026-04-18T00:00:00Z",
      created_by: "alice",
      metadata: { ticket: "SEC-42" },
    };
    expect(sp.shadow_bundle_id.startsWith("shadow-")).toBe(true);
    expect(sp.bundle_id).toContain("/");
  });

  it("summary drops the content field", () => {
    const summary: ShadowPolicySummary = {
      shadow_bundle_id: "shadow-012",
      bundle_id: "b",
      tenant_id: "t",
      created_at: "x",
      activated_at: "x",
    };
    // Summary is the projection shown in bundle-list cards; if content
    // ever leaks back in, this test forces the test author to notice.
    expect("content" in summary).toBe(false);
  });

  it("shadow results summary counts every outcome bucket", () => {
    const s: ShadowResultsSummary = {
      total_evaluated: 120,
      escalated_count: 40,
      relaxed_count: 15,
      approval_differ_count: 5,
      unchanged_count: 60,
    };
    expect(
      s.escalated_count + s.relaxed_count + s.approval_differ_count + s.unchanged_count,
    ).toBe(s.total_evaluated);
  });

  it("comparison entry carries active + shadow verdicts", () => {
    const e: ShadowComparisonEntry = {
      ts_ms: 1700000000000,
      job_id: "job-1",
      agent_id: "agent-1",
      active_verdict: "allow",
      shadow_verdict: "deny",
      diff: "escalated",
      active_rule_id: "rule-active",
      shadow_rule_id: "rule-shadow",
      latency_ms: 12,
      seq: 42,
    };
    expect(e.diff).toBe("escalated");
  });

  it("comparisons response may be truncated", () => {
    const resp: ShadowComparisonsResponse = {
      entries: [],
      truncated_at_max: true,
    };
    expect(resp.truncated_at_max).toBe(true);
  });

  it("timeseries buckets sum to total", () => {
    const b: ShadowTimeseriesBucket = {
      ts_ms: 1700000000000,
      escalated: 2,
      relaxed: 1,
      approval_differ: 0,
      unchanged: 7,
      total: 10,
    };
    const series: ShadowTimeseriesResponse = { buckets: [b], window_ms: 86400000 };
    expect(series.buckets[0].total).toBe(10);
  });
});

// Runtime URL/method coverage (task-44807b2c). These tests hit the fetch
// layer to lock in the exact wire contract between the hook and
// core/controlplane/gateway/handlers_policy_shadow.go +
// handlers_shadow_results.go.
describe("useShadowPolicy wire contract", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("useActivateShadow PUTs to /policy/shadows/{tildeEncodedID}", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/policy/shadows/secops~bundle-a",
        method: "PUT",
        body: {
          shadow_bundle_id: "shadow-1",
          bundle_id: "secops/bundle-a",
          tenant_id: "default",
          content: "version: 1\nrules: []",
          created_at: "2026-04-23T00:00:00Z",
          activated_at: "2026-04-23T00:00:00Z",
          created_by: "alice",
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useActivateShadow());
    await act(async () => {
      await hook.result.current?.mutateAsync({
        bundleID: "secops/bundle-a",
        content: "version: 1\nrules: []",
      });
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const call = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(call[1].method).toBe("PUT");
    expect(call[0]).toContain("/policy/shadows/secops~bundle-a");
    expect(call[0]).not.toContain("/policy/bundles/");
  });

  it("useDeactivateShadow DELETEs /policy/shadows/{tildeEncodedID}", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/policy/shadows/secops~bundle-a",
        method: "DELETE",
        status: 204,
      },
    ]);

    const hook = renderWithQueryClient(() => useDeactivateShadow());
    await act(async () => {
      await hook.result.current?.mutateAsync("secops/bundle-a");
    });

    const call = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(call[1].method).toBe("DELETE");
    expect(call[0]).toContain("/policy/shadows/secops~bundle-a");
  });

  it("useShadowPolicy GETs /policy/shadows/{tildeEncodedID}", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/policy/shadows/secops~bundle-a",
        method: "GET",
        body: {
          shadow_bundle_id: "shadow-1",
          bundle_id: "secops/bundle-a",
          tenant_id: "default",
          content: "",
          created_at: "",
          activated_at: "",
          created_by: "",
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useShadowPolicy("secops/bundle-a"));
    await hook.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    const call = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect((call[1].method ?? "GET").toUpperCase()).toBe("GET");
    expect(call[0]).toContain("/policy/shadows/secops~bundle-a");
  });

  it("useShadowResultsSummary emits `to=` on the wire (not `until=`)", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/policy/shadows/secops~bundle-a/results/summary",
        method: "GET",
        body: {
          total_evaluated: 0,
          escalated_count: 0,
          relaxed_count: 0,
          approval_differ_count: 0,
          unchanged_count: 0,
        },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useShadowResultsSummary({ bundleID: "secops/bundle-a", fromMs: 100, untilMs: 200 }),
    );
    await hook.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    const call = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(call[0]).toContain("from=100");
    expect(call[0]).toContain("to=200");
    expect(call[0]).not.toContain("until=");
  });

  it("useShadowResultsComparisons emits `to=` on the wire", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/policy/shadows/secops~bundle-a/results/comparisons",
        method: "GET",
        body: { entries: [], truncated_at_max: false },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useShadowResultsComparisons({ bundleID: "secops/bundle-a", fromMs: 100, untilMs: 200 }),
    );
    await hook.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    const call = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(call[0]).toContain("from=100");
    expect(call[0]).toContain("to=200");
    expect(call[0]).not.toContain("until=");
  });

  it("useShadowResultsTimeseries emits `to=` and `bucket=` on the wire", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/policy/shadows/secops~bundle-a/results/timeseries",
        method: "GET",
        body: { buckets: [], window_ms: 0 },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useShadowResultsTimeseries({
        bundleID: "secops/bundle-a",
        fromMs: 100,
        untilMs: 200,
        bucket: "1m",
      }),
    );
    await hook.waitFor(() => {
      expect(fetchSpy).toHaveBeenCalled();
    });

    const call = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(call[0]).toContain("from=100");
    expect(call[0]).toContain("to=200");
    expect(call[0]).toContain("bucket=1m");
    expect(call[0]).not.toContain("until=");
  });
});
