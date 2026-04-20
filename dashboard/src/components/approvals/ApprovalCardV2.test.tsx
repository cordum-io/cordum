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
import type { Approval } from "../../api/types";

// ---------------------------------------------------------------------------
// Mock state
// ---------------------------------------------------------------------------

const { mockPresenceMap, mockAssignmentMap } = vi.hoisted(() => ({
  mockPresenceMap: new Map<string, { actor: string; since: number }>(),
  mockAssignmentMap: new Map<string, string>(),
}));

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("react-router-dom", () => ({
  Link: ({ to, children }: { to: string; children: React.ReactNode }) =>
    React.createElement("a", { href: to }, children),
}));

vi.mock("../ui/Badge", () => ({
  Badge: ({ children, variant }: { children: React.ReactNode; variant?: string }) =>
    React.createElement("span", { "data-testid": `badge-${variant ?? "default"}` }, children),
}));

vi.mock("../ui/Button", () => ({
  Button: ({
    children,
    onClick,
    disabled,
  }: {
    children: React.ReactNode;
    onClick?: () => void;
    disabled?: boolean;
  }) => React.createElement("button", { onClick, disabled }, children),
}));

vi.mock("../ui/Card", () => ({
  Card: ({ children, className }: { children: React.ReactNode; className?: string }) =>
    React.createElement("div", { "data-testid": "card", className }, children),
}));

vi.mock("../ui/Textarea", () => ({
  Textarea: (props: React.TextareaHTMLAttributes<HTMLTextAreaElement>) =>
    React.createElement("textarea", props),
}));

vi.mock("../../lib/utils", () => ({
  cn: (...args: unknown[]) => args.filter(Boolean).join(" "),
}));

vi.mock("../../api/transform", () => ({
  computeUrgencyLevel: () => "fresh" as const,
}));

vi.mock("../../state/config", () => ({
  useConfigStore: (selector: (s: { approvalSlaMs: number }) => unknown) =>
    selector({ approvalSlaMs: 900_000 }),
  isSlaBreach: () => false,
  slaRemainingMs: () => 600_000,
}));

vi.mock("../../state/events", () => ({
  useEventStore: (selector: (s: {
    approvalPresence: Map<string, { actor: string; since: number }>;
    approvalAssignments: Map<string, string>;
  }) => unknown) =>
    selector({
      approvalPresence: mockPresenceMap,
      approvalAssignments: mockAssignmentMap,
    }),
}));

vi.mock("../../lib/logger", () => ({
  logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

vi.mock("../../hooks/usePermission", () => ({
  usePermission: () => ({ canApprove: true, canReject: true }),
}));

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;
interface CardProps {
  approval: Approval;
  onApprove: (id: string, comment?: string) => void;
  onReject: (id: string, reason: string) => void;
  onReview: (id: string) => void;
  selected?: boolean;
  onToggleSelect?: (id: string) => void;
}

let ApprovalCardV2: React.ComponentType<CardProps>;

function makeApproval(overrides: Partial<Approval> = {}): Approval {
  return {
    id: "apr-001",
    jobId: "job-abc12345",
    status: "pending",
    requestedAt: new Date().toISOString(),
    topic: "deploy.production",
    ...overrides,
  };
}

beforeEach(async () => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);

  mockPresenceMap.clear();
  mockAssignmentMap.clear();
  vi.clearAllMocks();

  const mod = await import("./ApprovalCardV2");
  ApprovalCardV2 = mod.ApprovalCardV2;
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render(props: Partial<CardProps> = {}) {
  const defaultProps = {
    approval: makeApproval(),
    onApprove: vi.fn(),
    onReject: vi.fn(),
    onReview: vi.fn(),
  };
  const merged = { ...defaultProps, ...props };
  act(() => {
    root.render(React.createElement(ApprovalCardV2, merged));
  });
  return merged;
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("ApprovalCardV2", () => {
  it("renders the approval job ID in the fallback summary", () => {
    const approval = makeApproval({ jobId: "job-xyz99999", topic: undefined, capabilities: [] });
    render({ approval });
    // Fallback summary uses first 8 chars of jobId
    expect(container.textContent).toContain("job-xyz9");
  });

  it("renders the topic in the fallback summary", () => {
    const approval = makeApproval({ topic: "deploy.staging", capabilities: [] });
    render({ approval });
    expect(container.textContent).toContain("deploy.staging");
  });

  it("shows urgency badge", () => {
    render();
    // computeUrgencyLevel is mocked to return "fresh", so urgency label "Fresh" is rendered
    const badge = container.querySelector("[data-testid='badge-success']");
    expect(badge).toBeTruthy();
    expect(badge?.textContent).toBe("Fresh");
  });

  it("shows approve, reject, and review buttons", () => {
    render();
    const buttons = container.querySelectorAll("button");
    const texts = Array.from(buttons).map((b) => b.textContent);
    expect(texts).toContain("Approve");
    expect(texts).toContain("Reject");
    expect(texts).toContain("Review");
  });

  it("calls onReview when review button is clicked", () => {
    const onReview = vi.fn();
    const approval = makeApproval({ id: "apr-review-test" });
    render({ approval, onReview });

    const reviewBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent === "Review",
    )!;
    act(() => reviewBtn.click());
    expect(onReview).toHaveBeenCalledWith("apr-review-test");
  });

  it("shows inline confirm panel when approve is clicked and calls onApprove on confirm", () => {
    const onApprove = vi.fn();
    const approval = makeApproval({ id: "apr-approve-test" });
    render({ approval, onApprove });

    // Click approve to enter confirmation mode
    const approveBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent === "Approve",
    )!;
    act(() => approveBtn.click());

    // Should now show "Confirm Approve" and "Cancel"
    const confirmBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent === "Confirm Approve",
    )!;
    expect(confirmBtn).toBeTruthy();

    act(() => confirmBtn.click());
    expect(onApprove).toHaveBeenCalledWith("apr-approve-test", undefined);
  });

  it("shows inline reject panel and requires reason before confirming", () => {
    const onReject = vi.fn();
    const approval = makeApproval({ id: "apr-reject-test" });
    render({ approval, onReject });

    // Click reject to enter confirmation mode
    const rejectBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent === "Reject",
    )!;
    act(() => rejectBtn.click());

    // Confirm reject button should be disabled when reason is empty
    const confirmRejectBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent === "Confirm Reject",
    )!;
    expect(confirmRejectBtn).toBeTruthy();
    expect(confirmRejectBtn.disabled).toBe(true);

    // Should show required message
    expect(container.textContent).toContain("A reason is required to reject");
  });

  it("hides checkbox column when onToggleSelect is not provided", () => {
    render({ onToggleSelect: undefined });
    const checkbox = container.querySelector("input[type='checkbox']");
    expect(checkbox).toBeFalsy();
  });

  it("shows checkbox when onToggleSelect is provided", () => {
    const onToggleSelect = vi.fn();
    const approval = makeApproval({ id: "apr-select" });
    render({ approval, onToggleSelect, selected: false });

    const checkbox = container.querySelector("input[type='checkbox']") as HTMLInputElement;
    expect(checkbox).toBeTruthy();
    expect(checkbox.checked).toBe(false);

    act(() => checkbox.click());
    expect(onToggleSelect).toHaveBeenCalledWith("apr-select");
  });

  it("shows matched rule when policyRule is present", () => {
    const approval = makeApproval({ policyRule: "block-external-deploy" });
    render({ approval });
    expect(container.textContent).toContain("Rule:");
    expect(container.textContent).toContain("block-external-deploy");
  });

  it("shows reason alongside policy rule when both present", () => {
    const approval = makeApproval({
      policyRule: "prod-write-block",
      reason: "Elevated risk detected",
    });
    render({ approval });
    expect(container.textContent).toContain("prod-write-block");
    expect(container.textContent).toContain("Elevated risk detected");
  });

  it("shows risk tags when present", () => {
    const approval = makeApproval({ riskTags: ["financial", "production"] });
    render({ approval });
    expect(container.textContent).toContain("financial");
    expect(container.textContent).toContain("production");
  });

  it("shows capabilities as badges when present", () => {
    const approval = makeApproval({ capabilities: ["code-exec", "network-access"] });
    render({ approval });
    expect(container.textContent).toContain("code-exec");
    expect(container.textContent).toContain("network-access");
  });

  it("shows humanSummary when provided instead of fallback", () => {
    const approval = makeApproval({ humanSummary: "Agent requests production deployment" });
    render({ approval });
    expect(container.textContent).toContain("Agent requests production deployment");
  });

  it("shows workflow context link when workflowContext is present", () => {
    const approval = makeApproval({
      workflowContext: {
        workflowId: "wf-1234567890ab",
        runId: "run-1",
        stepIndex: 2,
        totalSteps: 5,
        stepName: "deploy",
      },
    });
    render({ approval });
    expect(container.textContent).toContain("Workflow:");
    // .slice(0, 12) produces "wf-123456789"
    expect(container.textContent).toContain("wf-123456789");
    expect(container.textContent).toContain("step 3/5");
    expect(container.textContent).toContain("(deploy)");

    const link = container.querySelector("a[href='/workflows/wf-1234567890ab']");
    expect(link).toBeTruthy();
  });
});
