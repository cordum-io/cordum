import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { http, HttpResponse } from "msw";
import { fireEvent, screen, waitFor } from "@testing-library/dom";
import { cleanup } from "@testing-library/react";
import { renderWithProviders } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import { DecisionType } from "@/api/generated/model/decisionType";
import type { Decision } from "@/api/generated/model/decision";
import { DecisionExpandRow } from "./DecisionExpandRow";

const baseDecision: Decision = {
  source: DecisionSource.job,
  rule_id: "rule.input.secret-scan",
  bundle_id: "bundle.acme.input",
  bundle_version: "v3",
  type: DecisionType.deny,
  timestamp: "2026-05-10T12:00:00Z",
  audit_hash: "sha256:replay01",
  input_ref: "blob://acme/in/01HQ",
};

function mockReplay(body: {
  policy_snapshot?: string;
  changes?: unknown[];
  unchanged?: number;
  status?: number;
}) {
  const status = body.status ?? 200;
  return http.post("*/api/v1/policy/replay", () => {
    if (status !== 200) {
      return HttpResponse.json({ error: "internal" }, { status });
    }
    return HttpResponse.json({
      replay_id: "rep-x",
      policy_snapshot: body.policy_snapshot ?? "snap:v3",
      time_range: { from: baseDecision.timestamp, to: baseDecision.timestamp },
      summary: {
        total_jobs: 1,
        evaluated: 1,
        unchanged: body.unchanged ?? 0,
        escalated: 0,
        relaxed: body.changes && body.changes.length > 0 ? 1 : 0,
        errored: 0,
      },
      rule_hits: [],
      changes: body.changes ?? [],
    });
  });
}

describe("DecisionExpandRow — Replay action (D9b)", () => {
  beforeEach(() => {
    server.resetHandlers();
    // Provide a default artifact handler so useArtifact (driven by
    // decision.input_ref) doesn't fail and re-render the row.
    server.use(
      http.get("*/api/v1/artifacts/*", () =>
        HttpResponse.json({ content_base64: btoa('{"prompt":"redacted"}') }),
      ),
    );
  });

  afterEach(() => {
    cleanup();
  });

  it("Replay button is no longer a passive stub — clicking calls /api/v1/policy/replay", async () => {
    let posted = false;
    server.use(
      http.post("*/api/v1/policy/replay", () => {
        posted = true;
        return HttpResponse.json({
          replay_id: "rep-x",
          policy_snapshot: "snap:vX",
          time_range: { from: baseDecision.timestamp, to: baseDecision.timestamp },
          summary: { total_jobs: 1, evaluated: 1, unchanged: 1, escalated: 0, relaxed: 0, errored: 0 },
          rule_hits: [],
          changes: [],
        });
      }),
    );
    renderWithProviders(<DecisionExpandRow decision={baseDecision} />);

    // Disambiguate by exact accessible name to avoid the regex matching
    // both the action-row Replay button and the inline ReplayResult panel
    // which mentions "Replay" in its prose.
    const replay = await screen.findByRole("button", { name: /^replay this decision/i });
    // The data-stub="d9b" attribute is the canary used by the D8b test
    // suite to confirm the button was a no-op stub. After D9b wires the
    // handler, the attribute MUST be gone (Phase 6 (f) self-review).
    expect(replay.getAttribute("data-stub")).toBeNull();
    fireEvent.click(replay);

    await waitFor(() => {
      expect(posted).toBe(true);
    });
  });

  it("renders 'If evaluated now: <now> (was: <was>)' and a warning when the decision changed", async () => {
    server.use(
      mockReplay({
        policy_snapshot: "snap:v7",
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
    );
    renderWithProviders(<DecisionExpandRow decision={baseDecision} />);

    fireEvent.click(await screen.findByRole("button", { name: /^replay this decision/i }));

    const result = await screen.findByTestId("decision-replay-result");
    expect(result.textContent ?? "").toMatch(/if evaluated now/i);
    // Both the now and the was badges show their labels.
    expect(result.textContent ?? "").toMatch(/allow/i);
    expect(result.textContent ?? "").toMatch(/was/i);
    expect(result.textContent ?? "").toMatch(/deny/i);
    // Highlight class for the changed state — the row should expose
    // data-changed="true" so QA can confirm the visual highlight branch.
    expect(result.getAttribute("data-changed")).toBe("true");
  });

  it("renders a neutral 'No change' branch when was==now (summary.unchanged > 0)", async () => {
    server.use(mockReplay({ policy_snapshot: "snap:v3", unchanged: 1, changes: [] }));
    renderWithProviders(<DecisionExpandRow decision={baseDecision} />);

    fireEvent.click(await screen.findByRole("button", { name: /^replay this decision/i }));

    const result = await screen.findByTestId("decision-replay-result");
    expect(result.textContent ?? "").toMatch(/no change/i);
    expect(result.getAttribute("data-changed")).toBe("false");
  });

  it("shows a loading state while the replay request is in flight", async () => {
    let release: (() => void) | null = null;
    server.use(
      http.post("*/api/v1/policy/replay", async () => {
        await new Promise<void>((resolve) => {
          release = resolve;
        });
        return HttpResponse.json({
          replay_id: "rep-x",
          policy_snapshot: "snap:v3",
          time_range: { from: baseDecision.timestamp, to: baseDecision.timestamp },
          summary: { total_jobs: 1, evaluated: 1, unchanged: 1, escalated: 0, relaxed: 0, errored: 0 },
          rule_hits: [],
          changes: [],
        });
      }),
    );
    renderWithProviders(<DecisionExpandRow decision={baseDecision} />);
    fireEvent.click(await screen.findByRole("button", { name: /^replay this decision/i }));

    expect(
      await screen.findByTestId("decision-replay-loading"),
    ).not.toBeNull();
    (release as (() => void) | null)?.();
  });

  it("surfaces an inline error message when the gateway returns 500", async () => {
    server.use(mockReplay({ status: 500 }));
    renderWithProviders(<DecisionExpandRow decision={baseDecision} />);

    fireEvent.click(await screen.findByRole("button", { name: /^replay this decision/i }));

    const error = await screen.findByTestId("decision-replay-error");
    expect(error.textContent ?? "").toMatch(/couldn't replay|replay failed|error/i);
  });
});
