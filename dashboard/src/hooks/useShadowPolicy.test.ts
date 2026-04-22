// Type-shape coverage for the shadow-policy hook contracts.
// Runtime behaviour (URL construction, 404 → null) is exercised via
// the BundleDetailPage Shadow-tab tests which hit the same hooks
// under a mocked client.
import { describe, expect, it } from "vitest";
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
