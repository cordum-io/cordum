import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { UnifiedTimeline, type UnifiedTimelineProps } from "./UnifiedTimeline";
import type { TimelineEvent, Job, JobProgressEvent } from "../../api/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// ---------------------------------------------------------------------------
// Mock useAuthConfig so usePermission doesn't blow up (not used directly here
// but imported transitively). We only test canApprove prop gating.
// ---------------------------------------------------------------------------

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const baseJob: Job = {
  id: "j1",
  type: "default",
  topic: "sys.job.submit",
  status: "running",
  pool: "default",
  capabilities: [],
  riskTags: [],
  metadata: {},
  createdAt: "2026-02-13T12:00:00.000Z",
  updatedAt: "2026-02-13T12:05:00.000Z",
};

const baseProps: UnifiedTimelineProps = {
  events: [],
  job: baseJob,
  progressEvents: [],
  latestPercent: null,
  isStreaming: false,
  lastHeartbeat: null,
  canApprove: false,
};

function mkEvent(overrides: Partial<TimelineEvent>): TimelineEvent {
  return {
    timestamp: "2026-02-13T12:00:00.000Z",
    kind: "submitted",
    title: "Job Submitted",
    status: "completed",
    ...overrides,
  };
}

function renderTimeline(overrides: Partial<UnifiedTimelineProps> = {}) {
  const props = { ...baseProps, ...overrides };
  act(() => {
    root.render(React.createElement(UnifiedTimeline, props));
  });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("UnifiedTimeline", () => {
  it("renders timeline entries for a full lifecycle", () => {
    const events: TimelineEvent[] = [
      mkEvent({ kind: "submitted", title: "Job Submitted", status: "completed" }),
      mkEvent({ kind: "policy_evaluated", title: "Policy Evaluated", status: "completed", timestamp: "2026-02-13T12:00:01.000Z" }),
      mkEvent({ kind: "dispatched", title: "Dispatched", status: "completed", timestamp: "2026-02-13T12:00:02.000Z" }),
      mkEvent({ kind: "executing", title: "Executing", status: "current", timestamp: "2026-02-13T12:00:03.000Z" }),
      mkEvent({ kind: "succeeded", title: "Succeeded", status: "completed", timestamp: "2026-02-13T12:05:00.000Z" }),
    ];
    renderTimeline({ events });

    expect(container.textContent).toContain("Job Submitted");
    expect(container.textContent).toContain("Policy Evaluated");
    expect(container.textContent).toContain("Dispatched");
    expect(container.textContent).toContain("Executing");
    expect(container.textContent).toContain("Succeeded");
  });

  it("renders executing entry with progress bar when latestPercent provided", () => {
    const events = [mkEvent({ kind: "executing", title: "Executing", status: "current" })];
    renderTimeline({
      events,
      job: { ...baseJob, status: "running" },
      latestPercent: 42,
      isStreaming: true,
    });

    expect(container.textContent).toContain("Executing");
    expect(container.textContent).toContain("42%");
    expect(container.textContent).toContain("Progress");
    expect(container.textContent).toContain("Receiving updates...");
  });

  it("renders executing entry with progress messages", () => {
    const events = [mkEvent({ kind: "executing", title: "Executing", status: "current" })];
    const progressEvents: JobProgressEvent[] = [
      { timestamp: "2026-02-13T12:01:00.000Z", percent: 10, message: "Processing step 1" },
      { timestamp: "2026-02-13T12:02:00.000Z", percent: 50, message: "Processing step 2" },
    ];
    renderTimeline({ events, progressEvents, latestPercent: 50 });

    expect(container.textContent).toContain("Processing step 1");
    expect(container.textContent).toContain("Processing step 2");
  });

  it("shows heartbeat warning when > 15s ago", () => {
    const events = [mkEvent({ kind: "executing", title: "Executing", status: "current" })];
    const thirtySecsAgo = new Date(Date.now() - 30_000).toISOString();
    renderTimeline({
      events,
      job: { ...baseJob, status: "running" },
      lastHeartbeat: thirtySecsAgo,
      isStreaming: true,
    });

    expect(container.textContent).toMatch(/Last heartbeat \d+s ago/);
  });

  it("renders denied entry with remediation buttons", () => {
    const events = [mkEvent({ kind: "denied", title: "Denied", status: "error" })];
    const onRemediate = vi.fn();
    const onQuickRemediate = vi.fn();
    renderTimeline({
      events,
      job: {
        ...baseJob,
        status: "denied",
        safetyDecision: { type: "deny", reason: "Blocked by policy", matchedRule: "rule-1" },
      },
      onRemediate,
      onQuickRemediate,
    });

    expect(container.textContent).toContain("Blocked by policy");
    expect(container.textContent).toContain("rule-1");

    const buttons = container.querySelectorAll("button");
    const applyBtn = Array.from(buttons).find((b) => b.textContent?.includes("Apply Remediation"));
    const customizeBtn = Array.from(buttons).find((b) => b.textContent?.includes("Customize"));
    expect(applyBtn).toBeTruthy();
    expect(customizeBtn).toBeTruthy();

    act(() => applyBtn!.click());
    expect(onQuickRemediate).toHaveBeenCalledTimes(1);

    act(() => customizeBtn!.click());
    expect(onRemediate).toHaveBeenCalledTimes(1);
  });

  it("renders approval_requested with approve/reject when canApprove=true", () => {
    const events = [mkEvent({ kind: "approval_requested", title: "Approval Required", status: "warning" })];
    const onApprove = vi.fn();
    const onReject = vi.fn();
    renderTimeline({ events, canApprove: true, onApprove, onReject });

    expect(container.textContent).toContain("Approval Required");
    const buttons = container.querySelectorAll("button");
    const approveBtn = Array.from(buttons).find((b) => b.textContent === "Approve");
    const rejectBtn = Array.from(buttons).find((b) => b.textContent === "Reject");
    expect(approveBtn).toBeTruthy();
    expect(rejectBtn).toBeTruthy();

    act(() => approveBtn!.click());
    expect(onApprove).toHaveBeenCalledWith();
  });

  it("hides approval buttons when canApprove=false", () => {
    const events = [mkEvent({ kind: "approval_requested", title: "Approval Required", status: "warning" })];
    renderTimeline({ events, canApprove: false });

    const buttons = container.querySelectorAll("button");
    const approveBtn = Array.from(buttons).find((b) => b.textContent === "Approve");
    expect(approveBtn).toBeFalsy();
  });

  it("reject shows inline input and submits reason", () => {
    const events = [mkEvent({ kind: "approval_requested", title: "Approval Required", status: "warning" })];
    const onReject = vi.fn();
    renderTimeline({ events, canApprove: true, onReject });

    // Click reject to show input
    const rejectBtn = Array.from(container.querySelectorAll("button")).find((b) => b.textContent === "Reject")!;
    act(() => rejectBtn.click());

    const input = container.querySelector("input")!;
    expect(input).toBeTruthy();
    expect(input.placeholder).toContain("Reason for rejection");

    // Type a reason and submit
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "Not compliant");
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
    });

    // Click the submit reject button
    const submitReject = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent === "Reject" && b !== rejectBtn,
    )!;
    act(() => submitReject.click());

    expect(onReject).toHaveBeenCalledWith("Not compliant");
  });

  it("expandable toggle shows/hides context data", () => {
    const events = [
      mkEvent({
        kind: "policy_evaluated",
        title: "Policy Evaluated",
        status: "completed",
        expandedData: { rule: "block-external", evalMs: 12 },
      }),
    ];
    renderTimeline({ events });

    // Initially collapsed — context data not shown
    expect(container.textContent).not.toContain("block-external");

    // Click to expand
    const clickable = container.querySelector("[role='button']")!;
    act(() => (clickable as HTMLElement).click());

    // Now shows context
    expect(container.textContent).toContain("block-external");
    expect(container.textContent).toContain("12");

    // Click to collapse
    act(() => (clickable as HTMLElement).click());
    expect(container.textContent).not.toContain("block-external");
  });

  it("renders empty events gracefully", () => {
    renderTimeline({ events: [] });
    expect(container.textContent).toContain("No timeline events available");
  });

  it("renders approved entry with approver details", () => {
    const events = [
      mkEvent({
        kind: "approved",
        title: "Approved",
        status: "completed",
        expandedData: {
          approvedBy: "admin@corp.com",
          approvalRole: "admin",
          waitMs: 45_000,
          approvalNote: "Looks good",
        },
      }),
    ];
    renderTimeline({ events });

    expect(container.textContent).toContain("admin@corp.com");
    expect(container.textContent).toContain("admin");
    expect(container.textContent).toContain("45.0s");
    expect(container.textContent).toContain("Looks good");
  });

  it("renders output_scanned entry with findings badge", () => {
    const events = [
      mkEvent({
        kind: "output_scanned",
        title: "Output Scanned",
        status: "completed",
        expandedData: { findings: 3, scanner: "regex-scanner" },
      }),
    ];
    renderTimeline({ events });

    expect(container.textContent).toContain("3 findings");
    expect(container.textContent).toContain("regex-scanner");
  });

  it("renders succeeded entry with duration", () => {
    const events = [
      mkEvent({
        kind: "succeeded",
        title: "Succeeded",
        status: "completed",
        expandedData: { durationMs: 2500, resultPtr: "redis://result:j1" },
      }),
    ];
    renderTimeline({ events });

    expect(container.textContent).toContain("2.5s");
    expect(container.textContent).toContain("redis://result:j1");
  });

  it("applies stagger animation delay per entry", () => {
    const events = [
      mkEvent({ kind: "submitted", title: "First" }),
      mkEvent({ kind: "dispatched", title: "Second", timestamp: "2026-02-13T12:00:01.000Z" }),
      mkEvent({ kind: "executing", title: "Third", timestamp: "2026-02-13T12:00:02.000Z", status: "current" }),
    ];
    renderTimeline({ events });

    const entries = container.querySelectorAll(".animate-fade-in");
    expect(entries).toHaveLength(3);
    expect((entries[0] as HTMLElement).style.animationDelay).toBe("0ms");
    expect((entries[1] as HTMLElement).style.animationDelay).toBe("60ms");
    expect((entries[2] as HTMLElement).style.animationDelay).toBe("120ms");
  });

  it("dot colors match design system statuses", () => {
    const events = [
      mkEvent({ kind: "submitted", title: "Completed", status: "completed" }),
      mkEvent({ kind: "executing", title: "Current", status: "current", timestamp: "2026-02-13T12:00:01.000Z" }),
      mkEvent({ kind: "denied", title: "Error", status: "error", timestamp: "2026-02-13T12:00:02.000Z" }),
      mkEvent({ kind: "approval_requested", title: "Warning", status: "warning", timestamp: "2026-02-13T12:00:03.000Z" }),
    ];
    renderTimeline({ events });

    const dots = container.querySelectorAll(".rounded-full.border-2");
    expect(dots).toHaveLength(4);
    expect((dots[0] as HTMLElement).className).toContain("bg-emerald-500");
    expect((dots[1] as HTMLElement).className).toContain("bg-[hsl(var(--accent))]");
    expect((dots[2] as HTMLElement).className).toContain("bg-red-500");
    expect((dots[3] as HTMLElement).className).toContain("bg-amber-500");
  });
});
