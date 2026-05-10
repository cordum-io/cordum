import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { http, HttpResponse } from "msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor, act } from "@testing-library/react";
import type { ReactNode } from "react";
import { ensureMswServerListening, server } from "@/test-utils/msw";
import { useConfigStore } from "@/state/config";
import { DecisionType } from "@/api/generated/model/decisionType";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import type { Decision } from "@/api/generated/model/decision";
import { useReplayDecision } from "./useReplayDecision";

const sampleDecision: Decision = {
  source: DecisionSource.job,
  rule_id: "rule.input.secret-scan",
  bundle_id: "bundle.acme.input",
  bundle_version: "v3",
  type: DecisionType.deny,
  timestamp: "2026-05-10T12:00:00Z",
  audit_hash: "sha256:abcdef0123",
  input_ref: "blob://x/y",
};

function makeWrapper() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  return {
    client,
    Wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  };
}

describe("useReplayDecision (D9b — POST /api/v1/policy/replay wrapper)", () => {
  beforeEach(() => {
    ensureMswServerListening();
    server.resetHandlers();
    useConfigStore.setState({ apiKey: "test-key", apiBaseUrl: "" });
  });

  afterEach(() => {
    useConfigStore.setState({ apiKey: "", apiBaseUrl: "" });
  });

  it("POSTs to /api/v1/policy/replay with use_current_policy + filter on original decision type", async () => {
    let captured: Record<string, unknown> | null = null;
    server.use(
      http.post("*/api/v1/policy/replay", async ({ request }) => {
        captured = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({
          replay_id: "rep-1",
          policy_snapshot: "snap:v3",
          time_range: { from: sampleDecision.timestamp, to: sampleDecision.timestamp },
          summary: { total_jobs: 1, evaluated: 1, unchanged: 0, escalated: 1, relaxed: 0, errored: 0 },
          rule_hits: [],
          changes: [
            {
              job_id: "j-1",
              topic: "t",
              tenant: "default",
              original_decision: "deny",
              new_decision: "allow",
              direction: "relaxed",
            },
          ],
        });
      }),
    );
    const { Wrapper } = makeWrapper();
    const { result } = renderHook(() => useReplayDecision(), { wrapper: Wrapper });

    await act(async () => {
      await result.current.mutateAsync(sampleDecision);
    });

    expect(captured).not.toBeNull();
    const body = captured as unknown as Record<string, unknown>;
    expect(typeof body.from).toBe("string");
    expect(typeof body.to).toBe("string");
    expect(body.use_current_policy).toBe(true);
    expect(body.max_jobs).toBe(1);
    const filters = body.filters as { original_decision?: string };
    expect(filters?.original_decision).toBe("deny");
  });

  it("returns {was, now, bundleVersion, changed:true} when the response carries a change", async () => {
    server.use(
      http.post("*/api/v1/policy/replay", () =>
        HttpResponse.json({
          replay_id: "rep-2",
          policy_snapshot: "snap:v7",
          time_range: { from: sampleDecision.timestamp, to: sampleDecision.timestamp },
          summary: { total_jobs: 1, evaluated: 1, unchanged: 0, escalated: 0, relaxed: 1, errored: 0 },
          rule_hits: [],
          changes: [
            {
              job_id: "j-1",
              topic: "t",
              tenant: "default",
              original_decision: "deny",
              new_decision: "allow",
              direction: "relaxed",
            },
          ],
        }),
      ),
    );
    const { Wrapper } = makeWrapper();
    const { result } = renderHook(() => useReplayDecision(), { wrapper: Wrapper });

    let outcome: Awaited<ReturnType<typeof result.current.mutateAsync>> | undefined;
    await act(async () => {
      outcome = await result.current.mutateAsync(sampleDecision);
    });

    expect(outcome).toEqual({
      was: DecisionType.deny,
      now: DecisionType.allow,
      bundleVersion: "snap:v7",
      changed: true,
    });
  });

  it("returns {was, now (==was), changed:false} when summary.unchanged > 0 and changes is empty", async () => {
    server.use(
      http.post("*/api/v1/policy/replay", () =>
        HttpResponse.json({
          replay_id: "rep-3",
          policy_snapshot: "snap:v4",
          time_range: { from: sampleDecision.timestamp, to: sampleDecision.timestamp },
          summary: { total_jobs: 1, evaluated: 1, unchanged: 1, escalated: 0, relaxed: 0, errored: 0 },
          rule_hits: [],
          changes: [],
        }),
      ),
    );
    const { Wrapper } = makeWrapper();
    const { result } = renderHook(() => useReplayDecision(), { wrapper: Wrapper });

    let outcome: Awaited<ReturnType<typeof result.current.mutateAsync>> | undefined;
    await act(async () => {
      outcome = await result.current.mutateAsync(sampleDecision);
    });

    expect(outcome).toEqual({
      was: DecisionType.deny,
      now: DecisionType.deny,
      bundleVersion: "snap:v4",
      changed: false,
    });
  });

  it("rejects + flips isError when the gateway returns 500", async () => {
    server.use(
      http.post("*/api/v1/policy/replay", () =>
        HttpResponse.json({ error: "internal" }, { status: 500 }),
      ),
    );
    const { Wrapper } = makeWrapper();
    const { result } = renderHook(() => useReplayDecision(), { wrapper: Wrapper });

    await act(async () => {
      try {
        await result.current.mutateAsync(sampleDecision);
        throw new Error("expected mutateAsync to reject");
      } catch {
        // expected
      }
    });

    await waitFor(() => {
      expect(result.current.isError).toBe(true);
    });
  });

  it("invalidates the policy-studio decisions cache on success", async () => {
    server.use(
      http.post("*/api/v1/policy/replay", () =>
        HttpResponse.json({
          replay_id: "rep-4",
          policy_snapshot: "snap:v9",
          time_range: { from: sampleDecision.timestamp, to: sampleDecision.timestamp },
          summary: { total_jobs: 1, evaluated: 1, unchanged: 1, escalated: 0, relaxed: 0, errored: 0 },
          rule_hits: [],
          changes: [],
        }),
      ),
    );
    const { client, Wrapper } = makeWrapper();
    const invalidateSpy = vi.spyOn(client, "invalidateQueries");
    const { result } = renderHook(() => useReplayDecision(), { wrapper: Wrapper });

    await act(async () => {
      await result.current.mutateAsync(sampleDecision);
    });

    expect(invalidateSpy).toHaveBeenCalled();
    const calls = invalidateSpy.mock.calls.map(([arg]) => arg);
    const matched = calls.some((arg) => {
      const key = (arg as { queryKey?: unknown[] } | undefined)?.queryKey;
      return Array.isArray(key) && key.includes("policy-studio-decisions");
    });
    expect(matched).toBe(true);
  });
});
