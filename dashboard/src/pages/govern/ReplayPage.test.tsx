import { describe, expect, it } from "vitest";
import type {
  PolicyReplayResponse,
  PolicyReplaySummary,
  PolicyReplayChange,
  PolicyReplayRuleHit,
} from "@/api/types";
import {
  validateTimeRange,
  buildReplayRequest,
  REPLAY_PAGE_SECTIONS,
  MAX_REPLAY_RANGE_DAYS,
  DEFAULT_MAX_JOBS,
  MAX_JOBS_LIMIT,
} from "./ReplayPage";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeSummary(overrides?: Partial<PolicyReplaySummary>): PolicyReplaySummary {
  return {
    total_jobs: 100,
    evaluated: 95,
    escalated: 10,
    relaxed: 5,
    unchanged: 80,
    errored: 5,
    ...overrides,
  };
}

function makeChange(overrides?: Partial<PolicyReplayChange>): PolicyReplayChange {
  return {
    job_id: "job-1",
    topic: "deploy.prod",
    tenant: "acme",
    original_decision: "ALLOW",
    new_decision: "REQUIRE_APPROVAL",
    direction: "escalated",
    ...overrides,
  };
}

function makeRuleHit(overrides?: Partial<PolicyReplayRuleHit>): PolicyReplayRuleHit {
  return {
    rule_id: "rule-deploy-limit",
    decision: "REQUIRE_APPROVAL",
    count: 15,
    ...overrides,
  };
}

function makeResponse(overrides?: Partial<PolicyReplayResponse>): PolicyReplayResponse {
  return {
    replay_id: "replay-abc123",
    policy_snapshot: "snap-v2-abcdef1234567890",
    time_range: { from: "2026-04-13T00:00:00Z", to: "2026-04-14T00:00:00Z" },
    summary: makeSummary(),
    rule_hits: [makeRuleHit()],
    changes: [makeChange()],
    warnings: [],
    errors: [],
    ...overrides,
  };
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

describe("ReplayPage constants", () => {
  it("exports expected section identifiers", () => {
    expect(REPLAY_PAGE_SECTIONS).toContain("form");
    expect(REPLAY_PAGE_SECTIONS).toContain("summary");
    expect(REPLAY_PAGE_SECTIONS).toContain("warnings");
    expect(REPLAY_PAGE_SECTIONS).toContain("changes");
    expect(REPLAY_PAGE_SECTIONS).toContain("rule-hits");
  });

  it("has sensible job limits", () => {
    expect(DEFAULT_MAX_JOBS).toBe(500);
    expect(MAX_JOBS_LIMIT).toBe(1000);
    expect(MAX_REPLAY_RANGE_DAYS).toBe(7);
  });
});

// ---------------------------------------------------------------------------
// validateTimeRange
// ---------------------------------------------------------------------------

describe("validateTimeRange", () => {
  it("returns null for valid 24h range", () => {
    const from = "2026-04-13T00:00";
    const to = "2026-04-14T00:00";
    expect(validateTimeRange(from, to)).toBeNull();
  });

  it("returns null for valid 7-day range", () => {
    const from = "2026-04-07T00:00";
    const to = "2026-04-14T00:00";
    expect(validateTimeRange(from, to)).toBeNull();
  });

  it("returns error when from is empty", () => {
    expect(validateTimeRange("", "2026-04-14T00:00")).toBeTruthy();
  });

  it("returns error when to is empty", () => {
    expect(validateTimeRange("2026-04-13T00:00", "")).toBeTruthy();
  });

  it("returns error when from >= to", () => {
    const same = "2026-04-14T00:00";
    expect(validateTimeRange(same, same)).toBeTruthy();
    expect(validateTimeRange("2026-04-15T00:00", "2026-04-14T00:00")).toBeTruthy();
  });

  it("returns error when range exceeds 7 days", () => {
    const from = "2026-04-01T00:00";
    const to = "2026-04-09T00:00"; // 8 days
    const err = validateTimeRange(from, to);
    expect(err).toBeTruthy();
    expect(err).toContain("7");
  });

  it("returns error for invalid date format", () => {
    expect(validateTimeRange("not-a-date", "2026-04-14T00:00")).toBeTruthy();
  });
});

// ---------------------------------------------------------------------------
// buildReplayRequest
// ---------------------------------------------------------------------------

describe("buildReplayRequest", () => {
  it("builds minimal request with current policy", () => {
    const req = buildReplayRequest({
      from: "2026-04-13T00:00",
      to: "2026-04-14T00:00",
      tenant: "",
      topicPattern: "",
      originalDecision: "",
      useCurrentPolicy: true,
      candidateYaml: "",
      maxJobs: 500,
    });

    expect(req.use_current_policy).toBe(true);
    expect(req.max_jobs).toBe(500);
    // Dates are converted to ISO via new Date(local).toISOString(), so timezone
    // offset may shift the date. Just verify they're valid ISO strings.
    expect(new Date(req.from).getTime()).not.toBeNaN();
    expect(new Date(req.to).getTime()).not.toBeNaN();
    expect(new Date(req.to).getTime()).toBeGreaterThan(new Date(req.from).getTime());
    expect(req.filters).toBeUndefined();
    expect(req.candidate_content).toBeUndefined();
  });

  it("includes filters when provided", () => {
    const req = buildReplayRequest({
      from: "2026-04-13T00:00",
      to: "2026-04-14T00:00",
      tenant: "acme",
      topicPattern: "deploy.*",
      originalDecision: "ALLOW",
      useCurrentPolicy: true,
      candidateYaml: "",
      maxJobs: 200,
    });

    expect(req.filters).toBeDefined();
    expect(req.filters?.tenant).toBe("acme");
    expect(req.filters?.topic_pattern).toBe("deploy.*");
    expect(req.filters?.original_decision).toBe("ALLOW");
  });

  it("includes candidate_content when not using current policy", () => {
    const yaml = "rules:\n  - id: test\n    decision: deny\n";
    const req = buildReplayRequest({
      from: "2026-04-13T00:00",
      to: "2026-04-14T00:00",
      tenant: "",
      topicPattern: "",
      originalDecision: "",
      useCurrentPolicy: false,
      candidateYaml: yaml,
      maxJobs: 500,
    });

    expect(req.use_current_policy).toBe(false);
    // buildReplayRequest trims the YAML, so trailing newline is stripped.
    expect(req.candidate_content).toBe(yaml.trim());
  });

  it("does not include candidate_content when using current policy even if yaml provided", () => {
    const req = buildReplayRequest({
      from: "2026-04-13T00:00",
      to: "2026-04-14T00:00",
      tenant: "",
      topicPattern: "",
      originalDecision: "",
      useCurrentPolicy: true,
      candidateYaml: "some yaml",
      maxJobs: 500,
    });

    expect(req.candidate_content).toBeUndefined();
  });

  it("caps max_jobs at MAX_JOBS_LIMIT", () => {
    const req = buildReplayRequest({
      from: "2026-04-13T00:00",
      to: "2026-04-14T00:00",
      tenant: "",
      topicPattern: "",
      originalDecision: "",
      useCurrentPolicy: true,
      candidateYaml: "",
      maxJobs: 5000,
    });

    expect(req.max_jobs).toBe(MAX_JOBS_LIMIT);
  });

  it("trims whitespace from filter values", () => {
    const req = buildReplayRequest({
      from: "2026-04-13T00:00",
      to: "2026-04-14T00:00",
      tenant: "  acme  ",
      topicPattern: "  deploy.* ",
      originalDecision: "",
      useCurrentPolicy: true,
      candidateYaml: "",
      maxJobs: 500,
    });

    expect(req.filters?.tenant).toBe("acme");
    expect(req.filters?.topic_pattern).toBe("deploy.*");
  });

  it("omits empty filters object when no filters set", () => {
    const req = buildReplayRequest({
      from: "2026-04-13T00:00",
      to: "2026-04-14T00:00",
      tenant: "  ",
      topicPattern: "",
      originalDecision: "",
      useCurrentPolicy: true,
      candidateYaml: "",
      maxJobs: 500,
    });

    expect(req.filters).toBeUndefined();
  });
});

// ---------------------------------------------------------------------------
// Response shape validation
// ---------------------------------------------------------------------------

describe("PolicyReplayResponse shape", () => {
  it("makeResponse produces valid defaults", () => {
    const resp = makeResponse();
    expect(resp.replay_id).toBe("replay-abc123");
    expect(resp.summary.total_jobs).toBe(100);
    expect(resp.changes).toHaveLength(1);
    expect(resp.rule_hits).toHaveLength(1);
    expect(resp.warnings).toHaveLength(0);
    expect(resp.errors).toHaveLength(0);
  });

  it("summary percentages add up correctly", () => {
    const s = makeSummary();
    const total = s.escalated + s.relaxed + s.unchanged + s.errored;
    expect(total).toBeLessThanOrEqual(s.total_jobs);
  });

  it("empty response has zero summary", () => {
    const resp = makeResponse({
      summary: makeSummary({ total_jobs: 0, evaluated: 0, escalated: 0, relaxed: 0, unchanged: 0, errored: 0 }),
      changes: [],
      rule_hits: [],
    });
    expect(resp.summary.total_jobs).toBe(0);
    expect(resp.changes).toHaveLength(0);
    expect(resp.rule_hits).toHaveLength(0);
  });

  it("change direction values are valid union members", () => {
    const directions: PolicyReplayChange["direction"][] = ["escalated", "relaxed", "unchanged"];
    for (const dir of directions) {
      const change = makeChange({ direction: dir });
      expect(["escalated", "relaxed", "unchanged"]).toContain(change.direction);
    }
  });

  it("warnings array populates correctly", () => {
    const resp = makeResponse({
      warnings: [
        "Velocity rules were not replayed",
        "Content scanning was not replayed",
      ],
    });
    expect(resp.warnings).toHaveLength(2);
    expect(resp.warnings[0]).toContain("Velocity");
  });

  it("errors array populates correctly", () => {
    const resp = makeResponse({
      errors: ["job-bad-1: invalid input schema", "job-bad-2: timeout"],
    });
    expect(resp.errors).toHaveLength(2);
  });

  it("rule hits carry decision metadata", () => {
    const hit = makeRuleHit({ rule_id: "rule-cost-cap", decision: "DENY", count: 42 });
    expect(hit.rule_id).toBe("rule-cost-cap");
    expect(hit.decision).toBe("DENY");
    expect(hit.count).toBe(42);
  });
});
