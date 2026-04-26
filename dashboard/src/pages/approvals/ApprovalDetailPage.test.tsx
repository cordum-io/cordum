import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import type { BlastRadius, ApprovalContext, PriorApproval, ApprovalPolicySnapshot } from "@/api/types";
import {
  formatTimeRemaining,
  isBlastRadiusEmpty,
  blastRadiusCount,
  isShortcutSuppressed,
  APPROVAL_SHORTCUTS,
  PriorOutcomeBadge,
} from "./ApprovalDetailPage";

// ---------------------------------------------------------------------------
// Helper: build minimal ApprovalContext for testing
// ---------------------------------------------------------------------------

function makeBlastRadius(overrides?: Partial<BlastRadius>): BlastRadius {
  return {
    systems: [],
    namespaces: [],
    resources: [],
    scopeDescription: "",
    ...overrides,
  };
}

function makePriorApproval(overrides?: Partial<PriorApproval>): PriorApproval {
  return {
    jobId: "job-prior-1",
    topic: "deploy.prod",
    tenant: "acme",
    decision: "approve",
    resolvedBy: "admin@acme.com",
    resolvedAt: 1700000000000,
    wasApproved: true,
    ...overrides,
  };
}

function makePolicySnapshot(
  overrides?: Partial<ApprovalPolicySnapshot>,
): ApprovalPolicySnapshot {
  return {
    ruleCount: 1,
    matchedRule: {
      id: "rule-deploy-limit",
      description: "deployment exceeds threshold",
      decision: "REQUIRE_APPROVAL",
      constraintsSummary: "max_retries=3",
    },
    policyVersion: "snap-v1",
    ...overrides,
  };
}

function makeContext(overrides?: Partial<ApprovalContext>): ApprovalContext {
  return {
    approval: { job_id: "job-1", state: "APPROVAL_REQUIRED" },
    blastRadius: makeBlastRadius(),
    priorApprovals: [],
    rollbackHint: "",
    policySnapshotSummary: makePolicySnapshot(),
    timeRemainingMs: null,
    constraints: null,
    ...overrides,
  };
}

// ---------------------------------------------------------------------------
// formatTimeRemaining
// ---------------------------------------------------------------------------

describe("formatTimeRemaining", () => {
  it("returns '0s' for zero or negative values", () => {
    expect(formatTimeRemaining(0)).toBe("0s");
    expect(formatTimeRemaining(-500)).toBe("0s");
  });

  it("formats seconds only", () => {
    expect(formatTimeRemaining(5000)).toBe("5s");
    expect(formatTimeRemaining(45000)).toBe("45s");
  });

  it("formats minutes and seconds", () => {
    expect(formatTimeRemaining(90_000)).toBe("1m 30s");
    expect(formatTimeRemaining(300_000)).toBe("5m 0s");
  });

  it("formats hours and minutes", () => {
    expect(formatTimeRemaining(3_600_000)).toBe("1h 0m");
    expect(formatTimeRemaining(5_400_000)).toBe("1h 30m");
  });
});

// ---------------------------------------------------------------------------
// isBlastRadiusEmpty / blastRadiusCount
// ---------------------------------------------------------------------------

describe("blast radius helpers", () => {
  it("empty when all arrays are empty", () => {
    expect(isBlastRadiusEmpty(makeBlastRadius())).toBe(true);
    expect(blastRadiusCount(makeBlastRadius())).toBe(0);
  });

  it("not empty when systems present", () => {
    const br = makeBlastRadius({ systems: ["api-gateway"] });
    expect(isBlastRadiusEmpty(br)).toBe(false);
    expect(blastRadiusCount(br)).toBe(1);
  });

  it("not empty when namespaces present", () => {
    const br = makeBlastRadius({ namespaces: ["staging", "preview"] });
    expect(isBlastRadiusEmpty(br)).toBe(false);
    expect(blastRadiusCount(br)).toBe(2);
  });

  it("not empty when resources present", () => {
    const br = makeBlastRadius({ resources: ["pod-1"] });
    expect(isBlastRadiusEmpty(br)).toBe(false);
    expect(blastRadiusCount(br)).toBe(1);
  });

  it("sums across all categories", () => {
    const br = makeBlastRadius({
      systems: ["a", "b"],
      namespaces: ["c"],
      resources: ["d", "e", "f"],
    });
    expect(blastRadiusCount(br)).toBe(6);
  });
});

// ---------------------------------------------------------------------------
// isShortcutSuppressed
// ---------------------------------------------------------------------------

describe("isShortcutSuppressed", () => {
  it("returns false for null target", () => {
    expect(isShortcutSuppressed(null)).toBe(false);
  });

  it("returns true for input element", () => {
    const input = document.createElement("input");
    expect(isShortcutSuppressed(input)).toBe(true);
  });

  it("returns true for textarea element", () => {
    const textarea = document.createElement("textarea");
    expect(isShortcutSuppressed(textarea)).toBe(true);
  });

  it("returns true for select element", () => {
    const select = document.createElement("select");
    expect(isShortcutSuppressed(select)).toBe(true);
  });

  it("returns true for contentEditable element", () => {
    const div = document.createElement("div");
    div.contentEditable = "true";
    expect(isShortcutSuppressed(div)).toBe(true);
  });

  it("returns false for regular div", () => {
    const div = document.createElement("div");
    expect(isShortcutSuppressed(div)).toBe(false);
  });

  it("returns false for button", () => {
    const button = document.createElement("button");
    expect(isShortcutSuppressed(button)).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// APPROVAL_SHORTCUTS constant
// ---------------------------------------------------------------------------

describe("APPROVAL_SHORTCUTS", () => {
  it("defines expected shortcut keys", () => {
    expect(APPROVAL_SHORTCUTS.approve).toBe("a");
    expect(APPROVAL_SHORTCUTS.reject).toBe("r");
    expect(APPROVAL_SHORTCUTS.close).toBe("Escape");
  });
});

// ---------------------------------------------------------------------------
// Context data shape validation
// ---------------------------------------------------------------------------

describe("ApprovalContext shape", () => {
  it("makeContext produces valid defaults", () => {
    const ctx = makeContext();
    expect(ctx.approval).toBeDefined();
    expect(ctx.blastRadius.systems).toEqual([]);
    expect(ctx.priorApprovals).toEqual([]);
    expect(ctx.rollbackHint).toBe("");
    expect(ctx.policySnapshotSummary.ruleCount).toBe(1);
    expect(ctx.timeRemainingMs).toBeNull();
    expect(ctx.constraints).toBeNull();
  });

  it("empty sections produce correct display state", () => {
    const ctx = makeContext();
    // Blast radius should be empty.
    expect(isBlastRadiusEmpty(ctx.blastRadius)).toBe(true);
    // No prior approvals.
    expect(ctx.priorApprovals).toHaveLength(0);
    // No rollback hint.
    expect(ctx.rollbackHint).toBe("");
  });

  it("populated sections produce correct display state", () => {
    const ctx = makeContext({
      blastRadius: makeBlastRadius({
        systems: ["api", "db"],
        namespaces: ["production"],
        resources: ["deploy/api"],
      }),
      priorApprovals: [
        makePriorApproval({ wasApproved: true }),
        makePriorApproval({ jobId: "job-prior-2", wasApproved: false }),
      ],
      rollbackHint: "kubectl rollout undo deployment/api",
      timeRemainingMs: 120_000,
    });

    expect(isBlastRadiusEmpty(ctx.blastRadius)).toBe(false);
    expect(blastRadiusCount(ctx.blastRadius)).toBe(4);
    expect(ctx.priorApprovals).toHaveLength(2);
    expect(ctx.priorApprovals[0].wasApproved).toBe(true);
    expect(ctx.priorApprovals[1].wasApproved).toBe(false);
    expect(ctx.rollbackHint).toBe("kubectl rollout undo deployment/api");
    expect(ctx.timeRemainingMs).toBe(120_000);
    expect(formatTimeRemaining(ctx.timeRemainingMs!)).toBe("2m 0s");
  });

  it("policy snapshot summary with no rule sets ruleCount to 0", () => {
    const pss = makePolicySnapshot({
      ruleCount: 0,
      matchedRule: { id: "", description: "", decision: "", constraintsSummary: "" },
    });
    expect(pss.ruleCount).toBe(0);
    expect(pss.matchedRule.id).toBe("");
  });

  it("prior approval wasApproved correctly distinguishes outcomes", () => {
    const approved = makePriorApproval({ wasApproved: true, decision: "approve" });
    const rejected = makePriorApproval({ wasApproved: false, decision: "reject" });
    expect(approved.wasApproved).toBe(true);
    expect(rejected.wasApproved).toBe(false);
  });
});

describe("PriorOutcomeBadge", () => {
  function renderBadge(decision: string, wasApproved: boolean) {
    return render(<PriorOutcomeBadge decision={decision} wasApproved={wasApproved} />)
      .container.textContent?.trim() ?? "";
  }

  it.each([
    ["approve", true, "Approved"],
    ["approved", false, "Approved"],
    ["reject", true, "Rejected"],
    ["rejected", true, "Rejected"],
    ["deny", true, "Rejected"],
    ["expire", true, "Expired"],
    ["expired", false, "Expired"],
    ["invalidate", true, "Invalidated"],
    ["invalidated", false, "Invalidated"],
    ["repair", true, "Repaired"],
    ["repaired", false, "Repaired"],
  ])("decision %s is authoritative regardless of wasApproved=%s", (decision, wasApproved, expected) => {
    expect(renderBadge(decision, wasApproved)).toBe(expected);
  });

  it("falls back to legacy boolean when decision is empty: approved", () => {
    expect(renderBadge("", true)).toBe("Approved");
  });

  it("falls back to legacy boolean when decision is empty: rejected (not Unknown)", () => {
    // Regression: previously the empty-decision + wasApproved=false branch
    // showed "Unknown", losing the legacy "Rejected" outcome.
    expect(renderBadge("", false)).toBe("Rejected");
  });

  it("renders unknown decision verbatim without overriding via wasApproved", () => {
    // Regression: previously a non-empty unrecognized decision combined with
    // wasApproved=true rendered as "Approved", letting the boolean override
    // the authoritative decision string.
    expect(renderBadge("frobnicate", true)).toBe("frobnicate");
    expect(renderBadge("frobnicate", false)).toBe("frobnicate");
  });
});
