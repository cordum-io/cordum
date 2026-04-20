import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: () => ({
      matches: false,
      media: "",
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
});

import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { Job, SafetyDecision } from "../../api/types";

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("react-router-dom", () => ({
  Link: ({ to, children, ...rest }: { to: string; children: React.ReactNode }) =>
    React.createElement("a", { href: to, "data-testid": "link", ...rest }, children),
}));

vi.mock("../../lib/logger", () => ({
  logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "j-test-12345678",
    type: "default",
    topic: "sys.job.submit",
    status: "succeeded",
    pool: "default-pool",
    capabilities: [],
    riskTags: [],
    metadata: {},
    createdAt: "2026-02-13T12:00:00.000Z",
    updatedAt: "2026-02-13T12:05:00.000Z",
    ...overrides,
  };
}

function makeDecision(overrides: Partial<SafetyDecision> = {}): SafetyDecision {
  return {
    type: "allow",
    reason: "Policy matched",
    ...overrides,
  };
}

let GovernanceTrace: React.ComponentType<{ job: Job; decision?: SafetyDecision }>;

beforeEach(async () => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);

  vi.clearAllMocks();

  const mod = await import("./GovernanceTrace");
  GovernanceTrace = mod.GovernanceTrace;
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render(job: Job, decision?: SafetyDecision) {
  act(() => {
    root.render(<GovernanceTrace job={job} decision={decision} />);
  });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("GovernanceTrace", () => {
  it("renders Action pill for minimal job (no optional fields)", () => {
    render(makeJob());
    const text = container.textContent ?? "";
    expect(text).toContain("Action j-test-1");
    // No workflow, rule, approval, or audit pills
    expect(text).not.toContain("Run ");
    expect(text).not.toContain("Rule ");
    expect(text).not.toContain("Approval");
    expect(text).not.toContain("Audit Trail");
  });

  it("renders Action + Audit Trail when traceId exists", () => {
    render(makeJob({ traceId: "trace-abc123" }));
    const text = container.textContent ?? "";
    expect(text).toContain("Action j-test-1");
    expect(text).toContain("Audit Trail");
  });

  it("renders all 5 pills when all data present", () => {
    render(
      makeJob({
        workflowRunId: "wfr-99887766",
        approvalRef: "ap-ref-001",
        traceId: "trace-xyz",
      }),
      makeDecision({ matchedRule: "prod-write" }),
    );
    const text = container.textContent ?? "";
    expect(text).toContain("Action j-test-1");
    expect(text).toContain("Run wfr-9988");
    expect(text).toContain("Rule prod-write");
    expect(text).toContain("Approval");
    expect(text).toContain("Audit Trail");
  });

  it("hides Workflow Run pill when no workflowRunId", () => {
    render(
      makeJob({ traceId: "trace-1" }),
      makeDecision({ matchedRule: "some-rule" }),
    );
    expect(container.textContent).not.toContain("Run ");
  });

  it("hides Rule pill when no matchedRule in decision", () => {
    render(makeJob({ workflowRunId: "wfr-123", traceId: "trace-1" }), makeDecision());
    expect(container.textContent).not.toContain("Rule ");
  });

  it("hides Approval pill when no approvalRef", () => {
    render(makeJob({ traceId: "trace-1" }));
    expect(container.textContent).not.toContain("Approval");
  });

  it("renders correct link hrefs", () => {
    render(
      makeJob({
        workflowRunId: "wfr-99887766",
        approvalRef: "ap-ref-001",
        traceId: "trace-xyz",
      }),
      makeDecision({ matchedRule: "prod-write" }),
    );
    const links = container.querySelectorAll("a[data-testid='link']");
    const hrefs = Array.from(links).map((a) => a.getAttribute("href"));
    expect(hrefs).toContain("/runs/wfr-99887766");
    expect(hrefs).toContain("/policies/rules#prod-write");
    expect(hrefs).toContain("/approvals?id=ap-ref-001");
    expect(hrefs).toContain("/audit?correlationId=trace-xyz");
  });

  it("Action pill is a span (not a link)", () => {
    render(makeJob({ traceId: "trace-1" }));
    // Action pill should be a span, not a link
    const spans = container.querySelectorAll("span");
    const actionSpan = Array.from(spans).find((s) => s.textContent?.includes("Action j-test-1"));
    expect(actionSpan).toBeTruthy();
    // It should NOT be inside a link
    expect(actionSpan?.closest("a")).toBeNull();
  });

  it("renders ChevronRight connectors between pills", () => {
    render(makeJob({ traceId: "trace-1" }));
    // With 2 pills (Action + Audit Trail), there should be 1 SVG connector
    const svgs = container.querySelectorAll("svg");
    expect(svgs).toHaveLength(1);
  });

  it("truncates job ID to first 8 chars", () => {
    render(makeJob({ id: "j-abcdefghijklmnop" }));
    const text = container.textContent ?? "";
    expect(text).toContain("Action j-abcdef");
    expect(text).not.toContain("j-abcdefghijklmnop");
  });
});
